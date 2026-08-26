# crud/query — one JSON document or query string becomes a bounded repository call

**Covers:** `github.com/frostgrove/vv/crud/query`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — a cleared filter chip (`?f=status:notIn:`) compiles to `1 = 1` and answers 200 with every row; empty boolean groups, malformed unary terms and invalid paging can also silently change or remove the intended question. Four separate volume bounds remain unarmed on a stock mount: the page size, offset depth, `distinct`, and preload row count.

## What a consumer is actually trying to do

Somebody has a list screen. It has a status dropdown, a date range, a search
box, two sortable columns and a page control, and the front end changes which
of those it sends every sprint. Writing an endpoint per combination does not
scale and never did. What they want is one endpoint that takes the question and
answers it.

They also know what the cheap version of this costs. A hand-rolled filter layer
takes a map of field names and pastes them into SQL, and then one of two things
happens: a typo in a field name silently drops the clause and the list quietly
returns every row, or the field name reaches the statement and the endpoint is
an injection point. So the thing they are shopping for is not "filters" — it is
a promise that a name the model does not have is a refusal, that nothing the
client typed becomes SQL text, and that no shape of an accepted request ever
means "no condition at all".

Then they remember it is a public endpoint. A stranger can send that document.
So the second thing they want is a way to say, per endpoint, what may be asked:
these columns filtered, those sorted, this relation loaded, and no more than
this much of it. They want to write that once, next to the route, and be told at
start-up if they got it wrong — not discover in production that a column they
meant to expose has been refusing every request for a month.

The bound they care about most is the one they will be paged about. Not "how
many field names may this document mention" but "how many rows can one stranger
make this process hold, and how much can one stranger make it scan". Those are
three different questions, and a consumer reading a list of caps assumes the
second and third are covered because the first is.

Somebody also has to *build* the chips. The screen that sends the filter is
generated from a list of what may be filtered, and if that list lives only in Go
the front end keeps a second copy in TypeScript. The two drift in the direction
nobody notices: a column dropped from the allow-list becomes a chip that 400s.

And the model is not a demo model. The primary key is a `uuid.UUID`, the status
is an enum type that rejects values it does not know, the price is a decimal, and
half the interesting attributes live in one JSON column. Whether those columns
can be filtered at all — and what a client sees when the enum refuses a value —
is the first thing an adopter tries, not a corner case.

The last thing, and the one nobody asks for until the second month: the same
question has to be sendable two ways. A dashboard links to a filtered list, so
the state has to fit in a URL. A saved view lives in a database column, so it
has to be a document that round-trips. And an admin has to be able to ask for
more than the public does, on the same route, because the role is in the token
and not in the path.

## Happy cases

### H-QUERY-01 — The first list screen
**Who:** a backend developer on day one of a new admin panel
**Wants:** a filtered, sorted, paged list endpoint without writing one
**Story:** They declare the model, bind the repository, mount the routes and
hand the front end the URL. The front end sends `?f=status:eq:draft` in the flat
form on `GET /articles`, sorts by a column header, and pages. The same question
can also be posted as a JSON body to `POST /articles/query` when it stops
fitting in a URL. Nothing is configured yet.
**Must hold:**
1. `?page=2&limit=20&sort=-createdAt&f=status:eq:draft` filters, sorts and pages.
2. A request that names nothing leaves the repository's own page size in place —
   it does not become `LIMIT 0` or "every row".
3. A client cannot choose how many rows come back.
4. Every accepted filter shape narrows. No spelling of an accepted filter means
   "no condition".
**Today:** 🟡 partial — 1 and 2 hold; 3 and 4 do not, and both fail on exactly
the stock mount this case describes
**Evidence:** 1 is the query-string parse at `crud/query/querystring_test.go:197`
`TestParseQueryReadsEveryParameter` and the shared `Compile` at
`crud/query/compile.go:236-401`; the flat-term compiler both doors share is
`querystring.go:46-125`. 2 is `crud/query/compile_test.go:128`
`TestAnEmptyRequestKeepsTheRepositoryPageSize` — an empty document still renders
`LIMIT 20`. 3 fails: `compile.go:353-354` passes the client's `limit` through
unchanged and `sqlrepo.MaxLimit` is unset by default — see H-QUERY-07. 4 fails:
`?f=status:notIn:` compiles to the constant `1 = 1` — see H-QUERY-17, which is
this sweep's worst finding and lands on this mount with no config anywhere.
Value parity between the two doors is H-QUERY-12's; `select` narrowing the
statement and not the response body is the crudhttp sweep's, and is the one
thing `Selectable` does not buy.
**If not ready:** For 3, `sqlrepo.MaxLimit(100)` in the `Define` — another file,
and no doc requires it. For 4 there is nothing to write at the endpoint: the
front end must omit a filter parameter rather than send it empty, on every chip,
forever.

### H-QUERY-02 — Lock the public endpoint down, and be told when the lock is wrong
**Who:** the same developer, the week the endpoint goes public
**Wants:** to declare what may be filtered, sorted, selected, preloaded and
searched, and to find out at start-up if a name in that list is misspelled
**Story:** They write a `query.Config` next to the mount with five lists. Later
someone renames the Go field `Views` to `ViewCount`, and someone else adds a
column called `InternalNotes` — hidden from the response with `json:"-"` — to
the same struct.
**Must hold:**
1. The five lists are independent — filtering through a relation is not
   permission to preload it.
2. A list entry may name a subtree, whole segment at a time.
3. A column you did not list is refused however it is spelled — `authorEmail`,
   `author_email`, `AuthorEmail`.
4. An entry naming nothing on the model stops the process where it was written,
   not the clients where it was not.
5. A column added to the model after the endpoint was reviewed does not become
   filterable, sortable or searchable on it.
6. An endpoint whose lists name three cheap columns cannot be asked an expensive
   question about them.
**Today:** 🟡 partial — 1–3 hold, 4 exists and no production path calls it, 5 and
6 do not hold
**Evidence:** 1–3 are pinned: `compile_test.go:227`
`TestEachAllowListGuardsItsOwnVerb`, `compile_test.go:180`
`TestAllowListMatching`, `crud/query/hostile_test.go:211`
`TestADeniedColumnStaysDeniedHoweverItIsSpelled`. 4 exists as
`Config.Check`/`MustCheck` (`crud/query/compile.go:152-208`) and is called by
exactly one thing — its own test, `compile_test.go:712`
`TestAnAllowListEntryThatNamesNothingIsRefusedAtDeclaration`. No library code,
no binding, no example and no consumer-facing page calls it
(`grep -rn "\.Check(\|MustCheck" --include="*.go"` returns the definition, that
test, and `compile.go:204`), while [[D-060]]'s **Proven by** records it as the
closure of this failure — so a binding decision doc believes a hole is closed
that only a test closes. `Check` also returns on the *first* bad entry
(`compile.go:174-186`), which is one restart per typo once something does call
it. 5 fails at `compile.go:212-215`: `allowed` returns true for an empty list,
so an endpoint that left `Searchable` and `Filterable` empty grants every column
the model gains afterwards — a later `InternalNotes` hidden from the payload is
still searchable, and a search hit is then an oracle for a column the API never
renders. 6 fails because every cap in `query.Config` bounds what a request may
*name* or how much it names; none bounds what a named thing costs.
`{"title": {"contains": "a"}}` is a leading-wildcard `LIKE` on any `Filterable`
text column, run twice per paginated read.
**If not ready:** For 4, `func init() { must(cfg.Check(Articles.Meta())) }` per
resource — once they know it exists. The one line that closes it for every
transport is in `port.NewService` (`port/service.go:82-96`), which already
computes `repo.Meta()` and which all four bindings' `New` route through;
`WithQuery` cannot do it, because it closes over an options struct that holds no
repository (`crud/http/crudnet/options.go:64-66`). For 5 and 6 there is nothing
to write: name every column explicitly, re-review the lists on every model
change, and accept that a permitted column is a permitted scan.

### H-QUERY-03 — The search box
**Who:** a product engineer wiring one input above a table
**Wants:** `?q=smith` to match across two columns and nothing else
**Story:** They set `Searchable` to the two columns the box is meant to search
and `DefaultSearchFields` to the same, then hand the front end `?q=`. The first
thing anyone types into it is a name with a capital letter.
**Must hold:**
1. Adding `?q=` to a filtered list never returns a row the filter alone
   excluded.
2. `%` and `_` typed by the client are literal characters, not wildcards.
3. `?q=smith` matches "Smith", and matches it the same way on PostgreSQL and on
   MySQL.
4. A search field the endpoint did not permit is a refusal naming the field.
5. A search this endpoint cannot serve is a refusal — never a 200 whose body is
   the whole table wearing the label "results".
**Today:** 🟡 partial — 1, 2 and 4 hold; 3 and 5 do not
**Evidence:** 1 and 2 hold: `crud/query/query_test.go:251`
`TestSearchIsParenthesised` and `compile_test.go:137`
`TestFilterTermsAndSearchAreAndedNotMerged` pin the parenthesised `WHERE` so the
search node cannot leak out of the surrounding AND; `hostile_test.go:185`
`TestWildcardsInAPatternAreEscaped` is the escaping, at `crud/predicate.go:436`.
4 holds: `hostile_test.go:277` `TestSearchCannotReachOutsideItsList`. 3 fails:
`compile.go:580` and `:616` both build the search with `crud.Contains`, which is
`Like(field, "%"+escapeLike(s)+"%")` (`crud/predicate.go:436`) — a
case-**sensitive** `LIKE`. `crud.LikeIgnoreCase` sits two lines above it and
compares with `LOWER()` on both sides "which works on MySQL and PostgreSQL
alike" (`crud/predicate.go:430-433`), and the search path does not use it, while
`compile.go:565` claims in its own comment that search "builds a
case-insensitive OR". So `?q=smith` misses "Smith" on PostgreSQL and usually
matches it on MySQL under a default `_ci` collation: the same endpoint, two
answers, decided by the engine. 5 fails in **both** arms of `search`, not only
the fallback. With no field list, `compile.go:576-587` walks the root model's
string columns, keeps the permitted ones, and returns a nil predicate if none
survives. With an explicit list, `compile.go:599-627` joins a non-string column
only `if v, err := coerceString(...); err == nil` (`:620`), and ends the same way
— `if len(preds) == 0 { return nil, nil }` at `:624`. So
`?q=smith&searchFields=views` on an int column answers with the whole table,
from a **correct** config, on a query string a stranger sends.
`compile_test.go:302` `TestSearchWithNothingToSearchProducesNoPredicate` pins the
behaviour on purpose.
**If not ready:** For 3, give up the search box and make the front end send
`{"title": {"ilike": "%smith%"}}` — which reaches `crud.LikeIgnoreCase`
(`crud/query/filter.go:279-280`) and hands the client the raw pattern, giving up
the escaping guarantee 2 is about. For 5, list only text columns in
`DefaultSearchFields` and never rely on the fallback; the fix is to refuse
instead of returning nothing, in both arms, which is the posture [[D-013]] takes
everywhere else in this package. UC-002 records this asymmetry as "worth knowing
rather than fixing" — see **Contested**.

### H-QUERY-04 — Filter across a relation and load it in the same request
**Who:** whoever builds the "articles by author, with the author shown" screen
**Wants:** `tags.slug in [go,rust]`, `preload=author`, one call
**Story:** They send a filter that walks a relation and a preload list in the
same document, then read the page.
**Must hold:**
1. A relation filter does not multiply rows or inflate the total.
2. Permission to preload a relation is not permission to filter by its columns.
3. A preload may carry its own filter and sort, validated against the target
   model.
4. A preload's filter spends the same condition budget as the rest of the
   document.
