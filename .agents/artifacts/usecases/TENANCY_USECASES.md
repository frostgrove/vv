# TENANCY - USECASES & INVARIANTS  (written blind to the codebase)

> **Reading rule for this document.** No identifier here names anything that
> exists in the tree. Where a mechanism must be pointed at, it is named by its
> role: *the repository seam*, *the source-selection seam*, *the durable-work
> boundary*, *the object namespace*, *the cache partition*, *the trust
> authority*, *the composition root*. Every Go-ish fragment uses deliberately
> fictional placeholder names and is illustrative of **shape only**. If a name
> below happens to collide with a real symbol, that is a coincidence and the
> real symbol is not what is meant.
>
> Two source documents govern and are cited as **[MT]** — the current
> multitenancy roadmap dated 2026-09-01 — and **[EXT]** — the optional extension
> architecture roadmap dated 2026-09-01. The 2026-08-26 snapshot referenced by
> [MT] is no longer present in the tree; its threat catalogue is reconstructed
> from the obligations [MT] carries forward.

---

## Problem in one paragraph

One deployment serves many customer organisations (tenants), and every row,
object, cache entry, queued job and background side effect belongs to exactly
one of them — or, rarely and explicitly, to an administrative purpose that spans
several. The whole problem is that *belonging* is not represented anywhere the
runtime can see by default: a query is just a query, an object key is just a
string, a job payload is just bytes, and a connection pool will happily hand any
caller any connection. A multitenancy extension exists to make belonging a
precondition rather than a convention: a **verified tenant scope**, produced only
by an injected trust authority after the host has already authenticated identity
and membership, must select **exactly one** authorised data-plane capability —
one narrowing predicate under shared-row deployment, or one datasource under
database-per-tenant deployment — and an absent, forged, stale, lifecycle-invalid
or topologically incompatible scope must fail **before any tenant side effect
occurs**, in every subsystem, not only in the one the author remembered. Tenancy
is also a control-plane subject: tenants are provisioned, active, suspended,
migrating, deleting and restored into new generations, and the data plane must
refuse work for states it was not explicitly told are compatible, including
states it has never heard of. The extension must achieve all of this while
remaining *optional* — removing it leaves the base composition intact ([EXT]:
"the root and every pre-existing module expose no type from an optional
extension") — and while leaking nothing: [MT]'s privacy contract forbids tenant
reference, stable tenant hash, database/schema/DSN/user, pool key,
bucket/prefix/object reference, raw host/header/claim or wrapped error text from
appearing in generic telemetry signals. The failure mode this document exists to
prevent is not an outage. It is tenant A silently reading, counting, existing-
checking, caching, listing, exporting or deleting tenant B's data, with a green
test suite.

---

## Actors and entry modes

### Actors

| # | Actor | What it is | Trust it holds |
|---|---|---|---|
| **A1** | **Composition root** | Application wiring that constructs base capabilities, selects extensions and fixes their order | Full: it decides what exists. Holds no tenant scope itself |
| **A2** | **Business code** | Domain and application logic that reads and writes tenant-owned rows | None of its own; operates entirely on an already-bound capability |
| **A3** | **Request path** | HTTP/RPC ingress that has already authenticated a principal and verified its membership | Carries a *verified identity*, not a scope; must ask the trust authority for a scope |
| **A4** | **Durable-work boundary** | Producer that enqueues durable work and worker that later executes it, possibly in another process, after arbitrary delay | Carries a *durable reference*, which is never authority |
| **A5** | **Operator** | Human or runbook executing a tenant lifecycle procedure: provision, suspend, migrate, restore, delete, legal hold, move | Control-plane authority, explicitly out-of-band from the data plane |
| **A6** | **Cross-tenant job** | Administrative or analytical work spanning many tenants — billing rollup, cohort migration, platform report | A bounded **grant**: purpose + cohort + expiry. Never a nil scope, never a raw ID list |
| **A7** | **Trust authority** | The injected control-plane contract that maps a verified identity + tenant reference to a scope, and owns lifecycle state and binding epoch | The only production source of a verified scope |
| **A8** | **Observability consumer** | Dashboards, alerts, on-call. Reads generic signals | None. Must be able to operate without ever seeing tenant identity |
| **A9** | **Conformance author** | Whoever writes the fixtures and publishes the advertised profile | Test-only scope construction, unavailable in production |

Every actor has at least one happy usecase, so that a reader can see the system
working from each seat before reading how it fails:
A1 → UC-1..UC-4; A2 → UC-19..UC-25, UC-59, UC-60; A3 → UC-8; A4 → UC-51, UC-52;
A5 → UC-66..UC-70; A6 → UC-78; A7 → UC-9; A8 → UC-84, UC-85; A9 → UC-90, UC-91.

### Entry modes

| Mode | Description | Scope origin | Failure timing |
|---|---|---|---|
| **E1 Request-bound** | Synchronous work inside one authenticated request | A7, at ingress, per request | Before the first data-plane call |
| **E2 Durable** | Work enqueued now, executed later, possibly after process restart | Re-derived from A7 at execution against *current* lifecycle and epoch | Before the handler is given a bound context |
| **E3 Operator procedure** | A lifecycle transition run out-of-band | Control-plane authority; may be scope-less by design | Before the transition is announced to the data plane |
| **E4 Cohort** | Cross-tenant work under a grant | Grant issuance, with purpose and expiry | Per tenant, before that tenant's first effect |
| **E5 Composition** | Process start-up wiring | No scope exists | At construction, not at first request |
| **E6 Fixture** | Test and conformance construction | Test-only constructor | Compilation/link time in production builds |

---


**Group A — Composition and activation  (A1, mode E5)**

## UC-1 The composition root enables tenancy over an existing base composition  [happy]
- **Given** an application already constructs its base repository, object,
  cache and durable-work capabilities without any notion of tenants.
- **When** the composition root wraps those base capabilities with the tenancy
  extension's factories and declares the ordering relative to other selected
  extensions.
- **Then** the returned values are ordinary base-shaped capabilities; business
  code compiles unchanged; every subsequent tenant-owned operation flows through
  the tenancy decision.
- **Why it matters** [EXT] fixes that "the application composition root chooses
  which extensions exist and their deterministic order" and that importing an
  extension "must not register globals, discover application components or
  activate unused adapters". If enabling tenancy required editing business code,
  the extension has failed its sizing rule.
- **Acceptance** A fixture builds the same business-code package twice, once with
  the tenancy factories applied and once without, with no diff in the business
  package. Construction registers nothing observable process-wide: a probe that
  enumerates process-global state before and after import and after construction
  sees no new entry. The wrapped values still satisfy the base capability
  contracts they wrapped.

## UC-2 The composition root selects the shared-row topology for a tenant-owned resource  [happy]
- **Given** one PostgreSQL database in which the resource's rows carry an
  ownership column.
- **When** the root applies the shared-row strategy to that resource's repository
  seam, supplying the ownership mapping for that resource.
- **Then** every supported verb of that repository — get, list, count, exists,
  aggregate, relation/preload, create, save, update, upsert, delete, restore and
  each admitted bulk effect — is narrowed to the scope in context.
- **Why it matters** [MT] requires that "a typed CRUD middleware must cover the
  complete supported matrix"; a matrix with a hole is not a partial feature, it
  is an unlocked door.
- **Acceptance** A generated inventory of the repository seam's method set is
  compared against the set of verbs the strategy makes a narrowing decision for.
  The comparison fails the build when the two differ. A verb present in the base
  seam with no recorded decision is a failure, not a default-allow.

## UC-3 The composition root selects the database-per-tenant topology  [happy]
- **Given** a binding resolver that maps a tenant reference and binding epoch to
  a caller-owned root datasource capability, plus a bounded pool budget.
- **When** the root applies the source-selection strategy.
- **Then** a verified scope selects exactly one datasource *before* a transaction
  or repository value is handed to business code, and the selection is fixed for
  the lifetime of that unit of work.
- **Why it matters** [MT]: "Selection happens before a transaction or repository
  is exposed, nested operations remain on the selected source."
- **Acceptance** An instrumented fake datasource factory records every selection.
  For a request that performs N nested operations, exactly one selection is
  recorded and every statement is attributed to that source. Attempting to obtain
  a repository value before selection returns a refusal rather than an
  unselected value.

## UC-4 The composition root removes tenancy and the application still builds  [happy]
- **Given** an application built with the extension.
- **When** the tenancy factories are deleted from the composition root and the
  module requirement is dropped.
- **Then** the base composition compiles and behaves exactly as it did before
  tenancy was introduced, except where a durable-work definition explicitly
  demands a tenant partition and now refuses to register.
- **Why it matters** [MT]: "Removing tenancy leaves the base CRUD/service/jobs
  APIs and their module graphs intact." Optionality is a graded release gate, not
  a nicety.
- **Acceptance** Three isolated fixtures built outside any workspace overlay:
  base-only, tenancy-only, and multi-extension. The base-only fixture's resolved
  module graph contains no tenancy module and no tenancy-owned type. The
  multi-extension fixture's package count is the sum of its decisions, with no
  package whose identity is the intersection of two extensions.

## UC-5 An opaque third-party wrapper sits between tenancy and the base capability  [edge]
- **Given** the root composes tenancy with an unrelated wrapper the extension has
  never heard of, in either order.
- **When** some executable optional effect — an unscoped-existence check, a batch
  read, an administrative queue effect — is looked for through the chain.
- **Then** the effect is either satisfied by the exact outer authority or refused
  before any I/O. It is never located by walking past the unknown wrapper to a
  more capable inner value.
- **Why it matters** [MT] names this as an explicit blocker: the current unscoped
  existence walk "is a blocker that must become exact-outer/explicitly forwarded
  or fail closed"; [EXT] repeats it as a capability rule.
- **Acceptance** A test double that implements the base capability and *also*
  privately implements the unscoped effect is placed inside the chain behind an
  opaque wrapper. A discovery attempt must not reach it. The double records every
  call; the recorded call count for the unscoped effect is zero, and the caller
  receives a refusal rather than a narrowed-away success.

## UC-6 The root applies tenancy to some repositories and forgets one  [edge]
- **Given** an application with several tenant-owned resources, one of which the
  root never wrapped.
- **When** business code reads that resource under a valid scope.
- **Then** the unprotected repository is detectable — either it refuses because
  the resource is declared tenant-owned and has no strategy bound, or a
  composition-time completeness check fails the build.
- **Why it matters** This is the most common real-world tenancy breach and it is
  invisible to a test suite that only tests the resources someone remembered to
  test.
- **Acceptance** A fixture declares three tenant-owned resources and wires two.
  The third's first use, or the composition, fails with a stable refusal naming
  the *category* of omission (not the tenant). A control case wires all three and
  succeeds, so the test cannot pass vacuously.

## UC-7 Two independently selected extensions are composed in both orders  [edge]
- **Given** tenancy and one unrelated extension applied to the same base seam.
- **When** the same scenario runs with tenancy outermost and again with tenancy
  innermost.
- **Then** the *refusal* outcome is identical in both orders: an invalid scope
  produces zero tenant side effect either way. Ordering may change what the other
  extension observes, but never whether narrowing happens.
- **Why it matters** [EXT] requires conformance tests to "cover both orders where
  order affects capability preservation". An extension whose safety depends on
  being listed first is not safe.
- **Acceptance** The scenario matrix runs {valid, absent, forged, stale,
  inactive} × {tenancy-outer, tenancy-inner}. Side-effect counts recorded by an
  instrumented base capability are zero for all four invalid cases in both
  orders. Neither production module imports the other, verified by a source-level
  import check.

---

**Group B — Trust and scope establishment  (A3, A7; mode E1)**

## UC-8 The request path turns a verified identity into a verified scope  [happy]
- **Given** ingress has authenticated a principal and verified that it is a
  member of the tenant it is asking to act for.
- **When** ingress asks the trust authority for a scope for that principal and
  tenant reference.
- **Then** it receives an opaque, immutable scope carrying a stable tenant
  reference, the current lifecycle/binding epoch and the minimum capability the
  selected topology needs — and nothing else — which it places explicitly into
  the request context.
- **Why it matters** [MT]: "authentication verifies identity/membership before
  the tenancy resolver is called; the tenancy module imports no JWT or router
  implementation." The extension must never be the thing that decides who you
  are.
- **Acceptance** The scope value's observable surface is inspected: it exposes no
  DSN, database name, bucket, prefix, raw claim, header, carrier or telemetry
  label. Attempting to mutate it after receipt has no effect on a copy already
  handed to another goroutine — proven by a race-enabled concurrent read while a
  mutation is attempted. The extension's own dependency graph contains no token,
  router or transport package.

## UC-9 The trust authority issues a binding with lifecycle state and epoch  [happy]
- **Given** a control plane that owns tenant lifecycle state and, under
  database-per-tenant, the mapping to a datasource.
- **When** it answers a resolution request.
- **Then** the answer carries both the current lifecycle state and the current
  binding epoch, and the data plane admits the request only if the state is on
  the explicitly compatible list.
- **Why it matters** [MT]: "Data-plane resolution admits only an explicit
  compatible state." Compatibility is a whitelist; anything else is refusal.
- **Acceptance** A fake authority is driven through every enumerated state plus
  one state string the data plane has never seen. Admission occurs only for
  states on the declared compatible list. The unknown state produces a refusal,
  not a default-admit and not a panic.

## UC-10 A request arrives with no scope at all  [edge]
- **Given** a context with no scope — a forgotten middleware, a route mounted
  outside the ingress chain, an internal call constructed from a background
  context.
- **When** business code performs any tenant-owned read or write.
- **Then** the call fails with a stable "no scope" refusal, before any statement,
  object operation, cache read, queue write or transaction begins.
- **Why it matters** [MT]: "no package-level current tenant and no default
  tenant"; "missing… scope produces no tenant SQL, object, event, audit or job
  effect." A default tenant is the single most dangerous convenience possible
  here.
- **Acceptance** The base capability underneath is a recording double; after the
  refusal its recorded call count is zero and no transaction was opened. The test
  runs for every supported verb, including read-only ones. A control case with a
  valid scope shows a non-zero call count, so the assertion is not vacuous.

## UC-11 A caller manufactures a scope value directly  [edge]
- **Given** application code that composes a scope literal, zero value, or a copy
  reconstructed from fields it read somewhere.
- **When** it places that value in context and performs tenant-owned work.
- **Then** the value either does not compile in a production build, or is
  rejected as untrusted before any effect.
- **Why it matters** [MT] definition of done item 3: "verified scope cannot be
  manufactured from raw production input or defaulted."
- **Acceptance** A fixture attempts, in separate compilation units: (a) a
  composite literal of the scope type, (b) its zero value, (c) a struct with the
  same field layout converted across. (a) and (c) must fail to compile in a
  production build; (b) must be refused at use with the untrusted-scope kind and
  a recorded side-effect count of zero. The test-only constructor used by
  fixtures must be unavailable to that same production build (UC-92).

## UC-12 The caller supplies a tenant hint it controls  [edge]
- **Given** a request whose path, header, query parameter or self-asserted claim
  names a tenant the authenticated principal is not a member of.
- **When** ingress resolves a scope.
- **Then** the trust authority refuses; no scope is produced; nothing downstream
  ever sees a scope for the named tenant.
- **Why it matters** This is the textbook horizontal-privilege escalation. The
  hint is *input*, never authority; [MT] places membership verification strictly
  before resolution.
- **Acceptance** A fixture drives the full cross-product {principal of A,
  principal of B, unauthenticated} × {asks for A, asks for B, asks for
  nonexistent}. Only the two diagonal member cases produce a scope. The refusal
  for "member of A asks for B" is indistinguishable at the transport edge from
  "asks for nonexistent" — the response body, status and timing class carry no
  evidence that B exists.

## UC-13 A scope is well-formed but its epoch has moved  [edge]
- **Given** a scope minted before a restore, migration cutover, credential
  rotation or generation change, held by a long-lived request, a cached
  authorisation object or a resumed worker.
- **When** it is used.
- **Then** the operation is refused with a stale-scope kind and no side effect;
  the caller must re-resolve.
- **Why it matters** [MT] lists "stale" alongside absent and forged as a
  must-fail-before-effect case, and requires background work to "recheck current
  lifecycle/epoch rather than trusting request context or payload text".
- **Acceptance** A fixture obtains a scope, advances the authority's epoch, then
  performs each supported verb. Every verb refuses with the stale kind; the
  recording base capability shows zero calls. Re-resolution produces a fresh
  scope that succeeds — proving the refusal is about staleness, not about being
  broken.

## UC-14 A scope from a different deployment or environment is presented  [edge]
- **Given** a scope value serialised out of a staging deployment, or persisted
  from a previous incarnation of the same service, replayed into production.
- **When** it is used.
- **Then** it is refused as untrusted; deployment identity is part of what makes
  a scope verified.
- **Why it matters** A scope that is portable across deployments is a bearer
  token with no expiry. Restore-into-a-clone and copy-prod-to-staging are ordinary
  operational events.
- **Acceptance** Two trust authorities configured as distinct deployments each
  mint a scope for the same tenant reference. Cross-use is refused in both
  directions with the untrusted kind and zero side effects.

## UC-15 The tenant is in a non-active lifecycle state  [edge]
- **Given** a tenant that is provisioning, suspended, migrating or deleting.
- **When** ordinary data-plane work is attempted under an otherwise valid scope.
- **Then** it is refused with a lifecycle-class refusal, before any effect, and
  the refusal distinguishes *class* (retryable-later versus terminal) without
  disclosing the tenant.
- **Why it matters** [MT] enumerates these as control-plane states the data plane
  "must respect"; a suspended tenant that can still write is a billing, legal and
  security problem simultaneously.
- **Acceptance** Parametrised over every non-active state and every supported
  verb: zero recorded side effects, refusal kind matches the state's declared
  class, and the message contains no tenant reference. An active control case
  succeeds for the same verbs.

## UC-16 The lifecycle state is one the data plane does not recognise  [edge]
- **Given** the control plane introduces a new state — say, *quarantined* — that
  this build of the data plane predates.
- **When** resolution returns it.
- **Then** the data plane refuses. Unknown means closed.
- **Why it matters** Fail-open on unknown states means every future control-plane
  feature silently degrades every deployed data plane. [MT] admits "only an
  explicit compatible state".
- **Acceptance** A fake authority returns a state token absent from the compatible
  list. Result is refusal with zero side effects, not admission and not a crash.
  Adding the state to the compatible list flips the outcome — proving the check is
  the list and not an accident of parsing.

## UC-17 A scope is verified for one topology but the seam implements the other  [edge]
- **Given** a deployment migrating from shared-row to database-per-tenant (or a
  misconfiguration), where a scope carries a row-narrowing capability but the
  bound seam expects a source selection, or vice versa.
- **When** work is attempted.
- **Then** the mismatch is refused as incompatible, before effect. There is no
  best-effort coercion and no "fall back to the other topology".
- **Why it matters** [MT] requires an "incompatible" scope to fail before tenant
  side effects, and both topologies live in one extension — which is exactly why
  a mismatch is reachable.
- **Acceptance** A fixture crosses each topology's scope with each topology's
  strategy. The two matching pairs succeed; the two crossed pairs refuse with the
  incompatible kind and zero side effects.

## UC-18 Suspension lands while a request is in flight  [edge]
- **Given** a request that resolved a valid active scope, then the operator
  suspends the tenant while the request is mid-transaction.
- **When** the request continues.
- **Then** the outcome is one of two declared behaviours and never a third: either
  the in-flight unit of work completes atomically under the scope it validated,
  or it is refused and rolled back entirely. Partial application is forbidden.
- **Why it matters** [MT] requires a rehearsed "suspend race" among operational
  proofs. The dangerous answer is "half the writes landed".
- **Acceptance** A fixture opens a unit of work, performs write 1, triggers
  suspension, performs write 2, commits. Afterwards the database contains either
  both writes or neither, never one. The declared behaviour is stated in the
  published profile and the test asserts that declared behaviour specifically.

---

**Group C — Tenant-owned data plane, shared-row topology  (A2; mode E1)**

## UC-19 Business code reads an owned row by identifier  [happy]
- **Given** a valid active scope in context and a row owned by that tenant.
- **When** business code fetches by identifier, naming no tenant anywhere.
- **Then** the row is returned.
- **Why it matters** [MT]'s closing promise: "business code receives an already
  bound capability". If business code has to remember the tenant, every author
  eventually forgets.
- **Acceptance** The business-code call site contains no tenant parameter and no
  ownership predicate — verified by the fixture's source being compiled unchanged
  against a non-tenant build (UC-1). The statement actually issued carries an
  ownership constraint, verified by a statement-recording double.

## UC-20 Business code lists, filters, sorts and paginates within scope  [happy]
- **Given** rows for tenants A and B satisfying the same user-supplied filter,
  and a scope for A.
- **When** business code lists with that filter, an ordering and a page size.
- **Then** only A's rows are returned, in the requested order, and the page
  metadata (total, next cursor, has-more) reflects only A's rows.
- **Why it matters** Pagination metadata is a leak channel that narrowing of the
  *rows* does not close: a correct page of rows with a total of 4 000 discloses
  the size of B's data.
- **Acceptance** Seed A with 3 rows and B with 400 matching the same filter.
  Assert returned rows are A's three **and** the reported total is 3. A control
  case with narrowing disabled reports 403 and thereby proves the assertion is
  meaningful.

## UC-21 Business code counts, checks existence and aggregates within scope  [happy]
- **Given** the same seeded data.
- **When** business code counts, asks existence by identifier, or aggregates
  (sum, min, max, group-by).
- **Then** every answer is computed over A's partition alone.
- **Why it matters** [MT]'s shared-row profile requires "zero foreign
  count/existence leak" as named negative evidence.
- **Acceptance** For every aggregate verb the seam supports, the result over a
  mixed dataset equals the result over an A-only dataset computed independently.
  Group-by results contain no group key that exists only in B.

## UC-22 A create derives ownership from the scope  [happy]
- **Given** a valid scope and a new entity value.
- **When** business code creates it without setting any ownership field.
- **Then** the persisted row is owned by the scope's tenant.
- **Why it matters** [MT]: "Ownership is derived or validated on create and
  immutable for ordinary mutations."
- **Acceptance** The persisted row's ownership column is read back directly (not
  through the narrowed seam) and equals the scope's tenant. A create under B's
  scope with a byte-identical input value yields a row owned by B.

