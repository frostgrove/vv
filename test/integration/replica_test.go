//go:build integration

package integration

import (
	"context"
	"errors"
	"github.com/frostgrove/vv/crud/decorators/security"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

// A real replica lags, which is exactly what makes routing hard to test: you
// cannot tell "went to the replica" from "went to the primary" when the two
// agree. So these use two databases that deliberately *disagree* — the second
// one holds different rows. Whichever rows come back name the datasource the
// statement actually reached.

var ReplicaRows = sqlrepo.Define[ShardRow, int64, ShardRowUpdate]("shard_rows")

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

// The gate's own authorisation load takes the primary too.
//
// gate.Update loads the row and hands it to Inspect, which decides whether the
// update is allowed. That is a read that decides a write, so [[D-032]] puts it on
// the primary — and it was the one check in the gate that did not say so. Every
// other one (saveTarget, the hidden-row Exists, UpdateAll's target fetch,
// Delete's and DeleteAll's victim fetches) already passed PrimaryOnly.
//
// The failure it allows is the one D-032 exists for: a row that has just moved
// out of the caller's reach still authorises the update on a lagging replica,
// and the UPDATE that follows lands on the primary anyway.
func TestTheGatesAuthorisationLoadTakesThePrimary(t *testing.T) {
	ctx := context.Background()
	primary, replica := openShards(t)

	// The two disagree about who owns the row. The replica is stale.
	if _, err := primary.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (1, 'moved-away')`); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (1, 'still-mine')`); err != nil {
		t.Fatal(err)
	}

	// A rule that reads the row: only "still-mine" may be updated.
	seen := ""
	policy := security.Policy[ShardRow, int64]{
		Inspect: func(_ context.Context, _ security.Action, m *ShardRow) error {
			seen = m.Name
			if m.Name != "still-mine" {
				return security.Denied(security.Update, "not yours any more")
			}
			return nil
		},
	}

	repo := ReplicaRows.Bind(
		crud.ReadWrite(crudsql.Postgres(primary), crudsql.Postgres(replica)),
		security.Gate(policy),
	)

	_, err := repo.Update(ctx, 1, ShardRowUpdate{Name: ptrOf("rewritten")})
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("the update was allowed on the strength of the replica's copy (Inspect saw %q): %v", seen, err)
	}
	if seen != "moved-away" {
		t.Fatalf("Inspect was shown %q — the replica's row, not the primary's", seen)
	}

	// The control: with the primary agreeing, the same call goes through. The
	// refusal above is the staleness, not a policy that denies everything.
	if _, err := primary.ExecContext(ctx, `UPDATE shard_rows SET name = 'still-mine' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Update(ctx, 1, ShardRowUpdate{Name: ptrOf("rewritten")}); err != nil {
		t.Fatalf("the same update was refused when the primary agreed: %v", err)
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
