package jobsmemory

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/frostgrove/vv/jobs"
)

var ErrClosed = errors.New("jobsmemory: closed")

type Limits struct {
	MaxRecords int
	MaxBytes   int64
}

func DefaultLimits() Limits {
	return Limits{MaxRecords: 10_000, MaxBytes: 256 << 20}
}

type Clock interface {
	Now() time.Time
}

type Option interface {
	apply(*settings) error
}

type optionFunc func(*settings) error

func (option optionFunc) apply(settings *settings) error {
	return option(settings)
}

type settings struct {
	clock     Clock
	backendID jobs.BackendID
}

func WithClock(clock Clock) Option {
	return optionFunc(func(settings *settings) error {
		if nilValue(clock) {
			return fmt.Errorf("%w: clock is nil", jobs.ErrInvalid)
		}
		settings.clock = clock
		return nil
	})
}

func WithBackendID(id jobs.BackendID) Option {
	return optionFunc(func(settings *settings) error {
		if id.IsZero() {
			return fmt.Errorf("%w: backend id is zero", jobs.ErrInvalid)
		}
		settings.backendID = id
		return nil
	})
}

type Backend struct {
	mu          sync.Mutex
	description jobs.BackendDescription
	clock       Clock
	limits      Limits
	entries     map[jobs.InvocationID]*entry
	intents     map[jobs.IntentKey]jobs.InvocationID
	bytes       int64
	sequence    uint64
	leaseEpoch  uint64
	closed      bool
}

type entry struct {
	invocation jobs.Invocation
	record     jobs.DeliveryRecord
	size       int
	readyAt    time.Time
	sequence   uint64
	lease      *lease
	intents    []jobs.IntentKey
	excluded   exclusion
}

type lease struct {
	reference   jobs.LeaseRef
	incarnation jobs.WorkerIncarnation
	expiresAt   time.Time
}

type exclusion struct {
	binding jobs.BindingName
	build   jobs.BuildID
}

type Stats struct {
	Records int
	Bytes   int64
	Limits  Limits
	Leased  int
	Closed  bool
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

var (
	_ jobs.Sender         = (*Backend)(nil)
	_ jobs.DeliveryDriver = (*Backend)(nil)
)

func New(limits Limits, options ...Option) (*Backend, error) {
	if limits.MaxRecords < 1 || limits.MaxBytes < int64(jobs.MaxDeliveryRecordBytes) {
		return nil, fmt.Errorf("%w: memory limits", jobs.ErrInvalid)
	}
	settings := settings{clock: systemClock{}}
	for index, option := range options {
		if nilValue(option) {
			return nil, fmt.Errorf("%w: option %d is nil", jobs.ErrInvalid, index)
		}
		if err := option.apply(&settings); err != nil {
			return nil, fmt.Errorf("option %d: %w", index, err)
		}
	}
	if settings.backendID.IsZero() {
		var raw [jobs.BackendIDBytes]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, jobs.ErrEntropy
		}
		var err error
		settings.backendID, err = jobs.BackendIDFromBytes(raw)
		if err != nil {
			return nil, jobs.ErrEntropy
		}
	}
	durability, err := jobs.NewDurabilityProfile(jobs.AckBeforePersistence, jobs.AcknowledgedLossPossible, jobs.FailureSet{})
	if err != nil {
		return nil, err
	}
	description, err := jobs.NewBackendDescription(settings.backendID, durability, jobs.Capabilities{
		Priority:     true,
		Debounce:     true,
		Scheduled:    true,
		AttemptTrace: true,
	})
	if err != nil {
		return nil, err
	}
	return &Backend{
		description: description,
		clock:       settings.clock,
		limits:      limits,
		entries:     make(map[jobs.InvocationID]*entry),
		intents:     make(map[jobs.IntentKey]jobs.InvocationID),
	}, nil
}

