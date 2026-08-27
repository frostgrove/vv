package crud

import (
	"reflect"
	"unsafe"

	"github.com/frostgrove/vv/utils"
)

// modelBase returns the address of a *M, checking it against the schema.
func (s *Schema) modelBase(model any) (unsafe.Pointer, error) {
	v := reflect.ValueOf(model)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() || v.Type().Elem() != s.Type {
		return nil, &SchemaError{Model: s.Name, Reason: "expected a non-nil *" + s.Name}
	}
	return v.UnsafePointer(), nil
}

// Pointers returns scan destinations for the given fields of model (a *M).
func (s *Schema) Pointers(model any, fields []*Field) ([]any, error) {
	base, err := s.modelBase(model)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(fields))
	for i, f := range fields {
		out[i] = f.pointerTo(base)
	}
	return out, nil
}

// Values returns bind arguments for the given fields of model (a *M).
func (s *Schema) Values(model any, fields []*Field) ([]any, error) {
	base, err := s.modelBase(model)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(fields))
	for i, f := range fields {
		out[i] = f.valueOf(base)
	}
	return out, nil
}

// ID reads the primary key out of model (a *M).
func (s *Schema) ID(model any) (any, error) {
	base, err := s.modelBase(model)
	if err != nil {
		return nil, err
	}
	return s.PK.valueOf(base), nil
}

// HasID reports whether the primary key of model holds a non-zero value.
func (s *Schema) HasID(model any) (bool, error) {
	base, err := s.modelBase(model)
	if err != nil {
		return false, err
	}
	v := reflect.NewAt(s.PK.Type, unsafe.Add(base, s.PK.Offset)).Elem()
	return !v.IsZero(), nil
}

// SetID writes a primary key value, converting between integer widths so a
// driver's int64 LastInsertId lands in an int32 or uint field.
func (s *Schema) SetID(model any, v any) error {
	base, err := s.modelBase(model)
	if err != nil {
		return err
	}
	dst := reflect.NewAt(s.PK.Type, unsafe.Add(base, s.PK.Offset)).Elem()
	rv := reflect.ValueOf(v)
	switch {
	case !rv.IsValid():
		dst.SetZero()
	case rv.Type() == dst.Type():
		dst.Set(rv)
	case rv.Type().ConvertibleTo(dst.Type()):
		dst.Set(rv.Convert(dst.Type()))
	default:
		return &SchemaError{Model: s.Name, Field: s.PK.Name, Reason: "cannot assign " + rv.Type().String() + " to the primary key"}
	}
	return nil
}

// ElemValue unwraps an Opt or a pointer, yielding nil for null and the bare
// value otherwise. It is what makes a raw int64 from a request context
// comparable with an Opt[int64] column.
func ElemValue(v any) any {
	if v == nil {
		return nil
	}
	if value, defined, null, ok := utils.Inspect(v); ok {
		if !defined || null {
			return nil
		}
		return value
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		return rv.Elem().Interface()
	}
	return v
}

// CheckID verifies that the repository's ID type parameter matches the model's
// primary key. Called once at Define time.
func (s *Schema) CheckID(idType reflect.Type) error {
	if idType == s.PK.Type {
		return nil
	}
	if e := OptElem(s.PK.Type); e != nil && e == idType {
		return nil
	}
	if s.PK.Type.Kind() == reflect.Pointer && s.PK.Type.Elem() == idType {
		return nil
	}
	return &SchemaError{
		Model:  s.Name,
		Field:  s.PK.Name,
		Reason: "repository ID type is " + idType.String() + " but the primary key is " + s.PK.Type.String(),
	}
}
