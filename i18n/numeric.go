package i18n

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"strconv"
	"strings"

	"github.com/agentable/go-intl/locale"
	"github.com/agentable/go-intl/numberformat"
	"github.com/agentable/go-intl/pluralrules"
	"github.com/kaptinlin/messageformat-go/pkg/bidi"
	"github.com/kaptinlin/messageformat-go/pkg/functions"
	"github.com/kaptinlin/messageformat-go/pkg/messagevalue"
)

type NumericValue interface {
	isNumericValue()
}

type SignedNumber int64

func (SignedNumber) isNumericValue() {}

type UnsignedNumber uint64

func (UnsignedNumber) isNumericValue() {}

type DecimalNumber string

func (DecimalNumber) isNumericValue() {}

type BigIntegerNumber struct {
	value   *big.Int
	invalid string
}

func NewBigIntegerNumber(value *big.Int) BigIntegerNumber {
	if value == nil {
		return BigIntegerNumber{invalid: "big integer is nil"}
	}
	if value.BitLen() > maxBigIntegerBits {
		return BigIntegerNumber{invalid: "big integer exceeds the supported size"}
	}
	return BigIntegerNumber{value: new(big.Int).Set(value)}
}

func (BigIntegerNumber) isNumericValue() {}

type DigitRange struct {
	minimum    int
	maximum    int
	minimumSet bool
	maximumSet bool
}

func Digits(minimum, maximum int) DigitRange {
	return DigitRange{minimum: minimum, maximum: maximum, minimumSet: true, maximumSet: true}
}

func MinimumDigits(minimum int) DigitRange {
	return DigitRange{minimum: minimum, minimumSet: true}
}

func MaximumDigits(maximum int) DigitRange {
	return DigitRange{maximum: maximum, maximumSet: true}
}

func (r DigitRange) Minimum() int { return r.minimum }

func (r DigitRange) Maximum() int { return r.maximum }

type NumberGrouping uint8

const (
	NumberGroupingDefault NumberGrouping = iota
	NumberGroupingAlways
	NumberGroupingNever
	NumberGroupingMin2
)

func (g NumberGrouping) String() string {
	switch g {
	case NumberGroupingDefault:
		return "default"
	case NumberGroupingAlways:
		return "always"
	case NumberGroupingNever:
		return "never"
	case NumberGroupingMin2:
		return "min2"
	default:
		return "unknown"
	}
}

func (g NumberGrouping) Valid() bool { return g <= NumberGroupingMin2 }

type NumberSignDisplay uint8

const (
	NumberSignDefault NumberSignDisplay = iota
	NumberSignAlways
	NumberSignNever
	NumberSignExceptZero
	NumberSignNegative
)

func (s NumberSignDisplay) String() string {
	switch s {
	case NumberSignDefault:
		return "default"
	case NumberSignAlways:
		return "always"
	case NumberSignNever:
		return "never"
	case NumberSignExceptZero:
		return "exceptZero"
	case NumberSignNegative:
		return "negative"
	default:
		return "unknown"
	}
}

func (s NumberSignDisplay) Valid() bool { return s <= NumberSignNegative }

type NumberNotation uint8

const (
	NumberNotationDefault NumberNotation = iota
	NumberNotationScientific
	NumberNotationEngineering
	NumberNotationCompact
)

func (n NumberNotation) String() string {
	switch n {
	case NumberNotationDefault:
		return "default"
	case NumberNotationScientific:
		return "scientific"
	case NumberNotationEngineering:
		return "engineering"
	case NumberNotationCompact:
		return "compact"
	default:
		return "unknown"
	}
}

func (n NumberNotation) Valid() bool { return n <= NumberNotationCompact }

type NumberCompactDisplay uint8

const (
	NumberCompactDefault NumberCompactDisplay = iota
	NumberCompactShort
	NumberCompactLong
)

func (d NumberCompactDisplay) String() string {
	switch d {
	case NumberCompactDefault:
		return "default"
	case NumberCompactShort:
		return "short"
	case NumberCompactLong:
		return "long"
	default:
		return "unknown"
	}
}

func (d NumberCompactDisplay) Valid() bool { return d <= NumberCompactLong }

type NumberRoundingMode uint8

const (
	NumberRoundingModeDefault NumberRoundingMode = iota
	NumberRoundingModeCeil
	NumberRoundingModeFloor
	NumberRoundingModeExpand
	NumberRoundingModeTrunc
	NumberRoundingModeHalfCeil
	NumberRoundingModeHalfFloor
	NumberRoundingModeHalfExpand
	NumberRoundingModeHalfTrunc
	NumberRoundingModeHalfEven
)

func (m NumberRoundingMode) String() string {
	switch m {
	case NumberRoundingModeDefault:
		return "default"
	case NumberRoundingModeCeil:
		return "ceil"
	case NumberRoundingModeFloor:
		return "floor"
	case NumberRoundingModeExpand:
		return "expand"
	case NumberRoundingModeTrunc:
		return "trunc"
	case NumberRoundingModeHalfCeil:
		return "halfCeil"
	case NumberRoundingModeHalfFloor:
		return "halfFloor"
	case NumberRoundingModeHalfExpand:
		return "halfExpand"
	case NumberRoundingModeHalfTrunc:
		return "halfTrunc"
	case NumberRoundingModeHalfEven:
		return "halfEven"
	default:
		return "unknown"
	}
}

func (m NumberRoundingMode) Valid() bool { return m <= NumberRoundingModeHalfEven }

type NumberRoundingPriority uint8

const (
	NumberRoundingPriorityDefault NumberRoundingPriority = iota
	NumberRoundingPriorityAuto
	NumberRoundingPriorityMorePrecision
	NumberRoundingPriorityLessPrecision
)

func (p NumberRoundingPriority) String() string {
	switch p {
	case NumberRoundingPriorityDefault:
		return "default"
	case NumberRoundingPriorityAuto:
		return "auto"
	case NumberRoundingPriorityMorePrecision:
		return "morePrecision"
	case NumberRoundingPriorityLessPrecision:
		return "lessPrecision"
	default:
		return "unknown"
	}
}

func (p NumberRoundingPriority) Valid() bool {
	return p <= NumberRoundingPriorityLessPrecision
}

type NumberTrailingZeroDisplay uint8

const (
	NumberTrailingZeroDefault NumberTrailingZeroDisplay = iota
	NumberTrailingZeroAuto
	NumberTrailingZeroStripIfInteger
)

func (d NumberTrailingZeroDisplay) String() string {
	switch d {
	case NumberTrailingZeroDefault:
		return "default"
	case NumberTrailingZeroAuto:
		return "auto"
	case NumberTrailingZeroStripIfInteger:
		return "stripIfInteger"
	default:
		return "unknown"
	}
}

func (d NumberTrailingZeroDisplay) Valid() bool {
	return d <= NumberTrailingZeroStripIfInteger
}

type NumberRoundingIncrement uint16

const (
	NumberRoundingIncrementDefault NumberRoundingIncrement = 0
	NumberRoundingIncrement1       NumberRoundingIncrement = 1
	NumberRoundingIncrement2       NumberRoundingIncrement = 2
	NumberRoundingIncrement5       NumberRoundingIncrement = 5
	NumberRoundingIncrement10      NumberRoundingIncrement = 10
	NumberRoundingIncrement20      NumberRoundingIncrement = 20
	NumberRoundingIncrement25      NumberRoundingIncrement = 25
	NumberRoundingIncrement50      NumberRoundingIncrement = 50
	NumberRoundingIncrement100     NumberRoundingIncrement = 100
	NumberRoundingIncrement200     NumberRoundingIncrement = 200
	NumberRoundingIncrement250     NumberRoundingIncrement = 250
	NumberRoundingIncrement500     NumberRoundingIncrement = 500
	NumberRoundingIncrement1000    NumberRoundingIncrement = 1000
	NumberRoundingIncrement2000    NumberRoundingIncrement = 2000
	NumberRoundingIncrement2500    NumberRoundingIncrement = 2500
	NumberRoundingIncrement5000    NumberRoundingIncrement = 5000
)

func (i NumberRoundingIncrement) String() string {
	if i == NumberRoundingIncrementDefault {
		return "default"
	}
	if !i.Valid() {
		return "unknown"
	}
	return strconv.FormatUint(uint64(i), 10)
}

func (i NumberRoundingIncrement) Valid() bool {
	switch i {
	case NumberRoundingIncrementDefault,
		NumberRoundingIncrement1,
		NumberRoundingIncrement2,
		NumberRoundingIncrement5,
		NumberRoundingIncrement10,
		NumberRoundingIncrement20,
		NumberRoundingIncrement25,
		NumberRoundingIncrement50,
		NumberRoundingIncrement100,
		NumberRoundingIncrement200,
		NumberRoundingIncrement250,
		NumberRoundingIncrement500,
		NumberRoundingIncrement1000,
		NumberRoundingIncrement2000,
		NumberRoundingIncrement2500,
		NumberRoundingIncrement5000:
		return true
	default:
		return false
	}
}

type NumberFormatSpec struct {
	Grouping             NumberGrouping
	Sign                 NumberSignDisplay
	Notation             NumberNotation
	Compact              NumberCompactDisplay
	RoundingMode         NumberRoundingMode
	RoundingPriority     NumberRoundingPriority
	TrailingZeroDisplay  NumberTrailingZeroDisplay
	RoundingIncrement    NumberRoundingIncrement
	MinimumIntegerDigits int
	FractionDigits       DigitRange
	SignificantDigits    DigitRange
	NumberingSystem      string
}

type CurrencyDisplay uint8

const (
	CurrencyDisplayDefault CurrencyDisplay = iota
	CurrencyDisplayCode
	CurrencyDisplayName
	CurrencyDisplayNarrowSymbol
)

func (d CurrencyDisplay) String() string {
	switch d {
	case CurrencyDisplayDefault:
		return "default"
	case CurrencyDisplayCode:
		return "code"
	case CurrencyDisplayName:
		return "name"
	case CurrencyDisplayNarrowSymbol:
		return "narrowSymbol"
	default:
		return "unknown"
	}
}

