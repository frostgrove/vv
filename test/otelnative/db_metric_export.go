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
	pgxOperations = map[string]struct{}{
		"query": {}, "copy": {}, "batch": {}, "connect": {}, "prepare": {}, "acquire": {},
	}
	sqlOperations = map[string]struct{}{
		"sql.connector.connect": {}, "sql.conn.ping": {}, "sql.conn.exec": {}, "sql.conn.query": {},
		"sql.conn.prepare": {}, "sql.conn.begin_tx": {}, "sql.conn.reset_session": {},
		"sql.tx.commit": {}, "sql.tx.rollback": {}, "sql.stmt.exec": {}, "sql.stmt.query": {}, "sql.rows": {},
	}
)

type metricShape uint8

const (
	metricGaugeInt64 metricShape = iota + 1
	metricSumInt64
	metricSumFloat64
	metricHistogramInt64
	metricHistogramFloat64
)

type nativeMetricSpec struct {
	description string
	unit        string
	shape       metricShape
	monotonic   bool
	attributes  func([]attribute.KeyValue) []attribute.KeyValue
}

type metricScopeProjection struct {
	name    string
	version string
	metrics map[string]nativeMetricSpec
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
	options := make([]sdkmetric.Option, 0, 15)
	for _, policy := range scopes {
		for name, spec := range policy.metrics {
			filter := func(item attribute.KeyValue) bool {
				projected := spec.attributes([]attribute.KeyValue{item})
				return len(projected) == 1 && projected[0] == item
			}
			options = append(options, sdkmetric.WithView(sdkmetric.NewView(
				sdkmetric.Instrument{
					Name:  name,
					Scope: instrumentation.Scope{Name: policy.name},
				},
				sdkmetric.Stream{AttributeFilter: filter},
			)))
		}
	}
	return options
}

