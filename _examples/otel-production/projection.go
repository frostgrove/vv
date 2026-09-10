package main

import (
	"context"
	"errors"
	"fmt"
	"math"
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

func frostgroveInstrumentationScope() instrumentation.Scope {
	return instrumentation.Scope{
		Name:    vvotel.ScopeName,
		Version: vvotel.ScopeVersion,
	}
}

func instrumentationScopesEqual(left instrumentation.Scope, right instrumentation.Scope) bool {
	return left.Name == right.Name && left.Version == right.Version && left.SchemaURL == right.SchemaURL && left.Attributes.Equals(&right.Attributes)
}

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
		scope:         frostgroveInstrumentationScope(),
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

func composeTraceExporter(next sdktrace.SpanExporter, layers []TraceProjectionLayer, projection *exportProjection) (sdktrace.SpanExporter, error) {
	scopes := []instrumentation.Scope{projection.scope}
	for _, layer := range layers {
		scopes = append(scopes, layer.Scopes...)
	}
	current := sdktrace.SpanExporter(&scopeGateSpanExporter{next: next, scopes: scopes})
	for index := len(layers) - 1; index >= 0; index-- {
		ownerOutput := &scopeGateSpanExporter{next: current, scopes: layers[index].Scopes}
		wrapped, err := callTraceProjectionWrap(layers[index].Wrap, ownerOutput)
		if err != nil {
			if !nilInterfaceValue(wrapped) {
				return wrapped, fmt.Errorf("trace projection layer %d: %w", index, err)
			}
			return current, fmt.Errorf("trace projection layer %d: %w", index, err)
		}
		if nilInterfaceValue(wrapped) {
			return current, ErrInvalidTelemetryConfig
		}
		current = &scopedSpanProjectionExporter{
			next:       current,
			projection: wrapped,
			scopes:     layers[index].Scopes,
		}
	}
	return &projectingSpanExporter{next: current, projection: projection}, nil
}

func composeMetricExporter(next sdkmetric.Exporter, layers []MetricProjectionLayer, projection *exportProjection) (sdkmetric.Exporter, error) {
	scopes := []instrumentation.Scope{projection.scope}
	for _, layer := range layers {
		scopes = append(scopes, layer.Scopes...)
	}
	current := sdkmetric.Exporter(&scopeGateMetricExporter{next: next, scopes: scopes})
	for index := len(layers) - 1; index >= 0; index-- {
		ownerOutput := &scopeGateMetricExporter{next: current, scopes: layers[index].Scopes}
		wrapped, err := callMetricProjectionWrap(layers[index].Wrap, ownerOutput)
		if err != nil {
			if !nilInterfaceValue(wrapped) {
				return wrapped, fmt.Errorf("metric projection layer %d: %w", index, err)
			}
			return current, fmt.Errorf("metric projection layer %d: %w", index, err)
		}
		if nilInterfaceValue(wrapped) {
			return current, ErrInvalidTelemetryConfig
		}
		current = &scopedMetricProjectionExporter{
			next:       current,
			projection: wrapped,
			scopes:     layers[index].Scopes,
		}
	}
	return &projectingMetricExporter{next: current, projection: projection}, nil
}

func callTraceProjectionWrap(wrap func(sdktrace.SpanExporter) (sdktrace.SpanExporter, error), next sdktrace.SpanExporter) (result sdktrace.SpanExporter, err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			result = nil
			err = ErrTelemetryAssembly
		}
	}()
	result, err = wrap(next)
	completed = true
	return result, err
}

func callMetricProjectionWrap(wrap func(sdkmetric.Exporter) (sdkmetric.Exporter, error), next sdkmetric.Exporter) (result sdkmetric.Exporter, err error) {
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			result = nil
			err = ErrTelemetryAssembly
		}
	}()
	result, err = wrap(next)
	completed = true
	return result, err
}

type scopedSpanProjectionExporter struct {
	next       sdktrace.SpanExporter
	projection sdktrace.SpanExporter
	scopes     []instrumentation.Scope
}

