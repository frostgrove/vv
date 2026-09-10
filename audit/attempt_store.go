package audit

import (
	"context"
	"slices"
)

type AttemptLog interface {
	StoreInfo
	AttemptState(context.Context, AttemptStateQuery) (AttemptStateResult, error)
}

type AttemptTypeState interface {
	StoreInfo
	AttemptTypeState(context.Context, AttemptTypeStateQuery) (AttemptTypeStateResult, error)
	LockAttemptType(context.Context, AttemptTypeStateQuery) (AttemptTypeLease, error)
}

type AttemptTypeLease interface {
	State() AttemptTypeStateResult
	Append(context.Context, Writer, AppendRequest) (AppendResult, error)
	Release()
}

type AttemptStateQueryView struct {
	Log            LogID
	Catalogs       []CatalogRef
	Catalog        CatalogID
	OperationID    OperationID
	Chain          AttemptChainID
	MaxTransitions uint16
	MaxBytes       uint64
}

type attemptStateQueryOrigin struct{ marker byte }

type attemptStateQuery struct {
	view   AttemptStateQueryView
	origin *attemptStateQueryOrigin
}

type AttemptStateQuery struct{ value attemptStateQuery }

func (q AttemptStateQuery) View() AttemptStateQueryView {
	view := q.value.view
	view.Catalogs = slices.Clone(view.Catalogs)
	return view
}

func newAttemptStateQuery(view AttemptStateQueryView) (AttemptStateQuery, error) {
	query := AttemptStateQuery{value: attemptStateQuery{view: view, origin: &attemptStateQueryOrigin{}}}
	query.value.view.Catalogs = slices.Clone(view.Catalogs)
	if !validAttemptStateQuery(query) {
		return AttemptStateQuery{}, auditErrorAt(ErrInvalid, "attempt.state_query")
	}
	return query, nil
}

type AttemptStateResultData struct {
	State       AttemptProjectionStateView
	Transitions []ItemRef
}

type attemptStateResult struct {
	data   AttemptStateResultData
	origin *attemptStateQueryOrigin
}

type AttemptStateResult struct{ value attemptStateResult }

func NewAttemptStateResult(query AttemptStateQuery, data AttemptStateResultData) (AttemptStateResult, error) {
	data.Transitions = slices.Clone(data.Transitions)
	result := AttemptStateResult{value: attemptStateResult{data: data, origin: query.value.origin}}
	if err := validateAttemptStateResult(query, result); err != nil {
		return AttemptStateResult{}, err
	}
	return result, nil
}

func (r AttemptStateResult) State() AttemptProjectionStateView { return r.value.data.State }
func (r AttemptStateResult) Transitions() []ItemRef            { return slices.Clone(r.value.data.Transitions) }

func validAttemptStateQuery(query AttemptStateQuery) bool {
	view := query.value.view
	if query.value.origin == nil || view.Log == (LogID{}) || !validSemanticName(string(view.Catalog)) || view.OperationID == (OperationID{}) || view.Chain == (AttemptChainID{}) || view.MaxTransitions == 0 || view.MaxTransitions > MaxAttemptTransitions || view.MaxBytes == 0 || view.MaxBytes > MaxAttemptStateBytes || !validExactCatalogs(view.Catalogs) {
		return false
	}
	for _, catalog := range view.Catalogs {
		if catalog.ID == view.Catalog {
			return true
		}
	}
	return false
}

func validateAttemptStateResult(query AttemptStateQuery, result AttemptStateResult) error {
	if !validAttemptStateQuery(query) || result.value.origin != query.value.origin {
		return auditErrorAt(ErrIntegrity, "attempt.state_result")
	}
	view := query.value.view
	state := result.value.data.State
	transitions := result.value.data.Transitions
	if !state.Present {
		if state != (AttemptProjectionStateView{}) || len(transitions) != 0 {
			return auditErrorAt(ErrIntegrity, "attempt.state_result")
		}
		return nil
	}
	if !validAttemptProjectionState(state) || state.Chain != view.Chain || state.OperationID != view.OperationID || len(transitions) == 0 || len(transitions) > int(view.MaxTransitions) || state.Sequence != uint16(len(transitions)) || state.TransitionBytes > view.MaxBytes || transitions[0] != state.Start || transitions[len(transitions)-1] != state.Head {
		return auditErrorAt(ErrIntegrity, "attempt.state_result")
	}
	seen := make(map[ItemRef]struct{}, len(transitions))
	for _, reference := range transitions {
		if !reference.valid() || reference.Revision.Catalog.ID != view.Catalog || !slices.Contains(view.Catalogs, reference.Revision.Catalog) {
			return auditErrorAt(ErrIntegrity, "attempt.state_result")
		}
		if _, duplicate := seen[reference]; duplicate {
			return auditErrorAt(ErrIntegrity, "attempt.state_result")
		}
		seen[reference] = struct{}{}
	}
	return nil
}

