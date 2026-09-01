package jobsredis

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/frostgrove/vv/jobs"
)

func (d *Driver) Place(ctx context.Context, placement jobs.Placement) (jobs.PlacementResult, error) {
	if err := d.requireReady(); err != nil {
		return jobs.PlacementResult{}, err
	}
	if placement.IsZero() || placement.Namespace().Digest() != d.namespace.Digest() {
		return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrInvalid)
	}
	opCtx, cancel, unlock, now, err := d.begin(ctx)
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	defer cancel()
	defer unlock()

	existing, found, err := d.findIntent(opCtx, placement.IntentDigests())
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	if found {
		switch placement.Mode() {
		case jobs.PlacementOnce:
			record, decodeErr := decodeRecord(existing.Record)
			if decodeErr != nil {
				return jobs.PlacementResult{}, decodeErr
			}
			outcome := jobs.PlacementConflict
			if samePayloadDigest(record.PayloadDigest, placement.PayloadDigest()) {
				outcome = jobs.PlacementExistingSamePayload
			}
			return jobs.NewPlacementResult(record.Genesis.ID, outcome)
		case jobs.PlacementCollapse, jobs.PlacementDebounce:
			return d.collapse(opCtx, placement, existing, now)
		case jobs.PlacementUnique:
			id, parseErr := jobs.ParseInvocationID(existing.ID)
			if parseErr != nil {
				return jobs.PlacementResult{}, parseErr
			}
			return jobs.NewPlacementResult(id, jobs.PlacementExisting)
		}
	}

	id := placement.Candidate()
	if _, found, err := d.repo.entry(opCtx, id.String()); err != nil {
		return jobs.PlacementResult{}, err
	} else if found {
		return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrConflict)
	}
	record, invocation, err := recordFromPlacement(placement, id, now, now.Add(placement.Delay()).Round(0).UTC())
	if err != nil {
		return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrInvalid)
	}
	entry, err := entryFromRecord(record, invocation, intentStrings(placement.IntentDigests().ReservationKeys()))
	if err != nil {
		return jobs.PlacementResult{}, placementError(err)
	}
	if err := d.repo.save(opCtx, nil, &entry); err != nil {
		return jobs.PlacementResult{}, err
	}
	return jobs.NewPlacementResult(id, jobs.PlacementCreated)
}

func (d *Driver) collapse(ctx context.Context, placement jobs.Placement, existing storedEntry, now time.Time) (jobs.PlacementResult, error) {
	id, err := jobs.ParseInvocationID(existing.ID)
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	if existing.State != jobs.InvocationQueued || len(existing.LeaseToken) != 0 {
		return jobs.NewPlacementResult(id, jobs.PlacementCollapsed)
	}
	previous, err := decodeRecord(existing.Record)
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	invocation, _, err := restoreRecord(previous)
	if err != nil {
		return jobs.PlacementResult{}, err
	}
	readyAt := collapsedAvailability(placement, existing.ReadyAt, invocation.CreatedAt(), now)
	record, updatedInvocation, err := recordFromPlacement(placement, id, invocation.CreatedAt(), readyAt)
	if err != nil {
		return jobs.PlacementResult{}, jobs.RejectPlacement(jobs.ErrInvalid)
	}
	updated, err := entryFromRecord(record, updatedInvocation, intentStrings(placement.IntentDigests().ReservationKeys()))
	if err != nil {
		return jobs.PlacementResult{}, placementError(err)
	}
	if err := d.repo.save(ctx, &existing, &updated); err != nil {
		return jobs.PlacementResult{}, err
	}
	return jobs.NewPlacementResult(id, jobs.PlacementCollapsed)
}

