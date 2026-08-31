# FL-009 — Transactions: joining, opening, and which database

**Entry point:** `crud/executor.go:BindExecutor` and `crud/executor.go:InTx`
**Implements:** [[UC-005]] [[UC-012]] · **Governed by:** [[D-082]] [[D-016]]
[[D-017]] [[D-041]] [[D-042]] [[D-046]] [[D-061]] [[D-077]] [[D-079]] [[D-083]]

vv never owns a transaction it did not open, never guesses which pool a foreign
transaction came from, and never lets an unresolved binding fall back to a pool.
The application states the missing association once at the transaction boundary.

## The safe foreign-transaction path

```go
source := crudsql.Postgres(db)
users := Users.Bind(source)

tx, _ := db.BeginTx(ctx, nil)
txCtx := source.BindExecutor(ctx, tx)
```

The adapter helper is the short form of:

```go
txCtx := crud.BindExecutor(ctx, source, crudsql.From(tx))
```

`source` contributes its canonical `Identified.DataSource()` identity. The
foreign value contributes only `Exec` and `Query`. `crud.NewSession` validates
the same association eagerly and returns a reusable typed `Session`;
`MustSession` is the declarative start-up form.

A transaction handle is not a canonical source. These forms are refused with
an `ExecutorScopeError{Reason: ExecutorScopeTransactionSource}`:

```go
crud.BindExecutor(ctx, crudsql.Source(tx, crud.Postgres{}), crudsql.From(tx))
crud.BindExecutor(ctx, crudpgx.From(tx), crudpgx.From(tx))
```

Both would otherwise key the binding on `tx`, miss a repository keyed on its
pool and reproduce the outside-rollback write the safe API exists to prevent.

## Resolution, step by step

1. **`repository.exec` and `repository.read`** —
   `crud/sqlrepo/repository.go`. Every statement asks
   `crud.ExecutorFor(ctx, repositorySource)` before using the writable source or
   replica. Reads inside a transaction therefore cannot route around it.
   `repository.InsertBatch` validates that resolution before native bulk. The
   exact repository Source still owns permission/interception, while the
   matching bound executor is passed to its capability as the effect target.

2. **The binding stack** — `crud/executor.go:binding`. Bindings chain through
   `context`; each carries a datasource identity, executor, ownership bit and
   savepoint counter. A normal session is scoped and non-strict: if repository B
   does not match a session for database A, resolution continues and B uses its
   own source.

3. **Canonical identity** — `identityOf`, `KeyOf` and `SameDataSource`.
   `identityOf` walks `SourceUnwrapper`, so an instrumented source and a
   `ReadWrite` pair keep the primary's physical handle. `SameDataSource` checks
   the complete dynamic values for comparability before `==`; a malformed
   identity cannot panic the process.

4. **`BindExecutor` / `Session.Bind`** — push a safe scoped association. The
   source must be non-nil, identified, comparable and not itself transactional;
   the executor must be non-nil. Invalid declarations push a failing executor,
   so the first repository resolution returns `ErrExecutorScope` before a pool,
   replica or foreign handle is touched. `NewSession` is the eager-error form.

5. **Legacy names** — `WithExecutor` is deprecated and strict. It infers an
   identity from its executor. A foreign transaction identifies `*sql.Tx` or
   `pgx.Tx`, not the pool, so a repository mismatch returns
   `ErrExecutorScope`; it does not continue to an outer session or source.
   `WithExecutorFor(ctx, canonicalDB, e)` remains the low-level explicit form.
   For recognised database/sql and pgx transactions,
   `WithExecutorFor(ctx, tx, e)` is strict and therefore loud.

6. **The unsafe opt-out** — `WithUnsafeExecutor` is the only binding with no
   datasource identity. It is the legacy unconditional behaviour: every
   repository adopts it, including one on another database. Scoped matches
   still outrank this fallback wherever they sit in the stack.

7. **`InTx`** — first resolves an executor for its source. A matching session is
   joined; a failing executor returns its scope error before `fn`; no binding
   opens a new transaction. Before `Begin`, an owned source must expose a stable
   comparable identity and must not already be transactional. The opened `Tx`
   is pushed on that identity with `owned=true`, so same-database sibling
   repositories join and other databases do not.

8. **Commit and rollback** — `fn == nil` commits. An error rolls back with a
   five-second context that preserves request values but detaches request
   cancellation; rollback failure is joined with the operation failure. A panic
   follows the same bounded rollback and is re-raised ([[D-077]]).

