package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

var (
	ErrInvalidTelemetryConfig = errors.New("otel-production: invalid config")
	ErrTelemetryClosed        = errors.New("otel-production: telemetry closed")
)

type Config struct {
	ServiceName             string
	ServiceVersion          string
	ServiceNamespace        string
	FrameworkResource       vvotel.ApprovedName
	FrameworkResources      []vvotel.ApprovedName
	SampleRatio             float64
	Sampler                 sdktrace.Sampler
	Views                   []sdkmetric.View
	FrostgroveViewsDisabled bool
	BatchQueueSize          int
	BatchSize               int
	BatchTimeout            time.Duration
	MetricInterval          time.Duration
	MetricExportTimeout     time.Duration
	ForceFlushTimeout       time.Duration
	ProviderCloseTimeout    time.Duration
}

type Telemetry struct {
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  *sdkmetric.MeterProvider
	Propagator     propagation.TextMapPropagator
	Frostgrove     *vvotel.Telemetry

	lifecycle *telemetryLifecycle
}

func NewTelemetry(ctx context.Context, config Config) (*Telemetry, error) {
	return newTelemetry(ctx, config, defaultTelemetryFactories())
}

func (telemetry *Telemetry) ForceFlush(ctx context.Context) error {
	if telemetry == nil || telemetry.lifecycle == nil {
		return ErrTelemetryClosed
	}
	return telemetry.lifecycle.ForceFlush(ctx)
}

func (telemetry *Telemetry) Shutdown(ctx context.Context) error {
	if telemetry == nil || telemetry.lifecycle == nil {
		return ErrTelemetryClosed
	}
	return telemetry.lifecycle.Shutdown(ctx)
}

type providerLifecycle interface {
	ForceFlush(context.Context) error
	Shutdown(context.Context) error
}

type telemetryFactories struct {
	resource       func(context.Context, Config) (*resource.Resource, error)
	traceExporter  func(context.Context) (sdktrace.SpanExporter, error)
	metricExporter func(context.Context) (sdkmetric.Exporter, error)
}

func defaultTelemetryFactories() telemetryFactories {
	return telemetryFactories{
		resource: func(ctx context.Context, config Config) (*resource.Resource, error) {
			return resource.New(ctx,
				resource.WithTelemetrySDK(),
				resource.WithAttributes(
					semconv.ServiceName(config.ServiceName),
					semconv.ServiceVersion(config.ServiceVersion),
					semconv.ServiceNamespace(config.ServiceNamespace),
				),
			)
		},
		traceExporter: func(ctx context.Context) (sdktrace.SpanExporter, error) {
			return otlptracegrpc.New(ctx)
		},
		metricExporter: func(ctx context.Context) (sdkmetric.Exporter, error) {
			return otlpmetricgrpc.New(ctx)
		},
	}
}

