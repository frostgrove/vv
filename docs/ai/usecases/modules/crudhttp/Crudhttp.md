# crudhttp · crudnet · crudfiber · crudgin — ten routes over a repository, and the same ten on whichever framework the project already uses

**Covers:** `github.com/frostgrove/vv/crud/http/crudhttp`, `github.com/frostgrove/vv/crud/http/crudnet`, `github.com/frostgrove/vv/crud/http/crudfiber`, `github.com/frostgrove/vv/crud/http/crudgin`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — the happy half's write-side gaps remain, and hostile but valid HTTP bodies add three more ways to alter the wrong data: under `New`, an absent or `null` mutation body becomes a zero model, a duplicate JSON key chooses its last value, and `null` in a numeric bulk-id list becomes key zero. A nil query config also turns an apparently bounded public endpoint back into the open default. The common body-cap contract belongs to Port and still lacks an exact-boundary triplet; string IDs also have no stated encoded-slash or path-normalisation contract across the three routers.

## What a consumer is actually trying to do

Somebody has a table and needs an API over it this week. List with filters and
sorting, one row by id, create, edit, delete, a count for the dashboard. They
have written that by hand four times. It is six hundred lines of the same code
with different nouns in it, and the bugs are always in the same places: the
filter that silently matched everything, the patch that wiped a field the form
did not send, the id that came back wrong in the browser.

They are not starting on an empty router. There is already a health endpoint, an
authentication middleware, and three hand-written routes that will never be CRUD.
The new resource has to sit beside those, under the same prefix, behind the same
middleware, and answer a failure in the same shape — otherwise the frontend needs
two error parsers.

What they are afraid of is opening a query language to the internet. They want to
say, once, which columns a client may filter, sort and search on, and how much
may come back, and be told plainly when a client asks for something else. A
clause that is quietly dropped is worse than a 400, because it returns the whole
table and looks like success.

The other half of that fear points the other way, at the request body. Their
model has a `role` column, a `price`, a `tenant_id` and a `deleted_at`. Somebody
is going to ask which of those a client can set through the edit form, and the
answer has to be a line they can point at, not "whatever the generator kept".

They also have twelve resources, not one. The twelfth has to cost about what the
first cost. And half of them are the same shape: same tenant rule, same page
size, same presenter that hides the internal columns. Somebody on the frontend
then asks what the twelve endpoints accept, and the answer has to be better than
"read our Go".

By the second week the requirements stop being one row at a time. Create the
order and its lines. Stamp an audit entry beside the insert. Do not increment the
quota counter if the write failed. Refuse the second of two people editing the
same row, and do not create two orders when a phone retries a timed-out POST.
None of that is exotic; all of it is a boundary around more than one statement.

Then, some morning, somebody says "the API is returning 500". They need to know
why, from their own logs, without redeploying with a print statement in it — and
they need the 500 graph they alert on to mean something, rather than being half
made of browsers that closed a tab.

## Happy cases

### H-CRUDHTTP-01 — The first resource, Tuesday afternoon
**Who:** a backend developer with one model, one database handle and a router already running
**Wants:** a working list/read/create/update/delete API over that model before the end of the day.
**Story:** They declare the model, run the generator, bind the repository to the handle, and mount it under `/articles`. They curl the collection, get a page, curl one row by id, POST a new one.
**Must hold:**
1. Mounting is one statement, and it names no type arguments.
2. The list answers a body a pager can be rendered from — `items`, `page`, `limit`, `total`, `totalPages`, `hasNext`, `hasPrev` — not a bare array.
3. `GET /articles` and `GET /articles/` both reach the collection, or one redirects to the other. Neither is a 404.
4. An id in the URL is converted to the key type, whether the key is an int, a uuid or a slug.
5. Every value the key type can hold is reachable as a URL. A slug-keyed row is fetched at `/articles/<slug>` for any slug the table allows.
6. All of the above holds on a handler nothing was configured on.
**Today:** 🟡 partial
**Evidence:** (1), (2), (4) and (6) hold. `crud/http/crudnet/handler.go:149` registers both spellings of the collection and uses `/{$}` at the root so a root mount does not claim every unclaimed path in the process (`crud/http/crudnet/routing_test.go:89` `TestMountingAtTheRootClaimsOnlyTheRootPath`); `crud/http/crudnet/handler_test.go:139` compiles a filter, a sort, a preload and paging off a handler nothing was configured on; `:188` `TestListAnswersWithThePageEnvelope` pins the seven keys and their values; `port.CoerceID` is reached at `crud/http/crudnet/handler.go:465-466`. (3) holds outright on two and in the redirect form on the third: `crud/http/crudnet/routing_test.go:59` `TestBothSpellingsOfTheCollectionAnswer`, Fiber matches both, and Gin answers `/articles/` with a 301 from `RedirectTrailingSlash` because registering both forms on an engine collapses them and Gin panics (`crud/http/crudgin/handler.go:140-146`). **(5) fails on all three, and only for a string-shaped key.** `/count`, `/query` and `/bulk-delete` are siblings of `/{id}`, and the more specific pattern wins by construction — that is what `crud/http/crudnet/routing_test.go:18` `TestStaticRoutesAreNotSwallowedByTheIDRoute` exists to guarantee, and Gin and Fiber register `/count` before `/:id` for the same reason (`crud/http/crudfiber/handler.go:152-153`). So a slug-keyed resource has three ids that are unreachable and that answer the wrong route instead of 404: `GET /articles/count` returns `{"count": n}` for an article whose slug is `count`. No test, no case and no module page names the three.
**If not ready:** For (5), nothing to build before the tag — one row in each module page's routes table saying the three segments are reserved, and a sentence in the usage guides telling a slug-keyed resource to prefix its collection routes. Closing it properly means moving the fixed routes off the collection root (`/articles:count`, or a `_` prefix), which is a wire change on four transports and therefore a decision. Separately, (6) is true of the handler and not of the afternoon: `U` is `cmd/vv`'s output, so `sqlrepo.Define[Article, int64, ArticleUpdate]` does not compile until the generator has run — the step most likely to go wrong on day one, and no case here covers what a consumer sees when the DTO is stale.

### H-CRUDHTTP-02 — Bound what a public client may ask for
**Who:** the author of a public read API sitting in front of a table with an internal `cost_price` column
**Wants:** filtering and sorting on the columns they choose, and refusals with a reason on everything else.
**Story:** They pass a query config listing the filterable and sortable fields. They try a filter on a hidden column and expect a 400 naming it. Then they try `?limit=1000000` to see what the endpoint does with a client that wants the whole table in one response.
**Must hold:**
1. A field outside the allow-list is a 400 that names the path, not a silently dropped clause.
2. A misspelled parameter or document key is refused, not ignored.
3. A request cannot turn pagination off unless the endpoint said it may.
4. The endpoint has a ceiling on how many rows one response can carry, and it is set where the endpoint is declared.
5. `GET /count` and `POST /count` honour the same allow-lists, and honour the filter without paying for the paging.
**Today:** 🟡 partial
**Evidence:** 1–3 and 5 hold. `crud/http/crudnet/options_test.go:301` `TestWithQueryBoundsWhatClientsMayAskFor`; `crud/http/crudnet/edge_test.go:157` `TestAQueryThatNamesSomethingTheModelLacksIsABadRequest` (unknown filter, sort, select and preload paths, each a 400 naming the path); `crud/http/crudnet/write_edge_test.go:152` and `:165` refuse a misspelled document key and a misspelled query parameter; `crud/http/crudnet/handler_test.go:512` `TestUnpagedIsRefusedOnAnEndpointThatDidNotDeclareIt` is the control for `:490`, per [[D-060]]; `crud/http/crudnet/handler_test.go:229` `TestCountKeepsTheFilterAndDropsEverythingElse` covers 5, and the count body is `{"count": n}` (`crud/http/crudnet/handler.go:255`). **4 does not.** `query.Config` caps depth, conditions, `in` values, sort terms and preloads and has no page-size cap at all (`crud/query/compile.go:33-68`); the clamp is `crud/options.go:241` `Options.Resolved`, which reads `maxLimit` from the repository's blueprint, and `crud/sqlrepo/blueprint.go:53` documents zero as "no cap" — the default. What is set for you is `DefaultPageSize` (20, `crud/sqlrepo/blueprint.go:26`), which applies only when the client names no limit. The failure is the process's heap and not a slow query: the page is read into a slice and `json.Marshal`ed into one buffer before a byte is written (`crud/http/crudnet/options.go:205-221`), so `?limit=1000000` on a shared process is an out-of-memory kill, not a response the load balancer trims.
**If not ready:** The consumer goes back to the `sqlrepo.Define` call and adds `MaxLimit(100)`. That works, it is one line, and it clamps `unpaged` and a cursor walk as well as `?limit=` — see the Contested note. But it is on the wrong object for this job: two endpoints over the same repository — the public one and the admin one — cannot have different ceilings, and `Serving` over somebody else's service has no lever at all. This is [[UC-002]]'s recorded gap 20 seen from the transport, and closing it is joint with `port`: a `MaxLimit` on `port.Rules` that clamps down and never up, with a non-zero default in the shape of `Rules.BulkCap()`. There is also no streaming shape for the export job that makes people reach for `unpaged` in the first place, and no case here asks for one.

