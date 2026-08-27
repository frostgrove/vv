# vvgoose — Goose migrations and SQL generation from a Go model

```go
import "github.com/frostgrove/vv/utils/vvgoose"
```

```bash
go get github.com/frostgrove/vv/utils/vvgoose
```

**Module:** separate · **Depends on:** Goose, Cobra, Huh, and the pgx, MySQL
and modernc SQLite drivers

`vvgoose` gives an application a standalone migration command with no separate
bootstrap layer. Create `cmd/migrate/main.go`:

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

Keep `DB vvdb.Config` in the application's top-level configuration. Migration
settings live in that same block:

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

Defaults are `path: ./migrations`, `models: [.]`, and
`table: goose_db_version`. Their environment names are `DB_MIGRATION_PATH`,
`DB_MIGRATION_MODELS`, and `DB_MIGRATION_TABLE`.

## Commands

```text
go run ./cmd/migrate                         # list commands
go run ./cmd/migrate migration users        # generate SQL from model User
go run ./cmd/migrate migration users --empty
go run ./cmd/migrate migration users --model account.User
go run ./cmd/migrate migration users --no-interactive
go run ./cmd/migrate migrate
go run ./cmd/migrate status
go run ./cmd/migrate rollback                # one migration
go run ./cmd/migrate rollback 3
go run ./cmd/migrate fresh
```

`fresh` runs every known `Down` section and then every `Up` section. It does
not drop tables that are not owned by migrations.

## Model selection

Generation parses source with the standard `go/parser`; application code is
neither compiled nor executed. A struct is model evidence when it is:

- in `model.go`, `*.model.go`, or `*_model.go`;
- tagged with `db`, `rel`, or `gorm`;
- embedding `gorm.Model`; or
- declaring a constant `TableName() string` method.

Column names, `pk`, `auto`, nullable types, `time.Time`, UUID, JSON, embedded
structs, and `gorm.Model` fields are understood; relations do not become
columns.

One matching model is automatic. When several candidates are equally likely,
a terminal gets a searchable select. Non-interactive ambiguity creates an
empty editable Goose skeleton. `--empty` does the same; an invalid
`CREATE TABLE name ()` is intentionally not emitted because MySQL and SQLite
reject it. `--model` selects a struct explicitly.

The `migration` command never opens the database. `migrate`, `status`,
`rollback`, and `fresh` open only the primary even when a read replica is
configured.
