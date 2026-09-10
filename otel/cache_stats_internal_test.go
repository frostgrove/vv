package vvotel

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

func TestCacheMemoryStatsAggregateAndLifecycle(t *testing.T) {
	meter := &cacheStatsTestMeter{}
	tel, signals := cacheStatsTelemetry(meter, cacheStatsAllSignals()...)
	active := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 3, MaxBytes: 4096, MaxItemBytes: 64})
	closed := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 7, MaxBytes: 8192, MaxItemBytes: 64})
	var address cache.Address
	address.KeyDigest[0] = 1
	if err := active.Put(context.Background(), address, []byte("value"), cache.Expiry{Mode: cache.CapacityOnlyExpiry}); err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}

	registration, err := CacheMemoryStats(tel, active, closed)
	if err != nil {
		t.Fatal(err)
	}
	observer := newCacheStatsObserver(signals)
	if err := meter.invoke(context.Background(), observer); err != nil {
		t.Fatal(err)
	}
	values := observer.valuesSnapshot()
	want := map[Signal]int64{
		SignalCacheMemoryEntries:    1,
		SignalCacheMemoryBytes:      cachememory.FixedEntryChargeBytes + 5,
		SignalCacheMemoryEntryLimit: 3,
		SignalCacheMemoryByteLimit:  4096,
		SignalCacheMemoryActive:     1,
		SignalCacheMemoryClosed:     1,
	}
	for signal, value := range want {
		observations := values[signal]
		if len(observations) != 1 || observations[0].value != value || observations[0].attributes[AttrComponent].AsString() != ComponentCacheMemory {
			t.Errorf("signal %d observations = %#v, want %d", signal, observations, value)
		}
	}

	state := registration.(*cacheMemoryRegistration).state
	if err := registration.Unregister(); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	if state.backends != nil || state.gauges != nil || state.native != nil || state.slot != nil || state.token != 0 || !state.done || state.active {
		t.Errorf("teardown retained state: %+v", state)
	}
	state.mu.Unlock()
	before := observer.count()
	if err := meter.invoke(context.Background(), observer); err != nil {
		t.Fatal(err)
	}
	if observer.count() != before {
		t.Fatal("retained callback observed after unregister")
	}

	replacement, err := CacheMemoryStats(tel, active)
	if err != nil {
		t.Fatalf("singleton token was not released: %v", err)
	}
	if err := replacement.Unregister(); err != nil {
		t.Fatal(err)
	}
}

func TestCacheMemoryStatsClosedOnlyOmitsOccupancyAndCapacity(t *testing.T) {
	meter := &cacheStatsTestMeter{}
	tel, signals := cacheStatsTelemetry(meter, cacheStatsAllSignals()...)
	backend := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 3, MaxBytes: 4096, MaxItemBytes: 64})
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	registration, err := CacheMemoryStats(tel, backend)
	if err != nil {
		t.Fatal(err)
	}
	defer registration.Unregister()
	observer := newCacheStatsObserver(signals)
	if err := meter.invoke(context.Background(), observer); err != nil {
		t.Fatal(err)
	}
	values := observer.valuesSnapshot()
	if len(values) != 2 || values[SignalCacheMemoryActive][0].value != 0 || values[SignalCacheMemoryClosed][0].value != 1 {
		t.Fatalf("closed-only observations = %#v", values)
	}
}

