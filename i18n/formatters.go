package i18n

import (
	"context"
	"fmt"
	"math/big"
	"time"
	"unicode/utf8"

	"github.com/agentable/go-intl/displaynames"
	"github.com/agentable/go-intl/durationformat"
	"github.com/agentable/go-intl/listformat"
	"github.com/agentable/go-intl/locale"
	"github.com/agentable/go-intl/relativetimeformat"
	"github.com/kaptinlin/messageformat-go/pkg/bidi"
	"github.com/kaptinlin/messageformat-go/pkg/messagevalue"
)

type FormattedValue struct {
	Text   string
	Parts  []Part
	Locale string
}

type RangeSource uint8

const (
	RangeSourceStart RangeSource = iota + 1
	RangeSourceShared
	RangeSourceEnd
)

func (s RangeSource) String() string {
	switch s {
	case RangeSourceStart:
		return "startRange"
	case RangeSourceShared:
		return "shared"
	case RangeSourceEnd:
		return "endRange"
	default:
		return "unknown"
	}
}

func (s RangeSource) Valid() bool { return s >= RangeSourceStart && s <= RangeSourceEnd }

type RangePart struct {
	Kind      PartKind
	Type      string
	Text      string
	Locale    string
	Direction string
	Source    RangeSource
}

type FormattedRange struct {
	Text   string
	Parts  []RangePart
	Locale string
}

type FormatWidth uint8

const (
	FormatWidthDefault FormatWidth = iota
	FormatWidthLong
	FormatWidthShort
	FormatWidthNarrow
)

func (w FormatWidth) String() string {
	switch w {
	case FormatWidthDefault:
		return "default"
	case FormatWidthLong:
		return "long"
	case FormatWidthShort:
		return "short"
	case FormatWidthNarrow:
		return "narrow"
	default:
		return "unknown"
	}
}

func (w FormatWidth) Valid() bool { return w <= FormatWidthNarrow }

type ListType uint8

const (
	ListConjunction ListType = iota
	ListDisjunction
	ListUnit
)

func (t ListType) String() string {
	switch t {
	case ListConjunction:
		return "conjunction"
	case ListDisjunction:
		return "disjunction"
	case ListUnit:
		return "unit"
	default:
		return "unknown"
	}
}

func (t ListType) Valid() bool { return t <= ListUnit }

type ListFormatSpec struct {
	Type  ListType
	Width FormatWidth
}

type RelativeUnit uint8

const (
	RelativeSecond RelativeUnit = iota + 1
	RelativeMinute
	RelativeHour
	RelativeDay
	RelativeWeek
	RelativeMonth
	RelativeQuarter
	RelativeYear
)

func (u RelativeUnit) String() string {
	switch u {
	case RelativeSecond:
		return "second"
	case RelativeMinute:
		return "minute"
	case RelativeHour:
		return "hour"
	case RelativeDay:
		return "day"
	case RelativeWeek:
		return "week"
	case RelativeMonth:
		return "month"
	case RelativeQuarter:
		return "quarter"
	case RelativeYear:
		return "year"
	default:
		return "unknown"
	}
}

func (u RelativeUnit) Valid() bool { return u >= RelativeSecond && u <= RelativeYear }

type RelativeNumeric uint8

const (
	RelativeNumericAlways RelativeNumeric = iota
	RelativeNumericAuto
)

func (n RelativeNumeric) String() string {
	switch n {
	case RelativeNumericAlways:
		return "always"
	case RelativeNumericAuto:
		return "auto"
	default:
		return "unknown"
	}
}

func (n RelativeNumeric) Valid() bool { return n <= RelativeNumericAuto }

type RelativeFormatSpec struct {
	Width           FormatWidth
	Numeric         RelativeNumeric
	NumberingSystem string
}

type DurationValue struct {
	Years        int64
	Months       int64
	Weeks        int64
	Days         int64
	Hours        int64
	Minutes      int64
	Seconds      int64
	Milliseconds int64
	Microseconds int64
	Nanoseconds  int64
}

type DurationStyle uint8

const (
	DurationShort DurationStyle = iota
	DurationLong
	DurationNarrow
	DurationDigital
)

func (s DurationStyle) String() string {
	switch s {
	case DurationShort:
		return "short"
	case DurationLong:
		return "long"
	case DurationNarrow:
		return "narrow"
	case DurationDigital:
		return "digital"
	default:
		return "unknown"
	}
}

func (s DurationStyle) Valid() bool { return s <= DurationDigital }

type DurationUnitStyle uint8

const (
	DurationUnitDefault DurationUnitStyle = iota
	DurationUnitLong
	DurationUnitShort
	DurationUnitNarrow
	DurationUnitNumeric
	DurationUnitTwoDigit
)

func (s DurationUnitStyle) String() string {
	switch s {
	case DurationUnitDefault:
		return "default"
	case DurationUnitLong:
		return "long"
	case DurationUnitShort:
		return "short"
	case DurationUnitNarrow:
		return "narrow"
	case DurationUnitNumeric:
		return "numeric"
	case DurationUnitTwoDigit:
		return "2-digit"
	default:
		return "unknown"
	}
}

func (s DurationUnitStyle) Valid() bool { return s <= DurationUnitTwoDigit }

type DurationDisplay uint8

const (
	DurationDisplayDefault DurationDisplay = iota
	DurationDisplayAuto
	DurationDisplayAlways
)

