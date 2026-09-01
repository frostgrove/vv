package jobspg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/frostgrove/vv/jobs"
)

const DefaultListLimit = jobs.DefaultListLimit
const MaxListLimit = jobs.MaxListLimit
const MaxListOffset = jobs.MaxListOffset
const MaxListDefinitions = jobs.MaxListDefinitions
const DefaultPurgeLimit = jobs.DefaultPurgeLimit
const MaxPurgeLimit = jobs.MaxPurgeLimit

type ListSpec = jobs.ListSpec

type normalizedListSpec struct {
	definitions []jobs.Name
	states      []jobs.InvocationState
	limit       int
	offset      int
}

type redriveRecord struct {
	encoded   []byte
	size      int
	createdAt time.Time
	intent    jobs.IntentKey
	mode      jobs.PlacementMode
	view      jobs.DeliveryView
}

var _ jobs.Admin = (*Driver)(nil)

func (d *Driver) Get(ctx context.Context, id jobs.InvocationID) (jobs.DeliveryView, error) {
	if err := d.requireReady(); err != nil {
		return jobs.DeliveryView{}, err
	}
	if id.IsZero() {
		return jobs.DeliveryView{}, jobs.ErrInvalid
	}
	encoded, err := d.repo.getDeliveryRecord(ctx, d.db, d.namespace, id)
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.DeliveryView{}, jobs.ErrInvocationNotFound
	}
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	return d.deliveryView(encoded)
}

func (d *Driver) List(ctx context.Context, spec ListSpec) ([]jobs.DeliveryView, error) {
	if err := d.requireReady(); err != nil {
		return nil, err
	}
	normalized, err := normalizeListSpec(spec)
	if err != nil {
		return nil, err
	}
	records, err := d.repo.listDeliveryRecords(ctx, d.db, d.namespace, normalized)
	if err != nil {
		return nil, err
	}
	views := make([]jobs.DeliveryView, len(records))
	for index := range records {
		views[index], err = d.deliveryView(records[index])
		if err != nil {
			return nil, err
		}
	}
	return views, nil
}

func (d *Driver) Count(ctx context.Context, spec ListSpec) (int64, error) {
	if err := d.requireReady(); err != nil {
		return 0, err
	}
	normalized, err := normalizeListSpec(spec)
	if err != nil {
		return 0, err
	}
	return d.repo.countDeliveryRecords(ctx, d.db, d.namespace, normalized)
}

func (d *Driver) Redrive(ctx context.Context, id jobs.InvocationID) (jobs.DeliveryView, error) {
	if err := d.requireReady(); err != nil {
		return jobs.DeliveryView{}, err
	}
	if id.IsZero() {
		return jobs.DeliveryView{}, jobs.ErrInvalid
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	defer tx.Rollback()
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	snapshot, err := d.repo.readDeliveryRecord(ctx, tx, d.namespace, id)
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.DeliveryView{}, jobs.ErrInvocationNotFound
	}
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	preview, err := d.redriveRecord(snapshot, now)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	if err := d.repo.lockIntentKeys(ctx, tx, d.namespace, []jobs.IntentKey{preview.intent}); err != nil {
		return jobs.DeliveryView{}, err
	}
	encoded, err := d.repo.lockDeliveryRecord(ctx, tx, d.namespace, id)
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.DeliveryView{}, jobs.ErrInvocationNotFound
	}
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	redrive, err := d.redriveRecord(encoded, now)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	if redrive.mode != preview.mode || redrive.intent != preview.intent {
		return jobs.DeliveryView{}, jobs.ErrConflict
	}
	if redrive.mode == jobs.PlacementCollapse || redrive.mode == jobs.PlacementDebounce || redrive.mode == jobs.PlacementUnique {
		if err := d.repo.restoreCurrentIntent(ctx, tx, d.namespace, id, redrive.intent); err != nil {
			if errors.Is(err, errIntentConflict) {
				return jobs.DeliveryView{}, jobs.ErrConflict
			}
			return jobs.DeliveryView{}, err
		}
	}
	if err := d.repo.redriveDelivery(ctx, tx, d.namespace, id, redrive.encoded, redrive.size, redrive.createdAt); err != nil {
		return jobs.DeliveryView{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobs.DeliveryView{}, err
	}
	return redrive.view, nil
}

