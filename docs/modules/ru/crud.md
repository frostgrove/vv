# crud — контракт

```go
import (
    "github.com/frostgrove/vv/crud"
    "github.com/frostgrove/vv/utils"
)
```

**Модуль:** корневой · **Зависит от:** стандартной библиотеки и ничего больше
· **Манифест контракта:** да ([[D-048]])

Словарь, на котором говорят все остальные пакеты. Модель становится метаданными,
фильтр — замкнутым AST, опция — планом запроса, а соединение — двумя методами.
SQL здесь никто не выполняет — этим занимается `crud/sqlrepo` — и здесь не знают,
что такое транспорт.

**Импортируйте его напрямую, когда** пишете фильтры, подключаете чужую
транзакцию или реализуете адаптер. Трёхсостояние и маленькие generic-хелперы
лежат в `github.com/frostgrove/vv/utils`; основной код приложения трогает
`crud.Where`, `utils.Opt`, `utils.Ptr` и `crud.Page`.

> Нужна инструкция по применению репозитория, а не справочник контракта?
> Начните с [репозитория: от модели до сложного запроса](../../usage-guides/repository.md).
> Там последовательно разобраны GetByID, First, Get, specs, preload, Save,
> SaveOnly, Update и транзакции.

---

## Что вы получаете

| | |
|---|---|
| **Метаданные модели** | теги `db` и `rel` превращаются в `Schema`, `Meta` и граф связей, вычисляемый один раз |
| **`utils.Opt[T]`** | три состояния — undefined, null, set — чтобы PATCH мог отличить "оставить как есть" от "очистить" |
| **Опции** | `Page`, `Limit`, `Where`, `OrderBy`, `Preload`, `Select`, `Distinct`, `Aggregate` и ещё четырнадцать |
| **Предикаты** | замкнутый AST: 26 конструкторов, среди них `And`/`Or`/`Not`, пути через связи на любую глубину |
| **Пагинация** | `PaginatedResponse[T]`, постраничная пагинация по offset и по курсору поверх кортежа сортировки |
| **Связи** | `belongs_to`, `has_one`, `has_many`, `many_to_many` — выводятся автоматически, переопределяемы |
| **Шов исполнителя** | `Exec` и `Query`. Это вся граница абстракции |
| **Транзакции** | `InTx`, `BindExecutor`, `Session` — подключение к чужой транзакции без угадывания БД |
| **Диалекты** | `Postgres`, `MySQL` (и MariaDB), `SQLite` |
| **Сигнальные ошибки** | `ErrNotFound`, `ErrConflict`, `ErrForbidden`, `ErrStaleVersion`, … |

---

## Модель

`db:"column,option,option"`. Поле без тега всё равно мапится, а имя колонки
выводится из имени Go-поля в `snake_case`.

```go
type User struct {
    ID        int64         `db:"id,pk,auto"`
    TenantID  int64         `db:"tenant_id,immutable"`
    Email     string        `db:"email"`
    Age       utils.Opt[int] `db:"age"`
    Version   int           `db:"version,version"`
    CreatedAt time.Time     `db:"created_at,generated"`
}
```

| Опция | Значение |
|---|---|
| `pk` | первичный ключ. По умолчанию берётся поле `ID` или колонка `id` |
| `auto` | генерируется базой данных при вставке. Поведение по умолчанию для целочисленных первичных ключей |
| `noauto` | отключить это поведение по умолчанию для целочисленного первичного ключа |
| `immutable` | записывается при вставке, никогда — при обновлении: `created_at`, `tenant_id` |
| `generated` | никогда не записывается, читается заново после каждой записи — вычисляемые колонки, триггеры |
| `version` | оптимистичная блокировка: целое число, которое vv увеличивает и проверяет при каждом обновлении |
| `-` | полностью игнорировать поле |

Встроенные структуры разворачиваются в плоский список. `time.Time`,
`sql.Null[T]`, `utils.Opt[T]` и всё, что реализует `Valuer`/`Scanner`, считаются
одной колонкой. **Поля-структуры без тега `rel` пропускаются** — не становятся
ни колонкой, ни связью, что и требуется для вычисляемого поля.

