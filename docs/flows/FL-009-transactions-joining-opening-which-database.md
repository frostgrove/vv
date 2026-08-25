# FL-009 — Transactions: joining, opening, and which database

**Entry point:** `crud/executor.go:InTx` and `crud/executor.go:ExecutorFor`
**Implements:** [[UC-005]] [[UC-012]] · **Governed by:** [[D-009]] [[D-016]] [[D-017]] [[D-046]]

vv never owns a transaction it did not open, and it will not open one if
somebody else already did. The whole mechanism is a linked list of bindings in
the context and one question asked against it: *is there an executor here that
my database would accept?*

## The path

1. **`repository.exec`** — `repo/basic/repository.go:92`
   Every statement in the repository starts here.
   ```go
   if e, ok := crud.ExecutorFor(ctx, r.src); ok { return e }
   return r.src
   ```
   A context carrying somebody else's database is not this repository's business
   and is left alone.

2. **The binding stack** — `crud/executor.go:145`
   ```go
   type binding struct { ds any; e Executor; prev *binding }
   ```
   `push` (`executor.go:151`) links a new binding in front of whatever was there.
   They chain rather than replace, so an inner scoped binding cannot hide an
   outer unscoped one from a different repository.

3. **`WithExecutor`** — `crud/executor.go:165`
   Pushes with `ds == nil`: **every** repository runs on it, whatever datasource
   it was bound to. Deliberately unconditional — the executor an ent or gorm
   transaction hands over has no relationship to the source a repository holds,
   so no check could pass. This is the single interop point of the library.

4. **`WithExecutorFor`** — `crud/executor.go:184`
   Pushes with `ds = KeyOf(src)` (`executor.go:228`): the raw handle if the
   value is not `Identified`, otherwise its `DataSource()`. Both spellings — the
   `*sql.DB` and any `crudsql.DB` over it — land on the same key, because
   `crudsql.Executor.DataSource` returns the wrapped `Queryer`
   (`adapter/crudsql/crudsql.go:86`) and `crudpgx.Executor.DataSource` the pgx
   handle (`adapter/crudpgx/crudpgx.go:85`).

5. **`ExecutorFor`** — `crud/executor.go:202`
   Walks the chain innermost-first. The first *unscoped* binding it meets is
   remembered as the fallback; any binding whose `ds` matches `want` returns
   immediately. So **a matching scoped binding wins over an unscoped one
   wherever it sits in the stack**, and a repository with no match at all still
   joins an unscoped binding. `SameDataSource` (`executor.go:268`) compares by
   type and value and never panics on an identity that is not comparable.

6. **`InTx`** — `crud/executor.go:294`
   ```go
   if _, ok := ExecutorFor(ctx, src); ok { return fn(ctx) }   // join, do not nest
   b, ok := src.(Beginner); if !ok { return ErrNoTxSupport }
   tx, _ := b.Begin(ctx)
   // panic  -> Rollback, re-panic
   // error  -> Rollback (errors.Join if the rollback also fails)
   // else   -> Commit
   fn(push(ctx, ownScope(src), tx))
   ```
   Joining means the outer owner keeps control of commit and rollback — which is
   what makes an vv call inside somebody else's transaction safe.

7. **`ownScope`** — `crud/executor.go:245`
   For a transaction vv opens itself, nobody named a database. An
   `Identified` source scopes the binding to itself, so the transaction reaches
   every repository over that database and no others. A source that cannot say
   which database it is binds **unscoped** — the old, unconditional join —
   because scoping it to itself would quietly stop a sibling repository from
   joining a transaction it used to join, and a write landing outside the
   transaction is no better than one landing in the wrong database.

8. **`repository.Tx`** — `repo/basic/repository.go:128` — `crud.InTx(ctx,
   r.src, fn)`. `crud.Core.Tx` is on the interface, so a decorator can wrap it.

## Savepoints

- **`crudsql`** — `adapter/crudsql/crudsql.go:208`
  `database/sql` has no nested transactions, so `Tx.Begin` issues
  `SAVEPOINT vv_sp_<n>` with an atomic counter, and returns a `savepoint`
  (`crudsql.go:216`) whose `Commit` is `RELEASE SAVEPOINT` and whose `Rollback`
  is `ROLLBACK TO SAVEPOINT`. `savepoint.Begin` delegates back to the parent
  `Tx`, so the counter is shared and names never collide.
