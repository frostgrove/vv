# FL-024 — A module's routes become a verified API

**Entry point:** `app/http/appfiber/appfiber.go:Mount`
**Governed by:** [[D-073]] [[D-100]] [[D-074]] [[D-037]] [[D-021]] [[D-051]] [[D-058]]

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

## Writing the two halves as one call

`Route` is an interface and stays one: a module with a shape the registrar does
not cover writes `Mount` and `Access` by hand. What the registrar removes is the
common case, where the two were two hand-kept lists in one file.

1. **`Routes(prefix)`** — `app/http/appfiber/routeset.go:Routes` — the shorthand,
   over `NewRouteSet(RouteSetSpec)` under it. The shorthand carries a bad prefix
   to the build so a chain of registrations reads as one expression; the
   constructor refuses it at the call. Both go through
   `app/http/appfiber/routeset.go:newRouteSet`.
2. **`RouteSet.GET` / `POST` / `PUT` / `PATCH` / `DELETE`**, over
   `app/http/appfiber/routeset.go:RouteSet.Handle` — one call takes the path, a
   `Policy` and the handler. The prefix is joined here, once, and the joined
   string is what both projections use afterwards. A path that is not a path, a
   missing handler, a policy that states nothing and a second registration of the
   same method and path are collected as problems rather than panicking.
3. **`Policy`** — `app/http/appfiber/routeset.go:Requires`, `Authenticated`,
   `Public` — the one value the two halves come out of. `Requires()` with no
   permission and `Public("")` are refusals, so forgetting cannot read as a
   decision ([[D-100]]).
4. **`RouteSet.Route`** — returns the `Route` or every problem at once, wrapping
   `ErrRouteSet`; `MustRoute` is the panicking form for a wiring site that has no
   error to return.
5. **`registeredRoutes.Mount`** — `router.Add(method, path, enforcement, handler)`
   when the policy names permissions or asks for a signed-in caller, and
   `router.Add(method, path, handler)` when it is public. The enforcement is an
   ordinary Fiber handler in front of the route's own: `auth.Require`, then
   `auth.HasAll`, then `Next`; a refusal goes through
   `app/http/appfiber/routeset.go:refuse` and the `porthttp` renderer the set was
   built with, which is the same one `Health` refuses through.
6. **`registeredRoutes.Access`** — the same records, projected to
   `authhttp.Endpoint`. Nothing reads it back: `Mount` still hands it to
   `authfiber.Verify` and no request path touches it ([[D-073]]).

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
   out by Fiber and the HEAD Fiber generates for a GET filtered out here. The
   `autoHead` flag is unexported, so the generated half is recognised by its
   shape — a HEAD whose path also carries a GET. A hand-mounted OPTIONS, and a
   HEAD with no GET beside it, are surface and must declare ([[D-073]]).
2. **`authhttp.Verify`** — `auth/http/authhttp/surface.go:Verify` — splits the
   declarations at `Endpoint.Absolute` (`splitAtRoot`) and hands `VerifyAreas`
   two areas: the prefix with the relative declarations, and the root with the
   `AtRoot` ones. Nothing is skipped, so a probe outside the prefix is
   `is mounted and declares no access` until somebody says why it is open, and
   the root area carries the advice appended to that message — `declare it with
   authhttp.AtRoot or mount it under the prefix` — so the refusal names its own
   remedy rather than only the route. The
   prefix is compared by path segment — `/api/v10` is not under `/api/v1` — and is
   normalised for a missing leading or a trailing slash.
3. **`authhttp.VerifyAreas`** — same file — the comparison itself: `Area` is a
   prefix and its declarations, `Under` and `Rooted` build them, each mounted
   route goes to the most specific area covering it, and one no area covers is
   `is mounted outside every verified surface`. Both sides are keyed on
   `METHOD path` with the trailing slash normalised away and the area's prefix
   prepended to each declaration. Overlapping prefixes are a refusal of their
   own. It collects every problem — undeclared, unmounted, declaring nothing,
   declared twice — sorts them and wraps `ErrSurface`. `authfiber.VerifyAreas`,
   `authgin.VerifyAreas` and `Surface.VerifyAreas` are the three entry points for
   a composition root serving more than two surfaces.
4. **the refusal** — returned, never logged. `Mount` returns an error, `fx.New`
   collects it, and the process does not start.

The other bindings arrive at step 1 differently and at step 2 identically:
`auth/http/authgin/surface.go:Routes` reads `engine.Routes()`, and
`auth/http/authnet/surface.go:Surface` records what was registered through it,
because an `http.ServeMux` cannot be asked. What that costs on net/http is
`TestARouteRegisteredPastTheSurfaceIsInvisibleToTheGate`.

