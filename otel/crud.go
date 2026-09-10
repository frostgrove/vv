package vvotel

import (
	"context"

	"github.com/frostgrove/vv/crud"
)

func Source(t *Telemetry, next crud.Source) crud.Source {
	if nilInterface(next) {
		return nil
	}

	decorator := &crudSourceDecorator{inner: next, tel: t}
	decorator.beginner, decorator.hasBeginner = safeCrudBeginnerOf(next)
	decorator.readSourcer, decorator.hasReadSourcer = safeCrudReadSourcerOf(next)
	decorator.bulkInserter, decorator.hasBulkInserter = crud.UnsafeBulkInserterOf(next)

	switch {
	case decorator.hasBeginner && decorator.hasReadSourcer && decorator.hasBulkInserter:
		return &crudSourceAll{crudSourceDecorator: decorator}
	case decorator.hasBeginner && decorator.hasReadSourcer:
		return &crudSourceBeginRead{crudSourceDecorator: decorator}
	case decorator.hasBeginner && decorator.hasBulkInserter:
		return &crudSourceBeginBulk{crudSourceDecorator: decorator}
	case decorator.hasReadSourcer && decorator.hasBulkInserter:
		return &crudSourceReadBulk{crudSourceDecorator: decorator}
	case decorator.hasBeginner:
		return &crudSourceBegin{crudSourceDecorator: decorator}
	case decorator.hasReadSourcer:
		return &crudSourceRead{crudSourceDecorator: decorator}
	case decorator.hasBulkInserter:
		return &crudSourceBulk{crudSourceDecorator: decorator}
	default:
		return decorator
	}
}

type crudSourceDecorator struct {
	inner           crud.Source
	tel             *Telemetry
	beginner        crud.Beginner
	readSourcer     crud.ReadSourcer
	bulkInserter    crud.UnsafeBulkInserter
	hasBeginner     bool
	hasReadSourcer  bool
	hasBulkInserter bool
}

func (d *crudSourceDecorator) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return executeCrudSource(ctx, d.tel, OpCrudSourceExec, func(next context.Context) (crud.Result, error) {
		return d.inner.Exec(next, query, args...)
	})
}

func (d *crudSourceDecorator) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return executeCrudSource(ctx, d.tel, OpCrudSourceQuery, func(next context.Context) (crud.Rows, error) {
		return d.inner.Query(next, query, args...)
	})
}

func (d *crudSourceDecorator) Dialect() crud.Dialect { return d.inner.Dialect() }

func (d *crudSourceDecorator) UnwrapSourceExecutor() crud.Source { return d.inner }

func (d *crudSourceDecorator) begin(ctx context.Context) (crud.Tx, error) {
	tx, err := executeCrudSource(ctx, d.tel, OpCrudSourceBegin, func(next context.Context) (crud.Tx, error) {
		return d.beginner.Begin(next)
	})
	if err != nil || nilInterface(tx) {
		return tx, err
	}
	return wrapCrudTransaction(d.tel, tx), nil
}

func (d *crudSourceDecorator) readSource() crud.Source {
	return Source(d.tel, d.readSourcer.ReadSource())
}

func (d *crudSourceDecorator) unsafeBulkInsert(ctx context.Context, target crud.Executor, table crud.TableRef, columns []string, rows [][]any) (int64, error) {
	return executeCrudSource(ctx, d.tel, OpCrudSourceUnsafeBulkInsert, func(next context.Context) (int64, error) {
		return d.bulkInserter.UnsafeBulkInsert(next, target, table, columns, rows)
	})
}

type crudSourceBegin struct{ *crudSourceDecorator }

func (d *crudSourceBegin) Begin(ctx context.Context) (crud.Tx, error) { return d.begin(ctx) }

type crudSourceRead struct{ *crudSourceDecorator }

func (d *crudSourceRead) ReadSource() crud.Source { return d.readSource() }

type crudSourceBulk struct{ *crudSourceDecorator }

func (d *crudSourceBulk) UnsafeBulkInsert(ctx context.Context, target crud.Executor, table crud.TableRef, columns []string, rows [][]any) (int64, error) {
	return d.unsafeBulkInsert(ctx, target, table, columns, rows)
}

type crudSourceBeginRead struct{ *crudSourceDecorator }

func (d *crudSourceBeginRead) Begin(ctx context.Context) (crud.Tx, error) { return d.begin(ctx) }

func (d *crudSourceBeginRead) ReadSource() crud.Source { return d.readSource() }

type crudSourceBeginBulk struct{ *crudSourceDecorator }

func (d *crudSourceBeginBulk) Begin(ctx context.Context) (crud.Tx, error) { return d.begin(ctx) }

func (d *crudSourceBeginBulk) UnsafeBulkInsert(ctx context.Context, target crud.Executor, table crud.TableRef, columns []string, rows [][]any) (int64, error) {
	return d.unsafeBulkInsert(ctx, target, table, columns, rows)
}

type crudSourceReadBulk struct{ *crudSourceDecorator }

func (d *crudSourceReadBulk) ReadSource() crud.Source { return d.readSource() }

func (d *crudSourceReadBulk) UnsafeBulkInsert(ctx context.Context, target crud.Executor, table crud.TableRef, columns []string, rows [][]any) (int64, error) {
	return d.unsafeBulkInsert(ctx, target, table, columns, rows)
}

type crudSourceAll struct{ *crudSourceDecorator }

