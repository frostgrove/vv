package i18n

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/agentable/go-intl/datetimeformat"
	"github.com/agentable/go-intl/locale"
	"github.com/kaptinlin/messageformat-go/pkg/bidi"
	"github.com/kaptinlin/messageformat-go/pkg/functions"
	"github.com/kaptinlin/messageformat-go/pkg/messagevalue"
)

type DateTimeStyle uint8

const (
	DateTimeStyleDefault DateTimeStyle = iota
	DateTimeStyleFull
	DateTimeStyleLong
	DateTimeStyleMedium
	DateTimeStyleShort
)

func (s DateTimeStyle) String() string {
	switch s {
	case DateTimeStyleDefault:
		return "default"
	case DateTimeStyleFull:
		return "full"
	case DateTimeStyleLong:
		return "long"
	case DateTimeStyleMedium:
		return "medium"
	case DateTimeStyleShort:
		return "short"
	default:
		return "unknown"
	}
}

func (s DateTimeStyle) Valid() bool { return s <= DateTimeStyleShort }

type HourCycle uint8

const (
	HourCycleDefault HourCycle = iota
	HourCycleH11
	HourCycleH12
	HourCycleH23
	HourCycleH24
)

func (c HourCycle) String() string {
	switch c {
	case HourCycleDefault:
		return "default"
	case HourCycleH11:
		return "h11"
	case HourCycleH12:
		return "h12"
	case HourCycleH23:
		return "h23"
	case HourCycleH24:
		return "h24"
	default:
		return "unknown"
	}
}

func (c HourCycle) Valid() bool { return c <= HourCycleH24 }

type DateFieldStyle uint8

const (
	DateFieldDefault DateFieldStyle = iota
	DateFieldLong
	DateFieldShort
	DateFieldNarrow
)

func (s DateFieldStyle) String() string {
	switch s {
	case DateFieldDefault:
		return "default"
	case DateFieldLong:
		return "long"
	case DateFieldShort:
		return "short"
	case DateFieldNarrow:
		return "narrow"
	default:
		return "unknown"
	}
}

func (s DateFieldStyle) Valid() bool { return s <= DateFieldNarrow }

type DateNumericStyle uint8

const (
	DateNumericDefault DateNumericStyle = iota
	DateNumeric
	DateTwoDigit
)

func (s DateNumericStyle) String() string {
	switch s {
	case DateNumericDefault:
		return "default"
	case DateNumeric:
		return "numeric"
	case DateTwoDigit:
		return "2-digit"
	default:
		return "unknown"
	}
}

func (s DateNumericStyle) Valid() bool { return s <= DateTwoDigit }

type DateMonthStyle uint8

const (
	DateMonthDefault DateMonthStyle = iota
	DateMonthNumeric
	DateMonthTwoDigit
	DateMonthLong
	DateMonthShort
	DateMonthNarrow
)

func (s DateMonthStyle) String() string {
	switch s {
	case DateMonthDefault:
		return "default"
	case DateMonthNumeric:
		return "numeric"
	case DateMonthTwoDigit:
		return "2-digit"
	case DateMonthLong:
		return "long"
	case DateMonthShort:
		return "short"
	case DateMonthNarrow:
		return "narrow"
	default:
		return "unknown"
	}
}

func (s DateMonthStyle) Valid() bool { return s <= DateMonthNarrow }

type DateTimeZoneNameStyle uint8

const (
	DateTimeZoneNameDefault DateTimeZoneNameStyle = iota
	DateTimeZoneNameLong
	DateTimeZoneNameShort
	DateTimeZoneNameLongOffset
	DateTimeZoneNameShortOffset
	DateTimeZoneNameLongGeneric
	DateTimeZoneNameShortGeneric
)

func (s DateTimeZoneNameStyle) String() string {
	switch s {
	case DateTimeZoneNameDefault:
		return "default"
	case DateTimeZoneNameLong:
		return "long"
	case DateTimeZoneNameShort:
		return "short"
	case DateTimeZoneNameLongOffset:
		return "longOffset"
	case DateTimeZoneNameShortOffset:
		return "shortOffset"
	case DateTimeZoneNameLongGeneric:
		return "longGeneric"
	case DateTimeZoneNameShortGeneric:
		return "shortGeneric"
	default:
		return "unknown"
	}
}

func (s DateTimeZoneNameStyle) Valid() bool { return s <= DateTimeZoneNameShortGeneric }

type DateTimeFormatMatcher uint8

