//go:build integration

package integration

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	// A pure-Go SQLite, so the third engine costs no cgo and no container.
	_ "modernc.org/sqlite"

	"github.com/shardit-io/qq/adapter/crudsql"
	"github.com/shardit-io/qq/crud"
)

// openSQLite builds a fresh, file-backed database for one test. Files rather
// than :memory: because a file is where SQLite's locking and its transactions
// actually behave like a database; one connection because SQLite serialises
// writers anyway, and a queue is a friendlier failure than SQLITE_BUSY.
func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "rxcrud.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(context.Background(), schemaSQLite); err != nil {
		t.Fatal(err)
	}
	return db
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
	repo := Users.Bind(crudsql.SQLite(openSQLite(t)))
	seed(t, repo, 5)

	rest, err := repo.GetAll(ctx, crud.Unpaged(), crud.Offset(2), crud.OrderBy(crud.Asc("Email")))
	if err != nil {
		t.Fatalf("an unpaged read with an offset: %v", err)
	}
	if len(rest) != 3 || rest[0].Email != "user-02@x.io" {
		t.Fatalf("rows = %v, want the last three users", emails(rest))
	}
}

// SQLite locks the database, not the row, so there is no FOR UPDATE to render.
// The clause is dropped rather than refused — the statement is still correct,
// and inside a transaction SQLite gives the serialisation the caller wanted —
// but a silently missing lock is worth pinning, and it is in README's sharp
// edges for the same reason.
func TestForUpdateIsANoOpOnSQLite(t *testing.T) {
	ctx := context.Background()
	db := openSQLite(t)
	repo := Users.Bind(crudsql.SQLite(db))
	seed(t, repo, 2)

	got, err := repo.GetAll(ctx, crud.ForUpdate(), crud.OrderBy(crud.Asc("Email")))
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
	src := crudsql.SQLite(openSQLite(t))
	repo := Users.Bind(src)

	err := crud.InTx(ctx, src, func(ctx context.Context) error {
		keep := User{TenantID: 1, Email: "keep@x.io", Name: "keep"}
		if err := repo.Save(ctx, &keep); err != nil {
			return err
		}
		ex, _ := crud.ExecutorFrom(ctx)
		sp, err := ex.(crud.Beginner).Begin(ctx)
		if err != nil {
			return err
		}
		drop := User{TenantID: 1, Email: "drop@x.io", Name: "drop"}
		if err := repo.Save(crud.WithExecutor(ctx, sp), &drop); err != nil {
			return err
		}
		return sp.Rollback(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	all, err := repo.GetAll(ctx)
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