## UC-23 Business code updates, saves, upserts, deletes and restores an owned row  [happy]
- **Given** a valid scope and an owned row.
- **When** each mutating verb the seam supports is exercised.
- **Then** each affects exactly the intended owned row and nothing else.
- **Why it matters** The write matrix is where narrowing is most often partial:
  authors narrow `get` and forget `upsert` and `restore`.
- **Acceptance** For every mutating verb: the affected-row count is exactly 1;
  a byte-identical operation issued under B's scope against A's row affects 0
  rows and reports the same outcome as operating on a nonexistent row.

## UC-24 A relation or preload is traversed within scope  [happy]
- **Given** an owned parent with children, and a foreign parent with children.
- **When** business code loads the parent with its relation eagerly, and also
  navigates lazily.
- **Then** only the owned parent's children are materialised.
- **Why it matters** [MT]'s supported matrix explicitly includes
  "relation/preload"; a scope that stops at the root is the classic silent hole.
- **Acceptance** Both eager and lazy paths are exercised, at depth ≥ 2 and across
  a many-to-many join. A control subtest with the relation's narrowing removed
  shows the foreign children *do* appear, so the positive test cannot pass by
  accident of the fixture.

## UC-25 A unit of work spans several owned resources atomically  [happy]
- **Given** a valid scope and two tenant-owned resources.
- **When** business code performs writes to both inside one unit of work.
- **Then** both writes are narrowed to the same tenant, run on the same
  source/transaction, and commit or roll back together.
- **Why it matters** [MT] forbids "a tenancy-owned cross-subsystem UoW or late
  datasource switch"; the extension must ride the application's transaction, not
  invent one.
- **Acceptance** A failure injected on the second write leaves the first
  unobservable after rollback, verified by a direct read outside the seam. Both
  statements are attributed to a single transaction handle by a recording double.

## UC-26 Two tenants hold equal resource identifiers  [edge]
- **Given** tenants A and B each own a row whose primary identifier is the same
  value (natural keys, per-tenant sequences, imported legacy IDs).
- **When** business code under A's scope reads, updates, deletes, restores,
  upserts, counts and existence-checks that identifier.
- **Then** A's row is affected in every case and B's row is never read, returned,
  modified, counted or revealed.
- **Why it matters** [MT] names this as minimum negative evidence for both
  topologies: "Equal IDs in A/B" and "Two real databases containing equal
  resource IDs are the minimum integration fixture."
- **Acceptance** After the whole verb matrix has run under A's scope, B's row is
  byte-identical to its seeded state, checked by a direct read. Additionally,
  each verb's result under A is identical to the result it would give if B's row
  did not exist at all.

## UC-27 A create omits ownership, or supplies a foreign owner  [edge]
- **Given** an input value whose ownership field is unset, zero, or explicitly set
  to another tenant.
- **When** business code creates it under A's scope.
- **Then** unset/zero is filled from the scope; a foreign value is refused before
  the insert, never silently overwritten *and* never accepted.
- **Why it matters** Silent overwrite hides a client bug; acceptance is a breach.
  [MT] admits both "derived or validated" — the distinction must be declared per
  resource, not decided implicitly.
- **Acceptance** Three subtests. Unset → row owned by A. Foreign → refusal with
  an ownership-conflict kind and zero rows inserted, verified by a direct count.
  Matching-own-tenant → accepted. The chosen policy (derive vs validate) is
  declared per resource in the profile and the test asserts the declared one.

## UC-28 A mutation attempts to move a row between tenants  [edge]
- **Given** an owned row and an update whose payload changes the ownership field.
- **When** the update is applied through any mutating verb, including
  partial-update, upsert and bulk forms.
- **Then** the ownership change is refused before execution. Ownership is
  immutable for ordinary mutations.
- **Why it matters** [MT]: ownership is "immutable for ordinary mutations". A
  tenant move is an operator procedure (UC-67), not a field assignment.
- **Acceptance** For every mutating verb, an ownership-changing payload produces a
  refusal and the row's ownership is unchanged on direct read. The refusal is
  distinct from a validation failure so callers can tell the two apart. The
  operator move procedure, which *is* allowed to change ownership, goes through a
  separately named capability the request path cannot reach.

## UC-29 A bulk update or delete is issued with no scope predicate  [edge]
- **Given** business code that issues a bulk mutation whose criteria the
  application supplied and which contains no ownership constraint.
- **When** it reaches the seam.
- **Then** it is either narrowed by the strategy or refused before execution. It
  is never executed as written.
- **Why it matters** An unscoped bulk delete is the single highest-blast-radius
  operation in the system: one statement destroys every tenant.
- **Acceptance** Seed A and B. Issue a bulk delete with criteria matching all rows
  under A's scope. Assert B's row count is unchanged. Then issue the same bulk
  delete with the strategy configured to *refuse* rather than narrow, and assert
  zero rows deleted anywhere and a refusal returned. Both admitted behaviours are
  tested; the profile declares which is advertised.

## UC-30 A relation or preload could reach another tenant's rows  [edge]
- **Given** a relation whose foreign key can legitimately point across tenants
  because a shared reference table exists, or because data was imported before
  tenancy, or because an attacker crafted it.
- **When** business code traverses it under A's scope.
- **Then** either the traversal is narrowed at the relation as well as the root,
  or the relation is declared cross-tenant-by-design and the profile lists it as
  such. Never an unnarrowed silent traversal.
- **Why it matters** This is the failure the framework's own history warns about —
  "a scope that stopped at a preload". It survives every root-level test.
