package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

// UpdateAll writes rows nobody named, which makes it the write that most needs
// the gate. Everything the gate does to Update it must do here: the scope goes
// into the statement's own WHERE, not into a check in front of it.
func TestUpdateAllIsScopedInTheStatementItself(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(crudtest.Rows(docRow(1, 7, "before"))).
		ExecResult(crud.Result{RowsAffected: 2})

	n, err := gated(rec).UpdateAll(ctx, DocUpdate{Title: ptrTo("renamed")}, crud.Where(crud.Eq("Body", "x")))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("n = %d", n)
	}
	where := lastWhere(rec)
	for _, want := range []string{`"tenant_id" = $`, `"body" = $`, `"id" = $`, `"title" = $`} {
		if !strings.Contains(where, want) {
			t.Fatalf("where = %s, want scoped inspected row condition %s", where, want)
		}
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
		{"a freeze-only policy", security.Freeze[Doc, int64]("TenantID")},
		{"a combined freeze-only policy", security.Combine(security.Freeze[Doc, int64]("TenantID"))},
		{"a policy that only allows an unscoped DeleteAll",
			security.Policy[Doc, int64]{Immutable: []string{"TenantID"}, AllowUnscopedDeleteAll: true}},
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
		if _, err := Docs.Bind(rec, security.Gate(security.Freeze[Doc, int64]("TenantID"))).
			UpdateAll(ctx, DocUpdate{Title: ptrTo("x")}, crud.Where(crud.Eq("Body", "junk"))); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("so is opting in", func(t *testing.T) {
		rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 9})
		optIn := Docs.Bind(rec, security.Gate(security.Policy[Doc, int64]{
			Immutable:              []string{"TenantID"},
			AllowUnscopedUpdateAll: true,
		}))
		if _, err := optIn.UpdateAll(ctx, DocUpdate{Title: ptrTo("x")}); err != nil {
			t.Fatal(err)
		}
	})
}

// An empty NOT IN is SQL's true constant. It is not a caller narrowing and
// must not be able to make a policy's unscoped-write guard think there is one.
func TestATautologicalFilterDoesNotPermitAnUnscopedUpdateAll(t *testing.T) {
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 99})
	repository := Docs.Bind(rec, security.Gate(security.Freeze[Doc, int64]("TenantID")))
	_, err := repository.UpdateAll(context.Background(), DocUpdate{Title: ptrTo("x")},
		crud.Where(crud.NotInAny("ID", []int64{})))
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a tautology rewrote the table: %v", rec.SQL())
	}
}

func TestASameFieldComparisonDoesNotPermitAnUnscopedBulkWrite(t *testing.T) {
	repository := Docs.Bind(crudtest.Postgres(), security.Gate(security.Freeze[Doc, int64]("TenantID")))
	_, err := repository.UpdateAll(context.Background(), DocUpdate{Title: ptrTo("x")},
		crud.Where(crud.EqField("ID", "id")))
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want an unscoped bulk-write denial", err)
	}
}

// A policy that explicitly represents an unrestricted administrator still
// needs the separate bulk-write opt-in. Otherwise Scope: True would make the
// same full-table write look scoped merely because it has a predicate node.
func TestATautologicalPolicyScopeDoesNotPermitBulkWrites(t *testing.T) {
	policy := security.Policy[Doc, int64]{
		Scope:              func(context.Context) (crud.Predicate, error) { return crud.True(), nil },
		AllowUnscopedScope: true,
	}

	for _, tc := range []struct {
		name string
		call func(*crud.Repo[Doc, int64, DocUpdate]) error
	}{
		{"UpdateAll", func(repository *crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := repository.UpdateAll(context.Background(), DocUpdate{Title: ptrTo("x")})
			return err
		}},
		{"DeleteAll", func(repository *crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := repository.DeleteAll(context.Background())
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres()
			err := tc.call(Docs.Bind(rec, security.Gate(policy)))
			if !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if len(rec.Statements()) != 0 {
				t.Fatalf("a tautological policy scope wrote every row: %v", rec.SQL())
			}
		})
	}
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
	repository := Docs.Bind(rec, security.Gate(security.ReadOnly[Doc, int64]()))

	if _, err := repository.UpdateAll(context.Background(), DocUpdate{Title: ptrTo("x")},
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
	repository := sqlrepo.Define[Doc, int64, DocUpdate]("docs").Bind(rec, security.Gate(policy))

	_, err := repository.UpdateAll(ctx, DocUpdate{Title: ptrTo("x")}, crud.Where(crud.Eq("Body", "body")))
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

// The public query shape may carry a page or cursor, but neither exists on a
// bulk UPDATE. Its preliminary read has to ignore those shaping options or an
// Inspect hook approves only the first page while the UPDATE reaches every
// matching row.
func TestBulkInspectionIgnoresCallerPagingAndPreloads(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(*crud.Repo[Doc, int64, DocUpdate]) error
	}{
		{
			name: "UpdateAll",
			call: func(repository *crud.Repo[Doc, int64, DocUpdate]) error {
				_, err := repository.UpdateAll(ctx, DocUpdate{Title: ptrTo("x")},
					crud.Where(crud.Eq("Body", "body")), crud.Limit(1), crud.After("ignored"), crud.Preload("Comments"))
				return err
			},
		},
		{
			name: "DeleteAll",
			call: func(repository *crud.Repo[Doc, int64, DocUpdate]) error {
				_, err := repository.DeleteAll(ctx,
					crud.Where(crud.Eq("Body", "body")), crud.Limit(1), crud.After("ignored"), crud.Preload("Comments"))
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := 0
			rec := crudtest.Postgres().Push(crudtest.Rows(docRow(1, 7, "one"), docRow(2, 7, "sealed")))
			repository := Docs.Bind(rec, security.Gate(security.Policy[Doc, int64]{
				Inspect: func(_ context.Context, _ security.Action, d *Doc) error {
					seen++
					if d.Title == "sealed" {
						return security.Denied(security.Update, "sealed")
					}
					return nil
				},
			}))

			if err := tc.call(repository); !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if seen != 2 {
				t.Fatalf("Inspect saw %d rows, want every matching row", seen)
			}
			if len(rec.Statements()) != 1 {
				t.Fatalf("a refused inspection issued %d statements: %v", len(rec.Statements()), rec.SQL())
			}
			sql := crudtest.Normalize(rec.Last().SQL)
			if strings.Contains(sql, "LIMIT") || strings.Contains(sql, "Comments") {
				t.Fatalf("internal inspection inherited caller shaping: %s", sql)
			}
		})
	}
}
