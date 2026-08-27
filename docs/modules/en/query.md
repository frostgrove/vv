# query — the wire DSL

```go
import "github.com/frostgrove/vv/crud/query"
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
  "preload": ["author", {"path": "comments", "filter": {"approved": true}, "maxRows": 100}],
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
| `after`, `before` | cursor paging — an opaque string a previous page handed back. Replaces `page`/`offset`, but keeps the endpoint page limit. It requires an explicit sort whose terms (and implicit primary-key tiebreaker) are filterable |
| `sort` | `["-views", "author.name"]`. A leading `-` is descending |
| `select` | projection. Preload join columns are kept automatically |
| `preload` | `["author"]` or `[{"path": "comments", "filter": {…}, "maxRows": 100}]`; a nested path must declare every hop. `maxRows` tightens the endpoint's preload-row ceiling; it can never widen it. |
| `filter` | the nested form, below |
| `terms` | the flat form, ANDed with `filter` |
| `search`, `searchFields` | a parenthesised, case-insensitive OR over text columns |
| `unpaged` | ask for every matching row; requires `AllowUnpaged` and is physically clamped if the repository declares `MaxLimit` |
| `skipTotal` | drop the `COUNT` |
| `distinct` | `SELECT DISTINCT`; requires `AllowDistinct` |

## Operators

```
eq  ne  gt  gte  lt  lte
like  notLike  ilike  contains  startsWith  endsWith
iContains  iStartsWith  iEndsWith
in  notIn  between  isNull  isNotNull
```

With `$`-prefixed and symbolic aliases — `$gte`, `>=`. A bare value means `eq`,
a bare array means `in`, `null` means `IS NULL`.

Field names are matched case- and separator-insensitively, so `createdAt`,
`created_at` and `CreatedAt` are the same column.

`like`, `notLike` and `ilike` are raw SQL-pattern operators. `contains`,
`startsWith`, `endsWith` and their `i…` variants treat their value as literal
text: `%`, `_` and backslash are escaped and the server adds the wildcard and
the dialect-appropriate `ESCAPE` clause. The `i…` variants match through
portable `LOWER()`.

## Three things it gets right

- **Values are typed by their column.** `{"views": {"gte": 100}}` binds an `int`,
  not a `float64`; `{"createdAt": {"gte": "2026-01-02"}}` binds a `time.Time`. A
  column whose type has a `TextUnmarshaler` parses through it, so uuid and enum
  types keep their own rules. Byte columns use base64 in both JSON and a query
  string, matching Go's standard JSON representation ([[FL-012]]).
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
structural**, so timestamps survive. A pipe starts another term only when the
following text has the shape of one; `\\|` spells a literal pipe in the ambiguous
case. A comma separates values only for `in` and `notIn`; it is literal for
scalar operators, and `\\,` keeps one comma inside a list value. Backslash quotes
only comma, pipe, backslash and the literal `null`; an ordinary backslash (a
Windows path or regular expression, for example) is preserved. Bare `null`
means SQL null; `\\null` means the literal string. `filter=` takes the full JSON
document for anything the flat form cannot express.

**JSON is one unambiguous document.** Duplicate object keys are a 400 at every
depth; an option itself, or an element in `sort`, `select`, `searchFields` or
`preload`, cannot be `null`. This prevents a proxy or validator from seeing one
filter while the server executes a last-wins variant. `null` remains meaningful
where the DSL explicitly gives it meaning, such as a filter operand or a term
value.

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
    MaxSelect:     8,   // projection entries
    MaxBindValues: 500, // all bound values across the document
    MaxLimit:      50,  // client page size; default is 100
    MaxOffset:     5000,
    MaxPreloadRows: 100, // children at every requested relation hop; excess is a 400

    AllowUnpaged: true, // this endpoint serves whole result sets
    AllowDistinct: true, // only if this endpoint genuinely needs it
}
```

**Empty lists allow anything the model maps.** Depth, condition, preload, list
and sort limits apply either way. The zero `Config` remains a usable first
screen, but has active work budgets: 100 rows per client page, offset 10,000,
8,192 values across a document and 1,000 children per preload. A larger client
limit is capped; that cap is emitted even when `limit` is omitted and when a
cursor replaces offset paging. Too-deep offset, too many projections, binds or
preload rows are refused. An `in` list is still charged as one condition, which
is why list and bind budgets exist separately. A cursor is charged as the
lexicographic predicate the repository will generate — two sort terms cost three
conditions and three binds — so it cannot bypass those same budgets ([[D-060]]).

The scalar query-string controls are deliberately strict: a control may appear
once under one spelling. Repeating `limit`, combining `q` with `search`, or
using both `after` and `before` is a 400, never an order-dependent choice. This
keeps a proxy, framework and service from interpreting the same URL differently.

**`AllowUnpaged` and `AllowDistinct` are closed by default.** The first has no
ceiling of its own; the second can force a full deduplication pass. `unpaged`
is still subject to the repository's `MaxLimit`, so an export should use a
repository whose declaration matches its intended result size —
`sqlrepo.MaxLimit` clamps it, and `MaxLimit` is unset by default. Without the
field, `{"unpaged": true}` (or `?unpaged=true`, or `?all=1`) is refused with a
`query.Error` at path `unpaged`: a 400 over HTTP, `InvalidArgument` over gRPC
([[D-060]]).

`remote.GetAll` never sends `unpaged`. It reads bounded pages and follows cursor
edges, using the caller's sort or the model primary key. A restrictive endpoint
intended for remote exports grants that root path in both `Sortable` and
`Filterable`; the latter is required because a cursor is a lexicographic filter.
A custom list implementation without edges — and a `Distinct` projection that
excludes the primary key, which cannot be keyset-sorted without changing its
meaning — uses offset pages, so its `MaxOffset` must cover the export.

Wire it into any binding:

```go
crudfiber.WithQuery[Article, int64, ArticleUpdate](cfg)
crudgin.WithQuery[…](cfg)
crudnet.WithQuery[…](cfg)
crudgrpc.WithQuery[…](cfg)
port.WithQuery(cfg)
```

`port.NewService` validates every static `WithQuery` allow-list against the
model at construction, so a misspelt declaration fails where it is mounted.
When compiling directly, call `cfg.MustCheck(articles.Meta())` beside the
declaration.

When one route legitimately has role-specific vocabularies, keep them
declarative too: `port.WithQueryFor(defaultCfg, map[string]*query.Config{
"admin": adminCfg}, selectVocabulary)` validates every vocabulary at startup.
The selector receives the request context; `""` chooses the default and an
unknown name is refused rather than falling back to an unrestricted query.

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
Its `MaxPreloadRows` budget is applied at every hop of a nested path, fetches
one row beyond the cap and refuses instead of silently returning a partial
relation.

**The security gate adds statements only where it has to.** A custom scope with
no `Inspect` is read-only: body writes are refused before SQL. With an
`Inspect`, filtered writes fetch their victims — `UpdateAll`, `Delete` and
`DeleteAll` each become 2, because the rule has to see every row it destroys
([[D-026]]).

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
