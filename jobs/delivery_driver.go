package jobs

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

type DeliveryDriver interface {
	Description() BackendDescription
	Claim(context.Context, ClaimRequest) (ClaimBatch, error)
	Renew(context.Context, RenewRequest) (RenewResult, error)
	Apply(context.Context, ApplyRequest) (ApplyResult, error)
	Recover(context.Context, RecoverRequest) (RecoverResult, error)
}

func ValidateDeliveryDriver(driver DeliveryDriver) (description BackendDescription, err error) {
	if nilInterface(driver) {
		return BackendDescription{}, invalid("delivery driver")
	}
	defer func() {
		if recover() != nil {
			description = BackendDescription{}
			err = invalid("delivery driver description")
		}
	}()
	description = driver.Description()
	if !description.valid() {
		return BackendDescription{}, invalid("delivery driver description")
	}
	return description, nil
}

type PayloadRevision struct {
	codec   CodecID
	version SchemaVersion
}

func NewPayloadRevision(codec CodecID, version SchemaVersion) (PayloadRevision, error) {
	if !codec.valid() || version.IsZero() {
		return PayloadRevision{}, invalid("payload revision")
	}
	return PayloadRevision{codec: codec, version: version}, nil
}

func (r PayloadRevision) Codec() CodecID                 { return r.codec }
func (r PayloadRevision) Version() SchemaVersion         { return r.version }
func (r PayloadRevision) IsZero() bool                   { return r.codec.IsZero() || r.version.IsZero() }
func (PayloadRevision) String() string                   { return "[job payload revision]" }
func (r PayloadRevision) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, r.String()) }
func (r PayloadRevision) LogValue() slog.Value           { return slog.StringValue(r.String()) }
func (PayloadRevision) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: payload revision cannot be serialized", ErrUnsupported)
}
func (r PayloadRevision) valid() bool { return r.codec.valid() && !r.version.IsZero() }

type ClaimTargetSpec struct {
	Definition         Name
	Binding            BindingName
	Build              BuildID
	SupportedRevisions []PayloadRevision
	Available          int
}

func (ClaimTargetSpec) String() string { return "[job claim target spec]" }
func (s ClaimTargetSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}

type ClaimTarget struct {
	definition Name
	binding    BindingName
	build      BuildID
	revisions  []PayloadRevision
	available  int
}

func NewClaimTarget(spec ClaimTargetSpec) (ClaimTarget, error) {
	if len(spec.SupportedRevisions) == 0 {
		return ClaimTarget{}, invalid("claim target revisions")
	}
	if len(spec.SupportedRevisions) > MaxSupportedRevisions {
		return ClaimTarget{}, tooLarge("claim target revisions")
	}
	revisions := append([]PayloadRevision(nil), spec.SupportedRevisions...)
	sort.Slice(revisions, func(left, right int) bool {
		if revisions[left].version != revisions[right].version {
			return revisions[left].version < revisions[right].version
		}
		return revisions[left].codec.String() < revisions[right].codec.String()
	})
	target := ClaimTarget{
		definition: spec.Definition,
		binding:    spec.Binding,
		build:      spec.Build,
		revisions:  revisions,
		available:  spec.Available,
	}
	if !target.valid() {
		if len(spec.SupportedRevisions) > MaxSupportedRevisions || spec.Available > MaxBindingConcurrency {
			return ClaimTarget{}, tooLarge("claim target")
		}
		return ClaimTarget{}, invalid("claim target")
	}
	return target, nil
}

func (t ClaimTarget) Definition() Name     { return t.definition }
func (t ClaimTarget) Binding() BindingName { return t.binding }
func (t ClaimTarget) Build() BuildID       { return t.build }
func (t ClaimTarget) SupportedRevisions() []PayloadRevision {
	return append([]PayloadRevision(nil), t.revisions...)
}
func (t ClaimTarget) Available() int { return t.available }
func (ClaimTarget) String() string   { return "[job claim target]" }
func (t ClaimTarget) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, t.String())
}
func (t ClaimTarget) LogValue() slog.Value { return slog.StringValue(t.String()) }
func (ClaimTarget) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: claim target cannot be serialized", ErrUnsupported)
}
func (t ClaimTarget) valid() bool {
	if !t.definition.valid() || !t.binding.valid() || !t.build.valid() || len(t.revisions) == 0 || len(t.revisions) > MaxSupportedRevisions || t.available < 1 || t.available > MaxBindingConcurrency {
		return false
	}
	for index, revision := range t.revisions {
		if !revision.valid() {
			return false
		}
		if index > 0 && t.revisions[index-1].version >= revision.version {
			return false
		}
	}
	return true
}
func (t ClaimTarget) supports(codec CodecID, version SchemaVersion) bool {
	for _, revision := range t.revisions {
		if revision.codec == codec && revision.version == version {
			return true
		}
	}
	return false
}
func (t ClaimTarget) same(other ClaimTarget) bool {
	if t.definition != other.definition || t.binding != other.binding || t.build != other.build || t.available != other.available || len(t.revisions) != len(other.revisions) {
		return false
	}
	for index := range t.revisions {
		if t.revisions[index] != other.revisions[index] {
			return false
		}
	}
	return true
}
func cloneClaimTarget(target ClaimTarget) ClaimTarget {
	target.revisions = append([]PayloadRevision(nil), target.revisions...)
	return target
}

type ClaimRequestSpec struct {
	Namespace Namespace
	Targets   []ClaimTarget
	MaxItems  int
	MaxBytes  int
	LeaseTTL  time.Duration
}

func (ClaimRequestSpec) String() string { return "[job claim request spec]" }
func (s ClaimRequestSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}

type ClaimRequest struct {
	namespace Namespace
	targets   []ClaimTarget
	maxItems  int
	maxBytes  int
	leaseTTL  time.Duration
}

func NewClaimRequest(spec ClaimRequestSpec) (ClaimRequest, error) {
	if len(spec.Targets) == 0 {
		return ClaimRequest{}, invalid("claim request targets")
	}
	if len(spec.Targets) > MaxDefinitions {
		return ClaimRequest{}, tooLarge("claim request targets")
	}
	request := ClaimRequest{namespace: spec.Namespace, targets: cloneClaimTargets(spec.Targets), maxItems: spec.MaxItems, maxBytes: spec.MaxBytes, leaseTTL: spec.LeaseTTL}
	if err := validateClaimRequest(request); err != nil {
		return ClaimRequest{}, err
	}
	return request, nil
}

func (r ClaimRequest) Namespace() Namespace    { return r.namespace }
func (r ClaimRequest) Targets() []ClaimTarget  { return cloneClaimTargets(r.targets) }
func (r ClaimRequest) MaxItems() int           { return r.maxItems }
func (r ClaimRequest) MaxBytes() int           { return r.maxBytes }
func (r ClaimRequest) LeaseTTL() time.Duration { return r.leaseTTL }
func (ClaimRequest) String() string            { return "[job claim request]" }
func (r ClaimRequest) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (r ClaimRequest) LogValue() slog.Value { return slog.StringValue(r.String()) }
func (ClaimRequest) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: claim request cannot be serialized", ErrUnsupported)
}

