package otelnative

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const databaseServiceName = "database-fixture"

type databaseTelemetryFixture struct {
	providers  Providers
	spans      *tracetest.InMemoryExporter
	metrics    *metricCapture
	rawMetrics *metricCapture
	traces     *sdktrace.TracerProvider
	meters     *metric.MeterProvider
}

func newDatabaseTelemetryFixture(t *testing.T, pools []DatabasePoolName, sampler sdktrace.Sampler) *databaseTelemetryFixture {
	t.Helper()
	policy := DatabaseProjectionPolicy{
		PoolNames: pools,
		ResourceAttributes: []attribute.KeyValue{
			attribute.String("service.name", databaseServiceName),
		},
	}
	spanSink := tracetest.NewInMemoryExporter()
	spanExporter, err := NewDatabaseSpanExporter(spanSink, policy)
	if err != nil {
		t.Fatal(err)
	}
	telemetryResource := resource.NewSchemaless(
		attribute.String("service.name", databaseServiceName),
		attribute.String("secret.resource", secretResource),
	)
	traceOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithSyncer(spanExporter),
		sdktrace.WithResource(telemetryResource),
	}
	if sampler != nil {
		traceOptions = append(traceOptions, sdktrace.WithSampler(sampler))
	}
	tracerProvider := sdktrace.NewTracerProvider(traceOptions...)
	metricSink := &metricCapture{}
	metricExporter, err := NewDatabaseMetricExporter(metricSink, policy)
	if err != nil {
		t.Fatal(err)
	}
	rawMetricSink := &metricCapture{}
	reader := metric.NewPeriodicReader(&nativeRawMetricTap{next: metricExporter, capture: rawMetricSink})
	metricOptions := []metric.Option{
		metric.WithReader(reader),
		metric.WithResource(telemetryResource),
	}
	metricOptions = append(metricOptions, DatabaseMetricOptions(pools...)...)
	meterProvider := metric.NewMeterProvider(metricOptions...)
	fixture := &databaseTelemetryFixture{
		providers:  Providers{Tracer: tracerProvider, Meter: meterProvider},
		spans:      spanSink,
		metrics:    metricSink,
		rawMetrics: rawMetricSink,
		traces:     tracerProvider,
		meters:     meterProvider,
	}
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(context.Background())
		_ = tracerProvider.Shutdown(context.Background())
	})
	return fixture
}

func (f *databaseTelemetryFixture) databaseSpans() tracetest.SpanStubs {
	all := f.spans.GetSpans()
	spans := make(tracetest.SpanStubs, 0, len(all))
	for _, span := range all {
		if _, known := databaseScope(span.InstrumentationScope); known {
			spans = append(spans, span)
		}
	}
	return spans
}

func (f *databaseTelemetryFixture) flushMetrics(t *testing.T) {
	t.Helper()
	if err := f.meters.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func hasMetric(metrics *metricCapture, scope, name string) bool {
	for _, scoped := range metrics.snapshot().ScopeMetrics {
		if scoped.Scope.Name != scope {
			continue
		}
		for _, measurement := range scoped.Metrics {
			if measurement.Name == name {
				return true
			}
		}
	}
	return false
}
