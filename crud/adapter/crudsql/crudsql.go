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

type Queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type Executor struct {
	q           Queryer
	faults      errs.Classifier
	transaction bool
}

type Option func(*config)

type config struct {
	faults      errs.Classifier
	transaction bool
}

func WithFaults(c errs.Classifier) Option { return func(o *config) { o.faults = c } }

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

func From(q Queryer, options ...Option) Executor {
	return executor(q, nil, options)
}

func (this Executor) Unwrap() Queryer { return this.q }

func (this Executor) DataSource() any { return this.q }

func (this Executor) InTransaction() bool {
	if nilQueryer(this.q) {
		return false
	}
	if this.transaction {
		return true
	}

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

	if id, err := response.LastInsertId(); err == nil {
		out.LastInsertID, out.HasLastInsertID = id, true
	}
	return out, nil
}

func (this Executor) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	rs, err := this.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, this.conflict(err)
	}
	return rows{rs}, nil
}

type rows struct{ *sql.Rows }

func (this rows) Close() { _ = this.Rows.Close() }

type source struct {
	Executor
	d crud.Dialect
}

func (this source) Dialect() crud.Dialect { return this.d }

func Source(q Queryer, d crud.Dialect, options ...Option) crud.Source {
	return source{executor(q, nil, options), d}
}

type DB struct {
	Executor
	database *sql.DB
	d        crud.Dialect

	txOptions *sql.TxOptions
}

func Open(database *sql.DB, d crud.Dialect, options ...Option) DB {
	return DB{executor(database, nil, options), database, d, nil}
}

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

func (this DB) DB() *sql.DB { return this.database }

func (this DB) BindExecutor(ctx context.Context, q Queryer, options ...Option) context.Context {
	bound := executor(q, this.faults, options)
	return crud.BindExecutor(ctx, this, bound)
}

func (this DB) WithTxOptions(o *sql.TxOptions) DB {
	if o == nil {
		this.txOptions = nil
		return this
	}
	options := *o
	this.txOptions = &options
	return this
}

func (this DB) Begin(ctx context.Context) (crud.Tx, error) {
	tx, err := this.database.BeginTx(ctx, this.txOptions)
	if err != nil {
		return nil, err
	}

	return &Tx{Executor: Executor{q: tx, faults: this.faults}, tx: tx}, nil
}

type Tx struct {
	Executor
	tx    *sql.Tx
	depth atomic.Int64
}

func (this *Tx) Commit(ctx context.Context) error   { return this.conflict(this.tx.Commit()) }
func (this *Tx) Rollback(ctx context.Context) error { return this.tx.Rollback() }

func (this *Tx) Tx() *sql.Tx { return this.tx }

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
