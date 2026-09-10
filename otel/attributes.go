package vvotel

import "go.opentelemetry.io/otel/attribute"

func admitSignalAttributes(signal Signal, status string, attributes []attribute.KeyValue, facts ...SignalSourceValue) ([]attribute.KeyValue, bool) {
	candidate := append([]attribute.KeyValue(nil), attributes...)
	if !matchesSignal(signal, status, candidate, facts) {
		return nil, false
	}
	return candidate, true
}

func commandSpanAttributes(operation string, outcome string, errorType string, resourceName string, extra []attribute.KeyValue) ([]attribute.KeyValue, bool) {
	attributes := operationAttributes(ComponentCommand, operation, outcome, errorType)
	if resourceName != "" {
		attributes = append(attributes, AttrResourceName.String(resourceName))
	}
	attributes = append(attributes, extra...)
	return admitSignalAttributes(SignalCommandSpan, spanStatus(outcome, errorType), attributes)
}

func commandMetricAttributes(operation string, outcome string, errorType string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(
		SignalCommandDuration,
		"",
		operationAttributes(ComponentCommand, operation, outcome, errorType),
	)
}

func storageSpanAttributes(operation string, outcome string, errorType string, resourceName string, extra []attribute.KeyValue) ([]attribute.KeyValue, bool) {
	attributes := operationAttributes(ComponentStorage, operation, outcome, errorType)
	if resourceName != "" {
		attributes = append(attributes, AttrResourceName.String(resourceName))
	}
	attributes = append(attributes, extra...)
	return admitSignalAttributes(SignalStorageSpan, spanStatus(outcome, errorType), attributes)
}

func cacheFacadeAttributes(signal Signal, operation string, outcome string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(signal, "", []attribute.KeyValue{
		AttrComponent.String(ComponentCache),
		AttrCacheLayer.String(CacheLayerFacade),
		AttrOperationName.String(operation),
		AttrOperationOutcome.String(outcome),
	})
}

func cacheBackendAttributes(signal Signal, operation string, outcome string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(signal, "", []attribute.KeyValue{
		AttrComponent.String(ComponentCacheBackend),
		AttrCacheLayer.String(CacheBackendLayerMemoryBackend),
		AttrOperationName.String(operation),
		AttrOperationOutcome.String(outcome),
	})
}

func operationAttributes(component string, operation string, outcome string, errorType string) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		AttrComponent.String(component),
		AttrOperationName.String(operation),
		AttrOperationOutcome.String(outcome),
	}
	if errorType != "" {
		attributes = append(attributes, AttrErrorType.String(errorType))
	}
	return attributes
}

func spanStatus(outcome string, errorType string) string {
	if errorType == "" && outcome != OutcomeGoroutineExit {
		return "unset"
	}
	return "error"
}

func spanStartAttributes(attributes []attribute.KeyValue) []attribute.KeyValue {
	start := make([]attribute.KeyValue, 0, len(attributes))
	for _, current := range attributes {
		switch current.Key {
		case AttrOperationOutcome, AttrErrorType, AttrErrorCode, AttrResourceName:
			continue
		default:
			start = append(start, current)
		}
	}
	return start
}

func spanTerminalAttributes(attributes []attribute.KeyValue) []attribute.KeyValue {
	terminal := make([]attribute.KeyValue, 0, len(attributes))
	for _, current := range attributes {
		switch current.Key {
		case AttrOperationOutcome, AttrErrorType, AttrErrorCode:
			terminal = append(terminal, current)
		}
	}
	return terminal
}
