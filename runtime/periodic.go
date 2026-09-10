package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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
	spec     PeriodicSpec
	mutex    sync.Mutex
	flight   *periodicFlight
	draining bool
}

type periodicFlight struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func (this *periodic) Name() string { return this.spec.Name }

func (this *periodic) Declaration() Declaration { return PerReplicaTimer }

func (this *periodic) BeginRunnerGeneration() {
	this.mutex.Lock()
	this.draining = false
	this.mutex.Unlock()
}

func (this *periodic) Run(ctx context.Context) error {
	ticker := this.spec.Ticks(this.spec.Interval)
	defer ticker.Stop()

	if this.spec.Immediate {
		this.once(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			this.awaitPassOnStop()
			return ctx.Err()
		case <-ticker.Ticks():
			this.once(ctx)
		}
	}
}

func (this *periodic) once(ctx context.Context) {
	flight, started := this.beginPass(ctx)
	if !started {
		return
	}
	err, completed := waitPeriodicPass(flight)
	flight.cancel()
	if completed {
		this.finishPass(flight)
	}
	if err == nil || ctx.Err() != nil {
		return
	}
	this.spec.Logger.ErrorContext(ctx, "a periodic pass failed",
		slog.String("runner", this.spec.Name), slog.String("err", safelyRenderError(err, "periodic pass returned an error that could not be inspected")))
}

func (this *periodic) beginPass(ctx context.Context) (*periodicFlight, bool) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.draining {
		return nil, false
	}
	if this.flight != nil {
		select {
		case <-this.flight.done:
			this.flight = nil
		default:
			return nil, false
		}
	}
	pass, cancel := context.WithTimeout(ctx, this.spec.Timeout)
	flight := &periodicFlight{ctx: pass, cancel: cancel, done: make(chan struct{})}
	this.flight = flight
	go func() {
		flight.err = attempt(pass, this.spec.Pass)
		close(flight.done)
	}()
	return flight, true
}

func (this *periodic) finishPass(flight *periodicFlight) {
	this.mutex.Lock()
	if this.flight == flight {
		this.flight = nil
	}
	this.mutex.Unlock()
}

func waitPeriodicPass(flight *periodicFlight) (error, bool) {
	select {
	case <-flight.done:
		return flight.err, true
	default:
	}
	select {
	case <-flight.done:
		return flight.err, true
	case <-flight.ctx.Done():
		select {
		case <-flight.done:
			return flight.err, true
		default:
			return flight.ctx.Err(), false
		}
	}
}

func (this *periodic) Drain(ctx context.Context) error {
	this.mutex.Lock()
	this.draining = true
	flight := this.flight
	this.mutex.Unlock()
	if flight == nil {
		return nil
	}
	flight.cancel()
	select {
	case <-flight.done:
		this.finishPass(flight)
		return nil
	default:
	}
	select {
	case <-flight.done:
		this.finishPass(flight)
		return nil
	case <-ctx.Done():
		select {
		case <-flight.done:
			this.finishPass(flight)
			return nil
		default:
			return ctx.Err()
		}
	}
}

func (this *periodic) awaitPassOnStop() {
	this.mutex.Lock()
	flight := this.flight
	this.mutex.Unlock()
	if flight == nil {
		return
	}
	flight.cancel()
	<-flight.done
	this.finishPass(flight)
}

func attempt(ctx context.Context, pass func(context.Context) error) (err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			err = errors.New("the pass panicked")
		}
	}()
	err = pass(ctx)
	completed = true
	return err
}
