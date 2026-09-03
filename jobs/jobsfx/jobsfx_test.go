package jobsfx_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsfx"
	"github.com/frostgrove/vv/jobs/jobsmemory"
	vvruntime "github.com/frostgrove/vv/runtime"
	"github.com/frostgrove/vv/runtime/runtimefx"
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

type catalogHandler struct{ catalog jobs.Catalog }

func (*catalogHandler) Handle(context.Context, string) error { return nil }

type admissionProviderJob struct {
	reader jobs.AdmissionReader
}

func (*admissionProviderJob) Handle(context.Context, string) error { return nil }

func (job *admissionProviderJob) JobAdmission() jobs.AdmissionReader { return job.reader }

type directAdmissionJob struct {
	handled  chan struct{}
	callback func()
	calls    atomic.Int32
}

func (job *directAdmissionJob) Handle(context.Context, string) error {
	job.handled <- struct{}{}
	return nil
}

func (job *directAdmissionJob) Admit(free int) int {
	job.calls.Add(1)
	job.callback()
	return free
}

type conflictingAdmissionJob struct {
	reader jobs.AdmissionReader
}

func (*conflictingAdmissionJob) Handle(context.Context, string) error { return nil }

func (job *conflictingAdmissionJob) JobAdmission() jobs.AdmissionReader { return job.reader }

func (job *conflictingAdmissionJob) JobOptions(jobs.Name) []jobs.WorkerOption {
	return []jobs.WorkerOption{jobs.WithAdmission(job.reader)}
}

func (backend *preparingBackend) Prepare(ctx context.Context) error {
	if backend.panics {
		panic("prepare")
	}
	backend.called = true
	backend.before = jobs.Go(ctx, backend.automatic, "before")
	return backend.err
}

func TestBundleInjectsTypedHandlersAndAppliesConcurrency(t *testing.T) {
	standard := jobsfx.Auto((*injectedJob).Handle)
	adapter := jobsfx.AutoAdapter((*injectedJob).HandleAdapter, jobs.Heavy)
	jobs.MustWire(standard.Automatic, jobs.WireSpec[string]{Name: testName(t, "jobsfx.injected"), Codec: jobs.String(1)})
	jobs.MustWire(adapter.Automatic, jobs.WireSpec[string]{Name: testName(t, "jobsfx.injected-adapter"), Codec: jobs.String(1)})
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
			Consuming: jobsfx.Enabled,
			Workers: jobs.WorkersSpec{
				Build:        build,
				Identity:     testIdentityRestorer(),
				PollInterval: jobs.MinimumPollInterval,
			},
		}),
		runtimefx.Auto(),
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

