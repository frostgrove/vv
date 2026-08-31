package jobs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ContextScope uint8

const (
	ContextSystem ContextScope = iota + 1
	ContextTenant
)

func (s ContextScope) Valid() bool { return s == ContextSystem || s == ContextTenant }
func (s ContextScope) String() string {
	switch s {
	case ContextSystem:
		return "system"
	case ContextTenant:
		return "tenant"
	default:
		return "unknown"
	}
}

type IdentityEpoch uint64

func NewIdentityEpoch(value uint64) (IdentityEpoch, error) {
	if value == 0 {
		return 0, invalid("identity epoch")
	}
	return IdentityEpoch(value), nil
}

func (e IdentityEpoch) Value() uint64 { return uint64(e) }
func (e IdentityEpoch) IsZero() bool  { return e == 0 }
func (e IdentityEpoch) valid() bool   { return !e.IsZero() }

type IdentityProvenance struct{ value string }

func ParseIdentityProvenance(raw string) (IdentityProvenance, error) {
	value, err := parseRegistryName(raw, MaxIdentityProvenanceBytes, "identity provenance")
	if err != nil {
		return IdentityProvenance{}, err
	}
	return IdentityProvenance{value: value}, nil
}

func (p IdentityProvenance) Value() string  { return p.value }
func (p IdentityProvenance) IsZero() bool   { return p.value == "" }
func (p IdentityProvenance) String() string { return p.value }
func (p IdentityProvenance) valid() bool {
	return validRegistryName(p.value, MaxIdentityProvenanceBytes)
}

type ProducerActor struct {
	value    string
	rejected bool
}

func Actor(raw string) ProducerActor {
	if len(raw) > MaxActorIdentityBytes {
		return ProducerActor{rejected: true}
	}
	return ProducerActor{value: strings.Clone(raw)}
}

func ParseActor(raw string) (ProducerActor, error) {
	if len(raw) > MaxActorIdentityBytes {
		return ProducerActor{}, tooLarge("producer actor")
	}
	actor := Actor(raw)
	if !actor.valid() {
		return ProducerActor{}, invalid("producer actor")
	}
	return actor, nil
}

func (a ProducerActor) IsZero() bool   { return a.value == "" && !a.rejected }
func (a ProducerActor) String() string { return "[job producer actor]" }
func (a ProducerActor) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, a.String())
}
func (a ProducerActor) LogValue() slog.Value { return slog.StringValue(a.String()) }
func (ProducerActor) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: producer actor cannot be serialized", ErrUnsupported)
}
func (a ProducerActor) valid() bool {
	return !a.rejected && validProtectedText(a.value, MaxActorIdentityBytes)
}

type ProtectedIdentityToken struct {
	value    []byte
	rejected bool
}

func NewProtectedIdentityToken(value []byte) (ProtectedIdentityToken, error) {
	if len(value) == 0 {
		return ProtectedIdentityToken{}, invalid("protected identity token")
	}
	if len(value) > MaxIdentityTokenBytes {
		return ProtectedIdentityToken{}, tooLarge("protected identity token")
	}
	return ProtectedIdentityToken{value: append([]byte(nil), value...)}, nil
}

func (t ProtectedIdentityToken) Bytes() []byte  { return append([]byte(nil), t.value...) }
func (t ProtectedIdentityToken) IsZero() bool   { return len(t.value) == 0 && !t.rejected }
func (t ProtectedIdentityToken) String() string { return "[job protected identity token]" }
func (t ProtectedIdentityToken) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, t.String())
}
func (t ProtectedIdentityToken) LogValue() slog.Value { return slog.StringValue(t.String()) }
func (ProtectedIdentityToken) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: protected identity token cannot be serialized", ErrUnsupported)
}
func (t ProtectedIdentityToken) valid() bool {
	return !t.rejected && len(t.value) > 0 && len(t.value) <= MaxIdentityTokenBytes
}

type CorrelationKey struct{ value string }

func ParseCorrelationKey(raw string) (CorrelationKey, error) {
	value, err := parseRegistryName(raw, MaxCorrelationKeyBytes, "correlation key")
	if err != nil {
		return CorrelationKey{}, err
	}
	return CorrelationKey{value: value}, nil
}

func (k CorrelationKey) Value() string  { return k.value }
func (k CorrelationKey) IsZero() bool   { return k.value == "" }
func (k CorrelationKey) String() string { return k.value }
func (k CorrelationKey) valid() bool {
	return validRegistryName(k.value, MaxCorrelationKeyBytes)
}

