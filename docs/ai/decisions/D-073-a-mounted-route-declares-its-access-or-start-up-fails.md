# D-073 — A mounted route declares its access, or start-up fails

**Status:** accepted, narrowed by [[D-100]], [[D-107]] and [[D-109]]
**Invariant:** every mounted route names either the permissions it requires or the
reason it is open, and the router is compared against those declarations at
assembly. A route nobody declared is a start-up failure; so is a declaration whose
route no longer exists. A configured prefix says where the relative paths are
read from — it exempts nothing. The check runs once, at assembly, and never per
request.

## The decision

`authhttp.Endpoint` is a declaration — a method, a path, and either
`[]auth.Permission` or a written reason. `authhttp.Verify` compares a set of them
against what a router actually registered and refuses both directions of
disagreement at once, with every problem in one message.

The half that reads a real routing table is the binding's, because it is the only
half that cannot be written once: `authfiber.Routes`, `authgin.Routes`, and on
net/http a recording `authnet.Surface`, because a `http.ServeMux` cannot be
asked what it holds.

`crudhttp.Table.Guarded` supplies the paths of a CRUD resource so that a consumer
writes three permissions rather than ten routes, and `crudhttp.Table.GuardedBy`
writes none of them: it reads the permissions off the policy the repository's
gate enforces ([[D-107]]).

**A prefix is a path prefix, not a string prefix.** `UnderPrefix("/api/v1")`
covers `/api/v1` and `/api/v1/...` and nothing else; `/api/v10` belongs to
another tree and is not pulled into this one's verification.

**And a prefix is not an exemption.** What lives outside the API prefix is `/`,
`/favicon.ico`, `/live` and `/ready` — the part of the tree an anonymous caller
reaches first, and the part nobody declared. So `Verify` under a prefix does not
stop at its edge: a mounted route the prefix does not cover is compared against
the declarations marked `AtRoot`, whose paths are absolute, and one nobody wrote
is the same start-up failure as any other undeclared route. `AtRoot` is a
decorator over `Public`, `Requires` and `Authenticated` rather than three more
constructors, and it travels in the same `[]Endpoint` a module already returns —
so a composition root that only calls `Verify` under a prefix can declare its
probes without changing how it is wired. The refusal says so itself: a route the
prefix does not cover is reported with the seam that declares it appended to the
message, because this check is met for the first time on a deployment that has
just stopped starting, and a refusal that names only the route reads as a wall.

`VerifyAreas(mounted, areas...)` is the explicit form under that convenience:
each mounted route belongs to the most specific `Area` whose prefix covers it,
`Rooted` is the catch-all whose declarations are absolute paths, and a route no
area covers at all is a refusal of its own — `is mounted outside every verified
surface`. Two areas whose prefixes cover each other are refused rather than
resolved, because which of them checks a route would otherwise be an accident of
iteration order.

## Why

**Because a missing check and a deliberate omission look identical from the
inside.** A route added without an authorization check compiles, passes review
and serves. The only place the gap is visible is from outside, by somebody who
tries it. Nothing in a type system distinguishes "this endpoint needs no
permission" from "nobody thought about this endpoint", so the distinction has to
be written down, and writing it down is only worth anything if something checks
it.

**Because the check must fail at start-up, not at request time.** [[D-021]] is
the rule: the magic in this library fails at build or start-up, never on a
request. A per-request check would be a second authorization system to keep
correct, and a check that logged instead of refusing is a warning nobody reads on
a deployment that is already serving the undeclared endpoint.

**Because the stale half matters as much as the missing half.** A declaration
that outlives its handler is what makes a list look complete while it covers less
every month. Refusing only "mounted and undeclared" would leave a reviewer
reading a document that is confidently wrong. Both directions, or neither.

**Because the two statements have to be arrived at independently.** The router is
read, not recorded, on the two bindings that can be read. A recorder wrapped
around registration would agree with the declaration exactly when both were
wrong. `authnet` cannot do this and says so — that is a documented limit of that
binding, pinned by a test, not a silent difference.

**Because the paths are the part that rots and the permissions are the part that
matters.** [[D-020]]'s instinct — compare two independent statements — argues for
writing the routes out twice. Measured against what actually goes wrong, it is
the wrong place to spend it: the paths are checked against the real routing table
already, and a hand-kept second copy of them goes stale the first time a route is
added. What no machine could check, while the requirement lived inside an
authorizer closure, is which permission guards which route — and that is what
`Guarded` asked a consumer to write. [[D-107]] moved the requirement into data,
so a CRUD resource now derives its permissions from the gate that enforces them
and `GuardedBy` writes them for it. For a route that is not a CRUD resource,
[[D-109]] answers the same question the other way: the guard inside the use case
is read, the declaration beside it is paired with it, and `vv generate routes`
writes the one operation both then name. The sentence stands only until a
package has been through that generator.

