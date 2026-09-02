package module_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/frostgrove/vv/app/module"
)

func TestTheCatalogIsOrderedByOrderAndThenByName(t *testing.T) {
	catalog := module.MustCatalog(
		module.New("workspace").Order(200).Provide(labelled("workspace")).MustBuild(),
		module.New("ops").Order(200).Provide(labelled("ops")).MustBuild(),
		module.New("access").Order(100).Provide(labelled("access")).MustBuild(),
	)

	want := []string{"access", "ops", "workspace"}
	if !slices.Equal(catalog.Names(), want) {
		t.Fatalf("the catalog reads %v, want %v; a module list whose order depends on the call site orders the graph by luck",
			catalog.Names(), want)
	}
}

func TestTwoModulesWithOneNameAreRefusedBeforeAnythingIsBuilt(t *testing.T) {
	_, err := module.NewCatalog(
		module.Auto("workspace", labelled("first")),
		module.Auto("workspace", labelled("second")),
	)

	if !errors.Is(err, module.ErrCatalog) {
		t.Fatalf("two modules named workspace were accepted (%v); the descriptor would name one and the graph would hold both", err)
	}
}

func TestACatalogWithNoModuleIsRefused(t *testing.T) {
	_, err := module.NewCatalog()

	if !errors.Is(err, module.ErrCatalog) {
		t.Fatalf("an empty catalog was accepted (%v); the process it describes starts and does nothing", err)
	}
}

func TestADefinitionThatWasNeverBuiltIsRefusedByTheCatalog(t *testing.T) {
	_, err := module.NewCatalog(module.Definition{})

	if !errors.Is(err, module.ErrCatalog) {
		t.Fatalf("a zero definition reached the catalog (%v); an ignored refusal becomes a module that contributes nothing", err)
	}
}

func TestAProfileNamingARoleThatDoesNotExistIsRefused(t *testing.T) {
	catalog := module.MustCatalog(module.Auto("access", labelled("guard")))

	err := catalog.Check(module.Profile{Name: "batch", Roles: []module.Role{"cron"}})

	if !errors.Is(err, module.ErrProfile) {
		t.Fatalf("the role %q was accepted (%v); a role nothing answers to activates nothing and says so nowhere", "cron", err)
	}
}

func TestAnUnnamedProfileIsRefused(t *testing.T) {
	catalog := module.MustCatalog(module.Auto("access", labelled("guard")))

	if err := catalog.Check(module.Profile{Roles: []module.Role{module.API}}); !errors.Is(err, module.ErrProfile) {
		t.Fatalf("a profile with no name was accepted (%v); every log line about it would name nothing", err)
	}
}
