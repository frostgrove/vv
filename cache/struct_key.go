package cache

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

const (
	structKeyBool byte = iota + 1
	structKeySigned
	structKeyUnsigned
	structKeyString
	structKeyBytes
	structKeyArray
	structKeyStruct
	structKeyTime
)

type structKeyPlan struct {
	fields []structKeyField
}

type structKeyField struct {
	name  string
	index []int
	value structKeyValue
}

type structKeyValue struct {
	kind    byte
	length  int
	element *structKeyValue
	fields  []structKeyField
}

type structKeyBuilder struct {
	bytes   []byte
	maximum int
}

var structKeyTimeType = reflect.TypeFor[time.Time]()

func StructKey[K any](version KeyVersion) (KeyCodec[K], error) {
	if version == 0 {
		return nil, failure("build struct key", fmt.Errorf("%w: key version is zero", ErrInvalid))
	}
	typeOf := reflect.TypeFor[K]()
	if typeOf.Kind() != reflect.Struct || typeOf == structKeyTimeType {
		return nil, failure("build struct key", fmt.Errorf("%w: key type must be a struct", ErrInvalid))
	}
	fields, err := buildStructKeyFields(typeOf, make(map[reflect.Type]bool))
	if err != nil {
		return nil, failure("build struct key", err)
	}
	plan := structKeyPlan{fields: fields}
	return KeyFunc(version, func(key K, limit KeyLimit) ([]byte, error) {
		builder := structKeyBuilder{maximum: limit.MaxBytes}
		if err := builder.appendStruct(plan.fields, reflect.ValueOf(key)); err != nil {
			return nil, err
		}
		return builder.bytes, nil
	})
}

func MustStructKey[K any](version KeyVersion) KeyCodec[K] {
	codec, err := StructKey[K](version)
	if err != nil {
		panic(err)
	}
	return codec
}

func buildStructKeyFields(typeOf reflect.Type, active map[reflect.Type]bool) ([]structKeyField, error) {
	if active[typeOf] {
		return nil, fmt.Errorf("%w: recursive key type", ErrInvalid)
	}
	active[typeOf] = true
	defer delete(active, typeOf)
	fields := make([]structKeyField, 0, typeOf.NumField())
	names := make(map[string]struct{}, typeOf.NumField())
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		name, tagged := field.Tag.Lookup("cachekey")
		if !tagged {
			return nil, fmt.Errorf("%w: field %s needs a stable cachekey tag", ErrInvalid, field.Name)
		}
		if name == "-" {
			continue
		}
		if !field.IsExported() || validNamespacePart(name) != nil || strings.Contains(name, ",") {
			return nil, fmt.Errorf("%w: field %s has an invalid cachekey tag", ErrInvalid, field.Name)
		}
		if _, exists := names[name]; exists {
			return nil, fmt.Errorf("%w: duplicate cachekey tag", ErrInvalid)
		}
		value, err := buildStructKeyValue(field.Type, active)
		if err != nil {
			return nil, fmt.Errorf("%w: field %s has an unstable type", err, field.Name)
		}
		names[name] = struct{}{}
		fields = append(fields, structKeyField{name: strings.Clone(name), index: append([]int(nil), field.Index...), value: value})
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: key struct has no encoded fields", ErrInvalid)
	}
	sort.Slice(fields, func(left, right int) bool { return fields[left].name < fields[right].name })
	return fields, nil
}

func buildStructKeyValue(typeOf reflect.Type, active map[reflect.Type]bool) (structKeyValue, error) {
	if typeOf == structKeyTimeType {
		return structKeyValue{kind: structKeyTime}, nil
	}
	switch typeOf.Kind() {
	case reflect.Bool:
		return structKeyValue{kind: structKeyBool}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return structKeyValue{kind: structKeySigned}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return structKeyValue{kind: structKeyUnsigned}, nil
	case reflect.String:
		return structKeyValue{kind: structKeyString}, nil
	case reflect.Slice:
		if typeOf.Elem().Kind() != reflect.Uint8 {
			return structKeyValue{}, fmt.Errorf("%w: only byte slices are stable", ErrInvalid)
		}
		return structKeyValue{kind: structKeyBytes}, nil
	case reflect.Array:
		element, err := buildStructKeyValue(typeOf.Elem(), active)
		if err != nil {
			return structKeyValue{}, err
		}
		return structKeyValue{kind: structKeyArray, length: typeOf.Len(), element: &element}, nil
	case reflect.Struct:
		fields, err := buildStructKeyFields(typeOf, active)
		if err != nil {
			return structKeyValue{}, err
		}
		return structKeyValue{kind: structKeyStruct, fields: fields}, nil
	default:
		return structKeyValue{}, fmt.Errorf("%w: unsupported key kind", ErrInvalid)
	}
}