func (d *Driver) Claim(ctx context.Context, request jobs.ClaimRequest) (jobs.ClaimBatch, error) {
	if err := d.requireReady(); err != nil {
		return jobs.ClaimBatch{}, err
	}
	if request.Namespace().Digest() != d.namespace.Digest() {
		return jobs.ClaimBatch{}, jobs.ErrInvalid
	}
	opCtx, cancel, unlock, now, err := d.begin(ctx)
	if err != nil {
		return jobs.ClaimBatch{}, err
	}
	defer cancel()
	defer unlock()
	targets := request.Targets()
	priorities, err := d.repo.priorities(opCtx)
	if err != nil {
		return jobs.ClaimBatch{}, err
	}
	sort.Ints(priorities)
	counts := make([]int, len(targets))
	items := make([]jobs.ClaimedDelivery, 0, request.MaxItems())
	claimedBytes := 0
	seen := make(map[string]struct{})
	for _, priority := range priorities {
		for targetIndex, target := range targets {
			available := target.Available() - counts[targetIndex]
			if available < 1 || len(items) == request.MaxItems() {
				continue
			}
			ids, readErr := d.repo.readyIDs(opCtx, priority, target.Definition().Value(), now, int64(available*4+16))
			if readErr != nil {
				return jobs.ClaimBatch{}, readErr
			}
			for _, id := range ids {
				if counts[targetIndex] == target.Available() || len(items) == request.MaxItems() {
					break
				}
				if _, duplicate := seen[id]; duplicate {
					continue
				}
				entry, found, readErr := d.repo.entry(opCtx, id)
				if readErr != nil {
					return jobs.ClaimBatch{}, readErr
				}
				if !found || !targetMatches(target, entry) || entry.ReadyAt.After(now) || entry.RecordSize > request.MaxBytes()-claimedBytes {
					continue
				}
				token, tokenErr := d.token(32)
				if tokenErr != nil {
					return jobs.ClaimBatch{}, tokenErr
				}
				invocation, parseErr := jobs.ParseInvocationID(entry.ID)
				if parseErr != nil {
					return jobs.ClaimBatch{}, parseErr
				}
				lease, buildErr := jobs.NewLeaseRef(d.description.ID(), invocation, token)
				if buildErr != nil {
					return jobs.ClaimBatch{}, buildErr
				}
				record, decodeErr := decodeRecord(entry.Record)
				if decodeErr != nil {
					return jobs.ClaimBatch{}, decodeErr
				}
				delivery, buildErr := jobs.NewClaimedDelivery(target, lease, record)
				if buildErr != nil {
					return jobs.ClaimBatch{}, buildErr
				}
				updated := entry
				updated.LeaseToken = token
				updated.LeaseIncarnation = request.Incarnation().Bytes()
				updated.LeaseUntil = now.Add(request.LeaseTTL()).Round(0).UTC()
				if record.Genesis.Mode == jobs.PlacementCollapse || record.Genesis.Mode == jobs.PlacementDebounce {
					updated.Intents = nil
				}
				if err := d.repo.save(opCtx, &entry, &updated); err != nil {
					return jobs.ClaimBatch{}, err
				}
				items = append(items, delivery)
				claimedBytes += entry.RecordSize
				counts[targetIndex]++
				seen[id] = struct{}{}
			}
		}
		if len(items) == request.MaxItems() {
			break
		}
	}
	return jobs.NewClaimBatch(now, items)
}

