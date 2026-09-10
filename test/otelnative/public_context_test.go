package otelnative

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestPublicIngressDoesNotPromoteOrLinkAnUnextractedLocalContext(t *testing.T) {
	local := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), local)
	extracted := publicTraceContext{}.Extract(ctx, propagation.HeaderCarrier{})
	if got := trace.SpanContextFromContext(extracted); !got.Equal(local) || got.IsRemote() {
		t.Fatalf("empty carrier changed local context to %#v", got)
	}

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider shutdown: %v", err)
		}
	})
	delegate := scopedTracerProvider{
		TracerProvider: provider,
		scope:          "public-test",
		public:         true,
	}
	_, span := delegate.Tracer("public-test").Start(extracted, "request", trace.WithSpanKind(trace.SpanKindServer))
	span.End()
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d", len(ended))
	}
	if ended[0].Parent().IsValid() || len(ended[0].Links()) != 0 {
		t.Fatalf("public root parent/links = %#v/%#v", ended[0].Parent(), ended[0].Links())
	}
}
