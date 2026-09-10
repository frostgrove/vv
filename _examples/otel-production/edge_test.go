package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type hostileLifecycleProvider struct {
	flushCalls     atomic.Int64
	shutdownCalls  atomic.Int64
	flushErr       error
	shutdownErr    error
	panicFlush     bool
	panicShutdown  bool
	goexitFlush    bool
	goexitShutdown bool
}

type typedNilProductionContext struct{}

func (*typedNilProductionContext) Deadline() (time.Time, bool) { panic("typed nil context") }
func (*typedNilProductionContext) Done() <-chan struct{}       { panic("typed nil context") }
func (*typedNilProductionContext) Err() error                  { panic("typed nil context") }
func (*typedNilProductionContext) Value(any) any               { panic("typed nil context") }

type typedNilSampler struct{}

func (*typedNilSampler) ShouldSample(sdktrace.SamplingParameters) sdktrace.SamplingResult {
	panic("typed nil sampler")
}

func (*typedNilSampler) Description() string { panic("typed nil sampler") }

func (provider *hostileLifecycleProvider) ForceFlush(context.Context) error {
	provider.flushCalls.Add(1)
	if provider.panicFlush {
		panic("flush")
	}
	if provider.goexitFlush {
		runtime.Goexit()
	}
	return provider.flushErr
}

func (provider *hostileLifecycleProvider) Shutdown(context.Context) error {
	provider.shutdownCalls.Add(1)
	if provider.panicShutdown {
		panic("shutdown")
	}
	if provider.goexitShutdown {
		runtime.Goexit()
	}
	return provider.shutdownErr
}

type panicShutdownSpanExporter struct {
	shutdownCalls atomic.Int64
}

func (*panicShutdownSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return nil
}

func (exporter *panicShutdownSpanExporter) Shutdown(context.Context) error {
	exporter.shutdownCalls.Add(1)
	panic("shutdown")
}

