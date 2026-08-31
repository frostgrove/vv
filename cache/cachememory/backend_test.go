package cachememory

import (
	"bytes"
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/cache"
)

var testExpiry = cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Hour}

type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	calls int
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.calls++
	return clock.now
}

func (clock *fakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

func (clock *fakeClock) Calls() int {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.calls
}

type observerFunc func(context.Context, Event)

func (observe observerFunc) Observe(ctx context.Context, event Event) {
	observe(ctx, event)
}

type eventRecorder struct {
	mu     sync.Mutex
	events []Event
}

type stepCancelContext struct {
	calls    atomic.Int64
	cancelAt int64
	done     chan struct{}
	once     sync.Once
}

func newStepCancelContext(cancelAt int64) *stepCancelContext {
	return &stepCancelContext{cancelAt: cancelAt, done: make(chan struct{})}
}

func (*stepCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (ctx *stepCancelContext) Done() <-chan struct{}   { return ctx.done }
func (*stepCancelContext) Value(any) any               { return nil }

func (ctx *stepCancelContext) Err() error {
	if ctx.calls.Add(1) < ctx.cancelAt {
		return nil
	}
	ctx.once.Do(func() { close(ctx.done) })
	return context.Canceled
}

func (recorder *eventRecorder) Observe(_ context.Context, event Event) {
	recorder.mu.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mu.Unlock()
}

func (recorder *eventRecorder) Events() []Event {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	result := make([]Event, len(recorder.events))
	copy(result, recorder.events)
	return result
}

func TestEntryChargeAndLimits(t *testing.T) {
	charge, err := EntryCharge(17)
	if err != nil {
		t.Fatal(err)
	}
	if charge != FixedEntryChargeBytes+17 {
		t.Fatalf("charge = %d", charge)
	}

	valid := Limits{MaxEntries: 2, MaxBytes: FixedEntryChargeBytes + 32, MaxItemBytes: 32}
	if _, err := New(valid); err != nil {
		t.Fatalf("valid limits: %v", err)
	}
	invalid := []Limits{
		{},
		{MaxEntries: -1, MaxBytes: 1000, MaxItemBytes: 1},
		{MaxEntries: 1, MaxBytes: 0, MaxItemBytes: 1},
		{MaxEntries: 1, MaxBytes: 1000, MaxItemBytes: 0},
		{MaxEntries: 1, MaxBytes: FixedEntryChargeBytes + 31, MaxItemBytes: 32},
	}
	for index, limits := range invalid {
		if _, err := New(limits); !errors.Is(err, cache.ErrInvalid) {
			t.Fatalf("invalid limits %d: %v", index, err)
		}
	}
}

func TestOptionsRejectNilDependencies(t *testing.T) {
	limits := testLimits(1, 8)
	var clock *fakeClock
	if _, err := New(limits, WithClock(clock)); !errors.Is(err, cache.ErrInvalid) {
		t.Fatalf("nil clock: %v", err)
	}
	var observer *eventRecorder
	if _, err := New(limits, WithObserver(observer)); !errors.Is(err, cache.ErrInvalid) {
		t.Fatalf("nil observer: %v", err)
	}
	if _, err := New(limits, nil); !errors.Is(err, cache.ErrInvalid) {
		t.Fatalf("nil option: %v", err)
	}
}

func TestDescription(t *testing.T) {
	backend := mustBackend(t, testLimits(3, 17))
	description := backend.DescribeBackend()
	if description.Name != "memory" || description.Topology != cache.ProcessBackend ||
		description.ExpiryClock != cache.ProcessExpiryClock || description.MaxItemBytes != 17 ||
		!description.RelativeExpiry || description.MaxRelativeExpiry != time.Duration(math.MaxInt64) ||
		!description.CapacityBounded {
		t.Fatalf("description = %+v", description)
	}
	if _, ok := any(backend).(cache.Backend); !ok {
		t.Fatal("backend does not implement cache.Backend")
	}
	if _, ok := any(backend).(cache.BatchReader); !ok {
		t.Fatal("backend does not implement cache.BatchReader")
	}
	if _, ok := any(backend).(cache.BackendDescriber); !ok {
		t.Fatal("backend does not implement cache.BackendDescriber")
	}
}

func TestPutAndGetOwnTheirBytes(t *testing.T) {
	backend := mustBackend(t, testLimits(2, 16))
	ctx := context.Background()
	address := testAddress(1)
	input := []byte("value")
	if err := backend.Put(ctx, address, input, testExpiry); err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	first := mustGet(t, backend, address, 16)
	if string(first) != "value" {
		t.Fatalf("first = %q", first)
	}
	first[1] = 'X'
	second := mustGet(t, backend, address, 16)
	if string(second) != "value" {
		t.Fatalf("second = %q", second)
	}
}

func TestPutRejectsOversizeWithoutMutation(t *testing.T) {
	backend := mustBackend(t, testLimits(2, 3))
	ctx := context.Background()
	address := testAddress(1)
	if err := backend.Put(ctx, address, []byte("old"), testExpiry); err != nil {
		t.Fatal(err)
	}
	before := backend.Stats()
	if err := backend.Put(ctx, address, []byte("four"), testExpiry); !errors.Is(err, cache.ErrTooLarge) {
		t.Fatalf("put error = %v", err)
	}
	after := backend.Stats()
	if before != after {
		t.Fatalf("stats changed: before=%+v after=%+v", before, after)
	}
	if got := string(mustGet(t, backend, address, 3)); got != "old" {
		t.Fatalf("value = %q", got)
	}
}

func TestLRUEvictionUsesSuccessfulAccesses(t *testing.T) {
	backend := mustBackend(t, testLimits(2, 4))
	ctx := context.Background()
	a := testAddress(1)
	b := testAddress(2)
	c := testAddress(3)
	mustPut(t, backend, a, "a", testExpiry)
	mustPut(t, backend, b, "b", testExpiry)
	mustGet(t, backend, a, 4)
	mustPut(t, backend, c, "c", testExpiry)
	assertMiss(t, backend, b)
	if got := string(mustGet(t, backend, a, 4)); got != "a" {
		t.Fatalf("a = %q", got)
	}
	if got := string(mustGet(t, backend, c, 4)); got != "c" {
		t.Fatalf("c = %q", got)
	}
	assertInvariant(t, backend)

	backend = mustBackend(t, testLimits(2, 4))
	mustPut(t, backend, a, "aa", testExpiry)
	mustPut(t, backend, b, "b", testExpiry)
	if _, _, err := backend.Get(ctx, a, cache.ReadLimit{MaxBytes: 1}); !errors.Is(err, cache.ErrTooLarge) {
		t.Fatalf("limited get = %v", err)
	}
	mustPut(t, backend, c, "c", testExpiry)
	assertMiss(t, backend, a)
}

func TestByteEvictionAndReplacementAccounting(t *testing.T) {
	maxBytes := 2*FixedEntryChargeBytes + 4
	backend := mustBackend(t, Limits{MaxEntries: 5, MaxBytes: maxBytes, MaxItemBytes: 3})
	a := testAddress(1)
	b := testAddress(2)
	c := testAddress(3)
	mustPut(t, backend, a, "aa", testExpiry)
	mustPut(t, backend, b, "bb", testExpiry)
	mustPut(t, backend, c, "c", testExpiry)
	assertMiss(t, backend, a)
	if stats := backend.Stats(); stats.Entries != 2 || stats.ChargedBytes != 2*FixedEntryChargeBytes+3 {
		t.Fatalf("stats = %+v", stats)
	}
	mustPut(t, backend, b, "bbb", testExpiry)
	if stats := backend.Stats(); stats.ChargedBytes != 2*FixedEntryChargeBytes+4 {
		t.Fatalf("replacement stats = %+v", stats)
	}
	assertInvariant(t, backend)
}

func TestPutEvictsBeforeChargeAdditionCanOverflow(t *testing.T) {
	backend := mustBackend(t, Limits{MaxEntries: 2, MaxBytes: math.MaxInt64, MaxItemBytes: 1})
	oldAddress := testAddress(1)
	backend.mu.Lock()
	old := &entry{address: oldAddress, value: []byte("a"), charge: math.MaxInt64 - 100}
	backend.entries[oldAddress] = old
	backend.attachMRULocked(old)
	backend.charged = old.charge
	backend.mu.Unlock()

	newAddress := testAddress(2)
	if err := backend.Put(context.Background(), newAddress, []byte("b"), testExpiry); err != nil {
		t.Fatal(err)
	}
	assertMiss(t, backend, oldAddress)
	if got := string(mustGet(t, backend, newAddress, 1)); got != "b" {
		t.Fatalf("new value = %q", got)
	}
	if stats := backend.Stats(); stats.Entries != 1 || stats.ChargedBytes != FixedEntryChargeBytes+1 {
		t.Fatalf("stats = %+v", stats)
	}
	assertInvariant(t, backend)
}

func TestSaturatingChargeNeverWrapsObserverTotals(t *testing.T) {
	if result := saturatingCharge(math.MaxInt64-2, 3); result != math.MaxInt64 {
		t.Fatalf("overflow result = %d", result)
	}
	if result := saturatingCharge(11, 13); result != 24 {
		t.Fatalf("ordinary result = %d", result)
	}
	if result := saturatingCharge(-1, 1); result != math.MaxInt64 {
		t.Fatalf("invalid result = %d", result)
	}
}

func TestTTLBoundaryAndReplacement(t *testing.T) {
	clock := newFakeClock()
	backend := mustBackend(t, testLimits(2, 8), WithClock(clock))
	address := testAddress(1)
	expiry := cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: 10 * time.Second}
	mustPut(t, backend, address, "old", expiry)
	clock.Advance(10*time.Second - time.Nanosecond)
	mustGet(t, backend, address, 8)
	clock.Advance(time.Nanosecond)
	assertMiss(t, backend, address)
	if stats := backend.Stats(); stats.Entries != 0 || stats.ChargedBytes != 0 {
		t.Fatalf("expired stats = %+v", stats)
	}

	mustPut(t, backend, address, "old", expiry)
	clock.Advance(9 * time.Second)
	mustPut(t, backend, address, "new", expiry)
	clock.Advance(2 * time.Second)
	if got := string(mustGet(t, backend, address, 8)); got != "new" {
		t.Fatalf("replacement = %q", got)
	}
	clock.Advance(8 * time.Second)
	assertMiss(t, backend, address)
}

func TestCapacityOnlyDoesNotPhysicallyExpire(t *testing.T) {
	clock := newFakeClock()
	backend := mustBackend(t, testLimits(1, 8), WithClock(clock))
	address := testAddress(1)
	mustPut(t, backend, address, "value", cache.Expiry{Mode: cache.CapacityOnlyExpiry})
	clock.Advance(100 * 365 * 24 * time.Hour)
	if got := string(mustGet(t, backend, address, 8)); got != "value" {
		t.Fatalf("value = %q", got)
	}
}

func TestPutPurgesExpiredBeforeEvictingLiveLRU(t *testing.T) {
	clock := newFakeClock()
	backend := mustBackend(t, testLimits(2, 8), WithClock(clock))
	a := testAddress(1)
	b := testAddress(2)
	c := testAddress(3)
	mustPut(t, backend, a, "live", cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Hour})
	mustPut(t, backend, b, "dead", cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Second})
	clock.Advance(time.Second)
	mustPut(t, backend, c, "new", testExpiry)
	if got := string(mustGet(t, backend, a, 8)); got != "live" {
		t.Fatalf("a = %q", got)
	}
	assertMiss(t, backend, b)
	if got := string(mustGet(t, backend, c, 8)); got != "new" {
		t.Fatalf("c = %q", got)
	}
}