func databaseMetricScopes(pools map[string]struct{}) map[string]metricScopeProjection {
	return map[string]metricScopeProjection{
		pgxScopeName: {
			name:    pgxScopeName,
			version: pgxScopeVersion,
			metrics: map[string]nativeMetricSpec{
				"db.client.operation.duration": {
					description: "Duration of database client operations.",
					unit:        "s",
					shape:       metricHistogramFloat64,
					attributes:  projectPGXMetricAttributes,
				},
				"db.client.operation.errors": {
					description: "The count of database client operation errors",
					shape:       metricSumInt64,
					monotonic:   true,
					attributes:  projectPGXMetricAttributes,
				},
			},
		},
		sqlScopeName: {
			name:    sqlScopeName,
			version: "0.43.0",
			metrics: map[string]nativeMetricSpec{
				"db.client.operation.duration": {
					description: "Duration of database client operations.",
					unit:        "s",
					shape:       metricHistogramFloat64,
					attributes:  projectSQLDurationAttributes,
				},
				"db.sql.connection.max_open": {
					description: "Maximum number of open connections to the database",
					shape:       metricGaugeInt64,
					attributes:  poolAttributeProjector(pools, false),
				},
				"db.sql.connection.open": {
					description: "The number of established connections both in use and idle",
					shape:       metricGaugeInt64,
					attributes:  poolAttributeProjector(pools, true),
				},
				"db.sql.connection.wait": {
					description: "The total number of connections waited for",
					shape:       metricSumInt64,
					monotonic:   true,
					attributes:  poolAttributeProjector(pools, false),
				},
				"db.sql.connection.wait_duration": {
					description: "The total time blocked waiting for a new connection",
					unit:        "ms",
					shape:       metricSumFloat64,
					monotonic:   true,
					attributes:  poolAttributeProjector(pools, false),
				},
				"db.sql.connection.closed_max_idle": {
					description: "The total number of connections closed due to SetMaxIdleConns",
					shape:       metricSumInt64,
					monotonic:   true,
					attributes:  poolAttributeProjector(pools, false),
				},
				"db.sql.connection.closed_max_idle_time": {
					description: "The total number of connections closed due to SetConnMaxIdleTime",
					shape:       metricSumInt64,
					monotonic:   true,
					attributes:  poolAttributeProjector(pools, false),
				},
				"db.sql.connection.closed_max_lifetime": {
					description: "The total number of connections closed due to SetConnMaxLifetime",
					shape:       metricSumInt64,
					monotonic:   true,
					attributes:  poolAttributeProjector(pools, false),
				},
			},
		},
		pgxPoolScopeName: {
			name:    pgxPoolScopeName,
			version: pgxPoolVersion,
			metrics: map[string]nativeMetricSpec{
				pgxPoolAcquiredMetric: {
					description: pgxPoolAcquiredDescription,
					unit:        "{connection}",
					shape:       metricGaugeInt64,
					attributes:  poolAttributeProjector(pools, false),
				},
				pgxPoolIdleMetric: {
					description: pgxPoolIdleDescription,
					unit:        "{connection}",
					shape:       metricGaugeInt64,
					attributes:  poolAttributeProjector(pools, false),
				},
				pgxPoolMaximumMetric: {
					description: pgxPoolMaximumDescription,
					unit:        "{connection}",
					shape:       metricGaugeInt64,
					attributes:  poolAttributeProjector(pools, false),
				},
				pgxPoolAcquireWaitMetric: {
					description: pgxPoolAcquireWaitDescription,
					unit:        "{wait}",
					shape:       metricSumInt64,
					monotonic:   true,
					attributes:  poolAttributeProjector(pools, false),
				},
				pgxPoolAcquireTimeMetric: {
					description: pgxPoolAcquireTimeDescription,
					unit:        "s",
					shape:       metricSumFloat64,
					monotonic:   true,
					attributes:  poolAttributeProjector(pools, false),
				},
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

func projectSQLDurationAttributes(items []attribute.KeyValue) []attribute.KeyValue {
	projected := make([]attribute.KeyValue, 0, len(items))
	for _, item := range items {
		if string(item.Key) == "db.operation.name" && item.Value.Type() == attribute.STRING {
			if _, allowed := sqlOperations[item.Value.AsString()]; allowed {
				projected = append(projected, item)
			}
		}
	}
	return projected
}

func poolAttributeProjector(pools map[string]struct{}, includeStatus bool) func([]attribute.KeyValue) []attribute.KeyValue {
	return func(items []attribute.KeyValue) []attribute.KeyValue {
		projected := make([]attribute.KeyValue, 0, 2)
		for _, item := range items {
			switch string(item.Key) {
			case databasePoolKey:
				if item.Value.Type() == attribute.STRING {
					if _, allowed := pools[item.Value.AsString()]; allowed {
						projected = append(projected, item)
					}
				}
			case "status":
				if includeStatus && item.Value.Type() == attribute.STRING && (item.Value.AsString() == "inuse" || item.Value.AsString() == "idle") {
					projected = append(projected, item)
				}
			}
		}
		return projected
	}
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
			spec, allowed := policy.metrics[measurement.Name]
			if !allowed {
				continue
			}
			data, ok := projectBoundedAggregation(measurement.Data, spec)
			if !ok {
				continue
			}
			measurements = append(measurements, metricdata.Metrics{
				Name:        measurement.Name,
				Description: spec.description,
				Unit:        spec.unit,
				Data:        data,
			})
		}
		if len(measurements) > 0 {
			projected.ScopeMetrics = append(projected.ScopeMetrics, metricdata.ScopeMetrics{Scope: scope, Metrics: measurements})
		}
	}
	return projected
}

func projectBoundedAggregation(aggregation metricdata.Aggregation, spec nativeMetricSpec) (metricdata.Aggregation, bool) {
	switch spec.shape {
	case metricGaugeInt64:
		data, ok := aggregation.(metricdata.Gauge[int64])
		if !ok {
			return nil, false
		}
		data.DataPoints = projectBoundedDataPoints(data.DataPoints, spec.attributes)
		return data, true
	case metricSumInt64:
		data, ok := aggregation.(metricdata.Sum[int64])
		if !ok || data.IsMonotonic != spec.monotonic {
			return nil, false
		}
		data.DataPoints = projectBoundedDataPoints(data.DataPoints, spec.attributes)
		return data, true
	case metricSumFloat64:
		data, ok := aggregation.(metricdata.Sum[float64])
		if !ok || data.IsMonotonic != spec.monotonic {
			return nil, false
		}
		data.DataPoints = projectBoundedDataPoints(data.DataPoints, spec.attributes)
		return data, true
	case metricHistogramFloat64:
		data, ok := aggregation.(metricdata.Histogram[float64])
		if !ok {
			return nil, false
		}
		data.DataPoints = projectBoundedHistogramPoints(data.DataPoints, spec.attributes)
		return data, true
	case metricHistogramInt64:
		data, ok := aggregation.(metricdata.Histogram[int64])
		if !ok {
			return nil, false
		}
		data.DataPoints = projectBoundedHistogramPoints(data.DataPoints, spec.attributes)
		return data, true
	}
	return nil, false
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
