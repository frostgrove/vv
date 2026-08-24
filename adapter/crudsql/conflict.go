package crudsql

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/shardit-io/vv/crud"
)

// conflict tags a driver's integrity error as crud.ErrConflict, so a duplicate
// key reaches an HTTP client as 409 with a message rather than as a bare 500
// that deliberately says nothing. The driver error is kept underneath: whoever
// needs the SQLSTATE or the constraint name can still errors.As their way to it.
//
// The classification has three arms, one per way an engine answers the
// question, and SQLSTATE alone is not a gate for any of them. See isIntegrity.
func conflict(err error) error {
	if err == nil || !isIntegrity(err) {
		return err
	}
	return fmt.Errorf("%w: %w", crud.ErrConflict, err)
}

// mysqlIntegrityNumbers are integrity violations MySQL reports as HY000 — its
// "no more specific state" code — rather than as class 23. Measured on MySQL
// 8.4.11; both are silent 500s without this, because SQLSTATE alone classifies
// neither, while PostgreSQL reports the same two conditions as 23514 and 23502.
//
// MariaDB reportedly uses 4025 for a failed CHECK with SQLSTATE 23000, which
// class 23 would already catch. It is left out until it is measured rather than
// asserted.
var mysqlIntegrityNumbers = map[uint64]bool{
	3819: true, // ER_CHECK_CONSTRAINT_VIOLATED
	1364: true, // ER_NO_DEFAULT_FOR_FIELD
}

// isIntegrity answers whether the driver is describing a constraint the
// database refused to break.
//
// Three arms, because the four engines answer in three different ways, and the
// state is what selects between them rather than what decides:
//
//   - Class 23 is the portable half, and PostgreSQL's whole answer.
//   - HY000 is MySQL saying it has nothing more specific. Its CHECK and
//     missing-default errors land there, so the number is the only thing
//     separating them from an ordinary server error. MariaDB needs no entry: it
//     answers the same CHECK with 4025 and SQLSTATE 23000, which class 23
//     already covers.
//   - No state at all is SQLite, which has no SQLSTATE and never will. Every
//     one of its constraint violations was a bare 500 until this arm existed —
//     seven classes on a shipped dialect, because the one test that would have
//     caught it runs over a target list SQLite is not on.
//
// A number is only ever read once the state has already narrowed which engine
// is speaking, so a numeric field on some other driver's error cannot be
// mistaken for a MySQL code or a SQLite one.
func isIntegrity(err error) bool {
	state := sqlState(err)
	switch {
	case strings.HasPrefix(state, "23"):
		return true
	case state == "HY000":
		return mysqlIntegrityNumbers[nativeNumber(err)]
	case state == "":
		code, ok := sqliteResultCode(err)
		return ok && code&0xff == sqliteConstraint
	}
	return false
}

// sqliteConstraint is SQLITE_CONSTRAINT. Every constraint violation carries it
// in the low byte of an extended result code — 2067 for unique, 787 for a
// foreign key, 1299 for NOT NULL, 275 for CHECK — so the low byte is the test
// and the subcodes need no list. An extended code is not interchangeable with a
// primary one: SQLITE_BUSY is 5, busy-snapshot is 517.
const sqliteConstraint = 19

// sqliteResultCode digs out SQLite's own result code, by shape like everything
// else here. modernc.org/sqlite exposes a Code method; mattn/go-sqlite3 exposes
// Code and ExtendedCode as integer fields.
//
// pgconn also has a field named Code, and it holds the SQLSTATE as a string.
// That is why the kind is checked and not only the name — and why this arm is
// reached only when there is no SQLSTATE, which for pgconn there always is.
func sqliteResultCode(err error) (int, bool) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if c, ok := e.(interface{ Code() int }); ok {
			return c.Code(), true
		}
		v := reflect.ValueOf(e)
		for v.Kind() == reflect.Pointer {
			if v.IsNil() {
				break
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			continue
		}
		// The extended code first: it is the one that names the constraint.
		for _, name := range []string{"ExtendedCode", "Code"} {
			f := v.FieldByName(name)
			if !f.IsValid() || !f.CanInterface() {
				continue
			}
			switch f.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				return int(f.Int()), true
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				return int(f.Uint()), true
			}
		}
	}
	return 0, false
}

// nativeNumber reads the driver's own error number by shape, for the same
// reason sqlState does: this package may not name a driver's error type.
// go-sql-driver/mysql exposes Number as a uint16 field.
func nativeNumber(err error) uint64 {
	for e := err; e != nil; e = errors.Unwrap(e) {
		v := reflect.ValueOf(e)
		for v.Kind() == reflect.Pointer {
			if v.IsNil() {
				break
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			continue
		}
		f := v.FieldByName("Number")
		if !f.IsValid() || !f.CanInterface() {
			continue
		}
		switch f.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return f.Uint()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if n := f.Int(); n >= 0 {
				return uint64(n)
			}
		}
	}
	return 0
}

// sqlState digs the SQLSTATE out of whatever the driver returned. This package
// is in the dependency-free module and may not name a driver's error type, so
// it asks by shape instead: pgx and lib/pq expose a method, go-sql-driver/mysql
// an exported field.
func sqlState(err error) string {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if s, ok := e.(interface{ SQLState() string }); ok {
			return s.SQLState()
		}
		if s := sqlStateField(e); s != "" {
			return s
		}
	}
	return ""
}

func sqlStateField(e error) string {
	v := reflect.ValueOf(e)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	f := v.FieldByName("SQLState")
	if !f.IsValid() || !f.CanInterface() {
		return ""
	}
	switch f.Kind() {
	case reflect.String:
		return f.String()
	case reflect.Array:
		if f.Type().Elem().Kind() != reflect.Uint8 {
			return ""
		}
		b := make([]byte, f.Len())
		reflect.Copy(reflect.ValueOf(b), f)
		return string(b)
	}
	return ""
}
