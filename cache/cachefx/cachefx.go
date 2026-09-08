package cachefx

import (
	"context"
	"fmt"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/cache"
)

const (
	setGroupName      = "vv.cache.sets"
	providerGroupName = "vv.cache.providers"
	resourceGroupName = "vv.cache.resources"
	observerGroupName = "vv.cache.observers"

	setGroup      = `group:"` + setGroupName + `"`
	providerGroup = `group:"` + providerGroupName + `"`
	resourceGroup = `group:"` + resourceGroupName + `"`
	observerGroup = `group:"` + observerGroupName + `"`
)

func AsSet(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(setGroup))
}

func AsProvider(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(providerGroup))
}

func AsResource(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(resourceGroup))
}

// Two modules that both want to watch the cache is the ordinary case — metrics
// and tracing, say — and a single binding made the second one silently replace
// the first or fail the graph. Contributed observers are composed with
// cache.Observers; the single Observer binding stays as the zero-or-one
// shorthand and is composed alongside them.
func AsObserver(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(observerGroup))
}

// Resources is the composition root saying what lives on a resource the package
// living there cannot describe itself: a revocation list or a job queue is a
// tenant of the Redis it shares without importing `cache` for it ([[D-111]]).
func Resources(declarations ...cache.ResourceDeclaration) fx.Option {
	options := make([]fx.Option, 0, len(declarations))
	for _, declaration := range declarations {
		options = append(options, fx.Supply(fx.Annotated{Group: resourceGroupName, Target: declaration}))
	}
	return fx.Options(options...)
}

type Contributions struct {
	fx.In

	Sets []cache.Set `group:"vv.cache.sets"`

	Providers []cache.Provider `group:"vv.cache.providers"`

	Resources []cache.ResourceDeclaration `group:"vv.cache.resources"`

	Observer cache.Observer `optional:"true"`

	Observers []cache.Observer `group:"vv.cache.observers"`
}

// Undeclared is what a resource nobody described means to this deployment. The
// core defaults to accepting one so that the rule can be adopted resource by
// resource; a graph that reaches for this binding has finished adopting it, so
// here silence is the refusal and the permissive answer is a written word.
type Undeclared string

const (
	Refused  Undeclared = "refused"
	Accepted Undeclared = "accepted"
)

func (this Undeclared) declarationsRequired() (bool, error) {
	switch this {
	case "", Refused:
		return true, nil
	case Accepted:
		return false, nil
	}
	return false, fmt.Errorf("cachefx: %w: Spec.Undeclared is %q, which is neither cachefx.Refused nor cachefx.Accepted",
		cache.ErrInvalid, string(this))
}

type Spec struct {
	Application string
	Environment string

	Runtime cache.Runtime

	Sets      []cache.Set
	Providers []cache.Provider
	Resources []cache.ResourceDeclaration

	Undeclared Undeclared
}

func Caching(spec Spec) fx.Option {
	required, err := spec.check()
	if err != nil {
		return fx.Error(err)
	}
	return Activating(func(contributed Contributions) (cache.ActivationSpec, error) {
		return spec.activation(contributed, required)
	})
}

func Auto(application, environment string) fx.Option {
	return Caching(Spec{Application: application, Environment: environment})
}

// Activating is the form under the magic: the constructor builds the whole
// cache.ActivationSpec and this decides only when Activate is called on it.
func Activating(constructor any) fx.Option {
	return fx.Module("vv.cache",
		fx.Provide(constructor),
		fx.Invoke(activate),
	)
}

// activate holds the caches back until the start, so a dependency the providers
// were built from has been started by then, and so a refusal rolls back
// everything the start had already brought up rather than leaking it.
func activate(lifecycle fx.Lifecycle, spec cache.ActivationSpec) {
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error { return cache.Activate(ctx, spec) },
	})
}

func (this Spec) check() (bool, error) {
	if this.Application == "" || this.Environment == "" {
		return false, fmt.Errorf("cachefx: %w: Spec.Application and Spec.Environment name the namespace every cache is published under",
			cache.ErrInvalid)
	}
	return this.Undeclared.declarationsRequired()
}

func (this Spec) activation(contributed Contributions, required bool) (cache.ActivationSpec, error) {
	sets := join(this.Sets, contributed.Sets)
	if len(sets) == 0 {
		return cache.ActivationSpec{}, fmt.Errorf("cachefx: %w: no cache set was contributed; use cachefx.AsSet, configure Spec.Sets, or build the activation with cachefx.Activating",
			cache.ErrInvalid)
	}
	runtime := this.Runtime
	if runtime.Observer == nil {
		observer, err := composeObservers(contributed)
		if err != nil {
			return cache.ActivationSpec{}, err
		}
		runtime.Observer = observer
	}
	return cache.ActivationSpec{
		Application:              this.Application,
		Environment:              this.Environment,
		Runtime:                  runtime,
		Sets:                     sets,
		Providers:                join(this.Providers, contributed.Providers),
		Resources:                join(this.Resources, contributed.Resources),
		RequireDeclaredResources: required,
	}, nil
}

func join[T any](configured, contributed []T) []T {
	if len(contributed) == 0 {
		return configured
	}
	joined := make([]T, 0, len(configured)+len(contributed))
	joined = append(joined, configured...)
	return append(joined, contributed...)
}

// One contributed observer is used as it is, so a graph with a single watcher
// carries no fan-out. More than one is composed; cache.Observers is what refuses
// a count above cache.MaxObservers, and its error is returned rather than
// swallowed.
func composeObservers(contributed Contributions) (cache.Observer, error) {
	children := make([]cache.Observer, 0, len(contributed.Observers)+1)
	if contributed.Observer != nil {
		children = append(children, contributed.Observer)
	}
	children = append(children, contributed.Observers...)
	switch len(children) {
	case 0:
		return nil, nil
	case 1:
		return children[0], nil
	}
	observer, err := cache.Observers(children...)
	if err != nil {
		return nil, fmt.Errorf("cachefx: composing contributed cache observers: %w", err)
	}
	return observer, nil
}
