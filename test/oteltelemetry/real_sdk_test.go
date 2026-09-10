package oteltelemetry_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
	vvotel "github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/storage"
	otelglobal "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	collectormetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type testModel struct {
	ID string
}

type serviceStub struct {
	get func(context.Context, port.GetCommand[string]) (testModel, error)
}

const (
	cacheNameCanary         = "cache-name-secret-59241"
	cacheReasonCanary       = "cache-reason-secret-21495"
	cacheMemoryReasonCanary = "cache-memory-reason-secret-73126"
	storageKeyCanary        = "private/storage-key-secret-64820.bin"
	storageSourceCanary     = "storage-source-secret-38016"
	storageErrorCanary      = "storage-error-secret-90537"
)

type privacyCanarySource struct {
	secret      string
	readCalls   atomic.Int64
	stringCalls atomic.Int64
}

func (s *privacyCanarySource) Read([]byte) (int, error) {
	s.readCalls.Add(1)
	return 0, errors.New(s.secret)
}

func (s *privacyCanarySource) String() string {
	s.stringCalls.Add(1)
	return s.secret
}

type privacyStorageStore struct {
	err        error
	putCalls   int
	putContext context.Context
	putKey     storage.Key
	putSource  io.Reader
	putOptions storage.PutOptions
}

func (s *privacyStorageStore) Put(ctx context.Context, key storage.Key, source io.Reader, options storage.PutOptions) (storage.Info, error) {
	s.putCalls++
	s.putContext = ctx
	s.putKey = key
	s.putSource = source
	s.putOptions = options
	return storage.Info{}, s.err
}

func (*privacyStorageStore) Open(context.Context, storage.Key, storage.ReadOptions) (io.ReadCloser, storage.Info, error) {
	panic("unexpected storage Open")
}

func (*privacyStorageStore) Head(context.Context, storage.Key) (storage.Info, error) {
	panic("unexpected storage Head")
}

func (*privacyStorageStore) Delete(context.Context, storage.Key, storage.DeleteOptions) error {
	panic("unexpected storage Delete")
}

func (*privacyStorageStore) Stage(context.Context, io.Reader, storage.StageOptions) (storage.Staged, error) {
	panic("unexpected storage Stage")
}

func (*privacyStorageStore) Promote(context.Context, storage.StageID, storage.Key, storage.PromoteOptions) (storage.Info, error) {
	panic("unexpected storage Promote")
}

func (*privacyStorageStore) Abort(context.Context, storage.StageID) error {
	panic("unexpected storage Abort")
}

func (*privacyStorageStore) CleanupExpired(context.Context, storage.CleanupOptions) (storage.CleanupResult, error) {
	panic("unexpected storage CleanupExpired")
}

func (*privacyStorageStore) TemporaryURL(context.Context, storage.Key, storage.TemporaryURLOptions) (storage.Link, error) {
	panic("unexpected storage TemporaryURL")
}

func (*privacyStorageStore) Capabilities() storage.Capabilities { return storage.Capabilities{} }

type privacyScenario struct {
	cacheObserver       cache.Observer
	cacheMemoryObserver cachememory.Observer
	storage             storage.Store
	rawStorage          *privacyStorageStore
	key                 storage.Key
	source              *privacyCanarySource
	storageErr          error
	gotStorageErr       error
}

func newPrivacyScenario(t *testing.T, tel *vvotel.Telemetry) *privacyScenario {
	t.Helper()
	key, err := storage.ParseKey(storageKeyCanary)
	if err != nil {
		t.Fatalf("parse storage privacy canary key: %v", err)
	}
	storageErr := errors.New(storageErrorCanary)
	rawStorage := &privacyStorageStore{err: storageErr}
	return &privacyScenario{
		cacheObserver:       vvotel.Cache(tel, vvotel.WithCacheSpanEvents(true)),
		cacheMemoryObserver: vvotel.CacheMemory(tel, vvotel.WithCacheMemorySpanEvents(true)),
		storage:             vvotel.Store(tel)(rawStorage),
		rawStorage:          rawStorage,
		key:                 key,
		source:              &privacyCanarySource{secret: storageSourceCanary},
		storageErr:          storageErr,
	}
}

func (s *privacyScenario) emit(ctx context.Context) {
	s.cacheObserver.Observe(ctx, cache.Event{
		Cache:        cacheNameCanary,
		Operation:    cache.LookupOperation,
		Outcome:      cache.HitOutcome,
		Items:        937,
		EncodedBytes: 8142,
		PayloadBytes: 4815,
		Memoized:     true,
	})
	s.cacheMemoryObserver.Observe(ctx, cachememory.Event{
		Operation:    cachememory.PutOperation,
		Outcome:      cachememory.StoredOutcome,
		Items:        619,
		ValueBytes:   2718,
		ChargedBytes: 3141,
	})
	s.cacheObserver.Observe(ctx, cache.Event{Operation: cache.LookupOperation, Outcome: cache.HitOutcome, Reason: cache.Reason(cacheReasonCanary)})
	s.cacheMemoryObserver.Observe(ctx, cachememory.Event{Operation: cachememory.PutOperation, Outcome: cachememory.StoredOutcome, Reason: cachememory.Reason(cacheMemoryReasonCanary)})
	_, s.gotStorageErr = s.storage.Put(ctx, s.key, s.source, storage.PutOptions{})
}

func (s *privacyScenario) assertPreserved(t *testing.T) {
	t.Helper()
	if s.gotStorageErr != s.storageErr {
		t.Fatal("storage telemetry changed the private error identity")
	}
	if s.rawStorage.putCalls != 1 {
		t.Fatalf("storage Put calls = %d, want 1", s.rawStorage.putCalls)
	}
	if s.rawStorage.putKey != s.key {
		t.Fatal("storage telemetry changed the private key")
	}
	if s.rawStorage.putSource != s.source {
		t.Fatal("storage telemetry replaced the private source")
	}
	if !reflect.DeepEqual(s.rawStorage.putOptions, storage.PutOptions{}) {
		t.Fatalf("storage telemetry changed Put options: %#v", s.rawStorage.putOptions)
	}
	if s.source.readCalls.Load() != 0 {
		t.Fatalf("storage telemetry read the private source %d times, want none", s.source.readCalls.Load())
	}
	if s.source.stringCalls.Load() != 0 {
		t.Fatalf("storage telemetry formatted the private source %d times, want none", s.source.stringCalls.Load())
	}
}

func (s *privacyScenario) secrets(commandSecret string) []string {
	return []string{
		commandSecret,
		cacheNameCanary,
		cacheReasonCanary,
		cacheMemoryReasonCanary,
		storageKeyCanary,
		storageSourceCanary,
		storageErrorCanary,
	}
}

func (s *serviceStub) Meta() *crud.Meta     { return &crud.Meta{} }
func (s *serviceStub) Paths() errs.Resolver { return nil }

func (s *serviceStub) List(context.Context, port.ListCommand) (crud.PaginatedResponse[testModel], error) {
	return crud.PaginatedResponse[testModel]{}, nil
}

func (s *serviceStub) Count(context.Context, port.CountCommand) (int64, error) {
	return 0, nil
}

func (s *serviceStub) Get(ctx context.Context, cmd port.GetCommand[string]) (testModel, error) {
	if s.get != nil {
		return s.get(ctx, cmd)
	}
	return testModel{ID: cmd.ID}, nil
}

func (s *serviceStub) Create(context.Context, port.CreateCommand[testModel]) (testModel, error) {
	return testModel{}, nil
}

func (s *serviceStub) Update(context.Context, port.UpdateCommand[string, testModel]) (testModel, error) {
	return testModel{}, nil
}

func (s *serviceStub) Replace(context.Context, port.ReplaceCommand[string, testModel]) (testModel, error) {
	return testModel{}, nil
}

func (s *serviceStub) Delete(context.Context, port.DeleteCommand[string]) (int64, error) {
	return 0, nil
}

func (s *serviceStub) DeleteMany(context.Context, port.BulkDeleteCommand[string]) (int64, error) {
	return 0, nil
}

type sdkFixture struct {
	spans          *tracetest.SpanRecorder
	metrics        *sdkmetric.ManualReader
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
}

func newSDKFixture(t *testing.T, sampler sdktrace.Sampler) *sdkFixture {
	t.Helper()
	spans := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.Empty()),
		sdktrace.WithSampler(sampler),
		sdktrace.WithSpanProcessor(spans),
	)
	metrics := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(resource.Empty()),
		sdkmetric.WithReader(metrics),
		sdkmetric.WithExemplarFilter(exemplar.AlwaysOnFilter),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := tracerProvider.Shutdown(ctx); err != nil {
			t.Errorf("trace provider shutdown failed: %v", err)
		}
		if err := meterProvider.Shutdown(ctx); err != nil {
			t.Errorf("meter provider shutdown failed: %v", err)
		}
	})
	return &sdkFixture{
		spans:          spans,
		metrics:        metrics,
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
	}
}

