package audit

import (
	"bytes"
	"crypto/sha256"
	"slices"
	"time"
)

type AttemptDescriptor struct {
	Resource    Resource
	Owner       Owner
	Purpose     Purpose
	Retention   RetentionClass
	Consequence Consequence
	Context     ContextPolicy
}

type attemptTargetPolicy[S any] struct {
	configured     bool
	present        bool
	extract        func(S) Reference
	classification Classification
	mode           StorageMode
}

type AttemptTargetPolicy[S any] struct {
	value attemptTargetPolicy[S]
}

type attemptField[P any] interface {
	description() FieldDescription
	extract(P) (draftValue, []byte, error)
}

type attemptFieldValue[P, V any] struct {
	name           FieldName
	extractValue   func(P) V
	codec          Codec[V]
	classification Classification
	mode           StorageMode
}

type AttemptField[P any] struct {
	value attemptField[P]
}

type attemptStartPolicy[S any] struct {
	configured bool
	target     attemptTargetPolicy[S]
	fields     []attemptField[S]
}

type AttemptStartPolicy[S any] struct {
	value attemptStartPolicy[S]
}

type attemptCheckpointPolicy[C any] struct {
	configured bool
	disabled   bool
}

type AttemptCheckpointPolicy[C any] struct {
	value attemptCheckpointPolicy[C]
}

type attemptTransitionFields[F any] struct {
	configured bool
	transition AttemptTransitionKind
	fields     []attemptField[F]
}

type AttemptTransitionFields[F any] struct {
	value attemptTransitionFields[F]
}

type attemptFinishFieldSet[F any] struct {
	configured bool
	phases     []attemptTransitionFields[F]
}

type AttemptFinishFieldSet[F any] struct {
	value attemptFinishFieldSet[F]
}

type attemptReasonPolicy struct {
	transition AttemptTransitionKind
	codes      []Reason
}

type AttemptReasonPolicy struct {
	value attemptReasonPolicy
}

type attemptReasonPolicies struct {
	configured bool
	values     []attemptReasonPolicy
}

type AttemptReasonPolicies struct {
	value attemptReasonPolicies
}

type attemptFinishPolicy[F any] struct {
	configured bool
	phases     []attemptTransitionFields[F]
	reasons    []attemptReasonPolicy
}

type AttemptFinishPolicy[F any] struct {
	value attemptFinishPolicy[F]
}

type attemptContinuityPolicy struct {
	configured bool
	facts      []AttemptOwnerFact
}

type AttemptContinuityPolicy struct {
	value attemptContinuityPolicy
}

type AttemptPolicy[S, C, F any] struct {
	Operation      *OperationType
	Semantics      PolicySemantics
	Descriptor     AttemptDescriptor
	MaxOpen        time.Duration
	MaxCheckpoints uint16
	MaxStateBytes  uint64
	Continuity     AttemptContinuityPolicy
	Start          AttemptStartPolicy[S]
	Checkpoints    AttemptCheckpointPolicy[C]
	Finish         AttemptFinishPolicy[F]
}

type attemptType[S, C, F any] struct {
	seal           declarationSeal
	operation      *OperationType
	semantics      PolicySemantics
	descriptor     AttemptDescriptor
	maxOpen        time.Duration
	maxCheckpoints uint16
	maxStateBytes  uint64
	openReserve    uint64
	continuity     attemptContinuityPolicy
	start          attemptStartPolicy[S]
	checkpoints    attemptCheckpointPolicy[C]
	finish         attemptFinishPolicy[F]
	fingerprint    AttemptPolicyFingerprint
}

type AttemptType[S, C, F any] struct {
	value *attemptType[S, C, F]
}

func AttemptTarget[S any](extract func(S) Reference, classification Classification, mode StorageMode) AttemptTargetPolicy[S] {
	target, err := TryAttemptTarget(extract, classification, mode)
	if err != nil {
		panic(err)
	}
	return target
}

