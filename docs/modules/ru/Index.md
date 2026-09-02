# Модули

По одной странице на импортируемый пакет: что он делает, что вы получаете и как его подключить.
Декларативно и практично — *почему* живёт в [decisions/](../../decisions/Index.md),
а *где в исходниках* — в [flows/](../../flows/Index.md).

Сначала прочитайте [README](../../../README.md), если ещё не читали. Эти страницы —
его развёрнутая версия.

## Практический старт

Если вы подключаете vv в приложение, идите в таком порядке:

1. [Репозиторий: от модели до сложного запроса](../../usage-guides/repository.md) —
   генерация, чтение, specs, запись и транзакции на одном примере.
2. [specs](specs.md) — отдельно и подробно про сгенерированные ProductAttrs и
   Product_, композицию фильтров и связи.
3. [crud/sqlrepo](sqlrepo.md) — настройки самого SQL-репозитория: лимиты,
   permanent scope, soft delete и реплики.

## Карта

```
                      your model struct
                              │
      ┌───────────────────────┼───────────────────────┐
      ▼                       ▼                       ▼
   crud ─────────────► crud/sqlrepo ──────────► decorators
   the contract        speaks SQL           specs · security · faults
      │                       │                       │
      │                       ▼                       │
      │                  adapter/*  ◄─────────────────┘
      │              crudsql · crudpgx
      ▼
   errs ──► sqlerr ──► sqlfault ──► catalog ──► probe
   the error contract, then the four layers that fill it

   query ──► port ──► porthttp ──► crudhttp ──► crudnet · crudfiber · crudgin
                 │          └──────────► authhttp ──► authnet · authgin · authfiber
                 └──► crudgrpc
   one request document, classified once, spelled per transport —
   and one status table, which is why porthttp is not under crud/

   remote ◄── remotehttp.Transport · crudgrpc.Transport
   the same thing backwards: another service's resource, as a repository

   vvdb ──► dbpgx · database/sql ──► crudsql · crudpgx
   one config file, then the handle the application hands over

   auth ──► authjwt · apikey ──► authnet · authgin · authfiber · authgrpc
                                            │
                                            ▼
                                    security.Gate
   who is calling, established once at the door and read by every policy

   app · module ──► appfx · appfiber ──► authhttp.Verify
   what main() assembles, and the gate that refuses to start when the router
   and the access declarations disagree
```

## Ядро — импортируется всегда

| Модуль | Импорт | Что это |
|---|---|---|
| [crud](crud.md) | `vv/crud` | Контракт: метаданные модели, `Opt`, опции, предикаты, связи, пагинация, шов исполнителя из двух методов |
| [crud/sqlrepo](sqlrepo.md) | `vv/crud/sqlrepo` | Обычный репозиторий. `Define`, `Bind` и слой, говорящий на SQL |

## Декораторы — оборачивают репозиторий, все опциональны

| Модуль | Импорт | Что это |
|---|---|---|
| [specs](specs.md) | `vv/crud/decorators/specs` | JPA Specifications, Criteria API и проверяемая на этапе компиляции метамодель |
| [security](security.md) | `vv/crud/decorators/security` | Скоуп на уровне строк, авторизация, проверка на уровне сущности |
| [faults](faults.md) | `vv/crud/decorators/faults` | Превращает один отклонённый write в полный список нарушений, вызванных payload |

## Auth — кто вызывает и что ему позволено

