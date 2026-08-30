# crud/sqlrepo — the repository

```go
import "github.com/frostgrove/vv/crud/sqlrepo"
```

**Module:** root · **Depends on:** `crud`, and the standard library

The layer that speaks SQL. Declare a model once, bind it to a datasource, and
you have the whole CRUD surface with JPA-shaped semantics — over any driver,
without owning your connection or your transaction.

---

## Quick start

```go
type User struct {
    ID        int64         `db:"id,pk,auto"`
    TenantID  int64         `db:"tenant_id,immutable"`
    Email     string        `db:"email"`
    Name      string        `db:"name"`
    Age       utils.Opt[int] `db:"age"`
    CreatedAt time.Time     `db:"created_at,generated"`
}

// Field names match the model 1:1. Pointers are optional, Opt is nullable.
type UserUpdate struct {
    Email *string
    Name  *string
    Age   utils.Opt[int]
}

var Users = sqlrepo.Define[User, int64, UserUpdate]("users")

users := Users.Bind(crudpgx.Open(pool))
```

`Define` validates the tags, the ID type and the DTO **eagerly**, so a broken
mapping panics at package initialisation rather than on the first request. Use
`TryDefine` where you want the error instead of the panic, and `sqlrepo.New` to
skip the blueprint and go straight to a bound repository.

### A table outside the default namespace

Keep the qualifier and table as two identifier components:

```go
var Events = sqlrepo.DefineInSchema[Event, int64, EventUpdate](
    "analytics", "events",
)
```

The first component means a schema on PostgreSQL, a database on MySQL/MariaDB,
and an attached database on SQLite. vv quotes both components independently:
`"analytics"."events"` on PostgreSQL/SQLite and
`` `analytics`.`events` `` on MySQL.

`Define("analytics.events")` is deliberately refused during declaration. A
dotted string is ambiguous with a quoted table whose literal name contains a
dot, so vv never guesses or silently splits it. `TryDefineInSchema` is the
error-returning form. Low-level metadata and adapters carry the same identity as
`crud.TableRef`; its components are exact and may themselves contain dots.

---

## The surface

| Method | Meaning |
|---|---|
| `GetByID(ctx, id, opts...)` | one row, or `crud.ErrNotFound` |
| `Get(ctx, opts...)` | `crud.PaginatedResponse[M]` |
| `GetAll(ctx, opts...)` | every match; unpaged unless an option says otherwise |
| `First(ctx, opts...)` | first matching row, or `crud.ErrNotFound` |
| `Save(ctx, *M)` | no key → INSERT, key → UPSERT; returns a new stored model and leaves its argument unchanged |
| `SaveOnly(ctx, *M)` | the same write with no stored-row result and no argument mutation |
| `SaveAll(ctx, []*M)` | write-only batch insert/upsert; never mutates its models |
| `Update(ctx, id, dto, opts...)` | load, diff, write only what changed |
| `UpdateAll(ctx, dto, opts...)` | one `UPDATE` across a filter; returns rows touched |
| `Delete(ctx, ids...)` | returns how many rows went away |
| `DeleteAll(ctx, opts...)` | the same, filtered |
| `Count(ctx, opts...)` / `Exists(ctx, opts...)` | with the same options |
| `Aggregate(ctx, opts...)` | grouped summaries, under the same narrowing as a read |
| `Tx(ctx, fn)` | run in a transaction, joining one already in `ctx` |
| `Meta()` | the bound schema and table |

### Save returns the stored row

A zero primary key means `INSERT`. A non-zero one means `UPSERT` ([[D-011]]).

A `db:",auto"` key is left out of the column list. `Save` returns a separate
model containing the stored row — `RETURNING` on PostgreSQL and SQLite, or an
insert/upsert followed by a read in one transaction on a dialect without it.
That includes every `generated` column and trigger normalisation; the `*M` you
passed is never changed.

