package otelnative

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

var ErrInvalidDatabaseMetricExporter = errors.New("otelnative: downstream database metric exporter is required")

var (
	pgxMetricNames = map[string]struct{}{
		"db.client.operation.duration": {},
		"db.client.operation.errors":   {},
	}
	sqlMetricNames = map[string]struct{}{
		"db.client.operation.duration":           {},
		"db.sql.connection.max_open":             {},
		"db.sql.connection.open":                 {},
		"db.sql.connection.wait":                 {},
		"db.sql.connection.wait_duration":        {},
		"db.sql.connection.closed_max_idle":      {},
		"db.sql.connection.closed_max_idle_time": {},
		"db.sql.connection.closed_max_lifetime":  {},
	}
	pgxPoolMetricNames = map[string]struct{}{
		pgxPoolAcquiredMetric:    {},
		pgxPoolIdleMetric:        {},
		pgxPoolMaximumMetric:     {},
		pgxPoolAcquireWaitMetric: {},
		pgxPoolAcquireTimeMetric: {},
	}
	pgxOperations = map[string]struct{}{
		"query": {}, "copy": {}, "batch": {}, "connect": {}, "prepare": {}, "acquire": {},
	}
	sqlOperations = map[string]struct{}{
		"sql.connector.connect": {}, "sql.conn.ping": {}, "sql.conn.exec": {}, "sql.conn.query": {},
		"sql.conn.prepare": {}, "sql.conn.begin_tx": {}, "sql.conn.reset_session": {},
		"sql.tx.commit": {}, "sql.tx.rollback": {}, "sql.stmt.exec": {}, "sql.stmt.query": {}, "sql.rows": {},
	}
)

type metricScopeProjection struct {
	name       string
	version    string
	metrics    map[string]struct{}
	attributes func([]attribute.KeyValue) []attribute.KeyValue
}

type boundedMetricExporter struct {
	next      sdkmetric.Exporter
	resources map[string]attribute.Value
	scopes    map[string]metricScopeProjection
}

func NewDatabaseMetricExporter(next sdkmetric.Exporter, policy DatabaseProjectionPolicy) (sdkmetric.Exporter, error) {
	if nilInterface(next) {
		return nil, ErrInvalidDatabaseMetricExporter
	}
	compiled := compileDatabaseProjectionPolicy(policy)
	return &boundedMetricExporter{
		next:      next,
		resources: compiled.resources,
		scopes:    databaseMetricScopes(compiled.pools),
	}, nil
}

func DatabaseMetricOptions(poolNames ...DatabasePoolName) []sdkmetric.Option {
	pools := make(map[string]struct{}, len(poolNames))
	for _, pool := range poolNames {
		if validDatabasePoolName(pool.value) {
			pools[pool.value] = struct{}{}
		}
	}
	scopes := databaseMetricScopes(pools)
	options := make([]sdkmetric.Option, 0, len(scopes))
	for _, policy := range scopes {
		filter := func(item attribute.KeyValue) bool {
			return len(policy.attributes([]attribute.KeyValue{item})) == 1
		}
		options = append(options, sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{
				Name:  "*",
				Scope: instrumentation.Scope{Name: policy.name},
			},
			sdkmetric.Stream{AttributeFilter: filter},
		)))
	}
	return options
}

func databaseMetricScopes(pools map[string]struct{}) map[string]metricScopeProjection {
	return map[string]metricScopeProjection{
		pgxScopeName: {
			name:    pgxScopeName,
			version: pgxScopeVersion,
			metrics: pgxMetricNames,
			attributes: func(items []attribute.KeyValue) []attribute.KeyValue {
				return projectPGXMetricAttributes(items)
			},
		},
		sqlScopeName: {
			name:    sqlScopeName,
			version: "0.43.0",
			metrics: sqlMetricNames,
			attributes: func(items []attribute.KeyValue) []attribute.KeyValue {
				return projectSQLMetricAttributes(items, pools)
			},
		},
		pgxPoolScopeName: {
			name:    pgxPoolScopeName,
			version: pgxPoolVersion,
			metrics: pgxPoolMetricNames,
			attributes: func(items []attribute.KeyValue) []attribute.KeyValue {
				return projectPoolMetricAttributes(items, pools)
			},
		},
	}
}