func NewDefault(options ...Option) (*Backend, error) {
	return New(DefaultLimits(), options...)
}

func (backend *Backend) Description() jobs.BackendDescription {
	if backend == nil {
		return jobs.BackendDescription{}
	}
	return backend.description
}

func (backend *Backend) Stats() Stats {
	if backend == nil {
		return Stats{Closed: true}
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	leased := 0
	for _, item := range backend.entries {
		if item.lease != nil {
			leased++
		}
	}
	return Stats{Records: len(backend.entries), Bytes: backend.bytes, Limits: backend.limits, Leased: leased, Closed: backend.closed}
}

func (backend *Backend) Reset() error {
	if backend == nil {
		return ErrClosed
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.closed {
		return ErrClosed
	}
	backend.entries = make(map[jobs.InvocationID]*entry)
	backend.intents = make(map[jobs.IntentKey]jobs.InvocationID)
	backend.bytes = 0
	return nil
}

func (backend *Backend) Close() error {
	if backend == nil {
		return nil
	}
	backend.mu.Lock()
	backend.entries = make(map[jobs.InvocationID]*entry)
	backend.intents = make(map[jobs.IntentKey]jobs.InvocationID)
	backend.bytes = 0
	backend.closed = true
	backend.mu.Unlock()
	return nil
}

func (backend *Backend) Place(ctx context.Context, placement jobs.Placement) (jobs.PlacementResult, error) {
	if err := validateContext(ctx); err != nil {
		return jobs.PlacementResult{}, err
	}
	if backend == nil || placement.IsZero() {
		return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrInvalid)
	}
	now, err := backend.now()
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return jobs.PlacementResult{}, err
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if err := backend.readyLocked(ctx); err != nil {
		return jobs.PlacementResult{}, err
	}

	existing := backend.lookupIntentLocked(placement.IntentDigests())
	switch placement.Mode() {
	case jobs.PlacementOnce:
		if existing != nil {
			outcome := jobs.PlacementConflict
			if existing.record.PayloadDigest == placement.PayloadDigest() {
				outcome = jobs.PlacementExistingSamePayload
			}
			return jobs.NewPlacementResult(existing.invocation.ID(), outcome)
		}
	case jobs.PlacementCollapse, jobs.PlacementDebounce:
		if existing != nil && existing.lease == nil && existing.invocation.State() == jobs.InvocationQueued {
			return backend.collapseLocked(placement, existing, now)
		}
	}

	if _, exists := backend.entries[placement.Candidate()]; exists {
		return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrConflict)
	}
	record, invocation, err := recordFromPlacement(placement, placement.Candidate(), now, now.Add(placement.Delay()))
	if err != nil {
		return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrInvalid)
	}
	item, err := backend.newEntryLocked(invocation, record, placement.IntentDigests().ReservationKeys())
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	backend.insertLocked(item)
	return jobs.NewPlacementResult(invocation.ID(), jobs.PlacementCreated)
}

