package sqlrepo_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

// ---------------------------------------------------------------------------
// declarations

// definePanic runs a declaration that cannot work and returns the error Define
// panicked with. A declaration that is quietly accepted is the failure: Define
// exists so a broken mapping dies at package initialisation instead of on the
// first request.
func definePanic(t *testing.T, define func()) error {
	t.Helper()
	var got error
	func() {
		defer func() {
			switch v := recover().(type) {
			case nil:
			case error:
				got = v
			default:
				t.Errorf("Define panicked with %T (%v); it must panic with an error a caller can inspect", v, v)
			}
		}()
		define()
	}()
	if got == nil {
		t.Fatal("Define accepted a declaration that cannot work")
	}
	return got
}

// Every way a declaration can be wrong, refused at declaration time and named
// in the message — a start-up panic is only useful if it says which part of the
// mapping is broken. TryDefine is the same check without the panic, so the two
// have to agree word for word.
func TestBadDeclarationsAreRefusedAndSayWhy(t *testing.T) {
	type NoKey struct {
		Name string `db:"name"`
	}
	type WrongType struct {
		Name *int
	}
	type Unknown struct {
		Nope *string
	}
	type Frozen struct {
		TenantID *int64
	}
	type Key struct {
		ID *int64
	}
	type Computed struct {
		CreatedAt *string
	}

	for _, tc := range []struct {
		name   string
		try    func() error
		define func()
		says   string
	}{
		{
			"a model that is not a struct",
			func() error { _, err := sqlrepo.TryDefine[int, int, struct{}]("counters"); return err },
			func() { sqlrepo.Define[int, int, struct{}]("counters") },
			"model must be a struct",
		},
		{
			"a model with no primary key",
			func() error { _, err := sqlrepo.TryDefine[NoKey, int64, struct{}]("nokeys"); return err },
			func() { sqlrepo.Define[NoKey, int64, struct{}]("nokeys") },
			"no primary key",
		},
		{
			"an ID type that is not the primary key's",
			func() error { _, err := sqlrepo.TryDefine[User, string, UserUpdate]("users"); return err },
			func() { sqlrepo.Define[User, string, UserUpdate]("users") },
			"repository ID type is string but the primary key is int64",
		},
		{
			"an update DTO that is not a struct",
			func() error { _, err := sqlrepo.TryDefine[User, int64, string]("users"); return err },
			func() { sqlrepo.Define[User, int64, string]("users") },
			"update DTO must be a struct",
		},
		{
			"a DTO field the model does not have",
			func() error { _, err := sqlrepo.TryDefine[User, int64, Unknown]("users"); return err },
			func() { sqlrepo.Define[User, int64, Unknown]("users") },
			"no field Nope on model User",
		},
		{
			"a DTO field of the wrong type",
			func() error { _, err := sqlrepo.TryDefine[User, int64, WrongType]("users"); return err },
			func() { sqlrepo.Define[User, int64, WrongType]("users") },
			"type mismatch: DTO carries int, model field Name is string",
		},
		{
			"a DTO field the model froze",
			func() error { _, err := sqlrepo.TryDefine[User, int64, Frozen]("users"); return err },
			func() { sqlrepo.Define[User, int64, Frozen]("users") },
			"model field TenantID is `immutable`",
		},
		{
			"a DTO that would rewrite the primary key",
			func() error { _, err := sqlrepo.TryDefine[User, int64, Key]("users"); return err },
			func() { sqlrepo.Define[User, int64, Key]("users") },
			"the primary key cannot be updated",
		},
		{
			"a DTO field the database owns",
			func() error { _, err := sqlrepo.TryDefine[User, int64, Computed]("users"); return err },
			func() { sqlrepo.Define[User, int64, Computed]("users") },
			"model field CreatedAt is `generated` and never written",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.try()
			if err == nil {
				t.Fatal("TryDefine accepted a declaration that cannot work")
			}
			var se *crud.SchemaError
			if !errors.As(err, &se) {
				t.Fatalf("TryDefine reported %T (%v), want a *crud.SchemaError a caller can read", err, err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("TryDefine said %q, which never mentions %q", err, tc.says)
			}
			if got := definePanic(t, tc.define); got.Error() != err.Error() {
				t.Fatalf("Define panicked with %q but TryDefine reported %q; the two must be the same check", got, err)
			}
		})
	}
}

