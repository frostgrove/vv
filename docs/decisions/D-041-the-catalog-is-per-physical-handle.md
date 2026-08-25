# D-041 — The catalog is per physical handle, and its absence is a start-up failure

**Status:** accepted
**Invariant:** Schema introspection is keyed on the identity a source reports for its database handle, never on a DSN and never on a package-level variable. A repository declared with a feature that needs a catalog refuses to start when the catalog cannot be loaded.

## The decision

`Load(ctx, src)` reads the schema once, at declaration time, and the result is
keyed on what `crud.Identified.DataSource()` reports — the `*sql.DB`, the
`*pgxpool.Pool`, whatever the adapter holds. Lookups afterwards do no I/O and
take no `context`.

A load that cannot happen — an unknown dialect, a refused statement, a handle
that cannot be compared — says so **at declaration**, not on the first failed
write.

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

**Why the key is `crud.KeyOf` and not what `Identified` answers.**
`crud.ReadWrite` *is* `Identified` and answers nil when its primary is not, and
`KeyOf` says why it does not take that at face value: *"A wrapper that forwards
an identity it does not have answers nil; that is 'I cannot say', not 'my
identity is nil'."* Keying on the interface's answer puts **every** such pair on
nil — and `SameDataSource(nil, nil)` is false, so no entry keyed on nil is ever
found again. Every lookup misses, every declaration re-introspects, and the whole
thing still looks like it works.

`KeyOf` is what closes that: an unidentifiable source is returned as its own key,
so a pair over an unidentified primary keys on the pair and two such pairs are
two catalogs. This corrects the version of this section written in phase 0, which
said the test was `keyOf(src) != nil`. That predicate can never be false —
measured in-package, `keyOf` returns the source itself for anything that cannot
name a database. The sentence describing "a source that cannot name its database
gets no catalog" was describing `ownScope`, which is a different rule for a
different question ([[D-009]]).

**Why the crudtest recorder does not grow a `DataSource()`.** §16 of
`ROADMAP-errors.md` left that open, because it was read as the difference between
the probe having a unit-test seam and being integration-only. Under the rule
above it is not: `crud.KeyOf` gives a `*crudtest.Recorder` a perfectly good key —
itself, a non-nil comparable pointer — so two recorders are two catalogs and one
recorder is one. The seam is already there.

Adding the method would change something else entirely. `crud.InTx` binds the
transaction it opens to the source's own datasource **when the source is
`Identified`**, so every existing test that wraps a recorder in `InTx` would go
from an unscoped binding, which every repository joins, to a scoped one only
repositories over that recorder join. Two dozen test files across `crud`,
`query`, `repo/basic`, `repo/decorators` and `_examples` bind a recorder.
Nothing in the tree fails today if that changed, which is exactly why it is
pinned by a test rather than left to judgement. **Answered: no.**

**Why not a `map[any]`.** `SameDataSource` avoids one deliberately: *"a
datasource handle is a pointer in practice, but nothing in the contract says it
must be"*, and an uncomparable map key panics at run time. Catalogs live in a
slice compared with that function.

The refusal needs one thing `SameDataSource` does not give on its own, and it was
found by breaking the test rather than by reading the code. `reflect.Type.Comparable`
answers about the *static* type: a struct holding an interface is comparable, and
`==` on it panics once that interface turns out to hold a slice. So the
comparability probe is the comparison itself, guarded by a `recover` — a refusal
is what this decision asks for, and a panic at declaration is not.

**Why `Constraint` takes the table.** An index name is unique per table on MySQL,
not per schema — every InnoDB table's primary index is called `PRIMARY`. The
corpus records it: MariaDB reports a duplicate primary key as `for key
'PRIMARY'`, with nothing naming the table. A bare name is ambiguous across the
database. MySQL's own `information_schema.CHECK_CONSTRAINTS` makes the same point
from the other side: it has no `TABLE_NAME` column at all, so the loader has to
join `TABLE_CONSTRAINTS` to learn which table a check belongs to.

