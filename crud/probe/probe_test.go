package probe

import (
	"testing"

	"github.com/frostgrove/vv/crud/crudtest"
)

// Simple is what every verb the caller did not name gets, and what an engine
// whose transaction is poisoned falls back to. It has one job and one way to
// fail at it: issuing a statement, or losing the violation it was handed.
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

// The control: the same request through Full does issue one, so the assertion
// above is about Simple rather than about the fixture producing no plan.
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

// Declaring Simple accepts every model, so a caller can turn one verb off by
// name without the catalog having anything to say about it.
func TestSimpleDeclaresAgainstAnyModel(t *testing.T) {
	h, err := Simple().(Declarer).Declare(docMeta(t))
	if err != nil {
		t.Fatalf("Simple refused a declaration: %v", err)
	}
	if _, ok := h.(simple); !ok {
		t.Fatalf("Simple.Declare handed back a %T", h)
	}
}
