package otelnative

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"google.golang.org/grpc/stats"
)

type nativeFixtureInstrument struct {
	resource     string
	scope        string
	scopeName    string
	name         string
	originalUnit string
}

var nativeHTTPBudgetRoster = []nativeFixtureInstrument{
	{"transport", "otelhttp", "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", "http.server.request.body.size", "By"},
	{"transport", "otelhttp", "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", "http.server.response.body.size", "By"},
	{"transport", "otelhttp", "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", "http.server.request.duration", "s"},
	{"transport", "otelhttp", "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", "http.client.request.body.size", "By"},
	{"transport", "otelhttp", "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", "http.client.request.duration", "s"},
}

var nativeGinBudgetRoster = []nativeFixtureInstrument{
	{"transport", "otelgin", "go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin", "http.server.request.body.size", "By"},
	{"transport", "otelgin", "go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin", "http.server.response.body.size", "By"},
	{"transport", "otelgin", "go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin", "http.server.request.duration", "s"},
}

var nativeFiberBudgetRoster = []nativeFixtureInstrument{
	{"transport", "fiberotel", "github.com/gofiber/contrib/v3/otel", "http.server.active_requests", "1"},
	{"transport", "fiberotel", "github.com/gofiber/contrib/v3/otel", "http.server.request.body.size", "By"},
	{"transport", "fiberotel", "github.com/gofiber/contrib/v3/otel", "http.server.response.body.size", "By"},
	{"transport", "fiberotel", "github.com/gofiber/contrib/v3/otel", "http.server.request.duration", "s"},
}

var nativeGRPCBudgetRoster = []nativeFixtureInstrument{
	{"transport", "otelgrpc", "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc", "rpc.client.call.duration", "s"},
	{"transport", "otelgrpc", "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc", "rpc.server.call.duration", "s"},
}

var nativeDatabaseBudgetRoster = []nativeFixtureInstrument{
	{"database", "otelpgx", "github.com/exaring/otelpgx", "db.client.operation.duration", "s"},
	{"database", "otelpgx", "github.com/exaring/otelpgx", "db.client.operation.errors", ""},
	{"database", "otelsql", "github.com/XSAM/otelsql", "db.client.operation.duration", "s"},
	{"database", "otelsql", "github.com/XSAM/otelsql", "db.sql.connection.max_open", ""},
	{"database", "otelsql", "github.com/XSAM/otelsql", "db.sql.connection.open", ""},
	{"database", "otelsql", "github.com/XSAM/otelsql", "db.sql.connection.wait", ""},
	{"database", "otelsql", "github.com/XSAM/otelsql", "db.sql.connection.wait_duration", "ms"},
	{"database", "otelsql", "github.com/XSAM/otelsql", "db.sql.connection.closed_max_idle", ""},
	{"database", "otelsql", "github.com/XSAM/otelsql", "db.sql.connection.closed_max_idle_time", ""},
	{"database", "otelsql", "github.com/XSAM/otelsql", "db.sql.connection.closed_max_lifetime", ""},
	{"database", "pgxpool", "github.com/frostgrove/vv/test/otelnative/pgxpool", "app.db.pool.connections.acquired", "{connection}"},
	{"database", "pgxpool", "github.com/frostgrove/vv/test/otelnative/pgxpool", "app.db.pool.connections.idle", "{connection}"},
	{"database", "pgxpool", "github.com/frostgrove/vv/test/otelnative/pgxpool", "app.db.pool.connections.max", "{connection}"},
	{"database", "pgxpool", "github.com/frostgrove/vv/test/otelnative/pgxpool", "app.db.pool.acquire.waits", "{wait}"},
	{"database", "pgxpool", "github.com/frostgrove/vv/test/otelnative/pgxpool", "app.db.pool.acquire.wait.duration", "s"},
}

var nativeRuntimeBudgetRoster = []nativeFixtureInstrument{
	{"runtime", "runtime", "go.opentelemetry.io/contrib/instrumentation/runtime", "go.memory.used", "By"},
	{"runtime", "runtime", "go.opentelemetry.io/contrib/instrumentation/runtime", "go.memory.limit", "By"},
	{"runtime", "runtime", "go.opentelemetry.io/contrib/instrumentation/runtime", "go.memory.allocated", "By"},
	{"runtime", "runtime", "go.opentelemetry.io/contrib/instrumentation/runtime", "go.memory.allocations", "{allocation}"},
	{"runtime", "runtime", "go.opentelemetry.io/contrib/instrumentation/runtime", "go.memory.gc.goal", "By"},
	{"runtime", "runtime", "go.opentelemetry.io/contrib/instrumentation/runtime", "go.goroutine.count", "{goroutine}"},
	{"runtime", "runtime", "go.opentelemetry.io/contrib/instrumentation/runtime", "go.processor.limit", "{thread}"},
	{"runtime", "runtime", "go.opentelemetry.io/contrib/instrumentation/runtime", "go.config.gogc", "%"},
}

