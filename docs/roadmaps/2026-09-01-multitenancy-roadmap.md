# Multitenancy roadmap — linear optional extension — 2026-09-01

**Status:** current proposal, not an activated delivery commitment. No tenancy
package or module exists. This revision supersedes the package/module shape in
the [2026-08-26 snapshot](2026-08-26-1558-multitenancy-roadmap.md).
That snapshot remains useful for its threat catalogue, topology failure cases
and operational exercises; where it proposes a bridge, topology package or
satellite-to-satellite contract, this document governs.

This roadmap applies the
[optional extension architecture](2026-09-01-extension-architecture-roadmap.md)
to shared-row and database-per-tenant deployments. The security objective is
unchanged: a verified active tenant scope must select exactly one authorized
data-plane capability, and an absent, forged, stale or incompatible scope must
fail before tenant side effects.

## Current-tree baseline

As of this revision, no published tenancy module or package exists. The useful
dependency-neutral seams already in the root module are:

- `crud.Middleware`, `crud.Chain`, source-bound executors and typed optional
  repository effects; the current executable `ExistsUnscopedOf` `Next` walk is
  a blocker that must become exact-outer/explicitly forwarded or fail closed;
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

The first tenancy change is therefore an extension boundary and one thin
shared-row slice, not a claim that the existing framework is already
multi-tenant.

## Architectural decision

### One extension, one public package

The working layout is:

```text
tenancy/                         MODULE github.com/frostgrove/vv/tenancy
  go.mod
  doc.go                         package tenancy
  scope.go                       verified scope, lifecycle and epoch
  context.go                     explicit context propagation
  resolver.go                    injected trust/control-plane contracts
  row.go                         shared-row strategy and CRUD factories
  database.go                    database-per-tenant strategy and source factories
  rls.go                         database/sql PostgreSQL hardening, if accepted
  jobs.go                        adapters to root jobs context seams
  storage.go                     adapters to root storage seams, if justified
  cache.go                       adapters to root cache partition seams, if justified
  conformance_test.go
  internal/                      non-public implementation support
```

The path and filenames are working names until M0 accepts the architecture ADR.
The invariant itself is fixed: there is exactly one tenancy extension module
and one public package. Shared-row and database-per-tenant are strategies/factories
inside that package, not `tenancyrow` and `tenancydatabase` modules or public
subpackages. A file is not a dependency boundary in Go, and a topology is not a
separate consumer dependency decision.

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

- `tenancyrow` or `tenancydatabase` modules;
- `tenancyjwt`, `tenancyhttp`, `tenancygrpc` or router-specific tenant modules;
- `tenancyotel`, `tenancyaudit`, `eventtenancy`, `storagetenancy`,
  `jobstenancy`, `cachetenancy` or `i18ntenancy`;
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

### Illustrative linear composition

The following shows the intended application boundary. `crud.Chain` and the
jobs constructors are current; the tenancy/audit factories and
`port.ChainService` are illustrative until their milestones are accepted:

