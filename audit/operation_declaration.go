package audit

import (
	"slices"
	"strings"
)

type OperationPolicy struct {
	Name        OperationName
	Semantics   PolicySemantics
	Retention   RetentionClass
	Consequence Consequence
	Context     ContextPolicy
	Members     []OperationMember
}

type operationType struct {
	seal    declarationSeal
	members []declarationMember
}

type OperationType struct {
	value *operationType
}

func OperationMembers(members ...OperationMember) []OperationMember {
	return slices.Clone(members)
}

func DeclareOperation(policy OperationPolicy) *OperationType {
	operation, err := TryDeclareOperation(policy)
	if err != nil {
		panic(err)
	}
	return operation
}

func TryDeclareOperation(policy OperationPolicy) (*OperationType, error) {
	if !validSemanticName(string(policy.Name)) {
		return nil, auditErrorAt(ErrDeclaration, "operation.name")
	}
	if err := validatePolicySemantics(policy.Semantics, false); err != nil {
		return nil, err
	}
	if !validSemanticName(string(policy.Retention)) {
		return nil, auditErrorAt(ErrDeclaration, "operation.retention")
	}
	if !policy.Consequence.Valid() {
		return nil, auditErrorAt(ErrDeclaration, "operation.consequence")
	}
	if err := validateContextPolicy(policy.Context); err != nil {
		return nil, err
	}
	if len(policy.Members) == 0 {
		return nil, auditErrorAt(ErrDeclaration, "operation.members")
	}
	if len(policy.Members) > MaxOperationMembers {
		return nil, auditTooLarge("operation.members", MaxOperationMembers)
	}
	members := make([]declarationMember, len(policy.Members))
	seen := make(map[string]struct{}, len(policy.Members))
	for index, member := range policy.Members {
		if nilByReflection(member) {
			return nil, auditErrorAt(ErrDeclaration, "operation.members")
		}
		compiled, ok := member.(compiledOperationMember)
		if !ok {
			return nil, auditErrorAt(ErrDeclaration, "operation.members")
		}
		value := compiled.sealedOperationMember()
		if value.declaration == nil || value.key == "" || value.resource == "" || value.action == "" {
			return nil, auditErrorAt(ErrDeclaration, "operation.members")
		}
		if value.retention != policy.Retention || value.consequence != policy.Consequence {
			return nil, auditErrorAt(ErrDeclaration, "operation.members")
		}
		if _, duplicate := seen[value.key]; duplicate {
			return nil, auditErrorAt(ErrDeclaration, "operation.members")
		}
		seen[value.key] = struct{}{}
		members[index] = value
	}
	slices.SortFunc(members, func(left, right declarationMember) int { return strings.Compare(left.key, right.key) })
	memberNames := make([]string, len(members))
	for index, member := range members {
		memberNames[index] = member.key
	}
	description := DeclarationDescription{
		Kind: OperationDeclaration, Operation: policy.Name, Retention: policy.Retention,
		Consequence: policy.Consequence, Context: policy.Context.description(), Members: memberNames,
	}
	description.Semantics = policy.Semantics.description(PolicyFingerprint{})
	description.Semantics.Fingerprint = policyFingerprint(description, nil)
	operation := &OperationType{value: &operationType{members: members}}
	operation.value.seal.description = description
	operation.value.seal.operationMembers = slices.Clone(members)
	operation.value.seal.members = []declarationMember{{
		declaration: operation, operation: policy.Name, retention: policy.Retention,
		consequence: policy.Consequence, key: operationDeclarationKey(policy.Name),
	}}
	return operation, nil
}

func (o *OperationType) auditDeclaration() {}

func (o *OperationType) sealedDeclaration() *declarationSeal {
	if o == nil || o.value == nil {
		return nil
	}
	return &o.value.seal
}

func (o *OperationType) Description() DeclarationDescription {
	if o == nil || o.value == nil {
		return DeclarationDescription{}
	}
	return cloneDeclarationDescription(o.value.seal.description)
}

func (o *OperationType) History(history *History) *OperationHistory {
	if o == nil || o.value == nil || history == nil {
		return nil
	}
	return &OperationHistory{history: history, operation: o}
}
