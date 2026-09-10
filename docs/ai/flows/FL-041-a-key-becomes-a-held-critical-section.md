# FL-041 — A key becomes a held critical section

A caller names something that must not happen twice at once — a deployment-wide decision, one
document, one schema migration, one retention sweep. The name becomes an `int64`, the `int64`
becomes a PostgreSQL advisory lock, and the lock is released by the database rather than by the
code that took it.

## Naming

`vvdb/lock/key.go:KeyOf` hashes NUL-separated parts with FNV-1a. `vvdb/lock/key.go:KeyFrom` is the
way in for a driver that already derives its own key — `jobspg` and `auditpg` both do, with
SHA-256 over a versioned prefix, and keep doing it: only the mechanism is shared, not the
derivation. `vvdb/lock/key.go:Exclusively` and `vvdb/lock/key.go:Sharing` pair a key with the mode.

## Binding to an engine

`vvdb/lock/lock.go:For` reads `crud.Source`'s dialect once and hands it to
`vvdb/lock/postgres.go:backendFor`, which answers with the statement set for PostgreSQL and
`vvdb/lock/errors.go:ErrDialectUnsupported` for everything else. The refusal is here, at
construction, rather than at the first statement — see [[D-139]].

The statements themselves live in `vvdb/lock/postgres.go:postgresTake`,
`vvdb/lock/postgres.go:postgresTry` and `vvdb/lock/postgres.go:postgresTimeout`, and nowhere else.

## Taking

`vvdb/lock/lock.go:Take` refuses an executor that is not a transaction
(`vvdb/lock/errors.go:ErrNoTransaction`), sets `lock_timeout` once for the transaction, then walks
`vvdb/lock/lock.go:ordered` — ascending key, exclusive before shared, duplicates collapsed — and
issues one statement per guard. `vvdb/lock/lock.go:TryTake` is the non-blocking half and reads the
boolean the engine answers with.

## Owning the transaction

`vvdb/lock/lock.go:Guarded` opens a transaction with `crud.InNewTx`, resolves the executor bound to
that source with `crud.SourceBoundExecutorFor`, takes the guards and runs the body. It wraps the
whole thing in `vvdb/lock/lock.go:Retry`, which asks `vvdb/lock/retryable.go:RetryCode` whether the
failure is one a second attempt fixes — the answer comes from `errs/sqlerr`'s per-dialect table,
not from a list kept here — and backs off through `vvdb/lock/lock.go:backoff` and
`vvdb/lock/lock.go:sleep`.

`Retry` is exported on its own for a caller whose transaction is opened elsewhere and still owned
by it.

## The database/sql side

A driver holding `*sql.Tx` rather than a `crud.Source` goes through
`vvdb/lock/locksql/tx.go:Take` and `vvdb/lock/locksql/tx.go:TryTake`, which wrap the transaction
with `crudsql` and delegate — so the ordering, the timeout and the refusal are the same code.

A lock that must outlive any one transaction goes through `vvdb/lock/locksql/locksql.go:Hold`: it
pins a `*sql.Conn`, takes a session lock in a jittered try-loop, runs the work, and on an unlock
that answers false (`vvdb/lock/locksql/locksql.go:ErrNotHeld`) poisons the connection rather than
returning it to the pool still holding a lock nobody will release.

## Who takes what

- `event/eventpg/migration.go:withMigrationLock` — one replica migrates the event schema.
- `jobs/jobspg/retention_migration.go:withMigrationLock` — the same, for the retention schema.
- `jobs/jobspg/repo_ops.go:lockIntentKeys` — every intent key a delivery touches, in one order.
- `jobs/jobspg/retention_repo.go:tryRetentionLeadership` — whoever gets it sweeps; the losers
  return without sweeping rather than queueing.
- `audit/auditpg/deployment.go` — the audit schema migration.

`audit/auditpg/attempt_lock.go:Store.LockAttemptType` deliberately does not. It hands the caller an
`audit.AttemptTypeLease` holding an open transaction to release later, which no callback-shaped
API expresses; it keeps its own statement.

## Where the decisions bite

- [[D-139]] — why `Guarded` may retry and `Take` may not, why an unserved engine is refused rather
  than answered weakly, why the order is fixed and why `KeyOf` is frozen.
- [[D-040]] — the rule D-139 narrows: a retryable class is a 503, and the framework does not retry
  on the caller's behalf anywhere else.
- [[D-046]] — how the SQLSTATE that decides a retry is classified.

## Files

| File | What |
|---|---|
| `vvdb/lock/key.go` | `Key`, `KeyOf`, `KeyFrom`, `Guard`, `Exclusively`, `Sharing` |
| `vvdb/lock/lock.go` | `Policy`, `Locks`, `For`, `Take`, `TryTake`, `Guarded`, `Retry`, `ordered`, `backoff`, `sleep` |
| `vvdb/lock/postgres.go` | `backendFor` and the three statement builders |
| `vvdb/lock/retryable.go` | `Retryable`, `RetryCode` |
| `vvdb/lock/errors.go` | `ErrNoTransaction`, `ErrDialectUnsupported` |
| `vvdb/lock/locksql/locksql.go` | `Hold`, `ErrNotHeld` |
| `vvdb/lock/locksql/tx.go` | `In`, `Take`, `TryTake` |
| `event/eventpg/migration.go` | the event schema migration lock |
| `jobs/jobspg/repo_ops.go` | the intent locks |
| `jobs/jobspg/retention_repo.go` | the retention leadership try |
| `jobs/jobspg/retention_migration.go` | the retention schema migration lock |
| `audit/auditpg/deployment.go` | the audit schema migration lock |
| `audit/auditpg/attempt_lock.go` | the one that keeps its own, and why |

## Tests that walk this flow

- `TestGuardsAreTakenInOneOrderWhicheverOrderTheCallerAsked` and
  `TestTheSameKeyAskedForBothWaysIsTakenOnceAndExclusively` in `vvdb/lock/lock_test.go` — the order.
- `TestAnEngineWithoutTheseSemanticsIsRefusedRatherThanAnsweredWeakly` — the refusal, with a
  PostgreSQL control.
- `TestGuardedRunsTheWholeTransactionAgainWhenTheEngineSaysToTryAgain`,
  `TestGuardedHandsBackWhatASecondAttemptWillNotFix` and
  `TestGuardedStopsRetryingWhenTheCallerGivesUp` — the retry and its three edges.
- `TestALockFailureIsAnswerableAsRetryableAtTheBoundary` — the 503 rather than the 500.
- `TestTwoDatabasesDoNotShareACriticalSection` — one source, one keyspace.
- `TestKeyOfStillAnswersWhatItAlwaysAnswered` — the frozen numbers.
- `TestTakingALockSetsTheTimeoutBeforeItWaits` and
  `TestTheTimeoutIsSetOnceHoweverManyGuardsAreTaken` — `lock_timeout`.
