# D-046 — The classifier is keyed on `(dialect, sqlstate, native)`

**Status:** accepted
**Invariant:** No arm of the error classifier is a test on the SQLSTATE class alone. The key is the dialect, the SQLSTATE and the engine's own number together, and a dialect that reports no SQLSTATE is classified without one.

Supersedes [[D-015]]'s sentence *"class 23 is integrity constraint violation …
and nothing else does, so the classification needs no per-driver table."* The
rest of D-015 stands.

## The decision

`isIntegrity` has three arms, and the SQLSTATE selects between them rather than
deciding:

- **Class 23** — the portable half, and PostgreSQL's whole answer.
- **`HY000`** — MySQL saying it has nothing more specific. A short list of
  numbers is read, and only under that state.
- **No SQLSTATE at all** — SQLite. The engine's extended result code carries
  `SQLITE_CONSTRAINT` (19) in its low byte, and the low byte is the test.

A number is read only once the state has narrowed which engine is speaking. A
numeric field on some other driver's error can then never be mistaken for a
MySQL code or a SQLite one.

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
- Do not classify a class the caller cannot act on. Class 22 stays refused and
  class 40 belongs to [[D-040]].

## Where it lives

- `adapter/crudsql/conflict.go:isIntegrity` — the three arms.
- `adapter/crudsql/conflict.go:mysqlIntegrityNumbers` — the MySQL list, two
  entries, each provoked.
- `adapter/crudsql/conflict.go:sqliteResultCode` /
  `adapter/crudsql/conflict.go:sqliteConstraint` — the SQLite arm.
- `adapter/crudpgx/conflict.go:conflict` — the typed one.
- `errs/sqlerr/corpus.go` — the captured evidence and what it is compared on.
- `errs/sqlerr/testdata/corpus/` — four files, one per engine.
- `test/corpus/` — what provokes them.

## Proven by

- `TestSQLiteConstraintViolationsBecomeConflicts` and
  `TestAnOrdinarySQLiteErrorIsStillNotAConflict` in
  `adapter/crudsql/conflict_test.go` — the SQLite arm and its control, which
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

## See also

[[D-015]] [[D-039]] [[D-040]] [[D-019]] [[FL-011]]
