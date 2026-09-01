package jobsfx

import (
	"fmt"

	"github.com/frostgrove/vv/jobs"
)

func (binding *Binding[D, P]) Wire(spec jobs.WireSpec[P]) *Binding[D, P] {
	if binding == nil {
		panic(fmt.Errorf("jobsfx: %w: binding is nil", jobs.ErrInvalid))
	}
	jobs.MustWire(binding.Automatic, spec)
	return binding
}

func (binding *Binding[D, P]) JSON(name string, version jobs.SchemaVersion) *Binding[D, P] {
	return binding.Wire(jobs.WireSpec[P]{Name: mustName(name), Codec: jobs.JSON[P](version)})
}

func (binding *Binding[D, P]) TrustedJSON(name string, version jobs.SchemaVersion) *Binding[D, P] {
	return binding.Wire(jobs.WireSpec[P]{Name: mustName(name), Codec: jobs.TrustedJSON[P](version)})
}

func mustName(raw string) jobs.Name {
	name, err := jobs.ParseName(raw)
	if err != nil {
		panic(fmt.Errorf("jobsfx: job name: %w", err))
	}
	return name
}

type Registry struct {
	catalog       jobs.Catalog
	registrations []Registration
}

func NewRegistry(registrations ...Registration) (Registry, error) {
	if len(registrations) == 0 {
		return Registry{}, fmt.Errorf("jobsfx: %w: registry requires at least one job", jobs.ErrInvalid)
	}
	values := append([]Registration(nil), registrations...)
	declarations := make([]jobs.Declaration, len(values))
	for index, registration := range values {
		if registration == nil {
			return Registry{}, fmt.Errorf("jobsfx: %w: registry job %d is nil", jobs.ErrInvalid, index)
		}
		declarations[index] = registration.Declaration()
	}
	catalog, err := jobs.NewCatalog(declarations...)
	if err != nil {
		return Registry{}, fmt.Errorf("jobsfx: registry: %w", err)
	}
	return Registry{catalog: catalog, registrations: values}, nil
}

func MustRegistry(registrations ...Registration) Registry {
	registry, err := NewRegistry(registrations...)
	if err != nil {
		panic(err)
	}
	return registry
}

func (registry Registry) Catalog() jobs.Catalog {
	return registry.catalog
}

func (registry Registry) Module(options ...BundleOption) Option {
	return Bundle(registry.catalog, options, registry.registrations...)
}
