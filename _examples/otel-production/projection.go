package main

import (
	"context"
	"strconv"

	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

type exportProjection struct {
	resource      *resource.Resource
	scope         instrumentation.Scope
	spanSignals   map[string][]vvotel.SignalDescriptor
	eventSignals  map[string][]vvotel.SignalDescriptor
	metricSignals map[string][]vvotel.SignalDescriptor
	declared      map[attribute.Key]map[string]struct{}
}

func newExportProjection(config Config) *exportProjection {
	projection := &exportProjection{
		resource: resource.NewWithAttributes("",
			semconv.ServiceName(config.ServiceName),
			semconv.ServiceVersion(config.ServiceVersion),
			semconv.ServiceNamespace(config.ServiceNamespace),
		),
		scope: instrumentation.Scope{
			Name:    vvotel.ScopeName,
			Version: vvotel.ScopeVersion,
		},
		spanSignals:   make(map[string][]vvotel.SignalDescriptor),
		eventSignals:  make(map[string][]vvotel.SignalDescriptor),
		metricSignals: make(map[string][]vvotel.SignalDescriptor),
		declared: map[attribute.Key]map[string]struct{}{
			vvotel.AttrResourceName: {},
		},
	}
	resources := projection.declared[vvotel.AttrResourceName]
	if config.FrameworkResource != "" {
		resources[config.FrameworkResource.Value()] = struct{}{}
	}
	for _, name := range config.FrameworkResources {
		resources[name.Value()] = struct{}{}
	}
	for _, descriptor := range vvotel.SignalDescriptors() {
		if descriptor.Availability != "implemented" {
			continue
		}
		switch descriptor.Kind {
		case "span":
			for _, name := range descriptor.Names {
				projection.spanSignals[name] = append(projection.spanSignals[name], descriptor)
			}
		case "span_event":
			for _, name := range descriptor.Names {
				projection.eventSignals[name] = append(projection.eventSignals[name], descriptor)
			}
		case "metric":
			projection.metricSignals[descriptor.Name] = append(projection.metricSignals[descriptor.Name], descriptor)
		}
	}
	return projection
}

type projectingSpanExporter struct {
	next       sdktrace.SpanExporter
	projection *exportProjection
}

func (exporter *projectingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	projected := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, span := range spans {
		if candidate, ok := exporter.projection.projectSpan(span); ok {
			projected = append(projected, candidate)
		}
	}
	if len(projected) == 0 {
		return nil
	}
	return exporter.next.ExportSpans(ctx, projected)
}

func (exporter *projectingSpanExporter) Shutdown(ctx context.Context) error {
	return exporter.next.Shutdown(ctx)
}

type projectedReadOnlySpan struct {
	sdktrace.ReadOnlySpan
	name       string
	attributes []attribute.KeyValue
	events     []sdktrace.Event
	links      []sdktrace.Link
	status     sdktrace.Status
	resource   *resource.Resource
	scope      instrumentation.Scope
	spanCtx    trace.SpanContext
	parent     trace.SpanContext
}

func (span *projectedReadOnlySpan) Name() string { return span.name }

func (span *projectedReadOnlySpan) Attributes() []attribute.KeyValue {
	return cloneAttributes(span.attributes)
}

func (span *projectedReadOnlySpan) Events() []sdktrace.Event {
	return cloneEvents(span.events)
}

func (span *projectedReadOnlySpan) Links() []sdktrace.Link {
	return cloneLinks(span.links)
}

func (span *projectedReadOnlySpan) Status() sdktrace.Status { return span.status }

func (span *projectedReadOnlySpan) Resource() *resource.Resource { return span.resource }

func (span *projectedReadOnlySpan) InstrumentationScope() instrumentation.Scope { return span.scope }

func (span *projectedReadOnlySpan) InstrumentationLibrary() instrumentation.Library {
	return span.scope
}

func (span *projectedReadOnlySpan) SpanContext() trace.SpanContext { return span.spanCtx }

func (span *projectedReadOnlySpan) Parent() trace.SpanContext { return span.parent }

func (projection *exportProjection) projectSpan(span sdktrace.ReadOnlySpan) (sdktrace.ReadOnlySpan, bool) {
	if projection == nil || span == nil || span.InstrumentationScope() != projection.scope {
		return nil, false
	}
	signals := projection.spanSignals[span.Name()]
	status := spanStatusName(span.Status().Code)
	attributes, ok := projection.projectSignalAttributes(span.Attributes(), signals, status)
	if !ok {
		return nil, false
	}
	return &projectedReadOnlySpan{
		ReadOnlySpan: span,
		name:         span.Name(),
		attributes:   attributes,
		events:       projection.projectEvents(span.Events()),
		links:        projectLinks(span.Links()),
		status:       sdktrace.Status{Code: span.Status().Code},
		resource:     projection.resource,
		scope:        projection.scope,
		spanCtx:      sanitizeSpanContext(span.SpanContext()),
		parent:       sanitizeSpanContext(span.Parent()),
	}, true
}

func (projection *exportProjection) projectEvents(events []sdktrace.Event) []sdktrace.Event {
	result := make([]sdktrace.Event, 0, len(events))
	for _, event := range events {
		attributes, ok := projection.projectSignalAttributes(event.Attributes, projection.eventSignals[event.Name], "")
		if !ok {
			continue
		}
		result = append(result, sdktrace.Event{
			Name:                  event.Name,
			Attributes:            attributes,
			DroppedAttributeCount: event.DroppedAttributeCount + len(event.Attributes) - len(attributes),
			Time:                  event.Time,
		})
	}
	return result
}

func projectLinks(links []sdktrace.Link) []sdktrace.Link {
	result := make([]sdktrace.Link, 0, len(links))
	for _, link := range links {
		spanContext := link.SpanContext
		if !spanContext.IsValid() {
			continue
		}
		result = append(result, sdktrace.Link{
			SpanContext:           sanitizeSpanContext(spanContext),
			DroppedAttributeCount: link.DroppedAttributeCount + len(link.Attributes),
		})
	}
	return result
}

func sanitizeSpanContext(spanContext trace.SpanContext) trace.SpanContext {
	if !spanContext.IsValid() {
		return trace.SpanContext{}
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    spanContext.TraceID(),
		SpanID:     spanContext.SpanID(),
		TraceFlags: spanContext.TraceFlags(),
		Remote:     spanContext.IsRemote(),
	})
}

