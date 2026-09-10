package oteltelemetry_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsmemory"
	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type jobsWorkerSDKContextKey struct{}

func TestRealSDKJobsWorkerPreservesExecutionAndCorrelatesMetricFamilies(t *testing.T) {
	fixture := newSDKFixture(t, sdktrace.AlwaysSample())
	tel := vvotel.Must(vvotel.Config{TracerProvider: fixture.tracerProvider, MeterProvider: fixture.meterProvider})
	definitionSecret := "jobs-worker-definition-secret-48251"
	bindingSecret := "jobs-worker-binding-secret-31592"
	payloadSecret := "jobs-worker-payload-secret-76138"
	errorSecret := "jobs-worker-handler-error-secret-92714"
	name, err := jobs.ParseName(definitionSecret)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	definition := jobs.MustDefine(jobs.DefinitionSpec[string]{
		Name:   name,
		Codec:  jobs.String(jobs.SchemaVersion(1)),
		Policy: policy,
	})
	catalog := jobs.MustCatalog(definition)
	namespace, err := jobs.NamespaceOf("jobs-worker-secret-app", "jobs-worker-secret-env")
	if err != nil {
		t.Fatal(err)
	}
	backend, err := jobsmemory.NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close jobs backend: %v", err)
		}
	})
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: backend})
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := jobs.Enqueue(context.Background(), queue, definition, payloadSecret)
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobs-worker-build-secret")
	if err != nil {
		t.Fatal(err)
	}
	admission, err := jobs.NewAdmissionSnapshot(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = admission.Publisher().Update(1, jobs.HeldReason{}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	marker := new(int)
	handled := make(chan string, 1)
	var handlerContext context.Context
	consumer := jobs.On(
		definition,
		jobs.Handler[string](func(ctx context.Context, payload string) error {
			handlerContext = ctx
			handled <- payload
			return jobs.Permanent(errors.New(errorSecret))
		}),
		jobs.Binding(bindingSecret),
		jobs.Concurrency(1),
		jobs.WithAdmission(admission.Reader()),
	)
	finished := make(chan struct{})
	var finishOnce sync.Once
	terminalObserver := jobs.WorkerObserverFunc(func(_ context.Context, event jobs.WorkerEvent) {
		if event.Operation() == jobs.WorkerOperationApply && event.Outcome() == jobs.WorkerOutcomeComplete && event.CommandKind() == jobs.DeliveryCommandFinishAttempt {
			finishOnce.Do(func() { close(finished) })
		}
	})
	workers, err := jobs.NewWorkers(jobs.WorkersSpec{
		Namespace:    namespace,
		Catalog:      catalog,
		Driver:       backend,
		Build:        build,
		Identity:     jobs.SystemIdentityRestorer(),
		Observer:     jobs.MustWorkerObservers(vvotel.Workers(tel), terminalObserver),
		PollInterval: jobs.MinimumPollInterval,
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}

	caller, parent := fixture.tracerProvider.Tracer("application").Start(context.Background(), "worker runtime", trace.WithSpanKind(trace.SpanKindInternal))
	runContext := context.WithValue(caller, jobsWorkerSDKContextKey{}, marker)
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(runContext) }()
	select {
	case payload := <-handled:
		if payload != payloadSecret {
			t.Fatalf("handler payload = %q", payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not invoke handler")
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not apply terminal disposition")
	}
	drainContext, cancelDrain := context.WithTimeout(runContext, 5*time.Second)
	defer cancelDrain()
	if err = workers.Drain(drainContext); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-runResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
	parent.End()
	if handlerContext == nil || handlerContext.Value(jobsWorkerSDKContextKey{}) != marker {
		t.Fatal("worker handler lost the run context value")
	}

	spans := fixture.spans.Ended()
	if len(spans) != 1 {
		t.Fatalf("worker observer created spans: %d", len(spans)-1)
	}
	parentSpan := findSpan(t, spans, "worker runtime")
	metrics := collectMetricData(t, fixture.metrics)
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
		assertJobsSDKMetricExemplar(t, metrics, name, parentSpan.SpanContext())
	}
	assertSDKPrivacy(t, spans, metrics, []string{
		definitionSecret,
		bindingSecret,
		payloadSecret,
		errorSecret,
		invocation.String(),
		"jobs-worker-secret-app",
		"jobs-worker-secret-env",
		"jobs-worker-build-secret",
	})
}

func assertJobsSDKMetricExemplar(t *testing.T, data metricdata.ResourceMetrics, name string, spanContext trace.SpanContext) {
	t.Helper()
	metric := findMetricData(t, data, name)
	wantTraceID := spanContext.TraceID()
	wantSpanID := spanContext.SpanID()
	matches := func(traceID []byte, spanID []byte) bool {
		return bytes.Equal(traceID, wantTraceID[:]) && bytes.Equal(spanID, wantSpanID[:])
	}
	switch aggregation := metric.Data.(type) {
	case metricdata.Sum[int64]:
		for _, point := range aggregation.DataPoints {
			for _, exemplar := range point.Exemplars {
				if matches(exemplar.TraceID, exemplar.SpanID) {
					return
				}
			}
		}
	case metricdata.Histogram[int64]:
		for _, point := range aggregation.DataPoints {
			for _, exemplar := range point.Exemplars {
				if matches(exemplar.TraceID, exemplar.SpanID) {
					return
				}
			}
		}
	case metricdata.Histogram[float64]:
		for _, point := range aggregation.DataPoints {
			for _, exemplar := range point.Exemplars {
				if matches(exemplar.TraceID, exemplar.SpanID) {
					return
				}
			}
		}
	default:
		t.Fatalf("metric %q data = %T", name, metric.Data)
	}
	t.Fatalf("metric %q has no exemplar for %s/%s", name, wantTraceID, wantSpanID)
}
