package vvotel

import (
	"context"
	"time"

	"github.com/frostgrove/vv/jobs"
	"go.opentelemetry.io/otel/metric"
)

func Workers(t *Telemetry) jobs.WorkerObserver {
	return &workersObserver{tel: t}
}

type workersObserver struct {
	tel *Telemetry
}

var _ jobs.WorkerObserver = (*workersObserver)(nil)

type jobsWorkerMetricEvent struct {
	operation       jobs.WorkerOperation
	outcome         jobs.WorkerOutcome
	failure         jobs.WorkerFailure
	commandKind     jobs.DeliveryCommandKind
	disposition     jobs.DispositionKind
	reason          jobs.Reason
	admissionSignal jobs.AdmissionSignal
	results         []jobsWorkerMetricResult
	items           int
	released        int
	bytes           int
	more            bool
	elapsed         time.Duration
}

type jobsWorkerMetricResult struct {
	mutation jobs.DeliveryMutationStatus
	control  jobs.DeliveryControlStatus
	items    int
}

func (observer *workersObserver) Observe(ctx context.Context, event jobs.WorkerEvent) {
	if observer == nil || observer.tel == nil || nilInterface(ctx) {
		return
	}
	results := event.Results()
	metricResults := make([]jobsWorkerMetricResult, len(results))
	for index, result := range results {
		metricResults[index] = jobsWorkerMetricResult{
			mutation: result.Mutation(),
			control:  result.Control(),
			items:    result.Items(),
		}
	}
	observer.observe(ctx, jobsWorkerMetricEvent{
		operation:       event.Operation(),
		outcome:         event.Outcome(),
		failure:         event.Failure(),
		commandKind:     event.CommandKind(),
		disposition:     event.Disposition(),
		reason:          event.Reason(),
		admissionSignal: event.AdmissionSignal(),
		results:         metricResults,
		items:           event.Items(),
		released:        event.Released(),
		bytes:           event.Bytes(),
		more:            event.More(),
		elapsed:         event.Elapsed(),
	})
}

func (observer *workersObserver) observe(ctx context.Context, event jobsWorkerMetricEvent) {
	if observer == nil || observer.tel == nil || nilInterface(ctx) {
		return
	}
	outer, admitted := jobsWorkerOuterAttributes(event)
	if !admitted {
		return
	}

	if counter := observer.tel.int64Counter(SignalJobsWorkerOperations); !nilInterface(counter) {
		safeAdd(counter, ctx, 1, metric.WithAttributes(outer.attributes...))
	}
	if event.elapsed > 0 {
		if attributes, ok := jobsWorkerDurationAttributes(event, outer); ok {
			if histogram := observer.tel.float64Histogram(SignalJobsWorkerDuration); !nilInterface(histogram) {
				safeRecord(histogram, ctx, event.elapsed.Seconds(), metric.WithAttributes(attributes...))
			}
		}
	}
	if jobsWorkerMetricValue(event.items, jobs.MaxReclaimBatch) {
		if attributes, ok := jobsWorkerItemsAttributes(outer); ok {
			if histogram := observer.tel.int64Histogram(SignalJobsWorkerItems); !nilInterface(histogram) {
				safeRecordInt64(histogram, ctx, int64(event.items), metric.WithAttributes(attributes...))
			}
		}
	}
	if jobsWorkerMetricValue(event.bytes, jobs.MaxClaimBytes) {
		if attributes, ok := jobsWorkerBytesAttributes(outer); ok {
			if histogram := observer.tel.int64Histogram(SignalJobsWorkerBytes); !nilInterface(histogram) {
				safeRecordInt64(histogram, ctx, int64(event.bytes), metric.WithAttributes(attributes...))
			}
		}
	}
	if attributes, ok := jobsWorkerAdmissionAttributes(event, outer); ok {
		if counter := observer.tel.int64Counter(SignalJobsWorkerAdmission); !nilInterface(counter) {
			safeAdd(counter, ctx, 1, metric.WithAttributes(attributes...))
		}
	}
	if event.outcome == jobs.WorkerOutcomeComplete {
		for _, result := range event.results {
			if !jobsWorkerPositiveMetricValue(result.items, jobs.MaxClaimItems) {
				continue
			}
			if attributes, ok := jobsWorkerDeliveryResultAttributes(outer, result); ok {
				if counter := observer.tel.int64Counter(SignalJobsWorkerDeliveryResults); !nilInterface(counter) {
					safeAdd(counter, ctx, int64(result.items), metric.WithAttributes(attributes...))
				}
			}
		}
	}
	if attributes, ok := jobsWorkerDispositionAttributes(event, outer); ok {
		if counter := observer.tel.int64Counter(SignalJobsWorkerDispositions); !nilInterface(counter) {
			safeAdd(counter, ctx, 1, metric.WithAttributes(attributes...))
		}
	}
	if jobsWorkerMetricValue(event.released, jobs.MaxReclaimBatch) {
		if attributes, ok := jobsWorkerReleasedAttributes(event, outer); ok {
			if histogram := observer.tel.int64Histogram(SignalJobsWorkerReleased); !nilInterface(histogram) {
				safeRecordInt64(histogram, ctx, int64(event.released), metric.WithAttributes(attributes...))
			}
		}
	}
}

func jobsWorkerMetricValue(value, maximum int) bool {
	return value >= 0 && value <= maximum
}

func jobsWorkerPositiveMetricValue(value, maximum int) bool {
	return value > 0 && value <= maximum
}
