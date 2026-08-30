# crudsql — database/sql, and therefore everything

```go
import "github.com/frostgrove/vv/crud/adapter/crudsql"
```

**Module:** root — `database/sql` is standard library, so it costs nothing
· **Depends on:** `crud`, `errs`, `database/sql`

One adapter covers every stack that speaks `database/sql`: ent, gorm, sqlx,
sqlc, bun, squirrel, dbr, and no ORM at all.

---

## Opening one

```go
db := crudsql.Postgres(sqlDB)
db := crudsql.MySQL(sqlDB)
db := crudsql.MariaDB(sqlDB)   // same dialect as MySQL, different error numbers
db := crudsql.SQLite(sqlDB)

users := Users.Bind(db)
```

**Use a named constructor.** It says which *engine* is answering, and that is
what lets a refused statement come back carrying a code — `unique`,
`foreign_key`, `required` — as well as `crud.ErrConflict`.

`crudsql.Open(db, crud.Postgres{})` takes a `crud.Dialect` instead, which says
how to write SQL and **not** which server is speaking: `crud.MySQL` is MariaDB
too, and the two answer a failed `CHECK` with different numbers. So `Open`,
`From` and `Source` classify the *status* and not the *code* — vv refuses to
guess rather than answering "mysql" for a MariaDB server ([[D-046]]).

## Joining someone else's transaction

The shortest interop path is a method on the canonical source:

```go
db := crudsql.Postgres(sqlDB)
ctx = db.BindExecutor(ctx, tx)
```

Repositories built from `db` run on `tx`; repositories on another database do
not. A new framework means finding where it hides its transaction handle. The
joined executor inherits `db`'s declared engine classifier. The general form is
`crud.BindExecutor(ctx, db, crudsql.From(tx))` for callers who want to construct
that executor themselves.

| Stack | How |
|---|---|
| `*sql.DB` / `*sql.Tx` / `*sql.Conn` | `crudsql.Postgres(db)` · `crudsql.From(tx)` |
| sqlx | `crudsql.From(sqlxTx)` — its promoted `Commit`/`Rollback` lifecycle is recognised as transactional |
| gorm | `crudsql.From(tx.Statement.ConnPool)` inside `db.Transaction` |
| ent (`--feature sql/execquery`) | `crudsql.From(entTx)` — `*ent.Tx` has `ExecContext`/`QueryContext` |
| sqlc (database/sql) | `crudsql.From(tx)`; the same `*sql.Tx` goes to `sqlc.New(tx)` |
| bun, squirrel, dbr, … | `crudsql.From(tx)` |

`*sql.Tx` and wrappers that retain `Commit() error` plus `Rollback() error` are
recognised as transactions. This includes sqlx, ent and Gorm's prepared
transaction handle; `*sql.DB` and `*sql.Conn` do not expose that lifecycle and
remain non-transactional. For an opaque wrapper that deliberately hides it, pass
`crudsql.WithTransaction()` to `From` or `BindExecutor` explicitly.

```go
err := gormDB.Transaction(func(tx *gorm.DB) error {
    ctx := db.BindExecutor(ctx, tx.Statement.ConnPool)
    return users.SaveOnly(ctx, &u)
})
```

The source is mandatory on the safe path because a `*sql.Tx` cannot reveal its
`*sql.DB`. The old `crud.WithExecutor` spelling now fails with
`crud.ErrExecutorScope` instead of falling back to the pool. The low-level form
is `crud.WithExecutorFor(ctx, mainDB, e)`; unconditional legacy routing requires
the explicit `crud.WithUnsafeExecutor` opt-out ([[D-082]], [[UC-012]]).

## Anything that is not a `*sql.DB`

```go
type Queryer interface {
    ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
    QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}
```

`crudsql.From(q, opts...)` wraps one — typically a transaction somebody else
owns. `crudsql.Source(q, dialect, opts...)` builds a full `crud.Source` over one,
for when a framework hands you a handle rather than a `*sql.DB` and you want to
build repositories directly on it.

`Open` is what you use when you have the `*sql.DB` and want transactions too.

## Codes on a joined transaction

`From` and `Source` name no engine, so tell them:

```go
cls := sqlfault.New("postgres")
ctx = db.BindExecutor(ctx, tx, crudsql.WithFaults(cls))
```

