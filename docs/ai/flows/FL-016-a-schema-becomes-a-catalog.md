# FL-016 — A schema becomes a catalog

**Entry point:** `crud/catalog/load.go:Load` — usually through `crud/catalog/set.go:Set.Load`
**Implements:** [[UC-012]]

One database's schema, read once at declaration time, answered from memory
afterwards. Nothing in this path runs during a request: `Load` is the thing that
can fail and it fails at start-up ([[D-021]], [[D-041]]).

It has one reader outside its own tests since phase 3: `crud/sqlfault/catalog.go`
takes a loaded `Catalog` and answers which columns a constraint covers, so a
violation the driver named no column for can still say which fields it was about
([[FL-014]]). It reads and never loads, and it holds the catalog on a value the
caller declared.

Line numbers were correct when this was written. The symbol name is the thing
that has to still exist.

## The path

1. **`Set.Load`** — `crud/catalog/set.go:46`
   `k := crud.KeyOf(src)`. A raw handle, a `Source` over it, a `crud.ReadWrite`
   pair over that, and an instrumenting wrapper over any of them all reduce to
   the same key: `KeyOf` walks the wrapper chain through `crud.SourceUnwrapper`
   and `readWrite.DataSource` forwards the primary's identity
   (`crud/executor.go:identityOf`, [[D-032]], [[D-061]]). The walk is what makes
   "per handle" mean the handle rather than whatever is standing in front of it.
   A source that cannot name a database is
   returned as its own key and never as nil — see the trap below, because the
   distinction is the whole reason this keys on `KeyOf` and not on what
   `Identified` answers.

2. **The refusal** — `crud/catalog/set.go:95:findable`
   `crud.SameDataSource(k, k)`, guarded by a `recover`. A key that cannot be
   compared could be stored and never found again, so it is refused with
   `ErrUncomparableHandle` **before any statement runs**. The guard is there
   because `reflect.Type.Comparable` answers about the static type: a struct
   holding an interface is comparable and `==` on it panics once that interface
   holds a slice.

3. **The scan** — `crud/catalog/set.go:54`
   A slice compared with `crud.SameDataSource`, never a `map[any]` ([[D-041]]).
   A hit answers immediately. A miss loads outside the lock, then re-scans before
   appending, so two goroutines declaring over one handle end up with one
   catalog.

4. **`backendFor`** — `crud/catalog/load.go:58`
   Picks the statements from `src.Dialect().Name()`. An unknown name is
   `ErrUnknownDialect`, again before any statement. `"mysql"` is two engines, so
   this is where `isMariaDB` (`crud/catalog/load.go:85`) sends one `SELECT VERSION()`
   and splits on a case-insensitive `mariadb`. It has to happen here rather than
   later: MariaDB's `information_schema.STATISTICS` has no `EXPRESSION` column
   and MySQL's statement fails there with error 1054.

5. **The statements** — one file per engine, every one through `crud.Source.Query`
   because `Exec` and `Query` are all the seam has.

   | engine | file | statements |
   |---|---|---|
   | PostgreSQL | `crud/catalog/postgres.go` | columns, `pg_constraint`, the unique indexes no constraint backs — 3 |
   | MySQL | `crud/catalog/mysql.go` | `VERSION`, `COLUMNS`, `TABLE_CONSTRAINTS`, `STATISTICS`, `KEY_COLUMN_USAGE`+`REFERENTIAL_CONSTRAINTS`, `CHECK_CONSTRAINTS` — 6 |
   | MariaDB | `crud/catalog/mariadb.go` | the same six, two of them spelled differently |
   | SQLite | `crud/catalog/sqlite.go` | `sqlite_master`, then the four `pragma_*()` table-valued functions — 5 |

   Each read goes through `crud/catalog/load.go:109:eachRow`, which follows
   `crud/sqlrepo/repository.go`'s idiom exactly — `defer rows.Close()`, loop, then
   `rows.Err()` — and wraps any failure in `ErrIntrospection`.

6. **The schema is resolved once and recorded.** PostgreSQL scopes every
   statement with `pg_table_is_visible`, which is the server's own answer to
   "what does this bare name resolve to on this connection", and records the
   `nspname` it resolved to on each table. MySQL and MariaDB use `DATABASE()`;
   SQLite uses `main`. Resolving a bare name lazily per connection is what
   [[D-041]] forbids.