// An omitted table name is not a broken declaration: it means "the plural of the
// model", which is what lets the one-line Define in the package documentation be
// the common case.
func TestAnEmptyTableNameBecomesThePluralOfTheModel(t *testing.T) {
	type Story struct {
		ID    int64  `db:"id,pk,auto"`
		Title string `db:"title"`
	}
	rec := crudtest.Postgres().Push(crudtest.Rows())

	if _, err := sqlrepo.New[Story, int64, struct{}](rec, "").GetAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL, `SELECT "id", "title" FROM "stories"`)
}

// ---------------------------------------------------------------------------
// settings

// The two page-size settings are a floor and a ceiling, and the ceiling wins
// even when it is below the floor: a repository that says "20 by default, never
// more than 10" hands out 10, not 20.
func TestMaxLimitCapsEvenTheDefaultLimit(t *testing.T) {
	strict := sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.DefaultLimit(50), sqlrepo.MaxLimit(10))

	for _, tc := range []struct {
		name string
		opts []crud.Option
		want string
	}{
		{"a request with no limit gets the default, clamped", nil, "LIMIT 10"},
		{"a request under the cap is left alone", []crud.Option{crud.Limit(4)}, "LIMIT 4"},
		{"a request over the cap is clamped", []crud.Option{crud.Limit(1000)}, "LIMIT 10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows())
			if _, err := strict.Bind(rec).Get(context.Background(), tc.opts...); err != nil {
				t.Fatal(err)
			}
			if got := mustSQL(t, rec, 0).SQL; !strings.Contains(got, tc.want) {
				t.Fatalf("the page was fetched with %s, want %s", got, tc.want)
			}
		})
	}
}

// A default page size of zero would mean LIMIT 0 — a page with no rows on it —
// so a non-positive setting is read as "not set" and the package default stands.
func TestANonPositiveDefaultLimitFallsBackToThePackageDefault(t *testing.T) {
	for _, n := range []int{0, -5} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows())
			repo := sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.DefaultLimit(n)).Bind(rec)
			if _, err := repo.Get(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := "LIMIT " + strconv.Itoa(sqlrepo.DefaultPageSize)
			if got := mustSQL(t, rec, 0).SQL; !strings.Contains(got, want) {
				t.Fatalf("DefaultLimit(%d) produced %s, want %s", n, got, want)
			}
		})
	}
}

// Define validates the model, the ID and the update DTO, but not the sort terms
// — so a default sort naming a column that is not there survives declaration.
// It cannot survive a query: the statement is refused before it is sent, rather
// than handed to the database to reject.
func TestAnUnknownDefaultSortIsRefusedBeforeTheQueryIsSent(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	repo := sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.DefaultSort(crud.Desc("Nope"))).Bind(rec)

	_, err := repo.Get(context.Background())

	var uf *crud.UnknownFieldError
	if !errors.As(err, &uf) {
		t.Fatalf("err = %v, want an UnknownFieldError naming the sort column", err)
	}
	if uf.Field != "Nope" {
		t.Fatalf("the error blames field %q, want Nope", uf.Field)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a sort that does not resolve still reached the database: %v", rec.SQL())
	}
}

// ---------------------------------------------------------------------------
// preload depth

type Author struct {
	ID    int64  `db:"id,pk,auto"`
	Name  string `db:"name"`
	Books []Book `rel:"has_many"`
}

type Book struct {
	ID       int64  `db:"id,pk,auto"`
	AuthorID int64  `db:"author_id"`
	Title    string `db:"title"`
	Pages    []Page `rel:"has_many"`
}

type Page struct {
	ID     int64 `db:"id,pk,auto"`
	BookID int64 `db:"book_id"`
	Number int   `db:"number"`
}