func (d DurationDisplay) String() string {
	switch d {
	case DurationDisplayDefault:
		return "default"
	case DurationDisplayAuto:
		return "auto"
	case DurationDisplayAlways:
		return "always"
	default:
		return "unknown"
	}
}

func (d DurationDisplay) Valid() bool { return d <= DurationDisplayAlways }

type DurationFractionalDigits uint8

const (
	DurationFractionalDigitsDefault DurationFractionalDigits = iota
	DurationFractionalDigits0
	DurationFractionalDigits1
	DurationFractionalDigits2
	DurationFractionalDigits3
	DurationFractionalDigits4
	DurationFractionalDigits5
	DurationFractionalDigits6
	DurationFractionalDigits7
	DurationFractionalDigits8
	DurationFractionalDigits9
)

func (d DurationFractionalDigits) String() string {
	if d == DurationFractionalDigitsDefault {
		return "default"
	}
	if !d.Valid() {
		return "unknown"
	}
	return fmt.Sprintf("%d", d-1)
}

func (d DurationFractionalDigits) Valid() bool { return d <= DurationFractionalDigits9 }

func (d DurationFractionalDigits) value() (int, bool) {
	if d == DurationFractionalDigitsDefault || !d.Valid() {
		return 0, false
	}
	return int(d - 1), true
}

type DurationUnitSpec struct {
	Style   DurationUnitStyle
	Display DurationDisplay
}

type DurationFormatSpec struct {
	Style            DurationStyle
	NumberingSystem  string
	FractionalDigits DurationFractionalDigits
	Years            DurationUnitSpec
	Months           DurationUnitSpec
	Weeks            DurationUnitSpec
	Days             DurationUnitSpec
	Hours            DurationUnitSpec
	Minutes          DurationUnitSpec
	Seconds          DurationUnitSpec
	Milliseconds     DurationUnitSpec
	Microseconds     DurationUnitSpec
	Nanoseconds      DurationUnitSpec
}

type DisplayNameType uint8

const (
	DisplayLanguage DisplayNameType = iota + 1
	DisplayRegion
	DisplayScript
	DisplayCurrency
	DisplayCalendar
	DisplayDateTimeField
)

func (t DisplayNameType) String() string {
	switch t {
	case DisplayLanguage:
		return "language"
	case DisplayRegion:
		return "region"
	case DisplayScript:
		return "script"
	case DisplayCurrency:
		return "currency"
	case DisplayCalendar:
		return "calendar"
	case DisplayDateTimeField:
		return "dateTimeField"
	default:
		return "unknown"
	}
}

func (t DisplayNameType) Valid() bool { return t >= DisplayLanguage && t <= DisplayDateTimeField }

type DisplayNameFallback uint8

const (
	DisplayNameCode DisplayNameFallback = iota
	DisplayNameNone
)

func (f DisplayNameFallback) String() string {
	switch f {
	case DisplayNameCode:
		return "code"
	case DisplayNameNone:
		return "none"
	default:
		return "unknown"
	}
}

func (f DisplayNameFallback) Valid() bool { return f <= DisplayNameNone }

type LanguageDisplay uint8

const (
	LanguageDialect LanguageDisplay = iota
	LanguageStandard
)

func (d LanguageDisplay) String() string {
	switch d {
	case LanguageDialect:
		return "dialect"
	case LanguageStandard:
		return "standard"
	default:
		return "unknown"
	}
}

func (d LanguageDisplay) Valid() bool { return d <= LanguageStandard }

type DisplayNameSpec struct {
	Type            DisplayNameType
	Width           FormatWidth
	Fallback        DisplayNameFallback
	LanguageDisplay LanguageDisplay
}

type directFormatPart struct {
	kind      PartKind
	typeName  string
	text      string
	name      string
	direction bidi.Direction
	isolate   bool
}

type directRangePart struct {
	kind      PartKind
	typeName  string
	text      string
	direction bidi.Direction
	source    RangeSource
}

type displayNameResult struct {
	formatted FormattedValue
	found     bool
}

const maximumExactIntlInteger = int64(1<<53 - 1)

func (v *View) FormatList(ctx context.Context, values []string, spec ListFormatSpec) (FormattedValue, error) {
	return formatList(ctx, v, values, spec)
}

