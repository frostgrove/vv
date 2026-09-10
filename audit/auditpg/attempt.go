package auditpg

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"github.com/frostgrove/vv/audit"
)

const maxAttemptProjectionWireBytes = 64 << 10

type attemptStateEnvelope struct {
	Version uint16                           `json:"version"`
	State   audit.AttemptProjectionStateView `json:"state"`
}

type attemptTypeStateEnvelope struct {
	Version uint16                               `json:"version"`
	State   audit.AttemptTypeProjectionStateView `json:"state"`
}

func (s *Store) AttemptState(ctx context.Context, query audit.AttemptStateQuery) (audit.AttemptStateResult, error) {
	ready, err := s.ready()
	if err != nil {
		return audit.AttemptStateResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return audit.AttemptStateResult{}, err
	}
	view := query.View()
	if !attemptStateQueryMatchesReady(view, ready) {
		return audit.AttemptStateResult{}, audit.Failure(audit.Refused, errors.New("auditpg: attempt state query is stale"))
	}
	tx, err := s.value.configured.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return audit.AttemptStateResult{}, classifySQL(err, false)
	}
	defer func() { _ = tx.Rollback() }()
	q := quoteIdentifier(s.value.configured.schema.Name)
	var sequence int64
	var transitionBytes int64
	var wireBytes int64
	err = tx.QueryRowContext(ctx, `SELECT sequence, transition_bytes, octet_length(state_wire)
FROM `+q+`.attempt_chains WHERE chain_id=$1 AND catalog_id=$2 AND operation_id=$3`, view.Chain[:], string(view.Catalog), view.OperationID[:]).Scan(&sequence, &transitionBytes, &wireBytes)
	if errors.Is(err, sql.ErrNoRows) {
		result, resultErr := audit.NewAttemptStateResult(query, audit.AttemptStateResultData{})
		if resultErr != nil {
			return audit.AttemptStateResult{}, resultErr
		}
		if err := tx.Commit(); err != nil {
			return audit.AttemptStateResult{}, classifySQL(err, false)
		}
		return result, nil
	}
	if err != nil {
		return audit.AttemptStateResult{}, classifySQL(err, false)
	}
	if sequence <= 0 || sequence > int64(view.MaxTransitions) || transitionBytes <= 0 || uint64(transitionBytes) > view.MaxBytes || wireBytes <= 0 || wireBytes > maxAttemptProjectionWireBytes {
		return audit.AttemptStateResult{}, audit.Failure(audit.Refused, errors.New("auditpg: attempt state exceeds query bounds"))
	}
	var stateWire []byte
	if err := tx.QueryRowContext(ctx, `SELECT state_wire FROM `+q+`.attempt_chains
WHERE chain_id=$1 AND catalog_id=$2 AND operation_id=$3`, view.Chain[:], string(view.Catalog), view.OperationID[:]).Scan(&stateWire); err != nil {
		return audit.AttemptStateResult{}, classifySQL(err, false)
	}
	state, err := decodeAttemptState(stateWire)
	if err != nil || int64(state.Sequence) != sequence || state.TransitionBytes != uint64(transitionBytes) {
		return audit.AttemptStateResult{}, audit.Failure(audit.Corrupt, errors.New("auditpg: attempt state is malformed"))
	}
	rows, err := tx.QueryContext(ctx, `SELECT t.revision_id, t.ordinal, r.catalog_id, r.catalog_generation
FROM `+q+`.attempt_transitions t JOIN `+q+`.revisions r ON r.revision_id=t.revision_id
WHERE t.chain_id=$1 ORDER BY t.sequence LIMIT $2`, view.Chain[:], int(view.MaxTransitions)+1)
	if err != nil {
		return audit.AttemptStateResult{}, classifySQL(err, false)
	}
	defer rows.Close()
	transitions := make([]audit.ItemRef, 0, int(sequence))
	for rows.Next() {
		if len(transitions) >= int(view.MaxTransitions) {
			return audit.AttemptStateResult{}, audit.Failure(audit.Corrupt, errors.New("auditpg: attempt transition inventory exceeds its state"))
		}
		var revisionBytes []byte
		var ordinal int64
		var catalogID string
		var generation int64
		if err := rows.Scan(&revisionBytes, &ordinal, &catalogID, &generation); err != nil {
			return audit.AttemptStateResult{}, classifySQL(err, false)
		}
		revision, ok := id16[audit.RevisionID](revisionBytes)
		catalog, okCatalog := attemptCatalogRef(view.Catalogs, audit.CatalogID(catalogID), generation)
		if !ok || !okCatalog || ordinal < 0 || ordinal > int64(^uint16(0)) {
			return audit.AttemptStateResult{}, audit.Failure(audit.Corrupt, errors.New("auditpg: attempt transition reference is malformed"))
		}
		transitions = append(transitions, audit.ItemRef{Revision: audit.RevisionRef{Catalog: catalog, Revision: revision}, Ordinal: uint16(ordinal)})
	}
	if err := rows.Err(); err != nil {
		return audit.AttemptStateResult{}, classifySQL(err, false)
	}
	if err := rows.Close(); err != nil {
		return audit.AttemptStateResult{}, classifySQL(err, false)
	}
	result, err := audit.NewAttemptStateResult(query, audit.AttemptStateResultData{State: state, Transitions: transitions})
	if err != nil {
		return audit.AttemptStateResult{}, audit.Failure(audit.Corrupt, err)
	}
	if err := tx.Commit(); err != nil {
		return audit.AttemptStateResult{}, classifySQL(err, false)
	}
	return result, nil
}

