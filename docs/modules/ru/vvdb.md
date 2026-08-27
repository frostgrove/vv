# vvdb — одна конфигурация, четыре движка, соединение

```go
import "github.com/frostgrove/vv/utils/vvdb"
```

**Модуль:** корневой · **Зависит от:** стандартной библиотеки

Одна структура описывает базу. Из неё получается строка подключения, которую
ждёт любой драйвер, и — если нужно — открытый `*sql.DB` с настроенным пулом.
PostgreSQL, MySQL, MariaDB и SQLite.

**Зачем он нужен:** DSN был строковой константой в каждом примере этого
репозитория и собран руками в третий раз в тестовом корпусе. Различия между
четырьмя движками — какой порт, какое экранирование, как пишется «использовать
TLS» — мелкие, незапоминаемые и легко ошибиться незаметно, а одна такая ошибка
успешно подключается не туда.

---

## Три уровня

```go
dsn, err := vvdb.DSN(cfg)                         // 0. строка, ничего не открыто
primary, replica := vvdb.MustOpenReadWrite(cfg)    // 1. хендлы database/sql, пулы настроены
src := crudsql.Postgres(primary)                   // 2. и вот теперь это принадлежит vv
if replica != nil {
    src = crud.ReadWrite(src, crudsql.Postgres(replica))
}
```

Для pgx вместо второй строки используйте `dbpgx.MustConnectReadWrite(ctx, cfg)`.
Это альтернативы: для одного хендла выбирают одно семейство драйверов, а не
открывают два пула к одной базе.

В последней строке не пропущена абстракция. **Соединение открывает приложение и
отдаёт его дальше** — именно так выглядит записанное «vv не владеет вашим
соединением», и именно поэтому ничто в этом пакете не импортирует `crud`
([[D-057]]).

## Конфигурация

```yaml
db:
  engine: postgres          # postgres | mysql | mariadb | sqlite
  host: localhost
  port: 55432               # если не задан — порт самого движка
  user: vv
  password: vv
  name: vv
  sslmode: disable          # disable | allow | prefer | require | verify-ca | verify-full
  pragmas:                  # только SQLite
    - journal_mode=WAL
    - busy_timeout=5000
  params:
    application_name: orders
  pool:
    max_open: 20
    max_idle: 5
    max_lifetime: 30m
    max_idle_time: 5m
    connect_timeout: 5s
  replica:
    host: replica.internal  # наследует всё, что не назвала заново
```

Теги — `yaml` и `env`, так что [vvcfg](vvcfg.md) загружает её без клея:

```go
type Config struct {
    Addr string      `yaml:"addr" env:"ADDR"`
    DB   vvdb.Config `yaml:"db"`
}

func (c Config) Validate() error { return c.DB.Validate() }

cfg            := vvcfg.Must(vvcfg.Auto[Config](os.Args[1:]))
sqlDB, replica := vvdb.MustOpenReadWrite(cfg.DB)
```

`vvcfg` вызывает только метод `Validate` верхней структуры. Явный форвардер
оставляет у приложения одно место для проверки всех блоков и гарантирует, что
блок базы проверен при загрузке. Скалярные настройки используют `DB_*`, включая
`DB_POOL_CONNECT_TIMEOUT` (старый `DB_CONNECT_TIMEOUT` сохранён для
совместимости). `DB_PARAMS` — URL query, например
`application_name=orders,worker&statement_timeout=5s`; если `&` или `=` входят
в значение, их надо percent-кодировать. После YAML `vvcfg` также применяет
парные имена `DB_REPLICA_*` — например `DB_REPLICA_HOST`,
`DB_REPLICA_PARAMS`, `DB_REPLICA_POOL_MAX_IDLE`: реплику можно целиком задать
переменными или переопределить ими читаемое YAML-описание.
Если нет ни `--config-path`, ни `CONFIG_PATH`, `Auto` включает режим только
environment, поэтому та же декларация работает в image без config-файла.
Явный `DB_DSN` или `DB_REPLICA_DSN` одной операцией заменяет соответствующее
field-описание из YAML; `Config`, собранный в Go, по-прежнему откажет raw DSN
рядом с typed connection fields.
`DB_SQLITE_PRAGMAS` и `DB_REPLICA_PRAGMAS` используют тот же список
`name=value` через запятую. Для двух блоков БД cleanenv-prefix — часть
верхнеуровневой декларации: `Analytics vvdb.Config` с
`yaml:"analytics" env-prefix:"ANALYTICS_"` использует
`ANALYTICS_DB_HOST` и `ANALYTICS_DB_REPLICA_HOST`, а не переменные primary.

