# D-009 — The context executor is captured unconditionally; naming a database is opt-in

**Status:** accepted
**Invariant:** `crud.WithExecutor` must be honoured by every repository the context reaches, whatever datasource that repository was bound to; only `crud.WithExecutorFor` may restrict it.

## The decision

`crud.WithExecutor(ctx, e)` pushes an executor with no datasource identity.
`crud.ExecutorFor(ctx, src)` walks the binding chain: an innermost binding scoped
to `src`'s datasource wins, and failing that the innermost *unscoped* binding
wins. So an unscoped push reaches everything.

`crud.WithExecutorFor(ctx, ds, e)` scopes the binding to one database. Bindings
chain rather than replace, so an inner scoped binding cannot hide an outer
unscoped one from a repository it does not apply to.

`crud.InTx` scopes the transaction it opens to the source's own datasource —
but only when the source implements `crud.Identified`. An unidentified source
binds unscoped, which is the old behaviour.

## Why

**Why the capture is unconditional.** This is the whole interop seam. The
executor an ent or a gorm transaction hands over wraps the *transaction* handle,
and there is no way through `Exec`/`Query` to get from that back to the pool the
repository was bound to. So the identity it could supply would never equal the
repository's, and a check would mean the seam never works — see [[D-027]] for
the detail.

That leaves one line to carry the whole integration:

```go
txCtx := crud.WithExecutor(ctx, crudsql.From(tx))
```

It is what §5 of both usage guides is built on, so it has to be enough on its
own.

**Why naming a database is opt-in rather than required.** A process with one
database is the common case, and requiring it to name that database at every
transaction boundary is boilerplate for nothing. A process with two databases
has a real problem — a plain `WithExecutor` would push the analytics write into
the main database's transaction and report success — and `WithExecutorFor` is
the answer, spelled at the transaction site where the author already knows which
database they opened.

**Why `Identified` is an interface and not a field.** `Source` is `Exec` +
`Query` + `Dialect`. An adapter written outside this repository satisfies it
today; requiring a fourth method would break every one of them. `Identified` is
optional; a source that does not implement it is never matched by a scoped
binding and keeps the plain `WithExecutor` behaviour.

**Why an unidentified source keeps the unscoped behaviour in `InTx`.** This is
the case worth spelling out, because the safer-looking choice is the wrong one.
Scoping an unidentified source to *itself* would mean a sibling repository over
the same database — bound to a different `Source` value, which is normal — stops
joining a transaction it used to join. Its writes then land outside the
transaction and survive the rollback. Refusing to join is not safer than joining
wrong; it is a different wrong answer with the same shape, and it breaks working
code on upgrade rather than on the day someone adds a second database.
`crud/executor.go:ownScope` is where that choice is made.

**Why identity is compared by shape, not by `==` directly.**
`SameDataSource` checks the types match and are comparable before comparing. A
datasource handle is a pointer in practice, but nothing in the contract says it
must be, and `==` on an uncomparable dynamic type panics. It answers about the
static type, which is as far as it can see: a struct holding an interface is
comparable and `==` on it still panics when that interface holds a slice. A
caller that must not panic guards the comparison — `catalog/set.go:findable`
does, and [[D-041]] says why.

**Why `KeyOf` takes an unidentified value at face value.** If a caller names
something that cannot identify itself, the caller said it, so it is the key.
Both spellings — the raw `*sql.DB` and a `Source` over it — reduce to the same
key. It never answers nil, which is what makes it safe to key a catalog on
([[D-041]]); `ownScope`, right beside it, is the rule that does.

## What it forbids

- Do not add a datasource check to `WithExecutor`. It closes the interop seam,
  which is the library's reason to exist alongside an ORM.
- Do not make `Identified` a required method on `Source`.
- Do not change `ownScope` to scope an unidentified source to itself.
- Do not make a scoped binding replace rather than chain. An inner
  `WithExecutorFor(dbA, …)` must not hide an outer `WithExecutor` from a
  repository on `dbB`.
- Do not make `InTx` nest. It joins, so `fn` cannot roll back independently of
  the outer transaction; that is documented and depended on.
- See [[D-027]] for the part of this that is documented rather than enforced.

## Where it lives

- `crud/executor.go:WithExecutor` — the unconditional push.
- `crud/executor.go:WithExecutorFor` — the scoped push.
- `crud/executor.go:binding` / `crud/executor.go:push` — the chain.
- `crud/executor.go:ExecutorFor` — scoped match first, unscoped fallback.
- `crud/executor.go:ExecutorFrom` — answers "is there a transaction here at
  all", which is a different question from "is there one for MY database"; the
  repository always asks the second one.
- `crud/executor.go:Identified` — the optional interface.
- `crud/executor.go:KeyOf` / `crud/executor.go:ownScope` — the two identity
  rules, and the comment that records why they differ. `KeyOf` is exported and
  `ownScope` is not: phase 6's `catalog` keys on the first and has no business
  with the second.
- `crud/executor.go:SameDataSource` — never panics on an uncomparable handle,
  as far as its static type goes.
- `crud/executor.go:InTx` — join-or-open.
- `repo/basic/repository.go:repository.exec` — every statement in the basic
  repository goes through it.

## Proven by

- `TestAnUnscopedExecutorReachesEverySource` in `crud/executor_test.go`.
- `TestAScopedExecutorReachesOnlyItsOwnDatabase` in `crud/executor_test.go`.
- `TestAScopedBindingDoesNotHideTheUnscopedOneUnderIt` in
  `crud/executor_test.go` — the chain, not a replace.
- `TestInTxScopesTheTransactionItOpens` and
  `TestInTxLeavesAnUnidentifiedSourceUnscoped` in `crud/executor_test.go` — the
  two halves of the `ownScope` decision.
- `TestInTxJoinsRatherThanNests` and
  `TestInTxDoesNotJoinAnotherDatabasesTransaction` in `crud/executor_test.go`.
- `TestTheHandleAndASourceOverItNameTheSameDatabase` in `crud/executor_test.go`.
- `TestAnUncomparableDataSourceDoesNotPanic` in `crud/executor_test.go`.
- `TestAnUnscopedExecutorAdoptsEveryRepositoryIncludingTheWrongOne` in
  `test/integration/multidb_test.go` — two real databases; this is the test that
  makes the trade concrete rather than theoretical.
- `TestAScopedExecutorKeepsEachRepositoryOnItsOwnDatabase` and
  `TestARepositoryTransactionDoesNotCaptureAnotherDatabase` in
  `test/integration/multidb_test.go`.
- `TestEntSharedTransaction` in `test/integration/driver_ent_test.go`,
  `TestGormSharedTransaction` in `test/integration/driver_gorm_test.go`,
  `TestPgxSharedTransaction` in `test/integration/driver_pgx_test.go`,
  `TestSqlxSharedTransaction` in `test/integration/driver_sqlx_test.go` and
  `TestDatabaseSQLSharedTransaction` in `test/integration/driver_sql_test.go` —
  the seam itself, once per driver.
- `TestGormRollbackTakesVVWithIt` in `test/integration/driver_gorm_test.go`
  and `TestEntRollback` in `test/integration/driver_ent_test.go` — a rollback
  takes both halves.

## See also

[[D-027]] [[D-010]] [[D-016]] [[D-017]]
