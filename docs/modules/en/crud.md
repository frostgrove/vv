# crud — the contract

```go
import (
    "github.com/frostgrove/vv/crud"
    "github.com/frostgrove/vv/utils"
)
```

**Module:** root · **Depends on:** the standard library, and nothing else
· **Contract manifest:** yes ([[D-048]])

The vocabulary every other package speaks. A model becomes metadata, a filter
becomes a closed AST, an option becomes a query plan, and a connection becomes
two methods. Nothing here runs SQL — `crud/sqlrepo` does that — and nothing here
knows what a transport is.

**Import it directly when** you write filters, wire a foreign transaction, or
implement an adapter. Three-state values and small generic helpers live in
`github.com/frostgrove/vv/utils`; most application code touches `crud.Where`,
`utils.Opt`, `utils.Ptr` and `crud.Page`.

---

## What you get

| | |
|---|---|
| **Model metadata** | `db` and `rel` tags become a `Schema`, a `Meta` and a relation graph, resolved once |
| **`utils.Opt[T]`** | three states — undefined, null, set — so a PATCH can tell "leave it" from "clear it" |
| **Options** | `Page`, `Limit`, `Where`, `OrderBy`, `Preload`, `Select`, `Distinct`, `Aggregate`, and fourteen more |
| **Predicates** | a closed AST: 26 constructors, `And`/`Or`/`Not` among them, relation paths at any depth |
| **Pagination** | `PaginatedResponse[T]`, offset paging and cursor paging over the sort tuple |
| **Relations** | `belongs_to`, `has_one`, `has_many`, `many_to_many` — inferred, overridable |
| **The executor seam** | `Exec` and `Query`. That is the whole abstraction boundary |
| **Transactions** | `InTx`, `WithExecutor`, `WithExecutorFor` — join anyone's transaction |
| **Dialects** | `Postgres`, `MySQL` (MariaDB too), `SQLite` |
| **Sentinels** | `ErrNotFound`, `ErrConflict`, `ErrForbidden`, `ErrStaleVersion`, … |

---

## The model

`db:"column,option,option"`. A field with no tag is still mapped, with the column
derived from the Go name in `snake_case`.

```go
type User struct {
    ID        int64         `db:"id,pk,auto"`
    TenantID  int64         `db:"tenant_id,immutable"`
    Email     string        `db:"email"`
    Age       utils.Opt[int] `db:"age"`
    Version   int           `db:"version,version"`
    CreatedAt time.Time     `db:"created_at,generated"`
}
```

| Option | Meaning |
|---|---|
| `pk` | primary key. Falls back to a field named `ID`, or a column named `id` |
| `auto` | database-generated on insert. The default for integer primary keys |
| `noauto` | opt an integer primary key out of that default |
| `immutable` | written on insert, never on update — `created_at`, `tenant_id` |
| `generated` | never written, read back after every write — computed columns, triggers |
| `version` | optimistic lock: an integer vv advances and checks on every update |
| `-` | ignore the field entirely |

Embedded structs are flattened. `time.Time`, `sql.Null[T]`, `utils.Opt[T]` and
anything with a `Valuer`/`Scanner` count as one column. **Struct-shaped fields
without a `rel` tag are skipped** — neither column nor edge, which is what you
want for a computed field.

`SchemaOf[M]()`, `MustSchemaOf[M]()` and `NewMeta[M](table)` build the metadata
by hand if you need it; `sqlrepo.Define` does it for you and validates eagerly.
For a qualified physical table use `NewMetaInSchema[M](schema, table)` or the
low-level `NewMetaRef[M](crud.TableRef{Schema: schema, Name: table})`.
`Meta.TableReference()` returns the immutable structured identity by value;
`Meta.Table` remains its diagnostic compatibility spelling.

## `utils.Opt[T]` — three states, one type

The reason a PATCH works at all.

```go
utils.Undefined[int]()   // absent from the payload  → not written
utils.Null[int]()        // explicit null            → SET col = NULL
utils.Set(31)            // a value                  → SET col = 31
utils.FromPtr(p)         // nil → null, else set
```

```go
o.IsDefined() // is it either null or set?
o.IsSet()     // is it set?
o.Get()       // (T, bool)
o.OrElse(def) // T
o.Ptr()       // *T
```

It marshals as the bare value, or `null`, and disappears under `omitzero`. On the
wire that means the same struct serialises correctly in both directions
([[UC-003]]).