func TestTelemetryConfigRejectsHostileBounds(t *testing.T) {
	tracePass := func(next sdktrace.SpanExporter) (sdktrace.SpanExporter, error) { return next, nil }
	metricPass := func(next sdkmetric.Exporter) (sdkmetric.Exporter, error) { return next, nil }
	withScope := func(scope instrumentation.Scope) Config {
		config := validTelemetryConfig()
		config.TraceProjectionLayers = []TraceProjectionLayer{{Scopes: []instrumentation.Scope{scope}, Wrap: tracePass}}
		return config
	}
	tooManyScopes := make([]instrumentation.Scope, maxProjectionScopes+1)
	for index := range tooManyScopes {
		tooManyScopes[index] = instrumentation.Scope{Name: fmt.Sprintf("scope-%d", index)}
	}
	tooManyAttributes := make([]attribute.KeyValue, maxProjectionScopeAttributes+1)
	for index := range tooManyAttributes {
		tooManyAttributes[index] = attribute.Int(fmt.Sprintf("key-%d", index), index)
	}
	tooManyStrings := make([]string, maxProjectionAttributeSliceItems+1)
	largeBudgetStrings := make([]string, maxProjectionAttributeSliceItems)
	for index := range largeBudgetStrings {
		largeBudgetStrings[index] = strings.Repeat("v", maxProjectionAttributeValueBytes)
	}
	nested := attribute.StringValue("value")
	for range maxProjectionAttributeDepth + 1 {
		nested = attribute.SliceValue(nested)
	}

	cases := map[string]Config{
		"nan sample ratio": func() Config {
			config := validTelemetryConfig()
			config.SampleRatio = math.NaN()
			return config
		}(),
		"typed nil sampler": func() Config {
			config := validTelemetryConfig()
			config.Sampler = (*typedNilSampler)(nil)
			return config
		}(),
		"batch queue upper bound": func() Config {
			config := validTelemetryConfig()
			config.BatchQueueSize = maxBatchQueueSize + 1
			return config
		}(),
		"batch size upper bound": func() Config {
			config := validTelemetryConfig()
			config.BatchQueueSize = maxBatchQueueSize
			config.BatchSize = maxBatchSize + 1
			return config
		}(),
		"view upper bound": func() Config {
			config := validTelemetryConfig()
			config.Views = make([]sdkmetric.View, maxViews+1)
			return config
		}(),
		"trace layer upper bound": func() Config {
			config := validTelemetryConfig()
			config.TraceProjectionLayers = make([]TraceProjectionLayer, maxProjectionLayers+1)
			return config
		}(),
		"metric layer upper bound": func() Config {
			config := validTelemetryConfig()
			config.MetricProjectionLayers = make([]MetricProjectionLayer, maxProjectionLayers+1)
			return config
		}(),
		"scope upper bound": func() Config {
			config := validTelemetryConfig()
			config.TraceProjectionLayers = []TraceProjectionLayer{{Scopes: tooManyScopes, Wrap: tracePass}}
			return config
		}(),
		"scope name bytes":       withScope(instrumentation.Scope{Name: strings.Repeat("n", maxProjectionScopeNameBytes+1)}),
		"scope version bytes":    withScope(instrumentation.Scope{Name: "scope", Version: strings.Repeat("v", maxProjectionVersionBytes+1)}),
		"scope schema bytes":     withScope(instrumentation.Scope{Name: "scope", SchemaURL: "https://example.com/" + strings.Repeat("s", maxProjectionSchemaURLBytes)}),
		"scope attribute count":  withScope(instrumentation.Scope{Name: "scope", Attributes: attribute.NewSet(tooManyAttributes...)}),
		"scope attribute key":    withScope(instrumentation.Scope{Name: "scope", Attributes: attribute.NewSet(attribute.String(strings.Repeat("k", maxProjectionAttributeKeyBytes+1), "value"))}),
		"scope attribute string": withScope(instrumentation.Scope{Name: "scope", Attributes: attribute.NewSet(attribute.String("key", strings.Repeat("v", maxProjectionAttributeValueBytes+1)))}),
		"scope attribute slice":  withScope(instrumentation.Scope{Name: "scope", Attributes: attribute.NewSet(attribute.StringSlice("key", tooManyStrings))}),
		"scope attribute bytes":  withScope(instrumentation.Scope{Name: "scope", Attributes: attribute.NewSet(attribute.ByteSlice("key", make([]byte, maxProjectionAttributeValueBytes+1)))}),
		"scope attribute float":  withScope(instrumentation.Scope{Name: "scope", Attributes: attribute.NewSet(attribute.Float64("key", math.Inf(1)))}),
		"scope attribute depth":  withScope(instrumentation.Scope{Name: "scope", Attributes: attribute.NewSet(attribute.KeyValue{Key: "key", Value: nested})}),
		"scope attribute budget": withScope(instrumentation.Scope{Name: "scope", Attributes: attribute.NewSet(attribute.StringSlice("key", largeBudgetStrings))}),
		"invalid utf8 scope":     withScope(instrumentation.Scope{Name: string([]byte{0xff})}),
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeTelemetryConfig(config); !errors.Is(err, ErrInvalidTelemetryConfig) {
				t.Fatalf("normalize = %v", err)
			}
		})
	}

	valid := validTelemetryConfig()
	valid.TraceProjectionLayers = []TraceProjectionLayer{{
		Scopes: []instrumentation.Scope{{
			Name:       strings.Repeat("n", maxProjectionScopeNameBytes),
			Version:    strings.Repeat("v", maxProjectionVersionBytes),
			SchemaURL:  "https://example.com/schema",
			Attributes: attribute.NewSet(attribute.String("key", strings.Repeat("v", maxProjectionAttributeValueBytes))),
		}},
		Wrap: tracePass,
	}}
	valid.MetricProjectionLayers = []MetricProjectionLayer{{
		Scopes: []instrumentation.Scope{{Name: "metric", Attributes: attribute.NewSet(attribute.Bool("enabled", true))}},
		Wrap:   metricPass,
	}}
	if _, err := normalizeTelemetryConfig(valid); err != nil {
		t.Fatalf("valid boundary config = %v", err)
	}
}

