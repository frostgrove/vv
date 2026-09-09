package i18n

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type ArgumentType uint8

const (
	TypeText ArgumentType = iota + 1
	TypeBool
	TypeInteger
	TypeUnsignedInteger
	TypeBigInteger
	TypeDecimal
	TypeMoney
	TypeDate
	TypeInstant
	TypeEnum
)

func (t ArgumentType) String() string {
	switch t {
	case TypeText:
		return "text"
	case TypeBool:
		return "bool"
	case TypeInteger:
		return "integer"
	case TypeUnsignedInteger:
		return "unsigned_integer"
	case TypeBigInteger:
		return "big_integer"
	case TypeDecimal:
		return "decimal"
	case TypeMoney:
		return "money"
	case TypeDate:
		return "date"
	case TypeInstant:
		return "instant"
	case TypeEnum:
		return "enum"
	default:
		return "unknown"
	}
}

func (t ArgumentType) Valid() bool { return t >= TypeText && t <= TypeEnum }

type ArgumentSpec struct {
	Name     string
	Type     ArgumentType
	Required bool
	Nullable bool
	Values   []string
}

type Argument struct {
	name     string
	typeOf   ArgumentType
	null     bool
	invalid  string
	text     string
	boolean  bool
	signed   int64
	unsigned uint64
	big      *big.Int
	date     DateValue
	instant  time.Time
}

type DateValue struct {
	Year  int
	Month time.Month
	Day   int
}

func Text(name, value string) Argument {
	return Argument{name: name, typeOf: TypeText, text: value}
}

func Bool(name string, value bool) Argument {
	return Argument{name: name, typeOf: TypeBool, boolean: value}
}

func Integer(name string, value int64) Argument {
	return Argument{name: name, typeOf: TypeInteger, signed: value}
}

func UnsignedInteger(name string, value uint64) Argument {
	return Argument{name: name, typeOf: TypeUnsignedInteger, unsigned: value}
}

func BigInteger(name string, value *big.Int) Argument {
	argument := Argument{name: name, typeOf: TypeBigInteger}
	if value == nil {
		argument.invalid = "big integer is nil"
		return argument
	}
	if value.BitLen() > maxBigIntegerBits {
		argument.invalid = "big integer exceeds the supported size"
		return argument
	}
	argument.big = new(big.Int).Set(value)
	return argument
}

func Decimal(name, lexical string) Argument {
	argument := Argument{name: name, typeOf: TypeDecimal, text: lexical}
	if err := validateDecimal(lexical); err != nil {
		argument.invalid = err.Error()
	}
	return argument
}

func Money(name, lexical, currency string) Argument {
	argument := Argument{name: name, typeOf: TypeMoney}
	if len(lexical) > maxDecimalBytes {
		argument.invalid = "decimal is empty or too large"
		return argument
	}
	if !validCurrency(currency) {
		argument.invalid = "currency must contain three ASCII letters"
		return argument
	}
	if err := validateDecimal(lexical); err != nil {
		argument.invalid = err.Error()
		return argument
	}
	argument.text = lexical + "\x00" + strings.ToUpper(currency)
	return argument
}

func Date(name string, year int, month time.Month, day int) Argument {
	argument := Argument{name: name, typeOf: TypeDate, date: DateValue{Year: year, Month: month, Day: day}}
	if year < 1 || year > 9999 || month < time.January || month > time.December || day < 1 || day > 31 {
		argument.invalid = "date is outside the supported Gregorian range"
		return argument
	}
	check := time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
	if check.Year() != year || check.Month() != month || check.Day() != day {
		argument.invalid = "date does not exist"
	}
	return argument
}

func Instant(name string, value time.Time) Argument {
	argument := Argument{name: name, typeOf: TypeInstant, instant: value.Round(0)}
	if value.IsZero() {
		argument.invalid = "instant is zero"
	}
	return argument
}

func Enum(name, value string) Argument {
	return Argument{name: name, typeOf: TypeEnum, text: value}
}

