package sqlfault

import (
	"reflect"
	"strings"

	"github.com/frostgrove/vv/errs/sqlerr"
)

// An Extractor flattens a driver error into the shape sqlerr.Classify reads.
//
// crud/adapter/crudsql cannot name a driver type and asks by shape; crud/adapter/crudpgx
// can name *pgconn.PgError and does, so a field pgconn renames breaks it at
// compile time where the by-shape one goes quietly blank.
type Extractor interface {
	Extract(error) *sqlerr.Err
}

// ExtractorFunc adapts a function to [Extractor].
type ExtractorFunc func(error) *sqlerr.Err

func (this ExtractorFunc) Extract(err error) *sqlerr.Err { return this(err) }

// carried is what may be read off a driver error, and the whole list. There is
// one such list in the tree and this is it: test/corpus captures through
// [Extract] rather than keeping a copy, because the corpus is the expectation
// both adapters are tested against and two implementations of one rule drift.
//
// pgconn also exposes File, Line and Routine — nbtinsert.c, _bt_check_unique —
// which name PostgreSQL's own source and change when the server is rebuilt.
//
// Detail and Hint are carried and never read: they hold the offending value and
// they are localised, which is exactly the pair [[D-039]] forbids classifying
// on.
var carried = []string{
	"ConstraintName", "TableName", "SchemaName", "ColumnName", "DataTypeName",
	"Detail", "Hint",
}

// Extract flattens whatever a driver returned, by shape.
//
// It answers nil only for a nil error. Everything else produces an Err, because
// an error carrying no SQLSTATE and no number at all is a legitimate answer —
// a connection that never reached a server is one — and the caller has to be
// able to tell it from "nothing was extracted".
func Extract(err error) *sqlerr.Err {
	if err == nil {
		return nil
	}
	e := &sqlerr.Err{
		Type:     driverType(err),
		SQLState: sqlState(err),
		Native:   native(err),
		Message:  err.Error(),
		Fields:   fields(err),
	}
	if len(e.Fields) == 0 {
		e.Fields = nil
	}
	return e
}

// sqlState digs the SQLSTATE out of whatever the driver returned. pgx and lib/pq
// expose a method; go-sql-driver/mysql an exported field, spelled [5]byte and
// padded with NULs.
func sqlState(err error) string {
	var out string
	walk(err, func(e error, v reflect.Value) bool {
		if s, ok := e.(interface{ SQLState() string }); ok {
			if out = s.SQLState(); out != "" {
				return false
			}
		}
		if v.Kind() != reflect.Struct {
			return true
		}
		if s := stateField(v); s != "" {
			out = s
			return false
		}
		return true
	})
	return out
}

// stateField reads an exported SQLState field in the two spellings drivers use:
// a plain string, and go-sql-driver/mysql's [5]byte padded with NULs.
func stateField(v reflect.Value) string {
	f := v.FieldByName("SQLState")
	if !f.IsValid() || !f.CanInterface() {
		return ""
	}
	switch f.Kind() {
	case reflect.String:
		return f.String()
	case reflect.Array:
		if f.Type().Elem().Kind() == reflect.Uint8 {
			b := make([]byte, f.Len())
			reflect.Copy(reflect.ValueOf(b), f)
			return strings.TrimRight(string(b), "\x00")
		}
	}
	return ""
}

// native is the engine's own number: MySQL's 1062, SQLite's extended result
// code. One field, because sqlerr.Err has one and the corpus is written against
// it.
//
// The order is the order of certainty. Number is MySQL's and nothing else's. A
// Code method is modernc.org/sqlite's. ExtendedCode comes before Code because
// mattn/go-sqlite3 has both and the extended one is what names the constraint.
//
// A Code *field* is read only when it holds an integer. pgconn spells the
// SQLSTATE in a field called Code, so reading it as a number would record zero
// for every PostgreSQL entry while looking like it had worked — and would route
// every PostgreSQL error through SQLite's arm ([[D-046]]).
//
// The merge is safe for sqlerr.Classify, which is handed the engine and reads
// the number under a key that already names one. It is not safe for the gate's
// no-state arm, which has no engine — see [sqliteNative].
func native(err error) uint64 {
	var out uint64
	walk(err, func(e error, v reflect.Value) bool {
		if v.Kind() == reflect.Struct {
			out = intField(v, "Number")
		}
		if out == 0 {
			if c, ok := e.(interface{ Code() int }); ok {
				if n := c.Code(); n > 0 {
					out = uint64(n)
				}
			}
		}
		if out == 0 && v.Kind() == reflect.Struct {
			for _, name := range []string{"ExtendedCode", "Code"} {
				if out = intField(v, name); out != 0 {
					break
				}
			}
		}
		return out == 0
	})
	return out
}

// sqliteNative is the number the SQLite arm of [Integrity] is allowed to read,
// and only SQLite's own spellings of it: a Code method, then integer
// ExtendedCode and Code fields. Never Number.
//
// That arm has no engine to go on and uses "no SQLSTATE" as the proxy for
// SQLite, so reading the merged [native] there would read a MySQL number as a
// SQLite result code. The state is optional in MySQL's ERR packet and
// go-sql-driver/mysql leaves the [5]byte unset when the '#' marker is absent
// (packets.go:620), so a MySQLError with no state is a shape the driver
// produces — and 1043, ER_HANDSHAKE_ERROR, has 19 in its low byte. A refused
// handshake would answer 409 with the driver's sentence in the body. A number is
// read only once something has said whose it is ([[D-046]]).
func sqliteNative(err error) uint64 {
	var out uint64
	walk(err, func(e error, v reflect.Value) bool {
		if c, ok := e.(interface{ Code() int }); ok {
			if n := c.Code(); n > 0 {
				out = uint64(n)
			}
		}
		if out == 0 && v.Kind() == reflect.Struct {
			for _, name := range []string{"ExtendedCode", "Code"} {
				if out = intField(v, name); out != 0 {
					break
				}
			}
		}
		return out == 0
	})
	return out
}

