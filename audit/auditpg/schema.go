package auditpg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const (
	DefaultSchema = "frostgrove_audit"
	SchemaVersion = 2
	schemaModel   = "settings/catalogs/catalog_mutations/revisions/idempotency/entity_chains/entity_aliases/entity_transitions/attempt_chains/attempt_type_states/attempt_identity_aliases/attempt_idempotency/attempt_transitions:v2"
)

type Schema struct {
	Name string
}

func (s Schema) Resolved() (Schema, error) {
	if s.Name == "" {
		s.Name = DefaultSchema
	}
	if !validIdentifier(s.Name) {
		return Schema{}, fmt.Errorf("%w: schema name %q must match [a-z][a-z0-9_]{0,62}", ErrSpec, s.Name)
	}
	return s, nil
}

func (s Schema) Fingerprint() (string, error) {
	resolved, err := s.Resolved()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte("frostgrove.audit.postgres.schema/v1\x00" + resolved.Name + "\x00" + schemaModel))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		current := value[index]
		if current >= 'a' && current <= 'z' || current >= '0' && current <= '9' || current == '_' {
			continue
		}
		return false
	}
	return true
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

type pgColumnSpec struct {
	name              string
	typeName          string
	notNull           bool
	identity          string
	defaultExpression string
}

type pgConstraintSpec struct {
	kind              string
	columns           []string
	referenceTable    string
	referenceColumns  []string
	deferrable        bool
	initiallyDeferred bool
	check             string
	catalogCheck      string
}

type pgTableSpec struct {
	name        string
	columns     []pgColumnSpec
	constraints []pgConstraintSpec
	immutable   bool
}

type schemaDescriptor struct {
	kind   string
	object string
	member string
	detail string
}

const immutableFunctionSource = `BEGIN
	RAISE EXCEPTION 'auditpg: % is forbidden for immutable audit evidence', TG_OP;
END`

