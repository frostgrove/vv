# FL-025 — A cache declaration becomes a bounded value

**Entry points:** `cache.Auto`, `cache.Define`, `cache.Activate`,
`cachefx.Caching`, `cache.Cache.Resolve`
**Governed by:** [[D-021]] [[D-084]] [[D-085]] [[D-093]] [[D-094]] [[D-095]]
[[D-096]] [[D-104]] [[D-111]]

What happens between a top-level typed declaration and a hit, stale value,
negative result or application load, including every place the operation is
bounded.

## Declaration and activation

1. **`Auto[K,V]`** — `cache/declaration.go` — chooses `Hot` unless a profile is
   supplied, normalises its policy and derives a typed transient plan before a
   backend exists.
2. **`Define`** — the same file — binds the logical name, namespace purpose and
   generation, global or partitioned scope, key codec, value codec, optional
   provider and required capabilities. Codec descriptors and type-dependent
   allocation charges are validated here.
3. **`Set`** — snapshots declarations, sorts them and refuses duplicate names or
   targets.
4. **`Activate`** — `cache/activation.go` — validates application/environment,
   flattens every set, resolves providers, prevents two declarations from
   sharing one physical namespace, prepares every core and only then publishes
   all of them under one activation gate. A failed preparation publishes none.
5. **Eviction domains** — `cache/resource.go` — `ActivationSpec.Resources` says
   which tenants a physical resource identity carries. A cache resolved onto a
   resource declared with `DurableWorkTenant` or `DurableSecurityTenant` is
   refused with the rest of the activation problems, and no waiver excuses it;
   two durable tenants share one resource only behind `SharedDurableSecurity`,
   whose reason is validated and whose presence is refused where it excuses
   nothing. `RequireDeclaredResources` makes an undeclared resource a refusal
   too, naming the cache that resolved to it. [[D-104]] says why the check has to
   be a declaration.
6. **The binding** — `cache/cachefx/cachefx.go` — is what calls `Activate` in a
   running process. `AsSet`, `AsProvider` and `AsResource` file contributions
   into three fx groups, `Resources` supplies the declarations the composition
   root writes for a package that does not import `cache` at all, and
   `Contributions` is how any of it is read back. `Caching` assembles the
   `ActivationSpec` with `RequireDeclaredResources` on unless the spec says
   `Undeclared: Accepted`; `Auto` is the two-argument form of it; `Activating`
   takes a constructor that builds the whole spec by hand. `activate` appends
   one start hook, so a refused graph fails the start it can unwind rather than
   the construction it cannot. [[D-111]] says why the declaration is contributed
   rather than derived from the providers already in the graph.

`Cache.Describe` in `cache/descriptor.go` exposes the declaration before
activation and the resolved namespace, provider, backend, clock policy and
effective limits afterwards.

## Address and admission

1. The scope and key codec in `cache/address.go`, `cache/scope.go` and
   `cache/key.go` produce bounded owned bytes. Namespace, partition and key are
   hashed into a fixed `Address`; key and value schema versions are part of the
   envelope contract rather than string conventions.
2. `cache/transient.go` computes a typed plan for lookup, batch, load, put and
   forget. It reserves permanent capacity for `MaxTransientWaiters`. Every path
   that creates cache-owned transient work obtains its operation lease before
   coordination state, timers or backend work; disabled no-op lookup/mutation
   paths need no lease.
3. A rejecting policy returns `ErrSaturated`. A waiting policy registers only
   inside the pre-reserved waiter population, waits through the runtime clock
   and either acquires the exact requested charge or returns a bounded error.

## Lookup and decode

1. `cache/lookup.go` performs the backend read under the caller and backend
   deadlines, with an envelope-sized `ReadLimit`.
2. `cache/envelope.go` verifies envelope version, key/value schema and bounded
   times before exposing payload bytes.
3. The codec receives separate wire, decoded-allocation and depth limits.
   `cache/codec.go` preflights safe JSON structure, work, text, UTF-8 and
   destination charge before `encoding/json` runs. Encode performs the matching
   value preflight and proves that its output is admissible to the same decoder.
   Safe JSON activation is refused under `GOEXPERIMENT=jsonv2`; `TrustedJSON`
   retains bounded output/postflight but deliberately trusts its dynamic encode
   graph and hooks.
