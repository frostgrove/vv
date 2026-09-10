package audit

import (
	"bytes"
	"slices"
	"time"
)

type outcomeCodes struct {
	values []Outcome
}

type OutcomeCodes struct {
	value outcomeCodes
}

type reasonCodes struct {
	values []Reason
}

type ReasonCodes struct {
	value reasonCodes
}

type targetPolicy[E any] struct {
	configured     bool
	present        bool
	extract        func(E) Reference
	classification Classification
	mode           StorageMode
}

type TargetPolicy[E any] struct {
	value targetPolicy[E]
}

type outcomePolicy[E any] struct {
	codes   []Outcome
	extract func(E) Outcome
}

type OutcomePolicy[E any] struct {
	value outcomePolicy[E]
}

type reasonPolicy[E any] struct {
	codes    []Reason
	extract  func(E) Reason
	optional bool
}

type ReasonPolicy[E any] struct {
	value reasonPolicy[E]
}

type occurredAtPolicy[E any] struct {
	extract func(E) time.Time
}

type OccurredAtPolicy[E any] struct {
	value occurredAtPolicy[E]
}

type eventField[E any] interface {
	description() FieldDescription
	extract(E) (draftValue, []byte, error)
}

type eventFieldValue[E, V any] struct {
	name           FieldName
	extractValue   func(E) V
	codec          Codec[V]
	classification Classification
	mode           StorageMode
	queryIndex     bool
}

type EventField[E any] struct {
	value eventField[E]
}

type EventIndex[E, V any] struct {
	value eventFieldValue[E, V]
}

type EventPolicy[E any] struct {
	Semantics  PolicySemantics
	Descriptor Descriptor
	Target     TargetPolicy[E]
	Outcome    OutcomePolicy[E]
	Reason     ReasonPolicy[E]
	OccurredAt OccurredAtPolicy[E]
	Fields     []EventField[E]
}

type eventType[E any] struct {
	seal       declarationSeal
	descriptor Descriptor
	target     targetPolicy[E]
	outcome    outcomePolicy[E]
	reason     reasonPolicy[E]
	occurredAt occurredAtPolicy[E]
	fields     []eventField[E]
}

type EventType[E any] struct {
	value *eventType[E]
}

func Outcomes(values ...Outcome) OutcomeCodes {
	codes, err := TryOutcomes(values...)
	if err != nil {
		panic(err)
	}
	return codes
}

func TryOutcomes(values ...Outcome) (OutcomeCodes, error) {
	canonical, err := canonicalCodes(values, "outcomes")
	if err != nil {
		return OutcomeCodes{}, err
	}
	return OutcomeCodes{value: outcomeCodes{values: canonical}}, nil
}

func Reasons(values ...Reason) ReasonCodes {
	codes, err := TryReasons(values...)
	if err != nil {
		panic(err)
	}
	return codes
}

func TryReasons(values ...Reason) (ReasonCodes, error) {
	canonical, err := canonicalCodes(values, "reasons")
	if err != nil {
		return ReasonCodes{}, err
	}
	return ReasonCodes{value: reasonCodes{values: canonical}}, nil
}

func EventTarget[E any](extract func(E) Reference, classification Classification, mode StorageMode) TargetPolicy[E] {
	if extract == nil {
		panic(auditErrorAt(ErrDeclaration, "target.extractor"))
	}
	if err := validateFieldPrivacy(classification, mode, classification == Secret); err != nil {
		panic(err)
	}
	return TargetPolicy[E]{value: targetPolicy[E]{
		configured: true, present: true, extract: extract,
		classification: classification, mode: mode,
	}}
}

func NoEventTarget[E any]() TargetPolicy[E] {
	return TargetPolicy[E]{value: targetPolicy[E]{configured: true}}
}

func EventOutcome[E any](codes OutcomeCodes, extract func(E) Outcome) OutcomePolicy[E] {
	if extract == nil || len(codes.value.values) == 0 {
		panic(auditErrorAt(ErrDeclaration, "outcome"))
	}
	return OutcomePolicy[E]{value: outcomePolicy[E]{codes: slices.Clone(codes.value.values), extract: extract}}
}

func EventReason[E any](codes ReasonCodes, extract func(E) Reason) ReasonPolicy[E] {
	return eventReason(codes, extract, false)
}

func OptionalEventReason[E any](codes ReasonCodes, extract func(E) Reason) ReasonPolicy[E] {
	return eventReason(codes, extract, true)
}

