package otelnative

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/errs"
	vvotel "github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	_ "modernc.org/sqlite"
)

type fullStackContextKey struct{}

type fullStackModel struct {
	ID           string
	RowsAffected int64
	LastInsertID int64
}

type fullStackSQLSource struct {
	inner       crud.Source
	calls       int
	lastContext context.Context
	lastQuery   string
	lastArgs    []any
	result      crud.Result
	err         error
}

func (s *fullStackSQLSource) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	s.calls++
	s.lastContext = ctx
	s.lastQuery = query
	s.lastArgs = append([]any(nil), args...)
	s.result, s.err = s.inner.Exec(ctx, query, args...)
	return s.result, s.err
}

func (s *fullStackSQLSource) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return s.inner.Query(ctx, query, args...)
}

func (s *fullStackSQLSource) Dialect() crud.Dialect {
	return s.inner.Dialect()
}

type fullStackService struct {
	source      crud.Source
	meta        *crud.Meta
	query       string
	argument    string
	calls       int
	lastContext context.Context
	lastCommand port.GetCommand[string]
}

func (s *fullStackService) Meta() *crud.Meta {
	return s.meta
}

func (*fullStackService) Paths() errs.Resolver {
	return nil
}

func (*fullStackService) List(context.Context, port.ListCommand) (crud.PaginatedResponse[fullStackModel], error) {
	panic("unexpected List call")
}

func (*fullStackService) Count(context.Context, port.CountCommand) (int64, error) {
	panic("unexpected Count call")
}

func (s *fullStackService) Get(ctx context.Context, command port.GetCommand[string]) (fullStackModel, error) {
	s.calls++
	s.lastContext = ctx
	s.lastCommand = command
	result, err := s.source.Exec(ctx, s.query, s.argument)
	return fullStackModel{ID: command.ID, RowsAffected: result.RowsAffected, LastInsertID: result.LastInsertID}, err
}

func (*fullStackService) Create(context.Context, port.CreateCommand[fullStackModel]) (fullStackModel, error) {
	panic("unexpected Create call")
}

func (*fullStackService) Update(context.Context, port.UpdateCommand[string, fullStackModel]) (fullStackModel, error) {
	panic("unexpected Update call")
}

func (*fullStackService) Replace(context.Context, port.ReplaceCommand[string, fullStackModel]) (fullStackModel, error) {
	panic("unexpected Replace call")
}

func (*fullStackService) Delete(context.Context, port.DeleteCommand[string]) (int64, error) {
	panic("unexpected Delete call")
}

func (*fullStackService) DeleteMany(context.Context, port.BulkDeleteCommand[string]) (int64, error) {
	panic("unexpected DeleteMany call")
}

