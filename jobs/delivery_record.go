package jobs

import (
	"bytes"
	"fmt"
	"log/slog"
	"time"
)

type InvocationGenesisRecord struct {
	ID           InvocationID
	Namespace    Namespace
	Partition    PartitionKey
	Definition   Name
	Queue        QueueName
	Mode         PlacementMode
	Intent       IntentKey
	LegacyIntent LegacyIntent
	Priority     int
	CreatedAt    time.Time
	EligibleAt   time.Time
	StartBefore  time.Time
	Policy       PolicySnapshot
	Context      DurableContextRecord
}

func (InvocationGenesisRecord) String() string { return "[job invocation genesis record]" }
func (r InvocationGenesisRecord) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (InvocationGenesisRecord) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: invocation genesis record cannot be serialized", ErrUnsupported)
}

type EncodedPayloadRecord struct {
	Codec   CodecID
	Version SchemaVersion
	Data    []byte
}

func (EncodedPayloadRecord) String() string { return "[job encoded payload record]" }
func (r EncodedPayloadRecord) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (EncodedPayloadRecord) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: encoded payload record cannot be serialized", ErrUnsupported)
}

type DeliveryRecord struct {
	Genesis       InvocationGenesisRecord
	Payload       EncodedPayloadRecord
	WireDigest    WireDigest
	PayloadDigest PayloadDigest
	Outcomes      []InvocationOutcome
	Attempts      []AttemptRecord
}

const (
	deliveryRecordFixedBytes  = 1024
	deliveryOutcomeFixedBytes = 256
	deliveryAttemptFixedBytes = 256
	MaxDeliveryRecordBytes    = deliveryRecordFixedBytes + MaxPayloadBytes + MaxIdentityTokenBytes + MaxTraceCarrierBytes + MaxIntentBytes + 3*MaxNameBytes + 2*MaxQueueNameBytes + 2*MaxCodecIDBytes + MaxIdentityProvenanceBytes + MaxCorrelationFields*MaxCorrelationKeyBytes + MaxInvocationOutcomes*(deliveryOutcomeFixedBytes+MaxFailureCodeBytes+MaxPublicFailureBytes) + MaxAttemptOrdinal*(deliveryAttemptFixedBytes+MaxBindingNameBytes+MaxBuildIDBytes+MaxFailureCodeBytes+MaxPublicFailureBytes)
)

func (DeliveryRecord) String() string { return "[job delivery record]" }
func (r DeliveryRecord) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (DeliveryRecord) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: delivery record cannot be serialized", ErrUnsupported)
}
func (r DeliveryRecord) Size() (int, error) { return DeliveryRecordSize(r) }

type RestoredDelivery struct {
	invocation    Invocation
	payload       EncodedPayload
	wireDigest    WireDigest
	payloadDigest PayloadDigest
	compatibility DeliveryCompatibility
}

func (d RestoredDelivery) Invocation() Invocation       { return d.invocation }
func (d RestoredDelivery) Payload() EncodedPayload      { return cloneEncodedPayload(d.payload) }
func (d RestoredDelivery) WireDigest() WireDigest       { return d.wireDigest }
func (d RestoredDelivery) PayloadDigest() PayloadDigest { return d.payloadDigest }
func (d RestoredDelivery) Compatibility() DeliveryCompatibility {
	return d.compatibility
}
func (d RestoredDelivery) Compatible() bool { return d.compatibility == DeliveryCompatible }
func (d RestoredDelivery) IsZero() bool     { return d.invocation.IsZero() }
func (RestoredDelivery) String() string     { return "[job restored delivery]" }
func (d RestoredDelivery) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
func (d RestoredDelivery) LogValue() slog.Value { return slog.StringValue(d.String()) }
func (RestoredDelivery) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: restored delivery cannot be serialized", ErrUnsupported)
}

func (d RestoredDelivery) RequireCompatible() error {
	if d.IsZero() || !d.compatibility.Valid() {
		return invalid("restored delivery compatibility")
	}
	if d.compatibility != DeliveryCompatible {
		return incompatibleDelivery(d.compatibility.String())
	}
	return nil
}

