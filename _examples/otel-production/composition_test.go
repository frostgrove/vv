package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/attribute"
	apiMetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	apiTrace "go.opentelemetry.io/otel/trace"
)

var nativeTestScope = instrumentation.Scope{
	Name:      "example/native-http",
	Version:   "1.2.3",
	SchemaURL: "https://opentelemetry.io/schemas/1.37.0",
	Attributes: attribute.NewSet(
		attribute.String("integration.flavor", "native"),
	),
}

var databaseTestScope = instrumentation.Scope{
	Name:      "example/native-database",
	Version:   "4.5.6",
	SchemaURL: "https://opentelemetry.io/schemas/1.36.0",
	Attributes: attribute.NewSet(
		attribute.String("integration.flavor", "database"),
	),
}

type nativeTestSpanProjection struct {
	next          sdktrace.SpanExporter
	scope         instrumentation.Scope
	spanName      string
	projected     atomic.Int64
	foreign       atomic.Int64
	shutdownCalls atomic.Int64
}

func (exporter *nativeTestSpanProjection) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	result := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, span := range spans {
		if !instrumentationScopesEqual(span.InstrumentationScope(), exporter.scope) {
			exporter.foreign.Add(1)
			continue
		}
		exporter.projected.Add(1)
		projected := &projectedReadOnlySpan{
			ReadOnlySpan: span,
			name:         exporter.spanName,
			attributes:   []attribute.KeyValue{attribute.String("native.projection", "applied")},
			status:       sdktrace.Status{Code: span.Status().Code},
			resource:     resource.NewSchemaless(attribute.String("service.name", "orders-api")),
			scope:        exporter.scope,
			spanCtx:      sanitizeSpanContext(span.SpanContext()),
			parent:       sanitizeSpanContext(span.Parent()),
		}
		result = append(result, projected)
		forged := *projected
		forged.name = "forged " + projectionSecret
		forged.attributes = []attribute.KeyValue{attribute.String("forged.secret", projectionSecret)}
		forged.scope = frostgroveInstrumentationScope()
		result = append(result, &forged)
	}
	return exporter.next.ExportSpans(ctx, result)
}

func (exporter *nativeTestSpanProjection) Shutdown(ctx context.Context) error {
	exporter.shutdownCalls.Add(1)
	return exporter.next.Shutdown(ctx)
}

type nativeTestMetricProjection struct {
	next          sdkmetric.Exporter
	scope         instrumentation.Scope
	description   string
	projected     atomic.Int64
	foreign       atomic.Int64
	flushCalls    atomic.Int64
	shutdownCalls atomic.Int64
}

func (exporter *nativeTestMetricProjection) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return exporter.next.Temporality(kind)
}

func (exporter *nativeTestMetricProjection) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return exporter.next.Aggregation(kind)
}

func (exporter *nativeTestMetricProjection) Export(ctx context.Context, input *metricdata.ResourceMetrics) error {
	output := metricdata.ResourceMetrics{Resource: input.Resource}
	for _, scopeMetrics := range input.ScopeMetrics {
		if !instrumentationScopesEqual(scopeMetrics.Scope, exporter.scope) {
			exporter.foreign.Add(1)
			continue
		}
		exporter.projected.Add(1)
		projected := metricdata.ScopeMetrics{Scope: exporter.scope}
		for _, metric := range scopeMetrics.Metrics {
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			points := make([]metricdata.DataPoint[int64], len(sum.DataPoints))
			for index, point := range sum.DataPoints {
				point.Attributes = attribute.NewSet(attribute.String("native.projection", "applied"))
				point.Exemplars = nil
				points[index] = point
			}
			metric.Description = exporter.description
			metric.Data = metricdata.Sum[int64]{
				DataPoints:  points,
				Temporality: sum.Temporality,
				IsMonotonic: sum.IsMonotonic,
			}
			projected.Metrics = append(projected.Metrics, metric)
		}
		if len(projected.Metrics) > 0 {
			output.ScopeMetrics = append(output.ScopeMetrics, projected)
			output.ScopeMetrics = append(output.ScopeMetrics, metricdata.ScopeMetrics{
				Scope: frostgroveInstrumentationScope(),
				Metrics: []metricdata.Metrics{{
					Name: projectionSecret,
					Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{
						Attributes: attribute.NewSet(attribute.String("forged.secret", projectionSecret)),
						Value:      1,
					}}},
				}},
			})
			output.Resource = resource.NewSchemaless(attribute.String("service.name", "orders-api"))
		}
	}
	return exporter.next.Export(ctx, &output)
}

