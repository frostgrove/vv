package app_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/app"
)

// The environments a test deployment has, and the two orders a security model
// needs: the roles have to exist before anything is granted one.
const (
	dev  = "dev"
	prod = "prod"

	orderRoles    = 100
	orderAccounts = 200
)

var envs = []string{dev, prod}

func runner(t *testing.T, environment string, seeders ...app.Seeder) *app.Runner {
	t.Helper()
	r, err := app.NewRunner(seeders, app.Seeding{
		Env:    environment,
		Known:  envs,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("wiring the runner: %v", err)
	}
	return r
}

// records is a seeder that writes its name into a slice, which is how the tests
// below see what ran and in what order.
func records(name string, order int, into *[]string, environments ...string) app.Seeder {
	return app.Seeder{
		Name:  name,
		Order: order,
		Envs:  environments,
		Run: func(context.Context) error {
			*into = append(*into, name)
			return nil
		},
	}
}

// An overlay runs in the environments it names and in no others. This is the
// whole point of the mechanism: development credentials are in the repository,
// so a seeder that named dev and ran in prod would put them in a real database.
func TestAnOverlayRunsOnlyInTheEnvironmentsItNames(t *testing.T) {
	for _, environment := range envs {
		var ran []string
		r := runner(t, environment,
			records("common", orderRoles, &ran),
			records("dev-only", orderAccounts, &ran, dev),
			records("prod-only", orderAccounts, &ran, prod),
		)
		if err := r.Run(context.Background()); err != nil {
			t.Fatalf("%s: %v", environment, err)
		}

		want := []string{"common", environment + "-only"}
		if !slices.Equal(ran, want) {
			t.Errorf("in %s the seed ran %v, want %v", environment, ran, want)
		}
	}
}

// Order decides what runs first, and it has to, because a set collected from
// independent contributors has no order of its own — the one it arrives in
// changes when somebody reorders a list of modules and takes the seed with it.
func TestSeedersRunInTheirDeclaredOrder(t *testing.T) {
	var ran []string
	r := runner(t, dev,
		records("accounts", orderAccounts, &ran),
		records("roles", orderRoles, &ran),
	)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ran, []string{"roles", "accounts"}) {
		t.Fatalf("ran %v; an account cannot be granted a role that has not been created", ran)
	}
}

// Two seeders at the same order run in name order, so two runs of one build
// produce the same log and a log from one is comparable with a log from another.
func TestSeedersAtTheSameOrderRunByName(t *testing.T) {
	var ran []string
	r := runner(t, dev,
		records("zulu", orderRoles, &ran),
		records("alpha", orderRoles, &ran),
	)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ran, []string{"alpha", "zulu"}) {
		t.Fatalf("ran %v, want them sorted by name", ran)
	}
}

// The wiring mistakes are refused before anything runs. Each of them otherwise
// produces a seed that half works and reports success.
func TestTheRunnerRefusesAMisregisteredSeeder(t *testing.T) {
	nothing := func(context.Context) error { return nil }
	for name, seeders := range map[string][]app.Seeder{
		"one with no name":        {{Order: orderRoles, Run: nothing}},
		"one with nothing to run": {{Name: "empty", Order: orderRoles}},
		"two with the same name": {
			{Name: "roles", Run: nothing},
			{Name: "roles", Run: nothing},
		},
		"one naming an environment that does not exist": {
			{Name: "typo", Envs: []string{"prd"}, Run: nothing},
		},
	} {
		_, err := app.NewRunner(seeders, app.Seeding{Env: dev, Known: envs})
		if !errors.Is(err, app.ErrSeeder) {
			t.Errorf("%s was accepted (%v)", name, err)
		}
	}

	// The control: a well-formed set wires. Without it the four refusals above
	// would pass on a constructor that refused everything.
	runner(t, dev, app.Seeder{Name: "fine", Run: nothing})
}

// A deployment that does not list its environments gets no typo check, because
// there is nothing to check against. It must not get a refusal instead.
func TestAnEnvironmentIsOnlyCheckedAgainstOneThatWasDeclared(t *testing.T) {
	nothing := func(context.Context) error { return nil }
	seeders := []app.Seeder{{Name: "typo", Envs: []string{"prd"}, Run: nothing}}

	if _, err := app.NewRunner(seeders, app.Seeding{Env: dev}); err != nil {
		t.Fatalf("a deployment that named no environments was refused: %v", err)
	}

	// The control. With the set declared, the same typo is caught — so the case
	// above is the check being off rather than the check being broken.
	if _, err := app.NewRunner(seeders, app.Seeding{Env: dev, Known: envs}); !errors.Is(err, app.ErrSeeder) {
		t.Fatal("an environment nobody has was accepted even with the set declared, so the typo check does nothing at all")
	}
}

// A failure stops the run and names the seeder. Carrying on produces a second,
// misleading failure from a seeder that depended on the first, and buries the
// one somebody has to fix.
func TestAFailingSeederStopsTheRunAndIsNamed(t *testing.T) {
	broken := errors.New("the database said no")
	var ran []string
	r := runner(t, dev,
		app.Seeder{Name: "roles", Order: orderRoles, Run: func(context.Context) error { return broken }},
		records("accounts", orderAccounts, &ran),
	)

	err := r.Run(context.Background())
	if !errors.Is(err, broken) {
		t.Fatalf("the run answered %v, want the seeder's own failure", err)
	}
	if !strings.Contains(err.Error(), "roles") {
		t.Fatalf("the failure does not name the seeder: %v", err)
	}
	if len(ran) != 0 {
		t.Fatalf("%v ran after the failure", ran)
	}
}
