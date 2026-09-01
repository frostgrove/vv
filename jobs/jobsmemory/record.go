package jobsmemory

import (
	"time"

	"github.com/frostgrove/vv/jobs"
)

type rebuiltRecord struct {
	record jobs.DeliveryRecord
	size   int
}

func recordFromPlacement(placement jobs.Placement, id jobs.InvocationID, createdAt, eligibleAt time.Time) (jobs.DeliveryRecord, jobs.Invocation, error) {
	legacy, _ := placement.LegacyIntent()
	invocation, err := jobs.NewInvocation(jobs.InvocationSpec{
		ID:           id,
		Namespace:    placement.Namespace(),
		Partition:    placement.Partition(),
		Definition:   placement.Definition(),
		Queue:        placement.Queue(),
		Mode:         placement.Mode(),
		Intent:       placement.IntentDigests().Current(),
		LegacyIntent: legacy,
		Priority:     placement.Priority(),
		CreatedAt:    createdAt,
		EligibleAt:   eligibleAt,
		StartBefore:  placement.StartBefore(),
		Policy:       placement.Policy(),
		Context:      placement.Context(),
	})
	if err != nil {
		return jobs.DeliveryRecord{}, jobs.Invocation{}, err
	}
	record, err := jobs.NewDeliveryRecord(invocation, placement.Payload(), placement.WireDigest(), placement.PayloadDigest())
	if err != nil {
		return jobs.DeliveryRecord{}, jobs.Invocation{}, err
	}
	return record, invocation, nil
}

func restoreRecord(record jobs.DeliveryRecord) (jobs.Invocation, jobs.DeliveryRecord, error) {
	durable, err := jobs.RestoreDurableContext(record.Genesis.Namespace, record.Genesis.Partition, record.Genesis.Definition, record.Genesis.Policy.Trace(), record.Genesis.Context)
	if err != nil {
		return jobs.Invocation{}, jobs.DeliveryRecord{}, err
	}
	invocation, err := jobs.RestoreInvocation(jobs.InvocationRestoreSpec{
		Genesis: jobs.InvocationSpec{
			ID:           record.Genesis.ID,
			Namespace:    record.Genesis.Namespace,
			Partition:    record.Genesis.Partition,
			Definition:   record.Genesis.Definition,
			Queue:        record.Genesis.Queue,
			Mode:         record.Genesis.Mode,
			Intent:       record.Genesis.Intent,
			LegacyIntent: record.Genesis.LegacyIntent,
			Priority:     record.Genesis.Priority,
			CreatedAt:    record.Genesis.CreatedAt,
			EligibleAt:   record.Genesis.EligibleAt,
			StartBefore:  record.Genesis.StartBefore,
			Policy:       record.Genesis.Policy,
			Context:      durable,
		},
		Outcomes: record.Outcomes,
		Attempts: record.Attempts,
	})
	if err != nil {
		return jobs.Invocation{}, jobs.DeliveryRecord{}, err
	}
	payload, err := jobs.NewEncodedPayload(record.Payload.Codec, record.Payload.Version, record.Payload.Data)
	if err != nil {
		return jobs.Invocation{}, jobs.DeliveryRecord{}, err
	}
	canonical, err := jobs.NewDeliveryRecord(invocation, payload, record.WireDigest, record.PayloadDigest)
	if err != nil {
		return jobs.Invocation{}, jobs.DeliveryRecord{}, err
	}
	return invocation, canonical, nil
}

func recordFromInvocation(invocation jobs.Invocation, previous jobs.DeliveryRecord) (rebuiltRecord, error) {
	payload, err := jobs.NewEncodedPayload(previous.Payload.Codec, previous.Payload.Version, previous.Payload.Data)
	if err != nil {
		return rebuiltRecord{}, err
	}
	record, err := jobs.NewDeliveryRecord(invocation, payload, previous.WireDigest, previous.PayloadDigest)
	if err != nil {
		return rebuiltRecord{}, err
	}
	size, err := jobs.DeliveryRecordSize(record)
	if err != nil {
		return rebuiltRecord{}, err
	}
	return rebuiltRecord{record: record, size: size}, nil
}

func deliveryCharge(invocation jobs.Invocation, record jobs.DeliveryRecord, size int) (int, error) {
	if invocation.IsTerminal() {
		return size, nil
	}
	at := invocation.CreatedAt()
	for _, outcome := range invocation.History() {
		if outcome.OccurredAt().After(at) {
			at = outcome.OccurredAt()
		}
	}
	for _, attempt := range invocation.Attempts() {
		for _, occurredAt := range []time.Time{attempt.StartedAt(), attempt.ProgressedAt(), attempt.FinishedAt()} {
			if occurredAt.After(at) {
				at = occurredAt
			}
		}
	}
	if invocation.CancelRequestedAt().After(at) {
		at = invocation.CancelRequestedAt()
	}
	maximum := size
	terminated, err := invocation.Terminate(at)
	if err != nil {
		return 0, err
	}
	rebuilt, err := recordFromInvocation(terminated, record)
	if err != nil {
		return 0, err
	}
	maximum = max(maximum, rebuilt.size)
	if invocation.State() == jobs.InvocationCancelRequested {
		return maximum, nil
	}
	requested, err := invocation.RequestCancel(at)
	if err != nil {
		return 0, err
	}
	rebuilt, err = recordFromInvocation(requested, record)
	if err != nil {
		return 0, err
	}
	maximum = max(maximum, rebuilt.size)
	if requested.IsTerminal() {
		return maximum, nil
	}
	terminated, err = requested.Terminate(at)
	if err != nil {
		return 0, err
	}
	rebuilt, err = recordFromInvocation(terminated, record)
	if err != nil {
		return 0, err
	}
	return max(maximum, rebuilt.size), nil
}
