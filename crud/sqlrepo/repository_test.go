package sqlrepo_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

type User struct {
	ID        int64         `db:"id,pk,auto"`
	Email     string        `db:"email"`
	Name      string        `db:"name"`
	Age       crud.Opt[int] `db:"age"`
	TenantID  int64         `db:"tenant_id,immutable"`
	CreatedAt time.Time     `db:"created_at,generated"`
}

type UserUpdate struct {
	Email *string
	Name  *string
	Age   crud.Opt[int]
}

type Doc struct {
	ID    string `db:"id,pk"`
	Title string `db:"title"`
}

type DocUpdate struct {
	Title *string
}

type UnsignedUser struct {
	ID   uint   `db:"id,pk,auto"`
	Name string `db:"name"`
}

type UnsignedUserUpdate struct {
	Name *string
}

var (
	Users         = sqlrepo.Define[User, int64, UserUpdate]("users")
	Docs          = sqlrepo.Define[Doc, string, DocUpdate]("docs")
	UnsignedUsers = sqlrepo.Define[UnsignedUser, uint, UnsignedUserUpdate]("unsigned_users")
)

var now = time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

func userRow(id int64, email, name string, age any, tenant int64) []any {
	return []any{id, email, name, age, tenant, now}
}

func TestARepositoryCannotHideAnOlderExecutorDeclarationFailure(t *testing.T) {
	rec := crudtest.Postgres()
	ctx := (crud.Session{}).Bind(context.Background())
	ctx = crud.BindExecutor(ctx, rec, rec)

	err := Docs.Bind(rec).SaveOnly(ctx, &Doc{ID: "one", Title: "must not run"})
	if !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("SaveOnly returned %v, want ErrExecutorScope", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("the poisoned context reached the datasource: %v", rec.SQL())
	}
}

func ptr[T any](v T) *T { return &v }

func mustSQL(t *testing.T, rec *crudtest.Recorder, i int) crudtest.Statement {
	t.Helper()
	st := rec.Statements()
	if i >= len(st) {
		t.Fatalf("wanted statement %d, only %d recorded: %v", i, len(st), rec.SQL())
	}
	return st[i]
}

func wantSQL(t *testing.T, got, want string) {
	t.Helper()
	if crudtest.Normalize(got) != crudtest.Normalize(want) {
		t.Errorf("SQL mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestGetByID(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)))
	users := Users.Bind(rec)

	u, err := users.GetByID(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL,
		`SELECT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users" WHERE "id" = $1 LIMIT 1`)
	if u.Email != "a@b.c" || u.Name != "Ann" {
		t.Fatalf("scanned %+v", u)
	}
	if age, ok := u.Age.Get(); !ok || age != 30 {
		t.Fatalf("age = %v", u.Age)
	}
	if !u.CreatedAt.Equal(now) {
		t.Fatalf("created_at = %v", u.CreatedAt)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := Users.Bind(rec).GetByID(context.Background(), 42); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestNullableColumnScansToNull(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(1, "a@b.c", "Ann", nil, 7)))
	u, err := Users.Bind(rec).GetByID(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !u.Age.IsNull() || u.Age.IsSet() {
		t.Fatalf("age = %v, want null", u.Age)
	}
}

func TestGetPaginated(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(userRow(3, "c@x", "C", 20, 1), userRow(4, "d@x", "D", 21, 1)),
		crudtest.Rows([]any{int64(57)}),
	)
	page, err := Users.Bind(rec).Get(context.Background(),
		crud.Where(crud.Eq("TenantID", 1)),
		crud.Page(2), crud.Limit(2),
		crud.OrderBy(crud.Desc("CreatedAt")),
	)
	if err != nil {
		t.Fatal(err)
	}
	wantSQL(t, mustSQL(t, rec, 0).SQL,
		`SELECT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users" WHERE "tenant_id" = $1 `+
			`ORDER BY "created_at" DESC, "id" ASC LIMIT 2 OFFSET 2`)
	wantSQL(t, mustSQL(t, rec, 1).SQL, `SELECT count(*) FROM "users" WHERE "tenant_id" = $1`)

	if page.Total != 57 || page.TotalPages != 29 || page.Page != 2 || page.Limit != 2 {
		t.Fatalf("pager = %+v", page)
	}
	if !page.HasNext || !page.HasPrev {
		t.Fatalf("pager flags = %+v", page)
	}
}

func TestGetSkipsCountOnShortFirstPage(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(1, "a", "A", 1, 1)))
	page, err := Users.Bind(rec).Get(context.Background(), crud.Limit(20))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(rec.Statements()); n != 1 {
		t.Fatalf("%d statements, want 1 (no COUNT): %v", n, rec.SQL())
	}
	if page.Total != 1 || page.HasNext {
		t.Fatalf("pager = %+v", page)
	}
}