`utils.Ptr(v)` is the concise pointer constructor for patch DTOs and
`utils.Must(v, err)` returns `v` or panics at declaration-time boundaries.
`crud.Opt`, `crud.Set`, `crud.Null` and friends remain compatibility aliases;
new code should use `utils` so model, transport and validation code do not take
a CRUD dependency just to express an optional value.

## Options

Every read takes `...crud.Option`, and every option is additive.

| Group | Options |
|---|---|
| Paging | `Page(n)`, `Limit(n)`, `Offset(n)`, `Unpaged()`, `SkipTotal()` |
| Cursor paging | `After(cursor)`, `Before(cursor)` |
| Filtering | `Where(pred)` — **ANDs, never replaces** |
| Sorting | `OrderBy(orders...)`, `SortBy(...)`, `Unsorted()`, `Asc(f)`, `Desc(f)` |
| Projection | `Select(fields...)`, `SelectAll()`, `Distinct()`, `PrimaryOnly()` |
| Relations | `Preload(paths...)`, `PreloadWhere(path, opts...)`, `NarrowRelations(rs)` |
| Grouping | `GroupBy(fields...)`, `Aggregate(aggs...)` |
| Locking | `ForUpdate()` |
| Composition | `With(otherOptions)` |

`crud.Where` **ANDs**. That is what lets a decorator inject a filter a caller
cannot peel back off ([[D-004]]).

Three things happen without asking: the page size is clamped to the repository's
`MaxLimit` — including when the request says `Unpaged`, which is a flag on the
wire and does not outrank a server-side cap — a primary-key tiebreaker is
appended so pages do not shuffle, and the `COUNT` is skipped when the first page
already holds everything.

## Predicates

A closed AST. `Raw` is the only escape hatch, which is what makes a security
scope unremovable ([[D-003]]).

```go
crud.Eq  crud.Ne  crud.Gt  crud.Gte  crud.Lt  crud.Lte
crud.In  crud.NotIn  crud.InAny[T]  crud.NotInAny[T]  crud.Between
crud.Like  crud.NotLike  crud.LikeIgnoreCase
crud.Contains  crud.StartsWith  crud.EndsWith
crud.ContainsIgnoreCase  crud.StartsWithIgnoreCase  crud.EndsWithIgnoreCase
crud.IsNull  crud.IsNotNull  crud.EqField
crud.And  crud.Or  crud.Not  crud.True  crud.False  crud.Raw
```

`Eq` and `Ne` understand `utils.Opt`: set compares with the stored value, null
uses `IS NULL`/`IS NOT NULL`, and undefined is a schema error rather than a
silent `= NULL`.

`Like`, `NotLike` and `LikeIgnoreCase` accept an SQL pattern verbatim, for code
that intentionally owns `%` and `_`. `Contains`, `StartsWith` and `EndsWith`
accept ordinary text: they quote backslash, `%` and `_`, add the wildcard, and
emit a dialect-safe `ESCAPE` clause. Their `IgnoreCase` forms use portable
`LOWER()` matching.

A field name may cross a relation at any depth:

```go
crud.Where(crud.Eq("Author.Name", "Ann"))
crud.Where(crud.In("Tags.Slug", "go", "rust"))
crud.Where(crud.Contains("Comments.Author.Name", "bo"))
```

Each hop renders as a **correlated `EXISTS`**, not a join ([[D-005]]). A join
against a to-many relation multiplies the result set: an article with two
matching tags becomes two rows, `LIMIT 20` returns fewer than twenty distinct
articles, and `COUNT(*)` reports a number that does not exist.

`crud.Raw` is **not validated** — column names in a raw fragment are neither
resolved nor quoted. It is the one thing to grep for in review.

## Relations

```go
type Article struct {
    ID       int64  `db:"id,pk,auto"`
    AuthorID int64  `db:"author_id"`

    Author   *Author   `rel:"belongs_to"`                     // fk: AuthorID
    Comments []Comment `rel:"has_many"`                       // fk: ArticleID on Comment
    Tags     []Tag     `rel:"many_to_many,join=article_tags"` // article_id / tag_id
}
```

| Tag | Foreign key | Overrides |
|---|---|---|
| `belongs_to` | `<Field>ID` on this model | `fk=`, `ref=`, `table=`, `schema=` |
| `has_one` / `has_many` | `<ThisModel>ID` on the target | `fk=`, `ref=`, `table=`, `schema=` |
| `many_to_many` | the join table's two columns | target `table=`/`schema=`; `join=`, `joinSchema=`, `joinFK=`, `joinRef=` |
| `rel:""` | inferred from the Go type | |
| `rel:"-"` | never a relation | |

