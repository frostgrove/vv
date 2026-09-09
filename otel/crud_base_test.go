package vvotel_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/codes"
)

type crudContextKey struct{}

type crudTestRows struct {
	closed bool
}

func (*crudTestRows) Next() bool        { return false }
func (*crudTestRows) Scan(...any) error { return nil }
func (*crudTestRows) Err() error        { return nil }
func (r *crudTestRows) Close()          { r.closed = true }

type crudRecordingSource struct {
	id          any
	dialect     crud.Dialect
	execResult  crud.Result
	queryResult crud.Rows
	execErr     error
	queryErr    error
	execCalls   int
	queryCalls  int
	lastCtx     context.Context
	lastQuery   string
	lastArgs    []any
}

func (s *crudRecordingSource) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	s.execCalls++
	s.lastCtx = ctx
	s.lastQuery = query
	s.lastArgs = args
	return s.execResult, s.execErr
}

func (s *crudRecordingSource) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	s.queryCalls++
	s.lastCtx = ctx
	s.lastQuery = query
	s.lastArgs = args
	return s.queryResult, s.queryErr
}

func (s *crudRecordingSource) Dialect() crud.Dialect { return s.dialect }

func (s *crudRecordingSource) DataSource() any { return s.id }

type crudRecordingTx struct {
	id            any
	execResult    crud.Result
	queryResult   crud.Rows
	execErr       error
	queryErr      error
	commitErr     error
	rollbackErr   error
	execCalls     int
	queryCalls    int
	commitCalls   int
	rollbackCalls int
	lastCtx       context.Context
}

func (tx *crudRecordingTx) Exec(ctx context.Context, _ string, _ ...any) (crud.Result, error) {
	tx.execCalls++
	tx.lastCtx = ctx
	return tx.execResult, tx.execErr
}

func (tx *crudRecordingTx) Query(ctx context.Context, _ string, _ ...any) (crud.Rows, error) {
	tx.queryCalls++
	tx.lastCtx = ctx
	return tx.queryResult, tx.queryErr
}

func (tx *crudRecordingTx) Commit(ctx context.Context) error {
	tx.commitCalls++
	tx.lastCtx = ctx
	return tx.commitErr
}

func (tx *crudRecordingTx) Rollback(ctx context.Context) error {
	tx.rollbackCalls++
	tx.lastCtx = ctx
	return tx.rollbackErr
}

func (tx *crudRecordingTx) DataSource() any { return tx.id }

type crudFullSource struct {
	*crudRecordingSource
	tx              crud.Tx
	replica         crud.Source
	beginErr        error
	bulkErr         error
	bulkResult      int64
	beginCalls      int
	readSourceCalls int
	bulkCalls       int
	bulkTarget      crud.Executor
}

func (s *crudFullSource) Begin(ctx context.Context) (crud.Tx, error) {
	s.beginCalls++
	s.lastCtx = ctx
	return s.tx, s.beginErr
}

func (s *crudFullSource) ReadSource() crud.Source {
	s.readSourceCalls++
	return s.replica
}

func (s *crudFullSource) UnsafeBulkInsert(ctx context.Context, target crud.Executor, _ crud.TableRef, _ []string, _ [][]any) (int64, error) {
	s.bulkCalls++
	s.lastCtx = ctx
	s.bulkTarget = target
	return s.bulkResult, s.bulkErr
}

type crudSourceOnlyWrapper struct{ inner crud.Source }

func (w crudSourceOnlyWrapper) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return w.inner.Exec(ctx, query, args...)
}

func (w crudSourceOnlyWrapper) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return w.inner.Query(ctx, query, args...)
}

func (w crudSourceOnlyWrapper) Dialect() crud.Dialect { return w.inner.Dialect() }

func (w crudSourceOnlyWrapper) UnwrapSource() crud.Source { return w.inner }

