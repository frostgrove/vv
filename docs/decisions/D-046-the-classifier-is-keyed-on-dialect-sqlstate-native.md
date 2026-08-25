# D-046 — The classifier is keyed on `(dialect, sqlstate, native)`

**Status:** accepted
**Invariant:** No arm of the error classifier is a test on the SQLSTATE class alone. The key is the dialect, the SQLSTATE and the engine's own number together, and a dialect that reports no SQLSTATE is classified without one.

Supersedes [[D-015]]'s sentence *"class 23 is integrity constraint violation …
and nothing else does, so the classification needs no per-driver table."* The
rest of D-015 stands.

## The decision

The integrity gate — `sqlfault.Integrity` since phase 3, `crud/adapter/crudsql`'s own
`isIntegrity` before it — has three arms, and the SQLSTATE selects between them
rather than deciding:

- **Class 23** — the portable half, and PostgreSQL's whole answer.
- **`HY000`** — MySQL saying it has nothing more specific. A short list of
  numbers is read, and only under that state.
- **No SQLSTATE at all** — SQLite. The engine's extended result code carries
  `SQLITE_CONSTRAINT` (19) in its low byte, and the low byte is the test.

A number is read only once the state has narrowed which engine is speaking. A
numeric field on some other driver's error can then never be mistaken for a
MySQL code or a SQLite one.

That costs something to keep, because `sqlerr.Err` has **one** number field and
the corpus is written against it: extraction merges `Number`, a `Code` method and
`ExtendedCode`/`Code` fields into it, and the no-state arm is the one arm with no
engine behind it. So the arm reads its own number rather than the merged one —
`crud/sqlfault/extract.go:sqliteNative`, SQLite's spellings only. "MySQL always
carries a state" is not a property of the driver: the SQLSTATE is optional in the
ERR packet, go-sql-driver/mysql leaves the `[5]byte` unset when the `#` marker is
absent, and `1043` carries 19 in its low byte.

Those three arms describe the **integrity gate** — the boolean that decides
`crud.ErrConflict` — and not the whole shape of a parser. `errs/sqlerr` classifies
the data classes and the retryable ones too, and its tables are per dialect
rather than three arms over all four: PostgreSQL on the whole state alone,
MySQL and MariaDB on the state and the number together, SQLite on the number
read as bytes and no state at all. Read narrowly, this decision would ship four
parsers with no retryable arm and no data arm.

## Why

D-015's claim was tested against PostgreSQL and generalised. Four engines were
then provoked and recorded, and the claim fails on three of them in three
different ways.

| | PostgreSQL 17.11 | MySQL 8.4.11 | MariaDB 11.4.12 | SQLite 3.53.3 |
|---|---|---|---|---|
| unique | `23505` | `23000`/1062 | `23000`/1062 | —/2067 |
| CHECK | `23514` | **`HY000`/3819** | **`23000`/4025** | —/275 |
| missing default | `23502` | `HY000`/1364 | `HY000`/1364 | —/1299 |
| bad type for column | `22P02` | **`HY000`/1366** | **`22007`/1366** | *accepted* |
| deadlock | `40P01` | `40001`/1213 | `40001`/1213 | —/5, the primary `SQLITE_BUSY` |
| serialisation failure | `40001` | `40001`/1213 — the same key as a deadlock | same | —/5, the same again |
| tx aborted | `25P02` | *statement-level rollback* | same | same |

**MySQL puts a CHECK violation outside class 23.** `3819`/`HY000`. A client got
a bare 500 where [[FL-011]] promises 409, and the same for `1364`.

**MariaDB puts the same violation inside class 23**, as `4025`/`23000`. So the
identical constraint on two engines that share a driver, a dialect and a wire
protocol is reached through two different arms. A number list alone would be
wrong about MariaDB; a class test alone is wrong about MySQL.

**SQLite has no SQLSTATE and never will**, so for a quarter of the supported
engines the gate D-015 described is simply absent. Every SQLite constraint
violation — unique, primary key, foreign key, NOT NULL, CHECK, all of them — was
an unclassified 500 in the shipped library until this decision. Seven classes on
a documented dialect, for as long as SQLite has been supported.

**And the same number means different things.** `1366` is `HY000` on MySQL and
`22007` on MariaDB. A parser keyed on the number alone would agree with itself
and be describing two different classifications.

**Why the low byte for SQLite rather than a list of subcodes.** Every
`SQLITE_CONSTRAINT_*` code is `19 | (n<<8)`, so the low byte is the question and
the subcodes need no enumeration — a subcode added by a future SQLite is covered
the day it ships. An extended code is not interchangeable with a primary one:
`SQLITE_BUSY` is 5 and busy-snapshot is 517, which is why the *whole* code is
never compared.

**Why nothing was caught.** `TestIntegrityViolationsAreClassifiedByEveryAdapter`
walks `egTargets()`, and SQLite is not on that list — the dialect was added with
a conformance suite that exercises reads and writes, and constraint violations
are not part of conformance. The lesson is the reason the corpus exists: a
matrix written from a specification is a guess, and the guess was wrong in every
direction it could be.

