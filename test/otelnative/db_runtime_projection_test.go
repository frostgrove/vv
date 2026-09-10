package otelnative

import (
	"context"
	"testing"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/semconv/v1.41.0/goconv"
)

func TestDatabaseMetricProjectionPinsEveryFieldPerInstrumentWithoutMutation(t *testing.T) {
	pool := mustDatabasePoolName(t, "primary")
	traceID := []byte{1, 2, 3}
	bounds := []float64{0.1, 1}
	buckets := []uint64{1, 0, 0}
	original := metricdata.ResourceMetrics{
		Resource: resource.NewSchemaless(
			attribute.String("service.name", databaseServiceName),
			attribute.String("secret.resource", "database-resource-secret-73152"),
		),
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Scope: instrumentation.Scope{
				Name:       sqlScopeName,
				Version:    "0.43.0",
				Attributes: attribute.NewSet(attribute.String("secret.scope", "database-scope-secret-81523")),
			},
			Metrics: []metricdata.Metrics{
				{
					Name:        "db.client.operation.duration",
					Description: "database-description-secret-13285",
					Unit:        "database-unit-secret-95412",
					Data: metricdata.Histogram[float64]{Temporality: metricdata.CumulativeTemporality, DataPoints: []metricdata.HistogramDataPoint[float64]{
						{
							Attributes: attribute.NewSet(
								attribute.String("db.operation.name", "sql.conn.query"),
								attribute.String(databasePoolKey, pool.String()),
								attribute.String("status", "idle"),
								attribute.String("secret.attribute", "database-attribute-secret-38471"),
							),
							Bounds:       bounds,
							BucketCounts: buckets,
							Exemplars: []metricdata.Exemplar[float64]{
								{
									FilteredAttributes: []attribute.KeyValue{
										attribute.String("db.operation.name", "sql.conn.query"),
										attribute.String("secret.exemplar", "database-exemplar-secret-52814"),
									},
									TraceID: traceID,
								},
							},
						},
					}},
				},
				{Name: "db.client.operation.duration", Description: "undefined-temporality-secret", Data: metricdata.Histogram[float64]{}},
				{Name: "db.client.operation.duration", Description: "delta-temporality-secret", Data: metricdata.Histogram[float64]{Temporality: metricdata.DeltaTemporality}},
				{Name: "db.client.operation.duration", Description: "wrong-shape-secret-41782", Data: metricdata.Sum[int64]{IsMonotonic: true}},
				{
					Name:        "db.sql.connection.max_open",
					Description: "pool-description-secret-51842",
					Unit:        "pool-unit-secret-61935",
					Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{
						{Attributes: attribute.NewSet(
							attribute.String(databasePoolKey, pool.String()),
							attribute.String("status", "idle"),
							attribute.String("db.operation.name", "sql.conn.query"),
						)},
					}},
				},
				{Name: "db.secret.metric", Data: metricdata.Gauge[int64]{}},
			},
		}},
	}
	sink := &metricCapture{}
	exporter, err := NewDatabaseMetricExporter(sink, DatabaseProjectionPolicy{
		PoolNames:          []DatabasePoolName{pool},
		ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", databaseServiceName)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := exporter.Export(context.Background(), &original); err != nil {
		t.Fatal(err)
	}
	exported := sink.snapshot()
	if len(exported.ScopeMetrics) != 1 || len(exported.ScopeMetrics[0].Metrics) != 2 {
		t.Fatalf("projected database roster = %#v", exported.ScopeMetrics)
	}
	duration := findNativeMetric(t, exported, sqlScopeName, "db.client.operation.duration")
	if duration.Description != "Duration of database client operations." || duration.Unit != "s" {
		t.Fatalf("duration metadata = %q/%q", duration.Description, duration.Unit)
	}
	histogram := duration.Data.(metricdata.Histogram[float64])
	if got := histogram.DataPoints[0].Attributes.ToSlice(); len(got) != 1 || got[0].Key != "db.operation.name" {
		t.Fatalf("duration attributes = %v", got)
	}
	if got := histogram.DataPoints[0].Exemplars[0].FilteredAttributes; len(got) != 1 || got[0].Key != "db.operation.name" {
		t.Fatalf("duration exemplar attributes = %v", got)
	}
	poolMetric := findNativeMetric(t, exported, sqlScopeName, "db.sql.connection.max_open")
	if poolMetric.Description != "Maximum number of open connections to the database" || poolMetric.Unit != "" {
		t.Fatalf("pool metadata = %q/%q", poolMetric.Description, poolMetric.Unit)
	}
	if got := poolMetric.Data.(metricdata.Gauge[int64]).DataPoints[0].Attributes.ToSlice(); len(got) != 1 || got[0].Key != databasePoolKey {
		t.Fatalf("pool attributes = %v", got)
	}
	assertNativePrivacy(t, nil, exported,
		"database-resource-secret-73152",
		"database-scope-secret-81523",
		"database-description-secret-13285",
		"database-unit-secret-95412",
		"database-attribute-secret-38471",
		"database-exemplar-secret-52814",
		"undefined-temporality-secret",
		"delta-temporality-secret",
		"wrong-shape-secret-41782",
		"pool-description-secret-51842",
		"pool-unit-secret-61935",
		"db.secret.metric",
	)
	histogram.DataPoints[0].Bounds[0] = 99
	histogram.DataPoints[0].BucketCounts[0] = 99
	histogram.DataPoints[0].Exemplars[0].TraceID[0] = 99
	originalHistogram := original.ScopeMetrics[0].Metrics[0].Data.(metricdata.Histogram[float64])
	if originalHistogram.DataPoints[0].Bounds[0] != 0.1 || originalHistogram.DataPoints[0].BucketCounts[0] != 1 || originalHistogram.DataPoints[0].Exemplars[0].TraceID[0] != 1 {
		t.Fatal("database projection mutated or aliased SDK-owned input")
	}
}

