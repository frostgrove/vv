package storage_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frostgrove/vv/storage"
)

func TestAKeyHasOnePortableRepresentation(t *testing.T) {
	want := "invoices/2026/statement.pdf"
	key, err := storage.ParseKey(want)
	if err != nil {
		t.Fatalf("ParseKey(%q): %v", want, err)
	}
	if got := key.Value(); got != want {
		t.Fatalf("Value() = %q, want %q", got, want)
	}
	if got := fmt.Sprint(key); got != "[storage key]" {
		t.Fatalf("ordinary formatting disclosed or changed the key: %q", got)
	}
	if strings.Contains(fmt.Sprintf("%v", key), want) {
		t.Fatal("ordinary formatting disclosed the logical key")
	}
	assertFormattingRedacted(t, key, want)
}

func TestTheKeyParserRefusesEveryPortablePathAmbiguity(t *testing.T) {
	hostile := []string{
		"",
		"/absolute",
		"trailing/",
		"../escape",
		"a/../../escape",
		"a//b",
		"a/./b",
		`a\b`,
		"a\x00b",
		"a\nb",
		"a\x7fb",
		"кириллица",
		"CON",
		"con.txt",
		"a/NUL.bin",
		"trailing-dot.",
		"trailing-space ",
		"a<b",
		"a>b",
		"a:b",
		`a"b`,
		"a|b",
		"a?b",
		"a*b",
		strings.Repeat("a", storage.MaxKeySegmentBytes+1),
		strings.Repeat("a/", storage.MaxKeyBytes/2) + "a",
	}

	for _, raw := range hostile {
		_, err := storage.ParseKey(raw)
		if !errors.Is(err, storage.ErrInvalid) {
			t.Errorf("ParseKey(%q) error = %v, want ErrInvalid", raw, err)
			continue
		}
		if strings.Contains(fmt.Sprint(err), raw) && raw != "" {
			t.Errorf("ParseKey(%q) disclosed the rejected value in %q", raw, err)
		}
	}

	// The negative corpus proves something only beside representative values
	// that use the same separators and punctuation successfully.
	valid := []string{
		"a",
		"documents/2026/report.pdf",
		"uploads/01J6D5W8X2QZ6R8B4M7F.png",
		"printable space/in-the-middle.txt",
		strings.Repeat("a", storage.MaxKeySegmentBytes),
	}
	for _, raw := range valid {
		key, err := storage.ParseKey(raw)
		if err != nil {
			t.Errorf("ParseKey(%q): %v", raw, err)
			continue
		}
		if key.Value() != raw {
			t.Errorf("ParseKey(%q).Value() = %q", raw, key.Value())
		}
	}
}

func TestTheNamespaceParserAcceptsOnlyOneBoundedLogicalName(t *testing.T) {
	valid := []string{"a", "documents", "documents-2026", strings.Repeat("a", 63)}
	for _, raw := range valid {
		namespace, err := storage.ParseNamespace(raw)
		if err != nil {
			t.Errorf("ParseNamespace(%q): %v", raw, err)
			continue
		}
		if namespace.Value() != raw {
			t.Errorf("ParseNamespace(%q).Value() = %q", raw, namespace.Value())
		}
		if got := fmt.Sprint(namespace); got != "[storage namespace]" {
			t.Errorf("ordinary formatting disclosed or changed namespace: %q", got)
		}
	}
	privateNamespace, err := storage.ParseNamespace("private-2026")
	if err != nil {
		t.Fatal(err)
	}
	assertFormattingRedacted(t, privateNamespace, privateNamespace.Value())

	hostile := []string{
		"",
		"Documents",
		"documents_2026",
		"-documents",
		"documents-",
		"documents/other",
		"documents.other",
		"documents other",
		"документы",
		strings.Repeat("a", 64),
	}
	for _, raw := range hostile {
		_, err := storage.ParseNamespace(raw)
		if !errors.Is(err, storage.ErrInvalid) {
			t.Errorf("ParseNamespace(%q) error = %v, want ErrInvalid", raw, err)
		}
		if raw != "" && strings.Contains(fmt.Sprint(err), raw) {
			t.Errorf("ParseNamespace(%q) disclosed the rejected value in %q", raw, err)
		}
	}
}

func TestAStageIDRoundTripsWithoutBecomingItsLogRepresentation(t *testing.T) {
	id, err := storage.NewStageID()
	if err != nil {
		t.Fatalf("NewStageID: %v", err)
	}
	if id.Value() == "" {
		t.Fatal("NewStageID returned an empty serialized value")
	}
	parsed, err := storage.ParseStageID(id.Value())
	if err != nil {
		t.Fatalf("ParseStageID(NewStageID().Value()): %v", err)
	}
	if parsed != id {
		t.Fatalf("round trip changed StageID: got %q, want %q", parsed.Value(), id.Value())
	}
	if got := fmt.Sprint(id); got != "[storage stage]" {
		t.Fatalf("ordinary formatting disclosed or changed StageID: %q", got)
	}
	assertFormattingRedacted(t, id, id.Value())

	hostile := []string{"", "short", strings.Repeat("a", 31), strings.Repeat("a", 33), id.Value() + "=", id.Value()[:31] + "/"}
	for _, raw := range hostile {
		_, err := storage.ParseStageID(raw)
		if !errors.Is(err, storage.ErrInvalid) {
			t.Errorf("ParseStageID(%q) error = %v, want ErrInvalid", raw, err)
		}
		if raw != "" && strings.Contains(fmt.Sprint(err), raw) {
			t.Errorf("ParseStageID(%q) disclosed the rejected value in %q", raw, err)
		}
	}
}

func assertFormattingRedacted(t *testing.T, value any, secrets ...string) {
	t.Helper()
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		formatted := fmt.Sprintf(format, value)
		for _, secret := range secrets {
			if secret != "" && strings.Contains(formatted, secret) {
				t.Fatalf("format %q disclosed sensitive value in %q", format, formatted)
			}
		}
	}
}