type AttemptTypeSelectorView struct {
	Catalog   CatalogID
	Operation OperationName
	Policy    AttemptPolicyFingerprint
	Replay    AttemptReplayFingerprint
}

type AttemptTypeStateQueryView struct {
	Log      LogID
	Catalogs []CatalogRef
	Type     AttemptTypeSelectorView
}

type attemptTypeStateQueryOrigin struct{ marker byte }

type attemptTypeStateQuery struct {
	view   AttemptTypeStateQueryView
	origin *attemptTypeStateQueryOrigin
}

type AttemptTypeStateQuery struct{ value attemptTypeStateQuery }

func (q AttemptTypeStateQuery) View() AttemptTypeStateQueryView {
	view := q.value.view
	view.Catalogs = slices.Clone(view.Catalogs)
	return view
}

func newAttemptTypeStateQuery(view AttemptTypeStateQueryView) (AttemptTypeStateQuery, error) {
	query := AttemptTypeStateQuery{value: attemptTypeStateQuery{view: view, origin: &attemptTypeStateQueryOrigin{}}}
	query.value.view.Catalogs = slices.Clone(view.Catalogs)
	if !validAttemptTypeStateQuery(query) {
		return AttemptTypeStateQuery{}, auditErrorAt(ErrInvalid, "attempt.type_query")
	}
	return query, nil
}

type attemptTypeStateResult struct {
	state  AttemptTypeProjectionStateView
	origin *attemptTypeStateQueryOrigin
}

type AttemptTypeStateResult struct{ value attemptTypeStateResult }

func NewAttemptTypeStateResult(query AttemptTypeStateQuery, state AttemptTypeProjectionStateView) (AttemptTypeStateResult, error) {
	result := AttemptTypeStateResult{value: attemptTypeStateResult{state: state, origin: query.value.origin}}
	if err := validateAttemptTypeStateResult(query, result); err != nil {
		return AttemptTypeStateResult{}, err
	}
	return result, nil
}

func (r AttemptTypeStateResult) State() AttemptTypeProjectionStateView { return r.value.state }

func validAttemptTypeStateQuery(query AttemptTypeStateQuery) bool {
	view := query.value.view
	selector := view.Type
	if query.value.origin == nil || view.Log == (LogID{}) || !validExactCatalogs(view.Catalogs) || !validSemanticName(string(selector.Catalog)) || !validSemanticName(string(selector.Operation)) || selector.Policy == (AttemptPolicyFingerprint{}) || selector.Replay == (AttemptReplayFingerprint{}) {
		return false
	}
	for _, catalog := range view.Catalogs {
		if catalog.ID == selector.Catalog {
			return true
		}
	}
	return false
}

func validateAttemptTypeStateResult(query AttemptTypeStateQuery, result AttemptTypeStateResult) error {
	if !validAttemptTypeStateQuery(query) || result.value.origin != query.value.origin {
		return auditErrorAt(ErrIntegrity, "attempt.type_result")
	}
	selector := query.value.view.Type
	state := result.value.state
	if state.Catalog != selector.Catalog || state.Operation != selector.Operation || state.Policy != selector.Policy || state.Replay != selector.Replay {
		return auditErrorAt(ErrIntegrity, "attempt.type_result")
	}
	if (state.Head == (ItemRef{})) != (state.Leaf == (LeafDigest{})) || state.Head != (ItemRef{}) && !state.Head.valid() {
		return auditErrorAt(ErrIntegrity, "attempt.type_result")
	}
	return nil
}

func canonicalAttemptTypeState(selector AttemptTypeSelectorView) AttemptTypeProjectionStateView {
	return AttemptTypeProjectionStateView{
		Catalog: selector.Catalog, Operation: selector.Operation, Policy: selector.Policy, Replay: selector.Replay,
	}
}

