# D-117 — A verified scope is minted by an authority, never manufactured

**Status:** accepted
**Invariant:** A `tenancy.Scope` exists only where an `Authority` minted it, and every factory that *performs* work — the repository policy, the durable producer and restorer, the source directory, the grant loop — re-asks that authority whether the scope's lifecycle admits the class of work being asked for. `Namespace` and `Keyed` derive a name from a scope rather than performing work, so they take the scope itself and only refuse a zero one. There is no default tenant, no ambient tenant, and no path that succeeds without a scope.

## The decision

`tenancy.Scope` has unexported fields, no exported constructor and one internal
one: `Authority.mint`. Every scope carries a 32-byte binding —
`HMAC-SHA256(salt, origin ‖ reference ‖ lifecycle ‖ epoch)` — over a salt drawn
from `crypto/rand` when the authority is constructed and never leaving it. Every
consumer calls `Authority.accept`, which recomputes the binding and compares it
in constant time.

The trust authority the application injects is a `tenancy.Resolver`, and it
returns a `tenancy.Resolution` — plain data: a reference, a lifecycle state and
an epoch. It never returns a `Scope`. That is the whole reason the type cannot be
manufactured: if application code could return the accepted type, the type would
be constructible by application code.

Three more rules fall out of the same place:

- **Lifecycle admission is a whitelist, per operation class.** `Admission` is
  three sets — read, write, durable — and the default is `Active` in all three.
  A state nobody enumerated, including a `Lifecycle` value no version of this
  package declares, is refused rather than admitted.
- **Epoch is checked at each explicit boundary, and pinned in between —
  by default.** A request binds once; durable work re-derives at handler entry; a
  grant re-checks per cohort member. Between boundaries the carried lifecycle and
  generation hold, because asking the control plane once per statement makes it a
  hot dependency of every request, and a unit of work whose tenant can move
  underneath it has no atomicity to speak of. `Spec.Revalidate` is the other
  trade, offered rather than assumed: with it every verb re-asks the resolver, and
  a tenant deleted or restored mid-request stops the next one with `ErrStale` or
  `ErrInactive`. Whichever a deployment picks, the window is a number it can
  state — one `Bind`, or zero.
- **Admission is checked for consistency at construction.** A state admitted for
  *writing* but not for reading is refused, naming the state: every mutating verb
  resolves its narrowing predicate for the read class first, so such a policy would
  silently mean something narrower than it says. Durable work is deliberately not
  held to that floor — its producer asks for the durable class directly and reads
  nothing first, so "durable work during a migration, no reads and no writes" is a
  policy a deployment may genuinely mean.
- **A durable reference is authenticated with a configured key.** `Spec.DurableKey`
  is what the producer MACs the reference and generation with and the worker
  verifies. It cannot be the per-process scope salt, because the worker is a
  different process started later. Without it the durable seam refuses to be
  constructed, because the queue table is writable by whatever else reaches that
  database and an unauthenticated reference in a record is tenant impersonation.
- **A grant names the classes it permits, and they travel with the work.** A
  purpose is a label, not a permission; a read-only cohort job that reaches a
  write verb is refused by the class, not by the name.
- **A refusal names a kind and nothing else.** The ten sentinels in
  `tenancy/errors.go` are the vocabulary; a resolver's own error text — which is
  where the DSN, the tenant and the credential are — is collapsed to
  `ErrUnavailable` unless the resolver deliberately returned one of the
  sentinels.

## Why

**Because every other control rests on this one.** Narrowing, ownership,
namespaces, partitions and durable identity all begin by reading a scope. If a
scope can be written by the code being constrained, the extension is advice.
Unexported fields alone do not achieve that: `var s tenancy.Scope` compiles
anywhere, and a zero value that passed would be a tenant with an empty reference.
The binding is what makes the zero value fail, and it is what makes a scope from
a test double, another environment or another process fail with it.

**Because the alternative was tried in the shape the roadmap warned about.** The
[multitenancy revision](../../roadmaps/2026-09-01-multitenancy-roadmap.md)
requires "test-only construction", which in Go means an exported constructor that
production can also reach. There is none here and none is needed: a test wires a
`tenancy.Fixed` resolver into an ordinary `Authority` and gets a real scope by the
one path production uses. The forge that would have leaked was never built.

**Because a durable record is a reference and not an authority.** The job
identity restorer reads back the reference and the generation the producer wrote,
and then asks the control plane what is true now. A tenant suspended, deleted or
restored since the work was enqueued refuses at execution. A backlog does not undo
a suspension.

That lifecycle re-check is all this paragraph ever bought, and the sentence that
used to stand here — "queue write access is therefore not tenant impersonation" —
claimed more than it. It held against a *suspended* tenant and not at all against
an *active* one: the binding was the queue and the definition, so an honest token
lifted off one row and reattached to a row the attacker wrote verified, and the
control plane, asked about a tenant that really is active, answered yes. What
closes that is the record binding in [[D-119]], not this paragraph.

**Why a per-authority salt rather than a signature over a shared key.** There is
no key to share. A scope never leaves the process — it is a context value, not a
wire format, and it refuses to marshal — so the binding only has to distinguish
"this authority made it" from everything else, and a random salt does that
without a configuration item nobody would rotate.

