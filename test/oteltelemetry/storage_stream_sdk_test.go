package oteltelemetry_test

import (
	"context"
	"io"
	"reflect"
	"testing"
	"time"

	vvotel "github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/storage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type sdkStorageStreamReader struct {
	readCalls  int
	closeCalls int
}

func (reader *sdkStorageStreamReader) Read([]byte) (int, error) {
	reader.readCalls++
	if reader.readCalls == 1 {
		return 3, nil
	}
	return 2, io.EOF
}

func (reader *sdkStorageStreamReader) Close() error {
	reader.closeCalls++
	return nil
}

type sdkStorageStreamStore struct {
	reader      io.ReadCloser
	info        storage.Info
	openCalls   int
	openContext context.Context
	openKey     storage.Key
	openOptions storage.ReadOptions
}

func (*sdkStorageStreamStore) Put(context.Context, storage.Key, io.Reader, storage.PutOptions) (storage.Info, error) {
	panic("unexpected Put")
}

func (store *sdkStorageStreamStore) Open(ctx context.Context, key storage.Key, options storage.ReadOptions) (io.ReadCloser, storage.Info, error) {
	store.openCalls++
	store.openContext = ctx
	store.openKey = key
	store.openOptions = options
	return store.reader, store.info, nil
}

func (*sdkStorageStreamStore) Head(context.Context, storage.Key) (storage.Info, error) {
	panic("unexpected Head")
}

func (*sdkStorageStreamStore) Delete(context.Context, storage.Key, storage.DeleteOptions) error {
	panic("unexpected Delete")
}

func (*sdkStorageStreamStore) Stage(context.Context, io.Reader, storage.StageOptions) (storage.Staged, error) {
	panic("unexpected Stage")
}

func (*sdkStorageStreamStore) Promote(context.Context, storage.StageID, storage.Key, storage.PromoteOptions) (storage.Info, error) {
	panic("unexpected Promote")
}

func (*sdkStorageStreamStore) Abort(context.Context, storage.StageID) error {
	panic("unexpected Abort")
}

func (*sdkStorageStreamStore) CleanupExpired(context.Context, storage.CleanupOptions) (storage.CleanupResult, error) {
	panic("unexpected CleanupExpired")
}

func (*sdkStorageStreamStore) TemporaryURL(context.Context, storage.Key, storage.TemporaryURLOptions) (storage.Link, error) {
	panic("unexpected TemporaryURL")
}

func (*sdkStorageStreamStore) Capabilities() storage.Capabilities {
	return storage.Capabilities{Staging: true}
}

type sdkStorageStreamContextKey struct{}

