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

type Doc struct {
	ID       int64  `db:"id,pk,auto"`
	TenantID int64  `db:"tenant_id"`
	Title    string `db:"title"`
	Body     string `db:"body"`
}

type DocUpdate struct {
	Title    *string
	Body     *string
	TenantID *int64
}

var Docs = sqlrepo.Define[Doc, int64, DocUpdate]("docs")

type tenantKey struct{}

func withTenant(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, tenantKey{}, id)
}

func tenantOf(ctx context.Context) (any, error) {
	t, ok := ctx.Value(tenantKey{}).(int64)
	if !ok {
		return nil, security.Denied(security.Read, "no tenant in context")
	}
	return t, nil
}

var tenantPolicy = security.ScopeField[Doc, int64]("TenantID", tenantOf)

func docRow(id, tenant int64, title string) []any {
	return []any{id, tenant, title, "body"}
}

func gated(rec *crudtest.Recorder) crud.Repo[Doc, int64, DocUpdate] {
	return Docs.Bind(rec, security.Gate(tenantPolicy))
}

// lastWhere isolates the WHERE clause of the most recent statement.
func lastWhere(rec *crudtest.Recorder) string {
	_, clause, _ := strings.Cut(rec.Last().SQL, " WHERE ")
	for _, tail := range []string{" ORDER BY ", " LIMIT ", " OFFSET "} {
		clause, _, _ = strings.Cut(clause, tail)
	}
	return clause
}