## What it forbids

- Do not gate on `strings.HasPrefix(state, "23")` alone. That is the sentence
  this supersedes.
- Do not read an engine's number before the state has said which engine it is.
  `Number` on MySQL, `Code`/`ExtendedCode` on SQLite and `Code` on pgconn are
  three different things, and pgconn's is a string holding the SQLSTATE.
- Do not compare a SQLite extended result code whole. Compare the low byte, or
  a busy-snapshot becomes a constraint violation the day the codes shift.
- Do not add a number to any list from documentation. Provoke it, capture it in
  the corpus, and let the entry be the citation.
- Do not classify a class the caller cannot act on. Class 22 stays refused **by
  the conflict classifier** and class 40 belongs to [[D-040]]. `errs/sqlerr`
  does classify class 22 — `too_long`, `out_of_range`, `invalid_format`, which
  §2 puts at 422 and `errs/codes.go` names — and that is not the same gate.
- Do not decide a foreign-key direction from anything the key does not give you.
  On PostgreSQL both directions are `23503` with the same constraint and the
  same fields, and on SQLite both are ext `787`; the only thing left that
  separates them there is the localised `Detail`, and reading it is what
  [[D-039]] forbids. So the corpus records the collapse rather than papering
  over it — `restrict` answers `foreign_key` on PostgreSQL and SQLite — and the
  direction there is owed to whichever phase sets `Fault.Op`. MySQL and MariaDB
  do separate the two in the key, 1451 against 1452, so `{"23000", 1451}`
  answers `restrict` on both and `Want` stays `restrict` in their corpora. That
  row is not what this forbids.
- Do not derive the dialect from `crud.Dialect.Name()`. It answers `"mysql"` for
  MariaDB (`crud/dialect.go:77`), so MariaDB's `4025` would go through MySQL's
  table and come back unclassified. The dialect a parser is given is a fourth
  string.

  **Phase 3 closed the run-time gap by refusing to have one.** The engine is
  *declared* and derived from nothing: `crudsql.Postgres`, `crudsql.MySQL`,
  `crudsql.MariaDB` and `crudsql.SQLite` each write their engine as a literal,
  and `crudpgx` writes `"postgres"`. A type switch over `crud.Postgres` /
  `crud.MySQL` / `crud.SQLite` is *the same derivation in different syntax* — it
  answers "mysql" for a MariaDB server too — and is forbidden by this line as
  much as `Name()` is. `crudsql.Open`, `From` and `Source` take a dialect rather
  than an engine, so they name none and get no classifier: the only reachable
  failure is then *no* code, never a wrong one, which is what refusing rather
  than guessing means here. A consumer who wants it measured has exactly one
  measurement in the tree, `catalog.Catalog.Dialect()`, and may wire it through
  `crudsql.WithFaults`.

## What is deliberately not classified

A class-23 state whose number is unlisted — `1216` and `1217` are two — and a
SQLite low-byte-19 code whose high byte no probe has produced. Both are refused
rather than guessed, because adding a number from documentation is what the
forbid above rules out.

That makes the parsers **stricter than the integrity gate**, which calls any
class 23 and any low-byte-19 code a conflict. Phase 3 kept the class test for the
`crud.ErrConflict` sentinel rather than narrowing it onto the parsers' tables, so
a duplicate key on an engine nobody provoked is still a 409 — with no code. The
two gates are held apart by `TestTheTwoGatesAnswerDifferentQuestions` in
`crud/sqlfault/gate_test.go`: a 2×2 with at least one case per cell and a counter per
cell asserted non-zero, so if either gate widens or narrows into the other a cell
empties and the table cannot stay green with three rows.

## Where it lives

- `crud/sqlfault/gate.go:Integrity` — the three arms, moved out of `crud/adapter/crudsql`
  at phase 3 so both adapters answer the question with the same code.
- `crud/sqlfault/gate.go:mysqlIntegrityNumbers` — the MySQL list, two entries, each
  provoked.
- `crud/sqlfault/gate.go:sqliteConstraint` — the SQLite arm's low byte.
- `crud/sqlfault/extract.go:Extract` — where a state, a number and the structured
  fields are reached by shape, and where the kind check keeps pgconn's string
  `Code` from being read as a number.
- `crud/sqlfault/extract.go:sqliteNative` — the SQLite arm's own number, kept apart
  from the merged `Native`. `sqlerr.Err` has one number field and the corpus is
  written against it, so the provenance a three-reader extraction used to carry
  lives here instead: a `Code` method, then integer `ExtendedCode` and `Code`
  fields, and never `Number`. Without it the no-state arm — the one arm with no
  engine behind it — reads a MySQL number as a SQLite result code, and MySQL's
  SQLSTATE is optional in the ERR packet rather than always present.
- `crud/sqlfault/classify.go:Classifier` — the engine string, declared by the caller,
  and the refusal to build a fault without both a code and its kind.
