package jobspg

import (
	"strings"
	"testing"

	"github.com/frostgrove/vv/jobs"
)

func TestCatalogBindingsAllowAdditionsAndDetectDefinitionChanges(t *testing.T) {
	_, base, _ := testPlacement(t)
	baseBindings, err := catalogBindings(base)
	if err != nil {
		t.Fatal(err)
	}
	addedName, err := jobs.ParseName("jobspg.added")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	added := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: addedName, Codec: jobs.String(1), Policy: policy, Partition: jobs.PartitionGlobal})
	expandedDeclarations := append(base.Definitions(), added)
	expanded := jobs.MustCatalog(expandedDeclarations...)
	expandedBindings, err := catalogBindings(expanded)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseBindings) != 1 || len(expandedBindings) != 2 {
		t.Fatalf("binding counts = %d, %d", len(baseBindings), len(expandedBindings))
	}
	expandedByName := make(map[string]string, len(expandedBindings))
	for _, binding := range expandedBindings {
		expandedByName[binding.name] = binding.fingerprint
	}
	if expandedByName[baseBindings[0].name] != baseBindings[0].fingerprint {
		t.Fatal("adding a definition changed the existing definition binding")
	}
	baseName := base.Definitions()[0].Describe().Name
	changedPolicy, err := jobs.Default.With(jobs.Retries(0)).Build()
	if err != nil {
		t.Fatal(err)
	}
	changed := jobs.MustCatalog(jobs.MustDefine(jobs.DefinitionSpec[string]{Name: baseName, Codec: jobs.String(1), Policy: changedPolicy, Partition: jobs.PartitionGlobal}))
	changedBindings, err := catalogBindings(changed)
	if err != nil {
		t.Fatal(err)
	}
	if changedBindings[0].fingerprint == baseBindings[0].fingerprint {
		t.Fatal("definition policy change kept the persisted binding")
	}
}

func TestMigrationIncludesCatalogDefinitionBindingsAndV1Upgrade(t *testing.T) {
	statements, err := MigrationStatements("jobspg_catalog_test")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(statements, "\n")
	for _, required := range []string{`"jobspg_catalog_test".catalog_definitions`, `VALUES (true, 2)`, `SET version = 2`, `version = 1`} {
		if !strings.Contains(joined, required) {
			t.Fatalf("migration is missing %q", required)
		}
	}
}