const (
	DateTimeMatcherDefault DateTimeFormatMatcher = iota
	DateTimeMatcherBasic
	DateTimeMatcherBestFit
)

func (m DateTimeFormatMatcher) String() string {
	switch m {
	case DateTimeMatcherDefault:
		return "default"
	case DateTimeMatcherBasic:
		return "basic"
	case DateTimeMatcherBestFit:
		return "best fit"
	default:
		return "unknown"
	}
}

func (m DateTimeFormatMatcher) Valid() bool { return m <= DateTimeMatcherBestFit }

type DateHour12 uint8

const (
	DateHour12Default DateHour12 = iota
	DateHour12Enabled
	DateHour12Disabled
)

func (h DateHour12) String() string {
	switch h {
	case DateHour12Default:
		return "default"
	case DateHour12Enabled:
		return "enabled"
	case DateHour12Disabled:
		return "disabled"
	default:
		return "unknown"
	}
}

func (h DateHour12) Valid() bool { return h <= DateHour12Disabled }

type DateFractionalSecondDigits uint8

const (
	DateFractionalSecondDigitsDefault DateFractionalSecondDigits = iota
	DateFractionalSecondDigits1
	DateFractionalSecondDigits2
	DateFractionalSecondDigits3
)

func (d DateFractionalSecondDigits) String() string {
	switch d {
	case DateFractionalSecondDigitsDefault:
		return "default"
	case DateFractionalSecondDigits1:
		return "1"
	case DateFractionalSecondDigits2:
		return "2"
	case DateFractionalSecondDigits3:
		return "3"
	default:
		return "unknown"
	}
}

func (d DateFractionalSecondDigits) Valid() bool { return d <= DateFractionalSecondDigits3 }

type DateComponents struct {
	Weekday DateFieldStyle
	Era     DateFieldStyle
	Year    DateNumericStyle
	Month   DateMonthStyle
	Day     DateNumericStyle
}

type TimeComponents struct {
	DayPeriod              DateFieldStyle
	Hour                   DateNumericStyle
	Minute                 DateNumericStyle
	Second                 DateNumericStyle
	FractionalSecondDigits DateFractionalSecondDigits
	TimeZoneName           DateTimeZoneNameStyle
}

type DateFormatSpec struct {
	Style           DateTimeStyle
	Calendar        string
	NumberingSystem string
	FormatMatcher   DateTimeFormatMatcher
	Components      DateComponents
}

type TimeFormatSpec struct {
	Style           DateTimeStyle
	Calendar        string
	NumberingSystem string
	HourCycle       HourCycle
	Hour12          DateHour12
	FormatMatcher   DateTimeFormatMatcher
	Components      TimeComponents
}

type DateTimeFormatSpec struct {
	DateStyle       DateTimeStyle
	TimeStyle       DateTimeStyle
	Calendar        string
	NumberingSystem string
	HourCycle       HourCycle
	Hour12          DateHour12
	FormatMatcher   DateTimeFormatMatcher
	DateComponents  DateComponents
	TimeComponents  TimeComponents
}

func canonicalTimeZone(value string) (string, error) {
	if value == "" || len(value) > 255 {
		return "", fmt.Errorf("time zone is empty or exceeds 255 bytes")
	}
	locales, err := locale.ParseList("en")
	if err != nil {
		return "", err
	}
	formatter, err := datetimeformat.New(locales, datetimeformat.Options{TimeZone: &value})
	if err != nil {
		return "", fmt.Errorf("time zone %q is invalid: %w", value, err)
	}
	return formatter.ResolvedOptions().TimeZone, nil
}

type dateStyle uint8

const (
	styleDate dateStyle = iota + 1
	styleTime
	styleDateTime
)

type exactDateValue struct {
	value        time.Time
	dateOnly     bool
	formatLocale string
	timeZone     string
	source       string
	style        dateStyle
	options      map[string]any
	budget       *renderBudget
	metadata     partMetadata
}

func (v *View) FormatDate(ctx context.Context, value DateValue, spec DateFormatSpec) (FormattedValue, error) {
	return directFormatDate(ctx, v, value, spec)
}

func directFormatDate(ctx context.Context, target directFormatTarget, value DateValue, spec DateFormatSpec) (FormattedValue, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedValue, error) {
		options, err := directDateFormatOptions(spec)
		if err != nil {
			return FormattedValue{}, err
		}
		if err := validateDirectDateValue(value); err != nil {
			return FormattedValue{}, err
		}
		date := time.Date(value.Year, value.Month, value.Day, 12, 0, 0, 0, time.UTC)
		return state.formatDirectDate(ctx, date, true, styleDate, "date", TypeDate, options)
	})
}