- **Acceptance** Seed a child row owned by B but referenced from an A parent.
  Traversal under A's scope returns no B data (or returns exactly the declared
  shared reference, if the relation is declared shared). A control subtest with
  the relation's narrowing removed shows the B child appearing.

## UC-31 A count or existence check leaks the existence of a foreign row  [edge]
- **Given** an identifier that exists but is owned by B.
- **When** business code under A's scope calls the by-identifier get, the
  existence check, the count with a filter that matches only B's row, or any
  verb that surfaces "already exists".
- **Then** every one of them answers exactly as it would if that identifier had
  never existed anywhere.
- **Why it matters** [MT]: "zero foreign count/existence leak". Existence
  disclosure is enough to enumerate a competitor's customer list.
- **Acceptance** For each verb, run the scenario twice: once with B's row present,
  once with the database containing no such row at all. The two runs must be
  indistinguishable in returned value, error kind, error message and any emitted
  signal. A byte-comparison of the responses is the assertion.

## UC-32 An aggregate spans a foreign partition  [edge]
- **Given** an aggregate — sum, average, max, distinct-count, group-by, window —
  over a filter that would match rows of both tenants.
- **When** it runs under A's scope.
- **Then** the result is computed over A's rows only, including the group-by key
  set and any "having" evaluation.
- **Why it matters** An aggregate is a low-bandwidth but perfectly usable leak:
  a max, a distinct count and a group-by key set together reconstruct a lot.
- **Acceptance** The aggregate result over the mixed dataset equals the same
  aggregate over an A-only dataset. Group-by key sets are compared as sets, not
  just cardinalities.

## UC-33 An upsert collides with a globally unique key owned by another tenant  [edge]
- **Given** a unique constraint that is global rather than composite with
  ownership, and B already holds the conflicting value.
- **When** A upserts that value.
- **Then** either the constraint is composite with ownership so no collision
  exists, or the collision is reported as a constraint failure that does not
  disclose that the conflicting row belongs to another tenant, and the upsert
  does not update B's row.
- **Why it matters** [MT] M1 requires "composite database constraints". An upsert
  that resolves to B's row is a cross-tenant write dressed as a normal operation.
- **Acceptance** Direct read confirms B's row is untouched. The error surfaced to
  A is identical in kind and text to the one produced when the conflicting row is
  A's own — verified by running both scenarios and comparing. A schema check
  asserts that every unique constraint on a tenant-owned table includes the
  ownership column, or is explicitly listed as intentionally global in the
  profile.

## UC-34 A soft-deleted foreign row is restored  [edge]
- **Given** B owns a soft-deleted row.
- **When** A issues restore for that identifier.
- **Then** the restore affects zero rows and reports the same outcome as
  restoring an identifier that never existed.
- **Why it matters** Restore is a mutating verb that authors routinely leave out
  of the narrowing matrix because it "only undoes something".
- **Acceptance** B's row remains soft-deleted on direct read; A's result is
  byte-identical to the nonexistent-identifier case.

## UC-35 A pagination cursor minted under another tenant's scope is replayed  [edge]
- **Given** a cursor or continuation token obtained by B.
- **When** A submits it.
- **Then** it is refused, or it is evaluated strictly within A's partition so that
  it can reveal nothing about B — and the profile declares which.
- **Why it matters** Cursors frequently encode a row position, sort key values or
  an offset. An opaque token is not a secure one.
- **Acceptance** A cursor from B's listing, replayed by A, returns only A's rows
  and its decoded content (if inspectable) never influences which of A's rows are
  returned in a way that reveals B's sort-key values. The negative control shows
  that without the check, B's boundary values shape A's page.

## UC-36 A raw or escape-hatch query path bypasses narrowing  [edge]
- **Given** business code that reaches a lower-level query facility directly —
  raw SQL, a driver handle, a criteria object built outside the seam.
- **When** it executes tenant-touching work.
- **Then** either the path is unreachable from business code by construction, or
  it is explicitly declared unsupported in the profile and the deployment relies
  on a second layer (database-enforced predicates, UC-38) — never on the author's
  memory.
- **Why it matters** [MT]: defence in depth "never replaces application narrowing
  or turns an unscoped raw query into a supported path." The honest answer is a
  named exclusion, not a claim of coverage.
- **Acceptance** The published profile enumerates every escape hatch reachable
  from the seam's public surface. A check compares that enumeration against the
  surface and fails when the surface grows a new one. If a second layer is
  claimed, UC-38's evidence must also be green.

## UC-37 An integrity fault message names a foreign row  [edge]
- **Given** a write that violates a constraint whose conflicting row belongs to
  another tenant, and an error pipeline that enriches constraint failures with
  the conflicting values or the offending row's fields.
- **When** the error reaches the caller.
- **Then** it carries no value, key or field belonging to the foreign row.
- **Why it matters** Error enrichment is a leak path that no query-level test
  touches, and enriched constraint errors are precisely the feature a CRUD
  framework is proud of.
- **Acceptance** The rendered error for the foreign-conflict case is compared,
  field by field, against the rendered error for an own-conflict case with values
  scrubbed. Any field present in one and absent in the other is a finding. A
  sentinel value planted only in B's row must not appear anywhere in A's error,
  its cause chain, or any signal emitted while producing it.

## UC-38 Database-enforced defence in depth is on, and a pooled session leaks  [edge]
- **Given** the deployment enables the optional database-level predicate, which
  is configured through a transaction-local setting, and connections are pooled.
- **When** a connection used for tenant A is returned to the pool and reused for
  tenant B, including after an error, a rollback, a cancelled context and a
  timeout.
- **Then** B never observes A's setting, and A's setting never survives into any
  subsequent use.
- **Why it matters** [MT]'s hardening profile requires "Pooled A then B; ordinary
  app role; owner/BYPASSRLS check; transaction-local reset; app predicate still
  active".
- **Acceptance** A live-database test runs A then B on a pool of size 1, with the
  four abnormal terminations interleaved, and asserts B's visible row set is B's
  alone. A second assertion checks the session variable is empty at the start of
  B's transaction. A third asserts the application role is not the table owner and
  does not bypass the predicate.

## UC-39 Database-enforced defence is on but application narrowing was removed  [edge]
- **Given** someone removes or misconfigures the application-level narrowing,
  believing the database layer is sufficient.
- **When** the conformance suite runs.
- **Then** the suite fails — the database layer is defence in depth, not the
  primary control, and its presence must not make the primary control's absence
  invisible.
- **Why it matters** [MT] is explicit: the optional hardening "never replaces
  application narrowing". A silently-redundant control decays into the only
  control.
- **Acceptance** A mutation test: disable application narrowing in a fixture with
  database enforcement on, and assert that at least one conformance test fails.
  If none fail, the suite is measuring the database and not the extension, which
  is itself the finding.

---

**Group D — Database-per-tenant topology  (A1, A2; mode E1)**

## UC-40 A verified scope resolves to exactly one datasource before work starts  [happy]
- **Given** a binding resolver and two real tenant databases.
- **When** a request under A's scope obtains a repository or transaction.
- **Then** exactly one datasource — A's — is selected before the value is handed
  over, and every statement of that request runs on it.
- **Why it matters** [MT]: "The second release resolves a verified scope to one
  caller-owned root datasource capability."
- **Acceptance** A statement-recording wrapper on both datasources shows all of
  A's statements on A's source and zero on B's. Requests for A and B run
  concurrently under `-race` and the per-source statement attribution stays
  clean.

## UC-41 Nested operations remain on the selected source  [happy]
- **Given** a request that opens a unit of work and calls into several layers,
  each obtaining a repository from context.
- **When** those layers execute.
- **Then** all of them use the source selected at the outermost boundary; none
  re-resolves.
- **Why it matters** [MT]: "nested operations remain on the selected source". A
  re-resolution mid-request is a window in which the binding could change.
- **Acceptance** The resolver is instrumented; for a request with N nested
  repository acquisitions the resolver is called exactly once. Forcing the
  resolver to return a *different* source on a second call must not change the
  request's behaviour, proving nothing re-resolves.

## UC-42 The datasource mapping is missing  [edge]
- **Given** a verified scope for a tenant with no binding recorded — a
  provisioning race, an incomplete migration, a wiped cache with a control-plane
  outage behind it.
- **When** work is attempted.
- **Then** it is refused with an unmapped kind. There is no default source, no
  last-used source, no "the one from the previous request", no shared source.
- **Why it matters** [MT]: "a missing/inactive/stale/incompatible mapping has no
  default or last-used fallback." A fallback here means writes land in someone
  else's database.
- **Acceptance** A fixture serves one request for A successfully, then requests
  for a tenant with no binding. The second refuses; the recording wrapper on A's
  source shows no additional statements. Repeat with the resolver returning a
  transient error rather than "not found" — still refusal, still zero statements.

## UC-43 The datasource mapping is wrong  [edge]
- **Given** a resolver that, through corruption, an operator error or a cache
  bug, returns B's datasource for A's scope.
- **When** work is attempted.
- **Then** the selection is fenced: the datasource itself is checked to be the one
  the scope names, and a mismatch refuses before any statement.
- **Why it matters** [MT] requires "wrong-database fencing" as mandatory evidence.
  Without a fence, the resolver becomes a single point of total compromise.
- **Acceptance** A fixture forces the resolver to return the wrong source. The
  first tenant-touching operation refuses with a fencing kind and B's database is
  unmodified on direct inspection. The fence is proven to be a real check by a
  control in which the resolver returns the correct source and the same code path
  succeeds.

## UC-44 The resolved database has an incompatible schema generation  [edge]
- **Given** a tenant database that was not migrated with the cohort and sits at
  an older (or newer) schema generation than this build expects.
- **When** work is attempted against it.
- **Then** it is refused as incompatible before any statement that assumes the
  expected schema, and the refusal is distinguishable from an ordinary outage so
  operators can route it to the migration runbook.
- **Why it matters** [MT] requires a "schema capability" fence, and lists partial
  migration as an operational rehearsal.
- **Acceptance** Two live databases at different generations. Requests to the
  lagging one refuse with the incompatible kind; requests to the current one
  succeed. The refusal carries no database name, DSN or schema identifier
  (INV-25). A control confirms the check reads the actual database, not a
  configuration file: mutating the database's recorded generation flips the
  outcome.

## UC-45 A late datasource switch is attempted inside an open unit of work  [edge]
- **Given** an open unit of work bound to A's source.
- **When** code inside it changes the scope in context — by re-entering ingress
  logic, by a helper that resolves a different tenant, or by a cohort job's inner
  loop — and then performs work.
- **Then** the operation is refused. The unit of work's tenant and source are
  fixed at its start.
- **Why it matters** [MT] forbids a "late datasource switch". A transaction that
  changes tenant halfway has no meaningful atomicity or authorisation story.
- **Acceptance** A fixture opens a unit of work under A, swaps the context scope
  to B, and writes. The write refuses with a pinning kind; B's database is
  untouched; A's transaction can still be committed or rolled back cleanly. The
  same test under shared-row topology refuses equally — pinning is not a
  database-per-tenant-only property.

## UC-46 The binding cache serves a stale mapping after rotation  [edge]
- **Given** cached bindings and a credential rotation, endpoint change or
  failover on the control-plane side.
- **When** a request arrives after the rotation but before natural cache expiry.
- **Then** the stale binding is not used: either the epoch check catches it and
  the request re-resolves, or the request refuses. It never connects with revoked
  credentials or to a decommissioned endpoint and then silently succeeds against
  a stale replica.
- **Why it matters** [MT] lists "rotation" and "eviction" as mandatory evidence
  and ties binding validity to epoch.
- **Acceptance** A fixture caches a binding, rotates on the authority side
  (advancing the epoch), and issues a request. The request either re-resolves —
  proven by a resolver call count of ≥1 after rotation — or refuses. It never
  uses the cached value. A control without rotation shows a resolver call count
  of 0, proving the cache is real and the test is measuring invalidation.

## UC-47 Connection pools are exhausted under many tenants  [edge]
- **Given** far more active tenants than the process's connection budget allows
  (thousands of tenants, tens of connections).
- **When** load arrives spread across them.
- **Then** total resource use stays within the declared bound; requests that
  cannot be served are refused or queued within a declared bound and time limit;
  the process does not exhaust file descriptors, exceed the database's connection
  limit, or deadlock.
- **Why it matters** [MT] requires "Pool/cache limits, borrower lifetime,
  rotation, eviction" as mandatory evidence. Unbounded per-tenant pools are how
  database-per-tenant deployments fall over at customer number 200.
- **Acceptance** A soak fixture with N tenants ≫ budget: observed peak connection
  count ≤ the declared bound; every request terminates with success or a
  declared refusal within the declared time limit; no deadlock under `-race`; the
  refusal kind is distinguishable from a tenancy-authorisation refusal so
  operators do not chase a security ghost during a capacity incident.

## UC-48 Eviction reclaims a datasource that still has a live borrower  [edge]
- **Given** a bounded binding/pool cache under pressure, and a long-running
  request or transaction holding a datasource.
- **When** the cache evicts that entry to make room.
- **Then** the live borrower keeps a valid, still-open capability until it
  finishes, and the evicted entry is never handed to a different tenant while in
  use. Nothing is closed underneath an in-flight transaction.
- **Why it matters** Eviction under load is exactly when a use-after-free style
  cross-tenant handoff would occur, and exactly when nobody is watching.
- **Acceptance** A fixture pins a borrower, drives eviction, and starts work for
  a different tenant that reuses the freed slot. Under `-race`, the pinned
  borrower's statements all land on its original database and commit; the new
  tenant's statements land on its own. A recording layer asserts no handle object
  is observed under two distinct tenant references.

## UC-49 One tenant's datasource is down  [edge]
- **Given** tenant A's database is unreachable.
- **When** requests arrive for A and for B.
- **Then** A's requests fail with an availability kind (not an authorisation
  kind), B's requests are unaffected, and A's failures do not consume B's share of
  the connection budget or block B's requests.
