package corpus

import (
	"reflect"
	"strings"

	"github.com/shardit-io/vv/errs/sqlerr"
)

// capture flattens whatever a driver returned into a corpus entry.
//
// It asks by shape rather than by type, for the same reason adapter/crudsql
// does: a fifth engine should cost a table row, not a type switch. The shapes
// it meets are pgconn.PgError (a SQLState method and named string fields),
// mysql.MySQLError (Number, plus SQLState as a [5]byte) and sqlite.Error (a
// Code method and nothing else).
func capture(err error) *sqlerr.Err {
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

// carried is what a parser may read off a driver error, and the whole list.
// pgconn also exposes File, Line and Routine — nbtinsert.c, _bt_check_unique —
// which name PostgreSQL's own source and change when the server is rebuilt.
// Capturing those would put a version-dependent diff into the one file whose
// job is making version-dependent diffs legible.
var carried = []string{
	"ConstraintName", "TableName", "SchemaName", "ColumnName", "DataTypeName",
	"Detail", "Hint",
}

// shapes are the field names that mark a struct as the driver's own error
// rather than something wrapping it.
var shapes = append([]string{"SQLState", "Number", "Code"}, carried...)

func fields(err error) map[string]string {
	out := map[string]string{}
	walk(err, func(_ error, v reflect.Value) bool {
		for _, name := range carried {
			f := v.FieldByName(name)
			if !f.IsValid() || !f.CanInterface() || f.Kind() != reflect.String {
				continue
			}
			if s := f.String(); s != "" {
				out[name] = s
			}
		}
		return len(out) == 0
	})
	return out
}

// driverType names the error the database produced, not the one a repository
// wrapped it in. An error carrying no SQLSTATE at all — a TCP connection that
// never reached a server — is a corpus entry in its own right, and then the
// type is the only thing separating a real capture from a broken one.
func driverType(err error) string {
	var found string
	walk(err, func(e error, v reflect.Value) bool {
		for _, n := range shapes {
			if f := v.FieldByName(n); f.IsValid() {
				found = reflect.TypeOf(e).String()
				return false
			}
		}
		return true
	})
	if found != "" {
		return found
	}
	return reflect.TypeOf(err).String()
}

func sqlState(err error) string {
	var out string
	walk(err, func(e error, v reflect.Value) bool {
		if s, ok := e.(interface{ SQLState() string }); ok {
			if out = s.SQLState(); out != "" {
				return false
			}
		}
		f := v.FieldByName("SQLState")
		if !f.IsValid() || !f.CanInterface() {
			return true
		}
		switch f.Kind() {
		case reflect.String:
			out = f.String()
		case reflect.Array:
			if f.Type().Elem().Kind() == reflect.Uint8 {
				b := make([]byte, f.Len())
				reflect.Copy(reflect.ValueOf(b), f)
				out = string(b)
			}
		}
		return out == ""
	})
	return strings.TrimRight(out, "\x00")
}

// native is the engine's own number. It comes from a Number field on MySQL and
// from a Code method on SQLite, and deliberately not from a field named Code:
// pgconn spells the SQLSTATE that way, so reading it as a number would record
// zero for every PostgreSQL entry while looking like it had worked.
func native(err error) uint64 {
	var out uint64
	walk(err, func(e error, v reflect.Value) bool {
		f := v.FieldByName("Number")
		if f.IsValid() && f.CanInterface() {
			switch f.Kind() {
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				out = f.Uint()
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				if n := f.Int(); n > 0 {
					out = uint64(n)
				}
			}
		}
		if out == 0 {
			if c, ok := e.(interface{ Code() int }); ok {
				if n := c.Code(); n > 0 {
					out = uint64(n)
				}
			}
		}
		return out == 0
	})
	return out
}

// walk visits every error in the tree, handing the callback both the error and
// the struct behind it. It stops when the callback returns false.
//
// errors.Unwrap alone returns nil for a multi-error, and fmt.Errorf("%w: %w", …)
// is exactly what the adapters build, so a plain loop over Unwrap goes blind the
// moment a repository wraps twice.
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
		if v.Kind() == reflect.Struct && !fn(e, v) {
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
