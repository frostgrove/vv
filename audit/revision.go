package audit

import (
	"bytes"
	"reflect"
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
	attempts := 0
	for index, item := range view.Items {
		if item.Ordinal != uint16(index) || !item.Kind.Valid() || !validSemanticName(string(item.Resource)) || !validSemanticName(string(item.Action)) {
			return auditErrorAt(ErrMalformedEvidence, "items")
		}
		if item.Kind == AttemptItem {
			attempts++
			if attempts > 1 {
				return auditErrorAt(ErrMalformedEvidence, "items.attempt")
			}
			if err := validateAttemptItem(view.Header, item); err != nil {
				return err
			}
		} else if item.Attempt != (AttemptTransitionWireView{}) {
			return auditErrorAt(ErrMalformedEvidence, "items.attempt")
		}
	}
	return nil
}

func validateAttemptItem(header RevisionHeaderView, item ItemWireView) error {
	zeroStored := StoredValueView{}
	if item.EntityState != 0 || item.Chain != (EntityChainID{}) || !reflect.DeepEqual(item.Subject, zeroStored) || !item.OccurredAt.IsZero() || item.Outcome != "" || item.AccessRequest != (AccessRequestDigest{}) || item.AccessGrant != (AccessGrantDigest{}) || item.AccessResult != (AccessResultDigest{}) || item.ControlRequest != (ControlRequestDigest{}) || item.ControlGrant != (ControlGrantDigest{}) || item.ControlResult != (ControlResultDigest{}) || item.DenialRequest != (DenialRequestDigest{}) || len(item.Changes) != 0 || item.CorrectionOf != (ItemRef{}) || item.DisputeOf != (ItemRef{}) || !reflect.DeepEqual(item.Hold, HoldTransitionWireView{}) {
		return auditErrorAt(ErrMalformedEvidence, "attempt.item")
	}
	transition := item.Attempt
	wantAction, ok := attemptTransitionAction(transition.Kind)
	if !ok || item.Action != wantAction || header.Operation != transition.Operation || header.OperationID != transition.OperationID {
		return auditErrorAt(ErrMalformedEvidence, "attempt.transition")
	}
	if err := validateAttemptTransition(header, item); err != nil {
		return err
	}
	switch transition.Kind {
	case AttemptStartedTransition:
		if transition.Result.TargetPresent != storedValuePresent(item.Target) || transition.Result.TargetPresent && !validStoredAttemptTarget(item.Target) || !transition.Result.TargetPresent && !reflect.DeepEqual(item.Target, zeroStored) || item.Reason != "" {
			return auditErrorAt(ErrMalformedEvidence, "attempt.start")
		}
	case AttemptCheckpointTransition:
		if !reflect.DeepEqual(item.Target, zeroStored) || item.Reason != "" || !validSemanticName(string(transition.Checkpoint)) {
			return auditErrorAt(ErrMalformedEvidence, "attempt.checkpoint")
		}
	case AttemptSucceededTransition:
		if !reflect.DeepEqual(item.Target, zeroStored) || item.Reason != "" || transition.Checkpoint != "" {
			return auditErrorAt(ErrMalformedEvidence, "attempt.finish")
		}
	case AttemptFailedTransition, AttemptCancelledTransition, AttemptOutcomeUnknownTransition:
		if !reflect.DeepEqual(item.Target, zeroStored) || !validSemanticName(string(item.Reason)) || transition.Checkpoint != "" {
			return auditErrorAt(ErrMalformedEvidence, "attempt.finish")
		}
	case AttemptAbandonedTransition:
		if !reflect.DeepEqual(item.Target, zeroStored) || item.Reason != "" || transition.Checkpoint != "" || len(item.Values) != 0 {
			return auditErrorAt(ErrMalformedEvidence, "attempt.abandon")
		}
	}
	for _, value := range item.Values {
		if !validStoredAttemptValue(value) {
			return auditErrorAt(ErrMalformedEvidence, "attempt.values")
		}
	}
	return nil
}

func storedValuePresent(value StoredValueView) bool {
	return value.State == ValuePresent || value.State == ValueRedacted
}

