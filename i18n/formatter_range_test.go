package i18n

import (
	"context"
	"errors"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kaptinlin/messageformat-go/pkg/bidi"
)

func TestDirectNumberRangesPreserveExactEndpointsAndSources(t *testing.T) {
	formatter, err := NewFormatter(FormatterSpec{Locale: "en", Capabilities: []Capability{CapabilityUnit}, Presentation: PresentationNoIsolation})
	if err != nil {
		t.Fatal(err)
	}
	large := new(big.Int)
	large.SetString("1234567890123456789012345678901234567890", 10)
	tests := []struct {
		name  string
		start NumericValue
		end   NumericValue
		spec  NumberFormatSpec
	}{
		{name: "decimals", start: DecimalNumber("1.20"), end: DecimalNumber("2.345")},
		{name: "big integers", start: NewBigIntegerNumber(large), end: NewBigIntegerNumber(new(big.Int).Add(large, big.NewInt(2))), spec: NumberFormatSpec{Grouping: NumberGroupingNever}},
		{name: "inverted", start: SignedNumber(2), end: SignedNumber(1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			formatted, err := formatter.FormatNumberRange(context.Background(), test.start, test.end, test.spec)
			if err != nil {
				t.Fatal(err)
			}
			assertFormattedRange(t, formatted)
			if !rangeHasSource(formatted.Parts, RangeSourceStart) || !rangeHasSource(formatted.Parts, RangeSourceEnd) || !rangeHasSource(formatted.Parts, RangeSourceShared) {
				t.Fatalf("range sources = %#v", formatted.Parts)
			}
		})
	}
	equal, err := formatter.FormatNumberRange(context.Background(), DecimalNumber("1.0"), DecimalNumber("1.00"), NumberFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	assertFormattedRange(t, equal)
	for _, part := range equal.Parts {
		if part.Source != RangeSourceShared {
			t.Fatalf("equal range source = %#v", equal.Parts)
		}
	}
}

func TestDirectRangeStylesAndValidation(t *testing.T) {
	formatter, err := NewFormatter(FormatterSpec{Locale: "en", Capabilities: []Capability{CapabilityUnit, CapabilityDateTime}, Presentation: PresentationNoIsolation})
	if err != nil {
		t.Fatal(err)
	}
	rounding := NumberFormatSpec{FractionDigits: Digits(2, 2), RoundingIncrement: NumberRoundingIncrement25, MinimumIntegerDigits: 2, Notation: NumberNotationScientific}
	percent, err := formatter.FormatPercentRange(context.Background(), DecimalNumber("0.0125"), DecimalNumber("0.0375"), PercentFormatSpec{Number: rounding})
	if err != nil {
		t.Fatal(err)
	}
	assertFormattedRange(t, percent)
	unit, err := formatter.FormatUnitRange(context.Background(), SignedNumber(1), SignedNumber(2), UnitFormatSpec{Number: rounding, Unit: "meter", Width: FormatWidthLong})
	if err != nil {
		t.Fatal(err)
	}
	assertFormattedRange(t, unit)
	money, err := formatter.FormatMoneyRange(context.Background(), MoneyValue{Amount: "1.00", Currency: "usd"}, MoneyValue{Amount: "2.00", Currency: "USD"}, MoneyFormatSpec{Display: CurrencyDisplayCode})
	if err != nil {
		t.Fatal(err)
	}
	assertFormattedRange(t, money)
	if _, err := formatter.FormatMoneyRange(context.Background(), MoneyValue{Amount: "1", Currency: "USD"}, MoneyValue{Amount: "2", Currency: "EUR"}, MoneyFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("mixed currency error = %v", err)
	}
	if _, err := formatter.FormatNumberRange(context.Background(), SignedNumber(1), DecimalNumber("bad"), NumberFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("invalid endpoint error = %v", err)
	}
}

func TestDirectDateRangesAcceptEqualInvertedAndZeroInstants(t *testing.T) {
	formatter, err := NewFormatter(FormatterSpec{Locale: "en", Capabilities: []Capability{CapabilityDateTime}, Presentation: PresentationNoIsolation})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.September, 10, 10, 15, 0, 0, time.UTC)
	end := time.Date(2026, time.September, 9, 9, 30, 0, 0, time.UTC)
	checks := []func() (FormattedRange, error){
		func() (FormattedRange, error) {
			return formatter.FormatDateRange(context.Background(), DateValue{Year: 2026, Month: time.September, Day: 10}, DateValue{Year: 2026, Month: time.September, Day: 9}, DateFormatSpec{})
		},
		func() (FormattedRange, error) {
			return formatter.FormatInstantDateRange(context.Background(), time.Time{}, time.Time{}, DateFormatSpec{})
		},
		func() (FormattedRange, error) {
			return formatter.FormatTimeRange(context.Background(), start, end, TimeFormatSpec{})
		},
		func() (FormattedRange, error) {
			return formatter.FormatDateTimeRange(context.Background(), start, end, DateTimeFormatSpec{})
		},
	}
	for index, check := range checks {
		formatted, err := check()
		if err != nil {
			t.Fatalf("range %d: %v", index, err)
		}
		assertFormattedRange(t, formatted)
		if index == 1 {
			for _, part := range formatted.Parts {
				if part.Source != RangeSourceShared {
					t.Fatalf("equal date range source = %#v", formatted.Parts)
				}
			}
		}
	}
	if _, err := formatter.FormatDateRange(context.Background(), DateValue{Year: 2026, Month: 2, Day: 30}, DateValue{Year: 2026, Month: 3, Day: 1}, DateFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("invalid date endpoint = %v", err)
	}
}

