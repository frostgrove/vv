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

---

## T-03 — database-per-tenant datasource resolution and pool lifecycle

**Decision.** Database-per-tenant topology resolves a verified tenant scope to a
caller-owned datasource/client through a control-plane mapping. The resolver is
fail-closed, bounded in cache/pool lifecycle and independent from authentication
carrier parsing. A raw tenant ID never selects a DSN/database directly.

### Top-level declarative DX

```go
repo := tenancydatabase.Decorate(baseFactory, tenancy.Resolver{
    Lookup: controlPlane,
    Pools: poolManager,
})
```

### Happy use cases

1. Verified active tenant A resolves to its configured PostgreSQL datasource;
   repository command uses it for whole transaction/request scope.
2. Tenant B resolves to another datasource with identical resource/service code,
   while no caller exposes database name/DSN/pool key in a domain method.
3. Resolver cache has finite max/TTL/eviction policy and pool manager owns client
   construction, credentials, health, closing and backpressure.
4. An operation within one scope uses exactly one selected datasource/transaction;
   nested repository calls cannot re-resolve to another database mid-command.
5. New tenant provisioning creates DB/schema/migration state in control plane then
   changes lifecycle to active only after health/migration verification succeeds.
6. An inactive/suspended/migrating tenant returns declared unavailable/refused
   result before a data-plane SQL query, preserving zero cross-tenant fallback.

### Edge use cases

1. Resolver mapping missing/invalid/credential expired. It returns safe typed
   error; it never tries shared/default/last-used database.
2. Thousands tenants arrive once. Cache/pool has bounded queue/eviction; it does
   not create unlimited live connections or leak clients after one request.
3. Resolver returns datasource A for tenant B due control-plane bug. Integration
   tests detect misroute with sentinel data; trace/pool success cannot certify it.
4. Tenant DB is on older schema during rolling migration. Resolver/lifecycle
   refuses/informs cohort state; new repository SQL isn't run against unknown DB.
5. A transaction spans tenant A then code asks tenant B. Decorator refuses cross-
   tenant switch; named admin workflow/saga is required instead.
6. A worker replays job containing stale tenant reference. It re-verifies current
   lifecycle/mapping at execution and records bounded result, not old DSN context.
7. Pool close races an in-flight request. Pool manager's borrower/eviction contract
   makes lifecycle explicit; tenancy adapter never closes caller client directly.

### Invariants and acceptance evidence

- missing/wrong/disabled scope executes zero SQL against every datasource;
- two tenant databases with same record IDs prove no route/data cross-contamination;
- cache/pool stress test establishes max clients/eviction/close/active-borrow behavior;
- transaction fixture proves nested calls use one selected datasource per scope;
- database names/DSNs/usernames/tenant IDs stay absent from errors/OTel/metrics;
- migration cohort health/version compatibility is checked before active routing.

### First implementation slice

Start with injected resolver/factory and two test databases. Do not integrate
secret manager, cloud provisioning, HTTP host parsing or global pool singleton;
each is a separate consumer/control-plane decision.

---

## T-04 — relationship ownership and immutable tenant assignment

**Decision.** Many resources are owned transitively (`Comment → Article → Tenant`)
rather than carrying one scalar tenant column. Tenancy helpers must declare and
validate that relation at startup, narrow every relevant query through it, verify
create-time parents and freeze it on ordinary updates. Arbitrary dotted filter
strings are not a tenant policy API.

### Top-level declarative DX

```go
comments := tenancyrow.RelationScope(commentsRepo,
    tenancyrow.OwnedThrough("article", "tenant_id"),
)
```

### Happy use cases

1. List/get/count/exists of comments joins/narrows through article ownership under
   verified tenant scope and cannot return another tenant's comment by guessed ID.
2. Create comment validates that supplied article belongs to current tenant before
   insert; it cannot create a cross-tenant child by choosing a foreign parent key.
3. Update freezes article relation/tenant ownership unless a named authorized
   transfer/migration operation explicitly handles cross-tenant move semantics.
4. Nested preloads, filters, sorts, aggregates and relation predicates retain
   ownership narrowing under generated/validated relation plan.
5. Startup resolves declared relation field/table metadata and fails on typo/missing
   ownership path rather than silently emitting incomplete predicate.
6. Soft-delete/restore paths retain same relation scope and do not revive a child
   across tenant through ordinary Save/upsert semantics.

### Edge use cases

1. Relation is optional/null. Policy defines whether orphan is invalid/global or
   inaccessible; it never becomes visible because join predicate disappears.
2. Relation changes concurrently after create-time check. Final write/database
   predicate/transaction rule protects scope, not a stale existence check alone.
3. Deep preload creates unscoped join branch. Query matrix/control test proves
   tenant condition applies at every generated relation query, not only root list.
4. A bulk delete/update has page/limit mismatch. Existing victim set contract plus
   relation narrowing ensures inspected subjects equal changed subjects.
5. Parent article is soft-deleted/moved/restored. Child scope follows explicit
   domain policy and doesn't leak old parent/tenant history by join accident.
6. A global reference relation resembles tenant ownership. It must be separately
   declared public/global resource; no omitted relation means “probably global.”

### Invariants and acceptance evidence

- relation declaration validates at startup and has direct/nested/self relation tests;
- write matrix covers create/update/save/upsert/bulk/restore cross-tenant attempts;
- read matrix covers get/list/count/exists/aggregate/filter/order/preload relation forms;
- final SQL/recorder integration asserts ownership narrowing at every query path;
- no user-supplied relation/filter string can override the declared ownership plan.

### First implementation slice

Add one-level relation ownership after scalar helper's full security matrix is
green. Defer arbitrary relation traversal/DSL/recursive graph policy until a
bounded declaration schema and performance/security proof exist.

---

## T-05 — PostgreSQL RLS as defence in depth

**Decision.** PostgreSQL row-level security can provide a second enforcement
layer for shared database deployments, but it is not an excuse to omit vv policy
predicates/complete query tests. RLS session/transaction variables, roles and
connection-pool reset behavior are part of the tenancy contract if advertised.

### Top-level declarative DX

```go
tx, err := tenancyrow.WithPostgresRLS(ctx, executor, scope, func(ctx context.Context) error {
    return service(ctx)
})
```

### Happy use cases

1. App repository adds tenant predicate and transaction sets verified tenant RLS
   context locally; both independently permit only current tenant rows.
2. A raw SQL mistake inside same correctly configured transaction is blocked by
   RLS policy, giving defence in depth rather than proving raw SQL is supported.
3. Connection pool returns a session after transaction; next tenant transaction
   has its own local setting and cannot observe previous tenant's RLS value.
4. RLS role/policy migration is versioned/tested with table ownership and app
   role, documenting privileged admin/migration bypass behavior separately.
5. Live Postgres tests verify direct get/list/write and a raw control query under
   tenants A/B, plus missing-scope behavior fails safely.
6. Tenant lifecycle/suspension policy is represented in app/control-plane check;
   RLS alone does not silently decide business active status without a design.

### Edge use cases

1. RLS context set with session-wide `SET` leaks through pool to next request.
   Adapter uses transaction-local/reset-safe mechanism and regression test catches it.
2. App connection role bypasses RLS unexpectedly (owner/superuser/BYPASSRLS).
   Deployment checks/privilege test flag it; docs never imply protection is active.
3. Background worker opens query without tenancy RLS scope. It fails/uses named
   admin mode rather than reading all rows due a permissive database role.
4. RLS policy references unvalidated session text. Adapter only derives set value
   from opaque verified scope and validates syntax/length before SQL parameter/path.
5. Cross-tenant admin report requires privileged role. It has explicit purpose,
   bounded tenant cohort/audit; it never uses an empty RLS setting as bypass.
6. Schema migration needs RLS-disabled access. Migration runbook/role is separate
   from data-plane service credentials and cannot accidentally serve requests.

### Invariants and acceptance evidence

- advertised RLS mode has live Postgres pool-reuse/session-reset/role tests;
- app predicate and RLS matrix agree on allowed/denied rows for all resource verbs;
- privilege/owner/bypass configuration is verified in deployment health/review;
- RLS setting/raw tenant identity never appears in logs/traces/metric labels;
- disabling RLS doesn't make app policy tests pass falsely; both layers tested.

### First implementation slice

Treat RLS as a later hardening phase after application row scope is complete.
Ship an explicit Postgres-only adapter/ADR with test fixture; never add an opaque
`EnableRLS` boolean to a database-agnostic tenancy core.

---

## T-06 — tenant lifecycle, provisioning and deprovisioning

**Decision.** Tenant lifecycle is a control-plane state machine distinct from
ordinary tenant data: requested, provisioning, migrating, active, suspended,
deleting, archived/deleted. Data-plane resolver admits only allowed states; it
does not create schemas/databases, run migrations or delete data on first request.

### Top-level declarative DX

```text
requested -> provisioning -> migrating -> active -> suspended -> deleting -> archived
```

### Happy use cases

1. Provisioner creates shared-row registry entry or tenant DB through controlled
infrastructure process, applies required schema/version then marks active atomically.
2. Resolver denies provisioning/migrating/suspended tenant data-plane requests
   with bounded lifecycle outcome before query/datasource selection.
3. A migration controller advances a bounded tenant cohort and records schema/app
   compatibility state, allowing active traffic only at known compatible version.
4. Deprovision workflow freezes new commands, handles retention/audit/event/storage
   lifecycle, archives/deletes only after authorized policy/hold checks.
5. A deleted logical tenant ID is not immediately reused; control-plane uniqueness/
   retention policy prevents old backups/events/objects becoming another customer.
6. Admin lifecycle actions are separately authorized/audited and use explicit
   resource limits, not a tenant-scoped service method with nil scope.

### Edge use cases

1. Provisioning creates DB but migration fails. State remains non-active; resolver
   cannot route traffic into half-created database or default database fallback.
2. Tenant DB migration partially succeeds. Cohort controller/recovery records
   version/error and blocks incompatible writer/query operation until fixed.
3. Suspend occurs while request/job transaction is in flight. Contract defines
   whether current tx completes; subsequent commands/retries recheck lifecycle.
4. Delete conflicts with legal hold/audit/event retention/object storage lifecycle.
   State records blocked/deferred rather than broad physical deletion guess.
5. Restore tenant from backup has older schema/control-plane map. Restore runbook
   validates IDs, schema/upcasters/keys and marks active only after verification.
6. Provisioner credentials/control-plane outage. Data-plane never tries to self-
   provision from user request or silently caches a nonexistent active state.

### Invariants and acceptance evidence

- lifecycle state transition table has authorization, idempotency, audit and rollback tests;
- every non-active state results in zero unintended data-plane SQL/DSN fallback;
- shared/db-per-tenant provisioning/migration/restore/deletion have separate runbooks;
- tenant ID reuse, backup, event/audit/storage retention scenarios are explicit;
- lifecycle metrics use bounded state/topology/cohort results, never tenant labels.

### First implementation slice

Document lifecycle and resolver active-state guard before a provisioning API. Start
with an injected control-plane fake/test state machine; infrastructure/cloud/secret
manager integrations are intentionally outside the tenancy satellite core.

---

## T-07 — schema migration and application release cohorts

**Decision.** Shared-db migration is one controlled schema release; db-per-tenant
migration is a fleet/cohort process with durable status, compatibility gates and
rollback/forward-fix plan. A new application binary cannot run arbitrary queries
against a tenant whose database version/resolver lifecycle is unknown.

### Top-level declarative DX

```text
cohort C17: schema 42 -> 43; reader min 42; writer min 43; status migrating
```

### Happy use cases

1. Shared database deploys expand migration before application needs new column/
   index/policy; contract phase waits until all readers/writers move.
2. Db-per-tenant controller selects a bounded cohort, migrates each DB idempotently,
   validates health/version, then marks tenant compatible/active for new writer.
3. Application capability gate checks tenant schema compatibility at resolver
   boundary and uses old-compatible behavior or fails clearly during rollout.
4. A migration is resumable after controller crash: durable tenant/cohort state
   records attempted/version/result without an unbounded in-memory list.
5. Rollback decision uses documented database migration direction: revert before
   incompatible writes or forward-fix after new schema/data has been used.
6. OTel/metrics report bounded migration cohort/version/outcome; tenant DB name or
   identity remains protected control-plane/audit information.

### Edge use cases

1. Migration succeeds on 999 tenants and fails one. Controller isolates/fixes
   failed tenant; it doesn't mark fleet active or rerun destructive DDL blindly.
2. New app writer touches schema feature before cohort upgrade. Resolver/capability
   gate blocks traffic/action rather than producing arbitrary SQL errors/data drift.
3. Long migration locks a tenant DB. Lifecycle changes to migrating/maintenance;
   data-plane error policy is explicit and retries don't choose another DB.
4. Shared migration requires RLS/index constraint change. Live Postgres transaction/
   pool tests verify app/RLS safety across expand/contract steps.
5. Backup restore returns schema behind active app. Control plane detects version,
   applies/replays required migrations under runbook before marking tenant active.
6. Cross-tenant report spans mixed schema cohorts. Admin/report workflow declares
   compatible query version/cohort filter; no assumption all DBs are identical.

### Invariants and acceptance evidence

- every schema/app release has shared and db-per-tenant compatibility table;
- controller state transitions/migration idempotency/cancellation/retry run live fixtures;
- app route/query never hits a tenant DB below required schema capability silently;
- migration operations have scoped credentials/targets and validated destructive boundaries;
- tenant/cohort IDs absent from generic metrics/traces/default logs.

### First implementation slice

Start with schema-version control-plane interface and two-tenant migration fake/
Postgres integration fixture. Defer a general migration SaaS/CLI/orchestrator until
the lifecycle/credentials/rollback contract is clear.

---

## T-08 — asynchronous work, events and explicit scope restoration

**Decision.** Outbox messages, jobs, webhook deliveries, event projections and
scheduled maintenance do not inherit an ambient request tenant automatically.
Their durable envelope/job state names an authorized tenant/domain reference or
admin cohort; worker resolves current verified scope/lifecycle before selecting
repository/store/datasource. Raw trace baggage/header cannot route tenant data.

### Top-level declarative DX

```go
scope, err := resolver.ForWork(ctx, work.TenantReference)
if err != nil { return err }
return handler(tenancy.WithScope(ctx, scope), work)
```

### Happy use cases

1. Outbox event includes a declared durable tenant reference authorized by source
   transaction; publisher/consumer re-resolves it through control plane at work time.
2. Scheduled per-tenant job enumerates an explicitly authorized bounded cohort,
   establishes one scope per unit and never uses `context.Background` as all-tenants.
3. Event-store projection/checkpoint uses tenant scope or named shared global
   projection policy, preserving isolation in both one-db and DB-per-tenant modes.
4. Storage reconciliation receives scoped store after tenant resolver; crafted
   object key cannot select another prefix/root/bucket/database.
5. Worker retry sees suspended/deleted tenant and handles declared lifecycle outcome,
   rather than executing stale authorization/datasource selection.
6. OTel propagation can link causal work but tenancy resolver remains authoritative
   and emits only bounded topology/outcome, never raw carrier/tenant label.

### Edge use cases

1. Durable work envelope has tenant reference malformed/deleted/reused. Worker
   fails/quarantines safely and does zero unscoped SQL/action.
