# D-010 — `Update` is load-diff-write; safety against a concurrent writer is explicit

**Status:** accepted
**Invariant:** `Update` must write only the columns whose value actually differs from the row it loaded, and must take a row lock only when the context already carries a transaction.

## The decision

`repository.Update` loads the row by id (through whatever narrowing the options
carry), diffs the DTO against it, and issues an `UPDATE` for the changed columns
only. If nothing changed, the loaded row is returned and no statement is sent.

Concurrency safety is opt-in, in two independent forms:

- Inside a transaction, the load adds `FOR UPDATE`.
- With an integer column tagged `version`, the write pins itself to the version
  it read and bumps the counter, so the loser gets `crud.ErrStaleVersion`.

Neither is automatic beyond that. `Update` outside a transaction on a model
without a version column is last-writer-wins, and says so.

## Why

**Why load-diff-write at all.** A partial update DTO says which fields were
*provided*; it cannot say which of them differ from what is stored. Writing all
provided fields makes every PATCH touch every named column, which defeats
column-level triggers, inflates replication and WAL, and turns `UPDATE … SET
name = 'Ann'` on a row already named Ann into a write. The diff is also what
makes `updated_at` triggers meaningful.

**Why not always lock.** `FOR UPDATE` outside a transaction is pointless — the
lock is released with the implicit commit of the `SELECT`, before the `UPDATE`
runs — so adding it would cost a round trip's worth of contention and buy
nothing. Making `Update` open its own transaction instead was rejected because
it takes the transaction boundary away from the caller: two repository calls
that the caller wanted in one transaction would silently become two, and a
caller who *is* in a transaction would get a nested one. So the rule is: if you
are already in a transaction, the lock is free and it is taken; if you are not,
`crud.ExecutorFor` says so and nothing is locked.

**Why a version column outside a transaction.** It is the only mechanism that
works without holding anything: the counter is data, not a lock. Two servers do
not share a clock, which is why a timestamp version is refused
(`crud/meta.go:checkVersion`), but they do share the row.

**Why the version is not in `Schema.Update`.** It is not the caller's to set. An
update DTO naming it is refused, `version = version + 1` is written by the
repository, and `Save`'s conflict clause leaves it alone — so an upsert built
from a stale model cannot wind the counter back.

**Why an `UPDATE` that matched nothing needs two answers.** A row that is gone
is `ErrNotFound` and the caller should stop; a row that moved on is
`ErrStaleVersion` and the caller should read it again and reapply. Collapsing
them into one error makes the retry loop either impossible or infinite.
`repository.missedRow` re-checks existence to tell them apart.

**Why MySQL re-reads.** `RowsAffected` there cannot distinguish "no such row"
from "nothing to do", so patching the loaded model in memory reported success —
with a fabricated model — for a row deleted between the load and the write.
Re-reading is what `RETURNING` gives PostgreSQL for free. With a version column
the count *is* trustworthy, because the counter is always one of the changes, so
zero rows means the row is not the one that was read — and there the re-read is
skipped, because it would hand the caller somebody else's write as if it were
their own.

**Why the narrowing is in both halves.** Checking on the load and writing
unscoped is check-then-act. See [[D-008]].

## What it forbids

- Do not make `Update` open its own transaction to take a lock. The boundary
  belongs to the caller.
- Do not add `FOR UPDATE` unconditionally.
- Do not put the version column into `Schema.Update` or let a DTO set it.
- Do not accept a non-integer version column, a version on the primary key, a
  second version column, or `version` combined with `immutable` or `generated`.
  Each of those fails silently at run time; `checkVersion` refuses them at
  declaration time.
- Do not collapse `ErrStaleVersion` and `ErrNotFound`.
- Do not skip the MySQL re-read as an optimisation.
- `UpdateAll` is the filtered partner and deliberately does *not* diff: there is
  no single row to diff against, so every field the DTO defines is written to
  every matching row. It still bumps the version of every row it touches, or a
  stale `Update` somebody else is holding would sail past the change and undo it.

## Where it lives

- `repo/basic/repository.go:repository.Update` — the load, the diff, the
  conditional `FOR UPDATE`, the version predicate, both dialect paths.
- `repo/basic/repository.go:repository.versionCheck` — pins the row to the
  version it was read at.
- `repo/basic/repository.go:repository.missedRow` — `ErrNotFound` vs
  `ErrStaleVersion`.
- `repo/basic/repository.go:repository.UpdateAll` — the filtered write, and the
  version bump without a version check.
- `crud/update.go:UpdatePlan.Changes` — the diff.
- `crud/update.go:UpdatePlan.Writes` — the no-diff form `UpdateAll` uses.
- `crud/update.go:valuesEqual` — value comparison, including slices and
  `time.Time` (a time that differs only in its monotonic clock reading is not a
  change).
- `crud/meta.go:TagKey` — the `version` option, documented with the rest.
- `crud/meta.go:checkVersion` — the declarations an optimistic lock cannot be
  built on.
- `crud/meta.go:buildSchema` — keeps `version` out of `Schema.Update`.
- `crud/dialect.go:Dialect.LockClause` — and `SQLite.LockClause` returning empty,
  because SQLite has no row locks ([[D-019]]).

## Proven by

- `TestUpdateWritesOnlyChangedFields` in `repo/basic/repository_test.go`.
- `TestUpdateWithNothingToDoSkipsTheWrite` in `repo/basic/repository_test.go`.
- `TestATimeThatOnlyDiffersInItsClockReadingIsNotAChange` in
  `crud/edge_test.go`.
- `TestUpdateChecksTheVersionItReadAndAdvancesIt` in `repo/basic/version_test.go`.
- `TestAConcurrentWriteIsRefusedRatherThanLost` in
  `test/integration/dialect_edge_test.go` — two real writers, against both
  engines. This is the one that would catch a version predicate that renders but
  does not bind.
- `TestAVanishedRowIsStillNotFoundRatherThanStale` in
  `repo/basic/version_test.go` — the two answers stay distinct.
- `TestUpdateOfARowThatVanishedIsNotFoundOnEveryDialect` in
  `test/integration/dialect_edge_test.go` — the MySQL re-read.
- `TestUpdateOnADialectWithoutRETURNINGReadsTheRowBack` in
  `repo/basic/repository_test.go`.
- `TestAnUpdateWithNothingToDoLeavesTheVersionAlone` in
  `repo/basic/version_test.go`.
- `TestAnUpdateDTOCannotSetTheVersion` in `crud/version_test.go`.
- `TestADeclarationThatCannotBeALockIsRefused` in `crud/version_test.go` — every
  refused shape.
- `TestUpdateAllAdvancesTheVersionOfEveryRowItWrites` in
  `repo/basic/version_test.go`.
- `TestAFilteredUpdateIsAlsoNoticedByTheLock` in
  `test/integration/dialect_edge_test.go`.
- `TestForUpdateMakesTwoTransactionsTakeTurns` in
  `test/integration/edge_test.go` — the lock actually serialises.
- `TestForUpdateIsANoOpOnSQLite` in `test/integration/driver_sqlite_test.go`.

## See also

[[D-002]] [[D-008]] [[D-009]] [[D-011]] [[D-015]] [[D-019]]
