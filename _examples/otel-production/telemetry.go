package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

var (
	ErrInvalidTelemetryConfig = errors.New("otel-production: invalid config")
	ErrTelemetryClosed        = errors.New("otel-production: telemetry closed")
	ErrTelemetryAssembly      = errors.New("otel-production: telemetry assembly failed")
	ErrTelemetryLifecycle     = errors.New("otel-production: provider lifecycle failed")
)

const (
	maxBatchQueueSize                = 1 << 20
	maxBatchSize                     = 1 << 16
	maxViews                         = 256
	maxProjectionLayers              = 64
	maxProjectionScopes              = 256
	maxProjectionScopeAttributes     = 32
	maxProjectionScopeNameBytes      = 256
	maxProjectionVersionBytes        = 128
	maxProjectionSchemaURLBytes      = 2048
	maxProjectionAttributeKeyBytes   = 128
	maxProjectionAttributeValueBytes = 512
	maxProjectionAttributeSliceItems = 32
	maxProjectionAttributeDepth      = 4
	maxProjectionScopeAttributeBytes = 16 << 10
	maxBatchTimeout                  = time.Hour
	maxMetricInterval                = 24 * time.Hour
	maxMetricExportTimeout           = 10 * time.Minute
	maxForceFlushTimeout             = 10 * time.Minute
	maxProviderCloseTimeout          = 10 * time.Minute
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
	TraceProjectionLayers   []TraceProjectionLayer
	MetricProjectionLayers  []MetricProjectionLayer
	FrostgroveViewsDisabled bool
	BatchQueueSize          int
	BatchSize               int
	BatchTimeout            time.Duration
	MetricInterval          time.Duration
	MetricExportTimeout     time.Duration
	ForceFlushTimeout       time.Duration
	ProviderCloseTimeout    time.Duration
}

type TraceProjectionLayer struct {
	Scopes []instrumentation.Scope
	Wrap   func(sdktrace.SpanExporter) (sdktrace.SpanExporter, error)
}

type MetricProjectionLayer struct {
	Scopes []instrumentation.Scope
	Wrap   func(sdkmetric.Exporter) (sdkmetric.Exporter, error)
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

type telemetryAssemblyOwners struct {
	ctx       context.Context
	timeout   time.Duration
	trace     interface{ Shutdown(context.Context) error }
	metric    interface{ Shutdown(context.Context) error }
	dismissed bool
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
			return otlpmetricgrpc.New(ctx,
				otlpmetricgrpc.WithTemporalitySelector(cumulativeTemporality),
				otlpmetricgrpc.WithAggregationSelector(sdkmetric.DefaultAggregationSelector),
			)
		},
	}
}

func cumulativeTemporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func newTelemetry(ctx context.Context, config Config, factories telemetryFactories) (telemetry *Telemetry, err error) {
	owners := &telemetryAssemblyOwners{}
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		telemetry = nil
		err = errors.Join(ErrTelemetryAssembly, owners.cleanup())
	}()
	telemetry, err = assembleTelemetry(ctx, config, factories, owners)
	if err != nil {
		err = errors.Join(err, owners.cleanup())
	} else {
		owners.dismiss()
	}
	completed = true
	return telemetry, err
}

