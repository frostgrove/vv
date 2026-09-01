package jobspg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/jobs"
)

func (d *Driver) Place(ctx context.Context, placement jobs.Placement) (jobs.PlacementResult, error) {
	if err := d.requireReady(); err != nil {
		return jobs.PlacementResult{}, err
	}
	if placement.IsZero() || placement.Namespace().Digest() != d.namespace.Digest() {
		return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrInvalid)
	}
	if d.source != nil {
		executor, found := crud.ExecutorFor(ctx, d.source)
		if found {
			if !crud.IsTransaction(executor) {
				return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrUnsupported)
			}
			tx, ok := crudsql.Transaction(executor)
			if !ok {
				return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrUnsupported)
			}
			stager, err := d.Stager(tx)
			if err != nil {
				return jobs.PlacementResult{}, err
			}
			staged, err := stager.Stage(ctx, placement)
			if err != nil {
				return jobs.PlacementResult{}, err
			}
			return jobs.NewPlacementResult(staged.InvocationID(), staged.Outcome())
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		result, err := d.place(ctx, placement)
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, errIntentConflict) && !errors.Is(err, errCandidateConflict) {
			return jobs.PlacementResult{}, placementError(err)
		}
	}
	return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrConflict)
}

func (d *Driver) place(ctx context.Context, placement jobs.Placement) (jobs.PlacementResult, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	defer tx.Rollback()
	result, err := d.placeInTx(ctx, tx, placement)
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobs.PlacementResult{}, err
	}
	return result, nil
}

func (d *Driver) placeInTx(ctx context.Context, tx *sql.Tx, placement jobs.Placement) (jobs.PlacementResult, error) {
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	intents := placement.IntentDigests().ReadCandidates()
	if err := d.repo.lockIntentKeys(ctx, tx, d.namespace, intents); err != nil {
		return jobs.PlacementResult{}, err
	}
	existing, found, err := d.repo.findIntent(ctx, tx, d.namespace, intents)
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	if found {
		return d.placeExisting(ctx, tx, placement, intents, existing, now)
	}
	insert, err := d.newPlacement(placement, placement.Candidate(), now)
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	if err := d.repo.insertDelivery(ctx, tx, d.namespace, insert); err != nil {
		return jobs.PlacementResult{}, err
	}
	for _, intent := range intents {
		if err := d.repo.insertIntent(ctx, tx, d.namespace, insert.id, intent); err != nil {
			if errors.Is(err, errIntentConflict) {
				if cleanupErr := d.repo.deleteDelivery(ctx, tx, d.namespace, insert.id); cleanupErr != nil {
					return jobs.PlacementResult{}, cleanupErr
				}
			}
			return jobs.PlacementResult{}, err
		}
	}
	return jobs.NewPlacementResult(insert.id, jobs.PlacementCreated)
}

func (d *Driver) placeExisting(ctx context.Context, tx *sql.Tx, placement jobs.Placement, intents []jobs.IntentKey, existing storedDelivery, now time.Time) (jobs.PlacementResult, error) {
	switch placement.Mode() {
	case jobs.PlacementRegular:
		if existing.id != placement.Candidate() {
			return jobs.PlacementResult{}, jobs.ErrConflict
		}
		if err := d.persistExistingIntentKeys(ctx, tx, placement, intents, existing); err != nil {
			return jobs.PlacementResult{}, err
		}
		return jobs.NewPlacementResult(existing.id, jobs.PlacementCreated)
	case jobs.PlacementOnce:
		if err := d.persistExistingIntentKeys(ctx, tx, placement, intents, existing); err != nil {
			return jobs.PlacementResult{}, err
		}
		outcome := jobs.PlacementConflict
		if samePayloadDigest(existing, placement.PayloadDigest()) {
			outcome = jobs.PlacementExistingSamePayload
		}
		return jobs.NewPlacementResult(existing.id, outcome)
	case jobs.PlacementCollapse, jobs.PlacementDebounce:
		if existing.state == jobs.InvocationQueued && len(existing.leaseToken) == 0 {
			updated, err := d.collapsedPlacement(placement, existing)
			if err != nil {
				return jobs.PlacementResult{}, err
			}
			availableAt := collapsedAvailability(placement, existing, now)
			if err := d.repo.updateCollapsed(ctx, tx, d.namespace, existing, updated, availableAt, now); err != nil {
				return jobs.PlacementResult{}, err
			}
		}
		if err := d.persistExistingIntentKeys(ctx, tx, placement, intents, existing); err != nil {
			return jobs.PlacementResult{}, err
		}
		return jobs.NewPlacementResult(existing.id, jobs.PlacementCollapsed)
	case jobs.PlacementUnique:
		if err := d.persistExistingIntentKeys(ctx, tx, placement, intents, existing); err != nil {
			return jobs.PlacementResult{}, err
		}
		return jobs.NewPlacementResult(existing.id, jobs.PlacementExisting)
	default:
		return jobs.PlacementResult{}, jobs.ErrInvalid
	}
}