func TestCacheMemoryStatsInputBoundsAndDisabledInertPath(t *testing.T) {
	invalidBackends := make([]*cachememory.Backend, MaxCacheMemoryStatsBackends+1)
	if registration, err := CacheMemoryStats(nil, invalidBackends...); err != nil || registration == nil || registration.Unregister() != nil {
		t.Fatalf("nil telemetry inert result = %T, %v", registration, err)
	}

	disabledMeter := &cacheStatsTestMeter{}
	disabled, _ := cacheStatsTelemetry(disabledMeter)
	if registration, err := CacheMemoryStats(disabled, invalidBackends...); err != nil || registration == nil || registration.Unregister() != nil {
		t.Fatalf("disabled inert result = %T, %v", registration, err)
	}
	if disabledMeter.registerCalls() != 0 || cacheStatsSlotActive(disabled.cacheMemoryStats) {
		t.Fatal("disabled path touched provider or singleton")
	}

	tests := []struct {
		name     string
		backends []*cachememory.Backend
		kind     error
	}{
		{name: "zero", kind: ErrInvalidRegistration},
		{name: "nil", backends: []*cachememory.Backend{nil}, kind: ErrInvalidRegistration},
		{name: "sixty five", backends: invalidBackends, kind: ErrInvalidRegistration},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meter := &cacheStatsTestMeter{}
			tel, _ := cacheStatsTelemetry(meter, cacheStatsAllSignals()...)
			registration, err := CacheMemoryStats(tel, test.backends...)
			assertCacheRegistrationError(t, err, test.kind, false)
			if registration != nil || meter.registerCalls() != 0 || cacheStatsSlotActive(tel.cacheMemoryStats) {
				t.Fatalf("rejected result touched state: registration=%T calls=%d active=%v", registration, meter.registerCalls(), cacheStatsSlotActive(tel.cacheMemoryStats))
			}
		})
	}

	backend := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 1, MaxBytes: 1024, MaxItemBytes: 64})
	meter := &cacheStatsTestMeter{}
	tel, _ := cacheStatsTelemetry(meter, cacheStatsAllSignals()...)
	registration, err := CacheMemoryStats(tel, backend, backend)
	assertCacheRegistrationError(t, err, ErrDuplicateRegistration, false)
	if registration != nil || meter.registerCalls() != 0 || cacheStatsSlotActive(tel.cacheMemoryStats) {
		t.Fatal("duplicate pointer touched provider or singleton")
	}
	registration, err = CacheMemoryStats(tel, backend)
	if err != nil {
		t.Fatalf("duplicate rejection poisoned singleton: %v", err)
	}
	_ = registration.Unregister()

	overflowA := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 1, MaxBytes: math.MaxInt64, MaxItemBytes: 64})
	overflowB := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 1, MaxBytes: math.MaxInt64, MaxItemBytes: 64})
	meter = &cacheStatsTestMeter{}
	tel, _ = cacheStatsTelemetry(meter, cacheStatsAllSignals()...)
	registration, err = CacheMemoryStats(tel, overflowA, overflowB)
	assertCacheRegistrationError(t, err, ErrInvalidRegistration, false)
	if registration != nil || meter.registerCalls() != 0 || cacheStatsSlotActive(tel.cacheMemoryStats) {
		t.Fatal("overflow touched provider or singleton")
	}
}

func TestCacheMemoryStatsSixtyFourBackendBound(t *testing.T) {
	backends := make([]*cachememory.Backend, MaxCacheMemoryStatsBackends)
	for index := range backends {
		backends[index] = cacheStatsBackend(t, cachememory.Limits{MaxEntries: 1, MaxBytes: 1024, MaxItemBytes: 64})
	}
	meter := &cacheStatsTestMeter{}
	tel, _ := cacheStatsTelemetry(meter, SignalCacheMemoryActive)
	registration, err := CacheMemoryStats(tel, backends...)
	if err != nil {
		t.Fatal(err)
	}
	if meter.registerCalls() != 1 || len(meter.instrumentsSnapshot()) != 1 {
		t.Fatalf("registration calls/instruments = %d/%d", meter.registerCalls(), len(meter.instrumentsSnapshot()))
	}
	if err := registration.Unregister(); err != nil {
		t.Fatal(err)
	}
}

func TestCacheMemoryStatsTelemetryValueCopiesShareSingleton(t *testing.T) {
	meter := &cacheStatsTestMeter{}
	tel, _ := cacheStatsTelemetry(meter, SignalCacheMemoryActive)
	copyValue := *tel
	backend := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 1, MaxBytes: 1024, MaxItemBytes: 64})
	first, err := CacheMemoryStats(tel, backend)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CacheMemoryStats(&copyValue, backend)
	assertCacheRegistrationError(t, err, ErrDuplicateRegistration, false)
	if second != nil || meter.registerCalls() != 1 {
		t.Fatal("value copy registered a second callback")
	}
	if err := first.Unregister(); err != nil {
		t.Fatal(err)
	}
	second, err = CacheMemoryStats(&copyValue, backend)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Unregister()
}

