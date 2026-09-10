package oteltelemetry_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/crud"
	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type sdkCRUDRows struct {
	closed bool
}

func (*sdkCRUDRows) Next() bool        { return false }
func (*sdkCRUDRows) Scan(...any) error { return nil }
func (*sdkCRUDRows) Err() error        { return nil }
func (rows *sdkCRUDRows) Close()       { rows.closed = true }

type sdkCRUDExecutor struct {
	dataSource    any
	execResult    crud.Result
	queryRows     crud.Rows
	queryErr      error
	commitErr     error
	execCalls     int
	queryCalls    int
	commitCalls   int
	rollbackCalls int
	contexts      []context.Context
	queries       []string
	arguments     [][]any
}

func (executor *sdkCRUDExecutor) Exec(ctx context.Context, query string, arguments ...any) (crud.Result, error) {
	executor.execCalls++
	executor.contexts = append(executor.contexts, ctx)
	executor.queries = append(executor.queries, query)
	executor.arguments = append(executor.arguments, arguments)
	return executor.execResult, nil
}

func (executor *sdkCRUDExecutor) Query(ctx context.Context, query string, arguments ...any) (crud.Rows, error) {
	executor.queryCalls++
	executor.contexts = append(executor.contexts, ctx)
	executor.queries = append(executor.queries, query)
	executor.arguments = append(executor.arguments, arguments)
	return executor.queryRows, executor.queryErr
}

func (executor *sdkCRUDExecutor) Commit(ctx context.Context) error {
	executor.commitCalls++
	executor.contexts = append(executor.contexts, ctx)
	return executor.commitErr
}

func (executor *sdkCRUDExecutor) Rollback(ctx context.Context) error {
	executor.rollbackCalls++
	executor.contexts = append(executor.contexts, ctx)
	return nil
}

func (executor *sdkCRUDExecutor) DataSource() any { return executor.dataSource }

type sdkCRUDSource struct {
	*sdkCRUDExecutor
	tx              crud.Tx
	replica         crud.Source
	beginCalls      int
	readSourceCalls int
	bulkCalls       int
	bulkTarget      crud.Executor
	bulkTable       crud.TableRef
	bulkColumns     []string
	bulkRows        [][]any
	bulkContext     context.Context
}

func (*sdkCRUDSource) Dialect() crud.Dialect { return crud.Postgres{} }

func (source *sdkCRUDSource) Begin(ctx context.Context) (crud.Tx, error) {
	source.beginCalls++
	source.contexts = append(source.contexts, ctx)
	return source.tx, nil
}

func (source *sdkCRUDSource) ReadSource() crud.Source {
	source.readSourceCalls++
	return source.replica
}

func (source *sdkCRUDSource) UnsafeBulkInsert(ctx context.Context, target crud.Executor, table crud.TableRef, columns []string, rows [][]any) (int64, error) {
	source.bulkCalls++
	source.bulkContext = ctx
	source.bulkTarget = target
	source.bulkTable = table
	source.bulkColumns = columns
	source.bulkRows = rows
	return 17, nil
}

