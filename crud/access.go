package crud

import (
	"reflect"
	"unsafe"

	"github.com/frostgrove/vv/utils"
)

func (this *Schema) modelBase(model any) (unsafe.Pointer, error) {
	v := reflect.ValueOf(model)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() || v.Type().Elem() != this.Type {
		return nil, &SchemaError{Model: this.Name, Reason: "expected a non-nil *" + this.Name}
	}
	return v.UnsafePointer(), nil
}

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

func (this *Schema) ID(model any) (any, error) {
	base, err := this.modelBase(model)
	if err != nil {
		return nil, err
	}
	return this.PK.valueOf(base), nil
}

func (this *Schema) HasID(model any) (bool, error) {
	base, err := this.modelBase(model)
	if err != nil {
		return false, err
	}
	v := reflect.NewAt(this.PK.Type, unsafe.Add(base, this.PK.Offset)).Elem()
	return !v.IsZero(), nil
}

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
	case isSignedInt(rv.Kind()) && isSignedInt(destination.Kind()):
		if destination.OverflowInt(rv.Int()) {
			return this.idAssignmentError(rv.Type(), "value overflows "+destination.Type().String())
		}
		destination.SetInt(rv.Int())
	case isUnsignedInt(rv.Kind()) && isUnsignedInt(destination.Kind()):
		if destination.OverflowUint(rv.Uint()) {
			return this.idAssignmentError(rv.Type(), "value overflows "+destination.Type().String())
		}
		destination.SetUint(rv.Uint())
	default:
		return this.idAssignmentError(rv.Type(), "cannot assign it to the primary key")
	}
	return nil
}

func (this *Schema) idAssignmentError(source reflect.Type, reason string) error {
	return &SchemaError{Model: this.Name, Field: this.PK.Name, Reason: "cannot assign " + source.String() + ": " + reason}
}

func isSignedInt(kind reflect.Kind) bool {
	return kind >= reflect.Int && kind <= reflect.Int64
}

func isUnsignedInt(kind reflect.Kind) bool {
	return kind >= reflect.Uint && kind <= reflect.Uintptr
}

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

func relationKeyValue(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer && rv.IsNil() && utils.IsOptType(rv.Type().Elem()) {
		return nil
	}
	if value, defined, null, ok := utils.Inspect(v); ok {
		if !defined || null {
			return nil
		}
		return value
	}
	return v
}

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
