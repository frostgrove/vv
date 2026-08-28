# Репозиторий vv: от модели до сложного запроса

Это практический маршрут для прикладного кода. Здесь не нужно реализовывать
`crud.Core` и не нужно писать SQL: генератор описывает модель, приложение
подключает драйвер, а вы держите типизированный репозиторий.

```
ваша Product-модель
        │ vv generate
        ▼
ProductUpdate · ProductAttrs · Product_ · ProductRepo · ProductRepository
                                                        │ Bind(source)
                                                        ▼
                                              *ProductRepo в сервисе
```

Для деталей SQL-адаптера есть [crud/sqlrepo](../modules/ru/sqlrepo.md), а для
всего, что касается типизированных фильтров, — [specs](../modules/ru/specs.md).

## 1. Один раз: модель, генерация и подключение

Модель остаётся обычной Go-структурой. Теги нужны только там, где соглашения
об именах недостаточны.

```go
// src/app/product/product.model.go
type Product struct {
    ID          uuid.UUID
    CategoryID  uuid.UUID
    Name        string
    Description *string
    CreatedAt   time.Time
    UpdatedAt   time.Time

    Category *Category      `rel:"belongs_to"`
    Images   []ProductImage `rel:"has_many"`
}
```

```bash
go run github.com/frostgrove/vv/cmd/vv generate -dir ./src/app
```

Команда ищет экспортируемые структуры в `model.go`, `*.model.go` и
`*_model.go`, и пишет `vv_gen.go` рядом с пакетом. Этот файл коммитится, но
руками не редактируется.

```go
// vv_gen.go — сокращённо
type ProductUpdate struct {
    Name        *string
    Description utils.Opt[string]
}

type ProductAttrs struct {
    ID          specs.Attr[Product, uuid.UUID]
    Name        specs.Str[Product]
    Description specs.Str[Product]
    CreatedAt   specs.Cmp[Product, time.Time]
}

var Product_ = specs.Metamodel[Product, ProductAttrs]()

type ProductRepo = crud.Repo[Product, uuid.UUID, ProductUpdate]

var ProductRepository = sqlrepo.Define[Product, uuid.UUID, ProductUpdate]("")

func NewProductRepository(src crud.Source) *ProductRepo {
    return ProductRepository.Bind(src)
}
```

Что здесь за что отвечает:

| Артефакт | Когда нужен |
|---|---|
| `ProductUpdate` | PATCH/частичное обновление. Генератор делает поля опциональными. |
| `ProductAttrs` | Типизированная форма полей модели. Это технический тип для метамодели; его обычно не трогают напрямую. |
| `Product_` | Готовая метамодель. Через неё пишут `Product_.Name.Contains("chair")`, сортировку и пути связей. Суффикс `_` — только привычное имя, не отдельный вид Go-сущности. |
| `ProductRepo` | Короткий alias для `crud.Repo[Product, uuid.UUID, ProductUpdate]`. Зависите от `*ProductRepo`, а не повторяйте generic-параметры. |
| `ProductRepository` | Проверенный, но ещё не подключённый к БД blueprint. Его можно bind-ить к разным источникам. |
| `NewProductRepository` | Фабрика готового `*ProductRepo`. Её и отдавайте в Fx/DI. |

`Product_` **не хранит строки и не делает запрос сам**. `Product_.Name` —
типизированная ссылка на поле, а `Product_.Name.Contains("chair")` — значение
`specs.Specification[Product]`. Запрос выполняется только когда эту
спецификацию передали репозиторию.

Подключение выбирается в composition root, а не в модели или генераторе:

```go
sqlDB, err := vvdb.Open(&cfg.DB)
if err != nil {
    return err
}

products := product.NewProductRepository(crudsql.Postgres(sqlDB))
```

Для нативного pgx используйте `product.NewProductRepository(crudpgx.Open(pool))`.
Для GORM передайте тот же `*sql.DB`, который вернул `gormDB.DB()` — отдельный
пул создавать не нужно.

## 2. Шпаргалка методов

