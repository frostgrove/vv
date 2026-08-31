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

Use the typed repository verb; when metadata yields insert columns, `crudpgx`
supplies PostgreSQL `COPY` behind it automatically:

```go
err := users.InsertBatch(ctx, []*User{&a, &b, &c})
```

The repository derives the exact table, columns and values from metadata,
applies security and fault decorators before reaching the driver, and treats
every row as create-only. An assigned primary key may conflict; it is never an
upsert. The models are not mutated and generated values are not read back.

`InsertBatch` resolves the executor bound to this datasource in `ctx`. Inside a
foreign or vv-owned transaction, COPY therefore runs on that transaction and
rolls back with it. If the resolved handle cannot COPY, or the model has no
insert columns, the repository uses atomic bind-budgeted `INSERT` statements
instead.

COPY and ordinary INSERT do not have identical PostgreSQL table semantics. RLS
and rewrite-rule tables, values that need pgx's ordinary parameter encoding, or
callers requiring ordinary INSERT semantics should select the portable path:

```go
err := users.InsertBatch(ctx, rows, crud.PortableBatch())

var Users = sqlrepo.Define[User, int64, UserUpdate](
    "users",
    sqlrepo.PortableBatch(),
)
```

A Source wrapper sees a direct one-statement portable plan, not statements on a
transaction handle opened underneath it. Configure pgx's driver tracer, or
return an instrumented transaction from `Begin`, when every statement must be
visible.

This is an explicit semantic choice. A server/encoding error from a COPY that
was already attempted is returned as-is (with the configured classifier); vv
does not retry those rows as SQL and risk duplicating effects.

For deliberate raw work, the context-aware escape hatch still resolves an
ambient executor:

```go
n, err := crud.UnsafeBulkInsertFor(ctx, src,
    crud.TableRef{Schema: "tenant_42", Name: "products"}, cols, rows)
```

For exact-handle behaviour, the adapter exposes
`src.UnsafeCopyFrom(ctx, "users", cols, rows)` and the structured form:

```go
n, err := src.UnsafeCopyFromTable(ctx,
    crud.TableRef{Schema: "tenant_42", Name: "products"}, cols, rows)
```

`UnsafeCopyFrom` accepts one exact identifier component and refuses a dotted
legacy string. `UnsafeCopyFromTable` gives pgx the structured
`pgx.Identifier` components. Both run on the receiver's exact handle and do not
consult a transaction in `ctx`; both bypass repository metadata and Core
decorators, although pgx errors still pass through this executor's classifier.

Pre-release migration: `crud.BulkInserter`, `CopyFrom` and `CopyFromTable` were
removed. Use `Repo.InsertBatch` for application writes,
`crud.UnsafeBulkInsertFor` for raw context-aware rows, or the explicitly unsafe
adapter methods when an exact handle is intentional.

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
- [[UC-008]] safe typed batch insert · [[FL-009]] transactions