5. Listing one deep preload path grants that path and nothing shallower.
**Today:** 🟡 partial — 1–4 hold, 5 does not
**Evidence:** 1 is measured, not assumed:
`test/integration/relations_test.go:222`
`TestToManyFilterDoesNotDuplicateOrInflateCount`; the mechanism is the
correlated `EXISTS` of [[D-005]]. 2 is `hostile_test.go:252`
`TestAPreloadableRelationIsNotAFilterableOne`. 3 is
`crud/query/preload_test.go:119` `TestFilteredPreload` and `:150`
`TestPreloadFilterIsScopedToTheTarget`. 4 is `hostile_test.go:361`
`TestAPreloadSpendsTheDocumentsConditionBudget`, with the prefix rule at
`compile.go:406-459` pinned by `crud/query/preload_allowlist_test.go:16` `TestAPreloadSubFilterCostsTheSamePermissionAsTheFilterPath`. 5 is
UC-002 Gap 2: the allow-list is not checked hop by hop, so
`Preloadable: []string{"Comments.Author"}` also grants `Comments`, because
reaching the far end requires reading the near end.
**If not ready:** Nothing to write — list the shallow paths deliberately and
know that a deep entry grants them anyway. See H-QUERY-13 for the larger preload
problem, which is not about permission at all. A per-relation filter and sort are
JSON-only: `pathsToPreloads` (`crud/query/request.go:296-304`) builds a bare path
from a query string, so this whole capability is unreachable from a URL except
through `?filter=` — H-QUERY-12.

