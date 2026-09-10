package vvotel_test

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/trace"
)

type jobsContextKey struct{}

func TestJobContextInjectsTheCurrentW3CContextWithoutChangingTheCapture(t *testing.T) {
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := jobs.ParseIdentityProvenance("framework.test")
	if err != nil {
		t.Fatal(err)
	}
	base, err := jobs.NewContextCapture(jobs.ContextCaptureSpec{Provenance: provenance, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	provider := jobs.TrustedContextProviderFunc(func(got context.Context, _ jobs.ContextCaptureRequest) (jobs.ContextCapture, error) {
		calls++
		if got.Value(jobsContextKey{}) != "kept" {
			t.Fatal("base provider lost the invocation context")
		}
		return base, nil
	})
	traceState, err := trace.ParseTraceState("vendor=value")
	if err != nil {
		t.Fatal(err)
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    mustTraceID(t, "11111111111111111111111111111111"),
		SpanID:     mustSpanID(t, "2222222222222222"),
		TraceFlags: trace.FlagsSampled,
		TraceState: traceState,
	})
	ctx := trace.ContextWithSpanContext(context.WithValue(t.Context(), jobsContextKey{}, "kept"), spanContext)

	capture, err := vvotel.JobContext(tel, provider).Capture(ctx, jobs.ContextCaptureRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls=%d, want one", calls)
	}
	if capture.Trace().TraceParent() != "00-11111111111111111111111111111111-2222222222222222-01" ||
		capture.Trace().TraceState() != "vendor=value" {
		t.Fatalf("injected carrier=%q/%q", capture.Trace().TraceParent(), capture.Trace().TraceState())
	}
	assertJobsPropagationMetric(t, mp, vvotel.OpJobsPropagationInject, "injected")
}

func TestJobIdentityExtractsARemoteParentAndPreservesIdentityContext(t *testing.T) {
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	carrier, err := jobs.NewUntrustedTraceCarrier(jobs.TraceCarrierSpec{
		TraceParent: "00-33333333333333333333333333333333-4444444444444444-00",
		TraceState:  "vendor=remote",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := jobIdentityRequest(t, carrier)
	calls := 0
	restorer := jobs.TrustedIdentityRestorerFunc(func(ctx context.Context, _ jobs.IdentityRestoreRequest) (jobs.RestoredIdentity, error) {
		calls++
		return jobs.NewRestoredIdentity(context.WithValue(ctx, jobsContextKey{}, "identity-kept"), jobs.ProducerPartition{}, jobs.ProducerActor{})
	})

	identity, err := vvotel.JobIdentity(tel, restorer).RestoreIdentity(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("restorer calls=%d, want one", calls)
	}
	if identity.Context().Value(jobsContextKey{}) != "identity-kept" {
		t.Fatal("identity context values were lost")
	}
	spanContext := trace.SpanContextFromContext(identity.Context())
	if !spanContext.IsValid() || !spanContext.IsRemote() ||
		spanContext.TraceID() != mustTraceID(t, "33333333333333333333333333333333") ||
		spanContext.SpanID() != mustSpanID(t, "4444444444444444") || spanContext.TraceState().String() != "vendor=remote" {
		t.Fatalf("restored span context=%v", spanContext)
	}
	assertJobsPropagationMetric(t, mp, vvotel.OpJobsPropagationExtract, "extracted")
}

func jobIdentityRequest(t *testing.T, carrier jobs.UntrustedTraceCarrier) jobs.IdentityRestoreRequest {
	t.Helper()
	namespace, err := jobs.NamespaceOf("oteltest", "development")
	if err != nil {
		t.Fatal(err)
	}
	definition, err := jobs.ParseName("jobs.telemetry")
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := jobs.ParseIdentityProvenance("framework.system")
	if err != nil {
		t.Fatal(err)
	}
	capture, err := jobs.NewContextCapture(jobs.ContextCaptureSpec{Provenance: provenance, Epoch: 1, Trace: carrier})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.NewTracePolicy()
	if err != nil {
		t.Fatal(err)
	}
	partition, durable, err := jobs.BuildDurableContext(namespace, definition, jobs.PartitionGlobal, policy, capture)
	if err != nil {
		t.Fatal(err)
	}
	var invocationBytes [jobs.InvocationIDBytes]byte
	invocationBytes[0] = 1
	invocation, err := jobs.InvocationIDFromBytes(invocationBytes)
	if err != nil {
		t.Fatal(err)
	}
	var wireBytes [32]byte
	wireBytes[0] = 1
	wire, err := jobs.WireDigestFromBytes(wireBytes)
	if err != nil {
		t.Fatal(err)
	}
	request, err := durable.IdentityRestoreRequest(namespace, partition, definition, invocation, wire, policy)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func assertJobsPropagationMetric(t *testing.T, mp *testMeterProvider, operation string, outcome string) {
	t.Helper()
	if mp.metricCount() != 1 || mp.counterAddCalls != 1 {
		t.Fatalf("propagation metrics=%d counter calls=%d, want one", mp.metricCount(), mp.counterAddCalls)
	}
	measurement := mp.metrics[0]
	if measurement.name != vvotel.MetricJobsPropagation ||
		measurement.attributes[vvotel.AttrOperationName].AsString() != operation ||
		measurement.attributes[vvotel.AttrOperationOutcome].AsString() != outcome {
		t.Fatalf("propagation metric=%+v, want %s/%s", measurement, operation, outcome)
	}
}

func mustTraceID(t *testing.T, raw string) trace.TraceID {
	t.Helper()
	value, err := trace.TraceIDFromHex(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustSpanID(t *testing.T, raw string) trace.SpanID {
	t.Helper()
	value, err := trace.SpanIDFromHex(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
