# query — the wire DSL

```go
import "github.com/shardit-io/vv/query"
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
}
```

**Empty lists allow anything the model maps.** Depth, condition and preload
limits apply either way. The zero `Config` is a usable default — tighten it where
the endpoint is public.

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

## See also

- [port](port.md) — where a compiled request becomes a command
- [crudhttp](crudhttp.md) — how a `query.Error` becomes a 400
- [[UC-002]] let an untrusted client query · [[UC-006]] query and sort across relations
- [[FL-012]] a wire value becomes a Go value