## What it forbids

- Do not add an exported `NewScope`, `ScopeFor` or test constructor. A test that
  needs a scope wires a resolver.
- Do not let a `Resolver` return a `Scope`. It returns a `Resolution`.
- Do not cache a resolution inside the framework, or add a TTL to `Authority`. The
  two windows on offer are "one `Bind`" and "every verb"; a deployment that wants
  something between them puts it in its own resolver and owns the bound it is then
  claiming.
- Do not let a factory that performs work take a bare `Scope`, and do not export
  one that maps a scope to an address without checking it — `From(ctx)` is public,
  so the value such a function needs is always one call away. Holding a scope says
  an authority minted it, not that this authority admits this class of work now.
- Do not run a cohort grant from a context that is already bound to a tenant. It
  is refused, because the alternative — entering nobody and returning no error —
  is a billing run that bills nothing and says it worked.
- Do not put an unauthenticated reference into a durable record.
- Do not widen `Admission` with a wildcard or an "all states" convenience. The
  states are enumerated where the deployment can see them.
- Do not wrap a resolver's error into a refusal. The sentinel is the answer; the
  detail belongs to the protected channel the application owns.
- Do not add a scope-less bootstrap mode. Provisioning admits `Provisioning` for
  the write class, or it uses a grant. A second way past the narrowing is the
  thing this decision exists to prevent.

## Where it lives

- `tenancy/scope.go` — `Scope`, `Resolution`, `bind`, `Scope.boundTo`.
- `tenancy/authority.go` — `Resolver`, `Spec`, `Authority`, `New`, `Verify`,
  `Lookup`, `mint`, `accept`, `Fixed`.
- `tenancy/lifecycle.go` — `Lifecycle`, `Class`, `Admission`.
- `tenancy/context.go` — `Bind`, `With`, `Scope`, `From`.
- `tenancy/errors.go` — the refusal vocabulary and `Classify`.
- `tenancy/seal.go` — `Sealer`, `Seal`, `Unseal`: where a scope becomes a record
  another process can check, and where the durable class is re-asked before it
  does. `Unseal` answers a reference and a generation, never a `Scope`.
- `tenancy/tenancyjobs/jobs.go` — `ContextProvider`, `IdentityRestorer`, where
  the durable reference is authenticated and then re-derived anyway.
- `tenancy/grant.go` — `Accept`, `Each`, `permittedByGrant`, `grantBinding`.

## Proven by

- `TestOnlyTheAuthorityThatMintedAScopeAcceptsIt` in `tenancy/scope_test.go` — a
  zero value and a scope from a second authority are both refused, with the
  authority's own scope as the control.
- `TestOnlyAnAdmittedLifecycleReachesWork`, same file — every declared state plus
  an undeclared one, against every operation class.
- `TestAdmissionIsDecidedPerOperationClass`, same file — a deployment that keeps a
  suspended tenant readable still refuses its writes.
- `TestAResolverFailureNeverTravelsBackAsText`, same file — a resolver error
  carrying a tenant, a database name and a password does not reach the refusal,
  and a sentinel the resolver chose deliberately does.
- `TestNothingCarryingATenantRendersIt`, same file — `String`, `Format`, `%+v`
  and `json.Marshal` for the scope and the reference, with the control that the
  reference can still be read by a policy.
- `TestBindingASecondTenantInsideBoundWorkRefuses`, same file.
- `TestWorkWithNoScopeInContextRefuses`, same file — including immediately after a
  successful operation for another tenant.
- `TestNoVerbRunsWithoutAVerifiedScope` in `tenancy/tenancyrow/row_test.go` —
  fifteen verbs, each refused with zero statements executed.
- `TestOnlyAScopeTheDurableClassAdmitsIsSealed` in `tenancy/seal_test.go` — the
  core primitive refuses a lifecycle the durable class does not admit, rather
  than relying on each adapter to ask first.
- `TestARecordSealedInAnotherDeploymentIsNotRead`, same file — a copied
  `DurableKey` is not enough, because `Origin` is inside the seal too.
- `TestWorkEnqueuedByATenantIsExecutedAsThatTenant`,
  `TestAProducerWithNoTenantEnqueuesNothing`,
  `TestWorkEnqueuedBeforeASuspensionDoesNotRunAfterIt`,
  `TestAForgedDurableRecordEntersNoHandler` and
  `TestAProducerRefusesToCaptureAScopeTheDurableClassDoesNotAdmit` in
  `tenancy/tenancyjobs/durable_test.go` — the producing and executing halves,
  through a real queue and the in-memory backend rather than a hand-built
  record.
- `TestADatabaseIsNotHandedToAScopeTheClassNoLongerAdmits` in
  `tenancy/tenancydb/database_test.go` — the directory re-asks the authority, because under
  database-per-tenant there is no row policy below it to catch a lifecycle that
  admits reads and not writes.
- `TestEveryPartOfAGrantIsInsideItsBinding` in `tenancy/grant_binding_test.go` —
  a member appended, swapped or removed, a deadline pushed out and a purpose
  widened are each refused by the binding rather than by the salt.

## See also

[[D-008]] [[D-055]] [[D-115]] [[D-116]] [[FL-033]] [[UC-004]]