func formatList(ctx context.Context, target directFormatTarget, values []string, spec ListFormatSpec) (FormattedValue, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedValue, error) {
		if !spec.Type.Valid() || !spec.Width.Valid() {
			return FormattedValue{}, fmt.Errorf("%w: invalid list format", ErrInvalidMessage)
		}
		if len(values) > state.limits.MaxArguments {
			return FormattedValue{}, fmt.Errorf("%w: list exceeds %d elements", ErrLimitExceeded, state.limits.MaxArguments)
		}
		total := 0
		for index, value := range values {
			if index&31 == 0 {
				if err := ctx.Err(); err != nil {
					return FormattedValue{}, err
				}
			}
			if err := validateDirectText(value); err != nil {
				return FormattedValue{}, fmt.Errorf("%w: list element %d: %v", ErrInvalidMessage, index, err)
			}
			if len(value) > state.limits.MaxArgumentBytes-total {
				return FormattedValue{}, fmt.Errorf("%w: list exceeds %d bytes", ErrLimitExceeded, state.limits.MaxArgumentBytes)
			}
			total += len(value)
		}
		maximumBytes := saturatedDirectAdd(total, formattedValueOverhead)
		if state.presentation != PresentationNoIsolation {
			maximumBytes = saturatedDirectAdd(maximumBytes, saturatedDirectMultiply(len(values), len(string(bidi.FSI))+len(string(bidi.PDI))))
		}
		if err := state.requireDirectOutput(maximumBytes, saturatedDirectAdd(saturatedDirectMultiply(len(values), 4), 1)); err != nil {
			return FormattedValue{}, err
		}
		locales, err := locale.ParseList(state.locale)
		if err != nil {
			return FormattedValue{}, fmt.Errorf("%w: %v", ErrInvalidLocale, err)
		}
		options := listformat.Options{}
		typeName := spec.Type.String()
		options.Type = &typeName
		if width := directWidth(spec.Width); width != "" {
			options.Style = &width
		}
		formatter, err := listformat.New(locales, options)
		if err != nil {
			return FormattedValue{}, fmt.Errorf("%w: list format: %v", ErrInvalidMessage, err)
		}
		resolved := formatter.ResolvedOptions().Locale
		if err := requireResolvedLocale(state.locale, resolved); err != nil {
			return FormattedValue{}, fmt.Errorf("%w: list formatting: %v", ErrInvalidLocale, err)
		}
		source := formatter.FormatToParts(values)
		parts := make([]directFormatPart, 0, len(source))
		for _, part := range source {
			kind := PartText
			isolate := false
			direction := bidi.GetLocaleDirection(resolved.String())
			if part.Type == listformat.PartElement {
				kind = PartValue
				isolate = true
				direction = bidi.DirAuto
			}
			parts = append(parts, directFormatPart{kind: kind, typeName: string(part.Type), text: part.Value, name: string(part.Type), direction: direction, isolate: isolate})
		}
		return state.finishDirectFormat(ctx, resolved.String(), parts)
	})
}

func (v *View) FormatRelative(ctx context.Context, value int64, unit RelativeUnit, spec RelativeFormatSpec) (FormattedValue, error) {
	return formatRelative(ctx, v, value, unit, spec)
}

func formatRelative(ctx context.Context, target directFormatTarget, value int64, unit RelativeUnit, spec RelativeFormatSpec) (FormattedValue, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedValue, error) {
		if !unit.Valid() || !spec.Width.Valid() || !spec.Numeric.Valid() {
			return FormattedValue{}, fmt.Errorf("%w: invalid relative-time format", ErrInvalidMessage)
		}
		if value < -maximumExactIntlInteger || value > maximumExactIntlInteger {
			return FormattedValue{}, fmt.Errorf("%w: relative-time value is not exactly representable", ErrInvalidMessage)
		}
		if err := validateDirectOptionBounds(map[string]any{"numberingSystem": spec.NumberingSystem}, state.limits); err != nil {
			return FormattedValue{}, err
		}
		if err := state.requireDirectOutput(formattedValueOverhead, maximumRelativeValueParts); err != nil {
			return FormattedValue{}, err
		}
		locales, err := locale.ParseList(state.locale)
		if err != nil {
			return FormattedValue{}, fmt.Errorf("%w: %v", ErrInvalidLocale, err)
		}
		options := relativetimeformat.Options{}
		if width := directWidth(spec.Width); width != "" {
			options.Style = &width
		}
		if spec.Numeric == RelativeNumericAuto {
			numeric := spec.Numeric.String()
			options.Numeric = &numeric
		}
		if spec.NumberingSystem != "" {
			options.NumberingSystem = &spec.NumberingSystem
		}
		formatter, err := relativetimeformat.New(locales, options)
		if err != nil {
			return FormattedValue{}, fmt.Errorf("%w: relative-time format: %v", ErrInvalidMessage, err)
		}
		resolved := formatter.ResolvedOptions()
		if err := validateDirectNumberLocale(state.locale, resolved.Locale, resolved.NumberingSystem, spec.NumberingSystem); err != nil {
			return FormattedValue{}, fmt.Errorf("%w: relative-time formatting: %v", ErrInvalidLocale, err)
		}
		source, err := formatter.FormatToParts(relativetimeformat.Int(value), relativetimeformat.Unit(unit.String()))
		if err != nil {
			return FormattedValue{}, fmt.Errorf("%w: relative-time format: %v", ErrInvalidMessage, err)
		}
		parts := make([]directFormatPart, 0, len(source))
		for _, part := range source {
			kind := PartValue
			if part.Type == relativetimeformat.PartLiteral {
				kind = PartText
			}
			parts = append(parts, directFormatPart{kind: kind, typeName: string(part.Type), text: part.Value, name: string(part.Unit), direction: bidi.GetLocaleDirection(resolved.Locale.String())})
		}
		return state.finishDirectFormat(ctx, resolved.Locale.String(), parts)
	})
}

func (v *View) FormatDuration(ctx context.Context, value DurationValue, spec DurationFormatSpec) (FormattedValue, error) {
	return formatDuration(ctx, v, value, spec)
}

