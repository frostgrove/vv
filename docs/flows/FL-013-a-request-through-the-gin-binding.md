# FL-013 — A request through the Gin binding

**Entry point:** `http/crudgin/handler.go:List` (and every sibling route method)
**Implements:** [[UC-001]] [[UC-002]] [[UC-013]] [[UC-015]] · **Governed by:** [[D-034]] [[D-033]] [[D-022]] [[D-012]] [[D-015]]

There are two HTTP bindings and one path. This flow is the short document that
says where they part company: what `crudgin` does that `crudfiber` does not, and
where a reader should stop reading this file and go read [[FL-001]], [[FL-002]],
[[FL-003]], [[FL-011]] or [[FL-012]] instead.

Everything from `compile` onwards is identical, by construction rather than by
discipline — the shared half is one package, `http/crudhttp`, and both bindings
call into it ([[D-034]]).

## The path

1. **`Handler.List`** — `http/crudgin/handler.go:100` — a `gin.HandlerFunc`, so
   `func(*gin.Context)` with no error return. Where the Fiber handler returns an
   error for the framework to render, this one writes the response and returns.
2. **`Handler.parseQueryString`** — `http/crudgin/handler.go:371` — reads
   `c.Request.URL.Query()`, which is already a `url.Values` with every repeat
   intact. Fiber needed `queryValues` walking `QueryArgs().VisitAll` to get the
   same thing; Gin's own `c.Query` has the same collapsing hazard and is not
   used. From here it is `query.ParseQuery` and [[FL-001]].
3. **`Handler.parseBody`** — `http/crudgin/handler.go:375` — `crudhttp.DecodeJSON`
   over `c.Request.Body`. An empty body is not an error: `POST /count` and
   `POST /query` both mean "no narrowing" when sent with none.
4. **`Handler.compile`** — `http/crudgin/handler.go:353` — identical to Fiber's,
   including that `WithScope`'s options are *appended*, because `crud.Where` ANDs
   ([[D-004]]).
5. **The repository call** — `h.repo.Get(c.Request.Context(), opts…)`. The
   context is where a principal reaches the security gate, so an application's
   own middleware puts it there with `c.Request = c.Request.WithContext(ctx)`.
   The handler contributes nothing but the pass-through ([[FL-007]]).
6. **`Handler.entity` / `c.JSON`** — `http/crudgin/handler.go:396` — one status,
   one body. `crud.MapPage` renders a page through `WithTransform` exactly as in
   [[FL-001]].
7. **`Handler.fail` → `DefaultErrorHandler`** — `http/crudgin/options.go:103` —
   `crudhttp.Body(err)` decides both halves, then `c.Error(err)` records the
   cause for Gin's logging middleware and `c.AbortWithStatusJSON` writes the
   response. The error text still never reaches a 500 body ([[FL-011]]).

## Where the two bindings differ

Four differences, all of them deliberate. Nothing else about the two APIs is
different, including every option name and every status code.

- **Mounting.** Gin has no mountable sub-application, so there is no counterpart
  to `Routes() *fiber.App`. `Mount(r gin.IRouter, prefix string)` is the
  one-liner and `Register(r gin.IRoutes)` takes an engine or a group that already
  exists.
- **The collection route is registered as `""`, not `"/"`.** On a group of
  `/widgets` the `"/"` form produces `/widgets/`, which does not match
  `GET /widgets`. Registering both is not an option: on the engine itself they
  collapse to the same path and Gin panics with "handlers are already registered".
  The trailing-slash spelling is left to Gin's `RedirectTrailingSlash`, on by
  default, which answers 301.