func TryAttemptTarget[S any](extract func(S) Reference, classification Classification, mode StorageMode) (AttemptTargetPolicy[S], error) {
	if extract == nil {
		return AttemptTargetPolicy[S]{}, auditErrorAt(ErrDeclaration, "attempt.target.extractor")
	}
	if err := validateSubjectPrivacy(classification, mode, classification == Secret); err != nil {
		return AttemptTargetPolicy[S]{}, err
	}
	return AttemptTargetPolicy[S]{value: attemptTargetPolicy[S]{
		configured: true, present: true, extract: extract,
		classification: classification, mode: mode,
	}}, nil
}

func NoAttemptTarget[S any]() AttemptTargetPolicy[S] {
	return AttemptTargetPolicy[S]{value: attemptTargetPolicy[S]{configured: true}}
}

func AttemptFields[P any](fields ...AttemptField[P]) []AttemptField[P] {
	return slices.Clone(fields)
}

func AttemptValue[P, V any](name FieldName, extract func(P) V, codec Codec[V], classification Classification) AttemptField[P] {
	return newAttemptField(name, extract, codec, classification, AsPlaintext)
}

func AttemptRedacted[P, V any](name FieldName, extract func(P) V, codec Codec[V], classification Classification) AttemptField[P] {
	return newAttemptField(name, extract, codec, classification, AsRedacted)
}

func AttemptTokenized[P, V any](name FieldName, extract func(P) V, codec Codec[V], classification Classification) AttemptField[P] {
	return newAttemptField(name, extract, codec, classification, AsToken)
}

func AttemptProtected[P, V any](name FieldName, extract func(P) V, codec Codec[V], classification Classification) AttemptField[P] {
	return newAttemptField(name, extract, codec, classification, AsProtected)
}

func newAttemptField[P, V any](name FieldName, extract func(P) V, codec Codec[V], classification Classification, mode StorageMode) AttemptField[P] {
	if !validSemanticName(string(name)) || extract == nil || codec.value == nil {
		panic(auditErrorAt(ErrDeclaration, "attempt.field"))
	}
	if err := validateFieldPrivacy(classification, mode, classification == Secret); err != nil {
		panic(err)
	}
	return AttemptField[P]{value: attemptFieldValue[P, V]{
		name: name, extractValue: extract, codec: codec,
		classification: classification, mode: mode,
	}}
}

func (field attemptFieldValue[P, V]) description() FieldDescription {
	return FieldDescription{
		Name: field.name, Codec: field.codec.Description(), Classification: field.classification,
		Mode: field.mode,
	}
}

func (field attemptFieldValue[P, V]) extract(input P) (_ draftValue, _ []byte, err error) {
	defer func() {
		if recover() != nil {
			err = auditErrorAt(ErrInvalid, "attempt.field.extractor")
		}
	}()
	value := field.extractValue(input)
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
	}
	return draft, bytes.Clone(wire), nil
}

func AttemptStart[S any](target AttemptTargetPolicy[S], fields []AttemptField[S]) AttemptStartPolicy[S] {
	start, err := TryAttemptStart(target, fields)
	if err != nil {
		panic(err)
	}
	return start
}

func TryAttemptStart[S any](target AttemptTargetPolicy[S], fields []AttemptField[S]) (AttemptStartPolicy[S], error) {
	if !target.value.configured || target.value.present && target.value.extract == nil {
		return AttemptStartPolicy[S]{}, auditErrorAt(ErrDeclaration, "attempt.start.target")
	}
	canonical, _, err := canonicalAttemptFields(fields, "attempt.start.fields")
	if err != nil {
		return AttemptStartPolicy[S]{}, err
	}
	return AttemptStartPolicy[S]{value: attemptStartPolicy[S]{
		configured: true, target: target.value, fields: canonical,
	}}, nil
}

func NoAttemptCheckpoints[C any]() AttemptCheckpointPolicy[C] {
	return AttemptCheckpointPolicy[C]{value: attemptCheckpointPolicy[C]{configured: true, disabled: true}}
}

