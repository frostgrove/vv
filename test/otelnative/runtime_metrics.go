package otelnative

import (
	"errors"
	"sync"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/semconv/v1.41.0/goconv"
)

var (
	ErrInvalidRuntimeMeterProvider = errors.New("otelnative: runtime meter provider is required")
	ErrInvalidRuntimeMetrics       = errors.New("otelnative: runtime metrics handle is required")
	ErrRuntimeMetricStart          = errors.New("otelnative: runtime metric start failed")
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

type RuntimeMetrics struct {
	provider metric.MeterProvider
	once     sync.Once
	err      error
}

type RuntimeProjectionPolicy struct {
	ResourceAttributes []attribute.KeyValue
}

func NewRuntimeMetrics(provider metric.MeterProvider) (*RuntimeMetrics, error) {
	if nilInterface(provider) {
		return nil, ErrInvalidRuntimeMeterProvider
	}
	return &RuntimeMetrics{provider: provider}, nil
}

func (runtime *RuntimeMetrics) Start() error {
	if runtime == nil || nilInterface(runtime.provider) {
		return ErrInvalidRuntimeMetrics
	}
	runtime.once.Do(func() {
		runtime.err = ErrRuntimeMetricStart
		runtime.err = safeRuntimeMetricStart(runtime.provider)
	})
	return runtime.err
}

func safeRuntimeMetricStart(provider metric.MeterProvider) (err error) {
	_, err = runNativeAssembly(func() (bool, error) {
		return true, otelruntime.Start(otelruntime.WithMeterProvider(provider))
	})
	if err == ErrNativeAssembly {
		return ErrRuntimeMetricStart
	}
	return err
}

func RuntimeMetricOptions() []sdkmetric.Option {
	specs := runtimeMetricSpecs()
	options := make([]sdkmetric.Option, 0, len(specs))
	for name, spec := range specs {
		filter := func(item attribute.KeyValue) bool {
			projected := spec.attributes([]attribute.KeyValue{item})
			return len(projected) == 1 && projected[0] == item
		}
		options = append(options, sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{
				Name:  name,
				Scope: instrumentation.Scope{Name: otelruntime.ScopeName},
			},
			sdkmetric.Stream{AttributeFilter: filter},
		)))
	}
	return options
}

func runtimeMetricSpecs() map[string]nativeMetricSpec {
	noAttributes := func([]attribute.KeyValue) []attribute.KeyValue { return nil }
	return map[string]nativeMetricSpec{
		goconv.MemoryUsed{}.Name(): {
			description: goconv.MemoryUsed{}.Description(),
			unit:        goconv.MemoryUsed{}.Unit(),
			shape:       metricSumInt64,
			attributes:  projectRuntimeMetricAttributes,
		},
		goconv.MemoryLimit{}.Name(): {
			description: goconv.MemoryLimit{}.Description(),
			unit:        goconv.MemoryLimit{}.Unit(),
			shape:       metricSumInt64,
			attributes:  noAttributes,
		},
		goconv.MemoryAllocated{}.Name(): {
			description: goconv.MemoryAllocated{}.Description(),
			unit:        goconv.MemoryAllocated{}.Unit(),
			shape:       metricSumInt64,
			monotonic:   true,
			attributes:  noAttributes,
		},
		goconv.MemoryAllocations{}.Name(): {
			description: goconv.MemoryAllocations{}.Description(),
			unit:        goconv.MemoryAllocations{}.Unit(),
			shape:       metricSumInt64,
			monotonic:   true,
			attributes:  noAttributes,
		},
		goconv.MemoryGCGoal{}.Name(): {
			description: goconv.MemoryGCGoal{}.Description(),
			unit:        goconv.MemoryGCGoal{}.Unit(),
			shape:       metricSumInt64,
			attributes:  noAttributes,
		},
		goconv.GoroutineCount{}.Name(): {
			description: goconv.GoroutineCount{}.Description(),
			unit:        goconv.GoroutineCount{}.Unit(),
			shape:       metricSumInt64,
			attributes:  noAttributes,
		},
		goconv.ProcessorLimit{}.Name(): {
			description: goconv.ProcessorLimit{}.Description(),
			unit:        goconv.ProcessorLimit{}.Unit(),
			shape:       metricSumInt64,
			attributes:  noAttributes,
		},
		goconv.ConfigGogc{}.Name(): {
			description: goconv.ConfigGogc{}.Description(),
			unit:        goconv.ConfigGogc{}.Unit(),
			shape:       metricSumInt64,
			attributes:  noAttributes,
		},
	}
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