func TestTelemetryRejectsTypedNilContexts(t *testing.T) {
	var ctx *typedNilProductionContext
	factoryCalled := false
	telemetry, err := newTelemetry(ctx, validTelemetryConfig(), telemetryFactories{
		resource: func(context.Context, Config) (*resource.Resource, error) {
			factoryCalled = true
			return resource.Empty(), nil
		},
		traceExporter:  func(context.Context) (sdktrace.SpanExporter, error) { return &spanExporterStub{}, nil },
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) { return &metricExporterStub{}, nil },
	})
	if telemetry != nil || !errors.Is(err, ErrInvalidTelemetryConfig) || factoryCalled {
		t.Fatalf("NewTelemetry = %#v, %v; factory called = %t", telemetry, err, factoryCalled)
	}
	lifecycle := newTelemetryLifecycle(&hostileLifecycleProvider{}, &hostileLifecycleProvider{}, time.Second, time.Second)
	if err := lifecycle.ForceFlush(ctx); !errors.Is(err, ErrTelemetryClosed) {
		t.Fatalf("ForceFlush = %v", err)
	}
	if err := lifecycle.Shutdown(ctx); !errors.Is(err, ErrTelemetryClosed) {
		t.Fatalf("Shutdown = %v", err)
	}
}

func TestNewTelemetryRejectsNilSuccessfulFactoryProducts(t *testing.T) {
	t.Run("resource", func(t *testing.T) {
		traceCalled := false
		telemetry, err := newTelemetry(t.Context(), validTelemetryConfig(), telemetryFactories{
			resource: func(context.Context, Config) (*resource.Resource, error) { return nil, nil },
			traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
				traceCalled = true
				return &spanExporterStub{}, nil
			},
			metricExporter: func(context.Context) (sdkmetric.Exporter, error) { return &metricExporterStub{}, nil },
		})
		if telemetry != nil || !errors.Is(err, ErrInvalidTelemetryConfig) || traceCalled {
			t.Fatalf("result = %#v, %v; trace called = %t", telemetry, err, traceCalled)
		}
	})

	for _, typed := range []bool{false, true} {
		t.Run(fmt.Sprintf("trace typed=%t", typed), func(t *testing.T) {
			metricCalled := false
			telemetry, err := newTelemetry(t.Context(), validTelemetryConfig(), telemetryFactories{
				resource: func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
				traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
					if typed {
						return (*spanExporterStub)(nil), nil
					}
					return nil, nil
				},
				metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
					metricCalled = true
					return &metricExporterStub{}, nil
				},
			})
			if telemetry != nil || !errors.Is(err, ErrInvalidTelemetryConfig) || metricCalled {
				t.Fatalf("result = %#v, %v; metric called = %t", telemetry, err, metricCalled)
			}
		})

		t.Run(fmt.Sprintf("metric typed=%t", typed), func(t *testing.T) {
			traceExporter := &spanExporterStub{}
			telemetry, err := newTelemetry(t.Context(), validTelemetryConfig(), telemetryFactories{
				resource:      func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
				traceExporter: func(context.Context) (sdktrace.SpanExporter, error) { return traceExporter, nil },
				metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
					if typed {
						return (*metricExporterStub)(nil), nil
					}
					return nil, nil
				},
			})
			if telemetry != nil || !errors.Is(err, ErrInvalidTelemetryConfig) || traceExporter.shutdownCalls.Load() != 1 {
				t.Fatalf("result = %#v, %v; cleanup = %d", telemetry, err, traceExporter.shutdownCalls.Load())
			}
		})
	}
}

