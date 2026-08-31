package jobs

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"time"
)

type PlacementMode uint8

const MaximumCollapseDelay = MaximumMaxElapsed

const (
	PlacementRegular PlacementMode = iota + 1
	PlacementOnce
	PlacementCollapse
	PlacementDebounce
)

func (m PlacementMode) Valid() bool {
	return m >= PlacementRegular && m <= PlacementDebounce
}

func (m PlacementMode) String() string {
	switch m {
	case PlacementRegular:
		return "regular"
	case PlacementOnce:
		return "once"
	case PlacementCollapse:
		return "collapse"
	case PlacementDebounce:
		return "debounce"
	default:
		return "unknown"
	}
}

type PayloadDigest struct {
	identity CodecID
	version  SchemaVersion
	value    [32]byte
}

func NewPayloadDigest(identity CodecID, version SchemaVersion, value [32]byte) (PayloadDigest, error) {
	if identity.IsZero() || version.IsZero() || value == [32]byte{} {
		return PayloadDigest{}, invalid("payload digest")
	}
	return PayloadDigest{identity: identity, version: version, value: value}, nil
}

func (d PayloadDigest) Identity() CodecID      { return d.identity }
func (d PayloadDigest) Version() SchemaVersion { return d.version }
func (d PayloadDigest) Bytes() [32]byte        { return d.value }
func (d PayloadDigest) IsZero() bool           { return d.identity.IsZero() }
func (d PayloadDigest) String() string         { return "[job payload digest]" }
func (d PayloadDigest) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
func (d PayloadDigest) valid() bool {
	return !d.identity.IsZero() && !d.version.IsZero() && d.value != [32]byte{}
}

type WireDigest struct{ value [32]byte }

func WireDigestFromBytes(value [32]byte) (WireDigest, error) {
	if value == [32]byte{} {
		return WireDigest{}, invalid("wire digest is zero")
	}
	return WireDigest{value: value}, nil
}

func (d WireDigest) Bytes() [32]byte { return d.value }
func (d WireDigest) IsZero() bool    { return d.value == [32]byte{} }
func (d WireDigest) String() string  { return "[job wire digest]" }
func (d WireDigest) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
func (d WireDigest) valid() bool { return !d.IsZero() }

type PolicySnapshot struct {
	queue                 QueueName
	priority              int
	attemptTimeout        time.Duration
	progressTimeout       time.Duration
	maxElapsed            time.Duration
	retryLimit            RetryLimit
	handlerDeferralLimit  HandlerDeferralLimit
	deliveryDeferralLimit DeliveryDeferralLimit
	backoff               BackoffPolicy
	retention             time.Duration
	intentRetention       time.Duration
	payload               PayloadLimit
	durability            DurabilityRequirement
	trace                 TracePolicy
}

func NewPolicySnapshot(policy Policy) (PolicySnapshot, error) {
	if err := validatePolicy(policy); err != nil {
		return PolicySnapshot{}, err
	}
	retryLimit, err := NewRetryLimit(uint16(policy.MaxRetries))
	if err != nil {
		return PolicySnapshot{}, err
	}
	handlerDeferralLimit, err := NewHandlerDeferralLimit(uint16(policy.MaxHandlerDeferrals))
	if err != nil {
		return PolicySnapshot{}, err
	}
	deliveryDeferralLimit, err := NewDeliveryDeferralLimit(uint16(policy.MaxDeliveryDeferrals))
	if err != nil {
		return PolicySnapshot{}, err
	}
	return PolicySnapshot{
		queue:                 policy.Queue,
		priority:              policy.Priority,
		attemptTimeout:        policy.AttemptTimeout,
		progressTimeout:       policy.ProgressTimeout,
		maxElapsed:            policy.MaxElapsed,
		retryLimit:            retryLimit,
		handlerDeferralLimit:  handlerDeferralLimit,
		deliveryDeferralLimit: deliveryDeferralLimit,
		backoff:               policy.Backoff,
		retention:             policy.Retention,
		intentRetention:       policy.IntentRetention,
		payload:               policy.Payload,
		durability:            policy.Durability,
		trace:                 policy.Trace,
	}, nil
}

