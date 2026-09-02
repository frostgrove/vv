# D-094 — An execution memo remembers stored envelopes only

**Status:** accepted
**Invariant:** A `cache.Memo` is installed explicitly on one execution's
context, is bounded by entries and retained bytes, holds copied encoded
envelopes and nothing else, never remembers a backend miss, an error or a read
its own coordination discarded, is dropped for any address the same execution
writes, and answers nothing at all after `Close`.

## The decision

One HTTP request or one job attempt reads the same handful of cache addresses
several times. Each of those reads is a backend round trip and a decode, and
the backend is the part that is shared, remote and rate-limited. An L0 memo in
front of it is worth having — and is also the easiest place in the system to
build a second, unbounded, process-wide cache with no expiry by accident.

So the memo is a value, not a global. `NewMemo` takes an entry count and a byte
ceiling and refuses anything outside them. `WithMemo` puts it on a context, and
the execution that created it closes it. `Close` is idempotent, empties what it
held and turns every later read and write into a no-op — which is also why a
goroutine that outlived its request and kept the context cannot keep a document
alive: after the barrier there is nothing to keep.

What it stores is the encoded envelope, copied in and copied out. Not the
decoded value: two callers in one request must not share a mutable graph, and
freshness must be recomputed against the clock on every read, so an envelope
that was a `Hit` at the top of a request can legitimately answer `Stale` at the
bottom of it. Decoding twice is the price of both properties and it is the right
price.

What it does not store is the distinction the contract turns on. A **backend
miss** is not an answer; it is the absence of one, and a concurrent writer may
be filling it right now. Remembering it would make a request blind to its own
system for its whole lifetime. A **negative envelope** — an absence the loader
confirmed and the cache stored — is an answer, and it is remembered like any
other. Errors, corruption and oversized envelopes are never remembered, and a
memoized envelope that fails to decode is dropped before the corruption policy
runs.

What it also does not store is a read that lost its race. A lookup takes a
generation ticket before it reads and re-checks it afterwards; a concurrent
`Put` or `Forget` invalidates the ticket and the lookup reads again. The envelope
from the discarded attempt is a value from before the write, and remembering it
would outlive the retry that exists to replace it — the memo refuses to
overwrite a key it already holds, so the first, superseded envelope would be the
one the rest of the execution reads, and a second lookup would answer with a
value older than the first. So the currency check and the memo fill are one
operation under the coordination lock: `confirmReadAndMemoize` and
`confirmBatchReadAndMemoize` either confirm the read and remember it or report
that the caller must read again, and nothing is remembered in between.

Within one execution the first confirmed observation of an address stands until
that execution writes. That is what makes a repeated read cheap and stable, and
it is only sound because every stored envelope was current when it was stored.

Loads bypass the memo entirely. `Resolve` and `ResolveMany` read through to the
backend and drop the memo entry for every address they touch, because a loader
that writes must not leave the execution reading what it just replaced. `Put`
and `Forget` drop their address for the same reason. The memo is therefore an
L0 for reads and an invalidation target for writes, never a write-back layer.

The memo's bytes are the memo's own budget. They are not charged to
`MaxTransientBytes`: [[D-085]] bounds work the cache creates for one operation,
and the memo is retained by the execution across many.

## What it forbids

- Do not memoize a backend miss, an error, a corrupt envelope or a decoded
  value.
- Do not memoize before the read's generation is confirmed, and do not confirm
  it in one operation and remember it in another.
- Do not create a memo implicitly, per process, per cache or per goroutine.
- Do not let `jobs` or any other subsystem manufacture one; an execution entry
  point installs it, or there is none.
- Do not hand out the stored slice; copy on the way in and on the way out.
- Do not resurrect a closed memo, and do not treat `Close` as a hint.

## Where it lives

- `cache/memo.go` — the bounded container, the context seam and the close
  barrier.
- `cache/lookup.go` — the consult before coordination, the confirmed fill in
  `lookupStableStateAdmitted` and `LookupMany`, and the batch split in
  `batchGet`, which now returns the addresses it actually read.
- `cache/memo.go` — `confirmReadAndMemoize` and `confirmBatchReadAndMemoize`,
  where the generation check and the fill happen under one lock.
- `cache/mutation.go`, `cache/resolve.go`, `cache/resolve_many.go` — the drop on
  every write path.

## Proven by

- `cache/memo_test.go` — a second lookup in one execution never reaches the
  backend; a miss is not remembered and a confirmed absence is; `Put`, `Forget`
  and `Resolve` each drop what the memo held; a closed memo answers nothing and
  reports itself empty; entry bounds refuse rather than evict; `LookupMany` asks
  the backend only for what the memo lacks; a context without a memo behaves
  exactly as before.
- `cache/memo_currency_test.go` — a write that lands while a lookup is inside
  the backend makes that lookup read again, and neither the retried lookup nor
  the next one answers from the envelope the coordination discarded; the same
  for `LookupMany`, which would otherwise return the pre-write value from its
  own memo on the second round.

## See also

[[D-084]] [[D-085]] [[D-095]] [[FL-025]]