func validStoredAttemptValue(value StoredValueView) bool {
	if !validSemanticName(string(value.Field)) || value.Codec.Name == "" || !value.Classification.Valid() || !value.Mode.Valid() || value.State != ValuePresent && value.State != ValueRedacted && value.State != ValueAbsent {
		return false
	}
	return validStoredAttemptPayload(value)
}

func validStoredAttemptTarget(value StoredValueView) bool {
	if value.Field != "" || !reflect.DeepEqual(value.Codec, ReferenceText().Description()) || !value.Classification.Valid() || !value.Mode.Valid() || value.State != ValuePresent && value.State != ValueRedacted {
		return false
	}
	return validStoredAttemptPayload(value)
}

func validStoredAttemptPayload(value StoredValueView) bool {
	switch value.State {
	case ValueAbsent:
		return len(value.Plaintext) == 0 && !value.Redacted && value.Token == (Token{}) && protectedValueZero(value.Protected)
	case ValueRedacted:
		return value.Mode == AsRedacted && value.Redacted && len(value.Plaintext) == 0 && value.Token == (Token{}) && protectedValueZero(value.Protected)
	default:
		switch value.Mode {
		case AsPlaintext:
			return len(value.Plaintext) <= MaxValueBytes && !value.Redacted && value.Token == (Token{}) && protectedValueZero(value.Protected)
		case AsToken:
			return len(value.Plaintext) == 0 && !value.Redacted && len(value.Token.Bytes()) == 32 && protectedValueZero(value.Protected)
		case AsProtected:
			return len(value.Plaintext) == 0 && !value.Redacted && value.Token == (Token{}) && len(value.Protected.Ciphertext()) > 0
		case AsIndexedProtected:
			return len(value.Plaintext) == 0 && !value.Redacted && len(value.Token.Bytes()) == 32 && len(value.Protected.Ciphertext()) > 0
		}
	}
	return false
}

func protectedValueZero(value ProtectedValue) bool {
	return value.Algorithm() == "" && value.Profile() == "" && value.KeyID() == "" && len(value.Nonce()) == 0 && len(value.Ciphertext()) == 0
}

