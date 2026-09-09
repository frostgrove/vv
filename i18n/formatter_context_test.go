package i18n

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kaptinlin/messageformat-go/pkg/bidi"
)

func TestDirectGeneralFormattersWholeOutputIsolation(t *testing.T) {
	formatter, err := NewFormatter(FormatterSpec{Locale: "ar", Presentation: PresentationDefault})
	if err != nil {
		t.Fatal(err)
	}
	checks := []func() (FormattedValue, error){
		func() (FormattedValue, error) {
			return formatter.FormatList(context.Background(), []string{"one", "two"}, ListFormatSpec{})
		},
		func() (FormattedValue, error) {
			return formatter.FormatRelative(context.Background(), -1, RelativeDay, RelativeFormatSpec{})
		},
		func() (FormattedValue, error) {
			return formatter.FormatDuration(context.Background(), DurationValue{Hours: 1, Minutes: 2}, DurationFormatSpec{})
		},
		func() (FormattedValue, error) {
			value, _, err := formatter.FormatDisplayName(context.Background(), "US", DisplayNameSpec{Type: DisplayRegion})
			return value, err
		},
	}
	for index, check := range checks {
		formatted, err := check()
		if err != nil {
			t.Fatalf("format %d: %v", index, err)
		}
		if !strings.HasPrefix(formatted.Text, string(bidi.RLI)) || !strings.HasSuffix(formatted.Text, string(bidi.PDI)) || formatted.Text != joinPartText(formatted.Parts) {
			t.Fatalf("format %d = %#v", index, formatted)
		}
		if formatted.Parts[0].Kind != PartBidiIsolation || formatted.Parts[len(formatted.Parts)-1].Kind != PartBidiIsolation {
			t.Fatalf("format %d isolation parts = %#v", index, formatted.Parts)
		}
	}
	list, err := formatter.FormatList(context.Background(), []string{"one", "two"}, ListFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if countPartKind(list.Parts, PartBidiIsolation) != 6 {
		t.Fatalf("list isolation = %#v", list.Parts)
	}
}

func TestDirectGeneralFormatterPrevalidationAndPreflight(t *testing.T) {
	identifierLimits := Limits{MaxIdentifierBytes: 4}
	formatter, err := NewFormatter(FormatterSpec{Locale: "en", Limits: identifierLimits})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := formatter.FormatRelative(context.Background(), 1, RelativeDay, RelativeFormatSpec{NumberingSystem: "toolong"}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("relative identifier error = %v", err)
	}
	if _, err := formatter.FormatDuration(context.Background(), DurationValue{Seconds: 1}, DurationFormatSpec{NumberingSystem: "a\u202e"}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("duration bidi error = %v", err)
	}
	if _, err := formatter.FormatDuration(context.Background(), DurationValue{Years: 1}, DurationFormatSpec{Years: DurationUnitSpec{Style: DurationUnitNumeric}}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("duration unit error = %v", err)
	}
	if _, err := formatter.FormatDuration(context.Background(), DurationValue{Years: 1 << 32}, DurationFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("duration calendar bound error = %v", err)
	}
	if _, err := formatter.FormatDuration(context.Background(), DurationValue{Seconds: maximumExactIntlInteger, Nanoseconds: maximumExactIntlInteger}, DurationFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("duration normalized bound error = %v", err)
	}
	if formatted, err := formatter.FormatDuration(context.Background(), DurationValue{Milliseconds: 12}, DurationFormatSpec{Milliseconds: DurationUnitSpec{Style: DurationUnitNumeric}}); err != nil || formatted.Text == "" {
		t.Fatalf("numeric subsecond = %#v, %v", formatted, err)
	}

	tests := []struct {
		name  string
		bytes int
		parts int
		call  func(*Formatter) error
	}{
		{name: "relative", bytes: formattedValueOverhead, parts: maximumRelativeValueParts, call: func(f *Formatter) error {
			_, err := f.FormatRelative(context.Background(), 1, RelativeDay, RelativeFormatSpec{})
			return err
		}},
		{name: "duration", bytes: maximumDurationValueBytes, parts: maximumDurationValueParts, call: func(f *Formatter) error {
			_, err := f.FormatDuration(context.Background(), DurationValue{Seconds: 1}, DurationFormatSpec{})
			return err
		}},
		{name: "display", bytes: formattedValueOverhead, parts: 1, call: func(f *Formatter) error {
			_, _, err := f.FormatDisplayName(context.Background(), "US", DisplayNameSpec{Type: DisplayRegion})
			return err
		}},
		{name: "list", bytes: len("ab") + formattedValueOverhead, parts: 9, call: func(f *Formatter) error {
			_, err := f.FormatList(context.Background(), []string{"a", "b"}, ListFormatSpec{})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			atBound, err := NewFormatter(FormatterSpec{Locale: "en", Presentation: PresentationNoIsolation, Limits: Limits{MaxOutputBytes: test.bytes, MaxOutputParts: test.parts}})
			if err != nil {
				t.Fatal(err)
			}
			if err := test.call(atBound); err != nil {
				t.Fatalf("atad bound: %v", err)
			}
			oneLess, err := NewFormatter(FormatterSpec{Locale: "en", Presentation: PresentationNoIsolation, Limits: Limits{MaxOutputBytes: test.bytes - 1, MaxOutputParts: test.parts}})
			if err != nil {
				t.Fatal(err)
			}
			if err := test.call(oneLess); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("one-less error = %v", err)
			}
			if test.parts > 1 {
				oneLessParts, err := NewFormatter(FormatterSpec{Locale: "en", Presentation: PresentationNoIsolation, Limits: Limits{MaxOutputBytes: test.bytes, MaxOutputParts: test.parts - 1}})
				if err != nil {
					t.Fatal(err)
				}
				if err := test.call(oneLessParts); !errors.Is(err, ErrLimitExceeded) {
					t.Fatalf("one-less parts error = %v", err)
				}
			}
		})
	}
}

