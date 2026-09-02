# D-095 — `ResolveMany` writes nothing until the whole batch fits

**Status:** accepted
**Invariant:** `ResolveMany` reads once, calls its batch loader at most once
with the deduplicated missing keys in first-seen order, encodes every answer and
proves the cumulative encoded size against `MaxBatchResultBytes` before the
first backend write, and fails the whole call rather than returning a partial
one. It preserves caller order and duplicates.

## The decision

`Resolve` in a loop is N round trips to the loader and N to the backend, and
each of those loops is written slightly differently in each application. The
batch shape belongs in the cache. What it must not do is inherit the two habits
that make batch APIs dangerous: writing as it goes, and answering partially.

The loader is typed as `BatchLoader[K, V] func(ctx, []K) ([]LoadResult[V],
error)` — positional, one result per key. A map would demand a comparable `K`,
which the rest of the package deliberately does not; a positional answer needs
no constraint and makes a short answer a detectable contract breach rather than
a silent absence. A loader that returns the wrong count, an unset `Presence`, an
error or a panic fails the entire call, and nothing has been written by then.

Bounds are proved in two stages, both before any write. Keys are deduplicated by
address and charged against `MaxBatchKeyBytes` while the plan is built; every
loaded answer is encoded and its envelope charged against
`MaxBatchResultBytes` before the loop that stores them starts. A batch whose
answers do not collectively fit is an `ErrTooLarge` with an untouched backend —
not four stored values and a failure on the fifth.

Writes then go through the ordinary per-address mutation fence, the same one
`Put` uses: the generation is bumped, an in-flight loader for that address is
invalidated, and a superseded write is a no-op rather than an error. That is the
deliberate limit of this API and it is worth naming: **`ResolveMany` does not
join per-address flights.** Two concurrent batch resolves may each call their
loader. Coalescing a batch across callers would mean holding coordination state
for every address in the batch across one loader call, which is a lock-ordering
problem with a deadlock at the end of it; per-address `Resolve` remains the
API that coalesces.

`ResolveMany` fills misses. A `Stale` entry is returned stale and is refreshed
by `Resolve`, because a batch loader has no way to express "refresh these three
but serve the old value if you fail" without exactly the partial-failure
semantics this decision refuses.

## What it forbids

- Do not write any envelope before the whole batch has been encoded and
  charged.
- Do not return a partially filled result slice with an error.
- Do not call the loader more than once, and do not call it for a key the read
  already answered.
- Do not assume a native batch read is atomic; the merged response is validated
  per item and in aggregate exactly as `LookupMany` validates it.
- Do not claim cross-caller single-flight for this call.

## Where it lives

- `cache/resolve_many.go` — the plan, the single loader call, the encode-then-
  write order and the mutation fence.
- `cache/mutation.go` — `beginMutationAs` / `commitMutationAs`, the fence with
  the reporting operation as a parameter so a batch load is observed as
  `load_many` rather than as a `put`.
- `cache/lookup.go` — the read phase it reuses unchanged.

## Proven by

- `cache/resolve_many_test.go` — input order and duplicates survive while each
  address is asked for once; only missing keys reach the loader; what it loaded
  is in the cache afterwards; a loader error, short answer, unset presence or
  panic refuses the batch with nothing written; answers that exceed the
  cumulative budget refuse with nothing written; too many keys refuse before the
  loader runs; a confirmed absence becomes a stored negative.

## See also

[[D-084]] [[D-085]] [[D-094]] [[FL-025]]
