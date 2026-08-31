package probe

import (
	"testing"

	"github.com/frostgrove/vv/crud/crudtest"
)

func TestSimpleIssuesNoStatementAndKeepsTheDriversViolation(t *testing.T) {
	rec := crudtest.Postgres()
	f := conflict("docs_email_uk", "email")

	got, err := Simple().Enrich(ctx, request(f, rec, docMeta(t), row(insert())))
	if err != nil {
		t.Fatalf("Simple reported %v", err)
	}
	if got != f {
		t.Fatalf("Simple did not hand back the fault it was given: %+v", got)
	}
	if n := len(rec.Statements()); n != 0 {
		t.Fatalf("Simple issued %d statements", n)
	}
	if got.Partial {
		t.Fatal("Simple marked a complete answer incomplete")
	}
}

func TestFullIssuesOneWhereSimpleIssuesNone(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(answer(false, false, false, false, false))
	f := declared(t, fixture())
	if _, err := f.Enrich(ctx, request(conflict("", ""), rec, docMeta(t), row(insert()))); err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	if n := len(rec.Statements()); n != 1 {
		t.Fatalf("Full issued %d statements, want one", n)
	}
}

func TestSimpleDeclaresAgainstAnyModel(t *testing.T) {
	h, err := Simple().(Declarer).Declare(docMeta(t))
	if err != nil {
		t.Fatalf("Simple refused a declaration: %v", err)
	}
	if _, ok := h.(simple); !ok {
		t.Fatalf("Simple.Declare handed back a %T", h)
	}
}
