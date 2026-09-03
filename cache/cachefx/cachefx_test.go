package cachefx_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachefx"
	"github.com/frostgrove/vv/cache/cachememory"
)

func cards(t *testing.T) (*cache.Cache[string, string], cache.Set) {
	t.Helper()
	target := cache.Auto[string, string]()
	definition, err := cache.Define(target, cache.DefinitionSpec[string, string]{
		Name:      "product-cards",
		Namespace: cache.NamespaceTemplate{Purpose: "product-cards", Generation: 1},
		Scope:     cache.GlobalPlan[string](),
		Keys: cache.MustKeyFunc(cache.KeyVersion(1), func(key string, _ cache.KeyLimit) ([]byte, error) {
			return []byte(key), nil
		}),
		Values: cache.String(cache.ValueSchema(1)),
	})
	if err != nil {
		t.Fatalf("declaring the cache: %v", err)
	}
	set, err := cache.NewSet(definition)
	if err != nil {
		t.Fatalf("collecting the set: %v", err)
	}
	return target, set
}

func provider(t *testing.T, resource cache.ResourceID) cache.Provider {
	t.Helper()
	backend, err := cachememory.New(cachememory.Limits{MaxEntries: 64, MaxBytes: 64 << 20, MaxItemBytes: 32 << 20})
	if err != nil {
		t.Fatalf("building the backend: %v", err)
	}
	return cache.Provider{
		ID:       "product-cache",
		Resource: resource,
		Kind:     cache.MemoryProviderKind,
		Backend:  backend,
	}
}

func start(t *testing.T, options ...fx.Option) error {
	t.Helper()
	application := fx.New(append([]fx.Option{fx.NopLogger}, options...)...)
	if err := application.Err(); err != nil {
		return err
	}
	if err := application.Start(context.Background()); err != nil {
		return err
	}
	t.Cleanup(func() { _ = application.Stop(context.Background()) })
	return nil
}

func refusedProblems(t *testing.T, err error) []string {
	t.Helper()
	var refusal *cache.ActivationError
	if !errors.As(err, &refusal) {
		t.Fatalf("the start failed with %v, which is not a cache activation refusal", err)
	}
	return refusal.Problems()
}

func mentions(problems []string, fragment string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, fragment) {
			return true
		}
	}
	return false
}

func TestACacheAndItsProviderMeetThroughTheGraphAndNeitherNamesTheOther(t *testing.T) {
	target, set := cards(t)

	err := start(t,
		fx.Provide(cachefx.AsSet(func() cache.Set { return set })),
		fx.Provide(cachefx.AsProvider(func() cache.Provider { return provider(t, "redis-cache") })),
		cachefx.Resources(cache.ResourceDeclaration{
			Resource: "redis-cache",
			Tenants:  []cache.ResourceTenant{cache.CacheTenant},
		}),
		cachefx.Auto("catalog", "test"),
	)
	if err != nil {
		t.Fatalf("an application whose modules contributed a cache and a backend did not start: %v", err)
	}

	ctx := context.Background()
	if err := target.Put(ctx, "sku-1", "a card"); err != nil {
		t.Fatalf("the activated cache refused a write: %v", err)
	}
	result, err := target.Lookup(ctx, "sku-1")
	if err != nil || result.State != cache.Hit || result.Value != "a card" {
		t.Fatalf("result = %+v, err = %v, want the value the graph's backend stored", result, err)
	}
}

func TestAStartIsRefusedWhenACacheSharesItsResourceWithDurableState(t *testing.T) {
	tests := map[string]struct {
		tenants []cache.ResourceTenant
		waiver  cache.SharedResourceWaiver
		refused bool
	}{
		"revoked sessions live on it": {tenants: []cache.ResourceTenant{cache.DurableSecurityTenant}, refused: true},
		"queued work lives on it":     {tenants: []cache.ResourceTenant{cache.DurableWorkTenant}, refused: true},
		"a waiver is offered for it": {
			tenants: []cache.ResourceTenant{cache.DurableSecurityTenant},
			waiver:  cache.SharedDurableSecurity("staging runs one redis"),
			refused: true,
		},
		"the resource is the cache's own": {tenants: []cache.ResourceTenant{cache.CacheTenant}},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			target, set := cards(t)

			err := start(t,
				fx.Provide(cachefx.AsSet(func() cache.Set { return set })),
				fx.Provide(cachefx.AsProvider(func() cache.Provider { return provider(t, "redis-main") })),
				cachefx.Resources(cache.ResourceDeclaration{
					Resource: "redis-main",
					Tenants:  test.tenants,
					Waiver:   test.waiver,
				}),
				cachefx.Auto("catalog", "test"),
			)

			if !test.refused {
				if err != nil {
					t.Fatalf("a cache alone on its resource was refused the start: %v", err)
				}
				return
			}
			if !mentions(refusedProblems(t, err), "shares the eviction domain of resource \"redis-main\"") {
				t.Fatalf("the start failed without naming the shared eviction domain: %v", err)
			}
			if target.Describe().Activated {
				t.Fatal("the refused cache was published onto the durable resource anyway")
			}
		})
	}
}

