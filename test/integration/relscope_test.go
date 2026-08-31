//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

var egLiveParents = sqlrepo.Define[EgParent, int64, struct{}]("eg_parents",
	sqlrepo.Scope(crud.Ne("Name", "TOMBSTONE")),
	sqlrepo.RelationScope("Kids", crud.Ne("Name", "TOMBSTONE")))

func egSeedTree(t *testing.T, source crud.Source) {
	t.Helper()
	ctx := context.Background()
	parents, kids := EgParents.Bind(source), EgKids.Bind(source)

	for _, p := range []EgParent{{ID: 1, Name: "live"}, {ID: 2, Name: "TOMBSTONE"}} {
		if _, err := parents.Save(ctx, &p); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []EgKid{
		{ID: 10, ParentID: crud.Set(int64(1)), Name: "visible"},
		{ID: 11, ParentID: crud.Set(int64(1)), Name: "TOMBSTONE"},
	} {
		if _, err := kids.Save(ctx, &k); err != nil {
			t.Fatal(err)
		}
	}
}

func TestARelationScopeHidesTheSameRowsAPreloadWouldHaveExposed(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			egSeedTree(t, tg.source)
			live := egLiveParents.Bind(tg.source)

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

func TestARelationNobodyNarrowedIsStillReadWhole(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			egSeedTree(t, tg.source)

			plain := sqlrepo.Define[EgParent, int64, struct{}]("eg_parents",
				sqlrepo.Scope(crud.Ne("Name", "TOMBSTONE"))).Bind(tg.source)

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