func spanStatusName(code codes.Code) string {
	switch code {
	case codes.Unset:
		return "unset"
	case codes.Error:
		return "error"
	case codes.Ok:
		return "ok"
	default:
		return "unknown"
	}
}

func (projection *exportProjection) projectSignalAttributes(input []attribute.KeyValue, signals []vvotel.SignalDescriptor, status string) ([]attribute.KeyValue, bool) {
	if len(signals) == 0 {
		return nil, false
	}
	attributes, ok := projection.filterKnownAttributes(input, signals)
	if !ok {
		return nil, false
	}
	for _, signal := range signals {
		if projection.descriptorAccepts(signal, status, attributes) {
			return attributes, true
		}
	}
	return nil, false
}

func (projection *exportProjection) filterKnownAttributes(input []attribute.KeyValue, signals []vvotel.SignalDescriptor) ([]attribute.KeyValue, bool) {
	seen := make(map[attribute.Key]struct{}, len(input))
	result := make([]attribute.KeyValue, 0, len(input))
	for _, candidate := range input {
		if _, duplicate := seen[candidate.Key]; duplicate {
			return nil, false
		}
		seen[candidate.Key] = struct{}{}
		if projection.attributeAllowed(candidate, signals) {
			result = append(result, cloneAttribute(candidate))
		}
	}
	return result, true
}

func (projection *exportProjection) attributeAllowed(candidate attribute.KeyValue, signals []vvotel.SignalDescriptor) bool {
	for _, signal := range signals {
		for _, variant := range signal.Variants {
			for _, spec := range variant.Attributes {
				if spec.Key == candidate.Key && projection.attributeValueAllowed(spec, candidate.Value) {
					return true
				}
			}
		}
	}
	return false
}