type CorrelationField struct {
	key   CorrelationKey
	value string
}

func NewCorrelationField(key CorrelationKey, value string) (CorrelationField, error) {
	if !key.valid() {
		return CorrelationField{}, invalid("correlation key")
	}
	if len(value) > MaxCorrelationValueBytes {
		return CorrelationField{}, tooLarge("correlation value")
	}
	if !validProtectedText(value, MaxCorrelationValueBytes) {
		return CorrelationField{}, invalid("correlation value")
	}
	return CorrelationField{key: key, value: strings.Clone(value)}, nil
}

func (f CorrelationField) Key() CorrelationKey { return f.key }
func (f CorrelationField) Value() string       { return strings.Clone(f.value) }
func (f CorrelationField) IsZero() bool        { return f.key.IsZero() }
func (f CorrelationField) String() string      { return "[job correlation field]" }
func (f CorrelationField) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, f.String())
}
func (CorrelationField) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: correlation field cannot be serialized", ErrUnsupported)
}
func (f CorrelationField) valid() bool {
	return f.key.valid() && validProtectedText(f.value, MaxCorrelationValueBytes)
}

type TracePolicy struct {
	keys   [MaxCorrelationFields]CorrelationKey
	length uint8
}

func NewTracePolicy(keys ...CorrelationKey) (TracePolicy, error) {
	if len(keys) > MaxCorrelationFields {
		return TracePolicy{}, tooLarge("trace correlation allowlist")
	}
	values := append([]CorrelationKey(nil), keys...)
	sort.Slice(values, func(left, right int) bool { return values[left].value < values[right].value })
	var policy TracePolicy
	for index, key := range values {
		if !key.valid() || index > 0 && key == values[index-1] {
			return TracePolicy{}, invalid("trace correlation allowlist")
		}
		policy.keys[index] = key
	}
	policy.length = uint8(len(values))
	return policy, nil
}

func (p TracePolicy) Keys() []CorrelationKey {
	return append([]CorrelationKey(nil), p.keys[:p.length]...)
}
func (p TracePolicy) String() string { return "[job trace policy]" }
func (p TracePolicy) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, p.String())
}
func (p TracePolicy) valid() bool {
	if p.length > MaxCorrelationFields {
		return false
	}
	for index := range int(p.length) {
		if !p.keys[index].valid() || index > 0 && p.keys[index].value <= p.keys[index-1].value {
			return false
		}
	}
	for index := int(p.length); index < len(p.keys); index++ {
		if !p.keys[index].IsZero() {
			return false
		}
	}
	return true
}
func (p TracePolicy) allows(key CorrelationKey) bool {
	index := sort.Search(int(p.length), func(index int) bool { return p.keys[index].value >= key.value })
	return index < int(p.length) && p.keys[index] == key
}

type TraceCarrierSpec struct {
	TraceParent  string
	TraceState   string
	Correlations []CorrelationField
}

func (TraceCarrierSpec) String() string { return "[job trace carrier specification]" }
func (s TraceCarrierSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}

type UntrustedTraceCarrier struct {
	traceParent  string
	traceState   string
	correlations [MaxCorrelationFields]CorrelationField
	length       uint8
}

func NewUntrustedTraceCarrier(spec TraceCarrierSpec) (UntrustedTraceCarrier, error) {
	if len(spec.TraceParent) > MaxTraceParentBytes || len(spec.TraceState) > MaxTraceStateBytes || len(spec.Correlations) > MaxCorrelationFields {
		return UntrustedTraceCarrier{}, tooLarge("trace carrier")
	}
	if !validTraceParent(spec.TraceParent) || !validTraceState(spec.TraceState, spec.TraceParent != "") {
		return UntrustedTraceCarrier{}, invalid("trace carrier")
	}
	fields := append([]CorrelationField(nil), spec.Correlations...)
	sort.Slice(fields, func(left, right int) bool { return fields[left].key.value < fields[right].key.value })
	charge := len(spec.TraceParent) + len(spec.TraceState)
	var carrier UntrustedTraceCarrier
	for index, field := range fields {
		if !field.valid() || index > 0 && field.key == fields[index-1].key {
			return UntrustedTraceCarrier{}, invalid("trace correlations")
		}
		charge += len(field.key.value) + len(field.value) + 2
		if charge > MaxTraceCarrierBytes {
			return UntrustedTraceCarrier{}, tooLarge("trace carrier")
		}
		carrier.correlations[index] = CorrelationField{key: field.key, value: strings.Clone(field.value)}
	}
	carrier.traceParent = strings.Clone(spec.TraceParent)
	carrier.traceState = strings.Clone(spec.TraceState)
	carrier.length = uint8(len(fields))
	return carrier, nil
}

