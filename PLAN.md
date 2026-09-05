# План закрытия дыр OTEL REVIEW

Рабочий план исправления интеграции OpenTelemetry по результатам
`/home/user/ws/gd/lease/OTEL_REVIEW.md`.

## Правила выполнения

- [ ] Не считать OTel готовым к merge или публикации, пока не закрыты все P1.
- [ ] После каждого этапа запускать относящиеся к нему проверки.
- [ ] Не снимать OTel с live roadmap и не объявлять модуль consumable до прохождения release gate.
- [ ] Не добавлять SDK/exporter-зависимости в production-модуль `otel`.
- [ ] Не менять поведение бизнес-вызовов из-за сбоев telemetry.

## 0. Зафиксировать решения контракта

- [x] Зафиксировать полный vocabulary для `cache` и `cachememory` operations/outcomes.
- [x] Зафиксировать поведение неизвестных значений. Рекомендуемый вариант — не экспортировать их.
- [x] Добавить конечный allow-list для `errs.Code`; неизвестные коды не экспортировать.
- [x] Зафиксировать политику `ResourceName`: bounded declaration или отсутствие этого атрибута в metrics.
- [x] Зафиксировать поведение `nil`-результата middleware в `port.ChainService` и `storage.Chain`.
- [x] Зафиксировать канонический формат `ScopeVersion` и его связь с `V`.
- [x] Устранить преждевременное утверждение о готовности OTel в live roadmap и документации.

## 1. Закрыть schema и privacy escape hatches

Затронутые области: `internal/otelreg/registry.json`, `cmd/vv-otel-gen`,
`otel/schema_gen.go`, `otel/cache.go`, `otel/cachememory.go`, `otel/service.go`.

- [x] Расширить registry metadata: source, privacy class, cardinality bound,
  metric eligibility, unit, description, semconv metadata и migration metadata.
- [x] Добавить реальные operations для facade:
  `lookup`, `lookup_many`, `load`, `load_many`, `put`, `forget`.
- [x] Добавить реальные operations для memory backend:
  `get`, `get_many`, `put`, `delete`, `evict`, `reset`, `close`.
- [x] Описать все известные cache outcomes и правила mapping.
- [x] Добавить `error_codes` в registry.
- [x] Генерировать total mapping functions для cache operations/outcomes.
- [x] Генерировать allow-list/checker для error codes.
- [x] Перевести cache adapters с прямого `string(event.Operation)` и
  `string(event.Outcome)` на generated mappings.
- [x] Экспортировать `vv.error.code` только после allow-list проверки.
- [x] Сделать duration boundaries приватными либо возвращать защитную копию.
- [x] Проверить, что свободные `ResourceName`, `WithServiceResource` и
  `WithStorageResource` не создают неограниченную metric cardinality.

### Тесты schema/privacy

- [x] Exhaustive-тест всех известных facade enum-ов.
- [x] Exhaustive-тест всех известных memory enum-ов.
- [x] Canary-тест неизвестных operation/outcome.
- [x] Canary-тест `errs.Code("tenant-secret-or-id")`.
- [x] Cardinality stress-тест для resource и всех metric attributes.
- [x] PII-canary-тест для IDs, keys, URLs, payloads, messages и credentials.
- [x] Проверить отсутствие `vv.error.code` в metrics.

## 2. Сделать telemetry fail-safe

Затронутые области: `otel/service.go`, `otel/storage.go`, `otel/cache.go`,
`otel/cachememory.go`.

- [x] Вынести вызовы injected OTel API в приватные panic-isolating helpers.
- [x] Защитить `Tracer.Start`.
- [x] Защитить начальные и финальные `SetAttributes`.
- [x] Защитить `SetStatus` и `Span.End`.
- [x] Защитить `Histogram.Record` и `Counter.Add`.
- [x] Защитить `Span.AddEvent`.
- [x] При panic во время setup вызвать business operation без telemetry.
- [x] При panic после business operation сохранить исходные result/error.
- [x] При panic business operation сохранить исходное panic-значение и re-panic-ить его.
- [x] В отдельной реализации `storage.Open` сохранить семантику span до возврата stream.
- [x] Передавать base attributes через `trace.WithAttributes` при `Start`, если они
  должны участвовать в head sampling.
- [x] Не менять семантику `runtime.Goexit` и `panic(nil)`.

### Тесты fail-safe