func (exporter *scopedSpanProjectionExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	matching := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	siblings := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, span := range spans {
		if span != nil && containsInstrumentationScope(exporter.scopes, span.InstrumentationScope()) {
			matching = append(matching, span)
		} else {
			siblings = append(siblings, span)
		}
	}
	var projectionErr error
	if len(matching) > 0 {
		projectionErr = exporter.projection.ExportSpans(ctx, matching)
	}
	var siblingErr error
	if len(siblings) > 0 {
		siblingErr = exporter.next.ExportSpans(ctx, siblings)
	}
	return errors.Join(projectionErr, siblingErr)
}

func (exporter *scopedSpanProjectionExporter) Shutdown(ctx context.Context) error {
	return exporter.projection.Shutdown(ctx)
}

type scopedMetricProjectionExporter struct {
	next       sdkmetric.Exporter
	projection sdkmetric.Exporter
	scopes     []instrumentation.Scope
}

func (exporter *scopedMetricProjectionExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return exporter.next.Temporality(kind)
}

func (exporter *scopedMetricProjectionExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return exporter.next.Aggregation(kind)
}

func (exporter *scopedMetricProjectionExporter) Export(ctx context.Context, input *metricdata.ResourceMetrics) error {
	if input == nil {
		return nil
	}
	matching := metricdata.ResourceMetrics{Resource: input.Resource}
	siblings := metricdata.ResourceMetrics{Resource: input.Resource}
	for _, scopeMetrics := range input.ScopeMetrics {
		if containsInstrumentationScope(exporter.scopes, scopeMetrics.Scope) {
			matching.ScopeMetrics = append(matching.ScopeMetrics, scopeMetrics)
		} else {
			siblings.ScopeMetrics = append(siblings.ScopeMetrics, scopeMetrics)
		}
	}
	var projectionErr error
	if len(matching.ScopeMetrics) > 0 {
		projectionErr = exporter.projection.Export(ctx, &matching)
	}
	var siblingErr error
	if len(siblings.ScopeMetrics) > 0 {
		siblingErr = exporter.next.Export(ctx, &siblings)
	}
	return errors.Join(projectionErr, siblingErr)
}

func (exporter *scopedMetricProjectionExporter) ForceFlush(ctx context.Context) error {
	return exporter.projection.ForceFlush(ctx)
}

func (exporter *scopedMetricProjectionExporter) Shutdown(ctx context.Context) error {
	return exporter.projection.Shutdown(ctx)
}

type scopeGateSpanExporter struct {
	next   sdktrace.SpanExporter
	scopes []instrumentation.Scope
}