func (s PolicySnapshot) Queue() QueueName                             { return s.queue }
func (s PolicySnapshot) Priority() int                                { return s.priority }
func (s PolicySnapshot) DefaultPriority() int                         { return s.priority }
func (s PolicySnapshot) AttemptTimeout() time.Duration                { return s.attemptTimeout }
func (s PolicySnapshot) ProgressTimeout() time.Duration               { return s.progressTimeout }
func (s PolicySnapshot) MaxElapsed() time.Duration                    { return s.maxElapsed }
func (s PolicySnapshot) RetryLimit() RetryLimit                       { return s.retryLimit }
func (s PolicySnapshot) HandlerDeferralLimit() HandlerDeferralLimit   { return s.handlerDeferralLimit }
func (s PolicySnapshot) DeliveryDeferralLimit() DeliveryDeferralLimit { return s.deliveryDeferralLimit }
func (s PolicySnapshot) Backoff() BackoffPolicy                       { return s.backoff }
func (s PolicySnapshot) Retention() time.Duration                     { return s.retention }
func (s PolicySnapshot) IntentRetention() time.Duration               { return s.intentRetention }
func (s PolicySnapshot) Payload() PayloadLimit                        { return s.payload }
func (s PolicySnapshot) Durability() DurabilityRequirement            { return s.durability }
func (s PolicySnapshot) Trace() TracePolicy                           { return s.trace }
func (s PolicySnapshot) String() string                               { return "[job policy snapshot]" }
func (s PolicySnapshot) Format(state fmt.State, _ rune)               { _, _ = fmt.Fprint(state, s.String()) }
func (s PolicySnapshot) valid() bool {
	return s.queue.valid() && s.priority > 0 && s.priority <= MaximumPriority &&
		s.attemptTimeout > 0 && s.attemptTimeout <= MaximumAttemptTimeout &&
		s.progressTimeout >= 0 && s.progressTimeout <= s.attemptTimeout &&
		s.maxElapsed >= s.attemptTimeout && s.maxElapsed <= MaximumMaxElapsed &&
		s.retryLimit.valid() && s.handlerDeferralLimit.valid() && s.deliveryDeferralLimit.valid() &&
		validateBackoff(s.backoff, s.maxElapsed) == nil &&
		s.retention > 0 && s.retention <= MaxRetention &&
		s.intentRetention >= s.retention && s.intentRetention <= MaxRetention &&
		validatePayloadLimit(s.payload) == nil && s.durability.valid() && s.trace.valid()
}

type PlacementSpec struct {
	Namespace     Namespace
	Partition     PartitionKey
	Candidate     InvocationID
	Definition    Name
	Queue         QueueName
	Mode          PlacementMode
	Payload       EncodedPayload
	PayloadDigest PayloadDigest
	WireDigest    WireDigest
	IntentDigests IntentDigests
	LegacyIntent  LegacyIntent
	Priority      int
	Delay         time.Duration
	MaxDelay      time.Duration
	StartBefore   time.Time
	Policy        PolicySnapshot
	Context       DurableContext
}

func (PlacementSpec) String() string { return "[job placement spec]" }
func (s PlacementSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}

type Placement struct {
	namespace     Namespace
	partition     PartitionKey
	candidate     InvocationID
	definition    Name
	queue         QueueName
	mode          PlacementMode
	payload       EncodedPayload
	payloadDigest PayloadDigest
	wireDigest    WireDigest
	intentDigests IntentDigests
	legacyIntent  LegacyIntent
	priority      int
	delay         time.Duration
	maxDelay      time.Duration
	startBefore   time.Time
	policy        PolicySnapshot
	context       DurableContext
}