| | |
|---|---|
| `DSN(cfg)` | строка для того движка, который назван в конфигурации |
| `PostgresDSN` `MySQLDSN` `MariaDBDSN` `SQLiteDSN` | та же полностью проверенная декларация, когда вызывающий уже знает движок |
| `Open(cfg)` / `MustOpen(cfg)` | один `*sql.DB` с применённым пулом; при объявленной replica отказывают |
| `OpenReadWrite(cfg)` / `MustOpenReadWrite(cfg)` | primary и реплика; вторая — nil, если реплика не объявлена |
| `cfg.Pool.Apply(db)` | настроить `*sql.DB`, открытый самим приложением |
| `cfg.Pool.Validate()` | отказать противоречивым настройкам пула, если adapter принимает пул напрямую |
| `DriverName(cfg)` | имя драйвера `database/sql`, с которым будет открывать `Open` |
| `cfg.Validate()` | отказать конфигурации, которая не может значить то, что говорит |
| `cfg.ReadReplica()` | реплика в том виде, в каком она будет открыта, с наследованием |

## Драйвер — это ваш импорт

`Open` зовёт `sql.Open` и не регистрирует драйвер, и поэтому у пакета нет ни
одной зависимости и не нужен отдельный модуль:

```go
import _ "github.com/jackc/pgx/v5/stdlib"   // регистрирует "pgx"
import _ "github.com/go-sql-driver/mysql"   // регистрирует "mysql"
```

Имена по умолчанию — `pgx`, `mysql` и `sqlite`. Typed-конфигурация PostgreSQL
намеренно поддерживает только pgx: любое имя драйвера кроме `pgx` может иметь
собственную ambient-конфигурацию или passfile. Приложение, сознательно
использующее lib/pq — в том числе через локальный alias драйвера, — задаёт
полный raw `dsn:`. Для SQLite от mattn — `driver: sqlite3`.

## От чего отказывается и почему в каждом случае

| | |
|---|---|
| движок не из четырёх | прочитать неизвестный движок как движок по умолчанию — это успешное подключение не к тому серверу и ни слова об этом ([[D-013]]) |
| `dsn` рядом с `host`, `name`, … | два источника истины, один из них молча игнорируется |
| `dsn` рядом с `pool.connect_timeout` | raw-строка владеет своим timeout; иначе разные adapter-ы применяли бы разные значения |
| PostgreSQL `PGSERVICE` или `PGSSLNEGOTIATION` рядом с typed-полями | это второй документ подключения; при намеренном использовании нужен явный raw `dsn` |
| typed PostgreSQL с любым `driver`, кроме `pgx` | alias способен скрыть lib/pq и вернуть ambient-конфигурацию; используйте pgx либо полный raw DSN |
| `sslmode: verify-ca` на MySQL | драйверу нужен зарегистрированный `tls.Config`; молчаливый откат к `skip-verify` заявлял бы проверку, которой никто не делает |
| `:` в имени пользователя MySQL | драйвер делит пользователя и пароль по **первому** двоеточию |
| `path` у серверного движка, `host` у SQLite | поле принадлежит другому движку и было бы отброшено |

Всё перечисленное — отказ на старте с названным полем. Неверная конфигурация
должна остановить процесс до того, как пришёл трафик ([[D-021]]).