func (s *Store) AttemptTypeState(ctx context.Context, query audit.AttemptTypeStateQuery) (audit.AttemptTypeStateResult, error) {
	ready, err := s.ready()
	if err != nil {
		return audit.AttemptTypeStateResult{}, err
	}
	return attemptTypeStateFrom(ctx, query, ready, s.value.configured.schema.Name, s.value.configured.db)
}

type attemptTypeRowSource interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func attemptTypeStateFrom(ctx context.Context, query audit.AttemptTypeStateQuery, ready readiness, schema string, source attemptTypeRowSource) (audit.AttemptTypeStateResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.AttemptTypeStateResult{}, err
	}
	view := query.View()
	if !attemptTypeStateQueryMatchesReady(view, ready) {
		return audit.AttemptTypeStateResult{}, audit.Failure(audit.Refused, errors.New("auditpg: attempt type query is stale"))
	}
	selector := view.Type
	q := quoteIdentifier(schema)
	var wire []byte
	err := source.QueryRowContext(ctx, `SELECT state_wire FROM `+q+`.attempt_type_states
WHERE catalog_id=$1 AND operation=$2 AND policy=$3 AND replay=$4`, string(selector.Catalog), string(selector.Operation), selector.Policy[:], selector.Replay[:]).Scan(&wire)
	state := audit.AttemptTypeProjectionStateView{
		Catalog: selector.Catalog, Operation: selector.Operation, Policy: selector.Policy, Replay: selector.Replay,
	}
	if err == nil {
		if len(wire) == 0 || len(wire) > maxAttemptProjectionWireBytes {
			return audit.AttemptTypeStateResult{}, audit.Failure(audit.Corrupt, errors.New("auditpg: attempt type state exceeds its bound"))
		}
		state, err = decodeAttemptTypeState(wire)
		if err != nil {
			return audit.AttemptTypeStateResult{}, audit.Failure(audit.Corrupt, errors.New("auditpg: attempt type state is malformed"))
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return audit.AttemptTypeStateResult{}, classifySQL(err, false)
	}
	result, err := audit.NewAttemptTypeStateResult(query, state)
	if err != nil {
		return audit.AttemptTypeStateResult{}, audit.Failure(audit.Corrupt, err)
	}
	return result, nil
}

func attemptStateQueryMatchesReady(view audit.AttemptStateQueryView, ready readiness) bool {
	return ready.catalogs.HasActive() && view.Log == ready.logID && view.Catalog == ready.catalogs.Active().ID && slices.Contains(view.Catalogs, ready.catalogs.Active())
}

