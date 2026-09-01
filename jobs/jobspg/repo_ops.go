package jobspg

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/frostgrove/vv/jobs"
)

type deliveryInsert struct {
	id              jobs.InvocationID
	definition      jobs.Name
	codec           jobs.CodecID
	codecVersion    jobs.SchemaVersion
	priority        int
	state           jobs.InvocationState
	availableAt     time.Time
	recordSize      int
	record          []byte
	payloadIdentity jobs.CodecID
	payloadVersion  jobs.SchemaVersion
	payloadDigest   []byte
	createdAt       time.Time
}

func (r repository) findIntent(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, keys []jobs.IntentKey) (storedDelivery, bool, error) {
	if len(keys) == 0 {
		return storedDelivery{}, false, nil
	}
	args := []any{namespaceArgument(namespace)}
	conditions := make([]string, len(keys))
	for index, key := range keys {
		scope := key.Scope().Bytes()
		digest := key.Digest().Bytes()
		base := len(args) + 1
		conditions[index] = fmt.Sprintf("(scope = $%d AND revision = $%d AND purpose = $%d AND digest = $%d)", base, base+1, base+2, base+3)
		args = append(args, scope[:], int(key.Revision()), int(key.Purpose()), digest[:])
	}
	query := `SELECT invocation_id FROM ` + r.intents + ` WHERE namespace = $1 AND (` + strings.Join(conditions, ` OR `) + `) FOR UPDATE`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return storedDelivery{}, false, err
	}
	var found jobs.InvocationID
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return storedDelivery{}, false, err
		}
		id, err := scanInvocation(raw)
		if err != nil {
			rows.Close()
			return storedDelivery{}, false, err
		}
		if !found.IsZero() && found != id {
			rows.Close()
			return storedDelivery{}, false, fmt.Errorf("%w: compatible intent revisions point to different invocations", ErrCatalogMismatch)
		}
		found = id
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return storedDelivery{}, false, err
	}
	rows.Close()
	if found.IsZero() {
		return storedDelivery{}, false, nil
	}
	stored, err := r.loadDelivery(ctx, tx, namespace, found)
	return stored, err == nil, err
}

func (r repository) loadDelivery(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID) (storedDelivery, error) {
	var rawID []byte
	var state int
	var available sql.NullTime
	var payloadIdentity sql.NullString
	var payloadVersion sql.NullInt64
	var payloadDigest []byte
	var leaseToken []byte
	var stored storedDelivery
	err := tx.QueryRowContext(ctx, `SELECT id, record, record_size, state, created_at, available_at, payload_identity, payload_version, payload_digest, lease_token
FROM `+r.deliveries+`
WHERE namespace = $1 AND id = $2
FOR UPDATE`, namespaceArgument(namespace), invocationArgument(id)).Scan(
		&rawID,
		&stored.record,
		&stored.recordSize,
		&state,
		&stored.createdAt,
		&available,
		&payloadIdentity,
		&payloadVersion,
		&payloadDigest,
		&leaseToken,
	)
	if err != nil {
		return storedDelivery{}, err
	}
	stored.id, err = scanInvocation(rawID)
	if err != nil {
		return storedDelivery{}, err
	}
	stored.state = jobs.InvocationState(state)
	if available.Valid {
		stored.availableAt = available.Time.Round(0).UTC()
	}
	if payloadIdentity.Valid {
		stored.payloadIdentity = payloadIdentity.String
	}
	if payloadVersion.Valid {
		stored.payloadVersion = jobs.SchemaVersion(payloadVersion.Int64)
	}
	stored.payloadDigest = append([]byte(nil), payloadDigest...)
	stored.leaseToken = append([]byte(nil), leaseToken...)
	stored.createdAt = stored.createdAt.Round(0).UTC()
	return stored, nil
}

