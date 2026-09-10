package vvotel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/attribute"
)

type jobsSchedulerContextKey struct{}

type jobsSchedulerClock struct {
	now time.Time
}

func (clock jobsSchedulerClock) Now() time.Time { return clock.now }
func (jobsSchedulerClock) NewTimerAt(time.Time) jobs.Timer {
	panic("unexpected timer")
}

func TestJobsSchedulerObserverRecordsOneBoundedCycleAndFourResults(t *testing.T) {
	mp := newTestMeterProvider()
	tel := vvotel.Must(vvotel.Config{MeterProvider: mp})
	now := time.Date(2037, 3, 4, 5, 6, 7, 0, time.UTC)
	scheduler, scheduleName, definitionName := newJobsSchedulerFixture(t, vvotel.Scheduler(tel), now)
	ctx := context.WithValue(context.Background(), jobsSchedulerContextKey{}, "exact-context")

	result, err := scheduler.RunDue(ctx)
	if err != nil || result != (jobs.ScheduleRunResult{Due: 1, Placed: 1}) {
		t.Fatalf("RunDue = (%#v, %v)", result, err)
	}
	cycles := schedulerMetricsNamed(mp, vvotel.MetricJobsSchedulerCycles)
	durations := schedulerMetricsNamed(mp, vvotel.MetricJobsSchedulerDuration)
	results := schedulerMetricsNamed(mp, vvotel.MetricJobsSchedulerResults)
	if len(cycles) != 1 || len(durations) != 1 || len(results) != 4 || mp.metricCount() != 6 {
		t.Fatalf("cycles/durations/results/total = %d/%d/%d/%d", len(cycles), len(durations), len(results), mp.metricCount())
	}
	if cycles[0].value != int64(1) {
		t.Fatalf("cycle count = %v", cycles[0].value)
	}
	if duration, ok := durations[0].value.(float64); !ok || duration < 0 {
		t.Fatalf("cycle duration = %v", durations[0].value)
	}
	assertSchedulerTerminalAttributes(t, cycles[0], vvotel.OutcomeOk, "")
	assertSchedulerTerminalAttributes(t, durations[0], vvotel.OutcomeOk, "")
	wantResults := map[string]int64{"due": 1, "placed": 1, "existing": 0, "conflicts": 0}
	for _, measurement := range results {
		kind := measurement.attributes[vvotel.AttrResultKind].AsString()
		want, ok := wantResults[kind]
		if !ok || measurement.value != want {
			t.Fatalf("result measurement = %+v", measurement)
		}
		delete(wantResults, kind)
		assertSchedulerResultAttributes(t, measurement)
	}
	if len(wantResults) != 0 {
		t.Fatalf("missing result measurements = %v", wantResults)
	}
	for _, measurement := range schedulerMetricsSnapshot(mp) {
		if measurement.context != ctx {
			t.Fatal("scheduler metric lost the exact RunDue context")
		}
		assertSchedulerMetricPrivacy(t, measurement, scheduleName, definitionName, now.Format(time.RFC3339Nano))
	}
}