func (v *View) FormatInstantDate(ctx context.Context, value time.Time, spec DateFormatSpec) (FormattedValue, error) {
	return directFormatInstantDate(ctx, v, value, spec)
}

func directFormatInstantDate(ctx context.Context, target directFormatTarget, value time.Time, spec DateFormatSpec) (FormattedValue, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedValue, error) {
		options, err := directDateFormatOptions(spec)
		if err != nil {
			return FormattedValue{}, err
		}
		return state.formatDirectDate(ctx, value.Round(0).UTC(), false, styleDate, "date", TypeInstant, options)
	})
}

func (v *View) FormatTime(ctx context.Context, value time.Time, spec TimeFormatSpec) (FormattedValue, error) {
	return directFormatTime(ctx, v, value, spec)
}

func directFormatTime(ctx context.Context, target directFormatTarget, value time.Time, spec TimeFormatSpec) (FormattedValue, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedValue, error) {
		options, err := directTimeFormatOptions(spec)
		if err != nil {
			return FormattedValue{}, err
		}
		return state.formatDirectDate(ctx, value.Round(0).UTC(), false, styleTime, "time", TypeInstant, options)
	})
}

func (v *View) FormatDateTime(ctx context.Context, value time.Time, spec DateTimeFormatSpec) (FormattedValue, error) {
	return directFormatDateTime(ctx, v, value, spec)
}

func directFormatDateTime(ctx context.Context, target directFormatTarget, value time.Time, spec DateTimeFormatSpec) (FormattedValue, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedValue, error) {
		options, err := directDateTimeFormatOptions(spec)
		if err != nil {
			return FormattedValue{}, err
		}
		return state.formatDirectDate(ctx, value.Round(0).UTC(), false, styleDateTime, "datetime", TypeInstant, options)
	})
}

func (v *View) FormatDateRange(ctx context.Context, start, end DateValue, spec DateFormatSpec) (FormattedRange, error) {
	return directFormatDateRange(ctx, v, start, end, spec)
}

func directFormatDateRange(ctx context.Context, target directFormatTarget, start, end DateValue, spec DateFormatSpec) (FormattedRange, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedRange, error) {
		options, err := directDateFormatOptions(spec)
		if err != nil {
			return FormattedRange{}, err
		}
		if err := validateDirectDateValue(start); err != nil {
			return FormattedRange{}, fmt.Errorf("range start: %w", err)
		}
		if err := validateDirectDateValue(end); err != nil {
			return FormattedRange{}, fmt.Errorf("range end: %w", err)
		}
		startTime := time.Date(start.Year, start.Month, start.Day, 12, 0, 0, 0, time.UTC)
		endTime := time.Date(end.Year, end.Month, end.Day, 12, 0, 0, 0, time.UTC)
		return state.formatDirectDateRange(ctx, startTime, endTime, true, styleDate, "date", TypeDate, options)
	})
}

func (v *View) FormatInstantDateRange(ctx context.Context, start, end time.Time, spec DateFormatSpec) (FormattedRange, error) {
	return directFormatInstantDateRange(ctx, v, start, end, spec)
}

func directFormatInstantDateRange(ctx context.Context, target directFormatTarget, start, end time.Time, spec DateFormatSpec) (FormattedRange, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedRange, error) {
		options, err := directDateFormatOptions(spec)
		if err != nil {
			return FormattedRange{}, err
		}
		return state.formatDirectDateRange(ctx, start.Round(0).UTC(), end.Round(0).UTC(), false, styleDate, "date", TypeInstant, options)
	})
}

func (v *View) FormatTimeRange(ctx context.Context, start, end time.Time, spec TimeFormatSpec) (FormattedRange, error) {
	return directFormatTimeRange(ctx, v, start, end, spec)
}

func directFormatTimeRange(ctx context.Context, target directFormatTarget, start, end time.Time, spec TimeFormatSpec) (FormattedRange, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedRange, error) {
		options, err := directTimeFormatOptions(spec)
		if err != nil {
			return FormattedRange{}, err
		}
		return state.formatDirectDateRange(ctx, start.Round(0).UTC(), end.Round(0).UTC(), false, styleTime, "time", TypeInstant, options)
	})
}

