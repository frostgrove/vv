# FL-021 — A configuration becomes a connection

**Entry point:** `vvdb/dsn.go:DSN`, and `vvdb/open.go:Open` above it
**Implements:** [[UC-021]]

One struct, four engines, two syntaxes. Nothing on this path runs during a
request: every refusal here happens while the process is starting
([[D-021]], [[D-057]]).

The path ends where vv begins. `Open` answers a `*sql.DB` and stops; the hop
into the framework is the caller's next line, `crudsql.Postgres(db)` or
`crudpgx.Open(pool)`. That is [[D-057]] made visible rather than documented.

Line numbers were correct when this was written. The symbol name is the thing
that has to still exist.

## The path

1. **`Open`** — `vvdb/open.go:Open`
   Calls `DSN`, then `DriverName`, then `sql.Open`. It registers no driver: the
   consumer's blank import did that, which is what keeps this package free of a
   dependency and out of a module of its own ([[D-033]]).

2. **`DSN`** — `vvdb/dsn.go:DSN`
   Dispatches on `Config.Engine` to one of four builders. An engine outside the
   closed set is `ErrEngine` here, before anything is assembled ([[D-013]]).

3. **`prepare`** — `vvdb/dsn.go:prepare`
   The two questions every builder asks first. A `Config.DSN` set beside the
   fields it would override is `ErrConflict`; a `Config.DSN` on its own is
   returned as it arrived and the builder has nothing left to do.

4. **`Config.validateFields`** — `vvdb/config.go:validateFields`
   What the engine cannot do without, and what belongs to another engine. This
   is where `path` on a server engine, `host` on SQLite and a `:` in a MySQL
   user name are refused. The last one is not a style rule: the driver splits
   user from password at the *first* colon, so half the name would silently
   become the password.

5. **The four builders** — one per engine, and four rather than one switch for
   the same reason `crudsql` has four constructors: MySQL and MariaDB share a
   driver and a syntax today and are still two declarations ([[D-046]]).

   | engine | function | shape |
   |---|---|---|
   | PostgreSQL | `vvdb/dsn.go:PostgresDSN` | a URI, assembled by `net/url` |
   | MySQL | `vvdb/dsn.go:MySQLDSN` | `user:pass@tcp(host:port)/name?…`, which is not a URI |
   | MariaDB | `vvdb/dsn.go:MariaDBDSN` | the same shape, its own declaration |
   | SQLite | `vvdb/dsn.go:SQLiteDSN` | `file:path?…` |

6. **`tlsParam`** — `vvdb/dsn.go:tlsParam`
   `sslmode` is spelled in PostgreSQL's vocabulary for every engine, because one
   configuration has to spell it one way. PostgreSQL reads it directly; the
   MySQL family gets `tls=false|preferred|skip-verify|true`. `verify-ca` has no
   MySQL spelling and is `ErrUnsupported` rather than a downgrade to
   `skip-verify`, which would claim a verification nobody performs.
   Empty is not an ambient driver default: it resolves to verified TLS
   (`verify-full` / `tls=true`). Local plaintext is explicit `disable`; the
   compatibility modes `allow`/`prefer` explicitly permit fallback and are
   never defaults. A Unix socket has no hostname to verify,
   so its typed configuration must state that waiver rather than silently
   defeating the default.

7. **`seconds`** — `vvdb/dsn.go:seconds`
   `connect_timeout` is whole seconds and `0` there means no timeout at all, so
   a sub-second duration rounds **up**.

8. **`Pool.apply`** — `vvdb/open.go:apply`
   The four limits onto `database/sql`'s setters. A zero is left alone: writing
   it would be a pool that can open nothing rather than one with no limit.

9. **`Config.ReadReplica`** — `vvdb/config.go:ReadReplica`
   The replica as it will be opened: the primary with the replica's non-empty
   fields laid over it. `vvdb/open.go:OpenReadWrite` opens both, and closes the
   primary if the second fails. The pair is what `crud.ReadWrite` takes
   ([[D-032]]).

10. **`dbpgx.Connect`** — `vvdb/dbpgx/dbpgx.go:Connect`
    The same first three steps, then `pgxpool.ParseConfig`, then the pool
    section onto pgx's names and the caller's `Option`s. Unlike `sql.Open` this
    dials, so an absent server fails here. The pair helper accepts only
    `ReadWriteOption`: `Common` is copied to both configurations, while
    `Primary` and `Replica` remain on their declared side. Common runs first,
    so a side-specific option has final say. Credential and IAM hooks belong
    to a side; a common credential hook is an explicit and dangerous choice.

