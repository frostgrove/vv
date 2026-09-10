# Frostgrove: magic-first DX без скрытой магии

**Статус:** proposal для решения maintainer-а; baseline сверено с кодом
2026-09-08, public contracts ещё не приняты и не реализованы. Фактический статус
работ ведётся в [живом roadmap](Roadmap.md). Здесь остаются только три
воспроизводимых разрыва, предлагаемые решения и критерии приёмки.

**Decision checkpoint:** направление M1 уже задано D-057. До реализации
maintainer отдельно принимает либо отклоняет public API M0, M1 migration и
core/HTTP contract M2; принятый результат фиксируется ADR и статусом live item.
Owner решения и закрытия milestones — maintainer репозитория.

Целевой путь:

```text
package tree → vv generate module → подтверждённый kind
             → один binding этого kind в composition root
             → явно подключённый production consumer
```

Magic оправдан только там, где он убирает повторное описание уже принятого
решения. Наличие аналога в другом framework, экономия одной строки и возможный
будущий provider сами по себе новой механики не требуют.

## Фильтр

Задача остаётся в этом плане, только если одновременно:

1. разрыв воспроизводится в текущем Frostgrove или его реальном consumer;
2. исправление принадлежит существующему контракту Frostgrove;
3. оно убирает больше дублирования или риска, чем добавляет понятий;
4. гарантия проверяется сквозным тестом, а не наличием API или примера.

## Проверенный baseline

| Область | Что происходит сейчас | Подтверждённый разрыв |
|---|---|---|
| Generated modules | [`renderModule`](../../internal/codegen/module.go) раскладывает raw constructors по `provide`, `route`, `worker`, `seeder`, `check`; [`appfx.Option`](../../app/appfx/module.go) передаёт выбранные значения в обычный `fx.Provide` | Kind выбирает profile, но не применяет обязательные [`AsRoute`](../../app/http/appfiber/appfiber.go), [`AsRunner`](../../runtime/runtimefx/runtimefx.go), [`AsSeeder`](../../app/appfx/appfx.go), [`AsCheck`](../../health/healthfx/healthfx.go) |
| SQL/Fx lifecycle | [`crudsqlfx.Open`](../../crud/adapter/crudsql/crudsqlfx/crudsqlfx.go) вызывает `vvdb.Open` во время `fx.New` и регистрирует только `OnStop` | Если более поздний constructor ломает `fx.New`, `Start` не вызывается и этот `OnStop` не закрывает созданный pool; это также противоречит [D-057](../ai/decisions/D-057-the-application-opens-the-connection.md) |
| Optimistic writes | SQL repository сравнивает версию, прочитанную внутри своего текущего вызова; [`UpdateCommand`](../../port/command.go) не несёт версию клиента, а `DefaultService.Replace` вызывает `Save` | Последовательный stale HTTP write может затереть более новое изменение |

Текущий `TestTheGeneratedDefinitionCompilesAndActivatesByRole` проверяет только
число выбранных constructors. Он не доказывает, что route смонтирован, worker
запущен, seeder выполнен или check попал в registry.

<a id="m0"></a>

## M0. Generated kind требует branded binding

**Приоритет:** P0. Генератор уже просит подтвердить kind, но не исполняет смысл
этого подтверждения.

### Контракт

Container-neutral `module.Definition` сохраняется. `internal/codegen` не
импортирует Fx и transport packages, а `module.manifest.yml` не получает второй
список binder-ов.

Generated-файл `vv_module_gen.go` вызывает
`module.Unbound(canonicalSymbol, constructor)` для каждого raw non-`provide`
constructor. `canonicalSymbol` — стабильный `import/path.Symbol`, уже известный
generator-у. `module.Contribution` получает поля `Symbol string` и
`NeedsBinding bool`; handwritten definitions без этой отметки сохраняют
нынешнюю pre-bound семантику.

Kind и binder нельзя передавать независимой парой: `func(any) any` непрозрачен,
поэтому такая пара позволила бы назвать `AsRoute` worker-binding-ом. Вместо неё
`app/module` даёт container-neutral brand:

В `app/module`:

```go
func Unbound(symbol string, constructor any) any

type BindingKind interface {
    ModuleKind() Kind
}

type Binder[K BindingKind] func(constructor any) any
```