### H-QUERY-05 — The server's own condition, which the client cannot widen
**Who:** an engineer on a multi-tenant SaaS
**Wants:** every list narrowed to the caller's tenant, whatever the client sent
**Story:** They add a scope that reads the tenant from the request and returns
repository options. A client sends `{"or": [...]}` covering the whole table and
still sees only its own rows.
**Must hold (this module's half):**
1. No document shape — an `or`, a `not`, an empty filter, an empty `notIn` list —
   produces a predicate that escapes an enclosing AND or replaces it.
**Today:** 🟡 partial — nothing escapes the AND; a `notIn` over an empty list
stays inside it and is `1 = 1`, which narrows nothing
**Evidence:** the nesting half is pinned by `hostile_test.go:413`
`TestOneClauseCannotEscapeAnother` — a nested `or` and a `not` each stay inside
their own parentheses. The constant half is `crud/predicate.go:196-201`
(`inNode.render` degrading an empty `NOT IN` to `1 = 1`), pinned as intended
behaviour by `edge_test.go:118` `TestAnEmptyValueListBecomesAConstant`. The tenant
scope still holds — `1 = 1 AND owner_id = $1` is still narrowed — so this is a
widening within the client's own half of the AND, not a scope escape.
**Preconditions owned elsewhere:** that the narrowing reaches the rows *and* the
total is `port`'s and the bindings': `port/service.go:240-248` appends the
caller's options after compiling and `crud.Where` ANDs ([[D-004]]), pinned by
`crud/http/crudnet/options_test.go:213`
`TestWithScopeIsANDedWithTheClientFilter` (a scope of `OwnerID = 7` renders
`("price" >= $1 AND "owner_id" = $2)`) and `:183`
`TestWithScopeNarrowsEveryRead`, with the same names at
`crudgin/options_test.go:215` and `crudfiber/options_test.go:216`.
**If not ready:** Nothing at the endpoint. The write-side caveat — `WithScope` is
reads only, so `DELETE /{id}` on another tenant's row answers 200 — is
`decorators/security`'s and is listed under **Lands on this consumer, owned
elsewhere**.

### H-QUERY-06 — Admins get a wider vocabulary on the same route
**Who:** anyone whose app has more than one kind of caller
**Wants:** support staff may filter by `email` and preload `Payments`; the
public may not — same URL, decided by the token
**Story:** They mount `/articles` once. The middleware puts a principal in the
context. They want the bounds chosen per request from that principal.
**Must hold:**
1. The config that bounds a request can depend on the caller.
**Today:** ❌ missing
**Evidence:** this module already supports it — `req.Compile(meta, cfg)` takes
the config as an argument, so nothing here needs changing. The missing seam is
everywhere else: `WithQuery` takes one `*query.Config` and stores it
(`crud/http/crudnet/options.go:64`, `crudgin/options.go:64`,
`crudfiber/options.go:63`, `crudgrpc/options.go:60`), `port.WithQuery` is the
same (`port/service.go:53-55`), and `port.NewService` reads it once at
construction (`port/service.go:82-96`).
**If not ready:** Two mounts under two paths when the role is in the URL,
otherwise a hand-written `port.Service`. **The remedy belongs to the `port`
sweep and lives there in full**; this case is kept because a consumer deciding
what to put in a `query.Config` learns here that the answer cannot vary by
caller — see **Contested**.

### H-QUERY-07 — A ceiling on how much comes back, and a defined order
**Who:** whoever is on call the night the list endpoint is found
**Wants:** "no client gets more than 100 rows, no client makes us scan the
table, and an unsorted list still comes back in a stable order"
**Story:** They read the query configuration, set the allow-lists, and expect
the bounds on volume to be in the same declaration.
**Must hold:**
1. There is a ceiling on how many rows one request can ask for, and it is on by
   default.
2. There is a ceiling on how deep into a result a request can page.
3. A client cannot set a knob that changes how much work the statement does.
4. A list with no `sort` has a defined order, so page 2 does not repeat rows
   from page 1.
5. A sort a client asks for means the same thing on every engine the library
   supports.
6. All of these are declared where the endpoint's other bounds are declared.
**Today:** ❌ for 1, 2, 3 and 5; ✅ for 4; 🟡 for 6
**Evidence:** 1 fails. `compile.go:353-354` passes the client's `limit` through
unchanged. The only cap is `sqlrepo.MaxLimit`, and `crud/sqlrepo/blueprint.go:53`
says "Zero disables the cap" — zero is the default, and `crud/options.go:251-252`
only clamps `if maxLimit > 0`. So a stock mount honours `?limit=1000000000`: one
statement, every row, into memory. The package's own hostile suite never sends a
large limit — `hostile_test.go:299`
`TestTheDefaultBudgetsBoundAnUnconfiguredEndpoint` covers depth, conditions and
preloads only. 2 fails and is not bounded anywhere: `compile.go:351` appends
`crud.Page(r.Page)` unchanged, `crud/options.go:241-259` bounds `limit` only, and
the module page already concedes of `?page=5000` that "nothing helps; `OFFSET`
is O(n) on every engine" (`docs/modules/en/query.md:220`). `?page=10000000` is one
URL edit away from the link the dashboard already publishes. 3 fails:
`?distinct=1` is parsed at `querystring.go:199`, compiled at `compile.go:397-399`,
and gated by nothing — there is no `AllowDistinct` field. It forces a sort or a
hash over the whole result set, and on a paginated read the total becomes a
derived table over the same set (`crud/sqlrepo/repository.go:561-577`). Under
`DISTINCT` the projection also stops adding the primary key and the preload join
columns (`repository.go:436-448`), so `?distinct=1&select=title` hands back rows
the client cannot address. 4 **holds, with zero configuration**:
`crud/sqlrepo/repository.go:540-556` (`sortOf`) appends `crud.Asc(PK)` to every
paginated read whose sort does not already name the key, and `blueprint.go:176`
sets `stableSort: true` by default; `sqlrepo.UnstablePagination()`
(`blueprint.go:61-63`) is the opt-out. 5 fails: `{"sort":[{"field":"publishedAt",
"desc":true,"nulls":"last"}]}` is validated and set at `compile.go:302-310`, and
`crud/predicate.go:597-603` emits the clause **only when the dialect is
postgres**. On MySQL the request is accepted with a 200 and ordered differently,
and the caveat is written where no consumer reads it, in the `Order` method doc
(`crud/predicate.go:524-531`). `compile_test.go:149` `TestSortNullsPlacement`
asserts the PostgreSQL rendering and nothing asserts the MySQL one. 6 fails for
1, 2 and 5: `blueprint.go` is a different file with a different lifetime from the
allow-lists, and 2, 3 and 5 have no home at all.
**If not ready:** `sqlrepo.MaxLimit(100)` in the `Define`, which every runnable
example does (`_examples/sql-nethttp/main.go:58`) and no doc requires. [[D-060]]
closed exactly this hole for `unpaged` and argued it in one sentence — "two open
defaults that only protect in combination protect nothing" — while `limit` is
the same request spelled as a number and is still open. For 2 and 3 there is
nothing to write short of a `WithScope` that rewrites the request, which puts
paging arithmetic in a transport. For 5, never offer `nulls` to a client on a
MySQL deployment. UC-002 guarantee 17 and its Gap 6 both believe this is
narrower than it is; `docs/ai/usecases/Index.md` gap 20 repeats the same framing.
All three need correcting.

### H-QUERY-08 — Infinite scroll that does not repeat rows
**Who:** a mobile API developer
**Wants:** cursor paging from the same endpoint, from the query string
**Story:** The first page comes back with a token. The client sends it as
`?after=…&sort=-updatedAt&limit=20` and keeps going, while editors are saving
rows behind them.
**Must hold:**
1. Scrolling to the end shows every matching row exactly once — none repeated,
   none skipped, while rows are being inserted.
2. The same is true while rows the sort orders by are being *edited*.
3. `after` and `before` work from both doors and replace offset paging rather
   than adding to it.
4. A token made for one sort is not silently compared against another.
5. A cursor cannot reach a column the endpoint declined to expose to filtering.
**Today:** 🟡 partial — 3, 4 and 5 hold; 1 holds only on a repository that kept
its tiebreaker; 2 is unpinned and probably false
**Evidence:** 3 is `compile.go:381-386` for the options and
`crud/sqlrepo/repository.go:166-169` for the zeroed offset. 4 is
`test/integration/cursor_test.go:238` `TestACursorIsRefusedUnderADifferentSort`
([[D-028]] for why the token carries its own field names). 5 is the good one:
`compile.go:373-380` checks the sort paths against `Filterable` because a cursor
*is* a filter, pinned by `compile_test.go:658`
`TestACursorCannotCompareAColumnTheEndpointHidesFromFiltering`. 1 is pinned by
`test/integration/cursor_test.go:45`
`TestACursorWalkIsNotDisturbedByAConcurrentInsert`, and it depends on a
precondition nothing in this module states:
`crud/sqlrepo/repository.go:400-412` refuses a cursor whose effective sort names
no primary key, and `sqlrepo.UnstablePagination()` removes the implicit
tiebreaker "and with it the ability to page by cursor"
(`repository.go:397-398`). So a repository defined with `UnstablePagination()`
turns every cursor request into a `crud.SchemaError` — which
`port/kind.go:179-180` renders as a 400 with code `bad_query`, blaming the
client for the server's declaration. 2 has **no test at all**: the only
concurrency test's only mutation is `rows.Save(ctx, &EgRow{ID: 99, ...})`
(`cursor_test.go:73`), a new row, and `grep -n Update test/integration/cursor_test.go`
finds nothing. The tiebreaker fixes insert-shift, not sort-key mutation — a row
whose `updatedAt` is written while the reader is mid-walk moves across the
cursor position and is shown twice or not at all. `-updatedAt` is the default
sort of every admin list this file is about. One footnote on 5: the check runs
over the client's declared sort, while the repository hands `cursorWhere` the
effective sort with `Asc(PK)` appended (`repository.go:339-341`, `:540-556`), so
a cursor also compares the primary key on an endpoint whose `Filterable` never
named it. Small, because the key is addressable through `GET /{id}` anyway.
**If not ready:** Do not use `UnstablePagination()` on a repository whose
endpoints offer cursors, and there is nothing at declaration that says so. For 2,
cursor by a column nothing edits — the primary key or `createdAt` — and treat
"exactly once" as a promise about insert-shift only until a test says otherwise.

### H-QUERY-09 — Saved views
**Who:** an internal-tools engineer whose users save their filters
**Wants:** store what the user built, validate it when they save it, replay it
later
**Story:** The user assembles a filter in the UI. The app stores the document in
a column. Next week the view runs again from a scheduled job, on a route the
user is not sitting in front of.
**Must hold:**
1. The document round-trips through JSON without changing meaning.
2. A saved view can be validated when it is saved, against the model and the
   endpoint's bounds, without running it.
3. Replaying a saved view twice returns the same rows in the same order.
4. A view saved today still means the same thing after a refactor of the Go
   model.
**Today:** 🟡 partial — 1 holds except for a value containing a comma
**Evidence:** 1 is pinned by `compile_test.go:389`
`TestARequestSurvivesBeingWrittenBackOutAsJSON`, and the test cannot catch its
own exception: no value in its document contains a comma. `Term.Values` is a
`Strings` (`querystring.go:19-23`) and `Strings.UnmarshalJSON` splits **every**
element on commas (`crud/query/request.go:122-149`), so a saved
`{"terms":[{"path":"name","values":["Smith, John"]}]}` decodes to two values,
marshals back as two, and the scalar arm then keeps only the first
(`querystring.go:110`). The view silently becomes a search for "Smith". 2 is
`req.Compile(meta, cfg)` and throwing the options away, which works but is not
named as a use — the module page's "Using it directly" section is about serving
a request, not validating a stored one. 3 holds, and it takes two mechanisms in
two packages: keys are sorted before compiling (`crud/query/filter.go:32-36`,
[[D-014]]), pinned by `edge_test.go:445`
`TestTheSameDocumentAlwaysCompilesToTheSameStatement`, and the primary-key
tiebreaker of H-QUERY-07 is what makes "the same order" true of the rows rather
than only of the SQL. 4 is narrower than it looks. There *is* an alias layer:
`crud.TagKey` is `"db"` (`crud/meta.go:39`), a field's `Column` is that tag's
name when present (`crud/meta.go:395-398`), and `Schema.Field` resolves `byCol`
before the fold (`crud/meta.go:117-127`), so a field tagged `db:"views"` pins the
wire name and renaming the Go field does not invalidate a view naming `views`.
What is left: that alias is shared with the physical column, so a column rename
is still a breaking API change, and the response body is rendered through `json`
tags — a third vocabulary the filter door does not accept.
**If not ready:** Validate on save by compiling and discarding. Store filters in
the nested `filter` object, never in `terms`, because only the nested form
survives a comma. For 4, name every column with a `db` tag and treat the tag as
the public wire name.

### H-QUERY-10 — A 400 the UI can act on
**Who:** the front-end developer receiving the refusal
**Wants:** to highlight the filter chip that was wrong
**Story:** The client sends a filter on a column the endpoint does not expose.
They get a 400 and want to know which part of what they sent caused it, in a
field, not in a sentence they have to parse.
**Must hold:**
1. A field the model does not have is a refusal naming the path, not a dropped
   clause — in every name position the language has.
2. The path arrives structured and identifies **which** chip, not only which
   parameter.
3. Nothing internal — a Go type, a package path, a driver message — is in the
   body.
4. A refusal produces no options at all; the transport cannot run "the good
   half".
5. A client can tell "no such field" from "not filterable" from "too many
   conditions" without reading English.
**Today:** 🟡 partial — 1 and 4 hold; 2 holds on the nested filter and not on the
door the chips actually use; 3 has a recorded carve-out; 5 does not hold
**Evidence:** 1 and 4: `edge_test.go:192`
`TestEveryRejectionNamesThePathThatWasWrong` walks eleven positions;
`crud/query/query_test.go:368` `TestUnknownFieldNeverReachesTheDatabase`;
`hostile_test.go:446` `TestARejectedDocumentCompilesToNoOptions`;
`port/kind.go:172-173` turns a `*query.Error` into a fault with
`errs.ParsePath(qe.Path)`. 2 is where round 1 was wrong. The same cited test's
last row is `{"a flat term", {"terms":[{"path":"nope",...}]}, "filter"}`
(`edge_test.go:205`) — the bare string `filter`, with the offending field only
inside the prose. `querystring.go:49` calls `c.path(t.Path, "filter")` and `:54`
returns `errf("filter", "%s is not filterable", canonical)`. The rows at
`edge_test.go:198-203` pin `"sort"`, `"select"`, `"preload"` and `"searchFields"`
with no index, so `sort=["createdAt","nope"]` says `sort` and the UI cannot say
which term. Only the nested `filter` object produces a path a chip can be found
by — and `querystring.go:62,82,108,140` show the *operator* errors do use
`"filter."+canonical`, so the coarse path is on exactly the two refusals a UI
most needs. 3 holds for *values* — `coerce.go:159-190` `wanted()` says "a whole
number", never `crud.Opt[int64]` ([[D-044]]) — and does not hold for the model.
`Schema.Name` is `reflect.Type.Name()` (`crud/meta.go:238`),
`UnknownFieldError` carries it (`crud/errors.go:41-48`), and `cleanErr` folds
that message into the refusal a client reads (`compile.go:560`, `:631`), so
`{"filter":{"nope":1}}` answers with the Go struct name `Article`. UC-002 records
this under **Out of scope** — "a client can enumerate the schema through error
text" — so it is decided, not overlooked. 5 fails: every query refusal carries
one code, `errs.CodeBadQuery`. The finer code exists and the query door never
reaches it — `port/kind.go:175-177` emits `errs.CodeUnknownField`, but the
`*query.Error` arm at `:172-173` runs first, deliberately, because it carries the
path.
**If not ready:** For 2, a front end that wants chip-level errors must send the
nested `filter` object (through `?filter=` on a GET) and never `f=` terms. For 5,
a client parses message text. **No decision covers a client parsing this
library's own message text** — [[D-039]] is about *driver* text this library
reads, and [[D-044]] is about what must not leave. That absence is itself worth
recording. Closing it means `query.Error` carrying a reason kind, not a new arm
in `port/kind.go`.

### H-QUERY-11 — The export endpoint a job fleet reads
**Who:** whoever owns the nightly sync
**Wants:** one route that really does serve every matching row
**Story:** A worker needs the whole table, filtered. They declare
`AllowUnpaged: true` on that endpoint only, and the worker sends
`{"unpaged": true, "skipTotal": true}`.
**Must hold:**
1. An endpoint that has not declared it refuses `unpaged` by name, with a 400.
2. An endpoint that has declared it returns every matching row.
3. If something upstream is going to cut the result short, the caller finds out.
**Today:** 🟡 partial
**Evidence:** 1 and 2 hold: `compile.go:387-393`, pinned by
`compile_test.go:461` `TestUnpagedIsRefusedUnlessTheEndpointServesIt` with the
control that a declaring endpoint really gets the option. 3 does not:
`crud/options.go:242-247` clamps an unpaged read down to `MaxLimit` when one is
set, silently, and an unpaged read never reaches the `limit` clamp at all — it
takes its own branch. So the two pieces of advice a consumer is given — cap the
page with `sqlrepo.MaxLimit` (H-QUERY-07), declare `AllowUnpaged` for the export
— combine into an export that stops at the cap and says nothing. [[D-060]]
records the cost; nothing warns at declaration. The same mechanism seen from the
calling side is the remote sweep's blocker 1.
**If not ready:** Bind a second repository with no `MaxLimit` for the export
route (H-QUERY-21), or page the worker with cursors. Both are more than the
consumer thinks they are buying.

### H-QUERY-12 — The link-shareable dashboard URL
**Who:** an analyst pasting a filtered list into Slack
**Wants:** the whole question in a `GET` URL
**Story:** The dashboard puts its state in the query string: repeated `f=`
terms, a sort, a page, and its own `?includeArchived=1` that a scope reads.
Somebody edits the URL by hand.
**Must hold:**
1. A parameter that is one edit away from a real one is refused rather than
   ignored — `?filtr=` must not answer 200 with the whole table.
2. The application's own parameters still pass through, on both doors.
3. The same logical input binds the same Go value through either door.
4. Anything the language can express fits in a URL.
**Today:** 🟡 partial — 1 holds; 2 holds on one door of two; 3 breaks in five
places; 4 is false in two whole features
**Evidence:** 1 holds and is well made: `crud/query/querystring.go:254-266`
`checkParams` with the transposition case spelled out, pinned by
`crud/query/strict_test.go:108` `TestAMisspelledQueryParameterIsRefused`. 2
holds on the query string — `strict_test.go:121`
`TestAnApplicationsOwnParametersArePassedThrough` is the control, and its own
comment says why: "an application's own parameters have to survive, or WithScope
stops working" — and **fails on the JSON door**. `crud/query/request.go:89` sets
`dec.DisallowUnknownFields()` and `crud/http/crudnet/handler.go:443-448` decodes
the body straight onto `&query.Request{}`, so
`{"filter": {...}, "includeArchived": true}` is a 400.

3 breaks in **five** places, and three of them are in the shared `terms`
compiler that serves **both** doors rather than the query string alone. UC-002
Gap 3 and `docs/ai/usecases/Index.md` gap 19 both enumerate four and both need a
fifth row:
- `querystring.go:69-73` — an `isNull` whose value is not a Go boolean discards
  the parse error and falls to `false`, so `?f=deletedAt:isNull:yes` compiles to
  `IS NOT NULL`. The nested door refuses it (`filter.go:213-215`).
- `querystring.go:110` — a scalar term keeps `Values[:1]` after `splitList`
  already split on commas.
- `querystring.go:80-84` — **the fifth, and this file's own finding**: the
  textual arm takes `Values[0]` with no count check at all, so
  `?f=title:contains:go,rust` searches for "go" alone and nothing is refused.
- byte-slice columns decode base64 from JSON and raw text from the query string.
- a `gt` against `null` binds a nil rather than being refused like every other
  null operand.

There is a sixth divergence that is about the *absence* of a spelling rather
than a disagreement: `{"filter":{"title":""}}` binds the empty string, and
`?f=title:eq:` is a 400 (`splitList` returns nil for a blank value,
`request.go:329-334`, and the scalar arm answers `"eq needs a value"`,
`querystring.go:106-109`, pinned at `edge_test.go:362`). A filter UI that always
emits its chips and blanks the value gets a 400 on every reset.

4 is false in two features, both recorded by UC-002 under **Out of scope** so
they are decided rather than broken: `parseSortList` (`request.go:215-231`) never
sets `Nulls`, and `pathsToPreloads` (`request.go:296-304`) builds a bare path, so
null placement and a per-relation filter and sort cannot be written on a query
string at all.
**If not ready:** `?filter={json}` is the escape hatch and this file's round 1
did not name it. `querystring.go:225-226` reads the whole `filter` parameter as a
raw document, so `or`, `not`, per-column operator maps, a value containing a
comma and an empty string all fit in a URL through that one parameter — and it
bypasses `splitList` entirely, which makes it the real answer to the comma and
`isNull` findings for a query-string client. `docs/modules/en/query.md:90,94`
documents it. Nulls and per-preload narrowing still do not fit. For 2, put the
application's flag in a header or the path when the client posts a document.

### H-QUERY-13 — Load the children without loading the whole child table
**Who:** whoever added `?preload=comments` to the articles list
**Wants:** the comments shown under each article on the page, and a bound on how
many rows that is
**Story:** The list serves 100 articles a page. A few of them are popular. One
request preloads `comments` and the process holds every comment row belonging to
all hundred.
**Must hold:**
1. A preload has a row ceiling, and a stranger cannot raise it.
2. The endpoint's declaration says what that ceiling is, next to the relations it
   permits.
**Today:** ❌ missing, and there is no knob anywhere
**Evidence:** `MaxPreloads: 4` bounds how many relations, never how many rows.
`query.Preload` carries only `Path`, `Filter` and `Sort`
(`crud/query/request.go:233-238`), and the layer below **refuses** a limit
outright: `crud/preload.go:193-196` returns a `SchemaError` for `Limit`, `Page`,
`Offset` or `Unpaged` on a preload, "a preload cannot be paginated; it is loaded
for every parent at once". That is a correct statement about the batch strategy
([[D-006]]) and it is also the reason no ceiling can exist today. The module
page's own cost table stops at "a preload is one statement per relation per
level" (`docs/modules/en/query.md:197-198`) and never counts rows.
**If not ready:** There is no ceiling, but round 1 was wrong to say there is no
mitigation. `sqlrepo.RelationScope("Comments", crud.IsNull("DeletedAt"))`
narrows the far side of exactly this preload, validated against the model at
declaration (`crud/sqlrepo/blueprint.go:115-132`) — "only approved", "only the
last thirty days". It bounds the *predicate*, not the row count, so a popular
article still brings back everything that matches; it is the difference between
an unbounded read and a smaller unbounded read. The other option is removing the
relation from `Preloadable`, which removes the feature. This is the module's
remit verbatim — "bounded so an untrusted client cannot spend the server".

### H-QUERY-14 — The date range on the same screen
**Who:** the same product engineer, wiring the date picker next to the search box
**Wants:** `?f=createdAt:gte:2026-01-02&f=createdAt:lte:2026-01-02` to mean
"things from the second", for a user in Melbourne
**Story:** The picker sends two bare dates. The rows near midnight are wrong and
nobody notices for a month.
**Must hold:**
1. A bare `2026-01-02` has a documented meaning, and the endpoint's consumers
   are told what it is.
2. An offset-bearing RFC3339 value is honoured as sent.
3. A `lte` against a bare date either covers that whole day or is documented as
   covering only its first instant.
**Today:** 🟡 partial — 2 holds, 1 and 3 are undocumented behaviour
**Evidence:** `crud/query/coerce.go:58-64` lists five accepted layouts:
`time.RFC3339Nano`, `time.RFC3339`, `2006-01-02T15:04:05`, `2006-01-02 15:04:05`
and `2006-01-02`. `parseTime` calls `time.Parse` with no location
(`coerce.go:66-73`), so the last three are **UTC**. For a user in UTC+10 "today"
is off by ten hours, and a `lte` against a bare date is midnight, so the last
day of the range is excluded entirely rather than included. 2 holds because
RFC3339 carries its own offset, pinned by `edge_test.go:296`
`TestTimestampZonesSurviveCoercion`. Nothing in `docs/modules/en/query.md` states
any of this, and no test names a zone other than UTC.
**If not ready:** Make the front end send full RFC3339 with an offset and never
a bare date. Closing it is a documented rule — bare dates are UTC instants, and
an end-exclusive upper bound is the caller's job — plus one sentence in the
module page. A `WithScope` that rewrites the bound per tenant is the heavier
answer and puts date arithmetic in a transport.

### H-QUERY-15 — The detail screen next to the list
**Who:** the same front-end developer, one route over
**Wants:** `GET /articles/42?preload=author&select=id,title` — the same
vocabulary on the single-resource route
**Story:** The list screen works. The detail page wants the author loaded and a
narrower projection, and sends the parameters it already knows.
**Must hold:**
1. `preload` and `select` are honoured on a `GET /{id}`.
2. Everything meaningless there — `filter`, `sort`, `page`, `limit` — is dropped
   rather than obeyed.
3. Whether a dropped key is a refusal or silence is the same answer the list
   route gives for a name it will not accept.
**Today:** 🟡 partial — 1 and 2 hold, 3 does not
**Evidence:** `port.NarrowForEntity` (`port/request.go:38-41`) zeroes `Filter`,
`Terms`, `Search`, `Sort`, `Page`, `Limit` and `Offset` and keeps the shaping
options, and `port.NarrowForCount` (`:33-36`) does the equivalent for a total.
Both are exported, which is the tell: a hand-written service has to remember to
call them. 3 is the gap — the narrowing is **silent**, while the same document's
unknown *key* is refused by name (`crud/query/request.go:79-100`) and its unknown
*field* is refused with a path. So `GET /articles/42?limit=5` answers 200 having
ignored the parameter, and `GET /articles/42?limi=5` answers 400.
**If not ready:** Nothing to write; know that the entity route ignores the
paging half of the vocabulary. Closing it means deciding whether a meaningless
parameter on an entity route is worth a refusal, which is the same question
`checkParams` already answered "yes" to for a near-miss.

### H-QUERY-16 — Finding out which client query cost the database
**Who:** the on-call engineer at 2am
**Wants:** to get from "this endpoint's p99 is eight seconds" back to the filter
document that caused it
**Story:** The list endpoint slows down. They have a request id, a trace, and a
statement in `pg_stat_statements` with every literal bound out. They want to
know what the client asked for.
**Must hold:**
1. What a request resolved to — the paths it named, how many conditions it
   spent, which relations it preloaded — is reachable for a log line or a span.
2. It is reachable without the application re-parsing the request itself.
**Today:** ❌ missing
**Evidence:** nothing found for this. `crud/query` has no logging, no hook, no
context write — `grep -rn 'Logger\|trace' crud/query/*.go` is empty, and
`Compile` returns `[]crud.Option` and an error and nothing else. `port.Logger`
is the library's seam ([[D-062]]) and this package does not reach it. The module
page states the problem itself and stops there: "A consumer cannot measure this
from outside, and the number is the dominant cost of using this library"
(`docs/modules/en/query.md:176-177`), and "The query DSL bounds what a request
may *name*. It does not bound what that request costs" (`:212-213`).
**If not ready:** Log the raw query string or body at the handler and correlate
by hand, which loses the canonical paths and the condition count — the two
things that say *why* it was expensive. Closing it is one optional callback on
`Compile`, or a resolved summary written to the context, and it is the thing an
experienced buyer asks about before adopting.

### H-QUERY-17 — The user clears the status dropdown
**Who:** every filter UI ever written, on its default code path
**Wants:** clearing a chip to mean "no filter on this column"
**Story:** The screen has a multi-select on status. The user ticks two values,
then unticks both. The front end keeps emitting the parameter with nothing in
it: `?f=status:notIn:`. Or the other one: `?f=status:in:`.
**Must hold:**
1. An accepted filter never widens the result.
2. If the endpoint cannot make sense of what a chip sent, the client is told,
   the way it is told about every other unusable value.
**Today:** ❌ missing — this is the sweep's worst finding
**Evidence:** `ParseTerm` calls `splitList` on the value
(`querystring.go:32,37`), and `splitList` returns nil for a blank string
(`request.go:329-334`). The multi arm then passes an empty slice through
`countValues(0)` — which only checks the ceiling — and `coerceAll`
(`querystring.go:86-104`), and `buildMulti` calls `crud.NotIn(field)` with no
values (`filter.go:294-295`). `inNode.render` degrades that to the constant
`1 = 1` (`crud/predicate.go:201-208`). So a cleared multi-select answers 200 with
**every row in the table**, from a correct config, on a query string a stranger
sends. The mirror is worse to debug and better to survive: `?f=status:in:`
renders `1 = 0`, the screen goes blank, and nothing was refused, so the developer
cannot tell an empty result from a broken filter. Both are true on the JSON door
too — `{"filter":{"status":{"notIn":[]}}}` goes through `decodeList`
(`coerce.go:38-56`) to the same place. `edge_test.go:118`
`TestAnEmptyValueListBecomesAConstant` pins all four spellings **as intended
behaviour**, and its comment argues the case: `IN ()` is a syntax error
everywhere, so it degrades to a constant. That is a true statement about SQL and
the wrong answer for a request. Contrast the scalar arm one branch over:
`?f=title:eq:` with the same empty value is a 400 (`querystring.go:106-109`,
`edge_test.go:362`). UC-002 records this as Gap 4 and this file's round 1 skipped
it entirely.
**If not ready:** Nothing at the endpoint. The front end must omit the parameter
rather than send it empty — on every chip, in every client, forever, with no
refusal anywhere to catch the one that forgets. The fix is one decision and one
line: an empty list on a multi-value operator is a refusal at the path, not a
constant, exactly as an empty value on a scalar operator already is. That
changes a pinned test, which is why it is a decision and not a patch.

### H-QUERY-18 — The front end that builds the filter chips
**Who:** the developer writing the list component, not the endpoint
**Wants:** to render the filter and sort controls from what the endpoint accepts
**Story:** They have twenty resources. They do not want to hand-write twenty
filter bars, and they do not want the bar to offer a column the endpoint refuses.
They ask for a `/schema` route or an `OPTIONS` response.
**Must hold:**
1. What an endpoint accepts is readable from the same declaration that bounds it.
2. What an *empty* list resolves to is answerable, since that is the default.
3. The answer is in the client's vocabulary — the names the filter door accepts.
**Today:** ❌ missing, and round 1 got the reason wrong
**Evidence:** the lists are plain exported fields (`compile.go:69-81`), so a
hand-written `/schema` route reads `cfg.Filterable` directly and re-declares
nothing — round 1 claimed there was no accessor and that was false. What is
genuinely unreachable is everything that makes the lists *informative*: an empty
list means "everything the model maps" (`compile.go:212-215`) and there is no way
to ask what that expands to; the entries are raw strings never canonicalised
against the model, so `"createdAt"`, `"CreatedAt"` and `"created_at"` are three
strings for one column; the effective caps behind a zero field are unexported
(`compile.go:95-128`); and which operators a column's Go type actually supports
is knowable only by trying. `Check` already walks the lists against the model on
its way to refusing (`compile.go:152-198`) and throws the resolution away.
**If not ready:** Hard-code a second copy of the allow-list in TypeScript. The
drift is invisible in the direction that matters: a column removed from
`Filterable` shows up as a chip that 400s, and nothing fails at build time on
either side.

### H-QUERY-19 — The model whose columns are not plain Go scalars
**Who:** the first real adopter, on a service with a `uuid` key
**Wants:** `?f=id:in:<uuid>,<uuid>`, `?f=status:eq:archived` on a validating
enum, `?f=price:gte:10.50` on a decimal
**Story:** They wire the endpoint against their real model. The key is a
`uuid.UUID`, `Status` is a string type with an `UnmarshalText` that rejects
unknown values, and `Price` is a decimal with its own JSON decoding. Someone
sends `?f=status:eq:archvied`.
**Must hold:**
1. A column type with its own parser keeps its own rules on both doors.
2. A value that type rejects is a 400 naming the field, not a 500.
3. The refusal message does not name the Go type.
**Today:** ✅ ready — the one place in this sweep where the answer is better than
a consumer expects
**Evidence:** `coerceString` tries the type's own `TextUnmarshaler` first
(`crud/query/coerce.go:85-98`) with `time.Time` deliberately checked before it,
because `time`'s own `UnmarshalText` takes RFC3339 and nothing else and would
reject the date-only forms the JSON door accepts. The JSON door reaches the same
parser through `encoding/json`, which calls `UnmarshalText` for a JSON string
(`coerce.go:19-34`). Both are pinned together by `coerce_test.go:106`
`TestBothDoorsBindTheSameValue`, whose `code` fixture (`coerce_test.go:19-31`) is
a type that upper-cases and refuses blanks — "a uuid, an enum or a money column
would lose its own rules the moment its value arrives as text". 2 is
`coerce_test.go:286` `TestBadValuesAreRejectedByBothDoors` and
`edge_test.go:249` `TestUncoercibleValuesAreRejectedNotZeroed` — rejected, never
zeroed, which is the failure that makes a filter match the wrong rows. 3 is
`wanted()` (`coerce.go:159-190`), which answers "a value in this field's own
format" for any type with its own decoder rather than printing the Go type
([[D-044]]).
**If not ready:** — . Two things to know rather than fix: a uuid list on the flat
door is split on commas, which is fine because uuids contain none but is the same
mechanism that breaks a name (H-QUERY-09); and whether `gte` on a money column
compares numerically is decided by the physical column type, not by this package.

### H-QUERY-20 — The attributes that live in a JSON column
**Who:** anyone whose product has per-tenant custom fields
**Wants:** `?f=metadata.plan:eq:pro` against a `jsonb` column
**Story:** Half the filterable attributes on the model are not columns. They are
keys in one `metadata` JSON column, because the set of them changes per customer.
They look for the way to expose one of those keys to the DSL.
**Must hold:**
1. A consumer can expose a computed or nested value to the filter vocabulary
   without giving up the allow-list.
**Today:** ❌ missing, and there is no partial answer
**Evidence:** every name position resolves through `crud.Meta.FieldAt`
(`compile.go:550-563`), which resolves struct fields and relations and nothing
else — a dotted path is a relation hop, not a JSON path. There is no virtual-field
hook and no way for a `Config` to declare a name that is not a mapped column.
`crud.Raw` exists (`crud/predicate.go:477-480`) and is explicit that "column names
are NOT resolved or quoted here — that is the caller's job", so it is reachable
only from server-side options, and parameterising it from a client would be the
injection point this whole package exists to close. Nothing in
`docs/modules/en/query.md` mentions JSON columns.
**If not ready:** A hand-written endpoint that reads its own parameters and
builds `crud.Raw`, which loses the allow-list, the path-carrying refusals and the
budgets in one step — for the half of the model that needed them most. This is
worth saying out loud before a tag, because it is discovered in month two and
the answer is "that resource does not use this library".

### H-QUERY-21 — Two endpoints over one repository
**Who:** the team that has a public list and an internal export of the same rows
**Wants:** one `Define`, one model, two routes with two ceilings
**Story:** `/articles` is public and must never serve more than 100 rows.
`/internal/articles` is read by a job and must serve every row, sorted
differently. They are the same table and the same repository.
**Must hold:**
1. Two endpoints over one repository can have two page ceilings.
2. They can have two default sort orders.
3. One of them can serve whole result sets and the other cannot.
**Today:** ❌ for 1 and 2, ✅ for 3
**Evidence:** 3 is the only one this module owns and it works —
`AllowUnpaged` is a `query.Config` field, so two configs give two answers
(`compile.go:387-393`). 1 and 2 are `sqlrepo` settings on the `Define`:
`MaxLimit` (`blueprint.go:53-54`), `DefaultLimit` (`:50-51`) and `DefaultSort`
(`:56-59`) are properties of the repository, and the repository is what both
routes bind. So the export route needs a **second `sqlrepo.Define` over the same
table** to differ in either, and then H-QUERY-11's silent truncation is the thing
that made it necessary. This is the concrete case behind the `MaxPageSize`
proposal below, and behind H-QUERY-06: every bound that a consumer thinks of as
"this endpoint's" and that lives on the repository has this shape.
**If not ready:** A second `Define` and a second `Bind` per route that needs a
different bound — two objects that must be kept in step by hand, over one table,
with nothing checking that they agree about anything else.

### H-QUERY-22 — The rest of the list screen
**Who:** the same product engineer, finishing the page
**Wants:** the "42 draft · 17 published" counts above the table, and the
"archive everything matching this filter" button beside it
**Story:** The list works. Two things are still missing from the design, and
both reuse the filter the user already built.
**Must hold:**
1. A validated filter document can drive a grouped count, not only a page and a
   total.
2. A validated filter document can drive a bulk write.
**Today:** ❌ missing, both
**Evidence:** `Compile` returns `[]crud.Option` and the only consumers are
`DefaultService.List`, `Count` and `Get` (`port/service.go:108`, `:123`, `:139`)
— three reads. There is no grouping in `crud.Options` and no `GROUP BY` anywhere
in the DSL, so the counts are one request per option or hand-written SQL. Bulk
delete takes a list of ids capped at `port.DefaultMaxBulk` = 1024
(`port/rules.go:53-65`), and there is no filtered `DeleteAll` or `UpdateAll`
command in `port` at all (`grep -n "DeleteAll\|UpdateAll" port/*.go` is empty), so
"archive everything matching this filter" means paginating ids into a bulk
delete and racing the writers.
**If not ready:** One request per dropdown option for the counts, and a paginated
id harvest for the bulk action. Both give up the allow-list the consumer just
spent H-QUERY-02 declaring, which is the cost worth naming: the vocabulary bounds
three routes, and a list screen has five.

## The DX this should have

### The call site

```go
var Articles = sqlrepo.Define[Article, int64, ArticleUpdate]("articles")

var articleQuery = &query.Config{
    Filterable: []string{"Title", "Views", "CreatedAt", "Author.*"},
    Sortable:   []string{"CreatedAt", "Views"},
    Searchable: []string{"Title", "Body"},

    MaxPageSize:    100,  // does not exist here; default 100, not 0
    MaxOffsetRows: 10000, // does not exist anywhere
    MaxPreloadRows:  500, // does not exist anywhere
    // AllowDistinct defaults false, like AllowUnpaged
}

repo := Articles.Bind(crudsql.Postgres(db))
crudnet.New(repo,
    crudnet.WithQuery[Article, int64, ArticleUpdate](articleQuery),
).Mount(mux, "/articles")
// Every config handed to WithQuery is resolved against the model at mount
// and a misspelled entry panics there.
```

**Count it honestly.** That is 14 lines. The same endpoint today, with the page
ceiling and the check wired, is about 12: `sqlrepo.Define("articles",
sqlrepo.MaxLimit(100))` is the same line with one argument, the `Config` literal
loses three fields, and `func init() { articleQuery.MustCheck(Articles.Meta()) }`
adds one. The concepts a newcomer holds are unchanged: four packages, three type
parameters, five lists, a handful of caps. **The win is not brevity.** It is one
fewer file for the bound most likely to be wrong, and three bounds that do not
exist anywhere today at any line count.

### Turning one knob

```go
var adminQuery = articleQuery.Plus(   // Plus does not exist
    query.AlsoFilterable("Email"),
    query.Unpaged(),
)

port.WithQueryFor(
    articleQuery,                                  // the default, positional
    map[string]*query.Config{"admin": adminQuery}, // never contains ""
    func(ctx context.Context) string {
        if p, ok := auth.PrincipalFrom(ctx); ok && p.Has("articles:admin") {
            return "admin"
        }
        return ""
    },
)
```

**The delta cannot be a struct literal, and that is a finding about the struct.**
Every field's zero value already means something: an empty list means "allow
everything", `MaxDepth: 0` means "the default 6", `AllowUnpaged: false` cannot
turn a base's `true` off. A `With(query.Config{...})` over this shape can only
ever widen, and cannot tell "not mentioned" from "set to the zero value" for
exactly the fields a delta is for. Round 1 proposed `.With` and showed an example
that re-listed all four base columns while the prose beside it called it a delta
— two reviewers caught it and both were right. Functional options are the boring
construct that can express absence and removal; the base stays a literal.

**Every key resolves at mount, and a key that does not is a refusal.** The
default config is a positional argument, so a selector returning a name the map
does not hold cannot fall through to a nil `*query.Config` — which is the
*widest* configuration this package has, since empty lists allow everything. A
selector that fails open is the exact failure the mount-time check exists to
close, arriving one layer up.

### Why this shape

The struct literal stays a struct literal. A fluent builder would read better in
one example and worse in every diff, and this repository prefers the boring
construct where the magic is not load-bearing ([[D-021]] concentrates it
elsewhere). What the struct is missing is not syntax, it is the bounds that
decide *volume*: rows returned, rows scanned, rows preloaded, and whether the
client may ask for a sort-or-hash over the whole set. An endpoint is reviewed by
reading its bounds; if the bounds that matter are in another file or absent, the
review passes and the endpoint is not bounded.

**`MaxPageSize` must default non-zero, and that is the part round 1 got wrong.**
Round 1 wrote "the zero value means inherit, never unbounded" and then had it
inherit `sqlrepo.MaxLimit`, which is itself zero and documented as "Zero disables
the cap" (`blueprint.go:53`). Both zero is the stock mount, so the proposal
closed nothing and breached [[D-060]]'s invariant in the process — "Every bound
on how much comes back is closed by default". So: `MaxPageSize` defaults to 100
the way `MaxInValues` defaults to 1024, and `MaxPageSize: -1` is the explicit
spelling for "no ceiling". A bound whose safe value has to be typed is not a
bound.

**It refuses where the repository clamps, and adopting it is a behaviour
change.** Every other cap in `query.Config` refuses with a `query.Error` at a
path; `sqlrepo.MaxLimit` clamps silently (`crud/options.go:251-252`), which is
H-QUERY-11's sharp edge. Two caps with opposite behaviour is how a knob becomes a
trap, so the effective ceiling is the minimum of the two and **the endpoint
refuses at it**. With `MaxPageSize: 100` and `MaxLimit(50)`, `?limit=80` stops
being 50 rows and becomes a 400 — that is a break for every client relying on the
clamp, and it belongs in the release note rather than in a footnote. The
interaction with `unpaged` must be stated too, because an unpaged read never
reaches the limit clamp at all (`crud/options.go:242-247`): `AllowUnpaged: true`
means `MaxPageSize` does not apply, and an endpoint that wants both bounded says
so with `MaxOffsetRows`. `DefaultLimit` is deliberately **not** proposed —
`sqlrepo.DefaultLimit` already defaults to 20 and applies when a client names
none, so adding it here would make three places a page size is decided for no
case in this sweep.

**`MaxOffsetRows` and `AllowDistinct` are the two bounds nobody has proposed
anywhere.** `MaxPageSize` caps rows *returned* and does nothing about rows
*scanned*: `?page=10000000&limit=100` still walks to the offset, and the module
page already concedes nothing helps there. A deep page should refuse at path
`page` with a message pointing at `after`/`before`, which is the answer
H-QUERY-08 already gives. `AllowDistinct` gets the argument `AllowUnpaged`'s own
doc comment already makes (`compile.go:53-67`): this bounds how much work
happens rather than what may be named, so the dangerous direction is the one
that has to be named.

**`MaxPreloadRows` needs a mechanism, and round 1 forbade the only one.** Round 1
wrote that it "must be a *refusal* when the batch would exceed it, not a `LIMIT`
on the child statement". As specified that is not implementable: [[D-006]]'s
invariant is one statement per relation per level, which rules out a preflight
`COUNT(*)`, and a refusal computed after the batch returned has already
materialised the rows the ceiling exists to bound. The distinction the sentence
missed: a `LIMIT` that *truncates* a child list is what D-006 and
`crud/preload.go:193-196` forbid; a `LIMIT n+1` read as a **tripwire** and turned
into a refusal is not — it keeps the statement count and stops before the rows
land. Concretely: `query` emits a `crud.PreloadCeiling(n)` option,
`crud/preload.go` reads it, the child statement carries `LIMIT n+1`, and the
n+1st row is a `query.Error` naming the relation. It adds no statement and no
dependency. **Its failure mode has to be stated in the same breath**: the refusal
depends on the data, so the same `?preload=comments` works today and 400s
tomorrow because one thread went viral, and the client's only retry is a smaller
page. That is worse DX than a static bound and better than an unbounded read; if
the owner disagrees, the alternative is truncating with an explicit marker in the
payload, which costs a wire-format change.

**The seam belongs in `port`, and `port.ServiceOption` carries no type
parameters** by design (`port/service.go:42-45`), so `port.WithQueryFor(...)`
spells no generics — the three-parameter tax exists only on the binding wrapper.
One seam in `port` also survives `Serving`: `crudnet.Serving` panics on a
binding-level `WithQuery` via `Rules.RefuseServiceOptions`
(`port/rules.go:81-97`), so a binding-only option gives nothing to an application
mounting a service somebody else assembled. **The effort claim has to be honest
about what that costs**: `WithQueryFor` has to be added to `port.Rules` and given
an arm in `RefuseServiceOptions`, or it is silently ignored under `Serving` —
"an ignored `WithQuery` would leave an API accepting everything while its author
believed it was bounded" is the sentence that panic exists for. And
`make check-triplets` means the binding forward and its test name land in
`crudnet`, `crudgin` and `crudfiber`, with a `port`-vocabulary version in
`crudgrpc`. One seam, four constructors, four tests.

**The mount-time `Check` is not new capability, but wiring it has a deadline.**
`Config.Check` already exists and already writes the failure mode into its own
doc comment. It needs one caller, in `port.NewService` (`port/service.go:82-96`),
which already computes `repo.Meta()` and which all four bindings route through.
Two things round 1 did not say. First, `Check` returns on the first bad entry
(`compile.go:174-186`); at twenty resources with three typos that is three
restarts, and `errors.Join` over the whole walk is a two-line change that should
land with the caller, not after it. Second, **this is a pre-tag change or a
breaking one**: wiring `MustCheck` into `NewService` turns every latent
misspelled allow-list entry into a start-up panic, which is free today and a
major-version note the day after the first tag.

**A generated field constant would beat both.** `cmd/vv` already generates a
metamodel per model. String constants beside it — `Filterable:
[]string{ArticleField.Title}` — make a renamed Go field a *compile* error rather
than a start-up one, cost no dependency, add no `query` API, and generate into
the consumer's own package. [[D-021]] ranks the two in its own invariant: "fails
at build or start-up", and build is the better half. `Check` still earns its
place for the hand-written entry and the subtree spelling; the generator is what
makes twenty resources bearable.

### What it must not break

- **[[D-060]] is challenged twice and I am naming both.** It places the page
  ceiling deliberately — "an endpoint that wants a page size says so with
  `sqlrepo.MaxLimit`" — and a `MaxPageSize` in `query.Config` contradicts that
  placement. Its invariant also says every bound on how much comes back is closed
  by default, which a zero-means-unbounded field would breach; the non-zero
  default above is what keeps the second half. I propose both anyway, because
  D-060's own central argument — two open defaults that only protect in
  combination protect nothing — applies verbatim to `limit`, which is `unpaged`
  written as a number, and because H-QUERY-21 is the case D-060's placement
  cannot serve. D-060's **Proven by** section also needs correcting: it records
  `Config.Check` as having moved the misspelled-entry failure to declaration, and
  only a test calls `Config.Check`.
- **[[D-006]]** — a preload is a batched second statement, loaded for every
  parent at once. `MaxPreloadRows` keeps the statement count and uses a `LIMIT
  n+1` tripwire that refuses rather than truncates. Round 1's wording forbade
  that mechanism and left the field unimplementable; see above.
- **[[D-021]]** — the magic must fail early. `sqlrepo.Define`, `crud.NewMeta`,
  `Blueprint.resolveRelationScopes` and `security.relationFieldName` all fail at
  declaration for this class of typo. `query.Config` is the odd one out, and it
  is the one declaration in this library a consumer writes entirely by hand.
- **[[D-013]]** — an unknown name is a rejection. Every entry in a
  `WithQueryFor` map is a set of names, so all of them resolve at mount, and a
  selector key the map does not hold is a refusal rather than a nil config.
  H-QUERY-17's empty list is the same principle applied to a value.
- **[[D-055]]** — a principal reaches a policy only through a context a
  transport binding wrote. `WithQueryFor`'s selector takes a `context.Context`
  for exactly that reason, and nothing in `query.Config` names an auth model:
  who maps a principal to a key is the application's business. (Round 1 cited
  [[D-058]] here, which is about directory layout and says nothing about any of
  this.)