func TestRealSDKPreservesParentsLinksExemplarsAndPrivacy(t *testing.T) {
	fixture := newSDKFixture(t, sdktrace.AlwaysSample())
	tel, err := vvotel.New(vvotel.Config{
		TracerProvider: fixture.tracerProvider,
		MeterProvider:  fixture.meterProvider,
		ResourceName:   vvotel.MustApproveName("products"),
	})
	if err != nil {
		t.Fatalf("assemble telemetry: %v", err)
	}

	appTracer := fixture.tracerProvider.Tracer("application.native")
	privacy := newPrivacyScenario(t, tel)
	inner := &serviceStub{}
	inner.get = func(ctx context.Context, cmd port.GetCommand[string]) (testModel, error) {
		privacy.emit(ctx)
		_, client := appTracer.Start(ctx, "native.client", trace.WithSpanKind(trace.SpanKindClient))
		client.End()
		return testModel{ID: cmd.ID}, nil
	}
	service := vvotel.Service[testModel, string, testModel](tel)(inner)

	ctx, server := appTracer.Start(context.Background(), "native.server", trace.WithSpanKind(trace.SpanKindServer))
	result, err := service.Get(ctx, port.GetCommand[string]{ID: "customer-secret-4815"})
	server.End()
	if err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if result.ID != "customer-secret-4815" {
		t.Fatalf("command result ID = %q, want original value", result.ID)
	}
	privacy.assertPreserved(t)

	spans := fixture.spans.Ended()
	if len(spans) != 4 {
		t.Fatalf("real SDK exported %d spans, want native server, command, storage, and native client", len(spans))
	}
	serverSpan := findSpan(t, spans, "native.server")
	commandSpan := findSpan(t, spans, vvotel.CommandSpanName(vvotel.OpCommandGet))
	storageSpan := findSpan(t, spans, vvotel.StorageSpanName(vvotel.OpStoragePut))
	clientSpan := findSpan(t, spans, "native.client")
	assertSpan(t, commandSpan, spanExpectation{
		kind:   trace.SpanKindInternal,
		parent: serverSpan.SpanContext(),
		status: codes.Unset,
		attributes: map[string]any{
			string(vvotel.AttrComponent):        vvotel.ComponentCommand,
			string(vvotel.AttrOperationName):    vvotel.OpCommandGet,
			string(vvotel.AttrOperationOutcome): vvotel.OutcomeOk,
			string(vvotel.AttrResourceName):     "products",
		},
		events: []eventExpectation{
			{
				name: vvotel.EventCache,
				attributes: map[string]any{
					string(vvotel.AttrComponent):        vvotel.ComponentCache,
					string(vvotel.AttrCacheLayer):       vvotel.CacheLayerFacade,
					string(vvotel.AttrOperationName):    vvotel.OpCacheLookup,
					string(vvotel.AttrOperationOutcome): string(cache.HitOutcome),
					string(vvotel.AttrMemoized):         true,
				},
			},
			{
				name: vvotel.EventCacheBackend,
				attributes: map[string]any{
					string(vvotel.AttrComponent):        vvotel.ComponentCacheBackend,
					string(vvotel.AttrCacheLayer):       vvotel.CacheBackendLayerMemoryBackend,
					string(vvotel.AttrOperationName):    vvotel.OpCacheBackendPut,
					string(vvotel.AttrOperationOutcome): string(cachememory.StoredOutcome),
				},
			},
		},
	})
	assertSpan(t, storageSpan, spanExpectation{
		kind:   trace.SpanKindInternal,
		parent: commandSpan.SpanContext(),
		status: codes.Error,
		attributes: map[string]any{
			string(vvotel.AttrComponent):        vvotel.ComponentStorage,
			string(vvotel.AttrOperationName):    vvotel.OpStoragePut,
			string(vvotel.AttrOperationOutcome): vvotel.OutcomeError,
			string(vvotel.AttrErrorType):        vvotel.ErrorTypeInternal,
			string(vvotel.AttrResourceName):     "products",
		},
	})
	if len(commandSpan.Links()) != 0 {
		t.Fatalf("command span has %d links, want none", len(commandSpan.Links()))
	}
	if !clientSpan.Parent().Equal(commandSpan.SpanContext()) {
		t.Fatalf("native client parent = %s/%s, want command span %s/%s", clientSpan.Parent().TraceID(), clientSpan.Parent().SpanID(), commandSpan.SpanContext().TraceID(), commandSpan.SpanContext().SpanID())
	}
	if !commandSpan.SpanContext().IsSampled() {
		t.Fatal("command span is not sampled under AlwaysSample")
	}
	if got := trace.SpanContextFromContext(privacy.rawStorage.putContext); !got.Equal(storageSpan.SpanContext()) {
		t.Fatalf("storage received span context %s/%s, want storage span %s/%s", got.TraceID(), got.SpanID(), storageSpan.SpanContext().TraceID(), storageSpan.SpanContext().SpanID())
	}

	metrics := collectMetricData(t, fixture.metrics)
	if got := metricDataCount(metrics); got != 9 {
		t.Fatalf("real SDK collected %d metrics, want 9 operation and cache metrics", got)
	}
	metric, point := commandHistogramFromMetrics(t, metrics)
	if metric.Description != vvotel.MetricCommandDurationDescription {
		t.Fatalf("command histogram description = %q, want %q", metric.Description, vvotel.MetricCommandDurationDescription)
	}
	if metric.Unit != vvotel.MetricCommandDurationUnit {
		t.Fatalf("command histogram unit = %q, want %q", metric.Unit, vvotel.MetricCommandDurationUnit)
	}
	if point.Count != 1 {
		t.Fatalf("command histogram count = %d, want 1", point.Count)
	}
	if math.IsNaN(point.Sum) || math.IsInf(point.Sum, 0) || point.Sum < 0 {
		t.Fatalf("command histogram sum = %f, want finite non-negative value", point.Sum)
	}
	if !reflect.DeepEqual(point.Bounds, vvotel.MetricCommandDurationBoundaries()) {
		t.Fatalf("command histogram bounds = %v, want %v", point.Bounds, vvotel.MetricCommandDurationBoundaries())
	}
	if sumBuckets(point.BucketCounts) != point.Count {
		t.Fatalf("command histogram buckets total = %d, want count %d", sumBuckets(point.BucketCounts), point.Count)
	}
	assertAttributes(t, point.Attributes.ToSlice(), map[string]any{
		string(vvotel.AttrComponent):        vvotel.ComponentCommand,
		string(vvotel.AttrOperationName):    vvotel.OpCommandGet,
		string(vvotel.AttrOperationOutcome): vvotel.OutcomeOk,
	})
	assertExemplar(t, point.Exemplars, commandSpan.SpanContext(), point.Sum)
	assertStorageDuration(t, metrics, storageSpan.SpanContext())
	assertCacheCounter(t, metrics, commandSpan.SpanContext())
	assertIntMetricExemplar(t, metrics, vvotel.MetricCacheEvents, commandSpan.SpanContext())
	assertCacheSDKHistogram(t, metrics, vvotel.MetricCacheItems, commandSpan.SpanContext(), 937, 619)
	assertCacheSDKHistogram(t, metrics, vvotel.MetricCacheEncodedBytes, commandSpan.SpanContext(), 8142)
	assertCacheSDKHistogram(t, metrics, vvotel.MetricCachePayloadBytes, commandSpan.SpanContext(), 4815)
	assertCacheSDKHistogram(t, metrics, vvotel.MetricCacheValueBytes, commandSpan.SpanContext(), 2718)
	assertCacheSDKHistogram(t, metrics, vvotel.MetricCacheChargedBytes, commandSpan.SpanContext(), 3141)
	assertSDKPrivacy(t, spans, metrics, privacy.secrets("customer-secret-4815"))
	privacy.assertPreserved(t)
}

func TestRealSDKErrorStatusAndAllowListReachExportBoundary(t *testing.T) {
	fixture := newSDKFixture(t, sdktrace.AlwaysSample())
	tel, err := vvotel.New(vvotel.Config{
		TracerProvider: fixture.tracerProvider,
		MeterProvider:  fixture.meterProvider,
		ResourceName:   vvotel.ApprovedName("secret\nresource"),
	})
	if err != nil {
		t.Fatalf("assemble telemetry: %v", err)
	}
	privateErr := errors.New("password=hunter2")
	inner := &serviceStub{get: func(context.Context, port.GetCommand[string]) (testModel, error) {
		return testModel{}, privateErr
	}}
	service := vvotel.Service[testModel, string, testModel](tel)(inner)
	_, gotErr := service.Get(context.Background(), port.GetCommand[string]{ID: "tenant-99291"})
	if gotErr != privateErr {
		t.Fatalf("command error = %v, want original error", gotErr)
	}

	spans := fixture.spans.Ended()
	commandSpan := findSpan(t, spans, vvotel.CommandSpanName(vvotel.OpCommandGet))
	assertSpan(t, commandSpan, spanExpectation{
		kind:   trace.SpanKindInternal,
		status: codes.Error,
		attributes: map[string]any{
			string(vvotel.AttrComponent):        vvotel.ComponentCommand,
			string(vvotel.AttrOperationName):    vvotel.OpCommandGet,
			string(vvotel.AttrOperationOutcome): vvotel.OutcomeError,
			string(vvotel.AttrErrorType):        vvotel.ErrorTypeInternal,
		},
	})
	metrics := collectMetricData(t, fixture.metrics)
	_, point := commandHistogramFromMetrics(t, metrics)
	assertAttributes(t, point.Attributes.ToSlice(), map[string]any{
		string(vvotel.AttrComponent):        vvotel.ComponentCommand,
		string(vvotel.AttrOperationName):    vvotel.OpCommandGet,
		string(vvotel.AttrOperationOutcome): vvotel.OutcomeError,
		string(vvotel.AttrErrorType):        vvotel.ErrorTypeInternal,
	})
	assertSDKPrivacy(t, spans, metrics, []string{"password=hunter2", "tenant-99291", "secret\nresource"})
}

