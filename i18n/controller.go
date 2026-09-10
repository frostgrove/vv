package i18n

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrConflict         = errors.New("i18n: snapshot conflict")
	ErrPinUnavailable   = errors.New("i18n: snapshot pin unavailable")
	ErrSnapshotNotFound = errors.New("i18n: snapshot not found")
)

const (
	defaultRetainedSnapshots = 8
	defaultSnapshotPins      = 1024
	defaultPinLifetime       = 30 * 24 * time.Hour
	defaultSnapshotBytes     = 576 << 20
	maximumRetainedSnapshots = 4096
	maximumSnapshotPins      = 1 << 20
	maximumPinLifetime       = 10 * 365 * 24 * time.Hour
	maximumSnapshotBytes     = 1 << 40
)

type SnapshotRef struct {
	Revision string
	Digest   string
}

func (r SnapshotRef) Valid() bool {
	if r.Revision == "" || len(r.Revision) > 1<<20 || !utf8.ValidString(r.Revision) || len(r.Digest) != 64 {
		return false
	}
	decoded := make([]byte, hex.DecodedLen(len(r.Digest)))
	if _, err := hex.Decode(decoded, []byte(r.Digest)); err != nil {
		return false
	}
	return hex.EncodeToString(decoded) == r.Digest
}

func (s *Snapshot) Reference() SnapshotRef {
	if s == nil {
		return SnapshotRef{}
	}
	return SnapshotRef{Revision: s.revision, Digest: s.digest}
}

type Clock func() time.Time

type ControllerSpec struct {
	Initial          *Snapshot
	MaxRetained      int
	MaxPins          int
	MaxPinLifetime   time.Duration
	MaxSnapshotBytes int64
	Clock            Clock
	Observer         Observer
}

type headState struct {
	snapshot  *Snapshot
	reference SnapshotRef
}

type Head struct {
	owner *Controller
	state *headState
}

func (h Head) Snapshot() *Snapshot {
	if h.state == nil {
		return nil
	}
	return h.state.snapshot
}

func (h Head) Reference() SnapshotRef {
	if h.state == nil {
		return SnapshotRef{}
	}
	return h.state.reference
}

func (h Head) Valid() bool {
	return h.owner != nil && h.state != nil && h.state.snapshot != nil && h.state.reference.Valid()
}

type retainedSnapshot struct {
	snapshot *Snapshot
	order    uint64
}

type pinRecord struct {
	reference SnapshotRef
	expiresAt time.Time
}

type Controller struct {
	mu               sync.Mutex
	current          *headState
	retained         map[SnapshotRef]retainedSnapshot
	pins             map[uint64]pinRecord
	pinned           map[SnapshotRef]int
	clock            Clock
	lastNow          time.Time
	maxRetained      int
	maxPins          int
	maxPinLifetime   time.Duration
	maxSnapshotBytes int64
	order            uint64
	nextPin          uint64
	observer         Observer
}

func NewController(spec ControllerSpec) (*Controller, error) {
	if err := validateControllerSnapshot(spec.Initial); err != nil {
		return nil, err
	}
	maxRetained := spec.MaxRetained
	if maxRetained == 0 {
		maxRetained = defaultRetainedSnapshots
	}
	maxPins := spec.MaxPins
	if maxPins == 0 {
		maxPins = defaultSnapshotPins
	}
	maxPinLifetime := spec.MaxPinLifetime
	if maxPinLifetime == 0 {
		maxPinLifetime = defaultPinLifetime
	}
	maxSnapshotBytes := spec.MaxSnapshotBytes
	if maxSnapshotBytes == 0 {
		maxSnapshotBytes = defaultSnapshotBytes
	}
	if maxRetained < 1 || maxRetained > maximumRetainedSnapshots || maxPins < 1 || maxPins > maximumSnapshotPins || maxPinLifetime < time.Nanosecond || maxPinLifetime > maximumPinLifetime || maxSnapshotBytes < 1 || maxSnapshotBytes > maximumSnapshotBytes {
		return nil, fmt.Errorf("%w: controller limits are outside their supported bounds", ErrLimitExceeded)
	}
	if int64(snapshotCatalogBytes(spec.Initial)) > maxSnapshotBytes {
		return nil, fmt.Errorf("%w: initial snapshot exceeds the controller byte budget", ErrLimitExceeded)
	}
	clock := spec.Clock
	if clock == nil {
		clock = time.Now
	}
	reference := spec.Initial.Reference()
	observer := spec.Observer
	if observer == nil {
		observer = spec.Initial.observer
	}
	return &Controller{
		current:          &headState{snapshot: spec.Initial, reference: reference},
		retained:         make(map[SnapshotRef]retainedSnapshot),
		pins:             make(map[uint64]pinRecord),
		pinned:           make(map[SnapshotRef]int),
		clock:            clock,
		maxRetained:      maxRetained,
		maxPins:          maxPins,
		maxPinLifetime:   maxPinLifetime,
		maxSnapshotBytes: maxSnapshotBytes,
		observer:         observer,
	}, nil
}

