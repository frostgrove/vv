package cache

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

const coordinationTestTimeout = 10 * time.Second

type coordinationGetHook func(context.Context, Address, ReadLimit, int) ([]byte, bool, error)
type coordinationPutHook func(context.Context, Address, []byte, Expiry, int) error
type coordinationDeleteHook func(context.Context, Address, int) error

type coordinationBackend struct {
	mu          sync.Mutex
	values      map[Address][]byte
	getCalls    int
	putCalls    int
	deleteCalls int
	getHook     coordinationGetHook
	putHook     coordinationPutHook
	deleteHook  coordinationDeleteHook
}

func newCoordinationBackend() *coordinationBackend {
	return &coordinationBackend{values: make(map[Address][]byte)}
}

func (backend *coordinationBackend) DescribeBackend() BackendDescription {
	return BackendDescription{
		Name:              "coordination-test",
		Topology:          ProcessBackend,
		ExpiryClock:       ProcessExpiryClock,
		MaxItemBytes:      64 << 10,
		RelativeExpiry:    true,
		MaxRelativeExpiry: 24 * time.Hour,
		CapacityBounded:   true,
	}
}

func (backend *coordinationBackend) Get(ctx context.Context, address Address, limit ReadLimit) ([]byte, bool, error) {
	backend.mu.Lock()
	backend.getCalls++
	call := backend.getCalls
	hook := backend.getHook
	backend.mu.Unlock()
	if hook != nil {
		return hook(ctx, address, limit, call)
	}
	return backend.load(ctx, address)
}

func (backend *coordinationBackend) Put(ctx context.Context, address Address, value []byte, expiry Expiry) error {
	backend.mu.Lock()
	backend.putCalls++
	call := backend.putCalls
	hook := backend.putHook
	backend.mu.Unlock()
	if hook != nil {
		return hook(ctx, address, bytes.Clone(value), expiry, call)
	}
	return backend.store(ctx, address, value)
}

func (backend *coordinationBackend) Delete(ctx context.Context, address Address) error {
	backend.mu.Lock()
	backend.deleteCalls++
	call := backend.deleteCalls
	hook := backend.deleteHook
	backend.mu.Unlock()
	if hook != nil {
		return hook(ctx, address, call)
	}
	return backend.remove(ctx, address)
}

func (backend *coordinationBackend) load(ctx context.Context, address Address) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	value, found := backend.values[address]
	return bytes.Clone(value), found, nil
}

func (backend *coordinationBackend) store(ctx context.Context, address Address, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	backend.mu.Lock()
	backend.values[address] = bytes.Clone(value)
	backend.mu.Unlock()
	return nil
}

func (backend *coordinationBackend) remove(ctx context.Context, address Address) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	backend.mu.Lock()
	delete(backend.values, address)
	backend.mu.Unlock()
	return nil
}

func (backend *coordinationBackend) calls() (int, int, int) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.getCalls, backend.putCalls, backend.deleteCalls
}

func (backend *coordinationBackend) setPutHook(hook coordinationPutHook) {
	backend.mu.Lock()
	backend.putHook = hook
	backend.mu.Unlock()
}

func (backend *coordinationBackend) setDeleteHook(hook coordinationDeleteHook) {
	backend.mu.Lock()
	backend.deleteHook = hook
	backend.mu.Unlock()
}

func newCoordinationCache[V any](t *testing.T, backend *coordinationBackend, codec Codec[V]) *Cache[string, V] {
	t.Helper()
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) {
		return []byte(key), nil
	})
	instance, err := New(Runtime{
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, backend, Global[string](MustNamespace("tests", "unit", "coordination", 1)), keys, codec, coordinationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func coordinationPolicy() Policy {
	return Policy{
		Freshness:           Expiring(time.Hour, time.Hour),
		Retention:           ExpireAfter(3 * time.Hour),
		Negative:            NoNegativeCaching(),
		Jitter:              NoJitter(),
		MaxKeyBytes:         256,
		MaxValueBytes:       4 << 10,
		MaxValueDepth:       16,
		MaxFlights:          8,
		FlightSaturation:    WaitBounded(coordinationTestTimeout),
		Stale:               RefreshBlocking,
		LastWaiter:          CancelLoader,
		MaxBatchKeys:        16,
		MaxBatchKeyBytes:    4 << 10,
		MaxBatchResultBytes: 64 << 10,
		ReadFailure:         Propagate,
		WriteFailure:        Propagate,
		InvalidateFailure:   Propagate,
		Corruption:          RefuseCorrupt,
		profile:             "coordination-test",
	}
}

type resolveOutcome[V any] struct {
	result Result[V]
	err    error
}

func resolveAsync[K, V any](ctx context.Context, instance *Cache[K, V], key K, loader Loader[K, V]) <-chan resolveOutcome[V] {
	done := make(chan resolveOutcome[V], 1)
	go func() {
		result, err := instance.Resolve(ctx, key, loader)
		done <- resolveOutcome[V]{result: result, err: err}
	}()
	return done
}

func cacheAddress[K, V any](t *testing.T, instance *Cache[K, V], key K) Address {
	t.Helper()
	core, err := instance.core()
	if err != nil {
		t.Fatal(err)
	}
	address, _, err := addressOf(core.scope, core.keys, core.keyVersion, key, core.policy.MaxKeyBytes)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

func waitAddressState[K, V any](t *testing.T, instance *Cache[K, V], key K, accept func(*addressState) bool) {
	t.Helper()
	core, err := instance.core()
	if err != nil {
		t.Fatal(err)
	}
	address := cacheAddress(t, instance, key)
	deadline := time.NewTimer(coordinationTestTimeout)
	defer deadline.Stop()
	for {
		core.coord.mu.Lock()
		state := core.coord.states[address]
		if state != nil && accept(state) {
			core.coord.mu.Unlock()
			return
		}
		if state == nil {
			core.coord.mu.Unlock()
			t.Fatal("coordination state is missing")
		}
		core.coord.mu.Unlock()
		select {
		case <-deadline.C:
			t.Fatal("coordination state did not reach the expected condition")
		default:
		}
		runtime.Gosched()
	}
}

func waitCacheQuiescent[K, V any](t *testing.T, instance *Cache[K, V]) {
	t.Helper()
	deadline := time.NewTimer(coordinationTestTimeout)
	defer deadline.Stop()
	for {
		stats := instance.Stats()
		if stats == (LocalStats{}) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("cache did not quiesce: %+v", stats)
		default:
			runtime.Gosched()
		}
	}
}

func receiveResolve[V any](t *testing.T, done <-chan resolveOutcome[V]) resolveOutcome[V] {
	t.Helper()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(coordinationTestTimeout):
		t.Fatal("resolve did not finish")
		return resolveOutcome[V]{}
	}
}

func receiveError(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(coordinationTestTimeout):
		t.Fatal("operation did not finish")
		return nil
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(coordinationTestTimeout):
		t.Fatalf("%s did not happen", name)
	}
}

func assertNotSignaled(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
		t.Fatalf("%s happened too early", name)
	default:
	}
}

func assertQuiescent[K, V any](t *testing.T, instance *Cache[K, V]) {
	t.Helper()
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("stats = %+v", stats)
	}
}

func assertStringResult(t *testing.T, outcome resolveOutcome[string], value string, state State) {
	t.Helper()
	if outcome.err != nil || outcome.result.Value != value || outcome.result.State != state {
		t.Fatalf("result = %+v, err = %v; want value %q state %d", outcome.result, outcome.err, value, state)
	}
}

func unexpectedCall(kind string, call int) error {
	return fmt.Errorf("unexpected backend %s call %d", kind, call)
}