## What this proves, and what it does not

The gate compares two statements about *which routes exist*. It does not, and
cannot, prove that the handler behind a declared route asks anybody's permission
before answering. `Endpoint.Needs` is read by `Declares` and by the duplicate
message, and nowhere else: no runtime path derives anything from it.

That is the design and not an oversight, and it is worth writing down because the
gap looks like a bug to every reader who finds it. The runtime is elsewhere:

| What refuses at request time | Where |
|---|---|
| no credential, or one that does not verify | `auth.Guard` — [[FL-019]] |
| a principal without the permission an endpoint needs | `access.Require(ctx, perms...)` in the handler or the app use case |
| a principal without the permission a CRUD route's action needs | `security.Gate(policy)`, which is where `GuardedBy` read the declaration from — [[D-107]] |
| a principal reading or writing a row it may not | `security.Gate(policy)` around the repository — [[FL-020]] |

The obligation this leaves the consumer is real and belongs to them: **every
endpoint that declares `Needs` must reach one of those three before it answers**,
and the thing that holds it is a test over their own surface — a caller without
the permission gets 403 or 404 — not a mechanism here. A framework check cannot
see it: `access.Require` and `security.Gate` are two different runtimes, an
endpoint may legitimately use either, and a gate's refusal is a 404 by design
([[D-008]]), so "no check ran" and "the check ran and the row was out of scope"
are indistinguishable from outside the handler.

It also does not prove that anything authenticated the caller before a declared
route answered. The gate reads a table of routes, and a middleware is not a
route: an enforcement contribution that never reached the chain — one whose
handler came out nil on a wiring branch, or that was never contributed at all —
is invisible to it in both directions. Whether a contribution that declares
enforcement and carries no handler is a start-up failure is the composition
root's question, because the contributions are the only thing it holds and the
gate never sees them ([[FL-024]]). That question is now answered: `appfiber.Mount`
refuses to start and names the contribution, because it is the one place that can
tell "a middleware named `guard` was contributed" apart from "no middleware was".
A contribution that was never made at all remains outside anything's reach, which
is why a surface registered through [[D-100]]'s registrar also fails closed on its
own — its outer check asks `auth.Require`, and a request carrying no principal is
refused — while a hand-written `Mount` still has nothing equivalent.

## What it forbids

- Do not move the comparison to request time, and do not add a second per-request
  enforcement path derived from these declarations — not as an opt-in wrapper
  either. `auth.Guard`, `access.Require` and `security.Gate` are the runtime;
  this is the audit. A second path would be a second authorization system to keep
  correct, and the two would disagree the first time one of them was updated.
  **[[D-100]] narrows this to the arrow it is about.** What is forbidden is
  reading a declaration to decide a request. A registrar that produces *both* the
  declaration and an outer check from one policy value adds no second statement
  to keep correct, because there is no second statement: nothing reads
  `Endpoint.Needs`, and the two projections cannot drift.
- Do not read this decision as a claim that a declared route is an enforced one.
  It is a claim that a mounted route is a *considered* one.
- Do not let `Verify` accept a zero `Endpoint`. "I forgot" must not be able to
  pass as "no permissions needed" — that is what `Why` being required for an open
  endpoint buys.
- Do not derive a declaration from the router. It would agree always, including
  when both are wrong.
- Do not let a prefix become an exemption. Narrowing `Verify` to what a prefix
  covers is how `/`, `/favicon.ico`, `/live` and `/ready` served for a year
  without anybody having said why they are open: the routes that need the
  declaration least are the ones a prefix hides. A surface outside the prefix is
  declared with `AtRoot` or as its own `Area`; it is never skipped.
- Do not compare a prefix with a bare string prefix, and do not filter a method
  out of a binding's table because the framework *might* have generated it. What
  a consumer mounted by hand declares its access, whatever its verb: an OPTIONS
  handler is a route, and so is a HEAD with no GET beside it. Fiber's generated
  HEAD is recognised by its shape — a HEAD whose path also carries a GET —
  because `autoHead` is unexported. The one case that shape cannot separate is a
  hand-written HEAD on a path that also serves GET, and that is an accepted
  limit, not an omission: it is covered by that path's GET declaration.
