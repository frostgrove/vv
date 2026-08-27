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

// A scope has no way to judge an INSERT body: there is no WHERE clause to carry
// it. A scope-only policy must therefore refuse Save before it can turn a
// client-owned key into a cross-tenant overwrite.
func TestAScopeWithoutInspectRefusesSaveBeforeItCanWrite(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres()

	err := Docs.Bind(rec, security.Gate(scopeOnly)).
		Save(ctx, &Doc{ID: 1, TenantID: 7, Title: "mine now"})

	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden for a scope-only Save", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a scope-only Save consulted or wrote the database: %v", rec.SQL())
	}
}

func TestAScopeWithoutInspectAlsoRefusesAnUnusedClientKey(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres()

	err := Docs.Bind(rec, security.Gate(scopeOnly)).
		Save(ctx, &Doc{ID: 1, TenantID: 7, Title: "fresh"})
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden until the policy supplies Inspect", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a scope-only policy wrote an unchecked client key: %v", rec.SQL())
	}
}

func TestAScopeWithoutInspectRefusesEveryWriteWithABody(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	for _, tc := range []struct {
		name string
		call func(crud.Repo[Doc, int64, DocUpdate]) error
	}{
		{
			name: "Update",
			call: func(repo crud.Repo[Doc, int64, DocUpdate]) error {
				_, err := repo.Update(ctx, 1, DocUpdate{TenantID: ptrTo(int64(8))})
				return err
			},
		},
		{
			name: "UpdateAll",
			call: func(repo crud.Repo[Doc, int64, DocUpdate]) error {
				_, err := repo.UpdateAll(ctx, DocUpdate{TenantID: ptrTo(int64(8))})
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres()
			if err := tc.call(Docs.Bind(rec, security.Gate(scopeOnly))); !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("err = %v, want a scope-only body-write refusal", err)
			}
			if len(rec.Statements()) != 0 {
				t.Fatalf("scope-only %s reached SQL: %v", tc.name, rec.SQL())
			}
		})
	}
}

type softDoc struct {
	ID        int64  `db:"id,pk,auto"`
	TenantID  int64  `db:"tenant_id"`
	Title     string `db:"title"`
	DeletedAt *int64 `db:"deleted_at"`
}

type softDocUpdate struct{ Title *string }

// A tombstone is permanently invisible to normal repository reads, but it is
// still physically present. Save must discover that fact before it can upsert
// over a row another caller is not allowed to revive or re-tenant.
func TestSaveRefusesToOverwriteATombstoneHiddenByRepositoryScope(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	docs := sqlrepo.Define[softDoc, int64, softDocUpdate]("soft_docs", sqlrepo.SoftDelete("DeletedAt"))
	rec := crudtest.Postgres().Push(
		crudtest.Rows(),                // no visible row under the dynamic scope
		crudtest.Rows([]any{int64(1)}), // unscoped physical-existence probe sees the tombstone
	)
	repo := docs.Bind(rec, security.Gate(security.ScopeField[softDoc, int64]("TenantID", tenantOf)))

	err := repo.Save(ctx, &softDoc{ID: 1, TenantID: 7, Title: "resurrect"})
	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound for an invisible tombstone", err)
	}
	if errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v revealed that the invisible tombstone exists", err)
	}
	if wrote(rec, "INSERT") {
		t.Fatalf("Save resurrected a hidden tombstone: %v", rec.SQL())
	}
}

// A save needs an unscoped physical-existence check so it never turns a hidden
// assigned key into an overwrite. That check is an implementation detail: the
// response must remain the same not-found answer a caller gets for a hidden
// row through GetByID or Update.
func TestSaveOfAnotherTenantsAssignedKeyLooksMissing(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(),                // the scoped lookup finds no row
		crudtest.Rows([]any{int64(1)}), // the internal existence check does
	)

	err := gated(rec).Save(ctx, &Doc{ID: 1, TenantID: 7, Title: "overwrite"})
	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v revealed the other tenant's row", err)
	}
	if wrote(rec, "INSERT") || wrote(rec, "UPDATE") {
		t.Fatalf("a hidden row reached a write: %v", rec.SQL())
	}
}

