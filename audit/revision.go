package audit

import (
	"bytes"
	"slices"
	"time"
)

const revisionFormatV1 uint16 = 1

type AttemptProjectionStateView struct {
	Present         bool
	Chain           AttemptChainID
	Policy          AttemptPolicyFingerprint
	Replay          AttemptReplayFingerprint
	Operation       OperationName
	OperationID     OperationID
	TargetPresent   bool
	Target          AttemptTargetCommitment
	ScopePresent    bool
	Scope           EvidenceScopeCommitment
	Owner           AttemptOwnerCommitment
	State           AttemptState
	Sequence        uint16
	CheckpointCount uint16
	TransitionBytes uint64
	Start           ItemRef
	Head            ItemRef
	Leaf            LeafDigest
	ExpiresAt       time.Time
}

type AttemptProjectionNextView struct {
	Present         bool
	Chain           AttemptChainID
	Policy          AttemptPolicyFingerprint
	Replay          AttemptReplayFingerprint
	Operation       OperationName
	OperationID     OperationID
	TargetPresent   bool
	Target          AttemptTargetCommitment
	ScopePresent    bool
	Scope           EvidenceScopeCommitment
	Owner           AttemptOwnerCommitment
	State           AttemptState
	Sequence        uint16
	CheckpointCount uint16
	TransitionBytes uint64
	Start           ItemRef
	Head            ItemRef
	ExpiresAt       time.Time
}

type AttemptTypeProjectionStateView struct {
	Catalog   CatalogID
	Operation OperationName
	Policy    AttemptPolicyFingerprint
	Replay    AttemptReplayFingerprint
	Anchor    CatalogActivationGateDigest
	Unsettled uint64
	Head      ItemRef
	Leaf      LeafDigest
}

type AttemptTypeProjectionNextView struct {
	Catalog   CatalogID
	Operation OperationName
	Policy    AttemptPolicyFingerprint
	Replay    AttemptReplayFingerprint
	Anchor    CatalogActivationGateDigest
	Unsettled uint64
	Head      ItemRef
}

type HoldCommandKind uint8

const (
	HoldPlaceCommand HoldCommandKind = iota + 1
	HoldReleaseCommand
)

type HoldMembershipState uint8

const (
	HoldMembershipAbsent HoldMembershipState = iota + 1
	HoldMembershipActive
	HoldMembershipReleased
)

type HoldTransitionDisposition uint8

const (
	HoldActivated HoldTransitionDisposition = iota + 1
	HoldReleasedNow
	HoldAlreadyActive
	HoldAlreadyReleased
)

type HoldProjectionStateView struct {
	Revision   RevisionRef
	Membership HoldMembershipState
	Count      uint32
	Epoch      uint64
	ActiveSet  HoldSetDigest
	Head       RevisionRef
}

type AttemptTransitionWireView struct {
	Chain               AttemptChainID
	Policy              AttemptPolicyFingerprint
	Replay              AttemptReplayFingerprint
	Operation           OperationName
	OperationID         OperationID
	ScopePresent        bool
	Scope               EvidenceScopeCommitment
	Start               ItemRef
	Sequence            uint16
	CheckpointCount     uint16
	Kind                AttemptTransitionKind
	Checkpoint          AttemptCheckpointCode
	ExpiresAt           time.Time
	Expected            AttemptProjectionStateView
	Result              AttemptProjectionNextView
	TypeExpected        AttemptTypeProjectionStateView
	TypeResult          AttemptTypeProjectionNextView
	ResumeAuthorization AccessResultDigest
}

type HoldTransitionWireView struct {
	Command          HoldCommandKind
	Hold             HoldID
	Identity         HoldIdentity
	HoldCommitment   HoldIDCommitment
	Target           RevisionRef
	MatterPresent    bool
	Matter           StoredValueView
	MatterCommitment HoldMatterCommitment
	Expected         HoldProjectionStateView
	Result           HoldProjectionStateView
	Disposition      HoldTransitionDisposition
	Authorization    HoldRequestDigest
}

type QueryCoordinateKind uint8

