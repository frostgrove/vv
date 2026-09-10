# otel (vvotel)

`github.com/frostgrove/vv/otel` (пакет `vvotel`) предоставляет опциональные,
композируемые OpenTelemetry-адаптеры для service, storage, cache,
authentication, health, runtime, remote, CRUD и durable-job границ Frostgrove.

Production-код импортирует только стабильные OpenTelemetry API-пакеты, включая
W3C Trace Context propagation для durable jobs. Модуль не импортирует SDK,
exporter, Collector client, contrib instrumentation, `otelslog`, OTel Logs API
или bridge драйвера базы данных. Он заимствует providers и оставляет приложению
bootstrap SDK, native instrumentation, export policy, flush и shutdown.

Текущий сгенерированный контракт — `ContractVersion = vv-otel/v2`;
instrumentation scope — `github.com/frostgrove/vv/otel` с
`ScopeVersion = v0.1.0`. Зафиксированные registry, сгенерированная Go-схема и
wire manifest содержат 59 стабильных signal ID; все 59 имеют статус
`implemented`. Registry — источник истины
для имён, mapping-ов, privacy-классов, ограничений кардинальности, availability
и migration metadata; см. [otel-spec.md](../otel-spec.md).

## Что вы получаете

- `vvotel.New` и `vvotel.Must`, принимающие переданные `trace.TracerProvider` и
  `metric.MeterProvider`;
- закрытый выбор отдельных сигналов через `Config.Disable` и eager fail-fast
  создание всех включённых metric-инструментов совместимого provider family;
- `vvotel.WrapService` с выводом generic-типов и низкоуровневый middleware
  `vvotel.Service` для всех команд `port.Service`;
- `vvotel.Store` для всех операций `storage.Store`, включая duration,
  persisted-size успешного результата и cleanup-result;
- опциональный `vvotel.WithStorageStreams` для lazy lifetime reader-а,
  terminal outcome и фактических байтов; `vvotel.StorageStream.Unwrap` оставляет
  прямой низкоуровневый выход;
- Терминальные observers `vvotel.Cache` и `vvotel.CacheMemory`;
- Явная aggregate-регистрация `vvotel.CacheMemoryStats` и
  `vvotel.MustCacheMemoryStats` для 1–64 in-process memory backends;
- Адаптеры `vvotel.Auth`, `vvotel.AuthEvents`, `vvotel.Authenticator`,
  `vvotel.Health`, `vvotel.Runtime`, `vvotel.Periodic` и `vvotel.Remote`;
- `vvotel.Source` для прямых CRUD-вызовов, transaction, replica и разрешённой
  native-bulk границы;
- `vvotel.JobContext`, `vvotel.JobIdentity`, четыре enqueue helper-а,
  `vvotel.Job`, `vvotel.JobAdapter`, `vvotel.Workers` и `vvotel.Scheduler` для
  durable propagation и producer/consumer/control-plane telemetry;
- Независимый stdlib-декоратор `vvotel.TraceHandler` для `slog.Handler`: он
  добавляет неделимый набор `trace_id`/`span_id`/`trace_flags` из валидного
  контекста, но не экспортирует логи и не перезаписывает поля вызывающей стороны;
- fail-safe запись операции: сломанный trace-путь не подавляет рабочую метрику
  и не меняет бизнес-вызов, а сломанная метрика не подавляет span;
- admission полного набора атрибутов через сгенерированный allow-list до
  каждого испускающего OTel-вызова, классификацию ошибок без утечки PII и
  ограниченную кардинальность.

Все 59 descriptor-ов имеют испускающий adapter. Включённые metric-инструменты
создаются eager: сломанный provider останавливает сборку приложения, а не
теряется при первом запросе. `New` не регистрирует observable callbacks;
cache-memory gauges начинают работать только после явного `CacheMemoryStats` и
останавливаются после `Unregister` возвращённой регистрации.

`Disable: vvotel.Signals{vvotel.SignalX, ...}` отключает отдельные семантические
сигналы. Неизвестные и повторяющиеся явные ID отвергаются даже при
`Disabled: true`. Legacy bool-поля остаются точными aliases: command trace —
ID 2, command metric — ID 1, storage traces — ID 4 и 8, cache metrics — ID 9 и
12–23. Cache span events 10 и 11 они не отключают.

При двух providers пустой selector включает все families. Trace-only
конфигурация активирует trace- и context-only сигналы, metric-only — metric- и
context-only. Без обоих providers `New` возвращает `ErrNilProvider`; только
`Disabled` создаёт provider-free no-op и не вызывает OTel API. Ошибки
provider/инструмента возвращаются как отредактированный `*AssemblyError` с
поддержкой `errors.Is`. Методы `Signal`, `Provider` и `SignalName` показывают
только закрытую позицию в registry. `Unwrap` доступен только для ошибки, которую
вернул constructor.

Реализованный signal ID 5, `storage_operation_bytes`, имеет только успешные
варианты `put/ok` и `stage/ok`. Он записывает persisted size из возвращённого
`Info.Size` или `Staged.Info.Size`, не оборачивает и не читает source и не
выдаёт это значение за фактически прочитанные из reader байты.