func TestCacheMemoryStatsUnavailableSnapshotAndDuplicatePrecedence(t *testing.T) {
	backend := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 2, MaxBytes: 2048, MaxItemBytes: 64})
	locked := &cacheStatsNthBlockingContext{at: 3, entered: make(chan struct{}), release: make(chan struct{})}
	putDone := make(chan error, 1)
	go func() {
		var address cache.Address
		address.KeyDigest[0] = 1
		putDone <- backend.Put(locked, address, []byte("value"), cache.Expiry{Mode: cache.CapacityOnlyExpiry})
	}()
	<-locked.entered

	meter := &cacheStatsTestMeter{}
	tel, _ := cacheStatsTelemetry(meter, SignalCacheMemoryActive)
	registration, err := CacheMemoryStats(tel, backend, backend)
	assertCacheRegistrationError(t, err, ErrDuplicateRegistration, false)
	if registration != nil || meter.registerCalls() != 0 || cacheStatsSlotActive(tel.cacheMemoryStats) {
		t.Fatal("duplicate pointer proceeded to snapshot or registration")
	}
	registration, err = CacheMemoryStats(tel, backend)
	assertCacheRegistrationError(t, err, ErrInvalidRegistration, false)
	if registration != nil || meter.registerCalls() != 0 || cacheStatsSlotActive(tel.cacheMemoryStats) {
		t.Fatal("unavailable snapshot proceeded to registration")
	}
	close(locked.release)
	if err := <-putDone; err != nil {
		t.Fatal(err)
	}

	registration, err = CacheMemoryStats(tel, backend)
	if err != nil {
		t.Fatal(err)
	}
	observer := newCacheStatsObserver(map[metric.Int64Observable]Signal{tel.int64Gauge(SignalCacheMemoryActive): SignalCacheMemoryActive})
	locked = &cacheStatsNthBlockingContext{at: 3, entered: make(chan struct{}), release: make(chan struct{})}
	go func() {
		var address cache.Address
		address.KeyDigest[0] = 2
		putDone <- backend.Put(locked, address, []byte("next"), cache.Expiry{Mode: cache.CapacityOnlyExpiry})
	}()
	<-locked.entered
	if err := meter.invoke(context.Background(), observer); err != nil || observer.count() != 0 {
		t.Fatalf("contended collection = %v, %d observations", err, observer.count())
	}
	close(locked.release)
	if err := <-putDone; err != nil {
		t.Fatal(err)
	}
	_ = registration.Unregister()
}

func TestCacheMemoryStatsRegistrationFailureCleanupAndRedaction(t *testing.T) {
	nativeRegisterError := &cacheStatsHostileError{secret: "register-secret"}
	nativeCleanupError := &cacheStatsHostileError{secret: "cleanup-secret"}
	tests := []struct {
		name           string
		mode           string
		registerErr    error
		cleanupErr     error
		panicCleanup   bool
		wantCleanup    bool
		wantUnregister int64
	}{
		{name: "nil result", mode: "nil"},
		{name: "typed nil result", mode: "typed_nil"},
		{name: "nil with error", mode: "nil", registerErr: nativeRegisterError},
		{name: "partial with error", registerErr: nativeRegisterError, wantUnregister: 1},
		{name: "partial cleanup error", registerErr: nativeRegisterError, cleanupErr: nativeCleanupError, wantCleanup: true, wantUnregister: 1},
		{name: "partial cleanup panic", registerErr: nativeRegisterError, panicCleanup: true, wantCleanup: true, wantUnregister: 1},
		{name: "register panic", mode: "panic"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			native := &cacheStatsNativeRegistration{err: test.cleanupErr, panicUnregister: test.panicCleanup}
			meter := &cacheStatsTestMeter{mode: test.mode, err: test.registerErr, native: native}
			tel, signals := cacheStatsTelemetry(meter, SignalCacheMemoryActive)
			backend := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 1, MaxBytes: 1024, MaxItemBytes: 64})
			registration, err := CacheMemoryStats(tel, backend)
			assertCacheRegistrationError(t, err, ErrCallbackRegistration, test.wantCleanup)
			if registration != nil || native.calls.Load() != test.wantUnregister || cacheStatsSlotActive(tel.cacheMemoryStats) {
				t.Fatalf("failure cleanup = registration %T, calls %d, slot %v", registration, native.calls.Load(), cacheStatsSlotActive(tel.cacheMemoryStats))
			}
			if errors.Is(err, nativeRegisterError) || errors.Is(err, nativeCleanupError) {
				t.Fatal("native error was unwrapped")
			}
			_ = err.Error()
			observer := newCacheStatsObserver(signals)
			if invokeErr := meter.invoke(context.Background(), observer); invokeErr != nil || observer.count() != 0 {
				t.Fatalf("retained callback after failure = %v, %d observations", invokeErr, observer.count())
			}
		})
	}
}

