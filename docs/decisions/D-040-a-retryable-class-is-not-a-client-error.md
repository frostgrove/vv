# D-040 — A retryable class is not a client error

**Status:** accepted — the classification half is in force; the `Kind` and the status arrive with phase 1 (`ROADMAP-errors.md` §14)
**Invariant:** A lock timeout, a deadlock or a serialisation failure is never classified as a conflict or any other 4xx. It carries its own `Kind`, answers 503, and the framework does not retry on the caller's behalf.

## The decision

Three engine answers say *nothing you sent is wrong; try again*: SQLSTATE class
40 (serialisation failure, deadlock), PostgreSQL's `55P03` lock timeout, MySQL's
`1205`, SQLite's `SQLITE_BUSY`. They get a `Kind` of their own and a 503 with
`Retry-After` left to the application.

The framework does not retry. It classifies and hands back.

## Why

**Because 4xx tells the client to change something, and there is nothing to
change.** A 409 on a deadlock says *your write conflicts with the stored state*,
which is false — the same request succeeds unmodified a moment later. A client
that trusts the status stops retrying and surfaces a permanent error to a user
for a transient condition.

**Because it is not a 500 either.** A 500's body deliberately says nothing
([[D-015]]), so a caller cannot tell a retryable failure from a bug, and the
sensible client behaviour for the two is opposite.

**Why the framework does not retry.** A retry has to replay the whole
transaction, and the framework does not own it — [[D-009]] and [[D-027]] exist
because a caller's executor arrives through the context and may be shared with
work the repository cannot see. Replaying a statement inside somebody else's
transaction is not a retry, it is a second attempt at a broken transaction. And
on PostgreSQL a failed statement poisons the transaction (`25P02`) so the retry
cannot even run.

The argument for the other side is real and is recorded rather than dismissed:
when the repository *does* own the transaction, nobody else can see the failure
or retry it. That case is left open in `ROADMAP-errors.md` §16 and this decision
is what it has to supersede if it wins.

**Measured, not assumed.** The corpus provokes a lock timeout on all four
engines by holding a row on one connection and waiting on another whose patience
has been cut to a fraction of a second:

| PostgreSQL | MySQL | MariaDB | SQLite |
|---|---|---|---|
| `55P03` | `HY000`/1205 | `HY000`/1205 | —/5 |

Three of the four are outside class 40 entirely, which is the same lesson
[[D-046]] draws: the class is not the gate. MySQL puts it in `HY000` — the state
its CHECK violations also use — so the number is what separates `1205` from
`3819`, and reading the number without the state would classify a lock timeout
as a constraint violation.

**Why deadlock is not in the corpus yet.** A real deadlock needs two goroutines
racing through a barrier, and a corpus entry that depends on scheduling
regenerates differently every run. `lock_timeout` is two sequential connections
and is deterministic, so it carries the class for now; phase 2 owes the rest.

## What it forbids

- Do not map class 40, `55P03`, `1205` or `SQLITE_BUSY` to `crud.ErrConflict`.
  Class 23 and class 40 are not neighbours because their numbers are close.
- Do not let them fall through to 500. A client cannot act on silence.
- Do not add a retry loop to a repository, a decorator or a binding without
  superseding this decision.
- Do not read the number without the state. On MySQL `1205` and `3819` share
  `HY000` and mean opposite things.

## Where it lives

- `adapter/crudsql/conflict.go:isIntegrity` — where these are refused today.
  The classification half of this decision is what that refusal *is*.
- `errs/sqlerr/corpus.go:KindRetryable` — the label the corpus carries.
- `errs/sqlerr/testdata/corpus/*.json`, case `lock_timeout` — the evidence.
- `test/corpus/corpus.go:Engine.Contend` — how it is provoked.

## Proven by

- `TestEveryCorpusCaseClassifiesAsTheCorpusSays` in
  `test/integration/corpus_test.go` — the `lock_timeout` arm on four live
  engines asserts it is **not** a conflict, which is this decision's whole
  content until the `Kind` exists.
- `TestAnOrdinarySQLiteErrorIsStillNotAConflict` in
  `adapter/crudsql/conflict_test.go` — `SQLITE_BUSY` (5) and busy-snapshot (517)
  by number.

## Proven by (owed)

- Phase 1 owes the `Kind` and its mapping to 503.
- Phase 2 owes the deadlock and serialisation-failure corpus entries, and
  `25P02`.

## See also

[[D-046]] [[D-015]] [[D-009]] [[D-027]]
