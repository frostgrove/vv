package jobspg

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/frostgrove/vv/jobs"
)

func (r repository) getDeliveryRecord(ctx context.Context, db *sql.DB, namespace jobs.Namespace, id jobs.InvocationID) ([]byte, error) {
	var record []byte
	err := db.QueryRowContext(ctx, `SELECT record FROM `+r.deliveries+` WHERE namespace = $1 AND id = $2`, namespaceArgument(namespace), invocationArgument(id)).Scan(&record)
	return record, err
}

func (r repository) lockDeliveryRecord(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID) ([]byte, error) {
	var record []byte
	err := tx.QueryRowContext(ctx, `SELECT record FROM `+r.deliveries+` WHERE namespace = $1 AND id = $2 FOR UPDATE`, namespaceArgument(namespace), invocationArgument(id)).Scan(&record)
	return record, err
}

func (r repository) listDeliveryRecords(ctx context.Context, db *sql.DB, namespace jobs.Namespace, spec normalizedListSpec) ([][]byte, error) {
	args := []any{namespaceArgument(namespace)}
	conditions := []string{"namespace = $1"}
	if len(spec.definitions) != 0 {
		placeholders := make([]string, len(spec.definitions))
		for index, definition := range spec.definitions {
			args = append(args, definition.Value())
			placeholders[index] = fmt.Sprintf("$%d", len(args))
		}
		conditions = append(conditions, "definition IN ("+strings.Join(placeholders, ", ")+")")
	}
	if len(spec.states) != 0 {
		placeholders := make([]string, len(spec.states))
		for index, state := range spec.states {
			args = append(args, int(state))
			placeholders[index] = fmt.Sprintf("$%d", len(args))
		}
		conditions = append(conditions, "state IN ("+strings.Join(placeholders, ", ")+")")
	}
	args = append(args, spec.limit, spec.offset)
	query := `SELECT record FROM ` + r.deliveries + ` WHERE ` + strings.Join(conditions, " AND ") + ` ORDER BY created_at DESC, id DESC LIMIT $` + fmt.Sprint(len(args)-1) + ` OFFSET $` + fmt.Sprint(len(args))
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([][]byte, 0, spec.limit)
	for rows.Next() {
		var record []byte
		if err := rows.Scan(&record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (r repository) restoreCurrentIntent(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID, key jobs.IntentKey) error {
	scope := key.Scope().Bytes()
	digest := key.Digest().Bytes()
	result, err := tx.ExecContext(ctx, `INSERT INTO `+r.intents+` AS current (namespace, scope, revision, purpose, digest, invocation_id)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (namespace, scope, revision, purpose, digest)
DO UPDATE SET invocation_id = EXCLUDED.invocation_id
WHERE current.invocation_id = EXCLUDED.invocation_id`, namespaceArgument(namespace), scope[:], int(key.Revision()), int(key.Purpose()), digest[:], invocationArgument(id))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errIntentConflict
	}
	return nil
}

func (r repository) redriveDelivery(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID, record []byte, recordSize int, now time.Time) error {
	result, err := tx.ExecContext(ctx, `UPDATE `+r.deliveries+`
SET state = $3,
    available_at = $4,
    record_size = $5,
    record = $6,
    lease_owner = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    excluded_binding = NULL,
    excluded_build = NULL,
    created_at = $4,
    updated_at = $4
WHERE namespace = $1
  AND id = $2
  AND state IN ($7, $8, $9, $10, $11, $12, $13)`,
		namespaceArgument(namespace),
		invocationArgument(id),
		int(jobs.InvocationQueued),
		now,
		recordSize,
		record,
		int(jobs.InvocationSucceeded),
		int(jobs.InvocationFailed),
		int(jobs.InvocationDead),
		int(jobs.InvocationDiscarded),
		int(jobs.InvocationQuarantined),
		int(jobs.InvocationCancelled),
		int(jobs.InvocationTerminated),
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return jobs.ErrConflict
	}
	return nil
}

func (r repository) purgeTerminal(ctx context.Context, db *sql.DB, namespace jobs.Namespace, before time.Time, limit int) (int, error) {
	result, err := db.ExecContext(ctx, `WITH doomed AS (
    SELECT namespace, id
    FROM `+r.deliveries+`
    WHERE namespace = $1
      AND updated_at < $2
      AND state IN ($3, $4, $5, $6, $7, $8, $9)
    ORDER BY updated_at, id
    LIMIT $10
    FOR UPDATE SKIP LOCKED
)
DELETE FROM `+r.deliveries+` AS deliveries
USING doomed
WHERE deliveries.namespace = doomed.namespace
  AND deliveries.id = doomed.id`,
		namespaceArgument(namespace),
		before,
		int(jobs.InvocationSucceeded),
		int(jobs.InvocationFailed),
		int(jobs.InvocationDead),
		int(jobs.InvocationDiscarded),
		int(jobs.InvocationQuarantined),
		int(jobs.InvocationCancelled),
		int(jobs.InvocationTerminated),
		limit,
	)
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(rows), nil
}