func (exporter *scopeGateSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	allowed := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, span := range spans {
		if span != nil && containsInstrumentationScope(exporter.scopes, span.InstrumentationScope()) {
			allowed = append(allowed, span)
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	return exporter.next.ExportSpans(ctx, allowed)
}

func (exporter *scopeGateSpanExporter) Shutdown(ctx context.Context) error {
	return exporter.next.Shutdown(ctx)
}

type scopeGateMetricExporter struct {
	next   sdkmetric.Exporter
	scopes []instrumentation.Scope
}

func (exporter *scopeGateMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return exporter.next.Temporality(kind)
}

func (exporter *scopeGateMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return exporter.next.Aggregation(kind)
}

func (exporter *scopeGateMetricExporter) Export(ctx context.Context, input *metricdata.ResourceMetrics) error {
	if input == nil {
		return nil
	}
	filtered := metricdata.ResourceMetrics{Resource: input.Resource}
	for _, scopeMetrics := range input.ScopeMetrics {
		if containsInstrumentationScope(exporter.scopes, scopeMetrics.Scope) {
			filtered.ScopeMetrics = append(filtered.ScopeMetrics, scopeMetrics)
		}
	}
	if len(filtered.ScopeMetrics) == 0 {
		return nil
	}
	return exporter.next.Export(ctx, &filtered)
}

func (exporter *scopeGateMetricExporter) ForceFlush(ctx context.Context) error {
	return exporter.next.ForceFlush(ctx)
}

func (exporter *scopeGateMetricExporter) Shutdown(ctx context.Context) error {
	return exporter.next.Shutdown(ctx)
}

type projectingSpanExporter struct {
	next       sdktrace.SpanExporter
	projection *exportProjection
}

func (exporter *projectingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	projected := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, span := range spans {
		if span == nil {
			continue
		}
		if !instrumentationScopesEqual(span.InstrumentationScope(), exporter.projection.scope) {
			projected = append(projected, span)
			continue
		}
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
	spanKind   trace.SpanKind
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

func (span *projectedReadOnlySpan) SpanKind() trace.SpanKind { return span.spanKind }

func (projection *exportProjection) projectSpan(span sdktrace.ReadOnlySpan) (sdktrace.ReadOnlySpan, bool) {
	if projection == nil || span == nil || !instrumentationScopesEqual(span.InstrumentationScope(), projection.scope) {
		return nil, false
	}
	signals := projection.spanSignals[span.Name()]
	status := spanStatusName(span.Status().Code)
	attributes, ok := projection.projectSpanAttributes(span.Attributes(), signals, status, span.SpanKind())
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
		spanKind:     span.SpanKind(),
	}, true
}

func (projection *exportProjection) projectSpanAttributes(input []attribute.KeyValue, signals []vvotel.SignalDescriptor, status string, kind trace.SpanKind) ([]attribute.KeyValue, bool) {
	if len(signals) == 0 {
		return nil, false
	}
	attributes, ok := projection.filterKnownAttributes(input, signals)
	if !ok {
		return nil, false
	}
	for _, signal := range signals {
		if signal.SpanKind == spanKindName(kind) && projection.descriptorAccepts(signal, status, attributes) {
			return attributes, true
		}
	}
	return nil, false
}

func spanKindName(kind trace.SpanKind) string {
	switch kind {
	case trace.SpanKindUnspecified:
		return "unspecified"
	case trace.SpanKindInternal:
		return "internal"
	case trace.SpanKindServer:
		return "server"
	case trace.SpanKindClient:
		return "client"
	case trace.SpanKindProducer:
		return "producer"
	case trace.SpanKindConsumer:
		return "consumer"
	default:
		return ""
	}
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
	var exportErrors []error
	for index := range projected {
		exportErrors = append(exportErrors, exporter.next.Export(ctx, &projected[index]))
	}
	return errors.Join(exportErrors...)
}

func (exporter *projectingMetricExporter) ForceFlush(ctx context.Context) error {
	return exporter.next.ForceFlush(ctx)
}

func (exporter *projectingMetricExporter) Shutdown(ctx context.Context) error {
	return exporter.next.Shutdown(ctx)
}

func (projection *exportProjection) projectResourceMetrics(input *metricdata.ResourceMetrics) []metricdata.ResourceMetrics {
	if input == nil {
		return nil
	}
	passthrough := metricdata.ResourceMetrics{Resource: input.Resource}
	framework := metricdata.ResourceMetrics{Resource: projection.resource}
	for _, scopeMetrics := range input.ScopeMetrics {
		if !instrumentationScopesEqual(scopeMetrics.Scope, projection.scope) {
			passthrough.ScopeMetrics = append(passthrough.ScopeMetrics, scopeMetrics)
			continue
		}
		projected := metricdata.ScopeMetrics{Scope: projection.scope}
		for _, metrics := range scopeMetrics.Metrics {
			if candidate, ok := projection.projectMetrics(metrics); ok {
				projected.Metrics = append(projected.Metrics, candidate)
			}
		}
		if len(projected.Metrics) > 0 {
			framework.ScopeMetrics = append(framework.ScopeMetrics, projected)
		}
	}
	result := make([]metricdata.ResourceMetrics, 0, 2)
	if len(passthrough.ScopeMetrics) > 0 {
		result = append(result, passthrough)
	}
	if len(framework.ScopeMetrics) > 0 {
		result = append(result, framework)
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
	switch descriptor.APIKind {
	case "int64_observable_gauge":
		data, ok := input.(metricdata.Gauge[int64])
		if !ok {
			return nil, false
		}
		points := projectDataPoints(projection, data.DataPoints, descriptor)
		return metricdata.Gauge[int64]{DataPoints: points}, validProjectedPointCount(points, descriptor)
	case "int64_counter":
		data, ok := input.(metricdata.Sum[int64])
		if !ok || !data.IsMonotonic || !validMetricTemporality(data.Temporality) {
			return nil, false
		}
		points := projectSumDataPoints(projection, data.DataPoints, descriptor)
		return metricdata.Sum[int64]{DataPoints: points, Temporality: data.Temporality, IsMonotonic: data.IsMonotonic}, validProjectedPointCount(points, descriptor)
	case "int64_histogram":
		data, ok := input.(metricdata.Histogram[int64])
		if !ok || !validMetricTemporality(data.Temporality) {
			return nil, false
		}
		points := projectHistogramPoints(projection, data.DataPoints, descriptor)
		return metricdata.Histogram[int64]{DataPoints: points, Temporality: data.Temporality}, validProjectedPointCount(points, descriptor)
	case "float64_histogram":
		data, ok := input.(metricdata.Histogram[float64])
		if !ok || !validMetricTemporality(data.Temporality) {
			return nil, false
		}
		points := projectHistogramPoints(projection, data.DataPoints, descriptor)
		return metricdata.Histogram[float64]{DataPoints: points, Temporality: data.Temporality}, validProjectedPointCount(points, descriptor)
	default:
		return nil, false
	}
}

func validProjectedPointCount[T any](points []T, descriptor vvotel.SignalDescriptor) bool {
	return len(points) > 0 && descriptor.SeriesBudget > 0 && len(points) <= descriptor.SeriesBudget
}

func validMetricTemporality(temporality metricdata.Temporality) bool {
	return temporality == metricdata.CumulativeTemporality
}

type metricNumber interface {
	int64 | float64
}

func projectDataPoints[N metricNumber](projection *exportProjection, input []metricdata.DataPoint[N], descriptor vvotel.SignalDescriptor) []metricdata.DataPoint[N] {
	if !validInputPointCount(input, descriptor) {
		return nil
	}
	result := make([]metricdata.DataPoint[N], 0, len(input))
	seen := make(map[attribute.Distinct]struct{}, len(input))
	for _, point := range input {
		attributes, ok := projection.projectMetricAttributes(point.Attributes, descriptor)
		if !ok || !metricValueAllowed(point.Value, descriptor) {
			continue
		}
		point.Attributes = attribute.NewSet(attributes...)
		identity := point.Attributes.Equivalent()
		if _, duplicate := seen[identity]; duplicate {
			return nil
		}
		seen[identity] = struct{}{}
		point.Exemplars = projectExemplars(projection, point.Exemplars, descriptor)
		result = append(result, point)
	}
	return result
}

func projectSumDataPoints(projection *exportProjection, input []metricdata.DataPoint[int64], descriptor vvotel.SignalDescriptor) []metricdata.DataPoint[int64] {
	if !validInputPointCount(input, descriptor) {
		return nil
	}
	result := make([]metricdata.DataPoint[int64], 0, len(input))
	seen := make(map[attribute.Distinct]struct{}, len(input))
	for _, point := range input {
		attributes, ok := projection.projectMetricAttributes(point.Attributes, descriptor)
		if !ok || point.Value < 0 || descriptor.NumberType != "int64" {
			continue
		}
		point.Attributes = attribute.NewSet(attributes...)
		identity := point.Attributes.Equivalent()
		if _, duplicate := seen[identity]; duplicate {
			return nil
		}
		seen[identity] = struct{}{}
		point.Exemplars = projectExemplars(projection, point.Exemplars, descriptor)
		result = append(result, point)
	}
	return result
}

func projectHistogramPoints[N metricNumber](projection *exportProjection, input []metricdata.HistogramDataPoint[N], descriptor vvotel.SignalDescriptor) []metricdata.HistogramDataPoint[N] {
	if !validInputPointCount(input, descriptor) {
		return nil
	}
	result := make([]metricdata.HistogramDataPoint[N], 0, len(input))
	seen := make(map[attribute.Distinct]struct{}, len(input))
	for _, point := range input {
		attributes, ok := projection.projectMetricAttributes(point.Attributes, descriptor)
		if !ok || !validHistogramPoint(point, descriptor) {
			continue
		}
		point.Attributes = attribute.NewSet(attributes...)
		identity := point.Attributes.Equivalent()
		if _, duplicate := seen[identity]; duplicate {
			return nil
		}
		seen[identity] = struct{}{}
		point.Bounds = append([]float64(nil), point.Bounds...)
		point.BucketCounts = append([]uint64(nil), point.BucketCounts...)
		point.Exemplars = projectExemplars(projection, point.Exemplars, descriptor)
		result = append(result, point)
	}
	return result
}

func validInputPointCount[T any](input []T, descriptor vvotel.SignalDescriptor) bool {
	return len(input) > 0 && descriptor.SeriesBudget > 0 && len(input) <= descriptor.SeriesBudget
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

func validHistogramPoint[N metricNumber](point metricdata.HistogramDataPoint[N], descriptor vvotel.SignalDescriptor) bool {
	var zero N
	if len(point.BucketCounts) != len(point.Bounds)+1 || len(point.BucketCounts) > 4096 || !validMetricNumber(point.Sum) || point.Sum < zero {
		return false
	}
	if point.Count == 0 && point.Sum != zero {
		return false
	}
	for index, bound := range point.Bounds {
		if math.IsNaN(bound) || math.IsInf(bound, 0) || index > 0 && bound <= point.Bounds[index-1] {
			return false
		}
	}
	if !histogramCountMatches(point.Count, point.BucketCounts, 0) || !validMetricExtrema(point.Min, point.Max, point.Count, descriptor) {
		return false
	}
	return true
}

func histogramCountMatches(count uint64, buckets []uint64, initial uint64) bool {
	if initial > count {
		return false
	}
	total := initial
	for _, value := range buckets {
		if value > math.MaxUint64-total {
			return false
		}
		total += value
	}
	return total == count
}

func validMetricExtrema[N metricNumber](minimum, maximum metricdata.Extrema[N], count uint64, descriptor vvotel.SignalDescriptor) bool {
	minValue, hasMinimum := minimum.Value()
	maxValue, hasMaximum := maximum.Value()
	if count == 0 && (hasMinimum || hasMaximum) || hasMinimum && !metricValueAllowed(minValue, descriptor) || hasMaximum && !metricValueAllowed(maxValue, descriptor) {
		return false
	}
	return !hasMinimum || !hasMaximum || !metricNumberLess(maxValue, minValue)
}

func validMetricNumber[N metricNumber](value N) bool {
	if number, ok := any(value).(float64); ok {
		return !math.IsNaN(number) && !math.IsInf(number, 0)
	}
	return true
}

func metricNumberLess[N metricNumber](left, right N) bool {
	return left < right
}

func projectExemplars[N metricNumber](projection *exportProjection, input []metricdata.Exemplar[N], descriptor vvotel.SignalDescriptor) []metricdata.Exemplar[N] {
	result := make([]metricdata.Exemplar[N], 0, len(input))
	for _, exemplar := range input {
		attributes, ok := projection.filterKnownAttributes(exemplar.FilteredAttributes, []vvotel.SignalDescriptor{descriptor})
		if !ok || !metricValueAllowed(exemplar.Value, descriptor) || !validExemplarIDs(exemplar.TraceID, exemplar.SpanID) {
			continue
		}
		exemplar.FilteredAttributes = attributes
		exemplar.TraceID = append([]byte(nil), exemplar.TraceID...)
		exemplar.SpanID = append([]byte(nil), exemplar.SpanID...)
		result = append(result, exemplar)
	}
	return result
}

func validExemplarIDs(traceID, spanID []byte) bool {
	if len(traceID) == 0 && len(spanID) == 0 {
		return true
	}
	if len(traceID) != 16 || len(spanID) != 8 {
		return false
	}
	var traceIdentifier trace.TraceID
	var spanIdentifier trace.SpanID
	copy(traceIdentifier[:], traceID)
	copy(spanIdentifier[:], spanID)
	return traceIdentifier.IsValid() && spanIdentifier.IsValid()
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
	return attribute.KeyValue{Key: input.Key, Value: cloneAttributeValue(input.Value)}
}

func cloneAttributeValue(input attribute.Value) attribute.Value {
	switch input.Type() {
	case attribute.BOOLSLICE:
		return attribute.BoolSliceValue(input.AsBoolSlice())
	case attribute.INT64SLICE:
		return attribute.Int64SliceValue(input.AsInt64Slice())
	case attribute.FLOAT64SLICE:
		return attribute.Float64SliceValue(input.AsFloat64Slice())
	case attribute.STRINGSLICE:
		return attribute.StringSliceValue(input.AsStringSlice())
	case attribute.BYTESLICE:
		return attribute.ByteSliceValue(input.AsByteSlice())
	case attribute.SLICE:
		values := input.AsSlice()
		for index := range values {
			values[index] = cloneAttributeValue(values[index])
		}
		return attribute.SliceValue(values...)
	default:
		return input
	}
}
