package crud

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/frostgrove/vv/utils"
)

type cursorPayload struct {
	F []string          `json:"f"`
	V []json.RawMessage `json:"v"`
}

func EncodeCursor(fields []string, values []any) (string, error) {
	if len(fields) != len(values) {
		return "", fmt.Errorf("crud: cursor has %d fields and %d values", len(fields), len(values))
	}
	p := cursorPayload{F: fields, V: make([]json.RawMessage, len(values))}
	for i, v := range values {
		raw, err := json.Marshal(ElemValue(v))
		if err != nil {
			return "", fmt.Errorf("crud: cursor value for %s: %w", fields[i], err)
		}
		p.V[i] = raw
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeCursor(s string, sort []Order) (*cursorPayload, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, &SchemaError{Field: "cursor", Reason: "not a valid cursor"}
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, &SchemaError{Field: "cursor", Reason: "not a valid cursor"}
	}
	if len(p.F) != len(p.V) || len(p.F) == 0 {
		return nil, &SchemaError{Field: "cursor", Reason: "not a valid cursor"}
	}
	if len(p.F) != len(sort) {
		return nil, &SchemaError{Field: "cursor",
			Reason: "this cursor was made for a different sort order"}
	}
	for i, o := range sort {
		if p.F[i] != o.Field {
			return nil, &SchemaError{Field: "cursor",
				Reason: "this cursor was made for a different sort order"}
		}
	}
	return &p, nil
}

func (this *cursorPayload) value(i int, t reflect.Type) (any, error) {
	destination := reflect.New(t)
	if err := json.Unmarshal(this.V[i], destination.Interface()); err != nil {
		return nil, &SchemaError{Field: "cursor",
			Reason: fmt.Sprintf("value for %s does not fit %s", this.F[i], t)}
	}
	return destination.Elem().Interface(), nil
}

func CursorFieldSupported(f *Field) bool {
	return f != nil && !cursorNullableType(f.Type)
}

func cursorNullableType(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer || utils.IsOptType(t) {
		return true
	}

	if t.PkgPath() == reflect.TypeFor[sql.NullString]().PkgPath() && strings.HasPrefix(t.Name(), "Null") {
		return true
	}

	if t.Kind() == reflect.Struct {
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.Anonymous && cursorNullableType(field.Type) {
				return true
			}
		}
	}
	return false
}

func CursorPredicate(m *Meta, sort []Order, cursor string, back bool) (Predicate, error) {
	p, err := decodeCursor(cursor, sort)
	if err != nil {
		return nil, err
	}

	fields := make([]*Field, len(sort))
	values := make([]any, len(sort))
	for i, o := range sort {
		f := m.Field(o.Field)
		if f == nil {
			return nil, &UnknownFieldError{Model: m.Name, Field: o.Field}
		}

		if !CursorFieldSupported(f) {
			return nil, &SchemaError{Model: m.Name, Field: o.Field,
				Reason: "a cursor cannot page by a nullable column"}
		}
		if v, err := p.value(i, f.Type); err != nil {
			return nil, err
		} else {
			values[i] = v
		}
		fields[i] = f
	}

	branches := make([]Predicate, 0, len(sort))
	for i := range sort {
		terms := make([]Predicate, 0, i+1)
		for j := 0; j < i; j++ {
			terms = append(terms, Eq(sort[j].Field, values[j]))
		}
		terms = append(terms, cursorStep(sort[i], values[i], back))
		if len(terms) == 1 {
			branches = append(branches, terms[0])
		} else {
			branches = append(branches, And(terms...))
		}
	}
	if len(branches) == 1 {
		return branches[0], nil
	}
	return Or(branches...), nil
}

func cursorStep(o Order, v any, back bool) Predicate {
	if o.Desc != back {
		return Lt(o.Field, v)
	}
	return Gt(o.Field, v)
}
