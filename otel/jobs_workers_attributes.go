package vvotel

import (
	"github.com/frostgrove/vv/jobs"
	"go.opentelemetry.io/otel/attribute"
)

type jobsWorkerOuter struct {
	operation  string
	outcome    string
	attributes []attribute.KeyValue
}

func jobsWorkerOuterAttributes(event jobsWorkerMetricEvent) (jobsWorkerOuter, bool) {
	operation, ok := JobsWorkerOperationName(event.operation.String())
	if !ok {
		return jobsWorkerOuter{}, false
	}
	outcome, ok := JobsWorkerOutcomeName(event.outcome.String())
	if !ok {
		return jobsWorkerOuter{}, false
	}
	attributes := []attribute.KeyValue{
		AttrComponent.String(ComponentJobsWorker),
		AttrOperationName.String(operation),
		AttrOperationOutcome.String(outcome),
	}
	if event.failure != jobs.WorkerFailureNone {
		failure, valid := JobsWorkerFailureName(event.failure.String())
		if !valid || failure == "none" {
			return jobsWorkerOuter{}, false
		}
		attributes = append(attributes, AttrFailure.String(failure))
	}
	attributes, ok = admitSignalAttributes(SignalJobsWorkerOperations, "", attributes)
	if !ok {
		return jobsWorkerOuter{}, false
	}
	return jobsWorkerOuter{operation: operation, outcome: outcome, attributes: attributes}, true
}

func jobsWorkerDurationAttributes(event jobsWorkerMetricEvent, outer jobsWorkerOuter) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(
		SignalJobsWorkerDuration,
		"",
		outer.attributes,
		SignalSourceValue{Fact: SourceFactJobsWorkerElapsed, Value: int64(event.elapsed)},
	)
}

func jobsWorkerItemsAttributes(outer jobsWorkerOuter) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(SignalJobsWorkerItems, "", outer.attributes)
}

func jobsWorkerBytesAttributes(outer jobsWorkerOuter) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(SignalJobsWorkerBytes, "", outer.attributes)
}

func jobsWorkerAdmissionAttributes(event jobsWorkerMetricEvent, outer jobsWorkerOuter) ([]attribute.KeyValue, bool) {
	signal, ok := JobsWorkerAdmissionSignalName(event.admissionSignal.String())
	if !ok {
		return nil, false
	}
	return admitSignalAttributes(SignalJobsWorkerAdmission, "", []attribute.KeyValue{
		AttrComponent.String(ComponentJobsWorker),
		AttrOperationName.String(outer.operation),
		AttrOperationOutcome.String(outer.outcome),
		AttrAdmissionSignal.String(signal),
	})
}

func jobsWorkerDeliveryResultAttributes(outer jobsWorkerOuter, result jobsWorkerMetricResult) ([]attribute.KeyValue, bool) {
	mutation, ok := JobsWorkerMutationName(result.mutation.String())
	if !ok {
		return nil, false
	}
	control, ok := JobsWorkerControlName(result.control.String())
	if !ok {
		return nil, false
	}
	return admitSignalAttributes(SignalJobsWorkerDeliveryResults, "", []attribute.KeyValue{
		AttrComponent.String(ComponentJobsWorker),
		AttrOperationName.String(outer.operation),
		AttrMutation.String(mutation),
		AttrControl.String(control),
	})
}

func jobsWorkerDispositionAttributes(event jobsWorkerMetricEvent, outer jobsWorkerOuter) ([]attribute.KeyValue, bool) {
	command, ok := JobsWorkerCommandKindName(event.commandKind.String())
	if !ok {
		return nil, false
	}
	reason, ok := JobsWorkerReasonName(event.reason.String())
	if !ok {
		return nil, false
	}
	attributes := []attribute.KeyValue{
		AttrComponent.String(ComponentJobsWorker),
		AttrOperationName.String(outer.operation),
		AttrCommandKind.String(command),
		AttrReason.String(reason),
	}
	if event.disposition != 0 {
		disposition, valid := JobsWorkerDispositionName(event.disposition.String())
		if !valid {
			return nil, false
		}
		attributes = append(attributes, AttrDisposition.String(disposition))
	}
	return admitSignalAttributes(SignalJobsWorkerDispositions, "", attributes)
}

func jobsWorkerReleasedAttributes(event jobsWorkerMetricEvent, outer jobsWorkerOuter) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(SignalJobsWorkerReleased, "", []attribute.KeyValue{
		AttrComponent.String(ComponentJobsWorker),
		AttrOperationName.String(outer.operation),
		AttrOperationOutcome.String(outer.outcome),
		AttrMore.Bool(event.more),
	})
}