func TestRealSDKSamplingDoesNotSuppressCommandMetric(t *testing.T) {
	fixture := newSDKFixture(t, sdktrace.NeverSample())
	tel, err := vvotel.New(vvotel.Config{
		TracerProvider: fixture.tracerProvider,
		MeterProvider:  fixture.meterProvider,
	})
	if err != nil {
		t.Fatalf("assemble telemetry: %v", err)
	}
	var received trace.SpanContext
	inner := &serviceStub{get: func(ctx context.Context, cmd port.GetCommand[string]) (testModel, error) {
		received = trace.SpanContextFromContext(ctx)
		return testModel{ID: cmd.ID}, nil
	}}
	service := vvotel.Service[testModel, string, testModel](tel)(inner)
	if _, err := service.Get(context.Background(), port.GetCommand[string]{ID: "id"}); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if !received.IsValid() {
		t.Fatal("wrapped service did not receive the span-derived context")
	}
	if received.IsSampled() {
		t.Fatal("wrapped service received a sampled context under NeverSample")
	}
	if len(fixture.spans.Ended()) != 0 {
		t.Fatalf("span processor received %d unsampled spans, want none", len(fixture.spans.Ended()))
	}
	_, point := collectCommandHistogram(t, fixture.metrics)
	if point.Count != 1 {
		t.Fatalf("command histogram count = %d, want 1", point.Count)
	}
}

func TestRealSDKTraceOnlyMeterOnlyAndGlobalsRemainUntouched(t *testing.T) {
	globalTracer := otelglobal.GetTracerProvider()
	globalMeter := otelglobal.GetMeterProvider()

	traceRecorder := tracetest.NewSpanRecorder()
	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.Empty()),
		sdktrace.WithSpanProcessor(traceRecorder),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := traceProvider.Shutdown(ctx); err != nil {
			t.Errorf("trace provider shutdown failed: %v", err)
		}
	})
	traceTel, err := vvotel.New(vvotel.Config{TracerProvider: traceProvider})
	if err != nil {
		t.Fatalf("assemble trace-only telemetry: %v", err)
	}
	traceService := vvotel.Service[testModel, string, testModel](traceTel)(&serviceStub{})
	if _, err := traceService.Get(context.Background(), port.GetCommand[string]{ID: "trace"}); err != nil {
		t.Fatalf("execute trace-only command: %v", err)
	}
	findSpan(t, traceRecorder.Ended(), vvotel.CommandSpanName(vvotel.OpCommandGet))

	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(resource.Empty()),
		sdkmetric.WithReader(metricReader),
		sdkmetric.WithExemplarFilter(exemplar.AlwaysOnFilter),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := meterProvider.Shutdown(ctx); err != nil {
			t.Errorf("meter provider shutdown failed: %v", err)
		}
	})
	metricTel, err := vvotel.New(vvotel.Config{MeterProvider: meterProvider})
	if err != nil {
		t.Fatalf("assemble meter-only telemetry: %v", err)
	}
	metricService := vvotel.Service[testModel, string, testModel](metricTel)(&serviceStub{})
	if _, err := metricService.Get(context.Background(), port.GetCommand[string]{ID: "metric"}); err != nil {
		t.Fatalf("execute meter-only command: %v", err)
	}
	_, point := collectCommandHistogram(t, metricReader)
	if point.Count != 1 {
		t.Fatalf("meter-only histogram count = %d, want 1", point.Count)
	}

	if otelglobal.GetTracerProvider() != globalTracer {
		t.Fatal("vvotel changed the process-global tracer provider")
	}
	if otelglobal.GetMeterProvider() != globalMeter {
		t.Fatal("vvotel changed the process-global meter provider")
	}
}