| Задача | Метод | Результат |
|---|---|---|
| строка по ключу | `GetByID` | `Product` или `crud.ErrNotFound` |
| первая подходящая строка | `First` | `Product` или `crud.ErrNotFound` |
| страница | `Get` | `crud.PaginatedResponse[Product]` |
| все подходящие строки | `GetAll` | `[]Product` |
| проверить/посчитать | `Exists` / `Count` | `bool` / `int64` |
| создать или upsert-нуть и получить итог БД | `Save` | новая `Product` и `error` |
| записать без дочитывания | `SaveOnly` | только `error` |
| частично обновить одну строку | `Update` | новая `Product` и `error` |
| записать набор моделей | `SaveAll` | только `error` |
| изменить набор строк | `UpdateAll` | число строк и `error` |
| удалить по ключам / фильтру | `Delete` / `DeleteAll` | число строк и `error` |
| несколько действий атомарно | `Tx` | `error` |

Все методы чтения принимают `...crud.Option`: фильтр, сортировку, preload,
пагинацию, проекцию и т. д. `Update` тоже принимает опции — ими можно сузить
строку, которую разрешено менять.

## 3. Чтение: начните с простого

### Одна строка по ключу

```go
p, err := products.GetByID(ctx, id)
if errors.Is(err, crud.ErrNotFound) {
    // 404 или свой сценарий отсутствия
}
if err != nil {
    return err
}
```

`GetByID` не обязан быть «голым»: ему так же можно передать `Preload` или
`Select`.

### Первая запись, удовлетворяющая условию

```go
p, err := products.First(ctx,
    specs.As(Product_.Name.ContainsIgnoreCase(q)),
    crud.OrderBy(Product_.CreatedAt.Desc()),
)
```

У «первой» нет естественного смысла без сортировки. Если важна именно самая
новая/старая запись, всегда передавайте `OrderBy`. `First` сам ограничивает
запрос одной строкой; `Page`, `Limit` и курсоры вызывающего для него не нужны.

### Обычная страница

```go
page, err := products.Get(ctx,
    specs.As(Product_.Name.ContainsIgnoreCase(q)),
    crud.OrderBy(Product_.CreatedAt.Desc()),
    crud.Page(2),
    crud.Limit(20),
)
if err != nil {
    return err
}

for _, p := range page.Items {
    // ...
}
// page.Total, page.TotalPages, page.HasNext, page.HasPrev
```

`Page` начинается с 1. `Limit` ограничивается серверным `MaxLimit`, если он
объявлен у репозитория. Без `Page`/`Limit` применится `DefaultLimit` (20 у
`sqlrepo`).

### Все строки, count и exists

```go
filter := specs.As(Product_.Name.ContainsIgnoreCase(q))

items, err := products.GetAll(ctx, filter)
n, err := products.Count(ctx, filter)
found, err := products.Exists(ctx, filter)
```

`GetAll` намеренно не страничный: без явно переданной пагинации он вернёт все
совпадения и не будет молча применять `MaxLimit`. Не отдавайте его напрямую в
публичный endpoint: в API обычно нужна `Get`-страница.

### Пагинация курсором

Курсор — выбор для длинной ленты, в которую параллельно вставляют записи.
Сначала зафиксируйте сортировку, затем отдайте клиенту `NextCursor`:

```go
first, err := products.Get(ctx,
    crud.Limit(20),
    crud.OrderBy(Product_.CreatedAt.Desc()),
)

next, err := products.Get(ctx,
    crud.Limit(20),
    crud.OrderBy(Product_.CreatedAt.Desc()),
    crud.After(first.NextCursor),
)
```

Для предыдущего окна используйте `crud.Before(page.PrevCursor)`. Курсор
привязан к сортировке — не меняйте её между запросами.

## 4. Фильтры: одна строка или типизированные specs

Для разового простого условия достаточно `crud`:

```go
page, err := products.Get(ctx,
    crud.Where(crud.Gte("CreatedAt", since)),
    crud.Where(crud.Eq("Name", exactName)),
)
```