func formatDuration(ctx context.Context, target directFormatTarget, value DurationValue, spec DurationFormatSpec) (FormattedValue, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedValue, error) {
		options, err := directDurationOptions(spec)
		if err != nil {
			return FormattedValue{}, err
		}
		if err := validateDirectOptionBounds(map[string]any{"numberingSystem": spec.NumberingSystem}, state.limits); err != nil {
			return FormattedValue{}, err
		}
		if err := validateDirectDuration(value); err != nil {
			return FormattedValue{}, err
		}
		if err := state.requireDirectOutput(maximumDurationValueBytes, maximumDurationValueParts); err != nil {
			return FormattedValue{}, err
		}
		locales, err := locale.ParseList(state.locale)
		if err != nil {
			return FormattedValue{}, fmt.Errorf("%w: %v", ErrInvalidLocale, err)
		}
		formatter, err := durationformat.New(locales, options)
		if err != nil {
			return FormattedValue{}, fmt.Errorf("%w: duration format: %v", ErrInvalidMessage, err)
		}
		resolved := formatter.ResolvedOptions()
		if err := validateDirectNumberLocale(state.locale, resolved.Locale, resolved.NumberingSystem, spec.NumberingSystem); err != nil {
			return FormattedValue{}, fmt.Errorf("%w: duration formatting: %v", ErrInvalidLocale, err)
		}
		source, err := formatter.FormatToParts(durationformat.Duration{
			Years:        float64(value.Years),
			Months:       float64(value.Months),
			Weeks:        float64(value.Weeks),
			Days:         float64(value.Days),
			Hours:        float64(value.Hours),
			Minutes:      float64(value.Minutes),
			Seconds:      float64(value.Seconds),
			Milliseconds: float64(value.Milliseconds),
			Microseconds: float64(value.Microseconds),
			Nanoseconds:  float64(value.Nanoseconds),
		})
		if err != nil {
			return FormattedValue{}, fmt.Errorf("%w: duration format: %v", ErrInvalidMessage, err)
		}
		parts := make([]directFormatPart, 0, len(source))
		for _, part := range source {
			kind := PartValue
			if part.Type == durationformat.PartLiteral {
				kind = PartText
			}
			parts = append(parts, directFormatPart{kind: kind, typeName: string(part.Type), text: part.Value, name: string(part.Unit), direction: bidi.GetLocaleDirection(resolved.Locale.String())})
		}
		return state.finishDirectFormat(ctx, resolved.Locale.String(), parts)
	})
}

func (v *View) FormatDisplayName(ctx context.Context, code string, spec DisplayNameSpec) (FormattedValue, bool, error) {
	return formatDisplayName(ctx, v, code, spec)
}

func formatDisplayName(ctx context.Context, target directFormatTarget, code string, spec DisplayNameSpec) (FormattedValue, bool, error) {
	result, err := runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (displayNameResult, error) {
		if !spec.Type.Valid() || !spec.Width.Valid() || !spec.Fallback.Valid() || !spec.LanguageDisplay.Valid() {
			return displayNameResult{}, fmt.Errorf("%w: invalid display-name format", ErrInvalidMessage)
		}
		if len(code) > max(state.limits.MaxIdentifierBytes, state.limits.Locale.MaxTagBytes) {
			return displayNameResult{}, fmt.Errorf("%w: display-name code is too large", ErrLimitExceeded)
		}
		if err := validateDirectText(code); err != nil {
			return displayNameResult{}, fmt.Errorf("%w: display-name code: %v", ErrInvalidMessage, err)
		}
		if err := state.requireDirectOutput(formattedValueOverhead, 1); err != nil {
			return displayNameResult{}, err
		}
		locales, err := locale.ParseList(state.locale)
		if err != nil {
			return displayNameResult{}, fmt.Errorf("%w: %v", ErrInvalidLocale, err)
		}
		typeName := spec.Type.String()
		options := displaynames.Options{Type: &typeName}
		if width := directWidth(spec.Width); width != "" {
			options.Style = &width
		}
		if spec.Fallback == DisplayNameNone {
			fallback := spec.Fallback.String()
			options.Fallback = &fallback
		}
		if spec.LanguageDisplay == LanguageStandard {
			languageDisplay := spec.LanguageDisplay.String()
			options.LanguageDisplay = &languageDisplay
		}
		formatter, err := displaynames.New(locales, options)
		if err != nil {
			return displayNameResult{}, fmt.Errorf("%w: display-name format: %v", ErrInvalidMessage, err)
		}
		resolved := formatter.ResolvedOptions().Locale
		if err := requireResolvedLocale(state.locale, resolved); err != nil {
			return displayNameResult{}, fmt.Errorf("%w: display-name formatting: %v", ErrInvalidLocale, err)
		}
		name, found, err := formatter.Of(code)
		if err != nil {
			return displayNameResult{}, fmt.Errorf("%w: display-name format: %v", ErrInvalidMessage, err)
		}
		if !found {
			return displayNameResult{formatted: FormattedValue{Locale: resolved.String()}}, nil
		}
		formatted, err := state.finishDirectFormat(ctx, resolved.String(), []directFormatPart{{kind: PartValue, typeName: "displayName", text: name, name: spec.Type.String(), direction: bidi.GetLocaleDirection(resolved.String())}})
		return displayNameResult{formatted: formatted, found: err == nil}, err
	})
	return result.formatted, result.found, err
}

const (
	maximumRelativeValueParts = 16
	maximumDurationValueBytes = formattedValueOverhead * 4
	maximumDurationValueParts = 256
)

