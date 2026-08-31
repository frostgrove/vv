//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

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
	ShardRows  = sqlrepo.Define[ShardRow, int64, ShardRowUpdate]("shard_rows")
	ShardNotes = sqlrepo.Define[ShardNote, int64, ShardNoteUpdate]("shard_notes")
)

func shardDSN(t *testing.T, database string) string {
	t.Helper()
	u, err := url.Parse(pgDSN)
	if err != nil {
		t.Fatalf("the Postgres DSN is not a URL, cannot build a second one: %v", err)
	}
	u.Path = "/" + database
	return u.String()
}

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

	open := func(name string) *sql.DB {
		database, err := sql.Open("pgx", shardDSN(t, name))
		if err != nil {
			t.Fatalf("opening %s: %v", name, err)
		}
		database.SetMaxOpenConns(4)
		t.Cleanup(func() { _ = database.Close() })
		for _, stmt := range []string{
			`CREATE TABLE IF NOT EXISTS shard_rows (id bigserial PRIMARY KEY, name text NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS shard_notes (id bigserial PRIMARY KEY, text text NOT NULL)`,
			`DELETE FROM shard_rows`,
			`DELETE FROM shard_notes`,
		} {
			if _, err := database.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("%s: %s: %v", name, stmt, err)
			}
		}
		return database
	}
	return open("vv_shard_a"), open("vv_shard_b")
}

func names(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, err := database.QueryContext(context.Background(), "SELECT name FROM shard_rows ORDER BY name")
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

func TestWithExecutorRefusesToAdoptARepositoryOnTheWrongDatabase(t *testing.T) {
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
	if _, err := rowsA.Save(txCtx, &ShardRow{Name: "belongs-in-a"}); !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("same-database repository returned %v, want ErrExecutorScope until its canonical source is named", err)
	}
	if _, err := rowsB.Save(txCtx, &ShardRow{Name: "meant-for-b"}); !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("wrong-database repository returned %v, want ErrExecutorScope", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	if got := names(t, dbA); len(got) != 0 {
		t.Fatalf("shard a holds %v after the refused binding", got)
	}
	if got := names(t, dbB); len(got) != 0 {
		t.Fatalf("shard b holds %v after the refused binding", got)
	}
}

func TestWithUnsafeExecutorKeepsTheLegacyCrossDatabaseOptOut(t *testing.T) {
	ctx := context.Background()
	dbA, dbB := openShards(t)
	rowsA := ShardRows.Bind(crudsql.Postgres(dbA))
	rowsB := ShardRows.Bind(crudsql.Postgres(dbB))

	tx, err := dbA.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	txCtx := crud.WithUnsafeExecutor(ctx, crudsql.From(tx))
	if _, err := rowsA.Save(txCtx, &ShardRow{Name: "belongs-in-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rowsB.Save(txCtx, &ShardRow{Name: "explicitly-unsafe"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if got := names(t, dbA); !equal(got, []string{"belongs-in-a", "explicitly-unsafe"}) {
		t.Fatalf("shard a holds %v, want both explicitly adopted writes", got)
	}
	if got := names(t, dbB); len(got) != 0 {
		t.Fatalf("shard b holds %v, want nothing", got)
	}
}

func TestAScopedExecutorKeepsEachRepositoryOnItsOwnDatabase(t *testing.T) {
	ctx := context.Background()
	dbA, dbB := openShards(t)

	sourceA := crudsql.Postgres(dbA)
	rowsA := ShardRows.Bind(sourceA)
	rowsB := ShardRows.Bind(crudsql.Postgres(dbB))

	tx, err := dbA.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	txCtx := sourceA.BindExecutor(ctx, tx)
	if _, err := rowsA.Save(txCtx, &ShardRow{Name: "in-the-transaction"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rowsB.Save(txCtx, &ShardRow{Name: "in-shard-b"}); err != nil {
		t.Fatal(err)
	}

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

func TestWithExecutorForTransactionIdentityCannotEscapeRollback(t *testing.T) {
	ctx := context.Background()
	dbA, _ := openShards(t)
	rows := ShardRows.Bind(crudsql.Postgres(dbA))
	tx, err := dbA.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	txCtx := crud.WithExecutorFor(ctx, tx, crudsql.From(tx))
	if _, err := rows.Save(txCtx, &ShardRow{Name: "must-not-escape"}); !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("Save returned %v, want ErrExecutorScope", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dbA); len(got) != 0 {
		t.Fatalf("shard a holds %v: the mismatched binding fell back to the pool", got)
	}

	tx, err = dbA.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	txSource := crudsql.Source(tx, crud.Postgres{})
	txCtx = crud.BindExecutor(ctx, txSource, crudsql.From(tx))
	if _, err := rows.Save(txCtx, &ShardRow{Name: "must-not-escape-session"}); !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("BindExecutor with a transaction source returned %v, want ErrExecutorScope", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dbA); len(got) != 0 {
		t.Fatalf("shard a holds %v: BindExecutor accepted a transaction as canonical source", got)
	}
}

func TestARepositoryTransactionDoesNotCaptureAnotherDatabase(t *testing.T) {
	ctx := context.Background()
	dbA, dbB := openShards(t)

	rowsA := ShardRows.Bind(crudsql.Postgres(dbA))
	rowsB := ShardRows.Bind(crudsql.Postgres(dbB))

	boom := errors.New("the usecase failed after both writes")
	err := rowsA.Tx(ctx, func(ctx context.Context) error {
		if _, err := rowsA.Save(ctx, &ShardRow{Name: "doomed"}); err != nil {
			return err
		}
		if _, err := rowsB.Save(ctx, &ShardRow{Name: "survivor"}); err != nil {
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

func TestTwoRepositoriesOnOneDatabaseStillShareOneTransaction(t *testing.T) {
	ctx := context.Background()
	dbA, _ := openShards(t)

	rows := ShardRows.Bind(crudsql.Postgres(dbA))
	notes := ShardNotes.Bind(crudsql.Postgres(dbA))

	boom := errors.New("rolled back")
	err := rows.Tx(ctx, func(ctx context.Context) error {
		if _, err := rows.Save(ctx, &ShardRow{Name: "r"}); err != nil {
			return err
		}
		if _, err := notes.Save(ctx, &ShardNote{Text: "n"}); err != nil {
			return err
		}

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