func AttemptFieldsFor[F any](transition AttemptTransitionKind, fields ...AttemptField[F]) AttemptTransitionFields[F] {
	phase, err := TryAttemptFieldsFor(transition, fields...)
	if err != nil {
		panic(err)
	}
	return phase
}

func TryAttemptFieldsFor[F any](transition AttemptTransitionKind, fields ...AttemptField[F]) (AttemptTransitionFields[F], error) {
	if !runOnlyFinishTransition(transition) {
		return AttemptTransitionFields[F]{}, auditErrorAt(ErrDeclaration, "attempt.finish.transition")
	}
	canonical, _, err := canonicalAttemptFields(fields, "attempt.finish.fields")
	if err != nil {
		return AttemptTransitionFields[F]{}, err
	}
	return AttemptTransitionFields[F]{value: attemptTransitionFields[F]{
		configured: true, transition: transition, fields: canonical,
	}}, nil
}

func AttemptFinishFields[F any](phases ...AttemptTransitionFields[F]) AttemptFinishFieldSet[F] {
	set, err := TryAttemptFinishFields(phases...)
	if err != nil {
		panic(err)
	}
	return set
}

func TryAttemptFinishFields[F any](phases ...AttemptTransitionFields[F]) (AttemptFinishFieldSet[F], error) {
	if len(phases) > 3 {
		return AttemptFinishFieldSet[F]{}, auditTooLarge("attempt.finish.phases", 3)
	}
	values := make([]attemptTransitionFields[F], len(phases))
	seen := make(map[AttemptTransitionKind]struct{}, len(phases))
	for index, phase := range phases {
		value := phase.value
		if !value.configured || !runOnlyFinishTransition(value.transition) {
			return AttemptFinishFieldSet[F]{}, auditErrorAt(ErrDeclaration, "attempt.finish.phases")
		}
		if _, duplicate := seen[value.transition]; duplicate {
			return AttemptFinishFieldSet[F]{}, auditErrorAt(ErrDeclaration, "attempt.finish.phases")
		}
		seen[value.transition] = struct{}{}
		value.fields = slices.Clone(value.fields)
		values[index] = value
	}
	slices.SortFunc(values, func(left, right attemptTransitionFields[F]) int {
		return int(left.transition) - int(right.transition)
	})
	return AttemptFinishFieldSet[F]{value: attemptFinishFieldSet[F]{configured: true, phases: values}}, nil
}

func AttemptReasonsFor(transition AttemptTransitionKind, codes ReasonCodes) AttemptReasonPolicy {
	policy, err := TryAttemptReasonsFor(transition, codes)
	if err != nil {
		panic(err)
	}
	return policy
}

func TryAttemptReasonsFor(transition AttemptTransitionKind, codes ReasonCodes) (AttemptReasonPolicy, error) {
	if transition != AttemptFailedTransition && transition != AttemptCancelledTransition {
		return AttemptReasonPolicy{}, auditErrorAt(ErrDeclaration, "attempt.finish.reasons")
	}
	canonical, err := canonicalCodes(codes.value.values, "attempt.finish.reasons")
	if err != nil {
		return AttemptReasonPolicy{}, err
	}
	return AttemptReasonPolicy{value: attemptReasonPolicy{transition: transition, codes: canonical}}, nil
}

func AttemptReasons(policies ...AttemptReasonPolicy) AttemptReasonPolicies {
	reasons, err := TryAttemptReasons(policies...)
	if err != nil {
		panic(err)
	}
	return reasons
}

