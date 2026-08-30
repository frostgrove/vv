// Package nilvalue centralises the interface-aware nil check used at extension
// seams. A plain interface comparison does not recognise a typed-nil pointer or
// function held by an interface.
package nilvalue

import "reflect"

// Is reports whether value is nil, including a nil-able dynamic value carried
// by a non-nil interface.
func Is(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return v.IsNil()
	default:
		return false
	}
}
