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
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/sqlfault"
)

// Queryer is the database/sql shape vv needs. *sql.DB, *sql.Tx, *sql.Conn,
// gorm's ConnPool, sqlx's handles and ent's execquery clients all satisfy it.
type Queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Executor turns a Queryer into a crud.Executor.
//
// faults is what turns a refused statement into an errs.Fault, and it is nil
// unless the caller named an engine. The field is an interface holding a
// pointer, so an Executor stays comparable — crud.KeyOf and catalog.Set both
// key on the value.
type Executor struct {
	q      Queryer
	faults errs.Classifier
}

// An Option wires one part of an executor.
type Option func(*config)

type config struct{ faults errs.Classifier }

// WithFaults hands an executor a classifier. It is how a handle this package
// cannot name an engine for — a joined ent, gorm or sqlx transaction — gets one
// anyway:
//
//	crudsql.From(tx, crudsql.WithFaults(sqlfault.New("postgres")))
func WithFaults(c errs.Classifier) Option { return func(o *config) { o.faults = c } }

func classifier(def errs.Classifier, opts []Option) errs.Classifier {
	o := config{faults: def}
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	return o.faults
}

// From wraps a foreign handle — most often somebody else's transaction.
//
// It names no engine and so gets no classifier: a violation through it is the
// sentinel and no code. Deriving the engine from the dialect is what [[D-046]]
// forbids, because crud.MySQL is MariaDB too. Pass WithFaults to say which.
func From(q Queryer, opts ...Option) Executor {
	return Executor{q: q, faults: classifier(nil, opts)}
}

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
		return crud.Result{}, e.conflict(err)
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
		return nil, e.conflict(err)
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
//
// It names no engine either, for the same reason From does not.
func Source(q Queryer, d crud.Dialect, opts ...Option) crud.Source {
	return source{Executor{q: q, faults: classifier(nil, opts)}, d}
}

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
//
// crud.Dialect says how to write SQL and not which server is answering —
// crud.MySQL targets MySQL and MariaDB both — so Open cannot name an engine and
// gets no classifier. The four constructors below each name theirs.
func Open(db *sql.DB, d crud.Dialect, opts ...Option) DB {
	return DB{Executor{q: db, faults: classifier(nil, opts)}, db, d, nil}
}

// The four engine shorthands. Each writes its engine string here, as a literal,
// because that string is a declaration and not something to be derived: MariaDB
// and MySQL share a driver, a dialect and a wire protocol, and answer a failed
// CHECK with two different numbers ([[D-046]]).
func Postgres(db *sql.DB, opts ...Option) DB { return engine(db, crud.Postgres{}, "postgres", opts) }
func MySQL(db *sql.DB, opts ...Option) DB    { return engine(db, crud.MySQL{}, "mysql", opts) }
func MariaDB(db *sql.DB, opts ...Option) DB  { return engine(db, crud.MySQL{}, "mariadb", opts) }
func SQLite(db *sql.DB, opts ...Option) DB   { return engine(db, crud.SQLite{}, "sqlite", opts) }

func engine(db *sql.DB, d crud.Dialect, name string, opts []Option) DB {
	return DB{Executor{q: db, faults: classifier(sqlfault.New(name), opts)}, db, d, nil}
}

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
	// The classifier goes into the transaction. A deferred constraint fires at
	// COMMIT, and a Tx without it would make that one shape of conflict a
	// sentinel with no code while the immediate shape carried one.
	return &Tx{Executor: Executor{q: tx, faults: d.faults}, tx: tx}, nil
}

// Tx is a database/sql transaction. database/sql has no nested transactions, so
// Begin on a Tx issues a SAVEPOINT instead — which is what a nested Begin means
// in practice.
type Tx struct {
	Executor
	tx    *sql.Tx
	depth atomic.Int64
}

// Commit classifies too. A DEFERRABLE INITIALLY DEFERRED constraint is checked
// here and not at the statement, so a duplicate key or a missing parent arrives
// with no statement having just failed — and returning it untouched made that
// one shape of conflict a 500 while the immediate shape was a 409.
func (t *Tx) Commit(ctx context.Context) error   { return t.conflict(t.tx.Commit()) }
func (t *Tx) Rollback(ctx context.Context) error { return t.tx.Rollback() }

// Tx returns the underlying *sql.Tx, e.g. to hand it to another library.
func (t *Tx) Tx() *sql.Tx { return t.tx }

// Begin opens a savepoint inside the transaction.
func (t *Tx) Begin(ctx context.Context) (crud.Tx, error) {
	name := "vv_sp_" + strconv.FormatInt(t.depth.Add(1), 10)
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

// Commit releases the savepoint, and classifies — though not for a deferred
// constraint. PostgreSQL hands a deferred check to the parent transaction and
// fires it at the top-level COMMIT; RELEASE returns clean, measured, and the
// other three engines have no deferred constraints at all.
//
// What is reachable here is a transaction an earlier statement poisoned:
// PostgreSQL refuses the RELEASE with 25P02, and unclassified that reaches a
// caller as an anonymous 500 instead of errs.CodeTransactionAborted. It also
// keeps the two PostgreSQL adapters in step, because crudpgx's nested Begin
// returns the same Tx whose Commit classifies and one nested write must not be a
// 409 through pgx and a 500 through database/sql.
func (s *savepoint) Commit(ctx context.Context) error {
	_, err := s.parent.tx.ExecContext(ctx, "RELEASE SAVEPOINT "+s.name)
	return s.conflict(err)
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
