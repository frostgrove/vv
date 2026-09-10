package vvotel_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsmemory"
	"github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/attribute"
)

type jobsWorkerMetricContextKey struct{}

type jobsWorkerExercise struct {
	metrics      []*recordedMetric
	spans        []*recordedSpan
	runContext   context.Context
	drainContext context.Context
	runValue     string
	secrets      []string
}

func TestWorkersRecordsTheBoundedControlPlaneMetricFamilies(t *testing.T) {
	provider := newTestMeterProvider()
	exercise := exerciseJobsWorker(t, provider, false)

	wantMetrics := map[string]bool{
		vvotel.MetricJobsWorkerOperations:      false,
		vvotel.MetricJobsWorkerDuration:        false,
		vvotel.MetricJobsWorkerItems:           false,
		vvotel.MetricJobsWorkerBytes:           false,
		vvotel.MetricJobsWorkerAdmission:       false,
		vvotel.MetricJobsWorkerDeliveryResults: false,
		vvotel.MetricJobsWorkerDispositions:    false,
		vvotel.MetricJobsWorkerReleased:        false,
	}
	allowedAttributes := map[attribute.Key]bool{
		vvotel.AttrComponent:        true,
		vvotel.AttrOperationName:    true,
		vvotel.AttrOperationOutcome: true,
		vvotel.AttrFailure:          true,
		vvotel.AttrAdmissionSignal:  true,
		vvotel.AttrMutation:         true,
		vvotel.AttrControl:          true,
		vvotel.AttrCommandKind:      true,
		vvotel.AttrDisposition:      true,
		vvotel.AttrReason:           true,
		vvotel.AttrMore:             true,
	}
	finishDisposition := false
	for _, metric := range exercise.metrics {
		if _, ok := wantMetrics[metric.name]; !ok {
			t.Fatalf("unexpected worker metric %q", metric.name)
		}
		wantMetrics[metric.name] = true
		for key, value := range metric.attributes {
			if !allowedAttributes[key] {
				t.Fatalf("worker metric %q exported unowned attribute %q", metric.name, key)
			}
			rendered := fmt.Sprint(value.AsInterface())
			for _, secret := range exercise.secrets {
				if strings.Contains(rendered, secret) {
					t.Fatalf("worker metric %q exported secret %q in %q", metric.name, secret, rendered)
				}
			}
		}
		if metric.attributes[vvotel.AttrOperationName].AsString() == vvotel.OpJobsWorkerRun && metric.context != exercise.runContext {
			t.Fatal("run metric did not retain the exact Run context")
		}
		if metric.attributes[vvotel.AttrOperationName].AsString() == vvotel.OpJobsWorkerDrain && metric.context != exercise.drainContext {
			t.Fatal("drain metric did not retain the exact Drain context")
		}
		operation := metric.attributes[vvotel.AttrOperationName].AsString()
		if operation != vvotel.OpJobsWorkerDrain && metric.context.Value(jobsWorkerMetricContextKey{}) != exercise.runValue {
			t.Fatalf("%q metric lost its worker operation context value", operation)
		}
		if metric.name == vvotel.MetricJobsWorkerDispositions &&
			metric.attributes[vvotel.AttrCommandKind].AsString() == "finish_attempt" &&
			metric.attributes[vvotel.AttrDisposition].AsString() == "permanent_failure" &&
			metric.attributes[vvotel.AttrReason].AsString() == "handler_failure" {
			finishDisposition = true
		}
	}
	for name, found := range wantMetrics {
		if !found {
			t.Errorf("worker metric family %q was not recorded", name)
		}
	}
	if !finishDisposition {
		t.Fatal("finish_attempt metric did not use the nonzero disposition reason")
	}
	if len(exercise.spans) != 0 {
		t.Fatalf("worker polling created %d spans", len(exercise.spans))
	}
}

