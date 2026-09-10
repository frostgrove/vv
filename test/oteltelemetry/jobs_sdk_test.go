package oteltelemetry_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type jobsSDKContextKey struct{}

type jobsSDKSender struct {
	description jobs.BackendDescription
	calls       int
	ctx         context.Context
	placement   jobs.Placement
}

func (sender *jobsSDKSender) Description() jobs.BackendDescription { return sender.description }

func (sender *jobsSDKSender) Place(ctx context.Context, placement jobs.Placement) (jobs.PlacementResult, error) {
	sender.calls++
	sender.ctx = ctx
	sender.placement = placement
	return jobs.NewPlacementResult(placement.Candidate(), jobs.PlacementCreated)
}

type jobsSDKController struct{}

func (*jobsSDKController) Pulse(context.Context) error { return nil }

func (*jobsSDKController) Guard(context.Context, jobs.LeaseFence) error { return nil }

type jobsSDKClock struct {
	now time.Time
}

func (clock jobsSDKClock) Now() time.Time { return clock.now }

func (jobsSDKClock) NewTimerAt(time.Time) jobs.Timer { panic("unexpected timer") }

func TestRealSDKJobsProducerAndConsumerPreserveEffectsLinksAndExemplars(t *testing.T) {
	fixture := newSDKFixture(t, sdktrace.AlwaysSample())
	tel := vvotel.Must(vvotel.Config{
		TracerProvider: fixture.tracerProvider,
		MeterProvider:  fixture.meterProvider,
		ResourceName:   vvotel.MustApproveName("jobs"),
	})
	queue, definition, sender := newJobsSDKQueue(t)
	callerContext := context.WithValue(context.Background(), jobsSDKContextKey{}, "producer-caller-value")
	ctx, requestSpan := fixture.tracerProvider.Tracer("application").Start(callerContext, "jobs request", trace.WithSpanKind(trace.SpanKindServer))
	id, err := vvotel.Enqueue(ctx, tel, queue, definition, "jobs-payload-secret-50819", jobs.After(time.Second))
	if err != nil || sender.calls != 1 || id != sender.placement.Candidate() || sender.placement.Delay() != time.Second || !bytes.Equal(sender.placement.Payload().Bytes(), []byte("jobs-payload-secret-50819")) {
		t.Fatalf("Enqueue = %v, %v, calls=%d, delay=%s", id, err, sender.calls, sender.placement.Delay())
	}
	producerContext := trace.SpanContextFromContext(sender.ctx)
	if sender.ctx.Value(jobsSDKContextKey{}) != "producer-caller-value" || !producerContext.IsValid() || producerContext.Equal(requestSpan.SpanContext()) || producerContext.TraceID() != requestSpan.SpanContext().TraceID() {
		t.Fatalf("producer context = %v / %v, request=%v", sender.ctx.Value(jobsSDKContextKey{}), producerContext, requestSpan.SpanContext())
	}
	requestSpan.End()

	remoteProducer := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    producerContext.TraceID(),
		SpanID:     producerContext.SpanID(),
		TraceFlags: producerContext.TraceFlags(),
		TraceState: producerContext.TraceState(),
		Remote:     true,
	})
	deliveryContext := trace.ContextWithRemoteSpanContext(context.WithValue(context.Background(), jobsSDKContextKey{}, "consumer-caller-value"), remoteProducer)
	meta := newJobsSDKMeta(t)
	controller := &jobsSDKController{}
	handlerErr := errors.New("jobs-handler-error-secret-60427")
	handlerCalls := 0
	var handledContext context.Context
	adapter := vvotel.JobAdapter(tel, jobs.AdapterHandler[string](func(next context.Context, payload string, gotMeta jobs.DeliveryMeta, gotController jobs.AttemptController) error {
		handlerCalls++
		handledContext = next
		if payload != "jobs-payload-secret-50819" || gotMeta != meta || gotController != controller {
			t.Fatalf("handler inputs changed: %q / %v / %T", payload, gotMeta, gotController)
		}
		if next.Value(jobsSDKContextKey{}) != "consumer-caller-value" {
			t.Fatal("handler lost caller context value")
		}
		return handlerErr
	}))
	if got := adapter(deliveryContext, "jobs-payload-secret-50819", meta, controller); got != handlerErr || handlerCalls != 1 {
		t.Fatalf("handler = %v, calls=%d", got, handlerCalls)
	}

	spans := fixture.spans.Ended()
	if len(spans) != 3 {
		t.Fatalf("ended spans = %d, want request, producer, consumer", len(spans))
	}
	request := findSpan(t, spans, "jobs request")
	producer := findSpan(t, spans, "vv.jobs enqueue")
	consumer := findSpan(t, spans, vvotel.SpanJobsHandler)
	if producer.SpanKind() != trace.SpanKindProducer || !producer.Parent().Equal(request.SpanContext()) || !producer.SpanContext().Equal(producerContext) {
		t.Fatalf("producer kind/parent/context = %s / %v / %v", producer.SpanKind(), producer.Parent(), producer.SpanContext())
	}
	producerAttributes := attributesByName(producer.Attributes())
	if producer.Status().Code != codes.Unset || producerAttributes[string(vvotel.AttrComponent)] != vvotel.ComponentJobsEnqueue || producerAttributes[string(vvotel.AttrOperationName)] != vvotel.OpJobsEnqueueEnqueue || producerAttributes[string(vvotel.AttrOperationOutcome)] != vvotel.OutcomeOk {
		t.Fatalf("producer status/attributes = %#v / %#v", producer.Status(), producerAttributes)
	}
	if consumer.SpanKind() != trace.SpanKindConsumer || consumer.Parent().IsValid() || len(consumer.Links()) != 1 || !sameJobsSDKSpanContext(consumer.Links()[0].SpanContext, remoteProducer) {
		t.Fatalf("consumer kind/parent/links = %s / %v / %#v", consumer.SpanKind(), consumer.Parent(), consumer.Links())
	}
	consumerAttributes := attributesByName(consumer.Attributes())
	if consumer.Status().Code != codes.Error || consumerAttributes[string(vvotel.AttrComponent)] != vvotel.ComponentJobsHandler || consumerAttributes[string(vvotel.AttrOperationName)] != vvotel.OpJobsHandlerHandle || consumerAttributes[string(vvotel.AttrOperationOutcome)] != vvotel.OutcomeError || consumerAttributes[string(vvotel.AttrErrorType)] != vvotel.ErrorTypeInternal {
		t.Fatalf("consumer status/attributes = %#v / %#v", consumer.Status(), consumerAttributes)
	}
	if !trace.SpanContextFromContext(handledContext).Equal(consumer.SpanContext()) {
		t.Fatalf("handler active span = %v, want %v", trace.SpanContextFromContext(handledContext), consumer.SpanContext())
	}

	metrics := collectMetricData(t, fixture.metrics)
	assertFloatMetricExemplar(t, metrics, vvotel.MetricJobsEnqueueDuration, producer.SpanContext())
	assertFloatMetricExemplar(t, metrics, vvotel.MetricJobsHandlerDuration, consumer.SpanContext())
	assertFloatMetricExemplar(t, metrics, vvotel.MetricJobsQueueDelay, consumer.SpanContext())
	assertJobsSDKIntHistogramExemplar(t, metrics, vvotel.MetricJobsHandlerAttempt, consumer.SpanContext())
	assertSDKPrivacy(t, spans, metrics, []string{
		"jobs-payload-secret-50819",
		"jobs-handler-error-secret-60427",
		meta.Definition().String(),
		meta.Binding().String(),
		meta.InvocationID().String(),
	})
}

