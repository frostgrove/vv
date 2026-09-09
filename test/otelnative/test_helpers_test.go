package otelnative

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const (
	allowedServiceName = "transport-fixture"
	secretResource     = "secret-resource-4815"
)

type telemetryFixture struct {
	providers Providers
	spans     *tracetest.InMemoryExporter
	metrics   *metricCapture
	traces    *sdktrace.TracerProvider
	meters    *metric.MeterProvider
}

func newTelemetryFixture(t *testing.T, policy TraceProjectionPolicy, sampler sdktrace.Sampler) *telemetryFixture {
	t.Helper()
	t.Setenv("OTEL_SEMCONV_STABILITY_OPT_IN", "")
	spanSink := tracetest.NewInMemoryExporter()
	spanExporter, err := NewTransportSpanExporter(spanSink, policy)
	if err != nil {
		t.Fatal(err)
	}
	traceResource := resource.NewSchemaless(
		attribute.String("service.name", allowedServiceName),
		attribute.String("secret.resource", secretResource),
	)
	traceOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithSyncer(spanExporter),
		sdktrace.WithResource(traceResource),
	}
	if sampler != nil {
		traceOptions = append(traceOptions, sdktrace.WithSampler(sampler))
	}
	tracerProvider := sdktrace.NewTracerProvider(traceOptions...)
	metricSink := &metricCapture{}
	metricExporter, err := NewTransportMetricExporter(metricSink, policy)
	if err != nil {
		t.Fatal(err)
	}
	reader := metric.NewPeriodicReader(metricExporter)
	metricOptions := []metric.Option{
		metric.WithReader(reader),
		metric.WithResource(traceResource),
	}
	metricOptions = append(metricOptions, TransportMetricOptions(policy)...)
	meterProvider := metric.NewMeterProvider(metricOptions...)
	fixture := &telemetryFixture{
		providers: Providers{Tracer: tracerProvider, Meter: meterProvider},
		spans:     spanSink,
		metrics:   metricSink,
		traces:    tracerProvider,
		meters:    meterProvider,
	}
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(context.Background())
		_ = tracerProvider.Shutdown(context.Background())
	})
	return fixture
}

func (f *telemetryFixture) nativeSpans() tracetest.SpanStubs {
	all := f.spans.GetSpans()
	spans := make(tracetest.SpanStubs, 0, len(all))
	for _, span := range all {
		if _, known := nativeScope(span.InstrumentationScope); known {
			spans = append(spans, span)
		}
	}
	return spans
}

func (f *telemetryFixture) flushMetrics(t *testing.T) metricdata.ResourceMetrics {
	t.Helper()
	if err := f.meters.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	return f.metrics.snapshot()
}

type metricCapture struct {
	mu       sync.Mutex
	metrics  metricdata.ResourceMetrics
	flushes  int
	shutdown int
}

func (*metricCapture) Temporality(kind metric.InstrumentKind) metricdata.Temporality {
	return metric.DefaultTemporalitySelector(kind)
}

func (*metricCapture) Aggregation(kind metric.InstrumentKind) metric.Aggregation {
	return metric.DefaultAggregationSelector(kind)
}

func (c *metricCapture) Export(_ context.Context, metrics *metricdata.ResourceMetrics) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics = *metrics
	return nil
}

func (c *metricCapture) ForceFlush(context.Context) error {
	c.mu.Lock()
	c.flushes++
	c.mu.Unlock()
	return nil
}

func (c *metricCapture) Shutdown(context.Context) error {
	c.mu.Lock()
	c.shutdown++
	c.mu.Unlock()
	return nil
}

func (c *metricCapture) snapshot() metricdata.ResourceMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.metrics
}

type nameSampler struct {
	mu    sync.Mutex
	names []string
}

func (s *nameSampler) ShouldSample(parameters sdktrace.SamplingParameters) sdktrace.SamplingResult {
	s.mu.Lock()
	s.names = append(s.names, parameters.Name)
	s.mu.Unlock()
	return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample}
}

func (*nameSampler) Description() string {
	return "record span names"
}

func (s *nameSampler) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.names...)
}

func remoteHeaders(t *testing.T, header propagation.HeaderCarrier) trace.SpanContext {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("11111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("2222222222222222")
	if err != nil {
		t.Fatal(err)
	}
	traceState, err := trace.ParseTraceState("vendor=secret-state")
	if err != nil {
		t.Fatal(err)
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		TraceState: traceState,
		Remote:     true,
	})
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), spanContext)
	propagation.TraceContext{}.Inject(ctx, header)
	header.Set("baggage", "account=secret-baggage")
	return spanContext
}

