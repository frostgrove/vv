package oteltelemetry_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsmemory"
	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type jobsRestartContextKey struct{}

type jobsRestartClock struct {
	mu     sync.Mutex
	now    time.Time
	tick   time.Duration
	timers map[*jobsRestartTimer]struct{}
}

type jobsRestartTimer struct {
	clock    *jobsRestartClock
	deadline time.Time
	channel  chan time.Time
	active   bool
}

func newJobsRestartClock(now time.Time) *jobsRestartClock {
	return &jobsRestartClock{now: now, tick: time.Microsecond, timers: make(map[*jobsRestartTimer]struct{})}
}

func (clock *jobsRestartClock) Now() time.Time {
	clock.mu.Lock()
	result := clock.now
	clock.advanceLocked(clock.tick)
	clock.mu.Unlock()
	return result
}

func (clock *jobsRestartClock) NewTimerAt(deadline time.Time) jobs.Timer {
	clock.mu.Lock()
	timer := &jobsRestartTimer{clock: clock, deadline: deadline, channel: make(chan time.Time, 1), active: true}
	if deadline.After(clock.now) {
		clock.timers[timer] = struct{}{}
	} else {
		timer.active = false
		timer.channel <- clock.now
	}
	clock.mu.Unlock()
	return timer
}

func (clock *jobsRestartClock) Advance(delta time.Duration) {
	clock.mu.Lock()
	clock.advanceLocked(delta)
	clock.mu.Unlock()
}

func (clock *jobsRestartClock) advanceLocked(delta time.Duration) {
	clock.now = clock.now.Add(delta)
	for timer := range clock.timers {
		if timer.deadline.After(clock.now) {
			continue
		}
		delete(clock.timers, timer)
		timer.active = false
		timer.channel <- clock.now
	}
}

func (timer *jobsRestartTimer) C() <-chan time.Time { return timer.channel }

func (timer *jobsRestartTimer) Stop() bool {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	if !timer.active {
		return false
	}
	delete(timer.clock.timers, timer)
	timer.active = false
	return true
}

type jobsRestartAttempt struct {
	payload    string
	meta       jobs.DeliveryMeta
	controller jobs.AttemptController
	marker     any
	active     trace.SpanContext
	result     error
}

type jobsRestartFinishObserver struct {
	disposition jobs.DispositionKind
	done        chan struct{}
	once        sync.Once
}

type jobsRestartSDKFixture struct {
	*sdkFixture
	shutdownOnce sync.Once
	shutdownErr  error
}

func newJobsRestartFinishObserver(disposition jobs.DispositionKind) *jobsRestartFinishObserver {
	return &jobsRestartFinishObserver{disposition: disposition, done: make(chan struct{})}
}

func (observer *jobsRestartFinishObserver) Observe(_ context.Context, event jobs.WorkerEvent) {
	if event.Operation() == jobs.WorkerOperationApply && event.Outcome() == jobs.WorkerOutcomeComplete &&
		event.CommandKind() == jobs.DeliveryCommandFinishAttempt && event.Disposition() == observer.disposition {
		observer.once.Do(func() { close(observer.done) })
	}
}

