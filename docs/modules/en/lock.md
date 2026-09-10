# lock — a critical section two processes can rely on

```go
import "github.com/frostgrove/vv/vvdb/lock"         // the contract and the PostgreSQL backend
import "github.com/frostgrove/vv/vvdb/lock/locksql" // for a driver that holds database/sql handles
```

**Module:** both are in the root module — standard library and first-party only, so neither adds a
dependency ([[D-033]]) · **Depends on:** `crud`, `crud/sqlfault`, `errs`, `errs/sqlerr` ·
**Depended on by:** `jobspg`, `eventpg`, `auditpg`

A PostgreSQL advisory lock is a lock on a number you choose rather than on a row. It is what you
reach for when the thing to be serialised is a *decision* with no single row to lock — "which
languages does this installation offer", "who is sweeping retention right now", "who is migrating
this schema" — and it is held by the database, so it excludes every process talking to that
database, not just every goroutine in this one.

The lock and the data share a transaction, a connection and a failure domain. That is the whole
reason to prefer it over a lease in a cache: a TTL lease can expire while its holder is still
working, and the holder finds out never. Here the database releases the lock exactly when the
transaction ends, including when the process dies.

---

## What you get

### `lock` — the contract

| | |
|---|---|
| `Key`, `KeyOf(parts...)` | a name resolved to an `int64`, FNV-1a over NUL-separated parts |
| `KeyFrom(raw int64)` | for a caller that already derives its own key and only wants the mechanism |
| `Guard`, `Exclusively(key)`, `Sharing(key)` | one lock and the mode it is taken in |
| `Policy{Timeout, Retries}` | how long to wait for a guard, and how many times to replay a transaction that deadlocked |
| `For(source, policy) (*Locks, error)` | resolves the engine once and refuses one it cannot serve |
| `Locks.Take(ctx, exec, guards...)` | takes every guard in the caller's transaction |
| `Locks.TryTake(ctx, exec, guard)` | answers whether the guard was free, without waiting |
| `Locks.Guarded(ctx, guards, fn)` | runs `fn` in a transaction of its own that holds every guard |
| `Locks.Retry(ctx, fn)` | the retry loop on its own, for a caller that opens the transaction another way |
| `Locks.Retryable(err)`, `Locks.RetryCode(err)` | whether a failure is one a second attempt fixes, and which |
| `ErrNoTransaction`, `ErrDialectUnsupported` | the two refusals |

```go
locks, err := lock.For(source, lock.Policy{Timeout: 10 * time.Second, Retries: 3})
if err != nil {
    return err // this engine has no advisory lock with these semantics
}

err = locks.Guarded(ctx, []lock.Guard{
    lock.Sharing(deploymentKey),      // many writers read this decision at once
    lock.Exclusively(documentKey),    // one writer at a time on this document
}, func(ctx context.Context) error {
    return write(ctx)
})
```

### `locksql` — for a driver that holds `database/sql`

| | |
|---|---|
| `Take(ctx, tx, policy, guards...)` | the same as `Locks.Take`, over a `*sql.Tx` |
| `TryTake(ctx, tx, guard)` | the same as `Locks.TryTake`, over a `*sql.Tx` |
| `In(tx, policy) (*lock.Locks, crud.Executor, error)` | the two of them, when you want to keep them |
| `Hold(ctx, db, key, work)` | a **session** lock on a pinned connection, held for the whole of `work` |
| `ErrNotHeld` | the unlock answered false — the lock serialised nothing |

```go
// A schema migration spans several transactions, so its lock has to outlive any one of them.
err := locksql.Hold(ctx, db, migrationKey, func(conn *sql.Conn) error {
    return migrate(ctx, conn)
})
```

---

## One source, one keyspace

An advisory lock is scoped to one **database**. The identity of a critical section is the pair of
the database and the key, never the key alone: two `Locks` over two databases do not exclude each
other on an identical `Key`, and two over the same database do, across pools and processes. That
is why `For` binds to a source instead of the package offering free functions — a guard set cannot
accidentally span two databases, and `Guarded` opens its transaction on the very source the guards
are taken on.

A lock taken on a read replica coordinates nothing. `crud.ReadWrite` sends both `Exec` and `Query`
to the primary and exposes the replica only through `ReadSource()`, so the ordinary path is safe;
handing `For` a replica source explicitly is not.

## The order guards are taken in is not yours to choose

`Take` sorts by key and collapses duplicates to the strongest request. Two callers asking for the
same pair in opposite orders therefore send the same two statements in the same order, and cannot
deadlock against each other. Which order it is is not observable — every guard is held before the
body runs. `[[D-139]]`

## Retrying, and where it stops

`Guarded` and `Retry` replay on a deadlock, a serialisation failure, a lock timeout or a
transaction the engine aborted — the codes `errs/sqlerr` marks retryable for this dialect, read
from its versioned table rather than from a list kept here. `Take` and `TryTake` do not retry: they
run in an executor the caller supplied, and replaying somebody else's transaction is not a retry.
That split is the whole of `[[D-139]]`, which narrowed `[[D-040]]`.

`Policy.Timeout` becomes `SET LOCAL lock_timeout`, so it caps **every** lock wait in that
transaction, not only the advisory one, and reverts when the transaction ends.

## An engine that cannot do it is refused

`For` returns `ErrDialectUnsupported` for every dialect but PostgreSQL, before a single statement
is sent. MySQL's `GET_LOCK` is session-scoped where this contract is transaction-scoped, has no
shared mode and takes no part in deadlock detection; SQLite has nothing to map onto. Answering
either with a weaker lock would hand back a critical section that looks held and is not. `[[D-139]]`

## The key is frozen

`KeyOf` is FNV-1a over NUL-separated parts, and the numbers it produces are pinned by literal in
`TestKeyOfStillAnswersWhatItAlwaysAnswered`. Changing the derivation fails nothing at build time
and nothing at run time either — during a rolling deploy the old pods and the new ones simply stop
excluding each other, silently, until the rollout finishes. A new derivation is a new function
beside it, adopted in a window where no lock is held.

## See also

- [crud](crud.md) — `Source`, `Executor`, and the transaction helpers this builds on
- [sqlerr](sqlerr.md) — the per-dialect table that decides what a retry is for
- [[D-139]] · [[D-040]] · [[D-057]] · [[FL-041]] · [[UC-035]]