func validateClaimRequest(request ClaimRequest) error {
	if !request.namespace.valid() || len(request.targets) == 0 || len(request.targets) > MaxDefinitions || request.maxItems < 1 || request.maxItems > MaxClaimItems || request.maxBytes < MaxDeliveryRecordBytes || request.maxBytes > MaxClaimBytes || !validLeaseTTL(request.leaseTTL) {
		if len(request.targets) > MaxDefinitions || request.maxItems > MaxClaimItems || request.maxBytes > MaxClaimBytes {
			return tooLarge("claim request")
		}
		return invalid("claim request")
	}
	definitions := make(map[Name]struct{}, len(request.targets))
	bindings := make(map[BindingName]struct{}, len(request.targets))
	total := 0
	for _, target := range request.targets {
		if !target.valid() {
			return invalid("claim request target")
		}
		if _, exists := definitions[target.definition]; exists {
			return fmt.Errorf("%w: duplicate claim definition", ErrConflict)
		}
		if _, exists := bindings[target.binding]; exists {
			return fmt.Errorf("%w: duplicate claim binding", ErrConflict)
		}
		if target.available > MaxWorkerConcurrency-total {
			return tooLarge("claim target availability")
		}
		total += target.available
		definitions[target.definition] = struct{}{}
		bindings[target.binding] = struct{}{}
	}
	if request.maxItems > total {
		return invalid("claim item budget exceeds target availability")
	}
	return nil
}

func cloneClaimTargets(targets []ClaimTarget) []ClaimTarget {
	result := make([]ClaimTarget, len(targets))
	for index := range targets {
		result[index] = cloneClaimTarget(targets[index])
	}
	return result
}

type ClaimedDelivery struct {
	target ClaimTarget
	lease  LeaseRef
	record *deliveryRecordEnvelope
}

func NewClaimedDelivery(target ClaimTarget, lease LeaseRef, record DeliveryRecord) (ClaimedDelivery, error) {
	return newClaimedDelivery(target, lease, record, false)
}

func TakeClaimedDelivery(target ClaimTarget, lease LeaseRef, record *DeliveryRecord) (ClaimedDelivery, error) {
	if record == nil {
		return ClaimedDelivery{}, invalid("claimed delivery record")
	}
	delivery, err := newClaimedDelivery(target, lease, *record, true)
	if err != nil {
		return ClaimedDelivery{}, err
	}
	*record = DeliveryRecord{}
	return delivery, nil
}

func newClaimedDelivery(target ClaimTarget, lease LeaseRef, record DeliveryRecord, take bool) (ClaimedDelivery, error) {
	if !target.valid() || !lease.valid() {
		return ClaimedDelivery{}, invalid("claimed delivery")
	}
	if _, err := DeliveryRecordSize(record); err != nil {
		return ClaimedDelivery{}, err
	}
	return ClaimedDelivery{target: cloneClaimTarget(target), lease: cloneLeaseRef(lease), record: newDeliveryRecordEnvelope(record, take)}, nil
}

func (d ClaimedDelivery) Target() ClaimTarget { return cloneClaimTarget(d.target) }
func (d ClaimedDelivery) Lease() LeaseRef     { return cloneLeaseRef(d.lease) }
func (d ClaimedDelivery) Record() DeliveryRecord {
	if d.record == nil {
		return DeliveryRecord{}
	}
	return d.record.cloneSnapshot()
}
func (ClaimedDelivery) String() string { return "[job claimed delivery]" }
func (d ClaimedDelivery) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
func (d ClaimedDelivery) LogValue() slog.Value { return slog.StringValue(d.String()) }
func (ClaimedDelivery) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: claimed delivery cannot be serialized", ErrUnsupported)
}

type ClaimBatch struct {
	observedAt time.Time
	items      []ClaimedDelivery
}

func NewClaimBatch(observedAt time.Time, items []ClaimedDelivery) (ClaimBatch, error) {
	observedAt, err := requiredTime(observedAt, "claim observation time")
	if err != nil {
		return ClaimBatch{}, err
	}
	if len(items) > MaxClaimItems {
		return ClaimBatch{}, tooLarge("claim batch")
	}
	return ClaimBatch{observedAt: observedAt, items: cloneClaimedDeliveries(items)}, nil
}

func (b ClaimBatch) ObservedAt() time.Time          { return b.observedAt }
func (b ClaimBatch) Items() []ClaimedDelivery       { return cloneClaimedDeliveries(b.items) }
func (b ClaimBatch) Len() int                       { return len(b.items) }
func (ClaimBatch) String() string                   { return "[job claim batch]" }
func (b ClaimBatch) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, b.String()) }
func (b ClaimBatch) LogValue() slog.Value           { return slog.StringValue(b.String()) }
func (ClaimBatch) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: claim batch cannot be serialized", ErrUnsupported)
}

func ValidateClaimBatch(description BackendDescription, request ClaimRequest, batch ClaimBatch) (ClaimBatch, error) {
	if !description.valid() {
		return ClaimBatch{}, invalid("claim backend description")
	}
	if err := validateClaimRequest(request); err != nil {
		return ClaimBatch{}, err
	}
	if !canonicalRequiredDeliveryTime(batch.observedAt) || len(batch.items) > request.maxItems {
		return ClaimBatch{}, driverContractError("claim", invalid("batch bounds or observation time"))
	}
	sizes := make([]int, len(batch.items))
	total := 0
	for index := range batch.items {
		size, err := batch.items[index].recordSize()
		if err != nil {
			return ClaimBatch{}, driverContractError("claim", err)
		}
		if size > request.maxBytes-total {
			return ClaimBatch{}, driverContractError("claim", tooLarge("aggregate delivery bytes"))
		}
		sizes[index] = size
		total += size
	}
	seenInvocations := make(map[InvocationID]struct{}, len(batch.items))
	seenLeases := make(map[[32]byte]struct{}, len(batch.items))
	targetCounts := make([]int, len(request.targets))
	for index := range batch.items {
		item := batch.items[index]
		targetIndex := request.targetIndex(item.target)
		if sizes[index] < 1 || item.record == nil || !item.target.valid() || !item.lease.valid() || item.lease.backend != description.ID() || targetIndex < 0 {
			return ClaimBatch{}, driverContractError("claim", invalid("delivery item"))
		}
		targetCounts[targetIndex]++
		if targetCounts[targetIndex] > request.targets[targetIndex].available {
			return ClaimBatch{}, driverContractError("claim", tooLarge("target delivery count"))
		}
		if _, exists := seenInvocations[item.lease.invocation]; exists {
			return ClaimBatch{}, driverContractError("claim", fmt.Errorf("%w: duplicate invocation", ErrConflict))
		}
		if _, exists := seenLeases[item.lease.binding]; exists {
			return ClaimBatch{}, driverContractError("claim", fmt.Errorf("%w: duplicate lease", ErrConflict))
		}
		seenInvocations[item.lease.invocation] = struct{}{}
		seenLeases[item.lease.binding] = struct{}{}
	}
	return ClaimBatch{observedAt: batch.observedAt, items: cloneClaimedDeliveries(batch.items)}, nil
}

func (r ClaimRequest) targetIndex(target ClaimTarget) int {
	for index, candidate := range r.targets {
		if candidate.same(target) {
			return index
		}
	}
	return -1
}

