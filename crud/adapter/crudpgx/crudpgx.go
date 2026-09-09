package crudpgx

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs"
)

type Queryer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type copier interface {
	CopyFrom(ctx context.Context, table pgx.Identifier, columns []string, source pgx.CopyFromSource) (int64, error)
}

type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type Executor struct {
	q      Queryer
	faults errs.Classifier
}

type Option func(*config)

type config struct{ faults errs.Classifier }

func WithFaults(c errs.Classifier) Option { return func(o *config) { o.faults = c } }

func faults(options []Option) errs.Classifier {
	return configuredFaults(sqlfault.New("postgres", sqlfault.WithExtractor(sqlfault.ExtractorFunc(extract))), options)
}

func configuredFaults(def errs.Classifier, options []Option) errs.Classifier {
	o := config{faults: def}
	for _, fn := range options {
		if fn != nil {
			fn(&o)
		}
	}
	return o.faults
}

func From(q Queryer, options ...Option) Executor { return Executor{q: q, faults: faults(options)} }

func (this Executor) Unwrap() Queryer { return this.q }

func (this Executor) DataSource() any { return this.q }

func (this Executor) BindExecutor(ctx context.Context, q Queryer, options ...Option) context.Context {
	executor := Executor{q: q, faults: configuredFaults(this.faults, options)}
	return crud.BindExecutor(ctx, this, executor)
}

func (this Executor) InTransaction() bool {
	_, ok := this.q.(pgx.Tx)
	return ok
}

func (this Executor) Exec(ctx context.Context, sql string, args ...any) (crud.Result, error) {
	tag, err := this.q.Exec(ctx, sql, args...)
	if err != nil {
		return crud.Result{}, this.conflict(err)
	}
	return crud.Result{RowsAffected: tag.RowsAffected()}, nil
}

func (this Executor) Query(ctx context.Context, sql string, args ...any) (crud.Rows, error) {
	rs, err := this.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, this.conflict(err)
	}
	return rows{rs, this}, nil
}

type rows struct {
	pgx.Rows
	e Executor
}

func (this rows) Err() error { return this.e.conflict(this.Rows.Err()) }

func (this Executor) UnsafeBulkInsert(ctx context.Context, target crud.Executor, table crud.TableRef, columns []string, rows [][]any) (int64, error) {
	if target == nil {
		return this.UnsafeCopyFromTable(ctx, table, columns, rows)
	}
	executor, ok := crud.ExecutorAs[interface {
		UnsafeCopyFromTable(context.Context, crud.TableRef, []string, [][]any) (int64, error)
	}](target)
	if !ok {
		return 0, crud.ErrNoBulkInsertSupport
	}
	return executor.UnsafeCopyFromTable(ctx, table, columns, rows)
}

func (this Executor) UnsafeCopyFrom(ctx context.Context, table string, columns []string, rows [][]any) (int64, error) {
	ref, err := crud.NewTableRef(table)
	if err != nil {
		return 0, err
	}
	return this.UnsafeCopyFromTable(ctx, ref, columns, rows)
}

func (this Executor) UnsafeCopyFromTable(ctx context.Context, table crud.TableRef, columns []string, rows [][]any) (int64, error) {
	if err := table.Validate(); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	c, ok := this.q.(copier)
	if !ok {
		return 0, crud.ErrNoBulkInsertSupport
	}
	n, err := c.CopyFrom(ctx, pgx.Identifier(table.Components()), columns, pgx.CopyFromRows(rows))
	if err != nil {
		return 0, this.conflict(err)
	}
	return n, nil
}

func (this Executor) Begin(ctx context.Context) (crud.Tx, error) {
	b, ok := this.q.(beginner)
	if !ok {
		return nil, crud.ErrNoTxSupport
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return nil, err
	}

	return Tx{Executor: Executor{q: tx, faults: this.faults}, tx: tx}, nil
}

func (Executor) Dialect() crud.Dialect { return crud.Postgres{} }

func Open(q Queryer, options ...Option) Executor { return Executor{q: q, faults: faults(options)} }

type Tx struct {
	Executor
	tx pgx.Tx
}

func (this Tx) Commit(ctx context.Context) error   { return this.conflict(this.tx.Commit(ctx)) }
func (this Tx) Rollback(ctx context.Context) error { return this.tx.Rollback(ctx) }

func (this Tx) Tx() pgx.Tx { return this.tx }

func Transaction(executor crud.Executor) (pgx.Tx, bool) {
	var transaction pgx.Tx
	_, ok := crud.FindExecutor(executor, func(candidate crud.Executor) bool {
		if direct, directOK := candidate.(interface{ Tx() pgx.Tx }); directOK {
			transaction = direct.Tx()
			return transaction != nil && crud.IsTransaction(candidate)
		}
		var queryer Queryer
		switch current := candidate.(type) {
		case Executor:
			queryer = current.q
		case *Executor:
			if current != nil {
				queryer = current.q
			}
		}
		if queryer == nil {
			return false
		}
		var transactionOK bool
		transaction, transactionOK = queryer.(pgx.Tx)
		return transactionOK && transaction != nil && crud.IsTransaction(candidate)
	})
	if !ok {
		return nil, false
	}
	return transaction, true
}

func TransactionFor(ctx context.Context, source any) (pgx.Tx, bool) {
	executor, ok := crud.ExecutorFor(ctx, source)
	if !ok {
		return nil, false
	}
	return Transaction(executor)
}

var (
	_ crud.Source             = Executor{}
	_ crud.Beginner           = Executor{}
	_ crud.UnsafeBulkInserter = Executor{}
	_ crud.Identified         = Executor{}
	_ crud.Tx                 = Tx{}
)