11. **Display boundary** — `vvdb/secret.go`
    `Password` and a raw `DSN` are `Secret`; `Params` redacts every value.
    Value-rendering `fmt` verbs, JSON, YAML, TOML and `slog` therefore cannot turn a
    boot diagnostic into a credential leak. `RedactedDSN` renders the useful
    host/database target without credentials, query values or a fragment.
    `RedactError` hides untrusted parser/driver text while retaining its cause
    for `errors.Is/As`.

## Where the escaping actually lives

The two engines mangle different characters, and this is the part a string
comparison cannot check on its own.

- **PostgreSQL** — `net/url` does all of it. `url.UserPassword` percent-encodes
  the password; `url.Values.Encode` the query.
- **MySQL** — the password is deliberately **not** escaped, because the driver
  does not unescape it. It finds the field by taking the last `@` before the
  last `/`, and both of those are ours.
- **MySQL parameters and database name** — escaped, and this one is not
  cosmetic. The driver locates the database name by scanning back to the last
  `/` in the whole string, so an unescaped `loc=Europe/Moscow` makes it read
  `Moscow` as the database and fail on everything before it.
- **Sockets** — a `host` starting with `/` is not a host in either syntax:
  PostgreSQL takes it as `?host=…`, MySQL as `unix(…)`. Since neither has a
  hostname to verify, the configuration must say `sslmode: disable`.

`parseTime=true` is written for the MySQL family unless `params` overrides it.
It is the one default here that changes what the database returns, and it is a
default because without it a `DATETIME` arrives as bytes and the failure names a
column rather than the missing parameter.

## What the fx binding hands out

`crudsqlfx.Module` is where this path ends for a graph rather than for a call:
it opens the pool, reads the schema under a deadline, checks the connection on
the start hook, and provides the `crud.Source` every repository in the graph
depends on.

That source is the composed one. The deployment writes the chain around it —
`crudsqlfx.Module(&configuration.Db, crudsqlfx.Layers("otel.source", "slow-query"))`,
outside in — and each layer is contributed as a `crudsqlfx.Wrapping` through
`crudsqlfx.AsWrapping` or the group tag `group:"vv.crud.source.wrappings"`
written out. A `Wrapping` carries a name and a function and no place of its own:
the root is the only thing that knows what the other layer is.

Both directions are refusals, and each fails the graph before anything is
wrapped: a declared layer nobody contributed (a forgotten `AsWrapping` is
exactly this), a contributed layer nobody declared, one name twice, a nameless
or functionless layer, and a layer that answers with no source ([[D-137]]). An
empty declaration is the source itself. What a layer forwards is the
contributor's obligation ([[D-061]]).

The source this module wires is also addressable on its own, under
`name:"vv.crudsql.base"`. A graph that cannot reach a database supplies one with
`crudsqlfx.Base(source)` and keeps the declared chain around it; replacing
`crud.Source` itself still means the other thing — this value is the source,
layers included.

The layers are contributed rather than decorated because `fx.Replace` is itself
a decorator: a composition root that decorated this source would collide with
every harness that replaces it, and a decorator declared in a neighbouring
module would wrap nobody in silence.

## Files

| File | What it holds |
|---|---|
| `vvdb/config.go` | `Config`, `Pool`, `Engine`, the sentinels, `Validate`, `ReadReplica`, `DriverName` |
| `vvdb/secret.go` | `Secret`, redacted `Params`, and `RedactedDSN` |
| `vvdb/redacted_error.go` | cause-preserving display boundary for driver/parser failures |
| `vvdb/dsn.go` | the four builders, `DSN`, `prepare`, `tlsParam`, `seconds` |
| `vvdb/open.go` | `Open`, `MustOpen`, `OpenReadWrite`, `Pool.apply` |
| `vvdb/doc.go` | the boundary: who opens the connection |
| `vvdb/dbpgx/dbpgx.go` | `Connect`, `MustConnect`, `ConnectReadWrite`, `Option`, and the scoped `Common`/`Primary`/`Replica` declarations |
| `crud/adapter/crudsql/crudsqlfx/crudsqlfx.go` | `Module`, `Open`, the bounded schema read, the start-hook connection check |
| `crud/adapter/crudsql/crudsqlfx/wrapping.go` | `Wrapping`, `AsWrapping`, `Layers`, `Base`, the group, the base name, the sentinels, the composition |

