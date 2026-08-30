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
// To share a transaction with the framework that owns it, bind that handle to
// the source the repositories were built from:
//
//	ctx = source.BindExecutor(ctx, tx)
package crudsql

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"sync/atomic"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs"
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
	q           Queryer
	faults      errs.Classifier
	transaction bool
}

// An Option wires one part of an executor.
type Option func(*config)

type config struct {
	faults      errs.Classifier
	transaction bool
}

// WithFaults hands an executor a classifier. It is how a handle this package
// cannot name an engine for — a joined ent, gorm or sqlx transaction — gets one
// anyway:
//
//	crudsql.From(tx, crudsql.WithFaults(sqlfault.New("postgres")))
func WithFaults(c errs.Classifier) Option { return func(o *config) { o.faults = c } }

// WithTransaction marks an opaque foreign Queryer as a live transaction. The
// common database/sql shapes do not need it: *sql.Tx and wrappers such as
// *sqlx.Tx, *ent.Tx and gorm's prepared transaction expose Commit/Rollback and
// are recognised structurally. Use this only for a transaction wrapper that
// deliberately hides its lifecycle; marking a pool would make atomic code trust
// a boundary that does not exist.
func WithTransaction() Option { return func(o *config) { o.transaction = true } }

func configure(def errs.Classifier, options []Option) config {
	o := config{faults: def}
	for _, fn := range options {
		if fn != nil {
			fn(&o)
		}
	}
	return o
}

func executor(q Queryer, def errs.Classifier, options []Option) Executor {
	o := configure(def, options)
	return Executor{q: q, faults: o.faults, transaction: o.transaction}
}

// From wraps a foreign handle — most often somebody else's transaction.
//
// It names no engine and so gets no classifier: a violation through it is the
// sentinel and no code. Deriving the engine from the dialect is what [[D-046]]
// forbids, because crud.MySQL is MariaDB too. Pass WithFaults to say which.
func From(q Queryer, options ...Option) Executor {
	return executor(q, nil, options)
}

// Unwrap returns the wrapped handle.
func (this Executor) Unwrap() Queryer { return this.q }

// DataSource names the database this executor speaks to, which is what
// crud.WithExecutorFor matches on. For a DB built by Open it is the *sql.DB, so
// every repository over that pool answers with the same handle however it was
// wrapped.
func (this Executor) DataSource() any { return this.q }

// InTransaction reports whether this wrapper holds a database/sql transaction
// or a foreign wrapper retaining its Commit/Rollback lifecycle. A *sql.DB and a
// *sql.Conn both satisfy Queryer too, but neither provides the atomic boundary a
// multi-statement repository operation needs.
func (this Executor) InTransaction() bool {
	if nilQueryer(this.q) {
		return false
	}
	if this.transaction {
		return true
	}
	// database/sql intentionally has no transaction marker interface. The
	// lifecycle pair is the stable common denominator retained by *sql.Tx and
	// wrappers around it; pools and *sql.Conn expose neither method.
	_, ok := this.q.(interface {
		Commit() error
		Rollback() error
	})
	return ok
}

func nilQueryer(q Queryer) bool {
	if q == nil {
		return true
	}
	v := reflect.ValueOf(q)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func (this Executor) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	response, err := this.q.ExecContext(ctx, query, args...)
	if err != nil {
		return crud.Result{}, this.conflict(err)
	}
	out := crud.Result{}
	if n, err := response.RowsAffected(); err == nil {
		out.RowsAffected = n
	}
	// Not every driver supports it; MySQL does and that is where we need it.
	if id, err := response.LastInsertId(); err == nil {
		out.LastInsertID, out.HasLastInsertID = id, true
	}
	return out, nil
}

func (this Executor) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	// Queries fail on integrity too: an INSERT ... RETURNING is a query.
	rs, err := this.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, this.conflict(err)
	}
	return rows{rs}, nil
}

// rows adapts *sql.Rows, whose Close returns an error the interface does not
// want. The outer method shadows the embedded one.
type rows struct{ *sql.Rows }

func (this rows) Close() { _ = this.Rows.Close() }

// source pairs an arbitrary Queryer with a dialect.
type source struct {
	Executor
	d crud.Dialect
}

func (this source) Dialect() crud.Dialect { return this.d }

// Source builds a crud.Source over any Queryer. Use it when a framework hands
// you a handle rather than a *sql.DB and you want to build repositories on it
// directly; use Open when you have the *sql.DB and want transactions too.
//
// It names no engine either, for the same reason From does not.
func Source(q Queryer, d crud.Dialect, options ...Option) crud.Source {
	return source{executor(q, nil, options), d}
}

