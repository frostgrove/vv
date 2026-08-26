# query — the wire DSL

```go
import "github.com/shardit-io/vv/crud/query"
```

**Module:** root · **Depends on:** `crud`, and the standard library
· **Contract manifest:** yes ([[D-048]])

One JSON document compiles into `crud.Options`. Every path is resolved against
the model **before any SQL exists**, so an unknown field is a rejection with the
offending path named, never a silently dropped clause ([[D-013]]).

**You mostly do not import this** — the four transport bindings speak it for you.
Import it to bound what a client may ask for (`query.Config`), or to accept a
query document on an endpoint of your own.

---

## The document

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

| Key | Means |
|---|---|
| `page`, `limit`, `offset` | offset paging |
| `after`, `before` | cursor paging — an opaque string a previous page handed back. Replaces `page`/`offset` |
| `sort` | `["-views", "author.name"]`. A leading `-` is descending |
| `select` | projection. Preload join columns are kept automatically |
| `preload` | `["author"]` or `[{"path": "comments", "filter": {…}}]` |
| `filter` | the nested form, below |
| `terms` | the flat form, ANDed with `filter` |
| `search`, `searchFields` | a parenthesised OR over text columns |
| `unpaged` | ask for everything — still clamped by the repository's `MaxLimit` |
| `skipTotal` | drop the `COUNT` |
| `distinct` | `SELECT DISTINCT` |

## Operators

```
eq  ne  gt  gte  lt  lte
like  notLike  ilike  contains  startsWith  endsWith
in  notIn  between  isNull  isNotNull
```

With `$`-prefixed and symbolic aliases — `$gte`, `>=`. A bare value means `eq`,
a bare array means `in`, `null` means `IS NULL`.

Field names are matched case- and separator-insensitively, so `createdAt`,
`created_at` and `CreatedAt` are the same column.

## Three things it gets right

- **Values are typed by their column.** `{"views": {"gte": 100}}` binds an `int`,
  not a `float64`; `{"createdAt": {"gte": "2026-01-02"}}` binds a `time.Time`. A
  column whose type has a `TextUnmarshaler` parses through it, so uuid and enum
  types keep their own rules ([[FL-012]]).
- **Search cannot escape its scope.** The OR over search fields is its own AST
  node, so it is always parenthesised. Concatenating `a LIKE ? OR b LIKE ?` into
  a `WHERE` is how a filtered list quietly starts returning everything.
- **Output is deterministic.** JSON objects have no order and Go maps have less;
  keys are sorted before compiling, so the same request always produces the same
  statement — and can therefore be tested ([[D-014]]).

## The query-string form

Same ground, for `GET`:

```
?page=2&limit=20&sort=-createdAt,author.name&preload=author,comments.author
&select=id,title&q=generics&searchFields=title,body
&f=views:gte:100&f=tags.slug:in:go,rust&f=publishedAt:isNull:true
&filter={"or":[{"status":"draft"}]}
```

`f=field:op:value` repeats and ANDs. **Only the first two colons are
structural**, so timestamps survive. `filter=` takes the full JSON document for
anything the flat form cannot express.

---

## Bounding it

The DSL is driven by untrusted input, so bound it per endpoint ([[UC-002]]).

```go
cfg := &query.Config{
    Filterable:  []string{"Title", "Views", "Author.*"},  // .* allows a subtree
    Sortable:    []string{"CreatedAt", "Views"},
    Selectable:  []string{"ID", "Title", "Views"},
    Preloadable: []string{"Author", "Comments", "Comments.Author"},
    Searchable:  []string{"Title", "Body"},

    DefaultSearchFields: []string{"Title"},

    MaxDepth:      4,   // and/or nesting, and path length
    MaxConditions: 32,  // leaf comparisons in one document
    MaxPreloads:   4,
    MaxInValues:   200, // values in one `in` list
    MaxSort:       4,   // sort terms

    AllowUnpaged: true, // this endpoint serves whole result sets
}
```

**Empty lists allow anything the model maps.** Depth, condition, preload, list
and sort limits apply either way — and the last two exist because the condition
budget cannot see them: an `in` list is charged as one condition however long it
is, and a sort has no budget of its own ([[D-060]]). The zero `Config` is a usable default — tighten it where
the endpoint is public.

**`AllowUnpaged` is the one field that is closed by default.** Every other bound
here bounds what a request may *name*; this one bounds how much comes back, and
it is the only knob a client can set that has no ceiling of its own —
`sqlrepo.MaxLimit` clamps it, and `MaxLimit` is unset by default. Without the
field, `{"unpaged": true}` (or `?unpaged=true`, or `?all=1`) is refused with a
`query.Error` at path `unpaged`: a 400 over HTTP, `InvalidArgument` over gRPC
([[D-060]]).