func (v *View) FormatDateTimeRange(ctx context.Context, start, end time.Time, spec DateTimeFormatSpec) (FormattedRange, error) {
	return directFormatDateTimeRange(ctx, v, start, end, spec)
}

func directFormatDateTimeRange(ctx context.Context, target directFormatTarget, start, end time.Time, spec DateTimeFormatSpec) (FormattedRange, error) {
	return runDirectFormat(ctx, target, func(ctx context.Context, state directFormatContext) (FormattedRange, error) {
		options, err := directDateTimeFormatOptions(spec)
		if err != nil {
			return FormattedRange{}, err
		}
		return state.formatDirectDateRange(ctx, start.Round(0).UTC(), end.Round(0).UTC(), false, styleDateTime, "datetime", TypeInstant, options)
	})
}

func directDateOptions(style DateTimeStyle, calendar, numberingSystem string) (map[string]any, error) {
	if !style.Valid() {
		return nil, fmt.Errorf("%w: invalid date format", ErrInvalidMessage)
	}
	options := map[string]any{"dateStyle": directDateStyle(style)}
	if calendar != "" {
		options["calendar"] = calendar
	}
	if numberingSystem != "" {
		options["numberingSystem"] = numberingSystem
	}
	return options, nil
}

func directDateFormatOptions(spec DateFormatSpec) (map[string]any, error) {
	if !spec.FormatMatcher.Valid() {
		return nil, fmt.Errorf("%w: invalid date format", ErrInvalidMessage)
	}
	options, err := directDateOptions(spec.Style, spec.Calendar, spec.NumberingSystem)
	if err != nil {
		return nil, err
	}
	components, err := appendDirectDateComponents(options, spec.Components)
	if err != nil {
		return nil, err
	}
	if components {
		if spec.Style != DateTimeStyleDefault {
			return nil, fmt.Errorf("%w: date style conflicts with explicit components", ErrInvalidMessage)
		}
		delete(options, "dateStyle")
	}
	appendDirectFormatMatcher(options, spec.FormatMatcher)
	return options, nil
}

func directTimeFormatOptions(spec TimeFormatSpec) (map[string]any, error) {
	if !spec.HourCycle.Valid() || !spec.Hour12.Valid() || !spec.FormatMatcher.Valid() || spec.HourCycle != HourCycleDefault && spec.Hour12 != DateHour12Default {
		return nil, fmt.Errorf("%w: invalid time format", ErrInvalidMessage)
	}
	options, err := directDateOptions(spec.Style, spec.Calendar, spec.NumberingSystem)
	if err != nil {
		return nil, err
	}
	delete(options, "dateStyle")
	options["timeStyle"] = directDateStyle(spec.Style)
	components, err := appendDirectTimeComponents(options, spec.Components)
	if err != nil {
		return nil, err
	}
	if components {
		if spec.Style != DateTimeStyleDefault {
			return nil, fmt.Errorf("%w: time style conflicts with explicit components", ErrInvalidMessage)
		}
		delete(options, "timeStyle")
	}
	appendDirectTimePreferences(options, spec.HourCycle, spec.Hour12)
	appendDirectFormatMatcher(options, spec.FormatMatcher)
	return options, nil
}

func directDateTimeFormatOptions(spec DateTimeFormatSpec) (map[string]any, error) {
	if !spec.DateStyle.Valid() || !spec.TimeStyle.Valid() || !spec.HourCycle.Valid() || !spec.Hour12.Valid() || !spec.FormatMatcher.Valid() || spec.HourCycle != HourCycleDefault && spec.Hour12 != DateHour12Default {
		return nil, fmt.Errorf("%w: invalid datetime format", ErrInvalidMessage)
	}
	options := map[string]any{"dateStyle": directDateStyle(spec.DateStyle), "timeStyle": directDateStyle(spec.TimeStyle)}
	if spec.Calendar != "" {
		options["calendar"] = spec.Calendar
	}
	if spec.NumberingSystem != "" {
		options["numberingSystem"] = spec.NumberingSystem
	}
	dateComponents, err := appendDirectDateComponents(options, spec.DateComponents)
	if err != nil {
		return nil, err
	}
	timeComponents, err := appendDirectTimeComponents(options, spec.TimeComponents)
	if err != nil {
		return nil, err
	}
	if dateComponents || timeComponents {
		if spec.DateStyle != DateTimeStyleDefault || spec.TimeStyle != DateTimeStyleDefault {
			return nil, fmt.Errorf("%w: datetime styles conflict with explicit components", ErrInvalidMessage)
		}
		delete(options, "dateStyle")
		delete(options, "timeStyle")
	}
	appendDirectTimePreferences(options, spec.HourCycle, spec.Hour12)
	appendDirectFormatMatcher(options, spec.FormatMatcher)
	return options, nil
}

