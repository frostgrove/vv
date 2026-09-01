package jobspg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/frostgrove/vv/jobs"
)

type operationalIndex struct {
	name                string
	table               string
	columns             []string
	predicate           string
	predicateDefinition string
}

var operationalIndexes = []operationalIndex{
	{
		name:                "deliveries_ready_idx",
		table:               "deliveries",
		columns:             []string{"namespace", "definition", "priority", "available_at", "id"},
		predicate:           "state = " + fmt.Sprint(int(jobs.InvocationQueued)) + " AND lease_token IS NULL",
		predicateDefinition: "((state = " + fmt.Sprint(int(jobs.InvocationQueued)) + ") AND (lease_token IS NULL))",
	},
	{
		name:                "deliveries_expired_idx",
		table:               "deliveries",
		columns:             []string{"namespace", "lease_expires_at", "id"},
		predicate:           "lease_token IS NOT NULL",
		predicateDefinition: "(lease_token IS NOT NULL)",
	},
	{
		name:    "intents_invocation_idx",
		table:   "intents",
		columns: []string{"namespace", "invocation_id"},
	},
}

func (r repository) operationalIndexStatements() []string {
	statements := make([]string, len(operationalIndexes))
	for index, spec := range operationalIndexes {
		statements[index] = `CREATE INDEX IF NOT EXISTS ` + quoteIdentifier(spec.name) + ` ON ` + r.operationalIndexTable(spec) + ` (` + strings.Join(spec.columns, ", ") + `)`
		if spec.predicate != "" {
			statements[index] += ` WHERE ` + spec.predicate
		}
	}
	return statements
}

func (r repository) operationalIndexValidationStatements() []string {
	statements := make([]string, len(operationalIndexes))
	for index, spec := range operationalIndexes {
		statements[index] = `DO $frostgrove$
BEGIN
IF NOT EXISTS (
  SELECT 1
  FROM pg_class AS relation
  JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
  JOIN pg_index AS index ON index.indexrelid = relation.oid
  JOIN pg_class AS table_relation ON table_relation.oid = index.indrelid
  JOIN pg_namespace AS table_namespace ON table_namespace.oid = table_relation.relnamespace
  JOIN pg_am AS access_method ON access_method.oid = relation.relam
  WHERE namespace.nspname = ` + quoteStringLiteral(r.rawSchema) + `
    AND relation.relname = ` + quoteStringLiteral(spec.name) + `
    AND index.indisvalid
    AND index.indisready
    AND ` + r.operationalIndexMatchDefinition(spec) + `
) THEN
  RAISE EXCEPTION 'jobspg: operational index ` + spec.name + ` schema mismatch';
END IF;
END
$frostgrove$`
	}
	return statements
}

func (r repository) operationalIndexState(ctx context.Context, querier indexStateQuerier, spec operationalIndex) (bool, bool, bool, bool, error) {
	var valid, ready, matching bool
	err := querier.QueryRowContext(ctx, `SELECT index.indisvalid,
       index.indisready,
       `+r.operationalIndexMatchDefinition(spec)+`
FROM pg_class AS relation
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
JOIN pg_index AS index ON index.indexrelid = relation.oid
JOIN pg_class AS table_relation ON table_relation.oid = index.indrelid
JOIN pg_namespace AS table_namespace ON table_namespace.oid = table_relation.relnamespace
JOIN pg_am AS access_method ON access_method.oid = relation.relam
WHERE namespace.nspname = $1 AND relation.relname = $2`, r.rawSchema, spec.name).Scan(&valid, &ready, &matching)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, false, false, nil
	}
	if err != nil {
		return false, false, false, false, fmt.Errorf("jobspg: inspect operational index %q: %w", spec.name, err)
	}
	return valid, ready, matching, true, nil
}

func (r repository) validateOperationalIndexes(ctx context.Context, querier indexStateQuerier) error {
	for _, spec := range operationalIndexes {
		valid, ready, matching, exists, err := r.operationalIndexState(ctx, querier, spec)
		if err != nil {
			return err
		}
		if !exists || !valid || !ready || !matching {
			return fmt.Errorf("%w: operational index %q is not ready", ErrSchemaMismatch, spec.name)
		}
	}
	return nil
}

func (r repository) operationalIndexMatchDefinition(spec operationalIndex) string {
	conditions := []string{
		`table_namespace.nspname = ` + quoteStringLiteral(r.rawSchema),
		`table_relation.relname = ` + quoteStringLiteral(spec.table),
		`relation.relkind = 'i'`,
		`access_method.amname = 'btree'`,
		`NOT index.indisunique`,
		`NOT index.indisprimary`,
		`NOT index.indisexclusion`,
		`index.indnkeyatts = ` + fmt.Sprint(len(spec.columns)),
		`index.indnatts = ` + fmt.Sprint(len(spec.columns)),
		`index.indexprs IS NULL`,
		`index.indoption::text = ` + quoteStringLiteral(strings.TrimSpace(strings.Repeat("0 ", len(spec.columns)))),
	}
	for position, column := range spec.columns {
		conditions = append(conditions, `COALESCE(pg_get_indexdef(relation.oid, `+fmt.Sprint(position+1)+`, false), '') = `+quoteStringLiteral(column))
	}
	if spec.predicateDefinition == "" {
		conditions = append(conditions, `index.indpred IS NULL`)
	} else {
		conditions = append(conditions,
			`index.indpred IS NOT NULL`,
			`regexp_replace(pg_get_expr(index.indpred, index.indrelid, false), '[[:space:]]+', '', 'g') = `+quoteStringLiteral(normalizeIndexDefinition(spec.predicateDefinition)),
		)
	}
	return strings.Join(conditions, "\n    AND ")
}

func (r repository) operationalIndexTable(spec operationalIndex) string {
	if spec.table == "intents" {
		return r.intents
	}
	return r.deliveries
}
