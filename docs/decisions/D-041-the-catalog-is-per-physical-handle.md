# D-041 — The catalog is per physical handle, and its absence is a start-up failure

**Status:** accepted — in force from phase 6 (`ROADMAP-errors.md` §14)
**Invariant:** Schema introspection is keyed on the identity a source reports for its database handle, never on a DSN and never on a package-level variable. A repository declared with a feature that needs a catalog refuses to start when the catalog cannot be loaded.

## The decision

`Load(ctx, src)` reads the schema once, at declaration time, and the result is
keyed on what `crud.Identified.DataSource()` reports — the `*sql.DB`, the
`*pgxpool.Pool`, whatever the adapter holds. Lookups afterwards do no I/O and
take no `context`.

A source that cannot name its database gets no catalog and says so **at
declaration**, not on its first failed write.

## Why

**Because one process holds several databases** ([[UC-012]]), and two of them
can disagree about what `users_email_key` means. A package-level catalog is the
same bug [[D-032]] and [[D-027]] already exist for, wearing different clothes: a
global that is right in every single-database test and wrong in the deployment
that matters.

**Why the handle and not the DSN.** Two handles opened from the same DSN with
different `search_path` are different catalogs, and a string key merges them
silently. The seam already states the rule this reuses, in `crud/executor.go`:
*"Two sources that answer with the same handle are the same database, and that
is the only question `WithExecutorFor` needs answered."*

**Why the test is `keyOf(src) != nil` and not `src.(Identified)`.**
`crud.ReadWrite` *is* `Identified` and answers nil when its primary is not —
`keyOf` says why: *"A wrapper that forwards an identity it does not have answers
nil; that is 'I cannot say', not 'my identity is nil'."* Testing for the
interface makes every such source collide into one catalog entry, which is
exactly the silent merge the handle key exists to prevent.

**Why not a `map[any]`.** `sameDataSource` avoids one deliberately: *"a
datasource handle is a pointer in practice, but nothing in the contract says it
must be"*, and an uncomparable map key panics at run time. Catalogs live in a
slice compared with that function.

**Why `Constraint` takes the table.** An index name is unique per table on MySQL,
not per schema — every InnoDB table's primary index is called `PRIMARY`. The
corpus records it: MariaDB reports a duplicate primary key as `for key
'PRIMARY'`, with nothing naming the table. A bare name is ambiguous across the
database.

**Why lookups take no context.** A signature that accepts one is a lazy loader,
and a lazy loader cannot fail at start-up — which is the whole of [[D-021]]'s
rule. `Load` is the thing that can fail.

**Why a missing catalog is fatal rather than a downgrade.** Insufficient
privileges, a proxy blocking `information_schema`, an unknown dialect. Degrading
quietly to "no violations found" means the feature is off in production and the
only symptom is that it never reports anything — indistinguishable from a
database with no constraint problems.

**Why it must exist at all, given [[D-039]].** Because three of the four engines
supply no structured fields whatsoever. The corpus shows `mysql.MySQLError` and
`sqlite.Error` carrying no `ColumnName`, no `TableName`, no `ConstraintName` —
only PostgreSQL populates them. Without a catalog the column is unknowable on
three engines out of four, and the alternative is parsing the message, which
D-039 forbids.

## What it forbids

- Do not hold a catalog in a package-level variable, however convenient.
- Do not key it on a DSN, a database name or any string.
- Do not test `src.(Identified)` in place of `keyOf(src) != nil`.
- Do not put catalogs in a `map[any]`.
- Do not make a lookup do I/O, and do not give one a `context` — both turn a
  start-up failure into a request failure.
- Do not resolve a bare table name lazily per connection. The catalog resolves
  it once, on the connection it loaded from, and records the resolved schema.
- Do not let it grow into a migration tool, a DDL model, or a Go-side
  re-implementation of the database's rules. Two implementations of one
  constraint disagree eventually, and the one in the database is the one that is
  right.

## Where it lives

Nothing yet. `catalog/TODO.md` holds the place; phase 6 creates it.

- `crud/executor.go:keyOf` / `crud/executor.go:sameDataSource` — the identity
  rules this reuses rather than reinvents.
- `crud/executor.go:Identified` — the only identity the seam offers.

## Proven by (owed)

- Phase 6 owes: two `crud.ReadWrite` sources whose primaries differ do not share
  a catalog, and an uncomparable handle is refused at declaration rather than
  panicking.
- And the twin that stops it passing vacuously: a partial index is skipped
  **and** a non-partial index of the same shape is not. Without the second, a
  catalog that skips everything passes.
- And: an unknown constraint name does not re-introspect in a loop — the
  negative cache with backoff.

## See also

[[D-021]] [[D-027]] [[D-032]] [[D-039]] [[D-042]] [[UC-012]]