| Модуль | Импорт | Что это |
|---|---|---|
| [auth](auth.md) | `vv/auth` | Контракт: `Principal`, `Role`, `Permission`, `Credential`, `Authenticator`, `Guard`, ключ контекста, 401 |
| [authjwt](authjwt.md) | `vv/auth/authjwt` | **Модуль** — проверка JWT, generic по *вашей* структуре клеймов; HMAC, RSA, ECDSA, EdDSA, JWKS |
| [apikey](apikey.md) | `vv/auth/apikey` | `Authenticator` по общему секрету, сравнение за постоянное время |
| [authhttp](authhttp.md) | `vv/auth/http/authhttp` | HTTP-половина middleware: рендерер и отказ |
| [authnet](authnet.md) | `vv/auth/http/authnet` | Middleware для `net/http`. Стандартная библиотека, поэтому в составе библиотеки |
| [authgin](authgin.md) | `vv/auth/http/authgin` | **Модуль** — middleware для Gin |
| [authfiber](authfiber.md) | `vv/auth/http/authfiber` | **Модуль** — middleware для Fiber v3 |
| [authgrpc](authgrpc.md) | `vv/auth/rpc/authgrpc` | **Модуль** — unary- и stream-интерсепторы gRPC |
| [access](access.md) | `vv/auth/access` | **Модуль** — сессии, учётные данные, роли и права поверх семи таблиц, с ключом по субъекту, а не по пользователю |
| [accessjwt](accessjwt.md) | `vv/auth/access/accessjwt` | **Модуль** — подписанные access-токены поверх ротируемого refresh, с детекцией реплея |
| [revokeredis](revokeredis.md) | `vv/auth/access/accessjwt/revokeredis` | **Модуль** — список отзыва для него, в Redis |
| [accessnet](access.md) | `vv/auth/access/http/accessnet` | Роуты входа на `net/http`. Stdlib, поэтому едет внутри модуля access |
| [accessgin](access.md) | `vv/auth/access/http/accessgin` | **Модуль** — те же роуты на Gin |
| [accessfiber](access.md) | `vv/auth/access/http/accessfiber` | **Модуль** — те же роуты на Fiber v3 |
| [accessfx](access.md) | `vv/auth/access/accessfx` | **Модуль** — граф контекста, собранный в uber/fx |

`auth` намеренно **не** входит в манифест контрактов. Это пакет с двумя
реализациями собственного интерфейса — нормальный случай, а не исключение
([[D-048]], [[D-055]]).

## Storage — байты до и после фиксации формы

| Модуль | Импорт | Что это |
|---|---|---|
| [storage](storage.md) | `vv/storage` | Потоковый object-store contract, staged UI uploads и временные download-ссылки |
| [storagefs](storagefs.md) | `vv/storage/storagefs` | Безопасный stdlib filesystem backend и HMAC link handler |
| [storageminio](storageminio.md) | `vv/storage/storageminio` | **Модуль** — MinIO SDK backend и штатный pre-signed GET |
| [storageminiofx](storageminio.md) | `vv/storage/storageminio/storageminiofx` | **Модуль** — тот же backend, собранный в uber/fx, с проверкой бакета на старте |

## Cache — пересоздаваемые значения с ограниченной работой

| Модуль | Импорт | Что это |
|---|---|---|
| [cache](cache.md) | `vv/cache` | Типизированные декларации, profiles, schemas, локальная координация loaders, stale/negative results и жёсткий transient admission. Только stdlib |
| [cachememory](cachememory.md) | `vv/cache/cachememory` | Ограниченный process-local LRU backend с точным публичным charge accounting и batch reads |

## Запрос — один документ, четыре транспорта