func runDirectFormat[T any](ctx context.Context, target directFormatTarget, format func(context.Context, directFormatContext) (T, error)) (result T, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var state directFormatContext
	stateValid := false
	started := time.Now()
	defer func() {
		if recover() != nil {
			var zero T
			result = zero
			err = fmt.Errorf("%w: formatter panic", ErrInvalidMessage)
		}
		if stateValid {
			outcome, reason := terminalOperationResult(err)
			notifyObserver(ctx, state.observer, Observation{Operation: OperationRender, Outcome: outcome, Reason: reason, Duration: time.Since(started), Count: 1})
		}
	}()
	if target == nil {
		return result, fmt.Errorf("%w: formatter is nil", ErrInvalidMessage)
	}
	state, err = target.directFormatting()
	if err != nil {
		return result, err
	}
	stateValid = true
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return format(ctx, state)
}

func validateDirectText(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("text is not valid UTF-8")
	}
	if hasUnsafeBidiControls(value) {
		return fmt.Errorf("text contains directional controls")
	}
	return nil
}

func validateDirectOptionBounds(options map[string]any, limits Limits) error {
	for _, name := range sortedMapKeys(options) {
		value := options[name]
		text, ok := value.(string)
		if !ok {
			continue
		}
		if len(text) > limits.MaxIdentifierBytes {
			return fmt.Errorf("%w: option %s exceeds %d bytes", ErrLimitExceeded, name, limits.MaxIdentifierBytes)
		}
		if !utf8.ValidString(text) || hasUnsafeBidiControls(text) {
			return fmt.Errorf("%w: option %s is unsafe", ErrInvalidMessage, name)
		}
	}
	return nil
}

func directWidth(width FormatWidth) string {
	if width == FormatWidthDefault {
		return ""
	}
	return width.String()
}

func validateDirectNumberLocale(requested string, resolved locale.Locale, resolvedNumberingSystem, configuredNumberingSystem string) error {
	if err := requireResolvedLocale(requested, resolved); err != nil {
		return err
	}
	requestedLocale, err := locale.Parse(requested)
	if err != nil {
		return err
	}
	numberingSystem := requestedLocale.NumberingSystem()
	if configuredNumberingSystem != "" {
		numberingSystem = configuredNumberingSystem
	}
	if numberingSystem != "" && resolvedNumberingSystem != numberingSystem {
		return fmt.Errorf("numbering system %q resolves to %q", numberingSystem, resolvedNumberingSystem)
	}
	return nil
}

func validateDirectDuration(value DurationValue) error {
	values := [...]int64{
		value.Years,
		value.Months,
		value.Weeks,
		value.Days,
		value.Hours,
		value.Minutes,
		value.Seconds,
		value.Milliseconds,
		value.Microseconds,
		value.Nanoseconds,
	}
	sign := int64(0)
	for _, value := range values {
		if value < -maximumExactIntlInteger || value > maximumExactIntlInteger {
			return fmt.Errorf("%w: duration value is not exactly representable", ErrInvalidMessage)
		}
		if value == 0 {
			continue
		}
		if sign == 0 {
			sign = value
			continue
		}
		if (sign < 0) != (value < 0) {
			return fmt.Errorf("%w: duration fields must have one sign", ErrInvalidMessage)
		}
	}
	for _, value := range values[:3] {
		magnitude := new(big.Int).Abs(big.NewInt(value))
		if magnitude.BitLen() > 32 || magnitude.Cmp(new(big.Int).Lsh(big.NewInt(1), 32)) >= 0 {
			return fmt.Errorf("%w: duration calendar unit is outside the supported range", ErrInvalidMessage)
		}
	}
	total := big.NewInt(0)
	scaled := []struct {
		value int64
		scale int64
	}{
		{value.Days, 86_400_000_000_000},
		{value.Hours, 3_600_000_000_000},
		{value.Minutes, 60_000_000_000},
		{value.Seconds, 1_000_000_000},
		{value.Milliseconds, 1_000_000},
		{value.Microseconds, 1_000},
		{value.Nanoseconds, 1},
	}
	for _, unit := range scaled {
		term := new(big.Int).Mul(big.NewInt(unit.value), big.NewInt(unit.scale))
		total.Add(total, term)
	}
	limit := new(big.Int).Lsh(big.NewInt(1_000_000_000), 53)
	if new(big.Int).Abs(total).Cmp(limit) >= 0 {
		return fmt.Errorf("%w: normalized duration is outside the supported range", ErrInvalidMessage)
	}
	return nil
}

