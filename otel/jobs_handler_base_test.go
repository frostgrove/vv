package vvotel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type jobsHandlerContextKey struct{}

type jobsHandlerController struct{}

func (*jobsHandlerController) Pulse(context.Context) error { return nil }

func (*jobsHandlerController) Guard(context.Context, jobs.LeaseFence) error { return nil }

func TestJobAdapterCreatesLinkedConsumerSpanAndPreservesHandlerInputs(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel := vvotel.Must(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	meta := jobsHandlerTestMeta(t)
	controller := &jobsHandlerController{}
	parent := jobsHandlerRemoteSpanContext()
	base, cancel := context.WithCancelCause(context.WithValue(context.Background(), jobsHandlerContextKey{}, "application-value"))
	ctx := trace.ContextWithRemoteSpanContext(base, parent)
	var received context.Context
	adapter := vvotel.JobAdapter(tel, jobs.AdapterHandler[string](func(next context.Context, payload string, gotMeta jobs.DeliveryMeta, gotController jobs.AttemptController) error {
		received = next
		if payload != "secret-payload" || gotMeta != meta || gotController != controller {
			t.Fatalf("handler inputs changed: %q / %v / %T", payload, gotMeta, gotController)
		}
		if next.Value(jobsHandlerContextKey{}) != "application-value" || next.Done() != ctx.Done() || trace.SpanFromContext(next) == nil {
			t.Fatal("handler context lost values, lifetime, or active span")
		}
		return nil
	}))
	if err := adapter(ctx, "secret-payload", meta, controller); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("delayed-cancellation-cause")
	cancel(cause)
	if context.Cause(received) != cause {
		t.Fatalf("cancellation cause = %v", context.Cause(received))
	}

	if len(tp.spans) != 1 {
		t.Fatalf("spans = %d", len(tp.spans))
	}
	span := tp.spans[0]
	if span.name != vvotel.SpanJobsHandler || span.kind != trace.SpanKindConsumer || !span.newRoot || len(span.links) != 1 || !sameJobsHandlerSpanContext(span.links[0].SpanContext, parent) || !span.ended || span.status != codes.Unset {
		t.Fatalf("handler span = %+v", span)
	}
	if span.attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentJobsHandler || span.attributes[vvotel.AttrOperationName].AsString() != vvotel.OpJobsHandlerHandle || span.attributes[vvotel.AttrOperationOutcome].AsString() != vvotel.OutcomeOk {
		t.Fatalf("handler span attributes = %v", span.attributes)
	}
	if mp.metricCount() != 3 {
		t.Fatalf("metrics = %d, want duration, queue delay, attempt", mp.metricCount())
	}
	values := map[string]any{}
	for _, measurement := range mp.metrics {
		values[measurement.name] = measurement.value
		if measurement.context != received || measurement.attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentJobsHandler || measurement.attributes[vvotel.AttrOperationName].AsString() != vvotel.OpJobsHandlerHandle {
			t.Fatalf("handler metric = %+v", measurement)
		}
	}
	if values[vvotel.MetricJobsQueueDelay] != float64(2) || values[vvotel.MetricJobsHandlerAttempt] != int64(2) {
		t.Fatalf("queue delay/attempt = %v/%v", values[vvotel.MetricJobsQueueDelay], values[vvotel.MetricJobsHandlerAttempt])
	}
	assertJobsEnqueueTelemetryExcludes(t, tp.spans, mp.metrics, "secret-payload", meta.Definition().String(), meta.Binding().String(), meta.InvocationID().String())
}

func TestJobAdapterParentChildModeAndPanicTransparency(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel := vvotel.Must(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	meta := jobsHandlerTestMeta(t)
	panicValue := &struct{ secret string }{secret: "handler-panic-secret"}
	adapter := vvotel.JobAdapter(tel, jobs.AdapterHandler[int](func(context.Context, int, jobs.DeliveryMeta, jobs.AttemptController) error {
		panic(panicValue)
	}), vvotel.ParentChild())
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), jobsHandlerRemoteSpanContext())

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = adapter(ctx, 17, meta, &jobsHandlerController{})
	}()
	if recovered != panicValue {
		t.Fatalf("panic = %#v", recovered)
	}
	if len(tp.spans) != 1 || tp.spans[0].newRoot || len(tp.spans[0].links) != 0 || !tp.spans[0].ended || tp.spans[0].status != codes.Error || tp.spans[0].attributes[vvotel.AttrErrorType].AsString() != vvotel.ErrorTypePanic {
		t.Fatalf("parent-child panic span = %+v", tp.spans)
	}
	if mp.metricCount() != 3 {
		t.Fatalf("panic metrics = %d", mp.metricCount())
	}
	assertJobsEnqueueTelemetryExcludes(t, tp.spans, mp.metrics, panicValue.secret)
}

func TestJobBuildsAValidConsumerWithWorkerOptions(t *testing.T) {
	_, definition, _, _ := jobsEnqueueTestFixture(t)
	consumer := vvotel.Job[string](nil, definition, func(context.Context, string) error { return nil }, jobs.Concurrency(3))
	if consumer == nil || consumer.Declaration() != definition {
		t.Fatalf("consumer/declaration = %v/%v", consumer, consumer.Declaration())
	}
	if _, err := jobs.NewWorkerPlan(jobs.MustCatalog(definition), consumer); err != nil {
		t.Fatalf("worker options were not accepted: %v", err)
	}
}

func jobsHandlerTestMeta(t *testing.T) jobs.DeliveryMeta {
	t.Helper()
	invocation, err := jobs.NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := jobs.ParseName("secret.handler.definition")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := jobs.ParseBindingName("secret.handler.binding")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("secret-handler-build@1")
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := jobs.NewAttemptOrdinal(2)
	if err != nil {
		t.Fatal(err)
	}
	retrySpent, err := jobs.NewRetrySpent(1)
	if err != nil {
		t.Fatal(err)
	}
	retryLimit, err := jobs.NewRetryLimit(3)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	eligible := created.Add(time.Second)
	started := eligible.Add(2 * time.Second)
	meta, err := jobs.NewDeliveryMeta(jobs.DeliveryMetaSpec{
		Invocation:       invocation,
		Definition:       definition,
		Binding:          binding,
		Build:            build,
		Attempt:          attempt,
		RetrySpent:       retrySpent,
		RetryLimit:       retryLimit,
		CreatedAt:        created,
		EligibleAt:       eligible,
		StartedAt:        started,
		AttemptDeadline:  started.Add(time.Minute),
		MaxElapsedAt:     eligible.Add(time.Hour),
		ProgressDeadline: started.Add(30 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func jobsHandlerRemoteSpanContext() trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
}

func sameJobsHandlerSpanContext(left trace.SpanContext, right trace.SpanContext) bool {
	return left.TraceID() == right.TraceID() && left.SpanID() == right.SpanID() && left.TraceFlags() == right.TraceFlags() && left.TraceState().String() == right.TraceState().String() && left.IsRemote() == right.IsRemote()
}