func TestSkipTotalProbesOneExtraRow(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(
		userRow(1, "a", "A", 1, 1), userRow(2, "b", "B", 2, 1), userRow(3, "c", "C", 3, 1),
	))
	page, err := Users.Bind(rec).Get(context.Background(), crud.Limit(2), crud.SkipTotal())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Last().SQL, "LIMIT 3") {
		t.Fatalf("expected a limit+1 probe: %s", rec.Last().SQL)
	}
	if len(page.Items) != 2 || !page.HasNext {
		t.Fatalf("page = %+v", page)
	}
	if len(rec.Statements()) != 1 {
		t.Fatalf("SkipTotal still ran a COUNT: %v", rec.SQL())
	}
}

func TestGetAllIsUnpaged(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := Users.Bind(rec).GetAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Last().SQL, "LIMIT") {
		t.Fatalf("GetAll should not paginate: %s", rec.Last().SQL)
	}
}

func TestDistinctWithoutAProjectionStillNamesItsColumns(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := Users.Bind(rec).GetAll(context.Background(), crud.Distinct()); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL,
		`SELECT DISTINCT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users"`)
}

func TestDistinctProjectsOnlyWhatWasSelected(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := Users.Bind(rec).GetAll(context.Background(),
		crud.Distinct(), crud.Select("Email")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Last().SQL, `"id"`) {
		t.Fatalf("the primary key is in a DISTINCT projection, so no two rows can ever collapse: %s", rec.Last().SQL)
	}
	wantSQL(t, rec.Last().SQL, `SELECT DISTINCT "email" FROM "users"`)
}

func TestDistinctRefusesAPreloadItCannotAttach(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())

	_, err := sqlrepo.Define[Book, int64, struct{}]("books").Bind(rec).GetAll(context.Background(),
		crud.Distinct(), crud.Select("Title"), crud.Preload("Pages"))

	var se *crud.SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *crud.SchemaError naming the conflict", err)
	}
	if !strings.Contains(se.Error(), "Pages") {
		t.Fatalf("err = %v, want the refusal to name the preload", err)
	}
	if n := len(rec.Statements()); n != 0 {
		t.Fatalf("%d statements reached the database: %v", n, rec.SQL())
	}
}

func TestMaxLimitClamps(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(), crudtest.Rows([]any{int64(0)}))
	repository := sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.MaxLimit(50)).Bind(rec)
	if _, err := repository.Get(context.Background(), crud.Limit(1000)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustSQL(t, rec, 0).SQL, "LIMIT 50") {
		t.Fatalf("limit was not clamped: %s", mustSQL(t, rec, 0).SQL)
	}
}

func TestFirstUsesReadOptionsAndReturnsNotFound(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(9, "n@x", "New", 18, 3)))
	u, err := Users.Bind(rec).First(context.Background(),
		crud.Where(crud.Eq("TenantID", int64(3))), crud.OrderBy(crud.Desc("Name")), crud.Limit(99))
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != 9 {
		t.Fatalf("First returned %+v", u)
	}
	wantSQL(t, rec.Last().SQL,
		`SELECT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users" WHERE "tenant_id" = $1 ORDER BY "name" DESC, "id" ASC LIMIT 1`)

	_, err = Users.Bind(crudtest.Postgres().Push(crudtest.Rows())).First(context.Background())
	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSaveInsertsWithGeneratedKeyOnPostgres(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(9, "n@x", "New", 18, 3)))
	u := User{Email: "n@x", Name: "New", Age: crud.Set(18), TenantID: 3}

	saved, err := Users.Bind(rec).Save(context.Background(), &u)
	if err != nil {
		t.Fatal(err)
	}
	st := rec.Last()
	wantSQL(t, st.SQL,
		`INSERT INTO "users" ("email", "name", "age", "tenant_id") VALUES ($1, $2, $3, $4) `+
			`RETURNING "id", "email", "name", "age", "tenant_id", "created_at"`)
	if len(st.Args) != 4 {
		t.Fatalf("args = %v", st.Args)
	}
	if saved.ID != 9 || !saved.CreatedAt.Equal(now) {
		t.Fatalf("returned model is not from RETURNING: %+v", saved)
	}
	if u.ID != 0 || !u.CreatedAt.IsZero() {
		t.Fatalf("Save mutated its argument: %+v", u)
	}
}

