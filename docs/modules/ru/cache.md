# cache

`cache` — типизированный facade кеша только на stdlib. Он отвечает за стабильные
адреса, версии значений, freshness/stale/negative-семантику, локальную
координацию загрузок, fences для мутаций и жёсткие лимиты работы. Backend хранит
только непрозрачные конверты.

Кешируйте только то, что можно заново получить и не страшно потерять раньше
срока. Отзывы токенов, rate limits, intents и leases задач, аудит и состояние
workflow не становятся кешем лишь потому, что их можно положить в Redis.

## Декларативный путь

Объявляйте кеш рядом с application-модулем, которому принадлежит значение:

```go
type ProductCard struct {
	Name  string
	Price int64
}

type ProductCardKey struct {
	ID string `cachekey:"id"`
}

var ProductCards = cache.Auto[ProductCardKey, ProductCard]()

var ProductCardsDefinition = cache.MustDefine(ProductCards, cache.DefinitionSpec[ProductCardKey, ProductCard]{
	Name:      "product-cards",
	Namespace: cache.NamespaceTemplate{Purpose: "product-cards", Generation: 1},
	Scope:     cache.GlobalPlan[ProductCardKey](),
	Keys:      cache.MustStructKey[ProductCardKey](1),
	Values:    cache.JSON[ProductCard](1),
})

var Caches = cache.MustSet(ProductCardsDefinition)
```

`Auto` по умолчанию выбирает профиль `Hot`; можно передать `Warm`, `Durable`
или `Disabled`. На старте приложение один раз передаёт доступные backend'ы и
атомарно активирует все наборы:

```go
err := cache.Activate(ctx, cache.ActivationSpec{
	Application: "catalog",
	Environment: "production",
	Sets:        []cache.Set{Caches},
	Providers: []cache.Provider{{
		ID:       "local-cache",
		Resource: "catalog-process",
		Kind:     cache.MemoryProviderKind,
		Backend:  memoryBackend,
	}},
})
```

До публикации проверяется весь граф. Отсутствующий или неоднозначный provider,
повтор физического namespace, несовместимый backend, неверный codec или
недостающая capability дают одну ошибку старта, а не сюрприз на первом запросе.
`Describe` показывает итоговую декларацию и все эффективные лимиты.

### Домены вытеснения

`Provider.Resource` называет физический ресурс, с которым говорит provider, —
один Redis, одну базу, один процесс. Скажите, что ещё там живёт, и активация
откажет кешу, который разделил бы домен вытеснения с durable-состоянием:

```go
Resources: []cache.ResourceDeclaration{
	{Resource: "redis-cache", Tenants: []cache.ResourceTenant{cache.CacheTenant}},
	{Resource: "redis-durable", Tenants: []cache.ResourceTenant{
		cache.DurableWorkTenant, cache.DurableSecurityTenant,
	}, Waiver: cache.SharedDurableSecurity("один redis, пока не поднят кластер задач")},
},
```

Кеш вытесняется намеренно, а очередь задач и список отзывов этого не переживают.
Вытесненная запись отзыва читается как «не отозван», поэтому общий `maxmemory`
превращает нехватку памяти в вернувшуюся сессию. Три вида состояния — три
идентичности ресурса: `CacheTenant`, `DurableWorkTenant` и
`DurableSecurityTenant`. Кеш, попавший на ресурс, объявленный с любым из
durable-tenant'ов, отвергается, и никакой waiver этого не извиняет.
`SharedDurableSecurity` извиняет только совместное проживание durable work и
durable security на одном ресурсе; причина обязательна, а waiver там, где ничего
не разделяется, сам становится отказом. Необъявленный ресурс не проверен — это
не доказательство, что он отдельный: поставьте
`RequireDeclaredResources: true`, и активация откажет кешу, чей ресурс никто не
описал, — забытая декларация станет ошибкой старта, а не молчанием.
См. [[D-104]].

## Привязка к fx

```go
import "github.com/frostgrove/vv/cache/cachefx"   // uber/fx: группы и активация
```

`cachefx` — отдельный модуль, потому что тянет uber/fx ([[D-033]]). Именно он
вызывает `Activate` в работающем процессе: без него правило доменов вытеснения
выше не проверяет ничего.

```go
fx.Options(
    fx.Provide(cachefx.AsSet(func() cache.Set { return catalog.Caches })),
    fx.Provide(cachefx.AsProvider(newRedisCacheProvider)),
    cachefx.Resources(
        cache.ResourceDeclaration{Resource: "redis-cache", Tenants: []cache.ResourceTenant{cache.CacheTenant}},
        cache.ResourceDeclaration{Resource: "redis-sessions", Tenants: []cache.ResourceTenant{cache.DurableSecurityTenant}},
        cache.ResourceDeclaration{Resource: "postgres-jobs", Tenants: []cache.ResourceTenant{cache.DurableWorkTenant}},
    ),
    cachefx.Auto("catalog", "production"),
)
```

