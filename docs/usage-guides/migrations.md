# Add a migration command to an application

Install the migration satellite, add the migration fields under the existing
database config, then create one tiny command. No framework checkout and no
`replace` directive is part of consumer setup.

```bash
go get github.com/frostgrove/vv/utils/vvgoose
```

```go
// cmd/migrate/main.go
package main

import (
    "your.app/src/config"
    "github.com/frostgrove/vv/utils/vvcfg"
    "github.com/frostgrove/vv/utils/vvgoose"
)

func main() {
    cfg := vvcfg.MustLoad[config.Config]()
    vvgoose.Execute(cfg.DB)
}
```

```yaml
db:
  engine: postgres
  host: localhost
  name: app
  migration:
    path: ./migrations
    models: [./src]
```

Use `go run ./cmd/migrate table users` to find a unique `User` model and
generate its scalar columns; `table users,products` creates one file per table.
Use `go run ./cmd/migrate migration add_index` for an editable Goose file, or
`migration init_permissions --tables permissions,roles` to put explicit models
in one named migration. `go run ./cmd/migrate init` creates or replaces the
single `*_init.sql` baseline from all discovered models.

Run `go run ./cmd/migrate` with no arguments to open the searchable interactive
command menu. Direct arguments remain available for scripts and CI; a non-TTY
invocation prints help instead of trying to prompt.

See [the vvgoose module reference](../modules/en/vvgoose.md) for discovery
rules, environment names and exact `fresh` semantics.
