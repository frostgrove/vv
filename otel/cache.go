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
	if o.tel == nil {
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
	reason := ""
	if event.Reason != "" {
		reason, ok = CacheReasonName(string(event.Reason))
		if !ok {
			return
		}
	}

	if o.emitSpanEvents && o.tel.signalEnabled(SignalCacheEvent) {
		span := safeSpanFromContext(ctx)
		attributes, admitted := cacheFacadeAttributes(SignalCacheEvent, op, outcome, reason, event.Memoized)
		if !nilInterface(span) && safeIsRecording(span) && admitted {
			safeAddEvent(span, EventCache, trace.WithAttributes(attributes...))
		}
	}

	o.recordCounter(ctx, SignalCacheOperations, op, outcome, reason, event.Memoized)
	o.recordCounter(ctx, SignalCacheEvents, op, outcome, reason, event.Memoized)
	o.recordHistogram(ctx, SignalCacheItems, int64(event.Items), op, outcome, reason, event.Memoized)
	fact := SignalSourceValue{Fact: SourceFactCacheEncodedBytes, Value: event.EncodedBytes}
	o.recordHistogram(ctx, SignalCacheEncodedBytes, event.EncodedBytes, op, outcome, reason, event.Memoized, fact)
	o.recordHistogram(ctx, SignalCachePayloadBytes, event.PayloadBytes, op, outcome, reason, event.Memoized, fact)
}

func (o *cacheObserver) recordCounter(ctx context.Context, signal Signal, operation string, outcome string, reason string, memoized bool) {
	counter := o.tel.int64Counter(signal)
	attributes, admitted := cacheFacadeAttributes(signal, operation, outcome, reason, memoized)
	if !nilInterface(counter) && admitted {
		safeAdd(counter, ctx, 1, metric.WithAttributes(attributes...))
	}
}

func (o *cacheObserver) recordHistogram(ctx context.Context, signal Signal, value int64, operation string, outcome string, reason string, memoized bool, facts ...SignalSourceValue) {
	if value < 0 {
		return
	}
	histogram := o.tel.int64Histogram(signal)
	attributes, admitted := cacheFacadeAttributes(signal, operation, outcome, reason, memoized, facts...)
	if !nilInterface(histogram) && admitted {
		safeRecordInt64(histogram, ctx, value, metric.WithAttributes(attributes...))
	}
}
