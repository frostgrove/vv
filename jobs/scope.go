package jobs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxPartitionBytes   = 512
	MaxIntentDigestKeys = 2
	IntentScopeBytes    = 32
	BackendIDBytes      = 32
)

type DigestRevision uint16

const (
	DigestRevision1 DigestRevision = iota + 1
	DigestRevision2
)

func (r DigestRevision) Valid() bool { return r == DigestRevision1 || r == DigestRevision2 }
func (r DigestRevision) String() string {
	switch r {
	case DigestRevision1:
		return "v1"
	case DigestRevision2:
		return "v2"
	default:
		return "unknown"
	}
}

type IntentDigestPlan struct {
	current       DigestRevision
	compatibility DigestRevision
	legacy        bool
}

func NewIntentDigestPlan(current DigestRevision, compatibility ...DigestRevision) (IntentDigestPlan, error) {
	if !current.Valid() || len(compatibility) > MaxIntentDigestKeys-1 {
		return IntentDigestPlan{}, invalid("intent digest plan")
	}
	plan := IntentDigestPlan{current: current}
	if len(compatibility) == 1 {
		if !compatibility[0].Valid() || compatibility[0] == current {
			return IntentDigestPlan{}, invalid("intent digest compatibility revision")
		}
		plan.compatibility = compatibility[0]
	}
	return plan, nil
}

func CurrentIntentDigestPlan() IntentDigestPlan {
	return IntentDigestPlan{current: DigestRevision1}
}

func WithLegacyIntentCompatibility(plan IntentDigestPlan) (IntentDigestPlan, error) {
	if !plan.valid() || plan.legacy {
		return IntentDigestPlan{}, invalid("legacy intent compatibility")
	}
	plan.legacy = true
	return plan, nil
}

func (p IntentDigestPlan) Current() DigestRevision   { return p.current }
func (p IntentDigestPlan) LegacyCompatibility() bool { return p.legacy }
func (p IntentDigestPlan) Revisions() []DigestRevision {
	if p.compatibility == 0 {
		return []DigestRevision{p.current}
	}
	return []DigestRevision{p.current, p.compatibility}
}
func (p IntentDigestPlan) IsZero() bool { return p.current == 0 }
func (p IntentDigestPlan) valid() bool {
	if !p.current.Valid() {
		return false
	}
	return p.compatibility == 0 || p.compatibility.Valid() && p.compatibility != p.current
}

type Namespace struct {
	application Name
	environment Name
	digest      [32]byte
}

func NewNamespace(application, environment Name) (Namespace, error) {
	if !application.valid() || !environment.valid() {
		return Namespace{}, invalid("namespace application or environment")
	}
	digest := sha256.New()
	writePlacementString(digest, "frostgrove.jobs.namespace.v1")
	writePlacementString(digest, application.Value())
	writePlacementString(digest, environment.Value())
	var value [32]byte
	copy(value[:], digest.Sum(nil))
	return Namespace{application: application, environment: environment, digest: value}, nil
}

func NamespaceOf(application, environment string) (Namespace, error) {
	applicationName, err := ParseName(application)
	if err != nil {
		return Namespace{}, fmt.Errorf("namespace application: %w", err)
	}
	environmentName, err := ParseName(environment)
	if err != nil {
		return Namespace{}, fmt.Errorf("namespace environment: %w", err)
	}
	return NewNamespace(applicationName, environmentName)
}

type PartitionMode uint8

const (
	PartitionGlobal PartitionMode = iota
	PartitionTenantRequired
)

func (m PartitionMode) Valid() bool {
	return m == PartitionGlobal || m == PartitionTenantRequired
}

func (m PartitionMode) String() string {
	if m == PartitionTenantRequired {
		return "tenant_required"
	}
	if m == PartitionGlobal {
		return "global"
	}
	return "unknown"
}

type TenantPartitioner interface {
	Partition(context.Context) (ProducerPartition, error)
}

type TenantPartitionerFunc func(context.Context) (ProducerPartition, error)

func (f TenantPartitionerFunc) Partition(ctx context.Context) (ProducerPartition, error) {
	return f(ctx)
}