func projectPGXMetricAttributes(items []attribute.KeyValue) []attribute.KeyValue {
	projected := make([]attribute.KeyValue, 0, len(items))
	for _, item := range items {
		switch string(item.Key) {
		case "db.system.name":
			if item.Value.Type() == attribute.STRING && item.Value.AsString() == "postgresql" {
				projected = append(projected, item)
			}
		case "pgx.operation.type":
			if item.Value.Type() == attribute.STRING {
				if _, allowed := pgxOperations[item.Value.AsString()]; allowed {
					projected = append(projected, item)
				}
			}
		}
	}
	return projected
}

func projectSQLMetricAttributes(items []attribute.KeyValue, pools map[string]struct{}) []attribute.KeyValue {
	projected := make([]attribute.KeyValue, 0, len(items))
	for _, item := range items {
		switch string(item.Key) {
		case "db.operation.name":
			if item.Value.Type() == attribute.STRING {
				if _, allowed := sqlOperations[item.Value.AsString()]; allowed {
					projected = append(projected, item)
				}
			}
		case databasePoolKey:
			if item.Value.Type() == attribute.STRING {
				if _, allowed := pools[item.Value.AsString()]; allowed {
					projected = append(projected, item)
				}
			}
		case "status":
			if item.Value.Type() == attribute.STRING && (item.Value.AsString() == "inuse" || item.Value.AsString() == "idle") {
				projected = append(projected, item)
			}
		}
	}
	return projected
}

func projectPoolMetricAttributes(items []attribute.KeyValue, pools map[string]struct{}) []attribute.KeyValue {
	projected := make([]attribute.KeyValue, 0, 1)
	for _, item := range items {
		if string(item.Key) != databasePoolKey || item.Value.Type() != attribute.STRING {
			continue
		}
		if _, allowed := pools[item.Value.AsString()]; allowed {
			projected = append(projected, item)
		}
	}
	return projected
}

func (e *boundedMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.next.Temporality(kind)
}

func (e *boundedMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.next.Aggregation(kind)
}

func (e *boundedMetricExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	projected := projectBoundedResourceMetrics(metrics, e.resources, e.scopes)
	return e.next.Export(ctx, &projected)
}

func (e *boundedMetricExporter) ForceFlush(ctx context.Context) error {
	return e.next.ForceFlush(ctx)
}

func (e *boundedMetricExporter) Shutdown(ctx context.Context) error {
	return e.next.Shutdown(ctx)
}

func projectBoundedResourceMetrics(original *metricdata.ResourceMetrics, resources map[string]attribute.Value, scopes map[string]metricScopeProjection) metricdata.ResourceMetrics {
	if original == nil {
		return metricdata.ResourceMetrics{Resource: projectResource(nil, resources)}
	}
	projected := metricdata.ResourceMetrics{
		Resource:     projectResource(original.Resource, resources),
		ScopeMetrics: make([]metricdata.ScopeMetrics, 0, len(original.ScopeMetrics)),
	}
	for _, scopeMetrics := range original.ScopeMetrics {
		policy, known := scopes[scopeMetrics.Scope.Name]
		if !known {
			projected.ScopeMetrics = append(projected.ScopeMetrics, scopeMetrics)
			continue
		}
		scope := instrumentation.Scope{
			Name:       policy.name,
			Version:    policy.version,
			Attributes: attribute.NewSet(),
		}
		if !databaseScopeVersionAllowed(scopeMetrics.Scope.Name, scopeMetrics.Scope.Version, policy.version) || scopeMetrics.Scope.SchemaURL != "" {
			scope.Version = "_OTHER"
		}
		measurements := make([]metricdata.Metrics, 0, len(scopeMetrics.Metrics))
		for _, measurement := range scopeMetrics.Metrics {
			if _, allowed := policy.metrics[measurement.Name]; !allowed {
				continue
			}
			data, ok := projectBoundedAggregation(measurement.Data, policy.attributes)
			if !ok {
				continue
			}
			measurements = append(measurements, metricdata.Metrics{
				Name:        measurement.Name,
				Description: measurement.Description,
				Unit:        measurement.Unit,
				Data:        data,
			})
		}
		if len(measurements) > 0 {
			projected.ScopeMetrics = append(projected.ScopeMetrics, metricdata.ScopeMetrics{Scope: scope, Metrics: measurements})
		}
	}
	return projected
}

