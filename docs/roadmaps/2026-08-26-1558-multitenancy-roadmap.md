# Multitenancy roadmap — one database and database per tenant — 2026-08-26 15:58 +05

This roadmap designs multitenancy as a first-class security, routing and
operational model for vv applications. It covers two initial topology families:

1. **one shared database** with per-tenant row isolation; and
2. **database per tenant** with explicit tenant-to-datasource resolution.

It deliberately does not pretend both topologies are an interchangeable config
flag. They have different isolation blast radius, migration, connection-pool,
backup/restore, encryption, cost, observability and operational runbook
semantics. vv should offer one safe application shape with topology-specific
adapters and proofs, not a magic `TenantID` filter that claims security.

## Architectural decision

Tenancy is a satellite/extension around existing policy and repository seams,
not a root global. The root already has policy/context mechanisms; tenant
resolution, identity provider, datasource pool, migration runner and database
driver are consumer choices. A possible split is:

| Module | Owns | Must not own |
|---|---|---|
| root vv policy/repository | existing context/policy SQL narrowing seams | tenant resolver/database pool |
| `vv/tenancy` | tenant scope/context/topology contract | JWT/HTTP/database driver |
| `vv/tenancyrow` | one-db row scope helpers/RLS integration rules | per-tenant datasource pool |
| `vv/tenancydatabase` | tenant -> datasource resolution/lifecycle | raw identity provider/router |
| migration satellite | topology-aware schema rollout | hidden tenant discovery |
| audit/OTel bridges | durable/security correlation and bounded mode evidence | tenant identity data exhaust |

[[D-048]] and [[D-051]] apply: a module cannot quietly choose both OTel and a
cloud secret manager, or a web framework and pgx, just to make tenancy feel
automatic. The host owns authentication and trusted tenant claim extraction;
the tenancy satellite receives a verified tenant principal/scope, not a raw
header string it is expected to authenticate.

## Reference model

