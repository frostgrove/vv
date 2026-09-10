package runtime

import (
	"context"
	"errors"
	"reflect"
	"time"
)

const MaxObservers = 8

type LifecycleOperation uint8

const (
	LifecycleOperationRun LifecycleOperation = iota + 1
	LifecycleOperationDrain
)

type LifecycleOutcome uint8

const (
	LifecycleOutcomeOK LifecycleOutcome = iota + 1
	LifecycleOutcomeError
	LifecycleOutcomeCanceled
	LifecycleOutcomeTimeout
)

type LifecycleEvent struct {
	operation   LifecycleOperation
	outcome     LifecycleOutcome
	declaration Declaration
	err         error
	elapsed     time.Duration
}

func (e LifecycleEvent) Operation() LifecycleOperation { return e.operation }
func (e LifecycleEvent) Outcome() LifecycleOutcome     { return e.outcome }
func (e LifecycleEvent) Declaration() Declaration      { return e.declaration }
func (e LifecycleEvent) Err() error                    { return e.err }
func (e LifecycleEvent) Elapsed() time.Duration        { return e.elapsed }

type LifecycleObserver interface {
	ObservedLifecycle(context.Context, LifecycleEvent)
}

type LifecycleObserverFunc func(context.Context, LifecycleEvent)

func (LifecycleObserverFunc) Observed(RunnerState) {}

func (f LifecycleObserverFunc) ObservedLifecycle(ctx context.Context, event LifecycleEvent) {
	f(ctx, event)
}

var ErrTooManyObservers = errors.New("runtime: too many observers")

func Observers(observers ...Observer) (Observer, error) {
	if len(observers) > MaxObservers {
		return nil, ErrTooManyObservers
	}
	children := make(observerFanout, 0, len(observers))
	for _, observer := range observers {
		if observerNil(observer) {
			continue
		}
		children = append(children, observer)
	}
	return children, nil
}

func MustObservers(observers ...Observer) Observer {
	combined, err := Observers(observers...)
	if err != nil {
		panic(err)
	}
	return combined
}

type observerFanout []Observer

func (o observerFanout) Observed(state RunnerState) {
	for _, child := range o {
		observing(child, state)
	}
}

func (o observerFanout) ObservedLifecycle(ctx context.Context, event LifecycleEvent) {
	for _, child := range o {
		observingLifecycle(child, ctx, event)
	}
}

func observerNil(observer Observer) bool {
	if observer == nil {
		return true
	}
	value := reflect.ValueOf(observer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func observingLifecycle(observer Observer, ctx context.Context, event LifecycleEvent) {
	lifecycle, ok := observer.(LifecycleObserver)
	if !ok || observerNil(observer) {
		return
	}
	defer func() { _ = recover() }()
	lifecycle.ObservedLifecycle(ctx, event)
}

func lifecycleEvent(operation LifecycleOperation, declaration Declaration, err error, elapsed time.Duration) LifecycleEvent {
	return LifecycleEvent{
		operation:   operation,
		outcome:     lifecycleOutcome(err),
		declaration: declaration,
		err:         err,
		elapsed:     elapsed,
	}
}

func lifecycleOutcome(err error) (outcome LifecycleOutcome) {
	if err == nil {
		return LifecycleOutcomeOK
	}
	if safelyMatches(err, context.DeadlineExceeded) {
		return LifecycleOutcomeTimeout
	}
	if safelyMatches(err, context.Canceled) {
		return LifecycleOutcomeCanceled
	}
	return LifecycleOutcomeError
}
