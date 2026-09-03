# app — the composition root

```go
import "github.com/frostgrove/vv/app"                 // the values
import "github.com/frostgrove/vv/app/module"          // what a bounded context contributes
import "github.com/frostgrove/vv/app/appfx"           // uber/fx: the modules and the seed command
import "github.com/frostgrove/vv/app/http/appfiber"   // uber/fx + Fiber: the API
```

**Module:** `app` is the root module — it imports only the standard library and
`port`. `appfx` and `appfiber` are modules of their own, because they take a
container ([[D-033]], [[D-074]])
· **Depends on:** `port` · and, for `appfiber`, `authhttp` and `authfiber`

The parts of a program's start-up that every service writes and most write
slightly wrong: an ordered chain of contributions, a module that says what it
contributes and to which kind of deployment, a seed command that is safe to run
twice, and an HTTP surface assembled out of modules that do not import each
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

### `module` — what a bounded context contributes, container-free

| | |
|---|---|
| `Role` | `API`, `Worker`, `Seeder` — the three kinds of deployment a contribution can belong to |
| `Kind` | `ProvideKind`, `RouteKind`, `WorkerKind`, `SeederKind`, `CheckKind`, and `Kind.Role()` |
| `New(name)` | the builder: `Order`, `Provide`, `Routes`, `Workers`, `Seeders`, `Checks`, then `Build()` / `MustBuild()` |
| `Spec` / `Define(spec)` / `MustDefine(spec)` | the explicit form the builder is a call over |
| `Auto(name, ctors…)` | the short form for a module that only provides |
| `Definition` | the value: `Name()`, `Order()`, `Roles()`, `Contributions()`, `Active(profile)`, `Describe(profile)` |
| `Profile` | a deployment: a `Name` and the roles it runs, plus `With(roles…)`, `Named(name)`, `Carries(role)`, `Check()` |
| `Base` · `Serving` · `Working` · `Seeding` · `Complete` | the five a deployment usually is |
| `NewCatalog(defs…)` / `MustCatalog(defs…)` | the one list of modules, ordered and checked |
| `Catalog.Names()` / `Definitions()` / `Describe(profile)` / `Check(profile)` | what is in it, and whether a profile over it is usable |
| `Doctor(catalog, profile)` | the `Diagnosis`: the descriptor, the problems, the notices — built without building anything |
| `ErrDefinition` · `ErrProfile` · `ErrCatalog` · `Refusal` | the refusals, each carrying every problem at once |

```go
func Module() module.Definition {
    return module.New("workspace").
        Order(200).
        Provide(NewRepository, NewDocuments).
        Routes(appfiber.AsRoute(NewHandler)).
        Workers(runtimefx.AsRunner(NewDebtSweeper)).
        Seeders(appfx.AsSeeder(NewTemplateSeeder)).
        Checks(healthfx.AsCheck(NewStorageCheck)).
        MustBuild()
}
```

The constructors are annotated by the composition root, with the annotation the
subsystem owns: `module` files them and never learns what a router, a supervisor
or a container is. Adding a second HTTP binding adds no package here.

`Auto` is the short form, `New` is the builder and `Define` is the explicit
spec — and the builder is a call over the spec, not a second contract. All three
refuse the same things, all at once: a module with no name, a nil constructor, a
module that contributes nothing.

### The deployment profile

`Complete` runs every role. `Serving` mounts the routes and starts no worker;
`Working` starts the workers and mounts nothing; `Seeding` runs the seeders and
does neither. Plain providers and health checks belong to every profile — a
worker replica needs the repository and answers `/health/ready` as much as an
API one does.

```go
catalog := module.MustCatalog(access.Module(), workspace.Module(), ops.Module())

fx.New(
    appfx.Options(catalog, module.Serving),          // the API replica
    appfiber.Serving(appfiber.Spec{Prefix: "/api/v1", Addr: ":8080"}),
).Run()
```

