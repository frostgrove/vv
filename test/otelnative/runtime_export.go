package otelnative

import (
	"errors"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

var ErrInvalidRuntimeMetricExporter = errors.New("otelnative: downstream runtime metric exporter is required")

func NewRuntimeMetricExporter(next sdkmetric.Exporter, policy RuntimeProjectionPolicy) (sdkmetric.Exporter, error) {
	if nilInterface(next) {
		return nil, ErrInvalidRuntimeMetricExporter
	}
	resources := make(map[string]attribute.Value, len(policy.ResourceAttributes))
	for _, item := range policy.ResourceAttributes {
		resources[string(item.Key)] = item.Value
	}
	return &boundedMetricExporter{
		next:      next,
		resources: resources,
		scopes: map[string]metricScopeProjection{
			runtime.ScopeName: {
				name:       runtime.ScopeName,
				version:    runtime.Version,
				metrics:    runtimeMetricNames,
				attributes: projectRuntimeMetricAttributes,
			},
		},
	}, nil
}
