package appfx

import (
	"go.uber.org/fx"

	"github.com/frostgrove/vv/app/module"
)

// Option is the whole binding: a module's definition names constructors, the
// profile says which of them this process runs, and fx is handed the result.
// Nothing is discovered and nothing is called — the definition is the same
// value a doctor described before the graph existed.
func Option(definition module.Definition, profile module.Profile) fx.Option {
	if err := profile.Check(); err != nil {
		return fx.Error(err)
	}
	active := definition.Active(profile)
	if len(active) == 0 {
		return fx.Options()
	}
	return fx.Module(definition.Name(), fx.Provide(active...))
}

func Options(catalog module.Catalog, profile module.Profile) fx.Option {
	if err := catalog.Check(profile); err != nil {
		return fx.Error(err)
	}
	definitions := catalog.Definitions()
	options := make([]fx.Option, 0, len(definitions))
	for _, definition := range definitions {
		options = append(options, Option(definition, profile))
	}
	return fx.Options(options...)
}

func Auto(catalog module.Catalog) fx.Option { return Options(catalog, module.Complete) }