func cloneClaimedDeliveries(items []ClaimedDelivery) []ClaimedDelivery {
	return append([]ClaimedDelivery(nil), items...)
}

type RenewRequest struct {
	leases   []LeaseRef
	leaseTTL time.Duration
}

func NewRenewRequest(leases []LeaseRef, leaseTTL time.Duration) (RenewRequest, error) {
	if len(leases) == 0 || !validLeaseTTL(leaseTTL) {
		return RenewRequest{}, invalid("renew request")
	}
	if len(leases) > MaxClaimItems {
		return RenewRequest{}, tooLarge("renew request")
	}
	request := RenewRequest{leases: cloneLeaseRefs(leases), leaseTTL: leaseTTL}
	if err := validateRenewRequest(request); err != nil {
		return RenewRequest{}, err
	}
	return request, nil
}

func (r RenewRequest) Leases() []LeaseRef             { return cloneLeaseRefs(r.leases) }
func (r RenewRequest) LeaseTTL() time.Duration        { return r.leaseTTL }
func (RenewRequest) String() string                   { return "[job renew request]" }
func (r RenewRequest) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, r.String()) }
func (r RenewRequest) LogValue() slog.Value           { return slog.StringValue(r.String()) }
func (RenewRequest) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: renew request cannot be serialized", ErrUnsupported)
}

func validateRenewRequest(request RenewRequest) error {
	if len(request.leases) == 0 || len(request.leases) > MaxClaimItems || !validLeaseTTL(request.leaseTTL) {
		if len(request.leases) > MaxClaimItems {
			return tooLarge("renew request")
		}
		return invalid("renew request")
	}
	backend := request.leases[0].backend
	seen := make(map[[32]byte]struct{}, len(request.leases))
	seenInvocations := make(map[InvocationID]struct{}, len(request.leases))
	for _, lease := range request.leases {
		if !lease.valid() || lease.backend != backend {
			return invalid("renew lease")
		}
		if _, exists := seen[lease.binding]; exists {
			return fmt.Errorf("%w: duplicate renewal lease", ErrConflict)
		}
		if _, exists := seenInvocations[lease.invocation]; exists {
			return fmt.Errorf("%w: duplicate renewal invocation", ErrConflict)
		}
		seen[lease.binding] = struct{}{}
		seenInvocations[lease.invocation] = struct{}{}
	}
	return nil
}

type LeaseRenewal struct {
	previous LeaseRef
	current  LeaseRef
	mutation DeliveryMutationStatus
	control  DeliveryControlStatus
}

func NewLeaseRenewal(previous LeaseRef, current LeaseRef, mutation DeliveryMutationStatus, control DeliveryControlStatus) (LeaseRenewal, error) {
	renewal := LeaseRenewal{previous: cloneLeaseRef(previous), current: cloneLeaseRef(current), mutation: mutation, control: control}
	if !renewal.valid() {
		return LeaseRenewal{}, invalid("lease renewal")
	}
	return renewal, nil
}

func (r LeaseRenewal) Previous() LeaseRef               { return cloneLeaseRef(r.previous) }
func (r LeaseRenewal) Current() LeaseRef                { return cloneLeaseRef(r.current) }
func (r LeaseRenewal) Mutation() DeliveryMutationStatus { return r.mutation }
func (r LeaseRenewal) Control() DeliveryControlStatus   { return r.control }
func (LeaseRenewal) String() string                     { return "[job lease renewal]" }
func (r LeaseRenewal) Format(state fmt.State, _ rune)   { _, _ = fmt.Fprint(state, r.String()) }
func (r LeaseRenewal) LogValue() slog.Value             { return slog.StringValue(r.String()) }
func (LeaseRenewal) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: lease renewal cannot be serialized", ErrUnsupported)
}
func (r LeaseRenewal) valid() bool {
	if !r.previous.valid() || !r.mutation.Valid() || !r.control.Valid() {
		return false
	}
	if r.mutation == DeliveryMutationApplied {
		return r.control != DeliveryControlTerminated && r.current.valid() && r.current.backend == r.previous.backend && r.current.invocation == r.previous.invocation
	}
	return r.current.IsZero() && (r.mutation != DeliveryMutationAmbiguous || r.control == DeliveryControlNone)
}

type RenewResult struct {
	observedAt time.Time
	items      []LeaseRenewal
}

func NewRenewResult(observedAt time.Time, items []LeaseRenewal) (RenewResult, error) {
	observedAt, err := requiredTime(observedAt, "renew observation time")
	if err != nil {
		return RenewResult{}, err
	}
	if len(items) > MaxClaimItems {
		return RenewResult{}, tooLarge("renew result")
	}
	for _, item := range items {
		if !item.valid() {
			return RenewResult{}, invalid("renew result item")
		}
	}
	return RenewResult{observedAt: observedAt, items: cloneLeaseRenewals(items)}, nil
}

func (r RenewResult) ObservedAt() time.Time          { return r.observedAt }
func (r RenewResult) Items() []LeaseRenewal          { return cloneLeaseRenewals(r.items) }
func (r RenewResult) Len() int                       { return len(r.items) }
func (RenewResult) String() string                   { return "[job renew result]" }
func (r RenewResult) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, r.String()) }
func (r RenewResult) LogValue() slog.Value           { return slog.StringValue(r.String()) }
func (RenewResult) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: renew result cannot be serialized", ErrUnsupported)
}

func ValidateRenewResult(description BackendDescription, request RenewRequest, result RenewResult) (RenewResult, error) {
	if !description.valid() {
		return RenewResult{}, invalid("renew backend description")
	}
	if err := validateRenewRequest(request); err != nil {
		return RenewResult{}, err
	}
	if !canonicalRequiredDeliveryTime(result.observedAt) || len(result.items) != len(request.leases) {
		return RenewResult{}, driverContractError("renew", invalid("result cardinality or observation time"))
	}
	seenCurrent := make(map[[32]byte]struct{}, len(result.items))
	for index, item := range result.items {
		if !item.valid() || !sameLeaseRef(item.previous, request.leases[index]) || item.previous.backend != description.ID() {
			return RenewResult{}, driverContractError("renew", invalid("ordered renewal result"))
		}
		if item.mutation == DeliveryMutationApplied {
			if _, exists := seenCurrent[item.current.binding]; exists {
				return RenewResult{}, driverContractError("renew", fmt.Errorf("%w: duplicate rotated lease", ErrConflict))
			}
			seenCurrent[item.current.binding] = struct{}{}
		}
	}
	return RenewResult{observedAt: result.observedAt, items: cloneLeaseRenewals(result.items)}, nil
}

func cloneLeaseRefs(values []LeaseRef) []LeaseRef {
	result := make([]LeaseRef, len(values))
	for index := range values {
		result[index] = cloneLeaseRef(values[index])
	}
	return result
}

func cloneLeaseRenewals(values []LeaseRenewal) []LeaseRenewal {
	result := make([]LeaseRenewal, len(values))
	for index := range values {
		result[index] = values[index]
		result[index].previous = cloneLeaseRef(values[index].previous)
		result[index].current = cloneLeaseRef(values[index].current)
	}
	return result
}