4. Fresh, stale, negative and missing envelopes become distinct `Result.State`
   values. Corruption and backend failures follow the declared policy and emit
   classified events.

`LookupMany` deduplicates addresses internally, preserves caller order and
duplicates on output, and chooses a declared `BatchReader` or bounded fallback
without changing result semantics.

## The execution memo

`cache/memo.go` holds an optional L0 for one execution. `WithMemo` puts a
bounded `Memo` on a context and its owner closes it; `Close` empties the
container and turns every later read and write into a no-op.

1. `lookupStable` consults the memo after transient admission and before it
   takes the address's coordination state, so a memoized answer costs no
   coordination at all. A stored envelope is decoded fresh, so freshness is
   recomputed against the clock and a `Hit` may legitimately become `Stale`
   later in the same execution.
2. `lookupAddress` returns the decoded result together with the envelope it
   read, and `confirmReadAndMemoize` decides both questions at once under the
   coordination lock: the read's generation is still current, and the envelope
   is remembered. A read a concurrent write superseded is retried and never
   remembered. A backend miss, a corrupt envelope and every error path fill
   nothing, and a memoized envelope that fails to decode is dropped before the
   corruption policy runs.
3. `batchGet` splits the unique addresses into what the memo holds and what it
   lacks, asks the backend only for the remainder, merges, and reports which
   addresses it actually read. `confirmBatchReadAndMemoize` remembers exactly
   those, and only once every address in the batch is still on the generation
   the round began with. `decodeBatch` applies the cumulative batch bounds to
   the merged map unchanged.
4. `Put`, `Forget`, `Resolve` and every `ResolveMany` write drop the memo entry
   for the address they touch. Loads never read the memo.

Memo bytes are the memo's own budget and are not charged to
`MaxTransientBytes`; [[D-085]] bounds one operation's work, the memo is retained
across many.

## Resolve for a batch

`cache/resolve_many.go` reads once through `LookupMany`, then:

1. `planMissing` walks the results in caller order, deduplicates the `Miss`
   addresses, records every input index that maps to each and charges the keys
   against `MaxBatchKeyBytes`.
2. `loadBatch` calls the typed `BatchLoader` once, inside the value-blind loader
   deadline, containing a panic. A wrong result count, an unset `Presence`, a
   loader error or an expired loader context fails the whole call.
3. `encodeLoaded` encodes every answer and charges the cumulative envelope size
   against `MaxBatchResultBytes` **before** `commitLoaded` performs the first
   write. A clean absence with negative caching disabled becomes a `Negative`
   result and writes nothing.
4. `commitLoaded` writes each envelope through the ordinary mutation fence
   (`beginMutationAs`/`commitMutationAs`, reported as `load_many`), so a
   superseded write is a no-op rather than an error. It does not join
   per-address flights.

## Resolve and a shared flight

1. `cache/resolve.go` performs the same read first. A result ends the operation;
   a miss or blocking refresh proceeds to the per-address coordination state.
2. Existing members can be joined. New groups are capped by `MaxFlights`; the
   saturation policy rejects, waits or returns an already captured stale value.
3. A new group starts from `context.Background`, then receives only a finite
   loader deadline. The first caller's context values are not retained. Caller
   cancellation controls only that caller's wait.
4. The loader returns `Present` or `Absent`. The result is encoded under the
   typed limit, wrapped with freshness/retention and written under a detached
   finite backend context only if its generation is still current.
5. `Put` or `Forget` increments the generation and invalidates an older member,
   so its completion cannot overwrite the newer mutation.
6. Completion releases every waiter with one outcome, emits exactly one
   detached `Load` event and releases all timers, leases and coordination refs.

The `Disabled` branch skips lookup, coordination and storage but uses the same
value-blind timed loader boundary, transient admission, cancellation precedence
and load-event classification.

## Capabilities and the probe

`cache/capability.go` answers `Supports` for a built-in capability by walking
the decorator chain for its typed interface, and for every other capability from
the set the backend declared through `CapabilityDeclarer`. A declared built-in
name, a malformed name, an over-long declaration and a panicking declarer all
contribute nothing. `cache/activation.go` checks `Definition.Requires` against
the resolved provider through that same function, so an unmet requirement is a
start-up error.

