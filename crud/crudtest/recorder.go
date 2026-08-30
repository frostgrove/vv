// Package crudtest provides an in-memory crud.Source that records the SQL a
// repository produces and replays canned result sets back at it. It exists so
// repository behaviour — statement shape, bind order, pagination arithmetic,
// decorator composition — can be unit-tested without a database.
package crudtest

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frostgrove/vv/crud"
)

// Statement is one recorded call.
type Statement struct {
	SQL   string
	Args  []any
	Query bool // Query rather than Exec
}

func (this Statement) String() string { return fmt.Sprintf("%s %v", this.SQL, this.Args) }

// Result is a canned query response. Values are assigned to scan destinations
// positionally, using database/sql-compatible conversions and honouring
// sql.Scanner.
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

func (this *Recorder) Dialect() crud.Dialect { return this.D }

// DataSource makes one recorder one canonical datasource. Transactions opened
// by it stay available to repositories bound to that recorder without becoming
// an unscoped executor that unrelated test repositories may adopt.
func (this *Recorder) DataSource() any { return this }

// Push queues result sets, consumed by successive Query calls in order.
func (this *Recorder) Push(results ...Result) *Recorder {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.queue = append(this.queue, results...)
	return this
}

// ExecResult sets what Exec reports back.
func (this *Recorder) ExecResult(response crud.Result) *Recorder {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.exec = response
	return this
}

// Fail makes the next Exec return err.
func (this *Recorder) Fail(err error) *Recorder {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.execErr = err
	return this
}

// Statements returns everything recorded so far.
func (this *Recorder) Statements() []Statement {
	this.mu.Lock()
	defer this.mu.Unlock()
	return append([]Statement(nil), this.statements...)
}

// Last returns the most recent statement.
func (this *Recorder) Last() Statement {
	s := this.Statements()
	if len(s) == 0 {
		return Statement{}
	}
	return s[len(s)-1]
}

// SQL returns just the recorded statement texts.
func (this *Recorder) SQL() []string {
	out := []string{}
	for _, s := range this.Statements() {
		out = append(out, s.SQL)
	}
	return out
}

// Reset clears the recording and the queue.
func (this *Recorder) Reset() {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.statements, this.queue = nil, nil
}

// TxDepth reports how many transactions were begun.
func (this *Recorder) TxDepth() int {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.txDepth
}

func (this *Recorder) Exec(_ context.Context, q string, args ...any) (crud.Result, error) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.statements = append(this.statements, Statement{SQL: q, Args: args})
	if this.execErr != nil {
		err := this.execErr
		this.execErr = nil
		return crud.Result{}, err
	}
	return this.exec, nil
}

func (this *Recorder) Query(_ context.Context, q string, args ...any) (crud.Rows, error) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.statements = append(this.statements, Statement{SQL: q, Args: args, Query: true})
	var response Result
	if len(this.queue) > 0 {
		response, this.queue = this.queue[0], this.queue[1:]
	}
	if response.Err != nil {
		return nil, response.Err
	}
	return &rows{data: response.Rows, err: response.RowsErr}, nil
}

// Begin hands out a transaction that records into the same recorder.
func (this *Recorder) Begin(context.Context) (crud.Tx, error) {
	this.mu.Lock()
	this.txDepth++
	this.mu.Unlock()
	return &tx{Recorder: this}, nil
}

type tx struct {
	*Recorder
	Committed  bool
	RolledBack bool
}

func (this *tx) Commit(context.Context) error   { this.Committed = true; return nil }
func (this *tx) Rollback(context.Context) error { this.RolledBack = true; return nil }

type rows struct {
	data [][]any
	i    int
	cur  []any
	err  error
}

func (this *rows) Next() bool {
	if this.i >= len(this.data) {
		return false
	}
	this.cur = this.data[this.i]
	this.i++
	return true
}

func (this *rows) Err() error { return this.err }
func (this *rows) Close()     {}

func (this *rows) Scan(dest ...any) error {
	if len(dest) != len(this.cur) {
		return fmt.Errorf("crudtest: scanning %d values into %d destinations", len(this.cur), len(dest))
	}
	for i, d := range dest {
		if err := assign(d, this.cur[i]); err != nil {
			return fmt.Errorf("crudtest: column %d: %w", i, err)
		}
	}
	return nil
}

func assign(destination, source any) error {
	dv := reflect.ValueOf(destination)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("destination %T is not a non-nil pointer", destination)
	}
	if s, ok := destination.(sql.Scanner); ok {
		return s.Scan(source)
	}
	return setValue(dv.Elem(), source)
}

