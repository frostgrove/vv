package security_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/security"
)

type verb struct {
	name string

	call func(context.Context, *crud.Repo[Doc, int64, DocUpdate]) error

	gated  bool
	reason string
}

var coreVerbs = []verb{
	{name: "GetByID", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.GetByID(ctx, 1)
		return err
	}},
	{name: "Get", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.Get(ctx)
		return err
	}},
	{name: "GetAll", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.GetAll(ctx)
		return err
	}},
	{name: "First", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.First(ctx)
		return err
	}},
	{name: "Count", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.Count(ctx)
		return err
	}},
	{name: "Exists", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.Exists(ctx)
		return err
	}},
	{name: "Aggregate", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.Aggregate(ctx, crud.Aggregate(crud.CountAll("n")), crud.GroupBy("TenantID"))
		return err
	}},
	{name: "Save", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.Save(ctx, &Doc{Title: "a"})
		return err
	}},
	{name: "SaveOnly", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		return r.SaveOnly(ctx, &Doc{Title: "a"})
	}},
	{name: "SaveAll", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		return r.SaveAll(ctx, []*Doc{{Title: "a"}})
	}},
	{name: "Update", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		title := "b"
		_, err := r.Update(ctx, 1, DocUpdate{Title: &title})
		return err
	}},
	{name: "UpdateAll", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		title := "b"
		_, err := r.UpdateAll(ctx, DocUpdate{Title: &title})
		return err
	}},
	{name: "Delete", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
		_, err := r.Delete(ctx, 1)
		return err
	}},
	{name: "DeleteAll", gated: true, call: func(ctx context.Context, r *crud.Repo[Doc, int64, DocUpdate]) error {
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

func TestEveryVerbOnTheSeamIsGatedOrHasAWrittenReason(t *testing.T) {
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
			repository := Docs.Bind(rec, security.Gate(policy))
			err := v.call(context.Background(), repository)
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

func TestTheInheritedVerbsAreNotGated(t *testing.T) {
	rec := crudtest.New(crud.Postgres{})
	repository := Docs.Bind(rec, security.Gate(security.Policy[Doc, int64]{
		Authorize: func(context.Context, security.Action) error {
			return errors.New("no")
		},
	}))

	if m := repository.Meta(); m == nil || m.Table != "docs" {
		t.Fatalf("Meta was gated, or answered the wrong table: %+v", m)
	}

	ran := false
	if err := repository.Tx(context.Background(), func(context.Context) error {
		ran = true
		return nil
	}); err != nil {
		t.Fatalf("Tx was refused for an action nobody took: %v", err)
	}
	if !ran {
		t.Fatal("Tx returned without running the closure")
	}
}