func TestSaveOnlyWritesWithoutReturningOrMutation(t *testing.T) {
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	u := User{Email: "n@x", Name: "New", Age: crud.Set(18), TenantID: 3}

	if err := Users.Bind(rec).SaveOnly(context.Background(), &u); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL,
		`INSERT INTO "users" ("email", "name", "age", "tenant_id") VALUES ($1, $2, $3, $4)`)
	if u.ID != 0 || !u.CreatedAt.IsZero() {
		t.Fatalf("SaveOnly mutated its argument: %+v", u)
	}
}

func TestSaveUpsertsWhenKeyIsSet(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(9, "n@x", "New", 18, 3)))
	u := User{ID: 9, Email: "n@x", Name: "New", Age: crud.Set(18), TenantID: 3}

	if _, err := Users.Bind(rec).Save(context.Background(), &u); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL,
		`INSERT INTO "users" ("id", "email", "name", "age", "tenant_id") VALUES ($1, $2, $3, $4, $5) `+
			`ON CONFLICT ("id") DO UPDATE SET "email" = EXCLUDED."email", "name" = EXCLUDED."name", `+
			`"age" = EXCLUDED."age" `+
			`RETURNING "id", "email", "name", "age", "tenant_id", "created_at"`)
}

func TestSaveOnMySQLUsesLastInsertID(t *testing.T) {
	rec := crudtest.MySQL().
		ExecResult(crud.Result{RowsAffected: 1, LastInsertID: 77, HasLastInsertID: true}).
		Push(crudtest.Rows(userRow(77, "n@x", "New", 18, 3)))

	u := User{Email: "n@x", Name: "New", Age: crud.Set(18), TenantID: 3}
	saved, err := Users.Bind(rec).Save(context.Background(), &u)
	if err != nil {
		t.Fatal(err)
	}
	wantSQL(t, mustSQL(t, rec, 0).SQL,
		"INSERT INTO `users` (`email`, `name`, `age`, `tenant_id`) VALUES (?, ?, ?, ?)")

	wantSQL(t, mustSQL(t, rec, 1).SQL,
		"SELECT `id`, `email`, `name`, `age`, `tenant_id`, `created_at` FROM `users` WHERE `id` = ? LIMIT 1")
	if saved.ID != 77 {
		t.Fatalf("id = %d, want 77", saved.ID)
	}
	if u.ID != 0 {
		t.Fatalf("Save mutated its argument: %+v", u)
	}
}

func TestSaveOnMySQLLetsTheScannerAssignAnUnsignedGeneratedID(t *testing.T) {
	rec := crudtest.MySQL().
		ExecResult(crud.Result{RowsAffected: 1, LastInsertID: 77, HasLastInsertID: true}).
		Push(crudtest.Rows([]any{int64(77), "New"}))

	command := UnsignedUser{Name: "New"}
	saved, err := UnsignedUsers.Bind(rec).Save(context.Background(), &command)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID != 77 || command.ID != 0 {
		t.Fatalf("saved=%+v command=%+v; generated key was not scanned or the command was mutated", saved, command)
	}
	wantSQL(t, mustSQL(t, rec, 1).SQL,
		"SELECT `id`, `name` FROM `unsigned_users` WHERE `id` = ? LIMIT 1")
	if got := mustSQL(t, rec, 1).Args; len(got) != 1 || got[0] != int64(77) {
		t.Fatalf("refresh args = %#v, want the driver's int64 generated key", got)
	}
}