func (c UntrustedTraceCarrier) TraceParent() string { return strings.Clone(c.traceParent) }
func (c UntrustedTraceCarrier) TraceState() string  { return strings.Clone(c.traceState) }
func (c UntrustedTraceCarrier) Correlations() []CorrelationField {
	return append([]CorrelationField(nil), c.correlations[:c.length]...)
}
func (c UntrustedTraceCarrier) IsZero() bool {
	return c.traceParent == "" && c.traceState == "" && c.length == 0
}
func (c UntrustedTraceCarrier) String() string { return "[untrusted job trace carrier]" }
func (c UntrustedTraceCarrier) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, c.String())
}
func (c UntrustedTraceCarrier) LogValue() slog.Value { return slog.StringValue(c.String()) }
func (UntrustedTraceCarrier) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: trace carrier cannot be serialized", ErrUnsupported)
}
func (c UntrustedTraceCarrier) valid() bool {
	if c.length > MaxCorrelationFields || !validTraceParent(c.traceParent) || !validTraceState(c.traceState, c.traceParent != "") {
		return false
	}
	charge := len(c.traceParent) + len(c.traceState)
	for index := range int(c.length) {
		field := c.correlations[index]
		if !field.valid() || index > 0 && field.key.value <= c.correlations[index-1].key.value {
			return false
		}
		charge += len(field.key.value) + len(field.value) + 2
		if charge > MaxTraceCarrierBytes {
			return false
		}
	}
	for index := int(c.length); index < len(c.correlations); index++ {
		if !c.correlations[index].IsZero() {
			return false
		}
	}
	return true
}
func (c UntrustedTraceCarrier) validFor(policy TracePolicy) bool {
	if !policy.valid() || !c.valid() {
		return false
	}
	for index := range int(c.length) {
		if !policy.allows(c.correlations[index].key) {
			return false
		}
	}
	return true
}

type ContextCaptureSpec struct {
	Tenant     ProducerPartition
	Actor      ProducerActor
	Token      ProtectedIdentityToken
	Provenance IdentityProvenance
	Epoch      IdentityEpoch
	Trace      UntrustedTraceCarrier
}

func (ContextCaptureSpec) String() string { return "[job context capture specification]" }
func (s ContextCaptureSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}

type ContextCapture struct {
	tenant     ProducerPartition
	actor      ProducerActor
	token      ProtectedIdentityToken
	provenance IdentityProvenance
	epoch      IdentityEpoch
	trace      UntrustedTraceCarrier
}

func NewContextCapture(spec ContextCaptureSpec) (ContextCapture, error) {
	if !spec.Tenant.IsZero() && !spec.Tenant.valid() || !spec.Actor.IsZero() && !spec.Actor.valid() || !spec.Token.IsZero() && !spec.Token.valid() || !spec.Provenance.valid() || !spec.Epoch.valid() || !spec.Trace.valid() {
		return ContextCapture{}, invalid("context capture")
	}
	return ContextCapture{
		tenant:     ProducerPartition{value: strings.Clone(spec.Tenant.value)},
		actor:      ProducerActor{value: strings.Clone(spec.Actor.value)},
		token:      ProtectedIdentityToken{value: spec.Token.Bytes()},
		provenance: spec.Provenance,
		epoch:      spec.Epoch,
		trace:      spec.Trace,
	}, nil
}

func (c ContextCapture) IsZero() bool   { return c.provenance.IsZero() }
func (c ContextCapture) String() string { return "[job context capture]" }
func (c ContextCapture) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, c.String())
}
func (c ContextCapture) LogValue() slog.Value { return slog.StringValue(c.String()) }
func (ContextCapture) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: context capture cannot be serialized", ErrUnsupported)
}
func (c ContextCapture) valid() bool {
	return (c.tenant.IsZero() || c.tenant.valid()) && (c.actor.IsZero() || c.actor.valid()) && (c.token.IsZero() || c.token.valid()) && c.provenance.valid() && c.epoch.valid() && c.trace.valid()
}

type ContextCaptureRequest struct {
	namespace  Namespace
	definition Name
	partition  PartitionMode
}