Каждый Fx satellite держит brand приватным и экспортирует готовую фабрику:
`appfiber.RouteBinding`, `runtimefx.RunnerBinding`, `appfx.SeederBinding` и
`healthfx.CheckBinding`. Их return type можно передать дальше с generic type
inference, но root не может перепутать отдельный аргумент kind.

В `app/appfx` остаётся только упаковка branded binder-а:

```go
func NewBinding[K module.BindingKind](binder module.Binder[K]) (Binding, error)
func MustBind[K module.BindingKind](binder module.Binder[K]) Binding
func Option(definition module.Definition, profile module.Profile, bindings ...Binding) fx.Option
func Options(catalog module.Catalog, profile module.Profile, bindings ...Binding) fx.Option
func Auto(catalog module.Catalog, bindings ...Binding) fx.Option
```

Поля `Binding` закрыты. `NewBinding` выводит kind из zero value brand-а; zero
`Binding`, пустой или `provide` kind и nil typed binder отвергаются.

Composition root выбирает factory один раз на используемый kind и отдельно
подключает настраиваемый consumer, например `runtimefx.RunnerBinding()` и
`runtimefx.Supervising(spec)`. `appfx.Options`:

- пропускает `provide` и pre-bound contributions как раньше;
- применяет binding только к выбранным `NeedsBinding` contributions;
- до запуска constructors отвергает отсутствующий, nil или повторный binding и
  называет profile, module, kind и symbol.

Правильность четырёх стандартных factory доказывают сквозные consumer tests:
Go не умеет анализировать семантику значения, возвращённого `fx.Annotate`.
Custom marker flags генератора по-прежнему лишь сопоставляют другой result type
одному из пяти canonical kinds; новые kinds M0 не вводит. Consumer-defined brand
сознательно является extension point, а не framework guarantee: его автор
владеет binder-ом и таким же consumer test. Если одному kind нужны два
несовместимых container/transport consumer, такой каталог остаётся в ручном
pre-bound пути: новый registry и package scanning для этого не вводятся.

`Definition.Active` остаётся compatibility API. Новый путь читает полные
`Contribution`, чтобы не потерять kind и `NeedsBinding`. `Doctor` сообщает
только статически выбранные contributions и никогда не строит graph. Он не
называет их запущенными и не пытается угадать, потребил ли Fx value group.

### Acceptance criteria

Один внешний `GOWORK=off` fixture выполняет настоящий `vv generate module` и
проверяет:

1. `Serving` с `appfiber.Mounting` строит provider, монтирует route и через
   реального health endpoint потребляет check, но не строит worker/seeder.
2. `Working` запускает worker настоящим `runtime.Supervisor`, потребляет check и
   не строит route/seeder; stop вызывает drain и завершение один раз.
3. `Seeding` получает `*app.Runner` из `appfx.Seeding`, вызывает его как
   production command и выполняет seeder один раз без API/worker.
4. Удаление binding для выбранного generated kind ломает `fx.New` до вызова
   contribution constructors; ошибка содержит profile/module/kind/symbol.
5. Nil/duplicate binding отвергается детерминированно; один binding обслуживает
   любое число contributions своего kind.
6. Каждая стандартная binding factory отдельным control-тестом доставляет
   contribution именно своему production consumer; API не принимает отдельный
   kind, который root мог бы случайно сопоставить другой factory.
7. `Doctor` и `Describe` не вызывают constructors и не заявляют runtime-успех.
8. После изменения generated output `vv generate module -check` сообщает stale
   `vv_module_gen.go`, ничего не перезаписывая.
9. Handwritten `module.New(...).Workers(runtimefx.AsRunner(...))` с прежним
   двухаргументным вызовом `appfx.Options` продолжает работать без двойной
   annotation.

Отсутствующий production consumer остаётся ошибкой явной composition root, как
и для handwritten Fx wiring. Первый slice не вводит недостоверное состояние
`owned`: для seeder и health registry сам факт наличия option ещё не означает,
что command или endpoint действительно их потребил.

После реализации обновляются [FL-030](../ai/flows/FL-030-a-module-definition-becomes-a-running-deployment.md),
[FL-032](../ai/flows/FL-032-a-package-tree-becomes-a-confirmed-module.md), CLI docs
и оба языка module docs. До этого generated kind нельзя документировать как
дошедший до production consumer.

