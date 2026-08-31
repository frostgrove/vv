package cachememory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/cache"
)

func TestConcurrentOperationsPreserveBoundsAndOwnership(t *testing.T) {
	clock := newFakeClock()
	backend := mustBackend(t, testLimits(32, 64), WithClock(clock))
	ctx := context.Background()
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		worker := worker
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < 500; iteration++ {
				address := testAddress(byte((worker*17 + iteration) % 48))
				value := []byte{byte(worker), byte(iteration), byte(iteration >> 8)}
				switch (worker + iteration) % 5 {
				case 0:
					_ = backend.Put(ctx, address, value, cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Minute})
					value[0]++
				case 1:
					result, found, err := backend.Get(ctx, address, cache.ReadLimit{MaxBytes: 64})
					if err == nil && found && len(result) > 0 {
						result[0]++
					}
				case 2:
					_ = backend.Delete(ctx, address)
				case 3:
					_, _ = backend.GetMany(ctx, []cache.Address{address, address, testAddress(byte(iteration % 48))}, cache.BatchReadLimit{
						MaxItems: 3, MaxItemBytes: 64, MaxTotalBytes: 128,
					})
				case 4:
					clock.Advance(time.Nanosecond)
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	assertInvariant(t, backend)
}

func TestConcurrentResetAndCloseAreSafe(t *testing.T) {
	backend := mustBackend(t, testLimits(16, 32))
	ctx := context.Background()
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		worker := worker
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < 100; iteration++ {
				address := testAddress(byte(worker + iteration))
				err := backend.Put(ctx, address, []byte("value"), testExpiry)
				if err != nil && !errors.Is(err, cache.ErrClosed) {
					t.Errorf("put: %v", err)
					return
				}
				_, _, err = backend.Get(ctx, address, cache.ReadLimit{MaxBytes: 32})
				if err != nil && !errors.Is(err, cache.ErrClosed) {
					t.Errorf("get: %v", err)
					return
				}
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		for iteration := 0; iteration < 20; iteration++ {
			if err := backend.Reset(); err != nil && !errors.Is(err, cache.ErrClosed) {
				t.Errorf("reset: %v", err)
				return
			}
		}
		_ = backend.Close()
	}()
	close(start)
	workers.Wait()
	if stats := backend.Stats(); !stats.Closed || stats.Entries != 0 || stats.ChargedBytes != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func FuzzEntryCharge(f *testing.F) {
	f.Add(0)
	f.Add(1)
	f.Add(1 << 20)
	f.Add(-1)
	f.Fuzz(func(t *testing.T, valueBytes int) {
		charge, err := EntryCharge(valueBytes)
		if valueBytes < 0 {
			if !errors.Is(err, cache.ErrTooLarge) {
				t.Fatalf("negative bytes: charge=%d err=%v", charge, err)
			}
			return
		}
		if err == nil && charge != FixedEntryChargeBytes+int64(valueBytes) {
			t.Fatalf("charge=%d bytes=%d", charge, valueBytes)
		}
	})
}
