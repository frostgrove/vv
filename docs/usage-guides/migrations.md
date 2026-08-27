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

Now `go run ./cmd/migrate migration users` finds a unique `User` model and
generates its scalar columns. Add `--empty` for a blank Goose file,
`--no-interactive` for CI, or `--model package.User` to choose explicitly.
Run `go run ./cmd/migrate` with no arguments for `migrate`, `fresh`, `status`
and `rollback [count]` help.

See [the vvgoose module reference](../modules/en/vvgoose.md) for discovery
rules, environment names and exact `fresh` semantics.
