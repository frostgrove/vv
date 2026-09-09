package vvotel

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

func TestAdmitSignalAttributesUsesGeneratedVariantsAndCopies(t *testing.T) {
	input := []attribute.KeyValue{
		AttrComponent.String(ComponentCommand),
		AttrOperationName.String(OpCommandGet),
		AttrOperationOutcome.String(OutcomeOk),
	}
	admitted, ok := admitSignalAttributes(SignalCommandDuration, "", input)
	if !ok {
		t.Fatal("generated command metric variant was rejected")
	}
	input[0] = AttrComponent.String("secret")
	if admitted[0].Value.AsString() != ComponentCommand {
		t.Fatal("admitted attributes alias caller storage")
	}

	mutations := [][]attribute.KeyValue{
		{
			AttrComponent.String(ComponentCommand),
			AttrOperationName.String("secret"),
			AttrOperationOutcome.String(OutcomeOk),
		},
		{
			AttrComponent.String(ComponentCommand),
			AttrOperationName.String(OpCommandGet),
			AttrOperationOutcome.String(OutcomeOk),
			attribute.String("secret.attribute", "secret"),
		},
		{
			AttrComponent.String(ComponentCommand),
			AttrOperationName.String(OpCommandGet),
			AttrOperationOutcome.String(OutcomeOk),
			AttrOperationOutcome.String(OutcomeError),
		},
	}
	for _, mutation := range mutations {
		if got, accepted := admitSignalAttributes(SignalCommandDuration, "", mutation); accepted || got != nil {
			t.Fatalf("generated matcher admitted mutation: %+v", mutation)
		}
	}
}

func TestNarrowAttributeBuildersEnforceSignalOwnership(t *testing.T) {
	tests := []struct {
		name  string
		build func() ([]attribute.KeyValue, bool)
		ok    bool
	}{
		{name: "command_span", build: func() ([]attribute.KeyValue, bool) {
			return commandSpanAttributes(OpCommandGet, OutcomeError, ErrorTypeNotFound, "orders", []attribute.KeyValue{AttrErrorCode.String(ErrorCodeNotFound)})
		}, ok: true},
		{name: "command_metric", build: func() ([]attribute.KeyValue, bool) {
			return commandMetricAttributes(OpCommandGet, OutcomeTimeout, ErrorTypeTimeout)
		}, ok: true},
		{name: "storage_span", build: func() ([]attribute.KeyValue, bool) {
			return storageSpanAttributes(OpStoragePut, OutcomeOk, "", "objects", nil)
		}, ok: true},
		{name: "cache_facade", build: func() ([]attribute.KeyValue, bool) {
			return cacheFacadeAttributes(SignalCacheOperations, OpCacheLookup, "hit")
		}, ok: true},
		{name: "cache_backend", build: func() ([]attribute.KeyValue, bool) {
			return cacheBackendAttributes(SignalCacheOperations, OpCacheBackendGet, "hit")
		}, ok: true},
		{name: "foreign_command_operation", build: func() ([]attribute.KeyValue, bool) {
			return commandMetricAttributes(OpStoragePut, OutcomeOk, "")
		}},
		{name: "invalid_declared_name", build: func() ([]attribute.KeyValue, bool) {
			return storageSpanAttributes(OpStoragePut, OutcomeOk, "", "tenant/secret", nil)
		}},
		{name: "metric_error_code", build: func() ([]attribute.KeyValue, bool) {
			attributes := operationAttributes(ComponentCommand, OpCommandGet, OutcomeError, ErrorTypeInternal)
			attributes = append(attributes, AttrErrorCode.String(ErrorCodeInternal))
			return admitSignalAttributes(SignalCommandDuration, "", attributes)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attributes, ok := test.build()
			if ok != test.ok {
				t.Fatalf("admission = %v, want %v; attributes=%+v", ok, test.ok, attributes)
			}
			if !ok && attributes != nil {
				t.Fatal("rejected builder returned attributes")
			}
		})
	}
}

func TestSpanAttributePhasesDoNotCrossBoundaries(t *testing.T) {
	full, ok := commandSpanAttributes(OpCommandGet, OutcomeError, ErrorTypeInternal, "orders", []attribute.KeyValue{AttrErrorCode.String(ErrorCodeInternal)})
	if !ok {
		t.Fatal("control span attributes were rejected")
	}
	start := spanStartAttributes(full)
	terminal := spanTerminalAttributes(full)
	for _, current := range start {
		if current.Key == AttrOperationOutcome || current.Key == AttrErrorType || current.Key == AttrErrorCode || current.Key == AttrResourceName {
			t.Fatalf("terminal or declared attribute %s entered Start", current.Key)
		}
	}
	for _, current := range terminal {
		if current.Key == AttrComponent || current.Key == AttrOperationName || current.Key == AttrResourceName {
			t.Fatalf("base or declared attribute %s entered terminal mutation", current.Key)
		}
	}
}