func assembleTelemetry(ctx context.Context, config Config, factories telemetryFactories, owners *telemetryAssemblyOwners) (*Telemetry, error) {
	config, err := normalizeTelemetryConfig(config)
	if err != nil || nilInterfaceValue(ctx) || factories.resource == nil || factories.traceExporter == nil || factories.metricExporter == nil {
		return nil, ErrInvalidTelemetryConfig
	}
	owners.ctx = ctx
	owners.timeout = config.ProviderCloseTimeout
	res, err := callResourceFactory(factories.resource, ctx, config)
	if err != nil {
		return nil, fmt.Errorf("resource: %w", err)
	}
	if res == nil {
		return nil, ErrInvalidTelemetryConfig
	}
	traceExporter, err := callTraceExporterFactory(factories.traceExporter, ctx)
	owners.trace = traceExporter
	if err != nil {
		return nil, fmt.Errorf("trace exporter: %w", err)
	}
	if nilInterfaceValue(traceExporter) {
		return nil, ErrInvalidTelemetryConfig
	}
	projection := newExportProjection(config)
	traceExporter, err = composeTraceExporter(traceExporter, config.TraceProjectionLayers, projection)
	owners.trace = traceExporter
	if err != nil {
		return nil, err
	}
	metricExporter, err := callMetricExporterFactory(factories.metricExporter, ctx)
	owners.metric = metricExporter
	if err != nil {
		return nil, fmt.Errorf("metric exporter: %w", err)
	}
	if nilInterfaceValue(metricExporter) {
		return nil, ErrInvalidTelemetryConfig
	}
	metricExporter, err = composeMetricExporter(metricExporter, config.MetricProjectionLayers, projection)
	owners.metric = metricExporter
	if err != nil {
		return nil, err
	}

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
	owners.trace = traceProvider
	reader := sdkmetric.NewPeriodicReader(metricExporter,
		sdkmetric.WithInterval(config.MetricInterval),
		sdkmetric.WithTimeout(config.MetricExportTimeout),
	)
	owners.metric = reader
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
	owners.metric = meterProvider
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
		return nil, err
	}
	return &Telemetry{
		TracerProvider: traceProvider,
		MeterProvider:  meterProvider,
		Propagator:     propagation.TraceContext{},
		Frostgrove:     frostgrove,
		lifecycle:      lifecycle,
	}, nil
}

func (owners *telemetryAssemblyOwners) dismiss() {
	if owners == nil {
		return
	}
	owners.dismissed = true
	owners.trace = nil
	owners.metric = nil
}

func (owners *telemetryAssemblyOwners) cleanup() (result error) {
	if owners == nil || owners.dismissed {
		return nil
	}
	owners.dismissed = true
	traceOwner := owners.trace
	metricOwner := owners.metric
	owners.trace = nil
	owners.metric = nil
	metricAttempted := false
	defer func() {
		if metricAttempted {
			return
		}
		metricAttempted = true
		metricErr := shutdownDetachedOptional(owners.ctx, owners.timeout, metricOwner)
		result = errors.Join(result, metricErr)
	}()
	traceErr := shutdownDetachedOptional(owners.ctx, owners.timeout, traceOwner)
	metricAttempted = true
	metricErr := shutdownDetachedOptional(owners.ctx, owners.timeout, metricOwner)
	return errors.Join(traceErr, metricErr)
}