func TestWorkersMetricAndObserverPanicsCannotStopAWorker(t *testing.T) {
	provider := newTestMeterProvider()
	provider.panicCounterAdd = true
	provider.panicHistogramRecord = true
	exerciseJobsWorker(t, provider, true)
	if provider.counterAdds() == 0 {
		t.Fatal("panicking counter was never exercised")
	}
	provider.mu.Lock()
	histogramCalls := provider.histogramRecordCalls + provider.int64HistogramRecordCalls
	provider.mu.Unlock()
	if histogramCalls == 0 {
		t.Fatal("panicking histogram was never exercised")
	}
}

func exerciseJobsWorker(t *testing.T, meterProvider *testMeterProvider, panicObserver bool) jobsWorkerExercise {
	t.Helper()
	tracerProvider := newTestTracerProvider()
	tel := vvotel.Must(vvotel.Config{TracerProvider: tracerProvider, MeterProvider: meterProvider})
	if _, ok := vvotel.Workers(tel).(jobs.WorkerObserver); !ok {
		t.Fatal("Workers does not implement jobs.WorkerObserver")
	}

	definitionSecret := "otel.worker.private.definition"
	bindingSecret := "otel.worker.private.binding"
	payloadSecret := "otel-worker-private-payload"
	errorSecret := "otel-worker-private-handler-error"
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
		Codec:  jobs.String(1),
		Policy: policy,
	})
	catalog := jobs.MustCatalog(definition)
	namespace, err := jobs.NamespaceOf("otel-worker-private-app", "otel-worker-private-environment")
	if err != nil {
		t.Fatal(err)
	}
	backend, err := jobsmemory.NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close memory jobs backend: %v", err)
		}
	}()
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: backend})
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := jobs.Enqueue(context.Background(), queue, definition, payloadSecret)
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("otel-worker-private-build")
	if err != nil {
		t.Fatal(err)
	}
	admission, err := jobs.NewAdmissionSnapshot(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := admission.Publisher().Update(1, jobs.HeldReason{}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	handled := make(chan string, 1)
	consumer := jobs.On(
		definition,
		jobs.Handler[string](func(_ context.Context, payload string) error {
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
	panicChild := jobs.WorkerObserverFunc(func(context.Context, jobs.WorkerEvent) {
		if panicObserver {
			panic("otel-worker-private-observer-panic")
		}
	})
	workers, err := jobs.NewWorkers(jobs.WorkersSpec{
		Namespace:    namespace,
		Catalog:      catalog,
		Driver:       backend,
		Build:        build,
		Identity:     jobs.SystemIdentityRestorer(),
		Observer:     jobs.MustWorkerObservers(vvotel.Workers(tel), panicChild, terminalObserver),
		PollInterval: jobs.MinimumPollInterval,
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}

	runValue := "otel-worker-private-operation-context"
	runContext := context.WithValue(context.Background(), jobsWorkerMetricContextKey{}, runValue)
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(runContext) }()
	select {
	case payload := <-handled:
		if payload != payloadSecret {
			t.Fatalf("handler payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not invoke the handler")
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not apply the terminal disposition")
	}
	drainBase := context.WithValue(context.Background(), jobsWorkerMetricContextKey{}, "otel-worker-private-drain-context")
	drainContext, cancelDrain := context.WithTimeout(drainBase, 3*time.Second)
	defer cancelDrain()
	if err := workers.Drain(drainContext); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}

	meterProvider.mu.Lock()
	metrics := append([]*recordedMetric(nil), meterProvider.metrics...)
	meterProvider.mu.Unlock()
	tracerProvider.mu.Lock()
	spans := append([]*recordedSpan(nil), tracerProvider.spans...)
	tracerProvider.mu.Unlock()
	return jobsWorkerExercise{
		metrics:      metrics,
		spans:        spans,
		runContext:   runContext,
		drainContext: drainContext,
		runValue:     runValue,
		secrets: []string{
			definitionSecret,
			bindingSecret,
			payloadSecret,
			errorSecret,
			invocation.String(),
			"otel-worker-private-app",
			"otel-worker-private-environment",
			"otel-worker-private-build",
			"otel-worker-private-observer-panic",
		},
	}
}