func Null(name string) Argument {
	return Argument{name: name, null: true}
}

func (a Argument) Name() string {
	return a.name
}

func (a Argument) Type() ArgumentType {
	return a.typeOf
}

func (a Argument) IsNull() bool {
	return a.null
}

func cloneArgument(argument Argument) Argument {
	clone := argument
	if argument.big != nil {
		clone.big = new(big.Int).Set(argument.big)
	}
	return clone
}

type Message struct {
	key       Key
	revision  string
	digest    string
	arguments []Argument
}

func (m Message) Key() Key {
	return m.key
}

func (m Message) ContractRevision() string {
	return m.revision
}

func (m Message) Arguments() []Argument {
	if len(m.arguments) == 0 {
		return nil
	}
	arguments := make([]Argument, len(m.arguments))
	for i := range m.arguments {
		arguments[i] = cloneArgument(m.arguments[i])
	}
	return arguments
}

type ArgumentEncoder[A any] func(A) ([]Argument, error)

type ContractRef struct {
	Key      Key
	Revision string
	Digest   string
}

type DefinitionSpec[A any] struct {
	Contract ContractRef
	Encode   ArgumentEncoder[A]
}

type Definition[A any] struct {
	snapshot *Snapshot
	key      Key
	encode   ArgumentEncoder[A]
}

func NewDefinition[A any](snapshot *Snapshot, key Key, encode ArgumentEncoder[A]) (Definition[A], error) {
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
	return Define(snapshot, DefinitionSpec[A]{Contract: contract, Encode: encode})
}

func Define[A any](snapshot *Snapshot, spec DefinitionSpec[A]) (Definition[A], error) {
	if spec.Encode == nil {
		return Definition[A]{}, fmt.Errorf("%w: definition is incomplete", ErrInvalidMessage)
	}
	if _, err := definitionRecord(snapshot, spec.Contract); err != nil {
		return Definition[A]{}, err
	}
	return Definition[A]{snapshot: snapshot, key: spec.Contract.Key, encode: spec.Encode}, nil
}

func definitionRecord(snapshot *Snapshot, contract ContractRef) (*messageRecord, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("%w: definition is incomplete", ErrInvalidMessage)
	}
	if err := validateLookupKey(contract.Key, snapshot.limits.MaxIdentifierBytes); err != nil {
		return nil, err
	}
	if contract.Revision == "" || contract.Digest == "" {
		return nil, fmt.Errorf("%w: definition is incomplete", ErrInvalidMessage)
	}
	record, ok := snapshot.records[contract.Key]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, contract.Key)
	}
	if contract.Revision != record.descriptor.Revision || contract.Digest != record.contractHash {
		return nil, fmt.Errorf("%w: definition contract does not match the snapshot", ErrInvalidMessage)
	}
	return record, nil
}

func (d Definition[A]) Bind(value A) (Message, error) {
	if d.snapshot == nil || d.encode == nil {
		return Message{}, fmt.Errorf("%w: definition is incomplete", ErrInvalidMessage)
	}
	arguments, err := d.encode(value)
	if err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	return d.snapshot.Bind(d.key, arguments...)
}

func (d Definition[A]) Key() Key {
	return d.key
}