- **[[D-045]]** — the shared half is transport-neutral. Putting the seam in
  `port` respects it; a `*http.Request` signature would not.
- **[[D-014]]** — deterministic output. Nothing proposed touches key ordering.
- **[[D-016]]/[[D-036]]** — `query` stays on `crud` and the standard library.
  Three int fields, a bool and a variadic `Plus` add no dependency.
- **No decision covers a client parsing this library's own message text.**
  [[D-039]] is about driver text this library reads; [[D-044]] is about what must
  not leave. H-QUERY-10 guarantee 5 falls in the gap between them, and the gap is
  worth writing down.

## DX verdict

Distance is measured to the ideal, not by severity; the blockers table is where
severity lives, and the two disagree on purpose in one row below.

| What the ideal asks for | Today | Distance |
|---|---|---|
| Filter/sort/select/preload/search bounds in one struct next to the route | Exactly this, and it is good | none |
| A stable order for an unsorted page | On by default — the primary-key tiebreaker, `UnstablePagination()` opts out | none |
| A column type's own parser honoured on both doors | Exactly this, including the `time.Time` carve-out that had to be reasoned about | none |
| Refusals a UI can act on | Structured and safe for values; the Go struct name reaches the body on the commonest refusal, and the path is the bare parameter on every flat term and every `sort`/`select`/`preload` entry | small |
| A misspelled allow-list entry stops the process | `cfg.MustCheck(meta)` exists, is called only by its own test, appears in no consumer-facing page, and reports one typo per restart | small |
| A chosen default order per endpoint | `sqlrepo.DefaultSort(crud.Desc("CreatedAt"))`, another file, per repository — so two routes need two repositories | small |
| An export endpoint that serves every row | `AllowUnpaged: true` — one field, correct — silently truncated by a `MaxLimit` set for the page | small (severity: the blockers table calls the same mechanism a sharp edge, because the truncation is silent to a scheduled job) |
| One option at the call site | `crudnet.WithQuery[Article, int64, ArticleUpdate](cfg)` — three type parameters on every option, per resource; the package documents a per-resource helper as the workaround (`crud/http/crudnet/options.go:30-46`) | small at one resource, large at twenty |
| A delta config for a second role | Nothing. `query.Config`'s zero values all mean something, so no struct literal can express "the base plus one column" | large |
| The page ceiling in the same declaration | `sqlrepo.MaxLimit(100)` in the `Define`; **absent by default**, and one repository cannot give two endpoints two ceilings | large |
| A ceiling on how deep a client may page | Nothing, anywhere. `crud/options.go:241-259` bounds `limit` only | large |
| A gate on `distinct` | Nothing. `?distinct=1` forces a sort or hash over the whole set, and the total becomes a derived table over the same set | large |
| Bounds that depend on the caller | A hand-written `port.Service` that must re-derive `NarrowForCount`/`NarrowForEntity`; unavailable at all under `Serving` | large |
| A ceiling on preloaded rows | Nothing. `sqlrepo.RelationScope` narrows the far side without bounding it; `crud/preload.go:193-196` refuses the shapes that look like a ceiling | large |
| A case-insensitive search box | Nothing. `crud.LikeIgnoreCase` exists and the search path does not call it; the escape hatch is `ilike` with a client-built pattern | large |
| A filter on a JSON column | Nothing, and no partial answer. `crud.Raw` is server-side and unresolved by design | large |
| Seeing what a slow request asked for | Nothing in the package. Log the raw URL at the handler and lose the resolved paths | large |
| A client discovering the endpoint's vocabulary | The lists are exported fields, so they are readable — but an empty list means "everything" with no way to expand it, entries are never canonicalised, and the effective caps are unexported | large |

