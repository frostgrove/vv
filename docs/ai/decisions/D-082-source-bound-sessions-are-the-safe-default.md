# D-082 — Source-bound sessions are the safe transaction default

**Status:** accepted · **Supersedes:** [[D-009]] [[D-027]]
**Invariant:** A safe-looking executor binding never sends a repository to an
executor whose datasource was not named; an unresolved binding fails before any
datasource is used.

## The decision

A foreign transaction is bound as an association, not as a free executor:

```go
txCtx := crud.BindExecutor(ctx, source, crudsql.From(tx))
```

`source` supplies the canonical physical identity and the foreign value supplies
only `Exec` and `Query`. `crud.Session` is the checked reusable form;
`NewSession` returns a declaration error and `MustSession` is the declarative
start-up form. The database/sql and pgx adapters concentrate the common case to
`source.BindExecutor(ctx, tx)` and carry the source's classifier onto that
foreign handle unless an explicit option overrides it.

The database/sql adapter recognises the transaction lifecycle structurally, not
only by the concrete `*sql.Tx` type. A Queryer retaining `Commit() error` and
`Rollback() error` is transactional, which covers sqlx, ent and Gorm's prepared
wrapper while leaving `*sql.DB` and `*sql.Conn` non-transactional. An opaque
wrapper has the explicit `crudsql.WithTransaction()` opt-in.

A session applies only to repositories whose `KeyOf` resolves to its identity.
Repositories on another database use their own source. Nested sessions keep the
ordinary innermost matching binding, and separately built sources over the same
handle share one transaction. `ReadWrite` and declared source wrappers keep the
primary identity through the existing `SourceUnwrapper` walk.

`WithExecutor` remains for source-compatible upgrades, but it is no longer an
unscoped fallback. It infers the executor's identity and is strict: a foreign
transaction normally names its transaction handle, so a repository bound to the
pool receives `ErrExecutorScope` rather than silently running outside rollback.
`WithExecutorFor` remains the low-level explicit association; the canonical
database form is valid, while a recognised database/sql or pgx transaction used
as its own datasource is a strict mismatch.

The old unconditional adoption is available only as
`WithUnsafeExecutor`. Its name is the opt-out: every repository the context
reaches adopts the executor, including repositories on another database.

`InTx` follows the same rule. A source must expose a stable comparable
`Identified` identity before `Begin` is called. Guessing from an unidentified
wrapper can either strand a sibling write outside the transaction or capture an
unrelated database, so neither is a safe default. The test recorder identifies
itself; third-party adapters can do the same without changing `Source`.
An executor already known to be transactional is not a canonical source: using
`crudsql.Source(tx, ...)` or `crudpgx.From(tx)` as the source is refused with the
`transaction_source` reason. Otherwise the new safe API would reproduce the old
pool-versus-transaction mismatch under a different name.

## Why

The executor interface deliberately cannot recover a pool from a transaction.
That made the former one-argument hand-off convenient, but it also made a
database-A transaction silently execute a database-B repository when schemas
matched. The seemingly scoped spelling had the inverse failure: naming the
transaction itself matched no pool, so the repository fell back outside the
transaction and survived rollback.

The application already has the missing fact at the transaction boundary: the
source from which the repository was built. Binding that fact once preserves the
two-method interop seam and removes both guesses. It is the intended framework
magic: one declarative line on the common path, with checked `Session` and
low-level `WithExecutorFor` forms for callers who want explicit construction.

## Failure contract

`ExecutorScopeError` wraps `ErrExecutorScope` and carries a public
`ExecutorScopeReason` without embedding a database handle. Repository operations
receive a failing executor from resolution, so the error occurs before a pool,
replica or foreign handle is called. `InTx` and `InAtomic` detect that executor
before invoking their callback; an invalid owned source is refused before
`Begin`. `InNewTx` may ignore a valid ambient executor by contract, but never an
invalid binding: it returns the same scope error before `Begin`. `ClaimSavepoint`
cannot carry an error in its boolean result, so it refuses the claim without
consuming the failed binding; the repository's next executor resolution still
returns the typed scope error.