func postgresTables() []pgTableSpec {
	return []pgTableSpec{
		{
			name: "settings",
			columns: []pgColumnSpec{
				{name: "singleton", typeName: "boolean", notNull: true, defaultExpression: "true"},
				{name: "version", typeName: "integer", notNull: true},
				{name: "fingerprint", typeName: "text", notNull: true},
				{name: "backing_id", typeName: "bytea", notNull: true},
				{name: "log_id", typeName: "bytea", notNull: true},
				{name: "active_catalog_id", typeName: "text"},
				{name: "active_generation", typeName: "bigint"},
				{name: "active_digest", typeName: "bytea"},
				{name: "catalog_set_digest", typeName: "bytea"},
			},
			constraints: []pgConstraintSpec{
				primaryKey("singleton"),
				checkConstraint("singleton"),
				checkConstraint("version > 0"),
				checkConstraint("octet_length(backing_id) = 16"),
				checkConstraint("octet_length(log_id) = 16"),
				checkConstraint("(active_catalog_id IS NULL) = (active_generation IS NULL)"),
				checkConstraint("(active_catalog_id IS NULL) = (active_digest IS NULL)"),
				checkConstraint("active_digest IS NULL OR octet_length(active_digest) = 32"),
				checkConstraint("catalog_set_digest IS NULL OR octet_length(catalog_set_digest) = 32"),
			},
		},
		{
			name: "catalogs",
			columns: []pgColumnSpec{
				{name: "catalog_id", typeName: "text", notNull: true},
				{name: "generation", typeName: "bigint", notNull: true},
				{name: "digest", typeName: "bytea", notNull: true},
				{name: "previous_catalog_id", typeName: "text"},
				{name: "previous_generation", typeName: "bigint"},
				{name: "previous_digest", typeName: "bytea"},
				{name: "canonical", typeName: "bytea", notNull: true},
				{name: "change_ledger", typeName: "text", notNull: true},
				{name: "change_ref", typeName: "text", notNull: true},
				{name: "installed_at", typeName: "timestamp with time zone", notNull: true, defaultExpression: "clock_timestamp()"},
			},
			constraints: []pgConstraintSpec{
				primaryKey("catalog_id", "generation"),
				uniqueConstraint("catalog_id", "generation", "digest"),
				checkConstraint("generation > 0"),
				checkConstraint("octet_length(digest) = 32"),
				checkConstraint("octet_length(canonical) > 0"),
				checkConstraint("(previous_catalog_id IS NULL) = (previous_generation IS NULL)"),
				checkConstraint("(previous_catalog_id IS NULL) = (previous_digest IS NULL)"),
				checkConstraint("previous_digest IS NULL OR octet_length(previous_digest) = 32"),
			},
			immutable: true,
		},
		{
			name: "catalog_mutations",
			columns: []pgColumnSpec{
				{name: "position", typeName: "bigint", notNull: true, identity: "a"},
				{name: "kind", typeName: "smallint", notNull: true},
				{name: "catalog_id", typeName: "text", notNull: true},
				{name: "generation", typeName: "bigint", notNull: true},
				{name: "digest", typeName: "bytea", notNull: true},
				{name: "expected_catalog_id", typeName: "text"},
				{name: "expected_generation", typeName: "bigint"},
				{name: "expected_digest", typeName: "bytea"},
				{name: "change_ledger", typeName: "text", notNull: true},
				{name: "change_ref", typeName: "text", notNull: true},
				{name: "recorded_at", typeName: "timestamp with time zone", notNull: true, defaultExpression: "clock_timestamp()"},
			},
			constraints: []pgConstraintSpec{
				primaryKey("position"),
				checkConstraintAs("kind IN (1, 2)", "kind = ANY (ARRAY[1, 2])"),
				checkConstraint("generation > 0"),
				checkConstraint("octet_length(digest) = 32"),
				checkConstraint("expected_digest IS NULL OR octet_length(expected_digest) = 32"),
			},
			immutable: true,
		},
		{
			name: "revisions",
			columns: []pgColumnSpec{
				{name: "position", typeName: "bigint", notNull: true, identity: "a"},
				{name: "revision_id", typeName: "bytea", notNull: true},
				{name: "catalog_id", typeName: "text", notNull: true},
				{name: "catalog_generation", typeName: "bigint", notNull: true},
				{name: "operation", typeName: "text", notNull: true},
				{name: "semantic", typeName: "bytea", notNull: true},
				{name: "intent", typeName: "bytea", notNull: true},
				{name: "wire", typeName: "bytea", notNull: true},
				{name: "recorded_at", typeName: "timestamp with time zone", notNull: true, defaultExpression: "clock_timestamp()"},
			},
			constraints: []pgConstraintSpec{
				primaryKey("position"),
				uniqueConstraint("revision_id"),
				checkConstraint("octet_length(revision_id) = 16"),
				checkConstraint("octet_length(semantic) = 32"),
				checkConstraint("octet_length(intent) = 32"),
				checkConstraint("octet_length(wire) > 0"),
			},
			immutable: true,
		},
		{
			name: "idempotency",
			columns: []pgColumnSpec{
				{name: "catalog_id", typeName: "text", notNull: true},
				{name: "operation", typeName: "text", notNull: true},
				{name: "token", typeName: "bytea", notNull: true},
				{name: "semantic", typeName: "bytea", notNull: true},
				{name: "revision_id", typeName: "bytea", notNull: true},
			},
			constraints: []pgConstraintSpec{
				primaryKey("catalog_id", "operation", "token"),
				checkConstraint("octet_length(token) = 32"),
				checkConstraint("octet_length(semantic) = 32"),
				checkConstraint("octet_length(revision_id) = 16"),
				foreignKey([]string{"revision_id"}, "revisions", []string{"revision_id"}, true, true),
			},
			immutable: true,
		},
		{
			name: "entity_chains",
			columns: []pgColumnSpec{
				{name: "chain_id", typeName: "bytea", notNull: true},
				{name: "resource", typeName: "text", notNull: true},
				{name: "leaf", typeName: "bytea"},
				{name: "terminal", typeName: "boolean", notNull: true, defaultExpression: "false"},
			},
			constraints: []pgConstraintSpec{
				primaryKey("chain_id"),
				checkConstraint("octet_length(chain_id) = 32"),
				checkConstraint("leaf IS NULL OR octet_length(leaf) = 32"),
			},
		},
		{
			name: "entity_aliases",
			columns: []pgColumnSpec{
				{name: "resource", typeName: "text", notNull: true},
				{name: "scope_present", typeName: "boolean", notNull: true},
				{name: "scope", typeName: "bytea", notNull: true},
				{name: "algorithm", typeName: "text", notNull: true},
				{name: "profile", typeName: "text", notNull: true},
				{name: "key_id", typeName: "text", notNull: true},
				{name: "commitment", typeName: "bytea", notNull: true},
				{name: "chain_id", typeName: "bytea", notNull: true},
			},
			constraints: []pgConstraintSpec{
				primaryKey("resource", "scope_present", "scope", "algorithm", "profile", "key_id", "commitment"),
				checkConstraint("octet_length(scope) = 32"),
				checkConstraint("octet_length(commitment) = 32"),
				foreignKey([]string{"chain_id"}, "entity_chains", []string{"chain_id"}, false, false),
			},
			immutable: true,
		},
		{
			name: "entity_transitions",
			columns: []pgColumnSpec{
				{name: "revision_id", typeName: "bytea", notNull: true},
				{name: "ordinal", typeName: "integer", notNull: true},
				{name: "chain_id", typeName: "bytea", notNull: true},
				{name: "previous_leaf", typeName: "bytea"},
				{name: "leaf", typeName: "bytea", notNull: true},
				{name: "action", typeName: "text", notNull: true},
			},
			constraints: []pgConstraintSpec{
				primaryKey("revision_id", "ordinal"),
				foreignKey([]string{"revision_id"}, "revisions", []string{"revision_id"}, false, false),
				foreignKey([]string{"chain_id"}, "entity_chains", []string{"chain_id"}, false, false),
				checkConstraint("ordinal >= 0"),
				checkConstraint("octet_length(leaf) = 32"),
				checkConstraint("previous_leaf IS NULL OR octet_length(previous_leaf) = 32"),
			},
			immutable: true,
		},
		{
			name: "attempt_chains",
			columns: []pgColumnSpec{
				{name: "chain_id", typeName: "bytea", notNull: true},
				{name: "catalog_id", typeName: "text", notNull: true},
				{name: "operation", typeName: "text", notNull: true},
				{name: "operation_id", typeName: "bytea", notNull: true},
				{name: "policy", typeName: "bytea", notNull: true},
				{name: "replay", typeName: "bytea", notNull: true},
				{name: "state", typeName: "smallint", notNull: true},
				{name: "sequence", typeName: "integer", notNull: true},
				{name: "checkpoint_count", typeName: "integer", notNull: true},
				{name: "transition_bytes", typeName: "bigint", notNull: true},
				{name: "expires_at", typeName: "timestamp with time zone", notNull: true},
				{name: "state_wire", typeName: "bytea", notNull: true},
			},
			constraints: []pgConstraintSpec{
				primaryKey("chain_id"),
				uniqueConstraint("catalog_id", "operation_id"),
				checkConstraint("octet_length(chain_id) = 32"),
				checkConstraint("octet_length(operation_id) = 16"),
				checkConstraint("octet_length(policy) = 32"),
				checkConstraint("octet_length(replay) = 32"),
				checkConstraintAs("state BETWEEN 1 AND 6", "state >= 1 AND state <= 6"),
				checkConstraintAs("sequence BETWEEN 1 AND 259", "sequence >= 1 AND sequence <= 259"),
				checkConstraint("sequence > checkpoint_count AND checkpoint_count >= 0"),
				checkConstraint("transition_bytes > 0 AND transition_bytes <= 4194304"),
				checkConstraint("octet_length(state_wire) > 0"),
			},
		},
		{
			name: "attempt_type_states",
			columns: []pgColumnSpec{
				{name: "catalog_id", typeName: "text", notNull: true},
				{name: "operation", typeName: "text", notNull: true},
				{name: "policy", typeName: "bytea", notNull: true},
				{name: "replay", typeName: "bytea", notNull: true},
				{name: "unsettled", typeName: "numeric(20,0)", notNull: true},
				{name: "state_wire", typeName: "bytea", notNull: true},
			},
			constraints: []pgConstraintSpec{
				primaryKey("catalog_id", "operation", "policy", "replay"),
				checkConstraint("octet_length(policy) = 32"),
				checkConstraint("octet_length(replay) = 32"),
				checkConstraintAs("unsettled >= 0 AND unsettled <= 18446744073709551615", "unsettled >= 0::numeric AND unsettled <= '18446744073709551615'::numeric"),
				checkConstraint("octet_length(state_wire) > 0"),
			},
		},
		{
			name: "attempt_identity_aliases",
			columns: []pgColumnSpec{
				{name: "catalog_id", typeName: "text", notNull: true},
				{name: "operation", typeName: "text", notNull: true},
				{name: "policy", typeName: "bytea", notNull: true},
				{name: "replay", typeName: "bytea", notNull: true},
				{name: "domain", typeName: "smallint", notNull: true},
				{name: "algorithm", typeName: "text", notNull: true},
				{name: "profile", typeName: "text", notNull: true},
				{name: "key_id", typeName: "text", notNull: true},
				{name: "commitment", typeName: "bytea", notNull: true},
				{name: "operation_id", typeName: "bytea", notNull: true},
				{name: "chain_id", typeName: "bytea", notNull: true},
			},
			constraints: []pgConstraintSpec{
				primaryKey("catalog_id", "operation", "policy", "replay", "domain", "algorithm", "profile", "key_id", "commitment", "operation_id", "chain_id"),
				checkConstraintAs("domain IN (3, 7, 8)", "domain = ANY (ARRAY[3, 7, 8])"),
				checkConstraint("octet_length(commitment) = 32"),
				checkConstraint("octet_length(operation_id) = 16"),
				checkConstraint("octet_length(chain_id) = 32"),
				foreignKey([]string{"chain_id"}, "attempt_chains", []string{"chain_id"}, true, true),
			},
			immutable: true,
		},
		{
			name: "attempt_idempotency",
			columns: []pgColumnSpec{
				{name: "domain", typeName: "smallint", notNull: true},
				{name: "catalog_id", typeName: "text", notNull: true},
				{name: "operation", typeName: "text", notNull: true},
				{name: "chain_id", typeName: "bytea", notNull: true},
				{name: "algorithm", typeName: "text", notNull: true},
				{name: "profile", typeName: "text", notNull: true},
				{name: "key_id", typeName: "text", notNull: true},
				{name: "commitment", typeName: "bytea", notNull: true},
				{name: "semantic", typeName: "bytea", notNull: true},
				{name: "revision_id", typeName: "bytea", notNull: true},
			},
			constraints: []pgConstraintSpec{
				primaryKey("domain", "catalog_id", "chain_id", "algorithm", "profile", "key_id", "commitment"),
				checkConstraintAs("domain IN (2, 3, 4)", "domain = ANY (ARRAY[2, 3, 4])"),
				checkConstraint("octet_length(chain_id) = 32"),
				checkConstraint("octet_length(commitment) = 32"),
				checkConstraint("octet_length(semantic) = 32"),
				checkConstraint("octet_length(revision_id) = 16"),
				foreignKey([]string{"revision_id"}, "revisions", []string{"revision_id"}, true, true),
			},
			immutable: true,
		},
		{
			name: "attempt_transitions",
			columns: []pgColumnSpec{
				{name: "revision_id", typeName: "bytea", notNull: true},
				{name: "ordinal", typeName: "integer", notNull: true},
				{name: "chain_id", typeName: "bytea", notNull: true},
				{name: "sequence", typeName: "integer", notNull: true},
				{name: "kind", typeName: "smallint", notNull: true},
				{name: "previous_leaf", typeName: "bytea"},
				{name: "leaf", typeName: "bytea", notNull: true},
			},
			constraints: []pgConstraintSpec{
				primaryKey("revision_id", "ordinal"),
				uniqueConstraint("chain_id", "sequence"),
				foreignKey([]string{"revision_id"}, "revisions", []string{"revision_id"}, false, false),
				foreignKey([]string{"chain_id"}, "attempt_chains", []string{"chain_id"}, true, true),
				checkConstraint("ordinal >= 0"),
				checkConstraintAs("sequence BETWEEN 1 AND 259", "sequence >= 1 AND sequence <= 259"),
				checkConstraintAs("kind BETWEEN 1 AND 7", "kind >= 1 AND kind <= 7"),
				checkConstraint("previous_leaf IS NULL OR octet_length(previous_leaf) = 32"),
				checkConstraint("octet_length(leaf) = 32"),
			},
			immutable: true,
		},
	}
}

