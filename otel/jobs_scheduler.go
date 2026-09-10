package vvotel

import (
	"context"

	"github.com/frostgrove/vv/jobs"
	"go.opentelemetry.io/otel/metric"
)

func Scheduler(t *Telemetry) jobs.ScheduleObserver {
	return &schedulerObserver{tel: t}
}

type schedulerObserver struct {
	tel *Telemetry
}

func (observer *schedulerObserver) Observe(ctx context.Context, event jobs.ScheduleEvent) {
	if observer == nil || observer.tel == nil || nilInterface(ctx) || !validScheduleEvent(event) {
		return
	}
	outcome, errorType := OutcomeOk, ""
	if event.Err() != nil {
		outcome, errorType = safeClassifyError(classifyJobsError, event.Err())
	}
	observer.recordCycle(ctx, outcome, errorType)
	observer.recordDuration(ctx, event, outcome, errorType)
	observer.recordResults(ctx, event.Result())
}

func (observer *schedulerObserver) recordCycle(ctx context.Context, outcome string, errorType string) {
	counter := observer.tel.int64Counter(SignalJobsSchedulerCycles)
	attributes, admitted := jobsSchedulerCycleAttributes(SignalJobsSchedulerCycles, outcome, errorType)
	if nilInterface(counter) || !admitted {
		return
	}
	safeAdd(counter, ctx, 1, metric.WithAttributes(attributes...))
}

func (observer *schedulerObserver) recordDuration(ctx context.Context, event jobs.ScheduleEvent, outcome string, errorType string) {
	histogram := observer.tel.float64Histogram(SignalJobsSchedulerDuration)
	attributes, admitted := jobsSchedulerCycleAttributes(SignalJobsSchedulerDuration, outcome, errorType)
	if nilInterface(histogram) || !admitted {
		return
	}
	safeRecord(histogram, ctx, event.Elapsed().Seconds(), metric.WithAttributes(attributes...))
}

func (observer *schedulerObserver) recordResults(ctx context.Context, result jobs.ScheduleRunResult) {
	histogram := observer.tel.int64Histogram(SignalJobsSchedulerResults)
	if nilInterface(histogram) {
		return
	}
	values := []struct {
		kind  string
		value int
	}{
		{kind: "due", value: result.Due},
		{kind: "placed", value: result.Placed},
		{kind: "existing", value: result.Existing},
		{kind: "conflicts", value: result.Conflicts},
	}
	for _, value := range values {
		attributes, admitted := jobsSchedulerResultAttributes(value.kind)
		if admitted {
			safeRecordInt64(histogram, ctx, int64(value.value), metric.WithAttributes(attributes...))
		}
	}
}

func validScheduleEvent(event jobs.ScheduleEvent) bool {
	result := event.Result()
	if event.Elapsed() < 0 || result.Due < 0 || result.Due > jobs.MaxDefinitions ||
		result.Placed < 0 || result.Placed > jobs.MaxDefinitions ||
		result.Existing < 0 || result.Existing > jobs.MaxDefinitions ||
		result.Conflicts < 0 || result.Conflicts > jobs.MaxDefinitions {
		return false
	}
	return result.Placed+result.Existing+result.Conflicts <= result.Due
}