func attemptTypeStateQueryMatchesReady(view audit.AttemptTypeStateQueryView, ready readiness) bool {
	return ready.catalogs.HasActive() && view.Log == ready.logID && view.Type.Catalog == ready.catalogs.Active().ID && slices.Contains(view.Catalogs, ready.catalogs.Active())
}

func attemptCatalogRef(catalogs []audit.CatalogRef, id audit.CatalogID, generation int64) (audit.CatalogRef, bool) {
	if generation <= 0 {
		return audit.CatalogRef{}, false
	}
	for _, catalog := range catalogs {
		if catalog.ID == id && catalog.Generation == audit.CatalogGeneration(generation) {
			return catalog, true
		}
	}
	return audit.CatalogRef{}, false
}

func encodeAttemptState(state audit.AttemptProjectionStateView) ([]byte, error) {
	wire, err := json.Marshal(attemptStateEnvelope{Version: 1, State: state})
	if err != nil || len(wire) == 0 || len(wire) > maxAttemptProjectionWireBytes {
		return nil, errors.New("auditpg: attempt projection cannot be encoded")
	}
	return wire, nil
}

func decodeAttemptState(wire []byte) (audit.AttemptProjectionStateView, error) {
	var envelope attemptStateEnvelope
	if len(wire) == 0 || len(wire) > maxAttemptProjectionWireBytes || json.Unmarshal(wire, &envelope) != nil || envelope.Version != 1 {
		return audit.AttemptProjectionStateView{}, errors.New("auditpg: attempt projection wire is invalid")
	}
	canonical, err := encodeAttemptState(envelope.State)
	if err != nil || !bytes.Equal(canonical, wire) {
		return audit.AttemptProjectionStateView{}, errors.New("auditpg: attempt projection wire is not canonical")
	}
	return envelope.State, nil
}

func encodeAttemptTypeState(state audit.AttemptTypeProjectionStateView) ([]byte, error) {
	wire, err := json.Marshal(attemptTypeStateEnvelope{Version: 1, State: state})
	if err != nil || len(wire) == 0 || len(wire) > maxAttemptProjectionWireBytes {
		return nil, errors.New("auditpg: attempt type projection cannot be encoded")
	}
	return wire, nil
}

func decodeAttemptTypeState(wire []byte) (audit.AttemptTypeProjectionStateView, error) {
	var envelope attemptTypeStateEnvelope
	if len(wire) == 0 || len(wire) > maxAttemptProjectionWireBytes || json.Unmarshal(wire, &envelope) != nil || envelope.Version != 1 {
		return audit.AttemptTypeProjectionStateView{}, errors.New("auditpg: attempt type projection wire is invalid")
	}
	canonical, err := encodeAttemptTypeState(envelope.State)
	if err != nil || !bytes.Equal(canonical, wire) {
		return audit.AttemptTypeProjectionStateView{}, errors.New("auditpg: attempt type projection wire is not canonical")
	}
	return envelope.State, nil
}

func storedAttemptProjection(item audit.ItemWireView) audit.AttemptProjectionStateView {
	value := item.Attempt.Result
	return audit.AttemptProjectionStateView{
		Present: value.Present, Chain: value.Chain, Policy: value.Policy, Replay: value.Replay,
		Operation: value.Operation, OperationID: value.OperationID,
		TargetPresent: value.TargetPresent, Target: value.Target,
		ScopePresent: value.ScopePresent, Scope: value.Scope, Owner: value.Owner,
		State: value.State, Sequence: value.Sequence, CheckpointCount: value.CheckpointCount,
		TransitionBytes: value.TransitionBytes, Start: value.Start, Head: value.Head,
		Leaf: item.Leaf, ExpiresAt: value.ExpiresAt,
	}
}

