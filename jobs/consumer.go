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
	declaration   Declaration
	decode        func(EncodedPayload) (any, error)
	decodeOwned   func(EncodedPayload) (any, error)
	handle        func(context.Context, any) error
	handleAdapter func(context.Context, any, DeliveryMeta, ProgressReporter) error
	classifier    ErrorClassifier
	admission     AdmissionReader
	mode          consumerHandlerMode
	binding       BindingName
	concurrency   int
	err           error
	valid         bool
}

type consumerHandlerMode uint8

const (
	consumerHandlerStandard consumerHandlerMode = iota + 1
	consumerHandlerAdapter
)

func (mode consumerHandlerMode) valid() bool {
	return mode == consumerHandlerStandard || mode == consumerHandlerAdapter
}

type consumer[P any] struct {
	definition DefinitionOf[P]
	handler    Handler[P]
	options    []WorkerOption
}

type adapterConsumer[P any] struct {
	definition DefinitionOf[P]
	handler    AdapterHandler[P]
	options    []WorkerOption
}

type decodedConsumerValue[P any] struct{ value P }

func On[P any](definition DefinitionOf[P], handler Handler[P], options ...WorkerOption) Consumer {
	return &consumer[P]{definition: definition, handler: handler, options: append([]WorkerOption(nil), options...)}
}

func OnAdapter[P any](definition DefinitionOf[P], handler AdapterHandler[P], options ...WorkerOption) Consumer {
	return &adapterConsumer[P]{definition: definition, handler: handler, options: append([]WorkerOption(nil), options...)}
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

func (*adapterConsumer[P]) String() string { return "[job adapter consumer]" }
func (c *adapterConsumer[P]) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, c.String())
}

func (c *adapterConsumer[P]) Declaration() Declaration {
	if c == nil || nilInterface(c.definition) {
		return nil
	}
	return declarationOf(c.definition)
}

func (c *adapterConsumer[P]) consumerBinding() consumerBinding {
	if c == nil {
		return consumerBinding{err: invalid("adapter consumer")}
	}
	return typedAdapterConsumerBinding(c.definition, c.handler, c.options)
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
	resolved, err := resolveWorkerOptions(declaration, defaultConcurrency, options)
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
		decodeOwned: func(payload EncodedPayload) (any, error) {
			value, err := definition.decodeOwned(payload)
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
			return invokeHandlerContained(func() error { return handler(ctx, value.value) })
		},
		classifier:  resolved.classifier,
		admission:   resolved.admission,
		mode:        consumerHandlerStandard,
		binding:     resolved.binding,
		concurrency: resolved.concurrency,
		valid:       declaration != nil && declaration.declarationName().valid(),
	}
}

func typedAdapterConsumerBinding[P any](definition DefinitionOf[P], handler AdapterHandler[P], options []WorkerOption) consumerBinding {
	if nilInterface(definition) {
		return consumerBinding{err: invalid("adapter consumer definition")}
	}
	if handler == nil {
		return consumerBinding{declaration: declarationOf(definition), err: invalid("adapter consumer handler")}
	}
	declaration := declarationOf(definition)
	defaultConcurrency := 0
	if automatic, ok := any(definition).(interface{ defaultWorkerConcurrency() int }); ok {
		defaultConcurrency = automatic.defaultWorkerConcurrency()
	}
	resolved, err := resolveWorkerOptions(declaration, defaultConcurrency, options)
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
		decodeOwned: func(payload EncodedPayload) (any, error) {
			value, err := definition.decodeOwned(payload)
			if err != nil {
				return nil, err
			}
			return decodedConsumerValue[P]{value: value}, nil
		},
		handleAdapter: func(ctx context.Context, decoded any, meta DeliveryMeta, progress ProgressReporter) error {
			value, ok := decoded.(decodedConsumerValue[P])
			if !ok || !meta.valid() || meta.Definition() != declaration.declarationName() || meta.Binding() != resolved.binding || nilInterface(progress) {
				return fmt.Errorf("%w: adapter consumer input does not match", ErrInvalid)
			}
			return invokeHandlerContained(func() error { return handler(ctx, value.value, meta, progress) })
		},
		classifier:  resolved.classifier,
		admission:   resolved.admission,
		mode:        consumerHandlerAdapter,
		binding:     resolved.binding,
		concurrency: resolved.concurrency,
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