// setValue writes src into dst, allocating through a pointer column on the way.
func setValue(destination reflect.Value, source any) error {
	if source == nil {
		switch destination.Kind() {
		case reflect.Pointer, reflect.Interface, reflect.Slice:
			destination.SetZero()
			return nil
		default:
			return fmt.Errorf("converting NULL to %s is unsupported", destination.Kind())
		}
	}
	if destination.Kind() == reflect.Pointer {
		p := reflect.New(destination.Type().Elem())
		if err := setValue(p.Elem(), source); err != nil {
			return err
		}
		destination.Set(p)
		return nil
	}
	sv := reflect.ValueOf(source)
	if destination.Type() == reflect.TypeFor[any]() {
		if b, ok := source.([]byte); ok {
			destination.Set(reflect.ValueOf(bytes.Clone(b)))
		} else {
			destination.Set(sv)
		}
		return nil
	}
	if tm, ok := source.(time.Time); ok {
		formatted := tm.Format(time.RFC3339Nano)
		switch destination.Type() {
		case reflect.TypeFor[string]():
			destination.SetString(formatted)
			return nil
		case reflect.TypeFor[[]byte](), reflect.TypeFor[sql.RawBytes]():
			destination.SetBytes([]byte(formatted))
			return nil
		}
	}
	if sv.Type().AssignableTo(destination.Type()) {
		if b, ok := source.([]byte); ok {
			destination.Set(reflect.ValueOf(bytes.Clone(b)))
		} else {
			destination.Set(sv)
		}
		return nil
	}
	// database/sql permits conversion between named and unnamed values of the
	// same kind, but not arbitrary Go conversions such as int -> string (rune).
	if destination.Kind() == sv.Kind() && sv.Type().ConvertibleTo(destination.Type()) {
		destination.Set(sv.Convert(destination.Type()))
		return nil
	}

	if destination.Type() == reflect.TypeFor[[]byte]() {
		if b, ok := scanBytes(sv); ok {
			destination.SetBytes(b)
			return nil
		}
	}
	if destination.Type() == reflect.TypeFor[sql.RawBytes]() {
		if b, ok := scanBytes(sv); ok {
			destination.SetBytes(b)
			return nil
		}
	}
	if destination.Type() == reflect.TypeFor[time.Time]() {
		if tm, ok := source.(time.Time); ok {
			destination.Set(reflect.ValueOf(tm))
			return nil
		}
	}

	switch destination.Kind() {
	case reflect.Bool:
		v, err := driver.Bool.ConvertValue(source)
		if err != nil {
			return err
		}
		destination.SetBool(v.(bool))
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		s := scanString(source)
		n, err := strconv.ParseInt(s, 10, destination.Type().Bits())
		if err != nil {
			return fmt.Errorf("converting driver value %T (%q) to %s: %w", source, s, destination.Kind(), err)
		}
		destination.SetInt(n)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s := scanString(source)
		n, err := strconv.ParseUint(s, 10, destination.Type().Bits())
		if err != nil {
			return fmt.Errorf("converting driver value %T (%q) to %s: %w", source, s, destination.Kind(), err)
		}
		destination.SetUint(n)
		return nil
	case reflect.Float32, reflect.Float64:
		s := scanString(source)
		n, err := strconv.ParseFloat(s, destination.Type().Bits())
		if err != nil {
			return fmt.Errorf("converting driver value %T (%q) to %s: %w", source, s, destination.Kind(), err)
		}
		destination.SetFloat(n)
		return nil
	case reflect.String:
		switch v := source.(type) {
		case string:
			destination.SetString(v)
			return nil
		case []byte:
			destination.SetString(string(v))
			return nil
		default:
			// The built-in string destination is the database/sql convenience
			// case that formats numeric and boolean driver values. Named string
			// types intentionally accept only text, matching database/sql.
			if destination.Type() == reflect.TypeFor[string]() && scanScalar(sv.Kind()) {
				destination.SetString(scanString(source))
				return nil
			}
		}
	}
	return fmt.Errorf("unsupported Scan, storing driver value type %T into type %s", source, destination.Type())
}

func scanScalar(kind reflect.Kind) bool {
	switch kind {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func scanString(source any) string {
	switch v := source.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	}
	rv := reflect.ValueOf(source)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10)
	case reflect.Float32:
		return strconv.FormatFloat(rv.Float(), 'g', -1, 32)
	case reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'g', -1, 64)
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool())
	}
	return fmt.Sprint(source)
}

func scanBytes(source reflect.Value) ([]byte, bool) {
	switch source.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.AppendInt(nil, source.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.AppendUint(nil, source.Uint(), 10), true
	case reflect.Float32:
		return strconv.AppendFloat(nil, source.Float(), 'g', -1, 32), true
	case reflect.Float64:
		return strconv.AppendFloat(nil, source.Float(), 'g', -1, 64), true
	case reflect.Bool:
		return strconv.AppendBool(nil, source.Bool()), true
	case reflect.String:
		return []byte(source.String()), true
	case reflect.Slice:
		if source.Type().Elem().Kind() == reflect.Uint8 {
			return bytes.Clone(source.Bytes()), true
		}
	}
	return nil, false
}

// Normalize collapses runs of whitespace so tests can compare statements
// without caring about formatting.
func Normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

var (
	_ crud.Source   = (*Recorder)(nil)
	_ crud.Beginner = (*Recorder)(nil)
	_ crud.Tx       = (*tx)(nil)
)
