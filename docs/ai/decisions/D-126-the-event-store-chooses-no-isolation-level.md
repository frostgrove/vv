# D-126 — The event store chooses no isolation level, and admission is a database constraint

**Status:** accepted
**Invariant:** `event/eventpg` issues no `SET TRANSACTION`, takes no
`SELECT … FOR UPDATE`, opens no transaction outside `migration.go`, and reads no
stream's version into Go to decide whether to write. Expected-version admission
is `WHERE s.version = $expected` evaluated by PostgreSQL against a row it has
locked, under `UNIQUE (family, key, version)`. A conflict is a **row count of
zero** and nothing else; `40001`, `40P01` and `55P03` are backend failures with a
retryable cause and are never reported as conflicts.

## The decision

Three things a maintainer will reach for, refused for one reason each.

**`SERIALIZABLE`, or any level the store selects for itself.** The transaction is
the caller's throughout ([[D-118]]): a store that opened one, or set a level on
one it did not open, would be deciding an application-wide trade the application
already decided. `eventpg` is correct at all three levels and the *only* thing
that differs is what the loser of a race is told — `ErrConflict` at
`READ COMMITTED`, and `ErrBackend` with a retryable cause at `REPEATABLE READ`
and `SERIALIZABLE`, because PostgreSQL raises `40001` before the conditional
update can answer zero rows. That difference is documented on the module page and
pinned by a test that runs one losing append at all three levels; it is not
smoothed over by picking a level.

**`SELECT … FOR UPDATE` before the insert.** It reads the version into Go and
then decides, which is correct only against a snapshot nobody else can move. It
also costs a second statement, and the whole of this store's atomicity is that an
append is **one** statement: a single statement is atomic in PostgreSQL whether
or not it runs inside an explicit transaction, which is why the version advance
and the event rows commit or roll back together on the pool exactly as they do
inside the caller's transaction. Two statements would need a transaction the
store is forbidden to open, and it would leave `streams.version` and `events`
able to disagree — which is what the post-suite audit exists to detect.

**Reading `40001` as a conflict "because the writer lost anyway".** It is the
most tempting simplification here and it inverts the caller's obligation.
`ErrConflict` means *the stream moved: re-read, re-decide, and expect the same
answer next time.* A serialisation failure means *nothing is wrong with your
decision; the same operation is likely to succeed if you repeat it.* A caller
that reads the second as the first re-derives a decision it did not need to
re-derive; a caller that reads the first as the second retries a losing append
for ever. So `Conflict` is selected on `RowsAffected() == 0` and on nothing else,
and the three retryable SQLSTATEs get an `*errs.Fault` of kind
`errs.KindRetryable` — the second spelling of retryability the kernel already
reads.

## Why zero rows and not a unique violation

The admitting CTE writes one row per record when the predicate held and no rows
at all when it did not, so a lost race arrives as a **count**, not as an error to
be caught and classified. A fresh stream takes the speculative insertion, so two
writers at version 0 do not race into `23505` either: the second blocks on the
speculative tuple, takes the `DO UPDATE` path, fails `WHERE s.version = $expected`
and writes nothing.

`UNIQUE (family, key, version)` is still there, and it is not decoration. It is
what holds if the predicate is ever wrong: a duplicate arrives as `23505` →
`NotWritten` → `ErrBackend`, loudly, instead of as a second event at one version.
Verification refuses to start against a schema that has lost it.

## What it forbids

- Do not add `Spec.TxOptions`, `Spec.IsolationLevel` or any other way for the
  store to choose a level. None of the eight methods begins a transaction.
- Do not begin, commit or roll back anything outside `event/eventpg/migration.go`.
  The source check that holds this excludes that file **by name** and fails if the
  file does not exist, so a rename cannot turn the exclusion into a blanket
  exemption.
- Do not read `streams.version` into Go on any path an append reaches.
- Do not map `40001`, `40P01` or `55P03` to `Conflict`, and do not drop their
  retryable cause.
- Do not turn the append into two statements, however much clearer the second one
  reads.
- Do not add a retry, a backoff or a circuit breaker ([[D-040]]).

## Where it lives

- `event/eventpg/append.go` — `Store.Append` and `Store.appendStatement`: the one
  statement, and what its row count means.
- `event/eventpg/classify.go` — `outcomeOf` and `causeOf`: the rule with no
  default branch, and the three codes that earn a retryable kind.
- `event/eventpg/executor.go` — `Store.onExecutor`: a bound `*sql.Tx` or a
  checked-out `*sql.Conn`, never the pool.
- `event/eventpg/schema.go` — the `UNIQUE (family, key, version)` the predicate is
  backed by, and the `CHECK (version > 0)` that makes a version above
  `math.MaxInt64` unreachable.
- `event/eventpg/migration.go` — the one file that opens a transaction, and DDL
  is the one place there is no level worth choosing.

## Proven by

- `TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts` — eight racing
  writers, one winner, seven `ErrConflict`, one event on the stream, with a single
  writer afterwards as the control.
- `TestTheIsolationMatrixTellsAConflictFromASerialisationFailure` — the same
  losing append at all three levels: `ErrConflict` for the first and `ErrBackend`
  matching `crud.ErrUnavailable` for the other two. Each is the other's control.
- `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` — the whole
  rule driven from real driver errors, including a real `55P03` from a
  `lock_timeout` against a held row lock and a real `40P01` from a real cycle;
  narrowing `causeOf`'s retryable arm to `40001` alone fails it.
- `TestNoDoorOpensCommitsOrRollsBackAnything` and
  `TestNoStatementReadsTheStreamVersionIntoGo` — source checks over the package's
  non-test files, each with a fixture the check must report so neither can pass by
  walking nothing.
- `TestNoStatementIsIssuedOnTheDatabaseHandle` and
  `TestAKilledBackendAnswersUncertainAfterExactlyOneExecContext` — the append
  reaches a driver exactly once; issuing it on the pool makes that three.
- `TestTheAuditOverTheWholeSchemaHolds` — over a schema several writers raced
  into: `streams.version = max(events.version)` for every stream, no event row
  outside a stream, and per stream position order equal to version order. Each of
  the three is falsified by a planted violation in its own case.
- `TestVerificationRefusesEveryMutationAndPassesTheIntactSchema` — the
  `unique-dropped` case: a schema that has lost the constraint refuses at
  `Prepare` rather than being trusted to race correctly.

## See also

[[D-040]] [[D-101]] [[D-118]] [[D-121]] [[D-127]] [[FL-037]] [[UC-032]]
