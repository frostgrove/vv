# Multitenancy roadmap — linear optional extension — 2026-09-01

**Status:** M0, M1, M2 and M3 delivered; M4 and M5 open. The `tenancy` package
exists, with the verified scope, the shared-row strategy, the database-per-tenant
directory, the jobs/storage/cache adapters and bounded cross-tenant grants. The
published profile below names **shared row only**, because the two-database,
pool, migration and recovery rehearsals M2 and M5 owe have not been run against
real infrastructure. This revision supersedes the package/module shape in the
[2026-08-26 snapshot](2026-08-26-1558-multitenancy-roadmap.md).
That snapshot remains useful for its threat catalogue, topology failure cases
and operational exercises; where it proposes a bridge, topology package or
satellite-to-satellite contract, this document governs.

Two things this revision itself got wrong are corrected. It proposed a tenancy
*module*: in this repository a module boundary is a third-party dependency
boundary, not an optionality boundary, and `tenancy` adds no third-party package
to the graph. It is a package of the root module; a provider adapter becomes a
module when a concrete dependency is selected. And it read "one extension, one
public package" as *one Go package*, which would have made every seam one
decision — a deployment that wanted a tenant-partitioned cache compiling the
authorization subsystem to get it, because the row policy reaches `security` and
through it `auth` and `errs`. The extension is one; its core carries no seam and
each seam it adapts is a package beside it. [[D-116]] records both, and what this
revision actually fixed — one extension, one core, topologies that are not
modules, no combination packages — is unchanged.

This roadmap applies the
[optional extension architecture](2026-09-01-extension-architecture-roadmap.md)
to shared-row and database-per-tenant deployments. The security objective is
unchanged: a verified active tenant scope must select exactly one authorized
data-plane capability, and an absent, forged, stale or incompatible scope must
fail before tenant side effects.

## Current-tree baseline

The `tenancy` package now exists and is built entirely on the following
dependency-neutral seams, adding no third-party package to the root module's
graph:

- `crud.Middleware`, `crud.Chain`, source-bound executors and typed optional
  repository effects, including `ExistsUnscopedOf`, which is now exact-outer:
  a narrowing decorator answers the unscoped probe inside its own scope and an
  unknown wrapper fails closed ([[D-115]]);
- `storage.Store`, `storage.Namespace` and backend capabilities;
- cache namespaces and partition functions;
- jobs `TrustedContextProvider`, `TrustedIdentityRestorer`, durable partition,
  provenance and epoch values;
- ordinary `context.Context`, plus application-owned authentication and policy.

The jobs PostgreSQL code is present and the current scoped suite is green. The
same changing worktree exposed a durable-record round-trip regression and an
intermediate non-compiling fencing/repository shape during this audit, so it is
still `BUILDING`, not tenancy release evidence. No tenancy profile may cite
`jobspg` until one clean reviewable revision passes complete base-driver
conformance, live PostgreSQL and crash/recovery gates.

No tenancy evidence below cites `jobspg`: the durable adapter is proved against
the `jobs` contracts themselves, and the published profile names no PostgreSQL
job driver.

## Architectural decision

### One extension, one core, one package per seam

The delivered layout is:

```text
tenancy/                         PACKAGE github.com/frostgrove/vv/tenancy (root module, D-116)
  doc.go                         package tenancy
  reference.go                   the opaque tenant reference and the epoch
  scope.go                       verified scope, the binding that makes it unforgeable, the digest
  lifecycle.go                   lifecycle states, operation classes, admission whitelist
  authority.go                   injected trust contracts, minting and acceptance
  context.go                     explicit context propagation and the unit-of-work pin
  errors.go                      the refusal vocabulary, and what stops a resolver's text
  outcome.go                     the closed result vocabulary a signal may carry
  seal.go                        a scope becomes a record another process can check
  grant.go                       bounded cross-tenant grants
  tenancyrow/                    shared-row strategy: Ownership, Column, Through, Policy, Repository
  tenancydb/                     database-per-tenant strategy: Sources, Directory, Lease
  tenancyjobs/                   adapter to the root jobs context seams
  tenancystorage/                adapter to the root storage namespace seams
  tenancycache/                  adapter to the root cache partition seams
  */*_test.go                    conformance, per package
```

