package i18n

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type optionalStructArgument interface {
	i18nOptional() (any, uint8)
	i18nOptionalType() reflect.Type
	i18nOptionalContainerType() reflect.Type
}

type structArgumentField struct {
	index     int
	spec      ArgumentSpec
	valueType reflect.Type
	optional  bool
}

var (
	errMalformedStructTag      = errors.New("malformed struct tag")
	errDuplicateI18nStructTag  = errors.New("duplicate i18n struct tag")
	optionalStructArgumentType = reflect.TypeFor[optionalStructArgument]()
	bigIntegerType             = reflect.TypeFor[*big.Int]()
	moneyValueType             = reflect.TypeFor[MoneyValue]()
	dateValueType              = reflect.TypeFor[DateValue]()
	instantValueType           = reflect.TypeFor[time.Time]()
)

func DefineStruct[A any](snapshot *Snapshot, contract ContractRef) (Definition[A], error) {
	record, err := definitionRecord(snapshot, contract)
	if err != nil {
		return Definition[A]{}, err
	}
	encode, err := compileStructArgumentEncoder[A](record.descriptor, snapshot.limits)
	if err != nil {
		return Definition[A]{}, err
	}
	return Definition[A]{snapshot: snapshot, key: contract.Key, encode: encode}, nil
}

func NewStructDefinition[A any](snapshot *Snapshot, key Key) (Definition[A], error) {
	if snapshot == nil {
		return Definition[A]{}, fmt.Errorf("%w: definition is incomplete", ErrInvalidMessage)
	}
	if err := validateLookupKey(key, snapshot.limits.MaxIdentifierBytes); err != nil {
		return Definition[A]{}, err
	}
	contract, ok := snapshot.ContractRef(key)
	if !ok {
		return Definition[A]{}, fmt.Errorf("%w: %q", ErrNotFound, key)
	}
	return DefineStruct[A](snapshot, contract)
}

func compileStructArgumentEncoder[A any](descriptor Descriptor, limits Limits) (_ ArgumentEncoder[A], err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: struct definition is invalid", ErrInvalidMessage)
		}
	}()

	shape := reflect.TypeFor[A]()
	if shape.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: definition arguments must be a struct, got %s", ErrInvalidMessage, shape.Kind())
	}
	fields, err := compileStructArgumentFields(shape, descriptor, limits)
	if err != nil {
		return nil, err
	}

	return func(value A) (arguments []Argument, encodeErr error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				arguments = nil
				encodeErr = fmt.Errorf("%w: struct argument encoding failed", ErrInvalidMessage)
			}
		}()

		reflected := reflect.ValueOf(value)
		if !reflected.IsValid() || reflected.Type() != shape {
			return nil, fmt.Errorf("%w: definition argument has an invalid type", ErrInvalidMessage)
		}
		arguments = make([]Argument, 0, len(fields))
		for _, binding := range fields {
			fieldValue := reflected.Field(binding.index)
			if !binding.optional {
				arguments = append(arguments, argumentFromStructValue(binding.spec, fieldValue))
				continue
			}
			optional := fieldValue.Interface().(optionalStructArgument)
			value, state := optional.i18nOptional()
			switch state {
			case 0:
				continue
			case 1:
				reflectedValue := reflect.ValueOf(value)
				if !reflectedValue.IsValid() || reflectedValue.Type() != binding.valueType {
					return nil, fmt.Errorf("%w: argument %q has an invalid optional value", ErrInvalidMessage, binding.spec.Name)
				}
				arguments = append(arguments, argumentFromStructValue(binding.spec, reflectedValue))
			case 2:
				arguments = append(arguments, Null(binding.spec.Name))
			default:
				return nil, fmt.Errorf("%w: argument %q has an invalid optional state", ErrInvalidMessage, binding.spec.Name)
			}
		}
		return arguments, nil
	}, nil
}

