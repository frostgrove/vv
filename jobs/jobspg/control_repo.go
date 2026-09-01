package jobspg

import (
	"context"
	"database/sql"
	"time"

	"github.com/frostgrove/vv/jobs"
)

func (r repository) controlDelivery(ctx context.Context, tx *sql.Tx, namespace jobs.Namespace, id jobs.InvocationID, state jobs.InvocationState, record []byte, recordSize int, clearLease bool, recordExpiresAt, intentExpiresAt any, now time.Time) error {
	leaseOwner := `lease_owner`
	leaseToken := `lease_token`
	leaseExpiresAt := `lease_expires_at`
	leaseEpoch := `lease_epoch`
	if clearLease {
		leaseOwner = `NULL`
		leaseToken = `NULL`
		leaseExpiresAt = `NULL`
		leaseEpoch = `lease_epoch + 1`
	}
	result, err := tx.ExecContext(ctx, `UPDATE `+r.deliveries+`
SET state = $3,
    available_at = NULL,
    record = $4,
    record_size = $5,
    lease_owner = `+leaseOwner+`,
    lease_token = `+leaseToken+`,
    lease_epoch = `+leaseEpoch+`,
    lease_expires_at = `+leaseExpiresAt+`,
    excluded_binding = NULL,
    excluded_build = NULL,
    record_expires_at = $6,
    intent_expires_at = $7,
    updated_at = $8
WHERE namespace = $1 AND id = $2 AND record IS NOT NULL`,
		namespaceArgument(namespace),
		invocationArgument(id),
		int(state),
		record,
		recordSize,
		recordExpiresAt,
		intentExpiresAt,
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
		return jobs.ErrConflict
	}
	return nil
}