func TestJobsSchedulerObserverClassifiesAnAdmittedCanceledCycle(t *testing.T) {
	mp := newTestMeterProvider()
	tel := vvotel.Must(vvotel.Config{MeterProvider: mp})
	now := time.Date(2037, 3, 4, 5, 6, 7, 0, time.UTC)
	scheduler, _, _ := newJobsSchedulerFixture(t, vvotel.Scheduler(tel), now)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := scheduler.RunDue(ctx)
	if result != (jobs.ScheduleRunResult{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("RunDue = (%#v, %v)", result, err)
	}
	cycles := schedulerMetricsNamed(mp, vvotel.MetricJobsSchedulerCycles)
	durations := schedulerMetricsNamed(mp, vvotel.MetricJobsSchedulerDuration)
	results := schedulerMetricsNamed(mp, vvotel.MetricJobsSchedulerResults)
	if len(cycles) != 1 || len(durations) != 1 || len(results) != 4 {
		t.Fatalf("canceled cycles/durations/results = %d/%d/%d", len(cycles), len(durations), len(results))
	}
	assertSchedulerTerminalAttributes(t, cycles[0], vvotel.OutcomeCanceled, vvotel.ErrorTypeCanceled)
	assertSchedulerTerminalAttributes(t, durations[0], vvotel.OutcomeCanceled, vvotel.ErrorTypeCanceled)
	for _, measurement := range results {
		if measurement.value != int64(0) || measurement.context != ctx {
			t.Fatalf("canceled result measurement = %+v", measurement)
		}
		assertSchedulerResultAttributes(t, measurement)
	}
}

func TestJobsSchedulerObserverRecordsExistingConflictAndPartialFailure(t *testing.T) {
	tests := []struct {
		name        string
		count       int
		outcome     jobs.PlacementOutcome
		failAt      int
		failure     error
		wantResult  jobs.ScheduleRunResult
		wantOutcome string
		wantError   string
	}{
		{name: "existing", count: 1, outcome: jobs.PlacementExistingSamePayload, wantResult: jobs.ScheduleRunResult{Due: 1, Existing: 1}, wantOutcome: vvotel.OutcomeOk},
		{name: "conflict", count: 1, outcome: jobs.PlacementConflict, wantResult: jobs.ScheduleRunResult{Due: 1, Conflicts: 1}, wantOutcome: vvotel.OutcomeOk},
		{name: "partial ordinary failure", count: 2, outcome: jobs.PlacementCreated, failAt: 2, failure: jobs.RejectPlacement(jobs.ErrSaturated), wantResult: jobs.ScheduleRunResult{Due: 2, Placed: 1}, wantOutcome: vvotel.OutcomeError, wantError: vvotel.ErrorTypeConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mp := newTestMeterProvider()
			tel := vvotel.Must(vvotel.Config{MeterProvider: mp})
			now := time.Date(2037, 3, 4, 5, 6, 7, 0, time.UTC)
			scheduler, _, _, sender := newJobsSchedulerFixtureCount(t, vvotel.Scheduler(tel), now, test.count)
			sender.outcome = test.outcome
			sender.failAt = test.failAt
			sender.failure = test.failure

			result, err := scheduler.RunDue(context.Background())
			if result != test.wantResult || (test.failure == nil && err != nil) || (test.failure != nil && !errors.Is(err, jobs.ErrSaturated)) {
				t.Fatalf("RunDue = (%#v, %v), want %#v", result, err, test.wantResult)
			}
			cycles := schedulerMetricsNamed(mp, vvotel.MetricJobsSchedulerCycles)
			durations := schedulerMetricsNamed(mp, vvotel.MetricJobsSchedulerDuration)
			results := schedulerMetricsNamed(mp, vvotel.MetricJobsSchedulerResults)
			if len(cycles) != 1 || len(durations) != 1 || len(results) != 4 {
				t.Fatalf("cycle/duration/results = %d/%d/%d", len(cycles), len(durations), len(results))
			}
			assertSchedulerTerminalAttributes(t, cycles[0], test.wantOutcome, test.wantError)
			assertSchedulerTerminalAttributes(t, durations[0], test.wantOutcome, test.wantError)
			wantResults := map[string]int64{
				"due": int64(result.Due), "placed": int64(result.Placed),
				"existing": int64(result.Existing), "conflicts": int64(result.Conflicts),
			}
			for _, measurement := range results {
				if measurement.value != wantResults[measurement.attributes[vvotel.AttrResultKind].AsString()] {
					t.Fatalf("result measurement = %+v, result = %#v", measurement, result)
				}
			}
		})
	}
}

func TestJobsSchedulerObserverReceivesTheRunLoopCycle(t *testing.T) {
	mp := newTestMeterProvider()
	tel := vvotel.Must(vvotel.Config{MeterProvider: mp})
	now := time.Date(2037, 3, 4, 5, 6, 7, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	observer := jobs.MustScheduleObservers(vvotel.Scheduler(tel), jobs.ScheduleObserverFunc(func(context.Context, jobs.ScheduleEvent) { cancel() }))
	scheduler, _, _ := newJobsSchedulerFixture(t, observer, now)

	if err := scheduler.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v", err)
	}
	if cycles := schedulerMetricsNamed(mp, vvotel.MetricJobsSchedulerCycles); len(cycles) != 1 || cycles[0].attributes[vvotel.AttrOperationOutcome].AsString() != vvotel.OutcomeOk {
		t.Fatalf("run-loop cycles = %+v", cycles)
	}
}

func TestJobsSchedulerObserverInstrumentPanicsAreFailOpen(t *testing.T) {
	tests := []struct {
		name            string
		configure       func(*testMeterProvider)
		wantMetrics     int
		wantCounterCall int
		wantFloatCalls  int
		wantIntCalls    int
	}{
		{
			name: "cycle counter",
			configure: func(provider *testMeterProvider) {
				provider.panicCounterAdd = true
			},
			wantMetrics:     5,
			wantCounterCall: 1,
			wantFloatCalls:  1,
			wantIntCalls:    4,
		},
		{
			name: "duration and result histograms",
			configure: func(provider *testMeterProvider) {
				provider.panicHistogramRecord = true
			},
			wantMetrics:     1,
			wantCounterCall: 1,
			wantFloatCalls:  1,
			wantIntCalls:    4,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mp := newTestMeterProvider()
			tel := vvotel.Must(vvotel.Config{MeterProvider: mp})
			test.configure(mp)
			now := time.Date(2037, 3, 4, 5, 6, 7, 0, time.UTC)
			scheduler, _, _ := newJobsSchedulerFixture(t, vvotel.Scheduler(tel), now)
			result, err := scheduler.RunDue(context.Background())
			if err != nil || result != (jobs.ScheduleRunResult{Due: 1, Placed: 1}) {
				t.Fatalf("telemetry panic changed RunDue = (%#v, %v)", result, err)
			}
			if mp.metricCount() != test.wantMetrics || mp.counterAddCalls != test.wantCounterCall ||
				mp.histogramRecordCalls != test.wantFloatCalls || mp.int64HistogramRecordCalls != test.wantIntCalls {
				t.Fatalf("metrics/counter/float/int calls = %d/%d/%d/%d", mp.metricCount(), mp.counterAddCalls, mp.histogramRecordCalls, mp.int64HistogramRecordCalls)
			}
		})
	}
}

