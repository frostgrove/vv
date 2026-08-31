package cachememory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/cache"
)

type callbackPanicClock struct{}

func (callbackPanicClock) Now() time.Time {
	panic("clock")
}

func TestPanickingClockNeverLeavesMemoryMutexLocked(t *testing.T) {
	operations := []struct {
		name string
		run  func(*Backend) error
	}{
		{
			name: "get",
			run: func(backend *Backend) error {
				_, _, err := backend.Get(context.Background(), testAddress(1), cache.ReadLimit{MaxBytes: 8})
				return err
			},
		},
		{
			name: "put",
			run: func(backend *Backend) error {
				return backend.Put(context.Background(), testAddress(1), []byte("value"), testExpiry)
			},
		},
		{
			name: "get many",
			run: func(backend *Backend) error {
				_, err := backend.GetMany(context.Background(), []cache.Address{testAddress(1)}, cache.BatchReadLimit{
					MaxItems: 1, MaxItemBytes: 8, MaxTotalBytes: 8,
				})
				return err
			},
		},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			backend, err := New(testLimits(2, 8), WithClock(callbackPanicClock{}))
			if err != nil {
				t.Fatal(err)
			}
			if err := operation.run(backend); !errors.Is(err, cache.ErrInvalid) {
				t.Fatalf("operation error = %v", err)
			}
			done := make(chan error, 1)
			go func() {
				_ = backend.Stats()
				done <- backend.Reset()
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("memory mutex remained locked")
			}
		})
	}
}