A role the profile does not name is not wired at all. That is the difference
from three hand-written lists of options: the seed command and the API build the
same graph, and what separates them is one value rather than whoever last edited
two of the three files.

`module.Serving.With(module.Worker).Named("monolith")` is a deployment that is
both.

### `Doctor` — what this process is about to be

```go
fmt.Print(module.Doctor(catalog, profile))
```

```text
profile serving activates api
  access (order 100)
    provide   3  active
    check     1  active
  workspace (order 200)
    provide  12  active
    route     2  active
    worker    1  inactive
    seeder    1  inactive
  ops (order 300)
    worker    1  inactive
notice: the module "ops" contributes nothing to the serving profile
```

**Nothing in the catalog is called to answer that.** No constructor runs, no
pool opens — which is what makes it safe to print from a command that must not
touch the deployment, and what makes it answerable for a graph too broken to
build. A `Diagnosis` separates problems from notices: a problem is a refusal
([[D-106]]), a notice is a shape worth reading. Running every role over a
catalog with no worker in it is ordinary, so it is a notice; a profile that
activates nothing at all is a process that would start, report healthy and do
nothing, so it is a problem.

What the diagnosis does not yet carry is each subsystem's schema and readiness
summary — the migration a `jobs` driver needs, the resource identity a cache was
built on. That is where it will go.

### `appfx` — the modules and the seed command in an fx graph

| | |
|---|---|
| `Options(catalog, profile)` | every module in the catalog, wired for that deployment |
| `Option(definition, profile)` | one module, wired the same way |
| `Auto(catalog)` | `Options` under `module.Complete` |
| `AsSeeder(ctor)` | annotates a constructor so its `app.Seeder` joins the group |
| `Seeders` | the group, as an `fx.In` parameter object |
| `Seeding(spec)` | provides the `*app.Runner` over whatever joined |

The binding is the whole of it: one `fx.Module` per definition, holding the
constructors the profile activates. A catalog or a profile that was refused
becomes an `fx.Error`, because a container handed nothing starts perfectly.

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
| `Policy` | what reaching one operation takes: `Requires(perms…)`, `Authenticated(why)`, `Public(why)` |
| `RouteSetSpec` | `Prefix`, `Render` |
| `NewRouteSet(spec)` / `MustRouteSet(spec)` | the registrar, refusing a bad prefix at the call |
| `Routes(prefix, opts…)` | the same registrar, carrying a bad prefix to the build |
| `RootRouteSetSpec` | `App`, `Render` |
| `NewRootRouteSet(spec)` / `MustRootRouteSet(spec)` / `RootRoutes(app, opts…)` | the registrar for what answers outside every prefix, declared `AtRoot` |
| `RouteSet.GET/POST/PUT/PATCH/DELETE(path, policy, handler)` | one call: mount, declaration and outer check |
| `RouteSet.Handle(method, path, policy, handler)` | the same for a method the verbs do not name |
| `RouteSet.Route()` / `MustRoute()` | the `Route`, or every problem at once |
| `ErrRouteSet` | what every registrar refusal wraps |
| `Combine(routes…)` / `MustCombine(routes…)` | several shapes contributed as one route |
| `ErrCombine` | what a combination's refusal wraps |
| `UncheckedRule` | what to do about a declaration nothing in front of the handler checks |
| `NamingUnchecked` / `RefusingUnchecked` / `ExcusingUnchecked(reason, contributors…)` | log it, refuse to start, or exempt the contributors named |
| `Unchecked` / `ErrUnchecked` | the report, and what the refusal wraps |
| `Middleware` | `app.Ordered[fiber.Handler]` |
| `OrderAuth` | where a guard goes: before everything that assumes a caller |
| `AsRoute` / `AsMiddleware` / `AsResolver` | the three annotations a module uses |
| `Mounted` / `Resolvers` | the two groups, as `fx.In` parameter objects |
| `Mounted.Contributions()` | every route the graph mounts — contributed ones and the ones a `RouteSet` projected — the same list `Mount` reads |
| `Guarding(name, guard, opts…)` | an `auth.Guard`, as an ordered middleware at `OrderAuth` |
| `Spec` | `Prefix`, `Addr`, `Listen` |
| `Serving(spec)` | mount, verify, listen |
| `Mount(app, mounted, prefix)` | the same mounting without fx running it |
| `Listen(lc, sd, app, spec, log)` | the same listening |
| `HealthSpec` | `Registry`, `Path`, `Operator`, `Render` |
| `Health(spec)` | the three health projections as an ordinary contributed route |
| `DefaultHealthPath` | `/health` |