func (e *execution) appendAttempt(ctx context.Context, schema string, request audit.AppendRequest, wire []byte) (audit.AppendResult, error) {
	requestView := request.View()
	revision := requestView.Revision.View()
	transition := revision.Items[0].Attempt
	replayed, claimed, err := e.claimAttemptIdempotency(ctx, schema, requestView)
	if err != nil {
		return audit.AppendResult{}, err
	}
	if claimed {
		return audit.NewAttemptAppendResult(request, replayed, audit.Replayed, e.authority)
	}
	stored, found, err := loadStoredRevision(ctx, e.tx, schema, revision.Header.RevisionID, true)
	if err != nil {
		return audit.AppendResult{}, err
	}
	if found {
		if stored.Intent() != requestView.Intent {
			return audit.AppendResult{}, audit.Failure(audit.Conflict, errors.New("auditpg: attempt revision identity changed meaning"))
		}
		return audit.NewAttemptAppendResult(request, stored, audit.Replayed, e.authority)
	}
	if err := e.lockAttemptType(ctx, schema, transition); err != nil {
		return audit.AppendResult{}, err
	}
	if err := e.lockAttemptChain(ctx, schema, requestView); err != nil {
		return audit.AppendResult{}, err
	}
	if err := e.bindAttemptAliases(ctx, schema, requestView); err != nil {
		return audit.AppendResult{}, err
	}
	var position int64
	var recordedAt sql.NullTime
	err = e.tx.QueryRowContext(ctx, `INSERT INTO `+schema+`.revisions
(revision_id, catalog_id, catalog_generation, operation, semantic, intent, wire)
VALUES ($1,$2,$3,$4,$5,$6,$7)
RETURNING position, recorded_at`, revision.Header.RevisionID[:], string(revision.Header.Catalog.ID), int64(revision.Header.Catalog.Generation), string(revision.Header.Operation), revision.Header.Semantic[:], requestView.Intent[:], wire).Scan(&position, &recordedAt)
	if err != nil {
		return audit.AppendResult{}, classifySQL(err, true)
	}
	if !recordedAt.Valid {
		return audit.AppendResult{}, audit.Failure(audit.Corrupt, errors.New("auditpg: database returned no attempt record time"))
	}
	item := revision.Items[0]
	var previous any
	if item.Previous != (audit.LeafDigest{}) {
		previous = item.Previous[:]
	}
	if _, err := e.tx.ExecContext(ctx, `INSERT INTO `+schema+`.attempt_transitions
(revision_id, ordinal, chain_id, sequence, kind, previous_leaf, leaf)
VALUES ($1,$2,$3,$4,$5,$6,$7)`, revision.Header.RevisionID[:], int(item.Ordinal), transition.Chain[:], int(transition.Sequence), int(transition.Kind), previous, item.Leaf[:]); err != nil {
		return audit.AppendResult{}, classifySQL(err, true)
	}
	if err := e.advanceAttemptChain(ctx, schema, requestView); err != nil {
		return audit.AppendResult{}, err
	}
	if err := e.advanceAttemptType(ctx, schema, transition, requestView.Attempt.Candidate.TypeResult); err != nil {
		return audit.AppendResult{}, err
	}
	storePosition, err := positionOf(position)
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.Corrupt, err)
	}
	stored, err = audit.NewStoredHeader(audit.StoredHeaderData{
		Header: revision.Header, Intent: requestView.Intent, RecordedAt: recordedAt.Time.UTC(), Position: storePosition,
		AttemptTransitionPresent: true, AttemptTransition: transition, AttemptProjection: requestView.Attempt.Candidate.Result,
	})
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.Corrupt, err)
	}
	return audit.NewAttemptAppendResult(request, stored, audit.Inserted, e.authority)
}