func (d *Driver) PurgeTerminal(ctx context.Context, before time.Time, limit int) (int, error) {
	if err := d.requireReady(); err != nil {
		return 0, err
	}
	before = before.Round(0).UTC()
	if before.IsZero() || before.Year() < 1 || before.Year() > 9999 || limit < 0 {
		return 0, jobs.ErrInvalid
	}
	if limit == 0 {
		limit = DefaultPurgeLimit
	}
	if limit > MaxPurgeLimit {
		return 0, jobs.ErrTooLarge
	}
	return d.repo.purgeTerminal(ctx, d.db, d.namespace, before, limit)
}

func (d *Driver) deliveryView(encoded []byte) (jobs.DeliveryView, error) {
	record, err := decodeRecord(encoded)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	restored, err := jobs.RestoreDeliveryRecord(d.catalog, record)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	return jobs.NewDeliveryView(restored.Invocation(), restored.Payload())
}

func (d *Driver) redriveRecord(encoded []byte, now time.Time) (redriveRecord, error) {
	record, err := decodeRecord(encoded)
	if err != nil {
		return redriveRecord{}, err
	}
	restored, err := jobs.RestoreDeliveryRecord(d.catalog, record)
	if err != nil {
		return redriveRecord{}, err
	}
	invocation, err := jobs.RedriveInvocation(restored.Invocation(), now)
	if err != nil {
		return redriveRecord{}, err
	}
	updated, err := jobs.NewDeliveryRecord(invocation, restored.Payload(), restored.WireDigest(), restored.PayloadDigest())
	if err != nil {
		return redriveRecord{}, err
	}
	encoded, err = encodeRecord(updated)
	if err != nil {
		return redriveRecord{}, err
	}
	size, err := updated.Size()
	if err != nil {
		return redriveRecord{}, err
	}
	view, err := jobs.NewDeliveryView(invocation, restored.Payload())
	if err != nil {
		return redriveRecord{}, err
	}
	return redriveRecord{encoded: encoded, size: size, createdAt: invocation.CreatedAt(), intent: invocation.Intent(), mode: invocation.Mode(), view: view}, nil
}

func normalizeListSpec(spec ListSpec) (normalizedListSpec, error) {
	if spec.Limit < 0 || spec.Offset < 0 {
		return normalizedListSpec{}, jobs.ErrInvalid
	}
	limit := spec.Limit
	if limit == 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit || spec.Offset > MaxListOffset || len(spec.Definitions) > MaxListDefinitions || len(spec.States) > int(jobs.InvocationTerminated) {
		return normalizedListSpec{}, jobs.ErrTooLarge
	}
	definitions := append([]jobs.Name(nil), spec.Definitions...)
	seenDefinitions := make(map[jobs.Name]struct{}, len(definitions))
	for _, definition := range definitions {
		if definition.IsZero() {
			return normalizedListSpec{}, jobs.ErrInvalid
		}
		if _, exists := seenDefinitions[definition]; exists {
			return normalizedListSpec{}, fmt.Errorf("%w: duplicate definition filter", jobs.ErrConflict)
		}
		seenDefinitions[definition] = struct{}{}
	}
	states := append([]jobs.InvocationState(nil), spec.States...)
	seenStates := make(map[jobs.InvocationState]struct{}, len(states))
	for _, state := range states {
		if !state.Valid() {
			return normalizedListSpec{}, jobs.ErrInvalid
		}
		if _, exists := seenStates[state]; exists {
			return normalizedListSpec{}, fmt.Errorf("%w: duplicate state filter", jobs.ErrConflict)
		}
		seenStates[state] = struct{}{}
	}
	return normalizedListSpec{definitions: definitions, states: states, limit: limit, offset: spec.Offset}, nil
}
