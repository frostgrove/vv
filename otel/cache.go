package vvotel

import (
	"context"

	"github.com/frostgrove/vv/cache"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type CacheOption func(*cacheSettings)

type cacheSettings struct {
	emitSpanEvents bool
}

func WithCacheSpanEvents(enabled bool) CacheOption {
	return func(s *cacheSettings) {
		s.emitSpanEvents = enabled
	}
}

func Cache(t *Telemetry, opts ...CacheOption) cache.Observer {
	var s cacheSettings
	for _, opt := range opts {
		if opt != nil {
			opt(&s)
		}
	}
	return &cacheObserver{
		tel:            t,
		emitSpanEvents: s.emitSpanEvents,
	}
}

type cacheObserver struct {
	tel            *Telemetry
	emitSpanEvents bool
}

func (o *cacheObserver) Observe(ctx context.Context, event cache.Event) {
	if o.tel == nil || o.tel.config.Disabled {
		return
	}

	op, ok := CacheOperationName(string(event.Operation))
	if !ok {
		return
	}
	outcome, ok := CacheOutcomeName(string(event.Outcome))
	if !ok {
		return
	}

	if o.emitSpanEvents {
		span := safeSpanFromContext(ctx)
		if span != nil && safeIsRecording(span) {
			safeAddEvent(span, "cache.event", trace.WithAttributes(
				AttrComponent.String(ComponentCache),
				AttrCacheLayer.String("facade"),
				AttrOperationName.String(op),
				AttrOperationOutcome.String(outcome),
			))
		}
	}

	counter := o.tel.cacheOperationsInstrument()
	if counter != nil {
		safeAdd(counter, ctx, 1, metric.WithAttributes(
			AttrComponent.String(ComponentCache),
			AttrCacheLayer.String("facade"),
			AttrOperationName.String(op),
			AttrOperationOutcome.String(outcome),
		))
	}
}