func NewPlacement(spec PlacementSpec) (Placement, error) {
	if !spec.Namespace.valid() || !spec.Partition.validFor(spec.Namespace) || !spec.Candidate.valid() || !spec.Definition.valid() || !spec.Queue.valid() || !spec.Mode.Valid() || spec.Payload.IsZero() || !spec.WireDigest.valid() || !spec.IntentDigests.validFor(spec.Namespace, spec.Partition, spec.Definition) || !spec.Policy.valid() || !spec.Context.validFor(spec.Namespace, spec.Partition, spec.Definition, spec.Policy.Trace()) || spec.Queue != spec.Policy.queue || spec.Priority <= 0 || spec.Priority > MaximumPriority {
		return Placement{}, invalid("placement identity or policy")
	}
	purpose := spec.IntentDigests.Current().Purpose()
	if spec.Mode == PlacementRegular && purpose != IntentRegular || spec.Mode == PlacementOnce && purpose != IntentOnce || (spec.Mode == PlacementCollapse || spec.Mode == PlacementDebounce) && purpose != IntentCollapse {
		return Placement{}, invalid("placement intent purpose")
	}
	if spec.Mode == PlacementRegular && !regularIntentDigestsMatch(spec.Namespace, spec.Partition, spec.Definition, spec.Candidate, spec.IntentDigests) {
		return Placement{}, invalid("placement regular intent")
	}
	if !spec.LegacyIntent.IsZero() && (spec.Mode == PlacementRegular || !spec.LegacyIntent.valid() || !producerIntentDigestsMatch(spec.Namespace, spec.Partition, spec.Definition, spec.LegacyIntent, spec.IntentDigests)) {
		return Placement{}, invalid("placement legacy intent")
	}
	if spec.Mode == PlacementOnce && !spec.PayloadDigest.valid() || spec.Mode != PlacementOnce && !spec.PayloadDigest.IsZero() && !spec.PayloadDigest.valid() {
		return Placement{}, invalid("placement payload identity")
	}
	if spec.Payload.encodedLength() > spec.Policy.payload.MaxBytes {
		return Placement{}, tooLarge("placement payload")
	}
	if digestWirePayload(spec.Payload) != spec.WireDigest {
		return Placement{}, invalid("placement wire digest")
	}
	if spec.Delay < 0 || spec.Delay > MaxRetention {
		return Placement{}, invalid("placement delay")
	}
	switch spec.Mode {
	case PlacementRegular, PlacementOnce, PlacementCollapse:
		if spec.MaxDelay != 0 {
			return Placement{}, invalid("placement maximum delay")
		}
	case PlacementDebounce:
		if spec.Delay <= 0 || spec.MaxDelay < spec.Delay || spec.MaxDelay > MaximumCollapseDelay {
			return Placement{}, invalid("placement debounce window")
		}
	}
	startBefore, err := optionalTime(spec.StartBefore, "placement start deadline")
	if err != nil {
		return Placement{}, err
	}
	payload, err := NewEncodedPayload(spec.Payload.Codec(), spec.Payload.Version(), spec.Payload.encodedBytes())
	if err != nil {
		return Placement{}, err
	}
	return Placement{
		namespace:     spec.Namespace,
		partition:     spec.Partition,
		candidate:     spec.Candidate,
		definition:    spec.Definition,
		queue:         spec.Queue,
		mode:          spec.Mode,
		payload:       payload,
		payloadDigest: spec.PayloadDigest,
		wireDigest:    spec.WireDigest,
		intentDigests: spec.IntentDigests,
		legacyIntent:  spec.LegacyIntent,
		priority:      spec.Priority,
		delay:         spec.Delay,
		maxDelay:      spec.MaxDelay,
		startBefore:   startBefore,
		policy:        spec.Policy,
		context:       spec.Context,
	}, nil
}

func (p Placement) Namespace() Namespace         { return p.namespace }
func (p Placement) Partition() PartitionKey      { return p.partition }
func (p Placement) Candidate() InvocationID      { return p.candidate }
func (p Placement) Definition() Name             { return p.definition }
func (p Placement) Queue() QueueName             { return p.queue }
func (p Placement) Mode() PlacementMode          { return p.mode }
func (p Placement) Payload() EncodedPayload      { return cloneEncodedPayload(p.payload) }
func (p Placement) PayloadDigest() PayloadDigest { return p.payloadDigest }
func (p Placement) WireDigest() WireDigest       { return p.wireDigest }
func (p Placement) IntentDigests() IntentDigests { return p.intentDigests }
func (p Placement) IntentDigest() IntentDigest   { return p.intentDigests.Current().Digest() }
func (p Placement) LegacyIntent() (LegacyIntent, bool) {
	return p.legacyIntent, !p.legacyIntent.IsZero()
}
func (p Placement) Priority() int           { return p.priority }
func (p Placement) Delay() time.Duration    { return p.delay }
func (p Placement) MaxDelay() time.Duration { return p.maxDelay }
func (p Placement) StartBefore() time.Time  { return p.startBefore }
func (p Placement) Policy() PolicySnapshot  { return p.policy }
func (p Placement) Context() DurableContext { return p.context }
func (p Placement) IsZero() bool            { return p.candidate.IsZero() }
func (p Placement) String() string          { return "[job placement]" }
func (p Placement) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, p.String())
}

func cloneEncodedPayload(payload EncodedPayload) EncodedPayload {
	return EncodedPayload{codec: payload.codec, version: payload.version, data: bytes.Clone(payload.data)}
}

func digestWirePayload(payload EncodedPayload) WireDigest {
	digest := sha256.New()
	writePlacementString(digest, "frostgrove.jobs.wire.v1")
	writePlacementString(digest, payload.Codec().Value())
	writePlacementUint(digest, uint64(payload.Version()))
	writePlacementBytes(digest, payload.encodedBytes())
	var value [32]byte
	copy(value[:], digest.Sum(nil))
	return WireDigest{value: value}
}

func digestEncodedPayload(identity CodecID, version SchemaVersion, encoded []byte) PayloadDigest {
	digest := sha256.New()
	writePlacementString(digest, "frostgrove.jobs.payload.semantic.v1")
	writePlacementString(digest, identity.Value())
	writePlacementUint(digest, uint64(version))
	writePlacementBytes(digest, encoded)
	var value [32]byte
	copy(value[:], digest.Sum(nil))
	return PayloadDigest{identity: identity, version: version, value: value}
}

