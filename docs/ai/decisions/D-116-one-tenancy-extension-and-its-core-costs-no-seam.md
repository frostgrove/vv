# D-116 — One tenancy extension, in the root module, whose core costs no seam

**Status:** accepted
**Invariant:** There is exactly one tenancy extension and it lives in the root module. Its core, `tenancy/`, imports the standard library and `crud` — the stdlib-only contract tier whose error taxonomy decides what a refusal renders as — and no seam. Each seam it adapts is one package beside it: `tenancyrow`, `tenancydb`, `tenancyjobs`, `tenancystorage`, `tenancycache`. An adapter costs its own seam and no other, no base package imports any of them, and none of them imports a provider SDK, a router or a backend.

## The decision

`tenancy` is a package of `github.com/frostgrove/vv`, not a module of its own,
and it is a core with adapters rather than one package holding every seam. The
[multitenancy revision](../../roadmaps/2026-09-01-multitenancy-roadmap.md)
proposed `MODULE github.com/frostgrove/vv/tenancy`; that half of it is superseded
here. Everything else it fixes — one extension, one core, no
`tenancyjwt`/`tenancyotel`, topologies that are not modules — stands.

## Why it is not a module

**Because in this repository a module boundary is a third-party dependency
boundary, not an optionality boundary.** [[D-033]] and [[D-036]] say it directly:
a package that would add a third-party requirement becomes a module so a consumer
downloads only what it imports. `storage`, `cache`, `jobs`, `port` and
`crud/decorators/security` are all optional and all live in the root, because none
of them costs a consumer a dependency. Tenancy costs nothing either —
`go list -deps ./tenancy/...` names the standard library and first-party packages
and nothing else — so a module for it would be symmetry, which the
[extension architecture revision](../../roadmaps/2026-09-01-extension-architecture-roadmap.md)
already refuses in as many words: *"Stdlib-only implementations do not receive a
module merely for symmetry."*

**Because a module here would cost something real before the first tag.**
[[D-036]] measured it: every module that requires the root fails to walk its own
module graph until the root is tagged, and no `replace` or workspace edit fixes
it. A tenancy module would join `errs` in waiting for a tag it does not need.

**What would change the answer.** A provider adapter that takes a genuine
third-party dependency — a control-plane SDK, a secret manager, a migration
product, a PostgreSQL RLS helper that needs a driver — is a module at that point,
under `tenancy/`, exactly as `jobs/jobspg` became one for pgx. That is one
dependency decision per module, which is [[D-051]]. The move is mechanical: one
`go.mod`, one `go.work` line, and the `replace` lines `test/` and `_examples/`
already carry for the root.

## Why the core carries no seam

**Because optionality is a property of the import graph, and one package makes
every seam one decision.** A core that held the row policy, the directory, the
job seam, the object namespace and the cache partition would put all of them into
the graph of anything that imported it. That is not hypothetical weight:
`crud/decorators/security` reaches `auth`, `errs` and `internal/nilvalue`, so a
deployment that wanted nothing but a tenant-partitioned cache would compile the
authorization subsystem to get it. Splitting is measured rather than
symmetrical — `tenancyrow` is the only adapter that pays for `security`, and
`tenancydb` adapts the same subsystem for a different topology precisely because
it does not.

**Because the package a consumer imports is the decision it made.** Shared-row
narrowing, a database per tenant, durable work, object storage, a cache: the
import list says which, and nothing else arrives with it.
`scripts/tenancy_test.go:TestNoTenancyPackageCostsMoreThanTheSeamItNames`
computes each package's first-party graph and fails if it reaches past the core
plus its own seam — including reaching a sibling adapter.

This is the second criterion, and it is why `tenancydb` is a package although its
graph is the core's exactly: database-per-tenant is the topology a deployment
picks *instead of* shared-row, and `Directory` owns a mutex, a cache of open
sources and a `Close`, where nothing in the core outlives a call. Weight alone
would fold it back in. A package is admitted here when it costs more than the
core **or** when it names an alternative the deployment chooses, and by nothing
else — least of all by symmetry with a base seam.

**Because what the adapters need from the core is exported deliberately, and what
they must not have is not.** `Scope.Digest` identifies a tenant and a generation
for the two seams that address storage by it, and authorises nothing.
`Classify` is how every seam answers application-supplied code — a resolver, a
source factory, a fence — without letting its text travel. `Authority.Sealer`
keeps the durable key inside the authority and hands out a capability whose
`Seal` refuses a scope this authority did not mint, so an adapter cannot launder
one. The mint, the salt and the binding stay unexported, and no adapter can
construct a `Scope` ([[D-117]]).

**Because the direction is one-way.** `tenancy` and its adapters import base
seams; no base package imports any of them, and none of them exposes a
tenancy-owned type from a base seam.

## What it forbids

- Do not create a package whose identity is the intersection of tenancy with a
  third thing — `tenancyjwt`, `tenancyhttp`, `tenancyotel`, `tenancyaudit` — or
  the mirror image under another subsystem — `jobstenancy`, `cachetenancy`.
  An adapter package exists because a base seam exists; composition of two
  extensions happens in the application, and cross-extension scenarios are
  fixtures rather than packages ([[D-114]] refuses the same shape for OTel).
- Do not let an adapter import another adapter, or a second seam. Two seams in
  one package is the thing this decision undid.
- Do not let the core import a seam. The row policy, the directory, the job
  seam, the namespace and the cache key are adapters for exactly that reason,
  and the check that holds it is a test rather than a review.
- Do not import a provider SDK, a router binding or a backend from any of them.
- Do not make any base package import tenancy, or expose a tenancy-owned type
  from one.
- Do not split any of this into a module for tidiness. Split it when, and only
  when, a concrete third-party dependency is selected.

## Where it lives

- `tenancy/` — `doc.go`, `reference.go`, `scope.go`, `lifecycle.go`,
  `authority.go`, `context.go`, `errors.go`, `outcome.go`, `seal.go`, `grant.go`.
- `tenancy/tenancyrow/`, `tenancy/tenancydb/`, `tenancy/tenancyjobs/`,
  `tenancy/tenancystorage/`, `tenancy/tenancycache/` — one seam each.
- `scripts/checks.sh:SUBSYSTEMS` — what stops `utils/` importing any of it.
- `scripts/tenancy_test.go` — what stops a base subsystem importing it, and what
  measures each package's graph.

## Proven by

- `TestNoTenancyPackageCostsMoreThanTheSeamItNames` — the core reaches nothing
  but `crud`'s own graph, and each adapter reaches nothing but the core plus its
  seam. Both halves fail when violated: an import of `cache` added to the core is
  reported, and so is `security` added to `tenancydb`.
- `TestAPackageCostingMoreThanItsRowSaysIsReportedAndOneCostingExactlyItIsNot` —
  the walk both cost tables are read by, driven over a written tree instead of
  this repository, where the answer that is wanted is silence and silence is also
  what a deleted arm gives.
- `TestNoBaseSubsystemDependsOnTheOptionalExtension` — twelve subsystems listed,
  none of them reaching `tenancy` or anything under it.
- `make check-deps` — tenancy adds no external package to the root module's
  graph, which is the whole argument for it not being a module.
- `make check-tiers` — the contract tier still imports only the contract tier, so
  nothing in `crud`, `port` or `errs` gained a tenancy edge.
- `make check-utils` — with `tenancy` in `SUBSYSTEMS`, a package under `utils/`
  that imports it is a build failure rather than a code review.

## See also

[[D-033]] [[D-036]] [[D-051]] [[D-058]] [[D-114]] [[D-117]] [[FL-033]]
