package otelnative

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestScopedTracerForwardsBorrowedSpanProviderAndContextIdentity(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	delegate := scopedTracerProvider{
		TracerProvider: provider,
		scope:          "scoped",
		normalizeName:  func(string) string { return "normalized" },
	}
	ctx, span := delegate.Tracer("scoped").Start(context.Background(), "private")
	defer span.End()
	contextSpan := trace.SpanFromContext(ctx)
	if span.TracerProvider() != provider || contextSpan.TracerProvider() != provider {
		t.Fatalf("span providers = %T/%T, want borrowed %T", span.TracerProvider(), contextSpan.TracerProvider(), provider)
	}
	if !span.SpanContext().Equal(contextSpan.SpanContext()) {
		t.Fatalf("returned/context span contexts = %v/%v", span.SpanContext(), contextSpan.SpanContext())
	}
}
