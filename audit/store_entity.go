package audit

import "context"

type EntityHeadRequestView struct {
	Resource     Resource
	Candidate    EntityChainID
	ScopePresent bool
	Scope        EvidenceScopeCommitment
	Commitments  IdentityCommitmentSet
}

type entityHeadRequest struct {
	view   EntityHeadRequestView
	origin *struct{}
}

type EntityHeadRequest struct{ value entityHeadRequest }

func newEntityHeadRequest(view EntityHeadRequestView) (EntityHeadRequest, error) {
	if !validSemanticName(string(view.Resource)) || view.Candidate == (EntityChainID{}) || view.Commitments.Domain() != CommitEntitySubject || len(view.Commitments.Aliases()) == 0 {
		return EntityHeadRequest{}, auditErrorAt(ErrInvalid, "entity_head")
	}
	if view.ScopePresent != (view.Scope != (EvidenceScopeCommitment{})) {
		return EntityHeadRequest{}, auditErrorAt(ErrInvalid, "entity_scope")
	}
	return EntityHeadRequest{value: entityHeadRequest{view: view, origin: &struct{}{}}}, nil
}

func (r EntityHeadRequest) View() EntityHeadRequestView { return r.value.view }

type EntityHeadState uint8

const (
	EntityGenesis EntityHeadState = iota + 1
	EntityExisting
	EntityTerminal
)

type EntityHeadResultData struct {
	State     EntityHeadState
	Chain     EntityChainID
	Previous  LeafDigest
	Authority Authority
}

type EntityHeadResult struct{ value EntityHeadResultData }

func NewEntityHeadResult(request EntityHeadRequest, data EntityHeadResultData) (EntityHeadResult, error) {
	if request.value.origin == nil || !data.Authority.Valid() || data.State < EntityGenesis || data.State > EntityTerminal || data.Chain == (EntityChainID{}) {
		return EntityHeadResult{}, auditErrorAt(ErrMalformedEvidence, "entity_head")
	}
	if data.State == EntityGenesis && (data.Chain != request.value.view.Candidate || data.Previous != (LeafDigest{})) {
		return EntityHeadResult{}, auditErrorAt(ErrMalformedEvidence, "entity_head")
	}
	if data.State != EntityGenesis && data.Previous == (LeafDigest{}) {
		return EntityHeadResult{}, auditErrorAt(ErrMalformedEvidence, "entity_head")
	}
	return EntityHeadResult{value: data}, nil
}

func (r EntityHeadResult) State() EntityHeadState { return r.value.State }
func (r EntityHeadResult) ChainID() EntityChainID { return r.value.Chain }
func (r EntityHeadResult) Previous() LeafDigest   { return r.value.Previous }
func (r EntityHeadResult) Authority() Authority   { return r.value.Authority }

func entityChainCandidate(resource Resource, scope EvidenceScopeCommitment, scopePresent bool, commitment IdentityCommitment) EntityChainID {
	presence := []byte{0}
	if scopePresent {
		presence[0] = 1
	}
	hash := auditSHA256("frostgrove.audit/entity-chain/v1", []byte(resource), presence, scope[:], commitment.Bytes())
	return EntityChainID(hash)
}

func entityHead(ctx context.Context, execution Execution, request EntityHeadRequest) (EntityHeadResult, error) {
	if err := ctx.Err(); err != nil {
		return EntityHeadResult{}, err
	}
	result, err := execution.EntityHead(ctx, request)
	if err != nil {
		return EntityHeadResult{}, err
	}
	if !SameAuthority(result.Authority(), execution.Authority()) {
		return EntityHeadResult{}, auditErrorAt(ErrWrongAuthority, "entity_head")
	}
	return result, nil
}
