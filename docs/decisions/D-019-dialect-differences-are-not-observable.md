# D-019 — A dialect difference must not be observable through the API

**Status:** accepted
**Invariant:** The same repository call against PostgreSQL, MySQL and SQLite must return the same result, the same error and the same refreshed model, except where a difference is named in this file.

## The decision

Every syntax difference lives behind `crud.Dialect`, and where a dialect cannot
do something, the repository compensates rather than exposing the gap. The API
does not grow a "which engine am I on" branch, and a caller does not either.

`crud.OffsetLimiter` is the pattern for a capability a dialect may or may not
need: an optional interface, not a name check. `if d.Name() == "mysql"` is what
that replaced — SQLite needed the same treatment and was handed a bare
`OFFSET 5`, which it answers with `near "5": syntax error`, reachable straight
from the wire as `{"unpaged":true,"offset":5}`.

## Why

The alternative is a caller who has to know. That is fine for one application on
one engine and it is unmanageable for a library: an integration test that passes
on PostgreSQL and fails on MySQL is the cheapest bug to find; one that *passes*
on both while returning different data is the most expensive.

The compensations, and what each one costs:

| difference | how it is hidden | cost |
| --- | --- | --- |
| MySQL has no `RETURNING` | `Save` and `Update` re-read the row by primary key | one extra round trip on MySQL |
| MySQL's `RowsAffected` cannot distinguish "no such row" from "nothing to do" | the same re-read answers both ([[D-010]]) | included above |
| upsert syntax | `Dialect.Upsert` — `ON CONFLICT (pk) DO UPDATE SET c = EXCLUDED.c` vs `ON DUPLICATE KEY UPDATE c = VALUES(c)` or the 8.0.19 `AS new` row alias | `MySQL{RowAlias: true}` is a caller choice, because MariaDB and MySQL 5.7 do not have it |
| an upsert with nothing to update | PostgreSQL `DO NOTHING`; MySQL a no-op `pk = pk` assignment, because the grammar demands an assignment | MySQL then reports 0 rows, which is why the re-read is unconditional |
| `OFFSET` without `LIMIT` | `OffsetLimiter.LimitAll` — MySQL `LIMIT 18446744073709551615`, SQLite `LIMIT -1`, PostgreSQL nothing | none |
| identifier quoting | `"x"` vs `` `x` ``, each doubling its own quote character | none |
| bind markers | `$n` vs `?` | none |
| `ILIKE` | `crud.LikeIgnoreCase` renders `LOWER(col) LIKE LOWER(?)`, which works on both | a functional index is needed for it to use one |
| a generated key on insert | `RETURNING` where available, `LastInsertID` on MySQL | none |
| `count(DISTINCT a, b)` is MySQL-only | a derived table, which MySQL insists on being able to name (`AS vv_distinct`) | none |

## Where the difference *is* observable

Ten places. They are documented rather than hidden because hiding them would
mean emulating an engine, which is worse than saying so. Differences 5 through 8
were measured while building the error corpus, and were observable before anyone
wrote them down; difference 9 was measured while building the catalog, and
difference 10 arrived with the classifier that reads it.

1. **`Order.WithNullsLast` / `WithNullsFirst` are PostgreSQL-only.** `NULLS LAST`
   is not in MySQL's grammar. MySQL keeps its own rule — NULLs first ascending,
   NULLs last descending — and the hint is dropped. Emulating it would need
   `ORDER BY col IS NULL, col`, which changes the sort into an expression sort
   and defeats an index; the caller who needs it can write that themselves. This
   is the one named in both usage guides.
2. **`UpdateAll`'s row count.** PostgreSQL reports rows *matched*, MySQL rows
   *changed*. Writing a value a row already holds is counted by one and not by
   the other. This is the driver's number and there is nothing to normalise it
   against short of a second query. Stated on `crud.Repo.UpdateAll`.
3. **`crud.ForUpdate()` renders nothing on SQLite.** SQLite has no row locks; a
   write transaction locks the database. The serialisation the caller wanted
   comes from the transaction instead.
4. **`LIKE` case sensitivity follows the collation.** MySQL's default collation is
   case-insensitive and PostgreSQL's is not, so `crud.Contains` gives different
   answers. That is the column's business, not the library's — and it is exactly
   why `LikeIgnoreCase` exists as the portable spelling.