```go
fx.Options(
    fx.Provide(newFiberApp),                      // yours: CORS, the error seam
    fx.Provide(appfiber.AsRoute(users.NewHandler)),
    fx.Provide(appfiber.AsRoute(billing.NewHandler)),
    appfiber.Serving(appfiber.Spec{Prefix: "/api/v1", Addr: ":8080"}),
)
```

`Addr` empty does not listen, which is what a test that only wants the routes
mounted asks for. `Mounting` is the option that says so out loud, and it refuses
an `Addr` it would never use; `Listening` refuses an empty one.

### The start waits for the port

`Listen` does not return until the listener holds the address or the attempt to
take it failed. A bind opened in a goroutine makes "the application started" mean
"a goroutine was scheduled": the address another replica already holds, the
privileged port, the typo in the configuration all arrive after fx has told every
other hook that start-up succeeded, and the process then answers its readiness
probe while nothing is listening. TLS, the Unix socket and the listener network
stay Fiber's own code path rather than a copy of it, and whatever the caller put
in `ListenConfig.ListenerAddrFunc` still runs — the address it carries is what
gets logged, because a listener asked for port 0 knows its port and the
configuration does not.

What the start waits for is Fiber's `OnListen` hook, and the distinction is not
cosmetic. Fiber fires `ListenerAddrFunc` from `createListener` and only then runs
`startupProcess`, which appends the automatic HEAD route of every GET — a write
to the route table. A start that returned on the address would hand control back
while that write was still in flight, and every reader of the mounted surface
after it, `authfiber.Verify` included, would race the framework's own goroutine.
`OnListen` runs after `startupProcess` and before `Serve`: the first moment both
the port is held and nobody is writing.

`Listen.EnablePrefork` is refused: prefork re-executes the process, so each child
rebuilds the graph this one already built and the parent binds nothing at all.
Run replicas instead.

### One call, not two lists

`Mount` and `Access` are two methods, and writing them by hand means keeping two
lists in step. The registrar makes that impossible for the ordinary case: one
call carries the path, the policy and the handler, and the set produces the
mount, the declaration and the check in front of the handler out of that one
record.

```go
func NewHandler(useCase *DeadJobs) (appfiber.Route, error) {
    handler := &Handler{useCase: useCase}
    return appfiber.Routes("/ops/jobs").
        GET("/dead", appfiber.Requires(PermJobsRead), handler.list).
        POST("/dead/:id/restart", appfiber.Requires(PermJobsWrite), handler.restart).
        Route()
}
```

The path is joined with the prefix once, so the declared path and the mounted
path cannot be different strings. `Requires` puts an outer check in front of the
handler — `auth.Require`, then `auth.HasAll`, then the handler — and refuses
through the same `porthttp` renderer as the rest of the surface. `Public(why)`
mounts no check and needs the reason in writing; `Requires()` with no permission
and `Public("")` are both refused, because forgetting must not read as "no
permission needed".

The declaration is not what the check reads: both are projections of the `Policy`
([[D-100]]). Nothing derives a request-time decision from an
`authhttp.Endpoint`, which is [[D-073]] and still holds.

`Routes(prefix)` collects mistakes and hands them all to `Route()`, so a chain
reads as one expression; `NewRouteSet(RouteSetSpec{…})` is the same registrar
refusing at the call, for a wiring site that would rather fail there.