func (d CurrencyDisplay) Valid() bool { return d <= CurrencyDisplayNarrowSymbol }

type CurrencySign uint8

const (
	CurrencySignDefault CurrencySign = iota
	CurrencySignAccounting
)

func (s CurrencySign) String() string {
	switch s {
	case CurrencySignDefault:
		return "default"
	case CurrencySignAccounting:
		return "accounting"
	default:
		return "unknown"
	}
}

func (s CurrencySign) Valid() bool { return s <= CurrencySignAccounting }

type MoneyValue struct {
	Amount   DecimalNumber
	Currency string
}

type MoneyFormatSpec struct {
	Number  NumberFormatSpec
	Display CurrencyDisplay
	Sign    CurrencySign
}

type PercentFormatSpec struct {
	Number NumberFormatSpec
}

type UnitFormatSpec struct {
	Number NumberFormatSpec
	Unit   string
	Width  FormatWidth
}

type PluralType uint8

const (
	PluralCardinal PluralType = iota
	PluralOrdinal
)

func (t PluralType) String() string {
	switch t {
	case PluralCardinal:
		return "cardinal"
	case PluralOrdinal:
		return "ordinal"
	default:
		return "unknown"
	}
}

func (t PluralType) Valid() bool { return t <= PluralOrdinal }

type PluralCategory uint8

const (
	PluralZero PluralCategory = iota
	PluralOne
	PluralTwo
	PluralFew
	PluralMany
	PluralOther
)

func (c PluralCategory) String() string {
	switch c {
	case PluralZero:
		return "zero"
	case PluralOne:
		return "one"
	case PluralTwo:
		return "two"
	case PluralFew:
		return "few"
	case PluralMany:
		return "many"
	case PluralOther:
		return "other"
	default:
		return "unknown"
	}
}

func (c PluralCategory) Valid() bool { return c <= PluralOther }

type PluralFormatSpec struct {
	Type   PluralType
	Number NumberFormatSpec
}

type numericKind uint8

const (
	numericSigned numericKind = iota + 1
	numericUnsigned
	numericBig
	numericDecimal
)

type numericInput struct {
	kind     numericKind
	signed   int64
	unsigned uint64
	big      *big.Int
	decimal  string
}

func (n numericInput) clone() numericInput {
	clone := n
	if n.big != nil {
		clone.big = new(big.Int).Set(n.big)
	}
	return clone
}

func (n numericInput) lexical() string {
	switch n.kind {
	case numericSigned:
		return strconv.FormatInt(n.signed, 10)
	case numericUnsigned:
		return strconv.FormatUint(n.unsigned, 10)
	case numericBig:
		if n.big != nil {
			return n.big.String()
		}
	case numericDecimal:
		return n.decimal
	}
	return "0"
}

func (n numericInput) numberValue() (numberformat.Value, error) {
	switch n.kind {
	case numericSigned:
		return numberformat.Int(n.signed), nil
	case numericUnsigned:
		return numberformat.Uint(n.unsigned), nil
	case numericBig:
		if n.big == nil {
			return numberformat.Value{}, errors.New("nil big integer")
		}
		return numberformat.BigInt(n.big), nil
	case numericDecimal:
		return numberformat.Decimal(n.decimal)
	default:
		return numberformat.Value{}, errors.New("unknown numeric value")
	}
}

func (n numericInput) pluralValue() (pluralrules.Value, error) {
	switch n.kind {
	case numericSigned:
		return pluralrules.Int(n.signed), nil
	case numericUnsigned:
		return pluralrules.Uint(n.unsigned), nil
	case numericBig:
		if n.big == nil {
			return pluralrules.Value{}, errors.New("nil big integer")
		}
		return pluralrules.BigInt(n.big), nil
	case numericDecimal:
		return pluralrules.Decimal(n.decimal)
	default:
		return pluralrules.Value{}, errors.New("unknown numeric value")
	}
}

func (n numericInput) percentSelectionValue() (numericInput, error) {
	parts, err := parseDecimal(n.lexical())
	if err != nil {
		return numericInput{}, err
	}
	parts.exponent += 2
	return numericInput{kind: numericDecimal, decimal: parts.String()}, nil
}

func (n numericInput) offset(delta int64) (numericInput, error) {
	switch n.kind {
	case numericSigned:
		value := new(big.Int).Add(big.NewInt(n.signed), big.NewInt(delta))
		if value.IsInt64() {
			return numericInput{kind: numericSigned, signed: value.Int64()}, nil
		}
		return numericInput{kind: numericBig, big: value}, nil
	case numericUnsigned:
		value := new(big.Int).SetUint64(n.unsigned)
		value.Add(value, big.NewInt(delta))
		if value.Sign() >= 0 && value.IsUint64() {
			return numericInput{kind: numericUnsigned, unsigned: value.Uint64()}, nil
		}
		if value.IsInt64() {
			return numericInput{kind: numericSigned, signed: value.Int64()}, nil
		}
		return numericInput{kind: numericBig, big: value}, nil
	case numericBig:
		if n.big == nil {
			return numericInput{}, errors.New("nil big integer")
		}
		return numericInput{kind: numericBig, big: new(big.Int).Add(n.big, big.NewInt(delta))}, nil
	case numericDecimal:
		if delta == 0 {
			return n.clone(), nil
		}
		value, err := offsetDecimal(n.decimal, delta)
		if err != nil {
			return numericInput{}, err
		}
		return numericInput{kind: numericDecimal, decimal: value}, nil
	default:
		return numericInput{}, errors.New("unknown numeric value")
	}
}

func offsetDecimal(value string, delta int64) (string, error) {
	parts, err := parseDecimal(value)
	if err != nil {
		return "", err
	}
	expanded := parts.String()
	negative := strings.HasPrefix(expanded, "-")
	if negative {
		expanded = expanded[1:]
	}
	integer, fraction, fractional := strings.Cut(expanded, ".")
	scale := 0
	if fractional {
		scale = len(fraction)
	}
	unscaled := new(big.Int)
	if _, ok := unscaled.SetString(integer+fraction, 10); !ok {
		return "", errors.New("decimal digits are invalid")
	}
	if negative {
		unscaled.Neg(unscaled)
	}
	unscaled.Add(unscaled, new(big.Int).Mul(big.NewInt(delta), pow10(scale)))
	digits := new(big.Int).Abs(unscaled).String()
	if scale > 0 && len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	if scale > 0 {
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if unscaled.Sign() < 0 {
		digits = "-" + digits
	}
	if err := validateDecimal(digits); err != nil {
		return "", err
	}
	return digits, nil
}

type numericStyle uint8

const (
	styleNumber numericStyle = iota + 1
	styleInteger
	styleCurrency
	stylePercent
	styleUnit
)

type exactNumberValue struct {
	value         numericInput
	grammarLocale string
	formatLocale  string
	source        string
	style         numericStyle
	currency      string
	options       map[string]any
	selectMode    string
	budget        *renderBudget
	metadata      partMetadata
}

func (v *View) FormatNumber(ctx context.Context, value NumericValue, spec NumberFormatSpec) (FormattedValue, error) {
	return directFormatNumber(ctx, v, value, spec)
}

func directFormatNumber(ctx context.Context, target directFormatTarget, value NumericValue, spec NumberFormatSpec) (FormattedValue, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedValue, error) {
		options, err := directNumberOptions(spec)
		if err != nil {
			return FormattedValue{}, err
		}
		return state.formatDirectNumber(ctx, value, styleNumber, "number", "", options)
	})
}

func (v *View) FormatMoney(ctx context.Context, value MoneyValue, spec MoneyFormatSpec) (FormattedValue, error) {
	return directFormatMoney(ctx, v, value, spec)
}

func directFormatMoney(ctx context.Context, target directFormatTarget, value MoneyValue, spec MoneyFormatSpec) (FormattedValue, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedValue, error) {
		options, err := directNumberOptions(spec.Number)
		if err != nil {
			return FormattedValue{}, err
		}
		if !spec.Display.Valid() || !spec.Sign.Valid() {
			return FormattedValue{}, fmt.Errorf("%w: invalid money format", ErrInvalidMessage)
		}
		if spec.Display != CurrencyDisplayDefault {
			options["currencyDisplay"] = spec.Display.String()
		}
		if spec.Sign != CurrencySignDefault {
			options["currencySign"] = spec.Sign.String()
		}
		if !validCurrency(value.Currency) {
			return FormattedValue{}, fmt.Errorf("%w: currency must contain three ASCII letters", ErrInvalidMessage)
		}
		return state.formatDirectNumber(ctx, value.Amount, styleCurrency, "currency", strings.ToUpper(value.Currency), options)
	})
}

func (v *View) FormatPercent(ctx context.Context, value NumericValue, spec PercentFormatSpec) (FormattedValue, error) {
	return directFormatPercent(ctx, v, value, spec)
}

func directFormatPercent(ctx context.Context, target directFormatTarget, value NumericValue, spec PercentFormatSpec) (FormattedValue, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedValue, error) {
		options, err := directNumberOptions(spec.Number)
		if err != nil {
			return FormattedValue{}, err
		}
		return state.formatDirectNumber(ctx, value, stylePercent, "percent", "", options)
	})
}

func (v *View) FormatUnit(ctx context.Context, value NumericValue, spec UnitFormatSpec) (FormattedValue, error) {
	return directFormatUnit(ctx, v, value, spec)
}

func directFormatUnit(ctx context.Context, target directFormatTarget, value NumericValue, spec UnitFormatSpec) (FormattedValue, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedValue, error) {
		options, err := directNumberOptions(spec.Number)
		if err != nil {
			return FormattedValue{}, err
		}
		if !spec.Width.Valid() || spec.Unit == "" {
			return FormattedValue{}, fmt.Errorf("%w: invalid unit format", ErrInvalidMessage)
		}
		options["unit"] = spec.Unit
		if width := directWidth(spec.Width); width != "" {
			options["unitDisplay"] = width
		}
		return state.formatDirectNumber(ctx, value, styleUnit, "unit", "", options)
	})
}