func newTelemetry(ctx context.Context, config Config, factories telemetryFactories) (*Telemetry, error) {
	config, err := normalizeTelemetryConfig(config)
	if err != nil || ctx == nil || factories.resource == nil || factories.traceExporter == nil || factories.metricExporter == nil {
		return nil, ErrInvalidTelemetryConfig
	}
	res, err := factories.resource(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("resource: %w", err)
	}
	traceExporter, err := factories.traceExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("trace exporter: %w", err)
	}
	metricExporter, err := factories.metricExporter(ctx)
	if err != nil {
		cleanupErr := shutdownDetached(ctx, config.ProviderCloseTimeout, traceExporter)
		return nil, errors.Join(fmt.Errorf("metric exporter: %w", err), cleanupErr)
	}
	projection := newExportProjection(config)
	traceExporter = &projectingSpanExporter{next: traceExporter, projection: projection}
	metricExporter = &projectingMetricExporter{next: metricExporter, projection: projection}

	sampler := config.Sampler
	if sampler == nil {
		sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SampleRatio))
	}
	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
		sdktrace.WithBatcher(traceExporter,
			sdktrace.WithMaxQueueSize(config.BatchQueueSize),
			sdktrace.WithMaxExportBatchSize(config.BatchSize),
			sdktrace.WithBatchTimeout(config.BatchTimeout),
		),
	)
	reader := sdkmetric.NewPeriodicReader(metricExporter,
		sdkmetric.WithInterval(config.MetricInterval),
		sdkmetric.WithTimeout(config.MetricExportTimeout),
	)
	meterOptions := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
		sdkmetric.WithExemplarFilter(exemplar.TraceBasedFilter),
	}
	if !config.FrostgroveViewsDisabled {
		for _, view := range frostgroveViews() {
			meterOptions = append(meterOptions, sdkmetric.WithView(view))
		}
	}
	for _, view := range config.Views {
		meterOptions = append(meterOptions, sdkmetric.WithView(view))
	}
	meterProvider := sdkmetric.NewMeterProvider(meterOptions...)
	lifecycle := newTelemetryLifecycle(
		traceProvider,
		meterProvider,
		config.ForceFlushTimeout,
		config.ProviderCloseTimeout,
	)
	frostgrove, err := vvotel.New(vvotel.Config{
		TracerProvider: traceProvider,
		MeterProvider:  meterProvider,
		ResourceName:   config.FrameworkResource,
	})
	if err != nil {
		return nil, errors.Join(err, lifecycle.Shutdown(ctx))
	}
	return &Telemetry{
		TracerProvider: traceProvider,
		MeterProvider:  meterProvider,
		Propagator:     propagation.TraceContext{},
		Frostgrove:     frostgrove,
		lifecycle:      lifecycle,
	}, nil
}

func normalizeTelemetryConfig(config Config) (Config, error) {
	if !vvotel.ValidResourceName(config.ServiceName) || !vvotel.ValidResourceName(config.ServiceVersion) ||
		!vvotel.ValidResourceName(config.ServiceNamespace) || config.FrameworkResource != "" && !config.FrameworkResource.Valid() ||
		config.SampleRatio < 0 || config.SampleRatio > 1 {
		return Config{}, ErrInvalidTelemetryConfig
	}
	config.FrameworkResources = append([]vvotel.ApprovedName(nil), config.FrameworkResources...)
	resourceNames := make(map[vvotel.ApprovedName]struct{}, len(config.FrameworkResources)+1)
	if config.FrameworkResource != "" {
		resourceNames[config.FrameworkResource] = struct{}{}
	}
	for _, name := range config.FrameworkResources {
		if !name.Valid() {
			return Config{}, ErrInvalidTelemetryConfig
		}
		resourceNames[name] = struct{}{}
	}
	if len(resourceNames) > vvotel.MaxResourceNameValues {
		return Config{}, ErrInvalidTelemetryConfig
	}
	if config.SampleRatio == 0 {
		config.SampleRatio = 0.1
	}
	config.Views = append([]sdkmetric.View(nil), config.Views...)
	for _, view := range config.Views {
		if view == nil {
			return Config{}, ErrInvalidTelemetryConfig
		}
	}
	if config.BatchQueueSize == 0 {
		config.BatchQueueSize = 2048
	}
	if config.BatchSize == 0 {
		config.BatchSize = 512
	}
	if config.BatchTimeout == 0 {
		config.BatchTimeout = 5 * time.Second
	}
	if config.MetricInterval == 0 {
		config.MetricInterval = 30 * time.Second
	}
	if config.MetricExportTimeout == 0 {
		config.MetricExportTimeout = 10 * time.Second
	}
	if config.ForceFlushTimeout == 0 {
		config.ForceFlushTimeout = 10 * time.Second
	}
	if config.ProviderCloseTimeout == 0 {
		config.ProviderCloseTimeout = 10 * time.Second
	}
	if config.BatchQueueSize < 1 || config.BatchSize < 1 || config.BatchSize > config.BatchQueueSize ||
		config.BatchTimeout < time.Millisecond || config.MetricInterval < time.Millisecond ||
		config.MetricExportTimeout < time.Millisecond || config.ForceFlushTimeout < time.Millisecond ||
		config.ProviderCloseTimeout < time.Millisecond {
		return Config{}, ErrInvalidTelemetryConfig
	}
	return config, nil
}