2. Event is globally shared by design. It uses a named global/admin projection
   capability and audit policy, not absence of tenant scope by accident.
3. A task fans out across 10,000 tenants. Batch/cohort/cancellation/backpressure
   policy is explicit; no one context/transaction/pool/trace is held across all.
4. Worker retry after data-plane error tries a default datasource. Resolver design
   refuses fallback; controlled retry rechecks same scope/lifecycle only.
5. Trace carrier has `tenant_id` baggage. Worker ignores it for routing and tests
   prove hostile carrier cannot override durable verified reference.
6. Manual operator replay has broader scope. It requires named admin capability,
   bounded cohort/purpose/audit instead of injecting arbitrary tenant IDs into job.

### Invariants and acceptance evidence

- every background handler fixture starts without a tenant and proves explicit resolution;
- stale/malformed/misrouted/suspended scope suite executes zero cross-tenant action;
- outbox/event/job envelope schema separates tenant reference from raw headers/baggage;
- worker/pool/trace resource limits are bounded per tenant/batch rather than global;
- admin/global work has explicit authorization/audit/purpose documentation.

### First implementation slice

Publish worker scope-restoration helper/reference before any automatic job adapter.
Keep broker/job engine dependency outside tenancy core; test with in-memory durable
work fixture plus future event/outbox contract adapters.

---

## T-09 — tenant-neutral observability and diagnostics

**Decision.** OTel/metrics/log correlation can report topology (`row`/`database`),
resolution outcome and configured resource/operation. They cannot expose tenant
ID, database/schema/DSN, control-plane map, raw host/header or a stable hashed
tenant token. Authorized audit/support systems own detailed diagnosis.

### Top-level declarative DX

```go
observed := tenantotel.Decorate(resolver, tenantotel.Config{
    Telemetry: telemetry,
    Mode: tenantotel.Database,
})
```

### Happy use cases

1. Resolver span records `vv.tenant.resolve` and `vv.tenant.mode=database` with
   bounded `ok`, `missing`, `inactive`, `unavailable`, `error` result categories.
2. Service/repository spans retain resource/operation/outcome and correlate a
   tenant resolver child event without new tenant-specific metric time series.
3. One-db vs db-per-tenant dashboard compares topology-wide failure/latency/capacity
   trends using finite configured dimensions and application resource names.
4. Structured application log may carry trace/span IDs through OTel helper but no
   raw tenant identity unless an authorized host log policy separately chooses it.
5. Migration controller emits cohort/state aggregate progress using bounded state
   classes; per-tenant detail stays in protected control-plane/audit records.
6. Telemetry no-op/sampling/exporter outage leaves scope resolution/policy/pool
   lifecycle and public results exactly unchanged.

### Edge use cases

1. Operator requests per-tenant metric for one noisy customer. Generic bridge
   refuses; authorized support/audit/control-plane query is the correct workflow.
2. Resolver error wraps DSN/tenant/database name. Error projection strips it from
   trace/metric/default logs while retaining safe bounded class.
3. Hashing tenant ID looks safe. It remains durable high-cardinality correlation
   token and is prohibited absent specific rotating/privacy-approved design.
4. One request has many tenant scopes due admin batch. Metric/span events cap and
   use admin/batch mode, never append all tenant values.
5. Raw header host encodes tenant name. It is not an attribute even on invalid
   routing error; host transport instrumentation needs separate privacy review.
6. A collector backend is widely accessible. vv-emitted topology evidence alone
   cannot identify a tenant/database/customer by default vocabulary.

### Invariants and acceptance evidence

- registry test rejects tenant/database/schema/DSN/raw carrier attributes in all signals;
- cardinality stress with thousands tenant IDs creates no new metric dimensions;
- malicious resolver/host/error corpus scanner finds no protected sentinel export;
- trace topology suite works for shared/db-per-tenant with same safe attributes;
- telemetry bridge contains no resolver/provider/global exporter ownership imports.

### First implementation slice

Wait for tenancy resolver semantics and reuse OTel O-15 registry. Add trace
fixtures before production bridge, and refuse every “just add tenant_id label”
shortcut during design review.

---

## T-10 — audit revisions, history access and tenant lifecycle evidence

**Decision.** Audit is scoped to the same verified tenant boundary as the domain
mutation. In shared DB mode, revision/item/query rows carry protected tenant scope
and use application/RLS/composite constraints. In database-per-tenant mode, audit
writes join the selected tenant transaction or truthfully use an explicitly local
delivery model. A control-plane action or cross-tenant investigation is not an
ordinary tenant audit query with `nil` scope.

### Top-level declarative DX

```go
scope, err := tenants.VerifyAndResolve(ctx, principal, requested)
if err != nil { return err }

uow := tenants.UnitOfWork(ctx, scope)
orders := audit.Decorate(uow.Orders(), orderAuditPolicy)
```

The service receives a unit of work already bound to one verified scope. It does
not add `tenant_id` to audit row structs or select a tenant database in command
code after opening a transaction.

### Happy use cases

1. A shared database order update writes its order row and one audit revision/item
   under the same tenant predicate/transaction; protected reader returns it only
   within that tenant scope.
2. A database-per-tenant order update writes audit tables in that selected tenant
   database. Migration/readiness ensures policy schema exists before activation.
3. A tenant lifecycle action such as suspension is a control-plane resource with
   its own audit policy/actor/reason—not an unscoped modification to every tenant.
4. A support agent investigates one customer under a purpose-bound grant. Audit
   query is single-tenant; display labels resolve at edge without storing tenant
   names in action or telemetry.
5. An automated reconciliation worker acts for one tenant. It restores verified
   scope/service actor from durable job envelope before writing audit evidence.
6. An authorized fraud case needs two tenants. A separate short-lived cohort
   capability coordinates per-tenant history readers and audits investigator access.

### Edge use cases

1. Audit revision includes correct tenant but audit item/query omits it. Composite
   foreign keys/RLS/query-matrix tests prevent a join from leaking another tenant's
   item through a guessed revision or subject identity.
2. Resolver cache changes mapping between audit write and history read. Epoch/
   database binding checks make stale mapping a fail-closed error, not a silent
   foreign history location.
3. Tenant is suspended after mutation begins. Transaction contract specifies
   whether already-authorized request may finish; later writes/queries fail closed
   and audit truthfully records authorized state at decision time.
4. A support export spans tenant cohort. Normal endpoint refuses; cross-tenant
   workflow bounds cohort/purpose/time and returns partial/unavailable state rather
   than hiding inaccessible databases.
5. Tenant deletion conflicts with audit legal hold/retention. Lifecycle registry
   marks tenant as pending/legal-hold, preserving DB/audit evidence until approved.
6. Impersonation uses a support principal acting for tenant user. Audit records
   typed effective/impersonator lineage under policy, never caller-provided strings.
7. Shared RLS session setting leaks across pooled connections. Checkout/reset tests
   ensure next tenant cannot inherit prior tenant role/setting or audit scope.

### Invariants and acceptance evidence

- all normal audit revision/item/history APIs require exactly one verified scope;
- shared and db-per-tenant profiles execute same commit/rollback/history denial tests;
- lifecycle/control-plane audit has a distinct resource/authority/storage boundary;
- query plans/constraints/RLS tests cover revision-to-item-to-value joins and exports;
- cross-tenant access is capability/purpose/cohort/time-bound and separately audited;
- pool reset/mapping change/suspension/deletion/hold tests prove no foreign evidence;
- audit/OTel layers never export tenant/database name as generic metadata.

### First implementation slice

Require scope on every audit mutation/read and bind audit to `tenants.UnitOfWork`.
Ship shared DB test profile first. Do not expose cross-tenant history or central
audit aggregation until lifecycle, grants and federation behavior are designed.

---

## T-11 — scoped object storage, filesystem roots and S3-compatible prefixes

**Decision.** Storage receives a resolved tenant storage namespace/capability, not
a raw tenant string it turns into a filesystem path, bucket, S3 key or metric
label. Shared object stores use opaque scoped keys/prefixes plus storage policy;
db-per-tenant does not require a separate bucket by default, but any bucket/account
mapping belongs to the same trusted control plane and lifecycle registry.

### Top-level declarative DX

```go
store, err := tenantStorage.ForScope(ctx, scope)
if err != nil { return err }

ref, err := store.Put(ctx, storage.PutRequest{
    Key: storage.Key("documents/opaque-object"),
    Body: body,
})
```

`ForScope` returns a capability with an authorized logical namespace. Application
code never concatenates `tenantID + "/" + userFileName` or receives bucket/DSN
credentials just to upload a file.

### Happy use cases

1. Shared S3-compatible backend maps verified scope to an opaque partition/prefix
   and policy-bound client. Tenant A cannot list/read/write Tenant B keys through
   normal storage contract even when keys are guessed.
2. Local filesystem development adapter resolves to a preconfigured secure root
   and tenant namespace; path traversal/symlinks cannot escape due raw name input.
3. A database-per-tenant customer uses a dedicated storage account/bucket by
   control-plane policy. Resolver obtains configuration/credentials from protected
   mapping, and application sees same `Store` capability.
4. A pre-signed download is generated only after storage/tenant/resource policy.
   Audit stores safe logical object reference/version/digest, never signed URL.
5. Tenant suspend disables new storage mutations and download issuance while
   retention/legal-hold workflow can preserve approved evidence objects.
6. Tenant migration copies objects through staged/checksummed process; mapping
   cutover is fenced and old/new references remain reconcilable under policy.

### Edge use cases

1. Filename contains `../`, Unicode confusable separator or URL encoding. Key
   grammar treats it as opaque data/invalid input, never allows it to form a path.
2. User supplies an S3 URL/bucket/prefix claiming another tenant. Contract rejects
   it; only control plane chooses backend namespace/credentials.
3. Object write commits but later DB/audit transaction fails. Reconciler finds
   orphan staged/promoted object under scoped quarantine and never calls it evidence.
4. Storage lifecycle deletes noncurrent version linked by audit. Cross-policy
   validation blocks/flags conflict before destructive object lifecycle executes.
5. A pooled S3 client/credential cache is reused for wrong scope. Client capability
   binding/cache key/expiry tests prevent leakage; errors redact endpoint/bucket.
6. Tenant is moved to another region/account. Presigned URL/ref cache invalidation,
   object version/digest and eventual replication state are explicitly handled.
7. Bulk listing is requested without known keys. Default tenancy store declines
   unbounded listing; a named admin inventory process has cohort/purpose controls.

### Invariants and acceptance evidence

- raw tenant identity never appears in fs paths/S3 key grammar/bucket selection API;
- secure fs and at least one S3-compatible conformance suite prove scope isolation;
- storage reference includes opaque logical ref/version/digest rather than endpoint URL;
- migration/suspend/delete/hold tests reconcile DB/audit/object lifecycle boundaries;
- pool/credential/cache/error/telemetry scans contain no foreign namespace or secret;
- signed capability issue/expiry/download authorization is tenant/resource scoped;
- every special dedicated-bucket/account mapping is inventory-visible in control plane.

### First implementation slice

Add `ForScope` facade around storage with a shared opaque namespace mapping and
fs/S3 isolation tests. Defer per-tenant bucket/account provisioning and object
migration until tenant lifecycle/control-plane ownership exists.

---

## T-12 — PostgreSQL event streams, projections and tenant routing

**Decision.** The PostgreSQL-only event store is tenant aware at stream creation,
append, load, snapshot, projection, outbox and reader boundaries. It uses an
opaque verified tenant scope/binding; event payloads/stream identifiers cannot
choose another tenant database or bypass shared-row isolation. No generic event
store abstraction hides the distinct shared versus database topology semantics.

### Top-level declarative DX

```go
events := eventpg.ForTenant(scope, tenantUnitOfWork)
aggregate, err := events.Load(ctx, orderStream)
if err != nil { return err }
err = events.Append(ctx, aggregate, expectedVersion)
```

The facade binds scope and transaction/datasource before load/append. It does not
accept a `tenantID` parameter on each method, raw DB connection string in event
metadata or a cross-tenant stream id as a substitute for authorization.

### Happy use cases

1. Shared PostgreSQL table includes tenant scope in stream uniqueness/index and
   every append/load/snapshot/projection query; same aggregate ID may exist in two
   tenants without cross-read/collision.
2. Database-per-tenant event store selects one database using resolver mapping;
   expected-version/transaction behavior is local to that database and explicit.
3. Outbox worker reads one tenant partition/work item, reconstructs verified scope
   and publishes a tenant-authorized integration envelope without payload routing
   from untrusted event metadata.
4. A projection rebuild for one tenant runs under system actor/scope with rate/
   resource bounds. It cannot accidentally rebuild every tenant through a global
   connection because a scope is mandatory.
5. Event-upcaster converts old event revision inside correct tenant stream; it
   preserves original tenant/stream identity and never interprets payload field as
   a datasource selector.
6. Audit links a committed event transaction/ref to same scope while keeping event
   and audit readers separately authorized.

### Edge use cases

1. Aggregate stream ID is guessed across tenant. Shared composite key/query/RLS
   returns no foreign events; db-per-tenant resolver never opens foreign store.
2. Projection checkpoint is global but events are tenant scoped. Checkpoint model
   names per-tenant partition/epoch or a governed coordinator, avoiding a cursor
   that skips/duplicates a tenant after migration.
3. Tenant moves database while outbox/projection jobs are pending. Migration epoch,
   drain/fence/replay contract prevents two writers or lost/duplicated publication.
4. A batch command legitimately affects tenants. It is not modeled as one aggregate
   append across DBs; named orchestration uses per-tenant commands/outcomes/compensation.
5. Event payload contains `tenant_id` from legacy import. Import verifies/map it in
   control plane and stores scoped stream; it does not trust payload to route SQL.
6. Snapshot restored from backup belongs to old tenant mapping. Import/restore
   checks scope/epoch/schema before attaching; no overwrite of current stream.
7. RLS policy does not apply to a maintenance role. Maintenance procedure has named
   cohort/scope and audit, never a broad unrestricted replay script by default.

### Invariants and acceptance evidence

- stream/aggregate/snapshot/outbox/projection schemas and queries encode topology scope;
- optimistic append tests execute same aggregate ID across tenants and prove isolation;
- tenant move/drain/checkpoint/import/replay fault tests have deterministic outcomes;
- async event workers require verified scope and do not route from event payload;
- audit/event causal link requires matching approved tenant scope and distinct reader gates;
- event OTel signals use topology/outcome only, never stream/tenant/database identity;
- documentation names no cross-database atomic ES promise for db-per-tenant workflows.

### First implementation slice

Choose tenant-scope inclusion in initial PostgreSQL event-store schema/API before
the first stream. Build shared DB append/load isolation test, then db-per-tenant
resolver profile. Leave tenant migration/federated reports for later ADRs.

---

## T-13 — locale preference, i18n catalogue and tenant branding boundaries

**Decision.** Tenant locale/default language is presentation configuration, not
the tenancy identity/routing mechanism. A verified tenant scope may authorize a
bounded default-locale/brand catalogue choice through control plane, while request
user preference and `Accept-Language` still follow i18n resolver/fallback rules.
Business data, audit actions, event types, storage keys and telemetry remain
language-neutral stable identifiers.

### Top-level declarative DX

```go
presentation, err := locales.ForRequest(ctx, locale.Request{
    Tenant: scope,
    UserPreference: userLocale,
    AcceptLanguage: header,
})
if err != nil { return err }

text := presentation.Message("orders.status.confirmed", args)
```

