package module_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/frostgrove/vv/app/module"
)

func labelled(label string) any { return func() string { return label } }

func labels(constructors []any) []string {
	out := make([]string, 0, len(constructors))
	for _, constructor := range constructors {
		out = append(out, constructor.(func() string)())
	}
	return out
}

func workspace() module.Definition {
	return module.New("workspace").
		Order(200).
		Provide(labelled("repository"), labelled("use-case")).
		Routes(labelled("documents")).
		Workers(labelled("sweeper")).
		Seeders(labelled("templates")).
		Checks(labelled("storage-reachable")).
		MustBuild()
}

func TestABuilderIsTheSpecWrittenAsACall(t *testing.T) {
	built := workspace()
	specified := module.MustDefine(module.Spec{
		Name:    "workspace",
		Order:   200,
		Provide: []any{labelled("repository"), labelled("use-case")},
		Routes:  []any{labelled("documents")},
		Workers: []any{labelled("sweeper")},
		Seeders: []any{labelled("templates")},
		Checks:  []any{labelled("storage-reachable")},
	})

	if !reflect.DeepEqual(built.Describe(module.Complete), specified.Describe(module.Complete)) {
		t.Fatalf("the builder described %+v and the spec described %+v; a builder that loses a bucket loses whatever was in it, silently",
			built.Describe(module.Complete), specified.Describe(module.Complete))
	}
	if !slices.Equal(labels(built.Active(module.Complete)), labels(specified.Active(module.Complete))) {
		t.Fatal("the builder and the spec offer different constructors for the same module")
	}
}

func TestAutoIsTheBuilderWithNothingButProviders(t *testing.T) {
	automatic := module.Auto("access", labelled("guard"))
	explicit := module.New("access").Provide(labelled("guard")).MustBuild()

	if !reflect.DeepEqual(automatic.Describe(module.Complete), explicit.Describe(module.Complete)) {
		t.Fatal("Auto is not the explicit form with defaults, so the short way and the long way are two different contracts")
	}
	if roles := automatic.Roles(); len(roles) != 0 {
		t.Fatalf("a module of plain providers claims the roles %v; a provider is what every deployment carries", roles)
	}
}

func TestAModuleThatContributesNothingIsRefused(t *testing.T) {
	_, err := module.New("workspace").Build()

	if !errors.Is(err, module.ErrDefinition) {
		t.Fatalf("an empty module was accepted (%v); it would appear in every descriptor and provide nothing", err)
	}
}

func TestEveryProblemInOneDefinitionIsNamedAtOnce(t *testing.T) {
	_, err := module.Define(module.Spec{Provide: []any{nil}})

	var refusal *module.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("the refusal does not carry its problems: %v", err)
	}
	if len(refusal.Problems()) != 2 {
		t.Fatalf("a nameless module with a nil constructor reported %v; one problem per run means one run per problem",
			refusal.Problems())
	}
}

func TestASeedProfileOffersTheGraphWithoutTheRoutesAndTheWorkers(t *testing.T) {
	offered := labels(workspace().Active(module.Seeding))

	want := []string{"repository", "use-case", "templates", "storage-reachable"}
	if !slices.Equal(offered, want) {
		t.Fatalf("the seed command was offered %v, want %v; a seed run that mounts an API listens, and one that starts a sweeper competes with the deployment it precedes",
			offered, want)
	}
}

func TestAServingProfileStartsNoWorkerAndSeedsNothing(t *testing.T) {
	offered := labels(workspace().Active(module.Serving))

	want := []string{"repository", "use-case", "documents", "storage-reachable"}
	if !slices.Equal(offered, want) {
		t.Fatalf("an API replica was offered %v, want %v", offered, want)
	}
}

func TestAModuleNamesTheRolesItContributesTo(t *testing.T) {
	roles := workspace().Roles()

	want := []module.Role{module.API, module.Seeder, module.Worker}
	if !slices.Equal(roles, want) {
		t.Fatalf("the module answers the roles %v, want %v", roles, want)
	}
}
