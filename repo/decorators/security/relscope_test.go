package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shardit-io/ordo/crud"
	"github.com/shardit-io/ordo/crud/crudtest"
	"github.com/shardit-io/ordo/repo/basic"
	"github.com/shardit-io/ordo/repo/decorators/security"
)

// Scope only ever narrows the statement's own FROM. Everything below is about
// the second table a query reaches — the one a preload selects from, and the one
// a nested filter opens an EXISTS over.

type Folder struct {
	ID       int64   `db:"id,pk,auto"`
	TenantID int64   `db:"tenant_id"`
	Name     string  `db:"name"`
	Notes    []Memo  `rel:"has_many,fk=FolderID"`
	Owner    *Person `rel:"belongs_to,fk=OwnerID"`
	OwnerID  int64   `db:"owner_id"`
}

type Memo struct {
	ID       int64  `db:"id,pk,auto"`
	TenantID int64  `db:"tenant_id"`
	FolderID int64  `db:"folder_id"`
	Text     string `db:"text"`
}

type Person struct {
	ID       int64  `db:"id,pk,auto"`
	TenantID int64  `db:"tenant_id"`
	Name     string `db:"name"`
}

type FolderUpdate struct {
	Name *string
}

var Folders = basic.Define[Folder, int64, FolderUpdate]("folders")

// The policy a multi-tenant application actually needs: the table itself, plus
// every table this repository can reach from it.
var tenantEverywhere = security.Combine(
	security.ScopeField[Folder, int64]("TenantID", tenantOf),
	security.ScopeRelationField[Folder, int64]("Notes", "TenantID", tenantOf),
	security.ScopeRelationField[Folder, int64]("Owner", "TenantID", tenantOf),
)

func folders(rec *crudtest.Recorder, p security.Policy[Folder, int64]) crud.Repo[Folder, int64, FolderUpdate] {
	return Folders.Bind(rec, security.Gate(p))
}

func folderRow(id, tenant int64, name string, owner int64) []any {
	return []any{id, tenant, name, owner}
}

// The leak this closes: a preload is a second statement over a table the scope
// never mentioned, so without a relation narrowing it reads every tenant's rows.
func TestAPreloadIsNarrowedByThePolicy(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(folderRow(1, 7, "mine", 3)), // the folders page
		crudtest.Rows(), // the notes preload
	)

	if _, err := folders(rec, tenantEverywhere).GetAll(ctx, crud.Preload("Notes")); err != nil {
		t.Fatal(err)
	}

	preload := rec.Statements()[1]
	if !strings.Contains(preload.SQL, `"tenant_id" = $`) {
		t.Fatalf("the preload read every tenant's notes:\n%s", preload.SQL)
	}
	if !containsArg(preload.Args, int64(7)) {
		t.Fatalf("preload args = %v, want the principal's tenant in there", preload.Args)
	}
}

// Same statement, no relation narrowing declared: proof that the assertion above
// is testing the policy and not something the preloader does anyway.
func TestAPreloadIsNotNarrowedWithoutTheDeclaration(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(folderRow(1, 7, "mine", 3)),
		crudtest.Rows(),
	)

	onlyTheTable := security.ScopeField[Folder, int64]("TenantID", tenantOf)
	if _, err := folders(rec, onlyTheTable).GetAll(ctx, crud.Preload("Notes")); err != nil {
		t.Fatal(err)
	}
	// Note the shape: tenant_id appears in every projection, so the assertion has
	// to look for it as a comparison or it proves nothing either way.
	if strings.Contains(rec.Statements()[1].SQL, `"tenant_id" = $`) {
		t.Fatal("the preload narrowed itself — the positive test above proves nothing")
	}
}

// A nested filter opens a correlated EXISTS over the target table, and that
// subquery is just as unnarrowed as a preload is.
func TestANestedFilterIsNarrowedByThePolicy(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(crudtest.Rows())

	_, err := folders(rec, tenantEverywhere).GetAll(ctx, crud.Where(crud.Eq("Notes.Text", "hello")))
	if err != nil {
		t.Fatal(err)
	}

	sql := rec.Last().SQL
	if !strings.Contains(sql, "EXISTS") {
		t.Fatalf("expected a correlated subquery:\n%s", sql)
	}
	// Two tenant checks: one on folders, one inside the EXISTS over notes.
	if n := strings.Count(sql, `"tenant_id" = $`); n != 2 {
		t.Fatalf("found %d tenant checks, want 2 (the table and the subquery):\n%s", n, sql)
	}
}

// A to-one preload lands on a different model, and it is narrowed by the same
// declaration.
func TestABelongsToPreloadIsNarrowed(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(folderRow(1, 7, "mine", 3)),
		crudtest.Rows(),
	)

	if _, err := folders(rec, tenantEverywhere).GetAll(ctx, crud.Preload("Owner")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Statements()[1].SQL, `"tenant_id" = $`) {
		t.Fatalf("the owner preload was not narrowed:\n%s", rec.Statements()[1].SQL)
	}
}

