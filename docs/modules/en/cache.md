# cache

`cache` is the typed, stdlib-only cache facade. It owns stable addresses,
value schemas, freshness, stale and negative results, local load coordination,
mutation fences and hard limits around work performed for one cache. Backends
only store opaque envelopes.

Use it for values that can be recreated and may disappear early. Revocations,
rate limits, job intents, leases, audit records and workflow state are not cache
entries, even when the same storage technology could hold them.

## Declarative path

Declare caches next to the application module that owns the value:

```go
type ProductCard struct {
	Name  string
	Price int64
}

type ProductCardKey struct {
	ID string `cachekey:"id"`
}

var ProductCards = cache.Auto[ProductCardKey, ProductCard]()

var ProductCardsDefinition = cache.MustDefine(ProductCards, cache.DefinitionSpec[ProductCardKey, ProductCard]{
	Name:      "product-cards",
	Namespace: cache.NamespaceTemplate{Purpose: "product-cards", Generation: 1},
	Scope:     cache.GlobalPlan[ProductCardKey](),
	Keys:      cache.MustStructKey[ProductCardKey](1),
	Values:    cache.JSON[ProductCard](1),
})

var Caches = cache.MustSet(ProductCardsDefinition)
```

`Auto` defaults to the `Hot` profile. Pass `Warm`, `Durable` or `Disabled` to
choose a different declared contract. At application start, provide the
available backends once and activate every set atomically:

```go
err := cache.Activate(ctx, cache.ActivationSpec{
	Application: "catalog",
	Environment: "production",
	Sets:        []cache.Set{Caches},
	Providers: []cache.Provider{{
		ID:       "local-cache",
		Resource: "catalog-process",
		Kind:     cache.MemoryProviderKind,
		Backend:  memoryBackend,
	}},
})
```

Activation validates the complete graph before publishing any cache. A missing
or ambiguous provider, duplicate physical namespace, incompatible backend,
invalid codec or unmet capability is one start-up error, not a first-request
surprise. `Describe` exposes the resolved declaration and all effective limits.

## Reading and loading

`Lookup` never invokes application code. `Resolve` first reads the backend and
then calls the supplied loader on a miss. Return `cache.Present(value)` or
`cache.Absent[V]()`; absence is stored only when the profile enables negative
caching.

```go
result, err := ProductCards.Resolve(ctx, ProductCardKey{ID: productID}, func(ctx context.Context, key ProductCardKey) (cache.LoadResult[ProductCard], error) {
	card, found, err := products.FindCard(ctx, key.ID)
	if err != nil {
		return cache.LoadResult[ProductCard]{}, err
	}
	if !found {
		return cache.Absent[ProductCard](), nil
	}
	return cache.Present(card), nil
})
```

`Result.State` distinguishes `Hit`, `Miss`, `Negative`, `Stale` and `Loaded`.
`LookupMany` preserves input order and duplicates; a backend with
`BatchReadCapability` may satisfy it in one call. `Put` and `Forget` are fenced
against concurrent loads so an older loader cannot publish over a newer
mutation.

Load coalescing is process-local and per typed address. `MaxFlights` is the
number of loader groups, not the number of callers. Flight saturation may
reject, wait for a bounded interval or serve an already available stale value.
It is not a distributed lock.

## Hard work limits

Every operation that creates cache-owned transient work obtains admission
before it creates coordination state, timers, owned keys, encoded values or
decode destinations. Disabled no-op lookup and mutation paths create none.
`MaxTransientBytes` bounds cache-attributable transient work and
`MaxTransientWaiters` permanently reserves enough of that budget for the
declared number of waiters. `Descriptor.Policy.ReservedTransientBytes` makes
the reservation visible. Rejection returns `ErrSaturated` without starting the
loader or mutating the backend.

The budget includes allocations and reservations newly attributable to the
cache operation. It does not claim ownership of pre-existing runtime pools,
caller-owned keys, loader closures or context value graphs. Work and allocations
inside application loaders and observers are application-owned too. The same is
true for consumer-provided key functions, partitioners, backends, clocks and
randomness providers. A custom `Codec`, the entire dynamic encode path selected
by `TrustedJSON`, and every trusted encode/decode hook body may run outside the
bound; use those only as explicit trust escapes.

`ValueLimit.MaxBytes` limits wire bytes. `MaxDecodedBytes` separately limits the
decoded allocation model. The safe `JSON` codec validates the reachable Go type
and value before invoking `encoding/json`: interfaces, JSON/text hooks,
unsupported kinds and `time.Time` are refused; graph, text, depth and work are
bounded; raw invalid UTF-8 is corrupt on both safe and trusted JSON paths. Use
`RFC3339UTC` for a canonical UTC `time.Time`. `TrustedJSON` is the explicit
escape for application-controlled hook or interface types: its writer limits
wire output and its result is checked for decoded size and depth, but its
encode-side dynamic graph and traversal, plus trusted hook bodies on both encode
and decode, are outside the bounded-work promise. The safe JSON path is
intentionally unavailable with
`GOEXPERIMENT=jsonv2` until its allocation contract is independently proven.

## Context and observers

A shared loader is not owned by the request that happened to arrive first. Its
loader and backend write receive detached, value-blind contexts with finite
runtime timeouts. The terminal `Load` event is detached and value-blind too;
its observer must enforce the synchronous callback contract below. This
prevents a principal, transaction, request body or other request-scoped value
from leaking into work shared with unrelated callers. Put required domain data
in the typed key or an explicit bounded loader dependency. Direct lookup and
mutation calls still use the caller context for their own backend calls.

The `Disabled` profile bypasses backend storage and coordination, but still
applies loader timeout, caller cancellation, value-blind context and transient
admission. It is therefore a safe operational switch, not a second execution
model.

Observers run synchronously, outside cache locks, and panics are contained.
They must be bounded and non-blocking and must not re-enter the cache that is
currently emitting an event; `Stats` inspection is safe. Shared-load events are
metrics-oriented and deliberately have no request values.

## Explicit construction

Applications that do not want top-level declarations can build exactly the
same core directly:

```go
policy, err := cache.Hot.Build()
if err != nil {
	return err
}

cards, err := cache.New(
	runtime,
	memoryBackend,
	cache.Global[ProductCardKey](cache.MustNamespace("catalog", "production", "product-cards", 1)),
	cache.MustStructKey[ProductCardKey](1),
	cache.JSON[ProductCard](1),
	policy,
)
```

`New` remains useful for libraries, tests and consumers with their own
composition system. It does not weaken validation, policy or bounded-work
semantics.

## Backends

- [cachememory](cachememory.md) is the bounded in-process implementation.
- PostgreSQL and Redis adapters will be separate optional modules; the current
  core package has no driver dependency and does not claim those backends yet.
- A backend describes topology, expiry clock and size/capacity support. Shared
  backends additionally require an explicit bounded clock-skew policy.

See [[UC-024]], [[FL-025]], [[D-084]] and [[D-085]].
