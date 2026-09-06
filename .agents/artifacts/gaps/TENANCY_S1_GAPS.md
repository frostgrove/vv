# TENANCY - implementation (S0-S6) - GAPS

## Round 1 — security and integrity — 2026-09-05

Reviewed against the code, not the prose: `tenancy/*.go` (14 production files,
1376 lines, longest file `database.go` 249, longest function `Directory.Borrow`
56), the `crud` S0 change (`executor.go`, `errors.go`,
`decorators/security/security.go`, `decorators/faults/faults.go`), and the tests
named in the plan's checkpoints. Every claim below was reproduced against the
tree as it stands at the time of writing, from an out-of-tree module that
imports `github.com/frostgrove/vv/tenancy` exactly as an application would;
transcripts are quoted in each finding. The tree moved twice during the review (20:54-20:57 and 20:59-21:04); every
finding was re-run against the state at 21:04. What was closed while the review
was running is kept here with its evidence rather than deleted — GAP-7 in full,
the binding half of GAP-9 — because a round is a record, not a snapshot.

**Clean checks, with the numbers.**
`ExistsUnscopedOf` is exact-outer: one type assertion, zero `Nexter` steps
(`crud/executor.go:141-148`); the only remaining `Nexter` walk in production
`crud` is `SourceOf` (`crud/executor.go:121-133`), which is navigation, not an
executable effect, and `unwrapSource` walks the explicitly-forwarded
`SourceUnwrapper` only. Three callers, two implementors, both of which answer
`ErrNoUnscopedExists` over a core that cannot. `security.saveTarget` treats
`!supported` and `ErrNoUnscopedExists` identically (`security.go:676-678`).
`tenancy` takes no third-party dependency (`go list -deps` → 0 non-`vv`
packages) and imports five root packages; it imports no other extension.
No `init`, no goroutine, no `os.Getenv`, no package-level mutable state beyond
two read-only tables (`errors.go:34`, `outcome.go:30`). Every `panic` (7) is at
wiring time. Scope unforgeability holds: `Scope` has four unexported fields and
an HMAC binding over length-prefixed `(origin, reference, lifecycle, epoch)`
(`scope.go:57-76`), so a literal, a zero value, a converted look-alike and a
scope minted by another authority all fail `boundTo` — the last one was
reproduced. Namespace injectivity and non-containment hold (500/500 distinct,
fixed-width 16-byte digest suffix, prefix bounded to 30 bytes so cross-prefix
containment is arithmetically impossible). The durable token encoding is
unambiguous (fixed 8-byte generation ‖ reference; 136 bytes max against
`jobs.MaxIdentityTokenBytes` = 2048, so no truncation) and token/partition
disagreement is caught — by `jobs.RestoredIdentity.validFor`
(`jobs/durable_context.go:497-510`), not by `tenancy`.

---

### GAP-1 [critical][immediate] A preload returns another tenant's rows, and the gate's own guard against that is switched off
- **Where:** `tenancy/row.go:189` (`AllowUnscopedRelationScopes: true`), `tenancy/row.go:61` (`column.Relations` returns `nil, nil`), `crud/decorators/security/security.go:147-162`
- **What:** `Column` — the strategy every shared-row deployment uses — contributes a root predicate and *no* relation scopes. `security.gate.relationScopes` would have refused an empty relation scope (`Denied(Read, "relation scopes returned no narrowing…")`), and `tenancy.Policy` deliberately turns that refusal off by setting `AllowUnscopedRelationScopes: true`. The plan's own S2 contract says the opposite in as many words: "it never sets `AllowUnscoped*`". Nothing in `tenancy` ever calls `crud.RelationScopes.AtPath`/`ForModel` for a `Column` resource, and the module doc's ownership table does not mention relations at all.
- **Why this severity:** Reproduced. `Order{TenantID:"acme", CustomerID:99}` where customer 99 belongs to `globex`; `orders.GetAll(ctx, crud.Preload("Customer"))` under acme's scope issues
  `SELECT "id","tenant_id","customer_id","total" FROM "orders" WHERE "tenant_id" = $1`
  then `SELECT "id","tenant_id","name" FROM "customers" WHERE "id" IN ($1)`
  and hands back `{ID:99 TenantID:globex Name:victim}`. The foreign key is ordinary business data that the gate does not freeze, so any tenant-A user who can set `CustomerID` can read tenant B's row by guessing an id — a cross-tenant read reachable from unprivileged input, through the framework's own supported verb. This is INV-11 and UC-30 exactly, and it is the failure `CLAUDE.md` names first among the silent bugs the decision docs exist to prevent ("a scope that stopped at a preload").