func TestConfiguredCatalogDoesNotDependOnHandlerConstruction(t *testing.T) {
	binding := jobsfx.AutoFor[*catalogHandler, string]()
	jobs.MustWire(binding.Automatic, jobs.WireSpec[string]{
		Name: testName(t, "jobsfx.catalog-handler"), Codec: jobs.String(1),
	})
	catalog := jobs.MustCatalog(binding)
	namespace, err := jobs.NamespaceOf("jobsfx", "catalog-handler")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:catalog-handler")
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		jobsfx.Bundle(catalog, nil, binding),
		fx.Provide(
			func(runtimeCatalog jobs.Catalog) *catalogHandler {
				return &catalogHandler{catalog: runtimeCatalog}
			},
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{
			Namespace: namespace,
			Consuming: jobsfx.Enabled,
			Workers:   jobs.WorkersSpec{Build: build, Identity: testIdentityRestorer()},
		}),
		runtimefx.Auto(),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredBindingsDiscoverProvidedAndManualAdmissionSnapshots(t *testing.T) {
	provided := jobsfx.AutoFor[*admissionProviderJob, string]()
	manual := testAutomatic(t, "jobsfx.snapshot-manual", func(context.Context, string) error { return nil })
	jobs.MustWire(provided.Automatic, jobs.WireSpec[string]{Name: testName(t, "jobsfx.snapshot-provided"), Codec: jobs.String(1)})
	catalog := jobs.MustCatalog(provided, manual)
	namespace, err := jobs.NamespaceOf("jobsfx", "snapshot-admission")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:snapshot-admission")
	if err != nil {
		t.Fatal(err)
	}
	providedSnapshot, err := jobs.NewAdmissionSnapshot(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	manualSnapshot, err := jobs.NewAdmissionSnapshot(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	manualConsumer := jobs.On(manual, func(context.Context, string) error { return nil }, jobs.WithAdmission(manualSnapshot.Reader()))
	var workers *jobs.Workers
	app := fx.New(
		fx.NopLogger,
		fx.Supply(&admissionProviderJob{reader: providedSnapshot.Reader()}),
		jobsfx.Bundle(catalog, nil, provided),
		fx.Provide(
			jobsfx.AsConsumer(func() jobs.Consumer { return manualConsumer }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace, Consuming: jobsfx.Enabled, Workers: jobs.WorkersSpec{Build: build, Identity: testIdentityRestorer()}}),
		runtimefx.Auto(),
		fx.Populate(&workers),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	description := workers.Describe().Plan
	if len(description.Bindings) != 2 {
		t.Fatalf("snapshot admission bindings = %#v", description)
	}
	for _, binding := range description.Bindings {
		if !binding.DynamicAdmission {
			t.Fatalf("snapshot admission was not discovered: %#v", binding)
		}
	}
}

func TestConfiguredBindingsNeverInvokeLegacyAdmissionCallbacks(t *testing.T) {
	tests := []struct {
		name     string
		callback func()
	}{
		{name: "panic", callback: func() { panic("legacy admission callback") }},
		{name: "goexit", callback: runtime.Goexit},
		{name: "block", callback: func() { <-make(chan struct{}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := jobsfx.AutoFor[*directAdmissionJob, string]()
			jobs.MustWire(binding.Automatic, jobs.WireSpec[string]{Name: testName(t, "jobsfx.legacy-"+test.name), Codec: jobs.String(1)})
			namespace, err := jobs.NamespaceOf("jobsfx", "legacy-"+test.name)
			if err != nil {
				t.Fatal(err)
			}
			build, err := jobs.ParseBuildID("jobsfx:legacy-" + test.name)
			if err != nil {
				t.Fatal(err)
			}
			handler := &directAdmissionJob{handled: make(chan struct{}, 1), callback: test.callback}
			var workers *jobs.Workers
			app := fx.New(
				fx.NopLogger,
				fx.Supply(handler),
				jobsfx.Bundle(jobs.MustCatalog(binding), nil, binding),
				fx.Provide(jobsfx.AsBackend(jobsmemory.NewDefault)),
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
				fx.Populate(&workers),
			)
			if err = app.Err(); err != nil {
				t.Fatal(err)
			}
			if description := workers.Describe().Plan.Bindings; len(description) != 1 || description[0].DynamicAdmission {
				t.Fatalf("legacy callback changed worker admission = %#v", description)
			}
			startContext, cancelStart := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancelStart()
			if err = app.Start(startContext); err != nil {
				t.Fatal(err)
			}
			if err = binding.Go(context.Background(), "payload"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-handler.handled:
			case <-time.After(3 * time.Second):
				t.Fatal("handler did not run")
			}
			stopContext, cancelStop := context.WithTimeout(context.Background(), time.Second)
			defer cancelStop()
			if err = app.Stop(stopContext); err != nil {
				t.Fatal(err)
			}
			if calls := handler.calls.Load(); calls != 0 {
				t.Fatalf("legacy admission callback calls = %d", calls)
			}
		})
	}
}

func TestConfiguredBindingRejectsDuplicateSnapshotAdmission(t *testing.T) {
	binding := jobsfx.AutoFor[*conflictingAdmissionJob, string]()
	jobs.MustWire(binding.Automatic, jobs.WireSpec[string]{Name: testName(t, "jobsfx.snapshot-conflict"), Codec: jobs.String(1)})
	namespace, err := jobs.NamespaceOf("jobsfx", "snapshot-conflict")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:snapshot-conflict")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := jobs.NewAdmissionSnapshot(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Supply(&conflictingAdmissionJob{reader: snapshot.Reader()}),
		jobsfx.Bundle(jobs.MustCatalog(binding), nil, binding),
		fx.Provide(jobsfx.AsBackend(jobsmemory.NewDefault)),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace, Consuming: jobsfx.Enabled, Workers: jobs.WorkersSpec{Build: build, Identity: testIdentityRestorer()}}),
		runtimefx.Auto(),
	)
	if err := app.Err(); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("duplicate snapshot admission = %v", err)
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
		jobsfx.Module(jobsfx.Spec{Namespace: namespace, Consuming: jobsfx.Enabled, Workers: jobs.WorkersSpec{Build: build, Identity: testIdentityRestorer()}}),
		runtimefx.Auto(),
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
			jobsfx.AsDeclaration(func() jobs.Declaration { return automatic }),
			jobsfx.AsBackend(func() *preparingBackend { return backend }),
		),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace, Consuming: jobsfx.Enabled, Workers: jobs.WorkersSpec{Build: build, Identity: testIdentityRestorer()}}),
		runtimefx.Auto(),
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
	automatic := jobs.MustWire(jobs.Declare[string](), jobs.WireSpec[string]{Name: testName(t, "jobsfx.prepare-failure"), Codec: jobs.String(1)})
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
	automatic := jobs.MustWire(jobs.Declare[string](), jobs.WireSpec[string]{Name: testName(t, "jobsfx.prepare-panic"), Codec: jobs.String(1)})
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
			jobsfx.AsDeclaration(func() jobs.Declaration { return automatic }),
			jobsfx.AsDeclaration(func() *jobs.Definition[string] { return extra }),
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