func TestScopeOnlySavePreservesResolverFailures(t *testing.T) {
	want := errors.New("tenant is missing")
	policy := security.Policy[Doc, int64]{
		Scope: func(context.Context) (crud.Predicate, error) { return nil, want },
	}
	for _, tc := range []struct {
		name string
		call func(crud.Repo[Doc, int64, DocUpdate]) error
	}{
		{"Save", func(repo crud.Repo[Doc, int64, DocUpdate]) error {
			return repo.Save(context.Background(), &Doc{Title: "x"})
		}},
		{"SaveAll", func(repo crud.Repo[Doc, int64, DocUpdate]) error {
			return repo.SaveAll(context.Background(), []*Doc{{Title: "x"}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres()
			err := tc.call(Docs.Bind(rec, security.Gate(policy)))
			if !errors.Is(err, want) {
				t.Fatalf("err = %v, want the scope resolver error", err)
			}
			if len(rec.Statements()) != 0 {
				t.Fatalf("a failed scope resolver reached SQL: %v", rec.SQL())
			}
		})
	}
}

// Generated-key saves have no SQL WHERE in which to carry a scope, but a
// resolver failure is still a failed authorization decision. Inspect does not
// turn that failure into permission to insert an unscoped row.
func TestGeneratedSaveStillFailsWhenAScopeResolverFails(t *testing.T) {
	boom := errors.New("principal lookup failed")
	policies := []struct {
		name   string
		policy security.Policy[Doc, int64]
	}{
		{
			name: "root scope",
			policy: security.Policy[Doc, int64]{
				Scope:   func(context.Context) (crud.Predicate, error) { return nil, boom },
				Inspect: func(context.Context, security.Action, *Doc) error { return nil },
			},
		},
		{
			name: "relation scope",
			policy: security.Policy[Doc, int64]{
				RelationScopes: func(context.Context) (*crud.RelationScopes, error) { return nil, boom },
				Inspect:        func(context.Context, security.Action, *Doc) error { return nil },
			},
		},
	}
	for _, policy := range policies {
		for _, save := range []struct {
			name string
			call func(crud.Repo[Doc, int64, DocUpdate]) error
		}{
			{"Save", func(repo crud.Repo[Doc, int64, DocUpdate]) error {
				return repo.Save(context.Background(), &Doc{TenantID: 7, Title: "x"})
			}},
			{"SaveAll", func(repo crud.Repo[Doc, int64, DocUpdate]) error {
				return repo.SaveAll(context.Background(), []*Doc{{TenantID: 7, Title: "x"}})
			}},
		} {
			t.Run(policy.name+"/"+save.name, func(t *testing.T) {
				rec := crudtest.Postgres()
				err := save.call(Docs.Bind(rec, security.Gate(policy.policy)))
				if !errors.Is(err, boom) {
					t.Fatalf("err = %v, want the resolver error", err)
				}
				if len(rec.Statements()) != 0 {
					t.Fatalf("a failed resolver reached SQL: %v", rec.SQL())
				}
			})
		}
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

// Update has an inspect/read/write protocol. Its scope must be a single
// decision for that whole protocol: resolving it again after Inspect would let
// a changing principal or clock select a different row set for the mutation.
func TestUpdateResolvesItsWriteScopeOnce(t *testing.T) {
	boom := errors.New("scope was resolved twice")
	calls := 0
	policy := security.Policy[Doc, int64]{
		Scope: func(context.Context) (crud.Predicate, error) {
			calls++
			if calls > 1 {
				return nil, boom
			}
			return crud.Eq("TenantID", int64(7)), nil
		},
		Inspect: func(context.Context, security.Action, *Doc) error { return nil },
	}
	rec := crudtest.Postgres().Push(
		crudtest.Rows(docRow(42, 7, "before")), // gate's inspected snapshot
		crudtest.Rows(docRow(42, 7, "before")), // repository's diff load
		crudtest.Rows(docRow(42, 7, "after")),  // UPDATE ... RETURNING
	)

	got, err := Docs.Bind(rec, security.Gate(policy)).Update(context.Background(), 42, DocUpdate{Title: ptrTo("after")})
	if err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if got.Title != "after" || calls != 1 {
		t.Fatalf("Update() = %+v, scope calls = %d; want updated row and one resolver call", got, calls)
	}
}

func TestDeleteFailsClosedWhenARelationScopeCannotBeResolved(t *testing.T) {
	boom := errors.New("relation scope principal is unavailable")
	policy := security.Policy[Doc, int64]{
		RelationScopes: func(context.Context) (*crud.RelationScopes, error) { return nil, boom },
	}
	rec := crudtest.Postgres()
	_, err := Docs.Bind(rec, security.Gate(policy)).Delete(context.Background(), 42)
	if !errors.Is(err, boom) {
		t.Fatalf("Delete() = %v, want relation scope resolver error", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a failed relation scope reached SQL: %v", rec.SQL())
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
		{"a freeze-only policy", security.Freeze[Doc, int64]("TenantID")},
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

// A Gate is an enforcement declaration, not a decorative wrapper. A policy
// list that becomes empty during boot must fail immediately rather than mount
// an endpoint that reads every row.
func TestAGateWithNoEffectivePolicyPanicsAtBinding(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a zero Policy made a repository appear gated while leaving its reads unrestricted")
		}
	}()
	_ = security.Gate(security.Policy[Doc, int64]{})
}

// A nil predicate once meant both "administrator" and "could not resolve the
// tenant". The latter must fail closed; the former now needs a declaration that
// makes the bypass visible in code review.
func TestANilScopePredicateFailsClosedUnlessExplicitlyAllowed(t *testing.T) {
	nilScope := func(context.Context) (crud.Predicate, error) { return nil, nil }

	t.Run("implicit", func(t *testing.T) {
		rec := crudtest.Postgres()
		_, err := Docs.Bind(rec, security.Gate(security.Policy[Doc, int64]{Scope: nilScope})).GetAll(context.Background())
		if !errors.Is(err, security.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if len(rec.Statements()) != 0 {
			t.Fatalf("a nil tenant scope ran an unrestricted statement: %v", rec.SQL())
		}
	})

	t.Run("explicit administrator", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows())
		_, err := Docs.Bind(rec, security.Gate(security.Policy[Doc, int64]{
			Scope:              nilScope,
			AllowUnscopedScope: true,
		})).GetAll(context.Background())
		if err != nil {
			t.Fatalf("explicit unrestricted scope was refused: %v", err)
		}
		if len(rec.Statements()) != 1 {
			t.Fatalf("statements = %v, want the administrator's intentional read", rec.SQL())
		}
	})
}

// Composition is AND, including the failure rule. A second predicate must not
// make a missing tenant predicate disappear from the combined policy.
func TestCombinedScopeFailsWhenOneRequiredScopeAnswersNil(t *testing.T) {
	rec := crudtest.Postgres()
	policy := security.Combine(
		security.Policy[Doc, int64]{Scope: func(context.Context) (crud.Predicate, error) { return nil, nil }},
		security.Policy[Doc, int64]{Scope: func(context.Context) (crud.Predicate, error) {
			return crud.Eq("Title", "published"), nil
		}},
	)
	_, err := Docs.Bind(rec, security.Gate(policy)).GetAll(context.Background())
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a later scope masked the missing tenant scope: %v", rec.SQL())
	}
}

func TestATautologicalFilterDoesNotPermitAnUnscopedDeleteAll(t *testing.T) {
	for _, p := range []crud.Predicate{
		crud.NotInAny("ID", []int64{}),
		// Or drops nil branches when it renders. Its negation is therefore
		// `NOT (1 = 0)`, another spelling of an unrestricted statement.
		crud.Not(crud.Or(nil)),
	} {
		rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 99})
		repo := Docs.Bind(rec, security.Gate(security.Freeze[Doc, int64]("TenantID")))
		_, err := repo.DeleteAll(context.Background(), crud.Where(p))
		if !errors.Is(err, security.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if len(rec.Statements()) != 0 {
			t.Fatalf("a tautology deleted the table: %v", rec.SQL())
		}
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
