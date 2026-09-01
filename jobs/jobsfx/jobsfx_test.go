package jobsfx_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsfx"
	"github.com/frostgrove/vv/jobs/jobsmemory"
)

type injectedJob struct {
	standard chan string
	adapter  chan jobs.DeliveryMeta
	options  []jobs.WorkerOption
}

func (job *injectedJob) Handle(_ context.Context, payload string) error {
	job.standard <- payload
	return nil
}

func (job *injectedJob) HandleAdapter(_ context.Context, _ string, meta jobs.DeliveryMeta, _ jobs.AttemptController) error {
	job.adapter <- meta
	return nil
}

func (job *injectedJob) JobOptions(jobs.Name) []jobs.WorkerOption {
	return append([]jobs.WorkerOption(nil), job.options...)
}

type preparingBackend struct {
	*jobsmemory.Backend
	automatic *jobs.Automatic[string]
	called    bool
	before    error
	err       error
	panics    bool
}

func (backend *preparingBackend) Prepare(ctx context.Context) error {
	if backend.panics {
		panic("prepare")
	}
	backend.called = true
	backend.before = jobs.Go(ctx, backend.automatic, "before")
	return backend.err
}

func TestGeneratedBundleInjectsTypedHandlersAndAppliesConcurrency(t *testing.T) {
	standard := jobsfx.Auto((*injectedJob).Handle)
	adapter := jobsfx.AutoAdapter((*injectedJob).HandleAdapter, jobs.Heavy)
	jobs.MustMaterialize(standard.Automatic, jobs.GeneratedDefinitionSpec[string]{Name: testName(t, "jobsfx.injected"), Codec: jobs.String(1)})
	jobs.MustMaterialize(adapter.Automatic, jobs.GeneratedDefinitionSpec[string]{Name: testName(t, "jobsfx.injected-adapter"), Codec: jobs.String(1)})
	producer := testDefinition(t, "jobsfx.injected-producer")
	catalog := jobs.MustCatalog(standard, adapter, producer)
	var _ jobs.DefinitionOf[string] = standard
	var nilBinding *jobsfx.Binding[*injectedJob, string]
	if err := nilBinding.Go(context.Background(), "nil"); !errors.Is(err, jobs.ErrNotActivated) {
		t.Fatalf("nil binding enqueue = %v", err)
	}
	namespace, err := jobs.NamespaceOf("jobsfx", "injected")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:injected")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := jobs.NewAdmissionSnapshot(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = snapshot.Publisher().Update(1, jobs.HeldReason{}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	classifier := jobs.Classify(func(failure jobs.HandlerFailure) jobs.Disposition {
		reason := jobs.ReasonHandlerFailure
		if failure.Panicked() {
			reason = jobs.ReasonPanic
		}
		disposition, _ := jobs.RetryDisposition(reason, jobs.PublicFailure{}, 0, jobs.RetryCostCharged)
		return disposition
	})
	handler := &injectedJob{
		standard: make(chan string, 1),
		adapter:  make(chan jobs.DeliveryMeta, 1),
		options:  []jobs.WorkerOption{classifier, jobs.WithAdmission(snapshot.Reader())},
	}
	var queue *jobs.Queue
	var workers *jobs.Workers
	var runtimeCatalog jobs.Catalog
	app := fx.New(
		fx.NopLogger,
		fx.Supply(handler),
		fx.Provide(jobsfx.AsBackend(jobsmemory.NewDefault)),
		jobsfx.Bundle(catalog, []jobsfx.BundleOption{jobsfx.Concurrency(map[jobs.Name]int{standard.Name(): 3})}, standard, adapter),
		jobsfx.Module(jobsfx.Spec{
			Namespace: namespace,
			Workers: jobs.WorkersSpec{
				Build:        build,
				Identity:     testIdentityRestorer(),
				PollInterval: jobs.MinimumPollInterval,
			},
		}),
		fx.Populate(&queue, &workers, &runtimeCatalog),
	)
	if err = app.Err(); err != nil {
		t.Fatal(err)
	}
	startContext, cancelStart := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStart()
	if err = app.Start(startContext); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelStop()
		_ = app.Stop(stopContext)
	})
	if runtimeCatalog.Len() != 3 || workers.Describe().Plan.TotalConcurrency != 4 {
		t.Fatalf("catalog=%d concurrency=%d", runtimeCatalog.Len(), workers.Describe().Plan.TotalConcurrency)
	}
	for _, binding := range workers.Describe().Plan.Bindings {
		if !binding.CustomClassifier || !binding.DynamicAdmission {
			t.Fatalf("handler options were not applied: %#v", binding)
		}
	}
	if _, err = jobs.Enqueue(context.Background(), queue, standard, "standard"); err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.Enqueue(context.Background(), queue, adapter, "adapter"); err != nil {
		t.Fatal(err)
	}
	select {
	case payload := <-handler.standard:
		if payload != "standard" {
			t.Fatalf("standard payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("injected standard handler did not run")
	}
	select {
	case meta := <-handler.adapter:
		if meta.Definition() != adapter.Name() {
			t.Fatalf("adapter definition = %s", meta.Definition())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("injected adapter handler did not run")
	}
	if err = standard.Go(context.Background(), "automatic"); err != nil {
		t.Fatal(err)
	}
	select {
	case payload := <-handler.standard:
		if payload != "automatic" {
			t.Fatalf("automatic payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("binding Go did not enqueue")
	}
}

func TestBundleAllowsManualConsumerOverride(t *testing.T) {
	automatic := testAutomatic(t, "jobsfx.manual-override", func(context.Context, string) error { return nil })
	catalog := jobs.MustCatalog(automatic)
	namespace, err := jobs.NamespaceOf("jobsfx", "manual-override")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:manual-override")
	if err != nil {
		t.Fatal(err)
	}
	manual := jobs.On(automatic, func(context.Context, string) error { return nil }, jobs.Concurrency(7), jobs.Classify(func(jobs.HandlerFailure) jobs.Disposition {
		disposition, _ := jobs.RetryDisposition(jobs.ReasonHandlerFailure, jobs.PublicFailure{}, 0, jobs.RetryCostCharged)
		return disposition
	}))
	var workers *jobs.Workers
	app := fx.New(
		fx.NopLogger,
		jobsfx.Bundle(catalog, nil),
		fx.Provide(
			jobsfx.AsConsumer(func() jobs.Consumer { return manual }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace, Workers: jobs.WorkersSpec{Build: build, Identity: testIdentityRestorer()}}),
		fx.Populate(&workers),
	)
	if err = app.Err(); err != nil {
		t.Fatal(err)
	}
	description := workers.Describe().Plan
	if description.TotalConcurrency != 7 || len(description.Bindings) != 1 || !description.Bindings[0].CustomClassifier {
		t.Fatalf("manual worker plan = %#v", description)
	}
}

func TestModulePreparesOptionalBackendBeforeQueueActivation(t *testing.T) {
	automatic := testAutomatic(t, "jobsfx.prepared", func(context.Context, string) error { return nil })
	memory, err := jobsmemory.NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	backend := &preparingBackend{Backend: memory, automatic: automatic}
	namespace, err := jobs.NamespaceOf("jobsfx", "prepared")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:prepared")
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsBackend(func() *preparingBackend { return backend }),
		),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace, Workers: jobs.WorkersSpec{Build: build, Identity: testIdentityRestorer()}}),
	)
	if err = app.Err(); err != nil {
		t.Fatal(err)
	}
	startContext, cancelStart := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStart()
	if err = app.Start(startContext); err != nil {
		t.Fatal(err)
	}
	if !backend.called || !errors.Is(backend.before, jobs.ErrNotActivated) {
		t.Fatalf("called=%v enqueue before activation=%v", backend.called, backend.before)
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
}

func TestModulePrepareFailureLeavesQueueInactive(t *testing.T) {
	automatic := jobs.MustMaterialize(jobs.Declare[string](), jobs.GeneratedDefinitionSpec[string]{Name: testName(t, "jobsfx.prepare-failure"), Codec: jobs.String(1)})
	memory, err := jobsmemory.NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	prepareErr := fmt.Errorf("prepare failed")
	backend := &preparingBackend{Backend: memory, automatic: automatic, err: prepareErr}
	namespace, err := jobs.NamespaceOf("jobsfx", "prepare-failure")
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsDeclaration(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsBackend(func() *preparingBackend { return backend }),
		),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace}),
	)
	if err = app.Err(); err != nil {
		t.Fatal(err)
	}
	startContext, cancelStart := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStart()
	if err = app.Start(startContext); !errors.Is(err, prepareErr) {
		t.Fatalf("start = %v", err)
	}
	if !backend.called || !errors.Is(jobs.Go(context.Background(), automatic, "after"), jobs.ErrNotActivated) {
		t.Fatalf("called=%v queue activated after failure", backend.called)
	}
}