func (d *Driver) persistExistingIntentKeys(ctx context.Context, tx *sql.Tx, placement jobs.Placement, incoming []jobs.IntentKey, existing storedDelivery) error {
	current := placement.IntentDigests().Current()
	legacyFallback := false
	if len(existing.intentKeys) != 0 {
		current = existing.intentKeys[0]
	}
	if len(existing.record) != 0 {
		record, err := decodeRecord(existing.record)
		if err != nil {
			return err
		}
		restored, err := jobs.RestoreDeliveryRecord(d.catalog, record)
		if err != nil {
			return err
		}
		if restored.Invocation().ID() != existing.id {
			return jobs.ErrCorrupt
		}
		current = restored.Invocation().Intent()
		_, legacyFallback = restored.Invocation().LegacyIntent()
		if len(existing.intentKeys) != 0 {
			if _, err := validateInvocationIntentKeys(restored.Invocation(), existing.intentKeys); err != nil {
				return err
			}
		}
	}
	for _, key := range incoming {
		if err := d.repo.ensureIntent(ctx, tx, d.namespace, existing.id, key); err != nil {
			return err
		}
	}
	actual, err := d.repo.intentKeysForDeliveries(ctx, tx, d.namespace, []jobs.InvocationID{existing.id})
	if err != nil {
		return err
	}
	ordered, err := orderIntentKeys(current, actual)
	if err != nil {
		return err
	}
	for _, persisted := range existing.intentKeys {
		found := false
		for _, key := range ordered {
			if key == persisted {
				found = true
				break
			}
		}
		if !found {
			return corruptIntentKeys()
		}
	}
	if len(existing.intentKeys) == 0 && legacyFallback && len(ordered) == 1 {
		return nil
	}
	return d.repo.updateDeliveryIntentKeys(ctx, tx, d.namespace, existing.id, ordered)
}

func (d *Driver) newPlacement(placement jobs.Placement, id jobs.InvocationID, now time.Time) (deliveryInsert, error) {
	eligibleAt := now.Add(placement.Delay()).Round(0).UTC()
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
		CreatedAt:    now,
		EligibleAt:   eligibleAt,
		StartBefore:  placement.StartBefore(),
		Policy:       placement.Policy(),
		Context:      placement.Context(),
	})
	if err != nil {
		return deliveryInsert{}, err
	}
	record, err := jobs.NewDeliveryRecord(invocation, placement.Payload(), placement.WireDigest(), placement.PayloadDigest())
	if err != nil {
		return deliveryInsert{}, err
	}
	insert, err := deliveryInsertFromRecord(record, eligibleAt)
	if err != nil {
		return deliveryInsert{}, err
	}
	insert.intentKeys = placement.IntentDigests().ReservationKeys()
	return insert, nil
}

