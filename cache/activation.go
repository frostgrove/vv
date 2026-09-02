package cache

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

type activationGate struct {
	committed atomic.Bool
}

type ActivationSpec struct {
	Application string
	Environment string
	Runtime     Runtime
	Sets        []Set
	Providers   []Provider
	Resources   []ResourceDeclaration

	RequireDeclaredResources bool
}

type ActivationError struct {
	problems []string
}

func (this *ActivationError) Error() string {
	if this == nil || len(this.problems) == 0 {
		return "cache: activation failed"
	}
	return "cache: activation failed: " + strings.Join(this.problems, "; ")
}

func (this *ActivationError) Unwrap() error { return ErrInvalid }

func (this *ActivationError) Problems() []string {
	if this == nil {
		return nil
	}
	return append([]string(nil), this.problems...)
}

type activationInput struct {
	application string
	environment string
	runtime     Runtime
	gate        *activationGate
}

type activationPlan struct {
	commit func()
}

type activatedNamespace struct {
	resource   ResourceID
	purpose    string
	generation Generation
}

func Activate(ctx context.Context, spec ActivationSpec) error {
	if nilInterface(ctx) {
		return failure("activate caches", fmt.Errorf("%w: context is nil", ErrInvalid))
	}
	if err := ctx.Err(); err != nil {
		return failure("activate caches", err)
	}
	problems := make([]string, 0)
	if validNamespacePart(spec.Application) != nil {
		problems = append(problems, "application is invalid")
	}
	if validNamespacePart(spec.Environment) != nil {
		problems = append(problems, "environment is invalid")
	}
	declarations := flattenDeclarations(spec.Sets)
	problems = append(problems, validateDeclarations(declarations)...)
	providers, providerProblems := indexProviders(spec.Providers)
	problems = append(problems, providerProblems...)
	domains, resourceProblems := indexResources(spec.Resources)
	problems = append(problems, resourceProblems...)
	if len(problems) > 0 {
		return newActivationError(problems)
	}
	gate := &activationGate{}
	input := activationInput{
		application: spec.Application,
		environment: spec.Environment,
		runtime:     spec.Runtime,
		gate:        gate,
	}
	plans := make([]activationPlan, 0, len(declarations))
	namespaces := make(map[activatedNamespace]string, len(declarations))
	cacheOwners := make(map[ResourceID]string, len(declarations))
	for _, declaration := range declarations {
		descriptor := declaration.Describe()
		provider, err := selectProvider(descriptor.ProviderKind, descriptor.ProviderID, providers)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", declaration.declarationName(), err))
			continue
		}
		if descriptor.ProviderKind != NoProviderKind {
			if _, taken := cacheOwners[provider.resourceIdentity()]; !taken {
				cacheOwners[provider.resourceIdentity()] = declaration.declarationName()
			}
			namespace := activatedNamespace{
				resource:   provider.resourceIdentity(),
				purpose:    descriptor.Purpose,
				generation: descriptor.Generation,
			}
			if owner, exists := namespaces[namespace]; exists {
				problems = append(problems, fmt.Sprintf("%s: shares a physical namespace with %s on resource %q", declaration.declarationName(), owner, namespace.resource))
				continue
			}
			namespaces[namespace] = declaration.declarationName()
		}
		plan, err := declaration.prepareActivation(input, provider)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", declaration.declarationName(), err))
			continue
		}
		plans = append(plans, plan)
	}
	if spec.RequireDeclaredResources {
		problems = append(problems, undeclaredResourceProblems(domains, cacheOwners)...)
	}
	problems = append(problems, evictionDomainProblems(domains, cacheOwners)...)
	if len(problems) > 0 {
		return newActivationError(problems)
	}
	if err := ctx.Err(); err != nil {
		return failure("activate caches", err)
	}
	for _, declaration := range declarations {
		declaration.lockActivation()
	}
	defer func() {
		for index := len(declarations) - 1; index >= 0; index-- {
			declarations[index].unlockActivation()
		}
	}()
	for _, declaration := range declarations {
		if declaration.isActivated() {
			problems = append(problems, fmt.Sprintf("%s: cache is already activated", declaration.declarationName()))
		}
	}
	if len(problems) > 0 {
		return newActivationError(problems)
	}
	if err := ctx.Err(); err != nil {
		return failure("activate caches", err)
	}
	for _, plan := range plans {
		plan.commit()
	}
	gate.committed.Store(true)
	return nil
}

func flattenDeclarations(sets []Set) []Declaration {
	count := 0
	for _, set := range sets {
		count += len(set.declarations)
	}
	result := make([]Declaration, 0, count)
	for _, set := range sets {
		result = append(result, set.declarations...)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].declarationName() < result[right].declarationName()
	})
	return result
}

