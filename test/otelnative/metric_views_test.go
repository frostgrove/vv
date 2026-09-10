package otelnative

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestTransportMetricViewsRejectValuesBeforeAggregation(t *testing.T) {
	routes, err := NewRouteTable(Route{Method: "GET", Pattern: "/items/{id}"})
	if err != nil {
		t.Fatal(err)
	}
	rpcs, err := NewRPCTable("/echo.Echo/Call")
	if err != nil {
		t.Fatal(err)
	}
	policy := TraceProjectionPolicy{RouterTables: []RouteTable{routes}, RPCMethods: rpcs}
	reader := sdkmetric.NewManualReader()
	options := []sdkmetric.Option{sdkmetric.WithReader(reader)}
	options = append(options, TransportMetricOptions(policy)...)
	provider := sdkmetric.NewMeterProvider(options...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			t.Errorf("metric provider shutdown: %v", err)
		}
	})

	httpMeter := provider.Meter(otelhttp.ScopeName, metric.WithInstrumentationVersion(otelhttp.Version))
	httpDuration, err := httpMeter.Float64Histogram("http.server.request.duration")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 100; index++ {
		httpDuration.Record(t.Context(), 1,
			metric.WithAttributes(
				attribute.String("http.request.method", fmt.Sprintf("SECRET-%d", index)),
				attribute.String("http.route", fmt.Sprintf("/items/%d", index)),
				attribute.Int("http.response.status_code", 700+index),
				attribute.String("rpc.method", fmt.Sprintf("secret/%d", index)),
			),
		)
		httpDuration.Record(t.Context(), 1,
			metric.WithAttributes(
				attribute.String("http.request.method", "GET"),
				attribute.String("http.route", fmt.Sprintf("/items/%d", index)),
				attribute.Int("http.response.status_code", 700+index),
			),
		)
	}
	httpDuration.Record(t.Context(), 1, metric.WithAttributes(
		attribute.String("http.request.method", "GET"),
		attribute.String("http.route", "/items/{id}"),
		attribute.Int("http.response.status_code", 200),
	))
	httpDuration.Record(t.Context(), 1, metric.WithAttributes(attribute.String("http.request.method", "_OTHER")))

	rpcMeter := provider.Meter(otelgrpc.ScopeName, metric.WithInstrumentationVersion(otelgrpc.Version))
	rpcDuration, err := rpcMeter.Float64Histogram("rpc.server.call.duration")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 100; index++ {
		rpcDuration.Record(t.Context(), 1, metric.WithAttributes(
			attribute.String("rpc.system.name", "grpc"),
			attribute.String("rpc.method", fmt.Sprintf("secret.Service/Call%d", index)),
			attribute.String("rpc.response.status_code", fmt.Sprintf("SECRET_%d", index)),
			attribute.String("http.route", fmt.Sprintf("/secret/%d", index)),
		))
	}
	rpcDuration.Record(t.Context(), 1, metric.WithAttributes(
		attribute.String("rpc.system.name", "grpc"),
		attribute.String("rpc.method", "echo.Echo/Call"),
		attribute.String("rpc.response.status_code", "OK"),
	))
	rpcDuration.Record(t.Context(), 1, metric.WithAttributes(
		attribute.String("rpc.system.name", "grpc"),
		attribute.String("rpc.method", fallbackRPCName),
		attribute.String("rpc.response.status_code", "UNKNOWN"),
	))

	var exported metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &exported); err != nil {
		t.Fatal(err)
	}
	httpPoints := metricHistogramPoints(t, exported, otelhttp.ScopeName, "http.server.request.duration")
	if len(httpPoints) != 4 {
		t.Fatalf("HTTP series = %d, want four bounded projections", len(httpPoints))
	}
	for _, point := range httpPoints {
		for _, item := range point.Attributes.ToSlice() {
			if !transportMetricFilter(otelhttp.ScopeName, httpServerMetricAttributes, compileTracePolicy(policy))(item) {
				t.Fatalf("HTTP view retained invalid attribute %v", item)
			}
		}
	}
	rpcPoints := metricHistogramPoints(t, exported, otelgrpc.ScopeName, "rpc.server.call.duration")
	if len(rpcPoints) != 3 {
		t.Fatalf("gRPC series = %d, want three bounded projections", len(rpcPoints))
	}
	for _, point := range rpcPoints {
		for _, item := range point.Attributes.ToSlice() {
			if !transportMetricFilter(otelgrpc.ScopeName, rpcMetricAttributes, compileTracePolicy(policy))(item) {
				t.Fatalf("gRPC view retained invalid attribute %v", item)
			}
		}
	}
}

func metricHistogramPoints(t *testing.T, exported metricdata.ResourceMetrics, scopeName, metricName string) []metricdata.HistogramDataPoint[float64] {
	t.Helper()
	for _, scope := range exported.ScopeMetrics {
		if scope.Scope.Name != scopeName {
			continue
		}
		for _, measurement := range scope.Metrics {
			if measurement.Name != metricName {
				continue
			}
			histogram, ok := measurement.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("metric %s data = %T", metricName, measurement.Data)
			}
			return histogram.DataPoints
		}
	}
	t.Fatalf("metric %s@%s not found", metricName, scopeName)
	return nil
}