7. **The build** — `crud/catalog/load.go:133:builder`
   Rows arrive over several statements and are collected by `(schema, table)` and
   then, in `crud/catalog/load.go:210:tableBuild.constraint`, by **name and family** —
   `conBuildKey`, whose `conFamily` puts the three key kinds together and leaves a
   foreign key and a check each on their own. By name alone, one name that is two
   objects folds into one; see the trap. First sight fixes position; nothing
   sorts. Every statement carries its own `ORDER BY`, so the order is the
   engine's and it is the same on every run ([[D-014]]) — which matters because a
   probe reads its results by column position ([[D-042]]).

8. **The snapshot** — `crud/catalog/load.go:254:newSnapshot`
   One whole schema, stored through an `atomic.Pointer`. Lookup maps are built
   here; they feed no SQL and no output order, so they are maps. `byCons` is
   first-wins per `(table, name)`, because two objects can share a name and the
   lookup answers one.

9. **A lookup** — `crud/catalog/load.go:299:loaded.Table` / `:308:loaded.Constraint`
   No I/O, no `context`. `Constraint` takes the table as well as the name,
   because every InnoDB table's primary index is called `PRIMARY`.

## The reload path

**`Referrers` is the second optional interface, and it is there for the same
reason.** A constraint is recorded on the table that *declares* it, so no lookup
on `Catalog` can answer "which foreign keys point at this table" — which is
exactly what a `restrict` violation needs ([[FL-017]]). `crud/catalog/load.go`
builds the reverse index once, in `newSnapshot`, rather than walking every table
per lookup, because a lookup does no work. `Catalog` itself does not carry it:
the interface a consumer implements stays the small one, and a catalog written
elsewhere that does not implement it simply produces no `restrict` terms.

`crud/catalog/reload.go:66:loaded.Reload` — an optional `Reloader`, not part of
`Catalog`. A caller that met a constraint name the catalog has never heard of
asks for one more look; a rolling migration is the case it exists for.

Two guards under one mutex, and they close different loops:

- **The per-name negative entry.** `misses[consKey]` with an interval that starts
  at `minBackoff` (1s), doubles, and stops at `maxBackoff` (5 min). A pass that
  finds the name deletes the entry; one that does not arms it. Without it, one
  renamed constraint turns every failed write into a full introspection pass.
- **The per-handle floor.** `reloadFloor` (1s) between any two passes, armed even
  when a pass fails. Without it, fifty *different* unknown names — which is what
  one bulk write against a stale catalog produces — cause fifty passes a
  millisecond apart.

A failed pass keeps the old snapshot and returns the wrapped error. The clock is
`crud/catalog/reload.go:115:loaded.clock`, injectable, because the alternative is a
test that sleeps.

## Where the decisions bite

- **[[D-041]]** — all of it. The key, the slice, the refusal, the context-free
  lookup, the recorder answer, and the forbid on parsing DDL.
- **[[D-021]]** — why `Load` fails rather than answering an empty catalog. An
  empty one reads as "this database has no constraint problems".
- **[[D-014]]** — every statement has an `ORDER BY`, and nothing iterates a map
  into an order a caller can see.
- **[[D-019]]** — difference 9 is this flow's per-engine table: which unique keys
  each engine can tell apart, and which kinds of key it has at all.
- **[[D-032]]** — a `crud.ReadWrite` pair keys on its primary because
  `readWrite.DataSource` forwards it.
- **[[D-044]]** — a constraint, a table, a column and a CHECK expression are four
  of the seven things a body may not name, and this package is where all four
  live. `ErrIntrospection` says nothing itself.
- **[[D-048]]** — `catalog` is not on the contract manifest and must not be added
  to `Makefile:TIER0`.

## Traps

- **`crud.KeyOf` never answers nil.** It returns an unidentifiable source as its
  own key. `ownScope`, three lines away in the same file, is the one that answers
  nil, and D-041 was written against it by mistake until phase 6. Keying on what
  `Identified` answers puts every `ReadWrite` over an unidentified primary onto
  nil, and `SameDataSource(nil, nil)` is false, so nothing keyed there is ever
  found again.