func validAttemptProjectionState(state AttemptProjectionStateView) bool {
	if !state.Present || state.Chain == (AttemptChainID{}) || state.Policy == (AttemptPolicyFingerprint{}) || state.Replay == (AttemptReplayFingerprint{}) || !validSemanticName(string(state.Operation)) || state.OperationID == (OperationID{}) || state.TargetPresent != (state.Target != (AttemptTargetCommitment{})) || state.ScopePresent != (state.Scope != (EvidenceScopeCommitment{})) || state.Owner == (AttemptOwnerCommitment{}) || state.State < AttemptOpenState || state.State > AttemptAbandonedState || state.Sequence == 0 || state.Sequence > MaxAttemptTransitions || state.CheckpointCount > state.Sequence-1 || state.TransitionBytes == 0 || state.TransitionBytes > MaxAttemptStateBytes || !state.Start.valid() || !state.Head.valid() || state.Leaf == (LeafDigest{}) || state.ExpiresAt.IsZero() || !state.ExpiresAt.Equal(canonicalTime(state.ExpiresAt)) {
		return false
	}
	return state.Start.Revision.Catalog.ID == state.Head.Revision.Catalog.ID
}

type attemptIdentityAliasBinding struct {
	operation         OperationName
	policy            AttemptPolicyFingerprint
	replay            AttemptReplayFingerprint
	operationID       OperationID
	chain             AttemptChainID
	targetPresent     bool
	targetCommitments IdentityCommitmentSet
	scopePresent      bool
	scopeCommitments  IdentityCommitmentSet
	ownerCommitments  IdentityCommitmentSet
}

func newAttemptIdentityAliasBinding(operation OperationName, policy AttemptPolicyFingerprint, replay AttemptReplayFingerprint, operationID OperationID, chain AttemptChainID, targetPresent bool, target IdentityCommitmentSet, scopePresent bool, scope IdentityCommitmentSet, owner IdentityCommitmentSet) (AttemptIdentityAliasBinding, error) {
	if !validSemanticName(string(operation)) || policy == (AttemptPolicyFingerprint{}) || replay == (AttemptReplayFingerprint{}) || operationID == (OperationID{}) || chain == (AttemptChainID{}) || targetPresent != validAttemptAliasSet(target, CommitAttemptTarget) || scopePresent != validAttemptAliasSet(scope, CommitEvidenceScope) || !validAttemptAliasSet(owner, CommitAttemptOwner) {
		return AttemptIdentityAliasBinding{}, auditErrorAt(ErrInvalid, "attempt.aliases")
	}
	return AttemptIdentityAliasBinding{value: attemptIdentityAliasBinding{
		operation: operation, policy: policy, replay: replay, operationID: operationID, chain: chain,
		targetPresent: targetPresent, targetCommitments: target,
		scopePresent: scopePresent, scopeCommitments: scope, ownerCommitments: owner,
	}}, nil
}

func validAttemptAliasSet(set IdentityCommitmentSet, domain IdentityCommitmentDomain) bool {
	return set.Domain() == domain && len(set.Aliases()) > 0
}

func (b AttemptIdentityAliasBinding) Operation() OperationName         { return b.value.operation }
func (b AttemptIdentityAliasBinding) Policy() AttemptPolicyFingerprint { return b.value.policy }
func (b AttemptIdentityAliasBinding) Replay() AttemptReplayFingerprint { return b.value.replay }
func (b AttemptIdentityAliasBinding) OperationID() OperationID         { return b.value.operationID }
func (b AttemptIdentityAliasBinding) Chain() AttemptChainID            { return b.value.chain }
func (b AttemptIdentityAliasBinding) TargetPresent() bool              { return b.value.targetPresent }
func (b AttemptIdentityAliasBinding) TargetCommitments() IdentityCommitmentSet {
	return b.value.targetCommitments
}
func (b AttemptIdentityAliasBinding) ScopePresent() bool { return b.value.scopePresent }
func (b AttemptIdentityAliasBinding) ScopeCommitments() IdentityCommitmentSet {
	return b.value.scopeCommitments
}
func (b AttemptIdentityAliasBinding) OwnerCommitments() IdentityCommitmentSet {
	return b.value.ownerCommitments
}
