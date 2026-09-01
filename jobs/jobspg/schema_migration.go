package jobspg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/frostgrove/vv/jobs"
)

const catalogFingerprintPrefix = "sha256:"
const catalogFingerprintDigestBytes = 64

type schemaConstraint struct {
	name       string
	table      string
	definition string
}

func (r repository) schemaConstraints() []schemaConstraint {
	nameBytes := fmt.Sprint(jobs.MaxNameBytes)
	codecBytes := fmt.Sprint(jobs.MaxCodecIDBytes)
	bindingBytes := fmt.Sprint(jobs.MaxBindingNameBytes)
	buildBytes := fmt.Sprint(jobs.MaxBuildIDBytes)
	maxVersion := quoteStringLiteral(fmt.Sprint(uint64(math.MaxUint32))) + `::bigint`
	fingerprintBytes := len(catalogFingerprintPrefix) + catalogFingerprintDigestBytes
	fingerprint := schemaAnd(
		schemaComparison(`octet_length(fingerprint)`, `=`, fmt.Sprint(fingerprintBytes)),
		schemaComparison(`fingerprint`, `~`, quoteStringLiteral(`^`+catalogFingerprintPrefix+`[0-9a-f]{`+fmt.Sprint(catalogFingerprintDigestBytes)+`}$`)+`::text`),
	)
	revisions := schemaRevisionConstraint("codec_revisions", "codec_version", maxVersion)
	legacyDefinition := schemaAnd(
		schemaNull("codec"),
		schemaNull("codec_mode"),
		schemaNull("codec_version"),
		schemaNull("codec_revisions"),
		schemaNull("partition_mode"),
		schemaNull("payload_identity"),
		schemaNull("payload_identity_version"),
		schemaNull("payload_identity_automatic"),
	)
	identityDefinition := schemaOr(
		schemaAnd(
			schemaNull("payload_identity"),
			schemaNull("payload_identity_version"),
			`(NOT payload_identity_automatic)`,
		),
		schemaAnd(
			schemaNotNull("payload_identity"),
			schemaBounds(`octet_length(payload_identity)`, "1", codecBytes),
			schemaNotNull("payload_identity_version"),
			schemaBounds("payload_identity_version", "1", maxVersion),
		),
	)
	currentDefinition := schemaAnd(
		schemaNotNull("codec"),
		schemaBounds(`octet_length(codec)`, "1", codecBytes),
		schemaNotNull("codec_mode"),
		schemaComparison("codec_mode", `= ANY`, `(ARRAY[`+quoteStringLiteral(string(jobs.SafeCodecMode))+`::text, `+quoteStringLiteral(string(jobs.TrustedCodecMode))+`::text, `+quoteStringLiteral(string(jobs.CustomCodecMode))+`::text])`),
		schemaNotNull("codec_version"),
		schemaBounds("codec_version", "1", maxVersion),
		schemaNotNull("codec_revisions"),
		revisions,
		schemaNotNull("partition_mode"),
		schemaComparison("partition_mode", `= ANY`, `(ARRAY[0, 1])`),
		schemaNotNull("payload_identity_automatic"),
		identityDefinition,
	)
	payloadDefinition := schemaOr(
		schemaAnd(
			schemaNull("payload_identity"),
			schemaNull("payload_version"),
			schemaNull("payload_digest"),
		),
		schemaAnd(
			schemaNotNull("payload_identity"),
			schemaBounds(`octet_length(payload_identity)`, "1", codecBytes),
			schemaNotNull("payload_version"),
			schemaBounds("payload_version", "1", maxVersion),
			schemaNotNull("payload_digest"),
			schemaComparison(`octet_length(payload_digest)`, "=", "32"),
		),
	)
	exclusionDefinition := schemaOr(
		schemaAnd(schemaNull("excluded_binding"), schemaNull("excluded_build")),
		schemaAnd(
			schemaNotNull("excluded_binding"),
			schemaBounds(`octet_length(excluded_binding)`, "1", bindingBytes),
			schemaNotNull("excluded_build"),
			schemaBounds(`octet_length(excluded_build)`, "1", buildBytes),
		),
	)
	recordDefinition := schemaOr(
		schemaAnd(schemaNull("record"), schemaNull("record_size")),
		schemaAnd(
			schemaNotNull("record"),
			schemaNotNull("record_size"),
			schemaBounds("record_size", "1", fmt.Sprint(jobs.MaxDeliveryRecordBytes)),
			schemaBounds(`octet_length(record)`, "1", fmt.Sprint(maxEncodedDeliveryRecordBytes)),
		),
	)
	return []schemaConstraint{
		{
			name:  "catalogs_core_contract_check",
			table: "catalogs",
			definition: schemaAnd(
				schemaBounds(`octet_length(application)`, "1", nameBytes),
				schemaBounds(`octet_length(environment)`, "1", nameBytes),
				fingerprint,
			),
		},
		{
			name:  "catalog_definitions_core_contract_check",
			table: "catalog_definitions",
			definition: schemaAnd(
				schemaBounds(`octet_length(definition)`, "1", nameBytes),
				fingerprint,
				schemaOr(legacyDefinition, currentDefinition),
			),
		},
		{
			name:  "deliveries_core_contract_check",
			table: "deliveries",
			definition: schemaAnd(
				schemaBounds(`octet_length(definition)`, "1", nameBytes),
				schemaBounds(`octet_length(codec)`, "1", codecBytes),
				schemaBounds("codec_version", "1", maxVersion),
				schemaBounds("priority", "1", fmt.Sprint(jobs.MaximumPriority)),
				schemaBounds("state", fmt.Sprint(int(jobs.InvocationQueued)), fmt.Sprint(int(jobs.InvocationTerminated))),
				recordDefinition,
				payloadDefinition,
				exclusionDefinition,
			),
		},
		{
			name:       "deliveries_intent_keys_contract_check",
			table:      "deliveries",
			definition: intentKeysSchemaConstraint(),
		},
	}
}

