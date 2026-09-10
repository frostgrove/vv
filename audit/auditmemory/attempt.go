package auditmemory

import (
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/frostgrove/vv/audit"
)

type attemptRecord struct {
	state       audit.AttemptProjectionStateView
	transitions []audit.ItemRef
	binding     audit.AttemptIdentityAliasBinding
}

type attemptOperationKey struct {
	catalog   audit.CatalogID
	operation audit.OperationID
}

type attemptTypeKey struct {
	catalog   audit.CatalogID
	operation audit.OperationName
	replay    audit.AttemptReplayFingerprint
}

type attemptIdempotencyKey struct {
	kind       audit.IdempotencyDomainKind
	catalog    audit.CatalogID
	chain      audit.AttemptChainID
	algorithm  string
	profile    string
	keyID      string
	commitment [32]byte
}

type attemptTypeLease struct {
	store    *Store
	lock     chan struct{}
	state    audit.AttemptTypeStateResult
	mu       sync.Mutex
	released bool
	appended bool
}

func (s *Store) AttemptState(ctx context.Context, query audit.AttemptStateQuery) (audit.AttemptStateResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.AttemptStateResult{}, err
	}
	if s == nil || s.log == nil || s.isClosed() {
		return audit.AttemptStateResult{}, audit.Failure(audit.Closed, errors.New("auditmemory: store is closed"))
	}
	view := query.View()
	if view.Log != s.log.logID {
		return audit.AttemptStateResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: attempt query is invalid"))
	}
	s.log.mu.RLock()
	record, found := s.log.attempts[view.Chain]
	s.log.mu.RUnlock()
	if !found {
		return audit.NewAttemptStateResult(query, audit.AttemptStateResultData{})
	}
	result, err := audit.NewAttemptStateResult(query, audit.AttemptStateResultData{State: record.state, Transitions: record.transitions})
	if err != nil {
		return audit.AttemptStateResult{}, audit.Failure(audit.Corrupt, err)
	}
	return result, nil
}

func (s *Store) AttemptTypeState(ctx context.Context, query audit.AttemptTypeStateQuery) (audit.AttemptTypeStateResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.AttemptTypeStateResult{}, err
	}
	if s == nil || s.log == nil || s.isClosed() {
		return audit.AttemptTypeStateResult{}, audit.Failure(audit.Closed, errors.New("auditmemory: store is closed"))
	}
	view := query.View()
	if view.Log != s.log.logID {
		return audit.AttemptTypeStateResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: attempt type query is invalid"))
	}
	key := attemptTypeKey{catalog: view.Type.Catalog, operation: view.Type.Operation, replay: view.Type.Replay}
	s.log.mu.RLock()
	state, found := s.log.attemptTypes[key]
	s.log.mu.RUnlock()
	if !found {
		state = audit.AttemptTypeProjectionStateView{
			Catalog: view.Type.Catalog, Operation: view.Type.Operation, Policy: view.Type.Policy, Replay: view.Type.Replay,
		}
	}
	result, err := audit.NewAttemptTypeStateResult(query, state)
	if err != nil {
		return audit.AttemptTypeStateResult{}, audit.Failure(audit.Corrupt, err)
	}
	return result, nil
}

func (s *Store) LockAttemptType(ctx context.Context, query audit.AttemptTypeStateQuery) (audit.AttemptTypeLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.log == nil || s.isClosed() {
		return nil, audit.Failure(audit.Closed, errors.New("auditmemory: store is closed"))
	}
	view := query.View()
	if view.Log != s.log.logID {
		return nil, audit.Failure(audit.Refused, errors.New("auditmemory: attempt type query is invalid"))
	}
	key := attemptTypeKey{catalog: view.Type.Catalog, operation: view.Type.Operation, replay: view.Type.Replay}
	s.log.mu.Lock()
	lock := s.log.attemptLocks[key]
	if lock == nil {
		lock = make(chan struct{}, 1)
		s.log.attemptLocks[key] = lock
	}
	s.log.mu.Unlock()
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	state, err := s.AttemptTypeState(ctx, query)
	if err != nil {
		<-lock
		return nil, err
	}
	return &attemptTypeLease{store: s, lock: lock, state: state}, nil
}

func (l *attemptTypeLease) State() audit.AttemptTypeStateResult {
	if l == nil {
		return audit.AttemptTypeStateResult{}
	}
	return l.state
}