func (v *View) FormatNumberRange(ctx context.Context, start, end NumericValue, spec NumberFormatSpec) (FormattedRange, error) {
	return directFormatNumberRange(ctx, v, start, end, spec)
}

func directFormatNumberRange(ctx context.Context, target directFormatTarget, start, end NumericValue, spec NumberFormatSpec) (FormattedRange, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedRange, error) {
		options, err := directNumberOptions(spec)
		if err != nil {
			return FormattedRange{}, err
		}
		return state.formatDirectNumberRange(ctx, start, end, styleNumber, "number", "", options)
	})
}

func (v *View) FormatMoneyRange(ctx context.Context, start, end MoneyValue, spec MoneyFormatSpec) (FormattedRange, error) {
	return directFormatMoneyRange(ctx, v, start, end, spec)
}

func directFormatMoneyRange(ctx context.Context, target directFormatTarget, start, end MoneyValue, spec MoneyFormatSpec) (FormattedRange, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedRange, error) {
		options, err := directNumberOptions(spec.Number)
		if err != nil {
			return FormattedRange{}, err
		}
		if !spec.Display.Valid() || !spec.Sign.Valid() || !validCurrency(start.Currency) || !validCurrency(end.Currency) || !strings.EqualFold(start.Currency, end.Currency) {
			return FormattedRange{}, fmt.Errorf("%w: money range requires one valid currency", ErrInvalidMessage)
		}
		if spec.Display != CurrencyDisplayDefault {
			options["currencyDisplay"] = spec.Display.String()
		}
		if spec.Sign != CurrencySignDefault {
			options["currencySign"] = spec.Sign.String()
		}
		return state.formatDirectNumberRange(ctx, start.Amount, end.Amount, styleCurrency, "currency", strings.ToUpper(start.Currency), options)
	})
}

func (v *View) FormatPercentRange(ctx context.Context, start, end NumericValue, spec PercentFormatSpec) (FormattedRange, error) {
	return directFormatPercentRange(ctx, v, start, end, spec)
}

func directFormatPercentRange(ctx context.Context, target directFormatTarget, start, end NumericValue, spec PercentFormatSpec) (FormattedRange, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedRange, error) {
		options, err := directNumberOptions(spec.Number)
		if err != nil {
			return FormattedRange{}, err
		}
		return state.formatDirectNumberRange(ctx, start, end, stylePercent, "percent", "", options)
	})
}

func (v *View) FormatUnitRange(ctx context.Context, start, end NumericValue, spec UnitFormatSpec) (FormattedRange, error) {
	return directFormatUnitRange(ctx, v, start, end, spec)
}

func directFormatUnitRange(ctx context.Context, target directFormatTarget, start, end NumericValue, spec UnitFormatSpec) (FormattedRange, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedRange, error) {
		options, err := directNumberOptions(spec.Number)
		if err != nil {
			return FormattedRange{}, err
		}
		if !spec.Width.Valid() || spec.Unit == "" {
			return FormattedRange{}, fmt.Errorf("%w: invalid unit format", ErrInvalidMessage)
		}
		options["unit"] = spec.Unit
		if width := directWidth(spec.Width); width != "" {
			options["unitDisplay"] = width
		}
		return state.formatDirectNumberRange(ctx, start, end, styleUnit, "unit", "", options)
	})
}

func (v *View) SelectPlural(ctx context.Context, value NumericValue, spec PluralFormatSpec) (PluralCategory, error) {
	return directSelectPlural(ctx, v, value, spec)
}

func directSelectPlural(ctx context.Context, target directFormatTarget, value NumericValue, spec PluralFormatSpec) (PluralCategory, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (PluralCategory, error) {
		input, _, err := directNumericInput(value, state.limits)
		if err != nil {
			return 0, err
		}
		rules, err := directPluralRules(state, spec)
		if err != nil {
			return 0, err
		}
		pluralValue, err := input.pluralValue()
		if err != nil {
			return 0, fmt.Errorf("%w: plural value: %v", ErrInvalidMessage, err)
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return directPluralCategory(rules.Select(pluralValue))
	})
}

func (v *View) SelectPluralRange(ctx context.Context, start, end NumericValue, spec PluralFormatSpec) (PluralCategory, error) {
	return directSelectPluralRange(ctx, v, start, end, spec)
}

func directSelectPluralRange(ctx context.Context, target directFormatTarget, start, end NumericValue, spec PluralFormatSpec) (PluralCategory, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (PluralCategory, error) {
		startInput, _, err := directNumericInput(start, state.limits)
		if err != nil {
			return 0, fmt.Errorf("range start: %w", err)
		}
		endInput, _, err := directNumericInput(end, state.limits)
		if err != nil {
			return 0, fmt.Errorf("range end: %w", err)
		}
		rules, err := directPluralRules(state, spec)
		if err != nil {
			return 0, err
		}
		startValue, err := startInput.pluralValue()
		if err != nil {
			return 0, fmt.Errorf("%w: plural range start: %v", ErrInvalidMessage, err)
		}
		endValue, err := endInput.pluralValue()
		if err != nil {
			return 0, fmt.Errorf("%w: plural range end: %v", ErrInvalidMessage, err)
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		category, err := rules.SelectRange(startValue, endValue)
		if err != nil {
			return 0, fmt.Errorf("%w: plural range: %v", ErrInvalidMessage, err)
		}
		return directPluralCategory(category)
	})
}

func directPluralRules(state directFormatContext, spec PluralFormatSpec) (*pluralrules.PluralRules, error) {
	if !spec.Type.Valid() {
		return nil, fmt.Errorf("%w: invalid plural type", ErrInvalidMessage)
	}
	options, err := directNumberOptions(spec.Number)
	if err != nil {
		return nil, err
	}
	for _, name := range sortedMapKeys(options) {
		if !oneOf(name, "minimumIntegerDigits", "minimumFractionDigits", "maximumFractionDigits", "minimumSignificantDigits", "maximumSignificantDigits", "roundingIncrement", "roundingMode", "roundingPriority", "trailingZeroDisplay", "notation", "compactDisplay") {
			return nil, fmt.Errorf("%w: number option %s is not valid for plural selection", ErrInvalidMessage, name)
		}
	}
	pluralOptions := pluralrules.Options{}
	copyNumberOptions(options, nil, &pluralOptions)
	typeName := spec.Type.String()
	pluralOptions.Type = &typeName
	locales, err := locale.ParseList(state.locale)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLocale, err)
	}
	rules, err := pluralrules.New(locales, pluralOptions)
	if err != nil {
		return nil, fmt.Errorf("%w: plural rules: %v", ErrInvalidMessage, err)
	}
	if err := requireResolvedLocale(state.locale, rules.ResolvedOptions().Locale); err != nil {
		return nil, fmt.Errorf("%w: plural rules: %v", ErrInvalidLocale, err)
	}
	return rules, nil
}

func directPluralCategory(category pluralrules.Category) (PluralCategory, error) {
	switch category {
	case pluralrules.Zero:
		return PluralZero, nil
	case pluralrules.One:
		return PluralOne, nil
	case pluralrules.Two:
		return PluralTwo, nil
	case pluralrules.Few:
		return PluralFew, nil
	case pluralrules.Many:
		return PluralMany, nil
	case pluralrules.Other:
		return PluralOther, nil
	default:
		return 0, fmt.Errorf("%w: plural rules returned an invalid category", ErrInvalidMessage)
	}
}

func directNumberOptions(spec NumberFormatSpec) (map[string]any, error) {
	if !spec.Grouping.Valid() || !spec.Sign.Valid() || !spec.Notation.Valid() || !spec.Compact.Valid() ||
		!spec.RoundingMode.Valid() || !spec.RoundingPriority.Valid() || !spec.TrailingZeroDisplay.Valid() ||
		!spec.RoundingIncrement.Valid() || spec.MinimumIntegerDigits < 0 {
		return nil, fmt.Errorf("%w: invalid number format", ErrInvalidMessage)
	}
	if spec.Compact != NumberCompactDefault && spec.Notation != NumberNotationCompact {
		return nil, fmt.Errorf("%w: compact display requires compact notation", ErrInvalidMessage)
	}
	if err := validateDirectRounding(spec); err != nil {
		return nil, err
	}
	options := make(map[string]any, 12)
	if spec.Grouping != NumberGroupingDefault {
		options["useGrouping"] = spec.Grouping.String()
	}
	if spec.Sign != NumberSignDefault {
		options["signDisplay"] = spec.Sign.String()
	}
	if spec.Notation != NumberNotationDefault {
		options["notation"] = spec.Notation.String()
	}
	if spec.Compact != NumberCompactDefault {
		options["compactDisplay"] = spec.Compact.String()
	}
	if spec.RoundingMode != NumberRoundingModeDefault {
		options["roundingMode"] = spec.RoundingMode.String()
	}
	if spec.RoundingPriority != NumberRoundingPriorityDefault {
		options["roundingPriority"] = spec.RoundingPriority.String()
	}
	if spec.TrailingZeroDisplay != NumberTrailingZeroDefault {
		options["trailingZeroDisplay"] = spec.TrailingZeroDisplay.String()
	}
	if spec.RoundingIncrement != NumberRoundingIncrementDefault {
		options["roundingIncrement"] = int(spec.RoundingIncrement)
	}
	if spec.MinimumIntegerDigits != 0 {
		options["minimumIntegerDigits"] = spec.MinimumIntegerDigits
	}
	if err := appendDirectDigitRange(options, "minimumFractionDigits", "maximumFractionDigits", spec.FractionDigits, true); err != nil {
		return nil, err
	}
	if err := appendDirectDigitRange(options, "minimumSignificantDigits", "maximumSignificantDigits", spec.SignificantDigits, false); err != nil {
		return nil, err
	}
	if spec.NumberingSystem != "" {
		options["numberingSystem"] = spec.NumberingSystem
	}
	return options, nil
}

