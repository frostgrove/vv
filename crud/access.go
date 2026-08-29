package crud

import (
	"reflect"
	"unsafe"

	"github.com/frostgrove/vv/utils"
)

// modelBase returns the address of a *M, checking it against the schema.
func (this *Schema) modelBase(model any) (unsafe.Pointer, error) {
	v := reflect.ValueOf(model)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() || v.Type().Elem() != this.Type {
		return nil, &SchemaError{Model: this.Name, Reason: "expected a non-nil *" + this.Name}
	}
	return v.UnsafePointer(), nil
}

// Pointers returns scan destinations for the given fields of model (a *M).
func (this *Schema) Pointers(model any, fields []*Field) ([]any, error) {
	base, err := this.modelBase(model)
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
func (this *Schema) Values(model any, fields []*Field) ([]any, error) {
	base, err := this.modelBase(model)
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
func (this *Schema) ID(model any) (any, error) {
	base, err := this.modelBase(model)
	if err != nil {
		return nil, err
	}
	return this.PK.valueOf(base), nil
}

// HasID reports whether the primary key of model holds a non-zero value.
func (this *Schema) HasID(model any) (bool, error) {
	base, err := this.modelBase(model)
	if err != nil {
		return false, err
	}
	v := reflect.NewAt(this.PK.Type, unsafe.Add(base, this.PK.Offset)).Elem()
	return !v.IsZero(), nil
}

// SetID writes a primary key value, converting between integer widths so a
// driver's int64 LastInsertId lands in an int32 or uint field.
func (this *Schema) SetID(model any, v any) error {
	base, err := this.modelBase(model)
	if err != nil {
		return err
	}
	destination := reflect.NewAt(this.PK.Type, unsafe.Add(base, this.PK.Offset)).Elem()
	rv := reflect.ValueOf(v)
	switch {
	case !rv.IsValid():
		destination.SetZero()
	case rv.Type() == destination.Type():
		destination.Set(rv)
	case rv.Type().ConvertibleTo(destination.Type()):
		destination.Set(rv.Convert(destination.Type()))
	default:
		return &SchemaError{Model: this.Name, Field: this.PK.Name, Reason: "cannot assign " + rv.Type().String() + " to the primary key"}
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
func (this *Schema) CheckID(idType reflect.Type) error {
	if idType == this.PK.Type {
		return nil
	}
	if e := OptElem(this.PK.Type); e != nil && e == idType {
		return nil
	}
	if this.PK.Type.Kind() == reflect.Pointer && this.PK.Type.Elem() == idType {
		return nil
	}
	return &SchemaError{
		Model:  this.Name,
		Field:  this.PK.Name,
		Reason: "repository ID type is " + idType.String() + " but the primary key is " + this.PK.Type.String(),
	}
}