The i18n satellite receives trusted scope only to select a permitted configuration;
it never treats locale string/translated tenant name as proof of tenant access.

### Happy use cases

1. Tenant default is Kazakh, while user explicitly chooses Russian. Resolver uses
   explicit user choice first, then tenant default, then platform fallback under
   BCP-47 validation and deterministic policy.
2. Tenant custom branding permits a reviewed message override set. Manifest/version
   identifies it; missing keys fall back safely without arbitrary template code.
3. Audit viewer renders stable action/reason code in viewer locale while storing
   original tenant scope/action keys, not localized strings from write time.
4. Event payload contains stable event type/enum and user-facing notification
   is localized by receiving service/tenant policy at delivery edge.
5. A support operator views foreign tenant under authorized case. Viewer locale
   preference may differ from tenant default; capability governs data, not language.
6. Tenant changes default locale. New presentations update according to policy;
   historic audit/event records remain semantically stable and need no rewrite.

### Edge use cases

1. Raw hostname includes tenant plus locale. Authentication verifies host/tenant;
   locale parser treats locale separately and neither raw carrier reaches telemetry.
2. A tenant uploads a translation with unsafe ICU/template construct or huge text.
   Compiler/manifest/size/argument validation rejects it before runtime rendering.
3. Tenant name appears as message argument. Renderer follows output escaping and
   does not use it as catalogue/key/path/filter/trace attribute.
4. User asks locale not available for tenant's regulated template. Resolver applies
   declared fallback/denial and preserves content requirements; it does not choose
   a different tenant catalogue based on similar language code.
5. DB-per-tenant migration puts newer tenant catalogue schema on one cohort. Locale
   resolver reports compatibility/configuration failure safely rather than panics.
6. Operator tries metric label per tenant language combination. OTel registry keeps
   bounded locale/topology dimensions and prohibits tenant-linked high cardinality.

### Invariants and acceptance evidence

- tenant scope/default locale/user preference are distinct typed inputs with order;
- stable action/event/reason/error keys are stored; translation occurs only render edge;
- tenant-custom catalogue has manifest/version/limits/argument/escaping tests;
- resolver rejects malformed locale and never uses it for tenancy/database/storage routing;
- audit/event/export old data renders under current/fallback catalogue without rewrite;
- tenant default changes/missing catalogues/role viewers/cross-tenant support tests pass;
- OTel/log scanner asserts absent raw tenant/translation text/arguments as telemetry data.

### First implementation slice

Use platform catalogue plus one optional verified tenant default locale. Do not add
per-tenant arbitrary translation authoring/branding until manifest, review, cache,
escape and release compatibility controls are in place.

---

## T-14 — explicit cross-tenant administration, reports and batches

**Decision.** Cross-tenant work is a separate control-plane capability, not an
empty tenant context, an `all=true` filter or a repository that accepts a slice
of tenant IDs. It names purpose, authority, bounded cohort, time budget, results
contract, audit and operator ownership. It fans out through scoped operations so
each tenant adapter keeps its normal isolation boundary.

### Top-level declarative DX

```go
grant, err := admin.AuthorizeCohort(ctx, tenancy.CohortRequest{
    Purpose: "billing-reconciliation-2026-08",
    Tenants: cohortRefs,
    Expires: time.Now().Add(30 * time.Minute),
})
if err != nil { return err }

report, err := admin.RunScoped(ctx, grant, reconcileOneTenant)
```

The callback receives one verified tenant scope at a time. It cannot acquire a
general database pool or SQL connection for arbitrary tenants.

### Happy use cases

1. Finance reconciliation uses approved cohort of active tenants, bounded
   concurrency/time and per-tenant idempotency. Result exposes each outcome and
   cannot be mistaken for complete success when a database is unavailable.
2. Control-plane operator rolls out schema to a cohort. Each database migration
   is scoped, recorded/audited, fenced from incompatible app traffic and retryable.
3. Security investigator receives a short-lived two-tenant case grant. History
   federation obeys individual tenant reader policy and audit access evidence.
4. A platform-wide metric reports number of tenant migrations by bounded state;
   it does not embed tenant/db identity in labels.
5. A multi-tenant export stages separate scope-authorized results and merges only
   under explicit policy; output records completeness/cohort/authorization.
6. An admin wants all active tenants. Control plane snapshots/version-tags an
   explicit cohort before work so activation/deactivation races are truthful.

### Edge use cases

1. One tenant fails while others succeed. Contract records per-tenant completed/
   failed/unknown/retry state and avoids rolling back completed independent DBs
   as if a distributed transaction existed.
2. Caller supplies 100k IDs. Cohort authorizer enforces maximum/selectors/approval
   and pagination; it cannot cause unbounded resolver pools or data exfiltration.
3. Grant expires halfway through long run. Worker stops new scopes, records exact
   outcome, and resumption requires renewed authority/idempotency reconciliation.
4. Tenant becomes suspended/deleted/migrating during cohort. Each scoped callback
   rechecks lifecycle/epoch and result makes that state visible.
5. Cross-tenant query uses shared DB and forgets predicate in a join. It is still
   privileged but query builder/RLS/tests preserve selected cohort membership.
6. Operator attempts CSV global dump. Export rules restrict fields/tenant residence,
   stage access/TTL/approvals and never rely on batch grant alone as data permission.
7. A service tries to call `RunScoped` recursively creating N² fan-out. Capability
   prohibits/requires explicit nested budget/delegation and exposes safe failure.

### Invariants and acceptance evidence

- no public normal repository method accepts zero/multiple tenant scopes;
- cohort grants are immutable/purpose/authority/expiry/budget/audit-bound;
- fan-out executor uses one scope per callback and disposes connections/credentials;
- partial/unknown/expired/deleted/migrating outcomes have typed result model;
- cross-tenant SQL/report/export tests prove exact cohort/RLS/field/access controls;
- watchdogs bound concurrency, duration, memory, output/object size and retries;
- audit/OTel record safe control-plane mode/outcome, never full cohort/tenant IDs.

### First implementation slice

Keep cross-tenant APIs out of the first tenancy satellite. When required, ship one
read-only, two-tenant, purpose-bound operation with explicit partial result and
audit before bulk reports, migrations or global data exports.

---

## T-15 — control-plane registry, credential isolation and resolver cache safety

**Decision.** A tenant control plane is the sole authority for lifecycle state,
topology binding, datasource/storage configuration reference, schema/policy epoch
and bounded capabilities. It is distinct from tenant data plane and is accessed
through a trusted resolver interface. Application request paths never acquire or
store raw DSNs, passwords, bucket credentials or direct tenant-to-host mappings.

### Top-level declarative DX

```go
binding, err := registry.Resolve(ctx, verifiedScope)
if err != nil { return err }

handle, err := databases.Acquire(ctx, binding)
if err != nil { return err }
defer handle.Release()
```

`binding` is opaque, versioned and short-lived. It may point to a row-policy
profile or database handle, but it is not serializable application data or an
input constructible from HTTP/event fields.

### Happy use cases

1. Resolver receives verified tenant scope and returns active shared-row profile
   with policy epoch. Repository composes expected predicates/RLS setup without
   learning control-plane credential details.
2. Resolver receives a database-topology scope and obtains credentials through
   host-owned secret provider. Pool uses protected binding key/TTL and application
   gets a tenant-bound transactional handle.
3. Tenant is rotated to new database credentials. Registry increments binding
   epoch, stale pools drain, new checkout verifies epoch and requests continue
   without exposing secret/version in logs.
4. Tenant is suspended. Registry returns typed inactive state before datasource/
   storage acquisition; normal service sends no tenant SQL or object request.
5. Migration updates schema epoch. Resolver refuses incompatible app capability or
   routes to compatible cohort; operations dashboard shows counts by safe state.
6. A local development registry uses fixed safe mappings, while production adapter
   integrates host secret manager without pulling its SDK into tenancy root module.

### Edge use cases

1. Cache entry outlives suspend/delete/rehome. Every use validates expiry/epoch/
   lifecycle as required; safe stale result is a fail-closed/retryable resolver error.
2. Credential rotation happens during long transaction. Current transaction uses
   allowed bounded lifetime; new work acquires new binding. No hidden automatic
   cross-database retry turns unknown commit into duplicate mutation.
3. Registry is unavailable. Shared-row app may have no reason to query tenant
   data without verified active binding; db-per-tenant application fails closed,
   with availability policy/operator cache design explicitly decided.
4. Malicious tenant label has same display name as another. Resolver only uses
   opaque verified identity/key; display name is presentation/control-plane data.
5. A cache key uses unbounded tenant text. Internal binding cache uses opaque
   canonical ref plus topology/epoch and limits size/eviction/pool fan-out.
6. A library tries to expose `ResolveDSN` convenience method. API review rejects
   it because it widens credential/routing authority beyond the safe handle contract.
7. Registry audit itself needs a tenant scope. Control-plane audit uses separate
   system/control-plane policy, avoiding circular call through tenant data resolver.

### Invariants and acceptance evidence

- resolver accepts only verified opaque scope/ref and produces no raw credentials to app code;
- registry/binding state machine covers active/suspended/migrating/deleted/error states;
- cache/pool tests exercise expiry/epoch/rotation/suspension/delete/mapping race;
- no DSN/database/bucket/secret/display tenant value appears in errors/traces/metrics;
- secret manager and identity provider remain host/consumer dependencies, not root tenancy imports;
- startup/readiness checks distinguish registry availability from tenant data connectivity;
- direct raw datasource selection from request/job/event payload is impossible through public API.

### First implementation slice

Define opaque binding with active state/epoch and an injected resolver. Implement
one bounded cache and a test-only fake registry. Defer distributed registry/cache
coherence optimizations until observability and outage semantics are explicit.

---

## T-16 — database migration cohorts, compatibility fences and release protocol

**Decision.** Db-per-tenant releases are a fleet operation with explicit cohort
state, schema/capability versions, migration idempotency, compatibility fence and
rollback plan. Shared DB migration is also versioned/online-aware, but cannot be
treated as proof that the same application release is safe for every tenant DB.

### Top-level declarative DX

```text
tenant cohort: wave-3 / schema 12 / app capability min=41 max=43 / state=ready
resolver rule: acquire only when binding.schema in app.compatibleSchemaRange
migration job: one tenant + migration id + lock + outcome + retry checkpoint
```

Operators view named state rather than inferring it from deployment version,
database hostname or a best-effort list of migration log lines.

### Happy use cases

1. New nullable column/index is expanded on shared DB, reader supports old/new,
   writer cutover happens after migration checks and rollback remains safe.
2. Db-per-tenant fleet groups active tenants by cohort. Migration runner obtains
   one binding at a time, checks lifecycle/epoch, applies idempotent migration,
   verifies schema/policy/audit/event readiness, then marks cohort result.
3. Application deployed before writer cutover is backward compatible with old
   tenant schema and resolver fences any unsupported operation with safe status.
4. One tenant migration fails due to capacity/lock. Other cohort results stay
   accurate; runner retries/halts that tenant without falsely reporting fleet green.
5. Security patch must roll out quickly. Registry supports emergency cohort policy
   while preserving compatibility fence/recorded exception and rollback posture.
6. Tenant onboarding creates database from versioned baseline then applies current
   migration/audit/event/storage readiness checks before it becomes active.

### Edge use cases

1. Old app version is rolled back after some tenant DBs reached new write-only
   schema. Deployment guard blocks unsafe old writer; forward fix/read-only mode
   is selected instead of destructive down migration.
2. Two migration workers acquire same tenant. Database/control-plane lock and
   idempotent migration record ensure one owner; other reports deterministic state.
3. Tenant is moved/migrating while migration runs. Epoch check/lease/fence aborts
   or reschedules, preventing a schema result applied to wrong database generation.
4. A migration takes a table lock and impacts writes. Plan names online syntax,
   lock timeout, traffic guard, abort/rollback criteria and capacity monitoring.
5. Archived/offline tenant returns after months. Re-activation path runs compatible
   schema/audit/event/credential checks; old database cannot rejoin traffic directly.
6. Backfill uses global control-plane query and accidentally spans all tenants.
   Runner requires explicit cohort/scope/budget and per-tenant progress, not `all`.
7. One desired schema version relies on changed event/audit codec. Cohort considers
   app reader/writer compatibility and retained data—not DDL state alone.

### Invariants and acceptance evidence

- registry has schema/capability/migration state per binding, with owner/time/attempt;
- migration runner is idempotent, locked, scoped, bounded and auditable;
- application checks compatibility before a tenant write path reaches SQL/storage/event store;
- expand/cutover/rollback drill covers shared DB and a partial db-per-tenant cohort;
- negative tests cover duplicate runner, stale epoch, offline activation, old app/new schema;
- operations dashboard shows aggregate cohort states only, with protected detail path;
- no telemetry dimension includes database/tenant/migration identifier values.

### First implementation slice

Introduce a compatibility field and fail-closed resolver rule before fleet tooling.
Run a manual two-database cohort rehearsal with one deliberate failure. Defer auto
wave scheduling until idempotency/locks/alerts/readiness evidence exists.

---

## T-17 — backup, restore, disaster recovery and tenant move semantics

**Decision.** Backup/restore and migration are topology-specific tenant lifecycle
operations. Shared DB cannot promise inexpensive isolated tenant restore without
an explicit extraction/reconciliation plan. Db-per-tenant can isolate backups more
readily, but registry mapping, credentials, audit/event/outbox/storage references
and post-restore fencing remain critical to prevent duplicate or stale operation.

### Top-level declarative DX

```text
restore request -> authorized control-plane case -> isolated restore target
-> validate schema/tenant epoch/audit/event/storage references -> reconcile
-> approve cutover or export -> record outcome
```

There is no `RestoreTenant(id)` shortcut that reconnects a historical database
to live traffic before data consistency, lifecycle and access control checks.

### Happy use cases

1. Db-per-tenant backup restores to isolated database. Validator checks stable
   tenant ref, original epoch, schema/capability, audit retention/holds, event
   stream/outbox state and storage evidence/reference reachability.
2. A customer requests historical export from shared DB. Authorized process uses
   scoped extraction/protected reader, never direct physical restore assumed to
   contain only that tenant's rows.
3. Tenant moves to new database/region. Copy/delta/drain/verify/cutover sequence
   uses immutable migration epoch; resolver routes only one active writer target.
4. Disaster recovery restores shared DB. Data plane remains fenced until RLS,
   registry compatibility, credential rotation, audit/event/outbox recovery and
   retention states are verified; then traffic enables by controlled cohort.
5. Storage objects tied to restored tenant DB are verified by opaque refs/digests/
   versions; missing objects are marked truthful/unavailable rather than recreated.
6. An event projection is rebuilt after restore with system actor/scoped checkpoint
   and idempotent projection semantics; no global tenant replay occurs by accident.

### Edge use cases

1. Restored database is older than a later tenant deletion/legal hold. Control
   plane reconciles state before use; it does not reactivate deleted data silently.
2. Backup contains credentials or DSN mappings. Restore run rotates/replaces them
   under secret procedure and never logs/reuses stale credentials automatically.
3. Outbox message had published before disaster but acknowledgement lost. Recovery
   resumes at-least-once with consumer idempotency; no exactly-once claim from DB.
