package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"time"

	"github.com/frostgrove/vv/crud"
)

type IdempotencyDomainKind uint8

const (
	RecordIdempotencyDomain IdempotencyDomainKind = iota + 1
	AttemptStartIdempotencyDomain
	AttemptCheckpointIdempotencyDomain
	AttemptFinishIdempotencyDomain
)

type IdempotencyDomain struct {
	Kind      IdempotencyDomainKind
	Catalog   CatalogID
	Operation OperationName
	Attempt   AttemptChainID
	Token     IdempotencyToken
}

type reconcileKey struct {
	bytes     []byte
	backing   BackingID
	log       LogID
	catalog   CatalogID
	operation OperationName
	revision  RevisionID
}

type ReconcileKey struct {
	value reconcileKey
}

func ParseReconcileKey(input []byte) (ReconcileKey, error) {
	if len(input) < 1+16+16+2+2+16+sha256.Size || len(input) > MaxReconcileKeyBytes || input[0] != 1 {
		return ReconcileKey{}, auditErrorAt(ErrInvalid, "reconcile_key")
	}
	body, checksum := input[:len(input)-sha256.Size], input[len(input)-sha256.Size:]
	digest := sha256.Sum256(body)
	if !bytes.Equal(digest[:], checksum) {
		return ReconcileKey{}, auditErrorAt(ErrInvalid, "reconcile_key")
	}
	offset := 1
	var backing BackingID
	copy(backing[:], body[offset:offset+16])
	offset += 16
	var log LogID
	copy(log[:], body[offset:offset+16])
	offset += 16
	catalogBytes, next, ok := readReconcileFrame(body, offset)
	if !ok {
		return ReconcileKey{}, auditErrorAt(ErrInvalid, "reconcile_key")
	}
	offset = next
	operationBytes, next, ok := readReconcileFrame(body, offset)
	if !ok || next+16 != len(body) {
		return ReconcileKey{}, auditErrorAt(ErrInvalid, "reconcile_key")
	}
	var revision RevisionID
	copy(revision[:], body[next:])
	key := ReconcileKey{value: reconcileKey{
		bytes: bytes.Clone(input), backing: backing, log: log,
		catalog: CatalogID(catalogBytes), operation: OperationName(operationBytes), revision: revision,
	}}
	if !key.valid() {
		return ReconcileKey{}, auditErrorAt(ErrInvalid, "reconcile_key")
	}
	return key, nil
}

func (k ReconcileKey) Bytes() []byte {
	return bytes.Clone(k.value.bytes)
}

func (ReconcileKey) String() string { return "[audit reconcile key]" }

func newReconcileKey(backing BackingID, header RevisionHeaderView) (ReconcileKey, error) {
	if backing == (BackingID{}) || header.Log == (LogID{}) || !header.Catalog.valid() || !validSemanticName(string(header.Operation)) || header.RevisionID == (RevisionID{}) {
		return ReconcileKey{}, auditErrorAt(ErrInvalid, "reconcile_key")
	}
	buffer := bytes.NewBuffer(make([]byte, 0, 128))
	buffer.WriteByte(1)
	buffer.Write(backing[:])
	buffer.Write(header.Log[:])
	writeReconcileFrame(buffer, []byte(header.Catalog.ID))
	writeReconcileFrame(buffer, []byte(header.Operation))
	buffer.Write(header.RevisionID[:])
	digest := sha256.Sum256(buffer.Bytes())
	buffer.Write(digest[:])
	return ParseReconcileKey(buffer.Bytes())
}

func (k ReconcileKey) valid() bool {
	return len(k.value.bytes) > 0 && k.value.backing != (BackingID{}) && k.value.log != (LogID{}) && validSemanticName(string(k.value.catalog)) && validSemanticName(string(k.value.operation)) && k.value.revision != (RevisionID{})
}

func writeReconcileFrame(buffer *bytes.Buffer, value []byte) {
	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(len(value)))
	buffer.Write(size[:])
	buffer.Write(value)
}

func readReconcileFrame(input []byte, offset int) ([]byte, int, bool) {
	if offset+2 > len(input) {
		return nil, offset, false
	}
	size := int(binary.BigEndian.Uint16(input[offset : offset+2]))
	offset += 2
	if size == 0 || offset+size > len(input) {
		return nil, offset, false
	}
	return input[offset : offset+size], offset + size, true
}

type EntityAliasBinding struct{ value struct{} }
type AttemptIdentityAliasBinding struct{ value struct{} }
type HoldIDAliasBinding struct{ value struct{} }
type HoldMatterAliasBinding struct{ value struct{} }