func (l *attemptTypeLease) Append(ctx context.Context, writer audit.Writer, request audit.AppendRequest) (audit.AppendResult, error) {
	if l == nil || l.store == nil || writer == nil {
		return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: attempt type lease is invalid"))
	}
	l.mu.Lock()
	if l.released || l.appended {
		l.mu.Unlock()
		return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: attempt type lease is consumed"))
	}
	l.appended = true
	l.mu.Unlock()
	view := request.View()
	if view.Attempt.TypeExpected != l.state.State() {
		return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: attempt type lease does not match request"))
	}
	if writer.BackingID() != l.store.BackingID() || writer.LogID() != l.store.LogID() || !audit.SameBacking(writer.Backing(), l.store.Backing()) {
		return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: attempt writer does not match lease"))
	}
	return writer.Append(ctx, request)
}

func (l *attemptTypeLease) Release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return
	}
	l.released = true
	lock := l.lock
	l.mu.Unlock()
	<-lock
}

func appendAttemptLocked(target *log, clock func() time.Time, request audit.AppendRequest, authority audit.Authority) (audit.AppendResult, error) {
	view := request.View()
	revision := view.Revision.View()
	item := revision.Items[0]
	transition := item.Attempt
	domain := audit.AttemptFinishIdempotencyDomain
	chain := transition.Chain
	if transition.Kind == audit.AttemptStartedTransition {
		domain = audit.AttemptStartIdempotencyDomain
		chain = audit.AttemptChainID{}
	}
	keys, ok := attemptIdempotencyKeys(domain, revision.Header.Catalog.ID, chain, view.Idempotency)
	if !ok {
		return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: attempt idempotency is invalid"))
	}
	if existingID, found, consistent := findAttemptIdempotency(target.attemptKeys, keys); found {
		if !consistent {
			return audit.AppendResult{}, audit.Failure(audit.Corrupt, errors.New("auditmemory: attempt idempotency aliases diverge"))
		}
		existing := target.revisions[existingID]
		existingTransition, present := existing.stored.AttemptTransition()
		if !present || existing.stored.Revision().Semantic != revision.Header.Semantic || existingTransition.Chain != transition.Chain || existingTransition.Kind != transition.Kind || existingTransition.OperationID != transition.OperationID || existingTransition.Policy != transition.Policy || existingTransition.Replay != transition.Replay {
			return audit.AppendResult{}, audit.Failure(audit.Conflict, errors.New("auditmemory: attempt idempotency changed meaning"))
		}
		for _, key := range keys {
			target.attemptKeys[key] = existingID
		}
		return audit.NewAttemptAppendResult(request, existing.stored, audit.Replayed, authority)
	}
	binding := view.Attempts[0]
	current, found := target.attempts[transition.Chain]
	if transition.Kind == audit.AttemptStartedTransition {
		if found || view.Attempt.Expected.Present {
			return audit.AppendResult{}, audit.Failure(audit.Conflict, errors.New("auditmemory: attempt already exists"))
		}
		operationKey := attemptOperationKey{catalog: revision.Header.Catalog.ID, operation: transition.OperationID}
		if prior, exists := target.attemptOps[operationKey]; exists && prior != transition.Chain {
			return audit.AppendResult{}, audit.Failure(audit.Conflict, errors.New("auditmemory: operation belongs to another attempt"))
		}
	} else {
		if !found || current.state != view.Attempt.Expected || !attemptBindingsIntersect(current.binding, binding) {
			return audit.AppendResult{}, audit.Failure(audit.Conflict, errors.New("auditmemory: attempt head changed"))
		}
	}
	typeKey := attemptTypeKey{catalog: transition.TypeExpected.Catalog, operation: transition.TypeExpected.Operation, replay: transition.TypeExpected.Replay}
	typeState, typeFound := target.attemptTypes[typeKey]
	if !typeFound {
		typeState = audit.AttemptTypeProjectionStateView{
			Catalog: transition.TypeExpected.Catalog, Operation: transition.TypeExpected.Operation,
			Policy: transition.TypeExpected.Policy, Replay: transition.TypeExpected.Replay,
		}
	}
	if transition.TypeExpected != (audit.AttemptTypeProjectionStateView{}) && typeState != transition.TypeExpected {
		return audit.AppendResult{}, audit.Failure(audit.Conflict, errors.New("auditmemory: attempt type head changed"))
	}
	limits := target.limits.View()
	result := view.Attempt.Candidate.Result
	if result.Sequence > limits.AttemptTransitions || result.TransitionBytes > limits.AttemptStateBytes || result.ExpiresAt.Sub(revision.Header.ObservedAt) > limits.AttemptOpenLifetime && transition.Kind == audit.AttemptStartedTransition {
		return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: attempt exceeds configured limits"))
	}
	target.sequence++
	positionBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(positionBytes, target.sequence)
	position, _ := audit.NewStorePosition(positionBytes)
	stored, err := audit.NewStoredHeader(audit.StoredHeaderData{
		Header: revision.Header, Intent: view.Intent, RecordedAt: clock().UTC(), Position: position,
		AttemptTransitionPresent: true, AttemptTransition: transition, AttemptProjection: result,
	})
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.Corrupt, err)
	}
	target.revisions[revision.Header.RevisionID] = revisionRecord{revision: view.Revision, stored: stored}
	transitions := []audit.ItemRef{result.Head}
	if found {
		transitions = append(slices.Clone(current.transitions), result.Head)
	}
	target.attempts[transition.Chain] = attemptRecord{state: result, transitions: transitions, binding: binding}
	if transition.Kind == audit.AttemptStartedTransition {
		target.attemptOps[attemptOperationKey{catalog: revision.Header.Catalog.ID, operation: transition.OperationID}] = transition.Chain
	}
	if transition.TypeExpected != (audit.AttemptTypeProjectionStateView{}) {
		target.attemptTypes[typeKey] = view.Attempt.Candidate.TypeResult
	}
	for _, key := range keys {
		target.attemptKeys[key] = revision.Header.RevisionID
	}
	return audit.NewAttemptAppendResult(request, stored, audit.Inserted, authority)
}

