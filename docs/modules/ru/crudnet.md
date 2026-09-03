# crudnet — полноценный CRUD API на net/http

```go
import "github.com/frostgrove/vv/crud/http/crudnet"
```

**Модуль:** корневой — импортирует только стандартную библиотеку, поэтому
ничего не стоит · **Зависит от:** `crud`, `query`, `errs`, `port`, `crudhttp`, `net/http`

Десять маршрутов, полный DSL запросов, пагинация, preload, жизненный цикл
create/patch/replace и конверт ошибок — на `net/http`, вообще без зависимостей.

На `net/http` поверх `database/sql` `go get github.com/frostgrove/vv` — это вся
установка.

---

## Подключение

```go
articles := specs.Executor(Articles.Bind(db, security.Gate(policy)))

mux := http.NewServeMux()
crudnet.New(articles).Mount(mux, "/articles")

http.ListenAndServe(":8080", crudnet.Errors()(mux))
```

```http
POST /articles/query
{
  "filter": { "views": {"gte": 100}, "tags.slug": {"in": ["go","rust"]} },
  "preload": ["author", "comments.author"],
  "sort": ["-views", "author.name"],
  "page": 2, "limit": 20
}
```

## Маршруты

| Маршрут | Делает |
|---|---|
| `GET /` | список, DSL в строке запроса |
| `POST /query` | список, полный JSON DSL |
| `GET /count` · `POST /count` | подсчёт, тот же DSL |
| `GET /{id}` | одна сущность, `?preload=…&select=…` |
| `POST /` | создание |
| `PATCH /{id}` | частичное обновление через DTO |
| `PUT /{id}` | замена; там, где ключ владеет база данных, не создаёт |
| `DELETE /{id}` | удаление одной записи |
| `POST /bulk-delete` | `{"ids": […]}` |

**Каждый метод маршрута — обычный `http.HandlerFunc`**, поэтому chi,
gorilla/mux и httprouter могут регистрировать их по одному вместо вызова
`Mount`. Это стоит знать даже если ваш роутер не `ServeMux`.

## Шесть конструкторов

```go
crudnet.New(repo, opts...)                  // репозиторий; модель и есть wire-формат
crudnet.NewFor(repo, mapper, opts...)       // …со своим телом запроса
crudnet.Serving(svc, opts...)               // port.Service — ваши бизнес-правила
crudnet.ServingFor(svc, mapper, opts...)    // и то, и другое
```

`NewWire` и `ServingWire` — явная форма под этими четырьмя. Они принимают маппер
создания, `wire.PatchMapper` и `wire.Presenter`, так что публичное тело PATCH и
тело ответа становятся отдельными типами, а не persistence-DTO и моделью
([[D-105]]):

```go
crudnet.ServingWire(svc, ArticleInputMapper{}, ArticlePatchMapper{}, ArticlePresenter{})
```

Четыре коротких конструктора подставляют `wire.IdentityPatch` и
`wire.IdentityPresenter` — поэтому в них ничего не изменилось. См.
[wire](wire.md).

`New` принимает **интерфейс**, а не конкретный репозиторий, поэтому ему
удовлетворяет и ваш собственный тип сервиса ([[D-022]]):

```go
type articleService struct {
    specs.Repo[Article, int64, ArticleUpdate]   // встроено: тем самым бесплатно реализует интерфейс
}

func (s articleService) Save(ctx context.Context, a *Article) (Article, error) {
    if err := s.checkQuota(ctx, a); err != nil { return Article{}, err }
    return s.Repo.Save(ctx, a)
}

crudnet.New(articleService{…}).Mount(mux, "/articles")
```

`crudnet.Repository` и `crudnet.Service` — это **алиасы** типов из `port`, а
не отдельные интерфейсы, поэтому одно и то же значение монтируется на Fiber,
Gin и gRPC без единой строки изменений.

## Опции

