# otel (vvotel)

`github.com/frostgrove/vv/otel` (пакет `vvotel`) предоставляет декораторы и адаптеры OpenTelemetry для базовых интерфейсов фреймворка (`port.Service`, `storage.Store`, `cache.Observer`, `cachememory.Observer`).

Модуль импортирует только OpenTelemetry trace и metric API (GA). Он не зависит от SDK, экспортеров или мостов логирования, заимствует переданные провайдеры и оставляет настройку и завершение работы SDK приложению.

Текущий контракт — `ContractVersion = vv-otel/v1`, а instrumentation scope —
`github.com/frostgrove/vv/otel` с `ScopeVersion = v0.1.0`. Сгенерированный registry
является источником истины для имён, mapping-ов, privacy-классов, ограничений
кардинальности и migration metadata; см. [otel-spec.md](../otel-spec.md).

## Что вы получаете

- Фабрику `vvotel.New`, принимающую переданные `trace.TracerProvider` и `metric.MeterProvider`;
- Обобщённый middleware `vvotel.Service` для `port.Service`, создающий INTERNAL-спаны (`vv.command <op>`) и гистограмму длительности (`vv.command.duration`);
- Middleware `vvotel.Store` для `storage.Store`, создающий INTERNAL-спаны (`vv.storage <op>`);
- Наблюдатели терминальных событий `vvotel.Cache` и `vvotel.CacheMemory`, записывающие счетчик `vv.cache.operations` и опциональные события спанов;
- Безопасную закрытую схему, классификацию ошибок без утечки PII и ограниченную кардинальность.

`ResourceName` предназначен только для trace, необязателен и принимается только
как короткое логическое имя из букв, цифр, `.`, `_` и `-` (не более 64 байт).
Идентификаторы, ключи, URL, payload, сообщения и credentials не становятся
telemetry labels. Неизвестные значения cache и прикладные коды ошибок
опускаются.

Приложение владеет SDK exporters, readers/processors, flush и shutdown. Рабочий
пример stdout SDK находится в [`_examples/otel-sdk-bootstrap`](../../../_examples/otel-sdk-bootstrap/).

Для обслуживания используйте `make generate` и `make check-otel-schema`; второй
является read-only freshness gate. Команда `make version V=v0.1.0` обновляет
scope version в registry и сгенерированный исходник. Перед публикацией запустите
`make check-otel-consumer V=v0.1.0` в окружении, где доступны lockstep-теги root
и `otel`.

## Подключение

```go
telemetry, err := vvotel.New(vvotel.Config{
    TracerProvider: tracerProvider,
    MeterProvider:  meterProvider,
    ResourceName:   "products",
})
if err != nil {
    log.Fatal(err)
}

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
```
