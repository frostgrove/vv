package otelnative

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type scopedTracerContextKey struct{}

type replacingNativeTracerProvider struct {
	trace.TracerProvider
}

func (provider replacingNativeTracerProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return replacingNativeTracer{Tracer: provider.TracerProvider.Tracer(name, options...)}
}

type replacingNativeTracer struct {
	trace.Tracer
}

func (tracer replacingNativeTracer) Start(ctx context.Context, name string, options ...trace.SpanStartOption) (context.Context, trace.Span) {
	_, span := tracer.Tracer.Start(ctx, name, options...)
	return trace.ContextWithSpan(context.Background(), span), span
}

func TestScopedTracerForwardsBorrowedSpanProviderAndContextIdentity(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	delegate := scopedTracerProvider{
		TracerProvider: provider,
		scope:          "scoped",
		normalizeName:  normalizePGXSpanName,
	}
	caller := context.WithValue(context.Background(), scopedTracerContextKey{}, "kept")
	ctx, span := delegate.Tracer("scoped").Start(caller, "private-start-secret", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("kept", "value")))
	contextSpan := trace.SpanFromContext(ctx)
	if span.TracerProvider() != provider || contextSpan.TracerProvider() != provider {
		t.Fatalf("span providers = %T/%T, want borrowed %T", span.TracerProvider(), contextSpan.TracerProvider(), provider)
	}
	if !span.SpanContext().Equal(contextSpan.SpanContext()) {
		t.Fatalf("returned/context span contexts = %v/%v", span.SpanContext(), contextSpan.SpanContext())
	}
	if ctx.Value(scopedTracerContextKey{}) != "kept" {
		t.Fatal("scoped tracer lost caller context values")
	}
	span.SetName("copy_from private-table-secret")
	span.End()
	ended := recorder.Ended()
	if len(ended) != 1 || ended[0].Name() != "db.copy" || ended[0].SpanKind() != trace.SpanKindClient {
		t.Fatalf("ended spans = %#v", ended)
	}
	if len(ended[0].Attributes()) != 1 || ended[0].Attributes()[0].Key != "kept" || ended[0].Attributes()[0].Value.AsString() != "value" {
		t.Fatalf("forwarded attributes = %v", ended[0].Attributes())
	}
}

func TestScopedTracerIgnoresBorrowedContextReplacement(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	delegate := scopedTracerProvider{TracerProvider: replacingNativeTracerProvider{TracerProvider: provider}, scope: "scoped"}
	caller, cancel := context.WithCancel(context.WithValue(context.Background(), scopedTracerContextKey{}, "kept"))
	defer cancel()

	ctx, span := delegate.Tracer("scoped").Start(caller, "operation")
	defer span.End()
	if ctx.Value(scopedTracerContextKey{}) != "kept" || ctx.Done() != caller.Done() || !span.SpanContext().Equal(trace.SpanFromContext(ctx).SpanContext()) {
		t.Fatal("borrowed tracer replaced caller context semantics")
	}
}