- **Why it matters** Shared fate across tenants is both an availability bug and an
  information leak (B can infer A's outage). [MT] requires outage behaviour as
  evidence for the topology.
- **Acceptance** With A's database stopped, B's success rate and latency
  distribution are statistically indistinguishable from the baseline where A is
  healthy. A's errors carry an availability kind. Connection attempts to A are
  bounded (circuit-broken or capped), verified by a connection-attempt counter.

## UC-50 Two real databases contain equal resource identifiers  [edge]
- **Given** A's and B's databases each containing a row with the same identifier
  and different content.
- **When** the full supported verb matrix runs under A's scope.
- **Then** every verb touches A's database only, and B's row is untouched and
  never returned.
- **Why it matters** [MT]: "Two real databases containing equal resource IDs are
  the minimum integration fixture." This is the test that catches a source
  selection that silently defaulted.
- **Acceptance** Direct inspection of B's database after the matrix shows it
  byte-identical to its seed. Every returned value matches A's content. The test
  uses two genuinely separate databases, not two schemas or two prefixes.

---

**Group E — Durable work  (A4; mode E2)**

## UC-51 A producer captures tenant authority at enqueue  [happy]
- **Given** a request under a valid active scope that enqueues durable work.
- **When** the work is recorded.
- **Then** the durable record carries a partition and a protected reference to
  the tenant plus provenance and the epoch at capture time — enough to
  *re-resolve* later, never enough to *be* the authority.
- **Why it matters** [MT]: the durable boundary's values are "partition +
  protected token + provenance + epoch", and "background work restores authority
  deliberately".
- **Acceptance** The persisted record is inspected: it contains no DSN, bucket,
  credential, raw claim or plaintext that would let a worker act without asking
  the authority. Feeding a hand-edited record with a different tenant reference to
  the worker changes nothing about which tenant it acts for beyond what the
  authority confirms (UC-56).

## UC-52 A worker re-derives authority at execution  [happy]
- **Given** a durable record enqueued earlier, executed possibly hours later,
  possibly in a different process after a restart.
- **When** the worker picks it up.
- **Then** the worker asks the trust authority to re-resolve the tenant reference,
  validates the current lifecycle state and the current epoch, and only then is
  the handler given a tenant-bound context.
- **Why it matters** [MT] requires a combined fixture proving "a durable reference
  is re-resolved against current lifecycle/epoch before the handler is given a
  tenant-bound context."
- **Acceptance** An instrumented authority records a re-resolution call for every
  execution, including retries. A handler that asserts it received a bound
  context passes; a handler that receives an unbound context is a failure. With
  the authority made to fail, the handler is never entered — verified by a handler
  invocation counter of zero.

## UC-53 A job enqueued by a tenant is resumed after that tenant is suspended  [edge]
- **Given** work enqueued while A was active; A is suspended before execution.
- **When** the worker picks the job up.
- **Then** the handler is not entered, no tenant side effect occurs, and the job
  is parked in a declared terminal-or-retryable state consistent with the
  suspension's class — never silently dropped and never executed.
- **Why it matters** [MT]: "deleted/suspended/stale work has zero side effect."
  Executing a suspended tenant's work is the "we kept billing them after they
  asked us to stop" bug.
- **Acceptance** Handler invocation count is zero; every downstream recording
  double (rows, objects, cache, outbound messages) shows zero effects; the job's
  final state matches the declared policy and is observable to an operator.

## UC-54 A job is resumed after the tenant is deleted  [edge]
- **Given** work enqueued before deletion; the tenant is now deleted and its data
  removed.
- **When** the worker picks it up.
- **Then** the handler is not entered, no data is recreated, and the job reaches
  a terminal state. Deletion must not be undone by a queue.
- **Why it matters** A job that recreates rows for a deleted tenant defeats the
  deletion guarantee that was reported to a regulator.
- **Acceptance** After the worker drains, the tenant's data footprint (rows,
  objects, cache entries) is empty on direct inspection. Handler invocation count
  is zero. The job does not retry forever — its terminal state is asserted.

## UC-55 A job is resumed after the epoch moved  [edge]
- **Given** work enqueued at epoch N; a restore or migration cutover advanced the
  binding to epoch N+1.
- **When** the worker picks it up.
- **Then** the mismatch is detected and the declared policy applies: either
  re-resolve to the current generation and proceed *if the job definition says it
  is generation-agnostic*, or refuse. Never proceed against a stale generation.
- **Why it matters** A job written against pre-restore state, applied to
  post-restore data, corrupts the restored generation quietly.
- **Acceptance** Both policies are exercised. For the refuse policy, handler
  invocation count is zero and the job's state is observable. For the
  re-resolve policy, the recorded target generation equals the current one, proven
  by writing a sentinel row and reading it in the current generation only.

## UC-56 A job payload's text claims a tenant  [edge]
- **Given** a durable record whose *payload* (as opposed to its protected
  reference) contains a tenant identifier, either because a developer put it
  there or because an attacker with queue write access forged it.
- **When** the worker executes it.
- **Then** the payload's claim has no effect on which tenant the work is bound to.
- **Why it matters** [MT]: forbidden shortcut is "raw payload routing"; the
  worker must not trust "payload text".
- **Acceptance** Two records are executed: one whose payload names A and whose
  protected reference resolves to A; one whose payload names B and whose
  protected reference still resolves to A. Both bind to A. Recording doubles show
  zero effects in B. A record whose protected reference is tampered with is
  refused, not resolved to whatever the payload said.

## UC-57 A job is retried after partial application  [edge]
- **Given** a job that performed some tenant-owned writes and then failed, and is
  retried, possibly after the tenant's lifecycle state changed in between.
- **When** the retry runs.
- **Then** authority is re-derived from scratch for the retry (not reused from the
  first attempt), and the job's effects are idempotent or explicitly
  transactional per attempt, so a retry never doubles a side effect nor lands a
  second half against a different generation than the first half.
- **Why it matters** [MT] requires "rollback, retry" scenarios across advertised
  compositions.
- **Acceptance** A fixture fails a job midway, suspends the tenant, then retries.
  The retry does not execute. Resuming the tenant and retrying again produces a
  final state equal to a single successful execution — asserted by comparing
  against a control run that never failed.

## UC-58 A worker process has no tenancy wiring but the job requires a tenant partition  [edge]
- **Given** a deployment where the worker binary's composition root omitted the
  tenancy capture/restore wiring, while the job definition declares a required
  tenant partition.
- **When** the worker starts, or picks up such a job.
- **Then** it refuses at start-up (preferred) or refuses the job before entering
  the handler. It never executes tenant-owned work with no tenant.
- **Why it matters** [MT] notes base APIs stay intact when tenancy is removed
  "subject to a job definition's explicit tenant-partition requirement" — that
  subject clause must be enforced, not documented.
- **Acceptance** A fixture builds a worker without the wiring and registers a
  partition-requiring job. Start-up fails with a wiring refusal naming the job
  category. If the design defers to execution instead, handler invocation count is
  zero and the refusal is observable to an operator.

---

**Group F — Object namespace and cache  (A1, A2; modes E1, E2)**

## UC-59 An object namespace is derived from the verified scope  [happy]
- **Given** a verified scope and business code that stores and retrieves objects
  under logical names it chooses freely.
- **When** it writes and reads an object.
- **Then** the object lands in a namespace bounded to that tenant, business code
  never constructs a prefix, and no credential or bucket identity is present in
  the scope.
- **Why it matters** [MT]: the permitted role is "Map verified scope to a bounded
  object namespace"; the forbidden shortcut is "raw prefix concatenation or
  provider credentials in scope".
- **Acceptance** Two tenants store an object under the identical logical name;
  each reads back its own content. The underlying physical keys are recorded and
  asserted distinct. The scope's surface is asserted to contain no bucket, prefix
  or credential.

## UC-60 A cache partition is derived from the verified scope  [happy]
- **Given** a verified scope and business code caching values under logical keys.
- **When** it reads and writes the cache.
- **Then** the entries live in a partition bounded to that tenant, and the cache
  never infers tenancy from arbitrary context values.
- **Why it matters** [MT]'s forbidden shortcut here is "Cache inferring
  tenant/principal from arbitrary context values" — inference is a guess, and a
  guess that is right 99.9% of the time is a breach 0.1% of the time.
- **Acceptance** The cache is exercised with a context that contains a
  tenant-shaped value the partition mapper was not given. The partition is
  unchanged, proving no inference. Two tenants using an identical logical key
  retrieve their own values.

## UC-61 Object keys collide or alias across tenants  [edge]
- **Given** tenant references or logical names whose concatenation is ambiguous —
  the classic pair where one tenant's prefix is a prefix of another's, or a
  logical name contains the separator character, or one tenant's name is another
  tenant's name plus a path segment.
- **When** objects are written and listed.
- **Then** no tenant can read, overwrite or enumerate another's object, and the
  prefix relation between namespaces is never containment.
- **Why it matters** Prefix confusion is the object-store equivalent of a missing
  WHERE clause and is invisible to any test that uses tenant identifiers `a` and
  `b`.
- **Acceptance** A property test over adversarially chosen tenant references and
  logical names (including separator characters, empty components, unicode
  normalisation variants and case variants) asserts the physical-key mapping is
  injective and that no produced namespace is a prefix of another. Round-tripping
  any (tenant, logical name) pair returns exactly its own content.

## UC-62 A listing escapes the namespace  [edge]
- **Given** business code listing objects with a caller-supplied logical prefix,
  possibly containing traversal segments or an empty string.
- **When** it lists.
- **Then** results are confined to the tenant's namespace; an empty or
  traversing prefix lists only that tenant's objects, never the bucket root.
- **Why it matters** List is the enumeration primitive; a leaky list gives a full
  inventory of every tenant in one call.
- **Acceptance** With objects seeded for A and B, A's list with prefixes `""`,
  `"/"`, `"../"` and the raw tenant separator returns only A's objects. A control
  with the namespace mapping removed returns B's objects too.

## UC-63 A cache key collides or a shared in-flight is served across tenants  [edge]
- **Given** two tenants requesting the same logical key concurrently, and a cache
  that coalesces concurrent misses into one shared computation.
- **When** they race.
- **Then** each receives a value computed under its own scope. The coalescing
  never joins two different partitions into one flight.
- **Why it matters** Request coalescing is a correctness optimisation that becomes
  a cross-tenant read the instant the flight key omits the partition. It fires
  only under concurrency, so it never shows up in a serial test.
- **Acceptance** A `-race` test with a deliberately slow loader: A and B request
  the same logical key simultaneously; the loader records the scope it was called
  with; it must be invoked once per partition (not once total), and each caller
  receives its own tenant's value. Repeat with hundreds of goroutines across many
  tenants and assert zero cross-assignments.

## UC-64 A cache invalidation crosses partitions  [edge]
- **Given** business code invalidating a logical key, or a bulk/pattern
  invalidation, or a "clear all" during a deployment.
- **When** it runs under A's scope.
- **Then** only A's partition is affected. A bulk or pattern invalidation with no
  partition is refused or narrowed, never executed as written.
- **Why it matters** Cross-tenant invalidation is a self-inflicted availability
  event and, worse, its blast radius is the same shape as the read leak — if
  invalidation can reach B, so could a read.
- **Acceptance** After A's invalidation, B's entries are still present, verified
  by direct partition inspection. An unpartitioned pattern invalidation returns a
  refusal and B's entries survive.

## UC-65 A cached or stored value survives a lifecycle transition  [edge]
- **Given** cached values and stored objects for A, after which A is suspended,
  restored to a new generation, or deleted.
- **When** subsequent work occurs.
- **Then** suspension makes the data unreachable through the seam; restore into a
  new generation makes pre-restore cached values unreachable (the partition is
  generation-scoped); deletion makes them irrecoverable.
- **Why it matters** A cache that outlives a restore serves pre-restore data as
  if it were current — a silent, undetectable data-integrity failure.
- **Acceptance** For each transition: a value written before the transition is
  read after it. Suspension → refusal. Restore → miss, then a load from the
  current generation. Deletion → miss and the underlying entry is gone on direct
  inspection. A control shows the value is readable when no transition occurs,
  so the assertions are not passing because the cache was empty.

---

**Group G — Operations and lifecycle  (A5; mode E3)**

## UC-66 An operator suspends a tenant  [happy]
- **Given** an active tenant with in-flight and queued work.
- **When** the operator executes suspension.
- **Then** within a declared bound, no new data-plane work is admitted for that
  tenant in any subsystem — rows, objects, cache, durable work — and existing
  in-flight work resolves per UC-18's declared behaviour.
- **Why it matters** Suspension that only covers the HTTP path leaves the queue
  running, which is the part that keeps writing.
- **Acceptance** After suspension, one probe per subsystem attempts an operation
  and all refuse. The queue is drained and handler invocation count for that
  tenant is zero. The declared bound (immediate, or ≤ one resolution TTL) is
  measured and asserted.

## UC-67 An operator migrates a tenant with a fenced cutover  [happy]
- **Given** a tenant to be moved — between databases, or from shared-row into its
  own database, or between generations.
- **When** the operator runs the procedure.
- **Then** there is exactly one active generation at all times; during cutover the
  source is fenced so it accepts no further writes; after cutover all resolution
  yields the new binding at a new epoch; scopes minted before the cutover are
  stale (UC-13).
- **Why it matters** [MT]: lifecycle procedures are "topology-specific procedures
  with one active generation and a fenced cutover; they are not hidden inside
  first-request resolution."
- **Acceptance** A fixture writes continuously during a migration. After cutover,
  the new location contains every acknowledged write and the old location has
  received none after the fence. No acknowledged write is lost. The number of
  distinct generations observed as writable at any instant is 1, asserted by a
  concurrent observer.

## UC-68 An operator restores a tenant to a new generation  [happy]
- **Given** a backup and a tenant needing restoration.
- **When** the operator restores.
- **Then** the restored data is exposed as a new generation with a new epoch;
  every pre-restore scope, cached binding, cached value and durable reference is
  stale; the data plane serves the restored generation consistently across rows,
  objects and cache.
