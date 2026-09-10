package otelnative

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type ginContextKey struct{}

func TestGinRejectsHostileServerNames(t *testing.T) {
	telemetry := newTelemetryFixture(t, TraceProjectionPolicy{}, nil)
	for _, name := range []string{"", "   ", strings.Repeat("s", maxNativeServerNameLength+1), string([]byte{0xff})} {
		if _, err := GinMiddleware(telemetry.providers, RouteTable{}, TrustedIngress, name); !errors.Is(err, ErrInvalidServerName) {
			t.Fatalf("server name %q error=%v", name, err)
		}
	}
}

func TestGinPublicIngressUsesOneNativeBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
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
	telemetry := newTelemetryFixture(t, policy, nil)
	middleware, err := GinMiddleware(telemetry.providers, routes, PublicIngress, "secret-gin-host")
	if err != nil {
		t.Fatal(err)
	}
	marker := &struct{}{}
	var handlerSpan trace.SpanContext
	router := gin.New()
	router.Use(middleware)
	router.GET("/items/:id", func(ctx *gin.Context) {
		if ctx.Request.Context().Value(ginContextKey{}) != marker {
			t.Fatal("request context value was not preserved")
		}
		assertNoBaggage(t, ctx.Request.Context())
		handlerSpan = trace.SpanContextFromContext(ctx.Request.Context())
		ctx.String(http.StatusAccepted, ctx.Param("id"))
	})
	router.GET("/fail/:id", func(ctx *gin.Context) {
		_ = ctx.Error(errors.New("secret-gin-error"))
		ctx.Status(http.StatusInternalServerError)
	})
	request := httptest.NewRequest(http.MethodGet, "/items/secret-gin-id?token=secret-gin-query", nil)
	request = request.WithContext(context.WithValue(request.Context(), ginContextKey{}, marker))
	request.Header.Set("Authorization", "Basic secret-gin-basic")
	remote := remoteHeaders(t, propagation.HeaderCarrier(request.Header))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Body.String() != "secret-gin-id" {
		t.Fatalf("response=%d/%q", response.Code, response.Body.String())
	}
	failure := httptest.NewRequest(http.MethodGet, "/fail/secret-gin-failure", nil)
	remoteHeaders(t, propagation.HeaderCarrier(failure.Header))
	failureResponse := httptest.NewRecorder()
	router.ServeHTTP(failureResponse, failure)
	if failureResponse.Code != http.StatusInternalServerError {
		t.Fatalf("failure response=%d", failureResponse.Code)
	}
	unmatched := httptest.NewRequest(http.MethodGet, "/missing/secret-gin-missing", nil)
	remoteHeaders(t, propagation.HeaderCarrier(unmatched.Header))
	unmatchedResponse := httptest.NewRecorder()
	router.ServeHTTP(unmatchedResponse, unmatched)
	if unmatchedResponse.Code != http.StatusNotFound {
		t.Fatalf("unmatched response=%d", unmatchedResponse.Code)
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
	metrics := telemetry.flushMetrics(t)
	assertNativeMetricsPresent(t, metrics)
	assertNativeExemplarPresent(t, metrics)
	assertNativePrivacy(t, spans, metrics,
		secretResource,
		"secret-state",
		"secret-baggage",
		"secret-gin-host",
		"secret-gin-id",
		"secret-gin-query",
		"secret-gin-basic",
		"secret-gin-error",
		"secret-gin-failure",
		"secret-gin-missing",
	)
}