const (
	QueryScope QueryCoordinateKind = iota + 1
	QuerySubject
	QueryActor
	QueryClient
	QueryTarget
	QueryEventIndex
	QueryEntityIndex
	QueryAttemptTarget
	QueryService
	QueryDeployment
	QueryCorrelation
	QueryCausation
	QueryTrace
	QuerySource
)

type QueryCoordinateMatch uint8

const (
	QueryCoordinateExact QueryCoordinateMatch = iota + 1
	QueryCoordinateAllDeclared
)

type ActorPosition uint8
type EntityIndexSide uint8

type QueryCoordinateAlternativeView struct {
	Plaintext   []byte
	Tokens      []Token
	Commitments IdentityCommitmentSet
}

type QueryCoordinateView struct {
	Kind           QueryCoordinateKind
	Match          QueryCoordinateMatch
	Resource       Resource
	Action         Action
	Field          FieldName
	ActorPosition  ActorPosition
	ActorKind      ActorKind
	ContextKind    ContextFactKind
	EntitySide     EntityIndexSide
	Classification Classification
	Mode           StorageMode
	Alternatives   []QueryCoordinateAlternativeView
}

type RevisionAuthorizationSummaryView struct {
	Resources       []Resource
	Actions         []Action
	Classifications []Classification
	Coordinates     []QueryCoordinateView
}

type StoredValueView struct {
	Field          FieldName
	Codec          CodecDescription
	Classification Classification
	Mode           StorageMode
	State          ValueState
	Plaintext      []byte
	Redacted       bool
	Token          Token
	Protected      ProtectedValue
}

type StoredChangeView struct {
	Field  FieldName
	Before StoredValueView
	After  StoredValueView
}

type StoredContextFactView struct {
	Kind           ContextFactKind
	Provenance     Provenance
	Classification Classification
	Mode           StorageMode
	Plaintext      []byte
	Redacted       bool
	Token          Token
	Protected      ProtectedValue
}

type StoredActorView struct {
	Ordinal        uint8
	Kind           ActorKind
	Provenance     Provenance
	Classification Classification
	Mode           StorageMode
	Reference      StoredValueView
}

type ItemWireView struct {
	Ordinal        uint16
	Kind           ItemKind
	Resource       Resource
	Action         Action
	EntityState    EntityStateKind
	Chain          EntityChainID
	Subject        StoredValueView
	Target         StoredValueView
	OccurredAt     time.Time
	Outcome        Outcome
	Reason         Reason
	AccessRequest  AccessRequestDigest
	AccessGrant    AccessGrantDigest
	AccessResult   AccessResultDigest
	ControlRequest ControlRequestDigest
	ControlGrant   ControlGrantDigest
	ControlResult  ControlResultDigest
	DenialRequest  DenialRequestDigest
	Previous       LeafDigest
	Leaf           LeafDigest
	Values         []StoredValueView
	Changes        []StoredChangeView
	CorrectionOf   ItemRef
	DisputeOf      ItemRef
	Attempt        AttemptTransitionWireView
	Hold           HoldTransitionWireView
}

type RevisionHeaderView struct {
	Format         uint16
	Log            LogID
	Catalog        CatalogRef
	CatalogSet     CatalogSetDigest
	Deployment     DeploymentFingerprint
	Operation      OperationName
	OperationID    OperationID
	RevisionID     RevisionID
	HasIdempotency bool
	Idempotency    IdempotencyToken
	ObservedAt     time.Time
	Retention      RetentionClass
	Consequence    Consequence
	RetentionBasis RetentionBasisDigest
	Authorization  RevisionAuthorizationSummaryView
	Semantic       SemanticDigest
	Envelope       EnvelopeDigest
	Integrity      IntegrityDigest
	Seal           Seal
}

type RevisionWireView struct {
	Header  RevisionHeaderView
	Actors  []StoredActorView
	Context []StoredContextFactView
	Items   []ItemWireView
}

type revision struct {
	view RevisionWireView
}

type Revision struct {
	value revision
}

func newRevision(view RevisionWireView) (Revision, error) {
	if err := validateRevisionView(view); err != nil {
		return Revision{}, err
	}
	return Revision{value: revision{view: cloneRevisionView(view)}}, nil
}

func (r Revision) View() RevisionWireView {
	return cloneRevisionView(r.value.view)
}