`SaveOnly` performs only the write. It does not append `RETURNING`, does not
fetch a model and does not mutate its argument. Use it for writes whose
generated values are irrelevant.

`SaveAll` is likewise write-only. It is an `INSERT` on every adapter: a
driver's bulk-copy path is never reached for, because it takes its own handle
and would step outside a transaction the caller opened. pgx's `COPY` is there
as `crud.BulkInserter`, for an application that asks for it directly.

The dialect's bind budget is automatic. A fitting batch stays one statement. A
larger one is split on row boundaries, in caller order, after every model and
statement has passed preflight. All chunks join one transaction, so an error in
the last chunk cannot leave the first committed. Generated-key batches remain
write-only: `SaveAll` neither adds `RETURNING` nor mutates the models
([[D-079]]).

### Update is load, diff, write

`Update` loads the row, diffs the DTO against it and writes only what changed
([[D-010]]). A DTO field that is `nil` or `Undefined` is not in the statement at
all; an `Opt` that is explicitly null writes `NULL` ([[UC-003]]).

Inside a transaction the load locks. Outside one, two concurrent updates can
interleave — tag an integer column `version` and the second is refused with
`crud.ErrStaleVersion` instead ([[UC-009]]).

**Plain DTO fields are always applied**, slices included: a nil `[]byte` in a `T`
field writes `NULL`. Use `*T` or `Opt[T]` for optional columns.

`UpdateAll` is one statement across a filter and does **not** load first, so it
neither diffs nor advances a version column.

---

## Settings

Passed to `Define`, `TryDefine`, `DefineInSchema`, `TryDefineInSchema` or `New`,
and applied to every call.

| Setting | Does |
|---|---|
| `DefaultLimit(n)` | page size when a request asks for none. Default 20 |
| `MaxLimit(n)` | the cap. Clamps even an `Unpaged()` request |
| `DefaultSort(orders...)` | the sort when a request asks for none |
| `PreloadDepth(n)` | how deep a preload path may go. Default 5 |
| `Scope(pred)` | a predicate ANDed into every read and every scoped write |
| `RelationScope(path, pred)` | the same, on the far side of a relation |
| `SoftDelete(field)` | rows are flagged rather than removed, and hidden from every read |
| `UnstablePagination()` | drop the primary-key tiebreaker appended to every sort |
| `IndependentTable()` | keep an additional physical table for the model local to this blueprint |

`Scope` here is the **per-table** narrowing — it applies to everyone. The
**per-principal** form is [security](security.md), which reads the context.