func (r ContextCaptureRequest) Namespace() Namespace     { return r.namespace }
func (r ContextCaptureRequest) Definition() Name         { return r.definition }
func (r ContextCaptureRequest) Partition() PartitionMode { return r.partition }
func (r ContextCaptureRequest) String() string           { return "[job context capture request]" }
func (r ContextCaptureRequest) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (r ContextCaptureRequest) valid() bool {
	return r.namespace.valid() && r.definition.valid() && r.partition.Valid()
}

type TrustedContextProvider interface {
	Capture(context.Context, ContextCaptureRequest) (ContextCapture, error)
}

type TrustedContextProviderFunc func(context.Context, ContextCaptureRequest) (ContextCapture, error)

func (f TrustedContextProviderFunc) Capture(ctx context.Context, request ContextCaptureRequest) (ContextCapture, error) {
	return f(ctx, request)
}

type trustedTenantPartitioner struct {
	partitioner TenantPartitioner
	provenance  IdentityProvenance
	epoch       IdentityEpoch
}

func TrustTenantPartitioner(partitioner TenantPartitioner, provenance IdentityProvenance, epoch IdentityEpoch) (TrustedContextProvider, error) {
	if nilInterface(partitioner) || !provenance.valid() || !epoch.valid() {
		return nil, invalid("trusted tenant partitioner")
	}
	return trustedTenantPartitioner{partitioner: partitioner, provenance: provenance, epoch: epoch}, nil
}

func (p trustedTenantPartitioner) Capture(ctx context.Context, request ContextCaptureRequest) (ContextCapture, error) {
	if !request.valid() {
		return ContextCapture{}, invalid("context capture request")
	}
	spec := ContextCaptureSpec{Provenance: p.provenance, Epoch: p.epoch}
	if request.partition == PartitionTenantRequired {
		partition, err := p.partitioner.Partition(ctx)
		if err != nil {
			return ContextCapture{}, err
		}
		spec.Tenant = partition
	}
	return NewContextCapture(spec)
}

type IdentityRestoreRequest struct {
	namespace   Namespace
	partition   PartitionKey
	definition  Name
	tracePolicy TracePolicy
	durable     DurableContext
}

func (r IdentityRestoreRequest) Namespace() Namespace    { return r.namespace }
func (r IdentityRestoreRequest) Partition() PartitionKey { return r.partition }
func (r IdentityRestoreRequest) Definition() Name        { return r.definition }
func (r IdentityRestoreRequest) Scope() ContextScope     { return r.durable.scope }
func (r IdentityRestoreRequest) Tenant() (TenantIdentity, bool) {
	return r.durable.Tenant()
}
func (r IdentityRestoreRequest) Actor() (ActorIdentity, bool) {
	return r.durable.Actor()
}
func (r IdentityRestoreRequest) Token() (ProtectedIdentityToken, bool) {
	return r.durable.Token()
}
func (r IdentityRestoreRequest) Provenance() IdentityProvenance { return r.durable.provenance }
func (r IdentityRestoreRequest) Epoch() IdentityEpoch           { return r.durable.epoch }
func (r IdentityRestoreRequest) String() string                 { return "[job identity restore request]" }
func (r IdentityRestoreRequest) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (IdentityRestoreRequest) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: identity restore request cannot be serialized", ErrUnsupported)
}
func (r IdentityRestoreRequest) valid() bool {
	return r.durable.validFor(r.namespace, r.partition, r.definition, r.tracePolicy)
}

type TrustedIdentityRestorer interface {
	RestoreIdentity(context.Context, IdentityRestoreRequest) (RestoredIdentity, error)
}

type TrustedIdentityRestorerFunc func(context.Context, IdentityRestoreRequest) (RestoredIdentity, error)

func (f TrustedIdentityRestorerFunc) RestoreIdentity(ctx context.Context, request IdentityRestoreRequest) (RestoredIdentity, error) {
	return f(ctx, request)
}

type RestoredIdentity struct {
	context context.Context
	tenant  ProducerPartition
	actor   ProducerActor
}

func NewRestoredIdentity(ctx context.Context, tenant ProducerPartition, actor ProducerActor) (RestoredIdentity, error) {
	if nilInterface(ctx) || !tenant.IsZero() && !tenant.valid() || !actor.IsZero() && !actor.valid() {
		return RestoredIdentity{}, invalid("restored identity")
	}
	return RestoredIdentity{
		context: ctx,
		tenant:  ProducerPartition{value: strings.Clone(tenant.value)},
		actor:   ProducerActor{value: strings.Clone(actor.value)},
	}, nil
}

