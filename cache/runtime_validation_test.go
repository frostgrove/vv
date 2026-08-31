package cache

import (
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"
)

type invalidRuntimeTimer struct {
	panicChannel bool
	stopped      atomic.Bool
}

func (timer *invalidRuntimeTimer) C() <-chan time.Time {
	if timer.panicChannel {
		panic("channel")
	}
	return nil
}

func (timer *invalidRuntimeTimer) Stop() bool {
	timer.stopped.Store(true)
	return true
}

type invalidRuntimeTimerClock struct {
	timer Timer
}

func (clock invalidRuntimeTimerClock) Now() time.Time { return time.Unix(1_900_000_000, 0).UTC() }
func (clock invalidRuntimeTimerClock) NewTimer(time.Duration) Timer {
	return clock.timer
}

func TestNormalizeRuntimeRejectsEveryTimeoutOverflow(t *testing.T) {
	now := time.Unix(0, math.MaxInt64-5).UTC()
	tests := []struct {
		name   string
		change func(*Runtime)
	}{
		{name: "loader", change: func(runtime *Runtime) { runtime.LoaderTimeout = 6 * time.Nanosecond }},
		{name: "backend", change: func(runtime *Runtime) { runtime.BackendTimeout = 6 * time.Nanosecond }},
		{name: "cleanup", change: func(runtime *Runtime) { runtime.CleanupTimeout = 6 * time.Nanosecond }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := Runtime{
				Clock:          &policyTestClock{now: now},
				LoaderTimeout:  time.Nanosecond,
				BackendTimeout: time.Nanosecond,
				CleanupTimeout: time.Nanosecond,
			}
			test.change(&runtime)
			if _, err := normalizeRuntime(runtime, defaultLoaderTimeout); !errors.Is(err, ErrInvalid) {
				t.Fatalf("normalizeRuntime() error = %v", err)
			}
		})
	}
}

func TestNewRejectsTimeoutOverflowBeforeServing(t *testing.T) {
	now := time.Unix(0, math.MaxInt64-5).UTC()
	runtime := Runtime{
		Clock:          &policyTestClock{now: now},
		LoaderTimeout:  6 * time.Nanosecond,
		BackendTimeout: time.Nanosecond,
		CleanupTimeout: time.Nanosecond,
	}
	backend := &capabilityTestBackend{description: BackendDescription{
		Name:              "runtime-validation",
		Topology:          ProcessBackend,
		ExpiryClock:       ProcessExpiryClock,
		MaxItemBytes:      1 << 20,
		RelativeExpiry:    true,
		MaxRelativeExpiry: time.Hour,
		CapacityBounded:   true,
	}}
	instance, err := New(
		runtime,
		backend,
		Global[string](MustNamespace("runtime", "test", "timeout", 1)),
		cacheTestKeyCodec(),
		String(1),
		newCacheTestPolicy(64),
	)
	if !errors.Is(err, ErrInvalid) || instance != nil {
		t.Fatalf("New() cache=%v error=%v", instance, err)
	}
}

func TestBackendDescriptionExpiryRangeIsUnambiguous(t *testing.T) {
	base := BackendDescription{
		Name:            "description-validation",
		Topology:        ProcessBackend,
		ExpiryClock:     ProcessExpiryClock,
		MaxItemBytes:    1024,
		CapacityBounded: true,
	}
	tests := []struct {
		name        string
		relative    bool
		maximum     time.Duration
		wantInvalid bool
	}{
		{name: "capacity zero", maximum: 0},
		{name: "capacity negative", maximum: -1, wantInvalid: true},
		{name: "capacity positive", maximum: 1, wantInvalid: true},
		{name: "relative positive", relative: true, maximum: 1},
		{name: "relative zero", relative: true, maximum: 0, wantInvalid: true},
		{name: "relative negative", relative: true, maximum: -1, wantInvalid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			description := base
			description.RelativeExpiry = test.relative
			description.MaxRelativeExpiry = test.maximum
			err := validBackendDescription(description)
			if test.wantInvalid != errors.Is(err, ErrInvalid) {
				t.Fatalf("validBackendDescription() error = %v", err)
			}
		})
	}
}

func TestRuntimeTimerStopsInvalidTimers(t *testing.T) {
	for _, panicChannel := range []bool{false, true} {
		t.Run(map[bool]string{false: "nil channel", true: "panicking channel"}[panicChannel], func(t *testing.T) {
			inner := &invalidRuntimeTimer{panicChannel: panicChannel}
			timer, err := runtimeTimer(invalidRuntimeTimerClock{timer: inner}, time.Second)
			if !errors.Is(err, ErrInvalid) || timer != nil {
				t.Fatalf("runtimeTimer() timer=%v error=%v", timer, err)
			}
			if !inner.stopped.Load() {
				t.Fatal("invalid timer was not stopped")
			}
		})
	}
}
