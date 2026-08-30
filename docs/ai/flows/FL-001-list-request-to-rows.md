# FL-001 — A list request from wire to rows

**Entry point:** `crud/http/crudfiber/handler.go:List` (GET) and `crud/http/crudfiber/handler.go:Query` (POST /query)
**Implements:** [[UC-001]] [[UC-002]] · **Governed by:** [[D-013]] [[D-004]] [[D-014]] [[D-024]] [[D-060]] [[D-063]]

Two doors, one path. Everything after parsing is shared, which is the point: a
filter that works on `GET /articles?f=…` has to mean the same thing on
`POST /articles/query`.

## The path

1. **`HandlerFor.List`** — `crud/http/crudfiber/handler.go:List`
   Reads the query string through `queryValues`, which walks
   `QueryArgs().VisitAll` rather than Fiber's `Queries()`. `Queries()` collapses
   repeats into a map, so the second `f=` would vanish silently — a narrower
   filter than the client asked for, with a 200 on it.
   **`HandlerFor.Query`** — `handler.go:Query` — reads the JSON body instead
   (`parseBody` → `decodeOnly` → `decode` → `porthttp.DecodeJSONKeepLimit`; an empty body is a
   legal empty request, and a body past `MaxBody` is refused before anything
   parses it — [[D-063]]). `Request.UnmarshalJSON` (`crud/query/request.go`)
   decodes with `DisallowUnknownFields`: the strictness inside the document is
   worth nothing if `{"filtr":…}` parses as a document with no filter.

2. **`query.ParseQuery`** — `crud/query/querystring.go:143`
   Query string → `query.Request`. `checkParams` runs first and refuses a
   parameter one edit away from one of ours, because `?filtr=` left alone is a
   200 with the whole table. A name that is nothing like ours belongs to the
   application and passes. Numbers are parsed here and a non-number is
   rejected here (`page`, `limit`, `offset`). `f=` and `filters=` are split on
   `|`, then each triple goes through `ParseTerm` (`querystring.go:28`), which
   splits on the **first two** colons only, so a timestamp value survives. The
   `filter=` parameter is handed on raw, whatever it contains: dropping a
   malformed document here would turn a bad filter into an unfiltered answer,
   which is the one failure a client cannot see.

3. **`HandlerFor.list` → `port.ListCommand` → `DefaultService.List`** —
   `crud/http/crudfiber/handler.go:list`, `port/service.go:List`
   The binding reads `WithScope`, if any, into the command's `Options` and hands
   the parsed document over. The service calls `Request.Compile` and then appends
   those options. Appended, not prepended — harmless, because `crud.Where` ANDs
   (`crud/options.go:65`) and nothing in the option list can subtract. [[FL-015]]
   is the rest of this hop.

4. **`Request.Compile`** — `crud/query/compile.go:105`
   The whole validation pass. Nothing reaches SQL that did not resolve against
   the model first. In order: filter document, flat terms, search, sort,
   projection, preloads, paging. Each produces `crud.Option` values.
   - filter → `compiler.node` (`crud/query/filter.go:16`), keys sorted so the
     generated SQL is byte-identical for the same document.
   - terms → `compiler.terms` (`crud/query/querystring.go:46`).
   - search → `compiler.search` (`crud/query/compile.go:379`), wrapped in its own
     `crud.Or` node so it can never leak out of the surrounding AND.
   - every path → `compiler.path` (`crud/query/compile.go:360`) → `Meta.FieldAt` →
     `Meta.WalkPath` (`crud/relation.go:344`), which returns the **canonical**
     spelling. From here on only the canonical spelling exists; `createdAt`,
     `created_at` and `CreatedAt` have already become one thing.

5. **The allow-lists** — `allowed`, `crud/query/compile.go:81`
   Enforced at six separate sites, each against the canonical path:
   `Filterable` in `compiler.condition` (`crud/query/filter.go:124`) and in
   `compiler.terms` (`querystring.go:53`); `Sortable` in `Compile`
   (`compile.go:151`); `Selectable` (`compile.go:177`); `Preloadable`
   (`compile.go:193`); `Searchable` (`compile.go:389` and `compile.go:404`).
   A preload's own filter and sort compile through a **sub-compiler** with
   `prefix` set (`compile.go:247`), so its paths are checked against the root's
   list spelled from the root: `Comments.Body`, not `Body`.
   An empty list allows everything; `"Comments.*"` allows a subtree.