func TestNativeBudgetRealHTTPFixturesFit(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	t.Run("net/http server and client", func(t *testing.T) {
		routes := NewHTTPRoutes()
		if err := routes.HandleFunc("GET /items/{id}", func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte("ok"))
		}); err != nil {
			t.Fatal(err)
		}
		if err := routes.HandleFunc("/live", func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}); err != nil {
			t.Fatal(err)
		}
		policy := TraceProjectionPolicy{
			HTTPRoutes:         routes,
			ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", allowedServiceName)},
		}
		fixture := newTelemetryFixture(t, policy, nil)
		handler, err := HTTPServer(fixture.providers, routes, PublicIngress)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		defer server.Close()
		transport, err := HTTPTransport(fixture.providers, http.DefaultTransport)
		if err != nil {
			t.Fatal(err)
		}
		response, err := (&http.Client{Transport: transport}).Get(server.URL + "/items/1")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		if err = response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		response, err = (&http.Client{Transport: transport}).Get(server.URL + "/live")
		if err != nil {
			t.Fatal(err)
		}
		if err = response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		metrics := fixture.flushMetrics(t)
		scanNativeFixture(t, manifest, fixture.rawMetrics.snapshot(), metrics, nativeHTTPBudgetRoster)
	})
	t.Run("Gin", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		routes, err := NewRouteTable(Route{Method: http.MethodGet, Pattern: "/items/:id"}, Route{Method: http.MethodGet, Pattern: "/fail/:id"})
		if err != nil {
			t.Fatal(err)
		}
		policy := TraceProjectionPolicy{
			RouterTables:       []RouteTable{routes},
			ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", allowedServiceName)},
		}
		fixture := newTelemetryFixture(t, policy, nil)
		middleware, err := GinMiddleware(fixture.providers, routes, PublicIngress, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		router := gin.New()
		router.Use(middleware)
		router.GET("/items/:id", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/items/1", nil))
		metrics := fixture.flushMetrics(t)
		scanNativeFixture(t, manifest, fixture.rawMetrics.snapshot(), metrics, nativeGinBudgetRoster)
	})
	t.Run("Fiber", func(t *testing.T) {
		routes, err := NewRouteTable(Route{Method: http.MethodGet, Pattern: "/items/:id"}, Route{Method: http.MethodGet, Pattern: "/fail/:id"})
		if err != nil {
			t.Fatal(err)
		}
		policy := TraceProjectionPolicy{
			RouterTables:       []RouteTable{routes},
			ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", allowedServiceName)},
		}
		fixture := newTelemetryFixture(t, policy, nil)
		middleware, err := FiberMiddleware(fixture.providers, routes, PublicIngress, 8080)
		if err != nil {
			t.Fatal(err)
		}
		app := fiber.New()
		app.Use(middleware)
		app.Get("/items/:id", func(ctx fiber.Ctx) error { return ctx.SendStatus(http.StatusNoContent) })
		response, err := app.Test(httptest.NewRequest(http.MethodGet, "http://fixture/items/1", nil))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		if err = response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		metrics := fixture.flushMetrics(t)
		scanNativeFixture(t, manifest, fixture.rawMetrics.snapshot(), metrics, nativeFiberBudgetRoster)
	})
}

func TestNativeBudgetRealGRPCFixtureFits(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	raw, projected := captureNativeGRPCBudgetFixture(t)
	scanNativeFixture(t, manifest, raw, projected, nativeGRPCBudgetRoster)
}