func TestNewTelemetryContainsFactoryAndProjectionPanics(t *testing.T) {
	t.Run("resource factory", func(t *testing.T) {
		telemetry, err := newTelemetry(t.Context(), validTelemetryConfig(), telemetryFactories{
			resource:       func(context.Context, Config) (*resource.Resource, error) { panic(nil) },
			traceExporter:  func(context.Context) (sdktrace.SpanExporter, error) { return &spanExporterStub{}, nil },
			metricExporter: func(context.Context) (sdkmetric.Exporter, error) { return &metricExporterStub{}, nil },
		})
		if telemetry != nil || !errors.Is(err, ErrTelemetryAssembly) {
			t.Fatalf("result = %#v, %v", telemetry, err)
		}
	})

	t.Run("trace factory", func(t *testing.T) {
		metricCalled := false
		telemetry, err := newTelemetry(t.Context(), validTelemetryConfig(), telemetryFactories{
			resource:      func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
			traceExporter: func(context.Context) (sdktrace.SpanExporter, error) { panic("trace") },
			metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
				metricCalled = true
				return &metricExporterStub{}, nil
			},
		})
		if telemetry != nil || !errors.Is(err, ErrTelemetryAssembly) || metricCalled {
			t.Fatalf("result = %#v, %v; metric called = %t", telemetry, err, metricCalled)
		}
	})

	t.Run("metric factory", func(t *testing.T) {
		traceExporter := &spanExporterStub{}
		telemetry, err := newTelemetry(t.Context(), validTelemetryConfig(), telemetryFactories{
			resource:       func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
			traceExporter:  func(context.Context) (sdktrace.SpanExporter, error) { return traceExporter, nil },
			metricExporter: func(context.Context) (sdkmetric.Exporter, error) { panic("metric") },
		})
		if telemetry != nil || !errors.Is(err, ErrTelemetryAssembly) || traceExporter.shutdownCalls.Load() != 1 {
			t.Fatalf("result = %#v, %v; cleanup = %d", telemetry, err, traceExporter.shutdownCalls.Load())
		}
	})

	t.Run("trace projection", func(t *testing.T) {
		traceExporter := &spanExporterStub{}
		config := validTelemetryConfig()
		config.TraceProjectionLayers = []TraceProjectionLayer{{
			Scopes: []instrumentation.Scope{{Name: "panic.trace"}},
			Wrap:   func(sdktrace.SpanExporter) (sdktrace.SpanExporter, error) { panic(nil) },
		}}
		telemetry, err := newTelemetry(t.Context(), config, telemetryFactories{
			resource:       func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
			traceExporter:  func(context.Context) (sdktrace.SpanExporter, error) { return traceExporter, nil },
			metricExporter: func(context.Context) (sdkmetric.Exporter, error) { return &metricExporterStub{}, nil },
		})
		if telemetry != nil || !errors.Is(err, ErrTelemetryAssembly) || traceExporter.shutdownCalls.Load() != 1 {
			t.Fatalf("result = %#v, %v; cleanup = %d", telemetry, err, traceExporter.shutdownCalls.Load())
		}
	})

	t.Run("metric projection", func(t *testing.T) {
		traceExporter := &spanExporterStub{}
		metricExporter := &metricExporterStub{}
		config := validTelemetryConfig()
		config.MetricProjectionLayers = []MetricProjectionLayer{{
			Scopes: []instrumentation.Scope{{Name: "panic.metric"}},
			Wrap:   func(sdkmetric.Exporter) (sdkmetric.Exporter, error) { panic("metric projection") },
		}}
		telemetry, err := newTelemetry(t.Context(), config, telemetryFactories{
			resource:       func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
			traceExporter:  func(context.Context) (sdktrace.SpanExporter, error) { return traceExporter, nil },
			metricExporter: func(context.Context) (sdkmetric.Exporter, error) { return metricExporter, nil },
		})
		if telemetry != nil || !errors.Is(err, ErrTelemetryAssembly) || traceExporter.shutdownCalls.Load() != 1 || metricExporter.shutdownCalls.Load() != 1 {
			t.Fatalf("result = %#v, %v; cleanup = %d/%d", telemetry, err, traceExporter.shutdownCalls.Load(), metricExporter.shutdownCalls.Load())
		}
	})
}

func TestNewTelemetryContainsRollbackPanic(t *testing.T) {
	factoryErr := errors.New("metric factory")
	traceExporter := &panicShutdownSpanExporter{}
	telemetry, err := newTelemetry(t.Context(), validTelemetryConfig(), telemetryFactories{
		resource:       func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
		traceExporter:  func(context.Context) (sdktrace.SpanExporter, error) { return traceExporter, nil },
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) { return nil, factoryErr },
	})
	if telemetry != nil || !errors.Is(err, factoryErr) || !errors.Is(err, ErrTelemetryLifecycle) || traceExporter.shutdownCalls.Load() != 1 {
		t.Fatalf("result = %#v, %v; cleanup = %d", telemetry, err, traceExporter.shutdownCalls.Load())
	}
}

