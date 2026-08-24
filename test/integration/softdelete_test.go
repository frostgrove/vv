//go:build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shardit-io/ordo/crud"
	"github.com/shardit-io/ordo/repo/basic"
)

// Written by hand, a soft delete is two declarations: a scope that hides the
// tombstones and a service layer that stops Delete destroying rows. Adding the
// first and forgetting the second fails silently — the reads hide rows the
// deletes are still removing — so the point of the setting is that one
// declaration is both halves and they cannot be added separately.

// A table of its own: adding a tombstone column to a shared fixture would
// change what every other test on it sees.
type SdRow struct {
	ID      int64               `db:"id,pk,noauto"`
	Tenant  int64               `db:"tenant,immutable"`
	Name    string              `db:"name"`
	Deleted crud.Opt[time.Time] `db:"deleted_at"`
}

type SdRowUpdate struct {
	Name *string
}

var (
	// The same table twice: once with the setting, once without, so every
	// assertion below can be checked against what the row really is.
	SoftRows = basic.Define[SdRow, int64, SdRowUpdate]("sd_rows", basic.SoftDelete("Deleted"))
	RawRows  = basic.Define[SdRow, int64, SdRowUpdate]("sd_rows")
)

var sdSchema = map[string][]string{
	"postgres": {
		`DROP TABLE IF EXISTS sd_rows`,
		`CREATE TABLE sd_rows (
			id bigint PRIMARY KEY,
			tenant bigint NOT NULL DEFAULT 0,
			name text NOT NULL,
			deleted_at timestamptz)`,
	},
	"mysql": {
		`DROP TABLE IF EXISTS sd_rows`,
		`CREATE TABLE sd_rows (
			id bigint PRIMARY KEY,
			tenant bigint NOT NULL DEFAULT 0,
			name varchar(255) NOT NULL,
			deleted_at datetime(6))`,
	},
}

var (
	sdOnce sync.Once
	sdErr  error
)

func sdSetup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	sdOnce.Do(func() {
		for _, tg := range egEngines() {
			for _, stmt := range sdSchema[tg.db] {
				if _, err := tg.src.Exec(ctx, stmt); err != nil {
					sdErr = errors.New(tg.db + ": " + err.Error())
					return
				}
			}
		}
	})
	if sdErr != nil {
		t.Fatalf("the sd table was never built: %v", sdErr)
	}
	for _, tg := range egEngines() {
		if _, err := tg.src.Exec(ctx, "DELETE FROM "+tg.src.Dialect().Quote("sd_rows")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestASoftDeleteStampsRatherThanRemoves(t *testing.T) {
	ctx := context.Background()
	sdSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			sdSetup(t)
			soft := SoftRows.Bind(tg.src)
			raw := RawRows.Bind(tg.src) // the same table, without the setting

			for _, r := range []SdRow{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}, {ID: 3, Name: "c"}} {
				row := r
				if err := soft.Save(ctx, &row); err != nil {
					t.Fatal(err)
				}
			}

			n, err := soft.Delete(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("deleted %d rows", n)
			}

			// Gone as far as this repository is concerned.
			if _, err := soft.GetByID(ctx, 1); !errors.Is(err, crud.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
			if c, _ := soft.Count(ctx); c != 2 {
				t.Fatalf("count = %d, want 2", c)
			}

			// Still there in the table, which is the whole point.
			if c, _ := raw.Count(ctx); c != 3 {
				t.Fatalf("the row was really deleted: raw count = %d, want 3", c)
			}
			back, err := raw.GetByID(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if back.Deleted.IsNull() {
				t.Fatal("the tombstone column was not stamped")
			}
		})
	}
}

// DeleteAll takes the same route, and a filter still narrows it.
func TestASoftDeleteAllHonoursTheFilter(t *testing.T) {
	ctx := context.Background()
	sdSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			sdSetup(t)
			soft := SoftRows.Bind(tg.src)
			raw := RawRows.Bind(tg.src)

			for _, r := range []SdRow{{ID: 1, Name: "keep"}, {ID: 2, Name: "drop"}, {ID: 3, Name: "drop"}} {
				row := r
				if err := soft.Save(ctx, &row); err != nil {
					t.Fatal(err)
				}
			}
			n, err := soft.DeleteAll(ctx, crud.Where(crud.Eq("Name", "drop")))
			if err != nil {
				t.Fatal(err)
			}
			if n != 2 {
				t.Fatalf("stamped %d rows, want 2", n)
			}
			if c, _ := soft.Count(ctx); c != 1 {
				t.Fatalf("count = %d, want 1", c)
			}
			if c, _ := raw.Count(ctx); c != 3 {
				t.Fatalf("rows left the table: raw count = %d", c)
			}
		})
	}
}

// Deleting the same row twice reports nothing the second time, because the
// count is what this call removed from view rather than what it matched.
func TestDeletingATombstoneAgainChangesNothing(t *testing.T) {
	ctx := context.Background()
	sdSetup(t)

	tg := egEngines()[0]
	sdSetup(t)
	soft := SoftRows.Bind(tg.src)

	row := SdRow{ID: 1, Name: "a"}
	if err := soft.Save(ctx, &row); err != nil {
		t.Fatal(err)
	}
	if n, _ := soft.Delete(ctx, 1); n != 1 {
		t.Fatalf("first delete = %d", n)
	}
	if n, err := soft.Delete(ctx, 1); err != nil || n != 0 {
		t.Fatalf("second delete = %d err = %v, want 0", n, err)
	}
}

// A tombstoned row is not there as far as a write is concerned either, or a
// client that kept an id could keep editing something it deleted.
func TestATombstonedRowCannotBeUpdated(t *testing.T) {
	ctx := context.Background()
	sdSetup(t)

	tg := egEngines()[0]
	sdSetup(t)
	soft := SoftRows.Bind(tg.src)

	row := SdRow{ID: 1, Name: "a"}
	if err := soft.Save(ctx, &row); err != nil {
		t.Fatal(err)
	}
	if _, err := soft.Delete(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := soft.Update(ctx, 1, SdRowUpdate{Name: ptrOf("edited")}); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound: a deleted row was edited", err)
	}
}

// The control for every test above: without the setting, the same calls destroy
// rows. If that ever stops being true, the assertions above prove nothing.
func TestWithoutTheSettingADeleteStillRemovesTheRow(t *testing.T) {
	ctx := context.Background()
	sdSetup(t)

	tg := egEngines()[0]
	sdSetup(t)
	raw := RawRows.Bind(tg.src)

	row := SdRow{ID: 1, Name: "a"}
	if err := raw.Save(ctx, &row); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Delete(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if c, _ := raw.Count(ctx); c != 0 {
		t.Fatalf("count = %d: the plain repository soft-deleted, so the setting proves nothing", c)
	}
}

// The declaration is checked when it is written, not when a row is first
// deleted.
func TestABadSoftDeleteDeclarationIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, field string }{
		{"no such field", "Nope"},
		{"not nullable", "Name"},
		{"the primary key", "ID"},
		{"an immutable column", "Tenant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := basic.TryDefine[SdRow, int64, SdRowUpdate]("sd_rows",
				basic.SoftDelete(tc.field)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