## Tests that walk this flow

| Test | What it pins |
|---|---|
| `vvdb/dsn_test.go:TestEachEngineIsBuiltInItsOwnSyntax` | the four shapes |
| `vvdb/dsn_test.go:TestAPasswordSurvivesEveryPunctuationMark` | escaped for one engine, deliberately not for the other |
| `vvdb/dsn_test.go:TestAParameterHoldingASlashIsEscapedForMySQL` | the `Europe/Moscow` failure |
| `vvdb/dsn_test.go:TestWhatAnEngineCannotExpressIsRefusedRatherThanDowngraded` | `verify-ca` on MySQL |
| `vvdb/dsn_test.go:TestADSNIsUsedAsGivenAndRefusesToShareTheJob` | the escape hatch, and that it is whole or absent |
| `vvdb/dsn_test.go:TestASubSecondConnectTimeoutDoesNotBecomeForever` | rounding up |
| `vvdb/config_test.go:TestAReplicaInheritsEverythingItDoesNotRestate` | inheritance |
| `vvdb/config_test.go:TestAReplicaIsValidatedAsItWillBeOpened` | the merge is what is checked, with the control case beside it |
| `vvdb/open_test.go:TestOpenSizesThePool` | the pool section reaches the handle |
| `vvdb/open_test.go:TestAnUnsetPoolLimitIsLeftAlone` | the control: zero is not a limit |
| `vvdb/open_test.go:TestAFailureToOpenDoesNotPrintThePassword` | the DSN never reaches an error message |
| `vvdb/secret_test.go` | formatter/logger redaction, support-safe DSN and verified-TLS default |
| `utils/vvcfg/vvcfg_test.go:TestVVDBSecretsLoadNormallyAndRenderRedacted` | YAML/env input remains usable while JSON/YAML/TOML output is redacted |
| `vvdb/dbpgx/dbpgx_test.go:TestTheConfigReachesPgx` | the pool section onto pgx's names |
| `vvdb/dbpgx/readwrite_options_test.go` | common hooks reach both configurations while credentials stay on their declared side; caller slices are snapshotted |
| `test/dsn/dsn_test.go` | **the real parsers read back what was written** — pgx and go-sql-driver, which `vvdb` cannot import |
| `test/dsn/dsn_test.go:TestAnUnescapedParameterIsWhyTheEscapingExists` | the control: the driver does reject the unescaped form |
| `test/integration/vvdb_test.go:TestOneConfigShapeOpensEveryEngine` | three live servers from one shape of config |
| `test/integration/vvdb_test.go:TestAWrongPasswordIsRefusedByTheServer` | the control: the credentials are actually travelling |
| `crud/adapter/crudsql/crudsqlfx/activation_test.go:TestTheSchemaTheGraphReadsIsAskedForUnderADeadline` | the one read that cannot leave a constructor is bounded |
| `crud/adapter/crudsql/crudsqlfx/wrapping_test.go:TestTheSourceTheGraphHandsOutCarriesTheDeclaredChain` | the graph hands out the composed source, in declared order |
| `crud/adapter/crudsql/crudsqlfx/wrapping_test.go:TestASourceNoDeploymentWrappedIsTheSourceTheModuleWired` | the control: no declaration wraps nothing |
| `crud/adapter/crudsql/crudsqlfx/wrapping_test.go:TestAContributionThatNeverReachedTheGroupStopsTheStart` | a forgotten `AsWrapping` stops the start instead of measuring nothing |
| `crud/adapter/crudsql/crudsqlfx/wrapping_test.go:TestAReplacedBaseStillCarriesTheDeclaredChain` | an offline harness keeps the chain |

`test/dsn` exists because `vvdb` is in the root module and may not import a
driver ([[D-036]]). On its own it can only compare strings against a rule this
repository invented; those tests parse with the parsers that decide.

## Traps

**A string comparison agrees with itself.** Every rule above is about what a
*driver* does with the string. `test/dsn` is the only place that asks one.

**`sql.Open` does not connect.** A wrong driver name fails immediately, a wrong
password does not fail until the first statement. `dbpgx.Connect` differs here
and is the exception rather than the rule.

**The DSN carries the password.** Neither `Open` nor `Connect` puts the string
or third-party parser text in a displayed error. The safe wrapper still unwraps
to the original cause. Log `RedactedDSN`, not `DSN`; `Secret` also protects a
whole config that is logged by accident.