**Overall:** Inside the document, this is the best-argued code in the
repository: every refusal names a path, every value is typed by its column, a
column type's own parser survives both doors, the search node cannot leak out of
its AND, and the hostile suite is genuinely hostile. Two things it does not do.
It does not bound *volume* — four separate bounds, none armed on a stock mount,
and the one that is proposed for them lives in another file. And one accepted
shape of a filter widens instead of narrowing, which is the failure the package
was written to make impossible. Customising leaves the short path in four
places, and the steepest is the one nobody expects: **two endpoints over one
repository cannot differ in page size, default sort, or whether they serve whole
result sets without a second `Define`** — one sentence that carries three rows of
the table above and is the strongest argument for moving these bounds into
`query.Config`.

## Release blockers found here

Ordered by what a consumer hits first on a stock mount. The opening clause of
the last column says whether they can work around it.

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | An empty multi-value list widens: `?f=status:notIn:` and `{"status":{"notIn":[]}}` compile to `1 = 1` and answer 200 with every row (`querystring.go:86-104`, `filter.go:294`, `crud/predicate.go:201-208`) | blocker | **No workaround at the endpoint.** A cleared filter chip is the default behaviour of every filter UI, and this is the module's own headline failure — an accepted filter that returns the whole table. The mirror, `in:` → `1 = 0`, blanks the screen with no refusal. `edge_test.go:118` pins both as intended, so the fix is a decision, not a patch |
| 2 | A preload has no row ceiling and no mechanism can express one today — `?preload=comments` on a page of 100 articles loads every comment of all hundred (`crud/query/request.go:233-238`, `crud/preload.go:193-196`) | blocker | Nearest mitigation is `sqlrepo.RelationScope`, which narrows the far side without bounding it. The module's remit is that an untrusted client cannot spend the server; `MaxPreloads` bounds relations, never rows |
| 3 | `limit` has no ceiling on a stock mount — `?limit=1000000000` compiles to that `LIMIT`, and `sqlrepo.MaxLimit` is unset by default (`compile.go:353-354`, `crud/sqlrepo/blueprint.go:53`, `crud/options.go:251-252`) | blocker | Workaround: `sqlrepo.MaxLimit(100)`, in another file, per repository. [[D-060]] closed this hole for `unpaged` and left the same request spelled as a number. **The general sweep files the same defect as its blocker 5 (`H-GENERAL-09`) and grades it `blocker` too; the crudhttp sweep grades it `serious` at a third placement.** Three sweeps, three homes — reconcile before the tag |
| 4 | Stock defaults permit 65,536 bind parameters — `MaxConditions: 64` × `MaxInValues: 1024` — one over PostgreSQL's limit (`compile.go:85`, `:91`, `:541-546`) | serious | `MaxInValues`'s own doc comment says the cap exists so "the honest 400" does not arrive "from the driver, as a 500" (`compile.go:39-45`). `countValues` bounds one list and never the document, so a stranger farms 500s from a stock mount and the code comment says it cannot happen |
| 5 | A search that resolves to no usable column returns the whole table instead of refusing — in both arms, so `?q=smith&searchFields=views` does it from a correct config (`compile.go:576-587`, `:599-627`, pinned by `compile_test.go:302`) | serious | **No workaround for the client-supplied path.** Silent wrong answer labelled "search results", triggered by a query string a stranger sends |
| 6 | The search box is case-sensitive, and differently so per engine — `?q=smith` misses "Smith" on PostgreSQL and matches it on MySQL (`compile.go:580`, `:616`, `crud/predicate.go:436`) | serious | Workaround: make the client send `ilike` with its own raw pattern, giving up wildcard escaping. `crud.LikeIgnoreCase` exists; the search path does not use it, while `compile.go:565` claims it does |
| 7 | `Config.Check`/`MustCheck` is called by nothing but its own test and appears in no consumer-facing doc, while [[D-060]]'s **Proven by** records it as having closed the misspelled-entry failure | serious | Workaround: `func init() { must(cfg.Check(meta)) }`, per resource, once you know it exists. Otherwise the column stays closed forever and every request is blamed on the client. Wiring it after the first tag is a breaking change; before it, one line |
| 8 | `distinct` is a client-settable cost knob with no gate and no field — `?distinct=1` forces a sort or hash over the whole result set, and the total becomes a derived table over the same set (`querystring.go:199`, `compile.go:397-399`, `crud/sqlrepo/repository.go:561-577`) | serious | **No workaround.** `AllowUnpaged` exists because a bound on how much work happens must be named; `distinct` is the second such knob and got no gate. It also silently changes what `select` means (`repository.go:436-448`) |
| 9 | An `isNull` term whose value is not a Go boolean silently inverts the filter — `?f=deletedAt:isNull:yes` becomes `IS NOT NULL`, through **both** doors (`querystring.go:69-73`) | serious | Workaround: `?filter={json}`, or spell it `true`/`false` and hope no client does otherwise. Returns exactly the rows the caller meant to exclude, with no refusal anywhere |
| 10 | Bounds cannot depend on the caller — `WithQuery` takes one static `*query.Config` in all four bindings and in `port`, and `Serving` refuses it entirely | serious | Workaround: a hand-written service that must re-derive `NarrowForCount`/`NarrowForEntity`, or nothing at all under `Serving`. **The seam is `port`'s, not this module's** |
| 11 | No ceiling on offset depth anywhere — `?page=10000000&limit=100` walks to the offset (`compile.go:351`, `crud/options.go:241-259`) | sharp edge | **No workaround.** The module page already says "nothing helps; `OFFSET` is O(n) on every engine" (`docs/modules/en/query.md:220`), and it is one URL edit from the link the dashboard publishes. `MaxPageSize` would not close it: one caps memory, the other caps work |
| 12 | A flat term keeps only the text before the first comma, in **both** the scalar and the textual arm and through **both** doors (`querystring.go:110`, `:80-84`, `crud/query/request.go:140-148`) | sharp edge | Workaround: the nested `filter` object, reachable on a GET only as `?filter={json}`. The textual arm is a fifth divergence UC-002 Gap 3 and Index gap 19 do not list |
| 13 | An empty allow-list grants every column the model gains later — a new `InternalNotes` hidden with `json:"-"` is still searchable, so a hit is an oracle for a column the API never renders (`compile.go:212-215`) | sharp edge | Workaround: name every column explicitly and re-review on every model change. [[D-060]] records the default as deliberately open and calls it expensive; nobody has written down that it is a disclosure |
| 14 | No cap can be put on what a permitted column *costs* — any `Filterable` text column accepts `contains`/`ilike`, an unindexed leading-wildcard scan, run twice per paginated read (`crud/query/filter.go:275-290`) | sharp edge | **No workaround but removing the column.** A consumer who locked `Filterable` to three columns believes the endpoint is bounded, and it is not |
| 15 | `AllowUnpaged` plus a non-zero `MaxLimit` truncates an export silently (`crud/options.go:242-247`) | sharp edge | Workaround: a second repository with no cap. The two documented pieces of advice combine into a nightly job that syncs the first 100 rows and reports success. Same mechanism as the remote sweep's blocker 1 |
| 16 | `nulls: "last"` is honoured on PostgreSQL and dropped on MySQL, with a 200 either way (`compile.go:302-310`, `crud/predicate.go:597-603`) | sharp edge | Workaround: never offer it on MySQL. The caveat lives in the `Order` method doc (`crud/predicate.go:524-531`) where no consumer reads it, and every list screen sorts by a nullable date |
| 17 | A LIKE-family operator against a non-text column reaches the database and fails there (`filter.go:222-227`, `querystring.go:80-84`, no column-kind check) | sharp edge | Workaround: none at the endpoint. A client can farm 500s from a public endpoint, where every other bad value is a 400 |
| 18 | A repository declared with `UnstablePagination()` turns every cursor request into a 400 blaming the client (`crud/sqlrepo/repository.go:400-412`) | sharp edge | Workaround: do not use it on a repository whose endpoints offer cursors. Nothing at declaration says the two settings are incompatible |
| 19 | The JSON door refuses the application's own parameters — `{"filter":{…},"includeArchived":true}` is a 400 while the same flag on the query string passes (`crud/query/request.go:79-100`) | sharp edge | Workaround: a header or the path. The guarantee that makes `WithScope` work holds on one door of two |
| 20 | A cursor walk over a mutable sort column is untested and probably repeats or skips rows — the only concurrency test mutates by insert (`test/integration/cursor_test.go:45,73`) | sharp edge | Workaround: cursor by `createdAt` or the key, never `updatedAt`. The guarantee everyone assumes is written down nowhere and pinned nowhere, and `-updatedAt` is the default sort of every admin list |
| 21 | Every query refusal carries one code, `errs.CodeBadQuery`, and the path is the bare parameter for a flat term and for `sort`/`select`/`preload`/`searchFields` (`port/kind.go:172-173`, `querystring.go:49-54`, `edge_test.go:198-205`) | sharp edge | Workaround: parse message text, which no decision sanctions. A UI cannot highlight the chip that was wrong on the door the chips use |
| 22 | No seam reports what a request resolved to, so a slow endpoint cannot be traced back to the document that caused it | sharp edge | Workaround: log the raw URL and lose the canonical paths and the condition count. The module page states the problem and stops (`docs/modules/en/query.md:176-177`) |
| 23 | No way to expose a JSON-column key, a computed value, or a grouped count to the DSL, and no way to drive a filtered bulk write from a validated document | sharp edge | Workaround: a hand-written endpoint, which loses the allow-list, the paths and the budgets in one step. Found in month two, on the half of the model that needed them most |