5. **An upsert swallows a different set of conflicts per engine, and this is the
   worst one on the list.** `Save` is the upsert path ([[D-011]]). PostgreSQL
   emits `ON CONFLICT (pk) DO UPDATE`, which swallows the primary key only;
   MySQL and MariaDB emit `ON DUPLICATE KEY UPDATE`, which fires on **any**
   unique key. Measured with a table keyed on `id` and unique on `email`, saving
   `{id: 3, email: "a@x.io"}` where `a@x.io` belongs to row 1:

   | | PostgreSQL 17 | MySQL 8.4 |
   |---|---|---|
   | result | 409, nothing written | success |
   | table after | rows 1 and 2, untouched | **row 1 overwritten**; row 3 never created |

   So the same call is a refusal on one engine and a silent write to a row the
   caller never named on the other. There is nothing to normalise against: a
   per-unique-key conflict target is not in MySQL's grammar, and emulating
   PostgreSQL would mean a read before every save. An application that upserts
   on a table with a second unique key has to know which engine it is on.
6. **A declared width, a declared range and a declared type are not enforced on
   SQLite.** `VARCHAR(8)` stores a 27-character value; a column that MySQL and
   PostgreSQL refuse a 99999 into accepts it; `'abc'` into an integer column is
   kept as text. So the same payload is 422 on the two servers and 200 on SQLite,
   and the row afterwards holds something the schema says it cannot. This is
   SQLite's documented type affinity and cannot be compensated for without a
   Go-side length check, which [[D-042]] refuses for its own reasons.

   The catalog carries the consequence: a SQLite column declared `VARCHAR(255)`
   reports that text as its declared type and **never** as a maximum length,
   because a maximum length is a claim that the engine enforces one. On the other
   three engines the same column reports 255.
7. **A duplicate-key error names its index differently on MySQL and MariaDB.**
   MySQL prefixes the table (`for key 'users.email'`); MariaDB does not (`for
   key 'email'`). Both are reached through `crud.MySQL`. Nothing in the library
   reads it. It is still visible in a 409 body, but the condition is narrower
   since phase 3: only where the failure was **not** classified — an unlisted
   class-23 number, or a source built with `crudsql.Open`/`From`/`Source`, which
   name no engine. Where it was classified the body carries the classification
   and no driver text at all ([[D-047]], [[UC-015]] guarantee 11).
8. **A failed CHECK is class 23 on MariaDB and not on MySQL.** `4025`/`23000`
   against `3819`/`HY000`, on two engines that share a driver, a dialect and a
   wire protocol. It is not observable through the API because [[D-046]]'s
   classifier covers both, and it is on this list because the version that
   covered only one of them shipped.
9. **Which unique keys can be told apart, and which kinds of key exist at all,
   differs per engine — and the catalog says so rather than smoothing it over.**
   Measured on PostgreSQL 17, MySQL 8.4, MariaDB 11.4 and SQLite 3.53:

   | | PostgreSQL | MySQL | MariaDB | SQLite |
   |---|---|---|---|---|
   | a declared `UNIQUE` told apart from a bare `CREATE UNIQUE INDEX` | yes | **no** | **no** | yes |
   | the declared constraint name survives | yes | yes | yes | **no** |
   | partial (`WHERE`) indexes | yes | **no** | **no** | yes |
   | prefix (`col(10)`) indexes | **no** | yes | yes | **no** |
   | expression indexes | yes | yes | **no** | yes |
   | the expression's text is readable | yes | yes | — | **no** |
   | a partial index's predicate is readable | yes | — | — | **no** |
   | CHECK constraints are listed at all | yes | yes | yes | **no** |
   | a CHECK's own columns are readable | yes | **no** | **no** | — |
   | a key can be DEFERRABLE at all | yes | **no** | **no** | **no** |
   | one name can be two objects on one table | yes, a CHECK and an index | yes, an index and a foreign key | yes, the same | **no** |

   PostgreSQL separates the first row by whether a `pg_constraint` row backs the
   index and SQLite by `pragma_index_list.origin`; MySQL and MariaDB list every
   unique index in `information_schema.TABLE_CONSTRAINTS` as `UNIQUE`, so on
   those two there is nothing to tell apart. SQLite reports the kind and loses
   the name: a `CONSTRAINT uc UNIQUE (a, b)` comes back as
   `sqlite_autoindex_t_2`, so a lookup by the name the author wrote misses there
   and nowhere else. And SQLite answers *partial, predicate unknown* — the flag
   is in the pragma and the `WHERE` clause exists only in the index's DDL, which
   nothing parses.

   PostgreSQL reads a CHECK's columns out of `conkey`; the MySQL family hands
   back the clause and no columns, so those come back nil — not known, rather
   than none. Only PostgreSQL has a `DEFERRABLE` key, which is exactly the key a
   pre-flight probe must not claim to have checked, since the server does not
   apply it until COMMIT. And the last row is a namespace fact rather than a
   feature: an index name and a foreign-key name are separate namespaces on the
   MySQL family, a constraint name and an index name are on PostgreSQL, so one
   table can carry two objects of one name — `Catalog.Constraint` answers one of
   them and both are in `Table.Constraints`.

   None of it is normalisable, because three of those rows are about a key kind
   an engine does not have: `CREATE UNIQUE INDEX ... WHERE` is error 1064 on
   MySQL and MariaDB, and `((lower(col)))` is error 1064 on MariaDB. What the
   catalog does instead is record what the engine said and mark what it could not
   say, so a reader can tell "no" from "not known" ([[D-041]]).
