# errs — контракт ошибок

```go
import "github.com/frostgrove/vv/errs"
```

**Модуль:** корневой, получит собственную версионную строку при первом теге
· **Зависит от:** только стандартной библиотеки, и больше ничего — даже от `crud`
· **Манифест контракта:** да ([[D-048]])

Что такое проваленная операция *по своей сути*, в виде набора типов-значений и
пяти интерфейсов. Это JPA-половина разделения: интерфейсы и соглашения,
которые может реализовать что угодно, включая сервис без базы данных вообще.

**Импортируйте, когда** ваш собственный слой сервиса производит ошибки
валидации, когда вы объявляете собственные коды, или когда вы пишете каталог
сообщений. Всё в библиотеке ниже транспорта уже говорит на этом языке.

---

## Проблема, которую это устраняет

Клиент отправляет `POST` с формой, где занят email, указана несуществующая
организация и указан несовершеннолетний пользователь. Без этого — три
обращения: база данных останавливается на первом же ограничении, до которого
дошла, и ответ повторяет драйвер:

```json
{"error":"conflict","message":"conflict: ERROR: duplicate key value violates unique constraint \"users_email_key\" (SQLSTATE 23505)"}
```

С этим — один ответ:

```json
{
  "type": "error",
  "errors": {
    "validation": [
      { "field": ["user", "email"],  "error_code": "unique",      "message": "user with this email already exists" },
      { "field": ["user", "org_id"], "error_code": "foreign_key", "message": "the organisation does not exist" },
      { "field": ["user", "age"],    "error_code": "check",       "message": "age must be at least 18" }
    ]
  }
}
```

Никакого имени ограничения, имени таблицы, имени столбца, SQLSTATE, префикса
драйвера ([[D-044]]). `field` — это путь, который отправил **клиент**,
`error_code` стабилен и машиночитаем, `message` пришло из каталога ([[UC-017]]).

---

## Начнём отсюда — регистрация, которая проваливается

Самый частый случай: кто-то регистрируется с email, который уже занят. Вот всё
это, от начала до конца.

### Модель

Таблица `users` с уникальным индексом на `email`.

```go
type User struct {
    ID    int64  `db:"id,pk,auto" json:"id"`
    Email string `db:"email"      json:"email"`
    Name  string `db:"name"       json:"name"`
}

var Users = sqlrepo.Define[User, int64, UserUpdate]("users")
```

### Подключение — две строки, которые вы бы иначе не написали

```go
// 1. Указать, какой движок отвечает, чтобы отказ нёс код.
db := crudsql.Postgres(sqlDB, crudsql.WithFaults(sqlfault.New("postgres")))

// 2. Дать репозиторию назвать поле, на котором произошло нарушение.
users := Users.Bind(db, faults.Enrich[User, int64]())

mux := http.NewServeMux()
crudnet.New(users).Mount(mux, "/users")
http.ListenAndServe(":8080", crudnet.Errors()(mux))
```

Это вся настройка целиком. Ни каталога, ни пробы, ни собственного словаря —
они приходят позже, и каждый из них опционален.

### Что получает клиент

```http
POST /users
{"email": "ann@x.io", "name": "Ann"}
```

```http
409 Conflict

{"type":"error","errors":{"validation":[
  {"field":["email"],"error_code":"unique","message":"this value is already taken"}
]}}
```

Три вещи, на которые стоит обратить внимание, — в них вся суть:

- **`error_code` равен `unique`** — стабилен, машиночитаем, и клиент может
  ветвиться по нему, не разбирая предложение.
- **`field` равен `["email"]`**, в нижнем регистре — это ключ, *который отправил
  клиент*, а не Go-поле `Email` и не ограничение `users_email_key`.
