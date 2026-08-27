# vv

A generic CRUD repository for Go with JPA-shaped semantics, a Specifications /
Criteria API, a security gate and an error subsystem that tells a client
everything wrong with one request at once — over any driver, without owning your
connection or your transaction.

Only two things ever cross the abstraction boundary: **run this statement** and
**give me rows**. Scanning stays with the mapper, dialect stays with the
repository. That is why any foreign transaction can be pushed into a context —
all vv asks of it is `Exec` and `Query`.

```
crud/                       core: contracts, metadata, relations, predicates, Opt, pagination — stdlib only
├── crudtest/               an in-memory source for unit-testing repositories
├── query/                  the wire DSL: one JSON document -> crud.Options
├── sqlrepo/                the repository that speaks SQL
├── decorators/specs/       JPA Specifications + Criteria API + metamodel
├── decorators/security/    row-level scope, authorization, per-entity checks
├── decorators/faults/      names the field a violation happened at, and wires the probe in
├── adapter/crudsql/        database/sql — and therefore ent, gorm, sqlx, sqlc, bun, squirrel
├── catalog/                per-handle schema introspection, four dialects
├── probe/                  one extra statement finds every other violation the payload caused
├── sqlfault/               the tree walk, the integrity gate and fault assembly
├── http/crudhttp/          what is HTTP *and* CRUD: the request shapes, the model hop
└── http/crudnet/           a full CRUD API on net/http — stdlib only, so it costs nothing
auth/                       who the caller is: Principal, Role, Permission, Guard, the 401
├── apikey/                 an Authenticator over a shared secret
├── http/authhttp/          the HTTP half of the middleware: the renderer, the refusal
└── http/authnet/           the net/http auth middleware — stdlib only
port/                       the transport-neutral half: commands, Service, Mapper, the path chain
└── porthttp/               the HTTP projection of the error contract: the status table,
                            the envelope, the Renderer seam — shared by every subsystem
remote/                     the consuming half: another service's resource, held as a port.Repository
└── remotehttp/             the HTTP client transport
errs/                       the error contract: Code, Kind, Path, Violation, Fault, the SPI — stdlib only
└── sqlerr/                 a driver error becomes a code, one table per dialect
utils/                      for your application, never for the library
├── vvdb/                   one config -> a DSN or a *sql.DB: postgres, mysql, mariadb, sqlite — stdlib only
└── vvflag/                 one typed flag, without owning the command line
cmd/vv/                     generates the update DTO, the metamodel and — with -adapter — the resource

  ── separate modules, so you only download the one you use ──────────────────
crud/http/crudfiber/        a full CRUD API on Fiber v3
crud/http/crudgin/          the same API on Gin
crud/rpc/crudgrpc/          the same API on gRPC
crud/adapter/crudpgx/       pgx v5
auth/authjwt/               JWT verification, generic over your claims
auth/http/authgin/          the Gin auth middleware
auth/http/authfiber/        the Fiber auth middleware
auth/rpc/authgrpc/          the gRPC auth interceptors
utils/vvdb/dbpgx/           the same config, a pgx pool
utils/vvcfg/                a config struct, loaded and validated at start-up
utils/vvgoose/              Goose migrations and SQL generation from Go models
```

**One page per package** — what it does, everything it can do, and how to wire
it — is in [`docs/modules/`](docs/modules/Index.md)
([English](docs/modules/en/Index.md) · [Русский](docs/modules/ru/Index.md)).

Declare a model, get a filtered, sorted, paginated, relation-loading HTTP API:

```go
app.Use("/articles", crudfiber.New(articles).Routes())   // Fiber
crudgin.New(articles).Mount(r, "/articles")              // Gin
crudnet.New(articles).Mount(mux, "/articles")            // or net/http, no dependency at all
crudgrpc.New(articles).Register(srv, "Article")          // or gRPC, which is not HTTP at all
```

```http
POST /articles/query
{
  "filter": { "views": {"gte": 100}, "tags.slug": {"in": ["go","rust"]} },
  "preload": ["author", "comments.author"],
  "sort": ["-views", "author.name"],
  "page": 2, "limit": 20
}
```

And when a write is refused, the client is told **everything** that was wrong
with it — not the first thing the database happened to reach:

```http
POST /users            →  422
{ "type": "error", "errors": { "validation": [
  { "field": ["user","email"],  "error_code": "unique",      "message": "that address is taken" },
  { "field": ["user","org_id"], "error_code": "foreign_key", "message": "the organisation does not exist" },
  { "field": ["user","age"],    "error_code": "check",       "message": "age must be at least 18" }
]}}
```

No constraint name, no table name, no SQLSTATE, no driver prefix.

**Already using an ORM?** Two task-oriented guides — bind the structs you
already have, generate the update DTO, mount the API, share transactions with
the ORM's own builders. Every claim in both is executed by the integration
suite, with one exception named where it is made: ent's hooks and privacy rules
would need a test schema that declares one, and none does.

- [`docs/usage-guides/ent.md`](docs/usage-guides/ent.md) — ent's *generated* entity struct is the model, as-is
- [`docs/usage-guides/gorm.md`](docs/usage-guides/gorm.md) — your gorm struct is the model, `gorm.Model`
  and associations included
- [`docs/usage-guides/migrations.md`](docs/usage-guides/migrations.md) — add a standalone migration command

**Prefer running code?** [`_examples/`](_examples/) has one small, complete program per
stack — ent, gorm, sqlx and no-ORM-at-all, across all three HTTP bindings and both
engines. Each is a `main.go` you can read top to bottom, plus the generated
`vv_gen.go` beside it. Start the databases with `make up`, then `cd _examples` and
`GOWORK=off go run ./<example>` serves a real API you can curl — the workspace is
off because `_examples` is deliberately outside it, so its stacks never reach the
module graph of anyone building the library.

---

## Install

```bash
go get github.com/frostgrove/vv                      # the library — and, on net/http, the whole of it
go get github.com/frostgrove/vv/crud/http/crudgin   # …plus your HTTP framework, if you use one
go get github.com/frostgrove/vv/crud/rpc/crudgrpc   # …or gRPC instead of an HTTP framework
go get github.com/frostgrove/vv/crud/adapter/crudpgx # …and pgx, if that is your driver
go get github.com/frostgrove/vv/utils/vvdb/dbpgx     # …and a pgx pool opened from your config file
go get github.com/frostgrove/vv/utils/vvgoose        # …or the application migration CLI
go get github.com/frostgrove/vv/auth/authjwt        # …and JWT, if that is how you authenticate
```

The library has **no external dependencies at all**. Anything that would add one
is a module of its own in the same repository, so you download the Fiber binding
or the Gin binding or neither, and pgx only if you use pgx. The `net/http`
binding and the `database/sql` adapter need nothing, so they ship in the library
— on `net/http` over `database/sql`, which is how ent, gorm, sqlx, sqlc and bun
are reached, the first line is the whole installation.
`vvgoose` is the deliberate cross-engine command: it carries Goose, its
searchable terminal UI and the drivers it registers so `cmd/migrate` needs no
blank imports of its own.

Versions move in lockstep: the library and every binding are tagged together, so
`@v0.1.0` means the same thing everywhere. No `replace` is ever needed
([`D-033`](docs/ai/decisions/D-033-optional-dependencies-are-their-own-modules.md)).

## Quick start

Declare the model, the update DTO and the repository. That is all the
boilerplate there is.

```go
type User struct {
    ID        int64         `db:"id,pk,auto"`
    TenantID  int64         `db:"tenant_id,immutable"`
    Email     string        `db:"email"`
    Name      string        `db:"name"`
    Age       crud.Opt[int] `db:"age"`         // nullable
    Active    bool          `db:"active"`
    CreatedAt time.Time     `db:"created_at,generated"`
}

// Field names match the model 1:1. Pointers are optional, Opt is nullable.
type UserUpdate struct {
    Email  *string
    Name   *string
    Age    crud.Opt[int]
    Active *bool
}

var Users = sqlrepo.Define[User, int64, UserUpdate]("users")
```

`Define` validates the tags, the ID type and the DTO **eagerly**, so a broken
mapping panics at package initialisation rather than on the first request.

