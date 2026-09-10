package otelnative

import (
	"errors"
	"math"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestProjectionPoliciesRejectRawCollectionBounds(t *testing.T) {
	tooManyResources := make([]attribute.KeyValue, maxNativeProjectionResourceAttributes+1)
	if _, err := NewTransportSpanExporter(tracetest.NewInMemoryExporter(), TraceProjectionPolicy{ResourceAttributes: tooManyResources}); !errors.Is(err, ErrInvalidProjectionPolicy) {
		t.Fatalf("transport resources error=%v", err)
	}
	if _, err := TransportMetricOptions(TraceProjectionPolicy{RouterTables: make([]RouteTable, maxNativeProjectionTables+1)}); !errors.Is(err, ErrInvalidProjectionPolicy) {
		t.Fatalf("transport table error=%v", err)
	}
	tooManyPools := make([]DatabasePoolName, maxNativeDatabasePools+1)
	if _, err := NewDatabaseMetricExporter(&metricCapture{}, DatabaseProjectionPolicy{PoolNames: tooManyPools}); !errors.Is(err, ErrInvalidProjectionPolicy) {
		t.Fatalf("database policy error=%v", err)
	}
	if _, err := DatabaseMetricOptions(tooManyPools...); !errors.Is(err, ErrInvalidProjectionPolicy) {
		t.Fatalf("database options error=%v", err)
	}
	if _, err := NewRuntimeMetricExporter(&metricCapture{}, RuntimeProjectionPolicy{ResourceAttributes: tooManyResources}); !errors.Is(err, ErrInvalidProjectionPolicy) {
		t.Fatalf("runtime resources error=%v", err)
	}
}

func TestProjectionPoliciesRejectMalformedAndDuplicateIdentity(t *testing.T) {
	invalidResources := [][]attribute.KeyValue{
		{attribute.String("service.name", "one"), attribute.String("service.name", "two")},
		{attribute.String(strings.Repeat("k", maxNativeProjectionAttributeKeyBytes+1), "value")},
		{attribute.String("service.name", strings.Repeat("v", maxNativeProjectionAttributeValueBytes+1))},
		{attribute.Float64("number", math.NaN())},
		{{}},
	}
	for index, resources := range invalidResources {
		if _, err := NewTransportMetricExporter(&metricCapture{}, TraceProjectionPolicy{ResourceAttributes: resources}); !errors.Is(err, ErrInvalidProjectionPolicy) {
			t.Fatalf("resource case %d error=%v", index, err)
		}
	}
	pool, err := NewDatabasePoolName("primary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DatabaseMetricOptions(pool, pool); !errors.Is(err, ErrInvalidProjectionPolicy) {
		t.Fatalf("duplicate pool error=%v", err)
	}
	if _, err = NewDatabaseSpanExporter(tracetest.NewInMemoryExporter(), DatabaseProjectionPolicy{PoolNames: []DatabasePoolName{{}}}); !errors.Is(err, ErrInvalidProjectionPolicy) {
		t.Fatalf("zero pool error=%v", err)
	}
}
