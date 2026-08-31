package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

type Folder struct {
	ID       int64   `db:"id,pk,auto"`
	TenantID int64   `db:"tenant_id"`
	Name     string  `db:"name"`
	Notes    []Memo  `rel:"has_many,fk=FolderID"`
	Owner    *Person `rel:"belongs_to,fk=OwnerID"`
	OwnerID  int64   `db:"owner_id"`

	DeletedAt crud.Opt[time.Time] `db:"deleted_at"`
}

type Memo struct {
	ID       int64 `db:"id,pk,auto"`
	TenantID int64 `db:"tenant_id"`
	FolderID int64 `db:"folder_id"`

	Author string `db:"author"`
	Text   string `db:"text"`
}

type Person struct {
	ID       int64  `db:"id,pk,auto"`
	TenantID int64  `db:"tenant_id"`
	Name     string `db:"name"`
}

type FolderUpdate struct {
	Name *string
}

var Folders = sqlrepo.Define[Folder, int64, FolderUpdate]("folders")

var SoftFolders = sqlrepo.Define[Folder, int64, FolderUpdate]("folders",
	sqlrepo.SoftDelete("DeletedAt"))

var tenantEverywhere = security.Combine(
	security.ScopeField[Folder, int64]("TenantID", tenantOf),
	security.ScopeRelationField[Folder, int64]("Notes", "TenantID", tenantOf),
	security.ScopeRelationField[Folder, int64]("Owner", "TenantID", tenantOf),
)

func folders(rec *crudtest.Recorder, p security.Policy[Folder, int64]) *crud.Repo[Folder, int64, FolderUpdate] {
	return Folders.Bind(rec, security.Gate(p))
}

func folderRow(id, tenant int64, name string, owner int64) []any {
	return []any{id, tenant, name, owner, nil}
}

func TestAPreloadIsNarrowedByThePolicy(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(folderRow(1, 7, "mine", 3)),
		crudtest.Rows(),
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

	if strings.Contains(rec.Statements()[1].SQL, `"tenant_id" = $`) {
		t.Fatal("the preload narrowed itself — the positive test above proves nothing")
	}
}

func TestAnEmptyRelationScopeFailsClosedUnlessExplicitlyAllowed(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	base := security.ScopeField[Folder, int64]("TenantID", tenantOf)
	missing := security.Combine(base, security.Policy[Folder, int64]{
		RelationScopes: func(context.Context) (*crud.RelationScopes, error) { return nil, nil },
	})
	rec := crudtest.Postgres()
	if _, err := folders(rec, missing).GetAll(ctx, crud.Preload("Notes")); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want a failed relation scope to refuse rather than leak", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a failed relation scope reached SQL: %v", rec.SQL())
	}

	allowed := security.Combine(base, security.Policy[Folder, int64]{
		RelationScopes:              func(context.Context) (*crud.RelationScopes, error) { return nil, nil },
		AllowUnscopedRelationScopes: true,
	})
	rec = crudtest.Postgres().Push(crudtest.Rows())
	if _, err := folders(rec, allowed).GetAll(ctx); err != nil {
		t.Fatalf("an explicit unscoped-relation administrator was refused: %v", err)
	}
}

func TestAnIneffectiveCustomRelationScopeFailsBeforeSQL(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	for _, tc := range []struct {
		name string
		rs   *crud.RelationScopes
	}{
		{"unknown path", (*crud.RelationScopes)(nil).AtPath("Notse", crud.Eq("TenantID", int64(7)))},
		{"tautological path", (*crud.RelationScopes)(nil).AtPath("Notes", crud.True())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres()
			policy := security.Combine(
				security.ScopeField[Folder, int64]("TenantID", tenantOf),
				security.Policy[Folder, int64]{
					RelationScopes: func(context.Context) (*crud.RelationScopes, error) { return tc.rs, nil },
				},
			)
			if _, err := folders(rec, policy).GetAll(ctx, crud.Preload("Notes")); err == nil {
				t.Fatal("an ineffective relation declaration reached the query")
			}
			if len(rec.Statements()) != 0 {
				t.Fatalf("ineffective relation scope reached SQL: %v", rec.SQL())
			}
		})
	}
}

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

	if n := strings.Count(sql, `"tenant_id" = $`); n != 2 {
		t.Fatalf("found %d tenant checks, want 2 (the table and the subquery):\n%s", n, sql)
	}
}

