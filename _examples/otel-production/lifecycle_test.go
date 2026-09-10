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
	"go.opentelemetry.io/otel/sdk/instrumentation"
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

type lifecycleBudgetProbe struct {
	flushBudget    chan time.Duration
	shutdownBudget chan time.Duration
}

func (provider *lifecycleBudgetProbe) ForceFlush(ctx context.Context) error {
	provider.flushBudget <- remainingBudget(ctx)
	<-ctx.Done()
	return ctx.Err()
}

func (provider *lifecycleBudgetProbe) Shutdown(ctx context.Context) error {
	provider.shutdownBudget <- remainingBudget(ctx)
	<-ctx.Done()
	return ctx.Err()
}

func remainingBudget(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return time.Until(deadline)
}

type spanExporterStub struct {
	shutdownCalls      atomic.Int64
	shutdownContextErr error
	shutdownResult     error
}

func (*spanExporterStub) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }

func (exporter *spanExporterStub) Shutdown(ctx context.Context) error {
	exporter.shutdownCalls.Add(1)
	exporter.shutdownContextErr = ctx.Err()
	return exporter.shutdownResult
}

type metricExporterStub struct {
	flushCalls         atomic.Int64
	shutdownCalls      atomic.Int64
	shutdownContextErr error
	shutdownResult     error
}

type partialSpanExporter struct {
	next          sdktrace.SpanExporter
	shutdownCalls atomic.Int64
	shutdownErr   error
}

func (exporter *partialSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return exporter.next.ExportSpans(ctx, spans)
}

func (exporter *partialSpanExporter) Shutdown(ctx context.Context) error {
	exporter.shutdownCalls.Add(1)
	return errors.Join(exporter.shutdownErr, exporter.next.Shutdown(ctx))
}

type partialMetricExporter struct {
	next          sdkmetric.Exporter
	shutdownCalls atomic.Int64
	shutdownErr   error
}

func (exporter *partialMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return exporter.next.Temporality(kind)
}

func (exporter *partialMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return exporter.next.Aggregation(kind)
}

func (exporter *partialMetricExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	return exporter.next.Export(ctx, metrics)
}

func (exporter *partialMetricExporter) ForceFlush(ctx context.Context) error {
	return exporter.next.ForceFlush(ctx)
}

func (exporter *partialMetricExporter) Shutdown(ctx context.Context) error {
	exporter.shutdownCalls.Add(1)
	return errors.Join(exporter.shutdownErr, exporter.next.Shutdown(ctx))
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

func (exporter *metricExporterStub) Shutdown(ctx context.Context) error {
	exporter.shutdownCalls.Add(1)
	exporter.shutdownContextErr = ctx.Err()
	return exporter.shutdownResult
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
	if traceExporter.shutdownCalls.Load() != 1 || traceExporter.shutdownContextErr != nil {
		t.Fatalf("detached trace cleanup = %d, context error %v", traceExporter.shutdownCalls.Load(), traceExporter.shutdownContextErr)
	}
}

func TestNewTelemetryCleansNonNilTraceExporterReturnedWithFactoryError(t *testing.T) {
	factoryErr := errors.New("trace exporter factory")
	cleanupErr := errors.New("trace exporter cleanup")
	traceExporter := &spanExporterStub{shutdownResult: cleanupErr}
	metricCalled := false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	telemetry, err := newTelemetry(ctx, validTelemetryConfig(), telemetryFactories{
		resource: func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
		traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
			return traceExporter, factoryErr
		},
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
			metricCalled = true
			return nil, nil
		},
	})
	if telemetry != nil || !errors.Is(err, factoryErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("NewTelemetry result = %#v, %v", telemetry, err)
	}
	if metricCalled {
		t.Fatal("metric factory called after trace factory failure")
	}
	if traceExporter.shutdownCalls.Load() != 1 || traceExporter.shutdownContextErr != nil {
		t.Fatalf("trace cleanup = %d, context error %v", traceExporter.shutdownCalls.Load(), traceExporter.shutdownContextErr)
	}
}