4. Shared DB point-in-time restore rolls all tenants back. Incident plan acknowledges
   blast radius, coordinates registry/audit lifecycle and treats post-restore writes
   as a new controlled recovery epoch rather than a transparent event.
5. Tenant move runs twice after runner crash. Epoch/lease/source-target markers
   avoid two active writers and give operator reconciliation state.
6. Restore wants to load a production tenant into developer environment. Policy
   requires safe approved sanitized/export process; raw restore is not a default.
7. Archive/object storage uses different retention than database backup. Validation
   distinguishes missing evidence due lawful lifecycle from unexpected data loss.

### Invariants and acceptance evidence

- backup/restore/move runbooks are specific for shared and database topology;
- each restore/move is authorized, scoped, audited, versioned/epoch-fenced and rehearsed;
- recovery tests include audit/event/outbox/storage/i18n config compatibility checks;
- no restored target becomes active writer until resolver/control-plane cutover approval;
- deleted/suspended/held/migrating lifecycle states are reconciled before data access;
- DR evidence states exact RPO/RTO assumptions and no misleading tenant-isolation claim;
- backup keys/credentials/object refs are protected from generic telemetry/log paths.

### First implementation slice

Write a database-per-tenant isolated restore rehearsal and a shared-DB "not
supported without extraction" statement. Test epoch fence before designing online
tenant moves or self-service recovery interfaces.

---

## T-18 — resource quotas, fairness and connection-pool economics

**Decision.** Tenancy introduces explicit resource/fairness controls instead of
assuming routing alone prevents noisy neighbors. Shared DB needs query/index/lock/
rate limits; db-per-tenant needs pool/cache/client lifecycle budgets and can still
share host/cluster resources. Quotas are authorization/operational policy, not
tenant identifiers emitted as metric labels.

### Top-level declarative DX

```go
permit, err := budgets.Acquire(ctx, scope, tenancy.OperationWrite)
if err != nil { return err }
defer permit.Release()

return scopedRepo.Update(ctx, command)
```

The resource policy receives verified scope and stable operation class. It does
not ask application code to calculate a rate-limit key from tenant display name.

### Happy use cases

1. Shared database allows normal writes while a tenant's expensive export is
   throttled/queued by declared admin budget. Other tenants retain service capacity.
2. Db-per-tenant resolver has maximum live pools, idle timeout and safe eviction;
   low-traffic tenants do not create permanent client/pool per request.
3. A large tenant is assigned approved capacity class by control plane. Metrics
   expose aggregated class/outcome, not tenant identity, and policy remains auditable.
4. Projection/rebuild/export jobs use per-scope and global concurrency budgets,
   so one cohort cannot exhaust database/storage/worker resources.
5. Connection checkout resets row-security/session state before and after tenant use.
6. An overloaded tenant receives typed retry-after/deferred outcome without leaking
   other tenants' activity or silently rerouting data to another database.

### Edge use cases

1. Tenant opens many concurrent requests to force one pool per ephemeral scope.
   Resolver cache/pool key/quotas canonicalize scope and bound allocations/eviction.
2. Budget service unavailable. Operation follows risk-class fail-closed/degrade
   policy; no code bypasses quota merely because optional observer cannot answer.
3. One shared DB long transaction blocks outbox/projection/audit for others. Query
   timeout/transaction limit/operator alerts diagnose shared blast radius.
4. Db-per-tenant dedicated database is healthy but common storage/queue is full.
   Capacity view distinguishes topology component and returns truthful partial state.
5. Priority class comes from untrusted request plan header. Control plane assigns
   class after authorization; request may express intent but never authority.
6. A malicious query intentionally creates many distinct error labels. Signals use
   bounded operation/result classes and rate limits rather than raw tenant/query text.

### Invariants and acceptance evidence

- resource budget API consumes verified scope and bounded operation/class only;
- live pool/connection/client count stays under configured global/per-class limits;
- checkout/checkin tests prove RLS/session/role/reset and wrong-tenant isolation;
- load tests cover shared noisy query, many sparse db tenants, backlog and fairness;
- overload/client responses/errors/telemetry are bounded and avoid cross-tenant details;
- quota/pool configuration changes are control-plane audited and rollbackable;
- capacity plan states shared dependencies and no false full-isolation promise.

### First implementation slice

Set conservative pool/cache upper bounds and transactional/query timeouts with a
load test. Add sophisticated per-tenant quotas only after a control-plane class
model, fairness objective and abuse case are agreed.

---

## T-19 — tenancy conformance suite and adapter admission

**Decision.** Every row/database resolver, repository, event, storage, audit and
background-job integration publishes a conformance profile. Tests verify concrete
fail-closed scope behavior under real PostgreSQL/filesystem/S3-like targets and
faults. A package cannot claim "multi-tenant compatible" merely because it has a
tenant field or accepts `context.Context`.

### Top-level declarative DX

```text
tenancy conformance --profile shared-row-postgres
tenancy conformance --profile database-postgres
tenancy conformance --component eventpg
tenancy conformance --component storage-s3
```

Results state supported operations, exact topology and known exclusions. A profile
can be partial, but unsupported scope paths must remain unadvertised/fail closed.

### Happy use cases

1. Same service/repository test corpus creates identical aggregate ID in Tenant A
   and Tenant B, proving read/write/count/exist/list/relation isolation.
2. Shared profile runs application predicate and database RLS/connection-reset
   negative tests against real PostgreSQL; database profile runs mapping/pool/epoch
   tests against two real databases.
3. Storage profile runs scoped fs and S3-compatible put/get/sign/version/orphan
   tests. Event profile runs append/load/snapshot/outbox/projection scope tests.
4. Audit profile runs transaction/history/viewer/retention/tenant join isolation.
5. Job profile proves durable envelope restores verified scope/service actor and
   refuses raw carrier/missing/expired/deleted tenant work.
6. Migration profile tests old/new schema/capability and one failed cohort outcome.

### Edge use cases

1. Request context missing/forged/malformed/stale/suspended scope. Every component
   performs zero tenant side effects and returns classified safe failure.
2. IDs collide across tenants. Queries/preloads/joins/upserts/bulk paths cannot
   return/update foreign resource despite equal primary/business identity.
3. Pooled connection/session/client carries prior tenant setting/credential/cache.
   Next tenant operation is isolated after checkout/reset/epoch validation.
4. Resolver/migration mapping changes while request/job/transaction is in flight.
   Profile documents whether operation finishes/fails/retries and proves no misroute.
5. Multi-tenant admin report suffers one unavailable database. Result marks partial
   rather than showing a normal list with silently missing tenant data.
6. Trace/log/error/export uses fake tenant/DSN/secret canary. Scanner proves absence.
7. Restore/move/retry/outbox duplicate uses stale scope. Fences/idempotency produce
   exact durable state and not a second foreign write.

### Invariants and acceptance evidence

- common core scenario IDs run unchanged for shared and db-per-tenant profiles;
- each adapter lists query/write/background/bulk/restore operations covered/excluded;
- real integration setup versions/configuration are captured in test evidence;
- negative suite asserts zero SQL/object/event/audit effects for invalid scope;
- property/race/load/chaos tests include ID collision, pool reuse, mapping epoch and
  network/timeout failures; privacy scanner runs in each profile;
- release gate prevents new adapter from marketing tenancy support without profile;
- performance thresholds cover isolation correctness and do not erase edge cases.

### First implementation slice

Create two tenant scopes with identical entity IDs and an invalid-scope fixture.
Make Get/List/Count/Update/Delete plus pool reset tests pass before adding rich
filters, bulk, events, storage or any automatic resolver cache.

---

## T-20 — security threat model and defence-in-depth composition

**Decision.** Tenant isolation is a composed claim: verified scope, application
query/write policy, database constraints/RLS where applicable, resolver binding,
least-privilege credentials, pool reset, control-plane lifecycle and operational
tests each cover different failures. No single `tenant_id` column, middleware or
RLS policy is described as a complete defence without its assumptions.

### Top-level declarative DX

```text
trusted carrier -> identity verification -> immutable tenant scope
  -> topology resolver/binding -> repository/event/storage/audit adapter
  -> database role/RLS/constraints or bound tenant database
  -> conformance and incident detection
```

Every arrow has named inputs/outputs/failure mode. A developer can see where a
transport/auth library ends and where the tenant satellite begins.

### Happy use cases

1. Web host verifies signed identity and maps requested tenant membership before
   `tenancy.WithScope`; repository adds predicate; PostgreSQL RLS backs it up.
2. Service-to-service call uses verified workload principal and allowed tenant
   delegation. Event/job payload contains opaque scope reference but cannot grant it.
3. Database-per-tenant resolver maps scope to protected binding; role credentials
   are restricted to intended DB and connection checkout validates epoch/identity.
4. A code review adds a repository query. Static/integration checks require scope
   composition and run foreign-ID negative test before merge.
5. Incident responder investigates suspected leak. Protected audit/log/metric
   information identifies safe component/outcome while direct tenant data remains
   access-controlled and evidence is preserved.
6. A threat model explicitly accepts limited shared-infrastructure noisy-neighbor
   risk while using database topology for stronger data routing isolation.

### Edge use cases

1. User is authenticated but requests a tenant not in membership. Scope resolver
   denies before query; RLS alone is not asked to infer product membership rules.
2. JWT claim is valid but stale after tenant access revoked. Identity/control-plane
   freshness/expiry policy defines revalidation/caching and fails closed where needed.
3. SQL injection/bypass uses application database role. RLS/least privilege and
   prepared/query policy reduce scope; an admin schema-owner credential is absent.
4. Internal worker is trusted network-wise but has malformed tenant envelope.
   It still rejects/mints no scope; network location is not a tenancy capability.
5. Backup/analytics/BI account can read shared DB. Separate data governance and
   views/exports are required; application tenant filter does not secure that role.
6. A platform superuser has legitimate broad access. Break-glass/control-plane
   workflow makes it explicit, time/purpose-bound and audited—not invisible bypass.
7. New package imports database driver and skips decorator. Dependency/coverage
   gate detects unscoped access path or architecture rejects it at review.

### Threat-to-control worksheet

| Threat | Primary prevention | Defence/detection |
|---|---|---|
| forged tenant carrier | host identity/membership verification | resolver negative tests/audit attempt policy |
| missing query predicate | scoped repository contract | RLS/composite constraints/query matrix |
| wrong database mapping | control-plane binding/epoch | pool validation/fence/misroute drill |
| pooled session leakage | checkout/reset discipline | prior-tenant connection test |
| tenant ID collision | composite scope identities | same-ID property tests |
| stale lifecycle/cache | binding TTL/version/recheck | suspend/delete/rotation race tests |
| broad admin abuse | named cohort/break-glass authority | access audit/expiry review |
| export/telemetry leak | protected reader/schema registry | canary scanner/role tests |
| restore/migration reactivation | epoch/capability lifecycle fence | recovery rehearsal |
| async raw payload route | signed/verified work scope | job/event injection tests |

### Invariants and acceptance evidence

- threat model maps every trust boundary to prevention, assumptions and detection;
- web/service/job/admin/restore paths have distinct scope creation authorization;
- runtime roles/credentials cannot circumvent advertised data-plane isolation silently;
- architecture tests/inventory identify direct driver/query paths and their tenant profile;
- red-team-style forgery/pool/misroute/foreign-ID/export scenarios pass in CI/staging;
- incident runbook distinguishes containment of shared DB, resolver and control-plane faults;
- claims in product docs state topology/assumptions rather than vague "tenant secure" language.

### First implementation slice

Write the threat worksheet for one inbound host + Postgres repository and test
forged/missing/foreign scopes. Add RLS only after application predicate semantics
and connection session setup/reset behavior are independently proven.

---

## T-21 — complete query/write matrix and data-model ownership

**Decision.** A resource's tenant ownership and every access shape are declared
once, then adapters prove composition. Tenant predicate added only to `List` is
not sufficient: `Get`, exists, count, aggregate, relation preload, joins, sort,
filter, cursor, insert, update, upsert, delete, restore, bulk, raw command and
database constraint path each need a defined tenant story.

### Top-level declarative DX

```go
orders := tenancy.DecorateRepo(baseOrders, tenancy.OwnedBy[Order]("tenant_ref"))

order, err := orders.Get(ctx, id)
page, err := orders.List(ctx, query)
err = orders.Update(ctx, command)
```

Scope is read from verified context/binding and composed internally. The public
methods do not accept a caller-controlled owner field or untyped `tenantID` filter.

### Happy use cases

1. Create derives/validates tenant ownership from scope and ignores/rejects a
   mismatched tenant field in input model/command.
2. Get/List/Count/Exists/aggregate all apply same scope; cursor encodes query
   binding so a cursor cannot be replayed in another tenant.
3. Order preload line-items/products applies child ownership/relation policy; a
   relation cannot jump to same-ID row in another tenant.
4. Update/delete add scope to victim condition; zero affected row follows safe
   not-found/forbidden/conflict policy without leaking foreign existence.
5. Upsert uniqueness/key constraints include tenant scope where semantic identity
   is tenant-local; global resources are explicitly modeled/repository-separated.
6. Named bulk operation has deterministic scoped victim selection and audit summary
   or individual record semantics, with resource/cost bounds.

### Edge use cases

1. A child is globally shared (e.g. platform product catalogue). Relationship
   model names it tenant-neutral/read-only and avoids accidental `tenant_ref` fake;
   access policy is separate from tenant-owned records.
2. A join starts from tenant-owned row then joins global table then tenant-owned
   row. Query builder/constraints enforce expected scope on every owned branch.
3. Database cascade deletion removes children. Ownership/audit policy says which
   tenant constraints and evidence items apply; no silent cross-scope cascade.
4. Update changes `tenant_ref`. Normal methods reject; only named migration action
   with source/destination authority, constraints and audit can rehome data.
5. User-defined filter includes tenant ID or raw SQL. Public grammar excludes it;
   scoped query composes only reviewed fields and driver parameterization.
6. ORM auto-preload/cache/identity map returns entity loaded in another scope.
   Adapter scopes cache key/session lifetime or refuses unsafe shared cache behavior.
7. Aggregate query groups by tenant internally for control-plane report. Ordinary
   tenant endpoint sees only its scope; privileged report has cohort capability.

### Query matrix to verify per resource

| Shape | Required scope evidence | Negative proof |
|---|---|---|
| create/import | derived owner or control-plane mapping | mismatched owner cannot persist |
| get/by business key | tenant predicate/composite key/bound DB | same ID/key foreign row absent |
| list/cursor/filter/sort | forced scope + cursor binding | cursor/filter cannot escape scope |
| count/exists/aggregate | same predicate/RLS | no foreign count inference |
| preload/join/relation | parent/child/global ownership model | same-ID relation leak fails |
| update/upsert/delete/restore | scoped victim predicate/constraint | foreign ID affects zero rows |
| bulk | scoped deterministic victim set | race/partial/limit behavior exact |
| raw/custom SQL | named tenant-aware contract | strict guard blocks unknown raw path |
| cache/ORM session | scope/epoch included or isolated lifetime | reused session leaks no entity |
| export/history | reader scope/purpose/role | no foreign data/count/filter leak |

### Invariants and acceptance evidence