6. **Budgets** — `compiler.count` (`compile.go`), `maxDepth`, `maxPreloads`,
   `compiler.countValues`, `maxSort`
   One shared condition counter for the whole document, including a preload's
   sub-filter, so a client cannot buy more conditions by nesting them. Defaults
   when `Config` is nil: depth 6, 64 conditions, 16 preloads, 1024 values in one
   `in`/`notIn` list, 16 sort terms.
   The last two measure volume rather than names, which is why they are separate
   counters: a list is charged as **one** condition however long it is, and a
   sort was charged nothing at all ([[D-060]]). `Compile` also drops a repeated
   canonical sort path rather than rendering it twice — a second `ORDER BY` over
   a column already sorted decides nothing and still pays for the term, which
   for a relation hop is a correlated subquery.

7. **Paging** — `Compile`, then `repository.Get` — `crud/sqlrepo/repository.go:Get`
   `Unpaged` is refused in `Compile` unless the endpoint declared
   `query.Config{AllowUnpaged: true}`, at the parameter spelling the client sent
   — `Request.UnpagedParam` answers `unpaged` or its alias `all`. That refusal
   is the ceiling, because the one below it was never armed: `MaxLimit` defaults
   to no cap ([[D-060]]).
   `Options.Resolved` (`crud/options.go:Resolved`) then turns page/limit/offset into a
   limit and an offset, clamped to the blueprint's `MaxLimit`. An `Unpaged` that
   got this far is still honoured only as far as `MaxLimit` — one flag from the
   wire does not talk a repository out of its declared ceiling. A page number
   large enough to overflow `int` saturates instead of wrapping.

8. **The COUNT decision** — `crud/sqlrepo/repository.go:152-168`
   This is the part worth holding in your head. Four cases:
   - `SkipTotal` **and** a limit: no COUNT at all. The SELECT fetches `limit+1`
     rows, the extra one is dropped, and `HasNext` is true if it was there. At
     `math.MaxInt` the probe saturates instead of wrapping negative and silently
     dropping `LIMIT`; no Go slice can contain the unrepresentable extra row.
     `Total` is the page length and `TotalPages` is 0.
   - `Unpaged`: `total = offset + len(items)`.
   - offset 0 and a short page: the first page is the whole answer, so
     `total = len(items)` and no COUNT is issued.
   - otherwise: a second statement, `repository.Count` (`repository.go:392`),
     replaying the same options through `crud.With`.

9. **`repository.find`** — `crud/sqlrepo/repository.go:193`
   - `projection` (`repository.go:257`) resolves `Select` into a field list and
     **adds the primary key**, plus any column a preload joins on. Under
     `Distinct` it adds nothing: the key is unique, so carrying it would stop
     `SELECT DISTINCT` removing anything.
   - `hasPK` (`repository.go:305`) then decides two things: a preload over a
     keyless projection is refused with a `SchemaError`, and the
     stable-pagination tiebreaker is only appended when the rows can be
     identified.
   - `sortOf` (`repository.go:373`) falls back to the blueprint's `DefaultSort`
     and appends `Asc(PK)` unless the sort already names the key or
     `UnstablePagination()` was declared. Without it, two rows with the same
     sort value can appear on both page 1 and page 2.
   - `distinctSort` (`repository.go:330`) refuses a caller's sort that a
     `DISTINCT` projection cannot carry, and silently drops the repository's
     own default sort in the same situation. Widening the projection instead
     would produce a statement that runs and an answer nobody asked for.

10. **`crud.NewSQL` → `Done`** — `crud/render.go:12`, `crud/render.go:138`
    The predicate tree renders here. `RelationScopes` (`render.go:27`) is
    attached first, so a nested filter's subquery carries the narrowing —
    see [[FL-005]]. The first field-resolution failure is remembered rather
    than panicking, and surfaces from `Done`.

11. **`repository.queryCols`** — `crud/sqlrepo/repository.go:753`
    One destination slice built once per query, pointing into a single scratch
    model; each row is copied out on append. `Meta.Pointers`
    (`crud/access.go:18`) computes the destinations by field offset.