func compileStructArgumentFields(shape reflect.Type, descriptor Descriptor, limits Limits) ([]structArgumentField, error) {
	if shape.NumField() > limits.MaxArguments {
		return nil, fmt.Errorf("%w: definition struct exceeds %d fields", ErrLimitExceeded, limits.MaxArguments)
	}

	byName := make(map[string]int, len(descriptor.Arguments))
	byFold := make(map[string][]int, len(descriptor.Arguments))
	for index, spec := range descriptor.Arguments {
		byName[spec.Name] = index
		folded := foldStructArgumentName(spec.Name)
		byFold[folded] = append(byFold[folded], index)
	}

	fields := make([]structArgumentField, len(descriptor.Arguments))
	assigned := make([]bool, len(descriptor.Arguments))
	for fieldIndex := 0; fieldIndex < shape.NumField(); fieldIndex++ {
		field := shape.Field(fieldIndex)
		fieldReference := boundedStructFieldReference(fieldIndex, field.Name, limits.MaxIdentifierBytes)
		if len(field.Tag) > limits.MaxDescriptionBytes {
			return nil, fmt.Errorf("%w: %s has struct tags exceeding %d bytes", ErrLimitExceeded, fieldReference, limits.MaxDescriptionBytes)
		}
		tag, tagged, tagErr := parseI18nStructTag(field.Tag)
		if tagErr != nil {
			return nil, fmt.Errorf("%w: %s has %v", ErrInvalidMessage, fieldReference, tagErr)
		}
		if tagged && tag == "-" {
			continue
		}
		if tagged && (tag == "" || strings.Contains(tag, ",") || !validIdentifier(tag, limits.MaxIdentifierBytes)) {
			return nil, fmt.Errorf("%w: %s has an invalid i18n tag", ErrInvalidMessage, fieldReference)
		}
		if field.Anonymous {
			if tagged {
				return nil, fmt.Errorf("%w: embedded %s cannot bind an argument", ErrInvalidMessage, fieldReference)
			}
			continue
		}
		if field.PkgPath != "" {
			if tagged {
				return nil, fmt.Errorf("%w: tagged %s is not exported", ErrInvalidMessage, fieldReference)
			}
			continue
		}

		argumentIndex := -1
		if tagged {
			var ok bool
			argumentIndex, ok = byName[tag]
			if !ok {
				return nil, fmt.Errorf("%w: %s binds undeclared argument %q", ErrInvalidMessage, fieldReference, tag)
			}
		} else {
			if len(field.Name) > limits.MaxIdentifierBytes {
				continue
			}
			matches := byFold[foldStructArgumentName(field.Name)]
			switch len(matches) {
			case 0:
				continue
			case 1:
				argumentIndex = matches[0]
			default:
				return nil, fmt.Errorf("%w: %s matches more than one argument; add an i18n tag", ErrInvalidMessage, fieldReference)
			}
		}

		if assigned[argumentIndex] {
			return nil, fmt.Errorf("%w: argument %q is bound more than once", ErrInvalidMessage, descriptor.Arguments[argumentIndex].Name)
		}
		binding, bindingErr := compileStructArgumentField(fieldIndex, fieldReference, field.Type, descriptor.Arguments[argumentIndex])
		if bindingErr != nil {
			return nil, bindingErr
		}
		fields[argumentIndex] = binding
		assigned[argumentIndex] = true
	}

	for index, ok := range assigned {
		if !ok {
			return nil, fmt.Errorf("%w: argument %q has no struct field", ErrInvalidMessage, descriptor.Arguments[index].Name)
		}
	}

	return fields, nil
}

func parseI18nStructTag(tag reflect.StructTag) (string, bool, error) {
	remaining := string(tag)
	value := ""
	present := false
	for remaining != "" {
		space := 0
		for space < len(remaining) && remaining[space] == ' ' {
			space++
		}
		remaining = remaining[space:]
		if remaining == "" {
			break
		}
		colon := strings.IndexByte(remaining, ':')
		if colon <= 0 || colon+1 >= len(remaining) || remaining[colon+1] != '"' || !validStructTagKey(remaining[:colon]) {
			return "", false, errMalformedStructTag
		}
		name := remaining[:colon]
		quoted, err := strconv.QuotedPrefix(remaining[colon+1:])
		if err != nil || len(quoted) == 0 || quoted[0] != '"' {
			return "", false, errMalformedStructTag
		}
		decoded, err := strconv.Unquote(quoted)
		if err != nil {
			return "", false, errMalformedStructTag
		}
		remaining = remaining[colon+1+len(quoted):]
		if name != "i18n" {
			continue
		}
		if present {
			return "", false, errDuplicateI18nStructTag
		}
		value = decoded
		present = true
	}
	return value, present, nil
}