func (projection *exportProjection) descriptorAccepts(signal vvotel.SignalDescriptor, status string, attributes []attribute.KeyValue) bool {
	values := make(map[attribute.Key]attribute.Value, len(attributes))
	for _, candidate := range attributes {
		values[candidate.Key] = candidate.Value
	}
	for _, variant := range signal.Variants {
		if variant.Status != "" && variant.Status != status {
			continue
		}
		if projection.variantAccepts(variant, values) {
			return true
		}
	}
	return false
}

func (projection *exportProjection) variantAccepts(variant vvotel.SignalVariantDescriptor, values map[attribute.Key]attribute.Value) bool {
	if len(values) > len(variant.Attributes) {
		return false
	}
	for _, key := range variant.Absent {
		if _, exists := values[key]; exists {
			return false
		}
	}
	for _, spec := range variant.Attributes {
		value, exists := values[spec.Key]
		if !exists {
			if spec.Optional {
				continue
			}
			return false
		}
		if !projection.attributeValueAllowed(spec, value) {
			return false
		}
	}
	for key := range values {
		found := false
		for _, spec := range variant.Attributes {
			found = found || spec.Key == key
		}
		if !found {
			return false
		}
	}
	return true
}

func (projection *exportProjection) attributeValueAllowed(spec vvotel.SignalAttributeDescriptor, value attribute.Value) bool {
	rendered, ok := renderAttributeValue(spec.Type, value)
	if !ok {
		return false
	}
	if spec.Declared {
		_, ok = projection.declared[spec.Key][rendered]
		return ok
	}
	for _, allowed := range spec.Values {
		if rendered == allowed {
			return true
		}
	}
	return false
}

func renderAttributeValue(kind string, value attribute.Value) (string, bool) {
	switch kind {
	case "string":
		if value.Type() != attribute.STRING {
			return "", false
		}
		return value.AsString(), true
	case "bool":
		if value.Type() != attribute.BOOL {
			return "", false
		}
		return strconv.FormatBool(value.AsBool()), true
	case "int64":
		if value.Type() != attribute.INT64 {
			return "", false
		}
		return strconv.FormatInt(value.AsInt64(), 10), true
	case "float64":
		if value.Type() != attribute.FLOAT64 {
			return "", false
		}
		return strconv.FormatFloat(value.AsFloat64(), 'g', -1, 64), true
	default:
		return "", false
	}
}

type projectingMetricExporter struct {
	next       sdkmetric.Exporter
	projection *exportProjection
}

func (exporter *projectingMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return exporter.next.Temporality(kind)
}

func (exporter *projectingMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return exporter.next.Aggregation(kind)
}

func (exporter *projectingMetricExporter) Export(ctx context.Context, input *metricdata.ResourceMetrics) error {
	projected := exporter.projection.projectResourceMetrics(input)
	return exporter.next.Export(ctx, &projected)
}

func (exporter *projectingMetricExporter) ForceFlush(ctx context.Context) error {
	return exporter.next.ForceFlush(ctx)
}

func (exporter *projectingMetricExporter) Shutdown(ctx context.Context) error {
	return exporter.next.Shutdown(ctx)
}

func (projection *exportProjection) projectResourceMetrics(input *metricdata.ResourceMetrics) metricdata.ResourceMetrics {
	result := metricdata.ResourceMetrics{Resource: projection.resource}
	if input == nil {
		return result
	}
	for _, scopeMetrics := range input.ScopeMetrics {
		if scopeMetrics.Scope != projection.scope {
			continue
		}
		projected := metricdata.ScopeMetrics{Scope: projection.scope}
		for _, metrics := range scopeMetrics.Metrics {
			if candidate, ok := projection.projectMetrics(metrics); ok {
				projected.Metrics = append(projected.Metrics, candidate)
			}
		}
		if len(projected.Metrics) > 0 {
			result.ScopeMetrics = append(result.ScopeMetrics, projected)
		}
	}
	return result
}

