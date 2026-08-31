package appfx_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/app"
	"github.com/frostgrove/vv/app/appfx"
)

func records(name string, order int, into *[]string) func() app.Seeder {
	return func() app.Seeder {
		return app.Seeder{Name: name, Order: order, Run: func(context.Context) error {
			*into = append(*into, name)
			return nil
		}}
	}
}

func TestSeedersFromEveryContributorReachTheRunner(t *testing.T) {
	var ran []string
	var runner *app.Runner

	err := fx.New(
		fx.Provide(appfx.AsSeeder(records("accounts", 200, &ran))),
		fx.Provide(appfx.AsSeeder(records("roles", 100, &ran))),
		appfx.Seeding(app.Seeding{Env: "dev"}),
		fx.Populate(&runner),
	).Err()
	if err != nil {
		t.Fatalf("wiring the seed command: %v", err)
	}
	if err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if want := []string{"roles", "accounts"}; !slices.Equal(ran, want) {
		t.Fatalf("the seed ran %v, want %v; an account cannot be granted a role that has not been created", ran, want)
	}
}

func TestAMisregisteredSeederFailsTheCommand(t *testing.T) {
	var runner *app.Runner
	twice := records("roles", 100, new([]string))

	err := fx.New(
		fx.Provide(appfx.AsSeeder(twice)),
		fx.Provide(fx.Annotate(twice, fx.ResultTags(`group:"vv.app.seeders"`))),
		appfx.Seeding(app.Seeding{Env: "dev"}),
		fx.Populate(&runner),
	).Err()

	if !errors.Is(err, app.ErrSeeder) {
		t.Fatalf("two seeders with one name were accepted (%v); half the run would be doing the other's work", err)
	}
}
