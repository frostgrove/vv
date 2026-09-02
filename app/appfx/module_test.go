package appfx_test

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/app"
	"github.com/frostgrove/vv/app/appfx"
	"github.com/frostgrove/vv/app/module"
)

type repository struct{}

type documents struct{}

type sweeper struct{}

func workspace() module.Definition {
	return module.New("workspace").
		Provide(func() *repository { return &repository{} }).
		Routes(func(*repository) *documents { return &documents{} }).
		Workers(func(*repository) *sweeper { return &sweeper{} }).
		MustBuild()
}

func TestAModuleReachesTheGraphThroughItsDefinitionAlone(t *testing.T) {
	var mounted *documents

	err := fx.New(
		appfx.Options(module.MustCatalog(workspace()), module.Serving),
		fx.Populate(&mounted),
	).Err()

	if err != nil {
		t.Fatalf("the API replica did not build its routes: %v", err)
	}
}

func TestTheProfileIsWhatDecidesWhichHalfOfAModuleIsWired(t *testing.T) {
	var started *sweeper

	err := fx.New(
		appfx.Options(module.MustCatalog(workspace()), module.Serving),
		fx.Populate(&started),
	).Err()

	if err == nil {
		t.Fatal("an API replica built the module's worker; the deployment profile decided nothing")
	}
}

func TestASeederContributedByAModuleReachesTheSeedRunner(t *testing.T) {
	var ran []string
	var runner *app.Runner

	catalog := module.MustCatalog(
		module.New("accounts").Seeders(appfx.AsSeeder(records("accounts", 200, &ran))).MustBuild(),
		module.Auto("access", func() *repository { return &repository{} }),
	)

	err := fx.New(
		appfx.Options(catalog, module.Seeding),
		appfx.Seeding(app.Seeding{Env: "dev"}),
		fx.Populate(&runner),
	).Err()
	if err != nil {
		t.Fatalf("wiring the seed command: %v", err)
	}
	if err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(ran) != 1 {
		t.Fatalf("the seed command ran %v; a seeder contributed by a module is the same contribution as one provided by hand", ran)
	}
}

func TestAnApiReplicaRunsNoSeeder(t *testing.T) {
	var ran []string
	var runner *app.Runner

	catalog := module.MustCatalog(
		module.New("accounts").Seeders(appfx.AsSeeder(records("accounts", 200, &ran))).MustBuild(),
		module.Auto("access", func() *repository { return &repository{} }),
	)

	err := fx.New(
		appfx.Options(catalog, module.Serving),
		appfx.Seeding(app.Seeding{Env: "dev"}),
		fx.Populate(&runner),
	).Err()
	if err != nil {
		t.Fatalf("wiring the API replica: %v", err)
	}
	if err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(ran) != 0 {
		t.Fatalf("an API replica seeded %v on start-up", ran)
	}
}

func TestAProfileNothingAnswersToFailsTheGraphRatherThanProvidingNothing(t *testing.T) {
	err := fx.New(
		appfx.Options(module.MustCatalog(workspace()), module.Profile{Name: "batch", Roles: []module.Role{"cron"}}),
	).Err()

	if !errors.Is(err, module.ErrProfile) {
		t.Fatalf("a profile naming an unknown role built a graph (%v); a container that is handed nothing starts perfectly",
			err)
	}
}

func TestOneModuleIsWiredTheWayTheWholeCatalogIs(t *testing.T) {
	var mounted *documents

	err := fx.New(
		appfx.Option(workspace(), module.Serving),
		fx.Populate(&mounted),
	).Err()

	if err != nil {
		t.Fatalf("the single-module form did not build what the catalog form does: %v", err)
	}
}

func TestAutoIsEveryRoleOfEveryModule(t *testing.T) {
	var mounted *documents
	var started *sweeper

	err := fx.New(
		appfx.Auto(module.MustCatalog(workspace())),
		fx.Populate(&mounted, &started),
	).Err()

	if err != nil {
		t.Fatalf("the complete profile left something out: %v", err)
	}
}