- **Why it matters** A restore that leaves one subsystem on the old generation
  produces the worst kind of corruption: internally inconsistent, plausible data.
- **Acceptance** Sentinel data planted post-backup is absent after restore in
  *every* subsystem. A scope held across the restore refuses. A cached object and
  a cached value from before the restore are not served.

## UC-69 An operator deletes a tenant  [happy]
- **Given** a tenant whose deletion has been requested and whose retention window
  has elapsed, with no legal hold.
- **When** the operator runs deletion.
- **Then** rows, objects, cache entries and pending durable work for that tenant
  are removed or rendered permanently unresolvable, and afterwards no resolution
  path returns data for that tenant reference.
- **Why it matters** Deletion is a legal commitment, and it is made across
  subsystems that were built at different times by different people.
- **Acceptance** A per-subsystem probe after deletion finds nothing: direct row
  count 0, object listing empty, cache partition empty, queued work terminal.
  A previously valid scope refuses. The check enumerates subsystems from the
  published profile so a newly added subsystem forces the check to grow.

## UC-70 An operator places a tenant under legal hold  [happy]
- **Given** a tenant under legal hold.
- **When** any deletion — tenant deletion, retention expiry, per-row hard delete,
  object lifecycle expiry — is attempted.
- **Then** it is refused, and the refusal is durable and observable.
- **Why it matters** [MT] names "legal hold/deletion" as a rehearsed operational
  procedure. Silent hold bypass is a courtroom problem, not an engineering one.
- **Acceptance** Every deletion path enumerated by the profile is attempted under
  hold and each refuses. Direct inspection shows the data intact. Removing the
  hold makes the same paths succeed, proving the hold is what refused.

## UC-71 A migration is only partially completed  [edge]
- **Given** a migration interrupted midway — process killed, network partition,
  operator abort — with some data moved and some not.
- **When** requests arrive for that tenant.
- **Then** the tenant is in a non-active lifecycle state, requests refuse, and no
  request is served from a half-migrated generation. Resumption or rollback is an
  explicit operator action.
- **Why it matters** [MT] requires "Partial migration, restore/move, suspend race,
  hold/delete and cross-tenant grant expiry remain explicit."
- **Acceptance** A fixture kills the migration at several distinct points. At each
  point, data-plane requests refuse with the migrating class; direct inspection
  shows no partially-visible state; resuming completes correctly and rolling back
  restores the pre-migration generation, with the acknowledged-write set intact in
  both outcomes.

## UC-72 A migration cohort partially fails  [edge]
- **Given** a batch migrating many tenants, some of which fail.
- **When** the batch finishes.
- **Then** each tenant's outcome is individually recorded and individually
  consistent; a failure for one tenant leaves the others correct; failed tenants
  are in a state an operator can act on, and the schema-generation fence (UC-44)
  keeps lagging tenants from being served by code that expects the new schema.
- **Why it matters** [MT] M5: "Rehearse migration cohort failure."
- **Acceptance** A cohort of N tenants with injected failures on a subset. Per
  tenant, the final state is exactly one of {migrated, unchanged}, never mixed.
  Successful tenants serve traffic; failed ones refuse with the appropriate class.

## UC-73 A restore races in-flight writes  [edge]
- **Given** a restore beginning while writes are in flight for the same tenant.
- **When** both proceed.
- **Then** the restore fences writes before it begins reconstructing, and no write
  acknowledged after the fence exists in the restored generation, and no
  acknowledged write is silently lost — acknowledgement stops at the fence.
- **Why it matters** A restore that races writes produces a generation that
  contains some post-fence writes and not others, undetectably.
- **Acceptance** A writer records every acknowledgement. After the restore, the
  restored generation contains exactly the acknowledgements up to the fence and
  none after. No acknowledgement exists for a write that is absent.

## UC-74 A deleted tenant's reference is recycled  [edge]
- **Given** a tenant reference that is reused for a new organisation, or reissued
  after deletion, or reused in a restored-from-backup control plane.
- **When** an old scope, old durable record, old cached entry or old object
  namespace for that reference is encountered.
- **Then** none of them resolve to the new tenant. Generation, not just reference,
  identifies a tenant's data.
- **Why it matters** Reference recycling turns every stale artefact in the system
  into a cross-tenant leak at once, and it happens in exactly the deployments
  where references are human-chosen slugs.
- **Acceptance** A fixture deletes A, creates a new tenant with the same
  reference, then presents an old scope, an old durable record, an old cache key
  and an old object namespace. All four fail to resolve to the new tenant, proven
  by planting sentinel content under the old generation and asserting it is not
  reachable by the new one.

## UC-75 Two provisioning attempts race for the same tenant  [edge]
- **Given** concurrent provisioning requests for the same new tenant reference —
  a retried onboarding webhook, a double-clicked operator action.
- **When** they race.
- **Then** exactly one binding/generation results; no tenant ends up with two
  datasources, two ownership generations, or a half-provisioned state that the
  data plane will admit.
- **Why it matters** A tenant with two bindings is a tenant whose writes split
  between two databases depending on which cache entry a process holds.
- **Acceptance** A `-race` fixture runs K concurrent provisions. Exactly one
  succeeds and K−1 receive a stable already-exists or in-progress result. The
  resulting binding count is 1. During the race, data-plane requests for that
  tenant refuse (provisioning is not an admitted state).

## UC-76 A legal hold conflicts with a scheduled deletion  [edge]
- **Given** a tenant with a hold and an automated retention job that would delete
  it.
- **When** the job runs.
- **Then** the hold wins; deletion is refused and the refusal is recorded where an
  operator will see it rather than being retried silently forever.
- **Why it matters** Automated retention and manual holds are written by different
  teams and are the classic pair that disagrees.
- **Acceptance** The retention job's run leaves the held tenant's data intact on
  direct inspection and produces an operator-visible outcome. Non-held tenants in
  the same run are deleted — proving the job ran and the hold is what stopped it.

## UC-77 Row and object generations disagree after a restore or move  [edge]
- **Given** a restore or tenant move that returns rows to generation N but leaves
  objects (or cache, or queued work) at generation N+1, or vice versa.
- **When** the tenant is served.
- **Then** the mismatch is detected and the tenant is not admitted until the
  generations agree.
- **Why it matters** A row referencing an object that no longer matches it is data
  corruption that looks like an application bug for months.
- **Acceptance** A fixture sets subsystem generations deliberately out of step.
  Data-plane requests refuse with an incompatible-generation kind until they are
  reconciled. A control with matching generations serves normally.

---

**Group H — Cross-tenant work  (A6; mode E4)**

## UC-78 A cohort job runs under an explicit bounded grant  [happy]
- **Given** an administrative job — a billing rollup — that must read across many
  tenants, and a grant naming a purpose, a cohort and an expiry.
- **When** the job runs.
- **Then** it iterates tenants, obtaining a per-tenant bound capability for each,
  and each tenant's work is separately scoped, separately admitted against
  lifecycle, and separately recorded.
- **Why it matters** [MT]: "cross-tenant work uses a separate bounded
  purpose/grant/cohort capability, never `nil` scope or an arbitrary slice of
  tenant IDs."
- **Acceptance** The job's access to tenant data outside its cohort is refused —
  proven by adding a tenant outside the cohort and asserting a refusal, not an
  empty result. The grant's purpose bounds which operations are permitted:
  attempting a write under a read-purpose grant refuses.

## UC-79 Cross-tenant work is attempted with an absent or nil scope  [edge]
- **Given** a job or report that simply omits the scope, expecting "no scope
  means all tenants".
- **When** it runs.
- **Then** it refuses. Absent is never a wildcard.
- **Why it matters** [MT] is explicit that nil scope is not the cross-tenant
  mechanism. "No filter means everything" is the default behaviour of every query
  builder ever written, which is precisely why it must be refused loudly.
- **Acceptance** The unscoped job returns a refusal and its recording doubles show
  zero rows read. The same job with a grant succeeds — proving the refusal is
  about the missing grant, not about the job.

## UC-80 A grant expires mid-run  [edge]
- **Given** a long-running cohort job and a grant that expires partway through.
- **When** expiry occurs.
- **Then** further per-tenant work refuses; already-completed per-tenant work
  remains complete and recorded; the job reports partial completion explicitly
  rather than appearing to have finished.
- **Why it matters** [MT] M5 rehearses "cross-tenant grant expiry".
- **Acceptance** A fixture expires the grant after k of N tenants. Exactly k
  tenants have effects; N−k have none; the job's reported outcome names partial
  completion and identifies the resumption point in a protected (not generic)
  channel.

## UC-81 A grant is broader than the work needs  [edge]
- **Given** a grant covering all tenants for a job that in practice touches three.
- **When** the job runs.
- **Then** this is at minimum detectable: the effective cohort actually touched is
  recorded so the grant can be narrowed, and over-broad grants do not pass
  unnoticed as an operational default.
- **Why it matters** Grants that are always "all tenants, no expiry" are a nil
  scope with extra ceremony, and that is exactly what they decay into.
- **Acceptance** The job's run records the set of tenants actually accessed
  through a protected channel. A conformance check compares granted cohort size
  against accessed cohort size and flags a configured ratio. A grant with no
  expiry, or a wildcard cohort, is refused at issuance unless explicitly marked as
  such in the profile.

## UC-82 A cross-tenant operation is partially applied and must roll back or retry  [edge]
- **Given** a mutating cohort operation that fails after applying to some tenants.
- **When** rollback or retry is attempted.
- **Then** per-tenant atomicity holds — each tenant is fully applied or fully
  unapplied — and the retry resumes only the unapplied tenants, without
  reapplying the applied ones.
- **Why it matters** [MT] M5: "partial cross-tenant work" is a required rehearsal;
  cross-tenant atomicity is impossible in database-per-tenant, so the contract has
  to be per-tenant atomicity plus a resumable record, and that contract must be
  stated rather than assumed.
- **Acceptance** Fail after k tenants. Each of the N tenants is in exactly one of
  {applied, unapplied}. Retry brings the total to N with each tenant applied
  exactly once — asserted by a per-tenant effect counter that must equal 1.

## UC-83 A cohort member becomes non-active mid-run  [edge]
- **Given** a cohort job in progress when one member is suspended or enters
  migration.
- **When** the job reaches that member.
- **Then** that member is skipped with a recorded outcome; the rest of the cohort
  is unaffected; the job does not abort the whole run, nor silently include the
  suspended tenant.
- **Why it matters** Lifecycle transitions are routine and a cohort job that either
  crashes or ignores them will be "fixed" by removing the lifecycle check.
- **Acceptance** With one member suspended mid-run, effects for that member are
  zero, effects for the others are complete, and the job's outcome record
  distinguishes skipped from failed from succeeded.

---

**Group I — Privacy and telemetry  (A8)**

## UC-84 Dashboards are built from a bounded vocabulary  [happy]
- **Given** an on-call engineer building alerts on tenancy behaviour.
- **When** they read the generic signals the extension emits or contributes.
- **Then** they can see topology mode, operation, component, lifecycle class and
  a safe outcome — enough to alert on "resolution refusals are spiking" — and
  nothing that identifies a tenant.
- **Why it matters** [MT]: "Generic signals may contain only closed bounded values
  such as topology mode, operation, component, lifecycle class and safe outcome."
- **Acceptance** The complete set of emitted label keys and values is enumerable
  ahead of time and finite. A test asserts the emitted vocabulary is a subset of
  the declared closed set, and fails when a new value appears.

## UC-85 A protected workflow can identify a tenant for diagnosis  [happy]
- **Given** an incident requiring an engineer to know *which* tenant failed.
- **When** they use the protected audit/control-plane workflow.
- **Then** they get identifiable diagnosis, through a separately governed and
  access-controlled channel.
- **Why it matters** [MT]: "Protected audit/control-plane workflows own
  identifiable diagnosis." Without this, someone will add the tenant ID to a
  metric label during an outage and it will never be removed.
- **Acceptance** The protected channel is demonstrably distinct from the generic
  one: they have different sinks and different access controls, asserted by a
  fixture that reads the generic sink and finds no identifying content while the
  protected sink has it.

## UC-86 Tenant identity reaches a generic signal  [edge]
- **Given** any code path — success, refusal, panic recovery, retry, an error
  wrapped by an outer layer.
- **When** generic signals are emitted.
- **Then** none carries tenant reference, stable tenant hash, database, schema,
  DSN, user, pool key, bucket, prefix, object reference, raw host, header, claim,
  or wrapped error text.
- **Why it matters** [MT]'s privacy contract enumerates precisely this forbidden
  set. A stable hash is on the list because a stable hash is a stable identifier.
- **Acceptance** A sentinel scan: every fixture tenant reference, database name,
  bucket, object key and header value is a unique unguessable token; after
  running the entire conformance matrix — including every failure path — the
  captured generic signal stream is searched for any sentinel and must contain
  zero. The scan includes error strings, cause chains, span names, span
  attributes, metric labels, log fields and any structured payload. The scan runs
  as an ordinary test, not a manual review.

## UC-87 Signal cardinality grows with tenant count  [edge]
- **Given** thousands of distinct tenants, database names and object keys driven
  through the system.
- **When** generic signals are collected.
- **Then** the number of distinct label combinations stays bounded and independent
  of the number of tenants.
- **Why it matters** [MT]: "Every telemetry profile runs a sentinel scan and a
  cardinality test with thousands of distinct tenant inputs." Unbounded
  cardinality is both a privacy leak by reconstruction and a cost incident.
- **Acceptance** Run with 1 tenant and with 5 000 tenants. The distinct label-set
  count must be equal, or differ only by values from the declared closed
  vocabulary. Any growth proportional to tenant count fails.

## UC-88 A wrapped error carries a DSN, bucket or key  [edge]
- **Given** a driver, storage or control-plane error whose text embeds a
  connection string, host, bucket or object key, wrapped and propagated upward.
