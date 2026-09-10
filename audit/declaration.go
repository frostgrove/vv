package audit

import (
	"bytes"
	"slices"
	"strings"
	"time"
)

type DeclarationKind uint8

const (
	ResourceDeclaration DeclarationKind = iota + 1
	EventDeclaration
	OperationDeclaration
	AttemptDeclaration
)

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

type AttemptOwnerFact uint8

const (
	AttemptEffectiveActorOwner AttemptOwnerFact = iota + 1
	AttemptWorkloadActorOwner
	AttemptServiceOwner
)

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
	Operation             OperationName
	Fingerprint           AttemptPolicyFingerprint
	MaxOpen               time.Duration
	MaxCheckpoints        uint16
	MaxStateBytes         uint64
	OpenReserveBytes      uint64
	UncertainReserveBytes uint64
	Continuity            []AttemptOwnerFact
	Start                 AttemptPhaseDescription
	Checkpoint            AttemptPhaseDescription
	CheckpointCodes       []AttemptCheckpointCode
	Finish                []AttemptFinishPhaseDescription
	Reasons               []AttemptReasonDescription
}

type DeclarationDescription struct {
	Kind           DeclarationKind
	Resource       Resource
	Action         Action
	Operation      OperationName
	Semantics      PolicySemanticsDescription
	Owner          Owner
	Purpose        Purpose
	Retention      RetentionClass
	Consequence    Consequence
	Subject        SubjectDescription
	TargetPresent  bool
	Target         SubjectDescription
	Actions        []EntityAction
	Outcomes       []Outcome
	Reasons        []Reason
	ReasonOptional bool
	Fields         []FieldDescription
	Context        ContextPolicyDescription
	Members        []string
	Attempt        AttemptDescription
}

type declarationSeal struct {
	description DeclarationDescription
	members     []declarationMember
}

type declarationMember struct {
	declaration Declaration
	resource    Resource
	action      Action
	operation   OperationName
	retention   RetentionClass
	consequence Consequence
	key         string
}

type compiledDeclaration interface {
	Declaration
	sealedDeclaration() *declarationSeal
}

type OperationMember interface {
	auditOperationMember()
}

type compiledOperationMember interface {
	OperationMember
	sealedOperationMember() declarationMember
}

type historyValue struct{}

type History struct {
	value historyValue
}

type ResourceHistory[M any, ID comparable] struct {
	history *History
	policy  *ResourcePolicy[M, ID]
}

type EventHistory[E any] struct {
	history *History
	event   *EventType[E]
}

type OperationHistory struct {
	history   *History
	operation *OperationType
}

func cloneDeclarationDescription(input DeclarationDescription) DeclarationDescription {
	output := input
	output.Semantics = clonePolicySemanticsDescription(input.Semantics)
	output.Actions = slices.Clone(input.Actions)
	output.Outcomes = slices.Clone(input.Outcomes)
	output.Reasons = slices.Clone(input.Reasons)
	output.Fields = cloneFieldDescriptions(input.Fields)
	output.Context = cloneContextPolicyDescription(input.Context)
	output.Members = slices.Clone(input.Members)
	output.Attempt = cloneAttemptDescription(input.Attempt)
	return output
}

func clonePolicySemanticsDescription(input PolicySemanticsDescription) PolicySemanticsDescription {
	output := input
	output.Fixtures = slices.Clone(input.Fixtures)
	return output
}

func cloneFieldDescriptions(input []FieldDescription) []FieldDescription {
	output := make([]FieldDescription, len(input))
	for index, field := range input {
		output[index] = field
		output[index].Codec.ReadVersions = slices.Clone(field.Codec.ReadVersions)
	}
	return output
}

func cloneContextPolicyDescription(input ContextPolicyDescription) ContextPolicyDescription {
	output := ContextPolicyDescription{Facts: make([]ContextFactDescription, len(input.Facts))}
	for index, fact := range input.Facts {
		output.Facts[index] = fact
		output.Facts[index].Allowed = slices.Clone(fact.Allowed)
	}
	return output
}

func cloneAttemptDescription(input AttemptDescription) AttemptDescription {
	output := input
	output.Continuity = slices.Clone(input.Continuity)
	output.Start.Fields = cloneFieldDescriptions(input.Start.Fields)
	output.Checkpoint.Fields = cloneFieldDescriptions(input.Checkpoint.Fields)
	output.CheckpointCodes = slices.Clone(input.CheckpointCodes)
	output.Finish = make([]AttemptFinishPhaseDescription, len(input.Finish))
	for index, phase := range input.Finish {
		output.Finish[index] = phase
		output.Finish[index].Fields = cloneFieldDescriptions(phase.Fields)
	}
	output.Reasons = make([]AttemptReasonDescription, len(input.Reasons))
	for index, reason := range input.Reasons {
		output.Reasons[index] = reason
		output.Reasons[index].Codes = slices.Clone(reason.Codes)
	}
	return output
}

