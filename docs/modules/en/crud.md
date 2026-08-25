# crud — the contract

```go
import "github.com/shardit-io/vv/crud"
```

**Module:** root · **Depends on:** the standard library, and nothing else
· **Contract manifest:** yes ([[D-048]])

The vocabulary every other package speaks. A model becomes metadata, a filter
becomes a closed AST, an option becomes a query plan, and a connection becomes
two methods. Nothing here runs SQL — `repo/basic` does that — and nothing here
knows what a transport is.

**Import it directly when** you write filters, hold a `crud.Opt`, wire a foreign
transaction, or implement an adapter. Most application code touches `crud.Where`,
`crud.Opt`, `crud.Page` and little else.

---

## What you get

| | |
|---|---|
| **Model metadata** | `db` and `rel` tags become a `Schema`, a `Meta` and a relation graph, resolved once |
| **`Opt[T]`** | three states — undefined, null, set — so a PATCH can tell "leave it" from "clear it" |
| **Options** | `Page`, `Limit`, `Where`, `OrderBy`, `Preload`, `Select`, `Distinct`, `Aggregate`, and eighteen more |
| **Predicates** | a closed AST: 25 constructors, `And`/`Or`/`Not`, relation paths at any depth |
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
    Age       crud.Opt[int] `db:"age"`
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

Embedded structs are flattened. `time.Time`, `sql.Null[T]`, `crud.Opt[T]` and
anything with a `Valuer`/`Scanner` count as one column. **Struct-shaped fields
without a `rel` tag are skipped** — neither column nor edge, which is what you
want for a computed field.

`SchemaOf[M]()`, `MustSchemaOf[M]()` and `NewMeta[M](table)` build the metadata
by hand if you need it; `basic.Define` does it for you and validates eagerly.

## `Opt[T]` — three states, one type

The reason a PATCH works at all.

```go
crud.Undefined[int]()   // absent from the payload  → not written
crud.Null[int]()        // explicit null            → SET col = NULL
crud.Set(31)            // a value                  → SET col = 31
crud.FromPtr(p)         // nil → null, else set
```

```go
o.Defined()   // is it either null or set?
o.Valid()     // is it set?
o.Get()       // (T, bool)
o.OrZero()    // T
o.Ptr()       // *T
```

It marshals as the bare value, or `null`, and disappears under `omitzero`. On the
wire that means the same struct serialises correctly in both directions
([[UC-003]]).

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
crud.Like  crud.NotLike  crud.LikeIgnoreCase  crud.Contains  crud.StartsWith  crud.EndsWith
crud.IsNull  crud.IsNotNull  crud.EqField
crud.And  crud.Or  crud.Not  crud.True  crud.False  crud.Raw
```

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
| `belongs_to` | `<Field>ID` on this model | `fk=`, `ref=`, `table=` |
| `has_one` / `has_many` | `<ThisModel>ID` on the target | `fk=`, `ref=`, `table=` |
| `many_to_many` | the join table's two columns | `join=`, `joinFK=`, `joinRef=` |
| `rel:""` | inferred from the Go type | |
| `rel:"-"` | never a relation | |

Target tables resolve from `basic.Define`'s registration, then a `TableName()`
method, then the snake_case plural. `RegisterTable[M](table)` registers one by
hand.

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

## Aggregates

```go
rows, err := orders.Aggregate(ctx,
    crud.GroupBy("Status"),
    crud.Aggregate(crud.CountAll("n"), crud.Sum("total", "Amount"), crud.Avg("avg", "Amount")),
    crud.Where(crud.Gte("CreatedAt", cutoff)))
```

`CountAll`, `CountOf`, `CountDistinct`, `Sum`, `Avg`, `Min`, `Max`. They run
under the same narrowing as a read, so a security scope applies ([[D-029]]).

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

**Optional interfaces** an adapter may implement, each checked by assertion so a
third-party adapter keeps compiling without them: `Beginner` (savepoints),
`BulkInserter` (`COPY`), `OffsetLimiter`, `ReadSourcer`, `Identified`,
`Sourced`, `UpsertScope`, `StatementRollback`, `Tabler`.

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

## See also

- [basic](basic.md) — the repository that turns all of this into SQL
- [crudtest](crudtest.md) — assert on the SQL without a database
- [[FL-001]] a list request to rows · [[FL-012]] a wire value to a Go value
- [[D-001]] the two-parameter seam · [[D-003]] the closed AST · [[D-016]] stdlib only