`SchemaOf[M]()`, `MustSchemaOf[M]()` и `NewMeta[M](table)` строят метаданные
вручную, если это нужно; `sqlrepo.Define` делает это за вас и валидирует сразу.
Для qualified физической таблицы используйте
`NewMetaInSchema[M](schema, table)` либо low-level
`NewMetaRef[M](crud.TableRef{Schema: schema, Name: table})`.
`Meta.TableReference()` возвращает неизменяемую structured identity по значению;
`Meta.Table` остаётся её диагностическим compatibility spelling.

## `utils.Opt[T]` — три состояния, один тип

Именно из-за него PATCH вообще работает.

```go
utils.Undefined[int]()   // отсутствует в payload   → не записывается
utils.Null[int]()        // явный null              → SET col = NULL
utils.Set(31)            // значение                → SET col = 31
utils.FromPtr(p)         // nil → null, иначе set
```

```go
o.IsDefined() // либо null, либо set?
o.IsSet()     // set?
o.Get()       // (T, bool)
o.OrElse(def) // T
o.Ptr()       // *T
```

Сериализуется как голое значение, либо как `null`, и исчезает под `omitzero`.
На проводе это значит, что одна и та же структура корректно сериализуется в
обе стороны ([[UC-003]]).

`utils.Ptr(v)` — короткий конструктор указателя для PATCH DTO, а
`utils.Must(v, err)` возвращает `v` или паникует на границе декларации.
`crud.Opt`, `crud.Set`, `crud.Null` и остальные имена остаются алиасами для
совместимости; в новом коде используйте `utils`, чтобы модели, транспорт и
валидация не зависели от CRUD только ради optional-значения.

## Опции

Каждое чтение принимает `...crud.Option`, и каждая опция аддитивна.

| Группа | Опции |
|---|---|
| Пагинация | `Page(n)`, `Limit(n)`, `Offset(n)`, `Unpaged()`, `SkipTotal()` |
| Пагинация по курсору | `After(cursor)`, `Before(cursor)` |
| Фильтрация | `Where(pred)` — **добавляет через AND, никогда не заменяет** |
| Сортировка | `OrderBy(orders...)`, `SortBy(...)`, `Unsorted()`, `Asc(f)`, `Desc(f)` |
| Проекция | `Select(fields...)`, `SelectAll()`, `Distinct()`, `PrimaryOnly()` |
| Связи | `Preload(paths...)`, `PreloadWhere(path, opts...)`, `NarrowRelations(rs)` |
| Группировка | `GroupBy(fields...)`, `Aggregate(aggs...)` |
| Блокировка | `ForUpdate()` |
| Композиция | `With(otherOptions)` |

`crud.Where` **добавляет через AND**. Именно это позволяет декоратору внедрить
фильтр, который вызывающий код не может снять обратно ([[D-004]]).

Без явного запроса происходят три вещи: размер страницы обрезается до
`MaxLimit` репозитория — в том числе когда запрос говорит `Unpaged`, а это
флаг на проводе, который не перебивает серверное ограничение — добавляется
тай-брейкер по первичному ключу, чтобы страницы не перетасовывались, и
`COUNT` пропускается, если первая же страница содержит вообще всё.

## Предикаты

Замкнутый AST. `Raw` — единственный лаз наружу, и именно это делает
security-скоуп неснимаемым ([[D-003]]).

```go
crud.Eq  crud.Ne  crud.Gt  crud.Gte  crud.Lt  crud.Lte
crud.In  crud.NotIn  crud.InAny[T]  crud.NotInAny[T]  crud.Between
crud.Like  crud.NotLike  crud.LikeIgnoreCase
crud.Contains  crud.StartsWith  crud.EndsWith
crud.ContainsIgnoreCase  crud.StartsWithIgnoreCase  crud.EndsWithIgnoreCase
crud.IsNull  crud.IsNotNull  crud.EqField
crud.And  crud.Or  crud.Not  crud.True  crud.False  crud.Raw
```

`Eq` и `Ne` понимают `utils.Opt`: set сравнивает сохранённое значение, null
использует `IS NULL`/`IS NOT NULL`, а undefined даёт ошибку схемы вместо
молчаливого `= NULL`.

`Like`, `NotLike` и `LikeIgnoreCase` принимают SQL-паттерн как есть — это форма
для кода, который намеренно управляет `%` и `_`. `Contains`, `StartsWith` и
`EndsWith` принимают обычный текст: экранируют обратную косую черту, `%` и `_`,
добавляют wildcard и выводят безопасный для диалекта `ESCAPE`. Их формы
`IgnoreCase` используют переносимое сравнение через `LOWER()`.

