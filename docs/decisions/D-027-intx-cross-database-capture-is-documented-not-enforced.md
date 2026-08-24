# D-027 — `crud.InTx` cross-database capture is documented, not enforced

**Status:** open
**Invariant:** A repository must never run a statement on a database other than the one it was bound to — unless the application said so with `crud.WithExecutor`.

## The decision — and the gap it leaves

`InTx(ctx, src, fn)` first asks `ExecutorFor(ctx, src)` whether the context
already carries an executor this source would run on. `ExecutorFor` matches a
*scoped* binding by datasource identity, and otherwise falls back to the
innermost **unscoped** binding.

So an unscoped `crud.WithExecutor(ctx, tx)` — a transaction on database A —
satisfies `InTx(ctx, srcB, …)`. `fn` runs with A's transaction in the context,
and every repository bound to B runs its statements on A. No error, and on two
schema-identical databases (a shard pair, a staging copy) the writes land in the
wrong one and succeed.

The same fallback is what `repository.exec` uses for every statement, so this is
not confined to `InTx` — `InTx` is just where it is most visible, because that
is where a caller believes they opened a transaction on B.

This is the deliberate cost of [[D-009]], and it is documented: `WithExecutor`'s
doc comment says it is "deliberately unconditional", `WithExecutorFor` shows the
two-database example, `ExecutorFrom` and `ExecutorFor` document which question
each answers, and both usage guides tell the reader to say which database they
mean when the process talks to more than one.

## Why it is not enforced

Enforcement needs the `Source` to be able to say which database it speaks to,
and to refuse an executor that names a different one. Two things stand in the
way:

**The identity a foreign executor could give is not the one that would match.**
`crudsql.Executor` *does* implement `crud.Identified` —
`adapter/crudsql/crudsql.go:Executor.DataSource` returns the wrapped `Queryer`.
For a `DB` built by `Open` that is the `*sql.DB`, which is exactly right. But the
executor handed over by an ent or gorm transaction is `crudsql.From(tx)`, and its
`Queryer` is the `*sql.Tx` (or `*ent.Tx`, or the `*gorm.DB`) — the transaction
handle, not the pool. `database/sql` offers no way to get from a `*sql.Tx` back
to its `*sql.DB`, and neither ORM exposes one through the two-method interface
vv sees. So the identity a foreign executor can supply never equals a
repository's, which is what `WithExecutor`'s comment means by "no check could
pass". The binding is therefore pushed with `ds = nil` on purpose, and the
executor's own `DataSource()` is deliberately not consulted.

**Requiring identity on `Source` would break every external adapter.**
`crud.Identified` is optional for the reason given in [[D-009]]. And it would
have to be a *reliable* identity — two `Source` values over one pool must answer
with the same handle — which is a contract the interface cannot check.

## What is unresolved

1. **Whether an unscoped binding should be a fallback at all.**
   `ExecutorFor`'s fallback is the whole mechanism. Removing it makes
   `WithExecutor` do nothing, which is the seam. Keeping it is the hole.

   The obvious middle ground does not work: "let a fallback apply only when the
   *source* is unidentified" would close the two-database case, and it would also
   break the primary documented pattern, because `crudsql.Postgres(db)` **is**
   identified — the ent and gorm guides' §5 usecase binds repositories to exactly
   that and joins with a plain `WithExecutor`. Any rule of that shape has to
   answer for the case where the caller genuinely means "adopt everything", which
   is most of the time.
2. **Whether a process-level "strict" switch is worth it.** A package-level
   `crud.RequireScopedExecutors()` that turns the adoption into an error. Global
   mutable state, which the rest of the library avoids entirely, and it would
   have to be off by default, so it protects only processes that already knew.
3. **Whether `InTx` should refuse rather than join** when the source is
   `Identified`, the ambient binding is unscoped, and the source is a `Beginner`
   — i.e. exactly the case where it could have opened its own transaction. That
   is narrower than option 1 and easier to reason about, and it still breaks the
   guides' pattern for anyone who calls `repo.Tx` inside an ORM transaction. It
   also leaves `repository.exec` doing the same adoption on every other call, so
   it fixes the visible half and not the underlying one.

## What it forbids

While this is open, do not:

- Make `WithExecutor` scoped. That is the seam.
- Add a required `DataSource()` to `crud.Source`.
- Change `ownScope` to scope an unidentified source to itself. See [[D-009]] —
  it breaks sibling repositories that legitimately join.
- Remove or weaken the multi-database tests. They are what makes the trade
  concrete instead of theoretical.
- Describe `WithExecutor` as safe for a multi-database process anywhere in the
  documentation. It is not, and both guides currently say so.

## Where it lives

- `crud/executor.go:ExecutorFor` — the unscoped fallback, which is the gap.
- `crud/executor.go:InTx` — where a caller most expects the guarantee.
- `crud/executor.go:WithExecutor` — the doc comment that states the trade.
- `crud/executor.go:WithExecutorFor` — the answer a caller is expected to use.
- `crud/executor.go:Identified` — optional, with the reason.
- `crud/executor.go:ownScope` — the related choice, decided the other way.
- `repo/basic/repository.go:repository.exec` — the same fallback on every
  statement.
- `docs/usage-guides/ent.md` §5 and `docs/usage-guides/gorm.md` §5 — the
  "be deliberate about this if you talk to a second database" note.

## Proven by

The gap itself is tested — as *current behaviour*, not as a bug — which is the
right way to hold an open question:

- `TestAnUnscopedExecutorAdoptsEveryRepositoryIncludingTheWrongOne` in
  `test/integration/multidb_test.go` — two real databases; the test name states
  the problem. If the fallback is ever changed, this is the test that changes
  with it.
- `TestAnUnscopedExecutorReachesEverySource` in `crud/executor_test.go` — the
  unit-level statement of the same thing.
- `TestAScopedExecutorKeepsEachRepositoryOnItsOwnDatabase` and
  `TestARepositoryTransactionDoesNotCaptureAnotherDatabase` in
  `test/integration/multidb_test.go` — the workaround works.
- `TestInTxDoesNotJoinAnotherDatabasesTransaction` in `crud/executor_test.go` —
  a *scoped* binding for another database is correctly declined. Only the
  unscoped case is open.
- `TestTwoRepositoriesOnOneDatabaseStillShareOneTransaction` in
  `test/integration/multidb_test.go` — the behaviour any fix must not break.

## See also

[[D-009]] [[D-017]] [[D-016]]
