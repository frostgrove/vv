package jobsfx_test

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsfx"
)

type registryHandler struct{}

func (*registryHandler) Handle(context.Context, string) error { return nil }

type registryContributions struct {
	fx.In

	Declarations []jobs.Declaration `group:"vv.jobs.declarations"`
	Consumers    []jobs.Consumer    `group:"vv.jobs.consumers"`
}

func TestRegistryBuildsCatalogAndFxModuleFromOneApplicationList(t *testing.T) {
	binding := jobsfx.AutoFor[*registryHandler, string]().TrustedJSON("jobsfx.registry", 3)
	registrations := []jobsfx.Registration{binding}
	registry, err := jobsfx.NewRegistry(registrations...)
	if err != nil {
		t.Fatal(err)
	}
	registrations[0] = nil
	catalog := registry.Catalog()
	if catalog.Len() != 1 || catalog.Describe().Definitions[0].Name != binding.Name() || catalog.Describe().Definitions[0].Codec.CurrentVersion != 3 {
		t.Fatalf("catalog = %#v", catalog.Describe())
	}
	var contributions registryContributions
	app := fx.New(
		fx.NopLogger,
		fx.Supply(&registryHandler{}),
		registry.Module(),
		fx.Invoke(func(values registryContributions) { contributions = values }),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	if len(contributions.Declarations) != 1 || len(contributions.Consumers) != 1 {
		t.Fatalf("declarations=%d consumers=%d", len(contributions.Declarations), len(contributions.Consumers))
	}
}

func TestBindingWireSupportsSafeJSONAndCustomContracts(t *testing.T) {
	safe := jobsfx.AutoFor[*registryHandler, string]().JSON("jobsfx.safe-json", 2)
	if safe.Describe().Codec.CurrentVersion != 2 || safe.Describe().Codec.Mode != jobs.SafeCodecMode {
		t.Fatalf("safe descriptor = %#v", safe.Describe())
	}
	name, err := jobs.ParseName("jobsfx.custom")
	if err != nil {
		t.Fatal(err)
	}
	custom := jobsfx.AutoFor[*registryHandler, string]().Wire(jobs.WireSpec[string]{
		Name:      name,
		Codec:     jobs.String(4),
		Partition: jobs.PartitionTenantRequired,
	})
	if custom.Name() != name || custom.Partition() != jobs.PartitionTenantRequired {
		t.Fatalf("custom descriptor = %#v", custom.Describe())
	}
}

func TestRegistryRejectsMissingUnresolvedAndDuplicateJobs(t *testing.T) {
	if _, err := jobsfx.NewRegistry(); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("empty registry = %v", err)
	}
	unresolved := jobsfx.AutoFor[*registryHandler, string]()
	if _, err := jobsfx.NewRegistry(unresolved); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("unresolved registry = %v", err)
	}
	wired := unresolved.TrustedJSON("jobsfx.duplicate", 1)
	if _, err := jobsfx.NewRegistry(wired, wired); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("duplicate registry = %v", err)
	}
}

func TestBindingWireFailsAtDeclarationTime(t *testing.T) {
	assertPanicIs(t, jobs.ErrInvalid, func() {
		jobsfx.AutoFor[*registryHandler, string]().TrustedJSON("INVALID NAME", 1)
	})
	assertPanicIs(t, jobs.ErrInvalid, func() {
		jobsfx.AutoFor[*registryHandler, string]().TrustedJSON("jobsfx.zero-version", 0)
	})
}

func assertPanicIs(t *testing.T, target error, operation func()) {
	t.Helper()
	defer func() {
		failure, ok := recover().(error)
		if !ok || !errors.Is(failure, target) {
			t.Fatalf("panic = %#v", failure)
		}
	}()
	operation()
}
