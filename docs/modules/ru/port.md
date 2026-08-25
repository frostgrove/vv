# port — транспортно-нейтральная половина

```go
import "github.com/shardit-io/vv/port"
```

**Модуль:** корневой · **Зависит от:** `crud`, `query`, `errs` и стандартной
библиотеки · **Манифест контракта:** есть ([[D-048]])

Всё, что делает CRUD-ресурс и что не является HTTP. Восемь команд, один
интерфейс `Service`, шов маппера, цепочка путей и конвейер нарушений. Четыре
транспорта — это оболочки поверх него, и одно значение сервиса монтируется на
все четыре без изменений ([[D-045]]).

**Подключайте**, когда кладёте бизнес-правила между транспортом и
репозиторием, когда пишете маппер, или когда строите свой собственный
транспорт.

---

## Service

```go
type Service[M any, ID comparable, U any] interface {
    Meta() *crud.Meta

    List(ctx, ListCommand)                 (crud.PaginatedResponse[M], error)
    Count(ctx, CountCommand)               (int64, error)
    Get(ctx, GetCommand[ID])               (M, error)
    Create(ctx, CreateCommand[M])          (M, error)
    Update(ctx, UpdateCommand[ID, U])      (M, error)
    Replace(ctx, ReplaceCommand[ID, M])    (M, error)
    Delete(ctx, DeleteCommand[ID])         (int64, error)
    DeleteMany(ctx, BulkDeleteCommand[ID]) (int64, error)

    Paths() errs.Resolver
}
```

Три параметра типа, а не четыре: `Mapper` отрабатывает **до** сервиса, так что
входной тип транспорта сюда никогда не доходит. Именно это позволяет одному
значению монтироваться везде.

`port.NewService(repo, opts...)` строит сервис по умолчанию — только
оркестрация и ничего сверх: что не может диктовать запрос на создание, что
count отбрасывает из документа запроса, что должен найти `PUT` прежде чем
заменить.