func captureNativeGRPCBudgetFixture(t *testing.T) (metricdata.ResourceMetrics, metricdata.ResourceMetrics) {
	t.Helper()
	methods, err := NewRPCTable(grpcPingMethod, grpcFailMethod)
	if err != nil {
		t.Fatal(err)
	}
	policy := TraceProjectionPolicy{
		RPCMethods:         methods,
		ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", allowedServiceName)},
	}
	fixture := newTelemetryFixture(t, policy, nil)
	server, err := GRPCServerStats(fixture.providers, methods, PublicIngress)
	if err != nil {
		t.Fatal(err)
	}
	client, err := GRPCClientStats(fixture.providers, methods)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for _, item := range []struct {
		handler stats.Handler
		client  bool
	}{
		{handler: server},
		{handler: client, client: true},
	} {
		ctx := item.handler.TagRPC(context.Background(), &stats.RPCTagInfo{FullMethodName: grpcPingMethod})
		item.handler.HandleRPC(ctx, &stats.Begin{Client: item.client, BeginTime: started})
		item.handler.HandleRPC(ctx, &stats.End{Client: item.client, BeginTime: started, EndTime: started.Add(time.Millisecond)})
	}
	projected := fixture.flushMetrics(t)
	return fixture.rawMetrics.snapshot(), projected
}

func TestNativeBudgetRealDatabaseFixturesFit(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	raw, projected := captureNativeDatabaseBudgetFixture(t)
	scanNativeFixture(t, manifest, raw, projected, nativeDatabaseBudgetRoster)
}

func captureNativeDatabaseBudgetFixture(t *testing.T) (metricdata.ResourceMetrics, metricdata.ResourceMetrics) {
	t.Helper()
	poolName, err := NewDatabasePoolName("primary")
	if err != nil {
		t.Fatal(err)
	}
	fixture := newDatabaseTelemetryFixture(t, []DatabasePoolName{poolName}, nil)
	pgxTracer, err := NewPGXTracer(fixture.providers)
	if err != nil {
		t.Fatal(err)
	}
	query := pgxTracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "select 1"})
	pgxTracer.TraceQueryEnd(query, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})
	failure := pgxTracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "select fail"})
	pgxTracer.TraceQueryEnd(failure, nil, pgx.TraceQueryEndData{Err: errors.New("expected fixture failure")})

	database, err := OpenSQL(fixture.providers, "sqlite", "file:native-budget?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	sqlRegistration, err := RegisterSQLDBStats(fixture.providers, database, poolName)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlRegistration.Unregister()
	if _, err = database.ExecContext(context.Background(), "select 1"); err != nil {
		t.Fatal(err)
	}

	config, err := pgxpool.ParseConfig("postgres://fixture:fixture@127.0.0.1:1/fixture?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	pgxRegistration, err := RegisterPGXPoolStats(fixture.providers, pool, poolName)
	if err != nil {
		t.Fatal(err)
	}
	defer pgxRegistration.Unregister()
	fixture.flushMetrics(t)
	return fixture.rawMetrics.snapshot(), fixture.metrics.snapshot()
}

func TestNativeBudgetRealRuntimeFixtureFits(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	raw, projected := captureNativeRuntimeBudgetFixture(t)
	scanNativeFixture(t, manifest, raw, projected, nativeRuntimeBudgetRoster)
}

func captureNativeRuntimeBudgetFixture(t *testing.T) (metricdata.ResourceMetrics, metricdata.ResourceMetrics) {
	t.Helper()
	previousLimit := debug.SetMemoryLimit(1 << 40)
	defer debug.SetMemoryLimit(previousLimit)
	sink := &metricCapture{}
	exporter, err := NewRuntimeMetricExporter(sink, RuntimeProjectionPolicy{
		ResourceAttributes: []attribute.KeyValue{attribute.String("service.name", runtimeServiceName)},
	})
	if err != nil {
		t.Fatal(err)
	}
	rawSink := &metricCapture{}
	reader := sdkmetric.NewPeriodicReader(&nativeRawMetricTap{next: exporter, capture: rawSink})
	options := []sdkmetric.Option{
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(resource.NewSchemaless(
			attribute.String("service.name", runtimeServiceName),
			attribute.String("secret.resource", secretResource),
		)),
	}
	options = append(options, RuntimeMetricOptions()...)
	provider := sdkmetric.NewMeterProvider(options...)
	defer provider.Shutdown(context.Background())
	if err = StartRuntimeMetrics(provider); err != nil {
		t.Fatal(err)
	}
	if err = provider.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	return rawSink.snapshot(), sink.snapshot()
}

func scanNativeFixture(t *testing.T, manifest NativeBudgetManifest, raw, projected metricdata.ResourceMetrics, roster []nativeFixtureInstrument) {
	t.Helper()
	if err := validateNativeOriginalUnits(raw, roster); err != nil {
		t.Fatal(err)
	}
	scanner, err := NewNativeBudgetScanner(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = scanner.ScanExact(projected, nativeBudgetRosterRefs(roster)); err != nil {
		t.Fatal(err)
	}
}

func TestNativeBudgetRealFixtureMutationsFail(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	rawDatabase, projectedDatabase := captureNativeDatabaseBudgetFixture(t)
	_, projectedGRPC := captureNativeGRPCBudgetFixture(t)
	t.Run("one captured instrument removed", func(t *testing.T) {
		mutated := removeNativeFixtureInstrument(t, projectedDatabase, "github.com/exaring/otelpgx", "db.client.operation.errors")
		assertNativeExactScanViolation(t, manifest, mutated, nativeDatabaseBudgetRoster)
	})
	t.Run("client half removed", func(t *testing.T) {
		mutated := removeNativeFixtureInstrument(t, projectedGRPC, "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc", "rpc.client.call.duration")
		assertNativeExactScanViolation(t, manifest, mutated, nativeGRPCBudgetRoster)
	})
	t.Run("unexpected known instrument outside roster", func(t *testing.T) {
		assertNativeExactScanViolation(t, manifest, projectedGRPC, nativeGRPCBudgetRoster[1:])
	})
	t.Run("entire integration scope removed", func(t *testing.T) {
		mutated := removeNativeFixtureScope(t, projectedDatabase, "github.com/frostgrove/vv/test/otelnative/pgxpool")
		assertNativeExactScanViolation(t, manifest, mutated, nativeDatabaseBudgetRoster)
	})
	t.Run("original unit changed before projection", func(t *testing.T) {
		mutated := changeNativeFixtureUnit(t, rawDatabase, "github.com/exaring/otelpgx", "db.client.operation.duration", "ms")
		if err := validateNativeOriginalUnits(mutated, nativeDatabaseBudgetRoster); !errors.Is(err, ErrNativeBudgetViolation) {
			t.Fatalf("error=%v", err)
		}
	})
}

type nativeRawMetricTap struct {
	next    sdkmetric.Exporter
	capture *metricCapture
}

func (e *nativeRawMetricTap) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.next.Temporality(kind)
}

func (e *nativeRawMetricTap) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.next.Aggregation(kind)
}