func (backend *Backend) Enqueue(ctx context.Context, record jobs.DeliveryRecord) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if backend == nil {
		return ErrClosed
	}
	invocation, canonical, err := restoreRecord(record)
	if err != nil || invocation.State() != jobs.InvocationQueued {
		return fmt.Errorf("%w: delivery record", jobs.ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if err := backend.readyLocked(ctx); err != nil {
		return err
	}
	if _, exists := backend.entries[invocation.ID()]; exists {
		return jobs.ErrConflict
	}
	intents := []jobs.IntentKey(nil)
	if invocation.Mode() != jobs.PlacementRegular {
		intents = []jobs.IntentKey{invocation.Intent()}
		if _, exists := backend.intents[invocation.Intent()]; exists {
			return jobs.ErrConflict
		}
	}
	item, err := backend.newEntryLocked(invocation, canonical, intents)
	if err != nil {
		return err
	}
	backend.insertLocked(item)
	return nil
}

func (backend *Backend) Claim(ctx context.Context, request jobs.ClaimRequest) (jobs.ClaimBatch, error) {
	if err := validateContext(ctx); err != nil {
		return jobs.ClaimBatch{}, err
	}
	if backend == nil {
		return jobs.ClaimBatch{}, ErrClosed
	}
	now, err := backend.now()
	if err != nil {
		return jobs.ClaimBatch{}, err
	}
	targets := request.Targets()

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if err := backend.readyLocked(ctx); err != nil {
		return jobs.ClaimBatch{}, err
	}
	candidates := make([]claimCandidate, 0, request.MaxItems())
	for _, item := range backend.entries {
		if item.invocation.Namespace() != request.Namespace() || item.invocation.State() != jobs.InvocationQueued || item.readyAt.After(now) {
			continue
		}
		if item.lease != nil && (item.lease.expiresAt.After(now) || item.invocation.State() != jobs.InvocationQueued) {
			continue
		}
		for index, target := range targets {
			if targetMatches(target, item) {
				candidates = append(candidates, claimCandidate{item: item, target: index})
				break
			}
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].item.invocation.Priority() != candidates[right].item.invocation.Priority() {
			return candidates[left].item.invocation.Priority() < candidates[right].item.invocation.Priority()
		}
		if candidates[left].item.readyAt != candidates[right].item.readyAt {
			return candidates[left].item.readyAt.Before(candidates[right].item.readyAt)
		}
		return candidates[left].item.sequence < candidates[right].item.sequence
	})

	counts := make([]int, len(targets))
	claimed := make([]jobs.ClaimedDelivery, 0, request.MaxItems())
	claimedBytes := 0
	for _, candidate := range candidates {
		if len(claimed) == request.MaxItems() {
			break
		}
		target := targets[candidate.target]
		if counts[candidate.target] == target.Available() || candidate.item.size > request.MaxBytes()-claimedBytes {
			continue
		}
		leased, err := backend.issueLeaseLocked(candidate.item.invocation.ID(), request.Incarnation(), now, request.LeaseTTL())
		if err != nil {
			return jobs.ClaimBatch{}, err
		}
		delivery, err := jobs.NewClaimedDelivery(target, leased.reference, candidate.item.record)
		if err != nil {
			return jobs.ClaimBatch{}, err
		}
		candidate.item.lease = leased
		backend.releaseCollapseIntentLocked(candidate.item)
		claimed = append(claimed, delivery)
		claimedBytes += candidate.item.size
		counts[candidate.target]++
	}
	return jobs.NewClaimBatch(now, claimed)
}

type claimCandidate struct {
	item   *entry
	target int
}

func (backend *Backend) Renew(ctx context.Context, request jobs.RenewRequest) (jobs.RenewResult, error) {
	if err := validateContext(ctx); err != nil {
		return jobs.RenewResult{}, err
	}
	if backend == nil {
		return jobs.RenewResult{}, ErrClosed
	}
	now, err := backend.now()
	if err != nil {
		return jobs.RenewResult{}, err
	}
	requested := request.Leases()

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if err := backend.readyLocked(ctx); err != nil {
		return jobs.RenewResult{}, err
	}
	items := make([]jobs.LeaseRenewal, 0, len(requested))
	for _, previous := range requested {
		item := backend.entries[previous.InvocationID()]
		control := deliveryControl(item)
		if item == nil || item.lease == nil || !sameLease(item.lease.reference, previous) || !item.lease.expiresAt.After(now) || item.invocation.IsTerminal() {
			renewal, buildErr := jobs.NewLeaseRenewal(previous, jobs.LeaseRef{}, jobs.DeliveryMutationLeaseLost, control)
			if buildErr != nil {
				return jobs.RenewResult{}, buildErr
			}
			items = append(items, renewal)
			continue
		}
		current, issueErr := backend.issueLeaseLocked(item.invocation.ID(), item.lease.incarnation, now, request.LeaseTTL())
		if issueErr != nil {
			return jobs.RenewResult{}, issueErr
		}
		item.lease = current
		renewal, buildErr := jobs.NewLeaseRenewal(previous, current.reference, jobs.DeliveryMutationApplied, control)
		if buildErr != nil {
			return jobs.RenewResult{}, buildErr
		}
		items = append(items, renewal)
	}
	return jobs.NewRenewResult(now, items)
}

