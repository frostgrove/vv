# remote — репозиторий, которого нет в этом процессе

```go
import "github.com/frostgrove/vv/remote"
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
| `GetAll(ctx, opts...)` | все подходящие строки (либо явно запрошенное подмножество); обход с cursor edge делает page/offset cap размером чанка, а не обрезанным успехом. Shape без cursor и DISTINCT без PK всё ещё требует достаточный `MaxOffset`. |
| `GetByID(ctx, id, opts...)` | напрямую: `GET /{id}` · `Get`; root filter либо суженный/capped preload: `POST /query` · `List` с равенством primary key |
| `First(ctx, opts...)` | `List` со страницей в одну строку; при пустом ответе — `crud.ErrNotFound` |
| `Count(ctx, opts...)` | `POST /count` · `Count` |
| `Save(ctx, *m)` | `POST /`, если ключ не задан, и `PUT /{id}`, если задан; возвращает сохранённую модель и не меняет `m` |
| `SaveOnly(ctx, *m)` | тот же create/replace, но ответ-сущность отбрасывается, а `m` не меняется |
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
`Preload`/`PreloadWhere`/`PreloadCap`, `After`, `Before`, `Unpaged`, `SkipTotal`, `Distinct` и `Where` с
любым предикатом, который умеет выразить проводной DSL. Keyed `GetByID` также
сохраняет root filter: когда он есть (либо preload сужен или capped), клиент использует
document-shaped List route с дополнительным равенством primary key. Прямой
entity route сохраняет только projection и обычные preload paths; ordering и
paging не могут изменить keyed result и отбрасываются.

Этот fallback намеренно является обычным публичным List-запросом: endpoint с
ограничительным `query.Config.Filterable` должен разрешать primary key **и
каждое поле фильтра вызывающего**, как и для прямого `Get`. Иначе endpoint
вернёт обычный отказ query вместо ослабления keyed-read.

`GetAll` сбрасывает `SkipTotal` и превращает запрос всех строк — без page
controls либо с `Unpaged` (он побеждает переданные page/limit) — в ограниченные
запросы. С `Unpaged+Offset` он возвращает весь suffix после этого offset. Он
запрашивает первую страницу и затем идёт по любому полученному cursor edge. Он
использует sort вызывающего или явный sort по primary key, если sort не задан;
так он не упирается ни в `MaxOffset` удалённого endpoint, ни в maximum page
size. Endpoint с ограничительными allow-list должен поэтому разрешать этот sort
и primary-key filter курсора (либо вызывающий передаёт разрешённый уникальный
sort).

Есть одно намеренное исключение: `Distinct` с явной projection без primary key
нельзя безопасно keyset-сортировать, не изменив distinct-результат. Такая форма
остаётся на offset-страницах; так же ведёт себя кастомный list без cursor edge,
и в обоих случаях его `MaxOffset` обязан покрывать export. Иначе клиент вернёт
отказ endpoint, а не представит этот отказ успешным частичным результатом.
Непустая cursor-страница с edge читается
даже если `HasNext` или `HasPrev` устарел; terminal empty page, достигнутая по
этому edge, доказывает окончание. Кастомный cursor-ответ без terminal edge
вместо этого объявляет конец соответствующим `HasNext`/`HasPrev`. Пустая
страница, утверждающая что есть ещё данные, повторяющийся/отсутствующий edge
либо несогласованный offset total возвращает
`*remote.PartialResultError` (`errors.Is(err, remote.ErrPartialResult)`).
Только явный offset без `Unpaged`, либо явные page/limit без `Unpaged`,
сохраняют обычную семантику одного подмножества.

Как любое чтение сети из нескольких запросов, это enumeration, а не
транзакционный snapshot: строки, которые удалённый сервис вставляет, удаляет
или переупорядочивает во время обхода, могут перейти между страницами. Проверки
согласованности ловят противоречивый протокол, но не меняющийся набор данных.
Export, которому нужен один snapshot базы, должен быть отдельной far-side
операцией со своим контрактом.

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
`utils.Opt` не помечены `omitzero`, маршалит неопределённое значение как `null`,
и патч одной колонки опустошит все остальные nullable-колонки строки.
`remote.New` отклоняет такое DTO на старте, не давая ему дойти до базы.

```go
type ArticleInput struct {
    Title *string          `json:"title,omitempty"`
    Views *int             `json:"views,omitempty"`
    Note  utils.Opt[string] `json:"note,omitzero"`   // этот тег несущий
}
```

## Транспорты

| Транспорт | Где | Опции |
|---|---|---|
| `remotehttp.Transport(baseURL, …)` | `vv/remote/remotehttp` | `WithClient(*http.Client)`, `WithMaxResponse(int)`, `WithRequestHook(func(*http.Request) error)` |
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

- Filtered `GetByID`, в том числе со сужённым или capped preload, использует List на
  обоих transport, поэтому документ приходит целиком. Нефильтрованное HTTP
  entity-чтение остаётся `GET /{id}` и несёт только projection и обычные
  preload paths; gRPC может нести ту же форму как документ.
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
