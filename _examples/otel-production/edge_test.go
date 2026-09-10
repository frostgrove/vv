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

	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
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
		"batch timeout upper bound": func() Config {
			config := validTelemetryConfig()
			config.BatchTimeout = maxBatchTimeout + time.Nanosecond
			return config
		}(),
		"metric interval upper bound": func() Config {
			config := validTelemetryConfig()
			config.MetricInterval = maxMetricInterval + time.Nanosecond
			return config
		}(),
		"metric export timeout upper bound": func() Config {
			config := validTelemetryConfig()
			config.MetricExportTimeout = maxMetricExportTimeout + time.Nanosecond
			return config
		}(),
		"force flush timeout upper bound": func() Config {
			config := validTelemetryConfig()
			config.ForceFlushTimeout = maxForceFlushTimeout + time.Nanosecond
			return config
		}(),
		"provider close timeout upper bound": func() Config {
			config := validTelemetryConfig()
			config.ProviderCloseTimeout = maxProviderCloseTimeout + time.Nanosecond
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
		"framework resource raw count": func() Config {
			config := validTelemetryConfig()
			config.FrameworkResources = make([]vvotel.ApprovedName, vvotel.MaxResourceNameValues+1)
			for index := range config.FrameworkResources {
				config.FrameworkResources[index] = config.FrameworkResource
			}
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

func TestNewTelemetryGoexitRollsBackOwnedAssembly(t *testing.T) {
	t.Run("trace projection", func(t *testing.T) {
		traceExporter := &spanExporterStub{}
		metricCalled := false
		config := validTelemetryConfig()
		config.TraceProjectionLayers = []TraceProjectionLayer{{
			Scopes: []instrumentation.Scope{{Name: "goexit.trace"}},
			Wrap: func(sdktrace.SpanExporter) (sdktrace.SpanExporter, error) {
				runtime.Goexit()
				return nil, nil
			},
		}}
		exited := make(chan struct{})
		go func() {
			defer close(exited)
			_, _ = newTelemetry(context.Background(), config, telemetryFactories{
				resource:      func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
				traceExporter: func(context.Context) (sdktrace.SpanExporter, error) { return traceExporter, nil },
				metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
					metricCalled = true
					return &metricExporterStub{}, nil
				},
			})
		}()
		awaitGoexit(t, exited)
		if traceExporter.shutdownCalls.Load() != 1 || metricCalled {
			t.Fatalf("trace cleanup = %d; metric called = %t", traceExporter.shutdownCalls.Load(), metricCalled)
		}
	})

	t.Run("metric projection", func(t *testing.T) {
		traceExporter := &spanExporterStub{}
		metricExporter := &metricExporterStub{}
		config := validTelemetryConfig()
		config.MetricProjectionLayers = []MetricProjectionLayer{{
			Scopes: []instrumentation.Scope{{Name: "goexit.metric"}},
			Wrap: func(sdkmetric.Exporter) (sdkmetric.Exporter, error) {
				runtime.Goexit()
				return nil, nil
			},
		}}
		exited := make(chan struct{})
		go func() {
			defer close(exited)
			_, _ = newTelemetry(context.Background(), config, telemetryFactories{
				resource:       func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
				traceExporter:  func(context.Context) (sdktrace.SpanExporter, error) { return traceExporter, nil },
				metricExporter: func(context.Context) (sdkmetric.Exporter, error) { return metricExporter, nil },
			})
		}()
		awaitGoexit(t, exited)
		if traceExporter.shutdownCalls.Load() != 1 || metricExporter.shutdownCalls.Load() != 1 {
			t.Fatalf("cleanup = %d/%d", traceExporter.shutdownCalls.Load(), metricExporter.shutdownCalls.Load())
		}
	})

	t.Run("view", func(t *testing.T) {
		traceExporter := &spanExporterStub{}
		metricExporter := &metricExporterStub{}
		config := validTelemetryConfig()
		config.Views = []sdkmetric.View{func(sdkmetric.Instrument) (sdkmetric.Stream, bool) {
			runtime.Goexit()
			return sdkmetric.Stream{}, false
		}}
		exited := make(chan struct{})
		go func() {
			defer close(exited)
			_, _ = newTelemetry(context.Background(), config, telemetryFactories{
				resource:       func(context.Context, Config) (*resource.Resource, error) { return resource.Empty(), nil },
				traceExporter:  func(context.Context) (sdktrace.SpanExporter, error) { return traceExporter, nil },
				metricExporter: func(context.Context) (sdkmetric.Exporter, error) { return metricExporter, nil },
			})
		}()
		awaitGoexit(t, exited)
		if traceExporter.shutdownCalls.Load() != 1 || metricExporter.shutdownCalls.Load() != 1 {
			t.Fatalf("cleanup = %d/%d", traceExporter.shutdownCalls.Load(), metricExporter.shutdownCalls.Load())
		}
	})
}

func awaitGoexit(t *testing.T, exited <-chan struct{}) {
	t.Helper()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("Goexit path did not unwind")
	}
}

