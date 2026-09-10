# AUDIT — CHECKPOINTED IMPLEMENTATION PLAN

**Status:** active implementation; S0/S1 complete, recorder/memory/basic history
and sealed CRUD alpha implemented, PostgreSQL append/deployment/basic-history
alpha implemented, advanced investigation and remaining integration proofs in progress

**Target repository:** `/home/user/ws/gd/lease/frostgrove/framework`
**Contract:** `.agents/artifacts/usecases/AUDIT_USECASES.md`
**Reconciliation:** `.agents/artifacts/usecases/AUDIT_RECONCILE.md`

This plan replaces the stale one-module/PostgreSQL-first topology in
`docs/roadmaps/2026-09-01-audit-log-roadmap.md`. The contract and plan passed
fresh happy-path/DX and adversarial/security review before implementation. Each
coherent implementation slice is checked by fresh happy-path and adversarial
reviewers after its executable checkpoint; critical and high findings gate the
next delivery slice, while lower-priority combinatorial hardening accumulates for
S7 so that the usable base is delivered first.

## 1. Release boundary

The release is one audit subsystem with four root packages and one nested module:

```text
audit                    policy, records, recorder, reader, store seam
audit/auditmemory        complete in-memory implementation
audit/audittest          conformance and application-policy proxies
audit/auditcrud          transaction-aware CRUD adapter
audit/auditpg            nested PostgreSQL module
```

Application-only fixtures integrate auth, security, tenancy, event, jobs,
storage, OTel, and module profiles. There are no pairwise production packages.

The implementation release includes:

- typed explicit business/security events;
- declared entity changes and reconstructable fields;
- declared durable attempts with explicit target presence and restart-safe state;
- deterministic catalog/manifest and canonical codecs;
- trusted context with actor-chain provenance;
- redacted, tokenized, and protected values before the store boundary;
- canonical digest and optional signature;
- grouped atomic append inside a proven transaction;
- in-memory and PostgreSQL stores;
- protected subject history and bound keyset cursors;
- correction, access evidence, integrity verification, retention inventory,
  legal hold, and hold-aware dry run;
- sealed security-gated CRUD no-ID Save, scoped Save, update, delete, and restore through the exact
  algorithms frozen below;
- explicit refusal/capability reports for ambiguous Save, write-only, bulk, raw SQL, ORM shortcuts,
  M2M, cascades, and external writers;
- conformance, unit, race, live PostgreSQL, graph, documentation, and example tests.

The first delivery milestone is a usable recorder/history core at the end of S2,
not the completion of every advanced investigation or lifecycle feature. The
practical application alpha immediately adds S4 CRUD and the core S6 Frostgrove
composition fixtures before advanced work. This vertical slice contains catalog
compilation, typed targeted and targetless events, declared entity revisions,
canonical codecs/context/privacy policy, the recorder, idempotent append/retry,
protection/integrity, the concurrent memory store, basic bounded subject/event
history, the reusable conformance core, audited CRUD, and executable
auth/tenancy/event/jobs/storage/error/OTel/module wiring. Attempts, advanced
selectors/reconstruction/lifecycle, PostgreSQL, and the remaining deployment
matrix then land as independently usable increments; an absent increment is an
explicit capability refusal rather than a partial claim.

Automated purge, external export delivery, CDC/triggers for arbitrary writers,
WORM anchoring, full relationship temporal graphs, and a UI remain follow-up
unless all preceding sections finish with budget and no critical gaps.

## 2. Contracts frozen before code

Names below are plan-level names. Review may improve spelling while preserving
every observable distinction and refusal.

### 2.1 Names, identities, and bounds

```go
type CatalogID string
type CatalogGeneration uint64
type CatalogDigest [32]byte
type CatalogSetDigest [32]byte
type LogID [16]byte
type BackingID [16]byte
type OperationName string
type OperationID [16]byte
type RevisionID [16]byte
type AttemptChainID [32]byte
type AttemptPolicyFingerprint [32]byte
type AttemptReplayFingerprint [32]byte
type CatalogActivationGateDigest [32]byte
type AttemptCheckpointCode string
type RetentionCohortID [32]byte
type HoldID [16]byte
type IdempotencyKey string
type IdempotencyToken [32]byte
type EntityChainKey [32]byte
type EntityChainID [32]byte
type HoldIdentity [32]byte
type HoldIDCommitment [32]byte
type HoldMatterCommitment [32]byte
type RequesterCommitment [32]byte
type AttemptOwnerCommitment [32]byte
type AttemptTargetCommitment [32]byte
type EvidenceScopeCommitment [32]byte
type LeafDigest [32]byte
type SelectionDigest [32]byte
type FenceDigest [32]byte
type AppendIntentDigest [32]byte
type CorrectionProposalDigest [32]byte
type DenialRequestDigest [32]byte
type HoldRequestDigest [32]byte
type RetentionBasisDigest [32]byte
type HoldSetDigest [32]byte
type AccessRequestDigest [32]byte
type AccessGrantDigest [32]byte
type AccessResultDigest [32]byte
type AccessContinuationDigest [32]byte
type ControlRequestDigest [32]byte
type ControlGrantDigest [32]byte
type ControlResultDigest [32]byte
type ControlContinuationDigest [32]byte
type Resource string
type Action string
type FieldName string
type Purpose string
type Owner string
type RetentionClass string
type Reason string
type Reference string
type Outcome string
type Source string
type FixtureName string
type PolicyVersion uint32
type PolicyFingerprint [32]byte
type PolicyFixtureFingerprint [32]byte
type CodecSemanticFingerprint [32]byte
type PrivacyReason string
type DeploymentLedger string
type DeploymentChange string

type Classification uint8
const (
	Public Classification = iota + 1
	Internal
	Personal
	Secret
)

type Consequence uint8
const (
	Required Consequence = iota + 1
	BestEffort
)

type ContextPresence uint8
const (
	ContextRequired ContextPresence = iota + 1
	ContextOptional
)

type ActorKind uint8
const (
	HumanActor ActorKind = iota + 1
	WorkloadActor
	ServiceActor
	SystemActor
	ExternalActor
)

type ScopedReference struct {
	Scope     Reference
	Reference Reference
}

type ItemRef struct {
	Revision RevisionRef
	Ordinal  uint16
}

type ItemKind uint8
const (
	EventItem ItemKind = iota + 1
	EntityItem
	AttemptItem
	AccessItem
	LifecycleItem
)

type ContextFactKind uint8
const (
	ActorChainContext ContextFactKind = iota + 1
	ScopeContext
	ServiceContext
	DeploymentContext
	ClientContext
	OperationContext
	CorrelationContext
	CausationContext
	TraceContext
	SourceContext
)

type CatalogChangeRef struct{ value catalogChangeRef }
type CatalogChangeRefView struct {
	Ledger DeploymentLedger
	Change DeploymentChange
}
func NewCatalogChangeRef(DeploymentLedger, DeploymentChange) (CatalogChangeRef, error)
func (r CatalogChangeRef) View() CatalogChangeRefView

const (
	MaxNameBytes       = 128
	MaxReferenceBytes  = 2048
	MaxIdempotencyKeyBytes = 512
	MaxNarrativeBytes  = 4096
	MaxActorHops       = 8
	MaxObservers       = 8
	MaxItems           = 256
	MaxFieldsPerItem   = 256
	MaxValueBytes      = 1 << 20
	MaxRevisionBytes   = 16 << 20
	MaxAppendRequestBytes = 40 << 20
	MaxPageRevisions   = 1000
	MaxPageBytes       = 32 << 20
	MaxStorePosition   = 4096
	MaxCursorBytes     = 8192
	MaxCursorLifetime  = 24 * time.Hour
	MaxRetryTokenBytes = 40 << 20
	MaxReconcileKeyBytes = 4096
	MaxCatalogs        = 1024
	MaxCatalogDeclarations = 65_536
	MaxCatalogManifestBytes = 16 << 20
	MaxCatalogSetBytes      = 128 << 20
	MaxCatalogMutations     = 2048
	MaxCatalogMutationBytes = 4 << 20
	MaxCatalogActivationAttemptTypes = 4096
	MaxCatalogActivationProofBytes = 128 << 20
	MaxOperationMembers     = 256
	MaxCodesPerDeclaration  = 256
	MaxPolicyGoldens        = 256
	MaxCodecFixtures        = 1024
	MaxCodecFixtureBytes    = 1 << 20
	MaxCodecFixtureSetBytes = 16 << 20
	MaxQueryResources       = 256
	MaxQueryActions         = 256
	MaxQueryClassifications = 256
	MaxQueryCoordinates     = 512
	MaxQuerySelectors       = 16
	MaxSelectorAlternatives = 64
	MaxExactTargets     = 10_000
	MaxQueryWindow      = 100 * 365 * 24 * time.Hour
	MaxAttemptOpenLifetime = 365 * 24 * time.Hour
	MaxAttemptSettlementTimeout = time.Minute
	MaxAttemptCheckpoints  = 256
	MaxAttemptTransitions  = 259
	MaxAttemptStateBytes   = 4 << 20
	MaxRetentionCohortMembers = MaxAttemptTransitions
	MaxHoldsPerRevision = 256
	MaxHoldTransitionsPerRevision = 512
	MaxHoldStateBytes        = 4 << 20
	MaxInventoryCandidates = 10_000
	MaxInventoryCohorts = 10_000
	MaxInventoryResultBytes = 32 << 20
	MaxInventoryFenceBytes  = 8 << 20
	MaxInventoryCursorBytes = 8192
	MaxSnapshotBytes        = 8 << 20
	MaxSearchCohortRevisions = 1_000_000
	MaxSearchCohortBytes     = 256 << 20
	MaxFenceLifetime        = 24 * time.Hour
	MaxControlCursorLifetime = 24 * time.Hour
)
```

Semantic names match `^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`; they are never
trimmed, folded, or normalized. References are distinct types, bounded valid
UTF-8, NUL/control-free, opaque, and byte-exact. `OperationID` names one attempted
unit of work across all AttemptTypes in a CatalogID lineage, while `RevisionID`
names one proposed stored revision. For an ordinary Record, an optional
`IdempotencyKey` names a retry within `(CatalogID, OperationName)`; an
attempt-start key is kind-separated and unique across the CatalogID, and an
attempt-transition key is scoped to its stable AttemptChainID. CorrelationFact,
not OperationID reuse, groups several logical attempts under one umbrella request.
Injected entropy generates IDs in one canonical lowercase text form. Distinct
identity families cannot substitute for one another at compile time. A HoldID
names a legal matter independently of every revision membership; LogID is the
durable identity of one audit log across process restarts and backups.
DeploymentLedger uses the semantic-name grammar; DeploymentChange is a bounded
Reference-shaped opaque identifier. CatalogChangeRef requires both and renders no
authority or credential.

### 2.2 Declarations, catalog, codecs, and privacy admission

```go
type Catalog struct{ value catalog }
type ResourcePolicy[M any, ID comparable] struct{ value resourcePolicy[M, ID] }
type ResourceHistory[M any, ID comparable] struct{ value resourceHistory[M, ID] }
type EventType[E any] struct{ value eventType[E] }
type OperationType struct{ value operationType }
type Declaration interface{ auditDeclaration() }

type CatalogSpec struct {
	ID         CatalogID
	Owner      Owner
	Generation CatalogGeneration
	Previous   CatalogRef
	Retention  []RetentionRule
	Semantics  SemanticDigestDescription
	Identities IdentityCommitmentDescription
	Protection ProtectionDescription
	Tokens     TokenDescription
	Integrity  IntegrityPolicy
	Control    ControlPolicy
}

type CalendarPeriod struct {
	Years  uint16
	Months uint8
	Days   uint16
}

type RetentionRule struct{ value retentionRule }
func KeepFor(RetentionClass, CalendarPeriod) RetentionRule
func TryKeepFor(RetentionClass, CalendarPeriod) (RetentionRule, error)
func KeepForever(RetentionClass) RetentionRule
func TryKeepForever(RetentionClass) (RetentionRule, error)
func RetentionRules(...RetentionRule) []RetentionRule

type SignatureDescription struct {
	Algorithm string
	Profile   string
	KeyID     string
}

type ProtectionDescription struct {
	Algorithm string
	Profile   string
	KeyID     string
}

type TokenDescription struct {
	Algorithm string
	Profile   string
	KeyID     string
}

type IntegrityPolicy struct{ value integrityPolicy }
func IntegrityOnly() IntegrityPolicy
func RequireSignature(SignatureDescription) IntegrityPolicy

type IdentityCommitmentDescription struct {
	Algorithm string
	Profile   string
	KeyID     string
}

type CatalogRef struct {
	ID         CatalogID
	Generation CatalogGeneration
	Digest     CatalogDigest
}

type HistoricalCatalog struct{ value historicalCatalog }
type CatalogSet struct{ value catalogSet }
type Manifest struct{ value manifest }
type ManifestView struct {
	Ref          CatalogRef
	Previous     CatalogRef
	Owner        Owner
	Semantics    SemanticDigestDescription
	Identities   IdentityCommitmentDescription
	Protection   ProtectionDescription
	Tokens       TokenDescription
	Integrity    IntegrityPolicyView
	Retention    []RetentionRuleView
	Declarations []DeclarationDescription
	Codecs       []CodecDescription
	Contexts     []ContextPolicyDescription
	Control      ControlPolicyDescription
}

func Lineage(active *Catalog, retained ...HistoricalCatalog) (*CatalogSet, error)
func Retain(*Catalog) HistoricalCatalog
func (c *Catalog) Ref() CatalogRef
func (c *Catalog) Manifest() Manifest
func (s *CatalogSet) Active() CatalogRef
func (s *CatalogSet) Digest() CatalogSetDigest
func (s *CatalogSet) Manifests() []Manifest
func (s *CatalogSet) Manifest(CatalogRef) (Manifest, bool)
func CatalogSetDigestOf([]Manifest) (CatalogSetDigest, error)
func (m Manifest) Ref() CatalogRef
func (m Manifest) Previous() CatalogRef
func (m Manifest) Canonical() []byte
func (m Manifest) View() ManifestView

func Compile(CatalogSpec, ...Declaration) (*Catalog, error)
func Define[M any, ID comparable](Policy[M, ID]) *ResourcePolicy[M, ID]
func TryDefine[M any, ID comparable](Policy[M, ID]) (*ResourcePolicy[M, ID], error)
func Declare[E any](EventPolicy[E]) *EventType[E]
func TryDeclare[E any](EventPolicy[E]) (*EventType[E], error)
func DeclareOperation(OperationPolicy) *OperationType
func TryDeclareOperation(OperationPolicy) (*OperationType, error)

type AttemptType[S, C, F any] struct{ value attemptType[S, C, F] }
type AttemptPolicy[S, C, F any] struct {
	Operation   *OperationType
	Semantics   PolicySemantics
	Descriptor  AttemptDescriptor
	MaxOpen     time.Duration
	MaxCheckpoints uint16
	MaxStateBytes uint64
	Continuity  AttemptContinuityPolicy
	Start       AttemptStartPolicy[S]
	Checkpoints AttemptCheckpointPolicy[C]
	Finish      AttemptFinishPolicy[F]
}
type AttemptStartPolicy[S any] struct{ value attemptStartPolicy[S] }
type AttemptCheckpointPolicy[C any] struct{ value attemptCheckpointPolicy[C] }
type AttemptFinishPolicy[F any] struct{ value attemptFinishPolicy[F] }
type AttemptTargetPolicy[S any] struct{ value attemptTargetPolicy[S] }
type AttemptField[P any] struct{ value attemptField[P] }
type AttemptFinishFieldSet[F any] struct{ value attemptFinishFieldSet[F] }
type AttemptTransitionFields[F any] struct{ value attemptTransitionFields[F] }
type AttemptCheckpointCodes struct{ value attemptCheckpointCodes }
type AttemptReasonPolicies struct{ value attemptReasonPolicies }
type AttemptReasonPolicy struct{ value attemptReasonPolicy }
type AttemptContinuityPolicy struct{ value attemptContinuityPolicy }
type AttemptOwnerFact uint8
const (
	AttemptEffectiveActorOwner AttemptOwnerFact = iota + 1
	AttemptWorkloadActorOwner
	AttemptServiceOwner
)
type AttemptOwnerIdentityComponent struct {
	Fact      AttemptOwnerFact
	ActorKind ActorKind
	Reference Reference
}
type AttemptOwnerIdentityInput struct {
	Catalog    CatalogID
	Operation  OperationName
	Components []AttemptOwnerIdentityComponent
}
type AttemptDescriptor struct {
	Resource    Resource
	Owner       Owner
	Purpose     Purpose
	Retention   RetentionClass
	Consequence Consequence
	Context     ContextPolicy
}

type AttemptTransitionKind uint8
const (
	AttemptStartedTransition AttemptTransitionKind = iota + 1
	AttemptCheckpointTransition
	AttemptOutcomeUnknownTransition
	AttemptSucceededTransition
	AttemptFailedTransition
	AttemptCancelledTransition
	AttemptAbandonedTransition
)
const (
	AttemptStartedAction Action = "attempt.started"
	AttemptCheckpointAction Action = "attempt.checkpoint"
	AttemptOutcomeUnknownAction Action = "attempt.outcome_unknown"
	AttemptSucceededAction Action = "attempt.succeeded"
	AttemptFailedAction Action = "attempt.failed"
	AttemptCancelledAction Action = "attempt.cancelled"
	AttemptAbandonedAction Action = "attempt.abandoned"
)

type AttemptState uint8
const (
	AttemptOpenState AttemptState = iota + 1
	AttemptUncertainState
	AttemptSucceededState
	AttemptFailedState
	AttemptCancelledState
	AttemptAbandonedState
)

func DeclareAttempt[S, C, F any](AttemptPolicy[S, C, F]) *AttemptType[S, C, F]
func TryDeclareAttempt[S, C, F any](AttemptPolicy[S, C, F]) (*AttemptType[S, C, F], error)
func AttemptStart[S any](AttemptTargetPolicy[S], []AttemptField[S]) AttemptStartPolicy[S]
func TryAttemptStart[S any](AttemptTargetPolicy[S], []AttemptField[S]) (AttemptStartPolicy[S], error)
func AttemptTarget[S any](func(S) Reference, Classification, StorageMode) AttemptTargetPolicy[S]
func TryAttemptTarget[S any](func(S) Reference, Classification, StorageMode) (AttemptTargetPolicy[S], error)
func NoAttemptTarget[S any]() AttemptTargetPolicy[S]
func AttemptOwnerIdentityRequest(AttemptOwnerIdentityInput) (IdentityCommitmentRequest, error)
func AttemptOwnerCommitmentOf(IdentityCommitmentSet) (AttemptOwnerCommitment, error)
func AttemptTargetIdentityRequest(CatalogID, OperationName, Reference) (IdentityCommitmentRequest, error)
func AttemptTargetCommitmentOf(IdentityCommitmentSet) (AttemptTargetCommitment, error)
func AttemptFields[P any](...AttemptField[P]) []AttemptField[P]
func AttemptValue[P, V any](FieldName, func(P) V, Codec[V], Classification) AttemptField[P]
func AttemptRedacted[P, V any](FieldName, func(P) V, Codec[V], Classification) AttemptField[P]
func AttemptTokenized[P, V any](FieldName, func(P) V, Codec[V], Classification) AttemptField[P]
func AttemptProtected[P, V any](FieldName, func(P) V, Codec[V], Classification) AttemptField[P]
func CheckpointCodes(...AttemptCheckpointCode) AttemptCheckpointCodes
func TryCheckpointCodes(...AttemptCheckpointCode) (AttemptCheckpointCodes, error)
func AttemptCheckpoints[C any](AttemptCheckpointCodes, func(C) AttemptCheckpointCode, ...AttemptField[C]) AttemptCheckpointPolicy[C]
func TryAttemptCheckpoints[C any](AttemptCheckpointCodes, func(C) AttemptCheckpointCode, ...AttemptField[C]) (AttemptCheckpointPolicy[C], error)
func NoAttemptCheckpoints[C any]() AttemptCheckpointPolicy[C]
func AttemptFinish[F any](AttemptReasonPolicies, AttemptFinishFieldSet[F]) AttemptFinishPolicy[F]
func TryAttemptFinish[F any](AttemptReasonPolicies, AttemptFinishFieldSet[F]) (AttemptFinishPolicy[F], error)
func AttemptFinishFields[F any](...AttemptTransitionFields[F]) AttemptFinishFieldSet[F]
func TryAttemptFinishFields[F any](...AttemptTransitionFields[F]) (AttemptFinishFieldSet[F], error)
func AttemptFieldsFor[F any](AttemptTransitionKind, ...AttemptField[F]) AttemptTransitionFields[F]
func TryAttemptFieldsFor[F any](AttemptTransitionKind, ...AttemptField[F]) (AttemptTransitionFields[F], error)
func AttemptReasons(...AttemptReasonPolicy) AttemptReasonPolicies
func TryAttemptReasons(...AttemptReasonPolicy) (AttemptReasonPolicies, error)
func AttemptReasonsFor(AttemptTransitionKind, ReasonCodes) AttemptReasonPolicy
func TryAttemptReasonsFor(AttemptTransitionKind, ReasonCodes) (AttemptReasonPolicy, error)
func AttemptOwnedBy(...AttemptOwnerFact) AttemptContinuityPolicy
func TryAttemptOwnedBy(...AttemptOwnerFact) (AttemptContinuityPolicy, error)

type SemanticGolden struct{ value semanticGolden }
type PolicySemantics struct{ value policySemantics }
type PolicySemanticsDescription struct {
	Version     PolicyVersion
	Fingerprint PolicyFingerprint
	Fixtures    []PolicyGoldenDescription
}
type PolicyFixtureKind uint8
const (
	DeclarationPolicyFixture PolicyFixtureKind = iota + 1
	AttemptStartPolicyFixture
	AttemptCheckpointPolicyFixture
	AttemptFinishPolicyFixture
)
type PolicyGoldenDescription struct {
	Name        FixtureName
	Fingerprint PolicyFixtureFingerprint
	Kind        PolicyFixtureKind
	Transition  AttemptTransitionKind
	Checkpoint  AttemptCheckpointCode
	Reason      Reason
}
func Semantics(PolicyVersion, ...SemanticGolden) PolicySemantics
func TrySemantics(PolicyVersion, ...SemanticGolden) (PolicySemantics, error)
func PolicyGolden(FixtureName, string) SemanticGolden
func TryPolicyGolden(FixtureName, string) (SemanticGolden, error)
func AttemptStartGolden(FixtureName, string) SemanticGolden
func TryAttemptStartGolden(FixtureName, string) (SemanticGolden, error)
func AttemptCheckpointGolden(FixtureName, AttemptCheckpointCode, string) SemanticGolden
func TryAttemptCheckpointGolden(FixtureName, AttemptCheckpointCode, string) (SemanticGolden, error)
func AttemptFinishGolden(FixtureName, AttemptTransitionKind, Reason, string) SemanticGolden
func TryAttemptFinishGolden(FixtureName, AttemptTransitionKind, Reason, string) (SemanticGolden, error)

type Policy[M any, ID comparable] struct {
	Model      *crud.Meta
	Semantics  PolicySemantics
	Descriptor Descriptor
	Subject    SubjectPolicy[ID]
	Actions    []EntityAction
	Fields     []EntityField[M]
}

type SubjectPolicy[ID comparable] struct{ value subjectPolicy[ID] }
func PlaintextSubject[ID comparable](func(ID) string, Classification) SubjectPolicy[ID]
func TokenizedSubject[ID comparable](func(ID) string, Classification) SubjectPolicy[ID]
func IndexedProtectedSubject[ID comparable](func(ID) string, Classification) SubjectPolicy[ID]

type EntityAction Action
const (
	EntityCreated EntityAction = "entity.created"
	EntityChanged EntityAction = "entity.changed"
	EntitySoftDeleted EntityAction = "entity.soft_deleted"
	EntityHardDeleted EntityAction = "entity.hard_deleted"
	EntityRestored EntityAction = "entity.restored"
)
type EntityStateKind uint8
const (
	EntityDeltaState EntityStateKind = iota + 1
	EntityFullState
)

func Actions(...EntityAction) []EntityAction
func Fields[M any](...EntityField[M]) []EntityField[M]
func (p *ResourcePolicy[M, ID]) Action(EntityAction) OperationMember
func (p *ResourcePolicy[M, ID]) History(*History) *ResourceHistory[M, ID]

type CodecVersion uint32
type CodecWireFixture struct{ value codecWireFixture }
type CodecSpec struct {
	Name         string
	WriteVersion CodecVersion
	ReadVersions []CodecVersion
	Fixtures     []CodecWireFixture
}
type CodecDescription struct {
	Name         string
	WriteVersion CodecVersion
	ReadVersions []CodecVersion
	Fingerprint  CodecSemanticFingerprint
}

type CodecEngine[V any] interface {
	Encode(V) ([]byte, error)
	Decode(CodecVersion, []byte) (V, error)
}
type Codec[V any] struct{ value codec[V] }

func GoldenWire(FixtureName, CodecVersion, []byte, []byte) CodecWireFixture
func TryGoldenWire(FixtureName, CodecVersion, []byte, []byte) (CodecWireFixture, error)
func RejectedWire(FixtureName, CodecVersion, []byte) CodecWireFixture
func TryRejectedWire(FixtureName, CodecVersion, []byte) (CodecWireFixture, error)
func DefineCodec[V any](CodecSpec, CodecEngine[V]) Codec[V]
func TryDefineCodec[V any](CodecSpec, CodecEngine[V]) (Codec[V], error)
func (c Codec[V]) Description() CodecDescription
func (c Codec[V]) Encode(V) ([]byte, error)
func (c Codec[V]) Decode(CodecVersion, []byte) (V, error)

func Text() Codec[string]
func Bool() Codec[bool]
func Int64() Codec[int64]
func Uint64() Codec[uint64]
func DecimalText() Codec[string]
func Bytes() Codec[[]byte]
func ReferenceText() Codec[Reference]
func UUIDReference() Codec[Reference]
func Duration() Codec[time.Duration]
func Time() Codec[time.Time]

type ModelMember[M, F any] struct{ value modelMember[M, F] }
type ReconstructField[M, V any] struct{ field typedField[M, V] }
type EntityField[M any] interface{ auditEntityField(*M) }
type EntityIndex[M, V any] struct{ value entityIndex[M, V] }
func Member[M, F any](func(*M) *F) ModelMember[M, F]
func TryMember[M, F any](func(*M) *F) (ModelMember[M, F], error)
func Reconstruct[M, V any](string, FieldName, Codec[V], Classification) ReconstructField[M, V]
func ReconstructBy[M, F, V any](ModelMember[M, F], FieldName, Codec[V], Classification) ReconstructField[M, V]
func HistoricalReconstruct[M, V any](FieldName, Codec[V], Classification) ReconstructField[M, V]
func Value[M, V any](string, FieldName, Codec[V], Classification) EntityField[M]
func ValueBy[M, F, V any](ModelMember[M, F], FieldName, Codec[V], Classification) EntityField[M]
func Optional[M, V any](string, FieldName, Codec[V], Classification) EntityField[M]
func OptionalBy[M, F, V any](ModelMember[M, F], FieldName, Codec[V], Classification) EntityField[M]
func Redacted[M, V any](string, FieldName, Codec[V], Classification) EntityField[M]
func RedactedBy[M, F, V any](ModelMember[M, F], FieldName, Codec[V], Classification) EntityField[M]
func Tokenized[M, V any](string, FieldName, Codec[V], Classification) EntityField[M]
func TokenizedBy[M, F, V any](ModelMember[M, F], FieldName, Codec[V], Classification) EntityField[M]
func Protected[M, V any](string, FieldName, Codec[V], Classification) EntityField[M]
func ProtectedBy[M, F, V any](ModelMember[M, F], FieldName, Codec[V], Classification) EntityField[M]
func EntityPlaintextIndex[M, V any](string, FieldName, Codec[V], Classification) EntityIndex[M, V]
func EntityTokenIndex[M, V any](string, FieldName, Codec[V], Classification) EntityIndex[M, V]
func EntityIndexedProtectedIndex[M, V any](string, FieldName, Codec[V], Classification) EntityIndex[M, V]
func EntityPlaintextIndexBy[M, F, V any](ModelMember[M, F], FieldName, Codec[V], Classification) EntityIndex[M, V]
func EntityTokenIndexBy[M, F, V any](ModelMember[M, F], FieldName, Codec[V], Classification) EntityIndex[M, V]
func EntityIndexedProtectedIndexBy[M, F, V any](ModelMember[M, F], FieldName, Codec[V], Classification) EntityIndex[M, V]
func (i EntityIndex[M, V]) Field() EntityField[M]

func (p *ResourcePolicy[M, ID]) Created(*M) (EntityDraft, error)
func (p *ResourcePolicy[M, ID]) Changed(*M, *M) (EntityDraft, bool, error)
func (p *ResourcePolicy[M, ID]) BaselineChanged(*M, *M) (EntityDraft, bool, error)
func (p *ResourcePolicy[M, ID]) Deleted(*M) (EntityDraft, error)
func (p *ResourcePolicy[M, ID]) Restored(*M) (EntityDraft, error)

type OutcomeCodes struct{ value outcomeCodes }
type ReasonCodes struct{ value reasonCodes }
func Outcomes(...Outcome) OutcomeCodes
func TryOutcomes(...Outcome) (OutcomeCodes, error)
func Reasons(...Reason) ReasonCodes
func TryReasons(...Reason) (ReasonCodes, error)

type EventPolicy[E any] struct {
	Semantics  PolicySemantics
	Descriptor Descriptor
	Target     TargetPolicy[E]
	Outcome    OutcomePolicy[E]
	Reason     ReasonPolicy[E]
	OccurredAt OccurredAtPolicy[E]
	Fields     []EventField[E]
}

type TargetPolicy[E any] struct{ value targetPolicy[E] }
type OutcomePolicy[E any] struct{ value outcomePolicy[E] }
type ReasonPolicy[E any] struct{ value reasonPolicy[E] }
type OccurredAtPolicy[E any] struct{ value occurredAtPolicy[E] }
type EventField[E any] struct{ value eventField[E] }
type EventIndex[E, V any] struct{ value eventIndex[E, V] }

func EventTarget[E any](func(E) Reference, Classification, StorageMode) TargetPolicy[E]
func NoEventTarget[E any]() TargetPolicy[E]
func EventOutcome[E any](OutcomeCodes, func(E) Outcome) OutcomePolicy[E]
func EventReason[E any](ReasonCodes, func(E) Reason) ReasonPolicy[E]
func OptionalEventReason[E any](ReasonCodes, func(E) Reason) ReasonPolicy[E]
func EventOccurredAt[E any](func(E) time.Time) OccurredAtPolicy[E]
func EventValue[E, V any](FieldName, func(E) V, Codec[V], Classification) EventField[E]
func EventRedacted[E, V any](FieldName, func(E) V, Codec[V], Classification) EventField[E]
func EventTokenized[E, V any](FieldName, func(E) V, Codec[V], Classification) EventField[E]
func EventProtected[E, V any](FieldName, func(E) V, Codec[V], Classification) EventField[E]
func EventPlaintextIndex[E, V any](FieldName, func(E) V, Codec[V], Classification) EventIndex[E, V]
func EventTokenIndex[E, V any](FieldName, func(E) V, Codec[V], Classification) EventIndex[E, V]
func EventIndexedProtectedIndex[E, V any](FieldName, func(E) V, Codec[V], Classification) EventIndex[E, V]
func (i EventIndex[E, V]) Field() EventField[E]
func EventFields[E any](...EventField[E]) []EventField[E]
func (e *EventType[E]) New(E) (Draft, error)

type OperationPolicy struct {
	Name        OperationName
	Semantics   PolicySemantics
	Retention   RetentionClass
	Consequence Consequence
	Context     ContextPolicy
	Members     []OperationMember
}

type OperationMember interface{ auditOperationMember() }
func OperationMembers(...OperationMember) []OperationMember

type ControlPolicy struct {
	Resource    Resource
	Semantics   PolicySemantics
	Purpose     Purpose
	Retention   RetentionClass
	Consequence Consequence
	Context     ContextPolicy
	Matter      HoldMatterPolicy
	Actions     []ControlAction
	Reasons     []ControlReasonPolicy
}

type ControlAction Action
const (
	HistoryRead ControlAction = "audit.history.read"
	HistoryDenied ControlAction = "audit.history.denied"
	ControlDenied ControlAction = "audit.control.denied"
	CorrectionAppended ControlAction = "audit.correction.appended"
	DisputeAppended ControlAction = "audit.dispute.appended"
	IntegrityVerified ControlAction = "audit.integrity.verified"
	InventoryRead ControlAction = "audit.inventory.read"
	HoldPlaced ControlAction = "audit.hold.placed"
	HoldReleased ControlAction = "audit.hold.released"
	PurgePlanned ControlAction = "audit.purge.planned"
	AttemptContinuationAuthorized ControlAction = "audit.attempt.continuation_authorized"
	AttemptAccessDenied ControlAction = "audit.attempt.access_denied"
)
func ControlActions(...ControlAction) []ControlAction
type ControlReasonPolicy struct{ value controlReasonPolicy }
type ControlReasonDescription struct {
	Action ControlAction
	Codes  []Reason
}
type ControlOutcomeDescription struct {
	Action ControlAction
	Codes  []Outcome
}
func ReasonsFor(ControlAction, ReasonCodes) ControlReasonPolicy
func TryReasonsFor(ControlAction, ReasonCodes) (ControlReasonPolicy, error)
func ControlReasons(...ControlReasonPolicy) []ControlReasonPolicy

type HoldMatterPolicy struct{ value holdMatterPolicy }
type HoldMatterDescription struct {
	Codec          CodecDescription
	Classification Classification
	Mode           StorageMode
}
func HoldMatter(Codec[Reference], Classification, StorageMode) HoldMatterPolicy
func TryHoldMatter(Codec[Reference], Classification, StorageMode) (HoldMatterPolicy, error)

type Descriptor struct {
	Resource    Resource
	Action      Action
	Owner       Owner
	Purpose     Purpose
	Retention   RetentionClass
	Consequence Consequence
	Context     ContextPolicy
}

type ProvenanceSet struct{ value provenanceSet }
type ContextFactPolicy struct{ value contextFactPolicy }
type ContextPolicy struct{ value contextPolicy }
type ContextFactDescription struct {
	Kind           ContextFactKind
	Presence       ContextPresence
	Allowed        []Provenance
	Classification Classification
	Mode           StorageMode
}
type ContextPolicyDescription struct {
	Facts []ContextFactDescription
}
type DeclarationKind uint8
const (
	ResourceDeclaration DeclarationKind = iota + 1
	EventDeclaration
	OperationDeclaration
	AttemptDeclaration
)
type SubjectDescription struct {
	Classification Classification
	Mode           StorageMode
}
type FieldDescription struct {
	Source         string
	Name           FieldName
	Codec          CodecDescription
	Classification Classification
	Mode           StorageMode
	Reconstruct    bool
	HistoricalOnly bool
	QueryIndex     bool
}
type AttemptReasonDescription struct {
	Transition AttemptTransitionKind
	Codes      []Reason
}
type AttemptPhaseDescription struct {
	TargetPresent bool
	Target        SubjectDescription
	Fields        []FieldDescription
}
type AttemptFinishPhaseDescription struct {
	Transition AttemptTransitionKind
	Fields     []FieldDescription
}
type AttemptDescription struct {
	Operation        OperationName
	Fingerprint      AttemptPolicyFingerprint
	MaxOpen          time.Duration
	MaxCheckpoints   uint16
	MaxStateBytes    uint64
	OpenReserveBytes   uint64
	UncertainReserveBytes uint64
	Continuity       []AttemptOwnerFact
	Start            AttemptPhaseDescription
	Checkpoint       AttemptPhaseDescription
	CheckpointCodes  []AttemptCheckpointCode
	Finish           []AttemptFinishPhaseDescription
	Reasons          []AttemptReasonDescription
}
type DeclarationDescription struct {
	Kind          DeclarationKind
	Resource      Resource
	Action        Action
	Operation     OperationName
	Semantics     PolicySemanticsDescription
	Owner         Owner
	Purpose       Purpose
	Retention     RetentionClass
	Consequence   Consequence
	Subject       SubjectDescription
	TargetPresent bool
	Target        SubjectDescription
	Actions       []EntityAction
	Outcomes      []Outcome
	Reasons       []Reason
	ReasonOptional bool
	Fields        []FieldDescription
	Context       ContextPolicyDescription
	Members       []string
	Attempt       AttemptDescription
}
func (p *ResourcePolicy[M, ID]) Description() DeclarationDescription
func (e *EventType[E]) Description() DeclarationDescription
func (o *OperationType) Description() DeclarationDescription
func (a *AttemptType[S, C, F]) Description() DeclarationDescription
func ComputeSubjectFixtureFingerprint[M any, ID comparable](*ResourcePolicy[M, ID], FixtureName, ID) (PolicyFixtureFingerprint, error)
func ComputeEventFixtureFingerprint[E any](*EventType[E], FixtureName, E) (PolicyFixtureFingerprint, error)
func ComputeAttemptStartFixtureFingerprint[S, C, F any](*AttemptType[S, C, F], FixtureName, S) (PolicyFixtureFingerprint, error)
func ComputeAttemptCheckpointFixtureFingerprint[S, C, F any](*AttemptType[S, C, F], FixtureName, C) (PolicyFixtureFingerprint, error)
func ComputeAttemptFinishFixtureFingerprint[S, C, F any](*AttemptType[S, C, F], FixtureName, AttemptCompletion[F]) (PolicyFixtureFingerprint, error)
type RetentionRuleView struct {
	Class   RetentionClass
	Forever bool
	Period  CalendarPeriod
}
type IntegrityPolicyView struct {
	RequiresSignature bool
	Signature         SignatureDescription
}
type ControlPolicyDescription struct {
	Resource    Resource
	Semantics   PolicySemanticsDescription
	Purpose     Purpose
	Retention   RetentionClass
	Consequence Consequence
	Context     ContextPolicyDescription
	Matter      HoldMatterDescription
	Actions     []ControlAction
	Reasons     []ControlReasonDescription
	Outcomes    []ControlOutcomeDescription
}

type PrivacyAdmission struct{ value privacyAdmission }
type PrivacyAdmissionView struct {
	Reason          PrivacyReason
	Plaintext       []Classification
}
type DeploymentFingerprint [32]byte

func AdmitPlaintext(PrivacyReason, ...Classification) (PrivacyAdmission, error)
func (p PrivacyAdmission) View() PrivacyAdmissionView
func (p PrivacyAdmission) Fingerprint() DeploymentFingerprint

type StorageMode uint8
const (
	AsPlaintext StorageMode = iota + 1
	AsRedacted
	AsToken
	AsProtected
	AsIndexedProtected
)

func Provenances(...Provenance) ProvenanceSet
func ContextFacts(...ContextFactPolicy) ContextPolicy
func TryContextFacts(...ContextFactPolicy) (ContextPolicy, error)
func ActorChain(ContextPresence, ProvenanceSet, Classification, StorageMode) ContextFactPolicy
func ScopeFact(ContextPresence, ProvenanceSet, Classification, StorageMode) ContextFactPolicy
func ServiceFact(ContextPresence, ProvenanceSet, Classification, StorageMode) ContextFactPolicy
func DeploymentFact(ContextPresence, ProvenanceSet, Classification, StorageMode) ContextFactPolicy
func ClientFact(ContextPresence, ProvenanceSet, Classification, StorageMode) ContextFactPolicy
func OperationFact(ContextPresence, ProvenanceSet, Classification, StorageMode) ContextFactPolicy
func CorrelationFact(ContextPresence, ProvenanceSet, Classification, StorageMode) ContextFactPolicy
func CausationFact(ContextPresence, ProvenanceSet, Classification, StorageMode) ContextFactPolicy
func TraceFact(ContextPresence, ProvenanceSet, Classification, StorageMode) ContextFactPolicy
func SourceFact(ContextPresence, ProvenanceSet, Classification, StorageMode) ContextFactPolicy
func GeneratedOperationFact(Classification, StorageMode) ContextFactPolicy
```

The ten built-ins publish stable description names under
`frostgrove.audit.codec.*`, WriteVersion 1, and the singleton read inventory
`[1]`. Text is byte-exact valid UTF-8. Bool is one byte, `0` or `1`. Int64 and
Uint64 are fixed eight-byte big-endian values, with signed integers in two's
complement. DecimalText accepts only `0` or an optional minus followed by a
nonzero integer without leading zeroes and an optional fractional part whose last
digit is nonzero; plus signs, exponent form, negative zero, whitespace, and
trailing fractional zeroes refuse. Bytes is an exact copy. ReferenceText encodes
one already validated bounded Reference as its exact UTF-8 bytes. UUIDReference accepts
only the lowercase 8-4-4-4-12 hexadecimal form and returns that exact Reference.
Duration is fixed-width big-endian int64 nanoseconds. Time is UTC int64 Unix
seconds plus uint32 nanoseconds; monotonic and location data never enter the wire.
Encode rejects a value that has no canonical spelling, and Decode rejects wrong
version, length, UTF-8, decimal, UUID, boolean, nanosecond, or noncanonical bytes
instead of normalizing them. Golden descriptions and bytes are permanent wire
fixtures. DefineCodec is the only custom-codec door; an engine cannot self-report
its manifest identity. It bounds and copies CodecSpec and fixtures before calling
the engine, requires unique valid fixture names, exact duplicate-free read
versions, at least one accepted fixture per readable version, and an accepted
write-version fixture whose wire equals its current encoding. Accepted fixtures
must decode and upcast to the declared current bytes twice; rejected fixtures
must refuse twice. Construction converts an engine panic, fixture-observable nondeterminism,
input/output alias, missing version, or changed rejection into a declaration
error and computes CodecSemanticFingerprint from the versioned, length-framed,
canonically sorted complete transcript. The description and fingerprint enter
every field declaration and manifest. A later write version retains executable
fixtures for every advertised older reader; reuse of a version after any fixture
or fingerprint change is a lineage conflict. Built-ins use this same wrapper.

DefineCodec cannot prove arbitrary future behavior from a finite construction
transcript. A custom CodecEngine is an explicit trusted application collaborator:
the wrapper enforces bounds, ownership, canonical fixture behavior, and the
declared transcript on construction, but it does not claim to detect a stateful
engine that changes only after those calls. Production callers keep an engine
deterministic, terminating, complexity-bounded for their workload, and
concurrency-safe for its declared version, run audittest application samples and
state-switch canaries in CI, and bump the version plus fixtures when its meaning
changes. The kernel cannot bound work consumed inside the opaque callback; it does
bound and copy every returned byte before any later fingerprint, cryptographic,
observer, or store work. The built-in codecs remain audit-owned and fully
specified.

Every nonzero PolicySemantics has a nonzero PolicyVersion and a canonical copied set of
named expected PolicyFixtureFingerprints. Resource and event policies require at
least one DeclarationPolicyFixture; operation and control policies, which contain no application
extractor, may have none. An AttemptPolicy requires typed golden coverage for at
least one start sample, every declared checkpoint code, every application-authored
finish transition, and every reason code of that transition. The zero reason is
legal only for Succeeded; a typed checkpoint/finish golden whose phase, code, or
reason does not exactly match its computed sample is rejected. Every resource,
event, operation, and nonempty control policy requires nonzero semantics; only the
completely empty recorder-only ControlPolicy retains a zero value. PolicyGolden
and the typed Attempt...Golden constructors accept exactly one valid FixtureName and
one lowercase 64-hex domain-separated SHA-256 expectation. Fixtures are synthetic
non-secret review data because their hashes are public manifest inputs, not a
privacy mechanism. Compile derives PolicyFingerprint from the version, golden
inventory, exact declarative description excluding the computed fingerprint
field itself, metadata identity, code sets, context policy, and codec
fingerprints. That description and fingerprint enter
the manifest and retained lineage. CheckSubjectGoldens and CheckEventGoldens run
typed application samples outside production declarations and compare their
canonical logical outputs with the committed expected fingerprints. The exact
fixture digest is SHA-256 over `frostgrove.audit/policy-fixture/v1`, fixture kind
and name, policy version, stable Resource/Action, and a length-framed pre-
protection logical output. A subject case includes the mapper result; an event
case includes exact target presence and a target only when present, outcome,
optional-reason presence, occurred time, and every
declared field with codec fingerprint, classification, mode, and canonical bytes.
It includes neither context, typed input bytes, the golden inventory, nor the
computed PolicyFingerprint, so there is no recursion or accidental sample
persistence.

No fingerprint contains a function address, symbol name, source path, closure
state, reflected implementation, or binary hash. Two implementations with the
same declaration, policy version, and golden transcript intentionally have the
same fingerprint. An unsampled behavior change is not mechanically detectable;
the policy author must bump PolicyVersion or CodecVersion and add a fixture that
kills the old behavior. Claiming an old version for changed behavior is a policy
contract violation, not something Go reflection can make safe.

Outcome and reason codes are manifest-public protocol labels with no
classification/storage-policy arm. The kernel mechanically enforces only their
bounded semantic-name grammar and exact action-scoped record-era membership; it
cannot infer that a grammatically valid label is narratively or semantically
sensitive. Declaration authors and reviewers are responsible for ensuring codes
contain no user text, identifier, secret, or narrative, with application canaries
for deployment vocabularies. A read returns them only after the containing action
and item pass the complete sealed grant. Sensitive explanation belongs in a
separately declared classified field; code membership never grants value
visibility.

ControlPolicy.Matter is required exactly when HoldPlaced or HoldReleased is
declared. It binds a Reference codec, classification, and storage mode into the
control-policy fingerprint and manifest. Only AsProtected or
AsIndexedProtected is legal; plaintext, token-only, and redacted matter
declarations refuse Compile. The raw command value is encoded once, committed
under CommitHoldMatter, then stored as a protected lifecycle value with its
control action, target, HoldID, policy fingerprint, codec, classification, and
mode in protection AAD. Its classification joins the immutable authorization
summary. A control policy with no hold action must have a zero Matter policy.

Each catalog is immutable and has a stable ID, owner, generation, predecessor,
canonical policy digest, and declaration inventory. Generation 1 alone has a
zero predecessor; every later generation names the exact preceding ref.
`Lineage` admits at most
MaxCatalogs exact generations of one ID and refuses a missing generation, fork,
wrong predecessor, generation reuse with another digest, semantic identifier or
policy/codec-fingerprint reuse with another meaning, and an undeclared weakening of field,
context, or subject protection. Writes use only Active. Historical manifests
authenticate retained rows and codec paths; the current policy alone grants
present access. Stable CatalogRef values, not declaration pointers, cross a
restart. CatalogSet.Manifests returns the canonical complete genesis-to-active
order. CatalogSetDigestOf accepts only that complete valid order and computes with
the same audit-owned algorithm as Lineage, so an external deployment/runtime store
never reimplements a private digest rule. A draft is
accepted only when its sealed declaration pointer and declaration fingerprint are both
members of the active catalog. There is no package registry, import-time registration,
or first-request discovery. Short declaration forms panic; `Try` siblings return
the same classified error. Declaration is sealed; only helper-produced
`*ResourcePolicy`, `*EventType`, and `*OperationType` values implement it, and
typed-nil or duplicate declarations are refused. Explicit action lists replace a
broad `CRUD` preset.
Every non-default operation name is an `OperationPolicy` declaration compiled
into the same catalog. `GroupSpec` and a standalone operation override carry the
sealed `OperationType`, never a free string; its retention and consequence are
therefore known and checked before a grouping callback runs. OperationPolicy
also allowlists exact catalog declaration/action members; Stage refuses any
other item and a member whose retention or consequence disagrees.
ControlPolicy explicitly selects one declared control Resource plus the
audit-of-audit actions, purpose, retention, consequence, and ContextPolicy for
history reads/denials, corrections,
verification, inventory, hold transitions, and purge plans. An empty control
policy is valid for recorder-only use; constructors refuse a missing action they
need. Its action-specific reason inventory and derived closed outcome table are
canonical manifest data. A policy declaring HistoryRead, CorrectionAppended, DisputeAppended,
IntegrityVerified, InventoryRead, HoldPlaced, HoldReleased, PurgePlanned,
AttemptContinuationAuthorized, or AttemptAccessDenied must
declare its own ScopeFact as ContextRequired in AsPlaintext, AsToken, or
AsIndexedProtected mode. Compile and Lineage also reject any present ScopeFact in
an active or retained evidence declaration that uses AsProtected or AsRedacted:
an absent optional scope contributes no coordinate, but a present operational
scope must always be equality-searchable. Recorder-only catalogs may retain
protected or redacted scope because they advertise no history or lifecycle
authorization. There are no silently registered internal events.

Every Descriptor and ControlPolicy RetentionClass resolves to exactly one rule in
its record-era manifest. Release 1 supports Forever or one positive UTC calendar
period of at most 100 years, anchored only to the kernel's integrity-bound
`ObservedAt`. Calendar
addition preserves the anniversary and clamps the day to the target month's last
day; overflow is a declaration error. A revision becomes age-eligible when that
inclusive computed instant is `<=` the Control-selected normalized AsOf instant.
For an origin inventory call Control invokes its trusted Clock exactly once,
strips monotonic/location data, and binds the exact UTC value through selection,
authorization, store query/result, cursor, and fence. The store selects only an
opaque snapshot identity and cannot select or advance time.
RetentionBasisDigestOf is fixed SHA-256 under the exact ASCII domain
`frostgrove.audit/retention-basis/v1` over the section 2.2 canonical,
length-framed CatalogRef, UTC ObservedAt with monotonic data removed, and complete
RetentionRuleView. It rejects a zero catalog/time, a malformed Forever/Period
one-of, nonpositive or over-hard periods, and the impossible zero output. S1
publishes exact Forever and clamped-calendar goldens. Kernel, memory, PostgreSQL,
inventory, and fence validation all call this helper; no adapter chooses another
digest. Store RecordedAt remains ordering metadata and can never
make evidence eligible earlier. Reusing one RetentionClass with another rule is a semantic-identity
conflict. A new class affects new records only; no current manifest reinterprets
old evidence, and every active hold still wins.

RetentionCohortIDOf uses fixed SHA-256 under
`frostgrove.audit/retention-cohort/v1`. A singleton revision cohort commits LogID,
kind, and its one canonical RevisionRef with zero Attempt. An attempt cohort
commits LogID, kind, nonzero AttemptChainID, and the complete sequence-ordered,
duplicate-free member RevisionRefs. It rejects an illegal kind/attempt one-of,
empty or over-hard members, duplicates, noncanonical refs, or impossible zero
output. A different member order
produces a different ID; NewInventoryResult rejects semantically reordered or
mixed-kind members after exact inspection because bare RevisionRefs cannot prove
either property. Memory, PostgreSQL, Control,
cursor, fence, and purge validation call this one helper; a backend cannot invent
a grouping identity.

Typed value declarations cover required, optional, redacted, tokenized,
protected, and reconstructable fields. Policy.Model is the exact metadata from
the repository blueprint. Its canonical TableRef and the relevant immutable
field descriptors enter the declaration fingerprint; binding the same Go model
to another table, column mapping, secret/type flag, or incompatible metadata is
a refusal. An entity field declaration retains its model type, source name,
evidence name, codec, classification, and mode. `Define` resolves it through the
exact Policy.Model field identity, checks the codec type, freezes the relevant
descriptor, and thereafter extracts through `Schema.Values`; it never accepts a
model getter. A secret metadata field
is refused for plaintext, protected, or reconstructable capture under every
evidence name; only redacted change-presence or keyed one-way token modes are
admissible. A reconstructable field retains its typed handle and versioned codec
chain; reconstruction stores typed field values internally and exposes no whole
model, so it needs no caller setter. `HistoricalReconstruct` is an explicit
read-only current-policy declaration for a stable field identity no longer
present in the live Go model. Compile requires an exact authenticated ancestor
identity and compatible retained decode/upcast path; it can authorize projection
but is never extracted into a new entity revision.
The `...By` forms are optional declaration-time sugar over the same exact
metadata identity. Member stores one typed direct-member selector. Define invokes
that selector exactly once on a fresh `M`, compares the returned pointer against
the exact pointers exposed by the supplied `crud.Meta`, and then discards both
the selector and fresh model. It rejects nil, panic, an external, copied, nested,
or relation pointer, an ambiguous field, incompatible optional/codec type,
another Meta, and every secret-incompatible mode. Only the resolved immutable
`*crud.Field` descriptor survives. Record, query, reconstruction, and every hot
path make zero selector or reflection calls. The string forms remain first-class
for generated manifests and historical fields; no form auto-enumerates fields,
infers names, privacy, codecs, storage, reconstructability, or audit inclusion
from Go types or tags. This bounded opt-in reflection removes a duplicate
source-name decision without weakening the explicit allowlist.
The subject is always the resolved primary-key field, checked against `ID`; the
declaration supplies only the bounded canonical encoder and classification for
that typed ID. Subject storage is deliberately named: PlaintextSubject,
TokenizedSubject, or IndexedProtectedSubject. A secret primary key is refused in
reversible/plain form and requires a keyed tokenized policy, applied identically
to writes and typed queries. Plaintext personal subjects additionally require the
deployment PrivacyAdmission; random protection without an index is not a
subject-query spelling.
Event declarations require a Resource and Action and choose exactly one of
EventTarget or NoEventTarget before extracting outcome, reason, occurred time,
and values from the typed occurrence. The targetless form emits no placeholder
Reference, token, protected envelope, or query coordinate. Target presence and,
when present, its complete policy enter the declaration/manifest fingerprint,
fixture digest, append semantic and envelope digests, access request/grant/result,
and catalog compatibility checks. Plaintext, tokenized, and indexed-protected
target modes are equality-searchable. Randomly protected or redacted targets remain
recordable but have no target-history capability and never fall back to a scan;
NoEventTarget has no Target or SelectTarget door at all. Applications choose indexed
protection when they need both a protected display value and equality search. Built-in codecs are
text, bool, signed/unsigned integer, canonical decimal text, copied bytes,
duration, UUID-like reference, and UTC timestamp; floating point, generic JSON,
maps, and whole-model serialization have no built-in spelling. Fixtures prove
round-trip, determinism, non-aliasing, and every retained upcast. Encode may
borrow its input only for the call and returns independently owned bytes. Decode
retains neither its input nor a reference shared with any prior result.
ProjectedValue retains canonical copied bytes and invokes the record-era decoder
for each successful Get, so mutating a returned slice, map, pointer, or nested
reference cannot alter the projection or a later Get. Conformance proxies mutate
every supported reference-shaped sample to enforce this ownership law.

OutcomeCodes and ReasonCodes are nonempty, duplicate-free copied sets in the same
bounded lowercase ASCII dot-segment grammar as other semantic names. A manual
event has one required declared outcome policy and either no reason policy, one
required EventReason, or one OptionalEventReason whose zero value alone means
absent. Each extractor runs exactly once. Its nonzero result must be a member of
the declaration's record-era set before value encoding, protection, semantic
commitment, or Writer I/O. Code meaning is scoped by catalog, declaration
Resource/Action, code kind, and exact bytes; strings are never normalized or
treated as global prose. Sorted sets and optionality enter
DeclarationDescription and the manifest. A historical item carrying a code not
in its authenticated record-era declaration is malformed evidence, not an
accepted unknown code.

ControlReasons binds a ReasonCodes set to the exact action that consumes an
application-authored reason. HistoryDenied, ControlDenied, CorrectionAppended,
DisputeAppended, and AttemptAccessDenied require nonempty sets when declared and reject a reason from
another action; other control actions accept no caller-authored reason. DenyAccess,
DenyControl, CorrectionCommand, and DisputeCommand are origin-bound first and
then checked against the corresponding current set before evidence or store I/O.
Kernel-generated entity and control outcomes come from the closed package-owned
table exposed in ControlPolicyDescription and cannot be supplied by a backend.
VerificationStatus is already closed and carries no free Reason. PrivacyReason is
a distinct deployment-waiver code and cannot masquerade as a persisted event or
control reason.

A reason narrative has no ambient field. An event that needs one declares a
bounded ordinary EventField with personal classification and Protected storage;
it is subject to the same allowlist, canary, and reveal policy as any value.
Manual event extractors and context mappers are trusted policy code because the
kernel cannot infer that an ordinary string semantically contains a credential;
`audittest` runs application-supplied token/password canaries as defense in depth.

An AttemptType is a fourth Declaration kind and links one exact sealed
OperationType from the same Catalog compilation. The operation cannot list the
attempt as a member because that would make declaration construction cyclic;
the attempt's authenticated Operation link is the membership edge. Its
distinct AttemptDescriptor has no Action slot because the closed transition kind
determines the wire action, while Resource, owner, purpose, retention,
consequence, and context are explicit. Start declares exactly one of
AttemptTarget or NoAttemptTarget and a typed field schema. A present target must
be equality-searchable; the targetless form emits no placeholder value, token,
coordinate, or target-history capability. AttemptTargetIdentityRequest frames a
present logical target under CommitAttemptTarget with AAD bound to CatalogID and
OperationName; AttemptTargetCommitmentOf accepts only that domain and a 32-byte
active commitment while retaining aliases for rotation. Checkpoint either has a
nonempty bounded code inventory, one code extractor, and optional typed fields,
or is explicitly NoAttemptCheckpoints; a zero accidental policy is invalid.
Finish has separate typed field sets per
legal completion transition, so a success-only receipt is never evaluated or
encoded for failure, and nonempty action-scoped reason inventories exactly for Failed, Cancelled, and
OutcomeUnknown; Succeeded has no reason and Abandoned uses a kernel-defined
reason bound by its explicit command. Each extractor is invoked once and its
copied return is bounded before crypto or store work. No request, response,
header, raw error, stack, arbitrary map, or ambient context capture API exists.
AttemptOwnedBy is a nonempty duplicate-free all-of selection over stable facts
already required and equality-searchable by the AttemptDescriptor ContextPolicy.
AttemptOwnerIdentityRequest canonical-sorts the selected facts by their closed
AttemptOwnerFact tag, rejects duplicate, missing, or unexpected components, and
length-frames each fact tag, ActorKind presence, and Reference. Its AAD binds the
version, CatalogID, and OperationName; the keyring commits that one complete tuple
under CommitAttemptOwner. AttemptOwnerCommitmentOf accepts only that domain and a
32-byte active commitment while preserving the full active/retained tuple alias
set for rotation lookup. It therefore commits only the selected effective actor,
workload actor, or service identity; scope is bound separately. Ephemeral client,
correlation, causation, trace, source, and unrelated actor hops never enter
AttemptOwnerCommitment. A policy that needs worker handoff selects its stable
service/workload owner; a human workflow may select the effective actor.
The AttemptDescriptor ContextPolicy must contain exactly one required,
equality-searchable, caller- or trusted-integration-supplied OperationFact.
GeneratedOperationFact is forbidden for an AttemptType: Begin and Run expose no
free OperationID override, so a restart must resolve the same OperationID before
any state lookup or callback. Compile rejects an optional, absent, generated,
non-searchable, or multiply declared operation fact.

AttemptPolicyFingerprint commits the exact linked operation identity, policy
semantics, descriptor, MaxOpen, MaxCheckpoints, MaxStateBytes, the computed Open
and Uncertain settlement byte reserves, continuity policy, target policy, phase
field schemas, checkpoint codes, and
transition-specific reason sets. AttemptDescription is copied into the manifest;
all its sets are canonical byte-sorted. MaxOpen is positive and no greater than
MaxAttemptOpenLifetime. MaxCheckpoints cannot exceed the hard or store limit and
must be zero exactly for NoAttemptCheckpoints. MaxStateBytes is positive and no
greater than the hard/store limit. Compile computes maximum canonical direct
terminal, OutcomeUnknown-plus-resolution, and Uncertain-to-terminal contributions
from every terminal schema. It refuses unless both the OpenReserveBytes and
UncertainReserveBytes paths always fit inside MaxStateBytes and MaxRevisionBytes
after maximum start/context overhead. A Catalog with attempts requires signature integrity.
Compile permits at most one AttemptType for
one `(CatalogID, OperationName)` and refuses a duplicate before fingerprinting.
It reserves all seven `(AttemptDescriptor.Resource, Attempt...Action)` identities
for that declaration and rejects collision with an Event, Resource action, or
another AttemptType; the zero single Action field in DeclarationDescription is
never used to bypass generic uniqueness.
The three ComputeAttempt...FixtureFingerprint helpers are offline/test doors.
They invoke only the selected start, checkpoint, or completion extractors once,
commit fixture name and phase/transition kind, target/code/reason, declared field
schema, and canonical pre-protection values, and retain no typed input or context.
Their output must match the named PolicyGolden already stored in Semantics;
runtime declarations never embed application fixtures.
At Compile, AttemptReplayFingerprintOf combines that declaration fingerprint
with CatalogSpec.Semantics under
`frostgrove.audit/attempt-replay-policy/v1`. The derived value lives in the
compiled manifest/runtime binding and every attempt transition/projection; it
does not mutate the reusable declaration or make Description depend on which
CatalogSpec compiled it. Replay compatibility and per-type counters use this
derived fingerprint, so semantic-digester rotation is an explicit incompatible
attempt policy change even when the declaration itself is byte-identical.
An active catalog may add a new AttemptType,
but compatibility across a lineage means byte-identical AttemptPolicyFingerprint
and AttemptReplayFingerprint. A removed or different replay fingerprint cannot
activate while its authenticated Unsettled count, exactly Open plus Uncertain,
is nonzero. Every Started and terminal transition atomically advances the
per-type counter certificate described in 2.5.

PrepareCatalogActivation verifies the origin-bound mutation log first. An exact
already-persisted expected/next/change-independent activation reconstructs its
original gate proof for deployment replay; a never-persisted request requires
Expected to be the current active catalog. It then diffs the exact current/next
manifests and builds a canonical gate for each removed/changed old policy. It refuses more than
MaxCatalogActivationAttemptTypes or MaxCatalogActivationProofBytes. For a
non-genesis counter it exact-inspects the one revision/item named by Head,
verifies the required record-era signature, and requires the projection to equal
that signed transition's type result before considering Unsettled. It does not
claim to walk or recompute the global, lifetime-unbounded type-counter history;
correctness of that cumulative certificate is inductive from each kernel-minted
append CAS. A malformed or mismatched claimed nonzero is verified before denial,
so it cannot be a cheap forged availability veto.
CatalogActivationConfig.Mutations is always required. Types, Exact, and Verifier
may be nil only for an exact persisted replay or a fresh diff with zero affected
attempt types; otherwise all three are mandatory and every supplied seam must
report the same process Backing, durable BackingID/LogID, and catalog lineage.
This permits genesis activation without inventing a runtime store while keeping
later proof reads least-privilege and explicit.

CatalogActivationGateDigestOf is the only gate-anchor algorithm. It hashes the
fixed ASCII domain `frostgrove.audit/catalog-attempt-gate/v1` followed by
length-framed BackingID, LogID, Expected, Next, and the complete canonical gate
input sorted by `(CatalogID, OperationName, PriorPolicy, PriorReplay)`. Duplicate
keys, a foreign catalog, an inconsistent next-presence/fingerprint shape, or an
impossible zero digest refuse. There is exactly one gate for every removed or
incompatible old replay-policy key and no gate for a compatible type. Each digest
input contains the exact Expected counter and its zero-count/head/leaf Result
next-view whose Anchor must be zero, plus the verified head integrity digest.
Thus neither the digest itself nor a Result anchor can enter its own preimage.
After computing the digest, PrepareCatalogActivation fills every gate
Result.Anchor with that digest; a compatible type leaves its projection untouched
outside the proof. Every other anchor shape refuses. Proof construction and
persisted replay recompute the same helper,
byte-compare every final gate, and require one canonical gate order. Bytes is a
derived encoded-size bound and is not an alternate digest input.

ActivateCatalog takes the sealed proof, locks and byte-compares every prepared
counter projection in the same backing transaction, requires Unsettled zero,
then replaces each affected projection with the gate's zero/headless Result
anchored by CatalogActivationGateDigest before the active-catalog CAS. The
append-only CatalogMutationView retains those bounded, value-free gate records,
so the sensitive revision that held the old counter head can later purge and a
future re-add continues from the non-purgeable catalog anchor. A stale prepared
zero or nonzero conflicts, and a concurrent zero-to-one start or one-to-zero
close has exactly one serial order. Replay and rollback change no projection.
A store that consistently replays both an old valid projection and its matching
signed head is rollback-equivalent and cannot be detected locally without an
external freshness anchor; the plan makes no stronger Byzantine claim.
The linearization point for a fresh activation is the transaction's active-head
CAS after all gate resets; for replay it is the read of the immutable exact
mutation row and no counter or catalog state changes.

`Config.Privacy` is a typed deployment admission. Its zero value permits
plaintext only for public and internal classifications. A broader admission has
a stable reason code and contributes to a stored deployment fingerprint. Exact
CRUD metadata-secret fields and explicitly Secret manual-event fields are never
reversible; arbitrary manual extractors remain trusted policy code. Personal
plaintext requires the visible admission.
Protection happens before Writer, Observer, or error handling sees bytes.

```go
type Clock interface {
	Now() time.Time
}

type IDSource interface {
	NewOperationID() (OperationID, error)
	NewRevisionID() (RevisionID, error)
}

type Observer interface {
	ObserveAudit(AuditObservation)
}
type ObserverFunc func(AuditObservation)
func (f ObserverFunc) ObserveAudit(AuditObservation)
func Observers(...Observer) (Observer, error)
func MustObservers(...Observer) Observer

type ObservationPhase uint8
const (
	ObservationResolve ObservationPhase = iota + 1
	ObservationPrepare
	ObservationStore
	ObservationVerify
	ObservationEvidence
)
type ObservationKind uint8
const (
	ObservationRecord ObservationKind = iota + 1
	ObservationGroup
	ObservationHistory
	ObservationReconstruction
	ObservationControl
	ObservationAttempt
)
type FailureClass uint8
const (
	NoFailure FailureClass = iota
	InvalidFailure
	DeniedFailure
	ConflictFailure
	NotWrittenFailure
	UnconfirmedFailure
	UnreadableFailure
	BackendFailure
)
type AuditWorkView struct {
	StoreCalls        uint32
	SnapshotReads     uint32
	PagesScanned      uint32
	RevisionsScanned  uint32
	BytesScanned      uint64
	RevisionsVerified uint32
	UnknownFields     uint32
	Gaps              uint32
	BudgetStopped     bool
}
type AuditObservation struct {
	Phase       ObservationPhase
	Kind        ObservationKind
	Items       uint16
	Bytes       uint64
	Duration    time.Duration
	Disposition AppendDisposition
	Settlement  Settlement
	Failure     FailureClass
	Work        AuditWorkView
}

type Config struct {
	Catalogs  *CatalogSet
	Writer    Writer
	Context   ContextResolver
	Privacy   PrivacyAdmission
	Semantics SemanticDigester
	Identities IdentityKeyring
	Protector Protector
	Tokenizer Tokenizer
	Signer    Signer
	Clock     Clock
	IDs       IDSource
	Observer  Observer
}
```

Nil Clock and IDs select the stdlib UTC clock and cryptographic ID source; tests
inject deterministic implementations. ContextResolver is always explicit;
`StaticContext` is the validated no-ambient helper for a workload with fixed
system facts. CatalogSpec freezes the exact semantic, identity, protection,
token, and signature descriptions used by that generation. New requires each
configured active collaborator to report the active manifest's exact description
and requires every retained identity/token description needed for alias or query
continuity; an extra, missing, duplicate, or mismatched description refuses
without I/O. SemanticDigester and IdentityKeyring are always required;
Protector, Tokenizer, and Signer are required exactly when the active catalog
uses their write capability. The root HMAC semantic digester copies a
minimum 32-byte key and publishes only an algorithm/profile and key ID; raw
low-entropy protected values are never exposed through an unkeyed digest. Nil
Observer is a no-op. ObserverFunc is the zero-boilerplate adapter. Observers
copies at most MaxObservers non-nil, non-typed-nil children, rejects an oversized
or empty effective set, and invokes them synchronously in declaration order;
each child panic is isolated independently and cannot skip later children or
alter evidence/result bytes. MustObservers panics only on the same construction
error. AuditObservation is copied plain diagnostic data containing
only bounded enums, counts, bytes, duration, disposition, settlement, safe failure
class, and AuditWorkView; it has no identifiers, context, query, or error.
AuditWorkView reports checked store calls, snapshot reads, pages/revisions/bytes
scanned, revisions verified, aggregate unknown/gap counts, and budget stop. Counts
are exact per-observation phase deltas, never estimates or cumulative repeats.
StoreCalls counts calls actually invoked; SnapshotReads counts successfully
accepted origin/resume snapshot reads; PagesScanned and RevisionsScanned count
structurally accepted store pages and their complete revisions; BytesScanned is
the exact wire-byte charge; RevisionsVerified counts revisions that reached a
terminal authenticated valid/invalid verification result rather than a backend
failure; UnknownFields counts requested terminal field knowledge other than Known
or Absent, with Gaps its FieldGap subset; BudgetStopped is true exactly when that
phase stopped on an enforced work budget. Every inapplicable member is zero.
Counts advance only after the named bounded event, use checked addition, and
saturate at the operation's enforced hard ceiling rather than wrap; saturation
sets BudgetStopped and cannot affect the authoritative ceiling or result. Snapshot
identity and every resource/action/scope/subject/selector/revision/value/key/SQL/
error label remain absent. New validates all of this without I/O.

All injected interfaces are concurrently callable unless their contract
explicitly says the kernel serializes them; release 1 chooses concurrent-call
contracts for every store seam, CatalogAdmin, context/authority/denial services,
codec and cryptographic collaborator, Clock, IDSource, and Observer. Built-ins
meet that contract. Conformance race doubles overlap calls and mutate returned
buffers to prove ownership; a provider needing serialization must wrap itself
explicitly at composition rather than relying on accidental Recorder locking.

### 2.3 Trusted context and immutable records

```go
type Provenance uint8

const (
	UnstatedProvenance Provenance = iota
	ServerDerived
	Verified
	Forwarded
	ClientSupplied
)

type Actor struct {
	Kind       ActorKind
	Reference Reference
	Provenance Provenance
}

type ContextValue[T any] struct{ value contextValue[T] }
func NewContextValue[T any](T, Provenance) (ContextValue[T], error)
func (v ContextValue[T]) Get() (T, bool)
func (v ContextValue[T]) Provenance() Provenance

type Context struct {
	Actors      []Actor
	Scope       ContextValue[ScopedReference]
	Service     ContextValue[Reference]
	Deployment  ContextValue[Reference]
	Client      ContextValue[ScopedReference]
	Operation   ContextValue[OperationID]
	Correlation ContextValue[Reference]
	Causation   ContextValue[Reference]
	Trace       ContextValue[Reference]
	Source      ContextValue[Source]
}

type ContextView struct {
	Actors      []Actor
	Scope       ContextValue[ScopedReference]
	Service     ContextValue[Reference]
	Deployment  ContextValue[Reference]
	Client      ContextValue[ScopedReference]
	Operation   ContextValue[OperationID]
	Correlation ContextValue[Reference]
	Causation   ContextValue[Reference]
	Trace       ContextValue[Reference]
	Source      ContextValue[Source]
}
func (c Context) View() ContextView

type ContextResolver interface {
	ResolveAuditContext(context.Context) (Context, error)
}
type ContextResolverFunc func(context.Context) (Context, error)
func (f ContextResolverFunc) ResolveAuditContext(context.Context) (Context, error)

func StaticContext(Context) (ContextResolver, error)
```

Actor 0 is initiator and the final actor is effective. Every actor hop and every
other present fact carries one nonzero provenance label. Provenance labels are
incomparable: a ProvenanceSet is exact membership and no `<`, `>=`, enum-order,
or implicit strength rule exists. Every outer operation chooses one sealed
ContextPolicy before resolving: its compiled action for a standalone item and
the OperationPolicy's own policy for a group. Member policies are never unioned.
Context is resolved once, copied, filtered to declared facts, and only then
canonically committed and protected. Undeclared optional facts are discarded
before a custom collaborator sees them. Missing required facts or present facts
with disallowed provenance refuse before Writer I/O. Classification and storage
mode come only from policy. There is no anonymous/global/process fallback and a
nil resolver is invalid; callers cannot claim provenance in GroupSpec.
`GeneratedOperationFact` is the only kernel-generated fact mode, is always
ServerDerived, and refuses a resolver value for that slot. Every other required
fact must come from the configured resolver, including a fixed system actor
provided through StaticContext.

Immediately after ResolveAuditContext returns, the kernel samples the original
context's cancellation state. If canceled or deadline-exceeded is observable, that
state wins even when the resolver returned a value or a different error: both are
discarded, cleanup runs, and the public result contains only context.Canceled or
context.DeadlineExceeded with no diagnostic CauseOf payload. Direct, wrapped, and
joined caller-controlled causes are visible to the explicitly trusted resolver
because it receives the original context, but cannot escape its return boundary or
reach any later audit-injected collaborator or public diagnostic door.

Inside audit internals, the original context is read only by the single
ContextResolver call and the
kernel's neutral `crud.SourceBoundExecutorFor`, which can inspect only CRUD's private
executor-binding key. Every later
context-aware Recorder collaborator—including identity, semantic, protection,
token, signing, Writer, retry/lookup, and private evidence paths—receives a fresh
value-free context that delegates only Deadline, Done, and Err and whose Value
always returns nil. `context.Cause` on that wrapper consequently returns only the
normalized Err value (`context.Canceled` or `context.DeadlineExceeded`), never a
caller-supplied cancellation cause or raw error object. Resolver cancellation
errors are not wrapped as collaborator failures and CauseOf reports no resolver
cause. Logical
requester/context facts are passed only through copied typed inputs. Context
value canaries at every seam after ContextResolver prove that a collaborator cannot reread
or disagree with the frozen principal, scope, transaction, locale, trace baggage,
application secret, or secret-bearing cancellation cause.

The callback passed to Within and a mutation callback invoked by the application's
CRUD/security composition are application code, not injected audit collaborators.
They deliberately receive a child of the caller context preserving application
values, cancellation cause, and the private transaction/group bindings required to
perform the unit of work. They are explicit application trust boundaries. Any
value entering audit from those callbacks is still resolved, filtered, copied, and
then passed only through the normalized collaborator contexts above.

A revision stores active catalog coordinates, catalog-set and deployment
fingerprints, durable LogID, operation name and ID, revision ID, optional
keyed IdempotencyToken, observed time, filtered stored context, canonical items,
semantic digest, exact envelope digest, integrity digest, and seal.
Items are one of business/security event, entity change, access evidence, or
lifecycle evidence and carry their own occurred time, outcome, reason, target,
values or changes, correction/dispute links, and leaf digest. One-of validation
is total. Every entity item also binds its previous entity-head digest (or an
explicit absent genesis) into its leaf digest. A revision may group only one
retention class and one evidence consequence. Its
`RevisionAuthorizationSummaryView` is a canonical, duplicate-free union of every
item Resource and Action; every Classification on actor hops, context facts, subjects,
targets, values, and both arms of changes; the admitted scope coordinate; and
every entity-subject or searchable-event-target coordinate, each bound to its
Resource and Action where applicable. The summary is part of the immutable
envelope and is recomputed from stored actors, context, and items before use.
Hold memberships and purge eligibility address exact whole RevisionRefs in
release 1, so lifecycle authorization requires every summary dimension to be a
member of the sealed grant rather than selecting one representative label or any
matching item.

Items are canonicalized and sorted by canonical bytes before dense ordinals are
assigned. Identical items remain duplicate entries. Ordinal therefore expresses
canonical storage order, not caller scheduling or causality. Store-produced
`RecordedAt` and backend position are separate unsigned operational facts and
are never labelled observed time, commit time, evidence order, or an
authenticated historical boundary. Only integrity-bound ObservedAt may select a
public time reconstruction.
At most one entity item for one resource/EntityChainID may occur in a revision;
intersecting current/retained EntityChainKey aliases count as the same subject and
a second is refused during Stage. Applications coalesce repeated same-subject
mutations or use separate revisions, so canonical multiset ordering never erases
entity causality.

### 2.4 Digests and idempotency

```go
type SemanticDigest [32]byte
type EnvelopeDigest [32]byte
type IntegrityDigest [32]byte
type HoldSetDigestInput struct {
	Log      LogID
	Revision RevisionRef
	Holds    []HoldIdentity
}
type RetentionBasisDigestInput struct {
	Catalog    CatalogRef
	ObservedAt time.Time
	Rule       RetentionRuleView
}
type RetentionCohortIDInput struct {
	Log     LogID
	Kind    RetentionCohortKind
	Attempt AttemptChainID
	Members []RevisionRef
}
type CorrectionProposalValueView struct {
	Field          FieldName
	Codec          CodecDescription
	Classification Classification
	Mode           StorageMode
	State          ValueState
	Canonical      []byte
}
type CorrectionProposalDigestInput struct {
	CatalogSet     CatalogSetDigest
	Catalog        CatalogRef
	Declaration    PolicyFingerprint
	Resource       Resource
	Action         Action
	TargetPresent  bool
	Target         Reference
	TargetMode     StorageMode
	TargetClass    Classification
	Values         []CorrectionProposalValueView
	OccurredAt     time.Time
	Outcome        Outcome
	Reason         Reason
	CorrectionOf   ItemRef
	DisputeOf      ItemRef
}
type AttemptSemanticTargetView struct {
	Present        bool
	Classification Classification
	Mode           StorageMode
	Canonical      []byte
}
type AttemptSemanticValueView struct {
	Field          FieldName
	Codec          CodecDescription
	Classification Classification
	Mode           StorageMode
	State          ValueState
	Canonical      []byte
}
type AttemptSemanticDigestInput struct {
	Catalog        CatalogID
	Policy         AttemptPolicyFingerprint
	Replay         AttemptReplayFingerprint
	Resource       Resource
	Operation      OperationName
	OperationID    OperationID
	Chain          AttemptChainID
	ScopePresent   bool
	Scope          EvidenceScopeCommitment
	Owner          AttemptOwnerCommitment
	Transition     AttemptTransitionKind
	Checkpoint     AttemptCheckpointCode
	Reason         Reason
	Target         AttemptSemanticTargetView
	Values         []AttemptSemanticValueView
}

const (
	HMACSHA256Algorithm     = "hmac-sha256"
	HMACSemanticProfileV1   = "frostgrove.audit.semantic.v1"
	HMACIdentityProfileV1   = "frostgrove.audit.identity.v1"
	HMACTokenProfileV1      = "frostgrove.audit.token.v1"
	HMACSignatureProfileV1  = "frostgrove.audit.signature.v1"
)

type SemanticDigestDescription struct {
	Algorithm string
	Profile   string
	KeyID     string
}

type SemanticDigester interface {
	Description() SemanticDigestDescription
	Digest(context.Context, []byte) (SemanticDigest, error)
}

func HMACSemanticDigester(string, []byte) (SemanticDigester, error)
func HoldIdentityCandidate(LogID, HoldIDCommitment) (HoldIdentity, error)
func HoldSetDigestOf(HoldSetDigestInput) (HoldSetDigest, error)
func RetentionBasisDigestOf(RetentionBasisDigestInput) (RetentionBasisDigest, error)
func RetentionCohortIDOf(RetentionCohortIDInput) (RetentionCohortID, error)
func CorrectionProposalDigestOf(context.Context, SemanticDigester, CorrectionProposalDigestInput) (CorrectionProposalDigest, error)
func AttemptChainCandidate(LogID, CatalogID, OperationID) (AttemptChainID, error)
func AttemptReplayFingerprintOf(AttemptPolicyFingerprint, SemanticDigestDescription) (AttemptReplayFingerprint, error)
func CatalogActivationGateDigestOf(CatalogActivationGateDigestInput) (CatalogActivationGateDigest, error)
func AttemptSemanticDigestOf(context.Context, SemanticDigester, AttemptSemanticDigestInput) (SemanticDigest, error)

type IdentityCommitmentDomain uint8
const (
	CommitIdempotency IdentityCommitmentDomain = iota + 1
	CommitEntitySubject
	CommitEvidenceScope
	CommitHoldID
	CommitHoldMatter
	CommitRequester
	CommitAttemptOwner
	CommitAttemptTarget
)

type IdentityCommitmentRequest struct{ value identityCommitmentRequest }
type IdentityCommitmentSet struct{ value identityCommitmentSet }
type IdentityCommitment struct{ value identityCommitment }
type StoredIdentityCommitmentSetData struct {
	Domain      IdentityCommitmentDomain
	Active      IdentityCommitmentDescription
	Commitments []IdentityCommitment
}
type HMACIdentityKey struct {
	KeyID  string
	Key    []byte
	Active bool
}

type IdentityKeyring interface {
	ActiveDescription() IdentityCommitmentDescription
	Descriptions() []IdentityCommitmentDescription
	CommitIdentities(context.Context, IdentityCommitmentRequest) (IdentityCommitmentSet, error)
}

func (r IdentityCommitmentRequest) Domain() IdentityCommitmentDomain
func (r IdentityCommitmentRequest) Plaintext() []byte
func (r IdentityCommitmentRequest) AAD() []byte
func (r IdentityCommitmentRequest) Required() []IdentityCommitmentDescription
func NewIdentityCommitment(IdentityCommitmentDescription, []byte) (IdentityCommitment, error)
func NewIdentityCommitmentSet(IdentityCommitmentRequest, []IdentityCommitment) (IdentityCommitmentSet, error)
func NewStoredIdentityCommitmentSet(StoredIdentityCommitmentSetData) (IdentityCommitmentSet, error)
func (c IdentityCommitment) Description() IdentityCommitmentDescription
func (c IdentityCommitment) Bytes() []byte
func (s IdentityCommitmentSet) Active() IdentityCommitment
func (s IdentityCommitmentSet) Aliases() []IdentityCommitment
func (s IdentityCommitmentSet) Domain() IdentityCommitmentDomain
func HMACIdentityKeyring(...HMACIdentityKey) (IdentityKeyring, error)

type IdempotencyDomainKind uint8
const (
	RecordIdempotencyDomain IdempotencyDomainKind = iota + 1
	AttemptStartIdempotencyDomain
	AttemptCheckpointIdempotencyDomain
	AttemptFinishIdempotencyDomain
)
type IdempotencyDomain struct {
	Kind       IdempotencyDomainKind
	Catalog    CatalogID
	Operation  OperationName
	Attempt    AttemptChainID
	Token      IdempotencyToken
}

type ReconcileKey struct{ value reconcileKey }
func ParseReconcileKey([]byte) (ReconcileKey, error)
func (k ReconcileKey) Bytes() []byte
```

The semantic digester receives one versioned logical replay representation: its
own description, active catalog and lineage digest, deployment fingerprint,
operation name and stable operation ID, filtered logical context, canonical
caller-authored items, codecs, classifications, storage modes, and stable entity
predecessors. A hold uses a dedicated command projection containing its exact
HoldRequestDigest, command, HoldID commitment, target, and matter
presence/commitment. That projection deliberately excludes the state-derived
disposition, expected/result projection, transition head, generated revision ID,
and the enclosing control-result reference. Those exact facts remain in the
signed hold wire and AppendIntentDigest, but cannot make keyed equality depend on
which state happened to exist during a later retry.
The digester sees pre-protection canonical values and is therefore a trusted
privacy collaborator; it must not retain them. Every semantic replay projection
excludes the raw idempotency key, observed/recorded times, backend position,
protected envelopes, integrity digest, and seal. Built-in HMAC-SHA-256 prevents
an exposed digest from becoming a dictionary over low-entropy protected data.

AttemptSemanticDigestOf validates and copies one strict phase-shaped input,
canonically sorts its declared values by field bytes, and invokes the configured
SemanticDigester exactly once under `frostgrove.audit/attempt-semantic/v1`. It
includes stable CatalogID, policy and replay fingerprints, resource, operation and
OperationID, stable chain, logical scope/owner commitments, transition kind,
checkpoint/reason, declared target, and the exact pre-protection phase values.
Active CatalogRef/CatalogSet/deployment, Sequence, checkpoint count, cumulative bytes, Previous/Head/Leaf,
Expected/Result, type-counter states, observed/recorded time, generated revision
identity, protection randomness, envelope/integrity/seal, and transient resume
AccessResultDigest are deliberately excluded from logical replay and covered by
the acyclic leaf/envelope/integrity/AppendIntent inputs instead. S2 publishes an
exhaustive one-field mutation golden, permutation/copy/one-call vectors, and a
replay-after-head-advance vector.

AttemptChainCandidate validates every component and returns SHA-256 over the
versioned, length-framed domain `frostgrove.audit/attempt-chain/v1`, LogID,
CatalogID, and OperationID in that order. It contains no operation name, catalog
generation, policy, scope, owner, or key description. Fixed golden vectors and a
field-boundary mutation table make this durable identity independent of process
and provider configuration; the store's unique CatalogID/OperationID binding
remains the authoritative collision gate. OperationID consequently names one
logical attempted unit across renamed or replaced operation declarations in a
CatalogID lineage, not merely within the currently active OperationName.

AttemptReplayFingerprintOf returns SHA-256 over the versioned, length-framed
domain `frostgrove.audit/attempt-replay-policy/v1`, the nonzero declaration
fingerprint, and the complete valid SemanticDigestDescription. The active
Catalog compilation computes it once and fixed vectors independently mutate the
algorithm, profile, and key ID. It is a compatibility/type-counter identity, not
an authorization grant or a substitute for the record-era manifest.

IdempotencyDomain is a strict union. RecordIdempotencyDomain requires only the
catalog, operation, and token arms. AttemptStartIdempotencyDomain additionally
requires only catalog and token, with Operation and Attempt zero, so the same
attempt-start key cannot silently acquire a new namespace after an operation
rename or policy change. AttemptCheckpointIdempotencyDomain and
AttemptFinishIdempotencyDomain each require catalog, stable AttemptChainID, and
token while Operation is zero. Checkpoint keys therefore cannot collide with the
high-level Run completion derived from the same raw key; Finish,
ResolveUnknown, and Abandon deliberately share the finish namespace so the same
key cannot replay one competing outcome as another. Sequence has no domain arm
and remains in the signed candidate. Operation name and policy are compare-only
semantic claims, never lookup coordinates. One key therefore names one logical
phase transition even after the head advances. A field legal in one
arm is zero in every other arm. Begin, each Checkpoint, standalone Finish,
ResolveUnknown, and Abandon take an explicit nonempty bounded key; the high-level
Run obtains separate start and terminal commitments from its one copied key via
the existing typed domain AAD. A replay lookup is authenticated and validated before
the expected-state CAS, so an exact retry can return its original signed result
after the head advanced, while a different transition at the same domain
conflicts. A transaction-bound terminal returns only reconciliation state and is
never retried independently.

The CommitIdempotency AAD is the versioned pre-token encoding of Kind, CatalogID,
and only the arm's legal OperationName or AttemptChainID; it never contains the
raw key, output Token, policy, sequence, or semantic digest. Domain.Token is the
active commitment selected from the returned IdentityCommitmentSet. Lookup first
matches those non-token locator coordinates and then requires exactly one
intersection between the request's active/retained idempotency aliases and a
stored row's aliases. The stored Domain.Token may be a retained alias and need
not equal the request's active token. No match is absent only when alias
derivation and retained-catalog verification both succeeded; multiple matches are
corruption. A successful retained-alias match atomically binds the new active
alias to the same immutable row. Alias, key, catalog, or verification failure is
never downgraded to AbsentNow and can never authorize an Insert.

HMACSemanticDigester and HMACIdentityKeyring synthesize their fixed algorithm and
profile descriptions from the constants above; caller input supplies only a
bounded nonempty KeyID and key bytes, so an HMAC helper cannot advertise another
primitive or protocol. Every key is at least 32 bytes and copied at construction.
An identity keyring has unique KeyIDs and exactly one active key. Its commitment
is exactly 32 bytes over a versioned, domain-separated, length-framed encoding of
description, IdentityCommitmentDomain, AAD, and plaintext.

SemanticDigester is an opaque trusted boundary. A custom implementation is
required to be a stable keyed PRF for every domain that can contain private or
low-entropy coordinates, deterministic for its declared description, terminating,
work-bounded for its deployment, concurrency-safe, and non-retaining. Construction
fixtures, repeated calls, raw-SHA rejection canaries, and post-construction
state-switch canaries detect concrete violations but are never described as proof
of arbitrary future behavior. Every returned digest has exact length before any
later use. The audit-owned HMAC helper is the fully specified production default.

Raw IdempotencyKey is bounded input and never crosses Writer, Observer, evidence,
error, or rendering doors. Before other custom collaborators, IdentityKeyring
produces domain-separated fixed-size commitments for the exact active and
retained manifest descriptions; the kernel converts them to non-interchangeable
IdempotencyToken, EntityChainKey, HoldIDCommitment, or HoldMatterCommitment types.
CommitHoldID receives the fixed-width random HoldID under a distinct domain;
CommitHoldMatter receives its bounded logical matter under another. Inputs and AAD
are copied, keys are at least 32 bytes, outputs render nothing, and the built-in
HMAC keyring uses constant-time comparison. The active description must match the
active manifest and every retained description needed for an alias must be
present. Rotation therefore finds an old idempotency row, entity head, or hold
matter through its retained alias instead of silently creating another identity.
The store atomically rejects alias sets resolving to more than one existing
object and binds the active alias to the one stable object on success.
NewIdentityCommitmentSet remains hidden-origin-bound to one live
IdentityCommitmentRequest and is the only legal keyring response. The separate
NewStoredIdentityCommitmentSet reconstructs only a persisted canonical set from
its explicit domain, active description, and ordered described commitments; it
contains no plaintext or AAD and cannot satisfy a live request-origin check. It is
accepted only inside validating persisted row/header/query-result constructors,
which lets external stores rebuild authorization summaries after restart without
reimplementing commitments or forging a provider result.
For every entity item, CommitEvidenceScope first commits either explicit absence
or the complete canonical ScopedReference pair. The resulting typed
EvidenceScopeCommitment enters the AAD of every active/retained
CommitEntitySubject request. It is repeated in reservations, alias bindings,
EntityHead requests, and genesis derivation, so equal local subjects under absent,
tenant, and region scopes cannot share a head. Neither scope component nor their
ambiguous concatenation crosses the store boundary.
CommitRequester covers the complete canonical filtered ContextView: ContextPolicy
identity, every admitted actor kind/reference/provenance, and presence, value, and
provenance of scope, service, deployment, client, operation, correlation,
causation, trace, and source. Its active output becomes RequesterCommitment and
the full active/retained IdentityCommitmentSet remains the rotation alias set.
Both values enter access/control/hold/denial digest inputs and both cursor claims.
Equal subject aliases with different provenance or any admitted context coordinate
therefore differ. Active and retained aliases let a fresh authorization request
and denial limiter recognize one principal while querying records across an
admitted identity-key rotation without revealing those logical facts. An exact
CatalogSet change intentionally invalidates an existing cursor. ReconcileKey is
an authenticated, bounded, restart-safe encoding of durable BackingID, LogID,
lookup coordinates, and their identity-description generation. Parse checks
the outer format and bound; Recorder.Lookup authenticates it with the retained
IdentityKeyring before Writer I/O. Its bytes and rendering disclose no raw
idempotency key, requester, subject, or matter.

After logical items are sorted and dense ordinals assigned, protection receives
kernel-created AAD binding format, LogID, active catalog and lineage/deployment
fingerprints, revision identity and IdempotencyToken, semantic digest, item ordinal,
kind/resource/action, field or context-fact identity, codec,
classification, and storage mode. The exact stored canonical representation
is built without self-reference. Each LeafDigest is first computed from exactly
LeafDigestInput: log/catalog/revision coordinates and every ItemLeafPreimageView
member, including Previous, but never Leaf. The finalized item then enters
EnvelopeDigestInput with every immutable header preimage, actor, context, item,
typed subject/identity commitment, protection algorithm/key/nonce/ciphertext,
token, redaction state, and coordinate. RevisionHeaderPreimageView deliberately
has no Envelope, Integrity, or Seal, and AppendIntent plus unsigned store metadata
are not envelope fields. IntegrityDigest is then computed only from its explicit
RevisionRef, idempotency domain, ObservedAt, SemanticDigest, and EnvelopeDigest.
A seal signs only IntegrityDigest. Finally AppendIntentDigest binds the finalized
RevisionWireView and conditional arm; it is stored beside, never inside, that
revision. The mandatory order is item leaves, envelope, integrity, seal, then
append intent. RecordedAt and backend position remain honest unsigned metadata.
Raw subject, scope, actor, client, idempotency key, matter, or value bytes never
enter stored AAD. The SemanticDigest and typed domain-separated commitments bind
their logical identity without creating a circular subject envelope or plaintext
side channel.

HoldIdentityCandidate is the fixed SHA-256 conversion of the exact ASCII domain
`frostgrove.audit/hold-identity/v1` plus length-framed LogID and the active
pseudorandom HoldIDCommitment; zero inputs or the impossible zero output refuse.
A first placement proposes it; active/retained alias lookup reuses the already
persisted HoldIdentity after key or catalog rotation.
HoldSetDigestOf uses fixed SHA-256 domain `frostgrove.audit/hold-set/v1` over
length-framed LogID, target RevisionRef, count, and the canonically sorted unique
active HoldIdentity values. It copies and sorts caller input, rejects zero or
duplicate identities, and S1 publishes a nonzero empty-set golden for one fixed
valid LogID and RevisionRef. Raw HoldID, commitment generation, matter, transition
order, and current key are deliberately absent. The input
identities are pseudorandom stable store identities, so the public set digest
does not become a low-entropy HoldID oracle and does not change on key rotation.
Kernel, memory, PostgreSQL, and conformance use this one helper; no adapter chooses
another fold or hashes mutable transition rows.

CorrectionProposalDigestOf validates and copies the complete acyclic
CorrectionProposalDigestInput, canonically sorts its value multiset by declared
field bytes, rejects duplicate/unknown fields and every non-manual or already-
linked draft shape, and invokes the supplied SemanticDigester exactly once under
`frostgrove.audit/correction-proposal/v1`. CatalogSet, exact CatalogRef,
declaration fingerprint, Resource, Action, target presence/value/mode/class,
each field's codec/class/mode/state/canonical bytes, normalized OccurredAt,
Outcome, Reason, and all zero link arms are individually length-framed. The
resulting nonzero digest enters the value-free CorrectionProposalView; that view
is never used as its own preimage. S2 publishes a golden plus one-field-at-a-time,
permutation, mutable-input, wrong-domain, raw-SHA, and one-call vectors.

There are two non-overlapping replay doors. Repeating the same
`(CatalogID, RevisionID)` with a byte-identical AppendIntentDigest and immutable
candidate returns the original complete receipt; the same revision identity with
any different intent or content always conflicts. A hold append is one signed CAS
candidate derived from an authenticated prior projection, not a same-identity
bundle. Independently, an explicit idempotency domain plus equal stable
SemanticDigest returns the original revision ID, operation ID, observed time,
envelopes, envelope/integrity digests, seal, hold disposition/projection, and
receipt even when a fresh Protector or changed current hold state would produce a
different candidate. The store ignores that fresh candidate only after atomically
matching the complete keyed domain, identity aliases, catalog/deployment
coordinates, and logical digest. Different semantics conflict.
Reusing a key after catalog, semantic-digester profile/key, context policy, or
deployment fingerprint change therefore conflicts. An unkeyed RetryToken is safe
because it freezes the first revision identity and byte-exact stored content;
Retry reuses it without invoking resolver, digester, protector, tokenizer,
signer, clock, or IDs. Ordinary no-key Record calls still generate independent
revisions. Deterministic encryption and an unkeyed plaintext hash are not
accepted substitutes.

The preceding catalog/deployment equality is the RecordIdempotencyDomain rule.
AttemptStartIdempotencyDomain, AttemptCheckpointIdempotencyDomain, and
AttemptFinishIdempotencyDomain instead
match stable CatalogID plus respectively the global start token or exact
chain/token locator, then compare OperationName, AttemptPolicyFingerprint,
AttemptReplayFingerprint,
identity conditions, and AttemptSemanticDigest across a compatible active
generation. The request supplies the bounded retained CatalogRefs; the kernel
requires the original stored CatalogRef to be present and authentic, and the
current active manifest to contain the same policy and replay fingerprints before the store may
return Found. Active CatalogRef, CatalogSet/deployment fingerprint, and ephemeral
context may differ and remain signed in their respective original revisions.
Identity-key rotation works through explicitly retained commitment aliases. A
compatible AttemptReplayFingerprint combines the declaration fingerprint with
the exact SemanticDigestDescription; changing the semantic provider description
is an incompatible policy change and an old OperationID conflicts without
executing work. There is no fictitious semantic-digest alias mechanism. Missing history or
a missing retained identity alias refuses rather than creates a second attempt.

### 2.5 Split store seams and transaction truth

```go
type Support uint8
const (
	SupportUnstated Support = iota
	SupportUnsupported
	SupportSupported
)
type CapabilitySpec struct {
	Transactions     Support
	CrossSystemAtomic Support
	Persistence      Support
	Idempotency      Support
	Reconciliation   Support
	StableSearch     Support
	ExactInspection  Support
	AttemptLifecycle Support
	Holds            Support
	PurgePlanning    Support
}
type Capabilities struct{ value capabilities }
func (c Capabilities) View() CapabilitySpec

type LimitSpec struct {
	RevisionBytes      uint64
	AppendRequestBytes uint64
	PageRevisions      uint32
	PageBytes          uint64
	ExactTargets       uint32
	ExactBytes         uint64
	PositionBytes      uint32
	InventoryCandidates uint32
	InventoryCohorts uint32
	InventoryBytes     uint64
	SnapshotBytes      uint32
	SearchCohortRevisions uint32
	SearchCohortBytes  uint64
	AttemptTransitions uint16
	AttemptStateBytes  uint64
	AttemptOpenLifetime time.Duration
}
type Limits struct{ value limits }
func (l Limits) View() LimitSpec

type Backing struct{ value backing }
type Authority struct{ value authority }
type Execution interface {
	Authority() Authority
	EntityHead(context.Context, EntityHeadRequest) (EntityHeadResult, error)
	Append(context.Context, AppendRequest) (AppendResult, error)
}
type StorePosition struct{ value storePosition }
type StoredHeader struct{ value storedHeader }
type StoredRevision struct{ value storedRevision }
type StoredPage struct{ value storedPage }
type EntityHeadResult struct{ value entityHeadResult }
type LookupResult struct{ value lookupResult }
type IdempotencyLookupResult struct{ value idempotencyLookupResult }
type InventoryResult struct{ value inventoryResult }
type HoldStateResult struct{ value holdStateResult }
type StorePurgePlan struct{ value storePurgePlan }
type ExactResult struct{ value exactResult }
type EntityChainHead struct{ value entityChainHead }
type CatalogMutationLog struct{ value catalogMutationLog }

type StoreInfo interface {
	Capabilities() Capabilities
	Limits() Limits
	Backing() Backing
	BackingID() BackingID
	LogID() LogID
	Catalogs() StoreCatalogState
}

type StoreCatalogState struct{ value storeCatalogState }
func NewCapabilities(CapabilitySpec) (Capabilities, error)
func NewLimits(LimitSpec) (Limits, error)
func NewEmptyStoreCatalogState() StoreCatalogState
func NewInactiveStoreCatalogState(CatalogSetDigest) (StoreCatalogState, error)
func NewStoreCatalogState(CatalogRef, CatalogSetDigest) (StoreCatalogState, error)
func (s StoreCatalogState) HasActive() bool
func (s StoreCatalogState) Active() CatalogRef
func (s StoreCatalogState) SetDigest() CatalogSetDigest

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
type AttemptTypeSelectorView struct {
	Catalog   CatalogID
	Operation OperationName
	Policy    AttemptPolicyFingerprint
	Replay    AttemptReplayFingerprint
}
type AttemptStateQuery struct{ value attemptStateQuery }
type AttemptStateQueryView struct {
	Log          LogID
	Catalogs     []CatalogRef
	Catalog      CatalogID
	OperationID  OperationID
	Chain        AttemptChainID
	MaxTransitions uint16
	MaxBytes     uint64
}
type AttemptStateResultData struct {
	State       AttemptProjectionStateView
	Transitions []ItemRef
}
type AttemptStateResult struct{ value attemptStateResult }
func (q AttemptStateQuery) View() AttemptStateQueryView
func NewAttemptStateResult(AttemptStateQuery, AttemptStateResultData) (AttemptStateResult, error)
func (r AttemptStateResult) State() AttemptProjectionStateView
func (r AttemptStateResult) Transitions() []ItemRef

type AttemptTypeStateQuery struct{ value attemptTypeStateQuery }
type AttemptTypeStateQueryView struct {
	Log      LogID
	Catalogs []CatalogRef
	Type     AttemptTypeSelectorView
}
type AttemptTypeStateResult struct{ value attemptTypeStateResult }
func (q AttemptTypeStateQuery) View() AttemptTypeStateQueryView
func NewAttemptTypeStateResult(AttemptTypeStateQuery, AttemptTypeProjectionStateView) (AttemptTypeStateResult, error)
func (r AttemptTypeStateResult) State() AttemptTypeProjectionStateView

type AttemptTypeActivationGateDigestInput struct {
	Catalog       CatalogID
	Operation     OperationName
	PriorPolicy   AttemptPolicyFingerprint
	PriorReplay   AttemptReplayFingerprint
	NextPresent   bool
	NextPolicy    AttemptPolicyFingerprint
	NextReplay    AttemptReplayFingerprint
	Expected      AttemptTypeProjectionStateView
	Result        AttemptTypeProjectionNextView
	HeadIntegrity IntegrityDigest
}
type CatalogActivationGateDigestInput struct {
	Backing  BackingID
	Log      LogID
	Expected CatalogRef
	Next     CatalogRef
	Gates    []AttemptTypeActivationGateDigestInput
}

type AttemptTypeActivationGateView struct {
	Catalog       CatalogID
	Operation     OperationName
	PriorPolicy   AttemptPolicyFingerprint
	PriorReplay   AttemptReplayFingerprint
	NextPresent   bool
	NextPolicy    AttemptPolicyFingerprint
	NextReplay    AttemptReplayFingerprint
	Expected      AttemptTypeProjectionStateView
	Result        AttemptTypeProjectionStateView
	HeadIntegrity IntegrityDigest
}
type CatalogActivationProofView struct {
	Backing  BackingID
	Log      LogID
	Expected CatalogRef
	Next     CatalogRef
	Gates    []AttemptTypeActivationGateView
	Bytes    uint64
	Digest   CatalogActivationGateDigest
}
type CatalogActivationProof struct{ value catalogActivationProof }
type CatalogActivationConfig struct {
	Origin   StoreInfo
	Catalogs *CatalogSet
	Mutations CatalogMutationLogReader
	Types    AttemptTypeState
	Exact    ExactLog
	Verifier Verifier
}
func PrepareCatalogActivation(context.Context, CatalogActivationConfig, CatalogRef, Manifest) (CatalogActivationProof, error)
func (p CatalogActivationProof) View() CatalogActivationProofView

type CatalogMutationKind uint8
const (
	CatalogInstallMutation CatalogMutationKind = iota + 1
	CatalogActivateMutation
)
type CatalogMutationView struct {
	Kind     CatalogMutationKind
	Catalog  CatalogRef
	Expected CatalogRef
	Active   CatalogRef
	Change   CatalogChangeRef
	Gate     CatalogActivationGateDigest
	Gates    []AttemptTypeActivationGateView
	ProofBytes uint64
}
func CatalogInstalled(CatalogRef, CatalogChangeRef) CatalogMutationView
func CatalogActivated(CatalogRef, CatalogRef, CatalogChangeRef, CatalogActivationProof) CatalogMutationView
func NewCatalogMutationLog(BackingID, LogID, []CatalogMutationView) (CatalogMutationLog, error)
func (l CatalogMutationLog) BackingID() BackingID
func (l CatalogMutationLog) LogID() LogID
func (l CatalogMutationLog) Mutations() []CatalogMutationView
func VerifyCatalogMutations(StoreInfo, []CatalogMutationView, CatalogMutationLog) error

type CatalogMutationLogReader interface {
	StoreInfo
	CatalogMutations(context.Context) (CatalogMutationLog, error)
}

type Writer interface {
	StoreInfo
	TransactionSource() any
	BindTransaction(crud.Executor) (Execution, error)
	LookupIdempotency(context.Context, IdempotencyLookupRequest) (IdempotencyLookupResult, error)
	Append(context.Context, AppendRequest) (AppendResult, error)
	Lookup(context.Context, LookupRequest) (LookupResult, error)
}

type Log interface {
	StoreInfo
	Search(context.Context, StoreQuery) (StoredPage, error)
}

type ExactLog interface {
	StoreInfo
	Inspect(context.Context, ExactQuery) (ExactResult, error)
	EntityChainHead(context.Context, EntityChainHeadQuery) (EntityChainHead, error)
}

type AttemptLog interface {
	StoreInfo
	AttemptState(context.Context, AttemptStateQuery) (AttemptStateResult, error)
}

type AttemptTypeState interface {
	StoreInfo
	AttemptTypeState(context.Context, AttemptTypeStateQuery) (AttemptTypeStateResult, error)
}

type LifecycleLog interface {
	StoreInfo
	Inventory(context.Context, InventoryQuery) (InventoryResult, error)
	Verification(context.Context, VerificationQuery) (StoredPage, error)
	HoldState(context.Context, HoldStateQuery) (HoldStateResult, error)
	PlanPurge(context.Context, PurgeQuery) (StorePurgePlan, error)
}

type CatalogAdmin interface {
	CatalogMutationLogReader
	InstallCatalog(context.Context, Manifest, CatalogChangeRef) error
	ActivateCatalog(context.Context, CatalogRef, CatalogRef, CatalogChangeRef, CatalogActivationProof) error
	VerifyCatalogs(context.Context, []Manifest) error
}

type Closer interface {
	Close() error
}

type Store interface {
	Writer
	Log
	ExactLog
	AttemptLog
	AttemptTypeState
	LifecycleLog
	CatalogMutationLogReader
	Closer
}

type DeploymentStore interface {
	CatalogAdmin
	Closer
}

type AppendRequest struct{ value appendRequest }
type AppendRequestView struct {
	Revision      Revision
	Hold          HoldConditionalAppendView
	Attempt       AttemptConditionalAppendView
	Intent        AppendIntentDigest
	Idempotency   IdentityCommitmentSet
	Entities      []EntityAliasBinding
	Attempts      []AttemptIdentityAliasBinding
	HoldIDs       []HoldIDAliasBinding
	HoldMatters   []HoldMatterAliasBinding
}
type HoldCommandKind uint8
const (
	HoldPlaceCommand HoldCommandKind = iota + 1
	HoldReleaseCommand
)
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
type HoldAppendCandidateView struct {
	Disposition HoldTransitionDisposition
	Result      HoldProjectionStateView
	Revision    Revision
}
type AttemptConditionalAppendView struct {
	Chain             AttemptChainID
	Expected          AttemptProjectionStateView
	TypeExpected      AttemptTypeProjectionStateView
	ResumeAuthorization AccessResultDigest
	Candidate         AttemptAppendCandidateView
}
type AttemptAppendCandidateView struct {
	Result     AttemptProjectionStateView
	TypeResult AttemptTypeProjectionStateView
	Revision   Revision
}
type EntityAliasBinding struct{ value entityAliasBinding }
type AttemptIdentityAliasBinding struct{ value attemptIdentityAliasBinding }
type HoldIDAliasBinding struct{ value holdIDAliasBinding }
type HoldMatterAliasBinding struct{ value holdMatterAliasBinding }
func (r AppendRequest) View() AppendRequestView
func (b EntityAliasBinding) Resource() Resource
func (b EntityAliasBinding) ChainID() EntityChainID
func (b EntityAliasBinding) Scope() (EvidenceScopeCommitment, bool)
func (b EntityAliasBinding) Commitments() IdentityCommitmentSet
func (b AttemptIdentityAliasBinding) Operation() OperationName
func (b AttemptIdentityAliasBinding) Policy() AttemptPolicyFingerprint
func (b AttemptIdentityAliasBinding) Replay() AttemptReplayFingerprint
func (b AttemptIdentityAliasBinding) OperationID() OperationID
func (b AttemptIdentityAliasBinding) Chain() AttemptChainID
func (b AttemptIdentityAliasBinding) TargetPresent() bool
func (b AttemptIdentityAliasBinding) TargetCommitments() IdentityCommitmentSet
func (b AttemptIdentityAliasBinding) ScopePresent() bool
func (b AttemptIdentityAliasBinding) ScopeCommitments() IdentityCommitmentSet
func (b AttemptIdentityAliasBinding) OwnerCommitments() IdentityCommitmentSet
func (b HoldIDAliasBinding) Hold() HoldID
func (b HoldIDAliasBinding) Identity() HoldIdentity
func (b HoldIDAliasBinding) Commitments() IdentityCommitmentSet
func (b HoldMatterAliasBinding) Hold() HoldID
func (b HoldMatterAliasBinding) Commitments() IdentityCommitmentSet

type EntityHeadRequest struct{ value entityHeadRequest }
type EntityHeadRequestView struct {
	Resource     Resource
	Candidate    EntityChainID
	ScopePresent bool
	Scope        EvidenceScopeCommitment
	Commitments  IdentityCommitmentSet
}
type EntityHeadState uint8
const (
	EntityGenesis EntityHeadState = iota + 1
	EntityExisting
	EntityTerminal
)
type EntityHeadResultData struct {
	State      EntityHeadState
	Chain      EntityChainID
	Previous   LeafDigest
	Authority  Authority
}
func (r EntityHeadRequest) View() EntityHeadRequestView
func (r EntityHeadResult) State() EntityHeadState
func (r EntityHeadResult) ChainID() EntityChainID
func (r EntityHeadResult) Previous() LeafDigest
func (r EntityHeadResult) Authority() Authority

type LookupRequest struct{ value lookupRequest }
type LookupRequestView struct {
	Key             ReconcileKey
	Revision        RevisionID
	Idempotency     IdentityCommitmentSet
	Catalog         CatalogID
	Operation       OperationName
}
type LookupResultData struct {
	State      LookupState
	Stored     StoredHeader
	Visibility Settlement
	Authority  Authority
}
func (r LookupRequest) View() LookupRequestView

type IdempotencyLookupRequest struct{ value idempotencyLookupRequest }
type IdempotencyLookupRequestView struct {
	Catalog      CatalogRef
	Catalogs     []CatalogRef
	CatalogSet   CatalogSetDigest
	Deployment   DeploymentFingerprint
	Domain       IdempotencyDomain
	Idempotency  IdentityCommitmentSet
	Semantic     SemanticDigest
}
type IdempotencyLookupResultData struct {
	State  LookupState
	Stored StoredHeader
}
func (r IdempotencyLookupRequest) View() IdempotencyLookupRequestView
func NewIdempotencyLookupResult(IdempotencyLookupRequest, IdempotencyLookupResultData) (IdempotencyLookupResult, error)
func (r IdempotencyLookupResult) State() LookupState
func (r IdempotencyLookupResult) Stored() (StoredHeader, bool)

type AppendResult struct{ value appendResult }
func NewStoredHeader(StoredHeaderData) (StoredHeader, error)
func NewAppendResult(AppendRequest, StoredHeader, AppendDisposition, Authority) (AppendResult, error)
func NewHoldAppendResult(AppendRequest, StoredHeader, AppendDisposition, Authority) (AppendResult, error)
func NewAttemptAppendResult(AppendRequest, StoredHeader, AppendDisposition, Authority) (AppendResult, error)
func (r AppendResult) Stored() StoredHeader
func (r AppendResult) Disposition() AppendDisposition
func (r AppendResult) Authority() Authority
func (r AppendResult) HoldDisposition() (HoldTransitionDisposition, bool)
func (r AppendResult) HoldProjection() (HoldProjectionStateView, bool)
func (r AppendResult) AttemptTransition() (AttemptTransitionWireView, bool)
func (r AppendResult) AttemptProjection() (AttemptProjectionStateView, bool)

func BackingFor(any) (Backing, error)
func AuthorityFor(source, transaction any) (Authority, error)
func (a Authority) Valid() bool
func SameAuthority(Authority, Authority) bool
func SameBacking(Backing, Backing) bool
```

Every method is exact-outer. Recorder receives Writer and requires its declared
active CatalogRef and CatalogSet digest to match Config before resolving context
or writing; it rechecks that atomic snapshot on every operation and Append
enforces the same active ref atomically. History receives that Recorder plus
explicit Log and ExactLog facets, Revealer, and Verifier. Control receives the
Recorder plus explicit ExactLog and LifecycleLog. Attempts receives the Recorder,
AttemptLog, and already-authorized History service; restart resume uses History's
ExactLog and Verifier paths rather than discovering another backend. Constructors require all
supplied store seams to report the
same process-local Backing, durable BackingID, durable LogID, active CatalogRef,
and CatalogSet digest as the Recorder;
none discovers another seam by assertion or `Next`. The composition root owns an
explicit Closer. Store calls use exported but sealed permit/request values, copy
inputs/outputs, and expose no authorization-bearing constructor.

NewEmptyStoreCatalogState is the one canonical no-active/no-installed state and is
legal only from a DeploymentStore before genesis. Its Active and SetDigest values
are zero and HasActive is false. NewInactiveStoreCatalogState represents a
nonempty installed set before its first activation and also has HasActive false.
Operational Store, Recorder, History, Control, and Attempts constructors require HasActive
with a nonzero self-consistent set. Once any catalog is installed, neither handle
may report the empty state again.

Catalog installation and activation are explicit deployment operations, never
constructor or runtime-request work. Audit cannot bootstrap trustworthy evidence
about the operation that creates its own first manifest, so each mutating call
requires a nonzero bounded CatalogChangeRef issued by an independently authorized
deployment/change ledger. CatalogInstalled has only Catalog and Change populated;
CatalogActivated has Expected, Active, Change, the exact activation-proof Digest,
canonical sorted Gates, and original checked ProofBytes populated; Catalog is zero. Constructors and
readback reject zero, mixed, malformed, duplicate, reordered, over-count, or
over-byte records. Every Gate's expected/result key, old/new presence and
fingerprint, anchor, signed-head integrity, and aggregate proof bytes are
recomputed before the mutation can be trusted. Install and Activate first canonicalize the prospective record
and refuse atomically before persistence when appending it would make the complete
log exceed MaxCatalogMutations or MaxCatalogMutationBytes; every successful state
therefore remains completely queryable. The application keeps CatalogAdmin out of the serving
dependency graph through a distinct concrete deployment handle; a different
concrete runtime handle implements Store and its dynamic method set contains no
CatalogAdmin method. Interface narrowing of one larger concrete value is not
conforming. The backend persists the exact ledger/change reference beside each
append-only manifest or activation row and CatalogMutations returns the complete
bounded immutable sequence after reopen. CatalogMutationLog is origin-bound to its
nonzero durable BackingID and LogID; VerifyCatalogMutations requires the same live
StoreInfo and compares origin, kind, order, and every record field byte-for-byte.
Audit treats the external ledger as a declared trust
boundary and makes no claim that a bare reference authenticates it. Install is
append-only; an exact same-manifest/same-change replay succeeds without a second
record, while any same-ref disagreement conflicts. Activate compares the exact expected
active CatalogRef, byte-compares the origin-bound proof and every locked gate, and
advances to its direct child atomically; concurrent or stale activation conflicts.
An exact previously persisted expected/active/change/proof tuple is a no-write
success even after later children activated, returning the original mutation
identity rather than comparing against newly reset projections. A replay with the
same three public coordinates but a different proof conflicts. This makes the
complete genesis-to-active deployment plan safely resumable after a lost answer.
PrepareCatalogActivation first reads and verifies CatalogMutations. When that
exact activation already exists it reconstructs the original sealed proof from
the stored Gate/Gates and returns it without reading current attempt projections;
otherwise Expected must still be active and preparation reads the prospective
old-policy counters. It can therefore never manufacture a different post-reset
gate for an already completed step.
Append carries the active CatalogRef and the store
rejects it atomically if that ref is no longer active, so a stale process cannot
continue writing after rotation. VerifyCatalogs requires the complete canonical
manifest sequence, rejects missing/extra/reordered bytes, requires its final ref to
be active, and changes no state. Deployment conformance installs and
activates every direct child in order, closes/reopens between every mutation, then checks
manifest and mutation readback. A backend that acknowledges a mutation but omits,
alters, or fabricates its record fails. PostgreSQL stores append-only manifests
plus one active-head projection and mutation log; auditmemory implements the same
contract. No migration, installation, or activation runs in a runtime constructor.

EntityChainHeadQuery is a grant-narrowed exact-subject query with the same retained
catalog and protected scope/subject coordinates as history. Coordinates contain
exactly one QuerySubject and at most one QueryScope, all with
QueryCoordinateExact; AllDeclared, target/client kinds, empty subject, and a broad
scan have no spelling. Its origin-bound result is either missing or one nonzero
chain, complete RevisionRef, and LeafDigest under the hard exact-read byte ceiling.
For AtObservedTime, Missing is unauthenticated absence and returns
TemporalAmbiguity with no projection or evidence disclosure. For Present, History
exact-inspects the referenced revision, verifies its record-era signature and matching
entity item, chain, leaf, and coordinates, and then requires both the predecessor
walk and Search pages to reach that head. An omitted Search suffix relative to the
asserted head therefore fails. This is a store-present authenticated content anchor,
not an external freshness oracle: a hostile store can replay both an older complete
history and its still-valid older head, or erase both head and history, unless the
application independently retains and compares a newer anchor. No local API claims
to detect that rollback.

`Authority` is an opaque non-serializable pair of comparable source and
transaction identity. Store adapters mint it through `AuthorityFor`; callers
cannot inspect its parts, and rendering reveals nothing. Recorder uses neutral
`crud.SourceBoundExecutorFor`, which can read only CRUD's private binding key.
No binding lets Recorder open an owned transaction; poison, mismatch, or unsafe
fallback refuses. Once an owned or exact joined root executor exists, Recorder
passes only that executor to `Writer.BindTransaction`. The Writer validates it
for its exact TransactionSource and returns a non-nil store-owned
Execution whose private concrete value captures the executor. It receives no
caller context and begins, commits, and rolls back nothing. The root Writer keeps
no reference; only the active Recorder frame retains the Execution. An unsafe
source-less fallback refuses rather than becoming proof.

Standalone event Append calls use Writer. EntityHead exists only on the exact
transaction-bound Execution retained by an active supervised mutation frame;
transaction-bound Append uses that same Execution. Request views contain no
executor or Execution. The interface exposes only Authority, EntityHead, and
Append, so no application API or copied request can extract the executor. No
authority-to-executor registry, lease cleanup callback, caller-context lookup, or
global state exists. Dropping a frame after abort, panic, codec failure, or signer
failure drops its only Execution reference and leaves nothing to unregister.
Execution.EntityHead/Append must use the captured caller transaction, and every
successful result Authority must equal Execution.Authority and remains
`InCallerTransaction`. A typed nil, invalid/changing/foreign Authority, cross-frame
Execution, or result-authority substitution is refused. The kernel never compares
possibly non-comparable concrete Execution values; it origin-binds the interface
to the exact Writer, Recorder, and frame that received it.
`TransactionSource` is a comparable, non-rendered datasource/log identity.
Auditcrud requires supported cross-subsystem atomicity and exact equality with
`crud.KeyOf(source)` before mutation I/O.

`Backing` is process-local exact facet identity minted from a comparable private
value and is never rendered or serialized. `BackingID` is a nonzero persisted
128-bit storage-domain identity returned by StoreInfo and stable across new pool
and store values after restart. Public cursors, control cursors, fences, and
reconciliation coordinates bind BackingID plus LogID, never Backing. A deliberate
fork must be activated with a new BackingID and token keys before it serves; a
bit-for-bit concurrently served clone is operational split brain and cannot be
distinguished by local cryptography. Constructors and every operation compare
all facets' local Backing and durable BackingID independently.

Capabilities use `Unstated`, `Unsupported`, and `Supported` for transactions,
cross-subsystem atomicity, persistence, idempotency, reconciliation, stable
search, exact inspection, holds, and purge planning. Every capability required by
a constructed service or operation must equal SupportSupported; both
SupportUnstated and SupportUnsupported refuse binding before I/O.
Kernel limits never exceed store limits. Auditmemory supports atomic audit
append, rollback within its own transaction fixture, idempotency, reconciliation,
stable search, exact inspection, holds, and purge planning, but explicitly does not support persistence or cross-system
atomicity. Its fixture exposes a neutral `crud.Executor`/source binding so Recorder
can resolve it without a store-private context key. Auditpg supports database/sql
sharing; native pgx sharing is refused.

The store SPI is usable from an external package without reflection, unsafe,
concrete-type assertions, or private-field access. Requests remain kernel-minted,
but each has a copied read-only view; store answers are built through validating
constructors:

```go
type Revision struct{ value revision }
type RevisionWireView struct {
	Header  RevisionHeaderView
	Actors  []StoredActorView
	Context []StoredContextFactView
	Items   []ItemWireView
}
type RevisionHeaderView struct {
	Format          uint16
	Log             LogID
	Catalog         CatalogRef
	CatalogSet      CatalogSetDigest
	Deployment      DeploymentFingerprint
	Operation       OperationName
	OperationID     OperationID
	RevisionID      RevisionID
	HasIdempotency  bool
	Idempotency     IdempotencyToken
	ObservedAt      time.Time
	Retention       RetentionClass
	Consequence     Consequence
	RetentionBasis  RetentionBasisDigest
	Authorization   RevisionAuthorizationSummaryView
	Semantic        SemanticDigest
	Envelope        EnvelopeDigest
	Integrity       IntegrityDigest
	Seal            Seal
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
	Ordinal       uint16
	Kind          ItemKind
	Resource      Resource
	Action        Action
	EntityState   EntityStateKind
	Chain         EntityChainID
	Subject       StoredValueView
	Target        StoredValueView
	OccurredAt    time.Time
	Outcome       Outcome
	Reason        Reason
	AccessRequest AccessRequestDigest
	AccessGrant   AccessGrantDigest
	AccessResult  AccessResultDigest
	ControlRequest ControlRequestDigest
	ControlGrant   ControlGrantDigest
	ControlResult  ControlResultDigest
	DenialRequest DenialRequestDigest
	Previous      LeafDigest
	Leaf          LeafDigest
	Values        []StoredValueView
	Changes       []StoredChangeView
	CorrectionOf  ItemRef
	DisputeOf     ItemRef
	Attempt       AttemptTransitionWireView
	Hold           HoldTransitionWireView
}
type AttemptTransitionWireView struct {
	Chain           AttemptChainID
	Policy          AttemptPolicyFingerprint
	Replay          AttemptReplayFingerprint
	Operation       OperationName
	OperationID     OperationID
	ScopePresent    bool
	Scope           EvidenceScopeCommitment
	Start           ItemRef
	Sequence        uint16
	CheckpointCount uint16
	Kind            AttemptTransitionKind
	Checkpoint      AttemptCheckpointCode
	ExpiresAt       time.Time
	Expected        AttemptProjectionStateView
	Result          AttemptProjectionNextView
	TypeExpected    AttemptTypeProjectionStateView
	TypeResult      AttemptTypeProjectionNextView
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
type ItemLeafPreimageView struct {
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
	Values         []StoredValueView
	Changes        []StoredChangeView
	CorrectionOf   ItemRef
	DisputeOf      ItemRef
	Attempt        AttemptTransitionWireView
	Hold           HoldTransitionWireView
}
type LeafDigestInput struct {
	Log      LogID
	Catalog  CatalogRef
	Revision RevisionID
	Item     ItemLeafPreimageView
}
type RevisionHeaderPreimageView struct {
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
}
type EnvelopeDigestInput struct {
	Header  RevisionHeaderPreimageView
	Actors  []StoredActorView
	Context []StoredContextFactView
	Items   []ItemWireView
}
type IntegrityDigestInput struct {
	Revision     RevisionRef
	Domain       IdempotencyDomain
	ObservedAt   time.Time
	Semantic     SemanticDigest
	Envelope     EnvelopeDigest
}
type AppendIntentDigestInput struct {
	Domain   IdempotencyDomain
	Revision RevisionWireView
	Hold     HoldConditionalAppendView
	Attempt  AttemptConditionalAppendView
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
type ValueState uint8
const (
	ValueAbsent ValueState = iota + 1
	ValuePresent
	ValueRedacted
)
type StoredChangeView struct {
	Field  FieldName
	Before StoredValueView
	After  StoredValueView
}
func (r Revision) View() RevisionWireView

type StoreQuery struct{ value storeQuery }
func (q StoreQuery) View() StoreQueryView
type SearchProgressView struct {
	Pages     uint32
	Cohorts   uint32
	Revisions uint32
	Bytes     uint64
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
type QueryCoordinateView struct {
	Kind        QueryCoordinateKind
	Match       QueryCoordinateMatch
	Resource    Resource
	Action      Action
	Field       FieldName
	ActorPosition ActorPosition
	ActorKind   ActorKind
	ContextKind ContextFactKind
	EntitySide  EntityIndexSide
	Classification Classification
	Mode        StorageMode
	Alternatives []QueryCoordinateAlternativeView
}
type QueryCoordinateAlternativeView struct {
	Plaintext   []byte
	Tokens      []Token
	Commitments IdentityCommitmentSet
}
type StoreQueryView struct {
	Log          LogID
	Catalogs     []CatalogRef
	Class        QueryClass
	Resources    []Resource
	OperationName OperationName
	Operation    OperationID
	Coordinates  []QueryCoordinateView
	Actions      []Action
	Fields       FieldProjectionView
	Context      ContextProjectionView
	Time         TimeWindowView
	Direction    HistoryDirection
	Changed      ChangedFieldFilterView
	Outcomes     []Outcome
	Reasons      []Reason
	Limit        uint32
	MaxBytes     uint64
	CohortRevisions uint32
	CohortBytes uint64
	Progress     SearchProgressView
	Snapshot     []byte
	SnapshotExpiry time.Time
	After        StorePosition
}
type ExactTargetKind uint8
const (
	ExactRevisionTarget ExactTargetKind = iota + 1
	ExactItemTarget
)
type ExactTargetView struct {
	Kind     ExactTargetKind
	Revision RevisionRef
	Item     ItemRef
}
type ExactQuery struct{ value exactQuery }
type ExactQueryView struct {
	Log             LogID
	Catalogs        []CatalogRef
	Targets         []ExactTargetView
	Resources       []Resource
	Actions         []Action
	Classifications []Classification
	Coordinates     []QueryCoordinateView
	MaxBytes        uint64
}
func (q ExactQuery) View() ExactQueryView
type InventoryQuery struct{ value inventoryQuery }
func (q InventoryQuery) View() InventoryQueryView
type InventoryQueryView struct {
	Log             LogID
	Selection       SelectionDigest
	Catalogs        []CatalogRef
	Resources       []Resource
	Actions         []Action
	Retentions      []RetentionClass
	Classifications []Classification
	Coordinates     []QueryCoordinateView
	Limit           uint32
	MaxCohorts      uint32
	MaxCandidates   uint32
	MaxBytes        uint64
	CohortRevisions uint32
	CohortBytes     uint64
	Progress        SearchProgressView
	AsOf            time.Time
	Snapshot        []byte
	SnapshotExpiry  time.Time
	After           StorePosition
}
type VerificationQuery struct{ value verificationQuery }
func (q VerificationQuery) View() VerificationQueryView
type VerificationQueryView struct {
	Log             LogID
	Selection       SelectionDigest
	Request         ControlRequestDigest
	Grant           ControlGrantDigest
	Catalogs        []CatalogRef
	Resources       []Resource
	Actions         []Action
	Retentions      []RetentionClass
	Classifications []Classification
	Coordinates     []QueryCoordinateView
	Time            TimeWindowView
	Limit           uint32
	MaxBytes        uint64
	CohortRevisions uint32
	CohortBytes     uint64
	Progress        SearchProgressView
	Snapshot        []byte
	SnapshotExpiry  time.Time
	After           StorePosition
}
type HoldMembershipState uint8
const (
	HoldMembershipAbsent HoldMembershipState = iota + 1
	HoldMembershipActive
	HoldMembershipReleased
)
type HoldProjectionStateView struct {
	Revision   RevisionRef
	Membership HoldMembershipState
	Count      uint32
	Epoch      uint64
	ActiveSet  HoldSetDigest
	Head       RevisionRef
}
type HoldStateQuery struct{ value holdStateQuery }
type HoldStateQueryView struct {
	Log            LogID
	Catalogs       []CatalogRef
	Resources      []Resource
	Actions        []Action
	Classifications []Classification
	Coordinates    []QueryCoordinateView
	Request        ControlRequestDigest
	Grant          ControlGrantDigest
	Revision       RevisionRef
	Hold           HoldID
	Candidate      HoldIdentity
	HoldAliases    IdentityCommitmentSet
	MaxTransitions uint32
	MaxBytes       uint64
}
type HoldStateResultData struct {
	Identity    HoldIdentity
	State       HoldProjectionStateView
	Transitions []RevisionRef
}
func (q HoldStateQuery) View() HoldStateQueryView
type PurgeQuery struct{ value purgeQuery }
func (q PurgeQuery) View() PurgeQueryView
type PurgeQueryView struct {
	Log        LogID
	CatalogSet CatalogSetDigest
	AsOf       time.Time
	Snapshot   []byte
	SnapshotExpiry time.Time
	Cohorts    []RetentionCohortView
	Guards     []AttemptRetentionGuardView
	Candidates []InventoryCandidate
}

type StoredRevisionData struct {
	Revision      RevisionWireView
	RecordedAt    time.Time
	Position      StorePosition
	ActiveHoldCount uint32
	HoldEpoch     uint64
	ActiveHoldSet HoldSetDigest
	HoldTransitions []RevisionRef
}
type StoredHeaderData struct {
	Header                RevisionHeaderView
	Intent                AppendIntentDigest
	RecordedAt            time.Time
	Position              StorePosition
	AttemptTransitionPresent bool
	AttemptTransition     AttemptTransitionWireView
	AttemptProjection     AttemptProjectionStateView
	HoldTransitionPresent bool
	HoldDisposition       HoldTransitionDisposition
	HoldProjection        HoldProjectionStateView
}
type StoredRevisionView struct {
	Revision      RevisionWireView
	RecordedAt    time.Time
	Position      StorePosition
	ActiveHoldCount uint32
	HoldEpoch     uint64
	ActiveHoldSet HoldSetDigest
	HoldTransitions []RevisionRef
}
type StoredPageData struct {
	Revisions []StoredRevision
	Position  StorePosition
	HasMore   bool
	Progress  SearchProgressView
	Snapshot  []byte
	ExpiresAt time.Time
}
type RetentionCohortKind uint8
const (
	RevisionRetentionCohort RetentionCohortKind = iota + 1
	AttemptRetentionCohort
)
type RetentionCohortView struct {
	Kind       RetentionCohortKind
	ID         RetentionCohortID
	Attempt    AttemptChainID
	Terminal   AttemptState
	Members    []RevisionRef
	EligibleAt time.Time
	Held       bool
	CounterHeadPinned bool
}
type AttemptRetentionGuardView struct {
	Cohort  RetentionCohortID
	Current AttemptTypeProjectionStateView
}
type InventoryCandidate struct {
	Cohort         RetentionCohortID
	Revision       RevisionRef
	Authorization  RevisionAuthorizationSummaryView
	Retention      RetentionClass
	Integrity      IntegrityDigest
	RetentionBasis RetentionBasisDigest
	EligibleAt     time.Time
	ActiveHoldCount uint32
	HoldEpoch      uint64
	ActiveHoldSet  HoldSetDigest
	HoldTransitions []RevisionRef
}
type InventoryResultData struct {
	Cohorts      []RetentionCohortView
	Guards       []AttemptRetentionGuardView
	Candidates   []InventoryCandidate
	Continuation StorePosition
	Truncated    bool
	Progress     SearchProgressView
	AsOf         time.Time
	Snapshot     []byte
	ExpiresAt    time.Time
}
type PurgeCandidate struct {
	Cohort         RetentionCohortID
	Revision       RevisionRef
	Authorization  RevisionAuthorizationSummaryView
	Retention      RetentionClass
	Integrity      IntegrityDigest
	RetentionBasis RetentionBasisDigest
	ActiveHoldCount uint32
	HoldEpoch      uint64
	ActiveHoldSet  HoldSetDigest
	HoldTransitions []RevisionRef
}
type StorePurgePlanData struct {
	Cohorts  []RetentionCohortView
	Guards   []AttemptRetentionGuardView
	Eligible []PurgeCandidate
	AsOf     time.Time
}
type ExactEntryState uint8
const (
	ExactFound ExactEntryState = iota + 1
	ExactMissing
)
type ExactEntryData struct {
	Target   ExactTargetView
	State    ExactEntryState
	Revision StoredRevision
}
type ExactResultData struct {
	Entries []ExactEntryData
}

type EntityChainHeadQuery struct{ value entityChainHeadQuery }
type EntityChainHeadQueryView struct {
	Log         LogID
	Catalogs    []CatalogRef
	Resource    Resource
	Coordinates []QueryCoordinateView
	MaxBytes    uint64
}
type EntityChainHeadState uint8
const (
	EntityChainHeadMissing EntityChainHeadState = iota + 1
	EntityChainHeadPresent
)
type EntityChainHeadData struct {
	State EntityChainHeadState
	Chain EntityChainID
	Head  RevisionRef
	Leaf  LeafDigest
}
func (q EntityChainHeadQuery) View() EntityChainHeadQueryView
func NewEntityChainHead(EntityChainHeadQuery, EntityChainHeadData) (EntityChainHead, error)
func (h EntityChainHead) State() EntityChainHeadState
func (h EntityChainHead) Chain() EntityChainID
func (h EntityChainHead) Head() RevisionRef
func (h EntityChainHead) Leaf() LeafDigest

func NewEntityHeadResult(EntityHeadRequest, EntityHeadResultData) (EntityHeadResult, error)
func NewLookupResult(LookupRequest, LookupResultData) (LookupResult, error)
func NewStoredRevision(StoredRevisionData) (StoredRevision, error)
func NewStoredPage(StoreQuery, StoredPageData) (StoredPage, error)
func NewVerificationPage(VerificationQuery, StoredPageData) (StoredPage, error)
func NewInventoryResult(InventoryQuery, InventoryResultData) (InventoryResult, error)
func NewHoldStateResult(HoldStateQuery, HoldStateResultData) (HoldStateResult, error)
func NewStorePurgePlan(PurgeQuery, StorePurgePlanData) (StorePurgePlan, error)
func NewExactResult(ExactQuery, ExactResultData) (ExactResult, error)
func NewStorePosition([]byte) (StorePosition, error)
func (p StorePosition) Bytes() []byte
func (h StoredHeader) Revision() RevisionHeaderView
func (h StoredHeader) Intent() AppendIntentDigest
func (h StoredHeader) RecordedAt() time.Time
func (h StoredHeader) Position() StorePosition
func (h StoredHeader) AttemptTransition() (AttemptTransitionWireView, bool)
func (h StoredHeader) AttemptProjection() (AttemptProjectionStateView, bool)
func (h StoredHeader) HoldDisposition() (HoldTransitionDisposition, bool)
func (h StoredHeader) HoldProjection() (HoldProjectionStateView, bool)
func (r StoredRevision) View() StoredRevisionView
func (p StoredPage) Revisions() []StoredRevision
func (p StoredPage) Position() StorePosition
func (p StoredPage) HasMore() bool
func (p StoredPage) Progress() SearchProgressView
func (p StoredPage) Snapshot() []byte
func (p StoredPage) ExpiresAt() time.Time
func (r InventoryResult) Candidates() []InventoryCandidate
func (r InventoryResult) Cohorts() []RetentionCohortView
func (r InventoryResult) AttemptGuards() []AttemptRetentionGuardView
func (r InventoryResult) Continuation() StorePosition
func (r InventoryResult) Truncated() bool
func (r InventoryResult) Progress() SearchProgressView
func (r InventoryResult) AsOf() time.Time
func (r InventoryResult) Snapshot() []byte
func (r InventoryResult) ExpiresAt() time.Time
func (r HoldStateResult) State() HoldProjectionStateView
func (r HoldStateResult) Identity() HoldIdentity
func (r HoldStateResult) Transitions() []RevisionRef
func (r StorePurgePlan) Eligible() []PurgeCandidate
func (r StorePurgePlan) Cohorts() []RetentionCohortView
func (r StorePurgePlan) AttemptGuards() []AttemptRetentionGuardView
func (r StorePurgePlan) AsOf() time.Time
func (r ExactResult) Entries() []ExactEntryData
```

Header/data records contain only frozen wire fields enumerated in sections
2.3–2.4: format, LogID, CatalogRef/set/deployment digests, operation/revision/
idempotency identities, times, ordinal/kind/action/resource, protected subject
and context/value descriptors, predecessor/leaf, semantic/envelope/integrity
digests, seal, retention basis, canonical authorization summary, typed hold
transition, and backend position. Every
union has an explicit nonzero kind and exactly one legal arm. ValueState makes
absent distinct from a present empty encoding and from redacted-change evidence.
StoredActorView preserves dense hop ordinal, ActorKind, provenance, classification,
mode, and protected/tokenized/plain reference; constructors reject reordered,
duplicate, or gapped actor chains. Manifest exposes
copied `Canonical`, `Ref`, `Previous`, and ordered declaration/codec/context/
retention inventories; CatalogSet lookup is by exact CatalogRef. The authorization
summary is recomputed and compared before a Revision, StoredRevision, candidate,
fence, or report becomes valid. Query views contain the already-normalized
protected scope/subject/actor/context/target/event-index alternatives, action and
resource sets, projection, time axis and exact bounds, direction, changed-field
and code predicates, exact catalog refs, page/cumulative byte and cohort ceilings,
snapshot expiry/identity, progress, and backend position. Exact query views carry
only finite targets and their sealed resource/action/classification/coordinate
grant narrowing. No view contains a
grant-minting value or caller callback.

An entity item requires one nonzero EntityStateKind; every non-entity item
requires zero. EntityDeltaState has zero Values and Changes contains exactly the
declared canonical fields whose before/after state differs. EntityFullState has
the same honest Changes set plus a canonical Values entry for every ReconstructField
declared by that item's authenticated record-era catalog, with complete after-state
including explicit Absent or its
permitted reversible plaintext/protected representation. Redacted and token-only
fields are not reconstructable and are never pulled in merely because a baseline
is needed; no unchanged non-reconstructable field is captured. Created, restored, soft-deleted, and hard-
deleted items are full anchors. A changed item is Full only when establishing an
absent audit head for a pre-existing row and Delta only when advancing an
existing head in release 1. The action remains EntityChanged in the first case;
neither state kind implies business creation or a time before ObservedAt.
Constructors reject a missing/extra field, duplicate, wrong state kind, full
nonentity, delta genesis, full existing-head change, or Values/Changes
disagreement before signing or store I/O.

An attempt item requires ItemKind AttemptItem and exactly one nonzero
AttemptTransitionWireView. Resource equals the AttemptType descriptor resource;
Action is the exact closed mapping from transition kind to the seven
Attempt...Action constants. Entity state, hold, access/control/denial digests,
links, and every phase-illegal Target, Values, Reason, Checkpoint, or
ResumeAuthorization field are zero. Started alone carries the declared protected
Target and start Values. Checkpoint alone carries one declared Checkpoint code and
checkpoint Values. Succeeded carries finish Values and no Reason. Failed,
Cancelled, and OutcomeUnknown carry finish Values plus a reason from their exact
declaration-owned set. Abandoned carries no caller phase value and its
package-owned reason. Constructors reject mixed phase schemas before any
protection, signing, or store I/O. A Revision contains at most one AttemptItem;
co-revision business/entity items are allowed only through AttemptRun.Within.
This assigns every physical revision to at most one attempt retention cohort and
prevents transitive overlap between two attempt chains.

The immutable per-attempt state machine is total:

| Expected | Transition | Result |
|---|---|---|
| Missing | Started | Open |
| Open | Checkpoint | Open |
| Open | OutcomeUnknown | Uncertain |
| Open or Uncertain | Succeeded | Succeeded |
| Open or Uncertain | Failed | Failed |
| Open or Uncertain | Cancelled | Cancelled |
| Open or Uncertain | Abandoned | Abandoned |

Every other edge conflicts, except byte-identical idempotent replay, which
returns the original signed result and advances nothing. Started creates sequence
one, zero checkpoints, one Start/Head ItemRef, a nonzero previous-free leaf, and
ExpiresAt equal to its containing signed Revision Header.ObservedAt plus declared MaxOpen with
overflow refusal. TransitionBytes is the checked cumulative canonical size of
all attempt transition items including this result, never a store estimate. Each
checkpoint increments sequence and checkpoint count once and is admitted only
when its bytes plus the manifest's OpenReserveBytes remain within the policy,
hard, and store state ceilings. OutcomeUnknown is admitted only when its bytes
plus UncertainReserveBytes fit. These reserves cannot be spent by checkpoints or
uncertainty evidence.
Every other legal transition increments only sequence and its exact bytes. Start, policy,
operation/type, OperationID, target presence/commitment, scope presence/commitment, chain, and expiry never
change. Expected.Head/Leaf and the item Previous leaf are byte-identical. Result
names the candidate item but deliberately omits its not-yet-computed Leaf. After
the leaf is finalized, the outer AttemptAppendCandidateView binds the full result
projection including that leaf in AppendIntentDigest; it is the next transition's
Expected.Leaf. The type-counter result follows the same acyclic rule. The
containing signed Header.ObservedAt is transition time and may regress; sequence
never does. Duration is derived only from authenticated start and terminal
Header.ObservedAt values and is unknown on regression. No mutable duration
is stored.

The three ordinary finish methods can consume only Open. Uncertain can reach
Succeeded, Failed, or Cancelled only through ResolveUnknown after its distinct
current authorization evidence; Abandon is the sole other transition from
Uncertain. The shared state table describes wire edges, while the sealed command
kind and ResumeAuthorization distinguish and enforce the only legal public door.

AttemptChainID is the domain-separated stable candidate resolved only from LogID,
CatalogID, and OperationID. Linked OperationName, policy fingerprint, catalog
generation, scope, owner, key generation, and their active/retained aliases never
alter that candidate. They remain signed immutable start-era conditions, so the
same logical operation under a renamed operation or changed policy, scope, or
owner conflicts instead of creating a second chain.
AttemptIdentityAliasBinding carries the optional target, scope, and selected
stable owner tuple's active/retained commitments outside the evidence envelope; it never
exposes either logical identity. The start-era AttemptOwnerCommitment remains
signed in every expected/result state, and a later caller must match one retained
owner alias without changing that commitment. AttemptState looks up by the sole
chain candidate and must return an existing state even when its policy, scope, or
owner differs from the caller's candidate conditions; the kernel compares those
conditions after exact verification. A first start atomically inserts the unique
`(CatalogID, OperationID)` binding and binds every active identity
alias to that candidate, while resume and later transitions require that one
existing chain and preserve it across key rotation. A duplicate operation
binding, zero or multiple alias matches, a foreign log/backing/catalog/operation,
or an alias rebound conflicts.

For a start, AttemptStateQuery uses CatalogID plus the sole OperationID/Chain as
the existing-chain lookup coordinate. Any found chain is returned and its signed
operation/policy/replay coordinates are compared by the kernel after exact
verification. A separate AttemptTypeStateQuery reads the global counter, so a
missing chain still obtains the nonzero counter for another attempt of the same
type. PrepareCatalogActivation
uses AttemptTypeStateQuery directly for every changed or removed declaration;
that query is keyed by CatalogID, OperationName, Policy, and Replay and cannot
address an individual chain. Both result constructors bind the originating
sealed query, catalog lineage, and exact returned key, and reject a substituted
or omitted counter before it can enter a CAS or activation proof.

Started and a transition into any terminal state also carry one global
AttemptTypeProjectionStateView CAS for the stable
`(CatalogID, OperationName, AttemptReplayFingerprint)` type identity; Policy is
retained as an independently checked descriptive coordinate. Started
changes Unsettled from n to n+1; a transition from Open or Uncertain into a
terminal changes n to n-1; both move Head/Leaf to this signed item. Checkpoint and
OutcomeUnknown leave both type views canonically zero and do not contend on the
type counter. Overflow, underflow, noncanonical genesis, mismatched prior head,
or wrong signed result refuses. Every write uses one global lock order: active
catalog head; affected attempt-type projections sorted by
`(CatalogID, OperationName, AttemptReplayFingerprint)`; attempt heads; entity
heads; hold projections; then alias/idempotency rows, each inner class sorted by
canonical key. Start and terminal recheck the active CatalogRef while retaining
the catalog lock. Catalog activation follows the same prefix and locks every
changed/removed counter in sorted order. Append commits the revision, aliases,
all projections, and idempotency row atomically. Rollback and replay advance
none, and start/last-close versus activation has no lock inversion.

A never-used replay-policy counter has its complete Catalog/Operation/Policy/
Replay key, zero Anchor/Unsettled/Head/Leaf, and is the sole canonical genesis.
An activation-reset counter keeps that key, has zero Unsettled/Head/Leaf, and has
the exact nonzero gate Anchor; it is not interchangeable with genesis. The next
Started counter edge takes either form as Expected, preserves Anchor in Result,
and signs it. Every later counter edge preserves that anchor until another
incompatible activation installs a new gate. This makes a headless old-policy
certificate auditable without retaining its former sensitive transition head.

AttemptStateQuery is kernel-minted, equality-only, and bounded to one exact
CatalogID, OperationID, durable log, chain candidate,
transition count, and byte ceiling. Chain is the sole attempt lookup coordinate; the store
cannot turn a policy/scope/owner mismatch into Missing by adding hidden filters.
NewAttemptStateResult origin-validates the complete
ordered ItemRef set and chain projection. NewAttemptTypeStateResult separately
origin-validates the exact type selector and counter projection. Attempts then exact-inspects every
referenced containing revision, verifies record-era manifest, envelope, seal,
item ordinal, predecessor, and per-attempt transition, and fully recomputes the
bounded per-attempt state machine. The separately read current type-counter projection is checked
against exactly one exact-verified signed Head result, or against its canonical
genesis/activation anchor, and relies on inductive append-time CAS; its global
lifetime history is never claimed to be recomputed. A result cannot mint a handle
or allow catalog activation merely by claiming an unsigned head or zero count.

A lifecycle item for HoldPlaced or HoldReleased requires exactly one nonzero
HoldTransitionWireView and forbids Subject, Target, generic Values/Changes, and all
other item-specific arms except the required control digest trio. Every other
action requires the hold arm to be zero.
Placement, including HoldAlreadyActive no-op evidence, requires HoldPlaceCommand,
nonzero HoldID and commitments, MatterPresent, and exactly one protected StoredValue
matching the record-era matter codec/classification/mode. Release, including
HoldAlreadyReleased, requires HoldReleaseCommand, MatterPresent false, and zero
matter value/commitment. Both shapes require byte-equal
command/HoldID/stable HoldIdentity/target,
expected/result/disposition, and HoldRequestDigest across the signed arm and
HoldConditionalAppendView. EnvelopeDigest and IntegrityDigest cover the complete
arm. Header and receipt hold accessors are derived only after this unique arm
validates; no generic value or unsigned projection can substitute for it.

Audit-of-audit protocol fields are a strict union. A successful history or
reconstruction disclosure AccessItem requires exactly the access request/grant/
result trio. Every successful control evidence item requires exactly the control
request/grant/result trio; a hold lifecycle item additionally carries its distinct
HoldRequestDigest in the typed transition arm. A denial AccessItem carries only
DenialRequestDigest and its closed safe state. Missing,
mixed, cross-role, or partially zero trios fail construction, and every digest is
covered by the envelope and integrity signature.

Every QueryCoordinateView has an explicit match mode and declaration-owned
Classification. Exact carries one to MaxSelectorAlternatives logical
alternatives. Each alternative has exactly one legal canonical plaintext,
rotation-token set, or identity-commitment set for its kind and mode; aliases
inside one alternative are OR, alternatives are OR, and distinct coordinates
are AND. AllDeclared carries no value
material and is legal only in a control query derived from an origin-bound
EvidenceSelector for one exact declaration and action set. Empty coordinate
lists never mean wildcard. Stored authorization
summaries contain Exact coordinates only; an exact member is covered by either an
equal exact grant coordinate or one explicit same-kind/resource/action/mode/class
AllDeclared ceiling.

ExactQuery is the least-privilege control lookup seam. It carries a bounded,
canonical, duplicate-free set of RevisionRef or ItemRef targets plus the sealed
grant's catalog/resource/action/classification and protected scope/subject/target coordinates and byte
ceiling. It has no broad-search or caller-chosen predicate arm. ExactResult has
exactly one canonically ordered Found or Missing entry per requested target;
Found contains one complete copied StoredRevision and Missing contains none.
NewExactResult origin-validates target identity, cardinality, order, byte bounds,
and one-of shape. Control still authenticates and post-validates every Found row;
wrong-target, duplicate, omitted, extra, reordered, foreign-scope/resource/action/class,
and oversized results reject the whole operation.

HoldStateQuery is likewise a sealed-grant-narrowed store request,
not a raw HoldID lookup door. It binds the origin ControlRequestDigest and
ControlGrantDigest plus exact admitted catalogs, resources, record actions,
classifications, and protected scope/subject/target coordinates before its
count/byte limits. Its
origin-validating result constructor rejects a dropped, widened, substituted, or
foreign ceiling, and Control still exact-inspects every returned transition. An
external LifecycleLog can therefore apply least privilege before materializing any
hold state. HoldStateQuery carries HoldIdentityCandidate plus the complete
origin-bound active and retained CommitHoldID set so a rotated external store can
locate exactly one stable HoldIdentity without a broad scan; the active member
must match the authorization and conditional append commitment. No matched alias
returns the candidate, one returns the stored identity, and multiple distinct
identities are corruption. NewHoldStateResult origin-binds that exact identity,
projection, and complete transition set; Control authenticates the same identity
in every transition before computing HoldSetDigestOf.

AppendRequest.View separately carries the operational active/retained alias sets
needed to locate idempotency rows, entity heads, attempt scope/owner identities,
and HoldID-to-matter mappings.
They are not caller values and do not enter the immutable evidence envelope.
Each set has exactly one active description, at most one commitment per required
manifest description, the correct domain, canonical description order, and no
duplicate bytes. The store resolves all aliases and the active catalog/head in
the same transaction as Append, rejects zero/multiple mappings where an existing
identity is required, and atomically binds the active alias on replay/rotation.
The conditional arm's singular HoldCommitment must equal the active commitment
in its HoldIDAliasBinding, and its signed HoldIdentity must equal the binding's
resolved stable identity; the retained members are lookup-only. LookupRequest,
IdempotencyLookupRequest, EntityHeadRequest, and HoldStateQuery expose the
corresponding copied origin-bound sets. This makes external stores capable of
implementing rotation without a private assertion or a broad scan.

AppendRequest is a total one-of. An ordinary request has exactly one Revision and
zero Hold/Attempt arms. A hold request has a zero ordinary Revision, a sealed
HoldConditionalAppendView with one authenticated expected projection and one
legal result candidate, and a zero Attempt arm. An attempt request likewise has
a zero ordinary Revision, zero Hold arm, and exactly one sealed
AttemptConditionalAppendView whose candidate contains the sole revision; that
revision has exactly one AttemptItem but may also contain admitted entity items
for AttemptRun.Within. More than one AttemptItem or attempt chain per revision is
invalid. Before construction, Control obtains a bounded
HoldStateResult, exact-inspects every referenced transition, verifies its
record-era manifest, envelope, seal, target, previous/result state, and complete
genesis-to-head chain, then recomputes membership, count, epoch, and active-set
digest. The candidate disposition is a total function of that verified prior
state and the command. Its signed header and lifecycle item bind both expected
and resulting HoldProjectionStateView. AppendIntentDigest is a distinct keyed,
domain-separated canonical commitment to the complete immutable candidate,
including generated revision/operation identity, command, expected/result state,
disposition, exact wire/envelopes, semantic digest, integrity digest, and seal;
the intent field itself is outside that input. For an ordinary append it binds
the same complete immutable candidate shape; the attempt arm binds both CAS
states, resume authorization, full candidate projections, and candidate revision
without entering the revision/leaf cycle. The store persists both the stable
SemanticDigest and exact AppendIntentDigest in its idempotency/revision index.

After authorization and before HoldState, a keyed Control command computes its
stable logical SemanticDigest and calls LookupIdempotency through a kernel-minted
IdempotencyLookupRequest. A Found result is committed-only advisory lookup data:
Control exact-inspects and authenticates the complete original revision and
requires its command/request/domain/catalog/deployment/semantic identity before
returning the stored hold outcome. AbsentNow is not a write reservation. Control
then reads hold state and builds a candidate normally. For a conditional request
Append repeats the complete keyed lookup atomically before CAS, then locks HoldID
aliases, membership and the target projection, and performs an exact CAS against
every expected membership/count/epoch/set/head component before it stores the one
signed candidate. It updates the projection in the same transaction when
effective and returns the exact resulting state through NewHoldAppendResult.
Mismatch, stale state, conflict, and not-found rows append nothing. A keyed replay
matches the stable SemanticDigest and may return an older different-intent
candidate; an exact RevisionID replay requires the identical AppendIntentDigest.
Neither consults current hold state after its atomic match. The pre-read remains
advisory input to a signed CAS, never an unsigned guessed outcome.
Rollback to an older otherwise valid signed store snapshot remains part of the
explicit external-anchor limitation; it is not mislabeled as locally detectable.

Every slice/byte accessor returns a copy. Stores may read inputs only for the
call and must copy anything retained. Constructors copy answers before returning;
the caller owns every page/report afterwards. Constructors enforce kernel and
store bounds, canonical order, uniqueness, exact originating query token, and
one-of shape; History/Control still post-validate all semantics and integrity.
`Capabilities` and `Limits` are copied public data with a validating constructor;
`Backing` is minted from a comparable private identity by `BackingFor`, and
StoreCatalogState is a copied atomic snapshot. Every result above exposes copied
state/header/position/candidate accessors. Revision.View and every query/result
view are deep copies with only exported fields or further copied accessors; no
transitively opaque header, item, value, manifest, or result remains. No valid
success shape is constructible without its originating request, and no invalid
shape requires a store to forge a private field. External-package compile
fixtures implement Writer-only, Log-only, ExactLog-only, LifecycleLog-only, and
CatalogAdmin-only stores and construct every answer. The same fixtures are imported unchanged by
auditpg before its SQL work begins.

`NewStoredHeader` reconstructs an intrinsically valid complete persisted immutable
header, nonzero exact AppendIntentDigest, recorded time, position, and optional
hold or attempt result projection without an in-process AppendRequest. Its two
presence bits are a strict one-of: ordinary headers require both arms zero, a
hold header requires a valid disposition/projection and zero attempt fields, and
an attempt header requires one valid transition/full resulting projection and
zero hold fields. Then
`NewAppendResult` origin-validates it against the request: Inserted and a
same-RevisionID replay require byte-exact header and AppendIntentDigest equality;
a keyed replay with another RevisionID requires catalog, operation,
idempotency aliases, semantic digest, deployment and lineage while deliberately
retaining the persisted original revision/operation IDs, observed time, envelope,
integrity and seal. It never substitutes the fresh candidate header.
That exact-coordinate rule is for RecordIdempotencyDomain; attempt replay uses
the retained-compatible policy rule above. For a conditional request only
NewHoldAppendResult or NewAttemptAppendResult is legal. Inserted or
same-RevisionID replay requires the StoredHeader's hold disposition/projection and
intent to match the single signed candidate. A keyed replay carrying another
RevisionID instead returns the original persisted disposition/projection after
the stable request/domain/catalog/deployment/semantic checks; it must not match
the fresh state-derived candidate. NewAttemptAppendResult validates the unique
transition and complete attempt/type projection; its keyed replay returns the
historical transition-local result without claiming current state. Ordinary
NewAppendResult rejects a conditional request, each specialized constructor
rejects the other arm, and hold/attempt arms cannot coexist.
NewIdempotencyLookupResult applies the same stable
identity checks, accepts only Found-with-header or AbsentNow-without-header, and
never carries transaction Authority or staged visibility.
`NewLookupResult` independently binds the persisted header to the authenticated
ReconcileKey and lookup aliases. External stores can therefore restart and
reconstruct either answer from rows plus the current originating request.

Append owns one frozen request and executes it once. Its disposition is Inserted
or Replayed and it returns the original stored header on replay. `Lookup` returns
Found or `AbsentNow`; absence is not proof that an unconfirmed transaction cannot
later commit and never authorizes different content. The safe reconciliation
operation is another idempotent append of the exact frozen semantics.

EntityHead is an exact-authority, bounded, non-locking snapshot read used only
inside the internal mutation Guard.Run after the exact persisted outcome is known
and while its entity row lock is still held; generated-ID create uses the returned
ID. Writer has no standalone EntityHead door. Every successful EntityHeadResult
carries one valid Authority, and the kernel requires it to equal the enclosing
Execution authority before using any returned state. Its request carries the
active EntityChainKey plus every required retained alias. The genesis candidate
is the fixed domain-separated SHA-256 conversion of
`"frostgrove.audit/entity-chain/v1"`, the canonical Resource, exact scope-presence
bit and EvidenceScopeCommitment, and the active pseudorandom EntityChainKey; it
uses no clock, random ID source, raw subject, or
store allocation. Two writers for the same new subject therefore propose the same
candidate, while a key-rotated writer first resolves retained aliases. Zero
existing aliases means genesis; exactly one stable EntityChainID and
head may exist; aliases resolving to multiple IDs are corruption and never
silently choose a head. The returned chain ID and previous leaf digest enter the
entity draft before canonicalization/signing. Append locks all affected alias and
head rows in canonical resource/scope-presence/scope-commitment/key order, verifies each expected predecessor,
binds the active alias to the stable chain, and advances them atomically with the
revision. A mismatch conflicts and rolls
back the business mutation. Exact revision/idempotency replay is resolved before
head validation and never advances a head twice, so retrying an older accepted
revision after later changes still returns its original receipt.
Concurrent genesis is CAS-create: one append establishes the candidate, alias,
and head, while a different revision with the same absent predecessor conflicts;
retry/replay returns the original without a second head advance.

EntityGenesis requires the deterministic candidate and a zero Previous; it means
only that this audit log has no prior head, not that the business row was created
then. A real insert uses EntityCreated plus EntityFullState. A first changed
mutation of a pre-existing locked row uses EntityChanged plus EntityFullState;
BaselineChanged extracts that complete reconstructable after-state and only the
actual Changes once. A first audited delete or restore of a pre-existing row uses
its truthful action and existing full-anchor shape. A delta-shaped EntityChanged
can never establish genesis.
EntityExisting and EntityTerminal require the one resolved stable chain and its
nonzero current leaf. A newly inserted hard-delete entity item atomically advances
that head and marks it terminal. Every later non-replay mutation for any active or
retained alias of that scoped subject refuses, including a database row recreated
with the same local ID; it cannot be interpreted as a second genesis or a silent
new incarnation. A soft delete remains EntityExisting and only its declared
restore can advance the chain. Append CASes the expected nonterminal state as well
as the predecessor, so a concurrent hard delete cannot race a recreate into the
chain. Reuse requires an application-defined new subject identity/namespace; a
future explicit incarnation policy would need a distinct signed wire contract.

For Inserted, nil Execution plus invalid result authority means standalone
Committed; a non-nil frame-bound Execution requires the exact same result authority and means
InCallerTransaction. Any other successful Inserted pairing is itself an
Unconfirmed contract breach. Replayed is legal for a standalone request only when
the original row is already Committed, and for a transaction-bound request only
when the duplicate is visible inside that exact same live Execution. A committed
duplicate found while another non-nil Execution is expected is an authority replay
conflict that poisons and rolls back the new unit; a second business mutation can
never borrow evidence committed by the first transaction. A uniqueness waiter on
another transaction does not return until that transaction commits or rolls back.

### 2.6 Recorder, retries, receipts, and grouping

```go
type Recorder struct{ value recorder }
type Draft struct{ value draft }
type EntityDraft struct{ value entityDraft }
type RecordResult struct{ value recordResult }
type CaptureResult struct{ value captureResult }
type Receipt struct{ value receipt }
type RetryToken struct{ value retryToken }
type GroupResult struct{ value groupResult }

type RecordOption interface{ auditRecordOption(*recordOptions) }
func InOperation(*OperationType) RecordOption
func WithIdempotencyKey(IdempotencyKey) RecordOption

type GroupSpec struct {
	Operation      *OperationType
	IdempotencyKey IdempotencyKey
}

type AppendDisposition uint8
const (
	Inserted AppendDisposition = iota + 1
	Replayed
)

type Settlement uint8
const (
	Committed Settlement = iota + 1
	InCallerTransaction
)

type RetryState uint8
const (
	CertainlyNotWritten RetryState = iota + 1
	PendingUnknown
)

type RecoveryMode uint8
const (
	RetryStandalone RecoveryMode = iota + 1
	ReconcileTransaction
)

type LookupState uint8
const (
	Found LookupState = iota + 1
	AbsentNow
)

func (r RecordResult) Receipt() (Receipt, bool)
func (r RecordResult) RetryToken() (RetryToken, bool)
func (r CaptureResult) Staged() bool
func (r CaptureResult) Receipt() (Receipt, bool)
func (r CaptureResult) RetryToken() (RetryToken, bool)
func (r Receipt) Disposition() AppendDisposition
func (r Receipt) Settlement() Settlement
func (r Receipt) RevisionID() RevisionID
func (r Receipt) OperationID() OperationID
func (r Receipt) ReconcileKey() (ReconcileKey, bool)
func (r Receipt) HoldDisposition() (HoldTransitionDisposition, bool)
func (r Receipt) HoldProjection() (HoldProjectionStateView, bool)
func (r Receipt) AttemptTransition() (AttemptTransitionWireView, bool)
func (r Receipt) AttemptProjection() (AttemptProjectionStateView, bool)
func (r RetryToken) State() RetryState
func (r RetryToken) Mode() RecoveryMode
func (r RetryToken) ReconcileKey() ReconcileKey
func (r GroupResult) Joined() bool
func (r GroupResult) Receipt() (Receipt, bool)
func (r GroupResult) RetryToken() (RetryToken, bool)
func (r GroupResult) ReconcileKey() (ReconcileKey, bool)
func (r LookupResult) State() LookupState
func (r LookupResult) Receipt() (Receipt, bool)
func (r LookupResult) HoldDisposition() (HoldTransitionDisposition, bool)
func (r LookupResult) HoldProjection() (HoldProjectionStateView, bool)
func (r LookupResult) AttemptTransition() (AttemptTransitionWireView, bool)
func (r LookupResult) AttemptProjection() (AttemptProjectionStateView, bool)

func New(Config) (*Recorder, error)
func (r *Recorder) Record(context.Context, Draft, ...RecordOption) (RecordResult, error)
func (r *Recorder) Within(context.Context, GroupSpec, func(context.Context) error) (GroupResult, error)
func (r *Recorder) Stage(context.Context, Draft) error
func (r *Recorder) Capture(context.Context, Draft) (CaptureResult, error)
func (r *Recorder) Retry(context.Context, RetryToken) (RecordResult, error)
func (r *Recorder) Lookup(context.Context, ReconcileKey) (LookupResult, error)
func (r *Recorder) CheckAtomicSource(any) error
func RetryTokenOf(error) (RetryToken, bool)
func ReconcileKeyOf(error) (ReconcileKey, bool)
```

Durable attempts have a separate typed orchestration surface; generic Draft,
Record, Stage, and caller-authored GroupSpec cannot manufacture an AttemptItem:

```go
type Attempts struct{ value attempts }
type AttemptsConfig struct {
	Recorder          *Recorder
	State             AttemptLog
	Types             AttemptTypeState
	History           *History
	SettlementTimeout time.Duration
}
type AttemptRunSpec struct {
	IdempotencyKey IdempotencyKey
}
type AttemptBeginSpec struct {
	IdempotencyKey IdempotencyKey
}
type AttemptTransitionSpec struct {
	IdempotencyKey IdempotencyKey
}
type AttemptGroupSpec struct {
	IdempotencyKey IdempotencyKey
}
type AttemptAccessSpec struct {
	Purpose Purpose
	Role    Reference
	Scope   ScopeSelector
	Target  Reference
	MaxBytes uint64
}
type AttemptResult struct{ value attemptResult }
type AttemptExecution struct{ value attemptExecution }
type AttemptCompletion[F any] struct{ value attemptCompletion[F] }
type AttemptRun[C, F any] struct{ value attemptRun[C, F] }
type AttemptProgress[C any] struct{ value attemptProgress[C] }

func NewAttempts(AttemptsConfig) (*Attempts, error)
func AttemptSucceeded[F any](F) AttemptCompletion[F]
func AttemptFailed[F any](Reason, F) AttemptCompletion[F]
func AttemptCancelled[F any](Reason, F) AttemptCompletion[F]
func AttemptOutcomeUnknown[F any](Reason, F) AttemptCompletion[F]
func (a *AttemptType[S, C, F]) Run(
	context.Context,
	*Attempts,
	S,
	AttemptRunSpec,
	func(context.Context, *AttemptProgress[C]) (AttemptCompletion[F], error),
) (AttemptExecution, error)
func (a *AttemptType[S, C, F]) Begin(
	context.Context,
	*Attempts,
	S,
	AttemptBeginSpec,
) (*AttemptRun[C, F], AttemptResult, error)
func (a *AttemptType[S, C, F]) Resume(
	context.Context,
	*Attempts,
	OperationID,
	AttemptAccessSpec,
) (*AttemptRun[C, F], AttemptResult, error)
func (a *AttemptType[S, C, F]) ResolveUnknown(
	context.Context,
	*Attempts,
	OperationID,
	AttemptAccessSpec,
	AttemptCompletion[F],
	AttemptTransitionSpec,
) (AttemptResult, error)
func (a *AttemptType[S, C, F]) Abandon(
	context.Context,
	*Attempts,
	OperationID,
	AttemptAccessSpec,
	AttemptTransitionSpec,
) (AttemptResult, error)
func (r *AttemptRun[C, F]) Checkpoint(context.Context, C, AttemptTransitionSpec) (AttemptResult, error)
func (p *AttemptProgress[C]) Checkpoint(context.Context, C, AttemptTransitionSpec) (AttemptResult, error)
func (r *AttemptRun[C, F]) Finish(context.Context, AttemptCompletion[F], AttemptTransitionSpec) (AttemptResult, error)
func (r *AttemptRun[C, F]) Within(
	context.Context,
	AttemptGroupSpec,
	func(context.Context) (AttemptCompletion[F], error),
) (AttemptExecution, error)
func (r AttemptResult) ResultingState() (AttemptState, bool)
func (r AttemptResult) Receipt() (Receipt, bool)
func (r AttemptResult) RetryToken() (RetryToken, bool)
func (r AttemptResult) ReconcileKey() (ReconcileKey, bool)
func (r AttemptResult) Transition() (AttemptTransitionWireView, bool)
func (r AttemptExecution) Attempt() AttemptResult
func (r AttemptExecution) ApplicationError() error
func (r AttemptExecution) Invoked() bool
func (r *AttemptRun[C, F]) OperationID() OperationID
func (r *AttemptRun[C, F]) State() (AttemptState, bool)
```

NewAttempts is allocation/validation only. It requires AttemptLifecycle,
Idempotency, StableSearch, and ExactInspection to be exactly SupportSupported,
requires the
AttemptLog, AttemptTypeState, and History facets to share the Recorder's process Backing, durable
BackingID/LogID, active CatalogRef, and CatalogSet, and requires every catalog
containing an AttemptType to require a record-era signature. Attempts uses the
Recorder's already-frozen Clock; there is no second clock authority. It starts no
timer, scan, goroutine, reaper, callback registry, or framework integration.
The active ControlPolicy must declare AttemptContinuationAuthorized and
AttemptAccessDenied with the denial reason set before NewAttempts succeeds.
SettlementTimeout is positive and no greater than
MaxAttemptSettlementTimeout; it bounds only the one explicit post-callback
terminal attempt.
Each Required Begin/Run and every restart Resume additionally requires
Persistence and Reconciliation; AttemptRun.Within also requires Transactions and
CrossSystemAtomic.
Each required capability must equal SupportSupported: Unstated is not support.
The memory store can exercise the full state machine with an explicitly
BestEffort policy but advertises no durable-across-process completeness claim.

Run has a fixed spec followed by the callback, so its Go signature remains
extensible without an illegal non-final variadic. It derives distinct start and
terminal idempotency commitments by copying the nonempty Run key once and asking
the existing IdentityKeyring under the two typed IdempotencyDomain AAD values;
there is no second KDF, profile, or raw-key store exposure. For Required and
BestEffort alike, the callback runs exactly once only after Started is Committed
with Inserted disposition and a bound run exists. A Replayed start returns its
original receipt with AttemptExecution.Invoked false, no handle, zero callback
calls, and no invented application result; the caller may perform an explicitly
authorized Resume or query its business result. Concurrent same-key Run calls
therefore cannot both execute protected work. A failed or unconfirmed BestEffort begin remains visible
and proceeding without the callback is an explicit application choice outside
the lifecycle claim. The callback returns an explicit completion and an
application error as independent facts. No error, panic, cancellation, or
deadline is translated into a completion. A nonzero legal completion is appended
even when the application error is non-nil, and AttemptExecution returns that
same application error without sending it to any audit collaborator. A zero
completion plus non-nil application error leaves the attempt Open; zero plus nil
is invalid. A panic leaves it Open and is re-raised unchanged. An audit failure
is the separate Run error, while the returned AttemptExecution still preserves
the application error if the callback ran.
After an explicit completion returns, Run performs that one terminal append on a
new value-free settlement context with SettlementTimeout; it does not inherit
caller values, cancellation, deadline, or Cause and it uses the context evidence
frozen at Started. This is neither a retry nor inference from cancellation. A
timeout is classified normally and returns recovery state; there is no second
append or callback replay.

Before a start replay lookup, Attempts resolves the stable OperationID, computes
the global chain candidate, and asks AttemptState by that candidate. If a chain
exists it exact-verifies it first, requires byte-equal operation, policy, and
replay fingerprints plus a current scope/owner alias intersection, and builds the candidate semantic input
with the authenticated start-era Scope and Owner commitments. Current active
commitments are used only for a missing chain. Thus identity-key rotation neither
changes replay equality nor hides a changed logical scope or owner. The global
start idempotency locator is then checked even when the candidate chain is
missing, so reusing the same key with another OperationID or declaration
conflicts before the callback. Ephemeral context never enters
AttemptSemanticDigest; an exact replay returns the immutable original header and
context rather than re-enveloping the new request.

The callback receives only AttemptProgress, not the finish/group-capable run.
Progress is origin-bound and permits one synchronous Checkpoint call at a time.
Run atomically marks it closing before it accepts the returned completion; a
saved pointer invoked afterward or a concurrent second checkpoint refuses with
zero resolver, crypto, History, AttemptLog, or Writer calls. If one checkpoint
was already in flight, Run waits only within SettlementTimeout for its classified
result and never races a terminal against an unknown checkpoint. Timeout leaves
the durable attempt outcome unknown, invalidates both Progress and its underlying
run locally, returns recovery state, and performs no terminal append. Any
Unconfirmed checkpoint or finish does the same: subsequent calls and State refuse
locally until Retry/Lookup settles the frozen request and a newly authorized
Resume reconstructs an exact verified handle. NotWritten leaves the last verified
handle state usable only when the returned token proves no write statement was
issued. Exact replay of an older checkpoint
returns its receipt without moving Progress or the underlying run backward.

Begin independently commits exactly one Started transition and returns a run only
when its receipt is both Committed and Inserted. Replayed, not-written, and
unconfirmed starts return no run. Each standalone transition has its own explicit
key and exact typed idempotency domain. The opaque pointer run binds the exact
Attempts/Recorder, process Backing, durable BackingID/LogID, CatalogSet,
AttemptType/OperationType, OperationID, AttemptChainID, scope alias set, context
evidence, stable owner aliases, current authenticated state/head, and consequence. It is safe to
alias as the same capability but cannot be forged, serialized, text/JSON encoded,
or reconstructed from public fields. Checkpoint and Finish revalidate all bound
coordinates and the current store identity before callbacks or append. An exact
replay returns its original receipt but never moves a local run backward from a
later verified sequence or resurrects a consumed/terminal capability; a stale
genuine transition conflicts and does not retarget the handle.

AttemptRun.Within is the only public atomic grouping door. It internally creates
a group bound to the handle's exact sealed OperationType, OperationID, scope,
context, catalog, consequence, and attempt expected state; the caller cannot
substitute them through GroupSpec. It resolves and freezes the same source-bound
Execution before invoking its callback. The callback receives the group context,
performs admitted business mutations, and returns an explicit completion. The
kernel stages exactly one terminal AttemptItem in that group, then the single
Append atomically CASes all entity heads and the attempt/type heads. More than one
attempt conditional arm, a checkpoint, OutcomeUnknown, BestEffort/Required mix,
or a terminal whose AttemptDescriptor transaction coordinates differ from its
linked OperationPolicy refuses before the callback. Callback error or panic with
no explicit completion poisons/rolls back the group and leaves the independently
committed attempt Open. A known rollback may later support an explicit Failed;
an unconfirmed commit returns the terminal ReconcileKey and never relabels it.
The store's linearizable attempt-head CAS is the final race gate: a competing
standalone terminal cannot commit while the original transaction can still win;
after lock resolution it either observes the committed terminal and conflicts or
can advance only after the original rolled back.

Resume is not ordinary read permission. It asks History for one value-free
AttemptAccessRequestView under AccessIntent AttemptResumeAccess, obtains a
distinct origin-bound AttemptAccessGrantSpec, queries AttemptLog, exact-inspects
the bounded encrypted/signed chain, and independently commits only a value-free
AccessAttemptAuthorizationResult before returning a capability. Its strict
request branch has zero ordinary Target, Query, Reconstruction, and Comparison;
it projects no fields or context, invokes no Revealer, and does not require
permission to disclose Personal/Secret phase values. The current sealed grant
must cover the exact requester, stable attempt owner, declaration-fixed target
presence and caller-supplied logical target when present, scope, resource,
operation/policy, intent, transition/byte ceilings, and expiry. Its
AccessResultDigest is copied into every transition made by the resumed handle.
Before every later resumed Checkpoint or Finish, History samples the Recorder
clock once, reauthorizes the same intent and requester against both original and
prior-effective grant ceilings, commits fresh result evidence, and refuses expiry
or revocation before AttemptLog/Writer I/O. The current owner alias set must
intersect the start-bound AttemptOwner identity without changing its signed
commitment. AttemptAccessSpec.Target is required exactly for AttemptTarget and
must match one retained target alias; it is zero for NoAttemptTarget. A mismatch
or fabricated target refuses before access evidence or transition append. It
returns a mutable run only for Open. ResolveUnknown repeats
the process with AttemptResolveAccess and accepts only Succeeded, Failed, or
Cancelled. Abandon uses AttemptAbandonAccess, one trusted clock sample not before
ExpiresAt, and only Open or Uncertain. A history-disclosure grant cannot be
replayed as any of these mutation intents. Suppression/rate limiting before Begin
remains an explicit application admission precondition, not a kernel claim.

Receipt, LookupResult, StoredHeader, AppendResult, retry, and reconciliation
expose the original AttemptTransition and full resulting projection only after
the unique attempt arm validates. Ordinary/hold headers return false and zero.
No accessor recomputes an old outcome from current mutable state. OutcomeUnknown
is Uncertain rather than terminal; the original active handle is consumed and
only the currently authorized ResolveUnknown or Abandon doors can leave it.
AttemptResult.ResultingState returns present only for an authenticated Committed
Inserted or exact Replayed transition carrying that projection. NotWritten,
Unconfirmed, malformed, and pre-append refusal paths return `(0, false)`; neither
the expected nor candidate state is presented as the observed result. Recovery
knowledge remains solely in RetryToken/ReconcileKey and a later exact lookup.
AttemptRun.State likewise exposes only the last exact verified state while the
handle remains usable. It returns false after consumption or invalidation and
never predicts the result of an unconfirmed append.

`Record` is standalone shorthand and refuses inside a group; grouped callers use
`Stage`. Success returns a receipt with independent Inserted/Replayed disposition
and Committed/InCallerTransaction settlement. A caller-transaction receipt never
self-upgrades and exposes no backend position. After commit the caller may Lookup
visibility; only exact Retry/Record semantics settle uncertainty.
For a validated hold header, Receipt and LookupResult expose both the original
signed HoldDisposition and complete HoldProjection; every ordinary header returns
false and the zero value from both hold accessors. Retry and reconciliation thus
preserve the same observable hold result after later state changes rather than
reconstructing it from the current projection.

`ErrNotWritten` and `ErrUnconfirmed` return an opaque, copied RetryToken marked
CertainlyNotWritten or PendingUnknown. The token is present in the result and in
the returned audit error. `RetryTokenOf` is the bounded, panic-safe owner door
that recovers it after ordinary wrappers without exposing the store cause or
including token bytes in rendering. Other errors return no token. Retry calls
Append exactly once with the already frozen request; it reruns no callback,
getter, clock, context resolver, protector, signer, or ID source and does not
consume the token; it also reruns no semantic digester or tokenizer and reuses
the byte-exact protected envelope. RetryToken is deliberately process-local and
has no Bytes, Parse, text, JSON, or database encoding; MaxRetryTokenBytes bounds
its copied in-memory frozen request. Durable recovery uses the bounded
ReconcileKey plus an application rebuild of the same declared/idempotent logical
operation, so no unauthenticated serialized callback or envelope becomes an
execution capability. A process restart must rebuild the same stable event semantics
and idempotency identity; Lookup `AbsentNow` is never permission to change them.
There is no automatic retry, callback replay, compensation, or reconciliation.
Retry uses both replay doors: a keyed token retains its idempotency domain and an
unkeyed token retains its exact revision identity and integrity content. A token
also freezes whether the attempted append was standalone or caller-transaction
bound. Only `RetryStandalone` may call Retry. Every transaction-bound append
failure returns `ReconcileTransaction`; Retry refuses it before Writer I/O even
if the same transaction still looks structurally present, because a failed
transaction cannot be assumed usable and audit must never be re-appended apart
from the business mutation. `RetryToken.ReconcileKey` permits a committed-
visibility Lookup without exposing token bytes or authority; `AbsentNow` remains
non-final. `ErrCommitUnconfirmed` carries the same safe ReconcileKey through
`ReconcileKeyOf`, never a door that retries either write.

Every InCallerTransaction receipt exposes its opaque ReconcileKey, including the
outer GroupResult, before transaction control returns to its owner. Recorder.Lookup
constructs a Deadline/Done/Err-only context with normalized Cause, strips every caller value
and audit transaction/group binding, and uses a dedicated committed-snapshot store
door, and rejects any Found answer carrying live authority or uncommitted state.
LookupResultData makes that contract representable: both Found and AbsentNow must
declare Committed visibility and an invalid Authority; any other combination,
missing Found header, or present AbsentNow header fails origin validation with no
receipt. Store conformance requires a connection/snapshot that cannot see staged
rows even when the caller passed its original transaction context.
Passing the original transaction context cannot reveal its staged row: before
commit Lookup is AbsentNow, after commit it is Found, and after rollback it stays
AbsentNow. A malicious transactional Lookup answer is a contract failure with no
receipt. This is observation only and never changes the transaction outcome.

Required consequence is admitted for entity, control, and reconstructable
evidence. BestEffort is allowed only for an explicitly declared manual event,
marks the stored revision non-authoritative, and never changes an error into
success: the caller still receives the classified append error and decides what
its application may continue. Required and BestEffort items cannot group.

Generic Record, Stage, and Capture accept the distinct manual-event `Draft` type.
ResourcePolicy capture methods return the distinct opaque `EntityDraft` type,
which has no Recorder/Writer append door and runs only inside the shipped
adapter's internal mutation bridge;
control/access/lifecycle drafts use private service-owned doors. No exported root
API accepts an entity draft or exposes a mutation guard, spec, batch, or permit.
An external-package compile negative proves that application code cannot
manufacture entity evidence through Recorder even though it can always choose to
bypass the audited repository through a path declared outside policy.

Capture is the adapter door: it stages when the exact recorder has an active
group in context and returns `Staged`; otherwise it performs one Record and
returns the same receipt or retry token. An adapter may discard a successful
receipt, but a retryable error itself retains the identical token so an ordinary
CRUD-shaped return cannot lose recovery state. CheckAtomicSource is a pure
bind-time check over the configured Writer capability and non-rendered source
identity; it does not expose the Writer, inspect context, or perform I/O.

Auditcrud uses the narrower mutation door. The collaboration contract lives in
`audit/internal/auditcrudbridge`, which is importable by the root audit package
and its `auditcrud` child but not by application packages. It carries opaque
Spec, Guard, and Batch values and no global registry, callback registration, or
service lookup. Auditcrud first calls
`crud.SourceBoundExecutorFor`; no binding is allowed only so a non-grouped
adapter can open its owned transaction, while unsafe fallback and an explicitly bound
nontransaction executor are refused. The internal Spec is derived from a sealed
resource/action and carries all known subjects or one explicit generated-subject
slot. It is not mutation authority; only the guard owns the supervised callback.
Internal preflight purely checks
the Recorder's source/frame and atomically reserves item/byte capacity and known
`(resource, scope-presence, EvidenceScopeCommitment, EntityChainKey alias-set)`
intersections before `crud.InAtomic` or
database I/O. A known duplicate refuses before a second repository call. If a group exists,
Unbound, another source/recorder, or a transaction Authority different from the
Execution origin-bound to that exact frame is refused at this point. Conversely, an already caller-owned
transaction without this Recorder's active Within frame is refused before model
I/O: an ordinary CRUD return cannot carry the pending receipt, and no group poison
latch would force rollback if application code swallowed a later audit error.
Only an adapter-owned transaction or the exact active group is supported in
release 1.

Inside the transaction callback, the adapter calls the one-use Guard.Run. Run
resolves the Writer Execution, requires an explicitly
source-bound root transaction, rechecks the frame/reservations, and only then
invokes its repository callback exactly once. The callback returns a sealed
bounded batch of zero or more entity drafts matching the exact spec. A successful
empty batch
releases its reservation. Otherwise Run resolves generated subjects, obtains the
entity heads in canonical order, canonicalizes/protects/signs, and stages or
appends the one resulting revision in that same
transaction. Any callback error or panic, generated-subject collision, head,
codec, semantic-digester, protection, signing, membership, bound, sealing, or
staging failure after Run begins permanently latches the first safe failure on
the outermost group. Panic is re-raised unchanged. Nested cleanup never clears
the latch. Outermost Within refuses a poisoned or outstanding-reservation frame,
returns the latched failure even if application code suppresses the adapter
error, performs zero Append calls, and requires its transaction owner to roll
back. The guard is recorder/frame-bound, goroutine-safe, never renderable, and
has no public mutation permit to escape supervision.
The internal batch builder copies its inputs and rejects mixed policy/action, duplicate
subjects, manual/control drafts, or any item not covered by the internal Spec.
Plural delete/restore batches therefore advance every affected head atomically;
zero drafts is the only no-op representation.

For standalone Record, OperationName defaults to the sealed event action;
callers may override only with a catalog-declared name.
OperationID is resolved from the selected ContextPolicy's admitted trusted fact
or generated once only for `GeneratedOperationFact`.
`InOperation` accepts only a non-nil `OperationType` from the Recorder's exact
active CatalogSet and `WithIdempotencyKey` accepts one nonempty bounded key;
duplicates, a group-only member mismatch, or either option inside an active
group refuse before callbacks or collaborators. The sealed option values are
copied and have no caller-defined implementation. GroupSpec requires exactly one
catalog-member OperationType and cannot accept a free OperationID. Supplying
an idempotency key requires an explicit stable OperationID, so a process restart
cannot accidentally conflict solely because it generated a fresh correlation ID.

Outermost `Within` resolves context/identity, requires a catalog-member
OperationType, binds and freezes the Writer's exact Execution, and allocates
one revision ID before it invokes its callback once. Nil Execution is allowed
for a manual-only standalone group; a refused/unsafe authority aborts before the
callback. It seals/canonicalizes all items and appends once using the frozen
Execution. Same-recorder nested `Within` joins only when the sealed operation,
operation ID, idempotency key, CatalogSet, ContextPolicy, retention, consequence,
and exact origin-bound frame Authority agree. It reuses the frame's one Execution
without comparing its concrete interface value and returns Joined without a receipt.
A nested error or panic discards items tagged with that frame and descendants;
sibling concurrent items survive. An outer error/panic discards everything, and
panic is re-raised unchanged. Another recorder or conflicting group refuses
before its callback. Staging is mutex-safe and bounded; canonical sorting makes
goroutine scheduling irrelevant. Staging after sealing returns ErrGroupClosed.
Within never opens or finalizes a transaction. Reserved capacity cannot be stolen
by concurrent Stage and remains outstanding until its guard reaches no-op or
staged state.

Observers run only after the store answer, are panic-isolated, and receive bounded
enums, duration, count, settlement, failure class, and non-identifying work
counters—never actor, subject, resource, action, values, query, cursor, snapshot
identity, SQL, identifiers, keys, or raw errors. A forged or failing observation
cannot change authority, store calls, budgets, evidence, or results.

### 2.7 Closed safe-error contract

```go
var (
	ErrDeclaration        error
	ErrInvalid            error
	ErrTooLarge           error
	ErrDenied             error
	ErrNotFound           error
	ErrConflict           error
	ErrUnsupported        error
	ErrWrongCatalog       error
	ErrWrongStore         error
	ErrWrongAuthority     error
	ErrTransaction        error
	ErrAdmission          error
	ErrGroupClosed        error
	ErrGroupPoisoned      error
	ErrNotWritten         error
	ErrUnconfirmed        error
	ErrCommitUnconfirmed  error
	ErrRollbackUnconfirmed error
	ErrBackend            error
	ErrClosed             error
	ErrRefused            error
	ErrBadPosition        error
	ErrStaleCatalog       error
	ErrCursor             error
	ErrFence              error
	ErrExpired            error
	ErrUnknownCatalog     error
	ErrIntegrity          error
	ErrMissingKey         error
	ErrMalformedEvidence  error
	ErrTemporalAmbiguity  error
)

type StoreOutcome uint8

const (
	Unclassified StoreOutcome = iota
	Conflict
	Missing
	Corrupt
	NotWritten
	Unconfirmed
	Closed
	BadPosition
	StaleCatalog
	Refused
)

func Failure(StoreOutcome, error) error
type CryptoOutcome uint8
const (
	CryptoUnclassified CryptoOutcome = iota
	CryptoInvalid
	CryptoMissingKey
	CryptoUnsupported
	CryptoMalformed
	CryptoBackend
	CryptoRefused
)
func CryptoFailure(CryptoOutcome, error) error
func CryptoOutcomeOf(error) (CryptoOutcome, bool)
type DenialEvidenceState uint8
const (
	DenialEvidenceNotConfigured DenialEvidenceState = iota + 1
	DenialEvidenceSuppressed
	DenialEvidenceLimiterFailed
	DenialEvidenceNotWritten
	DenialEvidenceUnconfirmed
	DenialEvidenceCommitted
)
func DenialEvidenceStateOf(error) (DenialEvidenceState, bool)
func CauseOf(error) error
```

The sentinel inventory above is exhaustive for release 1. Each value belongs to
one stable declaration, request, authorization, conflict, transaction, uncertain
write, unreadable-evidence, or backend class; `errors.Is` never crosses between
audit sentinels. Request/too-large/denied/conflict/unavailable projections carry
the existing Frostgrove `crud`/`errs` class expected by ports, while no store or
crypto cause can inject a different transport class. ErrNotFound is deliberately
non-disclosing and does not reveal whether a target exists outside the sealed
grant. Bare context cancellation/deadline remains bare only under the precedence
rules below.

Every non-cancellation public error is or owns one `*errs.Fault` while preserving
`errors.Is` for exactly its audit sentinel. The mapping is exhaustive and stable:

| audit sentinel family | errs.Kind | errs.Code |
|---|---|---|
| Declaration, Invalid | Validation | CodeCheck |
| TooLarge | TooLarge | CodeTooLarge |
| Denied | Forbidden | CodeForbidden |
| NotFound | NotFound | CodeNotFound |
| Unsupported | MethodNotAllowed | CodeMethodNotAllowed |
| Conflict, WrongCatalog, WrongStore, WrongAuthority, Transaction, Admission, GroupClosed, GroupPoisoned, StaleCatalog | Conflict | CodeConflict |
| NotWritten, Unconfirmed, CommitUnconfirmed, RollbackUnconfirmed, Backend, Closed | Retryable | CodeUnavailable |
| Refused, BadPosition, Cursor, Fence, Expired | BadRequest | CodeBadQuery |
| UnknownCatalog, Integrity, MissingKey, MalformedEvidence, TemporalAmbiguity | Internal | CodeInternal |

Declaration/request builders attach only bounded copied `errs.Violation` paths
and protocol-safe parameters; they never embed values, collaborator text, SQL,
keys, or causes. `port.KindOf`, `port.FaultOf`, HTTP, and gRPC therefore preserve
the table without special audit imports. Locale-specific MessageSource rendering
is outside audit and cannot change the sentinel, kind, code, evidence, or digest.

Unknown outcomes normalize to Unclassified. Store failure has no `Unwrap`, `Is`,
or `As`; standard traversal cannot reach its cause. Classification walks at most
64 foreign error nodes, including joined/cyclic/lying/panicking `As` chains,
without allowing a panic or nil match to escape. Returned refusals expose only
the audit sentinel and safe class through standard `errors.Is/As`; `CauseOf` is
the only raw-cause door and rendering never includes it.

Every History or Control authorization denial is an ErrDenied owner wrapper with
exactly one DenialEvidenceState when the call returns. An undeclared denial action
or nil limiter is NotConfigured; false is Suppressed; a returned limiter error is
LimiterFailed; a definitely failed evidence append is NotWritten; an uncertain
write, invalid receipt, or non-Committed settlement is Unconfirmed; and only an
independently settled receipt is Committed. A limiter panic runs cleanup, performs
zero append/grant work, and is re-raised identically, so it returns no error and
has no DenialEvidenceState. DenialEvidenceStateOf walks only the bounded safe
owner graph and returns false for foreign denials. Denial wrappers are deliberately
cause-elided: even CauseOf reveals no limiter/store error, request, receipt,
ReconcileKey, or RetryToken. The state never changes authorization or the original
non-disclosing denial sentinel.

The same owner wrapper applies to ordinary errors from ContextResolver, custom
codecs, SemanticDigester, Protector, Tokenizer, Signer, AccessAuthority,
ControlAuthority, Revealer, Verifier, cursor/fence keys, clocks, ID sources, and
catalog administration. Their messages and values never
enter Error, observer data, transport details, or logs; CauseOf is the one
explicit diagnostic door. A resolver invocation whose original context is
observably canceled at the immediate post-return check is the exception: its
value and error are discarded, no owner wrapper is built, the returned sentinel
is normalized, and CauseOf is nil. Except for the explicitly isolated Observer, a panic
from any collaborator is not guessed into an error: grouping cleanup runs and
the identical panic is re-raised.

Protector, Tokenizer, Signer, Revealer, Verifier, CursorKeys, FenceKeys, and
IdentityKeyring classify operational failures through CryptoFailure. The closed
outcomes distinguish invalid authentication, missing or retired key, unsupported
algorithm/profile, malformed input, backend outage, and policy refusal without
exposing collaborator sentinels or text. A plain or unknown collaborator error is
CryptoUnclassified and maps to the safe backend class. The kernel never infers a
key or integrity outcome from a message, concrete third-party error, or returned
empty bytes. CryptoOutcomeOf uses the same bounded panic-safe owner traversal;
CauseOf remains the only diagnostic cause door.

Append Unclassified becomes ErrUnconfirmed. Search, Inspect, Lookup,
LookupIdempotency, EntityChainHead on ExactLog or Execution, AttemptState, Inventory,
Verification, HoldState, and PlanPurge Unclassified become ErrBackend. Conflict, Missing, Corrupt,
NotWritten, Unconfirmed, Closed, BadPosition, StaleCatalog, and Refused map to
distinct audit errors.
Malformed/retired cursor or fence tokens, unknown catalog history, temporal
ambiguity, and a non-Committed disclosure-evidence result are distinct kernel
refusals. Bare cancellation
survives only for a read or when the store proves no write statement was issued;
a classified write outcome wins. The kernel never parses messages or SQLSTATE,
never probes after a write to guess, and never recovers a store panic.

Auditcrud separately maps an error returned by `crud.InAtomic` after its callback
completed successfully to opaque ErrCommitUnconfirmed. An error before or from
the callback is not relabelled when rollback is confirmed. If the callback
failed and `InAtomic` also reports rollback failure, the adapter returns opaque
ErrRollbackUnconfirmed with the original safe class and ReconcileKey retained;
neither write is retried. It uses a private callback marker rather than parsing
the joined error. A joined caller transaction has no final outcome inside
auditcrud.

When an audit append error caused an adapter-owned transaction to roll back and
rollback succeeded, auditcrud consumes the intermediate transaction recovery
token and reports the audit class plus a definite whole-unit-not-committed state;
it exposes no audit-only Retry. The application may repeat its whole idempotent
business operation. When rollback or commit is unconfirmed, only the frozen
ReconcileKey escapes.

### 2.8 Protected history and reconstruction

```go
type History struct{ value history }
type Page struct{ value page }
type RevisionResult struct{ value revisionResult }
type ItemResult struct{ value itemResult }
type Comparison[M any] struct{ value comparison[M] }
type FieldDifference[V any] struct{ value fieldDifference[V] }
type DifferenceStatus uint8
const (
	DifferenceEqual DifferenceStatus = iota + 1
	DifferenceChanged
	DifferenceIndeterminate
)
type RevisionView struct {
	Ref         RevisionRef
	Operation   OperationName
	OperationID OperationID
	ObservedAt  time.Time
	RecordedAt  time.Time
	Retention   RetentionClass
	Consequence Consequence
	Actors      []ActorReadView
	Context     []ContextFactReadView
	Items       []ItemReadView
}
type ActorReadView struct {
	Ordinal    uint8
	Kind       ActorKind
	Provenance Provenance
	Reference  ReadValueView
}
type ContextFactReadView struct{ value contextFactReadView }
func (v ContextFactReadView) Kind() ContextFactKind
func (v ContextFactReadView) Provenance() Provenance
func (v ContextFactReadView) Classification() Classification
func (v ContextFactReadView) Knowledge() FieldKnowledge
func (v ContextFactReadView) Reference() (Reference, bool)
func (v ContextFactReadView) ScopedReference() (ScopedReference, bool)
func (v ContextFactReadView) OperationID() (OperationID, bool)
func (v ContextFactReadView) Source() (Source, bool)
type ReadValueView struct {
	Field          FieldName
	Codec          CodecDescription
	Classification Classification
	Knowledge      FieldKnowledge
	Canonical      []byte
}
type ChangeReadView struct {
	Field  FieldName
	Before ReadValueView
	After  ReadValueView
}
type ItemReadView struct {
	Ordinal      uint16
	Kind         ItemKind
	Resource     Resource
	Action       Action
	EntityState  EntityStateKind
	OccurredAt   time.Time
	Outcome      Outcome
	Reason       Reason
	Subject      ReadValueView
	Target       ReadValueView
	Values       []ReadValueView
	Changes      []ChangeReadView
	CorrectionOf ItemRef
	DisputeOf    ItemRef
	Attempt      AttemptTransitionReadView
}
type AttemptTransitionReadView struct {
	Chain           AttemptChainID
	Kind            AttemptTransitionKind
	Policy          AttemptPolicyFingerprint
	Replay          AttemptReplayFingerprint
	Operation       OperationName
	OperationID     OperationID
	State           AttemptState
	Sequence        uint16
	CheckpointCount uint16
	Checkpoint      AttemptCheckpointCode
	ExpiresAt       time.Time
	Start           ItemRef
	Previous        LeafDigest
	Leaf            LeafDigest
}
type AttemptStatus struct{ value attemptStatus }
type AttemptHistory[S, C, F any] struct{ value attemptHistory[S, C, F] }
func (a *AttemptType[S, C, F]) History(*History) *AttemptHistory[S, C, F]
func (h *AttemptHistory[S, C, F]) Operation(context.Context, OperationID, Query) (AttemptStatus, error)
func (h *AttemptHistory[S, C, F]) Transitions(context.Context, Query) (Page, error)
func (h *AttemptHistory[S, C, F]) Target(context.Context, Reference, Query) (Page, error)
func (s AttemptStatus) State() AttemptState
func (s AttemptStatus) Transitions() []ItemReadView
func (s AttemptStatus) Duration() (time.Duration, bool)
func (s AttemptStatus) Evidence() Receipt

type AccessAuthority interface {
	AuthorizeAudit(context.Context, AccessRequest) (AccessDecision, error)
}

type ControlAuthority interface {
	AuthorizeAuditControl(context.Context, ControlRequest) (ControlDecision, error)
}

type ScopeSelector struct{ value scopeSelector }
type ScopeSelectorKind uint8
const (
	ScopeCurrent ScopeSelectorKind = iota + 1
	ScopeExact
)
type ScopeSelectorView struct {
	Kind      ScopeSelectorKind
	Reference ScopedReference
}
func CurrentScope() ScopeSelector
func ExactScope(ScopedReference) (ScopeSelector, error)
func (s ScopeSelector) View() ScopeSelectorView

type ProjectionSelectionKind uint8
const (
	ProjectionNone ProjectionSelectionKind = iota + 1
	ProjectionAll
	ProjectionOnly
)
type FieldProjection struct{ value fieldProjection }
type FieldProjectionView struct {
	Kind   ProjectionSelectionKind
	Fields []FieldName
}
func NoFields() FieldProjection
func AllFields() FieldProjection
func OnlyFields(...FieldName) FieldProjection
func TryOnlyFields(...FieldName) (FieldProjection, error)
func (p FieldProjection) View() FieldProjectionView

type ContextProjection struct{ value contextProjection }
type ContextProjectionView struct {
	Kind  ProjectionSelectionKind
	Facts []ContextFactKind
}
func NoContext() ContextProjection
func AllContext() ContextProjection
func OnlyContext(...ContextFactKind) ContextProjection
func TryOnlyContext(...ContextFactKind) (ContextProjection, error)
func (p ContextProjection) View() ContextProjectionView

type TimeAxis uint8
const (
	ObservedTimeAxis TimeAxis = iota + 1
	OccurredTimeAxis
	RecordedTimeAxis
)
type RangeBounds uint8
const (
	ClosedOpenRange RangeBounds = iota + 1
	ClosedClosedRange
	OpenOpenRange
	OpenClosedRange
)
type TimeWindow struct{ value timeWindow }
type TimeWindowView struct {
	Axis    TimeAxis
	Bounded bool
	From    time.Time
	Until   time.Time
	Bounds  RangeBounds
}
func AllObservedTime() TimeWindow
func AllOccurredTime() TimeWindow
func AllRecordedTime() TimeWindow
func Between(TimeAxis, time.Time, time.Time, RangeBounds) (TimeWindow, error)
func (w TimeWindow) View() TimeWindowView

type HistoryDirection uint8
const (
	NewestFirst HistoryDirection = iota + 1
	OldestFirst
)
type ChangedFieldMatch uint8
const (
	NoChangedFieldFilter ChangedFieldMatch = iota + 1
	AnyChangedField
	AllChangedFields
)
type ChangedFieldFilter struct{ value changedFieldFilter }
type ChangedFieldFilterView struct {
	Match    ChangedFieldMatch
	Fields   []FieldName
	Excluded []FieldName
}
func UnfilteredChanges() ChangedFieldFilter
func AnyChanged(...FieldName) ChangedFieldFilter
func TryAnyChanged(...FieldName) (ChangedFieldFilter, error)
func EveryChanged(...FieldName) ChangedFieldFilter
func TryEveryChanged(...FieldName) (ChangedFieldFilter, error)
func (f ChangedFieldFilter) Excluding(...FieldName) ChangedFieldFilter
func (f ChangedFieldFilter) TryExcluding(...FieldName) (ChangedFieldFilter, error)
func (f ChangedFieldFilter) View() ChangedFieldFilterView

type Query struct {
	Purpose   Purpose
	Role      Reference
	Scope     ScopeSelector
	Actions   []Action
	Fields    FieldProjection
	Context   ContextProjection
	Time      TimeWindow
	Direction HistoryDirection
	Changed   ChangedFieldFilter
	Outcomes  []Outcome
	Reasons   []Reason
	Where     SelectorSet
	Limit     uint32
	Cursor    Cursor
}

type InvestigationQuery struct {
	Resources []Resource
	Query     Query
}
type ExactAccessQuery struct {
	Resources       []Resource
	Classifications []Classification
	Query           Query
}
type ActorPosition uint8
const (
	InitiatingActor ActorPosition = iota + 1
	EffectiveActor
	AnyActorHop
)
type ActorSelector struct{ value actorSelector }
type ActorSelectorView struct {
	Position  ActorPosition
	Kind      ActorKind
	Reference Reference
}
func MatchActor(ActorPosition, ActorKind, Reference) (ActorSelector, error)
func (s ActorSelector) View() ActorSelectorView

type ContextSelector struct{ value contextSelector }
type ContextSelectorView struct {
	Kind      ContextFactKind
	Reference Reference
	Scoped    ScopedReference
	Operation OperationID
	Source    Source
}
func MatchClient(ScopedReference) (ContextSelector, error)
func MatchService(Reference) (ContextSelector, error)
func MatchDeployment(Reference) (ContextSelector, error)
func MatchCorrelation(Reference) (ContextSelector, error)
func MatchCausation(Reference) (ContextSelector, error)
func MatchTrace(Reference) (ContextSelector, error)
func MatchOperation(OperationID) (ContextSelector, error)
func MatchSource(Source) (ContextSelector, error)
func (s ContextSelector) View() ContextSelectorView

type EntityIndexSide uint8
const (
	EntityBeforeValue EntityIndexSide = iota + 1
	EntityAfterValue
	EntityEitherValue
)
type EqualitySelectorKind uint8
const (
	ActorEqualitySelector EqualitySelectorKind = iota + 1
	ContextEqualitySelector
	EventIndexEqualitySelector
	EntityIndexEqualitySelector
	AttemptTargetEqualitySelector
)
type EqualitySelector interface{ auditEqualitySelector() }
type SelectorSet struct{ value selectorSet }
type EntityIndexPredicate[M any] struct{ value entityIndexPredicate[M] }
type EventIndexPredicate[E any] struct{ value eventIndexPredicate[E] }
type EqualitySelectorView struct {
	Kind           EqualitySelectorKind
	Resource       Resource
	Action         Action
	Operation      OperationName
	AttemptPolicy  AttemptPolicyFingerprint
	Field          FieldName
	EntitySide     EntityIndexSide
	Actor          ActorSelectorView
	Context        ContextSelectorView
	Target         Reference
	Codec          CodecDescription
	Mode           StorageMode
	Classification Classification
	Canonical      [][]byte
}
type SelectorSetView struct {
	Selectors []EqualitySelectorView
}
func BeforeEquals[M, V any](EntityIndex[M, V], V) EntityIndexPredicate[M]
func TryBeforeAnyOf[M, V any](EntityIndex[M, V], ...V) (EntityIndexPredicate[M], error)
func AfterEquals[M, V any](EntityIndex[M, V], V) EntityIndexPredicate[M]
func TryAfterAnyOf[M, V any](EntityIndex[M, V], ...V) (EntityIndexPredicate[M], error)
func EitherEquals[M, V any](EntityIndex[M, V], V) EntityIndexPredicate[M]
func TryEitherAnyOf[M, V any](EntityIndex[M, V], ...V) (EntityIndexPredicate[M], error)
func EventEquals[E, V any](EventIndex[E, V], V) EventIndexPredicate[E]
func TryEventAnyOf[E, V any](EventIndex[E, V], ...V) (EventIndexPredicate[E], error)
func AttemptTargetIs[S, C, F any](*AttemptType[S, C, F], Reference) EqualitySelector
func AllOf(...EqualitySelector) SelectorSet
func TryAllOf(...EqualitySelector) (SelectorSet, error)
func (s SelectorSet) View() SelectorSetView

type EventIndexSelectorView struct {
	Resource       Resource
	Action         Action
	Field          FieldName
	Codec          CodecDescription
	Mode           StorageMode
	Classification Classification
	Canonical      [][]byte
}

type SubjectRef struct{ value subjectRef }
type SubjectRefView struct {
	Resource       Resource
	Subject        Reference
	Mode           StorageMode
	Classification Classification
}
func (s SubjectRef) View() SubjectRefView

type EvidenceCoordinateKind uint8
const (
	EvidenceSubjectCoordinate EvidenceCoordinateKind = iota + 1
	EvidenceEventTargetCoordinate
)
type EvidenceSelectorMatch uint8
const (
	EvidenceExact EvidenceSelectorMatch = iota + 1
	EvidenceAllDeclared
)
type EvidenceSelector struct{ value evidenceSelector }
type EvidenceSelectorView struct {
	Kind           EvidenceCoordinateKind
	Match          EvidenceSelectorMatch
	Resource       Resource
	Actions        []Action
	Reference      Reference
	Mode           StorageMode
	Classification Classification
}
func (s EvidenceSelector) View() EvidenceSelectorView
func (s EvidenceSelector) NarrowActions(...Action) (EvidenceSelector, error)
func (p *ResourcePolicy[M, ID]) SelectSubject(ID, ...EntityAction) (EvidenceSelector, error)
func (p *ResourcePolicy[M, ID]) SelectAllSubjects(...EntityAction) (EvidenceSelector, error)
func (e *EventType[E]) SelectTarget(Reference) (EvidenceSelector, error)
func (e *EventType[E]) SelectAllTargets() (EvidenceSelector, error)

type ExactControlAccess struct {
	Scope           ScopeSelector
	Resources       []Resource
	Actions         []Action
	Coordinates     []EvidenceSelector
	Classifications []Classification
}
type ExactControlAccessView struct {
	Scope           ScopeSelectorView
	Resources       []Resource
	Actions         []Action
	Coordinates     []EvidenceSelectorView
	Classifications []Classification
}
func (a ExactControlAccess) View() ExactControlAccessView

type NormalizedQueryView struct {
	Purpose   Purpose
	Role      Reference
	Scope     ScopeSelectorView
	Class     QueryClass
	Actions   []Action
	Fields    FieldProjectionView
	Context   ContextProjectionView
	Time      TimeWindowView
	Direction HistoryDirection
	Changed   ChangedFieldFilterView
	Outcomes  []Outcome
	Reasons   []Reason
	Where     SelectorSetView
	Limit     uint32
}

type QueryClass uint8
const (
	SubjectHistoryQuery QueryClass = iota + 1
	ResourceHistoryQuery
	EventTargetHistoryQuery
	EventTypeHistoryQuery
	OperationTypeHistoryQuery
	OperationInstanceHistoryQuery
	AttemptHistoryQuery
	ActorHistoryQuery
	ContextHistoryQuery
	EventIndexHistoryQuery
	EntityIndexHistoryQuery
	AttemptTypeHistoryQuery
	AttemptTargetHistoryQuery
	ExactRevisionHistoryQuery
	ExactItemHistoryQuery
	RevisionNeighborsQuery
	ReconstructionQuery
	ComparisonQuery
)

type ReconstructionBoundaryKind uint8
const (
	ReconstructionAtRevision ReconstructionBoundaryKind = iota + 1
	ReconstructionAtObservedTime
	ReconstructionAtHead
)
type ReconstructionBoundaryRequestView struct {
	Kind       ReconstructionBoundaryKind
	Revision   RevisionRef
	ObservedAt time.Time
}
type ReconstructionBudgetView struct {
	MaxRevisions uint32
	MaxPages     uint32
	MaxBytes     uint64
}
type ReconstructionRequestView struct {
	Boundary ReconstructionBoundaryRequestView
	Budget   ReconstructionBudgetView
}

type ComparisonRequestView struct {
	Before ReconstructionBoundaryRequestView
	After  ReconstructionBoundaryRequestView
	Budget ReconstructionBudgetView
}

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
	Kind           AccessTargetKind
	Resource       Resource
	Action         Action
	Actions        []Action
	Subject        Reference
	SubjectMode    StorageMode
	SubjectClass   Classification
	Target         Reference
	TargetMode     StorageMode
	TargetClass    Classification
	OperationName  OperationName
	Operation      OperationID
	AttemptPolicy  AttemptPolicyFingerprint
	Resources      []Resource
	Classifications []Classification
	Actor          ActorSelectorView
	Context        ContextSelectorView
	EventIndex     EventIndexSelectorView
	Selectors      SelectorSetView
	Revision       RevisionRef
	Item           ItemRef
}

type ControlTargetKind uint8
const (
	ControlItemTarget ControlTargetKind = iota + 1
	ControlInventoryTarget
	ControlVerificationTarget
	ControlFenceTarget
	ControlHoldTarget
)
type ControlTargetView struct {
	Kind            ControlTargetKind
	Item            ItemRef
	Selection       ControlSelectionView
	SelectionDigest SelectionDigest
	Access          ExactControlAccessView
	Fence           FenceDigest
	Correction      CorrectionProposalView
	Hold            HoldAuthorizationView
}

type HoldAuthorizationView struct {
	Command          HoldCommandKind
	Target           RevisionRef
	Hold             HoldIDCommitment
	MatterPresent    bool
	Matter           HoldMatterCommitment
}

type CorrectionProposalFieldView struct {
	Field          FieldName
	Classification Classification
	Mode           StorageMode
}
type CorrectionProposalView struct {
	Digest   CorrectionProposalDigest
	Catalog  CatalogRef
	Resource Resource
	Action   Action
	Target   Reference
	TargetMode StorageMode
	TargetClass Classification
	Fields   []CorrectionProposalFieldView
}

type ControlSelectionView struct {
	Scope           ScopeSelectorView
	Resources       []Resource
	Actions         []Action
	Coordinates     []EvidenceSelectorView
	Retentions      []RetentionClass
	Classifications []Classification
	Revisions       []RevisionRef
	Time            TimeWindowView
	AsOf           time.Time
	Limit           uint32
	MaxCohorts      uint32
	MaxCandidates   uint32
	MaxBytes        uint64
}

type AccessIntent uint8
const (
	HistoryDisclosureAccess AccessIntent = iota + 1
	AttemptResumeAccess
	AttemptResolveAccess
	AttemptAbandonAccess
)
type AttemptAccessRequestView struct {
	Catalogs       []CatalogRef
	Resource       Resource
	Purpose        Purpose
	Role           Reference
	Operation      OperationName
	Policy         AttemptPolicyFingerprint
	Replay         AttemptReplayFingerprint
	OperationID    OperationID
	TargetPresent  bool
	Target         Reference
	TargetMode     StorageMode
	TargetClass    Classification
	Scope          ScopeSelectorView
	Owner          AttemptOwnerCommitment
	MaxTransitions uint16
	MaxBytes       uint64
}
type AttemptAccessGrantSpec struct {
	Intents        []AccessIntent
	Catalogs       []CatalogRef
	Resource       Resource
	Purpose        Purpose
	Role           Reference
	Operation      OperationName
	Policy         AttemptPolicyFingerprint
	Replay         AttemptReplayFingerprint
	TargetPresent  bool
	Target         Reference
	TargetMode     StorageMode
	TargetClass    Classification
	Scope          ScopeSelectorView
	Owner          AttemptOwnerCommitment
	ExpiresAt      time.Time
	MaxTransitions uint16
	MaxBytes       uint64
}
type AccessRequest struct{ value accessRequest }
type AccessRequestView struct {
	Intent           AccessIntent
	Requester        ContextView
	RequesterDigest  RequesterCommitment
	RequesterAliases IdentityCommitmentSet
	Target           AccessTargetView
	Query            NormalizedQueryView
	Reconstruction   ReconstructionRequestView
	Comparison       ComparisonRequestView
	Attempt          AttemptAccessRequestView
	Continuation     bool
	InputContinuation AccessContinuationDigest
	OriginRequest    AccessRequestDigest
	OriginGrant      AccessGrantDigest
}
type AccessVerdict uint8
const (
	AccessAllowed AccessVerdict = iota + 1
	AccessDenied
)
type AccessGrantSpec struct {
	Roles           []Reference
	Scopes          []ScopedReference
	Catalogs        []CatalogRef
	Resources       []Resource
	Actions         []Action
	Fields          []FieldName
	Context         []ContextFactKind
	Classifications []Classification
	Time            TimeWindowView
	Direction       HistoryDirection
	Changed         ChangedFieldFilterView
	Outcomes        []Outcome
	Reasons         []Reason
	ExpiresAt       time.Time
	MaxRevisions    uint32
	MaxPages        uint32
	MaxBytes        uint64
	Reconstruction  ReconstructionBoundaryRequestView
	ComparisonBefore ReconstructionBoundaryRequestView
	ComparisonAfter  ReconstructionBoundaryRequestView
	Attempt           AttemptAccessGrantSpec
}
type AccessRequestDigestInput struct {
	Intent           AccessIntent
	CatalogSet       CatalogSetDigest
	Requester        RequesterCommitment
	RequesterAliases IdentityCommitmentSet
	Target           AccessTargetView
	Query            NormalizedQueryView
	Reconstruction   ReconstructionRequestView
	Comparison       ComparisonRequestView
	Attempt          AttemptAccessRequestView
	Continuation     bool
	InputContinuation AccessContinuationDigest
	OriginRequest    AccessRequestDigest
	OriginGrant      AccessGrantDigest
}
type AccessGrantDigestInput struct {
	Request AccessRequestDigest
	Grant   AccessGrantSpec
}
type AccessResultKind uint8
const (
	AccessPageResult AccessResultKind = iota + 1
	AccessReconstructionResult
	AccessComparisonResult
	AccessExactRevisionResult
	AccessExactItemResult
	AccessRevisionNeighborsResult
	AccessAttemptAuthorizationResult
)
type AccessRevisionDigestView struct {
	Revision  RevisionRef
	RecordedAt time.Time
	Envelope  EnvelopeDigest
	Integrity IntegrityDigest
	Items     []ItemRef
}
type AccessPageDigestView struct {
	Revisions          []AccessRevisionDigestView
	Count              uint32
	Truncated          bool
	ContinuationPresent bool
	Continuation       AccessContinuationDigest
}
type ReconstructionBoundaryDigestView struct {
	Present             bool
	Revision            RevisionRef
	ObservedAt          time.Time
	ObservedThrough     time.Time
	AssertedHeadPresent bool
	AssertedHead        RevisionRef
	AssertedHeadLeaf    LeafDigest
}
type ReconstructionFieldDigestView struct {
	Resource  Resource
	Field     FieldName
	Knowledge FieldKnowledge
}
type AccessReconstructionDigestView struct {
	Boundary    ReconstructionBoundaryDigestView
	State       EntityLifecycleState
	Fields      []ReconstructionFieldDigestView
	Contributors []AccessRevisionDigestView
}
type AccessComparisonDigestView struct {
	Before AccessReconstructionDigestView
	After  AccessReconstructionDigestView
	Fields []ComparisonFieldDigestView
}
type ComparisonFieldDigestView struct {
	Resource Resource
	Field    FieldName
	Status   DifferenceStatus
}
type AccessExactItemDigestView struct {
	Item       ItemRef
	Containing AccessRevisionDigestView
}
type AccessNeighborDigestView struct {
	Knowledge NeighborKnowledge
	Revision  AccessRevisionDigestView
}
type AccessNeighborhoodDigestView struct {
	Center       AccessRevisionDigestView
	Previous     AccessNeighborDigestView
	Next         AccessNeighborDigestView
	AssertedHead ReconstructionBoundaryDigestView
}
type AccessAttemptDigestView struct {
	Chain       AttemptChainID
	Policy      AttemptPolicyFingerprint
	Operation   OperationName
	OperationID OperationID
	TargetPresent bool
	Target      AttemptTargetCommitment
	State       AttemptState
	Sequence    uint16
	Start       ItemRef
	Head        ItemRef
	Leaf        LeafDigest
	Transitions []ItemRef
}
type AccessResultDigestInput struct {
	Kind           AccessResultKind
	Request        AccessRequestDigest
	Grant          AccessGrantDigest
	Page           AccessPageDigestView
	Reconstruction AccessReconstructionDigestView
	Comparison     AccessComparisonDigestView
	Revision       AccessRevisionDigestView
	Item           AccessExactItemDigestView
	Neighbors      AccessNeighborhoodDigestView
	Attempt        AccessAttemptDigestView
}
type AccessContinuationDigestInput struct {
	Cursor  Cursor
	Request AccessRequestDigest
	Grant   AccessGrantDigest
}
type AccessDecision struct{ value accessDecision }
type AccessDecisionView struct {
	Verdict AccessVerdict
	Reason  Reason
	Grant   AccessGrantSpec
}
func (r AccessRequest) View() AccessRequestView
func AllowAccess(AccessRequest, AccessGrantSpec) (AccessDecision, error)
func DenyAccess(AccessRequest, Reason) (AccessDecision, error)
func (d AccessDecision) View() AccessDecisionView

type ControlRequest struct{ value controlRequest }
type ControlRequestView struct {
	Requester        ContextView
	RequesterDigest  RequesterCommitment
	RequesterAliases IdentityCommitmentSet
	Action           ControlAction
	Purpose          Purpose
	Role             Reference
	Reason           Reason
	Target           ControlTargetView
	Continuation     bool
	InputContinuation ControlContinuationDigest
	OriginRequest    ControlRequestDigest
	OriginGrant      ControlGrantDigest
	HoldRequest      HoldRequestDigest
}
type ControlGrantSpec struct {
	Roles           []Reference
	Scopes          []ScopedReference
	Catalogs        []CatalogRef
	Resources       []Resource
	RecordActions   []Action
	Coordinates     []EvidenceSelector
	Actions         []ControlAction
	Retentions      []RetentionClass
	Classifications []Classification
	ExpiresAt       time.Time
	MaxRevisions    uint32
	MaxCohorts      uint32
	MaxCandidates   uint32
	MaxBytes        uint64
	Correction      CorrectionProposalDigest
	Hold            HoldRequestDigest
}
type ControlRequestDigestInput struct {
	CatalogSet       CatalogSetDigest
	Requester        RequesterCommitment
	RequesterAliases IdentityCommitmentSet
	Action           ControlAction
	Purpose          Purpose
	Role             Reference
	Reason           Reason
	Target           ControlTargetView
	Continuation     bool
	InputContinuation ControlContinuationDigest
	OriginRequest    ControlRequestDigest
	OriginGrant      ControlGrantDigest
	HoldRequest      HoldRequestDigest
}
type ControlGrantDigestInput struct {
	Request ControlRequestDigest
	Grant   ControlGrantSpec
}
type SelectionDigestInput struct {
	CatalogSet CatalogSetDigest
	Action     ControlAction
	Selection  ControlSelectionView
}
type FenceCandidateDigestView struct {
	Cohort          RetentionCohortID
	Revision        RevisionRef
	Authorization   RevisionAuthorizationSummaryView
	Retention       RetentionClass
	Integrity       IntegrityDigest
	RetentionBasis  RetentionBasisDigest
	EligibleAt      time.Time
	ActiveHoldCount uint32
	HoldEpoch       uint64
	ActiveHoldSet   HoldSetDigest
	HoldTransitions []RevisionRef
}
type ControlCursorClaimView struct {
	Format              uint16
	Log                 LogID
	Backing             BackingID
	Catalog             CatalogRef
	CatalogSet          CatalogSetDigest
	Requester           RequesterCommitment
	RequesterAliases    IdentityCommitmentSet
	Action              ControlAction
	Purpose             Purpose
	Role                Reference
	Request             ControlRequestDigest
	Grant               ControlGrantDigest
	Selection           SelectionDigest
	SelectionView       ControlSelectionView
	OriginalGrant       ControlGrantSpec
	PriorEffectiveGrant ControlGrantSpec
	AsOf                time.Time
	CohortRevisions     uint32
	CohortBytes         uint64
	Progress            SearchProgressView
	Snapshot            []byte
	SnapshotExpiry      time.Time
	After               StorePosition
	IssuedAt            time.Time
	ExpiresAt           time.Time
}
type InventoryFenceClaimView struct {
	Format     uint16
	Log        LogID
	Backing    BackingID
	Catalog    CatalogRef
	CatalogSet CatalogSetDigest
	Requester  RequesterCommitment
	RequesterAliases IdentityCommitmentSet
	Action     ControlAction
	Purpose    Purpose
	Role       Reference
	Request    ControlRequestDigest
	Grant      ControlGrantDigest
	Selection  SelectionDigest
	SelectionView ControlSelectionView
	OriginalGrant ControlGrantSpec
	PriorEffectiveGrant ControlGrantSpec
	AsOf       time.Time
	IssuedAt   time.Time
	ExpiresAt  time.Time
	Snapshot   []byte
	SnapshotExpiry time.Time
	Cohorts    []RetentionCohortView
	Guards     []AttemptRetentionGuardView
	Candidates []FenceCandidateDigestView
}
type FenceDigestInput struct {
	Fence InventoryFence
	Claim InventoryFenceClaimView
}
type ControlResultKind uint8
const (
	ControlMutationResult ControlResultKind = iota + 1
	ControlInventoryResult
	ControlVerificationResult
	ControlPurgePlanResult
)
type HoldProjectionHeadKind uint8
const (
	HoldProjectionNoHead HoldProjectionHeadKind = iota + 1
	HoldProjectionPriorHead
	HoldProjectionEnclosingRevision
)
type HoldProjectionDigestView struct {
	Revision   RevisionRef
	Membership HoldMembershipState
	Count      uint32
	Epoch      uint64
	ActiveSet  HoldSetDigest
	HeadKind   HoldProjectionHeadKind
	PriorHead  RevisionRef
}
type ControlMutationDigestView struct {
	EnclosingRevision bool
	HoldPresent       bool
	HoldDisposition HoldTransitionDisposition
	HoldProjection  HoldProjectionDigestView
}
type ControlInventoryDigestView struct {
	Cohorts             []RetentionCohortView
	Revisions           []RevisionRef
	AsOf                time.Time
	Truncated           bool
	ContinuationPresent bool
	Continuation        ControlContinuationDigest
	FencePresent        bool
	Fence               FenceDigest
}
type ControlVerificationDigestEntry struct {
	Revision RevisionRef
	Status   VerificationStatus
}
type ControlVerificationDigestView struct {
	Results             []ControlVerificationDigestEntry
	Complete            bool
	ContinuationPresent bool
	Continuation        ControlContinuationDigest
}
type ControlPurgeDigestView struct {
	Cohorts   []RetentionCohortView
	Revisions []RevisionRef
	AsOf      time.Time
	Fence     FenceDigest
}
type ControlResultDigestInput struct {
	Kind         ControlResultKind
	Request      ControlRequestDigest
	Grant        ControlGrantDigest
	Mutation     ControlMutationDigestView
	Inventory    ControlInventoryDigestView
	Verification ControlVerificationDigestView
	Purge        ControlPurgeDigestView
}
type ControlContinuationDigestInput struct {
	Cursor  ControlCursor
	Claim   ControlCursorClaimView
	Request ControlRequestDigest
	Grant   ControlGrantDigest
}
type HoldRequestDigestInput struct {
	CatalogSet       CatalogSetDigest
	Requester        RequesterCommitment
	RequesterAliases IdentityCommitmentSet
	Action           ControlAction
	Purpose          Purpose
	Role             Reference
	Target           HoldAuthorizationView
	Continuation     bool
}
type DenialRequestKind uint8
const (
	AccessDenialRequest DenialRequestKind = iota + 1
	ControlDenialRequest
)
type DenialRequestDigestInput struct {
	Kind             DenialRequestKind
	CatalogSet       CatalogSetDigest
	Requester        RequesterCommitment
	RequesterAliases IdentityCommitmentSet
	Access           AccessRequestDigest
	Control          ControlRequestDigest
	ControlAction    ControlAction
	ControlTarget    ControlTargetView
	Purpose          Purpose
	Role             Reference
	Reason           Reason
	Continuation     bool
}
type ControlDecision struct{ value controlDecision }
type ControlDecisionView struct {
	Verdict AccessVerdict
	Reason  Reason
	Grant   ControlGrantSpec
}
func (r ControlRequest) View() ControlRequestView
func (r ControlRequest) RequestedCoordinates() []EvidenceSelector
func AllowControl(ControlRequest, ControlGrantSpec) (ControlDecision, error)
func DenyControl(ControlRequest, Reason) (ControlDecision, error)
func (d ControlDecision) View() ControlDecisionView

type DenialAttempt struct{ value denialAttempt }
type DenialAttemptView struct {
	Requester        RequesterCommitment
	RequesterAliases IdentityCommitmentSet
	Action           ControlAction
	Purpose          Purpose
	Request          DenialRequestDigest
}
type DenialLimiter interface {
	AdmitAuditDenial(context.Context, DenialAttempt) (bool, error)
}
func (r DenialAttempt) View() DenialAttemptView

type Revealer interface {
	Descriptions() []ProtectionDescription
	Reveal(context.Context, RevealRequest) ([]byte, error)
}

type Verifier interface {
	Descriptions() []SignatureDescription
	Verify(context.Context, IntegrityDigest, Seal) error
}

type Cursor struct{ value cursor }
type CursorSealRequest struct{ value cursorSealRequest }
type CursorOpenRequest struct{ value cursorOpenRequest }
type CursorEnvelope struct{ value cursorEnvelope }

type CursorKeys interface {
	Descriptions() []CursorKeyDescription
	SealCursor(context.Context, CursorSealRequest) (CursorEnvelope, error)
	OpenCursor(context.Context, CursorOpenRequest) ([]byte, error)
}

type CursorKeyDescription struct {
	Algorithm string
	Profile   string
	KeyID     string
	Primary   bool
}

func (r CursorSealRequest) Plaintext() []byte
func (r CursorSealRequest) AAD() []byte
func (r CursorOpenRequest) Envelope() CursorEnvelope
func (r CursorOpenRequest) AAD() []byte
func NewCursorEnvelope(string, string, string, []byte, []byte) (CursorEnvelope, error)
func (e CursorEnvelope) Algorithm() string
func (e CursorEnvelope) Profile() string
func (e CursorEnvelope) KeyID() string
func (e CursorEnvelope) Nonce() []byte
func (e CursorEnvelope) Ciphertext() []byte
func ParseCursor([]byte) (Cursor, error)
func (c Cursor) Bytes() []byte

type HistoryConfig struct {
	Recorder       *Recorder
	Log            Log
	Exact          ExactLog
	Attempts       AttemptLog
	Access         AccessAuthority
	Denials        DenialLimiter
	Cursors        CursorKeys
	CursorLifetime time.Duration
	Revealer       Revealer
	Verifier       Verifier
}

func NewHistory(HistoryConfig) (*History, error)
func (h *History) Subject(context.Context, SubjectRef, Query) (Page, error)
func (h *History) Target(context.Context, EventTargetRef, Query) (Page, error)
func (h *History) Actor(context.Context, ActorSelector, InvestigationQuery) (Page, error)
func (h *History) Context(context.Context, ContextSelector, InvestigationQuery) (Page, error)
func (h *History) Revision(context.Context, RevisionRef, ExactAccessQuery) (RevisionResult, error)
func (h *History) Item(context.Context, ItemRef, ExactAccessQuery) (ItemResult, error)
func (p *ResourcePolicy[M, ID]) SubjectRef(ID) (SubjectRef, error)
func (h *ResourceHistory[M, ID]) Subject(context.Context, ID, Query) (Page, error)
func (h *ResourceHistory[M, ID]) Revisions(context.Context, Query) (Page, error)
type EventTargetRef struct{ value eventTargetRef }
type EventHistory[E any] struct{ value eventHistory[E] }
type OperationHistory struct{ value operationHistory }
func (e *EventType[E]) TargetRef(Reference) (EventTargetRef, error)
func (e *EventType[E]) History(*History) *EventHistory[E]
func (h *EventHistory[E]) Target(context.Context, Reference, Query) (Page, error)
func (h *EventHistory[E]) Events(context.Context, Query) (Page, error)
func ByEventIndex[E, V any](context.Context, *EventHistory[E], EventIndex[E, V], V, Query) (Page, error)
func ByEventIndexMatch[E any](context.Context, *EventHistory[E], EventIndexPredicate[E], Query) (Page, error)
func ByEntityIndex[M any, ID comparable](context.Context, *ResourceHistory[M, ID], EntityIndexPredicate[M], Query) (Page, error)
func (o *OperationType) History(*History) *OperationHistory
func (h *OperationHistory) Operation(context.Context, OperationID, Query) (Page, error)
func (h *OperationHistory) Revisions(context.Context, Query) (Page, error)
func (p Page) Revisions() []RevisionView
func (p Page) Evidence() Receipt
func (p Page) Cursor() Cursor
func (p Page) HasMore() bool
func (r RevisionResult) Revision() RevisionView
func (r RevisionResult) Evidence() Receipt
func (r ItemResult) Item() ItemReadView
func (r ItemResult) Evidence() Receipt

type Boundary struct{ value boundaryValue }
type ResolvedBoundary struct{ value resolvedBoundary }
func AtRevisionAfter(RevisionRef) Boundary
func AtObservedTime(time.Time) Boundary
func AtHead() Boundary
func (b ResolvedBoundary) Revision() RevisionRef
func (b ResolvedBoundary) ObservedAt() time.Time
func (b ResolvedBoundary) ObservedThrough() time.Time
func (b ResolvedBoundary) AssertedHead() (RevisionRef, LeafDigest, bool)

type ReconstructionBudget struct {
	MaxRevisions uint32
	MaxPages     uint32
	MaxBytes     uint64
}

type StateRequest struct {
	Query  Query
	Budget ReconstructionBudget
}

type FieldKnowledge uint8
const (
	FieldKnown FieldKnowledge = iota + 1
	FieldAbsent
	FieldUnobserved
	FieldRedacted
	FieldTokenized
	FieldDestroyed
	FieldMissingKey
	FieldUnknownCodec
	FieldGap
	FieldBudgetExceeded
	FieldUnprojected
)

type EntityLifecycleState uint8
const (
	EntityPresent EntityLifecycleState = iota + 1
	EntitySoftDeletedState
	EntityHardDeletedState
)
type NeighborKnowledge uint8
const (
	NeighborPresent NeighborKnowledge = iota + 1
	NeighborChainBoundary
	NeighborUnavailable
)
type RevisionNeighbor struct{ value revisionNeighbor }
type RevisionNeighborhood struct{ value revisionNeighborhood }
type NeighborRequest struct {
	Query  Query
	Budget ReconstructionBudget
}
func (n RevisionNeighbor) Knowledge() NeighborKnowledge
func (n RevisionNeighbor) Revision() (RevisionView, bool)
func (n RevisionNeighborhood) Center() RevisionView
func (n RevisionNeighborhood) Previous() RevisionNeighbor
func (n RevisionNeighborhood) Next() RevisionNeighbor
func (n RevisionNeighborhood) Evidence() Receipt

type Projection[M any] struct{ fields map[fieldIdentity]projectedField }
type ProjectedValue[V any] struct{ value projectedValue }
func Projected[M, V any](Projection[M], ReconstructField[M, V]) ProjectedValue[V]
func (p Projection[M]) Boundary() (ResolvedBoundary, bool)
func (p Projection[M]) State() (EntityLifecycleState, bool)
func (p Projection[M]) Evidence() Receipt
func (v ProjectedValue[V]) Knowledge() FieldKnowledge
func (v ProjectedValue[V]) Get() (V, bool)
func (h *ResourceHistory[M, ID]) State(context.Context, ID, Boundary, StateRequest) (Projection[M], error)
func (h *ResourceHistory[M, ID]) Compare(context.Context, ID, Boundary, Boundary, StateRequest) (Comparison[M], error)
func (h *ResourceHistory[M, ID]) Neighbors(context.Context, ID, RevisionRef, NeighborRequest) (RevisionNeighborhood, error)
func (c Comparison[M]) Before() Projection[M]
func (c Comparison[M]) After() Projection[M]
func (c Comparison[M]) Evidence() Receipt
func Compared[M, V any](Comparison[M], ReconstructField[M, V]) FieldDifference[V]
func (d FieldDifference[V]) Before() ProjectedValue[V]
func (d FieldDifference[V]) After() ProjectedValue[V]
func (d FieldDifference[V]) Status() DifferenceStatus
func (d FieldDifference[V]) Changed() (bool, bool)
```

AccessRequest is a strict intent union. HistoryDisclosureAccess requires the
ordinary Target/Query branch and a zero Attempt branch. AttemptResumeAccess,
AttemptResolveAccess, and AttemptAbandonAccess require one exact
AttemptAccessRequestView and zero Target/Query/Reconstruction/Comparison/
continuation arms. Their AccessGrantSpec likewise has only the bounded Attempt
branch plus the origin request's role; ordinary projection ceilings are zero.
AllowAccess is origin-bound to the exact intent, so a disclosure decision cannot
mint a continuation capability and a grant for one attempt action cannot grant
another. The no-disclosure branch performs equality-narrowed StoreQuery/
ExactQuery reads internally, authenticates ciphertext and metadata without
Reveal, returns no Page/Revision/Item value, and commits one
AccessAttemptAuthorizationResult whose digest contains only stable chain/status
coordinates and transition ItemRefs. Every other AccessResult arm is zero. Its
successful evidence action is the catalog-declared
AttemptContinuationAuthorized, not HistoryRead; a denied intent uses
AttemptAccessDenied and its closed reason set after the same bounded denial
limiter. Neither evidence item recursively requires another continuation grant.

AttemptHistory.Operation never returns a partial AttemptStatus. It first resolves
the stable chain, authorizes the exact operation, reads AttemptState, and
internally tiles as many bounded ExactLog batches as needed to close every signed
transition up to MaxAttemptTransitions. Query projections and predicates control
only which already-authorized values appear in Transitions; they cannot remove a
member from integrity closure or change State or Duration. If the complete chain
or every matching disclosed transition cannot fit the request, current grant,
and hard count/byte ceilings, the call returns a typed refusal with no status,
evidence, or cursor. A successful result has one final state and one committed
evidence receipt. Duration is present only for a verified terminal whose terminal
ObservedAt is not before the verified Started ObservedAt; Open, Uncertain, or
regressing time returns `(0, false)`.

AttemptHistory.Transitions is a separately authorized, paged, type-wide
investigation over immutable transition revisions; Target is the same bounded
page class narrowed by the declaration's present searchable start target. These
doors never mint AttemptRun and never imply fleet-wide reaping. Target on a
NoAttemptTarget declaration refuses before authority, keyring, or store I/O.
Operation remains the only exact complete-status door and never accepts a cursor;
type-wide or target pages may contain fragments and therefore expose no aggregate
AttemptState or Duration. Their distinct query classes, access targets, cursor
claims, and evidence results prevent a fragment from impersonating exact status.

`ResourcePolicy.History(history).Subject(ctx, typedID, query)` is the typed entity
path; `.Revisions(ctx, query)` is its bounded declaration-wide investigation door.
`EventType.History(history).Target(ctx, reference, query)` is the matching typed
event path, and `OperationType.History(history).Revisions(ctx, query)` is the
bounded operation-type-wide door. SubjectRef and EventTargetRef each seal only an exact declaration
identity and one copied bounded logical subject/target; neither has a keyring or
retained-catalog authority. History derives the active and every required retained
query coordinate through its configured CatalogSet, IdentityKeyring, and Tokenizer
before Log I/O. The
authority view receives the logical target, Resource, Action, Classification, and
StorageMode but no internal store token. The typed shell fixes Resource and Action;
for an event target this is one exact Action, while an entity subject fixes its
Resource plus the declaration-derived finite entity-action set. Paged event
Target or TargetRef on NoEventTarget, and attempt Target on
NoAttemptTarget, refuse as unsupported declaration capabilities before requester
resolution, authorization, key derivation, or store I/O; fabricated sentinel
references are never accepted.

EntityIndex is an explicit allowlisted entity field with exactly one plaintext,
token, or indexed-protected equality mode. BeforeEquals, AfterEquals, and
EitherEquals bind the transition side; their AnyOf siblings permit one nonempty,
duplicate-free set of at most MaxSelectorAlternatives values for that same
coordinate. Event index matches have the same bounded single-coordinate AnyOf.
The typed codec runs once per logical query value before authority and produces
canonical copied alternatives; History derives all current/retained token and
commitment aliases only after authorization and never sends plaintext to the
store for a non-plaintext mode. Before and After compare the authenticated
record-era change side; Either is their union within one coordinate, not a broad
OR node. A missing side does not equal a zero value.

Query.Where is zero or one canonical SelectorSet. AllOf accepts only origin-sealed
ActorSelector, ContextSelector, EventIndexPredicate, EntityIndexPredicate, and
AttemptTargetIs values and combines every coordinate with logical AND. AnyOf is
the only OR and exists solely among values of one coordinate. At most
MaxQuerySelectors coordinates are accepted. Duplicate coordinates, contradictory
fixed declarations, a foreign CatalogSet/resource/action/model, a selector
incompatible with the bound history door, an unsearchable mode, or an unretained
codec/key alias refuses before authority or store I/O. The primary typed history
door is itself a mandatory coordinate and cannot be removed by Where. The access
request, exact grant, cursor, StoreQuery, access result, and denial bind the same
canonical selector set; authority may deny but cannot drop or rewrite a conjunct.
ActorSelector and ContextSelector directly satisfy EqualitySelector, so common
intersections remain declarative without a generic path, callback, range, SQL,
or analytics language.

Empty
Query.Actions means that exact target ceiling. A nonempty event-target set may
contain only its singleton; a nonempty subject set may be any duplicate-free
subset of its declaration ceiling. Neither can widen or substitute an action. A
paged Query requires nonempty Purpose and Role, a valid
Scope, and `1 <= Limit <= MaxPageRevisions`; reconstruction, comparison, and
exact inspection require zero Limit and Cursor because their exact targets and
combined budgets bound work. To keep the common declaration readable while
making its disclosure default least-privilege, zero Fields normalizes to
NoFields, zero Context to NoContext, zero Time to AllObservedTime, zero Direction
to NewestFirst, and zero Changed to UnfilteredChanges. The normalized views carry
the corresponding explicit nonzero enum values; a malformed nonzero view never
falls back to a default. Explicit AllFields and AllContext mean every current
policy-admitted stable declaration selected by this query, not arbitrary stored
columns or facts. OnlyFields and OnlyContext require a nonempty duplicate-free
canonical set. AnyChanged and EveryChanged require a nonempty duplicate-free
positive set; Excluding requires a nonempty duplicate-free negative set disjoint
from the positive set and may be applied to UnfilteredChanges for a negative-only
predicate. Every member must exist in every applicable current declaration.
Field projection decides what may be disclosed; a changed-
field predicate decides which revisions qualify and cannot smuggle an
unprojected value.

TimeWindow is normalized to UTC with monotonic data removed. An all-time window
has no endpoints or bounds; a bounded window has two nonzero endpoints, `from <=
until`, one explicit open/closed shape, and span no greater than MaxQueryWindow.
ObservedTimeAxis uses the signed header ObservedAt. RecordedTimeAxis uses the
adapter-owned unsigned operational RecordedAt and therefore makes no authenticity,
causality, or retention claim. OccurredTimeAxis is legal only for an event-type,
event-target, or event-index query and matches individual event items by their
signed OccurredAt. A returned revision contains only event items that matched the
sealed event declaration and window plus admitted revision metadata; its stable
occurred-time sort key is the earliest matching item time. Subject, operation,
actor, context, exact, reconstruction, and comparison queries reject
OccurredTimeAxis. Range membership follows the named endpoint inclusivity
exactly. Direction orders the selected axis, then RevisionID bytewise, and its
inverse reverses both keys. Equal timestamps are consequently stable across
pages and restarts.

Changed-field matching is legal only on a typed subject or resource history. It evaluates
the authenticated entity item's declared Changes names: AnyChanged requires at
least one requested name, EveryChanged requires all requested names, and every
Excluded name must be absent from the same item. Create, baseline, delete, and
restore have no special inference: they match only their authenticated honest
Changes set, never every full-state Value. It does not inspect decoded values,
and an empty or redacted value is still a change when the signed field name says
so. Outcome and reason filters are
legal only for event-type, event-target, or event-index history; each requested
code must belong to that exact current event declaration. A match is against the
record-era manifest member and exact action. An absent optional reason matches no
reason code. Empty Outcomes and Reasons mean no predicate; duplicates, unknown
codes, or a code from another action refuse before authority or Log I/O.

After zero normalization, the closed Query-class matrix is:

| Query class | Class-specific non-default members | Required external ceiling |
|---|---|---|
| subject/resource page | Actions, Fields, Context, observed/recorded Time, Direction, Changed | sealed SubjectRef or ResourcePolicy declaration |
| event target/type/index page | Actions, Fields, Context, any Time axis, Direction, Outcomes, Reasons | sealed declaration and optional typed equality coordinate |
| operation type/instance page | Actions, Fields, Context, observed/recorded Time, Direction | sealed OperationType and optional exact OperationID |
| actor/context page | Actions, Fields, Context, observed/recorded Time, Direction | nonempty Investigation Resources and Actions plus sealed selector |
| exact revision/item | Actions, Fields, and Context | nonempty ExactAccess Resources, Actions, and Classifications |
| reconstruction/comparison | Fields | typed resource/subject, boundary arm, and nonzero combined budget |

Purpose, Role, and Scope are required in every row. Page rows require Limit and
may carry Cursor; exact/reconstruction/comparison require both zero. Members not
listed for a row must equal their normalized safe default: NoFields/NoContext
where that projection is not listed, AllObservedTime, NewestFirst,
UnfilteredChanges, and empty code/action sets. In particular State and Compare
cannot time-filter or action-filter away a predecessor, request context that their
result has no place to carry, or reset work through a cursor. Exact inspection
cannot pretend direction or time changes one exact target. Any illegal non-default
member refuses before authority, tokenization, Log, or ExactLog I/O.

AccessTargetView is a strict union and every unused member is zero. Subject uses
Resource, its sealed subject metadata, and the declaration-derived Actions
ceiling. Resource-wide history uses only Resource plus the complete declaration-
derived Actions and Classifications ceilings. Event target/type/index uses
Resource and its one fixed Action; an index also uses EventIndex. Operation-type
history uses OperationName plus the complete declaration-derived Resources,
Actions, and Classifications ceilings and a zero OperationID; an exact operation
uses the same members plus its nonzero OperationID. Actor and
context investigation uses its selector plus the caller-requested current-
declared Resources and Actions. Exact revision/item uses its exact reference plus
the caller-requested Resources, Actions, and Classifications. For every multi-
action target, normalized Query.Actions must be a subset of the target ceiling;
for exact access it is nonempty and byte-equal to Target.Actions. The target,
request, grant, ExactQuery/StoreQuery, cursor, and evidence digests preserve the
same operation name and finite action ceiling; a singular Action can never stand
in for a multi-action revision or operation.

ExactControlAccess is the common least-privilege ceiling for correction, dispute,
hold, release, and explicit or selected verification. It requires one valid
ScopeSelector plus nonempty duplicate-free current-declared Resources, Actions,
and Classifications. Selection verification additionally requires one or more
origin-bound EvidenceSelectors. An exact reference may carry an empty selector
set with literal empty-set meaning so a revision whose authenticated summary has
no subject/searchable-target coordinate remains addressable, including an
AsProtected correction target. If the returned summary has any such coordinate,
every one requires coverage by an origin selector or the exact lookup is
non-disclosing. Empty never means all. The copied access view is embedded in the
strict ControlTarget arm before authority or lookup.
ControlGrantSpec.RecordActions and Coordinates can only retain members of that
origin access; ExactQuery, HoldStateQuery, and VerificationQuery receive the
effective resource/action/classification/coordinate intersection before reading.
The kernel then requires every returned authorization summary member to be
covered all-of. An authority cannot invent a selector after seeing stored data,
and an outside target remains non-disclosing.

Resource-wide and operation-type-wide reads are explicit query classes, not an
empty exact selector. They require a nonempty Purpose/Role, one bounded exact
ScopeSelector, explicit Limit, observed/recorded Time, and a nonempty effective
action ceiling. The sealed ResourcePolicy or OperationType supplies the only
resource/action/classification vocabulary; current authority may narrow it before
StoreQuery applies Limit. Every returned revision is wholly authenticated and
post-validated against the same declaration and all-of grant. They share the
immutable cohort, cumulative budget, cursor reauthorization, and independently
committed evidence barrier with narrower history. There is no all-resources,
all-scopes, arbitrary operation-name, or fallback-scan spelling.

AsPlaintext, AsToken, and AsIndexedProtected targets admit this
history door. AsProtected and AsRedacted remain valid capture declarations but
TargetRef and History.Target return the dedicated unsearchable-target refusal
before authority or Log I/O; they never trigger a broad scan. Cross-event/action/
resource and raw-versus-token target refs are origin mismatches. SelectSubject and
SelectAllSubjects seal one resource declaration plus either one logical subject
or an explicit all-subjects match; their optional action arguments must be a
duplicate-free subset of declared entity actions, and empty selects all declared
actions. SelectTarget and SelectAllTargets do the same for one event declaration's
fixed action. Exact selection validates the logical reference; all selection
carries none. Every selector requires an equality-searchable declared mode and is
rejected otherwise. NarrowActions can only retain a nonempty duplicate-free subset
of the selector's already sealed action set and preserves its hidden declaration/
origin identity; it cannot change match kind or coordinates. Query.Scope is mandatory: CurrentScope resolves only the
policy-admitted frozen scope and never means all scopes; ExactScope is the
explicit cross-scope request later bounded by the authority. The zero selector is
invalid. Both components of ScopedReference are independently nonempty bounded
References and the pair is encoded atomically as a domain tag plus two
length-delimited byte strings; concatenation or separate component tokenization is
invalid. That exact pair encoding remains canonical through the selector,
normalized request, authority view, grant subset/intersection,
query-token coordinates, cursor/fence claim, evidence digest, and revocation;
equal Reference values in different Scope namespaces never compare equal.

EventType.History(history).Events binds its one declaration without a target
predicate. ByEventIndex accepts only an EventIndex returned by that same sealed
EventType declaration and canonically encodes V once with its exact codec.
Plaintext, tokenized, and indexed-protected index constructors respectively
produce only the matching equality coordinate; the display EventField remains
independently projected and an indexed-protected value never substitutes its
randomized ciphertext for the index token. QueryIndex and the exact codec,
classification, mode, resource, action, and stable FieldName are manifest
meaning. A removed or incompatible index is not approximated with a scan.

OperationType.History(history).Operation binds the compiled operation declaration
and one exact OperationID. Its request target derives the operation name plus the
finite resource/action/classification ceilings of every declared member before
authority or Log I/O; Query.Actions may only narrow that member set. A bare
OperationID has no history method and cannot let unknown stored rows define their
own authorization scope.

Actor and Context are explicit investigation doors, never a generic evidence
query language. InvestigationQuery requires a nonempty, duplicate-free,
current-declared Resources set within MaxQueryResources and at least one exact
Action within MaxQueryActions; authority may narrow both. MatchActor binds exactly one nonempty ActorKind
and logical reference at the initiating hop, effective hop, or any hop. Context
selectors are strict one-of constructors for client, service, deployment,
operation, correlation, causation, trace, or source; the selected kind must be
declared by every applicable action and equality-searchable in every retained
generation. Initiating means ordinal zero, effective means the final dense actor
hop, and any-hop never changes ordinal semantics. No empty selector, implicit
all-resource form, arbitrary metadata/property path, substring, ordering, range,
join, callback predicate, or raw token has a public spelling. Every query
resource, action, classification, and coordinate set is independently bounded by
MaxQueryResources, MaxQueryActions, MaxQueryClassifications, and
MaxQueryCoordinates before canonical allocation; operation and exact targets
obey the same limits.

Revision and Item are exact-inspection doors. They use ExactLog with one exact
RevisionRef or ItemRef and no store search/cursor, verify the complete containing
revision before projection, and return ErrNotFound without a partial object. The
ExactAccessQuery requires nonempty duplicate-free current-declared Resource,
Query.Actions, and Classification ceilings; they enter AccessTargetView before
authority or ExactLog I/O and have no implicit-all spelling. The current grant
must be their subset and cover every actual resource/action/classification
coordinate of the whole revision before Revision returns it. Item may disclose
only the named item but still authenticates its containing revision and requires
that item's complete coordinates within both requested and granted ceilings.
Foreign LogID, catalog lineage, ordinal, or projection refuses.
History validates and normalizes the public request, chooses
the catalog ControlPolicy, resolves ContextResolver exactly once on the original
context, applies that policy's requiredness/provenance allowlist, copies the
logical result, and derives domain-separated requester aliases. AccessAuthority
receives a value-free context delegating only Deadline, Done, and Err plus an
AccessRequest whose View contains that exact frozen logical requester and query;
it cannot reread caller values from context. AccessDecision is origin-bound plain
data, not a grant: AllowAccess or DenyAccess embeds the unrendered identity of its
exact AccessRequest, and a decision constructed for another request is refused
even when every visible byte matches. Constructors deep-copy and validate all
sets, bounds, expiry, and subset constraints. History then mints an internal grant.
Only ReconstructionQuery carries a nonzero ReconstructionRequestView; only
ComparisonQuery carries a nonzero ComparisonRequestView, whose ordered before and
after boundaries share one combined revision/page/byte budget. Every other query
class requires both arms and every grant boundary to be zero. A reconstruction
grant must echo its exact requested boundary; a comparison grant must echo both
ordered boundaries. Either may only lower MaxRevisions, MaxPages, and MaxBytes.
State and Compare refuse a changed kind, boundary, order, time, or budget before
any Log or ExactLog I/O. Grant Roles, Scopes, Catalogs, Resources, Actions,
Fields, Context, and Classifications are each a
canonical subset of the origin request and current policy where the vocabulary is
an allowlist. Direction, Changed, Outcomes, and Reasons must echo exactly; an
authority that rejects those predicates denies instead of rewriting their
meaning. Time must keep the same axis and can only remove instants by tightening
endpoints. The effective request is the intersection, never replacement by grant
data. Page and cursor totals count against the grant's cumulative MaxRevisions,
MaxPages, and MaxBytes; reconstruction and comparison also cannot exceed their
request budgets.
Store narrowing and kernel post-validation both enforce
scope, resource, subject, action, field and context-fact projection, purpose,
role, time, expiry, and
allowed CatalogRefs. The exact retained manifest authenticates each row and its
wire/codec meaning; current access policy is the only authorization overlay. A
removed or renamed field is unavailable unless current policy explicitly admits
its stable historical identity and upcast. A malicious page causes whole-page rejection before values
reach the caller. Page.Revisions returns a wholly caller-owned deep copy and
exposes only admitted logical actor/context/value bytes after record-era decode
and reveal. No token, ciphertext, nonce, seal key, unrequested field/context fact,
or raw wire row crosses that view. ReadValueView.Canonical is populated (including
an explicitly present empty encoding) only for FieldKnown and is copied on every
access; every other FieldKnowledge state carries nil Canonical bytes.
ContextFactReadView is opaque copied read data. It exposes kind, provenance,
classification, and knowledge plus a typed value only through the accessor
matching its Kind; in particular ScopedReference returns both namespace
components and no public API asks callers to decode its internal canonical bytes.

NewHistory validates that Recorder, Log, and ExactLog have one exact process-local Backing,
durable BackingID, durable LogID, and CatalogSet. Recorder supplies its already
validated context, identity, tokenization, semantic, protection, signing, ID,
clock, and required access-evidence services without exposing its Writer.
Revealer and Verifier are separate least-privilege read capabilities. Verifier is
always nonnil and its copied description inventory covers every required signing
description in the complete admitted lineage at construction. Revealer may be nil
only when the complete lineage and current policy expose no read projection that
can require authenticated reveal; this is a constructor fact, not a later
query-dependent exception. Every equality-filtered subject, scope, actor hop, target, event
index, client, service, deployment, operation, correlation, causation, trace, or
source declared AsToken or AsIndexedProtected requires a query token for each
exact retained TokenDescription admitted by the query. Missing, extra, duplicate, foreign-query,
or wrongly described results and one missing retained key refuse the whole query
before Log I/O. AsProtected and AsRedacted coordinates are not searchable and
never fall back to a broad scan; AsIndexedProtected stores a token index
separately from its randomized protected display value. A catalog exposing
HistoryRead or any lifecycle action is rejected if any active or retained
resource subject or present ScopeFact it promises to authorize is not
equality-searchable. Its ControlPolicy scope is required and
equality-searchable. Typed event-target history and EvidenceSelector additionally
require an equality-searchable target declaration; an AsProtected target remains
eligible for an exact correction lookup followed by authenticated record-era
reveal. An absent optional evidence scope remains absent rather than becoming a
wildcard. The backing/LogID/set equality is
rechecked on every operation so a mutable or
malicious StoreInfo cannot repoint after construction. Wrong backing, catalog,
or key generation refuses before authority or store I/O.

Public Cursor and backend StorePosition are different bounded types. The cursor
is an authenticated encrypted envelope. Its non-sensitive AAD binds format,
durable LogID, durable BackingID, exact CatalogSet digest, and cursor-key
generation. Its encrypted claim contains the exact normalized request and
purpose, original RequesterCommitment and retained alias-description set,
original AccessRequestDigest, original AccessGrantSpec and AccessGrantDigest,
the prior effective AccessGrantSpec, issue time, hard expiry, immutable search-
snapshot identity, StorePosition, and
cumulative page/revision/wire-byte progress.
Empty cursor means origin. On every continuation History opens and validates the
claim before Log I/O, resolves the current requester, requires an active/retained
alias match, derives InputContinuation over the exact incoming cursor and its
authenticated origin request/grant, calls AccessAuthority again for the same
normalized request, and computes the next effective grant as the intersection of
current policy, the normalized request, the original grant, the prior effective
grant, and the current grant. Revocation or an empty intersection denies;
narrowing is honored and the next cursor freezes that exact effective grant. No
later decision can reintroduce a scope, resource, action, field, context fact,
classification, time instant, code, boundary, or unit of budget removed on any
prior page, or extend prior expiry. A different
principal, query, or purpose, a foreign/tampered/expired cursor, a retired key, or
a changed CatalogSet refuses rather than restarting. Catalog activation
intentionally invalidates outstanding cursors; callers restart and reauthorize.
CursorKeys advertises a copied canonical description inventory with exactly one
primary and no duplicate `(algorithm, profile, key ID)` identity. NewHistory
validates the inventory without I/O and requires a positive CursorLifetime no
greater than MaxCursorLifetime. An origin seals its computed effective grant with
`expiry = min(issued_at + CursorLifetime, grant.ExpiresAt)`; overflow, an already
expired grant, or an empty interval refuses. A continuation preserves the
original issue time and stores the new effective intersection; expiry is the
minimum of prior expiry, current grant expiry, and every applicable effective
ceiling.
Retained descriptions open only claims with `issued_at < expiry <= issued_at +
CursorLifetime` and no later than the hard maximum. Removing a key
before every token it sealed has expired is an explicit deployment revocation and
makes those continuations fail closed. ParseCursor checks version and
MaxCursorBytes before key selection or decoded allocation.
Before a returned page is committed as evidence, History derives the typed keyed
AccessContinuationDigest over the exact serialized Cursor plus its origin request
and grant digests. The authenticated cursor envelope thereby binds its protocol,
algorithm/profile/key generation, nonce/ciphertext, decoded normalized claim,
position, issue time, and expiry without placing those bytes in evidence. A result
with no continuation requires both presence false and a zero digest; a result with
one requires presence true and the exact nonzero digest. The cursor claim never
contains AccessResultDigest, so this ordering is acyclic.
Public Limit counts complete revisions; a revision is never split. PostgreSQL
pages revision headers by the selected time axis, stable tie-breaker, and requested
direction, and only uses item ordinal to assemble one revision. On an origin
Search, the store atomically materializes one bounded immutable cohort under an
opaque random snapshot identity expiring with the cursor. Every page is a slice
of that fixed cohort; concurrent earlier/later/backdated appends cannot enter it.
The cohort contains references and canonical sort keys, not copied evidence
payloads, and must fit the minimum of the current grant and store-declared
SearchCohortRevisions/SearchCohortBytes ceilings, each itself no greater than the
hard MaxSearchCohortRevisions/MaxSearchCohortBytes. Snapshot identity bytes remain
separately bounded by SnapshotBytes and MaxSnapshotBytes. NewHistory requires
StableSearch and ExactInspection to equal SupportSupported and every required
search/exact ceiling to be nonzero; Unstated or Unsupported refuses construction.
A CatalogSet containing an active or retained AttemptType additionally requires
HistoryConfig.Attempts, AttemptLifecycle support, and nonzero attempt count/byte
ceilings. Recorder, Log, Exact, and Attempts must expose the same process Backing,
durable BackingID/LogID, and exact catalog lineage; a catalog with no attempts may
leave Attempts nil so the S2 alpha does not invent that capability.
A caller narrows an oversized
history rather than receiving an unstable partial snapshot.
NewStoredPage requires the origin snapshot and expiry echo before releasing even
page one. Continuations carry the exact snapshot and position; a missing,
recreated, expired, foreign, widened, reordered, or mutable snapshot rejects the
whole page. Origin progress is zero; each accepted history page increments pages once,
adds its exact complete revision count and validated wire bytes before result
evidence, and requires Cohorts to remain zero. The sealed continuation echoes those cumulative counters, and both the
store request and kernel reject a regression, jump, overflow, or grant-ceiling
breach before disclosure. Snapshot storage is explicit durable adapter state with
lazy bounded expiry cleanup, not a constructor-started worker. A query whose
cohort cannot fit its ceilings refuses before partial disclosure.

`ResourcePolicy.History(*History)` returns `*ResourceHistory[M, ID]`, so Subject
and State preserve model and ID types without an illegal generic method.
Reconstruction is available only through that bound policy. `AtRevisionAfter`
means the state after applying the complete named revision, inclusive; the
revision must exist under the authorized catalog/resource/subject. A missing,
foreign, or invisible revision returns one non-disclosing boundary refusal.
`AtHead()` and `AtObservedTime(t)` first obtain exactly one origin-bound
EntityChainHead for the
narrowed subject. EntityChainHeadMissing is unauthenticated absence and returns
TemporalAmbiguity with no projection; a successful time boundary always carries a
present assertion. AtHead selects that authenticated revision after closing its
entire predecessor chain; it is the ergonomic current store-present projection,
not an external anti-rollback oracle. History exact-inspects and authenticates that referenced head revision,
then follows and authenticates every predecessor from that head through genesis.
Search pagination must reach the same head and leaf; omission relative to the
assertion, a fork, or a missing/mismatched link is TemporalAmbiguity. The kernel
then walks genesis to head while maintaining the cumulative maximum signed
ObservedAt. A revision is selectable only while that cumulative maximum is `<= t`.
If no revision is selectable because the authenticated genesis/full baseline is
observed after `t`, the request returns TemporalAmbiguity with no Projection or
result evidence; it never turns an unknown pre-audit interval into all-Absent.
If a descendant observed at or before `t` follows an excluded predecessor observed
after `t`, the request is TemporalAmbiguity rather than a projection through that
predecessor. Equal observations retain predecessor order. Unsigned RecordedAt,
RevisionID, backend position, and monotone rewrites of them never select the answer.
ResolvedBoundary exposes both the selected revision's ObservedAt and the cumulative
ObservedThrough value plus the asserted head. The assertion authenticates content,
not freshness: rollback of both chain and a formerly valid head needs an independent
external anchor to detect. Budget exhaustion before head-to-genesis closure returns
the bounded unknown result rather than a guessed boundary. Each declared field reports Known,
Absent, Unobserved, Redacted, Tokenized, Destroyed, MissingKey, UnknownCodec, Gap,
BudgetExceeded, or Unprojected; raw bytes and guessed zero values
never escape as known. There is no whole-model accessor. A retained typed
ReconstructField handle is the only key accepted by the free generic Projected
function; Get succeeds only for Known. State requires nonzero revision, page,
and byte budgets within hard catalog/store ceilings. It scans backward and marks
every still-unproven field BudgetExceeded when any budget is exhausted; Gap means
a missing, forked, or mismatched previous-entity digest in the retained chain.
Reconstruction follows that per-subject digest chain rather than treating
RecordedAt, a database sequence, or item ordinal as commit order. A handle outside the exact bound
resource policy is Unprojected. MissingKey is distinct from malformed ciphertext,
which rejects the page. A missing or unauthenticated record-era manifest is an
operation-level non-disclosing ErrUnknownCatalog with no page or projection, not
a per-field knowledge value. Destroyed requires an authenticated lifecycle
destruction assertion; age eligibility alone never hides retained data, and
release 1 cannot produce this state because it executes no purge. A held revision
past its cutoff remains readable under current authority. Purge eligibility
derives from the immutable record-era retention-rule digest. Current policy never
silently shortens or extends old evidence; a future explicit lifecycle assertion
would be required, and release 1 exposes no such rewrite.
Every successful Projection also exposes one authenticated EntityLifecycleState,
derived from the selected subject-chain action rather than from field nullness.
Create, baseline change, ordinary change, and restore resolve to EntityPresent;
soft delete resolves to EntitySoftDeletedState; hard delete resolves to the
terminal EntityHardDeletedState. An all-absent field set therefore cannot
impersonate deletion. A pre-baseline boundary remains operation-level
TemporalAmbiguity and never invents a fourth lifecycle state. The state is bound
into AccessReconstructionDigestView and both comparison endpoints, so a backend
or authority cannot substitute existence while preserving field digests.
The newest applicable after-state wins, so an older unknown codec
or missing key cannot poison a later known assignment. Create, restore, and
delete plus a first-touch changed baseline are full declared anchors. A baseline
proves state only at and after its signed ObservedAt; no before-baseline projection
or synthetic creation time exists. Corrections and disputes annotate assertions
but never mutate reconstructed entity state; a revert is a new entity mutation.
Full-anchor completeness is relative to the anchor's authenticated record-era
catalog. If the currently admitted ReconstructField did not yet exist there and
no later authenticated full anchor or delta assigns it by the requested boundary,
the field is FieldUnobserved, not FieldAbsent, FieldGap, or FieldUnprojected.
Unrelated deltas cannot change that state. Its first later assignment establishes
Known, Absent, or the corresponding explicit protected/unknown state; a retained
HistoricalReconstruct identity preserves this rule after rename or removal.
Compare resolves both boundaries with the same authenticated subject chain and
one combined budget; it never performs two independently authorized reads. The
resolved After boundary must equal or descend from Before. A reversed, forked,
foreign, or temporally ambiguous pair returns no projections or evidence. One
Comparison contains two immutable Projections and one committed access-evidence
receipt. Compared accepts only a field handle from the bound policy. It reports
DifferenceEqual when both endpoints are Known with byte-equal canonical values or
both are Absent, DifferenceChanged when Known values differ or exactly one
endpoint is Absent and the other Known, and DifferenceIndeterminate for every pair involving
Unobserved, Redacted, Tokenized, Destroyed, MissingKey, UnknownCodec, Gap, BudgetExceeded, or
Unprojected. Changed returns `(changed, known)` and is true only for
DifferenceChanged; it never guesses from a hidden value. Access comparison
evidence binds the two resolved boundaries, contributing authenticated revision
digests, both lifecycle states, per-field endpoint knowledge, and each
DifferenceStatus.

Neighbors authorizes the exact resource, scoped subject, and center RevisionRef
once, pins one authenticated subject-head assertion, and closes that same chain
within one combined ReconstructionBudget. It identifies adjacency only from
signed predecessor order: equal or regressing ObservedAt values never reorder it,
and a concurrent normal or backdated append after the asserted head is outside
the result. Previous is NeighborChainBoundary only when the verified center is
genesis; Next is that boundary only when center equals the verified asserted
head. A verified adjacent revision is NeighborPresent. Missing retained evidence,
a broken link, or exhausted budget yields NeighborUnavailable with no revision,
never a guessed boundary or bare leaf digest. Foreign scope/resource/subject or a
center outside the authenticated chain is one non-disclosing refusal. The center,
both tri-state arms, contributors, and asserted head are bound into the single
AccessRevisionNeighborsResult before any RevisionNeighborhood is released.

A context deadline returns cancellation and no projection. History authorizes
and prepares a copied page using the value-free context plus explicit private
frozen requester/grant values. Log, tokenizer, revealer, verifier, cursor keys,
and the evidence writer receive no caller context values or group. It refuses any
ambient audit Writer authority or group before Log I/O and never exposes
uncommitted audit rows.
Required access evidence then uses a private standalone append and the page,
cursor, and resolved boundary are returned only with a Committed receipt.
NotWritten, Unconfirmed, or a malicious InCallerTransaction settlement discards
the prepared page; retrying evidence never releases that old page, so a later
call reauthorizes and rereads. Evidence says `result_prepared` and binds bounded
request/result digests, counts, and enums, not values, raw subject, cursor, or
matter. Denied-access evidence follows its separate consequence, attaches only a
safe settlement state to ErrDenied, and can never change denial into permission.
It is attempted only when HistoryDenied is declared and DenialLimiter returns
true for the bounded origin-bound DenialAttempt on a value-free context. An
undeclared action or nil limiter maps to NotConfigured, false to Suppressed, and
a returned limiter error to LimiterFailed, all with zero evidence append. An
admitted append maps definite failure, uncertainty/non-Committed settlement, and
independent commit to NotWritten, Unconfirmed, and Committed respectively. A
limiter panic is re-raised unchanged with no returned state. Limiter output is
never authorization. The denial request contains a requester
commitment and request digest, not raw subject, scope, query, or target.
The private evidence path cannot recurse.

AccessRequestDigest, AccessGrantDigest, AccessResultDigest,
AccessContinuationDigest, ControlRequestDigest, ControlGrantDigest,
ControlResultDigest, ControlContinuationDigest, HoldRequestDigest, and DenialRequestDigest
plus CorrectionProposalDigest, SelectionDigest, and FenceDigest
are typed outputs of Config.Semantics, never raw SHA-256.
Their fixed domains are `frostgrove.audit/access-request/v1`,
`frostgrove.audit/access-grant/v1`, `frostgrove.audit/access-result/v1`,
`frostgrove.audit/access-continuation/v1`,
`frostgrove.audit/control-request/v1`, `frostgrove.audit/control-grant/v1`,
`frostgrove.audit/control-result/v1`,
`frostgrove.audit/control-continuation/v1`, `frostgrove.audit/hold-request/v1`, and
`frostgrove.audit/denial-request/v1`, plus
`frostgrove.audit/correction-proposal/v1`,
`frostgrove.audit/control-selection/v1` and
`frostgrove.audit/inventory-fence/v1`.
Each domain accepts only its corresponding exhaustive typed input above; its
length-framed canonical encoder has one explicit field tag for every struct field
and rejects a mixed one-of. Access request binds catalog set, exact requester
commitment and aliases, role and query class through NormalizedQueryView, target,
projection, direction, change/code predicates, time/limit ceilings, the exact
reconstruction boundary or ordered comparison boundaries and all combined work
budgets when present, continuation bit, and the original request and grant digests.
Origin calls require InputContinuation and both origin fields zero. Continuations
require all three nonzero: InputContinuation is the exact typed digest of the
incoming encrypted token plus the authenticated origin request/grant, and both
origin fields are byte-equal to that token's claim. None can be recomputed from
narrowed current state. Grant binds the full sealed AccessGrantSpec to its request. Access
result is a strict five-way page, reconstruction, comparison, exact-revision, or
exact-item union, and every unused arm must be canonical zero. Every referenced
revision binds its RevisionRef, unsigned normalized RecordedAt, EnvelopeDigest,
IntegrityDigest, and ordered complete returned ItemRefs. RecordedAt attests only
the operational metadata prepared for this disclosure; it is never promoted into
the signed envelope, causal order, or retention truth. The page arm binds those
ordered revision digests, count, truncation, and exact continuation
presence/digest. The reconstruction arm binds boundary presence, selected
RevisionRef, signed ObservedAt, cumulative ObservedThrough, asserted-head
presence/reference/leaf, ordered resource/field/knowledge triples, and all ordered
authenticated contributing revision digests. The comparison arm binds two such
ordered reconstruction arms plus every resource/field DifferenceStatus. The
exact-revision arm binds its one complete revision digest; the exact-item arm
binds its ItemRef and complete containing revision digest. No actor, context,
subject, target, outcome, reason, field value, cursor bytes, or other disclosed
logical bytes enter any result digest.
Control request binds the same complete requester commitment/aliases, action,
purpose, role, action-scoped reason, exact target including requested limit,
continuation bit, exact incoming control-continuation digest, origin request/grant
digests, and exact HoldRequestDigest when
applicable.
Control grant binds its complete sealed grant. Control result is a strict mutation,
inventory, verification, or purge-plan union binding the ordered references,
statuses/disposition, AsOf, truncation/completeness, continuation/fence presence,
exact typed continuation digest, fence digest, and for hold mutation the complete
returned projection applicable to that arm. A mutation requires
EnclosingRevision=true instead of hashing its generated containing RevisionRef
back into its own result. HoldPresent=false requires zero disposition/projection.
HoldPresent=true requires the disposition and every target membership/count/epoch/
set/head member. A zero head, an exact prior signed head, and the newly enclosing
revision are distinct HeadKind arms; only PriorHead carries PriorHead bytes. The
kernel verifies that EnclosingRevision maps to the containing signed header and
that HoldProjectionEnclosingRevision maps to the resulting wire projection's exact
self head. Thus the digest binds the complete returned meaning without a circular
or fresh-revision-dependent keyed replay identity. Each continuation digest binds the
exact typed encrypted cursor, decoded claim, protocol/key generation/expiry, and
origin request/grant without a circular result reference. Hold additionally binds catalog set,
requester commitment/aliases, action, purpose, role, command, target, keyed HoldID,
matter presence and keyed matter, and continuation state.
The authenticated prior projection is subsequently derived through the sealed
exact-target grant and is bound into the signed candidate rather than exposed as
unauthenticated caller input. Denial uses an explicit kind and exactly one access or
control request digest plus the complete requester commitment/aliases and
action/purpose/role/reason/continuation coordinates.
SelectionDigest is derived before ControlRequestDigest from exactly CatalogSet,
ControlAction, and the complete normalized ControlSelectionView; the target
contains both that view and its digest and the kernel recomputes equality before
authority, StoreQuery, cursor, or fence use. FenceDigest is derived only after
FenceKeys returns a valid InventoryFence envelope. Its input binds the exact
serialized envelope and the complete decoded claim: format, durable log/backing,
active catalog and catalog set, frozen requester commitment/aliases, original
action/purpose/role, origin request/grant digests, normalized selection and its
digest, original and prior-effective grant ceilings, normalized AsOf, issue/token
expiry, bounded snapshot and its immutable expiry, and every ordered candidate authorization/retention/integrity/
hold member. The claim contains SelectionDigest but never FenceDigest or a control
result, so sealing then digesting is acyclic. Opening a supplied fence reconstructs
that same input and requires exact equality before authorization or lifecycle I/O.
Changing any single field changes the input and its keyed digest. These domains
are disjoint from revision, correction, identity, token, and signature inputs. A
semantic key/profile change requires catalog lineage change, so low-entropy
subjects and targets never gain an offline dictionary oracle and cross-domain
substitution fails. Custom SemanticDigester behavior remains the explicit trusted
keyed-PRF boundary described in section 2.4.

### 2.9 Integrity, protection, holds, and purge planning

```go
type ProtectionRequest struct{ value protectionRequest }
type RevealRequest struct{ value revealRequest }
type TokenizeRequest struct{ value tokenizeRequest }
type TokenQuery struct{ value tokenQuery }
type TokenQueryResult struct{ value tokenQueryResult }
type ProtectedValue struct{ value protectedValue }
type Token struct{ value token }

type Protector interface {
	Description() ProtectionDescription
	Protect(context.Context, ProtectionRequest) (ProtectedValue, error)
}

type Tokenizer interface {
	ActiveDescription() TokenDescription
	Descriptions() []TokenDescription
	Tokenize(context.Context, TokenizeRequest) (Token, error)
	QueryTokens(context.Context, TokenQuery) (TokenQueryResult, error)
}

type Signer interface {
	Description() SignatureDescription
	Sign(context.Context, IntegrityDigest) (Seal, error)
}

const (
	AES256GCMAlgorithm           = "aes-256-gcm"
	AESGCMProtectionProfileV1    = "frostgrove.audit.protection.v1"
	AESGCMHistoryCursorProfileV1 = "frostgrove.audit.history-cursor.v1"
	AESGCMControlTokenProfileV1  = "frostgrove.audit.control-token.v1"
)

type AESGCMKey struct {
	KeyID  string
	Key    []byte
	Active bool
}

type ProtectionKeyring interface {
	Protector
	Revealer
}

func AESGCMProtection(...AESGCMKey) (ProtectionKeyring, error)
func AESGCMCursors(...AESGCMKey) (CursorKeys, error)
func AESGCMFences(...AESGCMKey) (FenceKeys, error)

type HMACTokenKey struct {
	KeyID  string
	Key    []byte
	Active bool
}
type HMACSigningKey struct {
	KeyID string
	Key   []byte
}
type HMACVerificationKey struct {
	KeyID string
	Key   []byte
}

func HMACTokenizer(...HMACTokenKey) (Tokenizer, error)
func HMACSigner(HMACSigningKey) (Signer, error)
func HMACVerifier(...HMACVerificationKey) (Verifier, error)

type Seal struct{ value seal }

func (r ProtectionRequest) Plaintext() []byte
func (r ProtectionRequest) AAD() []byte
func (r RevealRequest) Envelope() ProtectedValue
func (r RevealRequest) AAD() []byte
func (r TokenizeRequest) Plaintext() []byte
func (r TokenizeRequest) AAD() []byte
func (r TokenizeRequest) Description() TokenDescription
func (q TokenQuery) Requests() []TokenizeRequest
func NewTokenQueryResult(TokenQuery, []Token) (TokenQueryResult, error)
func (r TokenQueryResult) Tokens() []Token
func NewProtectedValue(string, string, string, []byte, []byte) (ProtectedValue, error)
func (v ProtectedValue) Algorithm() string
func (v ProtectedValue) Profile() string
func (v ProtectedValue) KeyID() string
func (v ProtectedValue) Nonce() []byte
func (v ProtectedValue) Ciphertext() []byte
func NewToken(string, string, string, []byte) (Token, error)
func (v Token) Algorithm() string
func (v Token) Profile() string
func (v Token) KeyID() string
func (v Token) Bytes() []byte
func NewSeal(string, string, string, []byte) (Seal, error)
func (v Seal) Algorithm() string
func (v Seal) Profile() string
func (v Seal) KeyID() string
func (v Seal) Bytes() []byte
```

The active manifest chooses exactly `IntegrityOnly` or a required
SignatureDescription; there is no per-call downgrade. Config.Signer must be nil
for IntegrityOnly and must match the required active description otherwise.
Historical verification uses each retained manifest's exact description and a
Verifier that retains the required keys. A signing profile or active key change
requires a new catalog generation and changes replay semantics. The root ships
keyed HMAC tokenizer/signer and AES-256-GCM protection/history-cursor/control-
token constructors over copied caller-owned keys. The HMAC helpers synthesize
HMACSHA256Algorithm and their distinct fixed profiles; caller input supplies only
KeyID, key bytes, and the one active-token-key bit. KeyIDs are
nonempty, bounded, and unique; keys are at least 32 bytes; a tokenizer has exactly
one active key, a signer exactly one key, and a verifier at least one key. Token
and signature bytes are exactly 32 bytes. Token MAC input is a versioned,
length-framed domain containing its description, request AAD, and plaintext.
Signature MAC input uses a disjoint domain and binds its description plus
IntegrityDigest. QueryTokens emits exactly one correctly described token per
requested description in request order. Verification uses constant-time
comparison.
AESGCMProtection returns one object implementing both Protector and Revealer;
AESGCMCursors and AESGCMFences return the two narrower continuation capabilities.
Each keyring has unique bounded KeyIDs, exactly one active key, copied
32-byte-or-longer input key material, random standard-size nonces from crypto/rand,
and a distinct synthesized profile. HKDF-SHA-256 with a fixed v1 salt and the
length-framed algorithm/profile/KeyID as info derives a disjoint exact 32-byte
AES key for protection, history cursor, and control-token profiles, so accidentally
reusing caller key material across constructors does not reuse an AEAD key.
Inventory fence and control cursor share the control-token derived key but use
disjoint typed versioned message domains. Every protocol authenticates the supplied
canonical AAD plus algorithm/profile/key identity. Active-key rotation writes only with the
new key while retained keys remain readable until removed; constructors perform
no I/O or background work. Missing keys, malformed nonce/ciphertext, invalid tag,
unsupported metadata, entropy failure, and policy refusal map to the exact closed
CryptoOutcome without leaking key material. Returned envelopes and plaintext are
copied. Applications may replace these helpers with KMS/HSM-backed implementations
of the same least-privilege interfaces. Verification distinguishes
valid, invalid, missing key, unsupported algorithm, malformed evidence, catalog
mismatch, envelope/AAD mismatch, and backend failure. ProtectionRequest exposes
copied plaintext and canonical AAD accessors only for the duration of Protect;
RevealRequest carries the exact same reconstructed AAD and copied envelope.
TokenizeRequest uses a separate canonical domain-bound AAD. TokenQuery contains a
bounded copied request for each retained generation/profile needed by the sealed
query and exposes no grant. The tokenizer returns an origin-bound TokenQueryResult
with exactly one token in the same order as those distinct descriptions; its
constructor rejects missing, extra, duplicate, wrongly described, or foreign-query
tokens, so a store query cannot silently omit retained history. ProtectedValue and Token are bounded, copied,
immutable, and render no key, nonce, or bytes.
Every collaborator-returned byte slice is independently owned by the caller for
the full return lifetime. The kernel immediately bounds and copies Reveal,
OpenCursor, OpenFence, and OpenControlCursor bytes before decode and never retains a provider-owned
view. Providers retain neither request slices nor returned aliases and do not
mutate them after return. Mutation and race conformance fakes enforce the same
rule for codec, protector, tokenizer, revealer, cursor, fence, signer, and
identity-keyring outputs.

The audit-owned HMAC/AES helpers are the inspectable production guarantees for
keyed-PRF behavior, domain separation, unforgeability, AEAD confidentiality,
cryptographic nonce generation, constant-time MAC comparison, copied key material,
and deterministic key continuity. A custom IdentityKeyring, Tokenizer, Protector,
Revealer, Signer, Verifier, CursorKeys, or FenceKeys implementation is an explicit
deployment cryptographic TCB. It is additionally trusted for the applicable PRF,
AEAD, unforgeability, nonce uniqueness, key selection/retention, non-retention of
plaintext/key material, deterministic restart behavior, concurrency safety,
termination, and complexity properties. The kernel still validates descriptions,
origin bindings, lengths, one-of shapes, copied ownership, and returned protocol
metadata. Finite vectors, alias/race fakes, state-switch canaries, and subprocess
watchdogs catch exhibited violations but never certify a custom provider's future
cryptography or resource use. Untrusted providers require process isolation outside
this in-process SPI.

SemanticDigest is a write-time keyed equality commitment. History does not
recompute it and therefore never asks for a historical semantic key; it
recomputes the exact EnvelopeDigest and IntegrityDigest from stored bytes and the
stored commitment. IntegrityOnly makes only an accidental-corruption claim. Any
CatalogSet exposed through NewHistory or NewControl must require a signature in
every retained generation they can admit, and the Verifier must advertise each
exact record-era description. An unsigned generation is valid only for a
recorder-only deployment and protected read/control constructors reject it before
authority or store I/O. A valid record-era seal is what authenticates content
against a malicious store.

Hold placement/release, correction, and dispute are built-in lifecycle drafts and
therefore use the same idempotent Writer, unknown-outcome channel, digest, and
receipt rules. Legal hold is set membership, never a boolean. One HoldID names
one matter encoded and protected by the exact record-era HoldMatterPolicy and may
cover several exact RevisionRefs; membership is
`(HoldID, RevisionRef)`. Placement adds one immutable membership, release names
exactly one, and there is no release-all-by-revision spelling. The same HoldID
with another matter conflicts, and a released `(HoldID, RevisionRef)` membership
cannot be resurrected; other memberships of that same matter remain independent.
Before randomized matter protection, IdentityKeyring produces the active and
retained domain-separated HoldIDCommitment and HoldMatterCommitment aliases.
Control proposes HoldIdentityCandidate from the active HoldID commitment. The
store atomically maintains one alias-to-stable-HoldIdentity-to-matter mapping:
another membership with any matching alias reuses that identity, no match adopts
the candidate, and multiple matched identities are corruption. Raw matter,
equality commitments, and HoldIdentity are never rendered or returned to the
application.
Placement derives both typed commitments before authorization; release derives
the HoldID commitment and carries canonical absent matter with a zero matter
commitment. The HoldRequestDigest is echoed by the grant and repeated in the
conditional append and unique signed hold wire arm. Every transition signs its
command, HoldID, stable HoldIdentity, keyed HoldID, target, exact matter presence and protected
representation, matter commitment, disposition, expected and resulting membership
state, active count, monotonically increasing hold epoch, canonical HoldSetDigest, and
prior/new transition head. HoldState returns the complete bounded transition references;
Control exact-inspects and verifies their genesis-to-head chain and recomputes
the state before minting one candidate. Stores CAS that entire expected state and
atomically project the signed result while appending; idempotent replay advances
none. Only effective activation/release revisions advance the transition head and
enter HoldState.Transitions; signed AlreadyActive/AlreadyReleased evidence leaves
the prior/result head equal and cannot make proof growth unbounded. With at most
MaxHoldsPerRevision identities and no resurrection, the effective proof has at
most MaxHoldTransitionsPerRevision entries and MaxHoldStateBytes. Inventory,
HoldState, and PlanPurge projection values are convenience
indexes only: Control accepts them only when the authenticated transitions
recompute identically. Missing matter/decryption keys never make a hold absent.
Purge eligibility requires a verified active count of zero.

Inventory and PlanPurge are read-only with respect to retained evidence, hold
membership, and eligibility state; each still performs its one required
standalone audit-evidence append before disclosing a report. Inventory requires
explicit positive Limit, MaxCohorts, MaxCandidates, and MaxBytes within hard/store
ceilings and asks the store for limit+1 whole
cohorts in one immutable snapshot. Limit counts RetentionCohortView entries;
 MaxCohorts is the cumulative unit ceiling, MaxCandidates counts flattened member
revisions, and MaxBytes counts the complete post-validation canonical Revision
wire bytes for every member, with a co-revision counted once. Encoded cohort/result
metadata is bounded independently by MaxInventoryResultBytes and sealed cursor or
fence metadata by MaxInventoryCursorBytes and MaxInventoryFenceBytes. A whole
cohort that cannot fit any applicable ceiling returns ErrTooLarge without members
or a skip cursor. SearchProgressView.Cohorts is zero for history/verification and advances by
the exact whole-cohort count for inventory. All progress dimensions are checked
and cannot reset across pages.

InventoryQuery carries the exact Control-selected AsOf; NewInventoryResult
rejects a missing, changed, non-normalized, earlier, or future echo before any
candidate eligibility is used. Before origin store I/O, Control derives one
normalized SnapshotExpiry no later than the minimum of `now + CursorLifetime`,
grant expiry, and MaxFenceLifetime, and InventoryQuery carries it. The store
atomically creates the snapshot with that expiry and NewInventoryResult requires
an exact nonzero echo before any candidate is used. Continuations preserve the
origin expiry; current authority may shorten the next cursor/fence expiry but
cannot extend snapshot storage. PurgeQuery and the decoded fence preserve both
snapshot identity and SnapshotExpiry, and the fence itself expires no later.
Early deletion, recreation, expiry extension, or any echo mismatch fails closed.
Expired snapshots are cleaned lazily under a bounded store operation, never by a
constructor-started lifecycle. Snapshot bytes are bounded opaque store identity
only and have no eligibility-time semantics. The same AsOf is preserved across
continuations and into the fence and purge plan; neither store nor caller can
override it.

RetentionCohortView is the one canonical grouping table and appears exactly once
per InventoryResult, cursor page, fence, PurgeQuery, store purge result, and public
PurgePlan. Every InventoryCandidate references one table ID; every listed member
has exactly one contiguous candidate in cohort/member order, and no unlisted,
duplicate, or cross-cohort RevisionRef is legal. A non-attempt revision is a
RevisionRetentionCohort with one member and zero Attempt/Terminal. A revision
containing an AttemptItem can never use that singleton form. An
AttemptRetentionCohort contains every and only containing revision of its
AttemptChainID in signed transition sequence order, has 2 through
MaxRetentionCohortMembers members, and has a terminal state of Succeeded, Failed,
Cancelled, or Abandoned. Its EligibleAt is the checked maximum of every member's
record-era retention cutoff regardless of transition order or regressing
ObservedAt. A Forever member, Open/Uncertain chain, incomplete budget, malformed
chain, or over-member group is ineligible rather than partially returned.
Exactly one AttemptRetentionGuardView accompanies each attempt cohort and none
accompanies a singleton; its Cohort joins byte-exactly and its Current state is
the store's same-snapshot type-counter certificate. Guards are copied through the
inventory result, cursor/fence, PurgeQuery, and StorePurgePlan result.
The origin snapshot includes a cohort only when every member is covered by the
effective resource/action/retention/classification/coordinate grant. It orders
whole cohorts by `(EligibleAt, RetentionCohortID)` and members by their canonical
cohort order; StorePosition is always a boundary after a whole cohort. A backend
position inside a cohort, a candidate interleaving, or a continuation that skips
an oversized next cohort is invalid.

When retained catalogs contain AttemptTypes, NewControl requires both AttemptLog
and CatalogMutationLogReader with the exact Recorder/Exact/Lifecycle origin. For
each proposed attempt cohort, Control asks AttemptState by stable chain, tiles
bounded ExactLog batches, and authenticates every containing revision. The fully
recomputed chain's transition refs must equal the cohort members with no missing,
extra, duplicate, reordered, or sibling transition; each member contains exactly
one item for that chain and the table terminal equals the signed head. Control
recomputes each RetentionBasisDigest/cutoff, the maximum EligibleAt, and
RetentionCohortIDOf. The helper itself validates shape/cardinality/duplicates and
hashes the supplied order; semantic sequence and kind validation necessarily
happens after exact inspection, not by pretending bare RevisionRefs reveal it.

One candidate remains one whole RevisionRef and contains its complete
RevisionAuthorizationSummaryView. The store applies only grants covering every
summary member. Before inventory, verification, hold, correction, dispute, or
purge output or mutation, Control uses bounded ExactLog batches to inspect every
candidate, authenticates the complete revision, recomputes the summary from
actors/context/items, and requires byte-exact equality with the header, lifecycle
candidate, cohort table, and sealed grant. Existing exact-target count/byte
ceilings apply before copying or decoding. Splitting a revision into per-resource
candidates, splitting a retention cohort across pages, choosing a representative
label, applying any-item authorization, or deduplicating after authorization is a
contract failure. If the next whole cohort does not fit a nonempty page, the page
ends before it. If it cannot fit an empty page or the fence hard/current-grant
ceilings, the operation returns ErrTooLarge without a skip cursor. Stored page and
inventory progress must be the exact checked successor of the origin.

A private authenticated and encrypted InventoryFence carries one bounded,
canonically sorted retention-cohort table and all member candidates: cohort
kind/ID/attempt/terminal/members/EligibleAt, RevisionRef, complete authorization
summary, integrity and immutable retention-basis digests, hold epoch, active-set
digest, exact authenticated hold-transition refs, frozen opaque store snapshot,
Control-selected AsOf, complete normalized selection/digest, original and
prior-effective grant ceilings, frozen requester commitment/aliases and original
action/purpose/role, snapshot expiry, active CatalogRef/CatalogSet, durable
LogID/BackingID, expiry, format, and fence-key generation. It contains no
plaintext matter or subject. PlanPurge opens and validates the token, considers
exactly those cohorts, re-reads every candidate and guard, and rejects changed
eligibility/hold/counter state. A late unrelated commit or the Inventory/
PlanPurge evidence itself is outside the cohort and cannot self-invalidate it.
No allocation high-water or timestamp is a settled commit watermark. Catalog
activation invalidates an old fence.

A hold on any attempt-cohort member sets the cohort's Held flag and blocks the
whole cohort from StorePurgePlan, including all co-revision items; inventory may
still report it diagnostically. A cohort containing the current nonzero Head of its
AttemptTypeProjectionStateView similarly sets CounterHeadPinned and is excluded
from StorePurgePlan: memory and PostgreSQL maintain
a reverse ItemRef-to-type-counter-head index and recheck it for Inventory and
PlanPurge. Atomic counter supersession unpins the old cohort. Removal or
incompatible activation first persists the non-purgeable
CatalogActivationGateDigest anchor and zero/headless counter result, which safely
unpins the retired signed head. Compatible activation preserves the pin. A
headless state for a previously existing type is accepted only with a nonzero
anchor authenticated against the immutable catalog mutation log; bare genesis,
foreign/stale/wrong-type anchors refuse. A counter-advance race may conservatively
refuse but never approve a partial or stale group.
StorePurgePlan.Cohorts is exactly the whole-cohort subset whose recomputed
EligibleAt is at or before AsOf and whose Held and CounterHeadPinned flags are both
false; its Eligible candidates are the exact flattened member bijection. It may
omit a newly blocked cohort but can never return a subset of its members.

Evidence retention does not erase replay safety. A future execute-purge operation
must atomically replace terminal AttemptState with a non-sensitive
AttemptTombstone containing CatalogID, OperationID, Chain, operation/policy/replay
fingerprints, terminal state, and the global start-idempotency alias set. It may
drop payload refs and transition aliases only after evidence deletion commits.
The same atomic unit appends a signed lifecycle destruction assertion whose
digest authenticates that tombstone; an unsigned backend row cannot create a
permanent availability veto.
The operation binding and start aliases are non-reusable coordination records:
later reuse returns an evidence-destroyed conflict with zero callback, never a
new genesis or dangling Found. Release 1 has no execute-purge API, but PurgePlan
cannot promise a deletion shape that would violate this invariant.

Fence tokens are at most MaxInventoryFenceBytes and live at most the hard
MaxFenceLifetime. Length is checked before key lookup/open; decoded count and
bytes are checked before allocation or lifecycle I/O. The newest key seals;
retained verification/decryption keys accept old tokens only through their maximum
lifetime. Restart succeeds only with the same durable BackingID, LogID, and
retained key; another activated backing identity or retired key fails closed. A
bit-identical concurrently served clone is operational split brain and is not a
distinction local token cryptography can prove. Release 1 intentionally
has no execute-purge, key-destruction, or export-delivery API.

FenceKeys advertises a copied, canonical inventory with exactly one primary and
unique `(algorithm, profile, key ID)` tuples. NewControl rejects an empty,
duplicate, ambiguous, or unbounded inventory and requires a positive
CursorLifetime no greater than MaxControlCursorLifetime. InventoryFence and ControlCursor
are distinct envelope/request types and outer formats. SealFence/OpenFence never
accept a control cursor; SealControlCursor/OpenControlCursor never accept a fence.
They use domain-separated AAD and reject cross-protocol output substitution before
trusting decoded claims. Their parsers check the
outer version and `MaxInventoryFenceBytes` or `MaxInventoryCursorBytes` before a
key lookup. Opened plaintext is length-delimited and checked incrementally before
allocating candidate/reference slices. A control cursor encrypts the exact
normalized selection, frozen RequesterCommitment and retained alias-description
set, original ControlRequestDigest, original ControlGrantSpec and
ControlGrantDigest, prior effective ControlGrantSpec, Control-selected AsOf,
immutable cohort revision/byte ceilings, cumulative pages/revisions/bytes,
snapshot/position, issue time and hard expiry, and binds active CatalogRef, LogID, BackingID,
CatalogSet and key generation in AAD. Every continuation resolves and
reauthorizes the current requester and computes the next effective grant as the
intersection of current policy, normalized selection, original grant, prior
effective grant, and current grant; it can only narrow or revoke, and the emitted
cursor freezes that exact result. An origin control cursor seals with
`expiry = min(issued_at + CursorLifetime, grant.ExpiresAt)`; overflow, expired
grant, or an empty interval refuses. Continuations preserve original issue time
and narrow expiry to the minimum of prior expiry, current grant expiry, and every
applicable effective ceiling. Opening
requires `issued_at < expiry <= issued_at + CursorLifetime` and the hard maximum.
Origin creates one immutable bounded store cohort no larger than the minimum
original/effective grant and store ceilings and seals its exact revision/byte
limits. After each accepted Inventory or selection-Verification page, Control
adds exactly one to Pages and adds that page's post-validated retention-cohort
count, whole-revision count, and wire bytes with checked arithmetic. Cohorts must
remain zero for selection verification. The next query carries the prior progress;
the returned page must echo the expected monotonic successor. Reset, jump,
double-count, overflow, changed cohort ceiling, or cumulative one-over refuses and
emits no replacement cursor. Inventory candidate count uses Revisions while
Cohorts counts retention units in the shared SearchProgressView; Limit remains
only the per-page cohort maximum and can never reset MaxCohorts, MaxCandidates,
or MaxBytes.
Removing a retained key early explicitly revokes its outstanding cursors.
Before a control report is committed as evidence, Control derives the typed keyed
ControlContinuationDigest over the exact serialized ControlCursor, complete
decoded ControlCursorClaimView, and its origin request and grant digests. Presence false requires a zero digest; presence true
requires the exact nonzero digest. The authenticated envelope thereby commits the
protocol, algorithm/profile/key generation, nonce/ciphertext, decoded selection,
original/prior-effective ceilings, cohort ceilings, cumulative progress, AsOf,
snapshot/position, issue time, and expiry without publishing cursor bytes.
ControlCursor never contains ControlResultDigest, so the construction is acyclic.

```go
type Control struct{ value control }
type ControlConfig struct {
	Recorder       *Recorder
	Exact          ExactLog
	Attempts       AttemptLog
	AttemptTypes   AttemptTypeState
	Mutations      CatalogMutationLogReader
	Lifecycle      LifecycleLog
	Authority      ControlAuthority
	Denials        DenialLimiter
	Revealer       Revealer
	Verifier       Verifier
	Fences         FenceKeys
	CursorLifetime time.Duration
	Clock          Clock
}

type RevisionRef struct {
	Catalog  CatalogRef
	Revision RevisionID
}

type PlaceHoldCommand struct {
	Hold           HoldID
	Matter         Reference
	Revision       RevisionRef
	Access         ExactControlAccess
	Purpose        Purpose
	Role           Reference
	IdempotencyKey IdempotencyKey
}

type CorrectionCommand struct {
	Original       ItemRef
	Corrected      Draft
	Reason         Reason
	Access         ExactControlAccess
	Purpose        Purpose
	Role           Reference
	IdempotencyKey IdempotencyKey
}

type DisputeCommand struct {
	Original       ItemRef
	Reason         Reason
	Access         ExactControlAccess
	Purpose        Purpose
	Role           Reference
	IdempotencyKey IdempotencyKey
}

type HoldTransitionDisposition uint8
const (
	HoldActivated HoldTransitionDisposition = iota + 1
	HoldReleasedNow
	HoldAlreadyActive
	HoldAlreadyReleased
)
type HoldResult struct{ value holdResult }
func (r HoldResult) Disposition() HoldTransitionDisposition
func (r HoldResult) Projection() HoldProjectionStateView
func (r HoldResult) Record() RecordResult

type ReleaseHoldCommand struct {
	Hold           HoldID
	Revision       RevisionRef
	Access         ExactControlAccess
	Purpose        Purpose
	Role           Reference
	IdempotencyKey IdempotencyKey
}

type FenceSealRequest struct{ value fenceSealRequest }
type FenceOpenRequest struct{ value fenceOpenRequest }
type FenceEnvelope struct{ value fenceEnvelope }
type ControlCursorSealRequest struct{ value controlCursorSealRequest }
type ControlCursorOpenRequest struct{ value controlCursorOpenRequest }
type ControlCursorEnvelope struct{ value controlCursorEnvelope }

type FenceKeyDescription struct {
	Algorithm string
	Profile   string
	KeyID     string
	Primary   bool
}

type FenceKeys interface {
	Descriptions() []FenceKeyDescription
	SealFence(context.Context, FenceSealRequest) (FenceEnvelope, error)
	OpenFence(context.Context, FenceOpenRequest) ([]byte, error)
	SealControlCursor(context.Context, ControlCursorSealRequest) (ControlCursorEnvelope, error)
	OpenControlCursor(context.Context, ControlCursorOpenRequest) ([]byte, error)
}

func (r FenceSealRequest) Plaintext() []byte
func (r FenceSealRequest) AAD() []byte
func (r FenceOpenRequest) Envelope() FenceEnvelope
func (r FenceOpenRequest) AAD() []byte
func NewFenceEnvelope(string, string, string, []byte, []byte) (FenceEnvelope, error)
func (v FenceEnvelope) Algorithm() string
func (v FenceEnvelope) Profile() string
func (v FenceEnvelope) KeyID() string
func (v FenceEnvelope) Nonce() []byte
func (v FenceEnvelope) Ciphertext() []byte
func (r ControlCursorSealRequest) Plaintext() []byte
func (r ControlCursorSealRequest) AAD() []byte
func (r ControlCursorOpenRequest) Envelope() ControlCursorEnvelope
func (r ControlCursorOpenRequest) AAD() []byte
func NewControlCursorEnvelope(string, string, string, []byte, []byte) (ControlCursorEnvelope, error)
func (v ControlCursorEnvelope) Algorithm() string
func (v ControlCursorEnvelope) Profile() string
func (v ControlCursorEnvelope) KeyID() string
func (v ControlCursorEnvelope) Nonce() []byte
func (v ControlCursorEnvelope) Ciphertext() []byte

type InventoryFence struct{ value inventoryFence }
type ControlCursor struct{ value controlCursor }
func ParseInventoryFence([]byte) (InventoryFence, error)
func (f InventoryFence) Bytes() []byte
func ParseControlCursor([]byte) (ControlCursor, error)
func (c ControlCursor) Bytes() []byte

type InventoryRequest struct {
	Purpose         Purpose
	Role            Reference
	Scope           ScopeSelector
	Resources       []Resource
	Actions         []Action
	Coordinates     []EvidenceSelector
	Retentions      []RetentionClass
	Classifications []Classification
	Limit           uint32
	MaxCohorts      uint32
	MaxCandidates   uint32
	MaxBytes        uint64
	Cursor          ControlCursor
}
type InventoryCandidateView struct {
	Cohort     RetentionCohortID
	Revision   RevisionRef
	Resources  []Resource
	Actions    []Action
	Classifications []Classification
	Retention  RetentionClass
	EligibleAt time.Time
	Held       bool
}
type InventoryReport struct{ value inventoryReport }
func (r InventoryReport) Candidates() []InventoryCandidateView
func (r InventoryReport) Cohorts() []RetentionCohortView
func (r InventoryReport) Truncated() bool
func (r InventoryReport) Continuation() ControlCursor
func (r InventoryReport) AsOf() time.Time
func (r InventoryReport) Fence() InventoryFence
func (r InventoryReport) Evidence() Receipt

type VerifyRequest struct {
	Purpose         Purpose
	Role            Reference
	Access          ExactControlAccess
	Revisions       []RevisionRef
	Time            TimeWindow
	Limit           uint32
	Cursor          ControlCursor
}
type VerificationStatus uint8
const (
	IntegrityValid VerificationStatus = iota + 1
	IntegrityInvalid
	IntegrityMissingKey
	IntegrityMissingSeal
	IntegrityUnsupported
	IntegrityMalformed
	IntegrityIncomplete
	IntegrityUnknownCatalog
)
type VerificationResult struct {
	Revision RevisionRef
	Status   VerificationStatus
}
type VerificationReport struct{ value verificationReport }
func (r VerificationReport) Results() []VerificationResult
func (r VerificationReport) Complete() bool
func (r VerificationReport) Continuation() ControlCursor
func (r VerificationReport) Evidence() Receipt

type PurgeRequest struct {
	Purpose Purpose
	Role    Reference
	Fence  InventoryFence
}
type PurgeCandidateView struct {
	Cohort    RetentionCohortID
	Revision  RevisionRef
	Resources []Resource
	Actions   []Action
	Classifications []Classification
	Retention RetentionClass
}
type PurgePlan struct{ value purgePlan }
func (r PurgePlan) Eligible() []PurgeCandidateView
func (r PurgePlan) Cohorts() []RetentionCohortView
func (r PurgePlan) Fence() InventoryFence
func (r PurgePlan) AsOf() time.Time
func (r PurgePlan) Evidence() Receipt
func (r PurgeRequest) WithFence(InventoryFence) PurgeRequest

func NewControl(ControlConfig) (*Control, error)
func (c *Control) Correct(context.Context, CorrectionCommand) (RecordResult, error)
func (c *Control) Dispute(context.Context, DisputeCommand) (RecordResult, error)
func (c *Control) PlaceHold(context.Context, PlaceHoldCommand) (HoldResult, error)
func (c *Control) ReleaseHold(context.Context, ReleaseHoldCommand) (HoldResult, error)
func (c *Control) Inventory(context.Context, InventoryRequest) (InventoryReport, error)
func (c *Control) Verify(context.Context, VerifyRequest) (VerificationReport, error)
func (c *Control) PlanPurge(context.Context, PurgeRequest) (PurgePlan, error)
```

Hold transitions use this complete store-serialized state machine. “Replay” means
the exact revision or idempotency replay door and always returns the original
HoldResult; it never appends or advances a projection.

| Command | Existing membership | Matter identity | Result | Evidence/projection |
|---|---|---|---|---|
| place | absent | HoldID unbound or one retained alias matches | `HoldActivated` | append effective transition; active count +1; epoch +1; recompute set digest |
| place | active | one retained alias matches | `HoldAlreadyActive` | a new request appends bounded no-op evidence; projection and epoch unchanged |
| place | released | one retained alias matches | conflict | no append; membership cannot resurrect |
| place | any | HoldID aliases identify another or multiple matters | conflict/corruption | no append and no projection change |
| release | active | already bound | `HoldReleasedNow` | append effective transition; active count -1; epoch +1; recompute set digest |
| release | released | already bound | `HoldAlreadyReleased` | a new request appends bounded no-op evidence; projection and epoch unchanged |
| release | absent | absent or non-disclosing | not found | no append and no projection change |

Genesis is membership Absent, count/epoch zero, HoldSetDigestOf for that exact
LogID, target RevisionRef, and empty identity set, and a zero transition head. An effective candidate sets
Head to its own complete RevisionRef; a no-op candidate keeps expected and result
Head byte-equal. Every nonzero head is the RevisionRef of the last effective
transition in the supplied proof.

Control derives the table row from an authenticated HoldState chain. The store
locks the HoldID aliases, membership, and revision hold projection in one
canonical order with the append and CASes every expected state component.
Concurrent same-key commands collapse to one replayed result. A different-key
command racing the same state gets a stale-state conflict and must reauthorize
and rebuild its candidate; it may then produce the appropriate effective or
no-op result. Competing matters under one HoldID produce one winner and one
conflict. Place/release races expose a legal serial order only: release after
place can deactivate it; release before any visible place is not found. No
outcome can decrement below zero, reuse an epoch, substitute another transition
head, or derive membership from matter decryption.

Every command/request carries a bounded purpose and role, plus an optional
idempotency key
where it can write. Operation identity comes only from the selected ControlPolicy
ContextPolicy; a keyed command therefore requires its stable admitted OperationID
and no Correction, Dispute, or hold command accepts a free one. Before authority
or store I/O, PlaceHold validates command/matter shape, derives the keyed HoldID and
matter commitments, and builds the dedicated HoldAuthorizationView; ReleaseHold
builds the same view with absent matter. Control derives HoldRequestDigest from the
exhaustive origin request, exposes it in ControlRequestView, and requires the grant's
Hold field to echo it byte-for-byte. Only then may a keyed command perform its
committed-only IdempotencyLookup. A valid Found result is exact-inspected and
returns the original signed outcome even if a later hold transition changed the
current projection; AbsentNow proceeds and remains race-safe only because Append
repeats keyed matching atomically. Only after absence may the exact sealed ceilings
be used to read and authenticate prior transitions and build the signed candidate. A change
to requester, role, purpose, command, target, HoldID, matter presence, or matter
commitment invalidates the decision before append. NewControl
requires Recorder, ExactLog, and LifecycleLog to have one exact
process-local Backing, durable BackingID, durable LogID, and CatalogSet, rechecks
them on every operation, uses Recorder's already validated private identity,
tokenization, and write services without exposing Writer, and uses current policy
to authorize every retained CatalogRef. ExactInspection and StableSearch must
equal SupportSupported. A current policy declaring HoldPlaced or HoldReleased
also requires Holds == SupportSupported; one declaring PurgePlanned requires
PurgePlanning == SupportSupported. Unstated and Unsupported are equally fatal,
and every corresponding limit is nonzero and no greater than the kernel hard cap.
Clock is required, concurrently safe, and
used once per invocation. An origin Inventory call uses that single normalized
instant as retention AsOf. A continuation or PlanPurge preserves the authenticated
origin AsOf; its current clock sample is used only for expiry and revocation checks,
never to move eligibility. Each
public method constructs exactly one normalized target
variant—item, hold request, bounded inventory selection,
explicit-or-time verification selection, or exact purge fence—for
ControlRequest; zero or mixed targets have no spelling. Correction and dispute
carry their exact action-scoped declared Reason; every other action requires zero
Reason. Inventory and selection verification bind the caller's normalized
nonzero Limit in ControlSelectionView; explicit verification and every other
non-selection target require zero Limit. A hold target first
derives HoldRequestDigest and then includes that exact nonzero value in
ControlRequestDigest; every non-hold action requires it to be zero. It selects the declared
ControlPolicy, resolves ContextResolver exactly once, filters/copies that logical
requester, derives the required active/retained requester aliases, and passes the
frozen request explicitly to ControlAuthority on a context that retains only
Deadline, Done, normalized Err, and normalized Cause. The returned decision must have been created
by AllowControl or DenyControl for that exact hidden request identity; copied,
stale, cross-request, zero, and application-constructed values are refused.
Origin control calls require zero origin digests. A continuation opens its token
before authority/store I/O, derives the exact nonzero InputContinuation from that
encrypted token plus its authenticated origin request/grant, and carries the
authenticated nonzero original ControlRequestDigest and ControlGrantDigest;
current narrowing never recomputes or replaces any of them. Origin requires all
three fields zero.
ControlGrantSpec roles, catalogs, scopes, resources, record actions, explicit
evidence-coordinate selectors, control action, retention classes,
classifications, expiry, revision/candidate
count and byte ceilings are validated
under the action-specific rules below and then sealed. RequestedCoordinates
returns copied sealed selectors from this exact request, not lossy views. An
authority can echo them or call NarrowActions and retain a subset; AllowControl
rejects a rebuilt view, foreign-origin selector, changed coordinate/match, broadened
action, duplicate, or selector not present in the request. This makes dynamic
exact-subject narrowing implementable without recovering typed IDs from canonical
Reference strings. ExactLog, LifecycleLog, Revealer, Verifier, FenceKeys, denial
limiter, and private evidence Writer all
receive the same value-free context plus explicit copied private inputs; none can
re-resolve caller context.

InventoryRequest.Actions is a required nonempty canonical upper bound and is
copied into ControlSelectionView.Actions. Its effective intersection with
ControlGrantSpec.RecordActions is copied into InventoryQueryView.Actions before
LifecycleLog.Inventory applies Limit; every candidate and returned
InventoryCandidateView is all-of post-validated against that same action ceiling.
InventoryRequest.Coordinates has exact empty-set semantics, never wildcard
semantics: an empty set can match only an authenticated revision summary with no
subject or searchable-target coordinate. Any summary containing one or more such
coordinates requires all of them to be covered by the effective exact or
declaration-bounded selectors before it can consume the store Limit. This keeps
coordinate-free lifecycle evidence inventoryable without making an omitted
selector authorize a coordinate-bearing record.
For either VerifyRequest branch, ControlTargetView.Access is the canonical source
of Scope, Resources, Actions, Coordinates, and Classifications. Those members are
copied byte-for-byte into ControlTargetView.Selection, whose Retentions is empty;
the target constructor recomputes canonical views and rejects any divergence
before SelectionDigest, authority, ExactLog, or LifecycleLog I/O. Explicit
verification additionally places only Revisions in Selection; selection
verification additionally places only Time and Limit. Every unused selection
member is zero.

| Control target | Grant relation |
|---|---|
| inventory or selection verification | every caller-selected dimension is an upper bound; the grant is its intersection with current policy and may only narrow |
| exact item dispute/correction, hold target, or explicit verification | ExactControlAccess supplies nonempty origin resource/action/classification ceilings and a literal possibly-empty origin coordinate set; the grant may only narrow them, ExactQuery or HoldStateQuery applies them before lookup, and any uncovered returned coordinate or outside target is non-disclosing |
| correction item | in addition to exact-target ceilings, Correction is required and byte-equal to the origin proposal digest |
| hold transition | the dedicated target binds command, target revision, keyed HoldID, and keyed matter presence/value; Hold is required and byte-equal to the origin HoldRequestDigest, after which the sealed ceilings narrow the authenticated prior-state read |
| inventory-fence purge plan or continuation | the new grant intersects current policy plus every encrypted original and prior-effective selection/grant ceiling; it may only narrow or revoke and can never reintroduce a removed dimension |
| every non-correction action | Correction must be zero |
| every non-hold action | Hold must be zero |

Roles and requested ControlAction always match exactly; expiry and count/byte
limits never exceed request, policy, hard, store, or prior continuation ceilings.
MaxCohorts is positive only for inventory and its continuation/purge lineage and
is zero for every other control action; MaxCandidates bounds its flattened member
revisions independently.
Every whole-revision result satisfies `authorization summary ⊆ sealed grant` with
all-of semantics for resources, record actions, classifications, scope, subjects, and searchable
targets. EvidenceSelector is an immutable logical ceiling from a sealed resource
or event declaration. EvidenceExact carries one bounded logical reference;
EvidenceAllDeclared is the only wildcard and covers only its exact coordinate
kind, declaration resource, admitted action set, mode, and classification. Control
derives operational retained tokens/commitments privately. Empty selectors cover
nothing, and subject and event-target selectors cannot substitute. There is no
any-item match.

CorrectionCommand requires one nonzero immutable manual-event Draft from the
current catalog. Before authorization or lookup, Control validates its current
declaration and builds a value-free CorrectionProposalView. A current correction
proposal whose event-target declaration is AsRedacted is not
correction-eligible and is refused before authority or ExactLog I/O. Its
CorrectionProposalDigestOf supplies the only proposal preimage and invokes
Config.Semantics under its distinct fixed domain over the complete logical draft.
It is never raw SHA-256 and CorrectionProposalView.Digest never enters its own
preimage. The authority
view lists the copied logical target and safe declaration metadata but exposes no
store token, ciphertext, or raw protected coordinate. ControlRequest origin binds
that proposal and an allow decision must echo its exact digest in
ControlGrantSpec; a zero, foreign, or different digest refuses before lookup.
Entity, access, lifecycle, foreign-catalog, or already-linked draft shapes refuse
before lookup. After authenticated ExactLog lookup, an entity original requires
the proposal's Resource and logical target to equal its searchable Subject. An
event original additionally requires the same stable Resource and Action
declaration across the retained lineage and equality with its logical Target; a
redacted record-era target or incompatible declaration returns the same
non-disclosing correction-ineligible refusal as any coordinate mismatch. Both
forms require compatible record-era retention and consequence. Control adds the
immutable CorrectionOf link to the exact original item; correction semantics and
idempotency bind original ItemRef, proposal digest, stable reason, and frozen
context. The corrected event keeps its declared fields and can never enter entity
reconstruction. Substitution of any proposal component after the decision is an
origin failure. Dispute carries
only its stable declared reason code and an immutable link to the original item;
neither command rewrites, hides, or changes the original assertion.

When CorrectionAppended is declared, NewControl requires a Revealer whose copied
description inventory covers every retained protection description that a
correction-eligible original target may require. The post-lookup comparison
decodes and, for AsProtected or AsIndexedProtected, reveals the authenticated
record-era target on a value-free context, compares its canonical logical bytes
to the proposal, and immediately discards the temporary plaintext. AsPlaintext
and AsToken use their authenticated canonical value or retained query-token
aliases. AsRedacted is never compared, guessed, inherited, or broad-scanned. A missing or
retired key, malformed envelope, wrong description, or reveal failure releases no
decision, mutation, or target-existence signal. Revealer may be nil only when the
current ControlPolicy does not declare CorrectionAppended.

VerifyRequest is a strict one-of: a bounded explicit Revisions set uses ExactLog,
while a resource/time selection with no explicit revisions uses
LifecycleLog.Verification;
mixed or empty targets refuse before authority. Verify returns one ordered
VerificationResult per admitted requested or selected revision.
Both branches require a valid nonempty ExactControlAccess. The explicit branch
requires zero Time, Limit, and Cursor. The selection branch requires zero
Revisions, a positive Limit, and a normalized TimeWindow. Zero Time normalizes to
AllObservedTime; bounded ObservedTimeAxis and RecordedTimeAxis preserve their UTC
endpoints and exact open/closed RangeBounds, while OccurredTimeAxis refuses before
authority or LifecycleLog I/O. Selection results use a fixed OldestFirst order by
selected-axis instant then
RevisionRef canonical bytes. NewVerificationPage rejects any other order. That
exact TimeWindowView and fixed order are bound through ControlSelectionView,
SelectionDigest, request/grant, VerificationQueryView, cursor, result evidence,
and store post-validation.
Valid means manifest, envelope digest, integrity digest, predecessor shape, and
required seal all verify. Invalid authentication, missing key, missing required
seal, unsupported record-era algorithm/profile, malformed bounded evidence,
incomplete/missing requested evidence, and unknown catalog remain separate safe
statuses with no raw row bytes. A verifier backend outage, cancellation,
authority failure, hostile oversized answer, or inability to commit verification
evidence is an operation-level error and releases no report. Invalid evidence is
therefore reportable without mistaking infrastructure failure for tampering.

Control asks ControlAuthority before Writer, ExactLog, or LifecycleLog I/O.
The authorized complete-summary resource, action, classification, and protected/
tokenized scope/subject/target ceilings are carried into InventoryQueryView and
its query/fence digest, so an external
store can apply the grant before its limit. Explicit-target verification uses only
ExactLog.Inspect. Selection verification uses the distinct sealed
VerificationQuery, which carries SelectionDigest, origin request/grant, every
catalog/resource/retention/classification/protected coordinate, exact time axis,
bounds and endpoints,
page/byte/cumulative ceilings, immutable snapshot/expiry, and position before
LifecycleLog.Verification. NewVerificationPage origin-validates those fields with
the same stable cohort rules as history; it cannot be substituted with StoreQuery
or InventoryQuery. ExactQuery carries its constraints before its byte limit and
cannot express a broad scan. Every returned header/candidate is then fully post-validated
before a hold/correction/dispute append or report. A foreign-scope target yields
one non-disclosing denial. Mutation methods use the ordinary receipt/retry state
machine. Read methods refuse ambient Writer authority/group, run ExactLog/LifecycleLog
against committed state, prepare and copy their report, then cross the same
private standalone committed-evidence barrier as History. The returned page or
report includes that Committed receipt; NotWritten, Unconfirmed, or
InCallerTransaction releases no result. `PlanPurge` requires the exact
InventoryFence issued for its normalized request, rechecks LogID, BackingID,
active CatalogRef, CatalogSet, requester commitment and aliases, original
action/purpose/role, normalized selection and SelectionDigest, original request/
grant digests, original and prior-effective ControlGrantSpec, snapshot/as-of time,
every candidate eligibility digest, hold epoch, and active-set digest. It resolves
the current requester, requires an admitted retained-alias match, and computes the
new effective grant as the intersection of current policy, normalized selection,
original grant, prior-effective grant, and current grant. A missing claim member,
digest mismatch, widening, revocation, empty intersection, or attempted action
reintroduction refuses before lifecycle I/O; a newly issued fence freezes the new
effective grant. Only then does Control record `purge_planned`, returning after
that evidence commits.
Every InventoryCandidateView and PurgeCandidateView returns canonical copied
Resources, Actions, and Classifications from the authenticated whole-revision
summary. They must be byte-equal to the fenced/store candidate and subsets of the
effective grant; omission, reordering, or action substitution invalidates the
whole result before evidence is committed.

Before any successful control result crosses the public boundary, Control derives
ControlRequestDigest from the frozen origin, ControlGrantDigest from the sealed
decision, and the strict action-appropriate ControlResultDigest from the prepared
result. For a selection it first derives and rechecks SelectionDigest; for an
inventory fence it seals the complete claim, derives FenceDigest from the exact
envelope plus decoded claim, and only then forms the result, with no result-to-fence
self-reference. The committed control evidence binds all three. Inventory binds exact AsOf,
candidate refs, truncation, continuation and fence; verification binds every ref/
status and completeness; purge binds cohort refs, AsOf and fence; mutations bind
the enclosing-revision marker and normalized complete hold projection where
applicable; the containing signed header supplies the exact RevisionRef. Any non-Committed
evidence settlement discards the prepared result and its continuation/fence.

A denied control request always remains denied. Bounded denial evidence is
eligible only when ControlDenied is declared and the configured DenialLimiter
returns true for the origin-bound DenialAttempt on the value-free context. The
same exact NotConfigured, Suppressed, LimiterFailed, NotWritten, Unconfirmed, and
Committed mapping as History applies. A limiter panic is re-raised unchanged and
produces no returned state or append. An admitted attempt may append one bounded
denial item but can never grant access or recursively generate another control
request.

### 2.10 Exact CRUD adapter contract

Release alpha exposes one sealed composition rather than a general audit
middleware and a public admission protocol:

```go
package auditcrud

func Secured[M any, ID comparable](
	*audit.Recorder,
	*audit.ResourcePolicy[M, ID],
	security.Policy[M, ID],
) crud.Middleware[M, ID]
```

The supported application order is:

```go
orders := Orders.Bind(
	source,
	auditcrud.Secured(
		recorder,
		OrdersAudit,
		security.Combine(
			tenancyrow.Policy[Order, OrderID](tenants, ownership),
			security.RequirePermission[Order, OrderID]("orders:write"),
		),
	),
	faults.Enrich(),
)
```

`Secured` validates nil/typed-nil recorder, resource, and security-policy
arguments at factory time. Applying the middleware validates exact model metadata,
catalog membership, source identity, Writer capabilities, and the inner effects
needed by the declared entity actions. Invalid factory arguments panic at the
factory; invalid repository wiring panics while the blueprint binds, before
callbacks or I/O. There is no latent first-request error and no unsecured audit
adapter entry.

The composite constructs this closed graph:

```text
returned terminal
    -> security.Gate(policy)
        -> private audited receiver
            -> caller inner (faults -> sqlrepo)
```

The terminal and receiver implement every `crud.Core` method explicitly and do
not embed `crud.Core`; a new core verb therefore breaks their build-time inventory
instead of silently bypassing audit. The terminal does not implement
`crud.Nexter`, does not expose the Gate or receiver, and returns the already
validated source directly. The receiver is reachable only by the private Gate.
The terminal has a structural `MutationBoundarySealed()` marker on its
unexported concrete type. `faults.Enrich` checks its exact direct inner through a
local anonymous interface and rejects `faults -> Secured` at bind time. The
documented `Secured -> faults -> repository` order remains valid.

D-061 is amended narrowly: a sealed authority firewall may intentionally omit
`Next` when navigation would expose an executable inner boundary. Unknown
application middleware cannot be inserted inside the composite. Middleware
outside the returned terminal may still decorate its public surface, but cannot
recover the Gate or receiver.

Security Gate remains the single implementation of authorization, scope,
relation-scope, immutable-field, inspection, hidden-row, and tombstone rules.
It authorizes before invoking the receiver. Policy callbacks and the Gate's
admission reads happen before the root mutation transaction; the receiver carries
the copied scope/relation/snapshot fence into that transaction and revalidates it.
The atomic claim covers the business mutation and audit evidence, not arbitrary
policy callback I/O. Transactional authorization reads require a future explicit
Gate seam and are not claimed by release alpha.

The release adds only two neutral provenance helpers:

```go
package crud

func SourceBoundExecutorFor(
	context.Context,
	any,
) (Executor, bool, error)
```

`(nil, false, nil)` means there is no binding and the adapter may open an owned
transaction. `(executor, true, nil)` means the executor was selected by an exact
declared datasource association. Poison, strict mismatch, and a sole
`WithUnsafeExecutor` fallback return an error. A found non-transaction executor
still fails the root-transaction proof when joining an existing frame. Existing
`ExecutorFor` behavior remains source-compatible.

```go
package crudsql

func TopLevelTransaction(crud.Executor) (*sql.Tx, bool)
```

It returns true only for the direct `*crudsql.Tx` created by the named
`crudsql.DB.Begin`. A framework savepoint, `crudsql.From(rawTx)`, an
unwrapped/wrapped transaction, `savepoint.Tx() -> From(...)`, a pool, or a
connection returns false. Database/sql cannot reveal raw SAVEPOINT statements
issued out of band through a root transaction; doing so violates the caller
contract and is outside the advertised detection claim. D-082 records this exact
provenance limit.

The child package needs one internal bridge because `auditcrud` may not receive
the Recorder's unrestricted draft/stage authority. It is internal to audit's
package tree and adds no consumer API:

```go
package auditcrudbridge

type Spec struct{ value spec }
type Batch struct{ value batch }
type Guard struct{ value guard }
type Carrier struct{ value carrier }

type Mutation func(context.Context) (Batch, error)
type Runner func(context.Context, Mutation) error
type Prepare func(context.Context, Spec) (Runner, error)

func NewSpec(
	policy any,
	operation any,
	subjects []any,
	generatedSubject bool,
) (Spec, error)
func NewBatch(...any) (Batch, error)
func NewCarrier(Prepare) Carrier
func Preflight(context.Context, any, Spec) (Guard, error)
func (Guard) Run(context.Context, Mutation) error
```

`Recorder` embeds a private alias of `Carrier`. `Preflight` recognizes a
bridge-private promoted method that application code cannot name; it uses no
registry or context token. Recorder's prepare callback validates the actual
`ResourcePolicy`, `OperationMember`, `SubjectRef`, and `EntityDraft` values
and returns the one allowed atomic runner. Carrier and Guard share one
concurrency-safe use cell. A zero, copied, replayed, foreign-recorder, wrong-kind,
or already-consumed Guard/Batch refuses before mutation I/O. The bridge imports
only the standard library, stores no audit payload, and cannot construct audit
values itself.

The dependency graph stays acyclic:

```text
crud <- security <- tenancyrow
crud <- faults
audit/internal/auditcrudbridge -> standard library
audit -> crud + auditcrudbridge
audit/auditcrud -> audit + auditcrudbridge + crud + security
audit/auditpg -> audit + crud + crudsql
```

For every authorized mutation the receiver performs pure bridge preflight, then
either opens one owned root transaction or joins the exact active frame created by
this Recorder's `Within`. A caller-owned transaction without that frame, an
unsafe fallback, a foreign source, a raw transaction, a savepoint, or a
non-transaction binding is refused before business mutation. Within one
`Guard.Run`, the receiver locks/revalidates the exact entity state, invokes the
business effect once, derives evidence from the actual persisted result, stages
or appends it, and settles only with the root transaction. Audit failure rolls
back the business mutation.

An inner `faults.Enrich` may use a framework savepoint only around the business
statement. It must release on success or roll back before result capture,
entity-head work, stage, or append. Begin/release/rollback/probe failure poisons
the root guard. The savepoint never becomes audit execution or settlement
authority and never escapes.

| Method/capability | Alpha behavior |
|---|---|
| metadata and reads | pass through the hidden Gate exactly once; no entity evidence |
| `Tx` | pass through the Gate exactly once; audited writes still require this Recorder's active `Within` when joining |
| no-ID `Save` | one root mutation; returned persisted row is `entity.created` |
| assigned-ID `Save` through `ScopedSave` | revalidate the Gate fence; create or truthful change/first-touch baseline |
| `Update` | evaluate caller options once, append security constraints last, lock exact before-state, diff persisted result |
| `Delete` / hidden `DeleteScoped` | lock exact victims, require exact count, emit declared soft/hard delete |
| optional `Restore` / hidden `RestoreScoped` | load exact tombstones, restore, reload exact live set, require exact count |
| safe `ExistsUnscoped`, `SupportsRestore` | preserved through the Gate where its hidden-row rules need them |
| `SaveOnly`, `SaveAll`, `UpdateAll`, `DeleteAll` | refuse before Gate callback, pre-read, transaction, mutation, or append |
| `Create`, `Replace`, batch/scoped effects, `LoadTombstones`, `Next` | not advertised by the terminal |

The receiver privately implements only the exact `SaveScoped`,
`DeleteScoped`, `RestoreScoped`, and `LoadTombstones` effects needed by Gate.
Write-only and bulk APIs cannot produce complete per-entity evidence in alpha and
remain fail-closed. Endpoint intent such as replace remains an explicit business
event rather than an inferred entity action.

Security Gate receives two alpha correctness fixes independently of audit:
`Update` builds caller options exactly once and appends policy
scope/relation/snapshot constraints last so an assigning hostile option cannot
erase narrowing; `Restore` authorizes before the empty-ID shortcut. The default
port restorable service forwards an empty `RestoreMany` to the repository instead
of returning before Gate, so the same authorization rule holds through the real
service path. Positive controls prove the ordinary authorized paths still execute
once.

Generated-ID Save must roll back when the resulting subject has a non-genesis
audit head. Assigned Save distinguishes create from existing state through the
Gate's exact hidden-row algorithm; a scoped miss is never guessed to be create.
For an existing row with no audit head, Save/Update emits a complete truthful
`BaselineChanged` state at the actual mutation time, not a synthetic creation.
No-op/missing input emits no revision. Count/ID mismatch, changed snapshot,
head-CAS loss, unsupported action, codec/protection failure, or source proof loss
rolls back. A hard-delete head terminally closes the subject identity.

Alpha tests cover:

1. external-package DX with combined tenancy/security and inner faults;
2. nil/zero factory values and wrong metadata/source/capabilities without I/O;
3. correct order, outer-faults bind refusal, and absence of `Next`;
4. every unsupported method before callbacks, reads, Begin, mutation, or append;
5. authorization/scope/inspection denial without Begin, mutation, or append;
6. hostile options, callback/option single evaluation, and hidden-row resistance;
7. Save create/assigned-change, Update, Delete, and Restore success, denial, no-op,
   business failure, audit failure, and rollback;
8. owned root, exact active `Within`, unsafe/foreign/raw/savepoint/caller-Tx
   refusals, and faults-savepoint completion before capture/append;
9. bridge zero/copy/replay/cross-recorder/wrong-batch refusals plus one executing
   positive control;
10. reflection inventory for all `crud.Core` methods and optional create/replace/
    batch/scoped effects.

Broad combinatorial substitution and property matrices remain in the final edge
hardening section; they do not block the usable CRUD alpha.
### 2.11 Public memory and conformance profiles

`audit/auditmemory` freezes this external adoption surface:

```go
package auditmemory

type LogSpec struct {
	Limits audit.LimitSpec
}

type Log struct{ value log }
func NewLog(LogSpec) (*Log, error)

type Deployment struct{ value deployment }
func NewDeployment(*Log) (*Deployment, error)
func (d *Deployment) Capabilities() audit.Capabilities
func (d *Deployment) Limits() audit.Limits
func (d *Deployment) Backing() audit.Backing
func (d *Deployment) BackingID() audit.BackingID
func (d *Deployment) LogID() audit.LogID
func (d *Deployment) Catalogs() audit.StoreCatalogState
func (d *Deployment) InstallCatalog(context.Context, audit.Manifest, audit.CatalogChangeRef) error
func (d *Deployment) ActivateCatalog(context.Context, audit.CatalogRef, audit.CatalogRef, audit.CatalogChangeRef, audit.CatalogActivationProof) error
func (d *Deployment) VerifyCatalogs(context.Context, []audit.Manifest) error
func (d *Deployment) CatalogMutations(context.Context) (audit.CatalogMutationLog, error)
func (d *Deployment) Close() error

type Spec struct {
	Log   *Log
	Clock func() time.Time
}

type Store struct{ value store }
func New(Spec) (*Store, error)
func (s *Store) AttemptState(context.Context, audit.AttemptStateQuery) (audit.AttemptStateResult, error)
func (s *Store) AttemptTypeState(context.Context, audit.AttemptTypeStateQuery) (audit.AttemptTypeStateResult, error)
func (s *Store) CatalogMutations(context.Context) (audit.CatalogMutationLog, error)

type Tx struct{ value tx }
func (s *Store) Begin(context.Context) (*Tx, error)
func WithTransaction(context.Context, *Tx) context.Context
func (tx *Tx) Commit(context.Context) error
func (tx *Tx) Rollback(context.Context) error

var _ audit.DeploymentStore = (*Deployment)(nil)
var _ audit.Store = (*Store)(nil)
```

Log owns its generated process-local Backing, cryptorandom nonzero BackingID and
LogID, catalog manifests/active head, immutable data, aliases, entity heads, hold
projection, attempt states/type counters/anchors/current-head reverse index,
retention snapshots/cohort guards, and positions. Sibling Store values over one Log preserve all of that
state; New never resets it. Store is a lightweight complete runtime `audit.Store`
handle whose dynamic type has no CatalogAdmin methods. Deployment is a distinct
concrete handle implementing CatalogAdmin and Closer only; NewDeployment never
returns or embeds Store. NewLog/New/NewDeployment validate and allocate only,
perform no external I/O, and start nothing.
Zero LogSpec limits choose documented kernel-bounded defaults. A supplied nonzero
limit must be positive and no wider than the kernel ceiling. Clock defaults to
time.Now and is called concurrently only for store RecordedAt/snapshot facts.

WithTransaction binds a private neutral crud.Executor owned by Tx through crud's
source-bound executor binding, never an auditmemory-private context key. No public
business Exec/Query door is exposed. BindTransaction accepts only the live private
executor of a Tx from the same Log. Commit atomically publishes all staged audit state;
Rollback publishes none; either operation is one-shot. Store.Close closes only
that handle and neither Log nor a caller-owned Tx. Deployment.Close likewise closes
only the deployment handle. Closing and reopening either handle preserves the exact
catalog mutation sequence owned by Log. Conformance rejects both a runtime dynamic
type that satisfies audit.CatalogAdmin and a deployment dynamic type that satisfies
audit.Store.

`audit/audittest` freezes this conformance and application-policy surface:

```go
package audittest

type Tx interface {
	Commit(context.Context) error
	Rollback(context.Context) error
}

type DeploymentHarness interface {
	OpenDeployment(*testing.T) audit.DeploymentStore
	OpenRuntime(*testing.T) audit.Store
}

type Factory struct {
	NewHarness         func(*testing.T) DeploymentHarness
	Begin              func(*testing.T, context.Context, audit.Store) (context.Context, Tx)
	Fail               func(*testing.T, audit.Store, audit.StoreOutcome) bool
	Tail               func(*testing.T, audit.Store) audit.StorePosition
	UnparsablePosition func(*testing.T, audit.Store) audit.StorePosition
	Window             time.Duration
}

type Verdict uint8
const (
	VerdictPassed Verdict = iota + 1
	VerdictFailed
	VerdictNotCertified
)

type SectionResult struct {
	Name    string
	Verdict Verdict
	Reason  string
}

type Report struct{ value report }
func Run(*testing.T, Factory) Report
func (r Report) Sections() []SectionResult
func (r Report) Certified() uint32
func (r Report) Failed() bool

type DeclarationFixture func() (audit.CatalogSpec, []audit.Declaration)
func Declarations(*testing.T, DeclarationFixture)
func Subjects[M any, ID comparable](*testing.T, *audit.ResourcePolicy[M, ID], ...ID)
func CodecRoundTrip[V any](*testing.T, audit.Codec[V], func(V, V) bool, ...V)
type SubjectCase[ID comparable] struct{ value subjectCase[ID] }
type EventCase[E any] struct{ value eventCase[E] }
type AttemptStartCase[S any] struct{ value attemptStartCase[S] }
type AttemptCheckpointCase[C any] struct{ value attemptCheckpointCase[C] }
type AttemptFinishCase[F any] struct{ value attemptFinishCase[F] }
type AttemptGoldenCases[S, C, F any] struct {
	Starts      []AttemptStartCase[S]
	Checkpoints []AttemptCheckpointCase[C]
	Finishes    []AttemptFinishCase[F]
}
func SubjectGolden[ID comparable](audit.FixtureName, ID) SubjectCase[ID]
func EventGolden[E any](audit.FixtureName, E) EventCase[E]
func AttemptStartGolden[S any](audit.FixtureName, S) AttemptStartCase[S]
func AttemptCheckpointGolden[C any](audit.FixtureName, C) AttemptCheckpointCase[C]
func AttemptFinishGolden[F any](audit.FixtureName, audit.AttemptCompletion[F]) AttemptFinishCase[F]
func CheckSubjectGoldens[M any, ID comparable](*testing.T, *audit.ResourcePolicy[M, ID], ...SubjectCase[ID])
func CheckEventGoldens[E any](*testing.T, *audit.EventType[E], ...EventCase[E])
func CheckAttemptGoldens[S, C, F any](*testing.T, *audit.AttemptType[S, C, F], AttemptGoldenCases[S, C, F])
```

Factory.NewHarness returns one isolated persistent backing whose schema preparation
and cleanup belong to the factory. Run compiles its own bounded conformance catalog
and external change references. It uses DeploymentStore alone for install and a
zero-gate genesis activation. After one active catalog exists, each later step
opens one temporary checked runtime handle solely as the AttemptLog/ExactLog proof
source, prepares the sealed proof with the deployment mutation reader, closes that
runtime handle, and then calls ActivateCatalog. It closes/reopens deployment after
every step for exact manifest/mutation verification, closes it, and only then
obtains the serving Store through OpenRuntime. An exact lost-answer replay obtains
the original proof from CatalogMutations without current counter reads. Repeated
OpenDeployment and OpenRuntime calls return new handles over that same backing;
their live values overlap only during this explicit bounded deployment-proof
phase and negative topology checks, never in the serving dependency graph. Run
calls NewHarness again for an isolated scenario. A claimed capability with a nil
required hook is Failed, an honestly unsupported capability is NotCertified, and
nil harnesses/handles, an unreported section, zero certified sections, or a hook that avoids
constructing usable state fail the run. Report is always populated before Run
returns and its accessors copy. Declarations invokes its non-nil fixture twice,
requires two independently rebuilt CatalogSpec/declaration graphs, compiles both,
then compiles bounded canonical permutations and compares complete Manifest views
and bytes. A nil, panicking, stateful, aliased, invalid, empty, or over-limit
fixture fails. It therefore has the CatalogSpec needed to test manifest meaning
and a real reconstruction boundary instead of pretending opaque declaration
pointers expose private state. Subjects detects collisions over the application's
samples without pretending injectivity is universally decidable.
ResourcePolicy, EventType, and OperationType Description methods return copied
root-owned declaration descriptions. ComputeSubjectFixtureFingerprint and
ComputeEventFixtureFingerprint execute the root-owned canonical logical projection
for one named test sample and return only its domain-separated fingerprint.
CheckSubjectGoldens, CheckEventGoldens, and CheckAttemptGoldens use those proxies,
require one uniquely named application case for every committed SemanticGolden
and no extra case, and compare the computed value with Description().Semantics.
Event cases collectively exercise every declared outcome and reason. Attempt
cases cover Start when present, every declared checkpoint code exactly once, and
every configured finish transition/reason combination; NoAttemptCheckpoints
requires an empty checkpoint case set. A cross-phase duplicate fixture name,
missing/extra code or reason, phase-shape mismatch, zero completion, aliased
mutable input, or nondeterministic extractor fails. A mismatch prints only the
reviewed replacement fingerprint, never the sample payload.
CodecRoundTrip supports non-comparable values through the explicit equality
function and checks golden determinism, ownership, repeated decode, malformed
versions, and concurrent calls. Additional conformance sections exercise every
advertised facet/capability, external-store constructors, transaction execution,
catalog CAS, history, lifecycle, bounds, ownership, failures, and anti-vacuity
defect stores. Generic helpers are free functions because Go has no generic
methods.

### 2.12 PostgreSQL profile

`audit/auditpg` freezes this public adoption surface:

```go
package auditpg

const (
	DefaultSchema = "frostgrove_audit"
	SchemaVersion = 1
)

var (
	ErrSpec           error
	ErrSchemaMismatch error
	ErrNotReady       error
)

type Schema struct {
	Name string
}
func (Schema) Resolved() (Schema, error)
func (Schema) Fingerprint() (string, error)

type SchemaManagement uint8
const (
	UnsetSchemaManagement SchemaManagement = iota
	VerifySchema
	ManageSchema
)
func (SchemaManagement) Valid() bool
func (SchemaManagement) String() string

type Spec struct {
	DB     *sql.DB
	Source crud.Source
	Schema Schema
	Limits audit.LimitSpec
}

type DeploymentSpec struct {
	Runtime          Spec
	SchemaManagement SchemaManagement
}

type Deployment struct{ value deployment }
func NewDeployment(DeploymentSpec) (*Deployment, error)
func (d *Deployment) Capabilities() audit.Capabilities
func (d *Deployment) Limits() audit.Limits
func (d *Deployment) Backing() audit.Backing
func (d *Deployment) BackingID() audit.BackingID
func (d *Deployment) LogID() audit.LogID
func (d *Deployment) Catalogs() audit.StoreCatalogState
func (d *Deployment) InstallCatalog(context.Context, audit.Manifest, audit.CatalogChangeRef) error
func (d *Deployment) ActivateCatalog(context.Context, audit.CatalogRef, audit.CatalogRef, audit.CatalogChangeRef, audit.CatalogActivationProof) error
func (d *Deployment) VerifyCatalogs(context.Context, []audit.Manifest) error
func (d *Deployment) CatalogMutations(context.Context) (audit.CatalogMutationLog, error)
func (d *Deployment) Prepare(context.Context) error
func (d *Deployment) Migrate(context.Context) error
func (d *Deployment) Verify(context.Context) error
func (d *Deployment) Schema() Schema
func (d *Deployment) SchemaManagement() SchemaManagement
func (d *Deployment) Close() error

type Store struct{ value store }
func New(Spec) (*Store, error)
func MigrationStatements(Schema) ([]string, error)
func (s *Store) Check(context.Context) error
func (s *Store) AttemptState(context.Context, audit.AttemptStateQuery) (audit.AttemptStateResult, error)
func (s *Store) AttemptTypeState(context.Context, audit.AttemptTypeStateQuery) (audit.AttemptTypeStateResult, error)
func (s *Store) CatalogMutations(context.Context) (audit.CatalogMutationLog, error)
func (s *Store) Schema() Schema
func (s *Store) Close() error

var _ audit.DeploymentStore = (*Deployment)(nil)
var _ audit.Store = (*Store)(nil)
```

New and NewDeployment validate/copy configuration but perform no I/O, environment
read, migration, verification, goroutine start, or pool ownership transfer. Store
is the runtime audit.Store handle and its dynamic method set contains no
CatalogAdmin, migration, preparation, or schema-management method. Deployment is
the distinct audit.CatalogAdmin/Closer handle and never implements audit.Store.
Unset schema management resolves to VerifySchema. Deployment.Migrate works only in
ManageSchema; Prepare conditionally migrates and then verifies; Verify never
mutates. Runtime Check verifies readiness without gaining administration authority.
Before a successful Deployment verification or runtime Check, their operational
methods return ErrNotReady and identity/catalog StoreInfo accessors return invalid
zero state so audit.New fails closed. Close never closes the caller's sql.DB. DB
and Source must identify one datasource.
Only schema properties enforced by DDL live in Schema; operational lower ceilings
live only in Spec.Limits. If a future limit becomes a CHECK operand, it moves to
Schema instead of gaining two authorities. MigrationStatements is deterministic,
returns copied statements, and performs no I/O.

`audit/auditpg` is a nested module. Neither constructor performs I/O. Explicit
VerifySchema and ManageSchema follow D-127 through Deployment only; migration is
never a constructor default. A deployment executable prepares storage, installs
and activates every direct child with CatalogActivationProof, using a temporary
checked Store only as the post-genesis AttemptLog/ExactLog proof source. It closes
that value before mutation, closes and reopens Deployment after each step, verifies
exact manifest and CatalogMutationLog gate readback, closes deployment, and only
then opens and Checks the serving Store.
Tests assert the two concrete dynamic method sets remain disjoint. The profile uses database/sql with pgx stdlib in live tests, one exact
`crud.Source`, provenance-preserving root `*crudsql.Tx` resolution, and checked-out
`*sql.Conn` for a standalone append. Framework savepoints, nested/unproven raw
transaction wrappers, and native pgx transactions are refused in release 1;
out-of-band raw SQL savepoints remain outside the caller contract.

Schema version 1 stores one cryptorandom persisted BackingID and LogID in schema
settings and normalizes revisions, declared context facts/actor hops, items,
values, changes, idempotency/append-intent commitments, keyed access/denial
evidence, hold memberships/transitions and signed expected/result
count/epoch/set/head projection, attempt operation/start-key aliases,
per-attempt transition refs and state, per-replay-policy Unsettled/head/anchor
certificates, reverse current-counter-head index, retention cohort tables/guards,
corrections, inventory snapshots,
append-only catalog manifests/active head plus deployment change references,
an immutable ordered catalog-mutation log, durable LogID, and schema settings. It
stores logical, envelope, integrity and leaf digests, exact protected envelopes,
seals, catalog-set/deployment/retention-rule fingerprints, consequence,
classifications, codecs, and key IDs. Whole-record JSON is not mutable authority.

One Append attempt uses one explicit database transaction and executes each SQL
statement once through its bound `*sql.Tx` or checked-out connection path, never
`*sql.DB.ExecContext` retry behavior. Unique idempotency-domain rows serialize
same-key races: equal stable SemanticDigest under the complete idempotency domain returns the
original header/outcome even when a hold candidate has another CAS-derived
AppendIntentDigest. Same-RevisionID replay requires exact intent equality, and
logical disagreement conflicts. Driver outcomes are classified at the adapter boundary. A lost answer
after possible server issue is Unconfirmed, never cancellation or NotWritten.

Search returns complete revisions under keyset order `(recorded_at, revision_id)`.
Ordinals order only children. Writer roles cannot UPDATE/DELETE/TRUNCATE immutable
evidence; dedicated append functions and triggers enforce invariants. Schema
verification checks version, migration fingerprint, columns, types, nullability,
indexes, constraints, functions, triggers, grants, catalog compatibility, and
query plans. No sequence is advertised as exact commit order.

### 2.13 Mutation-resistant test obligations

Every refusal has a passing legal neighbor. Every invariant negative and every
positive state transition has a defect fake, altered fixture, or subprocess
control that demonstrably makes the test fail when its guard or transition is
removed. `audittest` reports Passed, Failed, and Not
Certified per capability; unsupported cannot masquerade as covered.

The minimum matrix is:

**AT-001 —** Catalog membership, identity/generation changes, duplicate meanings,
   declaration sealing, stable ordering, no registry/import side effect, and
   concurrent reads. Lineage fixtures cover genesis, gaps, forks, wrong parents,
   generation reuse, same-version policy/codec golden drift, incompatible codec reuse, privacy
   weakening, identical manifests rebuilt from different declaration pointers,
   missing or tampered historical manifests, concurrent activation CAS, and stale
   writers after rotation. Catalog-admin cases require persisted external
	change-ledger references, direct-child install/activation order, complete exact
	CatalogMutationLog readback after reopen, atomic refusal at the prospective
	complete-log count/byte boundary, exact replay after a lost response and restart
	between every install/activation, stale concurrent activation, and distinct deployment/runtime
   concrete dynamic method sets.
   Distinct function values with identical declared versions and golden transcripts
   are the stable positive neighbor; a state-switching custom engine pins the honest
   finite-fixture/explicit-TCB boundary rather than a false future-behavior claim.
**AT-002 —** Every semantic name/reference bound, every codec version/round trip/upcast,
   every outcome/reason code grammar, membership, and domain, plus a syntactically
   valid sensitive-looking label that demonstrates the reviewed author boundary
   rather than an impossible kernel semantic filter; exact metadata/type binding,
   event-extractor single evaluation and undeclared-result refusal, mutable-byte
   ownership, panic behavior, secret canaries, absence of a model-getter capture
   door, and privacy-admission fingerprint changes. The complete context-fact and
   provenance matrix includes required and optional absence, every incomparable
   provenance pair, per-actor-hop provenance, undeclared canaries at every output,
   every storage mode, and grouped context-policy mismatch. Race doubles overlap
   every injected collaborator named by AI-030. After the sole resolver call,
   context-value canaries cover identity, semantic, tokenizer, protector, signer,
   Writer, Log, ExactLog, LifecycleLog, authorities, limiter, cursor/fence,
   verifier/revealer, and private evidence paths. Each canary includes a distinct
   secret-bearing cancel cause and requires normalized context.Cause. Direct,
   wrapped, and joined resolver returns of that cause are discarded at the
   immediate post-return cancellation check and CauseOf is nil; an uncanceled
   resolver error is the legal diagnostic neighbor. Hold matter
   cases cover codec/classification, protected-mode admission, manifest/AAD/
   summary binding, and raw-value nonleakage. Named subject/event semantic
   goldens cover every committed fixture and declared code without function
   addresses, paths, reflection, closure state, or binary identity. Built-in and
   custom codec descriptions and accepted/rejected golden bytes cover text, both integer signs, canonical decimal refusal, byte
   ownership, exact Reference text, lowercase UUID, duration extremes, and UTC seconds/nanos with
   monotonic/location data removed; external generic inference compiles. A
   required tokenized ScopeFact is the passing operational neighbor; Compile and
   Lineage reject active or retained protected-only/redacted scope while an absent
   optional scope remains absent.
**AT-003 —** Independently mutate every keyed semantic-digest input and prove a low-entropy
   value is not exposed as its raw SHA-256. Independently mutate every protected
   envelope byte and every AAD coordinate. Leaf vectors mutate every preimage
   member and prove Leaf itself is excluded; envelope vectors mutate every header
   preimage/actor/context/finalized-item member and prove Envelope, Integrity,
   Seal, AppendIntent, RecordedAt, and backend position are excluded. Integrity
   vectors bind only the explicit revision/domain/ObservedAt/Semantic/Envelope
   input, the signature binds only Integrity, and AppendIntent binds the finalized
   signed wire without including itself. Reordering the mandatory derivation or
   reintroducing any self-field fails. HMAC
   vectors cover synthesized algorithm/profile, short/duplicate/inactive keys,
   exact 32-byte outputs, key-copy ownership, every-bit flips, constant-time
   verification behavior, and cross-domain/AAD/protocol substitution. Access-request,
	   access-grant, access-result, access/control continuation, control request/grant/
	   result, selection, exact inventory-fence envelope/decoded claim, hold-request,
	   and denial-request digests cover
   low-entropy dictionaries, every exhaustive typed field including role/query
	   class, incoming/emitted continuation commitments and origins, distinct domains,
	   acyclic fence construction, and profile rotation. Each
	   RetentionBasisDigest vector fixes the exact domain and varies CatalogRef,
	   normalized ObservedAt, and every RetentionRuleView arm; kernel, memory,
	   PostgreSQL, inventory, and fence paths must call the one helper. Every
	   CorrectionProposalDigestInput member is independently varied, value order is
	   permuted, caller buffers mutate after return, link arms/cycles refuse, the
	   proposal digest is absent from its own preimage, and the SemanticDigester is
	   invoked exactly once under its distinct domain.
   admitted requester context fact and provenance is independently changed while
   identity aliases are held constant and must change RequesterCommitment plus the
   access/control/hold/denial request digest. A raw-
   SHA and post-construction state-switching SemanticDigester fixture demonstrate
	   the explicit custom keyed-PRF trust boundary without claiming finite proof.
	   Custom identity/token/protection/reveal/sign/verify/cursor/fence state-switch,
	   retention, collision, nonce, and unbounded-work fakes pin the same honest TCB
	   limitation. AES-256-GCM
   vectors cover minimum input-key size, HKDF profile separation, one active key, nonce entropy failure, retained-
   key rotation, tag/nonce/ciphertext/description/AAD mutation, copied plaintext,
   and cross-protocol substitution for protection and both continuation keyrings.
**AT-004 —** Idempotent same-key/same-logical-semantic replay returns the complete original
   receipt even when a randomized Protector would produce a different envelope;
   exact same-revision/full-content replay does too. Each operation, catalog set,
   semantic profile, key, or content disagreement and each same-revision/
   different-integrity disagreement conflicts. Protection swaps across fields,
   items, revisions, catalogs, and deployments fail. Retry reuses the frozen
   envelope with zero resolver, digester, protector, signer, or callback calls.
   A committed duplicate under a different expected live authority conflicts and
   rolls back a deliberately non-idempotent second business mutation; same-
   authority replay and authority-less committed replay are passing neighbors.
   Conditional hold appends accept exactly one signed authenticated-prior/CAS-result
   candidate, reject every prior/result/outcome substitution, and replay the
   original candidate even after membership state changes. That case proves
   authorization precedes a committed-only keyed lookup, Found performs no
   HoldState call, AbsentNow followed by a winning concurrent append is caught by
   Append's atomic recheck, equal SemanticDigest permits a different fresh intent,
   and same-RevisionID replay still rejects any AppendIntentDigest difference.
   First-touch EntityFullState replay returns the original truthful
   EntityChanged anchor without a second baseline/head advance; a changed
   catalog, field inventory, or full-state byte conflicts.
**AT-005 —** Standalone committed versus caller-transaction receipt, outer rollback,
   commit then visibility, direct Record and standalone Capture NotWritten and
   Unconfirmed tokens, keyed and unkeyed lost-response replay, every
   transaction-bound Retry refusal with zero Writer calls, commit-error
   ReconcileKey recovery, repeated uncertainty, process-rebuilt exact semantics,
   identical token recovery from returned errors, and no collaborator replay by
   Retry. RetryToken has no serialization interface and its size is bounded in
   memory. Lookup strips all context values, sees no staged row, requires explicit
   Committed visibility with zero authority, and rejects a malicious alternative.
**AT-006 —** Exact nested join, mismatch-before-callback, frame-scoped error/panic cleanup,
   undeclared-operation and nonmember-item refusal, identical repanic, concurrent
   canonical staging, duplicate items, hard bounds, late Stage refusal, and
   exactly one outer Append. Known subjects reserve before business I/O; generated
   collisions and every swallowed post-mutation failure poison the root group,
   roll back, and make zero Append calls; no-ops release reservations.
**AT-007 —** Writer-only, Log-only, ExactLog-only, LifecycleLog-only, and CatalogAdmin-only compile fakes;
   both Unstated and Unsupported required-capability refusal; NewHistory
   StableSearch/ExactInspection and NewControl ExactInspection/StableSearch plus
   action-driven Holds/PurgePlanning matrices; no seam discovery or Close call; memory
   cross-system refusal before CRUD I/O; exact database/sql source/transaction
   match; unsafe DB-B fallback against DB-A refusal; source-bound control; and
   authority-less, mismatched, and correct grouped CRUD preflights. Transaction
   tests distinguish framework roots from direct framework savepoints, exercise
   the savepoint-to-raw-transaction-to-wrapper laundering sequence, and document
   an invisible raw SQL savepoint as outside the guarantee. External `_test`
	   packages implement every public store/query/row/catalog SPI using only exported
	   validating constructors and copied accessors, including persisted commitment
	   sets that cannot masquerade as live keyring results. A public external Writer returns
   a private concrete Execution without unsafe/reflection or an executor accessor;
   typed-nil/changing/foreign authority, result-authority substitution, cross-
   recorder use, and simultaneous transaction swapping fail. Abort, panic, codec/
   signer failure, and swallowed errors leave no Writer registry or later
   executable capability. Restarted keyed replay rebuilds the original persisted
   header rather than the fresh randomized candidate. External compilation also
   proves that manual Draft and EntityDraft are not assignable and that no Writer,
   Recorder, or Execution method accepts EntityDraft outside the internal bridge.
   Entity head/alias/reservation cases vary scope absence, both ScopedReference
   components, rotation, grouping, genesis race, and a foreign-scope predecessor.
**AT-008 —** All catalog, store, write, read, settlement, protection, and fence outcomes;
   unknown enums; cancellation precedence; cyclic, joined, panicking, and lying
   foreign error chains; hidden causes; safe rendering; every collaborator's
   raw-error nonleakage; identical repanic; and an unrecovered store panic.
**AT-009 —** Cursor binding with each component varied independently, origin, expiry, byte
   flips, backend-position substitution, oversized positions, catalog-set
   rotation, complete-page tiling, and hostile foreign, duplicate, misordered,
   oversized, or digest-invalid rows. Token and decoded bounds fail before large
	   allocation or store I/O. Continuations re-resolve the same requester,
	   reauthorize, intersect every original and prior-effective ceiling, honor
	   revocation, and fail both direct widening and narrow-then-rewiden attempts
	   for every dimension. Exact emitted cursor
	   commitments vary protocol, key generation, nonce/ciphertext, decoded position,
	   issue/expiry, and origin request/grant independently. Subject and event-target
   shells bind exact declaration identity; zero/negative/over-hard
   CursorLifetime, time overflow, already-expired grants, expiry beyond the
   configured window, and continuation expiry extension all refuse. A grant
   expiring before the configured lifetime produces the exact shorter cursor and
   is the passing neighbor.
   Query-algebra cases cover each exact and one-over resource/action/
   classification/coordinate ceiling, the safe zero defaults, and every explicit field/
   context projection kind, time axis and endpoint shape, both directions,
	   changed-field match kinds, negative-only and positive-plus-excluded sets,
   overlap/duplicate/one-over refusal, honest create/baseline/delete/restore
   Changes semantics, event outcome/reason filters, event-type and typed-
	   index lookup, resource-wide and operation-type-wide discovery beside exact
	   subject/operation lookup, every actor position, every declared context selector, and
   invalid cross-class combinations. Each normalized member is varied across
   request, grant, StoreQuery, cursor, and result evidence; grants may narrow
   allowlists/time but cannot rewrite direction or predicates. Origin pagination
   freezes one bounded cohort. Normal and backdated concurrent appends, changed
   sort keys, snapshot deletion/recreation/mutation/expiry, cohort overflow, and
   page/revision/byte progress regression, jump, reset, or overflow cannot alter
   it; the quiescent, concurrent, and reverse-direction legal neighbors tile the
   exact same cohort once.
   History derives every retained target
   token, rejects non-searchable modes without store I/O, and kills resource,
   action, raw/token, and retained-profile substitutions. NewHistory repeats the
   operational-scope searchability check before authority or store I/O and never
   turns an absent optional scope into a wildcard.
**AT-010 —** Access allow/deny, projection before return, non-recursion, cross-scope grant
    expiry/cohort limits, current authorization over old generations, historical
    codec/upcast paths: removed or renamed live fields use explicit
    HistoricalReconstruct identity, an old wire type uses its declared upcast, an
    unknown or missing historical codec remains unknown, and a later authenticated
    full Known anchor supersedes an older undecodable delta. A field added after a
    baseline stays Unobserved through an unrelated delta, changes on its first
    assignment, remains addressable after removal through HistoricalReconstruct,
    and compares Unobserved-to-Known as Indeterminate. Record-era outcome/reason membership and policy/codec
    fingerprint validation, missing/tampered catalogs, equal-time/random-ID ordering,
    clock rollback, causally closed `10 -> 30 -> 20` boundaries, regressing-
	    descendant ambiguity, store-asserted head authentication, missing-head/history
	    ambiguity, suffix omission relative to that head, the explicit old-valid-head
	    and full-erasure external-anchor limitation,
    resolved-boundary repeatability, monotone hostile RecordedAt/position rewriting
    with unchanged signed ObservedAt, reveal failures, signed
    evidence, backing immutability, every inclusive/tied/missing/foreign boundary
   and unknown reason, all budgets, and cross-policy handles. No unknown value or
   whole model is exposed as known. Reconstruction evidence independently mutates
	   requested boundary kind/revision/time and each work budget before authority,
	   result boundary presence, selected revision, ObservedAt, ObservedThrough,
	   asserted head/ref/leaf,
   and every resource/field/knowledge triple in its strict result arm. Returned payloads carry independently
    Committed evidence; ambient audit authority, rollback, NotWritten,
   Unconfirmed, and malicious InCallerTransaction settlement return no payload.
   Cross-request/cross-principal decision replay, retained-token omission or
   duplication, protected-only broad-scan fallback, and every denial-limiter
   false/nil/returned-error/admitted outcome and keyed request/result/denial digest
   have dedicated kill controls. Each
   returned denial state is exact and cause-elided; a limiter panic performs zero
   append/grant work and is re-panicked identically with no returned state.
   Exact revision and item cases authenticate the complete containing revision,
   enforce all-of authorization for a revision and named-only disclosure for an
   item, and reject wrong LogID/catalog/ordinal, omitted items/summary members,
   envelope/integrity substitution, or mutation between preparation and evidence.
   Access-result vectors exercise all five strict one-of arms, zero every unused
   arm, and independently vary normalized unsigned RecordedAt and every ordered
   contributor. Comparison cases cover equal/different Known values, both-Absent,
   Known-versus-Absent, and every Indeterminate endpoint state; reverse, fork,
   foreign subject, temporal ambiguity, and second-walk widening return neither
   projection nor evidence. Combined-budget exhaustion cannot reset for the
   second walk and returns only BudgetExceeded endpoint knowledge plus
   Indeterminate differences after its bounded result evidence commits.
   Reconstruction from a legacy first-touch changed anchor knows every admitted
   reconstructable field at and after that boundary, exposes no before-baseline
   state, and never labels the anchor as created. Time vectors cover baseline minus
   one nanosecond, the exact baseline, and equal-time descendants in predecessor
   order; the earlier boundary is TemporalAmbiguity with no projection/evidence.
**AT-011 —** Correction/dispute links, exact mixed-local-Backing or durable-BackingID refusal, target post-validation,
    foreign-scope and target-substitution denial, and current authorization over
    old-generation targets. Hold tests cover independent H1/H2 membership,
    releasing H1 while H2 remains active, no membership resurrection, idempotent
    replay without epoch movement, ABA invalidation, missing matter keys, and
    catalog/key rotation. Every place/release table row and concurrent serial order
    verifies the unique typed signed arm, HoldID, stable HoldIdentity, command,
    target, protected matter
    presence, exact HoldRequestDigest, and expected/result membership, count, epoch,
    set, and transition head. Fixed-domain identity/set golden vectors, input-copy/
    sort invariance, zero/duplicate refusal, the target-specific empty-set golden,
    and retained-alias rotation prove the same logical holds retain one set digest;
    false AlreadyActive/AlreadyReleased, stale CAS,
    missing/generic/cross-HoldID/cross-command arms, authorization-request
    substitution, omitted/reordered/cross-target transition proofs, and unsigned
    projection changes fail;
    Correction tests vary the keyed proposal domain, declaration, logical target,
    current and record-era redacted modes, field/mode/class set, original ItemRef,
    reason, and post-decision draft; an exact protected target is the passing
    reveal neighbor. Whole-
    revision tests recompute every resource, nested classification, scope, subject,
    and searchable target. Exact and explicit same-declaration wildcard selectors
    are passing twins; implicit-empty, exact-to-wildcard, cross-declaration,
    first-label, any-item, omitted-coordinate, and partial-grant stores fail before
    output or mutation. Defect stores that trust an unauthenticated pre-read,
    accept stale expected state, substitute the result candidate, double-advance, or
    append on conflict are killed. Inventory proves limit-plus-one truncation and a real
    Inventory-to-PlanPurge path. It varies the record-action ceiling before the
    store limit, rejects an omitted/foreign/reordered action in inventory and purge
    views, admits empty coordinates only for a truly coordinate-free summary, and
    rejects the same empty set for any coordinate-bearing summary. Inventory and
    selection-verification continuations reject cumulative progress reset, jump,
    double-count, overflow, per-page budget reset, and changed cohort ceilings.
    Access and control continuations include a
    narrow-then-rewiden authority sequence for resources, coordinates,
    classifications, time, limits, and expiry; no removed member returns. Fence
    restart cases vary every frozen requester/origin/selection/original-grant/
    prior-effective-grant member and reject a narrow-then-rewiden purge.
    Inventory reads Control.Clock once, binds exact
    normalized AsOf through request/grant/query/result/cursor/fence, rejects a
    hostile changed or future store echo, and preserves origin AsOf on continuation
    and purge. Strict control evidence separately varies request/grant/result kind,
	    control reason/requested limit/hold request, mutation ref/disposition/full hold
	    projection, inventory refs/AsOf/truncation/exact cursor/fence, verification refs/
	    status/completeness/exact cursor, and purge refs/AsOf/fence. Fence tests vary bits, key, LogID, BackingID,
    catalog set, query, cohort, retention basis, hold epoch/set, as-of, expiry,
    ordering, duplication, bounds, rotation, retirement, and restart. Every report
    also covers zero/negative/over-hard Control CursorLifetime, overflow, an
    already-expired grant, a shorter grant-derived expiry, and refusal to extend
    the original expiry on continuation. Every report
    or plan is withheld unless its bounded evidence receipt is Committed.
    A required tokenized scope is the passing neighbor; Compile, Lineage,
    NewHistory, and NewControl reject active or retained protected-only/redacted
    operational ScopeFacts before authority or store I/O, while an absent optional
    scope remains absent.
**AT-012 —** DefaultService POST/PUT physical outcomes through the sole sealed
    `auditcrud.Secured` terminal; Save races; option-once
    Update; exact scoped delete/restore victim/result sets; generated after-state;
    empty DefaultService RestoreMany authorization; no-op/missing/duplicate/oversized
    IDs; denial; opaque wrappers; audit failure,
    rollback uncertainty, commit uncertainty, and panic; exact metadata field/
    codec binding; same-model/different-table and descriptor drift; secret alias,
    primary-key, tokenized-subject, and extractor canaries; tombstone-selected soft
    versus hard delete; complete core/optional inventory; predecessor mismatch;
    replay without double head advance; canonical multi-subject head locking; and
    refusal of two entity items for one subject in a revision. The sealed-boundary
    suite covers absence of `Next` and unsecured sibling entry, outer-faults bind
    refusal, private bridge zero/copy/replay/cross-recorder cases, fail-closed
    write-only/bulk verbs before callbacks or I/O, and substitution of Save
    candidate, Update payload/options, IDs, scopes, relations, and snapshots.
    Hard-delete cases prove the accepted revision atomically marks its scoped
    subject head terminal, recreation through the sealed terminal and every
    retained alias rolls back, delete/recreate races serialize, soft delete and
    restore remain nonterminal, and exact terminal replay advances nothing.
    Legacy-row cases cover first changed assigned Save/Update, first delete and
    restore, a no-op that creates no baseline, complete reconstructable full-state
    versus actual Changes, missing/duplicate/secret field failures, one extractor
    path, concurrent first touches, key rotation, rollback, and exact replay.
**AT-013 —** PostgreSQL fail-not-skip TestMain and unset-DSN subprocess control; begin,
    statement, and commit kill points; no pool retry; concurrent equal and
    conflicting keys; transaction mismatch; active-catalog CAS and stale append;
    append-only manifests, exact persisted mutation-log readback, distinct
    deployment/runtime handles, and holds; least-privilege roles; immutable triggers;
    schema drift; old codec/catalog fixtures; query plans; immutable materialized
    search snapshots under concurrent/backdated commits, cumulative continuation
    ceilings, terminal entity heads, legacy first-touch full anchors and
    recreation races; pagination; rollback;
    cancellation; fence restart/rotation; and full conformance twice under race.
**AT-014 —** Integration fixtures for auth/security provenance, tenancy scope, event commit,
    job invocation, logical storage references, lossy OTel observation, module
    profile activation, disabled components, every declared bypass verdict, and
    release-1 denied-attempt events admitted only after an application limiter;
    nil, false, failure, panic, admitted, concurrent, and requester/request-digest
    substitution controls prove the limiter cannot grant access or amplify itself.
    Observer vectors assert exact per-phase zero/applicability and deltas for store
    calls, snapshot reads, accepted pages/revisions/wire bytes, terminal verified
    revisions, unknown/gap fields, and budget stop; checked one-over saturation
    never wraps or changes the authoritative result, and a lying external adapter
    cannot construct or feed work counters back into the kernel.
**AT-015 —** Required versus BestEffort admission, authoritative marker, error visibility,
    forbidden entity/control use, grouping mismatch, and protected bounded reason
    narrative with plaintext canaries at Writer, Observer, semantic input,
    protection, evidence, and error doors.
**AT-016 —** External consumer packages compile the advertised root composition,
    every built-in codec/HMAC helper, a custom CodecEngine through DefineCodec,
    named subject/event golden checks, auditmemory Log/Deployment/Store/Tx,
    audittest Factory/Report/proxies, and separate auditpg DeploymentSpec/
    Deployment and Spec/Store lifecycle. Memory siblings preserve
    state and distinct logs do not; constructors perform zero I/O/lifecycle; Close
    preserves caller ownership. The identical non-vacuous conformance Factory runs
    against memory and PostgreSQL, and missing hooks, nil stores, unreported or zero
    certified sections, false capability claims, and unusable stores fail. A separate
    deployment consumer prepares schema, obtains CatalogChangeRefs, installs and
    activates every direct child, closes/reopens, verifies manifests and exact
    CatalogMutationLog records, then opens a distinct runtime value. Compile-time
    graph and dynamic assertions prove neither runtime concrete type exposes
    CatalogAdmin and neither deployment type implements audit.Store.
    The same external fixture compiles None/All/Only query projections, all time/
    direction/change/code selectors, event-type/index, actor/context, exact
    revision/item history, shared-budget State/Compare, and tri-state Compared
    access without reflection, raw store predicates, or private constructors. A
    multi-action entity policy compiles subject history with empty-as-all and an
    explicit proper action subset, while a foreign action and duplicate subset
    refuse before store I/O. The same fixture compiles
    `AnyChanged(status).Excluding(owner)` and a negative-only
    `UnfilteredChanges().Excluding(owner)` without exposing value predicates. It
    also compiles declaration-bound resource and operation-type Revisions doors;
    missing scope/limit and foreign declaration members refuse before store I/O.
**AT-017 —** A stdlib-only `scripts/audit-trace.sh S<N>` checkpoint parses the tracked
    normalized registry, exact semantic manifest, independent completeness anchor, and every AH, AE,
    AI, AT, AM, PN, RT, PKG, and S declaration. It rejects
    ranges/lists in fact rows, missing/duplicate/orphan or ill-typed endpoints,
    missing/duplicate AT activation, fabricated AT×section cross-product,
    node-kind/prefix mismatches, one-way or missing edges, missing or digest-changed
    AU/AH/AE/AI/AT/AM/PN semantic bodies, missing AT/AM/PN reservations,
    closed-subgraph/highest-ID deletion,
    noncanonical bytes, invalid activation sections, package/profile/cwd/pattern/
    import mismatches, and every unmapped AU or requirement.
    Semantic correctness of a structurally valid edge reassignment remains a review
    obligation, not a false duplicate golden map. The runner first executes from
    repository root the exact argv `["go","test","-json","-count=1","-run","^TestAuditTraceRegistry$","./scripts"]`
    and requires exactly one non-skipped pass
    event for the sole unreserved bootstrap TestAuditTraceRegistry from
    `github.com/frostgrove/vv/scripts`. Default-build inventory excludes the tagged design
    importer. It then groups every reservation active through the
    requested section by its fixed package execution tuple, invokes anchored exact
    test regexes without eval, and requires exactly one matching pass event for
    every declared `(Package, Test, Role)` tuple, including independent coverage,
    mutant-kill, and positive-neighbor executions. Skip, fail, zero/duplicate passes, wrong
    package, dropped integration tag/DSN, command failure, deletion, rename, or an
    unregistered in-scope `TestAT...`/`TestAuditTrace...` other than that exact
    bootstrap is failure rather than Go's successful no-test exit.
**AT-018 —** Attempt lifecycle conformance exercises the complete typed declaration,
    start, state, transaction, wire, bound, privacy, retention, catalog, and resume
    contract. It proves Required Run enters protected work only after a Committed
    Inserted Started transition; replay invokes no callback and exposes no handle.
    Checkpoint, direct terminal, OutcomeUnknown resolution, grouped transactional
    terminal, reconciliation, and abandonment each advance one authenticated
    expected-state CAS or return the exact inert replay/conflict/uncertainty result.
    Tests restart from persisted state, recompute every transition through the
    asserted head, keep per-chain and global-counter facets distinct, bind target
    presence, stable owner/scope and policy/replay fingerprints, and authorize
    Resume, ResolveUnknown, and Abandon with their exact nonzero Purpose and Role.
    Independent vectors mutate every transition/state/digest field, reserve terminal
    capacity, reject hostile projections and foreign handles, enforce all-or-none
    retention cohorts and current-counter-head pins, gate incompatible catalog
    activation on the authenticated Unsettled certificate, and keep raw payloads,
    errors, headers, credentials, and ambient context outside evidence. Exact
    operation history alone may return status after complete bounded verification;
    type-wide and searchable-target pages remain transition-only. Each of the nine
    attempt mutant families has a passing legal neighbor, a primary AT-018 kill,
    and independent mutant and positive reservations.

Fuzz targets cover name/reference parsing, canonical decoding, foreign error
graphs, cursor and fence parsing/authentication, catalog-lineage validation,
protected-envelope validation, store-row validation, and reconstruction.
Benchmarks record allocation and latency envelopes for declaration capture,
single-item append, 256-item grouping, page validation, and reconstruction; they
are reports rather than unstable wall-clock gates.

## 3. Implementation sections

Delivery order is dependency-driven rather than label-numeric: S0, S1, and S2
produce the usable core; S4 then lands CRUD plus small auth, tenancy, faults, and
service composition smoke fixtures against that vertical slice. S3 adds attempts
and advanced history/lifecycle, S5 adds PostgreSQL, and S6 closes the exhaustive
cross-module, deployment, and documentation matrix before S7. The trace registry
uses this explicit delivery rank: `S0 -> S1 -> S2 -> S4 -> S3 -> S5 -> S6 -> S7`.
Security invariants that prevent false commit/authorization claims remain gates,
but non-blocking hardening and broad edge matrices are accumulated for the final
S7 pass instead of delaying the first working core.

### S0 — decisions, public contract, and roadmap

**Status:** complete

Files:

- `docs/ai/decisions/D-136-audit-evidence-is-explicit-protected-and-transaction-honest.md`
- `docs/ai/usecases/modules/audit/UC-034-record-and-investigate-auditable-evidence.md`
- `docs/ai/decisions/Index.md`, `docs/ai/usecases/Index.md`, and
  `docs/ai/flows/FL-040-an-audit-contract-becomes-an-executable-checkpoint.md`,
  `docs/ai/flows/Index.md`, and `docs/roadmaps/Index.md`
- `docs/roadmaps/2026-09-01-audit-log-roadmap.md`
- `scripts/testdata/audit_trace.tsv`,
  `scripts/testdata/audit_trace_semantics.json`,
  `scripts/testdata/audit_trace_anchor.json`,
  `scripts/audit_trace_test.go`,
  build-tagged `scripts/audit_trace_import_test.go`, and
  executable `scripts/audit-trace.sh`

The identifiers above reflect the current OTel/i18n reservations. Re-read every
index immediately before creating a file; if concurrent work has claimed one,
advance all audit references coherently instead of overwriting it.

Work:

1. Accept root-kernel/nested-PostgreSQL topology and the four-product model:
   explicit events, entity revisions, declared attempts, and audit-control evidence.
2. Freeze error partition, transaction authority, allowlist/privacy boundary,
   catalog lineage, exact CRUD coverage, protected committed reads, and honest
   integrity and transaction claims.
3. Replace stale baselines and broken historical link.
4. Add the competitive matrix with primary-source links and dated caveats.
5. Materialize the frozen normalized trace facts, exact AU/AH/AE/AI/AT/AM/PN semantic bodies,
   and independent completeness anchor, add their section-aware structural/behavioral checkpoint, and document
   that real S0 path plus its invariant tests in FL-040. S0 documentation contains no
   runnable future API, exported-symbol link, module-path assertion, or deployment
   example; those land with their implementations in S4/S6.

Checkpoint:

```bash
git diff --check
go test ./scripts -run 'Docs|Decision|Usecase|Roadmap|Link' -count=1
go test -tags=audit_trace_import ./scripts -run '^TestAuditTraceDesignImport$' -count=1
./scripts/audit-trace.sh S0
```

Review gates: fresh happy/DX reviewer and fresh adversarial architecture/privacy
reviewer; all critical/high gaps closed and re-reviewed.

### S1 — vocabulary, declarations, codecs, and catalog

**Status:** complete

Files owned: `audit/names*.go`, `audit/bounds*.go`, `audit/errors*.go`,
`audit/catalog*.go`, `audit/declaration*.go`, `audit/codec*.go`,
`audit/context*.go`, `audit/privacy*.go`, and their focused tests. Store,
recorder, crypto-service, history, lifecycle, and internal-bridge filenames are
excluded explicitly.

Work:

1. Identifiers, all byte/count/time bounds, actor/context values, item/value/
   change records, `LogID`, `CatalogRef`, and immutable `CatalogSet` lineage.
2. Error vocabulary and safe rendering with hidden causes.
3. Typed resource and event declarations with `Define/TryDefine` and
   `Declare/TryDeclare`, versioned PolicySemantics, named logical goldens, closed
   manifest-public outcome/reason code sets and typed equality event indexes with
   mechanically enforced grammar/membership and an explicit reviewed semantic-privacy obligation, per-action
   control reasons, and hold-matter codec/classification/protected-mode policy.
4. Audit-owned CodecEngine wrapping, accepted/rejected wire fixtures and
   fingerprints, honest custom-engine termination/complexity/state trust boundary,
   built-in canonical codecs, historical version chains, context fact policies,
   post-resolver cancellation-cause elision, and exact allowed provenance sets.
5. Field/privacy modes, deployment admission, deterministic chained catalog
   manifests, default keyed digester plus explicit custom SemanticDigester keyed-
   PRF/state trust boundary, protected-envelope/AAD vocabulary, and
   stable public manifest accessors for external stores.
6. Round-trip, lineage, determinism, ownership, alias, provenance, concurrency,
   fuzz, and plaintext-canary tests.

Checkpoint:

```bash
go test ./audit -count=1
go test -race ./audit -count=1
go vet ./audit
./scripts/audit-trace.sh S1
```

Review gates: new happy/API reviewer; new edge/fuzz/privacy reviewer; mutation
controls for every negative test.

### S2 — store seam, recorder, memory store, and conformance

**Status:** alpha implemented — recorder, grouping, retry/reconciliation,
catalog-mutation readback, memory append, signed public one-page history and
exact revision/item reads pass the root conformance slice; protected disclosure
and cursors remain S3

Files owned:

- `audit/store*.go`, `audit/record*.go`, `audit/integrity*.go`
- `audit/recorder*.go`, `audit/group*.go`, `audit/retry*.go`,
  `audit/crypto*.go`, `audit/observation*.go`
- core `audit/history.go`, `audit/query.go`, `audit/access.go`, and
  `audit/cursor.go` needed for bounded subject/event alpha reads
- `crud/executor.go` and `crud/executor_test.go` for the neutral
  `SourceBoundExecutorFor` result needed by Writer authority resolution
- `audit/auditmemory/**`
- `audit/audittest/**`

Within the last two directories, S2 owns store/recorder/catalog/conformance-core
and core history/access/cursor files; attempt, advanced-selector,
reconstruction/comparison, lifecycle, and fence files are reserved for S3.

Work:

1. Add and exhaustively preserve-test neutral `crud.SourceBoundExecutorFor`, then split
   Writer/Log/ExactLog/AttemptLog/AttemptTypeState/LifecycleLog/CatalogAdmin seams, support/limits/durable
   BackingID/LogID, process-local Backing, Authority/Execution, outcomes, and
   validating public row/query/request constructors, terminal hard-delete entity
   heads,
   and copied accessors sufficient for implementations outside package `audit`;
   catalog mutations require persisted external change-ledger references, bounded
   immutable readback, and distinct concrete deployment/runtime handles; ExactLog
   also exposes the narrowed store-asserted signed entity-head anchor.
2. Recorder, retry token, Lookup visibility, active-catalog CAS, grouping/staging,
   context freeze, mutation reservations/root poison, idempotency, and observer
   isolation.
3. Keyed logical digest, exact envelope digest, scoped entity identity/continuity,
   HMAC token/sign helpers,
   AES-256-GCM protection/reveal, randomized protected-value extension contract,
   and coordinate-bound AAD.
4. Concurrent transactional memory log with its own transaction fixture, stable
   backend positions, terminal heads, append-only catalog activation, hold
   projection, and explicit cross-system refusal.
5. Conformance suite with external-package fixtures, anti-vacuity defect stores,
   malicious settlement stores, and policy proxies.
6. Failure/cancellation/unknown-outcome/error-leak tests.
7. The first usable read slice: current-catalog subject, event type, searchable
   event target, and exact revision/item queries with one bounded immutable page,
   sealed authorization, protected-value projection, committed access evidence,
   and restart-safe cursors. Advanced intersections, reconstruction, attempts,
   and lifecycle may return their declared unsupported capability until S3.

Checkpoint:

```bash
go test ./audit/... -count=1
go test -race ./audit/... -count=1
go vet ./audit/...
go test ./crud -count=1
go test -race ./crud -count=1
go vet ./crud
./scripts/audit-trace.sh S2
```

Review gates: fresh happy store/recorder reviewer; fresh edge/concurrency/error
reviewer; run conformance against both real and deliberately broken stores.

### S3 — attempts, advanced reader, reconstruction, and lifecycle kernel

**Status:** in progress — public exact revision/item inspection is implemented;
attempts and the remaining advanced reader/lifecycle surface follow the usable
recorder/CRUD/integration base

Files owned: `audit/attempt*.go`, advanced `audit/history*.go`,
`audit/selector*.go`, `audit/grant*.go`, `audit/reconstruct*.go`,
`audit/lifecycle*.go`, matching memory and conformance files. S2's working core
history surface remains source-compatible and is extended rather than replaced.

Work:

1. Declared attempt orchestration, per-chain and per-type projections, durable
   idempotent Begin/Run/Resume/Checkpoint/Finish/Resolve/Abandon, activation
   gates, exact history, and retention cohorts under the frozen state machine.
2. Extend sealed authorization grants and the finite typed subject/event/index/actor/
   context/operation/exact query algebra, normalized projections/time/direction/
   change/code predicates, entity before/after indexes, bounded AllOf selectors,
   neighbors, and AES-256-GCM history-cursor/control-token keyrings
   with distinct typed domains.
3. Cursor authenticated-encryption and catalog-set binding, immutable bounded
   search cohorts, cumulative progress, and returned-row verification.
4. Field and context-fact projection plus reveal authorization.
5. Predecessor-chain reconstruction and ordered shared-budget comparison across retained catalogs with record-era
   policy/codec fingerprint and code membership validation, explicit unknown
   reasons, authenticated asserted-head-to-genesis closure, causally closed signed-
   observed-time boundaries immune to store-metadata rewriting, explicit external-
   anchor rollback limitation, and temporal ambiguity.
6. Corrections/disputes, ordinary authorized entity-mutation reverts, and
   non-recursive, independently committed access/denial
   evidence that withholds payloads on every non-Committed settlement.
7. Record-era retention, identified overlapping legal holds, immutable transition
   proof, signed full-state CAS projection, bounded inventory, authenticated
   encrypted restart-safe fences,
   fenced hold-aware purge planning, and integrity verification; no destructive
   executor/export API.
8. Cross-scope grant expiry/cohort limits, catalog/key rotation, ABA, hostile-store,
   exhaustive typed keyed access/grant/result/hold/denial digests, exact trusted-
   clock AsOf echo, and fence substitution tests.

Checkpoint:

```bash
go test ./audit/... -count=1
go test -race ./audit/... -count=1
go vet ./audit/...
./scripts/audit-trace.sh S3
```

Review gates: fresh investigator-DX reviewer; fresh authorization/privacy/
lifecycle reviewer; cross-scope controls and cursor substitution attacks mandatory.

### S4 — transaction-aware CRUD adapter

**Status:** alpha implemented — supported CRUD paths and the first real
composition stack pass; broader S7 edge coverage remains deferred

Files owned:

- `audit/auditcrud/**`
- `audit/internal/auditcrudbridge/**`
- Recorder construction needed to install the private bridge Carrier
- `crud/executor.go` and focused exact source-binding tests
- `crud/decorators/security/security.go` and its mutation-order tests
- `crud/decorators/faults/faults.go` and its exact-forwarding tests
- `crud/adapter/crudsql/crudsql.go` and its focused transaction-scope tests
- `port/service.go` and focused empty-restore authorization tests
- alpha-only `test/auditflow` auth/tenancy/faults/service composition smoke tests
- the narrow audit provenance amendment to
  `docs/ai/decisions/D-082-source-bound-sessions-are-the-safe-default.md`
- the narrow semantic-order amendment to
  `docs/ai/decisions/D-061-a-wrapper-forwards-what-it-wraps.md`
- matching path amendments to `docs/ai/flows/FL-008-a-write-through-the-security-gate.md`,
  `docs/ai/flows/FL-009-transactions-joining-opening-which-database.md`, and
  `docs/ai/flows/FL-015-a-request-through-the-port-layer.md`

Any further neutral CRUD seam change is isolated and requires its own decision
amendment and repository-wide decorator inventory.

Work:

1. Add `crud.SourceBoundExecutorFor` and database/sql
   `TopLevelTransaction`, preserving existing executor behavior and refusing
   fallback, raw-wrapper, and savepoint laundering.
2. Implement the private one-use Recorder bridge without a registry, public
   mutation token, context authority, or new root-CRUD admission vocabulary.
3. Implement the sealed `auditcrud.Secured` graph with factory/bind validation,
   no navigation door, the neutral structural order marker, and the exact
   security -> audit -> faults/store order.
4. Fix Gate option precedence, Gate empty Restore authorization, and the default
   service's pre-repository empty `RestoreMany` shortcut with positive and
   mutation-resistant tests that also protect ordinary non-audit repositories.
5. Land no-ID/assigned-ID Save, Update, Delete, and Restore through the frozen
   exact-state algorithms and Recorder-supervised root `Guard.Run`.
6. Refuse write-only, bulk, raw, and unsupported optional effects before policy
   callbacks or I/O; keep the hidden receiver's Gate-facing capabilities private.
7. Complete the `crud.Core` plus Creator/Replacer/batch/scoped effect inventory.
8. Cover composition, option-once, denial, no-op, failure, rollback, source and
   root-transaction proof, savepoint ordering, bridge replay, and the four happy
   mutation paths. Land small real auth/tenancy/faults/DefaultService smoke
   fixtures now; AT-014's exhaustive integration matrix remains S6. Defer the
   broad combinatorial edge matrix to S7.
9. Land the two owner-decision amendments only with these concrete seams and keep
   their prose free of future paths or symbols.

Checkpoint:

```bash
go test ./audit/... ./crud/... -count=1
go test -race ./audit/... ./crud/... -count=1
go vet ./audit/... ./crud/...
go -C test test ./auditflow -run '^TestAuditAlpha' -count=1
./scripts/audit-trace.sh S4
```

Review gates: fresh happy CRUD/DX reviewer; fresh security/race/exact-effects
reviewer; every supported mutation must have commit, rollback, denial, no-op,
audit-fault, source-mismatch, and concurrent-victim cases.

### S5 — PostgreSQL module and live conformance

**Status:** partial alpha — exact schema readiness, deployment, activation and
bounded mutation readback, append, transaction joining, lookup/reconciliation,
basic search, exact revision/item inspection and restart behavior are
implemented; stable cursors and advanced control parity remain owed

Files owned: `audit/auditpg/**` and auditpg-local conformance/schema fixtures.
Workspace and consumer module files are reserved for S6, so S5 cannot race their
OTel/i18n edits.

Work:

1. Distinct deployment/runtime concrete handles, schema management, migration/
   fingerprint/catalog-lineage verification, append-only catalog installation and
   mutation-log readback with external change references, and expected-parent
   activation CAS.
2. Exact transaction resolution and one-call append/idempotency.
3. Typed search, expiring materialized immutable cohorts/cursors, exact reads,
   terminal entity heads, signed-time reconstruction/comparison, inventory,
   authenticated hold-transition proof/full-state CAS/fence/integrity
   implementation with persisted BackingID/LogID and committed evidence settlement.
4. Least-privilege and append-only enforcement for revisions, catalogs, and hold
   transitions.
5. Live rollback, audit-fault, concurrency, cancellation, uncertainty, duplicate,
   hostile-row, query-plan, and migration tests.
6. Run `audittest` unchanged against PostgreSQL.
7. An integration-tagged TestMain fails rather than skips when
   `FROSTGROVE_AUDITPG_TEST_DSN` is unset; an untagged subprocess test proves that
   fail-not-skip guard still runs.

Checkpoint:

```bash
go -C audit/auditpg test ./... -count=1
: "${FROSTGROVE_AUDITPG_TEST_DSN:?live auditpg suite requires a PostgreSQL DSN}"
go -C audit/auditpg test -race -tags=integration ./... -count=1
go -C audit/auditpg test -race -tags=integration ./... -count=1
go -C audit/auditpg vet ./...
./scripts/audit-trace.sh S5
make tidy
make check-tidy
```

Raw `go -C audit/auditpg mod tidy` is not a valid pre-tag isolation proof because
the nested module consumes the unreleased root module. Repository tidy/check-tidy
owns the workspace graph. Pre-release `GOWORK=off` fixtures use a temporary local
replace; a no-replace consumer graph is asserted only after the matching root tag
exists.

Review gates: fresh happy PostgreSQL/operations reviewer; fresh SQL/security/
uncertainty reviewer; then a third clean reviewer after every fix round.

### S6 — integration fixtures, docs, examples, and structural checks

**Status:** partial alpha — auth/tenancy/security/faults/CRUD/SQL/event/jobs/
storage/observer/i18n composition, generated serving/deployment profiles and a
live PostgreSQL CRUD atomicity fixture pass; broader deployment graphs and
adoption artifacts remain

Files owned: new audit examples/docs/tests, the non-alpha remainder of
`test/auditflow`, audit entries in `go.work`, `test/go.mod`, and `_examples/go.mod`, audit module manifests/generated module
definitions, the generated API baseline,
`docs/ai/usecases/modules/audit/Audit.md`,
`docs/ai/flows/FL-040-an-operation-becomes-a-verifiable-audit-revision.md`, and
their now-resolvable flow/module/index links. Shared indexes/check scripts are
changed only by small conflict-aware patches after re-reading current content.

Work:

1. Composition fixtures: auth wraps Guard.Authenticate and derives the actor chain
   only from its returned context; tenancy maps only Authority.Scope and its
   stable reference/epoch, never an unverified header or unkeyed Scope.Digest;
   the sealed security→private-audit-receiver→faults/store graph proves final-result
   ordering while telemetry remains a non-authoritative outside observer. Event
   integration protects Stream/First/Last/Count from an exact
   event.Commit while each store verifies its own unrelated authority in one
   application UoW. Storage integration is explicit non-atomic evidence for
   Put/Promote/Delete and protects only Namespace/Key, never URL/body/metadata.
2. Jobs use one explicit diagnostic per-delivery attempt identity derived from
   `(InvocationID, AttemptOrdinal)`, so duplicate delivery still invokes the
   handler and never becomes accidental at-most-once execution. A logical
   exactly-once effect barrier is a separate application-declared Attempt with
   reconciliation, not generic jobs instrumentation. Fixtures cover redelivery,
   lease loss, final ordinal, and uncertain start through jobsfx.AutoAdapterFor.
3. ObserverFunc/Observers fan out only non-identifying audit metrics into an
   app-local OTel adapter. Release 1 exports no auditotel package and no trace
   correlation claim; disabled sampling/export failure/panic cannot alter result
   or evidence bytes. ContextResolverFunc provides the app-local auth/tenancy map.
4. Audit safe errors preserve `errors.Is(audit.ErrX)` while exposing an exhaustive
   stable `errs.Kind`/`errs.Code` fault projection through port HTTP/gRPC paths.
   The concurrent i18n MessageSource is wired only in the app fixture; locale and
   rendered text never enter stored evidence and neither audit nor i18n imports
   the other.
5. The generated app definition provides Recorder, History, and Attempts runtime
   constructors, an app-selected health wrapper around auditpg.Store.Check, and
   zero Workers/Seeders in release 1. CatalogAdmin/schema activation exists only
   in a separate deployment command/definition and is absent dynamically and
   statically from the serving graph. Cache is never authoritative and cache
   eviction/failure cannot alter evidence.
6. Root-only, audit-only, PostgreSQL-only, and multi-extension `GOWORK=off` graphs,
   using local replaces for unreleased siblings and explicitly separating the
   post-tag no-replace proof.
7. Audit module pages in English and Russian, usage guide, API baseline, README
   correction, ORM guide corrections, flow documents, and roadmap status.
8. Structural checks for forbidden imports/packages, no implicit lifecycle,
   workspace module equality, graph cost, and base-method inventory.
9. Example tests for explicit events, closed code sets, semantic subject/event
   goldens, external custom-codec fixtures, CRUD, typed indexed/investigation/
   exact protected history, immutable cursors, reconstruction/comparison,
   correction, and lifecycle dry run.
10. Deployment fixture for schema preparation, external change-ledger evidence,
   ordered install and every direct-child expected-parent activation, close/reopen,
   exact manifest and CatalogMutationLog verification, and only then distinct
   runtime construction; both compile-time graph checks and a dynamic type assertion
   prove CatalogAdmin is absent from the serving graph.

Checkpoint:

After the first generator command, review every inferred row and explicitly
confirm or exclude it in `test/auditflow/module.manifest.yml` before running the
check form.

```bash
go run ./cmd/vv generate module -dir ./test/auditflow -name auditflow
go run ./cmd/vv generate module -dir ./test/auditflow -name auditflow -check
make fmt
make tidy
make check-tidy
make unit
make vet
make examples
make integration
make integration
make api
make check
: "${FROSTGROVE_AUDITPG_TEST_DSN:?S6 cumulative audit trace requires a PostgreSQL DSN}"
./scripts/audit-trace.sh S6
git diff --check
```

Review the API-baseline diff as a public-contract artifact; generation success is
not approval of an accidental symbol.

Review gates: fresh happy adopter/documentation reviewer; fresh edge/module-graph/
integration reviewer; repeat both after fixes until no critical/high finding.

### S7 — final whole-tree audit

**Status:** pending

1. Run all checks again from a clean command environment.
2. Review the final diff against every AU, AI, AH, AE, and AT identifier and record coverage.
3. Run fresh happy and adversarial reviewers with no prior thread context.
4. Check concurrent OTel/i18n edits were preserved and no unrelated file was
   reverted or absorbed.
5. Update docs/statuses to implemented, partially implemented, or deferred with
   exact evidence; never label a skipped live profile covered.
6. Record remaining production rollout prerequisites and project-worktree mismatch.
7. Close the deferred mutation controls without reopening base design: grouped
   exact-subject sibling projection, zero PostgreSQL `LogID`, unexpected
   non-unique PostgreSQL indexes, and replay of an older identical catalog
   activation after a later generation is active and the deployment restarts.

Final checkpoint:

```bash
git status --short
git diff --check
make fmt
make tidy
make check-tidy
make unit
make vet
make examples
make api
make check
make vuln
make integration
make integration
go -C audit/auditpg test ./... -count=1
: "${FROSTGROVE_AUDITPG_TEST_DSN:?live auditpg suite requires a PostgreSQL DSN}"
go -C audit/auditpg test -race -tags=integration ./... -count=1
go -C audit/auditpg test -race -tags=integration ./... -count=1
go -C audit/auditpg vet ./...
./scripts/audit-trace.sh S7
```

## 4. Exact traceability registry

The tables below and the source sections named below are the frozen pre-S0 design
presentation. S0 materializes their exact graph facts into tracked
`scripts/testdata/audit_trace.tsv` and the exact AU actor-goal, AH happy, AE edge,
AI invariant, AT obligation, AM defect, and PN positive-neighbor bodies into tracked
`scripts/testdata/audit_trace_semantics.json`; no clean-checkout check reads
`.agents`. A separately tracked `scripts/testdata/audit_trace_anchor.json` is
the deliberate completeness authority. It freezes the schema versions, exact
sorted ID inventory and count for every kind, graph/node/semantic-set digests,
the digest of every graph fact and semantic record, and the exact GoalTrace-edge
count. The TSV remains the sole adjacency authority and the semantics manifest
the sole behavioral-text authority. The anchor duplicates only completeness
fingerprints and inventories so deletion of an internally closed subgraph,
highest ID, or behavior body cannot pass as a smaller valid design. D-136 names
these tracked authorities and their review protocol but copies no digest. A
content change updates TSV and/or semantics, the anchor, and fresh happy/edge
review together; D-136 changes only when the protocol or authority paths change.
The build-tagged S0 import test compares the complete design facts and semantic
bodies and independently recomputes the anchor before these design artifacts
cease to be runtime/CI inputs. Its only legal invocation is
`go test -tags=audit_trace_import ./scripts -run '^TestAuditTraceDesignImport$' -count=1`;
the file's required `//go:build audit_trace_import` line is metadata. It reads
exactly `.agents/artifacts/usecases/AUDIT_USECASES.md` and
`.agents/artifacts/plans/AUDIT_PLAN.md`, performs comparison only, and never writes
or regenerates an authority. After a successful S0 handoff, the `.agents`
trace/usecase presentation is archival only as a trace-graph/semantic source: the
tracked TSV, semantics manifest, and anchor become the sole executable trace
authority. This plan remains the human-reviewed implementation blueprint for
exact section APIs, algorithms, and checkpoints until each section materializes
them in tracked code/tests; it cannot override the tracked product semantics and
is never a clean-checkout CI input. Any later
semantic or graph change explicitly reopens S0: update both presentations,
rerun the tagged compare-only importer, recompute the tracked anchor, and obtain
fresh happy/edge approval before dependent code proceeds. Ordinary clean-checkout
checkpoints deliberately consume only tracked authorities and never `.agents`.

The registry is ASCII with one U+0009 TAB between grammar fields, LF line endings,
no BOM/CR/NUL/blank line/trailing space, exactly one terminal LF, one first
`schema audit-trace/v1` row, and all remaining canonical lines sorted by raw
byte order. Metavariable spacing below denotes that one TAB. Each record contains
one fact and no list or range. Its closed row grammar is:

```text
schema audit-trace/v1
node <ID> <kind>
section <S> <delivery-rank>
edge <AU> requires <AH|AE|AI>
edge <AH|AE|AI> proved-by <AT>
edge <AH|AE|AI> implemented-in <S>
edge <AT> activated-in <S>
edge <AH|AE|AI> guards <AM>
edge <AM> killed-by <AT>
edge <AM> owned-by <S>
edge <AM> reserved-by <RT>
edge <AM> contrasted-by <PN>
edge <PN> proved-by <AT>
edge <PN> reserved-by <RT>
edge <AT> reserved-by <RT>
semantic <AU> actor-goal <semantic-record-sha256>
semantic <AH> happy <semantic-record-sha256>
semantic <AE> edge <semantic-record-sha256>
semantic <AI> invariant <semantic-record-sha256>
semantic <AT> obligation <semantic-record-sha256>
semantic <AM> defect <semantic-record-sha256>
semantic <PN> positive <semantic-record-sha256>
test <RT> <GoTestSymbol> <activation-S> <package-ID> <role>
package <package-ID> <working-directory> <package-pattern> <import-path> <profile>
```

The pre-S0 importer obtains AU text from each `### AU-NNN — title` heading plus
its following body through the next same-level AU heading or enclosing section;
AH/AE/AI text from each `- **ID[ optional label]:** body` item through the next
same-family item or enclosing heading; and AT text from each `**AT-NNN —** body`
item through the next AT item or enclosing heading. It removes only those exact
syntactic markers. An optional requirement label is preserved as `label: ` before
the body. For each block, every remaining physical line is trimmed of leading and
trailing ASCII space, empty lines are discarded, and the nonempty fragments are
joined by one ASCII space; internal bytes are unchanged. IDs must be zero-padded
three-digit values, appear once in numeric order inside their declared source
section, and a missing/extra marker or empty result refuses. AM and PN text are,
respectively, the Defect and Positive neighbor cells of each MutantTrace row. That
table admits no backslash or literal/escaped pipe in either semantic cell; the
importer splits the exact seven outer pipe delimiters, trims one optional ASCII
space at either cell edge, preserves every other byte including Markdown
backticks, and refuses malformed rows. These rules make source wrapping and
Markdown presentation mechanically unambiguous without treating rendered HTML as
an input.

The semantics JSON top level is exactly
`{"schema":"audit-trace-semantics/v1","records":[...]}`; each record has
exactly `{"id":string,"kind":string,"text":string}`. Kind is exactly
`actor-goal` for AU, `happy` for AH, `edge` for AE, `invariant` for AI,
`obligation` for AT, `defect` for AM, or `positive` for PN, with one record
for every such ID sorted by raw `(kind, id)` bytes. Unknown/duplicate JSON
members, duplicate IDs, trailing JSON values, invalid UTF-8, CR, NUL, and empty
text refuse. Text bytes, including whitespace and Unicode spelling, are
significant; no trim or Unicode normalization occurs. Its tracked bytes are the
result of `json.NewEncoder(w)` over fixed-order structs with
`SetEscapeHTML(false)`, `SetIndent("", "  ")`, and one `Encode` call whose
single terminal LF is retained; no
alternate insignificant formatting is accepted. Exact byte goldens cover empty
strings, `<`, `>`, `&`, U+2028, U+2029, ASCII controls that JSON permits only as
escapes, and multibyte non-ASCII text. A semantic-record digest is SHA-256 of the exact ASCII
domain `frostgrove.audit/trace-semantic/v1\x00` followed by the kind, ID, and
text as three unsigned 64-bit big-endian-length-prefixed byte strings. The
semantic-set digest uses domain `frostgrove.audit/trace-semantics/v1\x00`,
unsigned 64-bit record count, then those canonical records in sorted order.

The graph digest is SHA-256 of
`frostgrove.audit/trace-graph/v1\x00 || uint64be(len(TSV)) || TSV`. The node-set
digest uses `frostgrove.audit/trace-nodes/v1\x00`, the count, then each canonical
node line as an unsigned 64-bit big-endian-length-prefixed string. Each non-schema
fact digest uses `frostgrove.audit/trace-fact/v1\x00` plus one such
length-prefixed canonical line. These raw ASCII domains include the shown NUL
byte. Counts and lengths are never decimal strings. The anchor JSON is strictly
decoded with duplicate/unknown-member and trailing-value rejection, and every
inventory, count, per-record digest, and aggregate is recomputed independently.

The anchor top level has exactly these fixed-order members and types:
`schema:string`, `registry_schema:string`, `semantics_schema:string`,
`inventories:array`, `graph_fact_count:uint64`,
`goal_trace_edge_count:uint64`, `graph_digest:string`,
`node_set_digest:string`, `semantic_set_digest:string`,
`fact_digests:array<string>`, and `semantic_digests:array`.
Schema is `audit-trace-anchor/v1`; the next two values equal the two schemas
above. Each inventory has exactly `kind:string`, `count:uint64`, and
`ids:array<string>`, appears once for every closed node kind in raw kind order,
and has raw-byte-sorted unique IDs whose exact length equals count. Graph fact
count equals all non-schema TSV rows and GoalTrace count equals exact `requires`
rows. Every digest is lowercase 64-hex. Fact digests are the raw-byte-sorted
unique digest of every non-schema row. Each semantic-digest object has exactly
`id:string`, `kind:string`, and `digest:string`, is sorted by raw
`(kind,id)`, and equals its semantic row and manifest record. JSON integers are
plain nonnegative base-10 uint64 without fraction, exponent, sign, or leading
zero. Anchor bytes use the identical fixed-struct `json.Encoder`,
`SetEscapeHTML(false)`, `SetIndent("", "  ")`, and one-`Encode` rule; the
anchor never hashes itself.

The allowed node kinds and their bijective prefixes are exactly `actor-goal/AU`,
`happy-requirement/AH`, `edge-requirement/AE`, `invariant/AI`,
`test-obligation/AT`, `implementation-section/S`, `defect-mutant/AM`,
`positive-neighbor/PN`, `test-reservation/RT`, and `package/PKG`. Test roles are
exactly `coverage`, `mutant`, and `positive`. Forward and reverse
adjacency are derived from these same rows; no reciprocal table, copied Tests
column, active flag, or mutable checkpoint state exists. Every AU has one or more
requirement edges. Every AH/AE/AI has an inbound AU edge and outbound test,
implementation-section, and mutant edges. Every AT has exactly one activation
section independent of the sections that implement each requirement. Every AM has exactly one executable
primary killing AT, one owner, one mutant reservation, and one distinct PN. Every
AU, AH, AE, AI, AT, AM, and PN each have exactly one kind-correct semantic row
whose digest and plaintext record agree. Every PN has the same AT as its mutant and one
positive reservation. Every AT has one coverage reservation at its own activation
section, so a requirement may be implemented across several sections without
fabricating impossible AT×section executions. Once activated, that same test runs
at every checkpoint with an equal or greater delivery rank.
Every endpoint has its declared kind.
Every S0 through S7 node has exactly one section row with a unique decimal
delivery rank 0 through 7, no sign or leading zero. Labels are identities, not
ordering integers; the rows freeze the dependency order
`S0, S1, S2, S4, S3, S5, S6, S7`. S7 is the terminal whole-tree checkpoint and intentionally owns or activates no
requirement, mutant, or reservation; its section row prevents it from becoming
an orphan while the runner still executes every reservation active through S7.

Reservation identities are concrete by construction and collision-checked.
Mutant and positive IDs are `RT-M-<AM suffix>` and `RT-P-<AM suffix>`; PN is
`PN-<AM suffix>`. Coverage IDs are `RT-C-<AT without hyphen>-<activation-S>`. Each MutantTrace
test symbol except AM-TRACE-001 contains exactly one `Kills`; its positive symbol
replaces that token with `Preserves` and otherwise remains byte-identical.
AM-TRACE-001's positive symbol is exactly
`TestAuditTraceCheckpointPreservesCompleteGraph`. Coverage symbols are
`Test<AT without hyphen><S>Contract`. S0 materializes these derivations and refuses
any invalid Go identifier, duplicate ID/symbol within a package, missing `Kills`,
or derived collision. A positive reservation has exactly its paired mutant's
owner-section activation and package; the explicit test row is checked against
that derivation rather than choosing either independently. Active means the
reservation's activation-section delivery rank is no greater than the requested
checkpoint's delivery rank; label suffixes are never compared. `implemented-in` is never inferred from
documentation and `checked-at` is derived cumulatively rather than stored. A
structurally legal but semantically wrong edge assignment remains reviewer-owned,
while every declared goal/requirement/obligation/defect/neighbor text and digest
plus executable AT/AM/PN reservations are mechanical.

Executable `scripts/audit-trace.sh S<N>` validates its one bounded section-label argument
and invokes fixed argument arrays only, with no grep or eval. It first sets the
working directory to repository root `.` and invokes the exact argument array
`["go","test","-json","-count=1","-run","^TestAuditTraceRegistry$","./scripts"]`. It decodes JSON
with encoding/json and requires exactly one non-skipped pass event whose Test is
`TestAuditTraceRegistry` and whose Package is exactly
`github.com/frostgrove/vv/scripts`. It then groups every active
reservation by the registry's fixed package execution tuple, runs anchored exact
test-name regular expressions, and requires exactly one pass event for every
declared `(Package, Test)` pair. Skip, fail, zero/duplicate passes, wrong Package,
command failure, missing symbol, or an unregistered in-scope `TestAT...` or
`TestAuditTrace...` fails, with exactly one bootstrap exception:
`TestAuditTraceRegistry` is outside the reservation graph and is always run and
checked first. The ordinary source-test inventory excludes every Go file whose
build constraints do not admit the default tag set; in particular the
`audit_trace_import`-tagged `TestAuditTraceDesignImport` is never an ordinary
unregistered-test candidate. No `structural` reservation role exists. For each AT it requires its one activated-in edge and
matching coverage reservation, and for each AM it separately observes its mutant
and positive reservations; one broad test cannot impersonate all three roles. S0
subprocess controls independently remove or alter the script, structural test,
TSV, semantics manifest, anchor, closed subgraph, highest ID, edge, semantic body
or digest, each reservation role, package profile/cwd/pattern/import, integration
tag/DSN, and pass event and prove failure.
Every section and final checkpoint invokes the executable with its exact section.

The frozen package records and reservation routing are:

| Package ID | Working directory | Package pattern | Import path | Profile |
|---|---|---|---|---|
| PKG-SCRIPTS | `.` | `./scripts` | `github.com/frostgrove/vv/scripts` | unit |
| PKG-AUDIT | `.` | `./audit` | `github.com/frostgrove/vv/audit` | unit |
| PKG-AUDITMEMORY | `.` | `./audit/auditmemory` | `github.com/frostgrove/vv/audit/auditmemory` | unit |
| PKG-AUDITTEST | `.` | `./audit/audittest` | `github.com/frostgrove/vv/audit/audittest` | unit |
| PKG-AUDITCRUD | `.` | `./audit/auditcrud` | `github.com/frostgrove/vv/audit/auditcrud` | unit |
| PKG-AUDITCRUDBRIDGE | `.` | `./audit/internal/auditcrudbridge` | `github.com/frostgrove/vv/audit/internal/auditcrudbridge` | unit |
| PKG-AUDITPG | `audit/auditpg` | `./...` | `github.com/frostgrove/vv/audit/auditpg` | integration |
| PKG-AUDITFLOW | `test` | `./auditflow` | `github.com/frostgrove/vv/test/auditflow` | integration |

Package profiles are a closed enum. For `unit`, the runner sets `cmd.Dir` to
the repository-relative Working directory and invokes the exact argument array
`go test -json -count=1 -run <anchored-regexp> <package-pattern>`. For
`integration`, it first requires a nonempty
`FROSTGROVE_AUDITPG_TEST_DSN`, preserves that value unchanged, uses the same
directory rule, and invokes
`go test -json -count=1 -tags=integration -run <anchored-regexp> <package-pattern>`.
No other profile or environment synthesis is legal. PackagePattern is only the
argv selector; ImportPath must equal every matching JSON event's Package.
AnchoredRegexp is built with regexp.QuoteMeta from the sorted exact symbols as
`^(?:A|B)$`, never accepted from TSV as executable syntax.

The section-row delivery authority is exact:

| Section | DeliveryRank |
|---|---:|
| S0 | 0 |
| S1 | 1 |
| S2 | 2 |
| S4 | 3 |
| S3 | 4 |
| S5 | 5 |
| S6 | 6 |
| S7 | 7 |

The exact AT activation and coverage routing authority is the following table.
It has one row per AT; ranges, prose defaults, and inferred package selection are
not activation authority.

| AT | Activation | CoveragePackage |
|---|---|---|
| AT-001 | S1 | PKG-AUDIT |
| AT-002 | S1 | PKG-AUDIT |
| AT-003 | S2 | PKG-AUDIT |
| AT-004 | S2 | PKG-AUDITMEMORY |
| AT-005 | S2 | PKG-AUDIT |
| AT-006 | S2 | PKG-AUDIT |
| AT-007 | S2 | PKG-AUDIT |
| AT-008 | S2 | PKG-AUDIT |
| AT-009 | S3 | PKG-AUDIT |
| AT-010 | S3 | PKG-AUDIT |
| AT-011 | S3 | PKG-AUDIT |
| AT-012 | S4 | PKG-AUDITCRUD |
| AT-013 | S5 | PKG-AUDITPG |
| AT-014 | S6 | PKG-AUDITFLOW |
| AT-015 | S2 | PKG-AUDIT |
| AT-016 | S6 | PKG-AUDITTEST |
| AT-017 | S0 | PKG-SCRIPTS |
| AT-018 | S3 | PKG-AUDIT |

Mutant and positive reservations use the owner-section defaults below. Each
exception replaces exactly one default. A positive reservation always uses its
paired mutant's resolved package; the TSV still repeats and validates every
explicit package rather than computing it during execution.

| Owner | DefaultPackage |
|---|---|
| S0 | PKG-SCRIPTS |
| S1 | PKG-AUDIT |
| S2 | PKG-AUDIT |
| S3 | PKG-AUDIT |
| S4 | PKG-AUDITCRUD |
| S5 | PKG-AUDITPG |
| S6 | PKG-SCRIPTS |

| Mutant | PackageException |
|---|---|
| AM-APPEND-001 | PKG-AUDITMEMORY |
| AM-BRIDGE-001 | PKG-AUDITCRUDBRIDGE |
| AM-CONFORM-001 | PKG-AUDITTEST |
| AM-OBS-001 | PKG-AUDITFLOW |

The registry stores each table row as the corresponding individual activation,
coverage-test, or mutant/positive-test fact and rejects a second or missing
assignment. S7 is only the terminal cumulative checkpoint declared by its section
row.

The frozen pre-S0 completeness counts are exact consequences of the tables in
this section and the current use-case source. S0 must refuse materialization if
any recomputed value differs.

| NodeKind | Count |
|---|---:|
| actor-goal | 11 |
| happy-requirement | 99 |
| edge-requirement | 117 |
| invariant | 83 |
| test-obligation | 18 |
| implementation-section | 8 |
| defect-mutant | 70 |
| positive-neighbor | 70 |
| test-reservation | 158 |
| package | 8 |
| total | 642 |

| GraphFactKind | Count |
|---|---:|
| node | 642 |
| section | 8 |
| actor-goal requires requirement | 460 |
| requirement proved-by test-obligation | 722 |
| requirement implemented-in section | 718 |
| test-obligation activated-in section | 18 |
| requirement guards defect-mutant | 532 |
| defect-mutant killed-by test-obligation | 70 |
| defect-mutant owned-by section | 70 |
| defect-mutant reserved-by test-reservation | 70 |
| defect-mutant contrasted-by positive-neighbor | 70 |
| positive-neighbor proved-by test-obligation | 70 |
| positive-neighbor reserved-by test-reservation | 70 |
| test-obligation reserved-by test-reservation | 18 |
| semantic | 468 |
| test | 158 |
| package | 8 |
| total non-schema facts | 4172 |

Thus the semantic manifest contains exactly 468 records and the registry contains
exactly 4172 non-schema facts plus its one schema row. Equivalently, the fact
count is `1740 + GoalEdges + RequirementATEdges + RequirementSectionEdges +
RequirementMutantEdges`; substituting `460 + 722 + 718 + 532` yields 4172. The
node-set, graph, semantic-set, per-fact, and per-semantic digests and every anchor
inventory are still computed from canonical materialized bytes by the algorithms
above; neither this Markdown file nor an unchecked copied digest is an input to
those calculations.

### GoalTrace

Each row names one exact actor goal and its nonempty requirement edges. Tests are
derived through RequirementTrace and are not copied here.

| Goal | Requirements |
|---|---|
| AU-001 | AH-003, AH-013, AH-015, AH-016, AH-017, AH-019, AH-020, AH-023, AH-032, AH-065, AH-092, AE-006, AE-010, AE-012, AE-074, AE-076, AE-110, AI-001, AI-002, AI-035, AI-048, AI-055, AI-076 |
| AU-002 | AH-007, AH-008, AH-009, AH-010, AH-011, AH-024, AH-034, AH-035, AH-056, AH-064, AH-070, AH-075, AH-080, AH-081, AH-093, AH-095, AE-003, AE-004, AE-008, AE-013, AE-016, AE-021, AE-022, AE-023, AE-024, AE-025, AE-026, AE-027, AE-028, AE-045, AE-057, AE-063, AE-075, AE-082, AE-083, AE-092, AE-094, AE-111, AE-113, AI-009, AI-031, AI-036, AI-045, AI-053, AI-062, AI-064, AI-077, AI-079 |
| AU-003 | AH-025, AH-026, AH-033, AH-035, AH-036, AH-059, AH-086, AE-019, AE-041, AE-045, AE-067, AE-102, AI-008, AI-032, AI-036, AI-040, AI-068 |
| AU-004 | AH-037, AH-038, AH-039, AH-040, AH-043, AH-060, AH-062, AH-063, AH-071, AH-076, AH-077, AH-078, AH-091, AH-094, AH-096, AH-097, AH-099, AE-014, AE-029, AE-030, AE-031, AE-032, AE-050, AE-055, AE-056, AE-059, AE-068, AE-071, AE-086, AE-088, AE-089, AE-090, AE-093, AE-104, AE-106, AE-112, AE-114, AE-115, AE-117, AI-006, AI-019, AI-020, AI-021, AI-022, AI-038, AI-041, AI-042, AI-043, AI-046, AI-058, AI-059, AI-060, AI-063, AI-070, AI-071, AI-078, AI-080, AI-081, AI-083 |
| AU-005 | AH-006, AH-014, AH-041, AH-044, AH-067, AH-079, AH-081, AH-095, AH-096, AE-033, AE-034, AE-035, AE-043, AE-049, AE-052, AE-078, AE-091, AE-094, AE-113, AE-114, AI-033, AI-034, AI-037, AI-050, AI-061, AI-064, AI-079, AI-080 |
| AU-006 | AH-004, AH-018, AH-021, AH-022, AH-065, AH-066, AH-068, AH-069, AH-073, AH-082, AH-092, AH-093, AE-001, AE-002, AE-005, AE-028, AE-048, AE-076, AE-077, AE-079, AE-087, AE-107, AE-110, AE-111, AI-004, AI-005, AI-049, AI-054, AI-055, AI-056, AI-069, AI-076, AI-077 |
| AU-007 | AH-012, AH-027, AH-028, AH-029, AH-030, AH-031, AH-045, AH-047, AH-055, AH-083, AH-084, AH-085, AH-087, AH-088, AH-089, AH-090, AE-015, AE-018, AE-046, AE-047, AE-053, AE-058, AE-062, AE-064, AE-072, AE-095, AE-096, AE-097, AE-098, AE-099, AE-100, AE-101, AE-102, AE-103, AE-104, AE-105, AE-106, AI-010, AI-011, AI-012, AI-025, AI-026, AI-044, AI-065, AI-066, AI-067, AI-068, AI-069, AI-070, AI-071, AI-072, AI-074 |
| AU-008 | AH-042, AH-046, AH-048, AH-049, AH-050, AH-051, AH-052, AH-053, AH-054, AH-061, AH-068, AH-090, AH-091, AH-098, AE-036, AE-037, AE-038, AE-050, AE-051, AE-054, AE-060, AE-061, AE-066, AE-069, AE-070, AE-079, AE-080, AE-105, AE-107, AE-108, AE-116, AI-007, AI-021, AI-023, AI-024, AI-039, AI-051, AI-070, AI-073, AI-082 |
| AU-009 | AH-057, AH-069, AH-083, AH-086, AH-092, AH-098, AH-099, AE-011, AE-017, AE-020, AE-039, AE-040, AE-042, AE-044, AE-065, AE-087, AE-095, AE-101, AE-102, AE-103, AE-109, AE-110, AE-116, AE-117, AI-013, AI-014, AI-015, AI-016, AI-017, AI-018, AI-027, AI-028, AI-054, AI-065, AI-068, AI-074, AI-075, AI-076, AI-082, AI-083 |
| AU-010 | AH-001, AH-002, AH-005, AH-006, AH-014, AH-058, AH-066, AH-072, AH-074, AH-076, AH-079, AH-082, AH-087, AH-089, AH-091, AH-093, AH-094, AH-095, AH-096, AH-097, AH-098, AH-099, AE-007, AE-009, AE-072, AE-073, AE-081, AE-084, AE-085, AE-097, AE-098, AE-099, AE-100, AE-103, AE-104, AE-105, AE-106, AE-107, AE-108, AE-110, AE-111, AE-112, AE-113, AE-114, AE-115, AE-116, AE-117, AI-003, AI-029, AI-030, AI-047, AI-052, AI-056, AI-057, AI-065, AI-066, AI-067, AI-068, AI-069, AI-070, AI-071, AI-072, AI-073, AI-074, AI-076, AI-077, AI-078, AI-079, AI-080, AI-081, AI-082, AI-083 |
| AU-011 | AH-082, AH-083, AH-084, AH-085, AH-086, AH-087, AH-088, AH-089, AH-090, AH-091, AH-092, AH-097, AH-098, AH-099, AE-095, AE-096, AE-097, AE-098, AE-099, AE-100, AE-101, AE-102, AE-103, AE-104, AE-105, AE-106, AE-107, AE-108, AE-109, AE-110, AE-115, AE-116, AE-117, AI-065, AI-066, AI-067, AI-068, AI-069, AI-070, AI-071, AI-072, AI-073, AI-074, AI-075, AI-076, AI-081, AI-082, AI-083 |

### RequirementTrace

AT obligations and implementation Sections are independent adjacency sets, not
positional pairs or a Cartesian product. AT activation is the single map above.

| Requirement | AT obligations | Sections | Mutants |
|---|---|---|---|
| AH-001 | AT-001 | S1 | AM-CAT-001 |
| AH-002 | AT-001 | S1 | AM-CAT-001 |
| AH-003 | AT-001, AT-002 | S1 | AM-CAT-001, AM-CODE-001, AM-SEMANTICS-001 |
| AH-004 | AT-001 | S1 | AM-CAT-001, AM-SEMANTICS-001 |
| AH-005 | AT-002 | S1 | AM-CODEC-001, AM-SEMANTICS-001 |
| AH-006 | AT-002 | S1 | AM-CODEC-001, AM-SEMANTICS-001 |
| AH-007 | AT-002, AT-012 | S1, S4 | AM-ALLOW-001, AM-CRUD-001 |
| AH-008 | AT-002, AT-012 | S1, S4 | AM-ALLOW-001, AM-CRUD-001 |
| AH-009 | AT-002, AT-012 | S1, S4 | AM-ALLOW-001, AM-CRUD-001 |
| AH-010 | AT-002, AT-012 | S1, S4 | AM-ALLOW-001, AM-CRUD-001 |
| AH-011 | AT-002, AT-012 | S1, S4 | AM-ALLOW-001, AM-CRUD-001 |
| AH-012 | AT-003 | S1, S2 | AM-PROTECT-001 |
| AH-013 | AT-003, AT-004 | S1, S2 | AM-HMAC-001 |
| AH-014 | AT-002 | S1 | AM-CODEC-001, AM-SEMANTICS-001 |
| AH-015 | AT-002 | S1, S2 | AM-CONTEXT-001 |
| AH-016 | AT-002 | S1, S2 | AM-CONTEXT-001 |
| AH-017 | AT-002 | S1, S2 | AM-CONTEXT-001 |
| AH-018 | AT-002 | S1, S2 | AM-CONTEXT-001 |
| AH-019 | AT-002 | S1, S2 | AM-CONTEXT-001 |
| AH-020 | AT-002 | S1, S2 | AM-CODE-001 |
| AH-021 | AT-002 | S1, S2 | AM-CONTEXT-001 |
| AH-022 | AT-002 | S1, S2 | AM-CONTEXT-001 |
| AH-023 | AT-004, AT-005, AT-007 | S2 | AM-APPEND-001 |
| AH-024 | AT-005, AT-007, AT-012 | S2, S4, S5 | AM-EXEC-001, AM-BRIDGE-001 |
| AH-025 | AT-005, AT-007, AT-012 | S2, S4, S5 | AM-EXEC-001 |
| AH-026 | AT-005, AT-007, AT-012 | S2, S4, S5 | AM-EXEC-001 |
| AH-027 | AT-004 | S2, S5 | AM-IDEMP-001 |
| AH-028 | AT-004 | S2, S5 | AM-IDEMP-001 |
| AH-029 | AT-005, AT-008 | S2, S4, S5 | AM-OUTCOME-001 |
| AH-030 | AT-005, AT-008 | S2, S4, S5 | AM-OUTCOME-001 |
| AH-031 | AT-005, AT-008 | S2, S4, S5 | AM-OUTCOME-001 |
| AH-032 | AT-015 | S1, S2 | AM-CONSEQUENCE-001 |
| AH-033 | AT-006 | S2 | AM-GROUP-001 |
| AH-034 | AT-004, AT-006, AT-012 | S2, S4, S5 | AM-CONTINUITY-001 |
| AH-035 | AT-006, AT-012 | S2, S4 | AM-GROUP-001, AM-BRIDGE-001 |
| AH-036 | AT-005, AT-007, AT-012 | S2, S4 | AM-EXEC-001 |
| AH-037 | AT-009, AT-010 | S3, S5, S6 | AM-TARGET-001 |
| AH-038 | AT-009 | S3, S5 | AM-CURSOR-001 |
| AH-039 | AT-010 | S3, S5 | AM-READ-001 |
| AH-040 | AT-010 | S3, S5 | AM-READ-001 |
| AH-041 | AT-010 | S3, S5 | AM-HISTORY-001 |
| AH-042 | AT-011 | S3, S5 | AM-CORRECT-001 |
| AH-043 | AT-010, AT-011 | S3, S5 | AM-EVIDENCE-001 |
| AH-044 | AT-001, AT-010 | S1, S3, S5 | AM-HISTORY-001 |
| AH-045 | AT-003 | S2, S5 | AM-SIGN-001 |
| AH-046 | AT-003, AT-008, AT-011 | S2, S3, S5 | AM-SIGN-001 |
| AH-047 | AT-013 | S5 | AM-POSTGRES-001 |
| AH-048 | AT-011 | S3, S5 | AM-LIFE-001 |
| AH-049 | AT-011 | S3, S5 | AM-HOLD-001, AM-HOLD-SET-001 |
| AH-050 | AT-011 | S3, S5 | AM-BACKING-001 |
| AH-051 | AT-008, AT-011 | S3, S5 | AM-OUTCOME-001 |
| AH-052 | AT-001, AT-011 | S1, S3, S5 | AM-RETENTION-001 |
| AH-053 | AT-007, AT-011 | S2, S3, S5 | AM-LIFE-001 |
| AH-054 | AT-004, AT-011 | S2, S3, S5 | AM-HOLD-001, AM-HOLD-SET-001 |
| AH-055 | AT-005, AT-007 | S2, S5 | AM-OUTCOME-001 |
| AH-056 | AT-012, AT-014 | S4, S6 | AM-CRUD-BOUNDARY-001 |
| AH-057 | AT-016 | S1, S2, S6 | AM-API-001 |
| AH-058 | AT-016 | S2, S5, S6 | AM-CONFORM-001 |
| AH-059 | AT-007 | S2, S4, S5 | AM-EXEC-001 |
| AH-060 | AT-002, AT-009, AT-011 | S1, S3, S5 | AM-SCOPE-001 |
| AH-061 | AT-011 | S3, S5 | AM-LIFE-001 |
| AH-062 | AT-007, AT-009, AT-011, AT-013, AT-016 | S2, S3, S5, S6 | AM-BACKING-001 |
| AH-063 | AT-008, AT-010, AT-011, AT-014 | S2, S3, S6 | AM-DENIAL-001 |
| AH-064 | AT-007, AT-012, AT-014 | S4, S6 | AM-SAVEPOINT-001 |
| AH-065 | AT-002, AT-010, AT-011 | S1, S2, S3, S5 | AM-CODE-001 |
| AH-066 | AT-001, AT-002, AT-016 | S1, S6 | AM-SEMANTICS-001, AM-CODEC-001 |
| AH-067 | AT-010 | S3, S5 | AM-TIME-METADATA-001 |
| AH-068 | AT-002, AT-004, AT-011 | S1, S2, S3, S5 | AM-HOLD-MATTER-001, AM-HOLD-PROJECTION-001, AM-HOLD-SET-001 |
| AH-069 | AT-001, AT-016 | S1, S2, S5, S6 | AM-CATALOG-ADMIN-001 |
| AH-070 | AT-012, AT-014 | S4, S6 | AM-CRUD-BOUNDARY-001, AM-BRIDGE-001 |
| AH-071 | AT-003, AT-010, AT-011 | S2, S3, S5 | AM-EVIDENCE-DIGEST-001 |
| AH-072 | AT-002, AT-007, AT-010, AT-011 | S1, S2, S3, S5 | AM-CANCEL-CAUSE-001 |
| AH-073 | AT-002, AT-010, AT-011 | S1, S3, S5 | AM-CODE-001 |
| AH-074 | AT-017 | S0 | AM-TRACE-001 |
| AH-075 | AT-004, AT-006, AT-007, AT-010, AT-012 | S1, S2, S3, S4, S5 | AM-SCOPED-CONTINUITY-001 |
| AH-076 | AT-002, AT-009, AT-010, AT-016 | S1, S3, S6 | AM-QUERY-001, AM-EXACT-READ-001, AM-API-001 |
| AH-077 | AT-002, AT-009, AT-010, AT-013 | S1, S3, S5 | AM-QUERY-001, AM-TIME-001, AM-READ-001 |
| AH-078 | AT-009, AT-013 | S3, S5 | AM-SNAPSHOT-001, AM-CURSOR-001 |
| AH-079 | AT-010, AT-016 | S3, S6 | AM-COMPARE-001, AM-API-001 |
| AH-080 | AT-004, AT-012, AT-013 | S2, S4, S5 | AM-TERMINAL-001, AM-CONTINUITY-001, AM-CRUD-001 |
| AH-081 | AT-004, AT-010, AT-012, AT-013, AT-016 | S1, S2, S3, S4, S5, S6 | AM-BASELINE-001 |
| AH-082 | AT-001, AT-002, AT-016, AT-018 | S3, S6 | AM-SEMANTICS-001, AM-ATTEMPT-CATALOG-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-BOUND-001, AM-ATTEMPT-PRIVACY-001 |
| AH-083 | AT-005, AT-008, AT-018 | S3 | AM-ATTEMPT-START-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001 |
| AH-084 | AT-003, AT-004, AT-018 | S3, S5 | AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-BOUND-001 |
| AH-085 | AT-005, AT-008, AT-018 | S3, S5 | AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001, AM-ATTEMPT-WIRE-001 |
| AH-086 | AT-005, AT-006, AT-007, AT-012, AT-018 | S3, S4, S5 | AM-EXEC-001, AM-GROUP-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001 |
| AH-087 | AT-001, AT-003, AT-004, AT-013, AT-018 | S3, S5 | AM-IDEMP-001, AM-ATTEMPT-START-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-CATALOG-001 |
| AH-088 | AT-005, AT-007, AT-008, AT-018 | S3, S5 | AM-OUTCOME-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001, AM-ATTEMPT-RESUME-001 |
| AH-089 | AT-007, AT-010, AT-018 | S3, S5 | AM-BACKING-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-PRIVACY-001, AM-ATTEMPT-RESUME-001 |
| AH-090 | AT-005, AT-007, AT-010, AT-018 | S3, S5 | AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001, AM-ATTEMPT-RESUME-001 |
| AH-091 | AT-003, AT-010, AT-018 | S3, S5 | AM-HISTORY-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-BOUND-001, AM-ATTEMPT-PRIVACY-001 |
| AH-092 | AT-002, AT-009, AT-010, AT-016, AT-018 | S3, S5, S6 | AM-TARGET-001, AM-QUERY-001, AM-ATTEMPT-WIRE-001 |
| AH-093 | AT-002, AT-009, AT-010, AT-013, AT-016 | S3, S5, S6 | AM-ALLOW-001, AM-HMAC-001, AM-QUERY-001 |
| AH-094 | AT-002, AT-009, AT-010, AT-013, AT-016 | S3, S5, S6 | AM-HMAC-001, AM-BOUNDS-001, AM-QUERY-001, AM-SNAPSHOT-001 |
| AH-095 | AT-004, AT-010, AT-012, AT-013, AT-016 | S3, S4, S5, S6 | AM-CONTINUITY-001, AM-COMPARE-001, AM-TERMINAL-001 |
| AH-096 | AT-010, AT-013, AT-016 | S3, S5, S6 | AM-HISTORY-001, AM-BOUNDS-001, AM-CONTINUITY-001 |
| AH-097 | AT-009, AT-010, AT-013, AT-016, AT-018 | S3, S5, S6 | AM-QUERY-001, AM-SNAPSHOT-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-BOUND-001 |
| AH-098 | AT-007, AT-013, AT-016, AT-018 | S3, S5, S6 | AM-BACKING-001, AM-CONFORM-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-CATALOG-001 |
| AH-099 | AT-007, AT-010, AT-014, AT-016, AT-018 | S3, S5, S6 | AM-EVIDENCE-DIGEST-001, AM-ATTEMPT-CATALOG-001, AM-ATTEMPT-PRIVACY-001, AM-ATTEMPT-RESUME-001 |
| AE-001 | AT-001, AT-002 | S1 | AM-NAME-001 |
| AE-002 | AT-001 | S1 | AM-CAT-001 |
| AE-003 | AT-002 | S1 | AM-ALLOW-001 |
| AE-004 | AT-002 | S1 | AM-ALLOW-001 |
| AE-005 | AT-002 | S1 | AM-ALLOW-001 |
| AE-006 | AT-002 | S1 | AM-ALLOW-001 |
| AE-007 | AT-002 | S1, S2 | AM-CODEC-001, AM-SEMANTICS-001 |
| AE-008 | AT-002 | S1, S2 | AM-CODEC-001 |
| AE-009 | AT-002 | S1, S2 | AM-CODEC-001 |
| AE-010 | AT-002 | S1, S2 | AM-CONTEXT-001 |
| AE-011 | AT-002 | S1, S2 | AM-CONTEXT-001 |
| AE-012 | AT-002 | S1, S2 | AM-CONTEXT-001 |
| AE-013 | AT-008, AT-014 | S2, S3, S6 | AM-DENIAL-001 |
| AE-014 | AT-008, AT-014 | S2, S3, S6 | AM-DENIAL-001 |
| AE-015 | AT-005, AT-008 | S2, S5 | AM-OUTCOME-001 |
| AE-016 | AT-008, AT-012 | S2, S4 | AM-ERROR-001 |
| AE-017 | AT-014 | S2, S6 | AM-OBS-001 |
| AE-018 | AT-008 | S2, S5 | AM-ERROR-001 |
| AE-019 | AT-007, AT-012 | S4, S5 | AM-SAVEPOINT-001 |
| AE-020 | AT-007, AT-012 | S4 | AM-TOPO-001 |
| AE-021 | AT-002, AT-012 | S1, S4 | AM-SINGLE-001 |
| AE-022 | AT-012, AT-013 | S4, S5 | AM-CRUD-001 |
| AE-023 | AT-012 | S4, S5 | AM-CRUD-001 |
| AE-024 | AT-012 | S4, S5 | AM-CRUD-001 |
| AE-025 | AT-012 | S4, S5 | AM-CRUD-001 |
| AE-026 | AT-012 | S4, S5 | AM-CRUD-001 |
| AE-027 | AT-012 | S4, S5 | AM-CRUD-001 |
| AE-028 | AT-012 | S4, S5 | AM-CRUD-001 |
| AE-029 | AT-009, AT-010 | S3, S5 | AM-READ-001, AM-SCOPE-001 |
| AE-030 | AT-009 | S3, S5 | AM-CURSOR-001 |
| AE-031 | AT-002, AT-009, AT-011 | S1, S2, S3, S5 | AM-BOUNDS-001 |
| AE-032 | AT-009, AT-010 | S3, S5 | AM-READ-001 |
| AE-033 | AT-010 | S3, S5 | AM-HISTORY-001, AM-SEMANTICS-001 |
| AE-034 | AT-003, AT-008, AT-011 | S2, S3, S5 | AM-SIGN-001 |
| AE-035 | AT-010, AT-011 | S3, S5 | AM-RETENTION-001 |
| AE-036 | AT-011 | S3, S5 | AM-LIFE-001, AM-CORRECT-001, AM-HOLD-001 |
| AE-037 | AT-011 | S1, S3, S5 | AM-RETENTION-001 |
| AE-038 | AT-011, AT-013 | S3, S5 | AM-BACKING-001 |
| AE-039 | AT-014 | S2, S6 | AM-OBS-001 |
| AE-040 | AT-014 | S2, S6 | AM-OBS-001 |
| AE-041 | AT-007, AT-014 | S2, S6 | AM-EXEC-001 |
| AE-042 | AT-014, AT-016 | S1, S2, S5, S6 | AM-TOPO-001 |
| AE-043 | AT-010 | S3, S5 | AM-HISTORY-001, AM-BOUNDS-001 |
| AE-044 | AT-006, AT-007, AT-012 | S2, S4 | AM-ALLOW-001, AM-ENTITY-DRAFT-001 |
| AE-045 | AT-006, AT-012 | S2, S4, S5 | AM-GROUP-001, AM-CONTINUITY-001 |
| AE-046 | AT-003, AT-004 | S2, S5 | AM-PROTECT-001 |
| AE-047 | AT-004, AT-005 | S2, S5 | AM-IDEMP-001 |
| AE-048 | AT-001, AT-013 | S1, S5 | AM-CAT-001 |
| AE-049 | AT-001, AT-010 | S1, S3, S5 | AM-HISTORY-001 |
| AE-050 | AT-010, AT-011 | S3, S5 | AM-EVIDENCE-001 |
| AE-051 | AT-009, AT-011 | S3, S5 | AM-BACKING-001 |
| AE-052 | AT-010 | S3, S5 | AM-TIME-001 |
| AE-053 | AT-004, AT-005, AT-012 | S2, S4, S5 | AM-IDEMP-001, AM-EXEC-001 |
| AE-054 | AT-001, AT-011 | S1, S3, S5 | AM-RETENTION-001 |
| AE-055 | AT-010, AT-011 | S3, S5 | AM-READ-001 |
| AE-056 | AT-009, AT-010 | S2, S3, S5 | AM-HMAC-001, AM-TARGET-001 |
| AE-057 | AT-012, AT-014 | S4, S6 | AM-CRUD-BOUNDARY-001, AM-BRIDGE-001 |
| AE-058 | AT-004, AT-007, AT-011 | S2, S3, S5 | AM-IDENTITY-001 |
| AE-059 | AT-010, AT-011, AT-014 | S3, S6 | AM-DENIAL-001, AM-EVIDENCE-001 |
| AE-060 | AT-011 | S3, S5 | AM-LIFE-001, AM-READ-001 |
| AE-061 | AT-004, AT-011 | S2, S3, S5 | AM-HOLD-001 |
| AE-062 | AT-005, AT-007 | S2, S5 | AM-OUTCOME-001, AM-EXEC-001 |
| AE-063 | AT-012 | S4, S6 | AM-CRUD-BOUNDARY-001, AM-BRIDGE-001 |
| AE-064 | AT-005 | S2, S5 | AM-OUTCOME-001 |
| AE-065 | AT-002, AT-007, AT-010, AT-011 | S1, S2, S3, S5 | AM-CONTEXT-001 |
| AE-066 | AT-003, AT-008, AT-010, AT-011 | S2, S3, S5 | AM-SIGN-001 |
| AE-067 | AT-007, AT-016 | S2, S4, S5 | AM-EXEC-001 |
| AE-068 | AT-002, AT-009, AT-010, AT-011 | S1, S3, S5 | AM-SCOPE-001 |
| AE-069 | AT-011 | S2, S3, S5 | AM-LIFE-001 |
| AE-070 | AT-011 | S3, S5 | AM-CORRECT-001 |
| AE-071 | AT-009, AT-011, AT-013 | S2, S3, S5 | AM-BACKING-001 |
| AE-072 | AT-002, AT-003, AT-004, AT-016 | S1, S2, S6 | AM-HMAC-001, AM-API-001 |
| AE-073 | AT-016, AT-017 | S2, S5, S6 | AM-CONFORM-001 |
| AE-074 | AT-009, AT-010 | S1, S3, S5 | AM-TARGET-001 |
| AE-075 | AT-007, AT-012, AT-014 | S4, S6 | AM-SAVEPOINT-001 |
| AE-076 | AT-002, AT-010, AT-011 | S1, S2, S3, S5 | AM-CODE-001 |
| AE-077 | AT-001, AT-002, AT-016 | S1, S6 | AM-SEMANTICS-001, AM-CODEC-001 |
| AE-078 | AT-010, AT-013 | S3, S5 | AM-TIME-METADATA-001 |
| AE-079 | AT-002, AT-011 | S1, S3, S5 | AM-HOLD-MATTER-001 |
| AE-080 | AT-004, AT-011, AT-013 | S2, S3, S5 | AM-HOLD-PROJECTION-001, AM-HOLD-SET-001 |
| AE-081 | AT-002, AT-007, AT-010, AT-011 | S1, S2, S3, S5 | AM-CANCEL-CAUSE-001 |
| AE-082 | AT-004, AT-006, AT-007, AT-010, AT-012 | S1, S2, S3, S4, S5 | AM-SCOPED-CONTINUITY-001 |
| AE-083 | AT-012, AT-014 | S4, S6 | AM-CRUD-BOUNDARY-001, AM-BRIDGE-001 |
| AE-084 | AT-017 | S0 | AM-TRACE-001 |
| AE-085 | AT-001, AT-002, AT-016 | S1, S6 | AM-CODEC-001, AM-SEMANTICS-001 |
| AE-086 | AT-003, AT-010, AT-011 | S2, S3, S5 | AM-EVIDENCE-DIGEST-001 |
| AE-087 | AT-001, AT-013, AT-016 | S1, S2, S5, S6 | AM-CATALOG-ADMIN-001 |
| AE-088 | AT-002, AT-009, AT-010 | S1, S3 | AM-QUERY-001, AM-READ-001 |
| AE-089 | AT-002, AT-009, AT-010 | S1, S2, S3 | AM-QUERY-001, AM-HMAC-001 |
| AE-090 | AT-009, AT-013 | S3, S5 | AM-SNAPSHOT-001, AM-CURSOR-001, AM-BOUNDS-001 |
| AE-091 | AT-010, AT-016 | S3, S6 | AM-COMPARE-001, AM-BOUNDS-001 |
| AE-092 | AT-004, AT-012, AT-013 | S2, S4, S5 | AM-TERMINAL-001, AM-CRUD-001 |
| AE-093 | AT-003, AT-010, AT-013 | S2, S3, S5 | AM-EXACT-READ-001, AM-EVIDENCE-DIGEST-001 |
| AE-094 | AT-004, AT-007, AT-010, AT-012, AT-013 | S2, S3, S4, S5 | AM-BASELINE-001 |
| AE-095 | AT-005, AT-008, AT-018 | S3, S5 | AM-ATTEMPT-START-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001 |
| AE-096 | AT-008, AT-015, AT-018 | S3 | AM-CONSEQUENCE-001, AM-ATTEMPT-START-001, AM-ATTEMPT-STATE-001 |
| AE-097 | AT-001, AT-003, AT-004, AT-018 | S3, S5 | AM-IDEMP-001, AM-IDENTITY-001, AM-ATTEMPT-START-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-CATALOG-001 |
| AE-098 | AT-003, AT-008, AT-018 | S3, S5 | AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001 |
| AE-099 | AT-004, AT-005, AT-018 | S3, S5 | AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001 |
| AE-100 | AT-002, AT-007, AT-018 | S3, S5 | AM-BOUNDS-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-BOUND-001 |
| AE-101 | AT-005, AT-008, AT-018 | S3, S5 | AM-ERROR-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001 |
| AE-102 | AT-005, AT-007, AT-008, AT-013, AT-018 | S3, S5 | AM-OUTCOME-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001, AM-ATTEMPT-RESUME-001 |
| AE-103 | AT-007, AT-018 | S3, S5 | AM-BACKING-001, AM-ATTEMPT-CATALOG-001, AM-ATTEMPT-PRIVACY-001, AM-ATTEMPT-RESUME-001 |
| AE-104 | AT-003, AT-007, AT-010, AT-018 | S3, S5 | AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-RESUME-001 |
| AE-105 | AT-007, AT-010, AT-018 | S3, S5 | AM-TIME-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001, AM-ATTEMPT-RESUME-001 |
| AE-106 | AT-003, AT-010, AT-018 | S3, S5 | AM-TIME-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001 |
| AE-107 | AT-001, AT-013, AT-016, AT-018 | S3, S5, S6 | AM-CATALOG-ADMIN-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-CATALOG-001 |
| AE-108 | AT-011, AT-013, AT-018 | S3, S5 | AM-HOLD-001, AM-RETENTION-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-BOUND-001, AM-ATTEMPT-RETENTION-001 |
| AE-109 | AT-014, AT-018 | S3, S6 | AM-DENIAL-001, AM-TOPO-001, AM-ATTEMPT-START-001, AM-ATTEMPT-PRIVACY-001 |
| AE-110 | AT-002, AT-009, AT-010, AT-018 | S3, S5, S6 | AM-TARGET-001, AM-QUERY-001, AM-ATTEMPT-WIRE-001 |
| AE-111 | AT-002, AT-009, AT-010, AT-013 | S3, S5 | AM-ALLOW-001, AM-HMAC-001, AM-QUERY-001 |
| AE-112 | AT-002, AT-009, AT-010, AT-013 | S3, S5 | AM-HMAC-001, AM-BOUNDS-001, AM-QUERY-001, AM-SNAPSHOT-001 |
| AE-113 | AT-004, AT-010, AT-012, AT-013 | S3, S4, S5 | AM-HISTORY-001, AM-COMPARE-001, AM-TERMINAL-001 |
| AE-114 | AT-010, AT-013 | S3, S5 | AM-HISTORY-001, AM-BOUNDS-001, AM-CONTINUITY-001 |
| AE-115 | AT-009, AT-010, AT-018 | S3, S5 | AM-QUERY-001, AM-SNAPSHOT-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-BOUND-001 |
| AE-116 | AT-007, AT-013, AT-018 | S3, S5 | AM-BACKING-001, AM-CONFORM-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-CATALOG-001 |
| AE-117 | AT-007, AT-010, AT-018 | S3, S5 | AM-EVIDENCE-DIGEST-001, AM-ATTEMPT-CATALOG-001, AM-ATTEMPT-PRIVACY-001, AM-ATTEMPT-RESUME-001 |
| AI-001 | AT-002, AT-010 | S1, S3, S4 | AM-ALLOW-001 |
| AI-002 | AT-003 | S1, S2 | AM-PROTECT-001 |
| AI-003 | AT-002, AT-015 | S1, S2 | AM-ALLOW-001 |
| AI-004 | AT-001, AT-002 | S1 | AM-CAT-001, AM-CODE-001, AM-SEMANTICS-001 |
| AI-005 | AT-001, AT-010, AT-011 | S1, S3, S5 | AM-CAT-001, AM-SEMANTICS-001 |
| AI-006 | AT-002, AT-007, AT-009, AT-011 | S1, S2, S3, S5 | AM-BOUNDS-001 |
| AI-007 | AT-004, AT-013 | S2, S5 | AM-APPEND-001 |
| AI-008 | AT-005, AT-007, AT-012 | S2, S4, S5 | AM-EXEC-001 |
| AI-009 | AT-007, AT-012 | S2, S4, S5 | AM-CRUD-001, AM-ENTITY-DRAFT-001 |
| AI-010 | AT-005, AT-008 | S2, S5 | AM-OUTCOME-001 |
| AI-011 | AT-003, AT-004 | S2, S5 | AM-IDEMP-001 |
| AI-012 | AT-005 | S2, S4, S5 | AM-OUTCOME-001 |
| AI-013 | AT-014, AT-016 | S6 | AM-TOPO-001 |
| AI-014 | AT-016 | S2, S5, S6 | AM-TOPO-001 |
| AI-015 | AT-014 | S6 | AM-TOPO-001 |
| AI-016 | AT-007, AT-012 | S2, S4 | AM-OWNER-001 |
| AI-017 | AT-007, AT-012, AT-014 | S4, S6 | AM-CRUD-BOUNDARY-001 |
| AI-018 | AT-013, AT-016 | S1, S2, S5 | AM-TOPO-001 |
| AI-019 | AT-010 | S3, S5 | AM-READ-001 |
| AI-020 | AT-010, AT-011 | S3, S5 | AM-READ-001 |
| AI-021 | AT-009, AT-010, AT-011 | S2, S3, S5 | AM-LIFE-001 |
| AI-022 | AT-009, AT-011 | S3, S5 | AM-CURSOR-001, AM-BACKING-001, AM-SCOPE-001 |
| AI-023 | AT-011 | S1, S3, S5 | AM-RETENTION-001, AM-HOLD-001, AM-HOLD-SET-001 |
| AI-024 | AT-010, AT-011 | S3, S5 | AM-EVIDENCE-001 |
| AI-025 | AT-003, AT-004 | S1, S2, S5 | AM-PROTECT-001 |
| AI-026 | AT-003, AT-008, AT-011 | S2, S3, S5 | AM-SIGN-001 |
| AI-027 | AT-014 | S2, S6 | AM-OBS-001 |
| AI-028 | AT-014 | S6 | AM-OBS-001 |
| AI-029 | AT-002, AT-007, AT-010, AT-011, AT-016 | S1, S2, S3, S5, S6 | AM-OWNER-001 |
| AI-030 | AT-002, AT-006, AT-007, AT-010, AT-011, AT-013 | S1, S2, S3, S5 | AM-CONCURRENCY-001 |
| AI-031 | AT-002, AT-012 | S1, S4 | AM-SINGLE-001 |
| AI-032 | AT-006 | S2, S5 | AM-GROUP-001 |
| AI-033 | AT-010, AT-013 | S2, S3, S5 | AM-TIME-001 |
| AI-034 | AT-004, AT-006, AT-010, AT-012 | S2, S3, S4, S5 | AM-CONTINUITY-001 |
| AI-035 | AT-002, AT-010, AT-011 | S1, S2, S3 | AM-CONTEXT-001 |
| AI-036 | AT-006, AT-012 | S2, S4 | AM-GROUP-001, AM-CRUD-001, AM-BRIDGE-001 |
| AI-037 | AT-001, AT-010 | S1, S3, S5 | AM-HISTORY-001 |
| AI-038 | AT-010, AT-011 | S3, S5 | AM-EVIDENCE-001 |
| AI-039 | AT-004, AT-011 | S2, S3, S5 | AM-HOLD-001, AM-HOLD-SET-001 |
| AI-040 | AT-007, AT-012, AT-014 | S4, S6 | AM-SAVEPOINT-001 |
| AI-041 | AT-010, AT-011, AT-014 | S3, S5, S6 | AM-READ-001 |
| AI-042 | AT-009, AT-010, AT-011 | S3, S5 | AM-CURSOR-001 |
| AI-043 | AT-002, AT-009, AT-010 | S1, S2, S3, S5 | AM-TARGET-001, AM-HMAC-001 |
| AI-044 | AT-004, AT-010, AT-011 | S1, S2, S3, S5 | AM-IDENTITY-001, AM-HOLD-SET-001 |
| AI-045 | AT-012, AT-014 | S4, S6 | AM-CRUD-BOUNDARY-001, AM-BRIDGE-001 |
| AI-046 | AT-008, AT-010, AT-011, AT-014 | S2, S3, S6 | AM-DENIAL-001 |
| AI-047 | AT-017 | S0 | AM-TRACE-001 |
| AI-048 | AT-002, AT-010, AT-011 | S1, S2, S3, S5 | AM-CODE-001 |
| AI-049 | AT-001, AT-002, AT-016 | S1, S6 | AM-SEMANTICS-001, AM-CODEC-001 |
| AI-050 | AT-010, AT-013 | S3, S5 | AM-TIME-METADATA-001 |
| AI-051 | AT-004, AT-011, AT-013 | S2, S3, S5 | AM-HOLD-PROJECTION-001, AM-HOLD-SET-001 |
| AI-052 | AT-002, AT-007, AT-010, AT-011 | S1, S2, S3, S5 | AM-CANCEL-CAUSE-001 |
| AI-053 | AT-004, AT-006, AT-007, AT-010, AT-012 | S1, S2, S3, S4, S5 | AM-SCOPED-CONTINUITY-001 |
| AI-054 | AT-001, AT-013, AT-016 | S1, S2, S5, S6 | AM-CATALOG-ADMIN-001 |
| AI-055 | AT-002, AT-010, AT-011 | S1, S3, S5 | AM-CODE-001 |
| AI-056 | AT-001, AT-002, AT-016 | S1, S6 | AM-CODEC-001, AM-SEMANTICS-001 |
| AI-057 | AT-017 | S0 | AM-TRACE-001 |
| AI-058 | AT-003, AT-010, AT-011 | S2, S3, S5 | AM-EVIDENCE-DIGEST-001 |
| AI-059 | AT-002, AT-009, AT-010, AT-016 | S1, S3, S6 | AM-QUERY-001, AM-EXACT-READ-001, AM-API-001 |
| AI-060 | AT-009, AT-013 | S3, S5 | AM-SNAPSHOT-001, AM-CURSOR-001 |
| AI-061 | AT-010, AT-016 | S3, S6 | AM-COMPARE-001, AM-EVIDENCE-DIGEST-001 |
| AI-062 | AT-004, AT-012, AT-013 | S2, S4, S5 | AM-TERMINAL-001, AM-CONTINUITY-001 |
| AI-063 | AT-002, AT-003, AT-009, AT-010, AT-013 | S1, S2, S3, S5 | AM-QUERY-001, AM-SNAPSHOT-001, AM-EVIDENCE-DIGEST-001 |
| AI-064 | AT-004, AT-010, AT-012, AT-013 | S1, S2, S3, S4, S5 | AM-BASELINE-001 |
| AI-065 | AT-005, AT-008, AT-018 | S3, S6 | AM-ATTEMPT-START-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001 |
| AI-066 | AT-003, AT-013, AT-018 | S3, S5 | AM-SIGN-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001 |
| AI-067 | AT-004, AT-005, AT-018 | S3, S5 | AM-IDEMP-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001 |
| AI-068 | AT-005, AT-008, AT-018 | S3, S5 | AM-OUTCOME-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-TX-001 |
| AI-069 | AT-001, AT-003, AT-004, AT-007, AT-018 | S3, S5 | AM-IDENTITY-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-CATALOG-001, AM-ATTEMPT-RESUME-001 |
| AI-070 | AT-003, AT-007, AT-013, AT-018 | S3, S5 | AM-SIGN-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-CATALOG-001 |
| AI-071 | AT-002, AT-007, AT-018 | S3, S5 | AM-BOUNDS-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-BOUND-001 |
| AI-072 | AT-002, AT-003, AT-018 | S3, S5 | AM-ALLOW-001, AM-PROTECT-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-PRIVACY-001 |
| AI-073 | AT-011, AT-013, AT-018 | S3, S5 | AM-HOLD-001, AM-RETENTION-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-RETENTION-001 |
| AI-074 | AT-007, AT-014, AT-018 | S3, S6 | AM-TOPO-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-RESUME-001 |
| AI-075 | AT-014, AT-016, AT-018 | S3, S6 | AM-TOPO-001, AM-ATTEMPT-PRIVACY-001 |
| AI-076 | AT-002, AT-009, AT-010, AT-018 | S3, S5, S6 | AM-TARGET-001, AM-QUERY-001, AM-ATTEMPT-WIRE-001 |
| AI-077 | AT-002, AT-009, AT-010, AT-013 | S3, S5 | AM-ALLOW-001, AM-HMAC-001, AM-QUERY-001 |
| AI-078 | AT-002, AT-009, AT-010, AT-013 | S3, S5 | AM-HMAC-001, AM-BOUNDS-001, AM-QUERY-001, AM-SNAPSHOT-001 |
| AI-079 | AT-004, AT-010, AT-012, AT-013 | S3, S4, S5 | AM-HISTORY-001, AM-COMPARE-001, AM-TERMINAL-001 |
| AI-080 | AT-010, AT-013 | S3, S5 | AM-HISTORY-001, AM-BOUNDS-001, AM-CONTINUITY-001 |
| AI-081 | AT-009, AT-010, AT-018 | S3, S5 | AM-QUERY-001, AM-SNAPSHOT-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-WIRE-001, AM-ATTEMPT-BOUND-001 |
| AI-082 | AT-007, AT-013, AT-018 | S3, S5 | AM-BACKING-001, AM-CONFORM-001, AM-ATTEMPT-STATE-001, AM-ATTEMPT-CATALOG-001 |
| AI-083 | AT-007, AT-010, AT-014, AT-018 | S3, S5, S6 | AM-EVIDENCE-DIGEST-001, AM-ATTEMPT-CATALOG-001, AM-ATTEMPT-PRIVACY-001, AM-ATTEMPT-RESUME-001 |

### MutantTrace

The Related ATs column is review context, not multiple executable kill claims.
Exactly one primary AT is encoded by the `TestATNNN...` reserved symbol and is the
only `AM killed-by AT` fact materialized; AM-TRACE-001 explicitly uses AT-017.
Other AT relations remain requirement coverage claims with their own coverage
reservations. This prevents one unit reservation from pretending to execute an
integration obligation in another package.

| Mutant | Defect | Positive neighbor | Related ATs | Owner | Reserved test symbol |
|---|---|---|---|---|---|
| AM-NAME-001 | Accept malformed, overlong, duplicate, or confusable semantic identity. | A bounded lowercase dot-segment identity remains byte-exact. | AT-001, AT-002 | S1 | `TestAT001KillsMalformedIdentityMutant` |
| AM-CAT-001 | Skip lineage, predecessor, stable-meaning, retention, or activation-CAS validation. | A direct valid child manifest installs and activates once. | AT-001, AT-010, AT-011, AT-013 | S1 | `TestAT001KillsCatalogLineageMutant` |
| AM-CODE-001 | Accept an empty/malformed/undeclared code, use the current instead of record-era set, substitute event/control/reason domains, or let code membership grant value visibility; alternatively claim the kernel can infer semantic sensitivity from valid grammar. | One declared action-scoped label passes grammar/membership while narrative stays in a classified field; a sensitive-looking valid canary remains an explicit author-review violation, not a fabricated kernel rejection. | AT-001, AT-002, AT-010, AT-011 | S1 | `TestAT002KillsUndeclaredCodeMutant` |
| AM-SEMANTICS-001 | Omit policy/codec fingerprints, trust engine self-report, use function identity, reuse a version after golden drift, or claim finite calls prove an arbitrary future custom collaborator. | Rebuilt declarations with distinct function values and identical declared golden meaning have the same fingerprint while custom state-switch behavior remains an explicit trusted-boundary violation. | AT-001, AT-002, AT-010, AT-016 | S1 | `TestAT001KillsSemanticReuseMutant` |
| AM-CODEC-001 | Normalize, alias, change during an executed fixture, omit or skip a golden, alter accepted/rejected bytes, accept an unknown/noncanonical form, fail to bound returned bytes before later work, or claim the kernel bounds termination/complexity inside a custom engine. | Every built-in and retained custom codec reproduces its complete executed fixture transcript; a post-construction switch or unbounded callback remains an explicit trusted-engine violation and a legal returned value is bounded before later work. | AT-001, AT-002, AT-016 | S1 | `TestAT002KillsCodecCanonicalityMutant` |
| AM-ALLOW-001 | Capture or return an undeclared/secret/unprojected field or context fact. | A declared admitted field survives with its exact policy. | AT-002, AT-010, AT-012, AT-015 | S1 | `TestAT002KillsAllowlistLeakMutant` |
| AM-PROTECT-001 | Protect after store visibility, omit one envelope/AAD coordinate, reuse a crypto protocol, or mishandle AES key/nonce rotation. | A valid randomized envelope verifies at its original coordinate. | AT-003, AT-004 | S2 | `TestAT003KillsProtectionBoundaryMutant` |
| AM-HMAC-001 | Accept short/duplicate keys, caller-authored profile, wrong domain, missing retained token, or aliased key bytes. | One copied active key emits the specified 32-byte vector in its own domain. | AT-003, AT-004, AT-009, AT-010, AT-016 | S2 | `TestAT003KillsHMACProfileMutant` |
| AM-CONTEXT-001 | Order provenance enums, resolve twice, union member policies, or leak caller context downstream. | One allowed provenance and declared fact is frozen once and copied. | AT-002, AT-007, AT-010, AT-011 | S1 | `TestAT002KillsContextAuthorityMutant` |
| AM-EXEC-001 | Use an executor registry, expose/swap execution, accept foreign authority, or cross simultaneous transactions. | One frame-bound execution uses its captured source transaction only. | AT-005, AT-007, AT-012 | S2 | `TestAT007KillsExecutionSubstitutionMutant` |
| AM-ENTITY-DRAFT-001 | Make EntityDraft assignable to manual Draft, add a standalone entity append/head door, or accept caller-authored entity state outside Guard.Run. | A manual event records while an EntityDraft can cross only the internal supervised bridge. | AT-007, AT-012 | S2 | `TestAT007KillsStandaloneEntityDraftMutant` |
| AM-APPEND-001 | Split one revision across writes, append more than once, or retain a partial failed result. | One valid standalone or grouped request appends its complete immutable revision exactly once. | AT-004, AT-005, AT-007, AT-013 | S2 | `TestAT004KillsAtomicAppendMutant` |
| AM-IDEMP-001 | Compare randomized envelopes or CAS-derived AppendIntentDigest for keyed logical replay, skip the atomic post-lookup recheck, or ignore semantic/same-revision-content disagreement. | Equal stable logical retry replays the original envelope and persisted hold result across changed state; unequal semantics or same-revision intent conflict. | AT-003, AT-004, AT-005 | S2 | `TestAT004KillsIdempotencyMutant` |
| AM-OUTCOME-001 | Collapse not-written/unconfirmed/cancelled/backend states or retry transaction-bound evidence. | A classified definite failure and committed visibility remain distinct. | AT-005, AT-008, AT-011 | S2 | `TestAT005KillsOutcomeCollapseMutant` |
| AM-GROUP-001 | Let scheduling define order, clear poison, ignore reservation, or append after a swallowed failure. | A valid bounded group appends once in canonical order. | AT-006, AT-012 | S2 | `TestAT006KillsGroupPoisonMutant` |
| AM-ERROR-001 | Leak a collaborator cause through rendering/traversal or recover a non-observer panic. | A classified safe wrapper retains only the explicit diagnostic door. | AT-008, AT-012 | S2 | `TestAT008KillsErrorLeakMutant` |
| AM-READ-001 | Replay a decision, widen a grant, trust a hostile row, or disclose a forbidden projection. | A current origin-bound grant returns only its exact admitted projection. | AT-009, AT-010, AT-011 | S3 | `TestAT010KillsReadAuthorityMutant` |
| AM-CURSOR-001 | Omit requester/query/grant/expiry/catalog/backing binding, conflate cursor/fence protocols, or widen on continuation. | A same-request continuation tiles a quiescent result exactly. | AT-009, AT-010, AT-011 | S3 | `TestAT009KillsCursorBindingMutant` |
| AM-HISTORY-001 | Use current meaning for an old row, skip chain/integrity validation, turn a later-added never-captured field into Absent, or guess any unknown projection state. | An authenticated retained manifest and chain reconstruct a declared known value while a not-yet-captured historical field remains Unobserved until its first assignment. | AT-001, AT-010 | S3 | `TestAT010KillsHistoricalAuthenticityMutant` |
| AM-EVIDENCE-001 | Release prepared data before independently committed audit-of-audit evidence. | A committed standalone evidence receipt releases the prepared result. | AT-010, AT-011 | S3 | `TestAT010KillsPrematureDisclosureMutant` |
| AM-CORRECT-001 | Use an unkeyed/wrong proposal domain, omit or reorder any exhaustive proposal preimage member, include the digest in its own cycle, evaluate the digester twice, retain mutable input, admit a current redacted proposal or redacted exact original record-era target, skip record-era target reveal/token comparison, or substitute draft/original after authorization. | One correction-eligible declaration, canonical one-call exact proposal digest, and authenticated matching original target append a linked assertion. | AT-003, AT-011 | S3 | `TestAT011KillsCorrectionSubstitutionMutant` |
| AM-HOLD-001 | Model hold as a boolean, resurrect membership, or advance epoch on replay/no-op. | One serialized effective transition updates exactly one membership and projection. | AT-004, AT-011 | S3 | `TestAT011KillsHoldStateMutant` |
| AM-HOLD-SET-001 | Derive hold-set identity from raw or rotating commitments, preserve caller order or aliases, use a wrong domain, or admit zero or duplicate stable identities. | Stable HoldIdentity values are copied, byte-sorted, deduplicated, and hashed once in the fixed hold-set domain; alias rotation and input permutation preserve the same nonempty set identity. | AT-003, AT-004, AT-011, AT-013 | S2 | `TestAT004KillsHoldSetIdentityMutant` |
| AM-LIFE-001 | Authorize any item/first label, omit summary dimensions, turn an empty/exact/cross-declaration selector into a wildcard, or trust an uninspected candidate/fence. | A full-summary grant with exact or explicit same-declaration wildcard coordinates yields the same bounded fenced cohort. | AT-007, AT-011 | S3 | `TestAT011KillsWholeRevisionMutant` |
| AM-POSTGRES-001 | Mutate immutable rows, skip readiness/schema fingerprint, retry pool statements, or accept wrong transaction. | A verified schema performs one append through the proven executor. | AT-013 | S5 | `TestAT013KillsPostgresDurabilityMutant` |
| AM-CRUD-BOUNDARY-001 | Expose the Gate/private receiver, permit an unsecured sibling entry or outer-faults order, let unsupported verbs reach callbacks/I/O, or substitute the frozen mutation payload. | The sealed terminal reaches its receiver only through Gate and executes one exact authorized payload. | AT-012, AT-014 | S4 | `TestAT012KillsSealedCRUDBoundaryMutant` |
| AM-SAVEPOINT-001 | Admit caller savepoint, stage inside faults savepoint, ignore cleanup failure, or wrap audit from outside. | One inner faults statement savepoint finalizes before root audit capture. | AT-007, AT-012, AT-014 | S4 | `TestAT012KillsSavepointScopeMutant` |
| AM-OBS-001 | Let telemetry affect authority/result, emit protected dimensions, report arbitrary/estimated work, repeat cumulative counts as deltas, count rejected work, leave inapplicable fields nonzero, or wrap a work counter. | Exact bounded per-phase work deltas remain non-identifying, and a disabled, lying, or failing observer leaves evidence unchanged. | AT-014 | S6 | `TestAT014KillsObserverAuthorityMutant` |
| AM-TOPO-001 | Import optional extensions into the kernel, tunnel exact effects, or start lifecycle in construction. | An explicit composition root wires dependency-light packages without side effects. | AT-007, AT-014, AT-016 | S6 | `TestAT014KillsTopologyMutant` |
| AM-SCOPE-001 | Drop namespace, concatenate ambiguously, compare/tokenize only the local reference, or admit protected/redacted scope into history or lifecycle authorization. | Two equal local values in distinct namespaces remain distinct end to end, and every present operational scope is equality-searchable. | AT-002, AT-009, AT-011 | S3 | `TestAT009KillsScopedReferenceMutant` |
| AM-BACKING-001 | Serialize local Backing, omit/change durable BackingID, or confuse a sibling with a distinct store. | Restart under the same BackingID and LogID resumes; a different ID refuses. | AT-007, AT-009, AT-011, AT-013, AT-016 | S2 | `TestAT007KillsBackingIdentityMutant` |
| AM-TARGET-001 | Drop event Resource/Action, expose store token to authority, omit retained token, or scan an unsearchable target. | A typed searchable target derives exact retained coordinates and returns only its event. | AT-009, AT-010 | S3 | `TestAT009KillsEventTargetMutant` |
| AM-CONFORM-001 | Certify without a usable store, required hook, section verdict, or killed defect. | A real factory reports at least one certified section and every executed verdict. | AT-016, AT-017 | S6 | `TestAT016KillsVacuousConformanceMutant` |
| AM-BOUNDS-001 | Check an observable byte/count/time or reconstruction-work limit after allocation, decode, or store work, or pass a custom-callback result onward before bounding it. | The maximum legal neighbor succeeds and one-over refuses before attributable downstream work or becomes an explicit bounded-unknown projection; opaque callback-internal work is the declared trusted exception. | AT-002, AT-007, AT-009, AT-010, AT-011 | S1 | `TestAT002KillsLateBoundMutant` |
| AM-TIME-001 | Treat occurred/observed/recorded/position as interchangeable or break chain order on equal/regressing time. | One verified predecessor chain resolves an unambiguous boundary. | AT-010, AT-013 | S3 | `TestAT010KillsTemporalOrderMutant` |
| AM-OWNER-001 | Retain/alias caller buffers, discover through Next, or close a caller-owned resource. | Copied exact-outer values remain unchanged and caller lifecycle remains owned. | AT-002, AT-007, AT-010, AT-011, AT-016 | S2 | `TestAT007KillsOwnershipMutant` |
| AM-IDENTITY-001 | Persist raw private identity, use the wrong domain, omit aliases, or split one rotated identity. | Complete domain-separated aliases resolve atomically to one stable object. | AT-004, AT-007, AT-010, AT-011 | S2 | `TestAT004KillsPrivateIdentityMutant` |
| AM-CRUD-001 | Infer victims from counts/stale input, drop narrowing, misclassify Save/delete, or advance a no-op. | One authorized locked mutation records the exact persisted before/after state. | AT-012, AT-013 | S4 | `TestAT012KillsCRUDTruthMutant` |
| AM-RETENTION-001 | Use a wrong/raw retention-basis domain or divergent adapter fold, omit CatalogRef/ObservedAt/rule input, apply current policy to old evidence, ignore a hold, let caller/store select or alter AsOf, read Control clock more than once, or drift time across continuation/purge. | The one canonical RetentionBasisDigest helper, record-era observed-time rule, one normalized Control-selected AsOf echoed exactly end to end, and active holds determine eligibility. | AT-001, AT-003, AT-010, AT-011 | S3 | `TestAT011KillsRetentionMutant` |
| AM-DENIAL-001 | Amplify denial, collapse states, recover panic, or expose cause/receipt/retry data. | A bounded admitted attempt remains denied and reports its exact safe state. | AT-008, AT-010, AT-011, AT-014 | S3 | `TestAT010KillsDenialAdmissionMutant` |
| AM-SIGN-001 | Sign semantic data only, omit envelope/description, accept malformed key/seal, or conflate backend with invalidity. | A record-era seal authenticates the exact integrity digest under its description. | AT-003, AT-008, AT-010, AT-011 | S2 | `TestAT003KillsIntegritySealMutant` |
| AM-SINGLE-001 | Evaluate an extractor/option/predicate twice or retain its mutable input. | One invocation is copied and reused throughout the operation. | AT-002, AT-012 | S1 | `TestAT002KillsSingleEvaluationMutant` |
| AM-CONSEQUENCE-001 | Treat BestEffort as authoritative, swallow its visible error, or admit it for entity/control evidence. | A declared manual BestEffort event remains visibly non-authoritative. | AT-015 | S2 | `TestAT015KillsConsequenceMutant` |
| AM-CONTINUITY-001 | Derive entity identity/head from time/randomness, skip predecessor CAS, or advance on replay. | One stable chain advances once from its exact previous leaf. | AT-004, AT-006, AT-010, AT-012 | S2 | `TestAT004KillsEntityContinuityMutant` |
| AM-API-001 | Advertise a missing/uncompilable helper, perform constructor I/O, or expose two configuration authorities. | An external package compiles and constructs the exact documented no-I/O surface. | AT-016 | S6 | `TestAT016KillsAdoptionSurfaceMutant` |
| AM-CONCURRENCY-001 | Assume injected serialization, race on returned buffers, or publish mutable store identity. | Overlapped calls preserve copied stable results under race detection. | AT-002, AT-006, AT-007, AT-010, AT-011, AT-013 | S2 | `TestAT007KillsConcurrencyMutant` |
| AM-TIME-METADATA-001 | Let unsigned RecordedAt/position select AtObservedTime, select a descendant through an excluded later predecessor, trust a mismatched/unverified asserted head, omit a suffix relative to it, reorder equal observations, or claim old-valid-head rollback detection without an external anchor. | Rewriting unsigned metadata leaves one authenticated head-to-genesis causally closed boundary unchanged, while a straddling regression is explicit ambiguity. | AT-010, AT-013 | S3 | `TestAT010KillsUnsignedTimeBoundaryMutant` |
| AM-HOLD-MATTER-001 | Omit matter policy/fingerprint/AAD/summary, admit an unsafe mode, or expose raw matter. | One protected declared matter round-trips only under authorized record-era reveal. | AT-002, AT-011 | S1 | `TestAT002KillsHoldMatterPolicyMutant` |
| AM-HOLD-PROJECTION-001 | Trust unsigned projection, use a generic/missing/wrong-command or cross-HoldID typed wire arm, omit/reorder proof, accept stale CAS, substitute prior/result state, or accept a zero/foreign HoldRequestDigest. | One origin-authorized request, authenticated chain, unique signed hold arm, and byte-identical conditional append produce one full-state CAS and exact returned projection. | AT-004, AT-011, AT-013 | S3 | `TestAT011KillsHoldProjectionMutant` |
| AM-CANCEL-CAUSE-001 | Delegate caller-controlled context.Cause, retain a resolver value/error after post-return cancellation, or leak a direct/wrapped/joined cause through CauseOf or a downstream seam. | Cancellation immediately after resolution discards resolver output and exposes only the normalized sentinel with no diagnostic cause while deadline/Done/Err still work. | AT-002, AT-007, AT-010, AT-011 | S1 | `TestAT002KillsCancellationCauseMutant` |
| AM-SCOPED-CONTINUITY-001 | Omit scope presence/namespace/local reference from subject commitment, reservation, alias, genesis, head, or predecessor validation. | Equal local IDs under absent, tenant, and region scopes advance distinct stable chains. | AT-004, AT-006, AT-007, AT-010, AT-012 | S2 | `TestAT007KillsScopedContinuityMutant` |
| AM-EVIDENCE-DIGEST-001 | Use raw SHA-256/wrong domain, omit any typed field including role/query class, incoming/emitted continuation or origin, leave selection/fence unexplained, conflate request/grant/result/hold/denial inputs, introduce a fence/result cycle, or claim finite tests prove a custom digester stays keyed. | One exhaustive acyclic domain-separated keyed digest stays stable without exposing a low-entropy subject, while raw-SHA/state-switch fakes are explicit trusted-boundary violations. | AT-003, AT-010, AT-011 | S3 | `TestAT003KillsEvidenceDigestMutant` |
| AM-CATALOG-ADMIN-001 | Install/activate without a persisted external change reference, skip a direct child, acknowledge then omit/alter/fabricate mutation readback, bootstrap in runtime New, or let the runtime concrete type expose CatalogAdmin. | An independently authorized deployment installs and CAS-activates each child, verifies exact manifests/mutations after reopen, then hands serving code a distinct runtime concrete handle. | AT-001, AT-013, AT-016 | S2 | `TestAT001KillsCatalogAdminBoundaryMutant` |
| AM-BRIDGE-001 | Forge, copy, reuse, cross a Recorder, or bypass the one-use internal auditcrud Spec/Guard/Batch. | The sealed Gate-to-receiver path consumes one exact guard while an ordinary application package cannot import or name the internal bridge authority. | AT-012, AT-014 | S4 | `TestAT012KillsOneUseBridgeMutant` |
| AM-QUERY-001 | Default to broad disclosure, accept a class-illegal filter, drop or rewrite projection/time/direction/change/code/typed-selector meaning between request, grant, store, cursor, and evidence, accept a foreign event index, or expose a generic property predicate. | One sealed typed query normalizes its safe defaults and preserves every legal member exactly or monotonically narrower through the complete read path. | AT-002, AT-009, AT-010, AT-016 | S3 | `TestAT009KillsQueryAlgebraMutant` |
| AM-SNAPSHOT-001 | Page a live result, omit cohort identity/expiry/sort/progress/ceilings, admit a backdated concurrent append, recreate or mutate a snapshot, or reset cumulative limits per page. | One bounded origin cohort tiles exactly once in either direction while concurrent commits remain outside it. | AT-009, AT-013 | S3 | `TestAT009KillsStableSnapshotMutant` |
| AM-COMPARE-001 | Authorize two independent walks, reverse or cross subject chains, reset the shared budget, guess equality for an unknown endpoint, or omit a boundary/contributor/status from result evidence. | Two ordered boundaries on one authenticated chain yield exact Known/Absent comparison or an explicit Indeterminate under one combined budget. | AT-010, AT-016 | S3 | `TestAT010KillsComparisonKnowledgeMutant` |
| AM-TERMINAL-001 | Treat a hard-deleted subject as genesis, drop terminal state from head CAS, rebind a retained alias, reject soft-delete restore, or advance terminal replay twice. | One hard delete closes its scoped chain permanently while soft delete remains restorable and exact replay is inert. | AT-004, AT-012, AT-013 | S2 | `TestAT004KillsTerminalIdentityMutant` |
| AM-BASELINE-001 | Let a delta establish genesis, label a legacy first touch as creation, omit one reconstructable after-state field, capture an unchanged nonreconstructable field, accept a partial/racing baseline, guess pre-baseline absence, or append the same baseline twice on retry. | One changed first touch of a preexisting row creates exactly one truthful full-state anchor; its exact time boundary is known, earlier time is ambiguous, and subsequent mutations are deltas. | AT-004, AT-010, AT-012, AT-013 | S2 | `TestAT004KillsLegacyBaselineMutant` |
| AM-EXACT-READ-001 | Let unknown row contents supply their own access scope, omit requested exact ceilings, authorize any-item/partial-revision content, skip containing-envelope verification, or release a foreign ordinal/catalog/log. | One exact target is authenticated wholly and returned only inside the caller-requested and sealed resource/action/classification ceilings. | AT-010, AT-013 | S3 | `TestAT010KillsExactReadMutant` |
| AM-ATTEMPT-START-001 | Enter protected work before a Committed Inserted Started transition, return a handle on replay or failed begin, invoke the callback on replay, or let an uncertain absence authorize another start. | One Required Run commits and inserts Started before one callback while an identical retained replay invokes zero callbacks and returns no handle. | AT-005, AT-008, AT-014, AT-018 | S3 | `TestAT018KillsAttemptStartMutant` |
| AM-ATTEMPT-STATE-001 | Skip or weaken the attempt expected-state CAS, admit an illegal phase edge or second terminal, trust a mutable projection, conflate Open and Uncertain, or expose ResultingState for an uncommitted settlement. | One legal transition advances the verified projection exactly once and only a Committed Inserted or Replayed result exposes that authenticated resulting state. | AT-004, AT-007, AT-008, AT-013, AT-018 | S3 | `TestAT018KillsAttemptStateMutant` |
| AM-ATTEMPT-TX-001 | Claim a terminal from cancellation, panic, returned error, or unknown commit, split a grouped terminal from its mutation, replace reconcilable transactional uncertainty, or retry through another authority. | One proven transaction commits its admitted mutation and terminal together while a lost commit remains unresolved under its exact ReconcileKey. | AT-005, AT-006, AT-007, AT-012, AT-013, AT-018 | S3 | `TestAT018KillsAttemptTransactionMutant` |
| AM-ATTEMPT-WIRE-001 | Omit, reorder, or substitute a transition, prior leaf, sequence, target-presence arm, owner, scope, fingerprint, code, digest input, seal, or containing revision and still accept the chain. | One complete signed start-to-head chain recomputes byte-exactly with its target presence and every continuity coordinate intact. | AT-003, AT-010, AT-013, AT-018 | S3 | `TestAT018KillsAttemptWireMutant` |
| AM-ATTEMPT-BOUND-001 | Check attempt value, checkpoint, transition, selector, page, chain, or wire ceilings after work, reset cumulative limits, consume reserved settlement capacity, or synthesize status from a partial page. | The largest legal bounded chain settles and verifies while one-over stops before downstream work and transition pages never become status. | AT-002, AT-007, AT-009, AT-010, AT-018 | S3 | `TestAT018KillsAttemptBoundMutant` |
| AM-ATTEMPT-PRIVACY-001 | Capture an undeclared dump, header, credential, raw error, stack, ambient context, or payload map, expose internal aliases, or omit requester Purpose and Role from attempt authority or evidence. | One declared typed protected attempt value and one exact nonzero Purpose and Role survive authorization without exposing raw operational secrets or storage aliases. | AT-002, AT-003, AT-010, AT-014, AT-018 | S3 | `TestAT018KillsAttemptPrivacyMutant` |
| AM-ATTEMPT-RETENTION-001 | Purge an Open or Uncertain chain, split a terminal cohort or co-revision, use less than the maximum member cutoff, ignore any member hold, or select the current counter head without an authenticated supersession or reset anchor. | One closed unheld unpinned cohort becomes eligible all-or-none at its maximum record-era cutoff while an otherwise equal held cohort remains excluded. | AT-011, AT-013, AT-018 | S3 | `TestAT018KillsAttemptRetentionMutant` |
| AM-ATTEMPT-CATALOG-001 | Reuse changed attempt meaning under one policy fingerprint, merge per-chain and global-counter authority, trust a malformed Unsettled certificate, activate an incompatible policy while unsettled work exists, or accept a stale catalog/counter proof. | One compatible policy and replay fingerprint preserves retained replay while an exact zero authenticated Unsettled certificate permits its direct-child activation. | AT-001, AT-007, AT-013, AT-016, AT-018 | S3 | `TestAT018KillsAttemptCatalogMutant` |
| AM-ATTEMPT-RESUME-001 | Resume, resolve, or abandon through a copied, stale, foreign, partially verified, unauthorized, expired, or intent-substituted handle, grant, Purpose, Role, origin, or chain. | One restarted same-origin caller with the exact Purpose and Role verifies the complete chain and performs its one authorized continuation under CAS. | AT-007, AT-010, AT-013, AT-018 | S3 | `TestAT018KillsAttemptResumeMutant` |
| AM-TRACE-001 | Omit, duplicate, range-collapse, orphan, mistype, or disconnect an AU, requirement, AT, section, mutant, positive neighbor, reservation, package, or symbol; delete or change any AU/AH/AE/AI/AT/AM/PN semantic body, a closed subgraph, or highest ID; accept zero/skipped/wrong-package/duplicate pass events; or leave an active coverage/mutant/positive test unexecuted. | The independent anchor matches the normalized graph and every canonical semantic record, while the section-aware executable observes the structural pass plus exactly one non-skipped pass for every active declared package/test/role tuple. | AT-017 | S0 | `TestAuditTraceCheckpointRejectsMissingTest` |

## 5. Stop conditions

Stop and amend the plan rather than approximate when:

- exact transaction identity cannot be proved before a covered mutation;
- a mutation adapter would need to bypass a security layer to capture state;
- an option, callback, or event extractor would be evaluated twice;
- before/after state is inferred from a count or stale caller value;
- protected plaintext would cross the store/observer boundary;
- a reader cannot revalidate every returned row against its sealed grant;
- PostgreSQL retry behavior makes one append potentially execute more than once
  without an idempotent reconciliation path;
- a new package imports another optional extension to save application wiring;
- a test can pass after deleting the behavior it claims to prove;
- concurrent work modifies the same hunk and intent cannot be preserved safely.