func (r repository) insertDelivery(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, value deliveryInsert) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO `+r.deliveries+` (
namespace, id, definition, codec, codec_version, priority, state, available_at, record_size, record,
payload_identity, payload_version, payload_digest, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (namespace, id) DO NOTHING`,
		namespaceArgument(namespace),
		invocationArgument(value.id),
		value.definition.Value(),
		value.codec.Value(),
		int64(value.codecVersion),
		value.priority,
		int(value.state),
		value.availableAt,
		value.recordSize,
		value.record,
		nullableText(value.payloadIdentity.Value()),
		nullableInt(value.payloadVersion),
		nullableBytes(value.payloadDigest),
		value.createdAt,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errCandidateConflict
	}
	return nil
}

func (r repository) deleteDelivery(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM `+r.deliveries+` WHERE namespace = $1 AND id = $2`, namespaceArgument(namespace), invocationArgument(id))
	return err
}

func (r repository) insertIntent(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID, key jobs.IntentKey) error {
	scope := key.Scope().Bytes()
	digest := key.Digest().Bytes()
	result, err := tx.ExecContext(ctx, `INSERT INTO `+r.intents+` (namespace, scope, revision, purpose, digest, invocation_id)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (namespace, scope, revision, purpose, digest) DO NOTHING`,
		namespaceArgument(namespace), scope[:], int(key.Revision()), int(key.Purpose()), digest[:], invocationArgument(id))
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

func (r repository) ensureIntent(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID, key jobs.IntentKey) error {
	scope := key.Scope().Bytes()
	digest := key.Digest().Bytes()
	result, err := tx.ExecContext(ctx, `INSERT INTO `+r.intents+` AS existing (namespace, scope, revision, purpose, digest, invocation_id)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (namespace, scope, revision, purpose, digest)
DO UPDATE SET invocation_id = EXCLUDED.invocation_id
WHERE existing.invocation_id = EXCLUDED.invocation_id`,
		namespaceArgument(namespace), scope[:], int(key.Revision()), int(key.Purpose()), digest[:], invocationArgument(id))
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

func (r repository) updateCollapsed(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, existing storedDelivery, value deliveryInsert, availableAt, now time.Time) error {
	result, err := tx.ExecContext(ctx, `UPDATE `+r.deliveries+`
SET codec = $3,
    codec_version = $4,
    priority = LEAST(priority, $5),
    available_at = $6,
    record_size = $7,
    record = $8,
    payload_identity = $9,
    payload_version = $10,
    payload_digest = $11,
    updated_at = $12
WHERE namespace = $1 AND id = $2 AND state = $13 AND lease_token IS NULL`,
		namespaceArgument(namespace),
		invocationArgument(existing.id),
		value.codec.Value(),
		int64(value.codecVersion),
		value.priority,
		availableAt,
		value.recordSize,
		value.record,
		nullableText(value.payloadIdentity.Value()),
		nullableInt(value.payloadVersion),
		nullableBytes(value.payloadDigest),
		now,
		int(jobs.InvocationQueued),
	)
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

func (r repository) claimCandidates(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, target jobs.ClaimTarget, limit, remainingBytes int, now time.Time) ([]claimCandidate, error) {
	args := []any{
		namespaceArgument(namespace),
		int(jobs.InvocationQueued),
		target.Definition().Value(),
		now,
		remainingBytes,
		target.Binding().Value(),
		target.Build().Value(),
	}
	revisions := target.SupportedRevisions()
	conditions := make([]string, len(revisions))
	for index, revision := range revisions {
		base := len(args) + 1
		conditions[index] = fmt.Sprintf("(codec = $%d AND codec_version = $%d)", base, base+1)
		args = append(args, revision.Codec().Value(), int64(revision.Version()))
	}
	args = append(args, limit)
	limitParameter := len(args)
	query := `SELECT id, record, record_size
FROM ` + r.deliveries + `
WHERE namespace = $1
  AND state = $2
  AND definition = $3
  AND available_at <= $4
  AND record_size <= $5
  AND lease_token IS NULL
  AND (excluded_binding IS NULL OR excluded_binding <> $6 OR excluded_build <> $7)
  AND (` + strings.Join(conditions, ` OR `) + `)
ORDER BY priority, available_at, id
LIMIT $` + fmt.Sprint(limitParameter) + `
FOR UPDATE SKIP LOCKED`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]claimCandidate, 0, limit)
	for rows.Next() {
		var raw []byte
		var candidate claimCandidate
		if err := rows.Scan(&raw, &candidate.record, &candidate.recordSize); err != nil {
			return nil, err
		}
		candidate.id, err = scanInvocation(raw)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	return result, rows.Err()
}

func (r repository) claim(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID, incarnation jobs.WorkerIncarnation, token []byte, expiresAt, now time.Time) error {
	owner := incarnation.Bytes()
	result, err := tx.ExecContext(ctx, `UPDATE `+r.deliveries+`
SET lease_owner = $3,
    lease_token = $4,
    lease_epoch = lease_epoch + 1,
    lease_expires_at = $5,
    excluded_binding = NULL,
    excluded_build = NULL,
    updated_at = $6
WHERE namespace = $1 AND id = $2 AND lease_token IS NULL`,
		namespaceArgument(namespace), invocationArgument(id), owner[:], token, expiresAt, now)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("jobspg: claim lost row lock")
	}
	return nil
}

func (r repository) releaseCollapseIntent(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM `+r.intents+` WHERE namespace = $1 AND invocation_id = $2 AND purpose = $3`,
		namespaceArgument(namespace), invocationArgument(id), int(jobs.IntentCollapse))
	return err
}

func (r repository) fenceLease(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, lease jobs.LeaseRef) (bool, error) {
	var held int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM `+r.deliveries+`
WHERE namespace = $1
  AND id = $2
  AND lease_token = $3
  AND lease_expires_at > clock_timestamp()
  AND state IN ($4, $5)
FOR UPDATE`,
		namespaceArgument(namespace), invocationArgument(lease.InvocationID()), lease.DriverToken(), int(jobs.InvocationRunning), int(jobs.InvocationCancelRequested)).Scan(&held)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return held == 1, nil
}

