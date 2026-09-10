package audit

import (
	"context"
	"slices"
	"time"
)

type AccessAuthority interface {
	AuthorizeAudit(context.Context, AccessRequest) (AccessDecision, error)
}

type AccessAuthorityFunc func(context.Context, AccessRequest) (AccessDecision, error)

func (f AccessAuthorityFunc) AuthorizeAudit(ctx context.Context, request AccessRequest) (AccessDecision, error) {
	if f == nil {
		return AccessDecision{}, auditErrorAt(ErrDenied, "access")
	}
	return f(ctx, request)
}

type AccessIntent uint8

const (
	HistoryDisclosureAccess AccessIntent = iota + 1
	AttemptResumeAccess
	AttemptResolveAccess
	AttemptAbandonAccess
)

type AccessTargetKind uint8

const (
	AccessSubjectTarget AccessTargetKind = iota + 1
	AccessResourceTarget
	AccessEventTarget
	AccessEventTypeTarget
	AccessOperationTypeTarget
	AccessOperationTarget
	AccessAttemptTarget
	AccessAttemptTypeTarget
	AccessAttemptDeclaredTarget
	AccessActorTarget
	AccessContextTarget
	AccessEventIndexTarget
	AccessEntityIndexTarget
	AccessRevisionTarget
	AccessItemTarget
)

type AccessTargetView struct {
	Kind            AccessTargetKind
	Resource        Resource
	Action          Action
	Actions         []Action
	Subject         Reference
	SubjectMode     StorageMode
	SubjectClass    Classification
	Target          Reference
	TargetMode      StorageMode
	TargetClass     Classification
	OperationName   OperationName
	Operation       OperationID
	Revision        RevisionRef
	Item            ItemRef
	Resources       []Resource
	Classifications []Classification
	Fields          []FieldName
	Context         []ContextFactKind
}

type AccessRequestView struct {
	Intent    AccessIntent
	Requester ContextView
	Target    AccessTargetView
	Query     NormalizedQueryView
	Catalogs  []CatalogRef
}

type accessRequestOrigin struct{ marker byte }

type accessRequest struct {
	view   AccessRequestView
	origin *accessRequestOrigin
}

type AccessRequest struct {
	value accessRequest
}

func (r AccessRequest) View() AccessRequestView { return cloneAccessRequestView(r.value.view) }

type AccessVerdict uint8

const (
	AccessAllowed AccessVerdict = iota + 1
	AccessDenied
)

type AccessGrantSpec struct {
	Roles            []Reference
	Scopes           []ScopedReference
	Catalogs         []CatalogRef
	Resources        []Resource
	Actions          []Action
	Fields           []FieldName
	Context          []ContextFactKind
	Classifications  []Classification
	Time             TimeWindowView
	Direction        HistoryDirection
	Changed          ChangedFieldFilterView
	Outcomes         []Outcome
	Reasons          []Reason
	ExpiresAt        time.Time
	MaxRevisions     uint32
	MaxPages         uint32
	MaxBytes         uint64
	Reconstruction   struct{}
	ComparisonBefore struct{}
	ComparisonAfter  struct{}
}

type AccessDecisionView struct {
	Verdict AccessVerdict
	Reason  Reason
	Grant   AccessGrantSpec
}

type accessDecision struct {
	view   AccessDecisionView
	origin *accessRequestOrigin
}

type AccessDecision struct {
	value accessDecision
}

func AllowAccess(request AccessRequest, grant AccessGrantSpec) (AccessDecision, error) {
	if request.value.origin == nil {
		return AccessDecision{}, auditErrorAt(ErrInvalid, "access.request")
	}
	grant = cloneAccessGrant(grant)
	if err := validateBasicGrant(request.value.view, grant); err != nil {
		return AccessDecision{}, err
	}
	return AccessDecision{value: accessDecision{
		view: AccessDecisionView{Verdict: AccessAllowed, Grant: grant}, origin: request.value.origin,
	}}, nil
}

func DenyAccess(request AccessRequest, reason Reason) (AccessDecision, error) {
	if request.value.origin == nil || !validSemanticName(string(reason)) {
		return AccessDecision{}, auditErrorAt(ErrInvalid, "access.denial")
	}
	return AccessDecision{value: accessDecision{
		view: AccessDecisionView{Verdict: AccessDenied, Reason: reason}, origin: request.value.origin,
	}}, nil
}

