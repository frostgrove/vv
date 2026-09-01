package jobspgfx_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsfx"
	"github.com/frostgrove/vv/jobs/jobspg"
	"github.com/frostgrove/vv/jobs/jobspg/jobspgfx"
)

func TestModulePublishesOneDriverThroughAllContracts(t *testing.T) {
	database := &sql.DB{}
	source := crudsql.Postgres(database)
	catalog := jobs.MustCatalog(testDefinition(t, "jobspgfx.contracts"))
	settings := jobspgfx.Settings{Namespace: testNamespace(t, "contracts")}
	var driver *jobspg.Driver
	var backend jobsfx.Backend
	var admin jobs.Admin
	var transactions jobs.FencedTransactions
	var retention jobs.RetentionSweeper
	app := fx.New(
		fx.NopLogger,
		fx.Supply(database, catalog),
		fx.Provide(func() crud.Source { return source }),
		jobspgfx.Module(settings),
		fx.Populate(&driver, &backend, &admin, &transactions, &retention),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	if driver == nil || backend != driver || admin != driver || transactions != driver || retention != driver {
		t.Fatalf("driver=%v backend=%v admin=%v transactions=%v retention=%v", driver, backend, admin, transactions, retention)
	}
}

func TestModuleCompletesTheJobsFxGraphWithoutPreparingInTheConstructor(t *testing.T) {
	database := &sql.DB{}
	source := crudsql.Postgres(database)
	definition := jobs.MustWire(jobs.Declare[string](), jobs.WireSpec[string]{
		Name:  testName(t, "jobspgfx.graph"),
		Codec: jobs.String(1),
	})
	namespace := testNamespace(t, "graph")
	var queue *jobs.Queue
	var driver *jobspg.Driver
	var admin jobs.Admin
	app := fx.New(
		fx.NopLogger,
		fx.Supply(database),
		fx.Provide(
			func() crud.Source { return source },
			jobsfx.AsDeclaration(func() *jobs.Automatic[string] { return definition }),
		),
		jobspgfx.Module(jobspgfx.Settings{Namespace: namespace}),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace}),
		fx.Populate(&queue, &driver, &admin),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	if queue == nil || driver == nil || admin != driver || queue.Backend() != driver.Description().ID() {
		t.Fatalf("queue=%v driver=%v admin=%v", queue, driver, admin)
	}
	if _, err := driver.Place(context.Background(), jobs.Placement{}); !errors.Is(err, jobspg.ErrNotReady) {
		t.Fatalf("driver was unexpectedly prepared by its constructor: %v", err)
	}
}

func TestModuleRequiresItsInjectedDependencies(t *testing.T) {
	err := fx.ValidateApp(
		jobspgfx.Module(jobspgfx.Settings{Namespace: testNamespace(t, "missing")}),
		fx.Invoke(func(*jobspg.Driver) {}),
	)
	if err == nil {
		t.Fatal("graph accepted missing database, source, and catalog")
	}
}

func testDefinition(t *testing.T, raw string) *jobs.Definition[string] {
	t.Helper()
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	return jobs.MustDefine(jobs.DefinitionSpec[string]{Name: testName(t, raw), Codec: jobs.String(1), Policy: policy})
}

func testName(t *testing.T, raw string) jobs.Name {
	t.Helper()
	name, err := jobs.ParseName(raw)
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func testNamespace(t *testing.T, environment string) jobs.Namespace {
	t.Helper()
	namespace, err := jobs.NamespaceOf("jobspgfx", environment)
	if err != nil {
		t.Fatal(err)
	}
	return namespace
}