Open the database — or hand vv one you already have. `vvdb` takes one struct
with `yaml` and `env` tags and answers a handle for PostgreSQL, MySQL, MariaDB
or SQLite; the driver stays your own blank import, and the connection stays
yours ([`D-057`](docs/ai/decisions/D-057-the-application-opens-the-connection.md)):

```yaml
db:
  engine: postgres
  host: localhost
  port: 5432
  user: vv
  password: vv
  name: app
  pool: { max_open: 20 }
  replica: { host: replica.internal }   # inherits everything it does not restate
```

```go
primary, replica := vvdb.MustOpenReadWrite(cfg.DB) // database/sql, pools sized
src := crudsql.Postgres(primary)
if replica != nil {
    src = crud.ReadWrite(src, crudsql.Postgres(replica))
}
```

For pgx, use `dbpgx.MustConnectReadWrite(ctx, cfg.DB)` instead. These are
alternative driver families for the same configuration.

Bind it to a datasource:

```go
db    := crudpgx.Open(pool)                  // pgx
db    := crudsql.Postgres(sqlDB)             // database/sql
db    := crudsql.MySQL(sqlDB)                // MySQL
db    := crudsql.MariaDB(sqlDB)              // MariaDB — same dialect, different error numbers

users := Users.Bind(db)
```

…and use it:

```go
u := User{TenantID: 1, Email: "ann@x.io", Name: "Ann", Age: crud.Set(31)}
err := users.Save(ctx, &u)          // INSERT; u.ID and u.CreatedAt are filled in

got, err := users.GetByID(ctx, u.ID)
got, err = users.Update(ctx, u.ID, UserUpdate{Name: ptr("Anna")})

page, err := users.Get(ctx, crud.Page(2), crud.Limit(20),
    crud.Where(crud.Eq("Active", true)),
    crud.OrderBy(crud.Desc("CreatedAt")))

all, err  := users.GetAll(ctx)
n, err    := users.Delete(ctx, 1, 2, 3)
n, err     = users.DeleteAll(ctx, crud.Where(crud.Lt("Age", 18)))
```

---

## The surface

| Method | Meaning |
| --- | --- |
| `GetByID(ctx, id)` | one row, or `crud.ErrNotFound` |
| `Get(ctx, opts...)` | `PaginatedResponse[M]` |
| `GetAll(ctx, opts...)` | every match; unpaged unless an option says otherwise |
| `Save(ctx, *M)` | JPA semantics: no key → INSERT, key → UPSERT; the model is refreshed in place |
| `SaveAll(ctx, []*M)` | the same write, batched into one statement |
| `Update(ctx, id, dto)` | load, diff, write only what changed |
| `UpdateAll(ctx, dto, opts...)` | one `UPDATE` across a filter; returns how many rows were touched |
| `Delete(ctx, ids...)` | returns how many rows went away |
| `DeleteAll(ctx, opts...)` | same, filtered |
| `Count` / `Exists` | with the same options |
| `Aggregate(ctx, opts...)` | grouped summaries, under the same narrowing as a read |
| `Tx(ctx, fn)` | run in a transaction, joining one already in `ctx` |
| `Meta()` | the bound schema and table |

### Save

Zero primary key → `INSERT`. A `db:",auto"` key is left out of the column list
and read back (`RETURNING` on PostgreSQL, `LastInsertId` on MySQL), along with
every other database-side default.

Non-zero key → upsert (`ON CONFLICT DO UPDATE` / `ON DUPLICATE KEY UPDATE`).
Columns tagged `immutable` are *not* overwritten on conflict, so `created_at`
and `tenant_id` survive.

### Update

`Update` is the smart one: it loads the row, walks the DTO, and builds an
`UPDATE` out of the fields that are both **provided** and **actually
different**. Nothing provided, or nothing different, means no write at all.

Three intents, three types:

| DTO field | not provided | provided |
| --- | --- | --- |
| `T` | *impossible* — always applied | value |
| `*T` | `nil` | `*v` |
| `crud.Opt[T]` | zero value | `crud.Set(v)` → value, `crud.Null[T]()` → SQL `NULL` |

Which is exactly what a `PATCH` body needs:

```go
type UserUpdate struct {
    Name *string       `json:"name,omitempty"`
    Age  crud.Opt[int] `json:"age,omitzero"`
}

// {}                -> nothing happens
// {"name":"Anna"}   -> UPDATE users SET name = $1
// {"age":null}      -> UPDATE users SET age = NULL
// {"age":31}        -> UPDATE users SET age = 31
```

`Opt` decodes those three states straight out of JSON — an absent key never
reaches `UnmarshalJSON`, so it stays undefined — and implements
`driver.Valuer` / `sql.Scanner`, so it works as a model field for nullable
columns too.

Inside a transaction, the load is a `SELECT ... FOR UPDATE`. Outside one it is a
plain read — see **optimistic locking** below for the other way to make that
safe.

### Paging by cursor

Offset paging asks the database to walk and discard what it skips, and it asks a
question whose answer moves: "skip 10, take 10" means something else after
somebody inserts a row above them, so a client walking a list sees one row twice
and misses another. Every paged read hands back the edges of its own page:

```json
{ "items": [ … ], "nextCursor": "eyJmIjpb…", "prevCursor": "eyJmIjpb…" }
```

Send one back and the offset stops mattering:

```go
page, err := users.Get(ctx, crud.OrderBy(crud.Desc("CreatedAt")), crud.Limit(20))
next, err := users.Get(ctx, crud.OrderBy(crud.Desc("CreatedAt")), crud.Limit(20),
    crud.After(page.NextCursor))
```

```bash
curl '/users?sort=-createdAt&limit=20&after=eyJmIjpb…'
curl -XPOST /users/query -d '{"sort":["-createdAt"],"limit":20,"after":"eyJmIjpb…"}'
```

`crud.Before` walks back. The token carries the sort's own field names, so a
cursor made for one sort is refused under another rather than compared against
whichever columns line up. It needs a unique sort — the primary key is appended
to a paged sort already, so that is the default; `sqlrepo.UnstablePagination`
removes it and gives up cursors with it. A nullable sort column is refused:
`NULL > 'x'` is unknown, so the boundary would drop every row that has one.

A cursor walk skips the `COUNT` — there is no page number for a total to divide
into — so `total` is the page length and `totalPages` is zero.

### UpdateAll

`Update` addresses one row; `UpdateAll` addresses a filter, the way `DeleteAll`
is `Delete`'s filtered partner. It is one statement, so "deactivate every user in
this tenant" costs one round trip rather than one `SELECT` and one `UPDATE` per
row.

```go
n, err := users.UpdateAll(ctx, UserUpdate{Active: ptr(false)},
    crud.Where(crud.Eq("TenantID", tenant)))
```

The DTO means the same three things it means on `Update`: undefined is never
written, `crud.Null[T]()` writes `NULL`. The one difference is forced by the
shape — there is no single row to diff against, so every field the DTO defines is
written to every matching row, including one whose value is already there. A DTO
that defines nothing writes nothing at all.

The count is the database's own, and the engines count differently: PostgreSQL
reports the rows it *matched*, MySQL the rows it actually *changed*. Under
`security.Gate` an `UpdateAll` with neither a policy scope nor a caller filter is
refused unless the policy sets `AllowUnscopedUpdateAll`, and `specs` spells it
`UpdateBy(ctx, spec, dto)` — which refuses an empty specification outright.

### Aggregates

`Count` was the only summary there was, so the first time an application needed
"unread per chat" it dropped to raw SQL — and raw SQL runs outside the scope, the
relation scopes and the security gate. The query that produces a number became
the query that reads another tenant's rows.

```go
rows, err := messages.Aggregate(ctx,
    crud.GroupBy("ChatID"),
    crud.Aggregate(crud.CountAll("unread"), crud.Max("latest", "CreatedAt")),
    crud.Where(crud.Eq("Read", false)),
)
for _, r := range rows {
    chat := r.Group["ChatID"]
    n, _ := r.Int("unread")
}
```

`CountAll CountOf CountDistinct Sum Avg Min Max`, and that is the whole set —
the five functions the three dialects spell identically. Every name is resolved
against the model, so a typo is a refusal rather than a statement the database
rejects.

