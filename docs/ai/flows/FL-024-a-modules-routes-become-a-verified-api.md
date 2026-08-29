# FL-024 — A module's routes become a verified API

**Entry point:** `app/http/appfiber/appfiber.go:Mount`
**Governed by:** [[D-073]] [[D-074]] [[D-037]] [[D-021]] [[D-051]] [[D-058]]

What happens between a bounded context saying "here are my endpoints and here is
what they need" and a server that either serves them or refuses to start.

## Contributing

1. **`AsRoute`** — `app/http/appfiber/appfiber.go:AsRoute` — annotates a
   constructor so its result joins `group:"vv.appfiber.routes"` as an
   `appfiber.Route`. The interface has two methods and both are required:
   `Mount(fiber.Router)` and `Access() []authhttp.Endpoint`. Access is on the
   interface rather than in a group of its own so a new module cannot be written
   without confronting it ([[D-073]]).
2. **`AsMiddleware`** — the same for `app.Ordered[fiber.Handler]`.
   `Guarding(name, guard)` is the shorthand that puts an `auth.Guard` at
   `OrderAuth`.
3. **`AsResolver`** — the same for an `errs.Resolver`, collected separately in
   `Resolvers` because the renderer is built when the `*fiber.App` is
   constructed and the routes are mounted afterwards ([[D-043]]).

## Mounting

1. **`Serving`** — `app/http/appfiber/appfiber.go:Serving` — two `fx.Invoke`s in
   order: mount, then listen. fx has registered every provider before it runs the
   first one, so the group is complete however late a module was listed.
2. **`Mount`** — `app/http/appfiber/appfiber.go:Mount` —
   `app.Sorted(mounted.Middlewares)` first (`app/ordered.go:Sorted`, a stable
   sort by order and then by name), then `route.Mount(api)` for each contributor
   on a group already carrying the prefix and every middleware.
3. **`authfiber.Verify`** — `auth/http/authfiber/surface.go:Verify` — last, and
   before the server can accept anything.

## The gate

1. **`authfiber.Routes`** — `auth/http/authfiber/surface.go:Routes` — reads
   `app.GetRoutes(true)`: Fiber's own table, with `Use` registrations filtered
   out by Fiber and HEAD and OPTIONS filtered out here. The `autoHead` flag is
   unexported, so a generated HEAD and a hand-written one are the same value from
   outside.
2. **`authhttp.Verify`** — `auth/http/authhttp/surface.go:Verify` — keys both
   sides on `METHOD path` with the trailing slash normalised away, prepends the
   prefix to each declaration, and skips a mounted route outside it. It collects
   every problem — undeclared, unmounted, declaring nothing, declared twice —
   sorts them and wraps `ErrSurface`.
3. **the refusal** — returned, never logged. `Mount` returns an error, `fx.New`
   collects it, and the process does not start.

The other bindings arrive at step 1 differently and at step 2 identically:
`auth/http/authgin/surface.go:Routes` reads `engine.Routes()`, and
`auth/http/authnet/surface.go:Surface` records what was registered through it,
because an `http.ServeMux` cannot be asked. What that costs on net/http is
`TestARouteRegisteredPastTheSurfaceIsInvisibleToTheGate`.

## Where a CRUD resource's declaration comes from

`crud/http/crudhttp/table.go:Table.Routes` is the same list
`crudfiber.HandlerFor.Register` walks; `Table.Guarded` turns it into
`[]authhttp.Endpoint` with three permissions. The paths are the table's and the
permissions are the consumer's — the paths are checked against the real routing
table anyway, and a hand-kept second copy of them is what goes stale ([[D-073]]).

## Listening

**`Listen`** — `app/http/appfiber/appfiber.go:Listen` — an `fx.Hook` whose
`OnStart` runs `app.Listen` in a goroutine and calls `Shutdowner.Shutdown` on
anything that is not `http.ErrServerClosed`. Blocking would never return from
OnStart; logging and carrying on leaves a process that is up, answers a health
check and serves nothing on the port it was asked for.

## Files

| File | What it holds |
|---|---|
| `app/ordered.go` | `Ordered[H]`, `Sorted` — the chain, and why a tie breaks by name |
| `app/doc.go` | what the composition root may hold and what it may not become |
| `app/http/appfiber/appfiber.go` | `Route`, `Middleware`, `OrderAuth`, the three annotations, `Mounted`, `Resolvers`, `Guarding`, `Spec`, `Serving`, `Mount`, `Listen` |
| `auth/http/authhttp/surface.go` | `Endpoint`, `Route`, `Public`, `Requires`, `Authenticated`, `Verify`, `UnderPrefix`, `ErrSurface` |
| `auth/http/authfiber/surface.go` | `Routes`, `Verify` — Fiber's table |
| `auth/http/authgin/surface.go` | `Routes`, `Verify` — Gin's table |
| `auth/http/authnet/surface.go` | `Surface`, `Over`, `AnyMethod` — the recorder, and what it cannot see |
| `crud/http/crudhttp/table.go` | `Table`, `Route`, `Need`, `Guarded` |

## Tests that walk this flow

| Test | What it pins |
|---|---|
| `app/http/appfiber/appfiber_test.go:TestAContributedRouteIsMountedUnderThePrefix` | a module writes relative paths |
| `…:TestStartUpFailsWhenARouteDeclaresNothing` | the undeclared half |
| `…:TestStartUpFailsWhenADeclarationMountsNothing` | the stale half |
| `…:TestMiddlewareRunsInTheOrderItDeclared` | registered in the wrong order, run in the right one |
| `…:TestAMiddlewareDeclaresNoAccess` | a `Use` is not an endpoint |
| `auth/http/authhttp/surface_test.go` | the comparison itself: both directions, the trailing slash, the prefix and its control, every problem at once |
| `auth/http/auth{net,gin,fiber}/surface_test.go` | the same three names on all three bindings — `make check-triplets` holds it |
| `auth/http/authfiber/binding_test.go:TestTheHeadFiberGeneratesNeedsNoDeclaration` | the generated HEAD, with a control that it is really there |
| `auth/http/authnet/binding_test.go:TestARouteRegisteredPastTheSurfaceIsInvisibleToTheGate` | what the recorder cannot see, with a control that it sees the rest |
| `crud/http/crud{net,gin,fiber}/table_test.go:TestEveryRouteInTheTableIsMounted` | the table and `Register` agree, asked of the router |
| `…:TestAReadOnlyResourceMountsNothingTheTableOmits` | and agree about what is left out |
