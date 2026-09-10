package audit

import (
	"fmt"
	"slices"
)

type provenanceSet struct {
	values []Provenance
}

type ProvenanceSet struct {
	value provenanceSet
}

type contextFactPolicy struct {
	kind           ContextFactKind
	presence       ContextPresence
	allowed        []Provenance
	classification Classification
	mode           StorageMode
	generated      bool
}

type ContextFactPolicy struct {
	value contextFactPolicy
}

type contextPolicy struct {
	facts []contextFactPolicy
}

type ContextPolicy struct {
	value contextPolicy
}

type ContextFactDescription struct {
	Kind           ContextFactKind
	Presence       ContextPresence
	Allowed        []Provenance
	Classification Classification
	Mode           StorageMode
	Generated      bool
}

type ContextPolicyDescription struct {
	Facts []ContextFactDescription
}

func Provenances(values ...Provenance) ProvenanceSet {
	copy := slices.Clone(values)
	slices.Sort(copy)
	for index, value := range copy {
		if !validProvenance(value) || index > 0 && value == copy[index-1] {
			panic(fmt.Errorf("%w: provenance set is invalid", ErrDeclaration))
		}
	}
	return ProvenanceSet{value: provenanceSet{values: copy}}
}

func ContextFacts(facts ...ContextFactPolicy) ContextPolicy {
	policy, err := TryContextFacts(facts...)
	if err != nil {
		panic(err)
	}
	return policy
}

func TryContextFacts(facts ...ContextFactPolicy) (ContextPolicy, error) {
	if len(facts) > int(SourceContext) {
		return ContextPolicy{}, fmt.Errorf("%w: context policy has too many facts", ErrDeclaration)
	}
	copy := make([]contextFactPolicy, len(facts))
	seen := make(map[ContextFactKind]struct{}, len(facts))
	for index, fact := range facts {
		value := fact.value
		if err := validateContextFactPolicy(value); err != nil {
			return ContextPolicy{}, err
		}
		if _, duplicate := seen[value.kind]; duplicate {
			return ContextPolicy{}, fmt.Errorf("%w: duplicate context fact", ErrDeclaration)
		}
		seen[value.kind] = struct{}{}
		value.allowed = slices.Clone(value.allowed)
		copy[index] = value
	}
	slices.SortFunc(copy, func(left, right contextFactPolicy) int { return int(left.kind) - int(right.kind) })
	return ContextPolicy{value: contextPolicy{facts: copy}}, nil
}

func ActorChain(presence ContextPresence, allowed ProvenanceSet, classification Classification, mode StorageMode) ContextFactPolicy {
	return contextFact(ActorChainContext, presence, allowed, classification, mode)
}

func ScopeFact(presence ContextPresence, allowed ProvenanceSet, classification Classification, mode StorageMode) ContextFactPolicy {
	return contextFact(ScopeContext, presence, allowed, classification, mode)
}

func ServiceFact(presence ContextPresence, allowed ProvenanceSet, classification Classification, mode StorageMode) ContextFactPolicy {
	return contextFact(ServiceContext, presence, allowed, classification, mode)
}

func DeploymentFact(presence ContextPresence, allowed ProvenanceSet, classification Classification, mode StorageMode) ContextFactPolicy {
	return contextFact(DeploymentContext, presence, allowed, classification, mode)
}

func ClientFact(presence ContextPresence, allowed ProvenanceSet, classification Classification, mode StorageMode) ContextFactPolicy {
	return contextFact(ClientContext, presence, allowed, classification, mode)
}

func OperationFact(presence ContextPresence, allowed ProvenanceSet, classification Classification, mode StorageMode) ContextFactPolicy {
	return contextFact(OperationContext, presence, allowed, classification, mode)
}

func CorrelationFact(presence ContextPresence, allowed ProvenanceSet, classification Classification, mode StorageMode) ContextFactPolicy {
	return contextFact(CorrelationContext, presence, allowed, classification, mode)
}

func CausationFact(presence ContextPresence, allowed ProvenanceSet, classification Classification, mode StorageMode) ContextFactPolicy {
	return contextFact(CausationContext, presence, allowed, classification, mode)
}

func TraceFact(presence ContextPresence, allowed ProvenanceSet, classification Classification, mode StorageMode) ContextFactPolicy {
	return contextFact(TraceContext, presence, allowed, classification, mode)
}

func SourceFact(presence ContextPresence, allowed ProvenanceSet, classification Classification, mode StorageMode) ContextFactPolicy {
	return contextFact(SourceContext, presence, allowed, classification, mode)
}

func GeneratedOperationFact(classification Classification, mode StorageMode) ContextFactPolicy {
	return ContextFactPolicy{value: contextFactPolicy{
		kind:           OperationContext,
		presence:       ContextRequired,
		allowed:        []Provenance{ServerDerived},
		classification: classification,
		mode:           mode,
		generated:      true,
	}}
}

func contextFact(kind ContextFactKind, presence ContextPresence, allowed ProvenanceSet, classification Classification, mode StorageMode) ContextFactPolicy {
	return ContextFactPolicy{value: contextFactPolicy{
		kind:           kind,
		presence:       presence,
		allowed:        slices.Clone(allowed.value.values),
		classification: classification,
		mode:           mode,
	}}
}

func validateContextFactPolicy(value contextFactPolicy) error {
	if value.kind < ActorChainContext || value.kind > SourceContext {
		return fmt.Errorf("%w: context fact kind is invalid", ErrDeclaration)
	}
	if value.presence != ContextRequired && value.presence != ContextOptional {
		return fmt.Errorf("%w: context presence is invalid", ErrDeclaration)
	}
	if !validClassification(value.classification) || !validStorageMode(value.mode) {
		return fmt.Errorf("%w: context privacy policy is invalid", ErrDeclaration)
	}
	if len(value.allowed) == 0 {
		return fmt.Errorf("%w: context fact has no allowed provenance", ErrDeclaration)
	}
	for index, provenance := range value.allowed {
		if !validProvenance(provenance) || index > 0 && provenance <= value.allowed[index-1] {
			return fmt.Errorf("%w: context provenance set is invalid", ErrDeclaration)
		}
	}
	if value.generated && (value.kind != OperationContext || value.presence != ContextRequired || len(value.allowed) != 1 || value.allowed[0] != ServerDerived) {
		return fmt.Errorf("%w: generated context fact is invalid", ErrDeclaration)
	}
	return nil
}

func (p ContextPolicy) description() ContextPolicyDescription {
	description := ContextPolicyDescription{Facts: make([]ContextFactDescription, len(p.value.facts))}
	for index, fact := range p.value.facts {
		description.Facts[index] = ContextFactDescription{
			Kind:           fact.kind,
			Presence:       fact.presence,
			Allowed:        slices.Clone(fact.allowed),
			Classification: fact.classification,
			Mode:           fact.mode,
			Generated:      fact.generated,
		}
	}
	return description
}

func validClassification(value Classification) bool {
	return value == Public || value == Internal || value == Personal || value == Secret
}

func validStorageMode(value StorageMode) bool {
	return value == AsPlaintext || value == AsRedacted || value == AsToken || value == AsProtected || value == AsIndexedProtected
}