It is a method on the seam, which means a decorator can intercept it: the
security gate scopes an aggregate exactly as it scopes a page. It is deliberately
**not** reachable from the wire DSL — a total is a disclosure, and `GROUP BY`
over a client-chosen column is an oracle. Publish the totals your application
means to publish.

### Soft deletes

One declaration, both halves:

```go
var Docs = sqlrepo.Define[Doc, int64, DocUpdate]("docs",
    sqlrepo.SoftDelete("DeletedAt"))
```

`Delete` and `DeleteAll` stamp the column instead of removing rows, and every
read filters the stamped ones out. Written by hand this is a scope for the reads
plus a service layer for the writes, and adding the first while forgetting the
second fails silently — the reads hide rows the deletes are still destroying.

The column has to be nullable, because "not deleted" needs a value. Bind the same
blueprint without the setting for a repository that sees the tombstones. What it
cannot do for you: a unique index still sees them, so re-creating a row whose
soft-deleted twin holds the same key is a conflict — that wants a partial index.

### Batched writes

```go
err := users.SaveAll(ctx, []*User{&a, &b, &c})
```

One `INSERT`, the same upsert semantics, the same three `Opt` states. Two things
it will not do quietly: a batch that mixes rows the database keys with rows you
keyed is refused rather than split, because splitting would hide the round trips;
and generated keys come back only where the dialect has `RETURNING`. MySQL
reports one `LastInsertId` for the statement and only guarantees the rest are
contiguous under some settings, so it does not guess — assign the keys yourself
there and the batch is exact.

### Replicas

```go
src := crud.ReadWrite(crudsql.Postgres(primary), crudsql.Postgres(replica))
users := Users.Bind(src)
```

Reads go to the replica, writes to the primary. Two things are not negotiable,
both because a replica is *behind*: a read inside a transaction goes to that
transaction, and a read that decides a write never leaves the primary — the load
half of an `Update`, and every check the security gate makes. What is left is
yours: write, then read in a separate call before the replica catches up, and the
row is missing. Wrap the pair in a transaction, or read with `crud.PrimaryOnly()`.

### Optimistic locking

`Update` is load-then-write, and between those two statements somebody else can
write the same row. Tag an integer column `version` and that stops being possible:

```go
type Article struct {
    ID      int64  `db:"id,pk,auto"`
    Title   string `db:"title"`
    Version int    `db:"version,version"`
}
```

Every `Update` then pins its write to the version it read and advances it:

```sql
UPDATE articles SET title = $1, version = version + 1
WHERE id = $2 AND version = $3
```

If someone got there first the statement matches nothing, and the call returns
`crud.ErrStaleVersion` — which wraps `crud.ErrConflict`, so the HTTP handler
answers **409** — instead of quietly overwriting them. A row that is genuinely
gone is still `crud.ErrNotFound`: the two need different answers from the caller,
retry versus give up.

The column is the repository's, not the caller's. An update DTO that names it is
refused at `Define` time, `UpdateAll` advances it on every row it writes (a lock
only one write path respects protects nothing), and `Save` leaves it alone —
an upsert is a whole-row overwrite with no `WHERE` clause to check anything in,
so a `Save` from a stale model cannot wind the counter back either. Versions are
integers only: a timestamp version would need a clock, and two application
servers do not share one.

### Pagination

```go
type PaginatedResponse[T any] struct {
    Items      []T   `json:"items"`
    Page       int   `json:"page"`
    Limit      int   `json:"limit"`
    Total      int64 `json:"total"`
    TotalPages int   `json:"totalPages"`
    HasNext    bool  `json:"hasNext"`
    HasPrev    bool  `json:"hasPrev"`
}
```

Options: `Page`, `Limit`, `Offset`, `Where`, `OrderBy`/`SortBy`, `Unpaged`,
`Unsorted`, `SkipTotal`, `ForUpdate`, `Distinct`, `With`.

Three things happen automatically: the page size is clamped to `MaxLimit` —
including when a request asks to be `Unpaged`, which is a flag on the wire and
does not outrank the repository's own cap — a primary-key tiebreaker is appended
so pages do not shuffle under you, and the `COUNT` is skipped when the first page
already contains everything. `SkipTotal()` drops the `COUNT` entirely, answers
`HasNext` by fetching one row past the page, and reports `Total` as the size of
what came back, because nothing counted the rest.

`crud.Where` **ANDs** — it never replaces. That is what lets a decorator inject
a filter the caller cannot remove.

---

## Decorators

Decorators wrap a repository from the inside out, and every type parameter is
inferred from the argument, so call sites stay free of explicit generics.

```go
users := specs.Executor(Users.Bind(db,
    security.Gate(policy),                                        // row-level scope
    audit.Log(l),                                                 // your own
    faults.Enrich[User, int64](faults.WithProbe(probe.Full(cat))), // every violation, not just the first
))
```

`Bind`'s first decorator ends up outermost — and `faults.Enrich` belongs **last**,
so it wraps the repository directly and every driver error is enriched before
anything above can see it. Writing your own is an embedded interface and one
method:

```go
type auditing struct{ crud.Core[User, int64] }

func (a auditing) Save(ctx context.Context, u *User) error {
    log.Println("saving", u.Email)
    return a.Core.Save(ctx, u)
}

func Log() crud.Middleware[User, int64] {
    return func(next crud.Core[User, int64]) crud.Core[User, int64] {
        return auditing{next}
    }
}
```

### specs — JPA Specifications and the Criteria API

| JPA | vv |
| --- | --- |
| `Specification<T>` | `specs.Specification[M]` |
| `Root<T>`, `CriteriaBuilder` | `specs.Root[M]`, `specs.Builder` |
| `Specification.where(a).and(b).or(c).not()` | `specs.Where(a).And(b).Or(c).Not()` |
| `JpaSpecificationExecutor<T>` | `specs.Executor(repo)` |
| generated `User_` metamodel | `specs.Metamodel[User, userAttrs]()` |

The literal form:

```go
func IsActive() specs.Specification[User] {
    return specs.Of[User](func(root specs.Root[User], cb specs.Builder) crud.Predicate {
        return cb.Equal(root.Get("Active"), true)
    })
}

adults := specs.Where(IsActive()).And(specs.Of[User](
    func(r specs.Root[User], cb specs.Builder) crud.Predicate {
        return cb.GreaterThanOrEqualTo(r.Get("Age"), 18)
    }))
```

The metamodel form — same result, checked by the compiler:

```go
type userAttrs struct {
    ID        specs.Ord[User, int64]
    Email     specs.Str[User]
    Age       specs.Ord[User, int]
    Active    specs.Attr[User, bool]
    CreatedAt specs.Cmp[User, time.Time]
}

var User_ = specs.Metamodel[User, userAttrs]()   // validated at init

adults := specs.Where(User_.Active.Eq(true)).And(User_.Age.Gte(18))
```

Query with it:

```go
sp := specs.Executor(users)

one,  err := sp.FindOne(ctx, User_.Email.Eq("ann@x.io"))   // ErrNotFound / ErrNotUnique
list, err := sp.FindAll(ctx, adults, crud.OrderBy(User_.Age.Desc()))
page, err := sp.FindPage(ctx, adults, crud.Page(2), crud.Limit(20))
n,    err := sp.CountBy(ctx, adults)
ok,   err := sp.ExistsBy(ctx, User_.Email.Eq("ann@x.io"))
n,    err  = sp.DeleteBy(ctx, User_.Active.Eq(false))
```

`count`, `exists` and `delete` carry a `By` suffix because Go has no
overloading and the basic names are already taken. A specification also works
as a plain option — `users.Get(ctx, specs.As(adults), crud.Page(2))` — so you
are never forced into the decorator.

### security — the gate

Three independent things, all optional:

- **Scope** — a predicate ANDed into every read and every scoped write. Row-level
  security. Because the predicate AST is closed (`Raw` is the only escape hatch),
  a caller cannot peel it back off.
- **Authorize** — coarse: may this principal do this kind of thing at all.
- **Inspect** — fine: per entity, seeing the actual row.

Multi-tenancy is one line:

```go
policy := security.ScopeField[Doc, int64]("TenantID", func(ctx context.Context) (any, error) {
    t, ok := ctx.Value(tenantKey{}).(int64)
    if !ok {
        return nil, security.Denied(security.Read, "no tenant in context")
    }
    return t, nil
})

docs := Docs.Bind(db, security.Gate(policy))
```

