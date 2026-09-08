package jobsfx_test

import (
	"context"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsfx"
	"github.com/frostgrove/vv/jobs/jobsmemory"
	"github.com/frostgrove/vv/runtime/runtimefx"
)

type registryCatalogHandler struct{ catalog jobs.Catalog }

func (*registryCatalogHandler) Handle(context.Context, string) error { return nil }

func TestRegistryCatalogDoesNotDependOnHandlerConstruction(t *testing.T) {
	// TrustedJSON and not JSON: what this pins is the graph, and safe JSON is
	// refused at activation under a jsonv2 runtime ([[D-085]]), which would make
	// the test fail for a reason it is not about.
	binding := jobsfx.AutoFor[*registryCatalogHandler, string]().TrustedJSON("jobsfx.catalog-handler", 1)
	registry := jobsfx.MustRegistry(binding)
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
		registry.Module(),
		fx.Provide(
			func(catalog jobs.Catalog) *registryCatalogHandler {
				return &registryCatalogHandler{catalog: catalog}
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
