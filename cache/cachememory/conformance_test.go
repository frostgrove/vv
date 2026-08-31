package cachememory

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachetest"
)

func TestConformance(t *testing.T) {
	cachetest.Run(t, func(t *testing.T) cachetest.Harness {
		clock := cachetest.NewClock()
		backend, err := New(
			Limits{MaxEntries: 4, MaxBytes: 6000, MaxItemBytes: 4096},
			WithClock(clock),
		)
		if err != nil {
			t.Fatal(err)
		}
		return cachetest.Harness{
			Backend: backend,
			Runtime: cache.Runtime{
				Clock:          clock,
				ClockSkew:      cache.SingleProcessClock(),
				Random:         cachetest.NewRandom(0),
				Observer:       cachetest.NewObserver(),
				LoaderTimeout:  5 * time.Second,
				BackendTimeout: 5 * time.Second,
				CleanupTimeout: 5 * time.Second,
			},
			Advance: clock.Advance,
			Close:   backend.Close,
			Capacity: &cachetest.Capacity{
				MaxEntries:             4,
				BytePressureValueBytes: 3000,
			},
			VerifyCancellation: func(t *testing.T) {
				verifyCancellation(t, backend)
			},
		}
	})
}

type cancellationContext struct {
	context.Context
	calls   atomic.Int32
	entered chan struct{}
}

func (ctx *cancellationContext) Err() error {
	err := ctx.Context.Err()
	if ctx.calls.Add(1) == 1 {
		close(ctx.entered)
	}
	return err
}

func verifyCancellation(t *testing.T, backend *Backend) {
	t.Helper()
	expiry := cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Minute}
	stored := memoryAddress(1)
	deleted := memoryAddress(2)
	batch := memoryAddress(3)
	for _, address := range []cache.Address{stored, deleted, batch} {
		if err := backend.Put(context.Background(), address, []byte("value"), expiry); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		call func(context.Context) error
	}{
		{name: "get", call: func(ctx context.Context) error {
			_, _, err := backend.Get(ctx, stored, cache.ReadLimit{MaxBytes: 64})
			return err
		}},
		{name: "put", call: func(ctx context.Context) error {
			return backend.Put(ctx, memoryAddress(4), []byte("new"), expiry)
		}},
		{name: "delete", call: func(ctx context.Context) error {
			return backend.Delete(ctx, deleted)
		}},
		{name: "get_many", call: func(ctx context.Context) error {
			_, err := backend.GetMany(ctx, []cache.Address{batch}, cache.BatchReadLimit{MaxItems: 1, MaxItemBytes: 64, MaxTotalBytes: 64})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := cancelWhileLocked(t, backend, test.call); !errors.Is(err, context.Canceled) {
				t.Fatalf("in-progress cancellation = %v", err)
			}
		})
	}
	if _, found, err := backend.Get(context.Background(), memoryAddress(4), cache.ReadLimit{MaxBytes: 64}); err != nil || found {
		t.Fatalf("cancelled put mutated backend: %v, %v", found, err)
	}
	if value, found, err := backend.Get(context.Background(), deleted, cache.ReadLimit{MaxBytes: 64}); err != nil || !found || string(value) != "value" {
		t.Fatalf("cancelled delete mutated backend: %q, %v, %v", value, found, err)
	}
}

func cancelWhileLocked(t *testing.T, backend *Backend, call func(context.Context) error) error {
	t.Helper()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancellationContext{Context: base, entered: make(chan struct{})}
	done := make(chan error, 1)
	backend.mu.Lock()
	locked := true
	defer func() {
		if locked {
			backend.mu.Unlock()
		}
	}()
	go func() { done <- call(ctx) }()
	waitMemory(t, ctx.entered, "driver operation entry")
	cancel()
	backend.mu.Unlock()
	locked = false
	return waitMemory(t, done, "driver cancellation")
}

func waitMemory[T any](t *testing.T, values <-chan T, name string) T {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case value := <-values:
		return value
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", name)
		var zero T
		return zero
	}
}

func memoryAddress(value byte) cache.Address {
	var address cache.Address
	address.NamespaceDigest[0] = 1
	address.KeyDigest[0] = value
	return address
}