func TestScopedSaveCarriesRelationScopesIntoItsFinalUpdate(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	policy := security.Combine(
		security.ScopeField[Folder, int64]("TenantID", tenantOf),
		security.Policy[Folder, int64]{
			Scope: func(context.Context) (crud.Predicate, error) {
				return crud.Eq("Notes.Text", "visible"), nil
			},
			RelationScopes: func(ctx context.Context) (*crud.RelationScopes, error) {
				tenant, err := tenantOf(ctx)
				if err != nil {
					return nil, err
				}
				return (*crud.RelationScopes)(nil).AtPath("Notes", crud.Eq("TenantID", tenant)), nil
			},
		},
	)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(folderRow(1, 7, "before", 3)),
		crudtest.Rows(folderRow(1, 7, "after", 3)),
	)
	f := Folder{ID: 1, TenantID: 7, Name: "after", OwnerID: 3}
	if _, err := folders(rec, policy).Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	sql := rec.Last().SQL
	if !strings.HasPrefix(sql, "UPDATE") || !strings.Contains(sql, "EXISTS") {
		t.Fatalf("scoped save = %s, want a relation-hopping UPDATE", sql)
	}
	if !strings.Contains(sql, `rx1."tenant_id" = $`) {
		t.Fatalf("the Notes subquery lost its relation scope in final UPDATE:\n%s", sql)
	}
}

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