## Экранирование, в котором и состоит вся работа

Строка PostgreSQL — это URI, строка MySQL — нет, и калечат они разные символы:

- **PostgreSQL** — вся строка проходит через `net/url`, поэтому пароль с `@`,
  `/` или `?` доезжает percent-кодированным.
- **MySQL** — пароль **не** экранируется, потому что драйвер его и не
  раскодирует: он берёт последнюю `@` перед последней `/`, а обе эти позиции
  ставит vvdb. Параметры и имя базы, наоборот, экранируются, и это не
  косметика. Написанный как есть, `loc=Europe/Moscow` сдвигает то место, где
  драйвер считает конец имени базы, и он читает базу как `Moscow`.
- **Сокеты** — `host`, начинающийся с `/`, становится `?host=…` для PostgreSQL
  и `unix(…)` для MySQL. Хостом он не является ни в одном из двух синтаксисов.

`parseTime=true` пишется для семейства MySQL, если `params` не говорит иного.
Это единственное умолчание здесь, которое меняет то, что возвращает база: без
него `DATETIME` приезжает байтами, и скан в поле `time.Time` падает далеко от
пропущенного параметра.

SQLite `pragmas` — не map с одним значением: одновременно сохраняются
`journal_mode=WAL` и `busy_timeout=5000`. Для default `modernc.org/sqlite`
vvdb рендерит повторяющиеся ключи `_pragma`, для `mattn/go-sqlite3` — его
имена `_journal_mode` / `_busy_timeout`; произвольный SQL отклоняется, а не
попадает в connection string.

## Реплики

`replica` наследует каждое поле, которое не назвала заново, поэтому обычный
случай — одна строка. Пара уходит в `crud.ReadWrite`, который и решает, что
чтение идёт на реплику, а запись, чтение с блокировкой и загрузочная половина
update — нет ([[D-032]]):

```go
primary, replica, err := vvdb.OpenReadWrite(cfg.DB)
src := crudsql.Postgres(primary)
if replica != nil {
    src = crud.ReadWrite(src, crudsql.Postgres(replica))
}
```

`Open` и `MustOpen` намеренно отказывают конфигурации с `replica`: вернуть
только primary означало бы, что YAML выглядит работающим, но чтения молча
остаются на primary. При `replica` используйте helper, возвращающий пару.

Реплика может быть полевым переопределением, как выше, либо полным `dsn:`.
Полный DSN реплики владеет всеми полями подключения; смешивать его с `host`,
`name` и прочими полями подключения нельзя — это два источника истины. Но он
наследует primary `driver` и не-DSN политику пула (`max_open`, `max_idle`,
lifetime, idle time), поэтому оба хендла используют один зарегистрированный
драйвер и одно описание размеров. `connect_timeout` находится в raw-строке и
не наследуется.
По той же причине полевое переопределение реплики нельзя применить к
непрозрачному primary `dsn`: используйте `replica.dsn`, а не требуйте от vvdb
разобрать и переписать строку, которую он обещал сохранить без изменений.

## ORM

Модуля для ent, gorm, sqlx или sqlc нет, и он не нужен: каждый из них принимает
либо `*sql.DB`, либо строку, а есть уже и то и другое.

```go
sqlDB := vvdb.MustOpen(cfg.DB)
client := entmodel.NewClient(entmodel.Driver(entsql.OpenDB(dialect.Postgres, sqlDB)))

dsn, err := vvdb.DSN(cfg.DB)
if err != nil { log.Fatal(err) }
gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
if err != nil { log.Fatal(err) }
```

## Смотри также

- [dbpgx](dbpgx.md) — та же конфигурация, пул pgx
- [vvcfg](vvcfg.md) — загрузчик, который наполняет структуру
- [crudsql](crudsql.md) — то, что принимает хендл дальше
- [flows/FL-021](../../flows/FL-021-a-configuration-becomes-a-connection.md)