12. **`repository.preload`** — `repository.go:364` → [[FL-006]]
    Runs on the same executor, so a preload inside a transaction sees the
    transaction.

13. **`crud.NewPaginatedResponse`** — `crud/page.go:16`
    Derives `TotalPages`, `HasNext`, `HasPrev`. A nil item slice becomes `[]`,
    so the JSON is an array and never `null`.

14. Back in `HandlerFor.list`: `writeJSON(c, 200, page)`, or `crud.MapPage` when
    a `WithTransform` presenter is configured. `writeJSON` marshals before it
    touches the status, so a presenter that returns a value JSON cannot encode is
    a silent 500 rather than a half-written 200 ([[D-063]], [[FL-013]]).

## Where the decisions bite

- **An unknown field is a rejection, never an ignored clause.** Every path goes
  through `compiler.path` before any predicate is built. A typo that silently
  dropped its clause would return the whole table with a 200.
- **The allow-lists are checked against the canonical path, after resolution.**
  Checking the client's spelling would let `created_at` slip past a list that
  says `CreatedAt`. `TestADeniedColumnStaysDeniedHoweverItIsSpelled` is the
  regression test.
- **The projection always carries the primary key — except under DISTINCT.**
  Preloads attach by key and the pagination tiebreaker breaks ties by key. The
  `DISTINCT` exception is deliberate and is why `find` re-checks with `hasPK`.
- **The wire cannot ask to be unpaged unless the endpoint said it serves that.**
  `Compile` refuses first; `Options.Resolved` clamps second. Two bounds, and the
  second one was the only one there for a long time — with `MaxLimit` unset by
  default it clamped to nothing at all ([[D-060]]). `GetAll` deliberately does
  not go through `Resolved` when no paging option was given
  (`repository.go:174`): its contract is every matching row, and a decorator
  that reads a whole set in order to check it would otherwise check the first
  page and let the rest through. That is the in-process `GetAll`; the remote one
  is emulated with the flag and therefore needs the far endpoint's declaration
  ([[FL-018]]).

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| a `POST /query` body past `MaxBody` | `porthttp.DecodeJSONKeepLimit`, before anything parses it | 413 `too_large` naming the limit ([[D-063]]) |
| an unknown top-level key in the JSON document | `Request.UnmarshalJSON` | 400, the key named, and the accepted set offered back |
| `?filtr=` — a parameter one edit from a real one | `ParseQuery` → `checkParams` | 400, "did you mean …" |
| `page=abc` | `ParseQuery` → `num` (`querystring.go:146`) | 400 `{"error":"bad_request","path":"page"}` |
| `f=x` (no operator segment) | `ParseTerm` (`querystring.go:39`) | 400, path `filter` |
| unknown field in filter/sort/select/preload | `compiler.path` → `Meta.WalkPath` | 400, path names the exact clause |
| field resolves but is not allow-listed | `allowed` at the six call sites | 400 `"X is not filterable/sortable/…"` |
| document deeper than `MaxDepth`, or more than `MaxConditions` leaves | `compiler.node` / `compiler.count` | 400 |
| an `in`/`notIn` list past `MaxInValues`, or more than `MaxSort` sort terms | `compiler.countValues` / `Compile` | 400 naming the path and the cap ([[D-060]]) |
| `unpaged` on an endpoint that did not declare it | `Compile` | 400 at the spelling the client sent — `unpaged` or `all` — and the repository is never asked |
| `select` crossing a relation | `Compile` (`compile.go:174`) | 400 "use preload instead" |
| `DISTINCT` + a sort it cannot project | `distinctSort` (`repository.go:330`) | 400 (`SchemaError`) |
| `DISTINCT` + a preload | `find` (`repository.go:206`) | 400 (`SchemaError`) |
| driver or connection failure | `queryCols` | 500, body says nothing — [[FL-011]] |

## Files

