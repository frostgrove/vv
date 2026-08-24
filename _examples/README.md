# Runnable examples

One small, complete program per stack. Each is a single `main.go` you can read
top to bottom: a model, a declaration, a mount, and a `bootstrap` that creates
its own table and seeds three rows so the example runs against an empty
database.

They exist because the two usage guides describe the wiring and these execute
it. If a guide and an example disagree, the example is the one that was run.

## Start the databases, then pick one

```bash
make up                      # from the repository root
cd _examples
go run ./pgx-fiber           # or any directory below; -addr changes the port
curl 'localhost:8080/products?f=price:gte:100&sort=-price'
```

Every example serves the same resource at `/products` with the same ten routes,
so you can diff any two files and see only what the stack changes.

All of them need `go get github.com/shardit-io/rx`. Beyond that each
needs only what its own stack uses: the Fiber ones add `.../http/crudfiber`, the
Gin ones `.../http/crudgin`, `pgx-fiber` adds `.../adapter/crudpgx`, and
`sql-nethttp` adds nothing at all — `crudnet` and `crudsql` are both stdlib, so
both live in the library.

## The matrix

| Example | Data layer | Adapter | Engine | HTTP | What it shows that the others do not |
|---|---|---|---|---|---|
| [`pgx-fiber`](pgx-fiber/) | none | `crudpgx` | PostgreSQL | Fiber | The shortest path: a pgx pool, a struct with `db` tags, no ORM and no `database/sql` in the way. |
| [`ent-pgx-fiber`](ent-pgx-fiber/) | ent | `crudsql` | PostgreSQL | Fiber | An ent project keeps its generated entity, its migration and its builders; rx-crud binds the generated struct as-is and serves from the same pool. |
| [`ent-pgx-gin`](ent-pgx-gin/) | ent | `crudsql` | PostgreSQL | Gin | The same model, the same declaration and the same options as the example above — only the mount and the engine differ. |
| [`gorm-pgx-fiber`](gorm-pgx-fiber/) | gorm | `crudsql` | PostgreSQL | Fiber | One struct carrying both gorm and rx-crud tags: no second model, no adapter type, and gorm and rx-crud share one `*sql.DB`. |
| [`gorm-mysql-gin`](gorm-mysql-gin/) | gorm | `crudsql` | MySQL | Gin | The same declaration on a different engine. MySQL has no `RETURNING`, so rx-crud reads the written row back; the caller cannot tell ([`D-019`](../docs/decisions/D-019-dialect-differences-are-not-observable.md)). |
| [`sqlx-pgx-gin`](sqlx-pgx-gin/) | sqlx | `crudsql` | PostgreSQL | Gin | sqlx and rx-crud read the *same* `db` tag, so there is exactly one tag set: sqlx keeps the queries it is good at, rx-crud serves the CRUD surface. |
| [`sql-nethttp`](sql-nethttp/) | none | `crudsql` | PostgreSQL | `net/http` | The standard library and nothing else — and no second `go get`, because the net/http binding needs no dependency and ships in the library. |

[`example/`](example/) is not a server. It is the library's whole user-facing
surface in one file — model, DTO, declaration, metamodel, security policy —
with tests that run it against `crud/crudtest`, so it needs no database.

[`entmodel/`](entmodel/) and [`entstore/`](entstore/) are shared by the two ent
examples: ent's generated package, and the rx-crud DTO and metamodel generated
from it. In your own project both live in your own tree; they are shared here
only so the same ent code is not checked in twice.

## If your project is on neither Fiber nor Gin

Read [`sql-nethttp`](sql-nethttp/). It mounts `http/crudnet`, the `net/http`
binding, and it is the same one-liner the other examples use:

```go
mux := http.NewServeMux()
crudnet.New(repo).Mount(mux, "/products")
```

`crudnet` imports nothing outside the standard library, so unlike the Fiber and
Gin bindings it is not a module of its own — it ships in the library and costs
nothing to have. Its route methods are ordinary `http.HandlerFunc`s, so a router
that is neither `ServeMux` nor a framework — chi, gorilla/mux, httprouter — can
register them one by one instead of calling `Mount`.

## Two things the responses will show you

**A column `DEFAULT` does not fire.** rx-crud writes every mapped column, so an
INSERT it builds names `active`, and creating a product without one stores
`false` rather than the column's `true`. A default only reaches rows the
database makes on its own. Where the server must own a value, mark the column
`generated` or fill it in a `BeforeSave` hook.

**ent's Go-side defaults do not run either**, and ent's generated struct has no
`db` tags to mark a column `generated` with — so both ent examples stamp
`created_at` in a `BeforeSave` hook. That is the second of the three ways out
that [`docs/usage-guides/ent.md`](../docs/usage-guides/ent.md) §16 lists, and
the only one available when the model is generated code you must not edit.

## Building them

```bash
make examples      # from the repository root: build, vet and test this module
```

The leading underscore keeps this tree out of `go build ./...` at the root, so
`make unit` does not build it. It is a module of its own — like `test/`, and for
the same reason: ent, gorm, sqlx and both HTTP bindings must never become
dependencies of the library ([`D-033`](../docs/decisions/D-033-optional-dependencies-are-their-own-modules.md)).