Failure is monotonic across context derivation. Resolution walks the complete
binding chain before accepting a match: a newer valid session cannot hide an
older failed declaration or strict mismatch and cannot make a transaction helper
invoke its callback or `Begin` through a poisoned context.

The transaction-source refusal applies through wrapper walks and through
adapter receiver helpers. In particular, calling
`crudpgx.From(tx).BindExecutor(ctx, tx)` fails with the
`transaction_source` reason before either the transaction or its pool is used.

## What it forbids

- Do not reintroduce an unscoped fallback under a safe-looking name.
- Do not derive a foreign transaction's pool by reflection or driver internals.
- Do not let a strict mismatch fall through to an outer binding or repository
  source.
- Do not make a session for database A block or capture a repository on database
  B; only the deprecated strict-inference path fails globally because its target
  is ambiguous.
- Do not silently accept a nil, uncomparable or unidentified session source.
- Do not accept a transaction handle as a session source; it identifies the
  transaction, not the pool repositories were built from.
- Do not turn source-bound sessions into distributed transactions. Independent
  databases still commit independently.

## Where it lives

- `crud/executor.go` — `Session`, `NewSession`, `MustSession`, `BindExecutor`,
  strict resolution, `WithUnsafeExecutor`, and owned transaction validation.
- `crud/errors.go` — `ErrExecutorScope`, `ExecutorScopeError` and its reasons.
- `crud/adapter/crudsql/crudsql.go` and
  `crud/adapter/crudpgx/crudpgx.go` — the adapter-level one-line helpers.
- `crud/crudtest/recorder.go` — stable identity for the in-memory datasource.
- [[FL-009]] — the complete resolution and transaction path.

## Proven by

- `TestASessionReachesOnlyItsOwnDatabase`,
  `TestANaturalTransactionIdentityIsRefusedInsteadOfMissingSilently`,
  `TestAStrictInnerMismatchCannotFallThroughToAnOuterSession`,
  `TestASessionRefusesATransactionUsedAsTheCanonicalSource`,
  `TestWithUnsafeExecutorReachesEverySource` and
  `TestInTxRefusesAnUnidentifiedSourceBeforeBegin`,
  `TestInNewTxDoesNotHideAnInvalidAmbientBinding` and
  `TestARejectedScopeCannotBeConsumedAsASavepointMiss` in
  `crud/executor_test.go`.
- `TestAWrappedPrimaryIsStillTheDatabaseItNames` and
  `TestASessionRecognisesATransactionHiddenBySourceWrappers` in
  `crud/wrapsource_test.go`.
- `TestWithExecutorRefusesToAdoptARepositoryOnTheWrongDatabase`,
  `TestWithUnsafeExecutorKeepsTheLegacyCrossDatabaseOptOut`,
  `TestAScopedExecutorKeepsEachRepositoryOnItsOwnDatabase` and
  `TestWithExecutorForTransactionIdentityCannotEscapeRollback` in
  `test/integration/multidb_test.go`.
- `TestPgxSharedTransaction` and
  `TestPgxTransactionIdentityCannotEscapeRollback` (including the
  transaction-valued adapter receiver) in
  `test/integration/driver_pgx_test.go`.
- `TestSourceBoundExecutorInheritsTheDeclaredEngine` in
  `crud/adapter/crudsql/classify_test.go` and
  `TestSourceBoundExecutorInheritsItsClassifier` in
  `crud/adapter/crudpgx/conflict_test.go`.
- `TestEntForeignTransactionRollsBackChunkedWrites`,
  `TestSQLXForeignTransactionRollsBackChunkedWrites` and
  `TestPreparedGormForeignTransactionRollsBackChunkedWrites` prove that
  multi-statement `Delete` and `SaveAll` remain inside each foreign transaction.

## See also

[[UC-005]] [[UC-012]] [[D-017]] [[D-041]] [[D-061]] [[D-077]] [[FL-009]]