func (builder *structKeyBuilder) appendStruct(fields []structKeyField, value reflect.Value) error {
	if err := builder.appendByte(structKeyStruct); err != nil {
		return err
	}
	if err := builder.appendUint32(len(fields)); err != nil {
		return err
	}
	for _, field := range fields {
		if err := builder.appendString(field.name); err != nil {
			return err
		}
		if err := builder.appendValue(field.value, value.FieldByIndex(field.index)); err != nil {
			return err
		}
	}
	return nil
}

func (builder *structKeyBuilder) appendValue(plan structKeyValue, value reflect.Value) error {
	if plan.kind == structKeyStruct {
		return builder.appendStruct(plan.fields, value)
	}
	if err := builder.appendByte(plan.kind); err != nil {
		return err
	}
	switch plan.kind {
	case structKeyBool:
		if value.Bool() {
			return builder.appendByte(1)
		}
		return builder.appendByte(0)
	case structKeySigned:
		return builder.appendUint64(uint64(value.Int()))
	case structKeyUnsigned:
		return builder.appendUint64(value.Uint())
	case structKeyString:
		return builder.appendString(value.String())
	case structKeyBytes:
		if value.IsNil() {
			return builder.appendByte(0)
		}
		if err := builder.appendByte(1); err != nil {
			return err
		}
		return builder.appendBytes(value.Bytes())
	case structKeyArray:
		if err := builder.appendUint32(plan.length); err != nil {
			return err
		}
		for index := 0; index < plan.length; index++ {
			if err := builder.appendValue(*plan.element, value.Index(index)); err != nil {
				return err
			}
		}
		return nil
	case structKeyTime:
		timestamp := normalizedTime(value.Interface().(time.Time))
		if timestamp.IsZero() {
			return ErrInvalid
		}
		return builder.appendUint64(uint64(timestamp.UnixNano()))
	default:
		return ErrInvalid
	}
}

func (builder *structKeyBuilder) appendByte(value byte) error {
	if builder.maximum <= 0 || len(builder.bytes) >= builder.maximum {
		return ErrTooLarge
	}
	builder.bytes = append(builder.bytes, value)
	return nil
}

func (builder *structKeyBuilder) appendUint32(value int) error {
	if value < 0 || uint64(value) > uint64(^uint32(0)) || builder.maximum-len(builder.bytes) < 4 {
		return ErrTooLarge
	}
	builder.bytes = binary.BigEndian.AppendUint32(builder.bytes, uint32(value))
	return nil
}

func (builder *structKeyBuilder) appendUint64(value uint64) error {
	if builder.maximum-len(builder.bytes) < 8 {
		return ErrTooLarge
	}
	builder.bytes = binary.BigEndian.AppendUint64(builder.bytes, value)
	return nil
}

func (builder *structKeyBuilder) appendString(value string) error {
	if len(value) > builder.maximum-len(builder.bytes)-4 {
		return ErrTooLarge
	}
	if err := builder.appendUint32(len(value)); err != nil {
		return err
	}
	builder.bytes = append(builder.bytes, value...)
	return nil
}

func (builder *structKeyBuilder) appendBytes(value []byte) error {
	if len(value) > builder.maximum-len(builder.bytes)-4 {
		return ErrTooLarge
	}
	if err := builder.appendUint32(len(value)); err != nil {
		return err
	}
	builder.bytes = append(builder.bytes, value...)
	return nil
}
