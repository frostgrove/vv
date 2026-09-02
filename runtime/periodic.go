package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

type Ticker interface {
	Ticks() <-chan time.Time

	Stop()
}

type Ticks func(interval time.Duration) Ticker

func SystemTicks(interval time.Duration) Ticker { return systemTicker{inner: time.NewTicker(interval)} }

type systemTicker struct {
	inner *time.Ticker
}

func (this systemTicker) Ticks() <-chan time.Time { return this.inner.C }

func (this systemTicker) Stop() { this.inner.Stop() }

type PeriodicSpec struct {
	Name string

	Interval time.Duration

	Timeout time.Duration

	Immediate bool

	Pass func(ctx context.Context) error

	Logger *slog.Logger

	Ticks Ticks
}

func NewPeriodic(spec PeriodicSpec) (Runner, error) {
	var problems []error
	if spec.Name == "" {
		problems = append(problems, errors.New("runtime: a periodic runner has no name"))
	}
	if spec.Interval <= 0 {
		problems = append(problems, fmt.Errorf("runtime: %q has interval %s, which is not a period", spec.Name, spec.Interval))
	}
	if spec.Timeout < 0 {
		problems = append(problems, fmt.Errorf("runtime: %q has a negative pass timeout (%s)", spec.Name, spec.Timeout))
	}
	if spec.Pass == nil {
		problems = append(problems, fmt.Errorf("runtime: %q has nothing to do every %s", spec.Name, spec.Interval))
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}

	if spec.Timeout == 0 {
		spec.Timeout = spec.Interval
	}
	if spec.Logger == nil {
		spec.Logger = slog.Default()
	}
	if spec.Ticks == nil {
		spec.Ticks = SystemTicks
	}
	return &periodic{spec: spec}, nil
}

func Every(name string, interval time.Duration, pass func(ctx context.Context) error) (Runner, error) {
	return NewPeriodic(PeriodicSpec{Name: name, Interval: interval, Pass: pass})
}

type periodic struct {
	spec PeriodicSpec
}

func (this *periodic) Name() string { return this.spec.Name }

func (this *periodic) Declaration() Declaration { return PerReplicaTimer }

func (this *periodic) Run(ctx context.Context) error {
	ticker := this.spec.Ticks(this.spec.Interval)
	defer ticker.Stop()

	if this.spec.Immediate {
		this.once(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.Ticks():
			this.once(ctx)
		}
	}
}

// once keeps a failing pass from ending the schedule: a sweep that cannot reach
// the database at 3am must still run at 3:05, and a panic in one pass is a
// defect to report rather than a reason to stop sweeping for the life of the
// process. What it must not do is hide either — both leave at Error level.
func (this *periodic) once(ctx context.Context) {
	pass, cancel := context.WithTimeout(ctx, this.spec.Timeout)
	defer cancel()

	err := attempt(pass, this.spec.Pass)
	if err == nil || ctx.Err() != nil {
		return
	}
	this.spec.Logger.ErrorContext(ctx, "a periodic pass failed",
		slog.String("runner", this.spec.Name), slog.String("err", err.Error()))
}

func attempt(ctx context.Context, pass func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("the pass panicked: %v", recovered)
		}
	}()
	return pass(ctx)
}
