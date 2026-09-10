package main

import (
	"context"
	"strings"
	"testing"

	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const projectionSecret = "projection-secret-84291"

type captureSpanExporter struct {
	spans         []sdktrace.ReadOnlySpan
	shutdownCalls int
}

func (exporter *captureSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	exporter.spans = append(exporter.spans, spans...)
	return nil
}

func (exporter *captureSpanExporter) Shutdown(context.Context) error {
	exporter.shutdownCalls++
	return nil
}

type captureMetricExporter struct {
	metrics       []metricdata.ResourceMetrics
	shutdownCalls int
}

func (*captureMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (*captureMetricExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

func (exporter *captureMetricExporter) Export(_ context.Context, metrics *metricdata.ResourceMetrics) error {
	exporter.metrics = append(exporter.metrics, *metrics)
	return nil
}

func (*captureMetricExporter) ForceFlush(context.Context) error { return nil }

func (exporter *captureMetricExporter) Shutdown(context.Context) error {
	exporter.shutdownCalls++
	return nil
}

func TestSpanExportProjectionRemovesEveryUnapprovedField(t *testing.T) {
	config := validTelemetryConfig()
	projection := newExportProjection(config)
	downstream := &captureSpanExporter{}
	exporter := &projectingSpanExporter{next: downstream, projection: projection}
	traceState, err := trace.ParseTraceState("vendor=" + projectionSecret)
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
		TraceID:    trace.TraceID{3},
		SpanID:     trace.SpanID{4},
		TraceState: traceState,
		Remote:     true,
	})
	attributes := []attribute.KeyValue{
		vvotel.AttrComponent.String(vvotel.ComponentCommand),
		vvotel.AttrOperationName.String(vvotel.OpCommandGet),
		vvotel.AttrOperationOutcome.String(vvotel.OutcomeError),
		vvotel.AttrErrorType.String(vvotel.ErrorTypeInvalid),
		vvotel.AttrResourceName.String(config.FrameworkResource.Value()),
		attribute.String("url.full", "https://example.invalid/"+projectionSecret),
	}
	events := []sdktrace.Event{
		{
			Name: "auth.refusal",
			Attributes: []attribute.KeyValue{
				vvotel.AttrComponent.String(vvotel.ComponentAuthRefusal),
				vvotel.AttrOperationName.String(vvotel.OpAuthRefusalRefuse),
				vvotel.AttrOperationOutcome.String("refused"),
				vvotel.AttrReason.String("no_credential"),
				attribute.String("exception.message", projectionSecret),
			},
		},
		{Name: projectionSecret, Attributes: []attribute.KeyValue{attribute.String("secret", projectionSecret)}},
	}
	links := []sdktrace.Link{{SpanContext: parent, Attributes: []attribute.KeyValue{attribute.String("secret", projectionSecret)}}}
	span := tracetest.SpanStub{
		Name:        "vv.command get",
		SpanContext: spanContext,
		Parent:      parent,
		Attributes:  attributes,
		Events:      events,
		Links:       links,
		Status:      sdktrace.Status{Code: codes.Error, Description: projectionSecret},
		Resource:    resource.NewSchemaless(attribute.String("secret.resource", projectionSecret)),
		InstrumentationScope: instrumentation.Scope{
			Name:    vvotel.ScopeName,
			Version: vvotel.ScopeVersion,
		},
	}.Snapshot()

	if err := exporter.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{span}); err != nil {
		t.Fatal(err)
	}
	if len(downstream.spans) != 1 {
		t.Fatalf("exported spans = %d", len(downstream.spans))
	}
	projected := downstream.spans[0]
	if projected.Name() != "vv.command get" || projected.Status().Code != codes.Error || projected.Status().Description != "" {
		t.Fatalf("projected name/status = %q/%#v", projected.Name(), projected.Status())
	}
	if projected.SpanContext().TraceState().Len() != 0 || projected.Parent().TraceState().Len() != 0 {
		t.Fatal("span or parent tracestate survived export projection")
	}
	if len(projected.Events()) != 1 || projected.Events()[0].Name != "auth.refusal" || len(projected.Events()[0].Attributes) != 4 {
		t.Fatalf("projected events = %#v", projected.Events())
	}
	if len(projected.Links()) != 1 || projected.Links()[0].SpanContext.TraceState().Len() != 0 || len(projected.Links()[0].Attributes) != 0 {
		t.Fatalf("projected links = %#v", projected.Links())
	}
	if projected.InstrumentationScope() != projected.InstrumentationLibrary() || projected.InstrumentationScope() != projection.scope {
		t.Fatalf("projected scope mismatch = %#v/%#v", projected.InstrumentationScope(), projected.InstrumentationLibrary())
	}
	assertNoProjectedSecret(t, projected)

	attributes[0] = attribute.String("mutated", projectionSecret)
	events[0].Attributes[0] = attribute.String("mutated", projectionSecret)
	links[0].Attributes[0] = attribute.String("mutated", projectionSecret)
	assertNoProjectedSecret(t, projected)
}

func TestSpanExportProjectionDropsUnknownScopeAndInvalidContract(t *testing.T) {
	projection := newExportProjection(validTelemetryConfig())
	downstream := &captureSpanExporter{}
	exporter := &projectingSpanExporter{next: downstream, projection: projection}
	unknownScope := tracetest.SpanStub{
		Name:                 "vv.command get",
		InstrumentationScope: instrumentation.Scope{Name: projectionSecret},
	}.Snapshot()
	invalidContract := tracetest.SpanStub{
		Name: "vv.command get",
		Attributes: []attribute.KeyValue{
			vvotel.AttrComponent.String(vvotel.ComponentCommand),
			vvotel.AttrOperationName.String(vvotel.OpCommandGet),
			vvotel.AttrOperationOutcome.String(projectionSecret),
		},
		InstrumentationScope: projection.scope,
	}.Snapshot()
	if err := exporter.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{unknownScope, invalidContract}); err != nil {
		t.Fatal(err)
	}
	if len(downstream.spans) != 0 {
		t.Fatalf("invalid spans exported = %d", len(downstream.spans))
	}
}

