//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

type gatePrincipal struct{}

func asTenant(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, gatePrincipal{}, name)
}

func gateTenantOf(ctx context.Context) (any, error) {
	t, ok := ctx.Value(gatePrincipal{}).(string)
	if !ok {
		return nil, security.Denied(security.Read, "no tenant in context")
	}
	return t, nil
}

var gateWholePolicy = security.Combine(
	security.ScopeField[EgParent, int64]("Name", gateTenantOf),
	security.ScopeRelationField[EgParent, int64]("Kids", "Name", gateTenantOf),
)

var gateTableOnlyPolicy = security.ScopeField[EgParent, int64]("Name", gateTenantOf)

var GateParents = sqlrepo.Define[EgParent, int64, struct{}]("eg_parents")

func gateSeed(t *testing.T, source crud.Source) {
	t.Helper()
	ctx := context.Background()
	parents, kids := EgParents.Bind(source), EgKids.Bind(source)

	for _, p := range []EgParent{{ID: 1, Name: "t1"}, {ID: 2, Name: "t2"}} {
		if _, err := parents.Save(ctx, &p); err != nil {
			t.Fatal(err)
		}
	}

	for _, k := range []EgKid{
		{ID: 10, ParentID: crud.Set(int64(1)), Name: "t1"},
		{ID: 11, ParentID: crud.Set(int64(1)), Name: "t2"},
	} {
		if _, err := kids.Save(ctx, &k); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheGatesScopeFollowsAPreload(t *testing.T) {
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			gateSeed(t, tg.source)
			ctx := asTenant(context.Background(), "t1")

			t.Run("declared", func(t *testing.T) {
				gated := GateParents.Bind(tg.source, security.Gate(gateWholePolicy))
				got, err := gated.GetAll(ctx, crud.Preload("Kids"), crud.OrderBy(crud.Asc("ID")))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 1 || got[0].ID != 1 {
					t.Fatalf("parents = %+v, want only t1's", got)
				}
				if len(got[0].Kids) != 1 || got[0].Kids[0].ID != 10 {
					t.Fatalf("kids = %+v: another tenant's row came back through the preload", got[0].Kids)
				}
			})

			t.Run("not declared", func(t *testing.T) {
				gated := GateParents.Bind(tg.source, security.Gate(gateTableOnlyPolicy))
				got, err := gated.GetAll(ctx, crud.Preload("Kids"))
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 1 {
					t.Fatalf("parents = %+v", got)
				}
				if len(got[0].Kids) != 2 {
					t.Fatalf("kids = %+v: the leak closed itself, so the case above proves nothing",
						got[0].Kids)
				}
			})
		})
	}
}

func TestTheGatesScopeFollowsANestedFilter(t *testing.T) {
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			gateSeed(t, tg.source)
			ctx := asTenant(context.Background(), "t1")

			gated := GateParents.Bind(tg.source, security.Gate(gateWholePolicy))
			n, err := gated.Count(ctx, crud.Where(crud.Eq("Kids.Name", "t2")))
			if err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("count = %d: the filter answered over another tenant's rows", n)
			}

			n, err = gated.Count(ctx, crud.Where(crud.Eq("Kids.Name", "t1")))
			if err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("count = %d, want 1", n)
			}

			leaky := GateParents.Bind(tg.source, security.Gate(gateTableOnlyPolicy))
			n, err = leaky.Count(ctx, crud.Where(crud.Eq("Kids.Name", "t2")))
			if err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("count = %d: the leak closed itself, so the case above proves nothing", n)
			}
		})
	}
}

func TestABlueprintNarrowingAndAPolicyNarrowingBothApply(t *testing.T) {
	egSetup(t)

	scoped := sqlrepo.Define[EgParent, int64, struct{}]("eg_parents",
		sqlrepo.RelationScope("Kids", crud.Ne("Name", "TOMBSTONE")))

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			gateSeed(t, tg.source)
			ctx := asTenant(context.Background(), "t1")

			extra := EgKid{ID: 12, ParentID: crud.Set(int64(1)), Name: "TOMBSTONE"}
			if _, err := EgKids.Bind(tg.source).Save(context.Background(), &extra); err != nil {
				t.Fatal(err)
			}

			gated := scoped.Bind(tg.source, security.Gate(gateWholePolicy))
			got, err := gated.GetAll(ctx, crud.Preload("Kids"))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("parents = %+v", got)
			}

			if len(got[0].Kids) != 1 || got[0].Kids[0].ID != 10 {
				t.Fatalf("kids = %+v: one of the two narrowings was dropped", got[0].Kids)
			}
		})
	}
}
