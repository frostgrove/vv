# D-084 — Shared cache flights do not inherit a waiter's context

**Status:** accepted
**Invariant:** A cache flight shared by callers starts from a value-free
background context; its loader and backend contexts have finite framework-owned
deadlines, while its synchronous terminal observer is bounded by callback
contract rather than by context. No caller donates its principal, transaction,
request body, locale, memo or cancellation lifetime to another caller. The
disabled profile preserves the same value isolation and cancellation
classification.

## The decision

A miss is often discovered by an HTTP request, but the resulting loader is not
request-owned once another caller can join it or the policy can finish it after
the last waiter leaves. `context.WithoutCancel(firstCaller)` is therefore not a
safe base: it strips cancellation while retaining every value. The first
request's principal could authorize a load returned to another principal, its
transaction could be used after the request ends, and a large body or memo graph
could stay live for the full shared flight.

Every registered flight instead starts at `context.Background`. The runtime
adds a finite loader deadline. The backend write uses another detached,
value-free context with a finite backend deadline. The terminal `Load` observer
is detached and value-free; because its API is synchronous, bounded execution
is the observer's contract rather than a context deadline. Each waiter
separately watches its caller context. Cancelling one waiter stops that wait, and
`CancelLoader` cancels the group only when the last waiter has gone;
`FinishLoader` leaves the already admitted group running to its finite deadline.

There is no generic context-value projector. A whitelist of untyped keys would
still hide ownership, cardinality and retention, and would turn application
security into cache configuration. Data required to compute the value belongs
in the typed key or in an explicit loader dependency. Future tracing may attach
bounded links to shared work, but it may not restore arbitrary baggage or make
one request the parent of every waiter.

Direct lookup, put and forget are not shared flights. Their backend calls may
use caller context, including a verified transaction binding, because their
effect belongs to that operation. Their context is not retained after it.

`Disabled` skips storage and coordination, not the boundary. Its loader gets a
value-blind view of the caller's cancellation and deadline, combined with the
runtime loader timeout. Caller cancellation has the same precedence and error
classification as an enabled resolve. This lets operators disable caching
without changing authorization leakage or timeout behaviour.

## Observer consequence

A terminal load belongs to the shared flight, not to one waiter, so its event
has no request values. Observers must treat it as metrics-oriented. They run
synchronously after load work and before the admitted flight is fully released;
therefore they must be bounded, non-blocking and must not call `Resolve` on the
same cache. Releasing the slot first would permit unbounded callbacks, and
spawning an unowned goroutine would only move that leak elsewhere. `Stats`
inspection is safe.

## What it forbids

- Do not base a shared loader on the first caller with `WithoutCancel`.
- Do not preserve principals, transaction handles, bodies or arbitrary baggage
  through a generic projector.
- Do not let an observer infer a single request parent for shared work.
- Do not make the disabled profile a raw call with different cancellation or
  context-value semantics.
- Do not re-enter the emitting cache from its synchronous observer.

## Where it lives

- `cache/resolve.go` — flight registration, waiter cancellation and disabled
  load parity.
- `cache/context.go` — value-blind and finite runtime contexts.
- `cache/mutation.go` and `cache/lookup.go` — caller-owned direct operations.
- `cache/transient_test.go` — principal/body/transaction canaries, cancellation
  precedence, timeout and watcher teardown.

## See also

[[D-055]] [[D-077]] [[D-085]] [[FL-025]]
