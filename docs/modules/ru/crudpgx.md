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
src := crudpgx.Open(pool)
err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
    ctx := src.BindExecutor(ctx, tx)
    return users.SaveOnly(ctx, &u)
})
```

Связанный executor наследует классификатор `src`, включая каталог или
пользовательский словарь. `crudpgx.WithFaults(...)` переопределяет его явно.

С sqlc на pgx это вся интеграция целиком: `pgx.Tx` удовлетворяет и `DBTX` из
sqlc, и `Queryer` из vv одновременно, так что одна транзакция кормит обоих.
Общая форма — `crud.BindExecutor(ctx, src, crudpgx.From(tx))`; если назвать
source-ом сам `tx`, framework вернёт `crud.ErrExecutorScope`, а не уйдёт в pool
вне rollback ([[D-082]]).

## Массовая вставка через COPY

Используйте типизированный repository-метод; когда metadata даёт
insert-колонки, `crudpgx` автоматически даёт ему PostgreSQL `COPY`:

```go
err := users.InsertBatch(ctx, []*User{&a, &b, &c})
```

Repository выводит точную таблицу, колонки и значения из metadata, применяет
security- и fault-декораторы до обращения к драйверу и считает каждую строку
только create-операцией. Назначенный primary key может дать conflict, но никогда
не становится upsert. Модели не меняются, generated-значения не читаются назад.

`InsertBatch` разрешает executor, привязанный к этому datasource в `ctx`.
Внутри чужой или открытой vv транзакции COPY поэтому выполняется на этой
транзакции и откатывается вместе с ней. Если разрешённый хэндл не умеет COPY
или у модели нет insert-колонок, repository использует атомарные
bind-budgeted statements `INSERT`.

У COPY и обычного INSERT не полностью одинаковая семантика таблицы PostgreSQL.
Для RLS, rewrite rules, значений, которым нужен обычный parameter encoding pgx,
или требования обычной INSERT-семантики выбирайте переносимый путь:

```go
err := users.InsertBatch(ctx, rows, crud.PortableBatch())

var Users = sqlrepo.Define[User, int64, UserUpdate](
    "users",
    sqlrepo.PortableBatch(),
)
```

Source wrapper видит прямой переносимый план из одного statement, но не
statements на transaction handle, открытом под ним. Чтобы видеть каждый
statement, настройте tracer драйвера pgx либо возвращайте инструментированную
транзакцию из `Begin`.

Это явный выбор семантики. Server/encoding error уже начатого COPY возвращается
как есть (после настроенного классификатора); vv не повторяет эти строки как SQL
и не рискует продублировать эффект.

Для намеренной raw-работы context-aware escape hatch всё равно разрешает
ambient executor:

```go
n, err := crud.UnsafeBulkInsertFor(ctx, src,
    crud.TableRef{Schema: "tenant_42", Name: "products"}, cols, rows)
```

Для поведения на точном хэндле адаптер предоставляет
`src.UnsafeCopyFrom(ctx, "users", cols, rows)` и structured-форму:

```go
n, err := src.UnsafeCopyFromTable(ctx,
    crud.TableRef{Schema: "tenant_42", Name: "products"}, cols, rows)
```

`UnsafeCopyFrom` принимает один точный identifier-компонент и отказывает
legacy-строке с точкой. `UnsafeCopyFromTable` передаёт pgx structured-компоненты
`pgx.Identifier`. Оба выполняются на точном receiver и не смотрят на транзакцию
в `ctx`; оба обходят metadata repository и Core-декораторы, хотя pgx-ошибки всё
равно проходят через classifier этого executor.

Миграция pre-release API: `crud.BulkInserter`, `CopyFrom` и `CopyFromTable`
удалены. Для application-записей используйте `Repo.InsertBatch`, для raw
context-aware строк — `crud.UnsafeBulkInsertFor`, а для намеренной работы
на точном хэндле — явно unsafe-методы адаптера.

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
- [[UC-008]] безопасная типизированная batch-вставка · [[FL-009]] транзакции