- each model/relation is classified tenant-owned, tenant-neutral or control-plane owned;
- query matrix is generated/reviewed alongside repository changes and exercised in tests;
- database schema has composite uniqueness/foreign keys/checks/RLS as appropriate;
- every mutation verifies immutable ownership and reports zero-row outcome safely;
- cache/preload/ORM/raw/bulk paths are in support inventory with explicit exclusions;
- same-ID/cross-key/race/cursor/property tests run for all supported shapes;
- global resources cannot be casually joined into a tenant-owned write without policy.

### First implementation slice

Start one simple tenant-owned model with a composite unique key, Get/List/Update/
Delete matrix and same-ID fixtures. Defer global/shared relation semantics, ORM
preload and bulk until individual ownership guarantees are visible and tested.

---

## T-22 — tenant lifecycle state machine and irreversible transitions

**Decision.** A tenant is a control-plane lifecycle subject with immutable internal
reference and versioned transitions, not a row that becomes usable when a display
name exists. Provision, activate, suspend, migrate, delete, archive and restore
each state has preconditions, allowed data-plane behavior, audit, credential,
schema, storage, event and retention implications.

### Top-level declarative DX

```text
requested -> provisioning -> ready -> active -> suspended -> deleting -> deleted
                              \-> migrating -> active (new epoch)
legal hold / incident / restore are governed substates or flags, not free text
```

The actual state machine is an ADR, but resolver returns typed state/epoch and
cannot quietly treat `provisioning`, `deleting` or unknown as active.

### Happy use cases

1. Provision creates registry entry, credentials/binding, database/schema or row
   policy config, storage namespace, audit/event readiness and only then activates.
2. Suspend immediately blocks normal new scope acquisition/writes/downloads while
   preserving authorized support/retention/incident workflows under explicit policy.
3. Migration state drains/fences writes, records source/target/epoch/progress,
   validates all satellites, then atomically changes active binding/capability.
4. Delete transition records legal/retention/hold/backup/object/event/audit plan;
   data destruction occurs only through governed asynchronous stages and final state.
5. Restore opens isolated target, reconciles current lifecycle/epoch/authorization,
   then becomes a new active generation only after approved cutover.
6. Tenant rename/plan/default locale change updates mutable presentation/configuration
   fields without changing stable tenant ref used in ownership, audit or storage keys.

### Edge use cases

1. Provision halfway succeeds: DB exists but storage/audit schema fails. State
   remains provisioning/failed; resolver issues no active data-plane binding and
   cleanup/retry evidence is recorded.
2. Delete requested while legal hold appears. State moves to held/pending; worker
   cannot destroy database/bucket/keys/backups just because UI says delete.
3. A long-running request starts active then tenant moves/suspends. Epoch/lifecycle
   contract defines finish/fence behavior and exact audit/projection/outbox result.
4. Operator directly drops per-tenant DB before control-plane transition. Reconciler
   detects missing binding target and prevents accidental recreation/foreign mapping.
5. Tenant ID is reused after deletion. Default policy forbids reuse; if business
   requires new customer with same display label it receives a new opaque identity.
6. Restore version contains older localized catalogue/policy. Activation requires
   compatible config manifest or safe upgrade path, not automatic live attachment.
7. Control-plane state transition itself fails audit. Critical lifecycle policy
   decides fail-closed/reconciliation rather than claiming a deletion/move occurred.

### Invariants and acceptance evidence

- lifecycle transition table names caller authority, preconditions, side effects and rollback/compensation;
- resolver exposes only active/approved exception bindings and always includes epoch/version;
- satellite readiness checks include db/schema, storage, audit, event/outbox, i18n config and credentials;
- state/race/retry/partial provision/delete/hold/move/restore tests are durable/idempotent;
- final deletion reports each data copy/retention exception/truthful completion state;
- display-name/locale/plan updates never alter security ownership identity;
- control-plane logs/telemetry stay bounded and protected like other sensitive metadata.

### First implementation slice

Implement `provisioning`, `active`, `suspended`, `deleted` plus opaque ref/epoch.
Block activation on basic schema/audit readiness. Treat migration/restore/delete
execution as manual governed procedures until rehearsals demonstrate safety.

---

## T-23 — release ownership, implementation order and adoption stop rules

**Decision.** Tenancy must be adopted in a thin vertical slice before broad API
surface. The first release proves one trusted inbound host, one tenant-owned model,
one shared PostgreSQL profile, invalid-scope fail-closed behavior and observability
privacy. Database-per-tenant is a second adapter/release profile, not a config flag
added before control plane/pool/lifecycle/migration evidence.

### Recommended sequence

1. Define opaque verified scope, lifecycle ref/epoch and a no-global-current-tenant rule.
2. Bind scope to one repository/query matrix and build same-ID/missing/foreign tests.
3. Add shared PostgreSQL predicate/composite constraints; add RLS after session reset proof.
4. Wire audit, storage, event and job scopes only through their explicit satellites.
5. Add control-plane binding, lifecycle/readiness and compatibility fence.
6. Build db-per-tenant resolver/pool profile on two real PostgreSQL databases.
7. Rehearse migration/restore/suspend/credential rotation before expanding cohort.
8. Add cross-tenant reports/batches only through capability/grant/fan-out design.

### Stop rules

- stop if any data-plane entry point can obtain a nil/default/raw tenant scope;
- stop if a repository shape/preload/cache/raw SQL path has no matrix evidence;
- stop if an adapter needs tenant/database identity in telemetry to be debuggable;
- stop if db-per-tenant pool/cache mapping cannot safely handle expiry/rotation/eviction;
- stop if migration rollout/rollback needs an unbounded fleet action or schema-owner app role;
- stop if tenant deletion/hold/backup/restore is described only by a destructive script;
- stop if cross-tenant request returns partial data without telling caller/operator;
- stop if any framework "convenience" imports an identity/secret/web/driver dependency into root.

### Definition of done for an adapter profile

An adapter profile is ready only when its scope trust boundary, query matrix,
topology binding, tenant lifecycle behavior, role/credential/pool handling,
event/storage/audit/i18n integration, privacy-safe telemetry, migration/restore
compatibility, fault/load/conformance results and operator runbook are all present.

The phrase "multi-tenant ready" must additionally name which of shared-row and
database-per-tenant profiles passed, their intentional non-goals and their current
control-plane/operational assumptions. That precision is the framework DX: teams
can adopt a small secure path without accidentally promising a fleet platform.

---

## Multitenancy acceptance catalogue

Run this catalogue for both profiles whenever they are advertised. `row` means a
real shared PostgreSQL schema with the selected RLS/role configuration. `database`
means at least two independently selected PostgreSQL databases plus a control-plane
fake/real resolver. A scenario names topology-specific expectation where needed;
all other behavior must be equivalent to application callers.

### TC-01 through TC-10 — trusted scope acquisition

**TC-01: valid member.** Verified principal requests a tenant it is allowed to use.

Assert exactly one opaque active scope is created and propagated to service/repo.

Assert raw host/header/JWT claim does not become a tenant DB/key/telemetry value.

**TC-02: absent scope.** Invoke a tenant-owned endpoint/job without scope.

Assert zero SQL/object/event/audit work and safe classified failure.

Assert code cannot manufacture a default/global tenant through public helper.

**TC-03: forged carrier.** Send raw tenant header/body/event value of another tenant.

Assert host/resolver rejects before adapter acquisition and no foreign existence leak.

Assert audit/trace/error contains bounded outcome only, not raw supplied value.

**TC-04: non-member.** Verified user asks for a tenant outside membership.

Assert denial before query and no row/database discovery by timing/message distinction.

Assert optional attempt evidence follows separate safe policy if enabled.

**TC-05: suspended tenant.** Valid scope references suspended lifecycle state.

Assert normal data plane fails closed before datasource/storage/event handling.

Assert protected incident/retention exception requires named separate authority.

**TC-06: deleted tenant.** Reuse a stale signed request/job after deletion.

Assert resolver refuses; no database is auto-created/rebound from display identifier.

Assert result identifies lifecycle class without exposing historic tenant data.

**TC-07: stale membership.** Revoke membership while a token/cache remains usable.

Assert documented expiry/revalidation policy produces correct eventual/fail-closed state.

Assert test captures the exact accepted staleness window rather than assuming none.

**TC-08: service delegation.** A workload principal receives allowed tenant delegation.

Assert typed service actor/scope lineage is created and arbitrary payload cannot extend it.

Assert audit distinguishes service actor from an impersonated human actor.

**TC-09: locale carrier.** Provide locale/tenant-looking host combination.

Assert tenant verification and BCP-47 resolution remain separate typed decisions.

Assert locale cannot choose a tenant catalogue/database/storage namespace.

**TC-10: context cancellation.** Cancel request during scope/resolver acquisition.

Assert no leaked pool/client/transaction and no partial tenant side effect.

Assert errors remain safe and background work does not inherit canceled raw carrier.

### TC-11 through TC-20 — row isolation query matrix

**TC-11: equal primary ID.** Create same logical/primary ID in Tenant A and B.

Assert Get returns only current scope record in row and database profiles.

Assert update/delete cannot select the other record despite identical ID.

**TC-12: list/count.** Seed different record counts per tenant.

Assert List/Count/Exists/aggregate see exactly local rows; no global count inference.

Assert pagination cursor is bound and cannot cross-scope replay.

**TC-13: business-key collision.** Reuse natural key in two tenants.

Assert tenant-local uniqueness works and upsert selects local scope only.

Assert global-key resource follows separately declared global ownership policy.

**TC-14: relation preload.** Parent/child share ID patterns across tenants.

Assert preload/join loads only same-tenant child and foreign relation is unavailable.

Assert ORM/cache adapter profile documents/blocks unsafe preload behavior.

**TC-15: update victim.** Submit foreign subject ID under Tenant A.

Assert zero data/audit event change and safe not-found/forbidden outcome.

Assert timing/errors do not provide detailed foreign state.

**TC-16: bulk selection.** Execute named bulk operation under one scope.

Assert exact victim set is tenant-bound, bounded and audit outcome matches written rows.

Assert raw filter/ID list does not escape to default logs/traces.

**TC-17: raw/custom query.** Attempt unregistered raw SQL on tenant table.

Assert strict guard/architecture policy blocks or requires named scoped command.

Assert RLS/least privilege is tested as a second control where configured.

**TC-18: database cascade.** Delete parent with scoped child rows.

Assert cascade affects only declared same-scope children and audit semantics are exact.

Assert no unexpected cross-scope FK/cascade exists in schema fixture.

**TC-19: ownership change.** Try update setting different `tenant_ref`.

Assert ordinary path rejects and immutable ownership is enforced by code/constraint.

Assert named migration action is required for intentional rehome.

**TC-20: query cache.** Reuse query/entity cache/session across Tenant A then B.

Assert cache key/session scope prevents returned foreign object/count/relationship.

Assert cache eviction/epoch behavior remains correct after suspension/move.

### TC-21 through TC-30 — datasource, pool and control plane

**TC-21: database resolve.** Resolve A then B in database profile.

Assert each handle executes only against its bound test database and release clears state.

Assert application cannot inspect raw DSN/database name through handle/error.

**TC-22: stale binding.** Change mapping epoch before checkout/use.

Assert stale cached binding fails/re-resolves according to contract; no wrong write.

Assert telemetry emits safe resolver outcome, not mapping identity.

**TC-23: connection reset.** Use Tenant A RLS/session then checkout for B.

Assert role/settings/search path/temp state is reset/validated before B SQL.

Assert direct connection test detects any prior tenant residual state.

**TC-24: credential rotate.** Rotate a database credential/binding generation.

Assert old pools drain/stop new work and new checkout uses valid binding safely.

Assert in-flight unknown commit/retry does not duplicate cross-database mutation.

**TC-25: resolver outage.** Make registry unavailable.

Assert documented fail-closed/degraded behavior for each topology and operation class.

Assert no cache turns unknown/suspended/deleted tenant into indefinitely active scope.

**TC-26: pool exhaustion.** Request many sparse tenant scopes in database profile.

Assert max live pools/clients/queue and eviction constraints hold; errors are bounded.

Assert one abusive tenant cannot create unlimited process resource use.

**TC-27: duplicate provision.** Submit provisioning twice/concurrently.

Assert idempotent lifecycle record/resource creation and no two active bindings.

Assert partial state is visible/recoverable without raw infrastructure identifiers.

**TC-28: suspend race.** Suspend tenant during active request/transaction.

Assert documented finish/fence outcome and no later new operation slips through.

Assert audit/control-plane evidence reflect lifecycle/epoch truthfully.

**TC-29: map wrong DB.** Intentionally map Tenant A ref to B database.

Assert binding/identity fence detects it before foreign application/audit/event data changes.

Assert incident diagnostic makes component/class visible without leaking IDs.

**TC-30: display rename.** Rename tenant label/default locale/plan.

Assert stable opaque ownership/binding/cache keys remain correct and no data move occurs.

Assert old audit/event/storage identities retain stable ref, presentation updates only.

### TC-31 through TC-40 — satellites and asynchronous work

**TC-31: audit transaction.** Mutate one resource under each topology profile.

Assert data and tenant-bound audit revision/item commit or roll back together as promised.

Assert reader in other tenant cannot join/guess audit subjects or revisions.

**TC-32: event append.** Append equal stream IDs under A and B.

Assert append/load/snapshot/expected version/outbox remain local to each scope.

Assert event payload cannot cause resolver/database route choice.

**TC-33: projection job.** Deliver per-tenant event to worker.

Assert worker restores verified scope/service actor before projection/audit storage use.

Assert missing/forged/deleted scope causes zero projection side effect.

**TC-34: outbox duplicate.** Crash/duplicate event delivery for one tenant.

Assert idempotent delivery/checkpoint is tenant-bound and does not affect same ID in B.

Assert completion telemetry avoids stream/tenant identifiers.

**TC-35: storage put/get.** Put same logical object key under A/B scopes.

Assert each gets isolated opaque object reference and no cross read/list/sign URL.

Assert filesystem/S3 adapter rejects raw prefix/path/URL routing input.

**TC-36: storage failure.** Fail promotion after DB workflow begins.

Assert orphan/dangling reconciliation is scoped and cannot inspect/delete foreign object.

Assert audit reference/export lacks signed URL/bucket/credential.

**TC-37: i18n selection.** Tenant A default and B default differ; user override exists.

Assert resolver priority/fallback and stable persisted event/audit keys in both tenants.

Assert locale/catalogue cannot alter data topology/security scope.

**TC-38: telemetry privacy.** Use fake tenant/DB/DSN/object canary values.

Assert none appear in spans, logs, metrics, baggage, exporter errors or dashboards.

Assert bounded topology/result signals still let on-call identify faulty component.

**TC-39: audit history export.** Request authorized history export for A.

Assert scope/purpose/role/staging/TTL and fields apply; B content/count is absent.

Assert cross-tenant export requires separate cohort grant and completeness output.

**TC-40: retention/hold.** Place audit/object legal hold on A then request deletion.

Assert A lifecycle blocks correct copies; B lifecycle/objects remain unchanged.

Assert shared DB/bucket procedure cannot accidentally purge broad neighbor range.

### TC-41 through TC-50 — release, recovery and privileged operations

**TC-41: shared migration.** Expand/cutover schema in shared DB with live scopes.

Assert compatible reader/writer behavior, query/RLS continuity and rollback guard.

Assert no tenant-specific diagnostic leaks into migration signals.