// intField reads one integer field, and answers zero for a field of any other
// kind. The kind check is the guard: a string field with the right name is not a
// number, whatever it is called.
func intField(v reflect.Value, name string) uint64 {
	f := v.FieldByName(name)
	if !f.IsValid() || !f.CanInterface() {
		return 0
	}
	switch f.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return f.Uint()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n := f.Int(); n > 0 {
			return uint64(n)
		}
	}
	return 0
}

// fields carries across the structured extras the driver populated, and only the
// whitelisted ones. Empty values are dropped, and a driver that populated none
// answers nil rather than an empty map — "not known" must not read as "there
// were none".
//
// They come from one error, and from the engine's own wherever the tree holds
// one. Detail and Hint are ordinary names: a repository's wrapper carrying a
// Detail sits outside the driver error and the walk meets it first, so taking
// whatever contributed first would replace pgconn's constraint and table with
// that one string — and the constraint and the table are the key
// Classifier.fill looks the columns up by.
func fields(err error) map[string]string {
	var out map[string]string
	walk(err, func(e error, v reflect.Value) bool {
		if engineError(e, v) {
			out = carriedFrom(v)
			return false
		}
		if out == nil {
			out = carriedFrom(v)
		}
		return true
	})
	return out
}

// carriedFrom reads the whitelisted string fields off one struct, and answers nil
// where it has none.
func carriedFrom(v reflect.Value) map[string]string {
	if v.Kind() != reflect.Struct {
		return nil
	}
	var out map[string]string
	for _, name := range carried {
		f := v.FieldByName(name)
		if !f.IsValid() || !f.CanInterface() || f.Kind() != reflect.String {
			continue
		}
		if s := f.String(); s != "" {
			if out == nil {
				out = map[string]string{}
			}
			out[name] = s
		}
	}
	return out
}

// driverType names the error the database produced, not the one a repository
// wrapped it in. An error carrying no SQLSTATE at all is a legitimate entry, and
// then the type is the only thing separating a real extraction from a broken
// one.
//
// The mark is [engineError]'s. An error that says nothing about what happened —
// pgconn's ConnectError, a bare net.OpError — leaves none anywhere in the tree,
// and the outermost error's type is then the answer. The corpus records both
// under exactly those names.
func driverType(err error) string {
	var found string
	walk(err, func(e error, v reflect.Value) bool {
		if !engineError(e, v) {
			return true
		}
		found = reflect.TypeOf(e).String()
		return false
	})
	if found != "" {
		return found
	}
	return reflect.TypeOf(err).String()
}

// engineError answers whether this node of the tree is the error the engine
// raised rather than something wrapping it: it says what happened, in a state or
// in a number.
//
// A field's *presence* is not the mark. errs.Fault carries a Code of its own and
// is in the chain whenever [Wrap] classified something once already, and an
// application's error struct may carry a Detail — either would otherwise be read
// as the driver's own error.
func engineError(e error, v reflect.Value) bool {
	if s, ok := e.(interface{ SQLState() string }); ok && s.SQLState() != "" {
		return true
	}
	if c, ok := e.(interface{ Code() int }); ok && c.Code() > 0 {
		return true
	}
	if v.Kind() != reflect.Struct {
		return false
	}
	if stateField(v) != "" {
		return true
	}
	for _, name := range []string{"Number", "ExtendedCode", "Code"} {
		if intField(v, name) != 0 {
			return true
		}
	}
	return false
}

// walk visits every error in the tree, handing the callback both the error and
// the struct behind it. It stops when the callback returns false.
//
// errors.Unwrap alone returns nil for a multi-error, and fmt.Errorf("%w: %w", …)
// is exactly what [Wrap] builds — as is errs.Fault.Unwrap, which returns
// []error. A plain loop over Unwrap therefore goes blind the moment this
// package's own output is in the chain ([[D-038]]).
//
// The callback fires for every error, including one that is not a struct: a
// driver error can be a defined integer or string type with a method, and a
// callback reached only for structs would silently drop the method paths. Every
// callback checks the kind before it reaches for a field, because
// reflect.Value.FieldByName panics on anything else.
func walk(err error, fn func(error, reflect.Value) bool) {
	var visit func(error) bool
	visit = func(e error) bool {
		if e == nil {
			return true
		}
		v := reflect.ValueOf(e)
		for v.Kind() == reflect.Pointer && !v.IsNil() {
			v = v.Elem()
		}
		if !fn(e, v) {
			return false
		}
		switch u := e.(type) {
		case interface{ Unwrap() error }:
			return visit(u.Unwrap())
		case interface{ Unwrap() []error }:
			for _, sub := range u.Unwrap() {
				if !visit(sub) {
					return false
				}
			}
		}
		return true
	}
	visit(err)
}
