package vvotel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/otel"
	vvruntime "github.com/frostgrove/vv/runtime"
)

type telemetryRuntimeRunner struct {
	name         string
	declaration  vvruntime.Declaration
	runContext   chan context.Context
	drainContext chan context.Context
	runError     error
	drainError   error
}

func (r *telemetryRuntimeRunner) Name() string { return r.name }

func (r *telemetryRuntimeRunner) Run(ctx context.Context) error {
	r.runContext <- ctx
	if r.runError != nil {
		return r.runError
	}
	<-ctx.Done()
	return ctx.Err()
}

func (r *telemetryRuntimeRunner) Drain(ctx context.Context) error {
	r.drainContext <- ctx
	return r.drainError
}

func (r *telemetryRuntimeRunner) Declaration() vvruntime.Declaration { return r.declaration }

func TestRuntimeObserverEmitsNormalTransitionsRunAndDrain(t *testing.T) {
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	observer := vvotel.Runtime(tel)
	if _, ok := observer.(vvruntime.LifecycleObserver); !ok {
		t.Fatal("vvotel.Runtime does not preserve LifecycleObserver capability")
	}
	declaration := vvruntime.Declaration{Placement: vvruntime.Singleton, Durability: vvruntime.Durable}
	runner := &telemetryRuntimeRunner{
		name:         "secret-runner-name",
		declaration:  declaration,
		runContext:   make(chan context.Context, 1),
		drainContext: make(chan context.Context, 1),
	}
	supervisor, err := vvruntime.NewSupervisor(vvruntime.Spec{Runners: []vvruntime.Runner{runner}, Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	runContext := <-runner.runContext
	if err := supervisor.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	drainContext := <-runner.drainContext

	transitions := runtimeMetricsNamed(mp, vvotel.MetricRuntimeTransitions)
	operations := runtimeMetricsNamed(mp, vvotel.MetricRuntimeOperations)
	durations := runtimeMetricsNamed(mp, vvotel.MetricRuntimeDuration)
	if len(transitions) != 2 || len(operations) != 2 || len(durations) != 2 {
		t.Fatalf("transitions=%d operations=%d durations=%d", len(transitions), len(operations), len(durations))
	}
	if transitions[0].attributes[vvotel.AttrPhase].AsString() != "running" || transitions[1].attributes[vvotel.AttrPhase].AsString() != "stopped" {
		t.Fatalf("transition phases=%v / %v", transitions[0].attributes, transitions[1].attributes)
	}
	for _, transition := range transitions {
		assertRuntimeDeclarationAttributes(t, transition, "", "")
		if len(transition.attributes) != 4 || transition.value != int64(1) {
			t.Fatalf("transition=%+v", transition)
		}
	}
	for _, operation := range []struct {
		name string
		ctx  context.Context
	}{
		{name: vvotel.OpRuntimeLifecycleRun, ctx: runContext},
		{name: vvotel.OpRuntimeLifecycleDrain, ctx: drainContext},
	} {
		counter := runtimeMetricForOperation(t, operations, operation.name)
		duration := runtimeMetricForOperation(t, durations, operation.name)
		if counter.context != operation.ctx || duration.context != operation.ctx {
			t.Fatalf("%s lifecycle metric lost its exact context", operation.name)
		}
		if counter.value != int64(1) {
			t.Fatalf("%s operation counter=%v", operation.name, counter.value)
		}
		if value, ok := duration.value.(float64); !ok || value < 0 {
			t.Fatalf("%s duration=%v", operation.name, duration.value)
		}
		assertRuntimeDeclarationAttributes(t, counter, operation.name, vvotel.OutcomeOk)
		assertRuntimeDeclarationAttributes(t, duration, operation.name, vvotel.OutcomeOk)
	}
	assertRuntimeMetricsExcludePrivateData(t, append(append(transitions, operations...), durations...), runner.name)
}

func TestRuntimeObserverClassifiesEarlyErrorWithoutExportingIt(t *testing.T) {
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("secret database credentials failed")
	runner := &telemetryRuntimeRunner{
		name:         "secret-runner-name",
		runContext:   make(chan context.Context, 1),
		drainContext: make(chan context.Context, 1),
		runError:     wantErr,
	}
	supervisor, err := vvruntime.NewSupervisor(vvruntime.Spec{Runners: []vvruntime.Runner{runner}, Observer: vvotel.Runtime(tel)})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-runner.runContext
	awaitRuntimeMetricCount(t, mp, 4)

	operations := runtimeMetricsNamed(mp, vvotel.MetricRuntimeOperations)
	durations := runtimeMetricsNamed(mp, vvotel.MetricRuntimeDuration)
	if len(operations) != 1 || len(durations) != 1 {
		t.Fatalf("operations=%d durations=%d", len(operations), len(durations))
	}
	for _, measurement := range []*recordedMetric{operations[0], durations[0]} {
		if measurement.attributes[vvotel.AttrOperationOutcome].AsString() != vvotel.OutcomeError || measurement.attributes[vvotel.AttrErrorType].AsString() != vvotel.ErrorTypeInternal {
			t.Fatalf("failure metric=%+v", measurement)
		}
	}
	assertRuntimeMetricsExcludePrivateData(t, runtimeMetricsSnapshot(mp), runner.name, wantErr.Error())
	if err := supervisor.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func runtimeMetricsNamed(provider *testMeterProvider, name string) []*recordedMetric {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	var records []*recordedMetric
	for _, record := range provider.metrics {
		if record.name == name {
			records = append(records, record)
		}
	}
	return records
}

func runtimeMetricsSnapshot(provider *testMeterProvider) []*recordedMetric {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]*recordedMetric(nil), provider.metrics...)
}

func runtimeMetricForOperation(t *testing.T, records []*recordedMetric, operation string) *recordedMetric {
	t.Helper()
	for _, record := range records {
		if record.attributes[vvotel.AttrOperationName].AsString() == operation {
			return record
		}
	}
	t.Fatalf("no metric for operation %q in %+v", operation, records)
	return nil
}

func assertRuntimeDeclarationAttributes(t *testing.T, metric *recordedMetric, operation string, outcome string) {
	t.Helper()
	if metric.attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentRuntimeLifecycle || metric.attributes[vvotel.AttrPlacement].AsString() != "singleton" || metric.attributes[vvotel.AttrDurability].AsString() != "durable" {
		t.Fatalf("runtime declaration attributes=%v", metric.attributes)
	}
	if operation != "" && (metric.attributes[vvotel.AttrOperationName].AsString() != operation || metric.attributes[vvotel.AttrOperationOutcome].AsString() != outcome || len(metric.attributes) != 5) {
		t.Fatalf("runtime lifecycle attributes=%v", metric.attributes)
	}
}

func assertRuntimeMetricsExcludePrivateData(t *testing.T, metrics []*recordedMetric, private ...string) {
	t.Helper()
	for _, metric := range metrics {
		for key, value := range metric.attributes {
			for _, secret := range private {
				if string(key) == secret || value.AsString() == secret {
					t.Fatalf("private value %q exported in %s: %v", secret, metric.name, metric.attributes)
				}
			}
		}
	}
}

func awaitRuntimeMetricCount(t *testing.T, provider *testMeterProvider, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if provider.metricCount() >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("metrics=%d, want at least %d", provider.metricCount(), count)
		}
		time.Sleep(time.Millisecond)
	}
}