func primaryKey(columns ...string) pgConstraintSpec {
	return pgConstraintSpec{kind: "p", columns: slices.Clone(columns)}
}

func uniqueConstraint(columns ...string) pgConstraintSpec {
	return pgConstraintSpec{kind: "u", columns: slices.Clone(columns)}
}

func checkConstraint(expression string) pgConstraintSpec {
	return checkConstraintAs(expression, expression)
}

func checkConstraintAs(expression, catalog string) pgConstraintSpec {
	return pgConstraintSpec{kind: "c", check: expression, catalogCheck: canonicalSQL(catalog)}
}

func foreignKey(columns []string, table string, reference []string, deferrable, deferred bool) pgConstraintSpec {
	return pgConstraintSpec{
		kind: "f", columns: slices.Clone(columns), referenceTable: table,
		referenceColumns: slices.Clone(reference), deferrable: deferrable, initiallyDeferred: deferred,
	}
}

func MigrationStatements(schema Schema) ([]string, error) {
	resolved, err := schema.Resolved()
	if err != nil {
		return nil, err
	}
	fingerprint, err := resolved.Fingerprint()
	if err != nil {
		return nil, err
	}
	q := quoteIdentifier(resolved.Name)
	statements := []string{"CREATE SCHEMA IF NOT EXISTS " + q}
	for _, table := range postgresTables() {
		statements = append(statements, renderTable(q, table))
	}
	statements = append(statements, `CREATE OR REPLACE FUNCTION `+q+`.deny_immutable_audit_row() RETURNS trigger
LANGUAGE plpgsql AS $auditpg$
`+immutableFunctionSource+`
$auditpg$`)
	for _, table := range postgresTables() {
		if table.immutable {
			statements = append(statements, immutableTrigger(q, table.name))
		}
	}
	statements = append(statements,
		"INSERT INTO "+q+`.settings (singleton, version, fingerprint, backing_id, log_id)
VALUES (true, `+strconv.Itoa(SchemaVersion)+`, `+quoteLiteral(fingerprint)+`,
	decode(replace(gen_random_uuid()::text, '-', ''), 'hex'),
	decode(replace(gen_random_uuid()::text, '-', ''), 'hex'))
ON CONFLICT (singleton) DO NOTHING`,
		`DO $auditpg$
DECLARE deployed record;
BEGIN
	SELECT version, fingerprint INTO deployed FROM `+q+`.settings WHERE singleton;
	IF deployed.version IS DISTINCT FROM `+strconv.Itoa(SchemaVersion)+` OR deployed.fingerprint IS DISTINCT FROM `+quoteLiteral(fingerprint)+` THEN
		RAISE EXCEPTION 'auditpg: schema mismatch';
	END IF;
END
$auditpg$`,
	)
	return statements, nil
}