func appendDirectDateComponents(options map[string]any, components DateComponents) (bool, error) {
	if !components.Weekday.Valid() || !components.Era.Valid() || !components.Year.Valid() || !components.Month.Valid() || !components.Day.Valid() {
		return false, fmt.Errorf("%w: invalid date components", ErrInvalidMessage)
	}
	appendDirectEnumOption(options, "weekday", components.Weekday.String(), components.Weekday != DateFieldDefault)
	appendDirectEnumOption(options, "era", components.Era.String(), components.Era != DateFieldDefault)
	appendDirectEnumOption(options, "year", components.Year.String(), components.Year != DateNumericDefault)
	appendDirectEnumOption(options, "month", components.Month.String(), components.Month != DateMonthDefault)
	appendDirectEnumOption(options, "day", components.Day.String(), components.Day != DateNumericDefault)
	return components != (DateComponents{}), nil
}

func appendDirectTimeComponents(options map[string]any, components TimeComponents) (bool, error) {
	if !components.DayPeriod.Valid() || !components.Hour.Valid() || !components.Minute.Valid() || !components.Second.Valid() || !components.FractionalSecondDigits.Valid() || !components.TimeZoneName.Valid() {
		return false, fmt.Errorf("%w: invalid time components", ErrInvalidMessage)
	}
	appendDirectEnumOption(options, "dayPeriod", components.DayPeriod.String(), components.DayPeriod != DateFieldDefault)
	appendDirectEnumOption(options, "hour", components.Hour.String(), components.Hour != DateNumericDefault)
	appendDirectEnumOption(options, "minute", components.Minute.String(), components.Minute != DateNumericDefault)
	appendDirectEnumOption(options, "second", components.Second.String(), components.Second != DateNumericDefault)
	appendDirectEnumOption(options, "timeZoneName", components.TimeZoneName.String(), components.TimeZoneName != DateTimeZoneNameDefault)
	if components.FractionalSecondDigits != DateFractionalSecondDigitsDefault {
		options["fractionalSecondDigits"] = int(components.FractionalSecondDigits)
	}
	return components != (TimeComponents{}), nil
}

func appendDirectEnumOption(options map[string]any, name, value string, set bool) {
	if set {
		options[name] = value
	}
}

func appendDirectTimePreferences(options map[string]any, cycle HourCycle, hour12 DateHour12) {
	if cycle != HourCycleDefault {
		options["hourCycle"] = cycle.String()
	}
	if hour12 != DateHour12Default {
		options["hour12"] = hour12 == DateHour12Enabled
	}
}

func appendDirectFormatMatcher(options map[string]any, matcher DateTimeFormatMatcher) {
	if matcher != DateTimeMatcherDefault {
		options["formatMatcher"] = matcher.String()
	}
}

func directDateOptionAllowed(function, option string) bool {
	if oneOf(option, "calendar", "numberingSystem", "formatMatcher", "weekday", "era", "year", "month", "day", "dayPeriod", "hour", "minute", "second", "fractionalSecondDigits", "timeZoneName", "hourCycle", "hour12") {
		return true
	}
	switch function {
	case "date":
		return option == "dateStyle"
	case "time":
		return option == "timeStyle"
	case "datetime":
		return option == "dateStyle" || option == "timeStyle"
	default:
		return false
	}
}

func directDateStyle(style DateTimeStyle) string {
	if style == DateTimeStyleDefault {
		return "medium"
	}
	return style.String()
}

