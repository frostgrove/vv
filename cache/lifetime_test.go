package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

type lifetimeClock struct {
	now time.Time
}

func (this *lifetimeClock) Now() time.Time { return this.now }

func (*lifetimeClock) NewTimer(time.Duration) Timer {
	return policyTestTimer{channel: make(chan time.Time)}
}

func TestLoaderAndBackendUseIndependentLifetimes(t *testing.T) {
	started := time.Unix(1_900_000_000, 0).UTC()
	clock := &lifetimeClock{now: started}
	backend := newCoordinationBackend()
	var backendDeadline time.Time
	backend.setPutHook(func(ctx context.Context, address Address, value []byte, _ Expiry, _ int) error {
		var ok bool
		backendDeadline, ok = ctx.Deadline()
		if !ok || ctx.Err() != nil {
			t.Fatalf("backend context deadline = %v, err = %v", backendDeadline, ctx.Err())
		}
		return backend.store(ctx, address, value)
	})
	policy := coordinationPolicy()
	keys := MustKeyFunc(1, func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	instance, err := New(Runtime{
		Clock:          clock,
		LoaderTimeout:  10 * time.Second,
		BackendTimeout: 5 * time.Second,
		CleanupTimeout: 20 * time.Second,
	}, backend, Global[string](MustNamespace("tests", "unit", "lifetime", 1)), keys, String(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	var loaderCtx context.Context
	var loaderDeadline time.Time
	result, err := instance.Resolve(context.Background(), "key", func(ctx context.Context, _ string) (LoadResult[string], error) {
		loaderCtx = ctx
		var ok bool
		loaderDeadline, ok = ctx.Deadline()
		if !ok {
			t.Fatal("loader context has no deadline")
		}
		clock.now = clock.now.Add(9 * time.Second)
		return Present("value"), nil
	})
	if err != nil || result.Value != "value" || result.State != Loaded {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if !loaderDeadline.Equal(started.Add(10 * time.Second)) {
		t.Fatalf("loader deadline = %v", loaderDeadline)
	}
	if !backendDeadline.Equal(started.Add(14 * time.Second)) {
		t.Fatalf("backend deadline = %v", backendDeadline)
	}
	if !errors.Is(loaderCtx.Err(), context.Canceled) {
		t.Fatalf("loader context error = %v", loaderCtx.Err())
	}
	assertQuiescent(t, instance)
}

func TestLastWaiterPolicies(t *testing.T) {
	t.Run("cancel loader", func(t *testing.T) {
		backend := newCoordinationBackend()
		instance := newCoordinationCache(t, backend, String(1))
		entered := make(chan struct{})
		loaderCanceled := make(chan struct{})
		loader := func(ctx context.Context, _ string) (LoadResult[string], error) {
			close(entered)
			<-ctx.Done()
			close(loaderCanceled)
			return LoadResult[string]{}, ctx.Err()
		}
		firstCtx, cancelFirst := context.WithCancel(context.Background())
		secondCtx, cancelSecond := context.WithCancel(context.Background())
		first := resolveAsync(firstCtx, instance, "key", loader)
		waitSignal(t, entered, "loader entry")
		second := resolveAsync(secondCtx, instance, "key", loader)
		waitAddressState(t, instance, "key", func(state *addressState) bool {
			return state.member != nil && state.member.waiters == 2
		})
		cancelFirst()
		if outcome := receiveResolve(t, first); !errors.Is(outcome.err, context.Canceled) {
			t.Fatalf("first error = %v", outcome.err)
		}
		waitAddressState(t, instance, "key", func(state *addressState) bool {
			return state.member != nil && state.member.waiters == 1
		})
		assertNotSignaled(t, loaderCanceled, "loader cancellation")
		cancelSecond()
		if outcome := receiveResolve(t, second); !errors.Is(outcome.err, context.Canceled) {
			t.Fatalf("second error = %v", outcome.err)
		}
		waitSignal(t, loaderCanceled, "loader cancellation")
		assertQuiescent(t, instance)
	})

	t.Run("finish loader", func(t *testing.T) {
		backend := newCoordinationBackend()
		policy := coordinationPolicy()
		policy.LastWaiter = FinishLoader
		keys := MustKeyFunc(1, func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
		instance, err := New(Runtime{
			LoaderTimeout:  coordinationTestTimeout,
			BackendTimeout: coordinationTestTimeout,
			CleanupTimeout: coordinationTestTimeout,
		}, backend, Global[string](MustNamespace("tests", "unit", "finish-loader", 1)), keys, String(1), policy)
		if err != nil {
			t.Fatal(err)
		}
		entered := make(chan struct{})
		release := make(chan struct{})
		loaderCanceled := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		done := resolveAsync(ctx, instance, "key", func(loaderCtx context.Context, _ string) (LoadResult[string], error) {
			close(entered)
			select {
			case <-loaderCtx.Done():
				close(loaderCanceled)
				return LoadResult[string]{}, loaderCtx.Err()
			case <-release:
				return Present("value"), nil
			}
		})
		waitSignal(t, entered, "loader entry")
		cancel()
		if outcome := receiveResolve(t, done); !errors.Is(outcome.err, context.Canceled) {
			t.Fatalf("resolve error = %v", outcome.err)
		}
		assertNotSignaled(t, loaderCanceled, "loader cancellation")
		close(release)
		waitCacheQuiescent(t, instance)
		result, err := instance.Lookup(context.Background(), "key")
		if err != nil || result.Value != "value" || result.State != Hit {
			t.Fatalf("lookup = %+v, err = %v", result, err)
		}
		assertNotSignaled(t, loaderCanceled, "loader cancellation")
		assertQuiescent(t, instance)
	})
}

func TestServeOnLoaderErrorDoesNotExtendStaleWindow(t *testing.T) {
	started := time.Unix(1_900_000_000, 0).UTC()
	clock := &lifetimeClock{now: started}
	backend := newCoordinationBackend()
	policy := coordinationPolicy()
	policy.Stale = ServeOnLoaderError
	keys := MustKeyFunc(1, func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	instance, err := New(Runtime{
		Clock:          clock,
		LoaderTimeout:  4 * time.Hour,
		BackendTimeout: time.Hour,
		CleanupTimeout: time.Hour,
	}, backend, Global[string](MustNamespace("tests", "unit", "stale-window", 1)), keys, String(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Put(context.Background(), "key", "stale"); err != nil {
		t.Fatal(err)
	}
	clock.now = started.Add(90 * time.Minute)
	loaderErr := errors.New("loader failed")
	result, err := instance.Resolve(context.Background(), "key", func(context.Context, string) (LoadResult[string], error) {
		clock.now = started.Add(121 * time.Minute)
		return LoadResult[string]{}, loaderErr
	})
	if !errors.Is(err, loaderErr) || result.State != 0 {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	assertQuiescent(t, instance)
}