func (n Namespace) Application() Name { return n.application }
func (n Namespace) Environment() Name { return n.environment }
func (n Namespace) Digest() [32]byte  { return n.digest }
func (n Namespace) IsZero() bool      { return n.digest == [32]byte{} }
func (n Namespace) String() string    { return "[job namespace]" }
func (n Namespace) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, n.String())
}
func (Namespace) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: namespace cannot be serialized", ErrUnsupported)
}
func (n Namespace) valid() bool {
	if !n.application.valid() || !n.environment.valid() || n.IsZero() {
		return false
	}
	parsed, err := NamespaceOf(n.application.Value(), n.environment.Value())
	return err == nil && parsed.digest == n.digest
}

type ProducerPartition struct {
	value    string
	rejected bool
}

func Partition(raw string) ProducerPartition {
	if len(raw) > MaxPartitionBytes {
		return ProducerPartition{rejected: true}
	}
	return ProducerPartition{value: strings.Clone(raw)}
}

func ParsePartition(raw string) (ProducerPartition, error) {
	if len(raw) > MaxPartitionBytes {
		return ProducerPartition{}, tooLarge("producer partition")
	}
	partition := Partition(raw)
	if !partition.valid() {
		return ProducerPartition{}, invalid("producer partition")
	}
	return partition, nil
}

func (p ProducerPartition) IsZero() bool   { return p.value == "" && !p.rejected }
func (p ProducerPartition) String() string { return "[job producer partition]" }
func (p ProducerPartition) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, p.String())
}
func (p ProducerPartition) LogValue() slog.Value { return slog.StringValue(p.String()) }
func (ProducerPartition) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: producer partition cannot be serialized", ErrUnsupported)
}
func (p ProducerPartition) valid() bool {
	if p.rejected || p.value == "" || len(p.value) > MaxPartitionBytes || strings.TrimSpace(p.value) != p.value || !utf8.ValidString(p.value) {
		return false
	}
	for _, value := range p.value {
		if unicode.IsControl(value) || value == '\u2028' || value == '\u2029' {
			return false
		}
	}
	return true
}

type PartitionDigest struct{ value [32]byte }

func PartitionDigestFromBytes(value [32]byte) (PartitionDigest, error) {
	if value == [32]byte{} {
		return PartitionDigest{}, invalid("partition digest is zero")
	}
	return PartitionDigest{value: value}, nil
}

func (d PartitionDigest) Bytes() [32]byte { return d.value }
func (d PartitionDigest) IsZero() bool    { return d.value == [32]byte{} }
func (d PartitionDigest) String() string  { return "[job partition digest]" }
func (d PartitionDigest) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
func (d PartitionDigest) valid() bool { return !d.IsZero() }

type PartitionKey struct {
	namespace PartitionNamespaceBinding
	revision  DigestRevision
	digest    PartitionDigest
	global    bool
}

type PartitionNamespaceBinding struct{ value [32]byte }

func PartitionNamespaceBindingFromBytes(value [32]byte) (PartitionNamespaceBinding, error) {
	if value == [32]byte{} {
		return PartitionNamespaceBinding{}, invalid("partition namespace binding is zero")
	}
	return PartitionNamespaceBinding{value: value}, nil
}

func (b PartitionNamespaceBinding) Bytes() [32]byte { return b.value }
func (b PartitionNamespaceBinding) IsZero() bool    { return b.value == [32]byte{} }
func (b PartitionNamespaceBinding) String() string  { return "[job partition namespace binding]" }
func (b PartitionNamespaceBinding) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, b.String())
}
func (PartitionNamespaceBinding) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: partition namespace binding cannot be serialized", ErrUnsupported)
}
func (b PartitionNamespaceBinding) valid() bool { return !b.IsZero() }

func RestorePartitionKey(namespace Namespace, binding PartitionNamespaceBinding, revision DigestRevision, digest PartitionDigest, global bool) (PartitionKey, error) {
	if !namespace.valid() || !binding.valid() || binding != partitionNamespaceBinding(namespace) || !revision.Valid() || !digest.valid() {
		return PartitionKey{}, invalid("partition key")
	}
	return PartitionKey{namespace: binding, revision: revision, digest: digest, global: global}, nil
}

