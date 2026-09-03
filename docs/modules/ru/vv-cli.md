# cmd/vv — генератор

```bash
go run github.com/frostgrove/vv/cmd/vv
```

```go
//go:generate go run github.com/frostgrove/vv/cmd/vv
```

**Модуль:** root · **Зависит от:** `go/ast`, `go/parser`, `go/types`

DTO для обновления и метамодель — механические переложения вашей модели,
поэтому их пишет генератор. С `-adapter` он пишет и остальную часть ресурса:
тело запроса, mapper, **обратную сторону этого маппинга**, каркас сервиса
и его подключение.

Подкоманд пять, и они пишут разные артефакты:

| Команда | Пишет | Страница |
|---|---|---|
| `vv generate` (или без подкоманды) | `vv_gen.go` — update DTO, метамодель, каркас репозитория | эта |
| `vv generate resource` | `vv_wire_gen.go` и `resource.manifest.yml` — **публичные** тела create, patch и response | [ниже](#generate-resource) |
| `vv generate routes` | `vv_routes_gen.go` и `routes.manifest.yml` — операция, которую читают и маршрут, и его guard | [ниже](#generate-routes) |
| `vv generate module` | `vv_module_gen.go` и `module.manifest.yml` — модуль, который регистрирует композиционный корень | [ниже](#generate-module) |
| `vv generate cache` | `vv_cache_gen.go` и `cache.manifest.yml` — набор cache-скоупов | [cache](cache.md) |

Для одного пакета он читает экспортируемые структуры из `model.go`,
`*.model.go` и `*_model.go`, а затем пишет `vv_gen.go` рядом с ними. Обычным
полям Go не нужны теги `db`, GORM или другой ORM: применяются обычные правила
snake_case для колонок и множественного имени таблицы, а `ID`/`Id` — первичный
ключ. `db` и `rel` нужны только для исключений. Команда
`vv generate -dir ./src/app` обходит дерево и генерирует отдельный файл в
каждом пакете с моделями.

---

## Что получается на выходе

Из этого:

```go
type Article struct {
    ID          int64               `db:"id,pk,auto"`
    Title       string              `db:"title"`
    Rating      *float64            `db:"rating"`
    PublishedAt utils.Opt[time.Time] `db:"published_at"`
    TenantID    int64               `db:"tenant_id,immutable"`
    CreatedAt   time.Time           `db:"created_at,generated"`

    Author   *Author   `rel:"belongs_to"`
    Comments []Comment `rel:"has_many"`
}
```

**DTO для обновления** — указатели для опциональных колонок, `Opt` для
nullable, и ничего для ключа, immutable- и generated-колонок:

```go
type ArticleUpdate struct {
    Title       *string             `json:"title,omitempty"`
    Rating      utils.Opt[float64]   `json:"rating,omitzero"`
    PublishedAt utils.Opt[time.Time] `json:"publishedAt,omitzero"`
}
```

**Метамодель**, развёрнутая через отношения:

```go
var Article_ = specs.Metamodel[Article, ArticleAttrs]()

Article_.Views.Gte(100)                 // "views" >= $1
Article_.Author.Name.Eq("Ann")          // EXISTS (… authors … name = $1)
Article_.Comments.Approved.Eq(true)     // EXISTS (… comments … approved = $1)
Article_.Author.Name.Desc()             // ORDER BY (SELECT … LIMIT 1) DESC
```

Каждая из этих строк типизирована на этапе компиляции и проверяется против
схемы при инициализации пакета, поэтому **переименованная колонка ломает
сборку**, а не запрос. Развёртывание отношений останавливается на `-depth`
(по умолчанию 2) и никогда не возвращается в модель, уже стоящую на пути.

Каждое раскрытое отношение несёт ещё и собственный **путь** в виде хэндла,
поэтому настройки и опции, принимающие путь, а не предикат, тоже становятся
идентификаторами:

```go
Article_.Comments.Path()                // "Comments"
Article_.Comments.Author.Path()         // "Comments.Author"

sqlrepo.RelationScope(Article_.Comments.Path(), specs.Predicate(Comment_.Approved.Eq(true)))
crud.Preload(Article_.Comments.Path())
```

Хэндл помнит модель, на которую приземляется путь, поэтому хэндл, указывающий
на не ту модель, отклоняется при инициализации пакета. Он встроен, поэтому
колонка `Path` у целевой модели затеняет метод. Сгенерированный файл об этом
молчит, а `RelPath()` — форма, которую ничто не затеняет.

**И утверждение покрытия**, независимо от того, включён `-adapter` или нет:

```go
func init() {
    port.MustCoverUpdate[Article, ArticleUpdate]()
}
```

Добавьте колонку и забудьте перегенерировать — и пакет **откажется
запускаться**, назвав колонку ([[UC-014]]).

> Это чтение **скомпилированной структуры**, а не взгляда генератора на
> исходник — именно это и позволяет ему разойтись с закоммиченным файлом.
> Перегенерация с последующим diff'ом измеряет генератор только против него
> самого.

---

## Embedded base-модели

Полностью нетегированная value-embedded non-scalar структура разворачивается
точно так же, как в runtime metadata. Генератор разрешает local aliases,
инстанцированные generic base-типы и экспортные данные зависимостей, поэтому
обычному shared base-пакету регистрация не нужна. Для `gorm.Model` сохраняется
аудированная встроенная семантика. Anonymous scalar-структуры следуют точному
runtime method-set правилу: `time.Time`, driver-формы `Valuer`/`Scanner` и
text marshal/unmarshal-формы, включая scalar pointers, остаются одной колонкой.

Явный тег `db` или `rel` относится к самому anonymous-полю и запрещает
flattening. Struct-shaped поле с `rel` проходит обычные правила relation;
`rel:""` сохраняет присутствие тега и просит runtime вывести kind, в том числе
через local type aliases, а `rel:"-"` подавляет relation. Scalar с `rel`, кроме
`-`, отклоняется; scalar `rel:"-"` остаётся колонкой. `db:"-"` исключает
anonymous-поле целиком и остаётся low-level escape hatch. Нетегированный
pointer на non-scalar структуру отклоняется так же, как в runtime metadata.

Если Go type information не может разрешить anonymous-тип либо экспортная
колонка base имеет приватный named-тип или structural-тип с foreign unexported
field/method identity, который generated package не может воспроизвести,
генерация отказывает до записи и называет модель и тип. Разрешите dependency,
разверните поля или явно исключите embed целиком тегом `db:"-"`.
Если flattening создаёт duplicate effective Go field names или database
columns, генерация также отказывает до render.

Явное исключение — это low-level escape hatch: оно означает, что поля embed не
являются database-колонками. Оно не сохраняет колонки base, типы которых
генератору неизвестны.

---

## Флаги

| Флаг | По умолчанию | Что делает |
|---|---|---|
| `-dir` | `.` | директория пакета для чтения |
| `-out` | `vv_gen.go` | имя выходного файла |
| `-types` | tagged-структуры и exported-структуры в model-файлах | имена моделей через запятую |
| `-depth` | `2` | насколько далеко разворачивать пути отношений в метамодели |
| `-skip` | — | имена полей, полностью исключаемые, как `db:"-"` |
| `-readonly` | — | имена полей, не попадающих в DTO, но всё ещё доступных для фильтрации и сортировки, как `db:",immutable"` |
| `-into` | `-dir` | записать в другое место |
| `-import` | — | путь импорта `-dir`, чтобы типы модели были квалифицированы при записи в другое место |
| `-no-dto` | выкл | пропустить DTO для обновления |
| `-no-meta` | выкл | пропустить метамодели |
| `-no-repo` | выкл | пропустить blueprint репозитория и фабрику привязки |
| `-recursive` | выкл | обойти model-файлы ниже `-dir` и генерировать рядом с каждым пакетом; включён у `vv generate` |
| `-adapter` | **выкл** | сгенерировать ещё и адаптер ресурса |
| `-binding` | `net` | для какого транспорта пишется сгенерированное подключение: `net` или `none` |
| `-specs` | пакет specs | переопределение пути импорта |
| `-crud` | пакет crud | переопределение пути импорта |
| `-check` | выкл | отрендерить всё и сравнить с тем, что на диске, вместо записи; расходящийся или отсутствующий файл называется по пути, не пишется ничего |

`-check` — это то, что нужно в CI. Он падает, называя каждый пакет, чьи
артефакты отстали от моделей, а не только первый попавшийся при обходе: один
прогон даёт весь список.

Без `-types` exported-структуры в `model.go`, `*.model.go` и `*_model.go`
считаются моделями по соглашению; в остальных файлах структуру включает тег
`db`/`rel`.

`-import` — это путь, а не желаемый Go-идентификатор. Генератор читает
package declaration в `-dir` как preferred alias, поэтому
`-import example.com/acme/models/v2` с `package models` обычно даёт
`models.User`, а не `v2.User`; reserved/colliding имя получает читаемый
path-derived alias. Renamed-импорты
типов колонок сохраняются. Коллизии получают детерминированные path-derived
имена: `/alpha/common` и `/beta/common` превращаются в `alphaCommon` и
`betaCommon`, без numeric collision fallback. Composite и generic типы
переносят все selector imports, а один путь выводится один раз, даже если его использует и
generated support code. Dot imports отклоняются. Если output остаётся в model
package, source import сам назван `ProductUpdate`, а генератор должен объявить
`ProductUpdate`, source import нужно переименовать: Go отклоняет такую
межфайловую коллизию, и генератор сообщает о ней до записи. При `-into`
package declarations и file import aliases в destination проверяются в обе
стороны. Участвуют только imports, сохранившиеся в final rendered file, поэтому
локальный selector вроде `out.ID` не возвращает неиспользуемый source import.

## `-adapter`: остальная часть ресурса

По умолчанию выключен, потому что включение переписывает wire-форму каждого
ресурса, переходящего на сгенерированное подключение.

```go
type ArticleInput struct{ … }                      // тело для создания/замены
type ArticleMapper struct{}                        // port.Mapper + errs.Resolver
var  ArticlePaths = port.MustPathMap[Article](port.PathMap{ … })
type ArticleService struct{ *port.DefaultService[Article, int64, ArticleUpdate] }
func MountArticle(mux *http.ServeMux, prefix string, svc, opts ...)
```

Целочисленный первичный ключ — явно помеченный `pk` или выбранный runtime через
lookup `ID` (поле или колонку), а затем fallback на колонку `id` — по умолчанию
принадлежит базе и намеренно отсутствует в `ArticleInput` и `ArticlePaths`. Для целочисленного ключа,
назначаемого клиентом, укажите `noauto`. Назначаемый клиентом UUID, slug или
другой нецелочисленный ключ остаётся в обоих; явный `auto` по-прежнему доступен
для нецелочисленных database-generated первичных ключей. Сгенерированное
исключение `MustPathMap` принимает то же решение и сохраняет точной стартовую
проверку покрытия.

**`ArticlePaths` — вот ради чего существует этот флаг.** Он отображает поле
модели обратно на ключ, отправленный клиентом, поэтому тело ошибки называет
`authorID`, а не `AuthorID` — и поскольку он генерируется *вместе* с
маппингом, который инвертирует, `MustPathMap` может настаивать, что покрывает
каждую колонку, которую несёт запрос, и отказаться стартовать, когда это
перестаёт быть так ([[D-050]]).

Написанная вручную инверсия оказывается неверной с первого же переименования
ключа кем-то, а симптом — неверное поле `field` в боевом теле ошибки.

`ArticleMapper` удовлетворяет `errs.Resolver`, поэтому `port.Hops` подхватывает
его сам, и **биндинг не меняется вообще**.

> Сгенерированное тело берёт свои JSON-имена из **имён полей Go**, а не из
> собственных тегов `json` модели. Это сделано намеренно — одно правило для
> обоих тел, поэтому одна обратная карта обслуживает ресурс — и это значит,
> что у сгенерированного ресурса своя wire-форма. Монтируйте через `New` и не
> генерируйте адаптер, если форма модели и есть желаемый API.

`-binding net` пишет подключение на `net/http`; `-binding none` его
опускает — для монтирования на Fiber или Gin через `ServingFor` самостоятельно.
Подключение для Fiber и Gin не генерируется, потому что сгенерированный файл
в этой библиотеке не может импортировать сателлитный модуль ([[D-033]]).

## generate resource

Публичные тела. `vv_gen.go` — это **persistence**-половина: `ArticleUpdate` — то,
что может записать `UPDATE`, включая колонки, которые пишет только ваш
собственный код. Это не обещание клиенту, а параметризация им публичного
PATCH-биндера делает его таковым ([[D-105]]).

```bash
go run github.com/frostgrove/vv/cmd/vv generate resource -dir ./src/mod
```

пишет `vv_wire_gen.go` рядом с каждым пакетом моделей:

```go
type ArticleInput struct{ … }                 // тело создания
type ArticleInputMapper struct{}              // port.Mapper[ArticleInput, Article]

type ArticlePatch struct{ … }                 // тело PATCH
type ArticlePatchMapper struct{}              // wire.PatchMapper[ArticlePatch, ArticleUpdate]

type ArticleResponse struct{ … }              // тело ответа
type ArticlePresenter struct{}                // wire.Presenter[Article, ArticleResponse]

func init() {
    wire.MustCoverCreate[Article, ArticleInput]("ID", "CreatedAt")
    wire.MustCoverPatch[ArticleUpdate, ArticlePatch]("TenantID")
    wire.MustCoverResponse[Article, ArticleResponse]()
}
```

Монтируйте их явным конструктором биндинга — имя одно и то же на всех четырёх
транспортах:

```go
crudfiber.ServingWire(svc, ArticleInputMapper{}, ArticlePatchMapper{}, ArticlePresenter{})
```

`New`, `NewFor`, `Serving` и `ServingFor` не изменились и по-прежнему принимают
модель и persistence-DTO. См. [wire](wire.md).

### Манифест

Каждое тело выводится **сужением** — самым широким безопасным набором, а не
самым широким возможным, — и результат пишется в `resource.manifest.yml` рядом с
пакетом, где он коммитится и проходит ревью:

```json
{
  "format": 1,
  "generated_by": "vv generate resource",
  "package": "blog",
  "resources": [
    {
      "model": "Article",
      "patch": {
        "narrowed": ["Rating", "Title"],
        "fields": ["Title"],
        "widened": [],
        "derivation_fingerprint": "3850a0…",
        "confirmed": false
      }
    }
  ]
}
```

| тело | стартует от | выбрасывает |
|---|---|---|
| create | набора полей создания — без связей, без `generated`, без блокировки, без ключа, которым владеет база | `secret` |
| patch | колонок, которые пишет `ArticleUpdate` | `secret` |
| response | каждой колонки | `secret` и всё, что убрал `-skip` |

- **Сужение не стоит ничего.** Удалите имя из `fields` — следующий прогон
  сгенерирует тело поменьше и объявит пропуск в проверке покрытия.
- **Расширение подписывается.** Верните имя, которое сужение исключило, — оно
  попадёт в `widened`, и генерация остановится с ошибкой, называющей
  `Article patch`, пока рядом не встанет `confirmed: true`. Манифест при этом
  всё равно записывается — чтобы было что править; Go-файл — нет.
- **Подтверждение не переживает свой вывод.** `derivation_fingerprint` берётся с
  суженного набора, поэтому модель, получившая или потерявшая колонку, спросит
  заново.
- **Невозможное поле — ошибка, а не вопрос.** Колонка `generated`, предложенная
  как поле patch, отвергается сразу: никакое подтверждение не сделает для неё
  маппер.

### Флаги

| Флаг | По умолчанию | Что делает |
|---|---|---|
| `-dir` | `.` | каталог пакета для чтения |
| `-out` | `vv_wire_gen.go` | имя генерируемого Go-файла |
| `-manifest` | `resource.manifest.yml` | имя файла манифеста |
| `-types` | структуры с тегами и exported-структуры в model-файлах | имена моделей через запятую |
| `-skip` | — | имена полей, которые не попадают никуда |
| `-readonly` | — | имена полей, которые не попадают в тело patch |
| `-into` | `-dir` | писать в другой каталог; требует `-import` и несовместим с `-recursive` |
| `-import` | — | путь импорта `-dir`, чтобы типы моделей квалифицировались при записи в другое место |
| `-recursive` | **вкл** | обойти model-файлы под `-dir` и сгенерировать рядом с каждым пакетом |
| `-check` | выкл | отрендерить и сравнить, ничего не записывая; проверяются оба артефакта |

При `-recursive` обход доходит до конца и только потом отказывает. Один запуск
называет каждый отставший пакет и каждое тело, всё ещё ждущее подтверждения, с
префиксом каталога, в котором оно лежит, — то же правило, что у генератора
моделей, поэтому дереву не нужен отдельный запуск на пакет.

Оба артефакта отвергаются, если файл с таким именем писал не этот генератор, так
что написанный руками `resource.manifest.yml` никогда не будет перезаписан.

## generate routes

Для юзкейса, который **не** является CRUD-ресурсом. CRUD-маршрут берёт своё
право из политики, которой закрыт его репозиторий ([[D-107]],
[crudhttp](crudhttp.md)). У операции — список мёртвых джоб, смена пароля —
таблицы нет: она закрывает себя сама внутри своего тела, а маршрут рядом
объявляет то же право второй раз, руками. Эта подкоманда генерирует значение,
которое читают оба ([[D-109]]).

```bash
go run github.com/frostgrove/vv/cmd/vv generate routes -dir ./src/mod
```

Из

```go
func (this *DeadJobsUseCase) List(ctx context.Context) (DeadJobsView, error) {
    if _, err := access.Require(ctx, PermJobsRead); err != nil {
        return DeadJobsView{}, err
    }
    …
}

func (this *Handler) Access() []authhttp.Endpoint {
    return []authhttp.Endpoint{
        authhttp.Requires(fiber.MethodGet, "/ops/jobs/dead", PermJobsRead),
    }
}
```

читается guard, с ним сопоставляется объявление, называющее то же право, и пара
пишется в `routes.manifest.yml`:

```json
{
  "format": 1,
  "generated_by": "vv generate routes",
  "package": "ops",
  "operations": [
    {
      "operation": "DeadJobsUseCase.List",
      "policy": ["PermJobsRead"],
      "method": "GET",
      "path": "/ops/jobs/dead",
      "source": "inferred-from-guard",
      "guard_fingerprint": "9f21c4…",
      "confirmed": false
    }
  ]
}
```

Пока рядом не стоит `confirmed: true`, `vv_routes_gen.go` — файл, который не
компилируется:

```go
var VVRouteSet vvRouteSet = "confirm every operation in routes.manifest.yml"
```

После подтверждения это носитель:

```go
var OperationDeadJobsUseCaseList = Operation{…}

func Operations() []Operation
func Declarations() []authhttp.Endpoint
```

который обе стороны читают и ни одна не пишет:

```go
func (this *Handler) Access() []authhttp.Endpoint { return Declarations() }

access.Require(ctx, OperationDeadJobsUseCaseList.Permissions()...)
```

Дальше запуск видит guard, привязанный к операции, пишет `source:
bound-to-operation` и больше не спрашивает: то, что подтверждал человек, теперь
проверяет компилятор.

- **Источник — guard.** Маршрут выводится из него, а не наоборот: документируется
  ровно то право, которое проверяется ([[D-073]]).
- **Объявление, за которым ничего не стоит, — ошибка.** Маршрут, объявляющий
  право, которого не проверяет ни один юзкейс пакета, роняет генерацию с именем
  маршрута и строкой. Это опасное направление: аудируемая поверхность, аудит
  которой сделан не по тому списку.
- **Подтверждение не переживает того, за что было дано.** `guard_fingerprint`
  покрывает операцию, политику guard'а и *выведенный* маршрут. Меняется право в
  guard'е — подтверждение снимается, сборка встаёт. Метод и путь, вписанные в
  манифест руками, в отпечаток не входят, поэтому дозаполнить маршрут, который
  генератор вывести не смог, можно в той же правке, что и подтвердить его.
- **Право, которое генератор не может разрешить, роняет прогон.** За политикой
  вида `perm.Read` должен стоять ровно один пакет. Алиас импорта связывается
  пофайлово, и если два файла пакета связывают `perm` с разными путями, в
  сгенерированном файле — у него один блок импортов — можно было бы взять только
  один из них, и операция была бы объявлена и проверена под правом, которого её
  guard не называл. Вместо выбора прогон падает и называет оба пути и файл,
  в котором написан каждый. Алиас, которым не написана ни одна политика, не
  трогают.
- **Генератор не импортирует пакет авторизации.** `-guard` — строка. Если права
  проверяет что-то кроме `auth/access`, назовите это.

### Флаги

| Флаг | По умолчанию | Что делает |
|---|---|---|
| `-dir` | `.` | каталог пакета |
| `-out` | `vv_routes_gen.go` | имя сгенерированного Go-файла |
| `-manifest` | `routes.manifest.yml` | имя файла манифеста |
| `-guard` | `github.com/frostgrove/vv/auth/access` | import path пакета, функцию которого зовёт юзкейс, чтобы проверить право |
| `-guard-func` | `Require` | имя функции внутри `-guard`; её аргументы после контекста — политика |
| `-declare` | `github.com/frostgrove/vv/auth/http/authhttp` | import path пакета, чей `Requires` объявляет маршрут |
| `-auth` | `github.com/frostgrove/vv/auth` | import path пакета, которому принадлежит `Permission` |
| `-recursive` | **включён** | обойти пакеты под `-dir`, которые зовут guard, и сгенерировать рядом с каждым |
| `-check` | выключен | отрендерить и сравнить, ничего не записывая; проверяются оба артефакта |

Обход собирает, а не останавливается: один запуск называет каждую операцию,
ждущую подтверждения, и — под `-check` — каждый отставший пакет, каждое с
префиксом своего каталога. Подтверждение старше расхождения: пока операция ждёт
человека, сравнивать нечего.

## generate module

Для композиционного корня. [[D-106]] дал ограниченному контексту значение —
`module.Definition` называет конструкторы, раскладывает их по пяти видам, а
`Profile` решает, какие из них запускает процесс. Здесь берётся сам список,
чтобы он не был двухсотстрочной простынёй `fx.Module`, которую никто не читает
([[D-110]]).

```bash
go run github.com/frostgrove/vv/cmd/vv generate module -dir ./src/mod/workspace
```

Читается всё дерево пакетов под `-dir`. **Вклад** — функция верхнего уровня,
первый результат которой — *именованный* тип: `*ContractRepo`,
`health.Contribution`, `translation.Store`. Функция, возвращающая `string`,
`[]Language`, `any` или только `error`, — это помощник, а не то, что строит
контейнер, и она не предлагается вовсе. В подпакетах она обязана быть
экспортируемой; в собственном пакете модуля достижима и неэкспортируемая, и она
тоже предлагается.

**Вид** читается по этому типу результата:

| Конструктор возвращает | Вид | Кто несёт |
|---|---|---|
| `health.Contribution` | `check` | каждая реплика |
| `runtime.Runner` | `worker` | профиль с ролью `worker` |
| `app.Seeder` | `seeder` | профиль с ролью `seeder` |
| `appfiber.Route` | `route` | профиль с ролью `api` |
| любой другой именованный | `provide` | каждая реплика |

Каждый становится строкой в `module.manifest.yml`:

```json
{
  "format": 1,
  "generated_by": "vv generate module",
  "package": "workspace",
  "module": "workspace",
  "order": 0,
  "contributions": [
    {
      "symbol": "contract.NewContractRepo",
      "kind": "provide",
      "source": "inferred-from-signature",
      "signature_fingerprint": "4c81aa…",
      "excluded": false,
      "confirmed": false
    }
  ]
}
```

Там вам доступны три ответа:

- **`confirmed: true`** — да, это входит в модуль и именно таким видом.
- **другой `kind`** — вывод был неверен. `source` строки становится
  `declared-in-manifest`, и генератор перестаёт его перевыводить.
- **`excluded: true`** — это не вклад. Исключённой строки не ждут и не
  генерируют, а само исключение переживает смену сигнатуры.

Пока хоть одна включённая строка не подтверждена, `vv_module_gen.go` — файл,
который не компилируется:

```go
var VVModule vvModule = "confirm every contribution in module.manifest.yml"
```

Подтверждённый — это определение:

```go
var VVModule = vvmodule.MustDefine(vvmodule.Spec{
    Name:  "workspace",
    Order: 0,
    Provide: []any{contract.NewContractRepo, …},
    Workers: []any{pipeline.NewDebtSweeper},
    Checks:  []any{converterCheck},
})
```

которое композиционный корень отдаёт в
`appfx.Option(workspace.VVModule, profile)` — см. [app](app.md) и [[FL-030]].

На уже существующем модуле первый проход — это в основном проход исключений:
чистая функция, возвращающая именованный доменный тип, снаружи выглядит ровно
как конструктор, и генератор скорее спросит про неё, чем промолчит про
конструктор, который никуда не подключили. Спрашивает он один раз на символ.

- **Конструктор, который никто не разместил, останавливает сборку.** Ради этого
  всё и сделано: новый экспортируемый конструктор появляется неподтверждённой
  строкой, а не фичей, которая молча никуда не подключена.
- **Подтверждение не переживает сигнатуру, ради которой было дано.**
  `signature_fingerprint` покрывает символ, выведенный вид и всю сигнатуру.
  Измените, что конструктор берёт или возвращает, — и спросят ровно эту строку,
  и только её.
- **Типы-маркеры — это строки.** Если health-вклад у вас свой, передайте
  `-check-type your/pkg.Contribution`; передайте `-`, чтобы не выводить этот вид
  ниоткуда. Генератор не импортирует ни один из них.

### Флаги

| Флаг | По умолчанию | Что делает |
|---|---|---|
| `-dir` | `.` | каталог модуля; читается всё его дерево пакетов |
| `-out` | `vv_module_gen.go` | имя сгенерированного Go-файла |
| `-manifest` | `module.manifest.yml` | имя файла манифеста |
| `-name` | имя каталога | имя модуля |
| `-order` | `0` | порядок модуля в каталоге |
| `-import` | из ближайшего `go.mod` | import path каталога `-dir`, чтобы назвать его подпакеты |
| `-module` | `github.com/frostgrove/vv/app/module` | import path пакета, которому принадлежит `Definition` |
| `-check-type` | `github.com/frostgrove/vv/health.Contribution` | тип результата, делающий конструктор health-проверкой |
| `-route-type` | `github.com/frostgrove/vv/app/http/appfiber.Route` | тип результата, делающий конструктор маршрутом |
| `-worker-type` | `github.com/frostgrove/vv/runtime.Runner` | тип результата, делающий конструктор воркером |
| `-seeder-type` | `github.com/frostgrove/vv/app.Seeder` | тип результата, делающий конструктор сидером |
| `-recursive` | выключен | считать модулем каждый пакет непосредственно под `-dir` |
| `-check` | выключен | отрендерить и сравнить, ничего не записывая; проверяются оба артефакта |

`-recursive` здесь на один уровень, а не обход: модуль — это *дерево* пакетов,
и рекурсия сделала бы модулем каждый подпакет. `vv generate module -dir
./src/mod -recursive -check` — строка CI для всего `src/mod`. Обход собирает, а
не останавливается: каждый вклад, ждущий человека, и — под `-check` — каждый
отставший модуль, каждое с префиксом своего каталога; подтверждение старше
расхождения.

## Сгенерированный репозиторий

Вместе с DTO и метамоделью по умолчанию выводятся независимый от драйвера
blueprint репозитория, короткий alias и фабрика, которая возвращает указатель:

```go
type ArticleRepo = crud.Repo[Article, int64, ArticleUpdate]

var ArticleRepository = sqlrepo.Define[Article, int64, ArticleUpdate]("")

func NewArticleRepository(src crud.Source) *ArticleRepo {
    return ArticleRepository.Bind(src)
}
```

В фабрику можно передать любой `crud.Source`: `crudsql.Postgres(sqlDB)` для
`database/sql` (включая общий пул GORM), `crudpgx.Open(pool)` для нативного pgx
или тестовый source. В Fx зависите от `*ArticleRepo`, а не от длинной записи
`crud.Repo[Article, int64, ArticleUpdate]`. Если пакету нужны только DTO и
метамодели, используйте `-no-repo`.

## Принятие модели, сгенерированной ORM

С `-types` названные структуры принимаются как модели **даже без тегов
`db`**, что и позволяет сгенерированным сущностям ent работать как есть.
Пишите результат в собственный пакет, а не в пакет ent, где имена
столкнутся:

```bash
go run github.com/frostgrove/vv/cmd/vv \
    -dir ./ent -types User,Article -skip CreatedAt \
    -import myapp/ent -into ./internal/store
```

Полный рецепт см. в [usage-guides/ent.md](../../usage-guides/ent.md) и
[usage-guides/gorm.md](../../usage-guides/gorm.md).

## Поддержание честности

`_examples/example/blog` — разобранный пример: `model.go` — то, что пишете
вы, `vv_gen.go` — то, что получается на выходе — с `-adapter`, чтобы были
видны обе половины — а тест перегенерирует и сравнивает через diff, так что
эти две части не могут разойтись.

```bash
make generate   # перегенерировать каждый DTO и метамодель во всём дереве
```

## См. также

- [specs](specs.md) — во что подключается метамодель
- [port](port.md) — `PathMap`, `Mapper` и `MustCoverUpdate`
- [wire](wire.md) — `PatchMapper`, `Presenter` и три проверки покрытия
- [[UC-014]] держать сгенерированные артефакты в синхронизации · [[FL-010]] от модели к DTO и метамодели · [[FL-029]] от модели к публичному wire-телу · [[FL-031]] от guard к объявленной операции · [[FL-032]] от дерева пакетов к подтверждённому модулю
- [[D-018]] DTO и метамодели генерируются · [[D-050]] сгенерированный адаптер тотален · [[D-105]] persistence-патч — не публичное тело · [[D-109]] маршрут выводится из своего guard · [[D-110]] модуль выводится из того, что строят его пакеты
