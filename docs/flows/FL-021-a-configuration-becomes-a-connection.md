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
    dials, so an absent server fails here.

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
  PostgreSQL takes it as `?host=…`, MySQL as `unix(…)`.

`parseTime=true` is written for the MySQL family unless `params` overrides it.
It is the one default here that changes what the database returns, and it is a
default because without it a `DATETIME` arrives as bytes and the failure names a
column rather than the missing parameter.

## Files

| File | What it holds |
|---|---|
| `vvdb/config.go` | `Config`, `Pool`, `Engine`, the sentinels, `Validate`, `ReadReplica`, `DriverName` |
| `vvdb/dsn.go` | the four builders, `DSN`, `prepare`, `tlsParam`, `seconds` |
| `vvdb/open.go` | `Open`, `MustOpen`, `OpenReadWrite`, `Pool.apply` |
| `vvdb/doc.go` | the boundary: who opens the connection |
| `vvdb/dbpgx/dbpgx.go` | `Connect`, `MustConnect`, `ConnectReadWrite`, `Option` |

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
| `vvdb/dbpgx/dbpgx_test.go:TestTheConfigReachesPgx` | the pool section onto pgx's names |
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
in an error, and the tests keep it that way.
