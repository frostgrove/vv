package vvotel

import "go.opentelemetry.io/otel/attribute"

func jobsHandlerSpanAttributes(operation string, outcome string, errorType string, _ string, _ []attribute.KeyValue) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(SignalJobsHandlerSpan, spanStatus(outcome, errorType), operationAttributes(ComponentJobsHandler, operation, outcome, errorType))
}

func jobsHandlerMetricAttributes(operation string, outcome string, errorType string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(SignalJobsHandlerDuration, "", operationAttributes(ComponentJobsHandler, operation, outcome, errorType))
}

func jobsHandlerMeasurementAttributes() ([]attribute.KeyValue, bool) {
	attributes := []attribute.KeyValue{
		AttrComponent.String(ComponentJobsHandler),
		AttrOperationName.String(OpJobsHandlerHandle),
	}
	if admitted, ok := admitSignalAttributes(SignalJobsQueueDelay, "", attributes); ok {
		return admitted, true
	}
	return admitSignalAttributes(SignalJobsHandlerAttempt, "", attributes)
}