func (k PartitionKey) NamespaceBinding() PartitionNamespaceBinding { return k.namespace }
func (k PartitionKey) Revision() DigestRevision                    { return k.revision }
func (k PartitionKey) Digest() PartitionDigest                     { return k.digest }
func (k PartitionKey) Global() bool                                { return k.global }
func (k PartitionKey) IsZero() bool                                { return k.revision == 0 }
func (k PartitionKey) String() string                              { return "[job partition key]" }
func (k PartitionKey) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, k.String())
}
func (PartitionKey) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: partition key cannot be serialized", ErrUnsupported)
}
func (k PartitionKey) valid() bool {
	return k.namespace.valid() && k.revision.Valid() && k.digest.valid()
}
func (k PartitionKey) validFor(namespace Namespace) bool {
	return k.valid() && namespace.valid() && k.namespace == partitionNamespaceBinding(namespace)
}

func partitionNamespaceBinding(namespace Namespace) PartitionNamespaceBinding {
	return PartitionNamespaceBinding{value: namespace.digest}
}

func partitionKey(namespace Namespace, partition ProducerPartition) PartitionKey {
	digest := sha256.New()
	writePlacementString(digest, "frostgrove.jobs.partition.v1")
	value := namespace.Digest()
	writePlacementBytes(digest, value[:])
	global := partition.IsZero()
	if global {
		writePlacementString(digest, "global")
	} else {
		writePlacementString(digest, "partitioned")
		writePlacementString(digest, partition.value)
	}
	var encoded [32]byte
	copy(encoded[:], digest.Sum(nil))
	return PartitionKey{namespace: partitionNamespaceBinding(namespace), revision: DigestRevision1, digest: PartitionDigest{value: encoded}, global: global}
}

type IntentPurpose uint8

const (
	IntentRegular IntentPurpose = iota + 1
	IntentOnce
	IntentCollapse
)

func (p IntentPurpose) Valid() bool {
	return p >= IntentRegular && p <= IntentCollapse
}

func (p IntentPurpose) String() string {
	switch p {
	case IntentRegular:
		return "regular"
	case IntentOnce:
		return "once"
	case IntentCollapse:
		return "collapse"
	default:
		return "unknown"
	}
}

type IntentKey struct {
	scope    IntentScopeBinding
	revision DigestRevision
	purpose  IntentPurpose
	digest   IntentDigest
}

type IntentScopeBinding struct{ value [IntentScopeBytes]byte }

func IntentScopeBindingFromBytes(value [IntentScopeBytes]byte) (IntentScopeBinding, error) {
	if value == [IntentScopeBytes]byte{} {
		return IntentScopeBinding{}, invalid("intent scope binding is zero")
	}
	return IntentScopeBinding{value: value}, nil
}

func (b IntentScopeBinding) Bytes() [IntentScopeBytes]byte { return b.value }
func (b IntentScopeBinding) IsZero() bool                  { return b.value == [IntentScopeBytes]byte{} }
func (b IntentScopeBinding) String() string                { return "[job intent scope binding]" }
func (b IntentScopeBinding) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, b.String())
}
func (IntentScopeBinding) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: intent scope binding cannot be serialized", ErrUnsupported)
}
func (b IntentScopeBinding) valid() bool { return !b.IsZero() }

func NewIntentKey(scope IntentScopeBinding, revision DigestRevision, purpose IntentPurpose, digest IntentDigest) (IntentKey, error) {
	if !scope.valid() || !revision.Valid() || !purpose.Valid() || !digest.valid() {
		return IntentKey{}, invalid("intent key")
	}
	return IntentKey{scope: scope, revision: revision, purpose: purpose, digest: digest}, nil
}

