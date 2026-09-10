package errs_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
)

type namedString string

func (namedString) String() string { panic("String must not be called") }

type namedBool bool
type namedInt int32
type namedUint uint16
type namedFloat float32

type panickingStringer struct{}

func (panickingStringer) String() string { panic("String must not be called") }

type panickingError struct{}

func (panickingError) Error() string { panic("Error must not be called") }

type panickingTextMarshaler struct{}

func (panickingTextMarshaler) MarshalText() ([]byte, error) { panic("MarshalText must not be called") }

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

func TestMessageExpansionAcceptsOnlyDeterministicScalars(t *testing.T) {
	m := errs.NewMessages(nil)
	if err := m.Add("en", "check", "{text}|{flag}|{signed}|{unsigned}|{decimal}"); err != nil {
		t.Fatal(err)
	}
	v := errs.Violation{Code: errs.CodeCheck, Params: errs.P{
		"text":     namedString("ready"),
		"flag":     namedBool(true),
		"signed":   namedInt(-42),
		"unsigned": namedUint(17),
		"decimal":  namedFloat(1.5),
	}}

	got, ok := m.Message(context.Background(), v, "en")
	if !ok || got != "ready|true|-42|17|1.5" {
		t.Fatalf("named scalar expansion = (%q, %v)", got, ok)
	}
}

func TestUnsafeMessageParametersDeclineToSafeWording(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"a Stringer", panickingStringer{}},
		{"an error", panickingError{}},
		{"a text marshaler", panickingTextMarshaler{}},
		{"a map", map[string]string{"private": "value"}},
		{"a slice", []string{"private"}},
		{"a pointer", new(int)},
		{"NaN", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"negative infinity", math.Inf(-1)},
		{"invalid UTF-8", namedString(string([]byte{0xff}))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := errs.NewMessages(nil)
			if err := m.Add("en", "user.check", "unsafe {value}"); err != nil {
				t.Fatal(err)
			}
			if err := m.Add("en", "check", "safe wording"); err != nil {
				t.Fatal(err)
			}
			v := errs.Violation{Path: errs.Path{errs.Named("user")}, Code: errs.CodeCheck, Params: errs.P{"value": tc.value}}
			if got, ok := m.Message(context.Background(), v, "en"); !ok || got != "safe wording" {
				t.Fatalf("unsafe value resolved to (%q, %v)", got, ok)
			}
		})
	}
}

func TestMessageExpansionHasAnExactHardByteBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		template string
		value    string
		want     int
		ok       bool
	}{
		{"a value at N", "{value}", strings.Repeat("x", errs.MaxMessageOutputBytes), errs.MaxMessageOutputBytes, true},
		{"a value at N minus one plus a literal", "!{value}", strings.Repeat("x", errs.MaxMessageOutputBytes-1), errs.MaxMessageOutputBytes, true},
		{"one byte beyond N", "!{value}", strings.Repeat("x", errs.MaxMessageOutputBytes), 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := errs.NewMessages(nil)
			if err := m.Add("en", "check", tc.template); err != nil {
				t.Fatal(err)
			}
			got, ok := m.Message(context.Background(), errs.Violation{Code: errs.CodeCheck, Params: errs.P{"value": tc.value}}, "en")
			if ok != tc.ok || len(got) != tc.want {
				t.Fatalf("expansion = (%d bytes, %v), want (%d, %v)", len(got), ok, tc.want, tc.ok)
			}
		})
	}

	m := errs.NewMessages(nil)
	if err := m.Add("en", "user.check", "!{value}"); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("en", "check", "bounded fallback"); err != nil {
		t.Fatal(err)
	}
	v := errs.Violation{
		Path:   errs.Path{errs.Named("user")},
		Code:   errs.CodeCheck,
		Params: errs.P{"value": strings.Repeat("x", errs.MaxMessageOutputBytes)},
	}
	if got, ok := m.Message(context.Background(), v, "en"); !ok || got != "bounded fallback" {
		t.Fatalf("oversized narrow template resolved to (%q, %v)", got, ok)
	}
}