| | |
|---|---|
| `AsSet(ctor)` · `AsProvider(ctor)` · `AsResource(ctor)` | аннотируют конструктор в группу наборов, провайдеров или ресурсов |
| `Resources(declarations…)` | декларации, которые композиционный корень пишет сам |
| `Contributions` | три группы и необязательный `cache.Observer` как `fx.In`-объект параметров |
| `Spec` | `Application`, `Environment`, `Runtime`, `Sets`, `Providers`, `Resources`, `Undeclared` |
| `Caching(spec)` / `Auto(application, environment)` | магическая форма: собрать спецификацию и активировать |
| `Undeclared` | `Refused` (по умолчанию) · `Accepted` |
| `Activating(ctor)` | низкоуровневая форма: конструктор собирает весь `cache.ActivationSpec` |

**Здесь декларации обязательны.** В `cache` флаг `RequireDeclaredResources`
выключен, чтобы правило можно было внедрять ресурс за ресурсом; граф, который
берёт эту привязку, внедрение уже закончил, поэтому `Caching` включает флаг, и
молчание о ресурсе роняет старт. Развёртывание, которое ещё в середине
внедрения, пишет `Undeclared: cachefx.Accepted` — словом, а не забытым нулевым
значением.

**Декларация — это данные, поэтому импортировать кеш ради неё никому не нужно.**
Список отзывов и очередь задач — те самые два тенанта, ради которых правило и
существует, и ни один из них не импортирует `cache`: их `ResourceDeclaration`
пишет композиционный корень через `Resources`, а пакет, который говорит за себя
сам, вкладывается через `AsResource` или прямо написанным тегом группы —
``fx.ResultTags(`group:"vv.cache.resources"`)``, — для чего импорт `cachefx`
тоже не нужен. Из провайдера ничего не выводится: привязка, которая тихо
объявила бы `CacheTenant` для каждого увиденного ресурса, удовлетворяла бы
проверку тем самым, что проверяется. См. [[D-111]].

**Активация — хук старта.** Кеши публикуются, когда приложение стартует, а не
когда собирается его граф, поэтому отказ — это падение старта, которое fx
разматывает: всё уже поднятое останавливается обратно. Отказ приходит как
`*cache.ActivationError`, и его `Problems()` называет сразу все кеши и ресурсы.

Низкоуровневая форма — та же привязка без сборки:

```go
cachefx.Activating(func(contributed cachefx.Contributions) (cache.ActivationSpec, error) {
    return cache.ActivationSpec{
        Application: "catalog",
        Environment: "production",
        Sets:        []cache.Set{catalog.Caches},
        Providers:   contributed.Providers,
        Resources:   contributed.Resources,
        RequireDeclaredResources: true,
    }, nil
})
```

## Чтение и загрузка

`Lookup` никогда не вызывает application-код. `Resolve` сначала читает backend
и только при miss запускает переданный loader. Loader возвращает
`cache.Present(value)` либо `cache.Absent[V]()`. Отсутствие сохраняется лишь при
включённом negative caching.

```go
result, err := ProductCards.Resolve(ctx, ProductCardKey{ID: productID}, func(ctx context.Context, key ProductCardKey) (cache.LoadResult[ProductCard], error) {
	card, found, err := products.FindCard(ctx, key.ID)
	if err != nil {
		return cache.LoadResult[ProductCard]{}, err
	}
	if !found {
		return cache.Absent[ProductCard](), nil
	}
	return cache.Present(card), nil
})
```

`Result.State` различает `Hit`, `Miss`, `Negative`, `Stale` и `Loaded`.
`LookupMany` сохраняет порядок и повторы входа; backend с
`BatchReadCapability` может прочитать всё одной операцией. `Put` и `Forget`
ограждены от параллельных loader'ов: старый loader не перезапишет более новую
мутацию.

`ResolveMany` — пакетная форма. Она читает один раз, вызывает типизированный
`BatchLoader[K, V] func(ctx, []K) ([]LoadResult[V], error)` не более одного раза
с дедуплицированными недостающими ключами в порядке первого появления и
возвращает по одному результату на каждый входной ключ во входном порядке:

```go
results, err := ProductCards.ResolveMany(ctx, keys, func(ctx context.Context, missing []ProductCardKey) ([]cache.LoadResult[ProductCard], error) {
	cards, err := products.FindCards(ctx, missing)
	if err != nil {
		return nil, err
	}
	return cards, nil
})
```