Каждый `crud.Where` добавляется через `AND`; поздняя опция не может снять уже
добавленный фильтр. Строка здесь — имя Go-поля или колонки.

Для прикладного кода обычно удобнее метамодель:

```go
filter := specs.AllOf(
    Product_.Name.ContainsIgnoreCase(q),
    Product_.CreatedAt.Gte(since),
)

page, err := products.Get(ctx,
    specs.As(filter),
    crud.OrderBy(Product_.CreatedAt.Desc()),
)
```

У метамодели компилятор проверяет тип значения: в `Product_.CreatedAt.Gte(...)`
нельзя случайно передать строку. Полный разбор `Attrs`, `Product_`, композиции
и отношений — на странице [specs](../modules/ru/specs.md).

### Текстовый поиск: точный шаблон, а не магия

В репозитории поиск — обычный предикат. Выберите форму по нужному шаблону:

| API | SQL-форма | Что происходит с пользовательским текстом |
|---|---|---|
| `Contains(s)` | `%s%` | ищет вхождение; `%`, `_` и `\\` экранируются |
| `StartsWith(s)` | `s%` | ищет префикс; спецсимволы экранируются |
| `EndsWith(s)` | `%s` | ищет суффикс; спецсимволы экранируются |
| `Like(pattern)` | ровно `pattern` | сырой SQL LIKE-паттерн: wildcard'ами управляете вы |
| варианты `…IgnoreCase` | та же форма через `LOWER()` | переносимый case-insensitive поиск |

~~~go
filter := specs.AnyOf(
    Product_.Name.ContainsIgnoreCase(q),        // %q%
    Product_.Description.ContainsIgnoreCase(q), // %q%
)

page, err := products.Get(ctx, specs.As(filter))
~~~

То же работает через связи: этот запрос выбирает продукты, у которых категория
или хотя бы одно изображение соответствуют условию.

~~~go
filter := specs.AllOf(
    Product_.Category.Name.ContainsIgnoreCase(categoryQuery),
    Product_.Images.Path.EndsWith(".webp"),
)
~~~

Фильтр через to-one и to-many relation реализуется не небезопасным join'ом, а
коррелированным <code>EXISTS</code>. Поэтому несколько подходящих изображений
не дублируют один Product в странице. Сортировка через to-one relation разрешена;
по to-many она намеренно отклоняется, потому что у коллекции нет единственного
значения для порядка.

### Если поиск и фильтры приходят от клиента

Для HTTP/gRPC не пробрасывайте названия полей из запроса прямо в репозиторий.
Пакет [query](../modules/ru/query.md) компилирует строгий JSON или query string
в те же <code>crud.Option</code>, проверяет каждый путь по модели и ставит
бюджеты на глубину, условия, preload и число bind-параметров.

~~~http
GET /products?q=chair&searchFields=name,category.name
    &f=price:gte:10000
    &f=images.path:endsWith:.webp
    &sort=-createdAt,category.name
    &preload=category,images
~~~

Эквивалентный JSON для POST query-endpoint:

~~~json
{
  "search": "chair",
  "searchFields": ["name", "category.name"],
  "filter": {
    "price": {"gte": 10000},
    "images.path": {"endsWith": ".webp"}
  },
  "sort": ["-createdAt", "category.name"],
  "preload": ["category", "images"]
}
~~~

<code>search</code> (алиас в строке запроса — <code>q</code>) — это
регистронезависимое <code>Contains</code> по указанным полям, соединённое через
OR и заключённое в скобки. В примере это
<code>LOWER(name) LIKE LOWER('%chair%') OR LOWER(category.name) LIKE
LOWER('%chair%')</code>; остальные фильтры по-прежнему добавляются через AND.
Если в <code>searchFields</code> явно назвать нестроковое поле и строка поиска
парсится в его тип, оно добавится через equality; иначе игнорируется. Если
<code>searchFields</code> не передан, берётся <code>DefaultSearchFields</code>,
а при его отсутствии — все строковые поля корневой модели, разрешённые
конфигурацией.

Объявите положительный список на **каждом публичном endpoint-е**:

