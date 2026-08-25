# FL-014 — A driver error becomes a public violation

**Entry point:** `crud/sqlfault/classify.go:Wrap`, called by each adapter's `Executor.conflict`
**Implements:** [[UC-015]] [[UC-017]] · **Governed by:** [[D-046]] [[D-038]] [[D-039]] [[D-047]] [[D-044]] [[D-043]] [[D-041]] [[D-042]] [[D-015]] [[D-019]] [[D-040]]

**This flow makes one violation.** Turning it into a *public* one — the
envelope, the 422 arm, the `field` path a client reads — is [[FL-011]]'s, where
the fault meets the status table. Finding the **rest** of the violations one
payload caused is [[FL-017]]'s: a database reports the first constraint it
reaches and stops, and a second statement is what finds the others.

## The two gates, and which answers what

Two questions are asked of every failed statement, and they are not the same
question.

| | asks | decides | wider where |
|---|---|---|---|
| `crud/sqlfault/gate.go:Integrity` | did the database refuse to break a constraint | `crud.ErrConflict`, and so the 409 | a class-23 number nobody provoked (`1216`, `1217`); a SQLite low-byte-19 code whose high byte no probe produced |
| `errs/sqlerr/classify.go:Classify` | which violation was it | `errs.Code`, and so the code a client branches on | class 22 (`too_long`, `out_of_range`, `invalid_format`) and class 40 (`deadlock`, `serialization_failure`, `lock_timeout`), none of which is a collision |

Neither contains the other, and that is deliberate. Narrowing the sentinel onto
the parser's table would turn a duplicate key into a 500 on an engine nobody has
run yet ([[D-046]] says so in as many words); widening the parser onto the
sentinel's table would invent a code from documentation, which the same decision
forbids. `crud/sqlfault/gate_test.go:TestTheTwoGatesAnswerDifferentQuestions` is the
2×2 that holds them apart, with a counter per cell so a cell cannot quietly
empty.

## The path

1. **A statement fails.** Every path goes through one of the two adapters'
   `Executor`:
   `crudsql.Executor.Exec` / `.Query` (`crud/adapter/crudsql/crudsql.go`),
   `crudpgx.Executor.Exec` / `.Query` / `rows.Err` (`crud/adapter/crudpgx/crudpgx.go`),
   and both `Tx.Commit` — which is where a deferred constraint arrives, with no
   statement having just failed ([[FL-009]]).

2. **`Executor.conflict` → `sqlfault.Wrap(e.faults, err)`.**
   `crud/adapter/crudsql/conflict.go:16`, `crud/adapter/crudpgx/conflict.go:21`. One line
   each. The decision is in one place because the last time this rule lived in
   two, they diverged: a deferred constraint was a 409 through `crudsql` and a
   500 through `crudpgx`, with both test suites green.

3. **Already a fault? Return it.** `errs.AsFault(err)` short-circuits `Wrap`. A
   `crud.Source` wrapping another adapter's executor would otherwise classify
   twice, and the second fault would shadow the first for `errors.As`.

4. **Extraction.** `crud/sqlfault/extract.go:Extract` asks by shape:
   - SQLSTATE from a `SQLState() string` method (pgx, lib/pq) or an exported
     `SQLState` field in its `string` and `[5]byte` spellings, NUL-trimmed
     (go-sql-driver/mysql);
   - the number from a `Number` field, then a `Code() int` method, then integer
     `ExtendedCode` and `Code` fields — a `Code` field of *string* kind is never
     read as a number, because that is where pgconn keeps the SQLSTATE;
   - the structured fields from one whitelist, `crud/sqlfault/extract.go:carried`,
     taken off **one** error and off the engine's own wherever the tree holds
     one. `Detail` and `Hint` are ordinary names, so a repository's own wrapper
     carrying a `Detail` is met first by the walk and must not displace pgconn's
     constraint and table — which are the key step 8 looks the columns up by.
     `crud/sqlfault/extract.go:engineError` is the test: a state or a number, read,
     not a field name present. `errs.Fault` has a `Code` field of its own.

   `crud/adapter/crudpgx/conflict.go:extract` does it by name instead, because that
   module may spell `*pgconn.PgError`. The two are asserted to agree in
   `crud/adapter/crudpgx/conflict_test.go:TestBothExtractorsAgreeOnOnePgError`.

   **The walk is a tree walk, and that is load-bearing.**
   `crud/sqlfault/extract.go:walk` follows both `Unwrap() error` and
   `Unwrap() []error`. `errors.Unwrap` returns nil for a multi-error, which is
   exactly what step 6 builds with `fmt.Errorf("%w: %w", …)` and what
   `errs.Fault.Unwrap` returns — so a plain loop went blind on this layer's own
   output ([[D-038]]).

