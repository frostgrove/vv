// Package crud holds the contracts and value types shared by every layer of
// rx-crud: the datasource seam (Executor/Source/Dialect), the model metadata
// reader, the predicate AST, pagination types and the three-state Opt.
//
// It has zero dependencies outside the standard library — deliberately. Only
// two things ever cross the abstraction boundary: "run this statement" and
// "give me rows". Scanning stays with the mapper, dialect stays with the
// repository. That is why any foreign transaction can be pushed into a context:
// all rx-crud asks of it is Exec and Query.
package crud

import "context"

// Rows is the minimal cursor rx-crud needs. pgx.Rows satisfies it as-is;
// *sql.Rows needs a two-line wrapper because its Close returns an error.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

// Result is what a statement reports back. LastInsertID is only meaningful on
// drivers that expose it (MySQL); PostgreSQL uses RETURNING instead.
type Result struct {
	RowsAffected    int64
	LastInsertID    int64
	HasLastInsertID bool
}

// Executor is everything rx-crud requires from a connection, a pool or a
// foreign transaction.
type Executor interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// Tx is an Executor with a lifetime.
type Tx interface {
	Executor
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Beginner is implemented by executors that can start their own transaction.
// It is optional: an Executor handed over by a foreign framework usually is not
// one, and rx-crud simply joins whatever transaction it was given.
type Beginner interface {
	Begin(ctx context.Context) (Tx, error)
}

// Source is what an adapter returns: an executor that also knows its dialect.
type Source interface {
	Executor
	Dialect() Dialect
}

// BulkInserter is an optional fast path (pgx COPY). Adapters that implement it
// get used automatically; the core never learns about the driver.
type BulkInserter interface {
	CopyFrom(ctx context.Context, table string, columns []string, rows [][]any) (int64, error)
}

type ctxKey struct{}

// WithExecutor pushes a foreign executor (usually somebody else's transaction)
// into the context. Every repository call made with that context runs on it.
// This is the single interop point of the whole library.
func WithExecutor(ctx context.Context, e Executor) context.Context {
	return context.WithValue(ctx, ctxKey{}, e)
}

// ExecutorFrom returns the executor bound to ctx, if any.
func ExecutorFrom(ctx context.Context) (Executor, bool) {
	e, ok := ctx.Value(ctxKey{}).(Executor)
	return e, ok
}

// InTx runs fn inside a transaction of src. If ctx already carries a foreign
// executor, fn simply joins it — no nested transaction is started and the outer
// owner keeps control of commit and rollback.
//
// Use this to span several repositories with one transaction:
//
//	crud.InTx(ctx, db, func(ctx context.Context) error {
//	    if err := users.Save(ctx, &u); err != nil { return err }
//	    return orders.Save(ctx, &o)
//	})
func InTx(ctx context.Context, src Executor, fn func(context.Context) error) (err error) {
	if _, ok := ExecutorFrom(ctx); ok {
		return fn(ctx)
	}
	b, ok := src.(Beginner)
	if !ok {
		return ErrNoTxSupport
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()
	if err := fn(WithExecutor(ctx, tx)); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return errJoin(err, rbErr)
		}
		return err
	}
	return tx.Commit(ctx)
}