type ApplyRequest struct{ command DeliveryCommand }

func NewApplyRequest(command DeliveryCommand) (ApplyRequest, error) {
	command, err := validateDeliveryCommand(command)
	if err != nil {
		return ApplyRequest{}, err
	}
	return ApplyRequest{command: command}, nil
}

func (r ApplyRequest) Command() DeliveryCommand       { return cloneDeliveryCommand(r.command) }
func (ApplyRequest) String() string                   { return "[job apply request]" }
func (r ApplyRequest) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, r.String()) }
func (r ApplyRequest) LogValue() slog.Value           { return slog.StringValue(r.String()) }
func (ApplyRequest) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: apply request cannot be serialized", ErrUnsupported)
}

type ApplyResult struct {
	observedAt  time.Time
	result      DeliveryCommandResult
	application DeliveryApplication
	validated   bool
}

func NewApplyResult(observedAt time.Time, result DeliveryCommandResult, application DeliveryApplication) (ApplyResult, error) {
	if result.IsZero() || !result.mutation.Valid() || !result.control.Valid() {
		return ApplyResult{}, invalid("apply result")
	}
	if result.mutation == DeliveryMutationAmbiguous {
		if !observedAt.IsZero() {
			return ApplyResult{}, invalid("ambiguous apply observation time")
		}
	} else {
		var err error
		observedAt, err = requiredTime(observedAt, "apply observation time")
		if err != nil {
			return ApplyResult{}, err
		}
	}
	if (result.mutation == DeliveryMutationApplied) == application.IsZero() {
		return ApplyResult{}, invalid("apply result application certainty")
	}
	if result.mutation == DeliveryMutationApplied && result.control == DeliveryControlTerminated && !deadlineTerminationApplication(application) {
		return ApplyResult{}, invalid("applied terminated result")
	}
	if result.mutation == DeliveryMutationAmbiguous && result.control != DeliveryControlNone {
		return ApplyResult{}, invalid("ambiguous apply control status")
	}
	return ApplyResult{observedAt: observedAt, result: result, application: application}, nil
}

func (r ApplyResult) ObservedAt() time.Time            { return r.observedAt }
func (r ApplyResult) Result() DeliveryCommandResult    { return r.result }
func (r ApplyResult) Application() DeliveryApplication { return r.application }
func (r ApplyResult) HandlerReady() bool {
	if !r.validated || r.result.mutation != DeliveryMutationApplied || r.result.control != DeliveryControlNone || r.application.kind != DeliveryCommandBeginAttempt || r.application.invocation.IsZero() || r.application.invocation.State() != InvocationRunning {
		return false
	}
	attempt, ok := r.application.Attempt()
	return ok && attempt.State() == AttemptRunning && attempt.InvocationID() == r.application.invocation.ID()
}
func (ApplyResult) String() string                   { return "[job apply result]" }
func (r ApplyResult) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, r.String()) }
func (r ApplyResult) LogValue() slog.Value           { return slog.StringValue(r.String()) }
func (ApplyResult) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: apply result cannot be serialized", ErrUnsupported)
}

func (a DeliveryApplication) IsZero() bool {
	return a.kind == 0 && a.invocation.IsZero() && a.attempt.IsZero() && !a.changed && a.release.IsZero() && a.proof == [32]byte{}
}

func ValidateApplyResult(description BackendDescription, request ApplyRequest, result ApplyResult) (ApplyResult, error) {
	command, err := validateDeliveryCommand(request.command)
	if err != nil {
		return ApplyResult{}, err
	}
	request.command = command
	if !description.valid() || request.command.lease.backend != description.ID() {
		return ApplyResult{}, invalid("apply request backend")
	}
	validObservation := result.result.mutation == DeliveryMutationAmbiguous && result.observedAt.IsZero() || result.result.mutation != DeliveryMutationAmbiguous && canonicalRequiredDeliveryTime(result.observedAt)
	if !validObservation {
		return ApplyResult{}, driverContractError("apply", invalid("observation time"))
	}
	if result.result.IsZero() || !result.result.mutation.Valid() || !result.result.control.Valid() {
		return ApplyResult{}, driverContractError("apply", invalid("command result"))
	}
	if result.result.mutation == DeliveryMutationApplied && result.result.control == DeliveryControlTerminated && !deadlineTerminationApplication(result.application) {
		return ApplyResult{}, driverContractError("apply", invalid("applied terminated result"))
	}
	if result.result.mutation != DeliveryMutationApplied {
		if !result.application.IsZero() || result.result.mutation == DeliveryMutationAmbiguous && result.result.control != DeliveryControlNone {
			return ApplyResult{}, driverContractError("apply", invalid("uncertain application"))
		}
		result.validated = true
		return result, nil
	}
	application := result.application
	if application.kind != request.command.kind || !application.matches(request.command) {
		return ApplyResult{}, driverContractError("apply", invalid("application kind"))
	}
	if result.result.control != authoritativeApplicationControl(application) {
		return ApplyResult{}, driverContractError("apply", invalid("application control status"))
	}
	if !validAppliedDeliveryApplication(request.command, application, result.observedAt) {
		return ApplyResult{}, driverContractError("apply", invalid("authoritative application"))
	}
	result.validated = true
	return result, nil
}

func authoritativeApplicationControl(application DeliveryApplication) DeliveryControlStatus {
	if deadlineTerminationApplication(application) {
		return DeliveryControlTerminated
	}
	if (application.kind == DeliveryCommandProgress || application.kind == DeliveryCommandArbitrateAttemptDeadline) && application.invocation.State() == InvocationCancelRequested {
		return DeliveryControlCancelRequested
	}
	if application.kind == DeliveryCommandFinishAttempt && application.attempt.Disposition().kind == DispositionCancelled && application.attempt.Disposition().reason == ReasonCancelRequested {
		return DeliveryControlCancelRequested
	}
	return DeliveryControlNone
}

func deadlineTerminationApplication(application DeliveryApplication) bool {
	attempt, ok := application.Attempt()
	return application.kind == DeliveryCommandArbitrateAttemptDeadline && application.changed && ok && application.invocation.State() == InvocationTerminated && attempt.State() == AttemptFinished && attempt.Disposition() == cancellationTerminatedDisposition()
}

func validAppliedDeliveryApplication(command DeliveryCommand, application DeliveryApplication, observedAt time.Time) bool {
	if command.kind == DeliveryCommandRejectCorrupt {
		return application.invocation.IsZero() && application.attempt.IsZero() && application.changed && application.release.IsZero()
	}
	if application.invocation.IsZero() || application.invocation.ID() != command.lease.invocation {
		return false
	}
	switch command.kind {
	case DeliveryCommandBeginAttempt:
		return validBeginApplication(command, application, observedAt)
	case DeliveryCommandProgress:
		return validProgressApplication(application, observedAt)
	case DeliveryCommandFinishAttempt:
		return validFinishApplication(command, application, observedAt)
	case DeliveryCommandDeferDelivery:
		return validDeferApplication(command, application, observedAt)
	case DeliveryCommandFinishDelivery:
		return validFinishDeliveryApplication(command, application, observedAt)
	case DeliveryCommandReleaseUnchanged:
		return validReleaseApplication(command, application, observedAt)
	case DeliveryCommandArbitrateAttemptDeadline:
		return validDeadlineApplication(command, application, observedAt)
	default:
		return false
	}
}