func TestLocalOTLPRoundTripPreservesFrostgroveSignalContract(t *testing.T) {
	conn, traces, metrics := newOTLPReceiver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithGRPCConn(conn),
		otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{Enabled: false}),
	)
	if err != nil {
		t.Fatalf("create OTLP trace exporter: %v", err)
	}
	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithGRPCConn(conn),
		otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetryConfig{Enabled: false}),
	)
	if err != nil {
		t.Fatalf("create OTLP metric exporter: %v", err)
	}

	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.Empty()),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(traceExporter)),
	)
	metricReader := sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(time.Hour))
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(resource.Empty()),
		sdkmetric.WithReader(metricReader),
		sdkmetric.WithExemplarFilter(exemplar.AlwaysOnFilter),
	)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		if err := traceProvider.Shutdown(shutdownCtx); err != nil {
			t.Errorf("trace provider shutdown failed: %v", err)
		}
		if err := meterProvider.Shutdown(shutdownCtx); err != nil {
			t.Errorf("meter provider shutdown failed: %v", err)
		}
	}()

	tel, err := vvotel.New(vvotel.Config{
		TracerProvider: traceProvider,
		MeterProvider:  meterProvider,
	})
	if err != nil {
		t.Fatalf("assemble telemetry: %v", err)
	}
	appTracer := traceProvider.Tracer("application.native")
	privacy := newPrivacyScenario(t, tel)
	inner := &serviceStub{get: func(ctx context.Context, cmd port.GetCommand[string]) (testModel, error) {
		privacy.emit(ctx)
		_, client := appTracer.Start(ctx, "native.client", trace.WithSpanKind(trace.SpanKindClient))
		client.End()
		return testModel{ID: cmd.ID}, nil
	}}
	service := vvotel.Service[testModel, string, testModel](tel)(inner)
	commandContext, parent := appTracer.Start(context.Background(), "native.server", trace.WithSpanKind(trace.SpanKindServer))
	result, err := service.Get(commandContext, port.GetCommand[string]{ID: "otlp-secret-2384"})
	parent.End()
	if err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if result.ID != "otlp-secret-2384" {
		t.Fatalf("OTLP command result ID = %q, want original value", result.ID)
	}
	privacy.assertPreserved(t)
	if err := traceProvider.ForceFlush(ctx); err != nil {
		t.Fatalf("force trace export: %v", err)
	}
	if err := meterProvider.ForceFlush(ctx); err != nil {
		t.Fatalf("force metric export: %v", err)
	}
	privacy.assertPreserved(t)

	commandSpan, scope := traces.findSpan(t, vvotel.CommandSpanName(vvotel.OpCommandGet))
	storageSpan, storageScope := traces.findSpan(t, vvotel.StorageSpanName(vvotel.OpStoragePut))
	parentSpan, _ := traces.findSpan(t, "native.server")
	clientSpan, _ := traces.findSpan(t, "native.client")
	if got := traces.spanCount(); got != 4 {
		t.Fatalf("OTLP receiver got %d spans, want native server, command, storage, and native client", got)
	}
	if commandSpan.Kind != tracepb.Span_SPAN_KIND_INTERNAL {
		t.Fatalf("OTLP command span kind = %s, want INTERNAL", commandSpan.Kind)
	}
	if !bytes.Equal(commandSpan.ParentSpanId, parentSpan.SpanId) {
		t.Fatalf("OTLP command parent = %x, want %x", commandSpan.ParentSpanId, parentSpan.SpanId)
	}
	if !bytes.Equal(commandSpan.TraceId, parentSpan.TraceId) {
		t.Fatalf("OTLP command trace = %x, want parent trace %x", commandSpan.TraceId, parentSpan.TraceId)
	}
	if clientSpan.Kind != tracepb.Span_SPAN_KIND_CLIENT || !bytes.Equal(clientSpan.ParentSpanId, commandSpan.SpanId) || !bytes.Equal(clientSpan.TraceId, commandSpan.TraceId) {
		t.Fatalf("OTLP native client kind/parent/trace = %s/%x/%x, want CLIENT/%x/%x", clientSpan.Kind, clientSpan.ParentSpanId, clientSpan.TraceId, commandSpan.SpanId, commandSpan.TraceId)
	}
	if commandSpan.EndTimeUnixNano == 0 || commandSpan.EndTimeUnixNano < commandSpan.StartTimeUnixNano {
		t.Fatalf("OTLP command times = %d..%d, want an ended span", commandSpan.StartTimeUnixNano, commandSpan.EndTimeUnixNano)
	}
	if commandSpan.Status.GetCode() != tracepb.Status_STATUS_CODE_UNSET {
		t.Fatalf("OTLP command status = %s, want UNSET", commandSpan.Status.GetCode())
	}
	if len(commandSpan.Links) != 0 {
		t.Fatalf("OTLP command span has %d links, want none", len(commandSpan.Links))
	}
	if scope.GetName() != vvotel.ScopeName || scope.GetVersion() != vvotel.ScopeVersion {
		t.Fatalf("OTLP scope = %s@%s, want %s@%s", scope.GetName(), scope.GetVersion(), vvotel.ScopeName, vvotel.ScopeVersion)
	}
	assertOTLPAttributes(t, commandSpan.Attributes, map[string]string{
		string(vvotel.AttrComponent):        vvotel.ComponentCommand,
		string(vvotel.AttrOperationName):    vvotel.OpCommandGet,
		string(vvotel.AttrOperationOutcome): vvotel.OutcomeOk,
	})
	assertOTLPEvents(t, commandSpan.Events, []otlpEventExpectation{
		{
			name: vvotel.EventCache,
			attributes: map[string]any{
				string(vvotel.AttrComponent):        vvotel.ComponentCache,
				string(vvotel.AttrCacheLayer):       vvotel.CacheLayerFacade,
				string(vvotel.AttrOperationName):    vvotel.OpCacheLookup,
				string(vvotel.AttrOperationOutcome): string(cache.HitOutcome),
				string(vvotel.AttrMemoized):         true,
			},
		},
		{
			name: vvotel.EventCacheBackend,
			attributes: map[string]any{
				string(vvotel.AttrComponent):        vvotel.ComponentCacheBackend,
				string(vvotel.AttrCacheLayer):       vvotel.CacheBackendLayerMemoryBackend,
				string(vvotel.AttrOperationName):    vvotel.OpCacheBackendPut,
				string(vvotel.AttrOperationOutcome): string(cachememory.StoredOutcome),
			},
		},
	})
	if storageSpan.Kind != tracepb.Span_SPAN_KIND_INTERNAL {
		t.Fatalf("OTLP storage span kind = %s, want INTERNAL", storageSpan.Kind)
	}
	if !bytes.Equal(storageSpan.ParentSpanId, commandSpan.SpanId) || !bytes.Equal(storageSpan.TraceId, commandSpan.TraceId) {
		t.Fatalf("OTLP storage parent/trace = %x/%x, want command %x/%x", storageSpan.ParentSpanId, storageSpan.TraceId, commandSpan.SpanId, commandSpan.TraceId)
	}
	if storageSpan.EndTimeUnixNano == 0 || storageSpan.EndTimeUnixNano < storageSpan.StartTimeUnixNano {
		t.Fatalf("OTLP storage times = %d..%d, want an ended span", storageSpan.StartTimeUnixNano, storageSpan.EndTimeUnixNano)
	}
	if storageSpan.Status.GetCode() != tracepb.Status_STATUS_CODE_ERROR || storageSpan.Status.GetMessage() != "" {
		t.Fatalf("OTLP storage status = %s/%q, want ERROR/empty", storageSpan.Status.GetCode(), storageSpan.Status.GetMessage())
	}
	if len(storageSpan.Links) != 0 || len(storageSpan.Events) != 0 {
		t.Fatalf("OTLP storage span has %d links and %d events, want none", len(storageSpan.Links), len(storageSpan.Events))
	}
	if storageScope.GetName() != vvotel.ScopeName || storageScope.GetVersion() != vvotel.ScopeVersion {
		t.Fatalf("OTLP storage scope = %s@%s, want %s@%s", storageScope.GetName(), storageScope.GetVersion(), vvotel.ScopeName, vvotel.ScopeVersion)
	}
	assertOTLPAttributes(t, storageSpan.Attributes, map[string]string{
		string(vvotel.AttrComponent):        vvotel.ComponentStorage,
		string(vvotel.AttrOperationName):    vvotel.OpStoragePut,
		string(vvotel.AttrOperationOutcome): vvotel.OutcomeError,
		string(vvotel.AttrErrorType):        vvotel.ErrorTypeInternal,
	})
	storageContext := trace.SpanContextFromContext(privacy.rawStorage.putContext)
	storageTraceID := storageContext.TraceID()
	storageSpanID := storageContext.SpanID()
	if !bytes.Equal(storageTraceID[:], storageSpan.TraceId) || !bytes.Equal(storageSpanID[:], storageSpan.SpanId) {
		t.Fatalf("storage received span context %s/%s, want serialized storage span %x/%x", storageTraceID, storageSpanID, storageSpan.TraceId, storageSpan.SpanId)
	}

	commandMetric, metricScope := metrics.findMetric(t, vvotel.MetricCommandDuration)
	if metricScope.GetName() != vvotel.ScopeName || metricScope.GetVersion() != vvotel.ScopeVersion {
		t.Fatalf("OTLP metric scope = %s@%s, want %s@%s", metricScope.GetName(), metricScope.GetVersion(), vvotel.ScopeName, vvotel.ScopeVersion)
	}
	if commandMetric.Description != vvotel.MetricCommandDurationDescription || commandMetric.Unit != vvotel.MetricCommandDurationUnit {
		t.Fatalf("OTLP command metric metadata = %q/%q", commandMetric.Description, commandMetric.Unit)
	}
	histogram := commandMetric.GetHistogram()
	if histogram == nil || len(histogram.DataPoints) != 1 {
		t.Fatalf("OTLP command histogram has %d points, want 1", len(histogram.GetDataPoints()))
	}
	point := histogram.DataPoints[0]
	if point.Count != 1 {
		t.Fatalf("OTLP command histogram count = %d, want 1", point.Count)
	}
	if point.Sum == nil || math.IsNaN(point.GetSum()) || math.IsInf(point.GetSum(), 0) || point.GetSum() < 0 {
		t.Fatalf("OTLP command histogram sum = %f, want finite non-negative value", point.GetSum())
	}
	if histogram.AggregationTemporality != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
		t.Fatalf("OTLP command histogram temporality = %s, want cumulative", histogram.AggregationTemporality)
	}
	if point.StartTimeUnixNano == 0 || point.TimeUnixNano < point.StartTimeUnixNano {
		t.Fatalf("OTLP command metric times = %d..%d, want a valid interval", point.StartTimeUnixNano, point.TimeUnixNano)
	}
	if sumBuckets(point.BucketCounts) != point.Count {
		t.Fatalf("OTLP command histogram buckets total = %d, want count %d", sumBuckets(point.BucketCounts), point.Count)
	}
	if !reflect.DeepEqual(point.ExplicitBounds, vvotel.MetricCommandDurationBoundaries()) {
		t.Fatalf("OTLP command histogram bounds = %v, want %v", point.ExplicitBounds, vvotel.MetricCommandDurationBoundaries())
	}
	assertOTLPAttributes(t, point.Attributes, map[string]string{
		string(vvotel.AttrComponent):        vvotel.ComponentCommand,
		string(vvotel.AttrOperationName):    vvotel.OpCommandGet,
		string(vvotel.AttrOperationOutcome): vvotel.OutcomeOk,
	})
	otlpExemplar := findOTLPExemplar(point.Exemplars, commandSpan.TraceId, commandSpan.SpanId)
	if otlpExemplar == nil {
		t.Fatalf("OTLP exemplars do not carry command trace/span IDs %x/%x", commandSpan.TraceId, commandSpan.SpanId)
	}
	if len(point.Exemplars) != 1 {
		t.Fatalf("OTLP command histogram has %d exemplars, want 1", len(point.Exemplars))
	}
	if math.IsNaN(otlpExemplar.GetAsDouble()) || math.IsInf(otlpExemplar.GetAsDouble(), 0) || otlpExemplar.GetAsDouble() != point.GetSum() {
		t.Fatalf("OTLP command exemplar value = %f, want finite histogram sum %f", otlpExemplar.GetAsDouble(), point.GetSum())
	}
	if len(otlpExemplar.FilteredAttributes) != 0 {
		t.Fatalf("OTLP command exemplar has filtered attributes: %v", otlpExemplar.FilteredAttributes)
	}
	if got := metrics.metricCount(); got != 9 {
		t.Fatalf("OTLP receiver got %d metrics, want 9 operation and cache metrics", got)
	}
	assertOTLPStorageDuration(t, metrics, storageSpan.TraceId, storageSpan.SpanId)
	assertOTLPCacheCounter(t, metrics, commandSpan.TraceId, commandSpan.SpanId)
	assertOTLPPrivacy(t, traces, metrics, privacy.secrets("otlp-secret-2384"))
}

type spanExpectation struct {
	kind       trace.SpanKind
	parent     trace.SpanContext
	status     codes.Code
	attributes map[string]any
	events     []eventExpectation
}

type eventExpectation struct {
	name       string
	attributes map[string]any
}

func assertSpan(t *testing.T, span sdktrace.ReadOnlySpan, want spanExpectation) {
	t.Helper()
	if span.SpanKind() != want.kind {
		t.Fatalf("span kind = %s, want %s", span.SpanKind(), want.kind)
	}
	if want.parent.IsValid() && !span.Parent().Equal(want.parent) {
		t.Fatalf("span parent = %s/%s, want %s/%s", span.Parent().TraceID(), span.Parent().SpanID(), want.parent.TraceID(), want.parent.SpanID())
	}
	if !want.parent.IsValid() && span.Parent().IsValid() {
		t.Fatalf("span parent = %s, want no parent", span.Parent().SpanID())
	}
	if span.Status().Code != want.status || span.Status().Description != "" {
		t.Fatalf("span status = %s/%q, want %s/empty", span.Status().Code, span.Status().Description, want.status)
	}
	if span.EndTime().IsZero() || span.EndTime().Before(span.StartTime()) {
		t.Fatalf("span times = %v..%v, want an ended span", span.StartTime(), span.EndTime())
	}
	assertEvents(t, span.Events(), want.events)
	if span.InstrumentationScope().Name != vvotel.ScopeName || span.InstrumentationScope().Version != vvotel.ScopeVersion {
		t.Fatalf("span scope = %s@%s, want %s@%s", span.InstrumentationScope().Name, span.InstrumentationScope().Version, vvotel.ScopeName, vvotel.ScopeVersion)
	}
	assertAttributes(t, span.Attributes(), want.attributes)
}