| Модуль | Импорт | Что это |
|---|---|---|
| [query](query.md) | `vv/crud/query` | Wire DSL: один JSON-документ → `crud.Options`, с ограничениями для недоверенного ввода |
| [port](port.md) | `vv/port` | Транспортно-нейтральная половина: восемь команд, `Service`, `Mapper`, цепочка path |
| [porthttp](porthttp.md) | `vv/port/porthttp` | HTTP-проекция контракта ошибок: таблица статусов, конверт, шов `Renderer`, разбор тела. Общая для всех подсистем, не CRUD'а |
| [crudhttp](crudhttp.md) | `vv/crud/http/crudhttp` | То, что одновременно HTTP *и* CRUD: формы запроса, переход модели и форвардеры поверх `porthttp` |
| [wire](wire.md) | `vv/crud/wire` | Шов публичных тел: `PatchMapper`, `Presenter` и три проверки покрытия, которые держат тело честным |
| [crudnet](crudnet.md) | `vv/crud/http/crudnet` | Полный CRUD API на `net/http`. Stdlib, поэтому поставляется в библиотеке |
| [crudfiber](crudfiber.md) | `vv/crud/http/crudfiber` | **Модуль** — тот же API на Fiber v3 |
| [crudgin](crudgin.md) | `vv/crud/http/crudgin` | **Модуль** — тот же API на Gin |
| [crudgrpc](crudgrpc.md) | `vv/crud/rpc/crudgrpc` | **Модуль** — тот же API на gRPC, поверх `google.protobuf.Struct` |
| [remote](remote.md) | `vv/remote` | Потребляющая половина: ресурс другого сервиса, который держат как `port.Repository` |
| [remotehttp](remotehttp.md) | `vv/remote/remotehttp` | HTTP-транспорт клиента: `remote.Call` становится запросом |

## Подсистема ошибок — что проваленный write сообщает клиенту

| Модуль | Импорт | Что это |
|---|---|---|
| [errs](errs.md) | `vv/errs` | Контракт: `Code`, `Kind`, `Path`, `Violation`, `Fault`, SPI, каталоги сообщений. Только stdlib |
| [sqlerr](sqlerr.md) | `vv/errs/sqlerr` | Ошибка драйвера превращается в код. Четыре таблицы диалектов, три разных способа ключевания |
| [sqlfault](sqlfault.md) | `vv/crud/sqlfault` | Обход дерева, гейт целостности и сборка Fault. То, что принимает `WithFaults` |
| [catalog](catalog.md) | `vv/crud/catalog` | Интроспекция схемы по хэндлу, четыре диалекта. Читается один раз, дальше отвечает из памяти |
| [probe](probe.md) | `vv/crud/probe` | Один дополнительный запрос находит все *остальные* нарушения, вызванные тем же payload |

## Подключение — сторона приложения

| Модуль | Импорт | Что это |
|---|---|---|
| [vvdb](vvdb.md) | `vv/utils/vvdb` | Одна конфигурация → DSN или `*sql.DB` с настроенным пулом. Четыре движка, только stdlib |
| [dbpgx](dbpgx.md) | `vv/utils/vvdb/dbpgx` | **Модуль** — та же конфигурация, `*pgxpool.Pool` |

Из шва репозитория сюда не дотянуться: соединение открывает приложение и отдаёт
его адаптеру ниже ([[D-057]]). Оба лежат под `utils/` именно поэтому — это
обвязка приложения, а не подсистема библиотеки ([[D-058]]).

## Адаптеры — как vv добирается до вашей базы данных

| Модуль | Импорт | Что это |
|---|---|---|
| [crudsql](crudsql.md) | `vv/crud/adapter/crudsql` | `database/sql` — а значит и ent, gorm, sqlx, sqlc, bun, squirrel |
| [crudpgx](crudpgx.md) | `vv/crud/adapter/crudpgx` | **Модуль** — pgx v5, с массовой вставкой через `COPY` |
| [crudsqlfx](crudsql.md) | `vv/crud/adapter/crudsql/crudsqlfx` | **Модуль** — пул и source, собранные в uber/fx |
| [crudtest](crudtest.md) | `vv/crud/crudtest` | Источник в памяти: юнит-тестируйте репозиторий вообще без базы данных |

## Композиция — то, что собирает main()

