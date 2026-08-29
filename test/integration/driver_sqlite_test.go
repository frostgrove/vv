//go:build integration

package integration

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	// A pure-Go SQLite, so the third engine costs no cgo and no container.
	_ "modernc.org/sqlite"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

// openSQLite builds a fresh, file-backed database for one test. Files rather
// than :memory: because a file is where SQLite's locking and its transactions
// actually behave like a database; one connection because SQLite serialises
// writers anyway, and a queue is a friendlier failure than SQLITE_BUSY.
func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "vv.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	if _, err := database.ExecContext(context.Background(), schemaSQLite); err != nil {
		t.Fatal(err)
	}
	return database
}

// The third dialect, running the same conformance suite as the two servers.
// It is a different set of answers to the same questions — RETURNING like
// PostgreSQL, `?` markers like MySQL, no row locks at all — so shipping it
// untested meant shipping a dialect nobody had ever run.
func TestSQLite(t *testing.T) {
	RunSuite(t, Target{Name: "database/sql+sqlite", DB: "sqlite", Source: crudsql.SQLite(openSQLite(t))})
}

// SQLite's grammar has no bare OFFSET: `LIMIT` has to come first, and its
// spelling of "no limit" is -1, not MySQL's 64-bit sentinel. The combination
// arrives from the wire as {"unpaged":true,"offset":5} and used to reach the
// database as `... OFFSET 5`, which SQLite rejects outright.
func TestSQLiteTakesAnOffsetWithoutALimit(t *testing.T) {
	ctx := context.Background()
	repository := Users.Bind(crudsql.SQLite(openSQLite(t)))
	seed(t, repository, 5)

	rest, err := repository.GetAll(ctx, crud.Unpaged(), crud.Offset(2), crud.OrderBy(crud.Asc("Email")))
	if err != nil {
		t.Fatalf("an unpaged read with an offset: %v", err)
	}
	if len(rest) != 3 || rest[0].Email != "user-02@x.io" {
		t.Fatalf("rows = %v, want the last three users", emails(rest))
	}
}

// SQLite has no default LIKE escape character. This is a behavioural regression
// test, rather than just a rendered-SQL assertion: the ESCAPE clause must make
// a caller's %, _ and backslash literal on the actual engine.
func TestSQLiteLiteralLikeHelpers(t *testing.T) {
	ctx := context.Background()
	repository := Users.Bind(crudsql.SQLite(openSQLite(t)))
	for _, user := range []User{
		{TenantID: 1, Email: "match@x.io", Name: "100%_raw"},
		{TenantID: 1, Email: "other@x.io", Name: "1005xraw"},
		{TenantID: 1, Email: "plain@x.io", Name: "plain"},
		{TenantID: 1, Email: "slash@x.io", Name: `path\file`},
	} {
		if _, err := repository.Save(ctx, &user); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name string
		pred crud.Predicate
		want string
	}{
		{"contains", crud.Contains("Name", "%_"), "100%_raw"},
		{"starts with", crud.StartsWith("Name", "100%"), "100%_raw"},
		{"ends with", crud.EndsWith("Name", "_raw"), "100%_raw"},
		{"literal backslash", crud.Contains("Name", `path\file`), `path\file`},
		{"case-insensitive contains", crud.ContainsIgnoreCase("Name", "%_RAW"), "100%_raw"},
		{"case-insensitive starts with", crud.StartsWithIgnoreCase("Name", "100%"), "100%_raw"},
		{"case-insensitive ends with", crud.EndsWithIgnoreCase("Name", "_RAW"), "100%_raw"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repository.GetAll(ctx, crud.Where(tc.pred), crud.OrderBy(crud.Asc("ID")))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Name != tc.want {
				t.Fatalf("matched %#v, want only the literal row", got)
			}
		})
	}
}

// SQLite locks the database, not the row, so there is no FOR UPDATE to render.
// The clause is dropped rather than refused — the statement is still correct,
// and inside a transaction SQLite gives the serialisation the caller wanted —
// but a silently missing lock is worth pinning, and it is in README's sharp
// edges for the same reason.
func TestForUpdateIsANoOpOnSQLite(t *testing.T) {
	ctx := context.Background()
	database := openSQLite(t)
	repository := Users.Bind(crudsql.SQLite(database))
	seed(t, repository, 2)

	got, err := repository.GetAll(ctx, crud.ForUpdate(), crud.OrderBy(crud.Asc("Email")))
	if err != nil {
		t.Fatalf("SELECT ... FOR UPDATE reached SQLite: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %v", emails(got))
	}
	if clause := (crud.SQLite{}).LockClause(); clause != "" {
		t.Fatalf("LockClause() = %q: SQLite has no row lock to ask for", clause)
	}
}

// The savepoint shape, on the dialect that has no adapter of its own: crudsql
// gives every *sql.DB the same nested Begin, and SQLite honours it.
func TestSQLiteSavepointRollsBackWithoutLosingTheTransaction(t *testing.T) {
	ctx := context.Background()
	source := crudsql.SQLite(openSQLite(t))
	repository := Users.Bind(source)

	err := crud.InTx(ctx, source, func(ctx context.Context) error {
		keep := User{TenantID: 1, Email: "keep@x.io", Name: "keep"}
		if _, err := repository.Save(ctx, &keep); err != nil {
			return err
		}
		ex, _ := crud.ExecutorFrom(ctx)
		sp, err := ex.(crud.Beginner).Begin(ctx)
		if err != nil {
			return err
		}
		drop := User{TenantID: 1, Email: "drop@x.io", Name: "drop"}
		if _, err := repository.Save(crud.WithExecutor(ctx, sp), &drop); err != nil {
			return err
		}
		return sp.Rollback(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	all, err := repository.GetAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Email != "keep@x.io" {
		t.Fatalf("rows = %v, want only the kept one", emails(all))
	}
}

func emails(us []User) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Email
	}
	return out
}
