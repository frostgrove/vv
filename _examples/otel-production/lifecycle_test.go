package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vvotel "github.com/frostgrove/vv/otel"
	otelglobal "go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type lifecycleProviderStub struct {
	flushCalls    atomic.Int64
	shutdownCalls atomic.Int64
	flushErr      error
	shutdownErr   error
	flushEntered  chan struct{}
	flushRelease  chan struct{}
	closeEntered  chan struct{}
	closeRelease  chan struct{}
	flushOnce     sync.Once
	closeOnce     sync.Once
}

type spanExporterStub struct {
	shutdownCalls atomic.Int64
	shutdownErr   error
}

func (*spanExporterStub) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }

func (exporter *spanExporterStub) Shutdown(ctx context.Context) error {
	exporter.shutdownCalls.Add(1)
	exporter.shutdownErr = ctx.Err()
	return nil
}

type metricExporterStub struct {
	flushCalls    atomic.Int64
	shutdownCalls atomic.Int64
}

func (*metricExporterStub) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (*metricExporterStub) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

func (*metricExporterStub) Export(context.Context, *metricdata.ResourceMetrics) error { return nil }

func (exporter *metricExporterStub) ForceFlush(context.Context) error {
	exporter.flushCalls.Add(1)
	return nil
}

func (exporter *metricExporterStub) Shutdown(context.Context) error {
	exporter.shutdownCalls.Add(1)
	return nil
}

