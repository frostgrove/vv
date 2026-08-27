# vvdb — one configuration, four engines, a connection

```go
import "github.com/frostgrove/vv/utils/vvdb"
```

**Module:** root · **Depends on:** the standard library

One struct describes a database. From it come the connection string every
driver wants and, if you want it, an open `*sql.DB` with the pool sized.
PostgreSQL, MySQL, MariaDB and SQLite.

**Why it exists:** the DSN was a string constant in every example in this
repository and hand-assembled a third time in the test corpus. The differences
between the four engines — which port, which escaping, which spelling of "use
TLS" — are small, unmemorable and easy to get subtly wrong, and getting one
wrong connects successfully to the wrong place.

---

## The three levels

```go
dsn, err := vvdb.DSN(cfg)                         // 0. the string, and nothing opened
primary, replica := vvdb.MustOpenReadWrite(cfg)    // 1. database/sql handles, pools sized
src := crudsql.Postgres(primary)                   // 2. and now it is vv's
if replica != nil {
    src = crud.ReadWrite(src, crudsql.Postgres(replica))
}
```

For pgx, use `dbpgx.MustConnectReadWrite(ctx, cfg)` in place of the second
line. Choose one driver family for a handle; the two examples are alternatives,
not two pools to open for the same database.

The last line is not missing an abstraction. **The application opens the
connection and hands it over** — that is what "vv does not own your connection"
means when it is written out, and it is why nothing in this package imports
`crud` ([[D-057]]).

## The configuration

```yaml
db:
  engine: postgres          # postgres | mysql | mariadb | sqlite
  host: localhost
  port: 55432               # omitted: the engine's own
  user: vv
  password: vv
  name: vv
  sslmode: disable          # disable | allow | prefer | require | verify-ca | verify-full
  pragmas:                  # SQLite only
    - journal_mode=WAL
    - busy_timeout=5000
  params:
    application_name: orders
  pool:
    max_open: 20
    max_idle: 5
    max_lifetime: 30m
    max_idle_time: 5m
    connect_timeout: 5s
  migration:
    path: ./migrations
    models: [./src]
    table: goose_db_version
  replica:
    host: replica.internal  # inherits everything it does not restate
```

The tags are `yaml` and `env`, so [vvcfg](vvcfg.md) loads it with no glue:

```go
type Config struct {
    Addr string      `yaml:"addr" env:"ADDR"`
    DB   vvdb.Config `yaml:"db"`
}

func (c Config) Validate() error { return c.DB.Validate() }

cfg            := vvcfg.MustLoad[Config]()
sqlDB, replica := vvdb.MustOpenReadWrite(cfg.DB)
```

`vvcfg` invokes the top-level `Validate` method. Forwarding it deliberately
keeps an application-level validation point while ensuring its database block
is checked during loading. Scalar settings use `DB_*`, including
`DB_POOL_CONNECT_TIMEOUT` (the older `DB_CONNECT_TIMEOUT` is accepted for
compatibility). `DB_PARAMS` is a URL query, for example
`application_name=orders,worker&statement_timeout=5s`; percent-encode `&` or
`=` when they belong to a value. `vvcfg` also applies the matching
`DB_REPLICA_*` names after YAML — for example `DB_REPLICA_HOST`,
`DB_REPLICA_PARAMS` and `DB_REPLICA_POOL_MAX_IDLE` — so an operator can use an
environment-only replica or override the readable YAML declaration.
`MustLoad` uses `./config/app.yml` when neither `--config-path` nor
`CONFIG_PATH` is set. Set `vvcfg.DefaultCfgPath = ""` to use the same
declaration in an image with no config file. An explicit `DB_DSN` or
`DB_REPLICA_DSN` replaces the corresponding
field-form connection from YAML as one unit; a `Config` assembled in Go still
refuses a raw DSN beside typed connection fields.
`DB_SQLITE_PRAGMAS` and `DB_REPLICA_PRAGMAS` use the same comma-separated
`name=value` list. For two database blocks, make cleanenv's prefix part of the
top-level declaration: `Analytics vvdb.Config` with
`yaml:"analytics" env-prefix:"ANALYTICS_"` uses
`ANALYTICS_DB_HOST` and `ANALYTICS_DB_REPLICA_HOST`, never the primary's
variables.

`migration` is primary-only operational metadata used by
[vvgoose](vvgoose.md). It is not part of a DSN and is never inherited by a
read replica. Its environment names are `DB_MIGRATION_PATH`,
`DB_MIGRATION_MODELS`, and `DB_MIGRATION_TABLE`.

| | |
|---|---|
| `DSN(cfg)` | the string for the engine the config names |
| `PostgresDSN` `MySQLDSN` `MariaDBDSN` `SQLiteDSN` | the same fully validated declaration, when the caller already knows the engine |
| `Open(cfg)` / `MustOpen(cfg)` | one `*sql.DB` with the pool applied; they refuse a declared replica |
| `OpenReadWrite(cfg)` / `MustOpenReadWrite(cfg)` | primary and replica; the second is nil when none is declared |
| `cfg.Pool.Apply(db)` | size a `*sql.DB` that the application opened itself |
| `cfg.Pool.Validate()` | refuse contradictory pool settings when an adapter accepts a pool directly |
| `DriverName(cfg)` | the `database/sql` driver name `Open` will use |
| `cfg.Validate()` | refuse a configuration that cannot mean what it says |
| `cfg.ReadReplica()` | the replica as it will be opened, inheritance applied |