func TryAttemptReasons(policies ...AttemptReasonPolicy) (AttemptReasonPolicies, error) {
	if len(policies) == 0 || len(policies) > 2 {
		return AttemptReasonPolicies{}, auditErrorAt(ErrDeclaration, "attempt.finish.reasons")
	}
	values := make([]attemptReasonPolicy, len(policies))
	seen := make(map[AttemptTransitionKind]struct{}, len(policies))
	for index, policy := range policies {
		value := policy.value
		if value.transition != AttemptFailedTransition && value.transition != AttemptCancelledTransition || len(value.codes) == 0 {
			return AttemptReasonPolicies{}, auditErrorAt(ErrDeclaration, "attempt.finish.reasons")
		}
		if _, duplicate := seen[value.transition]; duplicate {
			return AttemptReasonPolicies{}, auditErrorAt(ErrDeclaration, "attempt.finish.reasons")
		}
		seen[value.transition] = struct{}{}
		value.codes = slices.Clone(value.codes)
		values[index] = value
	}
	slices.SortFunc(values, func(left, right attemptReasonPolicy) int {
		return int(left.transition) - int(right.transition)
	})
	return AttemptReasonPolicies{value: attemptReasonPolicies{configured: true, values: values}}, nil
}

func AttemptFinish[F any](reasons AttemptReasonPolicies, fields AttemptFinishFieldSet[F]) AttemptFinishPolicy[F] {
	finish, err := TryAttemptFinish(reasons, fields)
	if err != nil {
		panic(err)
	}
	return finish
}

func TryAttemptFinish[F any](reasons AttemptReasonPolicies, fields AttemptFinishFieldSet[F]) (AttemptFinishPolicy[F], error) {
	if !reasons.value.configured || !fields.value.configured || len(reasons.value.values) != 2 {
		return AttemptFinishPolicy[F]{}, auditErrorAt(ErrDeclaration, "attempt.finish")
	}
	if reasons.value.values[0].transition != AttemptFailedTransition || reasons.value.values[1].transition != AttemptCancelledTransition {
		return AttemptFinishPolicy[F]{}, auditErrorAt(ErrDeclaration, "attempt.finish.reasons")
	}
	return AttemptFinishPolicy[F]{value: attemptFinishPolicy[F]{
		configured: true, phases: cloneAttemptTransitionFields(fields.value.phases),
		reasons: cloneAttemptReasonPolicies(reasons.value.values),
	}}, nil
}

func AttemptOwnedBy(facts ...AttemptOwnerFact) AttemptContinuityPolicy {
	continuity, err := TryAttemptOwnedBy(facts...)
	if err != nil {
		panic(err)
	}
	return continuity
}

func TryAttemptOwnedBy(facts ...AttemptOwnerFact) (AttemptContinuityPolicy, error) {
	if len(facts) == 0 || len(facts) > 3 {
		return AttemptContinuityPolicy{}, auditErrorAt(ErrDeclaration, "attempt.continuity")
	}
	values := slices.Clone(facts)
	slices.Sort(values)
	for index, fact := range values {
		if !validAttemptOwnerFact(fact) || index > 0 && fact == values[index-1] {
			return AttemptContinuityPolicy{}, auditErrorAt(ErrDeclaration, "attempt.continuity")
		}
	}
	return AttemptContinuityPolicy{value: attemptContinuityPolicy{configured: true, facts: values}}, nil
}

func DeclareAttempt[S, C, F any](policy AttemptPolicy[S, C, F]) *AttemptType[S, C, F] {
	attempt, err := TryDeclareAttempt(policy)
	if err != nil {
		panic(err)
	}
	return attempt
}