Имя поля может пересекать связь на любой глубине:

```go
crud.Where(crud.Eq("Author.Name", "Ann"))
crud.Where(crud.In("Tags.Slug", "go", "rust"))
crud.Where(crud.Contains("Comments.Author.Name", "bo"))
```

Каждый переход рендерится как **коррелированный `EXISTS`**, а не join
([[D-005]]). Join против связи "многие" размножает результирующий набор:
статья с двумя подходящими тегами превращается в две строки, `LIMIT 20`
возвращает меньше двадцати уникальных статей, а `COUNT(*)` показывает число,
которого не существует.

`crud.Raw` **не валидируется** — имена колонок в сыром фрагменте не
разрешаются и не экранируются. Это единственное, что стоит искать при
ревью.

## Связи

```go
type Article struct {
    ID       int64  `db:"id,pk,auto"`
    AuthorID int64  `db:"author_id"`

    Author   *Author   `rel:"belongs_to"`                     // fk: AuthorID
    Comments []Comment `rel:"has_many"`                       // fk: ArticleID на Comment
    Tags     []Tag     `rel:"many_to_many,join=article_tags"` // article_id / tag_id
}
```

| Тег | Внешний ключ | Переопределения |
|---|---|---|
| `belongs_to` | `<Field>ID` на этой модели | `fk=`, `ref=`, `table=`, `schema=` |
| `has_one` / `has_many` | `<ThisModel>ID` на целевой модели | `fk=`, `ref=`, `table=`, `schema=` |
| `many_to_many` | две колонки таблицы связи | target `table=`/`schema=`; `join=`, `joinSchema=`, `joinFK=`, `joinRef=` |
| `rel:""` | выводится из Go-типа | |
| `rel:"-"` | никогда не связь | |

Целевые таблицы разрешаются по регистрации в `sqlrepo.Define`, затем по методу
`TableName()`, затем по snake_case множественному числу. `RegisterTable[M](table)`
регистрирует таблицу вручную. Результат разрешения неизменяем:
вторая таблица для одной модели или другая поздняя регистрация после того,
как relation уже выбрал convention, — это startup error, а не изменение регистра,
которого уже не видят опубликованные metadata. `TryRegisterTable` и
`TryRegisterTableType` возвращают этот schema error для low-level сборки;
structured-варианты — `RegisterTableRef` / `TryRegisterTableRef`, а повтор той
же полной ссылки идемпотентен. Если одна relation намеренно читает тот же Go-тип
из другой таблицы, укажите `table=...` и, для qualified таблицы, `schema=...`.
Join many-to-many использует `joinSchema=...`. Legacy-строки с точкой
отклоняются, а не разделяются догадкой ([[D-080]]).

**Preload** — это батчевый второй запрос на связь на уровень, никогда не
запрос на строку ([[D-006]]). Пути с общим префиксом делят один запрос, ключи
дедуплицируются, длинные списки бьются на батчи по 900 ключей, а проекция
`Select()` автоматически сохраняет колонки для join. Пагинация внутри preload
отклоняется — `LIMIT` поверх батчевой загрузки обрезал бы детей у одних
родителей и не обрезал бы у других.

Сортировка через связь "многие" тоже отклоняется: у коллекции нет одного
значения для сортировки, поэтому она отклоняется, а не молча выбирает
какое-то одно.

Путь здесь строка, потому что обычно строкой и приходит — из query string, от
клиента. Написанный на Go, тот же путь отдаётся сгенерированной метамоделью как
идентификатор, поэтому переименование связи ломает сборку:

```go
crud.Preload(Article_.Comments.Path(), Article_.Comments.Author.Path())
crud.PreloadWhere(Article_.Comments.Path(), crud.Where(specs.Predicate(Comment_.Approved.Eq(true))))
```

Про хэндл и его единственный случай затенения — см. [specs](specs.md).

## Пагинация

```go
type PaginatedResponse[T any] struct {
    Items      []T   `json:"items"`
    Page       int   `json:"page"`
    Limit      int   `json:"limit"`
    Total      int64 `json:"total"`
    TotalPages int   `json:"totalPages"`
    HasNext    bool  `json:"hasNext"`
    HasPrev    bool  `json:"hasPrev"`
}
```

`MapPage(p, f)` конвертирует элементы и сохраняет всю арифметику.

**Пагинация по курсору** кодирует кортеж сортировки, а не offset, поэтому
вставки не сдвигают окно читателя ([[D-028]]):