func validBeginApplication(command DeliveryCommand, application DeliveryApplication, observedAt time.Time) bool {
	if !application.changed || !application.release.IsZero() {
		return false
	}
	attempt, hasAttempt := application.Attempt()
	outcome := application.invocation.Outcome()
	if !hasAttempt {
		reason := application.invocation.deadlineReason(observedAt)
		return application.invocation.State() == InvocationDead && reason != ReasonNone && outcome.kind == InvocationOutcomeDeliveryTerminal && outcome.occurredAt == observedAt && outcome.terminalReason == reason && outcome.reason == reason && outcome.failure.IsZero() && outcome.availableAt.IsZero()
	}
	if application.invocation.State() != InvocationRunning || attempt.State() != AttemptRunning || attempt.InvocationID() != application.invocation.ID() || attempt.Binding() != command.binding || attempt.Build() != command.build || attempt.StartedAt() != observedAt || !latestAttemptIs(application.invocation, attempt) {
		return false
	}
	return validActiveAttemptOutcome(outcome, attempt) && validRunningAttemptSnapshot(application.invocation, attempt)
}

func validProgressApplication(application DeliveryApplication, observedAt time.Time) bool {
	attempt, hasAttempt := application.Attempt()
	if !hasAttempt || !application.release.IsZero() || attempt.State() != AttemptRunning || attempt.InvocationID() != application.invocation.ID() || attempt.ProgressedAt() != observedAt || !latestAttemptIs(application.invocation, attempt) || !validRunningAttemptSnapshot(application.invocation, attempt) {
		return false
	}
	outcome := application.invocation.Outcome()
	switch application.invocation.State() {
	case InvocationRunning:
		return validActiveAttemptOutcome(outcome, attempt)
	case InvocationCancelRequested:
		return application.invocation.CancelRequestedAt() == outcome.occurredAt && validCancelRequestedLedger(application.invocation.history, attempt, observedAt)
	default:
		return false
	}
}

func validFinishApplication(command DeliveryCommand, application DeliveryApplication, observedAt time.Time) bool {
	attempt, outcome, ok := finishedApplicationSnapshot(application, observedAt)
	if !ok || !validApplicationDeadlineRetryDelay(application, attempt, command.deadlineDelay) {
		return false
	}
	effective := attempt.Disposition()
	cancelled := effective.kind == DispositionCancelled && effective.reason == ReasonCancelRequested && effective.retryAfter == 0 && effective.retryCost == RetryCostNone && effective.failure.IsZero()
	timedOut := effective.kind == DispositionRetry && (effective.reason == ReasonAttemptTimeout || effective.reason == ReasonProgressTimeout) && effective.retryAfter == 0 && effective.retryCost == RetryCostCharged && effective.failure.IsZero()
	if cancelled {
		return application.invocation.State() == InvocationCancelled && outcome.availableAt.IsZero() && outcome.terminalReason == ReasonNone && validCancellationPredecessor(application.invocation, attempt, observedAt)
	}
	if !validActiveAttemptPredecessor(application.invocation, attempt) {
		return false
	}
	if timedOut {
		return validTimedOutApplication(command, application, attempt, outcome, observedAt)
	}
	if effective != command.disposition {
		return false
	}
	reason := application.invocation.attemptFinishDeadlineReason(attempt, effective, observedAt)
	if reason == ReasonAttemptTimeout || reason == ReasonProgressTimeout {
		return false
	}
	if reason == ReasonMaxElapsed {
		return application.invocation.State() == InvocationDead && outcome.terminalReason == ReasonMaxElapsed && outcome.availableAt.IsZero()
	}
	return validProposedFinishApplication(command, application, effective, outcome, observedAt)
}

func validDeadlineApplication(command DeliveryCommand, application DeliveryApplication, observedAt time.Time) bool {
	attempt, hasAttempt := application.Attempt()
	if !hasAttempt || !application.release.IsZero() || attempt.InvocationID() != application.invocation.ID() || !latestAttemptIs(application.invocation, attempt) || !validApplicationDeadlineRetryDelay(application, attempt, command.deadlineDelay) {
		return false
	}
	deadline, _ := attemptRuntimeDeadline(attempt)
	if attempt.State() == AttemptRunning {
		if application.changed || !observedAt.Before(deadline) || observedAt.Before(application.invocation.latestOccurredAt()) || !validRunningAttemptSnapshot(application.invocation, attempt) {
			return false
		}
		outcome := application.invocation.Outcome()
		switch application.invocation.State() {
		case InvocationRunning:
			return application.invocation.CancelRequestedAt().IsZero() && validActiveAttemptOutcome(outcome, attempt)
		case InvocationCancelRequested:
			return application.invocation.CancelRequestedAt() == outcome.occurredAt && validCancelRequestedLedger(application.invocation.history, attempt, observedAt)
		default:
			return false
		}
	}
	finished, outcome, ok := finishedApplicationSnapshot(application, observedAt)
	if !ok || finished != attempt || observedAt.Before(deadline) {
		return false
	}
	effective := attempt.Disposition()
	if effective == cancellationTerminatedDisposition() {
		return application.invocation.State() == InvocationTerminated && outcome.availableAt.IsZero() && outcome.terminalReason == ReasonNone && validCancellationPredecessor(application.invocation, attempt, observedAt)
	}
	if effective.kind != DispositionRetry || effective.reason != ReasonAttemptTimeout && effective.reason != ReasonProgressTimeout || effective.retryAfter != 0 || effective.retryCost != RetryCostCharged || !effective.failure.IsZero() {
		return false
	}
	return validActiveAttemptPredecessor(application.invocation, attempt) && validTimedOutApplication(command, application, attempt, outcome, observedAt)
}

func finishedApplicationSnapshot(application DeliveryApplication, observedAt time.Time) (Attempt, InvocationOutcome, bool) {
	attempt, hasAttempt := application.Attempt()
	expectedFinishedAt := time.Time{}
	if application.invocation.State().Terminal() {
		expectedFinishedAt = observedAt
	}
	if !application.changed || !hasAttempt || !application.release.IsZero() || attempt.State() != AttemptFinished || attempt.FinishedAt() != observedAt || attempt.ProgressedAt().After(observedAt) || !attempt.Disposition().valid() || !attempt.Disposition().allowedForAttempt() || !validAttemptSchedule(application.invocation, attempt) || application.invocation.FinishedAt() != expectedFinishedAt || !application.invocation.CancelRequestedAt().IsZero() || !latestAttemptIs(application.invocation, attempt) {
		return Attempt{}, InvocationOutcome{}, false
	}
	outcome := application.invocation.Outcome()
	if !outcome.valid() || outcome.kind != InvocationOutcomeAttemptFinished || outcome.attempt != attempt.Ordinal() || outcome.disposition != attempt.Disposition() || outcome.occurredAt != observedAt {
		return Attempt{}, InvocationOutcome{}, false
	}
	return attempt, outcome, true
}