- **Both ends of the seam classify**, and for two different reasons.
  `Tx.Commit` passes the driver's error through `Executor.conflict` because a
  `DEFERRABLE INITIALLY DEFERRED` constraint is checked at the top-level `COMMIT`
  and not at the statement — untouched, that one shape of conflict was a 500
  while the immediate shape was a 409. `savepoint.Commit` wraps its `RELEASE`
  too. No *integrity* violation reaches it — PostgreSQL hands a deferred check to
  the parent transaction, and no engine has been measured to raise integrity at
  `RELEASE SAVEPOINT` — but `25P02` does: a statement PostgreSQL refuses inside
  the savepoint poisons the whole transaction and the `RELEASE` after it is
  refused, so the classifier there is what turns that into
  `errs.CodeTransactionAborted` instead of an anonymous 500. It is also parity
  with `crudpgx` below. The classification itself is [[FL-011]]'s and
  [[FL-014]]'s.
- **`Begin` carries the classifier into the transaction.** `DB.Begin` copies
  `Executor.faults` into the `Tx`, and `Tx.Begin` hands its own `Executor` to the
  `savepoint`. Without that a deferred constraint would arrive as a conflict with
  no code while the immediate shape carried one — the same divergence in a
  smaller shape ([[FL-014]]).
- **A joined foreign transaction carries none.** `crudsql.From(tx)` names no
  engine, and the engine is declared rather than derived ([[D-046]]), so a write
  inside somebody else's transaction is a 409 with no code unless
  `crudsql.WithFaults` was passed. It is the one place in the tree where the same
  violation classifies differently depending on how the executor was built, and
  it is the price of never answering "mysql" for a MariaDB server.
- **`crudpgx`** — `adapter/crudpgx/crudpgx.go:128`
  `Begin` type-asserts the handle to pgx's own `Begin`; a nested one is already
  a savepoint, courtesy of pgx — and it comes back as the same `Tx` whose
  `Commit` (`crudpgx.go:158`) classifies. So a nested write cannot be a 409
  through pgx and a 500 through `database/sql`, which is the divergence the two
  PostgreSQL adapters are kept in step to avoid.
- **`SQLite`** — no row locks (`crud/dialect.go:130`), so `crud.ForUpdate()`
  renders nothing and the serialisation a caller wanted comes from the
  transaction instead.

### Who opened it, and how many savepoints it may hold

Two things phase 7 added to the binding, because the probe needed them and the
seam could not answer either ([[FL-017]], [[D-042]]).

- **`binding.owned`.** `crud.InTx` pushes `owned: true`; `crud.WithExecutor` and
  `crud.WithExecutorFor` push `false`. Before that they pushed the same
  `binding{ds, e, prev}` and nothing could tell an ent transaction from one vv
  opened. `crud.OwnedExecutorFor(ctx, src)` answers both questions in one walk,
  so `found` and `owned` cannot disagree about which binding they describe.

  It matters because **a foreign transaction is never given a savepoint.** An ent
  or gorm transaction has its own savepoint stack and its own expectations about
  what runs inside it, and `ROLLBACK TO SAVEPOINT` in the middle of somebody
  else's unit of work can discard work its owner has not finished with.

  Ask `OwnedExecutorFor` and never `ExecutorFrom`: with a foreign transaction
  scoped to a *different* handle, `ExecutorFrom` says "in a transaction" while
  this repository's write runs outside one.

- **`binding.saves`.** `crud.ClaimSavepoint(ctx, src)` reserves one against the
  transaction the binding names and reports the running count. The budget lives
  with the transaction and the policy lives with the caller, so two repositories
  sharing one transaction share one count and two transactions do not.

  It counts up and never down, and that is the shape of the limit rather than an
  oversight: PostgreSQL's 64-entry subxid cache overflows on the number of
  subtransactions a top-level transaction has assigned XIDs to, and releasing a
  savepoint does not give the entry back. The overflow is not a round trip — it
  forces pg_subtrans lookups on every reader in the cluster.

The probe never issues `SAVEPOINT` itself. It calls `Beginner.Begin`, so the
counter `crudsql.Tx.Begin` owns is the only thing naming savepoints and a
hand-rolled name cannot collide with one the seam issued.

## The ORM-owned-transaction pattern

The ORM opens and owns the transaction; vv is handed a handle and joins.

```go
// gorm
gdb.Transaction(func(tx *gorm.DB) error {
    ctx := crud.WithExecutor(ctx, crudsql.From(tx.Statement.ConnPool))
    return users.Save(ctx, &u)          // inside the gorm transaction
})

// ent (needs --feature sql/execquery)
tx, _ := client.Tx(ctx)
ctx = crud.WithExecutor(ctx, crudsql.From(tx))

// pgx / sqlc-on-pgx
tx, _ := pool.Begin(ctx)
ctx = crud.WithExecutor(ctx, crudpgx.From(tx))
```