| Модуль | Импорт | Что это |
|---|---|---|
| [app](app.md) | `vv/app` | Упорядоченная цепочка вкладов и команда сидов, которую не страшно запустить дважды. Только stdlib и `port` |
| [module](app.md) | `vv/app/module` | Что вносит ограниченный контекст, разложенное по ролям, и профиль деплоя, решающий, что из этого проводится, — описываемое, ничего не собирая. Только stdlib |
| [appfx](app.md) | `vv/app/appfx` | **Модуль** — каталог модулей под профилем деплоя, а также группа сидеров и runner в графе uber/fx |
| [appfiber](app.md) | `vv/app/http/appfiber` | **Модуль** — маршруты и middleware от модулей, которые не импортируют друг друга, смонтированные под одним префиксом, с гейтом доступа на старте, регистратором, у которого один вызов монтирует, объявляет и защищает операцию, и маршруты health |
| [health](health.md) | `vv/health` | Liveness, публичная проекция готовности и операторская проекция под аутентификацией — поверх проверок, важность которых задаёт композиционный корень. Только stdlib |
| [healthfx](health.md) | `vv/health/healthfx` | **Модуль** — группа проверок и реестр в графе uber/fx |
| [runtime](runtime.md) | `vv/runtime` | Контракт `Runner`, супервизор, который запускает, сливает и докладывает про фоновую работу, и per-replica периодический раннер. Только stdlib |
| [runtimefx](runtime.md) | `vv/runtime/runtimefx` | **Модуль** — группа раннеров, привязанная к жизненному циклу fx: вкладу не нужен invoke, который его называет |

Собственного контейнера у библиотеки нет и не будет ([[D-037]]). Эти модули
привязываются к тому, который выбрал потребитель: граф держит fx, а
`go get github.com/frostgrove/vv` по-прежнему не резолвит ничего ([[D-074]]).

## Инструменты

| Модуль | Импорт | Что это |
|---|---|---|
| [cmd/vv](vv-cli.md) | `vv/cmd/vv` | Генерирует update DTO, метамодель, публичные wire-тела с манифестом и — с `-adapter` — весь ресурс целиком |
| [vvflag](vvflag.md) | `vv/utils/vvflag` | Читает один типизированный флаг из `os.Args` до того, как им завладеет `flag.Parse` |
| [vvcfg](vvcfg.md) | `vv/utils/vvcfg` | **Модуль** — загружает конфиг-файл в структуру: валидируется всё дерево, неизвестные ключи отвергаются, происхождение значений в отчёте |
| [vvgoose](vvgoose.md) | `vv/utils/vvgoose` | **Модуль** — Goose CLI, миграции и генерация SQL-таблицы из Go-модели |

## Что здесь означает «модуль»

У опубликованного корневого модуля `github.com/frostgrove/vv` **вообще нет сторонних
зависимостей**. Всё, что добавило бы такую зависимость, становится отдельным
модулем в том же репозитории — так вы скачиваете биндинг для Fiber, или для Gin,
или ни один из них, а pgx — только если используете pgx ([[D-033]]).

Версии двигаются в лок-степе: библиотека и все сателлиты тегируются вместе,
поэтому `@v0.1.0` означает одно и то же везде. `replace` не нужен никогда.

**Манифест контракта** — это `crud`, `crud/crudtest`, `crud/query`, `errs`,
`errs/sqlerr`, `port` и `port/porthttp`. Это интерфейсы, которые реализует третья
сторона; они импортируют только стандартную библиотеку и друг друга, и список
закрыт ([[D-048]]). Это обеспечивает `make check`, а не это предложение — по
точному пути пакета и без рекурсии, потому что под `crud/` теперь есть поддерево
и совпадение по префиксу пропустило бы его целиком ([[D-058]]).

## См. также

- [usage-guides/ent.md](../../usage-guides/ent.md) — использовать модель ent как есть
- [usage-guides/gorm.md](../../usage-guides/gorm.md) — использовать модель gorm как есть
- [`_examples/`](../../../_examples/) — по одной рабочей программе на стек
- [roadmaps/Roadmap.md](../../roadmaps/Roadmap.md) — что осталось
