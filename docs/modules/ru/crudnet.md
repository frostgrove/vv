# crudnet — полноценный CRUD API на net/http

```go
import "github.com/shardit-io/vv/http/crudnet"
```

**Модуль:** корневой — импортирует только стандартную библиотеку, поэтому
ничего не стоит · **Зависит от:** `crud`, `query`, `errs`, `port`, `crudhttp`, `net/http`

Десять маршрутов, полный DSL запросов, пагинация, preload, жизненный цикл
create/patch/replace и конверт ошибок — на `net/http`, вообще без зависимостей.

На `net/http` поверх `database/sql` `go get github.com/shardit-io/vv` — это вся
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

## Четыре конструктора

```go
crudnet.New(repo, opts...)                  // репозиторий; модель и есть wire-формат
crudnet.NewFor(repo, mapper, opts...)       // …со своим телом запроса
crudnet.Serving(svc, opts...)               // port.Service — ваши бизнес-правила
crudnet.ServingFor(svc, mapper, opts...)    // и то, и другое
```

`New` принимает **интерфейс**, а не конкретный репозиторий, поэтому ему
удовлетворяет и ваш собственный тип сервиса ([[D-022]]):

```go
type articleService struct {
    specs.Repo[Article, int64, ArticleUpdate]   // встроено: тем самым бесплатно реализует интерфейс
}

func (s articleService) Save(ctx context.Context, a *Article) error {
    if err := s.checkQuota(ctx, a); err != nil { return err }
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
| `AllowClientID()` | разрешить create выбрать собственный ключ, генерируемый базой |
| `MaxBulk(n)` | ограничивает `POST /bulk-delete` |
| `WithRenderer(r)` | заменяет конверт |
| `WithErrorHandler(fn)` | `func(http.ResponseWriter, *http.Request, error)` |

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

## Смотрите также

- [crudfiber](crudfiber.md) · [crudgin](crudgin.md) · [crudgrpc](crudgrpc.md) — тот же API в другом месте
- [crudhttp](crudhttp.md) — таблица статусов, конверт, точка расширения рендерера
- [port](port.md) — бизнес-правила между обработчиком и репозиторием
- [[UC-001]] предоставить CRUD API без обработчиков · [[FL-013]] запрос через другой биндинг