func (d *Driver) collapsedPlacement(placement jobs.Placement, existing storedDelivery) (deliveryInsert, error) {
	record, err := decodeRecord(existing.record)
	if err != nil {
		return deliveryInsert{}, err
	}
	restored, err := jobs.RestoreDeliveryRecord(d.catalog, record)
	if err != nil {
		return deliveryInsert{}, err
	}
	replacement, err := jobs.NewDeliveryRecord(restored.Invocation(), placement.Payload(), placement.WireDigest(), placement.PayloadDigest())
	if err != nil {
		return deliveryInsert{}, err
	}
	updated, err := deliveryInsertFromRecord(replacement, existing.availableAt)
	if err != nil {
		return deliveryInsert{}, err
	}
	updated.priority = placement.Priority()
	return updated, nil
}

func deliveryInsertFromRecord(record jobs.DeliveryRecord, availableAt time.Time) (deliveryInsert, error) {
	encoded, err := encodeRecord(record)
	if err != nil {
		return deliveryInsert{}, err
	}
	size, err := record.Size()
	if err != nil {
		return deliveryInsert{}, err
	}
	value := deliveryInsert{
		id:           record.Genesis.ID,
		definition:   record.Genesis.Definition,
		codec:        record.Payload.Codec,
		codecVersion: record.Payload.Version,
		priority:     record.Genesis.Priority,
		state:        jobs.InvocationQueued,
		availableAt:  availableAt,
		recordSize:   size,
		record:       encoded,
		createdAt:    record.Genesis.CreatedAt,
	}
	if !record.PayloadDigest.IsZero() {
		value.payloadIdentity = record.PayloadDigest.Identity()
		value.payloadVersion = record.PayloadDigest.Version()
		digest := record.PayloadDigest.Bytes()
		value.payloadDigest = digest[:]
	}
	return value, nil
}

func samePayloadDigest(existing storedDelivery, digest jobs.PayloadDigest) bool {
	if digest.IsZero() || existing.payloadIdentity != digest.Identity().Value() || existing.payloadVersion != digest.Version() {
		return false
	}
	value := digest.Bytes()
	return equalBytes(existing.payloadDigest, value[:])
}

func collapsedAvailability(placement jobs.Placement, existing storedDelivery, now time.Time) time.Time {
	proposed := now.Add(placement.Delay()).Round(0).UTC()
	if placement.Mode() == jobs.PlacementCollapse {
		if existing.availableAt.IsZero() || proposed.Before(existing.availableAt) {
			return proposed
		}
		return existing.availableAt
	}
	if existing.availableAt.After(proposed) {
		proposed = existing.availableAt
	}
	maximum := existing.createdAt.Add(placement.MaxDelay()).Round(0).UTC()
	if proposed.After(maximum) {
		return maximum
	}
	return proposed
}

func placementError(err error) error {
	switch {
	case errors.Is(err, jobs.ErrInvalid):
		return jobs.RejectPlacement(jobs.ErrInvalid)
	case errors.Is(err, jobs.ErrTooLarge):
		return jobs.RejectPlacement(jobs.ErrTooLarge)
	case errors.Is(err, jobs.ErrConflict):
		return jobs.RejectPlacement(jobs.ErrConflict)
	default:
		return err
	}
}

