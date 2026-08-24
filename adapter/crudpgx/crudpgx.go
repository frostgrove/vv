// Package crudpgx adapts pgx v5.
//
// pgx.Rows already satisfies crud.Rows, so rows cross the boundary without
// being copied; the one method that is wrapped is Err, because that is where
// pgx reports a statement it refused. Anything with pgx's Exec/Query pair works
// — *pgxpool.Pool, *pgx.Conn and pgx.Tx alike:
//
//	src := crudpgx.Open(pool)
//	users := Users.Bind(src)
//
//	// join a transaction pgx (or sqlc-on-pgx) owns
//	tx, _ := pool.Begin(ctx)
//	ctx = crud.WithExecutor(ctx, crudpgx.From(tx))
package crudpgx

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/shardit-io/rx/crud"
)

// Queryer is the pgx shape: *pgxpool.Pool, *pgx.Conn and pgx.Tx all satisfy it.
type Queryer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// copier is the optional COPY fast path.
type copier interface {
	CopyFrom(ctx context.Context, table pgx.Identifier, columns []string, src pgx.CopyFromSource) (int64, error)
}

// beginner is implemented by pools, connections and transactions; a nested
// Begin on pgx.Tx is a SAVEPOINT, so nested transactions just work.
type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Executor turns any pgx handle into a crud.Executor.
type Executor struct{ q Queryer }

// From wraps a pgx handle — typically a transaction owned by somebody else.
func From(q Queryer) Executor { return Executor{q: q} }

// Unwrap returns the wrapped handle.
func (e Executor) Unwrap() Queryer { return e.q }

// DataSource names the database this executor speaks to, which is what
// crud.WithExecutorFor matches on — the *pgxpool.Pool for a source built by
// Open.
func (e Executor) DataSource() any { return e.q }

func (e Executor) Exec(ctx context.Context, sql string, args ...any) (crud.Result, error) {
	tag, err := e.q.Exec(ctx, sql, args...)
	if err != nil {
		return crud.Result{}, conflict(err)
	}
	return crud.Result{RowsAffected: tag.RowsAffected()}, nil
}

func (e Executor) Query(ctx context.Context, sql string, args ...any) (crud.Rows, error) {
	// Queries fail on integrity too: an INSERT ... RETURNING is a query.
	rs, err := e.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, conflict(err)
	}
	return rows{rs}, nil
}

// rows exists for one reason: pgx does not report a failed statement from
// Query. It hands back a live Rows whose first Next is false and whose Err
// carries the PgError — so on PostgreSQL, where every insert and every update
// runs as INSERT/UPDATE ... RETURNING, Err is the only place the classification
// can happen. Without it a duplicate key reached the client as a bare 500 while
// the same statement through database/sql answered 409.
type rows struct{ pgx.Rows }

func (r rows) Err() error { return conflict(r.Rows.Err()) }

// CopyFrom implements crud.BulkInserter when the underlying handle supports
// COPY, which every pool, connection and transaction does.
func (e Executor) CopyFrom(ctx context.Context, table string, columns []string, rows [][]any) (int64, error) {
	c, ok := e.q.(copier)
	if !ok {
		return 0, crud.ErrNoTxSupport
	}
	return c.CopyFrom(ctx, pgx.Identifier{table}, columns, pgx.CopyFromRows(rows))
}

// Begin starts a transaction, or a savepoint when the handle already is one.
func (e Executor) Begin(ctx context.Context) (crud.Tx, error) {
	b, ok := e.q.(beginner)
	if !ok {
		return nil, crud.ErrNoTxSupport
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return Tx{Executor: Executor{tx}, tx: tx}, nil
}

// Dialect makes any wrapped handle a full crud.Source.
func (Executor) Dialect() crud.Dialect { return crud.Postgres{} }

// Open binds a pool (or connection) as a datasource.
func Open(q Queryer) Executor { return Executor{q: q} }

// Tx is a pgx transaction. A nested Begin issues a SAVEPOINT, courtesy of pgx.
type Tx struct {
	Executor
	tx pgx.Tx
}

func (t Tx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t Tx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }

// Tx returns the underlying pgx.Tx, e.g. to hand to sqlc.
func (t Tx) Tx() pgx.Tx { return t.tx }

var (
	_ crud.Source       = Executor{}
	_ crud.Beginner     = Executor{}
	_ crud.BulkInserter = Executor{}
	_ crud.Identified   = Executor{}
	_ crud.Tx           = Tx{}
)