~~~go
cfg := &query.Config{
    // Колонки и пути, которыми клиент вправе фильтровать.
    // Category.* даёт всё поддерево; Images.Path — ровно одно поле.
    Filterable: []string{
        "Name", "Price", "Category.*", "Images.Path",
    },

    // searchFields может только сузить этот список, но не расширить его.
    Searchable: []string{
        "Name", "Description", "Category.Name",
    },
    DefaultSearchFields: []string{"Name", "Description"},

    Sortable:    []string{"Name", "Price", "CreatedAt", "Category.Name"},
    Selectable:  []string{"ID", "Name", "Price", "CategoryID"},
    Preloadable: []string{"Category", "Images"},

    MaxLimit:      50,
    MaxConditions: 24,
    MaxPreloads:   2,
}
~~~

Это include/allow-list модель. Отдельных <code>ExcludeFields</code> и
<code>ExcludeRelations</code> сейчас нет: пустой список разрешает всё, что
маппит модель, а чтобы закрыть поле — задайте явный список разрешённых путей,
не добавляя его. Для всех полей relation используйте <code>Category.*</code>;
для одного — <code>Category.Name</code>. Preload управляется отдельно:
<code>Preloadable</code> перечисляет именно relation paths, а не их колонки.

Полный wire-формат, все операторы и ограничения — в [query](../modules/ru/query.md).

## 5. От простого фильтра к запросу по связям

Представим, что у модели есть связи:

```go
type Product struct {
    ID       uuid.UUID
    Category *Category      `rel:"belongs_to"`
    Images   []ProductImage `rel:"has_many"`
}

type ProductImage struct {
    ProductID uuid.UUID
    Path      string
    Published bool
}
```

После генерации метамодель даёт не строки, а проверяемые пути:

```go
page, err := products.Get(ctx,
    // Выбирает products, у которых category.slug = "furniture".
    specs.As(Product_.Category.Slug.Eq("furniture")),

    // Загружает связи батчами, а не запросом на каждую Product.
    crud.Preload(Product_.Category.Path(), Product_.Images.Path()),

    crud.OrderBy(Product_.Category.Name.Asc(), Product_.Name.Asc()),
    crud.Limit(20),
)
```

Генератор раскрывает связи до `-depth` (по умолчанию 2). Если `Category` не
появился в `Product_`, увеличьте глубину и перегенерируйте:

```bash
go run github.com/frostgrove/vv/cmd/vv generate -dir ./src/app -depth 3
```

`Product_.Category.Slug.Eq(...)` фильтрует **корневые Products** по связанной
категории. Он не загружает `Category` в ответ — для этого отдельно нужен
`Preload`.

Для фильтра самого preload используйте `PreloadWhere`:

```go
items, err := products.GetAll(ctx,
    crud.PreloadWhere(
        Product_.Images.Path(),
        specs.As(ProductImage_.Published.Eq(true)),
    ),
)
```

Условие в `PreloadWhere` пишется против целевой модели (`ProductImage_`), а не
`Product_.Images`. Это разные вопросы: первый фильтрует детей в загруженной
связи, второй — родителей, у которых есть подходящий ребёнок.

## 6. Проекция и сводки

Когда нужна не полная модель, можно сократить поля, сохранив первичный ключ:

```go
page, err := products.Get(ctx,
    crud.Select(Product_.ID.Name(), Product_.Name.Name()),
    crud.Limit(50),
)
```

Для групповых отчётов используйте тот же фильтр, что и у чтения:

```go
rows, err := products.Aggregate(ctx,
    specs.As(Product_.CreatedAt.Gte(monthStart)),
    crud.GroupBy(Product_.CategoryID.Name()),
    crud.Aggregate(crud.CountAll("products")),
)
```