The wrapper is a plain `crud.Executor`, not a `Beginner`, so `InTx` on it can
only *join* — `ErrNoTxSupport` if there is nothing to join. That is the correct
answer: vv must not open a transaction on a handle whose lifetime somebody
else owns. When a rollback happens on the ORM's side, the vv writes go with
it, because they were the same transaction all along.

With more than one database in the process, name the one you mean:
```go
ctx = crud.WithExecutorFor(ctx, mainDB, crudsql.From(tx))
users.Save(ctx, &u)    // bound to mainDB      — runs in tx
events.Save(ctx, &e)   // bound to analyticsDB — runs on analyticsDB
```
With a plain `WithExecutor` that second call goes to `mainDB`, inside the
transaction, and reports success.

## Where the decisions bite

- **Join, never nest.** `InTx` checks before it begins. A nested `BEGIN` would
  either fail or silently commit early depending on the driver.
- **`WithExecutor` is unconditional on purpose.** It is the interop seam; making
  it check anything would break every ORM handoff, none of which can identify
  themselves as the repository's source.
- **`ownScope` returns `nil` for an unidentified source.** Not "scope it to
  itself". The failure mode of over-scoping is a write silently landing outside
  the transaction. `KeyOf`, right beside it, never answers nil — the two look
  alike and answer different questions, and [[D-041]] was written against the
  wrong one of them until phase 6.
- **Only `Exec` and `Query` cross the boundary** (`crud/executor.go:36`). That is
  the reason any foreign transaction can be pushed into a context at all —
  scanning stays with the mapper and dialect stays with the repository.
- **`ForUpdate` is only requested inside a transaction.** `repository.Update`
  asks `ExecutorFor` before adding the lock ([[FL-002]]); outside one the lock
  would be taken and dropped before the write.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| `InTx` on a handle that cannot begin | `InTx` (`executor.go:300`) | `ErrNoTxSupport` → 500 |
| `fn` returns an error | `InTx` (`executor.go:313`) | the error; rollback failure joined onto it |
| `fn` panics | `InTx` (`executor.go:307`) | rollback, then the panic is re-raised |
| plain `WithExecutor` with two databases in play | nothing catches it — by design | the write lands in the wrong database; use `WithExecutorFor` |
| a source that is not `Identified` under `WithExecutorFor` | `KeyOf` takes the value at face value | matched only if the caller passes the same value |
| uncomparable datasource identity | `SameDataSource` (`executor.go:268`) | no match, no panic — as far as the *static* type goes; a struct holding an interface is comparable and `==` on it still panics, which is why `catalog/set.go:findable` guards its own probe ([[FL-016]]) |
| a finished transaction still in the context | the driver | the driver's error, surfaced as-is |
| a deferred constraint fires at `COMMIT` rather than at the statement | the adapter's `Commit` → `Executor.conflict` → `sqlfault.Wrap` | `ErrConflict` → 409, with the code where the source named its engine ([[FL-011]], [[FL-014]]) |
| a write inside a transaction opened by `From` or `Open` | nothing classifies the engine — by design | 409 with the driver's message and no code; pass `crudsql.WithFaults` ([[FL-014]]) |
| a probe wanting a savepoint inside a **foreign** transaction | `OwnedExecutorFor` reports `owned == false` | no savepoint is taken, and on an engine that poisons its transaction the answer is one violation ([[FL-017]]) |
| a probe wanting a savepoint past the budget | `ClaimSavepoint` | no savepoint, and the fault is marked incomplete rather than silently short ([[D-042]]) |

## Files

| File | Role |
|---|---|
| `crud/executor.go` | `Executor`, `Tx`, `Beginner`, `Source`, `Identified`, `Sourced`, the binding stack with its `owned` flag and savepoint counter, `WithExecutor(For)`, `ExecutorFor`, `OwnedExecutorFor`, `ClaimSavepoint`, `bindingFor`, `InTx`, `ownScope`. `KeyOf` and `SameDataSource` are exported since phase 6 and `ownScope` is not: `catalog` keys on the first two and has no business with the third ([[FL-016]]) |
| `repo/decorators/faults/probe.go` | `enricher.savepoint` — the only caller of `ClaimSavepoint`, and the four conditions a savepoint needs ([[FL-017]]) |
| `repo/basic/repository.go` | `exec` — every statement's executor choice; `Tx` |
| `adapter/crudsql/crudsql.go` | `From`, `Open`, `Source`, `Postgres`/`MySQL`/`MariaDB`/`SQLite`, `WithFaults`, `DB.Begin`, `Tx.Begin` savepoints, `DataSource`; `Tx.Commit` and `savepoint.Commit` classify, and `Begin` propagates the classifier ([[FL-011]], [[FL-014]]) |
| `adapter/crudpgx/crudpgx.go` | `From`, `Open`, `WithFaults`, `Begin`, `DataSource`, `CopyFrom`; `Tx.Commit` classifies and `Begin` propagates the classifier ([[FL-011]], [[FL-014]]) |
| `crud/dialect.go` | `LockClause` per dialect |
| `crud/errors.go` | `ErrNoTxSupport` |

