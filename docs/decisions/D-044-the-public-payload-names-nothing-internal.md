# D-044 — The public payload names nothing internal

**Status:** accepted
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

- `http/crudhttp/errors.go:Body` — the existing 500 silence, which this extends
  to every status.
- `errs/sqlerr/testdata/corpus/*.json` — what the driver hands over, and
  therefore what must not pass through. The corpus is an internal fixture: it
  records `Detail`, usernames and constraint names on purpose, and nothing in it
  is a response.

## Proven by (owed)

- Phase 4 owes the render test: a body built from every corpus entry contains no
  substring of the entry's constraint name, table name, column name, SQLSTATE or
  native number. Asserting on one hand-written violation would pass for a
  renderer that leaks a different field.
- Phase 4 also owes the extension of `TestA500NeverEchoesTheInternalError` to
  `Detail` and `Params`.

## See also

[[D-015]] [[D-039]] [[D-043]] [[D-008]] [[UC-015]]
