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

var ReplicaRows = sqlrepo.Define[ShardRow, int64, ShardRowUpdate]("shard_rows")

func TestReadsGoToTheReplicaAndWritesDoNot(t *testing.T) {
	ctx := context.Background()
	primary, replica := openShards(t)

	if _, err := primary.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (1, 'on-primary')`); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (1, 'on-replica'), (2, 'replica-only')`); err != nil {
		t.Fatal(err)
	}

	repository := ReplicaRows.Bind(crud.ReadWrite(crudsql.Postgres(primary), crudsql.Postgres(replica)))

	t.Run("a list read is served by the replica", func(t *testing.T) {
		got, err := repository.GetAll(ctx, crud.OrderBy(crud.Asc("ID")))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Name != "on-replica" {
			t.Fatalf("rows = %+v, want the replica's two", got)
		}
	})

	t.Run("Count and Exists too", func(t *testing.T) {
		if n, err := repository.Count(ctx); err != nil || n != 2 {
			t.Fatalf("count = %d err = %v, want the replica's 2", n, err)
		}
		ok, err := repository.Exists(ctx, crud.Where(crud.Eq("Name", "replica-only")))
		if err != nil || !ok {
			t.Fatalf("exists = %v err = %v", ok, err)
		}
	})

	t.Run("an aggregate too", func(t *testing.T) {
		rows, err := repository.Aggregate(ctx, crud.Aggregate(crud.CountAll("n")))
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := rows[0].Int("n"); n != 2 {
			t.Fatalf("aggregate count = %d, want the replica's 2", n)
		}
	})

	t.Run("a write lands on the primary", func(t *testing.T) {
		row := ShardRow{ID: 9, Name: "written"}
		if _, err := repository.Save(ctx, &row); err != nil {
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
		got, err := repository.GetAll(ctx, crud.PrimaryOnly(), crud.OrderBy(crud.Asc("ID")))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 || got[0].Name != "on-primary" {
			t.Fatalf("rows = %+v, want the primary's", got)
		}
	})
}

func TestUpdateDiffsAgainstThePrimary(t *testing.T) {
	ctx := context.Background()
	primary, replica := openShards(t)

	if _, err := primary.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (1, 'current')`); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (1, 'stale')`); err != nil {
		t.Fatal(err)
	}

	repository := ReplicaRows.Bind(crud.ReadWrite(crudsql.Postgres(primary), crudsql.Postgres(replica)))

	got, err := repository.Update(ctx, 1, ShardRowUpdate{Name: ptrOf("stale")})
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

func TestTheGatesAuthorisationLoadTakesThePrimary(t *testing.T) {
	ctx := context.Background()
	primary, replica := openShards(t)

	if _, err := primary.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (1, 'moved-away')`); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (1, 'still-mine')`); err != nil {
		t.Fatal(err)
	}

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

	repository := ReplicaRows.Bind(
		crud.ReadWrite(crudsql.Postgres(primary), crudsql.Postgres(replica)),
		security.Gate(policy),
	)

	_, err := repository.Update(ctx, 1, ShardRowUpdate{Name: ptrOf("rewritten")})
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("the update was allowed on the strength of the replica's copy (Inspect saw %q): %v", seen, err)
	}
	if seen != "moved-away" {
		t.Fatalf("Inspect was shown %q — the replica's row, not the primary's", seen)
	}

	if _, err := primary.ExecContext(ctx, `UPDATE shard_rows SET name = 'still-mine' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Update(ctx, 1, ShardRowUpdate{Name: ptrOf("rewritten")}); err != nil {
		t.Fatalf("the same update was refused when the primary agreed: %v", err)
	}
}

func TestAReadInsideATransactionIgnoresTheReplica(t *testing.T) {
	ctx := context.Background()
	primary, replica := openShards(t)

	if _, err := replica.ExecContext(ctx, `INSERT INTO shard_rows (id, name) VALUES (7, 'replica-only')`); err != nil {
		t.Fatal(err)
	}
	repository := ReplicaRows.Bind(crud.ReadWrite(crudsql.Postgres(primary), crudsql.Postgres(replica)))

	err := repository.Tx(ctx, func(ctx context.Context) error {
		row := ShardRow{ID: 42, Name: "in-tx"}
		if _, err := repository.Save(ctx, &row); err != nil {
			return err
		}

		got, err := repository.GetByID(ctx, 42)
		if err != nil {
			return err
		}
		if got.Name != "in-tx" {
			t.Errorf("read back %q", got.Name)
		}

		if n, err := repository.Count(ctx); err != nil || n != 1 {
			t.Errorf("count inside the transaction = %d err = %v, want just the row written here", n, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func ptrOf[T any](v T) *T { return &v }
