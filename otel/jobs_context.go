package vvotel

import (
	"context"

	"github.com/frostgrove/vv/jobs"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	jobsPropagationInjected        = "injected"
	jobsPropagationExtracted       = "extracted"
	jobsPropagationAbsent          = "absent"
	jobsPropagationTraceparentDrop = "traceparent_dropped"
	jobsPropagationTracestateDrop  = "tracestate_dropped"
)

func JobContext(t *Telemetry, next jobs.TrustedContextProvider) jobs.TrustedContextProvider {
	if nilInterface(next) {
		return nil
	}
	return jobs.TrustedContextProviderFunc(func(ctx context.Context, request jobs.ContextCaptureRequest) (jobs.ContextCapture, error) {
		capture, err := next.Capture(ctx, request)
		if err != nil {
			return capture, err
		}
		updated, outcome := injectJobContext(ctx, capture)
		recordJobsPropagation(t, ctx, OpJobsPropagationInject, outcome)
		return updated, nil
	})
}

func JobIdentity(t *Telemetry, next jobs.TrustedIdentityRestorer) jobs.TrustedIdentityRestorer {
	if nilInterface(next) {
		return nil
	}
	return jobs.TrustedIdentityRestorerFunc(func(ctx context.Context, request jobs.IdentityRestoreRequest) (jobs.RestoredIdentity, error) {
		identity, err := next.RestoreIdentity(ctx, request)
		if err != nil {
			return identity, err
		}
		updated, outcome := extractJobIdentity(identity, request.Trace())
		recordJobsPropagation(t, ctx, OpJobsPropagationExtract, outcome)
		return updated, nil
	})
}

func injectJobContext(ctx context.Context, capture jobs.ContextCapture) (jobs.ContextCapture, string) {
	traceParent, traceState, valid := injectTraceContext(ctx)
	if !valid {
		return capture, jobsPropagationAbsent
	}
	correlations := capture.Trace().Correlations()
	carrier, err := jobs.NewUntrustedTraceCarrier(jobs.TraceCarrierSpec{
		TraceParent:  traceParent,
		TraceState:   traceState,
		Correlations: correlations,
	})
	if err == nil {
		if updated, copyErr := capture.WithTrace(carrier); copyErr == nil {
			return updated, jobsPropagationInjected
		}
	}
	carrier, err = jobs.NewUntrustedTraceCarrier(jobs.TraceCarrierSpec{
		TraceParent:  traceParent,
		Correlations: correlations,
	})
	if err == nil {
		if updated, copyErr := capture.WithTrace(carrier); copyErr == nil {
			return updated, jobsPropagationTracestateDrop
		}
	}
	return capture, jobsPropagationTraceparentDrop
}

func injectTraceContext(ctx context.Context) (traceParent string, traceState string, valid bool) {
	defer func() {
		if recover() != nil {
			traceParent = ""
			traceState = ""
			valid = false
		}
	}()
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return "", "", false
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	traceParent = carrier.Get("traceparent")
	if traceParent == "" {
		return "", "", false
	}
	return traceParent, carrier.Get("tracestate"), true
}

func extractJobIdentity(identity jobs.RestoredIdentity, carrier jobs.UntrustedTraceCarrier) (result jobs.RestoredIdentity, outcome string) {
	traceParent := carrier.TraceParent()
	if traceParent == "" {
		return identity, jobsPropagationAbsent
	}
	outcome = jobsPropagationExtracted
	traceState := carrier.TraceState()
	if traceState != "" {
		if _, err := trace.ParseTraceState(traceState); err != nil {
			outcome = jobsPropagationTracestateDrop
		}
	}
	defer func() {
		if recover() != nil {
			result = identity
			outcome = jobsPropagationTraceparentDrop
		}
	}()
	mapCarrier := propagation.MapCarrier{
		"traceparent": traceParent,
		"tracestate":  traceState,
	}
	extracted := propagation.TraceContext{}.Extract(context.Background(), mapCarrier)
	spanContext := trace.SpanContextFromContext(extracted)
	if !spanContext.IsValid() {
		return identity, jobsPropagationTraceparentDrop
	}
	derived := trace.ContextWithRemoteSpanContext(identity.Context(), spanContext)
	updated, err := identity.WithContext(derived)
	if err != nil {
		return identity, jobsPropagationTraceparentDrop
	}
	return updated, outcome
}

func recordJobsPropagation(t *Telemetry, ctx context.Context, operation string, outcome string) {
	if t == nil {
		return
	}
	counter := t.int64Counter(SignalJobsPropagation)
	attributes, admitted := jobsPropagationAttributes(operation, outcome)
	if nilInterface(counter) || !admitted {
		return
	}
	safeAdd(counter, ctx, 1, metric.WithAttributes(attributes...))
}

func jobsPropagationAttributes(operation string, outcome string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(SignalJobsPropagation, "", []attribute.KeyValue{
		AttrComponent.String(ComponentJobsPropagation),
		AttrOperationName.String(operation),
		AttrOperationOutcome.String(outcome),
	})
}