- **When** it is emitted to a generic signal or returned to a caller who logs it.
- **Then** the identifying text does not reach the generic signal. The extension's
  own refusal carries a stable kind and no wrapped provider text.
- **Why it matters** [MT] lists "wrapped error text" explicitly. Driver errors are
  the single most reliable way a DSN with a password reaches a log aggregator.
- **Acceptance** A fake driver returns an error containing a sentinel DSN. The
  sentinel appears in no generic signal. The caller-facing error is comparable
  against a stable sentinel kind, not by string matching, and its own rendered
  text is free of the sentinel.

## UC-89 The telemetry pipeline is down or disabled  [edge]
- **Given** telemetry configured to a dead endpoint, or disabled entirely, or
  configured as a no-op.
- **When** the full conformance matrix runs.
- **Then** every resolution, policy, routing and lifecycle outcome is identical to
  the run with telemetry healthy, and no operation blocks on telemetry.
- **Why it matters** [MT]: "OTel outage/no-op behaviour must not affect
  resolution, policy, routing or lifecycle results."
- **Acceptance** The matrix runs three times — healthy, disabled, dead endpoint —
  and the per-case outcome vectors are compared for equality. Latency in the dead
  case stays within a declared bound, proving nothing blocks on the exporter.

---

**Group J — Conformance and release  (A9; mode E6)**

## UC-90 A fixture constructs a scope explicitly for a test  [happy]
- **Given** a unit test needing a scope without a control plane.
- **When** it uses the test-only construction path.
- **Then** it gets a scope indistinguishable to the data plane from a real one, so
  tests exercise the real code path.
- **Why it matters** [MT] M1: "Implement opaque verified scope, lifecycle/epoch
  and test-only construction." Without it, tests will construct scopes some other
  way and that other way will become production's escape hatch.
- **Acceptance** A fixture-constructed scope succeeds through the same seam a
  production scope would, and a fixture-constructed scope with a stale epoch
  refuses exactly as a production stale scope does.

## UC-91 A published profile states its supported matrix  [happy]
- **Given** a release.
- **When** the profile is published.
- **Then** it names the topology, source/driver, the exact supported operation
  matrix, the admitted lifecycle states, the known exclusions and the evidence
  version.
- **Why it matters** [MT]: "Each advertised profile states its topology,
  source/driver, supported operation matrix, lifecycle states, known exclusions
  and evidence version." An unstated exclusion is read by consumers as coverage.
- **Acceptance** A check compares the profile's operation matrix against the
  seam's actual method inventory, and against which conformance tests are green.
  A method absent from the profile, or a profile entry with no green test, fails.

## UC-92 Test-only construction is reachable from production  [edge]
- **Given** an application that imports the extension normally.
- **When** it tries to reach the test-only scope constructor.
- **Then** it cannot — the constructor is unavailable in an ordinary production
  build.
- **Why it matters** A test-only forge that ships is the forged-scope
  vulnerability with a friendly name.
- **Acceptance** A production-build fixture that references the test-only
  constructor fails to compile. A test-build fixture referencing it compiles.
  Both are asserted, so the mechanism cannot silently stop working.

## UC-93 A profile advertises a capability whose evidence is not green  [edge]
- **Given** a release candidate claiming database-per-tenant support without the
  two-database, pool, epoch, migration and recovery evidence.
- **When** the release gate runs.
- **Then** it fails; the profile is narrowed to what is proven.
- **Why it matters** [MT] definition of done item 9: "the published profile names
  shared-row only unless the full two-database, pool, epoch, migration and
  recovery evidence has also passed." This is the rule that keeps the document
  from becoming marketing.
- **Acceptance** A gate maps every profile claim to a named required test. A claim
  whose test is skipped, absent or failing fails the gate. Deliberately skipping
  one required test flips the gate to red — proving the gate reads results and not
  a checklist.

---

## Invariants

Each is an always-true property of the running system. Each states what breaks
when it is false and how to falsify it.

### INV-1 No ambient tenant
- **Statement** At no point does any operation obtain a tenant from process-global
  state, a default, a last-used value, an environment variable or a fallback. The
  only source of a tenant for a data-plane operation is a verified scope passed
  explicitly for that operation.
- **Scope** Every subsystem, every entry mode, including start-up, background
  work, migrations and administrative tooling.
- **Violation consequence** A request that forgot its scope silently acts as
  whichever tenant was last active in that process — the highest-severity
  cross-tenant write, and one that appears only under concurrency.
- **How to check** Static: no package-level mutable tenant state exists (asserted
  by a source check plus a construction-time global-state probe, UC-1). Dynamic:
  every operation with no scope in context refuses (UC-10), including immediately
  after a successful operation for another tenant on the same goroutine and on a
  goroutine recycled from a pool.

### INV-2 Scope unforgeability
- **Statement** In a production build, a value that the data plane accepts as a
  verified scope exists only if the injected trust authority produced it.
- **Scope** All construction paths: literals, zero values, conversions from
  identically-shaped types, deserialisation, reflection, copying across a process
  boundary.
- **Violation consequence** Complete bypass of every other control; tenancy
  becomes advisory.
- **How to check** UC-11 and UC-92: literal/zero/converted construction is a
  compile error or a refusal; the test-only constructor is absent from production
  builds; a serialised scope from another deployment refuses (UC-14).

### INV-3 Scope immutability
- **Statement** A verified scope, once produced, never changes. Nothing widens,
  narrows, retargets or annotates it in place.
- **Scope** For the whole lifetime of the value, across goroutines.
- **Violation consequence** A scope mutated by one goroutine changes the tenant
  another goroutine is acting for, mid-operation.
- **How to check** A `-race` test with concurrent readers and an attempted
  mutation; the observed tenant per reader is stable. The scope's surface exposes
  no setter and no mutable reference reachable from it.

### INV-4 Scope minimality and opacity
- **Statement** A scope contains a stable tenant reference, a lifecycle/binding
  epoch and the minimum capability the selected topology needs. It contains no
  DSN, database name, bucket, prefix, credential, raw claim, header, carrier or
  telemetry label.
- **Scope** The value itself and anything transitively reachable from it.
- **Violation consequence** Every place a scope travels — logs, error text,
  durable records, telemetry — becomes a credential and topology disclosure.
- **How to check** A structural inspection of the value walks its reachable
  content and asserts each element is on the declared allow-list; a sentinel
  credential planted in the resolver's environment is not reachable from the
  scope (UC-8, UC-88).

### INV-5 Fail before effect
- **Statement** An absent, forged, stale, lifecycle-invalid, unmapped or
  topologically incompatible scope produces zero tenant side effects: no
  statement executed, no transaction opened, no object read or written, no cache
  entry read or written, no durable work enqueued, no outbound message sent.
- **Scope** Every subsystem the extension constrains, every verb, every entry
  mode.
- **Violation consequence** The refusal becomes cosmetic — the damage is done and
  only the response is denied.
- **How to check** A recording double under every subsystem seam; for each of the
  six invalid-scope classes × every supported verb, the recorded effect count is
  exactly zero. Control cases with valid scopes show non-zero counts so the
  assertion cannot pass vacuously.

### INV-6 Exactly one capability
- **Statement** A verified scope selects exactly one authorised data-plane
  capability — one narrowing predicate under shared-row, one datasource under
  database-per-tenant. Never zero-with-fallback, never more than one, never a
  union.
- **Scope** Selection, for the lifetime of a unit of work.
- **Violation consequence** Zero-with-fallback writes to the wrong place;
  more-than-one is a cross-tenant read by construction.
- **How to check** An instrumented selection layer asserts exactly one selection
  per unit of work (UC-3, UC-41), and that the selection is not made when
  resolution fails (UC-42).

### INV-7 Ownership derivation on create
- **Statement** Every tenant-owned row created through the seam is owned by the
  scope's tenant. Ownership is never taken from unvalidated caller input.
- **Scope** All creating verbs, including upsert's insert branch and bulk create.
- **Violation consequence** A client can plant rows in another tenant's data by
  setting a field.
- **How to check** UC-22 and UC-27: read the persisted ownership directly, not
  through the narrowed seam; a foreign ownership value refuses or is replaced per
  the declared policy, and never silently accepted.

### INV-8 Ownership immutability
- **Statement** No ordinary mutation changes a row's owning tenant.
- **Scope** Every mutating verb reachable from business code, including
  partial update, upsert's update branch, and bulk forms.
- **Violation consequence** A row is moved out of a tenant's data (destructive) or
  into it (a planted record).
- **How to check** UC-28: for every mutating verb, an ownership-changing payload
  refuses and direct read shows the ownership unchanged. The operator move
  procedure lives behind a capability business code cannot obtain.

### INV-9 Total verb coverage
- **Statement** Every verb the constrained seam exposes has an explicit
  forward/narrow/refuse decision. A verb with no decision does not exist.
- **Scope** All constrained seams: repository, object, cache, durable work.
- **Violation consequence** The next base-seam addition ships unprotected and
  nobody notices until it is used.
- **How to check** A generated method inventory compared against a recorded
  decision table; the build fails when the seam grows a method with no decision
  (UC-2). [EXT] requires exactly this: "method inventories and capability
  matrices fail when a base seam grows without a decorator decision."

### INV-10 Existence non-disclosure
- **Statement** For any identifier, filter or key owned by another tenant, the
  seam's observable behaviour is identical to the behaviour for something that
  does not exist anywhere.
- **Scope** Returned values, error kinds, error text, affected-row counts,
  aggregate results, pagination totals, constraint failures, and any signal
  emitted along the way.
- **Violation consequence** Enumeration of another tenant's identifiers, key
  space, customer names or data volume — a leak that needs no read access.
- **How to check** UC-31, UC-33, UC-37: every scenario is run twice, once with the
  foreign row present and once with it absent, and the two outcomes are compared
  for byte equality across value, error and signal.

### INV-11 Relation closure
- **Statement** No traversal starting from an in-scope row yields an out-of-scope
  row, at any depth, through any relation kind, eager or lazy — unless the
  relation is explicitly declared cross-tenant in the profile.
- **Scope** All relation and preload paths, including many-to-many and
  self-referencing relations.
- **Violation consequence** The scope stops at the root and every nested read is a
  leak — historically the exact failure this framework's decisions were written to
  prevent.
- **How to check** UC-24, UC-30: a fixture seeds a cross-owned reference at depth
  ≥ 2; traversal returns nothing foreign, with a control subtest proving the leak
  exists when narrowing is removed.

### INV-12 Affirmative bulk scoping
- **Statement** A bulk mutation whose criteria carry no ownership constraint is
  never executed as written.
- **Scope** Bulk update, bulk delete, bulk restore, pattern invalidation, bulk
  object deletion.
- **Violation consequence** One statement affects every tenant.
- **How to check** UC-29, UC-64: issue an unconstrained bulk mutation and assert
  the other tenant's data is unchanged (narrow policy) or that zero rows were
  affected anywhere (refuse policy). The chosen policy is declared per seam.

### INV-13 Unit-of-work pinning
- **Statement** A unit of work's tenant and source are fixed at its start and
  cannot change before it ends.
- **Scope** Both topologies. Under shared-row it pins the narrowing tenant; under
  database-per-tenant it pins the tenant and the datasource.
- **Violation consequence** A transaction that spans two tenants has no
  meaningful atomicity, authorisation or auditability.
- **How to check** UC-45: swapping the context scope inside an open unit of work
  and writing refuses; the resolver is called exactly once per unit of work
  (UC-41).

### INV-14 Durable reference is not authority
- **Statement** Nothing recorded in a durable artefact — partition, token,
  provenance, payload — is sufficient to act for a tenant. Authority is re-derived
  from the trust authority at execution time.
- **Scope** All durable work, all retries, all replays, all manual requeues.
- **Violation consequence** Queue write access becomes tenant impersonation, and
  every suspension and deletion is undone by the backlog.
- **How to check** UC-52, UC-56: an authority-call counter is ≥ 1 per execution
  including retries; a tampered payload changes nothing; a record whose protected
  reference does not verify refuses.

### INV-15 Lifecycle admission is a whitelist
- **Statement** Data-plane work is admitted only for lifecycle states on an
  explicit compatible list. Every other state, including unknown ones, refuses.
- **Scope** All entry modes, all subsystems, re-evaluated at durable execution
  time.
- **Violation consequence** Suspended tenants keep writing; deleted tenants get
  resurrected; new control-plane states silently degrade every deployed data
  plane.
- **How to check** UC-15, UC-16, UC-53, UC-54: every enumerated state plus one
  invented state, against every verb; only whitelisted states admit.

### INV-16 Epoch currency
- **Statement** A scope, binding or durable reference whose epoch differs from the
  authority's current epoch for that tenant is not used to perform work.
- **Scope** Request-bound work, cached bindings, cached values, durable work,
  long-running operations.
- **Violation consequence** Work lands in a superseded generation after a restore
  or migration — silent, plausible corruption.
- **How to check** UC-13, UC-46, UC-55, UC-65: advance the epoch and assert every
  holder of a pre-advance artefact refuses or re-resolves, with a control proving
  the artefact worked before the advance.

### INV-17 One active generation
- **Statement** At any instant a tenant has exactly one writable generation.
- **Scope** Migration, restore, move, provisioning.
- **Violation consequence** Split-brain: acknowledged writes land in a generation
  that is about to be discarded, and no one can tell which.
- **How to check** UC-67, UC-73: a concurrent observer samples the writable
  generation set throughout the procedure; its cardinality is always 1. Every
  acknowledged write is present in the surviving generation.

### INV-18 Namespace injectivity and non-containment
- **Statement** The mapping from (tenant generation, logical name) to a physical
  object key or cache key is injective, and no tenant's namespace is a prefix of,
  or otherwise contained in, another's.
- **Scope** Object storage and cache, including listing and pattern operations.
- **Violation consequence** One tenant overwrites, reads or enumerates another's
  data through ordinary use of an ordinary key.
- **How to check** UC-61, UC-62: a property test over adversarial references and
  names (separators, empty components, case and unicode variants) asserting
  injectivity and non-containment, plus a round-trip assertion per pair.