func intentKeysSchemaConstraint() string {
	cases := make([]string, 0, jobs.MaxIntentDigestKeys)
	for count := 1; count <= jobs.MaxIntentDigestKeys; count++ {
		encodedBytes := intentKeysHeaderBytes + count*intentKeyEncodingBytes
		cases = append(cases, `WHEN `+schemaComparison(`octet_length(intent_keys)`, "=", fmt.Sprint(encodedBytes))+`
        THEN `+schemaAnd(
			schemaComparison(`get_byte(intent_keys, 0)`, "=", fmt.Sprint(intentKeysEncodingVersion)),
			schemaComparison(`get_byte(intent_keys, 1)`, "=", fmt.Sprint(count)),
		))
	}
	return schemaOr(
		schemaNull("intent_keys"),
		`CASE
        `+strings.Join(cases, "\n        ")+`
        ELSE false
      END`,
	)
}

func schemaRevisionConstraint(column, version, maxVersion string) string {
	array := `(string_to_array(` + column + `, ','::text))::bigint[]`
	conditions := []string{
		schemaComparison("0", `< ALL`, `(`+array+`)`),
		schemaComparison(maxVersion, `>= ALL`, `(`+array+`)`),
		schemaComparison(version, "=", `(`+array+`)[cardinality(`+array+`)]`),
	}
	for position := 2; position <= jobs.MaxSupportedRevisions; position++ {
		conditions = append(conditions, schemaOr(
			schemaComparison(`cardinality(`+array+`)`, "<", fmt.Sprint(position)),
			schemaComparison(`(`+array+`)[`+fmt.Sprint(position-1)+`]`, "<", `(`+array+`)[`+fmt.Sprint(position)+`]`),
		))
	}
	return `CASE
        WHEN ` + schemaComparison(column, "~", quoteStringLiteral(`^[1-9][0-9]{0,9}(,[1-9][0-9]{0,9}){0,`+fmt.Sprint(jobs.MaxSupportedRevisions-1)+`}$`)+`::text`) + `
        THEN ` + schemaAnd(conditions...) + `
        ELSE false
      END`
}

func schemaAnd(conditions ...string) string {
	return schemaBoolean(` AND `, conditions)
}

func schemaOr(conditions ...string) string {
	return schemaBoolean(` OR `, conditions)
}

func schemaBoolean(operator string, conditions []string) string {
	flattened := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		if parts, ok := splitSchemaBoolean(condition, operator); ok {
			flattened = append(flattened, parts...)
		} else {
			flattened = append(flattened, condition)
		}
	}
	return `(` + strings.Join(flattened, operator) + `)`
}

func splitSchemaBoolean(expression, operator string) ([]string, bool) {
	if len(expression) < 2 || expression[0] != '(' || expression[len(expression)-1] != ')' {
		return nil, false
	}
	inner := expression[1 : len(expression)-1]
	depth := 0
	quoted := false
	start := 0
	var parts []string
	for position := 0; position < len(inner); position++ {
		switch inner[position] {
		case '\'':
			if quoted && position+1 < len(inner) && inner[position+1] == '\'' {
				position++
				continue
			}
			quoted = !quoted
		case '(':
			if !quoted {
				depth++
			}
		case ')':
			if !quoted {
				depth--
			}
		default:
			if !quoted && depth == 0 && strings.HasPrefix(inner[position:], operator) {
				parts = append(parts, strings.TrimSpace(inner[start:position]))
				position += len(operator) - 1
				start = position + 1
			}
		}
	}
	if len(parts) == 0 || quoted || depth != 0 {
		return nil, false
	}
	return append(parts, strings.TrimSpace(inner[start:])), true
}

