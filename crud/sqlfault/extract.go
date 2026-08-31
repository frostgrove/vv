package sqlfault

import (
	"reflect"
	"strings"

	"github.com/frostgrove/vv/errs/sqlerr"
)

type Extractor interface {
	Extract(error) *sqlerr.Err
}

type ExtractorFunc func(error) *sqlerr.Err

func (this ExtractorFunc) Extract(err error) *sqlerr.Err { return this(err) }

var carried = []string{
	"ConstraintName", "TableName", "SchemaName", "ColumnName", "DataTypeName",
	"Detail", "Hint",
}

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
