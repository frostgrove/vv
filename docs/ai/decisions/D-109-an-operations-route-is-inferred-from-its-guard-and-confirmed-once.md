# D-109 — An operation's route is inferred from its guard and confirmed once

**Status:** accepted, completes [[D-107]] for what is not a CRUD resource
**Invariant:** the permission an application operation enforces and the
permission its route declares are one value. Where they are still two, the pair
is derived by `vv generate routes`, recorded in `routes.manifest.yml`, and
nothing compiles until a person has confirmed it once.

## The decision

A use case that is not a CRUD resource — a dead-jobs listing, a password reset —
enforces its permission imperatively inside its own body, and there is no
`Table` to derive a route from. [[D-107]] closed the resource half by deleting
the second copy; here the second copy cannot be deleted by a library, because
the two statements live in the consumer's own packages and neither of them is
the framework's to write.

So the framework generates the value they can both read.

`vv generate routes` parses a package and finds two things:

- **the guard** — a call to the configured guard function (`-guard`, by default
  `auth/access`, `-guard-func Require`) inside a use-case body. Its arguments
  after the context are the policy. This is ground truth: it is the check that
  actually runs.
- **the declaration** — a literal `authhttp.Requires(method, path, …)` beside
  it. This is the route.

A guard and a declaration naming the same permission set are the same operation,
and that pairing is written to `routes.manifest.yml` as `source:
inferred-from-guard`, `confirmed: false`. Confirmed, the generator emits
`vv_routes_gen.go`: one `Operation` value per row carrying name, method, path
and permissions, plus `Operations()` and `Declarations()`. The handler returns
`Declarations()`; the use case calls the guard with
`OperationX.Permissions()...`. From that point the linkage is a compiler fact,
the manifest row's `source` becomes `bound-to-operation`, and the confirmation
is not asked for again.

**Inference is not a guess that stands on its own.** Pairing a guard with a
route is a human judgement, which is exactly what a confirmation is for. The
generator does not act on an inference nobody has signed, and it makes the
unsigned state impossible to run past rather than merely reported.

**An unconfirmed operation leaves a file that does not compile.** The written
artefact is `var VVRouteSet vvRouteSet = "confirm every operation in
routes.manifest.yml"`. That is `cachegen`'s shape verbatim ([[FL-025]]) and it
is deliberate here, where [[FL-029]] chose the opposite: the wire generator
leaves the previous valid file alone, because a stale public body is a smaller
hazard than a broken build. A stale *route* is the hazard — it is a permission
claim nobody re-read — so it stops the build instead of waiting for a review.

**The confirmation is over what the generator derived, and only that.** The
fingerprint covers the operation's name, the guard's policy and the route
*inferred* from a declaration. A method and a path a person wrote into the
manifest for an operation the generator could not pair are outside it — with
them inside, filling in a route would invalidate the confirmation written in the
same edit, and the row could never be confirmed at all.

**A declaration whose permission no use case enforces is refused, not
generated.** That is the defect this exists to catch, in the direction that
reads worst: a documented, audited endpoint whose audit is of a list nothing
checks. It fails where the manifest is built, naming the route and the file and
line it is written at.

**A permission the generator cannot resolve stops the run.** The policy is kept
as the expressions a person wrote, and `perm.Read` is only a permission once
`perm` is a package. An import alias is bound per file, so two files in one
package may bind one alias to two paths; the generated file has a single import
block and can carry only one of them. Choosing — by file order or by anything
else — publishes an operation under a permission its guard never named, and
`authhttp.Requires` then declares the route under that one. So the run is
refused, naming both import paths and the file each is written in. The refusal
is over the aliases a policy is written with, not over every import in the
package: a collision the generated file never has to spell changes nothing.

**The guard package is a flag, not an import.** `internal/codegen` names
`auth/access` as a default string and links to nothing. A consumer whose
authorization lives elsewhere passes `-guard`; the generated file imports only
`auth` and `authhttp`. The generator is a tool, and a tool that hard-wired one
authorization package would be the `otel_tenant_eventsource_` shape in a
different costume.

## Why

