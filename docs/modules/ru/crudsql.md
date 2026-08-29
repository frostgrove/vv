# crudsql — database/sql, а значит и всё остальное

```go
import "github.com/frostgrove/vv/crud/adapter/crudsql"
```

**Модуль:** корневой — `database/sql` из стандартной библиотеки, значит бесплатно
· **Зависит от:** `crud`, `errs`, `database/sql`

Один адаптер покрывает любой стек, говорящий на `database/sql`: ent, gorm, sqlx,
sqlc, bun, squirrel, dbr — и вовсе без ORM.

---

## Открытие соединения

```go
db := crudsql.Postgres(sqlDB)
db := crudsql.MySQL(sqlDB)
db := crudsql.MariaDB(sqlDB)   // тот же диалект, что у MySQL, но другие номера ошибок
db := crudsql.SQLite(sqlDB)

users := Users.Bind(db)
```

**Используйте именованный конструктор.** Он говорит, какой *движок* отвечает,
а именно это позволяет отклонённому запросу вернуться с кодом — `unique`,
`foreign_key`, `required` — вдобавок к `crud.ErrConflict`.

`crudsql.Open(db, crud.Postgres{})` принимает `crud.Dialect` вместо этого — он
говорит, как писать SQL, а **не** какой сервер отвечает: `crud.MySQL` — это и
MariaDB тоже, а они отвечают на неудавшийся `CHECK` разными номерами. Поэтому
`Open`, `From` и `Source` классифицируют *статус*, а не *код* — vv отказывается
угадывать, вместо того чтобы называть MariaDB-сервер «mysql» ([[D-046]]).

## Присоединение к чужой транзакции

Точка интеграции — ровно одна функция:

```go
ctx = crud.WithExecutor(ctx, crudsql.From(tx))
```

Каждый вызов репозитория с этим контекстом выполняется на этом исполнителе.
Новый фреймворк — это поиск, где он прячет свою транзакцию, и обёртка вокруг
неё — три строки.

| Стек | Как |
|---|---|
| `*sql.DB` / `*sql.Tx` / `*sql.Conn` | `crudsql.Postgres(db)` · `crudsql.From(tx)` |
| sqlx | `crudsql.From(sqlxTx)` — под капотом это `*sql.Tx` |
| gorm | `crudsql.From(tx.Statement.ConnPool)` внутри `db.Transaction` |
| ent (`--feature sql/execquery`) | `crudsql.From(entTx)` — у `*ent.Tx` есть `ExecContext`/`QueryContext` |
| sqlc (database/sql) | `crudsql.From(tx)`; тот же `*sql.Tx` идёт в `sqlc.New(tx)` |
| bun, squirrel, dbr, … | `crudsql.From(tx)` |

```go
err := gormDB.Transaction(func(tx *gorm.DB) error {
    ctx := crud.WithExecutor(ctx, crudsql.From(tx.Statement.ConnPool))
    return users.SaveOnly(ctx, &u)
})
```

**С несколькими базами данных указывайте, какую именно имеете в виду** —
`crud.WithExecutorFor(ctx, mainDB, e)`. Обычная форма захватывает каждый
репозиторий — это и есть точка интеграции, работающая как задумано, и это же
способ, которым запись попадает не в ту базу данных и рапортует об успехе
([[UC-012]]).

## Всё, что не является `*sql.DB`

```go
type Queryer interface {
    ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
    QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}
```

`crudsql.From(q, opts...)` оборачивает один такой — обычно транзакцию, которой
владеет кто-то другой. `crudsql.Source(q, dialect, opts...)` строит полноценный
`crud.Source` поверх него — для случаев, когда фреймворк даёт вам хендл вместо
`*sql.DB`, а вы хотите строить репозитории прямо на нём.

`Open` используется, когда у вас есть `*sql.DB` и нужны ещё и транзакции.

## Коды на присоединённой транзакции

`From` и `Source` не называют движок, так что скажите им сами:

```go
cls := sqlfault.New("postgres")
ctx = crud.WithExecutor(ctx, crudsql.From(tx, crudsql.WithFaults(cls)))
```

Это тот же `errs.Classifier`, что принимают именованные конструкторы. С
подключённым [каталогом](catalog.md) нарушения несут с собой и колонки,
которые не назвал драйвер:

```go
cat, _ := catalog.Load(ctx, crudsql.Postgres(sqlDB))
cls := sqlfault.New("postgres", sqlfault.WithColumns(sqlfault.FromCatalog(cat)))
db  := crudsql.Postgres(sqlDB, crudsql.WithFaults(cls))
```

## Транзакции и savepoint

```go
db := crudsql.Postgres(sqlDB).WithTxOptions(&sql.TxOptions{
    Isolation: sql.LevelSerializable,
})
```

`DB.Begin` открывает транзакцию; `Begin` на получившемся `crud.Tx` даёт
**savepoint**, выраженный как `SAVEPOINT` / `ROLLBACK TO SAVEPOINT` /
`RELEASE SAVEPOINT`.

`db.DB()` отдаёт `*sql.DB` обратно, где он нужен.

**Отложенные ограничения тоже классифицируются.** Ограничение, отложенное до
`COMMIT`, срабатывает именно там, а не на самом запросе, и `Tx.Commit`
классифицирует его — так что то же нарушение оказывается 409 через оба входа.

## For и Wired — переключатель движка и подсистема ошибок

| | |
|---|---|
| `Engine` | `EnginePostgres`, `EngineMySQL`, `EngineMariaDB`, `EngineSQLite` — те же четыре значения, что у `vvdb.Engine` |
| `For(engine, db, opts…)` | привязывает handle к *названному* движку, а не к одному из четырёх конструкторов, выбранному в исходнике |
| `Wired(ctx, engine, db, opts…)` | `For` с классификатором и каталогом на месте |
| `ErrEngine` | движок пустой или не один из четырёх |

```go
source, err := crudsql.Wired(ctx, crudsql.Engine(cfg.Db.Engine), db)
```

Чтобы отклонённая запись сказала, что именно с ней не так, на месте должны быть
три части, и отсутствие любой из них молчаливо — поэтому `Wired` один вызов, а не
три строки, которые копирует каждое приложение:

1. **классификатор**, чтобы дубль адреса был кодом `unique`, а не фразой драйвера,
   несущей схему ([[D-044]]);
2. **каталог**, чтобы классификатор мог ответить, какие колонки покрывает это
   ограничение. PostgreSQL на нарушении уникальности называет ограничение и
   таблицу и **не называет колонку вовсе** — без каталога нарушение приходит без
   поля, всё выглядит подключённым, а форме нечего подсветить;
3. `faults.Enrich` на каждом репозитории — переход от колонки к полю модели. Он
   на уровне репозитория и остаётся вашим.

Схема читается на старте, а не лениво: ленивый загрузчик не может упасть на
старте, а поиск по схеме, тихо вернувший ничего, — это то, как поле исчезает
снова ([[D-041]]).

Переключатель движка живёт здесь, чтобы у приложения, добавившего SQLite для
тестов, не появился свой switch, который разойдётся с этим в том, что означает
`mariadb`: MariaDB и MySQL делят драйвер, диалект и протокол и отвечают на
проваленный CHECK двумя разными номерами ([[D-046]]).

## crudsqlfx — проводка через fx

```go
import "github.com/frostgrove/vv/crud/adapter/crudsql/crudsqlfx"

fx.Options(crudsqlfx.Module(&configuration.Db))
```

**Модуль** — тянет uber/fx, поэтому потребитель, который открывает пул сам,
никогда его не резолвит ([[D-074]]).

Он предоставляет `*sql.DB` и `crud.Source` поверх него, пингует пул при
конструировании и закрывает при остановке. `sql.Open` валидирует DSN и никуда не
подключается, поэтому без пинга неверный хост, неверный пароль и незапущенная
база одинаково выглядят как здоровый старт.

Он **не регистрирует драйвер**. Какой драйвер отвечает на `sql.Open("pgx", …)` —
ваше решение и всегда им было ([[D-057]]): импортируйте его ради side effect
рядом с этим.

## Смотрите также

- [crudpgx](crudpgx.md) — pgx v5, с массовой вставкой через `COPY`
- [sqlfault](sqlfault.md) — что принимает `WithFaults`
- [usage-guides/ent.md](../../usage-guides/ent.md) · [usage-guides/gorm.md](../../usage-guides/gorm.md)
- [[UC-005]] выполнить работу репозитория в транзакции ORM · [[UC-010]] адаптировать существующую модель ORM
- [[FL-009]] транзакции: присоединение, открытие, какая база данных