func TestAResourceNobodyDescribedKeepsTheApplicationFromStartingUnlessItSaysOtherwise(t *testing.T) {
	tests := map[string]struct {
		undeclared cachefx.Undeclared
		refused    bool
	}{
		"silence is the refusal":      {refused: true},
		"the refusal written out":     {undeclared: cachefx.Refused, refused: true},
		"a deployment still adopting": {undeclared: cachefx.Accepted},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			target, set := cards(t)

			err := start(t,
				fx.Provide(cachefx.AsSet(func() cache.Set { return set })),
				fx.Provide(cachefx.AsProvider(func() cache.Provider { return provider(t, "redis-main") })),
				cachefx.Caching(cachefx.Spec{
					Application: "catalog",
					Environment: "test",
					Undeclared:  test.undeclared,
				}),
			)

			if !test.refused {
				if err != nil {
					t.Fatalf("a deployment that accepts an undeclared resource did not start: %v", err)
				}
				if !target.Describe().Activated {
					t.Fatal("the start succeeded and the cache was never published")
				}
				return
			}
			if !mentions(refusedProblems(t, err), "resource \"redis-main\" declares no tenant") {
				t.Fatalf("the start failed without naming the resource nobody described: %v", err)
			}
			if target.Describe().Activated {
				t.Fatal("a cache was published onto a resource nothing proved separate from durable state")
			}
		})
	}
}

func TestAPackageDeclaresItsResourceThroughTheGroupTagWithoutImportingThisBinding(t *testing.T) {
	target, set := cards(t)
	revocationList := func() cache.ResourceDeclaration {
		return cache.ResourceDeclaration{
			Resource: "redis-main",
			Tenants:  []cache.ResourceTenant{cache.DurableSecurityTenant},
		}
	}

	err := start(t,
		fx.Provide(cachefx.AsSet(func() cache.Set { return set })),
		fx.Provide(cachefx.AsProvider(func() cache.Provider { return provider(t, "redis-main") })),
		fx.Provide(fx.Annotate(revocationList, fx.ResultTags(`group:"vv.cache.resources"`))),
		cachefx.Auto("catalog", "test"),
	)

	if !mentions(refusedProblems(t, err), "shares the eviction domain of resource \"redis-main\"") {
		t.Fatalf("a declaration contributed by the group tag alone did not reach the activation: %v", err)
	}
	if target.Describe().Activated {
		t.Fatal("the cache was published over a declaration the binding did not annotate itself")
	}
}

func TestTheLowLevelFormActivatesTheSpecItWasHandedAndInfersNothingAroundIt(t *testing.T) {
	target, set := cards(t)
	built := func(contributed cachefx.Contributions) cache.ActivationSpec {
		return cache.ActivationSpec{
			Application:              "catalog",
			Environment:              "test",
			Sets:                     []cache.Set{set},
			Providers:                contributed.Providers,
			RequireDeclaredResources: false,
		}
	}

	err := start(t,
		fx.Provide(cachefx.AsProvider(func() cache.Provider { return provider(t, "redis-main") })),
		cachefx.Activating(built),
	)
	if err != nil {
		t.Fatalf("a hand-built activation spec did not start: %v", err)
	}

	if !target.Describe().Activated {
		t.Fatal("the low-level form started the application without publishing the cache")
	}
}

func TestNoCacheIsPublishedBeforeTheStartTheRefusalBelongsTo(t *testing.T) {
	target, set := cards(t)

	application := fx.New(
		fx.NopLogger,
		fx.Provide(cachefx.AsSet(func() cache.Set { return set })),
		fx.Provide(cachefx.AsProvider(func() cache.Provider { return provider(t, "redis-cache") })),
		cachefx.Resources(cache.ResourceDeclaration{
			Resource: "redis-cache",
			Tenants:  []cache.ResourceTenant{cache.CacheTenant},
		}),
		cachefx.Auto("catalog", "test"),
	)
	if err := application.Err(); err != nil {
		t.Fatalf("building the graph: %v", err)
	}

	if target.Describe().Activated {
		t.Fatal("the caches were published while the graph was being built, where a failed start rolls nothing back")
	}
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("starting: %v", err)
	}
	t.Cleanup(func() { _ = application.Stop(context.Background()) })

	if !target.Describe().Activated {
		t.Fatal("the start finished and the cache is still unactivated")
	}
}