func TestRuntimeMetricProjectionPinsMetadataShapeAndAttributeOwnership(t *testing.T) {
	secret := "runtime-projection-secret-81592"
	original := metricdata.ResourceMetrics{
		Resource: resource.NewSchemaless(
			attribute.String("service.name", runtimeServiceName),
			attribute.String("secret.resource", secret),
		),
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Scope: instrumentation.Scope{Name: otelruntime.ScopeName, Version: otelruntime.Version},
			Metrics: []metricdata.Metrics{
				{
					Name:        goconv.MemoryUsed{}.Name(),
					Description: secret,
					Unit:        secret,
					Data: metricdata.Sum[int64]{Temporality: metricdata.CumulativeTemporality, DataPoints: []metricdata.DataPoint[int64]{
						{Attributes: attribute.NewSet(attribute.String("go.memory.type", "stack"), attribute.String("secret.attribute", secret))},
					}},
				},
				{
					Name: goconv.MemoryLimit{}.Name(),
					Data: metricdata.Sum[int64]{Temporality: metricdata.CumulativeTemporality, DataPoints: []metricdata.DataPoint[int64]{
						{Attributes: attribute.NewSet(attribute.String("go.memory.type", "stack"))},
					}},
				},
				{Name: goconv.MemoryUsed{}.Name(), Description: "undefined-temporality-secret", Data: metricdata.Sum[int64]{}},
				{Name: goconv.MemoryUsed{}.Name(), Description: "delta-temporality-secret", Data: metricdata.Sum[int64]{Temporality: metricdata.DeltaTemporality}},
				{Name: goconv.MemoryAllocated{}.Name(), Data: metricdata.Sum[int64]{IsMonotonic: false}},
			},
		}},
	}
	sink := &metricCapture{}
	exporter, err := NewRuntimeMetricExporter(sink, RuntimeProjectionPolicy{
		ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", runtimeServiceName)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := exporter.Export(context.Background(), &original); err != nil {
		t.Fatal(err)
	}
	exported := sink.snapshot()
	if len(exported.ScopeMetrics) != 1 || len(exported.ScopeMetrics[0].Metrics) != 2 {
		t.Fatalf("runtime metric roster = %#v", exported.ScopeMetrics)
	}
	used := findNativeMetric(t, exported, otelruntime.ScopeName, (goconv.MemoryUsed{}).Name())
	if used.Description != (goconv.MemoryUsed{}).Description() || used.Unit != (goconv.MemoryUsed{}).Unit() {
		t.Fatalf("memory used metadata = %q/%q", used.Description, used.Unit)
	}
	usedAttributes := used.Data.(metricdata.Sum[int64]).DataPoints[0].Attributes.ToSlice()
	if len(usedAttributes) != 1 || usedAttributes[0].Key != "go.memory.type" || usedAttributes[0].Value.AsString() != "stack" {
		t.Fatalf("memory used attributes = %v", usedAttributes)
	}
	limit := findNativeMetric(t, exported, otelruntime.ScopeName, (goconv.MemoryLimit{}).Name())
	if limit.Data.(metricdata.Sum[int64]).DataPoints[0].Attributes.Len() != 0 {
		t.Fatalf("memory limit accepted memory-used attributes: %#v", limit.Data)
	}
	assertNativePrivacy(t, nil, exported, secret, "undefined-temporality-secret", "delta-temporality-secret")
}

func findNativeMetric(t *testing.T, metrics metricdata.ResourceMetrics, scopeName, name string) metricdata.Metrics {
	t.Helper()
	for _, scoped := range metrics.ScopeMetrics {
		if scoped.Scope.Name != scopeName {
			continue
		}
		for _, measurement := range scoped.Metrics {
			if measurement.Name == name {
				return measurement
			}
		}
	}
	t.Fatalf("metric %q in scope %q not found", name, scopeName)
	return metricdata.Metrics{}
}
