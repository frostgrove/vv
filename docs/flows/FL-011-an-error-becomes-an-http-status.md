# FL-011 — An error becomes an HTTP status

**Entry point:** `http/crudhttp/errors.go:Status` (via each binding's `Handler.fail`)
**Implements:** [[UC-015]] · **Governed by:** [[D-015]] [[D-008]] [[D-019]] [[D-034]] [[D-038]] [[D-047]] [[D-046]] [[D-039]] [[D-040]] [[D-044]]

Every handler ends in `h.fail(c, err)`. One function decides the status, one
decides the body, and everything the library knows how to classify is a client
mistake or an access decision. Everything else is a 500 that says nothing.

Phase 1 of `ROADMAP-errors.md` added the contract this path moves onto —
`errs.Fault`, `errs.Violation`, `errs.Code`, `errs.Kind`, the SPI and the message
source. Phase 2 added the dialect parsers under `errs/sqlerr`. **Phase 3 wired
them together: the adapters now produce faults**, through `sqlfault.Wrap`. That
path is [[FL-014]]'s, and only its result arrives here.

`Status` was not edited, and that is [[D-038]]'s claim made good rather than a
coincidence: a fault wraps the sentinel it describes, so a classified duplicate
key still matches `crud.ErrConflict` and this switch answers 409 with no new arm
and no registration step.

Two things a fault does **not** yet change. Nothing maps `errs.Kind`, so a
retryable or class-22 fault is still a 500 — the 503 arrives with the status
table at phase 4. And `ErrorBody` is still the body; the envelope replaces it at
phase 4 too. What did change is one observable thing, and it is in the failure
table below: a 409 whose failure was classified now carries the fault's own
classification text — `errs: conflict: unique (1 violation)` — instead of the
driver's sentence ([[D-047]]).

## Where each sentinel is produced

| Sentinel / type | Declared | Produced by |
|---|---|---|
| `crud.ErrNotFound` | `crud/errors.go:10` | `repository.GetByID` (`repo/basic/repository.go:121`), `repository.refresh` (`:526`), the `Update` load (`:554`), `missedRow` (`:661`), `gate.loadScoped` (`repo/decorators/security/security.go:258`), `Handler.Delete` when 0 rows went away (`http/crudfiber/handler.go:279`, `http/crudgin/handler.go:318`), `specs.FindOne`/`FindFirst` |
| `crud.ErrConflict` | `crud/errors.go:31` | the adapters — `adapter/crudsql:Executor.conflict` and `adapter/crudpgx:Executor.conflict`, both one line over `sqlfault/classify.go:Wrap`. The gate is `sqlfault/gate.go:Integrity` and it is not a class test ([[D-046]], [[FL-014]]) |
| `*errs.Fault` | `errs/fault.go:42` | `sqlfault/classify.go:Classifier.Classify`, through those same two methods. It **wraps** the sentinel rather than replacing it, which is why nothing below needed editing ([[D-038]]) |
| `crud.ErrStaleVersion` (wraps `ErrConflict`) | `crud/errors.go:36` | `repository.missedRow` (`repo/basic/repository.go:659`) |
| `specs.ErrNotUnique` (wraps `ErrConflict`) | `repo/decorators/specs/errors.go:13` | `specs.FindOne` when two rows match |
| `crud.ErrForbidden` | `crud/errors.go:25` | wrapped by `security.ErrForbidden` (`security.go:27`) and produced by `security.Denied` (`security.go:141`); a service layer is expected to wrap it too |
| `crud.ErrMissingID` | `crud/errors.go:16` | `repository.Save` on an unset `noauto` key (`repository.go:454`) |
| `*query.Error` | `query/compile.go:13` | every rejection in the wire DSL, carrying `Path` and `Reason` |
| `*crud.UnknownFieldError` | `crud/errors.go:41` | `Schema.Field` misses in the repository and the SQL writer |
| `*crud.SchemaError` | `crud/errors.go:52` | declaration-time and render-time refusals |
| `crudhttp.ErrBadRequest` | `http/crudhttp/errors.go:16` | `BadRequest` / `BadRequestf` (`errors.go:19`) — malformed body, unparseable id, `MaxBulk` exceeded. One sentinel for every binding, so a 400 raised by one is recognised by the shared switch ([[D-034]]) |
| `crud.ErrNoTxSupport` | `crud/errors.go:13` | `crud.InTx` on a handle that cannot begin — **not classified**, so 500 |
| `crud.ErrReadOnly` | `crud/errors.go:22` | nothing in the tree; it exists for a decorator that wants to say "read-only" rather than "forbidden" — **not classified**, so 500 |

## The path

1. **The adapters classify first, and there are two gates.**
   `adapter/crudsql:Executor.conflict` and `adapter/crudpgx:Executor.conflict`
   both call `sqlfault.Wrap`. `sqlfault.Integrity` decides `crud.ErrConflict` —
   class 23, MySQL's `HY000` numbers, SQLite's result code, and never a class
   test alone ([[D-046]]) — while `errs/sqlerr:Classify` decides the `errs.Code`
   the fault carries. Neither gate contains the other, and [[FL-014]] is where
   the whole path, the extraction shapes and the engine-declaration rule are
   written down. The driver error is kept underneath either way, so a caller can
   still `errors.As` their way to the constraint name.

   `crudpgx.rows.Err` is where it actually happens on PostgreSQL: pgx does not
   report a failed statement from `Query`, it hands back a live `Rows` whose
   first `Next` is false and whose `Err` carries the error — and on PostgreSQL
   every insert and update runs as `… RETURNING`, i.e. as a query.

   **`Commit` classifies too.** A `DEFERRABLE INITIALLY DEFERRED` constraint is
   checked at the top-level `COMMIT` and not at the statement, so the violation
   arrives with nothing having just failed. `crudsql.Tx.Commit` and
   `crudpgx.Tx.Commit` are where that lands, and untouched they handed the
   driver error straight back: that one shape of conflict was a 500 while the
   immediate shape was a 409. `crudsql.savepoint.Commit` classifies as well, and
   for a different reason — no engine has been measured to raise an *integrity*
   error at `RELEASE SAVEPOINT`, PostgreSQL included, which hands a deferred
   check to the parent transaction and fires it at `COMMIT`. What does arrive
   there is `25P02`: a statement the server refused inside the savepoint poisons
   the transaction, and the `RELEASE` after it is refused — so that door is what
   gives a nested `Commit` `transaction_aborted` rather than an anonymous 500. It
   is parity with `crudpgx` as well, whose nested `Begin` returns the same `Tx`
   whose `Commit` classifies ([[FL-009]]), so a nested write cannot be a 409
   through pgx and a 500 through `database/sql`.

2. **`Handler.fail`** — one per binding, in `http/crudfiber/handler.go`,
   `http/crudgin/handler.go` and `http/crudnet/handler.go` → `h.opt.errorHandler`,
   which is that binding's `DefaultErrorHandler` unless `WithErrorHandler`
   replaced it. Every route in every binding ends here; no route has its own
   mapping.

3. **`Status`** — `http/crudhttp/errors.go:39`
   A single ordered switch, using `errors.Is`/`errors.As` so wrapped errors
   classify:
   ```
   nil                                      -> 200
   ErrNotFound                              -> 404
   ErrForbidden                             -> 403
   ErrConflict                              -> 409     (ErrStaleVersion, ErrNotUnique)
   ErrBadRequest | *query.Error |
   *UnknownFieldError | *SchemaError |
   ErrMissingID                             -> 400
   anything else                            -> 500
   ```
   **The order is load-bearing.** An error that wraps two sentinels gets the
   first matching arm; 404 before 403 is the same instinct as
   `gate.loadScoped`'s ([[FL-007]]) — do not confirm that a row exists.

4. **`Body`** — `http/crudhttp/errors.go:80`
   ```go
   status := Status(err)
   body := ErrorBody{Error: StatusText(status)}
   switch {
   case errors.As(err, &qe):                      body.Path, body.Message = qe.Path, qe.Reason
   case status != http.StatusInternalServerError: body.Message = err.Error()
   }
   ```
   A `query.Error` reports its own `Path` and `Reason` — the clause that was
   wrong and why, and nothing internal. Everything else non-500 gets
   `err.Error()`. **A 500 gets no message at all**: the underlying text could be
   a SQL statement, a constraint name or a connection string.

   This is why a fault's `Error()` is classification only ([[D-047]]). Where the
   failure was classified the fault is the outermost error, so the 409 body's
   `message` is `errs: conflict: unique (1 violation)`; where it was not, the
   driver's own sentence is still copied through, constraint name and all. Phase
   4 removes the path rather than the disclosure.
   `StatusText` (`http/crudhttp/errors.go:61`) produces the stable
   machine-readable code: `not_found`, `forbidden`, `conflict`, `bad_request`,
   `internal_error`.

5. **The response.** `{"error":"…","path":"…","message":"…"}` — `ErrorBody`,
   `http/crudhttp/errors.go:27`, with `omitempty` on the last two. Each binding's
   `DefaultErrorHandler` is only the writer — one call to `crudhttp.Body(err)`
   and one response write: `http/crudfiber/options.go:99`,
   `http/crudgin/options.go:103` (which also hands the error to Gin's logger
   through `c.Error`, because the body is not allowed to carry it),
   `http/crudnet/options.go:102`.

## Where the decisions bite

- **A 500 leaks nothing.** One `case` in `crudhttp.Body`. Adding `err.Error()`
  to the default 500 branch is the change that turns an internal error into an
  information disclosure.
- **`ErrConflict` is produced by adapters, never by the repository.** That is
  what keeps the classification in one place per driver and out of every write
  path. A new adapter that skips it turns duplicate keys into 500s.
- **A fault is additive, and `Status` proves it.** Nothing in this switch knows
  about `errs.Fault`. A classified conflict reaches 409 because the fault wraps
  `crud.ErrConflict`, and a classified `22001` reaches 500 because it wraps no
  sentinel at all ([[D-038]]). A fault must not become a status by being a fault.
- **No `Kind` → status arm belongs here yet.** `errs.KindRetryable` and
  `errs.KindValidation` both exist and are both carried by faults this path now
  sees. Mapping either one here would ship two mappings for one failure, which is
  the outcome `ROADMAP-errors.md` §2 names as the one that must not happen
  ([[D-045]], phase 4).
- **`crudpgx` classifies in `rows.Err`, not only in `Query`.** Without it a
  duplicate key reached the client as a bare 500 through pgx while the same
  statement through `database/sql` answered 409.
- **Sentinels are the transport contract.** A service layer wraps
  `crud.ErrForbidden`/`ErrNotFound`/`ErrConflict` and gets the right status
  without importing anything from `http/`. `security` wraps `crud.ErrForbidden`
  for exactly this reason.
- **`ErrReadOnly` and `ErrNoTxSupport` are unclassified on purpose.** Neither is
  reachable from a well-formed request through the mounted routes; both would be
  a programming error, and 500 is the honest answer.

## Traps

- **DELETE is asymmetric.** `DELETE /:id` on nothing is 404
  (`handler.go:293`); `POST /bulk-delete` with ids that match nothing is
  `200 {"deleted":0}` (`handler.go:320`), and with an empty list it never
  reaches the repository at all (`handler.go:310`).
- **`Status` is exported.** A custom `WithErrorHandler` should reuse it rather
  than reimplement the mapping, or the two will drift.
- **A `query.Error` is 400 by type, not by wrapping.** It is matched with
  `errors.As`, so wrapping one in `fmt.Errorf("%w", …)` preserves the status but
  a `query.Error` value copied into a plain `errors.New` does not.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| unknown field, bad value, bad operator | `query` compile → `*query.Error` | 400 with `path` and `message` |
| unparseable `:id`, malformed body, over `MaxBulk` | `badRequestf` | 400 `bad_request` |
| unset `noauto` primary key on save | `ErrMissingID` | 400 |
| row absent or out of scope | `ErrNotFound` | 404 `{"error":"not_found"}` |
| policy denial | `security.Denied` → `ErrForbidden` | 403 |
| duplicate key / FK / NOT NULL / CHECK, PostgreSQL or MariaDB | `sqlfault.Integrity` — class 23 | 409, and where the engine was declared the body reads `errs: conflict: unique` rather than the driver's sentence |
| the same on MySQL, where a CHECK is `3819` and a missing default `1364` | `Integrity`'s `HY000` arm, which reads the number only under that state | 409. Both were an unclassified 500 before the number list ([[D-046]]) |
| the same on SQLite, which reports no SQLSTATE at all | `Integrity`'s no-state arm, on `SQLITE_CONSTRAINT` in the low byte of the extended result code | 409. All seven classes were an unclassified 500 until that arm existed |
| any of the above through `crudsql.Open`, `From` or `Source`, or a class-23 number no probe produced | gate 1 only — no engine was declared, or no table has the number | 409 **with the driver's message**, because no code was learned. [[FL-014]] |
| a value too long, out of range, or of the wrong type | `errs/sqlerr` classifies it — `too_long`, `out_of_range`, `invalid_format`. The sentinel gate still refuses class 22 ([[D-015]]) | 500, and now a 500 carrying a fault: the code and the kind are on the error, nothing maps `Kind` yet, and the body still says nothing. [[UC-017]] is where it becomes a 422 |
| a lock timeout, a deadlock, a serialisation failure | `errs/sqlerr` classifies all three, on `55P03`/`1205`/`SQLITE_BUSY`, `40P01`/`1213`, and `40001` | 500. A fault carrying `errs.KindRetryable` is produced from phase 3 — [[D-040]]'s 503 is **not** paid by it, and arrives with the status table at phase 4 |
| a PostgreSQL transaction poisoned by an earlier failure (`25P02`) | `errs/sqlerr` classifies it as `transaction_aborted` | 500, now carrying `errs.CodeTransactionAborted`: two saves in one `crud.InTx` where the first collides answer a truthful 409 and then a 500 whose body still says nothing |
| a deferred constraint, firing at `COMMIT` rather than at the statement | the adapters' `Tx.Commit` — `crudsql` and `crudpgx` both, each carrying the classifier its `Begin` propagated | 409 with the code, the same as the immediate shape. It was a 500 through both until `Commit` classified |
| stale optimistic-lock version | `ErrStaleVersion` | 409 |
| `FindOne` matched several rows | `specs.ErrNotUnique` | 409 |
| driver failure, closed pool, context cancelled | nothing classifies it | 500 `{"error":"internal_error"}` and no detail |

Every row above that still carries "the driver's message" is a disclosure
[[D-044]] refuses and phase 4 of `ROADMAP-errors.md` closes. Phase 3 narrowed the
set rather than emptying it: a 409 whose failure was classified carries the
fault's text and nothing a driver said ([[D-047]]), and one that was not carries
the sentence as before. It is recorded as [[UC-015]]'s guarantee 11, which still
does not hold.

## Files

| File | Role |
|---|---|
| `http/crudhttp/errors.go` | `Status`, `Body`, `StatusText`, `ErrorBody`, `ErrBadRequest`, `BadRequest` — the whole mapping, shared by every binding |
| `http/crudfiber/options.go`, `http/crudgin/options.go`, `http/crudnet/options.go` | the exported `Status` and `DefaultErrorHandler` per binding: one line and one response write each |
| `http/crudfiber/handler.go`, `http/crudgin/handler.go`, `http/crudnet/handler.go` | `fail`, and the DELETE asymmetry |
| `crud/errors.go` | every sentinel, `UnknownFieldError`, `SchemaError` |
| `query/compile.go` | `query.Error` — path and reason, safe to hand back |
| `adapter/crudsql/conflict.go` | `Executor.conflict` — one line over `sqlfault.Wrap`, and the note that this package may not name a driver's error type |
| `adapter/crudsql/crudsql.go` | the four engine-declaring constructors and the three that decline to name one, `WithFaults`, and the propagation into `Tx` and `savepoint` |
| `sqlfault/gate.go` | `Integrity` — the `crud.ErrConflict` gate, three arms, because the four engines answer in three ways ([[D-046]]) |
| `sqlfault/extract.go` | by-shape extraction and the tree walk that sees through a multi-error and through a fault ([[D-038]]) |
| `sqlfault/classify.go` | `Classifier`, `Wrap` — where a fault is assembled and where the sentinel is guaranteed |
| `sqlfault/catalog.go` | the one-method schema SPI that fills in columns a driver did not name |
| `sqlfault/doc.go` | the two gates, what no arm may read, why the engine is declared |
| `errs/doc.go` | what the package is, what it refuses, which half of it the first tag freezes, and the two rules that are not visible in a signature |
| `errs/code.go` | `Code` and its constants; `Kind`, its eight constants and `Kind.String` |
| `errs/codes.go` | `Codes` — the wired vocabulary — `StandardCodes`, `Add`, `KindOf`, `MessageFor`, `ErrCodeRedeclared` |
| `errs/path.go` | `Step`, `Path`, `Named`, `Indexed`, the three renderings (`MarshalJSON`, `String`, `Pointer`) and `ParsePath` |
| `errs/violation.go` | `Origin`, `Source`, `Violation` and its public projection |
| `errs/fault.go` | `Detail`, `Fault`, `Fault.Error`, `Fault.Unwrap`, `Fault.MarshalJSON`, `AsFault` |
| `errs/build.go` | `Builder` and `P` — the hand-built fault, and `Wrapping`, the only way a sentinel is attached. The rule that resolves the chain's ambiguity, which the plan was silent on: `Code`, `Params` and `Message` apply to the violation the most recent `Field`/`At`/`General` opened; with none open, `Code` and `Message` fall to the fault, and the four steps with no fault-level meaning — `Params`, `Origin`, `Source`, `Approximate` — open a general violation rather than dropping what they were given, so a misordered chain produces a visibly odd fault instead of a silently empty one ([[D-021]]). `Fault()` copies path, params and column lists deep, so a resolver rewriting a hop in place cannot reach a fault the builder already handed back |
| `errs/spi.go` | `Classifier`, `Resolver`, `CodeMapper`, `MessageSource`, `Chain`. No `Renderer`: it is HTTP-shaped and stays in `http/crudhttp` ([[D-045]]) |
| `errs/message.go` | `Messages` — the four-level ladder, the locale fallback and the template expansion. The four levels come from the path's first and last **named** steps and not from its depth, so a key spelling a whole nested path is accepted by `Add` and never consulted |
| `errs/bridge.go` | `FieldViolation` and `FromFieldViolations` — a validation library's errors, with no import of one |
| `errs/sqlerr/doc.go` | what a parser takes and produces, why the four files are keyed differently, the contract/fixture split, and why these are not an `errs.Classifier` |
| `errs/sqlerr/classify.go` | `Classify` — the one exported entry point, a switch on the four dialect strings, total on a nil error and on a dialect it does not know |
| `errs/sqlerr/postgres.go` | keyed on the **whole SQLSTATE** and never the class; the native number is never read, because pgconn has none. The only parser that fills in an `errs.Source` |
| `errs/sqlerr/mysql.go` | keyed on the **state and the number together**, because `1205` and `3819` share `HY000` and mean opposite things |
| `errs/sqlerr/mariadb.go` | the same shape, its own table, differing at exactly two rows: `23000`/`4025` for a CHECK and `22007`/`1366` for a bad value |
| `errs/sqlerr/sqlite.go` | keyed on the **number read as bytes**, and refuses anything carrying a SQLSTATE at all |
| `errs/sqlerr/corpus.go` | the corpus types: what a captured driver error holds, and `SameKey`, which is what a classifier is allowed to depend on |
| `errs/sqlerr/testdata/corpus/*.json` | four files, one per engine, twenty provoked violations each. The evidence every arm above rests on |
| `test/corpus/` | what provokes them — `Engine`, its case table, and the capture. Lives in the test module because it needs the drivers |
| `test/cmd/corpus/` | `make corpus`, and the report of what moved since the last capture |
| `adapter/crudpgx/conflict.go`, `adapter/crudpgx/crudpgx.go` | the same for pgx, including `rows.Err`, plus `extract` — the typed reader for `*pgconn.PgError`, which this module may name |
| `repo/decorators/security/security.go` | `ErrForbidden`, `Denied` |
| `repo/decorators/specs/errors.go` | `ErrNotUnique`, `ErrUnboundedDelete`, `ErrUnboundedUpdate` |
| `repo/decorators/specs/executor.go` | `FindOne` / `FindFirst` — where `ErrNotFound` and `ErrNotUnique` are raised |

## Tests that walk this flow

Every test below that names `http/crudfiber/` has an identical twin in
`http/crudgin/` and `http/crudnet/`, same name, same file name. The three suites
are ported one to one and the mapping they exercise is one switch ([[D-034]]) —
removing an arm from `crudhttp.Status` fails all three, which is the check that
it is shared rather than copied.

- `TestStatusMapsWhatItPromisesTo` — `http/crudfiber/edge_test.go` — the switch, arm by arm.
- `TestRepositoryErrorsBecomeStatusCodes` — `http/crudfiber/edge_test.go`.
- `TestEveryRouteMapsARefusalTheSameWay` — `http/crudfiber/edge_test.go` — no route has its own mapping.
- `TestA500NeverEchoesTheInternalError` — `http/crudfiber/edge_test.go` — the disclosure guard.
- `TestDeletingNothingIs404ForOneRowAndZeroForASet` — `http/crudfiber/edge_test.go`.
- `TestAQueryThatNamesSomethingTheModelLacksIsABadRequest` — `http/crudfiber/edge_test.go`.
- `TestEveryRejectionNamesThePathThatWasWrong` — `query/edge_test.go` — the `Path` field.
- `TestAnIntegrityConflictIsA409WithAMessage` — `http/crudfiber/write_edge_test.go`.
- `TestAClassifiedConflictsBodyCarriesNothingInternal` — `http/crudfiber/write_edge_test.go` — [[D-047]] under live load: the 409 body of a *classified* conflict names no constraint, no table, no schema, no SQLSTATE and no statement, and its control is the unclassified one beside it, asserted to still leak.
- `TestIntegrityErrorsAreClassifiedWhateverShapeTheDriverUses` — `adapter/crudsql/conflict_test.go`.
- `TestAClassifiedConflictIsNotAnyOtherSentinel` — `adapter/crudsql/conflict_test.go`.
- `TestASQLSTATEIsStillFoundThroughAMultiErrorAndThroughAFault` — `adapter/crudsql/conflict_test.go` — [[D-038]]'s owed regression, on all three readers.
- `TestTheExtractorReachesTheStructuredFieldsByShape` — `adapter/crudsql/conflict_test.go`.
- `TestOnlyADeclaredEngineProducesACode` / `TestAMariaDBNumberIsOnlyReadByTheMariaDBConstructor` — `adapter/crudsql/classify_test.go` — what a 409 carries when no engine was named.
- `TestTheTwoGatesAnswerDifferentQuestions` — `sqlfault/gate_test.go` — which gate decides the status and which decides the code, with a counter per cell of the 2×2.
- `TestAFaultIsBuiltOnlyWhenACodeAndItsKindAreKnown` / `TestASentinelIsAttachedWhateverTheClassifierReturned` / `TestAnUnknownDialectStillAnswersTheIntegrityGate` — `sqlfault/classify_test.go`.
- `TestAFaultCarriesNothingTheDriverSaidInItsErrorText` — `sqlfault/classify_test.go` — [[D-047]] at the producer, which is the one site that can break it.
- `TestTheTypedExtractorReadsThePgErrorFieldsThatExist` / `TestBothExtractorsAgreeOnOnePgError` — `adapter/crudpgx/conflict_test.go`.
- `TestEveryCorpusCaseReachesTheCallerAsTheFaultTheCorpusNames` — `test/integration/corpus_test.go` — four engines, every case, the code compared against the checked-in corpus, with per-engine counters of faults produced and negatives walked.
- `TestAMariaDBCheckIsOnlyClassifiedWhenTheSourceSaysMariaDB` / `TestACatalogFillsTheColumnsAUniqueViolationDoesNotName` — `test/integration/corpus_test.go`.
- `TestADuplicateKeyIsAConflictWhicheverWayPgxReportsIt` — `adapter/crudpgx/conflict_test.go` — including `rows.Err`.
- `TestOnlyIntegrityErrorsBecomeConflicts` — `adapter/crudpgx/conflict_test.go` — extended at phase 3: an unclassifiable state produces no fault on any of the three pgx paths, with a `40001` in the same table producing one as its control.
- `TestIntegrityViolationsAreClassifiedByEveryAdapter` — `test/integration/dialect_edge_test.go` — through every adapter, on PostgreSQL, MySQL and MariaDB, and from phase 3 asserting the code as well as the sentinel. It walks `egTargets()`, which has **no SQLite entry**; that is how an entire dialect's misclassification went unnoticed, and why the two below exist. Its two ent entries are built on `crudsql.From` and are asserted to carry no fault at all, which is the declared-engine degradation against a real ORM.
- `TestEveryCorpusCaseClassifiesAsTheCorpusSays` — `test/integration/corpus_test.go` — twenty provoked violations against four live engines, asserting both that an integrity case is a conflict and that a data, retryable or unclassifiable one is not.
- `TestTheCorpusStillDescribesTheseServers` — `test/integration/corpus_test.go` — recaptures and compares the key a classifier dispatches on. Deliberately not the message: see [[D-039]]. It also asserts that a field the corpus records as redacted comes back redacted, with the count of redacted fields walked as the control ([[D-040]]).
- `TestSameKeySeparatesTwoCapturesThatWouldClassifyDifferently` — `errs/sqlerr/corpus_test.go` — the control for the guard above, whose only teeth are `Err.SameKey`. Unequal pairs from the corpus move the SQLSTATE, the number, the driver type and one field name in turn, each with the precondition that the rest of the pair did not move; the self-comparison and the localised twin are the two legs a stuck comparator fails.
- `TestSavingAnUnchangedCorpusRewritesNothing` — `errs/sqlerr/corpus_test.go` — `Save`'s byte-identical promise, which is what makes the corpus diff readable at all. Its control is a corpus naming a different server, which has to come out different.
- `TestADeferredConstraintArrivesFromTheCommitAndNotTheStatement` — `test/integration/corpus_test.go` — PostgreSQL through `crudsql` and `crudpgx` and SQLite through `crudsql`: the insert is accepted, the commit is refused, and the refusal is a conflict. The immediate foreign key in the same run is the control. It walks top-level beginners only, so `crudsql.savepoint.Commit` is not what it exercises — no engine raises *integrity* at `RELEASE SAVEPOINT`.
- `TestANestedCommitOnAPoisonedTransactionCarriesItsCode` — `test/integration/edge_test.go` — the savepoint door on the one thing that does reach it, `25P02`, through both PostgreSQL adapters, with a healthy nested commit as the control. Reverting `savepoint.Commit` to `return err` reddens the `crudsql` leg alone.
- `TestAUniqueIndexOnAnExpressionFillsNoColumns` — `test/integration/corpus_test.go` — a key part no column can name is a miss rather than a blank column name, with the plain key on the same table and the same catalog as the control.
- `TestEveryClassifiableCorpusCaseGetsTheCodeItsClassNames` — `errs/sqlerr/classify_test.go` — every classifiable case on four engines. Its control is that the corpus's four undeclared classes are exactly the four the parsers coarsen.
- `TestTheCorpusNegativesStayUnclassified` — `errs/sqlerr/classify_test.go` — the refusals that must stay refused, with the count of real negatives walked asserted per engine so an emptied loop cannot pass.
- `TestAParserAnswersTheSameWhateverTheServerSaid` — `errs/sqlerr/classify_test.go` — [[D-039]]'s invariant: every case, four engines, classified as captured and with the message and the `Detail`/`Hint` replaced.
- `TestTheSameViolationInAnotherLocaleClassifiesIdentically` — `errs/sqlerr/classify_test.go` — [[D-039]]'s evidence: the duplicate key MySQL and MariaDB answer in Russian.
- `TestNoParserAnswersWithTheCorpusFinerVocabulary` — `errs/sqlerr/classify_test.go` — [[D-044]]: no code says which index was hit.
- `TestARetryableCaseNeverAnswersAConflictOrValidationCode` — `errs/sqlerr/classify_test.go` — [[D-040]], the only test here whose expectation is not written beside the parser.
- `TestOnlyPostgreSQLFillsInASource` — `errs/sqlerr/classify_test.go` — where a `Source` may come from, and the nil-versus-empty column list.
- `TestAnUnknownDialectAndANilErrorAreRefusedRatherThanPanicking` / `TestEveryEngineAnswersTheSameQuestions` — `errs/sqlerr/classify_test.go`.
- `TestARefusalFromOneEngineDoesNotClassifyThroughAnothersParser` — `errs/sqlerr/dialect_test.go` — the dialect half of [[D-046]]'s key, with the diagonal in the same loop.
- `TestMySQLAndMariaDBDoNotAnswerForEachOtherWhereTheyDiffer` — `errs/sqlerr/dialect_test.go` — the only thing in the tree that forces `mariadb.go` to exist.
- `TestASQLiteResultCodeIsReadAsBytesAndNotWhole` / `TestASQLiteCodeIsOnlyReadWhereThereIsNoSQLSTATE` — `errs/sqlerr/dialect_test.go` — the low byte, busy-snapshot 517, and the state guard.
- `TestSQLiteConstraintViolationsBecomeConflicts` / `TestAnOrdinarySQLiteErrorIsStillNotAConflict` / `TestASQLiteCodeIsOnlyTrustedWithoutASQLSTATE` — `adapter/crudsql/conflict_test.go` — the SQLite arm and its two controls.
- `TestMySQLIntegrityErrorsOutsideClass23BecomeConflicts` / `TestAnOrdinaryHY000IsStillNotAConflict` / `TestANumberIsOnlyTrustedUnderHY000` — `adapter/crudsql/conflict_test.go` — the MySQL arm and its two controls.
- `TestWithErrorHandlerReplacesTheMapping` — `http/crudfiber/options_test.go`.
- `TestAScopeThatFailsIsMappedLikeAnyOtherError` — `http/crudfiber/edge_test.go`.
- `TestHTTPRejections` — `test/integration/http_test.go` — end to end against a database.
- `TestGinHTTPRejections` — `test/integration/http_gin_test.go` — the same, through the Gin binding.
- `TestNetHTTPRejections` — `test/integration/http_net_test.go` — and through the net/http binding.

The tests below pin the contract this flow moves onto at phase 4. Most exercise
no handler and no database; they are listed here because this is the flow that
inherits them. Phase 3 made two of them live rather than theoretical — a fault
does now reach `Status`, from either adapter.

- `TestAFaultKeepsItsSentinelReachableThroughStatus` / `TestAFaultWrappingNoSentinelIsStillAnInternalError` — `http/crudhttp/errors_test.go` — [[D-038]] against a real `crud` sentinel, through the unedited `Status`.
- `TestAFaultWrappingASentinelMatchesIt` / `TestAFaultWrappingNothingMatchesNothing` — `errs/fault_test.go` — the mechanism and its negative, which is the load-bearing half.
- `TestAFaultSurvivesBeingWrappedAgain` — `errs/fault_test.go` — all three of `errors.Is`, `AsFault` and `errors.As(driver)` through a further `fmt.Errorf`.
- `TestAsFaultAnswersFalseForAnythingThatIsNotAFault` — `errs/fault_test.go` — the control the three `if !ok` call sites need: an `AsFault` that always reported true handed every one of them a nil `*Fault` with the suite green.
- `TestAFaultsErrorTextCarriesNothingInternal` — `errs/fault_test.go` — [[D-047]]: `Error()` is classification only, because `Body` copies it into every body below 500.
- `TestAFaultsErrorTextNamesWhicheverOfOpAndEntityItWas` — `errs/fault_test.go` — the four prefix arms; only the both-set one was reachable from the test above.
- `TestAMarshalledFaultNamesNothingInternal` / `TestAViolationMarshalsOnlyFieldCodeAndMessage` — `errs/marshal_test.go` — [[D-044]] carried by the types, with the default marshal as the control.
- `TestAPrintedFaultNamesNothingInternal` / `TestAPrintedViolationNamesNothingInternal` — `errs/marshal_test.go` — the third projection, the one a log line reaches: `%v` and `%+v` on a value, in a struct field, in a map entry and on the whole violation slice. The control is a shim struct of the same shape with no `String` method, asserted to leak through `fmt`'s struct printer; without it both `String` methods could be deleted with the root module green.
- `TestAFaultWithNoViolationsRendersAnEmptyArray` — `errs/marshal_test.go` — the empty envelope field is an array, not null; its control is the default marshal of a nil violation slice.
- `TestTheRetryableCodesAreTheirOwnKind` — `errs/codes_test.go` — [[D-040]]'s kind, `transaction_aborted` included.
- `TestEveryStandardCodeHasTheKindTheStatusTableGivesIt` — `errs/codes_test.go` — every row of `ROADMAP-errors.md` §2's code→kind table, written out rather than derived, with a declared-but-unpinned code as the control. Only the kind reaches the status table, so a row with the wrong one is the wrong status everywhere, and eight such edits at once left the root module green.
- `TestRedeclaringACodeWithADifferentKindIsRefused` / `TestTheInternalCodeHasNoDefaultMessage` / `TestTheZeroKindIsInternalAndSoIsAnUnknownOne` / `TestANilCodesReadsAsEmptyInsteadOfPanicking` — `errs/codes_test.go` — the wired vocabulary, the last one with the wired catalogue as its control.
- `TestAPathRendersThreeWaysFromOneValue` / `TestAPointerEscapesWhatRFC6901Requires` / `TestParsePathRoundTripsTheDottedForm` / `TestAParsedPositionIsANumberOnTheWire` / `TestABracketedNegativeNumberStaysPartOfTheName` — `errs/path_test.go` — the three renderings, the parser, and why a client reading `field[1]` gets a number rather than a string.
- `TestEachLevelOfTheMessageLadderResolves` / `TestATemplateWithAMissingParamFallsBackRatherThanEmittingThePlaceholder` / `TestAMessageExpandsByteIdenticallyEveryTime` / `TestTwoLocalesThroughTheSameFaultGiveTwoMessages` / `TestAnIndexedPathResolvesTheSameMessageAsAnyOtherRow` / `TestRedeclaringAMessageWithDifferentTextIsRefused` — `errs/message_test.go` — the ladder, what it falls back to, and the redeclaration guard.
- `TestAPOSIXLocaleFallsBackTheSameWayAHyphenatedOneDoes` / `TestALocaleIsWalkedBeforeAKeyIsNarrowed` / `TestOnlyTheFirstAndLastNamedStepsReachTheLadder` — `errs/message_test.go` — the two separators a locale arrives with, the locale-outer walk (the only shape that tells it from a key-outer one), and the collapse to the first and last named steps, which is what makes a key spelling a whole nested path silently unreachable.
- `TestAViolationCodeIsNotTheFaultsCode` / `TestAViolationMessageIsNotTheFaultsMessage` — `errs/build_test.go` — whose code is whose, and whose message. Each has two halves and each half is the other's control; routing every `Message` to the fault left the root module green.
- `TestEveryEntryPointCarriesItsKind` — `errs/build_test.go` — the eight constructors, with `Internal` as the control because its kind is the zero value.
- `TestAPerViolationStepWithNoViolationOpenOpensAGeneralOne` / `TestOriginSourceAndApproximateAlsoOpenAGeneralViolation` — `errs/build_test.go` — the misordered-chain rule, which lives nowhere else: a per-violation step arriving before any `Field`/`At`/`General` opens a general violation rather than dropping what it was given.
- `TestTheStepsThatWriteNothingElseReadsStillWrite` — `errs/build_test.go` — the four builder steps nothing else reads back: `Origin`, `Source`, `Approximate` and `Partial`. Stubbed to `return b` they left the whole root module green, so each is pinned on the value it writes, with the unwritten violation as the control.
- `TestAFaultDoesNotShareASliceWithTheBuilderOrTheCaller` — `errs/build_test.go` — the copy `Fault()` promises is deep: two faults from one builder share no `Path` array, a caller's scratch path and column lists do not stay live inside a fault, and an absent column list still reports nil. The last is the control on the clone helper, which an unconditional `make` would turn from "not known" into "no columns".
- `TestWrappingSkipsANilErrorAndKeepsTheRest` — `errs/build_test.go` — no nil reaches `Unwrap() []error`, with the sentinel still matching as the control.
- `TestAChainReportsWhenAHopDeclined` — `errs/spi_test.go` — [[D-043]]'s mechanism.
- `TestTheMeasuredValidatorNamespacesBecomePaths` / `TestARootThatDoesNotMatchKeepsEverySegment` / `TestAnIndexedNamespaceBecomesAnIndexStep` / `TestNoFieldViolationsConvertToNoSlice` — `errs/bridge_test.go` — over a hand-written fake, so `errs` imports nothing.
- `TestValidatorFieldErrorSatisfiesFieldViolation` / `TestWithoutTheTagNameFuncEveryPathIsGoFieldNames` — `test/bridge/fieldviolation_test.go` — the live validator, in the one module allowed to import it.

## See also

[[FL-001]] [[FL-002]] [[FL-003]] [[FL-007]] [[FL-008]] [[FL-012]] [[FL-013]]
[[UC-015]] [[UC-017]] [[D-046]] [[D-039]] [[D-040]] [[D-044]] [[D-047]] [[D-038]]
