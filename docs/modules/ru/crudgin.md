# crudgin — полноценный CRUD API на Gin

```go
import "github.com/frostgrove/vv/crud/http/crudgin"
```

```bash
go get github.com/frostgrove/vv/crud/http/crudgin
```

**Модуль:** отдельный — чтобы потребитель на Fiber, Echo или `net/http` не
тянул Gin в зависимости ([[D-033]]) · **Требует:** `github.com/gin-gonic/gin`

Десять маршрутов, полный DSL запросов, пагинация, preload, жизненный цикл
create/patch/replace и конверт ошибок, монтируемые на любой `gin.IRouter`.

Каждое имя опции, каждый код статуса и каждая форма ответа **идентичны**
[crudfiber](crudfiber.md) и [crudnet](crudnet.md). Отличается только
монтирование, какие кодировки тела принимаются и что роутер делает с
завершающим слэшем или методом, которого у него нет.

---

## Монтирование

```go
articles := specs.Executor(Articles.Bind(db, security.Gate(policy)))

r := gin.Default()
crudgin.New(articles).Mount(r, "/articles")

r.Run(":8080")
```

`Mount(r gin.IRouter, prefix string)` — однострочный вариант.
`Register(r gin.IRoutes)` принимает уже существующий движок или группу. У Gin
нет монтируемого под-приложения, поэтому аналога `Routes()` из Fiber здесь нет.

## Маршруты

| Маршрут | Делает |
|---|---|
| `GET /` | список, DSL в строке запроса |
| `POST /query` | список, полный JSON DSL |
| `GET /count` · `POST /count` | подсчёт, тот же DSL |
| `GET /:id` | одна сущность, `?preload=…&select=…` |
| `POST /` | создание |
| `PATCH /:id` | частичное обновление через DTO |
| `PUT /:id` | замена; там, где ключом владеет база, создания не будет |
| `DELETE /:id` | удаление одной записи |
| `POST /bulk-delete` | `{"ids": […]}` |

Каждый маршрут — это ещё и метод `gin.HandlerFunc`, так что их можно
регистрировать по одному.

## Четыре конструктора

```go
crudgin.New(repo, opts...)                  // репозиторий; модель — форма для передачи
crudgin.NewFor(repo, mapper, opts...)       // …со своей формой тела запроса
crudgin.Serving(svc, opts...)               // port.Service — ваши бизнес-правила
crudgin.ServingFor(svc, mapper, opts...)    // оба вместе
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

crudgin.New(articleService{…}).Mount(r, "/articles")
```

`crudgin.Repository` и `crudgin.Service` — это **алиасы** для типов `port`,
так что одно и то же значение монтируется на Fiber, `net/http` и gRPC без
изменений.

## Опции

| Опция | Делает |
|---|---|
| `WithQuery(cfg)` | ограничивает DSL — [`query.Config`](query.md#bounding-it) |
| `WithScope(fn)` | `func(*gin.Context) ([]crud.Option, error)` |
| `WithTransform(fn)` | презентер: `func(*gin.Context, M) any` |
| `BeforeSave(fn)` | `func(*gin.Context, *M) error`, при create и replace |
| `BeforeUpdate(fn)` | `func(*gin.Context, ID, *U) error` |
| `ReadOnly()` | зарегистрировать только чтение и ничего больше |
| `AllowClientID()` | разрешить create самому выбрать ключ, генерируемый базой |
| `MaxBulk(n)` | ограничить `POST /bulk-delete` — по умолчанию `port.DefaultMaxBulk` (1024), «без предела» не бывает |
| `MaxBody(n)` | ограничить тело запроса, которое читает этот хендлер, в байтах; по умолчанию 4 МиБ, тело сверх лимита — 413 ([[D-063]]) |
| `WithRenderer(r)` | заменить конверт |
| `WithErrorHandler(fn)` | `func(*gin.Context, error)` |

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

## Ошибки

```go
r.Use(crudgin.Errors(crudhttp.WithMessages(catalogue)))
```

Он рендерит то, с чем упал обработчик, превращает панику в тихий 500 и не
трогает обработчик, который уже записал ответ. Он также вызывает
`c.Error(err)`, так что причина доходит и до собственного логирующего
middleware Gin.

`crudgin.DefaultErrorHandler` — вариант без настройки, а `crudgin.Status(err)`
экспортирован на случай, если вы рендерите тело ответа сами.

Статусы отображаются по сентинелам: `crud.ErrNotFound` → 404,
`crud.ErrForbidden` → 403, `crud.ErrConflict` → 409, ошибки запроса и схемы →
400 с указанием проблемного пути, всё остальное → 500 **без деталей**.

При подключённой [подсистеме ошибок](errs.md) 409 или 422 также несут
`error_code` и `field` — см. [crudhttp](crudhttp.md#the-envelope).

## Особенности Gin

| | |
|---|---|
| тело запроса | **только JSON** |
| `/x` против `/x/` | `/x` матчится; `/x/` получает **301** от `RedirectTrailingSlash` Gin |
| немонтированный метод | **404** — задайте `Engine.HandleMethodNotAllowed` для 405 |
| незанятый путь | 404 |
| повторы в строке запроса | читаются из `c.Request.URL.Query()`, так что `?f=a&f=b` сохраняет оба значения — `c.Query` их бы схлопнул |

Три детали, которые стоит знать:

- **Маршрут коллекции регистрируется как `""`, а не `"/"`.** На группе
  `/articles` форма `"/"` даёт `/articles/`, что не совпадает с
  `GET /articles`. Регистрировать оба варианта нельзя — на самом движке они
  схлопываются, и Gin паникует с *handlers are already registered*.
- **`c.ShouldBindJSON` намеренно не используется.** Биндер Gin прогоняет
  `validator/v10` по тегам `binding:"…"`, так что тег на вашей модели менял бы
  то, что принимают CRUD-маршруты, только под **одним** транспортом, а не под
  остальными. Этот биндинг декодирует через `encoding/json` ([[D-045]]).
- **`HandleMethodNotAllowed` — настройка приложения**, и этот обработчик её
  не трогает.

## От чего отказываются create и replace

При create тело биндится на модель, после чего ключ, генерируемый базой, и
каждая колонка `generated` очищаются — клиент не может выбрать свой id или
подделать серверную временную метку. `PUT /:id` заменяет и **никогда не
создаёт** там, где ключом владеет база ([[D-012]]). `AllowClientID()`
передаёт это право клиенту.

## См. также

- [crudfiber](crudfiber.md) · [crudnet](crudnet.md) · [crudgrpc](crudgrpc.md) — тот же API в других местах
- [crudhttp](crudhttp.md) — таблица статусов, конверт, точка расширения рендерера
- [port](port.md) — бизнес-правила между обработчиком и репозиторием
- [`_examples/sqlx-pgx-gin`](../../../_examples/sqlx-pgx-gin/) · [`_examples/gorm-mysql-gin`](../../../_examples/gorm-mysql-gin/) · [`_examples/ent-pgx-gin`](../../../_examples/ent-pgx-gin/)
- [[FL-013]] запрос через другой биндинг · [[D-034]] биндинг транспорта — это оболочка над crudhttp