type HoldAppendCandidateView struct {
	Disposition HoldTransitionDisposition
	Result      HoldProjectionStateView
	Revision    Revision
}

type HoldConditionalAppendView struct {
	Kind             HoldCommandKind
	Hold             HoldID
	HoldCommitment   HoldIDCommitment
	Target           RevisionRef
	MatterPresent    bool
	MatterCommitment HoldMatterCommitment
	Expected         HoldProjectionStateView
	Authorization    HoldRequestDigest
	Candidate        HoldAppendCandidateView
}

type AttemptAppendCandidateView struct {
	Result     AttemptProjectionStateView
	TypeResult AttemptTypeProjectionStateView
	Revision   Revision
}

type AttemptConditionalAppendView struct {
	Chain               AttemptChainID
	Expected            AttemptProjectionStateView
	TypeExpected        AttemptTypeProjectionStateView
	ResumeAuthorization AccessResultDigest
	Candidate           AttemptAppendCandidateView
}

type AppendRequestView struct {
	Revision    Revision
	Hold        HoldConditionalAppendView
	Attempt     AttemptConditionalAppendView
	Intent      AppendIntentDigest
	Idempotency IdentityCommitmentSet
	Entities    []EntityAliasBinding
	Attempts    []AttemptIdentityAliasBinding
	HoldIDs     []HoldIDAliasBinding
	HoldMatters []HoldMatterAliasBinding
}

type appendRequestOrigin struct{}

type appendRequest struct {
	view   AppendRequestView
	origin *appendRequestOrigin
}

type AppendRequest struct {
	value appendRequest
}

func newAppendRequest(revision Revision, intent AppendIntentDigest, idempotency IdentityCommitmentSet) (AppendRequest, error) {
	if !revision.valid() || intent == (AppendIntentDigest{}) {
		return AppendRequest{}, auditErrorAt(ErrInvalid, "append_request")
	}
	return AppendRequest{value: appendRequest{
		view:   AppendRequestView{Revision: revision, Intent: intent, Idempotency: idempotency},
		origin: &appendRequestOrigin{},
	}}, nil
}

func (r AppendRequest) View() AppendRequestView {
	view := r.value.view
	view.Revision = Revision{value: revision{view: cloneRevisionView(view.Revision.View())}}
	view.Entities = slices.Clone(view.Entities)
	view.Attempts = slices.Clone(view.Attempts)
	view.HoldIDs = slices.Clone(view.HoldIDs)
	view.HoldMatters = slices.Clone(view.HoldMatters)
	return view
}

func (r AppendRequest) valid() bool {
	return r.value.origin != nil && r.value.view.Revision.valid() && r.value.view.Intent != (AppendIntentDigest{})
}

type storedHeader struct {
	data StoredHeaderData
}

type StoredHeader struct {
	value storedHeader
}

type StoredHeaderData struct {
	Header                   RevisionHeaderView
	Intent                   AppendIntentDigest
	RecordedAt               time.Time
	Position                 StorePosition
	AttemptTransitionPresent bool
	AttemptTransition        AttemptTransitionWireView
	AttemptProjection        AttemptProjectionStateView
	HoldTransitionPresent    bool
	HoldDisposition          HoldTransitionDisposition
	HoldProjection           HoldProjectionStateView
}

func NewStoredHeader(data StoredHeaderData) (StoredHeader, error) {
	if data.Intent == (AppendIntentDigest{}) || data.RecordedAt.IsZero() || len(data.Position.Bytes()) == 0 {
		return StoredHeader{}, auditErrorAt(ErrMalformedEvidence, "stored_header")
	}
	if len(data.Header.Authorization.Resources) == 0 || len(data.Header.Authorization.Actions) == 0 {
		return StoredHeader{}, auditErrorAt(ErrMalformedEvidence, "authorization")
	}
	probe := RevisionWireView{Header: data.Header, Items: []ItemWireView{{Ordinal: 0, Kind: EventItem, Resource: data.Header.Authorization.Resources[0], Action: data.Header.Authorization.Actions[0]}}}
	if err := validateRevisionView(probe); err != nil {
		return StoredHeader{}, err
	}
	if data.AttemptTransitionPresent && data.HoldTransitionPresent {
		return StoredHeader{}, auditErrorAt(ErrMalformedEvidence, "stored_header")
	}
	data.Header.Authorization.Resources = slices.Clone(data.Header.Authorization.Resources)
	data.Header.Authorization.Actions = slices.Clone(data.Header.Authorization.Actions)
	data.Header.Authorization.Classifications = slices.Clone(data.Header.Authorization.Classifications)
	data.Header.Authorization.Coordinates = cloneCoordinates(data.Header.Authorization.Coordinates)
	return StoredHeader{value: storedHeader{data: data}}, nil
}

