//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

// A GROUP BY written by hand runs outside every narrowing the repository
// applies, so the moment an application needs a total it drops out from under
// its own access control. These are about the summary carrying the same
// narrowing the rows do.

func aggSeed(t *testing.T, tg egTarget) {
	t.Helper()
	ctx := context.Background()
	rows := EgRows.Bind(tg.source)
	for _, r := range []EgRow{
		{ID: 1, Tenant: 1, Name: "a", Score: crud.Set(10)},
		{ID: 2, Tenant: 1, Name: "b", Score: crud.Set(20)},
		{ID: 3, Tenant: 1, Name: "c", Score: crud.Null[int]()},
		{ID: 4, Tenant: 2, Name: "d", Score: crud.Set(100)},
		{ID: 5, Tenant: 2, Name: "e", Score: crud.Set(200)},
	} {
		row := r
		if _, err := rows.Save(ctx, &row); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAggregatesGroupAndSummarise(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			aggSeed(t, tg)
			rows := EgRows.Bind(tg.source)

			got, err := rows.Aggregate(ctx,
				crud.GroupBy("Tenant"),
				crud.Aggregate(
					crud.CountAll("rows"),
					crud.CountOf("scored", "Score"),
					crud.Sum("total", "Score"),
					crud.Min("lowest", "Score"),
					crud.Max("highest", "Score"),
				),
				crud.OrderBy(crud.Asc("Tenant")),
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 {
				t.Fatalf("groups = %d, want 2", len(got))
			}

			// Tenant 1: three rows, two of them scored, 10 + 20.
			first := got[0]
			if n, ok := first.Int("rows"); !ok || n != 3 {
				t.Fatalf("count(*) = %v %v", first.Value["rows"], ok)
			}
			if n, ok := first.Int("scored"); !ok || n != 2 {
				t.Fatalf("count(score) = %v: a NULL was counted", first.Value["scored"])
			}
			if n, ok := first.Int("total"); !ok || n != 30 {
				t.Fatalf("sum = %v", first.Value["total"])
			}
			if n, ok := first.Int("lowest"); !ok || n != 10 {
				t.Fatalf("min = %v", first.Value["lowest"])
			}
			if n, ok := first.Int("highest"); !ok || n != 20 {
				t.Fatalf("max = %v", first.Value["highest"])
			}
			// The grouping column comes back so the caller does not have to
			// correlate rows by position.
			if _, ok := first.Group["Tenant"]; !ok {
				t.Fatalf("group = %v, want the grouping column on the row", first.Group)
			}
		})
	}
}

// A filter narrows the summary, the same way it narrows a page.
func TestAnAggregateHonoursTheFilter(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			aggSeed(t, tg)
			rows := EgRows.Bind(tg.source)

			got, err := rows.Aggregate(ctx,
				crud.Where(crud.Eq("Tenant", 1)),
				crud.Aggregate(crud.CountAll("n")),
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("rows = %d, want one ungrouped summary", len(got))
			}
			if n, _ := got[0].Int("n"); n != 3 {
				t.Fatalf("count = %d, want 3", n)
			}
		})
	}
}

// The repository's permanent scope reaches the summary too.
func TestAnAggregateHonoursThePermanentScope(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	scoped := sqlrepo.Define[EgRow, int64, EgRowUpdate]("eg_rows",
		sqlrepo.Scope(crud.Eq("Tenant", 1)))

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			aggSeed(t, tg)

			got, err := scoped.Bind(tg.source).Aggregate(ctx, crud.Aggregate(crud.CountAll("n")))
			if err != nil {
				t.Fatal(err)
			}
			if n, _ := got[0].Int("n"); n != 3 {
				t.Fatalf("count = %d, want 3: the scope did not reach the aggregate", n)
			}

			// The control: without the scope the same call sees everything, so
			// the assertion above is measuring the scope and not the fixture.
			all, err := EgRows.Bind(tg.source).Aggregate(ctx, crud.Aggregate(crud.CountAll("n")))
			if err != nil {
				t.Fatal(err)
			}
			if n, _ := all[0].Int("n"); n != 5 {
				t.Fatalf("unscoped count = %d, want 5", n)
			}
		})
	}
}

// And the gate's, which is the one that would have been missed: the gate embeds
// crud.Core, so an Aggregate it did not override would have fallen straight
// through to the plain repository and counted every tenant's rows.
func TestAnAggregateHonoursTheSecurityGate(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	policy := security.ScopeField[EgRow, int64]("Tenant", func(c context.Context) (any, error) {
		t, ok := c.Value(gatePrincipal{}).(int64)
		if !ok {
			return nil, security.Denied(security.Read, "no tenant in context")
		}
		return t, nil
	})

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			aggSeed(t, tg)
			gated := EgRows.Bind(tg.source, security.Gate(policy))

			mine := context.WithValue(ctx, gatePrincipal{}, int64(1))
			got, err := gated.Aggregate(mine, crud.Aggregate(crud.CountAll("n"), crud.Sum("total", "Score")))
			if err != nil {
				t.Fatal(err)
			}
			if n, _ := got[0].Int("n"); n != 3 {
				t.Fatalf("count = %d, want 3: the aggregate escaped the gate", n)
			}
			if n, _ := got[0].Int("total"); n != 30 {
				t.Fatalf("sum = %d, want 30: the aggregate summed another tenant's rows", n)
			}

			// A principal the policy cannot resolve gets nothing, not everything.
			if _, err := gated.Aggregate(ctx, crud.Aggregate(crud.CountAll("n"))); err == nil {
				t.Fatal("an aggregate ran without a principal")
			}
		})
	}
}

// Names are resolved against the model like every other path, so a typo is a
// refusal rather than a statement the database rejects.
func TestAnAggregateRefusesWhatTheModelDoesNotHave(t *testing.T) {
	ctx := context.Background()
	egSetup(t)
	rows := EgRows.Bind(egEngines()[0].source)

	for _, tc := range []struct {
		name    string
		options []crud.Option
	}{
		{"unknown field", []crud.Option{crud.Aggregate(crud.Sum("s", "Nope"))}},
		{"unknown group", []crud.Option{crud.Aggregate(crud.CountAll("n")), crud.GroupBy("Nope")}},
		{"unknown function", []crud.Option{crud.Aggregate(crud.Aggregation{As: "x", Fn: "MEDIAN", Field: "Score"})}},
		{"no aggregation at all", []crud.Option{crud.GroupBy("Tenant")}},
		{"two under one name", []crud.Option{crud.Aggregate(crud.CountAll("n"), crud.Sum("n", "Score"))}},
		{"a sum with no field", []crud.Option{crud.Aggregate(crud.Aggregation{As: "s", Fn: "SUM"})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := rows.Aggregate(ctx, tc.options...); err == nil {
				t.Fatal("accepted")
			} else if errors.Is(err, crud.ErrNotFound) {
				t.Fatalf("err = %v, want a declaration error", err)
			}
		})
	}
}