- Do not make `authnet`'s recorder look like the other two. What it cannot see is
  a real gap and is pinned by
  `TestARouteRegisteredPastTheSurfaceIsInvisibleToTheGate`; hiding it would make
  the guarantee untrue on one transport and unstated everywhere.
- Do not put the declaration types in `port/porthttp`. They name
  `auth.Permission`, and `make check-tiers` seals the contract packages against
  importing `auth`.

## Where it lives

| File | What it holds |
|---|---|
| `auth/http/authhttp/surface.go` | `Endpoint`, `Route`, `Public`, `Requires`, `Authenticated`, `AtRoot`, `Verify`, `UnderPrefix`, `Area`, `Under`, `Rooted`, `VerifyAreas`, `ErrSurface` |
| `auth/http/authfiber/surface.go` | `Routes`, `Verify`, `VerifyAreas` — Fiber's own table, with the HEAD it generates for a GET filtered |
| `auth/http/authgin/surface.go` | `Routes`, `Verify`, `VerifyAreas` — Gin's own table, unfiltered |
| `auth/http/authnet/surface.go` | `Surface`, `Over`, `Handler`, `AnyMethod` — the recorder, what it cannot see, and the sealed way to serve it |
| `crud/http/crudhttp/table.go` | `Table`, `Route`, `Policy`, `Guarded`, `GuardedBy` — a CRUD resource's ten routes, and the permissions derived for them ([[D-107]]) |
| `app/http/appfiber/appfiber.go` | `Route.Access`, and the `Mount` that refuses to start |
| `app/http/appfiber/routeset.go` | `Policy` and `RouteSet` — one call that mounts, declares and enforces ([[D-100]]) |

## Proven by

`auth/http/authhttp/surface_test.go` — both directions, the trailing slash, the
prefix and its control, every problem reported at once.
`TestARouteOutsideThePrefixStillHasToDeclareItsAccess` is that a prefix exempts
nothing, and `TestAnEndpointDeclaredAtRootIsCheckedByItsAbsolutePath` is the
`AtRoot` declaration that answers for it, with the control that a stale one is
refused. `TestARouteOutsideThePrefixIsToldWhereToDeclareItself` is that the refusal names
`AtRoot`, with the control that a route under the prefix is not sent to the root.
`TestANeighbouringPrefixIsNotPartOfThisSurface` is the segment boundary,
with the control that a route genuinely under the prefix is still checked;
`TestARouteMountedOutsideEveryVerifiedSurfaceIsRefused`,
`TestARootSurfaceDeclaresWhatLivesOutsideThePrefix` and
`TestTwoVerifiedSurfacesThatOverlapAreRefused` are the areas.

`TestTheGatePassesWhenEveryMountedRouteIsDeclared`,
`TestTheGateRefusesARouteNobodyDeclared`,
`TestTheGateRefusesADeclarationThatMountsNothing`,
`TestAHandMountedHeadOrOptionsRouteMustDeclareItsAccess`,
`TestTheGateRefusesARouteMountedOutsideEveryVerifiedSurface` and
`TestAPrefixIsNotAnExemptionForTheRoutesOutsideIt` — the last one over the
prefixed `Verify` a transport actually calls — in all three HTTP auth bindings,
which `make check-triplets` keeps in step. The Fiber-only control
that the generated HEAD is still exempt is
`TestTheHeadFiberGeneratesNeedsNoDeclaration` in its `binding_test.go`, and
`TestTheSurfaceCanBeServedWithoutHoldingTheEscapeHatch` in `authnet`'s is the
sealed handler beside the escape hatch this decision refuses to remove.

`TestEveryRouteInTheTableIsMounted` and
`TestAReadOnlyResourceMountsNothingTheTableOmits` in all three CRUD bindings:
the table and `Register` are checked against each other by asking the router.

`TestStartUpFailsWhenARouteDeclaresNothing` and
`TestStartUpFailsWhenADeclarationMountsNothing` in `app/http/appfiber`, which are
the same rule at the point an application actually meets it,
`TestAMiddlewareThatCameOutWithoutAHandlerStopsTheStart` in the same package,
which is the one thing the gate structurally cannot see, and
`TestARouteMountedPastTheRegistrarStillBreaksStartUp` in the same package, which
is the control that the router is still read rather than recorded now that a
registrar exists.

## See also

[[D-021]] [[D-020]] [[D-055]] [[D-056]] [[D-058]] [[D-074]] [[D-100]]