func (projection *exportProjection) projectMetrics(input metricdata.Metrics) (metricdata.Metrics, bool) {
	for _, descriptor := range projection.metricSignals[input.Name] {
		data, ok := projection.projectAggregation(input.Data, descriptor)
		if ok {
			return metricdata.Metrics{
				Name:        descriptor.Name,
				Description: descriptor.Description,
				Unit:        descriptor.Unit,
				Data:        data,
			}, true
		}
	}
	return metricdata.Metrics{}, false
}

func (projection *exportProjection) projectAggregation(input metricdata.Aggregation, descriptor vvotel.SignalDescriptor) (metricdata.Aggregation, bool) {
	switch data := input.(type) {
	case metricdata.Gauge[int64]:
		points := projectDataPoints(projection, data.DataPoints, descriptor)
		return metricdata.Gauge[int64]{DataPoints: points}, len(points) > 0
	case metricdata.Gauge[float64]:
		points := projectDataPoints(projection, data.DataPoints, descriptor)
		return metricdata.Gauge[float64]{DataPoints: points}, len(points) > 0
	case metricdata.Sum[int64]:
		points := projectDataPoints(projection, data.DataPoints, descriptor)
		return metricdata.Sum[int64]{DataPoints: points, Temporality: data.Temporality, IsMonotonic: data.IsMonotonic}, len(points) > 0
	case metricdata.Sum[float64]:
		points := projectDataPoints(projection, data.DataPoints, descriptor)
		return metricdata.Sum[float64]{DataPoints: points, Temporality: data.Temporality, IsMonotonic: data.IsMonotonic}, len(points) > 0
	case metricdata.Histogram[int64]:
		points := projectHistogramPoints(projection, data.DataPoints, descriptor)
		return metricdata.Histogram[int64]{DataPoints: points, Temporality: data.Temporality}, len(points) > 0
	case metricdata.Histogram[float64]:
		points := projectHistogramPoints(projection, data.DataPoints, descriptor)
		return metricdata.Histogram[float64]{DataPoints: points, Temporality: data.Temporality}, len(points) > 0
	case metricdata.ExponentialHistogram[int64]:
		points := projectExponentialHistogramPoints(projection, data.DataPoints, descriptor)
		return metricdata.ExponentialHistogram[int64]{DataPoints: points, Temporality: data.Temporality}, len(points) > 0
	case metricdata.ExponentialHistogram[float64]:
		points := projectExponentialHistogramPoints(projection, data.DataPoints, descriptor)
		return metricdata.ExponentialHistogram[float64]{DataPoints: points, Temporality: data.Temporality}, len(points) > 0
	default:
		return nil, false
	}
}

type metricNumber interface {
	int64 | float64
}

func projectDataPoints[N metricNumber](projection *exportProjection, input []metricdata.DataPoint[N], descriptor vvotel.SignalDescriptor) []metricdata.DataPoint[N] {
	result := make([]metricdata.DataPoint[N], 0, len(input))
	for _, point := range input {
		attributes, ok := projection.projectMetricAttributes(point.Attributes, descriptor)
		if !ok || !metricValueAllowed(point.Value, descriptor) {
			continue
		}
		point.Attributes = attribute.NewSet(attributes...)
		point.Exemplars = projectExemplars(projection, point.Exemplars, descriptor)
		result = append(result, point)
	}
	return result
}

func projectHistogramPoints[N metricNumber](projection *exportProjection, input []metricdata.HistogramDataPoint[N], descriptor vvotel.SignalDescriptor) []metricdata.HistogramDataPoint[N] {
	result := make([]metricdata.HistogramDataPoint[N], 0, len(input))
	for _, point := range input {
		attributes, ok := projection.projectMetricAttributes(point.Attributes, descriptor)
		if !ok {
			continue
		}
		point.Attributes = attribute.NewSet(attributes...)
		point.Bounds = append([]float64(nil), point.Bounds...)
		point.BucketCounts = append([]uint64(nil), point.BucketCounts...)
		point.Exemplars = projectExemplars(projection, point.Exemplars, descriptor)
		result = append(result, point)
	}
	return result
}

