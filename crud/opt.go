package crud

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

type optState uint8

const (
	optUndefined optState = iota // field was not provided at all
	optNull                      // field was explicitly provided as NULL
	optSet                       // field was provided with a value
)

// Opt is a three-state optional value: undefined, null, or set.
//
// It exists because a PATCH-style update has three distinct intents for a
// nullable column and a *T only expresses two:
//
//	{}                  -> undefined: leave the column alone
//	{"age": null}       -> null:      write NULL
//	{"age": 31}         -> set:       write 31
//
// The zero value is undefined, so a field omitted from a JSON body decodes to
// exactly the right state without any extra work. Opt is also a valid model
// field type for nullable columns, where it behaves as a scannable NULL box.
type Opt[T any] struct {
	value T
	state optState
}

// Set returns an Opt holding v.
func Set[T any](v T) Opt[T] { return Opt[T]{value: v, state: optSet} }

// Null returns an Opt that explicitly means SQL NULL.
func Null[T any]() Opt[T] { return Opt[T]{state: optNull} }

// Undefined returns the zero Opt: "not provided".
func Undefined[T any]() Opt[T] { return Opt[T]{} }

// FromPtr converts the two-state pointer convention into an Opt: nil becomes
// null, anything else becomes set. It never produces undefined.
func FromPtr[T any](p *T) Opt[T] {
	if p == nil {
		return Null[T]()
	}
	return Set(*p)
}

// IsDefined reports whether the field was provided at all (set or null).
func (o Opt[T]) IsDefined() bool { return o.state != optUndefined }

// IsNull reports whether the field was explicitly provided as NULL.
func (o Opt[T]) IsNull() bool { return o.state == optNull }

// IsSet reports whether the field carries a value.
func (o Opt[T]) IsSet() bool { return o.state == optSet }

// Get returns the value and whether it is set.
func (o Opt[T]) Get() (T, bool) { return o.value, o.state == optSet }

// MustGet returns the value and panics if it is not set.
func (o Opt[T]) MustGet() T {
	if o.state != optSet {
		panic("crud: MustGet on an Opt that is not set")
	}
	return o.value
}

// OrElse returns the value if set, otherwise def.
func (o Opt[T]) OrElse(def T) T {
	if o.state == optSet {
		return o.value
	}
	return def
}

// Ptr returns a pointer to the value, or nil when not set.
func (o Opt[T]) Ptr() *T {
	if o.state != optSet {
		return nil
	}
	v := o.value
	return &v
}

// IsZero makes `json:",omitzero"` skip undefined fields on marshal.
func (o Opt[T]) IsZero() bool { return o.state == optUndefined }

func (o Opt[T]) String() string {
	switch o.state {
	case optSet:
		return fmt.Sprintf("%v", o.value)
	case optNull:
		return "<null>"
	default:
		return "<undefined>"
	}
}

// Value implements driver.Valuer.
func (o Opt[T]) Value() (driver.Value, error) {
	if o.state != optSet {
		return nil, nil
	}
	if v, ok := any(o.value).(driver.Valuer); ok {
		return v.Value()
	}
	// database/sql insists on a canonical driver.Value; pgx happily takes the
	// rich type. Try the strict conversion first and fall back to the raw value.
	if cv, err := driver.DefaultParameterConverter.ConvertValue(o.value); err == nil {
		return cv, nil
	}
	return any(o.value), nil
}

// Scan implements sql.Scanner. A NULL column yields null, anything else set;
// scanning never produces undefined.
func (o *Opt[T]) Scan(src any) error {
	if src == nil {
		*o = Null[T]()
		return nil
	}
	// sql.Null[T] reuses database/sql's convertAssign, which also honours a
	// custom sql.Scanner on T.
	var n sql.Null[T]
	if err := n.Scan(src); err != nil {
		return err
	}
	if !n.Valid {
		*o = Null[T]()
		return nil
	}
	*o = Set(n.V)
	return nil
}

var jsonNull = []byte("null")

// MarshalJSON encodes set values normally and both other states as null. Pair
// the field with `json:",omitzero"` to drop undefined fields entirely.
func (o Opt[T]) MarshalJSON() ([]byte, error) {
	if o.state == optSet {
		return json.Marshal(o.value)
	}
	return jsonNull, nil
}

// UnmarshalJSON maps an explicit null to null and any other token to set. A key
// that is absent from the payload never reaches this method, so it stays
// undefined — which is exactly the PATCH semantics we want.
func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), jsonNull) {
		*o = Null[T]()
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*o = Set(v)
	return nil
}

// optional is the non-generic view the reflective planner uses to read an Opt
// field without knowing its element type.
type optional interface {
	optDefined() bool
	optNull() bool
	optValue() any
}

func (o Opt[T]) optDefined() bool { return o.state != optUndefined }
func (o Opt[T]) optNull() bool    { return o.state == optNull }
func (o Opt[T]) optValue() any {
	if o.state != optSet {
		return nil
	}
	return any(o.value)
}

var (
	_ driver.Valuer = Opt[int]{}
	_ sql.Scanner   = (*Opt[int])(nil)
	_ json.Marshaler
	_ optional = Opt[int]{}
)
