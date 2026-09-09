package otelnative

import (
	"context"
	"errors"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

var ErrInvalidMetricExporter = errors.New("otelnative: downstream metric exporter is required")

var nativeMetricNames = map[string]struct{}{
	"http.client.active_requests":     {},
	"http.client.connection.duration": {},
	"http.client.open_connections":    {},
	"http.client.request.body.size":   {},
	"http.client.request.duration":    {},
	"http.client.response.body.size":  {},
	"http.server.active_requests":     {},
	"http.server.request.body.size":   {},
	"http.server.request.duration":    {},
	"http.server.response.body.size":  {},
	"rpc.client.call.duration":        {},
	"rpc.server.call.duration":        {},
}

func NewTransportMetricExporter(next sdkmetric.Exporter, policy TraceProjectionPolicy) (sdkmetric.Exporter, error) {
	if nilInterface(next) {
		return nil, ErrInvalidMetricExporter
	}
	return &projectingMetricExporter{next: next, policy: compileTracePolicy(policy)}, nil
}

type projectingMetricExporter struct {
	next   sdkmetric.Exporter
	policy compiledTracePolicy
}

func (e *projectingMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.next.Temporality(kind)
}

func (e *projectingMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.next.Aggregation(kind)
}

func (e *projectingMetricExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	projected := projectResourceMetrics(metrics, e.policy)
	return e.next.Export(ctx, &projected)
}

func (e *projectingMetricExporter) ForceFlush(ctx context.Context) error {
	return e.next.ForceFlush(ctx)
}

func (e *projectingMetricExporter) Shutdown(ctx context.Context) error {
	return e.next.Shutdown(ctx)
}

func projectResourceMetrics(original *metricdata.ResourceMetrics, policy compiledTracePolicy) metricdata.ResourceMetrics {
	if original == nil {
		return metricdata.ResourceMetrics{Resource: projectResource(nil, policy.resources)}
	}
	projected := metricdata.ResourceMetrics{
		Resource:     projectResource(original.Resource, policy.resources),
		ScopeMetrics: make([]metricdata.ScopeMetrics, 0, len(original.ScopeMetrics)),
	}
	for _, scopeMetrics := range original.ScopeMetrics {
		scope, native := nativeMetricScope(scopeMetrics.Scope)
		if !native {
			scope = cloneScope(scopeMetrics.Scope)
		}
		metrics := make([]metricdata.Metrics, 0, len(scopeMetrics.Metrics))
		for _, item := range scopeMetrics.Metrics {
			if native {
				if _, allowed := nativeMetricNames[item.Name]; !allowed {
					continue
				}
			}
			data, ok := projectAggregation(scopeMetrics.Scope.Name, item.Data, policy, native)
			if !ok {
				continue
			}
			metrics = append(metrics, metricdata.Metrics{
				Name:        item.Name,
				Description: item.Description,
				Unit:        item.Unit,
				Data:        data,
			})
		}
		if len(metrics) > 0 {
			projected.ScopeMetrics = append(projected.ScopeMetrics, metricdata.ScopeMetrics{Scope: scope, Metrics: metrics})
		}
	}
	return projected
}

func nativeMetricScope(scope instrumentation.Scope) (instrumentation.Scope, bool) {
	expectedVersion, native := expectedNativeVersion(scope.Name)
	if !native {
		return instrumentation.Scope{}, false
	}
	expectedSchema := ""
	if scope.Name == otelgrpc.ScopeName {
		expectedSchema = semconv.SchemaURL
	}
	version := expectedVersion
	schemaURL := expectedSchema
	if scope.Version != expectedVersion || scope.SchemaURL != expectedSchema {
		version = "_OTHER"
		schemaURL = ""
	}
	return instrumentation.Scope{
		Name:       scope.Name,
		Version:    version,
		SchemaURL:  schemaURL,
		Attributes: attribute.NewSet(),
	}, true
}

func cloneScope(scope instrumentation.Scope) instrumentation.Scope {
	return instrumentation.Scope{
		Name:       scope.Name,
		Version:    scope.Version,
		SchemaURL:  scope.SchemaURL,
		Attributes: attribute.NewSet(scope.Attributes.ToSlice()...),
	}
}

func projectAggregation(scope string, aggregation metricdata.Aggregation, policy compiledTracePolicy, native bool) (metricdata.Aggregation, bool) {
	switch data := aggregation.(type) {
	case metricdata.Gauge[int64]:
		data.DataPoints = projectDataPoints(scope, data.DataPoints, policy, native)
		return data, true
	case metricdata.Gauge[float64]:
		data.DataPoints = projectDataPoints(scope, data.DataPoints, policy, native)
		return data, true
	case metricdata.Sum[int64]:
		data.DataPoints = projectDataPoints(scope, data.DataPoints, policy, native)
		return data, true
	case metricdata.Sum[float64]:
		data.DataPoints = projectDataPoints(scope, data.DataPoints, policy, native)
		return data, true
	case metricdata.Histogram[int64]:
		data.DataPoints = projectHistogramPoints(scope, data.DataPoints, policy, native)
		return data, true
	case metricdata.Histogram[float64]:
		data.DataPoints = projectHistogramPoints(scope, data.DataPoints, policy, native)
		return data, true
	case metricdata.ExponentialHistogram[int64]:
		data.DataPoints = projectExponentialHistogramPoints(scope, data.DataPoints, policy, native)
		return data, true
	case metricdata.ExponentialHistogram[float64]:
		data.DataPoints = projectExponentialHistogramPoints(scope, data.DataPoints, policy, native)
		return data, true
	default:
		return nil, false
	}
}

func projectDataPoints[N int64 | float64](scope string, points []metricdata.DataPoint[N], policy compiledTracePolicy, native bool) []metricdata.DataPoint[N] {
	projected := make([]metricdata.DataPoint[N], len(points))
	for index, point := range points {
		point.Attributes = projectMetricAttributeSet(scope, point.Attributes, policy, native)
		point.Exemplars = projectExemplars(scope, point.Exemplars, policy, native)
		projected[index] = point
	}
	return projected
}

func projectHistogramPoints[N int64 | float64](scope string, points []metricdata.HistogramDataPoint[N], policy compiledTracePolicy, native bool) []metricdata.HistogramDataPoint[N] {
	projected := make([]metricdata.HistogramDataPoint[N], len(points))
	for index, point := range points {
		point.Attributes = projectMetricAttributeSet(scope, point.Attributes, policy, native)
		point.Bounds = append([]float64(nil), point.Bounds...)
		point.BucketCounts = append([]uint64(nil), point.BucketCounts...)
		point.Exemplars = projectExemplars(scope, point.Exemplars, policy, native)
		projected[index] = point
	}
	return projected
}

func projectExponentialHistogramPoints[N int64 | float64](scope string, points []metricdata.ExponentialHistogramDataPoint[N], policy compiledTracePolicy, native bool) []metricdata.ExponentialHistogramDataPoint[N] {
	projected := make([]metricdata.ExponentialHistogramDataPoint[N], len(points))
	for index, point := range points {
		point.Attributes = projectMetricAttributeSet(scope, point.Attributes, policy, native)
		point.PositiveBucket.Counts = append([]uint64(nil), point.PositiveBucket.Counts...)
		point.NegativeBucket.Counts = append([]uint64(nil), point.NegativeBucket.Counts...)
		point.Exemplars = projectExemplars(scope, point.Exemplars, policy, native)
		projected[index] = point
	}
	return projected
}

func projectExemplars[N int64 | float64](scope string, exemplars []metricdata.Exemplar[N], policy compiledTracePolicy, native bool) []metricdata.Exemplar[N] {
	projected := make([]metricdata.Exemplar[N], len(exemplars))
	for index, exemplar := range exemplars {
		if native {
			exemplar.FilteredAttributes = projectTraceAttributes(scope, exemplar.FilteredAttributes, policy)
		} else {
			exemplar.FilteredAttributes = append([]attribute.KeyValue(nil), exemplar.FilteredAttributes...)
		}
		exemplar.TraceID = append([]byte(nil), exemplar.TraceID...)
		exemplar.SpanID = append([]byte(nil), exemplar.SpanID...)
		projected[index] = exemplar
	}
	return projected
}

func projectMetricAttributeSet(scope string, set attribute.Set, policy compiledTracePolicy, native bool) attribute.Set {
	attributes := set.ToSlice()
	if native {
		attributes = projectTraceAttributes(scope, attributes, policy)
	} else {
		attributes = append([]attribute.KeyValue(nil), attributes...)
	}
	return attribute.NewSet(attributes...)
}