9. **`InAtomic` and `InNewTx`** — `InAtomic` joins only an executor known to be
   transactional and checks a failed scope before invoking its callback.
   `crudsql` recognises `*sql.Tx` and Queryer wrappers retaining
   `Commit() error`/`Rollback() error`; opaque transaction wrappers opt in with
   `crudsql.WithTransaction()`. `InNewTx` deliberately opens a fresh transaction
   inside existing bindings.

10. **Raw helpers below a repository** — `UnsafeExecFor`, `UnsafeQueryFor` and
    `UnsafeBulkInsertFor` run the same source-bound resolution before executing.
    They preserve a failed declaration and never infer that an arbitrary query
    is replica-safe. The exact-handle pgx methods `UnsafeCopyFrom*` deliberately
    do not consult `ctx`; the caller must already hold the intended handle.

## Nested and multi-database contexts

Bindings are searched innermost first. A matching inner session wins. A
nonmatching safe session is irrelevant to another database and does not block
it. A strict legacy mismatch fails immediately rather than falling through to a
matching outer session: the inner declaration was ambiguous and silently
ignoring it would resurrect the rollback escape.

The complete chain is validated before that match is returned. Consequently an
older declaration failure or strict mismatch cannot be hidden by adding a newer
matching session to the derived context.

```go
txCtx := mainSource.BindExecutor(ctx, tx)

users.Save(txCtx, &u)  // mainSource: transaction
events.Save(txCtx, &e) // analyticsSource: its own datasource
```

Two independently built sources over the same `*sql.DB` or `*pgxpool.Pool`
reduce to the same identity and share the transaction. `ReadWrite(primary,
replica)` names the primary, so transactional reads stay on the transaction and
never visit the replica.

## Atomic statement chunks

`repository.executePrepared` uses `InAtomic` when one logical write exceeds a
dialect bind budget. Every chunk is rendered before the first statement. A
caller's source-bound transaction is joined; otherwise one owned transaction
spans the ordered plan. A later failure rolls back earlier chunks when vv owns
the transaction, while a foreign owner retains commit control. This includes
Ent, sqlx and prepared Gorm transaction wrappers; live rollback tests execute
both chunked `Delete` and `SaveAll`. A source without transaction support returns
`ErrNoTxSupport` before the first chunk ([[D-079]]).

## Native batch effects

`InsertBatch` first validates the source-bound executor and then tests the exact
repository Source for `UnsafeBulkInserter`. The Source owns permission and
interception; a matching bound executor is supplied as the execution target, or
nil selects the unbound capability receiver. A pool capability therefore cannot
pull work out of an ambient transaction, and a raw transaction cannot make a
wrapper-hidden effect reappear. COPY is atomic on the pgx handle; the portable
fallback uses the chunk rule above. `ReadWrite` forwards native bulk only to its
primary. An unknown Source wrapper does not expose the underlying effect, so
portable INSERT is selected instead of silently invoking COPY underneath it. A direct
one-statement plan goes through the wrapper's `Exec`; chunked plans execute on
the transaction handle and need transaction-aware or driver instrumentation for
complete visibility ([[D-061]], [[D-062]], [[D-083]]).

## Savepoints and ownership

`OwnedExecutorFor` resolves the same binding and reports whether vv opened it.
`ClaimSavepoint` reserves against that binding's monotonically increasing
counter. The probe never takes a savepoint inside a foreign session: rolling
back in somebody else's unit of work could discard work its owner has not
finished ([[D-042]], [[FL-017]]).

- `crudsql.Tx.Begin` emits a uniquely numbered `SAVEPOINT`; commit releases and
  rollback rolls back to it. Transaction options are snapshotted before use.
- `crudpgx.Tx.Begin` delegates to pgx, whose nested transaction is a savepoint.
- Both adapters classify commit/release errors. This matters for deferred
  constraints at top-level commit and PostgreSQL `25P02` at savepoint release
  ([[FL-011]], [[FL-014]]).
- A foreign executor built with `From` has only the classifier the caller
  supplied. `crudsql` cannot infer MySQL versus MariaDB from a dialect
  ([[D-046]]).

## ORM and query-builder examples

