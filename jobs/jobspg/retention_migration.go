package jobspg

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/frostgrove/vv/jobs"
)

type retentionIndex struct {
	name                 string
	columns              string
	predicate            string
	expressionDefinition string
	predicateDefinition  string
}

var retentionIndexes = []retentionIndex{
	{
		name:                 "deliveries_retention_idx",
		columns:              "namespace, (COALESCE(CASE WHEN record IS NULL THEN intent_expires_at ELSE record_expires_at END, '-infinity'::timestamptz)), updated_at, id",
		predicate:            "state IN (" + terminalStatesSQL + ")",
		expressionDefinition: "COALESCE(CASE WHEN (record IS NULL) THEN intent_expires_at ELSE record_expires_at END, '-infinity'::timestamp with time zone)",
		predicateDefinition:  "(state = ANY (ARRAY[" + terminalStatesSQL + "]))",
	},
}

type indexStateQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r repository) retentionIndexStatements(concurrent bool) []string {
	modifier := ""
	if concurrent {
		modifier = " CONCURRENTLY"
	}
	statements := make([]string, len(retentionIndexes))
	for index, spec := range retentionIndexes {
		statements[index] = `CREATE INDEX` + modifier + ` IF NOT EXISTS ` + quoteIdentifier(spec.name) + ` ON ` + r.deliveries + ` (` + spec.columns + `) WHERE ` + spec.predicate
	}
	return statements
}

func (r repository) retentionIndexCommentStatements() []string {
	statements := make([]string, len(retentionIndexes))
	for index, spec := range retentionIndexes {
		statements[index] = `COMMENT ON INDEX ` + r.schema + `.` + quoteIdentifier(spec.name) + ` IS '` + spec.fingerprint() + `'`
	}
	return statements
}