- **`PRAGMA table_xinfo(?)` is a parse error.** A PRAGMA takes no bind
  parameters. The table-valued `pragma_table_xinfo(?)` binds, which is why every
  SQLite statement here is a `SELECT ... FROM pragma_*()` and none concatenates a
  table name into SQL.
- **SQLite's `pragma_foreign_key_list` reports `on_update` before `on_delete`.**
  That is the opposite of the order the DDL is written in, and reading them in
  written order silently swaps the two actions.
- **`Partial` true with an empty `Predicate` means unreadable, not absent.**
  SQLite reports the flag and keeps the `WHERE` clause only inside the index's
  DDL. `Definition` carries that text verbatim and nothing parses it.
- **An empty catalog is a database with no tables**, not a blocked introspection.
  The one case phase 6 cannot separate is a MySQL user with no
  `information_schema` grants, who reads zero rows rather than being refused —
  D-041 records it as owed by phase 7.
- **MySQL's `information_schema.CHECK_CONSTRAINTS` has no `TABLE_NAME`.**
  MariaDB's has one. That is why one of them joins `TABLE_CONSTRAINTS` and the
  other does not.
- **MariaDB reports `COLUMN_DEFAULT` as expression text.** A nullable column with
  no `DEFAULT` clause comes back as the unquoted word `NULL`, and read literally
  every nullable MariaDB column would have a default of the string "NULL". A
  column declared `DEFAULT 'NULL'` comes back quoted, so the two are still told
  apart.
- **`information_schema.TABLE_CONSTRAINTS` lists every unique index as `UNIQUE`
  on MySQL and MariaDB.** §7 of `ROADMAP-errors.md` said a bare unique index
  appears only in `STATISTICS`; that is a PostgreSQL fact. `STATISTICS` is read
  for the key columns, their order, `SUB_PART` and `EXPRESSION`, which exist
  nowhere else.
- **`crud.SameDataSource` can still panic**, on a statically comparable type
  holding an uncomparable value. `crud/catalog/set.go:findable` is what makes the
  refusal a refusal.
- **One name on one table can be two objects.** An index name and a foreign-key
  name live in different namespaces on MySQL and MariaDB, so `UNIQUE KEY k (a)`
  beside `CONSTRAINT k FOREIGN KEY (a)` is legal; a constraint name and an index
  name do on PostgreSQL, so a `CHECK` named `k` beside `CREATE UNIQUE INDEX k`
  is. A build keyed on the bare name folds them into one: the key parts of both
  land in one `Columns`, which stops being parallel to `Expressions`, `Prefixes`
  and `RefColumns`, and one of the two objects disappears.
- **A foreign key carries a `conindid` too**, and it names the index it
  *references* on the parent table. `pgUniqueIndexes` anti-joins on
  `contype IN ('p','u','x')` for that reason: unqualified, the clause deletes any
  bare unique index a foreign key points at — silently, because `Load` still
  succeeds.
- **`pragma_foreign_key_list` answers `to = NULL`** for `REFERENCES parent` with
  no column list. Scanned into a string that is a start-up refusal on a schema
  SQLite accepts, so the column is `COALESCE`d and the empty `RefColumns` entry
  is the fact.
- **`information_schema.KEY_COLUMN_USAGE` lists a unique key's parts** under the
  same name and table as a foreign key of that name. Those rows carry a NULL
  referenced table, and both MySQL-family statements exclude them; without that
  they join the foreign key's `REFERENTIAL_CONSTRAINTS` row and put an empty
  entry into `RefColumns`.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| a datasource identity that cannot be compared | `findable` (`crud/catalog/set.go:95`), before the scan | `Set.Load` fails with `ErrUncomparableHandle`. No statement sent, and no panic — which is the whole point of the `recover` |