Every `field` and `path` above is a model field name or a relation path, and the
generated metamodel answers both as identifiers — see [Typed, or by
name](#typed-or-by-name--both-spellings-work) below.

### Soft delete

```go
var Docs = sqlrepo.Define[Doc, int64, DocUpdate]("docs", sqlrepo.SoftDelete("DeletedAt"))
```

`Delete` and `DeleteAll` write the flag instead of removing the row, and every
read excludes flagged rows. It is a property of the statement rather than a
decorator, so nothing can be composed around it to see the hidden rows
([[D-031]], [[UC-016]]).

### Relation scopes

A scope is a `WHERE` clause, and a `WHERE` clause only constrains its own `FROM`.
A preload is a second statement against a second table, so it inherits nothing —
which is how `?preload=comments` hands back exactly the rows the scope existed to
hide ([[D-007]]).

```go
sqlrepo.RelationScope("Comments", crud.Eq("TenantID", 1))
```

The path is resolved at declaration time, so a typo fails at start-up rather than
leaking rows later. Where a blueprint scope and a security policy both declare a
narrowing for the same path, **both apply**.

### Canonical and independent tables

The default remains declarative: `Define("users")` validates the entire
blueprint and then publishes `users` as the one canonical relation target for
`User`. `Define("")` asks `User.TableName()` and then the plural convention.
An empty result from `TableName()` is a startup error, but its validation
preview publishes nothing; an explicit non-empty registration or declaration
can correct it and retry. Failed `TryDefine` calls reserve neither the root nor
relation targets traversed before a later invalid scope, so a corrected
declaration is not poisoned by the failed one. A table becomes immutable only
after a successful canonical declaration or actual `Relation.Target`
publication.

`IndependentTable()` is the explicit low-level seam for an archive, projection,
or catalog probe that deliberately reuses the Go model without replacing its
canonical table:

```go
var Users = sqlrepo.Define[User, int64, UserUpdate]("users")
var ArchivedUsers = sqlrepo.Define[User, int64, UserUpdate](
    "archived_users", sqlrepo.IndependentTable())
```

Self-relations on `ArchivedUsers`, including cycles that later return to
`User`, stay on `archived_users`; they do not silently mix archive and live
rows. Other model types still use their canonical declarations. Put
`table=users` on a relation tag only when that particular edge is intentionally
supposed to leave the local view ([[D-080]]).

### Typed, or by name — both spellings work

Every setting above takes a name. The generated metamodel answers the same names
as identifiers, so a rename becomes a build failure instead of a declaration that
reads as protection and narrows nothing.

```go
sqlrepo.RelationScope(
    Article_.Comments.Path(),                       // the path
    specs.Predicate(Comment_.TenantID.Eq(1)))       // the far side
```

Two metamodels, because the two halves are different jobs. The **path** comes
from the group on the root's metamodel — `Article_.Comments`. The **predicate**
is written against the *target* model, so it comes from the target's own
metamodel — `Comment_`, not `Article_.Comments`. `Article_.Comments.TenantID` is
an attribute of an *Article*, spelled `Comments.TenantID`, and it filters
articles by their comments; that is a different question ([[FL-005]]).

The same identifiers serve the rest:

```go
sqlrepo.SoftDelete(Doc_.DeletedAt.Name())
sqlrepo.Scope(specs.Predicate(Article_.Hidden.Eq(false)))
sqlrepo.DefaultSort(Article_.CreatedAt.Desc())
crud.Preload(Article_.Comments.Path(), Article_.Author.Path())
```

`Name()` answers an attribute's canonical name, `Path()` a relation's canonical
path. Nothing is deprecated: a name that is only known at run time — one that
arrived over the wire — is still a string, and an unknown one is a refusal
rather than a clause that quietly disappears ([[D-013]], [[UC-007]]).

A relation group only carries a handle when the generator expanded it, which
`-depth` controls. And because the handle is embedded, a target model with a
column called `Path` shadows the method — the generated file says so in that
group's doc comment, and `RelPath()` is the spelling nothing shadows.

---

## Decorators

`Bind` takes middleware. The first one ends up outermost.

```go
users := specs.Executor(Users.Bind(db,
    security.Gate(policy),
    faults.Enrich[User, int64](faults.WithProbe(probe.Full(cat))),
))
```

Every type parameter is inferred from the argument, so call sites stay free of
explicit generics. Writing your own is an embedded interface and one method:

```go
type auditing struct{ crud.Core[User, int64] }

func (a auditing) Save(ctx context.Context, u *User) (User, error) {
    log.Println("saving", u.Email)
    return a.Core.Save(ctx, u)
}

func Log() crud.Middleware[User, int64] {
    return func(next crud.Core[User, int64]) crud.Core[User, int64] {
        return auditing{next}
    }
}
```

A decorator sees `crud.Core[M, ID]` — two type parameters, not three — which is
what keeps the middleware signature writable ([[D-001]]). The three-parameter
`crud.Repo[M, ID, U]` is the façade; a consumer holds `*crud.Repo[M, ID, U]`.

**A new verb on the seam is a decorator obligation** ([[D-030]]): adding a method
to `Core` means every decorator in the tree forwards it, or it silently bypasses
the gate.

---

## Binding to a datasource

```go
db := crudpgx.Open(pool)                 // pgx v5
db := crudsql.Postgres(sqlDB)            // database/sql
db := crudsql.MySQL(sqlDB)
db := crudsql.MariaDB(sqlDB)             // same dialect, different error numbers
db := crudsql.SQLite(sqlDB)
db := crud.ReadWrite(primary, replica)   // reads to the replica, writes to the primary

users := Users.Bind(db)
```

One blueprint may be bound many times — to a second database, to a test
recorder, to a replica pair. `Define` is the declaration; `Bind` is the wiring.

## Transactions

```go
err := users.Tx(ctx, func(ctx context.Context) error {
    u, err := users.GetByID(ctx, 42)
    if err != nil { return err }
    _, err = users.Update(ctx, u.ID, UserUpdate{Name: ptr("new")})
    return err
})
```

`Tx` **joins** a transaction already in the context rather than nesting inside
it: the outer owner keeps control of commit and rollback, and `fn` cannot roll
back independently. `crud.InTx(ctx, db, fn)` does the same for several
repositories at once. For genuine nesting, `Begin` gives a savepoint — natively
on pgx, via `SAVEPOINT` on `database/sql` ([[FL-009]]).

`SaveAll` and `Delete(ids...)` use that same rule when a bind budget requires
several statements. They join an ambient transaction or open one; a datasource
that cannot provide either atomic boundary returns `crud.ErrNoTxSupport` before
the first chunk. A one-statement call opens nothing extra.

## Sharp edges

- **Rows affected diverge.** MySQL reports 0 for an `UPDATE` that changed
  nothing, and counts *matched* rather than *changed* rows depending on
  configuration. `ErrNotFound` is therefore never derived from `n == 0` on a
  write path.
- **`DeleteAll` fetches its victims with the caller's options** before deleting,
  so a decorator's narrowing applies to the delete and not only to the fetch
  ([[D-026]]).
- **`Distinct()` forces the primary key into the projection** where a sort needs
  it, because `SELECT DISTINCT` and `ORDER BY` on an unselected column is an
  error on PostgreSQL ([[D-024]]).
- **An unknown field is a rejection**, never a dropped clause ([[D-013]]).
- **The SQL is deterministic** — same options, same statement, byte for byte
  ([[D-014]]). That is what makes it testable with [crudtest](crudtest.md).
- **Bind limits are preflighted.** `In` / `InAny` and every other direct Go
  predicate share one statement-wide dialect budget. An oversized statement is
  a typed schema refusal before the datasource. `SaveAll` and `Delete(ids...)`
  chunk because their operation stays equivalent; an arbitrary predicate does
  not ([[D-079]]).
- **Table registration is typed.** `RegisterTable` accepts a struct model, not
  `*Model`, a scalar, or an interface. A conflicting or already-published name
  fails loudly ([[D-080]]).
- **Qualified table identity is structured.** A dotted `Define`/`TableName`
  string is a declaration error. Use `DefineInSchema`; relation overrides use
  `schema=...,table=...`, and many-to-many joins add `joinSchema=...`.

## A column `DEFAULT` does not fire

vv writes every mapped column, so the INSERT it builds names them all — and a row
created without a value for one stores the Go zero value rather than the column's
`DEFAULT`. A default only reaches rows the database makes on its own.

This is the first surprise most newcomers hit, and it follows from [[D-014]]:
the same call has to compile to the same statement, and a statement that omitted
whichever columns happened to be zero would not. Where the server must own a
value, mark the column `generated` — vv then leaves it out of the INSERT and
reads it back — or fill it in a `BeforeSave` hook.

## See also

- [crud](crud.md) — the vocabulary this package renders
- [specs](specs.md) · [security](security.md) · [faults](faults.md) — the decorators
- [crudtest](crudtest.md) — assert on the statement with no database
- [[FL-001]] [[FL-002]] [[FL-003]] [[FL-004]] — the read, the patch, the save, the declaration
