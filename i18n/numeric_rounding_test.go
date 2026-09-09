package i18n

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
)

func TestNumberRoundingEnums(t *testing.T) {
	modes := []struct {
		value NumberRoundingMode
		name  string
	}{
		{NumberRoundingModeDefault, "default"},
		{NumberRoundingModeCeil, "ceil"},
		{NumberRoundingModeFloor, "floor"},
		{NumberRoundingModeExpand, "expand"},
		{NumberRoundingModeTrunc, "trunc"},
		{NumberRoundingModeHalfCeil, "halfCeil"},
		{NumberRoundingModeHalfFloor, "halfFloor"},
		{NumberRoundingModeHalfExpand, "halfExpand"},
		{NumberRoundingModeHalfTrunc, "halfTrunc"},
		{NumberRoundingModeHalfEven, "halfEven"},
	}
	for _, mode := range modes {
		if !mode.value.Valid() || mode.value.String() != mode.name {
			t.Fatalf("rounding mode %d = (%t, %q), want (true, %q)", mode.value, mode.value.Valid(), mode.value.String(), mode.name)
		}
	}
	if invalid := NumberRoundingMode(255); invalid.Valid() || invalid.String() != "unknown" {
		t.Fatalf("invalid rounding mode = (%t, %q)", invalid.Valid(), invalid.String())
	}

	priorities := []struct {
		value NumberRoundingPriority
		name  string
	}{
		{NumberRoundingPriorityDefault, "default"},
		{NumberRoundingPriorityAuto, "auto"},
		{NumberRoundingPriorityMorePrecision, "morePrecision"},
		{NumberRoundingPriorityLessPrecision, "lessPrecision"},
	}
	for _, priority := range priorities {
		if !priority.value.Valid() || priority.value.String() != priority.name {
			t.Fatalf("rounding priority %d = (%t, %q), want (true, %q)", priority.value, priority.value.Valid(), priority.value.String(), priority.name)
		}
	}
	if invalid := NumberRoundingPriority(255); invalid.Valid() || invalid.String() != "unknown" {
		t.Fatalf("invalid rounding priority = (%t, %q)", invalid.Valid(), invalid.String())
	}

	trailing := []struct {
		value NumberTrailingZeroDisplay
		name  string
	}{
		{NumberTrailingZeroDefault, "default"},
		{NumberTrailingZeroAuto, "auto"},
		{NumberTrailingZeroStripIfInteger, "stripIfInteger"},
	}
	for _, display := range trailing {
		if !display.value.Valid() || display.value.String() != display.name {
			t.Fatalf("trailing zero display %d = (%t, %q), want (true, %q)", display.value, display.value.Valid(), display.value.String(), display.name)
		}
	}
	if invalid := NumberTrailingZeroDisplay(255); invalid.Valid() || invalid.String() != "unknown" {
		t.Fatalf("invalid trailing zero display = (%t, %q)", invalid.Valid(), invalid.String())
	}
}

func TestNumberRoundingIncrements(t *testing.T) {
	increments := []NumberRoundingIncrement{
		NumberRoundingIncrementDefault,
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
		NumberRoundingIncrement5000,
	}
	view := mustView(t, directCoreSnapshot(t, nil, Limits{}, nil), "en", "", "", PresentationNoIsolation)
	for _, increment := range increments {
		want := "default"
		if increment != NumberRoundingIncrementDefault {
			want = increment.String()
		}
		if !increment.Valid() || increment.String() != want {
			t.Fatalf("rounding increment %d = (%t, %q), want (true, %q)", increment, increment.Valid(), increment.String(), want)
		}
		spec := NumberFormatSpec{RoundingIncrement: increment}
		if increment > NumberRoundingIncrement1 {
			spec.FractionDigits = Digits(4, 4)
		}
		formatted, err := view.FormatNumber(context.Background(), DecimalNumber("1.23456"), spec)
		if err != nil {
			t.Fatalf("increment %d: %v", increment, err)
		}
		directCoreValuePart(t, formatted, "number")
	}
	for _, invalid := range []NumberRoundingIncrement{3, 4999, 5001, ^NumberRoundingIncrement(0)} {
		if invalid.Valid() || invalid.String() != "unknown" {
			t.Fatalf("invalid rounding increment %d = (%t, %q)", invalid, invalid.Valid(), invalid.String())
		}
	}
}