func (d *crudSourceAll) Begin(ctx context.Context) (crud.Tx, error) { return d.begin(ctx) }

func (d *crudSourceAll) ReadSource() crud.Source { return d.readSource() }

func (d *crudSourceAll) UnsafeBulkInsert(ctx context.Context, target crud.Executor, table crud.TableRef, columns []string, rows [][]any) (int64, error) {
	return d.unsafeBulkInsert(ctx, target, table, columns, rows)
}

type crudTransactionDecorator struct {
	inner      crud.Tx
	tel        *Telemetry
	identified crud.Identified
}

func wrapCrudTransaction(t *Telemetry, tx crud.Tx) crud.Tx {
	decorator := &crudTransactionDecorator{inner: tx, tel: t}
	decorator.identified, _ = tx.(crud.Identified)
	beginner, ok := safeCrudBeginnerOf(tx)
	switch {
	case ok && !nilInterface(decorator.identified):
		return &crudNestedIdentifiedTransactionDecorator{crudNestedTransactionDecorator: &crudNestedTransactionDecorator{
			crudTransactionDecorator: decorator,
			beginner:                 beginner,
		}}
	case ok:
		return &crudNestedTransactionDecorator{crudTransactionDecorator: decorator, beginner: beginner}
	case !nilInterface(decorator.identified):
		return &crudIdentifiedTransactionDecorator{crudTransactionDecorator: decorator}
	default:
		return decorator
	}
}

func safeCrudBeginnerOf(value any) (beginner crud.Beginner, ok bool) {
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		beginner = nil
		ok = false
	}()
	beginner, ok = crud.BeginnerOf(value)
	completed = true
	return beginner, ok
}

func safeCrudReadSourcerOf(value any) (reader crud.ReadSourcer, ok bool) {
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		reader = nil
		ok = false
	}()
	reader, ok = crud.ReadSourcerOf(value)
	completed = true
	return reader, ok
}

func (d *crudTransactionDecorator) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return executeCrudSource(ctx, d.tel, OpCrudSourceExec, func(next context.Context) (crud.Result, error) {
		return d.inner.Exec(next, query, args...)
	})
}

func (d *crudTransactionDecorator) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return executeCrudSource(ctx, d.tel, OpCrudSourceQuery, func(next context.Context) (crud.Rows, error) {
		return d.inner.Query(next, query, args...)
	})
}

func (d *crudTransactionDecorator) Commit(ctx context.Context) error {
	_, err := executeCrudSource(ctx, d.tel, OpCrudSourceCommit, func(next context.Context) (struct{}, error) {
		return struct{}{}, d.inner.Commit(next)
	})
	return err
}

func (d *crudTransactionDecorator) Rollback(ctx context.Context) error {
	_, err := executeCrudSource(ctx, d.tel, OpCrudSourceRollback, func(next context.Context) (struct{}, error) {
		return struct{}{}, d.inner.Rollback(next)
	})
	return err
}

func (d *crudTransactionDecorator) UnwrapExecutor() crud.Executor { return d.inner }

type crudIdentifiedTransactionDecorator struct{ *crudTransactionDecorator }

func (d *crudIdentifiedTransactionDecorator) DataSource() any {
	return d.identified.DataSource()
}

type crudNestedTransactionDecorator struct {
	*crudTransactionDecorator
	beginner crud.Beginner
}

func (d *crudNestedTransactionDecorator) Begin(ctx context.Context) (crud.Tx, error) {
	tx, err := executeCrudSource(ctx, d.tel, OpCrudSourceBegin, func(next context.Context) (crud.Tx, error) {
		return d.beginner.Begin(next)
	})
	if err != nil || nilInterface(tx) {
		return tx, err
	}
	return wrapCrudTransaction(d.tel, tx), nil
}

type crudNestedIdentifiedTransactionDecorator struct {
	*crudNestedTransactionDecorator
}

func (d *crudNestedIdentifiedTransactionDecorator) DataSource() any {
	return d.identified.DataSource()
}

func executeCrudSource[T any](ctx context.Context, t *Telemetry, operation string, fn func(context.Context) (T, error)) (T, error) {
	if t == nil {
		return fn(ctx)
	}
	tracer := t.tracer
	if !t.signalEnabled(SignalCrudSourceSpan) {
		tracer = nil
	}
	spanName, _ := SpanCrudSourceName(operation)
	return executeOperation(ctx, operationSpec{
		tracer:           tracer,
		histogram:        t.float64Histogram(SignalCrudSourceDuration),
		spanName:         spanName,
		operation:        operation,
		classifyError:    classifyCommandError,
		spanAttributes:   crudSourceSpanAttributes,
		metricAttributes: crudSourceMetricAttributes,
	}, fn)
}

var (
	_ crud.Source                  = (*crudSourceDecorator)(nil)
	_ crud.SourceExecutorUnwrapper = (*crudSourceDecorator)(nil)
	_ crud.Tx                      = (*crudTransactionDecorator)(nil)
	_ crud.ExecutorUnwrapper       = (*crudTransactionDecorator)(nil)
	_ crud.Identified              = (*crudIdentifiedTransactionDecorator)(nil)
	_ crud.Beginner                = (*crudNestedTransactionDecorator)(nil)
	_ crud.Identified              = (*crudNestedIdentifiedTransactionDecorator)(nil)
	_ crud.Beginner                = (*crudNestedIdentifiedTransactionDecorator)(nil)
)