func (c *Controller) Current() Head {
	if c == nil {
		return Head{}
	}
	c.mu.Lock()
	state := c.current
	c.mu.Unlock()
	return Head{owner: c, state: state}
}

func (c *Controller) Activate(expected Head, candidate *Snapshot) (Head, error) {
	return c.ActivateContext(context.Background(), expected, candidate)
}

func (c *Controller) ActivateContext(ctx context.Context, expected Head, candidate *Snapshot) (head Head, err error) {
	started := time.Now()
	if c != nil {
		defer func() {
			notifyTerminalOperation(ctx, c.observer, OperationActivate, started, err)
		}()
	}
	if c == nil {
		return Head{}, fmt.Errorf("%w: controller is nil", ErrConflict)
	}
	if ctx == nil {
		return Head{}, fmt.Errorf("%w: context is nil", ErrInvalidCatalog)
	}
	if err := ctx.Err(); err != nil {
		return Head{}, err
	}
	if err := validateControllerSnapshot(candidate); err != nil {
		return Head{}, err
	}
	done := ctx.Done()
	now := c.clock()
	if err := ctx.Err(); err != nil {
		return Head{}, err
	}
	c.mu.Lock()
	if contextChannelClosed(done) {
		c.mu.Unlock()
		return Head{}, contextError(ctx)
	}
	c.expirePinsLocked(c.normalizeNowLocked(now))
	if expected.owner != c || expected.state == nil || expected.state != c.current {
		c.mu.Unlock()
		return Head{}, fmt.Errorf("%w: expected head is stale or belongs to another controller", ErrConflict)
	}
	head, err = c.activateLocked(candidate)
	c.mu.Unlock()
	return head, err
}

func (c *Controller) Rollback(expected Head, target SnapshotRef) (Head, error) {
	return c.RollbackContext(context.Background(), expected, target)
}

func (c *Controller) RollbackContext(ctx context.Context, expected Head, target SnapshotRef) (head Head, err error) {
	started := time.Now()
	if c != nil {
		defer func() {
			notifyTerminalOperation(ctx, c.observer, OperationRollback, started, err)
		}()
	}
	if c == nil {
		return Head{}, fmt.Errorf("%w: controller is nil", ErrConflict)
	}
	if ctx == nil {
		return Head{}, fmt.Errorf("%w: context is nil", ErrInvalidCatalog)
	}
	if err := ctx.Err(); err != nil {
		return Head{}, err
	}
	if !target.Valid() {
		return Head{}, fmt.Errorf("%w: target reference is invalid", ErrSnapshotNotFound)
	}
	done := ctx.Done()
	now := c.clock()
	if err := ctx.Err(); err != nil {
		return Head{}, err
	}
	c.mu.Lock()
	if contextChannelClosed(done) {
		c.mu.Unlock()
		return Head{}, contextError(ctx)
	}
	c.expirePinsLocked(c.normalizeNowLocked(now))
	if expected.owner != c || expected.state == nil || expected.state != c.current {
		c.mu.Unlock()
		return Head{}, fmt.Errorf("%w: expected head is stale or belongs to another controller", ErrConflict)
	}
	if target == c.current.reference {
		head = Head{owner: c, state: c.current}
		c.mu.Unlock()
		return head, nil
	}
	retained, ok := c.retained[target]
	if !ok {
		c.mu.Unlock()
		return Head{}, fmt.Errorf("%w: revision %q", ErrSnapshotNotFound, target.Revision)
	}
	head, err = c.activateLocked(retained.snapshot)
	c.mu.Unlock()
	return head, err
}

func contextChannelClosed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return context.Canceled
}

func (c *Controller) activateLocked(candidate *Snapshot) (Head, error) {
	reference := candidate.Reference()
	if reference == c.current.reference {
		return Head{owner: c, state: c.current}, nil
	}
	if c.revisionConflictLocked(reference) {
		return Head{}, fmt.Errorf("%w: revision %q identifies different snapshot content", ErrConflict, reference.Revision)
	}
	next := make(map[SnapshotRef]retainedSnapshot, len(c.retained)+1)
	for retainedRef, retained := range c.retained {
		if retainedRef != reference {
			next[retainedRef] = retained
		}
	}
	requiredBytes := int64(snapshotCatalogBytes(candidate)) + int64(snapshotCatalogBytes(c.current.snapshot))
	for len(next) >= c.maxRetained || requiredBytes+c.retainedBytes(next) > c.maxSnapshotBytes {
		oldest, ok := c.oldestUnpinned(next)
		if !ok {
			if len(next) == 0 {
				return Head{}, fmt.Errorf("%w: active and candidate snapshots exceed the controller byte budget", ErrLimitExceeded)
			}
			return Head{}, fmt.Errorf("%w: retained capacity is held by pinned snapshots", ErrPinUnavailable)
		}
		delete(next, oldest)
	}
	order := c.nextRetentionOrder(next)
	next[c.current.reference] = retainedSnapshot{snapshot: c.current.snapshot, order: order}
	state := &headState{snapshot: candidate, reference: reference}
	c.current = state
	c.retained = next
	c.order = order
	return Head{owner: c, state: state}, nil
}