<a id="m1"></a>

## M1. `crudsqlfx` принимает application-owned pool

**Приоритет:** P0. Это исправляет конкретную утечку и возвращает binding к уже
принятому [D-057](../ai/decisions/D-057-the-application-opens-the-connection.md),
а не вводит общий resource manager.

### Контракт

`crudsqlfx.Module(*vvdb.Config)` и `crudsqlfx.Open` заменяются единственным
borrowed-путём:

```go
type Spec struct {
    Database      *sql.DB
    Engine        crudsql.Engine
    SchemaTimeout time.Duration
}

func Module(spec Spec, options ...crudsql.Option) fx.Option
```

Application вызывает `vvdb.Open`, сразу устанавливает собственный cleanup и
только затем строит Fx app. `crudsqlfx.Module` предоставляет в graph тот же
`*sql.DB` и построенный над ним `crud.Source`, но никогда не вызывает `Close`.
Connection ping остаётся в `OnStart`; schema read остаётся constructor-time
операцией с конечным `SchemaTimeout` и существующим безопасным default.

Старый owned API удаляется в этой миграции. Compatibility shim, который
продолжает открывать pool внутри `fx.New`, запрещён: он сохранял бы исходную
утечку. Несколько pools одного типа, named bindings и универсальный
`Resource[T]` не входят в задачу; их root wiring и ownership остаются явными.

### Acceptance criteria

1. `crudsqlfx` больше не импортирует и не вызывает `vvdb`; это закреплено
   dependency-check тестом.
2. Module отвергает nil database, неизвестный engine и отрицательный timeout до
   сетевого I/O; zero сохраняет документированный default.
3. На ошибке более позднего constructor, ошибке `OnStart` и обычном stop module
   не закрывает borrowed pool.
4. Внешний root fixture закрывает тот же pool ровно один раз во всех трёх
   сценариях, включая ошибку `fx.New` до `Start`.
5. Ping использует context от `OnStart`; schema read завершается по
   `SchemaTimeout`, а исходная ошибка сохраняется.
6. `crud.Source` и native SDK consumer наблюдают тот же `*sql.DB`, без второго
   открытия.
7. PostgreSQL, MySQL/MariaDB и SQLite сохраняют текущий dialect/classifier
   mapping; unit tests не требуют живой сети, integration matrix использует
   реальные servers там, где они уже обязательны.

README, API surface, [FL-021](../ai/flows/FL-021-a-configuration-becomes-a-connection.md)
и русская/английская `crudsql` документация меняются вместе с API.

<a id="m2"></a>

## M2. Клиентская revision участвует в атомарной mutation

**Приоритет:** P1, design checkpoint. Задача закрывает lost update между
клиентами opt-in CRUD PATCH/PUT; она не объявляет model revision валидатором
HTTP representation.

### Core contract

`crud.Revision` — неотрицательный `uint64`; pointer в command отличает
отсутствие ожидания от revision `0`. Один `crud.RevisionOf` читает integer
version из model metadata и отвергает nil, отрицательное значение и overflow.
Revision `0` при этом валидна. SQL adapter так же проверяет преобразование
expectation в фактический Go/SQL type version column.

```go
func RevisionOf[M any](*Meta, *M) (Revision, error)

type RevisionUpdater[M any, ID comparable] interface {
    UpdateAtRevision(context.Context, ID, any, Revision, ...Option) (M, error)
}

type RevisionReplacer[M any] interface {
    ReplaceAtRevision(context.Context, *M, Revision, ...Option) (M, error)
}

func UpdateAtRevisionOf[M any, ID comparable](
    Core[M, ID], context.Context, ID, any, Revision, ...Option,
) (M, error, bool)

func ReplaceAtRevisionOf[M any, ID comparable](
    Core[M, ID], context.Context, *M, Revision, ...Option,
) (M, error, bool)

func (r *Repo[M, ID, U]) UpdateAtRevision(
    context.Context, ID, U, Revision, ...Option,
) (M, error)

func (r *Repo[M, ID, U]) ReplaceAtRevision(
    context.Context, *M, Revision, ...Option,
) (M, error)
```

