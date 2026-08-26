# crudgrpc — полноценный CRUD API на gRPC

```go
import "github.com/frostgrove/vv/crud/rpc/crudgrpc"
```

```bash
go get github.com/frostgrove/vv/crud/rpc/crudgrpc
```

**Модуль:** отдельный — чтобы потребитель на HTTP никогда не тянул gRPC,
protobuf и genproto в зависимости ([[D-033]], [[D-051]])

Восемь методов, по одному на команду `port`, поверх документов
`google.protobuf.Struct`. **Писать `.proto` и ставить `protoc` не нужно.** То же
самое значение сервиса, что монтируется на Fiber, Gin и `net/http`, монтируется
и здесь — и отвечает то же самое.

---

## Монтирование

```go
articles := specs.Executor(Articles.Bind(db, security.Gate(policy)))

srv := grpc.NewServer(grpc.UnaryInterceptor(crudgrpc.Errors()))
crudgrpc.New(articles).Register(srv, "Article")

srv.Serve(lis)
```

Это монтирует `vv.crud.v1.Article`. Имя, уже несущее пакет, используется как
есть — можно класть ресурсы в собственный пакет.

## Восемь методов

| Метод | Запрос | Ответ |
|---|---|---|
| `List` | документ запроса | страница |
| `Count` | документ запроса | `{"count": n}` |
| `Get` | `{"id": "42", "query": {…}}` | сущность |
| `Create` | документ сущности | сущность |
| `Update` | `{"id": "42", "patch": {…}}` | сущность |
| `Replace` | `{"id": "42", "entity": {…}}` | сущность |
| `Delete` | `{"id": "42"}` | `{"deleted": n}` |
| `BulkDelete` | `{"ids": ["1","2"]}` | `{"deleted": n}` |

Восемь против десяти у HTTP. Пропадают две — сдвоенные двери: у HTTP есть
`GET /` **и** `POST /query` для списка, потому что строка запроса и JSON-тело —
два разных входа. У gRPC одно сообщение-запрос, поэтому и метод один.

`ReadOnly()` регистрирует три чтения и оставляет пять записей
незарегистрированными — gRPC сам отвечает на них `Unimplemented`.

## Четыре конструктора

```go
crudgrpc.New(repo, opts...)                  // репозиторий; модель и есть wire-формат
crudgrpc.NewFor(repo, mapper, opts...)       // …со своим документом запроса
crudgrpc.Serving(svc, opts...)               // port.Service — ваша бизнес-логика
crudgrpc.ServingFor(svc, mapper, opts...)    // и то, и другое
```

`crudgrpc.Repository` и `crudgrpc.Service` — это **алиасы** типов `port`, так
что одно значение обслуживает все четыре транспорта:

```go
svc := articleService{port.NewService(articles)}

crudfiber.Serving(svc).Routes()
crudgin.Serving(svc).Mount(r, "/articles")
crudnet.Serving(svc).Mount(mux, "/articles")
crudgrpc.Serving(svc).Register(srv, "Article")
```

## Опции

Те же имена, что и у остальных биндингов, только вместо контекста фреймворка —
`context.Context`.