5. **Gate 1, then gate 2, then the kind.** `crud/sqlfault/classify.go:Classifier.Classify`
   asks `sqlerr.Classify(engine, e)` for the code, then the wired
   `errs.Codes.KindOf` for the kind, **and refuses to build unless both are
   known**. `errs.KindInternal` is the zero value, so a fault from an unwired
   vocabulary would claim 500 for a duplicate key.

6. **Assembly.** One `errs.Violation`: `Origin: errs.OriginState`, the same
   `Code`, the enriched `errs.Source`, a nil `Path`, `Approximate` false. An
   `errs.Detail` with the dialect, the SQLSTATE, the number, the constraint, the
   table, the columns and the driver error. `Wrapping(crud.ErrConflict, err)`
   when gate 1 said yes, `Wrapping(err)` when it did not.

7. **The sentinel, guaranteed at the seam.** `Wrap` re-checks `Integrity` and
   attaches `crud.ErrConflict` around any fault that does not already carry it,
   so a third-party `errs.Classifier` can neither forge a sentinel — `wrapped`
   is unexported, `Builder.Wrapping` is the only door — nor drop one.

8. **The columns the driver did not name.** `crud/sqlfault/catalog.go:Classifier.fill`
   asks the wired `Columns` SPI, and only where the parser returned no columns
   *and* the driver named both a table and a constraint. A miss leaves the list
   nil rather than empty: "not known" must not read as "no columns".

   A key part that is an **expression** is a miss too. `catalog.Constraint`
   records `lower(email)` as an empty name in `Columns` with the text in
   `Expressions`, and `""` is not a column: passed on it names one no schema has,
   and dropping just that part would describe `(tenant, lower(email))` as the key
   `(tenant)` — a key the engine does not enforce. `crud/sqlfault/catalog.go`
   refuses in both places, in `catalogColumns.ConstraintColumns` because it is
   the only thing that knows the positional convention, and again in `fill`
   because `Columns` is a third-party interface.

9. **The probe joins, where one is wired.** `crud/decorators/faults/probe.go`
   hands the fault to a `probe.Handler` before the path hop of step 8 runs, so
   the column-to-field translation is applied once over the driver's violation
   and the probe's alike ([[D-043]], one hop, one owner). Two rules the join
   never breaks: the driver's violation stays in the answer whatever the probe
   found, and a probe that failed keeps it and marks the fault incomplete rather
   than downgrading a truthful 409 ([[D-042]]).

   What the join *adds* is a `field` where the driver named no constraint. MySQL,
   MariaDB and SQLite carry no constraint in their structured error at all —
   "Which engine can say what", above — so a duplicate key arrived with nothing
   on it but a code. Where the probe found exactly one violation of that code,
   the driver's violation is folded into it and takes its path. [[FL-017]] has
   the whole of it.

10. **Out through `porthttp.Status`, unedited.** It is `StatusFor(port.KindOf(err))`
   now, and neither half needed an arm for this: the fault wraps the sentinel, so
   `errors.Is(err, crud.ErrConflict)` is still true and the switch answers 409
   with no new arm ([[D-038]]). See [[FL-011]] from here.

## Which engine can say what

From the corpus, not from documentation. `errs/sqlerr/testdata/corpus/`.

| | SQLSTATE | number | constraint | table | schema | column |
|---|---|---|---|---|---|---|
| PostgreSQL | yes | **never** — pgconn has none, every entry records 0 | yes | yes | yes | `23502` only |
| MySQL | yes | yes | no | no | no | no |
| MariaDB | yes | yes | no | no | no | no |
| SQLite | **never** | yes (extended result code) | no | no | no | no |

`mysql.MySQLError` has `Number`, `SQLState` and `Message` and nothing else, so
on MySQL and MariaDB a fault carries the code and the number and no structural
fact at all. **The catalog does not close that gap**: step 8 is keyed on a table
and a constraint the driver never names, so `WithColumns` changes nothing on
those two engines — nor on SQLite, for the same reason ([[D-019]] §10(a)).
**Only PostgreSQL populates `Fields` at all, and it is the only engine where the
`Columns` SPI is ever asked.**

