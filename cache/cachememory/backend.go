package cachememory

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/frostgrove/vv/cache"
)

const (
	ChargeModelVersion          = 1
	AddressChargeBytes    int64 = 96
	MetadataReserveBytes  int64 = 256
	FixedEntryChargeBytes       = AddressChargeBytes + MetadataReserveBytes
)

type Limits struct {
	MaxEntries   int
	MaxBytes     int64
	MaxItemBytes int
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
	clock    Clock
	observer Observer
}

func WithClock(clock Clock) Option {
	return optionFunc(func(settings *settings) error {
		if nilValue(clock) {
			return fmt.Errorf("%w: clock is nil", cache.ErrInvalid)
		}
		settings.clock = clock
		return nil
	})
}

func WithObserver(observer Observer) Option {
	return optionFunc(func(settings *settings) error {
		if nilValue(observer) {
			return fmt.Errorf("%w: observer is nil", cache.ErrInvalid)
		}
		settings.observer = observer
		return nil
	})
}

type Backend struct {
	mu      sync.Mutex
	entries map[cache.Address]*entry
	mru     *entry
	lru     *entry
	charged int64
	limits  Limits
	clock   Clock
	observe Observer
	closed  bool
}

var (
	_ cache.Backend          = (*Backend)(nil)
	_ cache.BatchReader      = (*Backend)(nil)
	_ cache.BackendDescriber = (*Backend)(nil)
	_ cache.HealthChecker    = (*Backend)(nil)
)

type entry struct {
	address   cache.Address
	value     []byte
	expiresAt time.Time
	charge    int64
	newer     *entry
	older     *entry
}

type snapshot struct {
	address cache.Address
	value   []byte
	charge  int64
}

type pendingEviction struct {
	item   *entry
	reason Reason
}

