# D-100 — A declaration and its enforcement are two projections of one policy

**Status:** accepted
**Narrows:** [[D-073]]
**Invariant:** in `appfiber`, an operation is registered by one call that carries
a `Policy`. The `authhttp.Endpoint` the boot gate compares and the outer check
that runs before the handler are both projections of that one value. Neither is
derived from the other, and the registrar has no signature that mounts a route
without stating a policy.

## The decision

`appfiber.Policy` is built by `Requires(permissions…)`, `Authenticated(why)` or
`Public(why)`. `RouteSet.GET(path, policy, handler)` — and the other verbs, and
`Handle` under them — records one operation. From that record the set produces
two things and nothing else does:

| Projection | What it becomes |
|---|---|
| `Policy.endpoint(method, path)` | the `authhttp.Endpoint` the boot gate compares against Fiber's table |
| `Policy.enforcement(renderer)` | a `fiber.Handler` in front of the route's own, which refuses a caller the policy does not admit |

The path is the same string in both, because the set concatenates the prefix once
and keeps the result.

`Routes(prefix)` is the shorthand and `NewRouteSet(spec)` is the constructor
under it. They share one builder; the shorthand carries a bad prefix to `Route()`
so a wiring chain reads as one expression, the constructor refuses it at the call.

## Why

**Because two lists is the defect, not two files.** `Route.Mount` and
`Route.Access` were written by hand next to each other, and the gate could only
report that they disagreed about *which paths exist*. It could not report that
the permission written in the declaration was not the permission the handler
checked, because nothing connected the two statements. Adding a third hand-kept
list would have made that worse. One value that both projections come out of
makes the disagreement unrepresentable rather than detectable.

**Because [[D-073]] bans a direction, not enforcement.** What that decision
refuses is a request-time check derived *from a declaration*: a declaration is an
audit artefact, and an audit artefact that starts making decisions becomes a
second authorization system which drifts from the first the day one of them is
edited. `Policy` inverts the arrow. The declaration is the derived thing, the
check is the other derived thing, and there is no step at which one is read to
produce the other — so there is nothing to keep in step.

**Because the router stays an independent witness.** The set records what it
registered; `authfiber.Routes` reads what Fiber actually holds. A route mounted
past the registrar — by a second contributed `Route`, or by touching the
`*fiber.App` — is still a start-up failure. That is what makes the registrar
worth using rather than merely convenient, and it is why `Mount` was not changed
to trust the set.

**Because an operation with no repository behind it had nowhere to be checked.**
[[UC-020]]'s rules live next to a repository. An operations endpoint that lists
dead jobs or restarts one touches no repository, so its permission could only be
checked in the body of the use case, by hand, and tied to the declaration by a
person reading two files and believing they matched.

**Because forgetting must not read as a decision.** A zero `Policy` is refused by
name, `Requires()` with no permission is refused, and `Public("")` is refused.
This is [[D-073]]'s rule about `Endpoint.Why` moved to the point where the
operation is written, so the refusal arrives while the author is still looking at
the line that caused it.

## What it forbids

- Do not give the route call a raw `permissions…` parameter. The mount and the
  declaration must both come out of the `Policy` value, or the registrar is
  a naming convention again.
- Do not derive a `Policy` from an `authhttp.Endpoint`, and do not let a request
  path read `Endpoint.Needs`. [[D-073]] stands in that direction and is not
  narrowed by this decision.
- Do not make `RouteSet` the only way to contribute. `Route` stays an interface:
  a module whose shape the set does not cover writes `Mount` and `Access` by
  hand, and the gate treats it identically.
- Do not read the outer check as a replacement for `security.Gate`. It answers
  "may this account call this operation at all", never "may it touch this row";
  a row it may not touch is still a 404 ([[D-008]]).
- Do not let the enforcement decide anything the policy did not say. It calls
  `auth.Require` and `auth.HasAll`, and refuses through the same `porthttp`
  renderer the rest of the surface uses.

## Where it lives

| File | What it holds |
|---|---|
| `app/http/appfiber/routeset.go` | `Policy`, `Requires`, `Authenticated`, `Public`, `RouteSetSpec`, `NewRouteSet`, `Routes`, `RouteSet` and its verbs, `Route`, `MustRoute`, `ErrRouteSet`, the shared `refuse` |
| `app/http/appfiber/appfiber.go` | `Mount` — unchanged, and still the witness |
| `auth/http/authfiber/surface.go` | `Routes`, `Verify` — Fiber's own table |

## Proven by

`app/http/appfiber/routeset_test.go`:

| Test | What it pins |
|---|---|
| `TestOneRegistrationBothMountsTheOperationAndDeclaresIt` | one call is both actions |
| `TestTheDeclaredPathIsTheMountedPath` | the two projections carry the same string |
| `TestAnOperationRefusesAPrincipalWithoutThePermissionItDeclares` | the declared permission is the enforced one |
| `TestAnOperationAdmitsAPrincipalHoldingThePermissionItDeclares` | the control: it lets the right caller through |
| `TestAnOperationThatNamesPermissionsRefusesAnAnonymousCaller` | no principal is a 401, not a pass |
| `TestAPublicOperationIsMountedWithoutAPermissionCheck` | `Public` mounts no check at all |
| `TestASignedInOperationTakesAnyPrincipalAndRefusesNone` | `Authenticated` checks the door and not the permissions |
| `TestASignedInOperationMustSayWhyBeingSignedInIsEnough` | and still has to say why that is enough |
| `TestAnOperationThatStatesNoPolicyIsRefusedAtTheBuild` | forgetting is not "no permission needed" |
| `TestRequiringNothingIsNotAWayToSayPublic` | `Requires()` is a mistake, not an opening |
| `TestAPublicOperationMustSayWhyItIsOpen` | the reason is required where the operation is written |
| `TestTheSameOperationCannotBeRegisteredTwice` | one path, one policy |
| `TestNewRouteSetRefusesAPrefixThatIsNotAPath` | the explicit constructor refuses now |
| `TestRoutesCarriesAPrefixMistakeToTheBuild` | the shorthand refuses at `Route()` |
| `TestARouteMountedPastTheRegistrarStillBreaksStartUp` | the router is still the independent witness |
| `TestAnOperationWithoutAHandlerIsRefused` | a declaration that answers nothing is refused |

## See also

[[D-073]] [[D-021]] [[D-020]] [[D-008]] [[D-074]] [[FL-024]] [[UC-020]]