func TestGetManyBoundsDeduplicationOwnershipAndLRU(t *testing.T) {
	backend := mustBackend(t, testLimits(3, 8))
	ctx := context.Background()
	a := testAddress(1)
	b := testAddress(2)
	c := testAddress(3)
	d := testAddress(4)
	mustPut(t, backend, a, "aa", testExpiry)
	mustPut(t, backend, b, "bb", testExpiry)
	mustPut(t, backend, c, "cc", testExpiry)
	values, err := backend.GetMany(ctx, []cache.Address{a, a, d, b}, cache.BatchReadLimit{
		MaxItems: 4, MaxItemBytes: 2, MaxTotalBytes: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || string(values[a]) != "aa" || string(values[b]) != "bb" {
		t.Fatalf("values = %#v", values)
	}
	values[a][0] = 'X'
	if got := string(mustGet(t, backend, a, 8)); got != "aa" {
		t.Fatalf("owned value = %q", got)
	}
	mustPut(t, backend, d, "dd", testExpiry)
	assertMiss(t, backend, c)
	assertInvariant(t, backend)

	if values, err := backend.GetMany(ctx, []cache.Address{a, b, d}, cache.BatchReadLimit{
		MaxItems: 2, MaxItemBytes: 8, MaxTotalBytes: 16,
	}); !errors.Is(err, cache.ErrTooLarge) || values != nil {
		t.Fatalf("item limit: values=%v err=%v", values, err)
	}
}

func TestGetManyLimitFailureDoesNotPromoteLiveEntries(t *testing.T) {
	backend := mustBackend(t, testLimits(3, 2))
	ctx := context.Background()
	a := testAddress(1)
	b := testAddress(2)
	c := testAddress(3)
	d := testAddress(4)
	mustPut(t, backend, a, "aa", testExpiry)
	mustPut(t, backend, b, "bb", testExpiry)
	mustPut(t, backend, c, "cc", testExpiry)
	values, err := backend.GetMany(ctx, []cache.Address{a, b}, cache.BatchReadLimit{
		MaxItems: 2, MaxItemBytes: 2, MaxTotalBytes: 2,
	})
	if !errors.Is(err, cache.ErrTooLarge) || values != nil {
		t.Fatalf("values=%v err=%v", values, err)
	}
	mustPut(t, backend, d, "dd", testExpiry)
	assertMiss(t, backend, a)
}

func TestGetManySamplesClockOnce(t *testing.T) {
	clock := newFakeClock()
	backend := mustBackend(t, testLimits(2, 8), WithClock(clock))
	a := testAddress(1)
	b := testAddress(2)
	mustPut(t, backend, a, "a", testExpiry)
	mustPut(t, backend, b, "b", testExpiry)
	before := clock.Calls()
	if _, err := backend.GetMany(context.Background(), []cache.Address{a, b}, cache.BatchReadLimit{
		MaxItems: 2, MaxItemBytes: 8, MaxTotalBytes: 16,
	}); err != nil {
		t.Fatal(err)
	}
	if calls := clock.Calls() - before; calls != 1 {
		t.Fatalf("Now calls = %d", calls)
	}
}

func TestDeleteResetAndClose(t *testing.T) {
	backend := mustBackend(t, testLimits(2, 8))
	ctx := context.Background()
	a := testAddress(1)
	b := testAddress(2)
	mustPut(t, backend, a, "a", testExpiry)
	if err := backend.Delete(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := backend.Delete(ctx, a); err != nil {
		t.Fatal(err)
	}
	assertMiss(t, backend, a)
	mustPut(t, backend, a, "a", testExpiry)
	mustPut(t, backend, b, "b", testExpiry)
	if err := backend.Reset(); err != nil {
		t.Fatal(err)
	}
	if stats := backend.Stats(); stats.Entries != 0 || stats.ChargedBytes != 0 || stats.Closed {
		t.Fatalf("reset stats = %+v", stats)
	}
	mustPut(t, backend, a, "again", testExpiry)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if stats := backend.Stats(); !stats.Closed || stats.Entries != 0 || stats.ChargedBytes != 0 {
		t.Fatalf("closed stats = %+v", stats)
	}
	if _, _, err := backend.Get(ctx, a, cache.ReadLimit{MaxBytes: 8}); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("closed get: %v", err)
	}
	if err := backend.Put(ctx, a, []byte("x"), testExpiry); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("closed put: %v", err)
	}
	if err := backend.Delete(ctx, a); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("closed delete: %v", err)
	}
	if _, err := backend.GetMany(ctx, []cache.Address{a}, cache.BatchReadLimit{MaxItems: 1, MaxItemBytes: 8, MaxTotalBytes: 8}); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("closed get many: %v", err)
	}
	if err := backend.Reset(); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("closed reset: %v", err)
	}
}