type DeliveryCompatibility uint8

const (
	DeliveryCompatible DeliveryCompatibility = iota + 1
	DeliveryDefinitionUnavailable
	DeliveryPartitionIncompatible
	DeliveryPayloadRevisionUnsupported
	DeliveryPayloadIdentityIncompatible
	DeliveryPayloadLimitIncompatible
)

func (c DeliveryCompatibility) Valid() bool {
	return c >= DeliveryCompatible && c <= DeliveryPayloadLimitIncompatible
}

func (c DeliveryCompatibility) String() string {
	switch c {
	case DeliveryCompatible:
		return "compatible"
	case DeliveryDefinitionUnavailable:
		return "definition_unavailable"
	case DeliveryPartitionIncompatible:
		return "partition_incompatible"
	case DeliveryPayloadRevisionUnsupported:
		return "payload_revision_unsupported"
	case DeliveryPayloadIdentityIncompatible:
		return "payload_identity_incompatible"
	case DeliveryPayloadLimitIncompatible:
		return "payload_limit_incompatible"
	default:
		return "unknown"
	}
}

func (d RestoredDelivery) Record() DeliveryRecord {
	if d.IsZero() {
		return DeliveryRecord{}
	}
	return deliveryRecordFromParts(d.invocation, d.payload, d.wireDigest, d.payloadDigest)
}

func NewDeliveryRecord(invocation Invocation, payload EncodedPayload, wireDigest WireDigest, payloadDigest PayloadDigest) (DeliveryRecord, error) {
	record := deliveryRecordFromParts(invocation, payload, wireDigest, payloadDigest)
	if err := preflightDeliveryRecord(record); err != nil {
		return DeliveryRecord{}, err
	}
	if digestWirePayload(payload) != wireDigest {
		return DeliveryRecord{}, invalid("delivery payload integrity")
	}
	return cloneDeliveryRecord(record), nil
}

func RestoreDeliveryRecord(catalog Catalog, record DeliveryRecord) (RestoredDelivery, error) {
	if err := preflightDeliveryRecord(record); err != nil {
		return RestoredDelivery{}, err
	}
	durable, err := RestoreDurableContext(record.Genesis.Namespace, record.Genesis.Partition, record.Genesis.Definition, record.Genesis.Policy.Trace(), cloneDurableContextRecord(record.Genesis.Context))
	if err != nil {
		return RestoredDelivery{}, corruptDeliveryRecord()
	}
	genesis := InvocationSpec{
		ID:           record.Genesis.ID,
		Namespace:    record.Genesis.Namespace,
		Partition:    record.Genesis.Partition,
		Definition:   record.Genesis.Definition,
		Queue:        record.Genesis.Queue,
		Mode:         record.Genesis.Mode,
		Intent:       record.Genesis.Intent,
		LegacyIntent: record.Genesis.LegacyIntent,
		Priority:     record.Genesis.Priority,
		CreatedAt:    record.Genesis.CreatedAt,
		EligibleAt:   record.Genesis.EligibleAt,
		StartBefore:  record.Genesis.StartBefore,
		Policy:       record.Genesis.Policy,
		Context:      durable,
	}
	invocation, err := RestoreInvocation(InvocationRestoreSpec{Genesis: genesis, Outcomes: record.Outcomes, Attempts: record.Attempts})
	if err != nil {
		return RestoredDelivery{}, corruptDeliveryRecord()
	}
	payload, err := NewEncodedPayload(record.Payload.Codec, record.Payload.Version, record.Payload.Data)
	if err != nil || digestWirePayload(payload) != record.WireDigest {
		return RestoredDelivery{}, corruptDeliveryRecord()
	}
	compatibility, err := checkDeliveryCompatibility(catalog, record, payload)
	if err != nil {
		return RestoredDelivery{}, err
	}
	return RestoredDelivery{invocation: invocation, payload: payload, wireDigest: record.WireDigest, payloadDigest: record.PayloadDigest, compatibility: compatibility}, nil
}

