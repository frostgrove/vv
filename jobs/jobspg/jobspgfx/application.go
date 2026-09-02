package jobspgfx

import (
	"context"
	"fmt"
	"io"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsfx"
	"github.com/frostgrove/vv/jobs/jobspg"
)

type ApplicationSettings struct {
	Application                    string
	Environment                    string
	Schema                         string
	Backend                        jobs.BackendID
	Entropy                        io.Reader
	SchemaManagement               jobspg.SchemaManagement
	AllowManagedSchemaInProduction bool
	Housekeeping                   HousekeepingSettings
	Catalog                        jobs.Catalog
	Queue                          jobs.QueueSpec
	Workers                        jobs.WorkersSpec
	Scheduler                      jobs.SchedulerSpec
}

func Application(settings ApplicationSettings) fx.Option {
	namespace, err := jobs.NamespaceOf(settings.Application, settings.Environment)
	if err != nil {
		return fx.Error(fmt.Errorf("jobspgfx: application namespace: %w", err))
	}
	decision, err := settings.SchemaManagementDecision()
	if err != nil {
		return fx.Error(err)
	}
	workers := settings.Workers
	if workers.Build.IsZero() {
		workers.Build, err = currentExecutableBuild()
		if err != nil {
			return fx.Error(fmt.Errorf("jobspgfx: application build: %w", err))
		}
	}
	if workers.Identity == nil {
		workers.Identity = jobs.TrustedIdentityRestorerFunc(func(ctx context.Context, _ jobs.IdentityRestoreRequest) (jobs.RestoredIdentity, error) {
			return jobs.NewRestoredIdentity(ctx, jobs.ProducerPartition{}, jobs.ProducerActor{})
		})
	}
	return fx.Options(
		Module(Settings{
			Namespace:        namespace,
			Schema:           settings.Schema,
			Backend:          settings.Backend,
			Entropy:          settings.Entropy,
			SchemaManagement: decision.Management,
			Housekeeping:     settings.Housekeeping,
		}),
		fx.Supply(decision),
		jobsfx.Module(jobsfx.Spec{
			Namespace: namespace,
			Catalog:   settings.Catalog,
			Queue:     settings.Queue,
			Workers:   workers,
			Scheduler: settings.Scheduler,
		}),
	)
}