func checkArguments(specs []ArgumentSpec, supplied []Argument, limits Limits) ([]Argument, error) {
	if len(supplied) > limits.MaxArguments {
		return nil, fmt.Errorf("%w: arguments exceed %d", ErrLimitExceeded, limits.MaxArguments)
	}
	byName := make(map[string]Argument, len(supplied))
	totalBytes := 0
	for i, argument := range supplied {
		if len(argument.name) > limits.MaxArgumentBytes-totalBytes {
			return nil, fmt.Errorf("%w: arguments exceed %d bytes", ErrLimitExceeded, limits.MaxArgumentBytes)
		}
		if argument.name == "" || !validIdentifier(argument.name, limits.MaxIdentifierBytes) {
			return nil, fmt.Errorf("%w: argument %d has an invalid name", ErrInvalidMessage, i)
		}
		if _, exists := byName[argument.name]; exists {
			return nil, fmt.Errorf("%w: argument %q is duplicated", ErrInvalidMessage, argument.name)
		}
		totalBytes += len(argument.name)
		if len(argument.text) > limits.MaxArgumentBytes-totalBytes {
			return nil, fmt.Errorf("%w: arguments exceed %d bytes", ErrLimitExceeded, limits.MaxArgumentBytes)
		}
		totalBytes += len(argument.text)
		if argument.invalid != "" {
			return nil, fmt.Errorf("%w: argument %q: %s", ErrInvalidMessage, argument.name, argument.invalid)
		}
		if !argument.null && !utf8.ValidString(argument.text) {
			return nil, fmt.Errorf("%w: argument %q is not valid UTF-8", ErrInvalidMessage, argument.name)
		}
		if !argument.null && hasUnsafeBidiControls(argument.text) {
			return nil, fmt.Errorf("%w: argument %q contains directional controls", ErrInvalidMessage, argument.name)
		}
		if argument.big != nil {
			if argument.big.BitLen() > limits.MaxBigIntegerBits || decimalIntegerBytes(argument.big) > limits.MaxArgumentBytes-totalBytes {
				return nil, fmt.Errorf("%w: arguments exceed %d bytes", ErrLimitExceeded, limits.MaxArgumentBytes)
			}
			totalBytes += len(argument.big.String())
		}
		if totalBytes > limits.MaxArgumentBytes {
			return nil, fmt.Errorf("%w: arguments exceed %d bytes", ErrLimitExceeded, limits.MaxArgumentBytes)
		}
		byName[argument.name] = cloneArgument(argument)
	}

	out := make([]Argument, 0, len(supplied))
	for _, spec := range specs {
		argument, exists := byName[spec.Name]
		if !exists {
			if spec.Required {
				return nil, fmt.Errorf("%w: required argument %q is absent", ErrInvalidMessage, spec.Name)
			}
			continue
		}
		delete(byName, spec.Name)
		if argument.null {
			if !spec.Nullable {
				return nil, fmt.Errorf("%w: argument %q does not allow null", ErrInvalidMessage, spec.Name)
			}
			argument.typeOf = spec.Type
			out = append(out, argument)
			continue
		}
		if argument.typeOf != spec.Type {
			return nil, fmt.Errorf("%w: argument %q is %s, expected %s", ErrInvalidMessage, spec.Name, argument.typeOf, spec.Type)
		}
		if spec.Type == TypeEnum && !slices.Contains(spec.Values, argument.text) {
			return nil, fmt.Errorf("%w: argument %q has an undeclared enum value", ErrInvalidMessage, spec.Name)
		}
		out = append(out, argument)
	}
	if len(byName) != 0 {
		names := make([]string, 0, len(byName))
		for name := range byName {
			names = append(names, name)
		}
		slices.Sort(names)
		return nil, fmt.Errorf("%w: undeclared argument %q", ErrInvalidMessage, names[0])
	}
	return out, nil
}

const maxBigIntegerBits = 16 << 20

func decimalIntegerBytes(value *big.Int) int {
	if value == nil || value.Sign() == 0 {
		return 1
	}
	bits := value.BitLen()
	bytes := (bits*30103)/100000 + 1
	if value.Sign() < 0 {
		bytes++
	}
	return bytes
}

func hasUnsafeBidiControls(value string) bool {
	for _, r := range value {
		if r == '\u061c' || r == '\u200e' || r == '\u200f' || r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069' {
			return true
		}
	}
	return false
}

func hasUnsafeAuthoredBidiControls(value string) bool {
	depth := 0
	for _, r := range value {
		switch {
		case r >= '\u202a' && r <= '\u202e':
			return true
		case r >= '\u2066' && r <= '\u2068':
			depth++
			if depth > 64 {
				return true
			}
		case r == '\u2069':
			if depth == 0 {
				return true
			}
			depth--
		}
	}
	return depth != 0
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}