The named constructors already carry the same classifier automatically; the
option above is an explicit override. With the
[catalog](catalog.md) wired into it, violations also carry the columns the driver
did not name:

```go
cat, _ := catalog.Load(ctx, crudsql.Postgres(sqlDB))
cls := sqlfault.New("postgres", sqlfault.WithColumns(sqlfault.FromCatalog(cat)))
db  := crudsql.Postgres(sqlDB, crudsql.WithFaults(cls))
```

## Transactions and savepoints

```go
db := crudsql.Postgres(sqlDB).WithTxOptions(&sql.TxOptions{
    Isolation: sql.LevelSerializable,
})
```

`WithTxOptions` snapshots the value. Mutating the struct passed to it later does
not reconfigure the source or race with a concurrent `Begin`.

**Migration:** the previously exported `DB.TxOptions` field was removed because
it exposed shared mutable configuration. Replace direct field assignment with
`db = db.WithTxOptions(options)`. Passing nil restores the driver default.

`DB.Begin` opens one; `Begin` on the resulting `crud.Tx` gives a **savepoint**,
emitted as `SAVEPOINT` / `ROLLBACK TO SAVEPOINT` / `RELEASE SAVEPOINT`.

`db.DB()` hands the `*sql.DB` back where you need it.

**Deferred constraints classify too.** A constraint deferred to `COMMIT` fires
there rather than at the statement, and `Tx.Commit` classifies it — so the same
violation is a 409 through both doors.

## For and Wired — the engine switch and the error subsystem

| | |
|---|---|
| `Engine` | `EnginePostgres`, `EngineMySQL`, `EngineMariaDB`, `EngineSQLite` — the four values `vvdb.Engine` uses |
| `For(engine, db, opts…)` | binds a handle to the engine *named*, rather than to one of the four constructors chosen in source |
| `Wired(ctx, engine, db, opts…)` | `For` with the classifier and the catalog in place |
| `ErrEngine` | an engine that is empty or not one of the four |

```go
source, err := crudsql.Wired(ctx, crudsql.Engine(cfg.Db.Engine), db)
```

Three pieces have to be wired for a refused write to say what was wrong with it,
and leaving any of them out is silent — which is why `Wired` is one call rather
than three lines every application copies:

1. **the classifier**, so a duplicate address is the code `unique` rather than a
   driver sentence carrying the schema ([[D-044]]);
2. **the catalog**, so the classifier can answer which columns that constraint
   covers. PostgreSQL names the constraint and the table on a unique violation
   and **no column at all** — without the catalog the violation arrives with no
   field, and everything looks wired while the form has no input to mark;
3. `faults.Enrich` on each repository, which turns the column into the model
   field. That one is per-repository and stays yours.

The schema is read at start-up rather than lazily: a lazy loader cannot fail at
start-up, and a schema lookup that quietly returns nothing is how the field
disappears again ([[D-041]]).

The engine switch exists here so an application that gains SQLite for its tests
does not gain a switch of its own that will disagree with this one about what
`mariadb` means — MariaDB and MySQL share a driver, a dialect and a wire protocol
and answer a failed CHECK with two different numbers ([[D-046]]).

## crudsqlfx — the fx wiring

```go
import "github.com/frostgrove/vv/crud/adapter/crudsql/crudsqlfx"

fx.Options(crudsqlfx.Module(&configuration.Db))
```

**Module** — it takes uber/fx, so a consumer who opens their own pool never
resolves one ([[D-074]]).

It provides a `*sql.DB` and a `crud.Source` over it, pings the pool at
construction and closes it on shutdown. `sql.Open` validates a DSN and connects
to nothing, so without the ping a wrong host, a wrong password and a database
that is not running all look like a healthy start-up.

It registers **no driver**. Which driver answers `sql.Open("pgx", …)` is yours
and always was ([[D-057]]) — import it for its side effect beside this.

## See also

- [crudpgx](crudpgx.md) — pgx v5, with `COPY` bulk insert
- [sqlfault](sqlfault.md) — what `WithFaults` takes
- [usage-guides/ent.md](../../usage-guides/ent.md) · [usage-guides/gorm.md](../../usage-guides/gorm.md)
- [[UC-005]] run repository work in an ORM transaction · [[UC-010]] adopt an existing ORM model
- [[FL-009]] transactions: joining, opening, which database
