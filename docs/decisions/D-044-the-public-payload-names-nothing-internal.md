# D-044 — The public payload names nothing internal

**Status:** accepted — the types carry it now: `Violation` and `Fault` marshal and print the public projection only; the rendered body comes into force with phase 4 (`ROADMAP-errors.md` §14)
**Invariant:** A response body carries no constraint name, table name, column name, SQLSTATE, native error number, CHECK expression, or any message parameter derived from one. What reaches a client is a code, a path into the request it sent, and a message written for a human.

## The decision

The error subsystem knows a great deal that a client must never see. The
envelope is a deliberate narrowing: `{code, field, message}` and nothing that
names the schema.

The rule covers the derived cases too, because those are the ones that leak by
accident: a message template interpolating `{constraint}`, a `Params` map
carrying a column name so a translation can use it, or a CHECK code generated
from the expression source text.

## Why

**Because the names are the schema.** `users_tenant_id_email_key` tells an
unauthenticated caller the table, two columns and the tenancy model. A CHECK
expression is worse: SQLite reports an *unnamed* check as its expression source
text, so `balance >= minimum_reserve` would arrive verbatim in a response body.

**Because [[D-015]] already made this argument for 500s and stopped there.**
`DefaultErrorHandler` fills `Message` only below 500, precisely because the
underlying text can be a SQL string, a column list or a connection fragment. A
409 built from a constraint name leaks the same class of thing through a status
that *does* carry a body — so the existing rule closes the loud door and leaves
the quiet one open.

**Because the corpus makes the exposure concrete.** These are captured, not
imagined:

- `Key (slug)=(anchor) already exists.` — PostgreSQL's `Detail`, carrying both
  the column and the submitted value.
- ``Cannot add or update a child row: a foreign key constraint fails (`vv`.`cp_child`, CONSTRAINT `cp_fk` FOREIGN KEY (`parent`) REFERENCES `cp_parent` (`id`))`` — MySQL naming the database, both tables and both columns.
- `Access denied for user 'vv'@'%' to database 'mysql'` — a username.

Every one of these is one `fmt.Sprintf` away from a response body.

**Why the value is refused too, and not only the name.** The offending value is
the caller's own input, so echoing it looks harmless — until the payload was a
bulk write and the conflicting row belongs to another tenant, at which point the
409 body confirms a row the caller cannot read. That is [[D-008]]'s argument
about 404 preceding 403, arriving through the error envelope instead of the
gate. Echoing is therefore opt-in, per deployment, and off.

**Why the custom marshal is the enforcement and not a convenience.** Measured on
go1.26.5: the default `json.Marshal` of a `Fault` does **not** fail on the
`Detail.Driver error` field. It succeeds, and emits the constraint name, the
table, the SQLSTATE and every exported field the driver error has — and
`*pgconn.PgError` exports `ConstraintName`, `TableName` and `Code`, so that is
the real shape and not a hypothetical one. The leak is silent, which is why
`Violation` and `Fault` carry their own `MarshalJSON`. The receiver has to be
the **value** one: with a pointer receiver, marshalling a violation held in a
struct field, in a map, or on its own bypasses the method entirely — three of
the five ways one is ever marshalled.

**Why a path is allowed when a column is not.** A path names something the
client sent — `["user","email"]` — and the client already has it. A column names
something the client did not send and cannot see. The translation between them
is [[D-043]]'s, and it is the one place the two vocabularies touch.

## What it forbids

- Do not put a constraint, index, table, column, schema or database name in a
  rendered body.
- Do not put a SQLSTATE or a native error number in one. They identify the
  engine and its version.
- Do not derive a public code from a CHECK expression's source text.
- Do not add a `Params` entry holding an internal name so a message template can
  interpolate it. A translated message that says *"balance must exceed
  minimum_reserve"* has leaked the column through the one channel that looked
  like presentation.
- Do not echo the offending value by default. Behind an explicit per-deployment
  option, and off.
- Do not relax this for 4xx on the grounds that only 500 is dangerous. The
  bodies below 500 are the ones clients actually read.

## Where it lives

- `errs/violation.go:Violation.MarshalJSON` — the public projection: `field`,
  `error_code`, `message`, and nothing else. Not `Params`, which is where an
  internal name would arrive wearing presentation.
- `errs/fault.go:Fault.MarshalJSON` — the same for a fault: kind, code,
  violations, partial. `Detail` never leaves the process.
- `errs/fault.go:Fault.Error` — the other channel a body can reach, governed by
  [[D-047]].
- `http/crudhttp/errors.go:Body` — the existing 500 silence, which this extends
  to every status.
