package jobspg

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/frostgrove/vv/jobs"
)

type deliveryRecordQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type adminDeliveryRecord struct {
	record     []byte
	intentKeys []jobs.IntentKey
}

func (r repository) getDeliveryRecord(ctx context.Context, db *sql.DB, namespace jobs.Namespace, id jobs.InvocationID) ([]byte, error) {
	stored, err := r.readDeliveryRecord(ctx, db, namespace, id)
	return stored.record, err
}

func (r repository) readDeliveryRecord(ctx context.Context, querier deliveryRecordQuerier, namespace jobs.Namespace, id jobs.InvocationID) (adminDeliveryRecord, error) {
	var stored adminDeliveryRecord
	var encodedIntentKeys []byte
	err := querier.QueryRowContext(ctx, `SELECT record, intent_keys FROM `+r.deliveries+` WHERE namespace = $1 AND id = $2 AND record IS NOT NULL`, namespaceArgument(namespace), invocationArgument(id)).Scan(&stored.record, &encodedIntentKeys)
	if err != nil {
		return adminDeliveryRecord{}, err
	}
	stored.intentKeys, err = decodeIntentKeys(encodedIntentKeys)
	if err != nil {
		return adminDeliveryRecord{}, err
	}
	return stored, nil
}

func (r repository) lockDeliveryRecord(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID) (adminDeliveryRecord, error) {
	var stored adminDeliveryRecord
	var encodedIntentKeys []byte
	err := tx.QueryRowContext(ctx, `SELECT record, intent_keys FROM `+r.deliveries+` WHERE namespace = $1 AND id = $2 AND record IS NOT NULL FOR UPDATE`, namespaceArgument(namespace), invocationArgument(id)).Scan(&stored.record, &encodedIntentKeys)
	if err != nil {
		return adminDeliveryRecord{}, err
	}
	stored.intentKeys, err = decodeIntentKeys(encodedIntentKeys)
	if err != nil {
		return adminDeliveryRecord{}, err
	}
	return stored, nil
}

func (r repository) listDeliveryRecords(ctx context.Context, db *sql.DB, namespace jobs.Namespace, spec normalizedListSpec) ([][]byte, error) {
	args := []any{namespaceArgument(namespace)}
	conditions := []string{"namespace = $1", "record IS NOT NULL"}
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

func (r repository) countDeliveryRecords(ctx context.Context, db *sql.DB, namespace jobs.Namespace, spec normalizedListSpec) (int64, error) {
	args := []any{namespaceArgument(namespace)}
	conditions := []string{"namespace = $1", "record IS NOT NULL"}
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
	var count int64
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+r.deliveries+` WHERE `+strings.Join(conditions, " AND "), args...).Scan(&count)
	return count, err
}

func (r repository) restoreIntents(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID, keys []jobs.IntentKey) error {
	if len(keys) == 0 || len(keys) > jobs.MaxIntentDigestKeys {
		return jobs.ErrInvalid
	}
	for _, key := range keys {
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
    record_expires_at = NULL,
    intent_expires_at = NULL,
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
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	ids, err := r.terminalCandidates(ctx, tx, namespace, before, limit)
	if err != nil {
		return 0, err
	}
	keys, err := r.intentKeysForDeliveries(ctx, tx, namespace, ids)
	if err != nil {
		return 0, err
	}
	if err := r.lockIntentKeys(ctx, tx, namespace, keys); err != nil {
		return 0, err
	}
	if err := r.lockIntentRowsForDeliveries(ctx, tx, namespace, ids); err != nil {
		return 0, err
	}
	ids, err = r.lockTerminalDeliveries(ctx, tx, namespace, ids)
	if err != nil {
		return 0, err
	}
	rows, err := r.deleteTerminalCandidates(ctx, tx, namespace, before, ids)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return rows, nil
}

func (r repository) terminalCandidates(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, before time.Time, limit int) ([]jobs.InvocationID, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id
FROM `+r.deliveries+`
WHERE namespace = $1
  AND updated_at < $2
  AND state IN ($3, $4, $5, $6, $7, $8, $9)
ORDER BY updated_at, id
LIMIT $10`,
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
		return nil, err
	}
	defer rows.Close()
	ids := make([]jobs.InvocationID, 0, limit)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		id, err := scanInvocation(raw)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r repository) intentKeysForDeliveries(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, ids []jobs.InvocationID) ([]jobs.IntentKey, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := []any{namespaceArgument(namespace)}
	placeholders := appendInvocationArguments(&args, ids)
	rows, err := tx.QueryContext(ctx, `SELECT scope, revision, purpose, digest
FROM `+r.intents+`
WHERE namespace = $1 AND invocation_id IN (`+strings.Join(placeholders, `, `)+`)
ORDER BY scope, revision, purpose, digest`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]jobs.IntentKey, 0, len(ids))
	for rows.Next() {
		var scope, digest []byte
		var revision, purpose int
		if err := rows.Scan(&scope, &revision, &purpose, &digest); err != nil {
			return nil, err
		}
		key, err := scanIntentKey(scope, revision, purpose, digest)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (r repository) lockIntentRowsForDeliveries(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, ids []jobs.InvocationID) error {
	if len(ids) == 0 {
		return nil
	}
	args := []any{namespaceArgument(namespace)}
	placeholders := appendInvocationArguments(&args, ids)
	rows, err := tx.QueryContext(ctx, `SELECT purpose
FROM `+r.intents+`
WHERE namespace = $1 AND invocation_id IN (`+strings.Join(placeholders, `, `)+`)
ORDER BY scope, revision, purpose, digest
FOR UPDATE`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var purpose int
		if err := rows.Scan(&purpose); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (r repository) deleteTerminalCandidates(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, before time.Time, ids []jobs.InvocationID) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := []any{namespaceArgument(namespace), before}
	placeholders := appendInvocationArguments(&args, ids)
	args = append(args,
		int(jobs.InvocationSucceeded),
		int(jobs.InvocationFailed),
		int(jobs.InvocationDead),
		int(jobs.InvocationDiscarded),
		int(jobs.InvocationQuarantined),
		int(jobs.InvocationCancelled),
		int(jobs.InvocationTerminated),
	)
	state := len(args) - 6
	result, err := tx.ExecContext(ctx, `DELETE FROM `+r.deliveries+`
WHERE namespace = $1
  AND updated_at < $2
  AND id IN (`+strings.Join(placeholders, `, `)+`)
  AND state IN (`+fmt.Sprintf(`$%d, $%d, $%d, $%d, $%d, $%d, $%d`, state, state+1, state+2, state+3, state+4, state+5, state+6)+`)`, args...)
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	return int(rows), err
}

func appendInvocationArguments(args *[]any, ids []jobs.InvocationID) []string {
	placeholders := make([]string, len(ids))
	for index, id := range ids {
		*args = append(*args, invocationArgument(id))
		placeholders[index] = fmt.Sprintf("$%d", len(*args))
	}
	return placeholders
}