`Route` remains an interface. A module whose shape the set does not cover writes
`Mount` and `Access` by hand, and the gate below treats it identically — a route
mounted past the registrar is still a start-up failure, because the gate reads
Fiber's table rather than the set's records.

`RootRoutes(app)` is the registrar for the paths a caller reaches before it knows
there is an API — `/`, `/favicon.ico`, `/live`, `/ready`. It mounts on the
`*fiber.App` instead of on the router it is handed, declares every operation with
`authhttp.AtRoot` so the gate compares it by its absolute path, and accepts `/`,
which a prefixed set refuses. Without the `*fiber.App` it is refused at
`NewRootRouteSet`, because a root set that mounts nothing would still declare
everything.

### Mounting what the registrar cannot express

A contributor rarely needs the hand-written form for *everything* it serves. A
CRUD resource mounts through its own `Register` and declares itself from the
gate's permissions ([[D-107]]); the operations around it are ordinary routes.
`Combine` lets one contribution carry both, so the hand-written half stays the
size of the problem:

```go
func NewHandler(contracts crud.Repository[…], useCase *Analyse) (appfiber.Route, error) {
    handler := &Handler{…}
    operations, err := appfiber.Routes("/contracts").
        POST("/:id/analyses", appfiber.Requires(PermAnalyse), handler.analyse).
        Route()
    if err != nil {
        return nil, err
    }
    return appfiber.Combine(resourceRoute(contracts), operations)
}
```

Parts are mounted in the order given and their declarations concatenated. Two
parts declaring the same method and path are refused at `Combine`: the second
registration answers nothing, and the gate compares a set of endpoints against
the table, so it would not notice. Combining nothing, or a nil part, is refused
there too.

### A declaration nothing checks

`Requires(perm)` written by hand is a claim the framework cannot verify — the
permission may be enforced inside the use case, or by the repository's gate, or
by nothing at all. What `appfiber` can see is that *it* mounted no check, and it
says so. `Mount` collects every such declaration and hands it to the contributed
`UncheckedRule`:

| Rule | What it does |
|---|---|
| `NamingUnchecked` | the default: logs each one, with the contributor, method and path |
| `RefusingUnchecked` | fails the start instead |
| `ExcusingUnchecked(reason, contributors…)` | refuses all but the contributors named — and refuses those too when the reason is blank |

Provide one and it replaces the default:

```go
fx.Provide(func() appfiber.UncheckedRule {
    return appfiber.ExcusingUnchecked(
        "the repository's gate enforces these until the resource moves to the registrar",
        "workspace/api.resource")
})
```

Because a combination reports against its *parts*, an excuse names the part that
needs it rather than the whole handler, and an operation added to the registrar
half later cannot inherit an exemption written for something else.

### The health routes

`Health` turns a `*health.Registry` into a `Route` like any other, so the gate
below sees it too:

```go
fx.Provide(appfiber.AsRoute(func(registry *health.Registry) (appfiber.Route, error) {
    return appfiber.Health(appfiber.HealthSpec{
        Registry: registry,
        Operator: []auth.Permission{PermHealthRead},
    })
}))
```

`GET /health/live` and `GET /health/ready` are declared `Public` — a probe has no
account to authenticate — and answer the two bounded projections. `Operator`
empty mounts no third route at all; set, it mounts `GET /health/detail` behind
those permissions, checked in the handler and refused through `porthttp` without
disclosing any of the detail.

Only `down` answers 503. A degraded replica answers 200 and says so in the body,
because taking it out of rotation removes the replicas that still serve the half
of the API that works. [[D-090]] and [[D-091]] are the reasoning; [health](health.md)
is the registry.

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
- [health](health.md) — what the health routes answer from
- [runtime](runtime.md) — background work, started by a supervisor rather than by itself
- [[D-037]] · [[D-073]] · [[D-074]] · [[D-090]] · [[D-091]] · [[D-100]] · [[D-106]] · [[FL-024]] · [[FL-027]] · [[FL-030]]