func projectExponentialHistogramPoints[N metricNumber](projection *exportProjection, input []metricdata.ExponentialHistogramDataPoint[N], descriptor vvotel.SignalDescriptor) []metricdata.ExponentialHistogramDataPoint[N] {
	result := make([]metricdata.ExponentialHistogramDataPoint[N], 0, len(input))
	for _, point := range input {
		attributes, ok := projection.projectMetricAttributes(point.Attributes, descriptor)
		if !ok {
			continue
		}
		point.Attributes = attribute.NewSet(attributes...)
		point.PositiveBucket.Counts = append([]uint64(nil), point.PositiveBucket.Counts...)
		point.NegativeBucket.Counts = append([]uint64(nil), point.NegativeBucket.Counts...)
		point.Exemplars = projectExemplars(projection, point.Exemplars, descriptor)
		result = append(result, point)
	}
	return result
}

func (projection *exportProjection) projectMetricAttributes(input attribute.Set, descriptor vvotel.SignalDescriptor) ([]attribute.KeyValue, bool) {
	attributes, ok := projection.filterKnownAttributes(input.ToSlice(), []vvotel.SignalDescriptor{descriptor})
	if !ok || !projection.descriptorAccepts(descriptor, "", attributes) {
		return nil, false
	}
	return attributes, true
}

func metricValueAllowed[N metricNumber](value N, descriptor vvotel.SignalDescriptor) bool {
	switch typed := any(value).(type) {
	case int64:
		return descriptor.NumberType == "int64" && descriptor.AcceptsInt64(typed)
	case float64:
		return descriptor.NumberType == "float64" && descriptor.AcceptsFloat64(typed)
	default:
		return false
	}
}

func projectExemplars[N metricNumber](projection *exportProjection, input []metricdata.Exemplar[N], descriptor vvotel.SignalDescriptor) []metricdata.Exemplar[N] {
	result := make([]metricdata.Exemplar[N], len(input))
	for index, exemplar := range input {
		attributes, _ := projection.filterKnownAttributes(exemplar.FilteredAttributes, []vvotel.SignalDescriptor{descriptor})
		exemplar.FilteredAttributes = attributes
		exemplar.TraceID = append([]byte(nil), exemplar.TraceID...)
		exemplar.SpanID = append([]byte(nil), exemplar.SpanID...)
		result[index] = exemplar
	}
	return result
}

func cloneEvents(input []sdktrace.Event) []sdktrace.Event {
	result := make([]sdktrace.Event, len(input))
	for index, event := range input {
		event.Attributes = cloneAttributes(event.Attributes)
		result[index] = event
	}
	return result
}

func cloneLinks(input []sdktrace.Link) []sdktrace.Link {
	result := make([]sdktrace.Link, len(input))
	for index, link := range input {
		link.Attributes = cloneAttributes(link.Attributes)
		result[index] = link
	}
	return result
}

func cloneAttributes(input []attribute.KeyValue) []attribute.KeyValue {
	result := make([]attribute.KeyValue, len(input))
	for index, candidate := range input {
		result[index] = cloneAttribute(candidate)
	}
	return result
}

func cloneAttribute(input attribute.KeyValue) attribute.KeyValue {
	result := input
	switch input.Value.Type() {
	case attribute.BOOLSLICE:
		result.Value = attribute.BoolSliceValue(input.Value.AsBoolSlice())
	case attribute.INT64SLICE:
		result.Value = attribute.Int64SliceValue(input.Value.AsInt64Slice())
	case attribute.FLOAT64SLICE:
		result.Value = attribute.Float64SliceValue(input.Value.AsFloat64Slice())
	case attribute.STRINGSLICE:
		result.Value = attribute.StringSliceValue(input.Value.AsStringSlice())
	}
	return result
}