func TestCacheMemoryStatsPartialFailureDeactivatesThenDrains(t *testing.T) {
	meter := &cacheStatsTestMeter{err: errors.New("register failure")}
	tel, signals := cacheStatsTelemetry(meter, SignalCacheMemoryActive)
	backend := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 1, MaxBytes: 1024, MaxItemBytes: 64})
	observer := newCacheStatsObserver(signals)
	ctx := &cacheStatsBlockingContext{entered: make(chan struct{}), release: make(chan struct{})}
	callbackDone := make(chan error, 1)
	meter.beforeReturn = func(callback metric.Callback) {
		go func() { callbackDone <- callback(ctx, observer) }()
		<-ctx.entered
	}
	meter.native = &cacheStatsNativeRegistration{onUnregister: func() {
		_ = meter.invoke(context.Background(), observer)
	}}
	result := make(chan error, 1)
	go func() {
		_, err := CacheMemoryStats(tel, backend)
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("constructor returned before admitted callback drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(ctx.release)
	if err := <-callbackDone; err != nil {
		t.Fatal(err)
	}
	err := <-result
	assertCacheRegistrationError(t, err, ErrCallbackRegistration, false)
	if observer.count() != 1 || meter.native.calls.Load() != 1 || cacheStatsSlotActive(tel.cacheMemoryStats) {
		t.Fatalf("failure cleanup observations/calls/slot = %d/%d/%v", observer.count(), meter.native.calls.Load(), cacheStatsSlotActive(tel.cacheMemoryStats))
	}
}

func TestCacheMemoryStatsUnregisterIsConcurrentIdempotentAndCached(t *testing.T) {
	native := &cacheStatsNativeRegistration{
		err:     errors.New("native cleanup"),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	meter := &cacheStatsTestMeter{native: native}
	tel, _ := cacheStatsTelemetry(meter, SignalCacheMemoryActive)
	backend := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 1, MaxBytes: 1024, MaxItemBytes: 64})
	registration, err := CacheMemoryStats(tel, backend)
	if err != nil {
		t.Fatal(err)
	}

	const callers = 16
	results := make(chan error, callers)
	for range callers {
		go func() { results <- registration.Unregister() }()
	}
	<-native.entered
	if native.calls.Load() != 1 {
		t.Fatalf("native unregister calls = %d", native.calls.Load())
	}
	close(native.release)
	for range callers {
		if got := <-results; got != ErrCallbackUnregister {
			t.Fatalf("Unregister result = %v", got)
		}
	}
	if native.calls.Load() != 1 || registration.Unregister() != ErrCallbackUnregister {
		t.Fatalf("cached teardown calls/result = %d/%v", native.calls.Load(), registration.Unregister())
	}
}

