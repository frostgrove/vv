# D-139 — A lock that owns its transaction may retry, and refuses what it cannot take

**Status:** accepted — narrows [[D-040]]
**Invariant:** The framework still never retries inside a transaction it did not open. A lock that opens its own transaction may replay it, because nobody else can see that transaction to replay it themselves. And an engine that cannot give the semantics the caller asked for is refused with a typed error, never answered with a weaker lock or with none.

## The decision

**Two parts, one package.** `lock` turns a key and a mode into a PostgreSQL
advisory lock. Both of the shapes below could reasonably have gone the other way,
and one of them contradicts a decision already in force.

### Retrying, and the line it does not cross

[[D-040]] says the framework does not retry, and the reason it gives is
ownership: *"a retry has to replay the whole transaction, and the framework does
not own it — [[D-009]] and [[D-027]] exist because a caller's executor arrives
through the context and may be shared with work the repository cannot see."*

That reason does not reach `Locks.Guarded`. It calls `crud.InNewTx` and the
transaction exists only for the duration of the call: no caller holds it, no
repository shares it, and the failure is not visible to anyone who could act on
it. D-040 recorded this case rather than dismissing it — *"when the repository
does own the transaction, nobody else can see the failure or retry it… this
decision is what it has to supersede if it wins"* — and the roadmap kept it open
in the same words until this decision closed it. It wins here, and only here.

The line, stated as code rather than as intent:

- `Locks.Guarded` and `Locks.Retry` retry. They own the transaction.
- `Locks.Take` and `Locks.TryTake` take an executor the caller opened. They
  classify and hand back, exactly as D-040 requires.

Everything else D-040 forbids still holds. A repository, a decorator or a
binding still does not retry.

### Refusing an engine, rather than answering it weakly

`crud.Dialect` already models an engine that cannot do something as an empty
answer: `crud.SQLite.LockClause()` returns `""` and the statement is built
without a `FOR UPDATE`. That reading is right for a row lock, whose absence the
caller can still see in the rows it gets back.

It is wrong for an advisory lock. There is nothing to look at afterwards: a
caller handed no lock and no error runs its critical section believing it is
alone. So `lock.For` resolves the engine once, from the source's dialect, and
returns `ErrDialectUnsupported` for anything it cannot serve — at construction,
before a single statement is sent.

PostgreSQL is the only engine served today. MySQL and MariaDB are refused rather
than mapped onto `GET_LOCK`: that lock is session-scoped where this contract is
transaction-scoped, has no shared mode, and does not take part in InnoDB's
deadlock detection, so all three of the properties a caller asks for would be
absent. SQLite is refused because it has nothing to map onto.

### The two smaller rules the same package settles

**Guards are taken in one deterministic order** — ascending key, an exclusive
request before a shared one on the same key, duplicates collapsed to the
strongest. Which order is not the point and is not observable, since every guard
is held before the body runs; that there is exactly one is the point.

**A key's derivation is frozen.** `KeyOf` is FNV-1a over NUL-separated parts, and
the numbers it produces are pinned by literal in a test.

### Where it lives, and what that cost

The package sits at `vvdb/lock`, beneath the package that opens the connection
rather than beside the repository. Three placements were tried and two were
wrong:

- `crud/lock` reads as "locking CRUD", and the package has nothing to do with
  repositories, models or predicates — its consumers are three drivers and the
  application, none of them CRUD.
- Merging the lock into `vvdb` itself was rejected on the call site: `vvdb.Guard`
  and `vvdb.Sharing(...)` name the connection-opener where the reader needs the
  concept, `Policy` collides with the configuration nouns already there
  (`Pool`, `Params`, `Migration`) and has to become `LockPolicy`, and one package
  ends up with two audiences that never overlap — `main.go` for `Open`, every use
  case for `Guard`.
- `vvdb/lock` keeps `lock.Guarded` at the call site and still says, in the path,
  that this is the database below the repository.

It cost [[D-057]] its subtree reading: that decision now binds the `vvdb`
package rather than everything under it. The claim it protects — a service with
no vv in it can adopt `vvdb` — survives, because adopting `vvdb` means importing
`vvdb`, and `vvdb/dbpgx` was already the precedent for a subpackage with
dependencies the root may not have.

## Why

**Because without the retry, a deadlock is a 500 on a save.** Advisory locks
take part in deadlock detection like any other, so a writer holding A then B and
a worker holding B then A eventually meet as `40P01`. The caller has done
nothing wrong and the same request succeeds a moment later. D-040 is right that
this is a 503 rather than a 4xx — but a 503 handed to a person who pressed Save
is a failure the framework was in a position to absorb and chose not to.