func deliveryRecordFromParts(invocation Invocation, payload EncodedPayload, wireDigest WireDigest, payloadDigest PayloadDigest) DeliveryRecord {
	legacy, _ := invocation.LegacyIntent()
	return DeliveryRecord{
		Genesis: InvocationGenesisRecord{
			ID:           invocation.ID(),
			Namespace:    invocation.Namespace(),
			Partition:    invocation.Partition(),
			Definition:   invocation.Definition(),
			Queue:        invocation.Queue(),
			Mode:         invocation.Mode(),
			Intent:       invocation.Intent(),
			LegacyIntent: legacy,
			Priority:     invocation.Priority(),
			CreatedAt:    invocation.CreatedAt(),
			EligibleAt:   invocation.EligibleAt(),
			StartBefore:  invocation.StartBefore(),
			Policy:       invocation.Policy(),
			Context:      invocation.Context().Record(),
		},
		Payload:       EncodedPayloadRecord{Codec: payload.Codec(), Version: payload.Version(), Data: payload.Bytes()},
		WireDigest:    wireDigest,
		PayloadDigest: payloadDigest,
		Outcomes:      invocation.History(),
		Attempts:      invocation.AttemptRecords(),
	}
}

func preflightDeliveryRecord(record DeliveryRecord) error {
	if _, err := DeliveryRecordSize(record); err != nil || len(record.Payload.Data) > record.Genesis.Policy.Payload().MaxBytes {
		return corruptDeliveryRecord()
	}
	if len(record.Outcomes) == 0 || int(record.Genesis.Policy.trace.length) > len(record.Genesis.Policy.trace.keys) {
		return corruptDeliveryRecord()
	}
	if !record.Genesis.ID.valid() || !record.Genesis.Namespace.valid() || !record.Genesis.Partition.validFor(record.Genesis.Namespace) || !record.Genesis.Definition.valid() || !record.Genesis.Queue.valid() || !record.Genesis.Mode.Valid() || !record.Genesis.Intent.validFor(record.Genesis.Namespace, record.Genesis.Partition, record.Genesis.Definition) || !record.Genesis.Policy.valid() || record.Genesis.Queue != record.Genesis.Policy.Queue() || record.Genesis.Priority <= 0 || record.Genesis.Priority > MaximumPriority || !record.Payload.Codec.valid() || record.Payload.Version.IsZero() || !record.WireDigest.valid() || !canonicalRequiredDeliveryTime(record.Genesis.CreatedAt) || !canonicalRequiredDeliveryTime(record.Genesis.EligibleAt) || !canonicalOptionalDeliveryTime(record.Genesis.StartBefore) {
		return corruptDeliveryRecord()
	}
	if !record.Genesis.LegacyIntent.IsZero() && !record.Genesis.LegacyIntent.valid() {
		return corruptDeliveryRecord()
	}
	if record.Genesis.Mode == PlacementOnce {
		if !record.PayloadDigest.valid() {
			return corruptDeliveryRecord()
		}
	} else if !record.PayloadDigest.IsZero() && !record.PayloadDigest.valid() {
		return corruptDeliveryRecord()
	}
	for _, outcome := range record.Outcomes {
		if !outcome.valid() || !canonicalOptionalDeliveryTime(outcome.occurredAt) || !canonicalOptionalDeliveryTime(outcome.availableAt) {
			return corruptDeliveryRecord()
		}
	}
	for _, attempt := range record.Attempts {
		if !attempt.Ordinal.valid() || !attempt.Binding.valid() || !attempt.Build.valid() || !attempt.State.Valid() || !canonicalRequiredDeliveryTime(attempt.StartedAt) || !canonicalRequiredDeliveryTime(attempt.Deadline) || !canonicalOptionalDeliveryTime(attempt.ProgressedAt) || !canonicalOptionalDeliveryTime(attempt.ProgressDeadline) || !canonicalOptionalDeliveryTime(attempt.FinishedAt) {
			return corruptDeliveryRecord()
		}
	}
	return nil
}