func validateDirectRounding(spec NumberFormatSpec) error {
	if spec.RoundingIncrement == NumberRoundingIncrementDefault || spec.RoundingIncrement == NumberRoundingIncrement1 {
		return nil
	}
	if !spec.FractionDigits.minimumSet || !spec.FractionDigits.maximumSet || spec.FractionDigits.minimum != spec.FractionDigits.maximum {
		return fmt.Errorf("%w: rounding increment requires equal minimum and maximum fraction digits", ErrInvalidMessage)
	}
	if spec.SignificantDigits.minimumSet || spec.SignificantDigits.maximumSet ||
		(spec.RoundingPriority != NumberRoundingPriorityDefault && spec.RoundingPriority != NumberRoundingPriorityAuto) {
		return fmt.Errorf("%w: rounding increment requires fraction-digit rounding with default priority", ErrInvalidMessage)
	}
	return nil
}

func appendDirectDigitRange(options map[string]any, minimumName, maximumName string, digits DigitRange, allowZero bool) error {
	if !digits.minimumSet && !digits.maximumSet {
		if digits.minimum != 0 || digits.maximum != 0 {
			return fmt.Errorf("%w: invalid digit range", ErrInvalidMessage)
		}
		return nil
	}
	minimum := 0
	if !allowZero {
		minimum = 1
	}
	if digits.minimumSet && digits.minimum < minimum {
		return fmt.Errorf("%w: invalid digit range", ErrInvalidMessage)
	}
	if digits.maximumSet && digits.maximum < minimum {
		return fmt.Errorf("%w: invalid digit range", ErrInvalidMessage)
	}
	if digits.minimumSet && digits.maximumSet && digits.maximum < digits.minimum {
		return fmt.Errorf("%w: invalid digit range", ErrInvalidMessage)
	}
	if digits.minimumSet {
		options[minimumName] = digits.minimum
	}
	if digits.maximumSet {
		options[maximumName] = digits.maximum
	}
	return nil
}