func validCancelRequestedLedger(current *invocationOutcomeLedger, attempt Attempt, observedAt time.Time) bool {
	if current == nil || current.previous == nil || current.value.occurredAt.After(observedAt) {
		return false
	}
	cancelled, err := CancelRequestedOutcome(attempt.Ordinal(), current.value.occurredAt)
	if err != nil || current.value != cancelled {
		return false
	}
	active, err := ActiveAttemptOutcome(attempt.Ordinal(), attempt.StartedAt())
	return err == nil && current.previous.value == active
}

func validCancellationPredecessor(invocation Invocation, attempt Attempt, observedAt time.Time) bool {
	return invocation.history != nil && validCancelRequestedLedger(invocation.history.previous, attempt, observedAt)
}

func validActiveAttemptPredecessor(invocation Invocation, attempt Attempt) bool {
	if invocation.history == nil || invocation.history.previous == nil {
		return false
	}
	return validActiveAttemptOutcome(invocation.history.previous.value, attempt)
}

func validActiveAttemptOutcome(outcome InvocationOutcome, attempt Attempt) bool {
	active, err := ActiveAttemptOutcome(attempt.Ordinal(), attempt.StartedAt())
	return err == nil && outcome == active
}

func validApplicationDeadlineRetryDelay(application DeliveryApplication, attempt Attempt, delay time.Duration) bool {
	retrySpent := application.invocation.RetrySpent().Value()
	if application.invocation.State() == InvocationQueued && attempt.Disposition().kind == DispositionRetry && attempt.Disposition().retryCost == RetryCostCharged {
		if retrySpent == 0 {
			return false
		}
		retrySpent--
	}
	return validTimeoutRetryDelay(application.invocation.Policy().Backoff(), retrySpent, delay)
}

func validTimedOutApplication(command DeliveryCommand, application DeliveryApplication, attempt Attempt, outcome InvocationOutcome, observedAt time.Time) bool {
	deadline, reason := attemptRuntimeDeadline(attempt)
	if observedAt.Before(deadline) || attempt.Disposition().reason != reason {
		return false
	}
	maxElapsedAt := application.invocation.MaxElapsedAt()
	switch application.invocation.State() {
	case InvocationQueued:
		if !observedAt.Before(maxElapsedAt) || command.deadlineDelay >= maxElapsedAt.Sub(observedAt) || outcome.terminalReason != ReasonNone || attempt.Ordinal().Value() == MaxAttemptOrdinal || application.invocation.RetrySpent().Value() > application.invocation.Policy().RetryLimit().Value() {
			return false
		}
		expected, err := requiredTime(observedAt.Add(command.deadlineDelay), "attempt timeout availability")
		return err == nil && outcome.availableAt == expected
	case InvocationDead:
		switch outcome.terminalReason {
		case ReasonMaxElapsed:
			if !observedAt.Before(maxElapsedAt) {
				return outcome.availableAt.IsZero()
			}
			return attempt.Ordinal().Value() != MaxAttemptOrdinal && application.invocation.RetrySpent().Value() < application.invocation.Policy().RetryLimit().Value() && command.deadlineDelay >= maxElapsedAt.Sub(observedAt) && outcome.availableAt == maxElapsedAt
		case ReasonRetryExhausted, ReasonAttemptsExhausted:
			if !observedAt.Before(maxElapsedAt) || !outcome.availableAt.IsZero() {
				return false
			}
			if outcome.terminalReason == ReasonAttemptsExhausted {
				return attempt.Ordinal().Value() == MaxAttemptOrdinal
			}
			return attempt.Ordinal().Value() != MaxAttemptOrdinal && application.invocation.RetrySpent().Value() == application.invocation.Policy().RetryLimit().Value()
		default:
			return false
		}
	default:
		return false
	}
}

func validProposedFinishApplication(command DeliveryCommand, application DeliveryApplication, effective Disposition, outcome InvocationOutcome, observedAt time.Time) bool {
	switch effective.kind {
	case DispositionSucceeded:
		return application.invocation.State() == InvocationSucceeded && outcome.terminalReason == ReasonNone && outcome.availableAt.IsZero()
	case DispositionPermanentFailure:
		return application.invocation.State() == InvocationFailed && outcome.terminalReason == ReasonNone && outcome.availableAt.IsZero()
	case DispositionDiscard:
		return application.invocation.State() == InvocationDiscarded && outcome.terminalReason == ReasonNone && outcome.availableAt.IsZero()
	case DispositionQuarantine:
		return application.invocation.State() == InvocationQuarantined && outcome.terminalReason == ReasonNone && outcome.availableAt.IsZero()
	case DispositionRetry, DispositionDeferred:
		expected, err := relativeDeliveryTime(observedAt, command.delay)
		if err != nil {
			return false
		}
		if application.invocation.State() == InvocationQueued {
			return outcome.terminalReason == ReasonNone && outcome.availableAt == expected
		}
		if application.invocation.State() != InvocationDead {
			return false
		}
		if effective.kind == DispositionRetry {
			switch outcome.terminalReason {
			case ReasonMaxElapsed:
				return outcome.availableAt == expected
			case ReasonRetryExhausted, ReasonAttemptsExhausted:
				return outcome.availableAt.IsZero()
			default:
				return false
			}
		}
		switch outcome.terminalReason {
		case ReasonMaxElapsed:
			return outcome.availableAt == expected
		case ReasonDeferralsExhausted, ReasonAttemptsExhausted:
			return outcome.availableAt.IsZero()
		default:
			return false
		}
	default:
		return false
	}
}

func validDeferApplication(command DeliveryCommand, application DeliveryApplication, observedAt time.Time) bool {
	if !application.changed || !application.attempt.IsZero() || !application.release.IsZero() {
		return false
	}
	outcome := application.invocation.Outcome()
	if outcome.reason != command.reason || outcome.failure != command.failure || outcome.occurredAt != observedAt {
		return false
	}
	expected, err := relativeDeliveryTime(outcome.occurredAt, command.delay)
	if err != nil {
		return false
	}
	if outcome.kind == InvocationOutcomeDeliveryDeferred {
		return application.invocation.State() == InvocationQueued && outcome.availableAt == expected && outcome.terminalReason == ReasonNone
	}
	if outcome.kind != InvocationOutcomeDeliveryTerminal || application.invocation.State() != InvocationDead {
		return false
	}
	if outcome.terminalReason != ReasonMaxElapsed && outcome.terminalReason != ReasonStartBefore && outcome.terminalReason != ReasonDeferralsExhausted && outcome.terminalReason != ReasonAttemptsExhausted {
		return false
	}
	return outcome.availableAt.IsZero() || outcome.availableAt == expected
}

func validFinishDeliveryApplication(command DeliveryCommand, application DeliveryApplication, observedAt time.Time) bool {
	if !application.changed || !application.attempt.IsZero() || !application.release.IsZero() {
		return false
	}
	outcome := application.invocation.Outcome()
	if outcome.kind != InvocationOutcomeDeliveryTerminal || outcome.reason != command.reason || outcome.failure != command.failure || outcome.occurredAt != observedAt || !outcome.availableAt.IsZero() {
		return false
	}
	if application.invocation.State() == command.state {
		return outcome.terminalState == command.state && outcome.terminalReason == command.reason
	}
	return application.invocation.State() == InvocationDead && outcome.terminalState == InvocationDead && (outcome.terminalReason == ReasonMaxElapsed || outcome.terminalReason == ReasonStartBefore)
}

