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
колонка `Path` у целевой модели затеняет метод — сгенерированный файл говорит
об этом в doc-комментарии группы, а `RelPath()` — форма, которую ничто не
затеняет.

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

Первичный ключ, явно помеченный `auto`, намеренно отсутствует в
`ArticleInput` и `ArticlePaths`: им владеет база, а create-путь всё равно его
очищает. Назначаемый клиентом UUID, slug или другой non-auto ключ остаётся в
обоих. Сгенерированное исключение `MustPathMap` явно фиксирует различие и
сохраняет точной стартовую проверку покрытия. Codegen пока не зеркалит runtime
implicit integer-key default и `noauto`; database-owned ключ нужно пометить явно.

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
- [[UC-014]] держать сгенерированные артефакты в синхронизации · [[FL-010]] от модели к DTO и метамодели
- [[D-018]] DTO и метамодели генерируются · [[D-050]] сгенерированный адаптер тотален