func TestRealSDKJobsApplicationRestartRetainsBackendRecordAndLinksRetryAttempts(t *testing.T) {
	clock := newJobsRestartClock(time.Date(2038, 4, 5, 6, 7, 8, 0, time.UTC))
	backend, err := jobsmemory.NewDefault(jobsmemory.WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := backend.Close(); closeErr != nil {
			t.Errorf("close retained jobs backend: %v", closeErr)
		}
	})

	definitionSecret := "jobs-restart-definition-secret-61842"
	bindingSecret := "jobs-restart-binding-secret-92517"
	payloadSecret := "jobs-restart-payload-secret-30764"
	retrySecret := "jobs-restart-retry-secret-58431"
	namespaceAppSecret := "jobs-restart-app-secret-43915"
	namespaceEnvSecret := "jobs-restart-env-secret-80623"
	firstBuildSecret := "jobs-restart-build-secret-one"
	secondBuildSecret := "jobs-restart-build-secret-two"
	firstMarker := new(int)
	secondMarker := new(int)

	name, err := jobs.ParseName(definitionSecret)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.With(
		jobs.Retries(1),
		jobs.RetryBackoff(jobs.Exponential(jobs.MinRetryDelay, jobs.MinRetryDelay, jobs.NoJitter)),
	).Build()
	if err != nil {
		t.Fatal(err)
	}
	definition := jobs.MustDefine(jobs.DefinitionSpec[string]{
		Name:      name,
		Codec:     jobs.String(jobs.SchemaVersion(1)),
		Policy:    policy,
		Partition: jobs.PartitionGlobal,
	})
	catalog := jobs.MustCatalog(definition)
	namespace, err := jobs.NamespaceOf(namespaceAppSecret, namespaceEnvSecret)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := jobs.NewAdmissionSnapshot(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = admission.Publisher().Update(1, jobs.HeldReason{}, clock.Now()); err != nil {
		t.Fatal(err)
	}

	firstFixture := newJobsRestartSDKFixture(t)
	firstTelemetry := vvotel.Must(vvotel.Config{
		TracerProvider: firstFixture.tracerProvider,
		MeterProvider:  firstFixture.meterProvider,
		ResourceName:   vvotel.MustApproveName("jobs-restart-first"),
	})
	firstQueue := newJobsRestartQueue(t, namespace, catalog, backend, firstTelemetry, 31)
	state, err := trace.ParseTraceState("vendor=restart")
	if err != nil {
		t.Fatal(err)
	}
	upstream := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    jobsRestartTraceID(t, "1234567890abcdef1234567890abcdef"),
		SpanID:     jobsRestartSpanID(t, "1234567890abcdef"),
		TraceFlags: trace.FlagsSampled,
		TraceState: state,
		Remote:     true,
	})
	requestBase := trace.ContextWithRemoteSpanContext(context.WithValue(context.Background(), jobsRestartContextKey{}, firstMarker), upstream)
	requestContext, requestSpan := firstFixture.tracerProvider.Tracer("application.jobs.restart").Start(requestBase, "jobs restart request", trace.WithSpanKind(trace.SpanKindServer))
	invocation, err := vvotel.Enqueue(requestContext, firstTelemetry, firstQueue, definition, payloadSecret)
	requestSpan.End()
	if err != nil || invocation.IsZero() {
		t.Fatalf("enqueue = %v, %v", invocation, err)
	}
	if stats := backend.Stats(); stats.Records != 1 || stats.Leased != 0 || stats.Closed {
		t.Fatalf("backend after enqueue = %+v", stats)
	}

	retryErr := errors.New(retrySecret)
	firstAttempt := make(chan jobsRestartAttempt, 2)
	firstFinish := newJobsRestartFinishObserver(jobs.DispositionRetry)
	var firstRecorded jobsRestartAttempt
	firstAdapter := vvotel.JobAdapter(firstTelemetry, jobs.AdapterHandler[string](func(ctx context.Context, payload string, meta jobs.DeliveryMeta, controller jobs.AttemptController) error {
		firstRecorded = jobsRestartAttempt{
			payload:    payload,
			meta:       meta,
			controller: controller,
			marker:     ctx.Value(jobsRestartContextKey{}),
			active:     trace.SpanContextFromContext(ctx),
		}
		return retryErr
	}))
	firstConsumer := jobs.OnAdapter(
		definition,
		jobs.AdapterHandler[string](func(ctx context.Context, payload string, meta jobs.DeliveryMeta, controller jobs.AttemptController) error {
			result := firstAdapter(ctx, payload, meta, controller)
			firstRecorded.result = result
			firstAttempt <- firstRecorded
			return result
		}),
		jobs.Binding(bindingSecret),
		jobs.Concurrency(1),
		jobs.WithAdmission(admission.Reader()),
	)
	firstBuild := jobsRestartBuild(t, firstBuildSecret)
	firstWorkers := newJobsRestartWorkers(t, namespace, catalog, backend, firstBuild, clock, firstTelemetry, firstFinish, firstConsumer, 41)
	firstRunBase, firstRuntime := firstFixture.tracerProvider.Tracer("application.jobs.worker").Start(context.Background(), "jobs worker runtime first")
	firstRunContext := context.WithValue(firstRunBase, jobsRestartContextKey{}, firstMarker)
	firstRun := make(chan error, 1)
	go func() { firstRun <- firstWorkers.Run(firstRunContext) }()
	awaitJobsRestartFinish(t, firstFinish.done, firstRun, "retry disposition")
	drainJobsRestartWorkers(t, firstWorkers, firstRunContext, firstRun)
	firstRuntime.End()
	firstCall := awaitJobsRestartAttempt(t, firstAttempt, "first handler call")
	assertJobsRestartCall(t, firstCall, payloadSecret, invocation, definition.Name(), bindingSecret, firstBuild, 1, 0, firstMarker, retryErr)
	assertNoJobsRestartAttempt(t, firstAttempt, "first worker")
	if stats := backend.Stats(); stats.Records != 1 || stats.Leased != 0 || stats.Closed {
		t.Fatalf("retained backend after retry = %+v", stats)
	}

	firstSpans := firstFixture.spans.Ended()
	firstMetrics := collectMetricData(t, firstFixture.metrics)
	if len(firstSpans) != 4 {
		t.Fatalf("first provider ended spans = %d, want request, producer, consumer, runtime", len(firstSpans))
	}
	request := findSpan(t, firstSpans, "jobs restart request")
	producer := findSpan(t, firstSpans, "vv.jobs enqueue")
	firstConsumerSpan := findSpan(t, firstSpans, vvotel.SpanJobsHandler)
	firstRuntimeSpan := findSpan(t, firstSpans, "jobs worker runtime first")
	if !sameJobsSDKSpanContext(request.Parent(), upstream) || producer.SpanKind() != trace.SpanKindProducer || !sameJobsSDKSpanContext(producer.Parent(), request.SpanContext()) || !producer.SpanContext().IsSampled() || producer.SpanContext().TraceState().String() != state.String() {
		t.Fatalf("request/producer lineage = parent %v, producer %s/%v/%v", request.Parent(), producer.SpanKind(), producer.Parent(), producer.SpanContext())
	}
	remoteProducer := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    producer.SpanContext().TraceID(),
		SpanID:     producer.SpanContext().SpanID(),
		TraceFlags: producer.SpanContext().TraceFlags(),
		TraceState: producer.SpanContext().TraceState(),
		Remote:     true,
	})
	assertJobsRestartConsumer(t, firstConsumerSpan, remoteProducer, firstCall.active)
	assertJobsRestartConsumerOutcome(t, firstConsumerSpan, true)
	assertFloatMetricExemplar(t, firstMetrics, vvotel.MetricJobsEnqueueDuration, producer.SpanContext())
	assertJobsRestartHandlerMetrics(t, firstMetrics, firstConsumerSpan.SpanContext(), 1)
	assertJobsRestartWorkerMetrics(t, firstMetrics, firstRuntimeSpan.SpanContext())
	assertJobsRestartDispositionMetric(t, firstMetrics, firstRuntimeSpan.SpanContext(), "retry", "handler_failure")
	assertJobsRestartPropagation(t, firstMetrics, vvotel.OpJobsPropagationInject, "injected", 1)
	assertJobsRestartPropagation(t, firstMetrics, vvotel.OpJobsPropagationExtract, "extracted", 1)

	shutdownJobsRestartSDK(t, firstFixture)
	clock.Advance(jobs.MinRetryDelay)

	secondFixture := newJobsRestartSDKFixture(t)
	secondTelemetry := vvotel.Must(vvotel.Config{
		TracerProvider: secondFixture.tracerProvider,
		MeterProvider:  secondFixture.meterProvider,
		ResourceName:   vvotel.MustApproveName("jobs-restart-second"),
	})
	secondQueue := newJobsRestartQueue(t, namespace, catalog, backend, secondTelemetry, 51)
	if secondQueue == firstQueue {
		t.Fatal("application restart reused the queue object")
	}
	secondAttempt := make(chan jobsRestartAttempt, 2)
	secondFinish := newJobsRestartFinishObserver(jobs.DispositionSucceeded)
	var secondRecorded jobsRestartAttempt
	secondAdapter := vvotel.JobAdapter(secondTelemetry, jobs.AdapterHandler[string](func(ctx context.Context, payload string, meta jobs.DeliveryMeta, controller jobs.AttemptController) error {
		secondRecorded = jobsRestartAttempt{
			payload:    payload,
			meta:       meta,
			controller: controller,
			marker:     ctx.Value(jobsRestartContextKey{}),
			active:     trace.SpanContextFromContext(ctx),
		}
		return nil
	}))
	secondConsumer := jobs.OnAdapter(
		definition,
		jobs.AdapterHandler[string](func(ctx context.Context, payload string, meta jobs.DeliveryMeta, controller jobs.AttemptController) error {
			result := secondAdapter(ctx, payload, meta, controller)
			secondRecorded.result = result
			secondAttempt <- secondRecorded
			return result
		}),
		jobs.Binding(bindingSecret),
		jobs.Concurrency(1),
		jobs.WithAdmission(admission.Reader()),
	)
	secondBuild := jobsRestartBuild(t, secondBuildSecret)
	secondWorkers := newJobsRestartWorkers(t, namespace, catalog, backend, secondBuild, clock, secondTelemetry, secondFinish, secondConsumer, 61)
	secondRunBase, secondRuntime := secondFixture.tracerProvider.Tracer("application.jobs.worker").Start(context.Background(), "jobs worker runtime second")
	secondRunContext := context.WithValue(secondRunBase, jobsRestartContextKey{}, secondMarker)
	secondRun := make(chan error, 1)
	go func() { secondRun <- secondWorkers.Run(secondRunContext) }()
	awaitJobsRestartFinish(t, secondFinish.done, secondRun, "successful disposition")
	drainJobsRestartWorkers(t, secondWorkers, secondRunContext, secondRun)
	secondRuntime.End()
	secondCall := awaitJobsRestartAttempt(t, secondAttempt, "second handler call")
	assertJobsRestartCall(t, secondCall, payloadSecret, invocation, definition.Name(), bindingSecret, secondBuild, 2, 1, secondMarker, nil)
	assertNoJobsRestartAttempt(t, secondAttempt, "second worker")
	if stats := backend.Stats(); stats.Records != 0 || stats.Leased != 0 || stats.Bytes != 0 || stats.Closed {
		t.Fatalf("retained backend after success = %+v", stats)
	}

	secondSpans := secondFixture.spans.Ended()
	secondMetrics := collectMetricData(t, secondFixture.metrics)
	if len(secondSpans) != 2 {
		t.Fatalf("second provider ended spans = %d, want consumer and runtime", len(secondSpans))
	}
	secondConsumerSpan := findSpan(t, secondSpans, vvotel.SpanJobsHandler)
	secondRuntimeSpan := findSpan(t, secondSpans, "jobs worker runtime second")
	assertJobsRestartConsumer(t, secondConsumerSpan, remoteProducer, secondCall.active)
	assertJobsRestartConsumerOutcome(t, secondConsumerSpan, false)
	if firstConsumerSpan.SpanContext().TraceID() == secondConsumerSpan.SpanContext().TraceID() || firstConsumerSpan.SpanContext().SpanID() == secondConsumerSpan.SpanContext().SpanID() {
		t.Fatal("retry attempts reused a consumer trace or span identity")
	}
	assertJobsRestartHandlerMetrics(t, secondMetrics, secondConsumerSpan.SpanContext(), 2)
	assertJobsRestartWorkerMetrics(t, secondMetrics, secondRuntimeSpan.SpanContext())
	assertJobsRestartDispositionMetric(t, secondMetrics, secondRuntimeSpan.SpanContext(), "succeeded", "none")
	assertJobsRestartPropagation(t, secondMetrics, vvotel.OpJobsPropagationExtract, "extracted", 1)

	secrets := []string{
		definitionSecret,
		bindingSecret,
		payloadSecret,
		retrySecret,
		namespaceAppSecret,
		namespaceEnvSecret,
		firstBuildSecret,
		secondBuildSecret,
		invocation.String(),
	}
	assertSDKPrivacy(t, firstSpans, firstMetrics, secrets)
	assertSDKPrivacy(t, secondSpans, secondMetrics, secrets)
}

