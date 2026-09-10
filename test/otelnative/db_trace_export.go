package otelnative

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var ErrInvalidDatabaseSpanExporter = errors.New("otelnative: downstream database span exporter is required")

type DatabaseProjectionPolicy struct {
	PoolNames          []DatabasePoolName
	ResourceAttributes []attribute.KeyValue
}

type databaseProjectionPolicy struct {
	pools     map[string]struct{}
	resources map[string]attribute.Value
}

func NewDatabaseSpanExporter(next sdktrace.SpanExporter, policy DatabaseProjectionPolicy) (sdktrace.SpanExporter, error) {
	if nilInterface(next) {
		return nil, ErrInvalidDatabaseSpanExporter
	}
	return &databaseSpanExporter{next: next, policy: compileDatabaseProjectionPolicy(policy)}, nil
}

func compileDatabaseProjectionPolicy(policy DatabaseProjectionPolicy) databaseProjectionPolicy {
	compiled := databaseProjectionPolicy{
		pools:     make(map[string]struct{}, len(policy.PoolNames)),
		resources: make(map[string]attribute.Value, len(policy.ResourceAttributes)),
	}
	for _, pool := range policy.PoolNames {
		if validDatabasePoolName(pool.value) {
			compiled.pools[pool.value] = struct{}{}
		}
	}
	for _, item := range policy.ResourceAttributes {
		compiled.resources[string(item.Key)] = item.Value
	}
	return compiled
}

type databaseSpanExporter struct {
	next   sdktrace.SpanExporter
	policy databaseProjectionPolicy
}

func (e *databaseSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	projected := make([]sdktrace.ReadOnlySpan, len(spans))
	for index, span := range spans {
		if _, known := databaseScope(span.InstrumentationScope()); !known {
			projected[index] = span
			continue
		}
		projected[index] = projectDatabaseSpan(span, e.policy)
	}
	return e.next.ExportSpans(ctx, projected)
}

func (e *databaseSpanExporter) Shutdown(ctx context.Context) error {
	return e.next.Shutdown(ctx)
}

type databaseProjectedSpan struct {
	sdktrace.ReadOnlySpan
	name              string
	spanContext       trace.SpanContext
	parent            trace.SpanContext
	attributes        []attribute.KeyValue
	links             []sdktrace.Link
	status            sdktrace.Status
	scope             instrumentation.Scope
	resource          *resource.Resource
	droppedAttributes int
	droppedLinks      int
	droppedEvents     int
}

func projectDatabaseSpan(span sdktrace.ReadOnlySpan, policy databaseProjectionPolicy) databaseProjectedSpan {
	attributes := projectDatabaseTraceAttributes(span.InstrumentationScope().Name, span.Attributes())
	links := make([]sdktrace.Link, 0, len(span.Links()))
	for _, link := range span.Links() {
		links = append(links, sdktrace.Link{
			SpanContext:           sanitizeSpanContext(link.SpanContext),
			DroppedAttributeCount: link.DroppedAttributeCount + len(link.Attributes),
		})
	}
	scope, _ := databaseScope(span.InstrumentationScope())
	return databaseProjectedSpan{
		ReadOnlySpan:      span,
		name:              normalizeDatabaseExportName(span.InstrumentationScope().Name, span.Name()),
		spanContext:       sanitizeSpanContext(span.SpanContext()),
		parent:            sanitizeSpanContext(span.Parent()),
		attributes:        attributes,
		links:             links,
		status:            sdktrace.Status{Code: span.Status().Code},
		scope:             scope,
		resource:          projectResource(span.Resource(), policy.resources),
		droppedAttributes: span.DroppedAttributes() + len(span.Attributes()) - len(attributes),
		droppedLinks:      span.DroppedLinks(),
		droppedEvents:     span.DroppedEvents() + len(span.Events()),
	}
}

func (s databaseProjectedSpan) Name() string {
	return s.name
}

func (s databaseProjectedSpan) SpanContext() trace.SpanContext {
	return s.spanContext
}

func (s databaseProjectedSpan) Parent() trace.SpanContext {
	return s.parent
}

func (s databaseProjectedSpan) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue(nil), s.attributes...)
}

func (s databaseProjectedSpan) Links() []sdktrace.Link {
	return append([]sdktrace.Link(nil), s.links...)
}

func (databaseProjectedSpan) Events() []sdktrace.Event {
	return nil
}

func (s databaseProjectedSpan) Status() sdktrace.Status {
	return s.status
}

func (s databaseProjectedSpan) InstrumentationScope() instrumentation.Scope {
	return s.scope
}

func (s databaseProjectedSpan) InstrumentationLibrary() instrumentation.Library {
	return s.scope
}

func (s databaseProjectedSpan) Resource() *resource.Resource {
	return s.resource
}

func (s databaseProjectedSpan) DroppedAttributes() int {
	return s.droppedAttributes
}

func (s databaseProjectedSpan) DroppedLinks() int {
	return s.droppedLinks
}

func (s databaseProjectedSpan) DroppedEvents() int {
	return s.droppedEvents
}

func normalizeDatabaseExportName(scope, name string) string {
	if scope == pgxScopeName {
		return normalizePGXSpanName(name)
	}
	switch name {
	case "db.connect", "db.ping", "db.exec", "db.query", "db.prepare", "db.begin", "db.reset", "db.commit", "db.rollback", "db.rows":
		return name
	default:
		return databaseOperation
	}
}

func projectDatabaseTraceAttributes(scope string, attributes []attribute.KeyValue) []attribute.KeyValue {
	if scope != pgxScopeName {
		return nil
	}
	projected := make([]attribute.KeyValue, 0, len(attributes))
	for _, item := range attributes {
		switch string(item.Key) {
		case "db.system.name":
			if item.Value.Type() == attribute.STRING && item.Value.AsString() == "postgresql" {
				projected = append(projected, item)
			}
		case "db.operation.name":
			if item.Value.Type() == attribute.STRING && item.Value.AsString() == "statement" {
				projected = append(projected, item)
			}
		case "db.operation.batch.size", "pgx.rows_affected":
			if item.Value.Type() == attribute.INT64 {
				projected = append(projected, item)
			}
		}
	}
	return projected
}
