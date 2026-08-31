package errs

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/fstest"
)

func files(entries map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, body := range entries {
		out["messages/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	return out
}

func load(t *testing.T, entries map[string]string) *Messages {
	t.Helper()
	m, err := LoadMessages(StandardCodes(), files(entries), "messages")
	if err != nil {
		t.Fatalf("loading the catalogue: %v", err)
	}
	return m
}

func say(t *testing.T, m *Messages, v Violation, locale string) string {
	t.Helper()
	s, _ := m.Message(context.Background(), v, locale)
	return s
}

func TestACatalogueLoadedFromFilesResolvesTheSameLadder(t *testing.T) {
	entries := map[string][2]string{
		"user.email.unique": {"", "that address is already registered"},
		"email.unique":      {"", "that address is taken"},
		"unique":            {"", "already taken"},
		"age.check":         {"fr", "trop jeune"},
	}

	loaded := load(t, map[string]string{
		"default.json": `{
			"user.email.unique": "that address is already registered",
			"email.unique": "that address is taken",
			"unique": "already taken"
		}`,
		"fr.json": `{"age.check": "trop jeune"}`,
	})

	declared := NewMessages(StandardCodes())
	for key, e := range entries {
		if err := declared.Add(e[0], key, e[1]); err != nil {
			t.Fatalf("declaring %q: %v", key, err)
		}
	}

	for _, tc := range []struct {
		name   string
		v      Violation
		locale string
	}{
		{"the narrowest key", Violation{Path: Path{Named("user"), Named("email")}, Code: CodeUnique}, ""},
		{"the last named step", Violation{Path: Path{Named("email")}, Code: CodeUnique}, ""},
		{"the code alone", Violation{Path: Path{Named("nickname")}, Code: CodeUnique}, ""},
		{"a locale of its own", Violation{Path: Path{Named("age")}, Code: CodeCheck}, "fr-CA"},
		{"nothing declared at all", Violation{Path: Path{Named("age")}, Code: CodeRequired}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, want := say(t, loaded, tc.v, tc.locale), say(t, declared, tc.v, tc.locale)
			if got != want {
				t.Fatalf("the loaded catalogue says %q and the declared one says %q", got, want)
			}
			if got == "" {
				t.Fatal("both said nothing, so agreeing proves nothing")
			}
		})
	}

	seen := map[string]bool{}
	for _, v := range []Violation{
		{Path: Path{Named("user"), Named("email")}, Code: CodeUnique},
		{Path: Path{Named("email")}, Code: CodeUnique},
		{Path: Path{Named("nickname")}, Code: CodeUnique},
	} {
		seen[say(t, loaded, v, "")] = true
	}
	if len(seen) != 3 {
		t.Fatalf("the three rungs answered %d distinct sentences: %v", len(seen), seen)
	}
}

func TestTwoFilesDisagreeingOnOneKeyAreRefused(t *testing.T) {
	_, err := LoadMessages(StandardCodes(), files(map[string]string{
		"en.json":    `{"email.unique": "that address is taken"}`,
		"en-GB.json": `{"email.unique": "that address is taken"}`,
	}), "messages")
	if err != nil {
		t.Fatalf("two files in different locales are not a redeclaration: %v", err)
	}

	if _, err := LoadMessages(StandardCodes(), files(map[string]string{
		"a-en.json": `{"email.unique": "that address is taken"}`,
		"b-en.json": `{"email.unique": "that address is taken"}`,
	}), "messages"); err != nil {
		t.Fatalf("two files agreeing on one key were refused: %v", err)
	}

	m := NewMessages(StandardCodes())
	err = m.Load(files(map[string]string{
		"default.json": `{"email.unique": "that address is taken"}`,
	}), "messages")
	if err != nil {
		t.Fatalf("the first load failed: %v", err)
	}
	err = m.Load(files(map[string]string{
		"default.json": `{"email.unique": "somebody beat you to it"}`,
	}), "messages")
	if !errors.Is(err, ErrMessageRedeclared) {
		t.Fatalf("a second, disagreeing template answered %v, want ErrMessageRedeclared", err)
	}
}

func TestANestedCatalogueFileIsRefused(t *testing.T) {
	_, err := LoadMessages(StandardCodes(), files(map[string]string{
		"default.json": `{"user": {"email": {"unique": "that address is taken"}}}`,
	}), "messages")
	if err == nil {
		t.Fatal("a nested file loaded; every key in it would be one nothing ever consults")
	}

	if _, err := LoadMessages(StandardCodes(), files(map[string]string{
		"default.json": `{"user.email.unique": "that address is taken"}`,
	}), "messages"); err != nil {
		t.Fatalf("the flat spelling was refused too: %v", err)
	}
}

func TestMissingNamesTheCodesWithNoTemplate(t *testing.T) {
	m := load(t, map[string]string{
		"default.json": `{"unique": "already taken"}`,
	})

	missing := m.Missing("")
	if len(missing) == 0 {
		t.Fatal("a catalogue with one entry and a forty-code vocabulary reports nothing missing")
	}
	for _, c := range missing {
		if c == CodeUnique {
			t.Fatalf("%q has a template and is still reported missing", c)
		}
	}

	for _, c := range missing {
		if err := m.Add("", string(c), "declared"); err != nil {
			t.Fatalf("declaring %q: %v", c, err)
		}
	}
	if left := m.Missing(""); len(left) != 0 {
		t.Fatalf("after declaring every one, %v is still reported missing", left)
	}

	base := load(t, map[string]string{"en.json": `{"unique": "already taken"}`})
	for _, c := range base.Missing("en-GB") {
		if c == CodeUnique {
			t.Fatal("a code en-GB resolves through en is reported missing from en-GB")
		}
	}

	var found bool
	for _, c := range base.Missing("fr") {
		found = found || c == CodeUnique
	}
	if !found {
		t.Fatal("fr does not report the code missing either, so en-GB not reporting it proves nothing")
	}
}

func TestLocalesNamesEveryFileThatDeclaredSomething(t *testing.T) {
	m := load(t, map[string]string{
		"default.json": `{"unique": "already taken"}`,
		"fr.json":      `{"unique": "deja pris"}`,
		"en-GB.json":   `{"unique": "already taken"}`,
		"notes.txt":    `not a catalogue file`,
	})
	if got, want := m.Locales(), []string{"", "en-GB", "fr"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the catalogue declares %v, want %v", got, want)
	}
}

func TestTheCatalogueLoadsInADeterministicOrder(t *testing.T) {
	fsys := files(map[string]string{
		"default.json": `{"unique": "already taken", "email.unique": "that address is taken", "check": "not allowed"}`,
		"fr.json":      `{"unique": "deja pris"}`,
		"en.json":      `{"unique": "already taken"}`,
	})
	v := Violation{Path: Path{Named("user"), Named("email")}, Code: CodeUnique}

	var first string
	for i := 0; i < 20; i++ {
		m, err := LoadMessages(StandardCodes(), fsys, "messages")
		if err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
		got := say(t, m, v, "fr") + "|" + say(t, m, v, "") + "|" + say(t, m, v, "en")
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("load %d answered %q and the first answered %q", i, got, first)
		}
	}

	if len(first) == 0 || first[0] == '|' {
		t.Fatalf("the fixture answers %q; it does not distinguish the locales", first)
	}
}