func TestMetricProjectionEnforcesDescriptorAggregation(t *testing.T) {
	projection := newExportProjection(validTelemetryConfig())
	descriptor := projection.metricSignals[vvotel.MetricJobsWorkerDeliveryResults][0]
	attributes := attribute.NewSet(
		vvotel.AttrComponent.String(vvotel.ComponentJobsWorker),
		vvotel.AttrControl.String("none"),
		vvotel.AttrMutation.String("applied"),
		vvotel.AttrOperationName.String("renew"),
	)
	point := metricdata.DataPoint[int64]{Attributes: attributes, Value: 1024}
	valid := metricdata.Sum[int64]{
		DataPoints:  []metricdata.DataPoint[int64]{point},
		Temporality: metricdata.CumulativeTemporality,
		IsMonotonic: true,
	}
	projected, ok := projection.projectAggregation(valid, descriptor)
	if !ok || projected.(metricdata.Sum[int64]).DataPoints[0].Value != 1024 {
		t.Fatalf("valid cumulative aggregate = %#v, %t", projected, ok)
	}
	invalid := []metricdata.Aggregation{
		metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{point}},
		metricdata.Sum[int64]{DataPoints: []metricdata.DataPoint[int64]{point}, Temporality: metricdata.CumulativeTemporality},
		metricdata.Sum[int64]{DataPoints: []metricdata.DataPoint[int64]{point}, Temporality: metricdata.DeltaTemporality, IsMonotonic: true},
		metricdata.Sum[int64]{DataPoints: []metricdata.DataPoint[int64]{point}, Temporality: metricdata.Temporality(255), IsMonotonic: true},
		metricdata.Sum[int64]{DataPoints: []metricdata.DataPoint[int64]{{Attributes: attributes, Value: -1}}, Temporality: metricdata.CumulativeTemporality, IsMonotonic: true},
		metricdata.Histogram[int64]{DataPoints: []metricdata.HistogramDataPoint[int64]{{Attributes: attributes, Count: 1, BucketCounts: []uint64{1}}}, Temporality: metricdata.CumulativeTemporality},
	}
	for index, candidate := range invalid {
		if got, accepted := projection.projectAggregation(candidate, descriptor); accepted {
			t.Fatalf("invalid aggregation %d = %#v, %t", index, got, accepted)
		}
	}
}

func TestMetricProjectionRejectsMalformedHistogramWireData(t *testing.T) {
	projection := newExportProjection(validTelemetryConfig())
	descriptor := projection.metricSignals[vvotel.MetricStorageOperationBytes][0]
	attributes := attribute.NewSet(
		vvotel.AttrComponent.String(vvotel.ComponentStorage),
		vvotel.AttrOperationName.String(vvotel.OpStoragePut),
		vvotel.AttrOperationOutcome.String(vvotel.OutcomeOk),
	)
	validPoint := metricdata.HistogramDataPoint[int64]{
		Attributes:   attributes,
		Count:        1,
		Sum:          1,
		BucketCounts: []uint64{1},
		Min:          metricdata.NewExtrema[int64](1),
		Max:          metricdata.NewExtrema[int64](1),
	}
	valid := metricdata.Histogram[int64]{DataPoints: []metricdata.HistogramDataPoint[int64]{validPoint}, Temporality: metricdata.CumulativeTemporality}
	if _, ok := projection.projectAggregation(valid, descriptor); !ok {
		t.Fatal("valid histogram was rejected")
	}

	badBucketLength := validPoint
	badBucketLength.Bounds = []float64{1}
	badCount := validPoint
	badCount.Count = 2
	badBounds := validPoint
	badBounds.Bounds = []float64{2, 1}
	badBounds.BucketCounts = []uint64{0, 0, 1}
	badNaNBound := validPoint
	badNaNBound.Bounds = []float64{math.NaN()}
	badNaNBound.BucketCounts = []uint64{0, 1}
	badExtrema := validPoint
	badExtrema.Min = metricdata.NewExtrema[int64](2)
	badExtrema.Max = metricdata.NewExtrema[int64](1)
	badDomain := validPoint
	badDomain.Min = metricdata.NewExtrema[int64](-1)
	badSum := validPoint
	badSum.Sum = -1
	badSum.Min = metricdata.Extrema[int64]{}
	badSum.Max = metricdata.Extrema[int64]{}
	tooMany := make([]metricdata.HistogramDataPoint[int64], descriptor.SeriesBudget+1)
	for index := range tooMany {
		tooMany[index] = validPoint
	}
	invalid := []metricdata.Aggregation{
		metricdata.Histogram[int64]{DataPoints: []metricdata.HistogramDataPoint[int64]{badBucketLength}, Temporality: metricdata.CumulativeTemporality},
		metricdata.Histogram[int64]{DataPoints: []metricdata.HistogramDataPoint[int64]{badCount}, Temporality: metricdata.CumulativeTemporality},
		metricdata.Histogram[int64]{DataPoints: []metricdata.HistogramDataPoint[int64]{badBounds}, Temporality: metricdata.CumulativeTemporality},
		metricdata.Histogram[int64]{DataPoints: []metricdata.HistogramDataPoint[int64]{badNaNBound}, Temporality: metricdata.CumulativeTemporality},
		metricdata.Histogram[int64]{DataPoints: []metricdata.HistogramDataPoint[int64]{badExtrema}, Temporality: metricdata.CumulativeTemporality},
		metricdata.Histogram[int64]{DataPoints: []metricdata.HistogramDataPoint[int64]{badDomain}, Temporality: metricdata.CumulativeTemporality},
		metricdata.Histogram[int64]{DataPoints: []metricdata.HistogramDataPoint[int64]{badSum}, Temporality: metricdata.CumulativeTemporality},
		metricdata.Histogram[int64]{DataPoints: tooMany, Temporality: metricdata.CumulativeTemporality},
		metricdata.ExponentialHistogram[int64]{DataPoints: []metricdata.ExponentialHistogramDataPoint[int64]{{Attributes: attributes, Count: 1, Sum: 1, PositiveBucket: metricdata.ExponentialBucket{Counts: []uint64{1}}}}, Temporality: metricdata.CumulativeTemporality},
	}
	for index, candidate := range invalid {
		if value, ok := projection.projectAggregation(candidate, descriptor); ok {
			t.Fatalf("invalid histogram %d = %#v", index, value)
		}
	}
}