**Because the audit's evidence was two lists and a hope.** `access.Require(ctx,
PermJobsRead)` in a use case and `authhttp.Requires(GET, "/ops/jobs/dead",
PermJobsRead)` in a handler are the same sentence written twice by a person, and
nothing in the repository could see that they had stopped agreeing. Now the
first one is read, the second is derived from it, and a change to either
withdraws the confirmation that joined them.

**Because a manifest is the right tool exactly where the second statement
cannot be removed.** [[D-107]] rejected a manifest for the CRUD half and was
right to: there the second statement went away, and confirming a copy that does
not exist buys nothing. That argument does not carry here, and reading it as if
it did is how this half stayed open. The cache has a manifest for this same
reason ([[FL-025]]), and so does the public wire body ([[D-105]]).

**Because the end state is better than the manifest.** A confirmation is a
person vouching for a pairing a machine could not prove. Once the use case reads
`OperationX.Permissions()` the machine can prove it, and the manifest stops
asking. The confirmation is scaffolding for the migration, not a permanent tax —
which is what distinguishes it from a routine that would collect signatures
forever.

**Because generation refuses earlier than boot.** The audit asked for a boot
that fails on `confirmed: false`. A file that does not compile fails earlier
than boot, cannot be reached by a binary that was built before the manifest
changed, and cannot be silenced by a stale confirmation elsewhere.

## What was rejected

**Parsing the whole call graph to pair a handler method with the use-case method
it calls.** It would remove the confirmation for the common case and be wrong
silently for every other one — a handler that calls two use cases, a use case
called from two handlers, a guard behind an interface. Pairing on the permission
set is a weaker inference that knows it is weak and asks.

**Making the manifest the source of the policy.** Then the guard would be
derived from a file, and [[D-073]]'s rule — that what runs is the source and the
document is derived — would be inverted. The guard stays ground truth; the
manifest records only what was paired with what.

**Type-checked loading with `go/packages`.** The model generator reads source
text with `parser.ParseDir` and this follows it. A permission is named by an
identifier, and comparing identifiers is what the pairing needs. The cost is
real and named in the roadmap for the model generator; it is the same cost here.

## Proven by

- `TestTheRouteOfAnOperationIsInferredFromTheGuardThatEnforcesIt` —
  `internal/codegen/routes_test.go`. The policy comes out of the guard, the
  route out of the declaration beside it, and the row arrives unconfirmed.
- `TestAnUnconfirmedOperationLeavesAFileThatWillNotCompile` — the placeholder,
  and that no operation was published in it.
- `TestAConfirmedOperationBecomesTheOneValueTheRouteAndTheGuardBothRead` — the
  carrier, with the placeholder gone as the control.
- `TestChangingThePermissionTheGuardEnforcesWithdrawsTheConfirmation` — the
  fingerprint moves with the guard, the confirmation drops, and the previous
  carrier is replaced by the file that stops the build.
- `TestFillingInARouteTheGeneratorCouldNotInferDoesNotWithdrawItsOwnConfirmation`
  — the reason the fingerprint is over the inference and not over the row.
- `TestADeclarationNamingAPermissionNoUseCaseEnforcesIsRefused` — the second
  list, caught and named, with nothing generated.
- `TestAGuardBoundToTheGeneratedOperationIsNotConfirmedAgain` — the end state.
- `TestTheGeneratedCarrierCompilesAsBothTheDeclarationAndTheGuardsArgument` — a
  real module is generated and built with the handler returning `Declarations()`
  and the use case guarding on `Operation.Permissions()`.
- `TestTheRouteCheckReportsDriftWithoutWriting` — `-check`, with the pair it had
  just written passing as the control.
- `TestTheWalkNamesEveryPackageWaitingForConfirmationAndSkipsTheOnesWithNoGuard`
  — every package waiting is named in one run, and a package that guards nothing
  is left without a manifest.
- `TestAPackageThatAlreadyDeclaresOperationsKeepsItsOwn`,
  `TestOneUseCaseCannotEnforceTwoPoliciesUnderOneName` — the two refusals that
  keep the generated names and the operation identity honest.
- `TestAnAliasThatNamesTwoPackagesIsRefusedRatherThanReadFromWhicheverFileSortsFirst`,
  `TestACarriedPolicyWhoseQualifierNamesTwoPackagesIsRefusedBeforeTheCarrierIsRewritten`
  — an alias two files bind to two paths is refused where the guard is read and
  again where a carried policy is rendered, with
  `TestAnAliasCollisionNoPolicyReadsLeavesGenerationAlone` as the control that
  the refusal is over the aliases a policy names.
- `TestTheRoutesSubcommandRefusesAnInferredPairNobodyConfirmed`,
  `TestTheRoutesSubcommandHasItsOwnFlags` — `cmd/vv/main_test.go`.

## See also

[[D-107]] [[D-073]] [[D-100]] [[D-105]] [[D-050]] [[FL-031]] [[FL-029]]
[[FL-025]] [[FL-024]]