func (d *Driver) Renew(ctx context.Context, request jobs.RenewRequest) (jobs.RenewResult, error) {
	if err := d.requireReady(); err != nil {
		return jobs.RenewResult{}, err
	}
	opCtx, cancel, unlock, now, err := d.begin(ctx)
	if err != nil {
		return jobs.RenewResult{}, err
	}
	defer cancel()
	defer unlock()
	leases := request.Leases()
	items := make([]jobs.LeaseRenewal, len(leases))
	for index, previous := range leases {
		if previous.Backend() != d.description.ID() {
			return jobs.RenewResult{}, jobs.ErrInvalid
		}
		entry, found, readErr := d.repo.entry(opCtx, previous.InvocationID().String())
		if readErr != nil {
			return jobs.RenewResult{}, readErr
		}
		control := deliveryControl(entry, found)
		if !found || !sameLease(entry, previous) || !entry.LeaseUntil.After(now) || entry.State.Terminal() {
			items[index], err = jobs.NewLeaseRenewal(previous, jobs.LeaseRef{}, jobs.DeliveryMutationLeaseLost, control)
			if err != nil {
				return jobs.RenewResult{}, err
			}
			continue
		}
		token, tokenErr := d.token(32)
		if tokenErr != nil {
			return jobs.RenewResult{}, tokenErr
		}
		updated := entry
		updated.LeaseToken = token
		updated.LeaseUntil = now.Add(request.LeaseTTL()).Round(0).UTC()
		if err := d.repo.save(opCtx, &entry, &updated); err != nil {
			return jobs.RenewResult{}, err
		}
		current, buildErr := jobs.NewLeaseRef(d.description.ID(), previous.InvocationID(), token)
		if buildErr != nil {
			return jobs.RenewResult{}, buildErr
		}
		items[index], err = jobs.NewLeaseRenewal(previous, current, jobs.DeliveryMutationApplied, control)
		if err != nil {
			return jobs.RenewResult{}, err
		}
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
	opCtx, cancel, unlock, now, err := d.begin(ctx)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	defer cancel()
	defer unlock()
	entry, found, err := d.repo.entry(opCtx, lease.InvocationID().String())
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	if !found || !sameLease(entry, lease) || !entry.LeaseUntil.After(now) {
		return leaseLostApply(now, deliveryControl(entry, found))
	}
	if command.Kind() == jobs.DeliveryCommandRejectCorrupt {
		application, applyErr := jobs.ApplyDeliveryCommand(jobs.Invocation{}, command, now)
		if applyErr != nil {
			return jobs.ApplyResult{}, applyErr
		}
		if err := d.repo.save(opCtx, &entry, nil); err != nil {
			return jobs.ApplyResult{}, err
		}
		result, _ := jobs.NewDeliveryCommandResult(jobs.DeliveryMutationApplied, jobs.DeliveryControlNone)
		return jobs.NewApplyResult(now, result, application)
	}
	record, err := decodeRecord(entry.Record)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	invocation, canonical, err := restoreRecord(record)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	application, err := jobs.ApplyDeliveryCommand(invocation, command, now)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	updatedRecord, err := recordFromInvocation(application.Invocation(), canonical)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	encoded, err := encodeRecord(updatedRecord)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	size, err := updatedRecord.Size()
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	updated := entry
	updated.Record = encoded
	updated.RecordSize = size
	updated.State = application.Invocation().State()
	if updated.State != jobs.InvocationRunning && updated.State != jobs.InvocationCancelRequested {
		updated.LeaseToken = nil
		updated.LeaseIncarnation = [jobs.WorkerIncarnationBytes]byte{}
		updated.LeaseUntil = time.Time{}
	}
	if updated.State == jobs.InvocationQueued {
		updated.ReadyAt = queuedAvailability(application.Invocation())
		updated.ExcludedBinding = ""
		updated.ExcludedBuild = ""
		if release, ok := application.Release(); ok {
			updated.ReadyAt = release.AvailableAt()
			updated.ExcludedBinding = release.ExcludedBinding().Value()
			updated.ExcludedBuild = release.ExcludedBuild().Value()
		}
	}
	if updated.State.Terminal() && updatedRecord.Genesis.Mode != jobs.PlacementOnce {
		err = d.repo.save(opCtx, &entry, nil)
	} else {
		err = d.repo.save(opCtx, &entry, &updated)
	}
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	control := applicationControl(command.Kind(), application)
	result, err := jobs.NewDeliveryCommandResult(jobs.DeliveryMutationApplied, control)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	return jobs.NewApplyResult(now, result, application)
}

func (d *Driver) Recover(ctx context.Context, request jobs.RecoverRequest) (jobs.RecoverResult, error) {
	if err := d.requireReady(); err != nil {
		return jobs.RecoverResult{}, err
	}
	if request.Namespace().Digest() != d.namespace.Digest() {
		return jobs.RecoverResult{}, jobs.ErrInvalid
	}
	opCtx, cancel, unlock, now, err := d.begin(ctx)
	if err != nil {
		return jobs.RecoverResult{}, err
	}
	defer cancel()
	defer unlock()
	ids, err := d.repo.recoveryIDs(opCtx, request.Incarnation(), now, int64(request.MaxItems()))
	if err != nil {
		return jobs.RecoverResult{}, err
	}
	items := make([]jobs.RecoveredDelivery, 0, request.MaxItems())
	remainingBytes := request.MaxBytes()
	more := false
	for index, id := range ids {
		entry, found, readErr := d.repo.entry(opCtx, id)
		if readErr != nil {
			return jobs.RecoverResult{}, readErr
		}
		if !found || len(entry.LeaseToken) == 0 || entry.LeaseIncarnation != request.Incarnation().Bytes() && entry.LeaseUntil.After(now) {
			continue
		}
		if len(items) == request.MaxItems() || entry.RecordSize > remainingBytes {
			more = true
			break
		}
		token, tokenErr := d.token(32)
		if tokenErr != nil {
			return jobs.RecoverResult{}, tokenErr
		}
		invocation, parseErr := jobs.ParseInvocationID(entry.ID)
		if parseErr != nil {
			return jobs.RecoverResult{}, parseErr
		}
		lease, buildErr := jobs.NewLeaseRef(d.description.ID(), invocation, token)
		if buildErr != nil {
			return jobs.RecoverResult{}, buildErr
		}
		record, decodeErr := decodeRecord(entry.Record)
		if decodeErr != nil {
			return jobs.RecoverResult{}, decodeErr
		}
		recovered, buildErr := jobs.NewRecoveredDelivery(lease, record)
		if buildErr != nil {
			return jobs.RecoverResult{}, buildErr
		}
		updated := entry
		updated.LeaseToken = token
		updated.LeaseIncarnation = request.Incarnation().Bytes()
		updated.LeaseUntil = now.Add(request.LeaseTTL()).Round(0).UTC()
		if err := d.repo.save(opCtx, &entry, &updated); err != nil {
			return jobs.RecoverResult{}, err
		}
		items = append(items, recovered)
		remainingBytes -= entry.RecordSize
		if len(items) == request.MaxItems() && index+1 < len(ids) {
			more = true
		}
	}
	return jobs.NewRecoverResult(now, items, 0, more)
}

func (d *Driver) begin(ctx context.Context) (context.Context, context.CancelFunc, func(), time.Time, error) {
	opCtx, cancel, err := d.operationContext(ctx)
	if err != nil {
		return nil, nil, nil, time.Time{}, err
	}
	lockToken, err := d.token(16)
	if err != nil {
		cancel()
		return nil, nil, nil, time.Time{}, err
	}
	unlock, err := d.repo.lock(opCtx, lockToken, d.operationTimeout*2)
	if err != nil {
		cancel()
		return nil, nil, nil, time.Time{}, err
	}
	now, err := d.repo.now(opCtx)
	if err != nil {
		unlock()
		cancel()
		return nil, nil, nil, time.Time{}, err
	}
	return opCtx, cancel, unlock, now, nil
}

func (d *Driver) findIntent(ctx context.Context, digests jobs.IntentDigests) (storedEntry, bool, error) {
	for _, intent := range digests.ReadCandidates() {
		entry, found, err := d.repo.intent(ctx, intentString(intent))
		if err != nil || found {
			return entry, found, err
		}
	}
	return storedEntry{}, false, nil
}

func entryFromRecord(record jobs.DeliveryRecord, invocation jobs.Invocation, intents []string) (storedEntry, error) {
	encoded, err := encodeRecord(record)
	if err != nil {
		return storedEntry{}, err
	}
	size, err := record.Size()
	if err != nil {
		return storedEntry{}, err
	}
	return storedEntry{
		ID:         invocation.ID().String(),
		Definition: invocation.Definition().Value(),
		Codec:      record.Payload.Codec.Value(),
		Version:    record.Payload.Version,
		Priority:   invocation.Priority(),
		State:      invocation.State(),
		ReadyAt:    invocation.EligibleAt(),
		RecordSize: size,
		Record:     encoded,
		Intents:    intents,
	}, nil
}

func targetMatches(target jobs.ClaimTarget, entry storedEntry) bool {
	if entry.State != jobs.InvocationQueued || len(entry.LeaseToken) != 0 || target.Definition().Value() != entry.Definition {
		return false
	}
	if entry.ExcludedBinding == target.Binding().Value() && entry.ExcludedBuild == target.Build().Value() {
		return false
	}
	for _, revision := range target.SupportedRevisions() {
		if revision.Codec().Value() == entry.Codec && revision.Version() == entry.Version {
			return true
		}
	}
	return false
}

func intentStrings(values []jobs.IntentKey) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = intentString(value)
	}
	return result
}

