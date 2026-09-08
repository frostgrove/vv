# FL-037 — A recorded fact becomes a PostgreSQL row

**Entry points:** `eventpg.New` / `Store.Prepare` (the composition root and the
deployment), `Store.Append` (the write), `Store.ReadStream` / `Store.ReadAll`
(the two reads), `eventpg.MigrationStatements` (the operator)
**Governed by:** [[D-091]] [[D-101]] [[D-118]] [[D-121]] [[D-126]] [[D-127]]

What happens between the kernel handing an `event.AppendRequest` to a store and
that fact being a row in an append-only PostgreSQL table a later process walks in
order — including the three levels a schema is verified at before anything is
written, the one statement an append is, the rule that decides *did not land*
from *nobody knows*, and the three numbers a cursor carries so that a walk never
passes a position a writer could still commit.

[[FL-036]] is the half above this one: the declaration, the fold, the token, the
six checks an append passes before a store hears about it, and the six classes a
refusal belongs to. This flow starts where that one reaches `Store.Append`.

`eventpg` is a module of its own because its live fixtures need a driver
([[D-121]]'s "what would change the answer", [[D-051]]). Its non-test files
import `database/sql`, `crud`, `crud/adapter/crudsql`, `crud/sqlfault`, `errs`,
`errs/sqlerr`, `event` and the standard library — and no driver at all.

## Construction, and what New refuses

`event/eventpg/config.go`, `event/eventpg/schema.go`:

1. `New(spec)` refuses a nil `DB`; a nil `Source` (a store that cannot see the
   caller's transaction writes beside the caller's work rather than inside it);
   a `Source` that is not `crud.SameDataSource` as the `DB`; an invalid
   `SchemaManagement`; a schema name outside 1–63 bytes of `[a-z][a-z0-9_]*`; a
   bound over a kernel ceiling; and a page above `event.ResidentPage`. Every
   refusal names the value the caller set and the one it may not pass, and every
   one wraps `ErrSpec`.
2. `Schema.Resolved` fills the four defaults — 64 KiB, 512 bytes, 64 records, a
   page of 256 or whatever the resident rule allows, whichever is smaller.
3. `event.NewBacking(backing{db, schema})` — the pool **and** the schema name
   together, comparable, so two schemas of one database are two backings and a
   cursor minted over one is refused by the other.
4. Nothing performs I/O. `New` against a pool whose DSN resolves nowhere answers
   a store, and it fails at its first operation rather than at construction.

`Schema.Fingerprint` digests a rendering the store builds **from its own
expectation model** and never one read back out of `pg_catalog` — a fingerprint
computed from the database would agree with the database by construction. The
rendering is three header lines plus one sorted ASCII line per object: `check`,
`column`, `fk`, `function`, `index`, `persistence`, `pk`, `rule`, `rls`,
`sequence`, `trigger`, `unique`. The two plpgsql function bodies are lines of it,
so two builds whose append-only trigger *does* different things no longer
fingerprint alike.

## The schema, and the three levels it is verified at

`event/eventpg/schema.go:MigrationStatements`, `event/eventpg/migration.go`,
`event/eventpg/verify.go`, `event/eventpg/catalog.go`:

1. `MigrationStatements(schema)` renders eleven statements from the same
   `expectation` the fingerprint digests, so a model change moves both and a
   DDL-rendering change moves only the first. All eleven are transactional DDL.
   Statement ten mints the log in the database and never reissues it; statement
   eleven **asserts** what ten wrote and raises SQLSTATE `EVPG1` on a mismatch.
   A schema another expectation built is refused rather than restamped.
2. `Store.Migrate` runs under `ManageSchema` only, and refuses under
   `VerifySchema` with `ErrSpec`. It takes a session advisory lock keyed by
   `sha256(schema)` on **one pinned `*sql.Conn`**, runs the eleven statements in
   one transaction on that same connection, commits, and unlocks on a context
   detached from cancellation — a lock taken over the pool serialises nothing.
3. `Store.Prepare` migrates (under `ManageSchema`), then verifies. Until it has
   returned nil the three operating doors answer
   `event.Failure(event.Refused, ErrNotReady)` — `Refused`, never `Closed`,
   because nothing was tried.
4. `Store.Verify` is three levels, failing closed at the first: the version
   integer; the fingerprint byte for byte; then `Store.inspect` over
   `pg_class`, `pg_attribute`, `pg_constraint`, `pg_trigger` joined to `pg_proc`,
   `pg_rewrite`, `pg_policy` and the identity sequence. Primary, unique and
   foreign keys are compared **structurally** (`conkey`/`confkey` resolved to
   column names in constraint order); checks are compared token by token because
   PostgreSQL parenthesises what it parsed; triggers are compared as an exact
   set, with their timing, event mask, level, enablement, `tgqual`, `tgattr` and
   the `prosrc` of the function they run.
5. `Store.Check` is levels 1 and 2 plus **the log**, out of the one row it
   already reads. A schema dropped and migrated again under a running store
   fingerprints identically and every cursor that store minted is foreign, which
   is the one drift the first two levels cannot see. Same signature as
   `health.Probe`, and no import of `health` ([[D-091]]).

Every statement of all three runs on a `*sql.Conn` checked out for the call.

## The transaction question, asked once per operation

`event/eventpg/executor.go`:

1. `Store.opened` is the fixed order every operating door opens in: `ctx.Err()` →
   closed → ready → the transaction question. Only then is anything built.
2. `Store.bound` asks `crud.ExecutorFor(ctx, this.source)` and hands what it
   finds to `crudsql.Transaction`. Three answers and no fourth: nothing bound;
   a `*sql.Tx` of this data source; or something bound that is not a transaction,
   which is `errAmbientNotTransaction` and refuses **before any statement**
   ([[D-118]]).
3. `Store.Transaction` turns the first two into `event.Authority` values —
   invalid, or one over the `*sql.Tx` — and issues nothing, mints nothing and
   memoises nothing.
4. `Store.onExecutor` runs the callback on the bound `*sql.Tx` when there is one
   and on a `*sql.Conn` it checked out otherwise. **Nothing in the package calls
   a statement method on the `*sql.DB`**: `(*sql.DB).ExecContext` retries a
   `driver.ErrBadConn` on up to three connections, which would execute one append
   three times on exactly the path where the outcome is uncertain.

## An append

`event/eventpg/append.go`, `event/eventpg/classify.go`:

1. `Store.opened`; an empty record list returns nil with no statement; an
   `Expected` above `math.MaxInt64` is a `Conflict` with no statement, because
   `streams.version` is a `bigint` under `CHECK (version > 0)` and no stream is
   at such a version.
2. `Store.appendStatement(count)` renders one statement per batch size: a CTE
   that advances `streams.version` `WHERE s.version = $expected` — a conditional
   row update PostgreSQL evaluates against the row it has locked — and an outer
   `INSERT … SELECT … FROM admitted, (VALUES …) ORDER BY record.ord`. A fresh
   stream takes the speculative insertion, so two writers at version 0 do not
   race into a unique violation. Nothing is cached: the driver's own
   prepared-statement cache holds one text per batch size actually used.
3. Three parameters per record, all `string`, `int32` or `[]byte`. A nil payload
   binds as an empty `[]byte`, because `driver.DefaultParameterConverter` turns a
   nil slice into NULL and the column is `NOT NULL`.
4. `RowsAffected` decides: `len(records)` is admitted; `0` is
   `Failure(Conflict, errStreamMoved)`; anything else is `Unclassified`, which
   the append door reads as uncertainty — a wrong row count means the store does
   not know what it left behind.
5. On an error, `outcomeOf(failure, issued, joined)` answers `NotWritten` **only
   on proof** — joined, or nothing issued, or `driver.ErrBadConn`, or a SQLSTATE
   outside class `08` and outside `57P01`/`57P02`/`57P03` — and `Unconfirmed`
   otherwise. There is no default branch. `causeOf` gives `40001`, `40P01` and
   `55P03` an `*errs.Fault` of kind `errs.KindRetryable`, which is the second
   spelling of retryability the kernel reads.

## The two reads

`event/eventpg/read.go`, `event/eventpg/cursor.go`:

`Store.ReadStream` is one text whose `LIMIT` **is** the published `StreamPage`,
so a short page is the end of the stream by construction rather than by two
places agreeing about a number. A version above `math.MaxInt64` answers an empty
page rather than binding a wrapped negative.

`Store.ReadAll` is the watermark walk, and its eight steps are written out in the
comment above it because the argument is not recoverable from the code:

1. `readCursor` — `""` is the origin and never a refusal.
2. One statement, one snapshot for both halves: a `LEFT JOIN LATERAL` whose left
   side carries `pg_snapshot_xmin(pg_current_snapshot())::text` even when the
   right side is empty. `xid8` is read as text because it is not a type
   `database/sql`'s scan contract covers.
3. The gap up to `reach` is settled when the floor has passed `bound`.
4. `deliverable` hands over rows while the next position follows the last or the
   whole gap before it lies at or below what is settled.
5. `reached` is the highest position the **query** returned, never the highest
   delivered — read the other way the walk stalls on a burnt gap for good.
6. Nothing left undelivered discharges the bound, and so does a walk that has
   delivered past the reach it was minted for.
7. A bound is minted only when there is no useful outstanding one, and **never**
   while a transaction of this backing is bound.
8. Only when nothing was delivered and a bound was just minted, the **same**
   statement is re-issued and step 3 re-applied to the rows *that* snapshot
   returned. Pairing a fresh floor with the earlier snapshot's rows would declare
   a gap burnt while the row filling it committed between the two.

`Store.promised` checks every scanned row against the deployed schema's own
promises before an envelope is built, and a row outside them refuses the whole
page as `Unclassified`. The offending values appear in no message.

A cursor is `"vve1"` plus 54 base64 characters over `log[16] || be64(from) ||
be64(bound) || be64(reach)` — 58 characters, always. `walk.possible` refuses the
triples no walk could have produced.

## Where the decisions bite

- **[[D-118]] — one answer to the ambient-transaction question.** A durable write
  made while the caller's transaction is bound to this store's `crud.Source` is
  written inside it; an ambient executor that is **not** a transaction is refused
  and never placed on autocommit; and `New` refuses a `Spec` whose `Source` is
  not the same data source as its `DB`. Two subsystems answering one question two
  ways is itself the defect, so `jobspg` and `eventpg` answer it once.
- **[[D-126]] — the store chooses no isolation level.** No `SET TRANSACTION`, no
  `SELECT … FOR UPDATE`, no `BeginTx` outside `migration.go`. A conflict is a
  zero-row answer and `40001` is not a conflict.
- **[[D-127]] — migrating is a deployment-profile choice.** The default is not
  migrate, and a verified start-up refuses a schema that is not the one the code
  expects, naming which of the three levels refused.
- **[[D-091]] — the store publishes a readiness answer and names no importance.**
  `Check` is `health.Probe`'s signature and the package imports no `health`.
- **[[D-092]] — nothing starts in a constructor.** No goroutine, no `init`, no
  `runtime.Runner`, no environment read, no process-wide logger.
- **[[D-101]] — the operator path is first class.** `MigrationStatements` is
  exported and `MIGRATIONS.md` says what each statement is for.

## Traps

- **A cursor is bound to the schema, not to the pool.** Two stores over one
  database and two schemas are two backings; a cursor does not cross between
  them and the read refuses it as `BadCursor` rather than reading it as a
  position.
- **A walk inside the caller's transaction stops at the first gap and stays
  there.** It mints no bound, by decision: on the caller's own transaction a
  bound settles nothing, and minting it on a second connection while the caller
  holds one is how a pool at its limit deadlocks. Drain the log outside the
  write.
- **`MonotoneVisibility` is `Unsupported`, and that is the watermark working.**
  A walk may legitimately stop short of an append that has already returned to
  its caller.
- **A contended append consumes a transaction id.** The xid-first trigger fires
  once per statement whether or not the statement produced rows.
- **One writer shape is outside the foreign-writer guarantee:** `nextval` in one
  transaction, `INSERT … OVERRIDING SYSTEM VALUE` in a later one. Drawn and
  inserted inside one transaction it is covered.
- **A row the read refuses is a row nobody can remove through the documented
  surface**, because history is append-only in the database. `MIGRATIONS.md`
  carries the operator's remedy.
- **`event/` outside `event/eventpg` is frozen.** `make check-event-kernel`
  compares the working tree against the commit that landed the phase-1 kernel and
  refuses rather than reporting ok when it cannot ask.

## Files

| File | What it holds |
|---|---|
| `event/eventpg/doc.go` | the package sentence, and what this store promises about ordering and about the writers it covers |
| `event/eventpg/schema.go` | `DefaultSchema`, `SchemaVersion`, the four `Default*` bounds, `Schema`, `Schema.Resolved`, `Schema.Fingerprint`, `MigrationStatements`, `validSchemaName`, and the expectation model both renderings are built from — `expectation`, `expectedTable`, `expectedColumn`, `expectedIdentity`, `expectedUnique`, `expectedForeignKey`, `expectedCheck`, `expectedTrigger`, `expectedFunction`, `expected`, `bytesBetween`, `rendering` |
| `event/eventpg/config.go` | `ErrSpec`, `ErrSchemaMismatch`, `ErrNotReady`, `SchemaManagement` and its `Valid`/`String`, `Spec`, `Store`, `backing`, `New`, `limitsOf`, `fits`, `bound`, and the six pure answers — `Store.Schema`, `Store.SchemaManagement`, `Store.Capabilities`, `Store.Limits`, `Store.Backing`, `Store.Close` |
| `event/eventpg/migration.go` | `migrationStatements`, `assertMeta`, `createTable`, `createFunction`, `createTrigger`, `quoteIdentifier`, `quoteLiteral`, `migrationLock`, `Store.Migrate`, `Store.withMigrationLock`, `Store.migrateOn`, `Store.migrationFailure` — the only file that names `BeginTx`, `Commit` or `Rollback` |
| `event/eventpg/verify.go` | `logBytes`, `readiness`, `Store.Prepare`, `Store.Verify`, `Store.Check`, `Store.readMeta`, `Store.metaFailure` — levels 1 and 2, and the readiness a cursor's log is read out of |
| `event/eventpg/catalog.go` | level 3: the seven `pg_catalog` reads, `deployedRelation`, `deployedColumn`, `deployedConstraint`, `deployedTrigger`, `deployedSchema`, `Store.inspect`, `readCatalog`, `expectation.compare` and its six comparisons, `sameDefinition`, `tokens`, `triggerMask` |
| `event/eventpg/executor.go` | `errAmbientNotTransaction`, `Store.Transaction`, `Store.opened`, `Store.bound`, `executor`, `run`, `Store.onExecutor` — the fixed opening order and the seam every statement is issued through |
| `event/eventpg/classify.go` | `outcomeOf`, `backendSurvived`, `causeOf` — `NotWritten` only on proof, and the retryable cause three SQLSTATEs earn |
| `event/eventpg/append.go` | `errStreamMoved`, `errStreamAhead`, `Store.Append`, `payloadOf`, `Store.appendStatement`, `recordRows`, `recordRow` — one statement, and what its row count means |
| `event/eventpg/read.go` | `errRowOutsideSchema`, `errNoRow`, `storedEvent`, `Store.ReadStream`, `Store.ReadAll`, `Store.fetch`, `deliverable`, `reached`, `walk.settledAt`, `walk.spent`, `Store.promised`, `number`, `streamStatement`, `logStatement` — the two doors and the eight-step watermark walk |
| `event/eventpg/cursor.go` | `cursorTag`, `cursorBytes`, `errCursorFormat`, `errCursorForeign`, `errCursorImpossible`, `walk`, `mintCursor`, `readCursor`, `walk.possible` — the fixed-width, tagged, log-bound encoding |
| `event/eventpg/MIGRATIONS.md` | the profile rule, what each of the eleven statements is for, why statement eleven asserts rather than assigns, and the remedy for a row the read refuses |
| `scripts/checks.sh` | `EVENT_KERNEL_BASELINE` and `check_event_kernel` — the arm that makes the zero-diff obligation executable |
| `scripts/event_test.go` | the `eventpg` row of `charged`: this package costs the vocabulary plus `crud/adapter/crudsql` and nothing else |

Every non-test `.go` file under `event/eventpg/` has a row above, `doc.go`
included: the reverse index in `docs/ai/flows/Index.md` is what an agent reads
before editing a file, and a file with no row there reads as a file outside every
flow.

## Tests that walk this flow

Untagged, in `make unit`: `event/eventpg/schema_test.go`,
`event/eventpg/config_test.go`, `event/eventpg/sources_test.go` — the goldens,
the refusals, and the four source checks.

Behind `//go:build integration`, against a live PostgreSQL:
`event/eventpg/main_integration_test.go` (the DSN gate, the shared and scratch
schema harness, the counting and injecting `database/sql` driver),
`migration_integration_test.go`, `schema_integration_test.go`,
`append_integration_test.go`, `transaction_integration_test.go`,
`uncertainty_integration_test.go`, `read_integration_test.go`,
`cursor_integration_test.go`, `watermark_integration_test.go`,
`conformance_integration_test.go`, `mutation_integration_test.go`,
`census_integration_test.go`, `audit_integration_test.go`.

The gate names its own command, because `make integration` runs `./test/...`
only and does not reach a satellite's tagged suite:

```sh
FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
  go test -race -count=1 -tags=integration ./event/eventpg/
```

**An unset DSN fails the gate rather than skipping it**, and
`TestTheGateFailsWhenTheDSNIsUnset` runs the binary without the variable to prove
it: a skipped run that prints `ok` is what a report calls evidence and is not.

### Proved by

| What holds | Proved by |
|---|---|
| the spec refusals, and a legal spec accepted | `TestNewRefusesEverySpecItCannotAssemble` |
| the fingerprint is the rendering it digests, and a bound is an input to it | `TestTheFingerprintIsTheRenderingItDigests` |
| the eleven statements are ordered transactional DDL, quoted, and assert rather than assign | `TestMigrationStatementsAreOrderedTransactionalDDL`, `TestAMigrationOverASchemaItDidNotBuildRefusesRatherThanRestampingIt`, `TestARefusedMigrationLeavesTheTablesItCreatedRolledBack`, `TestASchemaNamedForAReservedWordDeploys` |
| the backing is the database and the schema together | `TestTwoStoreValuesOverOneSchemaAreOneStoreAndTwoSchemasAreNot` |
| nothing starts, nothing is discovered, nothing is logged, and `New` performs no I/O | `TestTheStoreStartsNothingAndReadsNoEnvironment`, `TestNewAgainstADeadDSNAnswersAStore`, `TestMerelyImportingTheEventExtensionStartsNothing` |
| every bound parameter is a type every `database/sql` driver accepts | `TestEveryBoundParameterIsATypeEveryDatabaseSQLDriverAccepts` |
| the migration builds the schema the fingerprint describes, and a re-run reissues no log | `TestMigrationStatementsBuildTheSchemaTheFingerprintDescribes`, `TestRunningTheMigrationTwiceReissuesNoLog` |
| verification refuses every mutation and passes the intact schema | `TestVerificationRefusesEveryMutationAndPassesTheIntactSchema`, `TestTheZeroSchemaManagementVerifiesAndMigratesNothing`, `TestPrepareAgainstAMissingSchemaRefusesBeforeAnyAppend`, `TestLimitsAreTheDeployedConstraintOperands` |
| the migration lock is one lock on one backend, and somebody waited | `TestTwoConcurrentPreparesTakeOneLockOnOneBackend`, `TestASecondMigrationBlocksAgainstAHeldLock` |
| history is append-only in the database | `TestTheDatabaseRefusesUpdateDeleteAndTruncateAndAdmitsInsert` |
| readiness is an answer and names no importance | `TestCheckIsAReadinessAnswerAndNamesNoImportance` |
| an append is one atomic statement, and a rollback leaves no fragment | `TestAnAppendOnThePoolIsAtomicWithNothing`, `TestAnAppendJoinsTheBoundTransactionAndNothingOutsideItSees`, `TestARollbackLeavesNoFragmentAndBurnsThePositions`, `TestABatchLandsDenseAscendingAndAtOneInstant`, `TestTheWidestBatchTheStoreCanBuildIsAppended` |
| eight writers at one version leave one winner, and `40001` is not a conflict | `TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts`, `TestTheIsolationMatrixTellsAConflictFromASerialisationFailure` |
| uncertainty is never resolved by guessing | `TestAKilledBackendAnswersUncertainAfterExactlyOneExecContext`, `TestABadConnBeforeTheSendIsOneCallAndNotWritten`, `TestTheRetryWithTheSameTokenResolvesTheUncertainty`, `TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires`, `TestAFailureInsideABoundTransactionIsNeverUnconfirmed`, `TestEachCancellationWindowIsAnsweredByTheRuleAndNotByOneOutcome` |
| two subsystems prove they wrote in one transaction, savepoints included | `TestARepoAuthorityAndAReceiptCompareSameAcrossTwoWritersInOneTransaction`, `TestASavepointWritesUnderItsParentsAuthorityAndARollbackDiscardsThem` |
| an ambient executor that is not a transaction refuses at all four doors and issues nothing | `TestAnAmbientNonTransactionRefusesAtAllFourDoors` |
| no statement is issued on the pool, no door opens or resolves a transaction, no version is read into Go, and no `NotWritten` branch is unguarded | `TestNoStatementIsIssuedOnTheDatabaseHandle`, `TestNoDoorOpensCommitsOrRollsBackAnything`, `TestNoStatementReadsTheStreamVersionIntoGo`, `TestNoNotWrittenBranchIsUnguarded` |
| a stream pages to its end, and the cursor is a settled watermark | `TestAStreamPagesToItsEndAndFoldsTheSameThroughASibling`, `TestAWalkFromTheEmptyCursorTilesWithAWalkFromTheTail`, `TestAWalkDoesNotPassAPositionAWriterCouldStillCommit`, `TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot`, `TestTheXminEqualsXmaxRuleFailsTheInFlightCase` |
| a walk inside a transaction mints no bound and settles no gap | `TestAWalkInsideATransactionMintsNoBoundAndSettlesNoGap` |
| a foreign writer is not skipped and the overriding one is | `TestAForeignWriterIsNotSkippedAndTheOverridingWriterIs` |
| every unreadable cursor is refused and the empty one is the origin | `TestEveryUnreadableCursorIsRefusedAndTheEmptyOneIsTheOrigin` |
| a row outside the schema's promises refuses the whole read | `TestARowOutsideTheSchemasPromisesRefusesTheWholeRead` |
| an unprepared store refuses everything, and a closed one closes nothing it did not open | `TestAnUnpreparedStoreRefusesEveryOperationAndAnswersTheThreePureOnes`, `TestCloseIsIdempotentAndClosesNothingItDidNotOpen` |
| no sentinel of `eventpg`'s crosses the store seam | `TestEveryErrorTheEightMethodsProduceIsNilAContextErrorOrAFailure` |
| the store satisfies the contract, at its own limits and at narrow ones | `TestTheStoreSatisfiesTheContract`, `TestTheStoreSatisfiesTheContractAtNarrowerLimits` |
| both runs certify nineteen sections and decline exactly one, and a downgraded section is red somewhere | `TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines`, `TestADowngradedSectionIsCaughtByTheCensusAndByNothingElse` |
| the conformance suite catches a defective store, and the gate fails with no DSN | `TestTheConformanceSuiteCatchesADefectiveStore`, `TestTheGateFailsWhenTheDSNIsUnset` |
| the version advance and the rows are one statement, over the whole schema | `TestTheAuditOverTheWholeSchemaHolds` |
| stored rows are upcast and never rewritten | `TestStoredRowsAreUpcastAndNeverRewritten` |
| `event/` outside `event/eventpg` is where the phase-1 commit left it | `TestCheckEventKernelReportsADifferenceAndOtherwiseOk`, `TestTheEventKernelOfThisRepositoryIsWhereThePhaseOneCommitLeftIt` |
| this package costs the vocabulary plus one adapter and nothing else | `TestNoEventPackageCostsMoreThanTheSeamItNames` |

### What the conformance run is worth, and why the mutation harness exists

`eventtest` detects 27 of 165 of its own section-level assertions, so "eventpg
passes eventtest" is worth exactly as much as the suite's ability to fail.
`TestTheConformanceSuiteCatchesADefectiveStore` runs six decorators over the
**real** store through the real conformance test in a subprocess of the test
binary, selected by `EVENTPG_MUTATION`, and each must be reported by the section
named beside it: a short stream page by `stream paging`, a reversed log page by
`global order`, admission at `Expected - 1` by `expected version`, a cursor at
the log's newest position by `resumption`, one pooled payload buffer by
`payload ownership`, and a renamed stream by `stream identity`. An unmutated
subprocess run is the control, and it passes.

**A green run is not the evidence — the verdict census is.** `eventtest.Run`
reports a section that **failed** with `t.Error` and one it could **not certify**
with `t.Log`, so a `Factory` hook that stops answering downgrades a whole section,
every case in it stops running, and `go test` prints `ok`. Driven: `wayTo`'s
`event.Unconfirmed` arm returning `nil` left `store failure classification: not
certified`, eighteen `passed` lines instead of nineteen, and the whole tagged
suite green. `TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines`
is what reports it now — it drives both runs as subprocesses of the test binary,
reads the verdict lines back and asserts the twenty-row table in
`census_integration_test.go`, reason included for the one section this store
declines. `TestADowngradedSectionIsCaughtByTheCensusAndByNothingElse` withdraws
three hooks the factory really supplies, through `EVENTPG_DOWNGRADE`, and asserts
of each that the run stays green and the census goes red.

**One defect shape the suite cannot see**, measured rather than assumed: a cursor
that skips a position **still in flight** — the same defect bounded to the page
window rather than to the whole log — passes all twenty sections, because no
section walks the log while a lower position is uncommitted. Recorded as
`## P2` §59 of `EVENTSOURCE_BACKLOG.md`.
