package crudsql

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"rx-crud/crud"
)

// conflict tags a driver's integrity error as crud.ErrConflict, so a duplicate
// key reaches an HTTP client as 409 with a message rather than as a bare 500
// that deliberately says nothing. The driver error is kept underneath: whoever
// needs the SQLSTATE or the constraint name can still errors.As their way to it.
//
// The classification is SQLSTATE class 23 — integrity constraint violation —
// which both engines report and neither invents: unique key, foreign key, NOT
// NULL and CHECK all live there, and nothing else does.
func conflict(err error) error {
	if err == nil || !strings.HasPrefix(sqlState(err), "23") {
		return err
	}
	return fmt.Errorf("%w: %w", crud.ErrConflict, err)
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
