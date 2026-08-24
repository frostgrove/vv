# rx-crud

A generic CRUD repository for Go with JPA-shaped semantics, a Specifications /
Criteria API, and a security gate — over any driver, without owning your
connection or your transaction.

Only two things ever cross the abstraction boundary: **run this statement** and
**give me rows**. Scanning stays with the mapper, dialect stays with the
repository. That is why any foreign transaction can be pushed into a context —
all rx-crud asks of it is `Exec` and `Query`.

```
crud/                       core: contracts, metadata, relations, predicates, Opt, pagination — stdlib only
repo/basic/                 the plain repository: the layer that speaks SQL
repo/decorators/specs/      JPA Specifications + Criteria API + metamodel
repo/decorators/security/   row-level scope, authorization, per-entity checks
query/                      the wire DSL: one JSON document -> crud.Options
http/crudhttp/              the transport-neutral half: status mapping, id coercion, sanitising
http/crudnet/               a full CRUD API on net/http — stdlib only, so it costs nothing
cmd/rxcrud/                 generates the update DTO and the metamodel from your model
adapter/crudsql/            database/sql — and therefore ent, gorm, sqlx, sqlc, bun, squirrel
crud/crudtest/              an in-memory source for unit-testing repositories

  ── separate modules, so you only download the one you use ──────────────────
http/crudfiber/             a full CRUD API on Fiber v3
http/crudgin/               the same API on Gin
adapter/crudpgx/            pgx v5
```

Declare a model, get a filtered, sorted, paginated, relation-loading HTTP API:

```go
app.Use("/articles", crudfiber.New(articles).Routes())   // Fiber
crudgin.New(articles).Mount(r, "/articles")              // Gin
crudnet.New(articles).Mount(mux, "/articles")            // or net/http, no dependency at all
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

**Already using an ORM?** Two task-oriented guides — bind the structs you
already have, generate the update DTO, mount the API, share transactions with
the ORM's own builders. Every claim in both is executed by the integration
suite, with one exception named where it is made: ent's hooks and privacy rules
would need a test schema that declares one, and none does.

- [`docs/usage-guides/ent.md`](docs/usage-guides/ent.md) — ent's *generated* entity struct is the model, as-is
- [`docs/usage-guides/gorm.md`](docs/usage-guides/gorm.md) — your gorm struct is the model, `gorm.Model`
  and associations included

**Prefer running code?** [`_examples/`](_examples/) has one small, complete program per
stack — ent, gorm, sqlx and no-ORM-at-all, across all three HTTP bindings and both
engines. Start the databases with `make up` and `go run ./<example>` serves a real API
you can curl. Each one is a single file you can read top to bottom.

---

## Install

```bash
go get github.com/shardit-io/rx                    # the library — and, on net/http, the whole of it
go get github.com/shardit-io/rx/http/crudgin       # …plus your HTTP framework, if you use one
go get github.com/shardit-io/rx/adapter/crudpgx    # …and pgx, if that is your driver
```

The library has **no external dependencies at all**. Anything that would add one
is a module of its own in the same repository, so you download the Fiber binding
or the Gin binding or neither, and pgx only if you use pgx. The `net/http`
binding and the `database/sql` adapter need nothing, so they ship in the library
— on `net/http` over `database/sql`, which is how ent, gorm, sqlx, sqlc and bun
are reached, the first line is the whole installation.

Versions move in lockstep: the library and every binding are tagged together, so
`@v0.1.0` means the same thing everywhere. No `replace` is ever needed
([`D-033`](docs/decisions/D-033-optional-dependencies-are-their-own-modules.md)).

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

var Users = basic.Define[User, int64, UserUpdate]("users")
```

`Define` validates the tags, the ID type and the DTO **eagerly**, so a broken
mapping panics at package initialisation rather than on the first request.

Bind it to a datasource:

```go
db    := crudpgx.Open(pool)                  // pgx
db    := crudsql.Postgres(sqlDB)             // database/sql
db    := crudsql.Open(sqlDB, crud.MySQL{})   // MySQL

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
to a paged sort already, so that is the default; `basic.UnstablePagination`
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
var Docs = basic.Define[Doc, int64, DocUpdate]("docs",
    basic.SoftDelete("DeletedAt"))
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
users := specs.Executor(Users.Bind(db, security.Gate(policy), audit.Log(l)))
```

`Bind`'s first decorator ends up outermost. Writing your own is an embedded
interface and one method:

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