func TryDeclareAttempt[S, C, F any](policy AttemptPolicy[S, C, F]) (*AttemptType[S, C, F], error) {
	operation := policy.Operation
	if operation == nil || operation.value == nil || operation.sealedDeclaration() == nil {
		return nil, auditErrorAt(ErrDeclaration, "attempt.operation")
	}
	if err := validateAttemptDescriptor(policy.Descriptor, policy.Continuity.value); err != nil {
		return nil, err
	}
	if policy.MaxOpen <= 0 || policy.MaxOpen > MaxAttemptOpenLifetime {
		return nil, auditErrorAt(ErrDeclaration, "attempt.max_open")
	}
	if policy.MaxCheckpoints != 0 || !policy.Checkpoints.value.configured || !policy.Checkpoints.value.disabled {
		return nil, auditErrorAt(ErrDeclaration, "attempt.checkpoints")
	}
	if policy.MaxStateBytes == 0 || policy.MaxStateBytes > MaxAttemptStateBytes {
		return nil, auditErrorAt(ErrDeclaration, "attempt.max_state_bytes")
	}
	if !policy.Continuity.value.configured || len(policy.Continuity.value.facts) == 0 {
		return nil, auditErrorAt(ErrDeclaration, "attempt.continuity")
	}
	if !policy.Start.value.configured || !policy.Start.value.target.configured {
		return nil, auditErrorAt(ErrDeclaration, "attempt.start")
	}
	if !policy.Finish.value.configured {
		return nil, auditErrorAt(ErrDeclaration, "attempt.finish")
	}
	if err := validateAttemptPolicySemantics(policy.Semantics, policy.Finish.value); err != nil {
		return nil, err
	}
	startDescriptions := describeAttemptFields(policy.Start.value.fields)
	finishDescriptions := describeAttemptFinishFields(policy.Finish.value.phases)
	openReserve, err := attemptOpenReserve(policy.MaxStateBytes, policy.Start.value.target.present, startDescriptions, finishDescriptions)
	if err != nil {
		return nil, err
	}
	target := SubjectDescription{}
	if policy.Start.value.target.present {
		target = SubjectDescription{
			Classification: policy.Start.value.target.classification,
			Mode:           policy.Start.value.target.mode,
		}
	}
	attemptDescription := AttemptDescription{
		Operation: operation.value.seal.description.Operation,
		MaxOpen:   policy.MaxOpen, MaxCheckpoints: policy.MaxCheckpoints,
		MaxStateBytes: policy.MaxStateBytes, OpenReserveBytes: openReserve,
		Continuity: slices.Clone(policy.Continuity.value.facts),
		Start: AttemptPhaseDescription{
			TargetPresent: policy.Start.value.target.present, Target: target, Fields: startDescriptions,
		},
		Finish:  finishDescriptions,
		Reasons: describeAttemptReasons(policy.Finish.value.reasons),
	}
	description := DeclarationDescription{
		Kind: AttemptDeclaration, Resource: policy.Descriptor.Resource,
		Owner: policy.Descriptor.Owner, Purpose: policy.Descriptor.Purpose,
		Retention: policy.Descriptor.Retention, Consequence: policy.Descriptor.Consequence,
		Context: policy.Descriptor.Context.description(), Attempt: attemptDescription,
	}
	description.Semantics = policy.Semantics.description(PolicyFingerprint{})
	description.Attempt.Fingerprint = attemptDeclarationFingerprint(description, operation.value.seal.description)
	description.Semantics.Fingerprint = policyFingerprint(description, operation.value.seal.description.Semantics.Fingerprint[:])
	value := &attemptType[S, C, F]{
		operation: operation, semantics: cloneAttemptSemantics(policy.Semantics),
		descriptor: cloneAttemptDescriptor(policy.Descriptor), maxOpen: policy.MaxOpen,
		maxCheckpoints: policy.MaxCheckpoints, maxStateBytes: policy.MaxStateBytes, openReserve: openReserve,
		continuity: cloneAttemptContinuity(policy.Continuity.value),
		start:      cloneAttemptStart(policy.Start.value), checkpoints: policy.Checkpoints.value,
		finish: cloneAttemptFinish(policy.Finish.value), fingerprint: description.Attempt.Fingerprint,
	}
	declared := &AttemptType[S, C, F]{value: value}
	value.seal.description = description
	value.seal.attemptOperation = operation
	for _, action := range attemptActions() {
		value.seal.members = append(value.seal.members, declarationMember{
			declaration: declared, resource: description.Resource, action: action,
			retention: description.Retention, consequence: description.Consequence,
			key: declarationMemberKey(description.Resource, action),
		})
	}
	return declared, nil
}

func (a *AttemptType[S, C, F]) auditDeclaration() {}

func (a *AttemptType[S, C, F]) sealedDeclaration() *declarationSeal {
	if a == nil || a.value == nil {
		return nil
	}
	return &a.value.seal
}