func TestCRUDSourceDirectCallsPreserveEffectsAndEmitClosedSignals(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}

	wantResult := crud.Result{RowsAffected: 7, LastInsertID: 11, HasLastInsertID: true}
	wantRows := &crudTestRows{}
	wantErr := crud.ErrNotFound
	source := &crudRecordingSource{
		id:          new(int),
		dialect:     crud.Postgres{},
		execResult:  wantResult,
		queryResult: wantRows,
		queryErr:    wantErr,
	}
	wrapper := vvotel.Source(tel, source)
	baseCtx := context.WithValue(context.Background(), crudContextKey{}, "retained")

	result, err := wrapper.Exec(baseCtx, "secret exec statement", "secret-argument", 19)
	if err != nil || result != wantResult {
		t.Fatalf("Exec = %#v, %v", result, err)
	}
	if source.execCalls != 1 || source.lastQuery != "secret exec statement" || !reflect.DeepEqual(source.lastArgs, []any{"secret-argument", 19}) {
		t.Fatalf("Exec calls/query/args = %d/%q/%#v", source.execCalls, source.lastQuery, source.lastArgs)
	}
	if source.lastCtx.Value(crudContextKey{}) != "retained" || source.lastCtx.Value(spanKey{}) == nil {
		t.Fatal("Exec did not receive the derived context with application values")
	}

	rows, err := wrapper.Query(baseCtx, "secret query statement", "secret-query-argument")
	if rows != wantRows || !errors.Is(err, wantErr) {
		t.Fatalf("Query = %#v, %v", rows, err)
	}
	if source.queryCalls != 1 || source.lastQuery != "secret query statement" || !reflect.DeepEqual(source.lastArgs, []any{"secret-query-argument"}) {
		t.Fatalf("Query calls/query/args = %d/%q/%#v", source.queryCalls, source.lastQuery, source.lastArgs)
	}
	if wantRows.closed {
		t.Fatal("the source decorator touched the returned rows")
	}

	if len(tp.spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(tp.spans))
	}
	if tp.spans[0].name != "vv.crud_source exec" || tp.spans[1].name != "vv.crud_source query" {
		t.Fatalf("span names = %q, %q", tp.spans[0].name, tp.spans[1].name)
	}
	if tp.spans[0].status != codes.Unset || tp.spans[1].status != codes.Error {
		t.Fatalf("span status = %v, %v", tp.spans[0].status, tp.spans[1].status)
	}
	if got := tp.spans[1].attributes[vvotel.AttrErrorType].AsString(); got != vvotel.ErrorTypeNotFound {
		t.Fatalf("query error.type = %q", got)
	}
	if mp.metricCount() != 2 {
		t.Fatalf("metrics = %d, want 2", mp.metricCount())
	}
	for _, metric := range mp.metrics {
		if metric.name != vvotel.MetricCrudSourceDuration {
			t.Fatalf("metric name = %q", metric.name)
		}
		for _, value := range metric.attributes {
			if value.AsString() == "secret exec statement" || value.AsString() == "secret query statement" || value.AsString() == "secret-argument" || value.AsString() == "secret-query-argument" {
				t.Fatalf("private statement data reached metric attributes: %#v", metric.attributes)
			}
		}
	}
}