**Docs this sweep obsoletes.** `UC-002` Gap 3 says the two doors disagree in four
places; it is five — the textual arm's missing count check
(`querystring.go:80-84`) is in neither Gap 3 nor `docs/ai/usecases/Index.md`
gap 19, and both rows need it. `UC-002` guarantee 17 and Gap 6, and Index gap 20,
all frame the page cap as narrower than it is — none of them mentions offset
depth or `distinct`. `UC-002` Gap 4 already records blocker 1 and this file
promotes it. [[D-060]]'s **Proven by** records `Config.Check` as closing the
misspelled-entry failure; nothing but a test calls it. The Index is what the next
agent reads first, so a row left standing there is a fix nobody sees.

**Lands on this consumer, owned elsewhere.** Not the owner's to fix in this
package before the tag, but a consumer reading only this file would think they
were. `select` narrows the statement and not the response body — an unselected
column renders as its Go zero value, because the binding marshals `M`, and the
answer is `crudnet.WithTransform` (`crud/http/crudnet/options.go:81-83`), which
the crudhttp sweep carries as its own case. The per-caller config seam is the
`port` sweep's. `WithScope` reaching reads and not writes — with a scope of
`TenantID = 7`, `GET /{id}` on somebody else's row is 404 while `DELETE /{id}` is
200 (`crud/http/crudnet/options.go:87-96`, proven by
`crud/http/crudnet/write_edge_test.go:69`
`TestWithScopeReachesTheReadsAndSaysNothingAboutTheWrites`) — belongs in
`decorators/security`.