// DB is a full crud.Source over a *sql.DB: executes, knows its dialect and can
// start transactions.
type DB struct {
	Executor
	database *sql.DB
	d        crud.Dialect
	// txOptions is private so a configured source is immutable after build.
	// nil means the driver default.
	txOptions *sql.TxOptions
}

// Open binds a *sql.DB to a dialect.
//
// crud.Dialect says how to write SQL and not which server is answering —
// crud.MySQL targets MySQL and MariaDB both — so Open cannot name an engine and
// gets no classifier. The four constructors below each name theirs.
func Open(database *sql.DB, d crud.Dialect, options ...Option) DB {
	return DB{executor(database, nil, options), database, d, nil}
}

// The four engine shorthands. Each writes its engine string here, as a literal,
// because that string is a declaration and not something to be derived: MariaDB
// and MySQL share a driver, a dialect and a wire protocol, and answer a failed
// CHECK with two different numbers ([[D-046]]).
func Postgres(database *sql.DB, options ...Option) DB {
	return engine(database, crud.Postgres{}, "postgres", options)
}
func MySQL(database *sql.DB, options ...Option) DB {
	return engine(database, crud.MySQL{}, "mysql", options)
}
func MariaDB(database *sql.DB, options ...Option) DB {
	return engine(database, crud.MySQL{}, "mariadb", options)
}
func SQLite(database *sql.DB, options ...Option) DB {
	return engine(database, crud.SQLite{}, "sqlite", options)
}

func engine(database *sql.DB, d crud.Dialect, name string, options []Option) DB {
	return DB{executor(database, sqlfault.New(name), options), database, d, nil}
}

func (this DB) Dialect() crud.Dialect { return this.d }

// DB returns the underlying handle.
func (this DB) DB() *sql.DB { return this.database }

// BindExecutor derives a context in which repositories over this database use
// q. The DB supplies the canonical pool identity; q may be a *sql.Tx or a
// transaction handle from gorm, ent, sqlx, sqlc or another database/sql-based
// framework. The executor inherits the source's engine classifier; options may
// override it for an exceptional handle.
func (this DB) BindExecutor(ctx context.Context, q Queryer, options ...Option) context.Context {
	bound := executor(q, this.faults, options)
	return crud.BindExecutor(ctx, this, bound)
}

// WithTxOptions returns a copy that starts transactions with a snapshot of the
// given options. Keeping the caller's pointer would let a later mutation change
// a live source and race with Begin.
func (this DB) WithTxOptions(o *sql.TxOptions) DB {
	if o == nil {
		this.txOptions = nil
		return this
	}
	options := *o
	this.txOptions = &options
	return this
}

// Begin starts a transaction.
func (this DB) Begin(ctx context.Context) (crud.Tx, error) {
	tx, err := this.database.BeginTx(ctx, this.txOptions)
	if err != nil {
		return nil, err
	}
	// The classifier goes into the transaction. A deferred constraint fires at
	// COMMIT, and a Tx without it would make that one shape of conflict a
	// sentinel with no code while the immediate shape carried one.
	return &Tx{Executor: Executor{q: tx, faults: this.faults}, tx: tx}, nil
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
func (this *Tx) Commit(ctx context.Context) error   { return this.conflict(this.tx.Commit()) }
func (this *Tx) Rollback(ctx context.Context) error { return this.tx.Rollback() }

// Tx returns the underlying *sql.Tx, e.g. to hand it to another library.
func (this *Tx) Tx() *sql.Tx { return this.tx }

// Begin opens a savepoint inside the transaction.
func (this *Tx) Begin(ctx context.Context) (crud.Tx, error) {
	name := "vv_sp_" + strconv.FormatInt(this.depth.Add(1), 10)
	if _, err := this.tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return nil, err
	}
	return &savepoint{Executor: this.Executor, parent: this, name: name}, nil
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
func (this *savepoint) Commit(ctx context.Context) error {
	_, err := this.parent.tx.ExecContext(ctx, "RELEASE SAVEPOINT "+this.name)
	return this.conflict(err)
}

func (this *savepoint) Rollback(ctx context.Context) error {
	_, err := this.parent.tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+this.name)
	return err
}

func (this *savepoint) Begin(ctx context.Context) (crud.Tx, error) { return this.parent.Begin(ctx) }

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