func normalizeTelemetryConfig(config Config) (Config, error) {
	if !vvotel.ValidResourceName(config.ServiceName) || !vvotel.ValidResourceName(config.ServiceVersion) ||
		!vvotel.ValidResourceName(config.ServiceNamespace) || config.FrameworkResource != "" && !config.FrameworkResource.Valid() ||
		math.IsNaN(config.SampleRatio) || config.SampleRatio < 0 || config.SampleRatio > 1 ||
		len(config.FrameworkResources) > vvotel.MaxResourceNameValues {
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
	if config.Sampler != nil && nilInterfaceValue(config.Sampler) {
		return Config{}, ErrInvalidTelemetryConfig
	}
	if len(config.Views) > maxViews {
		return Config{}, ErrInvalidTelemetryConfig
	}
	config.Views = append([]sdkmetric.View(nil), config.Views...)
	for _, view := range config.Views {
		if view == nil {
			return Config{}, ErrInvalidTelemetryConfig
		}
	}
	traceProjectionLayers, err := normalizeTraceProjectionLayers(config.TraceProjectionLayers)
	if err != nil {
		return Config{}, err
	}
	config.TraceProjectionLayers = traceProjectionLayers
	metricProjectionLayers, err := normalizeMetricProjectionLayers(config.MetricProjectionLayers)
	if err != nil {
		return Config{}, err
	}
	config.MetricProjectionLayers = metricProjectionLayers
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
	if config.BatchQueueSize < 1 || config.BatchQueueSize > maxBatchQueueSize ||
		config.BatchSize < 1 || config.BatchSize > maxBatchSize || config.BatchSize > config.BatchQueueSize ||
		config.BatchTimeout < time.Millisecond || config.BatchTimeout > maxBatchTimeout ||
		config.MetricInterval < time.Millisecond || config.MetricInterval > maxMetricInterval ||
		config.MetricExportTimeout < time.Millisecond || config.MetricExportTimeout > maxMetricExportTimeout ||
		config.ForceFlushTimeout < time.Millisecond || config.ForceFlushTimeout > maxForceFlushTimeout ||
		config.ProviderCloseTimeout < time.Millisecond || config.ProviderCloseTimeout > maxProviderCloseTimeout {
		return Config{}, ErrInvalidTelemetryConfig
	}
	return config, nil
}

func normalizeTraceProjectionLayers(input []TraceProjectionLayer) ([]TraceProjectionLayer, error) {
	if len(input) > maxProjectionLayers {
		return nil, ErrInvalidTelemetryConfig
	}
	result := make([]TraceProjectionLayer, len(input))
	seen := []instrumentation.Scope{frostgroveInstrumentationScope()}
	for index, layer := range input {
		if layer.Wrap == nil || len(layer.Scopes) == 0 {
			return nil, ErrInvalidTelemetryConfig
		}
		if len(layer.Scopes) > maxProjectionScopes+1-len(seen) {
			return nil, ErrInvalidTelemetryConfig
		}
		result[index] = TraceProjectionLayer{
			Scopes: append([]instrumentation.Scope(nil), layer.Scopes...),
			Wrap:   layer.Wrap,
		}
		for _, scope := range result[index].Scopes {
			if !validInstrumentationScope(scope) || containsInstrumentationScope(seen, scope) {
				return nil, ErrInvalidTelemetryConfig
			}
			seen = append(seen, scope)
		}
	}
	return result, nil
}

func normalizeMetricProjectionLayers(input []MetricProjectionLayer) ([]MetricProjectionLayer, error) {
	if len(input) > maxProjectionLayers {
		return nil, ErrInvalidTelemetryConfig
	}
	result := make([]MetricProjectionLayer, len(input))
	seen := []instrumentation.Scope{frostgroveInstrumentationScope()}
	for index, layer := range input {
		if layer.Wrap == nil || len(layer.Scopes) == 0 {
			return nil, ErrInvalidTelemetryConfig
		}
		if len(layer.Scopes) > maxProjectionScopes+1-len(seen) {
			return nil, ErrInvalidTelemetryConfig
		}
		result[index] = MetricProjectionLayer{
			Scopes: append([]instrumentation.Scope(nil), layer.Scopes...),
			Wrap:   layer.Wrap,
		}
		for _, scope := range result[index].Scopes {
			if !validInstrumentationScope(scope) || containsInstrumentationScope(seen, scope) {
				return nil, ErrInvalidTelemetryConfig
			}
			seen = append(seen, scope)
		}
	}
	return result, nil
}

func validInstrumentationScope(scope instrumentation.Scope) bool {
	if strings.TrimSpace(scope.Name) == "" || len(scope.Name) > maxProjectionScopeNameBytes ||
		len(scope.Version) > maxProjectionVersionBytes || len(scope.SchemaURL) > maxProjectionSchemaURLBytes ||
		!utf8.ValidString(scope.Name) || !utf8.ValidString(scope.Version) || !utf8.ValidString(scope.SchemaURL) ||
		scope.Attributes.Len() > maxProjectionScopeAttributes {
		return false
	}
	if scope.SchemaURL != "" {
		parsed, err := url.Parse(scope.SchemaURL)
		if err != nil || !parsed.IsAbs() {
			return false
		}
	}
	remaining := maxProjectionScopeAttributeBytes
	for _, candidate := range scope.Attributes.ToSlice() {
		if !validProjectionScopeAttribute(candidate, &remaining) {
			return false
		}
	}
	return true
}

func validProjectionScopeAttribute(candidate attribute.KeyValue, remaining *int) bool {
	key := string(candidate.Key)
	if !candidate.Valid() || len(key) > maxProjectionAttributeKeyBytes || !utf8.ValidString(key) || !consumeProjectionAttributeBytes(remaining, len(key)) {
		return false
	}
	return validProjectionAttributeValue(candidate.Value, 0, remaining)
}

func validProjectionAttributeValue(value attribute.Value, depth int, remaining *int) bool {
	switch value.Type() {
	case attribute.EMPTY:
		return true
	case attribute.BOOL, attribute.INT64:
		return consumeProjectionAttributeBytes(remaining, 8)
	case attribute.FLOAT64:
		return !math.IsNaN(value.AsFloat64()) && !math.IsInf(value.AsFloat64(), 0) && consumeProjectionAttributeBytes(remaining, 8)
	case attribute.STRING:
		candidate := value.AsString()
		return len(candidate) <= maxProjectionAttributeValueBytes && utf8.ValidString(candidate) && consumeProjectionAttributeBytes(remaining, len(candidate))
	case attribute.BOOLSLICE:
		candidate := value.AsBoolSlice()
		return len(candidate) <= maxProjectionAttributeSliceItems && consumeProjectionAttributeBytes(remaining, len(candidate))
	case attribute.INT64SLICE:
		candidate := value.AsInt64Slice()
		return len(candidate) <= maxProjectionAttributeSliceItems && consumeProjectionAttributeBytes(remaining, len(candidate)*8)
	case attribute.FLOAT64SLICE:
		candidate := value.AsFloat64Slice()
		if len(candidate) > maxProjectionAttributeSliceItems || !consumeProjectionAttributeBytes(remaining, len(candidate)*8) {
			return false
		}
		for _, number := range candidate {
			if math.IsNaN(number) || math.IsInf(number, 0) {
				return false
			}
		}
		return true
	case attribute.STRINGSLICE:
		candidate := value.AsStringSlice()
		if len(candidate) > maxProjectionAttributeSliceItems {
			return false
		}
		for _, item := range candidate {
			if len(item) > maxProjectionAttributeValueBytes || !utf8.ValidString(item) || !consumeProjectionAttributeBytes(remaining, len(item)) {
				return false
			}
		}
		return true
	case attribute.BYTESLICE:
		candidate := value.AsByteSlice()
		return len(candidate) <= maxProjectionAttributeValueBytes && consumeProjectionAttributeBytes(remaining, len(candidate))
	case attribute.SLICE:
		candidate := value.AsSlice()
		if depth >= maxProjectionAttributeDepth || len(candidate) > maxProjectionAttributeSliceItems {
			return false
		}
		for _, item := range candidate {
			if !validProjectionAttributeValue(item, depth+1, remaining) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func consumeProjectionAttributeBytes(remaining *int, count int) bool {
	if count < 0 || remaining == nil || count > *remaining {
		return false
	}
	*remaining -= count
	return true
}

func containsInstrumentationScope(scopes []instrumentation.Scope, candidate instrumentation.Scope) bool {
	for _, scope := range scopes {
		if instrumentationScopesEqual(scope, candidate) {
			return true
		}
	}
	return false
}

func callResourceFactory(factory func(context.Context, Config) (*resource.Resource, error), ctx context.Context, config Config) (result *resource.Resource, err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			result = nil
			err = ErrTelemetryAssembly
		}
	}()
	result, err = factory(ctx, config)
	completed = true
	return result, err
}

func callTraceExporterFactory(factory func(context.Context) (sdktrace.SpanExporter, error), ctx context.Context) (result sdktrace.SpanExporter, err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			result = nil
			err = ErrTelemetryAssembly
		}
	}()
	result, err = factory(ctx)
	completed = true
	return result, err
}

