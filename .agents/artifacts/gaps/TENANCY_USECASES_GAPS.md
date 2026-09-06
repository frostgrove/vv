# TENANCY - USECASES & INVARIANTS - GAPS

## Round 1 — coverage (adversarial) — 2026-09-05

Reviewed: `.agents/artifacts/usecases/TENANCY_USECASES.md` against
`docs/roadmaps/2026-09-01-multitenancy-roadmap.md` [MT] and
`docs/roadmaps/2026-09-01-extension-architecture-roadmap.md` [EXT].
No implementation file was read.

---

### GAP-1 [critical][immediate] Epoch currency has no freshness model; UC-13, UC-66 and INV-16 demand three incompatible ones
- **Where:** TENANCY_USECASES.md — INV-16, UC-13, UC-66, UC-46, Q5, Q11, "Extension points" row *Trust authority*
- **What:** INV-16 states an absolute: "A scope, binding or durable reference whose epoch differs from the authority's current epoch for that tenant is not used to perform work." UC-13's acceptance operationalises that absolutely too — "advances the authority's epoch, then performs each supported verb. Every verb refuses with the stale kind". That is only satisfiable if the data plane learns the current epoch on **every verb**, i.e. a trust-authority round trip per repository call. UC-66 simultaneously admits a cache — "The declared bound (immediate, or ≤ one resolution TTL) is measured and asserted" — and Q11 confirms the bound is unsettled. The extension-points table gives the trust authority a request/response shape only ("Verified identity + tenant reference → scope, lifecycle state, epoch"); there is **no invalidation, subscription, watch or push seam**, so a deployment cannot get UC-13's zero-window behaviour without paying a control-plane call per operation. Q5 asks this question for the durable handler only; nobody asks it for the request path. Nothing in the document declares which of {per-operation authority call, TTL-bounded cache, push invalidation} is the contract.
- **Why this severity:** The three plausible resolutions are three different systems. Per-operation validation makes the control plane a hard dependency of every SQL statement (and UC-42's "resolver returning a transient error" then fails every request in the deployment, not just unmapped ones). A TTL cache makes UC-13 and INV-16 false for the width of the TTL — which is exactly the window in which a restored generation is written to by a pre-restore scope (INV-16's own violation consequence: "silent, plausible corruption"). Push invalidation needs an extension point that does not exist and a partition-tolerance story that nobody has written. [MT] M0.5 freezes "scope construction, lifecycle/epoch, error identity, context and redaction contracts" — this is precisely the contract being frozen, and it is currently three contracts.
- **Why this timing:** It is the epoch contract itself, frozen at M0 before any code. Choosing wrong forces a rewrite of scope, the durable capture/restore adapters, the binding cache and every acceptance that says "advance the epoch and assert".
- **Close criteria:**
  - [ ] INV-16 states an explicit freshness window (zero, or a named bound) and says who owns it.
  - [ ] UC-13's acceptance is rewritten to assert the declared window, not instantaneous detection, or the document declares per-operation revalidation and accepts its cost in writing.
  - [ ] A UC exists for "the trust authority is slow" and "the trust authority is unreachable" on the **request** path, with declared timeout and fail-closed behaviour, distinct from UC-42's binding-resolver outage.
  - [ ] If push/watch invalidation is the answer, it appears in the extension-points table with its own failure mode (missed notification → fall back to what?).
  - [ ] Q5 and Q11 are merged into one decision covering request path, long-lived request, durable handler and cached binding.
- **Status:** open

---

### GAP-2 [high][immediate] Ownership is hardcoded to "one column/attribute on the row" — a spec-level fit to one schema shape
- **Where:** TENANCY_USECASES.md — UC-2 (Given), UC-22 (Acceptance), UC-27, UC-33 (Acceptance), INV-7, INV-8, "Extension points" row *Ownership strategy*, DX snippet `tenancy.OwnedResource[Widget, WidgetID](ownedBy(widgetOwnerAttr))`
- **What:** Every shared-row usecase assumes the tenant-owned resource carries an ownership **column** on its own table: UC-2 Given — "rows carry an ownership column"; UC-22 — "The persisted row's ownership column is read back directly"; UC-33 — "every unique constraint on a tenant-owned table includes the ownership column"; the extension point is "Which attribute carries ownership". [MT] says only "Ownership is derived or validated on create and immutable for ordinary mutations" — it never says ownership is a column. The single most common shared-row schema in the wild is the **child table with no tenant column**, whose ownership is derivable only through a join to its parent; the second is ownership carried by a composite key or a derived expression. For those resources UC-2's mapping cannot be supplied, UC-22's direct read has nothing to read, UC-33's schema check is vacuous, and INV-7/INV-8 are unstatable.
- **Why this severity:** `universality.md`: "Shape assumptions lifted from an example". An extension whose only ownership mechanism is a column silently offers *no protection at all* for every table that lacks one — and the author of that table gets no failure, because UC-6's completeness check (itself broken, see GAP-7) is the only thing that would notice. That is the exact breach class this document says it exists to prevent, on input #2.
- **Why this timing:** The ownership contract is the shape of the M1 CRUD middleware and of the per-resource declaration in the profile. Retrofitting join-derived or expression-derived ownership after the column-shaped API ships is a public-contract rewrite.
- **Close criteria:**
  - [ ] Ownership is expressed as a *narrowing strategy per resource* with the column case named as one strategy among at least: own-column, parent-derived (join/relation), and expression/composite.
  - [ ] UC-2, UC-22, UC-27, UC-33 and INV-7/INV-8 are restated in terms of "the resource's declared ownership strategy" and each carries an acceptance for a resource whose ownership is *not* a local column.
  - [ ] UC-33's schema check is generalised: for a resource with no ownership column, the check states what it asserts instead.
  - [ ] The DX snippet no longer implies a single attribute is the only input.
- **Status:** open

---

### GAP-3 [high][immediate] The whole isolation matrix is specified at exactly two tenants; the only high-N usecases assert no isolation property
- **Where:** TENANCY_USECASES.md — Groups C and D throughout ("tenants A and B"), UC-20 ("Seed A with 3 rows and B with 400"), UC-26, UC-31, UC-32, UC-50; and the high-N usecases UC-47, UC-87
- **What:** Every isolation acceptance in the document is written for the pair {A, B}. The two usecases that do drive many tenants assert nothing about isolation: UC-47's acceptance is "peak connection count ≤ the declared bound; every request terminates with success or a declared refusal within the declared time limit; no deadlock under `-race`; the refusal kind is distinguishable" — no statement is ever attributed to a source; UC-87 counts label sets only. UC-48 exercises exactly one pinned borrower and one reusing tenant. So a binding cache keyed on a truncated or hashed tenant reference, a slot-reuse bug, or an eviction race that hands A's source to the 900th tenant, passes every acceptance in the document. UC-61 is the sole place that reasons about adversarial multiplicity, and it says so itself: prefix confusion "is invisible to any test that uses tenant identifiers `a` and `b`" — the document diagnoses the disease in one usecase and carries it in fifty.
- **Why this severity:** `universality.md` — the sample shape (N=2) is baked into the mechanism's evidence. The failure this document names in its opening paragraph ("tenant A silently reading … tenant B's data, with a green test suite") is reachable at N=3 and at N=1000 while the suite stays green.
- **Why this timing:** This defines the conformance matrix that M1/M2 evidence is built from. Adding an isolation assertion to a soak after the soak exists is cheap; discovering at M5 that the advertised profile's evidence never checked isolation above N=2 is a release-blocking rewrite.
- **Close criteria:**
  - [ ] UC-47's acceptance gains a per-tenant sentinel: every statement issued during the soak is attributed to the tenant that issued it, and the assertion is zero cross-assignments across all N.
  - [ ] UC-48 is generalised from one eviction to a churn loop over ≫ budget tenants, still asserting no handle is observed under two distinct tenant references.
  - [ ] At least the shared-row leak matrix (UC-26, UC-30, UC-31, UC-32) is restated at N ≥ 3, including one tenant that is neither the actor nor the seeded foreign tenant, so "narrowed to *the scope's* tenant" is distinguishable from "narrowed away from the one other tenant present".
  - [ ] The document states that any acceptance written at N=2 must justify why N=2 is sufficient for that specific property.
