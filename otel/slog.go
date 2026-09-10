package vvotel

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

type traceHandler struct {
	next     slog.Handler
	reserved bool
}

func TraceHandler(next slog.Handler) slog.Handler {
	if next == nil {
		return nil
	}
	return traceHandler{next: next}
}

func (h traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h traceHandler) Handle(ctx context.Context, record slog.Record) error {
	record = record.Clone()
	if h.reserved || recordOwnsCorrelation(record) {
		return h.next.Handle(ctx, record)
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return h.next.Handle(ctx, record)
	}
	record.AddAttrs(
		slog.String(LogTraceIDKey, spanContext.TraceID().String()),
		slog.String(LogSpanIDKey, spanContext.SpanID().String()),
		slog.String(LogTraceFlagsKey, spanContext.TraceFlags().String()),
	)
	return h.next.Handle(ctx, record)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	reserved := h.reserved || attrsOwnCorrelation(attrs)
	return traceHandler{
		next:     h.next.WithAttrs(attrs),
		reserved: reserved,
	}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{
		next:     h.next.WithGroup(name),
		reserved: h.reserved || reservedCorrelationKey(name),
	}
}

func recordOwnsCorrelation(record slog.Record) bool {
	reserved := false
	record.Attrs(func(attr slog.Attr) bool {
		reserved = attrOwnsCorrelation(attr)
		return !reserved
	})
	return reserved
}

func attrsOwnCorrelation(attrs []slog.Attr) bool {
	for _, attr := range attrs {
		if attrOwnsCorrelation(attr) {
			return true
		}
	}
	return false
}

func attrOwnsCorrelation(attr slog.Attr) bool {
	if reservedCorrelationKey(attr.Key) {
		return true
	}
	switch attr.Value.Kind() {
	case slog.KindGroup:
		return attrsOwnCorrelation(attr.Value.Group())
	case slog.KindLogValuer:
		return true
	default:
		return false
	}
}

func reservedCorrelationKey(key string) bool {
	return key == LogTraceIDKey || key == LogSpanIDKey || key == LogTraceFlagsKey
}