func TestDurationFractionalDigitsClosedEnum(t *testing.T) {
	values := []DurationFractionalDigits{
		DurationFractionalDigitsDefault,
		DurationFractionalDigits0,
		DurationFractionalDigits1,
		DurationFractionalDigits2,
		DurationFractionalDigits3,
		DurationFractionalDigits4,
		DurationFractionalDigits5,
		DurationFractionalDigits6,
		DurationFractionalDigits7,
		DurationFractionalDigits8,
		DurationFractionalDigits9,
	}
	for _, value := range values {
		if !value.Valid() || value.String() == "unknown" {
			t.Fatalf("fractional digits %d = %q", value, value.String())
		}
	}
	if DurationFractionalDigits(255).Valid() || DurationFractionalDigits(255).String() != "unknown" {
		t.Fatal("invalid duration fractional digits accepted")
	}
}

func TestViewBuildsIndependentFormatter(t *testing.T) {
	view := mustView(t, localeSnapshot(t, "en", "ready"), "en", "", "", PresentationNoIsolation)
	formatter, err := view.Formatter(Formats{Numbers: []NamedNumberFormat{{Name: "plain", Spec: NumberFormatSpec{Grouping: NumberGroupingNever}}}})
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := formatter.FormatNamedNumber(context.Background(), "plain", DecimalNumber("12.30"))
	if err != nil || formatted.Text != "12.30" {
		t.Fatalf("formatted = %#v, %v", formatted, err)
	}
}

func TestNamedFormatNamesRespectIdentifierLimitBeforeLookup(t *testing.T) {
	const oversized = "12345"
	limits := Limits{MaxIdentifierBytes: len(oversized) - 1}
	formatter, err := NewFormatter(FormatterSpec{
		Locale: "en",
		Limits: limits,
		Formats: Formats{
			Numbers: []NamedNumberFormat{{Name: "four"}},
			Plurals: []NamedPluralFormat{{Name: "many"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = formatter.FormatNamedNumber(context.Background(), oversized, SignedNumber(1))
	if !errors.Is(err, ErrLimitExceeded) || strings.Contains(err.Error(), oversized) {
		t.Fatalf("oversized number lookup error = %v", err)
	}
	_, err = formatter.SelectNamedPlural(context.Background(), oversized, SignedNumber(1))
	if !errors.Is(err, ErrLimitExceeded) || strings.Contains(err.Error(), oversized) {
		t.Fatalf("oversized plural lookup error = %v", err)
	}
	_, err = formatter.FormatNamedNumber(context.Background(), "bad\n", SignedNumber(1))
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("invalid lookup error = %v", err)
	}
	_, err = formatter.FormatNamedNumber(context.Background(), "none", SignedNumber(1))
	if !errors.Is(err, ErrFormatNotFound) {
		t.Fatalf("missing lookup error = %v", err)
	}
	_, err = NewFormatter(FormatterSpec{
		Locale:  "en",
		Limits:  limits,
		Formats: Formats{Numbers: []NamedNumberFormat{{Name: oversized}}},
	})
	if !errors.Is(err, ErrLimitExceeded) || strings.Contains(err.Error(), oversized) {
		t.Fatalf("oversized registry error = %v", err)
	}
}