From there:

- reads are filtered in SQL;
- a foreign id returns `ErrNotFound`, never `ErrForbidden` — a 403 would confirm
  that the row exists;
- `Save` into another tenant is refused, and so is overwriting a row that
  belongs to one;
- `TenantID` is frozen, so an update DTO that names it is rejected before any
  SQL runs;
- an unscoped `DeleteAll` is refused unless `AllowUnscopedDeleteAll` is set.

A scope is a `WHERE` clause, and a `WHERE` clause only constrains its own
`FROM`. A preload is a second statement against a second table, so it inherits
nothing — which is how `?preload=comments` hands back exactly the rows the scope
existed to hide. Narrow the far side too:

```go
policy := security.Combine(
    security.ScopeField[Article, int64]("TenantID", tenantOf),
    security.ScopeRelationField[Article, int64]("Comments", "TenantID", tenantOf),
)
```

The path is resolved at declaration time, so a typo panics at start-up rather
than leaking rows later, and it may be several hops (`"Comments.Author"`). This
is the per-principal form; the per-table form is `sqlrepo.RelationScope` on the
blueprint, and where both are declared both apply.

Also available: `security.ReadOnly`, `security.Freeze(fields...)` and
`security.Combine(policies...)`, which ANDs scopes, merges relation narrowings
and chains checks.

### faults — the field a violation happened at

```go
users := Users.Bind(db, faults.Enrich[User, int64]())
```

An adapter classifies a refused write but cannot name a path — a column is
meaningless without the table it belongs to, and an adapter has no `crud.Meta`.
This decorator is that hop, and it is where `probe.Full` plugs in to find every
*other* violation the same payload caused. It is the innermost decorator, and it
never invents a fault or a path.