func validateNamedDateFormats(state directFormatContext, formats namedFormats) error {
	if len(formats.dates)+len(formats.times)+len(formats.dateTimes) != 0 && !state.capabilities[CapabilityDateTime] {
		return fmt.Errorf("%w: named date-time formats require the date-time capability", ErrInvalidMessage)
	}
	if len(formats.dates)+len(formats.times)+len(formats.dateTimes) != 0 {
		if err := validateFormatRequirements(state.locale, formatDate); err != nil {
			return fmt.Errorf("%w: named date-time formats: %v", ErrInvalidLocale, err)
		}
	}
	validate := func(name, function string, argumentType ArgumentType, options map[string]any) error {
		if err := validateDirectOptionBounds(options, state.limits); err != nil {
			return fmt.Errorf("named %s format %q: %w", function, name, err)
		}
		for _, option := range sortedMapKeys(options) {
			if !directDateOptionAllowed(function, option) {
				return fmt.Errorf("%w: named %s format %q has invalid option %s", ErrInvalidMessage, function, name, option)
			}
		}
		if err := validateDateConfiguration(state.locale, function, argumentType, options); err != nil {
			return fmt.Errorf("%w: named %s format %q: %v", ErrInvalidMessage, function, name, err)
		}
		return nil
	}
	for _, name := range sortedMapKeys(formats.dates) {
		spec := formats.dates[name]
		options, err := directDateFormatOptions(spec)
		if err != nil {
			return fmt.Errorf("named date format %q: %w", name, err)
		}
		if err := validate(name, "date", TypeDate, options); err != nil {
			return err
		}
	}
	for _, name := range sortedMapKeys(formats.times) {
		spec := formats.times[name]
		options, err := directTimeFormatOptions(spec)
		if err != nil {
			return fmt.Errorf("named time format %q: %w", name, err)
		}
		if err := validate(name, "time", TypeInstant, options); err != nil {
			return err
		}
	}
	for _, name := range sortedMapKeys(formats.dateTimes) {
		spec := formats.dateTimes[name]
		options, err := directDateTimeFormatOptions(spec)
		if err != nil {
			return fmt.Errorf("named datetime format %q: %w", name, err)
		}
		if err := validate(name, "datetime", TypeInstant, options); err != nil {
			return err
		}
	}
	return nil
}

func validateDirectDateValue(value DateValue) error {
	if value.Year < 1 || value.Year > 9999 || value.Month < time.January || value.Month > time.December || value.Day < 1 || value.Day > 31 {
		return fmt.Errorf("%w: date is outside the supported Gregorian range", ErrInvalidMessage)
	}
	check := time.Date(value.Year, value.Month, value.Day, 12, 0, 0, 0, time.UTC)
	if check.Year() != value.Year || check.Month() != value.Month || check.Day() != value.Day {
		return fmt.Errorf("%w: date does not exist", ErrInvalidMessage)
	}
	return nil
}

