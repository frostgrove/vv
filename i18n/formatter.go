package i18n

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrFormatNotFound = errors.New("i18n: format not found")

type FormatterSpec struct {
	Locale              string
	TimeZone            string
	TimeZoneDataVersion string
	Capabilities        []Capability
	Presentation        Presentation
	Limits              Limits
	Observer            Observer
	Formats             Formats
}

type NamedNumberFormat struct {
	Name string
	Spec NumberFormatSpec
}

type NamedMoneyFormat struct {
	Name string
	Spec MoneyFormatSpec
}

type NamedPercentFormat struct {
	Name string
	Spec PercentFormatSpec
}

type NamedUnitFormat struct {
	Name string
	Spec UnitFormatSpec
}

type NamedPluralFormat struct {
	Name string
	Spec PluralFormatSpec
}

type NamedDateFormat struct {
	Name string
	Spec DateFormatSpec
}

type NamedTimeFormat struct {
	Name string
	Spec TimeFormatSpec
}

type NamedDateTimeFormat struct {
	Name string
	Spec DateTimeFormatSpec
}

type NamedListFormat struct {
	Name string
	Spec ListFormatSpec
}

type NamedRelativeFormat struct {
	Name string
	Spec RelativeFormatSpec
}

type NamedDurationFormat struct {
	Name string
	Spec DurationFormatSpec
}

type NamedDisplayNameFormat struct {
	Name string
	Spec DisplayNameSpec
}

type Formats struct {
	Numbers      []NamedNumberFormat
	Money        []NamedMoneyFormat
	Percents     []NamedPercentFormat
	Units        []NamedUnitFormat
	Plurals      []NamedPluralFormat
	Dates        []NamedDateFormat
	Times        []NamedTimeFormat
	DateTimes    []NamedDateTimeFormat
	Lists        []NamedListFormat
	Relatives    []NamedRelativeFormat
	Durations    []NamedDurationFormat
	DisplayNames []NamedDisplayNameFormat
}

type namedFormats struct {
	numbers      map[string]NumberFormatSpec
	money        map[string]MoneyFormatSpec
	percents     map[string]PercentFormatSpec
	units        map[string]UnitFormatSpec
	plurals      map[string]PluralFormatSpec
	dates        map[string]DateFormatSpec
	times        map[string]TimeFormatSpec
	dateTimes    map[string]DateTimeFormatSpec
	lists        map[string]ListFormatSpec
	relatives    map[string]RelativeFormatSpec
	durations    map[string]DurationFormatSpec
	displayNames map[string]DisplayNameSpec
}

type directFormatContext struct {
	locale       string
	timeZone     string
	presentation Presentation
	limits       Limits
	capabilities map[Capability]bool
	observer     Observer
}

type directFormatTarget interface {
	directFormatting() (directFormatContext, error)
}

type Formatter struct {
	context directFormatContext
	formats namedFormats
}

const formatterCapabilityCount = int(CapabilityUnit-CapabilityDateTime) + 1

func NewFormatter(spec FormatterSpec) (*Formatter, error) {
	limits, err := checkedLimits(spec.Limits)
	if err != nil {
		return nil, err
	}
	if len(spec.Capabilities) > formatterCapabilityCount {
		return nil, fmt.Errorf("%w: formatter capabilities exceed %d entries", ErrLimitExceeded, formatterCapabilityCount)
	}
	if spec.Locale == "" {
		return nil, fmt.Errorf("%w: formatter locale is required", ErrInvalidLocale)
	}
	_, localeName, err := canonicalLocale(spec.Locale, limits.Locale.MaxTagBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: formatter locale: %v", ErrInvalidLocale, err)
	}
	timeZone := spec.TimeZone
	if timeZone == "" {
		timeZone = "UTC"
	}
	timeZone, err = canonicalTimeZone(timeZone)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if timeZone != "UTC" && spec.TimeZoneDataVersion == "" {
		return nil, fmt.Errorf("%w: non-UTC formatting requires a time-zone data version", ErrInvalidMessage)
	}
	if !spec.Presentation.Valid() {
		return nil, fmt.Errorf("%w: presentation is invalid", ErrInvalidMessage)
	}
	if len(spec.TimeZoneDataVersion) > limits.MaxRevisionBytes || !utf8.ValidString(spec.TimeZoneDataVersion) || hasUnsafeBidiControls(spec.TimeZoneDataVersion) {
		return nil, fmt.Errorf("%w: time-zone data version is invalid or too large", ErrInvalidMessage)
	}
	capabilities := make(map[Capability]bool, len(spec.Capabilities))
	for _, capability := range spec.Capabilities {
		if !capability.Valid() || capabilities[capability] {
			return nil, fmt.Errorf("%w: formatter capability %s is invalid or repeated", ErrInvalidMessage, capability)
		}
		capabilities[capability] = true
	}
	state := directFormatContext{
		locale:       localeName,
		timeZone:     timeZone,
		presentation: spec.Presentation,
		limits:       limits,
		capabilities: capabilities,
		observer:     spec.Observer,
	}
	formats, err := compileNamedFormats(state, spec.Formats)
	if err != nil {
		return nil, err
	}
	return &Formatter{context: state, formats: formats}, nil
}