The whole subsystem is [its own section](#errors).

---

## auth — who the caller is

`security.Policy` has always asked "who is calling?" and always got its answer
from a closure over the context. `auth` is the vocabulary that closure can now
share.

Three layers, and you can take any one of them:

**The contract** — `auth`. A `Principal` is four methods, a `Permission` is a
string with a type, and the context key is one. Nothing else.

**A provider** — `auth/authjwt` or `auth/apikey`. The JWT parser is generic over
*your* claims struct and mentions nothing of ours:

```go
type MyClaims struct {
    Subject string `json:"sub"`
    Tenant  int64  `json:"tenant"`
}

parser := authjwt.New[MyClaims](authjwt.HMAC(secret),
    authjwt.Issuer("https://id.example.com"),
    authjwt.Audience("articles-api"))

claims, err := parser.Parse(ctx, token)
```

Stop there if a parser is all you wanted. The bridge to `Principal` is a
separate call, and `authjwt.Standard` is both calls for the ordinary shape of
token.

**A transport** — `auth/http/authnet`, `auth/http/authgin`, `auth/http/authfiber`,
`auth/rpc/authgrpc`. One line each:

```go
guard := auth.NewGuard(authjwt.Standard(authjwt.HMAC(secret), roles,
    authjwt.Issuer("https://id.example.com"),
    authjwt.Audience("articles-api")))

r.Use(crudgin.Errors(), authgin.Middleware(guard))
```

Then the policy reads the principal instead of a key you invented:

```go
policy := security.Combine(
    security.PerAction[Article, int64](map[security.Action]auth.Permission{
        security.Read:   "article:read",
        security.Create: "article:write",
        security.Update: "article:write",
        security.Delete: "article:delete",
    }),
    security.ScopeAttr[Article, int64]("TenantID", "tenant"),
)

articles := Articles.Bind(db, security.Gate(policy))
```

`ScopeAttr` is `ScopeField` with the extractor filled in, so it inherits the
whole of that helper: reads narrowed in SQL, a create into another tenant
refused, and `TenantID` frozen against updates. `ScopeSubject` narrows to rows
the caller owns; `ScopeRelationAttr` carries the claim across a relation, which
still has to be declared ([the reason](#security--the-gate)).

What it refuses, and refuses loudly:

- a signing algorithm nominated by the token — the key declares what it verifies,
  so `alg: none` and the RSA-public-key-as-HMAC-secret forgery are both refused
  before a key is consulted;
- a JWT parser with no issuer and no audience — `New` panics at start-up, and
  `AllowAnyIssuer()` / `AllowAnyAudience()` are how you say it is deliberate;
- a token with no `exp`;
- a claim the principal does not carry — a missing tenant is a denial, never a
  zero value that compiles to `WHERE tenant_id = 0`;
- an absent principal, at every policy, with no statement executed.

And what it will not tell a client. Every authentication failure is one answer —
`401`, `unauthenticated`, *"authentication is required"* — whether the token was
absent, expired, forged, for another audience, or valid for a tenant that no
longer exists. A 401 that distinguishes its reasons is a user-enumeration oracle
in the same way a 403 on a hidden row is a row oracle
([`D-056`](docs/ai/decisions/D-056-an-authentication-failure-is-a-fault-that-wraps-a-sentinel.md)).
The reason is kept, inside, for a log.

An optional guard is available and does not weaken that: a request with no
credential proceeds anonymously, and a token that fails to verify is still
refused. Note that anonymous plus a gated repository is still a 401 — it arrives
from the repository rather than from the door.

Running code: [`_examples/auth-jwt-gin`](_examples/auth-jwt-gin/) is the whole
chain in one file and prints three tokens on start-up.

---

## Relations

Tag a field and it becomes a navigable edge. Everything else — the foreign key,
the target table, the join direction — is inferred, and overridable.

```go
type Article struct {
    ID       int64  `db:"id,pk,auto"`
    AuthorID int64  `db:"author_id"`
    Title    string `db:"title"`

    Author   *Author   `rel:"belongs_to"`                      // fk: AuthorID
    Comments []Comment `rel:"has_many"`                        // fk: ArticleID on Comment
    Tags     []Tag     `rel:"many_to_many,join=article_tags"`  // article_id / tag_id
}
```

| Tag | Foreign key | Overrides |
| --- | --- | --- |
| `belongs_to` | `<Field>ID` on this model | `fk=`, `ref=`, `table=` |
| `has_one` / `has_many` | `<ThisModel>ID` on the target | `fk=`, `ref=`, `table=` |
| `many_to_many` | the join table's two columns | `join=`, `joinFK=`, `joinRef=` |
| `rel:""` | inferred from the Go type | |
| `rel:"-"` | never a relation | |

Target tables resolve from `sqlrepo.Define`'s registration, then a `TableName()`
method, then the snake_case plural. Struct-shaped fields *without* a `rel` tag
are skipped entirely — never mapped as a column by accident.

### Filtering across relations

Any path may cross an edge, at any depth, in filters and sorts:

```go
users.Get(ctx, crud.Where(crud.Eq("Author.Name", "Ann")))
users.Get(ctx, crud.Where(crud.In("Tags.Slug", "go", "rust")))
users.Get(ctx, crud.Where(crud.Contains("Comments.Author.Name", "bo")))
```

Each hop renders as a **correlated `EXISTS` subquery**, not a join:

```sql
SELECT … FROM "articles"
WHERE EXISTS (SELECT 1 FROM "authors" AS rx1
              WHERE rx1."id" = "articles"."author_id" AND rx1."name" = $1)
```

That choice is the whole point. A join against a to-many relation multiplies the
result set — an article with two matching tags becomes two rows, `LIMIT 20`
returns fewer than twenty distinct articles, and `COUNT(*)` reports a number
that does not exist. `EXISTS` is a semi-join: one row in, one row out, and the
planner still turns it into a hash semi-join. `NOT (…)` around a to-many path
means "no related row matches", which is what you wanted anyway.

Sorting by a related column uses a scalar subquery instead, and only through
to-one edges — sorting by a collection has no single value, so it is refused
rather than silently picking one.

### Preloading

```go
articles.Get(ctx, crud.Preload("Author", "Comments.Author", "Tags"))
articles.GetByID(ctx, id, crud.Preload("Author"))
articles.Get(ctx, crud.PreloadWhere("Comments", crud.Where(crud.Eq("Approved", true))))
```

One batched statement per relation per level — never one per row. Paths sharing
a prefix share a query, keys are deduplicated, and long lists chunk into
900-key batches. A `Select()` projection automatically keeps the columns the
preloads join on.

Pagination inside a preload is refused: a `LIMIT` over a batched load would
truncate some parents' children and not others.

---

## The query DSL

`query.Request` is one JSON document that compiles into `crud.Options`. Every
path is resolved against the model *before* any SQL exists, so an unknown field
is a 400 and never a silently dropped clause.

```json
{
  "page": 2, "limit": 20,
  "sort": ["-createdAt", "author.name"],
  "preload": ["author", {"path": "comments", "filter": {"approved": true}}],
  "select": ["id", "title"],
  "search": "generics", "searchFields": ["title", "body"],
  "filter": {
    "views":       {"gte": 100, "lt": 1000},
    "status":      ["draft", "review"],
    "author.name": {"contains": "an"},
    "tags.slug":   {"in": ["go", "rust"]},
    "publishedAt": {"isNull": false},
    "or":  [ {"title": "a"}, {"and": [{"views": {"gt": 10}}, {"pinned": true}]} ],
    "not": {"title": "spam"}
  }
}
```

Operators: `eq ne gt gte lt lte like notLike ilike contains startsWith endsWith
iContains iStartsWith iEndsWith
in notIn between isNull isNotNull`, with `$`-prefixed and symbolic aliases
(`$gte`, `>=`). A bare value means `eq`, a bare array means `in`, `null` means
`IS NULL`.

Field names are matched case- and separator-insensitively, so `createdAt`,
`created_at` and `CreatedAt` are the same column.

Three things this gets right that a hand-rolled filter layer usually does not:

- **Values are typed by their column.** `{"views": {"gte": 100}}` binds an `int`,
  not a `float64`; `{"createdAt": {"gte": "2026-01-02"}}` binds a `time.Time`. A
  column with a `TextUnmarshaler` parses through it, so uuid and enum types keep
  their own rules.
- **Search cannot escape its scope.** The OR over search fields is its own AST
  node, so it is always parenthesised. Concatenating `a LIKE ? OR b LIKE ?` into
  a `WHERE` is how a filtered list quietly starts returning everything.
- **Output is deterministic.** JSON objects have no order and Go maps have less;
  keys are sorted before compiling, so the same request always produces the same
  statement — and can therefore be tested.

The query-string form covers the same ground for `GET`:

```
?page=2&limit=20&sort=-createdAt,author.name&preload=author,comments.author
&select=id,title&q=generics&searchFields=title,body
&f=views:gte:100&f=tags.slug:in:go,rust&f=publishedAt:isNull:true
&filter={"or":[{"status":"draft"}]}
```

`f=field:op:value` repeats and ANDs. Only the first two colons are structural,
so timestamps survive. `filter=` takes the full JSON document for anything the
flat form cannot express.

### Bounding it

The DSL is driven by untrusted input, so `query.Config` bounds it:

```go
&query.Config{
    Filterable:  []string{"Title", "Views", "Author.*"},  // .* allows a subtree
    Sortable:    []string{"CreatedAt", "Views"},
    Preloadable: []string{"Author", "Comments", "Comments.Author"},
    Searchable:  []string{"Title", "Body"},
    MaxDepth:    4,  // and/or nesting, and path length
    MaxConditions: 32,
    MaxPreloads:   4,
}
```

Empty lists allow anything the model maps. Depth, condition and preload limits
apply either way.

---

## The HTTP handler

Four bindings, one API. Pick the one your project already uses:

```go
articles := specs.Executor(Articles.Bind(db, security.Gate(policy)))

app.Use("/articles", crudfiber.New(articles).Routes())   // Fiber v3
crudgin.New(articles).Mount(r, "/articles")              // Gin
crudnet.New(articles).Mount(mux, "/articles")            // net/http
crudgrpc.New(articles).Register(srv, "Article")          // gRPC
```

| Route | Does |
| --- | --- |
| `GET /` | list, query-string DSL |
| `POST /query` | list, full JSON DSL |
| `GET /count`, `POST /count` | count, same DSL |
| `GET /:id` | one entity, `?preload=…&select=…` |
| `POST /` | create |
| `PATCH /:id` | partial update through the DTO |
| `PUT /:id` | replace; where the database owns the key it will not create |
| `DELETE /:id` | delete one |
| `POST /bulk-delete` | `{"ids": […]}` |

`New` takes an **interface**, not a concrete repository:

```go
type Repository[M any, ID comparable, U any] interface {
    Meta() *crud.Meta
    GetByID(ctx context.Context, id ID, opts ...crud.Option) (M, error)
    Get(ctx context.Context, opts ...crud.Option) (crud.PaginatedResponse[M], error)
    …
}
```

`crud.Repo` and `specs.Repo` satisfy it, and so does your own service type — so
business rules go where they belong and the handler never notices:

```go
type articleService struct {
    specs.Repo[Article, int64, ArticleUpdate]   // embedded: satisfies the interface for free
}

func (s articleService) Save(ctx context.Context, a *Article) error {
    if err := s.checkQuota(ctx, a); err != nil { return err }
    return s.Repo.Save(ctx, a)
}

app.Use("/articles", crudfiber.New(articleService{…}).Routes())
```

`crudfiber.Repository`, `crudgin.Repository`, `crudnet.Repository` and
`crudgrpc.Repository` are the same type, so the service above satisfies all four
without a line of change — the integration suite mounts one service on all four
to keep that honest.

On create the body binds straight onto the model, then a database-generated key
and every `generated` column are cleared — a client cannot choose its own id or
forge a server-side timestamp. `PUT /:id` is held to the same rule from the other
side: where the database generates the key it replaces an existing row and never
creates one, so the id space stays the server's. (`AllowClientID` hands it over,
and a key the client owns anyway — a uuid, a slug — is unaffected.) Options cover
the rest: `WithQuery`,
`WithTransform` (presenters), `WithScope`, `BeforeSave`, `BeforeUpdate`,
`ReadOnly`, `AllowClientID`, `MaxBulk`, `WithErrorHandler`.

Errors map by sentinel, so the transport never imports the decorator that raised
them: `crud.ErrNotFound` → 404, `crud.ErrForbidden` (which `security.ErrForbidden`
wraps) → 403, `crud.ErrConflict` → 409, query and schema errors → 400 with the
offending path named, everything else → 500 with no detail. What kind of failure it is is decided once, in
`port`, and each protocol spells it: `port/porthttp` has the status table and
`crud/rpc/crudgrpc` has the `codes.Code` one, so there is one classification rather
than one per framework — and `Status(err) int` (or `crudgrpc.Code(err)`) is
exported if you render your own bodies. The status table lives in `port/` rather
than under `crud/` because the auth middleware answers through the same one
([[D-059]]); `crudhttp` re-exports every name it used to own. Wire the [error
subsystem](#errors) in and the same refusal also carries a machine code, the
field it happened at, and every *other* violation the payload caused.

Every option, every rule and every machine code is identical across the four.
What differs is mounting, which body encodings are accepted, what each router
does with a trailing slash or a method it does not have, and — on gRPC — that a
failure arrives as a status code plus `BadRequest` / `ErrorInfo` / `RetryInfo`
details rather than as the JSON envelope;
[`FL-013`](docs/ai/flows/FL-013-a-request-through-another-binding.md) has the table.

`crudnet` is worth a look even if your router is not `ServeMux`: every route
method on it is an ordinary `http.HandlerFunc`, so chi, gorilla/mux or
httprouter can register them one by one instead of calling `Mount`.

`crudgrpc` mounts eight methods — one per command — under
`vv.crud.v1.<Name>`, and every request and response is a
`google.protobuf.Struct` carrying the same JSON document the HTTP bindings
speak. There is no `.proto` to write and no `protoc` to install, which is also
why there is no server reflection: a resource generic over its model has no
compiled descriptor. Keys travel as strings, because a protobuf number is a
double.

---

## Calling it from another service

If one service declares a CRUD API, another consumes it. That side is
`remote`, and the point is that it is not a client: it is a `port.Repository`,
so the calling code is the code you would have written against a repository of
your own.

```go
articles := remote.New[Article, int64, ArticleInput](
    remotehttp.Transport("https://content.internal/articles"))

page, err := articles.Get(ctx,
    crud.Where(crud.Eq("Status", "draft")),
    crud.OrderBy(crud.Desc("CreatedAt")),
    crud.Limit(20))
```

Swap the transport for `crudgrpc.Transport(conn, "Article")` and nothing else
changes. Both need no generated code: the gRPC binding is one
`google.protobuf.Struct` in and out, so a call is `grpc.Invoke` with the
document in it.

**The filter crosses.** `crud.Where(crud.Eq(…))` is compiled into the same query
document the DSL speaks, so the far side receives the narrowing a local
repository would have received — not an approximation of it.

**The error branch you already wrote keeps working.**

```go
if _, err := articles.GetByID(ctx, 42); errors.Is(err, crud.ErrNotFound) {
    // the same branch, whichever side of the network the row is on
}
```

A refusal arrives as an `*errs.Fault` with its class, its code and every
violation it carried — path and machine code intact — and with nothing internal
in it. What did *not* come from this library is never read as if it had: a wrong
base URL, a proxy, an API gateway's own JSON 404 or a method a read-only service
never registered all arrive as a `*remote.ProtocolError`, so a misconfiguration
cannot masquerade as an empty table.

**And it composes.** A remote resource is a repository, so it mounts:

```go
crudnet.New(articles).Mount(mux, "/articles")   // your service is now a gateway
```

What cannot cross is refused by name before anything is sent — a relation scope,
a row lock, an aggregate, `crud.Raw` — because a narrowing that goes missing
between two services answers with more rows than were asked for, over a 200,
with nothing in the response to say so. The two options that *are* accepted and
cannot be honoured are named in
[`docs/modules/en/remote.md`](docs/modules/en/remote.md) rather than left to be
discovered.

---

## Errors

A database reports one violation at a time: the first constraint it reaches ends
the statement. A form with a taken email, a missing organisation and an under-age
user is three round trips, and the response echoes the driver.

Five packages fix that, and they turn on one at a time.

### 1. Say which engine is answering

```go
cls := sqlfault.New("postgres")            // or "mysql", "mariadb", "sqlite"
db  := crudsql.Postgres(sqlDB, crudsql.WithFaults(cls))
```

Now a refused write carries a **machine code** — `unique`, `foreign_key`,
`required`, `check`, `stale_version` — as well as `crud.ErrConflict`.

The engine is declared and never derived: `crud.MySQL` is MariaDB too, and the
two answer a failed `CHECK` with different numbers. So `crudsql.Open`, `From` and
`Source` classify the status and not the code, and the four named constructors
declare the engine.

`errs/sqlerr` holds four dialect tables, keyed three different ways, driven by a
corpus of 20 cases per engine **captured from live servers** rather than written
from documentation. That matters: the table it replaced was half wrong, and its
first run found that every SQLite constraint violation had been an unclassified
500 for as long as the dialect had been supported.

### 2. Name the field

```go
users := Users.Bind(db, faults.Enrich[User, int64]())
```

Adapters cannot fill in a path — a column is meaningless without the table it
belongs to, and an adapter has no `crud.Meta`. This decorator is that one hop.

### 3. Find the rest

```go
cat, err := catalog.Load(ctx, db)          // the schema, read once, at start-up
if err != nil { log.Fatal(err) }

users := Users.Bind(db, faults.Enrich[User, int64](
    faults.WithProbe(probe.Full(cat)),
))
```

`probe.Full` issues **one extra statement** — one boolean column per constraint
the write could have broken — and reports every violation it finds beside the one
the driver already reported. Three codes: `unique`, `foreign_key`, `restrict`,
plus intra-payload duplicates found with a map and no statement at all.

Everything it does is subordinate to the statement that actually failed. **The
index is the truth and the probe is advice**: nothing is invented, a probe that
fails keeps the driver's own violation and marks the answer `Partial`, and its
error is for your log rather than for the client.

CHECK expressions are not evaluated and NOT NULL, length and range are not
re-derived — every gap in either is a chance to report a violation the server
would not have raised.

### 4. Render it

The three HTTP bindings put one envelope on the wire:

```json
{ "type": "error", "partial": false, "errors": {
  "validation": [ {"field": ["user","email"], "error_code": "unique", "message": "…"} ],
  "general":    [ {"error_code": "restrict", "message": "…"} ]
}}
```

The group says **what the client can act on**, not where the failure came from —
which is why a 409 unique conflict appears under `validation`: it names a field
the client sent, so a form can mark it. gRPC carries the same violations as
`BadRequest` / `ErrorInfo` / `RetryInfo` details, with the same machine codes.

`partial` is not decoration. A response listing four violations without it is
claiming there were four.

Statuses come from the kind, never from the code, so a service can declare fifty
codes of its own without touching a status table:

| Kind | HTTP | gRPC |
|---|---|---|
| `KindNotFound` | 404 | `NotFound` |
| `KindUnauthorized` | 401 | `Unauthenticated` |
| `KindForbidden` | 403 | `PermissionDenied` |
| `KindRetryable` | 503 + `Retry-After` | `Unavailable` + `RetryInfo` |
| `KindConflict` | 409 | `AlreadyExists` |
| `KindValidation` | 422 | `InvalidArgument` |
| `KindBadRequest` | 400 | `InvalidArgument` |
| anything else | 500, **with no detail** | `Internal` |

### 5. Say it in the client's words

```go
//go:embed messages
var messages embed.FS

codes := errs.StandardCodes()
codes.Add("too_young", errs.KindValidation, "must be at least {min}")

cat, _ := errs.LoadMessages(codes, messages, "messages")

app.Use(crudfiber.Errors(porthttp.WithCodes(codes), porthttp.WithMessages(cat)))
```

One flat JSON file per locale, with a four-rung ladder per violation —
`user.email.unique → user.unique → email.unique → unique`, then the code's own
default. An override is as narrow or as broad as you need it, with no
configuration schema to learn.

### Your own service is a first-class producer

Business rules are not a lesser kind of error:

```go
return errs.Validation().
    Field("Age").Code("too_young").Params(errs.P{"min": 18}).
    Field("Email").Code(errs.CodeInvalidFormat).
    Fault()
```

And a validation library needs no adapter — `validator.FieldError` satisfies
`errs.FieldViolation` structurally, so neither package imports the other:

```go
vs := errs.FromFieldViolations("CreateUserRequest", verrs...)
```

A rule a validator refused and a constraint the database refused end up in **the
same list, of the same type**, told apart by `Origin`. Merging them is the point:
a payload with a malformed email *and* a taken email is two violations at one
path.

### It wraps, it never replaces

```go
errors.Is(err, crud.ErrConflict)   // still true — a branch written before any of this keeps working

if f, ok := errs.AsFault(err); ok {
    for _, v := range f.Violations { … }
}
```

Both on the same value, through as many further wrappings as a service layer
adds.

### Which field the client sees

A violation happens at a column; a client wants to hear about the key it sent.
Each layer translates **one hop and only its own**, and a layer that would have
to guess says so instead — the violation comes back marked approximate rather
than carrying an invented path.

`cmd/vv -adapter` generates the last hop from the mapping it inverts, so it is
*total*: `port.MustPathMap` refuses to boot if the map stops covering the model.
Without it, the renderer indexes the raw request body and matches by name — a
recognition rather than a mapping, and it declines rather than guessing when a
name folds to two leaves.

**None of this is on by default.** Wire nothing and a 409 is still a 409; you
just get no `error_code` and no `field`.

Full detail: [`docs/modules/en/errs.md`](docs/modules/en/errs.md),
[`sqlerr`](docs/modules/en/sqlerr.md), [`sqlfault`](docs/modules/en/sqlfault.md),
[`catalog`](docs/modules/en/catalog.md), [`probe`](docs/modules/en/probe.md),
[`faults`](docs/modules/en/faults.md), [`porthttp`](docs/modules/en/porthttp.md),
[`crudhttp`](docs/modules/en/crudhttp.md).

---

## Codegen

The update DTO and the metamodel are mechanical restatements of the model, so
`cmd/vv` writes them:

```go
//go:generate go run github.com/frostgrove/vv/cmd/vv
```

From this:

```go
type Article struct {
    ID          int64               `db:"id,pk,auto"`
    Title       string              `db:"title"`
    Rating      *float64            `db:"rating"`
    PublishedAt crud.Opt[time.Time] `db:"published_at"`
    TenantID    int64               `db:"tenant_id,immutable"`
    CreatedAt   time.Time           `db:"created_at,generated"`

    Author   *Author   `rel:"belongs_to"`
    Comments []Comment `rel:"has_many"`
}
```

it produces the DTO — pointers for optional columns, `Opt` for nullable ones,
and nothing at all for the key, the immutable and the generated columns:

```go
type ArticleUpdate struct {
    Title       *string             `json:"title,omitempty"`
    Rating      crud.Opt[float64]   `json:"rating,omitzero"`
    PublishedAt crud.Opt[time.Time] `json:"publishedAt,omitzero"`
}
```

and the metamodel, expanded through relations:

```go
var Article_ = specs.Metamodel[Article, ArticleAttrs]()

Article_.Views.Gte(100)                 // "views" >= $1
Article_.Author.Name.Eq("Ann")          // EXISTS (… authors … name = $1)
Article_.Comments.Approved.Eq(true)     // EXISTS (… comments … approved = $1)
Article_.Author.Name.Desc()             // ORDER BY (SELECT … LIMIT 1) DESC
```

Every one of those is compile-time typed and validated against the schema at
package initialisation, so a renamed column breaks the build rather than a
request. Relation expansion stops at `-depth` (2 by default) and never walks
back into a model already on the path.

The file also ends with an assertion that the DTO still covers every writable
column:

```go
func init() {
    port.MustCoverUpdate[Article, ArticleUpdate]()
}
```

Add a column and forget to regenerate, and the package **refuses to start**,
naming the column. It reads the compiled struct rather than the generator's view
of the source, which is what lets it disagree with the checked-in file at all —
regenerating and diffing only ever measures the generator against itself.

### `-adapter`: the rest of the resource

Off by default. With it, the generator also writes the API's own request body,
the mapper onto the model, **the inverse of that mapping**, a service shell and
`net/http` wiring:

```go
type ArticleInput struct{ … }                       // the create/replace body
type ArticleMapper struct{}                         // port.Mapper + errs.Resolver
var  ArticlePaths = port.MustPathMap[Article](port.PathMap{ … })
type ArticleService struct{ *port.DefaultService[Article, int64, ArticleUpdate] }
func MountArticle(mux *http.ServeMux, prefix string, svc, opts ...)
```

`ArticlePaths` is why the flag exists. It maps a model field back to the key the
client sent, so an error body names `authorID` rather than `AuthorID` — and
because it is generated with the mapping it inverts, `MustPathMap` can insist it
covers every column a request carries, and refuse to boot when it stops.
A hand-written inverse is wrong the first time somebody renames a key, and the
symptom is a wrong `field` in a production error body.

The generated body derives its JSON names from the Go field names, not from the
model's own `json` tags. That is deliberate — one rule for both bodies, so one
inverse map serves the resource — and it means a generated resource has a wire
shape of its own. Mount with `New` and generate no adapter if the model's shape
is the API you want.

`-binding net` (the default) writes the `net/http` wiring; `-binding none` leaves
it out, for mounting on Fiber or Gin with `ServingFor` yourself.

[`_examples/example/blog`](_examples/example/blog) is the worked example: `model.go` is what you write,
`vv_gen.go` is what comes out — with `-adapter`, so both halves are visible —
and a test regenerates and diffs so the two cannot drift.

---

## Adapters and interop

One adapter covers everything that speaks `database/sql`:

| Stack | How |
| --- | --- |
| `*sql.DB` / `*sql.Tx` / `*sql.Conn` | `crudsql.Postgres(db)` / `MySQL` / `MariaDB` / `SQLite`, or `crudsql.Open(db, crud.Postgres{})`; `crudsql.From(tx)` |
| pgx v5 (`*pgxpool.Pool`, `*pgx.Conn`, `pgx.Tx`) | `crudpgx.Open(pool)`, `crudpgx.From(tx)` |
| sqlx | `crudsql.From(sqlxTx)` — it is a `*sql.Tx` underneath |
| gorm | `crudsql.From(tx.Statement.ConnPool)` inside `db.Transaction` |
| ent (`--feature sql/execquery`) | `crudsql.From(entTx)` — `*ent.Tx` has `ExecContext`/`QueryContext` |
| sqlc (database/sql) | `crudsql.From(tx)`; the same `*sql.Tx` goes to `sqlc.New(tx)` |
| sqlc (pgx) | `crudpgx.From(tx)`; `pgx.Tx` satisfies sqlc's `DBTX` and vv's `Queryer` at once |
| bun, squirrel, dbr, … | `crudsql.From(tx)` |

The four named constructors say which *engine* is answering, and that is what
lets a refused statement come back carrying a code — `unique`, `foreign_key`,
`required` — as well as `crud.ErrConflict`. `Open`, `From` and `Source` are given
a `crud.Dialect`, which says how to write SQL and not which server is speaking:
`crud.MySQL` is MariaDB too, and the two answer a failed `CHECK` with different
numbers. So those three classify the status and not the code, and vv refuses to
guess rather than answering "mysql" for a MariaDB server. Pass
`crudsql.WithFaults(sqlfault.New("postgres"))` — or `"mysql"`, `"mariadb"`,
`"sqlite"` — to say which engine a joined transaction is talking to.

The interop point is exactly one function:

```go
ctx = crud.WithExecutor(ctx, crudsql.From(tx))
```

Every repository call made with that context runs on that executor. A new
framework means finding where it hides its transaction and wrapping it — three
lines.

That capture is unconditional, and it has to be: the transaction ent or gorm
hands over has no relationship to the source a repository holds, so there is
nothing to check it against. **When a process talks to more than one database,
say which one you mean:**

```go
ctx = crud.WithExecutorFor(ctx, mainDB, crudsql.From(tx))

users.Save(ctx, &u)    // bound to mainDB      — runs in tx
events.Save(ctx, &e)   // bound to analyticsDB — runs on analyticsDB
```

With the plain form that second call would have gone to `mainDB`, inside the
transaction, and reported success. `WithExecutorFor` matches on the datasource
handle, so naming `mainDB` and naming any `crud.Source` over it are the same
statement. A repository whose source cannot identify itself — a third-party
adapter that does not implement `crud.Identified` — is never matched by a scoped
binding and keeps the plain behaviour.

`Tx` and `InTx` scope the transactions they open the same way, so this holds
without you writing anything:

```go
err := users.Tx(ctx, func(ctx context.Context) error {
    users.Save(ctx, &u)    // in the transaction
    events.Save(ctx, &e)   // on its own database, and it survives a rollback here
    return nil
})
```

### Transactions

```go
// vv owns it
err := users.Tx(ctx, func(ctx context.Context) error {
    u, err := users.GetByID(ctx, 42)
    if err != nil { return err }
    _, err = users.Update(ctx, u.ID, UserUpdate{Name: ptr("new")})
    return err
})

// several repositories, one transaction
err = crud.InTx(ctx, db, func(ctx context.Context) error {
    if err := users.Save(ctx, &u); err != nil { return err }
    return orders.Save(ctx, &o)
})

// somebody else owns it
err = gormDB.Transaction(func(tx *gorm.DB) error {
    ctx := crud.WithExecutor(ctx, crudsql.From(tx.Statement.ConnPool))
    return users.Save(ctx, &u)
})
```

`Tx` **joins** a transaction already in the context rather than nesting inside
it: the outer owner keeps control of commit and rollback, and `fn` cannot roll
back independently. When you do want independent nesting, `Begin` on a
transaction gives you a savepoint — natively on pgx, via `SAVEPOINT` on
`database/sql`.

### Bulk

`crudpgx` implements `crud.BulkInserter` with `COPY`. **Nothing in the library
reaches for it.** `SaveAll` writes one multi-row `INSERT` whatever the executor
underneath can do, so this is a door you open yourself:

```go
if bulk, ok := src.(crud.BulkInserter); ok {
    n, err := bulk.CopyFrom(ctx, "users", cols, rows)
}
```

The call runs on the handle that executor holds and ignores any transaction in
the context.

---

## Struct tags

`db:"column,option,option"`. Without a tag a field is still mapped, with the
column derived from the Go name in `snake_case`.

| Option | Meaning |
| --- | --- |
| `pk` | primary key (falls back to a field named `ID`, or a column named `id`) |
| `auto` | database-generated on insert; the default for integer primary keys |
| `noauto` | opt an integer primary key out of that default |
| `immutable` | written on insert, never on update (`created_at`, `tenant_id`) |
| `generated` | never written; read back after every write (computed columns, triggers) |
| `version` | optimistic lock: an integer vv advances and checks on every update |
| `-` | ignore the field |

Embedded structs are flattened. `time.Time`, `sql.Null[T]`, `crud.Opt[T]` and
anything with a `Valuer`/`Scanner` count as a single column.

---

## Testing without a database

`crud/crudtest` is a `crud.Source` that records the SQL a repository produces and
replays canned rows back at it:

```go
rec := crudtest.Postgres().Push(crudtest.Rows(
    []any{int64(1), int64(7), "ann@x.io", "Ann", 31, true, time.Now()},
))
u, err := Users.Bind(rec).GetByID(ctx, 1)

rec.Last().SQL // SELECT "id", ... FROM "users" WHERE "id" = $1 LIMIT 1
```

The whole unit suite in this repository is built on it — statement shape, bind
order, pagination arithmetic and decorator composition are all checked without
Docker.

---

## Running the tests

```sh
make unit          # no database needed
make integration   # docker compose up + the full suite
make test          # both
```

The integration suite runs **one conformance suite** — 18 subtests covering
inserts, upserts, null semantics, partial updates, pagination, 18 predicate
forms, specifications, the security gate and transactions — against every
target:

| | PostgreSQL | MySQL |
| --- | --- | --- |
| pgx v5 | ✓ | — |
| database/sql | ✓ | ✓ (both upsert forms) |
| sqlx | ✓ | ✓ |
| gorm | ✓ | ✓ |
| ent | ✓ | ✓ |

…and once more against an in-process SQLite (`modernc.org/sqlite`, pure Go, no
container), which is the third dialect: `RETURNING` like PostgreSQL, `?` markers
like MySQL, and no row locks at all.

**MariaDB is a fourth engine**, run through `crudsql.MariaDB` against
`mariadb:11.4` in the compose file. It shares `crud.MySQL`'s dialect and does not
share its error numbers, which is why it is a separate target rather than a
footnote: it answers a failed `CHECK` with `4025 / 23000` where MySQL answers
`3819 / HY000`, and `1366` is `22007` there and `HY000` on MySQL. The claim that
`crud.MySQL` "targets MySQL and MariaDB" had never been run before it landed.

sqlc is not in that table on purpose: it generates queries, it is not a
`crud.Source`. What is worth proving about it — that one transaction can feed
sqlc's generated queries and an vv repository at once — is proved three
times below, on pgx, on `database/sql` and on MySQL. The vv code path
underneath is byte-for-byte the pgx and `database/sql` one.

Plus, on both databases:

- **shared-transaction tests** for each driver — one physical transaction, both
  libraries reading each other's uncommitted writes, and a rollback that takes
  everything with it;
- **relation tests** — every edge kind, two-hop paths, negation, nested sorts,
  batched and filtered preloads, and a check that a to-many filter neither
  duplicates rows nor inflates `COUNT`;
- **transport tests** — the handler driven end to end through Fiber, Gin,
  `net/http` and gRPC, including the full JSON DSL, pagination arithmetic, the
  create/patch/delete lifecycle, a service layer intercepting a write, and every
  rejection path. The three HTTP bindings answer the same tests, name for name
  and file for file; the gRPC one answers the subset of them that is not about
  HTTP, and one test mounts a single service value on all four and compares the
  *command* each handed over;
- **error tests** — the captured corpus replayed against each engine's parser,
  the probe's positive and negative controls (probe on ⇒ three distinct codes at
  three distinct paths; a payload with one real violation ⇒ exactly one), and a
  classified 409's body carrying nothing internal in all three bindings.

Adding a driver means adding a `Target`, never a test.

---

## Sharp edges

- **A column `DEFAULT` does not fire.** vv writes every mapped column, so an
  INSERT it builds names `active`, and creating a row without one stores the Go
  zero value rather than the column's default. A default only reaches rows the
  database makes on its own. Where the server must own a value, mark the column
  `generated` or fill it in a `BeforeSave` hook.
- **Rows affected diverge.** MySQL reports 0 for an `UPDATE` that changed
  nothing, and counts *matched* rather than *changed* rows depending on
  configuration. `ErrNotFound` is therefore never derived from `n == 0` on a
  write path.
- **`Tx` joins, it does not nest.** Documented above; use `Begin` for a
  savepoint when you need isolation.
- **`crud.WithExecutor` captures every repository, not just the ones on that
  database.** That is the interop seam working as designed — but across two
  databases it means a write can land in the wrong one and report success. Use
  `crud.WithExecutorFor(ctx, db, e)`, which is matched against the repository's
  own datasource.
- **`Update` is load-then-write.** Inside a transaction the load locks; outside
  one, two concurrent updates can interleave. Tag an integer column `version`
  and the second one is refused with `crud.ErrStaleVersion` instead.
- **Plain DTO fields are always applied**, slices included — a nil `[]byte` in a
  `T` field writes `NULL`. Use `*T` or `Opt[T]` for optional columns.
- **MySQL upserts** use the deprecated `VALUES()` by default for MariaDB and
  5.7 compatibility. `crud.MySQL{RowAlias: true}` switches to the `AS new` form
  (MySQL 8.0.19+). Both are covered by the integration suite.
- **`ForUpdate()` does nothing on SQLite.** SQLite locks the database, not the
  row, so there is no clause to render: the statement is still correct, and the
  serialisation has to come from the transaction instead.
- **`crud.Raw` is not validated.** Column names in a raw fragment are neither
  resolved nor quoted; that is the price of the escape hatch, and it is the one
  thing to grep for in review.
- **A relation filter is an `EXISTS`, not a join.** `Comments.Body contains "x"`
  means "has at least one comment matching", and negating it means "has none".
  That is almost always what was wanted, but it is not what a join would do.
- **Sorting through a to-many is refused**, because a collection has no single
  value to sort by. Sort by an aggregate in a view if you need it.
- **A preload is a second query, not a join.** It cannot be paginated, and it
  sees the same transaction as the parent read but not a snapshot of it.
- **Struct-shaped fields without a `rel` tag are skipped.** They are neither
  columns nor edges — which is what you want for a computed field, and a
  surprise if you expected an error.
- **SQLite enforces neither width, nor range, nor declared type.** A
  `VARCHAR(8)` stores 27 characters, 99999 goes into a small column, and `'abc'`
  stays text in an INTEGER column. The same payload is 422 on the two servers
  and 200 there, and the stored row then holds what the schema says it cannot.
- **`restrict` is reported as `foreign_key` on PostgreSQL and SQLite.** Both
  directions of a foreign-key violation are one error code with the same
  constraint name, and telling them apart would mean reading localised message
  text. MySQL and MariaDB report two numbers and keep the distinction.
- **`CodeExclusion` is reachable from no engine yet** — declared and rendered,
  produced by nothing, because no `EXCLUDE` has been provoked into the corpus.
- **The probe is advice, not truth.** It never invents a violation, its own
  failures go to your log rather than to the client, and a capped answer is
  marked `partial` rather than presented as complete.
- **A probe reads rows the caller may not be allowed to see.** A unique
  violation reveals that a value exists. `WithScope`, `Skip`, `CodeOnly` and
  leaving `WithValues` off narrow that; none of them closes it.

---

## Where to read next

| | |
| --- | --- |
| Everything one package can do, and how to wire it | [`docs/modules/`](docs/modules/en/Index.md) |
| Why is it like this? May I change it? | [`docs/ai/decisions/`](docs/ai/decisions/Index.md) |
| What must hold, in a consumer's language | [`docs/ai/usecases/`](docs/ai/usecases/Index.md) |
| Where does this happen, in which files? | [`docs/ai/flows/`](docs/ai/flows/Index.md) |
| Adopting an ORM you already use | [`docs/usage-guides/`](docs/usage-guides/) |
| What is left to build | [`docs/roadmaps/Roadmap.md`](docs/roadmaps/Roadmap.md) |