func shutdownDetached(ctx context.Context, timeout time.Duration, owner interface{ Shutdown(context.Context) error }) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	return owner.Shutdown(cleanupCtx)
}

const (
	telemetryOpen uint8 = iota
	telemetryClosing
	telemetryClosed
)

type telemetryLifecycle struct {
	mu              sync.Mutex
	cond            *sync.Cond
	done            chan struct{}
	state           uint8
	inFlight        int
	result          error
	trace           providerLifecycle
	metric          providerLifecycle
	flushTimeout    time.Duration
	shutdownTimeout time.Duration
}

func newTelemetryLifecycle(traceProvider providerLifecycle, metricProvider providerLifecycle, flushTimeout time.Duration, shutdownTimeout time.Duration) *telemetryLifecycle {
	lifecycle := &telemetryLifecycle{
		done:            make(chan struct{}),
		trace:           traceProvider,
		metric:          metricProvider,
		flushTimeout:    flushTimeout,
		shutdownTimeout: shutdownTimeout,
	}
	lifecycle.cond = sync.NewCond(&lifecycle.mu)
	return lifecycle
}

func (lifecycle *telemetryLifecycle) ForceFlush(ctx context.Context) error {
	if lifecycle == nil || ctx == nil {
		return ErrTelemetryClosed
	}
	lifecycle.mu.Lock()
	if lifecycle.state != telemetryOpen {
		lifecycle.mu.Unlock()
		return ErrTelemetryClosed
	}
	lifecycle.inFlight++
	lifecycle.mu.Unlock()

	traceErr := callWithTimeout(ctx, lifecycle.flushTimeout, lifecycle.trace.ForceFlush)
	metricErr := callWithTimeout(ctx, lifecycle.flushTimeout, lifecycle.metric.ForceFlush)

	lifecycle.mu.Lock()
	lifecycle.inFlight--
	if lifecycle.inFlight == 0 {
		lifecycle.cond.Broadcast()
	}
	lifecycle.mu.Unlock()
	return errors.Join(traceErr, metricErr)
}

func (lifecycle *telemetryLifecycle) Shutdown(ctx context.Context) error {
	if lifecycle == nil || ctx == nil {
		return ErrTelemetryClosed
	}
	lifecycle.mu.Lock()
	switch lifecycle.state {
	case telemetryClosed:
		result := lifecycle.result
		lifecycle.mu.Unlock()
		return result
	case telemetryClosing:
		done := lifecycle.done
		lifecycle.mu.Unlock()
		return lifecycle.waitForShutdown(ctx, done)
	default:
		lifecycle.state = telemetryClosing
		for lifecycle.inFlight > 0 {
			lifecycle.cond.Wait()
		}
		lifecycle.mu.Unlock()
	}

	ownerCtx := context.WithoutCancel(ctx)
	traceErr := callWithTimeout(ownerCtx, lifecycle.shutdownTimeout, lifecycle.trace.Shutdown)
	metricErr := callWithTimeout(ownerCtx, lifecycle.shutdownTimeout, lifecycle.metric.Shutdown)
	result := errors.Join(traceErr, metricErr)

	lifecycle.mu.Lock()
	lifecycle.result = result
	lifecycle.state = telemetryClosed
	close(lifecycle.done)
	lifecycle.cond.Broadcast()
	lifecycle.mu.Unlock()
	return result
}

func (lifecycle *telemetryLifecycle) waitForShutdown(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		lifecycle.mu.Lock()
		result := lifecycle.result
		lifecycle.mu.Unlock()
		return result
	default:
	}
	select {
	case <-done:
		lifecycle.mu.Lock()
		result := lifecycle.result
		lifecycle.mu.Unlock()
		return result
	case <-ctx.Done():
		select {
		case <-done:
			lifecycle.mu.Lock()
			result := lifecycle.result
			lifecycle.mu.Unlock()
			return result
		default:
			return ctx.Err()
		}
	}
}

func callWithTimeout(ctx context.Context, timeout time.Duration, call func(context.Context) error) error {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return call(callCtx)
}