func TestVocabularyExpansionUsesTheSameSafeOutputContract(t *testing.T) {
	codes := errs.NewCodes()
	if err := codes.Add("product_check", errs.KindValidation, "{value}"); err != nil {
		t.Fatal(err)
	}
	v := errs.Violation{Code: "product_check"}

	v.Params = errs.P{"value": namedString(strings.Repeat("x", errs.MaxMessageOutputBytes))}
	if got, ok := codes.Message(context.Background(), v, ""); !ok || len(got) != errs.MaxMessageOutputBytes {
		t.Fatalf("exact vocabulary output = (%d bytes, %v)", len(got), ok)
	}
	v.Params = errs.P{"value": strings.Repeat("x", errs.MaxMessageOutputBytes+1)}
	if got, ok := codes.Message(context.Background(), v, ""); ok || got != "" {
		t.Fatalf("oversized vocabulary output = (%d bytes, %v)", len(got), ok)
	}
	v.Params = errs.P{"value": panickingStringer{}}
	if got, ok := codes.Message(context.Background(), v, ""); ok || got != "" {
		t.Fatalf("Stringer vocabulary output = (%q, %v)", got, ok)
	}
}

func TestCancelledMessageLookupsDeclineCatalogueAndVocabularyWording(t *testing.T) {
	codes := errs.NewCodes()
	if err := codes.Add("product_check", errs.KindValidation, "vocabulary wording"); err != nil {
		t.Fatal(err)
	}
	messages := errs.NewMessages(codes)
	if err := messages.Add("en", "product_check", "catalogue wording"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, stop := context.WithTimeout(context.Background(), 0)
	defer stop()
	v := errs.Violation{Code: "product_check"}

	for _, lookup := range []struct {
		name string
		ctx  context.Context
	}{{"cancelled", ctx}, {"expired", deadline}} {
		t.Run(lookup.name, func(t *testing.T) {
			if got, ok := messages.Message(lookup.ctx, v, "en"); ok || got != "" {
				t.Fatalf("catalogue lookup = (%q, %v)", got, ok)
			}
			if got, locale, ok := messages.MessageWithLocale(lookup.ctx, v, "en"); ok || got != "" || locale != "" {
				t.Fatalf("localized lookup = (%q, %q, %v)", got, locale, ok)
			}
			if got, ok := codes.Message(lookup.ctx, v, "en"); ok || got != "" {
				t.Fatalf("vocabulary lookup = (%q, %v)", got, ok)
			}
		})
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

func TestMessageWithLocaleReportsTheTemplateThatActuallyWon(t *testing.T) {
	m := errs.NewMessages(errs.StandardCodes())
	if err := m.Add("en", "unique", "already taken"); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("", "required", "required by default"); err != nil {
		t.Fatal(err)
	}

	message, locale, ok := m.MessageWithLocale(context.Background(), errs.Violation{Code: errs.CodeUnique}, "en-GB")
	if !ok || message != "already taken" || locale != "en" {
		t.Fatalf("en-GB resolved to (%q, %q, %v), want the en template", message, locale, ok)
	}
	message, locale, ok = m.MessageWithLocale(context.Background(), errs.Violation{Code: errs.CodeRequired}, "de")
	if !ok || message != "required by default" || locale != "" {
		t.Fatalf("de resolved to (%q, %q, %v), want the locale-neutral template", message, locale, ok)
	}
	message, locale, ok = m.MessageWithLocale(context.Background(), errs.Violation{Code: errs.CodeNotFound}, "fr")
	if !ok || message == "" || locale != "" {
		t.Fatalf("the code default resolved to (%q, %q, %v), want wording without a claimed locale", message, locale, ok)
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
	if err := m.Add("en", "order.email.unique", "the two ends"); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("en", "order.items.email.unique", "the whole path"); err != nil {
		t.Fatal(err)
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

	dotted := errs.NewMessages(errs.StandardCodes())
	if err := dotted.Add("en", "order.items.email.unique", "a literal dotted member"); err != nil {
		t.Fatalf("a reachable key containing a literal dot was refused: %v", err)
	}
	dottedPath := errs.Violation{
		Path: errs.Path{errs.Named("order.items"), errs.Named("email")},
		Code: errs.CodeUnique,
	}
	if got, ok := dotted.Message(context.Background(), dottedPath, "en"); !ok || got != "a literal dotted member" {
		t.Fatalf("the byte-identical dotted member key resolved (%q, %v)", got, ok)
	}

}
