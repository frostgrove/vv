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
// The classification is SQLSTATE class 23 — integrity constraint violation —
// plus a short list of MySQL numbers that belong there and are not reported
// there. See isIntegrity.
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
// Class 23 is the portable half. The rest is MySQL: its CHECK and
// missing-default errors carry HY000, so the number is the only thing telling
// them apart from an ordinary server error. The number is only trusted when the
// state is exactly HY000, so a numeric field on some other driver's error type
// cannot be mistaken for a MySQL error code.
func isIntegrity(err error) bool {
	state := sqlState(err)
	if strings.HasPrefix(state, "23") {
		return true
	}
	if state != "HY000" {
		return false
	}
	return mysqlIntegrityNumbers[nativeNumber(err)]
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
