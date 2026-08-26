# D-032 — A replica serves reads, and never a read that decides a write

**Status:** accepted
**Invariant:** A read inside a transaction, and a read whose answer decides a write, always go to the primary.

## The decision

`crud.ReadWrite(primary, replica)` returns a `Source` that offers the replica for
reads. The repository asks for it once, at `Bind` time, through the optional
`crud.ReadSourcer` interface — so it works with any adapter and needs no setting
on the blueprint, which is deliberately datasource-independent.

Three rules, in order:

1. An executor on the context wins outright.
2. A read marked `crud.PrimaryOnly()` stays on the primary.
3. Everything else may go to the replica.

`PrimaryOnly` is set by the repository for the load half of an `Update`, and by
the security gate for every check it makes.

## Why

A replica is behind, and "behind" is only harmless for a read whose answer is
displayed. Three kinds of read it is not harmless for:

**Inside a transaction.** Joining a transaction and then reading around it
defeats the transaction, and breaks read-your-own-writes inside it. Rule 1 falls
out of `ExecutorFor` already winning — the routing did not have to invent it.

**The load half of an `Update`.** `Update` reads, diffs and writes. Served stale,
it diffs against a row as it *was* and writes the difference. `TestUpdateDiffsAgainstThePrimary`
is that exact shape.

**Every check the gate makes.** Authorising against a lagging row authorises
against a row that has moved. A row transferred out of a tenant a moment ago
would still look like theirs.

**What is deliberately left to the caller.** Write, then read in a *separate*
call before the replica catches up, and the row is missing. Nothing at this layer
can fix that without either routing every read to the primary — which is not a
replica — or tracking write timestamps per caller, which is a session concern.
Wrap the pair in a transaction, or ask with `PrimaryOnly`.

**Why two wrapper types.** `ReadWrite` returns a type with `Begin` only when the
primary has one. A wrapper that always implemented `crud.Beginner` and sometimes
refused would lie to a caller asking whether transactions work.

## What it forbids

- Do not route a read that decides a write to the replica. If a new such read
  appears, it gets `PrimaryOnly` in the same change.
- Do not let the pair swallow the primary's capabilities: `Begin` and
  `DataSource` are forwarded, and a new optional interface on `Source` has to be
  forwarded too or the pair silently downgrades the source.
- Do not promise read-your-own-writes across calls.

## Where it lives

- `crud/executor.go:ReadWrite` / `crud/executor.go:ReadSourcer`
- `crud/options.go:PrimaryOnly`
- `crud/sqlrepo/repository.go:read` — the three rules.
- `crud/sqlrepo/repository.go:Update` — `PrimaryOnly` on the load.
- `crud/decorators/security/security.go` — `PrimaryOnly` on every check.

## Proven by

- `TestReadsGoToTheReplicaAndWritesDoNot` in `test/integration/replica_test.go` —
  two databases holding *different* rows, so which rows come back names the
  datasource that answered.
- `TestUpdateDiffsAgainstThePrimary` in the same file.
- `TestAReadInsideATransactionIgnoresTheReplica` in the same file.

- `TestTheGatesAuthorisationLoadTakesThePrimary` in
  `test/integration/replica_test.go` — the gate loads the row it hands to
  `Inspect`, which decides whether the write is allowed, and that load was the one
  check in the gate with no `PrimaryOnly`. On a lagging replica a row that had
  just moved out of the caller's reach still authorised the update, and the UPDATE
  landed on the primary anyway. The two databases hold different owners, so the
  test fails with "the update was allowed on the strength of the replica's copy".

## See also

[[D-009]] [[D-010]] [[D-029]]