func TestNewTelemetryCleansBothExportersWhenMetricFactoryReturnsExporterAndError(t *testing.T) {
	factoryErr := errors.New("metric exporter factory")
	traceCleanupErr := errors.New("trace exporter cleanup")
	metricCleanupErr := errors.New("metric exporter cleanup")
	traceExporter := &spanExporterStub{shutdownResult: traceCleanupErr}
	metricExporter := &metricExporterStub{shutdownResult: metricCleanupErr}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	telemetry, err := newTelemetry(ctx, validTelemetryConfig(), telemetryFactories{
		resource: func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
		traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
			return traceExporter, nil
		},
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
			return metricExporter, factoryErr
		},
	})
	if telemetry != nil || !errors.Is(err, factoryErr) || !errors.Is(err, traceCleanupErr) || !errors.Is(err, metricCleanupErr) {
		t.Fatalf("NewTelemetry result = %#v, %v", telemetry, err)
	}
	if traceExporter.shutdownCalls.Load() != 1 || traceExporter.shutdownContextErr != nil {
		t.Fatalf("trace cleanup = %d, context error %v", traceExporter.shutdownCalls.Load(), traceExporter.shutdownContextErr)
	}
	if metricExporter.shutdownCalls.Load() != 1 || metricExporter.shutdownContextErr != nil {
		t.Fatalf("metric cleanup = %d, context error %v", metricExporter.shutdownCalls.Load(), metricExporter.shutdownContextErr)
	}
}

func TestNewTelemetryFactoryErrorWithNilExporterIsSafe(t *testing.T) {
	wantErr := errors.New("trace exporter factory")
	telemetry, err := newTelemetry(t.Context(), validTelemetryConfig(), telemetryFactories{
		resource: func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
		traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
			return (*spanExporterStub)(nil), wantErr
		},
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
			t.Fatal("metric factory called after trace factory failure")
			return nil, nil
		},
	})
	if telemetry != nil || !errors.Is(err, wantErr) {
		t.Fatalf("NewTelemetry result = %#v, %v", telemetry, err)
	}
}

func TestNewTelemetryCleansTraceExporterWhenTraceProjectionAssemblyFails(t *testing.T) {
	projectionErr := errors.New("trace projection assembly")
	traceExporter := &spanExporterStub{}
	metricCalled := false
	config := validTelemetryConfig()
	config.TraceProjectionLayers = []TraceProjectionLayer{{
		Scopes: []instrumentation.Scope{nativeTestScope},
		Wrap: func(sdktrace.SpanExporter) (sdktrace.SpanExporter, error) {
			return nil, projectionErr
		},
	}}

	telemetry, err := newTelemetry(t.Context(), config, telemetryFactories{
		resource: func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
		traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
			return traceExporter, nil
		},
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
			metricCalled = true
			return nil, nil
		},
	})
	if telemetry != nil || !errors.Is(err, projectionErr) {
		t.Fatalf("NewTelemetry result = %#v, %v", telemetry, err)
	}
	if metricCalled {
		t.Fatal("metric factory called after trace projection failure")
	}
	if traceExporter.shutdownCalls.Load() != 1 {
		t.Fatalf("trace cleanup calls = %d", traceExporter.shutdownCalls.Load())
	}
}

func TestNewTelemetryCleansBothExportersWhenMetricProjectionAssemblyFails(t *testing.T) {
	projectionErr := errors.New("metric projection assembly")
	traceExporter := &spanExporterStub{}
	metricExporter := &metricExporterStub{}
	config := validTelemetryConfig()
	config.MetricProjectionLayers = []MetricProjectionLayer{{
		Scopes: []instrumentation.Scope{nativeTestScope},
		Wrap: func(sdkmetric.Exporter) (sdkmetric.Exporter, error) {
			return nil, projectionErr
		},
	}}

	telemetry, err := newTelemetry(t.Context(), config, telemetryFactories{
		resource: func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
		traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
			return traceExporter, nil
		},
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
			return metricExporter, nil
		},
	})
	if telemetry != nil || !errors.Is(err, projectionErr) {
		t.Fatalf("NewTelemetry result = %#v, %v", telemetry, err)
	}
	if traceExporter.shutdownCalls.Load() != 1 || metricExporter.shutdownCalls.Load() != 1 {
		t.Fatalf("cleanup calls = trace:%d metric:%d", traceExporter.shutdownCalls.Load(), metricExporter.shutdownCalls.Load())
	}
}