func TestRealSDKJobsCarrierCompatibilityControls(t *testing.T) {
	validParent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	t.Run("malformed jobs carriers are rejected before restoration", func(t *testing.T) {
		for index, spec := range []jobs.TraceCarrierSpec{
			{TraceParent: "00-00000000000000000000000000000000-00f067aa0ba902b7-01"},
			{TraceParent: "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01"},
			{TraceState: "vendor=missing-parent"},
		} {
			if _, err := jobs.NewUntrustedTraceCarrier(spec); !errors.Is(err, jobs.ErrInvalid) {
				t.Fatalf("malformed carrier %d error = %v", index, err)
			}
		}
	})

	for _, test := range []struct {
		name        string
		carrier     jobs.TraceCarrierSpec
		outcome     string
		wantValid   bool
		wantState   string
		wantSampled bool
	}{
		{
			name:      "jobs-valid OTel-invalid trace flags drop the parent",
			carrier:   jobs.TraceCarrierSpec{TraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-09", TraceState: "vendor=jobs-valid"},
			outcome:   "traceparent_dropped",
			wantValid: false,
		},
		{
			name:        "jobs-valid OTel-invalid tracestate keeps the parent without state",
			carrier:     jobs.TraceCarrierSpec{TraceParent: validParent, TraceState: " , "},
			outcome:     "tracestate_dropped",
			wantValid:   true,
			wantSampled: true,
		},
		{
			name:        "shared-valid carrier restores the exact remote context",
			carrier:     jobs.TraceCarrierSpec{TraceParent: validParent, TraceState: "vendor=compatible"},
			outcome:     "extracted",
			wantValid:   true,
			wantState:   "vendor=compatible",
			wantSampled: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			carrier, err := jobs.NewUntrustedTraceCarrier(test.carrier)
			if err != nil {
				t.Fatalf("jobs carrier rejected: %v", err)
			}
			fixture := newSDKFixture(t, sdktrace.AlwaysSample())
			tel := vvotel.Must(vvotel.Config{MeterProvider: fixture.meterProvider})
			marker := new(int)
			identity, err := vvotel.JobIdentity(tel, jobs.SystemIdentityRestorer()).RestoreIdentity(
				context.WithValue(context.Background(), jobsRestartContextKey{}, marker),
				jobsRestartIdentityRequest(t, carrier),
			)
			if err != nil {
				t.Fatal(err)
			}
			if identity.Context().Value(jobsRestartContextKey{}) != marker {
				t.Fatal("carrier restoration lost the application context")
			}
			got := trace.SpanContextFromContext(identity.Context())
			if got.IsValid() != test.wantValid {
				t.Fatalf("restored context validity = %t, want %t: %v", got.IsValid(), test.wantValid, got)
			}
			if test.wantValid {
				if !got.IsRemote() || got.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || got.SpanID().String() != "00f067aa0ba902b7" || got.IsSampled() != test.wantSampled || got.TraceState().String() != test.wantState {
					t.Fatalf("restored context = %v, state %q", got, got.TraceState().String())
				}
			}
			metrics := collectMetricData(t, fixture.metrics)
			assertJobsRestartPropagation(t, metrics, vvotel.OpJobsPropagationExtract, test.outcome, 1)
		})
	}
}