```go
page, _ := users.Get(ctx, crud.Limit(20), crud.OrderBy(crud.Desc("CreatedAt")))
next, _ := users.Get(ctx, crud.Limit(20), crud.After(page.NextCursor))
```

`SkipTotal()` полностью убирает `COUNT`, отвечает на `HasNext`, забирая на
одну строку больше, чем нужно для страницы, и сообщает `Total` как размер
того, что вернулось — потому что остальное никто не считал.

Cursor-ссылки выдаются только для уникальной сортировки без nullable-колонок.
Сортировки по pointer, `utils.Opt` и `database/sql.Null` по-прежнему работают с
offset-пагинацией, но не рекламируют cursor, который невозможно корректно
пройти при трёхзначной NULL-семантике SQL.

## Агрегаты

```go
rows, err := orders.Aggregate(ctx,
    crud.GroupBy("Status"),
    crud.Aggregate(crud.CountAll("n"), crud.Sum("total", "Amount"), crud.Avg("avg", "Amount")),
    crud.Where(crud.Gte("CreatedAt", cutoff)))
```

`CountAll`, `CountOf`, `CountDistinct`, `Sum`, `Avg`, `Min`, `Max`. Они
выполняются под тем же сужением, что и чтение, поэтому security-скоуп
применяется и здесь ([[D-029]]).

По умолчанию summary не пагинируется: repository default больше не отрезает
группы незаметно. Явные `Limit`, `Page`, `Offset` или `Unpaged` по-прежнему
подчиняются `MaxLimit` репозитория. Обычный `OrderBy` может ссылаться только на
колонки группировки; остальные колонки модель отклоняет до отправки SQL.

Имена полей тоже принимают метамодель — `crud.GroupBy(Order_.Status.Name())`,
`crud.Sum("total", Order_.Amount.Name())`. Имя самого агрегата — это ключ, под
которым возвращается значение, поэтому оно остаётся строкой.

## Шов исполнителя

Два метода. Это вся граница абстракции, и именно поэтому любую чужую
транзакцию можно связать с источником.

```go
type Executor interface {
    Exec(ctx context.Context, query string, args ...any) (Result, error)
    Query(ctx context.Context, query string, args ...any) (Rows, error)
}

type Source interface {
    Executor
    Dialect() Dialect
}
```

Сканирование остаётся за маппером, диалект — за репозиторием.

**Подключение к чужой транзакции** — одна source-bound декларация:

```go
ctx = crud.BindExecutor(ctx, source, crudsql.From(tx))
```

Репозитории над `source` выполняются в транзакции, а репозитории другой БД
остаются на своём datasource. `source` даёт identity пула, который невозможно
восстановить из чужой транзакции ([[D-082]]). `crud.NewSession` проверяет связь
сразу и возвращает переиспользуемый `crud.Session`; `MustSession` — вариант для
декларативной проводки при старте.

Низкоуровневый эквивалент принимает raw identity:

```go
ctx = crud.WithExecutorFor(ctx, mainDB, crudsql.From(tx))

users.Save(ctx, &u)    // привязан к mainDB      — выполняется в tx
events.Save(ctx, &e)   // привязан к analyticsDB — выполняется на analyticsDB
```

`WithExecutor(ctx, e)` deprecated и работает строго: если `e` называет хендл
транзакции, а не pool, вызов репозитория возвращает inspectable
`ErrExecutorScope` до обращения к datasource. Старый безусловный захват остался
только как `WithUnsafeExecutor`; это явный cross-database opt-out.

`crud.InTx(ctx, src, fn)` открывает одну транзакцию для нескольких
репозиториев и подключается к уже имеющейся в контексте, а не вкладывается
внутрь неё. Source без стабильного identity через `Identified` отклоняется до
`Begin`.