func TestModuleExplainsConsumerOnlyCatalogMigration(t *testing.T) {
	automatic := testAutomatic(t, "jobsfx.consumer-only", func(context.Context, string) error { return nil })
	namespace, err := jobs.NamespaceOf("jobsfx", "consumer-only")
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace}),
	)
	if err = app.Err(); !errors.Is(err, jobs.ErrInvalid) || !strings.Contains(err.Error(), "jobsfx.Registry") || !strings.Contains(err.Error(), "jobsfx.AsDeclaration") {
		t.Fatalf("consumer-only migration error = %v", err)
	}
}

func TestModuleUsesTheConfiguredCatalogWithoutRebuildingIt(t *testing.T) {
	handled := make(chan string, 1)
	automatic := testAutomatic(t, "jobsfx.configured", func(_ context.Context, value string) error {
		handled <- value
		return nil
	})
	extra := testDefinition(t, "jobsfx.configured-producer")
	configured := jobs.MustCatalog(automatic, extra)
	namespace, err := jobs.NamespaceOf("jobsfx", "configured")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:configured")
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
			Catalog:   configured,
			Consuming: jobsfx.Enabled,
			Workers: jobs.WorkersSpec{
				Build:        build,
				Identity:     testIdentityRestorer(),
				PollInterval: jobs.MinimumPollInterval,
			},
		}),
		runtimefx.Auto(),
		fx.Populate(&catalog),
	)
	if err = app.Err(); err != nil {
		t.Fatal(err)
	}
	if catalog.Fingerprint() != configured.Fingerprint() {
		t.Fatalf("catalog fingerprint = %q", catalog.Fingerprint())
	}
	member, ok := catalog.Lookup(extra.Name())
	if !ok || member != extra {
		t.Fatalf("configured producer declaration = %v, %v", member, ok)
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
	if err = jobs.Go(context.Background(), automatic, "configured-payload"); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-handled:
		if value != "configured-payload" {
			t.Fatalf("handled %q", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not handle the configured declaration")
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	started = false
}

func TestModuleRunsAProducerOnlyConfiguredCatalogWithoutWorkers(t *testing.T) {
	name, err := jobs.ParseName("jobsfx.producer-only-configured")
	if err != nil {
		t.Fatal(err)
	}
	producer := jobs.MustWire(jobs.Declare[string](), jobs.WireSpec[string]{
		Name:  name,
		Codec: jobs.String(1),
	})
	configured := jobs.MustCatalog(producer)
	namespace, err := jobs.NamespaceOf("jobsfx", "producer-only")
	if err != nil {
		t.Fatal(err)
	}
	var queue *jobs.Queue
	var workers *jobs.Workers
	app := fx.New(
		fx.NopLogger,
		fx.Provide(jobsfx.AsBackend(jobsmemory.NewDefault)),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace, Catalog: configured}),
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

func TestModuleRejectsContributionsOutsideTheConfiguredCatalog(t *testing.T) {
	registered := testAutomatic(t, "jobsfx.registered", func(context.Context, string) error { return nil })
	configured := jobs.MustCatalog(testDefinition(t, "jobsfx.configured-only"))
	namespace, err := jobs.NamespaceOf("jobsfx", "catalog-membership")
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return registered }),
			jobsfx.AsDeclaration(func() jobs.Declaration { return registered }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{Namespace: namespace, Catalog: configured}),
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
			jobsfx.AsDeclaration(func() jobs.Declaration { return automatic }),
			jobsfx.AsSchedule(func() jobs.Schedule { return schedule }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{
			Namespace:  namespace,
			Consuming:  jobsfx.Enabled,
			Scheduling: jobsfx.Enabled,
			Workers: jobs.WorkersSpec{
				Build:        build,
				Identity:     testIdentityRestorer(),
				PollInterval: jobs.MinimumPollInterval,
			},
		}),
		runtimefx.Auto(),
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

func TestASchedulerThatDiesIsNamedByTheSupervisorAndTakesTheProcessDown(t *testing.T) {
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
	var supervisor *vvruntime.Supervisor
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsDeclaration(func() jobs.Declaration { return automatic }),
			jobsfx.AsSchedule(func() jobs.Schedule { return schedule }),
			jobsfx.AsBackend(jobsmemory.NewDefault),
		),
		jobsfx.Module(jobsfx.Spec{
			Namespace:  namespace,
			Consuming:  jobsfx.Enabled,
			Scheduling: jobsfx.Enabled,
			Workers: jobs.WorkersSpec{
				Build:        build,
				Identity:     testIdentityRestorer(),
				PollInterval: jobs.MinimumPollInterval,
			},
			Scheduler: jobs.SchedulerSpec{Clock: invalidClock{}},
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
	select {
	case signal := <-app.Wait():
		if signal.ExitCode != 1 {
			t.Fatalf("shutdown exit code = %d", signal.ExitCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler failure did not request shutdown")
	}
	failure := runnerState(t, supervisor, jobsfx.SchedulerRunnerName)
	if failure.Phase != vvruntime.PhaseFailed || !errors.Is(failure.Err, jobs.ErrInvalid) {
		t.Fatalf("the scheduler died as %s with %v", failure.Phase, failure.Err)
	}
	if failure.Declaration.Placement != vvruntime.Singleton {
		t.Fatalf("the scheduler is placed as %q", failure.Declaration.Placement)
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = app.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
}

func TestAWorkerPoolThatDiesIsNamedByTheSupervisorAndTakesTheProcessDown(t *testing.T) {
	automatic := testAutomatic(t, "jobsfx.failure", func(context.Context, string) error { return nil })
	namespace, err := jobs.NamespaceOf("jobsfx", "failure")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("jobsfx:failure")
	if err != nil {
		t.Fatal(err)
	}
	var supervisor *vvruntime.Supervisor
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			jobsfx.AsConsumer(func() *jobs.Automatic[string] { return automatic }),
			jobsfx.AsDeclaration(func() jobs.Declaration { return automatic }),
			jobsfx.AsBackend(newPanickingBackend),
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
	select {
	case signal := <-app.Wait():
		if signal.ExitCode != 1 {
			t.Fatalf("shutdown exit code = %d", signal.ExitCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker failure did not request shutdown")
	}
	failure := runnerState(t, supervisor, jobsfx.WorkersRunnerName)
	if failure.Phase != vvruntime.PhaseFailed || !errors.Is(failure.Err, jobs.ErrDriver) {
		t.Fatalf("the worker pool died as %s with %v", failure.Phase, failure.Err)
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStop()
	if err = app.Stop(stopContext); !errors.Is(err, jobs.ErrDriver) {
		t.Fatalf("stop = %v", err)
	}
}

func runnerState(t *testing.T, supervisor *vvruntime.Supervisor, name string) vvruntime.RunnerState {
	t.Helper()
	for _, state := range supervisor.States() {
		if state.Name == name {
			return state
		}
	}
	t.Fatalf("the supervisor never heard of a runner called %q", name)
	return vvruntime.RunnerState{}
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
	return jobs.MustWire(jobs.Auto(handler), jobs.WireSpec[string]{Name: name, Codec: jobs.String(1)})
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