func TestRealSDKCRUDCoversDirectTransactionReplicaAndBulkWithoutLeaks(t *testing.T) {
	fixture := newSDKFixture(t, sdktrace.AlwaysSample())
	tel := vvotel.Must(vvotel.Config{
		TracerProvider: fixture.tracerProvider,
		MeterProvider:  fixture.meterProvider,
		ResourceName:   vvotel.MustApproveName("articles"),
	})
	dataSource := new(int)
	queryErr := errors.New("crud-error-secret-90217")
	directRows := &sdkCRUDRows{}
	txRows := &sdkCRUDRows{}
	tx := &sdkCRUDExecutor{
		dataSource: dataSource,
		execResult: crud.Result{RowsAffected: 5},
		queryRows:  txRows,
		commitErr:  queryErr,
	}
	replica := &sdkCRUDSource{sdkCRUDExecutor: &sdkCRUDExecutor{
		dataSource: new(int),
		execResult: crud.Result{RowsAffected: 8},
	}}
	source := &sdkCRUDSource{
		sdkCRUDExecutor: &sdkCRUDExecutor{
			dataSource: dataSource,
			execResult: crud.Result{RowsAffected: 3, LastInsertID: 11, HasLastInsertID: true},
			queryRows:  directRows,
			queryErr:   queryErr,
		},
		tx:      tx,
		replica: replica,
	}
	wrapper := vvotel.Source(tel, source)
	appTracer := fixture.tracerProvider.Tracer("application")
	ctx, parent := appTracer.Start(context.Background(), "crud request", trace.WithSpanKind(trace.SpanKindServer))

	result, err := wrapper.Exec(ctx, "SELECT crud-direct-secret-18315", "crud-arg-secret-49163")
	if err != nil || result != source.execResult {
		t.Fatalf("direct Exec = %#v, %v", result, err)
	}
	rows, err := wrapper.Query(ctx, "SELECT crud-query-secret-57304", "crud-query-arg-secret-72041")
	if rows != directRows || err != queryErr {
		t.Fatalf("direct Query = %#v, %v", rows, err)
	}
	beginner, ok := wrapper.(crud.Beginner)
	if !ok {
		t.Fatal("instrumented source lost Beginner")
	}
	wrappedTx, err := beginner.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !crud.IsTransaction(wrappedTx) || crud.KeyOf(wrappedTx) != dataSource {
		t.Fatalf("wrapped transaction identity = %v/%#v", crud.IsTransaction(wrappedTx), crud.KeyOf(wrappedTx))
	}
	if got, err := wrappedTx.Exec(ctx, "UPDATE crud-tx-secret-63812", "crud-tx-arg-secret-36184"); err != nil || got != tx.execResult {
		t.Fatalf("transaction Exec = %#v, %v", got, err)
	}
	if got, err := wrappedTx.Query(ctx, "SELECT crud-tx-query-secret-51942"); err != nil || got != txRows {
		t.Fatalf("transaction Query = %#v, %v", got, err)
	}
	if err := wrappedTx.Commit(ctx); err != queryErr {
		t.Fatalf("Commit error = %v", err)
	}
	if err := wrappedTx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	readSourcer, ok := wrapper.(crud.ReadSourcer)
	if !ok {
		t.Fatal("instrumented source lost ReadSourcer")
	}
	wrappedReplica := readSourcer.ReadSource()
	if got, err := wrappedReplica.Exec(ctx, "SELECT crud-replica-secret-30258"); err != nil || got != replica.execResult {
		t.Fatalf("replica Exec = %#v, %v", got, err)
	}
	bulk, ok := wrapper.(crud.UnsafeBulkInserter)
	if !ok {
		t.Fatal("instrumented source lost UnsafeBulkInserter")
	}
	table := crud.TableRef{Name: "crud-table-secret-84716"}
	columns := []string{"crud-column-secret-16384"}
	bulkRows := [][]any{{"crud-row-secret-63850"}}
	inserted, err := bulk.UnsafeBulkInsert(ctx, wrappedTx, table, columns, bulkRows)
	parent.End()
	if err != nil || inserted != 17 {
		t.Fatalf("UnsafeBulkInsert = %d, %v", inserted, err)
	}

	if source.execCalls != 1 || source.queryCalls != 1 || source.beginCalls != 1 || source.readSourceCalls != 1 || source.bulkCalls != 1 {
		t.Fatalf("source calls = exec:%d query:%d begin:%d read:%d bulk:%d", source.execCalls, source.queryCalls, source.beginCalls, source.readSourceCalls, source.bulkCalls)
	}
	if tx.execCalls != 1 || tx.queryCalls != 1 || tx.commitCalls != 1 || tx.rollbackCalls != 1 || replica.execCalls != 1 {
		t.Fatalf("nested calls = tx exec:%d query:%d commit:%d rollback:%d replica:%d", tx.execCalls, tx.queryCalls, tx.commitCalls, tx.rollbackCalls, replica.execCalls)
	}
	parentContext := trace.SpanContextFromContext(ctx)
	for group, contexts := range map[string][]context.Context{
		"source direct and begin": source.contexts,
		"transaction":             tx.contexts,
		"replica":                 replica.contexts,
	} {
		for index, operationContext := range contexts {
			spanContext := trace.SpanContextFromContext(operationContext)
			if operationContext == ctx || !spanContext.IsValid() || spanContext.Equal(parentContext) || spanContext.TraceID() != parentContext.TraceID() {
				t.Fatalf("%s context %d is not the derived CRUD span context: %v", group, index, spanContext)
			}
		}
	}
	if source.bulkTarget != wrappedTx || source.bulkContext == ctx || trace.SpanContextFromContext(source.bulkContext).Equal(trace.SpanContextFromContext(ctx)) {
		t.Fatal("bulk target or derived operation context was not preserved")
	}
	if source.bulkTable != table || !reflect.DeepEqual(source.bulkColumns, columns) || !reflect.DeepEqual(source.bulkRows, bulkRows) {
		t.Fatal("bulk table, columns, or rows changed")
	}
	if directRows.closed || txRows.closed {
		t.Fatal("CRUD telemetry consumed or closed returned rows")
	}

	spans := fixture.spans.Ended()
	if len(spans) != 10 {
		t.Fatalf("ended spans = %d, want parent plus nine CRUD operations", len(spans))
	}
	parentSpan := findSpan(t, spans, "crud request")
	crudSpans := make([]sdktrace.ReadOnlySpan, 0, 9)
	operationCounts := make(map[string]int)
	for _, span := range spans {
		if span.Name() == "crud request" {
			continue
		}
		crudSpans = append(crudSpans, span)
		if span.SpanKind() != trace.SpanKindInternal || !span.Parent().Equal(parentSpan.SpanContext()) {
			t.Fatalf("CRUD span %q kind/parent = %s/%s", span.Name(), span.SpanKind(), span.Parent().SpanID())
		}
		attributes := attributesByName(span.Attributes())
		operation := attributes[string(vvotel.AttrOperationName)]
		operationCounts[operation]++
		if attributes[string(vvotel.AttrComponent)] != vvotel.ComponentCrudSource {
			t.Fatalf("CRUD span %q attributes = %#v", span.Name(), attributes)
		}
	}
	wantOperations := map[string]int{
		vvotel.OpCrudSourceExec:             3,
		vvotel.OpCrudSourceQuery:            2,
		vvotel.OpCrudSourceBegin:            1,
		vvotel.OpCrudSourceCommit:           1,
		vvotel.OpCrudSourceRollback:         1,
		vvotel.OpCrudSourceUnsafeBulkInsert: 1,
	}
	if !reflect.DeepEqual(operationCounts, wantOperations) {
		t.Fatalf("CRUD span operations = %#v, want %#v", operationCounts, wantOperations)
	}
	metric := findMetricData(t, collectMetricData(t, fixture.metrics), vvotel.MetricCrudSourceDuration)
	histogram, ok := metric.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("CRUD metric data = %T", metric.Data)
	}
	var samples uint64
	exemplarOperations := make(map[string]bool)
	for _, point := range histogram.DataPoints {
		samples += point.Count
		attributes := attributesByName(point.Attributes.ToSlice())
		if attributes[string(vvotel.AttrComponent)] != vvotel.ComponentCrudSource {
			t.Fatalf("CRUD metric attributes = %#v", attributes)
		}
		operation := attributes[string(vvotel.AttrOperationName)]
		matched := false
		for _, exemplar := range point.Exemplars {
			for _, span := range crudSpans {
				spanAttributes := attributesByName(span.Attributes())
				if spanAttributes[string(vvotel.AttrOperationName)] == operation && exemplarMatches(exemplar.TraceID, exemplar.SpanID, span.SpanContext()) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			t.Fatalf("CRUD metric operation %q has no exemplar for its operation span", operation)
		}
		exemplarOperations[operation] = true
	}
	if samples != 9 || !reflect.DeepEqual(histogram.DataPoints[0].Bounds, vvotel.MetricCrudSourceDurationBoundaries()) {
		t.Fatalf("CRUD samples/bounds = %d/%v", samples, histogram.DataPoints[0].Bounds)
	}
	if len(exemplarOperations) != len(wantOperations) {
		t.Fatalf("CRUD exemplar operations = %#v, want every operation in %#v", exemplarOperations, wantOperations)
	}
	assertSDKPrivacy(t, crudSpans, metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: []metricdata.Metrics{metric}}}}, []string{
		queryErr.Error(),
		"crud-direct-secret-18315",
		"crud-arg-secret-49163",
		"crud-query-secret-57304",
		"crud-query-arg-secret-72041",
		"crud-tx-secret-63812",
		"crud-tx-arg-secret-36184",
		"crud-tx-query-secret-51942",
		"crud-replica-secret-30258",
		table.Name,
		columns[0],
		bulkRows[0][0].(string),
	})
}

func attributesByName(values []attribute.KeyValue) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		result[string(value.Key)] = value.Value.Emit()
	}
	return result
}