10. **What a classified violation can say about itself, and which engine it says
    it on.** Two halves under one number, because they are the same fact seen
    from two sides.

    (a) *The driver's limit.* A classified fault carries a constraint, a table
    and a schema on PostgreSQL and none of the three on MySQL, MariaDB or
    SQLite. `mysql.MySQLError` has `Number`, `SQLState` and `Message` and nothing
    else; SQLite's foreign-key error is the sentence `FOREIGN KEY constraint
    failed` and nothing else. Only pgconn populates structured fields at all.
    Where the driver names a table and a constraint the columns can be filled
    from the catalog; where it names neither there is nothing to look one up by,
    so a PostgreSQL `22001` and a SQLite foreign key stay blank whatever is
    wired. The status is the same on all four engines; the richness is not.

    (b) *The caller's declaration.* Whether a MariaDB server is classified as
    MariaDB depends on which `crudsql` constructor declared it, because nothing
    in the tree tells the two servers apart at run time and guessing would answer
    "mysql" ([[D-046]]). One failed CHECK on one server is a 409 reading
    `errs: conflict: check` through `crudsql.MariaDB` and a 409 carrying the
    driver's own sentence through `crudsql.MySQL`. The status never moves; the
    code does.

## What it forbids

- Do not branch on `Dialect.Name()` in a repository or a builder. Add a method
  to `Dialect`, or an optional interface if not every dialect needs it. The one
  remaining name check is `Order.render`'s `postgres` test for `NULLS LAST`, and
  it is there because the behaviour is genuinely PostgreSQL-only rather than a
  capability another dialect might grow.
- Do not add a required method to `Dialect`. A dialect written outside this
  package must keep compiling; that is what `OffsetLimiter` demonstrates.
- Do not skip the MySQL re-read to save a round trip. It has been tried; see
  [[D-011]] and [[D-010]].
- Do not add a tenth observable difference without adding it to the list above
  and to both usage guides.
- Do not compensate for difference 6 with a Go-side length, range or type check.
  [[D-042]] has the argument: MySQL under a laxer `sql_mode` truncates where it
  otherwise errors, so a Go-side check reports violations the server would never
  raise on a deployment the library cannot see.

## Where it lives

- `crud/dialect.go:Dialect` — the interface; the whole difference surface.
- `crud/dialect.go:OffsetLimiter` — the optional-capability pattern.
- `crud/dialect.go:Postgres`, `crud/dialect.go:MySQL`, `crud/dialect.go:SQLite`.
- `crud/dialect.go:MySQL.LimitAll` / `crud/dialect.go:SQLite.LimitAll`.
- `crud/dialect.go:SQLite.LockClause` — empty, with the reason.
- `crud/render.go:SQL.LimitOffset` — asks the dialect rather than checking its
  name.
- `crud/predicate.go:Order.render` — the one remaining name check, and the
  `NULLS` clause it guards.
- `crud/predicate.go:LikeIgnoreCase` — the portable spelling.
- `repo/basic/repository.go:newRepository` — `returning` is empty when the
  dialect has none, which is what makes the two write paths diverge.
- `repo/basic/repository.go:repository.insert` and
  `repo/basic/repository.go:repository.Update` — the two paths.
- `repo/basic/repository.go:repository.Count` — the derived table for a DISTINCT
  count.
- `crud/repo.go:Repo.UpdateAll` — documents observable difference 2.
- `crud/dialect.go:Postgres.Upsert` / `crud/dialect.go:MySQL.Upsert` — where
  difference 5 comes from.
- `catalog/postgres.go`, `catalog/mysql.go`, `catalog/mariadb.go`,
  `catalog/sqlite.go` — difference 9, one file per engine, each naming what its
  server cannot answer.
- `catalog/catalog.go:Constraint` — `Kind`, `Partial`, `Predicate`, `Prefixes`
  and `Expressions` are the fields difference 9 is expressed in.
- `errs/sqlerr/testdata/corpus/` — differences 6, 7 and 8 as captured entries:
  `too_long`, `out_of_range` and `bad_type` are marked unreachable on SQLite,
  and `check` differs between `mysql.json` and `mariadb.json`.

## Proven by

- `TestEveryProviderAnswersTheSameQuery` in `test/integration/matrix_test.go` —
  the shape of this decision as a test: the same query through every driver and
  both engines.
- `TestDialectSyntax` and `TestDialectUpsert` in `crud/dialect_test.go`;
  `TestDialectShorthands` in `crud/crudtest/recorder_test.go`.
- `TestUpsertClauseCarriesItsOwnLeadingSpace` in `crud/dialect_test.go` — the
  kind of detail that produces `usersON DUPLICATE` on exactly one engine.
- `TestUpsertLeavesTheSameRowInEveryDialect` in
  `test/integration/dialect_edge_test.go`.
- `TestSaveLeavesTheCallerHoldingTheStoredRowOnEveryEngine` in
  `test/integration/dialect_edge_test.go` — the re-read compensation, from the
  caller's side.
- `TestUpdateOfARowThatVanishedIsNotFoundOnEveryDialect` in
  `repo/basic/repository_test.go`.
- `TestSQLiteTakesAnOffsetWithoutALimit` in
  `test/integration/driver_sqlite_test.go` — the `OffsetLimiter` bug, pinned.
- `TestBoundaryValuesRoundTripOnEveryProvider` and
  `TestDegenerateInputsAnswerEmptyOnEveryProvider` in
  `test/integration/edge_test.go`.
- `TestQuotedIdentifiersSurviveEveryClause` in
  `test/integration/dialect_edge_test.go`.

- `TestAUniqueIndexAndAUniqueConstraintAreToldApartWhereTheEngineTellsThemApart`
  in `test/integration/catalog_test.go` — difference 9's first two rows, asserted
  in both directions per engine, so a catalog reporting the split everywhere and
  one reporting it nowhere each fail.
- `TestAnUnreproducibleUniqueKeyIsRecordedAndItsPlainTwinIsNot` in
  `test/integration/catalog_test.go` — the partial/prefix rows, with the twin
  that stops "mark everything" from passing.
- `TestAnExpressionUniqueKeyIsRecordedAsOneAndItsPlainTwinIsNot` in
  `test/integration/catalog_test.go` — difference 9's two expression rows: the
  index exists on three engines and not on MariaDB, and its text is readable on
  two of those three.
- `TestADeferrableConstraintIsRecordedAndItsImmediateTwinIsNot` in
  `test/integration/catalog_test.go` — the DEFERRABLE row, asserted from both
  sides: the pair on PostgreSQL, and nothing invented on the other three.
- `TestTwoObjectsSharingOneNameStayTwoConstraints` in
  `test/integration/catalog_test.go` — the two-objects-one-name row, in each
  engine's own spelling of the collision and in SQLite's inability to produce it.
- `TestEachEngineReportsWhatTheProbeWillNeed` in
  `test/integration/catalog_test.go` — the `MaxLength` half of difference 6,
  difference 9's CHECK rows including nil-is-not-empty on a CHECK's columns, and
  the engines' own column ordinals.

The four observable differences each have their own test, so a future change
that accidentally *hides* one is also caught:

- `TestNullsOrderingIsPostgresOnly` in `crud/predicate_test.go` and
  `TestWhereNULLsSortIsTheEnginesChoiceAndTheHintIsPostgresOnly` in
  `test/integration/dialect_edge_test.go`.
- `TestForUpdateIsANoOpOnSQLite` in `test/integration/driver_sqlite_test.go`.
- `TestLikeFollowsTheCollationAndLikeIgnoreCaseOverridesIt` in
  `test/integration/dialect_edge_test.go` — asserts *both* halves: the portable
  one matches on both engines, and the collation-dependent one differs.
- `TestUpdateAllIsOneStatementForTheWholeFilter` in
  `repo/basic/updateall_test.go` covers the statement; the row-count difference
  is documented on the method rather than asserted, because asserting it means
  asserting a driver's behaviour.

- `TestAMariaDBCheckIsOnlyClassifiedWhenTheSourceSaysMariaDB` in
  `test/integration/corpus_test.go` — difference 10(b), live: `4025` through
  `crudsql.MariaDB` carries `check`, and the same violation on the same server
  through `crudsql.MySQL` is still a 409 and carries no code. The second half is
  the control; without it the test says nothing about why the constructor exists.
- `TestACatalogFillsTheColumnsAUniqueViolationDoesNotName` and
  `TestEveryCorpusCaseReachesTheCallerAsTheFaultTheCorpusNames` in the same file
  — difference 10(a): what each engine's violation can say about itself, and
  what only the catalog can add.

## See also

[[D-010]] [[D-011]] [[D-015]] [[D-020]] [[D-024]] [[D-041]]
