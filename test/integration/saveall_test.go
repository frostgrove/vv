//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/security"
)

type integrationBindBudget struct {
	crud.Dialect
	limit int
}

func (this integrationBindBudget) MaxBindValues() int { return this.limit }

type integrationBudgetSource struct {
	crud.Source
	d crud.Dialect
}

func withBindBudget(source crud.Source, limit int) integrationBudgetSource {
	return integrationBudgetSource{Source: source, d: integrationBindBudget{Dialect: source.Dialect(), limit: limit}}
}

func (this integrationBudgetSource) Dialect() crud.Dialect { return this.d }
func (this integrationBudgetSource) DataSource() any       { return crud.KeyOf(this.Source) }
func (this integrationBudgetSource) Begin(ctx context.Context) (crud.Tx, error) {
	beginner, ok := crud.BeginnerOf(this.Source)
	if !ok {
		return nil, crud.ErrNoTxSupport
	}
	return beginner.Begin(ctx)
}

// SaveAll exists to turn N round trips into one. These are about it still being
// the same write: its command objects remain untouched, stored rows can be read
// back explicitly, the batch is one statement, and nothing that guards Save is
// skipped because the rows arrived together.

func TestSaveAllWritesTheWholeBatch(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			rows := EgRows.Bind(tg.source)

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

func TestSaveAllChunksRollBackAsOneWriteAgainstEveryEngine(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			// Three generated-key columns means a six-bind budget puts two rows
			// in the first chunk and the duplicate in the second one.
			rows := EgConses.Bind(withBindBudget(tg.source, 6))
			err := rows.SaveAll(ctx, []*EgCons{
				{Slug: "first", Tag: crud.Set("ok")},
				{Slug: "second", Tag: crud.Set("ok")},
				{Slug: "first", Tag: crud.Set("duplicate")},
			})
			if err == nil {
				t.Fatal("the duplicate in the second chunk was accepted")
			}
			if n, countErr := EgConses.Bind(tg.source).Count(ctx); countErr != nil || n != 0 {
				t.Fatalf("count after failed chunks = %d, err = %v; first chunk survived rollback", n, countErr)
			}
		})
	}
}

func TestDeleteChunksRemoveTheWholeIDSetAgainstEveryEngine(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			plain := EgRows.Bind(tg.source)
			batch := make([]*EgRow, 5)
			ids := make([]int64, 5)
			for i := range batch {
				ids[i] = int64(i + 1)
				batch[i] = &EgRow{ID: ids[i], Name: "row"}
			}
			if err := plain.SaveAll(ctx, batch); err != nil {
				t.Fatal(err)
			}

			rows := EgRows.Bind(withBindBudget(tg.source, 2))
			n, err := rows.Delete(ctx, ids...)
			if err != nil {
				t.Fatal(err)
			}
			if n != 5 {
				t.Fatalf("deleted = %d, want 5", n)
			}
			if left, countErr := plain.Count(ctx); countErr != nil || left != 0 {
				t.Fatalf("count after chunked delete = %d, err = %v", left, countErr)
			}
		})
	}
}

// A batch that fits the dialect budget is one statement, not a loop wearing a
// batch's name. Larger batches chunk only at that hard boundary. Proved through
// the recorder rather than by timing.
func TestSaveAllIsOneStatement(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	tg := egEngines()[0]
	egWipe(t, tg.source)
	rec := newCountingSource(tg.source)
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
	rows := EgAutos.Bind(egEngines()[0].source)

	err := rows.SaveAll(ctx, []*EgAuto{{Name: "keyed", ID: 5}, {Name: "unkeyed"}})
	if err == nil {
		t.Fatal("a mixed batch was accepted")
	}
	var se *crud.SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("err = %T %v, want a declaration error", err, err)
	}
}

// SaveAll is the write-only batch primitive on every dialect. A caller that
// needs database-owned values reads the stored rows explicitly rather than
// observing dialect-dependent mutation of command objects.
func TestSaveAllLeavesGeneratedKeysOnItsInputsUntouched(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			rows := EgAutos.Bind(tg.source)

			batch := []*EgAuto{{Name: "first"}, {Name: "second"}, {Name: "third"}}
			if err := rows.SaveAll(ctx, batch); err != nil {
				t.Fatal(err)
			}
			if n, _ := rows.Count(ctx); n != 3 {
				t.Fatalf("count = %d, want 3", n)
			}
			for i, m := range batch {
				if m.ID != 0 {
					t.Fatalf("input row %d id = %d: SaveAll mutated a write-only command", i, m.ID)
				}
			}
			stored, err := rows.GetAll(ctx, crud.OrderBy(crud.Asc("ID")))
			if err != nil {
				t.Fatal(err)
			}
			if len(stored) != 3 {
				t.Fatalf("stored rows = %d, want 3", len(stored))
			}
			for i, want := range []string{"first", "second", "third"} {
				if stored[i].ID == 0 || stored[i].Name != want {
					t.Fatalf("stored row %d = %#v, want generated id and name %q", i, stored[i], want)
				}
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
			egWipe(t, tg.source)
			gated := EgRows.Bind(tg.source, security.Gate(policy))
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
			if n, _ := EgRows.Bind(tg.source).Count(ctx); n != 0 {
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

func (this *countingSource) Exec(ctx context.Context, q string, args ...any) (crud.Result, error) {
	this.n++
	return this.Source.Exec(ctx, q, args...)
}

func (this *countingSource) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	this.n++
	return this.Source.Query(ctx, q, args...)
}