func (d AccessDecision) View() AccessDecisionView {
	view := d.value.view
	view.Grant = cloneAccessGrant(view.Grant)
	return view
}

func newAccessRequest(view AccessRequestView) AccessRequest {
	return AccessRequest{value: accessRequest{view: cloneAccessRequestView(view), origin: &accessRequestOrigin{}}}
}

func cloneAccessRequestView(view AccessRequestView) AccessRequestView {
	view.Requester.Actors = slices.Clone(view.Requester.Actors)
	view.Target.Actions = slices.Clone(view.Target.Actions)
	view.Target.Resources = slices.Clone(view.Target.Resources)
	view.Target.Classifications = slices.Clone(view.Target.Classifications)
	view.Target.Fields = slices.Clone(view.Target.Fields)
	view.Target.Context = slices.Clone(view.Target.Context)
	view.Query = cloneNormalizedQuery(view.Query)
	view.Catalogs = slices.Clone(view.Catalogs)
	return view
}

func cloneAccessGrant(grant AccessGrantSpec) AccessGrantSpec {
	grant.Roles = slices.Clone(grant.Roles)
	grant.Scopes = slices.Clone(grant.Scopes)
	grant.Catalogs = slices.Clone(grant.Catalogs)
	grant.Resources = slices.Clone(grant.Resources)
	grant.Actions = slices.Clone(grant.Actions)
	grant.Fields = slices.Clone(grant.Fields)
	grant.Context = slices.Clone(grant.Context)
	grant.Classifications = slices.Clone(grant.Classifications)
	grant.Changed.Fields = slices.Clone(grant.Changed.Fields)
	grant.Changed.Excluded = slices.Clone(grant.Changed.Excluded)
	grant.Outcomes = slices.Clone(grant.Outcomes)
	grant.Reasons = slices.Clone(grant.Reasons)
	return grant
}

func validateBasicGrant(request AccessRequestView, grant AccessGrantSpec) error {
	if len(grant.Roles) != 1 || grant.Roles[0] != request.Query.Role {
		return auditErrorAt(ErrDenied, "access.roles")
	}
	if !scopesMatchRequest(grant.Scopes, request.Query.Scope) {
		return auditErrorAt(ErrDenied, "access.scopes")
	}
	if !nonemptySubset(grant.Catalogs, request.Catalogs) || !nonemptySubset(grant.Resources, request.Target.Resources) || !nonemptySubset(grant.Actions, request.Query.Actions) || !nonemptySubset(grant.Classifications, request.Target.Classifications) {
		return auditErrorAt(ErrDenied, "access.grant")
	}
	if !subset(grant.Fields, request.Query.Fields.Fields) || !subset(grant.Context, request.Query.Context.Facts) {
		return auditErrorAt(ErrDenied, "access.projection")
	}
	if grant.Direction != request.Query.Direction || grant.MaxRevisions == 0 || grant.MaxRevisions > request.Query.Limit || grant.MaxPages != 1 || grant.MaxBytes == 0 || grant.ExpiresAt.IsZero() {
		return auditErrorAt(ErrDenied, "access.bounds")
	}
	if grant.Time != (TimeWindowView{}) || grant.Changed.Match != 0 || len(grant.Changed.Fields) != 0 || len(grant.Changed.Excluded) != 0 || len(grant.Outcomes) != 0 || len(grant.Reasons) != 0 {
		return auditErrorAt(ErrUnsupported, "access.grant")
	}
	return nil
}

func scopesMatchRequest(granted []ScopedReference, requested ScopeSelectorView) bool {
	if requested.Kind == 0 {
		return len(granted) == 0
	}
	if requested.Kind != ScopeExact || len(granted) != 1 {
		return false
	}
	return granted[0] == requested.Reference
}

func nonemptySubset[T comparable](values, ceiling []T) bool {
	return len(values) > 0 && subset(values, ceiling)
}

func subset[T comparable](values, ceiling []T) bool {
	allowed := make(map[T]struct{}, len(ceiling))
	for _, value := range ceiling {
		allowed[value] = struct{}{}
	}
	seen := make(map[T]struct{}, len(values))
	for _, value := range values {
		if _, ok := allowed[value]; !ok {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
