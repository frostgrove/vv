package errs_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
)

func taken() errs.Violation {
	return errs.Violation{
		Path:   errs.Path{errs.Named("user"), errs.Named("email")},
		Code:   errs.CodeUnique,
		Origin: errs.OriginState,
	}
}

func TestEachLevelOfTheMessageLadderResolves(t *testing.T) {
	const (
		narrow  = "that address is already registered for this account"
		byFirst = "something about this user is already taken"
		byLast  = "that address is already registered"
		byCode  = "that value is already taken"
	)

	for _, tc := range []struct {
		name     string
		register map[string]string
		want     string
	}{
		{"only the narrowest key", map[string]string{"user.email.unique": narrow}, narrow},
		{"only the first step", map[string]string{"user.unique": byFirst}, byFirst},
		{"only the last step", map[string]string{"email.unique": byLast}, byLast},
		{"only the bare code", map[string]string{"unique": byCode}, byCode},

		{"all four", map[string]string{
			"user.email.unique": narrow,
			"user.unique":       byFirst,
			"email.unique":      byLast,
			"unique":            byCode,
		}, narrow},

		{"nothing at all", nil, "this value is already taken"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := errs.NewMessages(errs.StandardCodes())
			for k, v := range tc.register {
				if err := m.Add("en", k, v); err != nil {
					t.Fatal(err)
				}
			}
			got, ok := m.Message(context.Background(), taken(), "en")
			if !ok {
				t.Fatalf("nothing resolved, and the code's own default should have")
			}
			if got != tc.want {
				t.Fatalf("resolved %q, want %q", got, tc.want)
			}
		})
	}
}

func TestATemplateWithAMissingParamFallsBackRatherThanEmittingThePlaceholder(t *testing.T) {
	const (
		narrow = "at most {max} characters"
		broad  = "that value is too long"
	)
	m := errs.NewMessages(errs.StandardCodes())
	for k, v := range map[string]string{"user.email.too_long": narrow, "too_long": broad} {
		if err := m.Add("en", k, v); err != nil {
			t.Fatal(err)
		}
	}
	v := errs.Violation{Path: errs.Path{errs.Named("user"), errs.Named("email")}, Code: errs.CodeTooLong}

	got, ok := m.Message(context.Background(), v, "en")
	if !ok {
		t.Fatalf("nothing resolved; the broader template should have")
	}
	if got != broad {
		t.Fatalf("resolved %q, want the broader %q", got, broad)
	}
	if strings.Contains(got, "{") {
		t.Fatalf("the message carries a placeholder: %q", got)
	}

	v.Params = errs.P{"max": 255}
	got, ok = m.Message(context.Background(), v, "en")
	if !ok || got != "at most 255 characters" {
		t.Fatalf("with the parameter present the narrow template resolved to (%q, %v)", got, ok)
	}
}

func TestAMessageExpandsByteIdenticallyEveryTime(t *testing.T) {
	m := errs.NewMessages(errs.StandardCodes())
	if err := m.Add("en", "check", "between {min} and {max}, and not {forbidden}"); err != nil {
		t.Fatal(err)
	}
	v := errs.Violation{
		Path: errs.Path{errs.Named("user"), errs.Named("age")},
		Code: errs.CodeCheck,
		Params: errs.P{
			"min": 18, "max": 120, "forbidden": 42,
			"column": "age", "table": "users", "constraint": "users_age_check",
			"sqlstate": "23514", "value": 15,
		},
	}

	first, ok := m.Message(context.Background(), v, "en")
	if !ok {
		t.Fatalf("the template did not resolve")
	}
	if first != "between 18 and 120, and not 42" {
		t.Fatalf("the template expanded to %q", first)
	}
	for i := 0; i < 50; i++ {
		again, _ := m.Message(context.Background(), v, "en")
		if again != first {
			t.Fatalf("run %d produced %q, run 0 produced %q", i, again, first)
		}
	}

	for _, name := range []string{"age", "users", "users_age_check", "23514", "15"} {
		if strings.Contains(first, name) {
			t.Fatalf("the message carries %q, which the template never named", name)
		}
	}
}

func TestTwoLocalesThroughTheSameFaultGiveTwoMessages(t *testing.T) {
	m := errs.NewMessages(errs.StandardCodes())
	for _, d := range []struct{ locale, key, text string }{
		{"en", "user.email.unique", "that address is already registered"},
		{"fr", "user.email.unique", "cette adresse est déjà enregistrée"},
		{"", "user.email.unique", "email taken"},
	} {
		if err := m.Add(d.locale, d.key, d.text); err != nil {
			t.Fatal(err)
		}
	}

	f := errs.Conflict().At(taken().Path).Code(errs.CodeUnique).Fault()
	v := f.Violations[0]

	en, _ := m.Message(context.Background(), v, "en")
	fr, _ := m.Message(context.Background(), v, "fr")
	if en == fr {
		t.Fatalf("both locales resolved to %q, so the locale is not being read", en)
	}
	if en != "that address is already registered" || fr != "cette adresse est déjà enregistrée" {
		t.Fatalf("resolved en=%q fr=%q", en, fr)
	}

	if got, _ := m.Message(context.Background(), v, "en-GB"); got != en {
		t.Fatalf("en-GB resolved %q; with no catalogue of its own it falls back through en", got)
	}

	if got, _ := m.Message(context.Background(), v, "de"); got != "email taken" {
		t.Fatalf("an unknown locale resolved %q, want the default catalogue's text", got)
	}
	bare := errs.NewMessages(errs.StandardCodes())
	if got, _ := bare.Message(context.Background(), v, "de"); got != "this value is already taken" {
		t.Fatalf("with no catalogue at all the code's default should answer; got %q", got)
	}
}

