# crudsql · crudpgx — the one line between vv and the connection you already opened

**Covers:** `github.com/frostgrove/vv/crud/adapter/crudsql`, `github.com/frostgrove/vv/crud/adapter/crudpgx`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** ready with gaps — the binding line is one expression, the transaction join is the best-proven thing in the repository, and instrumentation is answered one level below vv rather than badly here; but seven things are silent when they go wrong: the COPY fast path disappears the day anything wraps the source, a COPY ignores the transaction it was called inside and comes back unclassified, a borrowed `database/sql` transaction answers conflicts without a code, lib/pq loses the constraint and the table, a scoped binding keyed on the wrong handle writes outside the transaction, the MySQL 8 upsert form costs the error codes, and neither adapter can give one flow its own isolation level. A `WithTxOptions` copy also aliases mutable caller state, and COPY cannot represent a schema-qualified destination as callers naturally spell one.

## What a consumer is actually trying to do

Somebody already has a connection. It was opened in `main`, it is sized, it has
a health check on it, and something else in the process is already using it — an
ORM, a handful of generated queries, a migration runner. They are not looking for
a database layer. They are looking for a way to point a CRUD library at the pool
they have without that library taking it over.

Before that there is a shorter question, and it is the one only this module can
answer: does this reach our database at all, and with which driver. A team on
SQL Server wants to know now rather than after the model is declared. A team on
PostgreSQL wants to know whether the pgx driver they picked two years ago is the
same choice as the pgx *adapter*, because it is not, and the difference decides
which half of this document applies to them.

The second thing they need is that a transaction somebody else opened stays
somebody else's. A service method that already wraps three writes in one
transaction has to be able to add a fourth through this library and have the
rollback take it with the rest. If that does not work, the library is not
adoptable into an existing service at all — it is only usable in a new one. And
the same question runs the other way once the service matures: the library opens
the transaction and the ORM joins it.

Then the ordinary week starts. Somebody signs up with an email that is already
taken, and the API has to answer 409 with `email` in the body rather than a 500.
A nightly job has a supplier file with a quarter of a million rows in it and the
row-at-a-time loop takes forty minutes. A second database appears for events, and
now it matters a great deal which handle a write lands on. Somebody has to write
a test for all of it against a real server without leaving rows behind. Somebody
wants one flow — one — to run at `SERIALIZABLE`.

The operational week starts in the same month and asks smaller questions that are
just as expensive to answer late. Somebody wants the statements: a trace, a slow
query log, a p99 per query, and they want it to include the statements that ran
inside the ORM's transaction. Every model has a `created_at`, and on MySQL that
column does not arrive as a time unless the DSN said so. When the reservation
flow does run at `SERIALIZABLE`, the loser of the race is aborted and somebody
has to run it again. And one morning the primary fails over, the pool is
replaced, and the question is whether anything the library holds still points at
the right server.

Underneath all of it there is a smaller question that decides how much of the
above works: which server is actually answering, and through which driver. The
dialect says how to write SQL; it does not say whether MySQL or MariaDB is on the
other end, and those two report a failed `CHECK` with different numbers. Nor does
it say whether the driver hands back a constraint name, whether it can report how
many rows an `UPDATE` touched, or whether it turns a `DATETIME` into a time. A
library that guesses any of them gets the answer wrong on somebody's production
API and nothing anywhere says why.

## Happy cases

### H-ADAPTERS-01 — Point it at the pool that already exists
**Who:** a backend engineer adding vv to a service that has been running for two years
**Wants:** repositories over the `*sql.DB` in `main`, without changing how it is opened, sized or closed
**Story:** They have a `*sql.DB` built from their own config, a driver blank-imported at the top of `main`, and a `defer db.Close()`. They want one line that turns it into something a repository can bind to, and they want to be sure nothing in the library opens a second connection or closes theirs.
**Must hold:**
1. Binding is one expression. No builder, no `Init`, no registration step.
2. The library never opens a connection and never closes the one it was given.
3. The pool's own settings — max open, max idle, lifetime — are the ones that apply.
4. Any driver registered with `database/sql` executes.
5. Where a driver changes what a call *returns* — a failure or a success — that is written down.
**Today:** ✅ ready for 1–4, ❌ for 5
**Evidence:** `crud/adapter/crudsql/crudsql.go:159-162` — `Postgres`, `MySQL`, `MariaDB` and `SQLite` each take the `*sql.DB` and a variadic `Option`, and nothing else; there is no second required argument and no lifecycle call. The package is 270 non-test lines across two files and holds no pool of its own: `Executor.Exec` at `:88` and `Query` at `:104` call straight through to the handle, and nothing in either file calls `Open`, `Close` or `SetMaxOpenConns`. Point 4 is structural — the package imports `database/sql` and no driver, and the root module takes no third-party requirement at all ([[D-036]], held by `make check-deps`). Point 5 is where it stops: execution is driver-agnostic and *everything else* is not. The failure half is H-ADAPTERS-03's and is at least written down in `docs/ai/flows/FL-014-a-driver-error-becomes-a-public-violation.md:182-186`; the success half — `RowsAffected`, `LastInsertId`, `Rows.Err` and how a `DATETIME` arrives — is H-ADAPTERS-16's and is written down nowhere. All nine runnable examples are the one expression, in one of three spellings: `_examples/sql-nethttp/main.go:83`, `_examples/gorm-mysql-gin/main.go:102`, `_examples/auth-jwt-gin/main.go:148`. [[D-057]] is why the line is the consumer's to write.
**If not ready:** n/a for 1–4. For 5, read H-ADAPTERS-03 and H-ADAPTERS-16 before choosing a driver, because that choice is made in `main` and is not cheap to revisit.

### H-ADAPTERS-02 — Which databases does this actually reach
**Who:** a team on SQL Server, Oracle or CockroachDB, deciding whether to evaluate this at all
**Wants:** a straight answer, and a route if the answer is no
**Story:** They read the README, see "any driver registered with `database/sql`", wire their driver, declare a model and hit a duplicate key. The statement ran. The 409 came back with no code on it, and nothing said why.
**Must hold:**
1. An engine nobody measured degrades explicitly, not quietly.
2. There is a route for a fifth engine, and it is walked somewhere.
3. An engine that is *nearly* one of the four is not silently treated as that one.
**Today:** 🟡 partial
**Evidence:** Four engines, and the constructors are the whole list: `crud/adapter/crudsql/crudsql.go:159-162` plus `crudpgx`. A fifth reaches execution through `crudsql.Open(db, myDialect)` — `crud.Dialect` is an ordinary interface and three of its four optional companions are written so that "a dialect written outside this package keeps compiling" (`crud/dialect.go:29`, `:43`, `:58`). Point 1 holds at the classifier: `sqlfault.New("mssql")` is accepted and then answers false for everything, and `TestAnUnknownDialectStillAnswersTheIntegrityGate` (`crud/sqlfault/classify_test.go:288`) pins `New("cockroach")` producing the sentinel and no code, deliberately. Point 3 is where it slips. `crud/dialect.go:65` says "Postgres targets PostgreSQL (and CockroachDB)", so a CockroachDB consumer is routed through the PostgreSQL SQLSTATE table with no constructor, no captured corpus and no test — MySQL-with-a-footnote, which is the exact shape [[D-046]] refuses for MariaDB. `docs/roadmaps/Roadmap.md:94` lists all three as open and names what each needs. Point 2 has no worked example anywhere: `crudsql.Open(db, yourDialect)` is unmentioned in both adapter docs as an escape hatch, and no test or example writes a `crud.Dialect` outside the package.
**If not ready:** A fifth engine is a hand-written `crud.Dialect` and a hand-written `errs.Classifier`, with no worked example in the tree — H-FAULTS-20 measures that half and rates it the same way. The cheap part is one sentence per adapter doc naming `Open(db, yourDialect)` as the route, and either dropping CockroachDB from `crud/dialect.go:65` or saying there that the dialect is shared and the error codes are not. Where the supported list itself belongs is H-ADAPTERS-05's table; writing it twice is how the two go out of step.

### H-ADAPTERS-03 — Our two-year-old service is on lib/pq
**Who:** the same engineer as H-ADAPTERS-01, whose `main` has `_ "github.com/lib/pq"` at the top
**Wants:** the field-level 409 the docs promise
**Story:** They wire `crudsql.Postgres(db)`, provoke a duplicate email, and get a 409 with `unique` on it. It looks right. Months later somebody asks why the form never highlights the field, and the answer is the driver they chose before they had heard of this library.
**Must hold:**
1. The constraint, table, schema and column a violation names reach the fault.
2. If the driver in use cannot supply them, the consumer is told — at the doc, not by absence.
3. Any driver the library's own documentation recommends supplies them.
**Today:** ❌ missing
**Evidence:** `crudsql` reaches the driver's structured fields **by shape**, against a fixed list of spellings — `crud/sqlfault/extract.go:35-38` is `ConstraintName`, `TableName`, `SchemaName`, `ColumnName`, `DataTypeName`, `Detail`, `Hint`. lib/pq spells four of those seven without the `Name` suffix (`Constraint`, `Table`, `Schema`, `Column`), so exactly the four that matter are lost and the three that cross are the two [[D-039]] forbids classifying on plus a type name. That is deliberate and pinned: `crud/adapter/crudsql/conflict_test.go:353-363` defines a `pqish` stand-in whose comment says "No capture exists for it, so nothing here reads them — deliberately", and `:396-405` asserts as a control that a lib/pq-shaped error yields `Constraint`, `Table`, `Schema` and `Columns` all empty while still classifying. It cascades: `sqlfault.FromCatalog` looks up `ConstraintColumns(table, constraint)` (`crud/sqlfault/catalog.go:32`) and returns `nil, false` on a miss (`:32-38`), so on lib/pq the catalog fills nothing and the composite-unique story is unreachable however carefully it is wired. Point 2: `FL-014` says it where a flow reader would find it and no adapter doc does. Point 3 fails outright: `docs/modules/en/vvdb.md:90` tells the consumer "On lib/pq set `driver: postgres`", and `utils/vvdb/open.go:13` names lib/pq as one of three suggested imports. Nothing measures it — no test drives the real driver; `pqish` is a hand-written three-field fake, `test/go.mod` requires pgx and go-sql-driver/mysql, and the corpus records its driver as `github.com/jackc/pgx/v5/stdlib` (`errs/sqlerr/testdata/corpus/postgres.json:4`).
**If not ready:** The consumer's fix is one line — blank-import `github.com/jackc/pgx/v5/stdlib` and change the driver name — which is exactly why not saying so is expensive. What is missing here is a sentence in `docs/modules/en/crudsql.md` saying a lib/pq violation carries its code and no constraint, table, schema or column. The matching row in `vvdb.md:90` is `utils/vvdb`'s doc and belongs to that sweep, or the same fix gets written twice and applied once. Reading lib/pq's own spellings would need a capture from a real lib/pq error, which [[D-046]] requires and nobody has taken.

### H-ADAPTERS-04 — The same repository straight onto a pgx pool
**Who:** an engineer on a new PostgreSQL service who chose pgx and no ORM
**Wants:** vv on `*pgxpool.Pool` with no `database/sql` in the path
**Story:** They open a pool, bind a repository to it, and serve. Later a colleague adds sqlc for two reporting queries and hands the same `pgx.Tx` to both libraries.
**Must hold:**
1. `*pgxpool.Pool`, a single `*pgx.Conn` and a live `pgx.Tx` are all bindable, with the same call — and stay bindable across a pgx minor release.
2. The pool's own configuration — query exec mode, statement cache, `after connect` hooks, a tracer — is what runs. The adapter sets none of it.
3. Rows cross the boundary without being copied into an intermediate shape.
4. A statement PostgreSQL refuses comes back classified, with no wiring asked for: pgx speaks to one engine and there is nothing to declare.
5. The same handle satisfies sqlc's interface and vv's at once.
**Today:** 🟡 partial — 2 to 5 hold, 1 is claimed and unpinned
**Evidence:** `crud/adapter/crudpgx/crudpgx.go:28-31` — `Queryer` is pgx's own `Exec`/`Query` pair, so all three handle types satisfy it structurally; `Open` at `:147` is the whole adapter. Point 2: `rg -n "pgxpool" crud/adapter/crudpgx/` returns three comment lines and no code, so nothing the pool was configured with is overridden — which is also what makes H-ADAPTERS-19 work. Point 3: `rows` at `:110-113` embeds `pgx.Rows` and overrides one method. Point 4 is `faults` at `:66` defaulting to `sqlfault.New("postgres")` with the typed extractor — the one place in the tree where an engine is a literal with no ambiguity to refuse. The comment at `:104-109` names the failure it was written for: pgx does not report a refused statement from `Query`, it reports it from `Err`, and before the wrapper a duplicate key was a bare 500 through pgx and a 409 through `database/sql`. Point 5 is `TestSqlcPgx` in `test/integration/driver_sqlc_test.go:87`. Point 1 rests on the doc comment at `:27` and nothing else: the package has no `var _ Queryer = ...` for any of the three, and a `*pgx.Conn` is bound nowhere in the tree — the suite uses `pgPool` and transactions from it (`test/integration/main_test.go:41`). A pgx signature change breaks the consumer's build and not ours.
**If not ready:** Two of the three assertions are free — `var _ Queryer = (*pgx.Conn)(nil)` and `var _ Queryer = (pgx.Tx)(nil)`, in a package that already imports `pgx`. The third is not: `pgxpool` is imported nowhere in `crudpgx` today, and naming it promotes `github.com/jackc/puddle/v2` and `golang.org/x/sync` from graph entries in `crud/adapter/crudpgx/go.sum:10-11,21-22` to requires in a published satellite's `go.mod`. That is a dependency decision for the module ([[D-051]]), not a no-op, and it should be taken deliberately or the pool left to an integration-suite binding instead. The repository already uses the technique for the harder case: `docs/usage-guides/ent.md:505-507` carries `var _ crudsql.Queryer = (*ent.Client)(nil)`.