func validStructTagKey(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == ' ' || character == '"' || character == ':' || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func compileStructArgumentField(index int, fieldReference string, fieldType reflect.Type, spec ArgumentSpec) (structArgumentField, error) {
	valueType := fieldType
	optional := valueType.Implements(optionalStructArgumentType)
	expectsOptional := !spec.Required || spec.Nullable
	if optional != expectsOptional {
		shape := "a direct value"
		if expectsOptional {
			shape = "i18n.Optional"
		}
		return structArgumentField{}, fmt.Errorf("%w: %s for argument %q must use %s", ErrInvalidMessage, fieldReference, spec.Name, shape)
	}
	if optional {
		var exact bool
		valueType, exact = exactOptionalStructArgumentType(valueType)
		if !exact {
			return structArgumentField{}, fmt.Errorf("%w: %s for argument %q must use an exact i18n.Optional type", ErrInvalidMessage, fieldReference, spec.Name)
		}
	}
	if !validStructArgumentType(spec.Type, valueType) {
		return structArgumentField{}, fmt.Errorf("%w: %s has an incompatible Go type for %s argument %q", ErrInvalidMessage, fieldReference, spec.Type, spec.Name)
	}
	return structArgumentField{index: index, spec: spec, valueType: valueType, optional: optional}, nil
}

func exactOptionalStructArgumentType(containerType reflect.Type) (_ reflect.Type, exact bool) {
	if containerType.Kind() != reflect.Struct {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			exact = false
		}
	}()
	marker := reflect.Zero(containerType).Interface().(optionalStructArgument)
	if marker.i18nOptionalContainerType() != containerType {
		return nil, false
	}
	return marker.i18nOptionalType(), true
}

func boundedStructFieldReference(index int, name string, maximum int) string {
	if len(name) > maximum {
		return fmt.Sprintf("field[%d]", index)
	}
	return fmt.Sprintf("field[%d] %q", index, name)
}

func validStructArgumentType(argumentType ArgumentType, valueType reflect.Type) bool {
	switch argumentType {
	case TypeText, TypeDecimal, TypeEnum:
		return valueType.Kind() == reflect.String
	case TypeBool:
		return valueType.Kind() == reflect.Bool
	case TypeInteger:
		return valueType.Kind() == reflect.Int64
	case TypeUnsignedInteger:
		return valueType.Kind() == reflect.Uint64
	case TypeBigInteger:
		return valueType == bigIntegerType
	case TypeMoney:
		return valueType == moneyValueType
	case TypeDate:
		return valueType == dateValueType
	case TypeInstant:
		return valueType == instantValueType
	default:
		return false
	}
}

func argumentFromStructValue(spec ArgumentSpec, value reflect.Value) Argument {
	switch spec.Type {
	case TypeText:
		return Text(spec.Name, value.String())
	case TypeBool:
		return Bool(spec.Name, value.Bool())
	case TypeInteger:
		return Integer(spec.Name, value.Int())
	case TypeUnsignedInteger:
		return UnsignedInteger(spec.Name, value.Uint())
	case TypeBigInteger:
		return BigInteger(spec.Name, value.Interface().(*big.Int))
	case TypeDecimal:
		return Decimal(spec.Name, value.String())
	case TypeMoney:
		money := value.Interface().(MoneyValue)
		return Money(spec.Name, string(money.Amount), money.Currency)
	case TypeDate:
		date := value.Interface().(DateValue)
		return Date(spec.Name, date.Year, date.Month, date.Day)
	case TypeInstant:
		return Instant(spec.Name, value.Interface().(time.Time))
	case TypeEnum:
		return Enum(spec.Name, value.String())
	default:
		return Argument{name: spec.Name, invalid: "unsupported argument type"}
	}
}

func foldStructArgumentName(value string) string {
	var folded strings.Builder
	folded.Grow(len(value))
	for _, character := range value {
		switch character {
		case '_', '-', ' ':
			continue
		}
		if character >= 'A' && character <= 'Z' {
			character += 'a' - 'A'
		}
		folded.WriteRune(character)
	}
	return folded.String()
}