func assertPublicSpan(t *testing.T, span tracetest.SpanStub, remote trace.SpanContext) {
	t.Helper()
	if span.Parent.IsValid() {
		t.Fatalf("public span parent=%v", span.Parent)
	}
	if span.SpanContext.TraceID() == remote.TraceID() {
		t.Fatalf("public span retained remote trace ID %s", remote.TraceID())
	}
	if len(span.Links) != 1 {
		t.Fatalf("public span links=%d", len(span.Links))
	}
	link := span.Links[0]
	if link.SpanContext.TraceID() != remote.TraceID() || link.SpanContext.SpanID() != remote.SpanID() {
		t.Fatalf("public link=%v remote=%v", link.SpanContext, remote)
	}
	if link.SpanContext.TraceState().Len() != 0 || len(link.Attributes) != 0 {
		t.Fatalf("public link leaked state or attributes: %#v", link)
	}
}

func assertTrustedSpan(t *testing.T, span tracetest.SpanStub, remote trace.SpanContext) {
	t.Helper()
	if span.Parent.TraceID() != remote.TraceID() || span.Parent.SpanID() != remote.SpanID() {
		t.Fatalf("trusted parent=%v remote=%v", span.Parent, remote)
	}
	if span.SpanContext.TraceID() != remote.TraceID() || len(span.Links) != 0 {
		t.Fatalf("trusted span=%v links=%#v", span.SpanContext, span.Links)
	}
}

func assertNoBaggage(t *testing.T, ctx context.Context) {
	t.Helper()
	if baggage.FromContext(ctx).Len() != 0 {
		t.Fatalf("baggage reached handler: %v", baggage.FromContext(ctx))
	}
	if trace.SpanContextFromContext(ctx).TraceState().Len() != 0 {
		t.Fatalf("tracestate reached handler: %v", trace.SpanContextFromContext(ctx).TraceState())
	}
}

func assertNativePrivacy(t *testing.T, spans tracetest.SpanStubs, metrics metricdata.ResourceMetrics, secrets ...string) {
	t.Helper()
	for _, span := range spans {
		parts := []string{
			span.Name,
			span.Status.Description,
			span.SpanContext.TraceState().String(),
			span.Parent.TraceState().String(),
			span.InstrumentationScope.Name,
			span.InstrumentationScope.Version,
			span.InstrumentationScope.SchemaURL,
		}
		for _, item := range span.Attributes {
			parts = append(parts, string(item.Key), item.Value.Emit())
		}
		for _, item := range span.Resource.Attributes() {
			parts = append(parts, string(item.Key), item.Value.Emit())
		}
		for _, event := range span.Events {
			parts = append(parts, event.Name)
			for _, item := range event.Attributes {
				parts = append(parts, string(item.Key), item.Value.Emit())
			}
		}
		for _, link := range span.Links {
			parts = append(parts, link.SpanContext.TraceState().String())
			for _, item := range link.Attributes {
				parts = append(parts, string(item.Key), item.Value.Emit())
			}
		}
		assertStringsExclude(t, "span", parts, secrets)
	}
	metricParts := metricPrivacyParts(metrics)
	assertStringsExclude(t, "metric", metricParts, secrets)
}

func assertNativeMetricsPresent(t *testing.T, metrics metricdata.ResourceMetrics) {
	t.Helper()
	measurements := 0
	for _, scope := range metrics.ScopeMetrics {
		if _, native := nativeScope(scope.Scope); !native {
			continue
		}
		measurements += len(scope.Metrics)
	}
	if measurements == 0 {
		t.Fatal("native metric export was empty")
	}

}

func assertNativeExemplarPresent(t *testing.T, metrics metricdata.ResourceMetrics) {
	t.Helper()
	exemplars := 0
	for _, scope := range metrics.ScopeMetrics {
		if _, native := nativeScope(scope.Scope); !native {
			continue
		}
		for _, measurement := range scope.Metrics {
			exemplars += aggregationExemplarCount(measurement.Data)
		}
	}
	if exemplars == 0 {
		t.Fatal("native metric export had no trace-based exemplar")
	}
}

func assertStringsExclude(t *testing.T, area string, values, secrets []string) {
	t.Helper()
	for _, value := range values {
		for _, secret := range secrets {
			if secret != "" && strings.Contains(value, secret) {
				t.Fatalf("%s leaked %q through %q", area, secret, value)
			}
		}
	}
}

func metricPrivacyParts(metrics metricdata.ResourceMetrics) []string {
	parts := []string{metrics.Resource.SchemaURL()}
	for _, item := range metrics.Resource.Attributes() {
		parts = append(parts, string(item.Key), item.Value.Emit())
	}
	for _, scope := range metrics.ScopeMetrics {
		parts = append(parts, scope.Scope.Name, scope.Scope.Version, scope.Scope.SchemaURL)
		for _, item := range scope.Scope.Attributes.ToSlice() {
			parts = append(parts, string(item.Key), item.Value.Emit())
		}
		for _, measurement := range scope.Metrics {
			parts = append(parts, measurement.Name, measurement.Description, measurement.Unit)
			parts = append(parts, aggregationPrivacyParts(measurement.Data)...)
		}
	}
	return parts
}