| JPA | rx-crud |
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
is the per-principal form; the per-table form is `basic.RelationScope` on the
blueprint, and where both are declared both apply.

Also available: `security.ReadOnly`, `security.Freeze(fields...)` and
`security.Combine(policies...)`, which ANDs scopes, merges relation narrowings
and chains checks.

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

Target tables resolve from `basic.Define`'s registration, then a `TableName()`
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

Three bindings, one API. Pick the one your project already uses:

```go
articles := specs.Executor(Articles.Bind(db, security.Gate(policy)))

app.Use("/articles", crudfiber.New(articles).Routes())   // Fiber v3
crudgin.New(articles).Mount(r, "/articles")              // Gin
crudnet.New(articles).Mount(mux, "/articles")            // net/http
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

`crudfiber.Repository`, `crudgin.Repository` and `crudnet.Repository` are the
same type, so the service above satisfies all three without a line of change —
the integration suite mounts one service on all three to keep that honest.

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
offending path named, everything else → 500 with no detail. The table lives in
`http/crudhttp` and both bindings call it, so there is one mapping rather than
one per framework — and `Status(err) int` is exported if you render your own
bodies.

Every option, every status and every response shape is identical across the
three. What differs is mounting, which body encodings are accepted, and what each
router does with a trailing slash or a method it does not have;
[`FL-013`](docs/flows/FL-013-a-request-through-another-binding.md) has the table.

`crudnet` is worth a look even if your router is not `ServeMux`: every route
method on it is an ordinary `http.HandlerFunc`, so chi, gorilla/mux or
httprouter can register them one by one instead of calling `Mount`.

---

## Codegen

The update DTO and the metamodel are mechanical restatements of the model, so
`cmd/rxcrud` writes them:

```go
//go:generate go run github.com/shardit-io/rx/cmd/rxcrud
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

`example/blog` is the worked example: `model.go` is what you write,
`rxcrud_gen.go` is what comes out, and a test regenerates and diffs so the two
cannot drift.

---

## Adapters and interop

One adapter covers everything that speaks `database/sql`:

| Stack | How |
| --- | --- |
| `*sql.DB` / `*sql.Tx` / `*sql.Conn` | `crudsql.Open(db, crud.Postgres{})`, `crudsql.From(tx)` |
| pgx v5 (`*pgxpool.Pool`, `*pgx.Conn`, `pgx.Tx`) | `crudpgx.Open(pool)`, `crudpgx.From(tx)` |
| sqlx | `crudsql.From(sqlxTx)` — it is a `*sql.Tx` underneath |
| gorm | `crudsql.From(tx.Statement.ConnPool)` inside `db.Transaction` |
| ent (`--feature sql/execquery`) | `crudsql.From(entTx)` — `*ent.Tx` has `ExecContext`/`QueryContext` |
| sqlc (database/sql) | `crudsql.From(tx)`; the same `*sql.Tx` goes to `sqlc.New(tx)` |
| sqlc (pgx) | `crudpgx.From(tx)`; `pgx.Tx` satisfies sqlc's `DBTX` and rx-crud's `Queryer` at once |
| bun, squirrel, dbr, … | `crudsql.From(tx)` |

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
// rx-crud owns it
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

`crudpgx` implements `crud.BulkInserter` with `COPY`. Any executor that offers
it is used automatically; the core never learns about the driver.

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
| `version` | optimistic lock: an integer rx-crud advances and checks on every update |
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

sqlc is not in that table on purpose: it generates queries, it is not a
`crud.Source`. What is worth proving about it — that one transaction can feed
sqlc's generated queries and an rx-crud repository at once — is proved three
times below, on pgx, on `database/sql` and on MySQL. The rx-crud code path
underneath is byte-for-byte the pgx and `database/sql` one.

Plus, on both databases:

- **shared-transaction tests** for each driver — one physical transaction, both
  libraries reading each other's uncommitted writes, and a rollback that takes
  everything with it;
- **relation tests** — every edge kind, two-hop paths, negation, nested sorts,
  batched and filtered preloads, and a check that a to-many filter neither
  duplicates rows nor inflates `COUNT`;
- **HTTP tests** — the handler driven end to end through Fiber, Gin and
  `net/http`, including the full JSON DSL, pagination arithmetic, the
  create/patch/delete lifecycle, a service layer intercepting a write, and every
  rejection path. The three bindings answer the same 147 unit tests, name for
  name.

Adding a driver means adding a `Target`, never a test.

---

## Sharp edges

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
