package oteltelemetry_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudpgx"
	vvotel "github.com/frostgrove/vv/otel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type advancedPGXPool struct {
	tx         pgx.Tx
	beginCalls int
	copyCalls  int
}

func (*advancedPGXPool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (*advancedPGXPool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}

func (pool *advancedPGXPool) Begin(context.Context) (pgx.Tx, error) {
	pool.beginCalls++
	return pool.tx, nil
}

func (pool *advancedPGXPool) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	pool.copyCalls++
	return 0, nil
}

type advancedPGXTx struct {
	pgx.Tx
	child      pgx.Tx
	beginCalls int
	copyCalls  int
	table      pgx.Identifier
	columns    []string
}

func (*advancedPGXTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (*advancedPGXTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}

func (tx *advancedPGXTx) Begin(context.Context) (pgx.Tx, error) {
	tx.beginCalls++
	return tx.child, nil
}

func (*advancedPGXTx) Commit(context.Context) error { return nil }

func (*advancedPGXTx) Rollback(context.Context) error { return nil }

func (tx *advancedPGXTx) CopyFrom(_ context.Context, table pgx.Identifier, columns []string, _ pgx.CopyFromSource) (int64, error) {
	tx.copyCalls++
	tx.table = append(pgx.Identifier(nil), table...)
	tx.columns = append([]string(nil), columns...)
	return 2, nil
}

type advancedExecutorWrapper struct{ inner crud.Executor }

func (wrapper advancedExecutorWrapper) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return wrapper.inner.Exec(ctx, query, args...)
}

func (wrapper advancedExecutorWrapper) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return wrapper.inner.Query(ctx, query, args...)
}

func (wrapper advancedExecutorWrapper) UnwrapExecutor() crud.Executor { return wrapper.inner }

func TestRealSDKCRUDPreservesNestedTransactionsAndNativeCopyTargetThroughWrappers(t *testing.T) {
	fixture := newSDKFixture(t, sdktrace.AlwaysSample())
	tel := vvotel.Must(vvotel.Config{
		TracerProvider: fixture.tracerProvider,
		MeterProvider:  fixture.meterProvider,
		ResourceName:   vvotel.MustApproveName("advanced-crud"),
	})
	child := &advancedPGXTx{}
	outer := &advancedPGXTx{child: child}
	pool := &advancedPGXPool{tx: outer}
	nativeSource := crudpgx.From(pool)
	observedSource := vvotel.Source(tel, nativeSource)

	beginner, ok := observedSource.(crud.Beginner)
	if !ok {
		t.Fatal("instrumented native source lost Beginner")
	}
	observedTx, err := beginner.Begin(t.Context())
	if err != nil || pool.beginCalls != 1 {
		t.Fatalf("outer Begin = %v, calls=%d", err, pool.beginCalls)
	}
	nativeTx, ok := crudpgx.Transaction(advancedExecutorWrapper{inner: observedTx})
	if !ok || nativeTx != outer {
		t.Fatalf("native transaction = %#v, %v", nativeTx, ok)
	}
	nestedBeginner, ok := observedTx.(crud.Beginner)
	if !ok {
		t.Fatal("instrumented transaction lost nested Beginner")
	}
	nested, err := nestedBeginner.Begin(t.Context())
	if err != nil || outer.beginCalls != 1 || nested == nil {
		t.Fatalf("nested Begin = %#v, %v, calls=%d", nested, err, outer.beginCalls)
	}
	if nativeNested, found := crudpgx.Transaction(nested); !found || nativeNested != child {
		t.Fatalf("nested native transaction = %#v, %v", nativeNested, found)
	}

	bulk, ok := observedSource.(crud.UnsafeBulkInserter)
	if !ok {
		t.Fatal("instrumented immediate native source lost UnsafeBulkInserter")
	}
	table := crud.TableRef{Schema: "private_schema", Name: "private_table"}
	columns := []string{"private_column"}
	rows := [][]any{{"private_value"}, {"private_value_2"}}
	inserted, err := bulk.UnsafeBulkInsert(t.Context(), advancedExecutorWrapper{inner: observedTx}, table, columns, rows)
	if err != nil || inserted != 2 {
		t.Fatalf("UnsafeBulkInsert = %d, %v", inserted, err)
	}
	if pool.copyCalls != 0 || outer.copyCalls != 1 || child.copyCalls != 0 {
		t.Fatalf("pool/outer/child COPY calls = %d/%d/%d", pool.copyCalls, outer.copyCalls, child.copyCalls)
	}
	if !reflect.DeepEqual(outer.table, pgx.Identifier{"private_schema", "private_table"}) || !reflect.DeepEqual(outer.columns, columns) {
		t.Fatalf("native COPY table/columns = %#v/%#v", outer.table, outer.columns)
	}

	spans := fixture.spans.Ended()
	if len(spans) != 3 {
		t.Fatalf("CRUD spans = %d, want two Begin plus one unsafe_bulk", len(spans))
	}
	operations := map[string]int{}
	for _, span := range spans {
		operations[attributesByName(span.Attributes())[string(vvotel.AttrOperationName)]]++
	}
	if !reflect.DeepEqual(operations, map[string]int{
		vvotel.OpCrudSourceBegin:            2,
		vvotel.OpCrudSourceUnsafeBulkInsert: 1,
	}) {
		t.Fatalf("CRUD operations = %#v", operations)
	}
	assertSDKPrivacy(t, spans, collectMetricData(t, fixture.metrics), []string{
		table.Schema,
		table.Name,
		columns[0],
		rows[0][0].(string),
		rows[1][0].(string),
	})
}