func (v *View) Formatter(formats Formats) (*Formatter, error) {
	state, err := v.directFormatting()
	if err != nil {
		return nil, err
	}
	compiled, err := compileNamedFormats(state, formats)
	if err != nil {
		return nil, err
	}
	return &Formatter{context: state, formats: compiled}, nil
}

func (v *View) directFormatting() (directFormatContext, error) {
	if v == nil || v.snapshot == nil {
		return directFormatContext{}, fmt.Errorf("%w: view is nil", ErrInvalidMessage)
	}
	return directFormatContext{
		locale:       v.formattingLocale,
		timeZone:     v.timeZone,
		presentation: v.presentation,
		limits:       v.snapshot.limits,
		capabilities: v.snapshot.capabilities,
		observer:     v.snapshot.observer,
	}, nil
}

func (f *Formatter) directFormatting() (directFormatContext, error) {
	if f == nil {
		return directFormatContext{}, fmt.Errorf("%w: formatter is nil", ErrInvalidMessage)
	}
	return f.context, nil
}

func (f *Formatter) namedFormats() namedFormats {
	if f == nil {
		return namedFormats{}
	}
	return f.formats
}

func (f *Formatter) FormatNumber(ctx context.Context, value NumericValue, spec NumberFormatSpec) (FormattedValue, error) {
	return directFormatNumber(ctx, f, value, spec)
}

func (f *Formatter) FormatMoney(ctx context.Context, value MoneyValue, spec MoneyFormatSpec) (FormattedValue, error) {
	return directFormatMoney(ctx, f, value, spec)
}

func (f *Formatter) FormatPercent(ctx context.Context, value NumericValue, spec PercentFormatSpec) (FormattedValue, error) {
	return directFormatPercent(ctx, f, value, spec)
}

func (f *Formatter) FormatUnit(ctx context.Context, value NumericValue, spec UnitFormatSpec) (FormattedValue, error) {
	return directFormatUnit(ctx, f, value, spec)
}

func (f *Formatter) FormatNumberRange(ctx context.Context, start, end NumericValue, spec NumberFormatSpec) (FormattedRange, error) {
	return directFormatNumberRange(ctx, f, start, end, spec)
}

func (f *Formatter) FormatMoneyRange(ctx context.Context, start, end MoneyValue, spec MoneyFormatSpec) (FormattedRange, error) {
	return directFormatMoneyRange(ctx, f, start, end, spec)
}

func (f *Formatter) FormatPercentRange(ctx context.Context, start, end NumericValue, spec PercentFormatSpec) (FormattedRange, error) {
	return directFormatPercentRange(ctx, f, start, end, spec)
}

func (f *Formatter) FormatUnitRange(ctx context.Context, start, end NumericValue, spec UnitFormatSpec) (FormattedRange, error) {
	return directFormatUnitRange(ctx, f, start, end, spec)
}

func (f *Formatter) SelectPlural(ctx context.Context, value NumericValue, spec PluralFormatSpec) (PluralCategory, error) {
	return directSelectPlural(ctx, f, value, spec)
}

func (f *Formatter) SelectPluralRange(ctx context.Context, start, end NumericValue, spec PluralFormatSpec) (PluralCategory, error) {
	return directSelectPluralRange(ctx, f, start, end, spec)
}