Оба interface — optional effects двухпараметрического `crud.Core[M, ID]`.
`UpdateAtRevisionOf` и `ReplaceAtRevisionOf` проверяют только exact outer Core и
никогда не идут через `Next`. `crud.Repo[M, ID, U]` восстанавливает typed
`UpdateAtRevision(..., U, ...)` façade; его наличие само по себе не считается
поддержкой, если exact wrapped Core потерял capability.

В `port`:

```go
type RevisionSupport interface {
    CheckRevisionMutations(Operations) error
}
```

`port.UpdateCommand` и `port.ReplaceCommand` получают
`ExpectedRevision *crud.Revision`. `DefaultService` вызывает CAS-capability
только при заданном ожидании; её отсутствие возвращает новый
`crud.ErrNoRevisionSupport`, никогда не падая обратно в обычный write.

На service seam `port.RevisionSupport.CheckRevisionMutations(operations)`
получает полный реально смонтированный mask. `DefaultService` проверяет
capabilities только для присутствующих `OpUpdate`/`OpReplace` и отдельно
отказывает небезопасному `OpCreate`, описанному ниже. Для `*crud.Repo` check
смотрит в `Unwrap()` Core; custom repository может явно реализовать typed
capability. HTTP owner вызывает check до mount; service decorators проводят его,
а repository decorators явно проводят оба effect-а либо честно теряют поддержку.
Третьего repository-level support interface нет.

При `ExpectedRevision == nil` нынешние `Update` и `Replace → Save` не меняются.
При заданной revision `DefaultService` вызывает только соответствующий CAS
effect; fallback в обычный write запрещён. `ReplaceAtRevision` является
update-only и не использует insert/upsert ветку, даже если target был удалён
после выдачи token.

`security.gate` и `faults.enricher` явно реализуют оба effect-а. Gate применяет
authorization, immutable и inspection rules, затем добавляет scope, relation
scopes и predicate inspected snapshot **после** caller options. Эти ограничения
доходят до exact inner capability и входят в тот же write; неизвестный decorator
возвращает `crud.ErrNoRevisionSupport`, а не обходится.

`crud/sqlrepo` реализует обе CAS-capabilities для всех поддержанных dialects.
ID, effective scope, relation scopes и expected version входят в `WHERE` того же
write statement, которое изменяет строку и увеличивает version. Для no-op PATCH
repository сравнивает expectation с row, уже полученным существующим
`mutationRead`, до раннего return: current no-op сохраняет revision, stale no-op
получает `ErrStaleVersion`. Это сохраняет UC-009 и не добавляет второй transport
read. Нулевой `RowsAffected` классифицируется проверкой с теми же scope и
relation scopes, но без revision: невидимый/отсутствующий row остаётся
`ErrNotFound`, видимый с другой revision становится `ErrStaleVersion`.

### HTTP projection

Каждый HTTP transport получает одну opt-in policy `RequireRevision()`. После её
включения GET-by-ID и успешные PATCH/PUT возвращают decimal
`Resource-Revision`; PATCH/PUT требуют decimal `If-Resource-Revision`. Общий
parser/renderer живёт в `crudhttp`, а model value нормализует `crud.RevisionOf`;
поэтому net/http, Gin и Fiber не получают три codec-а. Header ставится из model
до custom presenter/transform и является row CAS token, а не entity-tag.

GET storage projection всегда включает version, даже если public `select` её не
назвал; presentation остаётся отдельным шагом. Успешный backend обязан вернуть
model с доступным version field (и non-nil значением для pointer field), который
`RevisionOf` может представить; иначе transport возвращает internal error, а не
ложный token.

| Случай | HTTP | Code |
|---|---:|---|
| `If-Resource-Revision` отсутствует | 428 | `precondition_required` |
| Header пуст, не decimal или выходит за `uint64` | 400 | `invalid_revision` |
| Revision stale для видимого row | 412 | `precondition_failed` |
| Row отсутствует или скрыт scope/authorization | 404 | существующий `not_found` |
| Внутренний optimistic conflict без этой HTTP policy | 409 | существующий `stale_version` |

Парсинг формы header не обращается к storage. Решение 404 против 412 принимается
только после CAS/probe с одинаковыми scope и relation scopes; unscoped
existence/version probe запрещён. Versionless model или service без capability
для каждой смонтированной mutation отвергается при подключении policy, до mount
и сетевого I/O.

