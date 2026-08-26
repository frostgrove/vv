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
dsn, err := vvdb.DSN(cfg)          // 0. the string, and nothing opened
sqlDB, err := vvdb.Open(cfg)       // 1. a *sql.DB, pool sized
pool := dbpgx.MustConnect(ctx, cfg) // 1. a *pgxpool.Pool — utils/vvdb/dbpgx
src := crudsql.Postgres(sqlDB)     // 2. and now it is vv's
```

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
  params:
    application_name: orders
  pool:
    max_open: 20
    max_idle: 5
    max_lifetime: 30m
    max_idle_time: 5m
    connect_timeout: 5s
  replica:
    host: replica.internal  # inherits everything it does not restate
```

The tags are `yaml` and `env`, so [vvcfg](vvcfg.md) loads it with no glue:

```go
type Config struct {
    Addr string      `yaml:"addr" env:"ADDR"`
    DB   vvdb.Config `yaml:"db"`
}

cfg   := vvcfg.Must(vvcfg.Auto[Config](os.Args[1:]))
sqlDB := vvdb.MustOpen(cfg.DB)
```

| | |
|---|---|
| `DSN(cfg)` | the string for the engine the config names |
| `PostgresDSN` `MySQLDSN` `MariaDBDSN` `SQLiteDSN` | the same, when the caller already knows the engine |
| `Open(cfg)` / `MustOpen(cfg)` | a `*sql.DB` with the pool applied |
| `OpenReadWrite(cfg)` | primary and replica; the second is nil when none is declared |
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

The default names are `pgx`, `mysql` and `sqlite`. On lib/pq set
`driver: postgres`; on mattn's SQLite set `driver: sqlite3`.

## What it refuses, and why each one

| | |
|---|---|
| an engine that is not one of the four | reading an unknown engine as the default one connects to the wrong server and says nothing ([[D-013]]) |
| `dsn` set beside `host`, `name`, … | two sources of truth, one of them silently ignored |
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

## ORMs

There is no module for ent, gorm, sqlx or sqlc, and there does not need to be:
each takes either a `*sql.DB` or a string, and both already exist.

```go
sqlDB := vvdb.MustOpen(cfg.DB)
client := entmodel.NewClient(entmodel.Driver(entsql.OpenDB(dialect.Postgres, sqlDB)))

dsn, _ := vvdb.MySQLDSN(cfg.DB)
gormDB, _ := gorm.Open(mysql.Open(dsn), &gorm.Config{})
```

## See also

- [dbpgx](dbpgx.md) — the same configuration, a pgx pool
- [vvcfg](vvcfg.md) — the loader that fills the struct
- [crudsql](crudsql.md) — what takes the handle afterwards
- [flows/FL-021](../../flows/FL-021-a-configuration-becomes-a-connection.md)