func eventReason[E any](codes ReasonCodes, extract func(E) Reason, optional bool) ReasonPolicy[E] {
	if extract == nil || len(codes.value.values) == 0 {
		panic(auditErrorAt(ErrDeclaration, "reason"))
	}
	return ReasonPolicy[E]{value: reasonPolicy[E]{codes: slices.Clone(codes.value.values), extract: extract, optional: optional}}
}

func EventOccurredAt[E any](extract func(E) time.Time) OccurredAtPolicy[E] {
	if extract == nil {
		panic(auditErrorAt(ErrDeclaration, "occurred_at"))
	}
	return OccurredAtPolicy[E]{value: occurredAtPolicy[E]{extract: extract}}
}

func EventValue[E, V any](name FieldName, extract func(E) V, codec Codec[V], classification Classification) EventField[E] {
	return newEventField(name, extract, codec, classification, AsPlaintext, false)
}

func EventRedacted[E, V any](name FieldName, extract func(E) V, codec Codec[V], classification Classification) EventField[E] {
	return newEventField(name, extract, codec, classification, AsRedacted, false)
}

func EventTokenized[E, V any](name FieldName, extract func(E) V, codec Codec[V], classification Classification) EventField[E] {
	return newEventField(name, extract, codec, classification, AsToken, false)
}

func EventProtected[E, V any](name FieldName, extract func(E) V, codec Codec[V], classification Classification) EventField[E] {
	return newEventField(name, extract, codec, classification, AsProtected, false)
}

func EventPlaintextIndex[E, V any](name FieldName, extract func(E) V, codec Codec[V], classification Classification) EventIndex[E, V] {
	return EventIndex[E, V]{value: newEventFieldValue(name, extract, codec, classification, AsPlaintext, true)}
}

func EventTokenIndex[E, V any](name FieldName, extract func(E) V, codec Codec[V], classification Classification) EventIndex[E, V] {
	return EventIndex[E, V]{value: newEventFieldValue(name, extract, codec, classification, AsToken, true)}
}

func EventIndexedProtectedIndex[E, V any](name FieldName, extract func(E) V, codec Codec[V], classification Classification) EventIndex[E, V] {
	return EventIndex[E, V]{value: newEventFieldValue(name, extract, codec, classification, AsIndexedProtected, true)}
}

func (index EventIndex[E, V]) Field() EventField[E] {
	return EventField[E]{value: index.value}
}

func EventFields[E any](fields ...EventField[E]) []EventField[E] {
	return slices.Clone(fields)
}

func newEventField[E, V any](name FieldName, extract func(E) V, codec Codec[V], classification Classification, mode StorageMode, index bool) EventField[E] {
	return EventField[E]{value: newEventFieldValue(name, extract, codec, classification, mode, index)}
}

func newEventFieldValue[E, V any](name FieldName, extract func(E) V, codec Codec[V], classification Classification, mode StorageMode, index bool) eventFieldValue[E, V] {
	if !validSemanticName(string(name)) || extract == nil || codec.value == nil {
		panic(auditErrorAt(ErrDeclaration, "event_field"))
	}
	if err := validateFieldPrivacy(classification, mode, classification == Secret); err != nil {
		panic(err)
	}
	return eventFieldValue[E, V]{
		name: name, extractValue: extract, codec: codec,
		classification: classification, mode: mode, queryIndex: index,
	}
}

func (field eventFieldValue[E, V]) description() FieldDescription {
	return FieldDescription{
		Name: field.name, Codec: field.codec.Description(), Classification: field.classification,
		Mode: field.mode, QueryIndex: field.queryIndex,
	}
}

func (field eventFieldValue[E, V]) extract(event E) (_ draftValue, _ []byte, err error) {
	defer func() {
		if recover() != nil {
			err = auditErrorAt(ErrInvalid, "event_field.extractor")
		}
	}()
	value := field.extractValue(event)
	wire, encodeErr := field.codec.Encode(value)
	if encodeErr != nil {
		return draftValue{}, nil, encodeErr
	}
	draft := draftValue{
		field: field.name, codec: field.codec.Description(), classification: field.classification,
		mode: field.mode, state: ValuePresent, canonical: bytes.Clone(wire),
	}
	if field.mode == AsRedacted {
		draft.state = ValueRedacted
		draft.canonical = nil
	}
	return draft, bytes.Clone(wire), nil
}

func Declare[E any](policy EventPolicy[E]) *EventType[E] {
	declaration, err := TryDeclare(policy)
	if err != nil {
		panic(err)
	}
	return declaration
}

