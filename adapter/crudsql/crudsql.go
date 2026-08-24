// Package crudsql adapts anything that speaks database/sql.
//
// One adapter covers a lot of ground, because every one of these hands out a
// type with ExecContext/QueryContext:
//
//	*sql.DB, *sql.Tx, *sql.Conn        crudsql.Open(db, crud.Postgres{})
//	sqlx                               crudsql.From(sqlxDB), crudsql.From(sqlxTx)
//	gorm                               crudsql.From(tx.Statement.ConnPool)
//	ent (--feature sql/execquery)      crudsql.From(entTx)
//	sqlc (database/sql output)         crudsql.From(tx)
//	bun, squirrel, dbr, ...            crudsql.From(tx)
//
// To share a transaction with the framework that owns it, wrap the framework's
// transaction handle and push it into the context:
//
//	ctx = crud.WithExecutor(ctx, crudsql.From(tx))
package crudsql

import (
	"context"
	"database/sql"
	"strconv"
	"sync/atomic"

	"github.com/shardit-io/vv/crud"
)

// Queryer is the database/sql shape rx-crud needs. *sql.DB, *sql.Tx, *sql.Conn,
// gorm's ConnPool, sqlx's handles and ent's execquery clients all satisfy it.
type Queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Executor turns a Queryer into a crud.Executor.
type Executor struct{ q Queryer }

// From wraps a foreign handle — most often somebody else's transaction.
func From(q Queryer) Executor { return Executor{q: q} }

// Unwrap returns the wrapped handle.
func (e Executor) Unwrap() Queryer { return e.q }

// DataSource names the database this executor speaks to, which is what
// crud.WithExecutorFor matches on. For a DB built by Open it is the *sql.DB, so
// every repository over that pool answers with the same handle however it was
// wrapped.
func (e Executor) DataSource() any { return e.q }

func (e Executor) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	res, err := e.q.ExecContext(ctx, query, args...)
	if err != nil {
		return crud.Result{}, conflict(err)
	}
	out := crud.Result{}
	if n, err := res.RowsAffected(); err == nil {
		out.RowsAffected = n
	}
	// Not every driver supports it; MySQL does and that is where we need it.
	if id, err := res.LastInsertId(); err == nil {
		out.LastInsertID, out.HasLastInsertID = id, true
	}
	return out, nil
}

func (e Executor) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	// Queries fail on integrity too: an INSERT ... RETURNING is a query.
	rs, err := e.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, conflict(err)
	}
	return rows{rs}, nil
}

// rows adapts *sql.Rows, whose Close returns an error the interface does not
// want. The outer method shadows the embedded one.
type rows struct{ *sql.Rows }

func (r rows) Close() { _ = r.Rows.Close() }

// source pairs an arbitrary Queryer with a dialect.
type source struct {
	Executor
	d crud.Dialect
}

func (s source) Dialect() crud.Dialect { return s.d }

// Source builds a crud.Source over any Queryer. Use it when a framework hands
// you a handle rather than a *sql.DB and you want to build repositories on it
// directly; use Open when you have the *sql.DB and want transactions too.
func Source(q Queryer, d crud.Dialect) crud.Source { return source{Executor{q}, d} }

// DB is a full crud.Source over a *sql.DB: executes, knows its dialect and can
// start transactions.
type DB struct {
	Executor
	db *sql.DB
	d  crud.Dialect
	// TxOptions is used by Begin; nil means the driver default.
	TxOptions *sql.TxOptions
}

// Open binds a *sql.DB to a dialect.
func Open(db *sql.DB, d crud.Dialect) DB { return DB{Executor{db}, db, d, nil} }

// Postgres and MySQL are the two shorthands.
func Postgres(db *sql.DB) DB { return Open(db, crud.Postgres{}) }
func MySQL(db *sql.DB) DB    { return Open(db, crud.MySQL{}) }
func SQLite(db *sql.DB) DB   { return Open(db, crud.SQLite{}) }

func (d DB) Dialect() crud.Dialect { return d.d }

// DB returns the underlying handle.
func (d DB) DB() *sql.DB { return d.db }

// WithTxOptions returns a copy that starts transactions with the given options.
func (d DB) WithTxOptions(o *sql.TxOptions) DB { d.TxOptions = o; return d }

// Begin starts a transaction.
func (d DB) Begin(ctx context.Context) (crud.Tx, error) {
	tx, err := d.db.BeginTx(ctx, d.TxOptions)
	if err != nil {
		return nil, err
	}
	return &Tx{Executor: Executor{tx}, tx: tx}, nil
}

// Tx is a database/sql transaction. database/sql has no nested transactions, so
// Begin on a Tx issues a SAVEPOINT instead — which is what a nested Begin means
// in practice.
type Tx struct {
	Executor
	tx    *sql.Tx
	depth atomic.Int64
}

func (t *Tx) Commit(ctx context.Context) error   { return t.tx.Commit() }
func (t *Tx) Rollback(ctx context.Context) error { return t.tx.Rollback() }

// Tx returns the underlying *sql.Tx, e.g. to hand it to another library.
func (t *Tx) Tx() *sql.Tx { return t.tx }

// Begin opens a savepoint inside the transaction.
func (t *Tx) Begin(ctx context.Context) (crud.Tx, error) {
	name := "rxcrud_sp_" + strconv.FormatInt(t.depth.Add(1), 10)
	if _, err := t.tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return nil, err
	}
	return &savepoint{Executor: t.Executor, parent: t, name: name}, nil
}

type savepoint struct {
	Executor
	parent *Tx
	name   string
}

func (s *savepoint) Commit(ctx context.Context) error {
	_, err := s.parent.tx.ExecContext(ctx, "RELEASE SAVEPOINT "+s.name)
	return err
}

func (s *savepoint) Rollback(ctx context.Context) error {
	_, err := s.parent.tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+s.name)
	return err
}

func (s *savepoint) Begin(ctx context.Context) (crud.Tx, error) { return s.parent.Begin(ctx) }

var (
	_ crud.Source     = DB{}
	_ crud.Beginner   = DB{}
	_ crud.Identified = DB{}
	_ crud.Source     = source{}
	_ crud.Identified = source{}
	_ crud.Tx         = (*Tx)(nil)
	_ crud.Beginner   = (*Tx)(nil)
	_ crud.Tx         = (*savepoint)(nil)
)