| a source with no dialect, or a dialect no back-end serves | `backendFor` (`crud/catalog/load.go:58`) | `Load` fails with `ErrUnknownDialect`, before the first statement |
| `SELECT VERSION()` refused, so MySQL and MariaDB cannot be told apart | `isMariaDB` (`crud/catalog/load.go:85`) through `eachRow` | `Load` fails with `ErrIntrospection` rather than guessing an engine and sending a statement that cannot run |
| an introspection statement refused — no `information_schema` grant, a proxy in the way | `eachRow` (`crud/catalog/load.go:109`), on `Query`, on a scan and on `rows.Err` alike | `Load` fails at start-up wrapping the driver's error. Never a half-catalog, and never an empty one, which reads as "this database has no constraint problems" ([[D-021]]) |
| the driver's text names a constraint, a table or a column | nothing quotes it: `ErrIntrospection` says nothing itself ([[D-044]]) | a start-up log. No request exists yet, so no transport ever maps one of these to a status ([[FL-011]]) |
| a table or a constraint the catalog has never heard of | `loaded.Table` / `loaded.Constraint` (`crud/catalog/load.go:299`, `:308`) | `false`, no I/O, no error. That is the answer, not a failure |
| a reload whose read fails | `loaded.Reload` (`crud/catalog/reload.go:66`) | the wrapped `ErrIntrospection`. The old snapshot stays and the floor is armed anyway, so a database that is down does not turn every failed write into a pass. No transport recognises the error, so it renders as a 500 with a silent body |
| a name still unknown after a reload pass | `loaded.Reload`, after the pass | `nil`. The caller looks it up again and finds nothing. The negative entry arms and doubles to 5 min |
| a MySQL user with no `information_schema` grants | nothing — the server answers zero rows rather than refusing | an empty catalog, indistinguishable from a database with no tables. Owed to phase 7 ([[D-041]]) |
| a `(*Table)(nil)` reaching `Column` or `Constraint` | the accessors themselves (`crud/catalog/catalog.go:100`, `:114`) | `false` rather than a panic — a component wired wrong degrades instead of taking the process down |

## Files

| File | Role |
|---|---|
| `crud/catalog/doc.go` | what a catalog is, nil-versus-empty, the two rules a signature cannot carry |
| `crud/catalog/catalog.go` | `Catalog`, `Referrers`, `Table`, `Table.Column`, `Table.Constraint`, `Column`, `Constraint`, `Kind` and its constants |
| `crud/catalog/errors.go` | `ErrUncomparableHandle`, `ErrUnknownDialect`, `ErrIntrospection` |
| `crud/catalog/load.go` | `Load`, `backend`, `backendFor`, `isMariaDB`, `eachRow`, `builder`, `tableBuild`, `conBuildKey`, `conFamily`, `familyOf`, `snapshot`, `newSnapshot`, `snapshot.refs`, `loaded`, `loaded.ReferencedBy` |
| `crud/catalog/set.go` | `Set`, `Set.Load`, `Set.For`, `findable` |
| `crud/catalog/reload.go` | `Reloader`, `loaded.Reload`, `loaded.clock`, `negative`, `minBackoff`, `maxBackoff`, `reloadFloor` |
| `crud/catalog/postgres.go` | `readPostgres`, `pgColumns`, `pgConstraints`, `pgUniqueIndexes`, `pgKind` |
| `crud/catalog/mysql.go` | `readMySQL`, the five MySQL statements, and the shaping both MySQL and MariaDB share: `myReadColumns`, `myReadTableConstraints`, `myReadStatistics`, `myReadForeignKeys`, `myReadChecks`, `myKind` |
| `crud/catalog/mariadb.go` | `readMariaDB` and MariaDB's own five statements |
| `crud/catalog/sqlite.go` | `readSQLite`, the five SQLite statements, `sqliteKind`, `sqliteFKName`, `pkPart` |
| `crud/executor.go` | `KeyOf` and `SameDataSource` — exported by this phase — and `SourceUnwrapper` / `identityOf`, the walk `KeyOf` reduces through ([[D-061]]); also [[FL-002]] and [[FL-009]] |
| `crud/crudtest/recorder.go` | `Result.RowsErr` and `RowsFailing` — the mid-stream failure the `rows.Err()` arm exists for, which `Result.Err` cannot express |
| `test/integration/catalog_schema_test.go` | `catSchema`, `catSearchPathSchema` — the four-engine fixture |
| `test/integration/catalog_test.go` | `catEngines`, `catTarget`, `catUnreproducible` and the live assertions |

## Tests that walk this flow