func (s directFormatContext) formatDirectNumber(ctx context.Context, value NumericValue, style numericStyle, function, currency string, options map[string]any) (FormattedValue, error) {
	if function == "unit" && !s.capabilities[CapabilityUnit] {
		return FormattedValue{}, fmt.Errorf("%w: unit formatting capability is disabled", ErrInvalidMessage)
	}
	if err := validateDirectOptionBounds(options, s.limits); err != nil {
		return FormattedValue{}, err
	}
	input, argumentType, err := directNumericInput(value, s.limits)
	if err != nil {
		return FormattedValue{}, err
	}
	if function == "currency" {
		argumentType = TypeMoney
	}
	for _, name := range sortedMapKeys(options) {
		if !directNumberOptionAllowed(function, name) {
			return FormattedValue{}, fmt.Errorf("%w: option %s is not valid for %s formatting", ErrInvalidMessage, name, function)
		}
	}
	maximumBytes, err := numericOutputBytes(input, function, options)
	if err != nil {
		return FormattedValue{}, err
	}
	maximumParts, err := numericOutputParts(input, function, options)
	if err != nil {
		return FormattedValue{}, err
	}
	budget, err := s.directExactBudget(maximumBytes, maximumParts)
	if err != nil {
		return FormattedValue{}, err
	}
	if err := ctx.Err(); err != nil {
		return FormattedValue{}, err
	}
	if err := validateFormatRequirements(s.locale, formatNumber); err != nil {
		return FormattedValue{}, fmt.Errorf("%w: number formatting: %v", ErrInvalidLocale, err)
	}
	if err := validateNumberConfiguration(s.locale, function, argumentType, options); err != nil {
		return FormattedValue{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	exact := &exactNumberValue{
		value:         input,
		grammarLocale: s.locale,
		formatLocale:  s.locale,
		style:         style,
		currency:      currency,
		options:       maps.Clone(options),
		budget:        budget,
	}
	return s.finishDirectExact(ctx, exact)
}

func (s directFormatContext) formatDirectNumberRange(ctx context.Context, start, end NumericValue, style numericStyle, function, currency string, options map[string]any) (FormattedRange, error) {
	if function == "unit" && !s.capabilities[CapabilityUnit] {
		return FormattedRange{}, fmt.Errorf("%w: unit formatting capability is disabled", ErrInvalidMessage)
	}
	if err := validateDirectOptionBounds(options, s.limits); err != nil {
		return FormattedRange{}, err
	}
	startInput, argumentType, err := directNumericInput(start, s.limits)
	if err != nil {
		return FormattedRange{}, fmt.Errorf("range start: %w", err)
	}
	endInput, _, err := directNumericInput(end, s.limits)
	if err != nil {
		return FormattedRange{}, fmt.Errorf("range end: %w", err)
	}
	if function == "currency" {
		argumentType = TypeMoney
	}
	for _, name := range sortedMapKeys(options) {
		if !directNumberOptionAllowed(function, name) {
			return FormattedRange{}, fmt.Errorf("%w: option %s is not valid for %s formatting", ErrInvalidMessage, name, function)
		}
	}
	startBytes, err := numericOutputBytes(startInput, function, options)
	if err != nil {
		return FormattedRange{}, err
	}
	endBytes, err := numericOutputBytes(endInput, function, options)
	if err != nil {
		return FormattedRange{}, err
	}
	startParts, err := numericOutputParts(startInput, function, options)
	if err != nil {
		return FormattedRange{}, err
	}
	endParts, err := numericOutputParts(endInput, function, options)
	if err != nil {
		return FormattedRange{}, err
	}
	if err := s.requireDirectOutput(saturatedDirectAdd(saturatedDirectAdd(startBytes, endBytes), formattedValueOverhead), saturatedDirectAdd(saturatedDirectAdd(startParts, endParts), 1)); err != nil {
		return FormattedRange{}, err
	}
	if err := validateFormatRequirements(s.locale, formatNumber); err != nil {
		return FormattedRange{}, fmt.Errorf("%w: number formatting: %v", ErrInvalidLocale, err)
	}
	if err := validateNumberConfiguration(s.locale, function, argumentType, options); err != nil {
		return FormattedRange{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	startValue := exactNumberValue{value: startInput, grammarLocale: s.locale, formatLocale: s.locale, style: style, currency: currency, options: maps.Clone(options)}
	endValue := exactNumberValue{value: endInput, grammarLocale: s.locale, formatLocale: s.locale, style: style, currency: currency, options: maps.Clone(options)}
	locales, err := locale.ParseList(s.locale)
	if err != nil {
		return FormattedRange{}, fmt.Errorf("%w: %v", ErrInvalidLocale, err)
	}
	numberOptions := startValue.numberOptions()
	applyRangeLexicalPrecision(&numberOptions, startValue, endValue)
	formatter, err := numberformat.New(locales, numberOptions)
	if err != nil {
		return FormattedRange{}, fmt.Errorf("%w: number range format: %v", ErrInvalidMessage, err)
	}
	if err := validateResolvedNumberLocale(s.locale, formatter.ResolvedOptions(), options); err != nil {
		return FormattedRange{}, fmt.Errorf("%w: number range formatting: %v", ErrInvalidLocale, err)
	}
	startNumber, err := startInput.numberValue()
	if err != nil {
		return FormattedRange{}, err
	}
	endNumber, err := endInput.numberValue()
	if err != nil {
		return FormattedRange{}, err
	}
	source, err := formatter.FormatRangeToParts(startNumber, endNumber)
	if err != nil {
		return FormattedRange{}, fmt.Errorf("%w: number range format: %v", ErrInvalidMessage, err)
	}
	parts := make([]directRangePart, len(source))
	for index, part := range source {
		rangeSource, ok := directNumberRangeSource(part.Source)
		if !ok {
			return FormattedRange{}, fmt.Errorf("%w: number formatter returned invalid range source", ErrInvalidMessage)
		}
		kind := PartValue
		if part.Type == numberformat.PartLiteral {
			kind = PartText
		}
		parts[index] = directRangePart{kind: kind, typeName: string(part.Type), text: part.Value, direction: bidi.GetLocaleDirection(s.locale), source: rangeSource}
	}
	return s.finishDirectRange(ctx, s.locale, parts)
}

func applyRangeLexicalPrecision(options *numberformat.Options, start, end exactNumberValue) {
	if hasDigitOption(start.options) {
		return
	}
	startInput := start.value
	endInput := end.value
	if start.style == stylePercent {
		startInput, _ = startInput.percentSelectionValue()
		endInput, _ = endInput.percentSelectionValue()
	}
	startDigits, startErr := visibleFractionDigits(startInput.lexical())
	endDigits, endErr := visibleFractionDigits(endInput.lexical())
	if startErr != nil || endErr != nil {
		return
	}
	minimum := min(startDigits, endDigits)
	maximum := max(startDigits, endDigits)
	options.MinimumFractionDigits = &minimum
	options.MaximumFractionDigits = &maximum
}

func directNumberRangeSource(source numberformat.RangeSource) (RangeSource, bool) {
	switch source {
	case numberformat.SourceStartRange:
		return RangeSourceStart, true
	case numberformat.SourceShared:
		return RangeSourceShared, true
	case numberformat.SourceEndRange:
		return RangeSourceEnd, true
	default:
		return 0, false
	}
}

func directNumberOptionAllowed(function, option string) bool {
	if oneOf(option,
		"minimumIntegerDigits", "minimumFractionDigits", "maximumFractionDigits",
		"minimumSignificantDigits", "maximumSignificantDigits", "roundingIncrement", "roundingMode",
		"roundingPriority", "trailingZeroDisplay", "notation", "compactDisplay", "useGrouping",
		"signDisplay", "numberingSystem") {
		return true
	}
	switch function {
	case "currency":
		return oneOf(option, "currencyDisplay", "currencySign")
	case "unit":
		return oneOf(option, "unit", "unitDisplay")
	default:
		return false
	}
}

func validateNamedNumericFormats(state directFormatContext, formats namedFormats) error {
	if len(formats.units) != 0 && !state.capabilities[CapabilityUnit] {
		return fmt.Errorf("%w: named unit formats require the unit capability", ErrInvalidMessage)
	}
	if len(formats.numbers)+len(formats.money)+len(formats.percents)+len(formats.units) != 0 {
		if err := validateFormatRequirements(state.locale, formatNumber); err != nil {
			return fmt.Errorf("%w: named number formats: %v", ErrInvalidLocale, err)
		}
	}
	validate := func(name, function string, argumentType ArgumentType, options map[string]any) error {
		if err := validateDirectOptionBounds(options, state.limits); err != nil {
			return fmt.Errorf("named %s format %q: %w", function, name, err)
		}
		for _, option := range sortedMapKeys(options) {
			if !directNumberOptionAllowed(function, option) {
				return fmt.Errorf("%w: named %s format %q has invalid option %s", ErrInvalidMessage, function, name, option)
			}
		}
		if err := validateNumberConfiguration(state.locale, function, argumentType, options); err != nil {
			return fmt.Errorf("%w: named %s format %q: %v", ErrInvalidMessage, function, name, err)
		}
		return nil
	}
	for _, name := range sortedMapKeys(formats.numbers) {
		options, err := directNumberOptions(formats.numbers[name])
		if err != nil {
			return fmt.Errorf("named number format %q: %w", name, err)
		}
		if err := validate(name, "number", TypeDecimal, options); err != nil {
			return err
		}
	}
	for _, name := range sortedMapKeys(formats.money) {
		spec := formats.money[name]
		options, err := directNumberOptions(spec.Number)
		if err != nil {
			return fmt.Errorf("named currency format %q: %w", name, err)
		}
		if !spec.Display.Valid() || !spec.Sign.Valid() {
			return fmt.Errorf("%w: named currency format %q is invalid", ErrInvalidMessage, name)
		}
		if spec.Display != CurrencyDisplayDefault {
			options["currencyDisplay"] = spec.Display.String()
		}
		if spec.Sign != CurrencySignDefault {
			options["currencySign"] = spec.Sign.String()
		}
		if err := validate(name, "currency", TypeMoney, options); err != nil {
			return err
		}
	}
	for _, name := range sortedMapKeys(formats.percents) {
		options, err := directNumberOptions(formats.percents[name].Number)
		if err != nil {
			return fmt.Errorf("named percent format %q: %w", name, err)
		}
		if err := validate(name, "percent", TypeDecimal, options); err != nil {
			return err
		}
	}
	for _, name := range sortedMapKeys(formats.units) {
		spec := formats.units[name]
		options, err := directNumberOptions(spec.Number)
		if err != nil {
			return fmt.Errorf("named unit format %q: %w", name, err)
		}
		if !spec.Width.Valid() || spec.Unit == "" {
			return fmt.Errorf("%w: named unit format %q is invalid", ErrInvalidMessage, name)
		}
		options["unit"] = spec.Unit
		if width := directWidth(spec.Width); width != "" {
			options["unitDisplay"] = width
		}
		if err := validate(name, "unit", TypeDecimal, options); err != nil {
			return err
		}
	}
	for _, name := range sortedMapKeys(formats.plurals) {
		if _, err := directPluralRules(state, formats.plurals[name]); err != nil {
			return fmt.Errorf("named plural format %q: %w", name, err)
		}
	}
	return nil
}

func directNumericInput(value NumericValue, limits Limits) (numericInput, ArgumentType, error) {
	var input numericInput
	var argumentType ArgumentType
	switch value := value.(type) {
	case SignedNumber:
		input = numericInput{kind: numericSigned, signed: int64(value)}
		argumentType = TypeInteger
	case UnsignedNumber:
		input = numericInput{kind: numericUnsigned, unsigned: uint64(value)}
		argumentType = TypeUnsignedInteger
	case DecimalNumber:
		input = numericInput{kind: numericDecimal, decimal: string(value)}
		argumentType = TypeDecimal
		if err := validateDecimal(input.decimal); err != nil {
			return numericInput{}, 0, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
		}
	case BigIntegerNumber:
		argumentType = TypeBigInteger
		if value.invalid != "" {
			return numericInput{}, 0, fmt.Errorf("%w: %s", ErrInvalidMessage, value.invalid)
		}
		if value.value == nil {
			return numericInput{}, 0, fmt.Errorf("%w: big integer is nil", ErrInvalidMessage)
		}
		if value.value.BitLen() > limits.MaxBigIntegerBits {
			return numericInput{}, 0, fmt.Errorf("%w: big integer exceeds the configured size", ErrLimitExceeded)
		}
		input = numericInput{kind: numericBig, big: new(big.Int).Set(value.value)}
	default:
		return numericInput{}, 0, fmt.Errorf("%w: unsupported numeric value %T", ErrInvalidMessage, value)
	}
	size := 0
	if input.kind == numericBig {
		size = decimalIntegerBytes(input.big)
	} else {
		size = len(input.lexical())
	}
	if size > limits.MaxArgumentBytes {
		return numericInput{}, 0, fmt.Errorf("%w: numeric value exceeds %d bytes", ErrLimitExceeded, limits.MaxArgumentBytes)
	}
	return input, argumentType, nil
}

const (
	maximumCurrencyAffixParts = 8
	maximumUnitAffixParts     = 4
	maximumCompactAffixParts  = 8
	maximumScientificParts    = 3
)

func (v *exactNumberValue) Type() string {
	return "number"
}

func (v *exactNumberValue) Source() string {
	return v.source
}

func (v *exactNumberValue) Dir() bidi.Direction {
	return v.metadata.dir(bidi.GetLocaleDirection(v.formatLocale))
}

func (v *exactNumberValue) Locale() string {
	return v.formatLocale
}

func (v *exactNumberValue) ToString() (string, error) {
	maximum, err := numericOutputBytes(v.value, v.functionName(), v.options)
	if err != nil {
		return "", err
	}
	if err := v.budget.require(maximum, 1); err != nil {
		return "", err
	}
	formatted, err := v.format()
	if err != nil {
		return "", err
	}
	if err := v.budget.charge(len(formatted), 1); err != nil {
		return "", err
	}
	return formatted, nil
}

func (v *exactNumberValue) format() (string, error) {
	formatter, value, err := v.numberFormatter()
	if err != nil {
		return "", err
	}
	return formatter.Format(value), nil
}

func (v *exactNumberValue) formatParts() (string, []Subpart, error) {
	formatter, value, err := v.numberFormatter()
	if err != nil {
		return "", nil, err
	}
	formatted := formatter.FormatToParts(value)
	parts := make([]Subpart, len(formatted))
	var text strings.Builder
	for index, part := range formatted {
		parts[index] = Subpart{Type: string(part.Type), Text: part.Value}
		text.WriteString(part.Value)
	}
	return text.String(), parts, nil
}

func (v *exactNumberValue) numberFormatter() (*numberformat.NumberFormat, numberformat.Value, error) {
	locales, err := locale.ParseList(v.formatLocale)
	if err != nil {
		return nil, numberformat.Value{}, err
	}
	value, err := v.value.numberValue()
	if err != nil {
		return nil, numberformat.Value{}, err
	}
	formatter, err := numberformat.New(locales, v.numberOptions())
	if err != nil {
		return nil, numberformat.Value{}, err
	}
	if err := validateResolvedNumberLocale(v.formatLocale, formatter.ResolvedOptions(), v.options); err != nil {
		return nil, numberformat.Value{}, err
	}
	return formatter, value, nil
}

func (v *exactNumberValue) ToParts() ([]messagevalue.MessagePart, error) {
	maximum, err := numericOutputBytes(v.value, v.functionName(), v.options)
	if err != nil {
		return nil, err
	}
	maximumParts, err := numericOutputParts(v.value, v.functionName(), v.options)
	if err != nil {
		return nil, err
	}
	if err := v.budget.require(maximum, maximumParts); err != nil {
		return nil, err
	}
	formatted, subparts, err := v.formatParts()
	if err != nil {
		return nil, err
	}
	if err := v.budget.charge(len(formatted), 1+len(subparts)); err != nil {
		return nil, err
	}
	return []messagevalue.MessagePart{formattedMessagePart{kind: "number", value: formatted, source: v.source, locale: v.formatLocale, dir: v.Dir(), id: v.metadata.id, parts: subparts}}, nil
}

func numericOutputParts(value numericInput, function string, options map[string]any) (int, error) {
	integerDigits, fractionDigits, negative, zero, err := numericPartShape(value, function)
	if err != nil {
		return 0, err
	}
	significantIntegerDigits := integerDigits
	if numericMayRound(function, options) {
		integerDigits = saturatedPartAdd(integerDigits, 1)
	}
	notation, _ := optionString(options, "notation")
	if (notation == "scientific" || notation == "engineering") && integerDigits < 3 {
		integerDigits = 3
	}
	if minimum, ok := optionInt(options["minimumIntegerDigits"]); ok && minimum > integerDigits {
		integerDigits = minimum
	}
	integerParts := 1
	grouping, _ := optionString(options, "useGrouping")
	if grouping != "never" && grouping != "false" {
		integerParts = saturatedGroupedParts(integerDigits)
	}
	parts := saturatedPartAdd(1, integerParts)
	if numericMayRenderFraction(function, options, significantIntegerDigits, fractionDigits) {
		parts = saturatedPartAdd(parts, 2)
	}
	if numericMayRenderSign(options, negative, zero) {
		parts = saturatedPartAdd(parts, 1)
	}
	switch notation {
	case "scientific", "engineering":
		parts = saturatedPartAdd(parts, maximumScientificParts)
	case "compact":
		parts = saturatedPartAdd(parts, maximumCompactAffixParts)
	}
	switch function {
	case "currency":
		parts = saturatedPartAdd(parts, maximumCurrencyAffixParts)
	case "unit":
		parts = saturatedPartAdd(parts, maximumUnitAffixParts)
	case "percent":
		parts = saturatedPartAdd(parts, 1)
	}
	return parts, nil
}

func numericPartShape(value numericInput, function string) (integerDigits, fractionDigits int, negative, zero bool, err error) {
	shift := 0
	if function == "percent" {
		shift = 2
	}
	switch value.kind {
	case numericSigned:
		text := strconv.FormatInt(value.signed, 10)
		negative = value.signed < 0
		zero = value.signed == 0
		integerDigits = len(strings.TrimPrefix(text, "-")) + shift
	case numericUnsigned:
		integerDigits = len(strconv.FormatUint(value.unsigned, 10)) + shift
		zero = value.unsigned == 0
	case numericBig:
		if value.big == nil {
			return 0, 0, false, false, errors.New("nil big integer")
		}
		integerDigits = decimalIntegerBytes(value.big)
		negative = value.big.Sign() < 0
		if negative {
			integerDigits--
		}
		integerDigits = saturatedPartAdd(integerDigits, shift)
		zero = value.big.Sign() == 0
	case numericDecimal:
		parts, parseErr := parseDecimal(value.decimal)
		if parseErr != nil {
			return 0, 0, false, false, parseErr
		}
		point := len(parts.digits) - parts.fraction + parts.exponent + shift
		integerDigits = max(point, 1)
		if point < len(parts.digits) {
			fractionDigits = len(parts.digits) - point
		}
		negative = parts.negative
		zero = true
		for _, digit := range parts.digits {
			if digit != '0' {
				zero = false
				break
			}
		}
	default:
		return 0, 0, false, false, errors.New("unknown numeric value")
	}
	if integerDigits < 1 {
		integerDigits = 1
	}
	if fractionDigits < 0 {
		fractionDigits = saturatedPartAdd(len(value.decimal), 1)
	}
	return integerDigits, fractionDigits, negative, zero, nil
}

func numericMayRound(function string, options map[string]any) bool {
	if function == "currency" {
		return true
	}
	if notation, _ := optionString(options, "notation"); notation == "scientific" || notation == "engineering" || notation == "compact" {
		return true
	}
	for _, name := range []string{"fractionDigits", "minimumFractionDigits", "maximumFractionDigits", "minimumSignificantDigits", "maximumSignificantDigits", "roundingIncrement"} {
		if _, ok := options[name]; ok {
			return true
		}
	}
	return false
}

func numericMayRenderFraction(function string, options map[string]any, integerDigits, fractionDigits int) bool {
	if function == "integer" {
		return false
	}
	minimumSignificant, hasMinimumSignificant := optionInt(options["minimumSignificantDigits"])
	_, hasMaximumSignificant := optionInt(options["maximumSignificantDigits"])
	if (hasMinimumSignificant || hasMaximumSignificant) && (fractionDigits > 0 || minimumSignificant > integerDigits) {
		return true
	}
	if digits, ok := optionInt(options["fractionDigits"]); ok {
		return digits > 0
	}
	if minimum, ok := optionInt(options["minimumFractionDigits"]); ok && minimum > 0 {
		return true
	}
	if maximum, ok := optionInt(options["maximumFractionDigits"]); ok && maximum == 0 {
		return false
	}
	if notation, _ := optionString(options, "notation"); notation == "scientific" || notation == "engineering" || notation == "compact" {
		return true
	}
	return fractionDigits > 0 || function == "currency"
}

func numericMayRenderSign(options map[string]any, negative, zero bool) bool {
	display, _ := optionString(options, "signDisplay")
	switch display {
	case "never":
		return false
	case "always":
		return true
	case "exceptZero":
		return !zero
	case "negative":
		return negative && !zero
	default:
		return negative
	}
}

func saturatedGroupedParts(digits int) int {
	maximum := int(^uint(0) >> 1)
	if digits <= 1 {
		return 1
	}
	if digits > maximum/2 {
		return maximum
	}
	return digits*2 - 1
}

func saturatedPartAdd(left, right int) int {
	maximum := int(^uint(0) >> 1)
	if left < 0 || right < 0 || left > maximum-right {
		return maximum
	}
	return left + right
}

func (v *exactNumberValue) ValueOf() (any, error) {
	clone := *v
	clone.value = v.value.clone()
	clone.options = maps.Clone(v.options)
	return &clone, nil
}

func (v *exactNumberValue) SelectKeys(keys []string) ([]string, error) {
	if err := v.budget.require(0, 0); err != nil {
		return nil, err
	}
	selectionValue := v.value
	if v.style == stylePercent {
		var err error
		selectionValue, err = v.value.percentSelectionValue()
		if err != nil {
			return nil, err
		}
	}
	for _, key := range keys {
		candidate := strings.TrimPrefix(key, "=")
		if candidate == key && !looksNumeric(candidate) {
			continue
		}
		equal, err := numericEqual(selectionValue.lexical(), candidate)
		if err == nil && equal {
			return []string{key}, nil
		}
	}
	if v.selectMode == "exact" || v.style == styleCurrency || v.style == styleUnit {
		return nil, nil
	}
	locales, err := locale.ParseList(v.grammarLocale)
	if err != nil {
		return nil, err
	}
	rules, err := pluralrules.New(locales, v.pluralOptions())
	if err != nil {
		return nil, err
	}
	if err := requireResolvedLocale(v.grammarLocale, rules.ResolvedOptions().Locale); err != nil {
		return nil, err
	}
	value, err := selectionValue.pluralValue()
	if err != nil {
		return nil, err
	}
	category := rules.Select(value).String()
	for _, key := range keys {
		if key == category {
			return []string{key}, nil
		}
	}
	return nil, nil
}

func (v *exactNumberValue) functionName() string {
	switch v.style {
	case styleInteger:
		return "integer"
	case styleCurrency:
		return "currency"
	case stylePercent:
		return "percent"
	case styleUnit:
		return "unit"
	default:
		return "number"
	}
}

func (v *exactNumberValue) numberOptions() numberformat.Options {
	style := "decimal"
	switch v.style {
	case styleCurrency:
		style = "currency"
	case stylePercent:
		style = "percent"
	case styleUnit:
		style = "unit"
	}
	options := numberformat.Options{Style: &style}
	copyNumberOptions(v.options, &options, nil)
	v.applyLexicalPrecision(&options, nil)
	if v.style == styleInteger {
		zero := 0
		options.MaximumFractionDigits = &zero
	}
	if v.currency != "" {
		currency := v.currency
		options.Currency = &currency
	}
	return options
}

func (v *exactNumberValue) pluralOptions() pluralrules.Options {
	options := pluralrules.Options{}
	copyNumberOptions(v.options, nil, &options)
	v.applyLexicalPrecision(nil, &options)
	typeOf := "cardinal"
	if v.selectMode == "ordinal" {
		typeOf = "ordinal"
	}
	options.Type = &typeOf
	if v.style == styleInteger {
		zero := 0
		options.MaximumFractionDigits = &zero
	}
	return options
}

func (v *exactNumberValue) applyLexicalPrecision(number *numberformat.Options, plural *pluralrules.Options) {
	if v.value.kind != numericDecimal || v.style == styleInteger || hasDigitOption(v.options) {
		return
	}
	value := v.value
	if v.style == stylePercent {
		percent, err := value.percentSelectionValue()
		if err != nil {
			return
		}
		value = percent
	}
	digits, err := visibleFractionDigits(value.lexical())
	if err != nil {
		return
	}
	if number != nil {
		number.MinimumFractionDigits = &digits
		number.MaximumFractionDigits = &digits
	}
	if plural != nil {
		plural.MinimumFractionDigits = &digits
		plural.MaximumFractionDigits = &digits
	}
}

func hasDigitOption(options map[string]any) bool {
	for _, name := range []string{"fractionDigits", "minimumFractionDigits", "maximumFractionDigits", "minimumSignificantDigits", "maximumSignificantDigits", "roundingIncrement"} {
		if _, ok := options[name]; ok {
			return true
		}
	}
	return false
}

func exactNumberFunction(style numericStyle) functions.MessageFunction {
	return func(ctx functions.MessageFunctionContext, options functions.Options, operand any) messagevalue.MessageValue {
		resolved, resolveErr := resolveFunctionOperand(operand)
		if resolveErr != nil {
			ctx.OnError(resolveErr)
			return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
		}
		operand = resolved
		if value, ok := operand.(*nullMessageValue); ok && value != nil {
			return nullWithFunctionMetadata(ctx, value)
		}
		if value, ok := operand.(*exactNumberValue); ok && value != nil {
			if err := value.budget.require(0, 0); err != nil {
				return value
			}
		}
		value, err := numberOperand(operand, ctx)
		if err != nil {
			ctx.OnError(err)
			return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
		}
		clone := *value
		clone.value = value.value.clone()
		clone.source = ctx.Source()
		clone.style = style
		clone.options = resolvedNumberOptions(style, value.options, withoutUniversalOptions(options.Map()))
		clone.metadata = metadataFromFunctionOperand(ctx, value.Dir())
		clone.selectMode = ""
		if selected, ok := optionString(clone.options, "select"); ok {
			clone.selectMode = selected
		}

		if style == styleCurrency {
			requested, hasRequested := optionString(clone.options, "currency")
			if clone.currency != "" && hasRequested && !strings.EqualFold(clone.currency, requested) {
				err := errors.New("a Money value cannot be relabeled as another currency")
				ctx.OnError(err)
				return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
			}
			if clone.currency == "" {
				clone.currency = strings.ToUpper(requested)
			}
			if !validCurrency(clone.currency) {
				err := errors.New("currency formatting requires a currency code")
				ctx.OnError(err)
				return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
			}
		} else if clone.currency != "" {
			if style != styleNumber && style != styleInteger {
				err := errors.New("a Money value cannot change numeric style")
				ctx.OnError(err)
				return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
			}
			clone.currency = ""
		}
		return &clone
	}
}

func resolvedNumberOptions(style numericStyle, inherited, current map[string]any) map[string]any {
	resolved := withoutUniversalOptions(inherited)
	if resolved == nil {
		resolved = make(map[string]any, len(current))
	}
	switch style {
	case styleInteger:
		delete(resolved, "minimumFractionDigits")
		delete(resolved, "maximumFractionDigits")
		delete(resolved, "minimumSignificantDigits")
	case stylePercent:
		delete(resolved, "minimumIntegerDigits")
		delete(resolved, "roundingIncrement")
		delete(resolved, "select")
	}
	for name, value := range current {
		if strings.HasPrefix(name, "u:") {
			continue
		}
		resolved[name] = value
	}
	return resolved
}

func exactOffsetFunction(ctx functions.MessageFunctionContext, options functions.Options, operand any) messagevalue.MessageValue {
	resolved, resolveErr := resolveFunctionOperand(operand)
	if resolveErr != nil {
		ctx.OnError(resolveErr)
		return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
	}
	operand = resolved
	if value, ok := operand.(*nullMessageValue); ok && value != nil {
		return nullWithFunctionMetadata(ctx, value)
	}
	if value, ok := operand.(*exactNumberValue); ok && value != nil {
		if err := value.budget.require(0, 0); err != nil {
			return value
		}
	}
	value, err := numberOperand(operand, ctx)
	if err != nil {
		ctx.OnError(err)
		return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
	}
	delta, err := offsetDelta(withoutUniversalOptions(options.Map()))
	if err != nil {
		ctx.OnError(err)
		return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
	}
	adjusted, err := value.value.offset(delta)
	if err != nil {
		ctx.OnError(err)
		return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
	}
	clone := *value
	clone.value = adjusted
	clone.source = ctx.Source()
	clone.options = withoutUniversalOptions(value.options)
	clone.metadata = metadataFromFunctionOperand(ctx, value.Dir())
	return &clone
}

func validateOffsetConfiguration(options map[string]any) error {
	_, err := offsetDelta(options)
	return err
}

func offsetDelta(options map[string]any) (int64, error) {
	add, hasAdd := options["add"]
	subtract, hasSubtract := options["subtract"]
	if hasAdd == hasSubtract {
		return 0, errors.New("offset requires exactly one of add or subtract")
	}
	value := add
	sign := int64(1)
	name := "add"
	if hasSubtract {
		value = subtract
		sign = -1
		name = "subtract"
	}
	size, ok := digitSize(value)
	if !ok {
		return 0, fmt.Errorf("offset option %s must be an integer from 0 through 99", name)
	}
	return sign * int64(size), nil
}

func digitSize(value any) (int, bool) {
	switch value := value.(type) {
	case string:
		if value == "0" {
			return 0, true
		}
		if len(value) < 1 || len(value) > 2 || value[0] < '1' || value[0] > '9' {
			return 0, false
		}
		for index := 1; index < len(value); index++ {
			if value[index] < '0' || value[index] > '9' {
				return 0, false
			}
		}
		parsed, err := strconv.Atoi(value)
		return parsed, err == nil
	case int:
		return value, value >= 0 && value <= 99
	case int64:
		return int(value), value >= 0 && value <= 99
	case uint64:
		return int(value), value <= 99
	default:
		return 0, false
	}
}

func validateNumberConfiguration(localeName, name string, argumentType ArgumentType, options map[string]any) error {
	selectMode, hasSelect := optionString(options, "select")
	if hasSelect && selectMode != "exact" && selectMode != "plural" && selectMode != "cardinal" && selectMode != "ordinal" {
		return fmt.Errorf("%s select option %q is invalid", name, selectMode)
	}
	for _, option := range []string{"minimumIntegerDigits", "minimumFractionDigits", "maximumFractionDigits", "minimumSignificantDigits", "maximumSignificantDigits", "roundingIncrement"} {
		if value, ok := options[option]; ok {
			if _, valid := optionInt(value); !valid {
				return fmt.Errorf("%s option %s must be an integer", name, option)
			}
		}
	}
	if value, ok := options["fractionDigits"]; ok {
		if text, textOK := value.(string); !textOK || text != "auto" {
			if _, valid := digitSize(value); !valid {
				return fmt.Errorf("%s option fractionDigits must be auto or an integer from 0 through 99", name)
			}
		}
	}
	style := styleNumber
	switch name {
	case "integer":
		style = styleInteger
	case "currency":
		style = styleCurrency
	case "percent":
		style = stylePercent
	case "unit":
		style = styleUnit
	}
	currency := ""
	if name == "currency" {
		requested, requestedOK := optionString(options, "currency")
		if argumentType == TypeMoney {
			if requestedOK {
				return errors.New("a Money formatter cannot declare a replacement currency")
			}
			currency = "USD"
		} else {
			if !requestedOK || !validCurrency(requested) {
				return errors.New("currency option must be a three-letter code")
			}
			currency = strings.ToUpper(requested)
		}
	}
	v := exactNumberValue{
		value:         numericInput{kind: numericDecimal, decimal: "1.0"},
		grammarLocale: localeName,
		formatLocale:  localeName,
		style:         style,
		currency:      currency,
		options:       maps.Clone(options),
		selectMode:    selectMode,
	}
	locales, err := locale.ParseList(localeName)
	if err != nil {
		return err
	}
	formatter, err := numberformat.New(locales, v.numberOptions())
	if err != nil {
		return err
	}
	if err := validateResolvedNumberLocale(localeName, formatter.ResolvedOptions(), options); err != nil {
		return err
	}
	if requested, ok := optionString(options, "numberingSystem"); ok && formatter.ResolvedOptions().NumberingSystem != requested {
		return fmt.Errorf("%s numberingSystem option %q is unsupported", name, requested)
	}
	if hasSelect && selectMode != "exact" {
		rules, err := pluralrules.New(locales, v.pluralOptions())
		if err != nil {
			return err
		}
		if err := requireResolvedLocale(localeName, rules.ResolvedOptions().Locale); err != nil {
			return err
		}
	}
	return nil
}

func numberPluralCategories(localeName, name string, options map[string]any) []string {
	style := styleNumber
	if name == "integer" {
		style = styleInteger
	} else if name == "percent" {
		style = stylePercent
	}
	selectMode, _ := optionString(options, "select")
	v := exactNumberValue{
		value:         numericInput{kind: numericDecimal, decimal: "1.0"},
		grammarLocale: localeName,
		formatLocale:  localeName,
		style:         style,
		options:       maps.Clone(options),
		selectMode:    selectMode,
	}
	locales, err := locale.ParseList(localeName)
	if err != nil {
		return nil
	}
	rules, err := pluralrules.New(locales, v.pluralOptions())
	if err != nil {
		return nil
	}
	if err := requireResolvedLocale(localeName, rules.ResolvedOptions().Locale); err != nil {
		return nil
	}
	resolved := rules.ResolvedOptions()
	out := make([]string, len(resolved.PluralCategories))
	for i, category := range resolved.PluralCategories {
		out[i] = category.String()
	}
	return out
}

func exactStringFunction(ctx functions.MessageFunctionContext, _ functions.Options, operand any) messagevalue.MessageValue {
	resolved, resolveErr := resolveFunctionOperand(operand)
	if resolveErr != nil {
		ctx.OnError(resolveErr)
		return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
	}
	operand = resolved
	if value, ok := operand.(*nullMessageValue); ok && value != nil {
		return nullWithFunctionMetadata(ctx, value)
	}
	if value, ok := operand.(*boundedStringValue); ok && value != nil {
		if err := value.budget.require(0, 0); err != nil {
			return value
		}
		clone := *value
		clone.source = ctx.Source()
		clone.metadata = metadataFromFunctionOperand(ctx, value.Dir())
		return &clone
	}
	if value, ok := operand.(string); ok {
		return &boundedStringValue{value: value, locale: firstLocale(ctx.Locales()), source: ctx.Source(), metadata: metadataFromFunctionOperand(ctx, bidi.DirAuto)}
	}
	ctx.OnError(errors.New("string function requires a text, enum, or bool argument"))
	return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
}

func numberOperand(operand any, ctx functions.MessageFunctionContext) (*exactNumberValue, error) {
	if value, ok := operand.(*exactNumberValue); ok && value != nil {
		return value, nil
	}
	if value, ok := operand.(messagevalue.Valuer); ok {
		underlying, err := value.ValueOf()
		if err != nil {
			return nil, err
		}
		operand = underlying
	}
	if value, ok := operand.(*exactNumberValue); ok && value != nil {
		return value, nil
	}
	input, err := numericFromAny(operand)
	if err != nil {
		return nil, err
	}
	return &exactNumberValue{value: input, grammarLocale: firstLocale(ctx.Locales()), formatLocale: firstLocale(ctx.Locales()), source: ctx.Source(), style: styleNumber}, nil
}

func numericFromAny(value any) (numericInput, error) {
	switch value := value.(type) {
	case numericInput:
		return value.clone(), nil
	case int:
		return numericInput{kind: numericSigned, signed: int64(value)}, nil
	case int8:
		return numericInput{kind: numericSigned, signed: int64(value)}, nil
	case int16:
		return numericInput{kind: numericSigned, signed: int64(value)}, nil
	case int32:
		return numericInput{kind: numericSigned, signed: int64(value)}, nil
	case int64:
		return numericInput{kind: numericSigned, signed: value}, nil
	case uint:
		return numericInput{kind: numericUnsigned, unsigned: uint64(value)}, nil
	case uint8:
		return numericInput{kind: numericUnsigned, unsigned: uint64(value)}, nil
	case uint16:
		return numericInput{kind: numericUnsigned, unsigned: uint64(value)}, nil
	case uint32:
		return numericInput{kind: numericUnsigned, unsigned: uint64(value)}, nil
	case uint64:
		return numericInput{kind: numericUnsigned, unsigned: value}, nil
	case *big.Int:
		if value == nil {
			return numericInput{}, errors.New("numeric operand is nil")
		}
		return numericInput{kind: numericBig, big: new(big.Int).Set(value)}, nil
	case big.Int:
		return numericInput{kind: numericBig, big: new(big.Int).Set(&value)}, nil
	case string:
		if err := validateDecimal(value); err != nil {
			return numericInput{}, err
		}
		return numericInput{kind: numericDecimal, decimal: value}, nil
	default:
		return numericInput{}, fmt.Errorf("numeric operand has type %T", value)
	}
}

func copyNumberOptions(values map[string]any, number *numberformat.Options, plural *pluralrules.Options) {
	for name, value := range values {
		switch name {
		case "fractionDigits":
			if number == nil {
				continue
			}
			integer, ok := digitSize(value)
			if !ok {
				continue
			}
			number.MinimumFractionDigits = &integer
			number.MaximumFractionDigits = &integer
		case "minimumIntegerDigits", "minimumFractionDigits", "maximumFractionDigits", "minimumSignificantDigits", "maximumSignificantDigits", "roundingIncrement":
			integer, ok := optionInt(value)
			if !ok {
				continue
			}
			setIntegerOption(name, integer, number, plural)
		case "roundingMode", "roundingPriority", "trailingZeroDisplay", "notation", "compactDisplay", "localeMatcher":
			text, ok := value.(string)
			if !ok {
				continue
			}
			setSharedStringOption(name, text, number, plural)
		case "useGrouping", "signDisplay", "currencyDisplay", "currencySign", "unit", "unitDisplay", "numberingSystem":
			if number == nil {
				continue
			}
			text, ok := value.(string)
			if ok {
				setNumberStringOption(name, text, number)
			}
		}
	}
}

func setIntegerOption(name string, value int, number *numberformat.Options, plural *pluralrules.Options) {
	if number != nil {
		switch name {
		case "minimumIntegerDigits":
			number.MinimumIntegerDigits = &value
		case "minimumFractionDigits":
			number.MinimumFractionDigits = &value
		case "maximumFractionDigits":
			number.MaximumFractionDigits = &value
		case "minimumSignificantDigits":
			number.MinimumSignificantDigits = &value
		case "maximumSignificantDigits":
			number.MaximumSignificantDigits = &value
		case "roundingIncrement":
			number.RoundingIncrement = &value
		}
	}
	if plural != nil {
		switch name {
		case "minimumIntegerDigits":
			plural.MinimumIntegerDigits = &value
		case "minimumFractionDigits":
			plural.MinimumFractionDigits = &value
		case "maximumFractionDigits":
			plural.MaximumFractionDigits = &value
		case "minimumSignificantDigits":
			plural.MinimumSignificantDigits = &value
		case "maximumSignificantDigits":
			plural.MaximumSignificantDigits = &value
		case "roundingIncrement":
			plural.RoundingIncrement = &value
		}
	}
}

func setSharedStringOption(name, value string, number *numberformat.Options, plural *pluralrules.Options) {
	if number != nil {
		switch name {
		case "roundingMode":
			number.RoundingMode = &value
		case "roundingPriority":
			number.RoundingPriority = &value
		case "trailingZeroDisplay":
			number.TrailingZeroDisplay = &value
		case "notation":
			number.Notation = &value
		case "compactDisplay":
			number.CompactDisplay = &value
		case "localeMatcher":
			number.LocaleMatcher = &value
		}
	}
	if plural != nil {
		switch name {
		case "roundingMode":
			plural.RoundingMode = &value
		case "roundingPriority":
			plural.RoundingPriority = &value
		case "trailingZeroDisplay":
			plural.TrailingZeroDisplay = &value
		case "notation":
			plural.Notation = &value
		case "compactDisplay":
			plural.CompactDisplay = &value
		case "localeMatcher":
			plural.LocaleMatcher = &value
		}
	}
}

func setNumberStringOption(name, value string, options *numberformat.Options) {
	switch name {
	case "useGrouping":
		if value == "never" {
			value = "false"
		}
		options.UseGrouping = &value
	case "signDisplay":
		options.SignDisplay = &value
	case "currencyDisplay":
		options.CurrencyDisplay = &value
	case "currencySign":
		options.CurrencySign = &value
	case "unit":
		options.Unit = &value
	case "unitDisplay":
		options.UnitDisplay = &value
	case "numberingSystem":
		options.NumberingSystem = &value
	}
}

func optionString(options map[string]any, name string) (string, bool) {
	value, ok := options[name]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func optionInt(value any) (int, bool) {
	switch value := value.(type) {
	case int:
		return value, true
	case int64:
		return int(value), int64(int(value)) == value
	case uint64:
		return int(value), uint64(int(value)) == value
	case string:
		parsed, err := strconv.Atoi(value)
		return parsed, err == nil
	default:
		return 0, false
	}
}

type decimalParts struct {
	negative bool
	digits   string
	fraction int
	exponent int
}

const maxDecimalBytes = 10000

func parseDecimal(value string) (decimalParts, error) {
	if value == "" || len(value) > maxDecimalBytes {
		return decimalParts{}, errors.New("decimal is empty or too large")
	}
	parts := decimalParts{}
	if value[0] == '-' {
		parts.negative = true
		value = value[1:]
	}
	mantissa, exponentText, hasExponent := strings.Cut(value, "e")
	if !hasExponent {
		mantissa, exponentText, hasExponent = strings.Cut(value, "E")
	}
	if hasExponent {
		if exponentText == "" || len(exponentText) > 7 {
			return decimalParts{}, errors.New("decimal exponent is invalid")
		}
		exponent, err := strconv.Atoi(exponentText)
		if err != nil || exponent < -10000 || exponent > 10000 {
			return decimalParts{}, errors.New("decimal exponent is outside the supported range")
		}
		parts.exponent = exponent
	}
	integer, fraction, hasFraction := strings.Cut(mantissa, ".")
	if integer == "" || (len(integer) > 1 && integer[0] == '0') || (hasFraction && fraction == "") {
		return decimalParts{}, errors.New("decimal syntax is invalid")
	}
	for _, text := range []string{integer, fraction} {
		for _, r := range text {
			if r < '0' || r > '9' {
				return decimalParts{}, errors.New("decimal syntax is invalid")
			}
		}
	}
	parts.digits = integer + fraction
	parts.fraction = len(fraction)
	return parts, nil
}

func (p decimalParts) String() string {
	sign := ""
	if p.negative {
		sign = "-"
	}
	point := len(p.digits) - p.fraction + p.exponent
	var value string
	switch {
	case point <= 0:
		value = "0." + strings.Repeat("0", -point) + p.digits
	case point >= len(p.digits):
		value = p.digits + strings.Repeat("0", point-len(p.digits))
	default:
		value = p.digits[:point] + "." + p.digits[point:]
	}
	integer, fraction, fractional := strings.Cut(value, ".")
	integer = strings.TrimLeft(integer, "0")
	if integer == "" {
		integer = "0"
	}
	if fractional {
		return sign + integer + "." + fraction
	}
	return sign + integer
}

func validateDecimal(value string) error {
	if _, err := parseDecimal(value); err != nil {
		return err
	}
	if digits, err := visibleFractionDigits(value); err != nil || digits > 100 {
		return errors.New("decimal has more than 100 visible fraction digits")
	}
	if _, err := numberformat.Decimal(value); err != nil {
		return err
	}
	if _, err := pluralrules.Decimal(value); err != nil {
		return err
	}
	return nil
}

func visibleFractionDigits(value string) (int, error) {
	parts, err := parseDecimal(value)
	if err != nil {
		return 0, err
	}
	expanded := parts.String()
	_, fraction, ok := strings.Cut(expanded, ".")
	if !ok {
		return 0, nil
	}
	return len(fraction), nil
}

func numericEqual(left, right string) (bool, error) {
	leftRat, err := decimalRat(left)
	if err != nil {
		return false, err
	}
	rightRat, err := decimalRat(right)
	if err != nil {
		return false, err
	}
	return leftRat.Cmp(rightRat) == 0, nil
}

func decimalRat(value string) (*big.Rat, error) {
	parts, err := parseDecimal(value)
	if err != nil {
		return nil, err
	}
	numerator := new(big.Int)
	if _, ok := numerator.SetString(parts.digits, 10); !ok {
		return nil, errors.New("decimal digits are invalid")
	}
	if parts.negative {
		numerator.Neg(numerator)
	}
	scale := parts.fraction - parts.exponent
	if scale <= 0 {
		numerator.Mul(numerator, pow10(-scale))
		return new(big.Rat).SetInt(numerator), nil
	}
	return new(big.Rat).SetFrac(numerator, pow10(scale)), nil
}

func pow10(power int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(power)), nil)
}

func looksNumeric(value string) bool {
	if value == "" {
		return false
	}
	first := value[0]
	return first == '-' || (first >= '0' && first <= '9')
}

func firstLocale(locales []string) string {
	if len(locales) == 0 || locales[0] == "" {
		return "en"
	}
	return locales[0]
}

type formattedMessagePart struct {
	kind   string
	value  string
	source string
	locale string
	dir    bidi.Direction
	id     string
	parts  []Subpart
}

func (p formattedMessagePart) Type() string        { return p.kind }
func (p formattedMessagePart) Value() any          { return p.value }
func (p formattedMessagePart) Source() string      { return p.source }
func (p formattedMessagePart) Locale() string      { return p.locale }
func (p formattedMessagePart) Dir() bidi.Direction { return p.dir }
func (p formattedMessagePart) ID() string          { return p.id }
func (p formattedMessagePart) Parts() []messagevalue.MessagePart {
	if p.parts == nil {
		return nil
	}
	parts := make([]messagevalue.MessagePart, len(p.parts))
	for index, part := range p.parts {
		parts[index] = formattedSubpart{partType: part.Type, value: part.Text, source: p.source, locale: p.locale, dir: p.dir}
	}
	return parts
}

type formattedSubpart struct {
	partType string
	value    string
	source   string
	locale   string
	dir      bidi.Direction
}

func (p formattedSubpart) Type() string        { return p.partType }
func (p formattedSubpart) Value() any          { return p.value }
func (p formattedSubpart) Source() string      { return p.source }
func (p formattedSubpart) Locale() string      { return p.locale }
func (p formattedSubpart) Dir() bidi.Direction { return p.dir }