```go
core := crud.Chain(
    baseCore,
    tenancy.Repository[Order, OrderID](tenantPolicy),
    audit.Repository[Order, OrderID](auditor, auditPolicy),
)

service := port.ChainService(
    port.NewService(crud.Wrap[Order, OrderID, OrderUpdate](core)),
    tenancy.Service[Order, OrderID, OrderUpdate](tenantPolicy),
    audit.Service[Order, OrderID, OrderUpdate](auditor),
    vvotel.Service[Order, OrderID, OrderUpdate](telemetry),
)

queue, err := jobs.NewQueue(jobs.QueueSpec{
    Namespace: jobsNamespace,
    Catalog:   jobCatalog,
    Sender:    jobsBackend,
    Context:   tenancy.JobContext(tenantResolver),
})
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

### M0 — freeze extension and base boundaries

1. Accept the common extension ADR and link the final tenancy path/package.
2. Record exactly one tenancy `go.mod`, one public package and topology files,
   plus the rule for genuine provider adapter modules.
3. Inventory root CRUD/source/transaction, storage, cache, jobs and service
   seams; add no new seam without two independent consumers or one concrete
   conformance obligation.
4. Resolve `crud.ExistsUnscopedOf` executable discovery before accepting a
   tenancy CRUD decorator; an opaque wrapper must not expose an inner unscoped
   effect.
5. Freeze scope construction, lifecycle/epoch, error identity, context and
   redaction contracts.
6. Record source-import, direct-require and transitive dependency allow-lists.
7. Publish method inventories and exact optional-capability obligations for
   every base wrapper the extension will return.

Exit evidence:

- no public API is accepted by illustrative prose alone;
- row and database topologies fit one package without mode-dependent globals;
- two fake independent extensions compose with tenancy through the same base
  chain in both relevant orders;
- no bridge, topology submodule or extension-to-extension import is required.

### M1 — verified scope and one shared-row slice

1. Implement opaque verified scope, lifecycle/epoch and test-only construction.
2. Return one typed CRUD middleware for a scalar tenant-owned resource.
3. Prove equal-ID, absent/forged scope and complete supported query/write matrix.
4. Prove decorator capability preservation/fail-closed behaviour with opaque
   neighbours and both middleware orders.
5. Add composite database constraints; add RLS only after its separate role and
   pooled-session tests pass.

### M2 — database-per-tenant strategy

1. Add injected binding resolver, source factory and bounded pool/cache policy in
   the same public package.
2. Test two databases, wrong mapping, stale epoch, rotation, eviction, outage,
   transaction pinning and schema capability fences.
3. Keep provider SDKs out of the module; add a provider adapter only for a real
   selected consumer and review it as one dependency decision.
4. Rehearse suspend, migration and restore before advertising the profile.

### M3 — root-seam adapters

1. Add jobs capture/restore factories over the existing jobs interfaces and
   prove producer/worker lifecycle revalidation.
2. Add storage/cache factories only where their root-owned inputs are sufficient
   and a real consumer exists.
3. Keep each adapter in the same tenancy package, organized by file; do not add a
   package per seam.
4. Test explicit application order and exact capability preservation.

### M4 — multi-extension consumer evidence

1. In an unpublished consumer module, compose tenancy independently with audit,
   event source, storage, i18n, jobs and the one `vvotel` module as applicable.
2. Verify no participating production module imports another extension.
3. Run same-ID, invalid-scope, stale-epoch, rollback, retry and privacy scenarios
   across each advertised composition.
4. Keep unsupported interactions fenced/deferred rather than publishing a bridge.

### M5 — lifecycle and release profile

1. Publish exact supported topology, resources, operations and exclusions.
2. Rehearse migration cohort failure, credential rotation, restore/move fencing,
   legal hold/deletion and partial cross-tenant work.
3. Publish bounded dashboards and protected diagnostic runbooks.
4. Expand to another resource/provider only after its query matrix and dependency
   decision pass the same gates.

## Dependency and composition gates

These gates are release requirements, not review suggestions.

| Area | Required proof |
|---|---|
| Single extension | Exactly one published tenancy `go.mod` and one public package; row/database are files/strategies |
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
| Database-per-tenant | Two real DBs; wrong/stale mapping; no fallback; pool bound/eviction/rotation; schema fence |
| Jobs | Durable reference is not authority; current scope restored; deleted/suspended/stale work has zero side effect |
| Storage/cache | Base namespace/partition mapping cannot escape or infer identity; equal logical keys remain isolated |
| Cross-extension | Application fixture imports both; production graph has no edge; order/rollback/retry/privacy claims are exact |
| Operations | Partial migration, restore/move, suspend race, hold/delete and cross-tenant grant expiry remain explicit |

## Definition of done for the first tenancy release

The first release is complete only when:

1. the common architecture ADR and tenancy boundary are accepted;
2. one tenancy module/public package contains shared-row and database strategy
   files and no combination package exists;
3. verified scope cannot be manufactured from raw production input or defaulted;
4. one shared PostgreSQL resource passes its complete advertised query/write
   matrix, equal-ID and invalid-scope tests;
5. every tenancy wrapper passes method/capability obligations with unrelated
   wrappers in both relevant orders;
6. base-only, tenancy-only and multi-extension `GOWORK=off` fixtures prove graph
   optionality and linear package growth;
7. no root/base reverse edge, extension-to-extension edge, third-party tenancy
   requirement or hidden global activation exists;
8. privacy canaries and high-cardinality tests pass without tenant/database
   identity in generic signals;
9. the published profile names shared-row only unless the full two-database,
   pool, epoch, migration and recovery evidence has also passed;
10. every package and live-driver test used as evidence is green; the observed
    `jobspg` durable round-trip regression stays pinned and `jobspg` is not cited
    until base conformance plus live PostgreSQL/crash evidence pass;
11. module discovery/workspace/release/no-`replace` gates and `git diff --check`
    pass;
12. documentation distinguishes implemented APIs from illustrative factory names
    and lists every unsupported integration as fenced or deferred.

The developer-facing result stays small: the host verifies a scope, the
application composition root applies a tenancy factory to a base seam, and
business code receives an already bound capability. The package graph stays
equally small: adding another extension adds one decision, not every
intersection with tenancy.