func TryDeclare[E any](policy EventPolicy[E]) (*EventType[E], error) {
	if err := validatePolicySemantics(policy.Semantics, true); err != nil {
		return nil, err
	}
	if err := validateDescriptor(policy.Descriptor, true); err != nil {
		return nil, err
	}
	if !policy.Target.value.configured {
		return nil, auditErrorAt(ErrDeclaration, "target")
	}
	if policy.Target.value.present {
		if policy.Target.value.extract == nil {
			return nil, auditErrorAt(ErrDeclaration, "target.extractor")
		}
		if err := validateFieldPrivacy(policy.Target.value.classification, policy.Target.value.mode, policy.Target.value.classification == Secret); err != nil {
			return nil, err
		}
	}
	if policy.Outcome.value.extract == nil || len(policy.Outcome.value.codes) == 0 {
		return nil, auditErrorAt(ErrDeclaration, "outcome")
	}
	if policy.Reason.value.extract == nil && len(policy.Reason.value.codes) != 0 || policy.Reason.value.extract != nil && len(policy.Reason.value.codes) == 0 {
		return nil, auditErrorAt(ErrDeclaration, "reason")
	}
	if policy.OccurredAt.value.extract == nil {
		return nil, auditErrorAt(ErrDeclaration, "occurred_at")
	}
	fields, descriptions, err := canonicalEventFields(policy.Fields)
	if err != nil {
		return nil, err
	}
	target := SubjectDescription{}
	if policy.Target.value.present {
		target = SubjectDescription{Classification: policy.Target.value.classification, Mode: policy.Target.value.mode}
	}
	description := DeclarationDescription{
		Kind: EventDeclaration, Resource: policy.Descriptor.Resource, Action: policy.Descriptor.Action,
		Owner: policy.Descriptor.Owner, Purpose: policy.Descriptor.Purpose,
		Retention: policy.Descriptor.Retention, Consequence: policy.Descriptor.Consequence,
		TargetPresent: policy.Target.value.present, Target: target,
		Outcomes: slices.Clone(policy.Outcome.value.codes), Reasons: slices.Clone(policy.Reason.value.codes),
		ReasonOptional: policy.Reason.value.optional, Fields: descriptions,
		Context: policy.Descriptor.Context.description(),
	}
	description.Semantics = policy.Semantics.description(PolicyFingerprint{})
	description.Semantics.Fingerprint = policyFingerprint(description, nil)
	event := &eventType[E]{
		descriptor: policy.Descriptor, target: cloneTargetPolicy(policy.Target.value),
		outcome: cloneOutcomePolicy(policy.Outcome.value), reason: cloneReasonPolicy(policy.Reason.value),
		occurredAt: policy.OccurredAt.value, fields: fields,
	}
	event.seal.description = description
	declared := &EventType[E]{value: event}
	event.seal.members = []declarationMember{{
		declaration: declared, resource: description.Resource, action: description.Action,
		retention: description.Retention, consequence: description.Consequence,
		key: declarationMemberKey(description.Resource, description.Action),
	}}
	return declared, nil
}

func canonicalEventFields[E any](input []EventField[E]) ([]eventField[E], []FieldDescription, error) {
	if len(input) > MaxFieldsPerItem {
		return nil, nil, auditTooLarge("fields", MaxFieldsPerItem)
	}
	fields := make([]eventField[E], len(input))
	descriptions := make([]FieldDescription, len(input))
	seen := make(map[FieldName]struct{}, len(input))
	for index, field := range input {
		if nilByReflection(field.value) {
			return nil, nil, auditErrorAt(ErrDeclaration, "fields")
		}
		description := field.value.description()
		if _, duplicate := seen[description.Name]; duplicate {
			return nil, nil, auditErrorAt(ErrDeclaration, "fields")
		}
		seen[description.Name] = struct{}{}
		fields[index] = field.value
		descriptions[index] = description
	}
	order := make([]int, len(fields))
	for index := range order {
		order[index] = index
	}
	slices.SortFunc(order, func(left, right int) int { return compareFieldDescription(descriptions[left], descriptions[right]) })
	canonicalFields := make([]eventField[E], len(fields))
	canonicalDescriptions := make([]FieldDescription, len(fields))
	for index, source := range order {
		canonicalFields[index] = fields[source]
		canonicalDescriptions[index] = descriptions[source]
	}
	return canonicalFields, canonicalDescriptions, nil
}

func cloneTargetPolicy[E any](value targetPolicy[E]) targetPolicy[E] {
	return value
}

func cloneOutcomePolicy[E any](value outcomePolicy[E]) outcomePolicy[E] {
	value.codes = slices.Clone(value.codes)
	return value
}