**Why a build cannot be keyed on the bare name.** `Constraint` taking the table
answers *which* table a name belongs to and does not make the pair an identity.
One table can carry two objects of one name, because the namespaces are separate:
an index name and a foreign-key name are on MySQL and MariaDB, so `UNIQUE KEY k
(a)` beside `CONSTRAINT k FOREIGN KEY (a)` is legal and `TABLE_CONSTRAINTS`
answers two rows for `k`; a constraint name and an index name are on PostgreSQL,
so a `CHECK` named `k` beside `CREATE UNIQUE INDEX k` is. A build that finds a
constraint again by name alone amends the first object with the second: the key
parts of both end up in one `Columns`, which stops being parallel to
`Expressions`, `Prefixes` and `RefColumns` — the position a probe reads its
results by ([[D-042]]) — and one of the two objects is never recorded at all.

The key is the name and the *family*: the three key kinds together, because one
key arrives under two of them (MySQL announces `PRIMARY` in `TABLE_CONSTRAINTS`
and fills its columns in from `STATISTICS`, which knows only that the index is
unique), and a foreign key and a check each on their own. `Catalog.Constraint`
still answers one, and which one is the first the engine listed — deterministic
because every statement carries an `ORDER BY` that fully orders its rows
([[D-014]]).

**Why lookups take no context.** A signature that accepts one is a lazy loader,
and a lazy loader cannot fail at start-up — which is the whole of [[D-021]]'s
rule. `Load` is the thing that can fail.

**Why `Reload` is a separate, optional interface.** §7 states two rules in
adjacent subsections and never reconciles them: a lookup does no I/O and takes no
context, *and* the first unknown constraint name triggers one reload. The
reconciliation is that reloading is not a lookup. `Reload(ctx, table, name)` lives
on an optional `Reloader` that what `Load` returns implements and `Catalog` does
not, which is the house idiom for exactly this — `crud.Beginner`,
`crud.ReadSourcer`, `crud.OffsetLimiter` and `crud.Identified` all exist so a
component written outside the package keeps compiling.

It carries two guards and not one. The per-name negative entry is §7's own words
and stops one renamed constraint re-reading forever. The per-handle floor is the
loop the per-name entry does not close: fifty *different* unknown names, which is
what one bulk write against a stale catalog produces, would otherwise cause fifty
passes a millisecond apart.

**Why a flag and a verbatim definition rather than a parsed predicate.** §13.4
makes a constraint whose predicate cannot be reproduced faithfully **skipped and
said to be skipped**, and that is only expressible if *partial, predicate
unknown* is a state. It is a real one: SQLite's `pragma_index_list` reports
`partial = 1` and keeps the `WHERE` clause only inside the index's DDL. Reading
it back would mean finding a top-level `WHERE` inside DDL text, which breaks on
`CREATE UNIQUE INDEX i ON t (CASE WHEN a THEN b END)` and is the DDL model this
decision forbids. So `Partial` is true, `Predicate` is empty, and `Definition`
carries the engine's own text that nothing parses.

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
- Do not key on what `Identified` answers. Key on `crud.KeyOf`, which takes an
  unidentifiable source at face value — the interface test collides every
  `ReadWrite` over an unidentified primary onto nil, and nothing keyed on nil is
  ever found again.
- Do not put catalogs in a `map[any]`.
- Do not key a constraint build on the bare name. A table can carry two objects
  of one name, and folding them together breaks the position parallel and loses
  one of them.
- Do not add a `DataSource()` to `crud/crudtest`'s recorder. It needs none, and
  adding one silently rescopes `crud.InTx` in every test that wraps a recorder.
- Do not make a lookup do I/O, and do not give one a `context` — both turn a
  start-up failure into a request failure.
- Do not give `Reload` to `Catalog`. The interface a consumer implements has to
  stay the one that cannot fail.
- Do not parse DDL text to recover a predicate, an expression or a check. Carry
  the engine's own text verbatim and mark what is unknown.
- Do not read an empty catalog as a blocked introspection, or the reverse. A
  database with no tables is a database with no tables. The one thing phase 6
  cannot deliver is the MySQL case where a user with no grants sees zero
  `information_schema` rows rather than a refusal — see *Owed by phase 7*.
- Do not resolve a bare table name lazily per connection. The catalog resolves
  it once, on the connection it loaded from, and records the resolved schema.