## Contested

- **Blocker 1 contradicts a test that pins the behaviour on purpose.**
  `edge_test.go:118` `TestAnEmptyValueListBecomesAConstant` blesses all four
  spellings, and its comment argues the case well: `IN ()` is a syntax error in
  every database, so it degrades to a constant. I am filing it as this sweep's
  worst finding anyway. The comment is right about SQL and wrong about requests:
  the scalar arm one branch over refuses an empty value at the path
  (`querystring.go:106-109`), so the package already has the correct answer and
  applies it inconsistently. UC-002 Gap 4 agrees with me; the test does not. That
  is a decision for the owner and it changes a green test either way.
- **H-QUERY-06 is kept here, cut to the question, marked as `port`'s to fix.**
  Three reviewers across two rounds said the case belongs in the port and
  binding sweeps because every line of evidence is outside `crud/query`. The
  evidence is, and the *question* is not: a consumer deciding what to write in a
  `query.Config` needs to know that the answer cannot vary by caller. Round 2
  removes the forty-line remedy from the case body — that lives once, in the port
  sweep — and keeps the question and the ownership line.
- **H-QUERY-03 guarantee 5 stays a serious finding, against UC-002.** UC-002's
  Status closes by calling the empty-search asymmetry "behaviourally proven and
  worth knowing rather than fixing". UC-002 reasoned about the *fallback* arm,
  where the endpoint's own config is the cause. The named-fields arm has the same
  ending (`compile.go:624-626`) and is driven by `searchFields` on the wire, so a
  stranger reaches it from a correct config — including the zero `Config` the
  module page calls "a usable default".
- **`MaxPageSize` in `query.Config` still challenges [[D-060]]'s placement, and
  now its default too.** Three sweeps propose three homes: a non-zero default on
  `sqlrepo.MaxLimit` (general), a clamp in `port.Rules` (crudhttp), an endpoint
  field (here). I keep the endpoint field, because H-QUERY-21 is the case the
  other two cannot serve. One release cannot ship three placements.
- **H-QUERY-10's guarantee 3 is downgraded rather than dropped.** It is a
  recorded carve-out in UC-002's **Out of scope**, so it is not a defect. It
  stays visible because a consumer shipping a public endpoint reads "nothing
  internal is in the body", and the Go struct name is in the body on the
  commonest refusal there is.
- **The case numbering shifted once.** Round 1's H-QUERY-16 (`select` narrows the
  SQL, not the payload) was a full case for something the crudhttp sweep owns;
  two reviewers said demote it, and it is now one clause in **Lands on this
  consumer, owned elsewhere** plus a DX row. Round 1's H-QUERY-17 is now
  H-QUERY-16. Cases 01–15 keep their numbers; 17–22 are new.
- **H-QUERY-19 is scored ✅ against a reviewer who filed it as a gap.** A reviewer
  said no happy case exercises `TextUnmarshaler` coercion and that a uuid/enum
  model is untested. The case was missing and is added; the verdict is not a gap.
  `coerce_test.go:19-31` defines exactly such a type and `:106` walks it through
  both doors. The honest finding is that nothing in the *sweep* said so, not that
  nothing in the code did.

## Edge cases

### E-QUERY-01 — An empty boolean group from a half-cleared filter builder
**Shape:** boundary
**Setup:** A UI serialises its cleared "any of these" control as `{"filter":{"or":[]}}` rather than omitting `filter`.
**What the consumer does:** It sends that document beside a tenant scope, expecting either no client filter or a refusal that lets the UI remove the malformed control.
**What must happen:** A syntactically present boolean group with no operands must not silently impersonate a meaningful narrowing; it should be refused at its path, or have an explicit documented no-filter meaning.
**Today:** ❌ wrong or unhandled
**Evidence:** `node` turns an empty predicate list into nil at `crud/query/filter.go:72-79`; `list` produces that nil for an empty `or` at `crud/query/filter.go:100-114`. `crud/query/edge_test.go:90-112` pins `{"filter":{"or":[]}}` as an accepted request with no `WHERE`. No test establishes a consumer-facing meaning for a present empty group.
**Blast radius:** silent wrong answer