M2 не меняет POST. Если смонтированный `OpCreate` при текущих service rules может
upsert существующий client-owned ID, `RequireRevision` отказывает при mount:
такой путь менял бы row, не увеличив revision. Consumer может исключить create
из этого resource или принять отдельное решение о create-only transport path.

Гарантия ограничена write surface, которая соблюдает этот contract. Прямой
`Save`, raw SQL или внешний writer остаются last-write-wins и не превращают
`Resource-Revision` в strong ETag. Использующий policy consumer не должен менять
ту же строку такими путями; глобальный invariant для всех writers потребует
отдельного решения.

### Acceptance criteria

1. Два клиента получают одну revision; первый изменяющий PATCH/PUT проходит и
   возвращает новую, второй получает 412 и не меняет row.
2. Два одновременных изменяющих write с одной revision дают одного победителя.
3. SQL assertion видит ID, scope, relation scope, inspected predicate и expected
   revision в `WHERE` того же UPDATE; transport не делает read-before-write.
4. PUT с expectation вызывает `ReplaceAtRevision`, не достигает `Save` и не
   создаёт исчезнувший target; PUT без expectation сохраняет нынешний путь.
5. Conditional no-op PATCH с current revision проходит и оставляет её прежней;
   stale no-op получает 412.
6. Missing/empty/non-decimal/overflow/stale cases проходят общую status/code
   matrix во всех трёх HTTP bindings.
7. GET-by-ID и каждый успешный PATCH/PUT response возвращают текущий пригодный
   `Resource-Revision`, независимо от presentation; POST/DELETE M2 не меняет.
8. Security decorator сохраняет scope, relation scopes и inspected snapshot в
   CAS. Current и stale revision для невидимого row дают одинаковый 404 и не
   раскрывают текущую revision.
9. GET с `select` всё равно загружает version для header; custom presenter её не
   определяет. Nil/negative response version даёт internal error без header.
10. Versionless model, overflow, потенциально upserting mounted Create и
    backend/decorator без capability отказывают явно; обычного write fallback
    нет.

Strong `ETag`/`If-Match`, `If-None-Match`/304, DELETE preconditions, command
receipts, retries и distributed locks не входят в первый slice. ETag возвращается
только после отдельного инварианта: любой writer меняет revision и каждый
representation variant имеет корректный validator.

## Очерёдность

M0 и M1 — независимые P0 и могут выполняться параллельно. M2 — P1 и технически
от них не зависит; начинать его можно после принятия core/HTTP contracts выше.

До первой строки реализации решения синхронизируются с действующими ADR:

- M0 дополняет D-074 узкой container-neutral metadata, а D-106 и D-110 —
  generated unbound path и branded root binding; handwritten path не меняется;
- M1 реализует уже принятое D-057 и не вводит новую ownership model;
- M2 дополняет D-001 erased-core/typed-façade capability, D-010 client
  expectation, D-011 только conditional PUT-веткой и D-061 exact-effect
  forwarding. Безусловный PUT и `Save` не переопределяются.

## Не входит в активный план

- Recipe installer, lock/eject/merge и встроенный package manager для CLI.
- Huma/OpenAPI/SDK, Proto, realtime, OIDC и API versioning без выбранного
  consumer и transport ecosystem.
- Общие scheduler, notifications, webhooks, rate limiting, GraphQL, uploads,
  feature flags, MFA/ReBAC/PAT и secret leases: это самостоятельные product или
  security contexts.
- Новые jobs/cache/storage/tenancy/OTel механики в этом файле: они либо уже
  существуют, либо требуют решения в roadmap своего владельца.
- Общий idempotency/receipt protocol по одной application-specific реализации.

Удалённая карточка возвращается только с named consumer, owner, воспроизводимым
разрывом и тестом, который нельзя закрыть существующей public surface.

## Общий Definition of Done

Каждый milestone закрывает все собственные acceptance criteria. Дополнительно:

1. изменённые modules проходят unit/integration tests, `-race` и внешний
   `GOWORK=off` fixture в объёме изменённого контракта;
2. live Roadmap, flows, API surface и русская/английская документация описывают
   ровно реализованную гарантию;
3. base packages не получают Fx, transport или другую запрещённую dependency;
4. ни один fallback не превращает unsupported или stale состояние в успешную
   операцию.

Любая следующая механика заново проходит фильтр в начале документа. Этот файл
не становится каталогом возможностей второй раз.
