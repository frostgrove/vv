// Package vvflag reads one command-line flag into a typed value.
//
// It exists because reaching for the standard library's flag package means
// declaring every flag up front through a FlagSet, which a library cannot do on
// the application's behalf. This reads a single named flag out of a slice of
// arguments and nothing else.
//
// Three forms are understood:
//
//	--name=value
//	--name value      (except for bool, where the flag alone means true)
//	--name            (bool only)
//
// A `--` argument ends flag parsing, and everything after it is positional.
//
// The one rule worth knowing: **absent and malformed are different answers.**
// A flag that is not there returns the default and [ErrAbsent]; a flag that is
// there and will not parse returns the default and a parse error. Collapsing
// the two is how a typo'd `--port=abc` silently starts a server on port 0.
package vvflag

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
)

// ErrAbsent reports that the flag was not present. It is returned alongside the
// default value, so a caller that is happy with the default can ignore it and a
// caller that is not can branch on it.
var ErrAbsent = errors.New("vvflag: flag not present")

// Parse reads name out of args, which should not include the program name.
//
// It returns the default and [ErrAbsent] when the flag is missing, and the
// default with a parse error when the flag is present and unusable.
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

// Or is Parse with the absent case folded into the default, for the common
// call site that wants a value or a fallback. A malformed value is still an
// error: a flag someone typed wrong is not a flag they left out.
func Or[T any](args []string, name string, def T) (T, error) {
	v, err := Parse(args, name, def)
	if errors.Is(err, ErrAbsent) {
		return def, nil
	}
	return v, err
}

// Lookup is Or over os.Args.
func Lookup[T any](name string, def T) (T, error) {
	return Or(os.Args[1:], name, def)
}

// find locates the raw string for a flag. A bool flag may stand alone; every
// other flag consumes the following argument, including one that starts with a
// dash — otherwise a negative number could not be passed at all.
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
		return "", false // named, but nothing follows it
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

// coerce fills a T from raw. It switches on the reflect kind rather than on the
// dynamic type, so a named type — `type Port int` — is supported. A type switch
// would send it to the default case and report it as unparseable.
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