func (r RestoredIdentity) Context() context.Context { return r.context }
func (r RestoredIdentity) String() string           { return "[restored job identity]" }
func (r RestoredIdentity) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (r RestoredIdentity) LogValue() slog.Value { return slog.StringValue(r.String()) }
func (RestoredIdentity) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: restored identity cannot be serialized", ErrUnsupported)
}
func (r RestoredIdentity) validFor(request IdentityRestoreRequest) bool {
	if nilInterface(r.context) || !request.valid() {
		return false
	}
	if request.durable.scope == ContextTenant {
		if !r.tenant.valid() || partitionKey(request.namespace, r.tenant) != request.partition {
			return false
		}
	} else if !r.tenant.IsZero() {
		return false
	}
	if request.durable.actor.IsZero() {
		return r.actor.IsZero()
	}
	return r.actor.valid() && digestActorIdentity(request.namespace, request.partition, request.definition, request.durable.provenance, request.durable.epoch, r.actor) == request.durable.actor
}

type identityRestoreLineageKey struct{}

func RestoreTrustedIdentity(ctx context.Context, restorer TrustedIdentityRestorer, request IdentityRestoreRequest) (restored context.Context, err error) {
	if nilInterface(ctx) || nilInterface(restorer) || !request.valid() {
		return nil, invalid("trusted identity restoration")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if recover() != nil {
			restored = nil
			if contextErr := ctx.Err(); contextErr != nil {
				err = contextErr
			} else {
				err = ErrDriver
			}
		}
	}()
	lineage := new(byte)
	provided := context.WithValue(ctx, identityRestoreLineageKey{}, lineage)
	identity, restoreErr := restorer.RestoreIdentity(provided, request)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if restoreErr != nil || !identity.validFor(request) {
		return nil, ErrDriver
	}
	restored = identity.context
	if restored.Value(identityRestoreLineageKey{}) != lineage || !sameContextLifetime(provided, restored) {
		return nil, ErrDriver
	}
	return restored, nil
}

type TenantIdentity struct{ value [32]byte }

func (i TenantIdentity) Bytes() [32]byte { return i.value }
func (i TenantIdentity) IsZero() bool    { return i.value == [32]byte{} }
func (i TenantIdentity) String() string  { return "[job tenant identity]" }
func (i TenantIdentity) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, i.String())
}
func (TenantIdentity) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: tenant identity cannot be serialized", ErrUnsupported)
}

type ActorIdentity struct{ value [32]byte }

func (i ActorIdentity) Bytes() [32]byte { return i.value }
func (i ActorIdentity) IsZero() bool    { return i.value == [32]byte{} }
func (i ActorIdentity) String() string  { return "[job actor identity]" }
func (i ActorIdentity) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, i.String())
}
func (ActorIdentity) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: actor identity cannot be serialized", ErrUnsupported)
}

type DurableContextBinding struct{ value [32]byte }

func (b DurableContextBinding) Bytes() [32]byte { return b.value }
func (b DurableContextBinding) IsZero() bool    { return b.value == [32]byte{} }
func (b DurableContextBinding) String() string  { return "[job durable context binding]" }
func (b DurableContextBinding) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, b.String())
}
func (DurableContextBinding) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: durable context binding cannot be serialized", ErrUnsupported)
}

type DurableContext struct {
	scope      ContextScope
	tenant     TenantIdentity
	actor      ActorIdentity
	token      ProtectedIdentityToken
	provenance IdentityProvenance
	epoch      IdentityEpoch
	trace      UntrustedTraceCarrier
	binding    DurableContextBinding
}