func assertEvents(t *testing.T, events []sdktrace.Event, want []eventExpectation) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("span events = %v, want %d exact events", events, len(want))
	}
	for i, event := range events {
		if event.Name != want[i].name {
			t.Fatalf("span event %d name = %q, want %q", i, event.Name, want[i].name)
		}
		if event.DroppedAttributeCount != 0 {
			t.Fatalf("span event %q dropped %d attributes, want none", event.Name, event.DroppedAttributeCount)
		}
		if event.Time.IsZero() {
			t.Fatalf("span event %q has zero timestamp", event.Name)
		}
		assertAttributes(t, event.Attributes, want[i].attributes)
	}
}

func findSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	var found sdktrace.ReadOnlySpan
	for _, span := range spans {
		if span.Name() != name {
			continue
		}
		if found != nil {
			t.Fatalf("found more than one ended span named %q", name)
		}
		found = span
	}
	if found == nil {
		t.Fatalf("ended span %q not found", name)
	}
	return found
}

func collectCommandHistogram(t *testing.T, reader *sdkmetric.ManualReader) (metricdata.Metrics, metricdata.HistogramDataPoint[float64]) {
	t.Helper()
	return commandHistogramFromMetrics(t, collectMetricData(t, reader))
}

func collectMetricData(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var data metricdata.ResourceMetrics
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := reader.Collect(ctx, &data); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	return data
}

func commandHistogramFromMetrics(t *testing.T, data metricdata.ResourceMetrics) (metricdata.Metrics, metricdata.HistogramDataPoint[float64]) {
	t.Helper()
	found := findMetricData(t, data, vvotel.MetricCommandDuration)
	histogram, ok := found.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("metric %q data type = %T, want float64 histogram", found.Name, found.Data)
	}
	if histogram.Temporality != metricdata.CumulativeTemporality {
		t.Fatalf("metric %q temporality = %v, want cumulative", found.Name, histogram.Temporality)
	}
	if len(histogram.DataPoints) != 1 {
		t.Fatalf("metric %q has %d points, want 1", found.Name, len(histogram.DataPoints))
	}
	return found, histogram.DataPoints[0]
}

func findMetricData(t *testing.T, data metricdata.ResourceMetrics, name string) metricdata.Metrics {
	t.Helper()
	var found *metricdata.Metrics
	for scopeIndex := range data.ScopeMetrics {
		scope := &data.ScopeMetrics[scopeIndex]
		for metricIndex := range scope.Metrics {
			metric := &scope.Metrics[metricIndex]
			if metric.Name == name {
				if found != nil {
					t.Fatalf("found more than one metric named %q", name)
				}
				if scope.Scope.Name != vvotel.ScopeName || scope.Scope.Version != vvotel.ScopeVersion {
					t.Fatalf("metric %q scope = %s@%s, want %s@%s", name, scope.Scope.Name, scope.Scope.Version, vvotel.ScopeName, vvotel.ScopeVersion)
				}
				found = metric
			}
		}
	}
	if found == nil {
		t.Fatalf("metric %q not found", name)
	}
	return *found
}

func metricDataCount(data metricdata.ResourceMetrics) int {
	count := 0
	for _, scope := range data.ScopeMetrics {
		count += len(scope.Metrics)
	}
	return count
}

func assertAttributes(t *testing.T, attributes []attribute.KeyValue, want map[string]any) {
	t.Helper()
	got := make(map[string]any, len(attributes))
	for _, kv := range attributes {
		got[string(kv.Key)] = kv.Value.AsInterface()
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attributes = %#v, want %#v", got, want)
	}
}

func assertExemplar(t *testing.T, exemplars []metricdata.Exemplar[float64], spanContext trace.SpanContext, value float64) {
	t.Helper()
	if len(exemplars) != 1 {
		t.Fatalf("command histogram has %d exemplars, want 1", len(exemplars))
	}
	traceID := spanContext.TraceID()
	spanID := spanContext.SpanID()
	for _, exemplar := range exemplars {
		if bytes.Equal(exemplar.TraceID, traceID[:]) && bytes.Equal(exemplar.SpanID, spanID[:]) {
			if math.IsNaN(exemplar.Value) || math.IsInf(exemplar.Value, 0) || exemplar.Value != value {
				t.Fatalf("command exemplar value = %f, want finite histogram sum %f", exemplar.Value, value)
			}
			if len(exemplar.FilteredAttributes) != 0 {
				t.Fatalf("command exemplar has filtered attributes: %v", exemplar.FilteredAttributes)
			}
			return
		}
	}
	t.Fatalf("exemplars do not carry command trace/span IDs %s/%s: %#v", traceID, spanID, exemplars)
}

func assertCacheCounter(t *testing.T, data metricdata.ResourceMetrics, spanContext trace.SpanContext) {
	t.Helper()
	metric := findMetricData(t, data, vvotel.MetricCacheOperations)
	if metric.Description != vvotel.MetricCacheOperationsDescription || metric.Unit != vvotel.MetricCacheOperationsUnit {
		t.Fatalf("cache counter metadata = %q/%q, want %q/%q", metric.Description, metric.Unit, vvotel.MetricCacheOperationsDescription, vvotel.MetricCacheOperationsUnit)
	}
	sum, ok := metric.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("metric %q data type = %T, want int64 sum", metric.Name, metric.Data)
	}
	if sum.Temporality != metricdata.CumulativeTemporality || !sum.IsMonotonic {
		t.Fatalf("cache counter temporality/monotonic = %v/%v, want cumulative/true", sum.Temporality, sum.IsMonotonic)
	}
	if len(sum.DataPoints) != 2 {
		t.Fatalf("cache counter has %d points, want facade and memory-backend points", len(sum.DataPoints))
	}
	seen := make(map[string]bool, 2)
	for _, point := range sum.DataPoints {
		if point.Value != 1 {
			t.Fatalf("cache counter point value = %d, want 1", point.Value)
		}
		if point.StartTime.IsZero() || point.Time.Before(point.StartTime) {
			t.Fatalf("cache counter times = %v..%v, want a valid interval", point.StartTime, point.Time)
		}
		attributes := point.Attributes.ToSlice()
		layer, ok := stringAttribute(attributes, vvotel.AttrCacheLayer)
		if !ok {
			t.Fatalf("cache counter point has no %q attribute", vvotel.AttrCacheLayer)
		}
		switch layer {
		case vvotel.CacheLayerFacade:
			assertAttributes(t, attributes, map[string]any{
				string(vvotel.AttrComponent):        vvotel.ComponentCache,
				string(vvotel.AttrCacheLayer):       vvotel.CacheLayerFacade,
				string(vvotel.AttrOperationName):    vvotel.OpCacheLookup,
				string(vvotel.AttrOperationOutcome): string(cache.HitOutcome),
				string(vvotel.AttrMemoized):         true,
			})
		case vvotel.CacheBackendLayerMemoryBackend:
			assertAttributes(t, attributes, map[string]any{
				string(vvotel.AttrComponent):        vvotel.ComponentCacheBackend,
				string(vvotel.AttrCacheLayer):       vvotel.CacheBackendLayerMemoryBackend,
				string(vvotel.AttrOperationName):    vvotel.OpCacheBackendPut,
				string(vvotel.AttrOperationOutcome): string(cachememory.StoredOutcome),
			})
		default:
			t.Fatalf("cache counter layer = %q, want facade or memory_backend", layer)
		}
		if seen[layer] {
			t.Fatalf("cache counter has duplicate %q point", layer)
		}
		seen[layer] = true
		assertInt64Exemplar(t, point.Exemplars, spanContext, point.Value)
	}
}

func assertCacheSDKHistogram(t *testing.T, data metricdata.ResourceMetrics, name string, spanContext trace.SpanContext, want ...int64) {
	t.Helper()
	metric := findMetricData(t, data, name)
	histogram, ok := metric.Data.(metricdata.Histogram[int64])
	if !ok || len(histogram.DataPoints) != len(want) {
		t.Fatalf("cache histogram %q = %T/%d points, want %d", name, metric.Data, len(histogram.DataPoints), len(want))
	}
	remaining := make(map[int64]int, len(want))
	for _, value := range want {
		remaining[value]++
	}
	for _, point := range histogram.DataPoints {
		if point.Count != 1 || remaining[point.Sum] == 0 {
			t.Fatalf("cache histogram %q point = count %d sum %d", name, point.Count, point.Sum)
		}
		remaining[point.Sum]--
		assertInt64Exemplar(t, point.Exemplars, spanContext, point.Sum)
	}
}