func validateDeclarations(declarations []Declaration) []string {
	problems := make([]string, 0)
	names := make(map[string]struct{}, len(declarations))
	targets := make(map[any]struct{}, len(declarations))
	for index, declaration := range declarations {
		if nilInterface(declaration) {
			problems = append(problems, fmt.Sprintf("declaration %d is nil", index))
			continue
		}
		name := declaration.declarationName()
		if _, exists := names[name]; exists {
			problems = append(problems, fmt.Sprintf("duplicate cache name %q", name))
		}
		key := declaration.declarationKey()
		if _, exists := targets[key]; exists {
			problems = append(problems, fmt.Sprintf("%s: duplicate cache target", name))
		}
		names[name] = struct{}{}
		targets[key] = struct{}{}
	}
	return problems
}

func indexProviders(values []Provider) (map[ProviderID]Provider, []string) {
	providers := make(map[ProviderID]Provider, len(values))
	problems := make([]string, 0)
	for index, provider := range values {
		if err := validProvider(provider); err != nil {
			problems = append(problems, fmt.Sprintf("provider %d is invalid", index))
			continue
		}
		if _, exists := providers[provider.ID]; exists {
			problems = append(problems, fmt.Sprintf("duplicate provider id %q", provider.ID))
			continue
		}
		providers[provider.ID] = provider
	}
	return providers, problems
}

func selectProvider(kind ProviderKind, requested ProviderID, providers map[ProviderID]Provider) (Provider, error) {
	if kind == NoProviderKind {
		if requested != "" {
			return Provider{}, fmt.Errorf("%w: disabled cache selected a provider", ErrInvalid)
		}
		return Provider{}, nil
	}
	if requested != "" {
		provider, ok := providers[requested]
		if !ok {
			return Provider{}, fmt.Errorf("%w: provider %q is missing", ErrInvalid, requested)
		}
		return provider, nil
	}
	preferred := make([]Provider, 0)
	fallbacks := make([]Provider, 0)
	for _, provider := range providers {
		if provider.Kind != kind {
			continue
		}
		if provider.Fallback {
			fallbacks = append(fallbacks, provider)
		} else {
			preferred = append(preferred, provider)
		}
	}
	sort.Slice(preferred, func(left, right int) bool { return preferred[left].ID < preferred[right].ID })
	sort.Slice(fallbacks, func(left, right int) bool { return fallbacks[left].ID < fallbacks[right].ID })
	if len(preferred) == 1 {
		return preferred[0], nil
	}
	if len(preferred) > 1 {
		return Provider{}, fmt.Errorf("%w: provider kind %q is ambiguous", ErrInvalid, kind)
	}
	if len(fallbacks) == 1 {
		return fallbacks[0], nil
	}
	if len(fallbacks) > 1 {
		return Provider{}, fmt.Errorf("%w: fallback provider kind %q is ambiguous", ErrInvalid, kind)
	}
	return Provider{}, fmt.Errorf("%w: provider kind %q is missing", ErrInvalid, kind)
}

func newActivationError(problems []string) error {
	sorted := append([]string(nil), problems...)
	sort.Strings(sorted)
	return &ActivationError{problems: sorted}
}

func (this *Definition[K, V]) prepareActivation(input activationInput, provider Provider) (activationPlan, error) {
	if this == nil || this.target == nil || this.target.definition.Load() != this {
		return activationPlan{}, fmt.Errorf("%w: declaration target changed", ErrInvalid)
	}
	for _, capability := range this.spec.Requires {
		if !Supports(provider.Backend, capability) {
			return activationPlan{}, fmt.Errorf("%w: provider %q lacks capability %q", ErrInvalid, provider.ID, capability)
		}
	}
	namespace, err := NamespaceOf(input.application, input.environment, this.spec.Namespace.Purpose, this.spec.Namespace.Generation)
	if err != nil {
		return activationPlan{}, err
	}
	scope := this.spec.Scope.bind(namespace)
	var backend Backend
	if !this.policy.disabled {
		backend = provider.Backend
	}
	runtime := input.runtime
	if !this.policy.disabled {
		description, _ := BackendDescriptionOf(provider.Backend)
		if description.Topology == ProcessBackend {
			runtime.ClockSkew = SingleProcessClock()
		} else {
			runtime.ClockSkew = provider.ClockSkew
		}
	}
	prepared, err := newResolvedCache(runtime, backend, scope, this.spec.Keys, this.spec.Values, this.policy, this.transientPlan)
	if err != nil {
		return activationPlan{}, err
	}
	core := prepared.inner.Load()
	core.scope = scope
	core.keys = this.spec.Keys
	core.keyVersion = this.keyVersion
	core.values = this.spec.Values
	core.valueDescriptor = this.valueDescriptor
	core.name = this.spec.Name
	core.providerKind = NoProviderKind
	if !this.policy.disabled {
		core.providerKind = provider.Kind
	}
	core.providerID = provider.ID
	core.resourceID = provider.resourceIdentity()
	core.requires = append([]Capability(nil), this.spec.Requires...)
	core.activation = input.gate
	return activationPlan{commit: func() { this.target.inner.Store(core) }}, nil
}
