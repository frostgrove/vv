package jobspgfx_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsfx"
	"github.com/frostgrove/vv/jobs/jobspg"
	"github.com/frostgrove/vv/jobs/jobspg/jobspgfx"
)

func TestAnEnvironmentNobodyRecognisesIsTreatedAsProduction(t *testing.T) {
	for _, environment := range []string{"staging", "prod", "production", "PreProd", "", "  "} {
		if profile := jobspgfx.ProfileOf(environment); profile != jobspgfx.ProductionProfile {
			t.Fatalf("environment %q resolved to the %s profile", environment, profile)
		}
	}
	for _, environment := range []string{"dev", "Development", " local "} {
		if profile := jobspgfx.ProfileOf(environment); profile != jobspgfx.DevelopmentProfile {
			t.Fatalf("environment %q resolved to the %s profile", environment, profile)
		}
	}
	for _, environment := range []string{"test", "TESTING", "ci"} {
		if profile := jobspgfx.ProfileOf(environment); profile != jobspgfx.TestProfile {
			t.Fatalf("environment %q resolved to the %s profile", environment, profile)
		}
	}
}

func TestAProductionApplicationVerifiesTheJobsSchemaAndNeverMigratesIt(t *testing.T) {
	driver, decision := buildProfiledApplication(t, jobspgfx.ApplicationSettings{Application: "lease", Environment: "prod"})
	if driver.SchemaManagement() != jobspg.VerifySchema {
		t.Fatalf("a production application migrates its own jobs schema: %v", driver.SchemaManagement())
	}
	if decision.Profile != jobspgfx.ProductionProfile || decision.Overridden {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestADevelopmentOrTestApplicationStillManagesItsOwnJobsSchema(t *testing.T) {
	for _, environment := range []string{"dev", "test"} {
		driver, decision := buildProfiledApplication(t, jobspgfx.ApplicationSettings{Application: "lease", Environment: environment})
		if driver.SchemaManagement() != jobspg.ManageSchema {
			t.Fatalf("environment %q lost its zero-configuration migration: %v", environment, driver.SchemaManagement())
		}
		if decision.Overridden {
			t.Fatalf("environment %q was recorded as a production override", environment)
		}
	}
}

func TestManagedSchemaInProductionIsRefusedUntilSomebodyAsksForItByName(t *testing.T) {
	settings := jobspgfx.ApplicationSettings{Application: "lease", Environment: "prod", SchemaManagement: jobspg.ManageSchema}
	_, err := settings.SchemaManagementDecision()
	if !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("production accepted schema management without an override: %v", err)
	}
	if err = fx.ValidateApp(jobspgfx.Application(settings)); err == nil || !strings.Contains(err.Error(), "does not migrate its own jobs schema") {
		t.Fatalf("the refusal did not reach the graph: %v", err)
	}
}

func TestAProductionOverrideMigratesAndIsRecordedForTheGraphToRead(t *testing.T) {
	driver, decision := buildProfiledApplication(t, jobspgfx.ApplicationSettings{
		Application:                    "lease",
		Environment:                    "prod",
		SchemaManagement:               jobspg.ManageSchema,
		AllowManagedSchemaInProduction: true,
	})
	if driver.SchemaManagement() != jobspg.ManageSchema {
		t.Fatalf("an acknowledged override did not reach the driver: %v", driver.SchemaManagement())
	}
	if decision.Profile != jobspgfx.ProductionProfile || !decision.Overridden {
		t.Fatalf("decision = %+v", decision)
	}
}

func buildProfiledApplication(t *testing.T, settings jobspgfx.ApplicationSettings) (*jobspg.Driver, jobspgfx.SchemaManagementDecision) {
	t.Helper()
	settings.Consuming = jobsfx.Disabled
	database := &sql.DB{}
	source := crudsql.Postgres(database)
	definition := testDefinition(t, "jobspgfx.profile."+settings.Environment)
	consumer := jobs.On(definition, func(context.Context, string) error { return nil }, jobs.Concurrency(1))
	var driver *jobspg.Driver
	var decision jobspgfx.SchemaManagementDecision
	app := fx.New(
		fx.NopLogger,
		fx.Supply(database),
		fx.Provide(
			func() crud.Source { return source },
			jobsfx.AsDeclaration(func() jobs.Declaration { return definition }),
			jobsfx.AsConsumer(func() jobs.Consumer { return consumer }),
		),
		jobspgfx.Application(settings),
		fx.Populate(&driver, &decision),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	if driver == nil {
		t.Fatal("no driver was built")
	}
	return driver, decision
}