// PreloadDepth is the guard against a client turning one request into a dozen
// queries by asking for `a.b.a.b`. Zero cannot mean "no hops": Bind has no way
// to tell an explicit zero from an unset setting, so it means "the default".
func TestPreloadDepthCapsAPathAndZeroMeansUnset(t *testing.T) {
	authors := func() crudtest.Result { return crudtest.Rows([]any{int64(1), "Ann"}) }
	books := func() crudtest.Result { return crudtest.Rows([]any{int64(10), int64(1), "Dune"}) }
	pages := func() crudtest.Result { return crudtest.Rows([]any{int64(100), int64(10), 7}) }

	t.Run("one hop is allowed at a depth of one", func(t *testing.T) {
		rec := crudtest.Postgres().Push(authors(), books())
		repo := sqlrepo.Define[Author, int64, struct{}]("authors", sqlrepo.PreloadDepth(1)).Bind(rec)

		got, err := repo.GetAll(context.Background(), crud.Preload("Books"))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || len(got[0].Books) != 1 || got[0].Books[0].Title != "Dune" {
			t.Fatalf("the one allowed hop did not load: %+v", got)
		}
	})

	t.Run("the second hop is refused at a depth of one", func(t *testing.T) {
		rec := crudtest.Postgres().Push(authors())
		repo := sqlrepo.Define[Author, int64, struct{}]("authors", sqlrepo.PreloadDepth(1)).Bind(rec)

		_, err := repo.GetAll(context.Background(), crud.Preload("Books.Pages"))
		if err == nil {
			t.Fatal("a two-segment path was accepted by a repository that allows one")
		}
		if !strings.Contains(err.Error(), "deeper than the allowed 1") {
			t.Fatalf("err = %v, want a refusal naming the depth", err)
		}
		if n := len(rec.Statements()); n != 1 {
			t.Fatalf("%d statements ran, want only the root query: %v", n, rec.SQL())
		}
	})

	t.Run("zero leaves the default depth in place", func(t *testing.T) {
		rec := crudtest.Postgres().Push(authors(), books(), pages())
		repo := sqlrepo.Define[Author, int64, struct{}]("authors", sqlrepo.PreloadDepth(0)).Bind(rec)

		got, err := repo.GetAll(context.Background(), crud.Preload("Books.Pages"))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || len(got[0].Books) != 1 || len(got[0].Books[0].Pages) != 1 {
			t.Fatalf("PreloadDepth(0) refused a two-hop path: %+v", got)
		}
		if got[0].Books[0].Pages[0].Number != 7 {
			t.Fatalf("the second hop loaded the wrong row: %+v", got[0].Books[0].Pages)
		}
	})
}

// ---------------------------------------------------------------------------
// scope

var scopedUsers = sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.Scope(crud.Eq("TenantID", int64(1))))