func TestScopeIsAppendedToEveryRead(t *testing.T) {
	ctx := withTenant(context.Background(), 7)

	for _, tc := range []struct {
		name string
		push []crudtest.Result
		call func(r crud.Repo[Doc, int64, DocUpdate]) error
		want string
	}{
		{"GetAll", []crudtest.Result{crudtest.Rows()},
			func(r crud.Repo[Doc, int64, DocUpdate]) error { _, err := r.GetAll(ctx); return err },
			`"tenant_id" = $1`},
		{"GetAll with a caller filter", []crudtest.Result{crudtest.Rows()},
			func(r crud.Repo[Doc, int64, DocUpdate]) error {
				_, err := r.GetAll(ctx, crud.Where(crud.Eq("Title", "x")))
				return err
			},
			`("tenant_id" = $1 AND "title" = $2)`},
		{"Count", []crudtest.Result{crudtest.Rows([]any{int64(0)})},
			func(r crud.Repo[Doc, int64, DocUpdate]) error { _, err := r.Count(ctx); return err },
			`"tenant_id" = $1`},
		{"Exists", []crudtest.Result{crudtest.Rows()},
			func(r crud.Repo[Doc, int64, DocUpdate]) error { _, err := r.Exists(ctx); return err },
			`"tenant_id" = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(tc.push...)
			if err := tc.call(gated(rec)); err != nil {
				t.Fatal(err)
			}
			if got := lastWhere(rec); got != tc.want {
				t.Fatalf("where = %s, want %s", got, tc.want)
			}
			if rec.Last().Args[0] != int64(7) {
				t.Fatalf("args = %v", rec.Last().Args)
			}
		})
	}
}

// An id belonging to somebody else must look absent, not forbidden: a 403 would
// confirm that the row exists.
func TestOutOfScopeIDLooksMissing(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	_, err := gated(rec).GetByID(withTenant(context.Background(), 7), 42)
	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := lastWhere(rec); got != `("tenant_id" = $1 AND "id" = $2)` {
		t.Fatalf("where = %s", got)
	}
}

func TestReadWithoutAPrincipalFails(t *testing.T) {
	rec := crudtest.Postgres()
	if _, err := gated(rec).GetAll(context.Background()); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatal("no principal means no query")
	}
}

func TestUpdateIsScopedAndFreezesTheScopeField(t *testing.T) {
	ctx := withTenant(context.Background(), 7)

	// A document in another tenant is simply not there.
	rec := crudtest.Postgres().Push(crudtest.Rows())
	title := "new"
	if _, err := gated(rec).Update(ctx, 1, DocUpdate{Title: &title}); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	// Moving a row into another tenant is refused before any SQL runs.
	rec = crudtest.Postgres()
	other := int64(8)
	if _, err := gated(rec).Update(ctx, 1, DocUpdate{TenantID: &other}); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("an immutable field must be caught before the load: %v", rec.SQL())
	}

	// The happy path still works.
	rec = crudtest.Postgres().Push(
		crudtest.Rows(docRow(1, 7, "old")), // gate loads within scope
		crudtest.Rows(docRow(1, 7, "old")), // the repository loads to diff
		crudtest.Rows(docRow(1, 7, "new")), // RETURNING
	)
	d, err := gated(rec).Update(ctx, 1, DocUpdate{Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "new" {
		t.Fatalf("doc = %+v", d)
	}
}

func TestDeleteIsScoped(t *testing.T) {
	rec := crudtest.Postgres().
		Push(crudtest.Rows(docRow(1, 7, "a"))). // Inspect needs to see the victims
		ExecResult(crud.Result{RowsAffected: 1})

	n, err := gated(rec).Delete(withTenant(context.Background(), 7), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("n = %d", n)
	}
	if got := crudtest.Normalize(rec.Last().SQL); got != `DELETE FROM "docs" WHERE ("tenant_id" = $1 AND "id" IN ($2, $3))` {
		t.Fatalf("sql = %s", got)
	}
}

func TestSaveRefusesToWriteIntoAnotherTenant(t *testing.T) {
	ctx := withTenant(context.Background(), 7)

	rec := crudtest.Postgres()
	d := Doc{TenantID: 8, Title: "sneaky"}
	if err := gated(rec).Save(ctx, &d); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("nothing should have been written: %v", rec.SQL())
	}

	// Overwriting somebody else's row is refused as well.
	rec = crudtest.Postgres().Push(crudtest.Rows(docRow(1, 8, "theirs")))
	d = Doc{ID: 1, TenantID: 7, Title: "mine now"}
	if err := gated(rec).Save(ctx, &d); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	for _, s := range rec.SQL() {
		if strings.HasPrefix(s, "INSERT") {
			t.Fatalf("an insert slipped through: %v", rec.SQL())
		}
	}

	// The honest case goes through.
	rec = crudtest.Postgres().Push(crudtest.Rows(docRow(5, 7, "mine")))
	d = Doc{TenantID: 7, Title: "mine"}
	if err := gated(rec).Save(ctx, &d); err != nil {
		t.Fatal(err)
	}
	if d.ID != 5 {
		t.Fatalf("doc = %+v", d)
	}
}

func TestUnscopedDeleteAllIsRefused(t *testing.T) {
	rec := crudtest.Postgres()
	repo := Docs.Bind(rec, security.Gate(security.Policy[Doc, int64]{}))

	if _, err := repo.DeleteAll(context.Background()); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatal("a truncate must not reach the database by accident")
	}

	// Narrowing it makes it acceptable.
	rec.ExecResult(crud.Result{RowsAffected: 2})
	if _, err := repo.DeleteAll(context.Background(), crud.Where(crud.Eq("Title", "junk"))); err != nil {
		t.Fatal(err)
	}
	// So does opting in explicitly.
	optIn := Docs.Bind(rec, security.Gate(security.Policy[Doc, int64]{AllowUnscopedDeleteAll: true}))
	if _, err := optIn.DeleteAll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyPolicy(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(docRow(1, 1, "a")))
	repo := Docs.Bind(rec, security.Gate(security.ReadOnly[Doc, int64]()))

	if _, err := repo.GetByID(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	d := Doc{Title: "nope"}
	if err := repo.Save(context.Background(), &d); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if _, err := repo.Delete(context.Background(), 1); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	title := "nope"
	if _, err := repo.Update(context.Background(), 1, DocUpdate{Title: &title}); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestPoliciesCombine(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	policy := security.Combine(
		tenantPolicy,
		security.Policy[Doc, int64]{
			Scope: func(context.Context) (crud.Predicate, error) {
				return crud.Ne("Title", "hidden"), nil
			},
		},
	)
	repo := Docs.Bind(rec, security.Gate(policy))
	if _, err := repo.GetAll(withTenant(context.Background(), 7)); err != nil {
		t.Fatal(err)
	}
	if got := lastWhere(rec); got != `("tenant_id" = $1 AND "title" <> $2)` {
		t.Fatalf("where = %s", got)
	}
}

// A decorator chain applies outside in: the gate sees the call before the basic
// repository does.
func TestGateComposesWithOtherMiddleware(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	var seen []string
	trace := func(name string) crud.Middleware[Doc, int64] {
		return func(next crud.Core[Doc, int64]) crud.Core[Doc, int64] {
			return tracer{Core: next, name: name, seen: &seen}
		}
	}
	repo := Docs.Bind(rec, trace("outer"), security.Gate(tenantPolicy), trace("inner"))
	if _, err := repo.GetAll(withTenant(context.Background(), 7)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "outer" || seen[1] != "inner" {
		t.Fatalf("order = %v", seen)
	}
	if got := lastWhere(rec); got != `"tenant_id" = $1` {
		t.Fatalf("where = %s", got)
	}
}

type tracer struct {
	crud.Core[Doc, int64]
	name string
	seen *[]string
}

func (t tracer) GetAll(ctx context.Context, opts ...crud.Option) ([]Doc, error) {
	*t.seen = append(*t.seen, t.name)
	return t.Core.GetAll(ctx, opts...)
}

// A frozen field is frozen through every verb, whichever spelling declared it.
//
// The two enforcement points used to speak different vocabularies: Update
// compares against crud.DefinedFields, which answers *canonical* model field
// names, while Save resolved each name through the forgiving meta.Field, which
// also accepts the column spelling. So Freeze("tenant_id") froze the column on
// PUT and not on PATCH — silently writable through the verb a client is most
// likely to use, on a policy whose whole purpose is that it is not.
func TestAFrozenFieldIsFrozenByEitherSpellingAndThroughBothVerbs(t *testing.T) {
	for _, spelling := range []string{"TenantID", "tenant_id"} {
		t.Run(spelling, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows(docRow(1, 7, "mine")))
			repo := Docs.Bind(rec, security.Gate(security.Freeze[Doc, int64](spelling)))

			// PATCH: a DTO that defines the frozen field is refused.
			other := int64(9)
			_, err := repo.Update(context.Background(), 1, DocUpdate{TenantID: &other})
			if !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("declared as %q, the field was writable through PATCH: %v", spelling, err)
			}
			if n := len(rec.Statements()); n != 0 {
				t.Fatalf("the refusal still ran %d statements", n)
			}
		})
	}

	// The control: a DTO that leaves the frozen field alone still goes through,
	// so the refusals above are the field and not the policy.
	rec := crudtest.Postgres().Push(
		crudtest.Rows(docRow(1, 7, "mine")),
		crudtest.Rows(docRow(1, 7, "renamed")),
	).ExecResult(crud.Result{RowsAffected: 1})
	title := "renamed"
	if _, err := Docs.Bind(rec, security.Gate(security.Freeze[Doc, int64]("tenant_id"))).
		Update(context.Background(), 1, DocUpdate{Title: &title}); err != nil {
		t.Fatalf("an update that touched no frozen field was refused: %v", err)
	}
}

// A frozen name that resolves to nothing is a declaration mistake, and it fails
// where it was written.
//
// It used to be accepted: Update never matched it, so the field the author meant
// to freeze was writable, and Save turned it into a denial of every write. Both
// halves are silent until a request arrives ([[D-021]]).
func TestFreezingAFieldTheModelDoesNotHavePanicsAtDeclaration(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a frozen name that names nothing was accepted, so it freezes nothing and nobody is told")
		}
	}()
	security.Gate(security.Freeze[Doc, int64]("Nope"))(nil)
}

// A policy with no per-row rule does not pay for the row.
//
// gate.Update loaded the row and handed it to Inspect. With no Inspect wired it
// loaded the row and did nothing with it — a whole round trip before every
// update, on the most common policy shape there is: a scope and no per-row rule.
//
// The scope is not what needed it. The UPDATE carries the same scope, so a row
// outside it matches nothing and the repository answers ErrNotFound by itself
// ([[D-008]]), which is what the control below asserts.
func TestAPolicyWithNoPerRowRuleDoesNotLoadTheRow(t *testing.T) {
	title := "renamed"

	// The repository loads the row itself — Update is load-diff-write ([[D-010]])
	// — so the question is not whether a SELECT happens but whether the gate adds
	// a *second* one. Counting is the only way to see it.
	update := func(t *testing.T, policy security.Policy[Doc, int64], rows int) int {
		t.Helper()
		rec := crudtest.Postgres()
		for range rows {
			rec = rec.Push(crudtest.Rows(docRow(1, 7, "mine")))
		}
		if _, err := Docs.Bind(rec, security.Gate(policy)).
			Update(withTenant(context.Background(), 7), 1, DocUpdate{Title: &title}); err != nil {
			t.Fatal(err)
		}
		return len(rec.Statements())
	}

	// A scope and nothing else. tenantPolicy is ScopeField, which sets an Inspect
	// of its own — so it is not the shape this test is about.
	scopeOnly := security.Policy[Doc, int64]{
		Scope: func(ctx context.Context) (crud.Predicate, error) {
			t, err := tenantOf(ctx)
			if err != nil {
				return nil, err
			}
			return crud.Eq("TenantID", t), nil
		},
	}
	seen := 0
	withRule := security.Combine(scopeOnly, security.Policy[Doc, int64]{
		Inspect: func(context.Context, security.Action, *Doc) error { seen++; return nil },
	})

	bare := update(t, scopeOnly, 3)
	ruled := update(t, withRule, 3)

	if seen == 0 {
		t.Fatal("the rule was never shown the row, so the comparison below measures nothing")
	}
	if bare >= ruled {
		t.Fatalf("a policy with no per-row rule cost %d statements and one with a rule cost %d — the gate is still loading a row it does not look at", bare, ruled)
	}

	t.Run("control: an out-of-scope id is still not found", func(t *testing.T) {
		// This is what the load was not needed for. The UPDATE carries the scope,
		// so the row matches nothing and the answer is ErrNotFound rather than a
		// denial — D-008, reached without a read.
		rec := crudtest.Postgres().Push(crudtest.Rows())
		_, err := Docs.Bind(rec, security.Gate(scopeOnly)).
			Update(withTenant(context.Background(), 9), 1, DocUpdate{Title: &title})
		if !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("an out-of-scope update answered %v, want ErrNotFound", err)
		}
	})
}
