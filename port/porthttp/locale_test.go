package porthttp

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/port"
)

func TestALocaleSetByOneTransportIsReadByAnother(t *testing.T) {
	if got := LocaleFrom(port.WithLocale(context.Background(), "fr-CA")); got != "fr-CA" {
		t.Fatalf("a locale set through port reads as %q here; the two packages have different keys", got)
	}
	if got := port.LocaleFrom(WithLocale(context.Background(), "fr-CA")); got != "fr-CA" {
		t.Fatalf("a locale set here reads as %q through port; the two packages have different keys", got)
	}

	if got := LocaleFrom(context.Background()); got != "" {
		t.Fatalf("a context with no locale reads as %q", got)
	}
	if got := port.LocaleFrom(context.Background()); got != "" {
		t.Fatalf("a context with no locale reads as %q through port", got)
	}
}

func TestAcceptLanguageReadsTheFirstTag(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{"en-GB,en;q=0.9", "en-GB"},
		{" fr ; q=1", "fr"},
		{"de", "de"},
		{"", ""},
		{",en", ""},
	} {
		if got := AcceptLanguage(tc.header); got != tc.want {
			t.Fatalf("Accept-Language %q read as %q, want %q", tc.header, got, tc.want)
		}
	}
}
