package jobs

import (
	"context"
	"fmt"
)

type Handler[P any] func(context.Context, P) error

type Consumer interface {
	Declaration() Declaration
	consumerBinding() consumerBinding
}

type consumerBinding struct {
	declaration Declaration
	decode      func(EncodedPayload) (any, error)
	handle      func(context.Context, any) error
	valid       bool
}

type consumer[P any] struct {
	definition DefinitionOf[P]
	handler    Handler[P]
}

type decodedConsumerValue[P any] struct{ value P }

func On[P any](definition DefinitionOf[P], handler Handler[P]) Consumer {
	return &consumer[P]{definition: definition, handler: handler}
}

func (*consumer[P]) String() string { return "[job consumer]" }
func (c *consumer[P]) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, c.String())
}

func (c *consumer[P]) Declaration() Declaration {
	if c == nil || nilInterface(c.definition) {
		return nil
	}
	return declarationOf(c.definition)
}

func (c *consumer[P]) consumerBinding() consumerBinding {
	if c == nil {
		return consumerBinding{}
	}
	return typedConsumerBinding(c.definition, c.handler)
}

func typedConsumerBinding[P any](definition DefinitionOf[P], handler Handler[P]) consumerBinding {
	if nilInterface(definition) || handler == nil {
		return consumerBinding{}
	}
	declaration := declarationOf(definition)
	return consumerBinding{
		declaration: declaration,
		decode: func(payload EncodedPayload) (any, error) {
			value, err := definition.Decode(payload)
			if err != nil {
				return nil, err
			}
			return decodedConsumerValue[P]{value: value}, nil
		},
		handle: func(ctx context.Context, decoded any) error {
			value, ok := decoded.(decodedConsumerValue[P])
			if !ok {
				return fmt.Errorf("%w: consumer payload type does not match", ErrInvalid)
			}
			return handler(ctx, value.value)
		},
		valid: declaration != nil && declaration.declarationName().valid(),
	}
}

func validateConsumers(catalog Catalog, consumers ...Consumer) error {
	if catalog.Len() == 0 || catalog.Fingerprint() == "" {
		return fmt.Errorf("%w: consumer catalog is invalid", ErrInvalid)
	}
	seen := make(map[Declaration]struct{}, len(consumers))
	for index, consumer := range consumers {
		if nilInterface(consumer) {
			return fmt.Errorf("%w: consumer %d is nil", ErrInvalid, index)
		}
		binding := consumer.consumerBinding()
		if !binding.valid || nilInterface(binding.declaration) {
			return fmt.Errorf("%w: consumer %d is invalid or unresolved", ErrInvalid, index)
		}
		registered, ok := catalog.Lookup(binding.declaration.declarationName())
		if !ok || registered != binding.declaration {
			return fmt.Errorf("%w: consumer %d definition is not an exact catalog member", ErrInvalid, index)
		}
		if _, exists := seen[binding.declaration]; exists {
			return fmt.Errorf("%w: duplicate consumer definition %q", ErrConflict, binding.declaration.declarationName())
		}
		seen[binding.declaration] = struct{}{}
	}
	return nil
}

func (this *Automatic[P]) Declaration() Declaration {
	if this == nil {
		return nil
	}
	return this
}

func (this *Automatic[P]) consumerBinding() consumerBinding {
	return typedConsumerBinding[P](this, this.Handler())
}
