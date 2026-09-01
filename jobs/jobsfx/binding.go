package jobsfx

import (
	"context"
	"fmt"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/jobs"
)

type Option = fx.Option

type Binding[D, P any] struct {
	*jobs.Automatic[P]
	handler        func(D, context.Context, P) error
	adapterHandler func(D, context.Context, P, jobs.DeliveryMeta, jobs.AttemptController) error
}

func Auto[D, P any](handler func(D, context.Context, P) error, profiles ...jobs.Profile) *Binding[D, P] {
	if handler == nil {
		panic(fmt.Errorf("jobsfx: %w: automatic job handler is required", jobs.ErrInvalid))
	}
	return &Binding[D, P]{Automatic: jobs.Declare[P](profiles...), handler: handler}
}

func AutoAdapter[D, P any](handler func(D, context.Context, P, jobs.DeliveryMeta, jobs.AttemptController) error, profiles ...jobs.Profile) *Binding[D, P] {
	if handler == nil {
		panic(fmt.Errorf("jobsfx: %w: automatic adapter handler is required", jobs.ErrInvalid))
	}
	return &Binding[D, P]{Automatic: jobs.Declare[P](profiles...), adapterHandler: handler}
}

func (binding *Binding[D, P]) Declaration() jobs.Declaration {
	if binding == nil || binding.Automatic == nil {
		return nil
	}
	return binding.Automatic
}

func (binding *Binding[D, P]) Go(ctx context.Context, payload P, options ...jobs.EnqueueOption) error {
	if binding == nil {
		return jobs.ErrNotActivated
	}
	return jobs.Go(ctx, binding.Automatic, payload, options...)
}

type Registration interface {
	Declaration() jobs.Declaration
	consumerConstructor([]jobs.WorkerOption) any
}

type HandlerOptions interface {
	JobOptions(jobs.Name) []jobs.WorkerOption
}

type BundleOption interface{ applyBundle(*bundleOptions) error }

type bundleOption func(*bundleOptions) error

func (option bundleOption) applyBundle(options *bundleOptions) error { return option(options) }

type bundleOptions struct {
	concurrency    map[jobs.Name]int
	concurrencySet bool
}

func Concurrency(overrides map[jobs.Name]int) BundleOption {
	values := make(map[jobs.Name]int, len(overrides))
	for name, value := range overrides {
		values[name] = value
	}
	return bundleOption(func(options *bundleOptions) error {
		if options.concurrencySet {
			return fmt.Errorf("jobsfx: %w: duplicate bundle concurrency", jobs.ErrInvalid)
		}
		for name, value := range values {
			if name.IsZero() || value < 1 || value > jobs.MaxBindingConcurrency {
				return fmt.Errorf("jobsfx: %w: bundle concurrency for %q", jobs.ErrInvalid, name)
			}
		}
		options.concurrency = values
		options.concurrencySet = true
		return nil
	})
}

func Bundle(catalog jobs.Catalog, optionValues []BundleOption, registrations ...Registration) fx.Option {
	options, err := resolveBundleOptions(optionValues)
	if err != nil {
		return fx.Error(err)
	}
	providers, err := bundleProviders(catalog, options, registrations)
	if err != nil {
		return fx.Error(err)
	}
	return fx.Options(providers...)
}

func resolveBundleOptions(values []BundleOption) (bundleOptions, error) {
	var options bundleOptions
	for index, value := range values {
		if value == nil {
			return bundleOptions{}, fmt.Errorf("jobsfx: %w: bundle option %d is nil", jobs.ErrInvalid, index)
		}
		if err := value.applyBundle(&options); err != nil {
			return bundleOptions{}, err
		}
	}
	return options, nil
}

func bundleProviders(catalog jobs.Catalog, options bundleOptions, registrations []Registration) ([]fx.Option, error) {
	if catalog.Len() == 0 || catalog.Fingerprint() == "" {
		return nil, fmt.Errorf("jobsfx: %w: bundle catalog is required", jobs.ErrInvalid)
	}
	registered := make(map[jobs.Declaration]Registration, len(registrations))
	providers := make([]fx.Option, 0, catalog.Len())
	for index, registration := range registrations {
		if registration == nil {
			return nil, fmt.Errorf("jobsfx: %w: bundle registration %d is nil", jobs.ErrInvalid, index)
		}
		declaration := registration.Declaration()
		if declaration == nil {
			return nil, fmt.Errorf("jobsfx: %w: bundle registration %d is unresolved", jobs.ErrInvalid, index)
		}
		member, ok := catalog.Lookup(declaration.Describe().Name)
		if !ok || member != declaration {
			return nil, fmt.Errorf("jobsfx: %w: registration %q is not an exact catalog member", jobs.ErrInvalid, declaration.Describe().Name)
		}
		if _, exists := registered[declaration]; exists {
			return nil, fmt.Errorf("jobsfx: %w: duplicate registration %q", jobs.ErrConflict, declaration.Describe().Name)
		}
		workerOptions := []jobs.WorkerOption(nil)
		if concurrency, ok := options.concurrency[declaration.Describe().Name]; ok {
			workerOptions = append(workerOptions, jobs.Concurrency(concurrency))
		}
		registered[declaration] = registration
		providers = append(providers, fx.Provide(AsConsumer(registration.consumerConstructor(workerOptions))))
	}
	for name := range options.concurrency {
		declaration, ok := catalog.Lookup(name)
		if !ok || registered[declaration] == nil {
			return nil, fmt.Errorf("jobsfx: %w: concurrency override %q has no injected handler", jobs.ErrInvalid, name)
		}
	}
	for _, declaration := range catalog.Definitions() {
		if registered[declaration] != nil {
			continue
		}
		value := declaration
		providers = append(providers, fx.Provide(AsDeclaration(func() jobs.Declaration { return value })))
	}
	return providers, nil
}

func (binding *Binding[D, P]) consumerConstructor(options []jobs.WorkerOption) any {
	configured := func(dependency D) []jobs.WorkerOption {
		values := append([]jobs.WorkerOption(nil), options...)
		if provider, ok := any(dependency).(HandlerOptions); ok {
			values = append(values, provider.JobOptions(binding.Name())...)
		}
		return values
	}
	if binding.adapterHandler != nil {
		return func(dependency D) jobs.Consumer {
			return jobs.OnAdapter[P](binding, func(ctx context.Context, payload P, meta jobs.DeliveryMeta, controller jobs.AttemptController) error {
				return binding.adapterHandler(dependency, ctx, payload, meta, controller)
			}, configured(dependency)...)
		}
	}
	return func(dependency D) jobs.Consumer {
		return jobs.On[P](binding, func(ctx context.Context, payload P) error {
			return binding.handler(dependency, ctx, payload)
		}, configured(dependency)...)
	}
}
