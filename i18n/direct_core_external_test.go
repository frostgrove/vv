package i18n_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/frostgrove/vv/i18n"
)

func TestDirectCorePublicDXNeedsNoSyntheticMessageKey(t *testing.T) {
	snapshot, err := i18n.New(i18n.CatalogSpec{
		Revision:            "direct-dx",
		SourceLocale:        "en",
		DefaultLocale:       "en",
		Supported:           []string{"en"},
		DefaultTimeZone:     "UTC",
		TimeZoneDataVersion: "test-tzdb",
		Capabilities:        []i18n.Capability{i18n.CapabilityDateTime, i18n.CapabilityUnit},
		Modules: []i18n.Module{{Name: "app", Messages: []i18n.MessageSpec{{
			ID: "anchor", Revision: "1", Description: "Catalog anchor", Source: "ready", Output: i18n.OutputPlain,
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := snapshot.View(i18n.ViewSpec{Resolution: snapshot.Resolve(i18n.Exact(i18n.SourceExplicit, "en")), Presentation: i18n.PresentationNoIsolation})
	if err != nil {
		t.Fatal(err)
	}

	integer, ok := new(big.Int).SetString("123456789012345678901234567890", 10)
	if !ok {
		t.Fatal("failed to construct bigint")
	}
	number, err := view.FormatNumber(context.Background(), i18n.NewBigIntegerNumber(integer), i18n.NumberFormatSpec{Grouping: i18n.NumberGroupingNever})
	if err != nil {
		t.Fatal(err)
	}
	if number.Text != "123456789012345678901234567890" || len(number.Parts) != 1 || number.Parts[0].Type != "number" || len(number.Parts[0].Subparts) == 0 {
		t.Fatalf("number = %#v", number)
	}
	date, err := view.FormatDate(context.Background(), i18n.DateValue{Year: 2026, Month: time.September, Day: 9}, i18n.DateFormatSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if date.Text != "Sep 9, 2026" || len(date.Parts) != 1 || date.Parts[0].Type != "datetime" || len(date.Parts[0].Subparts) == 0 {
		t.Fatalf("date = %#v", date)
	}
}
