package otelnative

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type fiberContextKey struct{}

func TestFiberPublicIngressSanitizesBeforeSDKStart(t *testing.T) {
	routes, err := NewRouteTable(
		Route{Method: http.MethodGet, Pattern: "/items/:id"},
		Route{Method: http.MethodGet, Pattern: "/fail/:id"},
	)
	if err != nil {
		t.Fatal(err)
	}
	policy := TraceProjectionPolicy{
		RouterTables: []RouteTable{routes},
		ResourceAttributes: []attribute.KeyValue{
			attribute.String("service.name", allowedServiceName),
		},
	}
	sampler := &nameSampler{}
	telemetry := newTelemetryFixture(t, policy, sampler)
	middleware, err := FiberMiddleware(telemetry.providers, routes, PublicIngress, 8080)
	if err != nil {
		t.Fatal(err)
	}
	marker := &struct{}{}
	var handlerSpan trace.SpanContext
	var handlerContextOK bool
	var handlerPrivacyOK bool
	app := fiber.New()
	app.Use(func(ctx fiber.Ctx) error {
		ctx.SetContext(context.WithValue(ctx.Context(), fiberContextKey{}, marker))
		return ctx.Next()
	})
	app.Use(middleware)
	app.Get("/items/:id", func(ctx fiber.Ctx) error {
		handlerContextOK = ctx.Context().Value(fiberContextKey{}) == marker
		handlerPrivacyOK = trace.SpanContextFromContext(ctx.Context()).TraceState().Len() == 0
		handlerSpan = trace.SpanContextFromContext(ctx.Context())
		return ctx.Status(http.StatusAccepted).SendString(ctx.Params("id"))
	})
	app.Get("/fail/:id", func(fiber.Ctx) error {
		return errors.New("secret-fiber-error")
	})
	request := httptest.NewRequest(http.MethodGet, "http://public.example/items/secret-fiber-id?token=secret-fiber-query", nil)
	request.Header.Set("Authorization", "Basic c2VjcmV0LWZpYmVyLXVzZXI6c2VjcmV0")
	remote := remoteHeaders(t, propagation.HeaderCarrier(request.Header))
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted || string(body) != "secret-fiber-id" {
		t.Fatalf("response=%d/%q", response.StatusCode, body)
	}
	if !handlerContextOK || !handlerPrivacyOK {
		t.Fatalf("handler context=%t privacy=%t", handlerContextOK, handlerPrivacyOK)
	}
	failure := httptest.NewRequest(http.MethodGet, "http://public.example/fail/secret-fiber-failure", nil)
	remoteHeaders(t, propagation.HeaderCarrier(failure.Header))
	failureResponse, err := app.Test(failure)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, failureResponse.Body)
	_ = failureResponse.Body.Close()
	if failureResponse.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failure response=%d", failureResponse.StatusCode)
	}
	unmatched := httptest.NewRequest(http.MethodGet, "http://public.example/missing/secret-fiber-missing", nil)
	remoteHeaders(t, propagation.HeaderCarrier(unmatched.Header))
	unmatchedResponse, err := app.Test(unmatched)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, unmatchedResponse.Body)
	_ = unmatchedResponse.Body.Close()
	if unmatchedResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("unmatched response=%d", unmatchedResponse.StatusCode)
	}
	spans := telemetry.nativeSpans()
	if len(spans) != 3 {
		t.Fatalf("native spans=%d names=%v", len(spans), spanNames(spans))
	}
	successSpan := findSpan(t, spans, "GET /items/:id", trace.SpanKindServer)
	assertPublicSpan(t, successSpan, remote)
	if handlerSpan.TraceID() != successSpan.SpanContext.TraceID() || handlerSpan.SpanID() != successSpan.SpanContext.SpanID() {
		t.Fatalf("handler span=%v exported=%v", handlerSpan, successSpan.SpanContext)
	}
	failureSpan := findSpan(t, spans, "GET /fail/:id", trace.SpanKindServer)
	if failureSpan.Status.Code != codes.Error || failureSpan.Status.Description != "" || len(failureSpan.Events) != 0 {
		t.Fatalf("projected failure=%#v events=%#v", failureSpan.Status, failureSpan.Events)
	}
	findSpan(t, spans, fallbackHTTPName, trace.SpanKindServer)
	for _, name := range sampler.snapshot() {
		if strings.Contains(name, "secret-") {
			t.Fatalf("raw Fiber path reached SDK sampler: %q", name)
		}
	}
	metrics := telemetry.flushMetrics(t)
	assertNativeMetricsPresent(t, metrics)
	assertNativePrivacy(t, spans, metrics,
		secretResource,
		"secret-state",
		"secret-baggage",
		"secret-fiber-id",
		"secret-fiber-query",
		"secret-fiber-user",
		"secret-fiber-error",
		"secret-fiber-failure",
		"secret-fiber-missing",
	)
}
