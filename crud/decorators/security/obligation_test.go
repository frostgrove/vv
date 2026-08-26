package security_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/crudtest"
	"github.com/shardit-io/vv/crud/decorators/security"
)

// verb is one method of crud.Core and what the gate is obliged to do with it.
//
// The reason field is not decoration. [[D-030]] says a method the gate does not
// override must have a *written* reason why inheriting it is safe, and until
// this test existed that obligation was carried by a paragraph in a decision
// document — which nothing read and nothing checked.
type verb struct {
	name string
	// call drives the method through a gated repository. It exists because the
	// interesting question is behavioural: a gate that declared an override and
	// checked nothing would satisfy any list-comparison test ever written.
	call func(context.Context, crud.Repo[Doc, int64, DocUpdate]) error
	// gated is false for a method inherited from the wrapped Core.
	gated  bool
	reason string
}

// coreVerbs is the whole of crud.Core, decided one method at a time.
var coreVerbs = []verb{
	{name: "GetByID", gated: true, call: func(ctx context.Context, r crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.GetByID(ctx, 1)
		return err
	}},
	{name: "Get", gated: true, call: func(ctx context.Context, r crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.Get(ctx)
		return err
	}},
	{name: "GetAll", gated: true, call: func(ctx context.Context, r crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.GetAll(ctx)
		return err
	}},
	{name: "Count", gated: true, call: func(ctx context.Context, r crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.Count(ctx)
		return err
	}},
	{name: "Exists", gated: true, call: func(ctx context.Context, r crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.Exists(ctx)
		return err
	}},
	{name: "Aggregate", gated: true, call: func(ctx context.Context, r crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.Aggregate(ctx, crud.Aggregate(crud.CountAll("n")), crud.GroupBy("TenantID"))
		return err
	}},
	{name: "Save", gated: true, call: func(ctx context.Context, r crud.Repo[Doc, int64, DocUpdate]) error {
		return r.Save(ctx, &Doc{Title: "a"})
	}},
	{name: "SaveAll", gated: true, call: func(ctx context.Context, r crud.Repo[Doc, int64, DocUpdate]) error {
		return r.SaveAll(ctx, []*Doc{{Title: "a"}})
	}},
	{name: "Update", gated: true, call: func(ctx context.Context, r crud.Repo[Doc, int64, DocUpdate]) error {
		title := "b"
		_, err := r.Update(ctx, 1, DocUpdate{Title: &title})
		return err
	}},
	{name: "UpdateAll", gated: true, call: func(ctx context.Context, r crud.Repo[Doc, int64, DocUpdate]) error {
		title := "b"
		_, err := r.UpdateAll(ctx, DocUpdate{Title: &title})
		return err
	}},
	{name: "Delete", gated: true, call: func(ctx context.Context, r crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.Delete(ctx, 1)
		return err
	}},
	{name: "DeleteAll", gated: true, call: func(ctx context.Context, r crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.DeleteAll(ctx)
		return err
	}},

	{name: "Meta", gated: false, reason: "" +
		"Meta describes the bound model and the table it maps to. It reads no row, " +
		"takes no context and cannot be narrowed by a policy — there is nothing " +
		"about it a principal could be allowed or refused."},

	{name: "Tx", gated: false, reason: "" +
		"Tx runs the caller's closure inside a transaction and touches no row " +
		"itself. What the closure does reaches the database through the same " +
		"gated repository the caller already holds, so every statement inside it " +
		"is checked by the overrides above. A gate of its own here would refuse " +
		"the transaction rather than the work, which is a denial for an action " +
		"nobody took."},
}

// Every method on crud.Core is either overridden by the gate or has a written
// reason why inheriting it is safe, and the reason is checked to be true.
//
// This is [[D-030]] made mechanical. The decision has been in force since the
// seam grew Aggregate, SaveAll and UpdateAll — each of which was a leak until it
// was overridden — and it was enforced by nothing: the gate embeds crud.Core, so
// a thirteenth verb added to the seam would be inherited silently, run against
// the plain repository with no policy at all, and break no test.
func TestEveryVerbOnTheSeamIsGatedOrHasAWrittenReason(t *testing.T) {
	// Totality first. The seam is the source of truth, not this list.
	core := reflect.TypeOf((*crud.Core[Doc, int64])(nil)).Elem()
	listed := map[string]bool{}
	for _, v := range coreVerbs {
		listed[v.name] = true
	}
	for i := range core.NumMethod() {
		name := core.Method(i).Name
		if !listed[name] {
			t.Fatalf("crud.Core declares %s and this table has no row for it — decide, in "+
				"writing, what security.gate does with it ([[D-030]])", name)
		}
	}
	if core.NumMethod() != len(coreVerbs) {
		t.Fatalf("crud.Core declares %d methods and this table has %d rows",
			core.NumMethod(), len(coreVerbs))
	}

	// Then the behaviour. A row that claims an override has to refuse, and
	// refuse before the database is touched.
	denied := errors.New("no")
	policy := security.Policy[Doc, int64]{
		Authorize: func(context.Context, security.Action) error { return denied },
	}
	for _, v := range coreVerbs {
		if !v.gated {
			if v.reason == "" {
				t.Fatalf("%s is inherited and no reason is written down ([[D-030]])", v.name)
			}
			continue
		}
		t.Run(v.name, func(t *testing.T) {
			rec := crudtest.New(crud.Postgres{})
			repo := Docs.Bind(rec, security.Gate(policy))
			err := v.call(context.Background(), repo)
			if !errors.Is(err, denied) {
				t.Fatalf("a policy that refuses everything let %s through: %v", v.name, err)
			}
			if n := len(rec.Statements()); n != 0 {
				t.Fatalf("%s was refused and still ran %d statements: %v",
					v.name, n, rec.SQL())
			}
		})
	}
}

// The control on the table above.
//
// Every assertion in it would hold just as well if the gate refused everything
// unconditionally, including the two verbs it is supposed to inherit. This says
// they really do pass through, so "gated: false" means something.
func TestTheInheritedVerbsAreNotGated(t *testing.T) {
	rec := crudtest.New(crud.Postgres{})
	repo := Docs.Bind(rec, security.Gate(security.Policy[Doc, int64]{
		Authorize: func(context.Context, security.Action) error {
			return errors.New("no")
		},
	}))

	if m := repo.Meta(); m == nil || m.Table != "docs" {
		t.Fatalf("Meta was gated, or answered the wrong table: %+v", m)
	}

	ran := false
	if err := repo.Tx(context.Background(), func(context.Context) error {
		ran = true
		return nil
	}); err != nil {
		t.Fatalf("Tx was refused for an action nobody took: %v", err)
	}
	if !ran {
		t.Fatal("Tx returned without running the closure")
	}
}
