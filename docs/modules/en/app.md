# app — the composition root

```go
import "github.com/frostgrove/vv/app"                 // the values
import "github.com/frostgrove/vv/app/appfx"           // uber/fx: the seed command
import "github.com/frostgrove/vv/app/http/appfiber"   // uber/fx + Fiber: the API
```

**Module:** `app` is the root module — it imports only the standard library and
`port`. `appfx` and `appfiber` are modules of their own, because they take a
container ([[D-033]], [[D-074]])
· **Depends on:** `port` · and, for `appfiber`, `authhttp` and `authfiber`

The parts of a program's start-up that every service writes and most write
slightly wrong: an ordered chain of contributions, a seed command that is safe to
run twice, and an HTTP surface assembled out of modules that do not import each
other.

---

## What you get

### `app` — the values, container-free

| | |
|---|---|
| `Ordered[H]` | one contribution and where it goes in the sequence: `Name`, `Order`, `Handler` |
| `Sorted(contributions)` | that sequence, by order and then by name, over a clone of your slice |
| `Seeder` | one unit of seed data: `Name`, `Order`, `Envs`, `Run` |
| `Seeding` | what a runner is built for: the environment, optionally the set of environments the deployment has, a logger |
| `NewRunner(seeders, spec)` | sorts and validates; every wiring mistake is refused here rather than half-run |
| `Runner.Run(ctx)` | seeds this environment, stopping at the first failure and naming the seeder |
| `ErrSeeder` | what every refusal wraps |

Nothing here needs a container. `NewRunner` takes a slice you built however you
like.

### `appfx` — the seed command in an fx graph

| | |
|---|---|
| `AsSeeder(ctor)` | annotates a constructor so its `app.Seeder` joins the group |
| `Seeders` | the group, as an `fx.In` parameter object |
| `Seeding(spec)` | provides the `*app.Runner` over whatever joined |

```go
fx.Options(
    fx.Provide(appfx.AsSeeder(newRoleSeeder)),
    fx.Provide(appfx.AsSeeder(newAccountSeeder)),
    appfx.Seeding(app.Seeding{Env: cfg.Env, Known: cfg.Envs}),
)
```

### `appfiber` — the API in an fx graph

| | |
|---|---|
| `Route` | what a bounded context contributes: `Mount(fiber.Router)` and `Access() []authhttp.Endpoint` |
| `Middleware` | `app.Ordered[fiber.Handler]` |
| `OrderAuth` | where a guard goes: before everything that assumes a caller |
| `AsRoute` / `AsMiddleware` / `AsResolver` | the three annotations a module uses |
| `Mounted` / `Resolvers` | the two groups, as `fx.In` parameter objects |
| `Guarding(name, guard, opts…)` | an `auth.Guard`, as an ordered middleware at `OrderAuth` |
| `Spec` | `Prefix`, `Addr`, `Listen` |
| `Serving(spec)` | mount, verify, listen |
| `Mount(app, mounted, prefix)` | the same mounting without fx running it |
| `Listen(lc, sd, app, spec, log)` | the same listening |

```go
fx.Options(
    fx.Provide(newFiberApp),                      // yours: CORS, the error seam
    fx.Provide(appfiber.AsRoute(users.NewHandler)),
    fx.Provide(appfiber.AsRoute(billing.NewHandler)),
    appfiber.Serving(appfiber.Spec{Prefix: "/api/v1", Addr: ":8080"}),
)
```

`Addr` empty does not listen, which is what a test that only wants the routes
mounted asks for.

## The gate: start-up fails when the router and the declarations disagree

Every contributed route says what reaching it requires, and `Mount` compares that
against Fiber's own routing table before the server accepts anything:

```go
func (this *Handler) Access() []authhttp.Endpoint {
    return append(
        crudhttp.Table{Prefix: "/users"}.Guarded(PermRead, PermWrite, PermDelete),
        authhttp.Requires(http.MethodPost, "/users/:id/invite", PermInvite),
    )
}
```

A route nobody declared is a start-up failure. **So is a declaration whose route
no longer exists** — that half matters as much, because a declaration that
outlives its handler is what makes the list look complete while it covers less
every month. See [authhttp](authhttp.md) for the mechanism and [[D-073]] for why
it is at assembly and never per request.

## Why the order is a number

An fx value group is unordered. "The guard runs before the handler" decided by
which provider fx happened to visit first is a security property decided by luck —
one that every test mounting a single module still passes. `Order` is a number
rather than a position in a list so that a contributor can slot in between two
others without editing either.

Ties break by name, so two runs of one build sequence the same way and a log from
one is comparable with a log from another.

## Why a seeder must be runnable twice

A seed command is re-run after every migration by whoever is not sure whether it
was run already. One that inserts a second row the second time is a command
nobody dares use — which means the data it writes ends up being typed in by hand
instead.

`Envs` empty means every environment, and that default is the one you cannot get
wrong: a seeder that forgot to name its environments runs everywhere, which is
visible, rather than nowhere, which is not. Set `Seeding.Known` and a seeder
naming an environment the deployment does not have is refused at wiring instead
of silently never running.

## What this package may never become

It holds a list of things and the order to run them in. It does not hold a way to
*find* one: no `map[reflect.Type]any`, no `Get[T]()`, no `...any` that is
type-switched. [[D-037]] is the decision, with the three steps by which a
component list becomes a container.

That is not a refusal of dependency injection. `appfx` and `appfiber` hand these
types to fx, which holds the graph — the graph is the consumer's, in a module
they chose to import, and `go get github.com/frostgrove/vv` still resolves no
container ([[D-074]]).

## See also

- [authhttp](authhttp.md) — the access declaration and the comparison
- [authfiber](authfiber.md) — how Fiber's routing table is read
- [crudhttp](crudhttp.md) — `Table.Guarded`, a CRUD resource's ten routes
- [accessfx](access.md) — the same shape for the access context
- [[D-037]] · [[D-073]] · [[D-074]] · [[FL-024]]
