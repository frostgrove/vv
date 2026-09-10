package otelnative

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type httpContextKey struct{}

type typedNilHTTPTransport struct{}

func (*typedNilHTTPTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("typed nil transport invoked")
}

func TestHTTPTrustedServerAndClientComposeOnce(t *testing.T) {
	routes := NewHTTPRoutes()
	var handlerSpan trace.SpanContext
	if err := routes.HandleFunc("GET /items/{id}", func(writer http.ResponseWriter, request *http.Request) {
		handlerSpan = trace.SpanContextFromContext(request.Context())
		writer.Header().Set("X-Result", "exact-result")
		_, _ = writer.Write([]byte(request.PathValue("id")))
	}); err != nil {
		t.Fatal(err)
	}
	policy := TraceProjectionPolicy{
		HTTPRoutes: routes,
		ResourceAttributes: []attribute.KeyValue{
			attribute.String("service.name", allowedServiceName),
		},
	}
	telemetry := newTelemetryFixture(t, policy, nil)
	handler, err := HTTPServer(telemetry.providers, routes, TrustedIngress)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	transport, err := HTTPTransport(telemetry.providers, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport}
	rootCtx, root := telemetry.providers.Tracer.Tracer("fixture").Start(context.Background(), "fixture root")
	request, err := http.NewRequestWithContext(rootCtx, http.MethodGet, server.URL+"/items/secret-id-4815?token=secret-query-4815", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret-header-4815")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, span := range telemetry.nativeSpans() {
		if span.SpanKind == trace.SpanKindClient {
			t.Fatalf("HTTP client span ended before response body: %v", spanNames(telemetry.nativeSpans()))
		}
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	root.End()
	if string(body) != "secret-id-4815" || response.Header.Get("X-Result") != "exact-result" {
		t.Fatalf("response=%q header=%q", body, response.Header.Get("X-Result"))
	}
	if request.Header.Get("traceparent") != "" {
		t.Fatalf("client mutated original headers: %v", request.Header)
	}
	spans := telemetry.nativeSpans()
	if len(spans) != 2 {
		t.Fatalf("native spans=%d names=%v", len(spans), spanNames(spans))
	}
	clientSpan := findSpan(t, spans, "HTTP GET", trace.SpanKindClient)
	serverSpan := findSpan(t, spans, "GET /items/{id}", trace.SpanKindServer)
	if serverSpan.Parent.SpanID() != clientSpan.SpanContext.SpanID() || handlerSpan.TraceID() != serverSpan.SpanContext.TraceID() || handlerSpan.SpanID() != serverSpan.SpanContext.SpanID() {
		t.Fatalf("lineage client=%v server-parent=%v handler=%v server=%v", clientSpan.SpanContext, serverSpan.Parent, handlerSpan, serverSpan.SpanContext)
	}
	metrics := telemetry.flushMetrics(t)
	assertNativeMetricsPresent(t, metrics)
	assertNativeExemplarPresent(t, metrics)
	assertNativePrivacy(t, spans, metrics, secretResource, "secret-id-4815", "secret-query-4815", "secret-header-4815")
}

func TestHTTPTransportTreatsTypedNilAsDefault(t *testing.T) {
	telemetry := newTelemetryFixture(t, TraceProjectionPolicy{}, nil)
	var base *typedNilHTTPTransport
	transport, err := HTTPTransport(telemetry.providers, base)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, "typed-nil://example", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(request); err == nil {
		t.Fatal("default transport accepted unsupported protocol")
	}
}

func TestHTTPRoutesZeroValueIsUsable(t *testing.T) {
	telemetry := newTelemetryFixture(t, TraceProjectionPolicy{}, nil)
	routes := &HTTPRoutes{}
	if err := routes.HandleFunc("GET /ready", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}); err != nil {
		t.Fatal(err)
	}
	handler, err := HTTPServer(telemetry.providers, routes, TrustedIngress)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestHTTPPublicIngressSanitizesAndBoundsFailures(t *testing.T) {
	routes := NewHTTPRoutes()
	marker := &struct{}{}
	var handlerCalls int
	if err := routes.HandleFunc("GET /items/{id}", func(writer http.ResponseWriter, request *http.Request) {
		handlerCalls++
		if request.Context().Value(httpContextKey{}) != marker {
			t.Fatal("request context value was not preserved")
		}
		assertNoBaggage(t, request.Context())
		writer.WriteHeader(http.StatusCreated)
	}); err != nil {
		t.Fatal(err)
	}
	policy := TraceProjectionPolicy{
		HTTPRoutes: routes,
		ResourceAttributes: []attribute.KeyValue{
			attribute.String("service.name", allowedServiceName),
		},
	}
	telemetry := newTelemetryFixture(t, policy, nil)
	handler, err := HTTPServer(telemetry.providers, routes, PublicIngress)
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.HandleFunc("GET /late", func(http.ResponseWriter, *http.Request) {}); !errors.Is(err, ErrRoutesSealed) {
		t.Fatalf("late registration error=%v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://public.example/items/secret-public-id?token=secret-public-query", nil)
	request = request.WithContext(context.WithValue(request.Context(), httpContextKey{}, marker))
	request.Header.Set("X-Secret", "secret-public-header")
	remote := remoteHeaders(t, propagation.HeaderCarrier(request.Header))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || handlerCalls != 1 {
		t.Fatalf("response=%d calls=%d", response.Code, handlerCalls)
	}
	unmatched := httptest.NewRequest(http.MethodGet, "http://public.example/missing/secret-missing-id", nil)
	unmatched.Pattern = "GET /items/{id}"
	remoteHeaders(t, propagation.HeaderCarrier(unmatched.Header))
	unmatchedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unmatchedResponse, unmatched)
	if unmatchedResponse.Code != http.StatusNotFound || handlerCalls != 1 {
		t.Fatalf("unmatched response=%d calls=%d", unmatchedResponse.Code, handlerCalls)
	}
	spans := telemetry.nativeSpans()
	if len(spans) != 2 {
		t.Fatalf("native spans=%d names=%v", len(spans), spanNames(spans))
	}
	assertPublicSpan(t, findSpan(t, spans, "GET /items/{id}", trace.SpanKindServer), remote)
	findSpan(t, spans, fallbackHTTPName, trace.SpanKindServer)
	metrics := telemetry.flushMetrics(t)
	assertNativeMetricsPresent(t, metrics)
	assertNativeExemplarPresent(t, metrics)
	assertNativePrivacy(t, spans, metrics,
		secretResource,
		"secret-state",
		"secret-baggage",
		"secret-public-id",
		"secret-public-query",
		"secret-public-header",
		"secret-missing-id",
	)
	for _, span := range spans {
		for _, item := range span.Attributes {
			if strings.HasPrefix(string(item.Key), "url.") {
				t.Fatalf("URL attribute survived: %v", item)
			}
		}
	}
}