**TC-42: partial database cohort.** Migrate A database, fail B database deliberately.

Assert resolver/app capability fences report A ready/B failed accurately without false fleet green.

Assert retry/rollback target remains scoped/idempotent/audited.

**TC-43: backup restore.** Restore tenant DB to isolation target.

Assert schema/epoch/audit/event/outbox/storage/hold state checks before activation.

Assert target cannot receive live traffic or be mistaken for current active binding.

**TC-44: shared recovery.** Restore shared DB point-in-time scenario.

Assert plan acknowledges all-tenant blast radius, rechecks RLS/registry/credentials/lifecycle.

Assert post-recovery writes use controlled epoch and operators record reconciliation.

**TC-45: tenant move.** Copy/drain/cutover from source DB to target DB.

Assert exactly one writer binding/epoch; pending jobs/outbox/projections have defined outcome.

Assert retry after runner crash cannot create two active targets.

**TC-46: authorized cross-tenant report.** Issue bounded grant for A+B.

Assert callback gets one scope at a time; results show per-tenant success/failure/unknown.

Assert normal repository still refuses nil/multiple scope.

**TC-47: grant expiry.** Let cohort grant expire during execution.

Assert no new scopes open; existing result is precise and resume requires reauthorization.

Assert audit contains purpose/authority/outcome without full cohort identity telemetry.

**TC-48: break glass.** Use exceptional control-plane access.

Assert time/purpose/approver/least privilege, protected access audit and post-use review.

Assert ordinary app principal cannot attain equivalent raw database capability.

**TC-49: destructive deletion.** Run governed tenant deprovision/purge dry run.

Assert lifecycle/hold/backup/archive/storage/event/audit prerequisites and counts are scoped.

Assert code stops on uncertainty and can reconcile restored copies truthfully.

**TC-50: architecture inventory.** Add new package with direct driver/config import.

Assert dependency/architecture check rejects unprofiled routing/driver/secret dependency.

Assert roadmap/index/manifests list only actually supported tenancy profiles.

---

## Operator runbook and dashboard handoff

### Safe aggregate dashboards

| View | Bounded indicators | Question answered |
|---|---|---|
| resolver | success/failure class, cache age bucket, topology, lifecycle state counts | Are scopes resolving safely? |
| pools | active/idle/wait/error counts by safe capacity class/topology | Are connections bounded/fair? |
| data plane | request/DB latency/error/conflict classes by topology | Is shared or database path degraded? |
| migration | cohort state counts, lag/error class, compatible/blocked totals | Can tenants safely use release? |
| lifecycle | provisioning/suspended/migrating/deleting state counts | Is control plane progressing safely? |
| satellites | job/outbox/audit/storage scoped outcome totals | Is one integration failing without identity leak? |
| recovery | restore/move/fence state counts | Is a recovery target safely isolated/cut over? |
| access | cross-tenant/break-glass grant result counts | Are exceptional capabilities governed? |

### On-call rules

1. Never diagnose a tenant issue by adding tenant/database/DSN labels to telemetry.
   Use protected control-plane/audit workflow after bounded signal identifies class.
2. Resolver unknown, stale mapping, deleted/suspended or incompatible schema is
   fail-closed; do not bypass it with direct SQL/connection strings to restore traffic.
3. Suspected cross-tenant leak means contain affected route/pool/cache, preserve
   evidence, rotate credentials as necessary and inspect both app scope and DB/RLS.
4. Pool exhaustion is not solved by permanently lifting limits; find cache churn,
   capacity class, long transaction/worker behavior and shared dependency saturation.
5. Migration failure is per-cohort/per-binding state. Do not mark fleet complete or
   roll back unrelated data blindly; apply compatibility fence/retry/runbook.
6. Tenant move/restore unknown state requires epoch/target/progress reconciliation
   before traffic. Two active writers is higher risk than temporary unavailability.
7. Legal hold/delete conflict preserves data/evidence and escalates governance;
   never drop bucket/database/key because customer lifecycle screen changed.
8. Cross-tenant report incident returns explicit partial/unknown result; never
   substitute an empty list or hidden omission as evidence of zero activity.

### Required rehearsal evidence

- tenant A/B identical ID test for every repository/satellite profile;
- missing/forged/suspended/deleted scope test proves zero side effects;
- connection RLS/session reset and database mapping/epoch mismatch drill;
- credential rotation/cache expiry/pool eviction/load/fairness test;
- one shared migration plus one partially failed db-per-tenant cohort rehearsal;
- restore/move/fence/outbox/projection/storage/audit reconciliation drill;
- privacy canary scan across traces/logs/metrics/errors/exports/control-plane reports;
- cross-tenant grant expiry/partial result and break-glass access review rehearsal.

---

## Release compatibility matrix

Tenancy is compatible only when every participating satellite understands the
tenant binding's topology, epoch and capability range. A green application deploy
does not prove a tenant is ready if its database, audit schema, event projections,
storage mapping or catalogue configuration is at an incompatible stage.

| Component | Shared DB rule | Database-per-tenant rule | Release guard |
|---|---|---|---|
| root service | scope required before repo | scope required before resolver | no nil/default scope APIs |
| Postgres CRUD | current shared schema/RLS policy | binding schema range compatible | fence write/query shape |
| audit | common revision schema/policy | local audit schema/version ready | no claimed atomic audit otherwise |
| event store | tenant composite stream schema | local stream/snapshot/outbox schema | block incompatible append/replay |
| storage | active namespace policy/version | binding account/bucket/key policy ready | no raw fallback namespace |
| i18n | platform/tenant manifest compatible | local/cache manifest compatible | safe fallback/feature fence |
| jobs/outbox | scoped envelope version accepted | worker resolves current binding/epoch | reject stale/forged work |
| control plane | lifecycle and shared policy ready | lifecycle/binding/schema/credential ready | resolver active state only |
| migration | online global migration phase | per-binding cohort phase/lock | app capability range |
| recovery | global DR epoch/reconciliation | restored target isolation/cutover epoch | no active traffic precheck |

### Expand/cutover/rollback example

**Expand.** Add a new tenant-scoped audit/event column or storage policy field as
nullable/optional in shared schema and tenant baseline. Readers accept absent/new
state; resolver reports compatible old/new capability range.

**Deploy compatible readers.** Roll out application/workers that can read both
forms and keep safe behavior for older tenant databases/catalogues. Dashboard
shows cohort/version counts but no tenant identifiers.

**Migrate cohort.** Apply bounded idempotent tenant DB migration with lock/epoch
checks. Shared DB migration uses online-safe plan. Verify audit/event/storage/i18n
readiness before making the new writer feature reachable for that binding.

**Cut over writes.** Resolver/application only enables new behavior for compatible
cohorts. Old tenants receive an explicit safe feature-unavailable/queued result;
they are never routed to a database whose schema cannot represent their write.

**Retire.** After all retained jobs/cursors/readers/archives and rollback horizon
are compatible, remove legacy path through a separately reviewed release.

**Rollback.** If an app rollback would not read current tenant state, deployment
guard blocks it or runs safe read-only/forward-fix procedure. Never down-migrate
tenant histories blindly just to fit old application binaries.

### Compatibility failure cases

1. Job was queued with old scope-envelope version. Worker validates/migrates only
   under documented compatible rule; otherwise parks it as safely unsupported.
2. Tenant DB is two migrations behind due outage. Resolver reports unavailable/
   compatible-read-only state, not a generic connection error inviting direct bypass.
3. Per-tenant i18n override references newer message argument schema. Presentation
   falls back/denies safely; data ownership/routing stays independent.
4. Storage namespace migration incomplete when database mapping cuts over. Binding
   remains migrating/fenced until opaque refs/object availability checks succeed.
5. Audit row/event codec update is deployed in only some tenants. Authorized reader
   handles known historical range, returning typed unknown when not supported.
6. A tenant was restored from old backup then receives current job. Epoch/binding
   validation prevents job from applying to wrong generation, preserving retry state.
7. External consumer is behind. Its outbox contract/field version controls delivery
   separately; tenancy cohort readiness cannot silently leak a newer tenant export.

## Decision register for the first tenancy release

| ID | Decision | Evidence needed |
|---|---|---|
| TN-01 | trusted carriers/principals and scope creation | forged/non-member/stale test |
| TN-02 | opaque tenant ref, lifecycle and binding epoch | ID reuse/suspend/move tests |
| TN-03 | first topology profile and its stated isolation limits | same-ID query matrix/threat review |
| TN-04 | tenant-owned/global/control-plane model ownership | relations/cascade/upsert review |
| TN-05 | shared DB predicates/RLS/session reset/roles | direct SQL + pooled connection test |
| TN-06 | db-per-tenant mapping/credentials/pools/cache limits | two DB/misroute/rotation/eviction drill |
| TN-07 | migration capability/cohort and rollback fence | partial fleet deployment rehearsal |
| TN-08 | lifecycle provision/suspend/delete/hold/restore state table | partial/race/recovery runbook |
| TN-09 | audit/event/storage/i18n integration boundary | satellite scope/failure conformance results |
| TN-10 | telemetry/log privacy vocabulary | fake tenant/DSN/object canary collector scan |
| TN-11 | cross-tenant authority/cohort/export semantics | expiry/partial/authorization/audit drill |
| TN-12 | backup/DR/move and tenant generation strategy | isolated restore/cutover/fence rehearsal |
| TN-13 | capacity classes/pool/fairness/overload policy | load/noisy-neighbor/sparse tenant result |
| TN-14 | direct driver/raw SQL/ORM/cache inventory | architecture/strict coverage gate |
| TN-15 | user-visible absence/forbidden/conflict semantics | foreign-ID/no-enumeration API contract |

## Worked vertical slice — `orders` in the shared PostgreSQL profile

### Preconditions

1. Host authenticates user and verifies membership for opaque Tenant A reference.
2. Resolver returns active `row` binding with policy/schema epoch and no raw database
   credential; context carries immutable verified scope.
3. `orders` is declared tenant-owned; `order_lines` relation shares tenant ownership;
   a platform product catalogue is explicitly tenant-neutral/read-only.
4. Shared PostgreSQL schema has tenant-aware unique/foreign constraints and tested
   RLS/session configuration where RLS is enabled.
5. `orders` repository is wrapped by scoped query/write contract and audit policy.

### One request

1. User calls update of order ID `42`; service opens tenant-bound unit of work.
2. Repository loads `orders` with Tenant A predicate and optimistic version;
   same order ID `42` in Tenant B is never visible to the query.
3. Domain validates transition; update predicate repeats tenant/ID/version condition.
4. Audit revision/item use Tenant A within same transaction and capture declared
   status only; optional OTel span sees bounded operation/outcome/topology only.
5. If event sourcing is active, event append/outbox/snapshot/projection acquire the
   same scope and use tenant-aware stream/checkpoint schema.
6. Transaction commits. Storage/i18n work, if any, receives scoped capability or
   renders stable data at edge—neither routes from raw user request values.
7. On read, tenant-scoped cursor/history/profile show only A. A sees localized label
   based on user/tenant i18n policy, not a language value persisted in order/audit.

### Negative rehearsal

1. Substitute Tenant B's `42` under A: Get/Update/Audit/Events return zero foreign
   effect and a safe outcome; PostgreSQL RLS/query matrix both prove protection.
2. Remove context: no SQL, no audit revision, no event append, no storage lookup.
3. Reuse a pooled connection after A under B: RLS/session/caches are reset/verified.
4. Suspend A mid-flight: documented epoch/lifecycle outcome occurs; later request
   cannot renew scope or use cache. Audit/control-plane evidence gives truthful state.
5. Put fake A reference/DSN/object path into error input: scanner finds none in
   metric/traces/log/export/default error message.

### Transition to database-per-tenant

Only after this slice's proof, change the resolver adapter—not `orders` service
method—to return a database binding for A and B. Re-run the same scenario corpus.
Then add database pool/credential/migration/restore/fence tests. Any difference in
cross-database transaction, cohort availability or recovery semantics remains
visible to callers/operators rather than being hidden behind the same interface.

---

## Final review worksheet

Before enabling a topology or calling an integration tenant-safe, answer with a
link to test/ADR/runbook instead of an intention:

1. What exact trusted component creates scope, and which raw carriers are rejected?
2. What does lifecycle state/epoch mean, and how does resolver behave in every state?
3. Which models/relations are owned, global or control plane, and how is that enforced?
4. Does every supported query/write/cache/ORM/raw/bulk shape pass same-ID foreign tests?
5. What extra defence does PostgreSQL RLS/role/constraint provide, and how is pool state reset?
6. How does a database binding avoid DSN/credential exposure, stale cache and wrong mapping?
7. How are audit/event/storage/i18n/jobs given scope and prevented from routing raw payloads?
8. What happens in migration, partial cohort, rollback, restore, move and credential rotation?
9. What does an ordinary user see versus support/compliance/control-plane/cross-tenant operator?
10. How are partial/unknown/expired cohort outcomes represented without false success?
11. Which metrics/logs/traces remain safe, and which protected tooling diagnoses a tenant incident?
12. What resource/pool/fairness limits exist and how do they behave under control-plane outage?
13. Which files/tables/objects/backups/keys remain on suspend/delete/legal hold/restore?
14. Which tenancy claims have conformance evidence in each profile, and which are deferrals?
15. Can a new package bypass the model through direct driver/SQL/config import unnoticed?

## Definition of done

A tenancy profile is complete only when all answers above are evidence-backed and
the scope semantics are consistent across CRUD, audit, event store, storage, i18n,
jobs, telemetry, migration and recovery. The initial framework guarantee should be
small and precise: it is safer to offer a tested shared-row profile and a separate
tested database-per-tenant profile than one configuration switch that obscures the
different ownership, release and operational contracts.

---

## Review cards for every tenancy-expanding change

### TR-01 — new inbound transport

Name which carrier can request a tenant and which identity component verifies
membership/delegation before scope construction. State whether it supports only
one tenant, an authorized control-plane action or a separate cohort grant.

Show invalid/malformed/non-member/suspended request tests with zero data-plane
side effects. Do not accept a transport merely because it carries a `tenant_id`.

### TR-02 — new repository or SQL path

Classify resource ownership and list Get/List/Count/Exists/joins/preloads/cursor/
create/update/upsert/delete/bulk/cache behavior. Show equal-ID/foreign relation
tests, and either support raw command explicitly or reject it in strict mode.

Document all global/control-plane joins so a reviewer never mistakes an omitted
tenant predicate for a hidden security bug or, worse, an unreviewed deliberate one.

### TR-03 — new database topology/provider

State who owns resolver/credential client, opaque binding fields, epoch/TTL/cache/
pool limits, health/readiness and wrong-database fencing. State transaction,
backup/restore, migration, retention and service outage differences from shared DB.

Show two real targets, stale-map/rotation/pool-reuse/resolver-outage tests before
describing provider compatibility. Never expose provider DSN/bucket names via API.

### TR-04 — RLS or database role rule

State how session tenant setting/role is established, reset, protected from caller
control and tested on pooled connection checkout. State which queries/maintenance
roles bypass RLS and their separate cohort/authorization/audit procedure.

Show direct SQL positive/negative and connection reuse tests. RLS is extra defence,
not a reason to remove application scope/query contract or tenancy conformance.

### TR-05 — new satellite integration