func TestNewTelemetryUsesNonNilFailingTraceProjectionAsCompleteRollbackOwner(t *testing.T) {
	assemblyErr := errors.New("outer trace projection assembly")
	outerCleanupErr := errors.New("outer trace projection cleanup")
	innerCleanupErr := errors.New("inner trace projection cleanup")
	baseCleanupErr := errors.New("base trace exporter cleanup")
	base := &spanExporterStub{shutdownResult: baseCleanupErr}
	var outer *partialSpanExporter
	var inner *partialSpanExporter
	metricCalled := false
	config := validTelemetryConfig()
	config.TraceProjectionLayers = []TraceProjectionLayer{
		{
			Scopes: []instrumentation.Scope{nativeTestScope},
			Wrap: func(next sdktrace.SpanExporter) (sdktrace.SpanExporter, error) {
				outer = &partialSpanExporter{next: next, shutdownErr: outerCleanupErr}
				return outer, assemblyErr
			},
		},
		{
			Scopes: []instrumentation.Scope{databaseTestScope},
			Wrap: func(next sdktrace.SpanExporter) (sdktrace.SpanExporter, error) {
				inner = &partialSpanExporter{next: next, shutdownErr: innerCleanupErr}
				return inner, nil
			},
		},
	}

	telemetry, err := newTelemetry(t.Context(), config, telemetryFactories{
		resource: func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
		traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
			return base, nil
		},
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
			metricCalled = true
			return nil, nil
		},
	})
	if telemetry != nil || !errors.Is(err, assemblyErr) || !errors.Is(err, outerCleanupErr) ||
		!errors.Is(err, innerCleanupErr) || !errors.Is(err, baseCleanupErr) {
		t.Fatalf("NewTelemetry result = %#v, %v", telemetry, err)
	}
	if metricCalled {
		t.Fatal("metric factory called after trace projection failure")
	}
	if outer.shutdownCalls.Load() != 1 || inner.shutdownCalls.Load() != 1 || base.shutdownCalls.Load() != 1 {
		t.Fatalf("trace cleanup calls = outer:%d inner:%d base:%d", outer.shutdownCalls.Load(), inner.shutdownCalls.Load(), base.shutdownCalls.Load())
	}
}

func TestNewTelemetryUsesNonNilFailingMetricProjectionAsCompleteRollbackOwner(t *testing.T) {
	assemblyErr := errors.New("outer metric projection assembly")
	outerCleanupErr := errors.New("outer metric projection cleanup")
	innerCleanupErr := errors.New("inner metric projection cleanup")
	baseCleanupErr := errors.New("base metric exporter cleanup")
	traceCleanupErr := errors.New("trace exporter cleanup")
	traceExporter := &spanExporterStub{shutdownResult: traceCleanupErr}
	base := &metricExporterStub{shutdownResult: baseCleanupErr}
	var outer *partialMetricExporter
	var inner *partialMetricExporter
	config := validTelemetryConfig()
	config.MetricProjectionLayers = []MetricProjectionLayer{
		{
			Scopes: []instrumentation.Scope{nativeTestScope},
			Wrap: func(next sdkmetric.Exporter) (sdkmetric.Exporter, error) {
				outer = &partialMetricExporter{next: next, shutdownErr: outerCleanupErr}
				return outer, assemblyErr
			},
		},
		{
			Scopes: []instrumentation.Scope{databaseTestScope},
			Wrap: func(next sdkmetric.Exporter) (sdkmetric.Exporter, error) {
				inner = &partialMetricExporter{next: next, shutdownErr: innerCleanupErr}
				return inner, nil
			},
		},
	}

	telemetry, err := newTelemetry(t.Context(), config, telemetryFactories{
		resource: func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
		traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
			return traceExporter, nil
		},
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
			return base, nil
		},
	})
	if telemetry != nil || !errors.Is(err, assemblyErr) || !errors.Is(err, outerCleanupErr) ||
		!errors.Is(err, innerCleanupErr) || !errors.Is(err, baseCleanupErr) || !errors.Is(err, traceCleanupErr) {
		t.Fatalf("NewTelemetry result = %#v, %v", telemetry, err)
	}
	if outer.shutdownCalls.Load() != 1 || inner.shutdownCalls.Load() != 1 || base.shutdownCalls.Load() != 1 || traceExporter.shutdownCalls.Load() != 1 {
		t.Fatalf("cleanup calls = outer:%d inner:%d metric-base:%d trace:%d", outer.shutdownCalls.Load(), inner.shutdownCalls.Load(), base.shutdownCalls.Load(), traceExporter.shutdownCalls.Load())
	}
}