// A repository scope is permanent. Every statement that has a WHERE clause
// carries it, and a caller's own filter is ANDed onto it rather than replacing
// it.
func TestScopeIsANDedIntoEveryStatementWithAWhereClause(t *testing.T) {
	ctx := context.Background()
	const cols = `"id", "email", "name", "age", "tenant_id", "created_at"`

	for _, tc := range []struct {
		name string
		push []crudtest.Result
		call func(crud.Repo[User, int64, UserUpdate]) error
		want string
	}{
		{"GetAll", []crudtest.Result{crudtest.Rows()},
			func(r crud.Repo[User, int64, UserUpdate]) error { _, err := r.GetAll(ctx); return err },
			`SELECT ` + cols + ` FROM "users" WHERE "tenant_id" = $1`},
		{"GetAll with a caller filter", []crudtest.Result{crudtest.Rows()},
			func(r crud.Repo[User, int64, UserUpdate]) error {
				_, err := r.GetAll(ctx, crud.Where(crud.Eq("Email", "a@b.c")))
				return err
			},
			`SELECT ` + cols + ` FROM "users" WHERE ("tenant_id" = $1 AND "email" = $2)`},
		{"GetByID", []crudtest.Result{crudtest.Rows(userRow(5, "a@b.c", "Ann", 30, 1))},
			func(r crud.Repo[User, int64, UserUpdate]) error { _, err := r.GetByID(ctx, 5); return err },
			`SELECT ` + cols + ` FROM "users" WHERE ("tenant_id" = $1 AND "id" = $2) LIMIT 1`},
		{"Count", []crudtest.Result{crudtest.Rows([]any{int64(0)})},
			func(r crud.Repo[User, int64, UserUpdate]) error { _, err := r.Count(ctx); return err },
			`SELECT count(*) FROM "users" WHERE "tenant_id" = $1`},
		{"Exists", []crudtest.Result{crudtest.Rows()},
			func(r crud.Repo[User, int64, UserUpdate]) error { _, err := r.Exists(ctx); return err },
			`SELECT 1 FROM "users" WHERE "tenant_id" = $1 LIMIT 1`},
		{"DeleteAll", nil,
			func(r crud.Repo[User, int64, UserUpdate]) error { _, err := r.DeleteAll(ctx); return err },
			`DELETE FROM "users" WHERE "tenant_id" = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(tc.push...)
			if err := tc.call(scopedUsers.Bind(rec)); err != nil {
				t.Fatal(err)
			}
			wantSQL(t, mustSQL(t, rec, 0).SQL, tc.want)
			if got := mustSQL(t, rec, 0).Args[0]; got != int64(1) {
				t.Fatalf("the scope bound %#v, want the tenant it was declared with", got)
			}
		})
	}
}

// The caller cannot argue with the scope. A filter on the very column the scope
// pins is ANDed in beside it, which narrows the query to nothing — the one thing
// it must never do is replace it.
func TestACallerFilterCannotWidenTheScope(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())

	if _, err := scopedUsers.Bind(rec).GetAll(context.Background(),
		crud.Where(crud.Eq("TenantID", int64(2)))); err != nil {
		t.Fatal(err)
	}

	wantSQL(t, rec.Last().SQL,
		`SELECT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users" `+
			`WHERE ("tenant_id" = $1 AND "tenant_id" = $2)`)
	if args := rec.Last().Args; len(args) != 2 || args[0] != int64(1) || args[1] != int64(2) {
		t.Fatalf("bound %#v, want the scope's tenant first and the caller's second", args)
	}
}

// Permanent narrowings are independently safe declarations. Repeating Scope
// must retain both predicates — last-wins would turn adding a visibility guard
// into removing the tenant guard it followed.
func TestRepeatedScopesComposeByAND(t *testing.T) {
	repo := sqlrepo.Define[User, int64, UserUpdate]("users",
		sqlrepo.Scope(crud.Eq("TenantID", int64(1))),
		sqlrepo.Scope(crud.Eq("Age", 30)),
	).Bind(crudtest.Postgres().Push(crudtest.Rows()))

	if _, err := repo.GetAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Bind a fresh recorder to inspect the exact declaration-derived SQL: the
	// first repository above proves the declaration remains normally callable.
	rec := crudtest.Postgres().Push(crudtest.Rows())
	repo = sqlrepo.Define[User, int64, UserUpdate]("users",
		sqlrepo.Scope(crud.Eq("TenantID", int64(1))),
		sqlrepo.Scope(crud.Eq("Age", 30)),
	).Bind(rec)
	if _, err := repo.GetAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL,
		`SELECT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users" `+
			`WHERE ("tenant_id" = $1 AND "age" = $2)`)
}

// An UPDATE has a WHERE clause of its own, but it is the primary key alone: the
// scope does its work on the load that precedes it, so a row outside the scope
// is never diffed and never written.
func TestUpdateLoadsThroughTheScopeSoAnOutsideRowIsNotFound(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows()) // the scoped load finds nothing

	_, err := scopedUsers.Bind(rec).Update(context.Background(), 5, UserUpdate{Name: ptr("x")})

	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound for a row outside the scope", err)
	}
	wantSQL(t, mustSQL(t, rec, 0).SQL,
		`SELECT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users" `+
			`WHERE ("tenant_id" = $1 AND "id" = $2) LIMIT 1`)
	if n := len(rec.Statements()); n != 1 {
		t.Fatalf("%d statements ran, want only the load: %v", n, rec.SQL())
	}
}

// Save is an upsert: there is no WHERE clause for a scope to narrow, which the
// Scope documentation says out loud. Pinned here so that the day it changes, it
// changes deliberately — a service method or a security policy is what guards
// this hole today.
func TestScopeCannotReachSave(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(9, "n@x", "New", 18, 3)))
	u := User{Email: "n@x", Name: "New", TenantID: 3} // tenant 3, while the scope pins 1

	if err := scopedUsers.Bind(rec).Save(context.Background(), &u); err != nil {
		t.Fatal(err)
	}
	st := rec.Last()
	wantSQL(t, st.SQL,
		`INSERT INTO "users" ("email", "name", "age", "tenant_id") VALUES ($1, $2, $3, $4) `+
			`RETURNING "id", "email", "name", "age", "tenant_id", "created_at"`)
	if got := st.Args[3]; got != int64(3) {
		t.Fatalf("the insert wrote tenant %#v; if the scope now reaches Save, say so out loud", got)
	}
}
