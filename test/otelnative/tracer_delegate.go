package otelnative

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

type scopedTracerProvider struct {
	trace.TracerProvider
	scope         string
	public        bool
	normalizeName func(string) string
}

func (p scopedTracerProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	tracer := p.TracerProvider.Tracer(name, options...)
	if name != p.scope {
		return tracer
	}
	return scopedTracer{
		Tracer:        tracer,
		public:        p.public,
		normalizeName: p.normalizeName,
	}
}

type scopedTracer struct {
	trace.Tracer
	public        bool
	normalizeName func(string) string
}

func (t scopedTracer) Start(ctx context.Context, name string, options ...trace.SpanStartOption) (context.Context, trace.Span) {
	if t.normalizeName != nil {
		name = t.normalizeName(name)
	}
	if t.public {
		parent := sanitizeSpanContext(trace.SpanContextFromContext(ctx))
		if parent.IsValid() && parent.IsRemote() {
			ctx = trace.ContextWithRemoteSpanContext(ctx, parent)
			options = append(options, trace.WithNewRoot(), trace.WithLinks(trace.Link{SpanContext: parent}))
		} else {
			options = append(options, trace.WithNewRoot())
		}
	}
	_, span := t.Tracer.Start(ctx, name, options...)
	wrapped := scopedSpan{
		Span:          span,
		normalizeName: t.normalizeName,
	}
	return trace.ContextWithSpan(ctx, wrapped), wrapped
}

type scopedSpan struct {
	trace.Span
	normalizeName func(string) string
}

func (s scopedSpan) SetName(name string) {
	if s.normalizeName != nil {
		name = s.normalizeName(name)
	}
	s.Span.SetName(name)
}