- `errs/sqlerr/testdata/corpus/*.json` — what the driver hands over, and
  therefore what must not pass through. The corpus is an internal fixture: it
  records `Detail`, usernames and constraint names on purpose, and nothing in it
  is a response.
- The coarsening, which is not a function anywhere. The corpus speaks four
  classes the public vocabulary does not — `primary_key`, `not_null`,
  `missing_default`, `bad_type` — and they are absent from `errs/code.go` on
  purpose, so a parser has nothing finer to reach for. `errs/codes.go` states
  it and `errs/sqlerr/sqlite.go` is where it actually bites: ext `1555` is a
  duplicate *primary key* and answers `unique`, exactly as ext `2067` does. The
  same on MySQL and MariaDB, where `HY000`/`1364` and `23000`/`1048` are two
  keys and one code. A code naming which index was hit is a hair from naming the
  constraint.
- `errs/sqlerr/postgres.go` — `Detail` is not read even though it holds the
  offending value. The field it would fill is `errs.Detail.Value` in
  `errs/fault.go`; `errs.Source` is `{Table, Schema, Columns, Constraint}` and
  has no `Value` at all. **Phase 3 owns best-effort enrichment and deliberately
  did not do it**: the only source is PostgreSQL's localised `Detail`
  ([[D-039]]), echoing a value is off by deployment default anyway, and every
  field filled is a field a future renderer could leak.
- `sqlfault/catalog.go:Columns` — the one-method schema SPI that fills in the
  columns a driver did not name. One method, answering column names only: a
  third party can supply a schema without importing `catalog`, and cannot hand
  back a predicate, a constraint definition or a DDL text. Having a constraint
  name in hand at last invents no finer code from it — the coarsening above
  still holds.

## Proven by

- `TestNoParserAnswersWithTheCorpusFinerVocabulary` in
  `errs/sqlerr/classify_test.go` — no code any parser produces on any engine is
  one of the four fine words. Its control is that the two key pairs the
  coarsening actually acts on are asserted distinct in the corpus first — SQLite's
  `1555` against `2067`, and MySQL's and MariaDB's `HY000`/`1364` against
  `23000`/`1048` — because on the other engines the collapse falls out of a
  parser that never heard of it, and without pinning those two rows the test
  passes for a corpus that never made the distinction.
- `TestAMarshalledFaultNamesNothingInternal` in `errs/marshal_test.go` — a fault
  built from a real corpus entry on each of the four engines, marshalled three
  ways, carries no substring of the entry's constraint, table, schema, detail,
  SQLSTATE, native number or driver text. Its control is a shim struct of the
  same shape with no `MarshalJSON`, asserted to leak every one of those strings
  through the default path; without it the test would pass for a fixture that
  was empty, and nothing would record that the default marshal leaks.
- `TestAViolationMarshalsOnlyFieldCodeAndMessage` in the same file — all five
  marshal shapes are clean, the key set is exactly the populated subset of the
  three public keys, and fifty marshals of a violation carrying an eight-entry
  `Params` map are byte-identical. The fixture populates `Origin`, `Params`,
  `Source` and `Approximate` first and asserts them present on the Go value, so
  finding them absent on the wire means something.
- `TestAFaultWithNoViolationsRendersAnEmptyArray` in the same file — a fault with
  no violations renders `"violations":[]` in all three marshal shapes. Its
  control marshals a nil `[]Violation` through the default path and asserts it
  renders as `null`, so the custom marshal is what the assertion measures; every
  other marshal test here builds a fault that carries a violation.

- `TestAPrintedFaultNamesNothingInternal` and
  `TestAPrintedViolationNamesNothingInternal` in the same file — the third
  projection, and the one a log line actually reaches: `%v` and `%+v` on a
  value, in a struct field, in a map entry, and on the whole violation slice.
  The pointer goes through `Error()`; every other shape falls through to fmt's
  struct printer, which is why `Fault.String` and `Violation.String` have value
  receivers rather than pointer ones. The control is a shim struct of the same
  shape with no `String` method, asserted to emit every secret; without it both
  methods could be deleted with the whole root module green, which is how they
  were found. The fault's secret list is what the struct printer can reach — the
  driver error's own `Detail` field is excluded, because fmt renders an
  error-typed field through `Error()` and asserting its absence would be free.

## Proven by (owed)

- Phase 4 owes the render test: a body built from every corpus entry contains no
  substring of the entry's constraint name, table name, column name, SQLSTATE or
  native number. Asserting on one hand-written violation would pass for a
  renderer that leaks a different field.
- Phase 4 also owes the extension of `TestA500NeverEchoesTheInternalError` to
  `Detail` and `Params`.

## See also

[[D-015]] [[D-039]] [[D-043]] [[D-047]] [[D-008]] [[UC-015]]