func attemptIdempotencyKeys(kind audit.IdempotencyDomainKind, catalog audit.CatalogID, chain audit.AttemptChainID, set audit.IdentityCommitmentSet) ([]attemptIdempotencyKey, bool) {
	if set.Domain() != audit.CommitIdempotency || len(set.Aliases()) == 0 {
		return nil, false
	}
	result := make([]attemptIdempotencyKey, 0, len(set.Aliases()))
	for _, alias := range set.Aliases() {
		description := alias.Description()
		wire := alias.Bytes()
		if len(wire) != 32 {
			return nil, false
		}
		key := attemptIdempotencyKey{kind: kind, catalog: catalog, chain: chain, algorithm: description.Algorithm, profile: description.Profile, keyID: description.KeyID}
		copy(key.commitment[:], wire)
		result = append(result, key)
	}
	return result, true
}

func findAttemptIdempotency(index map[attemptIdempotencyKey]audit.RevisionID, keys []attemptIdempotencyKey) (audit.RevisionID, bool, bool) {
	var result audit.RevisionID
	found := false
	for _, key := range keys {
		value, ok := index[key]
		if !ok {
			continue
		}
		if found && value != result {
			return audit.RevisionID{}, true, false
		}
		result = value
		found = true
	}
	return result, found, true
}

func attemptBindingsIntersect(left, right audit.AttemptIdentityAliasBinding) bool {
	return left.Chain() == right.Chain() && left.Operation() == right.Operation() && left.Policy() == right.Policy() && left.Replay() == right.Replay() && left.OperationID() == right.OperationID() && left.TargetPresent() == right.TargetPresent() && left.ScopePresent() == right.ScopePresent() && identitySetsIntersect(left.OwnerCommitments(), right.OwnerCommitments()) && (!left.TargetPresent() || identitySetsIntersect(left.TargetCommitments(), right.TargetCommitments())) && (!left.ScopePresent() || identitySetsIntersect(left.ScopeCommitments(), right.ScopeCommitments()))
}

func identitySetsIntersect(left, right audit.IdentityCommitmentSet) bool {
	for _, first := range left.Aliases() {
		for _, second := range right.Aliases() {
			if first.Description() == second.Description() && string(first.Bytes()) == string(second.Bytes()) {
				return true
			}
		}
	}
	return false
}

func cloneAttempts(input map[audit.AttemptChainID]attemptRecord) map[audit.AttemptChainID]attemptRecord {
	output := make(map[audit.AttemptChainID]attemptRecord, len(input))
	for key, value := range input {
		value.transitions = slices.Clone(value.transitions)
		output[key] = value
	}
	return output
}

func cloneAttemptOps(input map[attemptOperationKey]audit.AttemptChainID) map[attemptOperationKey]audit.AttemptChainID {
	output := make(map[attemptOperationKey]audit.AttemptChainID, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneAttemptTypes(input map[attemptTypeKey]audit.AttemptTypeProjectionStateView) map[attemptTypeKey]audit.AttemptTypeProjectionStateView {
	output := make(map[attemptTypeKey]audit.AttemptTypeProjectionStateView, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneAttemptKeys(input map[attemptIdempotencyKey]audit.RevisionID) map[attemptIdempotencyKey]audit.RevisionID {
	output := make(map[attemptIdempotencyKey]audit.RevisionID, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