- **JSON bodies only.** Fiber's `Bind().Body()` dispatches on Content-Type and
  accepts XML and form encodings. `crudgin` decodes with `encoding/json`, and
  deliberately does not use `c.ShouldBindJSON`, because Gin's binder runs
  `validator/v10` over `binding:"…"` tags — a tag on a consumer's model would
  then change what the CRUD routes accept under one transport and not the other
  ([[D-034]], and the guarantee it protects is UC-003's).
- **An unmounted route is 404, not 405.** Gin answers 404 for a path that exists
  under another verb unless `Engine.HandleMethodNotAllowed` is set. That is the
  application's setting to make, and the handler does not touch it.

## Where the decisions bite

- **The status table is not here.** `crudgin.Status` is one line over
  `crudhttp.Status`. [[D-034]] forbids a second copy, because two switches over
  the same sentinels drift the day one gains an arm and nothing fails when they
  do — which is exactly what [[FL-011]] predicted before the second binding
  existed.
- **`Repository` is an alias, not a second interface.** `crudgin.Repository` and
  `crudfiber.Repository` are the same type, so one service struct satisfies both
  ([[D-022]]). The integration suite mounts one `articleService` on both engines
  and that is what proves it.
- **PUT still refuses to create.** `Handler.Replace` does the existence probe,
  the `ClearGenerated` and the `SetID` from the path, in that order ([[D-012]]).
  The PostgreSQL sequence hazard the decision exists for is engine-level, so it
  reaches this binding unchanged.
- **Gin is not a dependency of anybody else.** `http/crudgin` is its own module
  ([[D-033]]); sonic, validator/v10, quic-go, protobuf and mongo-driver arrive
  with it and reach no other consumer.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| `GET /widgets/count` read as an entity named "count" | Gin's router gives a static segment priority over `:id` | it does not happen; pinned by `TestStaticRoutesAreNotSwallowedByTheIDRoute` |
| `GET /widgets/` with the collection mounted at `""` | `Engine.RedirectTrailingSlash` | 301 to `/widgets` |
| `GET /widgets/abc` where the key is `int64` | `crudhttp.CoerceID` (`request.go:18`) | 400 `{"error":"bad_request","message":"\"abc\" is not a valid id"}` |
| a body that is not JSON | `crudhttp.DecodeJSON` (`request.go:55`) | 400, and the repository is never called |
| `?filtr=` — a parameter one edit from a real one | `query.ParseQuery` → `checkParams` | 400 with the offending path named |
| a write verb on a `ReadOnly` handler | the route was never registered | 404, or 405 with `HandleMethodNotAllowed` |

## Files

| File | Role |
|---|---|
| `http/crudgin/handler.go` | routes, `Mount`/`Register`, query-string reading, body decoding, option assembly |
| `http/crudgin/options.go` | the nine options, `Status`, `DefaultErrorHandler` |
| `http/crudhttp/repository.go` | `Repository` — the interface both bindings alias |
| `http/crudhttp/errors.go` | `Status`, `StatusText`, `Body`, `ErrBadRequest`, `BadRequest`, `BadRequestf` |
| `http/crudhttp/request.go` | `CoerceID`, `NarrowForCount`, `NarrowForEntity`, `DecodeJSON`, `BulkDeleteRequest` |
| `http/crudhttp/model.go` | `Sanitize`, `ClearGenerated` |
| `http/crudgin/go.mod` | the module boundary that keeps Gin off everybody else |

Everything the request touches after `compile` is in [[FL-001]]'s file table.

## Tests that walk this flow

- `TestStaticRoutesAreNotSwallowedByTheIDRoute` — `http/crudgin/routing_test.go`
  — the fixed paths, with a control case showing the `:id` route really is live
  and really would have taken them.
- `TestTheCollectionRouteAnswersWithoutATrailingSlash` —
  `http/crudgin/routing_test.go` — `GET /widgets` is 200 and `/widgets/` is 301.
- `TestMountingAtTheRootDoesNotCollide` — `http/crudgin/routing_test.go` — the
  panic the `""` choice avoids.
- `TestRoutesMountEveryDocumentedEndpoint` — `http/crudgin/handler_test.go` —
  every route, its verb, its status and the repository method behind it.
- `TestRepeatedFilterTermsAllSurvive` — `http/crudgin/handler_test.go` — the
  repeats `URL.Query()` keeps.
- `TestAServiceLayerCanStandInForTheRepository` — `http/crudgin/handler_test.go`.
- `TestStatusMapsWhatItPromisesTo` and `TestA500NeverEchoesTheInternalError` —
  `http/crudgin/edge_test.go` — the shared table, from this side.
- `TestPutIsNotAWayAroundAllowClientID` — `http/crudgin/write_edge_test.go`.
- `TestGinHTTP*` — `test/integration/http_gin_test.go` — nine tests end to end
  against live PostgreSQL and MySQL, mounting the same service type the Fiber
  suite mounts.

The two bindings' unit suites carry the same 147 test and subtest names. A
change that makes them diverge is either a bug or a fifth entry in *Where the
two bindings differ*.

## See also

[[FL-001]] [[FL-002]] [[FL-003]] [[FL-011]] [[FL-012]] [[FL-007]]
