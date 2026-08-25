# remote — репозиторий, которого нет в этом процессе

```go
import "github.com/shardit-io/vv/remote"
```

**Модуль:** корневой · **Зависит от:** `crud`, `query`, `port`, `errs` и
стандартной библиотеки · **Манифест контракта:** нет

Потребляющая половина. Один сервис объявляет CRUD-API через `crudnet`,
`crudfiber`, `crudgin` или `crudgrpc`; другой держит `remote.Resource` над той
же моделью и вызывает его теми же методами, какими пользовался бы у своего
собственного репозитория.

**Подключайте**, когда сервису нужен ресурс другого сервиса и иначе пришлось бы
писать `http.Client`, набор структур для тел запросов и switch по статусам.

---

## Как подключить

```go
articles := remote.New[Article, int64, ArticleInput](
    remotehttp.Transport("https://content.internal/articles"))

page, err := articles.Get(ctx,
    crud.Where(crud.Eq("Status", "draft")),
    crud.OrderBy(crud.Desc("CreatedAt")),
    crud.Limit(20))
```

Замените транспорт — и больше ничего не меняется:

```go
articles := remote.New[Article, int64, ArticleInput](
    crudgrpc.Transport(conn, "Article"))
```

`New` паникует, если модель не удаётся описать, если её ключ не совпадает с
`ID`, или если DTO обновления опустошит колонку — те же отказы на старте, что и
у `sqlrepo.Define`, и по той же причине. `TryNew` — то же самое без паники.

## Что вы получаете

`remote.Resource[M, ID, U]` реализует `port.Repository`, поэтому каждый метод —
тот, который вы уже знаете:

| Метод | Маршрут |
|---|---|
| `Get(ctx, opts...)` | `POST /query` · `List` |
| `GetAll(ctx, opts...)` | то же, с `unpaged` |
| `GetByID(ctx, id, opts...)` | `GET /{id}` · `Get` |
| `Count(ctx, opts...)` | `POST /count` · `Count` |
| `Save(ctx, *m)` | `POST /`, если ключ не задан, и `PUT /{id}`, если задан |
| `Update(ctx, id, dto)` | `PATCH /{id}` · `Update` |
| `Delete(ctx, id)` | `DELETE /{id}` · `Delete` |
| `Delete(ctx, ids...)` | `POST /bulk-delete` · `BulkDelete` |

Это **не** `crud.Core`: там есть `Tx`, а транзакция не пересекает вызов без
состояния.

И раз это `port.Repository`, он ещё и композируется. Смонтируйте его на свои
маршруты — получится шлюз:

```go
crudnet.New(articles).Mount(mux, "/articles")
```

## Ошибки продолжают работать

Ради этого пакет и существует. Отказ приходит как `*errs.Fault`, оборачивающий
тот же sentinel, который обернул бы локально:

```go
if _, err := articles.GetByID(ctx, 42); errors.Is(err, crud.ErrNotFound) {
    // та же ветка, по какую бы сторону сети ни лежала строка
}
```

и несёт то, на что клиент может отреагировать:

```go
if f, ok := errs.AsFault(err); ok {
    for _, v := range f.Violations {
        fmt.Println(v.Path, v.Code, v.Message) // ["email"] unique "…"
    }
}
```

Чего не приходит никогда — ничего внутреннего: ни имени ограничения, ни имени
таблицы, ни номера ошибки движка, ни фразы драйвера ([[D-044]]). Внутренний сбой
приходит вообще без слов.

**Ответ, пришедший не от этой библиотеки, никогда не читается как её ответ.**
Неверный базовый URL, прокси, API-шлюз или метод, который read-only сервис не
регистрировал, приходят как `*remote.ProtocolError`:

```go
var pe *remote.ProtocolError
if errors.As(err, &pe) {
    log.Printf("%s ответил %s", pe.Where, pe.Status)
}
```

Без этой проверки 404 от роутера читался бы как `crud.ErrNotFound`, и
неправильно настроенный сервис сообщал бы о пустой таблице ровно до тех пор,
пока кто-нибудь не посмотрит.

## Чего стоит сеть

Каждая `crud.Option` получает один из трёх ответов, и никогда четвёртый
([[D-053]]).

**Переносится** — `Page`, `Limit`, `Offset`, `OrderBy`/`SortBy`, `Select`,
`Preload`, `After`, `Before`, `Unpaged`, `SkipTotal`, `Distinct` и `Where` с
любым предикатом, который умеет выразить проводной DSL.