Target tables resolve from `sqlrepo.Define`'s registration, then a `TableName()`
method, then the snake_case plural. `RegisterTable[M](table)` registers one by
hand. Resolution is immutable: a second table for one model, or a different
registration after a relation has already resolved the conventional name, is a
start-up error rather than a registry update that existing relation metadata
cannot observe. `TryRegisterTable` and `TryRegisterTableType` return that schema
error for low-level assembly. The structured counterparts are
`RegisterTableRef` / `TryRegisterTableRef`; repeating the same full reference is
idempotent. When one model deliberately reaches the same Go type through a
different table, declare `table=...` on that relation and add `schema=...` when
qualified. A many-to-many join uses `joinSchema=...`. Dotted legacy strings are
refused rather than split ([[D-080]]).

**Preloading** is a batched second query per relation per level, never one per
row ([[D-006]]). Paths sharing a prefix share a statement, keys are deduplicated,
long lists chunk into 900-key batches, and a `Select()` projection automatically
keeps the join columns. Pagination inside a preload is refused — a `LIMIT` over a
batched load would truncate some parents' children and not others.

Sorting through a to-many is refused too: a collection has no single value to
sort by, so it is declined rather than silently picking one.

A path is a string here because it usually arrives as one — from a query string,
from a client. Written in Go, the generated metamodel answers the same path as an
identifier, so a renamed relation breaks the build:

```go
crud.Preload(Article_.Comments.Path(), Article_.Comments.Author.Path())
crud.PreloadWhere(Article_.Comments.Path(), crud.Where(specs.Predicate(Comment_.Approved.Eq(true))))
```

See [specs](specs.md) for the handle and its one shadowing case.

## Pagination

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

`MapPage(p, f)` converts the items and keeps the arithmetic.

**Cursor paging** encodes the sort tuple, not an offset, so inserts do not shift
a reader's window ([[D-028]]):

```go
page, _ := users.Get(ctx, crud.Limit(20), crud.OrderBy(crud.Desc("CreatedAt")))
next, _ := users.Get(ctx, crud.Limit(20), crud.After(page.NextCursor))
```

`SkipTotal()` drops the `COUNT` entirely, answers `HasNext` by fetching one row
past the page, and reports `Total` as the size of what came back — because
nothing counted the rest.

Cursor links are issued only for a unique, non-nullable sort. Pointer,
`utils.Opt` and `database/sql.Null` sort columns can still use offset pages, but
do not advertise a cursor that SQL's three-valued NULL comparisons cannot walk.

## Aggregates

```go
rows, err := orders.Aggregate(ctx,
    crud.GroupBy("Status"),
    crud.Aggregate(crud.CountAll("n"), crud.Sum("total", "Amount"), crud.Avg("avg", "Amount")),
    crud.Where(crud.Gte("CreatedAt", cutoff)))
```

`CountAll`, `CountOf`, `CountDistinct`, `Sum`, `Avg`, `Min`, `Max`. They run
under the same narrowing as a read, so a security scope applies ([[D-029]]).

A summary is unpaged by default: no repository default silently drops groups.
An explicit `Limit`, `Page`, `Offset`, or `Unpaged` still honours the
repository's `MaxLimit`. Ordinary `OrderBy` may name grouping columns only;
other model columns are rejected before a statement is sent.

The field names take the metamodel too — `crud.GroupBy(Order_.Status.Name())`,
`crud.Sum("total", Order_.Amount.Name())`. The aggregate's own name is the key
the value comes back under, so that one stays a string.

## The executor seam

Two methods. That is the entire abstraction boundary, and it is why any foreign
transaction can be pushed into a context.

```go
type Executor interface {
    Exec(ctx context.Context, query string, args ...any) (Result, error)
    Query(ctx context.Context, query string, args ...any) (Rows, error)
}

type Source interface {
    Executor
    Dialect() Dialect
}
```

Scanning stays with the mapper, dialect stays with the repository.

**Joining someone else's transaction** is one function:

```go
ctx = crud.WithExecutor(ctx, crudsql.From(tx))
```

Every repository call made with that context runs on that executor. The capture
is unconditional and has to be — the transaction ent or gorm hands over has no
relationship to the source a repository holds, so there is nothing to check it
against ([[D-009]]).

**With more than one database, say which one you mean:**

```go
ctx = crud.WithExecutorFor(ctx, mainDB, crudsql.From(tx))

users.Save(ctx, &u)    // bound to mainDB      — runs in tx
events.Save(ctx, &e)   // bound to analyticsDB — runs on analyticsDB
```