### H-CRUDHTTP-03 — The tenant rule has to reach the writes
**Who:** a SaaS developer whose rows all carry a `tenant_id` and whose tokens all carry a tenant claim
**Wants:** no request ever to see or touch another tenant's row, including on routes they forget about.
**Story:** They read the option list, see `WithScope`, and reach for it because it is the one lever on the handler that takes the request. Then they check the awkward ones by hand: a `GET` of another tenant's id, and a `DELETE` of the same id.
**Must hold:**
1. A scoped list's `total` counts only the caller's rows, and its pages are not short — the narrowing is in the statement, not a filter over the page.
2. A row outside the caller's scope is 404 on read, not 403 — a denial confirms the row exists.
3. The same id that answers 404 on `GET /{id}` also answers 404 on `DELETE /{id}` and `PATCH /{id}`, and is not removed by the bulk delete.
4. Nothing about the tenant appears in the handler's configuration, so a route added next month inherits the rule.
**Today:** 🟡 partial — the guarantee is `security`'s and holds there; the transport ships a lever with the same name that reaches half of it
**Evidence:** the answer is `security.Gate` on the repository, and it is pinned by tests rather than by an example: `crud/decorators/security/principal_test.go:147` `TestScopeAttrNarrowsInSQLAndFreezesTheColumn` (the refused create, with the control that a create into the caller's own tenant still succeeds), `test/integration/auth_jwt_test.go:112` `TestATokensTenantClaimNarrowsTheStatement`, and `crud/decorators/security/security.go:677` `Delete`, which narrows with `crud.And(scope, crud.InAny(pk, ids))` and returns zero rows for another tenant's id — which `port/service.go:217-226` turns into `crud.ErrNotFound`, so the delete route really is a 404. That is (3), and it is pinned end to end by `crud/http/crudnet/edge_test.go:390` `TestDeletingNothingIs404ForOneRowAndZeroForASet`. [[D-008]] is why it is 404 and not 403. The rest is [[UC-004]]'s and the security sweep's. **What is this module's is the transport option with the same name:** `WithScope` reaches `List`, `Count` and `Get` and nothing else — the scope is read at `crud/http/crudnet/handler.go:412` and fed to those three call sites, while `Delete` builds a `port.DeleteCommand[ID]` with no options at `crud/http/crudnet/handler.go:376` and `Save`/`Update` take none either. The asymmetry is pinned rather than left to be discovered (`crud/http/crudnet/write_edge_test.go:69`, and `:114` `TestARowHiddenFromReadsIsStillDeletableByID` states the outcome in the open: 404 on `GET`, 200 and a real deletion on `DELETE`), and the option's doc comment spends ten lines saying it (`crud/http/crudnet/options.go:87-96`). H-CRUDHTTP-17 is the same root cause with the parent id in the URL instead of the token.
**If not ready:** Nothing to build — the gate is the answer, and it is the one a consumer must be sent to. What is left is a name: an option called `WithScope`, listed beside `WithQuery` and `ReadOnly` in every module page's option table, reads as protection. `ReadScope` says at the call site what the doc comment has to say in a paragraph. It is not a one-line rename: `WithScope` exists on four bindings (`crud/rpc/crudgrpc/options.go:86` carries it too) and is named in eight module pages, so it is four packages plus eight option tables, before the tag or not at all.

### H-CRUDHTTP-04 — The wire shape is not the model
**Who:** an API author whose model has `PasswordHash`, `InternalNotes` and an audit column
**Wants:** a request body of their own on writes, and a response that never carries the columns above.
**Story:** They write a small input struct and a mapper onto the model, and a presenter function for the way out. They check that the presenter runs on the create response too, not only on reads. Then they try to give `PATCH` the same input type.
**Must hold:**
1. The create and replace bodies can be a type of their own, mapped onto the model before anything is written.
2. The response shape is theirs, on the entity routes and inside the page alike.
3. The pager survives the presenter — items change type, `total` and `hasNext` do not.
4. The same input type reaches `PATCH`.
**Today:** 🟡 partial
**Evidence:** 1–3 hold: `crud/http/crudnet/handler.go:97` (`NewFor` infers the fourth type parameter from the mapper), `crud/http/crudnet/options_test.go:99` `TestWithTransformAppliesToWritesToo`, `crud/http/crudnet/options_test.go:74` and `crud/page.go:48` `MapPage` (the pager kept, cursors included). **4 does not hold at all:** `crud/http/crudnet/handler.go:318-319` decodes `PATCH` straight into `U`, the generated DTO, and the mapper is never consulted there. How a violation on a renamed field comes back is H-CRUDHTTP-08's must-hold 1 and is not restated here.
**If not ready:** A resource whose public patch shape differs from the generated DTO has no seam — the choices are to rename the DTO's JSON tags at codegen time, or to write the `PATCH` route by hand. A second mapper for `U` is a fourth constructor's worth of surface on four transports, so it is a decision, not a patch.

### H-CRUDHTTP-05 — The server owns the id and the timestamps
**Who:** anyone exposing a create route to a client they do not control
**Wants:** a client that sends `{"id": 1, "createdAt": "2020-01-01"}` to be told the truth by the row that comes back.
**Story:** They POST a body with an id and a generated column in it, read the response, then try the same thing through `PUT /999`.
**Must hold:**
1. A create answers 201 with the row as stored, and every column marked generated or client-unchoosable is cleared before the insert.
2. `PUT /{id}` on a database-owned key replaces and never creates — it is the second door into the id space and it closes the same way.
3. A key the client legitimately owns — a uuid, a slug — is unaffected: `PUT /{uuid}` on a row that does not exist still creates it.
**Today:** 🟡 partial — 1 and 2 hold and are pinned; 3 is asserted by a comment and by nothing else
**Evidence:** the guarantee is `port`'s and belongs to that sweep — `port/service.go:190-196` is the `s.meta.PK.Auto && !s.allowClientID` branch, with the comment naming the PostgreSQL sequence that stops advancing and collides on every subsequent insert ([[D-012]]). What is HTTP's is that `PUT` is a second door, and it is pinned by a test carrying the same name in all three bindings: `crud/http/crudnet/write_edge_test.go:20` `TestPutIsNotAWayAroundAllowClientID`, `crudgin/write_edge_test.go:22`, `crudfiber/write_edge_test.go:22`. Nothing exercises the non-auto arm: every model that reaches `Replace` in a test has `db:"id,pk,auto"` (`crud/http/crudnet/fake_test.go:24`, `port/service_test.go:22`), and the one model in the tree with `db:"id,pk,noauto"` (`test/integration/uuid_test.go:38`) is exercised through the repository and the DSL, never through a binding and never through `Replace`.
**If not ready:** A uuid-keyed resource is relying on `PUT` creating, and that is what breaks silently the next time the auto/non-auto branch is touched. One `port/service_test.go` case over a `noauto` model closes it, and the finding belongs on the `port` sweep's list rather than this one.

### H-CRUDHTTP-06 — A form patch, and a key nobody knows
**Who:** an admin-tool developer whose edit form posts only the fields it displays
**Wants:** an omitted field left alone, an explicit null cleared, and a typo not to look like a save.
**Story:** They PATCH `{"title":"x"}` and check the description survived. They PATCH `{"description":null}` and check it cleared. Then somebody's client sends `{"titel":"x"}`, and later `{"tenantId": 9}`.
**Must hold:**
1. A field the body omits is not written.
2. A field sent as `null` writes NULL, and is distinguishable from (1) by the stored row.
3. The response is the row as it now stands, including anything the database changed.
4. A key the DTO does not define is refused, or at minimum is visible to the client — not accepted in silence.
**Today:** 🟡 partial
**Evidence:** 1–3 hold and carry the same test names in all three bindings (`crud/http/crudnet/handler_test.go:354` and `:385`); the semantics are [[UC-003]]'s. **4 does not, and it is worse than a typo.** `port/porthttp/body.go:87` decodes a write body with a plain `json.Unmarshal`, so an unknown key is dropped. On `PATCH` that is a 200 over an unchanged row — `crud/sqlrepo/repository.go:729` returns the current row when the diff is empty. On `POST` it is a **201 over a wrong row**: `Save` has no such branch (`crud/sqlrepo/repository.go:617-624`), so the insert proceeds with the mistyped field left at its zero value and the client is told it saved. And the generated DTO deliberately omits the key, the immutable and the generated columns (`internal/codegen/codegen.go:47` `tagDropped`), so `PATCH {"tenantId": 9}` is a 200 that changed nothing — a client attempting to move a row between tenants is answered with success. The contrast is one file over: `crud/query/request.go:87-89` decodes the *query* document with `DisallowUnknownFields` precisely because a typo that is ignored answers a different question than the one asked ([[D-013]]).
**If not ready:** The consumer writes their own decode in front of the route, or accepts that a client typo is invisible. The close is a strict body check specified against the *decode target*, not the model — see the DX section, and H-CRUDHTTP-04 for why the difference matters.

### H-CRUDHTTP-07 — Two people editing the same row, and one client sending the same request twice
**Who:** the same admin-tool developer, with two tabs open, and a mobile client on a flaky network
**Wants:** the second save refused rather than winning, and a retried create not to make a second row.
**Story:** They open a row in two tabs, edit both, and save both. Separately, their phone app POSTs an order, times out at 30s, and posts it again.
**Must hold:**
1. The second save does not overwrite the first without being told.
2. The client can say which version it is editing, on the wire, without a hand-written route.
3. The refusal is a 409 a generic client can branch on.
4. A create replayed with the same key does not create a second row, and the API says how to ask for that.
**Today:** 🟡 partial — the concurrency guarantee exists a layer down and has no spelling up here; the retry question has no answer at all
**Evidence:** (1) and (3) are [[UC-009]]'s and hold: the repository's version column refuses an interleaved write between the load and the write, and `crud.ErrStaleVersion` maps to 409 through the same table every other sentinel does (`port/porthttp/errors.go:61`). (2) has nothing at all. There is no `If-Match`, no `ETag`, no version key in the patch document — the generated DTO omits the version column by construction (`internal/codegen/codegen.go:47`) — and `PUT` routes through `port/service.go:209` `Replace`, whose `Save` has no predicate to check anything in. (4) has nothing either: no `Idempotency-Key`, no de-duplication seam, and `grep -rn "Idempoten" crud/ port/` comes back empty. The answer that exists is "declare a unique index and let the client interpret the 409" — which is defensible, is what `crud/http/crudnet/write_edge_test.go:134` `TestAnIntegrityConflictIsA409WithAMessage` produces, and is written down nowhere.
**If not ready:** For (2) a consumer writes a hand-rolled route that loads, compares and calls `UpdateAll` with the version in the predicate, which is the shape this module exists to remove. The cheap half is a wire spelling: `If-Match` on `PATCH`/`PUT`, refused with 428 when the resource declares a version column and the header is absent. For (4) the cheap half is a paragraph: say that a retried create is the unique index's job, and that the 409 is the intended answer. Both are wire-contract decisions and have to land on all four transports.

### H-CRUDHTTP-08 — A refusal the client can act on
**Who:** a frontend developer consuming the API
**Wants:** to put the message next to the right input, in the user's language, without parsing prose.
**Story:** They POST a signup with a duplicate email and a missing organisation. They read the body. They send it again with `Accept-Language: fr`, against a deployment that wired a message catalogue.
**Must hold:**
1. The violation names the key the client actually sent — including when the input type renamed the field, and when nobody declared a mapping.
2. The locale comes off this request's `Accept-Language`, and not off whatever locale the fault was created under.
3. Every violation the payload caused comes back, not the first one the database reached.
4. Wiring a message catalogue does not cost the field-name translation, and translating field names does not cost the catalogue.
**Today:** 🟡 partial
**Evidence:** the statuses, the envelope and the internal short-circuit are [[UC-015]]'s and the `errs`/`porthttp` sweep's, not this module's; `port/porthttp/render.go:130-131` is where a 500 loses its body before anything is copied out of the fault. What is this module's: (1) holds through the raw-body fallback on the three write routes — `crud/http/crudnet/write_edge_test.go:379` `TestAConstraintViolationNamesTheFieldTheClientSent` names the key with the control that another table's column yields no field at all, and `crudfiber` keeps its own copy in Locals because Fiber's `c.Body()` is valid only inside the handler (`crud/http/crudfiber/handler.go:476`). It **splits on a rename**, and the split is documented nowhere: a generated adapter declares the hop — `internal/codegen/adapter.go:80` emits `func (XMapper) Resolve(p errs.Path) (errs.Path, bool)` — and `port.Hops` picks it up (`port/path.go:53`); a hand-written mapper does not, because `port/mapper.go:9` makes `errs.Resolver` optional on purpose, and the test that looks like it proves the rename actually proves the *service's* hop (`crud/http/crudnet/edge_test.go:485` `TestAServicePathHopReachesTheRenderedField`, where the mapping is declared by `pathService.Paths()`). Without a hop, the fallback matches the violation's last path segment against the body's keys (`port/porthttp/bodyindex.go:104`), so `Name` against a body of `{"label":…}` finds nothing and reaches the client as `Name` — the model's name, on a resource whose whole point was that the model is not the wire. (2) holds and is read at the transport on purpose, so a fault crossing a queue does not carry a stale locale (`crud/http/crudnet/options.go:181`, `crudfiber/options.go:204`, `crudgin/options.go:213`; `TestTheRequestLocaleReachesTheMessageLadder` in all three). (3) is not out of the box: without `WithFaults` and a probe the answer is the first violation the driver reached. The probe is opt-in ([[D-042]]), nothing in `_examples` wires it, and neither `docs/modules/en/crudhttp.md` nor the option table on any binding's page mentions it. **(4) is the sharp one.** `build` uses the hop-aware renderer only when no renderer was set (`crud/http/crudnet/handler.go:129-131`), and a catalogue is wired by passing a renderer — so on a resource where a hop exists, `WithRenderer` silently drops it. It bites exactly where the hops are worth most: `NewFor` with a generated mapper, and `Serving`/`ServingFor` over a service that declares `Paths()` — which is what `_examples/example/blog/vv_gen.go:145` mounts.
**If not ready:** For (1)'s rename half, the honest fix before the tag is a sentence on `NewFor`'s doc comment: a mapper that renames a field should implement `errs.Resolver`, and `port.MustPathMap` is how. For (3), one sentence and one row in the option tables. For (4), one line inside each binding's `build`: compose rather than replace — `NewRenderer(WithResolvers(hops...), <the consumer's options>)` instead of taking the consumer's renderer whole. Today the consumer's recovery is to pass `crudhttp.WithResolvers(ArticleMapper{})` themselves, which they can only do if they know the hop was there.

### H-CRUDHTTP-09 — Twelve resources on one router
**Who:** a developer converting an existing service, resource by resource
**Wants:** the twelfth resource to cost what the first cost.
**Story:** They mount articles, tags, authors, comments, and eight more. Ten of them share a tenant scope, a page config and a presenter. Two are read-only. Then they write tests for all twelve without standing up a database.
**Must hold:**
1. Mounting a resource with the shared settings is one short statement.
2. Settings shared by ten resources are written once.
3. Turning one of them off for one resource does not mean abandoning the shared path.
4. A mounted resource can be exercised end to end in a unit test, and the supported way to do it is written down.
**Today:** 🟡 partial
**Evidence:** 1 holds only when nothing is configured. With options, every one spells all three type parameters — `crudgin.WithQuery[Article, int64, ArticleUpdate](cfg)` — and the `Option` type's own doc comment says so and offers a per-resource helper *function* as the workaround, noting that an alias does not help because it names the result type and the inference is not stuck there (`crud/http/crudnet/options.go:25-51`). A real example carries it: `_examples/sqlx-pgx-gin/main.go:87-93`, of which the type-parameter ceremony is the single line `:88`. 2 has nothing: no group, no defaults value, no way to say "these settings, for everything under `/api/v1`". The exported surface confirms the shape (`docs/api/surface.md:379-409`) — four constructors, eleven options, `Mount` and nothing that takes more than one resource. 4 works and is undocumented: `crud/crudtest` is a `crud.Source`, so `crudnet.New(Articles.Bind(crudtest.Postgres().Push(...)))` mounts over it and a handler test needs no database — but no case, module page or usage guide says so. The three bindings test themselves with hand-rolled fakes (`crud/http/crudnet/fake_test.go`), and `_examples/example/blog/blog_test.go:14` uses `crudtest` against a repository and never a handler, so a consumer looking for the supported pattern finds the library's private one.
**If not ready:** The consumer writes one generic helper per binding — `func mount[M any, ID comparable, U any](r gin.IRouter, path string, repo crudgin.Repository[M, ID, U], extra ...crudgin.Option[M, ID, U])` — whose type parameters infer from the repository, and puts the shared options inside it. That is about six lines and it does close 1 and 3. It is also the first thing every consumer will write, which is the argument for shipping the shape rather than the workaround. For 4, one paragraph in a usage guide.

### H-CRUDHTTP-10 — Beside the routes that will never be CRUD
**Who:** a developer adding a resource to a service that already has an API
**Wants:** the new routes under the same prefix and middleware, and every failure in the service the same shape on the wire.
**Story:** They mount the CRUD routes on the group that already carries the auth middleware. They install the error middleware over the whole router, with a message catalogue, so their own handlers and the CRUD routes answer alike. They check a 401 from the auth middleware against a 404 from a CRUD route.
**Must hold:**
1. The routes register onto an existing router or group, not only onto a fresh one.
2. Options given to the error middleware — the catalogue, the violation cap — reach the CRUD routes as well as the hand-written ones.
3. Installing it twice — on the engine and on a group — renders once.
4. A handler that already wrote a response is left alone.
5. An authentication refusal comes out of the same envelope as a CRUD refusal.
6. A body over the cap is refused in the envelope whichever way the routes were mounted.
**Today:** 🟡 partial
**Evidence:** 1 holds, with three different spellings: `crudgin.Register(gin.IRoutes)` at `crud/http/crudgin/handler.go:147`, `crudfiber.Register(fiber.Router)` at `crud/http/crudfiber/handler.go:154`, and on `crudnet` there is no `Register` at all — `Mount` takes a concrete `*http.ServeMux` (`crud/http/crudnet/handler.go:149`) and a chi or gorilla user registers the ten route methods one by one, which the package doc tells them to do (`crud/http/crudnet/handler.go:29-31`). 3, 4 and 5 hold: `crud/http/crudnet/middleware.go:58` is the double-install guard, and the marker is the response writer rather than anything on the fault because a fault is a value two goroutines may render at once ([[D-042]]); the 401 shares the status table by construction, which is the whole of [[D-059]]. **2 does not hold on any binding, and the doc comment says the opposite.** Each `Errors` builds a renderer from its options and then renders only what nobody else wrote: `crud/http/crudnet/middleware.go:77` (`rec.err != nil && !rec.wrote`), `crud/http/crudgin/middleware.go:55` (`c.Writer.Written() || len(c.Errors) == 0`), `crud/http/crudfiber/middleware.go:41` (`c.Next()` returns nil). Every CRUD route writes its own failure through `h.fail` (`crud/http/crudnet/handler.go:477`), so `crudnet.Errors(crudhttp.WithMessages(cat))` configures the hand-written routes and does nothing at all for the ten routes it is mounted over — while `crud/http/crudnet/middleware.go:44-46` reads "It covers this binding's own routes too … so mounting it over a mux carrying both CRUD routes and hand-rolled ones is one call." Fiber has a **second** seam with the same gap and a stronger promise: `crudfiber.ErrorHandler` (`crud/http/crudfiber/middleware.go:50-56`, "Every handler in the app then answers failures the way the CRUD routes do") never sees a CRUD failure at all, because `build` installs an error handler that writes and returns nil (`crud/http/crudfiber/handler.go:124`). 6 fails on Fiber under `Register`: only `Routes()` sets the app's `BodyLimit` to this handler's cap (`crud/http/crudfiber/handler.go:136-139`), and `Register` cannot — the limit belongs to the caller's app — so a body above the app's limit is Fiber's plain-text 413 and one below it is ours. [[FL-013]] states it outright.
**If not ready:** For 2 the consumer passes the same renderer again, per resource, as `WithRenderer` — and then pays H-CRUDHTTP-08's hop loss for it. The close is to let the middleware's render options be the default the routes compose onto, which is a change to `build`'s inputs and not to the middleware, plus a correction to two doc comments that currently teach the failure. For 6 the honest close is a sentence on `Register`'s doc comment telling the consumer to set their app's `BodyLimit` to the handler's cap plus one.

### H-CRUDHTTP-11 — A rule that has to run before the write
**Who:** a developer with a per-plan quota and an audit column to stamp
**Wants:** to refuse a create over quota, and to set `CreatedBy` from the principal rather than from the body.
**Story:** They add a before-save hook that reads the principal out of the request, sets the field, and returns an error when the quota is spent. They send a request over quota and look at the status.
**Must hold:**
1. The hook is handed the framework's own request, so the principal, the trace id and a path value are all reachable from it.
2. A mutation it makes reaches the database, and is not undone by the server-owned clearing.
3. Its refusal reaches the client as the status that refusal deserves.
4. The same is available on patch, with the id from the URL.
**Today:** 🟡 partial — 1, 2 and 4 hold; 3 holds for a sentinel and traps everyone else
**Evidence:** 1 is this module's own and is the reason the hook signatures differ per binding (`func(*http.Request, *M) error`, `func(fiber.Ctx, *M) error`, `func(*gin.Context, *M) error` — `docs/api/surface.md:397`, `:775`, `:806`, and `context.Context` on gRPC at `:846`). 2 and 4 are pinned at `crud/http/crudnet/options_test.go:118` and `:158`, and the *ordering* that makes 2 true is `port`'s: `port/service.go:152-164` runs `Sanitize` then `cmd.Before` then `Save`, with the comment saying a hook that ran before the clearing would be handed a client-chosen key and a forged timestamp. 3 works for every exported sentinel — `crud/http/crudnet/options_test.go:142` `TestBeforeSaveCanRefuseTheRequest` asserts `crud.ErrForbidden` is a 403 and the repository is never called. What traps is anything else: the status comes from the error's kind through `port/porthttp/errors.go:51` `StatusFor`, whose default arm is 500 with no body, so a hook that returns `fmt.Errorf("over quota")` — the first thing anyone writes — is an opaque 500. Nothing on the option, in its doc comment, or in any module page's option table says which errors map to what.
**If not ready:** The consumer has to find `errs.Validation()` / `port.BadRequestf` on their own, from the errs page. One sentence on each hook's doc comment and one row in each module page's option table closes it.

### H-CRUDHTTP-12 — The write and the row beside it
**Who:** a developer whose create has to stamp an audit entry, or whose order has line items
**Wants:** both writes to land, or neither.
**Story:** They add a before-save hook, write the audit row from it, and then kill the database mid-request to see what is left behind.
**Must hold:**
1. A write and the work a hook does around it are one transaction, or the module says plainly that they are not.
2. `PUT /{id}` — a read followed by a write — does not leave a window where another writer changes the row between the two.
3. There is a documented way to hand the handler a service that owns its own transaction.
**Today:** ❌ missing
**Evidence:** nothing in these four packages opens a transaction. `grep -rn 'Begin\|Transact' crud/http/ port/` outside tests comes back empty, and `crud.BeginnerOf` (`crud/executor.go:254`) — the seam that exists for exactly this — is reached from nowhere in the transport or the service. `port/service.go:190-196` is the unguarded read-then-write in `Replace`. `BeforeSave` receives `*M` and the framework's request and nothing that could enrol in a transaction. No module page and no case in this file says so, so a reader who finds the hook concludes it is transactional.
**If not ready:** The answer that exists is `Serving` over a service the consumer assembled, which begins its own transaction through `crud.BeginnerOf` and calls the repository inside it. That is the right answer and it appears nowhere: not in `docs/usage-guides/`, not in `_examples/`, not in this file until now. Writing it down is the cheapest release-blocking fix on this page. Making the handler itself transactional is not: it would put a transaction boundary in a transport, which is what [[D-045]] exists to stop.

### H-CRUDHTTP-13 — The same request, three routers
**Who:** a team running a public API on Fiber and an internal admin tool on `net/http`, with a shared client library
**Wants:** one service value serving both, and one client that does not need to know which door it hit.
**Story:** They build the service once, mount it on both, and diff a response. Then their uptime monitor sends `HEAD /articles` and their client library sends a verb the resource does not carry.
**Must hold:**
1. The repository or service value satisfies all the bindings without a wrapper, and choosing Gin does not pull Fiber into the build.
2. The same request answers the same status and the same bytes on each.
3. A verb the resource does not carry answers the same way on all three, or the difference is written where a consumer will find it.
4. `HEAD` on a mounted collection answers what `GET` answers, without a body.
**Today:** 🟡 partial
**Evidence:** 1 holds and is the part no other sweep owns: `crudnet` imports only the standard library and lives in the library's own module, so it costs a consumer nothing ([[D-033]], [[D-016]]); the type identity is `port.Repository` behind generic aliases ([[D-022]]). 2 is measured here, and measured narrowly: `test/integration/http_port_test.go:121` compares one `GET /articles/{id}?preload=author` byte for byte and sends two creates, one refused and one allowed. It never sends a trailing slash and never sends an unmounted verb, which are the two requests where the three are known to disagree. 3 is a real three-way difference and is **documented**: `crudnet` and `crudfiber` answer 405, Gin answers 404 unless the application sets `Engine.HandleMethodNotAllowed` — [[FL-013]]'s table has the row at `:105`, and so does the Gin module page in both languages (`docs/modules/en/crudgin.md:139` and `:153`, `docs/modules/ru/crudgin.md:143` and `:157`). What the tests do not cover is the request the difference is about: `TestReadOnlyMountsOnlyTheReadRoutes` (`crud/http/crudnet/options_test.go:232` and its two twins) sends `DELETE /widgets/42` on a `ReadOnly` handler, never an unmounted verb on a fully mounted one, and the Gin twin passes only because its fixture flips the flag (`crud/http/crudgin/fake_test.go:258`). **4 fails on Gin alone, and is in no difference table at all.** `net/http` matches `HEAD` against a `GET` pattern by construction, and Fiber v3 auto-registers a HEAD route for every GET unless `DisableHeadAutoRegister` is set (`gofiber/fiber/v3@v3.4.0/router.go:752-795`); Gin's `r.GET` registers `GET` only (`crud/http/crudgin/handler.go:153-156`), so a health check that works in development against `crudnet` is a 404 in production on Gin.
**If not ready:** For 4, one `r.HEAD("", h.List)` beside each Gin GET registration, or a row in [[FL-013]] and the Gin page saying it is the application's job — one line either way, and it has to be one of the two before the tag. For 2, either the integration test grows the trailing slash and the unmounted verb as *asserted differences*, or the claim is narrowed to "the same status and bytes on every route the routers agree exist". The second is honest and the first is better.

### H-CRUDHTTP-14 — The 3am question: why was that a 500?
**Who:** whoever is on call
**Wants:** to know what the failing request actually hit, and to trust the number on the 500 graph.
**Story:** An endpoint starts answering 500. The body says nothing, correctly. They go to the logs. Then they notice the 500 rate rose at the same time the load balancer's timeout was lowered.
**Must hold:**
1. The cause of a 500 is recorded somewhere the operator can reach.
2. The record carries the method, the path and the status, so it can be correlated without relying on the application having installed a request-scoped logger.
3. Whatever the answer is, it is the same on all three bindings, or the difference is written down.
4. A client that hung up, and a statement that ran past its deadline, are not counted as server faults.
**Today:** ❌ missing for the failure that matters most
**Evidence:** two of the three 500s an operator meets are already logged, on all three bindings, through the seam [[D-062]] describes, and both carry the method and the path: a panic (`crud/http/crudnet/middleware.go:68-74`, `crudfiber/middleware.go:32-34`, `crudgin/middleware.go:46-47`) and a response that will not marshal (`crud/http/crudnet/options.go:205-213`, `crudfiber/options.go:171`, `crudgin/options.go:173`). The one that is dropped is the third and the common one — the repository or driver error: `port/porthttp/render.go:130-131` returns `Internal()` and the original never leaves the function, and `crudnet`'s `render` (`crud/http/crudnet/options.go:174-194`) and `crudfiber`'s write and return with no log line and no seam. Only Gin keeps it — `crud/http/crudgin/options.go:201` calls `c.Error(err)`, so Gin's own logging middleware sees the cause. The middleware cannot fill the gap because the CRUD routes write their own failures (H-CRUDHTTP-10, evidence 2). [[FL-013]]'s difference table has a row for the Content-Type and for the double-install marker and none for this. 4 is worse: `grep -rn 'context.Canceled\|DeadlineExceeded'` over `port/`, `errs/` and `crud/` outside tests is empty, so a cancelled request falls to `StatusFor`'s default arm and is a 500 like any other. Behind a load balancer that trims slow requests, the 500 rate is then partly made of browsers closing tabs, and nothing separates them.
**If not ready:** Today the consumer writes, per resource and per binding, `WithErrorHandler[Article, int64, ArticleUpdate](func(w, r, err) { if crudnet.Status(err) >= 500 { log(err) }; crudnet.DefaultErrorHandler(w, r, err) })` — and pays for it with the path hops, exactly as `WithRenderer` does (H-CRUDHTTP-08). The close for 1–3 is one line in each binding's `render`, `port.Logger(ctx).Error("…", "method", …, "path", …, "status", …, "err", err)` when the status is 500, spelled the way the panic sites already spell it, plus a row in [[FL-013]]. The close for 4 is a `KindCanceled` in `errs` and a row in the status table — `errs`/`porthttp` work this module only consumes, so it is carried, not owned.

### H-CRUDHTTP-15 — The frontend already has a wire contract
**Who:** a developer converting a deployed service one resource at a time
**Wants:** the new endpoint to answer the shapes the existing clients already parse.
**Story:** Their API has answered `{"data": [...], "meta": {...}}` for three years, `204` on delete, and a `Location` header on create. They mount resource one and diff it against what the mobile app expects. Their web client is JavaScript and their key is a snowflake id.
**Must hold:**
1. The item shape is theirs, on the entity routes and inside the page alike.
2. The page envelope is theirs too, or there is a seam to make it so.
3. The other success shapes — what `DELETE`, `POST /bulk-delete` and `/count` answer — are theirs, or there is a seam.
4. A create answers with the location of the thing it created.
5. A key that does not fit a double survives the round trip through a browser client.
**Today:** 🟡 partial
**Evidence:** 1 holds — `WithTransform` runs on every read shape and on writes (`crud/http/crudnet/options_test.go:38` and `:99`). 2 has no seam at all: the page is `crud.PaginatedResponse` marshalled directly (`crud/http/crudnet/options.go:204-221`), and its keys are struct tags on a type in the core package (`crud/page.go:5-19`). `WithRenderer` is the *error* envelope and reaches nothing on the success path. `crud/http/crudnet/handler_test.go:188` pins the seven keys, which is the right thing to pin and also the proof they are not negotiable. 3 has no seam either, and there are three more fixed shapes than blocker 14 suggests: `DELETE /{id}` is 200 with `{"deleted": n}` (`crud/http/crudnet/handler.go:381`) rather than 204, `POST /bulk-delete` is 200 with the same key (`:403`), and `/count` is `{"count": n}` (`:255`) — a different key from delete's, which a client-library author needs told and can learn from no module page. 4 has nothing: `grep -rn "Location" crud/http/` outside tests is empty, and `Create` answers `h.entity(w, r, http.StatusCreated, m)` (`crud/http/crudnet/handler.go:308`) with no header. 5 fails silently: the key is marshalled by `encoding/json` as a JSON number, so an `int64` above 2^53 loses precision in every browser client — a wrong id, with a 200, and no error anywhere. The only lever is one hand-written `WithTransform` per resource, for a property the library could decide once from `Meta`.
**If not ready:** Resource one either changes the wire contract for every existing client, or is written by hand — which is the thing the module exists to avoid, on the exact consumer H-CRUDHTTP-09 describes. The close for 2 and 3 is one option beside the entity presenter: `func(*http.Request, crud.PaginatedResponse[M]) any` for the page, and a small enum or a second function for the count and delete shapes. Small, per binding, and it has to land on all four transports. For 4, one `w.Header().Set("Location", …)` in `Create`, which needs the mounted prefix and therefore a field on the handler. For 5, a decision: either a `Meta`-driven string encoding for wide integer keys, or a documented instruction to use a string key.

### H-CRUDHTTP-16 — Infinite scroll, and a table too big to count
**Who:** a developer with ten million rows and a mobile client
**Wants:** a page that does not run `COUNT(*)`, and a next-page token that survives a concurrent insert.
**Story:** They add `?skipTotal=true` and read the response. Then they take `nextCursor` off the page and send it back as `?after=`.
**Must hold:**
1. `skipTotal` skips the count and the page still says whether there is more.
2. A client can tell "this page was not counted" from "there are twenty rows", so a generic pager does not print a wrong total.
3. A page carries a cursor a client can hand straight back.
4. A cursor refused because of the endpoint's own allow-lists is a 400 that says which column and what to do.
**Today:** 🟡 partial
**Evidence:** 1 is true and is **not pinned through a binding**: `crud/http/crudnet/handler_test.go:490` `TestListHonoursUnpagedAndSkipTotal` asserts only that three booleans arrived on the fake's `crud.Options`, and would pass unchanged if the count still ran; the envelope test at `:188` reads a canned page from the fixture and never sends `skipTotal`. The guarantee lives two layers down and neither place is reachable from an HTTP test: `crud/sqlrepo/repository_test.go:148` `TestSkipTotalProbesOneExtraRow` (which asserts no COUNT ran) and `test/integration/suite.go:289-296`. **2 fails.** With the count skipped the response reports the page's own length as the total and zero pages: `crud/sqlrepo/repository.go:193-197` builds `crud.NewPaginatedResponse(items, page, limit, int64(len(items)))` and then sets `resp.TotalPages = 0`. So a ten-million-row table answers `{"limit":20, "total":20, "totalPages":0, "hasNext":true}`, and any client rendering "N results" off the envelope H-CRUDHTTP-01 pins key by key prints 20. 3 and 4 exist in the DSL and are pinned nowhere above the repository: `crud/query/request.go:47-50` (`after`/`before`), `crud/query/querystring.go:204`, `crud.PaginatedResponse.NextCursor` at `crud/page.go:14-19`, carried through a presenter by `MapPage` at `crud/page.go:61`. The refusal in 4 is a good one and the consumer will hit it first — a cursor compares the sort tuple, so a column that is `Sortable` and not `Filterable` is refused by name (`crud/query/compile.go:373-380`, with the reasoning in [[D-028]]). No test in `crud/http/*` sends `after=` or reads `nextCursor`; the cursor tests are all at the repository and DSL layer (`test/integration/cursor_test.go`). The endpoint-ceiling question is H-CRUDHTTP-02's must-hold 4 and is not counted again here.
**If not ready:** For 2 the honest close is a wire decision: either `total` and `totalPages` are omitted when the count was skipped — which `omitempty` on two fields would do and which changes the envelope H-CRUDHTTP-01 pins — or a `counted: false` key. Either way it is [[D-063]]'s question in a new place: there has to be a spelling for "not measured" that is not a number. For 1, 3 and 4 nothing needs building: what is missing is a test through a binding and a paragraph in each module page's routes table, because a client library author reading the page today has no way to learn that `?after=` exists.

### H-CRUDHTTP-17 — `/authors/{id}/articles`
**Who:** anyone whose API has a parent and a child
**Wants:** the sub-collection to list the parent's rows, and a POST into it to belong to that parent.
**Story:** They mount the articles handler under `/authors/{authorID}/articles`, read the parent id out of the request in a scope function, and then try to create.
**Must hold:**
1. `GET /authors/7/articles` lists only author 7's rows, and its `total` agrees.
2. `POST /authors/7/articles` sets `AuthorID` to 7 whatever the body says.
3. `DELETE /authors/7/articles/9` refuses when article 9 belongs to author 8.
4. The parameterised prefix survives mounting on all three bindings.
**Today:** 🟡 partial — 1 works, 2 has an undocumented answer, 3 has none, 4 is untested
**Evidence:** 1 is what `WithScope` is for and it is the one thing it does (`crud/http/crudnet/handler.go:412`). 2 and 3 are H-CRUDHTTP-03's gap with a second face and not a second gap: `Save`, `Update` and `Delete` take no options, so no per-request narrowing reaches a write. What is genuinely this case's is that **`security.Gate` cannot rescue it here**, which it can in H-CRUDHTTP-03: the parent id is in the URL and not in the principal, and the principal is the only thing the gate reads (`crud/decorators/security/security.go:677-712` narrows from `g.scope(ctx)`). So 2 has to go through `BeforeSave`, reading the path value off the framework's request — which no case, test or example in the tree demonstrates — and 3 has nothing at all. 4 is untested: every `Mount`/`Routes` call in the test tree and in `_examples` uses a literal prefix, and Fiber's mount spelling is `app.Use(prefix, h.Routes())`, where a parameter in the prefix is not something this library has ever exercised.
**If not ready:** This is the second shape any CRUD API reaches for, and the module's answer today is a scope function for the reads plus a hand-written `BeforeSave` for the create plus a hand-written route for the delete. A release sweep should at minimum establish (4) with a test in each binding, and write (2) down as the pattern — a worked sub-collection in `_examples` would carry both.

### H-CRUDHTTP-18 — Everything except DELETE
**Who:** an API author whose delete is admin-only and whose bulk delete is not public at all
**Wants:** to mount nine of the ten routes.
**Story:** They read the option list looking for a route switch. Then their gateway team asks why a read-only resource still answers two POSTs.
**Must hold:**
1. A resource can expose an arbitrary subset of the ten routes.
2. Doing so does not mean re-deriving the route table by hand.
3. What `ReadOnly` mounts is what a reader of the name would predict.
**Today:** ✅ closed by [[D-113]]
**Evidence:** `port.Rules.Expose` is a `port.Operations` bitmask over the ten routes, each binding sets it with `Exposing(ops)`, and `crudhttp.Table` carries the same field so the access declaration is derived from the set the router is walked with. 1 and 2 hold together: `Reads|Deletes` mounts seven routes without re-deriving the `/count`-before-`/:id` ordering, pinned as `TestExposingMountsExactlyTheOperationsItNames` in all three bindings. 3 is answered rather than fixed — `ReadOnly` still mounts the same five, POSTs included, because it is public and consumers spell it — but the consumer who did not want those two POSTs now has `OpList|OpGet|OpCount`, pinned as `TestExposingCanDropTheReadPosts`. Naming `ReadOnly` and `Expose` together is a start-up panic (`TestReadOnlyAndExposingTogetherIsRefusedAtDeclaration`). gRPC has no `query` or `count-query` method, so those two bits mount nothing there; the row is [[FL-013]]'s.
**Was:** `ReadOnly` is the only route switch and it is all-or-nothing — five routes on, five off, not four and six: outside the two guards sit `POST /query`, `GET /count`, `POST /count`, the collection `GET` and `GET /{id}`; inside them sit the collection `POST`, `POST /bulk-delete`, `PATCH`, `PUT` and `DELETE` (`crud/http/crudnet/handler.go:161-178`, `crudgin/handler.go:147-162`, `crudfiber/handler.go:154-169`, and the routes table at `docs/modules/en/crudnet.md:41-51`). That is also 3's answer: **two of the five routes a `ReadOnly` resource keeps are POSTs.** A consumer behind a gateway rule of the form "POST requires a write scope", or a CDN that will not cache POST, gets two routes they did not expect and cannot remove without hand-registration. The option list has nothing else (`docs/api/surface.md:395-406`).
**What it cost:** one field on the shared struct and one option per binding, which is the shape this entry proposed. The alternative it rejects is written down in [[D-113]]: a blocklist composes better and fails open the day an eleventh operation exists. Before it, the consumer registered the exported handler methods one by one — on `crudnet` that is honest work — every route method is an `http.HandlerFunc` and the package doc says to do it (`crud/http/crudnet/handler.go:29-31`). On Gin and Fiber it means re-deriving the `/count`-before-`/:id` ordering that `Register` exists to get right, and it surrenders the one-statement mount the whole DX rests on. The shape that fits is a field on the rules — an allowed-verb set, defaulting to all ten — rather than eleven more options; and the `ReadOnly` route table belongs in each module page whichever way that goes.

### H-CRUDHTTP-19 — What does the frontend send?
**Who:** the frontend developer who was handed twelve endpoints
**Wants:** to generate a client, or at least to read one document.
**Story:** They ask which fields are filterable on `/articles`, how a sort is spelled, what a preload object looks like, and what the page keys are called.
**Must hold:**
1. Something machine-readable describes the mounted resources.
2. It is derived from the same declaration the routes are, so it cannot drift.
**Today:** ❌ missing
**Evidence:** no OpenAPI document, no JSON schema, no descriptor endpoint anywhere in the four packages (`docs/api/surface.md:379-409` and the crudfiber/crudgin sections). The module pages carry a routes table — `docs/modules/en/crudnet.md:41-51` is a proper `| Route | Does |` table — and it is *identical for every resource*, which is the point: no formatting of a table that cannot vary by resource can answer "which fields are filterable on `/articles`". Those allow-lists live in `query.Config` (`crud/query/compile.go:70-81`) and are visible to no client. The material to build from exists and was put there on purpose: `port/service.go:20-25` says `Meta()` is on the interface because it "is what makes a Service self-describing for a transport that has to name its resource". [[D-052]] records the same absence for gRPC as a deliberate choice for that transport; HTTP has no such decision and no case had asked the question.
**If not ready:** Twelve resources is twelve hand-written client contracts kept in step with `query.Config` by memory. Whether shipping a schema is in scope for this tag is the owner's call — but it should be a decision doc either way, because the next consumer will ask and the answer should not be "nobody had thought about it".

### H-CRUDHTTP-20 — The search box
**Who:** the same public-API author from H-CRUDHTTP-02, adding a search field to the listing page
**Wants:** `?q=bolt` to match the two columns a human would search, and nothing else.
**Story:** They allow-list `Filterable` and `Sortable`, ship, and add `?q=` to the frontend. Later someone asks what `?q=` actually searches.
**Must hold:**
1. A search touches only columns the endpoint named, the way a filter does.
2. A search term that resolves to no searchable column is refused, not dropped.
3. The refusal names what was wrong, the way an unknown filter path does.
**Today:** 🟡 partial — the feature works; the bounding does not. This is the third exposure axis beside filter and sort, and the only one with no case, no warning and no line in any option table
**Evidence:** `?search=` (alias `?q=`, `crud/query/querystring.go:206`) is compiled by `crud/query/compile.go:569`. **1 fails by default.** With `Searchable` unset, `allowed` returns true for everything (`crud/query/compile.go:210-215`), and the no-field-list branch walks *every string field of the root model* and ORs a `LIKE` over each (`:576-586`). On the H-CRUDHTTP-04 resource — the one with `PasswordHash` and `InternalNotes` — a client that allow-listed `Filterable` and `Sortable` and forgot `Searchable` has handed the internet a prefix probe over the hash column. [[D-060]] makes that consistent (every bound on what a request may *name* is open by default) and the library's own example shows the safe spelling (`_examples/sqlx-pgx-gin/main.go:91` sets `Searchable`), but nothing warns and nothing defaults. **2 fails and is the worse half.** When nothing matches, `search` returns `nil, nil` (`:583-585` for the implicit path, `:624-626` for the explicit one when every named field is non-text and the term does not coerce), and `compile` appends a predicate only when it is non-nil (`crud/query/compile.go:266-273`). So the endpoint answers the whole collection with a 200 — which is exactly the failure `Request.UnmarshalJSON` calls out one file over as "the one failure a client cannot see" (`crud/query/request.go:76-83`, [[D-013]]). 3 works on the explicit path only: a named field outside `Searchable` is `errf("searchFields", "%s is not searchable", …)` at `crud/query/compile.go:606`.
**If not ready:** The consumer sets `Searchable` and hopes the next person copying the config keeps it. Two closes, both small. The drop is a bug, not a policy: an empty predicate set should be `errf("search", …)` naming the term and saying no searchable column matched, and it costs two `return nil, nil` sites. The default is a decision — either `Searchable` gains an explicit-only rule, which contradicts [[D-060]]'s "open by default for names" and must be argued as a challenge to it, or the option tables and the usage guides say plainly that an unset `Searchable` searches every text column.

### H-CRUDHTTP-21 — The list with its author embedded
**Who:** the frontend developer's backend counterpart, replacing a screen that made twenty-one queries
**Wants:** `GET /articles?preload=author,comments&select=id,title` to answer one page in a bounded number of statements.
**Story:** They add `?preload=author` and watch the query log. Then they add `?select=` to trim the payload, and notice the presenter they wrote stopped producing a display name.
**Must hold:**
1. A preload is a bounded number of statements, not one per row.
2. A preload survives the presenter and the page envelope.
3. `?select=` and a presenter either compose, or the endpoint refuses the combination.
4. The endpoint can say which relations a client may preload, and which columns it may select.
**Today:** 🟡 partial — 1, 2 and 4 hold and are the strongest thing on this page; 3 is a silent wrong answer
**Evidence:** 1 is [[D-006]] and is the reason to take this library rather than write the SELECT: one statement per relation per level, parent keys chunked into `IN (…)` lists, children indexed by owner key. It reaches the transport untouched — `crud/http/crudnet/handler_test.go:139` `TestQueryBodyCompilesTheWholeDSL` compiles two preloads including a per-relation filter and asserts the filter compiled against the *related* model, and `crud/http/crudnet/handler_test.go:281` sends `?preload=owner&select=name` through `GET /{id}`. 2 holds through `MapPage` (`crud/page.go:48-64`) and through `entity` (`crud/http/crudnet/handler.go:469-475`), and `test/integration/http_port_test.go:121` is a byte-for-byte comparison of a preloaded entity across bindings. 4 holds: `Preloadable` and `Selectable` are allow-lists like the others, and an unknown path on either is a 400 naming it (`crud/http/crudnet/edge_test.go:157`). **3 is the trap.** `Selectable` is empty by default, meaning anything the model maps (`crud/query/compile.go:70-71`), so any client may send `?select=id`; the unselected fields of the returned `M` are then zero values, and `WithTransform` is handed that `M` with no way to tell "the column is empty" from "the column was not asked for". A presenter composing a display name from two columns returns empty strings, with a 200, and neither the presenter's signature nor its doc comment mentions the interaction. No test anywhere combines `?select=` with `WithTransform`.
**If not ready:** The consumer sets `Selectable` to nothing they present from, or drops the presenter. The cheap close is a sentence on `WithTransform`'s doc comment and a row in each option table: a presenter and `?select=` are mutually exclusive unless the presenter tolerates zero values. The honest close is for the handler to refuse `?select=` when a presenter is installed — which is [[D-021]]'s shape but at request time rather than at declaration, so it is a decision.

### H-CRUDHTTP-22 — Which fields may a client write
**Who:** the reviewer on the pull request that adds `POST /users`
**Wants:** to see, in one place, the list of columns a request body can set — and to refuse a body that is wrong before it reaches the database.
**Story:** They read the route, ask who can set `role`, and then ask what happens when the email is missing and the age is negative on the same request.
**Must hold:**
1. There is one declaration that says which columns a client may write, and it is not "whatever the model has".
2. A field the server owns cannot be set through any of the three write routes.
3. A consumer's own validator can refuse a body with several field errors at once, in the same envelope the database's violations come back in.
4. Those violation paths get the same treatment as the database's: named the way the client sent them.
**Today:** 🟡 partial — 2 holds for four kinds of column and nothing else; 1 and 3 have no seam and no documentation
**Evidence:** 2 holds for the key, the `generated`, the `immutable` and the `version` columns, and only for those: `internal/codegen/codegen.go:265-280` reads the tags and `:47` `tagDropped` is the whole rule, so the update DTO carries every other column, and `port.Sanitize`/`ClearGenerated` (`port/service.go:155`, `:198`) clear the same four on create and replace. **1 has no seam at all.** Any caller allowed to `PATCH` may set `role`, `status`, `published` and `price`, and the only lever is `BeforeUpdate(func(*http.Request, ID, *U) error)` inspecting the DTO field by field — which no case, example or module page shows. H-CRUDHTTP-06's evidence is the near miss that makes this worth stating: `{"tenantId": 9}` is harmless *because the generator omitted an immutable column*, which reads as though the DTO were an allow-list. It is one only for those four kinds. **3 has no HTTP-shaped answer and the machinery exists:** `errs/bridge.go:24` `FromFieldViolations` was written for exactly this shape. Under `New` there is no seam over the wire body at all — `BeforeSave` receives `*M` after the mapper ran (`crud/http/crudnet/handler.go:298-303`) — so validating what the client actually sent means using `NewFor` and returning the fault from `Mapper.Model`; on `PATCH` it is `BeforeUpdate` over `*U`. 4 is unknown: nothing exercises whether a violation raised by a hook or a mapper gets the mapper hop and the raw-body fallback the way a driver violation does (H-CRUDHTTP-08), so a consumer's own validation errors may come back naming Go fields.
**If not ready:** The consumer marks the columns `immutable` and regenerates — which also stops the *server* writing them, so it is the wrong tool whenever the server sets the field and the client must not. Otherwise it is a hand-written `BeforeUpdate` per resource. The cheap close before the tag is documentation: one worked example of validation through `NewFor` and `BeforeUpdate` in `_examples`, and a paragraph in `docs/modules/en/crudhttp.md` saying what the DTO does and does not bound. A `Writable` allow-list on the rules is the shape that would close 1 properly, and it is a decision because it duplicates a job the tags already half do.

### H-CRUDHTTP-23 — Soft delete, the trash screen, and the restore button
**Who:** an admin-tool developer whose articles are soft-deleted so an editor can undo a mistake
**Wants:** DELETE to stamp, the listing to hide the row, a trash view to show it, and a restore to bring it back.
**Story:** They declare `SoftDelete("DeletedAt")` on the repository, delete a row, and look for the two screens their product manager asked for.
**Must hold:**
1. `DELETE /articles/9` stamps rather than removes, and every read then hides the row.
2. There is a way for an authorised caller to list the deleted rows.
3. There is a way to restore one.
4. Whatever route deletes a row is the route a deletion goes through — so middleware guarding the delete cannot be walked around.
**Today:** 🟡 partial — 1 holds; 2 and 3 do not exist; 4 is broken by the update route
**Evidence:** 1 is `sqlrepo`'s and holds: `sqlrepo.SoftDelete` (`crud/sqlrepo/blueprint.go:111`) turns `Delete` into a stamp (`crud/sqlrepo/repository.go:887-888`) and folds a "not deleted" predicate into the permanent scope (`crud/sqlrepo/blueprint.go:198-206`). 2 and 3 have nothing: `grep -rn "IncludeDeleted\|Restore\|WithDeleted" crud/ port/` outside tests is empty, and `WithScope`'s own doc comment offers "an `?includeArchived` flag" as its example (`crud/http/crudnet/options.go:89`) — which reaches reads only, so a flag can show the row and nothing can bring it back. **4 is the finding that belongs to this module and appears in no other sweep.** The tombstone is an ordinary column: it is not the key, not `generated`, not `immutable` and not `version`, so `tagDropped` keeps it and it lands in the update DTO (`internal/codegen/codegen.go:47`; the sqlrepo sweep records the DTO half as its point 7). So `PATCH /articles/9 {"deletedAt":"2026-01-01T00:00:00Z"}` deletes a row through the update route — past whatever middleware or `ReadOnly` split guards `DELETE` — and `{"deletedAt":null}` restores it. The route-level authorization bypass is HTTP's and is written down nowhere.
**If not ready:** The consumer hand-removes the field from the DTO, which panics at start-up unless the exclusion was declared at generation time (`port.MustCoverUpdate`), or writes their own `BeforeUpdate` refusing the key on every soft-deleting resource. The close for 4 is `cmd/vv` treating the declared tombstone the way it treats an immutable column, which needs the generator to know the blueprint's `SoftDelete` setting and is therefore a codegen decision. 2 and 3 are a route pair — `GET /articles?deleted=true` and `POST /articles/{id}/restore` — and that is a wire decision on four transports, so it is the owner's call whether it is in this tag.

### H-CRUDHTTP-24 — The nightly import
**Who:** a developer wiring a data feed: 20,000 rows a night, and a CSV upload screen for the same table
**Wants:** to hand the API a batch rather than a request per row.
**Story:** They look for a bulk create, find `POST /bulk-delete`, and read its shape. Then they look for a way to update every row matching a filter, because the feed marks a whole category as discontinued.
**Must hold:**
1. Creating many rows is one request, or the module says plainly that it is not.
2. Updating many rows by filter is a request, not a loop over ids.
3. A batch that fails partway answers which rows landed.
**Today:** ❌ missing
**Evidence:** the ten routes are the whole surface, and the only plural verb among them is `POST /bulk-delete`, which takes ids only (`crud/http/crudhttp` `BulkDeleteRequest[ID]`, `crud/http/crudnet/handler.go:388-404`) and is capped at `port.DefaultMaxBulk` = 1024 (`port/rules.go:53`, `:61-66`). There is no bulk create, no bulk update, and no delete-by-filter on any of the four transports. 3 has no shape to answer in: the two plural responses on the wire are `{"deleted": n}` and nothing else, and no route returns a per-row result. 20,000 rows is 20,000 POSTs, each with its own round trip, its own body cap check and its own implicit transaction.
**If not ready:** The import job goes back to a hand-written handler that calls the repository directly — which is honest, because the repository has `UpdateAll` and `DeleteAll` and the transport does not expose them. Closing it means widening `port.Service`, which four transports and every consumer-written service sit behind, so it is the largest item on this page and the one most clearly after the tag. What is cheap now is saying so: one line in each module page's routes table that the bulk surface is delete-by-id, and one sentence pointing an import job at the repository.

## The DX this should have

### The call site

```go
// the shortest thing that works
crudgin.New(articles).Mount(r, "/articles")
```

One statement, no type arguments. It is honest about the mount and not about the
afternoon, so count from where a consumer starts. `_examples/sqlx-pgx-gin/main.go`
is four statements and about six concepts before the first curl: the tagged
struct, the `//go:generate` line that produces `U` (`:41`), `sqlrepo.Define` with
three type arguments and a table name (`:56-61`), an adapter over the pool
(`:82`), and then the mount (`:87-93`). The mount really is one statement. The
setup around it is not, and the DX table below counts both.

The decorated version is the first step up, not a different path:

```go
articles := specs.Executor(Articles.Bind(crudsql.Postgres(db), security.Gate(policy)))

crudgin.New(articles).Mount(r, "/articles")
```

The mount spelling is not the same on all three, and the table below counts that
against it: `crudnet` has `Mount(*http.ServeMux, string)` and no `Register`;
`crudgin` has both; `crudfiber` has `Routes()` and `Register` and no `Mount`. A
chi or gorilla consumer registers ten methods one at a time.

### Turning one knob

```go
// one factory per resource; the triple is spelled once, here
var art = crudgin.For[Article, int64, ArticleUpdate]()

crudgin.New(articles,
    art.Rules(shared),                 // the five model-free fields, one value for twelve resources
    art.Query(articleFields),          // the allow-lists, which are this model's and no other's
    art.Transform(publicArticle),
).Mount(r, "/api/v1/articles")
```

`For` is the piece that closes the type-parameter cost, and it closes **all
eleven** options rather than the five a rules struct reaches. `Opts[M, ID, U]` is
an empty generic struct whose methods are ordinary non-generic option builders —
Go allows methods on a generic type, the parameters coming from the receiver —
so `art.Transform(publicArticle)` compiles where `WithTransform(publicArticle)`
cannot. It needs no change to `Option`, no change to `New`, and no existing call
site touched, and `cmd/vv` already writes a file per model, so it could be
generated beside the DTO.

`shared` is `crudhttp.Rules` — which is already exactly the struct twelve
resources want (`port/rules.go:34-41`), five fields, none of them mentioning a
model. It is not re-exported from `crudgin`, `crudfiber` or `crudnet` today; that
is a one-line alias each. `Rules` cannot be passed to `New` directly and cannot
be made to: `Option` is a `func` type (`crud/http/crudnet/options.go:51`), so a
struct value is not an element of that variadic. Putting `Rules` in a second
positional slot instead would change `New`, `NewFor`, `Serving` and `ServingFor`
on four transports — sixteen exported entry points and every call site and
example — which is a pre-tag breaking change and has to be priced as one. The
factory avoids it entirely, and `art.Rules(shared)` reads the same.

`Query` stays on the resource, and that is not an oversight. `query.Config` is
two things in one struct: model-agnostic caps (`MaxDepth`, `MaxConditions`,
`MaxInValues`, `MaxSort`, `MaxPreloads`, `AllowUnpaged`) and per-model allow-lists
of canonical field paths (`crud/query/compile.go:33-81`). Twelve resources cannot
share `Filterable: []string{"Title","Views"}`.

`Scope` is deliberately absent from the block. H-CRUDHTTP-03 calls the same
option a trap, and putting it in the showcase would teach the failure this file
exists to name.

And, for the twelfth resource:

```go
func Group(r gin.IRouter, prefix string, opts ...GroupOption) *Group
func Resource[M any, ID comparable, U any](
    g *Group, path string, repo Repository[M, ID, U], opts ...Option[M, ID, U],
)

api := crudgin.Group(r, "/api/v1",
    crudgin.GroupRules(shared),                        // port.Rules — no model named
    crudgin.GroupScope(tenantOf),                      // func(*gin.Context) ([]crud.Option, error)
    crudgin.GroupErrors(crudhttp.WithMessages(cat)),   // RenderOptions, composed per resource
)

crudgin.Resource(api, "/articles", articles, art.Query(articleFields), art.Transform(publicArticle))
crudgin.Resource(api, "/tags", tags, tag.Rules(crudhttp.Rules{ReadOnly: true}))
```

Three things that block is deliberately showing. `Resource` still takes the
resource's own options, so the third line adds a presenter without leaving the
group — H-CRUDHTTP-09's must-hold 3, which a group that only carried settings
would fail. `GroupScope` is on the group because **`WithScope` names no model**:
its argument is `func(*gin.Context) ([]crud.Option, error)` on Gin and the same
shape on the other three (`crud/http/crudnet/options.go:97`,
`crud/http/crudgin/options.go:97`, `crud/http/crudfiber/options.go:95`,
`crud/rpc/crudgrpc/options.go:86`), so one tenant scope is one value for twelve
resources — which is the first thing H-CRUDHTTP-09's own story says the twelve
have in common. And `GroupErrors` takes `crudhttp.RenderOption`s and not a
`Renderer`, because `Errors` does (`crud/http/crudgin/middleware.go:30`) and
because a group that swallowed a whole renderer would be a bigger version of
H-CRUDHTTP-08's trap.

`Resource` is a free function and not a method on `api`, because Go has no
generic methods and this repository has none: every generic entry point in it is
a free function. Saying so here stops the next reader re-proposing the method
form.

Two knobs this module wants and does not have:

```go
shared := crudhttp.Rules{
    // Page-cap field/name and MaxLimit migration are pending D-060 authority;
    // this binding forwards the selected shared rule rather than choosing it.
    StrictBody: true,                                         // an unknown key in a write body is a 400 naming it
    OnError:    func(ctx context.Context, err error) { … },   // see the failure; do not take over rendering it
}
```

All three belong on `port.Rules` rather than in a binding's option list, and for
the same reason the five fields already there do: none says anything about a
transport, so all four get them from one struct and a group wires them once for
twelve resources.

**`StrictBody` is specified against the decode target, not the model.** That is
the correction this round: the type actually decoded is `In` on create and
replace under `NewFor` (`crud/http/crudnet/handler.go:291-292`) and `U` on `PATCH`
(`:318-319`). Specified against the model it would 400 the H-CRUDHTTP-04
resource's own documented request body — a resource whose whole point is that
the model is not the wire. So the rule is `json.Decoder.DisallowUnknownFields`
against `In`/`U` — the same mechanism `crud/query/request.go:87-89` already uses
for the query document, so [[D-013]]'s argument transfers verbatim — plus a
tolerance for the key, immutable and generated names when the decode target *is*
the model, so a client that GETs a row and PATCHes the whole thing back keeps
working. Two things the implementation will need said out loud: `encoding/json`
reports one unknown key at a time and by Go field name, so the 400's `field` has
to be resolved through the raw-body index (`port/porthttp/bodyindex.go:104`) or
the refusal names a Go field on a resource whose author renamed it; and under
`Serving` the handler has `Meta()` but the DTO shape is the service's, so the
tolerance set has to come from `Meta` and not from the generator.

**`OnError` takes a `context.Context` and not the framework's request.** That is
what lets it live on `port.Rules` at all: one identical signature on all four
transports — including `crudgrpc`, where there is no request object — with
`port.Logger(ctx)` picking up the trace id the application already installed, and
[[D-062]] intact. It fires inside `fail` (`crud/http/crudnet/handler.go:477`, the
single funnel every route uses) before the error handler and unconditionally, so
it observes the failure whether or not the rendering was replaced. **It is scoped
to route failures and does not try to be the only hook.** Two of the three 500
shapes an operator meets never reach `fail`: a response that will not marshal
becomes a 500 inside `writeJSON` (`crud/http/crudnet/options.go:205-213`), which
`Delete`, `BulkDelete` and `count` reach directly, and a panic becomes a 500 in
the middleware (`crud/http/crudnet/middleware.go:68-74`). Both already log
through `port.Logger` with the method and the path, so `OnError` covering the
third is the whole of what is missing — but the option's doc comment has to say
which of the three it sees, or a consumer wires it and believes they are covered.

`OnError` is the second answer to H-CRUDHTTP-14 and not the first. The first is
one line inside each binding's `render`: `port.Logger(ctx).Error("…", "method",
…, "path", …, "status", …, "err", err)` when the status is 500, spelled with the
method and the path explicitly the way the panic sites already spell them
(`crud/http/crudgin/middleware.go:46-47`) — because the context carries a route
or a trace id only if the application installed a request-scoped logger, and
under the default logger a bare cause is a line with nothing to correlate on. It
is wired once per application rather than once per resource per binding, it needs
no new surface on four transports, and [[D-062]]'s own doc comment already
describes this exact case as belonging on its list. `OnError` earns its place
only if a consumer needs the fault *value* rather than a formatted line — a real
want, and a separate question from the blocker. If it ships, it should carry the
status as an argument, or a consumer who wants only 500s re-derives it with
`crudnet.Status(err)`.

### Why this shape

Four candidates, and the fourth is the one this file did not consider last round.

**A method chain.** A method infers its receiver's type parameters, so the three
names are spelled once. It costs three things. `New` builds the service from the
options it is handed (`crud/http/crudnet/handler.go:89-92`), so a setter called
afterwards arrives too late — the chain must either defer building until
`Mount`/`Register`, or build eagerly and rebuild in place. **Deferring breaks the
path `crudnet`'s own package doc recommends to chi and gorilla users**
(`crud/http/crudnet/handler.go:29-31`), who never call `Mount` and register
`h.List`, `h.Create` and the rest one by one: those handlers would have no
service until a `Mount` that never comes, which is the runtime failure [[D-021]]
says this library must not have. And every setter has to exist four times, once
per transport, kept in step by the triplet rule for as long as the library lives.
And every setter name has to be checked against the ten route methods first —
`List`, `Query`, `CountGet`, `CountPost`, `GetByID`, `Create`, `Update`,
`Replace`, `Delete`, `BulkDelete` — because a type cannot have two methods with
one name. The obvious `.Query(cfg)` collides with the `POST /query` handler on
all three bindings.

**A rules struct in a positional slot.** `crudgin.New(articles,
crudgin.Rules{…}, opts...)` names no type parameters for five of the eleven
settings. It is the cheapest thing to *write* and the most expensive thing to
*land*: sixteen exported entry points across four transports, every call site,
both examples. And it closes five options, not eleven.

**Model-free options.** A second, non-generic option shape would let five of the
eleven be written `crudgin.New(articles, crudgin.Query(cfg), crudgin.ReadOnly())`.
The obstacle is the same one: a non-generic value cannot be an element of a
`func(*options[M,ID,U])` variadic, so this needs `New(repo, opts ...any)` with a
start-up panic on a misapplied option — which is squarely what [[D-021]]
licenses — or a change to `Option` before the tag.

**A per-resource option factory.** `crudgin.For[Article, int64, ArticleUpdate]()`
returns a value whose methods are non-generic builders. It spells the triple once
per resource, closes all eleven options, needs no change to `Option`, to `New` or
to any call site, and is additive — a consumer who never calls `For` sees no
difference. Its cost is a second spelling of every option, which the triplet rule
makes permanent on four transports; that cost is real and is why it is a
recommendation and not an obvious win.

**The recommendation:** the factory for the type-parameter cost, a shared
`crudhttp.Rules` value plus a shared scope for the cross-resource cost, and a
`Group` as sugar over both. Not the chain, and not the positional struct. The
argument that decides it is the cost of four transports, and it has to be applied
to the winner too: `Group` and `Resource` are two more exported symbols per
binding times four, plus identical composition logic in each. The composition
belongs in `crudhttp`, where all three HTTP bindings already share `Rules`, the
`Renderer` alias and the request shapes — that is what [[D-034]] means by "a
binding is a shell" — leaving each binding a thin `Group` over a shared one
rather than a fourth copy of the rules.

**The group's open questions, answered rather than left.** A group sets `Rules`
and a resource sets its own: a resource **narrowing** a group's value is the
ordinary case and is allowed; a resource **widening** one — a larger `MaxBody`, a
larger `MaxLimit`, `ReadOnly: false` under a read-only group — panics at
declaration, because a silent widening is exactly the failure [[D-021]] forbids.
That is the correction to last round, which refused any override and thereby made
the twelve-resource case a start-up panic. And a `Serving`-built resource under a
group carrying a service-shaped rule panics the way `RefuseServiceOptions` does
today (`port/rules.go:91-98`), for the same reason.

**A group's renderer must compose, not replace.** A group carrying render options
would, built the obvious way, re-create H-CRUDHTTP-08's loss for every resource
under it, because `build` uses the hop-aware renderer only when none was set
(`crud/http/crudnet/handler.go:129-131`). The resource-level build has to be
`NewRenderer(WithResolvers(hops...), <the group's options>)`. That is one line
inside `build`, it closes the direct `WithRenderer` case as well, and without it
the group is a bigger version of the trap.

### What it must not break

- **[[D-022]]** — `New` takes an interface, in the first parameter position, and
  infers all three parameters from it. The factory and the group both keep that;
  a `Rules` value in a second positional slot keeps it only if the repository
  stays first, and costs sixteen signatures for the privilege.
- **[[D-045]]** and its predecessor **[[D-034]]** — a binding is a shell over
  `port`, `porthttp` and `crudhttp`. Rules set `port.Rules` fields and nothing
  else. **`OnError` is a `func` field and is therefore the one proposal here that
  has to be argued against that line**: if a callback counts as behaviour, it
  belongs beside the renderer in each binding's private `options` struct, and the
  four-transports argument used against the method chain applies to it too. A
  `func` field also makes `port.Rules` permanently non-comparable — free today,
  since nothing compares it, and irreversible. This is also why H-CRUDHTTP-12's
  answer is a service that owns its transaction and not a transactional handler.
- **[[D-021]]** — a misconfiguration fails at declaration. A resource *widening*
  a group's value, and a service-shaped rule handed to `Serving`, both panic at
  start-up rather than being ignored. A resource narrowing one does not.
- **[[D-063]]** — no spelling for "unbounded". `MaxBody(0)` stays "the default",
  a group inheriting a body cap must not make some route inherit zero, and
  H-CRUDHTTP-16's `total` needs a spelling for "not measured" that is not a
  number.
- **[[D-060]] page-cap authority is pending one explicit migration decision.**
  The current physical `sqlrepo.MaxLimit` clamp is real but unset by default;
  Query proposes route-owned `port.Rules.PageCap`. Crudhttp neither promotes one
  as permanent nor creates a local cap: it forwards and tests the selected shared
  rule, including its default, its relationship to `MaxLimit`, and Remote's
  non-truncation outcome. H-CRUDHTTP-20's proposal to close `Searchable` by default is
  a **challenge** to the other half of this decision and must be argued as one:
  search is a bound on what a request may name, and [[D-060]] says those are
  open. The other half of that case — a search that matches nothing returning the
  whole collection — is a bug and needs no decision.
- **[[D-062]]** — logging the cause of a 500 goes through `port.Logger(ctx)`, and
  the line carries the method, the path and the status because the context may
  carry nothing. Not a challenge; the missing entry on the list its doc comment
  already describes.
- **[[D-013]]** — `StrictBody` extends its argument one route over rather than
  contradicting it: the query document's own keys were closed for exactly the
  reason a write body's keys are still open. It applies to the decode target,
  which is what `DisallowUnknownFields` can see.
- **[[D-042]]** — the probe is advisory, so H-CRUDHTTP-08's "every violation"
  stays opt-in. What changes is that the option tables say so.
- **[[D-008]]** — out of scope is 404 and not 403, which is why H-CRUDHTTP-03's
  must-hold 2 is worded the way it is and why the delete route turning zero rows
  into `crud.ErrNotFound` is right rather than an accident.
- **[[D-006]]** — a preload is a batched second query and cannot be paginated.
  H-CRUDHTTP-21 depends on it and must not propose a per-preload page.
- **[[D-031]]** — soft delete is a statement, not a decorator. H-CRUDHTTP-23's
  close is the generator learning about the tombstone, not a new verb on the seam.
- **[[D-052]]** — records that a gRPC resource carries documents and not a
  schema. H-CRUDHTTP-19 asks the same question for HTTP and it does **not** have
  an answer on record. Whatever is decided, it belongs in a decision doc.
- The triplet rule from `CLAUDE.md` — anything added here is added to all three
  bindings with the same test names, and to `crudgrpc` in its own vocabulary
  where the concept survives. `StrictBody` survives; `OnError` survives with the
  `context.Context` signature; a page presenter survives; `HEAD` and `Location`
  do not, and belong in the HTTP-only half.

## DX verdict

`close` marks work that can land before a tag; `decide` marks work that needs a
decision doc written first.

| What the ideal asks for | Today | Distance |
|---|---|---|
| One statement to mount a resource | one statement, no type arguments — but three spellings: `Mount(mux, …)`, `Mount(r, …)`, `app.Use(prefix, h.Routes())`, and one method at a time on chi | small to live with · small to close |
| One knob turned without ceremony | `crudgin.WithQuery[Article, int64, ArticleUpdate](cfg)` — three type parameters per option, per resource; the `Option` doc offers a helper function per option as the workaround | large to live with · a per-resource option factory to close |
| Settings shared across twelve resources | nothing ships; six lines of generic helper in the application closes the model-shaped half, and nothing closes the group half | small to live with · small to close |
| A presenter that covers reads and writes | one option, entities and pages alike | none |
| A presenter that survives `?select=` | nothing — the presenter is handed zero values and answers 200 | small to live with · one doc sentence to close · a refusal to **decide** |
| The page envelope's own shape | fixed struct tags on `crud.PaginatedResponse`; no seam | large to live with · one option per binding to close |
| The delete, count and create response shapes | `{"deleted": n}`, `{"count": n}`, 201 with no `Location`; no seam for any of them | large to live with · one option plus one header to close |
| A request body of its own | `NewFor` on create and replace; `PATCH` always decodes the generated DTO | small to live with · a fourth constructor's surface to **decide** |
| A bound on which columns a client may write | the four kinds of column the tags already mark, and nothing else; the only seam is a hand-written `BeforeUpdate` | large to live with · a doc paragraph to close · a `Writable` list to **decide** |
| A search bounded like a filter | `Searchable` unset searches every text column, and a term matching none is dropped and answered 200 | large to live with · two `return nil, nil` sites to close · the default to **decide** |
| A page-size ceiling per endpoint | today only the unset repository `MaxLimit` clamp exists, so an unbounded page is marshalled into one buffer; the permanent route/repository authority is pending D-060 migration | large to live with · binding forwards/tests the selected shared rule |
| Refusals a client can act on | status, code, field, locale — nothing to write, until a catalogue and a renamed field are wanted together | small to live with · one line in `build` to close |
| Tenant isolation on the reads and the writes | the gate on the repository, which really does reach `DELETE` and `UPDATE` | none |
| The transport lever that looks like the answer | `WithScope` is reads-only; `GET /{id}` is 404 and `DELETE /{id}` is 200 and deletes; the doc comment says so, the name does not | small to live with · a four-package rename to close |
| See why a request failed | replace the error handler per resource per binding, and lose the mapper's path hops with it | large to live with · **one line per binding's `render`** to close |
| A typo'd key in a write body | nothing — 201 over a wrong row on create, 200 over an unchanged one on patch | large to live with · one decode option to **decide** (default on or off) |
| A transaction around a write | nothing in the module; the answer is a service the consumer assembles, written down nowhere | large to live with · a usage guide and an example to close |
| Nine of the ten routes | `ReadOnly` or hand-registration; and `ReadOnly` still mounts two POSTs | small to live with · a field on `port.Rules` to close |
| Soft delete's second and third screens | no trash listing, no restore, and `PATCH` writes the tombstone | large to live with · a codegen fix to close · two routes to **decide** |
| Bulk work | `POST /bulk-delete` by ids only; no bulk create, no bulk update, no delete-by-filter | large to live with · **large** — `port.Service` is an interface four transports sit behind |
| `HEAD` on a health check | 200 on `crudnet` and `crudfiber`, 404 on Gin, in no difference table | small to live with · one line to close |
| A machine-readable API description | nothing | large to live with · a decision first |

**Overall:** The short path is genuinely short, and the read path is nearly
finished work: one statement mounts ten routes, a preload is a batched second
query rather than an N+1, the page envelope is right, and a refusal names the
field the client sent even when nobody declared a mapping. What is not finished
is the *write side of the request*: nothing bounds which columns a body may set,
nothing refuses a key that is not one of them, nothing opens a transaction, and
on a soft-deleting resource the update route deletes rows past whatever guards
the delete route. Six of the twenty-four cases end in "write the route by hand" —
a public patch shape (H-04), optimistic locking and idempotency (H-07), an
unknown key (H-06), atomicity (H-12), the trash and the restore (H-23), and any
batch at all (H-24). Where customising means abandoning the short path rather
than extending it is the other failure: to see a 500 you replace the whole error
handler, and doing so silently takes the field-name translation with it; the same
trapdoor is under `WithRenderer`, and would be under a group-wide renderer if one
shipped without composing. Four of the top blockers close with a single line of
code or one doc sentence, which is worth saying plainly, because the table above
is what gets read before the tag.

## Release blockers found here

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | A repository or driver 500's cause reaches nobody on `crudnet` and `crudfiber` — no log line, no hook; only Gin keeps it, through `c.Error(err)` | blocker | The first production incident has no evidence, and the workaround silently costs the path hops. One line in each binding's `render`, carrying method, path and status, closes it |
| 2 | Nothing in these packages opens a transaction, and nothing says so — a create and its audit row cannot be atomic, and `PUT` is an unguarded read-then-write | blocker | It is the first question an adoption review asks, the answer that exists (`Serving` over a service using `crud.BeginnerOf`) is written down nowhere, and a reader who finds `BeforeSave` concludes it is transactional |
| 3 | On a soft-deleting resource the tombstone lands in the update DTO, so `PATCH {"deletedAt":…}` deletes a row through the update route — past whatever guards `DELETE` — and `null` restores it | serious | It is a route-level authorization bypass on a supported repository declaration, and the HTTP half of it is recorded in no sweep |
| 4 | `?search=`/`?q=` LIKEs every text column when `Searchable` is unset, and a term that resolves to no column is dropped, so the endpoint answers the whole collection with 200 | serious | The exposure half is a data leak nobody warns about; the drop half is [[D-013]]'s own named failure — "returns the whole table and looks like success" — in the one place the library does it |
| 5 | An unknown key in a write body is dropped: 201 over a row with that column defaulted on create, 200 over an unchanged row on patch — including `{"tenantId": 9}`, so a cross-tenant move is answered with success | serious | It is [[D-013]]'s failure mode one route over, and the create half writes something wrong rather than nothing |
| 6 | `WithRenderer` and `WithErrorHandler` both bypass the hop-aware renderer, so a message catalogue and a client-facing field name are mutually exclusive on a resource — and nothing says so | serious | It bites hardest on the generated adapter, which is the path `_examples/example/blog/vv_gen.go:145` mounts. Shares one fix location with #7: `build`'s renderer selection |
| 7 | Render options handed to `Errors` never reach the CRUD routes on any binding, and both doc comments say the opposite — `crudnet/middleware.go:44-46` and `crudfiber/middleware.go:50-56` | serious | A consumer configures the envelope in the one place that looks global, gets it in none of the places that matter, and the documentation told them it would work. Same fix location as #6 |
| 8 | No effective default page-size ceiling: `?limit=1000000` is served unless the repository declared its currently-unset `MaxLimit`, and the page is marshalled into one buffer before a byte is written | serious | The route-specific consumer need is real; D-060 must select and migrate the shared route/repository authority before this binding forwards and tests it. Until then `Serving` over somebody else's service has no lever and the failure is an out-of-memory kill. Already [[UC-002]] gap 20 — joint with `port` |
| 9 | Nothing bounds which columns a client may write; the update DTO carries every column that is not the key, generated, immutable or version, and the only seam is a hand-written `BeforeUpdate` | serious | "Who can set `role`?" is the first question on every API review, and the answer is in no case, example or module page |
| 10 | `docs/modules/en/crudfiber.md:151` and `docs/modules/ru/crudfiber.md:152` promise XML and form bodies (all three decode JSON only, `crudfiber/handler.go:424`), and `:157-160` promises the retained body is reachable from a hand-written endpoint (`bodyKey` is unexported) | serious | Two wrong sentences on one page, and the page is what a consumer trusts. [[FL-013]]'s difference table already has the right value, so the fix is a copy |
| 11 | A presenter is handed an `M` whose unselected fields are zero when a client sends `?select=`, and `Selectable` is open by default | sharp edge | A display name composed from two columns comes back empty with a 200, and no test anywhere combines the two |
| 12 | `HEAD /articles` is 200 on `crudnet` and `crudfiber` and 404 on Gin, and the difference is in no table | sharp edge | Every health check, CDN and uptime monitor sends HEAD; a consumer who develops on `crudnet` and deploys on Gin loses it silently. One `r.HEAD` line, or one row in [[FL-013]] |
| 13 | `/count`, `/query` and `/bulk-delete` are siblings of `/{id}`, so a slug-keyed row with one of those keys is unreachable and answers the wrong route | sharp edge | H-CRUDHTTP-01 promises the key may be a slug; three of its possible values are silently claimed by the router |
| 14 | With `skipTotal`, `total` reports the page's own length and `totalPages` reports zero | sharp edge | A generic client rendering "N results" off the envelope prints 20 for a ten-million-row table, and cannot tell "not counted" from "twenty rows" |
| 15 | A cancelled or timed-out request is an unclassified 500 | sharp edge | Behind a load balancer that trims slow requests, the 500 rate an operator alerts on is partly browsers closing tabs. **Inherited** — a `KindCanceled` in `errs` and a row in the status table, which is the `errs`/`porthttp` sweep's item |
| 16 | Route selection is all-or-nothing: `ReadOnly` or hand-registering nine methods per binding — and `ReadOnly` still mounts `POST /query` and `POST /count` | sharp edge | "Everything except DELETE" is an ordinary requirement and it surrenders the one-statement mount; and a gateway rule keyed on the verb sees two POSTs on a read-only resource |
| 17 | Three type parameters on every option, on every resource | sharp edge | Thirty-six repetitions across twelve resources, each a chance to paste the wrong DTO. The factory closes it without touching `Option` or `New` |
| 18 | No wire spelling for "the version I edited" and none for "this is the same request again" — no `If-Match`, no idempotency key, and the DTO never names the version column | sharp edge | Two admins editing one row is an ordinary Tuesday and the last save wins; a retried POST from a phone makes a second row, and the answer that exists ("declare a unique index, read the 409") is written nowhere |
| 19 | The success shapes have no seam: the page envelope, `{"deleted": n}`, `{"count": n}`, and a 201 with no `Location` | sharp edge | A consumer converting a deployed service changes their wire contract for every existing client, or writes the route by hand — four fixed shapes, not one |
| 20 | An `int64` key above 2^53 loses precision in every browser client | sharp edge | A wrong id with a 200 and no error anywhere; the only lever is a hand-written presenter per resource for a property `Meta` could decide once |
| 21 | No bulk create or update, and no delete-by-filter — `POST /bulk-delete` takes ids only, capped at 1024 | sharp edge | 20,000 rows is 20,000 POSTs, and closing it means widening `port.Service`, which four transports and every consumer service sit behind |
| 22 | No machine-readable description of the mounted API, and no decision saying that is deliberate | sharp edge | Twelve resources is twelve hand-written client contracts; [[D-052]] settled the same question for gRPC and HTTP has nothing |

## Contested

- **"An out-of-scope or missing `DELETE /{id}` is 200 `{"deleted":0}`, not 404."**
  A reviewer challenged H-CRUDHTTP-03's must-hold 3 on this, and asked for the DX
  table row to be downgraded because a client cannot tell a refused delete from a
  successful one. It is false, and the code says so in three places:
  `port/service.go:217-226` turns a zero-row single-id delete into
  `crud.ErrNotFound` with the comment "the caller named one row, and it was not
  there"; the gate returns zero rows for another tenant's id
  (`crud/decorators/security/security.go:706-712`); and
  `crud/http/crudnet/edge_test.go:390`
  `TestDeletingNothingIs404ForOneRowAndZeroForASet` asserts both halves — 404 with
  `not_found` in the envelope for one row, 200 with `deleted: 0` for a set. The
  bulk route answering 200 with a count is deliberate and now stated in
  H-CRUDHTTP-15. Must-hold 3 is kept, and the tenant row in the DX table is kept
  at "none" for the gate; the `WithScope` trap gets its own row instead, which is
  the half the reviewer was right about.
- **"`HEAD` is 405 on `crudfiber`."** A reviewer reported a three-way divergence.
  Fiber v3.4.0 auto-registers a HEAD route for every GET unless
  `Config.DisableHeadAutoRegister` is set — `gofiber/fiber/v3@v3.4.0/app.go:194-198`
  and `router.go:752-795` — and `net/http` matches HEAD against a `GET` pattern by
  construction. So it is a one-way divergence: Gin alone. The finding is kept and
  narrowed, which makes it sharper rather than weaker — two of three work, so a
  consumer has no reason to suspect the third.
- **"`sqlrepo.MaxLimit` clamps only `Limit`, so `unpaged` walks past it."** Last
  round said so and it was wrong: `crud/options.go:242-247` clamps `Unpaged` down
  to `maxLimit` and says so in its own doc comment at `:238-240`, and
  `crud/sqlrepo/repository.go:161` resolves the limit *before* the cursor branch
  at `:166`, so a cursor page carries the same clamp. H-CRUDHTTP-02's point 5 is
  removed and blocker 8 no longer claims a second, worse gap. The one thing that
  is genuinely unproven is that no test anywhere sends `after=` through a binding,
  and that is now stated in H-CRUDHTTP-16.
- **"Accept-Language cannot hold together with a renamed field."** Two reviewers
  framed the `WithRenderer` collision as breaking the locale. It does not: the
  locale is read in each binding's `render` off the live request, whichever
  renderer is installed, so `Accept-Language` holds unconditionally. What
  `WithRenderer` costs is the path hops, and it costs them only where hops exist —
  `NewFor` with a mapper that implements `errs.Resolver`, or a service that
  declares `Paths()`. Under a plain `New` the hop list is empty
  (`port/service.go:88-95`, `paths` nil unless `port.WithPaths` was passed) and
  nothing is lost. The finding is kept, narrowed, in H-CRUDHTTP-08 and blockers 6
  and 7.
- **The `crudfiber` module page stays "serious".** A reviewer argued it is a
  two-line documentation edit and should rank below the code gaps. It is two
  lines, and this round found a *second* false sentence on the same page — which
  is evidence the page was never read end to end, not that one line slipped. The
  page is a consumer's contract; a wrong option or format on it is a defect by
  this repository's own rule, not untidiness.
- **H-CRUDHTTP-05 is kept as a case rather than folded into the `port` sweep.**
  A reviewer asked for two sentences and a cross-reference. It is shrunk to three
  must-holds and its evidence now hands the guarantee to `port` explicitly, but
  the case stays, because `PUT /{id}` is an HTTP-shaped door into the id space
  that a reader of this page has to be told closes — and the missing `noauto` test
  is now named as `port`'s item rather than this module's.

## Edge cases

### E-CRUDHTTP-01 — An absent mutation body is not an empty resource
**Shape:** boundary
**Setup:** A browser, proxy or client library sends `POST /articles`, `PATCH /articles/9` or `PUT /articles/9` with no body.
**What the consumer does:** They expect a create or replace to require a JSON object; an empty patch should not look like a saved edit.
**What must happen:** The three mutation routes refuse an absent body with a 400 before the mapper, hook or service runs. `POST /query` and `POST /count` may retain their documented empty-body meaning.
**Today:** ❌ wrong or unhandled
**Evidence:** `port/porthttp/body.go:77-90` returns success without decoding an empty body. All three bindings then pass their zero `In` or `U` value onward: `crud/http/crudnet/handler.go:290-308`, `:312-330` and `:343-366`; `crud/http/crudgin/handler.go:273-291`, `:295-313` and `:326-349`; `crud/http/crudfiber/handler.go:267-282`, `:286-301` and `:314-333`. `port/service.go:152-166` saves the zero create model and `:190-212` saves the zero replace model after setting only the path id. The only empty-body route test is the deliberate `POST /count` case (`crud/http/crudnet/handler_test.go:477-485`); no mutation-body test was found.
**Blast radius:** data loss

### E-CRUDHTTP-02 — JSON `null` is not a model
**Shape:** adversarial input
**Setup:** A client sends the valid JSON document `null` to a create, patch or replace route.
**What the consumer does:** They expect a top-level JSON value of the wrong shape to be refused, as an array and a string already are.
**What must happen:** A mutation route accepts only an object. It must return a 400 without changing a row when the body is `null`.
**Today:** ❌ wrong or unhandled
**Evidence:** The shared decoder calls `json.Unmarshal` without checking the top-level token (`port/porthttp/body.go:77-90`); unmarshalling `null` onto the bindings' zero struct targets at `crud/http/crudnet/handler.go:291-303`, `:318-325` and `:349-361` leaves the same zero values that E-CRUDHTTP-01 sends to the service. Gin and Fiber take the same path at `crud/http/crudgin/handler.go:274-286`, `:301-308`, `:332-344` and `crud/http/crudfiber/handler.go:268-278`, `:291-297`, `:319-329`. The malformed-body triplets cover arrays, strings and bad syntax, but not a top-level `null` (`crud/http/crudnet/edge_test.go:88-118`, with matching test names in Gin and Fiber).
**Blast radius:** data loss

### E-CRUDHTTP-03 — One JSON key, two conflicting values
**Shape:** adversarial input
**Setup:** A client or intermediary sends `{"role":"reader","role":"admin"}` or two different `price` values in one mutation body.
**What the consumer does:** They expect an ambiguous document to be refused. A signer, audit trail and Go decoder must not disagree about which value was authorised.
**What must happen:** A body with a duplicate key, at any object level the binding maps, is a 400 before a write.
**Today:** ❌ wrong or unhandled
**Evidence:** Every binding delegates body parsing to the single `json.Unmarshal` call in `port/porthttp/body.go:87`; no binding scans for duplicate keys before `Create`, `Update` or `Replace` call the service (`crud/http/crudnet/handler.go:290-366`, `crud/http/crudgin/handler.go:273-349`, `crud/http/crudfiber/handler.go:267-333`). The malformed-body tests enumerate invalid syntax and wrong shapes but name no duplicate-key control (`crud/http/crudnet/edge_test.go:88-118`, with matching test names in Gin and Fiber). `encoding/json` keeps the last duplicate value, so the write can differ from the value an upstream check saw.
**Blast radius:** silent wrong answer

### E-CRUDHTTP-04 — A JSON route reached through `text/plain`
**Shape:** seam
**Setup:** A cookie-authenticated service has no separate CSRF middleware and receives a valid JSON write with `Content-Type: text/plain`, `application/xml` or no content type.
**What the consumer does:** They read “JSON only” as a wire contract and rely on that distinction when placing the CRUD routes behind their existing browser defences.
**What must happen:** The JSON-body routes either require a JSON media type and answer 415, or the module pages say that media type is not checked and leave CSRF protection to the application.
**Today:** 🟡 partial
**Evidence:** `crud/http/crudnet/handler.go:451-461` and `crud/http/crudgin/handler.go:435-445` read only the body; Fiber deliberately calls the same decoder over `c.Body()` at `crud/http/crudfiber/handler.go:421-431`. None reads a request `Content-Type`. Their test helpers set `application/json` for every non-empty body (`crud/http/crudnet/fake_test.go:264-275`, `crud/http/crudgin/fake_test.go:279-290`, `crud/http/crudfiber/fake_test.go:266-277`), so no test pins the policy.
**Blast radius:** data loss

### E-CRUDHTTP-05 — `null` inside a numeric bulk delete
**Shape:** adversarial input
**Setup:** A client sends `POST /articles/bulk-delete` with `{"ids":[7,null]}` to an `int64`-keyed resource.
**What the consumer does:** They expect a malformed id list to be rejected as one request, with no delete attempted.
**What must happen:** Every bulk id must be a valid key. A `null` element must produce a 400 before `DeleteMany` is called.
**Today:** ❌ wrong or unhandled
**Evidence:** `BulkDeleteRequest` is a slice of the concrete key type (`crud/http/crudhttp/request.go:8-11`), and each binding unmarshals it through the plain shared decoder before only checking length (`crud/http/crudnet/handler.go:388-403`, `crud/http/crudgin/handler.go:371-386`, `crud/http/crudfiber/handler.go:353-365`). For a numeric slice `encoding/json` leaves the element at zero for `null`; `port/service.go:228-235` then passes every non-empty id slice to the repository. The existing empty-list test covers `{}` and `{"ids":null}` but not a `null` element (`crud/http/crudnet/edge_test.go:198-220`, with matching test names in Gin and Fiber).
**Blast radius:** data loss

### E-CRUDHTTP-06 — Pointer: the exact body cap reaches a mounted route
**Owner:** [Port.md](../port/Port.md) owns inclusive body-limit semantics. Crudhttp owns the mounted-route impact: the exact and plus-one boundary must be tested through Crudnet, Gin and Fiber before their shared decoder’s guarantee is marked handled. **Today:** ❓ unverified — `port/porthttp/body.go:62-90` has the inclusive implementation, but the binding triplets cover under-cap and much-larger bodies, not exactly-at/plus-one (`crud/http/crudnet/edge_test.go:531-553`, with matching test names in Gin and Fiber).

### E-CRUDHTTP-07 — Pointer: zero or negative caps retain the shared default
**Owner:** [Port.md](../port/Port.md) owns body and bulk-cap defaults; [[D-060]] assigns the one bulk-cap owner to `port.Rules.BulkCap`. Crudhttp retains only the binding conformance question. **Today:** ❓ unverified — `port/porthttp/body.go:62-82` and `port/rules.go:43-66` restore defaults, but no three-binding test supplies explicit zero or negative options (`crud/http/crudnet/edge_test.go:531-573`, `crud/http/crudnet/options_test.go:276-297`, with matching test names in Gin and Fiber).

### E-CRUDHTTP-08 — A nil query config turns the public API open
**Shape:** misuse
**Setup:** An endpoint declares `WithQuery(cfg)`, but `cfg` is nil because a configuration branch or generated declaration failed to initialise it.
**What the consumer does:** They expect a call spelling `WithQuery` to either bound the endpoint or fail while the process starts.
**What must happen:** `WithQuery(nil)` panics with a useful declaration error, rather than silently reverting to the open query defaults.
**Today:** ❌ wrong or unhandled
**Evidence:** Each `WithQuery` stores its argument without validation (`crud/http/crudnet/options.go:63-66`, `crud/http/crudgin/options.go:63-66`, `crud/http/crudfiber/options.go:62-65`). `port.Rules.Service` omits a nil `Query` entirely (`port/rules.go:68-79`), then `DefaultService.compile` invokes the query compiler with nil (`port/service.go:240-248`). An empty allow-list allows every canonical path (`crud/query/compile.go:210-225`). No nil-config test was found in the bindings or `port`.
**Blast radius:** data leak

### E-CRUDHTTP-09 — A live query configuration changes under a request
**Shape:** concurrency
**Setup:** Two handlers share one `*query.Config`; an application reload goroutine appends a field to an allow-list while one request is compiling it.
**What the consumer does:** They expect endpoint policy to be frozen at declaration, or an explicitly documented synchronisation boundary before a hot reload can widen an endpoint.
**What must happen:** Handler construction copies or freezes the config, so concurrent requests cannot race with a policy change or observe a half-written allow-list.
**Today:** ❌ wrong or unhandled
**Evidence:** `port.NewService` keeps the caller's config pointer (`port/service.go:81-95`) and passes that same pointer to `Request.Compile` on each request (`port/service.go:237-248`). `query.Config` exposes its allow-list slices (`crud/query/compile.go:29-80`) and `allowed` reads them directly (`crud/query/compile.go:210-225`). No test was found for post-construction mutation or concurrent use of a mutable config.
**Blast radius:** data leak

### E-CRUDHTTP-10 — A key type that cannot be written in a URL
**Shape:** degenerate declaration
**Setup:** A consumer supplies a custom service with a comparable struct key that has no `encoding.TextUnmarshaler`, for example `type Key struct { Tenant, Number int64 }`.
**What the consumer does:** They expect the resource declaration to reject a key that no `/{id}` route can parse, before it is mounted.
**What must happen:** Construction fails loudly and names the key requirement, or the API exposes a deliberate composite-key wire shape.
**Today:** 🟡 partial
**Evidence:** The constructors accept every `ID comparable` and do no key-shape validation (`crud/http/crudnet/handler.go:84-119`; Gin and Fiber have the same constructor shape at `crud/http/crudgin/handler.go:80-115` and `crud/http/crudfiber/handler.go:75-110`). At request time, `port.CoerceID` delegates to `query.Coerce` (`port/request.go:13-28`); its fallback tries to decode the URL string as JSON into the struct (`crud/query/coerce.go:100-145`) and returns a 400. The binding id tests cover invalid text and overflow for an integer key only (`crud/http/crudnet/edge_test.go:121-151`, with matching test names in Gin and Fiber).
**Blast radius:** confusing error

### E-CRUDHTTP-11 — A pointer key panics on the first route
**Shape:** degenerate declaration
**Setup:** A custom repository or service uses `*Key` as its comparable ID type and mounts one of the HTTP handlers.
**What the consumer does:** They expect unsupported key declarations to fail at construction, not when the first request reaches `GET /{id}`.
**What must happen:** The constructor rejects a nil-able key type with a direct explanation, before serving traffic.
**Today:** ❌ wrong or unhandled
**Evidence:** For a nil pointer zero value, `reflect.TypeOf(zero)` in `port.CoerceID` is nil (`port/request.go:15-20`), and `query.coerceString` immediately calls `reflect.New(t)` (`crud/query/coerce.go:85-93`), which panics for a nil type. No binding constructor validates the key type (`crud/http/crudnet/handler.go:84-119`, `crud/http/crudgin/handler.go:80-115`, `crud/http/crudfiber/handler.go:75-110`), and no pointer-key route test was found.
**Blast radius:** crash

### E-CRUDHTTP-12 — A missing mapper waits for the first write
**Shape:** misuse
**Setup:** A `NewFor` caller passes a nil mapper interface through a conditional or an uninitialised generated variable.
**What the consumer does:** They expect construction to reject a nil dependency, because the read routes can otherwise look healthy until the first production write.
**What must happen:** `NewFor` and `ServingFor` fail at declaration with an error that names the mapper.
**Today:** ❌ wrong or unhandled
**Evidence:** `NewFor` passes the mapper through `build` without a nil check (`crud/http/crudnet/handler.go:94-135`; the matching paths are `crud/http/crudgin/handler.go:90-131` and `crud/http/crudfiber/handler.go:85-126`). The first create and replace call `h.mapper.Model` (`crud/http/crudnet/handler.go:298` and `:356`; `crud/http/crudgin/handler.go:281` and `:339`; `crud/http/crudfiber/handler.go:274` and `:325`). No nil-mapper test was found.
**Blast radius:** crash

### E-CRUDHTTP-13 — The caller gives up while the database is still working
**Shape:** partial failure
**Setup:** A client cancels a slow list, count or write after the handler has handed its request context to the service.
**What the consumer does:** They expect cancellation to remain distinct from a database fault, so it does not inflate the API's 500 graph or cause a retry policy to treat a departed client as a server failure.
**What must happen:** The context reaches the service, and a returned `context.Canceled` or deadline error has an explicit transport outcome and log treatment.
**Today:** ❌ wrong or unhandled
**Evidence:** The bindings do pass their request context into service calls (`crud/http/crudnet/handler.go:204-280` and `:290-403`; `crud/http/crudgin/handler.go:187-263` and `:273-386`; `crud/http/crudfiber/handler.go:192-257` and `:267-365`). But `port.sentinelKind` has no cancellation or deadline arm and falls through to `KindInternal` (`port/kind.go:103-128`), which maps to 500 (`port/porthttp/errors.go:51-71`). No cancellation or deadline test was found under the three HTTP bindings. This is the inherited `errs`/`porthttp` gap already recorded as item 15 in the happy-half blocker table.
**Blast radius:** confusing error

### E-CRUDHTTP-14 — A string ID crosses encoded slashes and path cleaning
**Shape:** seam | adversarial input
**Setup:** A resource has a string ID such as `tenant/a`, `a//b`, or `a/../b`. A client sends its encoded spelling through a proxy that may decode `%2F`, collapse duplicate slashes, or clean dot segments before the request reaches Crudnet, Gin, or Fiber.
**What the consumer does:** They need one documented wire rule: either string IDs exclude slash and dot-path spellings, or every supported router preserves one canonical escaped form. A request must not select a different row because an intermediary normalised its path.
**What must happen:** Each binding rejects an unsafe path spelling before `CoerceID`, or all three preserve and test the same ID bytes. The route table must say which form generated clients may use.
**Today:** ❓ unverified
**Evidence:** Crudnet mounts `/{id}` and hands `r.PathValue("id")` to `port.CoerceID` (`crud/http/crudnet/handler.go:149-177`, `:466`); Gin and Fiber instead pass `c.Param("id")` and `c.Params("id")` (`crud/http/crudgin/handler.go:448-451`; `crud/http/crudfiber/handler.go:440-443`). `port.CoerceID` accepts a raw string key (`port/request.go:15-28`). No encoded-slash, duplicate-slash, or dot-segment test was found under the three bindings.
**Blast radius:** silent wrong answer

## Edge verdict

The worst open path is a mutation body that carries no object at all: empty and
`null` documents travel through the normal write path, and `PUT` can save a row
whose ordinary fields are all zero. A hostile JSON document can also choose a
duplicate value silently, while `null` inside a numeric bulk list can become the
zero key. Configuration is not treated as a declaration: `WithQuery(nil)` opens
an endpoint, a mutable config remains live under requests, and some unusable
types or dependencies fail only after traffic arrives. The byte caps themselves
are Port-owned; this module needs mounted exact-boundary and explicit-default
conformance tests. String IDs also cross three router path-normalisation rules
without a stated shared contract.

## Release blockers found here (edge)

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | Under `New`, `POST`, `PATCH` and `PUT` accept an absent or top-level-`null` body; create saves a zero model and replace can save a zero row at a real id | blocker | A client, proxy or integration mistake can write the wrong record with a success response. `POST /query` and `POST /count` need their empty-body exception, so the mutation rule must be route-specific. |
| 2 | Duplicate JSON keys silently take the last value before a write | serious | An upstream check, signature or audit record can describe one value while the framework persists another. The client sees success and no warning. |
| 3 | `{"ids":[7,null]}` on a numeric bulk delete can pass key zero to the repository | serious | A malformed batch can delete an otherwise valid zero-key row. The whole request must fail before the repository sees any id. |
| 4 | `WithQuery(nil)` discards the apparent public-query boundary and leaves the open default | serious | A typed declaration that looks like an allow-list can expose every mapped field. It must fail when the handler is built. |

## Edge DX constraints

`WithQuery(nil)` must refuse at handler construction; its name is a declaration
of a query boundary, not a request-time optional value. Renderer precedence stays
with the shared Port renderer/hop path rather than adding another binding-local
renderer configuration. Any future success or page-envelope change must be
evaluated against [[D-013]]’s refusal rule, [[D-042]]’s advisory/complete
vocabulary, and [[D-052]]’s document contract; it is not a local JSON
convenience. [[D-060]] remains the owner of volume-default decisions: bulk has
the settled `port.Rules.BulkCap` rule, while page-cap authority/migration remains
pending. Crudhttp may test binding conformance for whichever shared page rule is
selected, but must not create another cap policy.