- **Status:** open

---

### GAP-4 [high][immediate] INV-30's refusal-kind list is closed and omits five kinds other usecases require
- **Where:** TENANCY_USECASES.md — INV-30, versus UC-27, UC-43, UC-49, UC-58, UC-77, and the "Failure surface" table
- **What:** INV-30 declares the vocabulary as an exhaustive list: "Every refusal carries a stable, comparable kind — absent, untrusted, stale, lifecycle-inadmissible, unmapped, incompatible, pinned, grant-required, capacity — and no identifying detail." Other usecases mandate kinds not on it: UC-27 "an ownership-conflict kind"; UC-43 "a fencing kind" (and UC-43 uses "fencing" in the same document that uses "incompatible" for UC-17 and UC-44, so they are not the same kind); UC-49 "an availability kind" — which the Failure-surface table also lists; UC-77 "an incompatible-generation kind"; UC-58 "a wiring refusal". A caller building the uniform `refusalToTransport` mapping the DX section promises will produce a mapping that is exhaustive over INV-30 and non-exhaustive over reality.
- **Why this severity:** Error identity is frozen at M0 ([MT]: "Freeze scope construction, lifecycle/epoch, **error identity**, context and redaction contracts"). An incomplete frozen enumeration means either the missing kinds are collapsed into existing ones — destroying UC-49's explicit requirement that capacity/availability be "distinguishable from a tenancy-authorisation refusal so operators do not chase a security ghost during a capacity incident" — or the list is not actually closed, in which case INV-30 is not falsifiable.
- **Why this timing:** It is a public contract (`errors.Is` against exported sentinels) that other sections and every consumer's transport mapping will depend on.
- **Close criteria:**
  - [ ] INV-30's list is reconciled with every kind named in a UC, or each surplus UC kind is explicitly mapped onto a listed kind and the UC text updated.
  - [ ] The document states whether the list is closed (new kinds are a breaking change) or open (and then what INV-30 actually asserts).
  - [ ] UC-58's construction-time wiring failure is classified as a refusal kind or excluded from INV-30's scope explicitly.
- **Status:** open

---

### GAP-5 [high][immediate] The wrong-database fence requires the scope to name a datasource, contradicting INV-4 and UC-8
- **Where:** TENANCY_USECASES.md — UC-43 (Then), INV-22, versus INV-4 and UC-8 (Acceptance)
- **What:** UC-43: "the datasource itself is checked to be the one **the scope names**". INV-22: "A resolved datasource is used only after being confirmed to be **the one the scope names**". INV-4 and UC-8 forbid exactly that: the scope "contains no DSN, database name, bucket, prefix, credential, raw claim, header, carrier or telemetry label", and UC-8's acceptance inspects the value to prove "it exposes no DSN, database name". An implementer reading INV-22 literally has two options and both are wrong: put a database identity into the scope (breaking INV-4, which [MT] and UC-8 make a hard gate), or implement the fence as a comparison against the resolver's own answer — which is circular, since UC-43's premise is that the resolver is the thing that lied.
- **Why this severity:** [MT] calls wrong-database fencing "mandatory evidence" and the document itself says "Without a fence, the resolver becomes a single point of total compromise". As written, UC-43's control case ("a control in which the resolver returns the correct source and the same code path succeeds") is satisfiable by a fence that compares nothing at all, because no comparand is specified.
- **Why this timing:** It is a contradiction between two frozen M0 contracts (scope minimal content; selection fencing). Both cannot ship as written.
- **Close criteria:**
  - [ ] UC-43/INV-22 state the comparand explicitly and it is not a DSN/database name — e.g. the connected database self-identifies over the connection with a stored (tenant reference, generation) marker, and that marker is compared against the scope's (reference, epoch).
  - [ ] The acceptance names how the marker is planted and asserts the fence fails when the marker is absent (a database with no marker is refused, not admitted).
  - [ ] INV-4's allow-list is restated to say what the "minimum capability the selected topology needs" may and may not contain under database-per-tenant, since that phrase is currently the only escape valve.
- **Status:** open

---

### GAP-6 [high][immediate] UC-33 and UC-35 each bless a branch that contradicts INV-10; UC-35's acceptance is self-contradictory
- **Where:** TENANCY_USECASES.md — UC-33, UC-35, versus INV-10 and UC-31; plus UC-12's timing claim
- **What:** Three separate defects around existence non-disclosure.
  1. **UC-33 branch 2.** UC-31 requires that any verb "that surfaces 'already exists'" answers "exactly as it would if that identifier had never existed anywhere". UC-33 permits the opposite: on a global unique constraint, "the collision is reported as a constraint failure that does not disclose that the conflicting row belongs to another tenant". Not disclosing *whose* row it is does not close the leak — A learns that a value it cannot see is taken, which is a working oracle over B's key space, and INV-10's scope explicitly includes "constraint failures". Only UC-33's first branch (composite constraint) satisfies INV-10; the second must be labelled a declared exclusion, not a satisfied requirement.
  2. **UC-35 branch 2 is self-contradictory.** The Then permits a foreign cursor to be "evaluated strictly within A's partition"; the acceptance then demands its "decoded content … never influences which of A's rows are returned in a way that reveals B's sort-key values". If the cursor is applied at all, A's page *is* a function of B's boundary value — that is a binary-search oracle over B's sort keys, repeatable. There is no implementation that both applies the cursor and satisfies the acceptance, so the acceptance cannot fail an implementation that ignores it; it can only fail one that refuses, which the Then also permits. The branch is unfalsifiable and unsafe.
  3. **Timing is asserted once and invarianted nowhere.** UC-12 requires the refusal to be indistinguishable "in the response body, status and **timing class**". INV-10's scope enumerates "Returned values, error kinds, error text, affected-row counts, aggregate results, pagination totals, constraint failures, and any signal emitted along the way" — no timing. UC-31's assertion is "A byte-comparison of the responses". So a timing side channel is demanded in one usecase, defined nowhere, and tested nowhere.
