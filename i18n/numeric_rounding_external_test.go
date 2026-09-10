package i18n_test

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/i18n"
)

func TestNumberRoundingPublicDeclarativeDX(t *testing.T) {
	snapshot, err := i18n.New(i18n.CatalogSpec{
		Revision:        "rounding-dx",
		SourceLocale:    "en",
		DefaultLocale:   "en",
		Supported:       []string{"en"},
		DefaultTimeZone: "UTC",
		Modules: []i18n.Module{{Name: "app", Messages: []i18n.MessageSpec{{
			ID: "anchor", Revision: "1", Description: "Catalog anchor", Source: "ready", Output: i18n.OutputPlain,
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := snapshot.View(i18n.ViewSpec{
		Resolution:   snapshot.Resolve(i18n.Exact(i18n.SourceExplicit, "en")),
		Presentation: i18n.PresentationNoIsolation,
	})
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := view.FormatNumber(context.Background(), i18n.DecimalNumber("1.23"), i18n.NumberFormatSpec{
		Grouping:            i18n.NumberGroupingNever,
		RoundingMode:        i18n.NumberRoundingModeHalfEven,
		RoundingPriority:    i18n.NumberRoundingPriorityAuto,
		TrailingZeroDisplay: i18n.NumberTrailingZeroAuto,
		RoundingIncrement:   i18n.NumberRoundingIncrement5,
		FractionDigits:      i18n.Digits(2, 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if formatted.Text != "1.25" || len(formatted.Parts) != 1 || formatted.Parts[0].Text != formatted.Text {
		t.Fatalf("formatted = %#v", formatted)
	}
}

func TestIndependentFractionDigitDXPreservesCurrencyDefaults(t *testing.T) {
	formatter, err := i18n.NewFormatter(i18n.FormatterSpec{
		Locale:       "en",
		Presentation: i18n.PresentationNoIsolation,
		Formats: i18n.Formats{Money: []i18n.NamedMoneyFormat{{
			Name: "money.flexible",
			Spec: i18n.MoneyFormatSpec{Number: i18n.NumberFormatSpec{
				Grouping:       i18n.NumberGroupingNever,
				FractionDigits: i18n.MinimumDigits(0),
			}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	usd, err := formatter.FormatNamedMoney(context.Background(), "money.flexible", i18n.MoneyValue{Amount: "1.234", Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	jpy, err := formatter.FormatNamedMoney(context.Background(), "money.flexible", i18n.MoneyValue{Amount: "1.234", Currency: "JPY"})
	if err != nil {
		t.Fatal(err)
	}
	if usd.Text != "$1.23" || jpy.Text != "¥1" {
		t.Fatalf("currency-dependent defaults = %q, %q", usd.Text, jpy.Text)
	}
	maximum, err := formatter.FormatNumber(context.Background(), i18n.DecimalNumber("1.25"), i18n.NumberFormatSpec{
		Grouping:       i18n.NumberGroupingNever,
		FractionDigits: i18n.MaximumDigits(1),
	})
	if err != nil || maximum.Text != "1.3" {
		t.Fatalf("maximum-only fraction digits = %#v, %v", maximum, err)
	}
}