## Where the decisions bite

- **The engine is declared, never derived** ([[D-046]]). `crud.Dialect.Name`
  answers `"mysql"` for MariaDB, and a type switch over the dialect types is the
  same derivation written differently. So the string is a literal in
  `crudsql.Postgres` / `MySQL` / `MariaDB` / `SQLite` and in `crudpgx`, and
  `Open`, `From` and `Source` — which are handed a dialect, not an engine —
  carry no classifier at all. The only reachable failure is *no* code, never a
  wrong one.
- **A fault must not become a status by being a fault** ([[D-038]]). `22001`
  classifies as `too_long` and wraps no sentinel at all, so nothing in the
  sentinel half would give it a 4xx; it reaches 422 because its *kind* does
  ([[D-049]]). `crud/sqlfault/gate_test.go`'s sentinel-no/code-yes cell is that
  claim.
- **Nothing reads message text** ([[D-039]]). `Detail` and `Hint` are carried
  and never read; `errs.Detail.Value` ships empty for exactly that reason.
- **A fault's `Error()` is classification only** ([[D-047]]). The body that
  copied the outermost `err.Error()` into every response below 500 is gone —
  `port.FaultOf` reads no error text at all — but the rule stands, because the
  fault being the outermost error is what lets `errs.AsFault` find it rather than
  something hung underneath a `fmt.Errorf`.
- **The path is not this layer's** ([[D-043]]). `Violation.Path` is nil, not
  approximate: no hop was attempted. The column-to-model-field hop belongs to
  the decorator that has `crud.Meta`.
- **The catalog is per handle** ([[D-041]]). `FromCatalog` holds it on the
  classifier value the caller declared. No package-level catalog, no I/O in the
  lookup, no context in the signature.

## Traps

- **`crudsql.Open`, `From` and `Source` carry no classifier.** A write inside a
  joined ent, gorm, sqlx or sqlc transaction is a 409 **without a code** unless
  `crudsql.WithFaults` was passed, while the same write outside one carries the
  code. That inconsistency is the price of not deriving the dialect.
- **`crudsql.MySQL` against a MariaDB server** classifies the sentinel and not
  the code: MariaDB answers a failed CHECK with `4025`, which is not in MySQL's
  table. Use `crudsql.MariaDB`.
- **lib/pq's `Constraint` / `Table` / `Column` spellings are deliberately
  unread.** No capture exists for them.
  `crud/adapter/crudsql/conflict_test.go:TestTheExtractorReachesTheStructuredFieldsByShape`
  pins the refusal so it does not read as an oversight.
- **A fault carrying `KindValidation` still answers 409.** MySQL's `3819` and
  `1364` and PostgreSQL's `23502` and `23514` classify as `check`/`required`,
  whose kind is `KindValidation`, while gate 1 still attaches `crud.ErrConflict`.
  Nothing maps `Kind` yet, so exactly one mapping exists and the answer is 409.
  Phase 4 resolves it; no `Kind`→status arm may be added here.
- **The SQLite arm reads its own number, not the merged one.** `sqlerr.Err` has
  one `Native` field and `Extract` fills it from whichever spelling it found, so
  the no-state arm — the one with no engine behind it — would otherwise read a
  MySQL `Number` as a SQLite result code. That is reachable: the SQLSTATE is
  optional in MySQL's ERR packet and go-sql-driver/mysql leaves the `[5]byte`
  unset when the `#` marker is absent, and `1043`, `ER_HANDSHAKE_ERROR`, carries
  19 in its low byte — a refused handshake would be a 409 with the driver's
  sentence in the body. `crud/sqlfault/extract.go:sqliteNative` is the arm's own
  reader: a `Code` method, then integer `ExtendedCode` and `Code` fields, never
  `Number` ([[D-046]]).
- **Deliberately blank in phase 3**, so a later reader does not read the gap as
  an oversight: `Violation.Path`, `Fault.Op`, `Fault.Entity`, `Detail.Value`,
  `Detail.RefTable`, `Detail.RefColumns`. And the whole of `Source` — table,
  schema, constraint and columns — on MySQL, MariaDB and SQLite, plus
  `Source.Columns` on a PostgreSQL `22001` and a SQLite foreign key. In none of
  those is the catalog what is missing: every one of them carries no table and no
  constraint, so there is nothing to look one up by and a wired `WithColumns` is
  inert. The roadmap's phase-3 row said phase 6 would unblock them; it does not.

