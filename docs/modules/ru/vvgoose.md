# vvgoose — миграции Goose и генерация SQL из Go-модели

```go
import "github.com/frostgrove/vv/utils/vvgoose"
```

```bash
go get github.com/frostgrove/vv/utils/vvgoose
```

**Модуль:** отдельный · **Зависит от:** Goose, Cobra, Huh и драйверов pgx,
MySQL и modernc SQLite

`vvgoose` даёт приложению отдельную migration-команду без собственного
bootstrap-кода. Создайте `cmd/migrate/main.go`:

```go
package main

import (
    "github.com/frostgrove/vv-template/src/config"
    "github.com/frostgrove/vv/utils/vvcfg"
    "github.com/frostgrove/vv/utils/vvgoose"
)

func main() {
    cfg := vvcfg.MustLoad[config.Config]()
    vvgoose.Execute(cfg.DB)
}
```

Верхнеуровневый конфиг приложения хранит `DB vvdb.Config`. Настройки миграций
находятся в том же блоке:

```yaml
db:
  engine: postgres
  host: localhost
  user: app
  password: secret
  name: app
  migration:
    path: ./migrations
    models:
      - ./src
    table: goose_db_version
```

Значения по умолчанию: `path: ./migrations`, `models: [.]`,
`table: goose_db_version`. Им соответствуют `DB_MIGRATION_PATH`,
`DB_MIGRATION_MODELS` и `DB_MIGRATION_TABLE`.

## Команды

```text
go run ./cmd/migrate                         # открыть интерактивное меню команд
go run ./cmd/migrate migration add_audit_log # создать редактируемую SQL-заготовку
go run ./cmd/migrate migration init_permission_tables --tables permissions,roles
go run ./cmd/migrate migration init_permission_tables permissions,roles
go run ./cmd/migrate table users             # найти User и создать миграцию таблицы
go run ./cmd/migrate table users,products    # создать отдельную миграцию для каждой модели
go run ./cmd/migrate table users --empty
go run ./cmd/migrate table users --model account.User
go run ./cmd/migrate init                    # создать или перезаписать *_init.sql по всем моделям
go run ./cmd/migrate migrate
go run ./cmd/migrate status
go run ./cmd/migrate rollback                # одна миграция
go run ./cmd/migrate rollback 3
go run ./cmd/migrate fresh
```

`migration <name>` создаёт редактируемую Goose-заготовку и не пытается угадать
таблицу по имени. Чтобы сгенерировать таблицы в этом одном файле, передай
`-t`/`--tables permissions,roles` либо вторым позиционным аргументом список
таблиц. `table` — shortcut для поиска моделей: он принимает список через
запятую и создаёт отдельную миграцию на таблицу.

`init` рендерит все найденные модели с колонками в один файл `*_init.sql`.
Повторный вызов перезаписывает именно этот файл, сохраняя его Goose-версию.
Не заменяй уже применённый init в общей БД: Goose не применит ту же версию
повторно.

Без аргументов в терминале открывается searchable-меню для migration, table,
init и runtime-команд. Без TTY или с `--no-interactive` команда печатает help;
все команды по-прежнему можно вызывать напрямую аргументами.

`fresh` выполняет все известные секции `Down`, затем все `Up`. Он не удаляет
таблицы, которые не принадлежат миграциям.

## Как выбирается модель

Генератор разбирает исходники через стандартный `go/parser`; код приложения не
компилируется и не запускается. Моделью считается структура:

- из `model.go`, `*.model.go` или `*_model.go`;
- с тегами `db`, `rel` или `gorm`;
- со встроенным `gorm.Model`;
- с константным методом `TableName() string`.

Учитываются имена колонок, `pk`, `auto`, nullable-типы, `time.Time`, UUID, JSON,
embedded-структуры и поля `gorm.Model`; связи в колонки не превращаются.

Одна подходящая модель выбирается автоматически. Если лучших кандидатов
несколько, в терминале появляется searchable select. В non-interactive режиме
неоднозначность создаёт пустую редактируемую table-миграцию. То же делает
`--empty`; невалидный `CREATE TABLE name ()` намеренно не генерируется, потому
что MySQL и SQLite его не принимают. `--model` задаёт структуру явно для одной
таблицы.

Команды `migration`, `table` и `init` не открывают БД. `migrate`, `status`,
`rollback` и `fresh` открывают только primary, даже если в конфигурации есть
read replica.