func callMetricExporterFactory(factory func(context.Context) (sdkmetric.Exporter, error), ctx context.Context) (result sdkmetric.Exporter, err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			result = nil
			err = ErrTelemetryAssembly
		}
	}()
	result, err = factory(ctx)
	completed = true
	return result, err
}

func shutdownDetached(ctx context.Context, timeout time.Duration, owner interface{ Shutdown(context.Context) error }) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	return callLifecycle(cleanupCtx, func(callCtx context.Context) error {
		return owner.Shutdown(callCtx)
	})
}

func shutdownDetachedOptional(ctx context.Context, timeout time.Duration, owner interface{ Shutdown(context.Context) error }) error {
	if nilInterfaceValue(owner) {
		return nil
	}
	return shutdownDetached(ctx, timeout, owner)
}

func nilInterfaceValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
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
	if lifecycle == nil || nilInterfaceValue(ctx) {
		return ErrTelemetryClosed
	}
	lifecycle.mu.Lock()
	if lifecycle.state != telemetryOpen {
		lifecycle.mu.Unlock()
		return ErrTelemetryClosed
	}
	lifecycle.inFlight++
	lifecycle.mu.Unlock()
	defer lifecycle.releaseFlush()
	metricAttempted := false
	defer func() {
		if metricAttempted {
			return
		}
		_ = callWithTimeout(ctx, lifecycle.flushTimeout, func(callCtx context.Context) error {
			return lifecycle.metric.ForceFlush(callCtx)
		})
	}()

	traceErr := callWithTimeout(ctx, lifecycle.flushTimeout, func(callCtx context.Context) error {
		return lifecycle.trace.ForceFlush(callCtx)
	})
	metricAttempted = true
	metricErr := callWithTimeout(ctx, lifecycle.flushTimeout, func(callCtx context.Context) error {
		return lifecycle.metric.ForceFlush(callCtx)
	})
	return errors.Join(traceErr, metricErr)
}

