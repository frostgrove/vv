# dbpgx — the same configuration, a pgx pool

```go
import "github.com/frostgrove/vv/utils/vvdb/dbpgx"
```

**Module:** `github.com/frostgrove/vv/utils/vvdb/dbpgx` · **Depends on:** pgx v5

[vvdb](vvdb.md) opens a `*sql.DB` with the standard library alone. `pgxpool` is
not `database/sql`, so it is a module of its own and a consumer on ent, gorm or
`database/sql` never takes pgx for it ([[D-033]], [[D-051]]).

---

## Using it

```go
pool := dbpgx.MustConnect(ctx, &cfg.DB)
defer pool.Close()

repo := Products.Bind(crudpgx.Open(pool))
```

| | |
|---|---|
| `Connect(ctx, cfg, opts…)` | build the string, size the pool, dial |
| `MustConnect(ctx, cfg, opts…)` | the same, panicking — for `main` |
| `ConnectReadWrite(ctx, cfg, rwOpts…)` | primary and replica with explicitly scoped options; the second is nil when none is declared |
| `MustConnectReadWrite(ctx, cfg, rwOpts…)` | the same pair, panicking — for `main` |
| `Apply(pc, pool)` | apply portable pool sizing to a `pgxpool.Config` the application owns |

`pgxpool.NewWithConfig` is lazy. `Connect` calls `Ping` before returning, so a
server that is not there fails here rather than at the first query. As with
`vvdb.Open`, `Connect` and `MustConnect` reject a declared replica; use the
pair-returning helpers so it cannot be silently ignored.

## Options reach pgx directly

```go
pool := dbpgx.MustConnect(ctx, cfg.DB, func(pc *pgxpool.Config) {
    pc.ConnConfig.Tracer = myTracer
})
```

An `Option` runs after vvdb's fields have been applied. It is the escape hatch
for what one portable configuration cannot describe for four engines — a
tracer, an `AfterConnect` hook, a custom type map.

The read/write helpers deliberately do not accept a bare `Option`. Declare
whether each hook is common or belongs to one side:

```go
primary, replica := dbpgx.MustConnectReadWrite(ctx, &cfg.DB,
    dbpgx.Common(tracing),
    dbpgx.Primary(writerIAM),
    dbpgx.Replica(readerIAM),
)
```

Common options run first and side-specific options run afterwards. Tracing and
type registration are usually common. Credentials, IAM token providers and
role-changing hooks must never be common: sharing them can grant the replica's
identity to the writable pool. The constructors snapshot their option slices;
mutating the caller's slice after declaration cannot reconfigure either pool.

## What the pool section maps onto

| vvdb | pgx |
|---|---|
| `max_open` | `MaxConns` |
| `max_idle` | `MinConns` — a floor rather than a ceiling, which is the closest pgx has |
| `max_lifetime` | `MaxConnLifetime` |
| `max_idle_time` | `MaxConnIdleTime` |
| `connect_timeout` | `ConnConfig.ConnectTimeout` |

A zero is left alone. pgx's own default for `MaxConns` is four connections or
the number of CPUs, and writing a 0 over it would be a pool that can open
nothing.

A config naming another engine is refused rather than coerced: `dbpgx` speaks
to PostgreSQL, and `engine: mysql` here is a mistake worth a message.

## See also

- [vvdb](vvdb.md) — the configuration and the string
- [crudpgx](crudpgx.md) — what takes the pool afterwards
