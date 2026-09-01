package jobsfx_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsfx"
	"github.com/frostgrove/vv/jobs/jobsmemory"
)

func TestModuleBuildsAndRunsTheDefaultJobRuntime(t *testing.T) {
	handled := make(chan string, 1)
	automatic := testAutomatic(t, "jobsfx.message", func(_ context.Context, value string) error {
		handled <- value
		return nil
	})
	extra := testDefinition(t, "jobsfx.producer-only")
	namespace, err := jobs.NamespaceOf("jobsfx", "test")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:test")
	if err != nil {
		t.Fatal(err)
	}
	var catalog jobs.Catalog
	var queue *jobs.Queue
	var workers *jobs.Workers
	var backend *jobsmemory.Backend
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsDeclaration(func() *jobs.Definition[string] { return extra }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{
			Namespace: namespace,
			Workers: jobs.WorkersSpec{
				Build:        build,
				Identity:     testIdentityRestorer(),
				PollInterval: jobs.MinimumPollInterval,
			},
		}),
		fx.Populate(&catalog, &queue, &workers, &backend),
	)
	if err = app.Err(); err != nil {
		t.Fatal(err)
	}
	startContext, cancelStart := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStart()
	if err = app.Start(startContext); err != nil {
		t.Fatal(err)
	}
	started := true
	t.Cleanup(func() {
		if started {
			stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancelStop()
			_ = app.Stop(stopContext)
		}
	})
	if catalog.Len() != 2 || queue == nil || workers == nil || backend == nil {
		t.Fatalf("runtime catalog=%d queue=%v workers=%v backend=%v", catalog.Len(), queue, workers, backend)
	}
	if err = jobs.Go(context.Background(), automatic, "payload"); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-handled:
		if value != "payload" {
			t.Fatalf("handled %q", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not handle the job")
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	started = false
	if err = jobs.Go(context.Background(), automatic, "after-stop"); !errors.Is(err, jobs.ErrNotActivated) {
		t.Fatalf("enqueue after stop = %v", err)
	}
}

func TestWorkerFailureRequestsApplicationShutdown(t *testing.T) {
	automatic := testAutomatic(t, "jobsfx.failure", func(context.Context, string) error { return nil })
	namespace, err := jobs.NamespaceOf("jobsfx", "failure")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:failure")
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsBackend(newPanickingBackend),
		),
		jobsfx.Module(jobsfx.Spec{
			Namespace: namespace,
			Workers: jobs.WorkersSpec{
				Build:        build,
				Identity:     testIdentityRestorer(),
				PollInterval: jobs.MinimumPollInterval,
			},
		}),
	)
	if err = app.Err(); err != nil {
		t.Fatal(err)
	}
	startContext, cancelStart := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStart()
	if err = app.Start(startContext); err != nil {
		t.Fatal(err)
	}
	select {
	case signal := <-app.Wait():
		if signal.ExitCode != 1 {
			t.Fatalf("shutdown exit code = %d", signal.ExitCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker failure did not request shutdown")
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = app.Stop(stopContext); !errors.Is(err, jobs.ErrDriver) {
		t.Fatalf("stop = %v", err)
	}
}

type panickingBackend struct {
	*jobsmemory.Backend
}

func newPanickingBackend() (*panickingBackend, error) {
	backend, err := jobsmemory.NewDefault()
	if err != nil {
		return nil, err
	}
	return &panickingBackend{Backend: backend}, nil
}

func (*panickingBackend) Claim(context.Context, jobs.ClaimRequest) (jobs.ClaimBatch, error) {
	panic("claim failed")
}

func testAutomatic(t *testing.T, raw string, handler jobs.Handler[string]) *jobs.Automatic[string] {
	t.Helper()
	name, err := jobs.ParseName(raw)
	if err != nil {
		t.Fatal(err)
	}
	return jobs.MustMaterialize(jobs.Auto(handler), jobs.GeneratedDefinitionSpec[string]{Name: name, Codec: jobs.String(1)})
}

func testDefinition(t *testing.T, raw string) *jobs.Definition[string] {
	t.Helper()
	name, err := jobs.ParseName(raw)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	return jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(1), Policy: policy})
}

func testIdentityRestorer() jobs.TrustedIdentityRestorer {
	return jobs.TrustedIdentityRestorerFunc(func(ctx context.Context, _ jobs.IdentityRestoreRequest) (jobs.RestoredIdentity, error) {
		return jobs.NewRestoredIdentity(ctx, jobs.ProducerPartition{}, jobs.ProducerActor{})
	})
}
