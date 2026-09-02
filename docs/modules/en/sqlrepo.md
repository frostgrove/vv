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
| `Create(ctx, *M)` | INSERT only — an existing key is a 409, never an overwrite |
| `Replace(ctx, *M)` | key-targeted upsert checked against `version`; a row somebody else advanced is `crud.ErrStaleVersion` |
| `InsertBatch(ctx, []*M, opts...)` | write-only, insert-only typed bulk; native when explicitly exposed, portable otherwise |
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

The upsert reaches the row that key names and no other. Where a single statement
cannot promise that — MySQL and MariaDB, whose `ON DUPLICATE KEY UPDATE` fires on
every unique index, or any repository that declared `Scope` — `Save` writes an
`UPDATE` narrowed by the key and the scope, probes, and inserts only if no row
was there, all in one transaction. On PostgreSQL and SQLite without a `Scope` it
stays the one statement it always was.

A `db:",auto"` key is left out of the column list. `Save` returns a separate
model containing the stored row — `RETURNING` on PostgreSQL and SQLite, or an
insert/upsert followed by a read in one transaction on a dialect without it.
That includes every `generated` column and trigger normalisation; the `*M` you
passed is never changed.

`SaveOnly` performs only the write. It does not append `RETURNING`, does not
fetch a model and does not mutate its argument. Use it for writes whose
generated values are irrelevant.

### Create and Replace say the part Save leaves open

```go
stored, err := users.Create(ctx, &u)   // INSERT only; a taken key is a 409
stored, err := users.Replace(ctx, &u)  // upsert, refused if u.Version is behind
```

`Save` is the default and every transport calls it. These two are for a caller
who wants the refusal spelled out: `Create` never absorbs a conflict, and
`Replace` pins the write to the `version` the model carries and advances it.
`Patch` is not among them — `Update` already is it.

Both are optional capabilities. A decorator that does not forward them makes the
call return `crud.ErrNoCreateSupport` / `crud.ErrNoReplaceSupport` rather than
quietly reaching past it, so an explicit verb can never become a way around
`security.Gate`.

`SaveAll` is likewise write-only and always uses ordinary SQL. It keeps Save's
semantics: assigned keys upsert, generated keys insert. Native bulk is a
separate verb because PostgreSQL COPY is not semantically identical to INSERT
for every table feature.

The dialect's bind budget is automatic. A fitting batch stays one statement. A
larger one is split on row boundaries, in caller order, after every model and
statement has passed preflight. All chunks join one transaction, so an error in
the last chunk cannot leave the first committed. Generated-key batches remain
write-only: `SaveAll` neither adds `RETURNING` nor mutates the models
([[D-079]]).

### InsertBatch is create-only native bulk

```go
err := users.InsertBatch(ctx, []*User{&a, &b, &c})
```

Unlike `SaveAll`, `InsertBatch` never upserts: a row with an assigned key is
still an insert and may conflict. It derives the exact table, columns and values
from immutable metadata, preserves Gate and fault decorators, joins an ambient
executor, and never mutates the models or reads generated values back.
The Gate authorises `Create` once and inspects every incoming row; a scope-only
policy with no `Inspect` refuses the batch rather than trusting values it cannot
verify. Fault enrichment keeps `InsertBatch` as the operation and preserves
field paths from the driver classifier.

When the exact repository Source exposes `crud.UnsafeBulkInserter` directly and
metadata yields insert columns, the repository selects it; crudpgx provides
PostgreSQL COPY and receives the matching bound executor as its target. The
Source remains authoritative, so a transaction cannot reveal native bulk hidden
or refused by a wrapper. Otherwise the repository uses the same preflighted, atomic
bind-budgeted `INSERT` machinery as a portable fallback. Capability discovery
does not guess whether COPY fits a table's semantics: RLS/rewrite-rule tables,
special pgx encodings, or callers requiring ordinary INSERT semantics need an
explicit opt-out:

```go
err := users.InsertBatch(ctx, rows, crud.PortableBatch())

var Users = sqlrepo.Define[User, int64, UserUpdate](
    "users",
    sqlrepo.PortableBatch(),
)
```

`SourceUnwrapper` is not followed for this write effect. An unknown source
wrapper therefore selects portable SQL unless it explicitly implements the
unsafe native forwarder. Its `Exec` sees a direct one-statement plan; chunked
plans run on the transaction handle. Complete statement tracing belongs in the
driver/connector or an explicitly instrumented `Begin`/`Tx`. A native
server/encoding failure is final; only a before-I/O
`ErrNoBulkInsertSupport` selects fallback, so rows are never retried after the
server has begun processing the native call.

Pre-release migration: the old driver-level `BulkInserter`, `CopyFrom` and
`CopyFromTable` surface was removed. Application code moves to
`Repo.InsertBatch`; deliberate raw pgx work uses the explicitly unsafe APIs.

### Raw predicates and raw statements are different boundaries

`crud.Raw(fragment, args...)` is a predicate node inside a repository-built
statement. Its fragment is not field-validated, but the repository still owns
the table, combines permanent and security scopes around it, resolves the
ambient executor, and runs the usual fault hooks.