### H-ADAPTERS-05 — Our driver is pgx and our ORM speaks `database/sql`
**Who:** an engineer on the most common Go PostgreSQL stack, choosing an adapter in `main`
**Wants:** to know which of the two adapters is theirs, and what the other one would have given them
**Story:** They picked pgx for the driver. They also run gorm, or ent, or sqlx, which want a `*sql.DB`. They open the pool through `pgx/v5/stdlib`, bind vv, and assume that choosing pgx meant choosing the pgx adapter.
**Must hold:**
1. Which adapter goes with which handle is stated before the model is declared.
2. What the other adapter would have given up is stated with it.
3. Holding both a `*pgxpool.Pool` and a `*sql.DB` over one physical database is described, including what breaks.
**Today:** 🟡 partial — 1 holds, 2 and 3 are missing
**Evidence:** Point 1 is answered, in three places: `README.md:1337-1338` maps `*sql.DB`/`*sql.Tx`/`*sql.Conn` to the `crudsql` constructors and pgx's three handle types to `crudpgx.Open`/`From`; `docs/modules/en/crudsql.md:48-55` carries the same stack table; `docs/modules/en/crudpgx.md:33-39` names the pgx three. Point 2 is answered nowhere — none of those tables says what the *other* column costs. The evidence that it matters is the examples: four of the nine are named `*-pgx-*` and every one binds **`crudsql`** — `_examples/gorm-pgx-fiber/main.go:90`, `_examples/sqlx-pgx-gin/main.go:83`, `_examples/ent-pgx-gin/main.go:79`, `_examples/ent-pgx-fiber/main.go:81`, each with `_ "github.com/jackc/pgx/v5/stdlib"` at the top. A consumer who reads "I chose pgx" off those examples is on the adapter with no `COPY` (H-ADAPTERS-11), no `Rows.Err()` classification (H-ADAPTERS-16) and no code on a joined transaction (H-ADAPTERS-10) — this file's three sharpest adapter findings, and nothing tells them the findings are theirs. Point 3 is worse and is structural: `crud.KeyOf` (`crud/executor.go:414`) reduces a source to the raw handle, so a `*pgxpool.Pool` and a `*sql.DB` over the same server are two different keys. A repository on `crudpgx` therefore cannot join a transaction the ORM opened on the `*sql.DB` at all, and `catalog.Set` would hold two catalogs for one database ([[D-041]], `crud/catalog/set.go:46-80`).
**If not ready:** The table exists and is missing its third column: what you give up. `COPY` is the largest entry and it is a reason to choose `crudpgx` before the model is declared — swapping adapters afterwards is a one-way door in `main`, because every join site, every scoped binding and the catalog key change with it. One sentence has to say that two handles over one database are two databases as far as this library is concerned.

### H-ADAPTERS-06 — Write inside the transaction the ORM already opened, and the other direction
**Who:** an engineer with a service method already wrapped in `db.Transaction` or `client.Tx`, and later a team standardising on `repo.Tx` as the outer boundary
**Wants:** vv's write to be part of that transaction, and eventually the ORM's write to be part of vv's
**Story:** The method loads a team with gorm, updates the members through a vv repository, then writes an audit row with gorm again. It returns an error halfway. Nothing may survive. A year later the same team inverts it: `repo.Tx` opens the transaction and the ORM joins.
**Must hold:**
1. Joining is one line inside the transaction the framework already gave them.
2. Everything the repository does with that context runs on that transaction — no second connection, no separate commit.
3. The two libraries read each other's uncommitted writes.
4. An error returned to the framework's transaction wrapper rolls back the vv writes too.
5. The pattern is the same shape whichever framework it is; adopting a new one is finding where it keeps the handle.
6. A nested `Tx` inside the borrowed one joins rather than opening a second transaction the outer owner cannot control.
7. The reverse works: an ORM write inside a vv-owned transaction rolls back with it.
**Today:** 🟡 partial — 1 to 6 hold and are the best-proven thing here; 7 is shipped and unproven
**Evidence:** `crudsql.From` at `crud/adapter/crudsql/crudsql.go:75` takes anything with `ExecContext`/`QueryContext`, which is what gorm's `Statement.ConnPool`, sqlx's handles, `*ent.Tx` under `--feature sql/execquery` and a bare `*sql.Tx` all are. Proven per driver against live databases: `TestGormSharedTransaction` and `TestGormRollbackTakesVVWithIt` in `test/integration/driver_gorm_test.go:66,114`; `TestEntSharedTransaction` and `TestEntRollback` in `driver_ent_test.go:86,154`; `TestSqlxSharedTransaction` in `driver_sqlx_test.go:36`; `TestDatabaseSQLSharedTransaction` in `driver_sql_test.go:46`; `TestPgxSharedTransaction` in `driver_pgx_test.go:20`. Point 6 is asserted inside `TestAnEntTransactionJoinsButCannotOpenASavepoint` at `driver_ent_test.go:181`, which carries its own control: it fails deliberately if an ent transaction ever starts offering `Begin`, so the test cannot quietly stop proving what it says. Point 7 is what `crud/adapter/crudsql/crudsql.go:205` and `crudpgx.go:162` exist for — "Tx returns the underlying `*sql.Tx`, e.g. to hand it to another library", "e.g. to hand to sqlc" — and `docs/ai/usecases/Index.md:207-211` records it as open: "No test writes through an ORM *inside* a repository-owned transaction and shows the rollback taking the ORM's write."
**If not ready:** For 7, the consumer writes it and finds out in production. It is two tests, one per adapter, in the files that already hold the forward direction. The cost the guides are honest about for the forward half: to make the ORM's *source* able to open its own transactions you write a wrapper — `docs/usage-guides/ent.md:483`, shown in full at `:502-537`, calls it worth twenty lines and shows all twenty. That wrapper is also where the classifier is lost (H-ADAPTERS-10) and where handle identity is lost if it omits `UnwrapSource` (H-ADAPTERS-12).

### H-ADAPTERS-07 — One flow needs SERIALIZABLE and the other four hundred do not
**Who:** an engineer writing a stock reservation, and later a bulk importer
**Wants:** `SERIALIZABLE` for the flow that needs it, without the rest of the service paying for it, and a sub-step that can roll back without killing the whole batch
**Story:** The reservation reads a count and writes a decrement, and two of them must not both commit. Everything else in the service is fine at `READ COMMITTED`. Separately, an importer processes rows in a transaction and wants one bad row rolled back without losing the nine hundred before it.
**Must hold:**
1. One flow can ask for an isolation level on the call it already writes.
2. A read-only transaction can be asked for.
3. Asking for either and having the driver drop it is distinguishable from asking for something that works.
4. A nested `Begin` gives a savepoint, and rolling it back leaves the outer transaction usable.
5. Whatever the answer is, it is the same on both adapters, because the engine is the same engine.
6. A nested `Begin` on a handle somebody else's transaction gave us behaves the same on both adapters.
**Today:** ❌ for 1, 🟡 for 2 and 3, ✅ for 4, ❌ for 5 and 6
**Evidence:** Point 1 fails on both adapters, but not equally, and round 1 of this file over-stated the `database/sql` half. Isolation there is a property of the source: `crudsql.DB.WithTxOptions` at `crud/adapter/crudsql/crudsql.go:174` returns a copy and `Begin` at `:177` reads `d.TxOptions`. The copy keeps the same `*sql.DB` in `Executor.q`, so `DataSource()` (`:86`) answers the same handle, `crud.InTx` binds on `ownScope(src)` → `identityOf` → that handle (`crud/executor.go:521`, `:439-441`, `:448-459`), and `bindingFor` matches every repository already bound to the pool (`:388-399`). So the real cost on `database/sql` is one extra source value used as the `InTx` argument — not a second `Bind` per repository — and **nothing proves it**: no test joins across two source values over one handle. On pgx there is nothing to ask with: `Executor.Begin` at `crud/adapter/crudpgx/crudpgx.go:128-141` asserts `Begin(ctx) (pgx.Tx, error)` and calls it, `pgx.TxOptions` appears nowhere in the package, and the exported surface is `Open`, `From`, `WithFaults`, `Queryer`, `Executor`, `Tx` and `Option` (`docs/api/surface.md:747-756`). Point 2 splits the same way: `WithTxOptions` takes a whole `*sql.TxOptions`, `ReadOnly` included, so it can be asked for on `crudsql` and not on pgx. Point 3 is unmeasured for read-only — nothing in the tree sets `ReadOnly: true`, and the only `sql.TxOptions` under `test/integration` is `edge_test.go:1238`, which sets `Isolation` alone. That matters because of what that test's own comment says at `:1231-1233`: "asking for one that the driver then drops looks exactly like asking for one that works" — the same argument that made the isolation test observe a consequence leaves read-only unobserved. Point 4 holds on both: `TestPgxNestedSavepoint` (`driver_pgx_test.go:69`), `TestDatabaseSQLSavepoint` (`driver_sql_test.go:82`), `TestSQLiteSavepointRollsBackWithoutLosingTheTransaction` (`driver_sqlite_test.go:87`). Point 6 diverges and nothing collects it: `crudpgx.From(tx)` is a `crud.Beginner` (`crudpgx.go:128`, asserted at `:166`), so a nested `Begin` on a joined pgx transaction opens a savepoint, while `crudsql.From(tx)` returns a bare `Executor` and the assertion block at `crudsql.go:245-253` claims `Beginner` for `DB` and `*Tx` only — so the same call answers `crud.ErrNoTxSupport`. An importer ported from pgx to `database/sql` loses per-row savepoints at run time.
**If not ready:** On `database/sql`: `serial := crudsql.Postgres(sqlDB).WithTxOptions(&sql.TxOptions{Isolation: sql.LevelSerializable})`, kept beside the ordinary source and passed to `crud.InTx` for that one flow. One value, one line, and no test says it joins. On pgx: `pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})`, `crudpgx.From(tx)`, push it into the context, own the commit and the rollback by hand — six lines and a `defer` replacing one. Process-wide, a PostgreSQL consumer can set `default_transaction_isolation` in the DSN or in `pgxpool.Config.ConnConfig.RuntimeParams` and it reaches every `repo.Tx`, because the adapter reads no pool configuration — **except behind a connection pooler in transaction-pooling mode**, where startup parameters and session state are the first thing PgBouncer takes away, which is the deployment `docs/ai/usecases/modules/vvdb/Vvdb.md:267` already treats as first-class. Whichever route is taken, the retry question is still open and is H-ADAPTERS-18's.

### H-ADAPTERS-08 — Test the repository code against a real database
**Who:** anyone writing the first integration test for their own service
**Wants:** each test in a transaction that is rolled back at the end, so tests leave nothing behind
**Story:** They open a `*sql.DB` in `TestMain`, and each test begins a transaction, binds a repository to it, runs the code under test, and rolls back. It is the default Go harness for repository code. Then a test calls a service method that opens its own transaction.
**Must hold:**
1. A repository can be bound to a transaction handle, not only to a pool.
2. Code under test that opens a transaction still runs — the harness must not change what the code does.
3. What the harness gives up compared with binding the pool is written down.
4. It is the same harness on both adapters.
**Today:** 🟡 partial on pgx, ❌ on `database/sql`
**Evidence:** Point 1 holds on both. On `database/sql` the natural binding is `crudsql.Source(tx, crud.Postgres{})` (`crud/adapter/crudsql/crudsql.go:132`), which the module doc sends a whole class of consumer to: "for when a framework hands you a handle rather than a `*sql.DB`" (`docs/modules/en/crudsql.md:78-81`). Point 2 then fails there. `source` embeds `Executor`, which has no `Begin`, and the assertion block at `crudsql.go:245-253` never claims `crud.Beginner` for `source{}` — so every path under test that calls `repo.Tx` or `crud.InTx` answers `crud.ErrNoTxSupport` (`crud/executor.go:507-509`). Point 3 fails too, and the second half of it is H-ADAPTERS-10's defect met in the consumer's test suite rather than in production: `Source` names no engine, so every conflict assertion in the harness is a code-free 409. On pgx the identical harness works: `crudpgx.From(tx)` is a `Beginner` and a nested `Begin` is a savepoint (`crudpgx.go:128-141`). No test in this repository binds a repository to `crudsql.Source` at all — the five uses in `test/integration/corpus_test.go:186-222` all call `.Exec` directly — so the gap is not measured here either.
**If not ready:** Today the `database/sql` consumer either truncates between tests instead of rolling back, or wraps their `*sql.Tx` in the twenty-line wrapper from `docs/usage-guides/ent.md:502-537` to give it a savepoint-issuing `Begin`. For unit tests with no database at all the answer is good and is elsewhere: `crud/crudtest` is a `crud.Source` that is also a `crud.Beginner` (`crud/crudtest/recorder.go:161`, asserted at `:252`). What closes this one is a `Begin` on `crudsql.Source` that issues `SAVEPOINT` — `Tx.Begin` at `crudsql.go:208-214` already writes that code — plus a line in the module doc saying `Source` names no engine.

