package vvflag

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
)

var ErrAbsent = errors.New("vvflag: flag not present")

func Parse[T any](args []string, name string, def T) (T, error) {
	raw, found := find(args, name, isBool(def))
	if !found {
		return def, ErrAbsent
	}
	v, err := coerce[T](raw)
	if err != nil {
		return def, fmt.Errorf("vvflag: --%s=%q: %w", name, raw, err)
	}
	return v, nil
}

func Or[T any](args []string, name string, def T) (T, error) {
	v, err := Parse(args, name, def)
	if errors.Is(err, ErrAbsent) {
		return def, nil
	}
	return v, err
}

func Lookup[T any](name string, def T) (T, error) {
	return Or(os.Args[1:], name, def)
}

func find(args []string, name string, boolean bool) (string, bool) {
	flag := "--" + name
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return "", false
		}
		if raw, ok := cutPrefix(arg, flag+"="); ok {
			return raw, true
		}
		if arg != flag {
			continue
		}
		if boolean {
			return "true", true
		}
		if i+1 < len(args) && args[i+1] != "--" {
			return args[i+1], true
		}
		return "", false
	}
	return "", false
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

func isBool[T any](v T) bool { return reflect.ValueOf(&v).Elem().Kind() == reflect.Bool }

func coerce[T any](raw string) (T, error) {
	var out T
	rv := reflect.ValueOf(&out).Elem()
	switch k := rv.Kind(); k {
	case reflect.String:
		rv.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return out, errors.New("not a boolean")
		}
		rv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, rv.Type().Bits())
		if err != nil {
			return out, errors.New("not an integer")
		}
		rv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, err := strconv.ParseUint(raw, 10, rv.Type().Bits())
		if err != nil {
			return out, errors.New("not an unsigned integer")
		}
		rv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, rv.Type().Bits())
		if err != nil {
			return out, errors.New("not a number")
		}
		rv.SetFloat(f)
	case reflect.Complex64, reflect.Complex128:
		c, err := strconv.ParseComplex(raw, rv.Type().Bits())
		if err != nil {
			return out, errors.New("not a complex number")
		}
		rv.SetComplex(c)
	default:
		return out, fmt.Errorf("unsupported kind %s", k)
	}
	return out, nil
}
