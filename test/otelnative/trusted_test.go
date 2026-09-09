package otelnative

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
)

func TestTrustedIngressRetainsRemoteParentAcrossNativeBridges(t *testing.T) {
	t.Run("gin", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		routes, err := NewRouteTable(Route{Method: http.MethodGet, Pattern: "/trusted/:id"})
		if err != nil {
			t.Fatal(err)
		}
		policy := TraceProjectionPolicy{
			RouterTables:       []RouteTable{routes},
			ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", allowedServiceName)},
		}
		telemetry := newTelemetryFixture(t, policy, nil)
		middleware, err := GinMiddleware(telemetry.providers, routes, TrustedIngress, "trusted")
		if err != nil {
			t.Fatal(err)
		}
		router := gin.New()
		router.Use(middleware)
		router.GET("/trusted/:id", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
		request := httptest.NewRequest(http.MethodGet, "/trusted/secret-trusted-gin", nil)
		remote := remoteHeaders(t, propagation.HeaderCarrier(request.Header))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("response=%d", response.Code)
		}
		spans := telemetry.nativeSpans()
		if len(spans) != 1 {
			t.Fatalf("spans=%v", spanNamesAndKinds(spans))
		}
		assertTrustedSpan(t, findSpan(t, spans, "GET /trusted/:id", trace.SpanKindServer), remote)
	})

	t.Run("fiber", func(t *testing.T) {
		routes, err := NewRouteTable(Route{Method: http.MethodGet, Pattern: "/trusted/:id"})
		if err != nil {
			t.Fatal(err)
		}
		policy := TraceProjectionPolicy{
			RouterTables:       []RouteTable{routes},
			ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", allowedServiceName)},
		}
		telemetry := newTelemetryFixture(t, policy, nil)
		middleware, err := FiberMiddleware(telemetry.providers, routes, TrustedIngress, 8080)
		if err != nil {
			t.Fatal(err)
		}
		app := fiber.New()
		app.Use(middleware)
		app.Get("/trusted/:id", func(ctx fiber.Ctx) error { return ctx.SendStatus(http.StatusNoContent) })
		request := httptest.NewRequest(http.MethodGet, "http://trusted.example/trusted/secret-trusted-fiber", nil)
		remote := remoteHeaders(t, propagation.HeaderCarrier(request.Header))
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("response=%d", response.StatusCode)
		}
		spans := telemetry.nativeSpans()
		if len(spans) != 1 {
			t.Fatalf("spans=%v", spanNamesAndKinds(spans))
		}
		assertTrustedSpan(t, findSpan(t, spans, "GET /trusted/:id", trace.SpanKindServer), remote)
	})

	t.Run("grpc", func(t *testing.T) {
		methods, err := NewRPCTable(grpcPingMethod)
		if err != nil {
			t.Fatal(err)
		}
		policy := TraceProjectionPolicy{
			RPCMethods:         methods,
			ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", allowedServiceName)},
		}
		telemetry := newTelemetryFixture(t, policy, nil)
		handler, err := GRPCServerStats(telemetry.providers, methods, TrustedIngress)
		if err != nil {
			t.Fatal(err)
		}
		headers := propagation.HeaderCarrier{}
		remote := remoteHeaders(t, headers)
		md := metadata.MD{}
		for key, values := range headers {
			md[strings.ToLower(key)] = append([]string(nil), values...)
		}
		ctx := metadata.NewIncomingContext(context.Background(), md)
		ctx = handler.TagRPC(ctx, &stats.RPCTagInfo{FullMethodName: grpcPingMethod})
		now := time.Now()
		handler.HandleRPC(ctx, &stats.Begin{BeginTime: now})
		handler.HandleRPC(ctx, &stats.End{BeginTime: now, EndTime: now.Add(time.Millisecond)})
		spans := telemetry.nativeSpans()
		if len(spans) != 1 {
			t.Fatalf("spans=%v", spanNamesAndKinds(spans))
		}
		assertTrustedSpan(t, findSpan(t, spans, "otelnative.Transport/Ping", trace.SpanKindServer), remote)
	})
}
