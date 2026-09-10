package otelnative

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

const runtimeServiceName = "runtime-fixture"

func TestRuntimeRecipeStartsExactlyOnceForTheSuppliedProvider(t *testing.T) {
	sink := &metricCapture{}
	exporter, err := NewRuntimeMetricExporter(sink, RuntimeProjectionPolicy{
		ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", runtimeServiceName)},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := sdkmetric.NewPeriodicReader(exporter)
	options := []sdkmetric.Option{
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(resource.NewSchemaless(
			attribute.String("service.name", runtimeServiceName),
			attribute.String("secret.resource", secretResource),
		)),
	}
	options = append(options, RuntimeMetricOptions()...)
	provider := sdkmetric.NewMeterProvider(options...)
	counted := &countingMeterProvider{MeterProvider: provider}
	if err = StartRuntimeMetrics(counted); err != nil {
		t.Fatal(err)
	}
	if err = StartRuntimeMetrics(counted); err != nil {
		t.Fatal(err)
	}
	if got := counted.calls.Load(); got != 1 {
		t.Fatalf("runtime requested meters %d times", got)
	}
	if err = provider.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	metrics := sink.snapshot()
	var names []string
	for _, scoped := range metrics.ScopeMetrics {
		if scoped.Scope.Name != otelruntime.ScopeName {
			continue
		}
		if scoped.Scope.Version != otelruntime.Version || scoped.Scope.SchemaURL != "" || scoped.Scope.Attributes.Len() != 0 {
			t.Fatalf("runtime scope=%+v", scoped.Scope)
		}
		for _, measurement := range scoped.Metrics {
			names = append(names, measurement.Name)
		}
	}
	for _, expected := range []string{"go.memory.used", "go.memory.allocated", "go.goroutine.count", "go.processor.limit"} {
		if !slices.Contains(names, expected) {
			t.Fatalf("runtime metrics %v do not contain %q", names, expected)
		}
	}
	assertNativePrivacy(t, nil, metrics, secretResource)
	if err = provider.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeRecipeRejectsUnownedProviderIdentity(t *testing.T) {
	if err := StartRuntimeMetrics(nil); !errors.Is(err, ErrInvalidRuntimeMeterProvider) {
		t.Fatalf("nil provider error=%v", err)
	}
	provider := nonComparableMeterProvider{
		MeterProvider: sdkmetric.NewMeterProvider(),
		identity:      []int{1},
	}
	if err := StartRuntimeMetrics(provider); !errors.Is(err, ErrUnidentifiableRuntimeMeterProvider) {
		t.Fatalf("non-comparable provider error=%v", err)
	}
}

type countingMeterProvider struct {
	otelmetric.MeterProvider
	calls atomic.Int64
}

func (p *countingMeterProvider) Meter(name string, options ...otelmetric.MeterOption) otelmetric.Meter {
	p.calls.Add(1)
	return p.MeterProvider.Meter(name, options...)
}

type nonComparableMeterProvider struct {
	otelmetric.MeterProvider
	identity []int
}