func (lifecycle *telemetryLifecycle) releaseFlush() {
	lifecycle.mu.Lock()
	lifecycle.inFlight--
	if lifecycle.inFlight == 0 {
		lifecycle.cond.Broadcast()
	}
	lifecycle.mu.Unlock()
}

func (lifecycle *telemetryLifecycle) Shutdown(ctx context.Context) error {
	if lifecycle == nil || nilInterfaceValue(ctx) {
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
	var traceErr error
	var metricErr error
	traceReturned := false
	metricAttempted := false
	metricReturned := false
	completed := false
	defer func() {
		if !completed {
			if !traceReturned {
				traceErr = errors.Join(traceErr, ErrTelemetryLifecycle)
			}
			if !metricReturned {
				metricErr = errors.Join(metricErr, ErrTelemetryLifecycle)
			}
			lifecycle.completeShutdown(errors.Join(traceErr, metricErr))
		}
	}()
	defer func() {
		if metricAttempted {
			return
		}
		metricAttempted = true
		metricErr = callWithTimeout(ownerCtx, lifecycle.shutdownTimeout, func(callCtx context.Context) error {
			return lifecycle.metric.Shutdown(callCtx)
		})
		metricReturned = true
	}()

	traceErr = callWithTimeout(ownerCtx, lifecycle.shutdownTimeout, func(callCtx context.Context) error {
		return lifecycle.trace.Shutdown(callCtx)
	})
	traceReturned = true
	metricAttempted = true
	metricErr = callWithTimeout(ownerCtx, lifecycle.shutdownTimeout, func(callCtx context.Context) error {
		return lifecycle.metric.Shutdown(callCtx)
	})
	metricReturned = true
	result := errors.Join(traceErr, metricErr)
	lifecycle.completeShutdown(result)
	completed = true
	return result
}

func (lifecycle *telemetryLifecycle) completeShutdown(result error) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.state == telemetryClosed {
		return
	}
	lifecycle.result = result
	lifecycle.state = telemetryClosed
	close(lifecycle.done)
	lifecycle.cond.Broadcast()
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
	return callLifecycle(callCtx, call)
}

func callLifecycle(ctx context.Context, call func(context.Context) error) (err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			err = ErrTelemetryLifecycle
		}
	}()
	err = call(ctx)
	completed = true
	return err
}
