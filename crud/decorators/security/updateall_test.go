package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/crudtest"
	"github.com/shardit-io/vv/crud/decorators/security"
	"github.com/shardit-io/vv/crud/sqlrepo"
)

// UpdateAll writes rows nobody named, which makes it the write that most needs
// the gate. Everything the gate does to Update it must do here: the scope goes
// into the statement's own WHERE, not into a check in front of it.
func TestUpdateAllIsScopedInTheStatementItself(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 2})

	n, err := gated(rec).UpdateAll(ctx, DocUpdate{Title: ptrTo("renamed")}, crud.Where(crud.Eq("Body", "x")))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("n = %d", n)
	}
	if got := lastWhere(rec); got != `("tenant_id" = $2 AND "body" = $3)` {
		t.Fatalf("where = %s, want the tenant scope ANDed into the update", got)
	}
	if rec.Last().Args[1] != int64(7) {
		t.Fatalf("args = %v, want the context tenant bound", rec.Last().Args)
	}
}

// Without a scope and without a filter, UpdateAll rewrites every row of the
// table. Behind an access-control layer that is almost always a bug, so it is
// refused unless the policy says otherwise — and the permission is its own, not
// inherited from having allowed the table to be emptied.
func TestAnUnscopedUpdateAllIsRefusedUnlessThePolicyAllowsIt(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		policy security.Policy[Doc, int64]
	}{
		{"the zero policy", security.Policy[Doc, int64]{}},
		{"Combine of nothing", security.Combine[Doc, int64]()},
		{"a policy that only allows an unscoped DeleteAll",
			security.Policy[Doc, int64]{AllowUnscopedDeleteAll: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 99})

			n, err := Docs.Bind(rec, security.Gate(tc.policy)).UpdateAll(ctx, DocUpdate{Title: ptrTo("x")})

			if !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("n = %d, err = %v; an unscoped UpdateAll behind a gate must be refused", n, err)
			}
			if len(rec.Statements()) != 0 {
				t.Fatalf("every row was rewritten: %v", rec.SQL())
			}
		})
	}

	t.Run("a filter is enough", func(t *testing.T) {
		rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
		if _, err := Docs.Bind(rec, security.Gate(security.Policy[Doc, int64]{})).
			UpdateAll(ctx, DocUpdate{Title: ptrTo("x")}, crud.Where(crud.Eq("Body", "junk"))); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("so is opting in", func(t *testing.T) {
		rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 9})
		optIn := Docs.Bind(rec, security.Gate(security.Policy[Doc, int64]{AllowUnscopedUpdateAll: true}))
		if _, err := optIn.UpdateAll(ctx, DocUpdate{Title: ptrTo("x")}); err != nil {
			t.Fatal(err)
		}
	})
}

// A frozen column cannot be reached through the filtered write either. It would
// otherwise be the way around Freeze: refused one row at a time, allowed for the
// whole table at once.
func TestUpdateAllRefusesAFrozenField(t *testing.T) {
	ctx := context.Background()
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 99})
	frozen := Docs.Bind(rec, security.Gate(security.Freeze[Doc, int64]("TenantID")))

	_, err := frozen.UpdateAll(ctx, DocUpdate{TenantID: ptrTo(int64(9))}, crud.Where(crud.Eq("Body", "x")))
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("the frozen column was written: %v", rec.SQL())
	}
}

// A read-only repository has no filtered write either.
func TestUpdateAllIsRefusedByAReadOnlyPolicy(t *testing.T) {
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 99})
	repo := Docs.Bind(rec, security.Gate(security.ReadOnly[Doc, int64]()))

	if _, err := repo.UpdateAll(context.Background(), DocUpdate{Title: ptrTo("x")},
		crud.Where(crud.Eq("Body", "y"))); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a read-only repository wrote: %v", rec.SQL())
	}
}

// With an Inspect hook the gate has to look at the rows first: there is no id in
// the call, so nothing else in the request could stand for consent to touch them.
func TestUpdateAllInspectsEveryRowItIsAboutToWrite(t *testing.T) {
	ctx := context.Background()
	rec := crudtest.Postgres().Push(crudtest.Rows(docRow(1, 7, "mine"), docRow(2, 9, "theirs")))
	rec.ExecResult(crud.Result{RowsAffected: 99})

	var seen []int64
	policy := security.Policy[Doc, int64]{
		Inspect: func(_ context.Context, _ security.Action, d *Doc) error {
			seen = append(seen, d.ID)
			if d.TenantID != 7 {
				return security.Denied(security.Update, "row belongs to a different tenant")
			}
			return nil
		},
	}
	repo := sqlrepo.Define[Doc, int64, DocUpdate]("docs").Bind(rec, security.Gate(policy))

	_, err := repo.UpdateAll(ctx, DocUpdate{Title: ptrTo("x")}, crud.Where(crud.Eq("Body", "body")))
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v: Inspect refused a row and the update went ahead anyway", err)
	}
	if len(seen) == 0 {
		t.Fatal("Inspect never saw a row")
	}
	for _, stmt := range rec.SQL() {
		if strings.HasPrefix(stmt, "UPDATE") {
			t.Fatalf("the write was issued despite the refusal: %s", stmt)
		}
	}
}