`cache/health.go:Check` is the neutral probe: it refuses while the cache is not
activated, passes for a disabled policy or a backend with no `HealthChecker`,
and otherwise calls the backend under the backend deadline and reports the
sanitized `ErrBackend` category. The composition root decides what that answer
is worth; [[D-096]] says why the cache does not.

## Observer and context boundary

`cache/observers.go:Observers` composes at most `MaxObservers` children,
synchronously, in registration order, isolating each child's panic and starting
nothing. `cache/cache.go:observe` contains observer panic and invokes
callbacks outside cache locks. Ordinary operation events may carry the operation caller's context.
Terminal shared-load events use the flight's detached, value-blind context.
Callbacks are synchronous so they remain part of the admitted operation; they
must not block or re-enter the emitting cache. `Stats` does not begin an
operation and is safe to inspect.

## In-process storage

`cache/cachememory/backend.go` owns byte copies, exact charge accounting,
expiry and the LRU list. Every mutation is planned under entry/item/byte limits
before publication. Batch reads bound unique addresses, each value and the
aggregate result. Observer calls occur after unlocking. Reset and close detach
the complete state before emitting events.

## Files

| File | What it holds |
|---|---|
| `cache/declaration.go`, `activation.go` | magic-first declaration and atomic provider resolution |
| `cache/resource.go` | declared resource tenants and the eviction-domain refusal |
| `cache/policy.go`, `descriptor.go` | profiles, explicit overrides and inspectable effective policy |
| `cache/address.go`, `scope.go`, `key.go` | typed identity and fixed backend address |
| `cache/transient.go` | permanent waiter reservation and per-operation admission |
| `cache/lookup.go`, `resolve.go`, `mutation.go` | read, local flight and mutation fence |
| `cache/resolve_many.go` | one batch loader call, cumulative bound before the first write |
| `cache/memo.go` | the bounded execution container and its close barrier |
| `cache/capability.go` | built-in capability interfaces and the driver-declared set |
| `cache/observers.go`, `cache/health.go` | bounded observer fan-out and the neutral probe |
| `cache/envelope.go`, `codec.go` | versioned wire value and bounded codecs |
| `cache/context.go`, `runtime.go` | injected clock, finite contexts and watcher teardown |
| `cache/cachememory/backend.go` | bounded process backend |
| `cache/cachefx/cachefx.go` | the fx groups, the required declarations and the one start hook |

## Tests that walk this flow

- `cache/activation_test.go` covers all-or-nothing publication and provider
  selection.
- `cache/coordination_test.go`, `cache/review_regression_test.go` and
  `cache/transient_test.go` cover flight races, mutation fences, context
  isolation, cancellation, saturation, timers and bounded codecs.
- `cache/batch_test.go` covers capability and fallback parity.
- `cache/capability_test.go` covers the declared capability set, the refusal of
  a declared built-in name and requirements beyond `batch_read` at activation.
- `cache/memo_test.go` covers the L0 consult, the miss that is never
  remembered, the confirmed absence that is, mutation invalidation, the close
  barrier and the entry bound.
- `cache/memo_currency_test.go` covers the read a concurrent write supersedes:
  neither `Lookup` nor `LookupMany` answers from the envelope their own
  coordination discarded.
- `cache/resource_test.go` covers the eviction domain: a cache refused the
  resource that holds durable state, the waiver that only two durable tenants
  may use, the resource declarations that are refused on their own shape, and
  the undeclared resource that passes until the root asks for declarations.
- `cache/resolve_many_test.go` covers order, deduplication, all-or-error and the
  cumulative bound proved before the first write.
- `cache/cachefx/cachefx_test.go` covers the binding: two modules that name
  nothing of each other meeting at one activation, the durable resource that
  fails the start, the undeclared resource that fails it until the deployment
  accepts one, the declaration contributed through the raw group tag, the
  hand-built spec, and the caches that exist only after the start. It also
  covers the two ends of the hook: a spec the binding cannot act on is refused
  at `fx.New` before any hook runs, and a refusal on the start hook closes the
  pool the provider's own hook had already opened.
- `cache/observers_test.go` and `cache/health_test.go` cover the fan-out and
  probe seams.
- `cache/cachememory/backend_test.go` and its conformance test cover ownership,
  LRU, expiry, limits, cancellation and observer behaviour.
