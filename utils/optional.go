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

type Optional interface {
	IsDefined() bool
	IsNull() bool
	IsSet() bool
}

type Opt[T any] struct {
	value T
	state optState
}

func Set[T any](v T) Opt[T] { return Opt[T]{value: v, state: optSet} }

func Null[T any]() Opt[T] { return Opt[T]{state: optNull} }

func Undefined[T any]() Opt[T] { return Opt[T]{} }

func FromPtr[T any](p *T) Opt[T] {
	if p == nil {
		return Null[T]()
	}
	return Set(*p)
}

func Ptr[T any](v T) *T { return &v }

func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func (this Opt[T]) IsDefined() bool { return this.state != optUndefined }

func (this Opt[T]) IsNull() bool { return this.state == optNull }

func (this Opt[T]) IsSet() bool { return this.state == optSet }

func (this Opt[T]) Get() (T, bool) { return this.value, this.state == optSet }

func (this Opt[T]) MustGet() T {
	if this.state != optSet {
		panic("utils: MustGet on an Opt that is not set")
	}
	return this.value
}

func (this Opt[T]) OrElse(def T) T {
	if this.state == optSet {
		return this.value
	}
	return def
}

func (this Opt[T]) Ptr() *T {
	if this.state != optSet {
		return nil
	}
	return Ptr(this.value)
}

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

func (this Opt[T]) MarshalJSON() ([]byte, error) {
	if this.state != optSet {
		return jsonNull, nil
	}
	return json.Marshal(this.value)
}

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

func Inspect(v any) (value any, defined, null, ok bool) {
	o, ok := v.(optional)
	if !ok {
		return nil, false, false, false
	}
	return o.optValue(), o.IsDefined(), o.IsNull(), true
}

func IsOptType(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.Implements(optionalTyp)
}

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