func TestCacheMemoryStatsUnregisterDrainsAdmittedCallback(t *testing.T) {
	meter := &cacheStatsTestMeter{}
	tel, signals := cacheStatsTelemetry(meter, SignalCacheMemoryActive)
	backend := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 1, MaxBytes: 1024, MaxItemBytes: 64})
	registration, err := CacheMemoryStats(tel, backend)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &cacheStatsBlockingContext{entered: make(chan struct{}), release: make(chan struct{})}
	callbackDone := make(chan error, 1)
	observer := newCacheStatsObserver(signals)
	go func() { callbackDone <- meter.invoke(ctx, observer) }()
	<-ctx.entered
	unregisterDone := make(chan error, 1)
	go func() { unregisterDone <- registration.Unregister() }()
	select {
	case err := <-unregisterDone:
		t.Fatalf("Unregister returned before callback drain: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(ctx.release)
	if err := <-callbackDone; err != nil {
		t.Fatal(err)
	}
	if err := <-unregisterDone; err != nil {
		t.Fatal(err)
	}
	before := observer.count()
	if err := meter.invoke(context.Background(), observer); err != nil || observer.count() != before {
		t.Fatal("callback admitted after teardown")
	}
}

func TestCacheMemoryStatsCallbacksAreCanceledConcurrentReentrantAndPanicIsolated(t *testing.T) {
	meter := &cacheStatsTestMeter{}
	tel, signals := cacheStatsTelemetry(meter, cacheStatsAllSignals()...)
	backend := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 1, MaxBytes: 1024, MaxItemBytes: 64})
	registration, err := CacheMemoryStats(tel, backend)
	if err != nil {
		t.Fatal(err)
	}
	defer registration.Unregister()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	observer := newCacheStatsObserver(signals)
	_ = meter.invoke(canceled, observer)
	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	_ = meter.invoke(deadline, observer)
	if observer.count() != 0 {
		t.Fatal("canceled callback emitted observations")
	}
	admissionCanceled := &cacheStatsCancelAfterContext{after: 5}
	_ = meter.invoke(admissionCanceled, observer)
	if observer.count() != 0 || admissionCanceled.calls.Load() != 5 {
		t.Fatalf("final admission cancellation emitted observations: count=%d calls=%d", observer.count(), admissionCanceled.calls.Load())
	}
	emissionContext, cancelEmission := context.WithCancel(context.Background())
	observer.reenter = cancelEmission
	_ = meter.invoke(emissionContext, observer)
	if observer.count() != 6 {
		t.Fatalf("cancellation after emission start truncated fixed sequence: %d", observer.count())
	}
	observer = newCacheStatsObserver(signals)

	observer.panicSignal = SignalCacheMemoryEntries
	if err := meter.invoke(context.Background(), observer); err != nil {
		t.Fatal(err)
	}
	if observer.count() != 5 {
		t.Fatalf("observer panic suppressed later gauges: count=%d", observer.count())
	}

	observer = newCacheStatsObserver(signals)
	const callbacks = 32
	var wait sync.WaitGroup
	wait.Add(callbacks)
	for range callbacks {
		go func() {
			defer wait.Done()
			_ = meter.invoke(context.Background(), observer)
		}()
	}
	wait.Wait()
	if observer.count() == 0 || observer.count() > callbacks*6 || observer.count()%6 != 0 {
		t.Fatalf("concurrent observations = %d", observer.count())
	}

	nested := newCacheStatsObserver(signals)
	outer := newCacheStatsObserver(signals)
	outer.reenter = func() { _ = meter.invoke(context.Background(), nested) }
	if err := meter.invoke(context.Background(), outer); err != nil {
		t.Fatal(err)
	}
	if outer.count() != 6 || nested.count() != 6 {
		t.Fatalf("reentrant counts = %d/%d", outer.count(), nested.count())
	}
}