func TestDefaultMetricExporterForcesStableMetricWireContract(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "delta")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION", "base2_exponential_bucket_histogram")
	exporter, err := defaultTelemetryFactories().metricExporter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })
	for kind := sdkmetric.InstrumentKindCounter; kind <= sdkmetric.InstrumentKindGauge; kind++ {
		if got := exporter.Temporality(kind); got != metricdata.CumulativeTemporality {
			t.Fatalf("temporality(%v) = %v", kind, got)
		}
	}
	if _, ok := exporter.Aggregation(sdkmetric.InstrumentKindHistogram).(sdkmetric.AggregationExplicitBucketHistogram); !ok {
		t.Fatalf("histogram aggregation = %T", exporter.Aggregation(sdkmetric.InstrumentKindHistogram))
	}
}

func TestMetricProjectionFiltersMalformedExemplarsAndClonesNewAttributeTypes(t *testing.T) {
	projection := newExportProjection(validTelemetryConfig())
	descriptor := projection.metricSignals[vvotel.MetricStorageOperationBytes][0]
	validTraceID := append([]byte{1}, make([]byte, 15)...)
	validSpanID := append([]byte{2}, make([]byte, 7)...)
	exemplars := []metricdata.Exemplar[int64]{
		{Value: 1, TraceID: validTraceID, SpanID: validSpanID},
		{Value: -1, TraceID: validTraceID, SpanID: validSpanID},
		{Value: 1, TraceID: make([]byte, 16), SpanID: make([]byte, 8)},
		{Value: 1, TraceID: []byte{1}, SpanID: []byte{2}},
	}
	projected := projectExemplars(projection, exemplars, descriptor)
	if len(projected) != 1 || len(projected[0].TraceID) != 16 || len(projected[0].SpanID) != 8 {
		t.Fatalf("projected exemplars=%#v", projected)
	}
	validTraceID[0] = 9
	validSpanID[0] = 9
	if projected[0].TraceID[0] != 1 || projected[0].SpanID[0] != 2 {
		t.Fatal("projected exemplar retained identifier slices")
	}

	value := attribute.SliceValue(
		attribute.ByteSliceValue([]byte{1, 2, 3}),
		attribute.SliceValue(attribute.StringValue("nested")),
	)
	cloned := cloneAttribute(attribute.KeyValue{Key: "nested", Value: value})
	if cloned.Value.Type() != attribute.SLICE || len(cloned.Value.AsSlice()) != 2 || cloned.Value.AsSlice()[0].Type() != attribute.BYTESLICE || cloned.Value.AsSlice()[1].Type() != attribute.SLICE {
		t.Fatalf("cloned value=%#v", cloned.Value)
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
		if metricProvider.flushCalls.Load() != 1 {
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
		if traceProvider.shutdownCalls.Load() != 1 || metricProvider.shutdownCalls.Load() != 1 {
			t.Fatalf("shutdown calls = %d/%d", traceProvider.shutdownCalls.Load(), metricProvider.shutdownCalls.Load())
		}
	})
}

var _ sdkmetric.Exporter = (*metricExporterStub)(nil)
var _ sdktrace.SpanExporter = (*panicShutdownSpanExporter)(nil)