func TestMetricExportProjectionCopiesAndBoundsDatapointsAndExemplars(t *testing.T) {
	projection := newExportProjection(validTelemetryConfig())
	downstream := &captureMetricExporter{}
	exporter := &projectingMetricExporter{next: downstream, projection: projection}
	validAttributes := []attribute.KeyValue{
		vvotel.AttrComponent.String(vvotel.ComponentJobsScheduler),
		vvotel.AttrOperationName.String(vvotel.OpJobsSchedulerRunDue),
		vvotel.AttrOperationOutcome.String(vvotel.OutcomeOk),
		attribute.String("url.full", projectionSecret),
	}
	invalidAttributes := []attribute.KeyValue{
		vvotel.AttrComponent.String(vvotel.ComponentJobsScheduler),
		vvotel.AttrOperationName.String(vvotel.OpJobsSchedulerRunDue),
		vvotel.AttrOperationOutcome.String(projectionSecret),
	}
	points := []metricdata.DataPoint[int64]{
		{
			Attributes: attribute.NewSet(validAttributes...),
			Value:      1,
			Exemplars: []metricdata.Exemplar[int64]{
				{Value: 1, FilteredAttributes: []attribute.KeyValue{vvotel.AttrComponent.String(vvotel.ComponentJobsScheduler), attribute.String("secret", projectionSecret)}, TraceID: []byte{1}, SpanID: []byte{2}},
			},
		},
		{Attributes: attribute.NewSet(invalidAttributes...), Value: 1},
	}
	input := metricdata.ResourceMetrics{
		Resource: resource.NewSchemaless(attribute.String("secret.resource", projectionSecret)),
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Scope: projection.scope,
				Metrics: []metricdata.Metrics{
					{Name: vvotel.MetricJobsSchedulerCycles, Description: projectionSecret, Unit: projectionSecret, Data: metricdata.Sum[int64]{DataPoints: points, Temporality: metricdata.CumulativeTemporality, IsMonotonic: true}},
					{Name: projectionSecret, Data: metricdata.Gauge[int64]{DataPoints: points}},
				},
			},
			{Scope: instrumentation.Scope{Name: projectionSecret}, Metrics: []metricdata.Metrics{{Name: projectionSecret, Data: metricdata.Gauge[int64]{DataPoints: points}}}},
		},
	}

	if err := exporter.Export(t.Context(), &input); err != nil {
		t.Fatal(err)
	}
	if len(downstream.metrics) != 1 || len(downstream.metrics[0].ScopeMetrics) != 1 || len(downstream.metrics[0].ScopeMetrics[0].Metrics) != 1 {
		t.Fatalf("projected resource metrics = %#v", downstream.metrics)
	}
	metric := downstream.metrics[0].ScopeMetrics[0].Metrics[0]
	sum, ok := metric.Data.(metricdata.Sum[int64])
	if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
		t.Fatalf("projected data = %#v", metric.Data)
	}
	if len(sum.DataPoints[0].Attributes.ToSlice()) != 3 || len(sum.DataPoints[0].Exemplars) != 1 || len(sum.DataPoints[0].Exemplars[0].FilteredAttributes) != 1 {
		t.Fatalf("projected point = %#v", sum.DataPoints[0])
	}
	points[0].Value = 99
	points[0].Exemplars[0].FilteredAttributes[0] = attribute.String("mutated", projectionSecret)
	if sum.DataPoints[0].Value != 1 || sum.DataPoints[0].Exemplars[0].FilteredAttributes[0].Key != vvotel.AttrComponent {
		t.Fatal("projection retained SDK-owned metric slices")
	}
	if strings.Contains(metric.Name+metric.Description+metric.Unit, projectionSecret) {
		t.Fatalf("metric metadata leaked secret: %#v", metric)
	}
	for _, candidate := range downstream.metrics[0].Resource.Attributes() {
		if strings.Contains(candidate.Value.Emit(), projectionSecret) {
			t.Fatalf("resource leaked secret: %v", candidate)
		}
	}
}

func assertNoProjectedSecret(t *testing.T, span sdktrace.ReadOnlySpan) {
	t.Helper()
	parts := []string{span.Name(), span.Status().Description, span.InstrumentationScope().Name, span.InstrumentationScope().Version, span.InstrumentationScope().SchemaURL}
	for _, candidate := range span.Attributes() {
		parts = append(parts, string(candidate.Key), candidate.Value.Emit())
	}
	for _, event := range span.Events() {
		parts = append(parts, event.Name)
		for _, candidate := range event.Attributes {
			parts = append(parts, string(candidate.Key), candidate.Value.Emit())
		}
	}
	for _, link := range span.Links() {
		for _, candidate := range link.Attributes {
			parts = append(parts, string(candidate.Key), candidate.Value.Emit())
		}
	}
	for _, candidate := range span.Resource().Attributes() {
		parts = append(parts, string(candidate.Key), candidate.Value.Emit())
	}
	if strings.Contains(strings.Join(parts, "\x00"), projectionSecret) {
		t.Fatalf("projected span leaked secret: %v", parts)
	}
}
