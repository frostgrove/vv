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

// A model without generated columns, to exercise the no-refresh paths.
type Doc struct {
	ID    string `db:"id,pk"`
	Title string `db:"title"`
}

type DocUpdate struct {
	Title *string
}

var (
	Users = sqlrepo.Define[User, int64, UserUpdate]("users")
	Docs  = sqlrepo.Define[Doc, string, DocUpdate]("docs")
)

var now = time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

func userRow(id int64, email, name string, age any, tenant int64) []any {
	return []any{id, email, name, age, tenant, now}
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

// DISTINCT is a keyword in front of a column list, so it cannot reuse the
// prebuilt SELECT — and a request that asks for it without asking for a
// projection is the ordinary case, not the exotic one.
func TestDistinctWithoutAProjectionStillNamesItsColumns(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := Users.Bind(rec).GetAll(context.Background(), crud.Distinct()); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL,
		`SELECT DISTINCT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users"`)
}

// Select normally keeps the primary key, because a row a client cannot address
// is not much use. Under DISTINCT that same key is fatal: it is unique, so every
// row stays distinct from every other and the keyword removes nothing.
// `?distinct=1&select=name` used to answer one row per user instead of one row
// per name. TestDistinctActuallyRemovesDuplicateRows in the integration suite
// counts the rows; this pins the statement that can produce them.
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

// The same reasoning, from the other end: a preload needs the key to attach the
// second statement's rows to the first statement's models, and a DISTINCT
// projection has none. Answering with unattached relations, or quietly putting
// the key back, would both be worse than saying so.
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
	repo := sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.MaxLimit(50)).Bind(rec)
	if _, err := repo.Get(context.Background(), crud.Limit(1000)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustSQL(t, rec, 0).SQL, "LIMIT 50") {
		t.Fatalf("limit was not clamped: %s", mustSQL(t, rec, 0).SQL)
	}
}

func TestSaveInsertsWithGeneratedKeyOnPostgres(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(9, "n@x", "New", 18, 3)))
	u := User{Email: "n@x", Name: "New", Age: crud.Set(18), TenantID: 3}

	if err := Users.Bind(rec).Save(context.Background(), &u); err != nil {
		t.Fatal(err)
	}
	st := rec.Last()
	wantSQL(t, st.SQL,
		`INSERT INTO "users" ("email", "name", "age", "tenant_id") VALUES ($1, $2, $3, $4) `+
			`RETURNING "id", "email", "name", "age", "tenant_id", "created_at"`)
	if len(st.Args) != 4 {
		t.Fatalf("args = %v", st.Args)
	}
	if u.ID != 9 || !u.CreatedAt.Equal(now) {
		t.Fatalf("model not refreshed from RETURNING: %+v", u)
	}
}

func TestSaveUpsertsWhenKeyIsSet(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(9, "n@x", "New", 18, 3)))
	u := User{ID: 9, Email: "n@x", Name: "New", Age: crud.Set(18), TenantID: 3}

	if err := Users.Bind(rec).Save(context.Background(), &u); err != nil {
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
	if err := Users.Bind(rec).Save(context.Background(), &u); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, mustSQL(t, rec, 0).SQL,
		"INSERT INTO `users` (`email`, `name`, `age`, `tenant_id`) VALUES (?, ?, ?, ?)")
	// created_at is `generated`, so MySQL has to read the row back.
	wantSQL(t, mustSQL(t, rec, 1).SQL,
		"SELECT `id`, `email`, `name`, `age`, `tenant_id`, `created_at` FROM `users` WHERE `id` = ? LIMIT 1")
	if u.ID != 77 {
		t.Fatalf("id = %d, want 77", u.ID)
	}
}