func (s directFormatContext) formatDirectDate(ctx context.Context, value time.Time, dateOnly bool, style dateStyle, function string, argumentType ArgumentType, options map[string]any) (FormattedValue, error) {
	if !s.capabilities[CapabilityDateTime] {
		return FormattedValue{}, fmt.Errorf("%w: date-time formatting capability is disabled", ErrInvalidMessage)
	}
	if err := validateDirectOptionBounds(options, s.limits); err != nil {
		return FormattedValue{}, err
	}
	for _, name := range sortedMapKeys(options) {
		if !directDateOptionAllowed(function, name) {
			return FormattedValue{}, fmt.Errorf("%w: option %s is not valid for %s formatting", ErrInvalidMessage, name, function)
		}
	}
	budget, err := s.directExactBudget(formattedValueOverhead, maximumDateValueParts)
	if err != nil {
		return FormattedValue{}, err
	}
	if err := ctx.Err(); err != nil {
		return FormattedValue{}, err
	}
	if err := validateFormatRequirements(s.locale, formatDate); err != nil {
		return FormattedValue{}, fmt.Errorf("%w: date formatting: %v", ErrInvalidLocale, err)
	}
	if err := validateDateConfiguration(s.locale, function, argumentType, options); err != nil {
		return FormattedValue{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	timeZone := s.timeZone
	if dateOnly {
		timeZone = "UTC"
	}
	exact := &exactDateValue{
		value:        value,
		dateOnly:     dateOnly,
		formatLocale: s.locale,
		timeZone:     timeZone,
		style:        style,
		options:      maps.Clone(options),
		budget:       budget,
	}
	return s.finishDirectExact(ctx, exact)
}

func (s directFormatContext) formatDirectDateRange(ctx context.Context, start, end time.Time, dateOnly bool, style dateStyle, function string, argumentType ArgumentType, options map[string]any) (FormattedRange, error) {
	if !s.capabilities[CapabilityDateTime] {
		return FormattedRange{}, fmt.Errorf("%w: date-time formatting capability is disabled", ErrInvalidMessage)
	}
	if err := validateDirectOptionBounds(options, s.limits); err != nil {
		return FormattedRange{}, err
	}
	for _, name := range sortedMapKeys(options) {
		if !directDateOptionAllowed(function, name) {
			return FormattedRange{}, fmt.Errorf("%w: option %s is not valid for %s formatting", ErrInvalidMessage, name, function)
		}
	}
	if err := s.requireDirectOutput(formattedValueOverhead*2, maximumDateRangeParts); err != nil {
		return FormattedRange{}, err
	}
	if err := validateFormatRequirements(s.locale, formatDate); err != nil {
		return FormattedRange{}, fmt.Errorf("%w: date formatting: %v", ErrInvalidLocale, err)
	}
	if err := validateDateConfiguration(s.locale, function, argumentType, options); err != nil {
		return FormattedRange{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	timeZone := s.timeZone
	if dateOnly {
		timeZone = "UTC"
	}
	value := exactDateValue{value: start, dateOnly: dateOnly, formatLocale: s.locale, timeZone: timeZone, style: style, options: maps.Clone(options)}
	formatter, err := value.dateFormatter()
	if err != nil {
		return FormattedRange{}, fmt.Errorf("%w: date range format: %v", ErrInvalidMessage, err)
	}
	source := formatter.FormatRangeToParts(start, end)
	parts := make([]directRangePart, len(source))
	for index, part := range source {
		rangeSource, ok := directDateRangeSource(part.Source)
		if !ok {
			return FormattedRange{}, fmt.Errorf("%w: date formatter returned invalid range source", ErrInvalidMessage)
		}
		kind := PartValue
		if part.Type == datetimeformat.PartLiteral {
			kind = PartText
		}
		parts[index] = directRangePart{kind: kind, typeName: string(part.Type), text: part.Value, direction: bidi.GetLocaleDirection(s.locale), source: rangeSource}
	}
	return s.finishDirectRange(ctx, s.locale, parts)
}

const maximumDateRangeParts = maximumDateValueParts*2 + 1

func directDateRangeSource(source datetimeformat.RangeSource) (RangeSource, bool) {
	switch source {
	case datetimeformat.SourceStartRange:
		return RangeSourceStart, true
	case datetimeformat.SourceShared:
		return RangeSourceShared, true
	case datetimeformat.SourceEndRange:
		return RangeSourceEnd, true
	default:
		return 0, false
	}
}

const maximumDateValueParts = 33

func (v *exactDateValue) Type() string {
	return "datetime"
}

func (v *exactDateValue) Source() string {
	return v.source
}

func (v *exactDateValue) Dir() bidi.Direction {
	return v.metadata.dir(bidi.GetLocaleDirection(v.formatLocale))
}

func (v *exactDateValue) Locale() string {
	return v.formatLocale
}

func (v *exactDateValue) ToString() (string, error) {
	if err := v.budget.require(formattedValueOverhead, 1); err != nil {
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

func (v *exactDateValue) format() (string, error) {
	formatter, err := v.dateFormatter()
	if err != nil {
		return "", err
	}
	return formatter.Format(v.value), nil
}

func (v *exactDateValue) formatParts() (string, []Subpart, error) {
	formatter, err := v.dateFormatter()
	if err != nil {
		return "", nil, err
	}
	formatted := formatter.FormatToParts(v.value)
	parts := make([]Subpart, len(formatted))
	var text strings.Builder
	for index, part := range formatted {
		parts[index] = Subpart{Type: string(part.Type), Text: part.Value}
		text.WriteString(part.Value)
	}
	return text.String(), parts, nil
}

func (v *exactDateValue) dateFormatter() (*datetimeformat.DateTimeFormat, error) {
	locales, err := locale.ParseList(v.formatLocale)
	if err != nil {
		return nil, err
	}
	options := v.dateOptions()
	formatter, err := datetimeformat.New(locales, options)
	if err != nil {
		return nil, err
	}
	if err := validateResolvedDateLocale(v.formatLocale, formatter.ResolvedOptions(), v.options); err != nil {
		return nil, err
	}
	return formatter, nil
}

func (v *exactDateValue) ToParts() ([]messagevalue.MessagePart, error) {
	if err := v.budget.require(formattedValueOverhead, maximumDateValueParts); err != nil {
		return nil, err
	}
	formatted, subparts, err := v.formatParts()
	if err != nil {
		return nil, err
	}
	if err := v.budget.charge(len(formatted), 1+len(subparts)); err != nil {
		return nil, err
	}
	return []messagevalue.MessagePart{formattedMessagePart{kind: "datetime", value: formatted, source: v.source, locale: v.formatLocale, dir: v.Dir(), id: v.metadata.id, parts: subparts}}, nil
}

func (v *exactDateValue) ValueOf() (any, error) {
	clone := *v
	clone.options = maps.Clone(v.options)
	return &clone, nil
}

func (v *exactDateValue) dateOptions() datetimeformat.Options {
	zone := v.timeZone
	if v.dateOnly {
		zone = "UTC"
	}
	options := datetimeformat.Options{TimeZone: &zone}
	for name, value := range v.options {
		if name == "fractionalSecondDigits" {
			if digits, ok := value.(int); ok {
				options.FractionalSecondDigits = &digits
			}
			continue
		}
		if name == "hour12" {
			if hour12, ok := value.(bool); ok {
				options.Hour12 = &hour12
			}
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		switch name {
		case "calendar":
			options.Calendar = &text
		case "numberingSystem":
			options.NumberingSystem = &text
		case "localeMatcher":
			options.LocaleMatcher = &text
		case "formatMatcher":
			options.FormatMatcher = &text
		case "timeZoneName":
			options.TimeZoneName = &text
		case "weekday":
			options.Weekday = &text
		case "era":
			options.Era = &text
		case "year":
			options.Year = &text
		case "month":
			options.Month = &text
		case "day":
			options.Day = &text
		case "dayPeriod":
			options.DayPeriod = &text
		case "hour":
			options.Hour = &text
		case "minute":
			options.Minute = &text
		case "second":
			options.Second = &text
		case "hourCycle":
			options.HourCycle = &text
		case "dateStyle":
			options.DateStyle = &text
		case "timeStyle":
			options.TimeStyle = &text
		}
	}
	if options.DateStyle == nil && options.TimeStyle == nil && options.Year == nil && options.Month == nil && options.Day == nil && options.Hour == nil && options.Minute == nil && options.Second == nil {
		medium := "medium"
		switch v.style {
		case styleDate:
			options.DateStyle = &medium
		case styleTime:
			options.TimeStyle = &medium
		default:
			options.DateStyle = &medium
			options.TimeStyle = &medium
		}
	}
	return options
}

func exactDateFunction(style dateStyle) functions.MessageFunction {
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
		value, err := dateOperand(operand)
		if err != nil {
			ctx.OnError(errors.New("date function requires a Date or Instant argument"))
			return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
		}
		if err := value.budget.require(0, 0); err != nil {
			return value
		}
		if style == styleTime && value.dateOnly {
			ctx.OnError(errors.New("a calendar Date has no time of day"))
			return messagevalue.NewFallbackValue(ctx.Source(), firstLocale(ctx.Locales()))
		}
		clone := *value
		clone.style = style
		clone.source = ctx.Source()
		clone.options = withoutUniversalOptions(options.Map())
		clone.metadata = metadataFromFunctionOperand(ctx, value.Dir())
		return &clone
	}
}

func dateOperand(operand any) (*exactDateValue, error) {
	if value, ok := operand.(*exactDateValue); ok && value != nil {
		return value, nil
	}
	if value, ok := operand.(messagevalue.Valuer); ok {
		resolved, err := value.ValueOf()
		if err != nil {
			return nil, err
		}
		if date, ok := resolved.(*exactDateValue); ok && date != nil {
			return date, nil
		}
	}
	return nil, errors.New("date function requires a Date or Instant argument")
}

func validateDateConfiguration(localeName, name string, argumentType ArgumentType, options map[string]any) error {
	style := styleDate
	switch name {
	case "time":
		style = styleTime
	case "datetime":
		style = styleDateTime
	}
	value := exactDateValue{
		value:        time.Date(2000, time.January, 2, 15, 4, 5, 0, time.UTC),
		dateOnly:     argumentType == TypeDate,
		formatLocale: localeName,
		timeZone:     "UTC",
		style:        style,
		options:      maps.Clone(options),
	}
	locales, err := locale.ParseList(localeName)
	if err != nil {
		return err
	}
	formatter, err := datetimeformat.New(locales, value.dateOptions())
	if err != nil {
		return err
	}
	resolved := formatter.ResolvedOptions()
	if err := validateResolvedDateLocale(localeName, resolved, options); err != nil {
		return err
	}
	if requested, ok := optionString(options, "calendar"); ok && resolved.Calendar != requested {
		return fmt.Errorf("%s calendar option %q is unsupported", name, requested)
	}
	if requested, ok := optionString(options, "numberingSystem"); ok && resolved.NumberingSystem != requested {
		return fmt.Errorf("%s numberingSystem option %q is unsupported", name, requested)
	}
	return nil
}
