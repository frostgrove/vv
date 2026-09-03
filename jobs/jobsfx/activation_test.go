package jobsfx_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsfx"
	"github.com/frostgrove/vv/jobs/jobsmemory"
	vvruntime "github.com/frostgrove/vv/runtime"
	"github.com/frostgrove/vv/runtime/runtimefx"
)

func TestAContainerThatHappensToHoldAConsumerIsNotAWorkerReplica(t *testing.T) {
	automatic := testAutomatic(t, "jobsfx.unstated-role", func(context.Context, string) error { return nil })
	namespace, err := jobs.NamespaceOf("jobsfx", "unstated-role")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:unstated-role")
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsDeclaration(func() jobs.Declaration { return automatic }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{
			Namespace: namespace,
			Workers:   jobs.WorkersSpec{Build: build, Identity: testIdentityRestorer()},
		}),
		runtimefx.Auto(),
	)
	if err = app.Err(); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("a graph with a consumer and no stated role built as %v", err)
	}
}

func TestAContainerThatHappensToHoldAScheduleDoesNotOwnTheClock(t *testing.T) {
	automatic := testAutomatic(t, "jobsfx.unstated-clock", func(context.Context, string) error { return nil })
	scheduleName, err := jobs.ParseName("jobsfx.unstated-cadence")
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := jobs.DefineSchedule(jobs.ScheduleSpec[string]{
		Name:     scheduleName,
		Revision: 1,
		Cadence:  jobs.At(time.Now().UTC().Add(time.Hour)),
		Job:      automatic,
		Payload:  func(time.Time) (string, error) { return "payload", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := jobs.NamespaceOf("jobsfx", "unstated-clock")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:unstated-clock")
	if err != nil {
		t.Fatal(err)
	}
	var scheduler *jobs.Scheduler
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsDeclaration(func() jobs.Declaration { return automatic }),
			jobsfx.AsSchedule(func() jobs.Schedule { return schedule }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{
			Namespace: namespace,
			Consuming: jobsfx.Disabled,
			Workers:   jobs.WorkersSpec{Build: build, Identity: testIdentityRestorer()},
		}),
		runtimefx.Auto(),
		fx.Populate(&scheduler),
	)
	if err = app.Err(); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("a graph with a schedule and no stated role built as %v", err)
	}
}

func TestARoleTurnedOffLeavesTheQueueAndBuildsNoWorkerPool(t *testing.T) {
	automatic := testAutomatic(t, "jobsfx.role-off", func(context.Context, string) error { return nil })
	namespace, err := jobs.NamespaceOf("jobsfx", "role-off")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:role-off")
	if err != nil {
		t.Fatal(err)
	}
	var queue *jobs.Queue
	var workers *jobs.Workers
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsDeclaration(func() jobs.Declaration { return automatic }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{
			Namespace:  namespace,
			Consuming:  jobsfx.Disabled,
			Scheduling: jobsfx.Disabled,
			Workers:    jobs.WorkersSpec{Build: build, Identity: testIdentityRestorer()},
		}),
		fx.Populate(&queue, &workers),
	)
	if err = app.Err(); err != nil {
		t.Fatal(err)
	}
	if queue == nil || workers != nil {
		t.Fatalf("queue=%v workers=%v", queue, workers)
	}
	startContext, cancelStart := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStart()
	if err = app.Start(startContext); err != nil {
		t.Fatal(err)
	}
	if err = jobs.Go(context.Background(), automatic, "produced"); err != nil {
		t.Fatal(err)
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
}

func TestAnEnabledRoleRunsUnderTheSupervisorAndIsNamedThere(t *testing.T) {
	automatic := testAutomatic(t, "jobsfx.supervised", func(context.Context, string) error { return nil })
	namespace, err := jobs.NamespaceOf("jobsfx", "supervised")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:supervised")
	if err != nil {
		t.Fatal(err)
	}
	var supervisor *vvruntime.Supervisor
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsDeclaration(func() jobs.Declaration { return automatic }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{
			Namespace: namespace,
			Consuming: jobsfx.Enabled,
			Workers: jobs.WorkersSpec{
				Build:        build,
				Identity:     testIdentityRestorer(),
				PollInterval: jobs.MinimumPollInterval,
			},
		}),
		runtimefx.Auto(),
		fx.Populate(&supervisor),
	)
	if err = app.Err(); err != nil {
		t.Fatal(err)
	}
	startContext, cancelStart := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStart()
	if err = app.Start(startContext); err != nil {
		t.Fatal(err)
	}
	pool := runnerState(t, supervisor, jobsfx.WorkersRunnerName)
	if pool.Phase != vvruntime.PhaseRunning {
		t.Fatalf("the worker pool is %s under the supervisor", pool.Phase)
	}
	if pool.Declaration.Placement != vvruntime.PerReplica || pool.Declaration.Durability != vvruntime.Durable {
		t.Fatalf("the worker pool declares %+v", pool.Declaration)
	}
	if err = waitReady(supervisor); err != nil {
		t.Fatalf("the supervisor never saw the pool as ready: %v", err)
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	if stopped := runnerState(t, supervisor, jobsfx.WorkersRunnerName); stopped.Phase != vvruntime.PhaseStopped {
		t.Fatalf("the drained pool is %s with %v", stopped.Phase, stopped.Err)
	}
}

func waitReady(supervisor *vvruntime.Supervisor) error {
	deadline := time.Now().Add(3 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		if err = supervisor.Ready(context.Background()); err == nil {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return err
}

func TestAnEnabledRoleRefusesToStartWhenNoSupervisorHoldsIt(t *testing.T) {
	automatic := testAutomatic(t, "jobsfx.unheld", func(context.Context, string) error { return nil })
	namespace, err := jobs.NamespaceOf("jobsfx", "unheld")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:unheld")
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsDeclaration(func() jobs.Declaration { return automatic }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
			func() (*vvruntime.Supervisor, error) { return vvruntime.Auto() },
		),
		jobsfx.Module(jobsfx.Spec{
			Namespace: namespace,
			Consuming: jobsfx.Enabled,
			Workers:   jobs.WorkersSpec{Build: build, Identity: testIdentityRestorer()},
		}),
	)
	if err = app.Err(); err != nil {
		t.Fatal(err)
	}
	startContext, cancelStart := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStart()
	err = app.Start(startContext)
	if !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("a worker role nobody supervises started as %v", err)
	}
	if !strings.Contains(err.Error(), jobsfx.WorkersRunnerName) {
		t.Fatalf("the refusal does not name the runner: %v", err)
	}
	if err = jobs.Go(context.Background(), automatic, "after"); !errors.Is(err, jobs.ErrNotActivated) {
		t.Fatalf("the queue was activated anyway: %v", err)
	}
}

func TestTheExplicitRunnersWaitForTheQueueTheModuleActivates(t *testing.T) {
	if _, err := jobsfx.WorkersRunner(nil, nil); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("a workers runner without workers = %v", err)
	}
	if _, err := jobsfx.SchedulerRunner(nil, nil); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("a scheduler runner without a scheduler = %v", err)
	}

	automatic := testAutomatic(t, "jobsfx.explicit-runner", func(context.Context, string) error { return nil })
	catalog := jobs.MustCatalog(automatic)
	build, err := jobs.ParseBuildID("jobsfx:explicit-runner")
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := jobs.NamespaceOf("jobsfx", "explicit-runner")
	if err != nil {
		t.Fatal(err)
	}
	backend, err := jobsmemory.NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	workers, err := jobs.NewWorkers(jobs.WorkersSpec{
		Namespace:    namespace,
		Catalog:      catalog,
		Driver:       backend,
		Build:        build,
		Identity:     testIdentityRestorer(),
		PollInterval: jobs.MinimumPollInterval,
	}, catalog.AutomaticConsumers()...)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	runner, err := jobsfx.WorkersRunner(workers, ready)
	if err != nil {
		t.Fatal(err)
	}
	if runner.Name() != jobsfx.WorkersRunnerName {
		t.Fatalf("runner name = %q", runner.Name())
	}
	supervisor, err := vvruntime.Auto(runner)
	if err != nil {
		t.Fatal(err)
	}
	if err = supervisor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err = supervisor.Ready(context.Background()); !errors.Is(err, jobs.ErrNotActivated) {
			t.Fatalf("the pool reached the backend before the queue was activated: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(ready)
	if err = waitReady(supervisor); err != nil {
		t.Fatalf("the pool never started after the gate opened: %v", err)
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = supervisor.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
}