- [x] Provider panic в `Start` не подавляет business call.
- [x] Provider panic в `SetAttributes` не подавляет business call.
- [x] Provider panic в `SetStatus` не меняет result/error.
- [x] Provider panic в `End` не меняет result/error.
- [x] Provider panic в `Record` не меняет result/error.
- [x] Provider panic в cache `AddEvent` не ломает observer.
- [x] Provider panic в cache `Add` не ломает observer.
- [x] Business panic re-panics с тем же значением.
- [x] `runtime.Goexit` не превращается в `panic(nil)`.
- [x] Error identity сохраняется на всех обычных путях.

## 3. Исправить lifecycle и закрыть public API

Затронутая область: `otel/telemetry.go` и тесты public surface.

- [x] При `Config.Disabled=true` не вызывать `TracerProvider.Tracer`.
- [x] При `Config.Disabled=true` не вызывать `MeterProvider.Meter`.
- [x] При `Config.Disabled=true` не создавать instruments.
- [x] Проверять typed-nil providers, а не только `interface == nil`.
- [x] Удалить публичные `Tracer`, `Meter`, `CommandDuration`, `CacheOperations`.
- [x] Оставить provider и instrument handles приватными для adapters.
- [x] Реализовать явную активацию signals в `Config` либо другую модель,
  гарантирующую отсутствие unused instrument registration.
- [x] Сохранить независимость command traces, command metrics, storage traces
  и cache metrics.
- [x] Cache observers должны проверять global disabled state до любого signal.
- [x] Внешний recording span не должен обходить `Disabled`.

### Тесты lifecycle/API

- [x] Panic-counting providers не вызываются при `Disabled=true`.
- [x] Disabled cache facade не добавляет span event.
- [x] Disabled cache memory observer не добавляет span event.
- [x] Service-only не регистрирует cache instrument.
- [x] Storage-only не регистрирует command/cache instruments.
- [x] Cache-only не регистрирует command histogram.
- [x] All-signals регистрирует только явно выбранные instruments.
- [x] Public API test не допускает возвращаемые tracer/meter/instrument handles.

## 4. Исправить точечную корректность

- [x] В `classifyCommandError` проверять `errors.Is(err, crud.ErrStaleVersion)`
  до `port.KindOf`.
- [x] Покрыть raw `crud.ErrStaleVersion`.
- [x] Покрыть wrapped `crud.ErrStaleVersion`.
- [x] В `port.ChainService` не сохранять предыдущий слой при `nil`-результате.
- [x] В `storage.Chain` не сохранять предыдущий слой при `nil`-результате.
- [x] Учесть typed-nil base, middleware и result.
- [x] Зафиксировать выбранную nil-семантику в тестах и документации.
- [x] Удалить лишнюю пустую строку в конце `cache/cachememory/observer.go`.

## 5. Сделать generator воспроизводимым и проверяемым

Затронутые области: `cmd/vv-otel-gen`, `internal/otelreg`, `otel/doc.go`,
`scripts/modules.sh`, `scripts/checks.sh`, `Makefile`.

- [x] Сортировать все map keys перед генерацией.
- [x] Стабилизировать порядок вложенных declarations.
- [x] Стабилизировать формат чисел и generated output.
- [x] Валидировать обязательные registry fields.
- [x] Валидировать неизвестные значения и конфликтующие generated identifiers.
- [x] Генерировать runtime metadata и mappings из registry.
- [x] Добавить `//go:generate` для OTel schema generator.
- [x] Добавить read-only режим `-check`.
- [x] Сделать `-check` использующим тот же rendering path, что и генерация.
- [x] Подключить freshness gate к `make check`.
- [x] Подключить freshness gate к `make release` до создания tags.
- [x] Добавить deterministic golden test.
- [x] Добавить тест, что `-check` не меняет файлы.
- [x] Добавить тест stale generated output.
- [x] Добавить тест invalid registry.

### Критерии generator gate

- [x] 20 последовательных запусков дают одинаковый SHA-256.
- [x] `schema_gen.go` byte-for-byte воспроизводится из registry.
- [x] Изменение registry меняет соответствующий runtime output.
- [x] Stale generated output блокирует check.

## 6. Связать ScopeVersion с release workflow

Затронутые области: `registry.json`, generator, `scripts/modules.sh`,
`scripts/release.sh`, script tests.

- [x] Удалить ручное постоянное значение `1.0.0` как источник истины.
- [x] Сделать `ScopeVersion` производным от версии OTel-модуля.
- [x] Оставить `ContractVersion = vv-otel/v1` независимым schema identifier.
- [x] Проверять совпадение `V`, `otel/go.mod`, generated `ScopeVersion`, root tag
  и `otel/$V` tag.
- [x] Заблокировать release при несовпадении версий.
- [x] Покрыть version/release workflow script tests.

