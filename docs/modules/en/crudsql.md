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

The interop point is exactly one function:

```go
ctx = crud.WithExecutor(ctx, crudsql.From(tx))
```

Every repository call made with that context runs on that executor. A new
framework means finding where it hides its transaction and wrapping it — three
lines.

| Stack | How |
|---|---|
| `*sql.DB` / `*sql.Tx` / `*sql.Conn` | `crudsql.Postgres(db)` · `crudsql.From(tx)` |
| sqlx | `crudsql.From(sqlxTx)` — it is a `*sql.Tx` underneath |
| gorm | `crudsql.From(tx.Statement.ConnPool)` inside `db.Transaction` |
| ent (`--feature sql/execquery`) | `crudsql.From(entTx)` — `*ent.Tx` has `ExecContext`/`QueryContext` |
| sqlc (database/sql) | `crudsql.From(tx)`; the same `*sql.Tx` goes to `sqlc.New(tx)` |
| bun, squirrel, dbr, … | `crudsql.From(tx)` |

```go
err := gormDB.Transaction(func(tx *gorm.DB) error {
    ctx := crud.WithExecutor(ctx, crudsql.From(tx.Statement.ConnPool))
    return users.Save(ctx, &u)
})
```

**With more than one database, say which one you mean** — `crud.WithExecutorFor(ctx, mainDB, e)`.
The plain form captures every repository, which is the interop seam working as
designed and also how a write lands in the wrong database and reports success
([[UC-012]]).

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
ctx = crud.WithExecutor(ctx, crudsql.From(tx, crudsql.WithFaults(cls)))
```

That is the same `errs.Classifier` the named constructors take. With the
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

`DB.Begin` opens one; `Begin` on the resulting `crud.Tx` gives a **savepoint**,
emitted as `SAVEPOINT` / `ROLLBACK TO SAVEPOINT` / `RELEASE SAVEPOINT`.

`db.DB()` hands the `*sql.DB` back where you need it.

**Deferred constraints classify too.** A constraint deferred to `COMMIT` fires
there rather than at the statement, and `Tx.Commit` classifies it — so the same
violation is a 409 through both doors.

## See also

- [crudpgx](crudpgx.md) — pgx v5, with `COPY` bulk insert
- [sqlfault](sqlfault.md) — what `WithFaults` takes
- [usage-guides/ent.md](../../usage-guides/ent.md) · [usage-guides/gorm.md](../../usage-guides/gorm.md)
- [[UC-005]] run repository work in an ORM transaction · [[UC-010]] adopt an existing ORM model
- [[FL-009]] transactions: joining, opening, which database
