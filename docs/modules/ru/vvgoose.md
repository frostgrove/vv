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
go run ./cmd/migrate                         # показать список команд
go run ./cmd/migrate migration users        # создать SQL по модели User
go run ./cmd/migrate migration users --empty
go run ./cmd/migrate migration users --model account.User
go run ./cmd/migrate migration users --no-interactive
go run ./cmd/migrate migrate
go run ./cmd/migrate status
go run ./cmd/migrate rollback                # одна миграция
go run ./cmd/migrate rollback 3
go run ./cmd/migrate fresh
```

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
неоднозначность создаёт пустой редактируемый Goose-шаблон. То же делает
`--empty`; невалидный `CREATE TABLE name ()` намеренно не генерируется, потому
что MySQL и SQLite его не принимают. `--model` задаёт структуру явно.

Команда `migration` не открывает БД. `migrate`, `status`, `rollback` и `fresh`
открывают только primary, даже если в конфигурации есть read replica.
