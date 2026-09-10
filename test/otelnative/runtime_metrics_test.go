package otelnative

import (
	"context"
	"errors"
	"runtime"
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
	runtimeMetrics, err := NewRuntimeMetrics(counted)
	if err != nil {
		t.Fatal(err)
	}
	if err = runtimeMetrics.Start(); err != nil {
		t.Fatal(err)
	}
	if err = runtimeMetrics.Start(); err != nil {
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

func TestRuntimeRecipeRejectsInvalidHandles(t *testing.T) {
	if runtimeMetrics, err := NewRuntimeMetrics(nil); runtimeMetrics != nil || !errors.Is(err, ErrInvalidRuntimeMeterProvider) {
		t.Fatalf("nil provider error=%v", err)
	}
	provider := nonComparableMeterProvider{
		MeterProvider: sdkmetric.NewMeterProvider(),
		identity:      []int{1},
	}
	runtimeMetrics, err := NewRuntimeMetrics(provider)
	if err != nil {
		t.Fatal(err)
	}
	if err = runtimeMetrics.Start(); err != nil {
		t.Fatal(err)
	}
	var nilRuntime *RuntimeMetrics
	if err = nilRuntime.Start(); !errors.Is(err, ErrInvalidRuntimeMetrics) {
		t.Fatalf("nil handle error=%v", err)
	}
	var typedNil *countingMeterProvider
	if runtimeMetrics, err = NewRuntimeMetrics(typedNil); runtimeMetrics != nil || !errors.Is(err, ErrInvalidRuntimeMeterProvider) {
		t.Fatalf("typed-nil provider result/error=%#v/%v", runtimeMetrics, err)
	}
}

func TestRuntimeHandleContainsHostileStartAndRemainsTerminal(t *testing.T) {
	for _, mode := range []string{"panic", "panicnil", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			provider := &hostileRuntimeMeterProvider{MeterProvider: sdkmetric.NewMeterProvider(), mode: mode}
			runtimeMetrics, err := NewRuntimeMetrics(provider)
			if err != nil {
				t.Fatal(err)
			}
			if err = runtimeMetrics.Start(); !errors.Is(err, ErrRuntimeMetricStart) {
				t.Fatalf("hostile start error=%v", err)
			}
			if err = runtimeMetrics.Start(); !errors.Is(err, ErrRuntimeMetricStart) {
				t.Fatalf("terminal start error=%v", err)
			}
			if calls := provider.calls.Load(); calls != 1 {
				t.Fatalf("meter calls=%d", calls)
			}
		})
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

type hostileRuntimeMeterProvider struct {
	otelmetric.MeterProvider
	mode  string
	calls atomic.Int64
}

func (provider *hostileRuntimeMeterProvider) Meter(string, ...otelmetric.MeterOption) otelmetric.Meter {
	provider.calls.Add(1)
	if provider.mode == "goexit" {
		runtime.Goexit()
	}
	if provider.mode == "panicnil" {
		panic(nil)
	}
	panic("runtime meter")
}
