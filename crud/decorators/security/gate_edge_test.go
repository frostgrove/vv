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

// scopeOnly is the policy a reader writes by hand when they want tenancy and
// nothing else: a Scope, no Inspect, no Immutable. The shipped ScopeField sets
// Scope and Inspect together, so the documented path was safe and this one was
// not — which is exactly why it needs pinning.
var scopeOnly = security.Policy[Doc, int64]{
	Scope: func(ctx context.Context) (crud.Predicate, error) {
		t, err := tenantOf(ctx)
		if err != nil {
			return nil, err
		}
		return crud.Eq("TenantID", t), nil
	},
}

// Save is an upsert: a Save carrying an id that already belongs to somebody else
// silently turns into an update of their row. There is no WHERE clause for the
// scope to narrow, so the gate has to refuse — with only a Scope declared,
// nothing used to and another tenant's row was overwritten and re-tenanted.
func TestAScopeWithoutInspectStillRefusesAnOverwriteOfAHiddenRow(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(),                // nothing visible under that id
		crudtest.Rows([]any{int64(1)}), // …but the row is there
	)

	err := Docs.Bind(rec, security.Gate(scopeOnly)).
		Save(ctx, &Doc{ID: 1, TenantID: 7, Title: "mine now"})

	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden for a Save onto a row outside the scope", err)
	}
	for _, sql := range rec.SQL() {
		if strings.HasPrefix(sql, "INSERT") {
			t.Fatalf("the upsert ran and re-tenanted another principal's row: %v", rec.SQL())
		}
	}
}

// The other half: an id that names no row at all is a plain insert, not a
// denial. Refusing here would make a client-chosen key unusable behind a gate.
func TestAScopedSaveOfAnUnusedIDIsStillAnInsert(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(), // nothing visible
		crudtest.Rows(), // and nothing hidden either
		crudtest.Rows(docRow(1, 7, "fresh")),
	)

	if err := Docs.Bind(rec, security.Gate(scopeOnly)).
		Save(ctx, &Doc{ID: 1, TenantID: 7, Title: "fresh"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rec.Last().SQL, "INSERT") {
		t.Fatalf("the insert never ran: %v", rec.SQL())
	}
}

// The scope belongs in the UPDATE itself, not only in the check in front of it.
// Checking and then writing unscoped is check-then-act: a row that leaves the
// scope in between is written anyway, and the gate hands the fresh copy of
// somebody else's record back to this caller with err == nil.
func TestTheGateScopeIsInTheUpdatesOwnWhereClause(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(docRow(42, 7, "mine")), // the gate's scoped load
		crudtest.Rows(docRow(42, 7, "mine")), // the repository's load
		crudtest.Rows(docRow(42, 7, "new")),  // RETURNING
	)

	if _, err := gated(rec).Update(ctx, 42, DocUpdate{Title: ptrTo("new")}); err != nil {
		t.Fatal(err)
	}

	update := rec.Last()
	if !strings.HasPrefix(update.SQL, "UPDATE") {
		t.Fatalf("the last statement was not the UPDATE: %v", rec.SQL())
	}
	if !strings.Contains(update.SQL, `"tenant_id" = `) {
		t.Fatalf("the UPDATE ran unscoped: %s", update.SQL)
	}
}

// And the consequence, from the caller's side: when the row is no longer inside
// the scope by the time the write runs, the write matches nothing and the
// answer is ErrNotFound — not another tenant's record.
func TestAnUpdateOfARowThatLeftTheScopeIsNotFound(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(docRow(42, 7, "mine")), // still visible when the gate looks
		crudtest.Rows(docRow(42, 7, "mine")), // and when the repository looks
		crudtest.Rows(),                      // by the time the UPDATE runs, gone
	)

	got, err := gated(rec).Update(ctx, 42, DocUpdate{Title: ptrTo("new")})

	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, got = %+v; want ErrNotFound rather than another tenant's row", err, got)
	}
}

// ---------------------------------------------------------------------------
// projections and Inspect

// ?select= is untrusted input and Inspect is an opaque closure, so the gate
// cannot know which columns the check reads. Under the documented tenancy
// policy a projection that left TenantID out made Inspect compare 0 against the
// context tenant: every single-entity read with ?select= answered 403.
func TestAProjectionDoesNotTurnEveryScopedReadIntoADenial(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(crudtest.Rows(docRow(42, 7, "mine")))

	got, err := gated(rec).GetByID(ctx, 42, crud.Select("Title"))
	if err != nil {
		t.Fatalf("err = %v; ?select= must not make the row invisible to its owner", err)
	}
	if got.Title != "mine" {
		t.Fatalf("row = %+v", got)
	}
	if !strings.Contains(rec.Last().SQL, `"tenant_id"`) {
		t.Fatalf("the read left out the column Inspect reads: %s", rec.Last().SQL)
	}
}