## Failure modes

| What goes wrong | Where it is caught | What the caller gets |
|---|---|---|
| a duplicate key, on any of the four engines | gate 1, then gate 2 | `crud.ErrConflict` + a fault carrying `unique` |
| a MySQL CHECK (`3819`/`HY000`) or missing default (`1364`) | gate 1's `HY000` arm, gate 2's `(state, number)` table | 409 + `check` / `required`. Both were unclassified 500s before phase 0 |
| the same CHECK on MariaDB (`4025`/`23000`) | gate 1's class arm; gate 2 only through `crudsql.MariaDB` | 409 always; the code only where the engine was declared |
| a class-23 number nobody provoked | gate 1 yes, gate 2 no | 409 with **no** code |
| a value too long, out of range, of the wrong type | gate 1 no, gate 2 yes | a fault carrying `too_long` / `out_of_range` / `invalid_format`, no sentinel — **500 until phase 4** |
| a deadlock, a lock timeout, a serialisation failure, `25P02` | gate 1 no, gate 2 yes | a fault carrying the retryable code, no sentinel — **500 until phase 4**; the 503 is [[D-040]]'s and phase 4's |
| an unclassifiable state — undefined table, access denied, a connection that never reached a server | neither gate | the driver error, untouched. 500 with an empty body |
| a driver whose vocabulary was never wired (`errs.NewCodes()`) | the kind lookup refuses | the sentinel, no fault. Never a fault claiming `KindInternal` |
| an engine nothing has a table for (`sqlfault.New("cockroach")`) | `sqlerr.Classify`'s dialect switch | the sentinel, no fault |
| a violation whose columns only the schema knows | the `Columns` SPI, when wired | `Source.Columns` filled; nil, not empty, when it is not — and nil for a key with an expression part, because `""` is not a column name |
| a duplicate key on MySQL, MariaDB or SQLite, where the driver names no constraint | the probe's merge rule, when a probe is wired | the driver's own violation, now carrying the `field` the probe resolved ([[FL-017]]) |

## Files

| File | Role |
|---|---|
| `crud/sqlfault/doc.go` | the two gates, the three things no arm may read, why the engine is declared, why the package is not on `Makefile:TIER0` ([[D-048]]) and why the name carries a prefix ([[D-035]]) |
| `crud/sqlfault/extract.go` | `Extract`, `Extractor`, `ExtractorFunc`, `walk`, `carried`, `engineError`, `sqliteNative` — by-shape extraction, the tree walk [[D-038]] owes, and the two places provenance has to be kept: which error the fields came from, and whose number the SQLite arm may read |
| `crud/sqlfault/gate.go` | `Integrity`, `mysqlIntegrityNumbers`, `sqliteConstraint` — gate 1, moved here from `crud/adapter/crudsql` so both adapters answer it identically |
| `crud/sqlfault/classify.go` | `Classifier`, `New`, `Option`, `WithCodes`, `WithExtractor`, `WithColumns`, `Classify`, `Engine`, `Wrap` — assembly, and the seam that guarantees the sentinel |
| `crud/sqlfault/catalog.go` | `Columns`, `FromCatalog`, `Classifier.fill` — the one-method schema SPI |
| `crud/adapter/crudsql/conflict.go` | `Executor.conflict` — one line, and the note that this package may not name a driver type |
| `crud/adapter/crudsql/crudsql.go` | `Executor.faults`, `Option`, `WithFaults`, `Postgres` / `MySQL` / `MariaDB` / `SQLite` (each with its engine literal), `Open` / `From` / `Source` (which carry none), and the propagation into `Tx` and `savepoint` |
| `crud/adapter/crudpgx/conflict.go` | `Executor.conflict` and `extract` — the typed reader for `*pgconn.PgError` |
| `crud/adapter/crudpgx/crudpgx.go` | `Executor.faults`, `Option`, `WithFaults`, `faults`, and the propagation into `rows` and `Tx` |
| `errs/sqlerr/doc.go` | gate 2's rules: why the dialect is part of the key, and why `mysql.go` and `mariadb.go` are two tables that agree on all but two rows |
| `errs/sqlerr/classify.go` | gate 2's entry point, the dialect switch |
| `errs/sqlerr/postgres.go`, `mysql.go`, `mariadb.go`, `sqlite.go` | the four tables, keyed three different ways |
| `errs/sqlerr/corpus.go`, `errs/sqlerr/testdata/corpus/*.json` | the captured evidence every arm rests on |
| `errs/fault.go` | `Fault`, `Detail`, `Error`, `Unwrap`, `AsFault` |
| `errs/build.go` | `Builder` and `Wrapping`, the only way a sentinel gets into a fault |
| `errs/violation.go` | `Violation`, `Origin`, `Source` |
| `errs/codes.go` | `Codes.KindOf` — the lookup that decides whether a fault may be built |
| `errs/spi.go` | `Classifier`, the interface an adapter holds |
| `crud/catalog/catalog.go` | `Catalog.Constraint`, what `FromCatalog` reads |
| `test/corpus/cases.go` | the probes behind the failure-mode table — `Session`, `Tx`, `RaceA`/`RaceB`, `Volatile` and `Engine.Restore`, the shapes that make the deferred constraint, `25P02` and the deadlock fire on every capture rather than one in ten |
| `test/corpus/capture.go` | the capture, now `sqlfault.Extract` plus the volatile substitution |
| `port/porthttp/errors.go` | `Status` and `StatusFor` — unedited by phase 3, and one line over `port.KindOf` since phase 5 |

