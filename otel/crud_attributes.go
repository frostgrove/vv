package vvotel

import "go.opentelemetry.io/otel/attribute"

func crudSourceSpanAttributes(operation string, outcome string, errorType string, _ string, extra []attribute.KeyValue) ([]attribute.KeyValue, bool) {
	attributes := operationAttributes(ComponentCrudSource, operation, outcome, errorType)
	attributes = append(attributes, extra...)
	return admitSignalAttributes(SignalCrudSourceSpan, spanStatus(outcome, errorType), attributes)
}

func crudSourceMetricAttributes(operation string, outcome string, errorType string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(
		SignalCrudSourceDuration,
		"",
		operationAttributes(ComponentCrudSource, operation, outcome, errorType),
	)
}