func (c *Controller) retainedBytes(retained map[SnapshotRef]retainedSnapshot) int64 {
	var total int64
	for _, entry := range retained {
		total += int64(snapshotCatalogBytes(entry.snapshot))
	}
	return total
}

func (c *Controller) revisionConflictLocked(candidate SnapshotRef) bool {
	if c.current.reference.Revision == candidate.Revision && c.current.reference != candidate {
		return true
	}
	for reference := range c.retained {
		if reference.Revision == candidate.Revision && reference != candidate {
			return true
		}
	}
	return false
}

func (c *Controller) nextRetentionOrder(retained map[SnapshotRef]retainedSnapshot) uint64 {
	next := c.order + 1
	if next != 0 {
		return next
	}
	type orderedReference struct {
		reference SnapshotRef
		order     uint64
	}
	ordered := make([]orderedReference, 0, len(retained))
	for reference, snapshot := range retained {
		ordered = append(ordered, orderedReference{reference: reference, order: snapshot.order})
	}
	slices.SortFunc(ordered, func(a, b orderedReference) int {
		if a.order < b.order {
			return -1
		}
		if a.order > b.order {
			return 1
		}
		if a.reference.Revision < b.reference.Revision {
			return -1
		}
		if a.reference.Revision > b.reference.Revision {
			return 1
		}
		if a.reference.Digest < b.reference.Digest {
			return -1
		}
		if a.reference.Digest > b.reference.Digest {
			return 1
		}
		return 0
	})
	for index, item := range ordered {
		entry := retained[item.reference]
		entry.order = uint64(index + 1)
		retained[item.reference] = entry
	}
	return uint64(len(ordered) + 1)
}

func (c *Controller) oldestUnpinned(retained map[SnapshotRef]retainedSnapshot) (SnapshotRef, bool) {
	var selected SnapshotRef
	var selectedOrder uint64
	found := false
	for reference, snapshot := range retained {
		if c.pinned[reference] != 0 {
			continue
		}
		if !found || snapshot.order < selectedOrder || snapshot.order == selectedOrder && referenceLess(reference, selected) {
			selected = reference
			selectedOrder = snapshot.order
			found = true
		}
	}
	return selected, found
}

func referenceLess(left, right SnapshotRef) bool {
	if left.Revision != right.Revision {
		return left.Revision < right.Revision
	}
	return left.Digest < right.Digest
}

func (c *Controller) Retained() []SnapshotRef {
	if c == nil {
		return nil
	}
	now := c.clock()
	c.mu.Lock()
	c.expirePinsLocked(c.normalizeNowLocked(now))
	type orderedReference struct {
		reference SnapshotRef
		order     uint64
	}
	ordered := make([]orderedReference, 0, len(c.retained))
	for reference, snapshot := range c.retained {
		ordered = append(ordered, orderedReference{reference: reference, order: snapshot.order})
	}
	c.mu.Unlock()
	slices.SortFunc(ordered, func(a, b orderedReference) int {
		if a.order < b.order {
			return -1
		}
		if a.order > b.order {
			return 1
		}
		if referenceLess(a.reference, b.reference) {
			return -1
		}
		if referenceLess(b.reference, a.reference) {
			return 1
		}
		return 0
	})
	out := make([]SnapshotRef, len(ordered))
	for index, item := range ordered {
		out[index] = item.reference
	}
	return out
}

func (c *Controller) Prune(reference SnapshotRef) error {
	if c == nil {
		return fmt.Errorf("%w: controller is nil", ErrSnapshotNotFound)
	}
	if !reference.Valid() {
		return fmt.Errorf("%w: reference is invalid", ErrSnapshotNotFound)
	}
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expirePinsLocked(c.normalizeNowLocked(now))
	if c.current.reference == reference {
		return fmt.Errorf("%w: active snapshot cannot be pruned", ErrConflict)
	}
	if _, ok := c.retained[reference]; !ok {
		return fmt.Errorf("%w: revision %q", ErrSnapshotNotFound, reference.Revision)
	}
	if c.pinned[reference] != 0 {
		return fmt.Errorf("%w: retained snapshot is pinned", ErrPinUnavailable)
	}
	delete(c.retained, reference)
	return nil
}