func TestTheNarrowingReachesEveryReadPath(t *testing.T) {
	ctx := withTenant(context.Background(), 7)

	for _, tc := range []struct {
		name string
		push []crudtest.Result
		call func(r *crud.Repo[Folder, int64, FolderUpdate]) error
	}{
		{"GetByID", []crudtest.Result{crudtest.Rows(folderRow(1, 7, "mine", 3)), crudtest.Rows()},
			func(r *crud.Repo[Folder, int64, FolderUpdate]) error {
				_, err := r.GetByID(ctx, 1, crud.Preload("Notes"))
				return err
			}},
		{"Get", []crudtest.Result{crudtest.Rows(folderRow(1, 7, "mine", 3)), crudtest.Rows()},
			func(r *crud.Repo[Folder, int64, FolderUpdate]) error {
				_, err := r.Get(ctx, crud.Preload("Notes"), crud.SkipTotal())
				return err
			}},
		{"GetAll", []crudtest.Result{crudtest.Rows(folderRow(1, 7, "mine", 3)), crudtest.Rows()},
			func(r *crud.Repo[Folder, int64, FolderUpdate]) error {
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

func TestCountAndExistsNarrowTheirSubqueries(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	where := crud.Where(crud.Eq("Notes.Text", "x"))

	for _, tc := range []struct {
		name string
		push crudtest.Result
		call func(r *crud.Repo[Folder, int64, FolderUpdate]) error
	}{
		{"Count", crudtest.Rows([]any{int64(0)}),
			func(r *crud.Repo[Folder, int64, FolderUpdate]) error { _, err := r.Count(ctx, where); return err }},
		{"Exists", crudtest.Rows(),
			func(r *crud.Repo[Folder, int64, FolderUpdate]) error { _, err := r.Exists(ctx, where); return err }},
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

func TestACallerCannotWidenARelationNarrowing(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(
		crudtest.Rows(folderRow(1, 7, "mine", 3)),
		crudtest.Rows(),
	)

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

func TestABadRelationDeclarationPanics(t *testing.T) {
	for _, tc := range []struct{ name, path, field string }{
		{"unknown relation", "Nope", "TenantID"},
		{"unknown field on the target", "Notes", "Nope"},
		{"a column where a relation was expected", "Name", "TenantID"},
	} {
		for _, form := range []struct {
			name    string
			declare func(path, field string)
		}{
			{"ScopeRelationField", func(path, field string) {
				security.ScopeRelationField[Folder, int64](path, field, tenantOf)
			}},
			{"ScopeRelationAttr", func(path, field string) {
				security.ScopeRelationAttr[Folder, int64](path, field, "tenant")
			}},
			{"ScopeRelationSubject", func(path, field string) {
				security.ScopeRelationSubject[Folder, int64](path, field)
			}},
		} {
			t.Run(tc.name+"/"+form.name, func(t *testing.T) {
				defer func() {
					if recover() == nil {
						t.Fatal("declaring it was accepted, so the mistake would surface as a leak at request time")
					}
				}()
				form.declare(tc.path, tc.field)
			})
		}
	}

	security.ScopeRelationField[Folder, int64]("Notes", "TenantID", tenantOf)
	security.ScopeRelationAttr[Folder, int64]("Notes", "TenantID", "tenant")
	security.ScopeRelationSubject[Folder, int64]("Notes", "Author")
}

func containsArg(args []any, want any) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestEveryStatementAGatedCallIssuesCarriesTheNarrowing(t *testing.T) {
	policy := security.Combine(
		security.ScopeField[Folder, int64]("TenantID", tenantOf),
		security.ScopeRelationField[Folder, int64]("Notes", "TenantID", tenantOf),
	)
	ctx := withTenant(context.Background(), 7)

	t.Run("the page total's COUNT", func(t *testing.T) {
		rec := crudtest.Postgres().Push(
			crudtest.Rows(folderRow(1, 7, "mine", 1)),
			crudtest.Rows([]any{int64(1)}),
		)
		if _, err := Folders.Bind(rec, security.Gate(policy)).
			Get(ctx, crud.Where(crud.Eq("Notes.Text", "secret")), crud.Limit(1)); err != nil {
			t.Fatal(err)
		}
		if n := len(rec.Statements()); n != 2 {
			t.Fatalf("%d statements ran, want the page and its total — without the COUNT this test inspects nothing", n)
		}

		count := rec.Statements()[len(rec.Statements())-1]
		if !strings.Contains(count.SQL, `rx1."tenant_id"`) {
			t.Fatalf("the total counts rows the gate hides:\n%s", count.SQL)
		}
	})

	t.Run("the soft-delete stamp", func(t *testing.T) {
		hard := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
		soft := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})

		if _, err := Folders.Bind(hard, security.Gate(policy)).
			DeleteAll(ctx, crud.Where(crud.Eq("Notes.Text", "secret"))); err != nil {
			t.Fatal(err)
		}
		if _, err := SoftFolders.Bind(soft, security.Gate(policy)).
			DeleteAll(ctx, crud.Where(crud.Eq("Notes.Text", "secret"))); err != nil {
			t.Fatal(err)
		}

		h, s := hard.Last().SQL, soft.Last().SQL
		if !strings.Contains(h, `rx1."tenant_id"`) {
			t.Fatalf("the control failed: the hard DELETE is not narrowed either, so this test proves nothing:\n%s", h)
		}
		if !strings.Contains(s, `rx1."tenant_id"`) {
			t.Fatalf("the soft-delete UPDATE writes rows the policy hides:\n%s", s)
		}
	})

	t.Run("the DELETE behind Delete(ids...)", func(t *testing.T) {
		hopping := security.Combine(
			security.Policy[Folder, int64]{
				Scope: func(context.Context) (crud.Predicate, error) {
					return crud.Eq("Notes.Text", "secret"), nil
				},
			},
			security.ScopeRelationField[Folder, int64]("Notes", "TenantID", tenantOf),
		)

		byID := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
		byFilter := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})

		if _, err := Folders.Bind(byID, security.Gate(hopping)).Delete(ctx, 5); err != nil {
			t.Fatal(err)
		}
		if _, err := Folders.Bind(byFilter, security.Gate(hopping)).
			DeleteAll(ctx, crud.Where(crud.Eq("ID", 5))); err != nil {
			t.Fatal(err)
		}

		a, b := byID.Last().SQL, byFilter.Last().SQL
		if !strings.Contains(b, `rx1."tenant_id"`) {
			t.Fatalf("the control failed: DeleteAll is not narrowed either, so this proves nothing:\n%s", b)
		}
		if !strings.Contains(a, `rx1."tenant_id"`) {
			t.Fatalf("Delete(id) issues an unnarrowed DELETE where DeleteAll narrows:\n  Delete:    %s\n  DeleteAll: %s", a, b)
		}
	})
}

func whereOf(sql string) string {
	_, clause, _ := strings.Cut(sql, " WHERE ")
	return clause
}