func schemaComparison(left, operator, right string) string {
	return `(` + left + ` ` + operator + ` ` + right + `)`
}

func schemaBounds(expression, minimum, maximum string) string {
	return schemaAnd(
		schemaComparison(expression, ">=", minimum),
		schemaComparison(expression, "<=", maximum),
	)
}

func schemaNull(column string) string {
	return `(` + column + ` IS NULL)`
}

func schemaNotNull(column string) string {
	return `(` + column + ` IS NOT NULL)`
}

func (r repository) schemaHardeningMigrationStatements() []string {
	specs := r.schemaConstraints()
	statements := make([]string, 0, 2*len(specs))
	for _, spec := range specs {
		statements = append(statements, `DO $frostgrove$
BEGIN
IF NOT EXISTS (
  SELECT 1
  FROM pg_constraint
  WHERE conname = `+quoteStringLiteral(spec.name)+`
    AND conrelid = '`+r.schemaConstraintTable(spec)+`'::regclass
    AND contype = 'c'
) THEN
  ALTER TABLE `+r.schemaConstraintTable(spec)+` ADD CONSTRAINT `+quoteIdentifier(spec.name)+` CHECK (
  `+spec.definition+`
  ) NOT VALID;
END IF;
END
$frostgrove$`)
	}
	for _, spec := range specs {
		statements = append(statements, `ALTER TABLE `+r.schemaConstraintTable(spec)+` VALIDATE CONSTRAINT `+quoteIdentifier(spec.name))
	}
	return statements
}

func (r repository) schemaConstraintValidationStatements() []string {
	statements := make([]string, len(r.schemaConstraints()))
	for index, spec := range r.schemaConstraints() {
		statements[index] = `DO $frostgrove$
BEGIN
IF NOT EXISTS (
  SELECT 1
  FROM pg_constraint AS schema_constraint
  JOIN pg_class AS relation ON relation.oid = schema_constraint.conrelid
  JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
  WHERE namespace.nspname = ` + quoteStringLiteral(r.rawSchema) + `
    AND relation.relname = ` + quoteStringLiteral(spec.table) + `
    AND schema_constraint.conname = ` + quoteStringLiteral(spec.name) + `
    AND schema_constraint.contype = 'c'
    AND schema_constraint.convalidated
    AND NOT schema_constraint.connoinherit
    AND regexp_replace(pg_get_expr(schema_constraint.conbin, schema_constraint.conrelid, false), '[[:space:]]+', '', 'g') = ` + quoteStringLiteral(normalizeIndexDefinition(spec.definition)) + `
) THEN
  RAISE EXCEPTION 'jobspg: schema constraint ` + spec.name + ` definition mismatch';
END IF;
END
$frostgrove$`
	}
	return statements
}

func (r repository) validateSchemaConstraints(ctx context.Context, querier indexStateQuerier) error {
	for _, spec := range r.schemaConstraints() {
		var validated, noInherit bool
		var definition string
		err := querier.QueryRowContext(ctx, `SELECT schema_constraint.convalidated,
       schema_constraint.connoinherit,
       COALESCE(pg_get_expr(schema_constraint.conbin, schema_constraint.conrelid, false), '')
FROM pg_constraint AS schema_constraint
JOIN pg_class AS relation ON relation.oid = schema_constraint.conrelid
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = $1
  AND relation.relname = $2
  AND schema_constraint.conname = $3
  AND schema_constraint.contype = 'c'`, r.rawSchema, spec.table, spec.name).Scan(&validated, &noInherit, &definition)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !schemaConstraintMatches(validated, noInherit, definition, spec.definition) {
			return fmt.Errorf("%w: schema constraint %q is not ready", ErrSchemaMismatch, spec.name)
		}
		if err != nil {
			return fmt.Errorf("jobspg: inspect schema constraint %q: %w", spec.name, err)
		}
	}
	return nil
}

func schemaConstraintMatches(validated, noInherit bool, definition, expected string) bool {
	return validated && !noInherit && normalizeIndexDefinition(definition) == normalizeIndexDefinition(expected)
}

func (r repository) schemaConstraintTable(spec schemaConstraint) string {
	switch spec.table {
	case "catalogs":
		return r.catalogs
	case "catalog_definitions":
		return r.definitions
	default:
		return r.deliveries
	}
}