func (f *Formatter) FormatDate(ctx context.Context, value DateValue, spec DateFormatSpec) (FormattedValue, error) {
	return directFormatDate(ctx, f, value, spec)
}

func (f *Formatter) FormatInstantDate(ctx context.Context, value time.Time, spec DateFormatSpec) (FormattedValue, error) {
	return directFormatInstantDate(ctx, f, value, spec)
}

func (f *Formatter) FormatTime(ctx context.Context, value time.Time, spec TimeFormatSpec) (FormattedValue, error) {
	return directFormatTime(ctx, f, value, spec)
}

func (f *Formatter) FormatDateTime(ctx context.Context, value time.Time, spec DateTimeFormatSpec) (FormattedValue, error) {
	return directFormatDateTime(ctx, f, value, spec)
}

func (f *Formatter) FormatDateRange(ctx context.Context, start, end DateValue, spec DateFormatSpec) (FormattedRange, error) {
	return directFormatDateRange(ctx, f, start, end, spec)
}

func (f *Formatter) FormatInstantDateRange(ctx context.Context, start, end time.Time, spec DateFormatSpec) (FormattedRange, error) {
	return directFormatInstantDateRange(ctx, f, start, end, spec)
}

func (f *Formatter) FormatTimeRange(ctx context.Context, start, end time.Time, spec TimeFormatSpec) (FormattedRange, error) {
	return directFormatTimeRange(ctx, f, start, end, spec)
}

func (f *Formatter) FormatDateTimeRange(ctx context.Context, start, end time.Time, spec DateTimeFormatSpec) (FormattedRange, error) {
	return directFormatDateTimeRange(ctx, f, start, end, spec)
}

func (f *Formatter) FormatList(ctx context.Context, values []string, spec ListFormatSpec) (FormattedValue, error) {
	return formatList(ctx, f, values, spec)
}

func (f *Formatter) FormatRelative(ctx context.Context, value int64, unit RelativeUnit, spec RelativeFormatSpec) (FormattedValue, error) {
	return formatRelative(ctx, f, value, unit, spec)
}

func (f *Formatter) FormatDuration(ctx context.Context, value DurationValue, spec DurationFormatSpec) (FormattedValue, error) {
	return formatDuration(ctx, f, value, spec)
}

func (f *Formatter) FormatDisplayName(ctx context.Context, code string, spec DisplayNameSpec) (FormattedValue, bool, error) {
	return formatDisplayName(ctx, f, code, spec)
}

func (f *Formatter) FormatNamedNumber(ctx context.Context, name string, value NumericValue) (FormattedValue, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().numbers)
	if err != nil {
		return FormattedValue{}, err
	}
	return f.FormatNumber(ctx, value, spec)
}

func (f *Formatter) FormatNamedNumberRange(ctx context.Context, name string, start, end NumericValue) (FormattedRange, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().numbers)
	if err != nil {
		return FormattedRange{}, err
	}
	return f.FormatNumberRange(ctx, start, end, spec)
}

func (f *Formatter) FormatNamedMoney(ctx context.Context, name string, value MoneyValue) (FormattedValue, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().money)
	if err != nil {
		return FormattedValue{}, err
	}
	return f.FormatMoney(ctx, value, spec)
}

func (f *Formatter) FormatNamedMoneyRange(ctx context.Context, name string, start, end MoneyValue) (FormattedRange, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().money)
	if err != nil {
		return FormattedRange{}, err
	}
	return f.FormatMoneyRange(ctx, start, end, spec)
}

func (f *Formatter) FormatNamedPercent(ctx context.Context, name string, value NumericValue) (FormattedValue, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().percents)
	if err != nil {
		return FormattedValue{}, err
	}
	return f.FormatPercent(ctx, value, spec)
}

func (f *Formatter) FormatNamedPercentRange(ctx context.Context, name string, start, end NumericValue) (FormattedRange, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().percents)
	if err != nil {
		return FormattedRange{}, err
	}
	return f.FormatPercentRange(ctx, start, end, spec)
}

func (f *Formatter) FormatNamedUnit(ctx context.Context, name string, value NumericValue) (FormattedValue, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().units)
	if err != nil {
		return FormattedValue{}, err
	}
	return f.FormatUnit(ctx, value, spec)
}

