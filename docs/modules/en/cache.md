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

### Eviction domains

`Provider.Resource` names the physical resource a provider talks to — one Redis,
one database, one process. Say what else lives there, and activation refuses a
cache that would share an eviction domain with durable state:

```go
Resources: []cache.ResourceDeclaration{
	{Resource: "redis-cache", Tenants: []cache.ResourceTenant{cache.CacheTenant}},
	{Resource: "redis-durable", Tenants: []cache.ResourceTenant{
		cache.DurableWorkTenant, cache.DurableSecurityTenant,
	}, Waiver: cache.SharedDurableSecurity("one redis until the jobs cluster lands")},
},
```

A cache evicts on purpose; a job queue and a revocation list do not survive it.
An evicted revocation entry reads as "not revoked", so a shared `maxmemory` turns
a capacity event into a signed-out session coming back. Three kinds of state,
three resource identities: `CacheTenant`, `DurableWorkTenant` and
`DurableSecurityTenant`. A cache resolved onto a resource declared with either
durable tenant is refused, and no waiver excuses that. `SharedDurableSecurity`
excuses only durable work and durable security sharing one resource; its reason
is required, and offering it where nothing is shared is itself a refusal. An
undeclared resource is unchecked, not proven separate — set
`RequireDeclaredResources: true` and activation refuses a cache whose resource
nobody described, so a forgotten declaration is a start-up error rather than
silence. See [[D-104]].

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

`ResolveMany` is the batch form. It reads once, calls a typed
`BatchLoader[K, V] func(ctx, []K) ([]LoadResult[V], error)` at most once with
the deduplicated missing keys in first-seen order, and returns one result per
input key in input order:

```go
results, err := ProductCards.ResolveMany(ctx, keys, func(ctx context.Context, missing []ProductCardKey) ([]cache.LoadResult[ProductCard], error) {
	cards, err := products.FindCards(ctx, missing)
	if err != nil {
		return nil, err
	}
	return cards, nil
})
```

The loader must answer with exactly one `LoadResult` per key it was given.
A wrong count, an unset `Presence`, an error or a panic fails the whole call,
and nothing has been written by then: every answer is encoded and charged
against `MaxBatchResultBytes` before the first backend write. `ResolveMany`
fills misses — a `Stale` entry comes back stale and is refreshed by `Resolve` —
and it does not join per-address flights, so two concurrent batch resolves may
each call their loader. See [[D-095]].

## Execution memo

An HTTP request or a job attempt can install a bounded L0 in front of the
backend for its own lifetime:

```go
memo, err := cache.NewMemo(cache.MemoLimit{MaxEntries: 128, MaxBytes: 1 << 20})
if err != nil {
	return err
}
defer memo.Close()
ctx = cache.WithMemo(ctx, memo)
```

Every cache reading from that context answers a repeated address without a
backend round trip. The memo holds copied encoded envelopes, so freshness is
recomputed on each read and no two callers share a mutable value. It remembers
only what the backend actually stored: a backend **miss** is never remembered —
a concurrent writer may be filling it — while a **negative** the loader
confirmed is. Errors and corrupt envelopes are never remembered, and neither is
a read a concurrent write superseded: the lookup that lost that race reads
again, and only the confirmed answer is remembered, so two reads in one
execution never move backwards in time.

`Put`, `Forget`, `Resolve` and `ResolveMany` drop the memo entry for the address
they touch, and loads never read the memo. `Close` is idempotent, empties the
container and turns every later read and write into a no-op, so a goroutine that
outlived its request and kept the context retains nothing. `Stats` reports
entries, retained bytes, hits, stores and refusals. Memo bytes are the memo's
own budget and are not charged to `MaxTransientBytes`. See [[D-094]].

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
metrics-oriented and deliberately have no request values. `Event.Memoized`
marks an answer served by an execution memo rather than by the backend.

Several independent observers compose through the base-owned fan-out rather
than through a chain any one of them owns:

```go
runtime.Observer = cache.MustObservers(applicationMetrics, exporter)
```

`Observers` validates and copies a finite list at construction, refuses more
than `MaxObservers` children, skips nil and typed-nil entries, runs children
synchronously in registration order and isolates each child's panic so the
later ones still run. It starts no goroutine, queue, retry or timer. See
[[D-096]].

## The probe

`Check(ctx) error` says whether this cache can serve, and nothing about how much
that matters:

```go
health.Contribution{
	Name:       "product-cards",
	Code:       "cache",
	Importance: health.Degrading,
	Probe:      health.ProbeFunc(ProductCards.Check),
}
```

It refuses with `ErrNotActivated` before activation, passes for a disabled
policy or a backend with no `HealthChecker`, and otherwise calls the backend
under the backend deadline and reports the sanitized `ErrBackend` category —
never the driver's own message. Importance, the public code and the transport
belong to the composition root ([[D-091]], [[D-096]]).

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

### Capabilities

A backend may do more than store envelopes. Six capabilities are built in —
`BatchReader`, `CompareAndSwapper`, `Maintainer`, `HealthChecker`,
`TagInvalidator` and `Transactional` — each with a constant
(`BatchReadCapability`, `CompareAndSwapCapability`, `MaintenanceCapability`,
`HealthCapability`, `TagInvalidationCapability`, `TransactionCapability`) and a
typed lookup (`BatchReaderOf`, `CompareAndSwapperOf`, and so on) that walks
decorators through `Next()`.

Anything else is the driver's to name. A backend that implements
`CapabilityDeclarer` publishes its own capability strings, and `Supports`
answers from that set. A declared name can never grant a built-in capability:
those are proved only by the method, so a driver cannot claim `batch_read`
without `GetMany`. `DeclaredCapabilitiesOf` drops built-in names, malformed
names and an over-long or panicking declaration.

`DefinitionSpec.Requires` may name any of them; a provider that cannot meet a
requirement is refused at activation, not at the first call. See [[D-093]].

See [[UC-024]], [[FL-025]], [[D-084]], [[D-085]], [[D-093]], [[D-094]],
[[D-095]], [[D-096]] and [[D-104]].