## Where the flow stops

It stops at "every mounted route was considered". `Endpoint.Needs` is read by
`Declares` and by the duplicate message and by nothing else — no request path
derives anything from a declaration, deliberately ([[D-073]]).

For a route written by hand, the permission it names is enforced by whatever the
handler behind it calls: `access.Require(ctx, perms...)`, or `security.Gate`
around the repository ([[FL-020]]), or both, and proving that each handler does
is the consumer's test over their own surface.

For a route registered through a `RouteSet` the outer check is there, and it is
not derived from the declaration — both come from the `Policy` ([[D-100]]). What
it answers is "may this account call this operation at all". Whether the rows it
then touches are the caller's is still `security.Gate`'s question, and a row out
of scope is still a 404 ([[D-008]]).

It stops short of the middleware chain, so the chain refuses on its own.
`app/http/appfiber/appfiber.go:Mount` fails the start on a contribution whose
`Handler` is nil, naming it, rather than mounting the rest. The gate cannot cover
this: it compares declarations against routes, and a `Use` registration is
filtered out of the table it reads two steps above, so "there was no guard" and
"the guard came out nil" are the same snapshot to it ([[D-073]]). A named
contribution that arrived empty is a wiring branch that fell through, and the
only honest answer is to stop — a guard that came out nil used to be a process
that started with the surface it protects answering unauthenticated.

## Where a CRUD resource's declaration comes from

`crud/http/crudhttp/table.go:Table.Routes` is the same list
`crudfiber.HandlerFor.Register` walks, and every route in it carries the
`crud.Action` it performs.

`Table.GuardedBy(policy)` turns that list into `[]authhttp.Endpoint` by asking
the policy the repository is gated with what each action requires
(`security.Policy.RequiredFor`, [[FL-020]]). Nobody writes a permission twice:
the paths are the table's and the permissions are the gate's ([[D-107]]). A
mounted route whose action the policy does not declare is not given an empty
declaration — `GuardedBy` collects every one of them and refuses, naming the
routes. An action declared with no permissions at all becomes
`authhttp.Authenticated`, because that is what "any signed-in caller" is called
here.

`Table.Guarded(read, write, del)` is the same derivation over a
three-permission policy, for a consumer whose enforcement is somewhere else. It
maps create and update onto `write` and both deletes onto `del`, which is the
collapse `GuardedBy` does not do.

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
| `app/http/appfiber/routeset.go` | `Policy`, `Requires`, `Authenticated`, `Public`, `RouteSetSpec`, `NewRouteSet`, `Routes`, `RouteSet`, `Route`, `MustRoute`, `ErrRouteSet`, `refuse` |
| `app/http/appfiber/health.go` | `HealthSpec`, `Health`, `DefaultHealthPath` — a contributed route that declares its own probes |
| `auth/http/authhttp/surface.go` | `Endpoint`, `Route`, `Public`, `Requires`, `Authenticated`, `AtRoot`, `Verify`, `UnderPrefix`, `Area`, `Under`, `Rooted`, `VerifyAreas`, `ErrSurface` |
| `auth/http/authfiber/surface.go` | `Routes`, `Verify`, `VerifyAreas` — Fiber's table |
| `auth/http/authgin/surface.go` | `Routes`, `Verify`, `VerifyAreas` — Gin's table, unfiltered |
| `auth/http/authnet/surface.go` | `Surface`, `Over`, `Handler`, `AnyMethod` — the recorder, what it cannot see, and the sealed way to serve it |
| `crud/http/crudhttp/table.go` | `Table`, `Route`, `Policy`, `Guarded`, `GuardedBy` |
| `crud/action.go` | `Action`, `Actions` — the vocabulary a route and a gate share |
| `crud/decorators/security/security.go` | `Policy.Requires`, `Policy.RequiredFor` — what `GuardedBy` reads |

## Tests that walk this flow