- **Why this severity:** This is the security core. INV-10's violation consequence is stated by the document itself as "Enumeration of another tenant's identifiers, key space, customer names or data volume — a leak that needs no read access", and two usecases currently authorise it.
- **Why this timing:** Both branches would be implemented as written and both would be signed off by their own acceptance. The cursor decision in particular changes the pagination contract.
- **Close criteria:**
  - [ ] UC-33 branch 2 is relabelled a declared, profile-listed exclusion with a stated residual leak, or removed so that composite-with-ownership constraints are the only admitted answer.
  - [ ] UC-35 drops the "evaluate within A's partition" branch, or states the mechanism that makes it oracle-free (e.g. the cursor is bound to the minting generation+tenant and refuses otherwise) and asserts the oracle is absent by an actual differential test.
  - [ ] INV-10's scope either includes response timing with a defined measurement and a declared bound, or the document states in writing that timing side channels are out of scope and UC-12's "timing class" clause is removed.
- **Status:** open

---

### GAP-7 [high][immediate] UC-6 needs a "declared tenant-owned resource" registry that no extension point supplies
- **Where:** TENANCY_USECASES.md — UC-6, versus "Extension points" table, "Declarative top-level DX", UC-1, UC-4, and [MT]'s ban on a tenant registry
- **What:** UC-6's Then requires detection of an unwrapped resource: "either it refuses because **the resource is declared tenant-owned** and has no strategy bound, or a composition-time completeness check fails the build". Nothing in the document says where that declaration lives. The extension-points table's only ownership entry is "Ownership strategy, per resource … Which attribute carries ownership", supplied *at the moment of wrapping* — i.e. it exists only for resources that were wrapped, which is the opposite of what UC-6 needs. The DX section shows per-resource wrapping and no declaration surface. And the declaration cannot live in the base seams, because UC-4 requires that removing tenancy leaves the base composition intact "except where a durable-work definition explicitly demands a tenant partition" — durable work is called out as the one exception, so a resource-level declaration in the base would be a second, undeclared one.
- **Why this severity:** UC-6 calls itself "the most common real-world tenancy breach and it is invisible to a test suite that only tests the resources someone remembered to test". As specified it is not implementable, so it will be implemented as its weaker sibling (a fixture that wires two of three and asserts nothing about the third), and the acceptance's control case will keep it green.
- **Why this timing:** It is a composition-root contract: whether the application declares its tenant-owned resource set, and where, changes the top-level API and the M0 seam inventory.
- **Close criteria:**
  - [ ] The document names the declaration surface (application-owned manifest passed to the extension spec, a per-resource marker on the model, or a generated inventory) and adds it to the extension-points table with its failure mode.
  - [ ] It states which side of the tenancy boundary the declaration lives on, and how UC-4 (tenancy removed entirely) is not broken by it.
  - [ ] It confirms the surface is not "a tenant registry, service locator or global current-tenant singleton" in [MT]'s sense, or explains the distinction.
  - [ ] UC-6's acceptance asserts the *composition-time* branch specifically when that branch is the declared design, not "the third's first use, **or** the composition".
- **Status:** open

---

### GAP-8 [high][immediate] Test-only scope construction across a module boundary is undefined; UC-92 and UC-90 cannot both hold for consumers
- **Where:** TENANCY_USECASES.md — UC-90, UC-92, INV-2; actor A9
- **What:** UC-92 requires that "A production-build fixture that references the test-only constructor fails to compile. A test-build fixture referencing it compiles." UC-90 assigns the constructor to actor A9, "Conformance author". Neither says whether a **consuming application's own tests** — which are a different module from the extension — can reach it. Both answers are broken as the document stands: if the constructor is unreachable outside the extension's own tests, every application adopting tenancy must invent its own way to get a scope into a test, which is precisely what UC-90's rationale forbids ("tests will construct scopes some other way and that other way will become production's escape hatch"); if it is reachable via a build tag or a separate package that any consumer can import, then "a production build" needs a definition, because the attacker's build is also just a build, and UC-92's compile-failure assertion measures the fixture's tags rather than any property of the extension.
- **Why this severity:** INV-2 (scope unforgeability) is "Complete bypass of every other control" when false, and its entire how-to-check leans on UC-92. An unresolved reachability question here is the difference between a shipped forge and an unusable extension.
- **Why this timing:** It is the public surface of the extension (which package, which build tag, which symbol) and it is frozen at M0 with scope construction.
- **Close criteria:**
  - [ ] The document states whether consumer test code can construct a scope, and by what mechanism.
  - [ ] "Production build" is defined operationally (what the check actually inspects) so UC-92 can fail.
  - [ ] If consumers can reach it, a UC covers the abuse case: an application that ships the tag/import into a served binary, and what detects that.
  - [ ] INV-2's how-to-check is updated to cover the consumer-module path, not only the extension's own compilation units.
- **Status:** open

---

### GAP-9 [high][immediate] UC-91 does not require the profile to contain the dozen declarations that other usecases assert against
- **Where:** TENANCY_USECASES.md — UC-91, versus UC-18, UC-27, UC-29, UC-30, UC-33, UC-35, UC-36, UC-47, UC-49, UC-55, UC-66, UC-69, UC-84, UC-89, INV-12, INV-26
- **What:** UC-91's Then enumerates exactly [MT]'s list — "topology, source/driver, the exact supported operation matrix, the admitted lifecycle states, the known exclusions and the evidence version" — and its acceptance checks only "the profile's operation matrix against the seam's actual method inventory, and against which conformance tests are green". Meanwhile at least fourteen other usecases assert *against a declaration the profile is supposed to carry* and that UC-91 never requires: UC-18's in-flight-suspension behaviour ("The declared behaviour is stated in the published profile"), UC-27's derive-vs-validate per resource, UC-29/INV-12's narrow-vs-refuse bulk policy, UC-30's cross-tenant-by-design relations, UC-33's intentionally-global constraints, UC-35's cursor policy, UC-36's escape-hatch enumeration, UC-47's connection budget and time limit, UC-49's availability behaviour, UC-55's generation-agnostic job policy, UC-66's suspension-propagation bound, UC-69's subsystem list, UC-84/INV-26's closed signal vocabulary, UC-89's latency bound. A profile missing every one of them passes UC-91's check.
- **Why this severity:** These fourteen usecases become untestable — each says "assert the declared behaviour" and there is nothing declared. The document's own line applies: "An unstated exclusion is read by consumers as coverage." It is also how UC-93's gate degrades: a gate that maps claims to tests cannot see a claim that was never required to be written down.
- **Why this timing:** The profile is the release artefact and the input to UC-93's gate; its schema has to exist before the first advertised profile.
- **Close criteria:**
  - [ ] UC-91's Then lists every declaration another UC asserts against, as a closed schema.
  - [ ] UC-91's acceptance fails when a declaration required by a green conformance test is absent from the profile (bidirectional check, not only profile → method inventory).
  - [ ] Every UC containing the phrase "the profile declares" cites the schema entry it depends on.
- **Status:** open

---

