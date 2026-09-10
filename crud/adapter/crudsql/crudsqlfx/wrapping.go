package crudsqlfx

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/crud"
)

const (
	wrappingGroupName = "vv.crud.source.wrappings"
	wrappingGroup     = `group:"` + wrappingGroupName + `"`

	baseName = "vv.crudsql.base"
	baseTag  = `name:"` + baseName + `"`
)

var (
	ErrWrappingUnnamed    = errors.New("crudsqlfx: a wrapping without a name")
	ErrWrappingEmpty      = errors.New("crudsqlfx: a wrapping without a function")
	ErrWrappingTwice      = errors.New("crudsqlfx: one name twice")
	ErrWrappingUndeclared = errors.New("crudsqlfx: a wrapping this deployment did not declare")
	ErrWrappingMissing    = errors.New("crudsqlfx: a declared wrapping nobody contributed")
	ErrWrappingDropped    = errors.New("crudsqlfx: a wrapping answered with no source")
)

// A Wrapping is one layer around the source this module wires — telemetry, a
// slow-query log, a replica router. It carries no place of its own: where it
// goes is Layers, which the deployment writes, because the composition root is
// the only thing that knows what a second layer is and which of the two belongs
// outside. What a layer answers with is bound by [[D-061]]: a wrapper answers
// for every capability it wrapped, and one that hides Begin takes transactions
// with it.
type Wrapping struct {
	Name string
	Wrap func(crud.Source) crud.Source
}

func AsWrapping(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(wrappingGroup))
}

// Base is the source this module would have wired, supplied by the graph
// instead. A harness that cannot reach a database replaces this rather than the
// source itself, and every declared layer still goes around what it supplied.
func Base(source crud.Source) fx.Option {
	return fx.Replace(fx.Annotate(source, fx.As(new(crud.Source)), fx.ResultTags(baseTag)))
}

type Option func(*declaration)

type declaration struct{ layers []string }

// Layers is the chain around this deployment's source, outside in: the first
// name sees a call first and its result last. A name here that nobody
// contributes and a contribution nobody names here are both refusals, so a
// layer that was meant to be there and is not stops the start instead of
// becoming a deployment that quietly measures nothing.
func Layers(names ...string) Option {
	return func(declared *declaration) { declared.layers = append(declared.layers, names...) }
}

func chain(base crud.Source, declared []string, contributed []Wrapping) (crud.Source, error) {
	byName, err := named(contributed)
	if err != nil {
		return nil, err
	}
	ordered, err := placed(declared, byName)
	if err != nil {
		return nil, err
	}
	for index := len(ordered) - 1; index >= 0; index-- {
		layer := ordered[index].Wrap(base)
		if layer == nil {
			return nil, fmt.Errorf("%w: %s", ErrWrappingDropped, ordered[index].Name)
		}
		base = layer
	}
	return base, nil
}

func named(contributed []Wrapping) (map[string]Wrapping, error) {
	byName := make(map[string]Wrapping, len(contributed))
	for _, wrapping := range contributed {
		switch {
		case wrapping.Name == "":
			return nil, ErrWrappingUnnamed
		case wrapping.Wrap == nil:
			return nil, fmt.Errorf("%w: %s", ErrWrappingEmpty, wrapping.Name)
		}
		if _, taken := byName[wrapping.Name]; taken {
			return nil, fmt.Errorf("%w: %s is contributed twice", ErrWrappingTwice, wrapping.Name)
		}
		byName[wrapping.Name] = wrapping
	}
	return byName, nil
}

// placed is where the declaration and the graph are compared, and both
// directions are refusals. A name is reported once and in an order that does not
// depend on the order fx handed the group over: what is missing is reported as
// the deployment wrote it, what was never declared is sorted.
func placed(declared []string, contributed map[string]Wrapping) ([]Wrapping, error) {
	ordered := make([]Wrapping, 0, len(declared))
	wanted := make(map[string]struct{}, len(declared))
	var missing []string

	for _, name := range declared {
		if _, taken := wanted[name]; taken {
			return nil, fmt.Errorf("%w: %s is declared twice", ErrWrappingTwice, name)
		}
		wanted[name] = struct{}{}

		wrapping, arrived := contributed[name]
		if !arrived {
			missing = append(missing, name)
			continue
		}
		ordered = append(ordered, wrapping)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrWrappingMissing, strings.Join(missing, ", "))
	}

	var undeclared []string
	for name := range contributed {
		if _, wantedHere := wanted[name]; !wantedHere {
			undeclared = append(undeclared, name)
		}
	}
	if len(undeclared) > 0 {
		slices.Sort(undeclared)
		return nil, fmt.Errorf("%w: %s", ErrWrappingUndeclared, strings.Join(undeclared, ", "))
	}

	return ordered, nil
}