### E-QUERY-02 — The boolean that is not a boolean
**Shape:** adversarial input
**Setup:** A hand-edited URL carries `?f=publishedAt:isNull:yes`.
**What the consumer does:** They expect the invalid value to receive the same 400 as `limit=lots`, rather than becoming a different null test.
**What must happen:** A unary operator must either accept a real boolean or refuse while naming the term; it must never choose the opposite predicate after a failed parse.
**Today:** ❌ wrong or unhandled
**Evidence:** `terms` ignores the `strconv.ParseBool` error and leaves `want` false at `crud/query/querystring.go:68-78`, so `isNull:yes` becomes `IS NOT NULL`. `crud/query/querystring_test.go:309-326` tests non-numeric paging refusals, but no query test covers an invalid unary value.
**Blast radius:** silent wrong answer

### E-QUERY-03 — The deliberately empty multi-select
**Shape:** boundary
**Setup:** A user unticks every status and the client retains `notIn: []` in the document.
**What the consumer does:** It asks the endpoint to interpret the empty chip rather than making every client know the database's answer to `NOT IN ()`.
**What must happen:** The request must be refused at the chip, because an accepted filter cannot widen to every row; `in: []` should not silently present an empty screen either.
**Today:** ❌ wrong or unhandled
**Evidence:** `buildMulti` passes an empty `notIn` list through at `crud/query/filter.go:292-303`; `crud/query/edge_test.go:115-140` pins the resulting `1 = 1` for `notIn` and `1 = 0` for `in`. The flat form reaches the same code after `splitList` drops a blank value at `crud/query/request.go:329-344` and `terms` accepts zero multi-values at `crud/query/querystring.go:86-104`.
**Blast radius:** silent wrong answer

### E-QUERY-04 — A pattern operator pointed at an integer
**Shape:** adversarial input
**Setup:** A public endpoint permits `Views` for filtering and a client sends `{"filter":{"views":{"contains":"1"}}}`.
**What the consumer does:** It expects an ordinary bad query to stop before the repository is called.
**What must happen:** LIKE-family operators must verify that the resolved field is text and refuse a numeric field with a `query.Error`.
**Today:** ❌ wrong or unhandled
**Evidence:** After resolving the field, the textual arm only unmarshals a string and emits `buildText`; it never checks `f.Type` at `crud/query/filter.go:222-227`. `buildText` unconditionally constructs a LIKE predicate at `crud/query/filter.go:275-290`. No `crud/query` test sends a textual operator to a non-text field.
**Blast radius:** confusing error

### E-QUERY-05 — A negative page size that becomes the default page
**Shape:** adversarial input
**Setup:** A stale client sends `?limit=-1&page=-2&offset=-3`.
**What the consumer does:** It expects the endpoint to reject impossible pagination rather than return page one under the requested URL.
**What must happen:** Negative paging values must be a refusal naming their parameter; zero may retain its documented default meaning.
**Today:** ❌ wrong or unhandled
**Evidence:** `ParseQuery` uses `strconv.Atoi` without a sign check at `crud/query/querystring.go:161-196`, then `Compile` emits page, limit and offset only when each is greater than zero at `crud/query/compile.go:349-358`. At the repository layer, negative limits and pages deliberately resolve to the default/first page (`crud/options_test.go:148-170`) and negative offsets resolve to zero (`crud/edge_test.go:460-477`). `TestParseQueryRejectsNonNumbers` covers `offset=-`, not a valid negative integer (`crud/query/querystring_test.go:309-326`).
**Blast radius:** silent wrong answer

### E-QUERY-06 — Two spellings of one page-size knob disagree
**Shape:** misuse
**Setup:** A URL assembled by two components contains both `limit=100` and `perPage=20`.
**What the consumer does:** It expects one authoritative value or a refusal that exposes the composition error.
**What must happen:** Conflicting aliases for the same setting must not silently select one by the library's private precedence order.
**Today:** ❌ wrong or unhandled
**Evidence:** the `num` helper returns the first non-empty spelling in its supplied order at `crud/query/querystring.go:161-171`, and the limit call gives `limit` precedence over `perPage`, `per_page`, `per-page` and `pageSize` at `:191-193`. The alias suite only tests one spelling at a time (`crud/query/querystring_test.go:258-306`); no test covers conflicting aliases.
**Blast radius:** silent wrong answer

### E-QUERY-07 — A cursor request with both directions
**Shape:** misuse
**Setup:** Infinite-scroll state accidentally retains `after` while the user presses Previous and adds `before`.
**What the consumer does:** It sends both opaque tokens and needs a clear refusal rather than a page chosen by option order.
**What must happen:** `after` and `before` are mutually exclusive at the wire boundary.
**Today:** ❌ wrong or unhandled
**Evidence:** `Request.Compile` appends both options when both strings are present at `crud/query/compile.go:381-386`. `crud.Before` then clears `After`, so the later option silently wins at `crud/options.go:118-133`, although `Options` documents that at most one may be set at `crud/options.go:29-32`. No `crud/query` test covers both fields together.
**Blast radius:** silent wrong answer

### E-QUERY-08 — A relation path longer than the configured depth
**Shape:** degenerate declaration
**Setup:** The resource sets `MaxDepth: 2` but exposes a self-referential preload path such as `Parent.Parent.Parent`.
**What the consumer does:** It relies on the one query declaration to bound relation traversal as it does filter and sort paths.
**What must happen:** The configured depth must constrain preload paths too, or the distinct fixed execution cap must be visible in the query declaration.
**Today:** 🟡 partial
**Evidence:** `path` enforces `Config.MaxDepth` for fields at `crud/query/compile.go:548-563`, but the preload loop calls `meta.RelationAt` directly at `crud/query/compile.go:330-347`. Execution does have a separate fixed default of five levels (`crud/preload.go:11-14,113-123`), and `TestPreloadDepthIsCapped` exercises that default (`crud/query/preload_test.go:170-177`); no test ties a smaller `query.Config.MaxDepth` to a preload.
**Blast radius:** confusing error

### E-QUERY-09 — A duplicate preload removes its own narrowing
**Shape:** seam
**Setup:** A saved-view merge emits both a bare `comments` preload and a filtered `comments` preload.
**What the consumer does:** It expects the duplicate to be refused or both supplied constraints to be preserved; the document includes a filter for a reason.
**What must happen:** A request must not return a wider child collection than one of its own duplicate entries asked for without a refusal or an explicit merge rule.
**Today:** 🟡 partial
**Evidence:** the query compiler accepts and appends each preload independently at `crud/query/compile.go:330-347`. The preload tree deliberately lets the bare request discard narrowings at `crud/preload.go:70-102`, and `crud/preload_edge_test.go:75-110` pins that "wider request wins" behaviour. No query-door test establishes that behaviour for duplicate `Preload` entries in one document.
**Blast radius:** silent wrong answer

### E-QUERY-10 — The same sort field requested both ways
**Shape:** misuse
**Setup:** A URL merger creates `sort=title,-title`.
**What the consumer does:** It expects a clear invalid-sort response, since ascending and descending cannot both be the requested order.
**What must happen:** Duplicate canonical sort paths with incompatible direction or null placement must be refused; a request must not receive an arbitrary first direction.
**Today:** ❌ wrong or unhandled
**Evidence:** duplicate canonical paths are skipped after their first occurrence at `crud/query/compile.go:281-311`, so the first direction is retained and the later one disappears. `TestAClientChosenListAndSortAreBounded` pins that a repeated sort path is rendered once (`crud/query/compile_test.go:619-644`), but it does not establish a rule for contradictory repeats.
**Blast radius:** silent wrong answer

### E-QUERY-11 — A deduplicated search list fails before it is deduplicated
**Shape:** boundary
**Setup:** A UI emits `searchFields` with the same selected field repeated 17 times under the default cap of 16.
**What the consumer does:** It expects the semantic set of one field to be searched once, as it is below the cap.
**What must happen:** Duplicate fields must be eliminated before their count is compared with the per-request limit, or the request must be refused as an explicit duplicate rather than "too many" fields.
**Today:** 🟡 partial
**Evidence:** `search` checks `len(names)` against `MaxSort` before creating its `seen` set at `crud/query/compile.go:589-614`. The same function deduplicates entries only afterwards at `:608-616`; `crud/query/compile_test.go:592-617` separately pins the cap and one repeated field, but not repeats beyond the cap.
**Blast radius:** confusing error

### E-QUERY-12 — A declaration whose cap is zero or negative
**Shape:** misuse
**Setup:** A tired resource author writes `MaxInValues: -1`, or explicitly sets `MaxDepth: 0` expecting a hard zero rather than the implicit default.
**What the consumer does:** It calls the documented declaration check during startup and expects a bad bound to fail there.
**What must happen:** A non-positive explicit bound must either be rejected at declaration time or have one documented, consistent meaning; a negative number must not silently become the same setting as zero.
**Today:** 🟡 partial
**Evidence:** every numeric accessor treats only values greater than zero as configured and otherwise supplies a default at `crud/query/compile.go:95-128`, so both zero and negative values silently select that default. `Config.Check` validates only allow-list entries at `:152-198`; its test table covers names and wildcards, not numeric bounds (`crud/query/compile_test.go:704-756`).
**Blast radius:** confusing error

### E-QUERY-13 — A document nested beyond the parser's own limit
**Shape:** adversarial input
**Setup:** A hostile caller sends a filter with tens of thousands of nested `not` objects.
**What the consumer does:** It expects an ordinary query refusal, not a stack failure or partial compilation after some nesting has been walked.
**What must happen:** Depth must be bounded before recursive compilation can consume the process, and either front door must return no options when it rejects.
**Today:** ✅ handled
**Evidence:** `node` checks depth before descending at `crud/query/filter.go:16-29`; `crud/query/hostile_test.go:384-406` drives depths 100, 5,000 and 50,000 through both the decoded document and a raw filter and requires rejection. `crud/query/hostile_test.go:442-467` separately pins that a rejected document returns no options.
**Blast radius:** none

## Edge verdict

The worst open edge is still an accepted filter that removes its own narrowing: an empty `notIn` is `1 = 1`, and an empty boolean group also becomes no predicate (`crud/query/filter.go:72-79,292-303`; `crud/query/edge_test.go:90-140`). The package is genuinely closed against deep filter recursion and against handing options back on that refusal (`crud/query/filter.go:16-29`; `crud/query/hostile_test.go:384-467`); its existing hostile suite also pins unknown-name and list-cap refusals. It remains too willing to normalise malformed or contradictory input — invalid unary values, negative paging, aliases, cursors and sort directions choose a different request rather than refuse it (`crud/query/querystring.go:68-78,161-196`; `crud/query/compile.go:281-311,381-386`). Declaration-time safety is incomplete: `Check` catches misspelled paths but not numeric bounds, and the configured depth does not govern preload paths (`crud/query/compile.go:95-198,330-347,548-563`).

## Release blockers found here (edge)

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | An empty boolean `or`/`and`/`not` is accepted and contributes no predicate (`crud/query/filter.go:72-114`; pinned by `crud/query/edge_test.go:90-112`) | serious | A malformed filter builder receives 200 for the unfiltered list. This is the same silent-widening failure class as the existing empty-`notIn` blocker, reached through a different ordinary UI shape. |
| 2 | `?f=publishedAt:isNull:yes` silently becomes `IS NOT NULL` because `ParseBool`'s error is discarded (`crud/query/querystring.go:68-78`) | serious | A typo reverses which rows a nullable-column filter returns, with no 400 for the client or operator to notice. |
| 3 | Negative `page`, `limit` and `offset` parse successfully then resolve to the default page/zero offset (`crud/query/querystring.go:161-196`, `crud/query/compile.go:349-358`) | sharp edge | A stale or malicious pagination value gets a 200 for a different page than the URL requested; the existing test only covers non-numbers. |