func BuildDurableContext(namespace Namespace, definition Name, mode PartitionMode, policy TracePolicy, capture ContextCapture) (PartitionKey, DurableContext, error) {
	if !namespace.valid() || !definition.valid() || !mode.Valid() || !policy.valid() || !capture.valid() || !capture.trace.validFor(policy) {
		return PartitionKey{}, DurableContext{}, invalid("durable context")
	}
	var partition PartitionKey
	scope := ContextSystem
	if mode == PartitionTenantRequired {
		if !capture.tenant.valid() {
			return PartitionKey{}, DurableContext{}, invalid("durable context tenant")
		}
		partition = partitionKey(namespace, capture.tenant)
		scope = ContextTenant
	} else {
		if !capture.tenant.IsZero() {
			return PartitionKey{}, DurableContext{}, invalid("global durable context tenant")
		}
		partition = partitionKey(namespace, ProducerPartition{})
	}
	token := capture.token
	if !capture.actor.IsZero() && token.IsZero() {
		return PartitionKey{}, DurableContext{}, invalid("durable actor restoration token")
	}
	if scope == ContextSystem && capture.actor.IsZero() && !token.IsZero() {
		return PartitionKey{}, DurableContext{}, invalid("system restoration token without identity")
	}
	if token.IsZero() && scope == ContextTenant {
		token = ProtectedIdentityToken{value: []byte(capture.tenant.value)}
	}
	result := DurableContext{scope: scope, token: token, provenance: capture.provenance, epoch: capture.epoch, trace: capture.trace}
	if scope == ContextTenant {
		result.tenant = TenantIdentity{value: partition.digest.value}
	}
	if capture.actor.valid() {
		result.actor = digestActorIdentity(namespace, partition, definition, capture.provenance, capture.epoch, capture.actor)
	}
	result.binding = digestDurableContext(namespace, partition, definition, result)
	if !result.validFor(namespace, partition, definition, policy) {
		return PartitionKey{}, DurableContext{}, invalid("durable context")
	}
	return partition, result, nil
}

func (c DurableContext) Scope() ContextScope { return c.scope }
func (c DurableContext) Tenant() (TenantIdentity, bool) {
	return c.tenant, !c.tenant.IsZero()
}
func (c DurableContext) Actor() (ActorIdentity, bool) {
	return c.actor, !c.actor.IsZero()
}
func (c DurableContext) Token() (ProtectedIdentityToken, bool) {
	return ProtectedIdentityToken{value: c.token.Bytes()}, !c.token.IsZero()
}
func (c DurableContext) IdentityRestoreRequest(namespace Namespace, partition PartitionKey, definition Name, policy TracePolicy) (IdentityRestoreRequest, error) {
	request := IdentityRestoreRequest{namespace: namespace, partition: partition, definition: definition, tracePolicy: policy, durable: c}
	if !request.valid() {
		return IdentityRestoreRequest{}, invalid("identity restore request")
	}
	return request, nil
}
func (c DurableContext) Provenance() IdentityProvenance { return c.provenance }
func (c DurableContext) Epoch() IdentityEpoch           { return c.epoch }
func (c DurableContext) Trace() UntrustedTraceCarrier   { return c.trace }
func (c DurableContext) Binding() DurableContextBinding { return c.binding }
func (c DurableContext) IsZero() bool                   { return c.binding.IsZero() }
func (c DurableContext) String() string                 { return "[job durable context]" }
func (c DurableContext) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, c.String())
}
func (c DurableContext) LogValue() slog.Value { return slog.StringValue(c.String()) }
func (DurableContext) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: durable context cannot be serialized", ErrUnsupported)
}
func (c DurableContext) validFor(namespace Namespace, partition PartitionKey, definition Name, policy TracePolicy) bool {
	identityTokenRequired := c.scope == ContextTenant || !c.actor.IsZero()
	if !namespace.valid() || !partition.validFor(namespace) || !definition.valid() || !policy.valid() || !c.scope.Valid() || !c.provenance.valid() || !c.epoch.valid() || !c.trace.validFor(policy) || c.binding.IsZero() || identityTokenRequired != !c.token.IsZero() {
		return false
	}
	if c.scope == ContextTenant {
		if partition.Global() || c.tenant.IsZero() || c.tenant.value != partition.digest.value || c.token.IsZero() {
			return false
		}
	} else if !partition.Global() || !c.tenant.IsZero() {
		return false
	}
	return c.binding == digestDurableContext(namespace, partition, definition, c)
}

type CorrelationRecord struct {
	Key   string
	Value string
}

func (CorrelationRecord) String() string { return "[job correlation record]" }
func (r CorrelationRecord) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (CorrelationRecord) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: correlation record cannot be serialized", ErrUnsupported)
}

type TraceCarrierRecord struct {
	TraceParent  string
	TraceState   string
	Correlations []CorrelationRecord
}

func (TraceCarrierRecord) String() string { return "[job trace carrier record]" }
func (r TraceCarrierRecord) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (TraceCarrierRecord) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: trace carrier record cannot be serialized", ErrUnsupported)
}

type DurableContextRecord struct {
	Scope      ContextScope
	Tenant     [32]byte
	Actor      [32]byte
	Token      []byte
	Provenance string
	Epoch      uint64
	Trace      TraceCarrierRecord
	Binding    [32]byte
}