func TestRealSDKStorageStreamParentsToOpenAndCorrelatesMetrics(t *testing.T) {
	fixture := newSDKFixture(t, sdktrace.AlwaysSample())
	tel := vvotel.Must(vvotel.Config{TracerProvider: fixture.tracerProvider, MeterProvider: fixture.meterProvider})
	keyText := "private/stream-key-secret-49617"
	key, err := storage.ParseKey(keyText)
	if err != nil {
		t.Fatal(err)
	}
	length := int64(29)
	options := storage.ReadOptions{Offset: 7, Length: &length}
	reader := &sdkStorageStreamReader{}
	info := storage.Info{
		Size:        991,
		ContentType: "application/stream-info-secret-61358",
		Metadata:    storage.Metadata{"stream-info-secret-73145": "stream-info-secret-84106"},
		ModifiedAt:  time.Unix(238, 0),
		ETag:        "stream-etag-secret-59713",
		Version:     "stream-version-secret-92407",
	}
	raw := &sdkStorageStreamStore{reader: reader, info: info}
	requestContext := context.WithValue(context.Background(), sdkStorageStreamContextKey{}, "request-context-secret-39628")
	ctx, request := fixture.tracerProvider.Tracer("application").Start(requestContext, "storage request", trace.WithSpanKind(trace.SpanKindServer))
	wrapped, gotInfo, err := vvotel.Store(tel, vvotel.WithStorageStreams())(raw).Open(ctx, key, options)
	if err != nil || !reflect.DeepEqual(gotInfo, info) || raw.openCalls != 1 || raw.openKey != key || !reflect.DeepEqual(raw.openOptions, options) {
		t.Fatalf("Open = %T, %#v, %v; calls/key/options=%d/%v/%#v", wrapped, gotInfo, err, raw.openCalls, raw.openKey, raw.openOptions)
	}
	if raw.openContext.Value(sdkStorageStreamContextKey{}) != "request-context-secret-39628" {
		t.Fatal("Open lost the caller context")
	}
	openContext := trace.SpanContextFromContext(raw.openContext)
	if !openContext.IsValid() || openContext.Equal(request.SpanContext()) || openContext.TraceID() != request.SpanContext().TraceID() {
		t.Fatalf("Open context=%v request=%v", openContext, request.SpanContext())
	}
	stream, ok := wrapped.(vvotel.StorageStream)
	if !ok {
		t.Fatalf("Open reader type=%T, want vvotel.StorageStream", wrapped)
	}
	buffer := []byte("stream-buffer-secret-72519")
	if n, err := stream.Read(buffer); n != 3 || err != nil {
		t.Fatalf("first Read=(%d,%v)", n, err)
	}
	if n, err := stream.Read(buffer); n != 2 || err != io.EOF {
		t.Fatalf("terminal Read=(%d,%v)", n, err)
	}
	request.End()
	if reader.readCalls != 2 || reader.closeCalls != 0 {
		t.Fatalf("reader calls read=%d close=%d", reader.readCalls, reader.closeCalls)
	}

	spans := fixture.spans.Ended()
	if len(spans) != 3 {
		t.Fatalf("ended spans=%d, want request, Open and stream", len(spans))
	}
	requestSpan := findSpan(t, spans, "storage request")
	openSpan := findSpan(t, spans, vvotel.StorageSpanName(vvotel.OpStorageOpen))
	streamSpan := findSpan(t, spans, vvotel.SpanStorageStream)
	if openSpan.SpanKind() != trace.SpanKindInternal || !openSpan.Parent().Equal(requestSpan.SpanContext()) || !openSpan.SpanContext().Equal(openContext) {
		t.Fatalf("Open kind/parent/context=%s/%v/%v", openSpan.SpanKind(), openSpan.Parent(), openSpan.SpanContext())
	}
	if streamSpan.SpanKind() != trace.SpanKindInternal || !streamSpan.Parent().Equal(openSpan.SpanContext()) {
		t.Fatalf("stream kind/parent=%s/%v, want Open %v", streamSpan.SpanKind(), streamSpan.Parent(), openSpan.SpanContext())
	}
	streamAttributes := attributesByName(streamSpan.Attributes())
	if streamSpan.Status().Code != codes.Unset || streamAttributes[string(vvotel.AttrComponent)] != vvotel.ComponentStorageStream || streamAttributes[string(vvotel.AttrOperationName)] != vvotel.OpStorageStreamConsume || streamAttributes[string(vvotel.AttrOperationOutcome)] != "eof" {
		t.Fatalf("stream status/attributes=%#v/%#v", streamSpan.Status(), streamAttributes)
	}

	metrics := collectMetricData(t, fixture.metrics)
	assertFloatMetricExemplar(t, metrics, vvotel.MetricStorageStreamDuration, streamSpan.SpanContext())
	bytesMetric := findMetricData(t, metrics, vvotel.MetricStorageStreamBytes)
	bytesHistogram, ok := bytesMetric.Data.(metricdata.Histogram[int64])
	if !ok || len(bytesHistogram.DataPoints) != 1 || bytesHistogram.DataPoints[0].Count != 1 || bytesHistogram.DataPoints[0].Sum != 5 {
		t.Fatalf("stream bytes=%#v (%T)", bytesHistogram, bytesMetric.Data)
	}
	if !storageStreamSDKExemplarMatches(bytesHistogram.DataPoints[0].Exemplars, streamSpan.SpanContext()) {
		t.Fatal("stream byte metric lacks the stream span exemplar")
	}
	byteAttributes := attributesByName(bytesHistogram.DataPoints[0].Attributes.ToSlice())
	if byteAttributes[string(vvotel.AttrComponent)] != vvotel.ComponentStorageStream || byteAttributes[string(vvotel.AttrOperationName)] != vvotel.OpStorageStreamConsume || byteAttributes[string(vvotel.AttrOperationOutcome)] != "eof" {
		t.Fatalf("stream byte attributes=%#v", byteAttributes)
	}
	assertSDKPrivacy(t, spans, metrics, []string{
		keyText,
		info.ContentType,
		"stream-info-secret-73145",
		"stream-info-secret-84106",
		info.ETag,
		info.Version,
		string(buffer),
		"request-context-secret-39628",
	})
}

func storageStreamSDKExemplarMatches(exemplars []metricdata.Exemplar[int64], spanContext trace.SpanContext) bool {
	for _, exemplar := range exemplars {
		if exemplarMatches(exemplar.TraceID, exemplar.SpanID, spanContext) {
			return true
		}
	}
	return false
}