func (a *AttemptType[S, C, F]) Description() DeclarationDescription {
	if a == nil || a.value == nil {
		return DeclarationDescription{}
	}
	return cloneDeclarationDescription(a.value.seal.description)
}

func (p attemptTargetPolicy[S]) reference(input S) (_ Reference, err error) {
	if !p.configured || !p.present || p.extract == nil {
		return "", auditErrorAt(ErrInvalid, "attempt.target")
	}
	defer func() {
		if recover() != nil {
			err = auditErrorAt(ErrInvalid, "attempt.target.extractor")
		}
	}()
	value := p.extract(input)
	if !validReferenceText(string(value)) {
		return "", auditErrorAt(ErrInvalid, "attempt.target")
	}
	return value, nil
}

func (p attemptFinishPolicy[F]) fieldsFor(transition AttemptTransitionKind) []attemptField[F] {
	for _, phase := range p.phases {
		if phase.transition == transition {
			return slices.Clone(phase.fields)
		}
	}
	return nil
}

func (p attemptFinishPolicy[F]) reasonsFor(transition AttemptTransitionKind) []Reason {
	for _, policy := range p.reasons {
		if policy.transition == transition {
			return slices.Clone(policy.codes)
		}
	}
	return nil
}

func canonicalAttemptFields[P any](input []AttemptField[P], path string) ([]attemptField[P], []FieldDescription, error) {
	if len(input) > MaxFieldsPerItem {
		return nil, nil, auditTooLarge(path, MaxFieldsPerItem)
	}
	fields := make([]attemptField[P], len(input))
	descriptions := make([]FieldDescription, len(input))
	seen := make(map[FieldName]struct{}, len(input))
	for index, field := range input {
		if nilByReflection(field.value) {
			return nil, nil, auditErrorAt(ErrDeclaration, path)
		}
		description := field.value.description()
		if !validSemanticName(string(description.Name)) || description.Codec.Name == "" {
			return nil, nil, auditErrorAt(ErrDeclaration, path)
		}
		if _, duplicate := seen[description.Name]; duplicate {
			return nil, nil, auditErrorAt(ErrDeclaration, path)
		}
		seen[description.Name] = struct{}{}
		fields[index] = field.value
		descriptions[index] = description
	}
	order := make([]int, len(fields))
	for index := range order {
		order[index] = index
	}
	slices.SortFunc(order, func(left, right int) int {
		return compareFieldDescription(descriptions[left], descriptions[right])
	})
	canonicalFields := make([]attemptField[P], len(fields))
	canonicalDescriptions := make([]FieldDescription, len(fields))
	for index, source := range order {
		canonicalFields[index] = fields[source]
		canonicalDescriptions[index] = descriptions[source]
	}
	return canonicalFields, canonicalDescriptions, nil
}

func describeAttemptFields[P any](fields []attemptField[P]) []FieldDescription {
	descriptions := make([]FieldDescription, len(fields))
	for index, field := range fields {
		descriptions[index] = field.description()
	}
	return descriptions
}

func describeAttemptFinishFields[F any](input []attemptTransitionFields[F]) []AttemptFinishPhaseDescription {
	fields := make(map[AttemptTransitionKind][]FieldDescription, len(input))
	for _, phase := range input {
		fields[phase.transition] = describeAttemptFields(phase.fields)
	}
	return []AttemptFinishPhaseDescription{
		{Transition: AttemptSucceededTransition, Fields: fields[AttemptSucceededTransition]},
		{Transition: AttemptFailedTransition, Fields: fields[AttemptFailedTransition]},
		{Transition: AttemptCancelledTransition, Fields: fields[AttemptCancelledTransition]},
	}
}

func describeAttemptReasons(input []attemptReasonPolicy) []AttemptReasonDescription {
	descriptions := make([]AttemptReasonDescription, len(input))
	for index, policy := range input {
		descriptions[index] = AttemptReasonDescription{Transition: policy.transition, Codes: slices.Clone(policy.codes)}
	}
	return descriptions
}