func (d *Driver) Claim(ctx context.Context, request jobs.ClaimRequest) (jobs.ClaimBatch, error) {
	if err := d.requireReady(); err != nil {
		return jobs.ClaimBatch{}, err
	}
	if request.Namespace().Digest() != d.namespace.Digest() {
		return jobs.ClaimBatch{}, jobs.ErrInvalid
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return jobs.ClaimBatch{}, err
	}
	defer tx.Rollback()
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return jobs.ClaimBatch{}, err
	}
	type plannedClaim struct {
		target    jobs.ClaimTarget
		candidate claimCandidate
	}
	planned := make([]plannedClaim, 0, request.MaxItems())
	collapseIDs := make([]jobs.InvocationID, 0, request.MaxItems())
	remainingPlanned := request.MaxItems()
	for _, target := range request.Targets() {
		if remainingPlanned == 0 {
			break
		}
		limit := min(target.Available(), remainingPlanned)
		candidates, err := d.repo.claimCandidates(ctx, tx, d.namespace, target, limit, request.MaxBytes(), now)
		if err != nil {
			return jobs.ClaimBatch{}, err
		}
		for _, candidate := range candidates {
			record, err := decodeRecord(candidate.record)
			if err != nil {
				return jobs.ClaimBatch{}, err
			}
			planned = append(planned, plannedClaim{target: target, candidate: candidate})
			if record.Genesis.Mode == jobs.PlacementCollapse || record.Genesis.Mode == jobs.PlacementDebounce {
				collapseIDs = append(collapseIDs, candidate.id)
			}
		}
		remainingPlanned -= len(candidates)
	}
	intents, err := d.repo.intentKeysForDeliveries(ctx, tx, d.namespace, collapseIDs)
	if err != nil {
		return jobs.ClaimBatch{}, err
	}
	if err := d.repo.lockIntentKeys(ctx, tx, d.namespace, intents); err != nil {
		return jobs.ClaimBatch{}, err
	}
	if err := d.repo.lockIntentRowsForDeliveries(ctx, tx, d.namespace, collapseIDs); err != nil {
		return jobs.ClaimBatch{}, err
	}
	items := make([]jobs.ClaimedDelivery, 0, request.MaxItems())
	remainingBytes := request.MaxBytes()
	for _, item := range planned {
		candidate, found, err := d.repo.lockClaimCandidate(ctx, tx, d.namespace, item.candidate.id, item.target, remainingBytes, now)
		if err != nil {
			return jobs.ClaimBatch{}, err
		}
		if !found {
			continue
		}
		record, err := decodeRecord(candidate.record)
		if err != nil {
			return jobs.ClaimBatch{}, err
		}
		token, err := d.token()
		if err != nil {
			return jobs.ClaimBatch{}, err
		}
		if err := d.repo.claim(ctx, tx, d.namespace, candidate.id, request.Incarnation(), token, now.Add(request.LeaseTTL()), now); err != nil {
			return jobs.ClaimBatch{}, err
		}
		if record.Genesis.Mode == jobs.PlacementCollapse || record.Genesis.Mode == jobs.PlacementDebounce {
			if err := d.repo.releaseCollapseIntent(ctx, tx, d.namespace, candidate.id); err != nil {
				return jobs.ClaimBatch{}, err
			}
		}
		lease, err := jobs.NewLeaseRef(d.description.ID(), candidate.id, token)
		if err != nil {
			return jobs.ClaimBatch{}, err
		}
		claimed, err := jobs.NewClaimedDelivery(item.target, lease, record)
		if err != nil {
			return jobs.ClaimBatch{}, err
		}
		items = append(items, claimed)
		remainingBytes -= candidate.recordSize
	}
	if err := tx.Commit(); err != nil {
		return jobs.ClaimBatch{}, err
	}
	return jobs.NewClaimBatch(now, items)
}

func (d *Driver) Renew(ctx context.Context, request jobs.RenewRequest) (jobs.RenewResult, error) {
	if err := d.requireReady(); err != nil {
		return jobs.RenewResult{}, err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return jobs.RenewResult{}, err
	}
	defer tx.Rollback()
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return jobs.RenewResult{}, err
	}
	items := make([]jobs.LeaseRenewal, len(request.Leases()))
	for index, previous := range request.Leases() {
		if previous.Backend() != d.description.ID() {
			return jobs.RenewResult{}, jobs.ErrInvalid
		}
		token, err := d.token()
		if err != nil {
			return jobs.RenewResult{}, err
		}
		state, applied, err := d.repo.renew(ctx, tx, d.namespace, previous, token, now.Add(request.LeaseTTL()), now)
		if err != nil {
			return jobs.RenewResult{}, err
		}
		if !applied {
			items[index], err = jobs.NewLeaseRenewal(previous, jobs.LeaseRef{}, jobs.DeliveryMutationLeaseLost, jobs.DeliveryControlNone)
		} else {
			current, leaseErr := jobs.NewLeaseRef(d.description.ID(), previous.InvocationID(), token)
			if leaseErr != nil {
				return jobs.RenewResult{}, leaseErr
			}
			control := jobs.DeliveryControlNone
			if state == jobs.InvocationCancelRequested {
				control = jobs.DeliveryControlCancelRequested
			}
			items[index], err = jobs.NewLeaseRenewal(previous, current, jobs.DeliveryMutationApplied, control)
		}
		if err != nil {
			return jobs.RenewResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return jobs.RenewResult{}, err
	}
	return jobs.NewRenewResult(now, items)
}