This is also what a resource has to declare to be read by `remote.GetAll`. There
is no "every row" route on the wire, so the client emulates `GetAll` with the
flag — a resource meant to be consumed that way says so once, at the far end.

Wire it into any binding:

```go
crudfiber.WithQuery[Article, int64, ArticleUpdate](cfg)
crudgin.WithQuery[…](cfg)
crudnet.WithQuery[…](cfg)
crudgrpc.WithQuery[…](cfg)
port.WithQuery(cfg)
```

## Using it directly

```go
var req query.Request
if err := json.NewDecoder(r.Body).Decode(&req); err != nil { … }

opts, err := req.Compile(articles.Meta(), cfg)
if err != nil {
    var qe *query.Error
    if errors.As(err, &qe) {
        // qe.Path and qe.Reason are both safe to hand back
    }
}
page, err := articles.Get(ctx, opts...)
```

`query.ParseQuery(url.Values)` parses the query-string form. `query.Coerce(s, t)`
converts one text value into a Go type through the type's own
`TextUnmarshaler` — exported because transports need it for path parameters too.

**Everything in a `query.Error` is safe to render.** It names the path that was
wrong and why, and never an internal name ([[D-044]]).

## What each call costs, in statements

A consumer cannot measure this from outside, and the number is the dominant cost
of using this library. Every figure below is read back from a recording
`crud.Source` driving real calls, on PostgreSQL with a page size of 20.

| Call | Statements | Why |
|---|---|---|
| `GetByID` | 1 | |
| `Get` | **2** | the page, then `SELECT count(*)` |
| `Get`, short first page | 1 | a page shorter than the limit is already the whole answer |
| `Get` with `SkipTotal` | 1 | `LIMIT n+1`; the extra row is the has-next probe |
| `Get` with a cursor, or `Unpaged` | 1 | |
| `GetAll` | 1 | no `LIMIT` — its contract is every matching row |
| `Count`, `Exists`, `Aggregate` | 1 | |
| `Save` | 1 | `INSERT … RETURNING`, or `INSERT … ON CONFLICT DO UPDATE … RETURNING` |
| `SaveAll(n)` | 1 | one multi-row `VALUES` |
| `Update` | **2** | the load-diff-write of [[D-010]]: `SELECT`, then `UPDATE … RETURNING` |
| `UpdateAll`, `Delete(ids…)`, `DeleteAll` | 1 | |

**MySQL adds a read-back** wherever PostgreSQL uses `RETURNING`: `Save` 2,
`Update` 3.

**A preload is one statement per relation per level**, per 900-key chunk
([[D-006]]). Two relations at one level is two more statements, not one join.

**The security gate adds statements only where it has to.** With a scope and no
`Inspect`: no change, except `Save` over an existing key, which probes first.
With an `Inspect`, the filtered writes fetch their victims — `UpdateAll`,
`Delete` and `DeleteAll` each become 2, because the rule has to see every row it
destroys ([[D-026]]).

**Inside a transaction the counts are identical**, and `Update`'s load gains
`FOR UPDATE`. **With a replica**, `Get`'s page and count both go to the replica
and every read that decides a write stays on the primary ([[D-032]]).

## Indexes these shapes imply

The query DSL bounds what a request may *name*. It does not bound what that
request costs, and four shapes are worth knowing before a table gets large.

| Shape | SQL | What it needs |
|---|---|---|
| `?search=go` with no `searchFields` | `LIKE '%go%'` on **every string column** of the root model, ORed | no B-tree index serves a leading wildcard. Use a trigram or full-text index, or set `Searchable` to the one or two columns that are really searched |
| a relation filter | a correlated `EXISTS` per hop ([[D-005]]) | an index on the target's join column, and on the filtered column |
| a relation sort | a scalar subquery per outer row | the same, and consider denormalising if it is a default sort |
| `?page=5000` | `LIMIT 20 OFFSET 99980` | nothing helps; `OFFSET` is O(n) on every engine. This is what cursors are for ([[D-028]]) |

The first three run **twice** on a paginated read: once for the page and once
for the total. `SkipTotal` halves them.

## See also

- [port](port.md) — where a compiled request becomes a command
- [crudhttp](crudhttp.md) — how a `query.Error` becomes a 400
- [[UC-002]] let an untrusted client query · [[UC-006]] query and sort across relations
- [[FL-012]] a wire value becomes a Go value