| Опция | Делает |
|---|---|
| `WithQuery(cfg)` | ограничивает DSL — [`query.Config`](query.md#bounding-it) |
| `WithScope(fn)` | сужение на уровне запроса: `func(*http.Request) ([]crud.Option, error)` |
| `WithTransform(fn)` | презентер: `func(*http.Request, M) any`. Применяется и к сущностям, и к страницам |
| `BeforeSave(fn)` | `func(*http.Request, *M) error`, при create и replace |
| `BeforeUpdate(fn)` | `func(*http.Request, ID, *U) error` |
| `ReadOnly()` | регистрирует только чтение и ничего больше |
| `Exposing(ops)` | регистрирует ровно названные операции — `port.Reads`, `port.Writes`, `port.Deletes` и десять `port.Op*` складываются через `\|` |
| `AllowClientID()` | разрешить create выбрать собственный ключ, генерируемый базой |
| `MaxBulk(n)` | ограничивает `POST /bulk-delete` — по умолчанию `port.DefaultMaxBulk` (1024), «без предела» не бывает |
| `MaxBody(n)` | ограничить тело запроса, которое читает этот хендлер, в байтах; по умолчанию 4 МиБ, тело сверх лимита — 413 ([[D-063]]) |
| `WithRenderer(r)` | заменяет конверт |
| `WithErrorHandler(fn)` | `func(http.ResponseWriter, *http.Request, error)` |

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

Каждая из этих опций пишется одинаково во всех четырёх биндингах.

## От чего отказываются create и replace

При create тело привязывается к модели, после чего ключ, генерируемый базой
данных, и все столбцы `generated` **очищаются** — клиент не может выбрать
собственный id или подделать серверный timestamp.

`PUT /{id}` подчиняется тому же правилу с другой стороны: там, где ключ
генерирует база данных, он заменяет существующую строку и **никогда не
создаёт новую**, так что пространство id остаётся за сервером ([[D-012]]).
`AllowClientID()` передаёт его клиенту, а ключ, которым клиент и так владеет
— uuid, slug — это не затрагивает.

## Ошибки

```go
mux.Handle("/", crudnet.Errors(crudhttp.WithMessages(catalogue))(routes))
```

`Errors` рендерит ошибку, которую вернул `HandlerFunc`, превращает панику в
тихий 500 и не трогает обработчик, который уже написал ответ. Он покрывает и
маршруты этого биндинга, и написанные вручную, поэтому смонтировать его над
mux, несущим и то, и другое — один вызов.

`crudnet.WithErrors(f, opts...)` адаптирует один обработчик, возвращающий
ошибку.

Статусы отображаются по сентинелу, поэтому транспорт никогда не импортирует
декоратор, который их выбросил: `crud.ErrNotFound` → 404,
`crud.ErrForbidden` → 403, `crud.ErrConflict` → 409, ошибки запроса и схемы →
400 с указанием пути, всё остальное → 500 **без подробностей**.
`crudnet.Status(err)` экспортируется на случай, если вы рендерите тело
ответа сами.

С подключённой [подсистемой ошибок](errs.md) 409 или 422 также несёт
`error_code` и `field` — см. [crudhttp](crudhttp.md#the-envelope).

## Две детали монтирования

- **Коллекция регистрируется под обоими написаниями.** У `ServeMux` нет
  редиректа с завершающего слэша, так что `/articles` и `/articles/` — это
  два разных паттерна, и незарегистрированный отвечает 404. `Mount`
  регистрирует оба.
- **В корне коллекция — это `"/{$}"`, никогда не `"/"`.** Голый `/` —
  catch-all у `ServeMux`: смонтированный в корне, он отвечал бы на любой
  незанятый путь в процессе, возвращая 200 и страницу строк там, где
  приложение подразумевало 404.

Несмонтированный метод на смонтированном пути отвечает **405**.

## Routing — собственный 404 у mux и 405, до которого не дотянуться

| | |
|---|---|
| `Routing(mux, opts…)` | регистрирует catch-all паттерн, рендеря 404 в конверте |

Путь, который никто не занял, отвечает сам `http.ServeMux` — до того как
отработает хоть один хендлер или middleware этой библиотеки, — и пишет он
`404 page not found` как `text/plain`, так что клиенту, разбирающему одну форму
на все отказы, разбирать нечего. `Routing` ставит catch-all `/` — единственный
шов, который даёт net/http.

**Глагол, которого у пути нет, — по-прежнему собственный 405 у mux.** Этот отказ
не доходит до хендлера: mux находит путь, не находит метода и отвечает сам. У
`crudfiber` и `crudgin` шов для этого есть, у этой привязки — нет ([[FL-013]]).

Вызывайте один раз, на mux, у которого нет своего `/`. Регистрация одного
паттерна дважды — паника из стандартной библиотеки, и она правильная: два
catch-all означают, что один из них никогда не отвечает.

## Смотрите также

- [crudfiber](crudfiber.md) · [crudgin](crudgin.md) · [crudgrpc](crudgrpc.md) — тот же API в другом месте
- [crudhttp](crudhttp.md) — таблица статусов, конверт, точка расширения рендерера
- [port](port.md) — бизнес-правила между обработчиком и репозиторием
- [[UC-001]] предоставить CRUD API без обработчиков · [[FL-013]] запрос через другой биндинг