type Stats struct {
	Entries      int
	ChargedBytes int64
	Limits       Limits
	Closed       bool
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

type discardObserver struct{}

func (discardObserver) Observe(context.Context, Event) {}

func New(limits Limits, options ...Option) (*Backend, error) {
	if err := validateLimits(limits); err != nil {
		return nil, fail("build backend", err)
	}
	settings := settings{clock: systemClock{}, observer: discardObserver{}}
	for index, option := range options {
		if nilValue(option) {
			return nil, fail("build backend", fmt.Errorf("%w: option %d is nil", cache.ErrInvalid, index))
		}
		if err := option.apply(&settings); err != nil {
			return nil, fail("build backend", fmt.Errorf("option %d: %w", index, err))
		}
	}
	return &Backend{
		entries: make(map[cache.Address]*entry),
		limits:  limits,
		clock:   settings.clock,
		observe: settings.observer,
	}, nil
}

func EntryCharge(valueBytes int) (int64, error) {
	if valueBytes < 0 || int64(valueBytes) > math.MaxInt64-FixedEntryChargeBytes {
		return 0, cache.ErrTooLarge
	}
	return FixedEntryChargeBytes + int64(valueBytes), nil
}

func (backend *Backend) DescribeBackend() cache.BackendDescription {
	if backend == nil {
		return cache.BackendDescription{}
	}
	return cache.BackendDescription{
		Name:              "memory",
		Topology:          cache.ProcessBackend,
		ExpiryClock:       cache.ProcessExpiryClock,
		MaxItemBytes:      backend.limits.MaxItemBytes,
		RelativeExpiry:    true,
		MaxRelativeExpiry: time.Duration(math.MaxInt64),
		CapacityBounded:   true,
	}
}

func (backend *Backend) Get(ctx context.Context, address cache.Address, limit cache.ReadLimit) ([]byte, bool, error) {
	if err := validateContext(ctx); err != nil {
		return nil, false, fail("get", err)
	}
	if limit.MaxBytes <= 0 {
		return nil, false, fail("get", fmt.Errorf("%w: read limit is not positive", cache.ErrInvalid))
	}
	if err := ctx.Err(); err != nil {
		return nil, false, fail("get", err)
	}
	if backend == nil {
		return nil, false, fail("get", cache.ErrClosed)
	}
	now, err := backend.now()
	if err != nil {
		return nil, false, fail("get", err)
	}

	backend.mu.Lock()
	if err := ctx.Err(); err != nil {
		backend.mu.Unlock()
		return nil, false, fail("get", err)
	}
	if backend.closed {
		backend.mu.Unlock()
		return nil, false, fail("get", cache.ErrClosed)
	}
	item := backend.entries[address]
	if item == nil {
		backend.mu.Unlock()
		backend.emit(ctx, Event{Operation: GetOperation, Outcome: MissOutcome})
		return nil, false, nil
	}
	if expired(item, now) {
		eviction := backend.removeLocked(item, ExpiredReason)
		backend.mu.Unlock()
		backend.emit(ctx, eviction)
		backend.emit(ctx, Event{Operation: GetOperation, Outcome: MissOutcome, Reason: ExpiredReason})
		return nil, false, nil
	}
	if len(item.value) > limit.MaxBytes {
		valueBytes := int64(len(item.value))
		backend.mu.Unlock()
		backend.emit(ctx, Event{Operation: GetOperation, Outcome: RejectedOutcome, Reason: ReadLimitReason, Items: 1, ValueBytes: valueBytes})
		return nil, false, fail("get", cache.ErrTooLarge)
	}
	backend.promoteLocked(item)
	value := item.value
	valueBytes := int64(len(value))
	charge := item.charge
	backend.mu.Unlock()

	result := clone(value)
	if err := ctx.Err(); err != nil {
		return nil, false, fail("get", err)
	}
	backend.emit(ctx, Event{Operation: GetOperation, Outcome: HitOutcome, Items: 1, ValueBytes: valueBytes, ChargedBytes: charge})
	return result, true, nil
}

func (backend *Backend) Put(ctx context.Context, address cache.Address, value []byte, expiry cache.Expiry) error {
	if err := validateContext(ctx); err != nil {
		return fail("put", err)
	}
	if err := validateExpiry(expiry); err != nil {
		return fail("put", err)
	}
	if err := ctx.Err(); err != nil {
		return fail("put", err)
	}
	if backend == nil {
		return fail("put", cache.ErrClosed)
	}
	charge, err := EntryCharge(len(value))
	if err != nil || len(value) > backend.limits.MaxItemBytes {
		backend.emit(ctx, Event{Operation: PutOperation, Outcome: RejectedOutcome, Reason: MaxItemBytesReason, Items: 1, ValueBytes: int64(len(value))})
		return fail("put", cache.ErrTooLarge)
	}
	if charge > backend.limits.MaxBytes {
		backend.emit(ctx, Event{Operation: PutOperation, Outcome: RejectedOutcome, Reason: MaxBytesReason, Items: 1, ValueBytes: int64(len(value)), ChargedBytes: charge})
		return fail("put", cache.ErrTooLarge)
	}
	owned := clone(value)
	if err := ctx.Err(); err != nil {
		return fail("put", err)
	}
	now, err := backend.now()
	if err != nil {
		return fail("put", err)
	}
	expiresAt, err := expirationTime(now, expiry)
	if err != nil {
		return fail("put", err)
	}

	backend.mu.Lock()
	if err := ctx.Err(); err != nil {
		backend.mu.Unlock()
		return fail("put", err)
	}
	if backend.closed {
		backend.mu.Unlock()
		return fail("put", cache.ErrClosed)
	}
	events, err := backend.purgeExpiredLocked(ctx, now)
	if err != nil {
		backend.mu.Unlock()
		backend.emitAll(ctx, events)
		return fail("put", err)
	}
	item := backend.entries[address]
	outcome := StoredOutcome
	if item != nil {
		outcome = ReplacedOutcome
	}
	evictions, err := backend.planEvictionsLocked(ctx, item, charge)
	if err != nil {
		backend.mu.Unlock()
		backend.emitAll(ctx, events)
		return fail("put", err)
	}
	if item != nil {
		backend.removeEntryLocked(item)
	}
	for _, eviction := range evictions {
		events = append(events, backend.removeLocked(eviction.item, eviction.reason))
	}
	item = &entry{address: address}
	backend.entries[address] = item
	backend.attachMRULocked(item)
	item.value = owned
	item.expiresAt = expiresAt
	item.charge = charge
	backend.charged += charge
	backend.mu.Unlock()

	backend.emitAll(ctx, events)
	backend.emit(ctx, Event{Operation: PutOperation, Outcome: outcome, Items: 1, ValueBytes: int64(len(value)), ChargedBytes: charge})
	return nil
}

func (backend *Backend) Delete(ctx context.Context, address cache.Address) error {
	if err := validateContext(ctx); err != nil {
		return fail("delete", err)
	}
	if err := ctx.Err(); err != nil {
		return fail("delete", err)
	}
	if backend == nil {
		return fail("delete", cache.ErrClosed)
	}

	backend.mu.Lock()
	if err := ctx.Err(); err != nil {
		backend.mu.Unlock()
		return fail("delete", err)
	}
	if backend.closed {
		backend.mu.Unlock()
		return fail("delete", cache.ErrClosed)
	}
	item := backend.entries[address]
	if item == nil {
		backend.mu.Unlock()
		backend.emit(ctx, Event{Operation: DeleteOperation, Outcome: MissOutcome})
		return nil
	}
	valueBytes := int64(len(item.value))
	charge := item.charge
	backend.removeEntryLocked(item)
	backend.mu.Unlock()
	backend.emit(ctx, Event{Operation: DeleteOperation, Outcome: DeletedOutcome, Items: 1, ValueBytes: valueBytes, ChargedBytes: charge})
	return nil
}

func (backend *Backend) GetMany(ctx context.Context, addresses []cache.Address, limit cache.BatchReadLimit) (map[cache.Address][]byte, error) {
	if err := validateContext(ctx); err != nil {
		return nil, fail("get many", err)
	}
	if err := validateBatchReadLimit(limit); err != nil {
		return nil, fail("get many", err)
	}
	if len(addresses) > limit.MaxItems {
		return nil, fail("get many", cache.ErrTooLarge)
	}
	if err := ctx.Err(); err != nil {
		return nil, fail("get many", err)
	}
	if backend == nil {
		return nil, fail("get many", cache.ErrClosed)
	}
	unique := make([]cache.Address, 0, len(addresses))
	seen := make(map[cache.Address]struct{}, len(addresses))
	for index, address := range addresses {
		if index%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, fail("get many", err)
			}
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		unique = append(unique, address)
	}
	now, err := backend.now()
	if err != nil {
		return nil, fail("get many", err)
	}

	backend.mu.Lock()
	if err := ctx.Err(); err != nil {
		backend.mu.Unlock()
		return nil, fail("get many", err)
	}
	if backend.closed {
		backend.mu.Unlock()
		return nil, fail("get many", cache.ErrClosed)
	}
	items := make([]snapshot, 0, len(unique))
	events := make([]Event, 0)
	var total int64
	for index, address := range unique {
		if index%64 == 0 {
			if err := ctx.Err(); err != nil {
				backend.mu.Unlock()
				backend.emitAll(ctx, events)
				return nil, fail("get many", err)
			}
		}
		item := backend.entries[address]
		if item == nil {
			continue
		}
		if expired(item, now) {
			events = append(events, backend.removeLocked(item, ExpiredReason))
			continue
		}
		valueBytes := int64(len(item.value))
		if len(item.value) > limit.MaxItemBytes {
			backend.mu.Unlock()
			backend.emitAll(ctx, events)
			backend.emit(ctx, Event{Operation: GetManyOperation, Outcome: RejectedOutcome, Reason: BatchItemLimitReason, Items: len(items) + 1, ValueBytes: valueBytes})
			return nil, fail("get many", cache.ErrTooLarge)
		}
		if valueBytes > limit.MaxTotalBytes-total {
			backend.mu.Unlock()
			backend.emitAll(ctx, events)
			backend.emit(ctx, Event{Operation: GetManyOperation, Outcome: RejectedOutcome, Reason: BatchTotalLimitReason, Items: len(items) + 1, ValueBytes: total + valueBytes})
			return nil, fail("get many", cache.ErrTooLarge)
		}
		total += valueBytes
		items = append(items, snapshot{address: address, value: item.value, charge: item.charge})
	}
	for index, item := range items {
		if index%64 == 0 {
			if err := ctx.Err(); err != nil {
				backend.mu.Unlock()
				backend.emitAll(ctx, events)
				return nil, fail("get many", err)
			}
		}
		backend.promoteLocked(backend.entries[item.address])
	}
	backend.mu.Unlock()
	backend.emitAll(ctx, events)

	result := make(map[cache.Address][]byte, len(items))
	var charged int64
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, fail("get many", err)
		}
		result[item.address] = clone(item.value)
		charged = saturatingCharge(charged, item.charge)
	}
	if err := ctx.Err(); err != nil {
		return nil, fail("get many", err)
	}
	backend.emit(ctx, Event{Operation: GetManyOperation, Outcome: CompleteOutcome, Items: len(items), ValueBytes: total, ChargedBytes: charged})
	return result, nil
}

