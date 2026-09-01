package jobspg

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/jobs"
)

func TestSchemaHardeningMigrationPinsCoreContracts(t *testing.T) {
	repo := newRepository("jobspg_schema_contract")
	statements := repo.schemaHardeningMigrationStatements()
	joined := strings.Join(statements, "\n")
	required := []string{
		`catalogs_core_contract_check`,
		`catalog_definitions_core_contract_check`,
		`deliveries_core_contract_check`,
		`(octet_length(application) <= ` + fmt.Sprint(jobs.MaxNameBytes) + `)`,
		`(octet_length(environment) <= ` + fmt.Sprint(jobs.MaxNameBytes) + `)`,
		`(octet_length(definition) <= ` + fmt.Sprint(jobs.MaxNameBytes) + `)`,
		`(octet_length(codec) <= ` + fmt.Sprint(jobs.MaxCodecIDBytes) + `)`,
		`(octet_length(payload_identity) <= ` + fmt.Sprint(jobs.MaxCodecIDBytes) + `)`,
		`(octet_length(excluded_binding) <= ` + fmt.Sprint(jobs.MaxBindingNameBytes) + `)`,
		`(octet_length(excluded_build) <= ` + fmt.Sprint(jobs.MaxBuildIDBytes) + `)`,
		`(priority <= ` + fmt.Sprint(jobs.MaximumPriority) + `)`,
		`(state >= ` + fmt.Sprint(int(jobs.InvocationQueued)) + `)`,
		`(state <= ` + fmt.Sprint(int(jobs.InvocationTerminated)) + `)`,
		`(record_size <= ` + fmt.Sprint(jobs.MaxDeliveryRecordBytes) + `)`,
		`(octet_length(record) <= ` + fmt.Sprint(maxEncodedDeliveryRecordBytes) + `)`,
		`fingerprint ~ '^sha256:[0-9a-f]{64}$'`,
		`codec_mode = ANY (ARRAY['safe'::text, 'trusted'::text, 'custom'::text])`,
		`'` + fmt.Sprint(uint64(math.MaxUint32)) + `'::bigint >= ALL`,
		`NOT VALID`,
		`VALIDATE CONSTRAINT`,
	}
	for _, fragment := range required {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("schema hardening migration is missing %q", fragment)
		}
	}
	if strings.Contains(joined, `octet_length(record) = record_size`) {
		t.Fatal("encoded bytes replaced logical delivery accounting")
	}
	manual, err := MigrationStatements(repo.rawSchema)
	if err != nil {
		t.Fatal(err)
	}
	manualJoined := strings.Join(manual, "\n")
	versionAt := strings.LastIndex(manualJoined, `SET version = 4`)
	for _, fragment := range []string{`pg_get_expr(schema_constraint.conbin, schema_constraint.conrelid, false)`, `NOT schema_constraint.connoinherit`} {
		if !strings.Contains(manualJoined, fragment) {
			t.Fatalf("manual schema validation is missing %q", fragment)
		}
	}
	for _, spec := range repo.schemaConstraints() {
		addAt := strings.Index(manualJoined, `ADD CONSTRAINT `+quoteIdentifier(spec.name))
		validateAt := strings.Index(manualJoined, `VALIDATE CONSTRAINT `+quoteIdentifier(spec.name))
		exactAt := strings.Index(manualJoined, `schema constraint `+spec.name+` definition mismatch`)
		if addAt < 0 || validateAt <= addAt || exactAt <= validateAt || versionAt <= exactAt {
			t.Fatalf("constraint %q phases are unordered: add=%d validate=%d exact=%d version=%d", spec.name, addAt, validateAt, exactAt, versionAt)
		}
	}
}

func TestSchemaConstraintMatchRejectsSameNameValidatedCheckTrue(t *testing.T) {
	repo := newRepository("jobspg_schema_contract")
	for _, spec := range repo.schemaConstraints() {
		if !schemaConstraintMatches(true, false, spec.definition, spec.definition) {
			t.Fatalf("exact constraint %q did not match", spec.name)
		}
		if schemaConstraintMatches(true, false, "true", spec.definition) {
			t.Fatalf("constraint %q accepted CHECK (true)", spec.name)
		}
		if schemaConstraintMatches(false, false, spec.definition, spec.definition) {
			t.Fatalf("constraint %q accepted unvalidated definition", spec.name)
		}
		if schemaConstraintMatches(true, true, spec.definition, spec.definition) {
			t.Fatalf("constraint %q accepted NO INHERIT", spec.name)
		}
	}
}

func TestOperationalIndexesHaveExactFailClosedContracts(t *testing.T) {
	want := []operationalIndex{
		{name: "deliveries_ready_idx", table: "deliveries", columns: []string{"namespace", "definition", "priority", "available_at", "id"}, predicate: "state = 1 AND lease_token IS NULL", predicateDefinition: "((state = 1) AND (lease_token IS NULL))"},
		{name: "deliveries_expired_idx", table: "deliveries", columns: []string{"namespace", "lease_expires_at", "id"}, predicate: "lease_token IS NOT NULL", predicateDefinition: "(lease_token IS NOT NULL)"},
		{name: "intents_invocation_idx", table: "intents", columns: []string{"namespace", "invocation_id"}},
	}
	if !slices.EqualFunc(operationalIndexes, want, func(left, right operationalIndex) bool {
		return left.name == right.name && left.table == right.table && slices.Equal(left.columns, right.columns) && left.predicate == right.predicate && left.predicateDefinition == right.predicateDefinition
	}) {
		t.Fatalf("operational index contracts = %#v", operationalIndexes)
	}
	repo := newRepository("jobspg_index_contract")
	statements, err := MigrationStatements(repo.rawSchema)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(statements, "\n")
	versionAt := strings.LastIndex(joined, `SET version = 4`)
	for _, spec := range operationalIndexes {
		create := `CREATE INDEX IF NOT EXISTS ` + quoteIdentifier(spec.name)
		validation := `operational index ` + spec.name + ` schema mismatch`
		createAt := strings.Index(joined, create)
		validationAt := strings.Index(joined, validation)
		if createAt < 0 || validationAt <= createAt || versionAt <= validationAt {
			t.Fatalf("operational index %q phases are unordered: create=%d validation=%d version=%d", spec.name, createAt, validationAt, versionAt)
		}
		definition := repo.operationalIndexMatchDefinition(spec)
		for _, fragment := range []string{`relation.relkind = 'i'`, `access_method.amname = 'btree'`, `NOT index.indisunique`, `index.indnkeyatts = ` + fmt.Sprint(len(spec.columns)), `index.indnatts = ` + fmt.Sprint(len(spec.columns)), `index.indexprs IS NULL`} {
			if !strings.Contains(definition, fragment) {
				t.Fatalf("operational index %q validation is missing %q", spec.name, fragment)
			}
		}
	}
	for _, fragment := range []string{`index.indisvalid`, `index.indisready`} {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("operational validation is missing %q", fragment)
		}
	}
}
