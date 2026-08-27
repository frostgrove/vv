# crudfiber — полноценный CRUD API на Fiber v3

```go
import "github.com/frostgrove/vv/crud/http/crudfiber"
```

```bash
go get github.com/frostgrove/vv/crud/http/crudfiber
```

**Модуль:** отдельный — чтобы потребитель на Gin, Echo или `net/http` никогда не
тянул Fiber в зависимости ([[D-033]]) · **Требует:** `github.com/gofiber/fiber/v3`

Десять маршрутов, полный DSL запросов, пагинация, preload'ы, жизненный цикл
create/patch/replace и конверт ошибок — смонтированные как под-приложение Fiber.

Каждое имя опции, каждый код статуса и форма каждого ответа **идентичны**
[crudgin](crudgin.md) и [crudnet](crudnet.md). Отличается только монтирование,
какие кодировки тела принимаются и что роутер делает с путём, которого у него нет.

---

## Монтирование

```go
articles := specs.Executor(Articles.Bind(db, security.Gate(policy)))

app := fiber.New()
app.Use("/articles", crudfiber.New(articles).Routes())

app.Listen(":8080")
```

`Routes()` возвращает `*fiber.App` — монтируемое под-приложение, которое есть у
Fiber и которого нет у других биндингов. `Register(r fiber.Router)` регистрирует
на уже имеющемся роутере или группе.

## Маршруты

| Маршрут | Делает |
|---|---|
| `GET /` | список, DSL в строке запроса |
| `POST /query` | список, полный JSON DSL |
| `GET /count` · `POST /count` | подсчёт, тот же DSL |
| `GET /:id` | одна сущность, `?preload=…&select=…` |
| `POST /` | создание |
| `PATCH /:id` | частичное обновление через DTO |
| `PUT /:id` | замена; там, где ключ владения у базы данных — не создаёт |
| `DELETE /:id` | удаление одной записи |
| `POST /bulk-delete` | `{"ids": […]}` |

Каждый маршрут — это ещё и метод: `List`, `Query`, `CountGet`, `CountPost`,
`GetByID`, `Create`, `Update`, `Replace`, `Delete`, `BulkDelete` — так что их
можно регистрировать по одному.

## Четыре конструктора

```go
crudfiber.New(repo, opts...)                  // репозиторий; модель и есть wire-формат
crudfiber.NewFor(repo, mapper, opts...)       // …с собственным телом запроса
crudfiber.Serving(svc, opts...)               // port.Service — ваши бизнес-правила
crudfiber.ServingFor(svc, mapper, opts...)    // и то, и другое
```

`New` принимает **интерфейс** ([[D-022]]), так что ему удовлетворяет и ваш
собственный тип сервиса:

```go
type articleService struct {
    specs.Repo[Article, int64, ArticleUpdate]
}

func (s articleService) Save(ctx context.Context, a *Article) (Article, error) {
    if err := s.checkQuota(ctx, a); err != nil { return Article{}, err }
    return s.Repo.Save(ctx, a)
}

app.Use("/articles", crudfiber.New(articleService{…}).Routes())
```

`crudfiber.Repository` и `crudfiber.Service` — это **алиасы** типов `port`, так
что одно и то же значение монтируется на Gin, `net/http` и gRPC без изменений.

## Опции