func TestSaveOnADialectWithoutRETURNINGReadsTheRowBack(t *testing.T) {
	rec := crudtest.MySQL().
		ExecResult(crud.Result{RowsAffected: 1}).
		Push(crudtest.Rows([]any{"abc", "what the table actually holds"}))

	d := Doc{ID: "abc", Title: "what the caller asked for"}
	saved, err := Docs.Bind(rec).Save(context.Background(), &d)
	if err != nil {
		t.Fatal(err)
	}

	wantSQL(t, mustSQL(t, rec, 0).SQL, "UPDATE `docs` SET `title` = ? WHERE `id` = ?")
	wantSQL(t, mustSQL(t, rec, 1).SQL, "SELECT `id`, `title` FROM `docs` WHERE `id` = ? LIMIT 1")
	if saved.Title != "what the table actually holds" {
		t.Fatalf("Save returned %q, want the stored value", saved.Title)
	}
	if d.Title != "what the caller asked for" {
		t.Fatalf("Save mutated its argument to %q", d.Title)
	}
}

func TestSaveRequiresAssignedKeyWhenNotGenerated(t *testing.T) {
	rec := crudtest.Postgres()
	d := Doc{Title: "no id"}
	if _, err := Docs.Bind(rec).Save(context.Background(), &d); !errors.Is(err, crud.ErrMissingID) {
		t.Fatalf("err = %v, want ErrMissingID", err)
	}
}

func TestUpdateWritesOnlyChangedFields(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)),
		crudtest.Rows(userRow(1, "a@b.c", "Anna", 30, 7)),
	)
	u, err := Users.Bind(rec).Update(context.Background(), 1, UserUpdate{
		Email: ptr("a@b.c"),
		Name:  ptr("Anna"),
	})
	if err != nil {
		t.Fatal(err)
	}
	st := mustSQL(t, rec, 1)
	wantSQL(t, st.SQL,
		`UPDATE "users" SET "name" = $1 WHERE "id" = $2 `+
			`RETURNING "id", "email", "name", "age", "tenant_id", "created_at"`)
	if len(st.Args) != 2 || st.Args[0] != "Anna" {
		t.Fatalf("args = %v", st.Args)
	}
	if u.Name != "Anna" {
		t.Fatalf("model = %+v", u)
	}
}

func TestUpdateWithNothingToDoSkipsTheWrite(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)))
	u, err := Users.Bind(rec).Update(context.Background(), 1, UserUpdate{
		Name: ptr("Ann"),
		Age:  crud.Set(30),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(rec.Statements()); n != 1 {
		t.Fatalf("%d statements, want only the load: %v", n, rec.SQL())
	}
	if u.Name != "Ann" {
		t.Fatalf("model = %+v", u)
	}
}

func TestUpdateUsesAFullMutationReadAndKeepsOnlyItsNarrowing(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)))

	got, err := Users.Bind(rec).Update(context.Background(), 1,
		UserUpdate{Name: ptr("Ann")},
		crud.Where(crud.Eq("TenantID", 7)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "a@b.c" || got.Name != "Ann" {
		t.Fatalf("mutation read returned a partial row: %+v", got)
	}
	if n := len(rec.Statements()); n != 1 {
		t.Fatalf("%d statements, want the full load and a no-op diff: %v", n, rec.SQL())
	}
	wantSQL(t, rec.Last().SQL,
		`SELECT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users" `+
			`WHERE ("tenant_id" = $1 AND "id" = $2) LIMIT 1`)
}

func TestMutationReadPreservesExplicitAndTransactionalLocks(t *testing.T) {
	assertLocked := func(t *testing.T, run func(*crudtest.Recorder, *crud.Repo[User, int64, UserUpdate]) error) {
		t.Helper()
		rec := crudtest.Postgres().Push(crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)))
		users := Users.Bind(rec)
		if err := run(rec, users); err != nil {
			t.Fatal(err)
		}
		if n := len(rec.Statements()); n != 1 {
			t.Fatalf("%d statements, want one mutation read: %v", n, rec.SQL())
		}
		if sql := rec.Last().SQL; !strings.HasSuffix(sql, " LIMIT 1 FOR UPDATE") {
			t.Fatalf("mutation read lost its row lock: %s", sql)
		}
	}

	t.Run("explicit lock", func(t *testing.T) {
		assertLocked(t, func(_ *crudtest.Recorder, users *crud.Repo[User, int64, UserUpdate]) error {
			_, err := users.Update(context.Background(), 1, UserUpdate{Name: ptr("Ann")}, crud.ForUpdate())
			return err
		})
	})

	t.Run("transaction lock", func(t *testing.T) {
		assertLocked(t, func(_ *crudtest.Recorder, users *crud.Repo[User, int64, UserUpdate]) error {
			return users.Tx(context.Background(), func(ctx context.Context) error {
				_, err := users.Update(ctx, 1, UserUpdate{Name: ptr("Ann")})
				return err
			})
		})
	})
}

