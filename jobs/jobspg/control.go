package jobspg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/frostgrove/vv/jobs"
)

const controlLockAttempts = jobs.MaxIntentDigestKeys + 1

type controlTransition uint8

const (
	controlCancel controlTransition = iota + 1
	controlTerminate
)

type controlledDelivery struct {
	record          []byte
	recordSize      int
	state           jobs.InvocationState
	recordExpiresAt any
	intentExpiresAt any
	view            jobs.DeliveryView
}

var _ jobs.Controller = (*Driver)(nil)
var _ jobs.Operations = (*Driver)(nil)

func (d *Driver) Cancel(ctx context.Context, id jobs.InvocationID) (jobs.DeliveryView, error) {
	return d.control(ctx, id, controlCancel)
}

func (d *Driver) Terminate(ctx context.Context, id jobs.InvocationID) (jobs.DeliveryView, error) {
	return d.control(ctx, id, controlTerminate)
}

func (d *Driver) control(ctx context.Context, id jobs.InvocationID, transition controlTransition) (jobs.DeliveryView, error) {
	if err := d.requireReady(); err != nil {
		return jobs.DeliveryView{}, err
	}
	if ctx == nil || id.IsZero() || transition != controlCancel && transition != controlTerminate {
		return jobs.DeliveryView{}, jobs.ErrInvalid
	}
	for attempt := 0; attempt < controlLockAttempts; attempt++ {
		view, retry, err := d.controlAttempt(ctx, id, transition)
		if !retry || err != nil {
			return view, err
		}
	}
	return jobs.DeliveryView{}, jobs.ErrConflict
}

func (d *Driver) controlAttempt(ctx context.Context, id jobs.InvocationID, transition controlTransition) (jobs.DeliveryView, bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return jobs.DeliveryView{}, false, err
	}
	defer tx.Rollback()
	preview, err := d.repo.readDeliveryRecord(ctx, tx, d.namespace, id)
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.DeliveryView{}, false, jobs.ErrInvocationNotFound
	}
	if err != nil {
		return jobs.DeliveryView{}, false, err
	}
	previewIntents, err := d.controlIntentKeys(id, preview)
	if err != nil {
		return jobs.DeliveryView{}, false, err
	}
	if err := d.repo.lockIntentKeys(ctx, tx, d.namespace, previewIntents); err != nil {
		return jobs.DeliveryView{}, false, err
	}
	if err := d.repo.lockDeliveryIntents(ctx, tx, d.namespace, id); err != nil {
		return jobs.DeliveryView{}, false, err
	}
	stored, err := d.repo.loadDelivery(ctx, tx, d.namespace, id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && len(stored.record) == 0 {
		return jobs.DeliveryView{}, false, jobs.ErrInvocationNotFound
	}
	if err != nil {
		return jobs.DeliveryView{}, false, err
	}
	lockedIntents, err := d.controlIntentKeys(id, adminDeliveryRecord{record: stored.record, intentKeys: stored.intentKeys})
	if err != nil {
		return jobs.DeliveryView{}, false, err
	}
	if !slices.Equal(previewIntents, lockedIntents) {
		return jobs.DeliveryView{}, true, nil
	}
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return jobs.DeliveryView{}, false, err
	}
	controlled, err := d.controlDelivery(stored.record, stored.state, transition, now)
	if err != nil {
		return jobs.DeliveryView{}, false, err
	}
	clearLease := controlled.state.Terminal()
	if err := d.repo.controlDelivery(ctx, tx, d.namespace, id, controlled.state, controlled.record, controlled.recordSize, clearLease, controlled.recordExpiresAt, controlled.intentExpiresAt, now); err != nil {
		return jobs.DeliveryView{}, false, err
	}
	if clearLease {
		if err := d.repo.releaseCollapseIntent(ctx, tx, d.namespace, id); err != nil {
			return jobs.DeliveryView{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return jobs.DeliveryView{}, false, err
	}
	return controlled.view, false, nil
}

func (d *Driver) controlIntentKeys(id jobs.InvocationID, stored adminDeliveryRecord) ([]jobs.IntentKey, error) {
	record, err := decodeRecord(stored.record)
	if err != nil {
		return nil, err
	}
	restored, err := jobs.RestoreDeliveryRecord(d.catalog, record)
	if err != nil {
		return nil, err
	}
	invocation := restored.Invocation()
	if invocation.ID() != id || invocation.Namespace().Digest() != d.namespace.Digest() {
		return nil, fmt.Errorf("jobspg: %w: delivery identity differs from record", jobs.ErrCorrupt)
	}
	return validateInvocationIntentKeys(invocation, stored.intentKeys)
}

func (d *Driver) controlDelivery(encoded []byte, storedState jobs.InvocationState, transition controlTransition, now time.Time) (controlledDelivery, error) {
	record, err := decodeRecord(encoded)
	if err != nil {
		return controlledDelivery{}, err
	}
	restored, err := jobs.RestoreDeliveryRecord(d.catalog, record)
	if err != nil {
		return controlledDelivery{}, err
	}
	if restored.Invocation().State() != storedState {
		return controlledDelivery{}, fmt.Errorf("jobspg: %w: delivery state differs from record", jobs.ErrCorrupt)
	}
	invocation := jobs.Invocation{}
	switch transition {
	case controlCancel:
		invocation, err = restored.Invocation().RequestCancel(now)
	case controlTerminate:
		invocation, err = restored.Invocation().Terminate(now)
	default:
		err = jobs.ErrInvalid
	}
	if err != nil {
		return controlledDelivery{}, err
	}
	updated, err := jobs.NewDeliveryRecord(invocation, restored.Payload(), restored.WireDigest(), restored.PayloadDigest())
	if err != nil {
		return controlledDelivery{}, err
	}
	encoded, err = encodeRecord(updated)
	if err != nil {
		return controlledDelivery{}, err
	}
	size, err := updated.Size()
	if err != nil {
		return controlledDelivery{}, err
	}
	view, err := jobs.NewDeliveryView(invocation, restored.Payload())
	if err != nil {
		return controlledDelivery{}, err
	}
	controlled := controlledDelivery{record: encoded, recordSize: size, state: invocation.State(), view: view}
	if invocation.IsTerminal() {
		recordExpiresAt, intentExpiresAt, deadlineErr := terminalRetentionDeadlines(now, invocation.Policy().Retention(), invocation.Policy().IntentRetention())
		if deadlineErr != nil {
			return controlledDelivery{}, deadlineErr
		}
		controlled.recordExpiresAt = recordExpiresAt
		controlled.intentExpiresAt = intentExpiresAt
	}
	return controlled, nil
}

func deliveryControl(state jobs.InvocationState) jobs.DeliveryControlStatus {
	switch state {
	case jobs.InvocationCancelRequested, jobs.InvocationCancelled:
		return jobs.DeliveryControlCancelRequested
	case jobs.InvocationTerminated:
		return jobs.DeliveryControlTerminated
	default:
		return jobs.DeliveryControlNone
	}
}