func (DurableContextRecord) String() string { return "[job durable context record]" }
func (r DurableContextRecord) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (DurableContextRecord) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: durable context record cannot be serialized", ErrUnsupported)
}

func (c DurableContext) Record() DurableContextRecord {
	record := DurableContextRecord{
		Scope:      c.scope,
		Tenant:     c.tenant.value,
		Actor:      c.actor.value,
		Token:      c.token.Bytes(),
		Provenance: c.provenance.value,
		Epoch:      c.epoch.Value(),
		Binding:    c.binding.value,
		Trace: TraceCarrierRecord{
			TraceParent: c.trace.TraceParent(),
			TraceState:  c.trace.TraceState(),
		},
	}
	fields := c.trace.Correlations()
	record.Trace.Correlations = make([]CorrelationRecord, len(fields))
	for index, field := range fields {
		record.Trace.Correlations[index] = CorrelationRecord{Key: field.key.value, Value: strings.Clone(field.value)}
	}
	return record
}

func RestoreDurableContext(namespace Namespace, partition PartitionKey, definition Name, policy TracePolicy, record DurableContextRecord) (DurableContext, error) {
	if !namespace.valid() || !partition.validFor(namespace) || !definition.valid() || !policy.valid() || !record.Scope.Valid() || record.Epoch == 0 || record.Binding == [32]byte{} || len(record.Token) > MaxIdentityTokenBytes || len(record.Trace.TraceParent) > MaxTraceParentBytes || len(record.Trace.TraceState) > MaxTraceStateBytes || len(record.Trace.Correlations) > MaxCorrelationFields {
		return DurableContext{}, corruptDurableContext()
	}
	provenance, err := ParseIdentityProvenance(record.Provenance)
	if err != nil {
		return DurableContext{}, corruptDurableContext()
	}
	charge := len(record.Trace.TraceParent) + len(record.Trace.TraceState)
	for _, stored := range record.Trace.Correlations {
		if len(stored.Key) > MaxCorrelationKeyBytes || len(stored.Value) > MaxCorrelationValueBytes {
			return DurableContext{}, corruptDurableContext()
		}
		charge += len(stored.Key) + len(stored.Value) + 2
		if charge > MaxTraceCarrierBytes {
			return DurableContext{}, corruptDurableContext()
		}
	}
	fields := make([]CorrelationField, len(record.Trace.Correlations))
	for index, stored := range record.Trace.Correlations {
		key, keyErr := ParseCorrelationKey(stored.Key)
		field, fieldErr := NewCorrelationField(key, stored.Value)
		if keyErr != nil || fieldErr != nil {
			return DurableContext{}, corruptDurableContext()
		}
		fields[index] = field
	}
	trace, err := NewUntrustedTraceCarrier(TraceCarrierSpec{TraceParent: record.Trace.TraceParent, TraceState: record.Trace.TraceState, Correlations: fields})
	if err != nil || !trace.validFor(policy) {
		return DurableContext{}, corruptDurableContext()
	}
	var token ProtectedIdentityToken
	if len(record.Token) > 0 {
		token, err = NewProtectedIdentityToken(record.Token)
		if err != nil {
			return DurableContext{}, corruptDurableContext()
		}
	}
	result := DurableContext{
		scope:      record.Scope,
		tenant:     TenantIdentity{value: record.Tenant},
		actor:      ActorIdentity{value: record.Actor},
		token:      token,
		provenance: provenance,
		epoch:      IdentityEpoch(record.Epoch),
		trace:      trace,
		binding:    DurableContextBinding{value: record.Binding},
	}
	canonical := result.Record()
	if !result.validFor(namespace, partition, definition, policy) || canonical.Trace.TraceParent != record.Trace.TraceParent || canonical.Trace.TraceState != record.Trace.TraceState || !sameCorrelationRecords(canonical.Trace.Correlations, record.Trace.Correlations) {
		return DurableContext{}, corruptDurableContext()
	}
	return result, nil
}

func digestActorIdentity(namespace Namespace, partition PartitionKey, definition Name, provenance IdentityProvenance, epoch IdentityEpoch, actor ProducerActor) ActorIdentity {
	digest := sha256.New()
	writePlacementString(digest, "frostgrove.jobs.actor-identity.v1")
	namespaceDigest := namespace.Digest()
	writePlacementBytes(digest, namespaceDigest[:])
	writePlacementUint(digest, uint64(partition.revision))
	writePlacementBytes(digest, partition.digest.value[:])
	writePlacementString(digest, definition.value)
	writePlacementString(digest, provenance.value)
	writePlacementUint(digest, epoch.Value())
	writePlacementString(digest, actor.value)
	var value [32]byte
	copy(value[:], digest.Sum(nil))
	return ActorIdentity{value: value}
}