- Do not let it grow into a migration tool, a DDL model, or a Go-side
  re-implementation of the database's rules. Two implementations of one
  constraint disagree eventually, and the one in the database is the one that is
  right.

## Where it lives

- `catalog/doc.go` — the rules a signature cannot carry.
- `catalog/catalog.go` — `Catalog`, `Table`, `Column`, `Constraint`, `Kind`.
- `catalog/errors.go` — `ErrUncomparableHandle`, `ErrUnknownDialect`,
  `ErrIntrospection`.
- `catalog/load.go` — `Load`, `backendFor`, `eachRow`, `builder`, `conBuildKey`,
  `conFamily`, `snapshot`.
- `catalog/set.go` — `Set`, `Set.Load`, `Set.For`, `findable` (the guarded
  comparability probe).
- `catalog/reload.go` — `Reloader`, `loaded.Reload`, the negative cache and the
  per-handle floor.
- `catalog/postgres.go`, `catalog/mysql.go`, `catalog/mariadb.go`,
  `catalog/sqlite.go` — one back-end per engine.
- `crud/executor.go:KeyOf` / `crud/executor.go:SameDataSource` — the identity
  rules this reuses rather than reinvents. They were unexported until phase 6 and
  `catalog` is what exports them.
- `crud/executor.go:Identified` — the only identity the seam offers.
- `sqlfault/catalog.go:FromCatalog` — the first consumer of a loaded catalog
  outside `catalog` itself. It holds the catalog on the classifier *value* the
  caller declared and never in a package-level variable, and its lookup does no
  I/O and takes no context, for the reason `Catalog` itself takes none: a
  signature that accepted one would be a lazy loader, and a lazy loader cannot
  fail at start-up. The declaration-time refusal this decision owes phase 7 is
  untouched — `sqlfault` reads a catalog that was already loaded and never loads
  one.
- `crud/crudtest/recorder.go:Result.RowsErr` / `crud/crudtest/recorder.go:RowsFailing`
  — the double's way of producing the mid-stream failure `eachRow`'s last arm
  exists for. `Result.Err` is Query refusing; this is pgx's shape, where a
  refused statement is a live `Rows` carrying the error on `Err`.

## Proven by

- `TestTwoSourcesOverDifferentHandlesDoNotShareACatalog` and its control
  `TestTwoIndependentlyBuiltSourcesOverOneHandleShareOneCatalog` in
  `catalog/set_test.go`.
- `TestAReadWritePairAndItsPrimaryShareOneCatalog` and
  `TestAReadWritePairOverAnotherPrimaryGetsItsOwnCatalog` in
  `catalog/set_test.go` — [[D-032]]'s forwarding rule as a key.
- `TestTwoReadWritePairsOverUnidentifiedPrimariesDoNotCollide` in
  `catalog/set_test.go` — the exact difference between `crud.KeyOf` and the
  interface test, asserted from both sides.
- `TestTheRecorderKeysAsItselfSoTheProbeHasAUnitTestSeam` in
  `catalog/set_test.go` and `TestTheRecorderStaysUnidentified` in
  `crud/crudtest/recorder_test.go` — the §16 answer, pinned where a revert would
  land.
- `TestAnUncomparableHandleIsRefusedRatherThanPanicking` and its control
  `TestAComparableHandleIsAcceptedAndFoundAgain` in `catalog/set_test.go` — both
  shapes of uncomparable, including the one `reflect` calls comparable.
- `TestAnUnknownDialectIsRefusedBeforeAnyStatement`,
  `TestABlockedIntrospectionFailsLoadRatherThanReturningAHalfCatalog` — both the
  refusal `Query` reports and the one `Rows.Err` reports, which is pgx's shape —
  and `TestARowThatCannotBeScannedFailsLoadRatherThanDroppingIt`, with their
  controls, in `catalog/load_test.go`. The mid-stream arm needs a double that can
  express it, which is what `crudtest.Result.RowsErr` is for; it is pinned by
  `TestARowsErrorArrivesAfterTheRowsRatherThanInsteadOfThem` in
  `crud/crudtest/recorder_test.go`.
