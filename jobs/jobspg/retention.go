package jobspg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/frostgrove/vv/jobs"
)

func (d *Driver) SweepTerminalRetention(ctx context.Context, limit int) (int, error) {
	if err := d.requireReady(); err != nil {
		return 0, err
	}
	if limit < 0 {
		return 0, jobs.ErrInvalid
	}
	if limit == 0 {
		limit = DefaultPurgeLimit
	}
	if limit > MaxPurgeLimit {
		return 0, jobs.ErrTooLarge
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	leader, err := d.repo.tryRetentionLeadership(ctx, tx, d.namespace)
	if err != nil || !leader {
		return 0, err
	}
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return 0, err
	}
	ids, err := d.repo.retentionCandidates(ctx, tx, d.namespace, now, limit)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	keys, err := d.repo.intentKeysForDeliveries(ctx, tx, d.namespace, ids)
	if err != nil {
		return 0, err
	}
	if err := d.repo.lockIntentKeys(ctx, tx, d.namespace, keys); err != nil {
		return 0, err
	}
	if err := d.repo.lockIntentRowsForDeliveries(ctx, tx, d.namespace, ids); err != nil {
		return 0, err
	}
	candidates, err := d.repo.lockRetentionCandidates(ctx, tx, d.namespace, ids)
	if err != nil {
		return 0, err
	}
	once, err := d.repo.onceIntentDeliveries(ctx, tx, d.namespace, ids)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, candidate := range candidates {
		rowChanged, err := d.sweepRetentionCandidate(ctx, tx, candidate, once[candidate.id], now)
		if err != nil {
			return 0, err
		}
		if rowChanged {
			changed++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return changed, nil
}

func (d *Driver) sweepRetentionCandidate(ctx context.Context, tx *sql.Tx, candidate retentionCandidate, once bool, now time.Time) (bool, error) {
	recordExpiresAt := nullableUTC(candidate.recordExpiresAt)
	intentExpiresAt := nullableUTC(candidate.intentExpiresAt)
	changed := false
	if recordExpiresAt.IsZero() || intentExpiresAt.IsZero() {
		var record []byte
		if candidate.hasRecord {
			var err error
			record, err = d.repo.retentionRecord(ctx, tx, d.namespace, candidate.id)
			if err != nil {
				return false, err
			}
		}
		retention, intentRetention := d.retentionDurations(candidate.definition, record)
		computedRecordExpiry, computedIntentExpiry, err := terminalRetentionDeadlines(candidate.updatedAt, retention, intentRetention)
		if err != nil {
			return false, err
		}
		if recordExpiresAt.IsZero() {
			recordExpiresAt = computedRecordExpiry
		}
		if intentExpiresAt.IsZero() {
			intentExpiresAt = computedIntentExpiry
		}
		if err := d.repo.setRetentionDeadlines(ctx, tx, d.namespace, candidate.id, recordExpiresAt, intentExpiresAt); err != nil {
			return false, err
		}
		changed = true
	}
	if candidate.hasRecord && now.Before(recordExpiresAt) {
		return changed, nil
	}
	if candidate.hasRecord && once && now.Before(intentExpiresAt) {
		if err := d.repo.tombstoneTerminal(ctx, tx, d.namespace, candidate.id); err != nil {
			return false, err
		}
		return true, nil
	}
	if !candidate.hasRecord && once && now.Before(intentExpiresAt) {
		return changed, nil
	}
	if err := d.repo.deleteTerminal(ctx, tx, d.namespace, candidate.id); err != nil {
		return false, err
	}
	return true, nil
}

func (d *Driver) retentionDurations(definition jobs.Name, encoded []byte) (time.Duration, time.Duration) {
	if len(encoded) != 0 {
		if record, err := decodeRecord(encoded); err == nil {
			return record.Genesis.Policy.Retention(), record.Genesis.Policy.IntentRetention()
		}
	}
	if declaration, ok := d.catalog.Lookup(definition); ok {
		policy := declaration.Describe().Policy
		return policy.Retention, policy.IntentRetention
	}
	return jobs.DefaultTerminalRetention, jobs.DefaultIntentRetention
}

func retainedUntil(base time.Time, retention time.Duration) (time.Time, error) {
	value := base.Add(retention).Round(0).UTC()
	if base.IsZero() || retention <= 0 || retention > jobs.MaxRetention || value.Year() < 1 || value.Year() > 9999 {
		return time.Time{}, fmt.Errorf("jobspg: %w: retention deadline", jobs.ErrInvalid)
	}
	return value, nil
}

func nullableUTC(value sql.NullTime) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.Round(0).UTC()
}

func terminalRetentionDeadlines(at time.Time, retention, intentRetention time.Duration) (time.Time, time.Time, error) {
	recordExpiresAt, err := retainedUntil(at, retention)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	intentExpiresAt, err := retainedUntil(at, intentRetention)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if intentExpiresAt.Before(recordExpiresAt) {
		return time.Time{}, time.Time{}, errors.New("jobspg: invalid retention order")
	}
	return recordExpiresAt, intentExpiresAt, nil
}