func (e *nativeRawMetricTap) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	cloned := cloneNativeFixtureMetrics(*metrics)
	if err := e.capture.Export(ctx, &cloned); err != nil {
		return err
	}
	return e.next.Export(ctx, metrics)
}

func (e *nativeRawMetricTap) ForceFlush(ctx context.Context) error {
	return e.next.ForceFlush(ctx)
}

func (e *nativeRawMetricTap) Shutdown(ctx context.Context) error {
	return e.next.Shutdown(ctx)
}

func nativeBudgetRosterRefs(roster []nativeFixtureInstrument) []NativeBudgetInstrumentRef {
	refs := make([]NativeBudgetInstrumentRef, len(roster))
	for index, item := range roster {
		refs[index] = NativeBudgetInstrumentRef{Resource: item.resource, Scope: item.scope, Name: item.name}
	}
	return refs
}

func validateNativeOriginalUnits(metrics metricdata.ResourceMetrics, roster []nativeFixtureInstrument) error {
	if len(roster) == 0 {
		return nativeBudgetViolation("original-unit roster is empty")
	}
	resourceID := roster[0].resource
	expectedScopes := make(map[string]struct{}, len(roster))
	expected := make(map[string]string, len(roster))
	for _, item := range roster {
		if item.resource != resourceID || item.scopeName == "" || item.name == "" {
			return nativeBudgetViolation("original-unit roster contains an invalid tuple")
		}
		key := nativeInstrumentKey(item.resource, item.scopeName, item.name)
		if _, duplicate := expected[key]; duplicate {
			return nativeBudgetViolation("original-unit roster duplicates %q/%q", item.scopeName, item.name)
		}
		expectedScopes[item.scopeName] = struct{}{}
		expected[key] = item.originalUnit
	}
	actualScopes := make(map[string]struct{}, len(metrics.ScopeMetrics))
	actual := make(map[string]struct{}, len(expected))
	for _, scoped := range metrics.ScopeMetrics {
		if _, wanted := expectedScopes[scoped.Scope.Name]; !wanted {
			return nativeBudgetViolation("unexpected original scope %q", scoped.Scope.Name)
		}
		if _, duplicate := actualScopes[scoped.Scope.Name]; duplicate {
			return nativeBudgetViolation("duplicate original scope %q", scoped.Scope.Name)
		}
		actualScopes[scoped.Scope.Name] = struct{}{}
		for _, measurement := range scoped.Metrics {
			key := nativeInstrumentKey(resourceID, scoped.Scope.Name, measurement.Name)
			unit, wanted := expected[key]
			if !wanted {
				return nativeBudgetViolation("unexpected original instrument %q/%q", scoped.Scope.Name, measurement.Name)
			}
			if _, duplicate := actual[key]; duplicate {
				return nativeBudgetViolation("duplicate original instrument %q/%q", scoped.Scope.Name, measurement.Name)
			}
			if measurement.Unit != unit {
				return nativeBudgetViolation("original instrument %q/%q unit is %q, expected %q", scoped.Scope.Name, measurement.Name, measurement.Unit, unit)
			}
			actual[key] = struct{}{}
		}
	}
	for scope := range expectedScopes {
		if _, present := actualScopes[scope]; !present {
			return nativeBudgetViolation("missing original scope %q", scope)
		}
	}
	for key := range expected {
		if _, present := actual[key]; !present {
			return nativeBudgetViolation("missing original instrument %q", key)
		}
	}
	return nil
}

