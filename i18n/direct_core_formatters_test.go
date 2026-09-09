package i18n

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaptinlin/messageformat-go/pkg/bidi"
)

func directCoreSnapshot(t *testing.T, capabilities []Capability, limits Limits, observer Observer) *Snapshot {
	t.Helper()
	spec := testCatalog(simpleMessage("anchor", "ready"))
	spec.Capabilities = capabilities
	spec.TimeZoneDataVersion = "test-tzdb-2026a"
	spec.Limits = limits
	spec.Observer = observer
	return mustSnapshot(t, spec)
}

func directCoreValuePart(t *testing.T, formatted FormattedValue, typeName string) Part {
	t.Helper()
	if formatted.Text != joinPartText(formatted.Parts) {
		t.Fatalf("parts join to %q, want %q", joinPartText(formatted.Parts), formatted.Text)
	}
	var value *Part
	for index := range formatted.Parts {
		part := &formatted.Parts[index]
		if part.Type == "" {
			t.Fatalf("part type is empty: %#v", formatted.Parts)
		}
		if part.Kind == PartValue {
			if value != nil {
				t.Fatalf("multiple value parts: %#v", formatted.Parts)
			}
			value = part
		}
	}
	if value == nil || value.Type != typeName || len(value.Subparts) == 0 || joinedSubparts(value.Subparts) != value.Text {
		t.Fatalf("value part = %#v, want type %q with exact subparts", value, typeName)
	}
	for _, part := range value.Subparts {
		if part.Type == "" {
			t.Fatalf("subpart type is empty: %#v", value.Subparts)
		}
	}
	return *value
}

