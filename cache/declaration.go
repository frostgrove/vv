package cache

import (
	"fmt"
	"sort"
)

type automaticDeclaration struct {
	profile Profile
}

type NamespaceTemplate struct {
	Purpose    string
	Generation Generation
}

type ScopePlan[K any] struct {
	mode      ScopeMode
	partition Partitioner[K]
}

func GlobalPlan[K any]() ScopePlan[K] {
	return ScopePlan[K]{mode: GlobalScopeMode}
}

func PartitionedPlan[K any](partition Partitioner[K]) ScopePlan[K] {
	return ScopePlan[K]{mode: PartitionedScopeMode, partition: partition}
}

func (this ScopePlan[K]) valid() bool {
	return this.mode == GlobalScopeMode || (this.mode == PartitionedScopeMode && this.partition != nil)
}

func (this ScopePlan[K]) bind(namespace Namespace) Scope[K] {
	if this.mode == GlobalScopeMode {
		return Global[K](namespace)
	}
	return Partitioned(namespace, this.partition)
}

type DefinitionSpec[K, V any] struct {
	Name      string
	Namespace NamespaceTemplate
	Scope     ScopePlan[K]
	Keys      KeyCodec[K]
	Values    Codec[V]
	Provider  ProviderID
	Requires  []Capability
}

type Definition[K, V any] struct {
	target          *Cache[K, V]
	spec            DefinitionSpec[K, V]
	profile         Profile
	policy          Policy
	keyVersion      KeyVersion
	valueDescriptor codecDescriptor
}

type Declaration interface {
	Describe() Descriptor
	declarationName() string
	declarationKey() any
	prepareActivation(activationInput, Provider) (activationPlan, error)
	lockActivation()
	unlockActivation()
	isActivated() bool
	declarationMarker()
}

type Set struct {
	declarations []Declaration
}

func Auto[K, V any](profiles ...Profile) *Cache[K, V] {
	if len(profiles) > 1 {
		panic(failure("declare cache", fmt.Errorf("%w: Auto accepts at most one profile", ErrInvalid)))
	}
	profile := Hot
	if len(profiles) == 1 {
		profile = profiles[0]
	}
	if _, err := profile.Build(); err != nil {
		panic(failure("declare cache", err))
	}
	target := &Cache[K, V]{}
	target.automatic.Store(&automaticDeclaration{profile: profile})
	return target
}

func Define[K, V any](target *Cache[K, V], spec DefinitionSpec[K, V]) (*Definition[K, V], error) {
	if target == nil || target.inner.Load() != nil {
		return nil, failure("define cache", fmt.Errorf("%w: target must be an inactive automatic cache", ErrInvalid))
	}
	automatic := target.automatic.Load()
	if automatic == nil {
		return nil, failure("define cache", fmt.Errorf("%w: target was not created by Auto", ErrInvalid))
	}
	policy, err := automatic.profile.Build()
	if err != nil {
		return nil, failure("define cache", err)
	}
	if validNamespacePart(spec.Name) != nil || validNamespacePart(spec.Namespace.Purpose) != nil || spec.Namespace.Generation == 0 || !spec.Scope.valid() {
		return nil, failure("define cache", fmt.Errorf("%w: name, namespace, generation, or scope is invalid", ErrInvalid))
	}
	if spec.Provider != "" && validNamespacePart(string(spec.Provider)) != nil {
		return nil, failure("define cache", fmt.Errorf("%w: provider id is invalid", ErrInvalid))
	}
	if policy.disabled && spec.Provider != "" {
		return nil, failure("define cache", fmt.Errorf("%w: disabled cache cannot select a provider", ErrInvalid))
	}
	requires, err := normalizeCapabilities(spec.Requires)
	if err != nil || (policy.disabled && len(requires) != 0) {
		return nil, failure("define cache", fmt.Errorf("%w: required capabilities are invalid", ErrInvalid))
	}
	spec.Requires = requires
	if nilInterface(spec.Keys) {
		return nil, failure("define cache", fmt.Errorf("%w: key codec is required", ErrInvalid))
	}
	keyVersion, err := describeKeyCodec(spec.Keys)
	if err != nil || keyVersion == 0 {
		return nil, failure("define cache", fmt.Errorf("%w: key codec is invalid", ErrInvalid))
	}
	valueDescriptor, err := describeCodec(spec.Values)
	if err != nil {
		return nil, failure("define cache", err)
	}
	definition := &Definition[K, V]{
		target:          target,
		spec:            spec,
		profile:         automatic.profile,
		policy:          policy,
		keyVersion:      keyVersion,
		valueDescriptor: valueDescriptor,
	}
	if !target.definition.CompareAndSwap(nil, definition) {
		return nil, failure("define cache", fmt.Errorf("%w: automatic cache is already defined", ErrInvalid))
	}
	return definition, nil
}