`ResourceName` — необязательный trace-only `ApprovedName`. Строковый литерал
остаётся кратким; динамическое значение проходит через `vvotel.ApproveName`
или `vvotel.MustApproveName`. Прямое преобразование `ApprovedName(value)` —
низкоуровневый escape hatch, но runtime всё равно отбрасывает невалидное имя.
Допустимы буквы, цифры, `.`, `_` и `-`, максимум 64 байта. Один `Telemetry`
имеет общий лимит в 32 различных имени для service и storage; дубликат не
занимает новый слот, configured default считается только при привязке trace-
адаптера. Имя никогда не попадает в metric attributes или exemplars.
Идентификаторы, ключи, URL, payload, сообщения и credentials не становятся
telemetry labels. Неизвестные значения cache и прикладные коды ошибок
опускаются.

Command- и storage-декораторы вызывают бизнес-операцию ровно один раз.
Контекст созданного span получают и операция, и command histogram, поэтому
exemplar может ссылаться на command span. Паники telemetry изолированы. Бизнес-
паника записывается и пробрасывается тем же значением; `runtime.Goexit`
завершает span как `goroutine_exit` без `error.type` и метрики незавершённой
операции.

Приложение владеет Resource, SDK providers, exporters, readers/processors,
Views, sampling, native instrumentation, export projection, flush и shutdown.
Рабочая OTLP-композиция и lifecycle ownership находятся в
[`_examples/otel-production`](../../../_examples/otel-production/). Не
публикуемый модуль [`test/otelnative`](../../../test/otelnative/) содержит явно
подключённые рецепты и privacy-canary для HTTP, Gin, Fiber, gRPC, HTTP client,
pgx, `database/sql`, pgxpool и Go runtime. `vvotel` не владеет этими contrib-
интеграциями.

Для обслуживания используйте `make generate` и `make check-otel-schema`; второй
является read-only freshness gate. Команда `make version V=v0.1.0` обновляет
scope version в registry и регенерирует Go-схему и wire manifest. Перед
публикацией запустите `make check-otel-consumer V=v0.1.0` в окружении, где
доступны lockstep-теги root и `otel`.

## Подключение

```go
telemetry := vvotel.Must(vvotel.Config{
    TracerProvider: tracerProvider,
    MeterProvider:  meterProvider,
    ResourceName:   "products",
})

service := vvotel.WrapService(telemetry, baseService)

store := storage.Chain(
    baseStore,
    vvotel.Store(telemetry),
)

cacheRuntime.Observer = cache.MustObservers(
    existingObserver,
    vvotel.Cache(telemetry, vvotel.WithCacheSpanEvents(true)),
)

memoryObserver := vvotel.CacheMemory(telemetry, vvotel.WithCacheMemorySpanEvents(true))
memoryPrimary, err := cachememory.New(primaryLimits, cachememory.WithObserver(memoryObserver))
memorySecondary, err := cachememory.New(secondaryLimits, cachememory.WithObserver(memoryObserver))

stats := vvotel.MustCacheMemoryStats(telemetry, memoryPrimary, memorySecondary)
defer stats.Unregister()

logger := slog.New(vvotel.TraceHandler(slog.NewJSONHandler(os.Stdout, nil)))
```

Короткий путь использует прямые wrappers:

```go
source = vvotel.Source(telemetry, source)
authenticator = vvotel.Authenticator(telemetry, authenticator)
transport = vvotel.Remote(telemetry, transport)
pass = vvotel.Periodic(telemetry, pass)
```

Исходные middleware/observer API остаются доступны, когда нужно явно управлять
порядком или набором сигналов. Native transport/database instrumentation
получает те же application-owned providers и не прячется внутри этих wrappers.

Для динамического логического имени:

```go
name, err := vvotel.ApproveName(config.ServiceName)
if err != nil {
    return err
}
telemetry := vvotel.Must(vvotel.Config{
    TracerProvider: tracerProvider,
    ResourceName:   name,
})
```

Используйте `New` вместо `Must`, если composition root возвращает startup
errors. `New` не регистрирует callbacks, не откатывает providers, не запускает
goroutines и не владеет shutdown. `CacheMemoryStats` — отдельная fallible
регистрация callback-а с собственным явным idempotent cleanup handle. Ошибки
валидации, дублирования и native-регистрации callback-а сопоставляются с
`ErrAssembly` и соответственно с `ErrInvalidRegistration`,
`ErrDuplicateRegistration` или `ErrCallbackRegistration`; cleanup failure
дополнительно сопоставляется с `ErrCallbackUnregister`. `MustCacheMemoryStats`
паникует тем же typed framework error.

Collector, sampling, PromQL и validation-рецепты находятся в
[`docs/operations/otel`](../../operations/otel/). Native HTTP, gRPC, database и
runtime composition описана в
[`test/otelnative`](../../../test/otelnative/), а production SDK/export
lifecycle — в [`_examples/otel-production`](../../../_examples/otel-production/).

Для экспорта логов через OTel приложение само создаёт native-handler `otelslog`,
оборачивает его своей политикой редактирования и передаёт результат в
`vvotel.TraceHandler`. Поэтому redaction/export chain видит уже добавленную
корреляцию. Если нужна только native-семантика, приложение передаёт handler
`otelslog` напрямую; `vvotel` его не импортирует и не владеет его provider-ом.