## Tests that walk this flow

- `TestADriverErrorIsFoundThroughEveryWrappingShape` — `crud/sqlfault/extract_test.go` — five wrapping shapes × three driver shapes, sentinel-first among them.
- `TestTheWrappingsThatDefeatAPlainUnwrapLoop` / `TestAnErrorWithNothingInItExtractsToNothing` — `crud/sqlfault/extract_test.go` — why the tree walk exists, and its control.
- `TestTheMethodPathIsReachedOnAnErrorThatIsNotAStruct` — `crud/sqlfault/extract_test.go` — the regression a struct-only callback would cause, with the no-method twin as its control.
- `TestExtractionCarriesOnlyTheWhitelistedFields` / `TestTheNumberComesFromTheFieldThatMeansIt` — `crud/sqlfault/extract_test.go` — the whitelist as a filter, and which integer is the number.
- `TestAWrappersOwnFieldsAreNotTheDriversFields` — `crud/sqlfault/extract_test.go` — a caller's own struct with a `Detail` field around the driver error, with the bare fixture as the control and the catalog-filled `Detail.Columns` as the consequence.
- `TestTheTwoGatesAnswerDifferentQuestions` — `crud/sqlfault/gate_test.go` — the 2×2, with a counter per cell. This is the test [[D-046]] says phase 3 owes.
- `TestANumberIsOnlyReadOnceTheStateSaysWhichEngineItIs` — `crud/sqlfault/gate_test.go` — including the MySQL error the server sent with no SQLSTATE, whose number would otherwise be read as SQLite's, with the still-classifying SQLite violation and the still-recorded `Native` as its two controls.
- `TestAFaultIsBuiltOnlyWhenACodeAndItsKindAreKnown` — `crud/sqlfault/classify_test.go` — with an empty vocabulary as the control, and the `Detail.Driver` handle back to the raised error among what it pins.
- `TestAnAlreadyClassifiedErrorIsNotClassifiedTwice` / `TestASentinelIsAttachedWhateverTheClassifierReturned` — `crud/sqlfault/classify_test.go` — [[D-038]] at the seam.
- `TestNothingInExtractionOrClassificationReadsMessageDetailOrHint` — `crud/sqlfault/classify_test.go` — [[D-039]] at the extraction layer, which nothing tested before.
- `TestADriverViolationIsStateShapedAndHasNoPath` — `crud/sqlfault/classify_test.go` — `OriginState`, nil path, and the `Source` leg as the control on both.
- `TestAFaultCarriesNothingTheDriverSaidInItsErrorText` — `crud/sqlfault/classify_test.go` — [[D-047]] at the producer.
- `TestAClassifiedConflictIsItsOwnOutermostError` — `crud/sqlfault/classify_test.go` — the fault is what the adapter returns, which is what makes [[D-047]] reach a body at all.
- `TestAnUnknownDialectStillAnswersTheIntegrityGate` — `crud/sqlfault/classify_test.go` — what `Open`/`From`/`Source` degrade to.
- `TestTheColumnsSPIFillsWhatTheDriverDidNotName` / `TestACatalogNeverOverwritesTheColumnTheDriverNamed` / `TestALookupThatMissesLeavesTheColumnsUnknown` / `TestAViolationWithNoLookupKeyIsNotLookedUp` — `crud/sqlfault/catalog_test.go`. The last one carries the MySQL row that says `WithColumns` is inert on that engine, with the never-asked counter as its control.
- `TestAKeyWithAnExpressionPartIsNotAColumnList` / `TestAColumnListWithANamelessEntryIsTreatedAsAMiss` — `crud/sqlfault/catalog_test.go` — the adapter and the seam, each with a key of plain columns beside it as the control.
- `TestASQLSTATEIsStillFoundThroughAMultiErrorAndThroughAFault` — `crud/adapter/crudsql/conflict_test.go` — [[D-038]]'s owed regression at the gate, all three readers.
- `TestTheExtractorReachesTheStructuredFieldsByShape` — `crud/adapter/crudsql/conflict_test.go` — with the lib/pq spellings as the control on what is deliberately unread.
- `TestOnlyADeclaredEngineProducesACode` / `TestAMariaDBNumberIsOnlyReadByTheMariaDBConstructor` — `crud/adapter/crudsql/classify_test.go`.
- `TestOnlyIntegrityErrorsBecomeConflicts` — `crud/adapter/crudpgx/conflict_test.go` — the phase's own control case, extended: an unclassifiable state produces no fault on all three paths, and the `40001` beside it does.
- `TestADuplicateKeyIsAConflictWhicheverWayPgxReportsIt` — `crud/adapter/crudpgx/conflict_test.go` — including the `22001` leg, where a fault carries no sentinel.
- `TestTheTypedExtractorReadsThePgErrorFieldsThatExist` / `TestBothExtractorsAgreeOnOnePgError` — `crud/adapter/crudpgx/conflict_test.go` — the pgconn spellings, and the `Message` assertion in each that pins the typed reader as the one that ran: every other assertion in that file holds for `sqlfault.Extract` too.
- `TestANonPgErrorStillReachesTheByShapeReader` — `crud/adapter/crudpgx/conflict_test.go` — the fallback arm, with the `errors.As` check on the fixture as its control.
- `TestAClassifiedConflictsBodyCarriesNothingInternal` — `crud/http/crudnet/write_edge_test.go`, with identical twins in `crud/http/crudfiber/` and `crud/http/crudgin/` — [[D-047]] under live load, with the still-leaking unclassified conflict as its control.
- `TestEveryCorpusCaseReachesTheCallerAsTheFaultTheCorpusNames` — `test/integration/corpus_test.go` — four engines, every case, against the checked-in corpus, with per-engine counters of faults and of negatives.
- `TestEveryCorpusCaseClassifiesAsTheCorpusSays` — `test/integration/corpus_test.go` — unchanged beside it, and the first thing to go red if `Wrap` ever dropped the sentinel while building a fault.
- `TestAMariaDBCheckIsOnlyClassifiedWhenTheSourceSaysMariaDB` — `test/integration/corpus_test.go` — [[D-046]]'s named harm, bounded and visible.
- `TestACatalogFillsTheColumnsAUniqueViolationDoesNotName` — `test/integration/corpus_test.go` — live PostgreSQL, with the no-catalog and driver-named-column halves as controls.
- `TestIntegrityViolationsAreClassifiedByEveryAdapter` — `test/integration/dialect_edge_test.go` — every adapter, now asserting the code too, with the two ent targets asserted to keep the sentinel and carry none.
- `TestADeferredConstraintArrivesFromTheCommitAndNotTheStatement` — `test/integration/corpus_test.go` — the `Commit` path, through both PostgreSQL adapters.
- `TestANestedCommitOnAPoisonedTransactionCarriesItsCode` — `test/integration/edge_test.go` — the savepoint door, on `25P02` rather than on integrity, through both PostgreSQL adapters with a healthy nested commit as the control.
- `TestAUniqueIndexOnAnExpressionFillsNoColumns` — `test/integration/corpus_test.go` — live PostgreSQL, a unique index on `lower(email)`, with the plain key on the same table and the same catalog as the control.

## See also

[[FL-011]] [[FL-003]] [[FL-009]] [[FL-016]] [[FL-017]] [[UC-015]] [[UC-017]]
[[D-046]] [[D-038]] [[D-039]] [[D-047]] [[D-044]] [[D-043]] [[D-041]] [[D-042]] [[D-015]]