```go
// gorm
source := crudsql.Postgres(sqlDB)
gormDB.Transaction(func(tx *gorm.DB) error {
    txCtx := source.BindExecutor(ctx, tx.Statement.ConnPool)
    return users.SaveOnly(txCtx, &u)
})

// ent
tx, _ := client.Tx(ctx)
txCtx := crud.BindExecutor(ctx, source, crudsql.From(tx))

// pgx / sqlc-on-pgx
source := crudpgx.Open(pool)
tx, _ := pool.Begin(ctx)
txCtx := source.BindExecutor(ctx, tx)
```

The wrapper is still a plain `crud.Executor`; no ORM-specific contract crosses
the boundary. ORM hooks and builder-side defaults do not run for statements vv
issues ([[D-017]]).

## Failure modes

| What goes wrong | Where caught | Result |
|---|---|---|
| source-less `WithExecutor` carries a transaction handle | strict resolution | typed `ErrExecutorScope`; no datasource call |
| transaction used as the `BindExecutor` source | `NewSession` | `transaction_source`; no datasource call |
| transaction passed directly as the `InTx` source | `inNewTx` | `transaction_source`; callback not run |
| nil, unidentified or uncomparable session source | `NewSession` / `InTx` | typed scope error before use / before `Begin` |
| `WithExecutorFor(ctx, tx, From(tx))` on sql or pgx | strict legacy binding | mismatch error; no pool fallback |
| context carries a session for another database | normal scoped resolution | repository uses its own source |
| caller explicitly uses `WithUnsafeExecutor` | unsafe fallback | every reached repository adopts it |
| raw `Unsafe*For` call receives a nil/failed source association | helper resolution | `ErrExecutorScope`; no executor call |
| exact pgx native method is called on a pool while a tx exists only in `ctx` | caller selected exact handle | pool call; use the repository or `UnsafeBulkInsertFor` |
| source cannot begin | `InTx` | `ErrNoTxSupport`; callback not run |
| stale committed/rolled-back session | driver | driver error; never pool fallback |
| callback error or panic | `InTx` | bounded detached rollback; panic re-raised |
| rollback also fails | `InTx` | joined errors, both inspectable |

## Files

| File | Role |
|---|---|
| `crud/executor.go` | contracts, identity walk, `Session`, binding resolution, ownership, transaction lifecycle and context-bound raw helpers |
| `crud/errors.go` | `ErrNoTxSupport`, `ErrExecutorScope`, typed reasons |
| `crud/sqlrepo/repository.go` | every statement's `exec`/`read` resolution and `Tx` |
| `crud/adapter/crudsql/crudsql.go` | database/sql executor/source/transaction/savepoints and `DB.BindExecutor` |
| `crud/adapter/crudpgx/crudpgx.go` | pgx executor/source/transaction/savepoints and `Executor.BindExecutor` |
| `crud/crudtest/recorder.go` | in-memory source identified by its own pointer |
| `crud/decorators/faults/probe.go` | owned savepoint consumer |
| `crud/batch.go` | typed repository entry before native executor selection |

## Tests that walk this flow

- `crud/executor_test.go` — safe session matching, strict legacy mismatch,
  transaction-source refusal, nested bindings, unsafe opt-out, ownership,
  identity validation and bounded rollback.
- `crud/wrapsource_test.go` — wrapper and `ReadWrite` identity, safe session and
  owned transaction scoping.
- `crud/crudtest/recorder_test.go` — one recorder is one datasource.
- `test/integration/multidb_test.go` — wrong-database refusal, explicit unsafe
  legacy behaviour, database/sql rollback, same-source siblings and natural
  transaction-identity refusals.
- `test/integration/driver_pgx_test.go` — pgx shared rollback and both strict and
  new-session transaction-source refusals, plus live InsertBatch rollback.
- `test/integration/driver_gorm_test.go`, `driver_ent_test.go`,
  `driver_sqlx_test.go`, `driver_sqlc_test.go`, `driver_sql_test.go` — one
  source-bound session shared with each foreign owner.
- `crud/sqlrepo/bind_budget_test.go` and `test/integration/saveall_test.go` —
  atomic statement chunks.
- `crud/executor_effect_test.go` and `crud/sqlrepo/insert_batch_test.go` — raw
  helper scoping, native capability selection inside owned/ambient sessions and
  portable fallback on the ambient executor.
- `test/integration/edge_test.go` — savepoints, stale transactions, isolation
  and commit classification.

## See also

[[FL-002]] [[FL-003]] [[FL-006]] [[FL-011]] [[FL-014]] [[FL-017]] [[D-082]]