func validateAttemptDescriptor(descriptor AttemptDescriptor, continuity attemptContinuityPolicy) error {
	if !validSemanticName(string(descriptor.Resource)) || !validSemanticName(string(descriptor.Owner)) ||
		!validSemanticName(string(descriptor.Purpose)) || !validSemanticName(string(descriptor.Retention)) ||
		!descriptor.Consequence.Valid() {
		return auditErrorAt(ErrDeclaration, "attempt.descriptor")
	}
	if err := validateContextPolicy(descriptor.Context); err != nil {
		return err
	}
	operationFacts := 0
	ownerContext := false
	for _, fact := range descriptor.Context.value.facts {
		if fact.kind == OperationContext {
			operationFacts++
			if fact.generated || fact.presence != ContextRequired || !searchableMode(fact.mode) {
				return auditErrorAt(ErrDeclaration, "attempt.context.operation")
			}
		}
		if fact.kind == ActorChainContext && fact.presence == ContextRequired && searchableMode(fact.mode) {
			ownerContext = true
		}
	}
	if operationFacts != 1 {
		return auditErrorAt(ErrDeclaration, "attempt.context.operation")
	}
	if !continuity.configured || len(continuity.facts) == 0 || !ownerContext {
		return auditErrorAt(ErrDeclaration, "attempt.context.owner")
	}
	return nil
}

func validateAttemptPolicySemantics[F any](semantics PolicySemantics, finish attemptFinishPolicy[F]) error {
	if semantics.value.version == 0 {
		return auditErrorAt(ErrDeclaration, "semantics.version")
	}
	start := false
	success := false
	covered := make(map[string]struct{})
	for _, golden := range semantics.value.goldens {
		if err := validateSemanticGolden(golden); err != nil {
			return err
		}
		switch golden.kind {
		case AttemptStartPolicyFixture:
			start = true
		case AttemptFinishPolicyFixture:
			switch golden.transition {
			case AttemptSucceededTransition:
				if golden.reason != "" {
					return auditErrorAt(ErrDeclaration, "semantics.fixtures")
				}
				success = true
			case AttemptFailedTransition, AttemptCancelledTransition:
				if !slices.Contains(finish.reasonsFor(golden.transition), golden.reason) {
					return auditErrorAt(ErrDeclaration, "semantics.fixtures")
				}
				covered[attemptGoldenCoordinate(golden.transition, golden.reason)] = struct{}{}
			default:
				return auditErrorAt(ErrDeclaration, "semantics.fixtures")
			}
		default:
			return auditErrorAt(ErrDeclaration, "semantics.fixtures")
		}
	}
	if !start || !success {
		return auditErrorAt(ErrDeclaration, "semantics.fixtures")
	}
	for _, reasons := range finish.reasons {
		for _, reason := range reasons.codes {
			if _, ok := covered[attemptGoldenCoordinate(reasons.transition, reason)]; !ok {
				return auditErrorAt(ErrDeclaration, "semantics.fixtures")
			}
		}
	}
	return nil
}

func attemptGoldenCoordinate(transition AttemptTransitionKind, reason Reason) string {
	return string(rune(transition)) + "\x00" + string(reason)
}

func attemptOpenReserve(maxState uint64, target bool, start []FieldDescription, finish []AttemptFinishPhaseDescription) (uint64, error) {
	startBytes, ok := maximumAttemptPhaseBytes(start, target, false)
	if !ok {
		return 0, auditErrorAt(ErrTooLarge, "attempt.start")
	}
	var reserve uint64
	for _, phase := range finish {
		bytes, fits := maximumAttemptPhaseBytes(phase.Fields, false, phase.Transition != AttemptSucceededTransition)
		if !fits {
			return 0, auditErrorAt(ErrTooLarge, "attempt.finish")
		}
		if bytes > reserve {
			reserve = bytes
		}
	}
	if startBytes > MaxRevisionBytes || reserve > MaxRevisionBytes || startBytes > maxState || reserve > maxState-startBytes {
		return 0, auditErrorAt(ErrTooLarge, "attempt.max_state_bytes")
	}
	return reserve, nil
}