func assertStorageDuration(t *testing.T, data metricdata.ResourceMetrics, spanContext trace.SpanContext) {
	t.Helper()
	metric := findMetricData(t, data, vvotel.MetricStorageDuration)
	histogram, ok := metric.Data.(metricdata.Histogram[float64])
	if !ok || len(histogram.DataPoints) != 1 {
		t.Fatalf("storage duration = %T/%d points", metric.Data, len(histogram.DataPoints))
	}
	point := histogram.DataPoints[0]
	assertAttributes(t, point.Attributes.ToSlice(), map[string]any{
		string(vvotel.AttrComponent):        vvotel.ComponentStorage,
		string(vvotel.AttrOperationName):    vvotel.OpStoragePut,
		string(vvotel.AttrOperationOutcome): vvotel.OutcomeError,
		string(vvotel.AttrErrorType):        vvotel.ErrorTypeInternal,
	})
	assertExemplar(t, point.Exemplars, spanContext, point.Sum)
}

func stringAttribute(attributes []attribute.KeyValue, key attribute.Key) (string, bool) {
	for _, current := range attributes {
		if current.Key == key {
			return current.Value.AsString(), current.Value.Type() == attribute.STRING
		}
	}
	return "", false
}

func assertInt64Exemplar(t *testing.T, exemplars []metricdata.Exemplar[int64], spanContext trace.SpanContext, value int64) {
	t.Helper()
	if len(exemplars) != 1 {
		t.Fatalf("cache counter has %d exemplars, want 1", len(exemplars))
	}
	traceID := spanContext.TraceID()
	spanID := spanContext.SpanID()
	exemplar := exemplars[0]
	if !bytes.Equal(exemplar.TraceID, traceID[:]) || !bytes.Equal(exemplar.SpanID, spanID[:]) {
		t.Fatalf("cache exemplar trace/span = %x/%x, want %s/%s", exemplar.TraceID, exemplar.SpanID, traceID, spanID)
	}
	if exemplar.Value != value {
		t.Fatalf("cache exemplar value = %d, want counter point %d", exemplar.Value, value)
	}
	if len(exemplar.FilteredAttributes) != 0 {
		t.Fatalf("cache exemplar has filtered attributes: %v", exemplar.FilteredAttributes)
	}
}

func assertSDKPrivacy(t *testing.T, spans []sdktrace.ReadOnlySpan, metrics metricdata.ResourceMetrics, secrets []string) {
	t.Helper()
	assertPrivacyCanaries(t, secrets)
	for _, span := range spans {
		assertTextHasNoSecrets(t, "span name", span.Name(), secrets)
		assertTextHasNoSecrets(t, "span status", span.Status().Description, secrets)
		assertSDKAttributesHaveNoSecrets(t, "span attribute", span.Attributes(), secrets)
		for _, event := range span.Events() {
			assertTextHasNoSecrets(t, "span event name", event.Name, secrets)
			assertSDKAttributesHaveNoSecrets(t, "span event attribute", event.Attributes, secrets)
		}
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			assertTextHasNoSecrets(t, "metric name", metric.Name, secrets)
			assertTextHasNoSecrets(t, "metric description", metric.Description, secrets)
			assertTextHasNoSecrets(t, "metric unit", metric.Unit, secrets)
			switch data := metric.Data.(type) {
			case metricdata.Gauge[int64]:
				assertSDKNumberPointPrivacy(t, metric.Name, data.DataPoints, secrets)
			case metricdata.Gauge[float64]:
				assertSDKNumberPointPrivacy(t, metric.Name, data.DataPoints, secrets)
			case metricdata.Sum[int64]:
				assertSDKNumberPointPrivacy(t, metric.Name, data.DataPoints, secrets)
			case metricdata.Sum[float64]:
				assertSDKNumberPointPrivacy(t, metric.Name, data.DataPoints, secrets)
			case metricdata.Histogram[int64]:
				assertSDKHistogramPointPrivacy(t, metric.Name, data.DataPoints, secrets)
			case metricdata.Histogram[float64]:
				assertSDKHistogramPointPrivacy(t, metric.Name, data.DataPoints, secrets)
			case metricdata.ExponentialHistogram[int64]:
				assertSDKExponentialHistogramPointPrivacy(t, metric.Name, data.DataPoints, secrets)
			case metricdata.ExponentialHistogram[float64]:
				assertSDKExponentialHistogramPointPrivacy(t, metric.Name, data.DataPoints, secrets)
			case metricdata.Summary:
				for _, point := range data.DataPoints {
					assertSDKAttributesHaveNoSecrets(t, "metric "+metric.Name+" datapoint attribute", point.Attributes.ToSlice(), secrets)
				}
			default:
				t.Fatalf("metric %q has unscanned data type %T", metric.Name, metric.Data)
			}
		}
	}
}

func assertSDKNumberPointPrivacy[N int64 | float64](t *testing.T, metricName string, points []metricdata.DataPoint[N], secrets []string) {
	t.Helper()
	for _, point := range points {
		assertSDKAttributesHaveNoSecrets(t, "metric "+metricName+" datapoint attribute", point.Attributes.ToSlice(), secrets)
		assertSDKExemplarPrivacy(t, metricName, point.Exemplars, secrets)
	}
}

func assertSDKHistogramPointPrivacy[N int64 | float64](t *testing.T, metricName string, points []metricdata.HistogramDataPoint[N], secrets []string) {
	t.Helper()
	for _, point := range points {
		assertSDKAttributesHaveNoSecrets(t, "metric "+metricName+" datapoint attribute", point.Attributes.ToSlice(), secrets)
		assertSDKExemplarPrivacy(t, metricName, point.Exemplars, secrets)
	}
}

func assertSDKExponentialHistogramPointPrivacy[N int64 | float64](t *testing.T, metricName string, points []metricdata.ExponentialHistogramDataPoint[N], secrets []string) {
	t.Helper()
	for _, point := range points {
		assertSDKAttributesHaveNoSecrets(t, "metric "+metricName+" datapoint attribute", point.Attributes.ToSlice(), secrets)
		assertSDKExemplarPrivacy(t, metricName, point.Exemplars, secrets)
	}
}

func assertSDKExemplarPrivacy[N int64 | float64](t *testing.T, metricName string, exemplars []metricdata.Exemplar[N], secrets []string) {
	t.Helper()
	for _, exemplar := range exemplars {
		assertSDKAttributesHaveNoSecrets(t, "metric "+metricName+" exemplar filtered attribute", exemplar.FilteredAttributes, secrets)
	}
}

func assertSDKAttributesHaveNoSecrets(t *testing.T, location string, attributes []attribute.KeyValue, secrets []string) {
	t.Helper()
	for _, current := range attributes {
		assertTextHasNoSecrets(t, location+" key", string(current.Key), secrets)
		assertTextHasNoSecrets(t, location+" value", fmt.Sprint(current.Value.AsInterface()), secrets)
	}
}

func assertPrivacyCanaries(t *testing.T, secrets []string) {
	t.Helper()
	if len(secrets) == 0 {
		t.Fatal("privacy scan has no canaries")
	}
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			t.Fatal("privacy scan has an empty canary")
		}
		if _, exists := seen[secret]; exists {
			t.Fatalf("privacy scan has duplicate canary %q", secret)
		}
		seen[secret] = struct{}{}
	}
}

func assertTextHasNoSecrets(t *testing.T, location string, text string, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("%s contains privacy canary %q", location, secret)
		}
	}
}

type recordingOTLPTraceReceiver struct {
	collectortracepb.UnimplementedTraceServiceServer
	mu       sync.Mutex
	requests []*collectortracepb.ExportTraceServiceRequest
}

func (r *recordingOTLPTraceReceiver) Export(_ context.Context, request *collectortracepb.ExportTraceServiceRequest) (*collectortracepb.ExportTraceServiceResponse, error) {
	r.mu.Lock()
	r.requests = append(r.requests, proto.Clone(request).(*collectortracepb.ExportTraceServiceRequest))
	r.mu.Unlock()
	return &collectortracepb.ExportTraceServiceResponse{}, nil
}

func (r *recordingOTLPTraceReceiver) findSpan(t *testing.T, name string) (*tracepb.Span, *commonpb.InstrumentationScope) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var found *tracepb.Span
	var foundScope *commonpb.InstrumentationScope
	for _, request := range r.requests {
		for _, resourceSpans := range request.ResourceSpans {
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				for _, span := range scopeSpans.Spans {
					if span.Name != name {
						continue
					}
					if found != nil {
						t.Fatalf("found more than one OTLP span named %q", name)
					}
					found = span
					foundScope = scopeSpans.Scope
				}
			}
		}
	}
	if found == nil {
		t.Fatalf("OTLP span %q not found", name)
	}
	return found, foundScope
}

func (r *recordingOTLPTraceReceiver) spanCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, request := range r.requests {
		for _, resourceSpans := range request.ResourceSpans {
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				count += len(scopeSpans.Spans)
			}
		}
	}
	return count
}

func (r *recordingOTLPTraceReceiver) snapshot() []*collectortracepb.ExportTraceServiceRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	requests := make([]*collectortracepb.ExportTraceServiceRequest, len(r.requests))
	for i, request := range r.requests {
		requests[i] = proto.Clone(request).(*collectortracepb.ExportTraceServiceRequest)
	}
	return requests
}

type recordingOTLPMetricReceiver struct {
	collectormetricpb.UnimplementedMetricsServiceServer
	mu       sync.Mutex
	requests []*collectormetricpb.ExportMetricsServiceRequest
}

