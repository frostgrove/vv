# FL-025 — A cache declaration becomes a bounded value

**Entry points:** `cache.Auto`, `cache.Define`, `cache.Activate`,
`cache.Cache.Resolve`
**Governed by:** [[D-021]] [[D-084]] [[D-085]]

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

## Observer and context boundary

`cache/cache.go:observe` contains observer panic and invokes callbacks outside
cache locks. Ordinary operation events may carry the operation caller's context.
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
| `cache/policy.go`, `descriptor.go` | profiles, explicit overrides and inspectable effective policy |
| `cache/address.go`, `scope.go`, `key.go` | typed identity and fixed backend address |
| `cache/transient.go` | permanent waiter reservation and per-operation admission |
| `cache/lookup.go`, `resolve.go`, `mutation.go` | read, local flight and mutation fence |
| `cache/envelope.go`, `codec.go` | versioned wire value and bounded codecs |
| `cache/context.go`, `runtime.go` | injected clock, finite contexts and watcher teardown |
| `cache/cachememory/backend.go` | bounded process backend |

## Tests that walk this flow

- `cache/activation_test.go` covers all-or-nothing publication and provider
  selection.
- `cache/coordination_test.go`, `cache/review_regression_test.go` and
  `cache/transient_test.go` cover flight races, mutation fences, context
  isolation, cancellation, saturation, timers and bounded codecs.
- `cache/batch_test.go` covers capability and fallback parity.
- `cache/cachememory/backend_test.go` and its conformance test cover ownership,
  LRU, expiry, limits, cancellation and observer behaviour.
