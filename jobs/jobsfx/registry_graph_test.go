package jobsfx_test

import (
	"context"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsfx"
	"github.com/frostgrove/vv/jobs/jobsmemory"
)

type registryCatalogHandler struct{ catalog jobs.Catalog }

func (*registryCatalogHandler) Handle(context.Context, string) error { return nil }

func TestRegistryCatalogDoesNotDependOnHandlerConstruction(t *testing.T) {
	binding := jobsfx.AutoFor[*registryCatalogHandler, string]().JSON("jobsfx.catalog-handler", 1)
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
			Workers:   jobs.WorkersSpec{Build: build, Identity: testIdentityRestorer()},
		}),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
}