func validateAttemptTransition(header RevisionHeaderView, item ItemWireView) error {
	value := item.Attempt
	ref := ItemRef{Revision: RevisionRef{Catalog: header.Catalog, Revision: header.RevisionID}, Ordinal: item.Ordinal}
	if value.Chain == (AttemptChainID{}) || value.Policy == (AttemptPolicyFingerprint{}) || value.Replay == (AttemptReplayFingerprint{}) || !validSemanticName(string(value.Operation)) || value.OperationID == (OperationID{}) || value.ScopePresent != (value.Scope != (EvidenceScopeCommitment{})) || !value.Start.valid() || value.Sequence == 0 || value.Sequence > MaxAttemptTransitions || value.CheckpointCount > value.Sequence-1 || value.ExpiresAt.IsZero() || !value.ExpiresAt.Equal(canonicalTime(value.ExpiresAt)) || value.Result.Chain != value.Chain || value.Result.Policy != value.Policy || value.Result.Replay != value.Replay || value.Result.Operation != value.Operation || value.Result.OperationID != value.OperationID || value.Result.ScopePresent != value.ScopePresent || value.Result.Scope != value.Scope || value.Result.Start != value.Start || value.Result.Head != ref || value.Result.Sequence != value.Sequence || value.Result.CheckpointCount != value.CheckpointCount || value.Result.ExpiresAt != value.ExpiresAt || value.Result.Owner == (AttemptOwnerCommitment{}) || value.Result.TargetPresent != (value.Result.Target != (AttemptTargetCommitment{})) {
		return auditErrorAt(ErrMalformedEvidence, "attempt.transition")
	}
	if value.Kind == AttemptStartedTransition {
		if value.Expected != (AttemptProjectionStateView{}) || value.Start != ref || value.Sequence != 1 || value.CheckpointCount != 0 || item.Previous != (LeafDigest{}) || value.Result.State != AttemptOpenState || value.Result.TransitionBytes == 0 || value.ResumeAuthorization != (AccessResultDigest{}) || !validAttemptTypeEdge(value, ref, true) {
			return auditErrorAt(ErrMalformedEvidence, "attempt.start")
		}
		return nil
	}
	if !validAttemptProjectionState(value.Expected) || value.Expected.Chain != value.Chain || value.Expected.Policy != value.Policy || value.Expected.Replay != value.Replay || value.Expected.Operation != value.Operation || value.Expected.OperationID != value.OperationID || value.Expected.ScopePresent != value.ScopePresent || value.Expected.Scope != value.Scope || value.Expected.Owner != value.Result.Owner || value.Expected.TargetPresent != value.Result.TargetPresent || value.Expected.Target != value.Result.Target || value.Expected.Start != value.Start || value.Expected.ExpiresAt != value.ExpiresAt || item.Previous != value.Expected.Leaf || value.Sequence != value.Expected.Sequence+1 || value.Result.TransitionBytes <= value.Expected.TransitionBytes {
		return auditErrorAt(ErrMalformedEvidence, "attempt.transition")
	}
	switch value.Kind {
	case AttemptCheckpointTransition:
		if value.Expected.State != AttemptOpenState || value.Result.State != AttemptOpenState || value.CheckpointCount != value.Expected.CheckpointCount+1 || value.TypeExpected != (AttemptTypeProjectionStateView{}) || value.TypeResult != (AttemptTypeProjectionNextView{}) {
			return auditErrorAt(ErrMalformedEvidence, "attempt.checkpoint")
		}
	case AttemptOutcomeUnknownTransition:
		if value.Expected.State != AttemptOpenState || value.Result.State != AttemptUncertainState || value.CheckpointCount != value.Expected.CheckpointCount || value.TypeExpected != (AttemptTypeProjectionStateView{}) || value.TypeResult != (AttemptTypeProjectionNextView{}) {
			return auditErrorAt(ErrMalformedEvidence, "attempt.unknown")
		}
	case AttemptSucceededTransition, AttemptFailedTransition, AttemptCancelledTransition, AttemptAbandonedTransition:
		want := attemptTerminalState(value.Kind)
		if value.Expected.State != AttemptOpenState && value.Expected.State != AttemptUncertainState || value.Result.State != want || value.CheckpointCount != value.Expected.CheckpointCount || !validAttemptTypeEdge(value, ref, false) {
			return auditErrorAt(ErrMalformedEvidence, "attempt.finish")
		}
	default:
		return auditErrorAt(ErrMalformedEvidence, "attempt.transition")
	}
	return nil
}

func validAttemptTypeEdge(value AttemptTransitionWireView, ref ItemRef, start bool) bool {
	expected := value.TypeExpected
	result := value.TypeResult
	if expected.Catalog != value.Result.Start.Revision.Catalog.ID || expected.Operation != value.Operation || expected.Policy != value.Policy || expected.Replay != value.Replay || result.Catalog != expected.Catalog || result.Operation != expected.Operation || result.Policy != expected.Policy || result.Replay != expected.Replay || result.Anchor != expected.Anchor || result.Head != ref {
		return false
	}
	if start {
		return expected.Unsettled < ^uint64(0) && result.Unsettled == expected.Unsettled+1
	}
	return expected.Unsettled > 0 && result.Unsettled == expected.Unsettled-1
}

func attemptTransitionAction(kind AttemptTransitionKind) (Action, bool) {
	switch kind {
	case AttemptStartedTransition:
		return AttemptStartedAction, true
	case AttemptCheckpointTransition:
		return AttemptCheckpointAction, true
	case AttemptOutcomeUnknownTransition:
		return AttemptOutcomeUnknownAction, true
	case AttemptSucceededTransition:
		return AttemptSucceededAction, true
	case AttemptFailedTransition:
		return AttemptFailedAction, true
	case AttemptCancelledTransition:
		return AttemptCancelledAction, true
	case AttemptAbandonedTransition:
		return AttemptAbandonedAction, true
	default:
		return "", false
	}
}

func attemptTerminalState(kind AttemptTransitionKind) AttemptState {
	switch kind {
	case AttemptSucceededTransition:
		return AttemptSucceededState
	case AttemptFailedTransition:
		return AttemptFailedState
	case AttemptCancelledTransition:
		return AttemptCancelledState
	case AttemptAbandonedTransition:
		return AttemptAbandonedState
	default:
		return 0
	}
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
