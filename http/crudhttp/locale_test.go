package crudhttp

import (
	"context"
	"testing"

	"github.com/shardit-io/vv/port"
)

// The locale key belongs to port, so a locale set by one transport is read by
// another. This is the test that catches the bug the move can introduce: a
// second context key left behind here would be invisible to a gRPC renderer and
// vice versa, and both packages' own suites would still pass.
func TestALocaleSetByOneTransportIsReadByAnother(t *testing.T) {
	if got := LocaleFrom(port.WithLocale(context.Background(), "fr-CA")); got != "fr-CA" {
		t.Fatalf("a locale set through port reads as %q here; the two packages have different keys", got)
	}
	if got := port.LocaleFrom(WithLocale(context.Background(), "fr-CA")); got != "fr-CA" {
		t.Fatalf("a locale set here reads as %q through port; the two packages have different keys", got)
	}

	// The control: with nothing set, both answer the empty string — so the two
	// assertions above are about the key rather than about a reader that
	// returns whatever it was asked for.
	if got := LocaleFrom(context.Background()); got != "" {
		t.Fatalf("a context with no locale reads as %q", got)
	}
	if got := port.LocaleFrom(context.Background()); got != "" {
		t.Fatalf("a context with no locale reads as %q through port", got)
	}
}

// The first tag only. q-values pick between translations this library does not
// have, and the ladder either finds the entry or falls through.
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