func newJobsRestartQueue(t *testing.T, namespace jobs.Namespace, catalog jobs.Catalog, backend *jobsmemory.Backend, telemetry *vvotel.Telemetry, entropy byte) *jobs.Queue {
	t.Helper()
	queue, err := jobs.NewQueue(jobs.QueueSpec{
		Namespace: namespace,
		Catalog:   catalog,
		Sender:    backend,
		Context:   vvotel.JobContext(telemetry, jobs.SystemContextProvider()),
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{entropy}, 512)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return queue
}

func newJobsRestartWorkers(t *testing.T, namespace jobs.Namespace, catalog jobs.Catalog, backend *jobsmemory.Backend, build jobs.BuildID, clock jobs.Clock, telemetry *vvotel.Telemetry, finish *jobsRestartFinishObserver, consumer jobs.Consumer, entropy byte) *jobs.Workers {
	t.Helper()
	workers, err := jobs.NewWorkers(jobs.WorkersSpec{
		Namespace:    namespace,
		Catalog:      catalog,
		Driver:       backend,
		Build:        build,
		Identity:     vvotel.JobIdentity(telemetry, jobs.SystemIdentityRestorer()),
		Observer:     jobs.MustWorkerObservers(vvotel.Workers(telemetry), finish),
		Clock:        clock,
		Entropy:      bytes.NewReader(bytes.Repeat([]byte{entropy}, 1024)),
		PollInterval: jobs.MinimumPollInterval,
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	return workers
}

func awaitJobsRestartFinish(t *testing.T, finished <-chan struct{}, run <-chan error, label string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-finished:
	case err := <-run:
		t.Fatalf("workers exited before %s: %v", label, err)
	case <-ctx.Done():
		t.Fatalf("workers did not reach %s: %v", label, ctx.Err())
	}
}

func drainJobsRestartWorkers(t *testing.T, workers *jobs.Workers, runContext context.Context, run <-chan error) {
	t.Helper()
	drainContext, cancel := context.WithTimeout(runContext, 5*time.Second)
	defer cancel()
	if err := workers.Drain(drainContext); err != nil {
		t.Fatalf("drain workers: %v", err)
	}
	select {
	case err := <-run:
		if err != nil {
			t.Fatalf("run workers: %v", err)
		}
	case <-drainContext.Done():
		t.Fatalf("workers did not stop: %v", drainContext.Err())
	}
}

func awaitJobsRestartAttempt(t *testing.T, attempts <-chan jobsRestartAttempt, label string) jobsRestartAttempt {
	t.Helper()
	select {
	case attempt := <-attempts:
		return attempt
	default:
		t.Fatalf("%s was not recorded", label)
		return jobsRestartAttempt{}
	}
}

func assertNoJobsRestartAttempt(t *testing.T, attempts <-chan jobsRestartAttempt, label string) {
	t.Helper()
	select {
	case extra := <-attempts:
		t.Fatalf("%s made an extra handler call: %v", label, extra)
	default:
	}
}

func assertJobsRestartCall(t *testing.T, call jobsRestartAttempt, payload string, invocation jobs.InvocationID, definition jobs.Name, binding string, build jobs.BuildID, attempt uint16, retry uint16, marker any, result error) {
	t.Helper()
	if call.payload != payload || call.meta.InvocationID() != invocation || call.meta.Definition() != definition || call.meta.Binding().String() != binding || call.meta.Build() != build || call.meta.AttemptOrdinal().Value() != attempt || call.meta.RetrySpent().Value() != retry || call.meta.RetryLimit().Value() != 1 || call.marker != marker || call.controller == nil || call.result != result || !call.active.IsValid() {
		t.Fatalf("handler call changed: payload=%q meta=%v marker=%v controller=%T result=%v active=%v", call.payload, call.meta, call.marker, call.controller, call.result, call.active)
	}
}

func assertJobsRestartConsumer(t *testing.T, consumer sdktrace.ReadOnlySpan, producer trace.SpanContext, active trace.SpanContext) {
	t.Helper()
	if consumer.SpanKind() != trace.SpanKindConsumer || consumer.Parent().IsValid() || len(consumer.Links()) != 1 || len(consumer.Links()[0].Attributes) != 0 || !sameJobsSDKSpanContext(consumer.Links()[0].SpanContext, producer) || !sameJobsSDKSpanContext(consumer.SpanContext(), active) || !consumer.SpanContext().IsSampled() || consumer.SpanContext().TraceID() == producer.TraceID() {
		t.Fatalf("consumer kind/parent/link/active = %s / %v / %#v / %v", consumer.SpanKind(), consumer.Parent(), consumer.Links(), active)
	}
}

func assertJobsRestartConsumerOutcome(t *testing.T, consumer sdktrace.ReadOnlySpan, failed bool) {
	t.Helper()
	attributes := attributesByName(consumer.Attributes())
	wantStatus := codes.Unset
	wantOutcome := vvotel.OutcomeOk
	if failed {
		wantStatus = codes.Error
		wantOutcome = vvotel.OutcomeError
	}
	if consumer.Status().Code != wantStatus || attributes[string(vvotel.AttrOperationOutcome)] != wantOutcome {
		t.Fatalf("consumer status/outcome = %s/%v, want %s/%s", consumer.Status().Code, attributes[string(vvotel.AttrOperationOutcome)], wantStatus, wantOutcome)
	}
	_, hasErrorType := attributes[string(vvotel.AttrErrorType)]
	if failed && (!hasErrorType || attributes[string(vvotel.AttrErrorType)] != vvotel.ErrorTypeInternal) || !failed && hasErrorType {
		t.Fatalf("consumer error type = %v, present=%t, failed=%t", attributes[string(vvotel.AttrErrorType)], hasErrorType, failed)
	}
}

func assertJobsRestartHandlerMetrics(t *testing.T, data metricdata.ResourceMetrics, spanContext trace.SpanContext, attempt int64) {
	t.Helper()
	assertFloatMetricExemplar(t, data, vvotel.MetricJobsHandlerDuration, spanContext)
	assertFloatMetricExemplar(t, data, vvotel.MetricJobsQueueDelay, spanContext)
	measurement := findMetricData(t, data, vvotel.MetricJobsHandlerAttempt)
	histogram, ok := measurement.Data.(metricdata.Histogram[int64])
	if !ok {
		t.Fatalf("metric %q data = %T", measurement.Name, measurement.Data)
	}
	for _, point := range histogram.DataPoints {
		if point.Count != 1 || point.Sum != attempt {
			continue
		}
		for _, exemplar := range point.Exemplars {
			if exemplar.Value == attempt && exemplarMatches(exemplar.TraceID, exemplar.SpanID, spanContext) {
				return
			}
		}
	}
	t.Fatalf("metric %q has no exact attempt %d exemplar for %s/%s", measurement.Name, attempt, spanContext.TraceID(), spanContext.SpanID())
}

func assertJobsRestartWorkerMetrics(t *testing.T, data metricdata.ResourceMetrics, spanContext trace.SpanContext) {
	t.Helper()
	for _, name := range []string{
		vvotel.MetricJobsWorkerOperations,
		vvotel.MetricJobsWorkerDuration,
		vvotel.MetricJobsWorkerItems,
		vvotel.MetricJobsWorkerBytes,
		vvotel.MetricJobsWorkerAdmission,
		vvotel.MetricJobsWorkerDeliveryResults,
		vvotel.MetricJobsWorkerDispositions,
		vvotel.MetricJobsWorkerReleased,
	} {
		assertJobsSDKMetricExemplar(t, data, name, spanContext)
	}
}

func assertJobsRestartDispositionMetric(t *testing.T, data metricdata.ResourceMetrics, spanContext trace.SpanContext, disposition string, reason string) {
	t.Helper()
	measurement := findMetricData(t, data, vvotel.MetricJobsWorkerDispositions)
	sum, ok := measurement.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("metric %q data = %T", measurement.Name, measurement.Data)
	}
	for _, point := range sum.DataPoints {
		gotDisposition, dispositionOK := stringAttribute(point.Attributes.ToSlice(), vvotel.AttrDisposition)
		gotReason, reasonOK := stringAttribute(point.Attributes.ToSlice(), vvotel.AttrReason)
		if !dispositionOK || !reasonOK || gotDisposition != disposition || gotReason != reason || point.Value != 1 {
			continue
		}
		for _, exemplar := range point.Exemplars {
			if exemplar.Value == 1 && exemplarMatches(exemplar.TraceID, exemplar.SpanID, spanContext) {
				return
			}
		}
	}
	t.Fatalf("worker disposition %s/%s has no exact exemplar for %s/%s", disposition, reason, spanContext.TraceID(), spanContext.SpanID())
}

func assertJobsRestartPropagation(t *testing.T, data metricdata.ResourceMetrics, operation string, outcome string, want int64) {
	t.Helper()
	measurement := findMetricData(t, data, vvotel.MetricJobsPropagation)
	sum, ok := measurement.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("metric %q data = %T", measurement.Name, measurement.Data)
	}
	var got int64
	for _, point := range sum.DataPoints {
		gotOperation, operationOK := stringAttribute(point.Attributes.ToSlice(), vvotel.AttrOperationName)
		gotOutcome, outcomeOK := stringAttribute(point.Attributes.ToSlice(), vvotel.AttrOperationOutcome)
		if operationOK && outcomeOK && gotOperation == operation && gotOutcome == outcome {
			got += point.Value
		}
	}
	if got != want {
		t.Fatalf("propagation %s/%s = %d, want %d", operation, outcome, got, want)
	}
}

