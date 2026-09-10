package vvotel

import "go.opentelemetry.io/otel/attribute"

func jobsEnqueueSpanAttributes(signal Signal) func(string, string, string, string, []attribute.KeyValue) ([]attribute.KeyValue, bool) {
	return func(operation string, outcome string, errorType string, _ string, _ []attribute.KeyValue) ([]attribute.KeyValue, bool) {
		return admitSignalAttributes(signal, spanStatus(outcome, errorType), operationAttributes(ComponentJobsEnqueue, operation, outcome, errorType))
	}
}

func jobsEnqueueMetricAttributes(operation string, outcome string, errorType string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(SignalJobsEnqueueDuration, "", operationAttributes(ComponentJobsEnqueue, operation, outcome, errorType))
}
