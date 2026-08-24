//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/shardit-io/rx/adapter/crudsql"
	"github.com/shardit-io/rx/crud"
	"github.com/shardit-io/rx/repo/basic"
)

// A real replica lags, which is exactly what makes routing hard to test: you
// cannot tell "went to the replica" from "went to the primary" when the two
// agree. So these use two databases that deliberately *disagree* — the second
// one holds different rows. Whichever rows come back name the datasource the
// statement actually reached.

var ReplicaRows = basic.Define[ShardRow, int64, ShardRowUpdate]("shard_rows")

func TestReadsGoToTheReplicaAndWritesDoNot(t *testing.T) {
	ctx := context.Background()
	primary, replica := openShards(t)

	// The two disagree on purpose.
	if _, err := primary.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (1, 'on-primary')`); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (1, 'on-replica'), (2, 'replica-only')`); err != nil {
		t.Fatal(err)
	}

	repo := ReplicaRows.Bind(crud.ReadWrite(crudsql.Postgres(primary), crudsql.Postgres(replica)))

	t.Run("a list read is served by the replica", func(t *testing.T) {
		got, err := repo.GetAll(ctx, crud.OrderBy(crud.Asc("ID")))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Name != "on-replica" {
			t.Fatalf("rows = %+v, want the replica's two", got)
		}
	})

	t.Run("Count and Exists too", func(t *testing.T) {
		if n, err := repo.Count(ctx); err != nil || n != 2 {
			t.Fatalf("count = %d err = %v, want the replica's 2", n, err)
		}
		ok, err := repo.Exists(ctx, crud.Where(crud.Eq("Name", "replica-only")))
		if err != nil || !ok {
			t.Fatalf("exists = %v err = %v", ok, err)
		}
	})

	t.Run("an aggregate too", func(t *testing.T) {
		rows, err := repo.Aggregate(ctx, crud.Aggregate(crud.CountAll("n")))
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := rows[0].Int("n"); n != 2 {
			t.Fatalf("aggregate count = %d, want the replica's 2", n)
		}
	})

	t.Run("a write lands on the primary", func(t *testing.T) {
		row := ShardRow{ID: 9, Name: "written"}
		if err := repo.Save(ctx, &row); err != nil {
			t.Fatal(err)
		}
		var name string
		if err := primary.QueryRowContext(ctx, `SELECT name FROM shard_rows WHERE id = 9`).Scan(&name); err != nil {
			t.Fatalf("the write did not reach the primary: %v", err)
		}
		if err := replica.QueryRowContext(ctx, `SELECT name FROM shard_rows WHERE id = 9`).Scan(&name); err == nil {
			t.Fatal("the write reached the replica")
		}
	})

	t.Run("PrimaryOnly opts a read back out", func(t *testing.T) {
		got, err := repo.GetAll(ctx, crud.PrimaryOnly(), crud.OrderBy(crud.Asc("ID")))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 || got[0].Name != "on-primary" {
			t.Fatalf("rows = %+v, want the primary's", got)
		}
	})
}

// The load half of an Update is a read that decides a write. Served from a
// replica it diffs against a row as it was, and writes the difference — so it is
// pinned to the primary whether or not anyone asked.
func TestUpdateDiffsAgainstThePrimary(t *testing.T) {
	ctx := context.Background()
	primary, replica := openShards(t)

	if _, err := primary.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (1, 'current')`); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (1, 'stale')`); err != nil {
		t.Fatal(err)
	}

	repo := ReplicaRows.Bind(crud.ReadWrite(crudsql.Postgres(primary), crudsql.Postgres(replica)))

	// Setting the name to what the *replica* holds must still be a real change,
	// because the row on the primary says something else.
	got, err := repo.Update(ctx, 1, ShardRowUpdate{Name: ptrOf("stale")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "stale" {
		t.Fatalf("name = %q", got.Name)
	}
	var onPrimary string
	if err := primary.QueryRowContext(ctx, `SELECT name FROM shard_rows WHERE id = 1`).Scan(&onPrimary); err != nil {
		t.Fatal(err)
	}
	if onPrimary != "stale" {
		t.Fatalf("the primary holds %q: the diff was taken against the replica and wrote nothing", onPrimary)
	}
}

// A transaction wins over the replica outright. Reading around a transaction one
// has just joined would defeat the transaction.
func TestAReadInsideATransactionIgnoresTheReplica(t *testing.T) {
	ctx := context.Background()
	primary, replica := openShards(t)

	if _, err := replica.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (7, 'replica-only')`); err != nil {
		t.Fatal(err)
	}
	repo := ReplicaRows.Bind(crud.ReadWrite(crudsql.Postgres(primary), crudsql.Postgres(replica)))

	err := repo.Tx(ctx, func(ctx context.Context) error {
		row := ShardRow{ID: 42, Name: "in-tx"}
		if err := repo.Save(ctx, &row); err != nil {
			return err
		}
		// Read-your-own-writes: the row exists only in this transaction, on the
		// primary. A read served by the replica would not see it.
		got, err := repo.GetByID(ctx, 42)
		if err != nil {
			return err
		}
		if got.Name != "in-tx" {
			t.Errorf("read back %q", got.Name)
		}
		// And the replica's row is not visible from in here.
		if n, err := repo.Count(ctx); err != nil || n != 1 {
			t.Errorf("count inside the transaction = %d err = %v, want just the row written here", n, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func ptrOf[T any](v T) *T { return &v }