## The driver is your import

`Open` calls `sql.Open` and does not register a driver, which is why this
package has no dependency and needs no module:

```go
import _ "github.com/jackc/pgx/v5/stdlib"   // registers "pgx"
import _ "github.com/go-sql-driver/mysql"   // registers "mysql"
```

The default names are `pgx`, `mysql` and `sqlite`. Typed PostgreSQL
configuration is deliberately pgx-only: any driver name other than `pgx` can
have its own ambient configuration or passfile semantics. An application
intentionally using lib/pq — including through a local driver alias — uses a
complete raw `dsn:`. On mattn's SQLite set `driver: sqlite3`.

## What it refuses, and why each one

| | |
|---|---|
| an engine that is not one of the four | reading an unknown engine as the default one connects to the wrong server and says nothing ([[D-013]]) |
| `dsn` set beside `host`, `name`, … | two sources of truth, one of them silently ignored |
| `dsn` set beside `pool.connect_timeout` | a raw string owns its timeout; applying the pool timeout only in some adapters would split behaviour |
| PostgreSQL `PGSERVICE` or `PGSSLNEGOTIATION` beside typed fields | they are a second connection document; use the intentional raw `dsn` escape hatch instead |
| typed PostgreSQL with any `driver` other than `pgx` | an alias can hide lib/pq and reintroduce ambient configuration; use pgx or own a complete raw DSN |
| `sslmode: verify-ca` on MySQL | the driver needs a registered `tls.Config`; downgrading to `skip-verify` would claim a verification nobody performs |
| a `:` in a MySQL user name | the driver splits user from password at the **first** colon |
| `path` on a server engine, `host` on SQLite | the field belongs to another engine and would be dropped |

Everything above is a start-up failure with the field named. A configuration
that is wrong should stop the process before traffic arrives ([[D-021]]).

## The escaping, which is the actual work

PostgreSQL's string is a URI and MySQL's is not one, and they mangle different
characters:

- **PostgreSQL** — the whole string goes through `net/url`, so a password
  holding `@`, `/` or `?` survives percent-encoded.
- **MySQL** — the password is *not* escaped, because the driver does not unescape
  it: it takes the last `@` before the last `/`, and both of those are vvdb's.
  Parameters and the database name **are** escaped, and that one is not
  cosmetic. Written out plainly, `loc=Europe/Moscow` moves where the driver
  thinks the database name ends, and it reads `Moscow` as the database.
- **Sockets** — a `host` beginning with `/` becomes `?host=…` for PostgreSQL and
  `unix(…)` for MySQL. It is not a host in either syntax.

`parseTime=true` is written for the MySQL family unless `params` says otherwise.
It is the one default here that changes what the database returns, and without
it a `DATETIME` arrives as bytes and scanning it into a `time.Time` field fails
somewhere far away from the missing parameter.

SQLite `pragmas` is not a one-value map: `journal_mode=WAL` and
`busy_timeout=5000` are both preserved. vvdb renders repeated `_pragma` keys
for the default `modernc.org/sqlite` driver and its `_journal_mode` /
`_busy_timeout` names for `mattn/go-sqlite3`; unsupported arbitrary SQL is
refused rather than entering a connection string.

## Replicas

`replica` inherits every field it does not restate, so the usual case is one
line. The pair goes to `crud.ReadWrite`, which is what decides that a read goes
to the replica and a write, a locked read and the load half of an update do not
([[D-032]]):

```go
primary, replica, err := vvdb.OpenReadWrite(cfg.DB)
src := crudsql.Postgres(primary)
if replica != nil {
    src = crud.ReadWrite(src, crudsql.Postgres(replica))
}
```

`Open` and `MustOpen` deliberately refuse a configuration that declares
`replica`: returning only the primary would make the YAML look effective while
silently keeping reads on it. Use the pair-returning helpers whenever the
declaration contains a replica.

A replica may be a field-based override as above, or a complete `dsn:`. A
complete replica DSN owns all connection fields; mixing it with `host`, `name`,
or other connection fields is refused as two sources of truth. It still
inherits the primary's `driver` and the non-DSN pool policy (`max_open`,
`max_idle`, lifetime and idle time), so both handles are opened with the same
registered driver and sizing declaration. `connect_timeout` is inside the raw
string and is not inherited.
For the same reason, a field-based replica cannot override an opaque primary
`dsn`: use `replica.dsn` rather than asking vvdb to parse and rewrite a string
it promised to preserve exactly.

## ORMs

There is no module for ent, gorm, sqlx or sqlc, and there does not need to be:
each takes either a `*sql.DB` or a string, and both already exist.

```go
sqlDB := vvdb.MustOpen(cfg.DB)
client := entmodel.NewClient(entmodel.Driver(entsql.OpenDB(dialect.Postgres, sqlDB)))

dsn, err := vvdb.DSN(cfg.DB)
if err != nil { log.Fatal(err) }
gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
if err != nil { log.Fatal(err) }
```

## See also

- [dbpgx](dbpgx.md) — the same configuration, a pgx pool
- [vvcfg](vvcfg.md) — the loader that fills the struct
- [crudsql](crudsql.md) — what takes the handle afterwards
- [flows/FL-021](../../flows/FL-021-a-configuration-becomes-a-connection.md)
