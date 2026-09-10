package otelnative

import (
	"errors"
	"reflect"
	"sync"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

var (
	ErrInvalidRuntimeMeterProvider        = errors.New("otelnative: runtime meter provider is required")
	ErrUnidentifiableRuntimeMeterProvider = errors.New("otelnative: runtime meter provider must have comparable identity")
)

var (
	runtimeStartMu sync.Mutex
	runtimeStarts  = make(map[metric.MeterProvider]*runtimeStart)
)

var runtimeMetricNames = map[string]struct{}{
	"go.memory.used":        {},
	"go.memory.limit":       {},
	"go.memory.allocated":   {},
	"go.memory.allocations": {},
	"go.memory.gc.goal":     {},
	"go.goroutine.count":    {},
	"go.processor.limit":    {},
	"go.config.gogc":        {},
}

type runtimeStart struct {
	once sync.Once
	err  error
}

type RuntimeProjectionPolicy struct {
	ResourceAttributes []attribute.KeyValue
}

func StartRuntimeMetrics(provider metric.MeterProvider) error {
	if nilInterface(provider) {
		return ErrInvalidRuntimeMeterProvider
	}
	providerType := reflect.TypeOf(provider)
	if providerType == nil || !providerType.Comparable() {
		return ErrUnidentifiableRuntimeMeterProvider
	}
	runtimeStartMu.Lock()
	state := runtimeStarts[provider]
	if state == nil {
		state = &runtimeStart{}
		runtimeStarts[provider] = state
	}
	runtimeStartMu.Unlock()
	state.once.Do(func() {
		state.err = otelruntime.Start(otelruntime.WithMeterProvider(provider))
	})
	return state.err
}

func RuntimeMetricOptions() []sdkmetric.Option {
	filter := func(item attribute.KeyValue) bool {
		return len(projectRuntimeMetricAttributes([]attribute.KeyValue{item})) == 1
	}
	return []sdkmetric.Option{sdkmetric.WithView(sdkmetric.NewView(
		sdkmetric.Instrument{
			Name:  "*",
			Scope: instrumentation.Scope{Name: otelruntime.ScopeName},
		},
		sdkmetric.Stream{AttributeFilter: filter},
	))}
}

func projectRuntimeMetricAttributes(items []attribute.KeyValue) []attribute.KeyValue {
	projected := make([]attribute.KeyValue, 0, len(items))
	for _, item := range items {
		if string(item.Key) != "go.memory.type" || item.Value.Type() != attribute.STRING {
			continue
		}
		value := item.Value.AsString()
		if value == "other" || value == "stack" {
			projected = append(projected, item)
		}
	}
	return projected
}