| Опция | Делает |
|---|---|
| `WithQuery(cfg)` | ограничить DSL запроса ([query.Config](query.md#bounding-it)) |
| `AllowClientID()` | позволить запросу на создание выбрать свой собственный сгенерированный БД ключ |
| `WithPaths(r)` | добавить переход этого сервиса в цепочку путей |

## Бизнес-правила между транспортом и репозиторием

Встраивайте и переопределяйте. Хендлер этого не замечает ([[UC-013]]).

```go
type articleService struct {
    *port.DefaultService[Article, int64, ArticleUpdate]
}

func (s articleService) Create(ctx context.Context, cmd port.CreateCommand[Article]) (Article, error) {
    if err := s.checkQuota(ctx, cmd.Model); err != nil {
        return Article{}, err
    }
    return s.DefaultService.Create(ctx, cmd)
}

svc := articleService{port.NewService(articles)}

crudfiber.Serving(svc).Routes()
crudgin.Serving(svc).Mount(r, "/articles")
crudnet.Serving(svc).Mount(mux, "/articles")
crudgrpc.Serving(svc).Register(srv, "Article")
```

**Одно и то же значение на всех четырёх**, и один интеграционный тест сравнивает
не ответ, а *команду*, которую передал каждый биндинг, — так что биндинг,
заново выводящий правило, будет назван.

Передать вместо сервиса обычный **репозиторий** тоже работает везде — биндинг
сам оборачивает его в `DefaultService`. `New`, `NewFor`, `Serving` и
`ServingFor` — четыре конструктора, которые несёт каждый биндинг.

## Команды

| Команда | Несёт |
|---|---|
| `ListCommand` | `Query *query.Request`, `Options []crud.Option` |
| `CountCommand` | то же самое, сужено |
| `GetCommand[ID]` | `ID`, `Query`, `Options` |
| `CreateCommand[M]` | `Model M`, `Before func(*M) error` |
| `UpdateCommand[ID, U]` | `ID`, `DTO U`, `Before` |
| `ReplaceCommand[ID, M]` | `ID`, `Model M`, `Before` |
| `DeleteCommand[ID]` | `ID` |
| `BulkDeleteCommand[ID]` | `IDs []ID` |

`Options` добавляются **после** компиляции документа запроса, так что скоуп
транспорта сужает фильтр клиента вместо того, чтобы его заменить ([[D-004]]).

---

## Шов маппера

`Mapper` превращает входной тип транспорта в модель, прежде чем её увидит
сервис.

```go
type Mapper[In, M any] interface {
    Model(ctx context.Context, in In) (M, error)
}
```

```go
type ArticleInput struct {
    Title    string `json:"title"`
    AuthorID int64  `json:"authorId"`
}

func (ArticleMapper) Model(ctx context.Context, in ArticleInput) (Article, error) {
    return Article{Title: in.Title, AuthorID: in.AuthorID}, nil
}

crudnet.ServingFor(svc, ArticleMapper{}).Mount(mux, "/articles")
```

`port.Identity[M]()` — маппер-заглушка, который используют `New`/`Serving`,
когда маппер не передан: модель *и есть* форма провода.

Маппер **может также** реализовывать `errs.Resolver`. Тот, что реализует,
добавляет переход адаптера в цепочку путей — потому что именно этот слой выполнял
преобразование, и только он способен его обратить. [`cmd/vv
-adapter`](vv-cli.md) генерирует ровно такой.

---

## Цепочка путей

Нарушение случается в колонке. Клиент хочет услышать про ключ, который он
отправил. Между этими двумя — несколько отображений, и **каждый слой
переводит один переход и только свой** ([[D-043]]).

```
column ──► model field ──► command field ──► the key the client sent
  faults          port.Fields        the mapper's PathMap
```

| Тип | Пишется | Необъявленная голова |
|---|---|---|
| `port.Fields` | вами, вручную | **пропускается насквозь** — написанная от руки карта по природе неполна |
| `port.PathMap` | генератором | **отказывает**, и нарушение помечается как приблизительное |

В этом различии — весь смысл генерации. `PathMap` выводится из модели и
проверяется относительно неё при инициализации пакета, поэтому она *полна*:
у каждой колонки, которую клиент может писать, есть запись. Необъявленная
голова поэтому не пробел — это колонка другой таблицы, и честность лучше
выдумки ([[D-050]]).

```go
var ArticlePaths = port.MustPathMap[Article](port.PathMap{
    "Title":    port.At("title"),
    "AuthorID": port.At("authorId"),
})
```

`MustPathMap` отказывает на старте приложения, если карта не точна **и**
не полна одновременно: пропущенная запись, и точно так же запись для
колонки `generated` или для блокировки — любая из них переводит нарушение в
ключ, которого клиент не найдёт в собственном теле запроса.

`port.At("shipping", "line1")` строит путь. `port.Hops(svc, mapper)` собирает
объявленные переходы по порядку — именно их биндинг подключает перед
собственным резервным вариантом.

## Конвейер нарушений

```go
vs := port.Violations(ctx, fault, port.ViolationOptions{
    Resolvers: port.Hops(svc, mapper),
    Fallback:  porthttp.BodyResolver(rawBody),
    Messages:  catalogue,
    Codes:     codes,
    Max:       port.MaxViolations,   // 100
})
```

Пять шагов, и **порядок несёт смысл**: копирование, цепочка путей, сортировка,
ограничение, сообщение.

- **Сообщения идут после перевода пути**, потому что лестница выводится из
  пути — сделай это раньше, и запись каталога окажется привязана к имени
  поля модели на одном развёртывании и к имени клиента на другом.
- **Ограничение идёт после сортировки**, так что выживает начало полного
  порядка, а не то, что классификатор случайно добавил первым.
- **`Fallback` — отдельное поле, а не последний резолвер**, так что
  объявление всегда побеждает догадку. Он срабатывает только для пути,
  который не изменил ни один объявленный переход.

Локаль читается из контекста (`port.WithLocale`, `port.LocaleFrom`), а не
передаётся явно, так что транспорт, который её установил, получает одну и ту
же лестницу независимо от того, какой рендерер сработает.

Ничего не пишется обратно в fault: это значение, которое две горутины могут
рендерить одновременно.

## Классификация происходит один раз, здесь

```go
port.KindOf(err)              errs.Kind
port.KindOfWith(err, codes)   errs.Kind, через ваш словарь
port.CodeForKind(k)           errs.Code
port.FaultOf(err)             *errs.Fault
```

`port/porthttp` превращает `Kind` в статус, а `crud/rpc/crudgrpc` — в `codes.Code`
— **одна классификация, разложенная по транспортам**, а не по одной на
каждый фреймворк ([[UC-015]]).

## Общие хелперы запроса

Ими пользуется каждый биндинг, и они экспортированы потому, что
рукописному эндпоинту нужны те же правила:

| | |
|---|---|
| `Sanitize(meta, *m, allowClientID)` | очистить то, что клиент не вправе выбрать при создании |
| `ClearGenerated(meta, *m)` | очистить каждую колонку `generated` |
| `CoerceID[ID](raw)` | параметр пути становится типом ключа |
| `NarrowForEntity(req)` / `NarrowForCount(req)` | отбросить то, что запрос к одной сущности или count-запрос не вправе запрашивать |
| `BadRequest(err)` · `BadRequestf` · `BadRequestAs(code, path, …)` | собрать 400 с указанным путём |
| `CoversUpdate[M, U]()` / `MustCoverUpdate[M, U]()` | DTO всё ещё покрывает каждую писабельную колонку |
| `FirstLanguageTag(list)` | первый тег из списка в форме `Accept-Language` |

## См. также

- [porthttp](porthttp.md) — HTTP-проекция контракта ошибок и таблица статусов
- [crudhttp](crudhttp.md) — то, что одновременно HTTP и CRUD
- [crudnet](crudnet.md) · [crudfiber](crudfiber.md) · [crudgin](crudgin.md) · [crudgrpc](crudgrpc.md)
- [cmd/vv](vv-cli.md) — генерирует маппер, карту путей и оболочку сервиса
- [[UC-013]] бизнес-правила между хендлером и репозиторием · [[FL-015]] запрос через слой port
- [[D-045]] общая половина транспортно-нейтральна · [[D-050]] сгенерированный адаптер полон
