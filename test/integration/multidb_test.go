//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/shardit-io/vv/adapter/crudsql"
	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/repo/basic"
)

// Two databases on one engine, reached through two *sql.DB handles. Everything
// about them is identical except which physical rows they hold, so a statement
// that lands in the wrong one is visible rather than merely suspected.
//
// This is the shape crud.WithExecutorFor exists for: a process that shards, or
// that keeps an analytics database next to its main one, and opens a
// transaction on one of them.

type ShardRow struct {
	ID   int64  `db:"id,pk,auto"`
	Name string `db:"name"`
}

type ShardRowUpdate struct {
	Name *string
}

type ShardNote struct {
	ID   int64  `db:"id,pk,auto"`
	Text string `db:"text"`
}

type ShardNoteUpdate struct {
	Text *string
}

var (
	ShardRows  = basic.Define[ShardRow, int64, ShardRowUpdate]("shard_rows")
	ShardNotes = basic.Define[ShardNote, int64, ShardNoteUpdate]("shard_notes")
)

// shardDSN points the configured Postgres DSN at another database on the same
// server.
func shardDSN(t *testing.T, database string) string {
	t.Helper()
	u, err := url.Parse(pgDSN)
	if err != nil {
		t.Fatalf("the Postgres DSN is not a URL, cannot build a second one: %v", err)
	}
	u.Path = "/" + database
	return u.String()
}

// openShards creates vv_shard_a and vv_shard_b if they are not there
// yet and returns a live handle to each. CREATE DATABASE cannot run inside a
// transaction and has no IF NOT EXISTS, so a duplicate is simply expected.
func openShards(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()
	ctx := context.Background()

	for _, name := range []string{"vv_shard_a", "vv_shard_b"} {
		if _, err := pgDB.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("creating %s: %v", name, err)
			}
		}
	}

	open := func(database string) *sql.DB {
		db, err := sql.Open("pgx", shardDSN(t, database))
		if err != nil {
			t.Fatalf("opening %s: %v", database, err)
		}
		db.SetMaxOpenConns(4)
		t.Cleanup(func() { _ = db.Close() })
		for _, stmt := range []string{
			`CREATE TABLE IF NOT EXISTS shard_rows (id bigserial PRIMARY KEY, name text NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS shard_notes (id bigserial PRIMARY KEY, text text NOT NULL)`,
			`DELETE FROM shard_rows`,
			`DELETE FROM shard_notes`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("%s: %s: %v", database, stmt, err)
			}
		}
		return db
	}
	return open("vv_shard_a"), open("vv_shard_b")
}

// names reads a shard directly, so the assertion never goes through the seam
// under test.
func names(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "SELECT name FROM shard_rows ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// crud.WithExecutor is unconditional by design — it is how an ent or gorm
// transaction adopts a repository, and such an executor has no relationship to
// the source a repository holds. The cost of that design is here, pinned so it
// stays a decision rather than a surprise: name the wrong context and a
// repository writes into a database it was never bound to, successfully.
func TestAnUnscopedExecutorAdoptsEveryRepositoryIncludingTheWrongOne(t *testing.T) {
	ctx := context.Background()
	dbA, dbB := openShards(t)

	rowsA := ShardRows.Bind(crudsql.Postgres(dbA))
	rowsB := ShardRows.Bind(crudsql.Postgres(dbB))

	tx, err := dbA.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	txCtx := crud.WithExecutor(ctx, crudsql.From(tx))
	if err := rowsA.Save(txCtx, &ShardRow{Name: "belongs-in-a"}); err != nil {
		t.Fatal(err)
	}
	// rowsB is bound to dbB and asked to write with a context carrying dbA's
	// transaction. It obeys the context.
	if err := rowsB.Save(txCtx, &ShardRow{Name: "meant-for-b"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if got := names(t, dbA); !equal(got, []string{"belongs-in-a", "meant-for-b"}) {
		t.Fatalf("shard a holds %v — the unscoped seam is meant to capture both", got)
	}
	if got := names(t, dbB); len(got) != 0 {
		t.Fatalf("shard b holds %v, want nothing", got)
	}
}

// The scoped form says which database the executor belongs to, and a repository
// on any other one carries on using its own.
func TestAScopedExecutorKeepsEachRepositoryOnItsOwnDatabase(t *testing.T) {
	ctx := context.Background()
	dbA, dbB := openShards(t)

	rowsA := ShardRows.Bind(crudsql.Postgres(dbA))
	rowsB := ShardRows.Bind(crudsql.Postgres(dbB))

	tx, err := dbA.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	txCtx := crud.WithExecutorFor(ctx, dbA, crudsql.From(tx))
	if err := rowsA.Save(txCtx, &ShardRow{Name: "in-the-transaction"}); err != nil {
		t.Fatal(err)
	}
	if err := rowsB.Save(txCtx, &ShardRow{Name: "in-shard-b"}); err != nil {
		t.Fatal(err)
	}

	// Rolling back must take the first write and only the first write.
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	if got := names(t, dbA); len(got) != 0 {
		t.Fatalf("shard a holds %v: the rollback did not reach the repository's write", got)
	}
	if got := names(t, dbB); !equal(got, []string{"in-shard-b"}) {
		t.Fatalf("shard b holds %v: its write was captured by another database's transaction", got)
	}
}

// A repository opening its own transaction scopes it the same way, so a second
// repository on a different database inside that block is untouched by it.
func TestARepositoryTransactionDoesNotCaptureAnotherDatabase(t *testing.T) {
	ctx := context.Background()
	dbA, dbB := openShards(t)

	rowsA := ShardRows.Bind(crudsql.Postgres(dbA))
	rowsB := ShardRows.Bind(crudsql.Postgres(dbB))

	boom := errors.New("the usecase failed after both writes")
	err := rowsA.Tx(ctx, func(ctx context.Context) error {
		if err := rowsA.Save(ctx, &ShardRow{Name: "doomed"}); err != nil {
			return err
		}
		if err := rowsB.Save(ctx, &ShardRow{Name: "survivor"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}

	if got := names(t, dbA); len(got) != 0 {
		t.Fatalf("shard a holds %v after a rollback", got)
	}
	if got := names(t, dbB); !equal(got, []string{"survivor"}) {
		t.Fatalf("shard b holds %v: another database's rollback reached into it", got)
	}
}

// The documented reason InTx exists — several repositories, one transaction —
// still holds, because two sources over the same *sql.DB name the same database.
func TestTwoRepositoriesOnOneDatabaseStillShareOneTransaction(t *testing.T) {
	ctx := context.Background()
	dbA, _ := openShards(t)

	rows := ShardRows.Bind(crudsql.Postgres(dbA))
	notes := ShardNotes.Bind(crudsql.Postgres(dbA)) // a second, independently built source

	boom := errors.New("rolled back")
	err := rows.Tx(ctx, func(ctx context.Context) error {
		if err := rows.Save(ctx, &ShardRow{Name: "r"}); err != nil {
			return err
		}
		if err := notes.Save(ctx, &ShardNote{Text: "n"}); err != nil {
			return err
		}
		// Both are visible from inside, which is only true if they share it.
		if n, err := notes.Count(ctx); err != nil || n != 1 {
			t.Errorf("the second repository did not join: count = %d err = %v", n, err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}

	if n, err := rows.Count(ctx); err != nil || n != 0 {
		t.Fatalf("shard_rows = %d err = %v", n, err)
	}
	if n, err := notes.Count(ctx); err != nil || n != 0 {
		t.Fatalf("shard_notes = %d: the second repository's write escaped the rollback", n)
	}
}