**Опциональные интерфейсы**, которые может реализовать адаптер: они ищутся, а
не требуются, чтобы сторонний адаптер компилировался и без них: `Beginner`
(savepoint'ы), `BulkInserter` (`COPY`), `OffsetLimiter`, `ReadSourcer`,
`Identified`, `Sourced`, `UpsertScope`, `StatementRollback`, `Tabler`.

### Обёртка над Source — для трейсинга, таймингов, логов стейтментов

Это и есть шов. `Source` — три метода, поэтому обёртка, которая замеряет или
логирует стейтменты, укладывается в десяток строк. И у неё одна обязанность:

```go
type tracing struct{ inner crud.Source }

func (t tracing) Exec(ctx context.Context, q string, a ...any) (crud.Result, error) { … }
func (t tracing) Query(ctx context.Context, q string, a ...any) (crud.Rows, error)  { … }
func (t tracing) Dialect() crud.Dialect { return t.inner.Dialect() }

// Вот эта. Без неё обёртка — не декоратор, а замена.
func (t tracing) UnwrapSource() crud.Source { return t.inner }
```

Встраивание в Go продвигает только методы самого встроенного интерфейса, поэтому
обёртка стирает все опциональные интерфейсы, которые были у обёрнутого
источника. Три из них чего-то стоят, и только два из трёх сообщают об этом:

| Что потеряно | Что происходит |
|---|---|
| `Beginner` | любой `Tx` — это `ErrNoTxSupport`; громко |
| `Identified` | `Session`/`InTx` возвращают `ErrExecutorScope`, а каталог отказывает на старте ([[D-041]], [[D-082]]); громко |
| `ReadSourcer` | **все чтения молча уходят на primary.** Реплика простаивает, и ничто не связывает это с днём, когда добавили обёртку |

`UnwrapSource` закрывает все три сразу: `crud.BeginnerOf`, `crud.ReadSourceOf` и
`crud.KeyOf` идут по нему ([[D-061]]).

**То же правило уровнем выше.** Декоратор над `crud.Core` стирает точно так же.
Встраивайте `crud.Base` — он даёт `Next()`, по которому идёт `crud.SourceOf`, —
иначе probe, навешенный над вашим декоратором, не найдёт источник под ним.

## Реплики

```go
db := crud.ReadWrite(primary, replica)
```

Чтения идут на реплику, записи — на основную базу — и **чтение, от которого
зависит запись, никогда не идёт на реплику** ([[D-032]]). Проверка
существования за `Save`, загрузка за `Update` и выборка жертв за `DeleteAll`
всегда выполняются на основной базе, потому что решение о записи по
отстающей реплике — это способ молча перезаписать строку.

## Сигнальные ошибки

Сравнивайте через `errors.Is`, никогда по строке ([[D-015]]).

```go
crud.ErrNotFound       crud.ErrConflict       crud.ErrForbidden
crud.ErrStaleVersion   crud.ErrReadOnly       crud.ErrMissingID
crud.ErrNoTxSupport    crud.ErrExecutorScope
```

Каждая из них переживает обёртывание в `errs.Fault`, поэтому вызывающий код,
который написал `errors.Is(err, crud.ErrConflict)` ещё до появления подсистемы
ошибок, сохраняет эту ветку рабочей ([[D-038]]).

## Диалекты

`crud.Postgres{}`, `crud.MySQL{}`, `crud.SQLite{}`. Диалект говорит **как
писать SQL**, а не какой сервер отвечает — `crud.MySQL` нацелен и на MariaDB
тоже, а эти два движка отвечают на упавший `CHECK` разными номерами. Сказать,
какой именно *движок* говорит — задача [sqlfault](sqlfault.md).

Различия, которые видны вызывающему коду, перечислены в [[D-019]], а не
заретушированы.

`crud.MySQL{RowAlias: true}` переключает upsert с устаревшей формы `VALUES()`
на `AS new` (MySQL 8.0.19+). `ForUpdate()` ничего не рендерит на SQLite,
который блокирует всю базу данных, а не строку.

Потолок параметров statement — тоже capability диалекта. `BindLimit(d)` читает
опциональный `BindBudget`: PostgreSQL и MySQL объявляют 65 535, SQLite — 999,
а внешний диалект без capability получает консервативный default 999.
`SQL.Done` считает полный список аргументов, поэтому соседние предикаты и
relation scopes расходуют тот же бюджет, что список `In`. Превышение даёт
типизированный `SchemaError` до datasource, а не ошибку драйвера ([[D-079]]).

## Смотрите также

- [sqlrepo](sqlrepo.md) — репозиторий, превращающий всё это в SQL
- [crudtest](crudtest.md) — проверка SQL без базы данных
- [[FL-001]] от запроса списка к строкам · [[FL-012]] от значения на проводе к значению Go
- [[D-001]] шов с двумя параметрами · [[D-003]] замкнутый AST · [[D-016]] только stdlib · [[D-079]] bind-бюджеты