`rls.go` is not there: PostgreSQL RLS remains optional defence in depth and is
gated on its own role and pooled-session evidence, which has not been run.

There is exactly one tenancy extension, and none of these is a module or a
combination package. What the core imports is the standard library and `crud`,
whose error taxonomy decides what a refusal renders as; what an adapter imports
is the core and one seam. That is measured rather than declared —
`TestNoTenancyPackageCostsMoreThanTheSeamItNames` computes each package's
first-party graph — and it is the property "one public package" was reaching for:
a consumer compiles the seam it chose and nothing else. Shared-row and
database-per-tenant remain two strategies rather than two products, and they are
two packages for the same measured reason: only the first pays for `security`.

The initial production module imports only the standard library and approved
dependency-light packages from the root module. A direct pgx client, cloud
secret manager, control-plane SDK, migration product or router binding is a
separately selected backend/provider adapter and receives its own module only
when that concrete dependency is selected. `database/sql` code does not receive
a module merely for symmetry.

The dependency direction is:

```text
application composition root
       |
       +-- constructs base CRUD/storage/cache/jobs values
       +-- selects and orders tenancy with other extensions
       |
       v
tenancy extension ----imports----> dependency-light root seams
       |
       +--------------------------> standard library
       |
       +-------------X------------> vvotel, audit, event source, i18n,
                                    authjwt, storageminio, routers,
                                    brokers or concrete provider SDKs

root/base modules ----------------X------------> tenancy
extension A ----------------------X------------> tenancy
```

Provider adapters may depend on the tenancy contract and one genuine provider
ecosystem. They may not combine that provider with OTel, a router, JWT, audit or
another independently selected extension.

### Explicitly forbidden package cross-products

Do not create:

- a module for any of the topology or seam packages, until one of them selects a
  concrete third-party dependency;
- `tenancyjwt`, `tenancyhttp`, `tenancygrpc` or router-specific tenant modules;
- `tenancyotel`, `tenancyaudit`, `eventtenancy`, `storagetenancy`,
  `jobstenancy`, `cachetenancy` or `i18ntenancy` — a package whose identity is
  the intersection of tenancy with a second *extension*, or the mirror image of a
  seam adapter under the seam's own subsystem;
- topology × provider × subsystem combinations;
- a generic `extensions/` bundle, tenant registry, service locator or global
  current-tenant singleton.

The production package count grows with selected extensions and concrete
provider choices. The conformance matrix may necessarily exercise topology ×
subsystem combinations; that test cross-product must not become a production
package cross-product.

## Base seam ownership

Tenancy supplies factories and strategies. It does not take ownership of the
subsystems it constrains.

| Behaviour | Base owner | Tenancy's permitted role | Forbidden shortcut |
|---|---|---|---|
| Repository narrowing and writes | `crud` | Return typed `crud.Middleware` and source-selection factories | A new repository facade that hides `crud.Chain`, imports a driver or skips exact optional effects |
| Transaction/source identity | `crud` or application UoW | Select/bind one verified source before work starts | A tenancy-owned cross-subsystem UoW or late datasource switch |
| Service wrapping | `port` after its typed chain is accepted | Return service middleware | A tenancy service registry containing application repositories |
| Object namespace | `storage` | Map verified scope to a bounded `storage.Namespace` or Store middleware | `storageminio` import, raw prefix concatenation or provider credentials in scope |
| Cache partition | `cache` | Map verified scope to the cache's typed partition input | Cache inferring tenant/principal from arbitrary context values |
| Durable job identity | `jobs` | Implement its capture/restore contracts and validate lifecycle at execution | `jobstenancy`, raw payload routing or jobs importing tenancy |
| Audit/event/i18n | Their own contracts or application wiring | Compose independently at a shared base seam | Either extension importing the other |
| Telemetry | Root typed seam or application wiring | Expose only a bounded tenancy-local result vocabulary | `tenancyotel` or `vvotel` importing tenancy |

`tenants.UnitOfWork(...).Orders()` and `tenants.For(scope).Orders()` from the old
snapshot are not framework package APIs. If an application chooses that DX, the
value is an application-owned typed composition object with explicitly injected
repositories. The tenancy extension does not discover application services or
hold a process-global repository registry.

### Linear composition, as delivered

