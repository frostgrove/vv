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
	binding     BindingName
	concurrency int
	err         error
	valid       bool
}

type consumer[P any] struct {
	definition DefinitionOf[P]
	handler    Handler[P]
	options    []WorkerOption
}

type decodedConsumerValue[P any] struct{ value P }

func On[P any](definition DefinitionOf[P], handler Handler[P], options ...WorkerOption) Consumer {
	return &consumer[P]{definition: definition, handler: handler, options: append([]WorkerOption(nil), options...)}
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
		return consumerBinding{err: invalid("consumer")}
	}
	return typedConsumerBinding(c.definition, c.handler, c.options)
}

func typedConsumerBinding[P any](definition DefinitionOf[P], handler Handler[P], options []WorkerOption) consumerBinding {
	if nilInterface(definition) {
		return consumerBinding{err: invalid("consumer definition")}
	}
	if handler == nil {
		return consumerBinding{declaration: declarationOf(definition), err: invalid("consumer handler")}
	}
	declaration := declarationOf(definition)
	defaultConcurrency := 0
	if automatic, ok := any(definition).(interface{ defaultWorkerConcurrency() int }); ok {
		defaultConcurrency = automatic.defaultWorkerConcurrency()
	}
	binding, concurrency, err := resolveWorkerOptions(declaration, defaultConcurrency, options)
	if err != nil {
		return consumerBinding{declaration: declaration, err: err}
	}
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
		binding:     binding,
		concurrency: concurrency,
		valid:       declaration != nil && declaration.declarationName().valid(),
	}
}

func validateConsumers(catalog Catalog, consumers ...Consumer) error {
	_, err := NewWorkerPlan(catalog, consumers...)
	return err
}

func (this *Automatic[P]) Declaration() Declaration {
	if this == nil {
		return nil
	}
	return this
}

func (this *Automatic[P]) consumerBinding() consumerBinding {
	return typedConsumerBinding[P](this, this.Handler(), nil)
}

func (this *Automatic[P]) defaultWorkerConcurrency() int {
	if this == nil {
		return 0
	}
	return this.profile.workerConcurrency
}
