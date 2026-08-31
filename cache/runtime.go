package cache

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"
)

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

type Random interface {
	Uint64() uint64
}

type Operation string

const (
	LookupOperation     Operation = "lookup"
	LookupManyOperation Operation = "lookup_many"
	LoadOperation       Operation = "load"
	PutOperation        Operation = "put"
	ForgetOperation     Operation = "forget"
)

type Outcome string

const (
	HitOutcome        Outcome = "hit"
	MissOutcome       Outcome = "miss"
	NegativeOutcome   Outcome = "negative"
	StaleOutcome      Outcome = "stale"
	LoadedOutcome     Outcome = "loaded"
	StoredOutcome     Outcome = "stored"
	DeletedOutcome    Outcome = "deleted"
	SupersededOutcome Outcome = "superseded"
	CompleteOutcome   Outcome = "complete"
	ErrorOutcome      Outcome = "error"
)

type Reason string

const (
	BackendReason Reason = "backend"
	CorruptReason Reason = "corrupt"
	LimitReason   Reason = "limit"
	RuntimeReason Reason = "runtime"
)

type Event struct {
	Cache        string
	Operation    Operation
	Outcome      Outcome
	Reason       Reason
	Items        int
	EncodedBytes int64
	PayloadBytes int64
}

type Observer interface {
	Observe(context.Context, Event)
}

type SkewPolicy struct {
	mode  SkewMode
	bound time.Duration
}

type SkewMode uint8

const (
	SingleProcessSkew SkewMode = iota + 1
	BoundedSharedSkew
)

func SingleProcessClock() SkewPolicy { return SkewPolicy{mode: SingleProcessSkew} }

func BoundedClockSkew(bound time.Duration) (SkewPolicy, error) {
	if bound <= 0 {
		return SkewPolicy{}, failure("build clock skew policy", fmt.Errorf("%w: skew bound must be positive", ErrInvalid))
	}
	return SkewPolicy{mode: BoundedSharedSkew, bound: bound}, nil
}

type Runtime struct {
	Clock          Clock
	ClockSkew      SkewPolicy
	Random         Random
	Observer       Observer
	LoaderTimeout  time.Duration
	BackendTimeout time.Duration
	CleanupTimeout time.Duration
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) NewTimer(duration time.Duration) Timer {
	return systemTimer{Timer: time.NewTimer(duration)}
}

type systemTimer struct{ *time.Timer }

func (this systemTimer) C() <-chan time.Time { return this.Timer.C }

type systemRandom struct{}

func (systemRandom) Uint64() uint64 { return rand.Uint64() }

type discardObserver struct{}

func (discardObserver) Observe(context.Context, Event) {}

type guardedTimer struct {
	inner   Timer
	channel <-chan time.Time
}

func (this *guardedTimer) C() <-chan time.Time { return this.channel }

func (this *guardedTimer) Stop() (stopped bool) {
	defer func() {
		if recover() != nil {
			stopped = false
		}
	}()
	return this.inner.Stop()
}

func runtimeNow(clock Clock) (now time.Time, err error) {
	defer func() {
		if recover() != nil {
			now = time.Time{}
			err = fmt.Errorf("%w: clock panicked", ErrInvalid)
		}
	}()
	now = clock.Now()
	now = normalizedTime(now)
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("%w: clock returned an unrepresentable time", ErrInvalid)
	}
	return now, nil
}

func runtimeTimer(clock Clock, duration time.Duration) (timer Timer, err error) {
	var inner Timer
	defer func() {
		if recover() != nil {
			timer = nil
			err = fmt.Errorf("%w: clock timer panicked", ErrInvalid)
		}
		if err != nil && !nilInterface(inner) {
			stopRuntimeTimer(inner)
		}
	}()
	inner = clock.NewTimer(duration)
	if nilInterface(inner) {
		return nil, fmt.Errorf("%w: clock returned a nil timer", ErrInvalid)
	}
	channel := inner.C()
	if channel == nil {
		return nil, fmt.Errorf("%w: clock returned a timer with no channel", ErrInvalid)
	}
	return &guardedTimer{inner: inner, channel: channel}, nil
}

func stopRuntimeTimer(timer Timer) {
	defer func() { _ = recover() }()
	timer.Stop()
}

func runtimeRandom(random Random) (value uint64, err error) {
	defer func() {
		if recover() != nil {
			value = 0
			err = fmt.Errorf("%w: random source panicked", ErrInvalid)
		}
	}()
	return random.Uint64(), nil
}

func normalizeRuntime(runtime Runtime, loaderDefault time.Duration) (Runtime, error) {
	if nilInterface(runtime.Clock) {
		runtime.Clock = systemClock{}
	}
	if nilInterface(runtime.Random) {
		runtime.Random = systemRandom{}
	}
	if nilInterface(runtime.Observer) {
		runtime.Observer = discardObserver{}
	}
	if runtime.ClockSkew.mode != 0 && runtime.ClockSkew.mode != SingleProcessSkew && runtime.ClockSkew.mode != BoundedSharedSkew {
		return Runtime{}, failure("build runtime", fmt.Errorf("%w: clock skew mode is invalid", ErrInvalid))
	}
	if (runtime.ClockSkew.mode == SingleProcessSkew && runtime.ClockSkew.bound != 0) ||
		(runtime.ClockSkew.mode == BoundedSharedSkew && runtime.ClockSkew.bound <= 0) {
		return Runtime{}, failure("build runtime", fmt.Errorf("%w: clock skew bound is invalid", ErrInvalid))
	}
	if runtime.LoaderTimeout < 0 {
		return Runtime{}, failure("build runtime", fmt.Errorf("%w: loader timeout is negative", ErrInvalid))
	}
	if runtime.LoaderTimeout == 0 {
		runtime.LoaderTimeout = loaderDefault
	}
	if runtime.LoaderTimeout <= 0 {
		return Runtime{}, failure("build runtime", fmt.Errorf("%w: loader timeout is not positive", ErrInvalid))
	}
	if runtime.BackendTimeout < 0 || runtime.CleanupTimeout < 0 {
		return Runtime{}, failure("build runtime", fmt.Errorf("%w: backend or cleanup timeout is negative", ErrInvalid))
	}
	if runtime.BackendTimeout == 0 {
		runtime.BackendTimeout = 10 * time.Second
	}
	if runtime.CleanupTimeout == 0 {
		runtime.CleanupTimeout = 30 * time.Second
	}
	if runtime.BackendTimeout <= 0 || runtime.CleanupTimeout <= 0 {
		return Runtime{}, failure("build runtime", fmt.Errorf("%w: backend and cleanup timeouts must be positive", ErrInvalid))
	}
	now, err := runtimeNow(runtime.Clock)
	if err != nil {
		return Runtime{}, failure("build runtime", err)
	}
	for _, timeout := range []struct {
		name  string
		value time.Duration
	}{
		{name: "loader", value: runtime.LoaderTimeout},
		{name: "backend", value: runtime.BackendTimeout},
		{name: "cleanup", value: runtime.CleanupTimeout},
	} {
		if _, ok := addTime(now, timeout.value); !ok {
			return Runtime{}, failure("build runtime", fmt.Errorf("%w: %s timeout overflows clock", ErrInvalid, timeout.name))
		}
	}
	return runtime, nil
}