- `TestTwoGoroutinesDeclaringOverOneHandleEndUpWithOneCatalog` in
  `catalog/set_test.go` — "one catalog per physical handle" under two declarers
  held at one barrier, which is the only assertion that holds it concurrently.
- `TestALookupIssuesNoStatement` in `catalog/reload_test.go` — no I/O, no
  context, and `Reload` as its control.
- `TestTheSameUnknownNameDoesNotReintrospectInALoop`,
  `TestOnceTheWindowPassesTheCatalogReadsAgain`,
  `TestManyDistinctUnknownNamesDoNotReintrospectOnceEach`,
  `TestOnceTheFloorLiftsADifferentNameReadsAgain`,
  `TestAReloadThatFindsTheNameResetsTheBackoff`,
  `TestTheBackoffStopsDoublingAtTheCeiling` and
  `TestAFailedReloadKeepsTheSchemaAndSaysSo` in `catalog/reload_test.go` — the
  negative cache, both guards, both directions, and the ceiling the doubling
  stops at.
- `TestAConstraintIsKeyedOnItsTableAsWellAsItsName` and
  `TestColumnsAndConstraintsKeepTheOrderTheEngineReported` in
  `catalog/catalog_test.go`.
- `TestAnUnreproducibleUniqueKeyIsRecordedAndItsPlainTwinIsNot` and
  `TestAnExpressionUniqueKeyIsRecordedAsOneAndItsPlainTwinIsNot` in
  `test/integration/catalog_test.go` — the twin, live, on all four engines, for
  each of the two kinds of key the catalog cannot replay from a value. The second
  is also the only place the SQLite expression text is pinned as *unreadable*
  rather than recovered from `sqlite_master.sql`.
- `TestADeferrableConstraintIsRecordedAndItsImmediateTwinIsNot` in
  `test/integration/catalog_test.go` — a key the server does not apply until
  COMMIT, told from its immediate twin on the one engine that has the notion and
  invented on none of the other three.
- `TestTwoObjectsSharingOneNameStayTwoConstraints` in
  `test/integration/catalog_test.go` — the bare-name forbid above, on the two
  spellings of the collision, with the control that nothing else on the table
  split.
- `TestABareUniqueIndexAForeignKeyPointsAtIsStillInTheCatalog` in
  `test/integration/catalog_test.go` — the class of key PostgreSQL enforces under
  a name no constraint catalog knows, which is the whole reason there is a third
  PostgreSQL statement, with the unreferenced twin and the control that the
  anti-join still excludes what it was written for.
- `TestAShorthandReferencesRecordsNoParentColumnAndItsExplicitTwinDoes` in
  `test/integration/catalog_test.go` — a schema SQLite accepts must not be a
  start-up refusal, and the parent column it does not name is recorded as unknown
  rather than derived.
- `TestEachEngineReportsWhatTheProbeWillNeed` in
  `test/integration/catalog_test.go` — every field the probe reads against its
  own opposite, including the engine's own column ordinals and the
  nil-is-not-empty rule on a CHECK's columns.
- `TestABareTableNameResolvesOnceAndTheResolvedSchemaIsRecorded` in
  `test/integration/catalog_test.go` — the `search_path` case a DSN key would
  merge.
- `TestOneSetHoldsFourLiveDatabasesWithoutMergingThem` in
  `test/integration/catalog_test.go` — the identity rule against real handles.

### Owed by phase 7

The invariant has two halves and phase 6 pays one. There is no repository
declaration that needs a catalog until `probe/` lands, so *"a repository declared
with a feature that needs a catalog refuses to start when the catalog cannot be
loaded"* has no site to be tested at. Phase 7 owes that test, and with it the one
case phase 6 could not close: a MySQL user with no `information_schema` grants
reads zero rows rather than being refused, so `Load` succeeds and the catalog is
empty. Phase 7 can tell the two apart, because by then a declaration names a
table the catalog must know.

## See also

[[D-009]] [[D-014]] [[D-019]] [[D-021]] [[D-027]] [[D-032]] [[D-039]] [[D-042]]
[[D-044]] [[UC-012]] [[FL-016]]