func TestNumberRoundingModesUseExactDecimals(t *testing.T) {
	view := mustView(t, directCoreSnapshot(t, nil, Limits{}, nil), "en", "", "", PresentationNoIsolation)
	tests := []struct {
		name  string
		mode  NumberRoundingMode
		value DecimalNumber
		want  string
	}{
		{"ceil", NumberRoundingModeCeil, "1.21", "1.3"},
		{"floor", NumberRoundingModeFloor, "1.29", "1.2"},
		{"expand", NumberRoundingModeExpand, "-1.21", "-1.3"},
		{"trunc", NumberRoundingModeTrunc, "-1.29", "-1.2"},
		{"half ceil", NumberRoundingModeHalfCeil, "1.25", "1.3"},
		{"half floor", NumberRoundingModeHalfFloor, "1.25", "1.2"},
		{"half expand", NumberRoundingModeHalfExpand, "-1.25", "-1.3"},
		{"half trunc", NumberRoundingModeHalfTrunc, "-1.25", "-1.2"},
		{"half even", NumberRoundingModeHalfEven, "1.25", "1.2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			formatted, err := view.FormatNumber(context.Background(), test.value, NumberFormatSpec{
				Grouping:       NumberGroupingNever,
				RoundingMode:   test.mode,
				FractionDigits: Digits(1, 1),
			})
			if err != nil {
				t.Fatal(err)
			}
			if formatted.Text != test.want {
				t.Fatalf("formatted = %q, want %q", formatted.Text, test.want)
			}
			directCoreValuePart(t, formatted, "number")
		})
	}
}

