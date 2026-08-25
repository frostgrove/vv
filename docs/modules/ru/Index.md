# Модули

По одной странице на импортируемый пакет: что он делает, что вы получаете и как его подключить.
Декларативно и практично — *почему* живёт в [decisions/](../../decisions/Index.md),
а *где в исходниках* — в [flows/](../../flows/Index.md).

Сначала прочитайте [README](../../../README.md), если ещё не читали. Эти страницы —
его развёрнутая версия.

## Карта

```
                      your model struct
                              │
      ┌───────────────────────┼───────────────────────┐
      ▼                       ▼                       ▼
   crud ─────────────► repo/basic ──────────► decorators
   the contract        speaks SQL           specs · security · faults
      │                       │                       │
      │                       ▼                       │
      │                  adapter/*  ◄─────────────────┘
      │              crudsql · crudpgx
      ▼
   errs ──► sqlerr ──► sqlfault ──► catalog ──► probe
   the error contract, then the four layers that fill it

   query ──► port ──► crudhttp ──► crudnet · crudfiber · crudgin
                 └──► crudgrpc
   one request document, classified once, spelled per transport

   remote ◄── crudhttp.Transport · crudgrpc.Transport
   the same thing backwards: another service's resource, as a repository
```

## Ядро — импортируется всегда

| Модуль | Импорт | Что это |
|---|---|---|
| [crud](crud.md) | `vv/crud` | Контракт: метаданные модели, `Opt`, опции, предикаты, связи, пагинация, шов исполнителя из двух методов |
| [repo/basic](basic.md) | `vv/repo/basic` | Обычный репозиторий. `Define`, `Bind` и слой, говорящий на SQL |

## Декораторы — оборачивают репозиторий, все опциональны

| Модуль | Импорт | Что это |
|---|---|---|
| [specs](specs.md) | `vv/repo/decorators/specs` | JPA Specifications, Criteria API и проверяемая на этапе компиляции метамодель |
| [security](security.md) | `vv/repo/decorators/security` | Скоуп на уровне строк, авторизация, проверка на уровне сущности |
| [faults](faults.md) | `vv/repo/decorators/faults` | Превращает один отклонённый write в полный список нарушений, вызванных payload |

## Запрос — один документ, четыре транспорта

| Модуль | Импорт | Что это |
|---|---|---|
| [query](query.md) | `vv/query` | Wire DSL: один JSON-документ → `crud.Options`, с ограничениями для недоверенного ввода |
| [port](port.md) | `vv/port` | Транспортно-нейтральная половина: восемь команд, `Service`, `Mapper`, цепочка path |
| [crudhttp](crudhttp.md) | `vv/http/crudhttp` | HTTP-половина: таблица статусов, конверт, шов рендерера |
| [crudnet](crudnet.md) | `vv/http/crudnet` | Полный CRUD API на `net/http`. Stdlib, поэтому поставляется в библиотеке |
| [crudfiber](crudfiber.md) | `vv/http/crudfiber` | **Модуль** — тот же API на Fiber v3 |
| [crudgin](crudgin.md) | `vv/http/crudgin` | **Модуль** — тот же API на Gin |
| [crudgrpc](crudgrpc.md) | `vv/rpc/crudgrpc` | **Модуль** — тот же API на gRPC, поверх `google.protobuf.Struct` |
| [remote](remote.md) | `vv/remote` | Потребляющая половина: ресурс другого сервиса, который держат как `port.Repository` |

## Подсистема ошибок — что проваленный write сообщает клиенту

| Модуль | Импорт | Что это |
|---|---|---|
| [errs](errs.md) | `vv/errs` | Контракт: `Code`, `Kind`, `Path`, `Violation`, `Fault`, SPI, каталоги сообщений. Только stdlib |
| [sqlerr](sqlerr.md) | `vv/errs/sqlerr` | Ошибка драйвера превращается в код. Четыре таблицы диалектов, три разных способа ключевания |
| [sqlfault](sqlfault.md) | `vv/sqlfault` | Обход дерева, гейт целостности и сборка Fault. То, что принимает `WithFaults` |
| [catalog](catalog.md) | `vv/catalog` | Интроспекция схемы по хэндлу, четыре диалекта. Читается один раз, дальше отвечает из памяти |
| [probe](probe.md) | `vv/probe` | Один дополнительный запрос находит все *остальные* нарушения, вызванные тем же payload |

## Адаптеры — как vv добирается до вашей базы данных

| Модуль | Импорт | Что это |
|---|---|---|
| [crudsql](crudsql.md) | `vv/adapter/crudsql` | `database/sql` — а значит и ent, gorm, sqlx, sqlc, bun, squirrel |
| [crudpgx](crudpgx.md) | `vv/adapter/crudpgx` | **Модуль** — pgx v5, с массовой вставкой через `COPY` |
| [crudtest](crudtest.md) | `vv/crud/crudtest` | Источник в памяти: юнит-тестируйте репозиторий вообще без базы данных |

## Инструменты

| Модуль | Импорт | Что это |
|---|---|---|
| [cmd/vv](vv-cli.md) | `vv/cmd/vv` | Генерирует update DTO, метамодель и — с `-adapter` — весь ресурс целиком |
| [vvflag](vvflag.md) | `vv/vvflag` | Читает один типизированный флаг из `os.Args` до того, как им завладеет `flag.Parse` |
| [vvcfg](vvcfg.md) | `vv/tools/vvcfg` | **Модуль** — загружает YAML-конфиг в структуру, с валидацией |

## Что здесь означает «модуль»

У опубликованного корневого модуля `github.com/shardit-io/vv` **вообще нет сторонних
зависимостей**. Всё, что добавило бы такую зависимость, становится отдельным
модулем в том же репозитории — так вы скачиваете биндинг для Fiber, или для Gin,
или ни один из них, а pgx — только если используете pgx ([[D-033]]).

Версии двигаются в лок-степе: библиотека и все сателлиты тегируются вместе,
поэтому `@v0.1.0` означает одно и то же везде. `replace` не нужен никогда.

Четыре пакета образуют **манифест контракта** — `crud`, `query`, `errs`, `port`.
Это интерфейсы, которые реализует третья сторона; они импортируют только
стандартную библиотеку и друг друга, и список закрыт ([[D-048]]). Это обеспечивает
`make check`, а не это предложение.

## См. также

- [usage-guides/ent.md](../../usage-guides/ent.md) — использовать модель ent как есть
- [usage-guides/gorm.md](../../usage-guides/gorm.md) — использовать модель gorm как есть
- [`_examples/`](../../../_examples/) — по одной рабочей программе на стек
- [roadmaps/Roadmap.md](../../roadmaps/Roadmap.md) — что осталось