// The narrowing has to survive the paths that build their own option lists —
// GetByID and Update do not go through the same code as Get.
func TestTheNarrowingReachesEveryReadPath(t *testing.T) {
	ctx := withTenant(context.Background(), 7)

	for _, tc := range []struct {
		name string
		push []crudtest.Result
		call func(r crud.Repo[Folder, int64, FolderUpdate]) error
	}{
		{"GetByID", []crudtest.Result{crudtest.Rows(folderRow(1, 7, "mine", 3)), crudtest.Rows()},
			func(r crud.Repo[Folder, int64, FolderUpdate]) error {
				_, err := r.GetByID(ctx, 1, crud.Preload("Notes"))
				return err
			}},
		{"Get", []crudtest.Result{crudtest.Rows(folderRow(1, 7, "mine", 3)), crudtest.Rows()},
			func(r crud.Repo[Folder, int64, FolderUpdate]) error {
				_, err := r.Get(ctx, crud.Preload("Notes"), crud.SkipTotal())
				return err
			}},
		{"GetAll", []crudtest.Result{crudtest.Rows(folderRow(1, 7, "mine", 3)), crudtest.Rows()},
			func(r crud.Repo[Folder, int64, FolderUpdate]) error {
				_, err := r.GetAll(ctx, crud.Preload("Notes"))
				return err
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(tc.push...)
			if err := tc.call(folders(rec, tenantEverywhere)); err != nil {
				t.Fatal(err)
			}
			last := rec.Last()
			if !strings.Contains(last.SQL, `"tenant_id" = $`) {
				t.Fatalf("the preload escaped the policy:\n%s", last.SQL)
			}
		})
	}
}

// Count and Exists reach the same second table through a nested filter.
func TestCountAndExistsNarrowTheirSubqueries(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	where := crud.Where(crud.Eq("Notes.Text", "x"))

	for _, tc := range []struct {
		name string
		push crudtest.Result
		call func(r crud.Repo[Folder, int64, FolderUpdate]) error
	}{
		{"Count", crudtest.Rows([]any{int64(0)}),
			func(r crud.Repo[Folder, int64, FolderUpdate]) error { _, err := r.Count(ctx, where); return err }},
		{"Exists", crudtest.Rows(),
			func(r crud.Repo[Folder, int64, FolderUpdate]) error { _, err := r.Exists(ctx, where); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(tc.push)
			if err := tc.call(folders(rec, tenantEverywhere)); err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(rec.Last().SQL, `"tenant_id" = $`); n != 2 {
				t.Fatalf("%d tenant checks, want 2:\n%s", n, rec.Last().SQL)
			}
		})
	}
}

// A caller cannot append its way out of a narrowing, the same way it cannot
// append its way out of Scope.
func TestACallerCannotWidenARelationNarrowing(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(folderRow(1, 7, "mine", 3)),
		crudtest.Rows(),
	)

	// Ask for the notes of every tenant, explicitly.
	_, err := folders(rec, tenantEverywhere).GetAll(ctx,
		crud.PreloadWhere("Notes", crud.Where(crud.IsNotNull("TenantID"))))
	if err != nil {
		t.Fatal(err)
	}
	sql := rec.Statements()[1].SQL
	if !strings.Contains(sql, `"tenant_id" = $`) {
		t.Fatalf("the caller's own filter replaced the policy:\n%s", sql)
	}
}

// Failing to resolve the principal must fail the query, not quietly widen it.
func TestARelationScopeErrorFailsClosed(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(), crudtest.Rows())

	boom := errors.New("no principal")
	p := security.Policy[Folder, int64]{
		RelationScopes: func(context.Context) (*crud.RelationScopes, error) { return nil, boom },
	}
	if _, err := folders(rec, p).GetAll(context.Background(), crud.Preload("Notes")); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the extractor's error", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("%d statements ran after the policy failed", len(rec.Statements()))
	}
}

// Combine has to merge them, not let the last one win.
func TestCombineMergesRelationNarrowings(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(folderRow(1, 7, "mine", 3)),
		crudtest.Rows(),
		crudtest.Rows(),
	)

	if _, err := folders(rec, tenantEverywhere).GetAll(ctx, crud.Preload("Notes"), crud.Preload("Owner")); err != nil {
		t.Fatal(err)
	}
	for _, st := range rec.Statements()[1:] {
		if !strings.Contains(st.SQL, `"tenant_id" = $`) {
			t.Fatalf("one of the two declarations was dropped:\n%s", st.SQL)
		}
	}
}

// Two narrowings of one path AND rather than replace, so composing a role out
// of policies can only ever tighten it.
func TestTwoNarrowingsOfOnePathAreBothApplied(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(crudtest.Rows(folderRow(1, 7, "mine", 3)), crudtest.Rows())

	p := security.Combine(
		security.ScopeRelationField[Folder, int64]("Notes", "TenantID", tenantOf),
		security.Policy[Folder, int64]{
			RelationScopes: func(context.Context) (*crud.RelationScopes, error) {
				return (*crud.RelationScopes)(nil).AtPath("Notes", crud.Eq("Text", "public")), nil
			},
		},
	)
	if _, err := folders(rec, p).GetAll(ctx, crud.Preload("Notes")); err != nil {
		t.Fatal(err)
	}
	sql := rec.Statements()[1].SQL
	if !strings.Contains(sql, `"tenant_id" = $`) || !strings.Contains(sql, `"text" = $`) {
		t.Fatalf("the two narrowings did not compose:\n%s", sql)
	}
}

// A path or a field that does not exist is a declaration bug, and it surfaces
// where it was written rather than as an empty result set months later.
func TestABadRelationDeclarationPanics(t *testing.T) {
	for _, tc := range []struct{ name, path, field string }{
		{"unknown relation", "Nope", "TenantID"},
		{"unknown field on the target", "Notes", "Nope"},
		{"a column where a relation was expected", "Name", "TenantID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("declaring it was accepted")
				}
			}()
			security.ScopeRelationField[Folder, int64](tc.path, tc.field, tenantOf)
		})
	}
}

func containsArg(args []any, want any) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