func TestFullStackHTTPServiceCRUDSQLUsesOneTraceTree(t *testing.T) {
	const (
		serviceName    = "full-stack-fixture"
		secretPathID   = "full-stack-path-secret-4815"
		secretSQL      = "INSERT INTO full_stack_secret_table_4815 (value) VALUES (?)"
		secretArgument = "full-stack-argument-secret-90217"
	)
	routes := NewHTTPRoutes()
	var service port.Service[fullStackModel, string, fullStackModel]
	var handlerCalls int
	var handlerContext context.Context
	var handlerResult fullStackModel
	var handlerErr error
	var writeErr error
	if err := routes.HandleFunc("GET /items/{id}", func(writer http.ResponseWriter, request *http.Request) {
		handlerCalls++
		handlerContext = request.Context()
		handlerResult, handlerErr = service.Get(request.Context(), port.GetCommand[string]{ID: request.PathValue("id")})
		if handlerErr != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set("X-Full-Stack", "exact-result")
		writer.WriteHeader(http.StatusCreated)
		_, writeErr = fmt.Fprintf(writer, "%s:%d:%d", handlerResult.ID, handlerResult.RowsAffected, handlerResult.LastInsertID)
	}); err != nil {
		t.Fatal(err)
	}

	allowedResource := attribute.String("service.name", serviceName)
	transportPolicy := TraceProjectionPolicy{
		HTTPRoutes:         routes,
		ResourceAttributes: []attribute.KeyValue{allowedResource},
	}
	databasePolicy := DatabaseProjectionPolicy{
		ResourceAttributes: []attribute.KeyValue{allowedResource},
	}
	spanSink := tracetest.NewInMemoryExporter()
	databaseSpanExporter, err := NewDatabaseSpanExporter(spanSink, databasePolicy)
	if err != nil {
		t.Fatal(err)
	}
	spanExporter, err := NewTransportSpanExporter(databaseSpanExporter, transportPolicy)
	if err != nil {
		t.Fatal(err)
	}
	telemetryResource := resource.NewSchemaless(allowedResource)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(spanExporter),
		sdktrace.WithResource(telemetryResource),
	)
	metricSink := &metricCapture{}
	databaseMetricExporter, err := NewDatabaseMetricExporter(metricSink, databasePolicy)
	if err != nil {
		t.Fatal(err)
	}
	metricExporter, err := NewTransportMetricExporter(databaseMetricExporter, transportPolicy)
	if err != nil {
		t.Fatal(err)
	}
	reader := sdkmetric.NewPeriodicReader(metricExporter)
	metricOptions := []sdkmetric.Option{
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(telemetryResource),
	}
	transportViews, err := TransportMetricOptions(transportPolicy)
	if err != nil {
		t.Fatal(err)
	}
	databaseViews, err := DatabaseMetricOptions()
	if err != nil {
		t.Fatal(err)
	}
	metricOptions = append(metricOptions, transportViews...)
	metricOptions = append(metricOptions, databaseViews...)
	meterProvider := sdkmetric.NewMeterProvider(metricOptions...)
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(context.Background())
		_ = tracerProvider.Shutdown(context.Background())
	})
	providers := Providers{Tracer: tracerProvider, Meter: meterProvider}

	database, err := OpenSQL(providers, "sqlite", filepath.Join(t.TempDir(), "full-stack.db"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(0)
	t.Cleanup(func() { _ = database.Close() })
	if _, err = database.ExecContext(context.Background(), "CREATE TABLE full_stack_secret_table_4815 (id INTEGER PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	spanSink.Reset()

	telemetry, err := vvotel.New(vvotel.Config{
		TracerProvider: tracerProvider,
		MeterProvider:  meterProvider,
		ResourceName:   vvotel.ApprovedName("items"),
	})
	if err != nil {
		t.Fatal(err)
	}
	recordingSource := &fullStackSQLSource{inner: crudsql.SQLite(database)}
	instrumentedSource := vvotel.Source(telemetry, recordingSource)
	rawService := &fullStackService{
		source:   instrumentedSource,
		meta:     &crud.Meta{},
		query:    secretSQL,
		argument: secretArgument,
	}
	service = vvotel.WrapService(telemetry, rawService)
	handler, err := HTTPServer(providers, routes, TrustedIngress)
	if err != nil {
		t.Fatal(err)
	}

	marker := new(int)
	request := httptest.NewRequest(http.MethodGet, "http://full-stack.example/items/"+secretPathID, nil)
	request = request.WithContext(context.WithValue(request.Context(), fullStackContextKey{}, marker))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	wantResult := fullStackModel{ID: secretPathID, RowsAffected: 1, LastInsertID: 1}
	wantSourceResult := crud.Result{RowsAffected: 1, LastInsertID: 1, HasLastInsertID: true}
	if response.Code != http.StatusCreated || response.Header().Get("X-Full-Stack") != "exact-result" || response.Body.String() != secretPathID+":1:1" {
		t.Fatalf("response=%d/%q/%q", response.Code, response.Header().Get("X-Full-Stack"), response.Body.String())
	}
	if handlerCalls != 1 || handlerErr != nil || writeErr != nil || handlerResult != wantResult {
		t.Fatalf("handler calls/result/errors=%d/%#v/%v/%v", handlerCalls, handlerResult, handlerErr, writeErr)
	}
	if rawService.calls != 1 || rawService.lastCommand.ID != secretPathID {
		t.Fatalf("service calls/command=%d/%#v", rawService.calls, rawService.lastCommand)
	}
	if recordingSource.calls != 1 || recordingSource.lastQuery != secretSQL || !reflect.DeepEqual(recordingSource.lastArgs, []any{secretArgument}) {
		t.Fatalf("source calls/query/args=%d/%q/%#v", recordingSource.calls, recordingSource.lastQuery, recordingSource.lastArgs)
	}
	if recordingSource.err != nil || recordingSource.result != wantSourceResult {
		t.Fatalf("source result/error=%#v/%v", recordingSource.result, recordingSource.err)
	}
	if handlerContext.Value(fullStackContextKey{}) != marker || rawService.lastContext.Value(fullStackContextKey{}) != marker || recordingSource.lastContext.Value(fullStackContextKey{}) != marker {
		t.Fatal("application context value did not survive the complete call chain")
	}

	spans := spanSink.GetSpans()
	if len(spans) != 4 {
		t.Fatalf("spans=%d, want one boundary span per layer: %v", len(spans), spanNamesAndKinds(spans))
	}
	serverSpan := findSpan(t, spans, "GET /items/{id}", trace.SpanKindServer)
	commandSpan := findSpan(t, spans, "vv.command get", trace.SpanKindInternal)
	crudSpan := findSpan(t, spans, "vv.crud_source exec", trace.SpanKindInternal)
	databaseSpan := findSpan(t, spans, "db.exec", trace.SpanKindClient)
	if serverSpan.Parent.IsValid() {
		t.Fatalf("server parent=%v, want root", serverSpan.Parent)
	}
	assertFullStackChild(t, commandSpan, serverSpan)
	assertFullStackChild(t, crudSpan, commandSpan)
	assertFullStackChild(t, databaseSpan, crudSpan)
	assertFullStackContextSpan(t, handlerContext, serverSpan)
	assertFullStackContextSpan(t, rawService.lastContext, commandSpan)
	assertFullStackContextSpan(t, recordingSource.lastContext, crudSpan)

	if err := meterProvider.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	metrics := metricSink.snapshot()
	for _, signal := range []struct {
		scope string
		name  string
	}{
		{otelhttp.ScopeName, "http.server.request.duration"},
		{vvotel.ScopeName, vvotel.MetricCommandDuration},
		{vvotel.ScopeName, vvotel.MetricCrudSourceDuration},
		{sqlScopeName, "db.client.operation.duration"},
	} {
		if !hasMetric(metricSink, signal.scope, signal.name) {
			t.Fatalf("shared SDK did not export %s/%s", signal.scope, signal.name)
		}
	}
	assertFullStackMetricExemplar(t, metrics, otelhttp.ScopeName, "http.server.request.duration", serverSpan.SpanContext)
	assertFullStackMetricExemplar(t, metrics, vvotel.ScopeName, vvotel.MetricCommandDuration, commandSpan.SpanContext)
	assertFullStackMetricExemplar(t, metrics, vvotel.ScopeName, vvotel.MetricCrudSourceDuration, crudSpan.SpanContext)
	assertFullStackMetricExemplar(t, metrics, sqlScopeName, "db.client.operation.duration", crudSpan.SpanContext)
	assertNativePrivacy(t, spans, metrics, secretPathID, secretSQL, secretArgument)
}

func assertFullStackChild(t *testing.T, child, parent tracetest.SpanStub) {
	t.Helper()
	if child.Parent.TraceID() != parent.SpanContext.TraceID() || child.Parent.SpanID() != parent.SpanContext.SpanID() {
		t.Fatalf("%s parent=%v, want %s=%v", child.Name, child.Parent, parent.Name, parent.SpanContext)
	}
}

func assertFullStackContextSpan(t *testing.T, ctx context.Context, span tracetest.SpanStub) {
	t.Helper()
	current := trace.SpanContextFromContext(ctx)
	if current.TraceID() != span.SpanContext.TraceID() || current.SpanID() != span.SpanContext.SpanID() {
		t.Fatalf("context span=%v, want %s=%v", current, span.Name, span.SpanContext)
	}
}

func assertFullStackMetricExemplar(t *testing.T, metrics metricdata.ResourceMetrics, scope, name string, spanContext trace.SpanContext) {
	t.Helper()
	var measurements []metricdata.Metrics
	for _, scoped := range metrics.ScopeMetrics {
		if scoped.Scope.Name != scope {
			continue
		}
		for _, measurement := range scoped.Metrics {
			if measurement.Name == name {
				measurements = append(measurements, measurement)
			}
		}
	}
	if len(measurements) != 1 {
		t.Fatalf("metric %s/%s matches=%d", scope, name, len(measurements))
	}
	measurement := measurements[0]
	histogram, ok := measurement.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("metric %s/%s data=%T", scope, name, measurement.Data)
	}
	wantTraceID := spanContext.TraceID()
	wantSpanID := spanContext.SpanID()
	for _, point := range histogram.DataPoints {
		for _, exemplar := range point.Exemplars {
			if bytes.Equal(exemplar.TraceID, wantTraceID[:]) && bytes.Equal(exemplar.SpanID, wantSpanID[:]) {
				return
			}
		}
	}
	t.Fatalf("metric %s/%s has no exemplar for %s/%s", scope, name, wantTraceID, wantSpanID)
}