func projectBoundedAggregation(aggregation metricdata.Aggregation, project func([]attribute.KeyValue) []attribute.KeyValue) (metricdata.Aggregation, bool) {
	switch data := aggregation.(type) {
	case metricdata.Gauge[int64]:
		data.DataPoints = projectBoundedDataPoints(data.DataPoints, project)
		return data, true
	case metricdata.Gauge[float64]:
		data.DataPoints = projectBoundedDataPoints(data.DataPoints, project)
		return data, true
	case metricdata.Sum[int64]:
		data.DataPoints = projectBoundedDataPoints(data.DataPoints, project)
		return data, true
	case metricdata.Sum[float64]:
		data.DataPoints = projectBoundedDataPoints(data.DataPoints, project)
		return data, true
	case metricdata.Histogram[int64]:
		data.DataPoints = projectBoundedHistogramPoints(data.DataPoints, project)
		return data, true
	case metricdata.Histogram[float64]:
		data.DataPoints = projectBoundedHistogramPoints(data.DataPoints, project)
		return data, true
	case metricdata.ExponentialHistogram[int64]:
		data.DataPoints = projectBoundedExponentialHistogramPoints(data.DataPoints, project)
		return data, true
	case metricdata.ExponentialHistogram[float64]:
		data.DataPoints = projectBoundedExponentialHistogramPoints(data.DataPoints, project)
		return data, true
	default:
		return nil, false
	}
}

func projectBoundedDataPoints[N int64 | float64](points []metricdata.DataPoint[N], project func([]attribute.KeyValue) []attribute.KeyValue) []metricdata.DataPoint[N] {
	result := make([]metricdata.DataPoint[N], len(points))
	for index, point := range points {
		point.Attributes = attribute.NewSet(project(point.Attributes.ToSlice())...)
		point.Exemplars = projectBoundedExemplars(point.Exemplars, project)
		result[index] = point
	}
	return result
}

func projectBoundedHistogramPoints[N int64 | float64](points []metricdata.HistogramDataPoint[N], project func([]attribute.KeyValue) []attribute.KeyValue) []metricdata.HistogramDataPoint[N] {
	result := make([]metricdata.HistogramDataPoint[N], len(points))
	for index, point := range points {
		point.Attributes = attribute.NewSet(project(point.Attributes.ToSlice())...)
		point.Bounds = append([]float64(nil), point.Bounds...)
		point.BucketCounts = append([]uint64(nil), point.BucketCounts...)
		point.Exemplars = projectBoundedExemplars(point.Exemplars, project)
		result[index] = point
	}
	return result
}

func projectBoundedExponentialHistogramPoints[N int64 | float64](points []metricdata.ExponentialHistogramDataPoint[N], project func([]attribute.KeyValue) []attribute.KeyValue) []metricdata.ExponentialHistogramDataPoint[N] {
	result := make([]metricdata.ExponentialHistogramDataPoint[N], len(points))
	for index, point := range points {
		point.Attributes = attribute.NewSet(project(point.Attributes.ToSlice())...)
		point.PositiveBucket.Counts = append([]uint64(nil), point.PositiveBucket.Counts...)
		point.NegativeBucket.Counts = append([]uint64(nil), point.NegativeBucket.Counts...)
		point.Exemplars = projectBoundedExemplars(point.Exemplars, project)
		result[index] = point
	}
	return result
}

func projectBoundedExemplars[N int64 | float64](exemplars []metricdata.Exemplar[N], project func([]attribute.KeyValue) []attribute.KeyValue) []metricdata.Exemplar[N] {
	result := make([]metricdata.Exemplar[N], len(exemplars))
	for index, exemplar := range exemplars {
		exemplar.FilteredAttributes = project(exemplar.FilteredAttributes)
		exemplar.TraceID = append([]byte(nil), exemplar.TraceID...)
		exemplar.SpanID = append([]byte(nil), exemplar.SpanID...)
		result[index] = exemplar
	}
	return result
}
