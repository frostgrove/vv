# otel (vvotel)

`github.com/frostgrove/vv/otel` (пакет `vvotel`) предоставляет опциональные
адаптеры OpenTelemetry. Текущие runtime-адаптеры покрывают `port.Service`,
`storage.Store`, `cache.Observer` и `cachememory.Observer`.

Текущий production-код импортирует только стабильные OpenTelemetry API-пакеты
`trace`, `metric`, `attribute` и `codes`. [[D-128]] также разрешает `propagation`,
но W3C Trace Context появится только вместе с пока не реализованными адаптерами
durable jobs. Модуль не импортирует SDK, exporter, Collector client, contrib
instrumentation, `otelslog`, OTel Logs API или bridge драйвера базы данных. Он
заимствует провайдеры и оставляет приложению bootstrap SDK, native
instrumentation, export, flush и shutdown.

Текущий сгенерированный контракт — `ContractVersion = vv-otel/v2`;
instrumentation scope — `github.com/frostgrove/vv/otel` с
`ScopeVersion = v0.1.0`. Зафиксированные registry, сгенерированная Go-схема и
wire manifest содержат 59 стабильных signal ID. Шесть имеют статус
`implemented`, 53 — `planned` и пока не испускаются. Registry — источник истины
для имён, mapping-ов, privacy-классов, ограничений кардинальности, availability
и migration metadata; см. [otel-spec.md](../otel-spec.md).

## Что вы получаете

- `vvotel.New` и `vvotel.Must`, принимающие переданные `trace.TracerProvider` и
  `metric.MeterProvider`;
- закрытый выбор отдельных сигналов через `Config.Disable` и eager fail-fast
  создание всех включённых metric-инструментов совместимого provider family;
- Обобщённый middleware `vvotel.Service` для `port.Service`, создающий INTERNAL-спаны (`vv.command <op>`) и гистограмму длительности (`vv.command.duration`);
- Middleware `vvotel.Store` для `storage.Store`, создающий INTERNAL-спаны (`vv.storage <op>`);
- Наблюдатели терминальных событий `vvotel.Cache` и `vvotel.CacheMemory`, записывающие счетчик `vv.cache.operations` и опциональные события спанов;
- Независимый stdlib-декоратор `vvotel.TraceHandler` для `slog.Handler`: он
  добавляет неделимый набор `trace_id`/`span_id`/`trace_flags` из валидного
  контекста, но не экспортирует логи и не перезаписывает поля вызывающей стороны;
- fail-safe запись операции: сломанный trace-путь не подавляет рабочую метрику
  и не меняет бизнес-вызов, а сломанная метрика не подавляет span;
- admission полного набора атрибутов через сгенерированный allow-list до
  каждого испускающего OTel-вызова, классификацию ошибок без утечки PII и
  ограниченную кардинальность.

Эти четыре адаптера испускают ровно шесть сигналов со статусом `implemented`:
command span/duration, storage span, cache operation count, cache facade event и
cache backend event. Остальные 53 descriptor-а пока ничего не испускают, но их
включённые metric-инструменты создаются eager: сломанный provider останавливает
сборку приложения, а не теряется при первом запросе. `New` не регистрирует
observable callbacks; aggregate-регистрация cache memory остаётся отдельной
запланированной операцией.

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

Запланированный signal ID 5, `storage_operation_bytes`, имеет только успешные
варианты `put/ok` и `stage/ok`. Он будет записывать persisted size из
возвращённого `Info.Size` или `Staged.Info.Size`, никогда не будет оборачивать
или читать source и не будет заявлять измерение фактически прочитанных из
reader байтов.

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
Views, sampling, native instrumentation, flush и shutdown. Рабочий пример
stdout SDK находится в
[`_examples/otel-sdk-bootstrap`](../../../_examples/otel-sdk-bootstrap/).

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

service := port.ChainService[Product, string, Product](
    baseService,
    vvotel.Service[Product, string, Product](telemetry),
)

store := storage.Chain(
    baseStore,
    vvotel.Store(telemetry),
)

cacheRuntime.Observer = cache.MustObservers(
    existingObserver,
    vvotel.Cache(telemetry, vvotel.WithCacheSpanEvents(true)),
)

logger := slog.New(vvotel.TraceHandler(slog.NewJSONHandler(os.Stdout, nil)))
```

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
errors. Во время assembly `vvotel` не регистрирует callbacks, не откатывает
providers, не запускает goroutines и не владеет shutdown.

Для экспорта логов через OTel приложение само создаёт native-handler `otelslog`,
оборачивает его своей политикой редактирования и передаёт результат в
`vvotel.TraceHandler`. Поэтому redaction/export chain видит уже добавленную
корреляцию. Если нужна только native-семантика, приложение передаёт handler
`otelslog` напрямую; `vvotel` его не импортирует и не владеет его provider-ом.