func (backend *Backend) Stats() Stats {
	if backend == nil {
		return Stats{Closed: true}
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return Stats{
		Entries:      len(backend.entries),
		ChargedBytes: backend.charged,
		Limits:       backend.limits,
		Closed:       backend.closed,
	}
}

func (backend *Backend) Reset() error {
	if backend == nil {
		return fail("reset", cache.ErrClosed)
	}
	backend.mu.Lock()
	if backend.closed {
		backend.mu.Unlock()
		return fail("reset", cache.ErrClosed)
	}
	items := len(backend.entries)
	charged := backend.charged
	backend.entries = make(map[cache.Address]*entry)
	backend.mru = nil
	backend.lru = nil
	backend.charged = 0
	backend.mu.Unlock()
	backend.emit(context.Background(), Event{Operation: ResetOperation, Outcome: CompleteOutcome, Reason: ResetReason, Items: items, ChargedBytes: charged})
	return nil
}

func (backend *Backend) CheckBackend(ctx context.Context) error {
	if err := validateContext(ctx); err != nil {
		return fail("check backend", err)
	}
	if backend == nil {
		return fail("check backend", cache.ErrClosed)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.closed {
		return fail("check backend", cache.ErrClosed)
	}
	return nil
}

func (backend *Backend) Close() error {
	if backend == nil {
		return nil
	}
	backend.mu.Lock()
	if backend.closed {
		backend.mu.Unlock()
		return nil
	}
	items := len(backend.entries)
	charged := backend.charged
	backend.entries = nil
	backend.mru = nil
	backend.lru = nil
	backend.charged = 0
	backend.closed = true
	backend.mu.Unlock()
	backend.emit(context.Background(), Event{Operation: CloseOperation, Outcome: CompleteOutcome, Reason: CloseReason, Items: items, ChargedBytes: charged})
	return nil
}

func (backend *Backend) purgeExpiredLocked(ctx context.Context, now time.Time) ([]Event, error) {
	events := make([]Event, 0)
	index := 0
	for item := backend.lru; item != nil; index++ {
		if index%64 == 0 {
			if err := ctx.Err(); err != nil {
				return events, err
			}
		}
		newer := item.newer
		if expired(item, now) {
			events = append(events, backend.removeLocked(item, ExpiredReason))
		}
		item = newer
	}
	return events, nil
}

func (backend *Backend) planEvictionsLocked(ctx context.Context, replacement *entry, charge int64) ([]pendingEviction, error) {
	entries := len(backend.entries)
	charged := backend.charged
	if replacement != nil {
		entries--
		charged -= replacement.charge
	}
	result := make([]pendingEviction, 0)
	index := 0
	for candidate := backend.lru; entries >= backend.limits.MaxEntries || charged > backend.limits.MaxBytes-charge; candidate = candidate.newer {
		if index%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		index++
		if candidate == nil {
			return nil, cache.ErrInvalid
		}
		if candidate == replacement {
			continue
		}
		reason := MaxBytesReason
		if entries >= backend.limits.MaxEntries {
			reason = MaxEntriesReason
		}
		result = append(result, pendingEviction{item: candidate, reason: reason})
		entries--
		charged -= candidate.charge
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (backend *Backend) removeLocked(item *entry, reason Reason) Event {
	event := Event{Operation: EvictOperation, Outcome: EvictedOutcome, Reason: reason, Items: 1}
	if item == nil {
		return event
	}
	event.ValueBytes = int64(len(item.value))
	event.ChargedBytes = item.charge
	backend.removeEntryLocked(item)
	return event
}

func (backend *Backend) removeEntryLocked(item *entry) {
	delete(backend.entries, item.address)
	backend.detachLocked(item)
	backend.charged -= item.charge
	item.value = nil
	item.charge = 0
	item.expiresAt = time.Time{}
}

func (backend *Backend) promoteLocked(item *entry) {
	if item == nil || backend.mru == item {
		return
	}
	backend.detachLocked(item)
	backend.attachMRULocked(item)
}

func (backend *Backend) attachMRULocked(item *entry) {
	item.newer = nil
	item.older = backend.mru
	if backend.mru != nil {
		backend.mru.newer = item
	} else {
		backend.lru = item
	}
	backend.mru = item
}

func (backend *Backend) detachLocked(item *entry) {
	if item.newer != nil {
		item.newer.older = item.older
	} else if backend.mru == item {
		backend.mru = item.older
	}
	if item.older != nil {
		item.older.newer = item.newer
	} else if backend.lru == item {
		backend.lru = item.newer
	}
	item.newer = nil
	item.older = nil
}

func (backend *Backend) emit(ctx context.Context, event Event) {
	defer func() {
		_ = recover()
	}()
	backend.observe.Observe(ctx, event)
}

func (backend *Backend) emitAll(ctx context.Context, events []Event) {
	for _, event := range events {
		backend.emit(ctx, event)
	}
}

func (backend *Backend) now() (now time.Time, err error) {
	defer func() {
		if recover() != nil {
			now = time.Time{}
			err = fmt.Errorf("%w: clock panicked", cache.ErrInvalid)
		}
	}()
	now = backend.clock.Now()
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("%w: clock returned zero time", cache.ErrInvalid)
	}
	return now, nil
}

func validateLimits(limits Limits) error {
	if limits.MaxEntries <= 0 || limits.MaxBytes <= 0 || limits.MaxItemBytes <= 0 {
		return fmt.Errorf("%w: limits must be positive", cache.ErrInvalid)
	}
	charge, err := EntryCharge(limits.MaxItemBytes)
	if err != nil || charge > limits.MaxBytes {
		return fmt.Errorf("%w: maximum item cannot fit the byte budget", cache.ErrInvalid)
	}
	return nil
}

func validateContext(ctx context.Context) error {
	if nilValue(ctx) {
		return fmt.Errorf("%w: context is nil", cache.ErrInvalid)
	}
	return nil
}

func validateExpiry(expiry cache.Expiry) error {
	switch expiry.Mode {
	case cache.RelativeExpiry:
		if expiry.RetainFor <= 0 {
			return fmt.Errorf("%w: relative expiry is not positive", cache.ErrInvalid)
		}
	case cache.CapacityOnlyExpiry:
		if expiry.RetainFor != 0 {
			return fmt.Errorf("%w: capacity-only expiry has a duration", cache.ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: expiry mode is invalid", cache.ErrInvalid)
	}
	return nil
}

func validateBatchReadLimit(limit cache.BatchReadLimit) error {
	if limit.MaxItems <= 0 || limit.MaxItemBytes <= 0 || limit.MaxTotalBytes < int64(limit.MaxItemBytes) {
		return fmt.Errorf("%w: batch read limits are invalid", cache.ErrInvalid)
	}
	return nil
}

func expirationTime(now time.Time, expiry cache.Expiry) (result time.Time, err error) {
	if expiry.Mode == cache.CapacityOnlyExpiry {
		return time.Time{}, nil
	}
	defer func() {
		if recover() != nil {
			result = time.Time{}
			err = fmt.Errorf("%w: relative expiry overflows time", cache.ErrInvalid)
		}
	}()
	result = now.Add(expiry.RetainFor)
	if !result.After(now) {
		return time.Time{}, fmt.Errorf("%w: relative expiry overflows time", cache.ErrInvalid)
	}
	return result, nil
}

func expired(item *entry, now time.Time) bool {
	return !item.expiresAt.IsZero() && !now.Before(item.expiresAt)
}

func clone(value []byte) []byte {
	result := make([]byte, len(value))
	copy(result, value)
	return result
}

func saturatingCharge(total, value int64) int64 {
	if total < 0 || value < 0 || value > math.MaxInt64-total {
		return math.MaxInt64
	}
	return total + value
}

func fail(operation string, cause error) error {
	return &cache.Error{Operation: "memory " + operation, Cause: cause}
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