func TestModuleContainsPreparePanic(t *testing.T) {
	automatic := jobs.MustMaterialize(jobs.Declare[string](), jobs.GeneratedDefinitionSpec[string]{Name: testName(t, "jobsfx.prepare-panic"), Codec: jobs.String(1)})
	memory, err := jobsmemory.NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	backend := &preparingBackend{Backend: memory, automatic: automatic, panics: true}
	namespace, err := jobs.NamespaceOf("jobsfx", "prepare-panic")
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsDeclaration(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsBackend(func() *preparingBackend { return backend }),
		),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace}),
	)
	if err = app.Err(); err != nil {
		t.Fatal(err)
	}
	startContext, cancelStart := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStart()
	if err = app.Start(startContext); !errors.Is(err, jobs.ErrDriver) {
		t.Fatalf("start = %v", err)
	}
	if !errors.Is(jobs.Go(context.Background(), automatic, "after"), jobs.ErrNotActivated) {
		t.Fatal("queue activated after prepare panic")
	}
}

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

func TestModuleUsesTheGeneratedCatalogWithoutRebuildingIt(t *testing.T) {
	handled := make(chan string, 1)
	automatic := testAutomatic(t, "jobsfx.generated", func(_ context.Context, value string) error {
		handled <- value
		return nil
	})
	extra := testDefinition(t, "jobsfx.generated-producer")
	generated := jobs.MustCatalog(automatic, extra)
	namespace, err := jobs.NamespaceOf("jobsfx", "generated")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:generated")
	if err != nil {
		t.Fatal(err)
	}
	var catalog jobs.Catalog
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{
			Namespace: namespace,
			Catalog:   generated,
			Workers: jobs.WorkersSpec{
				Build:        build,
				Identity:     testIdentityRestorer(),
				PollInterval: jobs.MinimumPollInterval,
			},
		}),
		fx.Populate(&catalog),
	)
	if err = app.Err(); err != nil {
		t.Fatal(err)
	}
	if catalog.Fingerprint() != generated.Fingerprint() {
		t.Fatalf("catalog fingerprint = %q", catalog.Fingerprint())
	}
	member, ok := catalog.Lookup(extra.Name())
	if !ok || member != extra {
		t.Fatalf("generated producer declaration = %v, %v", member, ok)
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
	if err = jobs.Go(context.Background(), automatic, "generated-payload"); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-handled:
		if value != "generated-payload" {
			t.Fatalf("handled %q", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not handle the generated declaration")
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	started = false
}

func TestModuleRunsAProducerOnlyGeneratedCatalogWithoutWorkers(t *testing.T) {
	name, err := jobs.ParseName("jobsfx.producer-only-generated")
	if err != nil {
		t.Fatal(err)
	}
	producer := jobs.MustMaterialize(jobs.Declare[string](), jobs.GeneratedDefinitionSpec[string]{
		Name:  name,
		Codec: jobs.String(1),
	})
	generated := jobs.MustCatalog(producer)
	namespace, err := jobs.NamespaceOf("jobsfx", "producer-only")
	if err != nil {
		t.Fatal(err)
	}
	var queue *jobs.Queue
	var workers *jobs.Workers
	app := fx.New(
		fx.NopLogger,
		fx.Provide(jobsfx.AsBackend(jobsmemory.NewDefault)),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace, Catalog: generated}),
		fx.Populate(&queue, &workers),
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
	if queue == nil || workers != nil {
		t.Fatalf("queue=%v workers=%v", queue, workers)
	}
	if err = jobs.Go(context.Background(), producer, "payload"); err != nil {
		t.Fatal(err)
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	started = false
}

func TestModuleRejectsContributionsOutsideTheGeneratedCatalog(t *testing.T) {
	registered := testAutomatic(t, "jobsfx.registered", func(context.Context, string) error { return nil })
	generated := jobs.MustCatalog(testDefinition(t, "jobsfx.generated-only"))
	namespace, err := jobs.NamespaceOf("jobsfx", "catalog-membership")
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return registered }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace, Catalog: generated}),
	)
	if err = app.Err(); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("application error = %v", err)
	}
}