func (e *execution) lockAttemptChain(ctx context.Context, schema string, request audit.AppendRequestView) error {
	transition := request.Revision.View().Items[0].Attempt
	if transition.Kind == audit.AttemptStartedTransition {
		wire, err := encodeAttemptState(request.Attempt.Candidate.Result)
		if err != nil {
			return audit.Failure(audit.Refused, err)
		}
		result, err := e.tx.ExecContext(ctx, `INSERT INTO `+schema+`.attempt_chains
(chain_id, catalog_id, operation, operation_id, policy, replay, state, sequence, checkpoint_count, transition_bytes, expires_at, state_wire)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT DO NOTHING`,
			transition.Chain[:], string(transition.TypeExpected.Catalog), string(transition.Operation), transition.OperationID[:], transition.Policy[:], transition.Replay[:],
			int(request.Attempt.Candidate.Result.State), int(request.Attempt.Candidate.Result.Sequence), int(request.Attempt.Candidate.Result.CheckpointCount), int64(request.Attempt.Candidate.Result.TransitionBytes), request.Attempt.Candidate.Result.ExpiresAt, wire)
		if err != nil {
			return classifySQL(err, true)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return audit.Failure(audit.Conflict, errors.New("auditpg: attempt already exists"))
		}
		return nil
	}
	current, currentWire, found, err := e.loadAttemptStateLocked(ctx, schema, transition.Chain)
	if err != nil {
		return err
	}
	if !found || current != request.Attempt.Expected {
		return audit.Failure(audit.Conflict, errors.New("auditpg: attempt head changed"))
	}
	if len(currentWire) == 0 {
		return audit.Failure(audit.Corrupt, errors.New("auditpg: attempt state wire is absent"))
	}
	return nil
}

func (e *execution) advanceAttemptChain(ctx context.Context, schema string, request audit.AppendRequestView) error {
	transition := request.Revision.View().Items[0].Attempt
	if transition.Kind == audit.AttemptStartedTransition {
		return nil
	}
	next := request.Attempt.Candidate.Result
	nextWire, err := encodeAttemptState(next)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	expectedWire, err := encodeAttemptState(request.Attempt.Expected)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	result, err := e.tx.ExecContext(ctx, `UPDATE `+schema+`.attempt_chains
SET state=$1, sequence=$2, checkpoint_count=$3, transition_bytes=$4, expires_at=$5, state_wire=$6
WHERE chain_id=$7 AND state_wire=$8`, int(next.State), int(next.Sequence), int(next.CheckpointCount), int64(next.TransitionBytes), next.ExpiresAt, nextWire, transition.Chain[:], expectedWire)
	if err != nil {
		return classifySQL(err, true)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return audit.Failure(audit.Conflict, errors.New("auditpg: attempt state CAS failed"))
	}
	return nil
}

func (e *execution) loadAttemptStateLocked(ctx context.Context, schema string, chain audit.AttemptChainID) (audit.AttemptProjectionStateView, []byte, bool, error) {
	var wire []byte
	err := e.tx.QueryRowContext(ctx, `SELECT state_wire FROM `+schema+`.attempt_chains WHERE chain_id=$1 FOR UPDATE`, chain[:]).Scan(&wire)
	if errors.Is(err, sql.ErrNoRows) {
		return audit.AttemptProjectionStateView{}, nil, false, nil
	}
	if err != nil {
		return audit.AttemptProjectionStateView{}, nil, false, classifySQL(err, false)
	}
	state, err := decodeAttemptState(wire)
	if err != nil {
		return audit.AttemptProjectionStateView{}, nil, false, audit.Failure(audit.Corrupt, err)
	}
	return state, wire, true, nil
}

