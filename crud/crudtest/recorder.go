// Package crudtest provides an in-memory crud.Source that records the SQL a
// repository produces and replays canned result sets back at it. It exists so
// repository behaviour — statement shape, bind order, pagination arithmetic,
// decorator composition — can be unit-tested without a database.
package crudtest

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/shardit-io/vv/crud"
)

// Statement is one recorded call.
type Statement struct {
	SQL   string
	Args  []any
	Query bool // Query rather than Exec
}

func (s Statement) String() string { return fmt.Sprintf("%s %v", s.SQL, s.Args) }

// Result is a canned query response. Values are assigned to scan destinations
// positionally, converting where Go allows and honouring sql.Scanner.
//
// Err and RowsErr are two different failures because the drivers this doubles
// for report one failure in two places. Err is Query itself refusing, which is
// database/sql's shape. RowsErr is pgx's: a statement the server refused arrives
// as a live Rows that yields what it has and then answers Err. A double that
// could only express the first cannot drive the arm a read has to end with, and
// a loop that never asks reads a truncated schema as a complete one.
type Result struct {
	Rows    [][]any
	Err     error
	RowsErr error
}

// Rows builds a Result from row literals.
func Rows(rows ...[]any) Result { return Result{Rows: rows} }

// RowsFailing builds a Result that yields its rows and then reports err from
// Rows.Err — the mid-stream failure, not the refusal Err carries.
func RowsFailing(err error, rows ...[]any) Result {
	return Result{Rows: rows, RowsErr: err}
}

// Recorder is a crud.Source that records everything and answers from a queue.
type Recorder struct {
	D crud.Dialect

	mu         sync.Mutex
	statements []Statement
	queue      []Result
	exec       crud.Result
	execErr    error
	txDepth    int
}

// New builds a recorder for a dialect.
func New(d crud.Dialect) *Recorder { return &Recorder{D: d} }

// Postgres and MySQL are the usual shorthands.
func Postgres() *Recorder { return New(crud.Postgres{}) }
func MySQL() *Recorder    { return New(crud.MySQL{}) }

func (r *Recorder) Dialect() crud.Dialect { return r.D }

// Push queues result sets, consumed by successive Query calls in order.
func (r *Recorder) Push(results ...Result) *Recorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queue = append(r.queue, results...)
	return r
}

// ExecResult sets what Exec reports back.
func (r *Recorder) ExecResult(res crud.Result) *Recorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exec = res
	return r
}

// Fail makes the next Exec return err.
func (r *Recorder) Fail(err error) *Recorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.execErr = err
	return r
}

// Statements returns everything recorded so far.
func (r *Recorder) Statements() []Statement {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Statement(nil), r.statements...)
}

// Last returns the most recent statement.
func (r *Recorder) Last() Statement {
	s := r.Statements()
	if len(s) == 0 {
		return Statement{}
	}
	return s[len(s)-1]
}

// SQL returns just the recorded statement texts.
func (r *Recorder) SQL() []string {
	out := []string{}
	for _, s := range r.Statements() {
		out = append(out, s.SQL)
	}
	return out
}

// Reset clears the recording and the queue.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statements, r.queue = nil, nil
}

// TxDepth reports how many transactions were begun.
func (r *Recorder) TxDepth() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.txDepth
}

func (r *Recorder) Exec(_ context.Context, q string, args ...any) (crud.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statements = append(r.statements, Statement{SQL: q, Args: args})
	if r.execErr != nil {
		err := r.execErr
		r.execErr = nil
		return crud.Result{}, err
	}
	return r.exec, nil
}

func (r *Recorder) Query(_ context.Context, q string, args ...any) (crud.Rows, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statements = append(r.statements, Statement{SQL: q, Args: args, Query: true})
	var res Result
	if len(r.queue) > 0 {
		res, r.queue = r.queue[0], r.queue[1:]
	}
	if res.Err != nil {
		return nil, res.Err
	}
	return &rows{data: res.Rows, err: res.RowsErr}, nil
}

// Begin hands out a transaction that records into the same recorder.
func (r *Recorder) Begin(context.Context) (crud.Tx, error) {
	r.mu.Lock()
	r.txDepth++
	r.mu.Unlock()
	return &tx{Recorder: r}, nil
}

type tx struct {
	*Recorder
	Committed  bool
	RolledBack bool
}

func (t *tx) Commit(context.Context) error   { t.Committed = true; return nil }
func (t *tx) Rollback(context.Context) error { t.RolledBack = true; return nil }

type rows struct {
	data [][]any
	i    int
	cur  []any
	err  error
}

func (r *rows) Next() bool {
	if r.i >= len(r.data) {
		return false
	}
	r.cur = r.data[r.i]
	r.i++
	return true
}

func (r *rows) Err() error { return r.err }
func (r *rows) Close()     {}

func (r *rows) Scan(dest ...any) error {
	if len(dest) != len(r.cur) {
		return fmt.Errorf("crudtest: scanning %d values into %d destinations", len(r.cur), len(dest))
	}
	for i, d := range dest {
		if err := assign(d, r.cur[i]); err != nil {
			return fmt.Errorf("crudtest: column %d: %w", i, err)
		}
	}
	return nil
}

func assign(dst, src any) error {
	if s, ok := dst.(sql.Scanner); ok {
		return s.Scan(src)
	}
	dv := reflect.ValueOf(dst)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("destination %T is not a non-nil pointer", dst)
	}
	return setValue(dv.Elem(), src)
}

// setValue writes src into dst, allocating through a pointer column on the way.
func setValue(dst reflect.Value, src any) error {
	if src == nil {
		dst.SetZero()
		return nil
	}
	if dst.Kind() == reflect.Pointer {
		p := reflect.New(dst.Type().Elem())
		if err := setValue(p.Elem(), src); err != nil {
			return err
		}
		dst.Set(p)
		return nil
	}
	sv := reflect.ValueOf(src)
	switch {
	case sv.Type().AssignableTo(dst.Type()):
		dst.Set(sv)
	case sv.Type().ConvertibleTo(dst.Type()):
		dst.Set(sv.Convert(dst.Type()))
	default:
		return fmt.Errorf("cannot assign %T to %s", src, dst.Type())
	}
	return nil
}

// Normalize collapses runs of whitespace so tests can compare statements
// without caring about formatting.
func Normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

var (
	_ crud.Source   = (*Recorder)(nil)
	_ crud.Beginner = (*Recorder)(nil)
	_ crud.Tx       = (*tx)(nil)
)