Everything named `tenancy.*` and `crud.*` below is implemented. `audit.*` and
`vvotel.Service` remain illustrative — audit has no package, and the OTel service
decorator is that revision's to accept:

```go
authority := tenancy.Must(tenancy.Spec{Resolver: controlPlane})

core := crud.Chain(
    baseCore,
    tenancyrow.Repository[Order, OrderID](authority, tenancyrow.Column[Order]("TenantID", tenancyrow.Derive, nil)),
    audit.Repository[Order, OrderID](auditor, auditPolicy),          // illustrative
)

queue, err := jobs.NewQueue(jobs.QueueSpec{
    Namespace: jobsNamespace,
    Catalog:   jobCatalog,
    Sender:    jobsBackend,
    Context:   tenancyJobContext,   // from tenancyjobs.ContextProvider(authority, provenance, epoch)
})
```

An application that already gates the same repository combines rather than
stacks, because a scoped upsert is an exact capability of the core directly below
and `security.gate` does not forward it:

```go
gate := security.Gate(security.Combine(
    tenancyrow.Policy[Order, OrderID](authority, ownership),
    security.RequirePermission[Order, OrderID]("orders:write"),
))
```

The application imports and orders the selected extensions. Tenancy factories
return ordinary base middleware/providers; they do not construct audit,
`vvotel`, `jobspg` or an application service. Removing tenancy leaves the base
CRUD/service/jobs APIs and their module graphs intact (subject to a job
definition's explicit tenant-partition requirement).

### Jobs seam already available

Jobs already owns the neutral durable-context boundary:

```text
producer: jobs.TrustedContextProvider
worker:   jobs.TrustedIdentityRestorer
values:   partition + protected token + provenance + epoch
```

The tenancy extension may return implementations of those interfaces. Jobs
continues to compile and work without tenancy, and tenancy does not import a
jobs backend. A combined consumer fixture imports both and proves that a durable
reference is re-resolved against current lifecycle/epoch before the handler is
given a tenant-bound context.

### Cross-extension scenarios are tests, not imports

Audit, event source, storage, i18n and OTel integrations remain important
behavioural profiles. They are wired in an unpublished consumer/integration
module that imports the independently selected extensions. Their production
modules do not receive tenancy-owned types merely to earn a compatibility
label.

In particular:

- replace `eventpg.ForTenant(scope, ...)` with application mapping to an
  event-owned partition/source capability;
- make scoped storage an application composition of a tenancy mapper and the
  base `storage.Store` seam, never a provider × tenancy bridge;
- make audit and tenancy independent middleware over a base transaction/service
  seam; neither constructs the other;
- pass a resolved locale/configuration to i18n through its own input rather than
  teaching i18n to resolve `tenancy.Scope`;
- defer tenant-resolver-specific OTel signals until a dependency-neutral base
  hook exists or wire them explicitly in the application. A generic telemetry
  facade is not introduced to evade the no-extension-edge rule.

## Security and topology contract

### Verified scope

A scope is opaque, immutable and constructible in production only by an injected
trusted resolver. It contains stable tenant reference, lifecycle/binding epoch
and the minimum capability needed by a topology strategy. It contains no DSN,
database name, bucket, raw JWT claim, HTTP carrier or telemetry labels.

Required behaviour:

- no package-level current tenant and no default tenant;
- missing, malformed, inactive, stale or untrusted scope produces no tenant SQL,
  object, event, audit or job effect;
- authentication verifies identity/membership before the tenancy resolver is
  called; the tenancy module imports no JWT or router implementation;
- background work restores authority deliberately and rechecks current
  lifecycle/epoch rather than trusting request context or payload text;
- cross-tenant work uses a separate bounded purpose/grant/cohort capability,
  never `nil` scope or an arbitrary slice of tenant IDs.

### Shared-row topology

The first release targets one tenant-owned resource on shared PostgreSQL. A
typed CRUD middleware must cover the complete supported matrix: get, list,
count, exists, aggregate, relation/preload, create, save, update, upsert,
delete, restore and admitted bulk effects. Ownership is derived or validated on
create and immutable for ordinary mutations.

PostgreSQL RLS is optional defence in depth. If accepted, its
transaction-local setting, application role, owner/BYPASSRLS assumptions and
pool reset behaviour are part of the profile. It never replaces application
narrowing or turns an unscoped raw query into a supported path.

### Database-per-tenant topology

The second release resolves a verified scope to one caller-owned root datasource
capability. The generic tenancy module owns resolver/cache/pool contracts and
bounds, not a driver or secret SDK. Selection happens before a transaction or
repository is exposed, nested operations remain on the selected source, and a
missing/inactive/stale/incompatible mapping has no default or last-used fallback.

Pool/cache limits, borrower lifetime, rotation, eviction, schema capability and
wrong-database fencing are mandatory evidence. Two real databases containing
equal resource IDs are the minimum integration fixture.

### Lifecycle and operations

Provisioning, active, suspended, migrating, deleting/deleted and restored
generations are control-plane states. Data-plane resolution admits only an
explicit compatible state. Migration, backup, restore, deletion, legal hold and
tenant move are topology-specific procedures with one active generation and a
fenced cutover; they are not hidden inside first-request resolution.

## Privacy contract

Generic signals may contain only closed bounded values such as topology mode,
operation, component, lifecycle class and safe outcome. They must not contain
tenant reference, stable tenant hash, database/schema/DSN/user, pool key,
bucket/prefix/object reference, raw host/header/claim or wrapped error text.

Protected audit/control-plane workflows own identifiable diagnosis. Every
telemetry profile runs a sentinel scan and a cardinality test with thousands of
distinct tenant inputs. OTel outage/no-op behaviour must not affect resolution,
policy, routing or lifecycle results.

## Delivery plan

### M0 — freeze extension and base boundaries — **delivered**

1. **Done** — [[D-116]] is the tenancy boundary ADR and names the path and
   package.
2. **Done, amended** — exactly one tenancy extension in the root module: a core
   that imports no seam, one package per topology and per seam, and the rule for
   genuine provider adapter modules ([[D-116]]). The `go.mod` half of the
   original wording, and its reading of "one public package" as one Go package,
   are what that decision corrects.
3. **Done** — the inventory is in `.agents/artifacts/usecases/TENANCY_RECONCILE.md`;
   the only base seam added is the exact-outer rule in item 4, and it had a
   concrete conformance obligation rather than a second consumer.
4. **Done** — `crud.ExistsUnscopedOf` is exact-outer, `security.gate` answers it
   inside its own scope and an opaque wrapper exposes no inner unscoped effect
   ([[D-115]]).
5. **Done** — [[D-117]] freezes scope construction, lifecycle/epoch, error
   identity, context propagation and redaction.
6. **Done** — the allow-list is *the standard library and first-party root
   packages*, and it is checked: `go list -deps ./tenancy/...` — the whole
   extension, not the core alone — names no external package, and
   `make check-deps`, `check-tiers` and `check-utils` pass with
   `tenancy` added to `SUBSYSTEMS`. `TestNoTenancyPackageCostsMoreThanTheSeamItNames`
   narrows it further, per package.
7. **Partial** — MISSING: a generated method inventory for the base wrappers the
   extension returns. The obligation is currently discharged by reuse:
   `tenancyrow.Repository` *is* `security.Gate`, which [[D-030]]'s
   `TestEveryVerbOnTheSeamIsGatedOrHasAWrittenReason` already holds against
   `crud.Core` by reflection, so a verb added to the seam fails that test rather
   than shipping unprotected. A tenancy-owned inventory is owed for the storage,
   cache and jobs adapters.

Exit evidence:

- no public API is accepted by illustrative prose alone — every name in this
  document that is not marked illustrative compiles;
- row and database topologies are two packages beside the core, each naming the
  topology a deployment chose, with no mode-dependent global and no state shared
  between them;
- **owed** — two fake independent extensions composing with tenancy in both
  orders. What exists is the composition with `faults.enricher`, which forwards
  every optional effect, the `security.Combine` path, and
  `_examples/tenancy-sharedrow`, which wires the whole extension from outside the
  module under `GOWORK=off`;
- no bridge, topology submodule or extension-to-extension import was required.

### M1 — verified scope and one shared-row slice — **delivered**

1. **Done** — `tenancy.Scope` is opaque, immutable and minted only by an
   `Authority`; there is **no** test-only constructor, because a test wires a
   `tenancy.Fixed` resolver and gets a scope by the path production uses. That is
   stricter than the original wording asked for ([[D-117]]).
2. **Done** — `tenancyrow.Repository[M, ID]` returns `crud.Middleware[M, ID]`, and
   `tenancyrow.Policy` returns a `security.Policy` for composition. Ownership is a
   strategy: `Column` for a row that carries its owner, `Through` for one owned
   across a relation, and the `Ownership` interface for anything else.
3. **Done** — `TestNoVerbRunsWithoutAVerifiedScope` covers fifteen verbs with
   zero statements executed; `TestEveryVerbNarrowsToTheScopesOwnTenantAndNoOther` and
   the write tests cover the matrix; `TestATenantOwnedRepositoryIsolatesTwoTenantsOnOneTable`
   proves foreign-id, count, existence, update and delete isolation against live
   PostgreSQL, with `TestATenantSeesEveryRowItOwns` as the control that the
   narrowing narrows to the right rows rather than to none.
4. **Partial** — MISSING: a second, unrelated fake extension. Fail-closed
   behaviour with an opaque neighbour is proved for the unscoped probe
   (`TestAnAssignedKeySaveRefusesWhenTheCoreBelowCannotAnswerTheProbe`), and
   `faults.enricher` composes in both orders, but a purpose-built pair of fake
   extensions is owed.
5. **Deferred** — composite constraints are the application's schema; RLS stays
   out until its separate role and pooled-session tests pass.

### M2 — database-per-tenant strategy — **strategy delivered, profile not advertised**

1. **Done** — `tenancydb.Sources`, `tenancydb.DirectorySpec` and `tenancydb.Directory`
   live in `database.go`. `MaxCached` and `TTL` are required rather than
   defaulted: an unbounded per-tenant pool is how one tenant takes a deployment
   down.
2. **Partial** — MISSING: two *real* databases, and outage behaviour. Wrong
   mapping, absent mapping, capacity, rotation to a new generation, eviction with
   a live borrower, borrower lifetime and the application-supplied identity fence
   are covered by `tenancy/tenancydb/database_test.go` against recording sources, along with
   the bound that matters — the number of sources open *at once*, which a burst of
   borrowers broke until the slot was reserved before the open.
3. **Done** — no provider SDK is imported; the driver and the secret store are
   the application's `Sources` implementation.
4. **Not started** — the suspend/migration/restore rehearsal. This is why the
   published profile below names shared row only.

### M3 — root-seam adapters — **delivered**

1. **Done** — `tenancyjobs.ContextProvider` and `tenancyjobs.IdentityRestorer` implement the
   existing `jobs` interfaces. `TestWorkEnqueuedBeforeASuspensionDoesNotRunAfterIt`
   drives a real durable round trip and proves that a tenant suspended, deleted
   or moved to a new generation after enqueue does not reach its handler, and
   `TestAForgedDurableRecordEntersNoHandler` proves a record whose
   token and partition disagree restores nothing.
2. **Done** — `tenancystorage.Namespace`/`Store` and `tenancycache.Partition`/`Keyed`
   /`CacheScope` map a verified scope onto inputs `storage` and `cache` already
   take. Injectivity and non-containment are proved over 500 tenants; the
   generation is inside both digests, so a restored tenant reads neither the
   previous generation's objects nor its cached values.
3. **Done** — `jobs.go`, `storage.go`, `cache.go`; no package per seam.
4. **Partial** — MISSING: the explicit-order matrix for the storage and cache
   decorators. Exact capability preservation for the repository seam is covered
   by [[D-115]]'s tests.

### M4 — multi-extension consumer evidence — **open**

Two of the extensions it names do not exist yet, so the fixture cannot be written
in full. What it needs, unchanged:

1. In an unpublished consumer module, compose tenancy independently with audit,
   event source, storage, i18n, jobs and the one `vvotel` module as applicable.
2. Verify no participating production module imports another extension.
3. Run same-ID, invalid-scope, stale-epoch, rollback, retry and privacy scenarios
   across each advertised composition.
4. Keep unsupported interactions fenced/deferred rather than publishing a bridge.

### M5 — lifecycle and release profile — **open**

Item 1 is discharged by the profile below; the rehearsals are not.

1. **Done** — see *Published profile* below.
2. Rehearse migration cohort failure, credential rotation, restore/move fencing,
   legal hold/deletion and partial cross-tenant work.
3. Publish bounded dashboards and protected diagnostic runbooks.
4. Expand to another resource/provider only after its query matrix and dependency
   decision pass the same gates.

## Dependency and composition gates

These gates are release requirements, not review suggestions.

| Area | Required proof |
|---|---|
| Single extension | Exactly one tenancy extension, no tenancy `go.mod`; a core that imports no seam, and row/database as two topology packages beside it rather than two products |
| Root optionality | With `GOWORK=off`, the root and every pre-existing module have no tenancy module or tenancy-owned type in their graph |
| Source imports | Tenancy production imports match the M0 root-seam allow-list; no optional extension, provider SDK, router or backend satellite import |
| Direct requirements | Initial tenancy `go.mod` requires only the reviewed first-party root version; every later provider module states its one ecosystem decision |
| Transitive graph | `go list -m all`, `go mod why -m` and a checked dependency diff explain every module edge |
| No extension edge | Source/module checks reject tenancy ↔ vvotel/audit/event/i18n/authjwt/storageminio/broker imports |
| Linear growth | Base-only, tenancy-only and representative multi-extension fixtures add no pairwise/nested combination package |
| Capability safety | Method inventory and opaque-wrapper matrix prove exact outer effects and honest method-set discovery |
| Activation | Import and factory construction register no globals, discover no app service and own no unrequested lifecycle |
| Workspace/release | Discovered intended modules and `go.work` members are set-equal; published modules have no `replace`; tags are coherent |

The graph checks run outside `go.work`; workspace builds alone are insufficient
because workspace members can hide a missing or accidental module requirement.
The unpublished combination fixtures may import several extensions. Production
modules may not.

## Conformance profiles

Each advertised profile states its topology, source/driver, supported operation
matrix, lifecycle states, known exclusions and evidence version. Minimum proofs:

| Profile | Required negative evidence |
|---|---|
| Shared-row CRUD | Equal IDs in A/B; missing/forged/stale scope; all supported reads/writes/relations; zero foreign count/existence leak |
| RLS hardening | Pooled A then B; ordinary app role; owner/BYPASSRLS check; transaction-local reset; app predicate still active |
| Database-per-tenant | Two real DBs; wrong/stale mapping; no fallback; pool bound measured as concurrently open sources rather than cached entries; eviction with a live borrower; rotation; an identity fence the application supplies, since the directory cannot know which database is whose |
| Jobs | Durable reference is not authority; current scope restored; deleted/suspended/stale work has zero side effect |
| Storage/cache | Base namespace/partition mapping cannot escape or infer identity; equal logical keys remain isolated |
| Cross-extension | Application fixture imports both; production graph has no edge; order/rollback/retry/privacy claims are exact |
| Operations | Partial migration, restore/move, suspend race, hold/delete and cross-tenant grant expiry remain explicit |

## Published profile

This is what the extension is advertised as supporting today. Anything not on it
is fenced or deferred, not implied.

| | |
|---|---|
| **Topology** | Shared row only. The database-per-tenant strategy is implemented and unit-proved but **not advertised**: M2's two real databases, outage behaviour and M5's suspend/migration/restore rehearsal have not been run |
| **Resources** | Any model whose ownership is one column (`tenancyrow.Column`) or held across a declared relation (`tenancyrow.Through`); anything else through the `Ownership` interface |
| **Operations** | The whole `crud.Core` matrix plus `InsertBatch` and `Restore`, via `security.Gate` |
| **Lifecycle states** | `Active` admits every class by default; admission is a whitelist per operation class, and an unknown state refuses |
| **Epoch** | Checked at each explicit boundary — request bind, durable execution, cohort member — and pinned in between. `Spec.Revalidate` moves the window to zero, re-asking the resolver on every verb. The framework caches no resolution either way; a deployment that wants a window between the two puts it in its own resolver and states the bound |
| **Suspension mid-unit-of-work** | Complete-then-refuse. A unit of work already bound finishes; the next boundary refuses |
| **Pre-tenancy rows** | Refused at runtime. Backfilling a null or default owner is an operator procedure |
| **Deletion footprint** | The extension *verifies* a fence — a new generation reads neither the previous generation's rows, objects nor cached values — it does not orchestrate cleanup |
| **Durable drivers** | The `jobs` contracts. No PostgreSQL or Redis job driver is cited; both remain `BUILDING` |
| **Durable identity** | Requires `Spec.DurableKey`, shared by producer and worker. The seam refuses to be constructed without it, because a reference in a queue row that nothing authenticates is tenant impersonation |
| **Relations** | Narrowed where the resource declares them (`tenancyrow.Relation` in the `Column` call). An undeclared relation is read whole — deliberately, and it is why the declaration is part of construction rather than a default |
| **Grants** | Bounded by cohort, deadline **and** the classes of work named at acceptance. A purpose is a label, not a permission |
| **Known exclusions** | PostgreSQL RLS; operator procedures (suspend, migrate, restore, delete, legal hold); a multi-extension consumer fixture; a pagination cursor minted under another tenant's scope, which is the same class of oracle as a unique-constraint create and is documented rather than closed; gate-over-gate assigned-key `Save`, for which `security.Combine` is the supported composition; `Tx`, which the gate inherits by [[D-030]]'s written reason, so an unscoped transaction opens and the first verb inside it refuses; an assigned-key `Save`, which carries the whole row and so must carry the owner |

## Definition of done for the first tenancy release

| # | Requirement | State |
|---|---|---|
| 1 | the common architecture ADR and tenancy boundary are accepted | **met** — [[D-116]], [[D-117]] |
| 2 | one tenancy extension carries shared-row and database strategies, and no combination package exists | **met** — and narrowed: the core imports no seam, and each topology or seam package reaches the core plus one seam, measured by `TestNoTenancyPackageCostsMoreThanTheSeamItNames` |
| 3 | verified scope cannot be manufactured from raw production input or defaulted | **met** — [[D-117]], and there is no test-only constructor to leak |
| 4 | one shared PostgreSQL resource passes its complete advertised query/write matrix, equal-ID and invalid-scope tests | **met** — `test/integration/tenancy_test.go` against live PostgreSQL, plus the verb matrix in `tenancy/tenancyrow/row_test.go` |
| 5 | every tenancy wrapper passes method/capability obligations with unrelated wrappers in both relevant orders | **partial** — the repository wrapper inherits [[D-030]]'s obligation test and [[D-115]]'s exact-outer tests; a purpose-built pair of fake extensions and an inventory for the storage/cache/jobs adapters are owed |
| 6 | base-only, tenancy-only and multi-extension `GOWORK=off` fixtures prove graph optionality and linear package growth | **partial** — optionality is proved by the import graph and `make check-deps`/`check-tiers`/`check-utils`; the isolated fixtures are owed with M4 |
| 7 | no root/base reverse edge, extension-to-extension edge, third-party tenancy requirement or hidden global activation exists | **met** — `go list -deps ./tenancy/...` names no external package, no base package imports `tenancy` or anything under it, no adapter imports a sibling, and construction registers nothing |
| 8 | privacy canaries and high-cardinality tests pass without tenant/database identity in generic signals | **met** — `TestAResolverFailureNeverTravelsBackAsText`, `TestNothingCarryingATenantRendersIt`, `TestTheOutcomeVocabularyIsClosed`, and injectivity over 500 tenants |
| 9 | the published profile names shared-row only unless the full two-database, pool, epoch, migration and recovery evidence has also passed | **met** — it does |
| 10 | every package and live-driver test used as evidence is green; `jobspg` is not cited | **met** — the `tenancy` and `crud` unit suites and the tenancy live-PostgreSQL test are green across two consecutive integration runs; no evidence cites `jobspg`. The suite as a whole ends `FAIL` on two `vvdb` pool-configuration tests that fail identically with this change set removed, and they are not evidence for anything here |
| 11 | module discovery/workspace/release/no-`replace` gates and `git diff --check` pass | **met for the gates this change can affect** — `check-workspace`, `check-replaces`, `check-todo` pass. `check-tidy` fails on four pre-existing modules (`app/http/appfiber`, `auth/access/accessjwt`, `.../revokeredis`, `.../revokeredisfx`) that this change does not touch |
| 12 | documentation distinguishes implemented APIs from illustrative factory names and lists every unsupported integration as fenced or deferred | **met** — see the composition section above and the profile |

The developer-facing result stays small: the host verifies a scope, the
application composition root applies a tenancy factory to a base seam, and
business code receives an already bound capability. The package graph stays
equally small: adding another extension adds one decision, not every
intersection with tenancy.