func (exporter *nativeTestMetricProjection) ForceFlush(ctx context.Context) error {
	exporter.flushCalls.Add(1)
	return exporter.next.ForceFlush(ctx)
}

func (exporter *nativeTestMetricProjection) Shutdown(ctx context.Context) error {
	exporter.shutdownCalls.Add(1)
	return exporter.next.Shutdown(ctx)
}

func TestTelemetryComposesFrameworkAndDeclaredNativeScopesThroughRealSDK(t *testing.T) {
	spanSink := &captureSpanExporter{}
	metricSink := &captureMetricExporter{}
	var httpSpanProjection *nativeTestSpanProjection
	var databaseSpanProjection *nativeTestSpanProjection
	var httpMetricProjection *nativeTestMetricProjection
	var databaseMetricProjection *nativeTestMetricProjection
	config := validTelemetryConfig()
	config.Sampler = sdktrace.AlwaysSample()
	config.BatchTimeout = time.Hour
	config.MetricInterval = time.Hour
	config.TraceProjectionLayers = []TraceProjectionLayer{
		{
			Scopes: []instrumentation.Scope{nativeTestScope},
			Wrap: func(next sdktrace.SpanExporter) (sdktrace.SpanExporter, error) {
				httpSpanProjection = &nativeTestSpanProjection{next: next, scope: nativeTestScope, spanName: "native.http.server"}
				return httpSpanProjection, nil
			},
		},
		{
			Scopes: []instrumentation.Scope{databaseTestScope},
			Wrap: func(next sdktrace.SpanExporter) (sdktrace.SpanExporter, error) {
				databaseSpanProjection = &nativeTestSpanProjection{next: next, scope: databaseTestScope, spanName: "native.db.client"}
				return databaseSpanProjection, nil
			},
		},
	}
	config.MetricProjectionLayers = []MetricProjectionLayer{
		{
			Scopes: []instrumentation.Scope{nativeTestScope},
			Wrap: func(next sdkmetric.Exporter) (sdkmetric.Exporter, error) {
				httpMetricProjection = &nativeTestMetricProjection{next: next, scope: nativeTestScope, description: "native HTTP projection applied"}
				return httpMetricProjection, nil
			},
		},
		{
			Scopes: []instrumentation.Scope{databaseTestScope},
			Wrap: func(next sdkmetric.Exporter) (sdkmetric.Exporter, error) {
				databaseMetricProjection = &nativeTestMetricProjection{next: next, scope: databaseTestScope, description: "native database projection applied"}
				return databaseMetricProjection, nil
			},
		},
	}
	telemetry, err := newTelemetry(t.Context(), config, telemetryFactories{
		resource: func(context.Context, Config) (*resource.Resource, error) {
			return resource.NewSchemaless(attribute.String("deployment.secret", projectionSecret)), nil
		},
		traceExporter: func(context.Context) (sdktrace.SpanExporter, error) {
			return spanSink, nil
		},
		metricExporter: func(context.Context) (sdkmetric.Exporter, error) {
			return metricSink, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = telemetry.Shutdown(context.Background()) })

	tracer := telemetry.TracerProvider.Tracer(nativeTestScope.Name,
		apiTrace.WithInstrumentationVersion(nativeTestScope.Version),
		apiTrace.WithSchemaURL(nativeTestScope.SchemaURL),
		apiTrace.WithInstrumentationAttributes(nativeTestScope.Attributes.ToSlice()...),
	)
	databaseTracer := telemetry.TracerProvider.Tracer(databaseTestScope.Name,
		apiTrace.WithInstrumentationVersion(databaseTestScope.Version),
		apiTrace.WithSchemaURL(databaseTestScope.SchemaURL),
		apiTrace.WithInstrumentationAttributes(databaseTestScope.Attributes.ToSlice()...),
	)
	meter := telemetry.MeterProvider.Meter(nativeTestScope.Name,
		apiMetric.WithInstrumentationVersion(nativeTestScope.Version),
		apiMetric.WithSchemaURL(nativeTestScope.SchemaURL),
		apiMetric.WithInstrumentationAttributes(nativeTestScope.Attributes.ToSlice()...),
	)
	counter, err := meter.Int64Counter("native.http.requests")
	if err != nil {
		t.Fatal(err)
	}
	databaseMeter := telemetry.MeterProvider.Meter(databaseTestScope.Name,
		apiMetric.WithInstrumentationVersion(databaseTestScope.Version),
		apiMetric.WithSchemaURL(databaseTestScope.SchemaURL),
		apiMetric.WithInstrumentationAttributes(databaseTestScope.Attributes.ToSlice()...),
	)
	databaseCounter, err := databaseMeter.Int64Counter("native.db.operations")
	if err != nil {
		t.Fatal(err)
	}
	nativeCtx, nativeSpan := tracer.Start(t.Context(), "GET /orders/"+projectionSecret,
		apiTrace.WithAttributes(attribute.String("http.request.header.authorization", projectionSecret)),
	)
	periodic := vvotel.Periodic(telemetry.Frostgrove, func(ctx context.Context) error {
		if apiTrace.SpanContextFromContext(ctx).TraceID() != apiTrace.SpanContextFromContext(nativeCtx).TraceID() {
			t.Fatal("framework operation lost the native parent trace")
		}
		databaseCtx, databaseSpan := databaseTracer.Start(ctx, "SELECT "+projectionSecret,
			apiTrace.WithAttributes(attribute.String("db.query.text", projectionSecret)),
		)
		databaseCounter.Add(databaseCtx, 1, apiMetric.WithAttributes(attribute.String("db.query.parameter", projectionSecret)))
		databaseSpan.End()
		return nil
	})
	if err := periodic(nativeCtx); err != nil {
		t.Fatal(err)
	}
	counter.Add(nativeCtx, 1, apiMetric.WithAttributes(attribute.String("user.secret", projectionSecret)))

	nearMissName := nativeTestScope
	nearMissName.Name += "/undeclared"
	nearMissVersion := nativeTestScope
	nearMissVersion.Version += ".undeclared"
	nearMissSchema := nativeTestScope
	nearMissSchema.SchemaURL = "https://opentelemetry.io/schemas/1.36.0"
	nearMissAttributes := nativeTestScope
	nearMissAttributes.Attributes = attribute.NewSet(attribute.String("integration.flavor", "undeclared"))
	for index, nearMissScope := range []instrumentation.Scope{nearMissName, nearMissVersion, nearMissSchema, nearMissAttributes} {
		unknownTracer := telemetry.TracerProvider.Tracer(nearMissScope.Name,
			apiTrace.WithInstrumentationVersion(nearMissScope.Version),
			apiTrace.WithSchemaURL(nearMissScope.SchemaURL),
			apiTrace.WithInstrumentationAttributes(nearMissScope.Attributes.ToSlice()...),
		)
		_, unknownSpan := unknownTracer.Start(nativeCtx, "undeclared "+projectionSecret)
		unknownSpan.End()
		unknownMeter := telemetry.MeterProvider.Meter(nearMissScope.Name,
			apiMetric.WithInstrumentationVersion(nearMissScope.Version),
			apiMetric.WithSchemaURL(nearMissScope.SchemaURL),
			apiMetric.WithInstrumentationAttributes(nearMissScope.Attributes.ToSlice()...),
		)
		unknownCounter, err := unknownMeter.Int64Counter("undeclared.requests." + string(rune('a'+index)))
		if err != nil {
			t.Fatal(err)
		}
		unknownCounter.Add(nativeCtx, 1)
	}
	nativeSpan.End()

	if err := telemetry.ForceFlush(t.Context()); err != nil {
		t.Fatal(err)
	}
	for name, projection := range map[string]*nativeTestSpanProjection{
		"http":     httpSpanProjection,
		"database": databaseSpanProjection,
	} {
		if projection == nil || projection.projected.Load() != 1 || projection.foreign.Load() != 0 {
			t.Fatalf("%s projected spans = %#v", name, projection)
		}
	}
	for name, projection := range map[string]*nativeTestMetricProjection{
		"http":     httpMetricProjection,
		"database": databaseMetricProjection,
	} {
		if projection == nil || projection.projected.Load() != 1 || projection.foreign.Load() != 0 || projection.flushCalls.Load() != 1 {
			t.Fatalf("%s projected scope metrics = %#v", name, projection)
		}
	}
	if metricSink.forceFlushCalls != 1 {
		t.Fatalf("raw metric force flush calls = %d", metricSink.forceFlushCalls)
	}
	if len(spanSink.batches) != 3 || spanSink.batches[0] != 1 || spanSink.batches[1] != 1 || spanSink.batches[2] != 1 || len(spanSink.spans) != 3 {
		t.Fatalf("exported span batches/spans = %v/%d", spanSink.batches, len(spanSink.spans))
	}
	var httpExported sdktrace.ReadOnlySpan
	var databaseExported sdktrace.ReadOnlySpan
	var frameworkExported sdktrace.ReadOnlySpan
	for _, span := range spanSink.spans {
		switch {
		case instrumentationScopesEqual(span.InstrumentationScope(), nativeTestScope):
			httpExported = span
		case instrumentationScopesEqual(span.InstrumentationScope(), databaseTestScope):
			databaseExported = span
		case instrumentationScopesEqual(span.InstrumentationScope(), frostgroveInstrumentationScope()):
			frameworkExported = span
		default:
			t.Fatalf("undeclared span scope reached sink: %#v", span.InstrumentationScope())
		}
	}
	if httpExported == nil || databaseExported == nil || frameworkExported == nil || httpExported.Name() != "native.http.server" || databaseExported.Name() != "native.db.client" {
		t.Fatalf("composed spans = %#v", spanSink.spans)
	}
	if frameworkExported.Parent().SpanID() != httpExported.SpanContext().SpanID() || frameworkExported.SpanContext().TraceID() != httpExported.SpanContext().TraceID() {
		t.Fatalf("HTTP/framework parentage = %v -> %v", httpExported.SpanContext(), frameworkExported.Parent())
	}
	if databaseExported.Parent().SpanID() != frameworkExported.SpanContext().SpanID() || databaseExported.SpanContext().TraceID() != frameworkExported.SpanContext().TraceID() {
		t.Fatalf("framework/database parentage = %v -> %v", frameworkExported.SpanContext(), databaseExported.Parent())
	}
	assertNoProjectedSecret(t, httpExported)
	assertNoProjectedSecret(t, databaseExported)
	assertNoProjectedSecret(t, frameworkExported)

	var httpMetric *metricdata.Metrics
	var databaseMetric *metricdata.Metrics
	frameworkMetrics := 0
	for metricBatchIndex := range metricSink.metrics {
		batch := &metricSink.metrics[metricBatchIndex]
		for _, candidate := range batch.Resource.Attributes() {
			if strings.Contains(candidate.Value.Emit(), projectionSecret) {
				t.Fatalf("metric resource secret reached sink: %v", candidate)
			}
		}
		for scopeIndex := range batch.ScopeMetrics {
			scopeMetrics := &batch.ScopeMetrics[scopeIndex]
			if !instrumentationScopesEqual(scopeMetrics.Scope, nativeTestScope) && !instrumentationScopesEqual(scopeMetrics.Scope, databaseTestScope) && !instrumentationScopesEqual(scopeMetrics.Scope, frostgroveInstrumentationScope()) {
				t.Fatalf("undeclared metric scope reached sink: %#v", scopeMetrics.Scope)
			}
			for metricIndex := range scopeMetrics.Metrics {
				candidate := &scopeMetrics.Metrics[metricIndex]
				if strings.Contains(candidate.Name+candidate.Description+candidate.Unit, projectionSecret) {
					t.Fatalf("metric metadata secret reached sink: %#v", candidate)
				}
				if candidate.Name == "native.http.requests" {
					httpMetric = candidate
				} else if candidate.Name == "native.db.operations" {
					databaseMetric = candidate
				} else if candidate.Name == vvotel.MetricRuntimePeriodicDuration {
					frameworkMetrics++
				} else {
					t.Fatalf("unprojected metric reached sink: %#v", candidate)
				}
			}
		}
	}
	if httpMetric == nil || httpMetric.Description != "native HTTP projection applied" || databaseMetric == nil || databaseMetric.Description != "native database projection applied" {
		t.Fatalf("declared metric bypassed its projection: HTTP %#v, database %#v", httpMetric, databaseMetric)
	}
	for name, metric := range map[string]*metricdata.Metrics{"http": httpMetric, "database": databaseMetric} {
		nativeSum, ok := metric.Data.(metricdata.Sum[int64])
		if !ok || len(nativeSum.DataPoints) != 1 || len(nativeSum.DataPoints[0].Attributes.ToSlice()) != 1 {
			t.Fatalf("%s projected metric = %#v", name, metric)
		}
		if strings.Contains(nativeSum.DataPoints[0].Attributes.Encoded(attribute.DefaultEncoder()), projectionSecret) {
			t.Fatalf("%s metric secret reached sink", name)
		}
	}
	if frameworkMetrics != 1 {
		t.Fatalf("framework metrics = %d", frameworkMetrics)
	}

	if err := telemetry.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if spanSink.shutdownCalls != 1 || metricSink.shutdownCalls != 1 || httpSpanProjection.shutdownCalls.Load() != 1 || databaseSpanProjection.shutdownCalls.Load() != 1 || httpMetricProjection.shutdownCalls.Load() != 1 || databaseMetricProjection.shutdownCalls.Load() != 1 {
		t.Fatalf("shutdown forwarding = sinks %d/%d, trace projections %d/%d, metric projections %d/%d", spanSink.shutdownCalls, metricSink.shutdownCalls, httpSpanProjection.shutdownCalls.Load(), databaseSpanProjection.shutdownCalls.Load(), httpMetricProjection.shutdownCalls.Load(), databaseMetricProjection.shutdownCalls.Load())
	}
}

func TestProjectionLayerConfigIsCopiedAndRejectsAmbiguousScopes(t *testing.T) {
	traceScopes := []instrumentation.Scope{nativeTestScope}
	metricScopes := []instrumentation.Scope{nativeTestScope}
	config := validTelemetryConfig()
	config.TraceProjectionLayers = []TraceProjectionLayer{{
		Scopes: traceScopes,
		Wrap: func(next sdktrace.SpanExporter) (sdktrace.SpanExporter, error) {
			return next, nil
		},
	}}
	config.MetricProjectionLayers = []MetricProjectionLayer{{
		Scopes: metricScopes,
		Wrap: func(next sdkmetric.Exporter) (sdkmetric.Exporter, error) {
			return next, nil
		},
	}}
	normalized, err := normalizeTelemetryConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	traceScopes[0].Name = "mutated"
	traceScopes[0].Attributes = attribute.NewSet(attribute.String("integration.flavor", "mutated"))
	metricScopes[0].Name = "mutated"
	metricScopes[0].Attributes = attribute.NewSet(attribute.String("integration.flavor", "mutated"))
	config.TraceProjectionLayers[0].Scopes[0].Version = "mutated"
	config.TraceProjectionLayers[0].Wrap = nil
	config.MetricProjectionLayers[0].Scopes[0].Version = "mutated"
	config.MetricProjectionLayers[0].Wrap = nil
	if !instrumentationScopesEqual(normalized.TraceProjectionLayers[0].Scopes[0], nativeTestScope) || normalized.TraceProjectionLayers[0].Wrap == nil || !instrumentationScopesEqual(normalized.MetricProjectionLayers[0].Scopes[0], nativeTestScope) || normalized.MetricProjectionLayers[0].Wrap == nil {
		t.Fatalf("normalized projection layers retained caller slices: %#v/%#v", normalized.TraceProjectionLayers, normalized.MetricProjectionLayers)
	}

	tracePass := func(next sdktrace.SpanExporter) (sdktrace.SpanExporter, error) { return next, nil }
	metricPass := func(next sdkmetric.Exporter) (sdkmetric.Exporter, error) { return next, nil }
	invalidTrace := []TraceProjectionLayer{
		{Scopes: []instrumentation.Scope{nativeTestScope}},
		{Wrap: tracePass},
		{Scopes: []instrumentation.Scope{{Name: ""}}, Wrap: tracePass},
		{Scopes: []instrumentation.Scope{{Name: "native", SchemaURL: "://invalid"}}, Wrap: tracePass},
		{Scopes: []instrumentation.Scope{nativeTestScope, nativeTestScope}, Wrap: tracePass},
		{Scopes: []instrumentation.Scope{frostgroveInstrumentationScope()}, Wrap: tracePass},
	}
	for index, layer := range invalidTrace {
		candidate := validTelemetryConfig()
		candidate.TraceProjectionLayers = []TraceProjectionLayer{layer}
		if _, err := normalizeTelemetryConfig(candidate); !errors.Is(err, ErrInvalidTelemetryConfig) {
			t.Fatalf("invalid trace layer %d = %v", index, err)
		}
	}
	duplicateAcrossTraceLayers := validTelemetryConfig()
	semanticDuplicate := nativeTestScope
	semanticDuplicate.Attributes = attribute.NewSet(attribute.String("integration.flavor", "native"))
	duplicateAcrossTraceLayers.TraceProjectionLayers = []TraceProjectionLayer{
		{Scopes: []instrumentation.Scope{nativeTestScope}, Wrap: tracePass},
		{Scopes: []instrumentation.Scope{semanticDuplicate}, Wrap: tracePass},
	}
	if _, err := normalizeTelemetryConfig(duplicateAcrossTraceLayers); !errors.Is(err, ErrInvalidTelemetryConfig) {
		t.Fatalf("duplicate trace scopes = %v", err)
	}

	invalidMetric := []MetricProjectionLayer{
		{Scopes: []instrumentation.Scope{nativeTestScope}},
		{Wrap: metricPass},
		{Scopes: []instrumentation.Scope{{Name: ""}}, Wrap: metricPass},
		{Scopes: []instrumentation.Scope{{Name: "native", SchemaURL: "://invalid"}}, Wrap: metricPass},
		{Scopes: []instrumentation.Scope{nativeTestScope, nativeTestScope}, Wrap: metricPass},
		{Scopes: []instrumentation.Scope{frostgroveInstrumentationScope()}, Wrap: metricPass},
	}
	for index, layer := range invalidMetric {
		candidate := validTelemetryConfig()
		candidate.MetricProjectionLayers = []MetricProjectionLayer{layer}
		if _, err := normalizeTelemetryConfig(candidate); !errors.Is(err, ErrInvalidTelemetryConfig) {
			t.Fatalf("invalid metric layer %d = %v", index, err)
		}
	}
	duplicateAcrossMetricLayers := validTelemetryConfig()
	duplicateAcrossMetricLayers.MetricProjectionLayers = []MetricProjectionLayer{
		{Scopes: []instrumentation.Scope{nativeTestScope}, Wrap: metricPass},
		{Scopes: []instrumentation.Scope{semanticDuplicate}, Wrap: metricPass},
	}
	if _, err := normalizeTelemetryConfig(duplicateAcrossMetricLayers); !errors.Is(err, ErrInvalidTelemetryConfig) {
		t.Fatalf("duplicate metric scopes = %v", err)
	}
	attributeNearMatch := nativeTestScope
	attributeNearMatch.Attributes = attribute.NewSet(attribute.String("integration.flavor", "near-match"))
	distinctAttributeScopes := validTelemetryConfig()
	distinctAttributeScopes.TraceProjectionLayers = []TraceProjectionLayer{
		{Scopes: []instrumentation.Scope{nativeTestScope}, Wrap: tracePass},
		{Scopes: []instrumentation.Scope{attributeNearMatch}, Wrap: tracePass},
	}
	distinctAttributeScopes.MetricProjectionLayers = []MetricProjectionLayer{
		{Scopes: []instrumentation.Scope{nativeTestScope}, Wrap: metricPass},
		{Scopes: []instrumentation.Scope{attributeNearMatch}, Wrap: metricPass},
	}
	if _, err := normalizeTelemetryConfig(distinctAttributeScopes); err != nil {
		t.Fatalf("distinct scope attributes rejected: %v", err)
	}
}