func TestAnObserverTheGraphCarriesSeesWhatEveryActivatedCacheDoes(t *testing.T) {
	target, set := cards(t)
	observer := &counter{}

	err := start(t,
		fx.Provide(cachefx.AsSet(func() cache.Set { return set })),
		fx.Provide(cachefx.AsProvider(func() cache.Provider { return provider(t, "redis-cache") })),
		fx.Provide(func() cache.Observer { return observer }),
		cachefx.Resources(cache.ResourceDeclaration{
			Resource: "redis-cache",
			Tenants:  []cache.ResourceTenant{cache.CacheTenant},
		}),
		cachefx.Auto("catalog", "test"),
	)
	if err != nil {
		t.Fatalf("starting: %v", err)
	}

	if _, err := target.Lookup(context.Background(), "sku-1"); err != nil {
		t.Fatalf("looking up: %v", err)
	}

	if observer.events.Load() == 0 {
		t.Fatal("the observer the graph carries was never installed, so an activated cache reports to nobody")
	}
}

type counter struct{ events atomic.Int64 }

func (this *counter) Observe(context.Context, cache.Event) { this.events.Add(1) }

func TestAGraphThatCannotSayWhatItIsActivatingIsRefusedBeforeItRuns(t *testing.T) {
	tests := map[string]struct {
		spec         cachefx.Spec
		contributing bool
		named        string
	}{
		"no application": {
			spec:         cachefx.Spec{Environment: "test"},
			contributing: true,
			named:        "Spec.Application",
		},
		"no environment": {
			spec:         cachefx.Spec{Application: "catalog"},
			contributing: true,
			named:        "Spec.Environment",
		},
		"a strictness nobody defined": {
			spec:         cachefx.Spec{Application: "catalog", Environment: "test", Undeclared: "maybe"},
			contributing: true,
			named:        `"maybe"`,
		},
		"not one cache set": {
			spec:  cachefx.Spec{Application: "catalog", Environment: "test"},
			named: "no cache set was contributed",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			target, set := cards(t)
			options := []fx.Option{
				fx.NopLogger,
				fx.Provide(cachefx.AsProvider(func() cache.Provider { return provider(t, "redis-cache") })),
				cachefx.Resources(cache.ResourceDeclaration{
					Resource: "redis-cache",
					Tenants:  []cache.ResourceTenant{cache.CacheTenant},
				}),
				cachefx.Caching(test.spec),
			}
			if test.contributing {
				options = append(options, fx.Provide(cachefx.AsSet(func() cache.Set { return set })))
			}

			application := fx.New(options...)
			err := application.Err()

			if err == nil {
				refused := application.Start(context.Background())
				_ = application.Stop(context.Background())
				t.Fatalf("the graph was built out of a spec nothing could act on: a provider and a declared resource were already in place, so the spec was the only thing left to refuse it, and the start answered %v", refused)
			}
			if !errors.Is(err, cache.ErrInvalid) {
				t.Fatalf("the graph was refused with %v, which is not a refusal of what it was handed", err)
			}
			if !strings.Contains(err.Error(), test.named) {
				t.Fatalf("the graph was refused with %v, which never names %s", err, test.named)
			}
			if target.Describe().Activated {
				t.Fatal("a graph nobody could build published a cache anyway")
			}
		})
	}
}

func TestARefusedActivationUnwindsWhatTheStartHadAlreadyBroughtUp(t *testing.T) {
	target, set := cards(t)
	pool := &redisPool{}

	application := fx.New(
		fx.NopLogger,
		fx.Provide(cachefx.AsSet(func() cache.Set { return set })),
		fx.Provide(cachefx.AsProvider(func(lifecycle fx.Lifecycle) cache.Provider {
			lifecycle.Append(fx.Hook{
				OnStart: func(context.Context) error { pool.opened.Add(1); return nil },
				OnStop:  func(context.Context) error { pool.closed.Add(1); return nil },
			})
			return provider(t, "redis-main")
		})),
		cachefx.Resources(cache.ResourceDeclaration{
			Resource: "redis-main",
			Tenants:  []cache.ResourceTenant{cache.DurableWorkTenant},
		}),
		cachefx.Auto("catalog", "test"),
	)
	if err := application.Err(); err != nil {
		t.Fatalf("the graph refused itself while it was being built, where the pool that opens on the start is never asked to close: %v", err)
	}

	err := application.Start(context.Background())

	if !mentions(refusedProblems(t, err), "shares the eviction domain of resource \"redis-main\"") {
		t.Fatalf("the start failed without naming the shared eviction domain: %v", err)
	}
	if pool.opened.Load() != 1 {
		t.Fatal("the pool the cache would have run on was never opened, so this start had nothing left behind to unwind")
	}
	if pool.closed.Load() != 1 {
		t.Fatalf("the start was refused and the pool it had already opened was closed %d times", pool.closed.Load())
	}
	if target.Describe().Activated {
		t.Fatal("the refused cache was published onto the durable resource anyway")
	}
}

type redisPool struct {
	opened atomic.Int64
	closed atomic.Int64
}
