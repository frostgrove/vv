//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/decorators/security"
)

// SaveAll exists to turn N round trips into one. These are about it still being
// the same write: the keys come back where the dialect can say, the batch is one
// statement, and nothing that guards Save is skipped because the rows arrived
// together.

func TestSaveAllWritesTheWholeBatch(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			rows := EgRows.Bind(tg.src)

			batch := []*EgRow{
				{ID: 1, Name: "a", Score: crud.Set(1)},
				{ID: 2, Name: "b", Score: crud.Set(2)},
				{ID: 3, Name: "c", Score: crud.Null[int]()},
			}
			if err := rows.SaveAll(ctx, batch); err != nil {
				t.Fatal(err)
			}
			if n, _ := rows.Count(ctx); n != 3 {
				t.Fatalf("count = %d, want 3", n)
			}
			got, err := rows.GetByID(ctx, 3)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Score.IsNull() {
				t.Fatalf("score = %v, want NULL: the three Opt states have to survive a batch", got.Score)
			}
		})
	}
}

// The batch is one statement, not a loop wearing a batch's name. Proved through
// the recorder rather than by timing.
func TestSaveAllIsOneStatement(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	tg := egEngines()[0]
	egWipe(t, tg.src)
	rec := newCountingSource(tg.src)
	rows := EgRows.Bind(rec)

	if err := rows.SaveAll(ctx, []*EgRow{
		{ID: 10, Name: "a"}, {ID: 11, Name: "b"}, {ID: 12, Name: "c"},
	}); err != nil {
		t.Fatal(err)
	}
	if rec.n != 1 {
		t.Fatalf("%d statements for three rows: SaveAll is a loop", rec.n)
	}
}

// A batch that mixes rows the database keys with rows the caller keyed is two
// different statements. Splitting it silently would hide the cost, which is the
// only reason to use this over a loop.
func TestSaveAllRefusesAMixedBatch(t *testing.T) {
	ctx := context.Background()
	egSetup(t)
	rows := EgAutos.Bind(egEngines()[0].src)

	err := rows.SaveAll(ctx, []*EgAuto{{Name: "keyed", ID: 5}, {Name: "unkeyed"}})
	if err == nil {
		t.Fatal("a mixed batch was accepted")
	}
	var se *crud.SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("err = %T %v, want a declaration error", err, err)
	}
}

// Where the dialect has RETURNING the generated keys come back, in the order the
// rows were handed in.
func TestSaveAllReadsGeneratedKeysBackWhereItCan(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			rows := EgAutos.Bind(tg.src)

			batch := []*EgAuto{{Name: "first"}, {Name: "second"}, {Name: "third"}}
			if err := rows.SaveAll(ctx, batch); err != nil {
				t.Fatal(err)
			}
			if n, _ := rows.Count(ctx); n != 3 {
				t.Fatalf("count = %d, want 3", n)
			}
			if !tg.src.Dialect().SupportsReturning() {
				// MySQL reports one LastInsertId for the statement and only
				// guarantees the rest are contiguous under some settings, so the
				// library does not guess. Documented, and pinned here so the
				// silence is deliberate.
				if batch[0].ID != 0 {
					t.Fatalf("id = %d: this dialect cannot report batch keys, so it should not have", batch[0].ID)
				}
				return
			}
			for i, m := range batch {
				if m.ID == 0 {
					t.Fatalf("row %d came back with no key", i)
				}
			}
			if batch[0].ID >= batch[1].ID || batch[1].ID >= batch[2].ID {
				t.Fatalf("keys %d %d %d are not in the order the rows were handed in",
					batch[0].ID, batch[1].ID, batch[2].ID)
			}
		})
	}
}

// The batch is checked row by row. Inherited from crud.Core it would have been
// the one call that writes the most rows and checks none of them.
func TestSaveAllIsCheckedByTheGate(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	policy := security.ScopeField[EgRow, int64]("Tenant", func(c context.Context) (any, error) {
		t, ok := c.Value(gatePrincipal{}).(int64)
		if !ok {
			return nil, security.Denied(security.Create, "no tenant in context")
		}
		return t, nil
	})

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			gated := EgRows.Bind(tg.src, security.Gate(policy))
			mine := context.WithValue(ctx, gatePrincipal{}, int64(1))

			// One row of somebody else's is enough to refuse the batch.
			err := gated.SaveAll(mine, []*EgRow{
				{ID: 1, Tenant: 1, Name: "mine"},
				{ID: 2, Tenant: 2, Name: "theirs"},
			})
			if !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			// And nothing was written: the checks all run before the statement.
			if n, _ := EgRows.Bind(tg.src).Count(ctx); n != 0 {
				t.Fatalf("%d rows written by a refused batch", n)
			}

			// A batch that is entirely the caller's goes through.
			if err := gated.SaveAll(mine, []*EgRow{
				{ID: 3, Tenant: 1, Name: "a"}, {ID: 4, Tenant: 1, Name: "b"},
			}); err != nil {
				t.Fatal(err)
			}
			if n, _ := gated.Count(mine); n != 2 {
				t.Fatalf("count = %d, want 2", n)
			}
		})
	}
}

// countingSource wraps a source and counts the statements that reach it.
type countingSource struct {
	crud.Source
	n int
}

func newCountingSource(s crud.Source) *countingSource { return &countingSource{Source: s} }

func (c *countingSource) Exec(ctx context.Context, q string, args ...any) (crud.Result, error) {
	c.n++
	return c.Source.Exec(ctx, q, args...)
}

func (c *countingSource) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	c.n++
	return c.Source.Query(ctx, q, args...)
}