func (r repository) renew(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, previous jobs.LeaseRef, token []byte, expiresAt, now time.Time) (jobs.InvocationState, bool, error) {
	var state int
	err := tx.QueryRowContext(ctx, `UPDATE `+r.deliveries+`
SET lease_token = $4,
    lease_epoch = lease_epoch + 1,
    lease_expires_at = $5,
    updated_at = $6
WHERE namespace = $1
  AND id = $2
  AND lease_token = $3
  AND lease_expires_at > $6
  AND state IN ($7, $8, $9)
RETURNING state`,
		namespaceArgument(namespace),
		invocationArgument(previous.InvocationID()),
		previous.DriverToken(),
		token,
		expiresAt,
		now,
		int(jobs.InvocationQueued),
		int(jobs.InvocationRunning),
		int(jobs.InvocationCancelRequested),
	).Scan(&state)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return jobs.InvocationState(state), true, nil
}

func (r repository) heldDelivery(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, lease jobs.LeaseRef, now time.Time) (storedDelivery, bool, error) {
	stored, err := r.loadDelivery(ctx, tx, namespace, lease.InvocationID())
	if err == sql.ErrNoRows {
		return storedDelivery{}, false, nil
	}
	if err != nil {
		return storedDelivery{}, false, err
	}
	if len(stored.leaseToken) == 0 || !equalBytes(stored.leaseToken, lease.DriverToken()) {
		return storedDelivery{}, false, nil
	}
	var expiresAt sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT lease_expires_at FROM `+r.deliveries+` WHERE namespace = $1 AND id = $2`, namespaceArgument(namespace), invocationArgument(lease.InvocationID())).Scan(&expiresAt)
	if err != nil {
		return storedDelivery{}, false, err
	}
	if !expiresAt.Valid || !expiresAt.Time.After(now) {
		return storedDelivery{}, false, nil
	}
	return stored, true, nil
}

func (r repository) saveApplication(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, lease jobs.LeaseRef, state jobs.InvocationState, availableAt any, record []byte, recordSize int, clearLease bool, excludedBinding, excludedBuild string, now time.Time) error {
	leaseOwner := `lease_owner`
	leaseToken := `lease_token`
	leaseExpiresAt := `lease_expires_at`
	if clearLease {
		leaseOwner = `NULL`
		leaseToken = `NULL`
		leaseExpiresAt = `NULL`
	}
	query := `UPDATE ` + r.deliveries + `
SET state = $4,
    available_at = $5,
    record = $6,
    record_size = $7,
    lease_owner = ` + leaseOwner + `,
    lease_token = ` + leaseToken + `,
    lease_expires_at = ` + leaseExpiresAt + `,
    excluded_binding = $8,
    excluded_build = $9,
    updated_at = $10
WHERE namespace = $1 AND id = $2 AND lease_token = $3`
	result, err := tx.ExecContext(ctx, query,
		namespaceArgument(namespace),
		invocationArgument(lease.InvocationID()),
		lease.DriverToken(),
		int(state),
		availableAt,
		record,
		recordSize,
		nullableText(excludedBinding),
		nullableText(excludedBuild),
		now,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return jobs.ErrLeaseLost
	}
	return nil
}

func (r repository) rejectCorrupt(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, lease jobs.LeaseRef, now time.Time) error {
	result, err := tx.ExecContext(ctx, `UPDATE `+r.deliveries+`
SET state = $4,
    available_at = NULL,
    lease_owner = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    excluded_binding = NULL,
    excluded_build = NULL,
    updated_at = $5
WHERE namespace = $1 AND id = $2 AND lease_token = $3`,
		namespaceArgument(namespace), invocationArgument(lease.InvocationID()), lease.DriverToken(), int(jobs.InvocationQuarantined), now)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return jobs.ErrLeaseLost
	}
	return nil
}

func (r repository) expired(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, maxItems int, now time.Time) ([]expiredCandidate, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, record, record_size
FROM `+r.deliveries+`
WHERE namespace = $1
  AND lease_token IS NOT NULL
  AND lease_expires_at <= $2
  AND state IN ($3, $4, $5)
ORDER BY lease_expires_at, id
LIMIT $6
FOR UPDATE SKIP LOCKED`,
		namespaceArgument(namespace),
		now,
		int(jobs.InvocationQueued),
		int(jobs.InvocationRunning),
		int(jobs.InvocationCancelRequested),
		maxItems+1,
	)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]expiredCandidate, 0, maxItems+1)
	for rows.Next() {
		var raw []byte
		var candidate expiredCandidate
		if err := rows.Scan(&raw, &candidate.record, &candidate.recordSize); err != nil {
			return nil, false, err
		}
		candidate.id, err = scanInvocation(raw)
		if err != nil {
			return nil, false, err
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(result) > maxItems
	if more {
		result = result[:maxItems]
	}
	return result, more, nil
}

func (r repository) recover(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID, incarnation jobs.WorkerIncarnation, token []byte, expiresAt, now time.Time) error {
	owner := incarnation.Bytes()
	result, err := tx.ExecContext(ctx, `UPDATE `+r.deliveries+`
SET lease_owner = $3,
    lease_token = $4,
    lease_epoch = lease_epoch + 1,
    lease_expires_at = $5,
    updated_at = $6
WHERE namespace = $1 AND id = $2 AND lease_token IS NOT NULL AND lease_expires_at <= $6`,
		namespaceArgument(namespace), invocationArgument(id), owner[:], token, expiresAt, now)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return jobs.ErrLeaseLost
	}
	return nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for index := range left {
		different |= left[index] ^ right[index]
	}
	return different == 0
}