Hibernate documentation identifies the same strategic choices: separate
database, separate schema, or discriminator/tenant ID. This roadmap explicitly
supports the first and third form, and defers schema-per-tenant until it has its
own migration/connection/DDL safety analysis. [Hibernate multitenancy overview](https://docs.hibernate.org/orm/6.2/introduction/html_single/)

## Product intent

The common path establishes tenant scope at a trusted boundary, passes it by
ordinary `context.Context`, and composes it into every query/write/transaction
or datasource selection before domain code can see a repository:

```go
ctx = tenancy.WithScope(ctx, verifiedScope)
repo := tenancy.DecorateRepo(baseRepo, resolver)

// Same service code; scope controls row predicate or selected datasource.
order, err := repo.GetByID(ctx, id)
```

The public API is illustrative. Required properties:

- no tenant ID from arbitrary query/body/header reaches data selection directly;
- every read, count, exists, aggregate, preload, nested relation and mass write
  has the same tenant security story;
- creates set/validate ownership; updates cannot move records across tenant
  without a named, separately authorized migration action;
- one-db database-side protection (e.g. PostgreSQL RLS) is optional defence in
  depth, not a replacement for tested application policy unless ADR says so;
- db-per-tenant resolver selects a caller-owned datasource using stable trusted
  mapping and bounded pool/cache lifecycle;
- missing/unknown/deactivated tenant fails closed before any unscoped query;
- audit gets durable tenant/actor detail under authorization; OTel gets only
  bounded topology/result, never tenant/database/schema identity;
- migration/backup/incident procedures are topology-specific and explicit.

## Vocabulary

| Term | Meaning |
|---|---|
| tenant | authorized logical isolation subject, not a display name/header value |
| tenant scope | immutable verified context value used by policy/routing |
| shared DB | one database with row-level discriminator/isolation policy |
| db per tenant | one physical/logical database selected per tenant scope |
| control plane | trusted mapping/lifecycle/configuration of tenants/databases |
| data plane | application request/work accessing one tenant's data |
| cross-tenant action | explicitly named privileged workflow, never ordinary scope bypass |
| tenant resolver | maps verified scope to row policy or datasource handle |
| tenant lifecycle | provision/active/suspended/migrating/deleted transitions |
| migration cohort | bounded set of tenants at a schema/app rollout state |

## Non-negotiable invariants

1. **Fail closed.** Absent, malformed, unknown, inactive or untrusted tenant
   scope produces zero SQL/side effects for tenant-scoped operation.
2. **Scope before repository.** No service selects a tenant after it has loaded
   a row or opened a transaction; routing/narrowing begins at entry seam.
3. **No caller-selected datasource.** Database name/URL/pool key never comes
   from HTTP path, query, body, user claims raw text or event payload unchecked.
4. **Complete query matrix.** Tenant narrowing covers `Get`, `GetAll`, count,
   exists, aggregate, nested relation/preload, sort/filter and every write/bulk
   victim selection, not only ordinary list calls.
5. **Ownership immutable by default.** Create sets/validates tenant key; normal
   update/upsert cannot change it or rehome child relationships across scope.
6. **Control plane is separate.** Tenant registry/database credentials/migration
   status are not stored or mutated via ordinary tenant-scoped repositories.
7. **No global current tenant.** Scope travels in caller context; background
   work must deliberately establish/recover authorized scope.
8. **Topology is observable but anonymous.** `row`/`database` can be bounded
   metrics/trace modes; tenant ID, DB/schema/connection name cannot.
9. **Admin crosses explicitly.** A cross-tenant report/migration uses named
   capability, audit, batching and authorization, not `nil` tenant scope.
10. **Migration is a safety feature.** App/database schema version mismatch
    fails visibly; db-per-tenant rollout cannot silently run new query on old DB.
11. **Events/storage follow scope contracts.** Event streams/object keys may be
    routed/scoped by authorized tenant but never use raw tenant identity as
    dynamic telemetry label or authorization shortcut.
12. **Removal/reassignment is lifecycle.** A deleted tenant is not automatically
    safe to reuse; retention, domain IDs, keys, backups and audit are governed.

## Topology decision matrix

| Concern | Shared DB / row tenant | Database per tenant |
|---|---|---|
| isolation unit | row/policy/RLS predicate | datasource/database boundary |
| cross-tenant query risk | missing/incorrect predicate | resolver/pool misrouting |
| schema migration | one coordinated DB migration | fleet/cohort lifecycle |
| connection overhead | shared bounded pool | potentially many pools/clients |
| noisy-neighbor isolation | limited/shared resources | stronger but operationally costly |
| backup/restore | selective tenant recovery difficult | tenant-level recovery easier but mapping critical |
| RLS | useful defence-in-depth PostgreSQL option | less central but DB credential policy matters |
| reporting | SQL needs explicit scope/admin policy | federated/ETL/admin workflow |
| tenant deletion | row/retention/partition process | DB teardown/backup/legal-hold process |
| observability | no tenant labels | no database labels |

The matrix informs product/business choice. Neither topology is universally
“more secure” without the surrounding control plane, credentials, policy,
migration and operator processes being correct.

## Scope acquisition and trust boundary

Tenant scope can originate from a verified identity claim, mutually authenticated
service identity, trusted domain routing record or internal scheduled work. It
may be influenced by hostname/path/header only after authentication/authorization
maps raw carrier to an allowed tenant. The tenancy package never parses JWTs or
declares a header secure by name.

```text
raw request host/header/claim
          ↓  host authentication + authorization
verified tenant principal / allowed scope
          ↓  tenancy resolver
row predicate OR datasource handle
          ↓
repository / transaction / event/storage/audit operation
```

## Cross-roadmap synergy

| Domain | Integration rule |
|---|---|
| OTel | emit only `vv.tenant.mode` and bounded resolution outcome |
| storage | hand scoped store/prefix/root after trusted resolution; no key escape |
| event PG | append/load stream under tenant routing; event history access authorized |
| audit | records tenant/actor/action durably under audit authorization |
| i18n | tenant default locale is presentation policy, never routing identity |
| outbox/jobs | durable envelope/work establishes authorized tenant scope explicitly |
| CRUD/policy | existing scope/gate semantics must cover complete query/write matrix |

---

## T-01 — verified scope type and context propagation

**Decision.** Tenant scope is an opaque immutable value created only by trusted
resolver/auth integration. Context carries it to transport-neutral service/
repository code; no public `WithTenant(ctx, rawString)` convenience bypasses the
verifier in production surface.

### Top-level declarative DX

```go
scope, err := tenants.VerifyAndResolve(ctx, principal, requestedTenant)
if err != nil { return err }
ctx = tenancy.WithScope(ctx, scope)
```

### Happy use cases

1. Authenticated principal is authorized for tenant A; resolver returns opaque
   active scope and request repository receives it through ordinary context.
2. Internal worker gets a durable job reference, resolves tenant through trusted
   control-plane record and then calls domain service with explicit scope.
3. A test uses a test-only verified scope factory, allowing tenant matrix tests
   without parsing HTTP/JWT or exposing raw production constructor.
4. Nested service/repository calls preserve same scope naturally with context;
   no thread-local/global variable creates cross-request contamination.
5. A cross-tenant admin workflow receives a distinct `AdminScope` capability
   with explicit allowed tenant set/batch policy, not an empty ordinary scope.

### Edge use cases

1. Raw `X-Tenant-ID` has arbitrary UUID/name. Host cannot pass it to tenancy
   directly; failed verification returns generic refusal before repository use.
2. Context is missing scope. Tenant-scoped decorator returns typed missing-scope
   error and executes zero SQL rather than broad unfiltered repository call.
3. Scope is copied into a background goroutine queued for days. Worker validates
   lifecycle/status at execution time; stale authorization is not immortalized.
4. Principal switches active tenant midway through request. Scope stays immutable
   for operation; a new authorized request/context is required.
5. Test accidentally uses `context.Background` with an unscoped repository.
   Test failure is loud; fake does not silently default to a test tenant.
6. A scope's debug string has tenant identity. Default error/log/trace printer
   redacts it; audit controlled access owns human-identifiable detail.

### Invariants and acceptance evidence

- missing/malformed/untrusted scope suite observes zero SQL/back-end selection;
- concurrent distinct scopes prove no global/current-tenant cross-contamination;
- external package compile fixtures cannot construct production verified scope
  from arbitrary raw ID without host resolver capability;
- context cancellation/error behaviour matches undecorated service apart from
  fail-closed scope check;
- raw tenant carriers are absent from errors/telemetry corpus.

### First implementation slice

Create opaque scope and missing-scope guard with test-only factory. Integrate it
first with one repository/service example before designing any identity/header
middleware, database pool or tenant administration API.

---

## T-02 — shared-database row isolation

**Decision.** One-db topology applies a declared tenant ownership relation to
all repository operations and validates ownership on create/update. It can use
PostgreSQL RLS as defence in depth, but application policy/SQL narrowing remains
explicit/tested and must not rely on an ambient `SET` value without transaction
discipline.

### Top-level declarative DX

```go
orders := tenancyrow.Scope(ordersRepo, tenancyrow.Column("tenant_id"))
```

### Happy use cases

1. `GetByID`, list, count, exists and aggregate add tenant narrowing generated
from verified scope; a tenant cannot observe another row even if it guesses ID.
2. `Create` assigns/validates current tenant ID in model relation and refuses a
   caller-supplied foreign tenant value.
3. `Update`, `Save` and bulk writes retain same tenant predicate and freeze
   tenant key so normal operations cannot rehome record across tenant boundary.
4. A child `Comment -> Article -> Tenant` uses declared ownership relation path
   with create-time parent verification, not only a scalar `tenant_id` shortcut.
5. PostgreSQL RLS policy using transaction-local verified setting provides a
   second guard; tests prove app queries and RLS agree on scope.
6. Admin reporting is a separately named policy/service that selects explicit
   tenant set and audits access; ordinary repository never drops predicate.

### Edge use cases

1. Bulk `UpdateAll/DeleteAll` has page/limit mismatch. Existing victim-selection
   safety decision applies; policy sees exact mutated set under tenant predicate.
2. Nested preload/filter/order/count generates a join/subquery. Tenant condition
   must survive every branch and cannot be lost by an unscoped relation query.
3. Upsert finds a tombstoned row from another tenant. It cannot resurrect/mutate
   it; save/restore semantics remain explicit and tenant scoped.
4. Application uses raw SQL/another ORM path bypassing vv decorator. Docs/DB RLS
   audit flag it as out-of-contract; no claim that vv protects arbitrary queries.
5. Connection pool reuses a session with RLS setting. Adapter uses transaction-
   local/reset-safe protocol; test proves tenant A setting cannot affect B.
6. Relation's ownership parent changes concurrently. Database constraints/locks
   and final write predicate must preserve scope, not a precheck alone.
7. A table genuinely global/public. It requires explicit unscoped resource policy,
   not absence of scope due developer forgetting tenancy decorator.

### Invariants and acceptance evidence

- generated integration matrix covers verbs × query shapes × direct/relation
  ownership × active/tombstone/missing scope cases;
- live PostgreSQL tests prove RLS/session pool reset behavior if advertised;
- no normal method accepts `tenant_id` as a caller filter/override;
- scope predicate is present in final SQL/recorder output for every tenant table;
- cross-tenant adversarial tests have control tenant-visible row and prove zero
  read/write/count/exists/aggregate leakage.

### First implementation slice

Implement scalar row ownership with strict create/freeze semantics and full read/
bulk test matrix. Add relation ownership and RLS only after its SQL, transaction
and connection-pool invariants are designed/tested, not as a decorator option.
