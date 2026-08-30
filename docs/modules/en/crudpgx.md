# crudpgx — pgx v5

```go
import "github.com/frostgrove/vv/crud/adapter/crudpgx"
```

```bash
go get github.com/frostgrove/vv/crud/adapter/crudpgx
```

**Module:** its own — so a consumer on `database/sql` never takes pgx as a
dependency ([[D-033]]) · **Requires:** `github.com/jackc/pgx/v5`

The shortest path there is: no ORM, no `database/sql` in the way. vv talks to
the pool directly.

---

## Opening one

```go
pool, err := pgxpool.New(ctx, dsn)
if err != nil { log.Fatal(err) }
defer pool.Close()

users := Users.Bind(crudpgx.Open(pool))
```

`crudpgx.Open` is the whole adapter. vv asks the pool to run a statement and to
give back rows, and **never opens a connection of its own**.

```go
type Queryer interface {
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}
```

`*pgxpool.Pool`, `*pgx.Conn` and `pgx.Tx` all satisfy it.

## Joining someone else's transaction

```go
src := crudpgx.Open(pool)
err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
    ctx := src.BindExecutor(ctx, tx)
    return users.SaveOnly(ctx, &u)
})
```

The joined executor inherits `src`'s classifier, including catalog or custom
vocabulary options. Pass `crudpgx.WithFaults(...)` to override it explicitly.

With sqlc on pgx this is the whole integration: `pgx.Tx` satisfies sqlc's `DBTX`
and vv's `Queryer` at once, so one transaction feeds both. The general form is
`crud.BindExecutor(ctx, src, crudpgx.From(tx))`; naming `tx` itself as the source
is refused with `crud.ErrExecutorScope` rather than falling back to the pool
([[D-082]]).

## Bulk insert, through COPY

`crudpgx` implements `crud.BulkInserter` with `COPY`. **Nothing in the library
reaches for it.** `SaveAll` writes one multi-row `INSERT` whatever the executor
underneath can do, so this is a door the application opens itself:

```go
if bulk, ok := src.(crud.BulkInserter); ok {
    n, err := bulk.CopyFrom(ctx, "users", cols, rows)
}
```

The compatibility interface accepts one table-name component. For a table in a
non-default PostgreSQL schema, keep the components structured:

```go
n, err := src.CopyFromTable(ctx,
    crud.TableRef{Schema: "tenant_42", Name: "products"},
    cols, rows,
)
```

`CopyFrom(ctx, "tenant_42.products", ...)` is rejected before pgx is called;
it is never guessed or passed as one quoted relation name. `CopyFromTable`
hands pgx the two exact `pgx.Identifier` components.

The call runs on the handle that executor holds and ignores any transaction in
the context ([[UC-008]]).

## Structured error codes

pgx names its own error type, so this adapter classifies typed over
`*pgconn.PgError` rather than by shape. Wire it in:

```go
cls := sqlfault.New("postgres")
db  := crudpgx.Open(pool, crudpgx.WithFaults(cls))
```

With the [catalog](catalog.md), violations also carry the columns PostgreSQL did
not name — a **composite unique** is the case that needs it:

```go
cat, _ := catalog.Load(ctx, crudpgx.Open(pool))
cls := sqlfault.New("postgres", sqlfault.WithColumns(sqlfault.FromCatalog(cat)))
db  := crudpgx.Open(pool, crudpgx.WithFaults(cls))
```

## Transactions and savepoints

`Begin` on a `crudpgx.Tx` returns a real pgx nested transaction — pgx's own
savepoint — rather than vv emitting `SAVEPOINT` text.

**Deferred constraints classify too.** A constraint deferred to `COMMIT` fires
there rather than at the statement, and `Tx.Commit` classifies it. A statement the
server refused inside a savepoint also poisons the transaction, so PostgreSQL
refuses the release with `25P02` — and that door gives a nested `Commit` a code
instead of an anonymous 500.

## See also

- [crudsql](crudsql.md) — `database/sql`, and therefore ent, gorm, sqlx, sqlc, bun
- [sqlfault](sqlfault.md) — what `WithFaults` takes
- [`_examples/pgx-fiber`](../../../_examples/pgx-fiber/) · [`_examples/pgx-grpc`](../../../_examples/pgx-grpc/)
- [[UC-008]] write many rows in one statement · [[FL-009]] transactions