func (provider *lifecycleProviderStub) ForceFlush(ctx context.Context) error {
	provider.flushCalls.Add(1)
	if provider.flushEntered != nil {
		provider.flushOnce.Do(func() { close(provider.flushEntered) })
	}
	if provider.flushRelease != nil {
		select {
		case <-provider.flushRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return provider.flushErr
}

func (provider *lifecycleProviderStub) Shutdown(ctx context.Context) error {
	provider.shutdownCalls.Add(1)
	if provider.closeEntered != nil {
		provider.closeOnce.Do(func() { close(provider.closeEntered) })
	}
	if provider.closeRelease != nil {
		select {
		case <-provider.closeRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return provider.shutdownErr
}

func TestTelemetryLifecycleFlushAttemptsBothProviders(t *testing.T) {
	traceErr := errors.New("trace flush")
	metricErr := errors.New("metric flush")
	traceProvider := &lifecycleProviderStub{flushErr: traceErr}
	metricProvider := &lifecycleProviderStub{flushErr: metricErr}
	lifecycle := newTelemetryLifecycle(traceProvider, metricProvider, time.Second, time.Second)

	err := lifecycle.ForceFlush(t.Context())
	if !errors.Is(err, traceErr) || !errors.Is(err, metricErr) {
		t.Fatalf("ForceFlush error = %v", err)
	}
	if traceProvider.flushCalls.Load() != 1 || metricProvider.flushCalls.Load() != 1 {
		t.Fatalf("flush calls = %d/%d", traceProvider.flushCalls.Load(), metricProvider.flushCalls.Load())
	}
}

func TestTelemetryLifecycleShutdownWaitsForAdmittedFlush(t *testing.T) {
	flushEntered := make(chan struct{})
	flushRelease := make(chan struct{})
	traceProvider := &lifecycleProviderStub{flushEntered: flushEntered, flushRelease: flushRelease}
	metricProvider := &lifecycleProviderStub{}
	lifecycle := newTelemetryLifecycle(traceProvider, metricProvider, time.Second, time.Second)

	flushDone := make(chan error, 1)
	go func() { flushDone <- lifecycle.ForceFlush(t.Context()) }()
	<-flushEntered
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- lifecycle.Shutdown(t.Context()) }()

	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before flush: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if traceProvider.shutdownCalls.Load() != 0 || metricProvider.shutdownCalls.Load() != 0 {
		t.Fatal("provider shutdown began before admitted flush completed")
	}
	if err := lifecycle.ForceFlush(t.Context()); !errors.Is(err, ErrTelemetryClosed) {
		t.Fatalf("ForceFlush during shutdown = %v", err)
	}
	close(flushRelease)
	if err := <-flushDone; err != nil {
		t.Fatalf("admitted ForceFlush: %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if traceProvider.shutdownCalls.Load() != 1 || metricProvider.shutdownCalls.Load() != 1 {
		t.Fatalf("shutdown calls = %d/%d", traceProvider.shutdownCalls.Load(), metricProvider.shutdownCalls.Load())
	}
}

func TestTelemetryLifecycleConcurrentShutdownCachesOneResult(t *testing.T) {
	traceErr := errors.New("trace shutdown")
	metricErr := errors.New("metric shutdown")
	closeEntered := make(chan struct{})
	closeRelease := make(chan struct{})
	traceProvider := &lifecycleProviderStub{shutdownErr: traceErr, closeEntered: closeEntered, closeRelease: closeRelease}
	metricProvider := &lifecycleProviderStub{shutdownErr: metricErr}
	lifecycle := newTelemetryLifecycle(traceProvider, metricProvider, time.Second, time.Second)

	ownerDone := make(chan error, 1)
	go func() { ownerDone <- lifecycle.Shutdown(t.Context()) }()
	<-closeEntered
	waitCtx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := lifecycle.Shutdown(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter = %v", err)
	}
	close(closeRelease)
	ownerErr := <-ownerDone
	if !errors.Is(ownerErr, traceErr) || !errors.Is(ownerErr, metricErr) {
		t.Fatalf("owner error = %v", ownerErr)
	}
	lateErr := lifecycle.Shutdown(waitCtx)
	if lateErr != ownerErr {
		t.Fatalf("late result identity changed: %p != %p", lateErr, ownerErr)
	}
	if traceProvider.shutdownCalls.Load() != 1 || metricProvider.shutdownCalls.Load() != 1 {
		t.Fatalf("shutdown calls = %d/%d", traceProvider.shutdownCalls.Load(), metricProvider.shutdownCalls.Load())
	}
}

func TestTelemetryConfigDefaultsAndBounds(t *testing.T) {
	base := Config{
		ServiceName:       "orders-api",
		ServiceVersion:    "1.0.0",
		ServiceNamespace:  "commerce",
		FrameworkResource: "orders",
	}
	normalized, err := normalizeTelemetryConfig(base)
	if err != nil {
		t.Fatalf("normalize defaults: %v", err)
	}
	if normalized.SampleRatio != 0.1 || normalized.BatchQueueSize != 2048 || normalized.BatchSize != 512 {
		t.Fatalf("unexpected defaults: %#v", normalized)
	}
	invalid := base
	invalid.BatchSize = 2
	invalid.BatchQueueSize = 1
	if _, err := normalizeTelemetryConfig(invalid); !errors.Is(err, ErrInvalidTelemetryConfig) {
		t.Fatalf("invalid bounds = %v", err)
	}
}

func TestNewTelemetryRollsBackDetachedTraceExporter(t *testing.T) {
	traceExporter := &spanExporterStub{}
	wantErr := errors.New("metric exporter failed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newTelemetry(ctx, validTelemetryConfig(), telemetryFactories{
		resource: func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
		traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
			return traceExporter, nil
		},
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) { return nil, wantErr },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("NewTelemetry error = %v", err)
	}
	if traceExporter.shutdownCalls.Load() != 1 || traceExporter.shutdownErr != nil {
		t.Fatalf("detached trace cleanup = %d, context error %v", traceExporter.shutdownCalls.Load(), traceExporter.shutdownErr)
	}
}

func TestNewTelemetryOwnsExplicitProvidersWithoutChangingGlobals(t *testing.T) {
	globalTracer := otelglobal.GetTracerProvider()
	globalMeter := otelglobal.GetMeterProvider()
	traceExporter := &spanExporterStub{}
	metricExporter := &metricExporterStub{}
	telemetry, err := newTelemetry(t.Context(), validTelemetryConfig(), telemetryFactories{
		resource: func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
		traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
			return traceExporter, nil
		},
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
			return metricExporter, nil
		},
	})
	if err != nil {
		t.Fatalf("NewTelemetry: %v", err)
	}
	if telemetry.TracerProvider == nil || telemetry.MeterProvider == nil || telemetry.Frostgrove == nil || telemetry.Propagator == nil {
		t.Fatalf("incomplete telemetry assembly: %#v", telemetry)
	}
	if otelglobal.GetTracerProvider() != globalTracer || otelglobal.GetMeterProvider() != globalMeter {
		t.Fatal("application recipe changed an OpenTelemetry global provider")
	}
	if err := telemetry.ForceFlush(t.Context()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	if err := telemetry.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if traceExporter.shutdownCalls.Load() != 1 || metricExporter.shutdownCalls.Load() != 1 {
		t.Fatalf("exporter shutdown calls = %d/%d", traceExporter.shutdownCalls.Load(), metricExporter.shutdownCalls.Load())
	}
}

func TestTelemetryForceFlushExportsBufferedFrameworkSignalsBeforeIntervals(t *testing.T) {
	traceExporter := &captureSpanExporter{}
	metricExporter := &captureMetricExporter{}
	config := validTelemetryConfig()
	config.Sampler = sdktrace.AlwaysSample()
	config.BatchTimeout = time.Hour
	config.MetricInterval = time.Hour
	telemetry, err := newTelemetry(t.Context(), config, telemetryFactories{
		resource: func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
		traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
			return traceExporter, nil
		},
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
			return metricExporter, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pass := vvotel.Periodic(telemetry.Frostgrove, func(context.Context) error { return nil })
	if err := pass(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(traceExporter.spans) != 0 || len(metricExporter.metrics) != 0 {
		t.Fatal("buffered signal exported before its configured interval")
	}
	if err := telemetry.ForceFlush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(traceExporter.spans) != 1 || traceExporter.spans[0].Name() != vvotel.SpanRuntimePeriodic {
		t.Fatalf("flushed spans = %#v", traceExporter.spans)
	}
	if len(metricExporter.metrics) != 1 || len(metricExporter.metrics[0].ScopeMetrics) != 1 ||
		len(metricExporter.metrics[0].ScopeMetrics[0].Metrics) != 1 ||
		metricExporter.metrics[0].ScopeMetrics[0].Metrics[0].Name != vvotel.MetricRuntimePeriodicDuration {
		t.Fatalf("flushed metrics = %#v", metricExporter.metrics)
	}
	if err := telemetry.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func validTelemetryConfig() Config {
	return Config{
		ServiceName:          "orders-api",
		ServiceVersion:       "1.0.0",
		ServiceNamespace:     "commerce",
		FrameworkResource:    "orders",
		ProviderCloseTimeout: time.Second,
	}
}