func validReleaseApplication(command DeliveryCommand, application DeliveryApplication, observedAt time.Time) bool {
	if !application.attempt.IsZero() {
		return false
	}
	if application.changed {
		if !application.release.IsZero() || application.invocation.State() != InvocationDead {
			return false
		}
		outcome := application.invocation.Outcome()
		if outcome.kind != InvocationOutcomeDeliveryTerminal || outcome.terminalState != InvocationDead || outcome.reason != command.reason || !outcome.failure.IsZero() || outcome.occurredAt != observedAt || outcome.terminalReason != ReasonMaxElapsed && outcome.terminalReason != ReasonStartBefore {
			return false
		}
		observedReason := application.invocation.deadlineReason(outcome.occurredAt)
		if observedReason != ReasonNone {
			return observedReason == outcome.terminalReason && outcome.availableAt.IsZero()
		}
		expected, err := releaseAvailability(application.invocation, outcome.occurredAt, command.delay)
		return err == nil && outcome.availableAt == expected && application.invocation.deadlineReason(expected) == outcome.terminalReason
	}
	release, ok := application.Release()
	if !ok || application.invocation.State() != InvocationQueued || release.binding != command.binding || release.build != command.build || release.reason != command.reason || !canonicalRequiredDeliveryTime(release.availableAt) {
		return false
	}
	expected, err := releaseAvailability(application.invocation, observedAt, command.delay)
	return err == nil && release.availableAt == expected && !observedAt.Before(application.invocation.readyAt()) && application.invocation.deadlineReason(observedAt) == ReasonNone && application.invocation.deadlineReason(release.availableAt) == ReasonNone
}

func validRunningAttemptSnapshot(invocation Invocation, attempt Attempt) bool {
	if !invocation.FinishedAt().IsZero() || invocation.State() == InvocationRunning && !invocation.CancelRequestedAt().IsZero() || invocation.State() == InvocationCancelRequested && invocation.CancelRequestedAt().IsZero() || invocation.State() != InvocationRunning && invocation.State() != InvocationCancelRequested || attempt.State() != AttemptRunning || !attempt.FinishedAt().IsZero() || !attempt.Disposition().IsZero() {
		return false
	}
	return validAttemptSchedule(invocation, attempt)
}

func validAttemptSchedule(invocation Invocation, attempt Attempt) bool {
	if attempt.InvocationID() != invocation.ID() || attempt.Ordinal().IsZero() || !attempt.Ordinal().valid() || attempt.Ordinal() != invocation.AttemptOrdinal() || !attempt.Binding().valid() || !attempt.Build().valid() || !invocation.RetrySpent().valid() || invocation.RetrySpent().Value() > invocation.Policy().RetryLimit().Value() || !invocation.HandlerDeferrals().valid() || invocation.HandlerDeferrals().Value() > invocation.Policy().HandlerDeferralLimit().Value() || !invocation.DeliveryDeferrals().valid() || invocation.DeliveryDeferrals().Value() > invocation.Policy().DeliveryDeferralLimit().Value() || invocation.attempts == nil || invocation.attempts.length != int(attempt.Ordinal().Value()) || !canonicalRequiredDeliveryTime(attempt.StartedAt()) || attempt.StartedAt().Before(invocation.EligibleAt()) || !attempt.StartedAt().Before(invocation.MaxElapsedAt()) || !canonicalRequiredDeliveryTime(attempt.Deadline()) || !attempt.StartedAt().Before(attempt.Deadline()) || attempt.Deadline().After(invocation.MaxElapsedAt()) || attempt.Ordinal().Value() == 1 && !invocation.StartBefore().IsZero() && !attempt.StartedAt().Before(invocation.StartBefore()) {
		return false
	}
	expectedDeadline, err := requiredTime(attempt.StartedAt().Add(invocation.Policy().AttemptTimeout()), "attempt deadline")
	if err != nil {
		return false
	}
	if expectedDeadline.After(invocation.MaxElapsedAt()) {
		expectedDeadline = invocation.MaxElapsedAt()
	}
	if attempt.Deadline() != expectedDeadline {
		return false
	}
	if invocation.Policy().ProgressTimeout() == 0 {
		return attempt.ProgressedAt().IsZero() && attempt.ProgressDeadline().IsZero()
	}
	if !canonicalRequiredDeliveryTime(attempt.ProgressedAt()) || !canonicalRequiredDeliveryTime(attempt.ProgressDeadline()) || attempt.ProgressedAt().Before(attempt.StartedAt()) || !attempt.ProgressedAt().Before(attempt.Deadline()) || attempt.ProgressDeadline().After(attempt.Deadline()) {
		return false
	}
	expected, err := requiredTime(attempt.ProgressedAt().Add(invocation.Policy().ProgressTimeout()), "progress deadline")
	if err != nil {
		return false
	}
	if expected.After(attempt.Deadline()) {
		expected = attempt.Deadline()
	}
	return attempt.ProgressDeadline() == expected
}

func latestAttemptIs(invocation Invocation, attempt Attempt) bool {
	return invocation.attempts != nil && invocation.attempts.value == attempt
}

type RecoverRequest struct {
	namespace Namespace
	maxItems  int
	maxBytes  int
	leaseTTL  time.Duration
}

func NewRecoverRequest(namespace Namespace, maxItems, maxBytes int, leaseTTL time.Duration) (RecoverRequest, error) {
	if !namespace.valid() || maxItems < 1 || maxBytes < MaxDeliveryRecordBytes || !validLeaseTTL(leaseTTL) {
		return RecoverRequest{}, invalid("recover request")
	}
	if maxItems > MaxReclaimBatch || maxBytes > MaxClaimBytes {
		return RecoverRequest{}, tooLarge("recover request")
	}
	return RecoverRequest{namespace: namespace, maxItems: maxItems, maxBytes: maxBytes, leaseTTL: leaseTTL}, nil
}

func (r RecoverRequest) Namespace() Namespace    { return r.namespace }
func (r RecoverRequest) MaxItems() int           { return r.maxItems }
func (r RecoverRequest) MaxBytes() int           { return r.maxBytes }
func (r RecoverRequest) LeaseTTL() time.Duration { return r.leaseTTL }
func (RecoverRequest) String() string            { return "[job recover request]" }
func (r RecoverRequest) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (r RecoverRequest) LogValue() slog.Value { return slog.StringValue(r.String()) }
func (RecoverRequest) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: recover request cannot be serialized", ErrUnsupported)
}

type RecoveredDelivery struct {
	lease  LeaseRef
	record *deliveryRecordEnvelope
}

func NewRecoveredDelivery(lease LeaseRef, record DeliveryRecord) (RecoveredDelivery, error) {
	return newRecoveredDelivery(lease, record, false)
}

func TakeRecoveredDelivery(lease LeaseRef, record *DeliveryRecord) (RecoveredDelivery, error) {
	if record == nil {
		return RecoveredDelivery{}, invalid("recovered delivery record")
	}
	delivery, err := newRecoveredDelivery(lease, *record, true)
	if err != nil {
		return RecoveredDelivery{}, err
	}
	*record = DeliveryRecord{}
	return delivery, nil
}

