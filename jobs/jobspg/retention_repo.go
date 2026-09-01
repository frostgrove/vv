package jobspg

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"github.com/frostgrove/vv/jobs"
)

type retentionCandidate struct {
	id              jobs.InvocationID
	definition      jobs.Name
	hasRecord       bool
	updatedAt       time.Time
	recordExpiresAt sql.NullTime
	intentExpiresAt sql.NullTime
}

const terminalStatesSQL = "3, 4, 5, 6, 7, 9, 10"

func (r repository) tryRetentionLeadership(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace) (bool, error) {
	var acquired bool
	err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, retentionAdvisoryLock(namespace)).Scan(&acquired)
	return acquired, err
}

func retentionAdvisoryLock(namespace jobs.Namespace) int64 {
	hash := sha256.New()
	_, _ = hash.Write([]byte("frostgrove.jobs.retention-lock.v1"))
	digest := namespace.Digest()
	_, _ = hash.Write(digest[:])
	return int64(binary.BigEndian.Uint64(hash.Sum(nil)))
}

func (r repository) retentionCandidates(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, now time.Time, limit int) ([]jobs.InvocationID, error) {
	rows, err := tx.QueryContext(ctx, r.retentionCandidatesQuery(), namespaceArgument(namespace), now, limit)
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

func (r repository) retentionCandidatesQuery() string {
	return `SELECT id
FROM ` + r.deliveries + `
WHERE namespace = $1
  AND state IN (` + terminalStatesSQL + `)
  AND COALESCE(CASE WHEN record IS NULL THEN intent_expires_at ELSE record_expires_at END, '-infinity'::timestamptz) <= $2
ORDER BY
  COALESCE(CASE WHEN record IS NULL THEN intent_expires_at ELSE record_expires_at END, '-infinity'::timestamptz),
  updated_at,
  id
LIMIT $3`
}

func (r repository) lockRetentionCandidates(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, ids []jobs.InvocationID) ([]retentionCandidate, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := []any{namespaceArgument(namespace)}
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
	rows, err := tx.QueryContext(ctx, `SELECT id,
       definition,
       record IS NOT NULL,
       updated_at,
       record_expires_at,
       intent_expires_at
FROM `+r.deliveries+`
WHERE namespace = $1
  AND id IN (`+strings.Join(placeholders, `, `)+`)
  AND state IN (`+fmt.Sprintf(`$%d, $%d, $%d, $%d, $%d, $%d, $%d`, state, state+1, state+2, state+3, state+4, state+5, state+6)+`)
ORDER BY id
FOR UPDATE SKIP LOCKED`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := make([]retentionCandidate, 0, len(ids))
	for rows.Next() {
		candidate, err := scanRetentionCandidate(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (r repository) lockTerminalDeliveries(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, ids []jobs.InvocationID) ([]jobs.InvocationID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := []any{namespaceArgument(namespace)}
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
	rows, err := tx.QueryContext(ctx, `SELECT id
FROM `+r.deliveries+`
WHERE namespace = $1
  AND id IN (`+strings.Join(placeholders, `, `)+`)
  AND state IN (`+fmt.Sprintf(`$%d, $%d, $%d, $%d, $%d, $%d, $%d`, state, state+1, state+2, state+3, state+4, state+5, state+6)+`)
ORDER BY id
FOR UPDATE`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	locked := make([]jobs.InvocationID, 0, len(ids))
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		id, err := scanInvocation(raw)
		if err != nil {
			return nil, err
		}
		locked = append(locked, id)
	}
	return locked, rows.Err()
}

func scanRetentionCandidate(scanner interface{ Scan(...any) error }) (retentionCandidate, error) {
	var rawID []byte
	var rawDefinition string
	var candidate retentionCandidate
	if err := scanner.Scan(&rawID, &rawDefinition, &candidate.hasRecord, &candidate.updatedAt, &candidate.recordExpiresAt, &candidate.intentExpiresAt); err != nil {
		return retentionCandidate{}, err
	}
	var err error
	candidate.id, err = scanInvocation(rawID)
	if err != nil {
		return retentionCandidate{}, err
	}
	candidate.definition, err = jobs.ParseName(rawDefinition)
	if err != nil {
		return retentionCandidate{}, err
	}
	candidate.updatedAt = candidate.updatedAt.Round(0).UTC()
	return candidate, nil
}

func (r repository) retentionRecord(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID) ([]byte, error) {
	var record []byte
	err := tx.QueryRowContext(ctx, `SELECT record FROM `+r.deliveries+` WHERE namespace = $1 AND id = $2 AND record IS NOT NULL`, namespaceArgument(namespace), invocationArgument(id)).Scan(&record)
	return record, err
}

func (r repository) onceIntentDeliveries(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, ids []jobs.InvocationID) (map[jobs.InvocationID]bool, error) {
	result := make(map[jobs.InvocationID]bool)
	if len(ids) == 0 {
		return result, nil
	}
	args := []any{namespaceArgument(namespace), int(jobs.IntentOnce)}
	placeholders := appendInvocationArguments(&args, ids)
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT invocation_id
FROM `+r.intents+`
WHERE namespace = $1
  AND purpose = $2
  AND invocation_id IN (`+strings.Join(placeholders, `, `)+`)
ORDER BY invocation_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		id, err := scanInvocation(raw)
		if err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

func (r repository) setRetentionDeadlines(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID, recordExpiresAt, intentExpiresAt time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE `+r.deliveries+`
SET record_expires_at = COALESCE(record_expires_at, $3),
    intent_expires_at = COALESCE(intent_expires_at, $4)
WHERE namespace = $1 AND id = $2`, namespaceArgument(namespace), invocationArgument(id), recordExpiresAt, intentExpiresAt)
	return err
}

func (r repository) tombstoneTerminal(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID) error {
	_, err := tx.ExecContext(ctx, `UPDATE `+r.deliveries+`
SET record = NULL,
    record_size = NULL
WHERE namespace = $1 AND id = $2 AND record IS NOT NULL`, namespaceArgument(namespace), invocationArgument(id))
	return err
}

func (r repository) deleteTerminal(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM `+r.deliveries+` WHERE namespace = $1 AND id = $2`, namespaceArgument(namespace), invocationArgument(id))
	return err
}