func (backend *Backend) Apply(ctx context.Context, request jobs.ApplyRequest) (jobs.ApplyResult, error) {
	if err := validateContext(ctx); err != nil {
		return jobs.ApplyResult{}, err
	}
	if backend == nil {
		return jobs.ApplyResult{}, ErrClosed
	}
	now, err := backend.now()
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	command := request.Command()
	leaseRef := command.Lease()

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if err := backend.readyLocked(ctx); err != nil {
		return jobs.ApplyResult{}, err
	}
	item := backend.entries[leaseRef.InvocationID()]
	if item == nil || item.lease == nil || !sameLease(item.lease.reference, leaseRef) || !item.lease.expiresAt.After(now) {
		result, buildErr := jobs.NewDeliveryCommandResult(jobs.DeliveryMutationLeaseLost, deliveryControl(item))
		if buildErr != nil {
			return jobs.ApplyResult{}, buildErr
		}
		return jobs.NewApplyResult(now, result, jobs.DeliveryApplication{})
	}
	current := item.invocation
	if command.Kind() == jobs.DeliveryCommandRejectCorrupt {
		current = jobs.Invocation{}
	}
	application, err := jobs.ApplyDeliveryCommand(current, command, now)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	control := applicationControl(command, application)
	result, err := jobs.NewDeliveryCommandResult(jobs.DeliveryMutationApplied, control)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	applyResult, err := jobs.NewApplyResult(now, result, application)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	if command.Kind() == jobs.DeliveryCommandRejectCorrupt {
		backend.removeLocked(item)
		return applyResult, nil
	}
	updated, err := recordFromInvocation(application.Invocation(), item.record)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	if delta := int64(updated.size - item.size); delta > backend.limits.MaxBytes-backend.bytes {
		return jobs.ApplyResult{}, jobs.ErrSaturated
	}
	backend.bytes += int64(updated.size - item.size)
	item.invocation = application.Invocation()
	item.record = updated.record
	item.size = updated.size
	if item.invocation.IsTerminal() {
		item.lease = nil
		item.readyAt = time.Time{}
		if item.invocation.Mode() != jobs.PlacementOnce {
			backend.removeLocked(item)
		}
		return applyResult, nil
	}
	if item.invocation.State() == jobs.InvocationQueued {
		item.readyAt = item.invocation.EligibleAt()
		item.lease = nil
		if release, ok := application.Release(); ok {
			item.readyAt = release.AvailableAt()
			item.excluded = exclusion{binding: release.ExcludedBinding(), build: release.ExcludedBuild()}
		}
	}
	return applyResult, nil
}