func cloneReasonPolicy[E any](value reasonPolicy[E]) reasonPolicy[E] {
	value.codes = slices.Clone(value.codes)
	return value
}

func (e *EventType[E]) auditDeclaration() {}

func (e *EventType[E]) sealedDeclaration() *declarationSeal {
	if e == nil || e.value == nil {
		return nil
	}
	return &e.value.seal
}

func (e *EventType[E]) auditOperationMember() {}

func (e *EventType[E]) sealedOperationMember() declarationMember {
	if e == nil || e.value == nil || len(e.value.seal.members) != 1 {
		return declarationMember{}
	}
	return e.value.seal.members[0]
}

func (e *EventType[E]) Description() DeclarationDescription {
	if e == nil || e.value == nil {
		return DeclarationDescription{}
	}
	return cloneDeclarationDescription(e.value.seal.description)
}

func (e *EventType[E]) History(history *History) *EventHistory[E] {
	if e == nil || e.value == nil || history == nil {
		return nil
	}
	return &EventHistory[E]{history: history, event: e}
}

func (e *EventType[E]) New(event E) (Draft, error) {
	value, _, err := e.newDraft(event)
	return value, err
}

func (e *EventType[E]) newDraft(event E) (_ Draft, logical [][]byte, err error) {
	if e == nil || e.value == nil || nilByReflection(event) {
		return Draft{}, nil, auditErrorAt(ErrInvalid, "event")
	}
	declaration := e.value
	defer func() {
		if recover() != nil {
			err = auditErrorAt(ErrInvalid, "event.extractor")
		}
	}()
	target := Reference("")
	if declaration.target.present {
		target = declaration.target.extract(event)
		if !validOpaqueReference(string(target), MaxReferenceBytes) {
			return Draft{}, nil, auditErrorAt(ErrInvalid, "event.target")
		}
	}
	outcome := declaration.outcome.extract(event)
	if !slices.Contains(declaration.outcome.codes, outcome) {
		return Draft{}, nil, auditErrorAt(ErrInvalid, "event.outcome")
	}
	reason := Reason("")
	if declaration.reason.extract != nil {
		reason = declaration.reason.extract(event)
		if reason == "" && !declaration.reason.optional || reason != "" && !slices.Contains(declaration.reason.codes, reason) {
			return Draft{}, nil, auditErrorAt(ErrInvalid, "event.reason")
		}
	}
	occurredAt := declaration.occurredAt.extract(event)
	if occurredAt.IsZero() {
		return Draft{}, nil, auditErrorAt(ErrInvalid, "event.occurred_at")
	}
	occurredAt = occurredAt.Round(0).UTC()
	values := make([]draftValue, len(declaration.fields))
	logical = make([][]byte, len(declaration.fields))
	for index, field := range declaration.fields {
		value, wire, fieldErr := field.extract(event)
		if fieldErr != nil {
			return Draft{}, nil, fieldErr
		}
		values[index] = value
		logical[index] = wire
	}
	return Draft{value: draft{
		declaration: e, descriptor: declaration.descriptor, target: target,
		targetPolicy: declaration.seal.description.Target, targetPresent: declaration.target.present,
		occurredAt: occurredAt, outcome: outcome, reason: reason, values: values,
	}}, logical, nil
}

func ComputeEventFixtureFingerprint[E any](eventType *EventType[E], name FixtureName, event E) (PolicyFixtureFingerprint, error) {
	if eventType == nil || eventType.value == nil || !policyHasFixture(eventType.value.seal.description.Semantics, name) {
		return PolicyFixtureFingerprint{}, auditErrorAt(ErrInvalid, "fixture")
	}
	draft, logical, err := eventType.newDraft(event)
	if err != nil {
		return PolicyFixtureFingerprint{}, err
	}
	var payload bytes.Buffer
	if draft.value.targetPresent {
		payload.WriteByte(1)
		writeFrame(&payload, []byte(draft.value.target))
	} else {
		payload.WriteByte(0)
	}
	writeFrame(&payload, []byte(draft.value.outcome))
	writeFrame(&payload, []byte(draft.value.reason))
	writeTime(&payload, draft.value.occurredAt)
	for index, value := range draft.value.values {
		writeFrame(&payload, []byte(value.field))
		writeFrame(&payload, value.codec.Fingerprint[:])
		payload.WriteByte(byte(value.classification))
		payload.WriteByte(byte(value.mode))
		writeFrame(&payload, logical[index])
	}
	description := eventType.value.seal.description
	return fixtureFingerprint(DeclarationPolicyFixture, name, description.Semantics.Version,
		description.Resource, description.Action, payload.Bytes()), nil
}