func TestJobsSchedulerObserverWithoutTelemetryIsInert(t *testing.T) {
	now := time.Date(2037, 3, 4, 5, 6, 7, 0, time.UTC)
	scheduler, _, _ := newJobsSchedulerFixture(t, vvotel.Scheduler(nil), now)
	result, err := scheduler.RunDue(context.Background())
	if err != nil || result != (jobs.ScheduleRunResult{Due: 1, Placed: 1}) {
		t.Fatalf("inert observer changed RunDue = (%#v, %v)", result, err)
	}
}

func newJobsSchedulerFixture(t *testing.T, observer jobs.ScheduleObserver, now time.Time) (*jobs.Scheduler, string, string) {
	scheduler, scheduleName, definitionName, _ := newJobsSchedulerFixtureCount(t, observer, now, 1)
	return scheduler, scheduleName, definitionName
}

func newJobsSchedulerFixtureCount(t *testing.T, observer jobs.ScheduleObserver, now time.Time, count int) (*jobs.Scheduler, string, string, *jobsEnqueueSender) {
	t.Helper()
	queue, definition, sender, _ := jobsEnqueueTestFixture(t)
	scheduleName := "otel.secret.scheduler.definition"
	schedules := make([]jobs.Schedule, count)
	for index := range schedules {
		name, err := jobs.ParseName(scheduleName + "." + string(rune('a'+index)))
		if err != nil {
			t.Fatal(err)
		}
		schedule, err := jobs.DefineSchedule(jobs.ScheduleSpec[string]{
			Name:     name,
			Revision: 1,
			Cadence:  jobs.At(now),
			Job:      definition,
			Payload:  func(time.Time) (string, error) { return "secret-scheduled-payload", nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		schedules[index] = schedule
	}
	scheduler, err := jobs.NewScheduler(jobs.SchedulerSpec{
		Queue:    queue,
		Clock:    jobsSchedulerClock{now: now},
		Observer: observer,
	}, schedules...)
	if err != nil {
		t.Fatal(err)
	}
	return scheduler, scheduleName, definition.Name().Value(), sender
}

func schedulerMetricsNamed(provider *testMeterProvider, name string) []*recordedMetric {
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

func schedulerMetricsSnapshot(provider *testMeterProvider) []*recordedMetric {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]*recordedMetric(nil), provider.metrics...)
}

func assertSchedulerTerminalAttributes(t *testing.T, measurement *recordedMetric, outcome string, errorType string) {
	t.Helper()
	attributes := measurement.attributes
	if attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentJobsScheduler ||
		attributes[vvotel.AttrOperationName].AsString() != vvotel.OpJobsSchedulerRunDue ||
		attributes[vvotel.AttrOperationOutcome].AsString() != outcome {
		t.Fatalf("scheduler terminal attributes = %v", attributes)
	}
	wantLength := 3
	if errorType != "" {
		wantLength = 4
		if attributes[vvotel.AttrErrorType].AsString() != errorType {
			t.Fatalf("scheduler error type = %v", attributes)
		}
	} else if _, ok := attributes[vvotel.AttrErrorType]; ok {
		t.Fatalf("successful scheduler metric has error.type: %v", attributes)
	}
	if len(attributes) != wantLength {
		t.Fatalf("scheduler terminal attribute count = %d: %v", len(attributes), attributes)
	}
}

func assertSchedulerResultAttributes(t *testing.T, measurement *recordedMetric) {
	t.Helper()
	attributes := measurement.attributes
	if attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentJobsScheduler ||
		attributes[vvotel.AttrOperationName].AsString() != vvotel.OpJobsSchedulerRunDue ||
		attributes[vvotel.AttrResultKind].AsString() == "" || len(attributes) != 3 {
		t.Fatalf("scheduler result attributes = %v", attributes)
	}
	if _, ok := attributes[vvotel.AttrOperationOutcome]; ok {
		t.Fatalf("scheduler result exported an outcome: %v", attributes)
	}
	if _, ok := attributes[vvotel.AttrErrorType]; ok {
		t.Fatalf("scheduler result exported an error type: %v", attributes)
	}
}

func assertSchedulerMetricPrivacy(t *testing.T, measurement *recordedMetric, forbidden ...string) {
	t.Helper()
	allowed := map[attribute.Key]bool{
		vvotel.AttrComponent:        true,
		vvotel.AttrOperationName:    true,
		vvotel.AttrOperationOutcome: true,
		vvotel.AttrErrorType:        true,
		vvotel.AttrResultKind:       true,
	}
	for key, value := range measurement.attributes {
		if !allowed[key] {
			t.Fatalf("scheduler metric exported undeclared attribute %q", key)
		}
		for _, secret := range forbidden {
			if string(key) == secret || value.AsString() == secret {
				t.Fatalf("scheduler metric exported %q: %v", secret, measurement.attributes)
			}
		}
	}
}