| Test | What it pins |
|---|---|
| `app/http/appfiber/appfiber_test.go:TestAContributedRouteIsMountedUnderThePrefix` | a module writes relative paths |
| `…:TestStartUpFailsWhenARouteDeclaresNothing` | the undeclared half |
| `…:TestStartUpFailsWhenADeclarationMountsNothing` | the stale half |
| `…:TestMiddlewareRunsInTheOrderItDeclared` | registered in the wrong order, run in the right one |
| `…:TestAMiddlewareDeclaresNoAccess` | a `Use` is not an endpoint |
| `…:TestAMiddlewareThatCameOutWithoutAHandlerStopsTheStart` | a named contribution that arrived empty is not skipped |
| `app/http/appfiber/routeset_test.go:TestOneRegistrationBothMountsTheOperationAndDeclaresIt` | one call is both actions |
| `…:TestTheDeclaredPathIsTheMountedPath` | the two projections carry the same string |
| `…:TestAnOperationRefusesAPrincipalWithoutThePermissionItDeclares` | the declared permission is the enforced one |
| `…:TestAnOperationAdmitsAPrincipalHoldingThePermissionItDeclares` | the control that it lets the right caller through |
| `…:TestAnOperationThatNamesPermissionsRefusesAnAnonymousCaller` | no principal is a 401, not a pass |
| `…:TestAPublicOperationIsMountedWithoutAPermissionCheck` | `Public` mounts no check |
| `…:TestASignedInOperationTakesAnyPrincipalAndRefusesNone` | `Authenticated` checks the door and not the permissions |
| `…:TestASignedInOperationMustSayWhyBeingSignedInIsEnough` | and still has to say why that is enough |
| `…:TestAnOperationThatStatesNoPolicyIsRefusedAtTheBuild` | forgetting is not "no permission needed" |
| `…:TestRequiringNothingIsNotAWayToSayPublic` | `Requires()` is a mistake, not an opening |
| `…:TestAPublicOperationMustSayWhyItIsOpen` | the reason is required where the operation is written |
| `…:TestTheSameOperationCannotBeRegisteredTwice` | one path, one policy |
| `…:TestNewRouteSetRefusesAPrefixThatIsNotAPath` | the explicit constructor refuses now |
| `…:TestRoutesCarriesAPrefixMistakeToTheBuild` | the shorthand refuses at `Route()` |
| `…:TestARouteMountedPastTheRegistrarStillBreaksStartUp` | the router is still the independent witness |
| `…:TestAnOperationWithoutAHandlerIsRefused` | a declaration that answers nothing is refused |
| `crud/http/crud{net,fiber,gin}/table_test.go:TestTheAccessDeclarationIsDerivedFromTheGatesOwnPermissions` | every mounted route is declared with the permission its own action requires, a policy naming a different one per action |
| `…:TestAMountedRouteThePolicyLeavesUndeclaredIsRefusedAtAssembly` | an undeclared action is a refusal naming the route, with the read-only resource as the control |
| `auth/http/authhttp/surface_test.go` | the comparison itself: both directions, the trailing slash, the prefix and its control, every problem at once |
| `auth/http/auth{net,gin,fiber}/surface_test.go` | the same five names on all three bindings — `make check-triplets` holds it |
| `…:TestAHandMountedHeadOrOptionsRouteMustDeclareItsAccess` | a verb the framework might have generated, mounted by hand, is still surface |
| `…:TestTheGateRefusesARouteMountedOutsideEveryVerifiedSurface` | the areas, with the control that declaring the probe as its own surface passes |
| `…:TestAPrefixIsNotAnExemptionForTheRoutesOutsideIt` | the prefixed `Verify` a transport calls refuses `/live` and `/favicon.ico`, with the control that `AtRoot` declarations satisfy it |
| `auth/http/authhttp/surface_test.go:TestARouteOutsideThePrefixStillHasToDeclareItsAccess` | configuring a prefix narrows where paths are read, not what is checked |
| `…:TestAnEndpointDeclaredAtRootIsCheckedByItsAbsolutePath` | an `AtRoot` declaration is matched unprefixed, and a stale one is still refused |
| `…:TestARouteOutsideThePrefixIsToldWhereToDeclareItself` | the refusal names `AtRoot`, with the control that a route under the prefix is not sent to the root |
| `auth/http/authhttp/surface_test.go:TestANeighbouringPrefixIsNotPartOfThisSurface` | `/api/v10` is another tree, with the control that `/api/v1/other` is still checked |
| `auth/http/authnet/binding_test.go:TestTheSurfaceCanBeServedWithoutHoldingTheEscapeHatch` | serving does not require the value that registers past the gate |
| `auth/http/authfiber/binding_test.go:TestTheHeadFiberGeneratesNeedsNoDeclaration` | the generated HEAD, with a control that it is really there |
| `auth/http/authnet/binding_test.go:TestARouteRegisteredPastTheSurfaceIsInvisibleToTheGate` | what the recorder cannot see, with a control that it sees the rest |
| `crud/http/crud{net,gin,fiber}/table_test.go:TestEveryRouteInTheTableIsMounted` | the table and `Register` agree, asked of the router |
| `…:TestAReadOnlyResourceMountsNothingTheTableOmits` | and agree about what is left out |
