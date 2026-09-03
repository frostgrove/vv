# D-107 — A CRUD resource declares the permissions its gate enforces

**Status:** accepted, narrows [[D-073]] · the CRUD half of [[D-100]], completed by [[D-109]]
**Invariant:** the permission a CRUD route declares and the permission the
repository's gate demands for that route's action are one list, read twice. A
mounted route whose action the policy does not declare is refused where the
declaration is built, not answered with a permission nothing enforces.

## The decision

`security.Policy` carries the requirement as data — `Requires
map[Action][]auth.Permission` — and the gate enforces it from there.
`RequirePermission` and `PerAction` became thin wrappers that fill that map;
`Requiring` is the explicit constructor under them, and a hand-written
`Policy{Requires: …}` is a complete, enforcing policy. The map is the source, and
`Authorize` is filled from it rather than instead of it — a consumer who tests
its own policy by calling `policy.Authorize` still can, and reads the same
answer the gate reads. What changed is the direction: the requirement is data
that a closure is derived from, not a closure that a value has to be guessed
out of.

`crudhttp.Table.GuardedBy(policy)` reads it. Every route the table mounts
carries the `crud.Action` it performs, the policy is asked what that action
requires, and the answer becomes the `authhttp.Endpoint`. `Guarded(read, write,
del)` did not go away — it is the same derivation over a three-permission
policy, which is what a consumer wants when the gate is somewhere else.

The action vocabulary moved to package `crud` (`crud.Action`, `crud.Actions`)
and `security.Action` is now an alias of it. That is the seam: `crudhttp` reads
a declaration in a vocabulary the core owns, through an interface `crudhttp`
declares itself, and never imports the decorator that produces it. A consumer
using a different authorization mechanism satisfies `crudhttp.Policy` and gets
the same derivation.

**An action the policy does not declare is an error, not an omission.**
`GuardedBy` collects every such route and refuses with all of them named. The
alternative — declaring the route with no permission — is the failure this is
here to prevent: a route that says it is guarded by nothing while the gate
behind it refuses everything, or worse, the reverse.

**A declared action with an empty permission list becomes `Authenticated`,**
not `Requires` with nothing in it. `RequirePermission()` naming no permission
means "any authenticated caller", and that is what `authhttp` has a constructor
for. `Requires` with an empty list fails `Endpoint.Declares` and would be
refused by the boot gate as an undeclared route, which is a true statement said
in the most confusing available way.

**`Combine` intersects the declared actions and unions the permissions.** That
is not a preference, it is what the chained authorizers already do: two
declaring policies each refuse an action the other declares alone, so an action
only one of them names is guarded by neither, and an action both name needs
both permissions. A declaration that said otherwise would be a lie about the
gate one line below it.

## Why

**Because two hand-written copies of a policy drift, and nothing here could
see it.** [[D-073]] says as much: "what no machine can check is which permission
guards which route, and that is what `Guarded` still asks a consumer to write."
That was true while the requirement lived in a closure. It is not a law of
nature — it was a property of where the value was stored. Moving it into data
makes the machine able to check it, and then the second copy has no reason to
exist.

**Because the drift is silent and its two directions fail differently.** A
declaration naming a stronger permission than the gate demands is a document
that lies to a reviewer; a declaration naming a weaker one is worse — it reads
as an audited surface, and the audit is of the wrong list. Neither shows up in a
test that only exercises the handler.

**Because the arrow points the way [[D-073]] requires.** What that decision
refuses is a request-time check derived from a declaration, and this is not
that: the policy value is the source, the gate reads it directly, and the
declaration is the derived artefact. [[D-100]] made the same inversion for an
`appfiber` operation, where the policy is stated at the route. Here the policy
is already stated at the repository, so the route has nothing left to say.

**Because the boot gate already refuses on the axis that is left.** [[D-073]]
compares declarations against the real routing table, so the paths are checked.
With the permissions derived, both halves of an endpoint come from something
that is enforced, and the consumer writes neither.

## What was rejected

**A `routes.manifest.yml` with `policy:`, `source: inferred-from-guard` and
`confirmed:`, on the model of `cache.manifest.yml`, *for a CRUD resource*.** A
manifest of that shape is a drift detector between two statements, and it earns
its keep exactly when the second statement cannot be removed — that is why the
cache has one ([[D-105]] and `internal/cachegen`). Here the second statement
*was* removed. Confirming a copy that no longer exists costs a file, a check and
a review step, and buys a record of what the code already derives. Worse, it
would not hold: the permissions come from a policy a package builds at run time,
so a generator reading source text would have to re-implement the constructors
to guess at them, and the guess would be the third copy.

That argument is about the CRUD half and only about it. Where the second
statement cannot be removed — an application operation guarding itself inside
its own body — the manifest is exactly the right tool, and [[D-109]] is it.

The part of the original proposal that survives is the failure it asked for. A
`confirmed: false` was to fail boot; an undeclared action fails where the
declaration is assembled, which is earlier and cannot be silenced by a stale
confirmation.

**Extending this to a use case that is not a CRUD resource.** An application
operation — a dead-jobs listing, a password reset — enforces its permission with
`access.Require` inside its own body, and there is no `Table` to derive a route
from. That half is [[D-109]]: `vv generate routes` reads the guard, pairs it
with the declaration beside it, and generates the operation both of them then
read. The two halves meet in the same place — the permission is one value — and
differ only in what derives it, because a resource has a table and an operation
has a guard.

## Proven by

- `TestTheAccessDeclarationIsDerivedFromTheGatesOwnPermissions` — the binding
  triplet's `table_test.go`. A policy naming a different permission per action;
  every mounted route is declared with the one its own action requires, which is
  what a table collapsing create and update into one "write" cannot produce.
- `TestAMountedRouteThePolicyLeavesUndeclaredIsRefusedAtAssembly` — same files.
  The refusal names the route, and the control is the same policy over a
  read-only resource, which declares every route it mounts.
- `TestTheThreePermissionShorthandCollapsesEveryWriteOntoOnePermission` — same
  files. `Guarded` is now the same derivation over a different policy, so what
  it always did is pinned rather than assumed.
- `TestTheDeclaredRequirementIsTheOneTheGateEnforces` —
  `crud/decorators/security/principal_test.go`. What `RequiredFor` answers is
  what the gate demands, with the caller who lacks it refused and the caller who
  holds it let through.
- `TestRequiringIsTheExplicitFormPerActionWraps` — same file. The explicit
  constructor under the wrapper, and that the wrapper declares what it declares.
- `TestADeclaredRequirementIsAlsoReachableAsTheAuthorizerAConsumerCalls` — same
  file. The declaration answers as `Policy.Authorize` too: the action it names is
  allowed to the caller holding it, the action it names differently and the
  action it never names are both refused, and an absent principal is refused
  before either.
- `TestADeclarationThatNamesNoActionRefusesEveryOne` — same file. A declaration
  present and empty refuses everything; it is the one shape that could have
  failed open.
- `TestCombiningDeclarationsKeepsOnlyWhatEveryDeclarationAllows` — same file,
  both directions of the intersection, each with the gate's own refusal beside
  it.

## See also

[[D-073]] [[D-100]] [[D-109]] [[D-030]] [[D-055]] [[FL-020]] [[FL-024]] [[FL-031]] [[UC-004]]