func TestDirectRangeIsolationBudgetsCancellationAndMutation(t *testing.T) {
	isolated, err := NewFormatter(FormatterSpec{Locale: "ar", Presentation: PresentationDefault})
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := isolated.FormatNumberRange(context.Background(), SignedNumber(1), SignedNumber(2), NumberFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	assertFormattedRange(t, formatted)
	if !strings.HasPrefix(formatted.Text, string(bidi.RLI)) || !strings.HasSuffix(formatted.Text, string(bidi.PDI)) || formatted.Parts[0].Source != RangeSourceShared || formatted.Parts[len(formatted.Parts)-1].Source != RangeSourceShared {
		t.Fatalf("isolated range = %#v", formatted)
	}
	formatted.Parts[0].Text = "changed"
	again, err := isolated.FormatNumberRange(context.Background(), SignedNumber(1), SignedNumber(2), NumberFormatSpec{})
	if err != nil || again.Parts[0].Text != string(bidi.RLI) {
		t.Fatalf("mutation leaked: %#v, %v", again, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := isolated.FormatNumberRange(canceled, SignedNumber(1), SignedNumber(2), NumberFormatSpec{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}

	options := map[string]any{}
	startInput := numericInput{kind: numericSigned, signed: 1}
	endInput := numericInput{kind: numericSigned, signed: 2}
	startBytes, _ := numericOutputBytes(startInput, "number", options)
	endBytes, _ := numericOutputBytes(endInput, "number", options)
	startParts, _ := numericOutputParts(startInput, "number", options)
	endParts, _ := numericOutputParts(endInput, "number", options)
	limits := Limits{MaxOutputBytes: saturatedDirectAdd(saturatedDirectAdd(startBytes, endBytes), formattedValueOverhead), MaxOutputParts: saturatedDirectAdd(saturatedDirectAdd(startParts, endParts), 1)}
	atBound, err := NewFormatter(FormatterSpec{Locale: "en", Presentation: PresentationNoIsolation, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := atBound.FormatNumberRange(context.Background(), SignedNumber(1), SignedNumber(2), NumberFormatSpec{}); err != nil {
		t.Fatalf("exact bound error = %v", err)
	}
	limits.MaxOutputBytes--
	oneLess, err := NewFormatter(FormatterSpec{Locale: "en", Presentation: PresentationNoIsolation, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oneLess.FormatNumberRange(context.Background(), SignedNumber(1), SignedNumber(2), NumberFormatSpec{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("one-less byte error = %v", err)
	}
	limits.MaxOutputBytes++
	limits.MaxOutputParts--
	oneLessParts, err := NewFormatter(FormatterSpec{Locale: "en", Presentation: PresentationNoIsolation, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oneLessParts.FormatNumberRange(context.Background(), SignedNumber(1), SignedNumber(2), NumberFormatSpec{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("one-less parts error = %v", err)
	}
}

func TestRangeSourceClosedEnum(t *testing.T) {
	values := []struct {
		value RangeSource
		name  string
	}{{RangeSourceStart, "startRange"}, {RangeSourceShared, "shared"}, {RangeSourceEnd, "endRange"}}
	for _, value := range values {
		if !value.value.Valid() || value.value.String() != value.name {
			t.Fatalf("range source = %d, %q", value.value, value.value.String())
		}
	}
	if RangeSource(0).Valid() || RangeSource(255).Valid() || RangeSource(255).String() != "unknown" {
		t.Fatal("invalid range source accepted")
	}
}

func TestViewDirectRangeAndPluralSurface(t *testing.T) {
	view := mustView(t, directCoreSnapshot(t, []Capability{CapabilityDateTime, CapabilityUnit}, Limits{}, nil), "en", "", "", PresentationNoIsolation)
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	ranges := []func() (FormattedRange, error){
		func() (FormattedRange, error) {
			return view.FormatNumberRange(context.Background(), SignedNumber(1), SignedNumber(2), NumberFormatSpec{})
		},
		func() (FormattedRange, error) {
			return view.FormatMoneyRange(context.Background(), MoneyValue{Amount: "1", Currency: "USD"}, MoneyValue{Amount: "2", Currency: "USD"}, MoneyFormatSpec{})
		},
		func() (FormattedRange, error) {
			return view.FormatPercentRange(context.Background(), DecimalNumber("0.1"), DecimalNumber("0.2"), PercentFormatSpec{})
		},
		func() (FormattedRange, error) {
			return view.FormatUnitRange(context.Background(), SignedNumber(1), SignedNumber(2), UnitFormatSpec{Unit: "meter"})
		},
		func() (FormattedRange, error) {
			return view.FormatDateRange(context.Background(), DateValue{Year: 2026, Month: 9, Day: 9}, DateValue{Year: 2026, Month: 9, Day: 10}, DateFormatSpec{})
		},
		func() (FormattedRange, error) {
			return view.FormatInstantDateRange(context.Background(), now, now.AddDate(0, 0, 1), DateFormatSpec{})
		},
		func() (FormattedRange, error) {
			return view.FormatTimeRange(context.Background(), now, now.Add(time.Hour), TimeFormatSpec{})
		},
		func() (FormattedRange, error) {
			return view.FormatDateTimeRange(context.Background(), now, now.Add(time.Hour), DateTimeFormatSpec{})
		},
	}
	for index, format := range ranges {
		value, err := format()
		if err != nil {
			t.Fatalf("range %d: %v", index, err)
		}
		assertFormattedRange(t, value)
	}
	if category, err := view.SelectPlural(context.Background(), SignedNumber(1), PluralFormatSpec{}); err != nil || category != PluralOne {
		t.Fatalf("plural = %s, %v", category, err)
	}
	if category, err := view.SelectPluralRange(context.Background(), SignedNumber(1), SignedNumber(2), PluralFormatSpec{}); err != nil || category != PluralOther {
		t.Fatalf("plural range = %s, %v", category, err)
	}
}

func assertFormattedRange(t *testing.T, formatted FormattedRange) {
	t.Helper()
	var text strings.Builder
	for _, part := range formatted.Parts {
		if !part.Source.Valid() || part.Type == "" || part.Locale != formatted.Locale {
			t.Fatalf("invalid range part = %#v", part)
		}
		text.WriteString(part.Text)
	}
	if formatted.Text == "" || formatted.Text != text.String() {
		t.Fatalf("range = %#v", formatted)
	}
}

func rangeHasSource(parts []RangePart, source RangeSource) bool {
	return slices.ContainsFunc(parts, func(part RangePart) bool { return part.Source == source })
}