func TestTelemetryLifecycleShutdownFirstRejectsEveryLaterFlush(t *testing.T) {
	traceProvider := &lifecycleProviderStub{}
	metricProvider := &lifecycleProviderStub{}
	lifecycle := newTelemetryLifecycle(traceProvider, metricProvider, time.Second, time.Second)

	if err := lifecycle.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	for range 3 {
		if err := lifecycle.ForceFlush(t.Context()); !errors.Is(err, ErrTelemetryClosed) {
			t.Fatalf("ForceFlush after shutdown = %v", err)
		}
	}
	if traceProvider.flushCalls.Load() != 0 || metricProvider.flushCalls.Load() != 0 {
		t.Fatalf("flush calls = %d/%d", traceProvider.flushCalls.Load(), metricProvider.flushCalls.Load())
	}
	if traceProvider.shutdownCalls.Load() != 1 || metricProvider.shutdownCalls.Load() != 1 {
		t.Fatalf("shutdown calls = %d/%d", traceProvider.shutdownCalls.Load(), metricProvider.shutdownCalls.Load())
	}
}

func TestTelemetryLifecycleGivesEachFlushProviderItsOwnBudget(t *testing.T) {
	traceProvider := &lifecycleBudgetProbe{flushBudget: make(chan time.Duration, 1), shutdownBudget: make(chan time.Duration, 1)}
	metricProvider := &lifecycleBudgetProbe{flushBudget: make(chan time.Duration, 1), shutdownBudget: make(chan time.Duration, 1)}
	lifecycle := newTelemetryLifecycle(traceProvider, metricProvider, 20*time.Millisecond, time.Second)

	err := lifecycle.ForceFlush(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ForceFlush = %v", err)
	}
	for name, budget := range map[string]time.Duration{
		"trace":  <-traceProvider.flushBudget,
		"metric": <-metricProvider.flushBudget,
	} {
		if budget <= 0 || budget > 25*time.Millisecond {
			t.Fatalf("%s flush budget = %v", name, budget)
		}
	}
}

func TestTelemetryLifecycleDetachesCanceledOwnerAndBudgetsEachShutdown(t *testing.T) {
	traceProvider := &lifecycleBudgetProbe{flushBudget: make(chan time.Duration, 1), shutdownBudget: make(chan time.Duration, 1)}
	metricProvider := &lifecycleBudgetProbe{flushBudget: make(chan time.Duration, 1), shutdownBudget: make(chan time.Duration, 1)}
	lifecycle := newTelemetryLifecycle(traceProvider, metricProvider, time.Second, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := lifecycle.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown = %v", err)
	}
	for name, budget := range map[string]time.Duration{
		"trace":  <-traceProvider.shutdownBudget,
		"metric": <-metricProvider.shutdownBudget,
	} {
		if budget <= 0 || budget > 25*time.Millisecond {
			t.Fatalf("%s shutdown budget = %v", name, budget)
		}
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
