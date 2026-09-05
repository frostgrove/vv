package vvotel

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func safeStart(tracer trace.Tracer, ctx context.Context, name string, options ...trace.SpanStartOption) (next context.Context, span trace.Span, ok bool) {
	defer func() {
		if recover() != nil {
			next = ctx
			span = nil
			ok = false
		}
	}()
	next, span = tracer.Start(ctx, name, options...)
	if next == nil || nilInterface(span) {
		if !nilInterface(span) {
			safeEnd(span)
		}
		return ctx, nil, false
	}
	return next, span, true
}

func safeSpanFromContext(ctx context.Context) (span trace.Span) {
	defer func() { _ = recover() }()
	return trace.SpanFromContext(ctx)
}

func safeSetAttributes(span trace.Span, attributes ...attribute.KeyValue) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	span.SetAttributes(attributes...)
	return true
}

func safeSetStatus(span trace.Span, code codes.Code, description string) {
	defer func() { _ = recover() }()
	span.SetStatus(code, description)
}

func safeEnd(span trace.Span) {
	defer func() { _ = recover() }()
	span.End()
}

func safeAddEvent(span trace.Span, name string, options ...trace.EventOption) {
	defer func() { _ = recover() }()
	span.AddEvent(name, options...)
}

func safeIsRecording(span trace.Span) (recording bool) {
	defer func() {
		if recover() != nil {
			recording = false
		}
	}()
	return span.IsRecording()
}

func safeRecord(histogram metric.Float64Histogram, ctx context.Context, value float64, options ...metric.RecordOption) {
	defer func() { _ = recover() }()
	histogram.Record(ctx, value, options...)
}

func safeAdd(counter metric.Int64Counter, ctx context.Context, value int64, options ...metric.AddOption) {
	defer func() { _ = recover() }()
	counter.Add(ctx, value, options...)
}

func durationSince(start time.Time) float64 {
	return time.Since(start).Seconds()
}
