# Frostgrove: механики фреймворка и magic-first DX

Framework `dev/ai-improvements` · `c93886680a96502a67d126a20249466b5e07d3ab` · 2026-09-08. Предложения, не реализованный API; соседние CLI/templates вне оценки.

Настройки принадлежат модулям, их сборка — приложению; готовые recipes сокращают проводку, не вводя общий Options, обязательный DI или замену native SDK. [Архитектурная граница](2026-09-01-extension-architecture-roadmap.md).

DX — требование к использованию, не фильтр возможностей. Новые карточки с `[ ]` не выполнены: пункт 4 отделяет существующие детали от недостающего механизма. CMS-механики исключены по запросу владельца. Event sourcing вынесен в [невыполненные приложения своего roadmap](2026-09-01-postgres-event-sourcing-roadmap.md#research-appendices-2026-09-08).

Собственный workflow engine не планируется. Durable workflows, signals, deterministic replay и совместимость исполняемой истории принадлежат выбранному движку, например Temporal; Frost подключает native SDK. Batches и порционная обработка ниже имеют ограниченный контракт и не превращаются в интерпретатор процессов.

Разделы: [сборка](#c) · [API и клиенты](#a) · [jobs/эксплуатация](#p) · [runtime-гарантии](#r) · [доступ и секреты](#s).

<a id="c"></a>

## C. Сборка и интеграции

<a id="dx-c01"></a>

### DX-C01. Один каталог, несколько profiles

1. Механизм: [Fx Provide](https://github.com/uber-go/fx/blob/d5da5b04ac906bfbad8b400baeee9b970c1be6f3/provide.go) создаёт результат по запросу потребителя, а Frost отбирает конструкторы каталога по profile.
2. Зачем: API, worker и seed используют один список модулей.
3. Адаптация: profile исключает проводку роли, сохраняя compiled imports.
4. Уже есть: [FL-030](../ai/flows/FL-030-a-module-definition-becomes-a-running-deployment.md) — каталог, profiles, `appfx.Options`; [FL-032](../ai/flows/FL-032-a-package-tree-becomes-a-confirmed-module.md) — генерация списка конструкторов; недостаёт доставки contributions потребителям (C05).
5. DX: передать каталог в `appfx.Options(catalog, module.Serving)` и подключить owners.

<a id="dx-c02"></a>

### DX-C02. Явный объект заменяет default

1. Механизм: Spring Boot [включает defaults условно](https://docs.spring.io/spring-boot/reference/features/developing-auto-configuration.html): `@ConditionalOnMissingBean` уступает пользовательскому bean, property-условия проверяют настройки, bean-проверки зависят от порядка регистрации.
2. Зачем: собственный pool подавляет открытие стандартного.
3. Адаптация: до создания ресурсов и `fx.New` явный объект подавляет default factory, отключение пропускает factory, ошибка выбранной factory завершает сборку.
4. Уже есть: [FL-021](../ai/flows/FL-021-a-configuration-becomes-a-connection.md) — фабрики соединений, [FL-030](../ai/flows/FL-030-a-module-definition-becomes-a-running-deployment.md) — profiles; недостаёт provider-примера с подавлением default.
5. DX: передать pool или constructor точной Go-сигнатуры в настройки сборки.

<a id="dx-c03"></a>

### DX-C03. Два pool одного типа

1. Механизм: [Fx annotations](https://github.com/uber-go/fx/blob/d5da5b04ac906bfbad8b400baeee9b970c1be6f3/annotated.go) связывают одноимённые результаты и параметры одинакового типа.
2. Зачем: добавить reporting pool, сохранив остальные зависимости.
3. Адаптация: root назначает `primary`/`reporting` через wrappers. Общий pool передаётся одним указателем: одинаковый DSN не объединяет экземпляры и транзакции.
4. Уже есть: [FL-009](../ai/flows/FL-009-transactions-joining-opening-which-database.md) — transaction source identity, [FL-030](../ai/flows/FL-030-a-module-definition-becomes-a-running-deployment.md) — annotations; недостаёт примера двух concrete values с точным соответствием consumers.
5. DX: сохранить `NewReports(database *sql.DB)`, выбор pool и владельца shutdown оставить root.

<a id="dx-c04"></a>

### DX-C04. Config с происхождением полей

1. Механизм: Frost [D-086](../ai/decisions/D-086-a-configuration-is-a-tree-and-its-provenance-is-part-of-the-result.md) возвращает typed config и provenance, Spring [ConditionEvaluationReport](https://github.com/spring-projects/spring-boot/blob/5893438a3abd89ecab1a1c970df7dace670ee60d/core/spring-boot-autoconfigure/src/main/java/org/springframework/boot/autoconfigure/condition/ConditionEvaluationReport.java) — причины выбора.
2. Зачем: объяснять overrides и проверять config до использования.
3. Адаптация: приложение проверяет требования profile и связывает правило выбора provider с field origin без config values и native objects.
4. Уже есть: [FL-026](../ai/flows/FL-026-a-file-becomes-a-validated-configuration.md) — strict loading, `ValidateTree`, provenance; недостаёт связи с provider choices и отметки неиспользованного profile блока.
5. DX: передать проверенные поля из `vvcfg.LoadFrom[Config](source)` конструкторам модулей.

<a id="dx-c05"></a>

### DX-C05. Generated worker доходит до supervisor

1. Механизм: ленивому [Fx Provide](https://github.com/uber-go/fx/blob/d5da5b04ac906bfbad8b400baeee9b970c1be6f3/provide.go) нужны annotation результата в группу и supervisor, который её получает и запускает.
2. Зачем: подтверждение worker в manifest должно приводить к `Run`.
3. Адаптация: отдельный путь raw constructors оборачивает активные kinds заданными root wrappers: worker → `runtimefx.AsRunner`, route → transport wrapper, seeder → `appfx.AsSeeder`, check → `healthfx.AsCheck`, provider → без обёртки. Начать с generator marker result; уже аннотированные значения и неоднозначный native `fx.Out` оставить в ручном пути.
4. Уже есть: [FL-032](../ai/flows/FL-032-a-package-tree-becomes-a-confirmed-module.md) генерирует raw symbols, [FL-030](../ai/flows/FL-030-a-module-definition-becomes-a-running-deployment.md) передаёт их в `fx.Provide`, [FL-028](../ai/flows/FL-028-a-background-activity-becomes-a-supervised-runner.md) содержит ручной binding/supervisor. Worker probe получил generated `constructed=0/runners=0`, manual `1/1`: недостаёт binding и регрессии `vv generate module` → `Run`.
5. DX: подтвердить worker один раз; сегодня работают ручные `runtimefx.AsRunner(NewExporter)` и `runtimefx.Auto()`.

<a id="dx-c06"></a>

### DX-C06. Recipe готовит подключение библиотеки

1. Механизм: Flex [Recipe](https://github.com/symfony/flex/blob/4a6d98eea3ebc7f68d82810cb682eedca2649e99/src/Recipe.php) описывает подключение, [Lock](https://github.com/symfony/flex/blob/4a6d98eea3ebc7f68d82810cb682eedca2649e99/src/Lock.php) хранит его версию отдельно от библиотеки.
2. Зачем: получать редактируемую проводку выбранного stack одним patch.
3. Адаптация: installer сохраняет версию и исходные outputs шаблона с точными SDK calls, config и dependencies. Повторное применение проверяет владение: совпадение с authored file требует разрешить конфликт.
4. Уже есть: [FL-032](../ai/flows/FL-032-a-package-tree-becomes-a-confirmed-module.md) и [FL-029](../ai/flows/FL-029-a-model-becomes-a-public-wire-body.md) — manifests, fingerprints, drift checks и защита outputs своих генераторов; недостаёт recipe installer/lock, контракт сторонних recipes (inputs, файлы, совместимость) открыт.
5. DX: выбрать известную сборку и применить diff application code.

<a id="dx-c07"></a>

### DX-C07. Recipe обновляется поверх локальных правок

1. Механизм: Flex [RecipePatcher](https://github.com/symfony/flex/blob/4a6d98eea3ebc7f68d82810cb682eedca2649e99/src/Update/RecipePatcher.php) объединяет старый шаблон, текущие файлы и новый шаблон трёхсторонним patch.
2. Зачем: обновлять проводку, сохраняя локальные изменения.
3. Адаптация: хранить исходные bytes для merge base. File eject снимает управление recipe. Изменения defaults обозначаются даже при чистом merge.
4. Уже есть: [FL-032](../ai/flows/FL-032-a-package-tree-becomes-a-confirmed-module.md) — confirmations, drift, защита authored outputs; недостаёт сохранённой базы, three-way merge и file eject.
5. DX: проверить diff, разрешить конфликты и забрать нужные файлы под своё управление.

<a id="dx-c08"></a>

### DX-C08. Adapter работает отдельно

1. Механизм: [Symfony Components](https://symfony.com/doc/current/components/using_components.html) потребляются отдельно; Frost adapter принимает готовые значения, DI satellite связывает их с контейнером.
2. Зачем: использовать компонент со своим stack.
3. Адаптация: recipe и ручной путь используют общий публичный constructor адаптера.
4. Уже есть: [storageminio](../modules/ru/storageminio.md) принимает native client, [D-074](../ai/decisions/D-074-a-container-binding-is-a-satellite.md) закрепляет самостоятельность adapter; недостаёт сопоставленных примеров одной операции в сборке и внешнем consumer со своим `go.mod`.
5. DX: передать client в `storageminio.New`, backend — в `storage.New`, store — сервису.

<a id="dx-c09"></a>

### DX-C09. Объяснение выбора и потерянного worker

1. Механизм: Spring [ConditionEvaluationReport](https://github.com/spring-projects/spring-boot/blob/5893438a3abd89ecab1a1c970df7dace670ee60d/core/spring-boot-autoconfigure/src/main/java/org/springframework/boot/autoconfigure/condition/ConditionEvaluationReport.java) сохраняет исходы условий и причины выбора.
2. Зачем: различать исключённую роль, потерянный вклад и ошибку runner.
3. Адаптация: статический отчёт читает descriptor/choices без constructors. Binding сравнивает ожидаемые contributions с группой, supervisor сообщает состояние. Неизвестная native-проводка даёт «нет сведений».
4. Уже есть: [FL-030](../ai/flows/FL-030-a-module-definition-becomes-a-running-deployment.md) — `Describe`/`Doctor`, [FL-028](../ai/flows/FL-028-a-background-activity-becomes-a-supervised-runner.md) — supervisor; недостаёт сопоставления contributions/группы и symbol/`Runner.Name()`, поскольку `Active` означает лишь выбор роли.
5. DX: увидеть «выбран worker: 1, supervisor получил: 0» без config values и native objects.

<a id="dx-c10"></a>

### DX-C10. Adapter и SDK делят один client

1. Механизм: [storageminio](../modules/ru/storageminio.md) получает client приложения; [Fx Replace](https://github.com/uber-go/fx/blob/d5da5b04ac906bfbad8b400baeee9b970c1be6f3/replace.go) concrete value не заменяет автоматически interface registrations.
2. Зачем: вызывать SDK-specific операции без второго client.
3. Адаптация: root передаёт один client adapter и native consumer. Смена backend затрагивает SDK-код и требует проверки семантики. Общий transaction source не включает ORM hooks/builders ([D-017](../ai/decisions/D-017-orm-go-side-behaviour-does-not-run.md)).
4. Уже есть: storageminio принимает client, Fx binding предоставляет client/backend, [FL-009](../ai/flows/FL-009-transactions-joining-opening-which-database.md) описывает transaction source; недостаёт native/facade-примера и сравнения двух adapters по нужным операциям.
5. DX: получить `storage.Store` и `*minio.Client` параметрами конструктора.

<a id="dx-c11"></a>

### DX-C11. Cleanup принадлежит создателю ресурса

1. Механизм: Fx [lifecycle tests](https://github.com/uber-go/fx/blob/d5da5b04ac906bfbad8b400baeee9b970c1be6f3/internal/lifecycle/lifecycle_test.go) закрепляют обратный shutdown и отсутствие `OnStop` после ошибки собственного `OnStart`.
2. Зачем: освобождать собственные ресурсы после сбоев, сохраняя borrowed clients.
3. Адаптация: неуспешные factory/`OnStart` очищают частичное приобретение сами. Ресурс, открытый до `Start`, сразу отдаёт сборке однократный cleanup для graph failure, startup failure и остановки: одного `OnStop` при ошибке constructor в `fx.New` недостаточно. Сетевые фазы задаёт библиотека. Создание bucket включается явно.
4. Уже есть: [FL-021](../ai/flows/FL-021-a-configuration-becomes-a-connection.md) — pool lifecycle, [FL-028](../ai/flows/FL-028-a-background-activity-becomes-a-supervised-runner.md) — supervisor, [storageminio](../modules/ru/storageminio.md) — borrowed client и `BucketOnDemand`; SQL binding недостаёт cleanup при graph failure до `Start`.
5. DX: «мой pool» закрывает приложение, «создай pool» — создающая сборка, ровно один раз.

<a id="a"></a>

## A. Операции, публичные контракты и клиенты

HTTP — optional интеграция Huma с auth, errs и DTO Frost; сервисы, ORM, DI и native API выбирает приложение.

<a id="dx-a01"></a>

### DX-A01. Go-функция становится HTTP API

1. Механизм: [Huma Register](https://github.com/danielgtaylor/huma/blob/ad801e09f5cad8f531ea8d17f31f1649ec4355bd/huma.go#L779) из method/path и typed handler строит OpenAPI, декодирует и проверяет запрос, вызывает функцию и сериализует ответ.
2. Зачем: публиковать согласование или расчёт, сохраняя прямой вызов бизнес-сервиса.
3. Адаптация: satellite связывает policy с HTTP enforcement/access declaration, ошибки — с A03; общие бизнес-правила остаются в сервисе.
4. Уже есть: [RouteSet](../../app/http/appfiber/routeset.go) и [port.Service](../../port/service.go); добавить binding функции с policy для [surface verifier](../../auth/http/authhttp/surface.go).
5. DX: `Approve` + Huma input/output + POST с policy.

<a id="dx-a02"></a>

### DX-A02. DTO сохраняет public shape и presence

1. Механизм: [Huma schema hooks](https://github.com/danielgtaylor/huma/blob/ad801e09f5cad8f531ea8d17f31f1649ec4355bd/schema.go#L747) описывают Go-типы/custom scalars, [FastAPI](https://github.com/fastapi/fastapi/blob/50113da16fec53b66b80d75e80a89296de4fa5a5/fastapi/routing.py#L301) проверяет output по response model.
2. Зачем: PATCH различает отсутствие/null/zero, ответ содержит только поля public DTO.
3. Адаптация: согласовать decoder/schema/serializer для `Opt[T]`, отвергая неоднозначные scalar defaults из-за [Huma IsZero](https://github.com/danielgtaylor/huma/blob/ad801e09f5cad8f531ea8d17f31f1649ec4355bd/huma.go#L2151) до доказанного presence-aware пути; hooks для запрета unknown/duplicate keys и output validation ещё не подтверждены.
4. Уже есть: [Opt](../../utils/optional.go), [wire mapper](../../crud/wire/wire.go), [public bodies](../ai/decisions/D-105-the-persistence-patch-and-the-public-patch-body-are-two-types.md); добавить Huma mapping и проверки новых операций.
5. DX: public PATCH DTO → `ProfileView` с сохранённым presence.

<a id="dx-a03"></a>

### DX-A03. Прежние ошибки в Huma

1. Механизм: по модели [Kratos](https://github.com/go-kratos/kratos/blob/668db92c2c001e9552594ba5a8aede8456af6d7e/errors/errors.go) transport проецирует `errs.Fault`, validator выдаёт `errs.Violation`, отделяя machine code от cause.
2. Зачем: клиент сохраняет code/public path при смене HTTP engine.
3. Адаптация: у Huma `ErrorDetail` нет keyword, поэтому bridge даёт общий validation code/public path без разбора message и раскрытия value/cause/metadata; при [глобальных error factories](https://github.com/danielgtaylor/huma/blob/ad801e09f5cad8f531ea8d17f31f1649ec4355bd/error.go#L245) instance-local hook для business/validation/decode/negotiation errors и status/schema ещё не доказан, при его отсутствии нужен upstream seam либо другой engine.
4. Уже есть: [renderer](../../port/porthttp/render.go), [status projection](../../port/porthttp/errors.go), [FL-011](../ai/flows/FL-011-an-error-becomes-an-http-status.md); добавить Huma error/validator adapter и schema прежнего envelope без изменения globals.
5. DX: сервис возвращает прежний sentinel/`errs.Fault`.

<a id="dx-a04"></a>

### DX-A04. OpenAPI из регистрации маршрутов

1. Механизм: [Huma Register](https://github.com/danielgtaylor/huma/blob/ad801e09f5cad8f531ea8d17f31f1649ec4355bd/huma.go#L779) создаёт handler и operation вместе, [offline export](https://huma.rocks/tutorial/client-sdks/) отдаёт OpenAPI внешним инструментам.
2. Зачем: docs и SDK описывают выбранные маршруты без запуска сервера и БД.
3. Адаптация: общая export/mount регистрация обходится без runtime dependencies и выводит security из A01 policy, не подменяя permissions OAuth scopes. Native handlers описываются явно, method/path сверяются с router table, но [authnet.Surface](../../auth/http/authnet/surface.go) видит только свои регистрации. Понижение OpenAPI сохраняет schema constraints.
4. Уже есть: [access verifier](../../auth/http/authhttp/surface.go), [resource operations](../ai/decisions/D-113-a-resource-states-its-mounted-operations-as-one-set.md), RouteSet; добавить export/surface comparison и CRUD projection существующих public bodies/operations.
5. DX: одна регистрация → OpenAPI → docs UI/SDK generator.

<a id="dx-a05"></a>

### DX-A05. SDK штатным генератором

1. Механизм: [Huma SDK recipe](https://huma.rocks/tutorial/client-sdks/) передаёт OpenAPI и native config внешнему generator, получая types и client methods.
2. Зачем: вызывать `ApproveLease` с подсказками типов через свой HTTP client и credentials hook.
3. Адаптация: проверить по одному Go/browser generator на optional/nullable, custom scalars и A03 errors, сохраняя raw response/unknown codes в optional decoder. Cancellation/auth идут через native seams без добавленного Frost retry POST/refresh с повтором mutation; version/config/outputs учитывает A11.
4. Уже есть: [remote](../../remote/resource.go) для CRUD и [remotehttp](../../remote/remotehttp/transport.go) с client/request hooks; добавить OpenAPI recipes и optional error companion, сохранив закрытый CRUD method set.
5. DX: OpenAPI → штатный generator → typed вызов.

<a id="dx-a06"></a>

### DX-A06. Собственный typed Proto/gRPC

1. Механизм: [Kratos generator](https://github.com/go-kratos/kratos/blob/668db92c2c001e9552594ba5a8aede8456af6d7e/cmd/protoc-gen-go-http/http.go#L65) и [template](https://github.com/go-kratos/kratos/blob/668db92c2c001e9552594ba5a8aede8456af6d7e/cmd/protoc-gen-go-http/httpTemplate.tpl) превращают `.proto` service/annotations в typed handlers с named messages.
2. Зачем: подключить auth/errors Frost к своему gRPC stack и native clients.
3. Адаптация: consumer генерирует `.pb.go` и монтирует auth/error bridges, сохраняя presence/compatibility в `.proto`, права по full method name и отдельную stream policy; [D-052](../ai/decisions/D-052-a-grpc-resource-carries-documents-not-a-schema.md) сохраняет документный CRUD и запрещает `protoc` в framework build targets, поэтому amendment нужен только для framework-owned Proto generation.
4. Уже есть: [crudgrpc](../../crud/rpc/crudgrpc/service.go) со Struct и [authgrpc](../../auth/rpc/authgrpc/interceptor.go); добавить consumer-service recipe и errs projection, named messages получают отдельный wire contract.
5. DX: generated service interface + свой `grpc.Server` + Frost interceptors.

<a id="dx-a07"></a>

### DX-A07. Native SSE и WebSocket

1. Механизм: [Huma SSE](https://github.com/danielgtaylor/huma/blob/ad801e09f5cad8f531ea8d17f31f1649ec4355bd/sse/sse.go#L85) получает input/event types/producer, строит schema и пишет SSE framing.
2. Зачем: отдавать прогресс и UI updates с существующими auth и typed input.
3. Адаптация: проверять access/input до headers и undeclared event перед записью; после headers ошибка означает terminal event/close. Disconnect отменяет producer, очередь ограничена; replay принадлежит source, WebSocket protocol — native библиотеке, session/message policy — приложению.
4. Уже есть: [RouteSet](../../app/http/appfiber/routeset.go) и [authgrpc.Stream](../../auth/rpc/authgrpc/interceptor.go); добавить SSE/WebSocket recipe или satellite и проверить router writers реальным socket/slow reader.
5. DX: SSE event types + producer; WebSocket native handler.

<a id="dx-a08"></a>

### DX-A08. Предметный поиск и CRUD query

1. Механизм: [query.Request](../../crud/query/request.go) разбирает DSL под ограничениями [server profile](../../port/service.go), typed endpoint A01 вызывает предметный native query.
2. Зачем: публичный поиск получает нужные параметры, resource API сохраняет generic filter/sort/preload.
3. Адаптация: выводить generic schema/helpers из public query profile с серверными ограничениями fields/operators/preload/page size и policy scope; предметный endpoint использует native backend без выдуманного total.
4. Уже есть: [page](../../crud/page.go), [cursor](../../crud/cursor.go), typed specs/DSL/profiles; добавить OpenAPI projection разрешённого query и пример предметного endpoint.
5. DX: `AvailableLeasesQuery` → native query → `LeasePage`.

<a id="dx-a09"></a>

### DX-A09. OIDC и одноразовые auth-сценарии

1. Механизм: [go-oidc](https://github.com/coreos/go-oidc) проверяет ID token после OAuth code exchange, mapper связывает `(issuer, subject)` с local subject, [SessionIssuer](../../auth/access/access.strategy.go) выдаёт credential; source/test pin ещё нужен.
2. Зачем: корпоративный вход использует прежние permissions, `me` и logout.
3. Адаптация: связать attempt с браузером/provider/state/nonce/PKCE, проверить redirect/[ID token](https://openid.net/specs/openid-connect-core-1_0.html#IDTokenValidation) по [OAuth BCP](https://datatracker.ietf.org/doc/html/rfc9700#section-2.1) и local active до issuance без linking по email. One-time completion с issuance/failure/rollback ещё проектируется; local logout/refresh не управляют provider. Reset/verify/magic-link/invite используют expiring token, связанный с purpose/subject: consume и локальное действие атомарны, delivery выполняет выбранный SDK.
4. Уже есть: [access runtime](../../auth/access/access.runtime.go), [HTTP flows](../../auth/access/http/accesshttp/accesshttp.go), [Active check](../../auth/access/usecase.login.go); добавить OIDC begin/callback и одноразовые сценарии с caller-owned mapping/stores по [D-066](../ai/decisions/D-066-access-owns-no-identity-and-no-route.md).
5. DX: provider + subject mapper → begin → callback → local session.

<a id="dx-a10"></a>

### DX-A10. Короткий тест настоящего HTTP binding

1. Механизм: [humatest New/Wrap](https://github.com/danielgtaylor/huma/blob/ad801e09f5cad8f531ea8d17f31f1649ec4355bd/humatest/humatest.go#L319) проводит запрос через router/API, [FastAPI overrides](https://github.com/fastapi/fastapi/blob/50113da16fec53b66b80d75e80a89296de4fa5a5/fastapi/dependencies/utils.py#L617) заменяют зависимость, сохраняя route/validation pipeline.
2. Зачем: проверять HTTP input, policy и error response с fake SDK без повторения bootstrap.
3. Адаптация: production constructors получают fake напрямую либо через native DI replacement, тест использует production регистрацию; готовый principal проверяет policy, но не authentication/login.
4. Уже есть: [Recorder](../../crud/crudtest/recorder.go), [transport matrix](../../test/portmount/mount_test.go), [Verify](../../auth/http/authhttp/surface.go); добавить runnable A01 setup с fake, denied request, renderer и независимым generated-client вызовом.
5. DX: fake → production registration → POST → status/DTO/code/path.

<a id="dx-a11"></a>

### DX-A11. Проверяемая генерация контрактов

1. Механизм: [resource manifest](../../internal/codegen/manifest.go) связывает generator/package owner, public narrowing и fingerprint подтверждённого widening; принцип распространяется на DTO → OpenAPI → SDK.
2. Зачем: получать воспроизводимый diff, защищая handwritten файлы и публичную поверхность.
3. Адаптация: из explicit A01 registrations и native A05 config генерировать и проверять temporary outputs для замены owned files; частичную запись обнаруживает drift check, повторная генерация восстанавливает outputs, SDK получает выбранную public schema.
4. Уже есть: [RunResource](../../internal/codegen/resource.go), [wire coverage](../../crud/wire/cover.go), [FL-029](../ai/flows/FL-029-a-model-becomes-a-public-wire-body.md); добавить drift/ownership checks OpenAPI/SDK, сохранив resource manifest format и решение A06 для framework Proto outputs.
5. DX: изменить DTO/регистрацию → export/generate → diff; optional check stale/foreign outputs.

<a id="a12"></a>

### [ ] A12. Сосуществование версий API

1. Механизм: [NestJS versioning](https://docs.nestjs.com/techniques/versioning) направляет запрос к регистрации выбранной версии. Каждая версия сохраняет свой публичный контракт.
2. Зачем: выпустить новый response shape, пока старые мобильные клиенты продолжают работать.
3. Адаптация: начать с native URI registrations `/v1` и `/v2`, отдельных DTO/presenters/operation IDs и явных converters к общему сервису. Обе регистрации проходят access gate; OpenAPI/SDK экспортируются по версии. Удаление старой версии явно объявляется, version dispatch в `port` или CRUD не добавляется.
4. Уже есть: [RouteSetSpec.Prefix](../../app/http/appfiber/routeset.go) позволяет смонтировать два prefix; A04/A05 предлагают контракт/SDK. Нет законченного multi-version recipe с проверкой старого SDK против нового deployment.
5. DX: v1 DTO + v2 DTO + общий service → две регистрации → два контракта.

<a id="a13"></a>

### [ ] A13. Consumer contracts проверяют совместимость поведения

1. Механизм: [Spring Cloud Contract](https://docs.spring.io/spring-cloud-contract/reference/getting-started/introducing-spring-cloud-contract.html) проверяет producer по контрактам потребителей; Go recipe использует готовый [Pact verifier](https://docs.pact.io/implementation_guides/go).
2. Зачем: корректно сгенерированный новый сервер ещё может сломать прежнего клиента изменением ответов или ошибок.
3. Адаптация: тестовое приложение поднимает production registration, задаёт provider states и проверяет сохранённые consumer artifacts штатным verifier. Зафиксировать нужные code/path/presence/null expectations и версии сторон. Проверка доказывает только заявленные contracts; для собственного Proto A06 добавить native [Buf breaking](https://buf.build/docs/breaking/) с выбранным уровнем wire/source compatibility.
4. Уже есть: [manifest/drift checks](../../internal/codegen/manifest.go), [roundtrip tests](../../remote/roundtrip_test.go), A10/A11. Нет consumer-contract fixture; воспроизводимость генерации не доказывает совместимость.
5. DX: прежний consumer contract → production test server + fixtures → native verifier.

<a id="p"></a>

## P. Фоновые задачи и эксплуатация

<a id="dx-p01"></a>

### DX-P01. Typed job и результат постановки

1. Механизм: typed payload с wire-именем/revision проходит существующий jobs binding; [Laravel](https://github.com/laravel/framework/blob/0aa8d7ecd7e5d887e1603d5f8608cab667b4a3be/src/Illuminate/Queue/Queue.php) даёт краткий dispatch.
2. Зачем: объявлять handler один раз и получать ID для tracking.
3. Адаптация: binding принимает producer intent и возвращает ID, сохраняя `Go` → error и `Stager(db, tx)` → `Staged`. SQL enqueue в бизнес-транзакции того же source — единственный outbox. ID ещё не означает commit. Redis не разделяет SQL atomicity. Доставка at-least-once/unordered; внешний эффект требует стабильного idempotency key.
4. Уже есть: [Binding.Go](../../jobs/jobsfx/binding.go), [enqueue](../../jobs/queue.go), [Stager](../../jobs/jobspg/stager.go), [SQL outbox](../ai/decisions/D-118-a-transactional-enqueue-is-the-outbox.md); добавить короткий результат и пример commit/rollback.
5. DX: payload → enqueue → ID; commit подтверждает transaction owner.

<a id="dx-p02"></a>

### DX-P02. Календарные jobs

1. Механизм: [robfig/cron](https://github.com/robfig/cron) вычисляет окно, Frost превращает schedule/revision/time в durable intent; [Laravel](https://github.com/laravel/framework/blob/0aa8d7ecd7e5d887e1603d5f8608cab667b4a3be/src/Illuminate/Console/Scheduling/ManagesAttributes.php) разделяет single-server запуск и запрет overlap.
2. Зачем: запускать jobs по местному времени и восстанавливать пропуски после простоя.
3. Адаптация: выбрать skip/latest/bounded catch-up и поведение при DST-пропусках/повторах. Preview использует тот же вычислитель. Повтор окна сохраняет intent, смена revision — прежние IDs. Межрепличный overlap требует durable lease running job и fencing/idempotency при потере lease.
4. Уже есть: [At/FixedEvery и occurrence intents](../../jobs/schedule.go), [локальный sweep](../../runtime/periodic.go); добавить calendar binding, misfire policy, затем backend-supported overlap.
5. DX: job + «09:00, Asia/Almaty» + latest-only + preview дат.

<a id="dx-p03"></a>

### DX-P03. История и условный redrive

1. Механизм: failed summary ведёт к истории и следующему поколению той же invocation с сохранением attempts/outcome; источник действия — [Laravel retry](https://github.com/laravel/framework/blob/0aa8d7ecd7e5d887e1603d5f8608cab667b4a3be/src/Illuminate/Queue/Console/RetryCommand.php).
2. Зачем: повторять job после инцидента, сохраняя историю.
3. Адаптация: мигрировать records к stable ID + generation. Summary pagination ограничивает записи/bytes без payload/ledger; payload читается отдельно с лимитом. Redrive атомарно проверяет права, terminal state, expected generation и дедуплицирует operator intent. Purge сохраняет producer dedup retention.
4. Уже есть: [Admin](../../jobs/redrive.go), но [PostgreSQL](../../jobs/jobspg/admin_repo.go) заменяет record прежнего ID; добавить summaries, retained generations и conditional redrive endpoint.
5. DX: failed summaries → история → redrive с причиной; повтор возвращает прежний результат, устаревшее поколение — conflict.

<a id="dx-p04"></a>

### DX-P04. Typed notifications

1. Механизм: routing/locale renderer превращает typed данные в native отправку либо jobs по recipient/channel, как у [Laravel](https://github.com/laravel/framework/blob/0aa8d7ecd7e5d887e1603d5f8608cab667b4a3be/src/Illuminate/Notifications/NotificationSender.php).
2. Зачем: менять provider при сборке и повторять только неудачный канал.
3. Адаптация: начать с email template и preview. Durable отправка использует текущую jobs transaction boundary. Retry сохраняет template revision/locale и перепроверяет consent при необходимости. Потерянный ответ разрешается provider idempotency contract; acceptance не доказывает доставку.
4. Уже есть: [jobs placement](../../jobs/queue.go) и [streaming attachments](../../storage/store.go); добавить rendering/routing и один provider binding с обычным jobs handler.
5. DX: `InvoiceReady` + recipient + typed данные + mail transport; preview через тестовый channel.

<a id="dx-p05"></a>

### DX-P05. Webhooks

1. Механизм: native verifier проверяет raw bytes по протоколу [Stripe](https://docs.stripe.com/webhooks); исходящая job подписывает bytes и повторяет прежний event ID.
2. Зачем: переживать повтор callback и timeout отправки.
3. Адаптация: проверять bounded raw body до decoding. Dedup по provider/account/event ID сверяет digest; marker и jobs intent commit-ятся вместе до ACK. Dedup retention независим от signature window. Endpoint закреплён за subscription revision; redirects/адрес проверяются до передачи private body. Приложение разрешает out-of-order по revision/provider state.
4. Уже есть: [EnqueueOnce](../../jobs/queue.go) и [SQL placement](../../jobs/jobspg/driver.go); добавить verifier/raw-body endpoint и durable recipe, отправку собрать из jobs и native `http.Client`.
5. DX: verifier + разрешённые events + billing route; исходящая job получает event ID/subscription revision.

<a id="dx-p06"></a>

### DX-P06. Read-through cache recipes

1. Механизм: fresh возвращается сразу, miss вызывает loader, stale обновляется по policy — как у [Laravel remember/flexible](https://github.com/laravel/framework/blob/0aa8d7ecd7e5d887e1603d5f8608cab667b4a3be/src/Illuminate/Cache/Repository.php).
2. Зачем: выбирать freshness и поведение при перегрузке без настройки каждого limit.
3. Адаптация: recipes задают freshness, negative TTL, loader deadline и saturation через текущие profiles; override сохраняет остальные limits.
4. Уже есть: [Hot/Warm/Durable/Disabled и Profile.With](../../cache/policy.go), [Resolve](../../cache/resolve.go), [memo](../../cache/memo.go); добавить recipes stale catalog, negative lookup и execution memo.
5. DX: typed cache + profile/freshness → `Resolve(loader)`.

<a id="dx-p07"></a>

### DX-P07. Upload completion

1. Механизм: owner-bound session выдаёт upload URL/headers, как [Laravel S3](https://github.com/laravel/framework/blob/0aa8d7ecd7e5d887e1603d5f8608cab667b4a3be/src/Illuminate/Filesystem/AwsS3V3Adapter.php), и публикует объект после completion.
2. Зачем: загружать файл до сохранения формы и публиковать проверенные bytes.
3. Адаптация: session хранит owner/stage/expiry/limits/result и координирует completion/cleanup/promote. Size/checksum/scanner проверяют публикуемую неизменяемую source version. SQL сохраняет jobs publication intent для восстановления promote после crash. SDK подписывает direct upload; filesystem использует server-mediated путь.
4. Уже есть: [Stage/Promote/Abort/CleanupExpired](../../storage/store.go); добавить session/completion, signing и source-version verification, поскольку [IfMatch](../../storage/types.go) относится к destination.
5. DX: session → URL/route + headers → upload → completion; повтор возвращает прежний file reference.

<a id="dx-p08"></a>

### DX-P08. Парная tenant jobs wiring

1. Механизм: [enqueue](../ai/flows/FL-033-a-request-becomes-a-tenant-bound-statement.md) запечатывает [record-bound tenant identity](../ai/decisions/D-119-a-durable-token-binds-the-record-not-the-queue.md); worker проверяет seal и текущие tenant lifecycle/epoch перед handler.
2. Зачем: согласовать tenant wiring разделённых API/worker deployments.
3. Адаптация: `tenancyjobs` строит capture/restore из одного authority. Каждый deployment получает свою половину; локальная проверка не доказывает конфигурацию удалённого worker.
4. Уже есть: [capture/restore и guards](../../tenancy/tenancyjobs/jobs.go), [независимый core](../ai/decisions/D-116-one-tenancy-extension-and-its-core-costs-no-seam.md); добавить парную сборку и API/worker пример.
5. DX: один authority → capture producer + restore worker.

<a id="dx-p09"></a>

### DX-P09. Jobs telemetry

1. Механизм: observer переводит claim/recover/renew/apply в OTel metrics/spans; [Aspire](https://github.com/dotnet/aspire/blob/1dd4584e3df56f5544a3e7f5fda8aa767f92318e/tests/Aspire.Hosting.Tests/Dashboard/ResourcePublisherTests.cs) даёт snapshot/updates для локального просмотра activation/health/jobs.
2. Зачем: обнаруживать остановившийся claim и исчерпанный admission.
3. Адаптация: OTel adapter использует synchronous jobs fan-out. SDK владеет export/buffering/shutdown; async exporter ограничивает очередь и считает потери. Labels исключают payload/signed URLs/tenant IDs. Optional viewer читает projections и P03; временный buffer ограничен bytes/age.
4. Уже есть: [Telemetry](../../otel/telemetry.go), [WorkerObserver](../../jobs/worker_observer.go), [fan-out](../../jobs/worker_observers.go); добавить jobs mapping в [telemetry schema](../modules/otel-spec.md), затем viewer.
5. DX: свои OTel providers → jobs observer; локально смонтировать read-only view.

<a id="dx-p10"></a>

### DX-P10. Lifecycle и health recipes

1. Механизм: [Supervisor](../ai/flows/FL-028-a-background-activity-becomes-a-supervised-runner.md) запускает role runners и ждёт drain; [health checks](../ai/flows/FL-027-a-dependency-becomes-a-health-answer.md) учитываются с required/degrading importance.
2. Зачем: запускать workers выбранной роли, отражать failure и соблюдать shutdown budget.
3. Адаптация: recipe связывает runner/probe contributions с ролью, importance и finite drain grace.
4. Уже есть: [Supervisor](../../runtime/supervisor.go), [runtimefx](../../runtime/runtimefx/runtimefx.go), [jobs roles](../../jobs/jobsfx/jobsfx.go), [healthfx](../../health/healthfx/healthfx.go); добавить API-only/worker fixtures с startup failure, drain deadline и required/degrading checks.
5. DX: role + runner + check importance + shutdown grace.

<a id="dx-p11"></a>

### DX-P11. Native client resilience

1. Механизм: [Failsafe-go](https://failsafe-go.dev/) оборачивает native вызов admission/deadline/retry/breaker policies; [go-zero](https://github.com/zeromicro/go-zero/blob/84c92d710b9f2ae11c3cbcee242cea40eec42e70/core/breaker/breaker.go) исключает application refusal из breaker failures.
2. Зачем: ограничить concurrency и суммарные сетевые попытки SDK/jobs retries.
3. Адаптация: read-only recipe фиксирует порядок policies, wire attempt limit с учётом SDK и общий deadline с backoff/`Retry-After`. Permit удерживается до завершения SDK call даже после timeout ожидающего. SDK должен поддерживать cancellation. Mutation повторяет replayable body с прежним provider intent по idempotency contract; unknown outcome требует reconciliation, включая смену provider. Длительными retries владеет один слой.
4. Уже есть: [remotehttp](../../remote/remotehttp/) и [delivery retries](../../jobs/disposition.go); добавить Failsafe-go recipe с проверкой attempts/deadline/error mapping, binding выделить при повторении wiring.
5. DX: policy composition при сборке, обычный native method в handler.

<a id="dx-p12"></a>

### DX-P12. Dev ресурсы и native SDK recipes

1. Механизм: recipe получает endpoint либо создаёт ресурс через [Testcontainers](https://golang.testcontainers.org/), ждёт readiness и собирает native client; [Aspire](https://github.com/dotnet/aspire/blob/1dd4584e3df56f5544a3e7f5fda8aa767f92318e/src/Aspire.Hosting/ResourceBuilderExtensions.cs) разделяет reference и started/healthy/completed.
2. Зачем: запускать примеры и integration tests с готовыми ресурсами/clients.
3. Адаптация: cleanup закрывает owned ресурсы, Stop сохраняет persistent volumes; migration ждёт completion. Search recipe соединяет native client с jobs reindex, task completion и защитой от stale revision; payment recipe — native SDK, stable intent и verifier P05. Temporal recipe подключает native client/worker lifecycle: [Signals/Queries/Updates](https://docs.temporal.io/develop/go/message-passing) и история исполнения остаются движку, без Frost workflow API.
4. Уже есть: [Module Doctor](../../app/module/doctor.go), [catalog](../../app/module/catalog.go), [Supervisor](../../runtime/supervisor.go); добавить container [_examples](../../_examples/) и независимый пример native [Temporal SDK](https://docs.temporal.io/develop/go), выделяя optional module при повторении wiring.
5. DX: Testcontainers PostgreSQL либо внешний DSN → readiness → native pool + `jobspg`.

<a id="dx-p13"></a>

### DX-P13. Batches и continuation

1. Механизм: конечный batch учитывает terminal acknowledgements однократно и по условию завершения ставит continuation job; [Laravel](https://github.com/laravel/framework/blob/0aa8d7ecd7e5d887e1603d5f8608cab667b4a3be/src/Illuminate/Bus/Batch.php) даёт counters/callbacks.
2. Зачем: после reindex/экспорта отправлять одно уведомление с результатами участников.
3. Адаптация: ограничить membership по числу/bytes и закрепить batch intent. Последний terminal update и continuation intent commit-ятся атомарно либо восстанавливаются по durable intent. Cancellation останавливает незапущенных участников без отмены внешних эффектов. Processed отличается от succeeded.
4. Уже есть: [jobs state/intents](../../jobs/) и [enqueue-outbox](../ai/decisions/D-118-a-transactional-enqueue-is-the-outbox.md); после history P03 добавить batch coordination rows, сохранив jobs в существующей очереди.
5. DX: конечные payloads + одна continuation → succeeded/failed/cancelled; workflows с signals/replay — native engine P12.

<a id="r"></a>

## R. Runtime-гарантии и сетевые протоколы

<a id="r01"></a>

### [ ] R01. Идемпотентность входящей команды

1. Механизм: [Stripe](https://docs.stripe.com/api/idempotent_requests) сохраняет результат выполнения: повтор с прежними ключом и параметрами получает тот же ответ, другое содержимое — конфликт.
2. Зачем: потерянный ответ на создание заказа не приводит к созданию второго заказа.
3. Адаптация: command-owner связывает tenant/actor/operation/key с fingerprint и bounded result. Резервирование ключа сериализует конкурирующие запросы; SQL-изменение и результат commit-ятся вместе. Replay проверяет текущий доступ. Retention задаёт окно гарантии; произвольный внешний SDK effect требует собственного idempotency contract.
4. Уже есть: [EnqueueOnce](../../jobs/queue.go) дедуплицирует enqueue; [Create](../../port/service.go) выполняется заново. Нет command result store/replay. Это запись результата команды, не второй outbox.
5. DX: операция + fingerprint + SQL result store; `Idempotency-Key` → сохранённый результат либо conflict/in-progress.

<a id="r02"></a>

### [ ] R02. Лимиты и доли воркеров по tenant

1. Механизм: [Oban Pro](https://oban.pro/docs/pro/1.6.9/Oban.Pro.Engines.Smart.html) применяет распределённые лимиты к partitions по аргументам job; burst использует свободную мощность. Borrowed mechanism — partition limits, строгие fair shares ниже — отдельное предложение Frost.
2. Зачем: тысяча exports одного клиента не занимает всех воркеров остальных клиентов.
3. Адаптация: jobs backend атомарно резервирует active attempts по доверенному partition key; задаются доля, потолок и допустимое заимствование свободной мощности. Scheduling policy ограничивает голодание активных partitions. Лимит lease-владельцев не ограничивает уже начавшиеся внешние calls после потери lease: для них нужен downstream admission.
4. Уже есть: [invocation Partition](../../jobs/invocation.go), [binding concurrency](../../jobs/worker_plan.go), [round-robin bindings](../../jobs/workers_run.go). Нет tenant-aware [claim](../../jobs/delivery_driver.go) и fleet-wide fair allocation.
5. DX: queue + partition selector + share/ceiling + burst policy; ядро не знает типа tenant.

<a id="r03"></a>

### [ ] R03. Входящие rate limits и ограничение перегрузки

1. Механизм: [ASP.NET Core](https://learn.microsoft.com/en-us/aspnet/core/performance/rate-limit?view=aspnetcore-10.0) разделяет частоту и in-flight concurrency по partitions; [Quarkus load shedding](https://quarkus.io/guides/load-shedding-reference/) добавляет адаптивный admission при перегрузке.
2. Зачем: дорогой endpoint не должен исчерпывать соединения БД, даже когда клиент формально укладывается в requests/minute.
3. Адаптация: optional HTTP middleware использует готовый limiter; доверенный identity key, bounded waiting/partition cardinality, явные local/shared backend и failure policy. Permit удерживается до фактического завершения handler. Transport различает quota refusal и saturation, выдаёт применимый `Retry-After`; adaptive policy выбирается отдельно от readiness. Объёмную DDoS фильтрует внешняя инфраструктура.
4. Уже есть: [login AttemptLimiter](../../auth/access/access.protection.go), [jobs admission](../../jobs/admission.go) и cache limits. Общего API limiter нет.
5. DX: route + identity partition + rate/in-flight limits + native backend; необязательная adaptive policy.

<a id="r04"></a>

### [ ] R04. HTTP ETag и атомарный If-Match

1. Механизм: [Django conditional processing](https://docs.djangoproject.com/en/dev/topics/conditional-view-processing/) использует validators для 304 и отказа от операции над устаревшим представлением.
2. Зачем: не пересылать неизменившийся ответ и не затереть изменение, случившееся после открытия формы.
3. Адаптация: HTTP owner определяет validator представления с учётом locale/scope. Write-owner атомарно сравнивает клиентскую expected revision при mutation; отдельные «прочитать ETag → обычный Update» оставляют гонку. Авторизация проверяется до условного ответа; слабый read validator не используется как strong write precondition.
4. Уже есть: [optimistic locking](../../crud/sqlrepo/version_test.go) проверяет версию repository-read. [Команды](../../port/command.go) и [HTTP handler](../../crud/http/crudnet/handler.go) не проводят клиентский `If-Match` до mutation.
5. DX: GET → ETag; PATCH + `If-Match` → обновление либо 412; read + `If-None-Match` → 304.

<a id="r05"></a>

### [ ] R05. Realtime channels и presence между репликами

1. Механизм: [Phoenix Presence](https://phoenix.hexdocs.pm/Phoenix.Presence.html) отслеживает участников topic; Go [Centrifuge](https://github.com/centrifugal/centrifuge) уже предоставляет channels, scale-out, presence и ограниченное восстановление после reconnect.
2. Зачем: live dashboard или чат работает между API-репликами, а не только внутри одного socket handler.
3. Адаптация: recipe подключает native server; приложение авторизует subscribe/publish и задаёт поведение при истечении session или отзыве доступа. Очереди и recovery window ограничены; за окном нужен resync. Presence приблизителен и не является бизнес-фактом; временный буфер сообщений — не event store.
4. Уже есть: [RouteSet](../../app/http/appfiber/routeset.go); A07 покрывает SSE/WebSocket transport. Channels/presence/scale-out не реализованы.
5. DX: native channel config + access callbacks → subscribe/updates/presence → reconnect либо resync.

<a id="r06"></a>

### [ ] R06. Возобновляемая обработка порциями

1. Механизм: [Spring Batch](https://docs.spring.io/spring-batch/reference/step/chunk-oriented-processing/configuring.html) обрабатывает chunk и сохраняет прогресс на границе commit.
2. Зачем: выгрузка или backfill после падения продолжает с подтверждённой позиции, а не с начала.
3. Адаптация: jobs recipe фиксирует input identity/snapshot, typed cursor и предел порции. SQL effect + checkpoint сохраняются одной fenced transaction; внешние effects требуют idempotency/reconciliation. Новый handler читает последний checkpoint. Это одна повторяемая job, без workflow graph, signals, execution history или интерпретатора.
4. Уже есть: [InFencedTx](../../jobs/jobspg/fencer.go), [cursor](../../crud/cursor.go). [Job progress](../../jobs/invocation.go) отмечает живость, не позицию входа; P13 считает участников batch. Прикладного chunk checkpoint нет.
5. DX: immutable input + reader(cursor, limit) + chunk handler; restart читает сохранённый cursor.

<a id="r07"></a>

### [ ] R07. Сохраняемая пауза выдачи jobs

1. Механизм: [BullMQ](https://docs.bullmq.io/guide/workers/pausing-queues) различает глобальную паузу очереди и локального worker; уже взятые jobs завершаются.
2. Зачем: на время инцидента остановить новые отправки интеграции, сохранив jobs и работающие процессы.
3. Адаптация: jobs backend хранит pause revision; выдача новых attempts через Claim и Recover сериализована с pause state. Recovery продолжает reconciliation/отзыв прежних attempts, но не начинает новую доставку. Resume проверяет expected revision. Пауза не отменяет выданные до неё attempts и внешние effects.
4. Уже есть: [held admission](../../jobs/admission.go), [локальный Drain](../../jobs/workers_run.go), [Cancel/Terminate invocation](../../jobs/control.go). Нет durable administrative pause/resume для всего binding.
5. DX: pause binding + причина → revision; resume + expected revision.

<a id="r08"></a>

### [ ] R08. Возобновляемая передача файла

1. Механизм: [tus](https://tus.io/protocols/resumable-upload) хранит подтверждённый upload offset: клиент узнаёт позицию и продолжает PATCH с неё, а не передаёт весь файл заново.
2. Зачем: большой upload переживает обрывы мобильной сети.
3. Адаптация: готовый tus server владеет protocol/offset storage; owner-bound session ограничивает bytes/expiry и сериализует конкурирующие PATCH. Completion передаёт неизменяемый результат в P07, expired partial uploads очищаются. `storage.Store` не реализует собственный HTTP-протокол.
4. Уже есть: [Stage/Put/Promote/Abort](../../storage/store.go) для целого потока. В P07 есть предложение completion, но нет возобновляемой передачи.
5. DX: начать upload → tus URL → resume → completion P07.

<a id="r09"></a>

### [ ] R09. Request-scoped DataLoader

1. Механизм: [dataloadgen](https://github.com/vikstrous/dataloadgen) собирает независимые Load(key) в один bulk вызов. Fetch adapter обязан сопоставить результаты исходным keys: через mapped loader либо явную перестановку по ID, не порядок SQL rows.
2. Зачем: параллельные resolvers или сборщики DTO не делают N запросов к одному SDK/БД.
3. Адаптация: native loader создаётся на request/attempt, имеет предел batch size/wait/bytes. Tenant/access scope не смешивается, missing/error сопоставляются каждому ключу. После mutation очищается затронутый request memo; singleton с результатами пользователей запрещён.
4. Уже есть: [batched preloads](../../crud/preload.go), [ResolveMany](../../cache/resolve_many.go) для заранее известных keys и [execution memo](../../cache/memo.go). Нет автоматического объединения независимых Load.
5. DX: свой bulk loader + request scope → отдельные Load(key) → native bulk call.

<a id="r10"></a>

### [ ] R10. Shared tag invalidation без возврата старого fill

1. Механизм: [Symfony cache tags](https://symfony.com/doc/current/cache.html#using-cache-tags) связывают entries с зависимостями. Для Frost дополняем это проверкой поколения, действующей между процессами.
2. Зачем: изменение товара инвалидирует связанные ответы; loader другой реплики не возвращает старые данные в действующий cache после invalidation.
3. Адаптация: shared backend фиксирует generations до load и условно публикует результат только для них; reads проверяют актуальные generations. Утрата metadata означает miss, прежний generation ID не переиспользуется. Гарантия начинается с подтверждённой invalidation; SQL commit и изменение внешнего cache сами по себе не атомарны.
4. Уже есть: [CAS/TagInvalidator SPI](../../cache/capability.go); [mutation fence](../../cache/mutation.go) и [текущий контракт](../modules/en/cache.md) ограничены процессом. Нужен shared backend/profile с atomic generation contract, не повторная реализация локального `Resolve`.
5. DX: entry + dependency tags → invalidate tag; distributed guarantee доступна только у поддерживающего backend.

<a id="r11"></a>

### [ ] R11. Ограниченное исполнение native GraphQL

1. Механизм: [NestJS](https://docs.nestjs.com/graphql/complexity) и [gqlgen](https://gqlgen.com/reference/complexity/) оценивают query cost до запуска resolvers, учитывая размножение списков.
2. Зачем: поддержать GraphQL без запроса, превращающего вложенные выборки в неограниченную работу.
3. Адаптация: native gqlgen schema/resolvers, limits стоимости/depth/input плюс pagination, deadline и response bounds в реальном исполнении. Permission действует на resolver/query, не только на общий endpoint. Оценка cost не заменяет ограничение native SQL/SDK вызова; GraphQL из persistence model автоматически не выводится.
4. Уже есть: [CRUD query profile](../../crud/query/request.go), [service limits](../../port/service.go), но не GraphQL binding. R09 используется для resolver batching.
5. DX: native schema/resolvers + budgets + auth/error projection + request loaders.

<a id="r12"></a>

### [ ] R12. Публичный ресурс долгой операции

1. Механизм: [Azure asynchronous request-reply](https://learn.microsoft.com/en-us/azure/architecture/patterns/asynchronous-request-reply) возвращает 202 с Location/Retry-After; отдельный status resource сообщает итог.
2. Зачем: клиент запускает экспорт, отключается и позже забирает результат без открытого HTTP-соединения.
3. Адаптация: operation-owner сохраняет public operation ID, owner и result reference; операция и существующий jobs enqueue commit-ятся вместе. Status/cancel проверяют доступ, результат имеет expiry. Operation row хранит публичное состояние, не вторую очередь исполнения. Запрос отмены не означает отмену уже совершённого effect.
4. Уже есть: [Admin Get/List](../../jobs/redrive.go), [Cancel](../../jobs/control.go), но их внутренний payload не является публичным DTO. P03 — операторская история; client operation resource отсутствует.
5. DX: POST → status URL; GET → progress/result; cancel → cancellation requested.

<a id="r13"></a>

### [ ] R13. Чтение и ссылка на конкретную версию файла

1. Механизм: [S3 GetObject(versionId)](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html) адресует сохранённую версию объекта, а не текущее содержимое ключа.
2. Зачем: выдавать именно проверенный или подписанный файл, даже после перезаписи того же key.
3. Адаптация: storage reference содержит backing/namespace/key и подтверждённую backend неизменяемую revision; операции проверяют caller scope. Пустая revision и [перезаписываемая S3 null version](https://docs.aws.amazon.com/AmazonS3/latest/userguide/AddingObjectstoVersionSuspendedBuckets.html) не подходят. Native read/signing выбирают точную версию либо отказывают, не выдают latest. Retention/удаление отдельны: ссылка не гарантирует вечного хранения. P07 связывает проверку с этой revision, не с ETag как якобы content hash.
4. Уже есть: [Info.Version](../../storage/types.go), но ReadOptions/TemporaryURLOptions не выбирают версию; [MinIO signing](../../storage/storageminio/backend.go) не передаёт versionId. Native SDK доступен уже сейчас; недостаёт согласованного facade contract.
5. DX: сохранить versioned reference → `Open(reference)` / signed URL именно этой версии; примерный API, не существующая перегрузка.

<a id="r14"></a>

### [ ] R14. Сохраняемый inbox уведомлений

1. Механизм: [Laravel database notifications](https://laravel.com/framework/docs/12.x/notifications#database-notifications) хранит уведомления и `read_at` отдельно от email/WebSocket доставки.
2. Зачем: пропущенное live-сообщение не исчезает; пользователь видит историю и непрочитанные записи.
3. Адаптация: notifications context хранит recipient, payload revision и read state; уникальный notification intent исключает повторный insert. Inbox и channel jobs одного SQL source commit-ятся вместе. Listing/mark-read ограничены recipient/tenant; «прочитать всё» фиксирует верхнюю границу, не захватывая новые записи. Read state не равен provider delivery state; целевой ресурс повторно проверяет доступ.
4. Уже есть: [ScopeSubject](../../crud/decorators/security/principal.go), [SQL stager](../../jobs/jobspg/stager.go), предложение отправки P04. Нет inbox storage/read API.
5. DX: typed notification + recipient → inbox; unread page → mark-read по ID либо фиксированной границе.

<a id="r15"></a>

### [ ] R15. Recipe резервирования конечного ресурса

1. Механизм: [Rails pessimistic locking](https://api.rubyonrails.org/classes/ActiveRecord/Locking/Pessimistic.html) выполняет проверку и изменение под row lock в транзакции. Применяем к reservation с отдельными confirm/cancel/expire состояниями.
2. Зачем: два клиента не резервируют последнее место одновременно; повтор запроса не уменьшает остаток второй раз.
3. Адаптация: прикладной context владеет capacity/reservation rows. Native SQL/ORM делает conditional decrement либо lock/check/update и insert с unique intent. Все writers соблюдают протокол, constraints защищают допустимые значения. Confirm/cancel/expire условны по текущему состоянию, освобождение однократное. Expiry job ставится той же SQL transaction; cache не решает наличие ресурса.
4. Уже есть: [InTx](../../crud/executor.go), [ForUpdate](../../crud/options.go), [transactional enqueue](../../jobs/jobspg/stager.go). Не новая транзакционная подсистема: недостаёт runnable recipe с конкурентными reserve/expiry/rollback.
5. DX: native `Reserve(resource, quantity, intent)` → reservation; `Confirm/Cancel`; expiry использует прежний reservation ID.

<a id="s"></a>

## S. Доступ, секреты и управление поведением

<a id="s01"></a>

### [ ] S01. Passkeys / WebAuthn

1. Механизм: [Spring Security Passkeys](https://docs.spring.io/spring-security/reference/servlet/authentication/passkeys.html) разделяет регистрацию credential и проверку подписанного challenge; в Go криптографию и протокол выполняет [go-webauthn](https://github.com/go-webauthn/webauthn).
2. Зачем: passwordless-вход и аппаратный фактор без собственного WebAuthn implementation.
3. Адаптация: native ceremony использует caller-owned subject/credential storage. Challenge одноразовый, ограничен временем, RP/origin и попыткой; добавление ключа требует подтверждённого владельца. Первый SQL-профиль атомарно расходует challenge через CAS, обновляет credential и выполняет issuance в одном source; active проверяется до Issue, ответ выходит после commit. Произвольные раздельные stores общей транзакции не получают.
4. Уже есть: [SessionIssuer](../../auth/access/access.strategy.go), [active check и выдача сессии](../../auth/access/usecase.login.go). Нет WebAuthn ceremonies/credential store; модель аккаунта остаётся приложению по [D-066](../ai/decisions/D-066-access-owns-no-identity-and-no-route.md).
5. DX: native WebAuthn config + subject/credential stores → register/login begin/finish → прежняя local session.

<a id="s02"></a>

### [ ] S02. MFA и повторное подтверждение опасной операции

1. Механизм: [Spring Security MFA](https://docs.spring.io/spring-security/reference/servlet/authentication/mfa.html) хранит подтверждённые факторы и требует их комбинацию для выбранной операции. Step-up требует свежего доказательства, даже если обычная сессия ещё действительна.
2. Зачем: просмотр профиля не требует повторного входа, смена платёжных реквизитов — требует.
3. Адаптация: auth хранит server-verified factor/time, связывает challenge с subject/session и, при подтверждении конкретного действия, digest команды. Recovery-код расходуется атомарно; reset факторов отзывает прежние доказательства. Политика различает отсутствие фактора и истёкшую свежесть; обычный login не выдаёт полный доступ до выполнения MFA.
4. Уже есть: [Principal.Attr](../../auth/principal.go), [Policy.Authorize](../../crud/decorators/security/security.go), [sessions](../../auth/access/access.model.go). Нет factor lifecycle/freshness policy; fallback нескольких authenticators не является MFA.
5. DX: policy «подтверждённый второй фактор не старше 5 минут» → challenge → повтор команды с доказательством.

<a id="s03"></a>

### [ ] S03. Права через связи объектов — ReBAC

1. Механизм: [OpenFGA](https://openfga.dev/docs/authorization-concepts) вычисляет доступ по связям user/group/folder/document; [версия authorization model](https://openfga.dev/docs/getting-started/immutable-models) фиксируется отдельно от изменяемых tuples.
2. Зачем: унаследовать доступ от папки или команды и дать исключение на один документ без роли на каждый объект.
3. Адаптация: optional auth adapter использует native SDK и pinned model ID. Отказ/недоступность не превращаются в разрешение. Для списков нужен отдельный permission-aware query plan: отфильтровать готовую страницу недостаточно для корректных total/cursor. Согласованность изменения SQL-данных и внешних tuples задаётся явно; общего commit нет.
4. Уже есть: [Scope/Inspect/Authorize](../../crud/decorators/security/security.go) и [ролевые grants](../../auth/access/access.model.go). Точки проверки доступны, но готового ReBAC adapter и стратегии permission-aware pagination нет.
5. DX: native FGA client + model ID + subject/object mapping → `can edit document`; listing подключается отдельным проверенным путём.

<a id="s04"></a>

### [ ] S04. Feature flags с устойчивым распределением пользователей

1. Механизм: [Unleash](https://docs.getunleash.io/concepts/activation-strategies) сочетает условия аудитории с постепенным включением; [stickiness](https://docs.getunleash.io/concepts/stickiness) удерживает пользователя в выбранной группе. [OpenFeature](https://openfeature.dev/docs/reference/concepts/evaluation-context/) даёт готовый контракт контекста оценки.
2. Зачем: включать новый алгоритм для части клиентов и отключать проблемный путь без redeploy.
3. Адаптация: native SDK получает проверенный targeting key, явные defaults и failure policy. Значение одного флага фиксируется на операцию; согласованный snapshot нескольких флагов требует поддержки provider. Если выбор изменяет необратимую команду, её принятое решение сохраняется с intent, а не переоценивается на каждом retry. Флаг не заменяет authorization.
4. Уже есть: [typed startup config](../../utils/vvcfg/vvcfg.go) и [durable job intents](../../jobs/queue.go). Runtime flag provider/targeting recipe нет.
5. DX: native client → typed flag с default → evaluate по доверенному контексту.

<a id="s05"></a>

### [ ] S05. Шифрование выбранных полей и ротация ключей

1. Механизм: [Rails Active Record Encryption](https://guides.rubyonrails.org/active_record_encryption.html) шифрует атрибуты перед хранением и читает старые ключи при ротации. Для Go выбирается готовая реализация, например [Tink AEAD](https://developers.google.com/tink/aead), а не своя криптография.
2. Зачем: дамп БД без ключей не раскрывает содержимое выбранных полей.
3. Адаптация: persistence codec хранит ciphertext/key revision, связывает associated data с tenant/resource/field; новый ключ используется для записи, старые — до завершения проверенной миграции и политики backup retention. Шифрованные поля исключены из generic filter/sort; поиск по ним требует отдельного явно выбранного механизма и модели утечек.
4. Уже есть: [публичные DTO](../../crud/wire/wire.go), но это не encryption. [D-017](../ai/decisions/D-017-orm-go-side-behaviour-does-not-run.md) запрещает рассчитывать на ORM hooks при vv SQL-вызове: recipe должен использовать native ORM-путь либо проверенный codec на всех разрешённых путях записи/чтения.
5. DX: typed field codec + key provider → обычная бизнес-модель; отдельная возобновляемая re-encrypt операция.

<a id="s06"></a>

### [ ] S06. Жизненный цикл короткоживущих credentials

1. Механизм: [Spring Cloud Vault](https://docs.spring.io/spring-cloud-vault/reference/advanced-topics.html#_lease_lifecycle_management_renewal_and_revocation) получает leased secrets, продлевает их до предела и отзывает принадлежащие процессу leases при завершении.
2. Зачем: работать с временными паролями БД и токенами вместо бессрочного секрета в config.
3. Адаптация: native SDK выполняет renew/revoke; обновление credentials использует native refresh hook. Замена client допустима только через явный resource-specific owner, закрепляющий экземпляр на операцию/транзакцию и дренирующий старый. Уже связанные с pool repositories без такого пути требуют controlled restart, не подмены переменной или повторной DI-инъекции. Borrowed resources не закрываются; expired credentials не сохраняются как fallback.
4. Уже есть: [startup loading](../../utils/vvcfg/vvcfg.go), [Supervisor](../../runtime/supervisor.go), ownership C11. Нет secret-lease recipe и безопасного переключения конкретного клиента; это не глобальный live-config/DI rebuild.
5. DX: native secret source + resource-specific renew/swap policy → работающий client; readiness отражает невозможность обновления.

<a id="s07"></a>

### [ ] S07. Управляемые API-токены для интеграций

1. Механизм: [Laravel Sanctum](https://laravel.com/framework/docs/12.x/sanctum#api-token-authentication) выдаёт несколько персональных токенов с abilities, expiry и отдельным отзывом; хранится hash, raw secret показывается при выпуске.
2. Зачем: выдать CI или партнёру доступ только на чтение и отозвать его без закрытия пользовательских сессий.
3. Адаптация: auth-owned store хранит owner, scopes, expires/revoked, last-used. Lookup проверяет active владельца; доступ требует и token scope, и текущего разрешения владельца. Scope проверяется независимо от выбора `Has`/ролей/Attrs/ReBAC: скопированная роль admin не даёт обход. Tenant boundary сохраняется. Listing не раскрывает secret; ротация/перекрытие явные, revocation-cache имеет оговорённую задержку либо отключён.
4. Уже есть: [apikey.Store/Authenticator](../../auth/apikey/apikey.go) принимает готовый lookup, [access sessions](../../auth/access/access.model.go) управляют входом. Нет готового PAT issuance/list/revoke и attenuation scopes.
5. DX: owner + «read reports» + expiry → token один раз; list metadata → revoke конкретного token ID.

## Очерёдность

C05: активация → A01–A05: операция, контракт, SDK → C06–C07: шаблон и обновление. Для API с mutations — R01/R03/R04; для background workloads — R02/R06/R07; остальные P/R/S подключаются под выбранный сценарий, не обязательным bundle.

Проверка DX: создать операцию, заменить зависимость, вызвать native SDK, обновить изменённый проект.

## Охват расширения

Добавлены 24 невыполненные карточки: A12–A13, R01–R15, S01–S07. Ещё 9 event-sourcing приложений находятся в отдельном roadmap. Это расширенный отбор, не заявление «все must-have найдены».

| Экосистема / источники | Что взято в этой ревизии |
|---|---|
| Java: Spring Security, Spring Batch, Spring Cloud Vault/Contract, Quarkus | Passkeys/MFA, chunk checkpoints, leased credentials, consumer contracts, load shedding |
| .NET: ASP.NET Core, Azure patterns | Partitioned admission, HTTP resource долгой операции |
| PHP: Symfony, Laravel | Shared invalidation, API tokens, notification inbox |
| Python / Ruby: Django, Rails | HTTP preconditions, field encryption, transactional reservation recipe |
| TypeScript / Go: NestJS, BullMQ, gqlgen, Centrifuge | API versions, queue pause, query budgets/loaders, realtime channels |
| Elixir: Oban Pro, Phoenix | Tenant partition limits, presence |
| Протоколы и готовые сервисы: Stripe, tus, S3, OpenFGA, Unleash/OpenFeature | Idempotency, upload resume, object versions, ReBAC, flags |

Не добавляли повторно: jobs dedup/debounce, SQL outbox, локальный supervisor, CRUD optimistic locking, bounded cache/SWR и CSRF — соответствующая база уже есть. Новая карточка описывает только недостающее отличие. Temporal остаётся native интеграцией P12; CMS исключены.

Отдельные планы: [i18n](2026-09-01-i18n-roadmap.md), [audit](2026-09-01-audit-log-roadmap.md), [event store](2026-09-01-postgres-event-sourcing-roadmap.md), [tenancy](2026-09-01-multitenancy-roadmap.md), [storage](2026-09-01-storage-roadmap.md).