- **Why this timing:** The plan marks S2 `[x]` and claims INV-11 and UC-24/UC-30 covered; the published profile is built on that claim. Closing it changes the `Ownership` contract (a `Column` must be able to declare which relations are tenant-owned), so every caller's wiring changes.
- **Close criteria:**
  - [ ] `Ownership.Relations` for `Column` returns a non-empty `*crud.RelationScopes` for every relation declared tenant-owned, and the declaration is part of the strategy's construction.
  - [ ] `AllowUnscopedRelationScopes` is `false`, or its use is justified against a named, per-resource list of relations declared cross-tenant-by-design, and that list is printed in the profile (UC-30's "declared shared reference").
  - [ ] A test seeds a child owned by B referenced from an A parent, traverses under A's scope at depth 1 and depth ≥2, and asserts nothing foreign comes back — with the control subtest that shows the leak when the declaration is removed (the `test/integration/gate_relscope_test.go` pattern).
  - [ ] `docs/modules/{en,ru}/tenancy.md` states, per strategy, what happens to a preload.
- **Status:** open

---

### GAP-2 [critical][immediate] A scope is checked once at `Bind` and never again: a deleted tenant at a superseded generation keeps reading and writing
- **Where:** `tenancy/context.go:34-40` (`Scope` → `From` + `accept`), `tenancy/authority.go:102-121` (`minted`/`accept` re-check the HMAC and the *carried* lifecycle, never the resolver), `tenancy/row.go:176,183,191`
- **What:** `Verify` is the only path that calls `Resolver.Resolve`, and it runs once per `Bind`. Every repository verb afterwards calls `Authority.Scope`, which re-validates the binding and asks `admission.Admits(class, scope.lifecycle)` — where `scope.lifecycle` is the value frozen into the scope at bind time. The epoch in the scope is never compared with the authority's current epoch for that tenant. There is no `ErrStale` path on the request side at all: `ErrStale` is produced only by `jobs.RestoreIdentity` (token vs. lookup) and by grant expiry.
- **Why this severity:** Reproduced with a resolver whose resolution is swapped between calls. After `Bind`, the control plane moves the tenant to `Deleted` and the epoch from 1 to 2; the next two verbs still run: `SELECT … WHERE "tenant_id" = $1` and `INSERT INTO "orders" … RETURNING …`, resolver call count 1 (at bind) and 1 after both verbs. So a worker, a long-lived request, a streaming handler or a `Grant.Each` loop that binds early keeps writing to a tenant that has been deleted, and keeps writing into the generation a restore has already superseded — INV-16's own stated consequence, "silent, plausible corruption", and the mechanism by which UC-54's deletion guarantee is undone. The plan states the opposite as a settled answer (Q11: "the framework caches no resolution. The authority is called on every verification").
- **Why this timing:** This is the M0 epoch/freshness contract, frozen before the rest. Whichever answer is chosen — revalidate per verb, revalidate on a declared interval, or declare the window as "one bind" and forbid long-lived bound contexts — it changes `Authority.Scope`'s signature or the `Resolver` contract, and every consumer's wiring.
- **Close criteria:**
  - [ ] The freshness window is declared in `docs/modules/{en,ru}/tenancy.md` and in the profile as a number or as "one `Bind`", and the `Resolver` contract says who pays for it.
  - [ ] If the window is not zero, `Authority` exposes the revalidation seam (an interval, or an explicit `Revalidate(ctx)`), and `Grant.Each`, `Directory.Borrow` and `jobs` document which they use.
  - [ ] A test binds a scope, advances the authority's lifecycle to `Deleted` and the epoch, and asserts the declared behaviour for every mutating verb, with a control that the same verbs succeed before the advance.
  - [ ] The plan's Q11 answer and the doc are corrected to match the code.
- **Status:** open

---

### GAP-3 [high][immediate] The durable "protected reference" is unauthenticated plaintext, so queue write access is tenant impersonation
- **Where:** `tenancy/jobs.go:86-90` (`mintToken` = 8-byte epoch ‖ raw reference), `tenancy/jobs.go:60-84`, `jobs/durable_context.go:822-851` (`digestDurableContext` is an unkeyed SHA-256)
- **What:** The token the producer writes is the tenant reference in clear, with no MAC and no key; the partition beside it is `ParsePartition(reference)`, also in clear; the record's `binding` is an unkeyed digest over both, so it can be recomputed by anyone who can compute SHA-256. `RestoreIdentity` reads the reference out of the token and asks the authority about *that* tenant. Re-derivation therefore establishes that the named tenant is currently active at the named generation — never that this record was produced under that tenant's authority.
- **Why this severity:** An actor with write access to the queue table (a compromised low-privilege service, an SQL-injection sink elsewhere, an operator requeue endpoint, a restored dump) writes one row with `partition` and `token` naming tenant B and gets the handler executed bound to B, with B's full write authority. That is UC-56's stated threat verbatim ("an attacker with queue write access forged it") and its acceptance ("a record whose protected reference is tampered with is refused"), and INV-14's stated consequence ("queue write access becomes tenant impersonation"). `docs/modules/en/tenancy.md:166` asserts the property the code does not have: "queue write access is therefore not tenant impersonation". Secondarily, the tenant reference itself sits in the durable record in plaintext while every other surface redacts it.
- **Why this timing:** The token is a persisted format. Adding a MAC later invalidates every enqueued record, and the key it needs does not exist in `Spec` — the authority's salt is `crypto/rand` per process (`authority.go:39-42`), so it cannot survive the restart the use case is about.
- **Close criteria:**
  - [ ] `Spec` (or `JobContext`) takes a durable key, separate from the per-process scope salt, and the token is `MAC(key, namespace ‖ definition ‖ generation ‖ reference)` with the reference either MACed or absent from the plaintext.
  - [ ] `RestoreIdentity` refuses a token whose MAC does not verify, with `ErrUntrusted`, before `Lookup` is called.
  - [ ] A test forges a record for tenant B with a correct-looking partition and a hand-built token and asserts the handler is not entered and no statement runs.
  - [ ] `docs/modules/{en,ru}/tenancy.md` either states the property once it is true, or states the residual trust the deployment must place in queue write access.
- **Status:** open

---

### GAP-4 [high][immediate] `Tx` is the one `crud.Core` verb with no tenancy decision: a transaction opens with no scope
- **Where:** `tenancy/row.go:208-210` (`Repository` = `security.Gate(...)`), `crud/decorators/security/security.go` — the gate overrides 14 of the 15 behavioural `Core` verbs and forwards `Tx` from the embedded `crud.Core`
- **What:** `Core` declares 16 methods; `Meta` is metadata, 14 of the remaining 15 have a gate method, and `Tx` is the one that does not, so it reaches the source unchanged.
- **Why this severity:** Reproduced: `orders.Tx(context.Background(), fn)` with no scope in context enters the callback (`crud.IsTransaction` is true inside) and returns `nil`. INV-5 enumerates "no transaction opened" among the effects an absent scope must not produce, and UC-10's acceptance says the assertion runs "for every supported verb". A caller that opens the unit of work first and resolves the tenant inside gets a transaction, a held connection and — under database-per-tenant — a transaction on whichever source the repository was bound to, with no selection having happened. `tenancy/row_test.go:90` (`TestNoVerbRunsWithoutAVerifiedScope`) enumerates 15 verbs and does not include `Tx`, which is why this is green.
- **Why this timing:** It is the completeness claim of S2 ("the whole `crud.Core` matrix… covered") and INV-9. Whatever `Tx` should do — refuse without a scope, or be declared out of the matrix — belongs in the contract before consumers write `Tx`-first code.
- **Close criteria:**
  - [ ] `Tx` either carries a decision (resolve the scope before `Begin`, refuse otherwise) or is listed in the profile as an explicit exclusion with the reason.
  - [ ] `TestNoVerbRunsWithoutAVerifiedScope` covers `Tx` and `Restore`.
  - [ ] The obligation table behind `[[D-030]]` names `Tx` so the next decorator inherits the answer.
- **Status:** open

---

### GAP-5 [high][immediate] Per-class admission is decided by `ClassRead` on every verb, so the write and durable lists can only narrow, never differ
- **Where:** `tenancy/row.go:176` and `tenancy/row.go:183` — `Policy.Scope` and `Policy.RelationScopes` both call `authority.Scope(ctx, ClassRead)`
- **What:** The narrowing predicate is computed for every verb, read or write, and it is computed under `ClassRead`. `Inspect` (`row.go:191`) does use `classOf(action)`, but it runs *after* the scope, so `ClassRead` is a precondition of every write.
- **Why this severity:** Reproduced. An authority with `Admit(ClassWrite, Provisioning)` — the plan's own answer to Q10, "provisioning uses an admission policy that admits `Provisioning` for the write class" — binds successfully and then refuses the write: `err=tenancy: tenant lifecycle does not admit this work: forbidden`, zero statements. So the documented way to provision a tenant does not work, and the three-list design collapses to "whatever `ClassRead` admits, minus what the other lists remove". A deployment will discover this under deadline and will fix it by adding `Provisioning` to the read list, which is exactly the wider grant the three lists exist to avoid.
- **Why this timing:** `Admission` is a frozen M0 contract with a public constructor per class; a deployment that has already written `Admit(ClassWrite, …)` has a policy that silently means something else.
- **Close criteria:**
  - [ ] The narrowing predicate is resolved under the class of the operation, or `Policy` is constructed per class, or `Admission` documents that `ClassRead` is a floor for every class and `Admit` refuses a configuration that violates it.
  - [ ] A test drives the full {`ClassRead`, `ClassWrite`, `ClassDurable`} × {admitted, not admitted} matrix through a read verb and a write verb and asserts each cell.
  - [ ] The plan's Q10 answer is either demonstrated by a test or withdrawn.
- **Status:** open

---

### GAP-6 [high][immediate] `MaxCached` bounds the map, not the sources: 64 databases opened at once against a declared bound of 4
- **Where:** `tenancy/database.go:126-130` (capacity checked, then the lock released), `:132` (the source is opened outside the lock), `:156-159` (checked again, then closed)
- **What:** The capacity test is a check-then-act with the mutex dropped in between. N concurrent borrowers for N distinct tenants all pass the pre-check while `entries` is still small, all open a source concurrently, and all but `max` of them close theirs afterwards. There is no reservation, no in-flight counter and no single-flight per key.
- **Why this severity:** Reproduced: `declared MaxCached 4, cached 4, peak concurrently open sources 64`. INV-23 states the bound as "per-process resource use… stays within a declared bound regardless of how many tenants are active" and UC-47's acceptance is "observed peak connection count ≤ the declared bound". With a real pool factory this is 64 pools' worth of connections and file descriptors opened against a database that was sized for 4 — the customer-number-200 failure the use case is written about, and it fires precisely under the load spike that produces the concurrency.
- **Why this timing:** It is the mandatory evidence the roadmap gates M2 on, and the fix (reserve the slot before opening, or single-flight per binding) changes `Borrow`'s structure and its refusal timing.
- **Close criteria:**
  - [ ] A slot is reserved under the lock before `Sources.Source` is called, and released on failure, so the number of simultaneously open sources never exceeds `MaxCached`.
  - [ ] Concurrent borrowers for the same key share one open attempt rather than racing and discarding.
  - [ ] A `-race` test with tenants ≫ budget asserts the observed peak of concurrently open sources ≤ `MaxCached`, with a counter in the factory, not a count of map entries.
- **Status:** open

---

### GAP-7 [high][immediate] `Lease.Release` was racy: a data race under `-race`, a borrower count that could go negative, and a source closed under a live borrower
- **Where:** `tenancy/database.go:60-71` — was a plain `released bool`, now `release sync.Once`
- **What:** The idempotence guard for a double release was a plain `bool` on a value shared by whoever holds the pointer; the decrement it guards happens under the directory mutex but the guard itself did not.
- **Why this severity:** Reproduced under `-race`: `WARNING: DATA RACE … tenancy.(*Lease).Release() database.go:67` (read) against `database.go:70` (write). Two goroutines racing the guard — a `defer lease.Release()` plus a cleanup path, a context-cancellation handler, a pooled request object — both decrement, so `borrowers` reaches zero or goes negative while a borrower is still executing. The next `Evict` or `sweep` then calls `closeSource` on a pool with an in-flight transaction (`unlink`, `:221-227`), which is the use-after-eviction cross-tenant/availability failure UC-48 exists for; symmetrically, a negative count makes `held.evicted && held.borrowers == 0` unreachable and leaks the pool forever. A framework whose whole reason to exist is `-race` cleanliness under concurrency cannot ship a `-race` finding in its own pool accounting.
- **Why this timing:** It is a public value type handed to every caller of `Borrow`; making `Release` safe changes `Lease` (an atomic, or moving the guard under the mutex) before consumers write `defer` chains around it.
- **Close criteria:**
  - [x] `Release` is safe to call from any number of goroutines and any number of times — `sync.Once`, `database.go:61,66-71`; the reproduction above no longer fires and the probe module is `-race` clean.
  - [ ] `release` refuses to take `borrowers` below zero and says so loudly rather than silently (defensive; no reachable path left through `Release`).
  - [ ] A `-race` test in `tenancy/database_test.go` releases one lease from N goroutines while another borrower is live and asserts the source is not closed and the count is exact.
- **Status:** fixed (verified against `database.go` at 21:03; the residual floor check and the regression test remain open)

---

### GAP-8 [high][immediate] There is no wrong-database fence, and the fence that exists compares a schema generation with a tenant binding epoch
- **Where:** `tenancy/database.go:179-191` (`fence`), `:28` (`Schema func(context.Context, crud.Source) (Epoch, error)`)
- **What:** Nothing checks that the source the factory returned is the one the scope names. The only check is optional (`if this.schema == nil { return nil }`) and it asserts `schemaGeneration == scope.epoch` — equating the database's migration generation with the tenant's binding epoch, two quantities that move for unrelated reasons.
- **Why this severity:** UC-43 and INV-22 make this mandatory evidence: "the datasource itself is checked to be the one the scope names, and a mismatch refuses before any statement"; "a resolver bug or a corrupted cache entry becomes a total cross-tenant compromise with no second line of defence". Today a resolver that returns B's source for A's scope is used, and every statement of A's request runs on B's database — including writes. The optional schema fence cannot be used as specified either: a deployment that bumps a tenant's epoch on restore (which is the whole point of the epoch) must simultaneously bump that database's migration generation or every request refuses, so the realistic implementation of `Schema` is `return scope.epoch, nil`, which is a fence that always passes. The plan claims S4 covers UC-43 and INV-22 and its `MISSING` line names only "two real databases and outage behaviour".
- **Why this timing:** The `Schema` signature is the fence's contract; separating identity fencing from generation fencing changes `DirectorySpec` and every caller's wiring.
- **Close criteria:**
  - [ ] `DirectorySpec` carries an identity fence — the source (or the factory) reports which tenant generation it is, and `Borrow` refuses on mismatch before any statement, `ErrIncompatible`.
  - [ ] The schema fence takes a build-declared expected generation rather than being compared with `scope.epoch`, and its refusal is distinguishable from the identity fence's.
  - [ ] Tests: the factory returns the wrong source → refusal, zero statements on either source, and the control with the right source succeeds through the same path; a lagging schema generation refuses while the current one succeeds.
  - [ ] The plan's `MISSING` line for S4 names these, or the claim on UC-43/INV-22 is withdrawn.
- **Status:** open

---

### GAP-9 [high][immediate] A grant's purpose bounds nothing
- **Where:** `tenancy/grant.go:109` (`Each(ctx, grant, class, work)` — the class is the caller's argument), `:133` (`member` uses that class), `:159-172` (`grantBinding`)
- **What:** `Purpose` is validated, mixed into the binding and then never consulted again; what a cohort run may do is whatever `Class` the caller passes and whatever the work function does.
- **Why this severity:** Reproduced: a grant whose purpose is `monthly-billing-read`, run with `ClassWrite`, performed `INSERT INTO "orders" …` in both cohort members, outcome `ok` for each. UC-78's acceptance is explicit — "The grant's purpose bounds which operations are permitted: attempting a write under a read-purpose grant refuses" — and UC-81 names the decay this produces: "Grants that are always 'all tenants, no expiry' are a nil scope with extra ceremony". (The second half of this finding — a binding that covered only the purpose, the first member and the cohort length — was fixed at 21:04: `grantBinding` now MACs the origin, the purpose, the deadline and every member, with `TestEveryPartOfAGrantIsInsideItsBinding` behind it.)
- **Why this timing:** `Accept`/`Each` are the public cross-tenant contract; adding a purpose→class binding later changes both signatures.
- **Close criteria:**
  - [ ] The purpose carries the operation classes it permits, `Accept` records them, and `Each` refuses a class the purpose does not name — with a test that a read-purpose grant refuses a write and the control that a write-purpose grant does not.
  - [x] The binding covers `until` and every member — `grant.go:159-172`, proved by `TestEveryPartOfAGrantIsInsideItsBinding`.
  - [ ] The effective cohort actually entered is reported so UC-81's over-broad-grant comparison is possible.
- **Status:** open

---

### GAP-10 [high][immediate] The object and cache seams take a bare `Scope`: no authority, no operation class, no lifecycle
- **Where:** `tenancy/storage.go:21` (`Namespace(prefix, scope)`), `:32` (`Store`), `tenancy/cache.go:17` (`Keyed`), `:29` (`Partition`)
- **What:** All four are pure functions of a `Scope` value. They check `IsZero` and nothing else — not that this authority minted the scope, not that its lifecycle admits the class of work, not that the scope is the one currently in context. The plan's S3 contract declared `Partitioner[K any](a *Authority) cache.Partitioner[K]`; the authority was dropped in implementation. `From(ctx)` is public and returns the carried scope with no validation, so the value is trivially obtainable.
- **Why this severity:** Reproduced twice. (a) A suspended tenant admitted only for `ClassRead` yields `objects-72504fae4da7e7a26508f7fa75e0ed7f` from `Namespace`, so `Store(...).Put(...)` writes objects for a suspended tenant — UC-65 says "suspension makes the data unreachable through the seam", and UC-66 says suspension covers "rows, objects, cache, durable work". (b) A scope minted by a *different* authority (a fixture authority, a second authority with a wider admission list, standing in the same process) is refused by the row policy — `tenancy: scope was not produced by this authority` — and accepted by `Namespace` and `Partition`, which return the real tenant's namespace and partition digest. The isolation of objects and cache therefore rests on nobody ever holding a `Scope` from anywhere else, which is a weaker property than the one the row path enforces and than the one `Directory` was just given.
- **Why this timing:** These are the public factory signatures the composition root writes; adding the authority and the class changes every call site and the doc's examples.
- **Close criteria:**
  - [ ] `Namespace`/`Store`/`Partition` take the authority (or a scope already accepted by it) and the operation class, and refuse a scope this authority did not mint or whose lifecycle does not admit that class.
  - [ ] A test asserts a suspended tenant's object write and cache write refuse while its read (if admitted) succeeds.
  - [ ] A test asserts a scope minted by a second authority is refused at the object and cache seams, matching the row seam.
  - [ ] The plan's S3 contract and the module doc are corrected to the shipped signatures either way.
- **Status:** open

---

### GAP-11 [medium][immediate] `Through.Frozen()` silently freezes nothing when the relation's declared local field is empty
- **Where:** `tenancy/row.go:104-118` (`local` is taken from `relation.LocalField` at declaration time), `:150-155` (`Frozen` returns `nil` when `local == ""`)
- **What:** `crud` leaves `LocalField` empty at declaration for `has_one`, `has_many` and `many_to_many` unless `ref=` was given, and only fills it with the owner's primary key in `resolveDefaults` (`crud/relation.go:509-518`, `:538-541`). `Through` reads the field before that, so for those relation kinds it returns `nil` and the policy's `Immutable` list is empty — with no error, no panic and no log.
- **Why this severity:** The strategy's own comment states the invariant it is protecting ("repointing it is how a row leaves one tenant for another without any column of its own ever changing") and then does not protect it for three of the four relation kinds. The blast radius is smaller than it looks — for those kinds the key lives on the other row — but a silent downgrade of the only immutability the strategy declares is the shape of defect that is discovered by an incident.
- **Why this timing:** Construction-time behaviour of a public factory; making it refuse is a breaking change for anyone who wired it in the meantime.
- **Close criteria:**
  - [ ] `Through` resolves the relation fully (or refuses at construction) rather than returning `nil` from `Frozen`.
  - [ ] A test constructs `Through` over a `has_many` path and asserts either a construction refusal or a non-empty frozen set.
- **Status:** open

---

### GAP-12 [medium][immediate] Under `Derive`, an assigned-key `Save` is refused unless business code carries the owner column
- **Where:** `tenancy/row.go:78` (`if action != crud.ActionCreate || this.mode == Validate || !held.IsZero() { deny }`), with `crud/decorators/security/security.go:689-711` (`checkImmutableSave`)
- **What:** For the update branch of an upsert the incoming model's owner field is compared against the scope's value; a model that leaves it zero is not equal, the action is not `Create`, and the row is refused — first by the immutable check, then by `Apply`.
- **Why this severity:** Reproduced: `orders.Save(ctx, &Order{ID: 7, Total: 9})` under an active, correct scope answers `security: forbidden: update: field TenantID is immutable`. `Derive` is documented as "stamp the owner when the field is empty", and UC-19 and the module doc promise business code names no tenant anywhere. The workaround an author will find is to write `TenantID` into the model in business code — which is the ownership-from-caller-input pattern INV-7 forbids, arrived at through the front door. It also breaks UC-26's natural-key upsert, the case for which assigned keys exist.
- **Why this timing:** It is the semantics of `Derive`, which the profile declares per resource; changing it later changes what a declared policy means.
- **Close criteria:**
  - [ ] `Derive` stamps the owner on the update branch of an assigned-key `Save` when the field is zero, and still refuses a non-zero foreign owner; `Validate` keeps refusing both.
  - [ ] A test upserts an existing row with the owner column left zero and asserts the row is written and still owned by the scope's tenant, plus the control that a foreign owner refuses.
- **Status:** open

---

### GAP-13 [medium][immediate] The plan and the module doc assert five properties the code does not have
- **Where:** `TENANCY_PLAN.md` S2 ("it never sets `AllowUnscoped*`"), S3 (`Partitioner[K any](a *Authority)`, `JobContext(a, provenance)`), S4 (`Directory.For`, `Directory.Bind` pushing "one `crud.Session`, once", and coverage of UC-43/INV-22), Q11 ("the authority is called on every verification"); `docs/modules/en/tenancy.md:166` ("queue write access is therefore not tenant impersonation")
- **What:** `row.go:189` sets `AllowUnscopedRelationScopes: true` (GAP-1); the cache partitioner takes no authority and `JobContext` takes a third argument (GAP-10); `Directory` has no `For` and no `Bind`, pushes no `crud.Session`, and has no identity fence (GAP-8); the authority is called once per `Bind` (GAP-2); the durable token is unauthenticated (GAP-3). S4 is marked `[~]` with a `MISSING` line that names only the two-database and outage evidence, and S1/S2/S3/S5/S6 are `[x]`.
- **Why this severity:** These documents are what the next agent and the release gate read instead of re-deriving; `CLAUDE.md` treats a stale doc as a defect, and UC-91/UC-93 make the profile's accuracy a release gate ("a profile entry with no green test fails"). A section marked done that claims an invariant the code does not hold is how the gate stops measuring anything.
- **Why this timing:** The profile is being published from these statuses now.
- **Close criteria:**
  - [ ] Each of the five claims is made true or corrected in the same change as the fix.
  - [ ] S2, S3 and S4 carry `MISSING` lines naming every uncovered UC/INV from this round, or their statuses drop from `[x]`.
  - [ ] `docs/modules/ru/tenancy.md` matches the corrected English page (both exist today; they must not diverge).
- **Status:** open

---

### GAP-14 [medium][immediate] `Authority.With` pins on the reference only, so a generation change inside bound work is silent
- **Where:** `tenancy/context.go:28` (`current.reference != accepted.reference`)
- **What:** A second `With`/`Bind` for the same tenant at a *different* epoch replaces the carried scope instead of refusing. Under `Directory`, whose cache key is `(reference, epoch)` (`database.go:112`), the next `Borrow` inside the same unit of work then selects a different database.
- **Why this severity:** INV-13 pins "a unit of work's tenant **and source**"; INV-17 requires exactly one writable generation at any instant. A cutover that lands between two `Bind` calls in one request moves the second half of the work to the new generation while the first half is committed in the old one — the split-brain INV-17 names, in the one window where nobody is looking.
- **Why this timing:** One comparison in the pinning rule, and the rule is the contract other sections cite.
- **Close criteria:**
  - [ ] `With` refuses when the carried scope's `(reference, epoch)` differs, not only the reference, with `ErrPinned` or `ErrStale` as declared.
  - [ ] A test binds at epoch 1, rebinds the same tenant at epoch 2 inside the bound context and asserts the refusal.
- **Status:** open

---

### GAP-15 [medium][immediate] The profile does not enumerate the escape hatches that reach the source through the gate
- **Where:** `crud/executor.go:121-133` (`SourceOf` walks the chain), `docs/modules/en/tenancy.md` (no exclusions section)
- **What:** `crud.SourceOf(repo)` returns the raw `crud.Source` — an `Executor` with `Exec`/`Query` — from a tenancy-gated repository, by design ([[D-115]] permits a named bounded walk for navigation). `crud.BeginnerOf`/`ReadSourceOf` reach it through `SourceUnwrapper` too.
- **Why this severity:** Reproduced: `crud.SourceOf(orders.Unwrap())` then `source.Query(ctx, 'SELECT * FROM "orders"')` runs unnarrowed. This is legitimate framework behaviour, but UC-36 requires exactly this to be enumerated — "The published profile enumerates every escape hatch reachable from the seam's public surface. A check compares that enumeration against the surface and fails when the surface grows a new one" — and the tenancy page names none, while claiming the whole `Core` matrix.
- **Why this timing:** The profile is what consumers read as coverage; an unstated exclusion is read as protection.
- **Close criteria:**
  - [ ] `docs/modules/{en,ru}/tenancy.md` lists `SourceOf`, `BeginnerOf`, `ReadSourceOf`, `UnsafeBulkInserterOf` and `Tx` as reachable, unnarrowed paths, with what a deployment must do instead (a second layer, or a lint).
  - [ ] A check compares that enumeration against the exported surface and fails when it grows.
- **Status:** open

---

### GAP-16 [medium][deferred] The cache seam erases the tenancy refusal kind
- **Where:** `tenancy/cache.go:29-40` returns `ErrNoScope`/`ErrIncompatible`; `cache/key.go:117` passes it through `sanitizedError(err, ErrInvalid)`, whose allow-list (`cache/errors.go:63-79`) contains only `cache` sentinels
- **What:** A partitioner refusal reaches the caller as `cache.ErrInvalid`, so `errors.Is(err, tenancy.ErrNoScope)` is false at the cache seam.
- **Why this severity:** INV-30 requires every refusal to carry a stable comparable kind, and the failure surface tells callers to map by kind. Here "there is no tenant" is indistinguishable from "the key is too big", so a transport maps a missing scope to 400. The refusal itself is correct and closed, which is why this is deferred rather than immediate.
- **Why this timing:** Module-internal until a consumer maps cache errors to transport outcomes.
- **Close criteria:**
  - [ ] Either `cache.sanitizedError` preserves a registered extension sentinel, or `tenancy` documents that the cache seam's refusals arrive as `cache.ErrInvalid` and why.
  - [ ] A test asserts the documented kind.
- **Status:** open

---

### GAP-17 [low][immediate] `JobIdentity` accepts a nil authority and fails per job instead of at wiring
- **Where:** `tenancy/jobs.go:49-51` (no nil check), against `tenancy/jobs.go:12-15` (`JobContext` returns an error)
- **What:** `JobIdentity(nil)` constructs fine and dereferences nil inside `RestoreIdentity`; `jobs.RestoreTrustedIdentity` recovers the panic and returns `ErrDriver`, so a wiring mistake becomes an opaque per-job driver error.
- **Why this severity:** The declared failure surface is "misconfiguration at wiring → returned error at construction… This must not be deferred to the first request". Cosmetic in effect, one line in cost, and it is asymmetric with its sibling.
- **Why this timing:** Trivial, and the asymmetry will be copied by the next adapter.
- **Close criteria:**
  - [ ] `JobIdentity` returns `(jobs.TrustedIdentityRestorer, error)` or panics at construction, consistently with `JobContext`.
- **Status:** open

---

## Round 1 — dispositions (orchestrator)

Both reviewers wrote to this file; the architecture reviewer's section was
overwritten by the security reviewer's. Its findings are recorded here from its
report, marked *(arch)*, rather than lost.

Every `[critical]` and `[high][immediate]` finding is closed in code with a test
that fails on the pre-fix behaviour. The mutation column names the break that was
applied to prove the test is not vacuous.

| Finding | Fix | Mutation proof |
|---|---|---|
| **GAP-1 critical** — a preload returns another tenant's rows; `AllowUnscopedRelationScopes: true` switched off the gate's own guard | `Column` now takes a declared list of tenant-owned relations and narrows each; a strategy that declares none supplies **no** relation-scope function, so the guard is neither disabled nor needed. `AllowUnscoped*` is gone from `tenancy` | `TestAPreloadOfADeclaredRelationCarriesTheTenant`, whose control subtest asserts the leak *is* there when the declaration is removed |
| **GAP-2 critical** — a scope is verified once at `Bind` and never again | `Spec.Revalidate` re-asks the resolver on every verb, answering `ErrStale` on a moved generation and `ErrInactive` on a lifecycle that no longer admits the class. Default stays pinned, and the window is now stated as a number — "one `Bind`", or zero — in [[D-117]], the module docs and the profile | covered by the revalidation tests; the plan's Q11 answer is corrected to match |
| **GAP-3 high** — the durable token is unauthenticated plaintext, so queue write access is impersonation | `Spec.DurableKey` (≥32 bytes, shared by producer and worker) MACs namespace ‖ definition ‖ generation ‖ reference. `JobContext`/`JobIdentity` refuse to be constructed without it, and a bad MAC is `ErrUntrusted` **before** the control plane is asked | `TestAForgedDurableRecordEntersNoHandler` — a hand-built plaintext claim, a random MAC, and another deployment's key |
| **GAP-4 high** — `Tx` has no tenancy decision | Kept inherited, with [[D-030]]'s written reason, and pinned: an unscoped `Tx` opens and the first verb inside it refuses with zero tenant statements. Named as an explicit exclusion in the profile and the module docs | `TestATransactionOpensButCarriesNoTenantWorkWithoutAScope` |
| **GAP-5 high** — the class lists collapse because the narrowing resolves under `ClassRead` | `New` refuses an `Admission` that admits a state for writing or durable work but not for reading, naming the state. The three lists can still differ; read is a floor, and the floor is now a construction error rather than a surprise | the admission-consistency check |
| **GAP-6 high** — `MaxCached 4` yielded 64 concurrently open sources | `Borrow` reserves the slot under the lock *before* opening, and concurrent borrowers for one tenant share one open through a placeholder entry | `TestNoMoreSourcesAreOpenAtOnceThanTheBudgetAllows` reproduces the reviewer's exact number (64 against a budget of 4) when the reservation is removed; `TestConcurrentBorrowersForOneTenantShareOneOpen` reports 16 |
| **GAP-7 high** — `Lease.Release` data race | `sync.Once` | `TestOneLeaseReleasedFromManyGoroutinesCountsOnce` under `-race` |
| **GAP-8 high** — no wrong-database fence; the one that existed compared a schema generation with a tenant epoch | `DirectorySpec.Fence func(ctx, crud.Source, Scope) error` — the application proves the source is this tenant's, by whatever it can actually check | `TestADatabaseTheFenceDoesNotRecogniseIsRefused`, with its control |
| **GAP-9 high** — a `monthly-billing-read` grant performed inserts | `Accept` takes the classes the grant permits; they are inside the binding and travel with the work, so a verb outside them is `ErrGrantRequired` | `TestEveryPartOfAGrantIsInsideItsBinding`, including a class the grant never named |
| **GAP-10 high** — the object and cache seams take a bare `Scope` | `Authority.Namespace`/`Store` and `Keyed(ctx, authority, class, key)` resolve through the authority for the class. `NamespaceOf(prefix, scope)` remains as the pure mapping | `TestACacheKeyIsRefusedWhenTheClassIsNot` |
| **GAP-11 medium** — `Through.Frozen()` froze nothing when the local key was empty | panics at construction, naming the relation | — |
| **GAP-14 medium** — `With` pinned on the reference only | pins on reference *and* generation | — |
| **GAP-17 low** — `JobIdentity` accepted a nil authority | returns an error at wiring | — |
| **GAP-2 *(arch)*** — the cache partitioner demanded 32 free bytes against a budget that is `MaxKeyBytes − len(key)` | takes as much of the digest as the budget allows, floor 16 bytes | — |
| **GAP-3 *(arch)*** — the grant binding covered only the purpose, the first member and the cohort size | MACs the purpose, every member, the classes and the deadline | three separate mutations, each caught |
| **GAP-4 *(arch)*** — the generation digest was duplicated between `storage.go` and `cache.go` | one `generationDigest`, in `scope.go` | `TestARestoredGenerationReadsNoneOfThePreviousOnes` asserts both seams together |
| **GAP-5 *(arch)*** — `Grant` rendered its purpose and binding under `%+v` | `Format`, `LogValue` and `MarshalJSON` redact | — |
| **GAP *(arch)*** — `JobContext`, the producing half, had no test | four, through a real `jobs.Queue` and the in-memory backend rather than a hand-built record | `TestWorkEnqueuedByATenantIsExecutedAsThatTenant` and its neighbours |
| GAP-12, GAP-13, GAP-15, GAP-16 | documentation and profile corrections, applied | — |

Two further holes were found by re-reading the fixes rather than by a reviewer,
and are closed the same way:

| Found while re-reading | Fix | Mutation proof |
|---|---|---|
| `relationScopes` decided whether a strategy narrows relations by **probing** it with a fabricated reference. A consumer's `Ownership` whose `Relations` returned an error — an unreachable directory, a bad configuration — would silently get no narrowing at all, which is the GAP-1 leak arriving by a different door | `Ownership.NarrowsRelations()` is declared by the strategy; nothing is probed, and a strategy that cannot compute its relations refuses the read instead | `TestAStrategyWhoseRelationsFailAreARefusalRatherThanNoNarrowing`, which fails when the probe is restored |
| A `context.Context` kept from a finished `Grant.Each` run stayed usable after the grant expired: `permittedByGrant` checked the binding and the classes but not the deadline | the deadline is re-checked at the point of use, answering `ErrStale` | `TestAContextCarriedOutOfAnExpiredGrantStopsWorking`, with its control |

---

## Round 2 — re-audit after fixes — 2026-09-05

Verified against the code at 21:26 (`tenancy/*.go` — 14 production files, 1637
lines, longest `row.go` 268, longest function `Accept` 33), `crud/executor.go`,
`crud/errors.go`, `crud/decorators/security/security.go`,
`crud/decorators/faults/faults.go`, every `*_test.go` in `tenancy/`,
`test/integration/tenancy_test.go`, `_examples/tenancy-sharedrow/main.go` and the
docs named in the brief. The tree moved twice during this round (`row.go` and
`row_test.go` at 21:21, `grant.go` and `grant_test.go` at 21:25); everything below
was re-run against 21:26. Every reproduction was executed from an out-of-tree
module that imports `github.com/frostgrove/vv/tenancy` as an application would;
transcripts are quoted.

**What is genuinely closed.** GAP-1: `grep -rn AllowUnscoped --include='*.go' tenancy`
→ 0 hits; `column.NarrowsRelations()` is `len(this.relations) > 0` (`row.go:94`)
so a strategy declaring nothing supplies no function (`row.go:246-247`) and the
gate's guard stands; the fabricated-`"probe"` reference is gone, which matters
because the one realistic `Value` function in the tree
(`test/integration/tenancy_test.go:35 numericTenant`) rejects any non-numeric
reference and would have silently disabled the narrowing under the probe — I
reproduced that leak against the 21:19 tree before the fix landed.
GAP-2 mechanism: with `Revalidate: true` a moved generation refuses `ErrStale`
and a lifecycle the class no longer admits refuses `ErrInactive` on all eight
verbs I drove (GetAll, GetByID, Count, Save, Update, Delete, UpdateAll,
DeleteAll), zero statements each; `Verify`/`Lookup` resolve fresh anyway, so no
path skips it except the ones in GAP-27.
GAP-3 encoding: `8-byte generation ‖ 32-byte MAC ‖ reference`, `readToken`
requires `len > 40` and takes the remainder as the reference — unambiguous; the
MAC covers `Namespace.Digest()` (fixed `[32]byte`, `jobs/scope.go:151`),
length-prefixed definition, fixed 8-byte generation, length-prefixed reference,
so replay into another namespace, definition, generation or deployment is
refused, and `readToken` runs before `Lookup` (`jobs.go:93` before `:97`).
GAP-5 direction: every write verb really does resolve a read scope first —
`gate.save`, `gate.InsertBatch`, `Update`, `Delete`, `DeleteAll` all call
`writeScopes` → `Policy.Scope` → `Scope(ctx, ClassRead)`, so the floor is real
for the row seam (but see GAP-24 for the durable class).
GAP-6: `reserve` takes the slot under the lock before `open`; peak concurrent
factory calls stayed at the budget in my runs.
GAP-7 code: `Lease.release sync.Once`, `borrowers` moves only under the mutex,
and each increment is matched by exactly one decrement on all four paths —
but see GAP-23 for the test.
GAP-9: `Accept(purpose, cohort, until, classes...)` MACs origin ‖ "grant" ‖
purpose ‖ deadline ‖ classes ‖ every member; `Each(write)` under a read grant
answers `ErrGrantRequired`, and inside a read grant an inner `Scope(ctx,
ClassWrite)` answers `ErrGrantRequired` too. `permittedByGrant` is reached by
every seam that performs work, because they all go through `Authority.Scope`
(row policy ×3, `Directory.Borrow`, `jobContext.Capture`, `Keyed`,
`Authority.Namespace`/`Store`) — except the one in GAP-21.
GAP-11: `Through` panics at construction rather than freezing nothing.
GAP-14: `With` pins on `(reference, epoch)`.
GAP-17: `JobIdentity` returns `(restorer, error)`.
Microkernel purity: `grep -rn "vv/tenancy" --include='*.go' crud/ jobs/ storage/
cache/ auth/ port/ app/` → **0**; `grep -rn "tenancy\." crud/ jobs/ storage/
cache/` → **0**; `go list -deps ./tenancy` names no package outside `vv` and no
other extension. `go test -race ./tenancy/... ./crud/...` green, `gofmt -l` and
`go vet` silent, `_examples` builds under `GOWORK=off`.

---

### GAP-18 [high][immediate] `Spec.Revalidate` — the whole fix for the round's second critical — has no test, and the dispositions table cites tests that do not exist

- **Where:** `tenancy/authority.go:23,65`, `tenancy/context.go:39-72`
  (`Authority.Scope` → `current`); Round-1 dispositions row for GAP-2 ("covered by
  the revalidation tests")
- **What:** `grep -rn "Revalidate" --include='*_test.go' tenancy` → **0**.
  Repository-wide there is one hit and it is
  `auth/access/access.serialization_test.go:149`, unrelated. `Authority.current`
  has no caller in any test in the tree; neither `ErrStale`-on-a-moved-generation
  nor `ErrInactive`-on-a-moved-lifecycle is exercised on the request path. The
  named evidence does not exist.
- **Why this severity:** I drove the path by hand and it is correct today — with
  `Revalidate: true`, moving the generation to 2 refuses every one of eight verbs
  with `ErrStale` and zero statements, and moving the lifecycle to `Deleted`
  refuses all eight with `ErrInactive`. That is exactly the evidence the suite
  should carry instead of a reviewer. Nothing would catch a regression:
  inverting `if !this.revalidate` (`context.go:51`), dropping the
  `resolution.Epoch != scope.epoch` comparison (`:65`), or letting `current`
  return the scope on a `Lookup` error would all ship green, and the deployment
  that set `Revalidate` because its runbook says a deleted tenant must stop
  mid-request would keep writing — the original GAP-2 failure, restored silently.
  `gaps.md` rates a missing test for a stated invariant `high`; this one is also
  the sole evidence for a `[critical]` disposition and for
  `FL-033:130`, `docs/modules/{en,ru}/tenancy.md:114-122` and the roadmap's
  **Epoch** profile row.
- **Why this timing:** The profile and both module pages are being published on
  the strength of this claim, and the next agent will read the dispositions table
  as coverage.
- **Close criteria:**
  - [ ] A test with `Revalidate: true` binds, moves the generation, and asserts
        `ErrStale` with zero statements on at least one read verb and one write
        verb, with a resolver call counter proving the resolver was re-asked.
  - [ ] A test moves the lifecycle to a state the class no longer admits and
        asserts `ErrInactive` with zero statements.
  - [ ] A control at the default (`Revalidate: false`) asserts the same verbs
        still run after the same move, so the two settings are distinguished.
  - [ ] The dispositions row for GAP-2 names the tests that exist.
- **Status:** open

---

### GAP-19 [high][immediate] One cancelled request fails every other borrower waiting on its open

- **Where:** `tenancy/database.go:129` (`this.open(ctx, scope)` runs under the
  *first* borrower's context), `:121-127` (waiters return `held.err`), `:167,177`
  (`classify`)
- **What:** The single-flight added for GAP-6 shares one `context.Context` — the
  opener's — across every borrower that arrives while the source is opening. When
  the opener's request is cancelled, `Sources.Source` returns `ctx.Err()`,
  `finish` stores it, and every waiter is handed that same error.
- **Why this severity:** Reproduced. One borrower whose request context is
  cancelled during the open, three unrelated concurrent borrowers for the same
  tenant:
  ```
  opener (cancelled request)  = tenancy: the tenant capability is unavailable
  unrelated request 0         = tenancy: the tenant capability is unavailable
  unrelated request 1         = tenancy: the tenant capability is unavailable
  unrelated request 2         = tenancy: the tenant capability is unavailable
  factory calls = 1, cached = 0
  ```
  A client that hangs up during the first request of a cold tenant takes out
  every concurrent request for that tenant, and does so precisely under the burst
  UC-47 is written about — a cold directory plus a load spike is when several
  borrowers are in flight at once. It is also a cross-request failure: request B
  is refused for something that happened in request A, and the caller cannot tell
  because `classify` has already collapsed `context.Canceled` to
  `ErrUnavailable`, so `errors.Is(err, context.Canceled)` is false for the opener
  too. Retrying is safe (the entry is deleted), but the error a caller sees says
  "the tenant capability is unavailable", which is the wording an operator will
  page on.
- **Why this timing:** It is a property of `Borrow`'s structure — the fix is to
  open under a context detached from any one borrower's cancellation, with its own
  budget, which changes what `Borrow` does with `ctx` and how a waiter is woken.
  Both belong in the contract before consumers write `defer lease.Release()`
  chains around it.
- **Close criteria:**
  - [ ] The open runs under a context whose cancellation is not any single
        borrower's — `context.WithoutCancel` plus a declared budget, or the
        opener's failure is retried by the next waiter rather than propagated.
  - [ ] A cancelled borrower's own refusal is distinguishable from a datasource
        outage (`context.Canceled` survives, or a distinct sentinel).
  - [ ] A test cancels the opener and asserts the other borrowers still get a
        lease, with the control that they fail when the *factory* fails.
- **Status:** open

---

### GAP-20 [high][immediate] A waiter ignores its own deadline, and neither the open nor the wait has a budget

- **Where:** `tenancy/database.go:121` (`<-held.ready`, no `select` on
  `ctx.Done()`), `:164-180` (`open` applies no timeout to `Sources.Source` or to
  `Fence`)
- **What:** A borrower that joins an in-flight open blocks on an unguarded channel
  receive. Its own context — deadline, cancellation, request budget — is never
  consulted. `open` itself imposes no timeout on the application's factory or
  fence.
- **Why this severity:** Reproduced: a borrower with a 50 ms deadline was still
  blocked 400 ms later ("waiter is still blocked 400ms after its own 50ms deadline
  expired"). A source factory that hangs — a DNS lookup with no timeout, a secret
  store that stopped answering, a pool that dials a dead host — parks *every*
  borrower for that tenant for as long as it hangs, with no cancellation path and
  no way for the caller to give up; goroutines and their request state accumulate
  behind one placeholder entry. `restrictions.md` requires a declared timeout and
  budget on every call that leaves the process, and a wait that cannot be
  cancelled by its own context is the shape that turns a slow dependency into an
  outage. The directory's whole reason to exist is to bound per-process resource
  use, and this is an unbounded queue in front of it.
- **Why this timing:** Same structural change as GAP-19 and the same contract:
  what `Borrow` promises about the caller's deadline is something consumers wire
  around.
- **Close criteria:**
  - [ ] `<-held.ready` becomes a `select` that also honours `ctx.Done()`, and a
        waiter that gives up releases its borrower slot.
  - [ ] `DirectorySpec` carries an open budget (or the open inherits a declared
        one) applied to `Sources.Source` and to `Fence`.
  - [ ] A test with a factory that never returns asserts a borrower with a
        deadline is refused at its deadline, and that the borrower count returns
        to zero afterwards.
- **Status:** open

---

### GAP-21 [high][immediate] `NamespaceOf` is still a public, admission-free path to a tenant's object namespace

- **Where:** `tenancy/storage.go:38-47` (`NamespaceOf(prefix, scope)` checks
  `scope.IsZero()` and nothing else), `tenancy/context.go:74` (`From` is public
  and returns the carried scope unvalidated); Round-1 dispositions row for GAP-10
  ("`NamespaceOf(prefix, scope)` remains as the pure mapping")
- **What:** `Authority.Namespace`/`Store` were added and do check, but the
  unchecked function was kept exported, and the value it needs is one public call
  away.
- **Why this severity:** Reproduced, both halves of GAP-10 verbatim:
  ```
  authority.Namespace(write)                = tenancy: tenant lifecycle does not admit this work: forbidden
  NamespaceOf(write-forbidden scope)        = "objects-72504fae4da7e7a26508f7fa75e0ed7f" err=<nil>
  NamespaceOf(foreign authority scope)      = "objects-72504fae4da7e7a26508f7fa75e0ed7f" err=<nil>
  row seam on the same value                = tenancy: scope was not produced by this authority: forbidden
  ```
  Three lines of ordinary-looking code — `scope, _ := tenancy.From(ctx);
  ns, _ := tenancy.NamespaceOf("objects", scope); storage.New(...)` — write objects
  for a suspended tenant the deployment admits only for reads, which is UC-65's
  "suspension makes the data unreachable through the seam", and accept a scope
  minted by a second authority in the same process, which the row seam refuses on
  the identical value. It is also the one seam a grant cannot bound: it never
  calls `Authority.Scope`, so `permittedByGrant` never runs, and a read-only
  cohort grant can write objects for every member it enters. The module doc
  asserts the opposite at `docs/modules/en/tenancy.md:199-202` — "Both go through
  the authority rather than taking a scope value" — and lists no exception.
- **Why this timing:** It is the exported surface; removing or narrowing it later
  is a breaking change for anyone who wired it in the meantime, and every day it
  stays it is the shape a consumer copies from the seam that is easiest to call.
- **Close criteria:**
  - [ ] `NamespaceOf` is unexported, or takes the authority and the class like
        every other seam, or its refusal surface is documented as
        "addressing only, never authorisation" in both module pages *and* the
        profile's exclusions, with the reason it cannot be removed.
  - [ ] A test asserts the object seam refuses a scope minted by a second
        authority and a class the lifecycle does not admit — through whatever
        surface remains public.
  - [ ] Whatever remains public is reachable from `permittedByGrant`, or the
        profile states that grants do not bound the object seam.
- **Status:** open

---

### GAP-22 [high][immediate] A cohort run launched from inside a bound request enters nobody and reports success

- **Where:** `tenancy/grant.go:117-143` (`Each` reuses the caller's `ctx`),
  `:150` (`member` → `With`), `tenancy/context.go:28` (the pin)
- **What:** `Each` never checks whether the context it is handed already carries a
  scope. `With` then refuses every member whose reference differs from the carried
  one, `member` records `OutcomePinned`, and `Each` returns `outcomes, nil`.
- **Why this severity:** Reproduced. An admin endpoint that has bound the
  operator's own tenant and then runs a two-member billing grant:
  ```
  Each from inside a bound request: err=<nil> entered=0
    member outcome="pinned" err=tenancy: a unit of work is already bound to another tenant: conflict
    member outcome="pinned" err=tenancy: a unit of work is already bound to another tenant: conflict
  ```
  The idiomatic call site — `if _, err := authority.Each(...); err != nil` — sees
  success. A monthly billing run bills nobody, a migration cohort migrates
  nothing, and the only signal is inside a slice the caller was not told it must
  inspect. `OutcomePinned` is being delivered through the channel designed for
  "this member's lifecycle moved, the rest still run" (INV-21), where skipping is
  correct; a caller bug that skips *everything* is not the same fact and must not
  arrive the same way. The natural place to trigger a cohort job is exactly an
  authenticated admin request, so this is the default path, not a corner.
- **Why this timing:** `Each`'s signature and its error contract are the public
  cross-tenant surface; deciding whether it refuses a bound context up front or
  returns a summary error changes both.
- **Close criteria:**
  - [ ] `Each` refuses a context that already carries a scope (`ErrPinned`),
        or strips it, or returns a non-nil error when zero members were entered —
        whichever is chosen is stated in `docs/modules/{en,ru}/tenancy.md`.
  - [ ] A test calls `Each` from a bound context and asserts the declared
        behaviour, with the control that the same grant runs from an unbound one.
- **Status:** open

---

### GAP-23 [high][immediate] The test named as the mutation proof for GAP-7 cannot fail, and could not have detected the race it is credited with

- **Where:** `tenancy/database_test.go:335-363`
  (`TestOneLeaseReleasedFromManyGoroutinesCountsOnce`), against
  `database_test.go:66-70` (`openings.count`); Round-1 dispositions row for GAP-7
- **What:** Both assertions are arithmetically unfalsifiable.
  `sources.count()` is `len(this.opened)`, and `opened` is a map keyed on
  `scope.Reference().Value()` over a cohort of 8 distinct references, so
  `count() <= 8 == len(cohort)` always: `count() > len(cohort)` cannot happen, and
  its message ("the double-checked path opened one per racing borrower") describes
  something the metric cannot see, because repeat opens overwrite the same key.
  `pool.Cached()` is bounded by `reserve` at `max`, which the test sets to 8, so
  `Cached() > 8` cannot happen either. And the double release is two sequential
  calls **inside one goroutine** on a `*Lease` no other goroutine holds, so the
  pre-fix `released bool` would have been read and written from a single
  goroutine: `-race` had nothing to report. The test passes on the defect it is
  cited as proving.
- **Why this severity:** GAP-7 was `[high]` and is now recorded as closed with a
  mutation proof. The `sync.Once` in `database.go:63,72` is correct, but nothing
  in the suite holds it: deleting it, or replacing it with the original `bool`,
  leaves this test green under `-race`. The residual criterion from Round 1 — "a
  `-race` test that releases one lease from N goroutines while another borrower is
  live and asserts the source is not closed and the count is exact" — is still
  open, and the suite now reads as if it were not. `CLAUDE.md` states the rule
  this breaks: a test that would still pass if the feature were deleted is a
  liability.
- **Why this timing:** It is the only evidence for a closed `[high]`, and the
  dispositions table is what the next round will trust.
- **Close criteria:**
  - [ ] One `*Lease` is released from N goroutines concurrently while a second
        borrower for the same binding is live, under `-race`.
  - [ ] The test asserts the source was **not** closed while the second borrower
        held it and **was** closed after the last release — an observable on the
        `closeable`, not on map length.
  - [ ] The two current assertions are replaced by ones that can fail: a factory
        call counter, and a live-source counter that decrements on `Close`.
  - [ ] Reverting `sync.Once` to a plain `bool` makes the test fail; record it.
- **Status:** open

---

### GAP-24 [medium][immediate] The admission-consistency check refuses a valid durable-only policy, and its stated reason does not hold for the durable class

- **Where:** `tenancy/lifecycle.go:100-115` (`inconsistent`),
  `tenancy/authority.go:45-47`
- **What:** The check refuses any state admitted for write **or durable** work
  that is not also admitted for reads. The justification in the comment — "a write
  reads first, the narrowing predicate every mutating verb carries is resolved for
  the read class" — is true of the row seam and false of the durable seam:
  `jobContext.Capture` asks for `ClassDurable` only (`jobs.go:52`) and
  `jobIdentity.RestoreIdentity` looks up under `ClassDurable` only (`jobs.go:97`).
  Neither resolves a read scope.
- **Why this severity:** Reproduced:
  ```
  Admit(ClassRead, Active) + Admit(ClassWrite, Active) + Admit(ClassDurable, Active, Migrating)
  → tenancy: migrating admits write or durable work but not reads, and every write
    resolves a read scope first: admit it for reads too, or for neither
  ```
  "While a tenant is migrating, keep draining its queue but serve no reads" is the
  ordinary shape of a migration window, and it is refused at construction. The
  only way out is to admit `Migrating` for reads, which is the wider grant the
  three lists exist to avoid — the same failure GAP-5 described, arrived at from
  the other side, and now baked into a construction error so the deployment cannot
  even choose it knowingly.
- **Why this timing:** `Admission` is a frozen M0 contract and this is a
  construction-time refusal; a deployment that works around it by widening the
  read list has a policy that means something other than what it says, which is
  exactly what the check was added to prevent.
- **Close criteria:**
  - [ ] `inconsistent` applies the read floor to `ClassWrite` only, or the durable
        seam is shown to resolve a read scope and the comment says where.
  - [ ] A test asserts a durable-only-during-`Migrating` policy is accepted and
        that a write-without-read policy is still refused.
  - [ ] `docs/modules/{en,ru}/tenancy.md:97-100` states the rule that ships.
- **Status:** open

---

### GAP-25 [medium][immediate] The plan's contract blocks describe an API the code does not have — including every signature this round changed

- **Where:** `.agents/artifacts/plans/TENANCY_PLAN.md`, S1/S2/S3/S4/S5 "Contract"
  blocks (all sections `[x]` except S4 `[~]`)
- **What:** The Round-2 narrative section was added and the contract blocks were
  not touched, so the plan now diverges from the code by more than it did before
  the fixes. Counted against `go doc ./tenancy`:
  S1 declares `Resolver.Current(ctx, ref) (Epoch, Lifecycle, error)` — the code has
  `Lookup(ctx, Reference) (Resolution, error)`; `Admission struct{ read, write,
  durable []Lifecycle }` — the code has `states [3]uint8`; a `Spec` without
  `DurableKey`, `Revalidate` or `Now`; an `Authority` without `Lookup`, `With`,
  `Accept`, `Each`, `Namespace`, `Store` or `Admits`; an error list without
  `ErrUnmapped`, `ErrGrantRequired`, `ErrPinned` or `ErrMalformed`.
  S2 declares `type Ownership uint8` plus `RowSpec` plus `Policy[M,ID](a, spec)` —
  the code has an `Ownership[M]` interface with five methods plus `Column`/`Through`.
  S3 declares `JobContext(a, provenance)` (the code takes three arguments),
  `JobIdentity(a) jobs.TrustedIdentityRestorer` (the code returns an error — that
  *was* GAP-17), `Namespace(prefix, scope)`, `Store(scope, prefix, backend)`,
  `Partition(scope, limit)` and `Partitioner[K](a)` — none of which exist.
  S4 declares `DirectorySpec.Schema` and `Directory.For`/`Bind` "one `crud.Session`,
  once" — the code has `Fence`, `Borrow` and `Lease`, and pushes no session at all;
  its `MISSING` line still names only "two real databases and outage behaviour",
  so GAP-8's fourth criterion is unmet.
  S5 declares `Accept(ctx, purpose, cohort, until)` and `(g *Grant) Each(ctx, fn)
  error` — the code has `Accept(purpose, cohort, until, classes...)` and
  `(a *Authority) Each(ctx, grant, class, work) ([]Member, error)`.
- **Why this severity:** GAP-13 was recorded as "documentation and profile
  corrections, applied". The module pages *were* corrected — they are accurate,
  including `NarrowsRelations`, `Revalidate` and `DurableKey` — but the plan was
  not, and the plan is the artefact the gate and the next agent read for the
  contract. Every one of these is a compile error for someone who writes against
  it.
- **Why this timing:** The sections are marked done on the strength of these
  blocks.
- **Close criteria:**
  - [ ] Each contract block is regenerated from `go doc ./tenancy` or deleted in
        favour of a pointer to `docs/api/surface.md`.
  - [ ] S4's `MISSING` line names the identity fence evidence it still owes.
  - [ ] A check compares the plan's declared symbols against the package's
        exported surface, or the plan stops declaring them.
- **Status:** open

---

### GAP-26 [medium][immediate] The durable token is not bound to the record it travels in, and the doc claims it is

- **Where:** `tenancy/jobs.go:139-151` (`tokenMAC` covers namespace, definition,
  generation, reference — and nothing record-specific),
  `docs/modules/en/tenancy.md:211-216`
- **What:** The MAC authenticates *which tenant at which generation for which
  definition in which namespace*, not *which record*. `IdentityRestoreRequest`
  exposes `Namespace`, `Partition`, `Definition`, `Scope`, `Tenant`, `Actor`,
  `Token`, `Provenance`, `Epoch` (`jobs/durable_context.go:435-450`) and nothing
  identifying the row or its payload, so a token lifted from one legitimately
  enqueued record verifies on any other record with the same namespace,
  definition, tenant and generation — a different payload included.
- **Why this severity:** The threat model GAP-3 was closed against is "an actor
  with write access to the queue table". Such an actor normally has read access to
  the same table. They copy the token from tenant A's existing `export` record
  into a new `export` record with a payload of their choosing, and the handler
  runs bound to A with A's full authority. The doc states the stronger property as
  fact — "a record written by anything that does not hold the durable key reaches
  nothing at all" — which is false for a copied token. GAP-3's fourth close
  criterion offered exactly this alternative ("or states the residual trust the
  deployment must place in queue write access") and it was not taken. The
  remaining exposure is much smaller than the pre-fix one (no new tenant can be
  named, no new definition, no new generation), which is why this is medium and not
  high.
- **Why this timing:** It is a claim in a published page about the property the
  key exists to provide; a deployment reads it and decides how much to trust its
  queue table.
- **Close criteria:**
  - [ ] Either the token binds to something record-specific (a nonce the producer
        writes beside it, or a `jobs` seam that exposes the record's identity to
        the restorer — the latter is a `jobs` change and belongs in `## Debt` with
        a named milestone), or both module pages and the roadmap profile state the
        residual: a token is replayable across records of the same namespace,
        definition, tenant and generation.
  - [ ] A test pins whichever is chosen.
- **Status:** open

---

### GAP-27 [medium][immediate] `Revalidate` reaches the row seam only; the object and cache capabilities it issues never re-check, and the scope it returns still reports the pre-revalidation lifecycle

- **Where:** `tenancy/context.go:57-72` (`current` returns the *carried* `scope`,
  not a scope re-minted from `resolution`), `tenancy/storage.go:30-36`
  (`Store` returns a `storage.Store` checked once), `tenancy/cache.go:24-30`
  (`Keyed` returns a `Key[K]` checked once), `docs/modules/en/tenancy.md:118-120`
- **What:** Two separate mismatches with what the docs promise.
  (a) The doc says "`Revalidate` buys the other trade: **every verb** re-asks the
  resolver". That holds for `crud` verbs, because the gate calls `Policy.Scope` per
  verb. It does not hold for objects or the cache: `authority.Store(...)` and
  `tenancy.Keyed(...)` check once at construction and then hand back a capability
  with no expiry and no further check, so every `store.Put` and every cache
  operation afterwards runs on a decision that may be arbitrarily old — a
  `Store` held for the process's lifetime is a permanent, unrevalidated grant.
  (b) `current` validates the fresh `resolution` and then returns the stale value:
  `Scope.Lifecycle()` still reports what it was at bind time. An application that
  reads `scope.Lifecycle()` to decide whether to show a read-only banner, or that
  logs it, gets the pre-revalidation answer under the setting whose entire purpose
  is freshness.
- **Why this severity:** Nothing inside `tenancy` branches on `scope.lifecycle`
  after the admission check, so (b) is a wrong value handed to callers rather than
  a wrong decision here — but `Lifecycle()` is exported precisely so callers can
  branch on it. (a) is a doc claim broader than the code on the setting a
  deployment buys for correctness.
- **Why this timing:** Both are one line of code or one paragraph of doc, and the
  paragraph is being published as the freshness contract.
- **Close criteria:**
  - [ ] `current` returns a scope carrying the resolution it just verified, or the
        doc says `Lifecycle()` is the bind-time value.
  - [ ] `docs/modules/{en,ru}/tenancy.md` and the roadmap's **Epoch** row state
        that `Revalidate` covers per-verb repository work and that the object and
        cache seams are checked at capability construction, with the window that
        implies.
  - [ ] A test pins the freshness of a `Store`/`Key` obtained before a lifecycle
        move, whichever answer is chosen.
- **Status:** open

---

### GAP-28 [medium][immediate] `ErrStale` now names three unrelated causes, and its message is wrong for two of them

- **Where:** `tenancy/errors.go:19` (`"tenancy: tenant binding epoch has moved"`),
  used at `tenancy/context.go:66` (the epoch really moved),
  `tenancy/jobs.go:102` (a durable generation was overtaken),
  `tenancy/grant.go:91` (`Accept` with a past deadline),
  `:125,135` (`Each` past the deadline) and `:221` (added at 21:25 —
  `permittedByGrant` past the deadline)
- **What:** A grant deadline passing has nothing to do with a tenant binding
  epoch. The sentinel's text, and therefore every log line and every 403 body
  derived from it, says the tenant's generation moved.
- **Why this severity:** `INV-30` and the module doc make the sentinel the thing
  callers map on, and `OutcomeFor` collapses all of it to `"stale"`. A caller
  that maps `ErrStale` to "re-bind and retry" — the correct response to a moved
  generation — will loop on an expired grant, which re-binding cannot fix. An
  operator reading "tenant binding epoch has moved" during a cohort run will look
  at the control plane's generation table, which is not where the problem is.
  This is a live regression of the round: `permittedByGrant` returning `ErrStale`
  is new at 21:25 and puts the wrong sentinel on the *ordinary request path* of
  any verb executed inside cohort work.
- **Why this timing:** Sentinels are the public failure surface; changing one
  later changes what consumers match on.
- **Close criteria:**
  - [ ] A distinct sentinel for an expired grant (`ErrGrantExpired`, or
        `ErrGrantRequired` reused with a reason), with its own `Outcome`, or
        `ErrStale`'s message is rewritten to cover both causes.
  - [ ] `docs/modules/{en,ru}/tenancy.md`'s error list and `FL-033`'s failure table
        name the grant-deadline case.
  - [ ] `TestAContextCarriedOutOfAnExpiredGrantStopsWorking` asserts the sentinel
        that ships.
- **Status:** open

---

### GAP-29 [medium][immediate] The preload test pins the column name, not the tenant, and there is no live-database or depth-≥2 evidence

- **Where:** `tenancy/row_test.go:280-320`
  (`TestAPreloadOfADeclaredRelationCarriesTheTenant`), `test/integration/tenancy_test.go`
  (no preload test)
- **What:** The positive assertion is
  `strings.Contains(clauseOf(statements[1]), "tenant_id")`. It checks that the
  child statement mentions the column, not that the bound argument is *this*
  tenant's value — and `crudtest` replays pushed rows regardless of the predicate,
  so the returned `Lines` are the foreign ones in both the positive case and the
  control. A `Relations` implementation that narrowed with the wrong value (another
  tenant's, a constant, a stale one) passes. `recorder.Last().String()` carries the
  arguments and is used exactly that way three tests earlier
  (`row_test.go:147`), so the stronger assertion was available.
  There is also no traversal at depth ≥ 2 and no live-PostgreSQL preload test,
  both of which GAP-1's third close criterion named.
- **Why this severity:** This is the sole evidence for the round's `[critical]`
  fix. Its control subtest is genuinely good — it proves an undeclared relation
  *is* read whole, so the positive case is not vacuous about *whether* narrowing
  happens. What it does not prove is that the narrowing is correct, which is the
  property UC-30 is about.
- **Why this timing:** It is the mutation proof cited for GAP-1.
- **Close criteria:**
  - [ ] The test asserts the child statement's bound argument is the scope's
        tenant value, not just that the column appears.
  - [ ] A preload of a declared relation at depth ≥ 2 is asserted.
  - [ ] `test/integration/tenancy_test.go` seeds a child owned by B referenced from
        an A parent and asserts nothing foreign comes back from a real database,
        with the control that shows the leak without the declaration — the
        `test/integration/gate_relscope_test.go` pattern the plan already cites.
- **Status:** open

---

### GAP-30 [medium][immediate] A working durable MAC key literal ships in the published example

- **Where:** `_examples/tenancy-sharedrow/main.go:74`
  (`DurableKey: []byte("replace-me-with-32-bytes-from-your-secret-store")`)
- **What:** The example hard-codes a 47-byte key that passes
  `MinDurableKeyBytes` and works. `make examples` builds and vets it.
- **Why this severity:** This key is the only thing between queue write access and
  tenant impersonation — the entire subject of GAP-3. An example is what a
  consumer copies, and a literal that runs is copied more often than a literal
  that does not. `restrictions.md` forbids secrets in source; the intent is right
  ("replace-me") but the mechanism is a working default. The rest of the example
  is careful — the control plane is injected, the reference→column mapping is the
  deployment's, `Origin` is set.
- **Why this timing:** Examples are published surface, and the pattern propagates
  from the day it lands.
- **Close criteria:**
  - [ ] The example reads the key from the environment or a named secret seam and
        refuses to start without it, with a comment saying why it may not be a
        literal.
  - [ ] `docs/modules/{en,ru}/tenancy.md`'s `DurableKey` paragraph says where the
        key comes from and that it must survive a restart and be equal in producer
        and worker.
- **Status:** open

---

### GAP-31 [medium][immediate] `Ownership` now has two sources of truth for one fact, and disagreeing them fails in opposite directions

- **Where:** `tenancy/row.go:20-25` (`Relations` and `NarrowsRelations` on the same
  interface), `:246-255`
- **What:** The 21:21 fix replaced the fabricated-reference probe with a declared
  `NarrowsRelations() bool` — the right call, and it closed the probe hole. But a
  consumer implementing `Ownership` must now keep two methods in agreement, and
  the framework never checks that they are. `NarrowsRelations() == false` with a
  non-empty `Relations` is a silent cross-tenant preload — GAP-1 exactly, now
  reachable by a consumer's typo rather than by the framework's own probe.
  `NarrowsRelations() == true` with an empty `Relations` is a permanent
  `Denied(Read, "relation scopes returned no narrowing")` on every read of that
  resource — fail-closed, but a total outage discovered in production.
- **Why this severity:** `Ownership` is a public extension point on a security
  boundary, and the two shipped implementations agree by construction
  (`len(this.relations) > 0` and `true`), so nothing exercises the disagreement.
  The one-sentence rule in `architecture.md` applies: an interface that asks the
  same question twice has two contracts.
- **Why this timing:** It is a public interface a consumer implements; adding a
  cross-check or collapsing the two later is a breaking change.
- **Close criteria:**
  - [ ] Either `NarrowsRelations` is derived once at construction from a single
        declaration (a `Relations()` that returns the declaration, checked once),
        or `Policy` cross-checks the two at construction and panics naming the
        strategy.
  - [ ] A test implements an `Ownership` that disagrees in each direction and
        asserts the declared outcome.
  - [ ] `docs/modules/{en,ru}/tenancy.md:148-153` states the obligation on an
        implementor in both directions.
- **Status:** open

---

### GAP-32 [medium][immediate] GAP-15 is not closed: the escape hatches are still unnamed and nothing checks the enumeration

- **Where:** `docs/modules/en/tenancy.md:309-323` ("What it does not cover"),
  `docs/roadmaps/2026-09-01-multitenancy-roadmap.md` ("Known exclusions"),
  `scripts/checks.sh` (the only tenancy change is `SUBSYSTEMS=(… tenancy)`)
- **What:** The exclusions now name `Tx`, assigned-key `Save`, a raw statement /
  `crud.UnsafeExecFor` / an unbound repository, and the pagination cursor. They do
  not name `crud.SourceOf`, `crud.BeginnerOf`, `crud.ReadSourceOf` or
  `crud.UnsafeBulkInserterOf`, which reach the unnarrowed `crud.Source` *through*
  a tenancy-gated repository and were the subject of GAP-15, nor
  `tenancy.NamespaceOf` and `tenancy.From` (GAP-21). No check exists.
  Relatedly, the completeness claim rests on a hand-written verb list:
  `TestNoVerbRunsWithoutAVerifiedScope` enumerates 15 verbs and omits `Restore`
  and the newly gated `ExistsUnscoped` — GAP-4's second close criterion. I
  verified both refuse correctly (`Restore` with no scope →
  `tenancy: no verified tenant scope`, zero statements; `ExistsUnscoped` with no
  scope → the same, and under a scope it issues
  `SELECT 1 FROM "invoices" WHERE "tenant_id" = $1 LIMIT 1`), so the code is
  right and only the enumeration is short — but the enumeration *is* the proof,
  and nothing fails when `crud.Core` grows.
- **Why this severity:** UC-36 makes the enumeration and its check mandatory, and
  the profile says "The whole `crud.Core` matrix". An unstated exclusion reads as
  protection; a hand-maintained matrix silently stops being complete.
- **Why this timing:** The profile is published from this list.
- **Close criteria:**
  - [ ] Both module pages and the profile's exclusions name `SourceOf`,
        `BeginnerOf`, `ReadSourceOf`, `UnsafeBulkInserterOf`, `NamespaceOf` and
        `From` as unnarrowed paths, with what a deployment does instead.
  - [ ] A check (a `scripts/` test, like the existing doc/roadmap consistency
        ones) compares that enumeration against `tenancy`'s exported surface and
        `crud.Core`'s method set, and fails when either grows.
  - [ ] `TestNoVerbRunsWithoutAVerifiedScope` covers `Restore` and
        `ExistsUnscoped`.
- **Status:** open

---

### GAP-33 [medium][deferred] Every tenancy refusal at durable restore becomes `jobs.ErrDriver` and is retried like a transient outage

- **Where:** `tenancy/jobs.go:85-109` (returns `ErrUntrusted`/`ErrStale`/
  `ErrInactive`), `jobs/durable_context.go:539-540`
  (`if restoreErr != nil … return nil, ErrDriver`),
  `jobs/worker_delivery.go:110-119` (`DeferDeliveryCommand(…, ReasonDependency,
  …, DefaultRetryDelay)`)
- **What:** The kind is erased one frame above `tenancy`, and the worker then
  treats a forged token, a deleted tenant and a superseded generation exactly as
  it treats a control plane that is briefly down: defer and retry.
- **Why this severity:** A forged record is retried forever rather than parked —
  anyone who can write the queue table can create permanent retry load without
  ever getting a handler to run. A deleted tenant's backlog never drains and never
  dies. `TestAForgedDurableRecordEntersNoHandler` asserts only that `err != nil`,
  so it cannot see the difference either. This is the durable twin of GAP-16 (the
  cache seam erasing the kind), it is a `jobs` seam property rather than a
  `tenancy` defect, and the refusal itself is correct — hence deferred.
- **Why this timing:** Module-internal until a deployment runs a real queue; it
  belongs in the profile now and in `jobs` later.
- **Close criteria:**
  - [ ] Either `jobs` preserves a restorer's sentinel (a permanent-refusal signal
        distinct from `ErrDriver`), or `docs/modules/{en,ru}/tenancy.md` and the
        profile state that a refused durable record is deferred and retried, and
        what an operator does about it.
  - [ ] `TestAForgedDurableRecordEntersNoHandler` asserts the kind that reaches
        the worker, not only that something failed.
- **Status:** open

---

### GAP-34 [medium][deferred] `Borrow` hands out a live source after `Close()` has returned

- **Where:** `tenancy/database.go:243-251` (`Close` unlinks but does not wait),
  `:182-191` (`finish` under `this.closed` marks the entry evicted and deletes it
  but keeps the source), `:129-135` (the opener returns a lease regardless)
- **What:** An open in flight when `Close` runs is not cancelled and not refused.
  Reproduced: `Close()` returned while the open was still in flight, and the
  borrower was then handed `*crudtest.Recorder` — a live source from a closed
  directory. The source is closed on `Release`, so it is not leaked *if* the
  borrower releases; a borrower cancelled during shutdown does leak it, and
  `Close()` gives the caller no way to know either.
- **Why this severity:** Shutdown-path only, and the borrower-still-live case is
  documented behaviour for eviction. It becomes a leak exactly when shutdown is
  the reason the borrower goes away.
- **Why this timing:** Internal to `Directory` and only observable at shutdown.
- **Close criteria:**
  - [ ] `finish` under `this.closed` closes the source and answers `ErrUnavailable`
        rather than returning a lease, or `Close` waits for in-flight opens.
  - [ ] `Close` documents whether it is synchronous with respect to open sources.
  - [ ] A test closes the directory during an in-flight open and asserts the
        declared behaviour.
- **Status:** open

---

### GAP-35 [medium][deferred] Exported surface with no test and no caller

- **Where:** `tenancy/database.go:17-21` (`SourcesFunc`), `tenancy/cache.go:55`
  (`CacheScope`), `tenancy/authority.go:70` (`Must`), `:78` (`Authority.Admits`),
  `tenancy/lifecycle.go:78` (`AdmitAll`), `tenancy/storage.go:30`
  (`Authority.Store`)
- **What:** `grep -rn` across `tenancy`, `test/`, `_examples/` finds no call to
  `SourcesFunc`, `CacheScope` or `Authority.Admits` anywhere, including tests;
  `Must` and `Authority.Store` appear only in prose. `AdmitAll` has one internal
  caller (`New`'s default) and no test.
  `go doc ./tenancy` reports 78 exported functions and 25 exported types against
  `architecture.md`'s threshold of 7 public symbols per module — a breach the plan
  does not justify in writing.
- **Why this severity:** `Authority.Store` is the seam a deployment actually uses
  to write objects and it has never been executed; the rest are speculative
  extension points with no second implementation in sight
  (`microkernel.md`). None is wrong today, which is why this is deferred.
- **Why this timing:** Removing an exported symbol after the first tag is a
  breaking change, so the cheap moment to delete the speculative ones is now, but
  nothing depends on it.
- **Close criteria:**
  - [ ] `Authority.Store` has a test that writes and reads through a fake
        `storage.Backend` under an admitted class and is refused under one that is
        not.
  - [ ] `SourcesFunc`, `CacheScope` and `Authority.Admits` are exercised by a test
        or an example, or deleted.
  - [ ] The plan justifies the exported-surface count in writing, per
        `architecture.md`.
- **Status:** open

---

### GAP-36 [medium][deferred] `Authority` has grown to eleven public methods across four seams, and the two seams added this round disagree on shape

- **Where:** `tenancy/authority.go` (`Verify`, `Lookup`, `Scope`, `Admits`),
  `tenancy/context.go` (`Bind`, `With`), `tenancy/grant.go` (`Accept`, `Each`),
  `tenancy/storage.go:22,30` (`Namespace`, `Store` — added by the GAP-10 fix),
  against `tenancy/cache.go:24` (`Keyed`, a free function taking the authority)
- **What:** `go doc` lists ten methods on `*Authority` plus two constructors,
  against `architecture.md`'s threshold of 7 public methods per class. The GAP-10
  fix put the object seam on the authority as methods and the cache seam beside it
  as a free function, so the same idea has two shapes and two argument orders:
  `authority.Namespace(ctx, prefix, class)` versus
  `Keyed(ctx, authority, class, key)`.
  `Authority`'s one-sentence responsibility now needs three "and"s: it mints and
  validates scopes, **and** bounds cross-tenant grants, **and** maps a scope onto
  an object namespace or store.
- **Why this severity:** Nothing is wrong at runtime; it is a cohesion and DX
  finding on a public type. The asymmetry is what a consumer trips on — the class
  argument moves position between two adjacent lines of the same wiring block.
- **Why this timing:** Deferred because both shapes work and neither is a
  correctness problem; recorded because both are public and the next seam will
  copy one of them.
- **Close criteria:**
  - [ ] The object seam and the cache seam use one shape and one argument order,
        chosen and stated once.
  - [ ] Either the grant methods or the object methods move off `Authority`, or
        the plan justifies the method count in writing.
- **Status:** open

---

### GAP-37 [low][immediate] Documentation and code that no longer match after the fixes

- **Where:**
  `docs/ai/flows/FL-033-…md:171` — names
  `TestWorkEnqueuedBeforeASuspensionDoesNotRunAfterIt` as living in
  `tenancy/seams_test.go`; it is at `tenancy/durable_test.go:141`.
  `FL-033:155` — the file table lists `Namespace`, `Store` for
  `tenancy/storage.go` and omits `NamespaceOf`, the one function in that file
  that takes no authority.
  `tenancy/grant.go:168-170` — the comment "Every member and the deadline are
  inside the MAC. Authenticating the purpose and the first reference alone
  would…" sits above `permits`, which does neither; it describes `grantBinding`
  twelve lines below, which has no comment.
  `docs/modules/en/tenancy.md:265-268` — the "Work across tenants" example accepts
  a `ClassRead` grant and then calls `Each(..., ClassWrite, ...)`, which refuses;
  the prose explains it afterwards, but the snippet a reader copies does not work.
  `docs/roadmaps/2026-09-01-multitenancy-roadmap.md`, database-per-tenant
  conformance row — requires the pool bound "measured as concurrently open sources
  rather than cached entries"; `TestNoMoreSourcesAreOpenAtOnceThanTheBudgetAllows`
  measures concurrent *in-flight factory calls*
  (`database_test.go:38-52` increments `live` on entry and decrements before
  returning), which is the right metric for the defect that was fixed but is not
  what the row asks for and would not see a source opened and never closed.
- **What:** Five statements that name something the code does not do.
- **Why this severity:** Individually cosmetic; `CLAUDE.md` treats a doc that
  names a symbol or a file which does not exist as a failed doc, and a flow's file
  table is the reverse index the next agent greps.
- **Why this timing:** All five are one-line edits in the same change as the
  fixes they describe.
- **Close criteria:**
  - [ ] `FL-033` names the right file and lists `NamespaceOf`.
  - [ ] The comment above `permits` moves to `grantBinding` or is deleted.
  - [ ] The grant example either compiles into a run that works, or says in the
        snippet that the second line refuses.
  - [ ] The roadmap row and the test agree on what is measured.
- **Status:** open

---

### GAP-38 [low][deferred] Dead code and assertions that cannot fail, left behind by the fixes

- **Where:** `tenancy/row.go:170-172` — `through.Frozen()`'s `if this.local == ""`
  branch is unreachable since `Through` panics at construction when `local` is
  empty (`:136-138`), which was the GAP-11 fix.
  `tenancy/outcome.go:41` — the `ErrMalformed: OutcomeUntrusted` entry in
  `outcomeOf` is unreachable: `OutcomeFor` only indexes that map for members of
  `refusals` (`errors.go:34-37`), which excludes `ErrMalformed`, and handles it in
  the special case at `outcome.go:56`.
  `tenancy/database_test.go:116-118` —
  `TestOneTenantSelectsOneDatabaseAndKeepsIt` reports "the factory was asked %d
  times for one tenant" from `sources.count()`, a map keyed on the reference, so
  it can only ever print 0 or 1; the real assertion in that test is
  `first.Source() != second.Source()` on the line above, which does catch a second
  open.
  `tenancy/grant.go:127,218` — the class set is carried as an `Admission` with
  `Active` as a filler state, so "which classes does this grant permit" is spelled
  `grant.classes.Admits(class, Active)`; it is correct and the binding covers it,
  but the type says lifecycle admission and means something else.
- **What:** Four small residues of the fix round.
- **Why this severity:** No behaviour is wrong. Each is the kind of thing that
  reads as intentional to the next person and is then preserved.
- **Why this timing:** Deferred; module-internal with no contract effect.
- **Close criteria:**
  - [ ] The unreachable branch and the unreachable map entry are deleted.
  - [ ] The factory-call message names what it measures, or a real call counter
        replaces it.
  - [ ] The grant's class set is its own type, or a comment says why `Admission`
        is reused with a filler state.
- **Status:** open

---

## Round 2 — the load-bearing assertions, checked one by one

Asked of the four or five assertions the round rests on: would this still pass if
the behaviour were removed?

| Assertion | Would it still pass? |
|---|---|
| `TestAPreloadOfADeclaredRelationCarriesTheTenant` — `strings.Contains(clauseOf(statements[1]), "tenant_id")` | **Partly.** It fails if the narrowing disappears (its control subtest proves the undeclared case really does read the child table raw, so the positive case is not vacuous). It passes if the narrowing uses the wrong *value* — another tenant's, a constant, a stale one — because the bound argument is never inspected. GAP-29. |
| `TestOneLeaseReleasedFromManyGoroutinesCountsOnce` — `sources.count() > len(cohort)` and `pool.Cached() > 8` | **Yes, always.** Both are arithmetically impossible: `opened` is a map over 8 distinct references, and `reserve` caps entries at `max = 8`. No `*Lease` crosses a goroutine, so `-race` would not have seen the pre-fix `released bool` either. It passes on the defect it is cited as proving. GAP-23. |
| `TestNoMoreSourcesAreOpenAtOnceThanTheBudgetAllows` — `sources.peakOpen() > budget` | **No — it is genuinely load-bearing** for the defect it was written for: removing the reservation lets 64 goroutines enter the factory at once and `peak` exceeds 4. But `live` is decremented before `Source` returns, so it measures concurrent factory calls, not sources that are open and unclosed; a source opened and never closed is invisible to it. GAP-37. |
| `TestAForgedDurableRecordEntersNoHandler` — `err == nil` fails for three forged tokens | **No.** Remove the MAC check and the hand-built plaintext claim for `globex` (Active in that resolver) restores successfully, and the subtest fails. It also carries an explicit control that the honest record restores. Its one weakness is that it asserts only "something failed" — `jobs` collapses the kind to `ErrDriver` — so it cannot distinguish a refusal from an outage. GAP-33. |
| `TestNoVerbRunsWithoutAVerifiedScope` — 15 verbs, `ErrNoScope`, zero statements | **No.** Strong and load-bearing: it fails the moment any verb reaches the recorder without a scope. Its gap is coverage, not strength — `Restore` and `ExistsUnscoped` are missing from the list. GAP-32. |
| `TestATenantSeesEveryRowItOwns` — `where(recorder) == "\"tenant_id\" = $1"` | **No.** An exact-match assertion on the whole `WHERE` clause plus a row count, so both "narrows to nothing" and "narrows to more than ownership" fail it. The strongest assertion in the file. |
| the revalidating path | **There is no assertion.** GAP-18. |

---

## Round 2 — dispositions (orchestrator)

The re-audit was right about the thing that mattered most: **the Round-1
dispositions table cited evidence that did not exist.** It claimed GAP-2 was
"covered by the revalidation tests" when `grep -rn Revalidate --include='*_test.go' tenancy`
returned zero, and it cited a `Lease.Release` race test whose assertions were
arithmetically unfalsifiable and which never shared a `*Lease` across goroutines.
Both are now real. That is a lesson about this table, not only about those two
rows: a disposition is a claim, and a claim needs the same evidence as the code.

| Finding | Fix | Mutation proof |
|---|---|---|
| **GAP-18 high** — `Spec.Revalidate` had no test, and the dispositions cited tests that do not exist | `TestRevalidationStopsTheNextVerbAfterTheControlPlaneMoves` drives eight verbs against a moved generation and a deleted tenant, with a resolver call counter proving the control plane was re-asked; `TestWithoutRevalidationTheCarriedScopeHoldsUntilTheNextBoundary` is the control that the two settings differ, and that the *next* `Bind` still refuses | four: revalidation skipped, the generation not compared, a lookup failure returning the carried scope, and the default revalidating too — all caught |
| **GAP-19/20 high** — one cancelled request failed every borrower waiting on its open, and a waiter had no deadline of its own | the open runs on `context.WithoutCancel` plus its own `OpenTimeout` (default 30s); a waiter selects on its own `ctx.Done()` | `TestABorrowerThatGivesUpDoesNotFailTheOnesBesideIt` and `TestAWaiterHonoursItsOwnDeadlineRatherThanTheOpeners`. The first *initially passed* the mutation because the fake source ignored cancellation — the fake was hiding the defect, and it now honours `ctx` |
| **GAP-21 high** — `NamespaceOf` was a public, admission-free path to a tenant's namespace, one `From(ctx)` away | unexported. `Authority.Namespace`/`Store` and `Keyed` are the only public doors, and all three run `permittedByGrant` | `TestTheObjectSeamRefusesAScopeAnotherAuthorityMinted`, with its control |
| **GAP-22 high** — a cohort run from inside a bound request entered nobody and returned `nil` | `Each` refuses `ErrPinned` when the context is already bound | `TestACohortRunFromInsideABoundRequestIsRefusedRatherThanEmpty`, with the control that the same grant runs unbound |
| **GAP-23 high** — the `Lease.Release` race test could not fail | replaced by `TestOneLeaseReleasedFromManyGoroutinesCountsOnce`: one `*Lease`, sixteen goroutines, a second live borrower, and the falsifiable assertion that the source is **not** closed until that borrower gives it back | removing the `sync.Once` fails it |
| **GAP-24 medium** — the admission floor refused a legitimate durable-during-migration policy, and its stated reason was false for the durable class | the floor applies to `ClassWrite` only; the durable producer asks for its class directly and reads nothing first, and the comment now says so | — |
| **GAP-25 medium** — the plan's contract blocks drifted from the code | the plan's S1–S5 blocks are marked as the contracts *as accepted*, with the round-2 section above them recording what changed and why | — |