func (r Revision) valid() bool {
	return validateRevisionView(r.value.view) == nil
}

func validateRevisionView(view RevisionWireView) error {
	header := view.Header
	if header.Format != revisionFormatV1 || header.Log == (LogID{}) || !header.Catalog.valid() || header.CatalogSet == (CatalogSetDigest{}) {
		return auditErrorAt(ErrMalformedEvidence, "header")
	}
	if !validSemanticName(string(header.Operation)) || header.OperationID == (OperationID{}) || header.RevisionID == (RevisionID{}) {
		return auditErrorAt(ErrMalformedEvidence, "operation")
	}
	if header.ObservedAt.IsZero() || !validSemanticLabel(string(header.Retention)) || !header.Consequence.Valid() {
		return auditErrorAt(ErrMalformedEvidence, "policy")
	}
	if header.Semantic == (SemanticDigest{}) || header.Envelope == (EnvelopeDigest{}) || header.Integrity == (IntegrityDigest{}) {
		return auditErrorAt(ErrMalformedEvidence, "digest")
	}
	if header.HasIdempotency != (header.Idempotency != (IdempotencyToken{})) {
		return auditErrorAt(ErrMalformedEvidence, "idempotency")
	}
	if len(view.Items) == 0 || len(view.Items) > MaxItems || len(view.Actors) > MaxActorHops || len(view.Context) > int(SourceContext) {
		return auditTooLarge("revision", MaxRevisionBytes)
	}
	for index, item := range view.Items {
		if item.Ordinal != uint16(index) || !item.Kind.Valid() || !validSemanticName(string(item.Resource)) || !validSemanticName(string(item.Action)) {
			return auditErrorAt(ErrMalformedEvidence, "items")
		}
	}
	return nil
}

func cloneRevisionView(view RevisionWireView) RevisionWireView {
	view.Header.Authorization.Resources = slices.Clone(view.Header.Authorization.Resources)
	view.Header.Authorization.Actions = slices.Clone(view.Header.Authorization.Actions)
	view.Header.Authorization.Classifications = slices.Clone(view.Header.Authorization.Classifications)
	view.Header.Authorization.Coordinates = cloneCoordinates(view.Header.Authorization.Coordinates)
	view.Actors = slices.Clone(view.Actors)
	for index := range view.Actors {
		view.Actors[index].Reference = cloneStoredValue(view.Actors[index].Reference)
	}
	view.Context = slices.Clone(view.Context)
	for index := range view.Context {
		view.Context[index].Plaintext = bytes.Clone(view.Context[index].Plaintext)
	}
	view.Items = slices.Clone(view.Items)
	for index := range view.Items {
		view.Items[index].Subject = cloneStoredValue(view.Items[index].Subject)
		view.Items[index].Target = cloneStoredValue(view.Items[index].Target)
		view.Items[index].Values = cloneStoredValues(view.Items[index].Values)
		view.Items[index].Changes = slices.Clone(view.Items[index].Changes)
		for change := range view.Items[index].Changes {
			view.Items[index].Changes[change].Before = cloneStoredValue(view.Items[index].Changes[change].Before)
			view.Items[index].Changes[change].After = cloneStoredValue(view.Items[index].Changes[change].After)
		}
	}
	return view
}

func cloneStoredValues(values []StoredValueView) []StoredValueView {
	copy := slices.Clone(values)
	for index := range copy {
		copy[index] = cloneStoredValue(copy[index])
	}
	return copy
}

func cloneStoredValue(value StoredValueView) StoredValueView {
	value.Codec.ReadVersions = slices.Clone(value.Codec.ReadVersions)
	value.Plaintext = bytes.Clone(value.Plaintext)
	return value
}

func cloneCoordinates(values []QueryCoordinateView) []QueryCoordinateView {
	copy := slices.Clone(values)
	for index := range copy {
		copy[index].Alternatives = slices.Clone(copy[index].Alternatives)
		for alternative := range copy[index].Alternatives {
			copy[index].Alternatives[alternative].Plaintext = bytes.Clone(copy[index].Alternatives[alternative].Plaintext)
			copy[index].Alternatives[alternative].Tokens = slices.Clone(copy[index].Alternatives[alternative].Tokens)
		}
	}
	return copy
}
