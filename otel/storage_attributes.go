package vvotel

import "go.opentelemetry.io/otel/attribute"

func storageMetricAttributes(operation string, outcome string, errorType string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(
		SignalStorageDuration,
		"",
		operationAttributes(ComponentStorage, operation, outcome, errorType),
	)
}

func storageOperationBytesAttributes(operation string, outcome string, errorType string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(
		SignalStorageOperationBytes,
		"",
		operationAttributes(ComponentStorage, operation, outcome, errorType),
	)
}

func storageCleanupRemovedAttributes(more bool) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(SignalStorageCleanupRemoved, "", []attribute.KeyValue{
		AttrComponent.String(ComponentStorage),
		AttrOperationName.String(OpStorageCleanupExpired),
		AttrOperationOutcome.String(OutcomeOk),
		AttrMore.Bool(more),
	})
}