func aggregationPrivacyParts(aggregation metricdata.Aggregation) []string {
	switch data := aggregation.(type) {
	case metricdata.Gauge[int64]:
		return dataPointPrivacyParts(data.DataPoints)
	case metricdata.Gauge[float64]:
		return dataPointPrivacyParts(data.DataPoints)
	case metricdata.Sum[int64]:
		return dataPointPrivacyParts(data.DataPoints)
	case metricdata.Sum[float64]:
		return dataPointPrivacyParts(data.DataPoints)
	case metricdata.Histogram[int64]:
		return histogramPrivacyParts(data.DataPoints)
	case metricdata.Histogram[float64]:
		return histogramPrivacyParts(data.DataPoints)
	case metricdata.ExponentialHistogram[int64]:
		return exponentialHistogramPrivacyParts(data.DataPoints)
	case metricdata.ExponentialHistogram[float64]:
		return exponentialHistogramPrivacyParts(data.DataPoints)
	default:
		return []string{fmt.Sprintf("%T", aggregation)}
	}
}

func aggregationExemplarCount(aggregation metricdata.Aggregation) int {
	switch data := aggregation.(type) {
	case metricdata.Gauge[int64]:
		return dataPointExemplarCount(data.DataPoints)
	case metricdata.Gauge[float64]:
		return dataPointExemplarCount(data.DataPoints)
	case metricdata.Sum[int64]:
		return dataPointExemplarCount(data.DataPoints)
	case metricdata.Sum[float64]:
		return dataPointExemplarCount(data.DataPoints)
	case metricdata.Histogram[int64]:
		return histogramExemplarCount(data.DataPoints)
	case metricdata.Histogram[float64]:
		return histogramExemplarCount(data.DataPoints)
	case metricdata.ExponentialHistogram[int64]:
		return exponentialHistogramExemplarCount(data.DataPoints)
	case metricdata.ExponentialHistogram[float64]:
		return exponentialHistogramExemplarCount(data.DataPoints)
	default:
		return 0
	}
}

func dataPointExemplarCount[N int64 | float64](points []metricdata.DataPoint[N]) int {
	count := 0
	for _, point := range points {
		count += len(point.Exemplars)
	}
	return count
}

func histogramExemplarCount[N int64 | float64](points []metricdata.HistogramDataPoint[N]) int {
	count := 0
	for _, point := range points {
		count += len(point.Exemplars)
	}
	return count
}

func exponentialHistogramExemplarCount[N int64 | float64](points []metricdata.ExponentialHistogramDataPoint[N]) int {
	count := 0
	for _, point := range points {
		count += len(point.Exemplars)
	}
	return count
}

func dataPointPrivacyParts[N int64 | float64](points []metricdata.DataPoint[N]) []string {
	var parts []string
	for _, point := range points {
		parts = append(parts, attributeParts(point.Attributes)...)
		parts = append(parts, exemplarPrivacyParts(point.Exemplars)...)
	}
	return parts
}

func histogramPrivacyParts[N int64 | float64](points []metricdata.HistogramDataPoint[N]) []string {
	var parts []string
	for _, point := range points {
		parts = append(parts, attributeParts(point.Attributes)...)
		parts = append(parts, exemplarPrivacyParts(point.Exemplars)...)
	}
	return parts
}

func exponentialHistogramPrivacyParts[N int64 | float64](points []metricdata.ExponentialHistogramDataPoint[N]) []string {
	var parts []string
	for _, point := range points {
		parts = append(parts, attributeParts(point.Attributes)...)
		parts = append(parts, exemplarPrivacyParts(point.Exemplars)...)
	}
	return parts
}

func exemplarPrivacyParts[N int64 | float64](exemplars []metricdata.Exemplar[N]) []string {
	var parts []string
	for _, exemplar := range exemplars {
		for _, item := range exemplar.FilteredAttributes {
			parts = append(parts, string(item.Key), item.Value.Emit())
		}
	}
	return parts
}

func attributeParts(set attribute.Set) []string {
	attributes := set.ToSlice()
	parts := make([]string, 0, len(attributes)*2)
	for _, item := range attributes {
		parts = append(parts, string(item.Key), item.Value.Emit())
	}
	return parts
}

func findSpan(t *testing.T, spans tracetest.SpanStubs, name string, kind trace.SpanKind) tracetest.SpanStub {
	t.Helper()
	var matches []tracetest.SpanStub
	for _, span := range spans {
		if span.Name == name && span.SpanKind == kind {
			matches = append(matches, span)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("span %q/%s matches=%d all=%v", name, kind, len(matches), spanNames(spans))
	}
	return matches[0]
}

func spanNames(spans tracetest.SpanStubs) []string {
	names := make([]string, len(spans))
	for index, span := range spans {
		names[index] = span.Name
	}
	return names
}

func spanNamesAndKinds(spans tracetest.SpanStubs) []string {
	names := make([]string, len(spans))
	for index, span := range spans {
		names[index] = fmt.Sprintf("%s/%s", span.Name, span.SpanKind)
	}
	return names
}
