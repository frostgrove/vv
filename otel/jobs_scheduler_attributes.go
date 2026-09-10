package vvotel

import "go.opentelemetry.io/otel/attribute"

func jobsSchedulerCycleAttributes(signal Signal, outcome string, errorType string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(
		signal,
		"",
		operationAttributes(ComponentJobsScheduler, OpJobsSchedulerRunDue, outcome, errorType),
	)
}

func jobsSchedulerResultAttributes(result string) ([]attribute.KeyValue, bool) {
	result, ok := JobsSchedulerResultName(result)
	if !ok {
		return nil, false
	}
	return admitSignalAttributes(SignalJobsSchedulerResults, "", []attribute.KeyValue{
		AttrComponent.String(ComponentJobsScheduler),
		AttrOperationName.String(OpJobsSchedulerRunDue),
		AttrResultKind.String(result),
	})
}