func (r repository) retentionIndexValidationStatements() []string {
	statements := make([]string, len(retentionIndexes))
	for index, spec := range retentionIndexes {
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
    AND table_namespace.nspname = ` + quoteStringLiteral(r.rawSchema) + `
    AND table_relation.relname = 'deliveries'
    AND access_method.amname = 'btree'
    AND NOT index.indisunique
    AND NOT index.indisprimary
    AND NOT index.indisexclusion
    AND index.indnkeyatts = 4
    AND index.indnatts = 4
    AND index.indoption::text = '0 0 0 0'
    AND index.indcollation::text = '0 0 0 0'
    AND pg_get_indexdef(relation.oid, 1, false) = 'namespace'
    AND regexp_replace(pg_get_indexdef(relation.oid, 2, false), '[[:space:]]+', '', 'g') = ` + quoteStringLiteral(normalizeIndexDefinition(spec.expressionDefinition)) + `
    AND pg_get_indexdef(relation.oid, 3, false) = 'updated_at'
    AND pg_get_indexdef(relation.oid, 4, false) = 'id'
    AND regexp_replace(pg_get_expr(index.indexprs, index.indrelid, false), '[[:space:]]+', '', 'g') = ` + quoteStringLiteral(normalizeIndexDefinition(spec.expressionDefinition)) + `
    AND regexp_replace(pg_get_expr(index.indpred, index.indrelid, false), '[[:space:]]+', '', 'g') = ` + quoteStringLiteral(normalizeIndexDefinition(spec.predicateDefinition)) + `
) THEN
  RAISE EXCEPTION 'jobspg: retention index ` + spec.name + ` schema mismatch';
END IF;
END
$frostgrove$`
	}
	return statements
}

func (spec retentionIndex) fingerprint() string {
	digest := sha256.Sum256([]byte(spec.name + "\x00" + spec.columns + "\x00" + spec.predicate))
	return fmt.Sprintf("frostgrove.jobs.retention-index.v1:%x", digest)
}

func normalizeIndexDefinition(value string) string {
	return strings.Join(strings.Fields(value), "")
}

func quoteStringLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

func (r repository) withMigrationLock(ctx context.Context, db *sql.DB, work func(*sql.Conn) error) (resultErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("jobspg: retention migration connection: %w", err)
	}
	locked := false
	discard := false
	defer func() {
		if locked {
			unlockContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			var unlocked bool
			unlockErr := conn.QueryRowContext(unlockContext, `SELECT pg_advisory_unlock($1)`, retentionMigrationLock(r.rawSchema)).Scan(&unlocked)
			cancel()
			if unlockErr == nil && !unlocked {
				unlockErr = jobs.ErrDriver
			}
			if unlockErr != nil {
				discard = true
				resultErr = errors.Join(resultErr, fmt.Errorf("jobspg: retention migration unlock: %w", unlockErr))
			}
		}
		if discard {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		resultErr = errors.Join(resultErr, conn.Close())
	}()
	for !locked {
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, retentionMigrationLock(r.rawSchema)).Scan(&locked); err != nil {
			discard = true
			return fmt.Errorf("jobspg: retention migration lock: %w", err)
		}
		if locked {
			break
		}
		delay := 250*time.Millisecond + time.Duration(rand.Int64N(int64(250*time.Millisecond)))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return work(conn)
}

func (r repository) buildRetentionIndexes(ctx context.Context, conn *sql.Conn) error {
	for _, spec := range retentionIndexes {
		valid, ready, matching, identified, exists, err := r.retentionIndexState(ctx, conn, spec)
		if err != nil {
			return err
		}
		qualified := r.schema + `.` + quoteIdentifier(spec.name)
		rebuild := !exists || !valid || !ready || !matching
		if exists && rebuild {
			if _, err := conn.ExecContext(ctx, `DROP INDEX CONCURRENTLY `+qualified); err != nil {
				return fmt.Errorf("jobspg: drop incompatible retention index %q: %w", spec.name, err)
			}
		}
		if rebuild {
			statement := `CREATE INDEX CONCURRENTLY ` + quoteIdentifier(spec.name) + ` ON ` + r.deliveries + ` (` + spec.columns + `) WHERE ` + spec.predicate
			if _, err := conn.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("jobspg: create retention index %q: %w", spec.name, err)
			}
		}
		if rebuild || !identified {
			if _, err := conn.ExecContext(ctx, `COMMENT ON INDEX `+qualified+` IS '`+spec.fingerprint()+`'`); err != nil {
				return fmt.Errorf("jobspg: identify retention index %q: %w", spec.name, err)
			}
		}
		valid, ready, matching, identified, exists, err = r.retentionIndexState(ctx, conn, spec)
		if err != nil {
			return err
		}
		if !exists || !valid || !ready || !matching || !identified {
			return fmt.Errorf("%w: retention index %q is not ready", ErrSchemaMismatch, spec.name)
		}
	}
	return nil
}

func (r repository) retentionIndexState(ctx context.Context, querier indexStateQuerier, spec retentionIndex) (bool, bool, bool, bool, bool, error) {
	var valid, ready, structural bool
	var first, expression, third, fourth, indexExpression, predicate, fingerprint string
	err := querier.QueryRowContext(ctx, `SELECT index.indisvalid,
       index.indisready,
       table_namespace.nspname = $1
       AND table_relation.relname = 'deliveries'
       AND access_method.amname = 'btree'
       AND NOT index.indisunique
       AND NOT index.indisprimary
       AND NOT index.indisexclusion
       AND index.indnkeyatts = 4
       AND index.indnatts = 4
       AND index.indexprs IS NOT NULL
       AND index.indpred IS NOT NULL
       AND index.indoption::text = '0 0 0 0'
       AND index.indcollation::text = '0 0 0 0',
       COALESCE(pg_get_indexdef(relation.oid, 1, false), ''),
       COALESCE(pg_get_indexdef(relation.oid, 2, false), ''),
       COALESCE(pg_get_indexdef(relation.oid, 3, false), ''),
       COALESCE(pg_get_indexdef(relation.oid, 4, false), ''),
       COALESCE(pg_get_expr(index.indexprs, index.indrelid, false), ''),
       COALESCE(pg_get_expr(index.indpred, index.indrelid, false), ''),
       COALESCE(obj_description(relation.oid, 'pg_class'), '')
FROM pg_class AS relation
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
JOIN pg_index AS index ON index.indexrelid = relation.oid
JOIN pg_class AS table_relation ON table_relation.oid = index.indrelid
JOIN pg_namespace AS table_namespace ON table_namespace.oid = table_relation.relnamespace
JOIN pg_am AS access_method ON access_method.oid = relation.relam
WHERE namespace.nspname = $1 AND relation.relname = $2`, r.rawSchema, spec.name).Scan(
		&valid,
		&ready,
		&structural,
		&first,
		&expression,
		&third,
		&fourth,
		&indexExpression,
		&predicate,
		&fingerprint,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, false, false, false, nil
	}
	if err != nil {
		return false, false, false, false, false, fmt.Errorf("jobspg: inspect retention index %q: %w", spec.name, err)
	}
	matching := structural &&
		first == "namespace" &&
		third == "updated_at" &&
		fourth == "id" &&
		normalizeIndexDefinition(expression) == normalizeIndexDefinition(spec.expressionDefinition) &&
		normalizeIndexDefinition(indexExpression) == normalizeIndexDefinition(spec.expressionDefinition) &&
		normalizeIndexDefinition(predicate) == normalizeIndexDefinition(spec.predicateDefinition)
	identified := fingerprint == spec.fingerprint()
	return valid, ready, matching, identified, true, nil
}

func (r repository) validateRetentionIndexes(ctx context.Context, querier indexStateQuerier) error {
	for _, spec := range retentionIndexes {
		valid, ready, matching, _, exists, err := r.retentionIndexState(ctx, querier, spec)
		if err != nil {
			return err
		}
		if !exists || !valid || !ready || !matching {
			return fmt.Errorf("%w: retention index %q is not ready", ErrSchemaMismatch, spec.name)
		}
	}
	return nil
}

func (r repository) finalizeMigration(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := r.validateIntentKeysColumn(ctx, tx); err != nil {
		return err
	}
	if err := r.validateSchemaConstraints(ctx, tx); err != nil {
		return err
	}
	if err := r.validateOperationalIndexes(ctx, tx); err != nil {
		return err
	}
	if err := r.validateRetentionIndexes(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+r.meta+` SET version = $1 WHERE singleton = true AND version IN (1, 2, 3, 4)`, SchemaVersion); err != nil {
		return fmt.Errorf("jobspg: finalize migration: %w", err)
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM `+r.meta+` WHERE singleton = true`).Scan(&version); err != nil {
		return fmt.Errorf("jobspg: finalize migration version: %w", err)
	}
	if version != SchemaVersion {
		return fmt.Errorf("%w: have %d, need %d", ErrSchemaMismatch, version, SchemaVersion)
	}
	return tx.Commit()
}

func retentionMigrationLock(schema string) int64 {
	digest := sha256.Sum256([]byte("frostgrove.jobs.retention-migration.v1\x00" + schema))
	return int64(binary.BigEndian.Uint64(digest[:]))
}
