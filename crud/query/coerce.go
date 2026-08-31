package query

import (
	"encoding"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/frostgrove/vv/crud"
)

func decodeValue(raw json.RawMessage, f *crud.Field) (any, error) {
	t := crud.ElemType(f.Type)
	ptr := reflect.New(t)
	if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
		if numericKind(t.Kind()) {
			var s string
			if json.Unmarshal(raw, &s) == nil {
				if v, serr := coerceString(s, t); serr == nil {
					return v, nil
				}
			}
		}

		if t == reflect.TypeOf(time.Time{}) {
			var s string
			if json.Unmarshal(raw, &s) == nil {
				if tv, perr := parseTime(s); perr == nil {
					return tv, nil
				}
			}
		}
		return nil, fmt.Errorf("%s expects %s, got %s", f.Name, wanted(t), preview(raw))
	}
	return ptr.Elem().Interface(), nil
}

func numericKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func decodeList(raw json.RawMessage, f *crud.Field) ([]any, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("%s expects an array, got %s", f.Name, preview(raw))
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		if isNull(trim(item)) {
			out = append(out, nil)
			continue
		}
		v, err := decodeValue(item, f)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseTime(s string) (time.Time, error) {
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse %q as a timestamp", s)
}

var textUnmarshaler = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()

func Coerce(s string, t reflect.Type) (any, error) { return coerceString(s, t) }

func coerceString(s string, t reflect.Type) (any, error) {
	if t == reflect.TypeOf(time.Time{}) {
		return parseTime(s)
	}
	ptr := reflect.New(t)
	if reflect.PointerTo(t).Implements(textUnmarshaler) {
		if err := ptr.Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s)); err != nil {
			return nil, err
		}
		return ptr.Elem().Interface(), nil
	}

	switch t.Kind() {
	case reflect.String:
		ptr.Elem().SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, err
		}
		ptr.Elem().SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, err
		}
		if ptr.Elem().OverflowInt(n) {
			return nil, fmt.Errorf("%s does not fit in %s", s, wanted(t))
		}
		ptr.Elem().SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil, err
		}
		if ptr.Elem().OverflowUint(n) {
			return nil, fmt.Errorf("%s does not fit in %s", s, wanted(t))
		}
		ptr.Elem().SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, err
		}
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, fmt.Errorf("%s is not a finite %s", s, wanted(t))
		}
		ptr.Elem().SetFloat(n)
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			v, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return nil, err
			}
			ptr.Elem().SetBytes(v)
			break
		}
		return nil, fmt.Errorf("cannot parse %q as %s", s, wanted(t))
	default:
		if err := json.Unmarshal([]byte(strconv.Quote(s)), ptr.Interface()); err != nil {
			return nil, fmt.Errorf("cannot parse %q as %s", s, wanted(t))
		}
	}
	return ptr.Elem().Interface(), nil
}

func wanted(t reflect.Type) string {
	if t == nil {
		return "a value of a different shape"
	}
	if t == reflect.TypeOf(time.Time{}) {
		return "a timestamp"
	}

	if t.Implements(textUnmarshaler) || reflect.PointerTo(t).Implements(textUnmarshaler) ||
		t.Implements(jsonUnmarshaler) || reflect.PointerTo(t).Implements(jsonUnmarshaler) {
		return "a value in this field's own format"
	}
	switch t.Kind() {
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.String:
		return "a string"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Map, reflect.Struct:
		return "an object"
	default:
		return "a value of a different shape"
	}
}

var jsonUnmarshaler = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