**Отклоняется, до того как что-либо отправлено** — `*remote.OptionError`:

| Опция | Почему |
|---|---|
| `crud.NarrowRelations` | scope по связи идёт за preload в другую таблицу, а туда не доходит ни один filter-документ. `security.Gate` поверх удалённого ресурса обязан упасть громко, а не протечь |
| `crud.ForUpdate` | блокировка строки принадлежит транзакции, а транзакции в этом процессе нет |
| `crud.Aggregate` / `crud.GroupBy` | ни один биндинг не отдаёт маршрут агрегата |

и внутри фильтра — `*crud.PredicateError` ([[D-054]]):

| Предикат | Почему |
|---|---|
| `crud.Raw` | это SQL, а filter-документ несёт пути полей и значения |
| `crud.EqField` | DSL сравнивает поле со значением, но никогда с другим полем |
| `crud.False`, `crud.Or()` | они не подходят ни под одну строку, а такого не говорит ни один документ |

**Принимается, но не может быть выполнено** — названо здесь потому, что молча
выброшенная опция и есть тот единственный сбой, которого вызывающая сторона не
видит:

- `crud.PrimaryOnly` — в DSL нет слова для «не отдавать это с реплики». Если
  реплика отстаёт, настраивать надо дальний сервис. Принимается, а не
  отклоняется, потому что `security.Gate` ставит эту опцию почти на каждый вызов.
- `crud.Unsorted` — пустой sort в документе означает «сервис решает сам», а не
  «без порядка». Строки те же самые.

## DTO обновления

Берите то, что генерирует `cmd/vv`. Написанное вручную DTO, у которого поля
`crud.Opt` не помечены `omitzero`, маршалит неопределённое значение как `null`,
и патч одной колонки опустошит все остальные nullable-колонки строки.
`remote.New` отклоняет такое DTO на старте, не давая ему дойти до базы.

```go
type ArticleInput struct {
    Title *string          `json:"title,omitempty"`
    Views *int             `json:"views,omitempty"`
    Note  crud.Opt[string] `json:"note,omitzero"`   // этот тег несущий
}
```

## Транспорты

| Транспорт | Где | Опции |
|---|---|---|
| `remotehttp.Transport(baseURL, …)` | `vv/crud/http/crudhttp` | `WithClient(*http.Client)`, `WithRequestHook(func(*http.Request) error)` |
| `crudgrpc.Transport(conn, name, …)` | `vv/crud/rpc/crudgrpc` | `WithVocabulary(*errs.Codes)`, `WithCallOptions(…)` |

Транспорт живёт рядом с тем биндингом, который он вызывает, поэтому таблица,
превращающая статус или код обратно в класс, лежит в том же файле, что и
таблица, которая его породила ([[D-045]]).

**HTTP-клиент один, а не три.** Потребитель, который ходит наружу, использует
`net/http` — чем бы он ни отдавал сам, — а три HTTP-биндинга регистрируют одни и
те же маршруты.

`WithRequestHook` — место для заголовка `Authorization`, трассировки или
`Accept-Language`:

```go
remotehttp.Transport(base, remotehttp.WithRequestHook(func(r *http.Request) error {
    r.Header.Set("Authorization", "Bearer "+token)
    return nil
}))
```

Два различия, видимых вызывающей стороне ([[FL-013]]):

- По HTTP `GetByID` несёт пути preload в query-строке, поэтому **сужённый**
  preload там отклоняется. gRPC отправляет документ целиком.
- По gRPC 422 и 400 — это один `InvalidArgument`; машинный код отменяет
  схлопывание, так что `errs.AsFault(err).Kind` по-прежнему их различает.

## Свой транспорт

Реализуйте один метод:

```go
type Transport interface {
    Do(ctx context.Context, call remote.Call) (json.RawMessage, error)
}
```

`remote.Call` несёт метод, текстовый ключ, JSON-массив ключей, query-документ и
сырое тело — и ничего про URL, заголовок или соединение. Неудачный вызов обязан
вернуться как `*errs.Fault`, построенный через `port.FaultFrom`: именно это
сохраняет рабочей ветку `errors.Is` у вызывающей стороны.

## Смотрите также

[[UC-018]] · [[FL-018]] · [[D-053]] · [[D-054]] · [[D-045]] ·
[crudhttp](crudhttp.md) · [crudgrpc](crudgrpc.md) · [port](port.md)
