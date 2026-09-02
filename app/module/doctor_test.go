package module_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/app/module"
)

func recording(label string, into *[]string) any {
	return func() string {
		*into = append(*into, label)
		return label
	}
}

func TestADeploymentIsDescribedWithoutBuildingIt(t *testing.T) {
	var built []string
	catalog := module.MustCatalog(
		module.New("workspace").
			Provide(recording("repository", &built)).
			Routes(recording("documents", &built)).
			Workers(recording("sweeper", &built)).
			MustBuild(),
	)

	diagnosis := module.Doctor(catalog, module.Serving)
	_ = diagnosis.String()

	if len(built) != 0 {
		t.Fatalf("describing the deployment ran %v; a doctor that constructs is a doctor nobody can run against production",
			built)
	}
	if !diagnosis.OK() {
		t.Fatalf("a well-formed catalog was refused: %v", diagnosis.Problems)
	}
}

func TestADescriptorSaysWhichContributionsTheProfileLeavesOut(t *testing.T) {
	descriptor := workspace().Describe(module.Serving)

	byKind := make(map[module.Kind]bool, len(descriptor.Kinds))
	for _, kind := range descriptor.Kinds {
		byKind[kind.Kind] = kind.Active
	}
	if !byKind[module.RouteKind] {
		t.Fatal("the serving profile does not activate the routes, which is the one thing it exists to do")
	}
	if byKind[module.WorkerKind] || byKind[module.SeederKind] {
		t.Fatalf("the serving profile activates %v; an API replica that also sweeps is a second, undeclared deployment", descriptor.Kinds)
	}
	if descriptor.Active != 4 {
		t.Fatalf("the profile activates %d of the module's contributions, want 4", descriptor.Active)
	}
}

func TestAProfileThatActivatesNothingIsRefusedRatherThanStartingAnEmptyProcess(t *testing.T) {
	catalog := module.MustCatalog(
		module.New("workspace").Routes(labelled("documents")).MustBuild(),
	)

	err := catalog.Check(module.Working)

	if !errors.Is(err, module.ErrCatalog) {
		t.Fatalf("a worker deployment over a catalog with no worker was accepted (%v); it starts, reports healthy and does nothing", err)
	}
}

func TestARoleNoModuleContributesToIsANoticeRatherThanARefusal(t *testing.T) {
	catalog := module.MustCatalog(
		module.New("workspace").Routes(labelled("documents")).MustBuild(),
	)

	diagnosis := module.Doctor(catalog, module.Complete)

	if !diagnosis.OK() {
		t.Fatalf("a monolith with no worker was refused (%v); running every role over a catalog that has no worker is ordinary",
			diagnosis.Problems)
	}
	if len(diagnosis.Notices) == 0 || !strings.Contains(strings.Join(diagnosis.Notices, "\n"), string(module.Worker)) {
		t.Fatalf("nothing said the worker role is answered by no module: %v", diagnosis.Notices)
	}
}

func TestTheDoctorPrintsEveryModuleAndWhatTheProfileDoesWithIt(t *testing.T) {
	catalog := module.MustCatalog(
		workspace(),
		module.Auto("access", labelled("guard")),
	)

	printed := module.Doctor(catalog, module.Seeding).String()

	for _, want := range []string{"seeding", "workspace", "access", "route", "inactive", "seeder", "active"} {
		if !strings.Contains(printed, want) {
			t.Fatalf("the doctor never printed %q:\n%s", want, printed)
		}
	}
}
