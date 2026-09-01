package jobsredis

import (
	"context"
	"fmt"
	"time"

	"github.com/frostgrove/vv/jobs"
)

type invocationTransition func(jobs.Invocation, time.Time) (jobs.Invocation, error)

func (d *Driver) Cancel(ctx context.Context, id jobs.InvocationID) (jobs.DeliveryView, error) {
	return d.control(ctx, id, jobs.Invocation.RequestCancel)
}

func (d *Driver) Terminate(ctx context.Context, id jobs.InvocationID) (jobs.DeliveryView, error) {
	return d.control(ctx, id, jobs.Invocation.Terminate)
}

func (d *Driver) control(ctx context.Context, id jobs.InvocationID, transition invocationTransition) (jobs.DeliveryView, error) {
	if err := d.requireReady(); err != nil {
		return jobs.DeliveryView{}, err
	}
	if id.IsZero() {
		return jobs.DeliveryView{}, jobs.ErrInvalid
	}
	opCtx, cancel, unlock, now, err := d.begin(ctx)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	defer cancel()
	defer unlock()
	entry, found, err := d.repo.entry(opCtx, id.String())
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	if !found {
		return jobs.DeliveryView{}, jobs.ErrInvocationNotFound
	}
	record, err := decodeRecord(entry.Record)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	invocation, canonical, err := restoreRecord(record)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	if invocation.ID() != id || invocation.Namespace().Digest() != d.namespace.Digest() || invocation.State() != entry.State {
		return jobs.DeliveryView{}, fmt.Errorf("jobsredis: %w: delivery metadata", jobs.ErrCorrupt)
	}
	updatedInvocation, err := transition(invocation, now)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	updatedRecord, err := recordFromInvocation(updatedInvocation, canonical)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	encoded, err := encodeRecord(updatedRecord)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	size, err := updatedRecord.Size()
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	payload, err := jobs.NewEncodedPayload(updatedRecord.Payload.Codec, updatedRecord.Payload.Version, updatedRecord.Payload.Data)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	view, err := jobs.NewDeliveryView(updatedInvocation, payload)
	if err != nil {
		return jobs.DeliveryView{}, err
	}
	updated := entry
	updated.Record = encoded
	updated.RecordSize = size
	updated.State = updatedInvocation.State()
	if updated.State.Terminal() {
		updated.ReadyAt = time.Time{}
		updated.LeaseToken = nil
		updated.LeaseIncarnation = [jobs.WorkerIncarnationBytes]byte{}
		updated.LeaseUntil = time.Time{}
		updated.ExcludedBinding = ""
		updated.ExcludedBuild = ""
		if updatedInvocation.Mode() != jobs.PlacementOnce {
			updated.Intents = nil
		}
	}
	if err := d.repo.save(opCtx, &entry, &updated); err != nil {
		return jobs.DeliveryView{}, err
	}
	return view, nil
}
