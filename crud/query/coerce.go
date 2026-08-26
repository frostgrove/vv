package query

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/frostgrove/vv/crud"
)

// decodeValue turns a JSON scalar into the Go type of the column it will be
// compared against. This is the difference between `views >= 100` binding an
// int64 and binding a float64 that the planner may refuse to use an index for —
// and it is what makes `"createdAt": {"gte": "2026-01-01T00:00:00Z"}` work at
// all.
func decodeValue(raw json.RawMessage, f *crud.Field) (any, error) {
	t := crud.ElemType(f.Type)
	ptr := reflect.New(t)
	if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
		// A date-only string is worth a second try; clients send them constantly.
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

// decodeList decodes a JSON array into a []any of correctly typed elements.
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

// Coerce converts a text value into the Go type t, using the type's own
// TextUnmarshaler when it has one. It is exported because transports need it
// for path parameters too.
func Coerce(s string, t reflect.Type) (any, error) { return coerceString(s, t) }

// coerceString converts a query-string value — always text — into the Go type
// of its column. It tries the type's own TextUnmarshaler first, so uuid, enum
// and money types keep their parsing rules.
func coerceString(s string, t reflect.Type) (any, error) {
	// time.Time is checked before the TextUnmarshaler branch it would otherwise
	// win: its own UnmarshalText takes RFC 3339 and nothing else, which would
	// reject the date-only and space-separated forms the JSON door accepts.
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
		ptr.Elem().SetFloat(n)
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			ptr.Elem().SetBytes([]byte(s))
			break
		}
		return nil, fmt.Errorf("cannot parse %q as %s", s, wanted(t))
	default:
		// Fall back to JSON, which covers structs with their own decoder.
		if err := json.Unmarshal([]byte(strconv.Quote(s)), ptr.Interface()); err != nil {
			return nil, fmt.Errorf("cannot parse %q as %s", s, wanted(t))
		}
	}
	return ptr.Elem().Interface(), nil
}

// wanted names a column's shape in the vocabulary a client speaks, never in Go's.
//
// These messages are rendered — port.FaultOf turns a *query.Error into a
// violation whose message is its Reason — so formatting a reflect.Type into one
// puts the Go type of the consumer's own field, package path included, on the
// wire: "Price expects crud.Opt[int64]". [[D-044]] forbids exactly that, and the
// corpus render test cannot see it, because that test renders faults the
// *database* produced and these come from the decoder.
//
// The field name stays. It is the client's own path into its own document, which
// is what [[D-013]] requires a refusal to carry.
func wanted(t reflect.Type) string {
	if t == nil {
		return "a value of a different shape"
	}
	if t == reflect.TypeOf(time.Time{}) {
		return "a timestamp"
	}
	// A type with its own text or JSON decoding — a uuid, an enum, a money type —
	// has rules of its own that this package cannot summarise and must not guess
	// at. Naming the field is as far as it can honestly go.
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
