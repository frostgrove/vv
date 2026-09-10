package otelnative

import (
	"context"
	"errors"
	"reflect"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var (
	ErrInvalidProviders = errors.New("otelnative: tracer and meter providers are required")
	ErrInvalidIngress   = errors.New("otelnative: invalid ingress mode")
)

type Providers struct {
	Tracer trace.TracerProvider
	Meter  metric.MeterProvider
}

type IngressMode uint8

const (
	TrustedIngress IngressMode = iota
	PublicIngress
)

func (p Providers) validate() error {
	if nilInterface(p.Tracer) || nilInterface(p.Meter) {
		return ErrInvalidProviders
	}
	return nil
}

func (m IngressMode) validate() error {
	if m != TrustedIngress && m != PublicIngress {
		return ErrInvalidIngress
	}
	return nil
}

func propagatorFor(mode IngressMode) propagation.TextMapPropagator {
	if mode == PublicIngress {
		return publicTraceContext{}
	}
	return propagation.TraceContext{}
}

type publicTraceContext struct {
	propagation.TraceContext
}

func (publicTraceContext) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	extracted := propagation.TraceContext{}.Extract(context.Background(), carrier)
	spanContext := trace.SpanContextFromContext(extracted)
	if !spanContext.IsValid() {
		return ctx
	}
	return trace.ContextWithRemoteSpanContext(ctx, sanitizeSpanContext(spanContext))
}

func sanitizeSpanContext(spanContext trace.SpanContext) trace.SpanContext {
	if !spanContext.IsValid() {
		return trace.SpanContext{}
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    spanContext.TraceID(),
		SpanID:     spanContext.SpanID(),
		TraceFlags: spanContext.TraceFlags(),
		Remote:     spanContext.IsRemote(),
	})
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	kind := reflect.ValueOf(value).Kind()
	return (kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface || kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice) && reflect.ValueOf(value).IsNil()
}