func intentString(value jobs.IntentKey) string {
	scope := value.Scope().Bytes()
	digest := value.Digest().Bytes()
	return hex.EncodeToString(scope[:]) + ":" + strconv.Itoa(int(value.Revision())) + ":" + strconv.Itoa(int(value.Purpose())) + ":" + hex.EncodeToString(digest[:])
}

func collapsedAvailability(placement jobs.Placement, previous, createdAt, now time.Time) time.Time {
	proposed := now.Add(placement.Delay()).Round(0).UTC()
	if placement.Mode() == jobs.PlacementCollapse {
		if previous.IsZero() || proposed.Before(previous) {
			return proposed
		}
		return previous
	}
	if previous.After(proposed) {
		proposed = previous
	}
	maximum := createdAt.Add(placement.MaxDelay()).Round(0).UTC()
	if proposed.After(maximum) {
		return maximum
	}
	return proposed
}

func queuedAvailability(invocation jobs.Invocation) time.Time {
	outcome := invocation.Outcome()
	if !outcome.AvailableAt().IsZero() {
		return outcome.AvailableAt()
	}
	return invocation.EligibleAt()
}

func samePayloadDigest(left, right jobs.PayloadDigest) bool {
	if left.IsZero() || right.IsZero() || left.Identity() != right.Identity() || left.Version() != right.Version() {
		return false
	}
	leftValue := left.Bytes()
	rightValue := right.Bytes()
	return bytes.Equal(leftValue[:], rightValue[:])
}

func deliveryControl(entry storedEntry, found bool) jobs.DeliveryControlStatus {
	if !found {
		return jobs.DeliveryControlNone
	}
	switch entry.State {
	case jobs.InvocationCancelRequested, jobs.InvocationCancelled:
		return jobs.DeliveryControlCancelRequested
	case jobs.InvocationTerminated:
		return jobs.DeliveryControlTerminated
	default:
		return jobs.DeliveryControlNone
	}
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

func leaseLostApply(now time.Time, control jobs.DeliveryControlStatus) (jobs.ApplyResult, error) {
	result, err := jobs.NewDeliveryCommandResult(jobs.DeliveryMutationLeaseLost, control)
	if err != nil {
		return jobs.ApplyResult{}, err
	}
	return jobs.NewApplyResult(now, result, jobs.DeliveryApplication{})
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

func (d *Driver) String() string {
	return "[Redis job driver]"
}

func (d *Driver) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