- `crud/catalog/set_test.go` — `TestTwoSourcesOverDifferentHandlesDoNotShareACatalog`,
  `TestTwoIndependentlyBuiltSourcesOverOneHandleShareOneCatalog`,
  `TestAReadWritePairAndItsPrimaryShareOneCatalog`,
  `TestAReadWritePairOverAnotherPrimaryGetsItsOwnCatalog`,
  `TestTwoReadWritePairsOverUnidentifiedPrimariesDoNotCollide`,
  `TestTheRecorderKeysAsItselfSoTheProbeHasAUnitTestSeam`,
  `TestAnUncomparableHandleIsRefusedRatherThanPanicking`,
  `TestAComparableHandleIsAcceptedAndFoundAgain`,
  `TestTwoGoroutinesDeclaringOverOneHandleEndUpWithOneCatalog` — step 3's
  re-scan, under two goroutines held at one barrier.
- `crud/catalog/load_test.go` — `TestAnUnknownDialectIsRefusedBeforeAnyStatement`,
  `TestAKnownDialectLoadsAndIssuesItsStatements`,
  `TestABlockedIntrospectionFailsLoadRatherThanReturningAHalfCatalog` — both
  axes, `Query` refusing and `Rows.Err` refusing —
  `TestARowThatCannotBeScannedFailsLoadRatherThanDroppingIt`,
  `TestAnUnblockedIntrospectionBuildsThePopulatedCatalog`.
- `crud/catalog/reload_test.go` — `TestALookupIssuesNoStatement`,
  `TestTheSameUnknownNameDoesNotReintrospectInALoop`,
  `TestOnceTheWindowPassesTheCatalogReadsAgain`,
  `TestManyDistinctUnknownNamesDoNotReintrospectOnceEach`,
  `TestOnceTheFloorLiftsADifferentNameReadsAgain`,
  `TestAReloadThatFindsTheNameResetsTheBackoff`,
  `TestTheBackoffStopsDoublingAtTheCeiling`,
  `TestAFailedReloadKeepsTheSchemaAndSaysSo`.
- `crud/catalog/catalog_test.go` — `TestAConstraintIsKeyedOnItsTableAsWellAsItsName`,
  `TestColumnsAndConstraintsKeepTheOrderTheEngineReported`,
  `TestANilTableAnswersFalseRatherThanPanicking`,
  `TestEveryKindPrintsItsOwnName`.
- `test/integration/catalog_test.go` —
  `TestAnUnreproducibleUniqueKeyIsRecordedAndItsPlainTwinIsNot`,
  `TestAnExpressionUniqueKeyIsRecordedAsOneAndItsPlainTwinIsNot`,
  `TestADeferrableConstraintIsRecordedAndItsImmediateTwinIsNot`,
  `TestAUniqueIndexAndAUniqueConstraintAreToldApartWhereTheEngineTellsThemApart`,
  `TestTheCatalogNamesMariaDBRatherThanCallingItMySQL`,
  `TestABareTableNameResolvesOnceAndTheResolvedSchemaIsRecorded`,
  `TestABareUniqueIndexAForeignKeyPointsAtIsStillInTheCatalog`,
  `TestAShorthandReferencesRecordsNoParentColumnAndItsExplicitTwinDoes`,
  `TestTwoObjectsSharingOneNameStayTwoConstraints`,
  `TestForeignKeysCarryTheirActionsInTheOrderTheEngineReportsThem`,
  `TestEachEngineReportsWhatTheProbeWillNeed`,
  `TestOneSetHoldsFourLiveDatabasesWithoutMergingThem`.
- `crud/crudtest/recorder_test.go` — `TestTheRecorderStaysUnidentified`,
  `TestARowsErrorArrivesAfterTheRowsRatherThanInsteadOfThem`.

## See also

[[FL-009]] [[FL-004]] [[FL-003]] [[FL-011]] [[UC-012]] [[D-041]] [[D-044]]

[[FL-009]] is the reciprocal link — it already points here twice, for
`KeyOf`/`SameDataSource` and for the uncomparable-handle row. [[FL-004]] is the
other declaration-time flow that fails at start-up; [[FL-003]] is where the
constraints this catalog records get hit; [[FL-011]] is where an error would
become a status, and the point of the failure table above is that none of these
ever does.
