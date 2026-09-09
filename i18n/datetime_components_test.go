package i18n

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDirectDateTimeComponentsAndRanges(t *testing.T) {
	formatter, err := NewFormatter(FormatterSpec{Locale: "en-US", Capabilities: []Capability{CapabilityDateTime}, Presentation: PresentationNoIsolation})
	if err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, time.September, 9, 15, 4, 5, 123456789, time.UTC)
	date, err := formatter.FormatInstantDate(context.Background(), instant, DateFormatSpec{
		FormatMatcher: DateTimeMatcherBasic,
		Components:    DateComponents{Weekday: DateFieldLong, Era: DateFieldShort, Year: DateNumeric, Month: DateMonthLong, Day: DateTwoDigit},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(date.Text, "Sep") || !strings.Contains(date.Text, "2026") {
		t.Fatalf("component date = %#v", date)
	}
	timeValue, err := formatter.FormatTime(context.Background(), instant, TimeFormatSpec{
		Hour12:        DateHour12Disabled,
		FormatMatcher: DateTimeMatcherBestFit,
		Components: TimeComponents{
			Hour: DateTwoDigit, Minute: DateTwoDigit, Second: DateTwoDigit,
			FractionalSecondDigits: DateFractionalSecondDigits3,
			TimeZoneName:           DateTimeZoneNameShortOffset,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(timeValue.Text, "123") || !strings.Contains(timeValue.Text, "GMT") {
		t.Fatalf("component time = %#v", timeValue)
	}
	dateTimeSpec := DateTimeFormatSpec{
		HourCycle:      HourCycleH23,
		FormatMatcher:  DateTimeMatcherBasic,
		DateComponents: DateComponents{Year: DateNumeric, Month: DateMonthTwoDigit, Day: DateTwoDigit},
		TimeComponents: TimeComponents{Hour: DateTwoDigit, Minute: DateTwoDigit, TimeZoneName: DateTimeZoneNameLongOffset},
	}
	dateTime, err := formatter.FormatDateTime(context.Background(), instant, dateTimeSpec)
	if err != nil {
		t.Fatal(err)
	}
	if dateTime.Text == "" || dateTime.Text != joinPartText(dateTime.Parts) {
		t.Fatalf("component datetime = %#v", dateTime)
	}
	ranged, err := formatter.FormatDateTimeRange(context.Background(), instant, instant.Add(90*time.Minute), dateTimeSpec)
	if err != nil {
		t.Fatal(err)
	}
	assertFormattedRange(t, ranged)
}

func TestDirectDateTimeComponentValidation(t *testing.T) {
	formatter, err := NewFormatter(FormatterSpec{Locale: "en", Capabilities: []Capability{CapabilityDateTime}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		call func() error
	}{
		{name: "date style conflict", call: func() error {
			_, err := formatter.FormatDate(context.Background(), DateValue{Year: 2026, Month: 9, Day: 9}, DateFormatSpec{Style: DateTimeStyleShort, Components: DateComponents{Year: DateNumeric}})
			return err
		}},
		{name: "time style conflict", call: func() error {
			_, err := formatter.FormatTime(context.Background(), time.Now(), TimeFormatSpec{Style: DateTimeStyleShort, Components: TimeComponents{Hour: DateNumeric}})
			return err
		}},
		{name: "datetime style conflict", call: func() error {
			_, err := formatter.FormatDateTime(context.Background(), time.Now(), DateTimeFormatSpec{DateStyle: DateTimeStyleShort, DateComponents: DateComponents{Year: DateNumeric}})
			return err
		}},
		{name: "hour preference conflict", call: func() error {
			_, err := formatter.FormatTime(context.Background(), time.Now(), TimeFormatSpec{HourCycle: HourCycleH23, Hour12: DateHour12Enabled})
			return err
		}},
		{name: "field", call: func() error {
			_, err := formatter.FormatDate(context.Background(), DateValue{Year: 2026, Month: 9, Day: 9}, DateFormatSpec{Components: DateComponents{Weekday: DateFieldStyle(255)}})
			return err
		}},
		{name: "numeric", call: func() error {
			_, err := formatter.FormatTime(context.Background(), time.Now(), TimeFormatSpec{Components: TimeComponents{Hour: DateNumericStyle(255)}})
			return err
		}},
		{name: "month", call: func() error {
			_, err := formatter.FormatDate(context.Background(), DateValue{Year: 2026, Month: 9, Day: 9}, DateFormatSpec{Components: DateComponents{Month: DateMonthStyle(255)}})
			return err
		}},
		{name: "zone", call: func() error {
			_, err := formatter.FormatTime(context.Background(), time.Now(), TimeFormatSpec{Components: TimeComponents{TimeZoneName: DateTimeZoneNameStyle(255)}})
			return err
		}},
		{name: "fraction", call: func() error {
			_, err := formatter.FormatTime(context.Background(), time.Now(), TimeFormatSpec{Components: TimeComponents{FractionalSecondDigits: DateFractionalSecondDigits(255)}})
			return err
		}},
		{name: "matcher", call: func() error {
			_, err := formatter.FormatTime(context.Background(), time.Now(), TimeFormatSpec{FormatMatcher: DateTimeFormatMatcher(255)})
			return err
		}},
		{name: "hour12", call: func() error {
			_, err := formatter.FormatTime(context.Background(), time.Now(), TimeFormatSpec{Hour12: DateHour12(255)})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestDateTimeComponentClosedEnums(t *testing.T) {
	field := []struct {
		value DateFieldStyle
		name  string
	}{{DateFieldDefault, "default"}, {DateFieldLong, "long"}, {DateFieldShort, "short"}, {DateFieldNarrow, "narrow"}}
	for _, value := range field {
		if !value.value.Valid() || value.value.String() != value.name {
			t.Fatalf("field %d = %q", value.value, value.value.String())
		}
	}
	numeric := []struct {
		value DateNumericStyle
		name  string
	}{{DateNumericDefault, "default"}, {DateNumeric, "numeric"}, {DateTwoDigit, "2-digit"}}
	for _, value := range numeric {
		if !value.value.Valid() || value.value.String() != value.name {
			t.Fatalf("numeric %d = %q", value.value, value.value.String())
		}
	}
	months := []DateMonthStyle{DateMonthDefault, DateMonthNumeric, DateMonthTwoDigit, DateMonthLong, DateMonthShort, DateMonthNarrow}
	zones := []DateTimeZoneNameStyle{DateTimeZoneNameDefault, DateTimeZoneNameLong, DateTimeZoneNameShort, DateTimeZoneNameLongOffset, DateTimeZoneNameShortOffset, DateTimeZoneNameLongGeneric, DateTimeZoneNameShortGeneric}
	matchers := []DateTimeFormatMatcher{DateTimeMatcherDefault, DateTimeMatcherBasic, DateTimeMatcherBestFit}
	hour12 := []DateHour12{DateHour12Default, DateHour12Enabled, DateHour12Disabled}
	fractions := []DateFractionalSecondDigits{DateFractionalSecondDigitsDefault, DateFractionalSecondDigits1, DateFractionalSecondDigits2, DateFractionalSecondDigits3}
	for _, values := range []int{len(months), len(zones), len(matchers), len(hour12), len(fractions)} {
		if values == 0 {
			t.Fatal("empty enum")
		}
	}
	for _, value := range months {
		if !value.Valid() || value.String() == "unknown" {
			t.Fatalf("month %d", value)
		}
	}
	for _, value := range zones {
		if !value.Valid() || value.String() == "unknown" {
			t.Fatalf("zone %d", value)
		}
	}
	for _, value := range matchers {
		if !value.Valid() || value.String() == "unknown" {
			t.Fatalf("matcher %d", value)
		}
	}
	for _, value := range hour12 {
		if !value.Valid() || value.String() == "unknown" {
			t.Fatalf("hour12 %d", value)
		}
	}
	for _, value := range fractions {
		if !value.Valid() || value.String() == "unknown" {
			t.Fatalf("fraction %d", value)
		}
	}
	if DateFieldStyle(255).Valid() || DateNumericStyle(255).Valid() || DateMonthStyle(255).Valid() || DateTimeZoneNameStyle(255).Valid() || DateTimeFormatMatcher(255).Valid() || DateHour12(255).Valid() || DateFractionalSecondDigits(255).Valid() {
		t.Fatal("invalid enum accepted")
	}
}
