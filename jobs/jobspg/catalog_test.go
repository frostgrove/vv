package jobspg

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/jobs"
)

func TestCatalogBindingsAllowAdditionsAndSeparatePolicyFromWireCompatibility(t *testing.T) {
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
	if upgrade, err := changedBindings[0].compatibleWith(storedBinding(baseBindings[0])); err != nil || upgrade {
		t.Fatalf("policy-only change compatibility = (%v, %v)", upgrade, err)
	}
}

func TestCatalogBindingEvolutionIsMonotonicAndRollingCompatible(t *testing.T) {
	name, err := jobs.ParseName("jobspg.versioned")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	v1 := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(1), Policy: policy, Partition: jobs.PartitionGlobal})
	v2 := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(2), Upcasters: []jobs.Upcaster{
		jobs.Upcast(jobs.String(1), jobs.String(2), func(value string) (string, error) { return value, nil }),
	}, Policy: policy, Partition: jobs.PartitionGlobal})
	v3WithoutV1 := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(3), Upcasters: []jobs.Upcaster{
		jobs.Upcast(jobs.String(2), jobs.String(3), func(value string) (string, error) { return value, nil }),
	}, Policy: policy, Partition: jobs.PartitionGlobal})
	v1Binding := onlyCatalogBinding(t, v1)
	v2Binding := onlyCatalogBinding(t, v2)
	v3Binding := onlyCatalogBinding(t, v3WithoutV1)
	if upgrade, err := v2Binding.compatibleWith(storedBinding(v1Binding)); err != nil || !upgrade {
		t.Fatalf("v1 -> v2 compatibility = (%v, %v)", upgrade, err)
	}
	if upgrade, err := v1Binding.compatibleWith(storedBinding(v2Binding)); err != nil || upgrade {
		t.Fatalf("rolling v1 compatibility = (%v, %v)", upgrade, err)
	}
	if _, err := v3Binding.compatibleWith(storedBinding(v2Binding)); !errors.Is(err, ErrCatalogMismatch) {
		t.Fatalf("history-dropping v3 compatibility = %v", err)
	}
	tenant := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(2), Upcasters: []jobs.Upcaster{
		jobs.Upcast(jobs.String(1), jobs.String(2), func(value string) (string, error) { return value, nil }),
	}, Policy: policy, Partition: jobs.PartitionTenantRequired})
	if _, err := onlyCatalogBinding(t, tenant).compatibleWith(storedBinding(v2Binding)); !errors.Is(err, ErrCatalogMismatch) {
		t.Fatalf("partition-changing compatibility = %v", err)
	}
}

func TestCatalogBindingsRejectCodecIdentityChangesWithinVersionHistory(t *testing.T) {
	name, err := jobs.ParseName("jobspg.codec-change")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	definition := jobs.MustDefine(jobs.DefinitionSpec[[]byte]{Name: name, Codec: jobs.Bytes(2), Upcasters: []jobs.Upcaster{
		jobs.Upcast(jobs.String(1), jobs.Bytes(2), func(value string) ([]byte, error) { return []byte(value), nil }),
	}, Policy: policy, Partition: jobs.PartitionGlobal})
	if _, err := catalogBindings(jobs.MustCatalog(definition)); !errors.Is(err, ErrCatalogMismatch) {
		t.Fatalf("codec-changing history = %v", err)
	}
}

func TestMigrationIncludesCatalogDefinitionBindingsAndV1Upgrade(t *testing.T) {
	statements, err := MigrationStatements("jobspg_catalog_test")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(statements, "\n")
	for _, required := range []string{`"jobspg_catalog_test".catalog_definitions`, `codec_revisions`, `catalog_definitions_contract_check`, `VALUES (true, 2)`, `SET version = 2`, `version = 1`, `SET version = 4`, `version IN (1, 2, 3)`, `deliveries_retention_idx`} {
		if !strings.Contains(joined, required) {
			t.Fatalf("migration is missing %q", required)
		}
	}
}

func onlyCatalogBinding(t *testing.T, declaration jobs.Declaration) catalogBinding {
	t.Helper()
	bindings, err := catalogBindings(jobs.MustCatalog(declaration))
	if err != nil {
		t.Fatal(err)
	}
	return bindings[0]
}

func storedBinding(binding catalogBinding) storedCatalogBinding {
	return storedCatalogBinding{
		name:              binding.name,
		fingerprint:       binding.fingerprint,
		codec:             sql.NullString{String: binding.codec, Valid: true},
		codecMode:         sql.NullString{String: binding.codecMode, Valid: true},
		codecVersion:      sql.NullInt64{Int64: int64(binding.codecVersion), Valid: true},
		codecRevisions:    sql.NullString{String: encodeRevisions(binding.codecRevisions), Valid: true},
		partition:         sql.NullInt64{Int64: int64(binding.partition), Valid: true},
		identity:          sql.NullString{String: binding.identity, Valid: binding.identityAvailable},
		identityVersion:   sql.NullInt64{Int64: int64(binding.identityVersion), Valid: binding.identityAvailable},
		identityAutomatic: sql.NullBool{Bool: binding.identityAutomatic, Valid: true},
	}
}
