package jobspgfx_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsfx"
	"github.com/frostgrove/vv/jobs/jobspg"
	"github.com/frostgrove/vv/jobs/jobspg/jobspgfx"
	"github.com/frostgrove/vv/runtime/runtimefx"
)

func TestApplicationBuildsThePostgresRuntimeFromNamesAndDefaults(t *testing.T) {
	database := &sql.DB{}
	source := crudsql.Postgres(database)
	definition := testDefinition(t, "jobspgfx.application")
	consumer := jobs.On(definition, func(context.Context, string) error { return nil }, jobs.Concurrency(1))
	var queue *jobs.Queue
	var workers *jobs.Workers
	var driver *jobspg.Driver
	app := fx.New(
		fx.NopLogger,
		fx.Supply(database),
		fx.Provide(
			func() crud.Source { return source },
			jobsfx.AsDeclaration(func() jobs.Declaration { return definition }),
			jobsfx.AsConsumer(func() jobs.Consumer { return consumer }),
		),
		jobspgfx.Application(jobspgfx.ApplicationSettings{
			Application: "lease",
			Environment: "test",
			Consuming:   jobsfx.Enabled,
		}),
		runtimefx.Auto(),
		fx.Populate(&queue, &workers, &driver),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	wantNamespace, err := jobs.NamespaceOf("lease", "test")
	if err != nil {
		t.Fatal(err)
	}
	wantBuild, err := executableBuildID()
	if err != nil {
		t.Fatal(err)
	}
	if queue == nil || workers == nil || driver == nil {
		t.Fatalf("queue=%v workers=%v driver=%v", queue, workers, driver)
	}
	if queue.Namespace() != wantNamespace || workers.Describe().Namespace != wantNamespace || workers.Describe().Build != wantBuild {
		t.Fatalf("queue=%+v workers=%+v", queue.Namespace(), workers.Describe())
	}
}

func executableBuildID() (jobs.BuildID, error) {
	path, err := os.Executable()
	if err != nil {
		return jobs.BuildID{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return jobs.BuildID{}, err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil {
		return jobs.BuildID{}, err
	}
	return jobs.ParseBuildID("exe:sha256:" + hex.EncodeToString(digest.Sum(nil)))
}

func TestApplicationPreservesExplicitWorkerIdentityAndBuild(t *testing.T) {
	database := &sql.DB{}
	source := crudsql.Postgres(database)
	definition := testDefinition(t, "jobspgfx.explicit")
	consumer := jobs.On(definition, func(context.Context, string) error { return nil }, jobs.Concurrency(1))
	build, err := jobs.ParseBuildID("deploy:42")
	if err != nil {
		t.Fatal(err)
	}
	restorer := jobs.TrustedIdentityRestorerFunc(func(ctx context.Context, _ jobs.IdentityRestoreRequest) (jobs.RestoredIdentity, error) {
		return jobs.NewRestoredIdentity(ctx, jobs.ProducerPartition{}, jobs.ProducerActor{})
	})
	var workers *jobs.Workers
	app := fx.New(
		fx.NopLogger,
		fx.Supply(database),
		fx.Provide(
			func() crud.Source { return source },
			jobsfx.AsDeclaration(func() jobs.Declaration { return definition }),
			jobsfx.AsConsumer(func() jobs.Consumer { return consumer }),
		),
		jobspgfx.Application(jobspgfx.ApplicationSettings{
			Application: "lease",
			Environment: "test",
			Consuming:   jobsfx.Enabled,
			Workers: jobs.WorkersSpec{
				Build:    build,
				Identity: restorer,
			},
		}),
		runtimefx.Auto(),
		fx.Populate(&workers),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	if workers == nil || workers.Describe().Build != build {
		t.Fatalf("workers=%v", workers)
	}
}

func TestApplicationRejectsInvalidNamesBeforeBuildingTheGraph(t *testing.T) {
	err := fx.ValidateApp(jobspgfx.Application(jobspgfx.ApplicationSettings{
		Application: "bad application",
		Environment: "test",
	}))
	if err == nil || !strings.Contains(err.Error(), "application namespace") {
		t.Fatalf("validation error = %v", err)
	}
}