func assertNativeExactScanViolation(t *testing.T, manifest NativeBudgetManifest, metrics metricdata.ResourceMetrics, roster []nativeFixtureInstrument) {
	t.Helper()
	scanner, err := NewNativeBudgetScanner(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = scanner.ScanExact(metrics, nativeBudgetRosterRefs(roster)); !errors.Is(err, ErrNativeBudgetViolation) {
		t.Fatalf("error=%v", err)
	}
}

func cloneNativeFixtureMetrics(metrics metricdata.ResourceMetrics) metricdata.ResourceMetrics {
	cloned := metrics
	cloned.ScopeMetrics = append([]metricdata.ScopeMetrics(nil), metrics.ScopeMetrics...)
	for index := range cloned.ScopeMetrics {
		cloned.ScopeMetrics[index].Metrics = append([]metricdata.Metrics(nil), metrics.ScopeMetrics[index].Metrics...)
	}
	return cloned
}

func removeNativeFixtureInstrument(t *testing.T, metrics metricdata.ResourceMetrics, scope, name string) metricdata.ResourceMetrics {
	t.Helper()
	cloned := cloneNativeFixtureMetrics(metrics)
	for scopeIndex := range cloned.ScopeMetrics {
		if cloned.ScopeMetrics[scopeIndex].Scope.Name != scope {
			continue
		}
		for metricIndex, measurement := range cloned.ScopeMetrics[scopeIndex].Metrics {
			if measurement.Name != name {
				continue
			}
			cloned.ScopeMetrics[scopeIndex].Metrics = append(cloned.ScopeMetrics[scopeIndex].Metrics[:metricIndex], cloned.ScopeMetrics[scopeIndex].Metrics[metricIndex+1:]...)
			return cloned
		}
	}
	t.Fatalf("missing captured instrument %q/%q", scope, name)
	return metricdata.ResourceMetrics{}
}

func removeNativeFixtureScope(t *testing.T, metrics metricdata.ResourceMetrics, scope string) metricdata.ResourceMetrics {
	t.Helper()
	cloned := cloneNativeFixtureMetrics(metrics)
	for index, scoped := range cloned.ScopeMetrics {
		if scoped.Scope.Name != scope {
			continue
		}
		cloned.ScopeMetrics = append(cloned.ScopeMetrics[:index], cloned.ScopeMetrics[index+1:]...)
		return cloned
	}
	t.Fatalf("missing captured scope %q", scope)
	return metricdata.ResourceMetrics{}
}

func changeNativeFixtureUnit(t *testing.T, metrics metricdata.ResourceMetrics, scope, name, unit string) metricdata.ResourceMetrics {
	t.Helper()
	cloned := cloneNativeFixtureMetrics(metrics)
	for scopeIndex := range cloned.ScopeMetrics {
		if cloned.ScopeMetrics[scopeIndex].Scope.Name != scope {
			continue
		}
		for metricIndex := range cloned.ScopeMetrics[scopeIndex].Metrics {
			if cloned.ScopeMetrics[scopeIndex].Metrics[metricIndex].Name == name {
				cloned.ScopeMetrics[scopeIndex].Metrics[metricIndex].Unit = unit
				return cloned
			}
		}
	}
	t.Fatalf("missing captured instrument %q/%q", scope, name)
	return metricdata.ResourceMetrics{}
}
