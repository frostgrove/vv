// Package utils contains small, dependency-free primitives shared by vv
// modules and application code.
package utils

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"reflect"
)

type optState uint8

const (
	optUndefined optState = iota
	optNull
	optSet
)

// Optional is the public three-state protocol shared by Opt values. It is
// useful to validation, serializers and application-level patch logic without
// making those packages depend on CRUD.
type Optional interface {
	IsDefined() bool
	IsNull() bool
	IsSet() bool
}

// Opt is a three-state optional value: undefined, null, or set.
//
// Its zero value is undefined. That makes it suitable for PATCH DTOs where an
// absent key must differ from an explicit JSON null. It also implements the
// standard database/sql Scanner and Valuer contracts, so it can be used in a
// model without choosing a particular repository or driver.
type Opt[T any] struct {
	value T
	state optState
}

// Set returns an Opt holding v.
func Set[T any](v T) Opt[T] { return Opt[T]{value: v, state: optSet} }

// Null returns an Opt that explicitly means null.
func Null[T any]() Opt[T] { return Opt[T]{state: optNull} }

// Undefined returns the zero Opt: the value was not provided.
func Undefined[T any]() Opt[T] { return Opt[T]{} }

// FromPtr converts the two-state pointer convention into an Opt. nil is null;
// a non-nil pointer is set. It intentionally never returns undefined.
func FromPtr[T any](p *T) Opt[T] {
	if p == nil {
		return Null[T]()
	}
	return Set(*p)
}

// Ptr returns a pointer to v. It is useful for concise construction of patch
// DTOs whose pointer fields mean "defined".
func Ptr[T any](v T) *T { return &v }

// Must returns v or panics with err. Use it only at construction boundaries
// where an error is irrecoverable, such as package-level declarations.
func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// IsDefined reports whether the field was provided at all (set or null).
func (this Opt[T]) IsDefined() bool { return this.state != optUndefined }

// IsNull reports whether the field was explicitly null.
func (this Opt[T]) IsNull() bool { return this.state == optNull }

// IsSet reports whether the field carries a value.
func (this Opt[T]) IsSet() bool { return this.state == optSet }

// Get returns the value and whether it is set.
func (this Opt[T]) Get() (T, bool) { return this.value, this.state == optSet }

// MustGet returns the value and panics if it is not set.
func (this Opt[T]) MustGet() T {
	if this.state != optSet {
		panic("utils: MustGet on an Opt that is not set")
	}
	return this.value
}

// OrElse returns the value if set, otherwise def.
func (this Opt[T]) OrElse(def T) T {
	if this.state == optSet {
		return this.value
	}
	return def
}

// Ptr returns a pointer to the value, or nil when it is not set.
func (this Opt[T]) Ptr() *T {
	if this.state != optSet {
		return nil
	}
	return Ptr(this.value)
}

// IsZero makes json:",omitzero" skip undefined fields on marshal.
func (this Opt[T]) IsZero() bool { return this.state == optUndefined }

func (this Opt[T]) String() string {
	switch this.state {
	case optSet:
		return fmt.Sprintf("%v", this.value)
	case optNull:
		return "<null>"
	default:
		return "<undefined>"
	}
}

// Value implements driver.Valuer.
func (this Opt[T]) Value() (driver.Value, error) {
	if this.state != optSet {
		return nil, nil
	}
	if v, ok := any(this.value).(driver.Valuer); ok {
		if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() &&
			rv.Type().Elem().Implements(valuerType) {
			return nil, nil
		}
		return v.Value()
	}
	if cv, err := driver.DefaultParameterConverter.ConvertValue(this.value); err == nil {
		return cv, nil
	}
	return any(this.value), nil
}

// Scan implements sql.Scanner. NULL becomes null; every non-NULL scan becomes
// set. A scan never manufactures the undefined state.
func (this *Opt[T]) Scan(source any) error {
	if source == nil {
		*this = Null[T]()
		return nil
	}
	var n sql.Null[T]
	if err := n.Scan(source); err != nil {
		return err
	}
	if !n.Valid {
		*this = Null[T]()
		return nil
	}
	*this = Set(n.V)
	return nil
}

var jsonNull = []byte("null")

// MarshalJSON encodes set values normally and both other states as null.
// Undefined is omitted only when its containing field uses json:",omitzero".
func (this Opt[T]) MarshalJSON() ([]byte, error) {
	if this.state != optSet {
		return jsonNull, nil
	}
	return json.Marshal(this.value)
}

// UnmarshalJSON preserves an explicit null. An absent object key never calls
// this method, so its field remains undefined.
func (this *Opt[T]) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), jsonNull) {
		*this = Null[T]()
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*this = Set(v)
	return nil
}

// optional is intentionally package-private. It lets framework internals
// recognise only utils.Opt, not an arbitrary type that happens to offer the
// public Optional methods and would otherwise alter persistence semantics.
type optional interface {
	Optional
	optValue() any
}

func (this Opt[T]) optValue() any {
	if this.state != optSet {
		return nil
	}
	return any(this.value)
}

var (
	valuerType                 = reflect.TypeOf((*driver.Valuer)(nil)).Elem()
	optionalTyp                = reflect.TypeOf((*optional)(nil)).Elem()
	_           driver.Valuer  = Opt[int]{}
	_           sql.Scanner    = (*Opt[int])(nil)
	_           json.Marshaler = Opt[int]{}
	_           Optional       = Opt[int]{}
)

// Inspect reports Opt's stored value, whether it is defined, whether it is
// null and whether v is a utils.Opt at all. It exists for framework packages
// that need reflection without duplicating Opt's private representation.
func Inspect(v any) (value any, defined, null, ok bool) {
	o, ok := v.(optional)
	if !ok {
		return nil, false, false, false
	}
	return o.optValue(), o.IsDefined(), o.IsNull(), true
}

// IsOptType reports whether t is an instantiation of utils.Opt.
func IsOptType(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.Implements(optionalTyp)
}

// OptElem returns Opt's element type, or nil for any other type.
func OptElem(t reflect.Type) reflect.Type {
	if !IsOptType(t) {
		return nil
	}
	f, ok := t.FieldByName("value")
	if !ok {
		return nil
	}
	return f.Type
}