func DeliveryRecordSize(record DeliveryRecord) (int, error) {
	if len(record.Outcomes) > MaxInvocationOutcomes || len(record.Attempts) > MaxAttemptOrdinal || len(record.Payload.Data) > MaxPayloadBytes || len(record.Genesis.Context.Token) > MaxIdentityTokenBytes || len(record.Genesis.Context.Trace.TraceParent) > MaxTraceParentBytes || len(record.Genesis.Context.Trace.TraceState) > MaxTraceStateBytes || len(record.Genesis.Context.Trace.Correlations) > MaxCorrelationFields || len(record.Genesis.Context.Provenance) > MaxIdentityProvenanceBytes || len(record.Genesis.LegacyIntent.value) > MaxIntentBytes || len(record.Genesis.Namespace.application.value) > MaxNameBytes || len(record.Genesis.Namespace.environment.value) > MaxNameBytes || len(record.Genesis.Definition.value) > MaxNameBytes || len(record.Genesis.Queue.value) > MaxQueueNameBytes || len(record.Genesis.Policy.queue.value) > MaxQueueNameBytes || len(record.Payload.Codec.value) > MaxCodecIDBytes || len(record.PayloadDigest.identity.value) > MaxCodecIDBytes {
		return 0, tooLarge("delivery record")
	}
	traceBytes := len(record.Genesis.Context.Trace.TraceParent) + len(record.Genesis.Context.Trace.TraceState)
	size := deliveryRecordFixedBytes + len(record.Payload.Data) + len(record.Genesis.LegacyIntent.value) + len(record.Genesis.Context.Token) + len(record.Genesis.Context.Provenance) + traceBytes + len(record.Genesis.Namespace.application.value) + len(record.Genesis.Namespace.environment.value) + len(record.Genesis.Definition.value) + len(record.Genesis.Queue.value) + len(record.Genesis.Policy.queue.value) + len(record.Payload.Codec.value) + len(record.PayloadDigest.identity.value)
	for index := range record.Genesis.Policy.trace.keys {
		if len(record.Genesis.Policy.trace.keys[index].value) > MaxCorrelationKeyBytes {
			return 0, tooLarge("delivery trace policy")
		}
		size += len(record.Genesis.Policy.trace.keys[index].value)
	}
	for _, field := range record.Genesis.Context.Trace.Correlations {
		if len(field.Key) > MaxCorrelationKeyBytes || len(field.Value) > MaxCorrelationValueBytes {
			return 0, tooLarge("delivery trace carrier")
		}
		fieldBytes := len(field.Key) + len(field.Value) + 2
		traceBytes += fieldBytes
		size += fieldBytes
		if traceBytes > MaxTraceCarrierBytes {
			return 0, tooLarge("delivery trace carrier")
		}
	}
	for _, outcome := range record.Outcomes {
		if len(outcome.failure.code.value) > MaxFailureCodeBytes || len(outcome.failure.message) > MaxPublicFailureBytes || len(outcome.disposition.failure.code.value) > MaxFailureCodeBytes || len(outcome.disposition.failure.message) > MaxPublicFailureBytes {
			return 0, tooLarge("delivery outcome ledger")
		}
		size += deliveryOutcomeFixedBytes + len(outcome.failure.code.value) + len(outcome.failure.message) + len(outcome.disposition.failure.code.value) + len(outcome.disposition.failure.message)
	}
	for _, attempt := range record.Attempts {
		if len(attempt.Binding.value) > MaxBindingNameBytes || len(attempt.Build.value) > MaxBuildIDBytes || len(attempt.Disposition.failure.code.value) > MaxFailureCodeBytes || len(attempt.Disposition.failure.message) > MaxPublicFailureBytes {
			return 0, tooLarge("delivery attempt ledger")
		}
		size += deliveryAttemptFixedBytes + len(attempt.Binding.value) + len(attempt.Build.value) + len(attempt.Disposition.failure.code.value) + len(attempt.Disposition.failure.message)
	}
	if size > MaxDeliveryRecordBytes {
		return 0, tooLarge("delivery record")
	}
	return size, nil
}