func TestUpdateDistinguishesUndefinedFromNull(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)),
		crudtest.Rows(userRow(1, "a@b.c", "Anna", 30, 7)),
	)
	if _, err := Users.Bind(rec).Update(context.Background(), 1, UserUpdate{Name: ptr("Anna")}); err != nil {
		t.Fatal(err)
	}
	if set, _, _ := strings.Cut(mustSQL(t, rec, 1).SQL, " RETURNING "); strings.Contains(set, `"age"`) {
		t.Fatalf("undefined Opt leaked into the statement: %s", set)
	}

	rec = crudtest.Postgres().Push(
		crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)),
		crudtest.Rows(userRow(1, "a@b.c", "Ann", nil, 7)),
	)
	u, err := Users.Bind(rec).Update(context.Background(), 1, UserUpdate{Age: crud.Null[int]()})
	if err != nil {
		t.Fatal(err)
	}
	st := mustSQL(t, rec, 1)
	wantSQL(t, st.SQL, `UPDATE "users" SET "age" = $1 WHERE "id" = $2 `+
		`RETURNING "id", "email", "name", "age", "tenant_id", "created_at"`)
	if st.Args[0] != nil {
		t.Fatalf("args = %v, want a NULL", st.Args)
	}
	if !u.Age.IsNull() {
		t.Fatalf("age = %v, want null", u.Age)
	}

	rec = crudtest.Postgres().Push(
		crudtest.Rows(userRow(1, "a@b.c", "Ann", nil, 7)),
		crudtest.Rows(userRow(1, "a@b.c", "Ann", 41, 7)),
	)
	if _, err := Users.Bind(rec).Update(context.Background(), 1, UserUpdate{Age: crud.Set(41)}); err != nil {
		t.Fatal(err)
	}
	if got := mustSQL(t, rec, 1).Args[0]; got != 41 {
		t.Fatalf("arg = %#v, want 41", got)
	}
}

func TestUpdateOnADialectWithoutRETURNINGReadsTheRowBack(t *testing.T) {
	rec := crudtest.MySQL().
		ExecResult(crud.Result{RowsAffected: 1}).
		Push(
			crudtest.Rows([]any{"abc", "old"}),
			crudtest.Rows([]any{"abc", "NEW (trimmed)"}),
		)

	d, err := Docs.Bind(rec).Update(context.Background(), "abc", DocUpdate{Title: ptr("new")})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(rec.Statements()); n != 3 {
		t.Fatalf("%d statements, want load+update+read-back: %v", n, rec.SQL())
	}
	wantSQL(t, mustSQL(t, rec, 1).SQL, "UPDATE `docs` SET `title` = ? WHERE `id` = ?")
	wantSQL(t, mustSQL(t, rec, 2).SQL, "SELECT `id`, `title` FROM `docs` WHERE `id` = ? LIMIT 1")
	if d.Title != "NEW (trimmed)" {
		t.Fatalf("model = %+v; the DTO was applied in memory instead of reading the row", d)
	}
}

func TestUpdateOfARowThatVanishedIsNotFoundOnEveryDialect(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  *crudtest.Recorder
	}{
		{"postgres", crudtest.Postgres().Push(
			crudtest.Rows([]any{"abc", "old"}),
			crudtest.Rows(),
		)},
		{"mysql", crudtest.MySQL().
			ExecResult(crud.Result{RowsAffected: 0}).
			Push(
				crudtest.Rows([]any{"abc", "old"}),
				crudtest.Rows(),
			)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Docs.Bind(tc.rec).Update(context.Background(), "abc", DocUpdate{Title: ptr("new")})
			if !errors.Is(err, crud.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound: the row was deleted under the update", err)
			}
		})
	}
}