// Save promises the model comes back describing the row. PostgreSQL keeps that
// promise with RETURNING; a dialect without it has to go and read the row, or
// the caller is left holding whatever they passed in — including the values an
// upsert's conflict clause refused to write.
func TestSaveOnADialectWithoutRETURNINGReadsTheRowBack(t *testing.T) {
	rec := crudtest.MySQL().
		ExecResult(crud.Result{RowsAffected: 1}).
		Push(crudtest.Rows([]any{"abc", "what the table actually holds"}))

	d := Doc{ID: "abc", Title: "what the caller asked for"}
	if err := Docs.Bind(rec).Save(context.Background(), &d); err != nil {
		t.Fatal(err)
	}

	wantSQL(t, mustSQL(t, rec, 0).SQL,
		"INSERT INTO `docs` (`id`, `title`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `title` = VALUES(`title`)")
	wantSQL(t, mustSQL(t, rec, 1).SQL, "SELECT `id`, `title` FROM `docs` WHERE `id` = ? LIMIT 1")
	if d.Title != "what the table actually holds" {
		t.Fatalf("the model still says %q after Save; it has to describe the stored row, "+
			"or the same handler serialises a different document per engine", d.Title)
	}
}

func TestSaveRequiresAssignedKeyWhenNotGenerated(t *testing.T) {
	rec := crudtest.Postgres()
	d := Doc{Title: "no id"}
	if err := Docs.Bind(rec).Save(context.Background(), &d); !errors.Is(err, crud.ErrMissingID) {
		t.Fatalf("err = %v, want ErrMissingID", err)
	}
}

func TestUpdateWritesOnlyChangedFields(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)),  // load
		crudtest.Rows(userRow(1, "a@b.c", "Anna", 30, 7)), // returning
	)
	u, err := Users.Bind(rec).Update(context.Background(), 1, UserUpdate{
		Email: ptr("a@b.c"), // unchanged — must not be written
		Name:  ptr("Anna"),  // changed
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

func TestUpdateDistinguishesUndefinedFromNull(t *testing.T) {
	// Undefined: age is left alone.
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

	// Explicit null: age is set to NULL.
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

	// Setting a null column to a value is also a change.
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

// The same on the update path: without RETURNING the row is read back rather
// than patched in memory, so what the caller receives is what the table holds —
// triggers, defaults and all.
func TestUpdateOnADialectWithoutRETURNINGReadsTheRowBack(t *testing.T) {
	rec := crudtest.MySQL().
		ExecResult(crud.Result{RowsAffected: 1}).
		Push(
			crudtest.Rows([]any{"abc", "old"}),           // load
			crudtest.Rows([]any{"abc", "NEW (trimmed)"}), // read back
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

// A row deleted between the load and the write is gone, and Update has to say
// so on every engine. RETURNING reports it for free; without RETURNING nothing
// but the read-back can tell "no such row" from MySQL's "nothing to do", and
// the answer used to be a fabricated model with err == nil.
func TestUpdateOfARowThatVanishedIsNotFoundOnEveryDialect(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  *crudtest.Recorder
	}{
		{"postgres", crudtest.Postgres().Push(
			crudtest.Rows([]any{"abc", "old"}), // load
			crudtest.Rows(),                    // the UPDATE ... RETURNING matches nothing
		)},
		{"mysql", crudtest.MySQL().
			ExecResult(crud.Result{RowsAffected: 0}).
			Push(
				crudtest.Rows([]any{"abc", "old"}), // load
				crudtest.Rows(),                    // the read-back finds nothing
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

func TestCountAndExists(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(4)}), crudtest.Rows([]any{1}))
	repo := Users.Bind(rec)

	n, err := repo.Count(context.Background(), crud.Where(crud.Eq("TenantID", 2)))
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	wantSQL(t, mustSQL(t, rec, 0).SQL, `SELECT count(*) FROM "users" WHERE "tenant_id" = $1`)

	ok, err := repo.Exists(context.Background(), crud.Where(crud.Eq("Email", "a@b")))
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
	ctx := crud.WithExecutor(context.Background(), outer)

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