func validateDescriptor(value Descriptor, event bool) error {
	if !validSemanticName(string(value.Resource)) {
		return auditErrorAt(ErrDeclaration, "descriptor.resource")
	}
	if event != (value.Action != "") || event && !validSemanticName(string(value.Action)) {
		return auditErrorAt(ErrDeclaration, "descriptor.action")
	}
	if !validSemanticName(string(value.Owner)) {
		return auditErrorAt(ErrDeclaration, "descriptor.owner")
	}
	if !validSemanticName(string(value.Purpose)) {
		return auditErrorAt(ErrDeclaration, "descriptor.purpose")
	}
	if !validSemanticName(string(value.Retention)) {
		return auditErrorAt(ErrDeclaration, "descriptor.retention")
	}
	if !value.Consequence.Valid() {
		return auditErrorAt(ErrDeclaration, "descriptor.consequence")
	}
	if !event && value.Consequence != Required {
		return auditErrorAt(ErrDeclaration, "descriptor.consequence")
	}
	return validateContextPolicy(value.Context)
}

func validateContextPolicy(value ContextPolicy) error {
	previous := ContextFactKind(0)
	for _, fact := range value.value.facts {
		if err := validateContextFactPolicy(fact); err != nil {
			return err
		}
		if fact.kind <= previous {
			return auditErrorAt(ErrDeclaration, "context.facts")
		}
		previous = fact.kind
	}
	return nil
}

func validatePolicySemantics(value PolicySemantics, extractor bool) error {
	if value.value.version == 0 {
		return auditErrorAt(ErrDeclaration, "semantics.version")
	}
	declarationGolden := false
	for _, golden := range value.value.goldens {
		if err := validateSemanticGolden(golden); err != nil {
			return err
		}
		if golden.kind != DeclarationPolicyFixture {
			return auditErrorAt(ErrDeclaration, "semantics.fixtures")
		}
		declarationGolden = true
	}
	if extractor && !declarationGolden {
		return auditErrorAt(ErrDeclaration, "semantics.fixtures")
	}
	return nil
}

func validateFieldPrivacy(classification Classification, mode StorageMode, secret bool) error {
	if !classification.Valid() || !mode.Valid() {
		return auditErrorAt(ErrDeclaration, "field.privacy")
	}
	if secret && mode != AsRedacted && mode != AsToken {
		return auditErrorAt(ErrDeclaration, "field.mode")
	}
	return nil
}

func validateSubjectPrivacy(classification Classification, mode StorageMode, secret bool) error {
	if !classification.Valid() || mode != AsPlaintext && mode != AsToken && mode != AsIndexedProtected {
		return auditErrorAt(ErrDeclaration, "subject.privacy")
	}
	if secret && mode != AsToken {
		return auditErrorAt(ErrDeclaration, "subject.mode")
	}
	return nil
}

func canonicalCodes[T ~string](values []T, path string) ([]T, error) {
	if len(values) == 0 {
		return nil, auditErrorAt(ErrDeclaration, path)
	}
	if len(values) > MaxCodesPerDeclaration {
		return nil, auditTooLarge(path, MaxCodesPerDeclaration)
	}
	output := slices.Clone(values)
	slices.Sort(output)
	for index, value := range output {
		if !validSemanticName(string(value)) || index > 0 && value == output[index-1] {
			return nil, auditErrorAt(ErrDeclaration, path)
		}
	}
	return output, nil
}

func compareFieldDescription(left, right FieldDescription) int {
	if value := strings.Compare(string(left.Name), string(right.Name)); value != 0 {
		return value
	}
	return strings.Compare(left.Source, right.Source)
}

func compareDeclarationDescription(left, right DeclarationDescription) int {
	if left.Kind != right.Kind {
		return int(left.Kind) - int(right.Kind)
	}
	if value := strings.Compare(string(left.Resource), string(right.Resource)); value != 0 {
		return value
	}
	if value := strings.Compare(string(left.Action), string(right.Action)); value != 0 {
		return value
	}
	return strings.Compare(string(left.Operation), string(right.Operation))
}

func declarationMemberKey(resource Resource, action Action) string {
	return "item:" + string(resource) + "\x00" + string(action)
}

func operationDeclarationKey(operation OperationName) string {
	return "operation:" + string(operation)
}

func samePolicyFingerprint(left, right PolicyFingerprint) bool {
	return bytes.Equal(left[:], right[:])
}
