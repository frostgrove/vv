//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

// A scope is a WHERE clause, and a WHERE clause only ever constrains its own
// FROM. A preload is a second statement against a second table and a nested
// filter opens a correlated subquery with its own FROM, so neither inherits
// anything from the parent query — which is how ?preload=kids used to hand back
// exactly the rows the parent's scope existed to hide.
//
// The two engines get the same treatment because the leak was in the statement,
// not in the driver: what is asserted here is the rows that come back.

// egLiveParents narrows both ends: its own table, and the kids it reaches.
var egLiveParents = sqlrepo.Define[EgParent, int64, struct{}]("eg_parents",
	sqlrepo.Scope(crud.Ne("Name", "TOMBSTONE")),
	sqlrepo.RelationScope("Kids", crud.Ne("Name", "TOMBSTONE")))

func egSeedTree(t *testing.T, src crud.Source) {
	t.Helper()
	ctx := context.Background()
	parents, kids := EgParents.Bind(src), EgKids.Bind(src)

	for _, p := range []EgParent{{ID: 1, Name: "live"}, {ID: 2, Name: "TOMBSTONE"}} {
		if err := parents.Save(ctx, &p); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []EgKid{
		{ID: 10, ParentID: crud.Set(int64(1)), Name: "visible"},
		{ID: 11, ParentID: crud.Set(int64(1)), Name: "TOMBSTONE"},
	} {
		if err := kids.Save(ctx, &k); err != nil {
			t.Fatal(err)
		}
	}
}

func TestARelationScopeHidesTheSameRowsAPreloadWouldHaveExposed(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			egSeedTree(t, tg.src)
			live := egLiveParents.Bind(tg.src)

			t.Run("the preload", func(t *testing.T) {
				got, err := live.GetAll(ctx, crud.Preload("Kids"), crud.OrderBy(crud.Asc("ID")))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 1 || got[0].ID != 1 {
					t.Fatalf("the root query returned %+v, want only the live parent", got)
				}
				for _, k := range got[0].Kids {
					if k.Name == "TOMBSTONE" {
						t.Fatalf("the preload handed back %+v; the narrowing has to travel "+
							"with the hop or ?preload= reopens everything the scope closed", got[0].Kids)
					}
				}
				if len(got[0].Kids) != 1 {
					t.Fatalf("kids = %+v, want just the visible one", got[0].Kids)
				}
			})

			// The filter half is worse than a leak: a client can probe the
			// content of hidden rows and read the answer off the parent page.
			t.Run("the filter hop", func(t *testing.T) {
				got, err := live.GetAll(ctx, crud.Where(crud.Eq("Kids.Name", "TOMBSTONE")))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 0 {
					t.Fatalf("asking which parents have a hidden child answered %+v; "+
						"the EXISTS subquery is an oracle over rows the scope hides", got)
				}
			})

			// And the same question about a row that is not hidden still works,
			// so the narrowing is doing its job rather than breaking the feature.
			t.Run("a visible child is still findable", func(t *testing.T) {
				got, err := live.GetAll(ctx, crud.Where(crud.Eq("Kids.Name", "visible")))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 1 || got[0].ID != 1 {
					t.Fatalf("got %+v, want the parent of the visible child", got)
				}
			})
		})
	}
}

// Without the declaration nothing is narrowed, which is the honest half of the
// contract: RelationScope is per relation and a relation without one is read
// raw. Pinned so that the day a scope starts leaking sideways between models,
// this fails rather than silently changing what a query returns.
func TestARelationNobodyNarrowedIsStillReadWhole(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			egSeedTree(t, tg.src)

			plain := sqlrepo.Define[EgParent, int64, struct{}]("eg_parents",
				sqlrepo.Scope(crud.Ne("Name", "TOMBSTONE"))).Bind(tg.src)

			got, err := plain.GetAll(ctx, crud.Preload("Kids"))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || len(got[0].Kids) != 2 {
				t.Fatalf("parents = %+v; an undeclared relation is not narrowed", got)
			}
		})
	}
}