// The same hole pointed the other way: a rule that hides rows by a column value
// was bypassed by simply not selecting that column.
func TestAProjectionCannotBypassAnInspectRule(t *testing.T) {
	hideClassified := security.Policy[Doc, int64]{
		InspectReads: true,
		Inspect: func(_ context.Context, a security.Action, m *Doc) error {
			if m.Body == "secret" {
				return security.Denied(a, "row is classified")
			}
			return nil
		},
	}
	// The row's Body is the one the rule refuses; Title, which the client did
	// select, says nothing about it.
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(1), int64(7), "innocent", "secret"}))

	_, err := Docs.Bind(rec, security.Gate(hideClassified)).
		GetAll(context.Background(), crud.Select("Title"))

	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v; not selecting the column the rule reads must not disarm the rule", err)
	}
}

// A policy with no Inspect has nothing to blind, so the projection the caller
// asked for is the projection that runs.
func TestAProjectionSurvivesAPolicyThatDoesNotInspect(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(1), "t"}))

	if _, err := Docs.Bind(rec, security.Gate(scopeOnly)).
		GetAll(ctx, crud.Select("Title")); err != nil {
		t.Fatal(err)
	}
	if got := rec.Last().SQL; !strings.HasPrefix(got, `SELECT "id", "title" FROM`) {
		t.Fatalf("sql = %s, want the caller's projection left alone", got)
	}
}

// ---------------------------------------------------------------------------
// composition

// Combine folds AllowUnscopedDeleteAll with &&, and "every policy allows it" is
// vacuously true of no policies. A policy list built from a role produces an
// empty list for a role with no policies, and that must not be the one shape
// that permits truncating the table.
func TestCombineOfNothingIsNoMorePermissiveThanTheZeroPolicy(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		policy security.Policy[Doc, int64]
	}{
		{"the zero policy", security.Policy[Doc, int64]{}},
		{"Combine of nothing", security.Combine[Doc, int64]()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 99})

			n, err := Docs.Bind(rec, security.Gate(tc.policy)).DeleteAll(ctx)

			if !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("n = %d, err = %v; an unscoped DeleteAll behind a gate must be refused", n, err)
			}
			if len(rec.Statements()) != 0 {
				t.Fatalf("the table was truncated: %v", rec.SQL())
			}
		})
	}
}

// Freeze is the one-liner for "these columns are not yours to change", and it
// is the whole policy — no scope, no checks.
func TestFreezeRefusesAnUpdateThatNamesAFrozenField(t *testing.T) {
	ctx := context.Background()
	frozen := Docs.Bind(crudtest.Postgres(), security.Gate(security.Freeze[Doc, int64]("TenantID")))

	if _, err := frozen.Update(ctx, 1, DocUpdate{TenantID: ptrTo(int64(9))}); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}

	rec := crudtest.Postgres().Push(
		crudtest.Rows(docRow(1, 7, "old")),
		crudtest.Rows(docRow(1, 7, "old")),
		crudtest.Rows(docRow(1, 7, "new")),
	)
	other := Docs.Bind(rec, security.Gate(security.Freeze[Doc, int64]("TenantID")))
	if _, err := other.Update(ctx, 1, DocUpdate{Title: ptrTo("new")}); err != nil {
		t.Fatalf("Freeze refused a field it was never given: %v", err)
	}
}

// ---------------------------------------------------------------------------
// scope across a relation

// The gate's scope is a predicate over the root model and, like any WHERE
// clause, it stops at the statement's own FROM. Reaching another model through
// a relation is the repository's declaration to make, and it holds under a gate
// too — the two narrowings compose rather than replace.
func TestARelationScopeStillAppliesUnderAGate(t *testing.T) {
	ctx := withTenant(context.Background(), 7)

	type Tag struct {
		ID     int64  `db:"id,pk,auto"`
		DocID  int64  `db:"doc_id"`
		Hidden bool   `db:"hidden"`
		Name   string `db:"name"`
	}
	type TaggedDoc struct {
		ID       int64  `db:"id,pk,auto"`
		TenantID int64  `db:"tenant_id"`
		Title    string `db:"title"`

		Tags []Tag `rel:"has_many,fk=DocID"`
	}
	docs := sqlrepo.Define[TaggedDoc, int64, struct{}]("tagged_docs",
		sqlrepo.RelationScope("Tags", crud.Eq("Hidden", false)))

	rec := crudtest.Postgres().Push(
		crudtest.Rows([]any{int64(1), int64(7), "mine"}),
		crudtest.Rows(),
	)
	repo := docs.Bind(rec, security.Gate(security.ScopeField[TaggedDoc, int64]("TenantID", tenantOf)))

	if _, err := repo.GetAll(ctx, crud.Preload("Tags")); err != nil {
		t.Fatal(err)
	}
	if got := rec.Last().SQL; !strings.Contains(got, `"hidden" = `) {
		t.Fatalf("the preload ran unscoped under a gate: %s", got)
	}
}
