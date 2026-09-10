package otelnative

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	_ "modernc.org/sqlite"
)

func TestSQLRecipeExecutesThroughOpenWithFixedNamesAndOwnedStats(t *testing.T) {
	const (
		secretTable = "secret_sql_table_4815"
		secretValue = "secret_sql_value_4815"
	)
	pool, err := NewDatabasePoolName("primary")
	if err != nil {
		t.Fatal(err)
	}
	sampler := &nameSampler{}
	fixture := newDatabaseTelemetryFixture(t, []DatabasePoolName{pool}, sampler)
	database, err := OpenSQL(fixture.providers, "sqlite", "file:secret_pool_dsn_4815?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	registration, err := RegisterSQLDBStats(fixture.providers, database, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer registration.Unregister()

	ctx, parent := fixture.providers.Tracer.Tracer("application").Start(context.Background(), "application")
	if _, err = database.ExecContext(ctx, "create table "+secretTable+" (value text)"); err != nil {
		t.Fatal(err)
	}
	statement, err := database.PrepareContext(ctx, "insert into "+secretTable+" (value) values (?)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = statement.ExecContext(ctx, secretValue); err != nil {
		t.Fatal(err)
	}
	if err = statement.Close(); err != nil {
		t.Fatal(err)
	}
	var value string
	if err = database.QueryRowContext(ctx, "select value from "+secretTable).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != secretValue {
		t.Fatalf("value=%q", value)
	}
	if err = database.QueryRowContext(ctx, "select value from "+secretTable+" where value=?", "missing").Scan(&value); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty row error=%v", err)
	}
	parent.End()
	fixture.flushMetrics(t)

	spans := fixture.databaseSpans()
	if len(spans) == 0 {
		t.Fatal("database/sql emitted no spans through the supplied tracer provider")
	}
	for _, span := range spans {
		if span.SpanKind != trace.SpanKindClient {
			t.Fatalf("span %q kind=%s", span.Name, span.SpanKind)
		}
		if span.InstrumentationScope.Name != sqlScopeName || span.InstrumentationScope.Version != "0.43.0" {
			t.Fatalf("span %q scope=%+v", span.Name, span.InstrumentationScope)
		}
		if span.Status.Code == codes.Error {
			t.Fatalf("successful/no-row path marked error: %q %+v", span.Name, span.Status)
		}
		if span.Status.Description != "" || len(span.Events) != 0 {
			t.Fatalf("span %q retained details", span.Name)
		}
	}
	for _, expected := range []string{"db.exec", "db.prepare", "db.query", "db.rows"} {
		if !slices.Contains(spanNames(spans), expected) {
			t.Fatalf("spans %v do not contain %q", spanNames(spans), expected)
		}
	}
	assertStringsExclude(t, "sampler names", sampler.snapshot(), []string{secretTable, secretValue, "secret_pool_dsn_4815"})
	if !hasMetric(fixture.metrics, sqlScopeName, "db.client.operation.duration") {
		t.Fatal("database/sql duration metric was not emitted through the supplied meter provider")
	}
	if !hasMetric(fixture.metrics, sqlScopeName, "db.sql.connection.max_open") {
		t.Fatal("database/sql pool metric was not emitted through the retained registration")
	}
	assertNativePrivacy(t, spans, fixture.metrics.snapshot(), secretTable, secretValue, "secret_pool_dsn_4815", secretResource)
	if err = registration.Unregister(); err != nil {
		t.Fatal(err)
	}
	if err = registration.Unregister(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLRecipeOpenDBAppliesNoRowsAndFailurePolicy(t *testing.T) {
	const (
		secretSQL     = "secret_query_4815"
		secretFailure = "secret_sql_failure_4815"
	)
	fixture := newDatabaseTelemetryFixture(t, nil, nil)
	connector := outcomeConnector{failure: errors.New(secretFailure)}
	database, err := OpenSQLDB(fixture.providers, connector)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx, parent := fixture.providers.Tracer.Tracer("application").Start(context.Background(), "application")
	if _, err = database.ExecContext(ctx, "no_rows "+secretSQL); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("no-row error=%v", err)
	}
	noRowsSpans := fixture.databaseSpans()
	if len(noRowsSpans) == 0 {
		t.Fatal("no-row driver path emitted no span")
	}
	for _, span := range noRowsSpans {
		if span.Status.Code == codes.Error || len(span.Events) != 0 {
			t.Fatalf("sql.ErrNoRows recorded as an error: %+v", span)
		}
	}
	if _, err = database.ExecContext(ctx, "fail "+secretSQL); !errors.Is(err, connector.failure) {
		t.Fatalf("driver error=%v", err)
	}
	parent.End()
	spans := fixture.databaseSpans()
	var failures int
	for _, span := range spans {
		if span.Status.Code == codes.Error {
			failures++
		}
		if span.Status.Description != "" || len(span.Events) != 0 {
			t.Fatalf("exported error details=%+v events=%v", span.Status, span.Events)
		}
	}
	if failures != 1 {
		t.Fatalf("failure spans=%d all=%v", failures, spanNames(spans))
	}
	assertNativePrivacy(t, spans, fixture.metrics.snapshot(), secretSQL, secretFailure)
}

func TestSQLRecipeRejectsMissingConnector(t *testing.T) {
	fixture := newDatabaseTelemetryFixture(t, nil, nil)
	if _, err := OpenSQLDB(fixture.providers, nil); !errors.Is(err, ErrInvalidSQLConnector) {
		t.Fatalf("error=%v", err)
	}
}

type outcomeConnector struct {
	failure error
}

func (c outcomeConnector) Connect(context.Context) (driver.Conn, error) {
	return outcomeConnection{failure: c.failure}, nil
}

func (c outcomeConnector) Driver() driver.Driver {
	return outcomeDriver{failure: c.failure}
}

type outcomeDriver struct {
	failure error
}

func (d outcomeDriver) Open(string) (driver.Conn, error) {
	return outcomeConnection{failure: d.failure}, nil
}

type outcomeConnection struct {
	failure error
}

func (outcomeConnection) Prepare(string) (driver.Stmt, error) {
	return nil, driver.ErrSkip
}

func (outcomeConnection) Close() error {
	return nil
}

func (outcomeConnection) Begin() (driver.Tx, error) {
	return nil, driver.ErrSkip
}

func (c outcomeConnection) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	switch {
	case strings.HasPrefix(query, "no_rows "):
		return nil, sql.ErrNoRows
	case strings.HasPrefix(query, "fail "):
		return nil, c.failure
	default:
		return driver.RowsAffected(1), nil
	}
}

func (outcomeConnection) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return emptyRows{}, nil
}

type emptyRows struct{}

func (emptyRows) Columns() []string {
	return []string{"value"}
}

func (emptyRows) Close() error {
	return nil
}

func (emptyRows) Next([]driver.Value) error {
	return io.EOF
}