## 7. Закрыть release и consumer gate

- [x] Обновить `otel/go.mod` на реальную lockstep-версию root.
- [x] Обновить `otel/go.sum` после фиксации версии.
- [x] Сохранить first-party checksums в `otel/go.sum`; локальный tidy-gate временно
  исключает их только на время directory `replace` и восстанавливает файл.
- [x] Удалить `replace` из публикуемого `otel/go.mod`.
- [x] Добавить внешний consumer fixture без `go.work` и локальных `replace`.
- [x] Consumer должен проверять service-only, storage-only и cache-only импорты.
- [ ] Прогнать `cd otel && GOWORK=off go test ./...` без replace через published proxy;
  candidate file-proxy gate проходит, но публичных тегов пока нет.
- [x] Прогнать `go list -m all` в consumer fixture через candidate proxy; published
  proxy execution остаётся обязательным после тегирования.
- [x] Сохранить проверку, что root и остальные published modules OTel-free.
- [x] Проверить dependency graph и объяснить неожиданные edges через `go mod why`.
- [x] Прогнать `govulncheck`, race и vet для OTEL и полного workspace release workflow.
  Dependency-diff остаётся ручной проверкой изменения графа.
- [x] Проверить set-equality между discovered workspace modules и `go.work`.

Текущее известное состояние: strict `GOWORK=off` gate и внешний consumer gate
не проходят до публикации lockstep root/`otel` tags (`v0.1.0`); локальный
replace не считается закрытием consumer gate.

## 8. Сделать SDK example действительно runnable

Затронутая область: `_examples/otel-sdk-bootstrap`.

- [x] Добавить trace exporter и `SpanProcessor`.
- [x] Добавить metric `Reader` и exporter.
- [x] Передать providers в `vvotel.New`.
- [x] Выполнить service, storage и cache операции.
- [x] Вывести или проверить минимум один exported span и одну metric observation.
- [x] Проверить instrumentation scope name/version.
- [x] Сделать явный flush/shutdown.
- [x] Не вызывать shutdown borrowed providers внутри `vvotel`.
- [x] Не использовать OTel globals в example.
- [x] Оставить SDK/exporter dependencies только в `_examples/go.mod`.

## 9. Финальный documentation handoff

Выполнять только после прохождения всех технических и release gates.

- [x] Обновить `docs/api/surface.md` после изменения public API.
- [x] Обновить English/Russian OTel module docs.
- [x] Добавить example в example index.
- [x] Добавить generated schema reference.
- [x] Описать migration policy и distinction `ScopeVersion`/`ContractVersion`.
- [x] Описать `make version`, `make generate`, freshness check и consumer gate.
- [x] Добавить changelog/release notes.
- [x] Вернуть OTel item в live roadmap до фактического закрытия либо
  удалить его только после выполнения полного DoD.
- [x] Отдельно обозначить deferred scope: logs, jobs propagation, repository
  spans и outbox/messaging.

## Финальная последовательность проверок

- [x] `go test ./...` в root module.
- [x] `go test -race ./...`.
- [x] `go vet ./...`.
- [x] `make generate` не изменяет generated files после повторного запуска.
- [x] generator golden/freshness check проходит.
- [x] privacy/cardinality suite проходит.
- [x] disabled/provider panic suite проходит.
- [ ] `cd otel && GOWORK=off go test ./...` проходит без replace через published proxy;
  candidate file-proxy gate проходит, но публичных тегов пока нет.
- [x] `_examples` build, vet, test и запуск проходят с наблюдаемым export.
- [x] `git diff --check` проходит.
- [x] root и non-OTel modules не содержат OTel dependency.
- [ ] Только после всех пунктов обновить roadmap и объявить модуль готовым.

## Верификация плана

- [x] OTEL REVIEW проверен по текущим исходникам.
- [x] Три независимых сабагента подтвердили P1/P2 и порядок работ.
- [x] Подтверждена недетерминированность generator повторными запусками.
- [x] Подтверждено падение strict `GOWORK=off` consumer gate.

## Верификация реализации

- [x] После реализации проведены независимые clean-context аудиты кода и release workflow.
- [x] Подтверждены race/vet, schema freshness/determinism, privacy/cardinality,
  panic isolation, `panic(nil)`/`Goexit`, disabled lifecycle и typed-nil chains.
- [x] SDK example реально экспортирует spans, metrics и instrumentation scope.
- [x] Release workflow останавливается до tag/push на strict module или consumer gate.
- [ ] Переисполнить strict module и consumer gate после публикации lockstep tags
  `v0.1.0`; до этого `unknown revision` является внешним blocker.
