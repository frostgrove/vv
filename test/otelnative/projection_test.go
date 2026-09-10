package otelnative

import (
	"context"
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestTransportSpanProjectionCoversEveryExportFieldWithoutMutation(t *testing.T) {
	traceState, err := trace.ParseTraceState("vendor=secret-projection-state")
	if err != nil {
		t.Fatal(err)
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{2},
		TraceFlags: trace.FlagsSampled,
		TraceState: traceState,
	})
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{3},
		TraceFlags: trace.FlagsSampled,
		TraceState: traceState,
		Remote:     true,
	})
	original := tracetest.SpanStub{
		Name:        "GET /secret-projection-id",
		SpanContext: spanContext,
		Parent:      parent,
		SpanKind:    trace.SpanKindServer,
		Attributes: []attribute.KeyValue{
			attribute.String("http.request.method", "SECRET-METHOD"),
			attribute.String("url.full", "https://secret-projection-host/secret-projection-id"),
		},
		Events: []sdktrace.Event{{
			Name:       "exception",
			Attributes: []attribute.KeyValue{attribute.String("exception.message", "secret-projection-error")},
		}},
		Links: []sdktrace.Link{{
			SpanContext: parent,
			Attributes:  []attribute.KeyValue{attribute.String("secret.link", "secret-projection-link")},
		}},
		Status: sdktrace.Status{Code: codes.Error, Description: "secret-projection-status"},
		Resource: resource.NewSchemaless(
			attribute.String("service.name", allowedServiceName),
			attribute.String("secret.resource", secretResource),
		),
		InstrumentationScope: instrumentation.Scope{
			Name:       otelhttp.ScopeName,
			Version:    otelhttp.Version,
			Attributes: attribute.NewSet(attribute.String("secret.scope", "secret-projection-scope")),
		},
	}
	sink := tracetest.NewInMemoryExporter()
	exporter, err := NewTransportSpanExporter(sink, TraceProjectionPolicy{
		ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", allowedServiceName)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{original.Snapshot()}); err != nil {
		t.Fatal(err)
	}
	spans := sink.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans=%d", len(spans))
	}
	projected := spans[0]
	if projected.Name != fallbackHTTPName || projected.Status.Code != codes.Error || projected.Status.Description != "" {
		t.Fatalf("projected name/status=%q/%#v", projected.Name, projected.Status)
	}
	if len(projected.Attributes) != 1 || projected.Attributes[0].Value.AsString() != "_OTHER" || len(projected.Events) != 0 || len(projected.Links) != 1 {
		t.Fatalf("projected fields attrs=%#v events=%#v links=%#v", projected.Attributes, projected.Events, projected.Links)
	}
	assertNativePrivacy(t, spans, metricdata.ResourceMetrics{Resource: resource.Empty()},
		secretResource,
		"secret-projection-state",
		"secret-projection-host",
		"secret-projection-id",
		"secret-projection-error",
		"secret-projection-link",
		"secret-projection-status",
		"secret-projection-scope",
	)
	if original.Name != "GET /secret-projection-id" || original.Status.Description != "secret-projection-status" || len(original.Events) != 1 || len(original.Links[0].Attributes) != 1 {
		t.Fatalf("input span mutated: %#v", original)
	}
}

func TestTransportMetricProjectionCoversExemplarsAndDoesNotAliasInput(t *testing.T) {
	originalBounds := []float64{0.1, 1}
	originalBuckets := []uint64{1, 2, 3}
	originalTraceID := []byte{1, 2, 3}
	original := metricdata.ResourceMetrics{
		Resource: resource.NewSchemaless(
			attribute.String("service.name", allowedServiceName),
			attribute.String("secret.resource", secretResource),
		),
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Scope: instrumentation.Scope{
				Name:       otelhttp.ScopeName,
				Version:    otelhttp.Version,
				Attributes: attribute.NewSet(attribute.String("secret.scope", "secret-metric-scope")),
			},
			Metrics: []metricdata.Metrics{
				{
					Name: "http.server.request.duration",
					Data: metricdata.Histogram[float64]{
						DataPoints: []metricdata.HistogramDataPoint[float64]{{
							Attributes: attribute.NewSet(
								attribute.String("http.request.method", "SECRET-METHOD"),
								attribute.String("url.full", "https://secret-metric-host/secret-metric-id"),
							),
							Bounds:       originalBounds,
							BucketCounts: originalBuckets,
							Exemplars: []metricdata.Exemplar[float64]{{
								FilteredAttributes: []attribute.KeyValue{attribute.String("url.full", "secret-exemplar-url")},
								TraceID:            originalTraceID,
							}},
						}},
					},
				},
				{Name: "secret.metric.name", Data: metricdata.Gauge[int64]{}},
			},
		}},
	}
	sink := &metricCapture{}
	exporter, err := NewTransportMetricExporter(sink, TraceProjectionPolicy{
		ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", allowedServiceName)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := exporter.Export(context.Background(), &original); err != nil {
		t.Fatal(err)
	}
	projected := sink.snapshot()
	if len(projected.ScopeMetrics) != 1 || len(projected.ScopeMetrics[0].Metrics) != 1 {
		t.Fatalf("projected metric roster=%#v", projected.ScopeMetrics)
	}
	histogram := projected.ScopeMetrics[0].Metrics[0].Data.(metricdata.Histogram[float64])
	if len(histogram.DataPoints) != 1 || histogram.DataPoints[0].Attributes.Len() != 1 || len(histogram.DataPoints[0].Exemplars) != 1 || len(histogram.DataPoints[0].Exemplars[0].FilteredAttributes) != 0 {
		t.Fatalf("projected datapoint=%#v", histogram.DataPoints)
	}
	assertNativePrivacy(t, nil, projected,
		secretResource,
		"secret-metric-scope",
		"secret-metric-host",
		"secret-metric-id",
		"secret-exemplar-url",
		"secret.metric.name",
	)
	histogram.DataPoints[0].Bounds[0] = 99
	histogram.DataPoints[0].BucketCounts[0] = 99
	histogram.DataPoints[0].Exemplars[0].TraceID[0] = 99
	originalHistogram := original.ScopeMetrics[0].Metrics[0].Data.(metricdata.Histogram[float64])
	if originalHistogram.DataPoints[0].Bounds[0] != 0.1 || originalHistogram.DataPoints[0].BucketCounts[0] != 1 || originalHistogram.DataPoints[0].Exemplars[0].TraceID[0] != 1 {
		t.Fatalf("input metric data aliased: %#v", originalHistogram.DataPoints[0])
	}
	if err := exporter.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := exporter.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sink.flushes != 1 || sink.shutdown != 1 {
		t.Fatalf("lifecycle flush=%d shutdown=%d", sink.flushes, sink.shutdown)
	}
}