func MustDefine[K, V any](target *Cache[K, V], spec DefinitionSpec[K, V]) *Definition[K, V] {
	definition, err := Define(target, spec)
	if err != nil {
		panic(err)
	}
	return definition
}

func NewSet(declarations ...Declaration) (Set, error) {
	result := Set{declarations: append([]Declaration(nil), declarations...)}
	names := make(map[string]struct{}, len(result.declarations))
	targets := make(map[any]struct{}, len(result.declarations))
	for index, declaration := range result.declarations {
		if nilInterface(declaration) {
			return Set{}, failure("build cache set", fmt.Errorf("%w: declaration %d is nil", ErrInvalid, index))
		}
		name := declaration.declarationName()
		if _, exists := names[name]; exists {
			return Set{}, failure("build cache set", fmt.Errorf("%w: duplicate cache name %q", ErrInvalid, name))
		}
		key := declaration.declarationKey()
		if _, exists := targets[key]; exists {
			return Set{}, failure("build cache set", fmt.Errorf("%w: duplicate cache target", ErrInvalid))
		}
		names[name] = struct{}{}
		targets[key] = struct{}{}
	}
	sort.Slice(result.declarations, func(left, right int) bool {
		return result.declarations[left].declarationName() < result.declarations[right].declarationName()
	})
	return result, nil
}

func MustSet(declarations ...Declaration) Set {
	set, err := NewSet(declarations...)
	if err != nil {
		panic(err)
	}
	return set
}

func (this Set) Describe() []Descriptor {
	result := make([]Descriptor, len(this.declarations))
	for index, declaration := range this.declarations {
		result[index] = declaration.Describe()
	}
	return result
}

func (this *Definition[K, V]) Describe() Descriptor {
	if this == nil {
		return Descriptor{}
	}
	if core := this.target.inner.Load(); core != nil && (core.activation == nil || core.activation.committed.Load()) {
		return describeCore(core)
	}
	return this.declaredDescriptor()
}

func (this *Definition[K, V]) declaredDescriptor() Descriptor {
	return Descriptor{
		LogicalName:  this.spec.Name,
		Purpose:      this.spec.Namespace.Purpose,
		Generation:   this.spec.Namespace.Generation,
		Scope:        this.spec.Scope.mode,
		KeyVersion:   this.keyVersion,
		ValueCodec:   this.valueDescriptor.id,
		ValueSchema:  this.valueDescriptor.schema,
		Profile:      this.profile.name,
		ProviderKind: this.profile.provider,
		ProviderID:   this.spec.Provider,
		Requires:     append([]Capability(nil), this.spec.Requires...),
		Policy:       describePolicy(this.policy),
	}
}

func (this *Definition[K, V]) declarationName() string { return this.spec.Name }
func (this *Definition[K, V]) declarationKey() any     { return this.target }
func (this *Definition[K, V]) declarationMarker()      {}
func (this *Definition[K, V]) lockActivation()         { this.target.activationMu.Lock() }
func (this *Definition[K, V]) unlockActivation()       { this.target.activationMu.Unlock() }

func (this *Definition[K, V]) isActivated() bool {
	return this.target.inner.Load() != nil
}

func normalizeCapabilities(values []Capability) ([]Capability, error) {
	result := append([]Capability(nil), values...)
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	for index, capability := range result {
		if capability != BatchReadCapability || (index > 0 && capability == result[index-1]) {
			return nil, ErrInvalid
		}
	}
	return result, nil
}