func renderTable(schema string, table pgTableSpec) string {
	definitions := make([]string, 0, len(table.columns)+len(table.constraints))
	for _, column := range table.columns {
		definition := column.name + " " + column.typeName
		if column.identity == "a" {
			definition += " GENERATED ALWAYS AS IDENTITY"
		}
		if column.notNull {
			definition += " NOT NULL"
		}
		if column.defaultExpression != "" {
			definition += " DEFAULT " + column.defaultExpression
		}
		definitions = append(definitions, definition)
	}
	for _, constraint := range table.constraints {
		definitions = append(definitions, renderConstraint(schema, constraint))
	}
	return "CREATE TABLE IF NOT EXISTS " + schema + "." + table.name + " (\n\t" + strings.Join(definitions, ",\n\t") + "\n)"
}

func renderConstraint(schema string, constraint pgConstraintSpec) string {
	columns := strings.Join(constraint.columns, ", ")
	switch constraint.kind {
	case "p":
		return "PRIMARY KEY (" + columns + ")"
	case "u":
		return "UNIQUE (" + columns + ")"
	case "c":
		return "CHECK (" + constraint.check + ")"
	case "f":
		definition := "FOREIGN KEY (" + columns + ") REFERENCES " + schema + "." + constraint.referenceTable + " (" + strings.Join(constraint.referenceColumns, ", ") + ")"
		if constraint.deferrable {
			definition += " DEFERRABLE"
		}
		if constraint.initiallyDeferred {
			definition += " INITIALLY DEFERRED"
		}
		return definition
	default:
		panic("auditpg: unknown schema constraint")
	}
}

