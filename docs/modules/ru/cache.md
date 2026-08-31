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
для метрик и намеренно не несут request values.

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

См. [[UC-024]], [[FL-025]], [[D-084]] и [[D-085]].