func (backend *Backend) Recover(ctx context.Context, request jobs.RecoverRequest) (jobs.RecoverResult, error) {
	if err := validateContext(ctx); err != nil {
		return jobs.RecoverResult{}, err
	}
	if backend == nil {
		return jobs.RecoverResult{}, ErrClosed
	}
	now, err := backend.now()
	if err != nil {
		return jobs.RecoverResult{}, err
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if err := backend.readyLocked(ctx); err != nil {
		return jobs.RecoverResult{}, err
	}
	candidates := make([]*entry, 0)
	for _, item := range backend.entries {
		if item.invocation.Namespace() != request.Namespace() || item.lease == nil {
			continue
		}
		if item.lease.incarnation == request.Incarnation() || !item.lease.expiresAt.After(now) {
			candidates = append(candidates, item)
		}
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].sequence < candidates[right].sequence })
	items := make([]jobs.RecoveredDelivery, 0, request.MaxItems())
	total := 0
	more := false
	for _, item := range candidates {
		if len(items) == request.MaxItems() || item.size > request.MaxBytes()-total {
			more = true
			break
		}
		leased, issueErr := backend.issueLeaseLocked(item.invocation.ID(), request.Incarnation(), now, request.LeaseTTL())
		if issueErr != nil {
			return jobs.RecoverResult{}, issueErr
		}
		delivery, buildErr := jobs.NewRecoveredDelivery(leased.reference, item.record)
		if buildErr != nil {
			return jobs.RecoverResult{}, buildErr
		}
		item.lease = leased
		items = append(items, delivery)
		total += item.size
	}
	return jobs.NewRecoverResult(now, items, 0, more)
}

func (backend *Backend) collapseLocked(placement jobs.Placement, item *entry, now time.Time) (jobs.PlacementResult, error) {
	eligibleAt := now.Add(placement.Delay())
	if placement.Mode() == jobs.PlacementDebounce {
		maximum := item.invocation.CreatedAt().Add(placement.MaxDelay())
		if eligibleAt.After(maximum) {
			eligibleAt = maximum
		}
	}
	record, invocation, err := recordFromPlacement(placement, item.invocation.ID(), item.invocation.CreatedAt(), eligibleAt)
	if err != nil {
		return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrInvalid)
	}
	size, err := jobs.DeliveryRecordSize(record)
	if err != nil {
		return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrTooLarge)
	}
	if int64(size-item.size) > backend.limits.MaxBytes-backend.bytes {
		return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrSaturated)
	}
	backend.unreserveLocked(item)
	backend.bytes += int64(size - item.size)
	item.invocation = invocation
	item.record = record
	item.size = size
	item.readyAt = eligibleAt
	item.excluded = exclusion{}
	item.intents = placement.IntentDigests().ReservationKeys()
	backend.reserveLocked(item)
	result, err := jobs.NewPlacementResult(invocation.ID(), jobs.PlacementCollapsed)
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	return result, nil
}

func (backend *Backend) newEntryLocked(invocation jobs.Invocation, record jobs.DeliveryRecord, intents []jobs.IntentKey) (*entry, error) {
	size, err := jobs.DeliveryRecordSize(record)
	if err != nil {
		return nil, jobs.RejectPlacement(jobs.ErrTooLarge)
	}
	if len(backend.entries) == backend.limits.MaxRecords || int64(size) > backend.limits.MaxBytes-backend.bytes {
		return nil, jobs.RejectPlacement(jobs.ErrSaturated)
	}
	backend.sequence++
	return &entry{invocation: invocation, record: record, size: size, readyAt: invocation.EligibleAt(), sequence: backend.sequence, intents: append([]jobs.IntentKey(nil), intents...)}, nil
}

func (backend *Backend) insertLocked(item *entry) {
	backend.entries[item.invocation.ID()] = item
	backend.bytes += int64(item.size)
	backend.reserveLocked(item)
}

func (backend *Backend) removeLocked(item *entry) {
	if item == nil {
		return
	}
	delete(backend.entries, item.invocation.ID())
	backend.bytes -= int64(item.size)
	backend.unreserveLocked(item)
}

func (backend *Backend) reserveLocked(item *entry) {
	for _, intent := range item.intents {
		backend.intents[intent] = item.invocation.ID()
	}
}

func (backend *Backend) unreserveLocked(item *entry) {
	for _, intent := range item.intents {
		if backend.intents[intent] == item.invocation.ID() {
			delete(backend.intents, intent)
		}
	}
}

func (backend *Backend) releaseCollapseIntentLocked(item *entry) {
	if item.invocation.Mode() != jobs.PlacementCollapse && item.invocation.Mode() != jobs.PlacementDebounce {
		return
	}
	backend.unreserveLocked(item)
	item.intents = nil
}

