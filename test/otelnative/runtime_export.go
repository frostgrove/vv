package otelnative

import (
	"errors"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

var ErrInvalidRuntimeMetricExporter = errors.New("otelnative: downstream runtime metric exporter is required")

func NewRuntimeMetricExporter(next sdkmetric.Exporter, policy RuntimeProjectionPolicy) (sdkmetric.Exporter, error) {
	if nilInterface(next) {
		return nil, ErrInvalidRuntimeMetricExporter
	}
	resources, err := compileNativeResources(policy.ResourceAttributes)
	if err != nil {
		return nil, err
	}
	return &boundedMetricExporter{
		next:      next,
		resources: resources,
		scopes: map[string]metricScopeProjection{
			runtime.ScopeName: {
				name:    runtime.ScopeName,
				version: runtime.Version,
				metrics: runtimeMetricSpecs(),
			},
		},
	}, nil
}