func (e *execution) lockAttemptType(ctx context.Context, schema string, transition audit.AttemptTransitionWireView) error {
	if transition.TypeExpected == (audit.AttemptTypeProjectionStateView{}) {
		return nil
	}
	expectedWire, err := encodeAttemptTypeState(transition.TypeExpected)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	selector := transition.TypeExpected
	if _, err := e.tx.ExecContext(ctx, `INSERT INTO `+schema+`.attempt_type_states
(catalog_id, operation, policy, replay, unsettled, state_wire)
VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, string(selector.Catalog), string(selector.Operation), selector.Policy[:], selector.Replay[:], strconv.FormatUint(selector.Unsettled, 10), expectedWire); err != nil {
		return classifySQL(err, true)
	}
	var currentWire []byte
	if err := e.tx.QueryRowContext(ctx, `SELECT state_wire FROM `+schema+`.attempt_type_states
WHERE catalog_id=$1 AND operation=$2 AND policy=$3 AND replay=$4 FOR UPDATE`, string(selector.Catalog), string(selector.Operation), selector.Policy[:], selector.Replay[:]).Scan(&currentWire); err != nil {
		return classifySQL(err, false)
	}
	current, err := decodeAttemptTypeState(currentWire)
	if err != nil {
		return audit.Failure(audit.Corrupt, err)
	}
	if current != transition.TypeExpected {
		return audit.Failure(audit.Conflict, errors.New("auditpg: attempt type head changed"))
	}
	return nil
}

func (e *execution) advanceAttemptType(ctx context.Context, schema string, transition audit.AttemptTransitionWireView, next audit.AttemptTypeProjectionStateView) error {
	if transition.TypeExpected == (audit.AttemptTypeProjectionStateView{}) {
		return nil
	}
	nextWire, err := encodeAttemptTypeState(next)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	expectedWire, err := encodeAttemptTypeState(transition.TypeExpected)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	selector := transition.TypeExpected
	result, err := e.tx.ExecContext(ctx, `UPDATE `+schema+`.attempt_type_states
SET unsettled=$1, state_wire=$2 WHERE catalog_id=$3 AND operation=$4 AND policy=$5 AND replay=$6 AND state_wire=$7`,
		strconv.FormatUint(next.Unsettled, 10), nextWire, string(selector.Catalog), string(selector.Operation), selector.Policy[:], selector.Replay[:], expectedWire)
	if err != nil {
		return classifySQL(err, true)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return audit.Failure(audit.Conflict, errors.New("auditpg: attempt type state CAS failed"))
	}
	return nil
}

func (e *execution) claimAttemptIdempotency(ctx context.Context, schema string, request audit.AppendRequestView) (audit.StoredHeader, bool, error) {
	revision := request.Revision.View()
	transition := revision.Items[0].Attempt
	domain, chain, ok := attemptIdempotencyDomain(transition)
	if !ok || request.Idempotency.Domain() != audit.CommitIdempotency {
		return audit.StoredHeader{}, false, audit.Failure(audit.Refused, errors.New("auditpg: attempt idempotency is invalid"))
	}
	aliases := request.Idempotency.Aliases()
	if len(aliases) == 0 {
		return audit.StoredHeader{}, false, audit.Failure(audit.Refused, errors.New("auditpg: attempt idempotency is empty"))
	}
	active := request.Idempotency.Active()
	ordered := []audit.IdentityCommitment{active}
	for _, alias := range aliases {
		if alias.Description() != active.Description() || !bytes.Equal(alias.Bytes(), active.Bytes()) {
			ordered = append(ordered, alias)
		}
	}
	selected, existing, err := e.findAttemptIdempotency(ctx, schema, domain, revision.Header.Catalog.ID, chain, revision.Header.Semantic, aliases)
	if err != nil {
		return audit.StoredHeader{}, false, err
	}
	if !existing {
		selected = revision.Header.RevisionID
	}
	for index, alias := range ordered {
		description := alias.Description()
		commitment := alias.Bytes()
		if len(commitment) != 32 {
			return audit.StoredHeader{}, false, audit.Failure(audit.Refused, errors.New("auditpg: attempt idempotency commitment is malformed"))
		}
		if _, err := e.tx.ExecContext(ctx, `INSERT INTO `+schema+`.attempt_idempotency
(domain, catalog_id, operation, chain_id, algorithm, profile, key_id, commitment, semantic, revision_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, int(domain), string(revision.Header.Catalog.ID), string(transition.Operation), chain[:], description.Algorithm, description.Profile, description.KeyID, commitment, revision.Header.Semantic[:], selected[:]); err != nil {
			return audit.StoredHeader{}, false, classifySQL(err, true)
		}
		var semanticBytes, revisionBytes []byte
		if err := e.tx.QueryRowContext(ctx, `SELECT semantic, revision_id FROM `+schema+`.attempt_idempotency
WHERE domain=$1 AND catalog_id=$2 AND chain_id=$3 AND algorithm=$4 AND profile=$5 AND key_id=$6 AND commitment=$7 FOR UPDATE`,
			int(domain), string(revision.Header.Catalog.ID), chain[:], description.Algorithm, description.Profile, description.KeyID, commitment).Scan(&semanticBytes, &revisionBytes); err != nil {
			return audit.StoredHeader{}, false, classifySQL(err, false)
		}
		storedRevision, valid := id16[audit.RevisionID](revisionBytes)
		if !valid || !bytes.Equal(semanticBytes, revision.Header.Semantic[:]) {
			return audit.StoredHeader{}, false, audit.Failure(audit.Conflict, errors.New("auditpg: attempt idempotency changed meaning"))
		}
		if index == 0 && !existing {
			selected = storedRevision
		} else if storedRevision != selected {
			return audit.StoredHeader{}, false, audit.Failure(audit.Corrupt, errors.New("auditpg: attempt idempotency aliases diverge"))
		}
	}
	if selected == revision.Header.RevisionID {
		return audit.StoredHeader{}, false, nil
	}
	stored, found, err := loadStoredRevision(ctx, e.tx, schema, selected, true)
	if err != nil {
		return audit.StoredHeader{}, false, err
	}
	if !found || stored.Revision().Semantic != revision.Header.Semantic {
		return audit.StoredHeader{}, false, audit.Failure(audit.Corrupt, errors.New("auditpg: attempt idempotency has no matching revision"))
	}
	prior, present := stored.AttemptTransition()
	if !present || prior.Chain != transition.Chain || prior.Kind != transition.Kind || prior.OperationID != transition.OperationID || prior.Policy != transition.Policy || prior.Replay != transition.Replay {
		return audit.StoredHeader{}, false, audit.Failure(audit.Conflict, errors.New("auditpg: attempt idempotency changed transition"))
	}
	return stored, true, nil
}