func newRecoveredDelivery(lease LeaseRef, record DeliveryRecord, take bool) (RecoveredDelivery, error) {
	if !lease.valid() {
		return RecoveredDelivery{}, invalid("recovered delivery")
	}
	if _, err := DeliveryRecordSize(record); err != nil {
		return RecoveredDelivery{}, err
	}
	return RecoveredDelivery{lease: cloneLeaseRef(lease), record: newDeliveryRecordEnvelope(record, take)}, nil
}

func (d RecoveredDelivery) Lease() LeaseRef { return cloneLeaseRef(d.lease) }
func (d RecoveredDelivery) Record() DeliveryRecord {
	if d.record == nil {
		return DeliveryRecord{}
	}
	return d.record.cloneSnapshot()
}
func (RecoveredDelivery) String() string { return "[job recovered delivery]" }
func (d RecoveredDelivery) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
func (d RecoveredDelivery) LogValue() slog.Value { return slog.StringValue(d.String()) }
func (RecoveredDelivery) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: recovered delivery cannot be serialized", ErrUnsupported)
}

type RecoverResult struct {
	observedAt time.Time
	items      []RecoveredDelivery
	released   int
	more       bool
}

func NewRecoverResult(observedAt time.Time, items []RecoveredDelivery, released int, more bool) (RecoverResult, error) {
	observedAt, err := requiredTime(observedAt, "recovery observation time")
	if err != nil {
		return RecoverResult{}, err
	}
	if released < 0 || more && len(items) == 0 && released == 0 {
		return RecoverResult{}, invalid("recover result")
	}
	if len(items) > MaxReclaimBatch || released > MaxReclaimBatch-len(items) {
		return RecoverResult{}, tooLarge("recover result")
	}
	return RecoverResult{observedAt: observedAt, items: cloneRecoveredDeliveries(items), released: released, more: more}, nil
}

func (r RecoverResult) ObservedAt() time.Time      { return r.observedAt }
func (r RecoverResult) Items() []RecoveredDelivery { return cloneRecoveredDeliveries(r.items) }
func (r RecoverResult) Released() int              { return r.released }
func (r RecoverResult) More() bool                 { return r.more }
func (RecoverResult) String() string               { return "[job recover result]" }
func (r RecoverResult) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (r RecoverResult) LogValue() slog.Value { return slog.StringValue(r.String()) }
func (RecoverResult) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: recover result cannot be serialized", ErrUnsupported)
}

func ValidateRecoverResult(description BackendDescription, request RecoverRequest, result RecoverResult) (RecoverResult, error) {
	if !description.valid() {
		return RecoverResult{}, invalid("recover backend description")
	}
	if !request.namespace.valid() || request.maxItems < 1 || request.maxItems > MaxReclaimBatch || request.maxBytes < MaxDeliveryRecordBytes || request.maxBytes > MaxClaimBytes || !validLeaseTTL(request.leaseTTL) {
		return RecoverResult{}, invalid("recover request")
	}
	if !canonicalRequiredDeliveryTime(result.observedAt) || result.released < 0 || len(result.items) > request.maxItems || result.released > request.maxItems-len(result.items) || result.more && len(result.items) == 0 && result.released == 0 {
		return RecoverResult{}, driverContractError("recover", invalid("result"))
	}
	total := 0
	for index := range result.items {
		size, err := result.items[index].recordSize()
		if err != nil {
			return RecoverResult{}, driverContractError("recover", err)
		}
		if size > request.maxBytes-total {
			return RecoverResult{}, driverContractError("recover", tooLarge("aggregate delivery bytes"))
		}
		total += size
	}
	seenInvocations := make(map[InvocationID]struct{}, len(result.items))
	seenLeases := make(map[[32]byte]struct{}, len(result.items))
	for _, item := range result.items {
		if item.record == nil || !item.lease.valid() || item.lease.backend != description.ID() {
			return RecoverResult{}, driverContractError("recover", invalid("delivery item"))
		}
		if _, exists := seenInvocations[item.lease.invocation]; exists {
			return RecoverResult{}, driverContractError("recover", fmt.Errorf("%w: duplicate invocation", ErrConflict))
		}
		if _, exists := seenLeases[item.lease.binding]; exists {
			return RecoverResult{}, driverContractError("recover", fmt.Errorf("%w: duplicate lease", ErrConflict))
		}
		seenInvocations[item.lease.invocation] = struct{}{}
		seenLeases[item.lease.binding] = struct{}{}
	}
	return RecoverResult{observedAt: result.observedAt, items: cloneRecoveredDeliveries(result.items), released: result.released, more: result.more}, nil
}

func cloneRecoveredDeliveries(values []RecoveredDelivery) []RecoveredDelivery {
	return append([]RecoveredDelivery(nil), values...)
}

type deliveryRecordEnvelope struct {
	mu    sync.Mutex
	value DeliveryRecord
	taken bool
}

func newDeliveryRecordEnvelope(record DeliveryRecord, take bool) *deliveryRecordEnvelope {
	if !take {
		record = cloneDeliveryRecord(record)
	}
	record.Payload.Data = record.Payload.Data[:len(record.Payload.Data):len(record.Payload.Data)]
	return &deliveryRecordEnvelope{value: record}
}

func (d ClaimedDelivery) recordSize() (int, error) {
	if d.record == nil {
		return DeliveryRecordSize(DeliveryRecord{})
	}
	return d.record.size()
}

func (d RecoveredDelivery) recordSize() (int, error) {
	if d.record == nil {
		return DeliveryRecordSize(DeliveryRecord{})
	}
	return d.record.size()
}

func (d ClaimedDelivery) takeRecordValue() (DeliveryRecord, bool) {
	if d.record == nil {
		return DeliveryRecord{}, false
	}
	return d.record.take()
}

func (d RecoveredDelivery) takeRecordValue() (DeliveryRecord, bool) {
	if d.record == nil {
		return DeliveryRecord{}, false
	}
	return d.record.take()
}

func (e *deliveryRecordEnvelope) cloneSnapshot() DeliveryRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cloneDeliveryRecord(e.value)
}

func (e *deliveryRecordEnvelope) size() (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.taken {
		return 0, invalid("consumed delivery record")
	}
	return DeliveryRecordSize(e.value)
}

func (e *deliveryRecordEnvelope) take() (DeliveryRecord, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.taken {
		return DeliveryRecord{}, false
	}
	e.taken = true
	record := e.value
	e.value = DeliveryRecord{}
	return record, true
}

func validLeaseTTL(value time.Duration) bool {
	return value >= MinimumLeaseTTL && value <= MaximumLeaseTTL
}

func sameLeaseRef(left, right LeaseRef) bool {
	return left.backend == right.backend && left.invocation == right.invocation && left.binding == right.binding && bytes.Equal(left.token, right.token)
}

func cloneDeliveryCommand(command DeliveryCommand) DeliveryCommand {
	command.lease = cloneLeaseRef(command.lease)
	return command
}

func driverContractError(operation string, cause error) error {
	return fmt.Errorf("%w: %w: %s result: %w", ErrDriver, ErrDriverContract, operation, cause)
}