func TestNumberRoundingPriorityAndTrailingZeros(t *testing.T) {
	view := mustView(t, directCoreSnapshot(t, nil, Limits{}, nil), "en", "", "", PresentationNoIsolation)
	base := NumberFormatSpec{
		Grouping:          NumberGroupingNever,
		FractionDigits:    Digits(2, 2),
		SignificantDigits: Digits(2, 4),
	}
	more := base
	more.RoundingPriority = NumberRoundingPriorityMorePrecision
	less := base
	less.RoundingPriority = NumberRoundingPriorityLessPrecision
	for _, test := range []struct {
		name string
		spec NumberFormatSpec
		want string
	}{
		{"more precision", more, "1.235"},
		{"less precision", less, "1.23"},
	} {
		t.Run(test.name, func(t *testing.T) {
			formatted, err := view.FormatNumber(context.Background(), DecimalNumber("1.2345"), test.spec)
			if err != nil {
				t.Fatal(err)
			}
			if formatted.Text != test.want {
				t.Fatalf("formatted = %q, want %q", formatted.Text, test.want)
			}
			directCoreValuePart(t, formatted, "number")
		})
	}

	stripped, err := view.FormatNumber(context.Background(), SignedNumber(1), NumberFormatSpec{
		Grouping:            NumberGroupingNever,
		FractionDigits:      Digits(2, 2),
		TrailingZeroDisplay: NumberTrailingZeroStripIfInteger,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stripped.Text != "1" {
		t.Fatalf("stripped integer = %q, want 1", stripped.Text)
	}
	directCoreValuePart(t, stripped, "number")
}

func TestNumberRoundingIncrementAndBigIntegerRemainExact(t *testing.T) {
	view := mustView(t, directCoreSnapshot(t, nil, Limits{}, nil), "en", "", "", PresentationNoIsolation)
	formatted, err := view.FormatNumber(context.Background(), DecimalNumber("1.23"), NumberFormatSpec{
		Grouping:          NumberGroupingNever,
		FractionDigits:    Digits(2, 2),
		RoundingIncrement: NumberRoundingIncrement5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if formatted.Text != "1.25" {
		t.Fatalf("increment result = %q, want 1.25", formatted.Text)
	}
	directCoreValuePart(t, formatted, "number")

	integer, ok := new(big.Int).SetString("1234567890123456789012345678901234567890", 10)
	if !ok {
		t.Fatal("failed to construct big integer")
	}
	formatted, err = view.FormatNumber(context.Background(), NewBigIntegerNumber(integer), NumberFormatSpec{
		Grouping:       NumberGroupingNever,
		RoundingMode:   NumberRoundingModeHalfEven,
		FractionDigits: Digits(0, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if formatted.Text != integer.String() {
		t.Fatalf("big integer = %q, want %q", formatted.Text, integer)
	}
	directCoreValuePart(t, formatted, "number")
}

func TestNumberRoundingFailsClosedBeforeFormatting(t *testing.T) {
	invalid := []NumberFormatSpec{
		{RoundingMode: NumberRoundingMode(255)},
		{RoundingPriority: NumberRoundingPriority(255)},
		{TrailingZeroDisplay: NumberTrailingZeroDisplay(255)},
		{RoundingIncrement: 3},
		{RoundingIncrement: NumberRoundingIncrement5},
		{RoundingIncrement: NumberRoundingIncrement5, FractionDigits: Digits(1, 2)},
		{RoundingIncrement: NumberRoundingIncrement5, FractionDigits: Digits(2, 2), SignificantDigits: Digits(1, 3)},
		{RoundingIncrement: NumberRoundingIncrement5, FractionDigits: Digits(2, 2), RoundingPriority: NumberRoundingPriorityMorePrecision},
	}
	for index, spec := range invalid {
		if options, err := directNumberOptions(spec); !errors.Is(err, ErrInvalidMessage) || options != nil {
			t.Fatalf("invalid spec %d = (%#v, %v), want nil ErrInvalidMessage", index, options, err)
		}
	}

	options, err := directNumberOptions(NumberFormatSpec{
		RoundingIncrement: NumberRoundingIncrement1,
		SignificantDigits: Digits(1, 3),
		RoundingPriority:  NumberRoundingPriorityMorePrecision,
	})
	if err != nil || options["roundingIncrement"] != 1 {
		t.Fatalf("increment one = (%#v, %v)", options, err)
	}
	options, err = directNumberOptions(NumberFormatSpec{
		RoundingIncrement: NumberRoundingIncrement5,
		FractionDigits:    Digits(2, 2),
		RoundingPriority:  NumberRoundingPriorityAuto,
	})
	if err != nil || options["roundingPriority"] != "auto" {
		t.Fatalf("explicit auto priority = (%#v, %v)", options, err)
	}
}

func TestIndependentDigitBoundsStayClosed(t *testing.T) {
	minimum := MinimumDigits(2)
	if minimum.Minimum() != 2 || minimum.Maximum() != 0 {
		t.Fatalf("minimum digits = %d..%d", minimum.Minimum(), minimum.Maximum())
	}
	options, err := directNumberOptions(NumberFormatSpec{FractionDigits: minimum})
	if err != nil || options["minimumFractionDigits"] != 2 {
		t.Fatalf("minimum fraction options = %#v, %v", options, err)
	}
	if _, exists := options["maximumFractionDigits"]; exists {
		t.Fatalf("minimum fraction unexpectedly set maximum: %#v", options)
	}

	maximum := MaximumDigits(4)
	if maximum.Minimum() != 0 || maximum.Maximum() != 4 {
		t.Fatalf("maximum digits = %d..%d", maximum.Minimum(), maximum.Maximum())
	}
	options, err = directNumberOptions(NumberFormatSpec{SignificantDigits: maximum})
	if err != nil || options["maximumSignificantDigits"] != 4 {
		t.Fatalf("maximum significant options = %#v, %v", options, err)
	}
	if _, exists := options["minimumSignificantDigits"]; exists {
		t.Fatalf("maximum significant unexpectedly set minimum: %#v", options)
	}

	invalid := []NumberFormatSpec{
		{FractionDigits: MinimumDigits(-1)},
		{FractionDigits: MaximumDigits(-1)},
		{SignificantDigits: MinimumDigits(0)},
		{SignificantDigits: MaximumDigits(0)},
		{RoundingIncrement: NumberRoundingIncrement5, FractionDigits: MinimumDigits(2)},
		{RoundingIncrement: NumberRoundingIncrement5, FractionDigits: MaximumDigits(2)},
	}
	for index, spec := range invalid {
		if options, err := directNumberOptions(spec); !errors.Is(err, ErrInvalidMessage) || options != nil {
			t.Fatalf("invalid independent bound %d = %#v, %v", index, options, err)
		}
	}
}

func TestNumberRoundingSpecIsImmutableAndConcurrent(t *testing.T) {
	view := mustView(t, directCoreSnapshot(t, nil, Limits{}, nil), "en", "", "", PresentationNoIsolation)
	spec := NumberFormatSpec{
		Grouping:            NumberGroupingNever,
		RoundingMode:        NumberRoundingModeHalfEven,
		TrailingZeroDisplay: NumberTrailingZeroStripIfInteger,
		FractionDigits:      Digits(2, 2),
	}
	want := spec
	const workers = 32
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			formatted, err := view.FormatNumber(context.Background(), DecimalNumber("42.00"), spec)
			if err != nil || formatted.Text != "42" || formatted.Text != joinPartText(formatted.Parts) {
				t.Errorf("concurrent formatting = %#v, %v", formatted, err)
			}
		}()
	}
	wait.Wait()
	if spec != want {
		t.Fatalf("spec mutated: %#v, want %#v", spec, want)
	}
}

func TestNumberRoundingPreflightOneLessBounds(t *testing.T) {
	input := numericInput{kind: numericDecimal, decimal: "1.23"}
	spec := NumberFormatSpec{
		Grouping:          NumberGroupingNever,
		RoundingMode:      NumberRoundingModeHalfEven,
		FractionDigits:    Digits(2, 2),
		RoundingIncrement: NumberRoundingIncrement5,
	}
	options, err := directNumberOptions(spec)
	if err != nil {
		t.Fatal(err)
	}
	maximumBytes, err := numericOutputBytes(input, "number", options)
	if err != nil {
		t.Fatal(err)
	}
	maximumParts, err := numericOutputParts(input, "number", options)
	if err != nil {
		t.Fatal(err)
	}
	limits := DefaultLimits()
	limits.MaxOutputBytes = maximumBytes
	limits.MaxOutputParts = maximumParts
	view := mustView(t, directCoreSnapshot(t, nil, limits, nil), "en", "", "", PresentationNoIsolation)
	if _, err := view.FormatNumber(context.Background(), DecimalNumber("1.23"), spec); err != nil {
		t.Fatalf("at exact preflight bound: %v", err)
	}

	byteLimits := limits
	byteLimits.MaxOutputBytes--
	view = mustView(t, directCoreSnapshot(t, nil, byteLimits, nil), "en", "", "", PresentationNoIsolation)
	if _, err := view.FormatNumber(context.Background(), DecimalNumber("1.23"), spec); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("one-less byte error = %v", err)
	}

	partLimits := limits
	partLimits.MaxOutputParts--
	view = mustView(t, directCoreSnapshot(t, nil, partLimits, nil), "en", "", "", PresentationNoIsolation)
	if _, err := view.FormatNumber(context.Background(), DecimalNumber("1.23"), spec); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("one-less part error = %v", err)
	}
}
