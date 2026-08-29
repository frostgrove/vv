# D-073 — A mounted route declares its access, or start-up fails

**Status:** accepted
**Invariant:** every route mounted under a verified prefix names either the
permissions it requires or the reason it is open, and the router is compared
against those declarations at assembly. A route nobody declared is a start-up
failure; so is a declaration whose route no longer exists. The check runs once,
at assembly, and never per request.

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
writes three permissions rather than ten routes.

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
added. What no machine can check is which permission guards which route, and that
is what `Guarded` still asks a consumer to write.

## What it forbids

- Do not move the comparison to request time, and do not add a second per-request
  enforcement path derived from these declarations. `auth.Guard` and
  `security.Gate` are the runtime; this is the audit.
- Do not let `Verify` accept a zero `Endpoint`. "I forgot" must not be able to
  pass as "no permissions needed" — that is what `Why` being required for an open
  endpoint buys.
- Do not derive a declaration from the router. It would agree always, including
  when both are wrong.
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
| `auth/http/authhttp/surface.go` | `Endpoint`, `Route`, `Public`, `Requires`, `Authenticated`, `Verify`, `UnderPrefix`, `ErrSurface` |
| `auth/http/authfiber/surface.go` | `Routes`, `Verify` — Fiber's own table, HEAD and OPTIONS filtered |
| `auth/http/authgin/surface.go` | `Routes`, `Verify` — Gin's own table |
| `auth/http/authnet/surface.go` | `Surface`, `Over`, `AnyMethod` — the recorder, and what it cannot see |
| `crud/http/crudhttp/table.go` | `Table`, `Route`, `Need`, `Guarded` — a CRUD resource's ten routes |
| `app/http/appfiber/appfiber.go` | `Route.Access`, and the `Mount` that refuses to start |

## Proven by

`auth/http/authhttp/surface_test.go` — both directions, the trailing slash, the
prefix and its control, every problem reported at once.

`TestTheGatePassesWhenEveryMountedRouteIsDeclared`,
`TestTheGateRefusesARouteNobodyDeclared` and
`TestTheGateRefusesADeclarationThatMountsNothing` in all three HTTP auth
bindings, which `make check-triplets` keeps in step.

`TestEveryRouteInTheTableIsMounted` and
`TestAReadOnlyResourceMountsNothingTheTableOmits` in all three CRUD bindings:
the table and `Register` are checked against each other by asking the router.

`TestStartUpFailsWhenARouteDeclaresNothing` and
`TestStartUpFailsWhenADeclarationMountsNothing` in `app/http/appfiber`, which are
the same rule at the point an application actually meets it.

## See also

[[D-021]] [[D-020]] [[D-055]] [[D-056]] [[D-058]] [[D-074]]
