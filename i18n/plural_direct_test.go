package i18n

import (
	"context"
	"errors"
	"math/big"
	"testing"
)

func TestDirectPluralSelectionLocalesExactValuesAndRounding(t *testing.T) {
	tests := []struct {
		locale string
		value  NumericValue
		spec   PluralFormatSpec
		want   PluralCategory
	}{
		{locale: "ru", value: SignedNumber(1), want: PluralOne},
		{locale: "ru", value: SignedNumber(2), want: PluralFew},
		{locale: "ru", value: SignedNumber(5), want: PluralMany},
		{locale: "ru", value: DecimalNumber("1.5"), want: PluralOther},
		{locale: "ar", value: SignedNumber(0), want: PluralZero},
		{locale: "ar", value: SignedNumber(2), want: PluralTwo},
		{locale: "ar", value: SignedNumber(3), want: PluralFew},
		{locale: "ar", value: SignedNumber(11), want: PluralMany},
		{locale: "en", value: SignedNumber(1), spec: PluralFormatSpec{Type: PluralOrdinal}, want: PluralOne},
		{locale: "en", value: SignedNumber(2), spec: PluralFormatSpec{Type: PluralOrdinal}, want: PluralTwo},
		{locale: "en", value: SignedNumber(3), spec: PluralFormatSpec{Type: PluralOrdinal}, want: PluralFew},
		{locale: "en", value: SignedNumber(4), spec: PluralFormatSpec{Type: PluralOrdinal}, want: PluralOther},
		{locale: "en", value: DecimalNumber("1.2"), want: PluralOther},
		{locale: "en", value: DecimalNumber("1.2"), spec: PluralFormatSpec{Number: NumberFormatSpec{FractionDigits: Digits(0, 0)}}, want: PluralOne},
	}
	for _, test := range tests {
		formatter, err := NewFormatter(FormatterSpec{Locale: test.locale})
		if err != nil {
			t.Fatal(err)
		}
		category, err := formatter.SelectPlural(context.Background(), test.value, test.spec)
		if err != nil {
			t.Fatalf("%s %v: %v", test.locale, test.value, err)
		}
		if category != test.want {
			t.Fatalf("%s %v = %s, want %s", test.locale, test.value, category, test.want)
		}
	}
	large := new(big.Int).Exp(big.NewInt(10), big.NewInt(200), nil)
	formatter, err := NewFormatter(FormatterSpec{Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if category, err := formatter.SelectPlural(context.Background(), NewBigIntegerNumber(large), PluralFormatSpec{}); err != nil || category != PluralOther {
		t.Fatalf("big integer category = %s, %v", category, err)
	}
}

func TestDirectPluralRangeAndNamedRegistry(t *testing.T) {
	formatter, err := NewFormatter(FormatterSpec{Locale: "cs", Formats: Formats{Plurals: []NamedPluralFormat{{Name: "items.cardinal"}}}})
	if err != nil {
		t.Fatal(err)
	}
	category, err := formatter.SelectNamedPluralRange(context.Background(), "items.cardinal", SignedNumber(1), SignedNumber(2))
	if err != nil || category != PluralFew {
		t.Fatalf("Czech range = %s, %v", category, err)
	}
	category, err = formatter.SelectPluralRange(context.Background(), SignedNumber(2), SignedNumber(1), PluralFormatSpec{})
	if err != nil || category != PluralOther {
		t.Fatalf("reversed Czech range = %s, %v", category, err)
	}
	if _, err := formatter.SelectNamedPlural(context.Background(), "missing", SignedNumber(1)); !errors.Is(err, ErrFormatNotFound) {
		t.Fatalf("missing plural format = %v", err)
	}
}

func TestDirectPluralValidationCancellationAndLimits(t *testing.T) {
	formatter, err := NewFormatter(FormatterSpec{Locale: "en", Limits: Limits{MaxBigIntegerBits: 8}})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := formatter.SelectPlural(canceled, SignedNumber(1), PluralFormatSpec{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	large := new(big.Int).Lsh(big.NewInt(1), 9)
	if _, err := formatter.SelectPlural(context.Background(), NewBigIntegerNumber(large), PluralFormatSpec{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("big integer limit error = %v", err)
	}
	invalid := []PluralFormatSpec{
		{Type: PluralType(255)},
		{Number: NumberFormatSpec{Grouping: NumberGroupingAlways}},
		{Number: NumberFormatSpec{Sign: NumberSignAlways}},
		{Number: NumberFormatSpec{NumberingSystem: "latn"}},
		{Number: NumberFormatSpec{Compact: NumberCompactLong}},
		{Number: NumberFormatSpec{RoundingIncrement: NumberRoundingIncrement25}},
	}
	for _, spec := range invalid {
		if _, err := formatter.SelectPlural(context.Background(), SignedNumber(1), spec); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("invalid plural spec %#v: %v", spec, err)
		}
	}
}

func TestPluralClosedEnums(t *testing.T) {
	types := []struct {
		value PluralType
		name  string
	}{{PluralCardinal, "cardinal"}, {PluralOrdinal, "ordinal"}}
	for _, value := range types {
		if !value.value.Valid() || value.value.String() != value.name {
			t.Fatalf("plural type %d = %q", value.value, value.value.String())
		}
	}
	categories := []struct {
		value PluralCategory
		name  string
	}{{PluralZero, "zero"}, {PluralOne, "one"}, {PluralTwo, "two"}, {PluralFew, "few"}, {PluralMany, "many"}, {PluralOther, "other"}}
	for _, value := range categories {
		if !value.value.Valid() || value.value.String() != value.name {
			t.Fatalf("plural category %d = %q", value.value, value.value.String())
		}
	}
	if PluralType(255).Valid() || PluralCategory(255).Valid() || PluralType(255).String() != "unknown" || PluralCategory(255).String() != "unknown" {
		t.Fatal("invalid plural enum accepted")
	}
}
