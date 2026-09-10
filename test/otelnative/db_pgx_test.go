package otelnative

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func TestPGXRecipeBoundsEveryOperationBeforeTheSDKAndAtExport(t *testing.T) {
	const (
		secretSQL      = "select secret_value_4815 from secret_table_4815 where id=$1"
		secretArgument = "secret_argument_4815"
		secretTable    = "secret_copy_table_4815"
		secretPrepared = "secret_prepared_name_4815"
		secretHost     = "secret-db-host-4815"
		secretUser     = "secret-db-user-4815"
		secretDatabase = "secret-db-name-4815"
		secretFailure  = "secret-driver-error-4815"
	)
	sampler := &nameSampler{}
	fixture := newDatabaseTelemetryFixture(t, nil, sampler)
	tracer, err := NewPGXTracer(fixture.providers)
	if err != nil {
		t.Fatal(err)
	}
	ctx, parent := fixture.providers.Tracer.Tracer("application").Start(context.Background(), "application")

	query := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: secretSQL, Args: []any{secretArgument}})
	tracer.TraceQueryEnd(query, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})

	noRows := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: secretSQL})
	tracer.TraceQueryEnd(noRows, nil, pgx.TraceQueryEndData{Err: pgx.ErrNoRows})

	failure := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: secretSQL})
	tracer.TraceQueryEnd(failure, nil, pgx.TraceQueryEndData{Err: errors.New(secretFailure)})

	prepared := tracer.TracePrepareStart(ctx, nil, pgx.TracePrepareStartData{Name: secretPrepared, SQL: secretSQL})
	tracer.TracePrepareEnd(prepared, nil, pgx.TracePrepareEndData{})

	copyFrom := tracer.TraceCopyFromStart(ctx, nil, pgx.TraceCopyFromStartData{TableName: pgx.Identifier{secretTable}})
	tracer.TraceCopyFromEnd(copyFrom, nil, pgx.TraceCopyFromEndData{CommandTag: pgconn.NewCommandTag("COPY 1")})

	batch := &pgx.Batch{}
	batch.Queue(secretSQL, secretArgument)
	batchContext := tracer.TraceBatchStart(ctx, nil, pgx.TraceBatchStartData{Batch: batch})
	tracer.TraceBatchQuery(batchContext, nil, pgx.TraceBatchQueryData{SQL: secretSQL, Args: []any{secretArgument}, CommandTag: pgconn.NewCommandTag("SELECT 1")})
	tracer.TraceBatchEnd(batchContext, nil, pgx.TraceBatchEndData{})

	config := &pgx.ConnConfig{Config: pgconn.Config{Host: secretHost, User: secretUser, Database: secretDatabase}}
	connect := tracer.TraceConnectStart(ctx, pgx.TraceConnectStartData{ConnConfig: config})
	tracer.TraceConnectEnd(connect, pgx.TraceConnectEndData{})
	parent.End()
	fixture.flushMetrics(t)

	names := sampler.snapshot()
	assertStringsExclude(t, "sampler names", names, []string{secretSQL, secretTable, secretPrepared})
	for _, expected := range []string{"db.query", "db.prepare", "db.copy", "db.batch", "db.connect"} {
		if !slices.Contains(names, expected) {
			t.Fatalf("sampler names %v do not contain %q", names, expected)
		}
	}
	spans := fixture.databaseSpans()
	if len(spans) != 8 {
		t.Fatalf("database spans=%d names=%v", len(spans), spanNames(spans))
	}
	for _, span := range spans {
		if span.SpanKind != trace.SpanKindClient {
			t.Fatalf("span %q kind=%s", span.Name, span.SpanKind)
		}
		if span.InstrumentationScope.Name != pgxScopeName || span.InstrumentationScope.Version != pgxScopeVersion {
			t.Fatalf("span %q scope=%+v", span.Name, span.InstrumentationScope)
		}
		if span.Status.Description != "" || len(span.Events) != 0 {
			t.Fatalf("span %q retained error details: status=%+v events=%v", span.Name, span.Status, span.Events)
		}
	}
	var errorsSeen int
	for _, span := range spans {
		if span.Status.Code == codes.Error {
			errorsSeen++
		}
	}
	if errorsSeen != 1 {
		t.Fatalf("error spans=%d", errorsSeen)
	}
	metrics := fixture.metrics.snapshot()
	if !hasMetric(fixture.metrics, pgxScopeName, "db.client.operation.duration") {
		t.Fatal("pgx duration metric was not emitted through the supplied meter provider")
	}
	assertNativePrivacy(t, spans, metrics,
		secretSQL, secretArgument, secretTable, secretPrepared, secretHost, secretUser, secretDatabase, secretFailure, secretResource,
	)
}

func TestPGXRecipeRejectsMissingProviders(t *testing.T) {
	if _, err := NewPGXTracer(Providers{}); !errors.Is(err, ErrInvalidProviders) {
		t.Fatalf("error=%v", err)
	}
}
