package vvotel

import (
	"context"
	"errors"

	vvruntime "github.com/frostgrove/vv/runtime"
	"go.opentelemetry.io/otel/metric"
)

func Runtime(t *Telemetry) vvruntime.Observer {
	return &runtimeObserver{tel: t}
}

type runtimeObserver struct {
	tel *Telemetry
}

func (o *runtimeObserver) Observed(state vvruntime.RunnerState) {
	if o == nil || o.tel == nil || !o.tel.signalEnabled(SignalRuntimeTransitions) {
		return
	}
	counter := o.tel.int64Counter(SignalRuntimeTransitions)
	attributes, admitted := runtimeTransitionAttributes(state)
	if nilInterface(counter) || !admitted {
		return
	}
	safeAdd(counter, context.Background(), 1, metric.WithAttributes(attributes...))
}

func (o *runtimeObserver) ObservedLifecycle(ctx context.Context, event vvruntime.LifecycleEvent) {
	if o == nil || o.tel == nil {
		return
	}
	if o.tel.signalEnabled(SignalRuntimeOperations) {
		counter := o.tel.int64Counter(SignalRuntimeOperations)
		attributes, admitted := runtimeLifecycleAttributes(SignalRuntimeOperations, event)
		if !nilInterface(counter) && admitted {
			safeAdd(counter, ctx, 1, metric.WithAttributes(attributes...))
		}
	}
	if o.tel.signalEnabled(SignalRuntimeDuration) && event.Elapsed() >= 0 {
		histogram := o.tel.float64Histogram(SignalRuntimeDuration)
		attributes, admitted := runtimeLifecycleAttributes(SignalRuntimeDuration, event)
		if !nilInterface(histogram) && admitted {
			safeRecord(histogram, ctx, event.Elapsed().Seconds(), metric.WithAttributes(attributes...))
		}
	}
}

func runtimeLifecycleErrorType(event vvruntime.LifecycleEvent) (errorType string) {
	switch event.Outcome() {
	case vvruntime.LifecycleOutcomeOK:
		return ""
	case vvruntime.LifecycleOutcomeCanceled:
		return ErrorTypeCanceled
	case vvruntime.LifecycleOutcomeTimeout:
		return ErrorTypeTimeout
	case vvruntime.LifecycleOutcomeError:
		defer func() {
			if recover() != nil {
				errorType = ErrorTypeInternal
			}
		}()
		if errors.Is(event.Err(), vvruntime.ErrRunnerPanicked) {
			return ErrorTypePanic
		}
		return ErrorTypeInternal
	default:
		return ""
	}
}
