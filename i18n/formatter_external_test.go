package i18n_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/i18n"
)

func TestFormatterStandaloneDeclarativeDX(t *testing.T) {
	var observations atomic.Int64
	capabilities := []i18n.Capability{i18n.CapabilityDateTime, i18n.CapabilityUnit}
	numbers := []i18n.NamedNumberFormat{{Name: "invoice.amount", Spec: i18n.NumberFormatSpec{Grouping: i18n.NumberGroupingNever}}}
	formats := i18n.Formats{
		Numbers:      numbers,
		Money:        []i18n.NamedMoneyFormat{{Name: "invoice.money"}},
		Percents:     []i18n.NamedPercentFormat{{Name: "invoice.percent"}},
		Units:        []i18n.NamedUnitFormat{{Name: "distance.long", Spec: i18n.UnitFormatSpec{Unit: "meter", Width: i18n.FormatWidthLong}}},
		Plurals:      []i18n.NamedPluralFormat{{Name: "items.ordinal", Spec: i18n.PluralFormatSpec{Type: i18n.PluralOrdinal}}},
		Dates:        []i18n.NamedDateFormat{{Name: "date.short", Spec: i18n.DateFormatSpec{Style: i18n.DateTimeStyleShort}}},
		Times:        []i18n.NamedTimeFormat{{Name: "time.short", Spec: i18n.TimeFormatSpec{Style: i18n.DateTimeStyleShort}}},
		DateTimes:    []i18n.NamedDateTimeFormat{{Name: "instant.short", Spec: i18n.DateTimeFormatSpec{DateStyle: i18n.DateTimeStyleShort, TimeStyle: i18n.DateTimeStyleShort}}},
		Lists:        []i18n.NamedListFormat{{Name: "list.or", Spec: i18n.ListFormatSpec{Type: i18n.ListDisjunction}}},
		Relatives:    []i18n.NamedRelativeFormat{{Name: "relative.auto", Spec: i18n.RelativeFormatSpec{Numeric: i18n.RelativeNumericAuto}}},
		Durations:    []i18n.NamedDurationFormat{{Name: "duration.digital", Spec: i18n.DurationFormatSpec{Style: i18n.DurationDigital, FractionalDigits: i18n.DurationFractionalDigits2}}},
		DisplayNames: []i18n.NamedDisplayNameFormat{{Name: "region.long", Spec: i18n.DisplayNameSpec{Type: i18n.DisplayRegion}}},
	}
	formatter, err := i18n.NewFormatter(i18n.FormatterSpec{
		Locale:       "en",
		Capabilities: capabilities,
		Formats:      formats,
		Observer: func(context.Context, i18n.Observation) {
			observations.Add(1)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	capabilities[0] = i18n.CapabilityUnit
	numbers[0].Name = "mutated"
	numbers[0].Spec.Grouping = i18n.NumberGroupingAlways

	now := time.Date(2026, time.September, 9, 12, 34, 56, 0, time.UTC)
	checks := []func() (i18n.FormattedValue, error){
		func() (i18n.FormattedValue, error) {
			return formatter.FormatNamedNumber(context.Background(), "invoice.amount", i18n.DecimalNumber("1234.50"))
		},
		func() (i18n.FormattedValue, error) {
			return formatter.FormatNamedMoney(context.Background(), "invoice.money", i18n.MoneyValue{Amount: i18n.DecimalNumber("12.50"), Currency: "USD"})
		},
		func() (i18n.FormattedValue, error) {
			return formatter.FormatNamedPercent(context.Background(), "invoice.percent", i18n.DecimalNumber("0.25"))
		},
		func() (i18n.FormattedValue, error) {
			return formatter.FormatNamedUnit(context.Background(), "distance.long", i18n.SignedNumber(3))
		},
		func() (i18n.FormattedValue, error) {
			return formatter.FormatNamedDate(context.Background(), "date.short", i18n.DateValue{Year: 2026, Month: time.September, Day: 9})
		},
		func() (i18n.FormattedValue, error) {
			return formatter.FormatNamedInstantDate(context.Background(), "date.short", now)
		},
		func() (i18n.FormattedValue, error) {
			return formatter.FormatNamedTime(context.Background(), "time.short", now)
		},
		func() (i18n.FormattedValue, error) {
			return formatter.FormatNamedDateTime(context.Background(), "instant.short", now)
		},
		func() (i18n.FormattedValue, error) {
			return formatter.FormatNamedList(context.Background(), "list.or", []string{"red", "blue"})
		},
		func() (i18n.FormattedValue, error) {
			return formatter.FormatNamedRelative(context.Background(), "relative.auto", -1, i18n.RelativeDay)
		},
		func() (i18n.FormattedValue, error) {
			return formatter.FormatNamedDuration(context.Background(), "duration.digital", i18n.DurationValue{Minutes: 1, Seconds: 2})
		},
	}
	for index, check := range checks {
		formatted, err := check()
		if err != nil {
			t.Fatalf("format %d: %v", index, err)
		}
		if formatted.Text == "" || formatted.Locale != "en" || formatted.Text != joinExternalParts(formatted.Parts) {
			t.Fatalf("format %d = %#v", index, formatted)
		}
	}
	display, found, err := formatter.FormatNamedDisplayName(context.Background(), "region.long", "US")
	if err != nil || !found || display.Text == "" || display.Text != joinExternalParts(display.Parts) {
		t.Fatalf("display name = %#v, %t, %v", display, found, err)
	}
	if category, err := formatter.SelectNamedPlural(context.Background(), "items.ordinal", i18n.SignedNumber(2)); err != nil || category != i18n.PluralTwo {
		t.Fatalf("plural = %s, %v", category, err)
	}
	numberRange, err := formatter.FormatNamedNumberRange(context.Background(), "invoice.amount", i18n.DecimalNumber("1.20"), i18n.DecimalNumber("2.30"))
	if err != nil || numberRange.Text == "" || numberRange.Text != joinExternalRangeParts(numberRange.Parts) {
		t.Fatalf("number range = %#v, %v", numberRange, err)
	}
	dateRange, err := formatter.FormatNamedDateRange(context.Background(), "date.short", i18n.DateValue{Year: 2026, Month: time.September, Day: 9}, i18n.DateValue{Year: 2026, Month: time.September, Day: 10})
	if err != nil || dateRange.Text == "" || dateRange.Text != joinExternalRangeParts(dateRange.Parts) {
		t.Fatalf("date range = %#v, %v", dateRange, err)
	}
	rangeChecks := []func() (i18n.FormattedRange, error){
		func() (i18n.FormattedRange, error) {
			return formatter.FormatNamedMoneyRange(context.Background(), "invoice.money", i18n.MoneyValue{Amount: "1", Currency: "USD"}, i18n.MoneyValue{Amount: "2", Currency: "USD"})
		},
		func() (i18n.FormattedRange, error) {
			return formatter.FormatNamedPercentRange(context.Background(), "invoice.percent", i18n.DecimalNumber("0.1"), i18n.DecimalNumber("0.2"))
		},
		func() (i18n.FormattedRange, error) {
			return formatter.FormatNamedUnitRange(context.Background(), "distance.long", i18n.SignedNumber(1), i18n.SignedNumber(2))
		},
		func() (i18n.FormattedRange, error) {
			return formatter.FormatNamedInstantDateRange(context.Background(), "date.short", now, now.AddDate(0, 0, 1))
		},
		func() (i18n.FormattedRange, error) {
			return formatter.FormatNamedTimeRange(context.Background(), "time.short", now, now.Add(time.Hour))
		},
		func() (i18n.FormattedRange, error) {
			return formatter.FormatNamedDateTimeRange(context.Background(), "instant.short", now, now.Add(time.Hour))
		},
	}
	for index, check := range rangeChecks {
		value, err := check()
		if err != nil || value.Text == "" || value.Text != joinExternalRangeParts(value.Parts) {
			t.Fatalf("named range %d = %#v, %v", index, value, err)
		}
	}
	if _, err := formatter.FormatNamedNumber(context.Background(), "mutated", i18n.SignedNumber(1)); !errors.Is(err, i18n.ErrFormatNotFound) {
		t.Fatalf("mutated name error = %v", err)
	}
	if observations.Load() != int64(len(checks)+5+len(rangeChecks)) {
		t.Fatalf("observations = %d", observations.Load())
	}
}

func TestFormatterConcurrentImmutableUse(t *testing.T) {
	formatter, err := i18n.NewFormatter(i18n.FormatterSpec{
		Locale:       "ar-u-nu-arab",
		Presentation: i18n.PresentationDefault,
		Formats: i18n.Formats{Numbers: []i18n.NamedNumberFormat{{
			Name: "number.exact",
			Spec: i18n.NumberFormatSpec{Grouping: i18n.NumberGroupingNever, FractionDigits: i18n.Digits(2, 2)},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	var wait sync.WaitGroup
	wait.Add(workers)
	errorsSeen := make(chan error, workers)
	for range workers {
		go func() {
			defer wait.Done()
			for range 50 {
				formatted, err := formatter.FormatNamedNumber(context.Background(), "number.exact", i18n.DecimalNumber("12.30"))
				if err != nil {
					errorsSeen <- err
					return
				}
				if !strings.HasPrefix(formatted.Text, "\u2067") || !strings.HasSuffix(formatted.Text, "\u2069") || formatted.Text != joinExternalParts(formatted.Parts) {
					errorsSeen <- errors.New("inconsistent isolated output")
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
}

func TestFormatterConstructionRejectsInvalidRegistry(t *testing.T) {
	tests := []struct {
		name string
		spec i18n.FormatterSpec
		want error
	}{
		{name: "missing locale", spec: i18n.FormatterSpec{}, want: i18n.ErrInvalidLocale},
		{name: "non utc without version", spec: i18n.FormatterSpec{Locale: "en", TimeZone: "Asia/Almaty"}, want: i18n.ErrInvalidMessage},
		{name: "invalid version utf8", spec: i18n.FormatterSpec{Locale: "en", TimeZoneDataVersion: "\xff"}, want: i18n.ErrInvalidMessage},
		{name: "unsafe version", spec: i18n.FormatterSpec{Locale: "en", TimeZoneDataVersion: "v\u202e"}, want: i18n.ErrInvalidMessage},
		{name: "version limit", spec: i18n.FormatterSpec{Locale: "en", TimeZoneDataVersion: "12345", Limits: i18n.Limits{MaxRevisionBytes: 4}}, want: i18n.ErrInvalidMessage},
		{name: "invalid capability", spec: i18n.FormatterSpec{Locale: "en", Capabilities: []i18n.Capability{255}}, want: i18n.ErrInvalidMessage},
		{name: "duplicate capability", spec: i18n.FormatterSpec{Locale: "en", Capabilities: []i18n.Capability{i18n.CapabilityUnit, i18n.CapabilityUnit}}, want: i18n.ErrInvalidMessage},
		{name: "newline name", spec: i18n.FormatterSpec{Locale: "en", Formats: i18n.Formats{Numbers: []i18n.NamedNumberFormat{{Name: "bad\nname"}}}}, want: i18n.ErrInvalidMessage},
		{name: "nul name", spec: i18n.FormatterSpec{Locale: "en", Formats: i18n.Formats{Numbers: []i18n.NamedNumberFormat{{Name: "bad\x00name"}}}}, want: i18n.ErrInvalidMessage},
		{name: "unicode name", spec: i18n.FormatterSpec{Locale: "en", Formats: i18n.Formats{Numbers: []i18n.NamedNumberFormat{{Name: "cafe\u0301"}}}}, want: i18n.ErrInvalidMessage},
		{name: "duplicate name", spec: i18n.FormatterSpec{Locale: "en", Formats: i18n.Formats{Numbers: []i18n.NamedNumberFormat{{Name: "same"}, {Name: "same"}}}}, want: i18n.ErrInvalidMessage},
		{name: "capability missing", spec: i18n.FormatterSpec{Locale: "en", Formats: i18n.Formats{Units: []i18n.NamedUnitFormat{{Name: "unit", Spec: i18n.UnitFormatSpec{Unit: "meter"}}}}}, want: i18n.ErrInvalidMessage},
		{name: "invalid named option", spec: i18n.FormatterSpec{Locale: "en", Formats: i18n.Formats{Relatives: []i18n.NamedRelativeFormat{{Name: "relative", Spec: i18n.RelativeFormatSpec{Width: i18n.FormatWidth(255)}}}}}, want: i18n.ErrInvalidMessage},
		{name: "registry limit", spec: i18n.FormatterSpec{Locale: "en", Limits: i18n.Limits{MaxCatalogItems: 1}, Formats: i18n.Formats{Numbers: []i18n.NamedNumberFormat{{Name: "one"}, {Name: "two"}}}}, want: i18n.ErrLimitExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := i18n.NewFormatter(test.spec); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
	var formatter *i18n.Formatter
	if _, err := formatter.FormatNamedNumber(context.Background(), "number", i18n.SignedNumber(1)); !errors.Is(err, i18n.ErrInvalidMessage) {
		t.Fatalf("nil named formatter error = %v", err)
	}
}

func joinExternalParts(parts []i18n.Part) string {
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.Text)
	}
	return text.String()
}

func joinExternalRangeParts(parts []i18n.RangePart) string {
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.Text)
	}
	return text.String()
}