func TestUpdateMissingRow(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := Users.Bind(rec).Update(context.Background(), 5, UserUpdate{Name: ptr("x")}); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDelete(t *testing.T) {
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	n, err := Users.Bind(rec).Delete(context.Background(), 4)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	wantSQL(t, rec.Last().SQL, `DELETE FROM "users" WHERE "id" = $1`)

	rec.Reset()
	rec.ExecResult(crud.Result{RowsAffected: 3})
	if _, err := Users.Bind(rec).Delete(context.Background(), 4, 5, 6); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL, `DELETE FROM "users" WHERE "id" IN ($1, $2, $3)`)
}

func TestDeleteNothingIsANoop(t *testing.T) {
	rec := crudtest.Postgres()
	if n, err := Users.Bind(rec).Delete(context.Background()); n != 0 || err != nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("statements: %v", rec.SQL())
	}
}

func TestDeleteAll(t *testing.T) {
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 12})
	if _, err := Users.Bind(rec).DeleteAll(context.Background(), crud.Where(crud.Lt("Age", 18))); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL, `DELETE FROM "users" WHERE "age" < $1`)

	rec.Reset()
	if _, err := Users.Bind(rec).DeleteAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL, `DELETE FROM "users"`)
}

func TestAggregateIsUnpagedByDefaultAndExplicitPagingKeepsTheCap(t *testing.T) {
	rows := crudtest.Rows([]any{int64(7), int64(3)})

	t.Run("default summary has no hidden page", func(t *testing.T) {
		rec := crudtest.Postgres().Push(rows)
		got, err := Users.Bind(rec).Aggregate(context.Background(),
			crud.GroupBy("TenantID"), crud.Aggregate(crud.CountAll("n")))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("rows = %d", len(got))
		}
		wantSQL(t, rec.Last().SQL,
			`SELECT "tenant_id", COUNT(*) FROM "users" GROUP BY "tenant_id"`)
	})

	t.Run("an explicit page is capped", func(t *testing.T) {
		rec := crudtest.Postgres().Push(rows)
		bounded := sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.MaxLimit(5)).Bind(rec)
		if _, err := bounded.Aggregate(context.Background(),
			crud.GroupBy("TenantID"), crud.Aggregate(crud.CountAll("n")), crud.Limit(99)); err != nil {
			t.Fatal(err)
		}
		wantSQL(t, rec.Last().SQL,
			`SELECT "tenant_id", COUNT(*) FROM "users" GROUP BY "tenant_id" LIMIT 5`)
	})

	t.Run("Unpaged cannot remove a declared cap", func(t *testing.T) {
		rec := crudtest.Postgres().Push(rows)
		bounded := sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.MaxLimit(5)).Bind(rec)
		if _, err := bounded.Aggregate(context.Background(),
			crud.GroupBy("TenantID"), crud.Aggregate(crud.CountAll("n")), crud.Unpaged()); err != nil {
			t.Fatal(err)
		}
		wantSQL(t, rec.Last().SQL,
			`SELECT "tenant_id", COUNT(*) FROM "users" GROUP BY "tenant_id" LIMIT 5`)
	})
}

func TestAggregateRefusesAnUngroupedSortBeforeQuery(t *testing.T) {
	rec := crudtest.Postgres()
	_, err := Users.Bind(rec).Aggregate(context.Background(),
		crud.GroupBy("TenantID"),
		crud.Aggregate(crud.CountAll("n")),
		crud.OrderBy(crud.Desc("Email")),
	)
	if err == nil {
		t.Fatal("ungrouped aggregate sort reached the database")
	}
	if n := len(rec.Statements()); n != 0 {
		t.Fatalf("refused aggregate issued %d statements: %v", n, rec.SQL())
	}
}