func TestModuleStartsAndStopsTheScheduler(t *testing.T) {
	handled := make(chan string, 1)
	automatic := testAutomatic(t, "jobsfx.scheduled", func(_ context.Context, value string) error {
		handled <- value
		return nil
	})
	scheduleName, err := jobs.ParseName("jobsfx.every-start")
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Add(100 * time.Millisecond)
	schedule, err := jobs.DefineSchedule(jobs.ScheduleSpec[string]{
		Name:     scheduleName,
		Revision: 1,
		Cadence:  jobs.At(due),
		Job:      automatic,
		Payload: func(at time.Time) (string, error) {
			return at.Format(time.RFC3339Nano), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := jobs.NamespaceOf("jobsfx", "scheduler")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:scheduler")
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsSchedule(func() jobs.Schedule { return schedule }),
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
	select {
	case value := <-handled:
		if value != due.Format(time.RFC3339Nano) {
			t.Fatalf("scheduled payload = %q", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not enqueue the occurrence")
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	started = false
}

func TestSchedulerFailureRequestsApplicationShutdown(t *testing.T) {
	automatic := testAutomatic(t, "jobsfx.scheduler-failure", func(context.Context, string) error { return nil })
	scheduleName, err := jobs.ParseName("jobsfx.invalid-clock")
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
	namespace, err := jobs.NamespaceOf("jobsfx", "scheduler-failure")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:scheduler-failure")
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsSchedule(func() jobs.Schedule { return schedule }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{
			Namespace: namespace,
			Workers: jobs.WorkersSpec{
				Build:        build,
				Identity:     testIdentityRestorer(),
				PollInterval: jobs.MinimumPollInterval,
			},
			Scheduler: jobs.SchedulerSpec{Clock: invalidClock{}},
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
		t.Fatal("scheduler failure did not request shutdown")
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = app.Stop(stopContext); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("stop = %v", err)
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

type invalidClock struct{}

func (invalidClock) Now() time.Time { return time.Time{} }

func (invalidClock) NewTimerAt(time.Time) jobs.Timer { return nil }

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
	name := testName(t, raw)
	return jobs.MustMaterialize(jobs.Auto(handler), jobs.GeneratedDefinitionSpec[string]{Name: name, Codec: jobs.String(1)})
}

func testName(t *testing.T, raw string) jobs.Name {
	t.Helper()
	name, err := jobs.ParseName(raw)
	if err != nil {
		t.Fatal(err)
	}
	return name
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