func TestTelemetryLifecycleContainsPanicsAndReleasesState(t *testing.T) {
	t.Run("flush", func(t *testing.T) {
		traceProvider := &hostileLifecycleProvider{panicFlush: true}
		metricProvider := &hostileLifecycleProvider{}
		lifecycle := newTelemetryLifecycle(traceProvider, metricProvider, time.Second, time.Second)
		if err := lifecycle.ForceFlush(t.Context()); !errors.Is(err, ErrTelemetryLifecycle) {
			t.Fatalf("ForceFlush = %v", err)
		}
		if traceProvider.flushCalls.Load() != 1 || metricProvider.flushCalls.Load() != 1 {
			t.Fatalf("flush calls = %d/%d", traceProvider.flushCalls.Load(), metricProvider.flushCalls.Load())
		}
		shutdownDone := make(chan error, 1)
		go func() { shutdownDone <- lifecycle.Shutdown(context.Background()) }()
		select {
		case err := <-shutdownDone:
			if err != nil {
				t.Fatalf("Shutdown = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Shutdown remained blocked by panicking flush")
		}
	})

	t.Run("shutdown", func(t *testing.T) {
		metricErr := errors.New("metric shutdown")
		traceProvider := &hostileLifecycleProvider{panicShutdown: true}
		metricProvider := &hostileLifecycleProvider{shutdownErr: metricErr}
		lifecycle := newTelemetryLifecycle(traceProvider, metricProvider, time.Second, time.Second)
		err := lifecycle.Shutdown(t.Context())
		if !errors.Is(err, ErrTelemetryLifecycle) || !errors.Is(err, metricErr) {
			t.Fatalf("Shutdown = %v", err)
		}
		if traceProvider.shutdownCalls.Load() != 1 || metricProvider.shutdownCalls.Load() != 1 {
			t.Fatalf("shutdown calls = %d/%d", traceProvider.shutdownCalls.Load(), metricProvider.shutdownCalls.Load())
		}
		if cached := lifecycle.Shutdown(t.Context()); cached != err {
			t.Fatalf("cached result identity changed: %p != %p", cached, err)
		}
	})
}

func TestTelemetryLifecycleGoexitCannotStrandState(t *testing.T) {
	t.Run("flush", func(t *testing.T) {
		traceProvider := &hostileLifecycleProvider{goexitFlush: true}
		metricProvider := &hostileLifecycleProvider{}
		lifecycle := newTelemetryLifecycle(traceProvider, metricProvider, time.Second, time.Second)
		flushExited := make(chan struct{})
		go func() {
			defer close(flushExited)
			_ = lifecycle.ForceFlush(context.Background())
		}()
		select {
		case <-flushExited:
		case <-time.After(2 * time.Second):
			t.Fatal("Goexit flush did not unwind")
		}
		shutdownDone := make(chan error, 1)
		go func() { shutdownDone <- lifecycle.Shutdown(context.Background()) }()
		select {
		case err := <-shutdownDone:
			if err != nil {
				t.Fatalf("Shutdown = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Shutdown stranded after Goexit flush")
		}
		if metricProvider.flushCalls.Load() != 0 {
			t.Fatalf("metric flush calls after trace Goexit = %d", metricProvider.flushCalls.Load())
		}
	})

	t.Run("shutdown", func(t *testing.T) {
		traceProvider := &hostileLifecycleProvider{goexitShutdown: true}
		metricProvider := &hostileLifecycleProvider{}
		lifecycle := newTelemetryLifecycle(traceProvider, metricProvider, time.Second, time.Second)
		ownerExited := make(chan struct{})
		go func() {
			defer close(ownerExited)
			_ = lifecycle.Shutdown(context.Background())
		}()
		select {
		case <-ownerExited:
		case <-time.After(2 * time.Second):
			t.Fatal("Goexit shutdown did not unwind")
		}
		if err := lifecycle.Shutdown(t.Context()); !errors.Is(err, ErrTelemetryLifecycle) {
			t.Fatalf("cached Shutdown = %v", err)
		}
		if traceProvider.shutdownCalls.Load() != 1 || metricProvider.shutdownCalls.Load() != 0 {
			t.Fatalf("shutdown calls = %d/%d", traceProvider.shutdownCalls.Load(), metricProvider.shutdownCalls.Load())
		}
	})
}

var _ sdkmetric.Exporter = (*metricExporterStub)(nil)
var _ sdktrace.SpanExporter = (*panicShutdownSpanExporter)(nil)