func (h StoredHeader) Revision() RevisionHeaderView {
	view := h.value.data.Header
	view.Authorization.Resources = slices.Clone(view.Authorization.Resources)
	view.Authorization.Actions = slices.Clone(view.Authorization.Actions)
	view.Authorization.Classifications = slices.Clone(view.Authorization.Classifications)
	view.Authorization.Coordinates = cloneCoordinates(view.Authorization.Coordinates)
	return view
}

func (h StoredHeader) Intent() AppendIntentDigest { return h.value.data.Intent }
func (h StoredHeader) RecordedAt() time.Time      { return h.value.data.RecordedAt }
func (h StoredHeader) Position() StorePosition    { return cloneStorePosition(h.value.data.Position) }

func (h StoredHeader) AttemptTransition() (AttemptTransitionWireView, bool) {
	return h.value.data.AttemptTransition, h.value.data.AttemptTransitionPresent
}

func (h StoredHeader) AttemptProjection() (AttemptProjectionStateView, bool) {
	return h.value.data.AttemptProjection, h.value.data.AttemptTransitionPresent
}

func (h StoredHeader) HoldDisposition() (HoldTransitionDisposition, bool) {
	return h.value.data.HoldDisposition, h.value.data.HoldTransitionPresent
}

func (h StoredHeader) HoldProjection() (HoldProjectionStateView, bool) {
	return h.value.data.HoldProjection, h.value.data.HoldTransitionPresent
}

func (h StoredHeader) valid() bool {
	return h.value.data.Intent != (AppendIntentDigest{}) && h.value.data.RecordedAt != (time.Time{}) && len(h.value.data.Position.Bytes()) > 0
}

type appendResult struct {
	stored      StoredHeader
	disposition AppendDisposition
	authority   Authority
}

type AppendResult struct {
	value appendResult
}

func NewAppendResult(request AppendRequest, stored StoredHeader, disposition AppendDisposition, authority Authority) (AppendResult, error) {
	if !request.valid() || !stored.valid() || !authority.Valid() || disposition != Inserted && disposition != Replayed {
		return AppendResult{}, auditErrorAt(ErrMalformedEvidence, "append_result")
	}
	candidate := request.value.view.Revision.View().Header
	actual := stored.Revision()
	if disposition == Inserted && (candidate.RevisionID != actual.RevisionID || candidate.Catalog != actual.Catalog || candidate.Semantic != actual.Semantic || request.value.view.Intent != stored.Intent()) {
		return AppendResult{}, auditErrorAt(ErrMalformedEvidence, "append_result")
	}
	return AppendResult{value: appendResult{stored: stored, disposition: disposition, authority: authority}}, nil
}

func NewHoldAppendResult(request AppendRequest, stored StoredHeader, disposition AppendDisposition, authority Authority) (AppendResult, error) {
	return NewAppendResult(request, stored, disposition, authority)
}

func NewAttemptAppendResult(request AppendRequest, stored StoredHeader, disposition AppendDisposition, authority Authority) (AppendResult, error) {
	return NewAppendResult(request, stored, disposition, authority)
}

func (r AppendResult) Stored() StoredHeader           { return r.value.stored }
func (r AppendResult) Disposition() AppendDisposition { return r.value.disposition }
func (r AppendResult) Authority() Authority           { return r.value.authority }
func (r AppendResult) HoldDisposition() (HoldTransitionDisposition, bool) {
	return r.value.stored.HoldDisposition()
}
func (r AppendResult) HoldProjection() (HoldProjectionStateView, bool) {
	return r.value.stored.HoldProjection()
}
func (r AppendResult) AttemptTransition() (AttemptTransitionWireView, bool) {
	return r.value.stored.AttemptTransition()
}
func (r AppendResult) AttemptProjection() (AttemptProjectionStateView, bool) {
	return r.value.stored.AttemptProjection()
}

type LookupRequestView struct {
	Key         ReconcileKey
	Revision    RevisionID
	Idempotency IdentityCommitmentSet
	Catalog     CatalogID
	Operation   OperationName
}

type lookupRequest struct {
	view   LookupRequestView
	origin *struct{}
}