- `crud/adapter/crudsql/crudsql.go:Postgres` / `:MySQL` / `:MariaDB` / `:SQLite` —
  the four declarations; `:Open`, `:From` and `:Source` — the three that decline
  to make one.
- `errs/sqlerr/classify.go:Classify` — the dialect switch, and what no arm reads.
- `errs/sqlerr/postgres.go` — the whole state, never the class; the native
  number is never read.
- `errs/sqlerr/mysql.go` / `errs/sqlerr/mariadb.go` — two tables on the same
  `(state, number)` key, differing at exactly two rows.
- `errs/sqlerr/sqlite.go` — the low byte, then the high byte, and the SQLSTATE
  guard.
- `crud/adapter/crudpgx/conflict.go:extract` — the typed reader. It holds no class
  test since phase 3: `strings.HasPrefix(pg.SQLState(), "23")` was the sentence
  this decision supersedes, and it was still shipped in the one file named here
  as "the typed one".
- `errs/sqlerr/corpus.go` — the captured evidence and what it is compared on.
- `errs/sqlerr/testdata/corpus/` — four files, one per engine.
- `test/corpus/` — what provokes them.

## Proven by

- `TestTheTwoGatesAnswerDifferentQuestions` in `crud/sqlfault/gate_test.go` — the
  test this decision asked phase 3 for. Four cells, a counter each: both gates
  yes, sentinel-only (`{23000, 1216}` and an unproduced SQLite subcode),
  code-only (`SQLITE_BUSY`, `40001`, `22001` — the dangerous direction), neither.
- `TestAMariaDBCheckIsOnlyClassifiedWhenTheSourceSaysMariaDB` in
  `test/integration/corpus_test.go` — live MariaDB, `4025` through
  `crudsql.MariaDB` carrying `check`, and the same violation on the same server
  through `crudsql.MySQL` still a 409 and carrying no code. The named harm, made
  visible and bounded.
- `TestOnlyADeclaredEngineProducesACode` and
  `TestAMariaDBNumberIsOnlyReadByTheMariaDBConstructor` in
  `crud/adapter/crudsql/classify_test.go` — every constructor, and what each one
  degrades to.
- `TestANumberIsOnlyReadOnceTheStateSaysWhichEngineItIs` in
  `crud/sqlfault/gate_test.go` — the three forbids above, as three assertions, plus
  the fourth shape the merged `Native` field made reachable: `1043` with no
  SQLSTATE, which is `ER_HANDSHAKE_ERROR` and carries 19 in its low byte. Two
  controls beside it, because the assertion has two ways to pass vacuously — the
  SQLite arm still classifies `2067`, and `Extract` still records `1043` in
  `Native` for the corpus.
- `TestANumberIsOnlyTrustedUnderHY000` in `crud/adapter/crudsql/conflict_test.go` —
  the same pair one layer up, through `Executor.conflict`.
- `TestSQLiteConstraintViolationsBecomeConflicts` and
  `TestAnOrdinarySQLiteErrorIsStillNotAConflict` in
  `crud/adapter/crudsql/conflict_test.go` — the SQLite arm and its control, which
  keeps `SQLITE_BUSY` out.
- `TestASQLiteCodeIsOnlyTrustedWithoutASQLSTATE` in the same file — pgconn's
  string `Code` is not read as a number.
- `TestMySQLIntegrityErrorsOutsideClass23BecomeConflicts`,
  `TestAnOrdinaryHY000IsStillNotAConflict` and
  `TestANumberIsOnlyTrustedUnderHY000` — the MySQL arm and its two controls.
- `TestEveryCorpusCaseClassifiesAsTheCorpusSays` in
  `test/integration/corpus_test.go` — all four engines live, every case, both
  directions. Removing `3819` fails MySQL's `check` arm and **not** MariaDB's,
  which is the whole decision in one observation.
- `TestARefusalFromOneEngineDoesNotClassifyThroughAnothersParser` in
  `errs/sqlerr/dialect_test.go` — the cross-dialect grid, with the diagonal in
  the same loop. It is what makes the dialect part of the key non-free:
  PostgreSQL's native number is zero on all twenty entries, so nothing in its
  own corpus exercises the number at all. Not every cell refuses, and the test
  says why rather than claiming otherwise — `22001`, `22003` and `40001` are
  genuinely portable and are asserted to agree.
- `TestMySQLAndMariaDBDoNotAnswerForEachOtherWhereTheyDiffer` in the same file —
  the only thing in the tree that forces `mariadb.go` to exist. Merge the two
  tables and everything else stays green.
- `TestASQLiteResultCodeIsReadAsBytesAndNotWhole` and
  `TestASQLiteCodeIsOnlyReadWhereThereIsNoSQLSTATE` in the same file — the low
  byte, busy-snapshot `517` asserted as a consequence of the rule rather than as
  a table row, and the state guard that keeps a pgconn `Code` out.

## See also

[[D-015]] [[D-039]] [[D-040]] [[D-019]] [[FL-011]]