State how it receives verified scope/binding and how it avoids raw payload/header/
locale/key routing. Cover write/read/retry/background/failure/retention behavior:
audit, events, storage, i18n and OTel each have different local boundaries.

Show one same-ID and one missing/forged scope test in that satellite, including
privacy canary scan. Generic `context.Context` acceptance alone is insufficient.

### TR-06 — new lifecycle transition

State authority, preconditions, idempotency/epoch, external resources, operator
visibility, data-plane effect, audit and compensation/irreversibility. Include
provision partial failure, suspend race, legal hold, delete and restore cases.

Show exact control-plane state machine. A destructive cloud/database command cannot
be the lifecycle design by itself because it loses truth about dependent evidence.

### TR-07 — new migration/release requirement

State compatible app/schema/codec/catalogue range and phase: expand, reader deploy,
cohort migration, writer cutover, rollback fence and retirement. Include old/offline/
restored tenant generations and partial cohort failure.

Show runner lock/idempotency/query/rollback drills. Reject a release if the only
answer is "all databases will get it before deploy" without an enforced fence.

### TR-08 — new cross-tenant feature

State purpose, issuing authority, immutable cohort snapshot, expiry, budgets,
per-tenant callback/partial result, field/export limits and access audit. State
why it cannot be performed through normal scoped repository or a global admin pool.

Show expired grant, one-failing-tenant, oversized cohort and no-nil-scope tests.
Cross-tenant capabilities may be powerful, but they must be conspicuous and finite.

### TR-09 — new telemetry/diagnostic field

State bounded question answered and prove it contains no tenant/ref/database/schema/
DSN/object path/raw carrier. State protected debug workflow for when on-call needs
the identity that generic signals intentionally omit.

Show collector test with fake tenant/secret and high-cardinality stress. Hashing a
tenant ref remains a correlation identifier; it is not a default safe workaround.

### TR-10 — capacity/quota policy

State trusted capacity class, global/per-class limits, pool/client/queue budgets,
fairness objective, overload result and control-plane outage behavior. State how
shared dependencies still create blast radius in database topology.

Show sparse-tenant, noisy-neighbor, long transaction and cache churn load tests.
Avoid rate keys/metric labels derived from user-controlled tenant display strings.

### TR-11 — tenant data move/recovery

State source/target generation/epoch, copy/delta/drain/fence/validation/cutover,
credentials, backups, audit/event/outbox/storage catalogues and rollback point.
State what happens to queued jobs, presigned references, retained evidence/holds.

Show crash/retry/two-writer/old-backup/partial-copy rehearsal. A data move should
be observable as a controlled lifecycle operation, not a transparent resolver edit.

### TR-12 — a new framework convenience API

State why it cannot create a raw tenant context, nil scope, direct datasource, raw
storage prefix, unbounded cohort or root-level identity/driver dependency. State
the explicit host/satellite adapter boundary it preserves.

Show architecture/dependency test and a misuse example that fails loudly. Good DX
removes boilerplate while retaining authority at the verified scope boundary.

---

## Common shortcuts to reject

1. Adding `tenant_id` to the model but filtering only normal list queries.
2. Trusting URL subdomain/header/body claim without identity membership verification.
3. A package-level `CurrentTenant` global that background worker/pool reuses.
4. Passing database name, DSN, S3 prefix or tenant secret through request/event JSON.
5. Choosing database-per-tenant by string config without lifecycle/migration/pool plan.
6. Assuming a per-tenant database means tenant-scoped object storage/audit/events are safe.
7. Calling `nil` scope "platform admin" and adding arbitrary SQL/report methods.
8. Assuming RLS protects a connection whose role/settings are inherited in a pool.
9. Logging/hash-labeling tenant identities because it makes dashboards convenient.
10. Treating a partial cross-tenant report as an empty or fully complete result.
11. Dropping a tenant database/bucket before checking audit retention, legal hold or backup.
12. Routing workers from legacy payload tenant ID with no current lifecycle/epoch check.
13. Promising global transaction/ordering across per-tenant PostgreSQL databases.
14. Letting cache/ORM identity map outlive tenant scope without scope/epoch in its key.
15. Bypassing resolver after migration trouble with an operator-provided direct connection.
16. Treating locale/branding/display name as a security identity.
17. Capturing whole tenant config/metadata in trace/log/error for "support".
18. Creating one permanently open pool for every tenant with no capacity/cost bound.
19. Hiding unsupported bulk/raw SQL/cross-tenant paths behind a generic feature flag.
20. Calling the implementation secure without current conformance/recovery evidence.

## First production profile record

```text
Profile:                    shared PostgreSQL row isolation
Scope source:               verified host membership resolver
Scope identity:             opaque immutable ref + lifecycle/epoch
Supported models:           orders/order_lines (tenant-owned); catalogue (global read-only)
Query coverage:             Get/List/Count/Exists/Update/Delete + cursor and relation fixtures
Database protection:        composite constraints + tested RLS/session reset (if enabled)
Audit/event/storage/i18n:   scoped adapters, one conformance case each
Telemetry:                  topology/outcome only, privacy-canary scanner passed
Lifecycle:                  provisioning/active/suspended/deleted, manual move/restore deferred
Database-per-tenant:        not implied; separate profile status shown in docs/index
Known exclusions:           raw SQL, generic bulk, cohort reports, arbitrary per-tenant buckets
Release guard:              schema/policy capability compatible before writes
Operators:                  resolver/pool/migration/incident dashboards and runbook owners
Evidence:                   TC-01..40 applicable results, same-ID, invalid scope, load/DR sample
```

Publishing this profile record beside the implementation stops teams from reading
more guarantee into the word "multitenancy" than has been built. Each later
extension replaces a named `deferred` line with a review card, adapter profile
and evidence—not an implicit new obligation for all framework consumers.

## Closing principle

Tenant identity is a security and operational boundary that must be established
before data access, carried explicitly through the whole operation and validated
again wherever topology/lifecycle makes stale or forged state dangerous. It is
not a presentation field, a driver option, a database name or a telemetry label.

The framework's job is to make the safe path terse—one verified scope, one bound
capability, one scoped callback—while making exceptional cross-tenant, lifecycle,
migration and recovery work visibly deliberate. That is how shared-row and
database-per-tenant deployments can share business code without pretending their
failure domains, release choreography or evidence requirements are identical.

---

## Team handoff map

No team owns multitenancy alone. The following handoff map identifies where a
change must be coordinated and what each owner should be able to demonstrate.

| Owner | Accountable boundary | Handoff evidence |
|---|---|---|
| application/domain | model ownership, scoped repo use, business errors | query matrix and same-ID tests |
| host/identity | trusted carrier, membership/delegation, scope issuance | forged/non-member/stale claim tests |
| tenancy/control plane | lifecycle/binding/epoch/cohort/capacity class | state transitions and resolver drills |
| database | schema/RLS/roles/pool reset/migrations/backups | direct SQL/connection/recovery evidence |
| event platform | scoped stream/outbox/projection/replay | payload-route/checkpoint/duplicate tests |
| storage platform | namespace/capability/version/lifecycle | fs/S3 scope/orphan/hold/recovery tests |
| audit/privacy | actor/tenant evidence, reader/export/retention | viewer/canary/hold/cross-tenant tests |
| i18n/product | locale default/catalogue/branding boundaries | fallback/unsafe catalogue/version tests |
| observability | bounded safe signals/protected diagnosis route | collector/cardinality/privacy results |
| release/operations | cohorts/SLOs/runbooks/incident/rehearsal | migration/restore/failover/load reports |

### Handoff questions between teams

**Host → tenancy.** What exactly is verified at the moment scope is minted? Is
membership current enough for the requested operation, and what raw carrier data
is intentionally discarded before app code sees it?

**Tenancy → database.** Which opaque binding/epoch is allowed to obtain which
database role/pool? How does a checked-out connection prove it has no state from
a prior scope, and how do migration/readiness failures fence use?

**Database → application.** Which resource constraints/RLS roles cover each query
shape, and which repository/ORM/raw SQL paths remain unsupported/forbidden?

**Application → audit/events/storage.** How is scope passed across transaction,
outbox/job/retry/object promotion boundaries? Which state becomes provenance versus
which raw value is prohibited from durable/telemetry output?

**Control plane → operations.** What does `migrating`, `suspended`, `deleting`,
`restored` and `unknown binding` mean for traffic, alert, customer communication
and human intervention? How can a recovery never create two active generations?

**Operations → product.** Which topology/capacity/recovery guarantees are actual
for the selected plan, and which cross-tenant/reporting/export requests need a
separately funded control-plane capability rather than a repository feature?

## Incident response decision table

| Symptom | Immediate safe action | Do not do | Reconciliation evidence |
|---|---|---|---|
| foreign data suspected | fence route/pool/cache and preserve evidence | add broad log labels or direct SQL bypass | scope/binding/role/RLS/query/audit trail |
| resolver unavailable | fail closed per policy, assess cache safety | hardcode DSN/default tenant | binding TTL/lifecycle/availability runbook |
| wrong mapping detected | revoke/fence binding, stop new writes | retry against guessed DB | epoch/source/target and foreign-write check |
| pool exhaustion | apply bounds/queue, inspect long work | unlimited pools/role reuse | capacity class, checkout/reset/load profile |
| migration partial | fence incompatible cohort and report partial | mark fleet complete/force old app writes | migration lock/version/capability outcomes |
| tenant move uncertain | stop cutover/new scopes, find active generation | accept two writers or edit mapping blindly | lease/epoch/copy/delta/outbox checkpoints |
| restore target exposed | isolate/rotate/fence it | attach to traffic to "test" | schema/identity/hold/audit/event/storage validation |
| legal hold/delete conflict | preserve and escalate governance | drop DB/bucket/key | lifecycle/hold/backup/archive record |
| telemetry canary leak | stop unsafe signal/export and triage access | rely on hashing/retention alone | collector artifacts, recipient/copy inventory |
| cross-tenant report incomplete | return explicit partial/unknown and resume safely | silently omit failed tenants | grant/cohort/per-scope outcome/idempotency |

## Capacity planning worksheet

For the selected profile, establish actual inputs rather than a generic tenant
count: active/idle tenant distribution, request concurrency, write/read mix, DB
connections, background jobs, object traffic, retention volume, migration window,
backup/restore target and peak cross-tenant control-plane activity.

For shared DB, quantify shared lock/index/vacuum/IO/connection/query-plan pressure
and how one tenant's export/rebuild/mass change is throttled. For database-per-
tenant, quantify pool/client/credential/cache overhead, common queue/storage/host
dependencies and the cost/time of cohort migrations/backups/recovery. Both need
tested quotas/timeouts/budgets, not a promise that arbitrary tenant counts scale.

At every capacity threshold, define a user-visible safe response: queue, retry
after, feature unavailable, partial cohort outcome or controlled admission wait.
Never respond by weakening scope validation, sharing a prior connection's tenant
state, or exposing identifiers in a desperate dashboard label.

## Compact data-flow audit

```text
verified principal / control-plane authority
        -> opaque scope(ref, lifecycle, epoch, topology capability)
        -> scoped repository or bound datasource
        -> tenant-owned CRUD / event append / audit revision / storage reference
        -> asynchronous envelope with verified scope reference, never raw router
        -> protected history/control-plane operations

locale, display name, raw header, event payload, object URL, DSN, SQL and telemetry
are not substitute forms of scope and must not cross arrows as authority.
```

The same flow applies to shared DB (the capability composes safe predicates/RLS)
and database per tenant (the capability resolves a bound local datasource). The
downstream business code stays similar, but migrations, pools, backups, recovery
and cross-tenant operations deliberately branch at the control-plane boundary.

---

## Adoption exercises

These exercises turn the roadmap into a team workshop. Each one is completed with
the actual host, repository, driver and satellite adapters, not an isolated mock.
Record assumptions, commands, expected evidence, result, owner and any deferred
work in the tenancy profile release record.

### EX-01 — prove the trust boundary

Send requests with a correct tenant membership, no carrier, malformed carrier,
valid carrier for another tenant, stale membership and suspended tenant.

For each, inspect whether a verified scope was minted, whether any database/object/
event/audit work occurred, the client error class and telemetry privacy output.

The exercise fails if a developer can reach the repository by manually creating
the same context value that production host is supposed to protect.

### EX-02 — prove equal-ID isolation

Create identical IDs and business keys in two tenants, including parent/child
relation and audit/event/storage logical reference where that satellite is enabled.

Run all supported getters, lists, counts, aggregates, cursor pages, updates,
deletes, preloads, upserts and error paths from each scope.

The exercise fails if any result/mutation/count/cache/trace/export contains or
infers the other tenant's state, including under a zero-row/not-found response.

### EX-03 — prove pooled state reset

Run Tenant A operations that set RLS/session/query/cache/client state, release
resources, then force Tenant B to acquire the same physical connection/client.

Inspect current role/settings/search path, query output, cache keys and resolver
epoch. Run it under normal completion, cancellation, error and transaction rollback.

The exercise fails if cleanup is merely assumed by a library rather than verified
against the actual PostgreSQL driver/pool/settings profile.

### EX-04 — prove lifecycle fences

Provision a test tenant, activate it, suspend it during an in-flight operation,
attempt normal work, begin delete with a simulated legal hold and then restore
an older isolated database target.

Observe binding epochs, data-plane effect, audit/control-plane records, satellite
readiness and the exact point at which a target could become active again.

The exercise fails if a lifecycle state can be changed in UI/control plane while
raw cached connection, job or object URL continues unrestricted work.

### EX-05 — prove database-per-tenant mapping

Use two actual PostgreSQL databases. Resolve A/B, rotate one binding/credential,
then inject wrong/stale mapping and resolver outage during request/job/transaction.

Observe pool count/eviction, fencing, unknown commit/retry result and error redaction.
Repeat with same aggregate/resource IDs in each database.

The exercise fails if app code sees DSN/database identity, fallback picks a default
database, or stale map can write foreign data before detection.

### EX-06 — prove migration cohort protocol

Deploy compatible reader, migrate only A, deliberately fail B, attempt old/new
write paths, retry migration and attempt application rollback.

Observe resolver compatibility fence, user-visible behavior, audit/event codec
support, dashboard aggregate cohort result and ability to return to safe state.

The exercise fails if compatibility is inferred from deployment success or an
operator can bypass it by opening a database directly through the service.

### EX-07 — prove satellite propagation

For one scoped command, append event, update projection/job, produce audit item,
stage/promote storage object, render i18n message and emit OTel signals.

Then repeat with missing/forged/stale scope and an injected satellite failure.
Inspect which transactions/records stay local, pending, absent or reconciled.

The exercise fails if any satellite routes from raw payload/locale/key, emits a
tenant identifier in telemetry, or claims cross-system atomicity it lacks.

### EX-08 — prove exceptional cohort grant

Issue a short case grant for A+B, let B fail/expire midway, attempt an oversized
cohort and request an export. Inspect callback scopes, result completeness, audit
access evidence, output fields/TTL and cancellation behavior.

The exercise fails if ordinary repositories accept nil/multiple scopes, if a report
hides an unavailable tenant, or if grant alone unlocks unrestricted data export.

### EX-09 — prove recovery and move

Restore/move a tenant in a disposable environment: copy, validate references,
drain, fence source, cut over epoch, resume jobs/outbox and simulate runner crash.

Inspect two-writer prevention, storage/audit/event readiness, backup/hold status
and post-cutover identity/telemetry privacy.

