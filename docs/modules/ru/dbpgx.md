# dbpgx — та же конфигурация, пул pgx

```go
import "github.com/frostgrove/vv/utils/vvdb/dbpgx"
```

**Модуль:** `github.com/frostgrove/vv/utils/vvdb/dbpgx` · **Зависит от:** pgx v5

[vvdb](vvdb.md) открывает `*sql.DB` одной стандартной библиотекой. `pgxpool` —
не `database/sql`, поэтому это отдельный модуль, и потребитель на ent, gorm или
`database/sql` не берёт ради него pgx ([[D-033]], [[D-051]]).

---

## Использование

```go
pool := dbpgx.MustConnect(ctx, cfg.DB)
defer pool.Close()

repo := Products.Bind(crudpgx.Open(pool))
```

| | |
|---|---|
| `Connect(ctx, cfg, opts…)` | собрать строку, настроить пул, подключиться |
| `MustConnect(ctx, cfg, opts…)` | то же самое с паникой — для `main` |
| `ConnectReadWrite(ctx, cfg, opts…)` | primary и реплика; вторая — nil, если реплика не объявлена |

В отличие от `sql.Open`, `pgxpool.NewWithConfig` действительно подключается, так
что отсутствующий сервер обнаруживается здесь, а не на первом запросе.

## Опции доходят до pgx напрямую

```go
pool := dbpgx.MustConnect(ctx, cfg.DB, func(pc *pgxpool.Config) {
    pc.ConnConfig.Tracer = myTracer
})
```

`Option` выполняется после того, как применены поля vvdb. Это аварийный люк для
того, что одна переносимая конфигурация не может описать для четырёх движков, —
трассировщик, хук `AfterConnect`, свой map типов.

## Во что ложится секция pool

| vvdb | pgx |
|---|---|
| `max_open` | `MaxConns` |
| `max_idle` | `MinConns` — нижняя граница вместо верхней, ближе у pgx ничего нет |
| `max_lifetime` | `MaxConnLifetime` |
| `max_idle_time` | `MaxConnIdleTime` |
| `connect_timeout` | `ConnConfig.ConnectTimeout` |

Ноль не трогается. Умолчание pgx для `MaxConns` — четыре соединения или число
процессоров, и записанный поверх 0 означал бы пул, который не может открыть
ничего.

Конфигурация с другим движком отклоняется, а не приводится к нужному: `dbpgx`
говорит с PostgreSQL, и `engine: mysql` здесь — ошибка, которую стоит назвать.

## Смотри также

- [vvdb](vvdb.md) — конфигурация и строка
- [crudpgx](crudpgx.md) — то, что принимает пул дальше
