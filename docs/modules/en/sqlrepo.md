# crud/sqlrepo — the repository

```go
import "github.com/shardit-io/vv/crud/sqlrepo"
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
    Age       crud.Opt[int] `db:"age"`
    CreatedAt time.Time     `db:"created_at,generated"`
}

// Field names match the model 1:1. Pointers are optional, Opt is nullable.
type UserUpdate struct {
    Email *string
    Name  *string
    Age   crud.Opt[int]
}

var Users = sqlrepo.Define[User, int64, UserUpdate]("users")

users := Users.Bind(crudpgx.Open(pool))
```

`Define` validates the tags, the ID type and the DTO **eagerly**, so a broken
mapping panics at package initialisation rather than on the first request. Use
`TryDefine` where you want the error instead of the panic, and `sqlrepo.New` to
skip the blueprint and go straight to a bound repository.

---

## The surface

| Method | Meaning |
|---|---|
| `GetByID(ctx, id, opts...)` | one row, or `crud.ErrNotFound` |
| `Get(ctx, opts...)` | `crud.PaginatedResponse[M]` |
| `GetAll(ctx, opts...)` | every match; unpaged unless an option says otherwise |
| `Save(ctx, *M)` | JPA semantics: no key → INSERT, key → UPSERT. The model is refreshed in place |
| `SaveAll(ctx, []*M)` | the same write, batched into one statement |
| `Update(ctx, id, dto, opts...)` | load, diff, write only what changed |
| `UpdateAll(ctx, dto, opts...)` | one `UPDATE` across a filter; returns rows touched |
| `Delete(ctx, ids...)` | returns how many rows went away |
| `DeleteAll(ctx, opts...)` | the same, filtered |
| `Count(ctx, opts...)` / `Exists(ctx, opts...)` | with the same options |
| `Aggregate(ctx, opts...)` | grouped summaries, under the same narrowing as a read |
| `Tx(ctx, fn)` | run in a transaction, joining one already in `ctx` |
| `Meta()` | the bound schema and table |

### Save is JPA-shaped

A zero primary key means `INSERT`. A non-zero one means `UPSERT` ([[D-011]]).

A `db:",auto"` key is left out of the column list and read back — `RETURNING` on
PostgreSQL and SQLite, `LastInsertId` on MySQL — along with every `generated`
column. The model you passed is refreshed in place, so `u.ID` and `u.CreatedAt`
are filled when `Save` returns.

`SaveAll` batches into one statement and reads every key back in order
([[UC-008]]). On pgx it uses `COPY` where the shape allows.

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

Passed to `Define`, `TryDefine` or `New`, and applied to every call.

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

A decorator sees `crud.Core[M, ID]` — two type parameters, not three — which is
what keeps the middleware signature writable ([[D-001]]). The three-parameter
`crud.Repo[M, ID, U]` is the façade a consumer holds.

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

## See also

- [crud](crud.md) — the vocabulary this package renders
- [specs](specs.md) · [security](security.md) · [faults](faults.md) — the decorators
- [crudtest](crudtest.md) — assert on the statement with no database
- [[FL-001]] [[FL-002]] [[FL-003]] [[FL-004]] — the read, the patch, the save, the declaration