### INV-19 Partition isolation of shared work
- **Statement** A cached value, an in-flight coalesced computation, a connection's
  session state or any other shared resource is never observed by a tenant other
  than the one it was produced for.
- **Scope** Cache reads and coalescing, pooled connections and their session
  settings, any memoisation.
- **Violation consequence** A concurrency-only cross-tenant read that no serial
  test can find.
- **How to check** UC-38, UC-63: `-race` tests with a deliberately slow loader and
  a pool of size 1; loader invocation count equals the number of distinct
  partitions, not 1; a session setting is empty at the start of every transaction.

### INV-20 Explicit grant for cross-tenant work
- **Statement** Work touching more than one tenant occurs only under a grant
  naming purpose, cohort and expiry. An absent scope, a nil scope, an empty
  cohort or a bare list of tenant identifiers is never authority.
- **Scope** Reports, rollups, cohort migrations, admin tooling, exports.
- **Violation consequence** Every "just this once" admin script becomes an
  unaudited cross-tenant read path.
- **How to check** UC-78, UC-79, UC-81: unscoped attempts refuse with zero reads;
  out-of-cohort access refuses rather than returning empty; grants with no expiry
  or wildcard cohort are refused at issuance unless declared.

### INV-21 Per-tenant atomicity of cross-tenant work
- **Statement** In a multi-tenant operation, each tenant is either fully applied
  or fully unapplied; a retry applies each tenant exactly once.
- **Scope** Cohort jobs, cross-tenant migrations, bulk administrative operations.
- **Violation consequence** Half-applied tenants that no operator can distinguish
  from applied ones, and retries that double effects.
- **How to check** UC-82: per-tenant effect counters equal 1 after failure and
  retry; each tenant's state is in exactly one of {applied, unapplied} at every
  observation point.

### INV-22 Selection fencing
- **Statement** A resolved datasource is used only after being confirmed to be the
  one the scope names and to carry a compatible schema generation.
- **Scope** Database-per-tenant selection, including after cache hits, rotation
  and failover.
- **Violation consequence** A resolver bug or a corrupted cache entry becomes a
  total cross-tenant compromise with no second line of defence.
- **How to check** UC-43, UC-44: force a wrong source and a lagging generation;
  the first tenant-touching operation refuses and the wrong database is unmodified
  on direct inspection; a control with the correct source succeeds through the
  same path.

### INV-23 Bounded resources under tenant growth
- **Statement** Per-process resource use — connections, cached bindings, goroutines
  — stays within a declared bound regardless of how many tenants are active, and
  eviction never hands an in-use capability to a different tenant.
- **Scope** Datasource pools, binding caches, any per-tenant lazily created
  resource.
- **Violation consequence** Availability collapse at a customer count nobody
  tested, and a use-after-eviction cross-tenant handoff at exactly that moment.
- **How to check** UC-47, UC-48: a soak with tenants ≫ budget; peak observed usage
  ≤ bound; under `-race`, a pinned borrower's statements all land on its original
  source across an eviction.

### INV-24 Session hygiene
- **Statement** A connection returned to a pool carries no tenant-derived state.
- **Scope** All pooled connections, after commit, rollback, error, cancellation
  and timeout.
- **Violation consequence** The next tenant to borrow that connection inherits the
  previous tenant's database-level scoping.
- **How to check** UC-38: pool of size 1, A then B, with all four abnormal
  terminations interleaved; the session variable is empty at the start of B's
  transaction and B's visible rows are B's alone.

### INV-25 Generic signals carry no tenant identity
- **Statement** No generic telemetry signal — metric label, span name, span
  attribute, log field, event body or error text propagated into one — contains a
  tenant reference, a stable tenant hash, a database, schema, DSN or user, a pool
  key, a bucket, prefix or object reference, a raw host, header or claim, or
  wrapped provider error text.
- **Scope** Every code path, including refusals, retries, panics and recovery.
- **Violation consequence** The observability pipeline — the least access-
  controlled system in the deployment — becomes a tenant data store, and a
  "stable hash" makes it a cross-signal join key.
- **How to check** UC-86, UC-88: unguessable sentinels for every tenant
  reference, database name, bucket, key and header; after the full matrix, the
  captured generic stream contains zero sentinel occurrences. The scan runs as a
  test.

### INV-26 Bounded signal vocabulary
- **Statement** The set of distinct label values a generic signal can carry is
  closed, declared in advance and finite.
- **Scope** All generic signals contributed by the extension.
- **Violation consequence** An open vocabulary is a leak channel that a sentinel
  scan can miss, because the leaking value need not be one of the sentinels.
- **How to check** UC-84: the emitted vocabulary is asserted to be a subset of the
  declared closed set; a new value fails the test.

### INV-27 Cardinality independent of tenant count
- **Statement** The number of distinct label combinations produced does not grow
  with the number of tenants, databases, buckets or keys.
- **Scope** All generic signals.
- **Violation consequence** Both a cost incident and a reconstruction leak: a
  per-tenant time series identifies a tenant even without a label naming it.
- **How to check** UC-87: run at 1 tenant and 5 000 tenants; distinct label-set
  counts are equal.

### INV-28 Telemetry neutrality
- **Statement** Resolution, policy, routing and lifecycle outcomes are identical
  whether telemetry is healthy, disabled or failing, and no operation's
  correctness or completion depends on an exporter.
- **Scope** All entry modes.
- **Violation consequence** An observability outage becomes a tenancy outage, or
  worse, a tenancy *bypass* if a failed emission is treated as a failed check.
- **How to check** UC-89: three runs of the full matrix with equal outcome vectors
  and bounded latency in the dead-exporter case.

### INV-29 Identifiable diagnosis is separately governed
- **Statement** Tenant-identifying diagnostic content exists only in a protected
  channel with its own access control, distinct from the generic signal path.
- **Scope** Incident diagnosis, audit, control-plane records.
- **Violation consequence** Either operators cannot diagnose (and will paste the
  tenant ID into a metric during the next outage), or the generic path is used and
  INV-25 falls.
- **How to check** UC-85: the two channels have different sinks; a fixture reading
  the generic sink finds no identifying content while the protected sink has it.

### INV-30 Refusal identity without disclosure
- **Statement** Every refusal carries a stable, comparable kind — absent,
  untrusted, stale, lifecycle-inadmissible, unmapped, incompatible, pinned,
  grant-required, capacity — and no identifying detail. Refusals are compared by
  sentinel, never by string.
- **Scope** All refusals from the extension.
- **Violation consequence** Callers cannot distinguish "re-authenticate" from
  "retry later" from "this is broken", so they retry the wrong things; or the
  detail they need is supplied by leaking identity.
- **How to check** For each refusal class, a test asserts the sentinel comparison
  succeeds and that the rendered text contains no sentinel value from UC-86's
  planted set. A capacity refusal is asserted distinguishable from an
  authorisation refusal (UC-47).

### INV-31 Order-independent refusal
- **Statement** Whether an invalid scope is refused does not depend on the order
  in which extensions were composed.
- **Scope** Any chain containing tenancy and at least one other decorator.
- **Violation consequence** A composition-root reordering — a refactor nobody
  flags as security-relevant — silently disables narrowing.
- **How to check** UC-7: {valid, absent, forged, stale, inactive} × both orders;
  effect counts zero for every invalid case in both orders.

### INV-32 No executable effect tunnels through an unknown wrapper
- **Statement** An executable optional effect is satisfied by the exact outer
  authority or refused before I/O. Discovery never walks past an unknown wrapper
  to a more capable inner value. Navigation, identity and description may use a
  named bounded walk; execution may not.
- **Scope** Every optional capability on every constrained seam.
- **Violation consequence** Precisely the blocker [MT] names: an opaque tenancy
  wrapper exposing an inner unscoped effect, which is a narrowing bypass that
  compiles.
- **How to check** UC-5: an inner double privately implementing the unscoped
  effect records zero calls when discovery is attempted through an opaque
  wrapper; the caller receives a refusal.

### INV-33 Optionality of the extension
- **Statement** The base subsystems and every pre-existing module have no
  dependency on the tenancy extension and expose no type owned by it, and no
  extension imports another extension.
- **Scope** The module and import graph, evaluated outside any workspace overlay.
- **Violation consequence** Tenancy stops being optional, the package graph starts
  growing by intersection, and every consumer downloads it.
- **How to check** [MT]'s gate: isolated base-only, tenancy-only and
  multi-extension fixtures resolved without workspace overlay; the base-only graph
  contains no tenancy module; a source-level import check rejects every
  extension-to-extension edge; discovered modules contain no package whose
  identity is an intersection (UC-4).

### INV-34 No activation on import
- **Statement** Importing the extension or constructing its factories registers no
  global, discovers no application component, owns no unrequested lifecycle and
  activates no adapter the composition root did not ask for.
- **Scope** Package initialisation and factory construction.
- **Violation consequence** Behaviour that appears without being wired cannot be
  reasoned about, removed or tested in isolation — and a global tenant registry is
  explicitly forbidden.
- **How to check** UC-1: a probe enumerates process-global registration points
  before import, after import and after construction, and finds no new entry; no
  goroutine is started by construction.

### INV-35 Explicit propagation across boundaries
- **Statement** A scope never crosses a process or service boundary implicitly,
  and no inbound carrier — header, claim, payload field, queue message — is ever
  accepted as a scope. Each boundary re-derives from its own trust authority.
- **Scope** Outbound service calls, durable work, any serialisation.
- **Violation consequence** The scope becomes a bearer token that any caller who
  can set a header can mint (UC-12), or that any queue writer can forge (UC-56).
- **How to check** An attempt to serialise a scope fails or produces a value that
  does not deserialise into an accepted scope; an inbound carrier that looks like
  a scope is not accepted (UC-11, UC-14).

### INV-36 Deletion and hold finality
- **Statement** After deletion completes, no resolution path in any subsystem
  returns data for that tenant generation, and no subsequent work recreates it;
  while a legal hold is in force, no deletion path succeeds.
- **Scope** Rows, objects, cache, durable work, bindings, backups within the
  declared retention contract.
- **Violation consequence** A deletion commitment made to a regulator is undone by
  a queue, a cache or a retention job — and discovered by someone else.
- **How to check** UC-54, UC-69, UC-70, UC-76: a per-subsystem probe after
  deletion finds nothing, the subsystem list is generated from the published
  profile so a new subsystem forces the probe to grow, and every enumerated
  deletion path refuses under hold while non-held tenants in the same run are
  deleted.

---

## Declarative top-level DX

> Every identifier below is a placeholder invented for this document. None of
> them names anything in the tree. The point is the **shape** of what a caller
> asks for.

### What the composition root writes

```go
// One extension, selected once, ordered explicitly among other extensions.
tenancy, err := tenantExtension(tenantExtensionSpec{
    Trust:     controlPlaneTrust,   // injected: identity+ref -> scope, lifecycle, epoch
    Topology:  sharedRowTopology(), // or perTenantSourceTopology(bindings, budget)
    Lifecycle: admitStates(active), // whitelist; everything else refuses
})
if err != nil {
    return err // misconfiguration is an error at wiring, not a surprise at request 10 000
}

// Applied to the seams the application already had. Same shapes out as in.
widgets := repositoryChain(
    baseWidgets,
    tenancy.OwnedResource[Widget, WidgetID](ownedBy(widgetOwnerAttr)),
    otherExt.Resource[Widget, WidgetID](otherExtDeps),      // unrelated extension, unaware of tenancy
)

objects := objectChain(baseObjects, tenancy.ObjectNamespace())
values  := cacheChain(baseValues,  tenancy.CachePartition())

work, err := durableWork(durableWorkSpec{
    Sender:  backend,
    Catalog: catalog,
    Context: tenancy.DurableIdentity(), // capture at enqueue, re-derive at execution
})
```

### What the request path writes

```go
// The host has already authenticated and verified membership. Only then:
scope, err := tenancy.ScopeFor(ctx, verifiedPrincipal, requestedTenantRef)
if err != nil {
    return refusalToTransport(err) // one mapping, applied uniformly
}
ctx = tenancy.With(ctx, scope)
```

### What business code writes

```go
func (s *WidgetService) Place(ctx context.Context, in NewWidget) (Widget, error) {
    o, err := s.widgets.Create(ctx, in)   // ownership derived; no tenant argument
    if err != nil {
        return Widget{}, err
    }
    if err := s.objects.Put(ctx, "invoice.pdf", body); err != nil {  // namespace derived
        return Widget{}, err
    }
    return o, s.work.Enqueue(ctx, SendInvoice{WidgetID: o.ID})        // identity captured
}
```

### What a worker writes

```go
work.Handle(SendInvoice{}, func(ctx context.Context, m SendInvoice) error {
    // ctx is already bound: lifecycle and epoch were revalidated before entry.
    // If they had not validated, this function would not have been called.
    return svc.Send(ctx, m.WidgetID)
})
```

### What a cross-tenant job writes

```go
grant, err := tenancy.Grant(ctx, grantRequest{
    Purpose: monthlyBilling,     // bounds which operations are permitted
    Cohort:  activeBillableTenants,
    Until:   endOfWindow,
})
if err != nil {
    return err
}
return grant.EachTenant(ctx, func(ctx context.Context) error {
    // ctx is bound to exactly one cohort member; lifecycle checked per member
    return billing.Roll(ctx)
})
```

### What the caller must NOT have to do

- **Pass a tenant.** No tenant parameter appears in a business-code signature.
- **Write an ownership predicate.** No `WHERE owner = ?` in application code, ever
  — that is what the repository seam is for.
- **Choose a datasource, DSN, pool or connection.** Under database-per-tenant the
  caller obtains a repository and it is already the right one.
- **Build a key or a prefix.** Object and cache keys are logical; the physical
  key is the extension's business and its collision-freedom is an invariant.
- **Copy tenant identity into a job payload.** The durable boundary carries it,
  and the payload's claim is deliberately powerless.
