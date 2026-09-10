package vvotel

import (
	"context"

	"github.com/frostgrove/vv/cache/cachememory"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type CacheMemoryOption func(*cacheMemorySettings)

type cacheMemorySettings struct {
	emitSpanEvents bool
}

func WithCacheMemorySpanEvents(enabled bool) CacheMemoryOption {
	return func(s *cacheMemorySettings) {
		s.emitSpanEvents = enabled
	}
}

func CacheMemory(t *Telemetry, opts ...CacheMemoryOption) cachememory.Observer {
	var s cacheMemorySettings
	for _, opt := range opts {
		if opt != nil {
			opt(&s)
		}
	}
	return &cacheMemoryObserver{
		tel:            t,
		emitSpanEvents: s.emitSpanEvents,
	}
}

type cacheMemoryObserver struct {
	tel            *Telemetry
	emitSpanEvents bool
}

func (o *cacheMemoryObserver) Observe(ctx context.Context, event cachememory.Event) {
	if o.tel == nil {
		return
	}

	op, ok := CacheBackendOperationName(string(event.Operation))
	if !ok {
		return
	}
	outcome, ok := CacheBackendOutcomeName(string(event.Outcome))
	if !ok {
		return
	}

	if o.emitSpanEvents && o.tel.signalEnabled(SignalCacheBackendEvent) {
		span := safeSpanFromContext(ctx)
		attributes, admitted := cacheBackendAttributes(SignalCacheBackendEvent, op, outcome)
		if !nilInterface(span) && safeIsRecording(span) && admitted {
			safeAddEvent(span, EventCacheBackend, trace.WithAttributes(attributes...))
		}
	}

	counter := o.tel.int64Counter(SignalCacheOperations)
	attributes, admitted := cacheBackendAttributes(SignalCacheOperations, op, outcome)
	if !nilInterface(counter) && admitted {
		safeAdd(counter, ctx, 1, metric.WithAttributes(attributes...))
	}
}