Loader обязан вернуть ровно один `LoadResult` на каждый переданный ключ.
Неверное количество, неустановленный `Presence`, ошибка или panic отменяют весь
вызов — и к этому моменту ничего не записано: каждый ответ кодируется и
проверяется по совокупному `MaxBatchResultBytes` до первой записи в backend.
`ResolveMany` закрывает `Miss`; `Stale` возвращается как есть и обновляется
через `Resolve`. Она не присоединяется к per-address flights, поэтому два
параллельных пакетных вызова могут каждый обратиться к своему loader'у.
См. [[D-095]].

## Execution memo

HTTP-запрос или попытка выполнения job'а может на своё время поставить перед
backend'ом ограниченный L0:

```go
memo, err := cache.NewMemo(cache.MemoLimit{MaxEntries: 128, MaxBytes: 1 << 20})
if err != nil {
	return err
}
defer memo.Close()
ctx = cache.WithMemo(ctx, memo)
```

Любой кеш, читающий из этого контекста, отвечает на повторный адрес без
обращения к backend'у. Memo хранит копии encoded envelope, поэтому свежесть
пересчитывается на каждом чтении и два вызова не делят изменяемое значение.
Он помнит только то, что backend действительно сохранил: **miss** не
запоминается никогда — его прямо сейчас может заполнять параллельная запись, —
а подтверждённый loader'ом **negative** запоминается. Ошибки и повреждённые
envelope не запоминаются, как и чтение, которое обогнала параллельная запись:
проигравший гонку lookup читает заново, и запоминается только подтверждённый
ответ, поэтому два чтения в одной execution не идут назад во времени.

`Put`, `Forget`, `Resolve` и `ResolveMany` сбрасывают запись memo по своему
адресу, а загрузки memo не читают. `Close` идемпотентен, очищает контейнер и
превращает любое дальнейшее чтение и запись в no-op, поэтому переживший запрос
goroutine ничего не удерживает. `Stats` показывает записи, удержанные байты,
попадания, сохранения и отказы. Байты memo — его собственный бюджет и не
относятся к `MaxTransientBytes`. См. [[D-094]].

Singleflight действует только внутри процесса и для одного типизированного
адреса. `MaxFlights` ограничивает группы loader'ов, а не вызывающих клиентов.
При насыщении можно отказать, ограниченно ждать или вернуть уже имеющееся stale
значение. Это не распределённая блокировка.

## Жёсткие лимиты работы

Каждая операция, создающая cache-owned transient work, получает admission до
создания coordination state, таймеров, собственной копии ключа, encoded value
или decode destination. Disabled no-op lookup/mutation ничего такого не создают.
`MaxTransientBytes` ограничивает transient-работу, относимую к кешу, а
`MaxTransientWaiters` навсегда резервирует достаточную часть бюджета под
объявленное число ожидающих. Размер резерва виден в
`Descriptor.Policy.ReservedTransientBytes`. Отказ возвращает `ErrSaturated`, не
запуская loader и не меняя backend.

В бюджет входят новые allocations и reservations, вызванные операцией кеша. В
него не входят уже существующие runtime pools, принадлежащие caller'у ключи,
замыкания loader'а и граф значений context. Работа и allocations внутри
application loaders/observers тоже принадлежат приложению, как и
consumer-provided key functions, partitioners, backends, clocks и randomness
providers. Пользовательский `Codec`, весь динамический encode path
`TrustedJSON` и каждый trusted hook body при encode/decode могут выполняться вне
лимита; это явные trust escapes.

`ValueLimit.MaxBytes` ограничивает wire bytes, а `MaxDecodedBytes` отдельно —
модель памяти decoded value. Безопасный `JSON` до вызова `encoding/json`
проверяет весь достижимый Go-тип и значение: interfaces, JSON/text hooks,
неподдерживаемые kinds и `time.Time` запрещены; глубина, граф, текст и объём
работы ограничены; сырой невалидный UTF-8 считается corrupt и для `JSON`, и для
`TrustedJSON`. Для канонического UTC `time.Time` есть `RFC3339UTC`.
`TrustedJSON` — явный escape для контролируемых приложением hooks/interfaces:
writer ограничивает wire output, результат проверяется по decoded size/depth,
но динамический encode graph/traversal и trusted hook bodies при encode/decode
не входят в bounded-work promise. Под `GOEXPERIMENT=jsonv2` безопасный JSON
намеренно недоступен, пока его allocation contract не доказан отдельно.

## Context и observers

