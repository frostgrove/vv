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
	if o.tel == nil || o.tel.config.Disabled {
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

	if o.emitSpanEvents {
		span := safeSpanFromContext(ctx)
		if span != nil && safeIsRecording(span) {
			safeAddEvent(span, "cache_backend.event", trace.WithAttributes(
				AttrComponent.String(ComponentCacheBackend),
				AttrCacheLayer.String("memory_backend"),
				AttrOperationName.String(op),
				AttrOperationOutcome.String(outcome),
			))
		}
	}

	counter := o.tel.cacheOperationsInstrument()
	if counter != nil {
		safeAdd(counter, ctx, 1, metric.WithAttributes(
			AttrComponent.String(ComponentCacheBackend),
			AttrCacheLayer.String("memory_backend"),
			AttrOperationName.String(op),
			AttrOperationOutcome.String(outcome),
		))
	}
}