func maximumAttemptPhaseBytes(fields []FieldDescription, target, reason bool) (uint64, bool) {
	value := uint64(64 << 10)
	if target {
		value += uint64(MaxReferenceBytes + 1024)
	}
	if reason {
		value += uint64(MaxNameBytes + 128)
	}
	perField := uint64(MaxValueBytes + 4*MaxNameBytes + 1024)
	if uint64(len(fields)) > (^uint64(0)-value)/perField {
		return 0, false
	}
	return value + uint64(len(fields))*perField, true
}

func attemptDeclarationFingerprint(description DeclarationDescription, operation DeclarationDescription) AttemptPolicyFingerprint {
	candidate := cloneDeclarationDescription(description)
	candidate.Semantics.Fingerprint = PolicyFingerprint{}
	candidate.Attempt.Fingerprint = AttemptPolicyFingerprint{}
	candidate.Attempt.Replay = AttemptReplayFingerprint{}
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/attempt-policy/v1"))
	writeDeclarationDescription(digest, candidate)
	writeFrame(digest, []byte(operation.Operation))
	writeFrame(digest, operation.Semantics.Fingerprint[:])
	var output AttemptPolicyFingerprint
	copy(output[:], digest.Sum(nil))
	return output
}

func attemptActions() []Action {
	return []Action{
		AttemptStartedAction,
		AttemptCheckpointAction,
		AttemptOutcomeUnknownAction,
		AttemptSucceededAction,
		AttemptFailedAction,
		AttemptCancelledAction,
		AttemptAbandonedAction,
	}
}

func runOnlyFinishTransition(value AttemptTransitionKind) bool {
	return value == AttemptSucceededTransition || value == AttemptFailedTransition || value == AttemptCancelledTransition
}

func validAttemptOwnerFact(value AttemptOwnerFact) bool {
	return value == AttemptEffectiveActorOwner || value == AttemptWorkloadActorOwner || value == AttemptServiceOwner
}

func cloneAttemptTransitionFields[F any](input []attemptTransitionFields[F]) []attemptTransitionFields[F] {
	output := make([]attemptTransitionFields[F], len(input))
	for index, phase := range input {
		output[index] = phase
		output[index].fields = slices.Clone(phase.fields)
	}
	return output
}

func cloneAttemptReasonPolicies(input []attemptReasonPolicy) []attemptReasonPolicy {
	output := make([]attemptReasonPolicy, len(input))
	for index, policy := range input {
		output[index] = policy
		output[index].codes = slices.Clone(policy.codes)
	}
	return output
}

func cloneAttemptStart[S any](input attemptStartPolicy[S]) attemptStartPolicy[S] {
	input.fields = slices.Clone(input.fields)
	return input
}

func cloneAttemptFinish[F any](input attemptFinishPolicy[F]) attemptFinishPolicy[F] {
	input.phases = cloneAttemptTransitionFields(input.phases)
	input.reasons = cloneAttemptReasonPolicies(input.reasons)
	return input
}

func cloneAttemptContinuity(input attemptContinuityPolicy) attemptContinuityPolicy {
	input.facts = slices.Clone(input.facts)
	return input
}

func cloneAttemptSemantics(input PolicySemantics) PolicySemantics {
	input.value.goldens = slices.Clone(input.value.goldens)
	return input
}

func cloneAttemptDescriptor(input AttemptDescriptor) AttemptDescriptor {
	input.Context = cloneAttemptContext(input.Context)
	return input
}

func cloneAttemptContext(input ContextPolicy) ContextPolicy {
	facts := make([]contextFactPolicy, len(input.value.facts))
	for index, fact := range input.value.facts {
		facts[index] = fact
		facts[index].allowed = slices.Clone(fact.allowed)
	}
	return ContextPolicy{value: contextPolicy{facts: facts}}
}
