package vvotel

import "go.opentelemetry.io/otel/attribute"

const authenticationOutcomeRefused = "refused"

func authRefusalAttributes(signal Signal, reason string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(signal, "", []attribute.KeyValue{
		AttrComponent.String(ComponentAuthRefusal),
		AttrOperationName.String(OpAuthRefusalRefuse),
		AttrOperationOutcome.String(authenticationOutcomeRefused),
		AttrReason.String(reason),
	})
}

func authenticationSpanAttributes(operation string, outcome string, errorType string, _ string, _ []attribute.KeyValue) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(
		SignalAuthenticationSpan,
		spanStatus(outcome, errorType),
		operationAttributes(ComponentAuthentication, operation, outcome, errorType),
	)
}

func authenticationMetricAttributes(operation string, outcome string, errorType string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(
		SignalAuthenticationDuration,
		"",
		operationAttributes(ComponentAuthentication, operation, outcome, errorType),
	)
}