func checkDeliveryCompatibility(catalog Catalog, record DeliveryRecord, payload EncodedPayload) (DeliveryCompatibility, error) {
	declaration, ok := catalog.Lookup(record.Genesis.Definition)
	if !ok || nilInterface(declaration) {
		return DeliveryDefinitionUnavailable, nil
	}
	descriptor := declaration.Describe()
	if !descriptor.Resolved || descriptor.Name != record.Genesis.Definition {
		return DeliveryDefinitionUnavailable, nil
	}
	wantsTenant := descriptor.Partition == PartitionTenantRequired
	if !descriptor.Partition.Valid() || wantsTenant == record.Genesis.Partition.Global() {
		return DeliveryPartitionIncompatible, nil
	}
	if !descriptorAcceptsPayload(descriptor.Codec, record.Payload.Codec, record.Payload.Version) {
		return DeliveryPayloadRevisionUnsupported, nil
	}
	if descriptor.Policy.MaxPayloadBytes < len(record.Payload.Data) {
		return DeliveryPayloadLimitIncompatible, nil
	}
	if !record.PayloadDigest.IsZero() {
		identity := descriptor.PayloadIdentity
		if !identity.Available || identity.ID != record.PayloadDigest.Identity() || identity.Version != record.PayloadDigest.Version() {
			return DeliveryPayloadIdentityIncompatible, nil
		}
	}
	if !payloadDigestMatchesRecord(record.Genesis.Mode, payload, record.PayloadDigest, descriptor.PayloadIdentity) {
		return 0, corruptDeliveryRecord()
	}
	return DeliveryCompatible, nil
}

func descriptorAcceptsPayload(codec CodecDescription, identity CodecID, version SchemaVersion) bool {
	if version == codec.CurrentVersion {
		return identity == codec.ID
	}
	for _, upcast := range codec.Upcasts {
		if upcast.From == version && upcast.SourceCodec == identity {
			return true
		}
	}
	return false
}

func payloadDigestMatchesRecord(mode PlacementMode, payload EncodedPayload, digest PayloadDigest, identity PayloadIdentityDescription) bool {
	if mode == PlacementOnce && !digest.valid() || mode != PlacementOnce && !digest.IsZero() && !digest.valid() {
		return false
	}
	if digest.IsZero() || !identity.Available {
		return digest.IsZero() || identity.Available
	}
	if digest.Identity() != identity.ID || digest.Version() != identity.Version {
		return false
	}
	return !identity.Automatic || digest == digestEncodedPayload(identity.ID, identity.Version, payload.encodedBytes())
}

func canonicalRequiredDeliveryTime(value time.Time) bool {
	canonical, err := requiredTime(value, "delivery record time")
	return err == nil && canonical == value
}

func canonicalOptionalDeliveryTime(value time.Time) bool {
	return value.IsZero() || canonicalRequiredDeliveryTime(value)
}

func cloneDeliveryRecord(record DeliveryRecord) DeliveryRecord {
	result := record
	result.Genesis.Context = cloneDurableContextRecord(record.Genesis.Context)
	result.Payload.Data = bytes.Clone(record.Payload.Data)
	result.Outcomes = append([]InvocationOutcome(nil), record.Outcomes...)
	result.Attempts = append([]AttemptRecord(nil), record.Attempts...)
	return result
}

func cloneDurableContextRecord(record DurableContextRecord) DurableContextRecord {
	record.Token = bytes.Clone(record.Token)
	record.Trace.Correlations = append([]CorrelationRecord(nil), record.Trace.Correlations...)
	return record
}

func corruptDeliveryRecord() error {
	return fmt.Errorf("%w: delivery record", ErrCorrupt)
}

func incompatibleDelivery(reason string) error {
	return fmt.Errorf("%w: delivery compatibility: %s", ErrUnsupported, reason)
}
