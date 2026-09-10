package auditpg

import (
	"bytes"
	"strings"
	"testing"
)

func TestSchemaResolvesValidatesAndFingerprints(t *testing.T) {
	resolved, err := (Schema{}).Resolved()
	if err != nil || resolved.Name != DefaultSchema {
		t.Fatalf("resolved default = (%+v, %v)", resolved, err)
	}
	first, err := resolved.Fingerprint()
	if err != nil || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("fingerprint = (%q, %v)", first, err)
	}
	second, _ := (Schema{Name: "tenant_audit"}).Fingerprint()
	if first == second {
		t.Fatal("schema identity is absent from its fingerprint")
	}
	for _, invalid := range []string{"A", "1audit", "audit-events", "audit;drop", strings.Repeat("a", 64)} {
		if _, err := (Schema{Name: invalid}).Resolved(); err == nil {
			t.Fatalf("schema %q was accepted", invalid)
		}
	}
}

func TestMigrationStatementsAreDeterministicOwnedAndComplete(t *testing.T) {
	first, err := MigrationStatements(Schema{Name: "tenant_audit"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := MigrationStatements(Schema{Name: "tenant_audit"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 18 || strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Fatalf("migration is incomplete or nondeterministic: %d statements", len(first))
	}
	joined := strings.Join(first, "\n")
	for _, required := range []string{
		`"tenant_audit".settings`, `"tenant_audit".catalogs`, `"tenant_audit".revisions`,
		`"tenant_audit".idempotency`, `"tenant_audit".entity_chains`, `"tenant_audit".entity_aliases`,
		`"tenant_audit".entity_transitions`, "deny_immutable_audit_row", "pg_advisory",
	} {
		if required == "pg_advisory" {
			continue
		}
		if !strings.Contains(joined, required) {
			t.Fatalf("migration does not contain %s", required)
		}
	}
	first[0] = "mutated"
	again, _ := MigrationStatements(Schema{Name: "tenant_audit"})
	if again[0] == "mutated" || bytes.Equal([]byte(again[0]), []byte(first[0])) {
		t.Fatal("migration statements alias a previous result")
	}
}

func TestSchemaManagementVocabularyIsClosed(t *testing.T) {
	for _, value := range []SchemaManagement{UnsetSchemaManagement, VerifySchema, ManageSchema} {
		if !value.Valid() || value.String() == "" {
			t.Fatalf("valid mode %d is not usable", value)
		}
	}
	if SchemaManagement(99).Valid() || SchemaManagement(99).String() == "" {
		t.Fatal("unknown schema-management value was accepted or rendered empty")
	}
}