func (e *execution) findAttemptIdempotency(ctx context.Context, schema string, domain audit.IdempotencyDomainKind, catalog audit.CatalogID, chain audit.AttemptChainID, semantic audit.SemanticDigest, aliases []audit.IdentityCommitment) (audit.RevisionID, bool, error) {
	var selected audit.RevisionID
	found := false
	for _, alias := range aliases {
		description := alias.Description()
		var semanticBytes, revisionBytes []byte
		err := e.tx.QueryRowContext(ctx, `SELECT semantic, revision_id FROM `+schema+`.attempt_idempotency
WHERE domain=$1 AND catalog_id=$2 AND chain_id=$3 AND algorithm=$4 AND profile=$5 AND key_id=$6 AND commitment=$7 FOR UPDATE`,
			int(domain), string(catalog), chain[:], description.Algorithm, description.Profile, description.KeyID, alias.Bytes()).Scan(&semanticBytes, &revisionBytes)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return audit.RevisionID{}, false, classifySQL(err, false)
		}
		revision, valid := id16[audit.RevisionID](revisionBytes)
		if !valid || !bytes.Equal(semanticBytes, semantic[:]) {
			return audit.RevisionID{}, false, audit.Failure(audit.Conflict, errors.New("auditpg: attempt idempotency changed meaning"))
		}
		if found && revision != selected {
			return audit.RevisionID{}, false, audit.Failure(audit.Corrupt, errors.New("auditpg: attempt idempotency aliases diverge"))
		}
		selected, found = revision, true
	}
	return selected, found, nil
}

func attemptIdempotencyDomain(transition audit.AttemptTransitionWireView) (audit.IdempotencyDomainKind, audit.AttemptChainID, bool) {
	switch transition.Kind {
	case audit.AttemptStartedTransition:
		return audit.AttemptStartIdempotencyDomain, audit.AttemptChainID{}, true
	case audit.AttemptCheckpointTransition:
		return audit.AttemptCheckpointIdempotencyDomain, transition.Chain, true
	case audit.AttemptOutcomeUnknownTransition, audit.AttemptSucceededTransition, audit.AttemptFailedTransition, audit.AttemptCancelledTransition, audit.AttemptAbandonedTransition:
		return audit.AttemptFinishIdempotencyDomain, transition.Chain, true
	default:
		return 0, audit.AttemptChainID{}, false
	}
}