- **`message` — значение по умолчанию** из стандартного словаря. Замените его
  собственным текстом когда угодно; см. [Сообщения](#сообщения).

Статус 409, потому что `unique` отображается в `KindConflict`. Он оказывается
в `validation`, а не в `general`, потому что **называет поле** — так форма
может отметить нужное поле. Группа говорит о том, на что клиент может
среагировать, а не о том, откуда пришёл отказ.

### Что видит ваш Go-код

Та же ошибка, из `users.Save(ctx, &u)`:

```go
_, err := users.Save(ctx, &u)

// Ветка, которая у вас уже была, продолжает работать — это дополнение ([[D-038]]).
if errors.Is(err, crud.ErrConflict) { … }

// А теперь под ней есть ещё.
if f, ok := errs.AsFault(err); ok {
    f.Kind                    // errs.KindConflict
    f.Violations[0].Code      // errs.CodeUnique
    f.Violations[0].Path      // ["Email"] — поле модели
    f.Violations[0].Origin    // errs.OriginState — столкнулось с уже сохранённой строкой
}
```

> **Почему путь здесь `Email`, а на проводе — `email`.** Каждый слой переводит
> один переход, и только свой ([[D-043]]). Репозиторий знает модель, поэтому
> отвечает `Email`; транспорт знает, что отправил клиент, и завершает работу.
> Для простого случая вы не пишете ни тот, ни другой переход.

### Ваши собственные правила производят то же самое

Отказ на уровне сервиса — не ошибка второго сорта: он попадает в тот же
список, с той же формой, и рендерится через тот же конверт:

```go
func (s userService) Create(ctx context.Context, cmd port.CreateCommand[User]) (User, error) {
    if !strings.Contains(cmd.Model.Email, "@") {
        return User{}, errs.Validation().
            Field("Email").Code(errs.CodeInvalidFormat).
            Fault()
    }
    return s.DefaultService.Create(ctx, cmd)
}
```

```http
422 Unprocessable Entity

{"type":"error","errors":{"validation":[
  {"field":["email"],"error_code":"invalid_format","message":"this value is not in the expected format"}
]}}
```

422, а не 409, потому что `invalid_format` отображается в `KindValidation` —
payload неверен сам по себе, а не сталкивается с чем-то сохранённым.

### Получить *все* проблемы разом

Всё написанное выше сообщает **одно** нарушение, потому что так устроена база
данных: первое же достигнутое ограничение обрывает выполнение запроса. Чтобы
получить остальные, добавьте [каталог](catalog.md) и [пробу](probe.md) — ещё
две строки, при старте приложения:

```go
cat, err := catalog.Load(ctx, db)          // прочитать схему один раз
if err != nil { log.Fatal(err) }

users := Users.Bind(db, faults.Enrich[User, int64](
    faults.WithProbe(probe.Full(cat)),     // найти остальные
))
```

Теперь тот же запрос, который нарушил три ограничения, отвечает всеми тремя —
это и есть ответ в начале страницы.

### Где что лежит

| Вам нужно | Добавьте | Страница |
|---|---|---|
| Код на отклонённой записи | `crudsql.WithFaults(sqlfault.New(engine))` | [sqlfault](sqlfault.md) |
| Поле, на котором это произошло | `faults.Enrich[M, ID]()` | [faults](faults.md) |
| Каждое нарушение, а не только первое | `probe.Full(cat)` + `catalog.Load` | [probe](probe.md) · [catalog](catalog.md) |
| Собственные коды и статусы | `errs.Codes` | [ниже](#словарь) |
| Формулировки по локали | `errs.LoadMessages` | [ниже](#сообщения) |
| Ключ клиента вместо ключа модели | `cmd/vv -adapter` | [cmd/vv](vv-cli.md) |

**По умолчанию ничего этого не включено.** Не подключите ничего — 409 всё
равно останется 409, просто без `error_code` и без `field`.

---

## Пять типов-значений

### `Code` — то, по чему ветвится клиент

Стабильная строка. Константы — это стандартный набор; **здесь ничего не
закрыто** — объявляйте собственные того же типа.

| Группа | Коды |
|---|---|
| Целостность | `unique` `not_unique` `foreign_key` `restrict` `required` `check` `exclusion` |
| Данные | `too_long` `out_of_range` `invalid_format` `invalid_enum` |
| Конкурентность | `stale_version` `deadlock` `serialization_failure` `lock_timeout` `transaction_aborted` |
| Запрос | `malformed_body` `invalid_id` `unknown_field` `bad_query` `too_large` |
| Грубая классификация | `conflict` `not_found` `forbidden` `unauthenticated` `unavailable` `internal` |

Код **никогда не выводится из того, что сказал драйвер** — код, построенный из
исходного текста CHECK-выражения, уносит с собой имена столбцов.

### `Kind` — класс транспорта

Девять значений, и транспорт отображает **kind, но никогда не code**. Именно
это позволяет сервису объявить пятьдесят собственных кодов, не трогая таблицу
статусов ([[D-049]]).

| Kind | HTTP | gRPC |
|---|---|---|
| `KindInternal` | 500 | `Internal` |
| `KindNotFound` | 404 | `NotFound` |
| `KindUnauthorized` | 401 | `Unauthenticated` |
| `KindForbidden` | 403 | `PermissionDenied` |
| `KindRetryable` | 503 | `Unavailable` |
| `KindConflict` | 409 | `AlreadyExists` |
| `KindValidation` | 422 | `InvalidArgument` |
| `KindBadRequest` | 400 | `InvalidArgument` |
| `KindTooLarge` | 413 | `ResourceExhausted` |

`KindInternal` — **нулевое значение намеренно**: kind, потерявший смысл,
говорит 500, а не притворяется 4xx, который не может обосновать.

### `Path` — где это произошло, словами клиента

```go
errs.Path{errs.Named("user"), errs.Named("email")}   // ["user","email"]
errs.ParsePath("items[3].email")                     // ["items",3,"email"]
```

Три представления, одно значение: `MarshalJSON` для конверта, `String()` для
лога, `Pointer()` для указателя RFC 6901.

Имена и позиции, никогда столбцы. Перевод из одного в другое — задача слоя,
который выполнил это отображение, **по одному переходу на слой** ([[D-043]]).

### `Violation` — одна конкретная неполадка

```go
type Violation struct {
    Path        Path
    Code        Code
    Origin      Origin       // OriginInput или OriginState
    Message     string
    Params      map[string]any  // заполняет шаблон; остаётся на сервере
    Source      Source          // происхождение из хранилища; внутреннее, не рендерится
    Approximate bool            // переход не удалось разрешить, и он не был выдуман
}
```

Ограничение, которое отказалась нарушить база данных, и правило, которое
отказался пропустить валидатор, — это **один и тот же тип в одном и том же
списке**, различаемый по `Origin`. Их объединение — вся суть: payload с
некорректным email *и* с занятым email — это два нарушения в одном пути, и
клиент, делающий два обращения, чтобы это узнать, — та самая проблема, которую
это устраняет.

`Origin` определяет три вещи: статус (входное правило — это 422, коллизия с
хранимым состоянием — 409), может ли спорное значение вообще быть возвращено
клиенту (только `OriginState` раскрывает то, чего вызывающий уже не мог
видеть), и запускается ли проба (payload, уже известный как плохой, не
пробуется).

### `Fault` — классифицированный сбой со всеми нарушениями под ним

```go
type Fault struct {
    Kind       Kind
    Code       Code
    Message    string   // для разработчика — но см. ниже
    Violations []Violation
    Op         string   // глагол репозитория: "Save", "Update"
    Entity     string
    Partial    bool     // достигнут предел; набор неполный
    Detail     Detail   // диалект, SQLSTATE, ограничение, таблица — никогда не рендерится
}
```

**`Message` — для разработчика, и есть ровно один путь, по которому его читает
клиент.** У fault'а без нарушений — 404, голый 403, 401 — одно нарушение
синтезируется, и оно берёт сообщение fault'а, если раньше не ответит каталог по
коду. То есть сообщение, написанное для лога, — это сообщение, которое кому-то
могут показать: держите его общим или оставьте пустым и дайте ответить словарю
([[D-056]]). У `Detail` такого пути нет, он не рендерится никогда.

**Он оборачивает и никогда не заменяет** ([[D-038]]). Вызывающий, написавший
`errors.Is(err, crud.ErrConflict)` ещё до того, как всё это появилось,
сохраняет эту ветку, а вызывающий, которому нужен список, достаёт его через
`errors.As` — оба на одном и том же значении, через сколько бы ещё оболочек
ни добавил слой сервиса.

```go
if f, ok := errs.AsFault(err); ok {
    for _, v := range f.Violations {
        log.Printf("%s at %s", v.Code, v.Path)
    }
}
```

**Поля `Retryable` нет.** `KindRetryable` уже говорит это, а второе написание
сделало бы представимым то самое состояние, которое запрещает [[D-040]]:
конфликт, который утверждает, что он повторяем, при отсутствии правила, какому
из них верить транспорту.

---

## Сборка вручную

Слой сервиса — полноправный производитель нарушений, а не запоздалая мысль.

```go
return errs.Validation().
    Field("Age").Code("too_young").Params(errs.P{"min": 18}).
    Field("Email").Code(errs.CodeInvalidFormat).
    Entity("User").Op("Save").
    Wrapping(crud.ErrConflict).
    Fault()
```

Десять точек входа: `New(kind)`, `Validation()`, `BadRequest()`, `Conflict()`,
`NotFound()`, `Forbidden()`, `Unauthorized()`, `Retryable()`, `TooLarge()`,
`Internal()`.

Шаги: `Field(name)` · `At(path)` · `General()` · `Code(c)` · `Message(s)` ·
`Params(p)` · `Origin(o)` · `Source(s)` · `Approximate(b)` · `Detail(d)` ·
`Op(s)` · `Entity(s)` · `Partial(b)` · `Wrapping(errs...)` · `Fault()`

**Цепочку разрешает одно правило.** `Code`, `Params` и `Message` относятся к
нарушению, открытому последним вызовом `Field`, `At` или `General`; пока не
открыто ни одно нарушение, `Code` и `Message` относятся к самому `Fault`.

`Field` называет поле **модели**. Превращение его в `["user","age"]` на выходе
— задача того слоя, который выполнил это отображение, и никакого другого.

---

## Словарь

`Codes` — это **значение**, а не таблица уровня пакета. Две библиотеки в одном
бинарнике могут независимо объявить `too_long`, и с глобальным реестром та,
что была слинкована первой, решала бы статус за вторую.

```go
codes := errs.StandardCodes()
codes.Add("too_young", errs.KindValidation, "must be at least {min}")
codes.Add("quota_exceeded", errs.KindForbidden, "your plan does not allow this")
```

Передайте его туда, где он нужен:

```go
crudnet.Errors(crudhttp.WithCodes(codes))
sqlfault.New("postgres", sqlfault.WithCodes(codes))
```

`Add` возвращает `errs.ErrCodeRedeclared`, если код уже объявлен с другим
kind. И нулевое значение, и `nil *Codes` читаются как пустые, а не паникуют.

---

## Сообщения

### Каталог

Один плоский JSON-файл на локаль. Не пакет, не запись в манифесте — просто
файлы.

```
messages/
  default.json
  ru.json
  de.json
```

```json
{
  "unique": "this value is already taken",
  "user.email.unique": "somebody already signed up with that address",
  "email.unique": "that email address is taken",
  "too_long": "at most {max} characters"
}
```

```go
//go:embed messages
var messages embed.FS

cat, err := errs.LoadMessages(errs.StandardCodes(), messages, "messages")
```

### Лестница поиска

Для нарушения по пути `["user","email"]` с кодом `unique`:

```
user.email.unique  →  user.unique  →  email.unique  →  unique  →  значение по умолчанию для кода
```

Скроено по образцу Spring-овского `MessageSource`: переопределение может быть
настолько узким или широким, насколько нужно его автору, без схемы конфигурации
для изучения.

**В деле участвуют только первый и последний именованные шаги**, так что
лестница всегда в четыре ступени, каким бы ни был путь. Нарушение по пути
`["order","items","email"]` читается как
`order.email.unique → order.unique → email.unique → unique`, а ключ,
записывающий весь путь целиком, никогда не рассматривается.

`Messages.Load(fsys, dir)` добавляет локаль во время выполнения. `Locales()`
их перечисляет. `Missing(locale)` сообщает, какие объявленные коды эта локаль
не покрывает — подключите это к тесту, и наполовину переведённый каталог
завалит сборку.

Словарь — это то, до чего лестница в итоге проваливается, так что
**неполный каталог — это предусмотренный случай**, а не сломанный.

---

## SPI — пять интерфейсов, которые реализует третья сторона

| Интерфейс | Один метод | Реализуется |
|---|---|---|
| `Classifier` | `Classify(error) (*Fault, bool)` | [sqlfault](sqlfault.md), или вашим ORM-адаптером |
| `Resolver` | `Resolve(Path) (Path, bool)` | сгенерированным `<Model>Mapper`, `port.Fields`, индексом тела запроса |
| `MessageSource` | `Message(ctx, Violation, locale) (string, bool)` | `errs.Messages`, или вашей библиотекой i18n |
| `CodeMapper` | `CodeFor(*Fault, Violation) (Code, bool)` | сервисом, которому нужен `email_taken` там, где классификатор сказал `unique` |
| `FieldViolation` | `Namespace/Tag/Param/Value` | **go-playground/validator, структурно** |

`errs.Chain(resolvers...)` применяет их по порядку. **Если любой переход
отказывается, путь возвращается таким, каким успел преобразоваться, а
результат — false** — вызывающий сохраняет частичный перевод и помечает
нарушение как `Approximate`, вместо того чтобы отправить догадку.

### Мост валидации, без цены в виде зависимости

`validator.FieldError` удовлетворяет `errs.FieldViolation` **структурно**, так
что ни один из пакетов не импортирует другой:

```go
if verrs, ok := err.(validator.ValidationErrors); ok {
    vs := errs.FromFieldViolations("CreateUserRequest", verrs...)
    return errs.Validation().Fault()   // …неся в себе vs
}
```

`Tag()` становится `Code`, `Namespace()` становится `Path` — при этом
`Items[3].Email` разбирается прямо в шаг с индексом — а `Param()` и `Value()`
идут в `Params` для шаблона сообщения.

> **Зарегистрируйте функцию имён тегов validator**, иначе `Namespace()` будет
> сообщать имена полей Go, и каждый путь будет тихо неверным. Это шаг старта
> приложения, а не сюрприз во время выполнения.
> `TestWithoutTheTagNameFuncEveryPathIsGoFieldNames` в `test/bridge/`
> фиксирует ровно то, что вы получите, если забудете.

---

## Сортировка

`errs.SortViolations(vs)` приводит список к одному стабильному порядку, так
что `Fault` и отрендеренное тело согласуются, а ответ побайтово идентичен от
запуска к запуску.

## См. также

- [sqlerr](sqlerr.md) — как ошибка драйвера становится `Code`
- [sqlfault](sqlfault.md) — `Classifier`, который собирает `Fault`
- [probe](probe.md) — как находятся *остальные* нарушения
- [crudhttp](crudhttp.md) — конверт и таблица статусов
- [[UC-017]] все ошибки для одного payload сразу · [[UC-015]] отобразить сбой на транспорт
- [[D-038]] fault аддитивен · [[D-043]] один переход на слой · [[D-044]] payload не называет ничего внутреннего
