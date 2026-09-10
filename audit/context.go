package audit

import (
	"context"
	"fmt"
	"slices"
)

type Actor struct {
	Kind       ActorKind
	Reference  Reference
	Provenance Provenance
}

type contextValue[T any] struct {
	value      T
	provenance Provenance
	present    bool
}

type ContextValue[T any] struct {
	value contextValue[T]
}

func NewContextValue[T any](value T, provenance Provenance) (ContextValue[T], error) {
	if !validProvenance(provenance) {
		return ContextValue[T]{}, fmt.Errorf("%w: context provenance is invalid", ErrInvalid)
	}
	if err := validateContextCoordinate(value); err != nil {
		return ContextValue[T]{}, err
	}
	return ContextValue[T]{value: contextValue[T]{value: value, provenance: provenance, present: true}}, nil
}

func (v ContextValue[T]) Get() (T, bool) {
	return v.value.value, v.value.present
}

func (v ContextValue[T]) Provenance() Provenance {
	if !v.value.present {
		return UnstatedProvenance
	}
	return v.value.provenance
}

type Context struct {
	Actors      []Actor
	Scope       ContextValue[ScopedReference]
	Service     ContextValue[Reference]
	Deployment  ContextValue[Reference]
	Client      ContextValue[ScopedReference]
	Operation   ContextValue[OperationID]
	Correlation ContextValue[Reference]
	Causation   ContextValue[Reference]
	Trace       ContextValue[Reference]
	Source      ContextValue[Source]
}

type ContextView struct {
	Actors      []Actor
	Scope       ContextValue[ScopedReference]
	Service     ContextValue[Reference]
	Deployment  ContextValue[Reference]
	Client      ContextValue[ScopedReference]
	Operation   ContextValue[OperationID]
	Correlation ContextValue[Reference]
	Causation   ContextValue[Reference]
	Trace       ContextValue[Reference]
	Source      ContextValue[Source]
}

func (c Context) View() ContextView {
	return ContextView{
		Actors:      slices.Clone(c.Actors),
		Scope:       c.Scope,
		Service:     c.Service,
		Deployment:  c.Deployment,
		Client:      c.Client,
		Operation:   c.Operation,
		Correlation: c.Correlation,
		Causation:   c.Causation,
		Trace:       c.Trace,
		Source:      c.Source,
	}
}

type ContextResolver interface {
	ResolveAuditContext(context.Context) (Context, error)
}

type ContextResolverFunc func(context.Context) (Context, error)

func (f ContextResolverFunc) ResolveAuditContext(ctx context.Context) (Context, error) {
	if f == nil {
		return Context{}, fmt.Errorf("%w: context resolver is nil", ErrInvalid)
	}
	return f(ctx)
}

type staticContextResolver struct {
	value Context
}

func StaticContext(value Context) (ContextResolver, error) {
	copy, err := validateAndCopyContext(value)
	if err != nil {
		return nil, err
	}
	return staticContextResolver{value: copy}, nil
}

func (s staticContextResolver) ResolveAuditContext(context.Context) (Context, error) {
	value, _ := validateAndCopyContext(s.value)
	return value, nil
}

func validateAndCopyContext(value Context) (Context, error) {
	if len(value.Actors) > MaxActorHops {
		return Context{}, fmt.Errorf("%w: actor chain exceeds %d hops", ErrTooLarge, MaxActorHops)
	}
	copy := value
	copy.Actors = slices.Clone(value.Actors)
	for _, actor := range copy.Actors {
		if !validActorKind(actor.Kind) || !validProvenance(actor.Provenance) || !validReferenceText(string(actor.Reference)) {
			return Context{}, fmt.Errorf("%w: actor is invalid", ErrInvalid)
		}
	}
	if err := validateContextValue(copy.Scope); err != nil {
		return Context{}, err
	}
	if err := validateContextValue(copy.Service); err != nil {
		return Context{}, err
	}
	if err := validateContextValue(copy.Deployment); err != nil {
		return Context{}, err
	}
	if err := validateContextValue(copy.Client); err != nil {
		return Context{}, err
	}
	if err := validateContextValue(copy.Operation); err != nil {
		return Context{}, err
	}
	if err := validateContextValue(copy.Correlation); err != nil {
		return Context{}, err
	}
	if err := validateContextValue(copy.Causation); err != nil {
		return Context{}, err
	}
	if err := validateContextValue(copy.Trace); err != nil {
		return Context{}, err
	}
	if err := validateContextValue(copy.Source); err != nil {
		return Context{}, err
	}
	return copy, nil
}

func validateContextValue[T any](value ContextValue[T]) error {
	coordinate, present := value.Get()
	if !present {
		if value.Provenance() != UnstatedProvenance {
			return fmt.Errorf("%w: absent context value has provenance", ErrInvalid)
		}
		return nil
	}
	if !validProvenance(value.Provenance()) {
		return fmt.Errorf("%w: context provenance is invalid", ErrInvalid)
	}
	return validateContextCoordinate(coordinate)
}

func validateContextCoordinate(value any) error {
	switch coordinate := value.(type) {
	case Reference:
		if !validReferenceText(string(coordinate)) {
			return fmt.Errorf("%w: context reference is invalid", ErrInvalid)
		}
	case ScopedReference:
		if !validReferenceText(string(coordinate.Scope)) || !validReferenceText(string(coordinate.Reference)) {
			return fmt.Errorf("%w: scoped context reference is invalid", ErrInvalid)
		}
	case OperationID:
		if coordinate == (OperationID{}) {
			return fmt.Errorf("%w: operation identity is zero", ErrInvalid)
		}
	case Source:
		if !validReferenceText(string(coordinate)) {
			return fmt.Errorf("%w: context source is invalid", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported context coordinate type", ErrInvalid)
	}
	return nil
}

func validProvenance(value Provenance) bool {
	return value == ServerDerived || value == Verified || value == Forwarded || value == ClientSupplied
}

func validActorKind(value ActorKind) bool {
	return value == HumanActor || value == WorkloadActor || value == ServiceActor || value == SystemActor || value == ExternalActor
}