func (backend *Backend) lookupIntentLocked(intents jobs.IntentDigests) *entry {
	for _, intent := range intents.ReadCandidates() {
		if id, ok := backend.intents[intent]; ok {
			if item := backend.entries[id]; item != nil {
				return item
			}
			delete(backend.intents, intent)
		}
	}
	return nil
}

func (backend *Backend) issueLeaseLocked(invocation jobs.InvocationID, incarnation jobs.WorkerIncarnation, now time.Time, ttl time.Duration) (*lease, error) {
	backend.leaseEpoch++
	var token [24]byte
	incarnationBytes := incarnation.Bytes()
	copy(token[:16], incarnationBytes[:])
	binary.BigEndian.PutUint64(token[16:], backend.leaseEpoch)
	reference, err := jobs.NewLeaseRef(backend.description.ID(), invocation, token[:])
	if err != nil {
		return nil, err
	}
	expiresAt := now.Add(ttl).Round(0).UTC()
	if expiresAt.IsZero() || expiresAt.Year() < 1 || expiresAt.Year() > 9999 {
		return nil, jobs.ErrInvalid
	}
	return &lease{reference: reference, incarnation: incarnation, expiresAt: expiresAt}, nil
}

func (backend *Backend) readyLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if backend.closed {
		return ErrClosed
	}
	return nil
}

func (backend *Backend) now() (time.Time, error) {
	if backend == nil || nilValue(backend.clock) {
		return time.Time{}, jobs.ErrInvalid
	}
	now := backend.clock.Now().Round(0).UTC()
	if now.IsZero() || now.Year() < 1 || now.Year() > 9999 {
		return time.Time{}, jobs.ErrInvalid
	}
	return now, nil
}

func targetMatches(target jobs.ClaimTarget, item *entry) bool {
	if target.Definition() != item.invocation.Definition() {
		return false
	}
	if item.excluded.binding == target.Binding() && item.excluded.build == target.Build() {
		return false
	}
	for _, revision := range target.SupportedRevisions() {
		if revision.Codec() == item.record.Payload.Codec && revision.Version() == item.record.Payload.Version {
			return true
		}
	}
	return false
}

func sameLease(left, right jobs.LeaseRef) bool {
	return left.Backend() == right.Backend() && left.InvocationID() == right.InvocationID() && bytes.Equal(left.DriverToken(), right.DriverToken())
}

func deliveryControl(item *entry) jobs.DeliveryControlStatus {
	if item == nil {
		return jobs.DeliveryControlNone
	}
	switch item.invocation.State() {
	case jobs.InvocationCancelRequested, jobs.InvocationCancelled:
		return jobs.DeliveryControlCancelRequested
	case jobs.InvocationTerminated:
		return jobs.DeliveryControlTerminated
	default:
		return jobs.DeliveryControlNone
	}
}

func applicationControl(command jobs.DeliveryCommand, application jobs.DeliveryApplication) jobs.DeliveryControlStatus {
	if application.Invocation().State() == jobs.InvocationTerminated {
		return jobs.DeliveryControlTerminated
	}
	if command.Kind() == jobs.DeliveryCommandProgress && application.Invocation().State() == jobs.InvocationCancelRequested {
		return jobs.DeliveryControlCancelRequested
	}
	if command.Kind() == jobs.DeliveryCommandFinishAttempt {
		if attempt, ok := application.Attempt(); ok && attempt.Disposition().Kind() == jobs.DispositionCancelled && attempt.Disposition().Reason() == jobs.ReasonCancelRequested {
			return jobs.DeliveryControlCancelRequested
		}
	}
	return jobs.DeliveryControlNone
}

func validateContext(ctx context.Context) error {
	if nilValue(ctx) {
		return jobs.ErrInvalid
	}
	return ctx.Err()
}

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
