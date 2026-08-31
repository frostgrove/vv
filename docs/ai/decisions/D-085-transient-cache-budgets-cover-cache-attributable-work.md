# D-085 — Transient cache budgets cover cache-attributable work

**Status:** accepted
**Invariant:** Before a cache operation creates owned coordination, timers,
copies, encoded values or decode destinations, it must hold enough typed
transient admission for that operation. Waiters are finite and permanently
backed by reserved bytes. The bound covers work newly attributable to the cache,
not pre-existing runtime pools or caller-owned object graphs, and explicit
trusted hooks are outside it.

## The decision

Limits on stored value bytes are not a memory bound. A cache can stay under its
backend item limit while thousands of misses allocate keys, timers, envelopes,
JSON destinations and singleflight members at once. A semaphore counting only
loaders is also insufficient: a blocked waiter has state and a timer, and an
encode/decode path may need more transient memory than the stored payload.

Each typed cache therefore derives a conservative charge plan for lookup,
batch, load, put and forget. The plan includes fixed coordination and timer
reserves, key/envelope/value copies and codec-specific decoded allocation.
`MaxTransientBytes` is the total admitted charge. `MaxTransientWaiters` is a
separate finite population whose exact charge is permanently removed from the
general pool, so a flood cannot first spend all bytes and then claim waiter
slots that no longer have backing memory. The public descriptor exposes the
reserved amount.

Admission occurs before address ownership, state-map insertion, timers and
backend calls. A reject policy fails immediately. A wait policy can only enter a
pre-reserved slot, waits through the injected finite clock and either atomically
obtains the operation charge or returns saturation. Every timer and watcher is
stopped and joined before its lease is released, so observed zero usage means
the operation no longer has framework-owned background work.

`MaxFlights` remains a distinct logical cap on loader groups. Byte admission
cannot replace it: two small loaders may still be an unacceptable backend load,
while one large decode may exhaust bytes without using another flight. Profiles
set both and expose both.

## Wire and decoded value limits

`ValueLimit.MaxBytes` is the encoded wire limit. `MaxDecodedBytes` is a separate
conservative model of allocations required by the decoded value. Treating their
minimum as both limits rejects a safely escaped wire representation merely
because it is longer than its decoded string; treating wire bytes alone as the
allocation bound undercharges maps, slices, pointers and decoder scratch.

The safe JSON codec validates the reachable type at construction and the value
graph before encode. It bounds type nodes/edges/depth, runtime visits, field
scans, text, nesting and output, rejects interfaces, custom JSON/text hooks,
unsupported kinds and time shapes, and preflights the encoded output against the
same decoded-allocation model before returning it. Decode scans syntax and raw
UTF-8, calculates the destination charge and only then invokes `encoding/json`.
`RFC3339UTC` gives time a fixed decoded charge and independent wire bound. Safe
JSON is refused at activation under `GOEXPERIMENT=jsonv2` until that runtime's
allocation behaviour is proven separately.

`TrustedJSON` permits hooks and dynamic interfaces. Its limited writer bounds
the resulting wire and its postflight checks decoded allocation and depth, but
the encode-side dynamic graph, traversal, type-cache work and hook
implementations execute before that postflight and are application code. Decode
preflight bounds the destination shell before unmarshal, but a trusted decode
hook body is application code too. The dynamic encode path and every trusted
hook body are explicitly outside the hard bounded-work claim. A custom `Codec`
has the same responsibility and can publish an additional decoded charge
through its codec contract.

## Ownership boundary

The budget covers allocations, reservations and retained state newly caused by
the cache operation. It does not retroactively own:

- buffers already retained in Go runtime or standard-library global pools;
- the caller's key object before the cache makes its bounded representation;
- loader closures and dependencies the application already owns;
- values reachable from the caller context; shared flights do not retain those
  values under [[D-084]];
- work and allocations performed inside application loader and observer
  callbacks. The cache-created contexts, timers, result reserves and
  coordination around those callbacks remain inside the bound;
- work and allocations performed by consumer key functions, partitioners,
  backends, clocks and randomness providers. Cache-owned copies, call wrappers
  and retained state around those extension points remain inside the bound.

This is an ownership statement, not a process-RSS promise. A process-wide RSS
limit belongs at deployment/runtime level; the cache promise is that admitted
cache work cannot independently multiply without crossing its declared cap.

## What it forbids

- Do not create coordination state or a timer before transient admission.
- Do not count waiters without reserving their byte charge.
- Do not describe `MaxFlights` as a memory bound or transient bytes as a loader
  count.
- Do not collapse wire and decoded limits into one minimum.
- Do not call a user hook and then claim its allocations were bounded.
- Do not release a lease while a framework timer watcher can still run.

## Where it lives

- `cache/transient.go` and `cache/policy.go` — typed plans, reservation,
  admission and profile defaults.
- `cache/codec.go` — codec descriptors and bounded JSON/RFC3339 work.
- `cache/lookup.go`, `cache/resolve.go`, `cache/mutation.go` — admission order.
- `cache/descriptor.go` — observable total and reserved budgets.
- `cache/transient_test.go` — exact-boundary, saturation, JSON graph and
  quiescence proofs.

## See also

[[D-020]] [[D-063]] [[D-084]] [[FL-025]]