func (e *execution) bindAttemptAliases(ctx context.Context, schema string, request audit.AppendRequestView) error {
	binding := request.Attempts[0]
	revision := request.Revision.View()
	transition := revision.Items[0].Attempt
	catalog := revision.Header.Catalog.ID
	policy := binding.Policy()
	replay := binding.Replay()
	operationID := binding.OperationID()
	chain := binding.Chain()
	sets := []audit.IdentityCommitmentSet{binding.OwnerCommitments()}
	if binding.TargetPresent() {
		sets = append(sets, binding.TargetCommitments())
	}
	if binding.ScopePresent() {
		sets = append(sets, binding.ScopeCommitments())
	}
	if transition.Kind != audit.AttemptStartedTransition {
		for _, set := range sets {
			matched, err := e.attemptAliasIntersects(ctx, schema, catalog, binding, set)
			if err != nil {
				return err
			}
			if !matched {
				return audit.Failure(audit.Conflict, errors.New("auditpg: attempt identity continuity changed"))
			}
		}
	}
	for _, set := range sets {
		for _, alias := range set.Aliases() {
			description := alias.Description()
			commitment := alias.Bytes()
			if len(commitment) != 32 {
				return audit.Failure(audit.Refused, errors.New("auditpg: attempt identity alias is malformed"))
			}
			if _, err := e.tx.ExecContext(ctx, `INSERT INTO `+schema+`.attempt_identity_aliases
(catalog_id, operation, policy, replay, domain, algorithm, profile, key_id, commitment, operation_id, chain_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT DO NOTHING`, string(catalog), string(binding.Operation()), policy[:], replay[:], int(set.Domain()), description.Algorithm, description.Profile, description.KeyID, commitment, operationID[:], chain[:]); err != nil {
				return classifySQL(err, true)
			}
			var operationBytes, chainBytes []byte
			if err := e.tx.QueryRowContext(ctx, `SELECT operation_id, chain_id FROM `+schema+`.attempt_identity_aliases
WHERE catalog_id=$1 AND operation=$2 AND policy=$3 AND replay=$4 AND domain=$5 AND algorithm=$6 AND profile=$7 AND key_id=$8 AND commitment=$9 AND operation_id=$10 AND chain_id=$11`,
				string(catalog), string(binding.Operation()), policy[:], replay[:], int(set.Domain()), description.Algorithm, description.Profile, description.KeyID, commitment, operationID[:], chain[:]).Scan(&operationBytes, &chainBytes); err != nil {
				return classifySQL(err, false)
			}
			operationID, validOperation := id16[audit.OperationID](operationBytes)
			chainID, validChain := digest32[audit.AttemptChainID](chainBytes)
			if !validOperation || !validChain || operationID != binding.OperationID() || chainID != binding.Chain() {
				return audit.Failure(audit.Conflict, errors.New("auditpg: attempt identity alias belongs to another chain"))
			}
		}
	}
	return nil
}

func (e *execution) attemptAliasIntersects(ctx context.Context, schema string, catalog audit.CatalogID, binding audit.AttemptIdentityAliasBinding, set audit.IdentityCommitmentSet) (bool, error) {
	policy := binding.Policy()
	replay := binding.Replay()
	operationID := binding.OperationID()
	chain := binding.Chain()
	for _, alias := range set.Aliases() {
		description := alias.Description()
		var present bool
		err := e.tx.QueryRowContext(ctx, `SELECT EXISTS (
SELECT 1 FROM `+schema+`.attempt_identity_aliases
WHERE catalog_id=$1 AND operation=$2 AND policy=$3 AND replay=$4 AND domain=$5 AND algorithm=$6 AND profile=$7 AND key_id=$8 AND commitment=$9 AND operation_id=$10 AND chain_id=$11)`,
			string(catalog), string(binding.Operation()), policy[:], replay[:], int(set.Domain()), description.Algorithm, description.Profile, description.KeyID, alias.Bytes(), operationID[:], chain[:]).Scan(&present)
		if err != nil {
			return false, classifySQL(err, false)
		}
		if present {
			return true, nil
		}
	}
	return false, nil
}

var _ audit.AttemptLog = (*Store)(nil)
var _ audit.AttemptTypeState = (*Store)(nil)
