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

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs"
)

// Queryer is the pgx shape: *pgxpool.Pool, *pgx.Conn and pgx.Tx all satisfy it.
type Queryer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// copier is the optional COPY fast path.
type copier interface {
	CopyFrom(ctx context.Context, table pgx.Identifier, columns []string, source pgx.CopyFromSource) (int64, error)
}

// beginner is implemented by pools, connections and transactions; a nested
// Begin on pgx.Tx is a SAVEPOINT, so nested transactions just work.
type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Executor turns any pgx handle into a crud.Executor.
type Executor struct {
	q      Queryer
	faults errs.Classifier
}

// An Option wires one part of an executor.
type Option func(*config)

type config struct{ faults errs.Classifier }

// WithFaults replaces the classifier — a vocabulary of the caller's own, or a
// catalog to fill in the columns a unique violation does not name:
//
//	crudpgx.Open(pool, crudpgx.WithFaults(sqlfault.New("postgres",
//		sqlfault.WithColumns(sqlfault.FromCatalog(cat)))))
func WithFaults(c errs.Classifier) Option { return func(o *config) { o.faults = c } }

// faults is the default, and unlike database/sql there is no ambiguity to
// refuse: pgx speaks to PostgreSQL and nothing else, so the engine is a literal
// here and derived from nothing. The typed extractor is wired in because this
// module may name *pgconn.PgError.
func faults(options []Option) errs.Classifier {
	o := config{faults: sqlfault.New("postgres", sqlfault.WithExtractor(sqlfault.ExtractorFunc(extract)))}
	for _, fn := range options {
		if fn != nil {
			fn(&o)
		}
	}
	return o.faults
}

// From wraps a pgx handle — typically a transaction owned by somebody else.
func From(q Queryer, options ...Option) Executor { return Executor{q: q, faults: faults(options)} }

// Unwrap returns the wrapped handle.
func (this Executor) Unwrap() Queryer { return this.q }

// DataSource names the database this executor speaks to, which is what
// crud.WithExecutorFor matches on — the *pgxpool.Pool for a source built by
// Open.
func (this Executor) DataSource() any { return this.q }

// InTransaction reports whether this wrapper holds pgx's transaction handle.
// Pools and connections can Begin but are not themselves transactional.
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
	// Queries fail on integrity too: an INSERT ... RETURNING is a query.
	rs, err := this.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, this.conflict(err)
	}
	return rows{rs, this}, nil
}

// rows exists for one reason: pgx does not report a failed statement from
// Query. It hands back a live Rows whose first Next is false and whose Err
// carries the PgError — so on PostgreSQL, where every insert and every update
// runs as INSERT/UPDATE ... RETURNING, Err is the only place the classification
// can happen. Without it a duplicate key reached the client as a bare 500 while
// the same statement through database/sql answered 409.
type rows struct {
	pgx.Rows
	e Executor
}

func (this rows) Err() error { return this.e.conflict(this.Rows.Err()) }

// CopyFrom implements crud.BulkInserter when the underlying handle supports
// COPY, which every pool, connection and transaction does.
func (this Executor) CopyFrom(ctx context.Context, table string, columns []string, rows [][]any) (int64, error) {
	c, ok := this.q.(copier)
	if !ok {
		return 0, crud.ErrNoTxSupport
	}
	return c.CopyFrom(ctx, pgx.Identifier{table}, columns, pgx.CopyFromRows(rows))
}

// Begin starts a transaction, or a savepoint when the handle already is one.
func (this Executor) Begin(ctx context.Context) (crud.Tx, error) {
	b, ok := this.q.(beginner)
	if !ok {
		return nil, crud.ErrNoTxSupport
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return nil, err
	}
	// The classifier goes into the transaction. A deferred constraint fires at
	// COMMIT, and a Tx without it would make that one shape of conflict a
	// sentinel with no code while the immediate shape carried one.
	return Tx{Executor: Executor{q: tx, faults: this.faults}, tx: tx}, nil
}

// Dialect makes any wrapped handle a full crud.Source.
func (Executor) Dialect() crud.Dialect { return crud.Postgres{} }

// Open binds a pool (or connection) as a datasource.
func Open(q Queryer, options ...Option) Executor { return Executor{q: q, faults: faults(options)} }

// Tx is a pgx transaction. A nested Begin issues a SAVEPOINT, courtesy of pgx.
type Tx struct {
	Executor
	tx pgx.Tx
}

// Commit classifies too. A deferred constraint fires here rather than at the
// statement, and both PostgreSQL adapters have to answer the same or the same
// write is a 409 through database/sql and a 500 through pgx.
func (this Tx) Commit(ctx context.Context) error   { return this.conflict(this.tx.Commit(ctx)) }
func (this Tx) Rollback(ctx context.Context) error { return this.tx.Rollback(ctx) }

// Tx returns the underlying pgx.Tx, e.g. to hand to sqlc.
func (this Tx) Tx() pgx.Tx { return this.tx }

var (
	_ crud.Source       = Executor{}
	_ crud.Beginner     = Executor{}
	_ crud.BulkInserter = Executor{}
	_ crud.Identified   = Executor{}
	_ crud.Tx           = Tx{}
)