Общий loader не принадлежит случайно пришедшему первым HTTP-запросу. Loader и
запись backend получают detached value-blind contexts с конечными runtime
timeouts. Терминальное событие `Load` тоже detached и value-blind; его observer
обязан соблюдать описанный ниже синхронный callback contract. Поэтому principal,
transaction, request body и другие request-scoped значения не протекают в
работу, общую для разных caller'ов. Нужные доменные данные передавайте
типизированным ключом или явной ограниченной зависимостью loader'а. Прямые
lookup/mutation всё ещё используют caller context для собственных
backend-вызовов.

Профиль `Disabled` обходит storage и coordination, но сохраняет loader timeout,
caller cancellation, value-blind context и transient admission. Это безопасный
операционный переключатель, а не вторая модель исполнения.

Observers вызываются синхронно вне locks, panic изолируется. Callback обязан
быть ограниченным и неблокирующим и не должен повторно входить в тот же кеш во
время события; чтение `Stats` безопасно. События общей загрузки предназначены
для метрик и намеренно не несут request values. `Event.Memoized` отмечает
ответ, выданный execution memo, а не backend'ом.

Несколько независимых observers собираются базовым веером, а не цепочкой,
принадлежащей одному из них:

```go
runtime.Observer = cache.MustObservers(applicationMetrics, exporter)
```

`Observers` копирует и проверяет конечный список при сборке, отказывает при
более чем `MaxObservers` детях, пропускает nil и typed-nil, вызывает детей
синхронно в порядке регистрации и изолирует panic каждого, так что следующие
всё равно отработают. Веер не запускает ни goroutine, ни очередь, ни retry, ни
таймер. См. [[D-096]].

## Проба

`Check(ctx) error` говорит, может ли этот кеш обслуживать, и ничего не говорит о
том, насколько это важно:

```go
health.Contribution{
	Name:       "product-cards",
	Code:       "cache",
	Importance: health.Degrading,
	Probe:      health.ProbeFunc(ProductCards.Check),
}
```

До активации она отказывает `ErrNotActivated`, при `Disabled` и при backend'е
без `HealthChecker` проходит, иначе вызывает backend в рамках backend deadline и
возвращает санированную категорию `ErrBackend` — но не собственное сообщение
драйвера. Importance, публичный код и транспорт принадлежат composition root
([[D-091]], [[D-096]]).

## Явная сборка

Если top-level декларации не нужны, тот же core можно собрать напрямую:

```go
policy, err := cache.Hot.Build()
if err != nil {
	return err
}

cards, err := cache.New(
	runtime,
	memoryBackend,
	cache.Global[ProductCardKey](cache.MustNamespace("catalog", "production", "product-cards", 1)),
	cache.MustStructKey[ProductCardKey](1),
	cache.JSON[ProductCard](1),
	policy,
)
```

`New` полезен библиотекам, тестам и приложениям со своей композицией. Он не
ослабляет validation, policy или bounded-work semantics.

## Backends

- [cachememory](cachememory.md) — ограниченный in-process backend.
- PostgreSQL и Redis adapters будут отдельными optional modules; текущий core
  не зависит от драйверов и пока не объявляет эти backends готовыми.
- Backend объявляет topology, источник времени expiry и поддержку
  size/capacity. Shared backend дополнительно требует явной границы clock skew.

### Capabilities

Backend может уметь больше, чем хранить envelope. Шесть capabilities встроены —
`BatchReader`, `CompareAndSwapper`, `Maintainer`, `HealthChecker`,
`TagInvalidator` и `Transactional` — у каждой есть константа
(`BatchReadCapability`, `CompareAndSwapCapability`, `MaintenanceCapability`,
`HealthCapability`, `TagInvalidationCapability`, `TransactionCapability`) и
типизированный поиск (`BatchReaderOf`, `CompareAndSwapperOf` и так далее),
проходящий декораторы через `Next()`.

Всё остальное называет драйвер. Backend, реализующий `CapabilityDeclarer`,
публикует собственные строки capabilities, и `Supports` отвечает по этому
набору. Объявленное имя никогда не даёт встроенную capability: встроенные
доказываются только методом, поэтому драйвер не может заявить `batch_read` без
`GetMany`. `DeclaredCapabilitiesOf` отбрасывает встроенные имена, некорректные
имена, слишком длинное объявление и объявление, упавшее в panic.

`DefinitionSpec.Requires` может назвать любую из них; провайдер, который
требование не закрывает, отвергается на активации, а не на первом вызове.
См. [[D-093]].

См. [[UC-024]], [[FL-025]], [[D-084]], [[D-085]], [[D-093]], [[D-094]],
[[D-095]], [[D-096]], [[D-104]] и [[D-111]].