The exercise fails if recovery attaches target before validation or a retry creates
two active databases for the same tenant reference.

### EX-10 — prove diagnostics without identity exhaust

Inject fake tenant ref, display name, database, DSN, bucket/path and object URL
into ordinary/error/retry/timeout/migration/export code paths and collect signals.

Verify dashboards still distinguish topology/component/outcome while protected
control-plane/audit workflow is the only path to actual tenant diagnosis.

The exercise fails if hash/truncation is used as unreviewed escape hatch or if
observability cannot operate without raw high-cardinality identity.

## Evidence package template

```text
Profile/version:                 ______________________________
Topology:                        shared-row | database-per-tenant
Host/scope source:               ______________________________
Database/driver/RLS configuration: ____________________________
Control-plane/binding epoch policy: ___________________________
Supported resource query matrix: ______________________________
Satellite profiles passed:       audit / eventpg / storage / i18n / jobs / otel
Conformance scenario results:    TC-__________________________
Lifecycle/migration/recovery drills: __________________________
Capacity/pool/load result:       ______________________________
Privacy canary collector result: ______________________________
Known exclusions/deferrals:      ______________________________
Release/rollback compatibility:  ______________________________
Runbook/dashboard owners:        ______________________________
Approvals/risk expiries:         ______________________________
```

Do not sign this template based on architecture diagrams alone. Link every claimed
property to a test, configuration, migration, drill or protected operational
artifact that was actually executed for the profile being released.

---

## Tenancy decision patterns

These compact patterns make common implementation choices reviewable without
copying a large policy into every package.

### Pattern: tenant-owned resource

Use when a row/object/stream exists only for one verified tenant.

Require immutable opaque owner ref, tenant-scoped create/load/list/write/delete,
tenant-aware unique/foreign constraints and scope/epoch-aware cache behavior.

Forbid caller-selected owner changes and raw unscoped repository access.

Example: an order, tenant-specific user profile, subscription or private document.

### Pattern: platform-global reference resource

Use when a resource is deliberately shared and is not a tenant's private data.

Keep it in a separately named repository/ownership class; writes require platform
authority and joins into tenant-owned rows are explicit/read-only where possible.

Forbid adding a fake tenant column just to pass generic decorator or treating global
visibility as cross-tenant administration.

Example: a controlled product catalogue or platform locale registry.

### Pattern: control-plane resource

Use when it configures bindings, lifecycle, cohorts, capacity classes or credentials.

Protect it with control-plane authorization/audit and never access it through normal
tenant data scope/repository. Its stable tenant ref is input to resolver, not a row
that application users can mutate with an update endpoint.

Example: tenant lifecycle entry or database-binding generation.

### Pattern: tenant-scoped asynchronous work

Use when a job, outbox item, notification or projection acts for one tenant.

Store opaque scope/reference plus necessary version/provenance, then reauthorize/
resolve active lifecycle/epoch before handler obtains data-plane capability.

Forbid handlers from trusting raw payload `tenant_id`, a global current scope or
reusing sender's database/client connection across process boundaries.

Example: order projection, file scanner, localized notification or report rebuild.

### Pattern: bounded cohort work

Use when an operator legitimately needs more than one tenant.

Create explicit purpose/authority/cohort snapshot/expiry/budget and run a callback
with one ordinary scope per tenant. Return per-scope success/failure/unknown state.

Forbid an empty scope, arbitrary tenant slice, generic global SQL/connection or a
report that suppresses unavailable tenants.

Example: regulated reconciliation, migration wave or named fraud investigation.

### Pattern: tenant generation move/restore

Use when data changes physical/database/storage location or re-enters from backup.

Maintain source/target/epoch/fence/validation/cutover/audit state and block new
writes until exactly one active generation is approved by control plane.

Forbid resolver edits that create two writers, stale job replay into old target or
automatic reactivation of historical data without lifecycle/hold/schema checks.

Example: regional migration, disaster recovery restore or database split repair.

## Non-goals for the first implementation

The first satellite deliberately does not provide automatic authentication/JWT
parsing, identity membership administration, secret-manager ownership, generic SQL
rewriting, schema-per-tenant, arbitrary cloud account creation, global analytics,
unbounded cross-tenant reporting, exactly-once multi-database command processing
or a claim that every installed driver/ORM/cache is safe by default.

These are not omissions to paper over with convenience APIs. They are boundaries
that prevent the framework root from owning consumer-specific dependencies and
keep the two initial profiles testable. Each future expansion needs its own adapter,
control-plane contract, security/privacy review and scenario results.

## Sign-off statement

Select this statement only when the profile's evidence package is complete:

> This release supports the named topology/profile and listed adapters. Tenant
> scope is verified before data-plane access, invalid lifecycle/mapping fails closed,
> supported query and satellite paths have passed conformance, and operations have
> rehearsed migration/recovery/privacy controls. Unlisted topologies and paths are
> deliberate non-goals, not implied by the presence of a tenant field.

This wording is intentionally constrained. It gives adopters a dependable promise
and gives maintainers a clear trigger to add evidence before expanding it.

---

## Deployment day checklist

### Before traffic

1. Verify the compiled profile states `shared-row` or `database-per-tenant`, never
   an ambiguous default; list current adapter version and known exclusions.
2. Verify host identity integration creates opaque scope only after membership/
   lifecycle checks, using production configuration—not a unit-test fake alone.
3. Run TC-01, TC-02, TC-03, TC-11, TC-14, TC-15 and TC-20 on release candidate.
4. For shared DB, verify actual RLS/roles/session reset/query plans and pool config.
5. For database topology, verify live binding epoch, max pools, stale map/rotation
   behavior and two real database targets; inspect safe aggregate health state.
6. Compare app compatible schema/capability range with shared DB/tenant cohort state.
7. Verify audit/event/storage/i18n/job adapters are all on a passed scope profile;
   disable/fence features whose satellite is not ready rather than bypass scope.
8. Run fake tenant/DSN/bucket/object canary through errors/retries and collector.
9. Verify lifecycle state and hold/deletion/migration flags have no unexpected active
   binding, and recovery targets are isolated from service routing.
10. Verify dashboards/runbooks/ownership and the declared cross-tenant authority
    before granting any privileged report/migration/export access.

### During rollout

1. Start with an internal/small cohort and execute equal-ID, invalid-scope and
   scoped mutation/history smoke paths under actual traffic wiring.
2. Watch bounded resolver/pool/DB/audit/event/storage outcome signals and query
   protected control-plane details only through authorized incident workflow.
3. Stop rollout on foreign result, scope bypass, unknown binding, unsafe telemetry,
   unbounded pool growth, incompatible cohort or unexpected partial outcome.
4. If rollback is considered, first verify writer/reader/schema/codec compatibility
   for each cohort; prefer fence/read-only/forward fix to unsafe broad downgrade.
5. Record actual scope/profile/schema/epoch evidence—not only deployment timestamp.

### After rollout

1. Reconcile audit/event/outbox/job/storage results for the canary scope/commands.
2. Review missing/denied/foreign-scope failures and ensure none used a direct bypass.
3. Validate cache/pool eviction/session reset behavior under one real traffic cycle.
4. Review capacity envelope against observed pool/DB/queue/object workload and adjust
   bounds through control-plane policy rather than code-side raw tenant exceptions.
5. Mark evidence package and roadmap index with actual supported profile/version.
6. Schedule migration/restore/suspend/cross-tenant drill before the next expansion.

## Final compact summary

One database mode needs a complete predicate/constraint/RLS/query/caching story.

Database-per-tenant mode needs a resolver/binding/credential/pool/cohort/recovery
story. Both need verified scope, lifecycle epoch, satellite propagation, protected
observability, audit evidence and a disciplined exceptional control plane.

If an implementation cannot explain how one verified scope becomes exactly one
bound data-plane capability—and how that capability fails during lifecycle change—
it is not ready to expose tenancy as a framework feature.

## Compact FAQ for implementers

**Can a repository accept `tenantID` as a parameter?** Not on normal data-plane
methods. It should receive verified scope from context/bound unit of work. A named
control-plane/migration method may receive a protected opaque tenant reference.

**Can we use tenant ID in a storage prefix?** The storage adapter may derive an
opaque authorized namespace from scope. Application code must not construct paths,
prefixes or signed URLs by concatenating raw tenant/user input.

**Does every table have `tenant_ref`?** No. Classify it as tenant-owned, global or
control-plane. Tenant-owned data needs an enforced ownership model; fake columns
on global tables make joins/security semantics less clear, not safer.

**Does RLS solve all shared DB risks?** No. It is valuable defence in depth, but
scope creation, query shape, role grants, session reset, raw path inventory, cache
and lifecycle still need proof. Maintenance/backup/BI roles need separate controls.

**Can we add database-per-tenant later?** Yes, when the service depends on a
scope-to-capability interface, but it remains a separate adapter profile requiring
resolver, pool, credentials, lifecycle, cohorts, migration and recovery tests.

**How do we run an all-tenant report?** Through explicit short-lived purpose-bound
cohort authority. Execute ordinary single-scope operations one at a time, return
partial state and audit/export the exceptional access. Never use nil tenant scope.

**Where does tenant locale live?** In presentation/control-plane configuration.
It can affect deterministic i18n fallback after scope verification, but not data
routing, stored audit/event keys, object namespaces or generic telemetry identity.

**What should appear in metrics/traces?** Bounded topology, operation, result and
capacity state. Use protected audit/control-plane tooling for actual identity;
tenant/database/schema/DSN/object references are not default signal dimensions.

**What happens when a tenant is deleted?** Lifecycle/retention/legal-hold/backup/
object/event/audit decisions are executed and recorded by control plane. The ref is
normally never reused; deletion UI state alone must not authorize destructive work.

**How do we test it?** Start two tenants with equal IDs, then make missing, forged,
stale, suspended, wrong-mapped and pooled-after-foreign scenarios fail with zero
side effect. Repeat through every advertised satellite and topology profile.

## Invariant sampler

- no scoped operation runs with an absent/default/forged tenant capability;
- every normal repository operation uses exactly one verified tenant scope;
- wrong/missing lifecycle, epoch or schema binding fails before side effect;
- foreign equal IDs never cross reads, counts, joins, writes, caches or exports;
- tenant metadata cannot be turned into a raw DSN/path/key/locale/telemetry route;
- satellite work restores scope intentionally and cannot trust raw queue/event payload;
- cross-tenant work is explicit, finite, auditable and honestly partial on failure;
- topology-specific migration/backup/recovery has one active generation/fence truth;
- generic observability remains useful without tenant identity exhaust;
- all advertised guarantees map to a current conformance profile and operator drill.

These invariants are short enough for design review, but they are backed by the
query matrix, catalogue and runbooks above. A new feature must preserve all of
them or expand the roadmap and evidence intentionally.

---

## Integration reference table

This table is a final code-review shortcut: it summarizes where scope belongs in
the request and which tempting inputs must not become authority for each satellite.

| Integration | Receives | Must not use to route | Minimum negative test |
|---|---|---|---|
| CRUD repository | verified scope/bound UoW | input owner/tenant filter | foreign equal-ID update |
| PostgreSQL RLS | resolver-controlled session/role | client session setting | pooled A then B checkout |
| db resolver | opaque binding/ref/epoch | raw tenant header/DSN | stale/wrong mapping fence |
| event store | scoped stream capability | event payload tenant key | equal stream IDs A/B |
| projection/job | verified durable scope reference | raw queue tenant field | forged/deleted job no-op |
| audit | one tenant scope/actor policy | nil admin/default context | foreign history join denial |
| storage | scoped store namespace capability | filename/S3 URL/prefix | path/prefix foreign access |
| i18n | permitted tenant default config | locale/display name identity | malformed locale isolation |
| OTel/logs | bounded topology/result | tenant/db/DSN/object ref | fake-canary absent collector |
| migration | cohort binding/version/epoch | global unbounded list | partial cohort accurate state |
| recovery/move | source/target generation/fence | stale direct connection | no two active writers |
| cross-tenant admin | purpose-bound cohort grant | slice/nil scope | expiry/partial/outcome test |

For any new satellite, add a row before implementation. If the row cannot say
what trusted scope it receives and which raw input it rejects, it has not yet
joined the tenancy architecture safely.

## Final readiness statement

The roadmap's desired developer experience is concise at use sites:

```go
scope := verifiedScopeFromHost(ctx)
return tenants.For(scope).Orders().Update(ctx, command)
```

The apparent simplicity rests on the considerable explicit work above: trust
verification, scope lifecycle, query matrix, database binding, pool reset, tenant
satellite propagation, migration/recovery fences and conformance evidence. This is
the right trade: product code remains readable while unsafe shortcuts have no
plausible default route through the framework.

## Last-mile release questions

Use these questions in the final go/no-go meeting; each must point to current
profile evidence, not to an intention to add controls after adoption.

1. Which topology is enabled for this release, and is the other one explicitly
   marked unavailable rather than appearing as a configuration value?
2. Which host code mints a scope, and has the production integration rejected
   forged, absent, non-member and stale inputs with no tenant side effects?
3. Which entities, keys and relations were exercised with identical IDs across two
   tenants, including all list/count/cache/preload/update/delete paths?
4. Which role/session/pool test proves one tenant cannot inherit another one's
   PostgreSQL state—and which database binding test proves mapping cannot drift?
5. Which event, audit, storage, job and i18n adapters passed the scope propagation
   negative tests, and which integrations are still deferred/fenced?
6. Which telemetry canary verifies no tenant/database/DSN/object identity leaks
   while incident responders can still locate protected diagnostic evidence?
7. Which schema/capability/cohort migration release combination is safe to deploy
   and roll back, including an offline or failed tenant database?
8. Which lifecycle/hold/restore/move rehearsal proves deletion/recovery cannot
   create a foreign access path or two active generations?
9. Which capacity/pool/timeouts prevent an active or sparse tenant fleet from
   exhausting shared resources, and what result does overload return?
10. Which exact authority issues exceptional cross-tenant access, how long does it
    last, and how does caller learn a partial/unknown cohort result?
11. What unsafe direct driver/SQL/global context API is impossible or rejected by
    architecture tests, rather than merely discouraged in code review?
12. Who owns the next re-run of this catalogue when scope, topology, lifecycle or
    a satellite changes?

## Closing operational promise

The implementation should promise only this: a verified active scope maps to one
authorized tenant data-plane capability; scopes cannot be skipped accidentally;
and every advertised adapter/topology behavior has executable proof. The promise
does not grow merely because more models receive a `tenant_ref` field.

That restraint makes multitenancy a dependable foundation for the storage, event,
audit, i18n and observability roadmaps rather than a cross-cutting source of hidden
exceptions.

### Final implementation reminder

Do not start by adding a tenant field across models.

Start by proving the verified scope boundary and one scoped repository operation.

Then make every additional query, satellite, topology and lifecycle transition
earn its place through the same conformance evidence.

Never permit a failure/recovery shortcut to silently choose a default tenant.

Never use a tenant identity as convenient observability payload.

Never call a partial cohort operation complete without naming its missing scopes.

Never confuse a control-plane capability with ordinary tenant data access.

Never let a database name or storage prefix become application-level authority.

Never delete/reuse a tenant identity until retention, holds, backups and references
have reached the explicitly approved lifecycle disposition.
