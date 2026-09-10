package i18n

import "reflect"

type Optional[T any] struct {
	value T
	state uint8
}

func Some[T any](value T) Optional[T] {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return NullValue[T]()
	}
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if reflected.IsNil() {
			return NullValue[T]()
		}
	}
	return Optional[T]{value: value, state: 1}
}

func NullValue[T any]() Optional[T] {
	return Optional[T]{state: 2}
}

func (o Optional[T]) Present() bool {
	return o.state == 1 || o.state == 2
}

func (o Optional[T]) IsNull() bool {
	return o.state == 2
}

func (o Optional[T]) Get() (T, bool) {
	return o.value, o.state == 1
}

func (o Optional[T]) i18nOptional() (any, uint8) {
	return o.value, o.state
}

func (o Optional[T]) i18nOptionalType() reflect.Type {
	return reflect.TypeFor[T]()
}

func (o Optional[T]) i18nOptionalContainerType() reflect.Type {
	return reflect.TypeFor[Optional[T]]()
}