func digestProducerIntents(plan IntentDigestPlan, namespace Namespace, partition PartitionKey, definition Name, purpose IntentPurpose, intent ProducerIntent) (IntentDigests, error) {
	if !plan.valid() || !namespace.valid() || !partition.validFor(namespace) || !definition.valid() || purpose != IntentOnce && purpose != IntentCollapse || !intent.valid() {
		return IntentDigests{}, invalid("producer intent digest input")
	}
	return digestIntentKeys(plan, intentScopeBinding(namespace, partition, definition, purpose), purpose, func(revision DigestRevision) IntentDigest {
		return digestIntentValue(revision, purpose, namespace, partition, definition, []byte(intent.value))
	})
}

func digestRegularIntents(plan IntentDigestPlan, namespace Namespace, partition PartitionKey, definition Name, candidate InvocationID) (IntentDigests, error) {
	if !plan.valid() || !namespace.valid() || !partition.validFor(namespace) || !definition.valid() || !candidate.valid() {
		return IntentDigests{}, invalid("regular intent digest input")
	}
	value := candidate.Bytes()
	return digestIntentKeys(plan, intentScopeBinding(namespace, partition, definition, IntentRegular), IntentRegular, func(revision DigestRevision) IntentDigest {
		return digestIntentValue(revision, IntentRegular, namespace, partition, definition, value[:])
	})
}

func digestIntentKeys(plan IntentDigestPlan, scope IntentScopeBinding, purpose IntentPurpose, build func(DigestRevision) IntentDigest) (IntentDigests, error) {
	revisions := plan.Revisions()
	current, err := NewIntentKey(scope, revisions[0], purpose, build(revisions[0]))
	if err != nil {
		return IntentDigests{}, err
	}
	if len(revisions) == 1 {
		return NewIntentDigests(current)
	}
	compatibility, err := NewIntentKey(scope, revisions[1], purpose, build(revisions[1]))
	if err != nil {
		return IntentDigests{}, err
	}
	return NewIntentDigests(current, compatibility)
}

func digestIntentValue(revision DigestRevision, purpose IntentPurpose, namespace Namespace, partition PartitionKey, definition Name, value []byte) IntentDigest {
	digest := sha256.New()
	writePlacementString(digest, "frostgrove.jobs.intent")
	writePlacementUint(digest, uint64(revision))
	writePlacementUint(digest, uint64(purpose))
	writePlacementString(digest, namespace.Application().Value())
	writePlacementString(digest, namespace.Environment().Value())
	writePlacementString(digest, definition.Value())
	writePlacementUint(digest, uint64(partition.Revision()))
	if partition.Global() {
		writePlacementString(digest, "global")
	} else {
		writePlacementString(digest, "tenant")
	}
	partitionDigest := partition.Digest().Bytes()
	writePlacementBytes(digest, partitionDigest[:])
	writePlacementBytes(digest, value)
	var result [IntentDigestBytes]byte
	copy(result[:], digest.Sum(nil))
	return IntentDigest{value: result}
}

func regularIntentDigestsMatch(namespace Namespace, partition PartitionKey, definition Name, candidate InvocationID, intents IntentDigests) bool {
	if !candidate.valid() || !intents.validFor(namespace, partition, definition) || intents.Current().Purpose() != IntentRegular {
		return false
	}
	value := candidate.Bytes()
	for _, intent := range intents.ReservationKeys() {
		if intent.Digest() != digestIntentValue(intent.Revision(), IntentRegular, namespace, partition, definition, value[:]) {
			return false
		}
	}
	return true
}

func producerIntentDigestsMatch(namespace Namespace, partition PartitionKey, definition Name, legacy LegacyIntent, intents IntentDigests) bool {
	if !legacy.valid() || !intents.validFor(namespace, partition, definition) || intents.Current().Purpose() == IntentRegular {
		return false
	}
	for _, intent := range intents.ReservationKeys() {
		if intent.Digest() != digestIntentValue(intent.Revision(), intent.Purpose(), namespace, partition, definition, []byte(legacy.value)) {
			return false
		}
	}
	return true
}

func writePlacementString(digest hash.Hash, value string) {
	writePlacementBytes(digest, []byte(value))
}

func writePlacementBytes(digest hash.Hash, value []byte) {
	writePlacementUint(digest, uint64(len(value)))
	_, _ = digest.Write(value)
}

func writePlacementUint(digest hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = digest.Write(encoded[:])
}