| Опция | Что делает |
|---|---|
| `WithQuery(cfg)` | ограничивает DSL — [`query.Config`](query.md#bounding-it) |
| `WithScope(fn)` | `func(context.Context) ([]crud.Option, error)` |
| `WithTransform(fn)` | презентер: `func(context.Context, M) any` |
| `BeforeSave(fn)` | `func(context.Context, *M) error` |
| `BeforeUpdate(fn)` | `func(context.Context, ID, *U) error` |
| `ReadOnly()` | регистрирует только три чтения |
| `AllowClientID()` | разрешить create'у самому задать ключ, генерируемый базой |
| `MaxBulk(n)` | ограничить `BulkDelete` — по умолчанию `port.DefaultMaxBulk` (1024), «без предела» не бывает |
| `WithRenderer(r)` | заменить рендерер статуса |

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

**`WithErrorHandler` здесь нет.** Ответ gRPC — это возвращаемое значение, а не
поток, который обработчик мог бы наполовину записать, так что перехватывать
там нечего: `WithRenderer` — это весь шов целиком.

## Ошибки

```go
srv := grpc.NewServer(grpc.UnaryInterceptor(crudgrpc.Errors(
    crudgrpc.WithMessages(catalogue),
    crudgrpc.WithCodes(codes),
)))
```

| Kind | Код |
|---|---|
| `KindNotFound` | `NotFound` |
| `KindUnauthorized` | `Unauthenticated` |
| `KindForbidden` | `PermissionDenied` |
| `KindRetryable` | `Unavailable`, с `RetryInfo{1s}` |
| `KindConflict` | `AlreadyExists` |
| `KindValidation` · `KindBadRequest` | `InvalidArgument` |
| `KindTooLarge` | `ResourceExhausted` |
| всё остальное | `Internal` |

Отказ приходит как код статуса плюс **детали `BadRequest` / `ErrorInfo` /
`RetryInfo`**, а не JSON-конверт. Машинные коды в этих деталях пишутся так же,
как HTTP `error_code`, так что клиенту нужна одна таблица ([[D-052]]).

`crudgrpc.Code(err)` экспортирован — на случай, если вы сами отвечаете на
собственные вызовы. `crudgrpc.CodeFor(kind)` — сама таблица.

**Два кода схлопываются, и это плата, а не баг.** `KindValidation` и
`KindBadRequest` оба отвечают `InvalidArgument`, а любой конфликт отвечает
`AlreadyExists` — включая `restrict` и `stale_version`. Уточнение по каждому
*коду* было бы второй таблицей, ключом которой служило бы то, что [[D-049]]
запрещает использовать для решения об ответе.

**Сообщение статуса никогда не равно `err.Error()`.** Собственный текст сбоя
называет сущность, а имя таблицы в сообщении статуса — как раз то раскрытие,
которое закрывает [[D-044]]. Используется ответ лестницы сообщений. Статус
`Internal` говорит `internal` и не несёт никаких деталей вовсе.

Локаль берётся из метаданных: `grpc-accept-language`, `accept-language` или
`x-locale`. `crudgrpc.WithLocale(ctx, l)` задаёт её напрямую.

Установка `Errors` дважды рендерит один раз — маркер это уже несущая статус
ошибка, так что интерцептор никогда не переопределяет метод, ответивший сам за
себя.

---

## Вызов другого сервиса

С этой стороны сгенерированной заглушки тоже нет. Каждый метод — это один
`google.protobuf.Struct` на входе и один на выходе, так что вызов — это
`grpc.Invoke` с документом внутри: то самое свойство, ради которого выбрана
форма Struct ([[D-052]]), прочитанное с другой стороны.

```go
articles := remote.New[Article, int64, ArticleInput](
    crudgrpc.Transport(conn, "Article"))
```

`name` — это то, что дальняя сторона передала в `Register`, и `ServiceName`
превращает его в одно и то же полное имя сервиса на обоих концах одной и той же
функцией.

| Опция | Что делает |
|---|---|
| `WithVocabulary(*errs.Codes)` | словарь кодов, через который уточняется класс — то же значение, что получил рендерер дальней стороны |
| `WithCallOptions(...grpc.CallOption)` | учётные данные на вызов, компрессор, лимит размера |

`KindForCode` — это `CodeFor`, прочитанная наоборот, и именно здесь отменяется
единственное схлопывание этого транспорта: `InvalidArgument` — это и 422, и 400,
поэтому класс возвращается грубым, а `ErrorInfo.Reason` его уточняет. Вызывающая
сторона получает `errs.KindValidation` или `errs.KindBadRequest`, различённые по
коду.

Статус, который написала не эта библиотека — `Unimplemented` от метода, который
[`ReadOnly`](#опции)-сервис не регистрировал, что угодно от перехватчика по
дороге, — приходит как `*remote.ProtocolError`, а не как класс. См.
[remote](remote.md) и [[FL-018]].

## Четыре ограничения, названные заранее, а не обнаруженные постфактум

**Схемы для ресурса не существует.** Репозиторий обобщён по своей модели,
поэтому скомпилированное proto-сообщение для него не может существовать в
библиотеке. Каждый запрос и ответ — это `google.protobuf.Struct`, несущий тот
же JSON-документ, на котором говорят HTTP-биндинги, — именно это и позволяет
одному значению сервиса обслуживать все четыре транспорта ([[D-052]]).

**Server reflection не может описать сервис.** У обобщённого ресурса нет file
descriptor, поэтому grpcurl и его собратья не могут перечислить методы. Клиенты
вызывают по полному имени метода, либо приложение регистрирует дескриптор,
сгенерированный им самим.

**Число в Struct — это double.** У `google.protobuf.Value` нет целого типа,
поэтому `int64` выше 2⁵³ теряет точность *в документе сущности*. **Ключи —
нет**: запрос несёт `id` строкой, и `port.CoerceID` конвертирует её так же, как
и параметр пути HTTP. Модели, которой нужны точные большие ключи ещё и в
сущности, объявляют `json:"id,string"`.

**Резервного варианта из сырого тела нет.** HTTP-биндинги хранят
декодированные байты, чтобы нарушение по колонке, которую ничто не объявляло,
всё равно могло назвать ключ, присланный клиентом. Здесь объявленные переходы —
сервиса и мэппера — это вся цепочка целиком, и путь, которым ничто не владеет,
помечается как приблизительный, а не угадывается ([[D-043]]).

## `crud.Opt` переживает провод

Конвертация идёт через `protojson` и `encoding/json`, так что теги `json`
самой модели определяют документ, а `crud.Opt` сохраняет свои три состояния:
отсутствующий ключ **не попадает в `Struct.Fields`**, явный null — это запись
`NullValue` ([[UC-003]]).

## Константы

```go
crudgrpc.ServicePrefix      // "vv.crud.v1."
crudgrpc.ErrorDomain        // "vv" — домен ErrorInfo
crudgrpc.PartialKey         // "partial" — ключ метаданных ErrorInfo
crudgrpc.DefaultRetryDelay  // time.Second
crudgrpc.LocaleKeys         // ключи метаданных, из которых читается локаль
crudgrpc.MaxViolations      // 100
```

## См. также

- [crudnet](crudnet.md) · [crudfiber](crudfiber.md) · [crudgin](crudgin.md) — тот же API на HTTP
- [port](port.md) — команды и конвейер нарушений, общий с ними
- [`_examples/pgx-grpc`](../../../_examples/pgx-grpc/)
- [[FL-013]] запрос через другой биндинг
- [[D-052]] ресурс gRPC несёт документы, а не схему · [[D-051]] сателлит несёт одно решение о зависимости
