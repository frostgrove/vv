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
	reason := ""
	if event.Reason != "" {
		reason, ok = CacheBackendReasonName(string(event.Reason))
		if !ok {
			return
		}
	}

	if o.emitSpanEvents && o.tel.signalEnabled(SignalCacheBackendEvent) {
		span := safeSpanFromContext(ctx)
		attributes, admitted := cacheBackendAttributes(SignalCacheBackendEvent, op, outcome, reason)
		if !nilInterface(span) && safeIsRecording(span) && admitted {
			safeAddEvent(span, EventCacheBackend, trace.WithAttributes(attributes...))
		}
	}

	o.recordCounter(ctx, SignalCacheOperations, op, outcome, reason)
	o.recordCounter(ctx, SignalCacheEvents, op, outcome, reason)
	o.recordHistogram(ctx, SignalCacheItems, int64(event.Items), op, outcome, reason)
	o.recordHistogram(ctx, SignalCacheValueBytes, event.ValueBytes, op, outcome, reason)
	o.recordHistogram(ctx, SignalCacheChargedBytes, event.ChargedBytes, op, outcome, reason)
}

func (o *cacheMemoryObserver) recordCounter(ctx context.Context, signal Signal, operation string, outcome string, reason string) {
	counter := o.tel.int64Counter(signal)
	attributes, admitted := cacheBackendAttributes(signal, operation, outcome, reason)
	if !nilInterface(counter) && admitted {
		safeAdd(counter, ctx, 1, metric.WithAttributes(attributes...))
	}
}

func (o *cacheMemoryObserver) recordHistogram(ctx context.Context, signal Signal, value int64, operation string, outcome string, reason string) {
	if value < 0 {
		return
	}
	histogram := o.tel.int64Histogram(signal)
	attributes, admitted := cacheBackendAttributes(signal, operation, outcome, reason)
	if !nilInterface(histogram) && admitted {
		safeRecordInt64(histogram, ctx, value, metric.WithAttributes(attributes...))
	}
}