`Aggregate` возвращает не `Product`, а строки агрегатов. Подробный список
агрегатов (`CountAll`, `Sum`, `Avg`, `Min`, `Max`) есть в
[crud](../modules/ru/crud.md#агрегаты).

## 7. Запись: Save, SaveOnly и Update

### Save возвращает то, что сохранила БД

```go
input := Product{Name: "Chair"}
saved, err := products.Save(ctx, &input)
if err != nil {
    return err
}

// saved содержит ID, timestamp, значения триггеров и т. п.
// input не менялся.
```

Нулевой первичный ключ означает insert, ненулевой — upsert. PostgreSQL и SQLite
используют `RETURNING`; на другом диалекте vv делает нужное дочитывание внутри
одной транзакции.

Если итоговая строка не нужна, выбирайте чистый write:

```go
err := products.SaveOnly(ctx, &Product{ID: id, Name: "Chair"})
```

`SaveOnly` не добавляет `RETURNING`, не делает дополнительный select и тоже не
меняет переданный объект. Для пачки есть `SaveAll(ctx, []*Product{...})` с тем
же write-only поведением.

### Частичное обновление

Генератор создаёт `ProductUpdate` специально для `Update`:

```go
updated, err := products.Update(ctx, id, ProductUpdate{
    Name:        utils.Ptr("Oak chair"),
    Description: utils.Null[string](), // SET description = NULL
})
```

| Поле DTO | Значение |
|---|---|
| `*T == nil` | не менять колонку |
| `*T != nil` | записать значение |
| `utils.Opt[T]` undefined | не менять колонку |
| `utils.Opt[T]` null | записать SQL `NULL` |
| `utils.Opt[T]` set | записать значение |

`Update` сначала читает строку, сравнивает её с DTO и обновляет только реально
изменившиеся колонки. Возвращаемая модель — новое значение из БД; DTO и старый
объект не мутируются.

### Массовое изменение и удаление

```go
inCategory := Product_.CategoryID.Eq(categoryID)

n, err := products.UpdateAll(ctx, ProductUpdate{
    Description: utils.Null[string](),
}, specs.As(inCategory))

n, err = products.DeleteAll(ctx, specs.As(inCategory))
```

Это один statement, без загрузки каждой строки. Не подменяйте им `Update`:
`UpdateAll` не сравнивает старое значение и не возвращает модели. Если нужен
защитный API для bulk-операций с обязательной спецификацией, оберните
репозиторий в `specs.Executor` и используйте `UpdateBy`/`DeleteBy` — они
откажутся от пустого фильтра.

## 8. Транзакция

Несколько вызовов на одном репозитории — или разных репозиториях от одного
source — объединяются через `Tx`:

```go
err := products.Tx(ctx, func(txCtx context.Context) error {
    saved, err := products.Save(txCtx, &Product{Name: "Chair"})
    if err != nil {
        return err
    }
    _, err = images.Save(txCtx, &ProductImage{
        ProductID: saved.ID,
        Path:      "chair.png",
    })
    return err
})
```

`txCtx` обязателен: именно в нём находится transaction executor. Если
транзакцию уже начал GORM, ent или код на `database/sql`, передайте её vv через
`crud.WithExecutor`/`crud.WithExecutorFor`; детали и примеры драйверов — в
[crud](../modules/ru/crud.md#шов-исполнителя).

## 9. Что выбрать

| Сценарий | Используйте |
|---|---|
| Одно условие в одном месте | `crud.Where(crud.Eq(...))` |
| Фильтр из формы / несколько частей | `specs.AllOf`, `specs.If`, `EqPtr`, `EqOpt` |
| Фильтр используется в нескольких местах | именованную `specs.Specification` |
| Нужны связи, безопасная сортировка по полям | `Product_` и `specs.As(...)` |
| Нужна строка после insert/upsert | `Save` |
| Нужен только write | `SaveOnly` или `SaveAll` |
| PATCH одной строки | `Update` + сгенерированный `ProductUpdate` |
| Массовый write по строгому фильтру | `specs.Executor(...).UpdateBy` / `DeleteBy` |

Дальше: [specs](../modules/ru/specs.md) — для типизированных запросов;
[crud/sqlrepo](../modules/ru/sqlrepo.md) — настройки репозитория, soft delete,
реплики и адаптеры; [генератор](../modules/ru/vv-cli.md) — все флаги.