func digestDurableContext(namespace Namespace, partition PartitionKey, definition Name, durable DurableContext) DurableContextBinding {
	digest := sha256.New()
	writePlacementString(digest, "frostgrove.jobs.durable-context.v1")
	namespaceDigest := namespace.Digest()
	writePlacementBytes(digest, namespaceDigest[:])
	writePlacementUint(digest, uint64(partition.revision))
	writePlacementBytes(digest, partition.digest.value[:])
	writePlacementString(digest, definition.value)
	if partition.global {
		writePlacementUint(digest, 1)
	} else {
		writePlacementUint(digest, 0)
	}
	writePlacementUint(digest, uint64(durable.scope))
	writePlacementBytes(digest, durable.tenant.value[:])
	writePlacementBytes(digest, durable.actor.value[:])
	writePlacementBytes(digest, durable.token.value)
	writePlacementString(digest, durable.provenance.value)
	writePlacementUint(digest, durable.epoch.Value())
	writePlacementString(digest, durable.trace.traceParent)
	writePlacementString(digest, durable.trace.traceState)
	writePlacementUint(digest, uint64(durable.trace.length))
	for index := range int(durable.trace.length) {
		writePlacementString(digest, durable.trace.correlations[index].key.value)
		writePlacementString(digest, durable.trace.correlations[index].value)
	}
	var value [32]byte
	copy(value[:], digest.Sum(nil))
	return DurableContextBinding{value: value}
}

func sameCorrelationRecords(left, right []CorrelationRecord) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validProtectedText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, item := range value {
		if unicode.IsControl(item) || item == '\u2028' || item == '\u2029' {
			return false
		}
	}
	return true
}

func validTraceParent(value string) bool {
	if value == "" {
		return true
	}
	if len(value) < 55 || len(value) > MaxTraceParentBytes || value[2] != '-' || value[35] != '-' || value[52] != '-' || value[:2] == "ff" {
		return false
	}
	for index := range 55 {
		if index == 2 || index == 35 || index == 52 {
			continue
		}
		if !asciiLowerHex(value[index]) {
			return false
		}
	}
	if value[3:35] == "00000000000000000000000000000000" || value[36:52] == "0000000000000000" || value[:2] == "00" && len(value) != 55 {
		return false
	}
	if len(value) == 55 {
		return true
	}
	if len(value) == 56 || value[55] != '-' {
		return false
	}
	for index := 56; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

func validTraceState(value string, hasParent bool) bool {
	if value == "" {
		return true
	}
	if !hasParent || len(value) > MaxTraceStateBytes {
		return false
	}
	members := strings.Split(value, ",")
	if len(members) > 32 {
		return false
	}
	seen := make(map[string]struct{}, len(members))
	for _, raw := range members {
		member := strings.Trim(raw, " \t")
		if member == "" {
			continue
		}
		separator := strings.IndexByte(member, '=')
		if separator <= 0 || separator == len(member)-1 || strings.IndexByte(member[separator+1:], '=') >= 0 {
			return false
		}
		key, memberValue := member[:separator], member[separator+1:]
		if !validTraceStateKey(key) || !validTraceStateValue(memberValue) {
			return false
		}
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validTraceStateKey(value string) bool {
	if value == "" || len(value) > 256 || !asciiLowerAlphaNumeric(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		item := value[index]
		if !asciiLowerAlphaNumeric(item) && item != '_' && item != '-' && item != '*' && item != '/' && item != '@' {
			return false
		}
	}
	return true
}

func validTraceStateValue(value string) bool {
	if value == "" || len(value) > 256 || value[len(value)-1] == ' ' {
		return false
	}
	for index := range len(value) {
		item := value[index]
		if item < 0x20 || item > 0x7e || item == ',' || item == '=' {
			return false
		}
	}
	return true
}

func sameContextLifetime(left, right context.Context) bool {
	leftDeadline, leftHasDeadline := left.Deadline()
	rightDeadline, rightHasDeadline := right.Deadline()
	return left.Done() == right.Done() && leftHasDeadline == rightHasDeadline && (!leftHasDeadline || leftDeadline == rightDeadline)
}

func asciiLowerHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f'
}

func corruptDurableContext() error {
	return fmt.Errorf("%w: durable context", ErrCorrupt)
}