### H-ADAPTERS-09 — The duplicate email is a 409 that names the field
**Who:** an engineer on the signup endpoint
**Wants:** the unique violation to reach the client as a conflict with `email` on it, not a 500
**Story:** Two people register with the same address. The API must answer 409 with a code the client branches on and a field the form can highlight. Later a second constraint is added over `(tenant_id, email)` and PostgreSQL names only the constraint, not the columns.
**Must hold:**
1. Naming the engine is a declaration the consumer makes once, at the constructor.
2. Adding the catalog to that declaration does not remove anything the constructor already gave.
**Today:** ✅ for 1, ❌ for 2 — and the route a consumer walks to get there is the faults sweep's
**Evidence:** Point 1 holds: `crudsql.Postgres/MySQL/MariaDB/SQLite` write the engine string as a literal at `crud/adapter/crudsql/crudsql.go:159-162` and hand it to `sqlfault.New`; `crudpgx` gets it for free at `crudpgx.go:66`, which is the one place in the tree with no ambiguity to refuse. Both adapters route through the same `sqlfault.Wrap`, and both files' comments say why: "the last time one rule lived in both adapters they diverged" (`crud/adapter/crudpgx/conflict.go:17-20`). Point 2 fails on `crudpgx` and is already owned: `faults` seeds the default as `sqlfault.New("postgres", sqlfault.WithExtractor(...extract))` and then lets `WithFaults` overwrite the whole value (`crudpgx.go:60,66-74`), so the documented catalog snippet at `docs/modules/en/crudpgx.md:81-85` silently drops the typed `*pgconn.PgError` reader the package comment says exists to prevent a silent blank (`conflict.go:23-26`). **H-FAULTS-28 states that case and counts its blocker; it is not counted again here.** The mechanism itself is proven live with two controls — `TestACatalogFillsTheColumnsAUniqueViolationDoesNotName` (`test/integration/corpus_test.go:569`) asserts `Source.Columns == [a b]` in key order, with a control showing the identical live error and no catalog leaves the list nil — and the three-statement recipe both adapter docs ship (`crudsql.md:98-102`, `crudpgx.md:81-85`), including the `cat, _ := catalog.Load(...)` that discards an error `crud/catalog/load.go:21-24` says must not be discarded, is H-FAULTS-01's and H-FAULTS-08's to fix.
**If not ready:** The constructor that collapses the recipe is `crudsql.Introspect` / `crudpgx.Introspect`, specified in full by the faults sweep (H-FAULTS-01's DX section) and adopted here rather than re-specified — round 1 of this file proposed a `WithCatalog(ctx)` `Option` instead, which cannot work, because `Option` is `func(*config)` (`crudsql.go:49`) and cannot carry an error. What is this module's and is not fixed anywhere: the pgx twin has no receiver yet, because `crudpgx.Open` returns `Executor` and there is no `crudpgx.DB` (`crudpgx.go:147`). See the DX section.

### H-ADAPTERS-10 — The same 409, inside the borrowed transaction
**Who:** the same engineer, whose signup now runs inside the ORM's transaction
**Wants:** the error body not to change because the write moved inside a transaction
**Story:** Signup grew a second write, so it went inside `db.Transaction`. The duplicate-email test still passes — it is still a 409 — but the response body lost `"error_code": "unique"` and the front end's branch stopped firing.
**Must hold:**
1. A conflict inside a joined transaction carries the same code as the same conflict outside it.
2. Saying which engine it is does not mean restating it at every join site.
3. Whatever the consumer is told about the degradation is true.
**Today:** ❌ missing on `database/sql`; ✅ on pgx
**Evidence:** `crudsql.From` at `crud/adapter/crudsql/crudsql.go:75-77` passes `classifier(nil, opts)` — no engine, no classifier. This is deliberate and pinned: `TestOnlyADeclaredEngineProducesACode` in `crud/adapter/crudsql/classify_test.go:25` runs one duplicate-key error through all six constructors and asserts a fault comes back from `Postgres` and from `From(..., WithFaults(...))` and from nothing else, with `From` named in the table as "the joined-transaction path". The reasoning is [[D-046]]: `crud.MySQL` is MariaDB too, and guessing would answer "mysql" for a MariaDB server. That reasoning is right, and it does not follow that a classifier cannot ride from a source the consumer *did* name. `crudpgx.From` at `crudpgx.go:77` does exactly that — it defaults to the postgres classifier — because pgx has one engine. Point 2 is what actually fails: there is no spelling that carries the declaration, so the engine string is restated at every join site and any site missed is a silent downgrade. Round 1 of this file said the consumer is told "a thousand lines away"; that was wrong and the correction matters, because the telling is fine and the mechanism is what is missing. It is said in four places, one of them nine lines above the snippet a reader copies: `README.md:1346-1354` immediately before the join block at `:1358-1360`; `docs/modules/en/crudsql.md:85-92` under its own "Codes on a joined transaction" heading; `From`'s own doc comment at `crudsql.go:70-77`, with the correct spelling verbatim at `:53-58`; and both usage guides. Point 3 is where the documentation is wrong rather than distant: `docs/usage-guides/gorm.md:1220` and `docs/usage-guides/ent.md:1301` both say "Where it does not, the 409 carries the driver's own sentence, constraint name included." It does not. [[D-044]]'s invariant forbids it and `port/porthttp/render.go:123-148` builds the body from `port.FaultOf` and `port.Violations` with `err.Error()` nowhere in the path — so the joined-transaction conflict loses the code *and* the sentence, and the guides tell the reader the loss is cosmetic. And no runnable example joins a transaction at all: `rg -n "WithExecutor|crudsql.From|crudpgx.From|InTx" _examples/` finds one prose comment (`_examples/gorm-pgx-fiber/main.go:88`) and no code.
**If not ready:** The consumer restates the engine at every join site. The fix is reading the declaration back off the source that holds it; `sqlfault.Classifier.Engine()` already exists (`crud/sqlfault/classify.go:54-60`), and the faults sweep independently names that accessor as the piece all three sweeps need first — its own smaller form is `crudsql.WithFaultsFrom(src)`. Both guides' stale sentence is a two-line fix and they are parallel by design, so it is two files.

### H-ADAPTERS-11 — The nightly import
**Who:** the author of a supplier-feed job
**Wants:** a quarter of a million rows in, without a forty-minute round-trip loop
**Story:** They parse a CSV into `[]*Product` and want them in the table. `SaveAll` is one `INSERT` per batch and is fine at a thousand rows; at 250,000 they reach for `COPY`, which is the reason they chose pgx. The job writes a manifest row afterwards, so the whole thing runs inside `repo.Tx`.
**Must hold:**
1. `COPY` is reachable from the source the repository is bound to — including after a replica or an instrumenting wrapper is added.
2. The columns and the values come from the model that was already declared — a second, hand-kept list of column names is a bug waiting for the next migration.
3. A `COPY` called inside a transaction is inside it, or the call says loudly that it is not.
4. A row the server refuses comes back the same way it would from the row-at-a-time loop.
5. What the fast path skips is stated where it is called, not in a reference doc: the tenant scope, the soft-delete filter, the version counter, any hook.
6. A handle with no `COPY` says so in words about `COPY`.
7. A consumer on `database/sql` over pgx is told whether this is reachable for them at all.
**Today:** ❌ missing — only point 1's narrowest reading holds
**Evidence:** Point 1 holds for a source that is literally `crudpgx.Open(pool)` and for nothing else. `crud.BulkInserter` is the one optional interface in the library with **no walker**: `crud/executor.go` carries `SourceOf` (`:195`), `BeginnerOf` (`:254`), `ReadSourceOf` (`:268`) and `KeyOf` (`:414`) and no fifth, and [[D-061]]'s own table lists only `Nexter` and `SourceUnwrapper` as walkable. The documented call is a bare assertion — `if bulk, ok := src.(crud.BulkInserter); ok` in the library's own doc comment at `crud/executor.go:130-139` and again at `docs/modules/en/crudpgx.md:59-63` — and the only test does `any(src).(crud.BulkInserter)` on a bare `Open(pool)` (`test/integration/driver_pgx_test.go:110-112`). So `crud.ReadWrite`, whose `readWrite` embeds the `Source` **interface** (`crud/executor.go:98-101`) and therefore promotes only `Source`'s three methods, deletes `CopyFrom` today; so does any consumer wrapper written to time statements. The failure is `ok == false`, silently, at run time — and `SourceUnwrapper`'s own doc comment at `:211-219` lists the three things a wrapper loses without mentioning this fourth one. Point 2 fails: the signature is `(table string, columns []string, rows [][]any)` (`crud/executor.go:137-139`) and nothing derives any of the three from the model — `TestPgxBulkCopy` hand-writes both, with `age` written as a bare `20` where the model field is a `crud.Opt[int]` (`test/integration/model.go:24`). Point 3 fails and is the one that loses data: "The call runs on the handle that executor holds and ignores any transaction in the context" (`docs/modules/en/crudpgx.md:65-66`, and [[UC-008]]'s Out of scope at `docs/ai/usecases/modules/sqlrepo/UC-008-write-many-rows-in-one-statement.md:54-57`). Point 4 fails and nothing anywhere says so: `Executor.CopyFrom` (`crudpgx.go:119-126`) returns the driver's error straight out and is the **only** statement path in that file that does not call `e.conflict` — so one duplicate SKU in the feed comes back as a bare `*pgconn.PgError`, not `crud.ErrConflict`, no `errs.Fault`, no code, and the status table above it answers 500 where the row loop answered 409. Point 5 is stated nowhere at all, and the call site is a type assertion the application writes, so there is nothing there to state it. Point 6 fails: an unsupported handle answers `crud.ErrNoTxSupport` (`crudpgx.go:122`), whose message is "crud: executor cannot begin transactions" (`crud/errors.go:13`). Point 7 fails: there is a route on `database/sql` over pgx — `(*sql.Conn).Raw` down to the `*pgx.Conn` underneath, which pgx's own `stdlib` package documents — and nothing in this repository names it (`rg -ni "CopyIn|\.Raw\("` outside `crud/` finds nothing).
**If not ready:** Today they write the column list twice — once as `db` tags, once as a `[]string` — rebuild each row into `[]any` in that order by hand, must not add a replica or a wrapper afterwards, must not run it inside the job's transaction, and must not rely on the error. The bypass is the sharper half: a repository bound with `security.Gate` narrows every write it makes, and `CopyFrom` sits below the repository entirely, so the first bulk import in a multi-tenant service writes rows past its own tenant scope with nothing in the way. `SaveAll`'s own limits are H-SQLREPO-06's, not this file's.

### H-ADAPTERS-12 — More than one handle
**Who:** an engineer whose service grew an events database
**Wants:** each repository on its own handle, and no write ever landing in the wrong one
**Story:** They add an analytics database for an event log. The existing service method opens a transaction on the main database with gorm and joins vv to it — and now that join must not capture the event write.
**Must hold:**
1. Joining a transaction can be scoped to the database it belongs to.
2. A repository bound elsewhere keeps running on its own handle inside that scope.
3. Naming the handle and naming any source over that handle are the same statement — including the handle a consumer is actually holding.
4. A transaction vv opens is scoped the same way, with nothing written by the consumer.
5. Getting the scoping wrong is loud.
6. The answer is the same on both adapters.
**Today:** 🟡 partial — 1, 2 and 4 proven on `crudsql`/PostgreSQL; 3 holds only for the exact value passed in; 5 fails; 6 unmeasured
**Evidence:** `crud.WithExecutorFor` at `crud/executor.go:335-337` keys on `crud.KeyOf(ds)`, and both adapters implement `Identified` by handing back the raw handle — `crudsql.go:86`, `crudpgx.go:85` — so two independently built sources over one pool are one database. Proven with two live databases: `TestAScopedExecutorKeepsEachRepositoryOnItsOwnDatabase` (`test/integration/multidb_test.go:171`) and `TestARepositoryTransactionDoesNotCaptureAnotherDatabase` (`:206`), with `TestAnUnscopedExecutorAdoptsEveryRepositoryIncludingTheWrongOne` at `:135` as the control that keeps them honest. Every source in those tests is `crudsql.Postgres` — `rg -n "crudpgx" test/integration/multidb_test.go` returns nothing — and `docs/ai/usecases/Index.md:241-246` records exactly that: "Scoped bindings with pgx, and any combination across two engines, are untested."

Point 3 holds for the value passed to the constructor and not for the value the consumer holds. On every ORM stack the thing in hand is a `*gorm.DB`, a `*sqlx.DB` or an `*ent.Client` while the constructor was given the `*sql.DB` underneath — `_examples/sqlx-pgx-gin/main.go:83` binds `crudsql.Postgres(db.DB)`. `crud.WithExecutorFor(ctx, sqlxDB, e)` compiles, reads correctly, and matches nothing: `crud.SameDataSource` (`crud/executor.go:477-490`) answers false on a type mismatch rather than complaining. The same shape arrives through the wrapper `docs/modules/en/crudsql.md:44-46` invites — a consumer `Source` wrapper that omits `UnwrapSource` ends the walk in `identityOf` (`crud/executor.go:448-459`, `:230-246`), `KeyOf` falls back to the wrapper value, and every scoped binding over that handle stops matching. An uncomparable wrapper is refused outright by `catalog.Set.Load` with `ErrUncomparableHandle` (`crud/catalog/set.go:46-50`), which is the loud half; the executor half is silent.

Point 5 fails, and the adapter is what creates the failure: `Executor.DataSource()` returns `e.q`, so for `crudsql.From(tx)` the identity is the `*sql.Tx`. A consumer who writes `crud.WithExecutorFor(ctx, tx, ...)` — naming the transaction rather than the database — gets a binding no repository bound to the pool matches. It is ignored, the write goes to the pool outside the transaction, and it reports success. `docs/ai/usecases/Index.md:262-266` files it as needing a decision. The adapter residue on point 6: `crud.ReadWrite` over two `crudpgx.Executor` values is not only untested, it silently removes `CopyFrom` — see H-ADAPTERS-11 point 1.
**If not ready:** The plain `WithExecutor` captures everything, on purpose ([[D-009]]), and the failure it produces is a write in the wrong database reporting success ([[D-027]], still open). The mis-keyed scoped binding produces the same shape one level in, and so does the ORM-handle spelling, which is the one a consumer is far more likely to write. All three are closed by making the join a method on the value that already knows which database it is; see the DX section, which also names what that costs against D-009. The replica half of multi-handle routing is H-CRUD-14's and H-SQLREPO-11's, and neither `crud.ReadWrite` nor `Options.Primary` is adapter code.

### H-ADAPTERS-13 — MySQL today, MariaDB next quarter, SQLite in the unit tests
**Who:** an engineer at a company that runs MySQL and is evaluating MariaDB, whose test suite must not need Docker
**Wants:** one line changed per target, and error codes that are right on each
**Story:** They ship on MySQL. The platform team moves to MariaDB. Meanwhile their package tests run against an in-process SQLite so CI does not start a container. Then a DBA files a ticket: every upsert emits the deprecated `VALUES()` form and MySQL 8.0.20 warns about it.
**Must hold:**
1. Changing engine is one line at the binding and nothing in the model or the repository.
2. MariaDB is a target in its own right, not MySQL with a footnote.
3. SQLite works in-process with no container, including savepoints.
4. What the engines genuinely cannot do the same way is refused or documented, not papered over.
5. A dialect setting the library exposes as a caller's choice can be said without giving up the engine declaration.
6. Naming the wrong engine for the server that is answering is detectable.
**Today:** 🟡 partial — 1 to 4 hold, 5 fails, 6 is undetectable by design and unsaid
**Evidence:** `crud/adapter/crudsql/crudsql.go:159-162` — four constructors, one line each, and the comment above them says why the engine string is a literal rather than derived: MariaDB and MySQL "share a driver, a dialect and a wire protocol, and answer a failed CHECK with two different numbers". `TestAMariaDBNumberIsOnlyReadByTheMariaDBConstructor` in `classify_test.go:70` proves both halves — MariaDB's `4025` classifies through `MariaDB()`, and through `MySQL()` the same violation stays a 409 and carries no code, which is the control that makes the constructor's existence provable rather than asserted. The `MariaDB` constructor is driven live in four places (`test/integration/corpus_test.go:446-449`, `edge_test.go:367`, `probe_test.go:98,118`, `vvdb_test.go:49`), and the MariaDB *server* additionally runs the whole conformance suite through the `MySQL` constructor as a fourth target (`driver_sql_test.go:33`) — the two are separate claims and both are covered. SQLite runs the suite in-process via `modernc.org/sqlite` (`driver_sqlite_test.go:40`), and `TestForUpdateIsANoOpOnSQLite` at `:67` is the documented difference rather than a hidden one.

Point 5 is where the ✅ this case carried in round 1 does not survive. All four constructors pass a zero-value dialect (`crudsql.go:159-162`), and `Option` carries a classifier and nothing else (`:47-49`) — there is no `WithDialect`. `crud.MySQL{RowAlias: true}` is documented as a caller's choice ([[D-019]]:31, `docs/modules/en/crud.md:357`), and the only way to say it is `crudsql.Open(db, crud.MySQL{RowAlias: true})`, which is the constructor that names no engine. So the MySQL 8 shop takes the modern upsert form and loses every error code with it, unless they also hand-restate the engine string they had already declared: `crudsql.Open(db, crud.MySQL{RowAlias: true}, crudsql.WithFaults(sqlfault.New("mysql")))`. That is H-ADAPTERS-10's defect arriving at the binding line this case calls solved, and the repository's own tests show the trap rather than the workaround: `test/integration/driver_sql_test.go:37-43` and `dialect_edge_test.go:161-164` both build the row-alias target with `Open` and no classifier, the second with a comment saying so. MariaDB has the mirror trap — the same bool must stay off — and nothing pairs the two.

Point 6: nothing pings, probes a version or warns. A MariaDB server bound with `crudsql.MySQL` runs the whole suite green and quietly loses every MariaDB-only code; this repository does that on purpose at `driver_sql_test.go:33`, and `classify_test.go:81-87` pins that the same violation is then a 409 with no code.
**If not ready:** For 5, either a fifth constructor pair carrying the bool, or a `crudsql.WithDialect(d)` option so the engine declaration and the dialect setting stop being alternatives. For 6, one sentence in the module doc saying the mismatch is undetectable and the platform migration has to change the constructor in the same commit as the server.

### H-ADAPTERS-14 — The column that is not a scalar
**Who:** an engineer on PostgreSQL whose table has `tags text[]` and `metadata jsonb`
**Wants:** those two columns on the model like any other
**Story:** They tag the fields and bind. On pgx it works. Later the same model is bound through `database/sql` for a reporting job and the insert fails with a message from the driver about an unsupported type.
**Must hold:**
1. A column the driver knows how to carry is a field like any other.
2. Where the two adapters differ, the difference is written down in both adapter docs.
3. The supported way to make one work on both is stated once.
**Today:** ❓ unverified
**Evidence:** Nothing found for this. `crud/meta.go:340` maps every exported field, and `isScalarStruct` at `:482` decides only whether a *struct* is one column or something to walk into — a `[]string` or a `map[string]any` is neither a struct nor rejected, so it becomes a column and its value is handed to the driver as-is. That is right for pgx, which encodes `[]string` to `text[]` natively; on `database/sql` the value has to be a `driver.Value` and a bare `[]string` is not, so the same model needs `pq.Array` and there is no seam to put it in. Neither adapter doc mentions the subject and nothing in `test/integration` binds an array or a JSON column — `rg -i "jsonb|text\[\]|pq.Array"` over `test/` finds only a `Raw` predicate that escapes a `?` for jsonb operators (`dialect_edge_test.go:706`). Whether the declaration should *refuse* such a field is `crud/meta.go`'s question and H-CRUD-01's; what is this module's is that the same model binds on one adapter and fails on the other.
**If not ready:** Today the consumer gives the field a type implementing `driver.Valuer` and `sql.Scanner` and it works on both, which is the right answer and is written nowhere. One paragraph in both adapter docs, and one model in the integration matrix carrying an array and a JSON column, would turn an unknown into a known.

### H-ADAPTERS-15 — Session state on the connection the statement runs on
**Who:** a PostgreSQL team that enforces its tenant boundary with row-level security, or sets a statement timeout, or runs schema-per-tenant
**Wants:** `SET LOCAL app.tenant_id`, `SET LOCAL statement_timeout` or `SET search_path` to apply to what vv runs
**Story:** Their middleware acquires a connection, sets the session variables the policy reads, and runs the request on it. They want the repository's statements on that same connection.
**Must hold:**
1. A repository can be bound to one connection, not only to a pool.
2. Which connection a given call runs on is stated.
3. Both adapters answer the same way, or the difference has a reason.
**Today:** 🟡 partial, and the documentation names no working call
**Evidence:** Point 1 works and is undocumented. `crudsql.Queryer` is satisfied by `*sql.Conn`, and the package doc says so — but maps it to a constructor that will not take it: `crud/adapter/crudsql/crudsql.go:6` reads "`*sql.DB, *sql.Tx, *sql.Conn` → `crudsql.Open(db, crud.Postgres{})`", and `Open` takes a `*sql.DB` (`:151`). `*sql.Tx` has a working spelling four lines below at `:10` and in the README cell itself (`README.md:1337` ends "; `crudsql.From(tx)`"). `*sql.Conn` has none anywhere: `crudsql.Source(conn, crud.Postgres{})` is the call that works, and it appears in neither the package doc's table, nor the README's adapter section, nor either module doc's stack table. The route it names also names no engine and cannot open a transaction — H-ADAPTERS-08's and H-ADAPTERS-10's defects, met here at once. On pgx, `crudpgx.Open` takes the `Queryer` interface, so a `*pgx.Conn` binds through the ordinary constructor: one of the two adapters answering a consumer question differently for no stated reason. Point 2: outside a transaction a statement runs on whatever pooled connection the driver hands out, and the faults sweep already measures the sharpest consequence — the probe's own statement resolves through `crud.ExecutorFor` and falls back to the source when there is no transaction (`crud/probe/full.go:119-124`), so the session state does not follow it. That is H-FAULTS-08's second boundary.
**If not ready:** Today an RLS shop either binds a `*sql.Conn` per request and gives up `repo.Tx`, or does the work inside a transaction it owns and joins — which works, and which nothing states. Fixing the package-doc line and adding `crudsql.Source(conn, dialect)` to the two stack tables costs nothing and is the whole of point 1. Whether `security.Gate` and database-side RLS should both be used is `crud/decorators/security`'s question, not an adapter's — H-FAULTS-09 tells that story.

### H-ADAPTERS-16 — The same code, a different driver underneath
**Who:** an engineer whose team standardises the driver a year after standardising the library
**Wants:** the repository to behave the same, or to be told where it will not
**Story:** They swap the driver in `main` — one blank import and one name. The suite is green. Weeks later an optimistic-locked update starts refusing writes nobody else touched, and a `created_at` column comes back as bytes.
**Must hold:**
1. What the adapter needs from a driver beyond `ExecContext`/`QueryContext` is written down.
2. A driver that cannot report affected rows or a generated key does not silently change what a repository call means.
3. A server error raised mid-stream reaches the caller the same way through both adapters.
4. A `time.Time` column works, or the DSN parameter that makes it work is named where a consumer building their own DSN will see it.
**Today:** ❌ missing on all four
**Evidence:** `crudsql.Executor.Exec` (`crud/adapter/crudsql/crudsql.go:88-101`) discards the error from both `res.RowsAffected()` and `res.LastInsertId()` and reports zero and absent. Both are optional in `database/sql`. On a driver without `RowsAffected`, every optimistic-locked update becomes a stale-write refusal — `crud/sqlrepo/repository.go:790-792` turns `RowsAffected == 0` into exactly that — and every `UpdateAll`/`DeleteAll` reports zero touched rows, which [[UC-008]] guarantee 5 tells callers is advisory and which a caller reads as "nothing matched". On a driver without `LastInsertId`, a generated primary key silently stays zero (`repository.go:656` requires `res.HasLastInsertID`). Point 3: `crudsql`'s `rows` (`crudsql.go:113-117`) embeds `*sql.Rows` and overrides `Close` only, so `Rows.Err()` is returned unclassified; `crudpgx`'s `rows` overrides `Err` and classifies (`crudpgx.go:110-115`), with a comment naming the divergence that made it necessary. A server error raised mid-stream is therefore a coded 409 through pgx and an anonymous 500 through `database/sql`. All four supported drivers report statement errors from `QueryContext`, so it is narrow — and nothing pins it either way. Point 4: without `parseTime=true` go-sql-driver/mysql hands a `DATETIME` back as `[]byte` and every scan into a `time.Time` fails. vv's own `utils/vvdb/dsn.go:132-137` forces it as a default for exactly that reason, pinned by `dsn_test.go:137-138` ("without parseTime a DATETIME arrives as bytes and scanning it into a time.Time field fails"), and `loc` has its own escaping trap at `dsn.go:151-153`. A consumer following H-ADAPTERS-01 — their own pool, their own DSN, `vvdb` nowhere in the picture — is handed none of it by either adapter doc.
**If not ready:** They find out from the failure. What closes it is a short "what the adapter assumes about a driver" section in `crudsql.md`: `RowsAffected` and `LastInsertId` are used and their errors are ignored, `Rows.Err()` is not classified, and MySQL needs `parseTime=true`. Reading the two discarded errors and surfacing them is a behaviour change and wants a decision; saying they are discarded costs a sentence.

### H-ADAPTERS-17 — The conflict that arrives from the commit
**Who:** an engineer whose schema uses `DEFERRABLE INITIALLY DEFERRED`, or whose importer takes savepoints
**Wants:** a deferred constraint to reach the client as the same 409 an immediate one does
**Story:** The order and its lines are written in one transaction with a deferred foreign key. The rows go in cleanly. The commit fails.
**Must hold:**
1. A constraint that fires at `COMMIT` is classified, not returned raw.
2. It is classified the same way through both adapters.
3. A statement that poisoned the transaction gives the nested commit a code rather than an anonymous 500.
**Today:** ✅ ready
**Evidence:** Both adapters carry the classifier into the transaction and both say why in the same words: `crudsql.go:182-186` and `crudpgx.go:137-140` ("A deferred constraint fires at COMMIT, and a Tx without it would make that one shape of conflict a sentinel with no code while the immediate shape carried one"), with the commit side at `crudsql.go:197-201` and `crudpgx.go:155-158` naming the divergence it came from — one write being a 409 through `database/sql` and a 500 through pgx. Point 3 is `crudsql.go:222-236`, whose comment is unusually explicit that PostgreSQL refuses the `RELEASE` with `25P02` and that unclassified it reaches a caller as an anonymous 500. Proven live: `TestADeferredConstraintArrivesFromTheCommitAndNotTheStatement` (`test/integration/corpus_test.go:244`) and the nested-commit assertions in `edge_test.go:1217-1228`. Both module docs advertise it (`crudsql.md:116-118`, `crudpgx.md:92-96`).
**If not ready:** n/a. The one thing a consumer has to know and is not told at the call site: the 409 does not come back from `repo.Save`, it comes back from the implicit commit inside `crud.InTx`, so an application that maps errors around its repository calls and not around its transaction boundary loses it. That is a sentence in both module docs.

### H-ADAPTERS-18 — Somebody has to run the reservation again
**Who:** the same engineer as H-ADAPTERS-07, the morning after `SERIALIZABLE` shipped
**Wants:** the loser of a serialization race to be retried, once, without hand-rolling it
**Story:** Two reservations race. PostgreSQL aborts one with `40001`. The flow has to run again from the top, and the engineer has to work out whether the context from the failed attempt is safe to reuse.
**Must hold:**
1. Whether the transaction body may be called more than once is stated by whatever opens the transaction.
2. A retryable failure is distinguishable from one that is not, without matching strings.
3. If the library does not retry, it says so where the isolation level is asked for.
**Today:** ❌ for 1 and 3, ✅ for 2
**Evidence:** Point 2 is covered and covered well: `40001`, `40P01`, `55P03` and `25P02` are all in `errs/sqlerr/postgres.go:17-29` and map to `errs.KindRetryable` (`errs/codes.go`), which is why H-ERRS-08 rates the job-fleet story ✅. Nothing acts on it. `crud.InTx` (`crud/executor.go:503-524`) calls `fn` exactly once and its doc comment says nothing about re-running; no `Beginner` implementation retries; `rg -n "Retry"` over `crud/` finds nothing outside error vocabulary. Point 3 is the sharp part: asking for `SERIALIZABLE` is what *creates* the aborts, and the only place isolation can be asked for today — `crudsql.DB.WithTxOptions` (`crudsql.go:174`), shown in `docs/modules/en/crudsql.md:104-110` — says nothing about them.
**If not ready:** The consumer writes the loop themselves around `crud.InTx`, and has to know that a context carrying a binding from the failed attempt must not be reused. A retry helper is a real design question — how many attempts, what backoff, whether a joined transaction may be retried at all (it may not: the outer owner holds the commit) — and it hardens at a tag. What costs nothing now is the sentence saying vv never re-runs the body, placed where the level is asked for.

### H-ADAPTERS-19 — See the statements, and the p99 per query
**Who:** the platform engineer wiring tracing into a service that already has it for HTTP
**Wants:** every statement vv runs to appear in a trace, including the ones inside the ORM's transaction
**Story:** They add a tracer. They want the same spans whether the write went through vv, through gorm, or through a generated query, and they do not want to maintain a wrapper per library.
**Must hold:**
1. There is an answer that does not require the library to grow a hook.
2. It sees statements that run inside a transaction another library opened.
3. It is stated in the adapter's own documentation, because that is where a consumer looks for "what runs my SQL".
**Today:** 🟡 partial — 1 and 2 hold and are structural; 3 is missing
**Evidence:** Neither adapter opens a connection or reads any pool configuration — `crudsql` never calls `sql.Open` or `SetMaxOpenConns` (H-ADAPTERS-01), and `rg -n "pgxpool" crud/adapter/crudpgx/` returns three comment lines and no code (H-ADAPTERS-04 point 2). That is exactly what makes driver-level instrumentation work without the library's cooperation: an instrumented `database/sql` driver or a `sql.OpenDB(connector)` for `crudsql`, `pgxpool.Config.ConnConfig.Tracer` for `crudpgx`. Both sit *below* the handle every library in the process shares, so they see the ORM's statements and vv's, inside a transaction and out — which the `crud.Source` wrapper [[D-062]] recommends cannot do, because a joined transaction runs through `crudsql.From(tx)` and not through the wrapped source. The repository has already decided this: `docs/roadmaps/2026-08-26-1558-opentelemetry-roadmap.md:38` lists `vv/pgxotel` as "defer; use upstream driver instrumentation". The `vvdb` sweep documents the pgx half (`docs/ai/usecases/modules/vvdb/Vvdb.md:268-274`, `:769-773`). Neither adapter doc says any of it, and `docs/modules/en/crudsql.md` and `crudpgx.md` are where a consumer looks.
**If not ready:** They find the `crud.Source` wrapper first, because that is what [[D-062]] and [[D-061]] talk about, write four methods, and then discover it goes blind inside the ORM's transaction — and, if they wrapped a pgx source, that it deleted `COPY` (H-ADAPTERS-11). One paragraph per adapter doc naming the driver-level route as the default and the `Source` wrapper as the one for statements vv shapes rather than statements the database runs. `crud.Instrument` as a shipped wrapper is H-CRUD-15's proposal, not this file's.

### H-ADAPTERS-20 — The pool is replaced while the process runs
**Who:** whoever is on call during a failover, or shipping a blue/green endpoint swap
**Wants:** the repositories to follow the new handle, or to be told plainly that they will not
**Story:** The primary fails over and the connection string now points somewhere else. They build a new `*sql.DB`. Every repository in the process was bound at start-up.
**Must hold:**
1. What has to be rebuilt when the handle changes is stated.
2. Anything the library keeps keyed on the old handle is released or is bounded.
**Today:** ❌ missing
**Evidence:** `Bind` takes the source once, at declaration (`crud/sqlrepo/blueprint.go:246-249`), and a repository holds it for its lifetime — so every repository, every decorator chain and every handler built over one must be rebuilt, and nothing says so. `catalog.Set.Load` only ever appends (`crud/catalog/set.go:88-101`): there is no eviction and no `Forget`, so each retired handle leaves an entry behind and every subsequent `Load` scans past it. That is bounded in practice by how many times a process re-pools, and unbounded in contract. The schema half of the same question is answered and answered well — `catalog.Reloader` (`crud/catalog/reload.go:25-27`) exists precisely so a constraint added by a rolling migration is picked up without a restart, with a per-name backoff and a per-handle floor — but nothing in the library calls it, which is H-FAULTS-14's finding rather than this one's.
**If not ready:** Today the answer is to restart the process, which is what most deployments do anyway. What is missing is the sentence saying so, in both adapter docs, next to the sentence that says vv never opens or closes the connection: the handle is captured at `Bind`, so replacing it means rebuilding what was bound to it.

**Owned elsewhere, and not counted here.** A connection failure mid-request is `errs/sqlerr`'s: `errs/sqlerr/postgres.go:17-29` has no class `08` row and `errs.CodeUnavailable` (`errs/code.go:59`) is kind-mapped retryable with nothing producing it. H-ERRS-08 and H-ERRS-09 own the table. The adapter's whole share is one fact, and `crudpgx` states it itself: "a dead connection is a `*pgconn.ConnectError`, which extracts to nothing and classifies to nothing" (`crud/adapter/crudpgx/conflict.go:38-40`). Closing it here means letting that error reach a classifier that has somewhere to put it.

## The DX this should have

### The call site

```go
// database/sql — ent, gorm, sqlx, sqlc, bun, or none of them
repo := specs.Executor(Products.Bind(crudsql.Postgres(sqlDB)))

// pgx, straight onto the pool
repo := specs.Executor(Products.Bind(crudpgx.Open(pool)))
```

That is the shape all nine runnable examples contain, in three spellings — the
handle is named `db`, `sqlDB` or `db.DB` depending on what the ORM gave back, and
`_examples/gorm-mysql-gin/main.go:102` names a different engine. The one that is
worth reading separately is `_examples/auth-jwt-gin/main.go:148`,
`specs.Executor(Notes.Bind(crudpgx.Open(pool), security.Gate(policy)))`: it is the
only example that puts a gate over a pgx source, which is precisely the stack
where H-ADAPTERS-11's "a COPY writes past its own tenant scope" lands.

The adapter's share of the line is one expression — `specs.Executor` belongs to
another module and is optional — and it is the right amount: the engine named
because naming it is what buys the error codes, and the handle staying the
caller's.

### Turning one knob

```go
// database/sql. Introspect is the faults sweep's constructor, adopted here
// rather than re-specified (H-FAULTS-01): it loads the catalog through the
// handle it was given and returns a crudsql.DB whose classifier already has it.
// The catalog comes back too because probe.Full(cat) needs the same value.
sqlSrc, cat, err := crudsql.Introspect(ctx, crudsql.Postgres(sqlDB))
if err != nil {
    // The default, and the one to show. errors.Is(err, catalog.ErrSchemaUnreadable)
    // is what a reduced-privilege role branches on to bind sqlSrc anyway and
    // accept codes without columns — a decision, not a silent degradation.
    return err
}

// The join: one line, scoped to this database, carrying the classifier the
// constructor already holds.
ctx = sqlSrc.Join(ctx, tx.Statement.ConnPool)

// The one flow that needs SERIALIZABLE, on the call it already writes.
err = crud.InTxWith(ctx, sqlSrc, crud.TxOptions{Isolation: crud.Serializable}, func(ctx context.Context) error {
    return reserve(ctx, stock, order)
})
```

```go
// pgx, its own stack and its own source value. The import takes the models, so
// the columns and the value order come from the declaration; it runs inside the
// caller's transaction; and a refused row is classified like every other one.
pgxSrc, _, err := crudpgx.Introspect(ctx, crudpgx.Open(pool))
if err != nil {
    return err
}
n, err := crudpgx.CopyUnchecked(ctx, pgxSrc, Products.Meta(), products) // []*Product
```

### Why this shape

**`Join` returns a context, not an executor.** Today the join is
`crud.WithExecutor(ctx, crudsql.From(tx, crudsql.WithFaults(sqlfault.New("postgres"))))`
— one line and four concepts, one of which the consumer has to know to respell as
`WithExecutorFor` the day a second database appears. `Join` is a method on the one
value that knows both which engine it is and which database it is, so it can be
`WithExecutorFor(ctx, d.db, From(q, WithFaults(d.faults)))` underneath. That is
one line and one concept, it closes H-ADAPTERS-10, and it makes two of
H-ADAPTERS-12's three silent failures unreachable through the recommended call.
The name is `Join` and not `Adopt` because [[UC-010]] is already "adopt an
existing ORM model", cited by name at `docs/modules/en/crudsql.md:126`, while both
module docs already head this section "Joining someone else's transaction"
(`crudsql.md:36`, `crudpgx.md:41`). The faults sweep calls the same proposal
`Adopt` (`Faults.md:1217`, `:1503`); it is one proposal, and if the name moves it
moves in both files.

Two things about `Join` have to be written into the doc comment, because leaving
them out is how the fix reproduces the failure. **The handle argument is trusted
and unverifiable**: `database/sql` offers no way to ask a `*sql.Tx` which
`*sql.DB` opened it — that is [[D-027]]'s own reason for existing — so
`mainSrc.Join(ctx, eventsTx)` compiles, keys the binding on `mainDB`, and runs
the main repository's writes on the events transaction. That is
H-ADAPTERS-12 inverted, reached through the recommended call rather than around
it, and the one place a sentence costs nothing. **And the receivers have to be
named**: `crudsql.DB` for certain; `crudsql.Source(q, d)` has no engine to carry,
so if it gets the method at all the method must be a no-op with respect to the
classifier rather than a silent nil; `crudpgx` has no `DB` type at all
(`crudpgx.go:147` returns `Executor`), so its twin's receiver is an open
question that this design does not get to leave to the reader — four cases in
this file carry a must-hold of the form "the answer is the same on both
adapters".

**`InTxWith` rather than `crudpgx.WithTxOptions`.** A `WithTxOptions` on pgx
would restore symmetry with `crudsql` by shipping the same limitation to a second
adapter: isolation would stay a property of the source, so the one
`SERIALIZABLE` flow would still need a second source value threaded to the call
site. The per-call form is reached the way `BeginnerOf` reaches `Beginner` — an
optional `BeginnerWith` the adapter implements, resolved through the [[D-061]]
walk so a wrapper does not erase it. Four rules have to be in the proposal or the
first implementer will guess at all of them.

1. **A savepoint cannot change isolation**, so the options apply to a pool or a
   connection and are ignored on a nested `Begin`. That is already what
   `database/sql` does — `crudsql.Tx.Begin` at `crudsql.go:208-214` issues
   `SAVEPOINT` and never looks at `TxOptions` — and it is forced on pgx, where
   `pgx.Tx` has `Begin` and no `BeginTx`.
2. **An ambient transaction is a refusal, not a shrug.** `crud.InTx` joins rather
   than nests and [[UC-005]] guarantee 7 pins that it must (`crud/executor.go:504`
   returns `fn(ctx)` unchanged). So `InTxWith` inside the ORM's transaction —
   H-ADAPTERS-06's shape, the best-proven thing in this repository — would run the
   reservation at whatever level the ORM opened, silently, which is the exact
   failure this whole case exists to prevent. It must answer a distinct sentinel
   instead. `OwnedExecutorFor` (`crud/executor.go:365-371`) already answers "did
   vv open this one", so the refusal is cheap, and a caller who genuinely means
   "join whatever is there" still has `crud.InTx`.
3. **`ReadOnly` is part of `crud.TxOptions`, not a follow-up.** H-ADAPTERS-07
   raises it and `crudsql` can already carry it; a per-call form that covers only
   the isolation level re-ships the unmeasured half.
4. **The body is called once.** vv does not retry a `40001`, and the doc comment
   for the call that makes `40001` likely is where that has to be said
   (H-ADAPTERS-18).

`repo.Tx(ctx, fn)` deliberately does not grow an options form, and that has to be
stated rather than left to be noticed: `Tx` is on `crud.Core` (`crud/repo.go:52-53`),
so a second verb beside it is [[D-030]]'s obligation — `coreVerbs` in
`crud/decorators/security/obligation_test.go` refuses to compile until the gate
overrides it or a written reason says why inheriting is safe. The one flow that
needs a level uses the free function and holds the source. That is one line and
three concepts replacing one line and none, and it is the honest price.

**`CopyUnchecked` takes the source, the meta and the models.**
`func CopyUnchecked[M any](ctx context.Context, src crud.Source, m *crud.Meta, models []*M) (int64, error)`.
Taking `crud.Source` is what lets it be reached behind an instrumenting wrapper
or a `ReadWrite` pair, which the bare type assertion cannot be — and it needs
`crud.BulkInserterOf` beside `BeginnerOf` and `ReadSourceOf` to do it. Taking the
models rather than `[][]any` is the half round 1 left open: the caller was still
hand-building rows in an order they had to match by hand, and could not encode a
`crud.Opt[T]` at all, which is the exact defect `TestPgxBulkCopy` shows. The
columns come from `Schema.Insert` or `Schema.InsertGen` (`crud/meta.go:96-97`,
built at `:285-291`) and **not** from `Schema.Columns()`, which round 1 named:
`Columns` returns every mapped column, primary key first (`:178-186`), so a copy
built on it writes explicit zeros into a serial key and into every `generated`
column — which is why `test/integration/driver_pgx_test.go:120` copies five
columns and deliberately not `id`. Each row is then `Schema.Values(model, fields)`
(`crud/access.go:31-41`). `*crud.Meta` embeds `*crud.Schema` and `Blueprint.Meta()`
is already exported (`crud/sqlrepo/blueprint.go:242`), so `crudpgx` gains no
`crud/sqlrepo` import. If a 250,000-row CSV should never be materialised, the
streaming form takes an `iter.Seq[*M]` instead of a slice.

It resolves its executor the way a repository does, `crud.ExecutorFor(ctx, src)`
first, so a `COPY` inside `repo.Tx` is inside it and rolls back with it. It
classifies through the same `e.conflict` every other statement in `crudpgx` uses,
because a duplicate SKU answering 500 in the fast path and 409 in the slow one is
the divergence `conflict.go:17-20` says the two adapters exist to prevent. When
the resolved handle has no `COPY` it refuses with a `COPY`-specific sentinel
rather than `ErrNoTxSupport`, and it never falls back to row-at-a-time: a fast
path that silently becomes a slow one is the failure that made this a case. The
name stays ugly on purpose — what a tenant-scoped application loses by reaching
for `COPY` is its tenant scope, and that belongs in the name and in the doc
comment where the call site can see it.

**`Introspect` returns the concrete `DB`, and the error is a named sentinel.**
The faults sweep specifies the signature
(`Faults.md:1131`) and the reason: `DB` carries `Begin` and the exported
`TxOptions` field, and narrowing to `crud.Source` would make the short path a
one-way door. That is also what makes `sqlSrc.Join(...)` legal in the block
above. The addition this file asks for is on the error arm: "fail here" is the
right default and the wrong *only* option, because a managed role restricted from
`information_schema` then cannot boot the service at all for a feature that fills
in column names on 409 bodies. A `catalog.ErrSchemaUnreadable` beside the existing
`catalog.ErrUncomparableHandle` is what lets the doc show fail-fast as the default
and "log it and bind anyway" as a deliberate alternative — which is the advisory
posture [[D-042]] already takes for the probe.

**What this section no longer proposes.** Round 1 proposed
`crudsql.WithCatalog(ctx)` as an `Option` and `crud.Instrument(src, fn)` as a
shipped statement wrapper. The first cannot exist: `Option` is `func(*config)`
(`crudsql.go:49`) and a catalog load returns an error. `crudsql.Introspect` is the
faults sweep's answer to the same problem and there must be one, not two. The
second is a `crud` proposal argued in an adapters file; **H-CRUD-15** owns it —
round 1 sent the reader to H-CRUD-11, which is about `crud.Raw`. The residue that
is only visible here stays in H-ADAPTERS-11: a `Source` wrapper erases
`BulkInserter`, which is the one optional interface [[D-061]]'s table does not
list. And this file no longer carries a case for a connection failure mid-request;
H-ERRS-08 and H-ERRS-09 own it, and the one adapter-side fact is recorded above.

### What it must not break

- **[[D-009]] — the context executor is captured unconditionally; naming a
  database is opt-in.** `Join` does not change what `WithExecutor` does, and only
  `WithExecutorFor` still restricts, so the invariant holds. What it *does* change
  is the default a consumer is taught: today the documented join is unscoped, and
  under `Join` the recommended spelling is scoped. D-009 gives the counter-argument
  in advance — "refusing to join is not safer than joining wrong; it is a
  different wrong answer with the same shape" — and it bites in two places this
  file names. A repository bound to `crudsql.Source(conn, …)` for the RLS story
  (H-ADAPTERS-15) has the `*sql.Conn` as its identity (`crudsql.go:86`), and a
  repository on `crudpgx` over the same server has the pool (H-ADAPTERS-05 point
  3). Both join a plain `WithExecutor` today and neither would match a scoped
  `Join`. Two sources over the *same* handle are unaffected: `KeyOf` reduces both
  to the `*sql.DB`. **The executor-returning form must stay reachable** for the
  deliberate unscoped case, and the docs should say which is which rather than
  only showing one.
- **[[D-027]] — cross-database capture is documented, not enforced. Status: open.**
  `Join` is a live answer to that decision's open question 1 — whether an unscoped
  binding should be a fallback at all — and it is the owner's call, not a module
  sweep's. It is a fourth option beside the three D-027 lists, and it is the
  narrow one: it does not remove the fallback, it stops the recommended call site
  from needing it. If it is taken, D-027 gets the fourth option written into it
  with what was chosen, including a rejection.
- **[[D-046]] — an engine is never derived from a dialect.** `Join` derives
  nothing. It copies a classifier built from a literal the consumer wrote at the
  constructor, read back through `sqlfault.Classifier.Engine()`
  (`crud/sqlfault/classify.go:54-60`). Reading back what the caller already said
  is not inspecting `crud.Dialect`. `crudsql.Open(db, crud.Postgres{}).Join(...)`
  must therefore carry no classifier, exactly as `Open` carries none — and
  H-ADAPTERS-13 point 5 is the reason that matters more than it looks, because
  `Open` is the only way to say `crud.MySQL{RowAlias: true}`.
- **[[D-030]] — a new verb on the seam is an obligation on every decorator.** Two
  things here touch it. `repo.TxWith` would be a `crud.Core` verb and is
  deliberately not proposed. `CopyUnchecked` is *not* on the seam and cannot be
  seen by `coreVerbs`, which is exactly why its reasoning has to be written down:
  it is a write path that bypasses `security.Gate`, the soft-delete filter, the
  version counter and every hook. D-030 exists because of `SaveAll`, "the call
  that writes the most rows and checks none of them"; this is that one layer
  lower. The name is the substitute for the gate override D-030 would otherwise
  require, and H-ADAPTERS-11 point 5's list belongs in the doc comment.
- **[[D-057]] — the application opens the connection.** Nothing here opens one.
  `Introspect` reads through the handle it was given and returns an error, which
  is the honest cost of folding the three-statement recipe into one.
- **[[D-061]] — a wrapper forwards what it wraps, and the library walks to find
  it.** `crud.BulkInserterOf` is the fifth walker and an extension of the decision
  rather than a challenge: D-061's table lists two walkable interfaces and four
  helpers, and `BulkInserter` was left out because nothing in the library reached
  for it. Something does now. `SourceUnwrapper`'s doc comment
  (`crud/executor.go:211-219`) lists three losses and gains a fourth.
- **[[D-062]] — no statement hook anywhere.** Nothing proposed here is one. A
  per-call `TxOptions` is an argument to `Begin`, not a callback. H-ADAPTERS-19
  does not challenge it either: driver-level instrumentation is below vv entirely
  and needs nothing from the library. The shipped statement wrapper that *would*
  be a challenge to D-062's invariant sentence is H-CRUD-15's to raise.
- **[[UC-008]] — nothing in the library reaches for a driver's bulk-copy path.**
  `CopyUnchecked` is still a named function the caller calls, and `SaveAll` still
  writes one multi-row `INSERT` whatever is underneath it. But UC-008's Out of
  scope also says the copy "ignores any transaction in the context", and joining
  the caller's transaction changes that sentence. **That is a deliberate
  amendment to UC-008 and the owner should take it as one**, not as an
  implementation detail: the current behaviour is a rollback that silently keeps
  250,000 rows.
- **[[D-041]] — the catalog is per physical handle.** `Introspect` keys on the
  handle it was given, which is what the adapter's `DataSource()` already answers.

**What can wait.** Every proposal above is additive except the UC-008 amendment,
which changes documented behaviour and therefore wants to land before the tag or
be written down as a known change. The documentation defects cost nothing and can
ship today: the `*sql.Conn` mapping in `crudsql`'s package doc, the two guides'
sentence that contradicts [[D-044]], and the `cat, _ := catalog.Load(...)` in both
module docs.

## DX verdict

| What the ideal asks for | Today | Distance |
|---|---|---|
| Bind an existing handle in one expression | Exactly this, on both adapters, in all nine examples | none |
| See every statement, with the ORM's included | An instrumented driver or connector for `crudsql`, `ConnConfig.Tracer` for `crudpgx`; the adapter reads no pool configuration, so both work and both see inside a joined transaction — and neither adapter doc says so | small (docs only) |
| A deferred constraint that classifies | Exactly this, on both adapters, proven live, with the savepoint `25P02` case as well | none |
| MariaDB as a target of its own | Exactly this, and driven live through both the constructor and the server | none |
| One line per engine, including the MySQL 8 upsert form | Four constructors hardcode a zero-value dialect, so `crud.MySQL{RowAlias: true}` means dropping to `Open` and hand-restating the engine to keep the codes | small (a fifth constructor or `WithDialect`) |
| Join somebody else's transaction, keeping the codes | 1 line and 1 concept if you accept a code-free 409; 1 line and 4 concepts restated at every join site if you do not, with every site missed a silent downgrade and no compiled call site anywhere to copy | large |
| Two databases without crossing them | `crud.WithExecutorFor(ctx, mainDB, ...)` — 1 line, proven both ways on `crudsql`; unmeasured on pgx, and naming the handle you are holding rather than the one you passed matches nothing | small (the call) · large (the silence) |
| Isolation for one flow | `database/sql`: a second source value, one line, and nothing proves it joins. pgx: abandon `repo.Tx` — 6 lines and a `defer` replacing 1. Neither says who retries the `40001` | large |
| Columns named on a composite unique | 3 statements, a throwaway source and a discarded error, from a doc; the mechanism is proven and no example walks it | small (the saving is the second handle, not the line count) |
| Bulk insert from the model | A hand-written `[]string` of columns and hand-built `[][]any` rows, kept in sync with the tags by hand — and only from a source nothing has wrapped | large |
| A bulk insert inside the job's transaction, whose failures classify | Not reachable, either half. The rollback keeps the copied rows and the refused row is a bare driver error | large |
| A driver that supplies the constraint name | Choose pgx and do not choose lib/pq. Nothing says so, and `vvdb`'s doc recommends lib/pq | large |
| Know which engines are supported, and how to add a fifth | The four are named in the README and both module docs; nothing says they are the *boundary* and nothing names `Open(db, yourDialect)` as the route out | small |
| Test in a rolled-back transaction | `crudpgx`: works. `crudsql`: `repo.Tx` under test answers `ErrNoTxSupport`, and every conflict assertion loses its code | large |
| Bind a single connection for RLS | `crudsql.Source(conn, dialect)` works and is named in no table, no README row and no module doc; the one line that names `*sql.Conn` names a constructor that will not take it | small (docs) · large (it also gives up `repo.Tx`) |

**Overall:** the short path is as short as it can be, and two of the module's
strongest properties are ones it never claims — a deferred constraint classifies
identically through both adapters, and because neither adapter reads a line of
pool configuration, driver-level tracing works better than any wrapper the
library could ship. The distance opens on the second knob, and it opens the same
way five times: the thing a consumer wants next is not an argument to the call
they already wrote, it is a different call. Isolation for one flow means a second
source value threaded to the call site. Codes on a joined transaction mean
restating the engine. The catalog means a throwaway source. The modern MySQL
upsert means leaving the constructor that names the engine. Worse than any of
those, three of the knobs cancel each other out with no compiler error and no
run-time complaint: adding a replica or instrumentation through the `Source`
removes `COPY`, calling `COPY` removes the transaction and the classifier, and
wrapping the source without `UnwrapSource` removes the identity every scoped
binding matches on. The short path is excellent and the second step is where a
consumer starts writing code this library was supposed to have written.

## Release blockers found here

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `crud.BulkInserter` is reached by a bare type assertion and has no walker, so `crud.ReadWrite` and any `Source` wrapper delete `COPY` silently | blocker | It is [[D-061]]'s exact failure on the one optional interface D-061's table omits, so a consumer who obeys the decision to the letter still hits it. `ok` is false, the fast path is gone, and this file's own advice — add a replica, instrument the source — is what triggers it |
| 2 | A `COPY` ignores the transaction in the context, and the call site says nothing about it | blocker | Most bulk importers are transactional. The rollback takes everything except the copied rows, reports success, and nothing anywhere connects the two. Fixing it after a tag changes documented behaviour ([[UC-008]] Out of scope) rather than adding to it |
| 3 | `crudpgx.Executor.CopyFrom` never calls `e.conflict` — the only statement path in the file that does not | serious | One duplicate SKU in a supplier feed comes back as a bare `*pgconn.PgError`: no `crud.ErrConflict`, no fault, no code, so the importer and any status mapping above it answer 500 where the row-at-a-time loop answered 409. It is H-ADAPTERS-09's whole story evaporating in the path already blocked twice above |
| 4 | `crudsql.From(tx)` drops the fault code and there is no spelling that carries the engine the consumer already declared | serious | The 409 body changes because the write moved inside a transaction, which is invisible to any test asserting the status. The degradation is documented in four places; what is missing is a way to avoid it, and no example shows the correct spelling |
| 5 | On lib/pq a violation loses the constraint, the table, the schema and the column — and with them everything the catalog could fill | serious | `WithFaults` appears to work and the field-level half is gone with nothing reported. The consumer's fix is one import line, which is why not saying so is expensive. `docs/modules/en/vvdb.md:90` recommending lib/pq is `utils/vvdb`'s row to fix, not this one's |
| 6 | Neither adapter can give one flow its own isolation level; on pgx there is nothing to ask with, and nothing anywhere says who retries the `40001` | serious | `Begin` and `InTx` take no options. On `database/sql` it costs a second source value and no test proves it joins; on pgx it costs leaving `repo.Tx`. Adding the per-call seam later is additive; shipping two adapters that disagree about what a transaction can be asked for is not |
| 7 | A scoped binding keyed on anything but the exact handle passed to the constructor matches nothing, so the write goes to the pool outside the transaction and reports success | serious | Three spellings reach it and all three read correctly at the call site: naming the transaction, naming the `*gorm.DB`/`*sqlx.DB` the consumer is actually holding, and wrapping the source without `UnwrapSource`. Recorded in `docs/ai/usecases/Index.md:262-266` as needing a decision, not a test |
| 8 | `crud.MySQL{RowAlias: true}` cannot be said at a named constructor, so the modern MySQL 8 upsert costs the engine declaration | serious | The four constructors hardcode a zero-value dialect and `Option` carries only a classifier. A DBA ticket about a deprecated `VALUES()` form is answered by dropping to `Open` and losing every error code — blocker 4's shape arriving at the binding line. The repository's own two row-alias targets are built exactly that way |
| 9 | `crudsql` ignores the errors from `RowsAffected()` and `LastInsertId()` and reports zero and absent | sharp edge | On a driver that does not implement either, an optimistic-locked update becomes a permanent stale-write refusal and a generated key silently stays zero. What the adapter needs from a driver beyond two methods is written down nowhere |
| 10 | Multi-handle scoping is proven on `crudsql`/PostgreSQL only; no test in the tree builds a scoped binding on pgx | sharp edge | Half the module's subject is argued rather than proven for the guarantee that decides whether a write lands in the right database |
| 11 | `crudsql.Source` and `crudsql.From` cannot open a transaction, so a repository bound to a `*sql.Tx` or a `*sql.Conn` answers `ErrNoTxSupport` for `repo.Tx` | sharp edge | It decides whether a consumer's own integration suite is writable at all, and it is the same defect the RLS story hits. `crudpgx` has none of it |
| 12 | `*sql.Conn` has no working call named anywhere — not in `crudsql`'s package doc, not in `README.md:1337`, not in either module doc's stack table | sharp edge | The line that names the type names `crudsql.Open`, which takes a `*sql.DB`. `crudsql.Source(conn, dialect)` is the call that works and appears in none of them |
| 13 | Both usage guides say an unclassified 409 "carries the driver's own sentence, constraint name included" | sharp edge | [[D-044]]'s invariant forbids it and the render path has no route for `err.Error()`, so the consumer is told the loss is cosmetic when it is total. Two lines, two files, and they are parallel by design |
| 14 | `crudsql` does not classify `Rows.Err()`; `crudpgx` does, and its comment names exactly the divergence that creates | sharp edge | A server error raised mid-stream is a coded 409/400 through pgx and an anonymous 500 through `database/sql`. All four supported drivers report statement errors from `QueryContext`, so it is narrow, and nothing pins it either way |
| 15 | `crudpgx.CopyFrom` on a handle without `COPY` returns `crud.ErrNoTxSupport` — "executor cannot begin transactions" | sharp edge | The message describes a different question than the one asked, and there is no sentinel for the one that was |
| 16 | No runnable example wires `WithFaults` or `catalog.Load`, and both module docs write `cat, _ := catalog.Load(...)` against that function's own doc comment | sharp edge | The feature that turns a refused write into a field-level 409 has no compiled call site to copy, and the shipped snippet degrades silently to codes-without-columns when the role cannot read the schema |
| 17 | `crud/dialect.go:65` claims CockroachDB as a Postgres target: no constructor, no captured corpus, no test | sharp edge | It is MySQL-with-a-footnote, which is the shape [[D-046]] exists to refuse, and a Cockroach consumer is routed through PostgreSQL's SQLSTATE table with nothing saying so |
| 18 | `crudpgx.Queryer`'s three claimed handle types have no compile-time assertion, and a `*pgx.Conn` is bound nowhere | sharp edge | A pgx signature change breaks the consumer's build and not ours. Two of the three assertions are free; the `*pgxpool.Pool` one promotes two indirect requirements into a published module and is [[D-051]]'s decision |
| 19 | Array and JSON columns are unspecified: mapped, passed straight to the driver, work on pgx, fail on `database/sql`, tested nowhere | sharp edge | A PostgreSQL consumer meets it in the first month and finds no answer in either adapter's doc |
| 20 | Neither adapter says what happens when the handle is replaced: repositories capture the source at `Bind`, and `catalog.Set` has no eviction | sharp edge | A failover or a blue/green swap means rebuilding everything bound to the old handle, and nothing says so next to the sentence that says vv never opens or closes the connection |
| 21 | No worked route to a fifth engine, and neither adapter doc names `crudsql.Open(db, yourDialect)` as the escape hatch | sharp edge | The evaluation question — "does this reach our database" — is answered by four constructor names and a silence about everything else |

## Contested

- **"That is the line all nine runnable examples contain, verbatim" was challenged
  by all three reviewers.** Conceded and corrected: it is the same *shape* nine
  times in three spellings, and the example that deserved naming —
  `auth-jwt-gin`, the only one that puts a `security.Gate` over a pgx source — is
  now named, because that is the stack H-ADAPTERS-11's tenant-scope finding lands
  on.
- **Blocker 6 (isolation) was over-stated in round 1 and is kept, rewritten.** The
  claim that one `SERIALIZABLE` flow means "a second `Bind` for every repository
  it touches" was wrong five times over: `WithTxOptions` returns a copy holding
  the same `*sql.DB`, `InTx` scopes on `ownScope` → `identityOf` → that handle,
  and `bindingFor` matches every repository already bound to the pool. The real
  cost on `database/sql` is one extra source value threaded to the call site, and
  nothing proves the join. On pgx there is still nothing to ask with. The blocker
  survives because it is now two adapters disagreeing about what a transaction can
  be asked for, plus a retry contract nobody owns.
- **Blocker 4's "you are told a thousand lines away" was wrong and is withdrawn.**
  `From`'s own doc comment, `crudsql.md`'s "Codes on a joined transaction" heading
  and `README.md:1346-1354` — nine lines above the join snippet — all say it. The
  blocker is kept and re-aimed: the telling is fine, the *mechanism* is missing,
  and what is genuinely wrong is the guides' neighbouring sentence, which is now
  blocker 13 in its own right.
- **Blocker 12 was filed against two artifacts and one of them was innocent.**
  `README.md:1337` names `crudsql.From(tx)` in the same cell, so `*sql.Tx` has a
  working spelling. Narrowed to the one handle type that has none anywhere,
  `*sql.Conn` — which is sharper, because it is the handle H-ADAPTERS-15's whole
  RLS story depends on.
- **`Adopt` was renamed to `Join`.** [[UC-010]] already means "adopt an existing
  ORM model" and is cited by name in `crudsql.md`, while both module docs already
  head the section "Joining someone else's transaction". The faults sweep names
  the same proposal `Adopt`; it is one proposal and the name has to move in both
  files or in neither.
- **The proposal's silent trade against [[D-009]] and [[D-027]] was raised and is
  conceded.** Round 1 proposed a scoped-by-default join and named neither
  decision. Both are now in "What it must not break", with the repositories that
  stop joining named and the executor-returning form kept reachable, and `Join` is
  recorded as a fourth option against D-027's open question 1 — the owner's call,
  not this file's.
- **The "schema changes while the process runs" gap was raised and is kept only
  in half.** `catalog.Reloader` exists for exactly the rolling-migration case, with
  a per-name backoff and a per-handle floor, so a constraint added mid-run is
  recoverable and the fact that nothing calls it is H-FAULTS-14's finding. What is
  this module's is the handle changing, which nothing addresses at all —
  H-ADAPTERS-20.
- **`crudpgx.WithFaults` dropping the typed extractor was raised as a missing case
  and is kept as a cross-reference.** It is a real `crudpgx` defect and
  H-FAULTS-28 already states it, rates it ❌ and counts its blocker. Counting it
  twice would make the release look worse than it is; H-ADAPTERS-09 names it in
  one sentence and says where it is counted.
- **`CopyUnchecked` keeps the source argument, and now takes models rather than
  rows.** A `*sqlrepo.Blueprint` holds a meta, a plan, the settings, the relation
  scopes and the soft-delete field (`crud/sqlrepo/blueprint.go:138-144`) and no
  handle, so there is nothing on `Products` for the call to resolve an executor
  from. The `[][]any` half of round 1's proposal is withdrawn: it left the caller
  hand-ordering values against a column list they could not see, which is the
  defect the case exists for.

## Edge cases

### E-ADAPTERS-01 — Dependency wiring supplies a nil handle
**Shape:** misuse
**Setup:** A service's database dependency is optional in one environment and an unset `*sql.DB`, `*pgx.Conn`, or `pgx.Tx` reaches a named adapter constructor.
**What the consumer does:** They bind repositories during startup and expect a configuration failure naming the missing dependency, before a live request reaches it.
**What must happen:** Construction refuses a nil handle loudly, or the adapter exposes an error-returning assembly path; it must not make the first query panic.
**Today:** ❌ wrong or unhandled
**Evidence:** `crudsql.Open` and every named `crudsql` constructor store the handle without a nil check (`crud/adapter/crudsql/crudsql.go:151-165`), then `DB.Begin` dereferences it (`:177-185`); `crudpgx.Open` and `From` likewise store `Queryer` unchanged (`crud/adapter/crudpgx/crudpgx.go:76-77`, `:146-147`) and `Exec` invokes it unconditionally (`:87-92`). The adjacent tests use nil only to exercise private classification methods (`crud/adapter/crudsql/classify_test.go:25-64`); no constructor-to-call nil test was found.
**Blast radius:** crash

### E-ADAPTERS-02 — A custom database handle is paired with a nil dialect
**Shape:** degenerate declaration
**Setup:** An integration helper calls `crudsql.Source(q, nil)` or `crudsql.Open(db, nil)` while factoring its own dialect behind configuration.
**What the consumer does:** They expect a bad declaration to fail where it is made, rather than long after a repository has been bound.
**What must happen:** The adapter rejects a nil `crud.Dialect` at construction, because every repository needs a usable SQL renderer.
**Today:** ❌ wrong or unhandled
**Evidence:** `crudsql.Source` and `Open` store `d` unchanged (`crud/adapter/crudsql/crudsql.go:127-134`, `:146-153`) and `DB.Dialect` returns it unchanged (`:168`); repository assembly retains the source dialect (`crud/sqlrepo/repository.go:32`) and SQL rendering calls `Dialect.Placeholder` (`crud/sqlrepo/repository.go:85`). `classify_test.go:25-64` exercises a real `crud.Postgres{}` dialect only; no nil-dialect test was found.
**Blast radius:** crash

### E-ADAPTERS-03 — An optional classifier silently erases pgx's default one
**Shape:** misuse
**Setup:** An application builds an optional catalog classifier, leaves it nil in one deployment, and still passes `crudpgx.WithFaults(classifier)` alongside `crudpgx.Open(pool)`.
**What the consumer does:** They expect an absent optional extension to retain the standard PostgreSQL classification, or to be refused at startup; a duplicate key should not lose its code because a feature flag was off.
**What must happen:** A nil `WithFaults` argument is rejected or ignored, and conflicting classifier options have documented, tested precedence.
**Today:** ❌ wrong or unhandled
**Evidence:** `crudpgx.faults` begins with the PostgreSQL classifier then applies every non-nil option (`crud/adapter/crudpgx/crudpgx.go:62-74`), while `WithFaults(nil)` overwrites that field (`:55-60`). `sqlfault.Wrap` accepts a nil classifier and degrades an integrity error to the sentinel without a fault or code (`crud/sqlfault/classify.go:127-156`). The adapter tests cover a non-nil replacement (`crud/adapter/crudsql/classify_test.go:25-64`) but no nil or duplicate option.
**Blast radius:** silent wrong answer

### E-ADAPTERS-04 — A transaction configuration changes after the source was built
**Shape:** concurrency
**Setup:** Two source values are derived from one `*sql.TxOptions`, then configuration code changes its `Isolation` or `ReadOnly` field while requests begin transactions.
**What the consumer does:** They rely on `WithTxOptions` returning an independent source whose transaction policy cannot change behind its back or race with `Begin`.
**What must happen:** The option value is copied at configuration time, or the API makes shared mutable configuration explicit and safe.
**Today:** ❌ wrong or unhandled
**Evidence:** `WithTxOptions` copies the `DB` value but stores the caller's `*sql.TxOptions` pointer verbatim (`crud/adapter/crudsql/crudsql.go:138-144`, `:173-174`), and `Begin` passes that same pointer to `BeginTx` (`:176-185`). `TestWithTxOptionsReachesTheDriver` (`test/integration/edge_test.go:1231-1239`) uses a fresh literal and tests only the configured isolation; no mutation, aliasing or concurrent-begin test was found.
**Blast radius:** silent wrong answer

### E-ADAPTERS-05 — A cancelled commit context means different things on the two adapters
**Shape:** seam
**Setup:** A request reaches its commit boundary after its deadline is cancelled, with the application passing that context to `crud.Tx.Commit`.
**What the consumer does:** They need the same documented cancellation semantics through `database/sql` and pgx; a context argument must not be silently ignored on only one of the two doors.
**What must happen:** Both adapters honour the supplied commit context where their driver can, or the `database/sql` limitation is surfaced in the API and documented next to transaction use.
**Today:** 🟡 partial
**Evidence:** `crudpgx.Tx.Commit` and `Rollback` pass `ctx` to pgx (`crud/adapter/crudpgx/crudpgx.go:155-159`), whereas `crudsql.Tx.Commit` and `Rollback` ignore their context argument because the underlying methods take none (`crud/adapter/crudsql/crudsql.go:197-202`). The transaction tests exercise deferred constraints and isolation, not a cancellation at commit (`crud/adapter/crudsql/*_test.go`, `crud/adapter/crudpgx/*_test.go`: no cancellation test found).
**Blast radius:** confusing error

### E-ADAPTERS-06 — A pinned `*sql.Conn` is accidentally shared by tenants
**Shape:** concurrency
**Setup:** Middleware binds a repository to one `*sql.Conn` after setting RLS or session state, then stores that repository where two tenant requests can use it at once.
**What the consumer does:** They need the adapter's documented single-connection route to make its lifetime and concurrency boundary unmistakable, so tenant state cannot be reused by accident.
**What must happen:** The documented call scopes a connection to one request or transaction, and the adapters state whether a bound connection may be shared concurrently.
**Today:** ❓ unverified
**Evidence:** `crudsql.Queryer` accepts `*sql.Conn` structurally (`crud/adapter/crudsql/crudsql.go:30-35`) and `Source` retains the supplied `Queryer` as its data source (`:119-134`); every call then goes directly through that value (`:88-110`). The module page's stack table instead groups `*sql.Conn` with `crudsql.Postgres(db)`, whose constructor takes `*sql.DB` (`docs/modules/en/crudsql.md:48-50`; `crudsql.go:151`, `:159`); its later generic `Source(q, dialect)` paragraph gives no connection lifetime or sharing rule (`crudsql.md:69-83`). No adapter test binds a `*sql.Conn` or drives concurrent calls.
**Blast radius:** data leak

### E-ADAPTERS-07 — A shared pgx connection is used like a pool
**Shape:** concurrency
**Setup:** A team reads that `*pgxpool.Pool`, `*pgx.Conn` and `pgx.Tx` all satisfy `Queryer`, binds a global repository to a single connection, and serves concurrent requests through it.
**What the consumer does:** They need the adapter to distinguish the pool's ordinary shared use from a connection or transaction's ownership and concurrency constraints.
**What must happen:** The public documentation names the safe sharing boundary for each advertised handle type, and a concurrent-use test pins the adapter's own state behaviour.
**Today:** ❓ unverified
**Evidence:** The public `Queryer` comment groups all three types together (`crud/adapter/crudpgx/crudpgx.go:27-31`), `Open` stores whichever value it receives (`:146-147`), and `Exec`/`Query` forward to the same stored value (`:87-102`). The nearby adapter tests use fake handles serially (`crud/adapter/crudpgx/conflict_test.go:38-53`) and no concurrent connection or transaction test was found.
**Blast radius:** confusing error

### E-ADAPTERS-08 — A foreign Queryer breaks its own success contract
**Shape:** degenerate declaration
**Setup:** An ORM shim or test double implementing `crudsql.Queryer` returns `(nil, nil)` from `ExecContext` or `QueryContext` after an internal bug.
**What the consumer does:** They need a diagnostic failure at the adapter boundary, not a nil dereference that hides which foreign handle broke its contract.
**What must happen:** A nil result or row cursor accompanying a nil error is rejected with an adapter error before repository code dereferences it.
**Today:** ❌ wrong or unhandled
**Evidence:** `crudsql.Executor.Exec` immediately calls `RowsAffected` on a successful result (`crud/adapter/crudsql/crudsql.go:88-101`), and `Query` wraps a successful row pointer without a nil check (`:104-117`). `Queryer` is intentionally public for foreign handles (`:30-35`), but its adjacent tests cover only error classification and have no success-contract test (`crud/adapter/crudsql/conflict_test.go`, `classify_test.go`).
**Blast radius:** crash

### E-ADAPTERS-09 — The cursor reports its failure when it is closed
**Shape:** partial failure
**Setup:** A `database/sql` driver discovers a protocol or server-side failure while closing a result set after the repository has read its rows.
**What the consumer does:** They need the repository call to fail, preferably through the usual classifier, rather than return a complete-looking page after the driver reports a close error.
**What must happen:** A close-time failure is retained and returned through `Rows.Err`, or the public cursor contract makes the unavoidable limitation explicit.
**Today:** ❌ wrong or unhandled
**Evidence:** `crud.Rows.Close` has no error result (`crud/executor.go:18-25`); `crudsql.rows.Close` explicitly discards `*sql.Rows.Close`'s error (`crud/adapter/crudsql/crudsql.go:113-117`); and repository read paths defer `Close` then return the earlier `Rows.Err` result (`crud/sqlrepo/repository.go:964-985`, `:988-1000`). `crudpgx` only wraps `Rows.Err` (`crud/adapter/crudpgx/crudpgx.go:104-115`). No adapter test exercises a close-time error.
**Blast radius:** silent wrong answer

### E-ADAPTERS-10 — COPY targets a schema-qualified table
**Shape:** boundary
**Setup:** A PostgreSQL application keeps tenant tables outside `public` and calls `CopyFrom(ctx, "tenant_42.products", columns, rows)`.
**What the consumer does:** They expect the normal PostgreSQL spelling to reach `tenant_42.products`, or a clear rejection that says the API accepts only one identifier component.
**What must happen:** The COPY API accepts a schema and relation as distinct identifier parts, or rejects dotted input before contacting PostgreSQL.
**Today:** ❌ wrong or unhandled
**Evidence:** The public adapter accepts one `table string` (`crud/adapter/crudpgx/crudpgx.go:117-125`) and wraps that entire string as the single-element `pgx.Identifier{table}` before calling pgx (`:119-124`); it has no schema parameter or dotted-name validation. The integration COPY test uses only `"users"` (`test/integration/driver_pgx_test.go:110-124`), and no schema-qualified COPY test was found.
**Blast radius:** confusing error

### E-ADAPTERS-11 — COPY is asked to import an empty feed
**Shape:** boundary
**Setup:** A scheduled import has no rows after validation but still takes the same COPY path as a non-empty file.
**What the consumer does:** They expect `CopyFrom` to return zero, make no durable change, and avoid turning an empty normal condition into a driver-specific protocol error.
**What must happen:** The empty input is either a tested no-op with count zero or a documented, deliberate refusal; it must not be left to a version-specific driver behaviour.
**Today:** ❓ unverified
**Evidence:** `crudpgx.Executor.CopyFrom` forwards every `[][]any`, including an empty slice, unchanged to `pgx.CopyFromRows` (`crud/adapter/crudpgx/crudpgx.go:117-125`). The only live COPY test passes two rows (`test/integration/driver_pgx_test.go:110-124`); no zero-row COPY case was found in `crud/adapter/crudpgx` or its adjacent integration test.
**Blast radius:** confusing error

### E-ADAPTERS-12 — Two classifier options disagree
**Shape:** misuse
**Setup:** A base package supplies `WithFaults(sqlfault.New("postgres"))` and a feature package appends its own classifier, accidentally using a nil or different vocabulary.
**What the consumer does:** They expect one declared error contract per data source; adding an extension should not silently replace its code table because option order changed.
**What must happen:** Duplicate classifier options are refused, or their last-wins rule and its consequences are explicit and tested for both adapters.
**Today:** 🟡 partial
**Evidence:** Both option collectors apply every option in order and retain the final field value (`crud/adapter/crudsql/crudsql.go:48-68`; `crud/adapter/crudpgx/crudpgx.go:50-74`), while `WithFaults` is an unconditional replacement in both (`crudsql.go:53-58`; `crudpgx.go:55-60`). The tests prove one supplied classifier changes the result (`crud/adapter/crudsql/classify_test.go:25-64`) but do not exercise duplicate ordering or a feature-composed option list.
**Blast radius:** silent wrong answer

## Edge verdict

The riskiest fresh behaviour is configuration that remains mutable after the
adapter appears assembled: a shared `*sql.TxOptions` can change isolation or
read-only policy for later transactions without changing the source value. The
adapters also defer nil dependency and dialect mistakes to request-time crashes,
and `crudsql` deliberately has no way to retain a close-time cursor error. The
pgx COPY entry point is careful to use pgx identifiers, but its one-string shape
cannot express the schema-qualified table a PostgreSQL deployment normally
names. Connection ownership, pgx connection sharing, commit cancellation and
empty COPY are not falsely called safe here: the implementation exposes the
seams, but the adjacent tests do not pin the consumer contract.

## Release blockers found here (edge)

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `WithTxOptions` advertises a copied source but preserves the caller's mutable `*sql.TxOptions` pointer, so a later mutation can silently change isolation or `ReadOnly` for live transactions. | serious | A reservation flow can run at a weaker isolation than the source value a reviewer approved; concurrent mutation additionally makes the policy race-dependent. |
| 2 | `crudsql.rows.Close` drops its only error, after repository paths have already decided to return success from the preceding `Rows.Err` check. | serious | A database-side failure at the cursor boundary can be reported as a successful read, with no error contract left for the caller to inspect. |