func immutableTrigger(schema, table string) string {
	name := table + "_immutable"
	return `DO $auditpg$
BEGIN
	DROP TRIGGER IF EXISTS ` + name + ` ON ` + schema + `.` + table + `;
	CREATE TRIGGER ` + name + ` BEFORE UPDATE OR DELETE OR TRUNCATE ON ` + schema + `.` + table + `
		FOR EACH STATEMENT EXECUTE FUNCTION ` + schema + `.deny_immutable_audit_row();
END
$auditpg$`
}

func expectedSchemaDescriptors(schema Schema) []schemaDescriptor {
	var descriptors []schemaDescriptor
	for _, table := range postgresTables() {
		descriptors = append(descriptors, schemaDescriptor{kind: "table", object: table.name, detail: descriptorDetail("r", "p", false, false)})
		for _, column := range table.columns {
			descriptors = append(descriptors, schemaDescriptor{
				kind: "column", object: table.name, member: column.name,
				detail: descriptorDetail(column.typeName, column.notNull, column.identity, "", column.defaultExpression),
			})
		}
		for _, constraint := range table.constraints {
			descriptors = append(descriptors, expectedConstraintDescriptor(schema, table, constraint))
			if constraint.kind == "p" || constraint.kind == "u" {
				descriptors = append(descriptors, schemaDescriptor{
					kind: "index", object: table.name,
					detail: descriptorDetail(
						constraint.kind, strings.Join(constraint.columns, ","), true, constraint.kind == "p",
						true, true, true, false, "btree", len(constraint.columns), len(constraint.columns), "", false,
					),
				})
			}
		}
		if table.immutable {
			descriptors = append(descriptors, schemaDescriptor{
				kind: "trigger", object: table.name, member: table.name + "_immutable",
				detail: descriptorDetail("O", 58, schema.Name, "deny_immutable_audit_row", "", "", 0, false, false),
			})
		}
	}
	descriptors = append(descriptors, schemaDescriptor{
		kind: "function", object: "deny_immutable_audit_row",
		detail: descriptorDetail("trigger", "plpgsql", "v", false, false, "f", "u", false, "", normalizeFunctionSource(immutableFunctionSource)),
	})
	slices.SortFunc(descriptors, func(left, right schemaDescriptor) int {
		if order := strings.Compare(left.kind, right.kind); order != 0 {
			return order
		}
		if order := strings.Compare(left.object, right.object); order != 0 {
			return order
		}
		if order := strings.Compare(left.member, right.member); order != 0 {
			return order
		}
		return strings.Compare(left.detail, right.detail)
	})
	return descriptors
}

