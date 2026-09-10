package otelnative

import (
	"context"
	"fmt"
	"testing"
	"time"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestDatabaseAndRuntimeViewsOwnValuesPerInstrumentBeforeAggregation(t *testing.T) {
	pool := mustDatabasePoolName(t, "primary")
	reader := sdkmetric.NewManualReader(sdkmetric.WithAggregationSelector(exponentialHistogramSelector))
	options := []sdkmetric.Option{sdkmetric.WithReader(reader)}
	databaseViews, err := DatabaseMetricOptions(pool)
	if err != nil {
		t.Fatal(err)
	}
	options = append(options, databaseViews...)
	options = append(options, RuntimeMetricOptions()...)
	provider := sdkmetric.NewMeterProvider(options...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			t.Errorf("metric provider shutdown: %v", err)
		}
	})

	sqlMeter := provider.Meter(sqlScopeName, metric.WithInstrumentationVersion("0.43.0"))
	duration, err := sqlMeter.Float64Histogram("db.client.operation.duration")
	if err != nil {
		t.Fatal(err)
	}
	maximum, err := sqlMeter.Int64Gauge("db.sql.connection.max_open")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 100; index++ {
		duration.Record(t.Context(), 1, metric.WithAttributes(
			attribute.String("db.operation.name", "sql.conn.query"),
			attribute.String(databasePoolKey, pool.String()),
			attribute.String("status", fmt.Sprintf("secret-%d", index)),
		))
		duration.Record(t.Context(), 1, metric.WithAttributes(
			attribute.String("db.operation.name", fmt.Sprintf("secret-operation-%d", index)),
			attribute.String(databasePoolKey, fmt.Sprintf("secret-pool-%d", index)),
		))
		maximum.Record(t.Context(), 1, metric.WithAttributes(
			attribute.String(databasePoolKey, pool.String()),
			attribute.String("status", fmt.Sprintf("secret-%d", index)),
			attribute.String("db.operation.name", fmt.Sprintf("secret-operation-%d", index)),
		))
		maximum.Record(t.Context(), 1, metric.WithAttributes(attribute.String(databasePoolKey, fmt.Sprintf("secret-pool-%d", index))))
	}

	runtimeMeter := provider.Meter(otelruntime.ScopeName, metric.WithInstrumentationVersion(otelruntime.Version))
	memoryUsed, err := runtimeMeter.Int64UpDownCounter("go.memory.used")
	if err != nil {
		t.Fatal(err)
	}
	memoryLimit, err := runtimeMeter.Int64UpDownCounter("go.memory.limit")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 100; index++ {
		memoryUsed.Add(t.Context(), 1, metric.WithAttributes(attribute.String("go.memory.type", fmt.Sprintf("secret-%d", index))))
		memoryLimit.Add(t.Context(), 1, metric.WithAttributes(attribute.String("go.memory.type", fmt.Sprintf("secret-%d", index))))
	}
	memoryUsed.Add(t.Context(), 1, metric.WithAttributes(attribute.String("go.memory.type", "other")))
	memoryUsed.Add(t.Context(), 1, metric.WithAttributes(attribute.String("go.memory.type", "stack")))
	memoryLimit.Add(t.Context(), 1, metric.WithAttributes(attribute.String("go.memory.type", "stack")))

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &collected); err != nil {
		t.Fatal(err)
	}
	durationPoints := findNativeMetric(t, collected, sqlScopeName, "db.client.operation.duration").Data.(metricdata.Histogram[float64]).DataPoints
	if len(durationPoints) != 2 {
		t.Fatalf("SQL duration series = %d, want operation and absent", len(durationPoints))
	}
	for _, point := range durationPoints {
		for _, item := range point.Attributes.ToSlice() {
			if item.Key != "db.operation.name" {
				t.Fatalf("SQL duration retained cross-instrument attribute %v", item)
			}
		}
	}
	maximumPoints := findNativeMetric(t, collected, sqlScopeName, "db.sql.connection.max_open").Data.(metricdata.Gauge[int64]).DataPoints
	if len(maximumPoints) != 2 {
		t.Fatalf("SQL max-open series = %d, want pool and absent", len(maximumPoints))
	}
	for _, point := range maximumPoints {
		for _, item := range point.Attributes.ToSlice() {
			if item.Key != databasePoolKey {
				t.Fatalf("SQL max-open retained cross-instrument attribute %v", item)
			}
		}
	}
	usedPoints := findNativeMetric(t, collected, otelruntime.ScopeName, "go.memory.used").Data.(metricdata.Sum[int64]).DataPoints
	if len(usedPoints) != 3 {
		t.Fatalf("runtime memory-used series = %d, want other, stack and absent", len(usedPoints))
	}
	limitPoints := findNativeMetric(t, collected, otelruntime.ScopeName, "go.memory.limit").Data.(metricdata.Sum[int64]).DataPoints
	if len(limitPoints) != 1 || limitPoints[0].Attributes.Len() != 0 {
		t.Fatalf("runtime memory-limit series = %#v", limitPoints)
	}
}
