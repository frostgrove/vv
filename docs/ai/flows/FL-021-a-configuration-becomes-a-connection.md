# FL-021 — A configuration becomes a connection

**Entry point:** `utils/vvdb/dsn.go:DSN`, and `utils/vvdb/open.go:Open` above it
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

1. **`Open`** — `utils/vvdb/open.go:Open`
   Calls `DSN`, then `DriverName`, then `sql.Open`. It registers no driver: the
   consumer's blank import did that, which is what keeps this package free of a
   dependency and out of a module of its own ([[D-033]]).

2. **`DSN`** — `utils/vvdb/dsn.go:DSN`
   Dispatches on `Config.Engine` to one of four builders. An engine outside the
   closed set is `ErrEngine` here, before anything is assembled ([[D-013]]).

3. **`prepare`** — `utils/vvdb/dsn.go:prepare`
   The two questions every builder asks first. A `Config.DSN` set beside the
   fields it would override is `ErrConflict`; a `Config.DSN` on its own is
   returned as it arrived and the builder has nothing left to do.

4. **`Config.validateFields`** — `utils/vvdb/config.go:validateFields`
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
   | PostgreSQL | `utils/vvdb/dsn.go:PostgresDSN` | a URI, assembled by `net/url` |
   | MySQL | `utils/vvdb/dsn.go:MySQLDSN` | `user:pass@tcp(host:port)/name?…`, which is not a URI |
   | MariaDB | `utils/vvdb/dsn.go:MariaDBDSN` | the same shape, its own declaration |
   | SQLite | `utils/vvdb/dsn.go:SQLiteDSN` | `file:path?…` |

6. **`tlsParam`** — `utils/vvdb/dsn.go:tlsParam`
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

7. **`seconds`** — `utils/vvdb/dsn.go:seconds`
   `connect_timeout` is whole seconds and `0` there means no timeout at all, so
   a sub-second duration rounds **up**.

8. **`Pool.apply`** — `utils/vvdb/open.go:apply`
   The four limits onto `database/sql`'s setters. A zero is left alone: writing
   it would be a pool that can open nothing rather than one with no limit.

9. **`Config.ReadReplica`** — `utils/vvdb/config.go:ReadReplica`
   The replica as it will be opened: the primary with the replica's non-empty
   fields laid over it. `utils/vvdb/open.go:OpenReadWrite` opens both, and closes the
   primary if the second fails. The pair is what `crud.ReadWrite` takes
   ([[D-032]]).

10. **`dbpgx.Connect`** — `utils/vvdb/dbpgx/dbpgx.go:Connect`
    The same first three steps, then `pgxpool.ParseConfig`, then the pool
    section onto pgx's names and the caller's `Option`s. Unlike `sql.Open` this
    dials, so an absent server fails here. The pair helper accepts only
    `ReadWriteOption`: `Common` is copied to both configurations, while
    `Primary` and `Replica` remain on their declared side. Common runs first,
    so a side-specific option has final say. Credential and IAM hooks belong
    to a side; a common credential hook is an explicit and dangerous choice.

11. **Display boundary** — `utils/vvdb/secret.go`
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

## Files

| File | What it holds |
|---|---|
| `utils/vvdb/config.go` | `Config`, `Pool`, `Engine`, the sentinels, `Validate`, `ReadReplica`, `DriverName` |
| `utils/vvdb/secret.go` | `Secret`, redacted `Params`, and `RedactedDSN` |
| `utils/vvdb/redacted_error.go` | cause-preserving display boundary for driver/parser failures |
| `utils/vvdb/dsn.go` | the four builders, `DSN`, `prepare`, `tlsParam`, `seconds` |
| `utils/vvdb/open.go` | `Open`, `MustOpen`, `OpenReadWrite`, `Pool.apply` |
| `utils/vvdb/doc.go` | the boundary: who opens the connection |
| `utils/vvdb/dbpgx/dbpgx.go` | `Connect`, `MustConnect`, `ConnectReadWrite`, `Option`, and the scoped `Common`/`Primary`/`Replica` declarations |

## Tests that walk this flow

| Test | What it pins |
|---|---|
| `utils/vvdb/dsn_test.go:TestEachEngineIsBuiltInItsOwnSyntax` | the four shapes |
| `utils/vvdb/dsn_test.go:TestAPasswordSurvivesEveryPunctuationMark` | escaped for one engine, deliberately not for the other |
| `utils/vvdb/dsn_test.go:TestAParameterHoldingASlashIsEscapedForMySQL` | the `Europe/Moscow` failure |
| `utils/vvdb/dsn_test.go:TestWhatAnEngineCannotExpressIsRefusedRatherThanDowngraded` | `verify-ca` on MySQL |
| `utils/vvdb/dsn_test.go:TestADSNIsUsedAsGivenAndRefusesToShareTheJob` | the escape hatch, and that it is whole or absent |
| `utils/vvdb/dsn_test.go:TestASubSecondConnectTimeoutDoesNotBecomeForever` | rounding up |
| `utils/vvdb/config_test.go:TestAReplicaInheritsEverythingItDoesNotRestate` | inheritance |
| `utils/vvdb/config_test.go:TestAReplicaIsValidatedAsItWillBeOpened` | the merge is what is checked, with the control case beside it |
| `utils/vvdb/open_test.go:TestOpenSizesThePool` | the pool section reaches the handle |
| `utils/vvdb/open_test.go:TestAnUnsetPoolLimitIsLeftAlone` | the control: zero is not a limit |
| `utils/vvdb/open_test.go:TestAFailureToOpenDoesNotPrintThePassword` | the DSN never reaches an error message |
| `utils/vvdb/secret_test.go` | formatter/logger redaction, support-safe DSN and verified-TLS default |
| `utils/vvcfg/vvcfg_test.go:TestVVDBSecretsLoadNormallyAndRenderRedacted` | YAML/env input remains usable while JSON/YAML/TOML output is redacted |
| `utils/vvdb/dbpgx/dbpgx_test.go:TestTheConfigReachesPgx` | the pool section onto pgx's names |
| `utils/vvdb/dbpgx/readwrite_options_test.go` | common hooks reach both configurations while credentials stay on their declared side; caller slices are snapshotted |
| `test/dsn/dsn_test.go` | **the real parsers read back what was written** — pgx and go-sql-driver, which `vvdb` cannot import |
| `test/dsn/dsn_test.go:TestAnUnescapedParameterIsWhyTheEscapingExists` | the control: the driver does reject the unescaped form |
| `test/integration/vvdb_test.go:TestOneConfigShapeOpensEveryEngine` | three live servers from one shape of config |
| `test/integration/vvdb_test.go:TestAWrongPasswordIsRefusedByTheServer` | the control: the credentials are actually travelling |

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