func newJobsRestartSDKFixture(t *testing.T) *jobsRestartSDKFixture {
	t.Helper()
	spans := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.Empty()),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(spans),
	)
	metrics := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(resource.Empty()),
		sdkmetric.WithReader(metrics),
		sdkmetric.WithExemplarFilter(exemplar.AlwaysOnFilter),
	)
	fixture := &jobsRestartSDKFixture{sdkFixture: &sdkFixture{
		spans:          spans,
		metrics:        metrics,
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
	}}
	t.Cleanup(func() {
		if err := fixture.shutdown(); err != nil {
			t.Errorf("shutdown jobs restart SDK: %v", err)
		}
	})
	return fixture
}

func shutdownJobsRestartSDK(t *testing.T, fixture *jobsRestartSDKFixture) {
	t.Helper()
	if err := fixture.shutdown(); err != nil {
		t.Fatalf("shutdown jobs restart SDK: %v", err)
	}
}

func (fixture *jobsRestartSDKFixture) shutdown() error {
	fixture.shutdownOnce.Do(func() {
		var failures []error
		flushContext, cancelFlush := context.WithTimeout(context.Background(), time.Second)
		if err := fixture.tracerProvider.ForceFlush(flushContext); err != nil {
			failures = append(failures, fmt.Errorf("force flush trace provider: %w", err))
		}
		cancelFlush()
		traceContext, cancelTrace := context.WithTimeout(context.Background(), time.Second)
		if err := fixture.tracerProvider.Shutdown(traceContext); err != nil {
			failures = append(failures, fmt.Errorf("shutdown trace provider: %w", err))
		}
		cancelTrace()
		metricContext, cancelMetric := context.WithTimeout(context.Background(), time.Second)
		if err := fixture.meterProvider.Shutdown(metricContext); err != nil {
			failures = append(failures, fmt.Errorf("shutdown meter provider: %w", err))
		}
		cancelMetric()
		fixture.shutdownErr = errors.Join(failures...)
	})
	return fixture.shutdownErr
}

func jobsRestartBuild(t *testing.T, raw string) jobs.BuildID {
	t.Helper()
	build, err := jobs.ParseBuildID(raw)
	if err != nil {
		t.Fatal(err)
	}
	return build
}

func jobsRestartTraceID(t *testing.T, raw string) trace.TraceID {
	t.Helper()
	id, err := trace.TraceIDFromHex(raw)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func jobsRestartSpanID(t *testing.T, raw string) trace.SpanID {
	t.Helper()
	id, err := trace.SpanIDFromHex(raw)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func jobsRestartIdentityRequest(t *testing.T, carrier jobs.UntrustedTraceCarrier) jobs.IdentityRestoreRequest {
	t.Helper()
	namespace, err := jobs.NamespaceOf("jobs-carrier-control", "development")
	if err != nil {
		t.Fatal(err)
	}
	definition, err := jobs.ParseName("jobs.carrier.control")
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

func (attempt jobsRestartAttempt) String() string {
	return fmt.Sprintf("[jobs restart attempt ordinal=%d]", attempt.meta.AttemptOrdinal().Value())
}