func directDurationOptions(spec DurationFormatSpec) (durationformat.Options, error) {
	if !spec.Style.Valid() {
		return durationformat.Options{}, fmt.Errorf("%w: invalid duration format", ErrInvalidMessage)
	}
	options := durationformat.Options{}
	style := spec.Style.String()
	options.Style = &style
	if spec.NumberingSystem != "" {
		options.NumberingSystem = &spec.NumberingSystem
	}
	if !spec.FractionalDigits.Valid() {
		return durationformat.Options{}, fmt.Errorf("%w: invalid duration fractional digits", ErrInvalidMessage)
	}
	if fractionalDigits, set := spec.FractionalDigits.value(); set {
		options.FractionalDigits = &fractionalDigits
	}
	units := []struct {
		spec    DurationUnitSpec
		style   **string
		display **string
	}{
		{spec.Years, &options.Years, &options.YearsDisplay},
		{spec.Months, &options.Months, &options.MonthsDisplay},
		{spec.Weeks, &options.Weeks, &options.WeeksDisplay},
		{spec.Days, &options.Days, &options.DaysDisplay},
		{spec.Hours, &options.Hours, &options.HoursDisplay},
		{spec.Minutes, &options.Minutes, &options.MinutesDisplay},
		{spec.Seconds, &options.Seconds, &options.SecondsDisplay},
		{spec.Milliseconds, &options.Milliseconds, &options.MillisecondsDisplay},
		{spec.Microseconds, &options.Microseconds, &options.MicrosecondsDisplay},
		{spec.Nanoseconds, &options.Nanoseconds, &options.NanosecondsDisplay},
	}
	for index, unit := range units {
		if !unit.spec.Style.Valid() || !unit.spec.Display.Valid() {
			return durationformat.Options{}, fmt.Errorf("%w: invalid duration unit format", ErrInvalidMessage)
		}
		if unit.spec.Style != DurationUnitDefault {
			if index < 4 && (unit.spec.Style == DurationUnitNumeric || unit.spec.Style == DurationUnitTwoDigit) || index > 6 && unit.spec.Style == DurationUnitTwoDigit {
				return durationformat.Options{}, fmt.Errorf("%w: numeric duration style is invalid for this unit", ErrInvalidMessage)
			}
			value := unit.spec.Style.String()
			*unit.style = &value
		}
		if unit.spec.Display != DurationDisplayDefault {
			value := unit.spec.Display.String()
			*unit.display = &value
		}
	}
	return options, nil
}

func validateNamedGeneralFormats(state directFormatContext, formats namedFormats) error {
	locales, err := locale.ParseList(state.locale)
	if err != nil {
		return fmt.Errorf("%w: named formats: %v", ErrInvalidLocale, err)
	}
	for _, name := range sortedMapKeys(formats.lists) {
		spec := formats.lists[name]
		if !spec.Type.Valid() || !spec.Width.Valid() {
			return fmt.Errorf("%w: named list format %q is invalid", ErrInvalidMessage, name)
		}
		typeName := spec.Type.String()
		options := listformat.Options{Type: &typeName}
		if width := directWidth(spec.Width); width != "" {
			options.Style = &width
		}
		formatter, formatErr := listformat.New(locales, options)
		if formatErr != nil {
			return fmt.Errorf("%w: named list format %q: %v", ErrInvalidMessage, name, formatErr)
		}
		if err := requireResolvedLocale(state.locale, formatter.ResolvedOptions().Locale); err != nil {
			return fmt.Errorf("%w: named list format %q: %v", ErrInvalidLocale, name, err)
		}
	}
	for _, name := range sortedMapKeys(formats.relatives) {
		spec := formats.relatives[name]
		if !spec.Width.Valid() || !spec.Numeric.Valid() {
			return fmt.Errorf("%w: named relative format %q is invalid", ErrInvalidMessage, name)
		}
		if err := validateDirectOptionBounds(map[string]any{"numberingSystem": spec.NumberingSystem}, state.limits); err != nil {
			return fmt.Errorf("named relative format %q: %w", name, err)
		}
		options := relativetimeformat.Options{}
		if width := directWidth(spec.Width); width != "" {
			options.Style = &width
		}
		if spec.Numeric == RelativeNumericAuto {
			numeric := spec.Numeric.String()
			options.Numeric = &numeric
		}
		if spec.NumberingSystem != "" {
			options.NumberingSystem = &spec.NumberingSystem
		}
		formatter, formatErr := relativetimeformat.New(locales, options)
		if formatErr != nil {
			return fmt.Errorf("%w: named relative format %q: %v", ErrInvalidMessage, name, formatErr)
		}
		resolved := formatter.ResolvedOptions()
		if err := validateDirectNumberLocale(state.locale, resolved.Locale, resolved.NumberingSystem, spec.NumberingSystem); err != nil {
			return fmt.Errorf("%w: named relative format %q: %v", ErrInvalidLocale, name, err)
		}
	}
	for _, name := range sortedMapKeys(formats.durations) {
		spec := formats.durations[name]
		options, optionsErr := directDurationOptions(spec)
		if optionsErr != nil {
			return fmt.Errorf("named duration format %q: %w", name, optionsErr)
		}
		if err := validateDirectOptionBounds(map[string]any{"numberingSystem": spec.NumberingSystem}, state.limits); err != nil {
			return fmt.Errorf("named duration format %q: %w", name, err)
		}
		formatter, formatErr := durationformat.New(locales, options)
		if formatErr != nil {
			return fmt.Errorf("%w: named duration format %q: %v", ErrInvalidMessage, name, formatErr)
		}
		resolved := formatter.ResolvedOptions()
		if err := validateDirectNumberLocale(state.locale, resolved.Locale, resolved.NumberingSystem, spec.NumberingSystem); err != nil {
			return fmt.Errorf("%w: named duration format %q: %v", ErrInvalidLocale, name, err)
		}
	}
	for _, name := range sortedMapKeys(formats.displayNames) {
		spec := formats.displayNames[name]
		if !spec.Type.Valid() || !spec.Width.Valid() || !spec.Fallback.Valid() || !spec.LanguageDisplay.Valid() {
			return fmt.Errorf("%w: named display-name format %q is invalid", ErrInvalidMessage, name)
		}
		typeName := spec.Type.String()
		options := displaynames.Options{Type: &typeName}
		if width := directWidth(spec.Width); width != "" {
			options.Style = &width
		}
		if spec.Fallback == DisplayNameNone {
			fallback := spec.Fallback.String()
			options.Fallback = &fallback
		}
		if spec.LanguageDisplay == LanguageStandard {
			languageDisplay := spec.LanguageDisplay.String()
			options.LanguageDisplay = &languageDisplay
		}
		formatter, formatErr := displaynames.New(locales, options)
		if formatErr != nil {
			return fmt.Errorf("%w: named display-name format %q: %v", ErrInvalidMessage, name, formatErr)
		}
		if err := requireResolvedLocale(state.locale, formatter.ResolvedOptions().Locale); err != nil {
			return fmt.Errorf("%w: named display-name format %q: %v", ErrInvalidLocale, name, err)
		}
	}
	return nil
}