**Because the transaction that would be replayed is one nobody else can name.**
D-040's second reason — *"on PostgreSQL a failed statement poisons the
transaction (`25P02`) so the retry cannot even run"* — is exactly why `Guarded`
opens a new one per attempt rather than reusing anything, and the test named in
*Proven by* asserts the second attempt is a second `Begin` — so a retry that
quietly reused the poisoned transaction fails the test rather than the request.

**Because a lock that silently is not one is the failure this package exists to
prevent.** The empty-`LockClause` reading was tried on paper first and produces
a caller that cannot tell success from a no-op, on the one code path whose entire
purpose is that two callers do not run at once.

**Because the deadlock the ordering prevents was previously prevented by
remembering.** Both trees already had the discipline written down as prose —
"take the deployment-wide guard first, the per-document one second" — and one of
them, `jobs/jobspg/repo_ops.go`, had already implemented it mechanically instead
by sorting. Sorting is the version that holds when the person who wrote the
prose is not in the room.

**Because a key is an agreement between processes that never meet, and changing
it fails nothing.** Not the build, not the tests, not the run: during a rolling
deploy the old pods and the new ones simply stop excluding each other, silently,
for as long as the rollout takes. A pinned literal is the only thing that turns
that into a failure someone sees.

## What it forbids

- Do not retry inside an executor the caller supplied. `Take` and `TryTake`
  classify and hand back; only `Guarded` and `Retry` may run something twice.
- Do not answer an unsupported engine with a no-op, a best-effort lock, or a
  weaker mode than was asked for. Refuse at `For`, before any statement.
- Do not map this contract onto MySQL `GET_LOCK` without saying, in this
  decision, what happened to transaction scope, the shared mode and deadlock
  detection.
- Do not change what `KeyOf` computes. A new derivation is a new function beside
  it, and a deployment that adopts it needs a window in which no lock is held —
  not a rolling restart.
- Do not switch to PostgreSQL's two-`int32` advisory form. It is a different
  keyspace from the one-argument form, so a mixed fleet excludes nothing, and
  the observability that reads `pg_locks` back reconstructs `classid`/`objid`
  from the 64-bit key.
- Do not let `Take` set `lock_timeout` per guard. It is transaction-wide.

## Where it lives

- `vvdb/lock/lock.go:Locks.Guarded` — the transaction this decision says may be
  replayed, and `vvdb/lock/lock.go:Locks.Retry` beside it, which is the loop on
  its own for a caller that opens the transaction some other way.
- `vvdb/lock/lock.go:Locks.Take` — the half that does not retry, and where
  `lock_timeout` is set once for the transaction rather than once per guard.
- `vvdb/lock/lock.go:ordered` — the one order.
- `vvdb/lock/postgres.go:backendFor` — the engine switch, and the only place
  that decides an engine is unserved.
- `vvdb/lock/errors.go:ErrDialectUnsupported` — the refusal.
- `vvdb/lock/retryable.go:Locks.RetryCode` — which failures count, read out of
  `errs/sqlerr`'s per-dialect table rather than a list kept here.
- `vvdb/lock/key.go:KeyOf` — the derivation that is frozen.

## Proven by

- `TestGuardedRunsTheWholeTransactionAgainWhenTheEngineSaysToTryAgain` in
  `vvdb/lock/lock_test.go` — a `40P01` on the first attempt is retried, the body
  runs once rather than twice, and the recorder's transaction depth is 2, so a
  retry that reused the poisoned transaction fails here.
- `TestGuardedHandsBackWhatASecondAttemptWillNotFix` in the same file — the
  control. A `23505` is attempted once and returned, so the retry loop cannot
  pass the test above by retrying everything.
- `TestGuardedStopsRetryingWhenTheCallerGivesUp` — a cancelled context ends the
  loop instead of sleeping through it.
- `TestALockFailureIsAnswerableAsRetryableAtTheBoundary` — the D-040 half that
  did not change: a lock timeout reaches `port.KindOf` as `errs.KindRetryable`,
  so it is still a 503 and still not a 4xx.
- `TestAnEngineWithoutTheseSemanticsIsRefusedRatherThanAnsweredWeakly` — a MySQL
  source is refused, hands back nothing to lock with, and is sent zero
  statements, with a PostgreSQL source beside it as the control: without that,
  the test passes for a `For` that refuses every engine.
- `TestGuardsAreTakenInOneOrderWhicheverOrderTheCallerAsked` — two callers
  asking for the same pair in opposite orders send the same two statements in
  the same order.
- `TestTheSameKeyAskedForBothWaysIsTakenOnceAndExclusively` — the collapse keeps
  the exclusive request, not whichever came first.
- `TestKeyOfStillAnswersWhatItAlwaysAnswered` — the pinned numbers.
- `TestTwoDatabasesDoNotShareACriticalSection` — two sources are two keyspaces,
  with one source behind two `Locks` as the control.

## See also

[[D-040]] [[D-009]] [[D-027]] [[D-046]] [[D-057]] [[FL-041]] [[UC-035]]