func (f *Formatter) FormatNamedUnitRange(ctx context.Context, name string, start, end NumericValue) (FormattedRange, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().units)
	if err != nil {
		return FormattedRange{}, err
	}
	return f.FormatUnitRange(ctx, start, end, spec)
}

func (f *Formatter) SelectNamedPlural(ctx context.Context, name string, value NumericValue) (PluralCategory, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().plurals)
	if err != nil {
		return 0, err
	}
	return f.SelectPlural(ctx, value, spec)
}

func (f *Formatter) SelectNamedPluralRange(ctx context.Context, name string, start, end NumericValue) (PluralCategory, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().plurals)
	if err != nil {
		return 0, err
	}
	return f.SelectPluralRange(ctx, start, end, spec)
}

func (f *Formatter) FormatNamedDate(ctx context.Context, name string, value DateValue) (FormattedValue, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().dates)
	if err != nil {
		return FormattedValue{}, err
	}
	return f.FormatDate(ctx, value, spec)
}

func (f *Formatter) FormatNamedDateRange(ctx context.Context, name string, start, end DateValue) (FormattedRange, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().dates)
	if err != nil {
		return FormattedRange{}, err
	}
	return f.FormatDateRange(ctx, start, end, spec)
}

func (f *Formatter) FormatNamedInstantDate(ctx context.Context, name string, value time.Time) (FormattedValue, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().dates)
	if err != nil {
		return FormattedValue{}, err
	}
	return f.FormatInstantDate(ctx, value, spec)
}

func (f *Formatter) FormatNamedInstantDateRange(ctx context.Context, name string, start, end time.Time) (FormattedRange, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().dates)
	if err != nil {
		return FormattedRange{}, err
	}
	return f.FormatInstantDateRange(ctx, start, end, spec)
}

func (f *Formatter) FormatNamedTime(ctx context.Context, name string, value time.Time) (FormattedValue, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().times)
	if err != nil {
		return FormattedValue{}, err
	}
	return f.FormatTime(ctx, value, spec)
}

func (f *Formatter) FormatNamedTimeRange(ctx context.Context, name string, start, end time.Time) (FormattedRange, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().times)
	if err != nil {
		return FormattedRange{}, err
	}
	return f.FormatTimeRange(ctx, start, end, spec)
}

func (f *Formatter) FormatNamedDateTime(ctx context.Context, name string, value time.Time) (FormattedValue, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().dateTimes)
	if err != nil {
		return FormattedValue{}, err
	}
	return f.FormatDateTime(ctx, value, spec)
}

func (f *Formatter) FormatNamedDateTimeRange(ctx context.Context, name string, start, end time.Time) (FormattedRange, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().dateTimes)
	if err != nil {
		return FormattedRange{}, err
	}
	return f.FormatDateTimeRange(ctx, start, end, spec)
}

func (f *Formatter) FormatNamedList(ctx context.Context, name string, values []string) (FormattedValue, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().lists)
	if err != nil {
		return FormattedValue{}, err
	}
	return f.FormatList(ctx, values, spec)
}

func (f *Formatter) FormatNamedRelative(ctx context.Context, name string, value int64, unit RelativeUnit) (FormattedValue, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().relatives)
	if err != nil {
		return FormattedValue{}, err
	}
	return f.FormatRelative(ctx, value, unit, spec)
}

func (f *Formatter) FormatNamedDuration(ctx context.Context, name string, value DurationValue) (FormattedValue, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().durations)
	if err != nil {
		return FormattedValue{}, err
	}
	return f.FormatDuration(ctx, value, spec)
}

func (f *Formatter) FormatNamedDisplayName(ctx context.Context, name, code string) (FormattedValue, bool, error) {
	spec, err := namedSpec(ctx, name, f, f.namedFormats().displayNames)
	if err != nil {
		return FormattedValue{}, false, err
	}
	return f.FormatDisplayName(ctx, code, spec)
}