| Опция | Делает |
|---|---|
| `WithQuery(cfg)` | ограничивает DSL — [`query.Config`](query.md#bounding-it) |
| `WithScope(fn)` | `func(fiber.Ctx) ([]crud.Option, error)` |
| `WithTransform(fn)` | presenter: `func(fiber.Ctx, M) any` |
| `BeforeSave(fn)` | `func(fiber.Ctx, *M) error`, при create и replace |
| `BeforeUpdate(fn)` | `func(fiber.Ctx, ID, *U) error` |
| `ReadOnly()` | зарегистрировать только чтения, ничего больше |
| `AllowClientID()` | разрешить create выбрать собственный сгенерированный базой ключ |
| `MaxBulk(n)` | ограничить `POST /bulk-delete` — по умолчанию `port.DefaultMaxBulk` (1024), «без предела» не бывает |
| `MaxBody(n)` | ограничить тело запроса, которое читает этот хендлер, в байтах; по умолчанию 4 МиБ, тело сверх лимита — 413 ([[D-063]]) |
| `WithRenderer(r)` | заменить конверт |
| `WithErrorHandler(fn)` | `func(fiber.Ctx, error) error` |

Каждая опция ниже принимает три параметра типа ресурса явно —
`WithQuery[Article, int64, ArticleUpdate](cfg)`. `New` выводит их из
репозитория, который ему передан; опция же — значение, построенное до вызова
`New`, а Go выводит параметры функции из её собственных аргументов, которые ни
одного из них не называют. Обычный способ сократить места вызова — локальный
хелпер на ресурс.

**`WithScope` работает только на чтениях, и это не пробел, который ждёт
заполнения.** `Save` и `Delete` не принимают опций, так что предикату на запрос
там просто некуда попасть. Асимметрия выглядит защитой и ею не является: при
скоупе `TenantID = 7` запрос `GET /{id}` по чужой строке отдаст 404, а
`DELETE /{id}` по той же строке — 200. Построчным правилам на записях место в
[`security.Gate`](security.md), чей скоуп действительно доходит до DELETE и
UPDATE.

```go
crudfiber.WithQuery[Article, int64, ArticleUpdate](&query.Config{
    Filterable: []string{"Title", "Views", "Author.*"},
    Sortable:   []string{"CreatedAt", "Views"},
})
```

## Ошибки

Два способа подключения, потому что у Fiber есть оба:

```go
app := fiber.New(fiber.Config{ErrorHandler: crudfiber.ErrorHandler(
    crudhttp.WithMessages(catalogue),
)})
```

```go
app.Use(crudfiber.Errors(crudhttp.WithMessages(catalogue)))
```

`ErrorHandler` — это собственный шов Fiber, покрывающий всё, что отдаёт
приложение. `Errors` — вариант в виде middleware. `crudfiber.DefaultErrorHandler`
— это вариант без настройки, а `crudfiber.Status(err)` экспортирован на случай,
если вы рендерите тело ответа сами.

Статусы сопоставляются по сигнальным ошибкам: `crud.ErrNotFound` → 404,
`crud.ErrForbidden` → 403, `crud.ErrConflict` → 409, ошибки запроса и схемы →
400 с указанием проблемного пути, всё остальное → 500 **без деталей**.

С подключённой [подсистемой ошибок](errs.md) 409 или 422 также несёт
`error_code` и `field` — см. [crudhttp](crudhttp.md#the-envelope).

## Специфика Fiber

| | |
|---|---|
| тела запросов | **JSON, XML и form** — байндер Fiber диспетчеризует по Content-Type. Остальные биндинги — только JSON |
| `/x` против `/x/` | совпадает и то, и другое |
| несмонтированный метод | 405 |
| незанятый путь | 404 |
| повторы в строке запроса | читаются через `QueryArgs().VisitAll`, так что `?f=a&f=b` сохраняет оба значения — `c.Query` бы их схлопнул |

**Что нужно знать о резервном варианте с сырым телом.** `c.Body()` у Fiber
документированно валиден только внутри обработчика, поэтому этот биндинг
копирует его через `crudhttp.KeepBody` перед сохранением. Именно это позволяет
телу ошибки назвать ключ, который клиент отправил на написанном вручную эндпоинте.

## Что отказываются делать create и replace

При create тело привязывается к модели, а затем сгенерированный базой ключ и
каждая колонка с `generated` очищаются — клиент не может выбрать собственный id
или подделать серверную временную метку. `PUT /:id` заменяет и **никогда не
создаёт** там, где ключ принадлежит базе данных ([[D-012]]). `AllowClientID()`
передаёт это право клиенту.

## См. также

- [crudgin](crudgin.md) · [crudnet](crudnet.md) · [crudgrpc](crudgrpc.md) — тот же API в других местах
- [crudhttp](crudhttp.md) — таблица статусов, конверт, шов рендерера
- [port](port.md) — бизнес-правила между обработчиком и репозиторием
- [`_examples/pgx-fiber`](../../../_examples/pgx-fiber/) · [`_examples/gorm-pgx-fiber`](../../../_examples/gorm-pgx-fiber/) · [`_examples/ent-pgx-fiber`](../../../_examples/ent-pgx-fiber/)
- [[FL-013]] запрос через другой биндинг · [[D-034]] транспортный биндинг — это обёртка над crudhttp