func (r *recordingOTLPMetricReceiver) Export(_ context.Context, request *collectormetricpb.ExportMetricsServiceRequest) (*collectormetricpb.ExportMetricsServiceResponse, error) {
	r.mu.Lock()
	r.requests = append(r.requests, proto.Clone(request).(*collectormetricpb.ExportMetricsServiceRequest))
	r.mu.Unlock()
	return &collectormetricpb.ExportMetricsServiceResponse{}, nil
}

func (r *recordingOTLPMetricReceiver) findMetric(t *testing.T, name string) (*metricpb.Metric, *commonpb.InstrumentationScope) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var found *metricpb.Metric
	var foundScope *commonpb.InstrumentationScope
	for _, request := range r.requests {
		for _, resourceMetrics := range request.ResourceMetrics {
			for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
				for _, metric := range scopeMetrics.Metrics {
					if metric.Name != name {
						continue
					}
					if found != nil {
						t.Fatalf("found more than one OTLP metric named %q", name)
					}
					found = metric
					foundScope = scopeMetrics.Scope
				}
			}
		}
	}
	if found == nil {
		t.Fatalf("OTLP metric %q not found", name)
	}
	return found, foundScope
}

func (r *recordingOTLPMetricReceiver) metricCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, request := range r.requests {
		for _, resourceMetrics := range request.ResourceMetrics {
			for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
				count += len(scopeMetrics.Metrics)
			}
		}
	}
	return count
}

func (r *recordingOTLPMetricReceiver) snapshot() []*collectormetricpb.ExportMetricsServiceRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	requests := make([]*collectormetricpb.ExportMetricsServiceRequest, len(r.requests))
	for i, request := range r.requests {
		requests[i] = proto.Clone(request).(*collectormetricpb.ExportMetricsServiceRequest)
	}
	return requests
}

func newOTLPReceiver(t *testing.T) (*grpc.ClientConn, *recordingOTLPTraceReceiver, *recordingOTLPMetricReceiver) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	traces := &recordingOTLPTraceReceiver{}
	metrics := &recordingOTLPMetricReceiver{}
	collectortracepb.RegisterTraceServiceServer(server, traces)
	collectormetricpb.RegisterMetricsServiceServer(server, metrics)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	conn, err := grpc.NewClient(
		"passthrough:///otel-test",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatalf("connect local OTLP receiver: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
		if err := <-serveErrors; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("OTLP receiver stopped with error: %v", err)
		}
	})
	return conn, traces, metrics
}

type otlpEventExpectation struct {
	name       string
	attributes map[string]any
}

func assertOTLPEvents(t *testing.T, events []*tracepb.Span_Event, want []otlpEventExpectation) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("OTLP span has %d events, want %d exact events", len(events), len(want))
	}
	for i, event := range events {
		if event.GetName() != want[i].name {
			t.Fatalf("OTLP span event %d name = %q, want %q", i, event.GetName(), want[i].name)
		}
		if event.GetTimeUnixNano() == 0 {
			t.Fatalf("OTLP span event %q has zero timestamp", event.GetName())
		}
		if event.GetDroppedAttributesCount() != 0 {
			t.Fatalf("OTLP span event %q dropped %d attributes, want none", event.GetName(), event.GetDroppedAttributesCount())
		}
		assertOTLPAnyAttributes(t, event.GetAttributes(), want[i].attributes)
	}
}

func assertOTLPCacheCounter(t *testing.T, receiver *recordingOTLPMetricReceiver, traceID, spanID []byte) {
	t.Helper()
	metric, scope := receiver.findMetric(t, vvotel.MetricCacheOperations)
	if scope.GetName() != vvotel.ScopeName || scope.GetVersion() != vvotel.ScopeVersion {
		t.Fatalf("OTLP cache metric scope = %s@%s, want %s@%s", scope.GetName(), scope.GetVersion(), vvotel.ScopeName, vvotel.ScopeVersion)
	}
	if metric.GetDescription() != vvotel.MetricCacheOperationsDescription || metric.GetUnit() != vvotel.MetricCacheOperationsUnit {
		t.Fatalf("OTLP cache counter metadata = %q/%q, want %q/%q", metric.GetDescription(), metric.GetUnit(), vvotel.MetricCacheOperationsDescription, vvotel.MetricCacheOperationsUnit)
	}
	sum := metric.GetSum()
	if sum == nil {
		t.Fatalf("OTLP cache metric data type = %T, want sum", metric.GetData())
	}
	if sum.GetAggregationTemporality() != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE || !sum.GetIsMonotonic() {
		t.Fatalf("OTLP cache counter temporality/monotonic = %s/%v, want cumulative/true", sum.GetAggregationTemporality(), sum.GetIsMonotonic())
	}
	if len(sum.GetDataPoints()) != 2 {
		t.Fatalf("OTLP cache counter has %d points, want facade and memory-backend points", len(sum.GetDataPoints()))
	}
	seen := make(map[string]bool, 2)
	for _, point := range sum.GetDataPoints() {
		if _, ok := point.GetValue().(*metricpb.NumberDataPoint_AsInt); !ok || point.GetAsInt() != 1 {
			t.Fatalf("OTLP cache counter point value = %T/%d, want int64 1", point.GetValue(), point.GetAsInt())
		}
		if point.GetStartTimeUnixNano() == 0 || point.GetTimeUnixNano() < point.GetStartTimeUnixNano() {
			t.Fatalf("OTLP cache counter times = %d..%d, want a valid interval", point.GetStartTimeUnixNano(), point.GetTimeUnixNano())
		}
		layer, ok := otlpStringAttribute(point.GetAttributes(), string(vvotel.AttrCacheLayer))
		if !ok {
			t.Fatalf("OTLP cache counter point has no string %q attribute", vvotel.AttrCacheLayer)
		}
		switch layer {
		case vvotel.CacheLayerFacade:
			assertOTLPAnyAttributes(t, point.GetAttributes(), map[string]any{
				string(vvotel.AttrComponent):        vvotel.ComponentCache,
				string(vvotel.AttrCacheLayer):       vvotel.CacheLayerFacade,
				string(vvotel.AttrOperationName):    vvotel.OpCacheLookup,
				string(vvotel.AttrOperationOutcome): string(cache.HitOutcome),
				string(vvotel.AttrMemoized):         true,
			})
		case vvotel.CacheBackendLayerMemoryBackend:
			assertOTLPAnyAttributes(t, point.GetAttributes(), map[string]any{
				string(vvotel.AttrComponent):        vvotel.ComponentCacheBackend,
				string(vvotel.AttrCacheLayer):       vvotel.CacheBackendLayerMemoryBackend,
				string(vvotel.AttrOperationName):    vvotel.OpCacheBackendPut,
				string(vvotel.AttrOperationOutcome): string(cachememory.StoredOutcome),
			})
		default:
			t.Fatalf("OTLP cache counter layer = %q, want facade or memory_backend", layer)
		}
		if seen[layer] {
			t.Fatalf("OTLP cache counter has duplicate %q point", layer)
		}
		seen[layer] = true
		if len(point.GetExemplars()) != 1 {
			t.Fatalf("OTLP cache counter %q point has %d exemplars, want 1", layer, len(point.GetExemplars()))
		}
		exemplar := findOTLPExemplar(point.GetExemplars(), traceID, spanID)
		if exemplar == nil {
			t.Fatalf("OTLP cache counter %q exemplar does not carry command trace/span IDs %x/%x", layer, traceID, spanID)
		}
		if _, ok := exemplar.GetValue().(*metricpb.Exemplar_AsInt); !ok || exemplar.GetAsInt() != point.GetAsInt() {
			t.Fatalf("OTLP cache counter %q exemplar value = %T/%d, want int64 point value %d", layer, exemplar.GetValue(), exemplar.GetAsInt(), point.GetAsInt())
		}
		if len(exemplar.GetFilteredAttributes()) != 0 {
			t.Fatalf("OTLP cache counter %q exemplar has filtered attributes: %v", layer, exemplar.GetFilteredAttributes())
		}
	}
}

func assertOTLPStorageDuration(t *testing.T, receiver *recordingOTLPMetricReceiver, traceID, spanID []byte) {
	t.Helper()
	metric, scope := receiver.findMetric(t, vvotel.MetricStorageDuration)
	if scope.GetName() != vvotel.ScopeName || scope.GetVersion() != vvotel.ScopeVersion {
		t.Fatalf("OTLP storage metric scope = %s@%s", scope.GetName(), scope.GetVersion())
	}
	histogram := metric.GetHistogram()
	if histogram == nil || len(histogram.GetDataPoints()) != 1 {
		t.Fatalf("OTLP storage duration = %T/%d points", metric.GetData(), len(histogram.GetDataPoints()))
	}
	point := histogram.GetDataPoints()[0]
	assertOTLPAttributes(t, point.GetAttributes(), map[string]string{
		string(vvotel.AttrComponent):        vvotel.ComponentStorage,
		string(vvotel.AttrOperationName):    vvotel.OpStoragePut,
		string(vvotel.AttrOperationOutcome): vvotel.OutcomeError,
		string(vvotel.AttrErrorType):        vvotel.ErrorTypeInternal,
	})
	if findOTLPExemplar(point.GetExemplars(), traceID, spanID) == nil {
		t.Fatalf("OTLP storage exemplar does not carry %x/%x", traceID, spanID)
	}
}

