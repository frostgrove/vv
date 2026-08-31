//go:build integration

package integration

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

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

func TestSQLite(t *testing.T) {
	RunSuite(t, Target{Name: "database/sql+sqlite", DB: "sqlite", Source: crudsql.SQLite(openSQLite(t))})
}

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
		if _, err := repository.Save(crud.BindExecutor(ctx, source, sp), &drop); err != nil {
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