func (k IntentKey) Scope() IntentScopeBinding { return k.scope }
func (k IntentKey) Revision() DigestRevision  { return k.revision }
func (k IntentKey) Purpose() IntentPurpose    { return k.purpose }
func (k IntentKey) Digest() IntentDigest      { return k.digest }
func (k IntentKey) IsZero() bool              { return k.revision == 0 }
func (k IntentKey) String() string            { return "[job intent key]" }
func (k IntentKey) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, k.String())
}
func (k IntentKey) valid() bool {
	return k.scope.valid() && k.revision.Valid() && k.purpose.Valid() && k.digest.valid()

}
func (k IntentKey) validFor(namespace Namespace, partition PartitionKey, definition Name) bool {
	return k.valid() && k.scope == intentScopeBinding(namespace, partition, definition, k.purpose)
}

type IntentDigests struct {
	current       IntentKey
	compatibility IntentKey
}

func NewIntentDigests(current IntentKey, compatibility ...IntentKey) (IntentDigests, error) {
	if !current.valid() || len(compatibility) > MaxIntentDigestKeys-1 {
		return IntentDigests{}, invalid("intent digests")
	}
	digests := IntentDigests{current: current}
	if len(compatibility) == 1 {
		candidate := compatibility[0]
		if !candidate.valid() || candidate.scope != current.scope || candidate.purpose != current.purpose || candidate.revision == current.revision || candidate.digest == current.digest {
			return IntentDigests{}, invalid("intent compatibility digest")
		}
		digests.compatibility = candidate
	}
	return digests, nil
}

func (d IntentDigests) Current() IntentKey { return d.current }
func (d IntentDigests) ReadCandidates() []IntentKey {
	if d.compatibility.IsZero() {
		return []IntentKey{d.current}
	}
	return []IntentKey{d.current, d.compatibility}
}
func (d IntentDigests) ReservationKeys() []IntentKey { return d.ReadCandidates() }
func (d IntentDigests) IsZero() bool                 { return d.current.IsZero() }
func (d IntentDigests) String() string               { return "[job intent digests]" }
func (d IntentDigests) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
func (d IntentDigests) valid() bool {
	if !d.current.valid() {
		return false
	}
	return d.compatibility.IsZero() || d.compatibility.valid() && d.compatibility.scope == d.current.scope && d.compatibility.purpose == d.current.purpose && d.compatibility.revision != d.current.revision && d.compatibility.digest != d.current.digest
}
func (d IntentDigests) validFor(namespace Namespace, partition PartitionKey, definition Name) bool {
	if !d.valid() || !d.current.validFor(namespace, partition, definition) {
		return false
	}
	return d.compatibility.IsZero() || d.compatibility.validFor(namespace, partition, definition)
}

func intentScopeBinding(namespace Namespace, partition PartitionKey, definition Name, purpose IntentPurpose) IntentScopeBinding {
	if !namespace.valid() || !partition.validFor(namespace) || !definition.valid() || !purpose.Valid() {
		return IntentScopeBinding{}
	}
	digest := sha256.New()
	writePlacementString(digest, "frostgrove.jobs.intent.scope.v1")
	namespaceDigest := namespace.Digest()
	writePlacementBytes(digest, namespaceDigest[:])
	writePlacementUint(digest, uint64(partition.Revision()))
	partitionDigest := partition.Digest().Bytes()
	writePlacementBytes(digest, partitionDigest[:])
	if partition.Global() {
		writePlacementUint(digest, 1)
	} else {
		writePlacementUint(digest, 0)
	}
	writePlacementString(digest, definition.Value())
	writePlacementUint(digest, uint64(purpose))
	var value [IntentScopeBytes]byte
	copy(value[:], digest.Sum(nil))
	return IntentScopeBinding{value: value}
}

type BackendID struct{ value [BackendIDBytes]byte }

func BackendIDFromBytes(value [BackendIDBytes]byte) (BackendID, error) {
	if value == [BackendIDBytes]byte{} {
		return BackendID{}, invalid("backend id is zero")
	}
	return BackendID{value: value}, nil
}

func (id BackendID) Bytes() [BackendIDBytes]byte { return id.value }
func (id BackendID) IsZero() bool                { return id.value == [BackendIDBytes]byte{} }
func (id BackendID) String() string              { return "[job backend id]" }
func (id BackendID) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, id.String())
}
func (id BackendID) valid() bool { return !id.IsZero() }