- **Check lifecycle state.** Business code never asks "is this tenant suspended";
  a suspended tenant's operation refuses.
- **Remember composition order for correctness.** Order may change what other
  extensions observe; it never changes whether narrowing happens (INV-31).
- **Handle a nil or absent scope.** There is no nil-scope branch to write, because
  there is no nil-scope path that succeeds.
- **Distinguish "foreign" from "missing".** The seam does not offer that
  distinction, by design (INV-10).

### Extension points (the things a real deployment must supply)

| Point | What it supplies | Why it must be injected |
|---|---|---|
| Trust authority | Verified identity + tenant reference → scope, lifecycle state, epoch | It is the control plane; the extension must not import a control-plane SDK ([MT]: providers are separate adapter modules) |
| Lifecycle admission policy | Which states admit which operation classes | Deployments differ on whether, say, `migrating` admits reads |
| Ownership strategy, per resource | Which attribute carries ownership; derive-or-validate on create | [MT] admits both; the choice is per resource and must be declared |
| Binding resolver + source factory | Tenant generation → one datasource capability | The extension owns the contract and the bounds; the driver and secret store are the application's decision |
| Pool/cache budget and eviction policy | Bounds, borrower lifetime, rotation behaviour | [MT] makes these mandatory evidence, so they must be configurable and observable |
| Namespace and partition mappers | Scope → bounded object namespace / cache partition | The base subsystems own their inputs; tenancy only maps |
| Durable capture/restore | How identity is captured and re-derived | Must satisfy the durable boundary's contract without the boundary knowing tenancy exists |
| Grant issuance | Who may issue cross-tenant grants, with what purposes and expiry | Deployment policy, not a framework default |
| Result vocabulary / redaction | The closed set of values allowed in generic signals | INV-26; the set is declared, not discovered |

Explicitly **not** extension points: a way to disable narrowing; a way to supply
a default tenant; a way to construct a scope from raw input; a global registry of
repositories or tenants; a hook that observes another tenant's data.

### Failure surface

| Situation | Surface | What the caller can do |
|---|---|---|
| Misconfiguration at wiring: unknown topology, missing trust authority, tenant-owned resource with no ownership mapping, incompatible options | **Returned error at construction**, before the process serves traffic | Fix the wiring. This must not be deferred to the first request |
| Programmer error the type system should have caught: applying a strategy to a value that cannot support it, a nil required collaborator | **Panic at construction only** — never during a request | Fix the code. Nothing recovers from it in production |
| Absent, untrusted, stale, lifecycle-inadmissible, unmapped, incompatible, pinned, grant-required scope | **Returned refusal with a stable comparable kind**, no identifying detail | Map to a transport outcome by kind: re-authenticate, retry later, or refuse. The mapping is the application's, applied uniformly |
| Foreign row or key | **Same result as nonexistent** — no distinct error kind exists | Nothing. This is deliberate (INV-10) |
| Capacity: pool exhausted, budget reached | **Returned refusal with a capacity kind**, distinct from every authorisation kind | Shed load, retry with backoff, or scale. Do not page the security team |
| Datasource unavailable for one tenant | **Returned refusal with an availability kind** | Retry; other tenants are unaffected (UC-49) |
| Telemetry unavailable | **Nothing**. No error, no delay beyond a declared bound | Nothing (INV-28) |

Refusals are compared against exported sentinels, never by message text. No
refusal message contains a tenant reference, database, key or wrapped provider
text (INV-25, INV-30).

---

## Axes of variation

Each axis below carries evidence from a governing document that it genuinely
varies. An axis without such evidence is speculative, and a speculative axis is a
defect — it produces configuration nobody uses, tested by nobody, that then
constrains real design. The rejected list at the end is part of the deliverable.

| # | Axis | Values | Evidence |
|---|---|---|---|
| V1 | **Topology** | shared-row; database-per-tenant | [MT] devotes a section to each and states both are "strategies/factories inside that package"; the delivery plan gives them separate milestones (M1, M2) |
| V2 | **Database-enforced defence in depth** | absent; present | [MT]: "PostgreSQL RLS is optional defence in depth. If accepted, its transaction-local setting, application role, owner/BYPASSRLS assumptions and pool reset behaviour are part of the profile." Optional and profile-affecting is exactly an axis |
| V3 | **Admitted lifecycle states** | the compatible subset of {provisioning, active, suspended, migrating, deleting/deleted, restored-generation} | [MT]: "Data-plane resolution admits only an explicit compatible state" — *explicit* implies the set is declared per deployment, not fixed |
| V4 | **Constrained subsystem breadth** | rows only; + durable work; + objects; + cache | [MT] M3: "Add storage/cache factories only where their root-owned inputs are sufficient and a real consumer exists" — so a valid deployment has rows and jobs but no object/cache adapter |
| V5 | **Ownership policy per resource** | derive on create; validate on create | [MT]: "Ownership is derived **or** validated on create" — the disjunction is the axis. It must be declared per resource so UC-27 knows what to assert |
| V6 | **Bulk-mutation policy** | narrow; refuse | [MT] includes "admitted bulk effects" in the supported matrix — *admitted* implies some deployments admit none. Both behaviours are conformance-tested (UC-29) |
| V7 | **Binding/pool bounds and eviction policy** | budget, borrower lifetime, rotation, eviction strategy | [MT]: "Pool/cache limits, borrower lifetime, rotation, eviction, schema capability and wrong-database fencing are mandatory evidence" — mandatory evidence for a value implies the value is chosen |
| V8 | **Composition order with other extensions** | tenancy outermost; tenancy inner | [EXT]: "conformance tests cover both orders where order affects capability preservation"; [MT] M1 requires proof "with opaque neighbours and both middleware orders" |
| V9 | **Cross-tenant grant shape** | purpose; cohort; expiry | [MT]: "cross-tenant work uses a separate bounded purpose/grant/cohort capability" — three named dimensions of the same capability |
| V10 | **Trust authority implementation** | in-process control plane; external control-plane service; fixture | [MT]: "A direct pgx client, cloud secret manager, control-plane SDK … is a separately selected backend/provider adapter" — the authority is injected precisely because it varies |
| V11 | **Advertised profile scope** | shared-row only; shared-row + database-per-tenant | [MT] definition of done item 9 makes this a release-time variable: "the published profile names shared-row only unless the full two-database, pool, epoch, migration and recovery evidence has also passed" |
| V12 | **Schema generation per datasource** | current; lagging; ahead | [MT]: "schema capability and wrong-database fencing are mandatory evidence"; M5 rehearses "migration cohort failure", which produces lagging generations by construction |

### Rejected as speculative (listed so they are not silently reintroduced)

- **Schema-per-tenant as a third topology.** [MT] names two topologies and only
  two. "schema" appears in the governing text only in the privacy forbidden-value
  list. Adding a third strategy on that basis would be inventing a requirement.
  Raised instead as an open question (Q4).
- **Tenant hierarchies / sub-tenants / reseller nesting.** Nothing in either
  document implies a tenant is anything but flat. A hierarchy changes the meaning
  of "exactly one authorised capability" and must not be assumed.
- **Per-tenant encryption keys / BYOK.** Not mentioned. It would change the scope's
  minimal content (INV-4), which is frozen at M0, so it must be a deliberate
  decision rather than an axis.
- **Read-replica or geo routing per tenant.** Not mentioned. It would break INV-13
  pinning and INV-16 epoch currency in ways that need their own design.
- **Per-tenant rate limits / quotas / noisy-neighbour control.** An availability
  concern, not a tenancy-correctness one. Adding it here would smuggle a
  scheduling subsystem into a security boundary.
- **Per-tenant feature flags or configuration.** Application concern. Making the
  scope a configuration carrier directly contradicts INV-4.

---

## Out of scope

1. **Authentication and membership verification.** The host does this before the
   trust authority is called. [MT]: "the tenancy module imports no JWT or router
   implementation."
2. **The control plane itself.** Tenant onboarding, billing, plan management, the
   admin UI, the tenant registry's own storage. The extension consumes an injected
   contract; it does not implement one.
3. **Provider SDKs.** Drivers, secret managers, cloud clients, migration products.
   [MT]: each is "a separately selected backend/provider adapter" with its own
   module, added only for a real consumer.
4. **Transport and router bindings.** Extracting a tenant hint from a request is
   the host's job; the extension never sees a carrier.
5. **Telemetry backends.** The extension exposes a bounded result vocabulary; it
   does not adapt an exporter. [MT] forbids a combined telemetry × tenancy package
   in either direction.
6. **Production coupling to audit, event sourcing, i18n or object-store providers.**
   [MT]: these are "tests, not imports"; they are wired in an unpublished consumer
   fixture.
7. **Schema migration tooling.** The extension *fences* on schema generation
   (UC-44); it does not run migrations.
8. **Backup and restore mechanics.** The extension defines generation and epoch
   semantics and refuses stale artefacts; taking and restoring backups is an
   operator procedure.
9. **Data residency, region pinning, sovereignty routing.** Not in the governing
   documents; see rejected axes.
10. **Per-tenant capacity management, quotas and fairness.** Only the *bounds*
    that prevent cross-tenant harm (INV-23) are in scope.
11. **Cross-tenant transactional atomicity.** Impossible under
    database-per-tenant; replaced by per-tenant atomicity plus resumability
    (INV-21). Anything stronger must be refused rather than approximated.
12. **A tenant-aware unit of work spanning subsystems.** [MT] names "a
    tenancy-owned cross-subsystem UoW" as a forbidden shortcut.
13. **Any global current-tenant convenience.** Explicitly forbidden by [MT]'s
    package list: "a generic extensions bundle, tenant registry, service locator or
    global current-tenant singleton."

---

## Open questions

**Q1 — Is a foreign row a refusal or a not-found, at the seam?**
INV-10 requires them to be indistinguishable *at the edge*. It does not settle
whether the seam itself returns a distinct internal kind that the transport then
flattens. A distinct internal kind is more useful for protected diagnosis and
more dangerous, because one careless transport mapping restores the leak.
*Consequence of getting it wrong:* enumeration via status code. *Proposed
default:* no distinct kind exists at the seam; the protected channel gets the
detail out-of-band (UC-85).

**Q2 — What exactly happens to an in-flight unit of work when suspension lands?**
UC-18 requires the behaviour to be declared and one of two options. Complete-then-
refuse is simpler and briefly violates the suspension; refuse-and-roll-back is
stricter and can lose work an operator believed was committed. *Needs an owner
decision, and the profile must state it.*

**Q3 — Is the tenant reference or the (reference, generation) pair the identity?**
INV-16 and UC-74 assume generation is part of identity. If it is not, every
restore and every recycled reference is a leak. If it is, then every durable
record, cache key and object namespace must carry the generation, which makes a
restore invalidate all of them — an availability cost. *Needs a decision before
the epoch contract is frozen at M0.*

**Q4 — Is schema-per-tenant a third topology, or a variant of one of the two?**
[MT] names two. Some deployments in this shape use one database with a schema per
tenant, which behaves like database-per-tenant for isolation but like shared-row
for pooling. Listing it now would be speculative (see rejected axes); ignoring it
now may bake in an assumption that a datasource is a database. *Recommendation:*
keep the source-selection strategy's contract at "one caller-owned root
datasource capability" so a schema-bound source can satisfy it later, and do not
add a third strategy.

**Q5 — What is the durable freshness bound?**
UC-52 requires re-derivation at execution. If a worker holds a re-derived scope
for a long handler, at what point is it stale again? Re-checking per side effect
is safe and expensive; checking once at entry is cheap and leaves a window whose
width nobody has stated. *Needs a declared bound in the profile.*

**Q6 — Do reads and writes share one lifecycle admission policy?**
A suspended tenant plausibly should still be readable (export, dispute
resolution) while not writable; a migrating tenant plausibly should be neither.
V3 treats the admitted set as a single list; it may need to be a list per
operation class. *Consequence:* if it is one list, some deployments will keep
tenants active when they should be suspended, which is worse.

**Q7 — Who owns cache and object cleanup on deletion?**
INV-36 requires the tenant's footprint to be empty in every subsystem after
deletion, but object stores and caches have their own lifecycles and the
extension does not own them. Is deletion an orchestrated fan-out the extension
drives, or a per-subsystem operator procedure the extension only *verifies*?
*Verification is the smaller commitment and is testable; orchestration is what
people will expect.*

**Q8 — Is grant issuance in this extension at all?**
UC-78 assumes it is. It is arguably control-plane policy, in which case the
extension only *consumes* a grant. Consuming-only keeps the extension smaller and
pushes a genuinely security-critical decision onto every application.

**Q9 — How is an unscoped bulk mutation classified when the criteria are opaque?**
UC-29 and INV-12 assume the seam can tell whether criteria constrain ownership.
If criteria arrive as an opaque compiled artefact, the safe answer is to refuse
all bulk mutations unless the strategy itself adds the constraint. *This changes
what V6 can offer* and should be settled before the bulk matrix is advertised.

**Q10 — Does the extension need a scope-less bootstrap mode?**
Migrations, seeding and provisioning must write tenant-owned rows before a tenant
is active (its state is `provisioning`, which INV-15 refuses). Either those paths
use a grant (UC-78's mechanism, which then has a very privileged purpose), or a
distinct bootstrap capability exists. *A bootstrap capability is a second way to
bypass narrowing and needs the same scrutiny as the first.*

**Q11 — What is the observable bound on suspension propagation?**
UC-66 asserts "within a declared bound", but the bound depends on how long a
resolution result may be cached. A long cache makes suspension slow to take
effect; a short one makes the control plane a hot dependency of every request.
*The profile must state the number, and UC-66 must assert it.*

**Q12 — Are pre-tenancy rows with no owner a migration concern or a runtime one?**
An application adopting the extension has existing rows with a null or default
ownership value. Refusing them is correct and breaks the application; admitting
them is a permanent hole. *Proposed:* refuse at runtime, and make backfill an
explicit operator procedure — but this needs stating, because the alternative
will otherwise be discovered by an author under deadline.