func (s directFormatContext) finishDirectFormat(ctx context.Context, localeName string, source []directFormatPart) (FormattedValue, error) {
	parts := make([]Part, 0, min(s.limits.MaxOutputParts, len(source)*3+2))
	text := make([]byte, 0)
	appendPart := func(part Part) error {
		if len(parts) >= s.limits.MaxOutputParts {
			return fmt.Errorf("%w: output has too many parts", ErrLimitExceeded)
		}
		if len(part.Text) > s.limits.MaxOutputBytes-len(text) {
			return fmt.Errorf("%w: output exceeds %d bytes", ErrLimitExceeded, s.limits.MaxOutputBytes)
		}
		text = append(text, part.Text...)
		parts = append(parts, part)
		return nil
	}
	isolated := s.presentation != PresentationNoIsolation && len(source) != 0
	if isolated {
		start := directIsolationStart(localeName)
		if err := appendPart(Part{Kind: PartBidiIsolation, Type: "bidiIsolation", Text: string(start), Locale: localeName, Direction: string(bidi.DirAuto)}); err != nil {
			return FormattedValue{}, err
		}
	}
	for index, sourcePart := range source {
		if index&31 == 0 {
			if err := ctx.Err(); err != nil {
				return FormattedValue{}, err
			}
		}
		if !sourcePart.kind.Valid() || !validPartType(sourcePart.typeName) {
			return FormattedValue{}, fmt.Errorf("%w: formatter returned an invalid part type", ErrInvalidMessage)
		}
		if sourcePart.isolate && s.presentation != PresentationNoIsolation {
			if err := appendPart(Part{Kind: PartBidiIsolation, Type: "bidiIsolation", Text: string(bidi.FSI), Locale: localeName, Direction: string(bidi.DirAuto)}); err != nil {
				return FormattedValue{}, err
			}
		}
		if err := appendPart(Part{Kind: sourcePart.kind, Type: sourcePart.typeName, Text: sourcePart.text, Name: sourcePart.name, Locale: localeName, Direction: string(sourcePart.direction)}); err != nil {
			return FormattedValue{}, err
		}
		if sourcePart.isolate && s.presentation != PresentationNoIsolation {
			if err := appendPart(Part{Kind: PartBidiIsolation, Type: "bidiIsolation", Text: string(bidi.PDI), Locale: localeName, Direction: string(bidi.DirAuto)}); err != nil {
				return FormattedValue{}, err
			}
		}
	}
	if isolated {
		if err := appendPart(Part{Kind: PartBidiIsolation, Type: "bidiIsolation", Text: string(bidi.PDI), Locale: localeName, Direction: string(bidi.DirAuto)}); err != nil {
			return FormattedValue{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return FormattedValue{}, err
	}
	return FormattedValue{Text: string(text), Parts: parts, Locale: localeName}, nil
}

func (s directFormatContext) finishDirectRange(ctx context.Context, localeName string, source []directRangePart) (FormattedRange, error) {
	parts := make([]RangePart, 0, min(s.limits.MaxOutputParts, len(source)+2))
	text := make([]byte, 0)
	appendPart := func(part RangePart) error {
		if len(parts) >= s.limits.MaxOutputParts {
			return fmt.Errorf("%w: output has too many parts", ErrLimitExceeded)
		}
		if len(part.Text) > s.limits.MaxOutputBytes-len(text) {
			return fmt.Errorf("%w: output exceeds %d bytes", ErrLimitExceeded, s.limits.MaxOutputBytes)
		}
		text = append(text, part.Text...)
		parts = append(parts, part)
		return nil
	}
	isolated := s.presentation != PresentationNoIsolation && len(source) != 0
	if isolated {
		if err := appendPart(RangePart{Kind: PartBidiIsolation, Type: "bidiIsolation", Text: string(directIsolationStart(localeName)), Locale: localeName, Direction: string(bidi.DirAuto), Source: RangeSourceShared}); err != nil {
			return FormattedRange{}, err
		}
	}
	for index, sourcePart := range source {
		if index&31 == 0 {
			if err := ctx.Err(); err != nil {
				return FormattedRange{}, err
			}
		}
		if !sourcePart.kind.Valid() || !validPartType(sourcePart.typeName) || !sourcePart.source.Valid() {
			return FormattedRange{}, fmt.Errorf("%w: formatter returned an invalid range part", ErrInvalidMessage)
		}
		if err := appendPart(RangePart{Kind: sourcePart.kind, Type: sourcePart.typeName, Text: sourcePart.text, Locale: localeName, Direction: string(sourcePart.direction), Source: sourcePart.source}); err != nil {
			return FormattedRange{}, err
		}
	}
	if isolated {
		if err := appendPart(RangePart{Kind: PartBidiIsolation, Type: "bidiIsolation", Text: string(bidi.PDI), Locale: localeName, Direction: string(bidi.DirAuto), Source: RangeSourceShared}); err != nil {
			return FormattedRange{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return FormattedRange{}, err
	}
	return FormattedRange{Text: string(text), Parts: parts, Locale: localeName}, nil
}

func (s directFormatContext) requireDirectOutput(maximumBytes, maximumParts int) error {
	_, err := s.directExactBudget(maximumBytes, maximumParts)
	return err
}

func (s directFormatContext) directExactBudget(maximumBytes, maximumParts int) (*renderBudget, error) {
	isolationBytes := 0
	isolationParts := 0
	if s.presentation != PresentationNoIsolation {
		isolationBytes = len(string(bidi.FSI)) + len(string(bidi.PDI))
		isolationParts = 2
	}
	if maximumBytes < 0 || maximumParts < 0 || isolationBytes > s.limits.MaxOutputBytes || maximumBytes > s.limits.MaxOutputBytes-isolationBytes || isolationParts > s.limits.MaxOutputParts || maximumParts > s.limits.MaxOutputParts-isolationParts {
		return nil, ErrLimitExceeded
	}
	return &renderBudget{bytes: s.limits.MaxOutputBytes - isolationBytes, parts: s.limits.MaxOutputParts - isolationParts}, nil
}

func (s directFormatContext) finishDirectExact(ctx context.Context, value messagevalue.MessageValue) (FormattedValue, error) {
	source, err := value.ToParts()
	if err != nil {
		return FormattedValue{}, err
	}
	if len(source) != 1 || source[0] == nil {
		return FormattedValue{}, fmt.Errorf("%w: exact formatter returned invalid parts", ErrInvalidMessage)
	}
	part := source[0]
	partType := part.Type()
	if !validPartType(partType) {
		return FormattedValue{}, fmt.Errorf("%w: formatter returned an invalid part type", ErrInvalidMessage)
	}
	text, ok := part.Value().(string)
	if !ok {
		return FormattedValue{}, fmt.Errorf("%w: exact formatter returned a non-text value", ErrInvalidMessage)
	}
	subparts, err := directConvertSubparts(ctx, s.limits, part, text)
	if err != nil {
		return FormattedValue{}, err
	}
	localeName := part.Locale()
	direction := part.Dir()
	parts := make([]Part, 0, 3)
	formatted := make([]byte, 0, len(text)+8)
	if s.presentation != PresentationNoIsolation {
		start := bidi.FSI
		switch direction {
		case bidi.DirLTR:
			start = bidi.LRI
		case bidi.DirRTL:
			start = bidi.RLI
		}
		formatted = append(formatted, string(start)...)
		parts = append(parts, Part{Kind: PartBidiIsolation, Type: "bidiIsolation", Text: string(start), Locale: localeName, Direction: string(bidi.DirAuto)})
	}
	formatted = append(formatted, text...)
	parts = append(parts, Part{Kind: PartValue, Type: partType, Text: text, Locale: localeName, Direction: string(direction), Subparts: subparts})
	if s.presentation != PresentationNoIsolation {
		formatted = append(formatted, string(bidi.PDI)...)
		parts = append(parts, Part{Kind: PartBidiIsolation, Type: "bidiIsolation", Text: string(bidi.PDI), Locale: localeName, Direction: string(bidi.DirAuto)})
	}
	if err := ctx.Err(); err != nil {
		return FormattedValue{}, err
	}
	return FormattedValue{Text: string(formatted), Parts: parts, Locale: localeName}, nil
}

func directConvertSubparts(ctx context.Context, limits Limits, sourcePart messagevalue.MessagePart, value string) ([]Subpart, error) {
	nested, ok := sourcePart.(interface {
		Parts() []messagevalue.MessagePart
	})
	if !ok {
		return nil, nil
	}
	source := nested.Parts()
	if source == nil {
		return nil, nil
	}
	if len(source) > limits.MaxOutputParts {
		return nil, fmt.Errorf("%w: output has too many subparts", ErrLimitExceeded)
	}
	converted := make([]Subpart, len(source))
	joined := make([]byte, 0, len(value))
	for index, part := range source {
		if index&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if part == nil || !validPartType(part.Type()) {
			return nil, fmt.Errorf("%w: formatter returned an invalid subpart", ErrInvalidMessage)
		}
		text, ok := part.Value().(string)
		if !ok || len(text) > len(value)-len(joined) {
			return nil, fmt.Errorf("%w: formatter subparts do not match their value", ErrInvalidMessage)
		}
		joined = append(joined, text...)
		converted[index] = Subpart{Type: part.Type(), Text: text}
	}
	if string(joined) != value {
		return nil, fmt.Errorf("%w: formatter subparts do not match their value", ErrInvalidMessage)
	}
	return converted, nil
}

func directIsolationStart(localeName string) rune {
	if bidi.GetLocaleDirection(localeName) == bidi.DirRTL {
		return bidi.RLI
	}
	return bidi.LRI
}

func saturatedDirectAdd(left, right int) int {
	maximum := int(^uint(0) >> 1)
	if left < 0 || right < 0 || left > maximum-right {
		return maximum
	}
	return left + right
}

func saturatedDirectMultiply(left, right int) int {
	maximum := int(^uint(0) >> 1)
	if left < 0 || right < 0 || left != 0 && right > maximum/left {
		return maximum
	}
	return left * right
}
