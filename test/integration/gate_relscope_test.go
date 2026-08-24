//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/shardit-io/go-rx-crud/crud"
	"github.com/shardit-io/go-rx-crud/repo/basic"
	"github.com/shardit-io/go-rx-crud/repo/decorators/security"
)

// basic.RelationScope closes the preload leak for a narrowing that belongs to
// the table. This closes it for the other kind: a narrowing that depends on who
// is asking, which cannot be declared on the blueprint because the blueprint is
// built once, at start-up, and the principal arrives per request.
//
// The rows below are the shape that makes the leak real — a child table whose
// foreign key says nothing about which tenant the child belongs to. Kid 10 and
// kid 11 both hang off parent 1, and they belong to different tenants.

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

// The whole declaration: the table, and the table it can reach.
var gateWholePolicy = security.Combine(
	security.ScopeField[EgParent, int64]("Name", gateTenantOf),
	security.ScopeRelationField[EgParent, int64]("Kids", "Name", gateTenantOf),
)

// The same policy with the second line missing, kept so the assertions above it
// are measuring the declaration rather than something the preloader does anyway.
var gateTableOnlyPolicy = security.ScopeField[EgParent, int64]("Name", gateTenantOf)

var GateParents = basic.Define[EgParent, int64, struct{}]("eg_parents")

func gateSeed(t *testing.T, src crud.Source) {
	t.Helper()
	ctx := context.Background()
	parents, kids := EgParents.Bind(src), EgKids.Bind(src)

	for _, p := range []EgParent{{ID: 1, Name: "t1"}, {ID: 2, Name: "t2"}} {
		if err := parents.Save(ctx, &p); err != nil {
			t.Fatal(err)
		}
	}
	// Both hang off parent 1; only one of them is t1's.
	for _, k := range []EgKid{
		{ID: 10, ParentID: crud.Set(int64(1)), Name: "t1"},
		{ID: 11, ParentID: crud.Set(int64(1)), Name: "t2"},
	} {
		if err := kids.Save(ctx, &k); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheGatesScopeFollowsAPreload(t *testing.T) {
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			gateSeed(t, tg.src)
			ctx := asTenant(context.Background(), "t1")

			t.Run("declared", func(t *testing.T) {
				gated := GateParents.Bind(tg.src, security.Gate(gateWholePolicy))
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
				gated := GateParents.Bind(tg.src, security.Gate(gateTableOnlyPolicy))
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

// A nested filter is the other door into the same table: the EXISTS it opens has
// its own FROM, and answering it over rows the caller cannot see turns the
// filter into an oracle for another tenant's data.
func TestTheGatesScopeFollowsANestedFilter(t *testing.T) {
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			gateSeed(t, tg.src)
			ctx := asTenant(context.Background(), "t1")

			// "give me parents that have a t2 kid" — a question t1 may ask and
			// must always be answered no, because t1 cannot see t2's kids.
			gated := GateParents.Bind(tg.src, security.Gate(gateWholePolicy))
			n, err := gated.Count(ctx, crud.Where(crud.Eq("Kids.Name", "t2")))
			if err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("count = %d: the filter answered over another tenant's rows", n)
			}

			// The same question about its own kids still works, so the narrowing
			// did not simply break nested filters.
			n, err = gated.Count(ctx, crud.Where(crud.Eq("Kids.Name", "t1")))
			if err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("count = %d, want 1", n)
			}

			// Without the declaration the oracle answers.
			leaky := GateParents.Bind(tg.src, security.Gate(gateTableOnlyPolicy))
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

// The blueprint's narrowing and the policy's are different declarations by
// different authors, and both have to hold.
func TestABlueprintNarrowingAndAPolicyNarrowingBothApply(t *testing.T) {
	egSetup(t)

	// The table hides tombstones from everyone; the policy hides other tenants.
	scoped := basic.Define[EgParent, int64, struct{}]("eg_parents",
		basic.RelationScope("Kids", crud.Ne("Name", "TOMBSTONE")))

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			gateSeed(t, tg.src)
			ctx := asTenant(context.Background(), "t1")

			// One more kid of t1's, tombstoned.
			extra := EgKid{ID: 12, ParentID: crud.Set(int64(1)), Name: "TOMBSTONE"}
			if err := EgKids.Bind(tg.src).Save(context.Background(), &extra); err != nil {
				t.Fatal(err)
			}

			gated := scoped.Bind(tg.src, security.Gate(gateWholePolicy))
			got, err := gated.GetAll(ctx, crud.Preload("Kids"))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("parents = %+v", got)
			}
			// Kid 11 is another tenant's, kid 12 is a tombstone: one survives.
			if len(got[0].Kids) != 1 || got[0].Kids[0].ID != 10 {
				t.Fatalf("kids = %+v: one of the two narrowings was dropped", got[0].Kids)
			}
		})
	}
}
