package vvotel

import "go.opentelemetry.io/otel/attribute"

func remoteSpanAttributes(operation string, outcome string, errorType string, _ string, _ []attribute.KeyValue) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(
		SignalRemoteSpan,
		spanStatus(outcome, errorType),
		operationAttributes(ComponentRemote, operation, outcome, errorType),
	)
}

func remoteMetricAttributes(operation string, outcome string, errorType string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(
		SignalRemoteDuration,
		"",
		operationAttributes(ComponentRemote, operation, outcome, errorType),
	)
}