func TestRealSDKJobsSchedulerPreservesRunDueAndCorrelatesEveryMetric(t *testing.T) {
	fixture := newSDKFixture(t, sdktrace.AlwaysSample())
	tel := vvotel.Must(vvotel.Config{TracerProvider: fixture.tracerProvider, MeterProvider: fixture.meterProvider})
	queue, definition, sender := newJobsSDKQueue(t)
	now := time.Date(2034, 5, 6, 7, 8, 9, 0, time.UTC)
	scheduleName, err := jobs.ParseName("jobs.sdk.schedule.secret")
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := jobs.DefineSchedule(jobs.ScheduleSpec[string]{
		Name:     scheduleName,
		Revision: 1,
		Cadence:  jobs.At(now),
		Job:      definition,
		Payload:  func(time.Time) (string, error) { return "jobs-scheduled-payload-secret-17396", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := jobs.NewScheduler(jobs.SchedulerSpec{
		Queue:    queue,
		Clock:    jobsSDKClock{now: now},
		Observer: vvotel.Scheduler(tel),
	}, schedule)
	if err != nil {
		t.Fatal(err)
	}
	caller := context.WithValue(context.Background(), jobsSDKContextKey{}, "scheduler-caller-value")
	ctx, parent := fixture.tracerProvider.Tracer("application").Start(caller, "scheduler request", trace.WithSpanKind(trace.SpanKindInternal))
	result, err := scheduler.RunDue(ctx)
	parent.End()
	if err != nil || result != (jobs.ScheduleRunResult{Due: 1, Placed: 1}) || sender.calls != 1 || sender.ctx.Value(jobsSDKContextKey{}) != "scheduler-caller-value" {
		t.Fatalf("RunDue = %#v, %v, calls=%d, context=%v", result, err, sender.calls, sender.ctx.Value(jobsSDKContextKey{}))
	}
	parentSpan := findSpan(t, fixture.spans.Ended(), "scheduler request")
	metrics := collectMetricData(t, fixture.metrics)
	assertIntMetricExemplar(t, metrics, vvotel.MetricJobsSchedulerCycles, parentSpan.SpanContext())
	assertFloatMetricExemplar(t, metrics, vvotel.MetricJobsSchedulerDuration, parentSpan.SpanContext())
	assertJobsSDKIntHistogramExemplar(t, metrics, vvotel.MetricJobsSchedulerResults, parentSpan.SpanContext())
	assertSDKPrivacy(t, []sdktrace.ReadOnlySpan{parentSpan}, metrics, []string{
		scheduleName.String(),
		definition.Name().String(),
		"jobs-scheduled-payload-secret-17396",
		sender.placement.Candidate().String(),
	})
}

func newJobsSDKQueue(t *testing.T) (*jobs.Queue, *jobs.Definition[string], *jobsSDKSender) {
	t.Helper()
	name, err := jobs.ParseName("jobs.sdk.secret.definition")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	definition := jobs.MustDefine(jobs.DefinitionSpec[string]{
		Name:      name,
		Codec:     jobs.String(jobs.SchemaVersion(1)),
		Policy:    policy,
		Partition: jobs.PartitionGlobal,
	})
	namespace, err := jobs.NamespaceOf("otel", "sdk-jobs")
	if err != nil {
		t.Fatal(err)
	}
	var backendBytes [jobs.BackendIDBytes]byte
	backendBytes[0] = 9
	backend, err := jobs.BackendIDFromBytes(backendBytes)
	if err != nil {
		t.Fatal(err)
	}
	failures, err := jobs.Failures()
	if err != nil {
		t.Fatal(err)
	}
	durability, err := jobs.NewDurabilityProfile(jobs.AckBeforePersistence, jobs.AcknowledgedLossPossible, failures)
	if err != nil {
		t.Fatal(err)
	}
	description, err := jobs.NewBackendDescription(backend, durability, jobs.Capabilities{Priority: true, Debounce: true, Unique: true, Scheduled: true, AttemptTrace: true})
	if err != nil {
		t.Fatal(err)
	}
	sender := &jobsSDKSender{description: description}
	queue, err := jobs.NewQueue(jobs.QueueSpec{
		Namespace: namespace,
		Catalog:   jobs.MustCatalog(definition),
		Sender:    sender,
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{19}, 256)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return queue, definition, sender
}

func newJobsSDKMeta(t *testing.T) jobs.DeliveryMeta {
	t.Helper()
	invocation, err := jobs.NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := jobs.ParseName("jobs.sdk.handler.secret")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := jobs.ParseBindingName("jobs.sdk.binding.secret")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobs-sdk-secret@1")
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

func assertJobsSDKIntHistogramExemplar(t *testing.T, data metricdata.ResourceMetrics, name string, spanContext trace.SpanContext) {
	t.Helper()
	measurement := findMetricData(t, data, name)
	histogram, ok := measurement.Data.(metricdata.Histogram[int64])
	if !ok || len(histogram.DataPoints) == 0 {
		t.Fatalf("metric %q data = %T", name, measurement.Data)
	}
	for _, point := range histogram.DataPoints {
		for _, exemplar := range point.Exemplars {
			if exemplarMatches(exemplar.TraceID, exemplar.SpanID, spanContext) {
				return
			}
		}
	}
	t.Fatalf("metric %q has no exemplar for %s/%s", name, spanContext.TraceID(), spanContext.SpanID())
}

func sameJobsSDKSpanContext(left trace.SpanContext, right trace.SpanContext) bool {
	return left.TraceID() == right.TraceID() && left.SpanID() == right.SpanID() && left.TraceFlags() == right.TraceFlags() && left.TraceState().String() == right.TraceState().String() && left.IsRemote() == right.IsRemote()
}