func TestMustCacheMemoryStatsPanicsWithExactError(t *testing.T) {
	meter := &cacheStatsTestMeter{mode: "nil"}
	tel, _ := cacheStatsTelemetry(meter, SignalCacheMemoryActive)
	backend := cacheStatsBackend(t, cachememory.Limits{MaxEntries: 1, MaxBytes: 1024, MaxItemBytes: 64})
	_, expected := CacheMemoryStats(tel, backend)

	meter = &cacheStatsTestMeter{mode: "nil"}
	tel, _ = cacheStatsTelemetry(meter, SignalCacheMemoryActive)
	defer func() {
		recovered := recover()
		got, ok := recovered.(error)
		if !ok || got.Error() != expected.Error() {
			t.Fatalf("panic = %T %v, want framework error %v", recovered, recovered, expected)
		}
		var typed *cacheRegistrationError
		if !errors.As(got, &typed) {
			t.Fatalf("panic type = %T, want *cacheRegistrationError", got)
		}
		assertCacheRegistrationError(t, got, ErrCallbackRegistration, false)
	}()
	MustCacheMemoryStats(tel, backend)
}

func assertCacheRegistrationError(t *testing.T, err error, kind error, cleanup bool) {
	t.Helper()
	if err == nil || !errors.Is(err, ErrAssembly) || !errors.Is(err, kind) || errors.Is(err, ErrInvalidRegistration) != (kind == ErrInvalidRegistration) || errors.Is(err, ErrDuplicateRegistration) != (kind == ErrDuplicateRegistration) || errors.Is(err, ErrCallbackRegistration) != (kind == ErrCallbackRegistration) || errors.Is(err, ErrCallbackUnregister) != cleanup {
		t.Fatalf("error set for %v = %v", kind, err)
	}
}

func cacheStatsBackend(t *testing.T, limits cachememory.Limits) *cachememory.Backend {
	t.Helper()
	backend, err := cachememory.New(limits)
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func cacheStatsAllSignals() []Signal {
	signals := cacheMemoryGaugeSignals()
	return signals[:]
}

func cacheStatsTelemetry(meter *cacheStatsTestMeter, enabled ...Signal) (*Telemetry, map[metric.Int64Observable]Signal) {
	tel := &Telemetry{
		activeSignals:        make(map[Signal]struct{}, len(enabled)),
		meter:                meter,
		int64ObservableGauge: make(map[Signal]metric.Int64ObservableGauge, len(enabled)),
		cacheMemoryStats:     &cacheMemoryStatsSlot{},
	}
	byInstrument := make(map[metric.Int64Observable]Signal, len(enabled))
	for _, signal := range enabled {
		gauge := &cacheStatsGauge{signal: signal}
		tel.activeSignals[signal] = struct{}{}
		tel.int64ObservableGauge[signal] = gauge
		byInstrument[gauge] = signal
	}
	return tel, byInstrument
}

func cacheStatsSlotActive(slot *cacheMemoryStatsSlot) bool {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.active != 0
}

type cacheStatsGauge struct {
	metricnoop.Int64ObservableGauge
	signal Signal
}

type cacheStatsTestMeter struct {
	metricnoop.Meter
	mu           sync.Mutex
	callback     metric.Callback
	instruments  []metric.Observable
	calls        int
	mode         string
	err          error
	native       *cacheStatsNativeRegistration
	beforeReturn func(metric.Callback)
}

func (m *cacheStatsTestMeter) RegisterCallback(callback metric.Callback, instruments ...metric.Observable) (metric.Registration, error) {
	m.mu.Lock()
	m.calls++
	m.callback = callback
	m.instruments = append([]metric.Observable(nil), instruments...)
	mode := m.mode
	err := m.err
	native := m.native
	if native == nil {
		native = &cacheStatsNativeRegistration{}
		m.native = native
	}
	m.mu.Unlock()
	if m.beforeReturn != nil {
		m.beforeReturn(callback)
	}
	switch mode {
	case "nil":
		return nil, err
	case "typed_nil":
		var registration *cacheStatsNativeRegistration
		return registration, err
	case "panic":
		panic("register-secret")
	default:
		return native, err
	}
}

func (m *cacheStatsTestMeter) invoke(ctx context.Context, observer metric.Observer) error {
	m.mu.Lock()
	callback := m.callback
	m.mu.Unlock()
	if callback == nil {
		return nil
	}
	return callback(ctx, observer)
}

func (m *cacheStatsTestMeter) registerCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *cacheStatsTestMeter) instrumentsSnapshot() []metric.Observable {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]metric.Observable(nil), m.instruments...)
}