func namedSpec[T any](ctx context.Context, name string, formatter *Formatter, values map[string]T) (value T, err error) {
	if formatter == nil {
		return value, fmt.Errorf("%w: formatter is nil", ErrInvalidMessage)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now()
	defer func() {
		if err == nil {
			return
		}
		outcome, reason := terminalOperationResult(err)
		notifyObserver(ctx, formatter.context.observer, Observation{Operation: OperationRender, Outcome: outcome, Reason: reason, Duration: time.Since(started), Count: 1})
	}()
	if err = ctx.Err(); err != nil {
		return value, err
	}
	if len(name) > formatter.context.limits.MaxIdentifierBytes {
		return value, fmt.Errorf("%w: named format name exceeds %d bytes", ErrLimitExceeded, formatter.context.limits.MaxIdentifierBytes)
	}
	if !validModuleName(name, formatter.context.limits.MaxIdentifierBytes) {
		return value, fmt.Errorf("%w: named format name is invalid", ErrInvalidMessage)
	}
	value, ok := values[name]
	if !ok {
		return value, fmt.Errorf("%w: %q", ErrFormatNotFound, name)
	}
	return value, nil
}

func compileNamedFormats(state directFormatContext, formats Formats) (namedFormats, error) {
	total := 0
	for _, count := range []int{len(formats.Numbers), len(formats.Money), len(formats.Percents), len(formats.Units), len(formats.Plurals), len(formats.Dates), len(formats.Times), len(formats.DateTimes), len(formats.Lists), len(formats.Relatives), len(formats.Durations), len(formats.DisplayNames)} {
		total = saturatedDirectAdd(total, count)
	}
	if total > state.limits.MaxCatalogItems {
		return namedFormats{}, fmt.Errorf("%w: named formats exceed %d entries", ErrLimitExceeded, state.limits.MaxCatalogItems)
	}
	if err := preflightNamedFormatMaterial(formats, state.limits.MaxCatalogBytes); err != nil {
		return namedFormats{}, err
	}
	compiled := namedFormats{}
	var err error
	compiled.numbers, err = compileNamed(formatNames(formats.Numbers), formatNumberSpecs(formats.Numbers), state.limits)
	if err != nil {
		return namedFormats{}, err
	}
	compiled.money, err = compileNamed(formatNames(formats.Money), formatMoneySpecs(formats.Money), state.limits)
	if err != nil {
		return namedFormats{}, err
	}
	compiled.percents, err = compileNamed(formatNames(formats.Percents), formatPercentSpecs(formats.Percents), state.limits)
	if err != nil {
		return namedFormats{}, err
	}
	compiled.units, err = compileNamed(formatNames(formats.Units), formatUnitSpecs(formats.Units), state.limits)
	if err != nil {
		return namedFormats{}, err
	}
	compiled.plurals, err = compileNamed(formatNames(formats.Plurals), formatPluralSpecs(formats.Plurals), state.limits)
	if err != nil {
		return namedFormats{}, err
	}
	compiled.dates, err = compileNamed(formatNames(formats.Dates), formatDateSpecs(formats.Dates), state.limits)
	if err != nil {
		return namedFormats{}, err
	}
	compiled.times, err = compileNamed(formatNames(formats.Times), formatTimeSpecs(formats.Times), state.limits)
	if err != nil {
		return namedFormats{}, err
	}
	compiled.dateTimes, err = compileNamed(formatNames(formats.DateTimes), formatDateTimeSpecs(formats.DateTimes), state.limits)
	if err != nil {
		return namedFormats{}, err
	}
	compiled.lists, err = compileNamed(formatNames(formats.Lists), formatListSpecs(formats.Lists), state.limits)
	if err != nil {
		return namedFormats{}, err
	}
	compiled.relatives, err = compileNamed(formatNames(formats.Relatives), formatRelativeSpecs(formats.Relatives), state.limits)
	if err != nil {
		return namedFormats{}, err
	}
	compiled.durations, err = compileNamed(formatNames(formats.Durations), formatDurationSpecs(formats.Durations), state.limits)
	if err != nil {
		return namedFormats{}, err
	}
	compiled.displayNames, err = compileNamed(formatNames(formats.DisplayNames), formatDisplayNameSpecs(formats.DisplayNames), state.limits)
	if err != nil {
		return namedFormats{}, err
	}
	if err := validateNamedFormats(state, compiled); err != nil {
		return namedFormats{}, err
	}
	return compiled, nil
}

func compileNamed[T any](names []string, specs []T, limits Limits) (map[string]T, error) {
	values := make(map[string]T, len(names))
	for index, name := range names {
		if len(name) > limits.MaxIdentifierBytes {
			return nil, fmt.Errorf("%w: named format name exceeds %d bytes", ErrLimitExceeded, limits.MaxIdentifierBytes)
		}
		if !validModuleName(name, limits.MaxIdentifierBytes) {
			return nil, fmt.Errorf("%w: named format name %q is invalid", ErrInvalidMessage, name)
		}
		if _, exists := values[name]; exists {
			return nil, fmt.Errorf("%w: named format %q is repeated", ErrInvalidMessage, name)
		}
		values[strings.Clone(name)] = specs[index]
	}
	return values, nil
}

func preflightNamedFormatMaterial(formats Formats, maximum int) error {
	total := 0
	add := func(values ...string) bool {
		for _, value := range values {
			if len(value) > maximum-total {
				return false
			}
			total += len(value)
		}
		return true
	}
	for _, value := range formats.Numbers {
		if !add(value.Name, value.Spec.NumberingSystem) {
			return namedFormatMaterialLimit(maximum)
		}
	}
	for _, value := range formats.Money {
		if !add(value.Name, value.Spec.Number.NumberingSystem) {
			return namedFormatMaterialLimit(maximum)
		}
	}
	for _, value := range formats.Percents {
		if !add(value.Name, value.Spec.Number.NumberingSystem) {
			return namedFormatMaterialLimit(maximum)
		}
	}
	for _, value := range formats.Units {
		if !add(value.Name, value.Spec.Number.NumberingSystem, value.Spec.Unit) {
			return namedFormatMaterialLimit(maximum)
		}
	}
	for _, value := range formats.Plurals {
		if !add(value.Name, value.Spec.Number.NumberingSystem) {
			return namedFormatMaterialLimit(maximum)
		}
	}
	for _, value := range formats.Dates {
		if !add(value.Name, value.Spec.Calendar, value.Spec.NumberingSystem) {
			return namedFormatMaterialLimit(maximum)
		}
	}
	for _, value := range formats.Times {
		if !add(value.Name, value.Spec.Calendar, value.Spec.NumberingSystem) {
			return namedFormatMaterialLimit(maximum)
		}
	}
	for _, value := range formats.DateTimes {
		if !add(value.Name, value.Spec.Calendar, value.Spec.NumberingSystem) {
			return namedFormatMaterialLimit(maximum)
		}
	}
	for _, value := range formats.Lists {
		if !add(value.Name) {
			return namedFormatMaterialLimit(maximum)
		}
	}
	for _, value := range formats.Relatives {
		if !add(value.Name, value.Spec.NumberingSystem) {
			return namedFormatMaterialLimit(maximum)
		}
	}
	for _, value := range formats.Durations {
		if !add(value.Name, value.Spec.NumberingSystem) {
			return namedFormatMaterialLimit(maximum)
		}
	}
	for _, value := range formats.DisplayNames {
		if !add(value.Name) {
			return namedFormatMaterialLimit(maximum)
		}
	}
	return nil
}

func namedFormatMaterialLimit(maximum int) error {
	return fmt.Errorf("%w: named format material exceeds %d bytes", ErrLimitExceeded, maximum)
}

func validateNamedFormats(state directFormatContext, formats namedFormats) error {
	if err := validateNamedNumericFormats(state, formats); err != nil {
		return err
	}
	if err := validateNamedDateFormats(state, formats); err != nil {
		return err
	}
	return validateNamedGeneralFormats(state, formats)
}

func formatNames[T interface{ formatName() string }](values []T) []string {
	names := make([]string, len(values))
	for index, value := range values {
		names[index] = value.formatName()
	}
	return names
}

func (v NamedNumberFormat) formatName() string      { return v.Name }
func (v NamedMoneyFormat) formatName() string       { return v.Name }
func (v NamedPercentFormat) formatName() string     { return v.Name }
func (v NamedUnitFormat) formatName() string        { return v.Name }
func (v NamedPluralFormat) formatName() string      { return v.Name }
func (v NamedDateFormat) formatName() string        { return v.Name }
func (v NamedTimeFormat) formatName() string        { return v.Name }
func (v NamedDateTimeFormat) formatName() string    { return v.Name }
func (v NamedListFormat) formatName() string        { return v.Name }
func (v NamedRelativeFormat) formatName() string    { return v.Name }
func (v NamedDurationFormat) formatName() string    { return v.Name }
func (v NamedDisplayNameFormat) formatName() string { return v.Name }

func formatNumberSpecs(values []NamedNumberFormat) []NumberFormatSpec {
	return collectSpecs(values, func(value NamedNumberFormat) NumberFormatSpec { return cloneNumberFormatSpec(value.Spec) })
}
func formatMoneySpecs(values []NamedMoneyFormat) []MoneyFormatSpec {
	return collectSpecs(values, func(value NamedMoneyFormat) MoneyFormatSpec {
		value.Spec.Number = cloneNumberFormatSpec(value.Spec.Number)
		return value.Spec
	})
}
func formatPercentSpecs(values []NamedPercentFormat) []PercentFormatSpec {
	return collectSpecs(values, func(value NamedPercentFormat) PercentFormatSpec {
		value.Spec.Number = cloneNumberFormatSpec(value.Spec.Number)
		return value.Spec
	})
}
func formatUnitSpecs(values []NamedUnitFormat) []UnitFormatSpec {
	return collectSpecs(values, func(value NamedUnitFormat) UnitFormatSpec {
		value.Spec.Number = cloneNumberFormatSpec(value.Spec.Number)
		value.Spec.Unit = strings.Clone(value.Spec.Unit)
		return value.Spec
	})
}
func formatPluralSpecs(values []NamedPluralFormat) []PluralFormatSpec {
	return collectSpecs(values, func(value NamedPluralFormat) PluralFormatSpec {
		value.Spec.Number = cloneNumberFormatSpec(value.Spec.Number)
		return value.Spec
	})
}
func formatDateSpecs(values []NamedDateFormat) []DateFormatSpec {
	return collectSpecs(values, func(value NamedDateFormat) DateFormatSpec {
		value.Spec.Calendar = strings.Clone(value.Spec.Calendar)
		value.Spec.NumberingSystem = strings.Clone(value.Spec.NumberingSystem)
		return value.Spec
	})
}
func formatTimeSpecs(values []NamedTimeFormat) []TimeFormatSpec {
	return collectSpecs(values, func(value NamedTimeFormat) TimeFormatSpec {
		value.Spec.Calendar = strings.Clone(value.Spec.Calendar)
		value.Spec.NumberingSystem = strings.Clone(value.Spec.NumberingSystem)
		return value.Spec
	})
}
func formatDateTimeSpecs(values []NamedDateTimeFormat) []DateTimeFormatSpec {
	return collectSpecs(values, func(value NamedDateTimeFormat) DateTimeFormatSpec {
		value.Spec.Calendar = strings.Clone(value.Spec.Calendar)
		value.Spec.NumberingSystem = strings.Clone(value.Spec.NumberingSystem)
		return value.Spec
	})
}
func formatListSpecs(values []NamedListFormat) []ListFormatSpec {
	return collectSpecs(values, func(value NamedListFormat) ListFormatSpec { return value.Spec })
}
func formatRelativeSpecs(values []NamedRelativeFormat) []RelativeFormatSpec {
	return collectSpecs(values, func(value NamedRelativeFormat) RelativeFormatSpec {
		value.Spec.NumberingSystem = strings.Clone(value.Spec.NumberingSystem)
		return value.Spec
	})
}
func formatDurationSpecs(values []NamedDurationFormat) []DurationFormatSpec {
	return collectSpecs(values, func(value NamedDurationFormat) DurationFormatSpec {
		value.Spec.NumberingSystem = strings.Clone(value.Spec.NumberingSystem)
		return value.Spec
	})
}
func formatDisplayNameSpecs(values []NamedDisplayNameFormat) []DisplayNameSpec {
	return collectSpecs(values, func(value NamedDisplayNameFormat) DisplayNameSpec { return value.Spec })
}

func collectSpecs[T, S any](values []T, spec func(T) S) []S {
	out := make([]S, len(values))
	for index, value := range values {
		out[index] = spec(value)
	}
	return out
}

func cloneNumberFormatSpec(spec NumberFormatSpec) NumberFormatSpec {
	spec.NumberingSystem = strings.Clone(spec.NumberingSystem)
	return spec
}
