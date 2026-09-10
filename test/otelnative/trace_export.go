package otelnative

import (
	"context"
	"errors"
	"strings"

	"go.opentelemetry.io/contrib"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var ErrInvalidSpanExporter = errors.New("otelnative: downstream span exporter is required")

type TraceProjectionPolicy struct {
	HTTPRoutes         *HTTPRoutes
	RouterTables       []RouteTable
	RPCMethods         RPCTable
	ResourceAttributes []attribute.KeyValue
}

func NewTransportSpanExporter(next sdktrace.SpanExporter, policy TraceProjectionPolicy) (sdktrace.SpanExporter, error) {
	if nilInterface(next) {
		return nil, ErrInvalidSpanExporter
	}
	compiled, err := compileTracePolicy(policy)
	if err != nil {
		return nil, err
	}
	return &projectingSpanExporter{next: next, policy: compiled}, nil
}

func compileTracePolicy(policy TraceProjectionPolicy) (compiledTracePolicy, error) {
	if len(policy.RouterTables) > maxNativeProjectionTables {
		return compiledTracePolicy{}, ErrInvalidProjectionPolicy
	}
	resources, err := compileNativeResources(policy.ResourceAttributes)
	if err != nil {
		return compiledTracePolicy{}, err
	}
	compiled := compiledTracePolicy{
		httpNames:  map[string]struct{}{fallbackHTTPName: {}},
		httpRoutes: make(map[string]struct{}),
		rpcNames:   map[string]struct{}{fallbackRPCName: {}},
		resources:  resources,
	}
	routeCount := 0
	if policy.HTTPRoutes != nil {
		policy.HTTPRoutes.mu.Lock()
		if len(policy.HTTPRoutes.entries) > maxNativeRoutes {
			policy.HTTPRoutes.mu.Unlock()
			return compiledTracePolicy{}, ErrInvalidProjectionPolicy
		}
		routeCount = len(policy.HTTPRoutes.entries)
		for pattern := range policy.HTTPRoutes.entries {
			compiled.httpNames[pattern] = struct{}{}
			compiled.httpRoutes[pattern] = struct{}{}
			if slash := strings.IndexByte(pattern, '/'); slash >= 0 {
				compiled.httpRoutes[pattern[slash:]] = struct{}{}
			}
		}
		policy.HTTPRoutes.mu.Unlock()
	}
	for _, table := range policy.RouterTables {
		if len(table.names) > maxNativeRoutes-routeCount {
			return compiledTracePolicy{}, ErrInvalidProjectionPolicy
		}
		routeCount += len(table.names)
		for _, name := range table.names {
			compiled.httpNames[name] = struct{}{}
		}
		for pattern := range table.patterns {
			compiled.httpRoutes[pattern] = struct{}{}
		}
	}
	for _, name := range policy.RPCMethods.methods {
		compiled.rpcNames[name] = struct{}{}
	}
	return compiled, nil
}

type compiledTracePolicy struct {
	httpNames  map[string]struct{}
	httpRoutes map[string]struct{}
	rpcNames   map[string]struct{}
	resources  map[string]attribute.Value
}

type projectingSpanExporter struct {
	next   sdktrace.SpanExporter
	policy compiledTracePolicy
}

func (e *projectingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	projected := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, span := range spans {
		scope, known := nativeScope(span.InstrumentationScope())
		if !known {
			projected = append(projected, span)
			continue
		}
		if scope.Name != "" {
			projected = append(projected, newProjectedSpan(span, e.policy))
		}
	}
	return e.next.ExportSpans(ctx, projected)
}

func (e *projectingSpanExporter) Shutdown(ctx context.Context) error {
	return e.next.Shutdown(ctx)
}

type projectedSpan struct {
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

func newProjectedSpan(span sdktrace.ReadOnlySpan, policy compiledTracePolicy) projectedSpan {
	scope, _ := nativeScope(span.InstrumentationScope())
	attributes := projectTraceAttributes(span.InstrumentationScope().Name, span.Attributes(), policy)
	links := make([]sdktrace.Link, 0, len(span.Links()))
	for _, link := range span.Links() {
		links = append(links, sdktrace.Link{
			SpanContext:           sanitizeSpanContext(link.SpanContext),
			DroppedAttributeCount: link.DroppedAttributeCount + len(link.Attributes),
		})
	}
	return projectedSpan{
		ReadOnlySpan:      span,
		name:              projectSpanName(span, policy),
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

func (s projectedSpan) Name() string {
	return s.name
}

func (s projectedSpan) SpanContext() trace.SpanContext {
	return s.spanContext
}

func (s projectedSpan) Parent() trace.SpanContext {
	return s.parent
}

func (s projectedSpan) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue(nil), s.attributes...)
}

func (s projectedSpan) Links() []sdktrace.Link {
	return append([]sdktrace.Link(nil), s.links...)
}

func (projectedSpan) Events() []sdktrace.Event {
	return nil
}

func (s projectedSpan) Status() sdktrace.Status {
	return s.status
}

func (s projectedSpan) InstrumentationScope() instrumentation.Scope {
	return s.scope
}

func (s projectedSpan) InstrumentationLibrary() instrumentation.Library {
	return s.scope
}

func (s projectedSpan) Resource() *resource.Resource {
	return s.resource
}

func (s projectedSpan) DroppedAttributes() int {
	return s.droppedAttributes
}

func (s projectedSpan) DroppedLinks() int {
	return s.droppedLinks
}

func (s projectedSpan) DroppedEvents() int {
	return s.droppedEvents
}

func nativeScope(scope instrumentation.Scope) (instrumentation.Scope, bool) {
	expectedVersion, known := expectedNativeVersion(scope.Name)
	if !known {
		return instrumentation.Scope{}, false
	}
	if scope.Version != expectedVersion || scope.SchemaURL != "" {
		return instrumentation.Scope{}, true
	}
	return instrumentation.Scope{
		Name:       scope.Name,
		Version:    expectedVersion,
		Attributes: attribute.NewSet(),
	}, true
}

func expectedNativeVersion(scopeName string) (string, bool) {
	switch scopeName {
	case otelhttp.ScopeName:
		return otelhttp.Version, true
	case otelgin.ScopeName:
		return otelgin.Version, true
	case fiberScopeName:
		return contrib.Version(), true
	case otelgrpc.ScopeName:
		return otelgrpc.Version, true
	default:
		return "", false
	}
}

func projectSpanName(span sdktrace.ReadOnlySpan, policy compiledTracePolicy) string {
	name := span.Name()
	switch span.InstrumentationScope().Name {
	case otelgrpc.ScopeName:
		if _, ok := policy.rpcNames[name]; ok {
			return name
		}
		return fallbackRPCName
	case otelhttp.ScopeName, otelgin.ScopeName, fiberScopeName:
		if span.SpanKind() == trace.SpanKindClient {
			method := strings.TrimPrefix(name, "HTTP ")
			return "HTTP " + normalizeHTTPMethod(method)
		}
		if _, ok := policy.httpNames[name]; ok {
			return name
		}
		return fallbackHTTPName
	default:
		return "operation"
	}
}

func projectTraceAttributes(scope string, attributes []attribute.KeyValue, policy compiledTracePolicy) []attribute.KeyValue {
	projected := make([]attribute.KeyValue, 0, len(attributes))
	for _, item := range attributes {
		key := string(item.Key)
		switch scope {
		case otelgrpc.ScopeName:
			switch key {
			case "rpc.system.name", "rpc.system":
				if item.Value.Type() == attribute.STRING && item.Value.AsString() == "grpc" {
					projected = append(projected, item)
				}
			case "rpc.method":
				if item.Value.Type() != attribute.STRING {
					continue
				}
				if _, ok := policy.rpcNames[item.Value.AsString()]; ok {
					projected = append(projected, item)
				} else {
					projected = append(projected, attribute.String(key, fallbackRPCName))
				}
			case "rpc.grpc.status_code":
				if item.Value.Type() != attribute.INT64 {
					continue
				}
				value := item.Value.AsInt64()
				if value >= 0 && value <= 16 {
					projected = append(projected, item)
				}
			case "rpc.response.status_code":
				if item.Value.Type() != attribute.STRING {
					continue
				}
				if status, ok := normalizeRPCStatus(item.Value.AsString()); ok {
					projected = append(projected, attribute.String(key, status))
				}
			}
		case otelhttp.ScopeName, otelgin.ScopeName, fiberScopeName:
			switch key {
			case "http.request.method":
				if item.Value.Type() == attribute.STRING {
					projected = append(projected, attribute.String(key, normalizeHTTPMethod(item.Value.AsString())))
				}
			case "http.response.status_code":
				if item.Value.Type() != attribute.INT64 {
					continue
				}
				value := item.Value.AsInt64()
				if value >= 100 && value <= 599 {
					projected = append(projected, item)
				}
			case "http.route":
				if item.Value.Type() != attribute.STRING {
					continue
				}
				if _, ok := policy.httpRoutes[item.Value.AsString()]; ok {
					projected = append(projected, item)
				}
			}
		}
	}
	return projected
}

func projectTransportMetricAttributes(scope string, attributes []attribute.KeyValue, policy compiledTracePolicy) []attribute.KeyValue {
	projected := projectTraceAttributes(scope, attributes, policy)
	if scope != otelhttp.ScopeName {
		return projected
	}
	for _, item := range attributes {
		if string(item.Key) == httpManagementMetricAttribute && item.Value.Type() == attribute.STRING && (item.Value.AsString() == httpManagementMetricValue || item.Value.AsString() == httpApplicationMetricValue) {
			projected = append(projected, item)
		}
	}
	return projected
}

func normalizeRPCStatus(value string) (string, bool) {
	switch value {
	case "OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND", "ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION", "ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS", "UNAUTHENTICATED":
		return value, true
	default:
		return "", false
	}
}

func projectResource(original *resource.Resource, allowed map[string]attribute.Value) *resource.Resource {
	if original == nil || len(allowed) == 0 {
		return resource.Empty()
	}
	projected := make([]attribute.KeyValue, 0, len(allowed))
	for _, item := range original.Attributes() {
		want, ok := allowed[string(item.Key)]
		if ok && item.Value.Type() == want.Type() && item.Value.Emit() == want.Emit() {
			projected = append(projected, item)
		}
	}
	return resource.NewSchemaless(projected...)
}