func otlpStringAttribute(attributes []*commonpb.KeyValue, key string) (string, bool) {
	for _, current := range attributes {
		if current.GetKey() != key || current.GetValue() == nil {
			continue
		}
		value, ok := current.GetValue().GetValue().(*commonpb.AnyValue_StringValue)
		if ok {
			return value.StringValue, true
		}
		return "", false
	}
	return "", false
}

func assertOTLPPrivacy(t *testing.T, traces *recordingOTLPTraceReceiver, metrics *recordingOTLPMetricReceiver, secrets []string) {
	t.Helper()
	assertPrivacyCanaries(t, secrets)
	for _, request := range traces.snapshot() {
		for _, resourceSpans := range request.GetResourceSpans() {
			assertOTLPAttributesHaveNoSecrets(t, "trace resource attribute", resourceSpans.GetResource().GetAttributes(), secrets)
			assertTextHasNoSecrets(t, "trace schema URL", resourceSpans.GetSchemaUrl(), secrets)
			for _, scopeSpans := range resourceSpans.GetScopeSpans() {
				assertTextHasNoSecrets(t, "trace scope name", scopeSpans.GetScope().GetName(), secrets)
				assertTextHasNoSecrets(t, "trace scope version", scopeSpans.GetScope().GetVersion(), secrets)
				assertOTLPAttributesHaveNoSecrets(t, "trace scope attribute", scopeSpans.GetScope().GetAttributes(), secrets)
				assertTextHasNoSecrets(t, "trace scope schema URL", scopeSpans.GetSchemaUrl(), secrets)
				for _, span := range scopeSpans.GetSpans() {
					assertTextHasNoSecrets(t, "OTLP span name", span.GetName(), secrets)
					assertTextHasNoSecrets(t, "OTLP span status", span.GetStatus().GetMessage(), secrets)
					assertOTLPAttributesHaveNoSecrets(t, "OTLP span attribute", span.GetAttributes(), secrets)
					for _, event := range span.GetEvents() {
						assertTextHasNoSecrets(t, "OTLP span event name", event.GetName(), secrets)
						assertOTLPAttributesHaveNoSecrets(t, "OTLP span event attribute", event.GetAttributes(), secrets)
					}
					for _, link := range span.GetLinks() {
						assertTextHasNoSecrets(t, "OTLP span link trace state", link.GetTraceState(), secrets)
						assertOTLPAttributesHaveNoSecrets(t, "OTLP span link attribute", link.GetAttributes(), secrets)
					}
				}
			}
		}
	}
	for _, request := range metrics.snapshot() {
		for _, resourceMetrics := range request.GetResourceMetrics() {
			assertOTLPAttributesHaveNoSecrets(t, "metric resource attribute", resourceMetrics.GetResource().GetAttributes(), secrets)
			assertTextHasNoSecrets(t, "metric schema URL", resourceMetrics.GetSchemaUrl(), secrets)
			for _, scopeMetrics := range resourceMetrics.GetScopeMetrics() {
				assertTextHasNoSecrets(t, "metric scope name", scopeMetrics.GetScope().GetName(), secrets)
				assertTextHasNoSecrets(t, "metric scope version", scopeMetrics.GetScope().GetVersion(), secrets)
				assertOTLPAttributesHaveNoSecrets(t, "metric scope attribute", scopeMetrics.GetScope().GetAttributes(), secrets)
				assertTextHasNoSecrets(t, "metric scope schema URL", scopeMetrics.GetSchemaUrl(), secrets)
				for _, metric := range scopeMetrics.GetMetrics() {
					assertOTLPMetricPrivacy(t, metric, secrets)
				}
			}
		}
	}
}

func assertOTLPMetricPrivacy(t *testing.T, metric *metricpb.Metric, secrets []string) {
	t.Helper()
	assertTextHasNoSecrets(t, "OTLP metric name", metric.GetName(), secrets)
	assertTextHasNoSecrets(t, "OTLP metric description", metric.GetDescription(), secrets)
	assertTextHasNoSecrets(t, "OTLP metric unit", metric.GetUnit(), secrets)
	if gauge := metric.GetGauge(); gauge != nil {
		for _, point := range gauge.GetDataPoints() {
			assertOTLPNumberPointPrivacy(t, metric.GetName(), point, secrets)
		}
		return
	}
	if sum := metric.GetSum(); sum != nil {
		for _, point := range sum.GetDataPoints() {
			assertOTLPNumberPointPrivacy(t, metric.GetName(), point, secrets)
		}
		return
	}
	if histogram := metric.GetHistogram(); histogram != nil {
		for _, point := range histogram.GetDataPoints() {
			assertOTLPAttributesHaveNoSecrets(t, "OTLP metric "+metric.GetName()+" datapoint attribute", point.GetAttributes(), secrets)
			assertOTLPExemplarPrivacy(t, metric.GetName(), point.GetExemplars(), secrets)
		}
		return
	}
	if histogram := metric.GetExponentialHistogram(); histogram != nil {
		for _, point := range histogram.GetDataPoints() {
			assertOTLPAttributesHaveNoSecrets(t, "OTLP metric "+metric.GetName()+" datapoint attribute", point.GetAttributes(), secrets)
			assertOTLPExemplarPrivacy(t, metric.GetName(), point.GetExemplars(), secrets)
		}
		return
	}
	if summary := metric.GetSummary(); summary != nil {
		for _, point := range summary.GetDataPoints() {
			assertOTLPAttributesHaveNoSecrets(t, "OTLP metric "+metric.GetName()+" datapoint attribute", point.GetAttributes(), secrets)
		}
		return
	}
	t.Fatalf("OTLP metric %q has unscanned data type %T", metric.GetName(), metric.GetData())
}

func assertOTLPNumberPointPrivacy(t *testing.T, metricName string, point *metricpb.NumberDataPoint, secrets []string) {
	t.Helper()
	assertOTLPAttributesHaveNoSecrets(t, "OTLP metric "+metricName+" datapoint attribute", point.GetAttributes(), secrets)
	assertOTLPExemplarPrivacy(t, metricName, point.GetExemplars(), secrets)
}

func assertOTLPExemplarPrivacy(t *testing.T, metricName string, exemplars []*metricpb.Exemplar, secrets []string) {
	t.Helper()
	for _, exemplar := range exemplars {
		assertOTLPAttributesHaveNoSecrets(t, "OTLP metric "+metricName+" exemplar filtered attribute", exemplar.GetFilteredAttributes(), secrets)
	}
}

func assertOTLPAttributesHaveNoSecrets(t *testing.T, location string, attributes []*commonpb.KeyValue, secrets []string) {
	t.Helper()
	for _, current := range attributes {
		if current == nil {
			t.Fatalf("%s is nil", location)
		}
		assertTextHasNoSecrets(t, location+" key", current.GetKey(), secrets)
		assertOTLPAnyValueHasNoSecrets(t, location+" value", current.GetValue(), secrets)
	}
}

func assertOTLPAnyValueHasNoSecrets(t *testing.T, location string, value *commonpb.AnyValue, secrets []string) {
	t.Helper()
	if value == nil {
		t.Fatalf("%s is nil", location)
	}
	assertTextHasNoSecrets(t, location, value.GetStringValue(), secrets)
	assertTextHasNoSecrets(t, location, string(value.GetBytesValue()), secrets)
	if array := value.GetArrayValue(); array != nil {
		for _, child := range array.GetValues() {
			assertOTLPAnyValueHasNoSecrets(t, location+" array child", child, secrets)
		}
	}
	if values := value.GetKvlistValue(); values != nil {
		assertOTLPAttributesHaveNoSecrets(t, location+" key/value child", values.GetValues(), secrets)
	}
}

func assertOTLPAttributes(t *testing.T, attributes []*commonpb.KeyValue, want map[string]string) {
	t.Helper()
	got := make(map[string]string, len(attributes))
	for _, attr := range attributes {
		got[attr.Key] = attr.Value.GetStringValue()
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OTLP attributes = %#v, want %#v", got, want)
	}
}

func assertOTLPAnyAttributes(t *testing.T, attributes []*commonpb.KeyValue, want map[string]any) {
	t.Helper()
	got := make(map[string]any, len(attributes))
	for _, attr := range attributes {
		switch value := attr.Value.GetValue().(type) {
		case *commonpb.AnyValue_StringValue:
			got[attr.Key] = value.StringValue
		case *commonpb.AnyValue_BoolValue:
			got[attr.Key] = value.BoolValue
		default:
			got[attr.Key] = attr.Value.String()
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OTLP attributes = %#v, want %#v", got, want)
	}
}

func findOTLPExemplar(exemplars []*metricpb.Exemplar, traceID, spanID []byte) *metricpb.Exemplar {
	for _, exemplar := range exemplars {
		if bytes.Equal(exemplar.TraceId, traceID) && bytes.Equal(exemplar.SpanId, spanID) {
			return exemplar
		}
	}
	return nil
}

func sumBuckets(counts []uint64) uint64 {
	var total uint64
	for _, count := range counts {
		total += count
	}
	return total
}
