# Model-first generation

`vv` can derive its repository declaration from an ordinary Go model. No ORM is
part of this contract: the same model works over `database/sql`, native pgx,
GORM, sqlx, a test source, or another vv adapter.

Put exported persistence models in `model.go`, `*.model.go`, or `*_model.go`.
For example, `src/app/product/product.model.go` can contain only this:

```go
package product

import (
    "time"

    "github.com/google/uuid"
)

type Product struct {
    ID          uuid.UUID
    Name        string
    Description *string

    CreatedAt time.Time
    UpdatedAt time.Time
}
```

By convention, vv maps `Name` to `name`, `CreatedAt` to `created_at`, and
`ID` (or `Id`) to the primary-key column `id`. The default table name is the
snake-case plural of the type name: `Product` becomes `products`.

## Generate the package artefacts

Run one command from the application module:

```bash
go run github.com/frostgrove/vv/cmd/vv generate -dir ./src/app
```

`generate` walks the directory tree, finds model files, and writes a
deterministic `vv_gen.go` next to every package that contains exported models.
Add the command to a `Makefile` and commit the generated output:

```make
generate:
	go run github.com/frostgrove/vv/cmd/vv generate -dir ./src/app
```

The generated file contains the three mechanical declarations and the
datasource-independent repository blueprint:

```go
type ProductUpdate struct {
    Name        *string
    Description utils.Opt[string]
    // …
}

var Product_ = specs.Metamodel[Product, ProductAttrs]()

type ProductRepo = crud.Repo[Product, uuid.UUID, ProductUpdate]

var ProductRepository = sqlrepo.Define[Product, uuid.UUID, ProductUpdate]("")

func NewProductRepository(src crud.Source) *ProductRepo {
    return ProductRepository.Bind(src)
}
```

The empty table argument is intentional: it preserves the model's `TableName`
method when one is present, otherwise vv applies the usual convention.
`ProductRepo` is an alias, not a wrapper: use `*ProductRepo` in Fx services and
handlers without repeating the three generic parameters.

## Choose the driver in the composition root

Generation does not open a connection or decide on a driver. Bind the generated
factory where the application already composes infrastructure.

`database/sql` over pgx:

```go
sqlDB, err := vvdb.Open(&cfg.DB)
if err != nil {
    return err
}

products := product.NewProductRepository(crudsql.Postgres(sqlDB))
```

Native pgx:

```go
pool, err := dbpgx.Connect(ctx, &cfg.DB)
if err != nil {
    return err
}

products := product.NewProductRepository(crudpgx.Open(pool))
```

GORM also uses `database/sql`, so give vv the same pool GORM holds:

```go
sqlDB, err := gormDB.DB()
if err != nil {
    return err
}

products := product.NewProductRepository(crudsql.Postgres(sqlDB))
```

There is one pool in the last example. In a GORM-owned transaction, put
`tx.Statement.ConnPool` into the context with
`source.BindExecutor(ctx, tx.Statement.ConnPool)`; vv calls then join that
transaction without losing the pool identity.

## Exceptions stay explicit

Tags are not required for normal fields. Use them only when the model is not
conventional:

```go
type LedgerEntry struct {
    EntryNumber int64  `db:"entry_number,pk,noauto"`
    ExternalRef string `db:"external_reference,immutable"`
    SearchText  string `db:"search_text,generated"`
    DeletedAt   *time.Time `db:"deleted_at,serverowned,tombstone"`
}

func (LedgerEntry) TableName() string { return "ledger_entries" }
```

`db` covers an unusual column name or repository-owned behaviour such as an
assigned integer key, immutable field, generated value, version column,
repository-owned tombstone or a field vv must ignore. A tombstone declaration
automatically disappears from generated PATCH/create/replace shapes, freezes
full Save, and makes the generated repository soft-delete with an explicit
Restore lifecycle action. Its type must be a nullable timestamp such as
`*time.Time`, `Opt[time.Time]`, `sql.NullTime`, or `gorm.DeletedAt`. `rel`
declares navigable relationships. Neither tag is needed merely to repeat the
normal naming convention, and GORM tags are not a requirement of vv.

For a model that cannot live in a conventional model file, keep the explicit
single-package form:

```bash
go run github.com/frostgrove/vv/cmd/vv -dir ./ent -types User,Article \
    -import myapp/ent -into ./internal/store
```

## Keep generated output current

Run generation after every model change and make CI reject stale output:

```bash
make generate
git diff --exit-code
```

The generated file also runs `port.MustCoverUpdate` at package initialisation.
If a model gained a writable field while its generated DTO was not refreshed,
the application refuses to start instead of silently making that field
unpatchable.

See [cmd/vv](../modules/en/vv-cli.md) for every generator option and
[migrations](migrations.md) for generating SQL migrations from the same model
files.