With the plain form that second call would have gone to `mainDB`, inside the
transaction, and reported success ([[UC-012]]).

`crud.InTx(ctx, src, fn)` opens one transaction for several repositories, and
joins one already in the context rather than nesting inside it.

**Optional interfaces** an adapter may implement, each looked up rather than
required so a third-party adapter keeps compiling without them: `Beginner`
(savepoints), `BulkInserter` (`COPY`), `OffsetLimiter`, `ReadSourcer`,
`Identified`, `Sourced`, `UpsertScope`, `StatementRollback`, `Tabler`.

### Wrapping a Source — for tracing, timing, statement logs

This is the seam. A `Source` is three methods, so a wrapper that times or logs
statements is a dozen lines — and it has one obligation:

```go
type tracing struct{ inner crud.Source }

func (t tracing) Exec(ctx context.Context, q string, a ...any) (crud.Result, error) { … }
func (t tracing) Query(ctx context.Context, q string, a ...any) (crud.Rows, error)  { … }
func (t tracing) Dialect() crud.Dialect { return t.inner.Dialect() }

// This one. Without it the wrapper is not a decorator, it is a replacement.
func (t tracing) UnwrapSource() crud.Source { return t.inner }
```

Go's embedding promotes only the embedded interface's own method set, so a
wrapper erases every optional interface the wrapped source had. Three of them
cost something, and only two of the three say so:

| Lost | What happens |
|---|---|
| `Beginner` | every `Tx` is `ErrNoTxSupport` — loud |
| `Identified` | the catalog keyed on the handle stops matching, and refuses at start-up ([[D-041]]) — loud |
| `ReadSourcer` | **every read goes to the primary, silently.** The replica sits idle and nothing connects that to the day the wrapper was added |

`UnwrapSource` is all three at once: `crud.BeginnerOf`, `crud.ReadSourceOf` and
`crud.KeyOf` follow it ([[D-061]]).

**The same rule one level up.** A decorator over `crud.Core` erases in the same
way. Embed `crud.Base` — it supplies the `Next()` that `crud.SourceOf` walks —
or a probe wired above your decorator cannot find the datasource underneath it.

## Replicas

```go
db := crud.ReadWrite(primary, replica)
```

Reads go to the replica, writes to the primary — and **a read that decides a
write never does** ([[D-032]]). The existence probe behind a `Save`, the load
behind an `Update`, and the victim fetch behind a `DeleteAll` all run on the
primary, because deciding a write from a lagging replica is how a row gets
silently overwritten.

## Sentinels

Compare with `errors.Is`, never by string ([[D-015]]).

```go
crud.ErrNotFound       crud.ErrConflict       crud.ErrForbidden
crud.ErrStaleVersion   crud.ErrReadOnly       crud.ErrMissingID
crud.ErrNoTxSupport
```

Every one of them survives being wrapped in an `errs.Fault`, so a caller who
wrote `errors.Is(err, crud.ErrConflict)` before the error subsystem existed keeps
that branch ([[D-038]]).

## Dialects

`crud.Postgres{}`, `crud.MySQL{}`, `crud.SQLite{}`. A dialect says **how to write
SQL**, not which server is answering — `crud.MySQL` targets MariaDB too, and the
two answer a failed `CHECK` with different numbers. Saying which *engine* is
speaking is [sqlfault](sqlfault.md)'s job.

Differences a caller can see are enumerated in [[D-019]] rather than papered
over.

`crud.MySQL{RowAlias: true}` switches upserts from the deprecated `VALUES()` form
to `AS new` (MySQL 8.0.19+). `ForUpdate()` renders nothing on SQLite, which locks
the database rather than the row.

The statement parameter ceiling is a dialect capability too. `BindLimit(d)`
reads the optional `BindBudget`: PostgreSQL and MySQL declare 65,535, SQLite
declares 999, and an external dialect that does not implement the capability
gets the conservative 999 default. `SQL.Done` counts the complete argument
list, so sibling predicates and relation scopes consume the same budget as an
`In` list. Oversize is a typed `SchemaError` before the datasource, not a driver
failure ([[D-079]]).

## See also

- [sqlrepo](sqlrepo.md) — the repository that turns all of this into SQL
- [crudtest](crudtest.md) — assert on the SQL without a database
- [[FL-001]] a list request to rows · [[FL-012]] a wire value to a Go value
- [[D-001]] the two-parameter seam · [[D-003]] the closed AST · [[D-016]] stdlib only · [[D-079]] bind budgets