func TestCRUDSourcePreservesPrimaryTransactionReplicaAndImmediateBulkCapabilities(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel := vvotel.Must(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	id := new(int)
	txRows := &crudTestRows{}
	tx := &crudRecordingTx{
		id:          id,
		execResult:  crud.Result{RowsAffected: 3},
		queryResult: txRows,
	}
	replica := &crudRecordingSource{id: new(int), dialect: crud.Postgres{}, execResult: crud.Result{RowsAffected: 5}}
	inner := &crudFullSource{
		crudRecordingSource: &crudRecordingSource{id: id, dialect: crud.Postgres{}},
		tx:                  tx,
		replica:             replica,
		bulkResult:          13,
	}
	wrapper := vvotel.Source(tel, inner)

	if wrapper.Dialect() != inner.dialect || crud.KeyOf(wrapper) != id {
		t.Fatalf("dialect/key = %#v/%#v", wrapper.Dialect(), crud.KeyOf(wrapper))
	}
	beginner, ok := wrapper.(crud.Beginner)
	if !ok {
		t.Fatal("Beginner was lost")
	}
	reader, ok := wrapper.(crud.ReadSourcer)
	if !ok {
		t.Fatal("ReadSourcer was lost")
	}
	bulk, ok := wrapper.(crud.UnsafeBulkInserter)
	if !ok {
		t.Fatal("UnsafeBulkInserter was lost")
	}

	wrappedTx, err := beginner.Begin(context.Background())
	if err != nil || inner.beginCalls != 1 {
		t.Fatalf("Begin calls/error = %d/%v", inner.beginCalls, err)
	}
	if found, ok := crud.ExecutorAs[*crudRecordingTx](wrappedTx); !ok || found != tx {
		t.Fatalf("wrapped transaction does not navigate to %p: %#v, %v", tx, found, ok)
	}
	if !crud.IsTransaction(wrappedTx) || crud.KeyOf(wrappedTx) != id {
		t.Fatalf("transaction state/key = %v/%#v", crud.IsTransaction(wrappedTx), crud.KeyOf(wrappedTx))
	}

	result, err := wrappedTx.Exec(context.Background(), "tx exec")
	if err != nil || result != tx.execResult {
		t.Fatalf("tx Exec = %#v, %v", result, err)
	}
	rows, err := wrappedTx.Query(context.Background(), "tx query")
	if err != nil || rows != txRows {
		t.Fatalf("tx Query = %#v, %v", rows, err)
	}
	if err := wrappedTx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := wrappedTx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tx.execCalls != 1 || tx.queryCalls != 1 || tx.commitCalls != 1 || tx.rollbackCalls != 1 {
		t.Fatalf("tx calls = exec:%d query:%d commit:%d rollback:%d", tx.execCalls, tx.queryCalls, tx.commitCalls, tx.rollbackCalls)
	}

	wrappedReplica := reader.ReadSource()
	if inner.readSourceCalls != 1 {
		t.Fatalf("ReadSource calls = %d", inner.readSourceCalls)
	}
	if unwrapped, ok := wrappedReplica.(crud.SourceUnwrapper); !ok || unwrapped.UnwrapSource() != replica {
		t.Fatalf("replica wrapper = %#v", wrappedReplica)
	}
	if _, err := wrappedReplica.Exec(context.Background(), "replica exec"); err != nil || replica.execCalls != 1 {
		t.Fatalf("replica Exec calls/error = %d/%v", replica.execCalls, err)
	}

	count, err := bulk.UnsafeBulkInsert(context.Background(), wrappedTx, crud.TableRef{Name: "events"}, []string{"id"}, [][]any{{1}})
	if err != nil || count != 13 || inner.bulkCalls != 1 || inner.bulkTarget != wrappedTx {
		t.Fatalf("bulk = %d, %v calls=%d target=%#v", count, err, inner.bulkCalls, inner.bulkTarget)
	}

	plain := vvotel.Source(tel, &crudRecordingSource{id: new(int), dialect: crud.SQLite{}})
	if _, ok := plain.(crud.Beginner); ok {
		t.Fatal("plain source gained Beginner")
	}
	if _, ok := plain.(crud.ReadSourcer); ok {
		t.Fatal("plain source gained ReadSourcer")
	}
	if _, ok := plain.(crud.UnsafeBulkInserter); ok {
		t.Fatal("plain source gained UnsafeBulkInserter")
	}

	if len(tp.spans) != 7 || mp.metricCount() != 7 {
		t.Fatalf("spans/metrics = %d/%d, want 7/7", len(tp.spans), mp.metricCount())
	}
	wantOperations := map[string]int{
		vvotel.OpCrudSourceBegin:            1,
		vvotel.OpCrudSourceExec:             2,
		vvotel.OpCrudSourceQuery:            1,
		vvotel.OpCrudSourceCommit:           1,
		vvotel.OpCrudSourceRollback:         1,
		vvotel.OpCrudSourceUnsafeBulkInsert: 1,
	}
	gotOperations := make(map[string]int)
	for _, span := range tp.spans {
		if !span.ended {
			t.Fatalf("span %q did not end", span.name)
		}
		gotOperations[span.attributes[vvotel.AttrOperationName].AsString()]++
	}
	if !reflect.DeepEqual(gotOperations, wantOperations) {
		t.Fatalf("operations = %#v, want %#v", gotOperations, wantOperations)
	}
}

func TestCRUDSourceOnlyNavigationPreservesDiscoveryWithoutGrantingBulk(t *testing.T) {
	tel := vvotel.Must(vvotel.Config{Disabled: true})
	tx := &crudRecordingTx{id: new(int)}
	replica := &crudRecordingSource{id: new(int), dialect: crud.Postgres{}}
	inner := &crudFullSource{
		crudRecordingSource: &crudRecordingSource{id: tx.id, dialect: crud.Postgres{}},
		tx:                  tx,
		replica:             replica,
	}
	wrapper := vvotel.Source(tel, crudSourceOnlyWrapper{inner: inner})

	beginner, beginOK := wrapper.(crud.Beginner)
	reader, readOK := wrapper.(crud.ReadSourcer)
	_, bulkOK := wrapper.(crud.UnsafeBulkInserter)
	if !beginOK || !readOK || bulkOK {
		t.Fatalf("capabilities beginner/read/bulk = %v/%v/%v", beginOK, readOK, bulkOK)
	}
	if _, err := beginner.Begin(context.Background()); err != nil || inner.beginCalls != 1 {
		t.Fatalf("Begin calls/error = %d/%v", inner.beginCalls, err)
	}
	if got := reader.ReadSource(); got == nil || inner.readSourceCalls != 1 {
		t.Fatalf("ReadSource = %#v, calls=%d", got, inner.readSourceCalls)
	}
}