type LookupRequest struct{ value lookupRequest }

func newLookupRequest(key ReconcileKey) LookupRequest {
	return LookupRequest{value: lookupRequest{view: LookupRequestView{
		Key: key, Revision: key.value.revision, Catalog: key.value.catalog, Operation: key.value.operation,
	}, origin: &struct{}{}}}
}

func (r LookupRequest) View() LookupRequestView {
	view := r.value.view
	view.Key.value.bytes = bytes.Clone(view.Key.value.bytes)
	return view
}

type LookupResultData struct {
	State      LookupState
	Stored     StoredHeader
	Visibility Settlement
	Authority  Authority
}

type lookupResult struct {
	data LookupResultData
}

type LookupResult struct{ value lookupResult }

func NewLookupResult(request LookupRequest, data LookupResultData) (LookupResult, error) {
	if request.value.origin == nil || data.State != Found && data.State != AbsentNow || !data.Authority.Valid() {
		return LookupResult{}, auditErrorAt(ErrMalformedEvidence, "lookup_result")
	}
	if data.State == Found {
		if !data.Stored.valid() || data.Stored.Revision().RevisionID != request.value.view.Revision {
			return LookupResult{}, auditErrorAt(ErrMalformedEvidence, "lookup_result")
		}
	} else if data.Stored.valid() {
		return LookupResult{}, auditErrorAt(ErrMalformedEvidence, "lookup_result")
	}
	return LookupResult{value: lookupResult{data: data}}, nil
}

func (r LookupResult) State() LookupState { return r.value.data.State }

func (r LookupResult) Receipt() (Receipt, bool) {
	if r.value.data.State != Found {
		return Receipt{}, false
	}
	return receiptFromStored(r.value.data.Stored, r.value.data.Visibility), true
}

func (r LookupResult) HoldDisposition() (HoldTransitionDisposition, bool) {
	return r.value.data.Stored.HoldDisposition()
}

func (r LookupResult) HoldProjection() (HoldProjectionStateView, bool) {
	return r.value.data.Stored.HoldProjection()
}

func (r LookupResult) AttemptTransition() (AttemptTransitionWireView, bool) {
	return r.value.data.Stored.AttemptTransition()
}

func (r LookupResult) AttemptProjection() (AttemptProjectionStateView, bool) {
	return r.value.data.Stored.AttemptProjection()
}

type IdempotencyLookupRequestView struct {
	Catalog     CatalogRef
	Catalogs    []CatalogRef
	CatalogSet  CatalogSetDigest
	Deployment  DeploymentFingerprint
	Domain      IdempotencyDomain
	Idempotency IdentityCommitmentSet
	Semantic    SemanticDigest
}

type IdempotencyLookupRequest struct{ value IdempotencyLookupRequestView }
type IdempotencyLookupResult struct{ value IdempotencyLookupResultData }

type IdempotencyLookupResultData struct {
	State  LookupState
	Stored StoredHeader
}

func (r IdempotencyLookupRequest) View() IdempotencyLookupRequestView {
	view := r.value
	view.Catalogs = slices.Clone(view.Catalogs)
	return view
}

func NewIdempotencyLookupResult(request IdempotencyLookupRequest, data IdempotencyLookupResultData) (IdempotencyLookupResult, error) {
	if !request.value.Catalog.valid() || data.State != Found && data.State != AbsentNow || data.State == Found && !data.Stored.valid() {
		return IdempotencyLookupResult{}, auditErrorAt(ErrMalformedEvidence, "idempotency_lookup")
	}
	return IdempotencyLookupResult{value: data}, nil
}

func (r IdempotencyLookupResult) State() LookupState { return r.value.State }
func (r IdempotencyLookupResult) Stored() (StoredHeader, bool) {
	return r.value.Stored, r.value.State == Found
}

type Execution interface {
	Authority() Authority
	EntityHead(context.Context, EntityHeadRequest) (EntityHeadResult, error)
	Append(context.Context, AppendRequest) (AppendResult, error)
}

type Writer interface {
	StoreInfo
	TransactionSource() any
	BindTransaction(crud.Executor) (Execution, error)
	LookupIdempotency(context.Context, IdempotencyLookupRequest) (IdempotencyLookupResult, error)
	Append(context.Context, AppendRequest) (AppendResult, error)
	Lookup(context.Context, LookupRequest) (LookupResult, error)
}

func cloneStorePosition(position StorePosition) StorePosition {
	copy, _ := NewStorePosition(position.Bytes())
	return copy
}