func expectedConstraintDescriptor(schema Schema, table pgTableSpec, constraint pgConstraintSpec) schemaDescriptor {
	referenceSchema, updateAction, deleteAction, matchType := "", "", "", ""
	if constraint.kind == "f" {
		referenceSchema, updateAction, deleteAction, matchType = schema.Name, "a", "a", "s"
	}
	columns := constraint.columns
	if constraint.kind == "c" {
		columns = checkColumns(table, constraint.check)
	}
	return schemaDescriptor{
		kind: "constraint", object: table.name,
		detail: descriptorDetail(
			constraint.kind, strings.Join(columns, ","), referenceSchema, constraint.referenceTable,
			strings.Join(constraint.referenceColumns, ","), constraint.deferrable, constraint.initiallyDeferred,
			updateAction, deleteAction, matchType, true, constraint.kind != "c", constraint.catalogCheck,
		),
	}
}

func checkColumns(table pgTableSpec, expression string) []string {
	var columns []string
	for _, column := range table.columns {
		for offset := 0; offset+len(column.name) <= len(expression); offset++ {
			if expression[offset:offset+len(column.name)] != column.name {
				continue
			}
			before := offset == 0 || !identifierByte(expression[offset-1])
			after := offset+len(column.name) == len(expression) || !identifierByte(expression[offset+len(column.name)])
			if before && after {
				columns = append(columns, column.name)
				break
			}
		}
	}
	return columns
}

func identifierByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func descriptorDetail(values ...any) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = fmt.Sprint(value)
	}
	return strings.Join(parts, "\x1f")
}

func canonicalSQL(value string) string {
	var result strings.Builder
	for _, current := range strings.ToLower(value) {
		switch current {
		case ' ', '\t', '\n', '\r', '(', ')', '"':
		default:
			result.WriteRune(current)
		}
	}
	return result.String()
}

func normalizeFunctionSource(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
