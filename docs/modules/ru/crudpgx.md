# crudpgx — pgx v5

```go
import "github.com/frostgrove/vv/crud/adapter/crudpgx"
```

```bash
go get github.com/frostgrove/vv/crud/adapter/crudpgx
```

**Модуль:** отдельный — чтобы потребитель на `database/sql` никогда не тянул
pgx в зависимости ([[D-033]]) · **Требует:** `github.com/jackc/pgx/v5`

Самый короткий путь: без ORM, без `database/sql` на пути. vv говорит с пулом
напрямую.

---

## Открытие

```go
pool, err := pgxpool.New(ctx, dsn)
if err != nil { log.Fatal(err) }
defer pool.Close()

users := Users.Bind(crudpgx.Open(pool))
```

`crudpgx.Open` — это весь адаптер целиком. vv просит пул выполнить запрос и
вернуть строки, и **никогда не открывает соединение сама**.

```go
type Queryer interface {
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}
```

`*pgxpool.Pool`, `*pgx.Conn` и `pgx.Tx` — все ему удовлетворяют.

## Подключение к чужой транзакции

```go
err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
    ctx := crud.WithExecutor(ctx, crudpgx.From(tx))
    return users.Save(ctx, &u)
})
```

С sqlc на pgx это вся интеграция целиком: `pgx.Tx` удовлетворяет и `DBTX` из
sqlc, и `Queryer` из vv одновременно, так что одна транзакция кормит обоих.

## Массовая вставка через COPY

`crudpgx` реализует `crud.BulkInserter` через `COPY`. **Библиотека сама за этим
не тянется.** `SaveAll` пишет один многострочный `INSERT` независимо от того, что
умеет исполнитель под ним, так что эту дверь приложение открывает само:

```go
if bulk, ok := src.(crud.BulkInserter); ok {
    n, err := bulk.CopyFrom(ctx, "users", cols, rows)
}
```

Вызов идёт на том хэндле, который держит исполнитель, и игнорирует любую
транзакцию в контексте ([[UC-008]]).

## Структурированные коды ошибок

pgx именует собственный тип ошибки, поэтому этот адаптер классифицирует по
типу поверх `*pgconn.PgError`, а не по форме. Подключается так:

```go
cls := sqlfault.New("postgres")
db  := crudpgx.Open(pool, crudpgx.WithFaults(cls))
```

С [каталогом](catalog.md) нарушения несут ещё и столбцы, которые PostgreSQL не
назвал — **составной unique** — как раз тот случай, где это нужно:

```go
cat, _ := catalog.Load(ctx, crudpgx.Open(pool))
cls := sqlfault.New("postgres", sqlfault.WithColumns(sqlfault.FromCatalog(cat)))
db  := crudpgx.Open(pool, crudpgx.WithFaults(cls))
```

## Транзакции и savepoint'ы

`Begin` на `crudpgx.Tx` возвращает настоящую вложенную транзакцию pgx —
собственный savepoint pgx — а не текст `SAVEPOINT`, сформированный vv.

**Отложенные ограничения тоже классифицируются.** Ограничение, отложенное до
`COMMIT`, срабатывает там, а не на самом запросе, и `Tx.Commit` классифицирует
его. Запрос, который сервер отклонил внутри savepoint'а, тоже отравляет
транзакцию, поэтому PostgreSQL отказывает в release с кодом `25P02` — и этот
путь даёт вложенному `Commit` код вместо безымянной 500.

## См. также

- [crudsql](crudsql.md) — `database/sql`, а значит и ent, gorm, sqlx, sqlc, bun
- [sqlfault](sqlfault.md) — что принимает `WithFaults`
- [`_examples/pgx-fiber`](../../../_examples/pgx-fiber/) · [`_examples/pgx-grpc`](../../../_examples/pgx-grpc/)
- [[UC-008]] запись многих строк одним запросом · [[FL-009]] транзакции