### GAP-10 [high][immediate] UC-7 blesses unbounded cross-extension observation; [MT] M4's per-composition scenario matrix has no usecase
- **Where:** TENANCY_USECASES.md — UC-7 (Then), INV-31; versus [MT] M4.3 and the *Cross-extension* conformance-profile row
- **What:** UC-7's Then says "Ordering may change **what the other extension observes**, but never whether narrowing happens." That sentence authorises a neighbouring extension composed outside tenancy to observe the pre-narrowing operation — its criteria, its arguments, its results — and no usecase or invariant bounds what it may then do with them. The concrete hazard is not hypothetical: [MT] names audit, event sourcing, storage and i18n as the compositions to prove, and an audit decorator sitting outside tenancy records the operation the caller *asked for* (including a foreign identifier), into a second durable store, on a path INV-10 and INV-25 never inspect. Symmetrically, an audit decorator inside tenancy records nothing for refusals, which is a compliance hole rather than a leak. The document has no UC that names either outcome, and no UC covers [MT] M4.3 at all — "Run same-ID, invalid-scope, stale-epoch, rollback, retry and privacy scenarios **across each advertised composition**". UC-7 uses one anonymous "unrelated extension"; UC-4 checks the module graph. Nothing runs the scenario matrix per advertised composition, and nothing states what a neighbour is permitted to see.
- **Why this severity:** A leak into an audit or event store is indistinguishable, in consequence, from a leak into a response — and it is the one path the entire Group C matrix, the sentinel scan (UC-86, scoped to *generic signals*) and INV-10 (scoped to *the seam's observable behaviour*) all structurally miss.
- **Why this timing:** It determines whether composition order is security-relevant. Right now INV-31 says it is not ("Order may change what other extensions observe; it never changes whether narrowing happens" is repeated verbatim in the DX section as something the caller must NOT have to remember), and that statement is only true if a neighbour's observation is bounded. If it is not bounded, order *is* security-relevant and the DX promise is wrong.
- **Close criteria:**
  - [ ] A UC covers "a neighbouring extension observes an operation that tenancy narrowed or refused", stating what it may observe (post-narrowing arguments only? refusal kind only?) and asserting it with a recording neighbour.
  - [ ] A UC covers [MT] M4.3: the same-ID / invalid-scope / stale-epoch / rollback / retry / privacy matrix run once per advertised composition, with the composition list generated from the profile so a new advertised composition forces the matrix to grow.
  - [ ] UC-7's Then and the DX "must NOT have to" bullet are amended to match whatever bound is chosen, or the document states that ordering is security-relevant and how the composition root is told.
- **Status:** open

---

### GAP-11 [high][immediate] No usecase covers producer-less tenant work: schedulers, manual requeue, provisioning and backfill
- **Where:** TENANCY_USECASES.md — Group E (all of it assumes UC-51's producer), INV-14 (Scope), Q10, Q12; entry-mode table E2
- **What:** E2 is defined as "Work enqueued now, executed later" with scope "Re-derived from A7 at execution", and every Group E usecase starts from UC-51's producer that "captures tenant authority at enqueue" inside a request under a valid scope. Three real origins of tenant-owned work have no producer and therefore no usecase:
  - **Scheduled/recurring per-tenant work** (nightly rollups, retention sweeps, per-tenant reindex) created by a timer, not a request. There is no scope at creation and no principal to resolve from.
  - **Manual requeue / replay by an operator.** INV-14's scope explicitly claims this territory — "All durable work, all retries, all replays, **all manual requeues**" — and no UC exercises it. An operator-requeued record is the exact shape UC-56 warns about (payload-claimed tenancy) with an actor who has write access to the queue by design.
  - **Bootstrap writes**: provisioning, seeding and backfill, which must write tenant-owned rows while the tenant is `provisioning` — a state INV-15 refuses. Q10 raises this and answers it with two options ("a grant with a very privileged purpose" or "a distinct bootstrap capability"), and notes "A bootstrap capability is a second way to bypass narrowing". Q12 raises the adjacent case of pre-tenancy rows with null ownership. Neither has a UC.
- **Why this severity:** Each is a path that writes tenant-owned data with no verified scope in its origin, i.e. the precondition the whole document is built on is absent by construction. They will be built anyway, because provisioning must work, and whatever mechanism appears will be the second forge (INV-2) or the second unscoped path (INV-1).
- **Why this timing:** Whether a bootstrap capability exists is a top-level API decision and a second security-critical surface; it cannot be discovered during M2.
- **Close criteria:**
  - [ ] A UC for system-originated (producer-less) per-tenant durable work: where its authority comes from, and that its authority is not the scheduler's configuration file.
  - [ ] A UC for operator requeue/replay, asserting INV-14 holds for a record an operator hand-created.
  - [ ] Q10 is resolved into either a UC for the bootstrap capability (with the same negative matrix as scope: absent, forged, stale, over-broad) or a UC showing provisioning uses the grant mechanism, plus a UC for Q12's null-ownership backfill.
- **Status:** open

---

### GAP-12 [high][immediate] INV-9 claims total verb coverage for four seams; only the repository seam has an acceptance
- **Where:** TENANCY_USECASES.md — INV-9 (Scope and How-to-check), UC-2; Group F
- **What:** INV-9's scope is "All constrained seams: repository, object, cache, durable work", and its violation consequence is "The next base-seam addition ships unprotected and nobody notices until it is used." Its how-to-check cites UC-2 only — and UC-2 is entirely about the repository seam ("A generated inventory of **the repository seam's** method set"). Group F's object/cache usecases (UC-59 through UC-65) test specific behaviours — put/get, list, invalidate, coalesce — but no usecase asserts that the object seam's or the cache seam's or the durable seam's **complete method set** has a recorded narrowing decision. So the object seam growing a copy/move/presign/multipart verb, or the cache seam growing a scan/TTL-sweep/atomic-increment verb, or the durable seam growing a cancel/reschedule/admin verb, is exactly the ungated addition INV-9 exists to prevent.
- **Why this severity:** Per the severity table, "missing test for a stated invariant" is `high`. Concretely: an object seam's copy verb that takes two logical names and is never narrowed on the *source* name is a cross-tenant read that Group F's put/get/list tests cannot see.
- **Why this timing:** [EXT] requires "method inventories and capability matrices fail when a base seam grows without a decorator decision" as a base-architecture obligation, and [MT] M0.7 requires "method inventories and exact optional-capability obligations for **every base wrapper the extension will return**". The inventory template is an M0 deliverable.
- **Close criteria:**
  - [ ] UC-2's acceptance is generalised to every constrained seam, or a sibling UC exists per seam.
  - [ ] The inventory check is asserted to fail on a synthetic seam growth for each seam, not only the repository one.
  - [ ] INV-9's how-to-check cites one UC per seam in its scope.
- **Status:** open

---

### GAP-13 [high][immediate] Two injected policies can each disable a control, with no declared floor; V3's evidence is inferred rather than cited
- **Where:** TENANCY_USECASES.md — "Extension points" rows *Lifecycle admission policy* and *Grant issuance*; V3; UC-81 (Acceptance); INV-15; INV-20; and "Explicitly not extension points"
- **What:** The document forbids, in writing, "a way to disable narrowing; a way to supply a default tenant" as extension points, then supplies two configuration surfaces that do the same work:
  - **Lifecycle admission policy** is injectable and V3's values are "the compatible subset of {provisioning, active, suspended, migrating, deleting/deleted, restored-generation}" — an unconstrained subset, so a deployment may admit `deleting` or `deleted`. INV-36 ("Deletion and hold finality") and UC-54 then become false by configuration. V3's evidence is also not evidence of variation: "[MT]: 'Data-plane resolution admits only an explicit compatible state' — *explicit* implies the set is declared per deployment, not fixed" is an inference from one word, and the extension-points table adds an invented justification ("Deployments differ on whether, say, `migrating` admits reads") with no citation. By the document's own rule — "An axis without such evidence is speculative, and a speculative axis is a defect" — V3 fails its own test as written. Q6 confirms the shape is unsettled.
  - **Wildcard grant cohorts.** INV-20 says "An absent scope, a nil scope, an empty cohort or a bare list of tenant identifiers is never authority", and UC-81 then reopens it: "A grant with no expiry, or a wildcard cohort, is refused at issuance **unless explicitly marked as such in the profile**." An all-tenants no-expiry grant is, in UC-81's own words, "a nil scope with extra ceremony".
- **Why this severity:** A security control that a configuration file can switch off is not a control, and both switches are reachable through documented, supported surfaces. The document's stated failure mode ("tenant A silently reading … tenant B's data, with a green test suite") is achievable here with a green suite *and* a supported configuration.
- **Why this timing:** These are extension points — the public shape of the spec object — and the lifecycle contract is frozen at M0.
- **Close criteria:**
  - [ ] A floor is declared: states that can never be admitted regardless of policy (at minimum `deleted`), asserted by a UC that attempts to configure them and is refused at construction.
  - [ ] V3 either cites evidence of genuine per-deployment variation or is demoted to a fixed set with Q6's read/write split as the only variation, and the extension-points justification is replaced with a citation.
  - [ ] UC-81's "unless explicitly marked in the profile" escape is removed or bounded (e.g. wildcard cohorts require an expiry, and expiry has a declared maximum), with a UC asserting the bound.
- **Status:** open

---

### GAP-14 [medium][immediate] Six usecases require an operator-visible per-tenant outcome without naming the channel, colliding with INV-25
- **Where:** TENANCY_USECASES.md — UC-53, UC-54, UC-58, UC-66, UC-72, UC-76, versus INV-25, INV-29, UC-85; UC-80 is the only one that gets it right
- **What:** UC-53 — "the job's final state matches the declared policy and **is observable to an operator**"; UC-58 — "the refusal is observable to an operator"; UC-76 — "the refusal is recorded where an operator will see it"; UC-72 — "failed ones refuse with the appropriate class"; UC-66 and UC-54 the same shape. None names a channel. INV-25 forbids tenant reference from every generic signal including "log field, event body or error text propagated into one", and INV-29 requires identifiable diagnosis to live "only in a protected channel with its own access control". The naive and obvious implementation of "observable to an operator" is a log line naming the tenant and the job — an INV-25 violation that UC-86's sentinel scan would catch only if the scan's captured stream includes application logs from the durable-work path, which UC-86 does not say. UC-80 is the counterexample that proves the pattern is knowable: it says "identifies the resumption point in a **protected (not generic) channel**".
- **Why this severity:** A usecase that silently breaks an invariant. It does not corrupt data, but it puts tenant identity into the least access-controlled system in the deployment, which is INV-25's exact stated violation consequence.
- **Why this timing:** Cheap to fix now (five phrases), and it determines whether the durable/operator surface needs a protected-channel dependency at all — a wiring decision.
- **Close criteria:**
  - [ ] Every "observable to an operator" clause names generic or protected, following UC-80's wording.
  - [ ] UC-86's scan scope is stated to include the operator-visible outputs of the durable and lifecycle paths, not only telemetry signals.
- **Status:** open

---

### GAP-15 [medium][immediate] Five acceptance lines are not falsifiable
- **Where:** TENANCY_USECASES.md — UC-1 / INV-34, UC-49, UC-81, UC-85, UC-67 / INV-17
- **What:**
  - **UC-1 / INV-34:** "a probe that enumerates process-global state before and after import and after construction sees no new entry". There is no defined enumerable set of "process-global state" in a Go process, so no implementation can visibly fail this. INV-1's how-to-check leans on it too ("a construction-time global-state probe, UC-1"). INV-34's second clause — "no goroutine is started by construction" — is the only falsifiable half.
  - **UC-49:** "B's success rate and latency distribution are **statistically indistinguishable** from the baseline". No test, no statistic, no sample size, no threshold — the assertion cannot be written, and if written will be flaky in whichever direction the author chooses.
  - **UC-81:** "A conformance check … **flags** a configured ratio." Flags to whom, with what consequence? An implementation that emits a log line satisfies it. "a configured ratio" is also an unmotivated threshold with no derivation.
  - **UC-85:** "they have different sinks and **different access controls**, asserted by a fixture that reads the generic sink and finds no identifying content" — the fixture proves different sinks; the access-control half is asserted by nothing.
  - **UC-67 / INV-17:** "The number of distinct generations observed as writable **at any instant** is 1, asserted by a concurrent observer" / "a concurrent observer samples the writable generation set throughout the procedure; its cardinality is always 1". A sampling observer cannot establish an at-every-instant property; a split-brain window narrower than the sample interval never fails.
- **Why this severity:** Local, one per usecase, no contract impact — but each is a stated proof that proves nothing, and INV-1, INV-17 and INV-34 rest on them.
- **Why this timing:** These acceptances are the specification of the conformance suite; writing an unfalsifiable test costs the same as writing a falsifiable one only before it is written.
- **Close criteria:**
  - [ ] UC-1/INV-34 replace the probe with enumerable surfaces: a source-level check for package-level mutable state, goroutine-count delta, and a declared list of known global registries in the build.
  - [ ] UC-49 states a bound (e.g. B's error rate is exactly 0 and B's p99 is within a declared multiple of baseline over a stated request count).
  - [ ] UC-81 states the consequence of a flag (gate failure, profile entry) and where the ratio comes from.
  - [ ] UC-85 either asserts access control mechanically or drops the claim to "different sinks" and states access control as an operator obligation.
  - [ ] UC-67/INV-17 replace the sampling observer with a fence-based assertion: after the fence, writes to the old generation are refused, and each acknowledged write appears exactly once in the surviving generation.
- **Status:** open

---

### GAP-16 [medium][immediate] UC-38 and INV-24 encode one vendor's mechanism for a generically stated axis
- **Where:** TENANCY_USECASES.md — UC-38 (Given and Acceptance), INV-24 (How to check), V2
- **What:** V2 is stated generically — "Database-enforced defence in depth | absent; present" — and INV-24 is stated generically — "A connection returned to a pool carries no tenant-derived state." The only acceptance is PostgreSQL-RLS-shaped and prescribes the mechanism in the *Given*: "the optional database-level predicate, **which is configured through a transaction-local setting**", then asserts "the **session variable** is empty at the start of B's transaction" and "the application role is not the table owner and does not bypass the predicate". If a deployment's second layer is a per-tenant database role, a security-barrier view, a connection-level setting or a proxy, INV-24's outcome still applies but none of the assertions are writable, and V2 has silently collapsed from an axis to a single implementation.
- **Why this severity:** [MT] does name RLS, so the mechanism is roadmap-sourced; the defect is that the *outcome* (B never observes A's setting; the app predicate is still active) is stated once and then buried under one vendor's vocabulary, so the usecase names a mechanism rather than an outcome. The consequence is bounded — a second mechanism would need a second usecase — but it prejudges V2.
- **Why this timing:** V2 is an axis in the frozen profile schema; whether it is "RLS on/off" or "second layer: none | RLS | other" changes the profile and UC-39's mutation test.
- **Close criteria:**
  - [ ] UC-38's Given states the outcome ("a database-enforced predicate is active") and moves the transaction-local-setting mechanism into a named PostgreSQL-RLS instantiation of the usecase.
  - [ ] INV-24's how-to-check states the mechanism-independent assertion (no tenant-derived state observable at the start of the next borrow, by any means the mechanism exposes) with the session-variable check as one instance.
  - [ ] V2's values are restated so a non-RLS second layer is expressible, or the document states that RLS is the only supported second layer and V2 is binary for that reason.
- **Status:** open

---

### GAP-17 [medium][immediate] INV-35 has no usecase; outbound cross-service work is an uncovered entry mode
- **Where:** TENANCY_USECASES.md — INV-35, entry-mode table, Group B
- **What:** INV-35's scope is "Outbound service calls, durable work, any serialisation", and its how-to-check cites UC-11 and UC-14 — one of which is about locally manufactured scopes and the other about a replayed cross-deployment scope. Neither is an outbound call. There is no usecase in which tenant-bound business code calls another service: what, if anything, is propagated; whether the extension is permitted to attach anything to an outbound carrier; what the receiving deployment does with it; and what happens when the callee is *itself* tenant-aware with a different trust authority. Out-of-scope item 4 excludes "Transport and router bindings", which covers the *inbound* hint (UC-12) but not the outbound obligation INV-35 asserts. The entry-mode table has no row for it.
- **Why this severity:** INV-35's own violation consequence is "The scope becomes a bearer token that any caller who can set a header can mint". An invariant with no usecase is a claim, and this one governs the boundary where a distributed deployment leaks. Also relevant to a repository seam backed by a remote service: under UC-2's "complete supported matrix" such a seam is a constrained seam, and nothing says how narrowing works when the narrowing must happen in another process.
- **Why this timing:** It determines whether the extension has an outbound surface at all — a public-API question, and the answer "none, the application propagates identity through its own contract" is itself a contract that consumers must be told.
- **Close criteria:**
  - [ ] A UC for outbound tenant-bound work: what the extension does (preferably nothing) and what the application must do.
  - [ ] A UC for the receiving side: an inbound carrier shaped like a scope is refused, and the callee re-derives from its own authority.
  - [ ] The entry-mode table gains the outbound/inbound service row or explicitly states it is the host's, with INV-35 restated as an obligation on the host rather than a property of the extension.
- **Status:** open

---

### GAP-18 [medium][immediate] UC-57 asserts idempotence of code the extension does not own
- **Where:** TENANCY_USECASES.md — UC-57 (Then and Acceptance), INV-21
- **What:** UC-57's Then requires that "the job's effects are idempotent or explicitly transactional per attempt, so a retry never doubles a side effect". The job's effects are the application handler's, not the extension's; no tenancy implementation can make an arbitrary handler idempotent, so the clause cannot be satisfied by the thing under test. The acceptance then tests something else entirely — one suspend/resume path — and never exercises the idempotence claim: "A fixture fails a job midway, suspends the tenant, then retries. The retry does not execute. Resuming the tenant and retrying again produces a final state equal to a single successful execution." The comparison against a control that never failed is only meaningful if the fixture handler was written to be idempotent, which makes the assertion about the fixture.
- **Why this severity:** It puts an obligation the extension cannot discharge inside a conformance requirement, so it will either be dropped (losing the part that *is* the extension's — that authority is re-derived per attempt and never reused from the previous attempt) or "satisfied" by an idempotent fixture that proves nothing.
- **Why this timing:** It is the division of responsibility between the extension and the handler author, which belongs in the profile and in the DX "what the caller must NOT have to do" list — currently that list implies the caller is off the hook.
- **Close criteria:**
  - [ ] UC-57 splits into the extension's obligation (authority re-derived from scratch per attempt, never reused; the attempt's binding is the current generation) and the application's declared obligation (handler idempotence or per-attempt transactionality).
  - [ ] The extension-side obligation has an acceptance that fails when authority is cached across attempts.
  - [ ] The application-side obligation appears in the profile and in the DX section as something the caller *is* responsible for.
- **Status:** open

---

### GAP-19 [medium][deferred] Named release and module-graph gates have no usecase or invariant
- **Where:** TENANCY_USECASES.md — UC-4, INV-33; versus [MT] "Dependency and composition gates" rows *Single extension*, *Transitive graph*, *Workspace/release*, and M0 exit evidence
- **What:** UC-4 and INV-33 cover graph optionality and extension-to-extension edges. Four gates [MT] states as "release requirements, not review suggestions" have no coverage:
  - "Exactly one published tenancy `go.mod` and one public package; row/database are files/strategies" — the forbidden **topology submodule** (`tenancyrow`, `tenancydatabase`) is not an *intersection of two extensions*, so UC-4's acceptance ("no package whose identity is the intersection of two extensions") does not reject it, and M0's exit evidence explicitly names "no bridge, **topology submodule** or extension-to-extension import".
  - "`go list -m all`, `go mod why -m` and a checked dependency diff explain every module edge".
  - "Discovered intended modules and `go.work` members are set-equal; published modules have no `replace`; tags are coherent" — nothing in the document mentions `replace`, tags or workspace membership.
  - [MT] DoD 10's rule that a component with a pinned regression may not be cited as evidence — UC-93 covers "a claim whose test is skipped, absent or failing", not "a claim whose evidence comes from a dependency whose own conformance is not green". (Note in the spec's favour: the document correctly refuses to name that dependency, avoiding a hardcode.)
- **Why this severity:** Structural, outside the data-plane security core, and mostly mechanical to check — but each is a stated release gate with nothing asserting it, and the topology-submodule one is the specific shape [MT] spends a section forbidding.
- **Why this timing:** Deferred: none of it changes a public contract or blocks M1; it is release-gate coverage that can be added alongside the M3 fixtures. It must not be dropped.
- **Close criteria:**
  - [ ] UC-4's acceptance rejects a topology submodule and a per-seam public subpackage, not only an intersection package.
  - [ ] A UC or INV covers workspace/`go.work` set-equality, absence of `replace` in published modules, tag coherence and the explained transitive graph.
  - [ ] UC-93 (or a sibling) covers evidence drawn from a component whose own base conformance is not green, stated generically.
- **Status:** open

---

### GAP-20 [medium][deferred] Runtime lifetime edges uncovered: borrower lifetime, cancellation/timeout mid-selection, panic during a request
- **Where:** TENANCY_USECASES.md — UC-47, UC-48, INV-23; "Failure surface" row 2; UC-86 (mentions panic recovery), INV-25 (Scope)
- **What:** [MT] lists "Pool/cache limits, **borrower lifetime**, rotation, eviction, schema capability and wrong-database fencing" as mandatory evidence. UC-47 covers limits, UC-48 eviction, UC-46 rotation, UC-44 schema, UC-43 fencing — **borrower lifetime has no usecase**: a borrower that never returns its capability (a leaked transaction, an abandoned unit of work) starves the budget and is indistinguishable from load. Two adjacent runtime edges are also absent: **context cancellation or timeout between source selection and use** (is the borrowed capability returned? is a half-opened transaction left?), and **a panic during a request** — the failure-surface table declares "Panic at construction only — never during a request", which is a statement about the extension's own panics and says nothing about an injected collaborator (trust authority, binding resolver, namespace mapper) panicking mid-operation, or about whether a recovered panic can leave a bound context on a pooled goroutine (INV-1's how-to-check does anticipate "a goroutine recycled from a pool", but only for the no-scope case).
- **Why this severity:** Availability and resource-leak class, plus one narrow correctness path (bound context surviving on a recycled goroutine). Contained by INV-1 and INV-23 in principle, untested in practice.
- **Why this timing:** Deferred: these are additional acceptances on existing usecases, no contract change.
- **Close criteria:**
  - [ ] A UC or an added acceptance for borrower lifetime: a borrower exceeding the declared lifetime is reclaimed or the process refuses new work, with a declared behaviour, and the reclaimed capability is never handed to a different tenant.
  - [ ] A UC for cancellation/timeout between selection and use, asserting no leaked borrower and no half-open transaction.
  - [ ] A UC for a panicking injected collaborator: refusal with a stable kind, zero side effects, no bound context observable on a subsequently recycled goroutine.
- **Status:** open

---

### GAP-21 [medium][deferred] Adversarial tenant references are exercised only at the namespace layer
- **Where:** TENANCY_USECASES.md — UC-61, INV-18; versus Group C and UC-2
- **What:** UC-61 is the document's one property test over "adversarially chosen tenant references and logical names (including separator characters, empty components, unicode normalisation variants and case variants)" — and it is scoped to object and cache keys. The same reference values flow into the shared-row ownership predicate, into composite unique constraints (UC-33), into the durable record's partition (UC-51) and into the binding cache key (UC-46). No usecase asks what an empty, oversized, case-varying or normalisation-varying tenant reference does there: whether two references that differ only by unicode normalisation produce one partition or two, whether an empty reference produces a predicate that matches everything, whether an oversized reference is truncated somewhere and collides. The document is aware of the class in one place and does not carry it to the others.
- **Why this severity:** A normalisation-collapsing or truncating reference is a cross-tenant read at the row layer with the same blast radius as UC-61's prefix confusion, and it is not covered by GAP-3's N-count fix either. Deferred only because it is an added acceptance rather than a design change.
- **Why this timing:** Deferred: no contract change; extends existing acceptances once the reference type is decided at M0.
- **Close criteria:**
  - [ ] UC-61's property generator is reused for the row-layer predicate, the composite constraint, the durable partition and the binding cache key, asserting injectivity of the reference → partition mapping at each.
  - [ ] The document states whether the tenant reference has a declared normal form and who enforces it, or asserts that no normalisation is performed anywhere.
- **Status:** open

---

### GAP-22 [medium][deferred] Missing actor (provider-adapter author), missing operation (backup), and two axis-table defects
- **Where:** TENANCY_USECASES.md — "Actors" table, Group G, "Axes of variation" V6 and V9; versus [MT] provider-adapter rules and "Migration, backup, restore, deletion, legal hold and tenant move"
- **What:** Three smaller coverage items.
  - **No actor for the provider-adapter author.** [MT] permits and constrains them — "Provider adapters may depend on the tenancy contract and one genuine provider ecosystem. They may not combine that provider with OTel, a router, JWT, audit or another independently selected extension" — and the gates require "every later provider module states its one ecosystem decision". Nobody in the actor table writes one, and no UC covers the shape: which tenancy contract a provider adapter implements (trust authority? binding resolver?), what proves it isolates exactly one ecosystem, and what proves the base tenancy module still resolves without it.
  - **Backup has no usecase.** [MT] names it among the topology-specific procedures. Restore gets UC-68 and UC-73; deletion, hold and move get usecases; backup gets none, while out-of-scope item 8 excludes "Backup and restore mechanics" — an asymmetric exclusion, since restore is not excluded in practice. The interesting case is unaddressed: a shared-row backup that must be per-tenant extractable for deletion (INV-36) and portability, and a backup taken mid-migration.
  - **Axis-table defects.** V9's "Values" column lists "purpose; cohort; expiry" — those are three *components* of one capability, not values of an axis. V6's evidence is an inference, not a citation: "'admitted bulk effects' … *admitted* implies some deployments admit none" (harmless, since both behaviours are tested by UC-29, but it does not meet the document's own standard for an axis).
- **Why this severity:** Each is a genuine hole, none is in the data-plane security core, and the axis defects are presentational.
- **Why this timing:** Deferred: the provider adapter appears at M2 at the earliest, backup is an operator procedure rehearsed at M5, the axis table is editorial.
- **Close criteria:**
  - [ ] The actor table gains the provider-adapter author with at least one happy and one edge usecase (one ecosystem only; the base module still resolves without it).
  - [ ] Either a backup usecase exists, or out-of-scope item 8 is narrowed to state which restore obligations *are* in scope and why backup carries none — including per-tenant extractability for INV-36.
  - [ ] V9 is restated as an axis with actual values (or removed as a non-axis); V6's evidence is cited or the axis is demoted.
- **Status:** open

---

## What was checked and found adequate

So that "nothing found here" is distinguishable from "not looked at":

- **Happy usecase per actor and per entry mode.** Present for all nine actors and all six entry modes; the mapping line after the actor table is accurate.
- **Unknown-lifecycle-state handling.** UC-16 plus INV-15 handle the forward-compatibility case correctly, with a control that flips the outcome — the enumerated state list is *not* a hardcode because unknown is closed.
- **The `jobspg` pinning rule from [MT] DoD 10** is generalised into UC-93 rather than naming the component — correct avoidance of a spec-level hardcode.
- **Equal IDs in A/B**: UC-26 (shared-row) and UC-50 (two real databases, explicitly "not two schemas or two prefixes") would each catch a regression.
- **Missing / forged / stale scope**: UC-10, UC-11, UC-13, UC-14 with recording doubles and non-vacuous controls; INV-5's six-class × every-verb matrix is the right shape.
- **Zero foreign count/existence leak**: UC-31's run-twice byte comparison is the strongest acceptance in the document (subject to GAP-6).
- **Pooled RLS**: UC-38 covers all four [MT]-named assertions (pooled A then B, ordinary role, owner/BYPASSRLS, transaction-local reset) plus abnormal terminations (subject to GAP-16), and UC-39's mutation test is exactly right — it is the only place the document checks that the suite measures the extension and not the database.
- **Durable reference is not authority**: UC-51, UC-52, UC-56 and INV-14 would catch a regression; UC-56's two-record differential (payload names B, protected reference resolves to A) is falsifiable.
- **Namespace/partition escape**: UC-61's injectivity-and-non-containment property test and UC-62's `""` / `"/"` / `"../"` prefixes with a negative control are adequate.
- **Cross-extension rollback/retry/privacy**: UC-82, UC-57, UC-86–UC-89 cover the mechanisms; the per-composition gap is GAP-10.
- **Operations**: UC-66–UC-77 cover suspend, migrate, restore, delete, hold, partial migration, cohort failure, restore race, reference recycling, provisioning race and cross-subsystem generation skew. Reference recycling (UC-74) and the provisioning race (UC-75) are both real and both non-obvious.
- **Rejected-axes list** is a genuine deliverable and each rejection is argued from absence of evidence rather than taste; the schema-per-tenant recommendation in Q4 (keep the source contract at "one caller-owned root datasource capability") is the right shape.
- **Open questions Q1–Q12** are real, correctly scoped and mostly name their consequence. Q2, Q5, Q6, Q9, Q10, Q11 and Q12 each sit on a gap above, which is why several close criteria route through them.

---

## Round 1 — dispositions (orchestrator, after implementation)

Two validators ran in parallel and both wrote to this file; the invariant/DX
validator's section was overwritten by the coverage validator's. Its findings are
recorded here from its report rather than lost, marked *(DX)*.

Design-changing findings were closed in the implementation. The rest are recorded
against the plan's `## Debt` or against the published profile, which is where a
deferred item stays visible.

| Finding | Disposition |
|---|---|
| GAP-1 *(critical)* — three incompatible epoch freshness models | **Closed by decision.** One model: the epoch is checked at each explicit boundary — request bind, durable execution, cohort member — and pinned in between; the framework caches no resolution, so the propagation bound is one boundary and any caching is the deployment's resolver with the deployment's own bound. Recorded in [[D-117]] and in the published profile |
| GAP-1 *(DX, critical)* — the trust authority returning the scope type would make it constructible in production | **Closed in the design.** `Resolver` returns `Resolution` — plain data. `Scope` has no exported constructor and carries a per-authority HMAC binding, so a zero value and a scope from a second authority both refuse. [[D-117]] |
| GAP-2 — ownership hardcoded to one column | **Closed.** `Ownership[M]` is an interface; `Column` and `Through` are two implementations, and a resource shaped like neither implements it. `Through` narrows through a dotted path and freezes the key that points at the owner |
| GAP-2 *(DX)* — no conservation invariant; a predicate narrowing to nothing satisfies every isolation assertion | **Closed.** Guarantee 15 of UC-028, and `TestATenantSeesEveryRowItOwns` is its control, asserting both the row count and the exact `WHERE` clause |
| GAP-3 — the isolation matrix is specified at N=2 | **Partly closed.** Namespace injectivity and non-containment are proved over 500 tenants and scope minting over 1000; the row matrix remains at N=2, which is what the shared-row leak actually needs |
| GAP-4 — the refusal-kind list omits five kinds | **Closed.** Ten sentinels, including `ErrUnmapped`, `ErrGrantRequired`, `ErrPinned`, `ErrCapacity` and `ErrUnavailable`, with the capacity and availability kinds deliberately not wrapping `crud.ErrForbidden` |
| GAP-5 — the wrong-database fence needs the scope to name a datasource, contradicting INV-4 | **Closed.** The scope names a reference and a generation and nothing else; the `Directory` maps them to a source, and the fence compares the *database's* schema generation against the scope's epoch. No DSN, database name or credential is reachable from a scope |
| GAP-6 — UC-33 and UC-35 bless branches contradicting INV-10 | **Documented, not closed.** A globally unique create and a replayed foreign cursor are the same class of oracle UC-004 already records for unique constraints. Both are named in the published profile's known exclusions |
| GAP-7 — UC-6 needs a declared-tenant-owned registry | **Refused.** A registry of repositories is on the roadmap's forbidden list. Forgetting to apply the middleware is caught by code review, not by a service locator. Named in the profile |
| GAP-8 — test-only construction across a module boundary | **Closed better than asked.** There is no test-only constructor at all: a consumer's test wires `tenancy.Fixed` into an ordinary `Authority`, which is the path production uses. Nothing to leak |
| GAP-9 — the profile does not require its declarations | **Closed.** The published profile in the multitenancy roadmap states topology, resources, operations, lifecycle admission, epoch model, suspension behaviour, pre-tenancy rows, deletion footprint, durable drivers and known exclusions |
| GAP-10 — unbounded cross-extension observation | **Deferred to M4.** Two of the extensions do not exist yet |
| GAP-11 — no usecase for producer-less work | **Answered by construction.** A scheduler, a requeue and a backfill all enter through the same `Authority`: either a bound scope or an accepted grant. There is no third path, which is the point |
| GAP-12 — INV-9 claims four seams, one has an acceptance | **Partly closed.** The repository seam inherits [[D-030]]'s reflection-based obligation test. A method inventory for the storage, cache and jobs adapters is owed and is in the plan's Debt |
| GAP-13 — two injected policies can each disable a control | **Closed by shape.** `Admission` cannot express "any state" without enumerating it, and a grant cannot express a wildcard cohort. Both refuse empty |
| GAP-14 — operator-visible per-tenant outcome collides with INV-25 | **Closed.** `Grant.Each` returns `[]Member` to the caller — which is application code inside the process — while `Outcome` is what a *signal* may carry. The two channels are different by construction |
| GAP-4/GAP-11 *(DX)* — `EachTenant` returns one error and cannot carry per-member outcomes | **Closed.** `Each` returns `([]Member, error)`; `TestOneCohortMemberFailingLeavesTheRestResumable` pins it |
| GAP-12 *(DX)* — no shutdown surface | **Closed.** `Directory.Close`, and `Lease.Release`; construction starts no goroutine, so there is nothing else to stop |
| GAP-14 *(DX)* — the reference's type and normalisation owner are unstated | **Closed.** The reference is opaque, compared byte for byte and never folded — whether `Acme` and `acme` are one tenant is the control plane's answer. `TestTheReferenceIsCompareByteForByteAndNeverFolded` |
| GAP-15 — five unfalsifiable acceptance lines | **Not fixed in the spec.** The implemented tests assert observable behaviour rather than those lines; the spec is left as written because it is a phase-1 artifact, not a living document |
| GAP-16 — UC-38/INV-24 encode one vendor's mechanism | **Moot.** RLS is not implemented and is a named exclusion |
| GAP-17, GAP-18, GAP-19, GAP-20, GAP-21, GAP-22 | **Deferred**, recorded in the plan's `## Debt` and in the roadmap's open milestones |