type Lease struct {
	controller *Controller
	id         uint64
	reference  SnapshotRef
	expiresAt  time.Time
}

func (l *Lease) Reference() SnapshotRef {
	if l == nil {
		return SnapshotRef{}
	}
	return l.reference
}

func (l *Lease) ExpiresAt() time.Time {
	if l == nil {
		return time.Time{}
	}
	return l.expiresAt
}

func (l *Lease) Snapshot() (*Snapshot, error) {
	if l == nil || l.controller == nil || l.id == 0 {
		return nil, fmt.Errorf("%w: lease is empty", ErrPinUnavailable)
	}
	c := l.controller
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expirePinsLocked(c.normalizeNowLocked(now))
	record, ok := c.pins[l.id]
	if !ok || record.reference != l.reference || !record.expiresAt.Equal(l.expiresAt) {
		return nil, fmt.Errorf("%w: lease was released or expired", ErrPinUnavailable)
	}
	if c.current.reference == record.reference {
		return c.current.snapshot, nil
	}
	retained, ok := c.retained[record.reference]
	if !ok {
		return nil, fmt.Errorf("%w: pinned revision %q", ErrSnapshotNotFound, record.reference.Revision)
	}
	return retained.snapshot, nil
}

func (l *Lease) Release() {
	if l == nil || l.controller == nil || l.id == 0 {
		return
	}
	c := l.controller
	c.mu.Lock()
	c.removePinLocked(l.id)
	c.mu.Unlock()
}

func (c *Controller) PinCurrent(lifetime time.Duration) (*Lease, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: controller is nil", ErrPinUnavailable)
	}
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pinLocked(c.current.reference, lifetime, now)
}

func (c *Controller) Pin(reference SnapshotRef, lifetime time.Duration) (*Lease, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: controller is nil", ErrPinUnavailable)
	}
	if !reference.Valid() {
		return nil, fmt.Errorf("%w: reference is invalid", ErrSnapshotNotFound)
	}
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pinLocked(reference, lifetime, now)
}

func (c *Controller) pinLocked(reference SnapshotRef, lifetime time.Duration, observedNow time.Time) (*Lease, error) {
	now := c.normalizeNowLocked(observedNow)
	c.expirePinsLocked(now)
	if lifetime <= 0 || lifetime > c.maxPinLifetime {
		return nil, fmt.Errorf("%w: lifetime must be positive and at most %s", ErrPinUnavailable, c.maxPinLifetime)
	}
	if len(c.pins) >= c.maxPins {
		return nil, fmt.Errorf("%w: pin capacity %d is exhausted", ErrPinUnavailable, c.maxPins)
	}
	if c.current.reference != reference {
		if _, ok := c.retained[reference]; !ok {
			return nil, fmt.Errorf("%w: revision %q", ErrSnapshotNotFound, reference.Revision)
		}
	}
	expiresAt := now.Add(lifetime)
	if !expiresAt.After(now) || expiresAt.Sub(now) != lifetime {
		return nil, fmt.Errorf("%w: lifetime overflows the controller clock", ErrPinUnavailable)
	}
	id := c.allocatePinIDLocked()
	c.pins[id] = pinRecord{reference: reference, expiresAt: expiresAt}
	c.pinned[reference]++
	return &Lease{controller: c, id: id, reference: reference, expiresAt: expiresAt}, nil
}

func (c *Controller) allocatePinIDLocked() uint64 {
	for {
		c.nextPin++
		if c.nextPin == 0 {
			c.nextPin++
		}
		if _, exists := c.pins[c.nextPin]; !exists {
			return c.nextPin
		}
	}
}

func (c *Controller) expirePinsLocked(now time.Time) {
	for id, record := range c.pins {
		if !now.Before(record.expiresAt) {
			c.removePinLocked(id)
		}
	}
}

func (c *Controller) removePinLocked(id uint64) {
	record, ok := c.pins[id]
	if !ok {
		return
	}
	delete(c.pins, id)
	count := c.pinned[record.reference]
	if count <= 1 {
		delete(c.pinned, record.reference)
		return
	}
	c.pinned[record.reference] = count - 1
}

func (c *Controller) normalizeNowLocked(now time.Time) time.Time {
	if !c.lastNow.IsZero() && now.Before(c.lastNow) {
		return c.lastNow
	}
	c.lastNow = now
	return now
}

func validateControllerSnapshot(snapshot *Snapshot) error {
	if snapshot == nil {
		return fmt.Errorf("%w: controller snapshot is nil", ErrInvalidCatalog)
	}
	reference := snapshot.Reference()
	if !reference.Valid() || snapshot.resolver == nil || len(snapshot.records) == 0 || snapshot.digest != snapshotDigest(snapshot) {
		return fmt.Errorf("%w: controller snapshot is incomplete or its semantic digest is invalid", ErrInvalidCatalog)
	}
	return nil
}