A whole raw statement bypasses that repository boundary. Calling `Exec` or
`Query` on the `Source` returned by `SourceOf` runs on that source handle
directly and does **not** resolve an executor bound in `ctx`. Use the explicit
context-aware escape hatches when raw SQL must join the same transaction:

```go
result, err := crud.UnsafeExecFor(ctx, source, statement, args...)
rows, err := crud.UnsafeQueryFor(ctx, source, query, args...)
```

The `Unsafe` prefix is still literal: these calls preserve datasource/ambient
transaction routing, but bypass metadata, repository policy, validation and
fault decorators. Put business SQL behind a repository method; reserve these
functions for deliberate infrastructure-level statements.

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
| `Scope(pred)` | a predicate ANDed into every read, every delete, and the row a write may reach — including the update half of `Save`/`SaveAll` and the read-back after it. It does not narrow the values a new row may hold |
| `RelationScope(path, pred)` | the same, on the far side of a relation |
| `SoftDelete(field)` | rows are flagged rather than removed, and hidden from every read |
| `PortableBatch()` | keep every `InsertBatch` call on ordinary bind-budgeted SQL |
| `UnstablePagination()` | drop the primary-key tiebreaker appended to every sort |
| `IndependentTable()` | keep an additional physical table for the model local to this blueprint |

`Scope` here is the **per-table** narrowing — it applies to everyone. The
**per-principal** form is [security](security.md), which reads the context.

Every `field` and `path` above is a model field name or a relation path, and the
generated metamodel answers both as identifiers — see [Typed, or by
name](#typed-or-by-name--both-spellings-work) below.

### Soft delete

```go
type Doc struct {
    ID        int64                `db:"id,pk,auto"`
    DeletedAt utils.Opt[time.Time] `db:"deleted_at,serverowned,tombstone"`
}
var Docs = sqlrepo.Define[Doc, int64, DocUpdate]("docs")
```

`Delete` and `DeleteAll` write the flag instead of removing the row, and every
read excludes flagged rows. `Restore` clears only tombstones as a distinct
lifecycle verb; generated wire inputs and generic saves cannot write the field.
Security sees `Restore` as a distinct action, not `Update`. For an external model
that cannot carry vv tags, `SoftDelete("DeletedAt")` is the equivalent explicit
blueprint setting. The field must be a nullable timestamp (`*time.Time`,
`Opt[time.Time]`, or a compatible Scanner/Valuer wrapper). Soft delete and
restore also advance an optimistic-lock version when present, closing
Delete→Restore ABA windows. It is a property of the statement rather than a
decorator ([[D-031]], [[UC-016]]).

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

`SaveAll`, portable `InsertBatch`, and `Delete(ids...)` use that same rule when
a bind budget requires several statements. They join an ambient transaction or
open one; a datasource that cannot provide either atomic boundary returns
`crud.ErrNoTxSupport` before the first chunk. A one-statement call opens nothing
extra. Native `InsertBatch` is authorised by the exact Source and receives the
resolved executor as its target, so pgx COPY joins the same ambient transaction
rather than escaping to the pool or bypassing a Source wrapper.
A bound non-transaction executor is not an atomic boundary and is not reused for
a chunked plan; sqlrepo opens a transaction from its source. Bind an already
started transaction, or bind the repository to the session source itself, when
connection-local state must survive across the chunks.

## Sharp edges

- **Rows affected diverge.** MySQL reports 0 for an `UPDATE` that changed
  nothing, and counts *matched* rather than *changed* rows depending on
  configuration. `ErrNotFound` is therefore never derived from `n == 0` on a
  write path.
- **`DeleteAll` fetches its victims with the caller's narrowing** before
  deleting, so a decorator's narrowing applies to the delete and not only to the
  fetch ([[D-026]]).
- **A filtered write refuses what it cannot apply.** `Update`, `UpdateAll` and
  `DeleteAll` take `Where`, `NarrowRelations`, `ForUpdate` and `PrimaryOnly`; a `Limit`, a
  page, a cursor, a sort, a projection or a preload is a `*crud.SchemaError`
  naming the option, not a write of every matching row ([[D-087]]).
- **`Distinct()` forces the primary key into the projection** where a sort needs
  it, because `SELECT DISTINCT` and `ORDER BY` on an unselected column is an
  error on PostgreSQL ([[D-024]]).
- **An unknown field is a rejection**, never a dropped clause ([[D-013]]).
- **The SQL is deterministic** — same options, same statement, byte for byte
  ([[D-014]]). That is what makes it testable with [crudtest](crudtest.md).
- **Bind limits are preflighted.** `In` / `InAny` and every other direct Go
  predicate share one statement-wide dialect budget. An oversized statement is
  a typed schema refusal before the datasource. `SaveAll`, portable
  `InsertBatch`, and `Delete(ids...)` chunk because their operation stays
  equivalent; an arbitrary predicate does not ([[D-079]]).
- **COPY suitability is a declaration, not a guess.** A bare pgx source exposes
  native bulk by default. Choose per-call `crud.PortableBatch()` or blueprint
  `sqlrepo.PortableBatch()` for RLS/rewrite rules, special encoding, or complete
  statement-middleware observability.
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
