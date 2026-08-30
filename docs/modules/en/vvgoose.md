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
    vvgoose.Execute(&cfg.DB)
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
  sslmode: disable          # local development only
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
go run ./cmd/migrate                         # open the interactive command menu
go run ./cmd/migrate migration add_audit_log # create an editable SQL skeleton
go run ./cmd/migrate migration init_permission_tables --tables permissions,roles
go run ./cmd/migrate migration init_permission_tables permissions,roles
go run ./cmd/migrate table users             # infer User and create its table migration
go run ./cmd/migrate table users,products    # create one table migration per model
go run ./cmd/migrate table users --empty
go run ./cmd/migrate table users --model account.User
go run ./cmd/migrate init                    # create or replace *_init.sql from all models
go run ./cmd/migrate migrate
go run ./cmd/migrate status
go run ./cmd/migrate rollback                # one migration
go run ./cmd/migrate rollback 3
go run ./cmd/migrate fresh
go run ./cmd/migrate flush                   # delete every local database object
```

`migration <name>` is an editable Goose skeleton; it never guesses tables from
its name. Add `-t`/`--tables permissions,roles`, or a second positional table
list, to generate those models in that one named migration. `table` is the
model-inference shortcut and accepts one comma-separated list, writing a
separate migration per table.

`init` renders every discovered model with mapped columns into one `*_init.sql`
file. Re-running it replaces that file while preserving its Goose version. Do
not replace an init migration that has already been applied to a shared
database: Goose will not apply the same version twice.

With no arguments, a terminal gets a searchable command menu for migration,
table, init and runtime operations. A non-terminal invocation, or
`--no-interactive`, prints help instead; every command remains directly
addressable by arguments.

`fresh` runs every known `Down` section and then every `Up` section. It does
not drop tables that are not owned by migrations.

`flush` is the development recovery command when Goose history refers to a
migration file that no longer exists, so `fresh` cannot find its `Down`
section. It drops all objects in the active schema/database, including the
Goose history table, and does **not** run migrations afterwards. Run `migrate`
explicitly when the empty database is the desired state. PostgreSQL recreates
the current non-system schema; MySQL/MariaDB remove every table, view, routine,
and event in the selected database; SQLite removes every non-system table, view,
and trigger.
It is destructive and must only point at a local development database.

Generated table migrations use `CREATE TABLE IF NOT EXISTS`. This makes an
initial migration safe to re-run after tables survived a failed local setup; it
does not reconcile a schema that differs from the model.

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
a terminal gets a searchable select. Non-interactive ambiguity creates an empty
editable table migration. `--empty` does the same; an invalid `CREATE TABLE
name ()` is intentionally not emitted because MySQL and SQLite reject it.
`--model` selects a struct explicitly for a one-table command.

`migration`, `table`, and `init` never open the database. `migrate`, `status`,
`rollback`, `fresh`, and `flush` open only the primary even when a read replica is
configured.