func (d *Driver) Apply(ctx context.Context, request jobs.ApplyRequest) (jobs.ApplyResult, error) {
	if err := d.requireReady(); err != nil {
		return jobs.ApplyResult{}, err
	}
	command := request.Command()
	lease := command.Lease()
	if lease.Backend() != d.description.ID() {
		return jobs.ApplyResult{}, jobs.ErrInvalid
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	defer tx.Rollback()
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	if err := d.repo.lockDeliveryIntents(ctx, tx, d.namespace, lease.InvocationID()); err != nil {
		return jobs.ApplyResult{}, err
	}
	stored, held, err := d.repo.heldDelivery(ctx, tx, d.namespace, lease, now)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	if !held {
		return leaseLostApply(now)
	}
	record, decodeErr := decodeRecord(stored.record)
	if command.Kind() == jobs.DeliveryCommandRejectCorrupt {
		if decodeErr == nil {
			_, decodeErr = jobs.RestoreDeliveryRecord(d.catalog, record)
		}
		if decodeErr == nil {
			return jobs.ApplyResult{}, jobs.ErrConflict
		}
		application, err := jobs.ApplyDeliveryCommand(jobs.Invocation{}, command, now)
		if err != nil {
			return jobs.ApplyResult{}, err
		}
		retention, intentRetention := d.retentionDurations(stored.definition, nil)
		recordExpiresAt, intentExpiresAt, err := terminalRetentionDeadlines(now, retention, intentRetention)
		if err != nil {
			return jobs.ApplyResult{}, err
		}
		if err := d.repo.rejectCorrupt(ctx, tx, d.namespace, lease, recordExpiresAt, intentExpiresAt, now); err != nil {
			return jobs.ApplyResult{}, err
		}
		if err := d.repo.releaseCollapseIntent(ctx, tx, d.namespace, lease.InvocationID()); err != nil {
			return jobs.ApplyResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return jobs.ApplyResult{}, err
		}
		result, _ := jobs.NewDeliveryCommandResult(jobs.DeliveryMutationApplied, jobs.DeliveryControlNone)
		return jobs.NewApplyResult(now, result, application)
	}
	if decodeErr != nil {
		return jobs.ApplyResult{}, decodeErr
	}
	restored, err := jobs.RestoreDeliveryRecord(d.catalog, record)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	application, err := jobs.ApplyDeliveryCommand(restored.Invocation(), command, now)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	updated, err := jobs.NewDeliveryRecord(application.Invocation(), restored.Payload(), restored.WireDigest(), restored.PayloadDigest())
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	encoded, err := encodeRecord(updated)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	size, err := updated.Size()
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	state := application.Invocation().State()
	clearLease := state != jobs.InvocationRunning && state != jobs.InvocationCancelRequested
	var availableAt any
	var recordExpiresAt any
	var intentExpiresAt any
	var excludedBinding, excludedBuild string
	if state == jobs.InvocationQueued {
		availableAt = queuedAvailability(application.Invocation())
		if release, ok := application.Release(); ok {
			availableAt = release.AvailableAt()
			excludedBinding = release.ExcludedBinding().Value()
			excludedBuild = release.ExcludedBuild().Value()
		}
	}
	if state.Terminal() {
		recordDeadline, intentDeadline, err := terminalRetentionDeadlines(now, application.Invocation().Policy().Retention(), application.Invocation().Policy().IntentRetention())
		if err != nil {
			return jobs.ApplyResult{}, err
		}
		recordExpiresAt = recordDeadline
		intentExpiresAt = intentDeadline
	}
	if err := d.repo.saveApplication(ctx, tx, d.namespace, lease, state, availableAt, encoded, size, clearLease, excludedBinding, excludedBuild, recordExpiresAt, intentExpiresAt, now); err != nil {
		if errors.Is(err, jobs.ErrLeaseLost) {
			return leaseLostApply(now)
		}
		return jobs.ApplyResult{}, err
	}
	if state.Terminal() {
		if err := d.repo.releaseCollapseIntent(ctx, tx, d.namespace, lease.InvocationID()); err != nil {
			return jobs.ApplyResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return jobs.ApplyResult{}, err
	}
	control := applicationControl(command.Kind(), application)
	result, err := jobs.NewDeliveryCommandResult(jobs.DeliveryMutationApplied, control)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	return jobs.NewApplyResult(now, result, application)
}

func leaseLostApply(now time.Time) (jobs.ApplyResult, error) {
	result, err := jobs.NewDeliveryCommandResult(jobs.DeliveryMutationLeaseLost, jobs.DeliveryControlNone)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	return jobs.NewApplyResult(now, result, jobs.DeliveryApplication{})
}

func queuedAvailability(invocation jobs.Invocation) time.Time {
	outcome := invocation.Outcome()
	if !outcome.AvailableAt().IsZero() {
		return outcome.AvailableAt()
	}
	return invocation.EligibleAt()
}

func applicationControl(kind jobs.DeliveryCommandKind, application jobs.DeliveryApplication) jobs.DeliveryControlStatus {
	invocation := application.Invocation()
	if (kind == jobs.DeliveryCommandArbitrateAttemptDeadline || kind == jobs.DeliveryCommandRevokeAttempt) && application.Changed() && invocation.State() == jobs.InvocationTerminated {
		return jobs.DeliveryControlTerminated
	}
	if (kind == jobs.DeliveryCommandProgress || kind == jobs.DeliveryCommandArbitrateAttemptDeadline) && invocation.State() == jobs.InvocationCancelRequested {
		return jobs.DeliveryControlCancelRequested
	}
	if kind == jobs.DeliveryCommandFinishAttempt {
		if attempt, ok := application.Attempt(); ok && attempt.Disposition().Kind() == jobs.DispositionCancelled && attempt.Disposition().Reason() == jobs.ReasonCancelRequested {
			return jobs.DeliveryControlCancelRequested
		}
	}
	return jobs.DeliveryControlNone
}

func (d *Driver) Recover(ctx context.Context, request jobs.RecoverRequest) (jobs.RecoverResult, error) {
	if err := d.requireReady(); err != nil {
		return jobs.RecoverResult{}, err
	}
	if request.Namespace().Digest() != d.namespace.Digest() {
		return jobs.RecoverResult{}, jobs.ErrInvalid
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return jobs.RecoverResult{}, err
	}
	defer tx.Rollback()
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return jobs.RecoverResult{}, err
	}
	candidates, more, err := d.repo.expired(ctx, tx, d.namespace, request.MaxItems(), now)
	if err != nil {
		return jobs.RecoverResult{}, err
	}
	items := make([]jobs.RecoveredDelivery, 0, len(candidates))
	remainingBytes := request.MaxBytes()
	for _, candidate := range candidates {
		if candidate.recordSize > remainingBytes {
			more = true
			break
		}
		record, err := decodeRecord(candidate.record)
		if err != nil {
			return jobs.RecoverResult{}, err
		}
		token, err := d.token()
		if err != nil {
			return jobs.RecoverResult{}, err
		}
		if err := d.repo.recover(ctx, tx, d.namespace, candidate.id, request.Incarnation(), token, now.Add(request.LeaseTTL()), now); err != nil {
			return jobs.RecoverResult{}, err
		}
		lease, err := jobs.NewLeaseRef(d.description.ID(), candidate.id, token)
		if err != nil {
			return jobs.RecoverResult{}, err
		}
		recovered, err := jobs.NewRecoveredDelivery(lease, record)
		if err != nil {
			return jobs.RecoverResult{}, err
		}
		items = append(items, recovered)
		remainingBytes -= candidate.recordSize
	}
	if err := tx.Commit(); err != nil {
		return jobs.RecoverResult{}, err
	}
	return jobs.NewRecoverResult(now, items, 0, more)
}

func (d *Driver) String() string {
	return "[PostgreSQL job driver]"
}

func (d *Driver) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