func TestCancelledAndNilContextsDoNotMutate(t *testing.T) {
	backend := mustBackend(t, testLimits(1, 8))
	a := testAddress(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backend.Put(ctx, a, []byte("a"), testExpiry); !errors.Is(err, context.Canceled) {
		t.Fatalf("put: %v", err)
	}
	if stats := backend.Stats(); stats.Entries != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if _, _, err := backend.Get(nil, a, cache.ReadLimit{MaxBytes: 8}); !errors.Is(err, cache.ErrInvalid) {
		t.Fatalf("nil get: %v", err)
	}
	if err := backend.Delete(nil, a); !errors.Is(err, cache.ErrInvalid) {
		t.Fatalf("nil delete: %v", err)
	}
}

func TestObserverEventsPanicContainmentAndReentry(t *testing.T) {
	recorder := &eventRecorder{}
	limits := testLimits(1, 8)
	backend := mustBackend(t, limits, WithObserver(recorder))
	a := testAddress(1)
	b := testAddress(2)
	mustPut(t, backend, a, "a", testExpiry)
	mustPut(t, backend, b, "bb", testExpiry)
	events := recorder.Events()
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Operation != PutOperation || events[0].Outcome != StoredOutcome ||
		events[1].Operation != EvictOperation || events[1].Reason != MaxEntriesReason ||
		events[1].ValueBytes != 1 || events[1].ChargedBytes != FixedEntryChargeBytes+1 ||
		events[2].Operation != PutOperation || events[2].Outcome != StoredOutcome {
		t.Fatalf("events = %#v", events)
	}

	panicking := mustBackend(t, limits, WithObserver(observerFunc(func(context.Context, Event) {
		panic("observer")
	})))
	mustPut(t, panicking, a, "a", testExpiry)

	var reentered atomic.Bool
	var reentrant *Backend
	reentrant = mustBackend(t, limits, WithObserver(observerFunc(func(context.Context, Event) {
		if reentered.CompareAndSwap(false, true) {
			_ = reentrant.Stats()
		}
	})))
	mustPut(t, reentrant, a, "a", testExpiry)
	if !reentered.Load() {
		t.Fatal("observer did not reenter")
	}
}

func TestObserverRunsWithoutBackendLock(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var first atomic.Bool
	observer := observerFunc(func(context.Context, Event) {
		if first.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
	})
	backend := mustBackend(t, testLimits(2, 8), WithObserver(observer))
	a := testAddress(1)
	b := testAddress(2)
	done := make(chan error, 1)
	go func() {
		done <- backend.Put(context.Background(), a, []byte("a"), testExpiry)
	}()
	<-entered
	if err := backend.Put(context.Background(), b, []byte("b"), testExpiry); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestInvalidExpiryDoesNotMutate(t *testing.T) {
	backend := mustBackend(t, testLimits(1, 8))
	a := testAddress(1)
	invalid := []cache.Expiry{
		{},
		{Mode: cache.RelativeExpiry},
		{Mode: cache.CapacityOnlyExpiry, RetainFor: time.Second},
	}
	for _, expiry := range invalid {
		if err := backend.Put(context.Background(), a, []byte("a"), expiry); !errors.Is(err, cache.ErrInvalid) {
			t.Fatalf("expiry=%+v err=%v", expiry, err)
		}
	}
	if stats := backend.Stats(); stats.Entries != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestGetManyRejectsInvalidLimits(t *testing.T) {
	backend := mustBackend(t, testLimits(1, 8))
	invalid := []cache.BatchReadLimit{
		{},
		{MaxItems: 1, MaxItemBytes: 1},
		{MaxItems: 1, MaxItemBytes: 2, MaxTotalBytes: 1},
	}
	for _, limit := range invalid {
		if _, err := backend.GetMany(context.Background(), nil, limit); !errors.Is(err, cache.ErrInvalid) {
			t.Fatalf("limit=%+v err=%v", limit, err)
		}
	}
}

func TestGetManyReturnsIndependentSlices(t *testing.T) {
	backend := mustBackend(t, testLimits(2, 8))
	a := testAddress(1)
	b := testAddress(2)
	mustPut(t, backend, a, "same", testExpiry)
	mustPut(t, backend, b, "same", testExpiry)
	values, err := backend.GetMany(context.Background(), []cache.Address{a, b}, cache.BatchReadLimit{
		MaxItems: 2, MaxItemBytes: 8, MaxTotalBytes: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	values[a][0] = 'X'
	if bytes.Equal(values[a], values[b]) {
		t.Fatalf("values share mutation: %q %q", values[a], values[b])
	}
	if got := string(mustGet(t, backend, b, 8)); got != "same" {
		t.Fatalf("backend value = %q", got)
	}
}

func TestGetManyChecksCancellationDuringDedupe(t *testing.T) {
	backend := mustBackend(t, testLimits(192, 8))
	addresses := make([]cache.Address, 192)
	for index := range addresses {
		addresses[index] = testAddress(byte(index + 1))
	}
	ctx := newStepCancelContext(3)
	values, err := backend.GetMany(ctx, addresses, cache.BatchReadLimit{
		MaxItems: 192, MaxItemBytes: 8, MaxTotalBytes: 192 * 8,
	})
	if !errors.Is(err, context.Canceled) || values != nil {
		t.Fatalf("GetMany() values=%v error=%v", values, err)
	}
}

func TestGetManyChecksCancellationWhileLocked(t *testing.T) {
	clock := newFakeClock()
	recorder := &eventRecorder{}
	backend := mustBackend(t, testLimits(192, 8), WithClock(clock), WithObserver(recorder))
	addresses := make([]cache.Address, 192)
	shortExpiry := cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Second}
	for index := range addresses {
		addresses[index] = testAddress(byte(index + 1))
		mustPut(t, backend, addresses[index], "value", shortExpiry)
	}
	before := len(recorder.Events())
	clock.Advance(time.Second)
	ctx := newStepCancelContext(7)
	values, err := backend.GetMany(ctx, addresses, cache.BatchReadLimit{
		MaxItems: 192, MaxItemBytes: 8, MaxTotalBytes: 192 * 8,
	})
	if !errors.Is(err, context.Canceled) || values != nil {
		t.Fatalf("GetMany() values=%v error=%v", values, err)
	}
	events := recorder.Events()[before:]
	if len(events) != 64 {
		t.Fatalf("eviction events = %d", len(events))
	}
	for _, event := range events {
		if event.Operation != EvictOperation || event.Reason != ExpiredReason {
			t.Fatalf("event = %+v", event)
		}
	}
	if stats := backend.Stats(); stats.Entries != 128 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestPutChecksCancellationWhilePurging(t *testing.T) {
	clock := newFakeClock()
	recorder := &eventRecorder{}
	backend := mustBackend(t, testLimits(193, 8), WithClock(clock), WithObserver(recorder))
	shortExpiry := cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Second}
	for index := 0; index < 192; index++ {
		mustPut(t, backend, testAddress(byte(index+1)), "value", shortExpiry)
	}
	before := len(recorder.Events())
	clock.Advance(time.Second)
	ctx := newStepCancelContext(5)
	address := testAddress(250)
	err := backend.Put(ctx, address, []byte("new"), testExpiry)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put() error = %v", err)
	}
	events := recorder.Events()[before:]
	if len(events) != 64 {
		t.Fatalf("eviction events = %d", len(events))
	}
	backend.mu.Lock()
	_, exists := backend.entries[address]
	entries := len(backend.entries)
	backend.mu.Unlock()
	if exists || entries != 128 {
		t.Fatalf("new exists=%t entries=%d", exists, entries)
	}
}

func TestPutCancellationDuringEvictionPlanningPreservesLiveEntries(t *testing.T) {
	const entries = 192
	limits := Limits{
		MaxEntries:   entries,
		MaxBytes:     entries * (FixedEntryChargeBytes + 1),
		MaxItemBytes: 30_000,
	}
	backend := mustBackend(t, limits)
	for index := 0; index < entries; index++ {
		mustPut(t, backend, testAddress(byte(index+1)), "a", testExpiry)
	}
	before := backend.Stats()
	ctx := newStepCancelContext(8)
	newAddress := testAddress(250)
	err := backend.Put(ctx, newAddress, bytes.Repeat([]byte{'b'}, limits.MaxItemBytes), testExpiry)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put() error = %v", err)
	}
	after := backend.Stats()
	if after != before {
		t.Fatalf("stats changed: before=%+v after=%+v", before, after)
	}
	backend.mu.Lock()
	_, exists := backend.entries[newAddress]
	backend.mu.Unlock()
	if exists {
		t.Fatal("canceled value was stored")
	}
	assertInvariant(t, backend)
}

func mustBackend(t *testing.T, limits Limits, options ...Option) *Backend {
	t.Helper()
	backend, err := New(limits, options...)
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func testLimits(entries, itemBytes int) Limits {
	return Limits{
		MaxEntries:   entries,
		MaxBytes:     int64(entries) * (FixedEntryChargeBytes + int64(itemBytes)),
		MaxItemBytes: itemBytes,
	}
}

func testAddress(value byte) cache.Address {
	var address cache.Address
	address.KeyDigest[len(address.KeyDigest)-1] = value
	return address
}

func mustPut(t *testing.T, backend *Backend, address cache.Address, value string, expiry cache.Expiry) {
	t.Helper()
	if err := backend.Put(context.Background(), address, []byte(value), expiry); err != nil {
		t.Fatal(err)
	}
}

func mustGet(t *testing.T, backend *Backend, address cache.Address, maxBytes int) []byte {
	t.Helper()
	value, found, err := backend.Get(context.Background(), address, cache.ReadLimit{MaxBytes: maxBytes})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("address %v missed", address)
	}
	return value
}

func assertMiss(t *testing.T, backend *Backend, address cache.Address) {
	t.Helper()
	value, found, err := backend.Get(context.Background(), address, cache.ReadLimit{MaxBytes: backend.limits.MaxItemBytes})
	if err != nil {
		t.Fatal(err)
	}
	if found || value != nil {
		t.Fatalf("found=%t value=%q", found, value)
	}
}

func assertInvariant(t *testing.T, backend *Backend) {
	t.Helper()
	backend.mu.Lock()
	defer backend.mu.Unlock()
	seen := make(map[*entry]struct{}, len(backend.entries))
	var charged int64
	var newer *entry
	count := 0
	for item := backend.lru; item != nil; item = item.newer {
		if _, exists := seen[item]; exists {
			t.Fatal("cycle in LRU")
		}
		seen[item] = struct{}{}
		if item.older != newer {
			t.Fatal("broken older link")
		}
		if backend.entries[item.address] != item {
			t.Fatal("list entry missing from map")
		}
		charged += item.charge
		newer = item
		count++
	}
	if newer != backend.mru {
		t.Fatal("MRU mismatch")
	}
	if count != len(backend.entries) || count > backend.limits.MaxEntries {
		t.Fatalf("entries: list=%d map=%d", count, len(backend.entries))
	}
	if charged != backend.charged || charged > backend.limits.MaxBytes {
		t.Fatalf("charge: list=%d backend=%d", charged, backend.charged)
	}
}