| File | Role |
|---|---|
| `crud/http/crudfiber/handler.go` | routes, query-string reading, option assembly |
| `crud/http/crudgin/handler.go`, `crud/http/crudnet/handler.go` | the same, for Gin and `net/http` — `URL.Query()` in place of the `queryValues` workaround ([[FL-013]]) |
| `port/porthttp/body.go` | `DecodeJSONKeepLimit`, `MaxBody`, `TooLarge` — the bounded read every binding and every subsystem shares ([[D-059]], [[D-063]]); `crudhttp` forwards to them |
| `port/service.go` | `DefaultService.List` / `:Count` / `:Get` — where the document is narrowed and compiled ([[FL-015]]) |
| `port/request.go` | `NarrowForCount`, `NarrowForEntity` — what a count and a keyed read drop |
| `crud/query/querystring.go` | `ParseQuery`, `ParseTerm`, flat-term compilation |
| `crud/query/request.go` | the `Request` document, its forgiving JSON shapes, and `UnmarshalJSON` — forgiving about how a value is written, strict about which keys exist |
| `crud/query/compile.go` | `Compile`, the allow-lists, budgets, path resolution |
| `crud/query/filter.go` | the structured filter document → predicates |
| `crud/options.go` | `Options`, `Where`, `Resolved` |
| `crud/sqlrepo/repository.go` | `Get`, `find`, `projection`, `sortOf`, `Count` |
| `crud/render.go` | statement assembly |
| `crud/relation.go` | `WalkPath` — the single source of truth for paths |
| `crud/page.go` | the response envelope |

## Tests that walk this flow

Each of the four below has an identical twin in `crud/http/crudgin/handler_test.go`
and `crud/http/crudnet/handler_test.go`.

- `TestListCompilesQueryStringPagingAndSorting` — `crud/http/crudfiber/handler_test.go` — the query-string door end to end.
- `TestQueryBodyCompilesTheWholeDSL` — `crud/http/crudfiber/handler_test.go` — the JSON door.
- `TestRepeatedFilterTermsAllSurvive` — `crud/http/crudfiber/handler_test.go` — pins the `queryValues` workaround.
- `TestListAnswersWithThePageEnvelope` — `crud/http/crudfiber/handler_test.go` — the response shape.
- `TestEachAllowListGuardsItsOwnVerb` — `crud/query/compile_test.go` — one list per verb, no cross-authorisation.
- `TestADeniedColumnStaysDeniedHoweverItIsSpelled` — `crud/query/hostile_test.go` — canonicalisation before the allow-list.
- `TestTheDefaultBudgetsBoundAnUnconfiguredEndpoint` — `crud/query/hostile_test.go` — a nil `Config` is still bounded.
- `TestAClientChosenListAndSortAreBounded` — `crud/query/compile_test.go` — the two volume caps and the sort dedup, with the control that the same 40-value list compiles clean under `MaxConditions: 1`, so the condition budget really never saw it.
- `TestUnpagedIsRefusedUnlessTheEndpointServesIt` — `crud/query/compile_test.go` — the refusal, the declaring control, and that it is a `query.Error` naming the parameter rather than a 500.
- `TestAMisspelledDocumentKeyIsRefused` / `TestAMisspelledQueryParameterIsRefused` — `crud/query/strict_test.go` — the two doors' own key sets, with `TestEveryDocumentKeyStillParses` and `TestAnApplicationsOwnParametersArePassedThrough` as the controls.
- `TestGetSkipsCountOnShortFirstPage` — `crud/sqlrepo/repository_test.go` — the no-COUNT case.
- `TestSkipTotalProbesOneExtraRow` — `crud/sqlrepo/repository_test.go` — the `limit+1` probe.
- `TestSkipTotalReportsWhatWasFetchedAndNotTheOffset` — `crud/sqlrepo/paging_edge_test.go` — pins the fabricated-total regression.
- `TestSkipTotalDoesNotOverflowTheLargestLimit` —
  `crud/sqlrepo/paging_edge_test.go` — the `limit+1` probe saturates and the
  statement keeps its limit.
- `TestMaxLimitSurvivesEveryWayAPageCanBeAskedFor` — `crud/sqlrepo/paging_edge_test.go`.
- `TestUnstablePaginationDropsTheTiebreaker` — `crud/sqlrepo/paging_edge_test.go`.
- `TestAPageNumberThatWouldOverflowAsksForAPagePastTheEnd` — `crud/sqlrepo/paging_edge_test.go`.
- `TestTheSameDocumentAlwaysCompilesToTheSameStatement` — `crud/query/edge_test.go` — deterministic key order.

## See also

[[FL-005]] [[FL-006]] [[FL-007]] [[FL-011]] [[FL-012]] [[FL-013]]