## Tests that walk this flow

- `TestAnUnscopedExecutorReachesEverySource` — `crud/executor_test.go`.
- `TestAScopedExecutorReachesOnlyItsOwnDatabase` — `crud/executor_test.go`.
- `TestTheHandleAndASourceOverItNameTheSameDatabase` — `crud/executor_test.go` — `KeyOf`.
- `TestAScopedBindingDoesNotHideTheUnscopedOneUnderIt` — `crud/executor_test.go` — the chain walk.
- `TestAnUncomparableDataSourceDoesNotPanic` — `crud/executor_test.go`.
- `TestInTxScopesTheTransactionItOpens` — `crud/executor_test.go` — `ownScope`.
- `TestInTxLeavesAnUnidentifiedSourceUnscoped` — `crud/executor_test.go` — the other half.
- `TestTheRecorderStaysUnidentified` — `crud/crudtest/recorder_test.go` — the recorder binds unscoped, and the control shows what giving it a `DataSource()` would change ([[D-041]]).
- `TestInTxJoinsRatherThanNests` — `crud/executor_test.go`.
- `TestInTxDoesNotJoinAnotherDatabasesTransaction` — `crud/executor_test.go`.
- `TestInTxWithoutABeginnerIsRefused` — `crud/executor_test.go`.
- `TestTransactionJoinsAnAmbientExecutor` / `TestTransactionRollsBackOnError` — `repo/basic/repository_test.go`.
- `TestGormRollbackTakesVVWithIt` — `test/integration/driver_gorm_test.go` — the ORM-owned pattern.
- `TestAnEntTransactionJoinsButCannotOpenASavepoint` — `test/integration/driver_ent_test.go`.
- `TestSavepointsRollBackAndReleaseIndependently` / `TestASavepointInsideASavepointUnwindsOneLevelAtATime` — `test/integration/edge_test.go`.
- `TestSQLiteSavepointRollsBackWithoutLosingTheTransaction` — `test/integration/driver_sqlite_test.go`.
- `TestATransactionVVOpenedIsMarkedOwnedAndAForeignOneIsNot` — `crud/executor_test.go` — the `owned` flag from both sides, plus the `ExecutorFrom` trap.
- `TestASavepointClaimCountsPerTransactionAndNotPerRepository` / `TestNoSavepointIsClaimedInAForeignTransactionOrOutsideOne` — `crud/executor_test.go` — the budget's shape, each with its control.
- `TestAForeignTransactionIsNeverGivenASavepoint` / `TestOurOwnTransactionIsGivenASavepointAndTheProbeRuns` / `TestPastTheSavepointBudgetTheAnswerIsPartial` — `repo/decorators/faults/probe_test.go` — the savepoint wrap at the seam that uses it.
- `TestTheTransactionMatrix` — `test/integration/probe_test.go` — the whole table live, twenty arms with a counter.
- `TestAScopedExecutorKeepsEachRepositoryOnItsOwnDatabase` — `test/integration/multidb_test.go`.
- `TestAnUnscopedExecutorAdoptsEveryRepositoryIncludingTheWrongOne` — `test/integration/multidb_test.go` — the documented hazard, executed.
- `TestATransactionThatFailsHalfwayLeavesNothingBehind` — `test/integration/edge_test.go`.
- `TestAFinishedTransactionInTheContextIsNotIgnored` — `test/integration/edge_test.go`.
- `TestADeferredConstraintArrivesFromTheCommitAndNotTheStatement` — `test/integration/corpus_test.go` — the commit path through both PostgreSQL adapters, with the immediate foreign key in the same run as its control. It walks top-level beginners only, so the savepoint door is not what it exercises.
- `TestANestedCommitOnAPoisonedTransactionCarriesItsCode` — `test/integration/edge_test.go` — the savepoint door, on the one thing that does reach it: `25P02` from a `RELEASE` after a refused statement, through `crudsql` and `crudpgx` both, with a healthy nested commit as the control. Reverting `savepoint.Commit` to `return err` reddens the `crudsql` leg alone, which is the parity claim measured.

## See also

[[FL-002]] [[FL-003]] [[FL-006]] [[FL-011]] [[FL-014]] [[FL-017]] [[D-042]]