func TestAnIndexedPathResolvesTheSameMessageAsAnyOtherRow(t *testing.T) {
	m := errs.NewMessages(errs.StandardCodes())
	for _, d := range []struct{ key, text string }{
		{"items.email.unique", "that address is already on the list"},
		{"orders.email.unique", "that address already ordered"},
	} {
		if err := m.Add("en", d.key, d.text); err != nil {
			t.Fatal(err)
		}
	}
	at := func(root string, i int) errs.Violation {
		return errs.Violation{
			Path: errs.Path{errs.Named(root), errs.Indexed(i), errs.Named("email")},
			Code: errs.CodeUnique,
		}
	}

	three, _ := m.Message(context.Background(), at("items", 3), "en")
	seven, _ := m.Message(context.Background(), at("items", 7), "en")
	if three != seven {
		t.Fatalf("row 3 resolved %q and row 7 resolved %q — a position is not a message scope", three, seven)
	}
	if three != "that address is already on the list" {
		t.Fatalf("resolved %q", three)
	}

	other, _ := m.Message(context.Background(), at("orders", 3), "en")
	if other == three {
		t.Fatalf("items[3].email and orders[3].email both resolved %q, so the path is not being read", other)
	}
}

func TestRedeclaringAMessageWithDifferentTextIsRefused(t *testing.T) {
	m := errs.NewMessages(errs.StandardCodes())
	if err := m.Add("en", "unique", "that value is already taken"); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("en", "unique", "that value is already taken"); err != nil {
		t.Fatalf("declaring the same text twice was refused: %v", err)
	}
	err := m.Add("en", "unique", "something else")
	if !errors.Is(err, errs.ErrMessageRedeclared) {
		t.Fatalf("a second, disagreeing template was accepted: %v", err)
	}
	if got, _ := m.Message(context.Background(), taken(), "en"); got != "that value is already taken" {
		t.Fatalf("the refused declaration changed the catalogue to %q", got)
	}
}

func TestAPOSIXLocaleFallsBackTheSameWayAHyphenatedOneDoes(t *testing.T) {
	m := errs.NewMessages(errs.StandardCodes())
	if err := m.Add("en", "user.email.unique", "that address is already registered"); err != nil {
		t.Fatal(err)
	}

	for _, locale := range []string{"en-GB", "en_GB"} {
		got, ok := m.Message(context.Background(), taken(), locale)
		if !ok || got != "that address is already registered" {
			t.Fatalf("%s resolved (%q, %v); with no catalogue of its own it falls back through en", locale, got, ok)
		}
	}

	for _, locale := range []string{"de-DE", "de_DE"} {
		if got, _ := m.Message(context.Background(), taken(), locale); got != "this value is already taken" {
			t.Fatalf("%s resolved %q, want the code's own default", locale, got)
		}
	}
}

func TestALocaleIsWalkedBeforeAKeyIsNarrowed(t *testing.T) {
	m := errs.NewMessages(errs.StandardCodes())
	if err := m.Add("fr", "unique", "cette valeur est déjà prise"); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("", "user.email.unique", "that address is already registered"); err != nil {
		t.Fatal(err)
	}

	if got, _ := m.Message(context.Background(), taken(), "fr"); got != "cette valeur est déjà prise" {
		t.Fatalf("resolved %q — the broad French entry outranks the narrow default one", got)
	}

	if got, _ := m.Message(context.Background(), taken(), "de"); got != "that address is already registered" {
		t.Fatalf("a locale with nothing of its own resolved %q, so the default catalogue's narrow key is unreachable and the test above proves nothing", got)
	}
}

func TestOnlyTheFirstAndLastNamedStepsReachTheLadder(t *testing.T) {
	m := errs.NewMessages(errs.StandardCodes())
	for _, d := range []struct{ key, text string }{
		{"order.items.email.unique", "the whole path"},
		{"order.email.unique", "the two ends"},
	} {
		if err := m.Add("en", d.key, d.text); err != nil {
			t.Fatal(err)
		}
	}
	v := errs.Violation{
		Path: errs.Path{errs.Named("order"), errs.Named("items"), errs.Named("email")},
		Code: errs.CodeUnique,
	}

	got, ok := m.Message(context.Background(), v, "en")
	if !ok {
		t.Fatalf("nothing resolved at all")
	}
	if got != "the two ends" {
		t.Fatalf("resolved %q, want the first-and-last key — a key spelling the whole path is never consulted", got)
	}

	bare := errs.NewMessages(errs.StandardCodes())
	if err := bare.Add("en", "order.items.email.unique", "the whole path"); err != nil {
		t.Fatal(err)
	}
	if got, _ := bare.Message(context.Background(), v, "en"); got != "this value is already taken" {
		t.Fatalf("the full dotted key resolved %q, so it is reachable after all and this test is measuring the wrong thing", got)
	}
}