type cacheStatsNativeRegistration struct {
	metricnoop.Registration
	calls           atomic.Int64
	err             error
	panicUnregister bool
	entered         chan struct{}
	release         chan struct{}
	onUnregister    func()
}

func (r *cacheStatsNativeRegistration) Unregister() error {
	r.calls.Add(1)
	if r.onUnregister != nil {
		r.onUnregister()
	}
	if r.entered != nil {
		close(r.entered)
	}
	if r.release != nil {
		<-r.release
	}
	if r.panicUnregister {
		panic("cleanup-secret")
	}
	return r.err
}

type cacheStatsObservation struct {
	value      int64
	attributes map[attribute.Key]attribute.Value
}

type cacheStatsObserver struct {
	metricnoop.Observer
	mu          sync.Mutex
	signals     map[metric.Int64Observable]Signal
	values      map[Signal][]cacheStatsObservation
	panicSignal Signal
	reenter     func()
	reentered   atomic.Bool
}

func newCacheStatsObserver(signals map[metric.Int64Observable]Signal) *cacheStatsObserver {
	return &cacheStatsObserver{signals: signals, values: make(map[Signal][]cacheStatsObservation)}
}

func (o *cacheStatsObserver) ObserveInt64(instrument metric.Int64Observable, value int64, options ...metric.ObserveOption) {
	signal := o.signals[instrument]
	if o.reenter != nil && o.reentered.CompareAndSwap(false, true) {
		o.reenter()
	}
	if signal == o.panicSignal {
		panic("observer-secret")
	}
	config := metric.NewObserveConfig(options)
	attributes := make(map[attribute.Key]attribute.Value)
	set := config.Attributes()
	for _, current := range set.ToSlice() {
		attributes[current.Key] = current.Value
	}
	o.mu.Lock()
	o.values[signal] = append(o.values[signal], cacheStatsObservation{value: value, attributes: attributes})
	o.mu.Unlock()
}

func (o *cacheStatsObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	count := 0
	for _, values := range o.values {
		count += len(values)
	}
	return count
}

func (o *cacheStatsObserver) valuesSnapshot() map[Signal][]cacheStatsObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	result := make(map[Signal][]cacheStatsObservation, len(o.values))
	for signal, values := range o.values {
		result[signal] = append([]cacheStatsObservation(nil), values...)
	}
	return result
}

type cacheStatsBlockingContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *cacheStatsBlockingContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cacheStatsBlockingContext) Done() <-chan struct{}       { return nil }
func (c *cacheStatsBlockingContext) Value(any) any               { return nil }
func (c *cacheStatsBlockingContext) Err() error {
	c.once.Do(func() {
		close(c.entered)
		<-c.release
	})
	return nil
}

type cacheStatsHostileError struct {
	secret string
}

func (e *cacheStatsHostileError) Error() string { panic(e.secret) }

type cacheStatsNthBlockingContext struct {
	at      int64
	calls   atomic.Int64
	entered chan struct{}
	release chan struct{}
}

func (*cacheStatsNthBlockingContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*cacheStatsNthBlockingContext) Done() <-chan struct{}       { return nil }
func (*cacheStatsNthBlockingContext) Value(any) any               { return nil }
func (c *cacheStatsNthBlockingContext) Err() error {
	if c.calls.Add(1) == c.at {
		close(c.entered)
		<-c.release
	}
	return nil
}

type cacheStatsCancelAfterContext struct {
	after int64
	calls atomic.Int64
}

func (*cacheStatsCancelAfterContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*cacheStatsCancelAfterContext) Done() <-chan struct{}       { return nil }
func (*cacheStatsCancelAfterContext) Value(any) any               { return nil }
func (c *cacheStatsCancelAfterContext) Err() error {
	if c.calls.Add(1) >= c.after {
		return context.Canceled
	}
	return nil
}