func TestDirectCoreNumericFormattersPreserveExactValues(t *testing.T) {
	snapshot := directCoreSnapshot(t, []Capability{CapabilityUnit}, Limits{}, nil)
	view := mustView(t, snapshot, "en", "", "", PresentationNoIsolation)
	original := new(big.Int)
	if _, ok := original.SetString("-1234567890123456789012345678901234567890", 10); !ok {
		t.Fatal("failed to construct bigint")
	}
	bigValue := NewBigIntegerNumber(original)
	original.SetInt64(9)

	tests := []struct {
		name  string
		value NumericValue
		want  string
	}{
		{name: "signed", value: SignedNumber(-42), want: "-42"},
		{name: "unsigned", value: UnsignedNumber(^uint64(0)), want: "18446744073709551615"},
		{name: "big integer", value: bigValue, want: "-1234567890123456789012345678901234567890"},
		{name: "lexical decimal", value: DecimalNumber("12345678901234567890.2300"), want: "12345678901234567890.2300"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			formatted, err := view.FormatNumber(nil, test.value, NumberFormatSpec{Grouping: NumberGroupingNever})
			if err != nil {
				t.Fatal(err)
			}
			if formatted.Text != test.want || formatted.Locale != "en" {
				t.Fatalf("formatted = %#v, want %q", formatted, test.want)
			}
			part := directCoreValuePart(t, formatted, "number")
			if part.Direction != string(bidi.DirLTR) {
				t.Fatalf("direction = %q", part.Direction)
			}
		})
	}

	padded, err := view.FormatNumber(context.Background(), SignedNumber(1200), NumberFormatSpec{
		Grouping:             NumberGroupingNever,
		Sign:                 NumberSignAlways,
		MinimumIntegerDigits: 6,
		FractionDigits:       Digits(2, 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if padded.Text != "+001200.00" {
		t.Fatalf("padded number = %q", padded.Text)
	}

	money, err := view.FormatMoney(context.Background(), MoneyValue{Amount: DecimalNumber("-12.50"), Currency: "usd"}, MoneyFormatSpec{Sign: CurrencySignAccounting})
	if err != nil {
		t.Fatal(err)
	}
	if money.Text != "($12.50)" {
		t.Fatalf("money = %q", money.Text)
	}
	directCoreValuePart(t, money, "number")

	percent, err := view.FormatPercent(context.Background(), DecimalNumber("0.1250"), PercentFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if percent.Text != "12.50%" {
		t.Fatalf("percent = %q", percent.Text)
	}
	directCoreValuePart(t, percent, "number")

	unit, err := view.FormatUnit(context.Background(), SignedNumber(3), UnitFormatSpec{Unit: "meter", Width: FormatWidthLong})
	if err != nil {
		t.Fatal(err)
	}
	if unit.Text != "3 meters" {
		t.Fatalf("unit = %q", unit.Text)
	}
	directCoreValuePart(t, unit, "number")
}

func TestDirectPercentAndUnitAcceptBackendNumberOptions(t *testing.T) {
	formatter, err := NewFormatter(FormatterSpec{Locale: "en", Capabilities: []Capability{CapabilityUnit}, Presentation: PresentationNoIsolation})
	if err != nil {
		t.Fatal(err)
	}
	tests := []NumberFormatSpec{
		{MinimumIntegerDigits: 3},
		{Notation: NumberNotationScientific},
		{Notation: NumberNotationEngineering},
		{Notation: NumberNotationCompact, Compact: NumberCompactLong},
		{FractionDigits: Digits(2, 2), RoundingIncrement: NumberRoundingIncrement25},
		{FractionDigits: Digits(0, 2), SignificantDigits: Digits(1, 3), RoundingPriority: NumberRoundingPriorityMorePrecision},
		{FractionDigits: Digits(2, 2), RoundingMode: NumberRoundingModeHalfEven, TrailingZeroDisplay: NumberTrailingZeroStripIfInteger},
	}
	for index, spec := range tests {
		percent, err := formatter.FormatPercent(context.Background(), DecimalNumber("12.375"), PercentFormatSpec{Number: spec})
		if err != nil || percent.Text == "" || percent.Text != joinPartText(percent.Parts) {
			t.Fatalf("percent %d = %#v, %v", index, percent, err)
		}
		unit, err := formatter.FormatUnit(context.Background(), DecimalNumber("12.375"), UnitFormatSpec{Number: spec, Unit: "meter", Width: FormatWidthLong})
		if err != nil || unit.Text == "" || unit.Text != joinPartText(unit.Parts) {
			t.Fatalf("unit %d = %#v, %v", index, unit, err)
		}
	}
}

func TestDirectCoreFormattingLocalePresentationAndParts(t *testing.T) {
	snapshot := directCoreSnapshot(t, nil, Limits{}, nil)
	isolated := mustView(t, snapshot, "en", "", "", PresentationDefault)
	formatted, err := isolated.FormatNumber(context.Background(), DecimalNumber("12.30"), NumberFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if formatted.Text != string(bidi.LRI)+"12.30"+string(bidi.PDI) || len(formatted.Parts) != 3 {
		t.Fatalf("isolated number = %#v", formatted)
	}
	value := directCoreValuePart(t, formatted, "number")
	if value.Text != "12.30" || formatted.Parts[0].Type != "bidiIsolation" || formatted.Parts[2].Type != "bidiIsolation" {
		t.Fatalf("isolated parts = %#v", formatted.Parts)
	}

	arabic := mustView(t, snapshot, "en", "ar-u-nu-arab", "", PresentationDefault)
	formatted, err = arabic.FormatNumber(context.Background(), SignedNumber(123), NumberFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	value = directCoreValuePart(t, formatted, "number")
	if formatted.Locale != "ar-u-nu-arab" || !strings.Contains(formatted.Text, "١٢٣") || !strings.HasPrefix(formatted.Text, string(bidi.RLI)) || value.Direction != string(bidi.DirRTL) {
		t.Fatalf("Arabic number = %#v", formatted)
	}

	plain := mustView(t, snapshot, "en", "", "", PresentationNoIsolation)
	formatted, err = plain.FormatNumber(context.Background(), SignedNumber(1), NumberFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(formatted.Text, "\u2066\u2067\u2068\u2069") || len(formatted.Parts) != 1 {
		t.Fatalf("plain number = %#v", formatted)
	}
}

func TestDirectCoreDateFormattersSeparateCalendarDatesAndInstants(t *testing.T) {
	snapshot := directCoreSnapshot(t, []Capability{CapabilityDateTime}, Limits{}, nil)
	view := mustView(t, snapshot, "en", "", "Pacific/Honolulu", PresentationNoIsolation)

	date, err := view.FormatDate(context.Background(), DateValue{Year: 2024, Month: time.January, Day: 2}, DateFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if date.Text != "Jan 2, 2024" {
		t.Fatalf("calendar date = %q", date.Text)
	}
	directCoreValuePart(t, date, "datetime")

	instant := time.Date(2024, time.January, 2, 1, 30, 0, 123, time.UTC)
	instantDate, err := view.FormatInstantDate(context.Background(), instant, DateFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if instantDate.Text != "Jan 1, 2024" {
		t.Fatalf("instant date = %q", instantDate.Text)
	}
	directCoreValuePart(t, instantDate, "datetime")

	timeOnly, err := view.FormatTime(context.Background(), instant, TimeFormatSpec{HourCycle: HourCycleH12})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(timeOnly.Text, "3:30:00 PM") {
		t.Fatalf("time = %q", timeOnly.Text)
	}
	directCoreValuePart(t, timeOnly, "datetime")

	dateTime, err := view.FormatDateTime(context.Background(), instant, DateTimeFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dateTime.Text, "Jan 1, 2024") || !strings.Contains(dateTime.Text, "3:30:00 PM") {
		t.Fatalf("datetime = %q", dateTime.Text)
	}
	directCoreValuePart(t, dateTime, "datetime")
	if _, err := view.FormatInstantDate(context.Background(), time.Time{}, DateFormatSpec{}); err != nil {
		t.Fatalf("zero instant error = %v", err)
	}
}

func TestDirectCoreFormattersFailClosed(t *testing.T) {
	view := mustView(t, directCoreSnapshot(t, nil, Limits{}, nil), "en", "", "", PresentationNoIsolation)
	var missing NumericValue
	invalidNumbers := []struct {
		name  string
		value NumericValue
		spec  NumberFormatSpec
	}{
		{name: "nil", value: missing},
		{name: "nil bigint", value: NewBigIntegerNumber(nil)},
		{name: "zero bigint", value: BigIntegerNumber{}},
		{name: "empty decimal", value: DecimalNumber("")},
		{name: "decimal scale", value: DecimalNumber("0." + strings.Repeat("1", 101))},
		{name: "grouping", value: SignedNumber(1), spec: NumberFormatSpec{Grouping: NumberGrouping(255)}},
		{name: "digits", value: SignedNumber(1), spec: NumberFormatSpec{FractionDigits: Digits(3, 2)}},
		{name: "significant zero", value: SignedNumber(1), spec: NumberFormatSpec{SignificantDigits: Digits(0, 2)}},
		{name: "compact dependency", value: SignedNumber(1), spec: NumberFormatSpec{Compact: NumberCompactLong}},
		{name: "unsafe numbering system", value: SignedNumber(1), spec: NumberFormatSpec{NumberingSystem: "latn\u202e"}},
	}
	for _, test := range invalidNumbers {
		t.Run(test.name, func(t *testing.T) {
			if _, err := view.FormatNumber(context.Background(), test.value, test.spec); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("error = %v", err)
			}
		})
	}

	if _, err := view.FormatMoney(context.Background(), MoneyValue{Amount: "1", Currency: "US"}, MoneyFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("currency error = %v", err)
	}
	if _, err := view.FormatMoney(context.Background(), MoneyValue{Amount: "1", Currency: "USD"}, MoneyFormatSpec{Display: CurrencyDisplay(255)}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("currency display error = %v", err)
	}
	if _, err := view.FormatPercent(context.Background(), SignedNumber(1), PercentFormatSpec{Number: NumberFormatSpec{Notation: NumberNotationScientific}}); err != nil {
		t.Fatalf("percent scientific error = %v", err)
	}
	if _, err := view.FormatUnit(context.Background(), SignedNumber(1), UnitFormatSpec{Unit: "meter"}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("missing unit capability error = %v", err)
	}
	if _, err := view.FormatDate(context.Background(), DateValue{Year: 2024, Month: time.February, Day: 30}, DateFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("invalid date error = %v", err)
	}
	if _, err := view.FormatTime(context.Background(), time.Now(), TimeFormatSpec{HourCycle: HourCycle(255)}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("hour-cycle error = %v", err)
	}
	if _, err := view.FormatDateTime(context.Background(), time.Now(), DateTimeFormatSpec{DateStyle: DateTimeStyle(255)}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("date-style error = %v", err)
	}
	if _, err := view.FormatDate(context.Background(), DateValue{Year: 2024, Month: time.January, Day: 1}, DateFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("missing date capability error = %v", err)
	}

	capable := mustView(t, directCoreSnapshot(t, []Capability{CapabilityDateTime, CapabilityUnit}, Limits{}, nil), "en", "", "", PresentationNoIsolation)
	if _, err := capable.FormatNumber(context.Background(), SignedNumber(1), NumberFormatSpec{NumberingSystem: "bogus"}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("numbering-system error = %v", err)
	}
	if _, err := capable.FormatUnit(context.Background(), SignedNumber(1), UnitFormatSpec{Unit: "Meter"}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("unit identifier error = %v", err)
	}
	if _, err := capable.FormatDate(context.Background(), DateValue{Year: 2024, Month: time.January, Day: 1}, DateFormatSpec{Calendar: "bogus"}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("calendar error = %v", err)
	}

	bigLimits := DefaultLimits()
	bigLimits.MaxBigIntegerBits = 64
	bigView := mustView(t, directCoreSnapshot(t, nil, bigLimits, nil), "en", "", "", PresentationNoIsolation)
	tooWide := new(big.Int).Lsh(big.NewInt(1), 64)
	if _, err := bigView.FormatNumber(context.Background(), NewBigIntegerNumber(tooWide), NumberFormatSpec{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("big integer limit error = %v", err)
	}

	var nilView *View
	if _, err := nilView.FormatNumber(context.Background(), SignedNumber(1), NumberFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("nil view number error = %v", err)
	}
	if _, err := nilView.FormatDate(context.Background(), DateValue{Year: 2024, Month: time.January, Day: 1}, DateFormatSpec{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("nil view date error = %v", err)
	}
}

func TestDirectCoreFormatterPreflightBounds(t *testing.T) {
	input := numericInput{kind: numericSigned, signed: 7}
	options := map[string]any{"useGrouping": "never"}
	numberBytes, err := numericOutputBytes(input, "number", options)
	if err != nil {
		t.Fatal(err)
	}
	numberParts, err := numericOutputParts(input, "number", options)
	if err != nil {
		t.Fatal(err)
	}

	for _, presentation := range []Presentation{PresentationNoIsolation, PresentationDefault} {
		extraBytes, extraParts := 0, 0
		if presentation == PresentationDefault {
			extraBytes = len(string(bidi.LRI)) + len(string(bidi.PDI))
			extraParts = 2
		}
		limits := DefaultLimits()
		limits.MaxOutputBytes = numberBytes + extraBytes
		limits.MaxOutputParts = numberParts + extraParts
		view := mustView(t, directCoreSnapshot(t, nil, limits, nil), "en", "", "", presentation)
		if _, err := view.FormatNumber(context.Background(), SignedNumber(7), NumberFormatSpec{Grouping: NumberGroupingNever}); err != nil {
			t.Fatalf("at bound %s: %v", presentation, err)
		}

		partLimits := limits
		partLimits.MaxOutputParts--
		partView := mustView(t, directCoreSnapshot(t, nil, partLimits, nil), "en", "", "", presentation)
		if _, err := partView.FormatNumber(context.Background(), SignedNumber(7), NumberFormatSpec{Grouping: NumberGroupingNever}); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("part one-less %s error = %v", presentation, err)
		}

		byteLimits := limits
		byteLimits.MaxOutputBytes--
		byteView := mustView(t, directCoreSnapshot(t, nil, byteLimits, nil), "en", "", "", presentation)
		if _, err := byteView.FormatNumber(context.Background(), SignedNumber(7), NumberFormatSpec{Grouping: NumberGroupingNever}); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("byte one-less %s error = %v", presentation, err)
		}
	}

	dateLimits := DefaultLimits()
	dateLimits.MaxOutputBytes = formattedValueOverhead
	dateLimits.MaxOutputParts = maximumDateValueParts
	dateView := mustView(t, directCoreSnapshot(t, []Capability{CapabilityDateTime}, dateLimits, nil), "en", "", "", PresentationNoIsolation)
	date := DateValue{Year: 2024, Month: time.January, Day: 2}
	if _, err := dateView.FormatDate(context.Background(), date, DateFormatSpec{}); err != nil {
		t.Fatalf("date at bound: %v", err)
	}
	dateLimits.MaxOutputParts--
	dateView = mustView(t, directCoreSnapshot(t, []Capability{CapabilityDateTime}, dateLimits, nil), "en", "", "", PresentationNoIsolation)
	if _, err := dateView.FormatDate(context.Background(), date, DateFormatSpec{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("date one-less error = %v", err)
	}
	dateLimits = DefaultLimits()
	dateLimits.MaxOutputBytes = formattedValueOverhead - 1
	dateView = mustView(t, directCoreSnapshot(t, []Capability{CapabilityDateTime}, dateLimits, nil), "en", "", "", PresentationNoIsolation)
	if _, err := dateView.FormatDate(context.Background(), date, DateFormatSpec{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("date byte one-less error = %v", err)
	}

	huge := new(big.Int).Exp(big.NewInt(10), big.NewInt(20000), nil)
	hugeLimits := DefaultLimits()
	hugeLimits.MaxOutputParts = 1
	hugeView := mustView(t, directCoreSnapshot(t, nil, hugeLimits, nil), "en", "", "", PresentationNoIsolation)
	if _, err := hugeView.FormatNumber(context.Background(), NewBigIntegerNumber(huge), NumberFormatSpec{NumberingSystem: "bogus"}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("huge preflight error = %v", err)
	}
}

func TestDirectCoreFormatterLocaleFallbackIsNeverSilent(t *testing.T) {
	spec := testCatalog(simpleMessage("anchor", "ready"))
	spec.SourceLocale = "ast"
	spec.DefaultLocale = "ast"
	spec.Supported = []string{"ast"}
	spec.Capabilities = []Capability{CapabilityDateTime}
	snapshot := mustSnapshot(t, spec)
	view := mustView(t, snapshot, "ast", "", "", PresentationNoIsolation)
	if _, err := view.FormatNumber(context.Background(), SignedNumber(1), NumberFormatSpec{}); !errors.Is(err, ErrInvalidLocale) {
		t.Fatalf("number locale fallback error = %v", err)
	}
	if _, err := view.FormatDate(context.Background(), DateValue{Year: 2024, Month: time.January, Day: 1}, DateFormatSpec{}); !errors.Is(err, ErrInvalidLocale) {
		t.Fatalf("date locale fallback error = %v", err)
	}
	if _, err := view.FormatNumberRange(context.Background(), SignedNumber(1), SignedNumber(2), NumberFormatSpec{}); !errors.Is(err, ErrInvalidLocale) {
		t.Fatalf("number range locale fallback error = %v", err)
	}
	if _, err := view.SelectPlural(context.Background(), SignedNumber(1), PluralFormatSpec{}); !errors.Is(err, ErrInvalidLocale) {
		t.Fatalf("plural locale fallback error = %v", err)
	}
}

func TestDirectCoreFormatterCancellationPanicMutationAndRace(t *testing.T) {
	var observed atomic.Int64
	snapshot := directCoreSnapshot(t, nil, Limits{}, func(_ context.Context, observation Observation) {
		if observation.Operation == OperationRender {
			observed.Add(1)
		}
	})
	view := mustView(t, snapshot, "en", "", "", PresentationNoIsolation)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := view.FormatNumber(canceled, SignedNumber(1), NumberFormatSpec{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}

	if _, err := runDirectFormat(context.Background(), view, func(context.Context, directFormatContext) (FormattedValue, error) {
		panic("formatter")
	}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("panic error = %v", err)
	}

	original, err := view.FormatNumber(context.Background(), DecimalNumber("1234.50"), NumberFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	original.Parts[0].Text = "changed"
	for index := range original.Parts {
		if len(original.Parts[index].Subparts) != 0 {
			original.Parts[index].Subparts[0].Text = "changed"
		}
	}
	again, err := view.FormatNumber(context.Background(), DecimalNumber("1234.50"), NumberFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if again.Text != "1,234.50" || joinedSubparts(directCoreValuePart(t, again, "number").Subparts) != "1,234.50" {
		t.Fatalf("mutated result aliased later output: %#v", again)
	}

	bigInput, ok := new(big.Int).SetString("123456789012345678901234567890", 10)
	if !ok {
		t.Fatal("failed to construct bigint")
	}
	value := NewBigIntegerNumber(bigInput)
	const workers = 32
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			formatted, err := view.FormatNumber(context.Background(), value, NumberFormatSpec{Grouping: NumberGroupingNever})
			if err != nil || formatted.Text != "123456789012345678901234567890" {
				t.Errorf("concurrent number = %#v, %v", formatted, err)
			}
		}()
	}
	wait.Wait()
	if observed.Load() != workers+4 {
		t.Fatalf("observations = %d, want %d", observed.Load(), workers+4)
	}
}