func TestCountAndExists(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(4)}), crudtest.Rows([]any{1}))
	repository := Users.Bind(rec)

	n, err := repository.Count(context.Background(), crud.Where(crud.Eq("TenantID", 2)))
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	wantSQL(t, mustSQL(t, rec, 0).SQL, `SELECT count(*) FROM "users" WHERE "tenant_id" = $1`)

	ok, err := repository.Exists(context.Background(), crud.Where(crud.Eq("Email", "a@b")))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	wantSQL(t, mustSQL(t, rec, 1).SQL, `SELECT 1 FROM "users" WHERE "email" = $1 LIMIT 1`)
}

func TestUnknownFieldIsAnError(t *testing.T) {
	rec := crudtest.Postgres()
	_, err := Users.Bind(rec).GetAll(context.Background(), crud.Where(crud.Eq("Nope", 1)))
	var ufe *crud.UnknownFieldError
	if !errors.As(err, &ufe) {
		t.Fatalf("err = %v, want UnknownFieldError", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatal("a broken predicate must not reach the database")
	}
}

func TestPredicateComposition(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	_, err := Users.Bind(rec).GetAll(context.Background(), crud.Where(crud.And(
		crud.Or(crud.Eq("Name", "a"), crud.Eq("Name", "b")),
		crud.Not(crud.IsNull("Age")),
		crud.Between("CreatedAt", now, now),
		crud.InAny("TenantID", []int64{1, 2}),
		crud.Contains("Email", "50%"),
		crud.Raw(`lower("email") <> ?`, "x"),
	)))
	if err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL,
		`SELECT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users" WHERE `+
			`(("name" = $1 OR "name" = $2) AND NOT ("age" IS NULL) AND "created_at" BETWEEN $3 AND $4 `+
			`AND "tenant_id" IN ($5, $6) AND "email" LIKE $7 ESCAPE '\' AND lower("email") <> $8)`)
	if got := rec.Last().Args[6]; got != `%50\%%` {
		t.Fatalf("LIKE argument = %q, want the %% escaped", got)
	}
}

func TestTransactionJoinsAnAmbientExecutor(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(1, "a", "A", 1, 1)))
	users := Users.Bind(rec)

	outer := crudtest.Postgres().Push(crudtest.Rows(userRow(2, "b", "B", 2, 1)))
	ctx := crud.BindExecutor(context.Background(), rec, outer)

	if err := users.Tx(ctx, func(ctx context.Context) error {
		_, err := users.GetByID(ctx, 1)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if rec.TxDepth() != 0 {
		t.Fatal("a foreign executor in the context must not start a new transaction")
	}
	if len(outer.Statements()) != 1 || len(rec.Statements()) != 0 {
		t.Fatalf("the query did not run on the ambient executor: outer=%v own=%v", outer.SQL(), rec.SQL())
	}
}

func TestTransactionRollsBackOnError(t *testing.T) {
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	users := Users.Bind(rec)
	boom := errors.New("boom")

	err := users.Tx(context.Background(), func(ctx context.Context) error {
		_, _ = users.Delete(ctx, 1)
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if rec.TxDepth() != 1 {
		t.Fatalf("tx depth = %d", rec.TxDepth())
	}
}

func TestBadDeclarationsPanicEarly(t *testing.T) {
	type NoKey struct {
		Name string `db:"name"`
	}
	type Mismatch struct {
		Name *int
	}
	type Unknown struct {
		Nope *string
	}
	type Immutable struct {
		TenantID *int64
	}

	if _, err := sqlrepo.TryDefine[NoKey, int64, struct{}]("x"); err == nil {
		t.Error("a model without a primary key should not define")
	}
	if _, err := sqlrepo.TryDefine[User, string, UserUpdate]("users"); err == nil {
		t.Error("an ID type that disagrees with the primary key should not define")
	}
	if _, err := sqlrepo.TryDefine[User, int64, Mismatch]("users"); err == nil {
		t.Error("an update DTO with the wrong field type should not define")
	}
	if _, err := sqlrepo.TryDefine[User, int64, Unknown]("users"); err == nil {
		t.Error("an update DTO naming a field the model lacks should not define")
	}
	if _, err := sqlrepo.TryDefine[User, int64, Immutable]("users"); err == nil {
		t.Error("an update DTO touching an immutable column should not define")
	}
}
