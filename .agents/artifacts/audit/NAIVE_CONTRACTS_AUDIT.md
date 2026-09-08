# GAPS — naive contracts and naive implementations

Audit of six modules — `tenancy`, `storage`, `jobs`, `cache`, `auth`, `access` — for contracts and
implementations that do not model a condition a real consumer will hit.

| | |
|---|---|
| Date | 2026-09-06 |
| Scope | `tenancy/`, `storage/`, `jobs/`, `cache/`, `auth/` (excl. `auth/access`), `auth/access/` |
| Method | 15 reviewers (2–3 per module, each with its own file slice and lens), then one adversarial verifier per module |
| Raw findings | 90 |
| Verified | **74** — 2 critical, 12 high, 44 medium, 16 low |
| Refuted | 10 (recorded in [Refuted](#refuted-do-not-re-litigate) so they are not re-litigated) |

## Status — 2026-09-06, all 74 fixed

Every verified finding below has been fixed in this tree, each with a test that fails when the fix
is reverted. Two things outside the 74 were fixed on the way, because they were red and in the way:
`internal/cachegen` detected the jsonv2 experiment by reading `GOEXPERIMENT`, which a toolchain that
enables it by default does not set; and the `otel` storage decorator had to follow `storage.Store`
through its new `ReadOptions` / `DeleteOptions`.

Gates, on this tree:

| Gate | Result |
|---|---|
| `make unit` | green, every module |
| `make integration` | green twice in a row, PostgreSQL + MySQL + MariaDB |
| `make vet`, `gofmt -l` | clean |
| `make check` | all nine structural checks ok |
| `make examples` | green |
| `make api` | `docs/api/surface.md` regenerated — read its diff |

Two entries in that surface diff are removals rather than additions: `jobs.ScheduleOverlap` and
`jobs.AllowOverlap`/`SkipOverlap` are gone, because `DefineSchedule` refused `SkipOverlap` every time
and `ScheduleDescription.NoOverlap` could therefore never be true. Naming a capability that does not
exist is the defect the finding was about, so the names went with it. Nothing is tagged yet, so this
is not a break of a published surface — but it is the one change here a consumer of the pre-tag tree
would have to notice.

One thing this tree does **not** fix, and it is not one of the 74: under a Go toolchain with
`goexperiment.jsonv2` on — which is the default from Go 1.27 — `cache.JSON` and `jobs.JSON` refuse at
activation, by [[D-085]], because that runtime's allocation behaviour has not been proven against the
bounded-work model. That makes safe JSON unavailable by default rather than by configuration. It is a
product decision, not a bug in the code, and it is the owner's to make.

## How to read this

Every finding is anchored to a file and a line that was opened, and every one was re-checked by a
verifier who went looking for the refutation: the guard clause elsewhere on the path, the test that
pins it, the `docs/ai/decisions/` entry that deliberately sanctions it. A decision doc is binding in
this repository, so ten candidate findings died on one — the survivors are the ones where no decision
covers the failure, or where the decision's stated rationale does not reach it.

- **CONFIRMED** — the verifier read the code and it holds. Several were proved by running a probe or
  by deleting the guard and watching the suite stay green; those probes were removed and the tree is
  clean.
- **PLAUSIBLE** — probably right, not fully settled. What could not be settled is stated in the finding.

Findings that are **`vacuous-test`** or **`false-promise`** are not documentation nits. A false promise
is a guarantee a consumer designs against and does not get; a vacuous test is why the defect under it
was able to ship.

## Cross-cutting themes

The 74 findings are not 74 unrelated defects. Seven shapes account for nearly all of them, and each
recurs in modules that share no code — which makes them house patterns rather than local slips.

**1. The guarantee holds on the path the library itself walks, and stops one step outside it.**
The single most common shape here. `auth.Guard.Authenticate` — the only entry point any document shows —
cannot see the duplicated credential its own decision refuses, while the unshown `AuthenticateValues`
can (`G-AUT-02`). `access` applies its identifier `Normalize` rule to three of the four paths that touch
the column, and the fourth is `SetPassword` (`G-ACC-01`). `authnet` strips the host out of a `ServeMux`
pattern, so the boot gate's route identity is coarser than net/http's own (`G-AUT-01`). `tenancy.Unbound`
is a public, capability-free way to clear a pin the use case states unconditionally (`G-TEN-04`).

**2. The second implementation of a shared interface is where the contract turns into a lie.**
`jobsmemory` and `jobsredis` both read "a lease stamped with my own incarnation" as "work I lost", so a
worker revokes its own in-flight attempts every reclaim tick (`G-JOB-01`, reproduced through the public
API). `jobsredis` honours neither retention knob and leaks every once-intent forever (`G-JOB-08`).
`storagefs` and `storageminio` disagree about what `Info.ModifiedAt` means (`G-STO-08`) and about whether
a `Range` request works (`G-STO-09`). The conformance suite that exists to catch exactly this is itself
the weakest artefact in the audit (theme 5).

**3. The interruption window is unmodelled — the code assumes the process finishes what it starts.**
Both storage backends fence a promote with a lock whose only expiry is a ceiling, so an OOM kill mid-
`Promote` strands the stage for seven days while `Promote` and `Abort` both refuse it and `CleanupExpired`
reports success (`G-STO-01`, `G-STO-03`). The jobs heartbeat holds every active delivery's lock across one
batched driver call and charges a single transport failure to all of them (`G-JOB-02`). `accessfx` syncs
grants with a check-then-insert, so replicas starting together fail each other's `OnStart` (`G-ACC-10`).

**4. A bounded resource has an admission cap but no replacement policy.**
`tenancydb.Directory` refuses every tenant past `MaxCached` rather than evicting an idle binding, and each
incumbent borrow renews its own TTL — so under steady traffic the servable tenant set is frozen by arrival
order, permanently (`G-TEN-02`). `cache` does the same twice over: `MaxFlights` caps loader groups across
the *whole cache* and the shipped profiles default it to 1 (`G-CCH-04`), and the transient charge plan
bills worst-case bytes plus a flat 16 MiB JSON constant per operation (`G-CCH-03`). Both were reproduced.

**5. The test suite is exhaustive everywhere except the one sequence the module exists for.**
Proved by deletion in five places: the jobs lease-takeover branch — the module's central crash guarantee —
can be replaced with a no-op and `go test ./jobs/...` stays green (`G-JOB-06`); `access`'s entire refresh-
theft response can be deleted and the suite stays green (`G-ACC-08`); the cross-site assertion in two of the
three HTTP bindings passes with the `Protect` call removed (`G-ACC-03`); and `cachetest` — the harness every
future cache backend will be certified by — delegates its cancellation check to the implementer, returns
silently instead of skipping when a probe is missing, never writes a `CapacityOnlyExpiry`, and never
exercises `Check` (`G-CCH-09`…`G-CCH-12`). Separately, three decision documents (D-102, D-098, D-112) cite
tests under **Proven by** that do not prove what they claim.

**6. Declared surface that nothing enforces or can implement.**
Five of the eight permissions `access` declares and seeds are enforced nowhere, and its own administrative
use cases check none of them (`G-ACC-04`). `jobs.Capabilities.OrderedPartition` and `ServerSideWakeup` are
dead bits that let a producer require an ordering D-118 forbids promising (`G-JOB-12`); `SkipOverlap` is a
public constant `DefineSchedule` always refuses (`G-JOB-13`). `cache.TagInvalidation` is a built-in
capability no out-of-package backend can implement (`G-CCH-14`). `storage`'s `Info.ETag`/`Version` and
`ErrPreconditionFailed` are exported and inert because no operation takes a precondition (`G-STO-05`).

**7. An operational condition renders as a bug.**
`tenancy`'s `ErrCapacity` and `ErrUnavailable` wrap no `crud` sentinel, so a full directory or a control-
plane blip reaches the client as HTTP 500, indistinguishable from a panic (`G-TEN-05`) — and `Classify`
folds `context.Canceled` into `ErrUnavailable`, so a client disconnect reads as an outage (`G-TEN-07`).

### Where these modules are deliberately and soundly minimal

Worth recording, because it is what the ten refutations bought. `jobs` says out loud the things a naive job
library gets wrong — at-least-once, unordered, producer-side dedup, one database — and its refusals are
deliberate. `cache`'s bounded-work machinery, activation graph, memo, batch all-or-nothing rule and
capability discovery each hold up under reading, and D-084 already answers the classic single-flight
context question. `tenancy`'s scope value is genuinely unforgeable and its admission whitelist is checked at
construction. `storage`'s key grammar, `os.OpenRoot` containment, exact-size readers, redacted `String()`
methods and ABA-safe claim fencing are all sound. `auth`'s marker chain and cardinality refusal are real and
pinned by non-vacuous tests. The naivety in all six modules is at the edges, not in the centre.

## Severity roll-up

| Module | Verified | 🔴 | 🟠 | 🟡 | ⚪ | Refuted |
|---|---:|---:|---:|---:|---:|---:|
| [tenancy](#tenancy) | 11 | 1 | 1 | 6 | 3 | 0 |
| [storage](#storage) | 11 | 0 | 1 | 7 | 3 | 2 |
| [jobs](#jobs) | 13 | 1 | 1 | 9 | 2 | 5 |
| [cache](#cache) | 15 | 0 | 5 | 7 | 3 | 2 |
| [auth](#auth) | 7 | 0 | 2 | 4 | 1 | 1 |
| [access (auth/access)](#access) | 17 | 0 | 2 | 11 | 4 | 0 |
| **Total** | **74** | **2** | **12** | **44** | **16** | **10** |

## Start here

The findings that are not a matter of taste, in the order they should be looked at.

- **`G-JOB-01`** 🔴 critical — Recover returns the caller's own live leases, so the worker revokes its own in-flight attempts every reclaim tick (jobsmemory and jobsredis)  
  [jobs/jobsmemory/backend.go:628](jobs/jobsmemory/backend.go#L628) · `Backend.Recover / repository.recoveryIDs + (*Driver).Recover`
- **`G-TEN-01`** 🔴 critical — The durable identity token binds only (origin, queue, definition, tenant, generation), so an honest token copied onto an attacker-written queue row executes attacker-chosen work as that tenant  
  [tenancy/tenancyjobs/jobs.go:104](tenancy/tenancyjobs/jobs.go#L104) · `record / Sealer.Seal`
- **`G-ACC-01`** 🟠 high — SetPassword writes the directory's raw identifier into credentials.identifier, so Normalize is applied to the lookup but not to this write  
  [auth/access/usecase.set-password.go:34](auth/access/usecase.set-password.go#L34) · `SetPasswordUseCase.Execute`
- **`G-ACC-02`** 🟠 high — Refresh-credential reuse is detectable only one rotation back; an older stolen credential is a plain 401 that closes nothing  
  [auth/access/accessjwt/accessjwt.go:290](auth/access/accessjwt/accessjwt.go#L290) · `core.find / core.Refresh`
- **`G-AUT-01`** 🟠 high — authnet.Surface strips the host from a ServeMux pattern, so a host-scoped route inherits another host's access declaration  
  [auth/http/authnet/surface.go:58](auth/http/authnet/surface.go#L58) · `routeOf`
- **`G-AUT-02`** 🟠 high — Guard.Authenticate, the only documented entry point, cannot see a duplicated credential, so D-099's refusal does not hold on the documented path  
  [auth/guard.go:104](auth/guard.go#L104) · `(*Guard).Authenticate`
- **`G-CCH-01`** 🟠 high — A schema/codec mismatch is treated as corruption: under RefuseCorrupt every Resolve fails forever, the loader never runs and nothing evicts the entry  
  [cache/lookup.go:170](cache/lookup.go#L170) · `cacheCore.corruptRead / cacheCore.resolveAddress`
- **`G-CCH-02`** 🟠 high — One WriteFailure knob governs both the store-after-load and an explicit Put, so Warm/Durable destroy a loaded value on a cache write error and Hot's Put reports success without storing  
  [cache/resolve.go:646](cache/resolve.go#L646) · `cacheCore.storeLoaded / cacheCore.commitMutationAs`
- **`G-CCH-03`** 🟠 high — The transient charge plan bills worst-case MaxValueBytes plus a flat 16 MiB JSON constant per operation, so a profile-default cache admits about two concurrent reads and refuses the rest  
  [cache/transient.go:107](cache/transient.go#L107) · `transientPlanFor / cacheCore.lookupStable`
- **`G-CCH-04`** 🟠 high — MaxFlights caps loader groups across the whole cache, not per address, and Hot/Warm default it to 1, so a miss on an unrelated key is refused with ErrSaturated  
  [cache/resolve.go:249](cache/resolve.go#L249) · `cacheCore.resolveAddress`
- **`G-CCH-05`** 🟠 high — Every cachememory Put walks the entire entry list while holding the single mutex all reads need  
  [cache/cachememory/backend.go:269](cache/cachememory/backend.go#L269) · `(*Backend).Put / purgeExpiredLocked`
- **`G-JOB-02`** 🟠 high — The heartbeat locks every active delivery across one batched driver call and charges a single failure to all of them, so one slow fenced effect costs the whole pool its in-flight work  
  [jobs/workers_run.go:1263](jobs/workers_run.go#L1263) · `workerPool.renewActiveBatch`
- **`G-STO-01`** 🟠 high — An interrupted Promote strands the stage for seven days: Promote and Abort both refuse and CleanupExpired reports success  
  [storage/storageminio/claim.go:54](storage/storageminio/claim.go#L54) · `Backend.acquireClaim / Backend.Promote / Backend.CleanupExpired`
- **`G-TEN-02`** 🟠 high — tenancydb.Directory refuses every tenant beyond MaxCached instead of evicting an idle binding, and each incumbent borrow renews its own TTL  
  [tenancy/tenancydb/database.go:179](tenancy/tenancydb/database.go#L179) · `Directory.reserve`

---

## tenancy

<a id="tenancy"></a>

### Verdict

Where this module is genuinely naive is at its edges, not in its core: the scope value itself is unforgeable, the mint is exercised properly, the grant binding covers every field, and the admission whitelist is checked at construction — all of that survived adversarial probing intact. What did not survive is everything that leaves the process or outlives the request. The durable token is the sharpest case: it authenticates a (deployment, queue, definition, tenant, generation) tuple and nothing per-record, so an honest token lifted off one queue row and reattached to a row the attacker wrote restores that tenant with a full durable-class context — I reproduced it in-tree — which voids D-117's own claim that queue write access is not tenant impersonation, and the same key admits no rotation path at all. The second cluster is the seam between tenancy's vocabulary and everyone else's: `Seal` takes a bare scope so a grant's classes cannot reach it, `Unbound` publicly clears the pin UC-028 states unconditionally, and the two operational sentinels wrap nothing so a control-plane blip or a full directory renders as a silent 500. The `tenancydb` directory is a real implementation defect independent of contracts — a bounded map with no replacement policy is an admission cap, and under steady traffic the incumbents freeze the servable tenant set permanently. Against that, several things that look naive are deliberate and soundly so and I dropped nothing into that bucket only because the reviewers did not raise them: durable work's exemption from the read floor, the refusal to cache resolutions, the per-process salt, and the collapse of resolver text are all decided in D-117 and hold. Nothing here is padding: every one of the eleven findings is anchored to lines I opened, and eight of them were reproduced by a probe or a mutation that I then removed, leaving the tree green and `git diff` empty.

### Findings

| ID | Sev | Verdict | Kind | Where | What |
|---|---|---|---|---|---|
| `G-TEN-01` | 🔴 critical | CONFIRMED | false-promise | [jobs.go:104](tenancy/tenancyjobs/jobs.go#L104) | The durable identity token binds only (origin, queue, definition, tenant, generation), so an honest token copied onto an attacker-written queue row executes attacker-chosen work as that tenant |
| `G-TEN-02` | 🟠 high | CONFIRMED | naive-implementation | [database.go:179](tenancy/tenancydb/database.go#L179) | tenancydb.Directory refuses every tenant beyond MaxCached instead of evicting an idle binding, and each incumbent borrow renews its own TTL |
| `G-TEN-03` | 🟡 medium | CONFIRMED | naive-contract | [seal.go:48](tenancy/seal.go#L48) | Sealer.Seal takes a bare Scope and no context, so the classes a cohort grant permits cannot constrain the durable records made under it |
| `G-TEN-04` | 🟡 medium | CONFIRMED | false-promise | [context.go:84](tenancy/context.go#L84) | tenancy.Unbound is a public, capability-free way to clear the tenant pin, so a bound unit of work can be re-targeted at another tenant |
| `G-TEN-05` | 🟡 medium | CONFIRMED | naive-contract | [errors.go:29](tenancy/errors.go#L29) | ErrCapacity and ErrUnavailable wrap no crud sentinel, so a full directory or a control-plane outage renders as HTTP 500 — indistinguishable from a bug |
| `G-TEN-06` | 🟡 medium | CONFIRMED | missing-capability | [authority.go:22](tenancy/authority.go#L22) | Spec.DurableKey admits exactly one key and the token carries no key id, so rotating it strands every enqueued tenant record and the obvious two-authority workaround does not work |
| `G-TEN-07` | 🟡 medium | CONFIRMED | naive-implementation | [errors.go:48](tenancy/errors.go#L48) | Classify folds context.Canceled and DeadlineExceeded into ErrUnavailable and drops the cause, so a client disconnect reads as a control-plane outage |
| `G-TEN-08` | 🟡 medium | CONFIRMED | vacuous-test | [cache.go:56](tenancy/tenancycache/cache.go#L56) | tenancycache.Partitioned — the only function that puts the tenant partition into a cache — has no caller and no test, and replacing it with a global scope leaves the suite green |
| `G-TEN-09` | ⚪ low | CONFIRMED | false-promise | [tenancy.md:123](docs/modules/en/tenancy.md#L123) | The module reference says a state admitted for durable work but not for reading is refused at construction; it is deliberately accepted |
| `G-TEN-10` | ⚪ low | CONFIRMED | missing-capability | [row.go:340](tenancy/tenancyrow/row.go#L340) | tenancyrow.Policy hardcodes ClassRead/ClassWrite while every other seam takes a Class, so an erasure job cannot ask for the one class a deleted tenant may be admitted for |
| `G-TEN-11` | ⚪ low | PLAUSIBLE | naive-contract | [grant.go:146](tenancy/grant.go#L146) | Authority.Each returns a nil error when every cohort member failed, so a cron wrapper that checks only err records a total failure as a completed run |

#### `G-TEN-01` — The durable identity token binds only (origin, queue, definition, tenant, generation), so an honest token copied onto an attacker-written queue row executes attacker-chosen work as that tenant

**🔴 critical** · **CONFIRMED** · `false-promise` · [tenancy/tenancyjobs/jobs.go:104](tenancy/tenancyjobs/jobs.go#L104) · `record / Sealer.Seal`

**Evidence**

`record` is the entire per-record binding: `return [][]byte{digest[:], []byte(definition.Value())}` (tenancy/tenancyjobs/jobs.go:104-107), and `Sealer.mac` keys HMAC over origin, those two fields, the epoch and the reference only (tenancy/seal.go:94-107). Nothing that distinguishes one invocation from another can be inside it: `ContextCaptureRequest` exposes exactly `Namespace()`, `Definition()`, `Partition()` (jobs/durable_context.go:378-380) and `capturePlacementContext` runs before `preparePlacement` mints the invocation id and encodes the payload (jobs/queue.go:698 vs 604-676). `IdentityRestoreRequest` likewise carries no payload and no invocation id (jobs/durable_context.go:435-449). Every other integrity field on a row is an unkeyed sha256 the writer can recompute — `digestDurableContext` (jobs/durable_context.go:822-851) covers namespace, partition, definition, scope, tenant, actor, token, provenance, epoch and trace, and nothing else; `grep -rln hmac jobs/` returns nothing, so the tenancy seal is the only keyed authenticator on a record. Claims contradicted: tenancy/seal.go:34-37 "The binding fields tie the token to the record that carries it"; docs/modules/en/tenancy.md:265-266 "a record written by anything that does not hold the durable key reaches nothing at all"; D-117:78-80 "Queue write access is therefore not tenant impersonation".

**Why it is naive**

It models the adversary as one who must forge a MAC, when the adversary the design names — anything with write access to the shared queue table — can read an existing row and copy its token. The token authenticates a (deployment, queue, definition, tenant, generation) tuple, not the record it travels in, so it is a bearer credential with unlimited replay against a mutable payload.

**Failure scenario**

A deployment wires `tenancyjobs.ContextProvider`/`IdentityRestorer` with a 32-byte DurableKey exactly as documented, on a queue whose database is shared with other services (the configuration jobs.go:10-14 names). Tenant `acme` enqueues one `send-invoice` job, so one row now carries an honest sealed token. Anything with SELECT+INSERT on that table copies the token, the partition and the durable context onto a new row with its own payload and a fresh invocation id. `Unseal` verifies, the control plane reports acme active, the generation matches, `RestoredIdentity.validFor` re-digests the partition and matches, and the handler runs attacker-chosen work inside a context bound to acme for the full durable class.

**Suggested shape of the fix**

Bind the token to the record: extend `jobs.ContextCaptureRequest` with the candidate invocation id and the payload/wire digest (capture after `preparePlacement` has them) and add them to `record(...)`. Until that seam exists, stop claiming per-record binding — say in seal.go:34-37, docs/modules/en/tenancy.md and D-117 that the token authenticates a tenant for a queue and a definition, and that queue write access remains replay of that tenant's work with a chosen payload.

**How it was verified**

Proved in-tree, not argued: a probe (since deleted) enqueued an honest job for acme through the real queue, took the token off the placement, and fed it to the package's own `hostileRequest` helper — the attacker-assembly path built from exported API only — and `jobs.RestoreTrustedIdentity` answered `REPLAY ACCEPTED: an attacker-assembled record restored tenant "acme" (bound=true)`. `TestAForgedDurableRecordEntersNoHandler` (durable_test.go:428-442) stops one step short: all four of its cases feed tokens nobody with the key wrote. D-117 is the binding decision and its stated rationale (lines 76-80) covers only the lifecycle re-check at execution — a suspended/deleted/rotated tenant — so it does not sanction an active tenant whose valid token is reattached to a different record. I rated this critical rather than the reviewer's own critical-by-assertion because the prerequisite (queue-table write access) is exactly the adversary the DurableKey exists to defeat, so the control buys much less than every doc on the subject says it does.

#### `G-TEN-02` — tenancydb.Directory refuses every tenant beyond MaxCached instead of evicting an idle binding, and each incumbent borrow renews its own TTL

**🟠 high** · **CONFIRMED** · `naive-implementation` · [tenancy/tenancydb/database.go:179](tenancy/tenancydb/database.go#L179) · `Directory.reserve`

**Evidence**

`reserve` is the only admission path: an existing entry is reused and its expiry pushed out — `held.borrowers++; held.expires = this.now().Add(this.ttl)` (database.go:174-177) — and otherwise `if len(this.entries) >= this.max { return nil, false, expired, tenancy.ErrCapacity }` (database.go:179-180). The only reclamation is `sweep`, which needs the entry to be both expired and idle (`now.After(held.expires) && held.borrowers == 0`, database.go:281) and is called from nowhere but `reserve` (database.go:173). There is no LRU, no wait and no way for a caller to make room: `Evict` takes a specific reference (database.go:265) and `Cached()` returns a count (database.go:308-312), so the directory cannot be asked which tenants it holds. The docs sell the bound purely as a connection budget — "MaxCached: 64, // mandatory" (docs/modules/en/tenancy.md:304) and "an unbounded per-tenant pool is how one tenant takes a deployment down" (tenancy.md:325-327) — and never say it is also the maximum number of distinct tenants servable in a TTL window.

**Why it is naive**

It assumes the number of distinct tenants active within one TTL never exceeds MaxCached, which is the assumption nobody wrote down. A bounded cache that refuses new entries rather than evicting an idle one is an admission cap, and under steady traffic the incumbents keep renewing their own expiry, so the servable set is frozen by arrival order.

**Failure scenario**

A deployment with 300 tenants uses the documented `MaxCached: 64, TTL: 10 * time.Minute`. The first 64 tenants to arrive each take a slot; every later request from any of them refreshes `expires`, so `sweep` never reclaims. The remaining 236 tenants get `ErrCapacity` permanently rather than transiently — and, per the finding below, as HTTP 500. The operator's only escapes are raising MaxCached until it equals the tenant count (defeating the bound the field exists for) or calling `Evict` on tenants they have no way to enumerate. One caller that forgets `lease.Release()` retires a slot for the process lifetime, since `borrowers` never returns to zero.

**Suggested shape of the fix**

When the map is full, close the least-recently-used idle entry (`borrowers == 0`) to make room and answer `ErrCapacity` only when every entry is in use; failing that, expose the held references so a caller can run its own policy, and document MaxCached as "the most tenants this process can serve per TTL".

**How it was verified**

Reproduced with two probes (since deleted) against the package's own fixtures: with MaxCached=2, TTL=10m and five tenants, two hours of simulated traffic from the two incumbents left the other three refused `ErrCapacity` on every attempt while both slots sat idle (`cached=2`); and with MaxCached=1 a single unreleased lease still refused the second tenant after 48 simulated hours against a one-minute TTL. No decision covers the replacement policy — D-116 places `tenancydb` in its own package for topology reasons, D-117 governs re-checking the scope on borrow, and FL-033 step 11 records only "a full directory answers ErrCapacity". `TestABindingIsGivenBackAfterItsBorrowerLifetime` (database_test.go:284-309) pins the refusal as intended behaviour for the released-but-unexpired case, which is the narrow case; it does not reach the steady-traffic one.

#### `G-TEN-03` — Sealer.Seal takes a bare Scope and no context, so the classes a cohort grant permits cannot constrain the durable records made under it

**🟡 medium** · **CONFIRMED** · `naive-contract` · [tenancy/seal.go:48](tenancy/seal.go#L48) · `Sealer.Seal`

**Evidence**

`func (this Sealer) Seal(scope Scope, binding ...[]byte) ([]byte, error)` (seal.go:48) validates through `this.authority.accept(scope, ClassDurable)` (seal.go:52); `accept` (authority.go:136-145) checks only the mint binding and the deployment-wide Admission. The grant's class restriction is context state — `withGrant` (grant.go:218) read only by `permittedByGrant` (grant.go:226-238), which is called from exactly one place, `Authority.Scope` (context.go:53-55) — so a function with no `ctx` parameter structurally cannot consult it. The shipped job seam is safe only by its own ordering: `tenancyjobs.Capture` calls `this.authority.Scope(ctx, tenancy.ClassDurable)` first (tenancyjobs/jobs.go:36). The module reference invites consumers to build their own seam on the raw primitive — "A seam of your own is written against these five" with the sample `token, err := sealer.Seal(scope, queue, definition)` (docs/modules/en/tenancy.md:284-289) — two paragraphs below "Both go through the authority rather than taking a scope value, because holding a scope says an authority minted it once — not that this authority admits this class of work now" (tenancy.md:246-249).

**Why it is naive**

It treats the durable-class question as a property of the scope value alone. A grant is per-unit-of-work state that only a context can carry, so any security-relevant factory whose signature omits `context.Context` silently opts out of every per-work-unit control the package adds — today the grant, tomorrow whatever else the context comes to carry.

**Failure scenario**

Operations issues a read-only cohort grant for a monthly report over 10 000 tenants and runs it with `Each(ctx, grant, tenancy.ClassRead, work)`. Inside the run, `work` reaches the deployment's own outbox seam, written exactly as the module doc shows — `scope, _ := tenancy.From(ctx); token, _ := sealer.Seal(scope, outbox, name)` — and enqueues a durable record per member. `Authority.Scope(ctx, ClassDurable)` would have refused each with ErrGrantRequired; `Seal` produces valid records instead and the worker later executes them as full durable-class work for all 10 000 tenants.

**Suggested shape of the fix**

Give the sealing entry point a context — `Sealer.Seal(ctx, scope, binding...)` routing through `Authority.Scope(ctx, ClassDurable)` (or an internal `permittedByGrant` call) instead of `accept` — matching D-117's own rule that a factory which performs work does not take a bare `Scope`.

**How it was verified**

Reproduced in-tree (probe since deleted): inside `Each(ctx, grant, ClassRead, ...)` over a read-only grant, `Authority.Scope(ctx, ClassDurable)` answered `work across tenants needs an explicit grant: forbidden` for both members while `sealer.Seal(From(ctx))` returned a token that `Unseal` read back for both. D-117 is the binding decision and it does not sanction this: its "What it forbids" says in as many words "Do not let a factory that performs work take a bare `Scope`", and its exception is written for `Namespace`/`Keyed`, which "derive a name from a scope rather than performing work". Severity lowered from the reviewer's high because exploitation needs a conjunction of two uncommon things — a deployment that uses cohort grants and one that has written its own durable seam on the raw primitive rather than using `tenancyjobs`.

#### `G-TEN-04` — tenancy.Unbound is a public, capability-free way to clear the tenant pin, so a bound unit of work can be re-targeted at another tenant

**🟡 medium** · **CONFIRMED** · `false-promise` · [tenancy/context.go:84](tenancy/context.go#L84) · `Unbound`

**Evidence**

`Unbound` sets the scope key to `Scope{}` (context.go:84-89) and `From` then reports absent (context.go:91-97). The pin in `With` is conditional on `From` seeing something: `if current, ok := From(ctx); ok && (current.reference != accepted.reference || current.epoch != accepted.epoch) { return nil, ErrPinned }` (context.go:28-30). One slot therefore carries two different facts — "this context names no tenant" and "this unit of work has already chosen its tenant" — and clearing the first clears the second. The function needs neither the authority nor a scope, i.e. no capability at all. It appears in no doc: FL-033's file table calls `tenancy/context.go` "`Bind`, `With`, `Scope`, `From` — explicit propagation and the pin" and D-117's "Where it lives" lists the same four symbols; `grep -rn Unbound docs/` finds only `docs/api/surface.md`.

**Why it is naive**

The pin models a convention rather than a property of the unit of work: it holds only while nobody removes the marker it is derived from, and the removal is a public one-argument call that any helper can make.

**Failure scenario**

UC-028 §11 states unconditionally: "A unit of work's tenant is fixed when it starts. Binding a different tenant inside work that is already bound refuses rather than switching." A handler binds tenant A and opens `orders.Tx`. A shared helper — copied from the only recipe in the tree that uses this function, the worker path at tenancyjobs/jobs.go:78 — does `ctx = tenancy.Unbound(ctx)` to "reset" tenancy before a sub-call, and a later `authority.Bind` re-resolves to tenant B. The transaction commits statements narrowed to two different tenants with no error, and nothing in any doc or review checklist flags the call.

**Suggested shape of the fix**

Keep the escape hatch but make it a capability and make it one-way: either move it onto the authority (`authority.Unbound(ctx)`), or store the pin under a second key that `Unbound` does not clear, so a context that was ever bound to A still refuses a later bind for B with ErrPinned. Then document it in docs/modules/en/tenancy.md, FL-033's file table and D-117's inventory.

**How it was verified**

Reproduced in-tree (probe since deleted) with a control: `authority.With(bound, globex)` refused with ErrPinned, and `authority.With(tenancy.Unbound(bound), globex)` succeeded with `From` then reporting globex. Confirmed the reviewer's coverage claim: `grep -rn Unbound tenancy/*_test.go tenancy/*/*_test.go` matches nothing but the tenancyjobs worker path's indirect use. No decision covers it — D-117 names the pin as one of its invariants and does not list `Unbound` among the context.go symbols, so nothing sanctions the hole.

#### `G-TEN-05` — ErrCapacity and ErrUnavailable wrap no crud sentinel, so a full directory or a control-plane outage renders as HTTP 500 — indistinguishable from a bug

**🟡 medium** · **CONFIRMED** · `naive-contract` · [tenancy/errors.go:29](tenancy/errors.go#L29) · `ErrCapacity / ErrUnavailable`

**Evidence**

`ErrCapacity = errors.New(...)` and `ErrUnavailable = errors.New(...)` (errors.go:29-31) wrap nothing, while the other nine refusals wrap `crud.ErrForbidden`, `crud.ErrConflict` or `crud.ErrBadRequest`. Both are what the operational paths answer: `tenancy.ErrCapacity` when the directory is full (tenancydb/database.go:180), `tenancy.ErrUnavailable` on a panicking factory (database.go:198), and `Classify` collapses every resolver or factory error naming no sentinel to `ErrUnavailable` (errors.go:48-57). `port.sentinelKind` recognises only the crud sentinels and falls to `default: return errs.KindInternal` (port/kind.go:86-88); `StatusFor` maps `KindInternal` to 500 (port/porthttp/errors.go:27-49). `errs.KindRetryable` and `errs.CodeUnavailable` already exist (errs/codes.go:59) but only a `*errs.Fault` can carry them and `crud` exports no availability sentinel.

**Why it is naive**

The error vocabulary can express "forbidden", "conflict" and "malformed" by wrapping a crud sentinel, and cannot express "try again" at all — so the two refusals that are purely operational are the two that project worst. The documented rationale stops at the security team and never asks what the transport does with the result.

**Failure scenario**

A database-per-tenant deployment mounts CRUD routes through `crud/http/crudnet` and `port/porthttp`. The control plane blips, so the resolver fails and `Authority.Verify` answers `Classify(err)` = `ErrUnavailable`. Every in-flight request across every tenant renders 500 with the deliberately empty internal body (D-015), so clients with correct 503/Retry-After backoff do not back off, and on-call sees a mass of internal errors indistinguishable from a code bug. The same happens for `ErrCapacity` under the starvation above, and the only in-application escape is `errors.Is(err, tenancy.ErrCapacity)` plus a hand-built fault in every handler.

**Suggested shape of the fix**

Give the retryable class a wrappable sentinel — a `crud.ErrUnavailable` recognised by `port.sentinelKind` as `errs.KindRetryable`, or make `ErrCapacity`/`ErrUnavailable` `*errs.Fault` values built with `errs.Retryable().Code(errs.CodeUnavailable)` — and correct docs/modules/en/tenancy.md:374-376 to say what they render as today.

**How it was verified**

Measured, not inferred: a probe through `port.KindOf` and `porthttp.StatusFor` (since deleted) printed `ErrCapacity kind=internal status=500`, `ErrUnavailable kind=internal status=500`, against `ErrNoScope 403`, `ErrPinned 409`, `ErrMalformed 400`. I re-anchored the reviewer's decision citation: D-040's invariant is scoped to SQL engine answers (class 40, 55P03, 1205, SQLITE_BUSY), so this is not literally a D-040 violation — the binding decision is D-015, whose invariant is "every failure a caller is expected to branch on must be reachable with `errors.Is` against an exported sentinel in package `crud`", and UC-028 §14 requires a capacity refusal and an availability refusal to be different answers from an authorisation one. The only sanction anywhere is docs/modules/en/tenancy.md:374-376, whose stated reason covers not wrapping `crud.ErrForbidden` and never mentions the 500 it leaves behind. The same shape was already recorded as a blocker elsewhere in this repo for `ErrUnboundedDelete` (docs/ai/usecases/modules/specs/Specs.md:758), and `ErrMalformed` here has since been fixed the way this asks for.

#### `G-TEN-06` — Spec.DurableKey admits exactly one key and the token carries no key id, so rotating it strands every enqueued tenant record and the obvious two-authority workaround does not work

**🟡 medium** · **CONFIRMED** · `missing-capability` · [tenancy/authority.go:22](tenancy/authority.go#L22) · `Spec.DurableKey`

**Evidence**

`Spec` carries a single `DurableKey []byte` (authority.go:22), copied once into `Authority.durableKey` (authority.go:68). `Sealer.mac` keys HMAC with that one value (seal.go:95) and `Unseal` accepts only a match against it (seal.go:83-86). The token layout is 8 bytes of generation, 32 bytes of MAC, then the reference (seal.go:56-61) — no key id, no version. `tenancyjobs.IdentityRestorer(authority)` takes one authority (tenancyjobs/jobs.go:58-64), so there is no seam-level place to try a second key, and no `PreviousKeys`, `KeyID` or verify-with-any field exists in the package.

**Why it is naive**

It models a shared secret as a constant of the deployment. A durable record is by definition read by a process started later, so the key's lifetime must exceed the queue's drain time and any rolling deploy — precisely the case where a single-valued key cannot be changed atomically.

**Failure scenario**

An operator rotates `TENANCY_DURABLE_KEY` after someone with secret-store access leaves. During the rolling restart, producers on the new key write records that old-key workers refuse, and every record already in the queue is refused by new-key workers: `Unseal` → ErrUntrusted → `RestoreIdentity` returns before the control plane is asked (tenancyjobs/jobs.go:84-87), so every tenant-partitioned job in flight fails permanently and enters no handler.

**Suggested shape of the fix**

Accept `DurableKeys [][]byte` (or `DurableKey` plus `AcceptedDurableKeys`): seal with the first, verify against all, and put a one-byte key index in the token so verification is a lookup rather than a trial. Document the procedure in docs/usage-guides/tenancy.md beside the key requirement at line 202.

**How it was verified**

I went looking for the workaround the reviewer assumed a deployment could invent, and it does not work: a probe (since deleted) built two authorities differing only in DurableKey, restored a scope through the old-key one, and asked the primary authority for it — `tenancy: scope was not produced by this authority: forbidden`, because the mint salt is drawn per `New` (authority.go:52-55) and `accept` compares against it (authority.go:130). So a chained `jobs.TrustedIdentityRestorerFunc` over two authorities produces contexts every downstream seam rejects; the only real workaround is to hand-write the restorer against `Unseal` on the old-key sealer plus `Lookup`/`With` on the primary, reimplementing the unexported `record()` binding format as you go. D-117 mandates the key and discusses rotation only for the scope salt ("a configuration item nobody would rotate"), which is an argument for the salt's design and not a sanction for this; docs/usage-guides/tenancy.md:202 requires "the same value in the producer and the worker" and never mentions changing it.

#### `G-TEN-07` — Classify folds context.Canceled and DeadlineExceeded into ErrUnavailable and drops the cause, so a client disconnect reads as a control-plane outage

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [tenancy/errors.go:48](tenancy/errors.go#L48) · `Classify`

**Evidence**

`Classify` returns one of the eleven sentinels or, for everything else, the bare `ErrUnavailable` (errors.go:48-58) — the value returned is the sentinel itself, so the cause is dropped rather than wrapped. Every seam that calls application code routes through it: `Authority.Verify` (authority.go:89), `Authority.Lookup` (authority.go:100), `Authority.current` under `Spec.Revalidate` (context.go:63), and the directory's source factory and fence (tenancydb/database.go:205, 215). `OutcomeFor` then labels the result "unavailable" (outcome.go:40) and the transport renders 500 (see the finding above).

**Why it is naive**

It assumes every error out of a resolver is a control-plane failure whose text is dangerous. Cancellation and deadline expiry are neither: they name no tenant, database or credential, they are the caller's own signal coming back, and callers, retry loops and metrics all have to tell them apart from a backend being down.

**Failure scenario**

A deployment sets `Spec.Revalidate: true` as the module doc recommends for stopping a deleted tenant mid-request (docs/modules/en/tenancy.md:144-152), so every verb goes through `Authority.current` → `resolver.Lookup`. A client hangs up mid-request; the resolver's HTTP or SQL call returns `context.Canceled`; each subsequent verb answers ErrUnavailable. The error-rate SLO burns on a client disconnect, `OutcomeFor` labels the metric "unavailable", and any circuit breaker the application wrapped around `Bind` treats a hang-up as a control-plane outage. The caller cannot recover the truth because the cause is dropped rather than wrapped.

**Suggested shape of the fix**

In `Classify`, pass `context.Canceled` and `context.DeadlineExceeded` through unchanged — they name nothing that must not travel — and keep the collapse for everything else.

**How it was verified**

Probed in-tree (since deleted): `Classify(context.Canceled)`, `Classify(context.DeadlineExceeded)` and `Classify(fmt.Errorf("dial control plane: %w", context.Canceled))` all answered "tenancy: the tenant capability is unavailable" with `errors.Is(out, cause) == false` in every case. D-117 forbids wrapping a resolver's error into a refusal and justifies it entirely by text leakage — "which is where the DSN, the tenant and the credential are" — so its stated rationale does not reach `context.Canceled`, and passing the two context sentinels through leaks nothing. Note `Authority.Each` already treats cancellation as its own answer (`ctx.Err()` returned raw, grant.go:141-143), so the package is inconsistent with itself here.

#### `G-TEN-08` — tenancycache.Partitioned — the only function that puts the tenant partition into a cache — has no caller and no test, and replacing it with a global scope leaves the suite green

**🟡 medium** · **CONFIRMED** · `vacuous-test` · [tenancy/tenancycache/cache.go:56](tenancy/tenancycache/cache.go#L56) · `Partitioned`

**Evidence**

`func Partitioned[K any](namespace cache.Namespace) cache.Scope[Key[K]] { return cache.Partitioned(namespace, Partition[K]()) }` (cache.go:56-58) is the one place a tenant partitioner is attached to a `cache.Scope`, and `partitionOf` short-circuits to a zero digest for a global scope (cache/key.go:107-111). `grep -rn 'Partitioned' --include=*.go .` finds it only at its own definition and in the archived audit snapshot; nothing in the repository imports `tenancycache` at all. The package's own tests reach `Partition[string]()` directly (cache_test.go:46, 74, 83, 89) and never build a `cache.Scope` or a `cache.Cache`. The sibling gap was already closed for storage and the fix says why: "Store is the function a composition root actually calls, and until now nothing exercised it: every test reached namespaceOf or Namespace directly, so a Store that dropped the namespace and put every tenant in one place would have shipped green" (tenancystorage/store_test.go:66-69).

**Why it is naive**

The tests verify the ingredient (a digest prefix) and not the dish (a cache whose addresses differ per tenant). Every assertion in cache_test.go is about the bytes `Partition` returns, so nothing pins that those bytes are ever wired into a cache.

**Failure scenario**

Any refactor that replaces `cache.Partitioned` with `cache.Global` in cache.go:57, or drops the `Partition[K]()` argument, ships green. Two tenants then `Resolve` the same logical key, `addressOf` computes an identical `Address` because the partition digest is zero for both, and one tenant is served the other's cached value.

**Suggested shape of the fix**

Add the cache twin of store_test.go: build a real `cache.Cache[Key[string], string]` over `cachememory` with `Partitioned[string](namespace)` and a codec that encodes only `key.Unwrap()`, `Put` under tenant A and `Lookup` under tenant B, with a control that A reads its own value back — plus a second case pinning that a restored generation misses.

**How it was verified**

I ran the mutation rather than describing it: rewriting the body to `cache.Global[Key[K]](namespace)` compiles, and `go test -count=1 ./tenancy/...` and `./cache/...` are entirely green — the whole tenant partition can be deleted from the cache seam without a single failure. Source restored afterwards; `git diff` is empty. FL-033:193 nevertheless cites `TestARestoredGenerationReadsNoneOfThePreviousOnesValues` as the test that walks the cache half of the flow. Severity set to medium rather than the reviewer's high: today's code is correct, the exposure is a future regression shipping green, which is exactly the liability CLAUDE.md and D-020 name.

#### `G-TEN-09` — The module reference says a state admitted for durable work but not for reading is refused at construction; it is deliberately accepted

**⚪ low** · **CONFIRMED** · `false-promise` · [docs/modules/en/tenancy.md:123](docs/modules/en/tenancy.md#L123) · `Admission.inconsistent`

**Evidence**

docs/modules/en/tenancy.md:122-125: "Admission is also checked for consistency at construction: a state admitted for writing or for durable work but not for reading is refused, naming the state." The check is `Admission.inconsistent` (lifecycle.go:134-141) and it iterates one class only: `if !this.Admits(ClassRead, state) && this.Admits(ClassWrite, state) { return state }`. Durable is excluded on purpose and the comment two lines above says so (lifecycle.go:131-133: "Durable work is deliberately not held to that floor").

**Why it is naive**

The code is right and the doc is wrong: the reference promises a construction-time refusal the implementation deliberately does not perform.

**Failure scenario**

An operator writes an admission where a state is admitted for durable work but not for reads, relying on the documented check to catch the mistake. `tenancy.New` returns nil, `tenancy.Must` does not panic, and the mistake surfaces later as durable handlers that enter and then fail on their first read. Symmetrically, a reader of this page believes a durable-only admission cannot be expressed and works around a policy the library supports.

**Suggested shape of the fix**

Delete "or for durable work" from docs/modules/en/tenancy.md:123 and add the sentence D-117 and lifecycle.go:131-133 already carry: durable work is not held to the read floor, because its producer asks for ClassDurable directly and reads nothing first. Check docs/modules/ru/tenancy.md for the same sentence.

**How it was verified**

Probed (since deleted): `New` with `Admit(ClassDurable, Active, Migrating)` merged with read and write for Active only returned `<nil>`. D-117:37-43 sanctions the code and contradicts the doc in as many words, which is what makes the doc the defect rather than the code. `grep` shows the claim appears only on this line in the English reference; docs/usage-guides/tenancy.md does not repeat it.

#### `G-TEN-10` — tenancyrow.Policy hardcodes ClassRead/ClassWrite while every other seam takes a Class, so an erasure job cannot ask for the one class a deleted tenant may be admitted for

**⚪ low** · **CONFIRMED** · `missing-capability` · [tenancy/tenancyrow/row.go:340](tenancy/tenancyrow/row.go#L340) · `Policy / classOf`

**Evidence**

`Policy` fixes the classes it asks for: `Scope` uses `authority.Scope(ctx, tenancy.ClassRead)` (row.go:301), `relationScopes` the same (row.go:331), and `Inspect` uses `classOf(action)` — `if action == crud.ActionRead { return tenancy.ClassRead }; return tenancy.ClassWrite` (row.go:340-345). The four other seams take the class as a parameter: `tenancystorage.Namespace(ctx, authority, prefix, class)`, `tenancycache.Keyed(ctx, authority, class, key)` (tenancycache/cache.go:25), `Directory.Borrow(ctx, class)` (tenancydb/database.go:126), and `tenancyjobs` asks for `ClassDurable` (tenancyjobs/jobs.go:36). Since `Admission.inconsistent()` forces any state admitted for writes to be admitted for reads (lifecycle.go:134-141) while durable is exempt, `Admit(ClassDurable, Deleted)` is the only admission that opens work for a dead tenant without opening its request path — and it reaches storage, cache and the directory but never the row seam.

**Why it is naive**

The lifecycle vocabulary models `Deleting` and `Deleted` as states work happens in, but the row seam's class vocabulary has no name for the work that happens in them, so admitting the eraser and admitting the world are the same act there.

**Failure scenario**

A deployment must erase a tenant on deletion. It admits `Admit(ClassDurable, Active, Deleted)` and runs an erasure job: `tenancystorage.Store(..., tenancy.ClassDurable, backend)` deletes the objects, `tenancycache.Keyed(..., tenancy.ClassDurable, k)` forgets the values, `directory.Borrow(ctx, tenancy.ClassDurable)` reaches the database — and `invoices.DeleteAll(ctx)` answers `tenancy.ErrInactive`, because the gate's `Inspect` asks for ClassWrite. The deployment falls back to `Admit(ClassWrite, Deleted)`, which `inconsistent()` forces to carry `Admit(ClassRead, Deleted)` too, so every ordinary request path now serves the deleted tenant.

**Suggested shape of the fix**

Give `tenancyrow.Policy` an optional class mapping (a `Classes` field defaulting to read/write) so an erasure repository can declare `ClassDurable`, or add a fourth maintenance class the row seam accepts and no request path asks for.

**How it was verified**

Code facts verified line by line, and I checked the escape the reviewer said did not exist: a grant does not help (`Each` re-mints each member through `Lookup(..., class)`, so ClassWrite for `Deleted` is still required, grant.go:150), but the reviewer's conclusion that the rows must then be deleted outside the library is wrong — `Ownership[M]` is a public interface (row.go:42-47) and `security.Policy` is public, so a consumer can hand-write a twelve-line erasure policy that asks `authority.Scope(ctx, tenancy.ClassDurable)` and reuses `ownership.Narrow`/`Apply`. That workaround is what drops this from the reviewer's medium to low. No decision covers it: D-117's "What it forbids" addresses the start of a tenant's life (provisioning), not the end.

#### `G-TEN-11` — Authority.Each returns a nil error when every cohort member failed, so a cron wrapper that checks only err records a total failure as a completed run

**⚪ low** · **PLAUSIBLE** · `naive-contract` · [tenancy/grant.go:146](tenancy/grant.go#L146) · `Authority.Each`

**Evidence**

`member` turns every per-tenant failure into a `Member` value and never propagates it — a failed `Lookup` (grant.go:150-153), a refused `With` (grant.go:154-157) and an error from `work` itself (grant.go:158-160) — and the loop appends and continues, ending `return outcomes, nil` (grant.go:146). The error return is reserved for whole-run refusals (nil grant, foreign grant, expired, wrong class, pinned — grant.go:119-133) and the mid-run staleness and cancellation checks (grant.go:138-143). There is no count, no aggregate error and no `MemberError`. UC-028 §12 and docs/modules/en/tenancy.md:349-353 describe only the skip-a-non-active-member behaviour and are silent on the aggregate.

**Why it is naive**

It models a cohort run as a list of independent units with no run-level verdict. Real cohort work fails in correlated ways — the downstream service is down, the control plane is down, a deploy broke the handler — so "every member failed" is a common case and it is reported only through a value the caller has to walk.

**Failure scenario**

`outcomes, err := authority.Each(ctx, grant, tenancy.ClassWrite, billing.Roll)` runs monthly under cron. The billing service is down for the whole window: all 10 000 members answer OutcomeError, `Each` returns nil, the cron job exits 0 and the run is recorded as complete. The identical shape is reachable when the control plane is unreachable — every `Lookup` classifies to ErrUnavailable and every member becomes OutcomeUnavailable, still with `err == nil`.

**Suggested shape of the fix**

Return a run-level error when no member reached OutcomeOk, or expose a failed count / `Members(outcomes)` helper so the aggregate cannot be missed — the same defence D-117 already applies to the pinned case, where entering nobody and returning no error is called out by name.

**How it was verified**

Reproduced in-tree (since deleted): a two-member grant whose `work` always failed produced `err=<nil>` with both outcomes `error` and both `Err` values set. Marked PLAUSIBLE rather than CONFIRMED because I could not settle that this is a defect rather than the intended shape: `[]Member` is the first return value and carries every failure with its `Outcome` and `Err`, no document promises a run verdict, and D-117's cited sentence is about entering nobody at all rather than about entering everybody and failing. What keeps it on the list is that D-117 names the exact consequence — "a billing run that bills nothing and says it worked" — and closes only one of its two entrances. `TestOneCohortMemberFailingLeavesTheRestResumable` (grant_test.go:169-176) pins the nil return for the skip case, so the contract is deliberate for skips and simply unconsidered for total failure.

---

## storage

<a id="storage"></a>

### Verdict

The storage module is unusually careful where it has decided to be careful, and its naivety is concentrated in the staging lifecycle and in what happens after a process or a request stops early. The write path, key grammar, private file format, stream ownership and error projection are genuinely well built: the fixed 16 KiB header, `os.OpenRoot` containment, the exact-size readers, the redacted `String()` on every sensitive type and the ABA-safe token/ETag claim fencing on MinIO are all deliberate and sound, and two of the reviewers' complaints (Chain's nil result, missing provider write options) are settled conventions or declined scope rather than defects. Where it is genuinely naive is in modelling interruption: both backends fence a promote with a lock whose only expiry is a ceiling — the stage's own TTL on the filesystem, a hard-coded seven days on MinIO — so an OOM kill or a request deadline mid-promote leaves a StageID that `Promote` and `Abort` both refuse, that `CleanupExpired` skips while reporting `{Removed:0, More:false}`, and that no public call can release. The second cluster is contract shape: validation applied at the wrong end of a two-request lifecycle (`Stage` accepts what the default `Promote` can never place), the write vocabulary having no precondition at all while `Info.ETag`/`Version` and `ErrPreconditionFailed` sit exported and inert, and the read path enforcing the library's own write-side metadata budget on bytes it did not write. The third is quieter drift between the two backends behind one contract — `ModifiedAt` means visibility time on MinIO and private-write time on the filesystem, ranges work through one link and not the other — none of it decided in `docs/ai/decisions/`, which contains no storage entry whatsoever, so nothing here is protected by a binding decision.

### Findings

| ID | Sev | Verdict | Kind | Where | What |
|---|---|---|---|---|---|
| `G-STO-01` | 🟠 high | CONFIRMED | naive-implementation | [claim.go:54](storage/storageminio/claim.go#L54) | An interrupted Promote strands the stage for seven days: Promote and Abort both refuse and CleanupExpired reports success |
| `G-STO-02` | 🟡 medium | CONFIRMED | naive-contract | [backend.go:268](storage/storageminio/backend.go#L268) | Stage accepts a payload larger than MaxCreateOnlySize that the default Promote can never place |
| `G-STO-03` | 🟡 medium | CONFIRMED | naive-implementation | [storagefs.go:705](storage/storagefs/storagefs.go#L705) | A crash between claimStage and consumeClaim wedges a StageID until its TTL, and Abort answers ErrConflict against a doc that calls it idempotent |
| `G-STO-04` | 🟡 medium | CONFIRMED | naive-implementation | [metadata.go:115](storage/storageminio/metadata.go#L115) | An object whose stored metadata exceeds the library's own write-side budget cannot be read at all |
| `G-STO-05` | 🟡 medium | CONFIRMED | naive-contract | [types.go:101](storage/types.go#L101) | No conditional / compare-and-swap write anywhere in the Store contract, and the ETag/Version that would carry one are inert |
| `G-STO-06` | 🟡 medium | CONFIRMED | naive-implementation | [storagefs.go:205](storage/storagefs/storagefs.go#L205) | Delete removes only the object file, so the per-segment directory chain every key creates is never reclaimed and no public call can prune it |
| `G-STO-07` | 🟡 medium | CONFIRMED | false-promise | [backend.go:517](storage/storageminio/backend.go#L517) | TemporaryURL bounds the link TTL only by seven days and reports an expiry that can outlive the signing credentials |
| `G-STO-08` | 🟡 medium | CONFIRMED | naive-implementation | [format.go:107](storage/storagefs/format.go#L107) | storagefs stamps Info.ModifiedAt at private-write time, not at visibility, so a promoted object reports its stage time and concurrent replaces can invert |
| `G-STO-09` | ⚪ low | CONFIRMED | missing-capability | [store.go:12](storage/store.go#L12) | Open has no offset or length, and the shipped filesystem link handler answers every Range request with the whole object |
| `G-STO-10` | ⚪ low | CONFIRMED | naive-contract | [backend.go:250](storage/storageminio/backend.go#L250) | Delete writes a delete marker on a versioned bucket, and Info.Version is a handle no operation accepts |
| `G-STO-11` | ⚪ low | CONFIRMED | false-promise | [storageminio.md:137](docs/modules/en/storageminio.md#L137) | The module doc sends a deployment to run "the adapter's integration scenarios", which exist nowhere in the repository |

#### `G-STO-01` — An interrupted Promote strands the stage for seven days: Promote and Abort both refuse and CleanupExpired reports success

**🟠 high** · **CONFIRMED** · `naive-implementation` · [storage/storageminio/claim.go:54](storage/storageminio/claim.go#L54) · `Backend.acquireClaim / Backend.Promote / Backend.CleanupExpired`

**Evidence**

The claim lease is always the global ceiling: `expiresAt := this.now().Add(storage.MaxStageTTL)` (claim.go:54) — seven days (storage/types.go:20) — with no relation to the stage's own `ExpiresIn` and no `Config` knob (backend.go:39-45). `Promote` deliberately keeps that lease when the final write is uncertain: `if uncertain(err) { release = false }` (backend.go:335-337), and `uncertain` includes `KindCancelled` (errors.go:107-113), which is what `put` returns for any context deadline (backend.go:601-604). While the lease is live, `acquireClaim` refuses everyone: `if current.state == claimStateActive && this.now().Before(current.expiresAt) { return ... KindConflict }` (claim.go:81-83), so `Promote` retries and `Abort` (backend.go:361-365) both return `ErrConflict`. The sweeper cannot reclaim either: `claim, err := this.acquireClaim(ctx, "cleanup", ...); if errors.Is(err, storage.ErrConflict) ... { continue }` (backend.go:437-440) with no record of the skip, and `cleanupExpiredClaims` leaves an unexpired active claim alone while the stage exists (claim.go:235-240). `CleanupResult` has only `Removed` and `More` (storage/types.go:127-131) and `More` is set only where the removal budget is exhausted, so the run reports `{Removed:0, More:false}` — indistinguishable from "nothing to do". `More` is defined in no document (grep of docs/modules/en/storage*.md finds no mention).

**Why it is naive**

The lease models an operation that ends by returning. An operation cut off mid-PUT by a request deadline — the ordinary outcome for a several-hundred-megabyte promote behind a busy server — leaves the fence in place, and the fence's length was chosen as the global safety ceiling rather than from anything about the operation or the stage. The sweep then treats contention as if it were nothing to come back for, so the one signal an operator has says the job succeeded.

**Failure scenario**

An HTTP handler with a 30 s deadline calls `Promote` for a 300 MB stage. The final PUT exceeds the deadline; the adapter returns `ErrCancelled` and leaves the claim active until now+7d. The user retries: `ErrConflict`. Support calls `Abort`: `ErrConflict`. The nightly `CleanupExpired` returns `{Removed:0, More:false}` — success — every night for a week while the 300 MB stays billed and that upload can never be confirmed.

**Suggested shape of the fix**

Derive the lease from the operation's own bound (caller deadline plus margin, or the stage's remaining TTL) capped by `MaxStageTTL`, and expose it on `Config`; let `CleanupExpired` break an active claim whose lease has passed even when the stage still exists; and set `More` (or add a `Deferred` counter) when an entry was inspected, found expired and left in place, so "come back" is reported for contention as well as for the budget.

**How it was verified**

Read every cited line. The reviewer's framing as a false promise against docs/modules/en/storageminio.md is wrong and I corrected it: lines 98-101 do document that "An uncertain final result ... retains the active generation until bounded expiry", so the fencing itself is deliberate and disclosed. What is not disclosed or sanctioned anywhere is that the lease is hard-coded to the seven-day maximum regardless of the stage TTL, that no configuration can shorten it, and that `CleanupExpired` reports success while it skips. The doc's own mitigation at 103-106 ("Give `Promote`, `Abort` and cleanup calls shorter context deadlines") makes the deadline-cancelled path — the entry point to this state — more frequent, and 93-95 forbids the manual fix ("Do not manually delete active/retired claims"). Merged the separate `More` finding here because the user-visible failure is one story; I did not verify the reviewer's in-package probe, but the control flow is unambiguous and I traced it line by line.

*Merged from reviewer findings: storage-minio-backend-cleanup-reports-done-while-work-remains*

#### `G-STO-02` — Stage accepts a payload larger than MaxCreateOnlySize that the default Promote can never place

**🟡 medium** · **CONFIRMED** · `naive-contract` · [storage/storageminio/backend.go:268](storage/storageminio/backend.go#L268) · `Backend.Stage / Backend.Promote`

**Evidence**

`Stage` always writes with `storage.Replace` — `this.put(ctx, "stage", object, source, callerSource, storage.Replace, options.Size, ...)` (backend.go:268) — and the ceiling exists only inside the `CreateOnly` branch of `put`: `if *size > MaxCreateOnlySize { return ..., storage.NewError(operation, storage.KindUnsupported, ...) }` (backend.go:576-578). `copySize` applies no upper bound (storage/validate.go:142-150), so `StageOptions.Size` above 5 GiB is accepted. `Promote` places with `options.Mode` (backend.go:334) and `normalizeMode` turns the zero value into `CreateOnly` (storage/validate.go:110-113); docs/modules/en/storage.md:78 says "Promotion also defaults to create-only." `storagefs` has no such ceiling — its `Promote` places by link/rename (storage/storagefs/storagefs.go:305) — so the same program passes on one backend and fails on the other. `storage.Capabilities` (storage/types.go:173-178) carries no size limit and `MaxCreateOnlySize` is a `storageminio` constant invisible through `storage.Store`.

**Why it is naive**

The size ceiling is modelled as a property of one call rather than of the staged-upload lifecycle. It assumes the moment a payload's size is known is the moment it is placed; with staging those are two requests, possibly hours apart, and only the second one checks.

**Failure scenario**

A video app stages a 6 GiB upload. `Stage` succeeds after minutes of transfer and the UI shows "uploaded". On submit, `Promote(id, key, storage.PromoteOptions{})` returns `storage.ErrUnsupported`, and does so on every retry — the size never changes. The 6 GiB is billed until the stage TTL. The only escape is `PromoteOptions{Mode: storage.Replace}`, which silently gives up the create-only collision guarantee the default promised.

**Suggested shape of the fix**

Refuse the oversized payload where it is first knowable: reject a declared `StageOptions.Size` above the placement ceiling in `Stage`, and post-check the staged object's real size before returning a `Staged`; or have `Promote` use a server-side multipart copy above the single-PUT limit so the ceiling stops applying to promotion.

**How it was verified**

Verified empirically, not just by reading. I added a throwaway in-package test with the existing `fakeClient`: `Stage` with `Size: ExactSize(MaxCreateOnlySize+1)` returned a StageID, the following `Promote(..., PromoteOptions{})` returned `storage promote: unsupported`, and `Promote(..., PromoteOptions{Mode: storage.Replace})` returned nil. Probe file removed afterwards; `git status` shows no residue from me. No decision doc covers storage sizes; the storage roadmap's non-goals (docs/roadmaps/2026-09-01-storage-roadmap.md:349-358) do not mention it.

#### `G-STO-03` — A crash between claimStage and consumeClaim wedges a StageID until its TTL, and Abort answers ErrConflict against a doc that calls it idempotent

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [storage/storagefs/storagefs.go:705](storage/storagefs/storagefs.go#L705) · `Backend.claimStage`

**Evidence**

`claimStage` elects a promoter with a hard link and turns an existing link into a refusal with no lease of its own: `err := this.root.Link(stagedName, claimedName)` … `if errors.Is(err, fs.ErrExist) { return "", storage.NewError(operation, storage.KindConflict, err) }` (storagefs.go:708, 720-722). Release happens only in-process, through the deferred `releaseClaim` (storagefs.go:272-279) or `consumeClaim` (:786-801). `Abort` goes through the same election and propagates the refusal (storagefs.go:322-328). `cleanupExpiredStages` skips anything whose header has not expired — `if header.ExpiresAt == 0 || this.now().Before(time.Unix(0, header.ExpiresAt)) { continue }` (storagefs.go:404) — and the `.claim` hard link carries the same `ExpiresAt` as the `.stage` it was linked from, so nothing is reclaimed before the stage TTL (default 24 h, up to `MaxStageTTL` = 7 d). docs/modules/en/storagefs.md:80 states flatly "`Abort` and `Delete` are idempotent." `storageminio` took the other route — an expired claim is stolen (claim.go:81) — so the same `Store.Promote` contract has a self-healing lock on one backend and a TTL-bound one on the other.

**Why it is naive**

The claim is a mutual-exclusion lock whose only expiry is the expiry of the thing it locks and whose release depends on the promoting process staying alive. That models promotion as an operation that cannot be interrupted; restarts, rolling deploys and OOM kills during a several-hundred-millisecond promote are ordinary.

**Failure scenario**

A user uploads an avatar (`Stage`, default TTL 24 h) and submits the form. The container is evicted after `claimStage` created the `.claim` link and before `consumeClaim` ran; the bytes are intact and no final object exists. The user retries: `Promote` -> `ErrConflict`. The application cleans up: `Abort` -> `ErrConflict`, contradicting the documented idempotence. `CleanupExpired` returns `{Removed:0, More:false}` and touches neither file for the rest of the stage TTL. No public call releases the claim.

**Suggested shape of the fix**

Give the claim a bounded lease of its own — the acquiring deadline written into the claim, so `claimStage` can take over one whose lease has passed — or have `cleanupExpiredStages` reap a `.claim` older than that lease even when the stage is unexpired. Note that a naive steal is unsafe: `TestPromoteNeverRestoresAStageAfterFinalPlacementBecomesVisible` (storagefs_test.go:846-848) relies on a surviving claim to stop a committed stage being placed at a second key, so the lease needs a marker distinguishing "placed" from "never placed". Until then, correct docs/modules/en/storagefs.md:80 to say `Abort` is idempotent only against a stage with no live claim.

**How it was verified**

Verified empirically. A throwaway test called `backend.claimStage` and then abandoned it (the crash shape): `Promote` = `storage promote: conflict`, `Abort` = `storage abort: conflict`, `CleanupExpired{Limit:10}` = `{Removed:0, More:false}`, with both `.stage` and `.claim` still on disk. Probe removed. I corrected the reviewer's "unrecoverable" claim: the state does self-heal once the stage TTL passes, because `stageIDFromName` accepts `.claim` names and the expired sweep removes both (storagefs.go:392, 411-414). The doc-contradiction and the absence of any public recovery inside the TTL window stand.

#### `G-STO-04` — An object whose stored metadata exceeds the library's own write-side budget cannot be read at all

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [storage/storageminio/metadata.go:115](storage/storageminio/metadata.go#L115) · `portableMetadata / infoFromObject`

**Evidence**

`portableMetadata` errors rather than filtering: `if len(out) == storage.MaxMetadataEntries { return nil, errors.New("object metadata has too many entries") }` (metadata.go:109-111), `if len(value) > storage.MaxMetadataValueBytes || !utf8.ValidString(value) || hasControl(value) { return nil, ... }` (metadata.go:115-117), a total-bytes cap (metadata.go:118-121) and a `mime.ParseMediaType` gate on the stored content type (metadata.go:55-60). `Open` turns any of these into a hard failure and throws the body away: `info, err := infoFromObject(objectInfo); if err != nil { _ = body.Close(); return nil, storage.Info{}, storage.NewError("open", storage.KindInternal, err) }` (backend.go:221-225); `Head` does the same (backend.go:238-241). The budget is the library's write-side policy (storage/validate.go:165-191), not S3's — S3 allows 2 KB of user metadata, this allows 512 bytes per value and 1536 bytes total. docs/modules/en/storageminio.md:126 promises filtering, not refusal: "Only bounded portable user metadata reaches `storage.Info`; reserved staging metadata and raw SDK headers stay inside the adapter."

**Why it is naive**

The read path applies the write path's portability budget to bytes it did not write. That is only correct if this library is the sole writer to the bucket, which the module never states and which `Prefix` (backend.go:136-147, pointing the adapter at an existing tree) directly invites you to violate.

**Failure scenario**

An operator uploads a brochure with `mc cp --attr 'x-amz-meta-description=<600 chars>' brochure.pdf s3/app-files/production/tenant/brochure.pdf`, or a predecessor service wrote metadata within S3's 2 KB budget but above this library's 1536 bytes. Every `Open` and `Head` of that key returns `storage.ErrInternal` forever. The bytes are intact and downloadable with any other tool, and the library offers no option to skip or truncate metadata, so the application can never serve that file again.

**Suggested shape of the fix**

Drop or truncate non-portable entries on read the way `vv-` entries are already dropped (metadata.go:107-108), and surface the fact — a flag on `Info`, or a distinguishable non-fatal error — instead of failing the whole read. Keep the strict budget on the write path, where the caller can still fix the input.

**How it was verified**

Verified empirically: a throwaway in-package test served a `minio.ObjectInfo` carrying one 513-byte `description` value; `Open` = `storage open: internal` and `Head` = `storage head: internal`. Probe removed. The neighbouring pinned invariant `TestOpenRejectsCaseAmbiguousMetadata` (backend_test.go:674-689) covers a genuinely ambiguous key, which is a different case and does not sanction failing on a merely over-budget one. Note `rawUserMetadata` lowercases keys before validation, so foreign key *casing* is not a trigger — the reachable triggers are value length, total bytes, entry count, non-token key characters and an unparsable stored content type.

#### `G-STO-05` — No conditional / compare-and-swap write anywhere in the Store contract, and the ETag/Version that would carry one are inert

**🟡 medium** · **CONFIRMED** · `naive-contract` · [storage/types.go:101](storage/types.go#L101) · `PutOptions / PromoteOptions / Store.Delete`

**Evidence**

`PutOptions{Mode, Size, ContentType, Metadata}` (types.go:101-106) and `PromoteOptions{Mode}` (types.go:115-117) are the whole write vocabulary, and `WriteMode` has exactly two values (types.go:93-99). `Store.Delete(context.Context, Key) error` (store.go:14) takes no precondition either. `Info` carries `ETag` and `Version` (types.go:140-141), but nothing consumes them — `normalizePutOptions` has no precondition to normalise (validate.go:67-85) — and `storagefs` never populates them: `privateHeader.info()` returns only `{Size, ContentType, Metadata, ModifiedAt}` (storage/storagefs/format.go:46-53), while `storageminio` does fill them (metadata.go:70-71). The taxonomy already reserves the vocabulary the mechanism lacks: `KindPreconditionFailed` / `ErrPreconditionFailed` (errors.go:15, :35) is produced by no operation a consumer can request — `storagefs` never returns it, and in `storageminio` the only conditional path a consumer can reach is `CreateOnly`, whose 412 is deliberately relabelled `KindAlreadyExists` (storageminio/errors.go:59-63). `Replace` on the filesystem is a bare `this.root.Rename(sourceName, destinationName)` (storagefs.go:583): atomic, last-writer-wins, no losers.

**Why it is naive**

It models an object store as keys only ever written by one writer at a time. Every real object store is a shared mutable namespace: the moment two handlers can update the same JSON document, manifest, snapshot or avatar, a read-modify-write cycle needs "write only if what I read is still there". Both shipped backends can express it — MinIO natively (the adapter already uses `options.SetMatchETag(matchETag)` for its own claim protocol, claim.go:298-301), the filesystem through the link/rename primitives it already uses for `CreateOnly`.

**Failure scenario**

A settings document lives at `tenants/acme/settings.json`. Handler A does Open -> decode -> set field X -> `Put(Replace)`; handler B does the same for field Y concurrently. Both succeed, both get a non-error `Info`, and the surviving object contains exactly one of X and Y with no error and no way for either caller to detect the loss. There is no workaround through the public API: `CreateOnly` cannot be used because the key exists, `Head`-then-`Put` is a TOCTOU gap, and the `ETag`/`Version` a consumer would use as a version token are empty strings on `storagefs`. Every consumer must build a lock or a version table outside the library.

**Suggested shape of the fix**

Add a precondition to the write options — `PutOptions.IfMatch *string` (and the same on `PromoteOptions` and a `DeleteOptions`) — refused with `ErrUnsupported` by a backend that cannot honour it and advertised through a new `Capabilities.ConditionalWrite` flag; return the already-exported `ErrPreconditionFailed` when it fails, and make `storagefs` populate `Info.ETag` (the fixed header already stores size and a nanosecond stamp) so the token exists on both backends.

**How it was verified**

Opened every cited line; all hold. I checked reachability of `ErrPreconditionFailed` more carefully than the reviewer: it is producible from `writeClaim`'s `If-Match` path (claim.go:295-301 via errors.go:59-63) and can escape through a lost-CAS release, but no verb a consumer calls can request a precondition, so the class remains unreachable by intent. Searched docs/ai/decisions/ for storage/conditional/etag/precondition — there is no storage decision at all (D-012 "put replaces never creates" is `crud`, not `storage`), and docs/ai/flows and usecases index nothing under `storage/` except the OTel and fx wiring. The roadmap's explicit non-goals (docs/roadmaps/2026-09-01-storage-roadmap.md:349-358) name `List`, recursive delete, retry and provider raw options — not conditional writes. I re-judged severity down from the reviewer's high: `Replace` means what it says, so the caller did ask for an unconditional overwrite; the defect is that the read-modify-write class is inexpressible, not that a documented guarantee is broken.

#### `G-STO-06` — Delete removes only the object file, so the per-segment directory chain every key creates is never reclaimed and no public call can prune it

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [storage/storagefs/storagefs.go:205](storage/storagefs/storagefs.go#L205) · `Backend.Delete`

**Evidence**

Every write materialises one directory per key segment: `objectPath` builds `.vv-storage-v1/objects/<ns>/<enc seg1>/…/object.vv` (storagefs.go:669-675) and `place` creates the chain with `this.root.MkdirAll(path.Dir(destinationName), this.dirMode)` (storagefs.go:572). `Delete` unlinks the leaf and nothing else (storagefs.go:209-216). No `Rmdir`/`RemoveAll` exists anywhere under `storage/` — a grep for `RemoveAll|Rmdir|removeDir` over the whole subtree returns nothing. The only maintenance verb walks `staging/<ns>` (`cleanupExpiredStages`, storagefs.go:369-434) and `work/<ns>` (`cleanupOrphanWork`, :436-488) and never looks at `objects/`. `MaxKeyBytes` is 768 with `MaxKeySegmentBytes` 128 (types.go:12-13), so one key can create hundreds of nested directories.

**Why it is naive**

It assumes a small, long-lived key space rewritten in place. The documented use is the opposite — docs/modules/en/storage.md shows `users/01J.../avatar.png` and an upload flow promoting to a per-entity key, i.e. high-cardinality keys with a per-record lifetime. Deleting the record then leaves its directory behind permanently, and the parent's entry count only ever grows.

**Failure scenario**

A tenant stores per-upload objects at `uploads/<ulid>/original.png` and deletes them after processing. After a few million upload/delete cycles the tree holds a few million empty `uploads/<enc ulid>/` directories: inodes are never returned, `readdir` and directory-hash lookups in the parent degrade, and a filesystem with a finite inode table can hit ENOSPC with almost no bytes stored. `CleanupExpired` reports `Removed: 0` because it never inspects `objects/`, and the public API offers no prune.

**Suggested shape of the fix**

After a successful `Delete` (and after a `Replace` that changes depth), unlink the now-empty ancestors up to `objects/<ns>` best-effort, tolerating ENOTEMPTY and ENOENT so a concurrent writer is never disturbed; or extend `CleanupExpired` with a bounded empty-directory sweep and say so in docs/modules/en/storagefs.md.

**How it was verified**

Verified empirically: a throwaway test put `uploads/01j8zabcd/original.png`, deleted it, then walked the tree — three key-derived directories remained under `objects/documents` with the object gone, and the namespace directory still had one child. Probe removed. I softened the reviewer's "the docs forbid the operator from pruning by hand": storagefs.md:34-36 forbids constructing paths into the private tree for object access, and an operator could run `find -type d -empty -delete`, but that races `place`'s MkdirAll-then-Link and is nowhere sanctioned. The roadmap's non-goal is recursive delete as a public verb, which is a different thing from reclaiming the adapter's own directories.

#### `G-STO-07` — TemporaryURL bounds the link TTL only by seven days and reports an expiry that can outlive the signing credentials

**🟡 medium** · **CONFIRMED** · `false-promise` · [storage/storageminio/backend.go:517](storage/storageminio/backend.go#L517) · `Backend.TemporaryURL`

**Evidence**

The only bound applied is the configured maximum: `if options.ExpiresIn < time.Second || options.ExpiresIn > this.maxLinkTTL || options.ExpiresIn%time.Second != 0` (backend.go:517), and `maxLinkTTL` defaults to `storage.MaxTemporaryURLTTL` = seven days (backend.go:98-101, storage/types.go:22). The reported expiry comes purely from the local clock and the requested TTL: `storage.NewLink(u.String(), issuedAt.Truncate(time.Second).Add(options.ExpiresIn))` (backend.go:533). Nothing consults the credential provider. The SDK does not help: `presignURL` validates only `isValidExpiry` (1 s..7 d) at minio-go/v7@v7.3.0/api-presigned.go:41-43 and never compares against the credential, and a SigV4 presign carries the session token, so the URL dies with it. docs/modules/en/storageminio.md:129-132 says "`TemporaryURL` uses MinIO's native pre-signed GET and the same TTL bounds as the common store … the reported expiry is conservative", and storageminio.md:13-14 invites the consumer to build the client with their own credentials — which includes `credentials.NewIAM("")` or an STS provider.

**Why it is naive**

It models a presigned URL's lifetime as a function of the requested TTL alone. Under SigV4 with temporary credentials the URL dies when the session token dies, whichever comes first, and the SDK signs a seven-day URL from a one-hour session without complaint.

**Failure scenario**

A service running under an EC2/EKS role (`credentials.NewIAM`) issues `TemporaryURL(ctx, key, {ExpiresIn: 24*time.Hour})` for an emailed invoice link. `Link.ExpiresAt()` says 24 h, the application stores and displays that, and the URL starts returning `403 The provided token has expired` about an hour later when the role session rotates. Nothing in the library reports the shortfall and the one signal a caller has is wrong.

**Suggested shape of the fix**

Cap `maxLinkTTL` against the credential's remaining lifetime — `(*minio.Client).GetCreds()` returns a `credentials.Value` with an `Expiration` field (minio-go/v7@v7.3.0/api.go:1265, pkg/credentials/credentials.go:50-51) — and refuse a longer request by name; or state in `Config` and the module doc that `MaxLinkTTL` must be set below the session duration when the injected client uses non-static credentials, and stop calling the reported expiry conservative.

**How it was verified**

Read the adapter lines and the vendored SDK. I corrected what "conservative" means: `issuedAt.Truncate(time.Second)` rounds the issue time down, so the reported expiry is conservative with respect to clock rounding — the doc sentence is defensible on its own terms. The defect is that credential lifetime is not considered at all and no configuration surfaces it, which the module doc does not warn about while explicitly inviting an injected IAM/STS client. The fix is feasible: `GetCreds()` is exported and `Value.Expiration` exists.

#### `G-STO-08` — storagefs stamps Info.ModifiedAt at private-write time, not at visibility, so a promoted object reports its stage time and concurrent replaces can invert

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [storage/storagefs/format.go:107](storage/storagefs/format.go#L107) · `Backend.writePrivateFile`

**Evidence**

`writePrivateFile` takes the timestamp as soon as the body copy finishes — `modifiedAt := this.now().UTC()` (format.go:107) — and only then does `file.WriteAt(block, 0)` (:129), `Truncate` (:132), `Chmod` (:135), `Sync()` when configured (:138-142) and `Close()` (:143). Visibility happens later still, in `Put` via `this.place(...)` (storagefs.go:170) whose `Replace` arm is `this.root.Rename(...)` (:583). Readers get exactly the stored value (`privateHeader.info()`, format.go:51). For staged uploads the gap is not a race at all: the header is written during `Stage`, `Promote` places that same file by link/rename (`placeClaim`, storagefs.go:601-620) and returns `header.info()` (storagefs.go:311), so the object at the final key carries the stage time. `storageminio` does the opposite — it re-PUTs on promote and takes `upload.LastModified` (backend.go:620-623). The suite pins nothing: the only assertion is `!info.ModifiedAt.IsZero()` (storagefs_test.go:37), and the concurrency test runs one writer against many readers (storagefs_test.go:325-336).

**Why it is naive**

It treats the stamp as free to take at any convenient point, assuming the interval to commit is negligible and identical for all writers. It is neither: `fsync` sits in that window when `Config.Sync` is on, and staging puts hours in it. `ModifiedAt` is the only change token this backend supplies — `ETag` and `Version` are left empty (format.go:46-53) — so consumers will use it for exactly the thing it cannot support.

**Failure scenario**

Two handlers `Put(Replace)` the same key. A finishes its copy and stamps T=100, then blocks in `Sync`. B stamps T=140, syncs quickly and renames at T=150. A's sync returns and it renames at T=200. The committed object is A's bytes carrying `ModifiedAt = 100`. A mirroring or CDN-purge job that re-fetches only when `ModifiedAt` advances has already recorded 140, sees 100, concludes nothing changed and serves B's superseded content indefinitely. The same value is emitted as `Last-Modified` by the signed-link handler (link.go:149-151).

**Suggested shape of the fix**

Stamp `ModifiedAt` immediately before the placement that makes the file visible — write the header after the copy, then rewrite only the timestamp field before `place`, or take it from the destination's post-rename `Lstat` — and decide explicitly what a promoted object reports, then say so in docs/modules/en/storagefs.md, because the two backends currently disagree.

**How it was verified**

Verified the staged half empirically: with a controlled clock, `Stage` at 12:00 and `Promote` at 15:00 produced `info.ModifiedAt` and a subsequent `Head` of 12:00 — three hours stale, deterministic, no race needed. Probe removed. The concurrent-Replace inversion I confirmed by reading only (the window is real but I did not construct a race). Nothing in docs/modules/en/storage.md or storagefs.md defines `ModifiedAt` semantics, so neither reading is sanctioned; the promote skew is arguably defensible as "when the content was written", the inversion under concurrent Replace is not.

#### `G-STO-09` — Open has no offset or length, and the shipped filesystem link handler answers every Range request with the whole object

**⚪ low** · **CONFIRMED** · `missing-capability` · [storage/store.go:12](storage/store.go#L12) · `Store.Open`

**Evidence**

`Open(context.Context, Key) (io.ReadCloser, Info, error)` (store.go:12) and `Backend.Open` (store.go:46) take no offset, no length and no options struct — the one verb with none. The filesystem body is hard-wired to the whole object: `return &objectBody{ctx: ctx, file: file, remaining: header.Size}, header.info(), nil` (storagefs.go:188), and `objectBody` exposes no `Seek` (format.go:283-317). The escape hatch is `TemporaryURL`, and the filesystem handler ignores `Range` entirely: it sets `Content-Length` from `info.Size`, writes `response.WriteHeader(http.StatusOK)` unconditionally and `io.Copy(response, body)` (link.go:146-156) — no `Accept-Ranges`, no 206, no `If-Range`. `storageminio.TemporaryURL` hands back `this.client.PresignedGetObject(...)` (backend.go:525), a URL served by S3 itself, which does honour `Range`. docs/modules/en/storage.md:90-93 introduces the method as "Temporary download links — Both adapters expose the same call".

**Why it is naive**

It models a stored object as something a consumer always wants in full, from byte zero, in one uninterrupted read, and then advertises the two backends' links as the same call when one is range-capable and the other is not.

**Failure scenario**

A consumer stores large exports and hands users a `TemporaryURL`. On MinIO a download manager issues `Range: bytes=4294967296-` and gets a 206. The same code on `storagefs` gets a 200 with the full object from byte 0, so every resume restarts the transfer. Inside the process there is no workaround either: serving a range from `Store` requires `Open` plus `io.CopyN` to discard the prefix, an O(offset) read per request, because the returned body is not a `Seeker`.

**Suggested shape of the fix**

Give `Open` an options struct (`ReadOptions{Offset int64, Length *int64}`) and report support through `Capabilities`; the filesystem backend can honour it with one `file.Seek(privateHeaderSize+offset, io.SeekStart)` because the header is a fixed 16 KiB (format.go:22). Failing that, have the link handler answer `Range` or set `Accept-Ranges: none` and say in docs/modules/en/storagefs.md that the handler is whole-object only.

**How it was verified**

Opened every cited line; the code says what the reviewer says. I re-judged severity down from medium: the fs handler is deliberately a download-only surface (it forces `Content-Disposition: attachment`, `no-store` and a sandbox CSP, link.go:143-148, described at storagefs.md:60-63), so whole-object delivery is coherent with its stated purpose; what is not coherent is docs/modules/en/storage.md:90-93 calling it "the same call" as the S3 presign. No decision, use case or roadmap item covers ranged reads either way.

#### `G-STO-10` — Delete writes a delete marker on a versioned bucket, and Info.Version is a handle no operation accepts

**⚪ low** · **CONFIRMED** · `naive-contract` · [storage/storageminio/backend.go:250](storage/storageminio/backend.go#L250) · `Backend.Delete`

**Evidence**

`err = this.client.RemoveObject(ctx, this.bucket, object, minio.RemoveObjectOptions{})` (backend.go:250) — the zero options carry no `VersionID` and no `GovernanceBypass`, so on a versioned bucket this writes a delete marker and every prior version survives. The adapter is version-aware in the read direction — `Version: object.VersionID` (metadata.go:71) and `Version: upload.VersionID` (backend.go:629) populate `storage.Info.Version` (types.go:141) — but no method on `storage.Backend` or `storage.Store` accepts a version (store.go:10-57) and `Open` passes an empty `minio.GetObjectOptions{}` (backend.go:211). docs/modules/en/storage.md:9 advertises "idempotent `Delete`", and storageminio.md:42-44 shows the module knows operators run versioned buckets: "A bucket this creates is not the bucket an operator would have provisioned: it has no versioning, retention or replication".

**Why it is naive**

`Delete` is written for an unversioned bucket and the assumption is stated nowhere, while the module hands the caller a `Version` string it accepts in no call — so the one field that would let a consumer work around the gap is inert on both backends (`storagefs` never sets it at all).

**Failure scenario**

An operator provisions the bucket with versioning on — the normal production choice, and what the docs steer them toward instead of `EnsureBucket`. A user exercises erasure: the app calls `files.Delete(ctx, key)`, gets nil, `Head` afterwards returns `ErrNotFound`, and the application reports the data deleted. Every byte is still retrievable by anyone with `s3:GetObjectVersion`, and no library call can remove it.

**Suggested shape of the fix**

Say in docs/modules/en/storageminio.md that this backend's `Delete` removes the current version only and that erasure on a versioned bucket is the operator's lifecycle policy; or give `Delete` a way to purge — accept `Info.Version`, or delete all versions — so the `Version` the adapter already returns is usable.

**How it was verified**

Opened the cited lines; all correct. I re-judged the reviewer's medium down to low: deleting the current version is the standard S3 semantic that every non-versioning-aware client implements, and an operator who enables versioning has asked for old versions to be retained, so this is a documentation and dead-field problem rather than a wrong implementation. No decision doc mentions versioning, delete markers, retention or object lock.

#### `G-STO-11` — The module doc sends a deployment to run "the adapter's integration scenarios", which exist nowhere in the repository

**⚪ low** · **CONFIRMED** · `false-promise` · [docs/modules/en/storageminio.md:137](docs/modules/en/storageminio.md#L137) · `storageminio testing guidance`

**Evidence**

storageminio.md:136-142: "The default unit/wire suite uses an injected SDK seam and performs no network access. A deployment claiming MinIO compatibility should additionally run the adapter's integration scenarios against the exact server/version it operates, especially concurrent conditional single PUT, multipart staging/replace and pre-signed GET. Include wrong-ETag and read-quorum-failure conditional PUT cases." `grep -rl storageminio --include=*.go .` returns only the twelve files of the package itself and its fx satellite — nothing under `test/`, nothing under `_examples/`, and `test/integration/` has no storage file at all. Every fencing test runs against `fakeClaimState` (backend_test.go:1815-1854), an in-memory `map[string]minio.ObjectInfo` under a mutex that implements `If-None-Match: *` and exact `If-Match` linearizably — precisely the server property the doc declares unproven at 108-114. `wire_test.go` exercises two paths through the real SDK (`TestWireCreateOnlyAboveMultipartThresholdIsOneConditionalPUT`, `TestWireOpenUsesImmediateGET`); the claim protocol, staging, promotion and presigning never reach the SDK's request path.

**Why it is naive**

The test double satisfies the same seam as the production client while granting, for free, the strongest guarantee the production server may not have. That is a defensible unit-test choice, but the doc discharges the residual risk by pointing at scenarios that were never written, so an operator who follows the instruction finds nothing and concludes there is nothing to run.

**Failure scenario**

An operator reads storageminio.md, looks for "the adapter's integration scenarios" to run against their MinIO build, finds no `make` target and no file referencing the package, and deploys on a build without minio/minio#21653. Two concurrent `Promote` calls both acquire the claim because `If-None-Match: *` is not strictly enforced, both write the final object, one silently clobbers the other, and every test in the repository is still green.

**Suggested shape of the fix**

Either add the named scenarios as a build-tagged suite under `test/` (concurrent conditional PUT, wrong-ETag CAS, multipart stage then replace, presigned GET) wired into `make integration`, or reword the paragraph to say plainly that no such scenarios ship and spell out the cases a deployment must write for itself.

**How it was verified**

Confirmed the grep and the directory listing myself, and read the fake's conditional implementation and both wire tests. I narrowed the finding: the doc is honest at 108-114 that the server-side guarantee cannot come from the SDK, so the substantive risk is disclosed; what fails is the pointer at 137 to an artifact that does not exist, which CLAUDE.md treats as a defect in its own right ("A doc that names a symbol which no longer exists has failed at the one job it has"). Severity reduced from the reviewer's high accordingly — this is a documentation defect, not a code defect.

### Refuted in storage

<details><summary><b>Chain signals a failed composition by returning a nil Store instead of an error</b></summary>

Refuted as a storage-specific naivety: the shape is a repo-wide convention, not an oversight here. `port.ChainService` is character-for-character the same construction — `if nilService(base) { return nil }` … `current = middleware[i](current); if nilService(current) { return nil }` (port/service.go:54-69) — and the repository uses the error-returning shape elsewhere when it decides to (`cache.Observers`/`cache.MustObservers`, cache/observers.go:28-34), so the choice is made deliberately at the level of the whole library rather than missed in `storage`. The behaviour is pinned non-vacuously by `TestChain_NilMiddlewareResultDoesNotFailOpen` (storage/store_chain_test.go:110-121) and `TestChain_NilBase` (:96-108), which assert exactly the nil result for untyped and typed nil, and the storage roadmap records "nil behaviour, typed-nil handling and panic policy" as an open M0 freeze item (docs/roadmaps/2026-09-01-storage-roadmap.md:270-272), so it is a tracked open question rather than an unexamined one. The failure is also loud and immediate (a nil-interface call panics on first use), not silent.

</details>

<details><summary><b>Neither a write nor a presigned link can carry Content-Disposition, Content-Encoding or Cache-Control</b></summary>

The code facts are correct — `PresignedGetObject(ctx, bucket, object, ttl, nil)` (storage/storageminio/backend.go:525) passes no `url.Values`, and `putOptions` sets only `ContentType` and `UserMetadata` (backend.go:568-571) — but the gap is sanctioned scope with a real escape hatch. The storage roadmap's Explicit non-goals name "adding `List`, recursive delete, automatic retry or provider raw options" (docs/roadmaps/2026-09-01-storage-roadmap.md:349-358), and `PutObjectOptions.ServerSideEncryption`/`StorageClass`/`ContentDisposition` are exactly provider raw options; the suggested fix is the thing declined. A portable disposition option would also collide with the other backend's deliberate posture: the filesystem link handler hard-codes `Content-Disposition: attachment` plus `nosniff`, sandbox CSP and `no-store` as defence in depth (storage/storagefs/link.go:143-148, documented at docs/modules/en/storagefs.md:60-63). And the consumer constructs and keeps the `*minio.Client` themselves (`Config.Client`, backend.go:39-45, storageminio.md:13-14), so a bespoke presign with `response-content-disposition` is available without abandoning the library. The SSE-mandating-bucket sub-case is the strongest part of the argument and is worth raising with the owner as a scope question, but it is not a defect in this module as scoped.

</details>

---

## jobs

<a id="jobs"></a>

### Verdict

The neutral half of this module — definitions, codecs, placement modes, dispositions, the invocation state machine — is unusually well specified, and most of the reviewers' contract findings dissolved on reading: the refusals are deliberate, the redactions are pinned by tests, and D-118/UC-031 already say out loud the things a naive job library gets wrong (at-least-once, unordered, producer-side dedup, one database). Where the module is genuinely naive is the seam between the worker loop and a delivery backend. Two of the three shipped backends read "a lease stamped with my incarnation" as "work I lost", which the dispatcher turns into revoking its own live attempts every reclaim tick — I reproduced duplicate handler execution over jobsmemory with nothing but the public API — and the heartbeat holds every active delivery's mutex across one batched driver call and charges a single transport failure to all of them, so the framework's own recommended fencing pattern can cost a pool its entire in-flight set. The scheduler is the thinnest surface: deliberately minimal is fine, but its one durability primitive is an EnqueueOnce intent whose retention is unrelated to the cadence, and both of its constructors accept a job whose codec makes that primitive unusable. The test suite is exhaustive at the command and driver-contract level and empty at the one sequence the module exists for — replacing the lease-takeover branch with a no-op leaves `go test ./jobs/...` green — which is precisely why the Recover defect was able to ship. Soundly minimal, and correctly so: the loud refusal of no-overlap scheduling, the absence of a broker adapter, the panic redaction from the error surface, and the refusal to describe delivery as exactly-once.

### Findings

| ID | Sev | Verdict | Kind | Where | What |
|---|---|---|---|---|---|
| `G-JOB-01` | 🔴 critical | CONFIRMED | naive-implementation | [backend.go:628](jobs/jobsmemory/backend.go#L628) | Recover returns the caller's own live leases, so the worker revokes its own in-flight attempts every reclaim tick (jobsmemory and jobsredis) |
| `G-JOB-02` | 🟠 high | CONFIRMED | naive-implementation | [workers_run.go:1263](jobs/workers_run.go#L1263) | The heartbeat locks every active delivery across one batched driver call and charges a single failure to all of them, so one slow fenced effect costs the whole pool its in-flight work |
| `G-JOB-03` | 🟡 medium | CONFIRMED | naive-implementation | [schedule.go:203](jobs/schedule.go#L203) | A schedule occurrence is deduplicated only by a producer intent whose retention is unrelated to the cadence, so the occurrence fires again once the intent is swept |
| `G-JOB-04` | 🟡 medium | CONFIRMED | naive-contract | [schedule.go:144](jobs/schedule.go#L144) | DefineSchedule and NewScheduler accept a job whose payload identity is unavailable, which the scheduler's only enqueue path can never place |
| `G-JOB-05` | 🟡 medium | CONFIRMED | naive-implementation | [worker_clock.go:38](jobs/worker_clock.go#L38) | The worker clock strips the monotonic reading and treats any backwards wall-clock step as a fatal, unrestartable runtime failure |
| `G-JOB-06` | 🟡 medium | CONFIRMED | vacuous-test | [workers_run.go:912](jobs/workers_run.go#L912) | The lease-takeover branch — the module's central crash guarantee — is unpinned at the worker level; turning it into a no-op leaves the suite green |
| `G-JOB-07` | 🟡 medium | CONFIRMED | naive-contract | [stager.go:20](jobs/jobspg/stager.go#L20) | jobspg.Driver.Stager adopts any *sql.Tx, so the explicit outbox path has none of the one-database refusal UC-031 point 5 promises |
| `G-JOB-08` | 🟡 medium | CONFIRMED | false-promise | [driver.go:324](jobs/jobsredis/driver.go#L324) | jobsredis honours neither Policy.Retention nor Policy.IntentRetention: once-intents and their records are written with no expiry and never swept, every other terminal record is deleted at once |
| `G-JOB-09` | 🟡 medium | CONFIRMED | missing-capability | [classifier.go:50](jobs/classifier.go#L50) | A handler panic's value and stack are discarded, nothing is logged, and no observer event carries a delivery outcome |
| `G-JOB-10` | 🟡 medium | CONFIRMED | naive-implementation | [admin_repo.go:75](jobs/jobspg/admin_repo.go#L75) | jobspg Admin List and Count sort and count the whole namespace with no index the migration ever creates |
| `G-JOB-11` | 🟡 medium | PLAUSIBLE | naive-implementation | [repo.go:73](jobs/jobsredis/repo.go#L73) | Every jobsredis mutation is a blind read-modify-write whose only guard is a namespace-wide SETNX lease that is never fenced or re-checked at write time |
| `G-JOB-12` | ⚪ low | CONFIRMED | false-promise | [durability.go:223](jobs/durability.go#L223) | Capabilities.OrderedPartition and ServerSideWakeup are dead bits that let a producer require an ordering D-118 forbids promising |
| `G-JOB-13` | ⚪ low | CONFIRMED | missing-capability | [schedule.go:147](jobs/schedule.go#L147) | SkipOverlap is a public constant DefineSchedule always refuses, ScheduleDescription.NoOverlap can never be true, and no other way exists to stop a scheduled run overlapping the previous one |

#### `G-JOB-01` — Recover returns the caller's own live leases, so the worker revokes its own in-flight attempts every reclaim tick (jobsmemory and jobsredis)

**🔴 critical** · **CONFIRMED** · `naive-implementation` · [jobs/jobsmemory/backend.go:628](jobs/jobsmemory/backend.go#L628) · `Backend.Recover / repository.recoveryIDs + (*Driver).Recover`

**Evidence**

jobs/jobsmemory/backend.go:628 selects recovery candidates with `if item.lease.incarnation == request.Incarnation() || !item.lease.expiresAt.After(now)` — any lease held by the caller's own incarnation, expired or not. jobs/jobsredis/repo.go:182 does the same by unioning expired leases with `ZRange(incarnationKey(incarnation))`, and jobs/jobsredis/driver.go:365 keeps them: `if !found || len(entry.LeaseToken) == 0 || entry.LeaseIncarnation != request.Incarnation().Bytes() && entry.LeaseUntil.After(now) { continue }` binds as `!found || len==0 || (notMine && stillValid)`, so mine-and-live survives and is re-tokened at driver.go:392. The incarnation is minted fresh per Run (jobs/workers_run.go:102 `newWorkerIncarnation`, random 16 bytes) and is the same value used to claim (workers_run.go:533) and to reclaim (workers_run.go:893), so the `==` branch can only ever match leases the live session is still executing. jobs/workers_run.go:382 fires the sweep every `reclaimInterval` (default min(15s, leaseTTL/2)); jobs/workers_run.go:912 turns a recovered `InvocationRunning` into `RevokeAttemptCommand(lease, ReasonLeaseLost, delay)`. jobspg does the opposite — jobs/jobspg/repo_ops.go:617 selects `WHERE lease_token IS NOT NULL AND lease_expires_at <= $2` with no incarnation term.

**Why it is naive**

It reads 'a lease stamped with my incarnation' as 'work I dropped'. That is only true for a worker identity that survives a restart; this framework mints the identity per `Run`, so the predicate matches exactly the set of deliveries the live session is still running. Neither driver has any bookkeeping that could tell a lease the pool still holds from one it lost, and the pool passes no in-flight set.

**Failure scenario**

Proved with the public API: `jobsmemory.NewDefault()` as both `Sender` and `DeliveryDriver`, one handler that sleeps 900 ms, `ReclaimInterval: 200ms`. The reclaim sweep hands the worker its own live delivery, rotates the token and applies `RevokeAttempt(ReasonLeaseLost)`; the handler completes, its `FinishAttempt` is refused as a stale lease, the invocation is back in Queued and runs a second time (handler start #1 completed, then handler start #2). With production defaults (reclaim 15 s) every handler slower than 15 s repeats its side effect and discards its own successful outcome on every tick until `MaxElapsed` (24 h) kills it as dead. `ReasonLeaseLost` is deliberately uncharged (D-118), so nothing bounds the loop.

**Suggested shape of the fix**

Recover only leases whose `expiresAt` is not after `now`, matching jobspg. If reclaiming an own uncertain claim (a Claim that committed but whose response was lost) is really wanted, it needs a worker identity that outlives `Run` plus the set of invocations the session still holds — not the per-run random incarnation.

**How it was verified**

Read both drivers at the cited lines and confirmed the Go binding of the redis condition. Ran a throwaway `Workers`+`jobsmemory` test (since deleted, tree clean) that logged two handler starts for one enqueue. jobs/jobsmemory/backend_test.go:116 (`TestRenewRotatesFenceAndRecoverFindsUncertainClaim`) pins the driver-level behaviour as intended, so it is deliberate at the driver seam — but no test runs it under `Workers`, and D-118/FL-035 describe recovery only as the takeover of a worker that stopped renewing. jobsredis is 'building' per docs/roadmaps/2026-09-01-jobs-cache-roadmap.md:56, pending 'crash/lease evidence' — that defers the evidence, not the defect. I could not exercise jobsredis live (no Redis here); its verdict rests on code reading plus the identical, empirically proven jobsmemory path.

*Merged from reviewer findings: jobs-drivers-redis-recover-steals-own-live-leases*

#### `G-JOB-02` — The heartbeat locks every active delivery across one batched driver call and charges a single failure to all of them, so one slow fenced effect costs the whole pool its in-flight work

**🟠 high** · **CONFIRMED** · `naive-implementation` · [jobs/workers_run.go:1263](jobs/workers_run.go#L1263) · `workerPool.renewActiveBatch`

**Evidence**

jobs/workers_run.go:1263-1287 takes `delivery.mu` for every delivery in the batch (up to `MaxClaimItems` = 256, jobs/bounds.go:51) and holds them all across `pool.workers.callRenew`. The same mutex is held across unbounded work elsewhere: jobs/workers_run.go:1375 `guard` does `delivery.mu.Lock(); defer Unlock(); ... fence.Fence(ctx, delivery.lease)`, and `Fence` is `SELECT 1 ... FOR UPDATE` on the delivery row inside the handler's own transaction (jobs/jobspg/repo_ops.go:462), which `InFencedTx` orders before the effect (jobs/jobspg/fencer.go:79) so the row lock is held for the whole effect. `jobspg.Renew` updates every requested lease in one transaction, so it blocks on that row. When the call errors or times out — `operationTimeout` defaults to `min(10s, (leaseTTL-heartbeat)/3)` (jobs/workers_config.go:320) — jobs/workers_run.go:1288 charges it to the whole batch: `if call.err != nil || result.Len() != len(locked) { for _, delivery := range locked { delivery.closeLost(call.err) ... } }`, which cancels each handler with `ErrLeaseLost` and refuses every later `apply`. Nothing records how much lease life was actually left: `LeaseRef` carries no expiry and the `RenewResult.observedAt` that would give one is discarded.

**Why it is naive**

It assumes a renewal is fast and that a failed renewal means the lease is already gone. The framework's own recommended fencing pattern makes the renewal wait on the effect it is supposed to be covering, and a renewal that fails at the first heartbeat (15 s into a 60 s TTL) still has three chances left before the lease actually expires.

**Failure scenario**

A handler follows FL-035 step 3 and calls `driver.InFencedTx(ctx, controller, nil, effect)` with an effect that writes for 12 s. Its fenced transaction holds `FOR UPDATE` on its delivery row from `Guard` to commit. The next heartbeat locks all N active deliveries, issues one `Renew` covering all of them, blocks on that row until the 10 s operation timeout, and then marks all N lost — including unrelated jobs whose leases had 45 s left. Their handlers are cancelled mid-write with `ErrLeaseLost` and their invocations sit Running until the lease expires and someone redelivers them. Any fenced effect longer than the operation timeout can therefore never complete: every attempt is killed at the heartbeat.

**Suggested shape of the fix**

Do not hold `delivery.mu` across a driver call — copy the lease under a short lock and `TryLock`/skip a delivery that is mid-`Guard` or mid-`apply`. And stop treating one transport failure as terminal: record `observedAt + leaseTTL` from the last successful renewal and declare a lease lost only when that horizon passes or the driver answers `DeliveryMutationLeaseLost` for that item.

**How it was verified**

Read every cited line and the surrounding paths. The amplification is worse than the reviewer stated in one respect and softer in another: `apply` also holds `delivery.mu` across a driver round trip (workers_run.go:1188), so even an ordinary slow `FinishAttempt` blocks the heartbeat for the whole pool; conversely a non-fatal renew failure does not kill the pool (`call.fatal()` is only contract/panic/runtime, worker_driver.go:20), it just loses the batch's leases. The 10 s threshold is derived from the default resolution in workers_config.go:320, not measured against a live PostgreSQL.

#### `G-JOB-03` — A schedule occurrence is deduplicated only by a producer intent whose retention is unrelated to the cadence, so the occurrence fires again once the intent is swept

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [jobs/schedule.go:203](jobs/schedule.go#L203) · `scheduleIntent / typedSchedule.scheduleEntry`

**Evidence**

jobs/schedule.go:203 builds the whole identity of an occurrence as `"schedule:"+name+":"+revision+":"+due.UnixNano()`, and jobs/schedule.go:186 places it with `EnqueueOnce(ctx, queue, definition, Intent(key), payload)`. That reservation has a finite life chosen for forensics, not for the cadence: jobs/policy.go:280 only requires `IntentRetention >= Retention`, jobs/bounds.go:76 defaults it to 30 days, and jobs/jobspg/retention.go:112 falls through to `deleteTerminal` once `now` passes `intentExpiresAt` — `DELETE FROM deliveries WHERE namespace=$1 AND id=$2` (jobs/jobspg/retention_repo.go:232), with the intents table carrying `FOREIGN KEY (namespace, invocation_id) REFERENCES deliveries ON DELETE CASCADE` (jobs/jobspg/repo.go:196). The reservation is gone, not tombstoned. Meanwhile jobs/schedule.go:85-89 makes a one-shot permanently due (`return cadence.at, time.Time{}, true`) and jobs/schedule.go:284-294 re-enqueues every due schedule on every cycle. Neither `DefineSchedule` (schedule.go:144) nor `NewScheduler` (schedule.go:234) looks at the definition's `IntentRetention`, and `FixedEvery` accepts any period up to `MaxRetention` = 365 days.

**Why it is naive**

It assumes the dedup reservation outlives the occurrence it identifies. The reservation's lifetime is a retention policy; the cadence is a business decision; nothing relates the two, so the only thing keeping a scheduled occurrence from running twice disappears on a timer nobody connected to it.

**Failure scenario**

A one-shot `At(t)` schedule (a migration or a cut-over job) shares a scheduler with an hourly schedule. It runs at t and goes terminal. 30 days later the retention sweeper deletes the delivery row and cascades away the intent. On the next tick `occurrence(now)` still reports `due = t`, `EnqueueOnce` finds no reservation, returns `EnqueueCreated`, and the one-shot runs a second time — and again every 30 days, or on any process restart thereafter. The same happens to `FixedEvery(45*24*time.Hour, ...)`: any restart between day 30 and day 45 re-places the day-0 occurrence.

**Suggested shape of the fix**

Refuse the combination where it is decidable — a cadence whose period exceeds the job's `IntentRetention`, and an `At` cadence, which is unbounded — or make the scheduler remember the last `due` it placed per schedule and enqueue only strictly newer occurrences.

**How it was verified**

Verified every link: the cascade FK, the terminal-state candidate query (retention_repo.go:61), the delete, and that `occurrence` returns a fixed `due` for a one-shot and for any window longer than the retention. I did not run a live 30-day sweep. D-118 sanctions only that `EnqueueOnce` collapses two enqueues into one invocation; here two enqueues become two invocations, and the natural idempotency key it recommends (the `InvocationID`) differs between them, so the decision's own mitigation does not cover this.

#### `G-JOB-04` — DefineSchedule and NewScheduler accept a job whose payload identity is unavailable, which the scheduler's only enqueue path can never place

**🟡 medium** · **CONFIRMED** · `naive-contract` · [jobs/schedule.go:144](jobs/schedule.go#L144) · `DefineSchedule / NewScheduler`

**Evidence**

The scheduler's only enqueue path is `EnqueueOnce` (jobs/schedule.go:186), and jobs/queue.go:575 refuses it outright: `if once && !definition.PayloadIdentity().Available { return enqueueRequest{}, ErrUnsupported }`. `Available` is true only for an explicit `PayloadIdentity[P]` or a `SafeCodecMode` codec (jobs/definition.go:86). `TrustedJSON` is `TrustedCodecMode` (jobs/json.go:79), and safe `JSON` refuses any type reachable from a `json.Marshaler`/`TextMarshaler` (jobs/json.go:1074 via `jsonUnsafeHook`, jobs/json.go:1350) — which includes `time.Time`, the value the scheduler itself hands the author through `Payload func(time.Time) (P, error)`. `DefineSchedule` (schedule.go:144) validates name, revision, cadence, job, payload func, overlap and partition; `NewScheduler` (schedule.go:248) validates catalog membership and name uniqueness. Neither looks at `Available`.

**Why it is naive**

The declaration path treats 'how do I encode this' and 'can this be deduplicated' as independent, but the codec mode silently decides the second, and the scheduler cannot work without it. Two constructors that exist to validate a schedule both accept one that is structurally undeliverable.

**Failure scenario**

Ran it: `type probePayload struct{ Due time.Time }` — `JSON[probePayload](1)` is refused by `Define` ('jobs: invalid value'), so the author uses `TrustedJSON[probePayload](1)`, whose `PayloadIdentity().Available` is false. `DefineSchedule` accepts it, `NewScheduler` accepts it, and the first `RunDue` returns `ScheduleRunResult{Due:1}` with `jobs: unsupported`. `Run` propagates it; under `jobsfx` + `runtimefx` the supervisor takes the process down (D-108, pinned by jobs/jobsfx/jobsfx_test.go). Because the fault is deterministic, the restart hits it again — and for an `At(future)` cadence the process runs healthily until the schedule falls due and then crash-loops.

**Suggested shape of the fix**

Make `DefineSchedule` refuse a job whose `PayloadIdentity().Available` is false, since the scheduler has no other placement mode; and let an identity be supplied independently of the codec mode so `TrustedJSON` payloads can still be scheduled.

**How it was verified**

Confirmed empirically with a throwaway test in package jobs (since deleted; `git status` clean for tracked files). Note the mitigating detail the reviewer missed: `RFC3339UTC` is a safe codec for a bare `time.Time` payload, so the very simplest schedule payload does work — the trap is the ordinary struct payload.

#### `G-JOB-05` — The worker clock strips the monotonic reading and treats any backwards wall-clock step as a fatal, unrestartable runtime failure

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [jobs/worker_clock.go:38](jobs/worker_clock.go#L38) · `workerClock.now`

**Evidence**

jobs/worker_clock.go:38 `if clock.started && now.Before(clock.last) { return time.Time{}, ErrInvalid }`. The reading is funnelled through `callWorkerClockNow` → `requiredTime` (jobs/invocation.go:1031), which does `value = value.Round(0).UTC()` — `Round(0)` strips Go's monotonic reading, so the comparison is pure wall clock; jobs/worker_clock_test.go:77 pins the stripping and jobs/workers_config.go:23 makes `time.Now()` the default source. Every consumer treats the result as fatal: jobs/workers_run.go:377 `now, err := pool.workers.config.clock.Now(); if err != nil { pool.fail(err); return }`, and `pool.fail` calls `session.requestForce()` (workers_run.go:962), which force-cancels every in-flight attempt with `ErrTerminated` and makes `Workers.Run` return `ErrInvalid`. The pool cannot be restarted: `runtime.begin` only transitions from Fresh (workers_run.go:172), so a second `Run` returns `ErrConflict`, and `Workers.Check` returns the latched failure (jobs/health.go:12).

**Why it is naive**

It models the host clock as monotonic. `Clock.Now() time.Time` is an exported seam with no stated monotonicity requirement, and the default implementation cannot meet the requirement the code imposes because the framework itself discards the monotonic reading that would have made `time.Now()` safe.

**Failure scenario**

chronyd steps the clock back (it steps rather than slews above its step threshold, and always after a VM suspend/resume or live migration). The next `clock.Now()` in the dispatch loop returns `ErrInvalid`, every running handler is force-cancelled mid-work, and `Workers.Run` returns. The `*Workers` value is single-use, so nothing in-process can recover: under `jobsfx` the supervisor takes the process down and the orchestrator restarts it, and under a hand-wired `runtime.Supervisor` the runner is merely logged as failed (runtime/supervisor.go:179) while the process stays up and delivers nothing.

**Suggested shape of the fix**

Keep the monotonic reading for interval and deadline arithmetic (compare a `time.Time` that has not been through `Round(0)`), and treat a wall-clock regression as an observable, recoverable tick — skip and retry — rather than `pool.fail`. If strict monotonicity is genuinely required of an injected `Clock`, state it on the interface and stop stripping it from the default.

**How it was verified**

jobs/worker_clock_test.go:134 shows the clock itself does not poison `last`, so a *transient* regression recovers at the clock level — but the first `ErrInvalid` has already reached `pool.fail`, which is unconditional. Severity lowered from the reviewer's 'high' because the recommended fx wiring turns this into a process restart rather than a silent dead worker.

#### `G-JOB-06` — The lease-takeover branch — the module's central crash guarantee — is unpinned at the worker level; turning it into a no-op leaves the suite green

**🟡 medium** · **CONFIRMED** · `vacuous-test` · [jobs/workers_run.go:912](jobs/workers_run.go#L912) · `workerPool.prepareRecovered`

**Evidence**

jobs/workers_run.go:911-921 is the whole takeover path: `case InvocationRunning, InvocationCancelRequested:` → sample a delay → `RevokeAttemptCommand(lease, ReasonLeaseLost, delay)`. The only test that calls `prepareRecovered` is jobs/workers_admission_test.go:266, whose fixture invocation takes the sibling `InvocationQueued` branch (it asserts a `ClaimedDelivery` came back, which the Running branch never returns). Every other worker-level driver double returns an empty `RecoverResult` (workers_run_test.go:309, workers_timer_test.go:160, workers_config_test.go:45). I mutated the branch twice and ran `go test ./jobs/...`: swapping `ReasonLeaseLost` for `ReasonShutdown` — green; replacing the whole branch with `return ClaimedDelivery{}, false, true` (recover a running invocation and silently drop it) — green.

**Why it is naive**

The suite covers the delivery command state machine and the driver contract exhaustively, and never the one sequence the module exists for: worker A begins an attempt, stops renewing, worker B takes the invocation over, and A's later write is refused. UC-031 point 7 is asserted nowhere above the driver seam — which is why the `Recover` defect reported separately ships unnoticed.

**Failure scenario**

Any regression in the takeover path ships silently. Concretely, the `jobsmemory`/`jobsredis` self-recovery defect turns this exact branch into a self-inflicted revoke of live work and no test in the repository notices.

**Suggested shape of the fix**

Add a worker-level test that recovers an `InvocationRunning` delivery and asserts the `RevokeAttempt`/`ReasonLeaseLost` command and the uncharged retry, plus a live test where the superseded owner's fenced write is refused after another incarnation recovers the invocation — with the control case that shows the write does land when the fence is not taken, in the style of test/integration/gate_relscope_test.go.

**How it was verified**

Corrected the reviewer: jobs/jobspg/integration_test.go:275-302 *does* have a driver-level expiry/takeover test (claim as incarnation 1, sleep past the lease TTL, recover as incarnation 2), and jobs/jobspg/control_integration_test.go:67 pins that a stale lease's `BeginAttempt` answers `DeliveryMutationLeaseLost`. The gap is the worker-level orchestration between them, which the two mutations prove is unpinned. Tree restored after mutating.

#### `G-JOB-07` — jobspg.Driver.Stager adopts any *sql.Tx, so the explicit outbox path has none of the one-database refusal UC-031 point 5 promises

**🟡 medium** · **CONFIRMED** · `naive-contract` · [jobs/jobspg/stager.go:20](jobs/jobspg/stager.go#L20) · `(*Driver).Stager`

**Evidence**

jobs/jobspg/stager.go:20-42 checks only `d.requireReady()` and `tx == nil`. Nothing relates `tx` to `d.db`, and `database/sql` gives a `*sql.Tx` no way to name the `*sql.DB` it came from, so as declared it cannot be checked. `TxStager.Stage` then writes through that handle: stager.go:62 `s.driver.placeInTx(ctx, s.tx, placement)`, inserting into `schema.deliveries`/`schema.intents` on whatever connection the tx holds. The ambient path is guarded — jobs/jobspg/config.go:87 `if spec.Source != nil && !crud.SameDataSource(crud.KeyOf(spec.Source), spec.DB)` — but `Stager` is reachable on a driver built with no `Source` at all, so that guard never runs for it. `jobs.EnqueueIn` checks only that the stager's `TransactionContext` names the same backend (jobs/queue.go:876), which it does. UC-031 point 5 states the guarantee for both shapes named in point 2: 'The obligation and the decision live in one database, and a configuration that would put them in two is refused when the program is assembled, not when a request arrives.' jobs/README.md documents `driver.Stager(tx)` as the supported explicit form.

**Why it is naive**

The signature takes a bare `*sql.Tx` and assumes the caller passes one from the driver's own pool. That is exactly the assumption D-118 says must be caught at construction, and it is the one an application with a second `*sql.DB` (tenant-per-database, a second bounded context, a reporting handle) breaks by ordinary refactoring.

**Failure scenario**

An application with a shared jobs database and per-tenant databases that both carry `frostgrove_jobs` (the schema name is a default and `MigrationStatements` is run per database in a DB-per-tenant deployment) writes `tx, _ := tenantDB.BeginTx(ctx, nil); stager, _ := jobsDriver.Stager(tx); staged, _ := jobs.EnqueueIn(ctx, queue, stager, NotifyJob, payload)`. Every check passes, an `InvocationID` comes back, the tenant transaction commits — and the invocation row is in the tenant database, which no worker polls, while the driver's own database never received it. No error, no row anywhere the operator would look. Where the second database has no jobs schema the same call is a loud 'relation does not exist', so the silent case is the one that ships.

**Suggested shape of the fix**

Make the stager mint its own transaction (`Driver.StageIn(ctx, func(*TxStager) error)` beginning on `d.db`), or require the caller to hand over the `*sql.DB`/`crud.Source` alongside the tx so `SameDataSource` can run, and refuse `Stager` on a driver with no `Source` the way `InFencedTx` already does (fencer.go:63).

**How it was verified**

D-118's 'One database' section names this exact failure but states the refusal only for `New`/`Spec.Source`, and its proven-by test `TestSpecSourceMustNameTheConfiguredDatabase` (jobs/jobspg/transaction_test.go:74) covers only the ambient path — so the decision's rationale covers the failure and the code does not implement it on the path the README advertises. Severity lowered from the reviewer's 'high' because the silent variant needs the jobs schema present in both databases.

#### `G-JOB-08` — jobsredis honours neither Policy.Retention nor Policy.IntentRetention: once-intents and their records are written with no expiry and never swept, every other terminal record is deleted at once

**🟡 medium** · **CONFIRMED** · `false-promise` · [jobs/jobsredis/driver.go:324](jobs/jobsredis/driver.go#L324) · `(*Driver).Apply / repository.save`

**Evidence**

jobs/jobsredis/driver.go:324 `if updated.State.Terminal() && updatedRecord.Genesis.Mode != jobs.PlacementOnce { err = d.repo.save(opCtx, &entry, nil) } else { err = d.repo.save(opCtx, &entry, &updated) }` — a terminal non-once delivery is deleted immediately (retention 0), a terminal once delivery is kept forever: jobs/jobsredis/repo.go:234 `pipe.Set(ctx, r.entryKey(current.ID), encoded, 0)`, repo.go:235 `pipe.SAdd(ctx, r.deliveriesKey(), current.ID)` and repo.go:244 `pipe.Set(ctx, r.intentKey(intent), current.ID, 0)` — the trailing `0` is the Redis TTL. Nothing in the package reads the policy it faithfully serialises (jobs/jobsredis/record.go:339 round-trips `Retention`/`IntentRetention`); `jobs.RetentionSweeper` is implemented only by jobspg (jobs/jobspg/config.go:63), and jobs/jobsredis/config.go:45 declares only Sender, DeliveryDriver and Controller. The knobs are validated and defaulted for every definition — jobs/policy.go:280 refuses `Retention <= 0`, jobs/bounds.go:75 defaults 7 d / 30 d — and jobspg computes and sweeps them in bounded batches.

**Why it is naive**

It models the queue as the set of live entries and never models the terminal tail the policy describes, so a knob the neutral contract validates on every definition is accepted and discarded by one of the shipped backends. It also assumes Redis memory is free for keys that are only ever written.

**Failure scenario**

An application uses `EnqueueOnce`/`Intent` for invoice processing at 50 jobs/s with the default 30-day `IntentRetention`. Every completed once-invocation leaves a full encoded delivery record at `…:delivery:<id>`, a member of `…:deliveries`, and one `…:intent:<key>` string, none with a TTL and nothing to sweep them: ~130 M permanent keys after a month. The instance reaches maxmemory and either evicts — silently destroying the deduplication the caller relies on — or refuses writes and takes the queue down. Separately, the same key set means the intent never expires, so a schedule or an `EnqueueOnce` key that would be re-usable after `IntentRetention` on jobspg is permanently consumed here: the same program has two different dedup lifetimes depending on the backend.

**Suggested shape of the fix**

Set the entry and intent keys with an `EXPIRE` derived from `Genesis.Policy.Retention()`/`IntentRetention()` at the terminal transition (and sweep the `deliveries` index members), so no `RetentionSweeper` is even needed — or refuse definitions the backend cannot honour at construction, the way `resolveQueueDurability` already refuses an unmeetable durability requirement (jobs/queue.go:145).

**How it was verified**

Read every save path in jobsredis; no TTL anywhere and no sweeper. jobsredis implements no `Admin`, so the immediate deletion of non-once terminal records costs no operator-visible history through this library — the unbounded once-key growth and the divergent dedup lifetime are the real halves. The roadmap lists jobsredis as 'building' but names retention nowhere among the gaps.

#### `G-JOB-09` — A handler panic's value and stack are discarded, nothing is logged, and no observer event carries a delivery outcome

**🟡 medium** · **CONFIRMED** · `missing-capability` · [jobs/classifier.go:50](jobs/classifier.go#L50) · `invokeHandlerContained / workerEventSpec`

**Evidence**

jobs/classifier.go:50-55 `defer func() { if recover() != nil { result = HandlerFailure{panicked: true, initialized: true} } }()` — the recovered value and the stack are dropped. `HandlerFailure.Unwrap` returns nil for a panic (classifier.go:26), so even a custom `ErrorClassifier` learns only the boolean `Panicked()`. Nothing logs it: `grep port.Logger jobs/` is empty, while the same contained panic in every HTTP transport is logged with the value (crud/http/crudnet/middleware.go:41). D-062 names exactly this case as a reason `port.Logger` exists — 'a handler panicked and the connection has to be closed' — and `port` is TIER0 (scripts/checks.sh:13), so jobs may import it. The observer cannot fill the gap: `WorkerOperation` (jobs/worker_observer.go:16-23) is run/drain/claim/recover/renew/apply/admission, and `workerEventSpec`/`WorkerEvent` (worker_observer.go:180, 208) carry no disposition and no reason — `observeApply` reports the command kind plus mutation/control counts, and its `Elapsed` is the driver round trip, not the handler's runtime.

**Why it is naive**

It models the interesting failures as driver failures. For a job runner the operationally decisive facts are per-definition outcome (succeeded / retrying / permanently failed) and why a handler died, and the second is expressible in no seam the module exposes.

**Failure scenario**

A handler panics on a nil map for one tenant's payload. Nothing is logged, no `WorkerEvent` distinguishes it from a success (both surface as `apply`/`applied`), and the classifier that could count it cannot say what panicked or where. The operator sees the retry count climb with `ReasonPanic` and has no message, no stack and no line number anywhere in the process — while the same panic in an HTTP handler of this repository produces a logged line carrying the panic value.

**Suggested shape of the fix**

Keep the recovered value and a captured stack privately on `HandlerFailure` and log them through `port.Logger(ctx)` as the HTTP middlewares do, and add a delivery-outcome event to the observer vocabulary (a handle operation, or at minimum `Disposition`/`Reason` on the apply event) so success, retry and permanent failure are distinguishable per definition.

**How it was verified**

Narrowed the reviewer's claim: an application *can* observe ordinary handler failures — `jobs.Classify` hands a per-definition `ErrorClassifier` the `HandlerFailure`, whose `Unwrap` returns the real error — so only the panic case is opaque. The redaction of the panic value from the error surface is deliberate and pinned (jobs/classifier_test.go:66 asserts the value is never formatted), which explains the error contract but not the absence of a log or an observer field; D-062 endorses exactly that split.

#### `G-JOB-10` — jobspg Admin List and Count sort and count the whole namespace with no index the migration ever creates

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [jobs/jobspg/admin_repo.go:75](jobs/jobspg/admin_repo.go#L75) · `repository.listDeliveryRecords / repository.countDeliveryRecords`

**Evidence**

jobs/jobspg/admin_repo.go:75 `SELECT record FROM <deliveries> WHERE namespace = $1 AND record IS NOT NULL [AND definition IN (…)] [AND state IN (…)] ORDER BY created_at DESC, id DESC LIMIT $n OFFSET $n+1`, and admin_repo.go:112 the matching `count(*)`. The only indexes ever created are the primary key `(namespace, id)` (jobs/jobspg/repo.go:150) and three exactly-validated operational ones (jobs/jobspg/operational_migration.go:21-40): `deliveries_ready_idx (namespace, definition, priority, available_at, id) WHERE state = 1 AND lease_token IS NULL`, `deliveries_expired_idx (namespace, lease_expires_at, id) WHERE lease_token IS NOT NULL`, `intents_invocation_idx`, plus the partial retention index. None contains `created_at`, and the List predicate has no state filter by default, so the plan is every row in the namespace plus a sort. The offsets the contract admits are large — `MaxListOffset = 1_000_000` (jobs/redrive.go:15) — and the table is meant to be big: terminal rows are retained for `DefaultTerminalRetention` = 7 days before the sweeper touches them.

**Why it is naive**

The bound on the answer (limit, offset, filters, `normalizeListSpec`) is mistaken for a bound on the work: it limits what comes back, not what PostgreSQL reads. Every other query in this package was given an exact, validated index; this one was not.

**Failure scenario**

An operator opens the jobs console on a queue doing 100 jobs/s. With 7-day retention the deliveries table holds tens of millions of rows for that namespace, and `List(ListSpec{Limit: 100})` makes PostgreSQL read and sort all of them to return the first page, then `Count` scans them again — on the same pool the workers claim through, so looking at the queue degrades the queue. Paging deeper repeats the full sort.

**Suggested shape of the fix**

Add `(namespace, created_at DESC, id DESC)` to the operational index set (it goes through the same exact-match validation as the other three), or order by something the existing keys can serve; and replace offset paging with the sort-tuple cursor this repository prefers elsewhere.

**How it was verified**

docs/roadmaps/2026-09-01-jobs-cache-roadmap.md:73 acknowledges only the response side ('selects and materializes each full payload-and-ledger record with no aggregate byte budget') and keeps the Admin slice 'building' for that reason; it says nothing about the database-side scan, which no byte budget or summary projection would fix.

#### `G-JOB-11` — Every jobsredis mutation is a blind read-modify-write whose only guard is a namespace-wide SETNX lease that is never fenced or re-checked at write time

**🟡 medium** · **PLAUSIBLE** · `naive-implementation` · [jobs/jobsredis/repo.go:73](jobs/jobsredis/repo.go#L73) · `repository.lock / repository.save`

**Evidence**

jobs/jobsredis/repo.go:73-96: the lock is `SetNX(base+":mutation-lock", encoded, ttl)` in a 5 ms spin loop, taken by `begin` with `ttl = d.operationTimeout*2` (driver.go:418) on every entry point (Place 23, Claim 115, Renew 202, Apply 257, Recover 347, Cancel/Terminate control.go:28). The critical section is a read-modify-write with no compare-and-set of its own: `entry()` GETs (repo.go:110), the driver mutates the struct, and `save` overwrites blind — repo.go:212 `TxPipelined` with no `WATCH` and repo.go:234 `pipe.Set(...)`. `storedEntry` (repo.go:22) carries no version or epoch a writer could check, so nothing in the write path can notice the state changed under it. The release ignores its own outcome: repo.go:85 `_, _ = releaseLockScript.Run(...)`. jobspg gets the same guarantees from the store instead — `FOR UPDATE SKIP LOCKED` and state-guarded updates such as `WHERE ... AND lease_token IS NULL` (jobs/jobspg/repo_ops.go:403, 425).

**Why it is naive**

Correctness rests on a distributed mutex built from SETNX on a single asynchronously-replicated primary, and the code assumes that holding it at entry means still holding it at every write. The neutral contract's claim/apply semantics are enforced by the database in jobspg and only by an advisory lease here.

**Failure scenario**

Redis fails over (or an operator promotes a replica) between one worker's `entry()` and its `save()`. The new primary never received the `mutation-lock` key, so a second worker's SETNX succeeds; both read invocation X with `LeaseToken == nil`, both build a claim, and both `save` — the later blind SET wins, so two workers each hold what they believe is a valid lease and run the same attempt concurrently, and the loser's Apply reports LeaseLost, discarding a completed effect. jobsredis implements no `FencedTransactions`, so a handler has no fence to fall back on.

**Suggested shape of the fix**

Stop relying on the mutex for correctness: give `storedEntry` a monotonic version and make `save` a Lua script that re-reads, compares the version (or the lease token) and refuses when it changed, returning a conflict the driver can turn into 'not claimed'. The mutex then becomes an optimisation rather than the invariant.

**How it was verified**

Confirmed every cited line. Downgraded from the reviewer's 'high' and marked PLAUSIBLE: I could not exercise a failover, and the pause window is partly mitigated by a margin the reviewer missed — the operation context is bounded at `operationTimeout` while the lock TTL is `2 × operationTimeout`, so a merely slow holder is normally cut off by its own deadline. The failover case has no such mitigation. The declared profile (`AckBeforePersistence`/`AcknowledgedLossPossible`, config.go:82) covers losing acknowledged writes, not losing mutual exclusion.

#### `G-JOB-12` — Capabilities.OrderedPartition and ServerSideWakeup are dead bits that let a producer require an ordering D-118 forbids promising

**⚪ low** · **CONFIRMED** · `false-promise` · [jobs/durability.go:223](jobs/durability.go#L223) · `Capabilities.OrderedPartition / satisfies`

**Evidence**

jobs/durability.go:217-237 declares `OrderedPartition bool` and `ServerSideWakeup bool` in the exported capability set and matches them in `satisfies`, which `NewQueue` runs against the backend at construction (jobs/queue.go:103, over the public `QueueSpec.Requirements` / `RequireProducerCapabilities`). A repository-wide grep finds no other reader: no shipped backend sets either bit, no test names them, no doc mentions them. Ordering is not a backend property in this design — the dispatcher decides it and decides against: jobs/workers_run.go:780-808 starts one goroutine per claimed delivery, capped only per binding and per admission group, and `ClaimTarget` (jobs/delivery_driver.go:74) has no partition field, so a claim cannot even ask for one partition's worth of work. D-118's 'What it forbids' says: 'Do not promise ordering — not per queue, not per partition, not per definition'; UC-031 point 9 puts ordering out of scope.

**Why it is naive**

The capability set models ordering as something a backend can provide, when the worker loop is what would have to provide it and structurally does the opposite.

**Failure scenario**

A consumer writes their own `DeliveryDriver` over a FIFO-partitioned store (the driver contract is fully exported for exactly this), sets `OrderedPartition: true` in its `Description()`, and declares `RequireProducerCapabilities(Capabilities{OrderedPartition: true})` on the producer side. Construction succeeds because both halves agree, and the worker then runs two invocations of the same partition concurrently in separate goroutines, with nothing anywhere reporting a violated guarantee.

**Suggested shape of the fix**

Delete both bits from `Capabilities`, or make `OrderedPartition` mean something the dispatcher enforces (a serialization key on `ClaimTarget` and a partition-keyed slot in `acceptClaimed`). A capability nothing can honour is worse than an absent one.

**How it was verified**

Verified that the negotiation is reachable from the public API (`QueueSpec.Requirements` → `satisfies`) and that nothing outside durability.go reads either field. Kept at low because reaching the failure requires a consumer to write a driver that sets a bit no shipped backend sets.

#### `G-JOB-13` — SkipOverlap is a public constant DefineSchedule always refuses, ScheduleDescription.NoOverlap can never be true, and no other way exists to stop a scheduled run overlapping the previous one

**⚪ low** · **CONFIRMED** · `missing-capability` · [jobs/schedule.go:147](jobs/schedule.go#L147) · `ScheduleOverlap / SkipOverlap / ScheduleDescription.NoOverlap`

**Evidence**

jobs/schedule.go:100-105 exports the mode and reports it legal: `const ( AllowOverlap ScheduleOverlap = iota; SkipOverlap )` with `Valid()` returning true for both. jobs/schedule.go:147 then refuses it unconditionally: `if spec.Overlap == SkipOverlap { return nil, fmt.Errorf("%w: durable no-overlap scheduling is not available", ErrUnsupported) }`, so schedule.go:161 `NoOverlap: spec.Overlap == SkipOverlap` can only ever be false and the `NoOverlap` field on the exported `ScheduleDescription` (schedule.go:123) is dead. The primitive that would implement it exists one layer down and the scheduler cannot reach it: `jobs.Unique(key)` yields `PlacementExisting` while an invocation under that key is live (jobs/queue.go:383), but `scheduleEntry.enqueue` hard-codes `EnqueueOnce` with a per-occurrence key (schedule.go:186).

**Why it is naive**

A cadence shorter than the job's worst-case runtime is the normal case for a maintenance job, not an exotic one. The API names the concept, so a reader believes it exists, and the only escape is to stop using `Scheduler` and drive `Enqueue(..., Unique(key))` from a ticker of one's own.

**Failure scenario**

An author declares a 15-minute reindex schedule; one run degrades and takes 40 minutes, so three handlers rebuild the same index concurrently. Writing `Overlap: SkipOverlap` to prevent that fails at construction with `ErrUnsupported`, and no option on `ScheduleSpec`, `ScheduleCadence` or the enqueue options reaches `PlacementUnique` from a schedule.

**Suggested shape of the fix**

Either implement it — have `scheduleEntry.enqueue` use a per-schedule `Unique` key instead of a per-occurrence `EnqueueOnce` key when it is requested — or remove `SkipOverlap` and `ScheduleDescription.NoOverlap` from the public surface so the API stops naming a capability that does not exist.

**How it was verified**

jobs/schedule_test.go:133 (`TestSchedulerDefaultsToAllowOverlapAndRejectsUnsupportedNoOverlap`) pins the refusal, so the refusal is deliberate and loud, not an oversight in one branch — which is why this is low rather than medium. What the test does not justify is exporting a constant whose only reachable effect is an error, plus a public struct field that is now unreachable.

### Refuted in jobs

<details><summary><b>One schedule's enqueue error abandons every remaining schedule in the cycle, and ScheduleRunResult cannot report it</b></summary>

The code says what the reviewer says (jobs/schedule.go:291 returns on the first error; schedule.go:310/313 make `Run` single-use), but the consequence does not survive. D-108's 'Proven by' explicitly sanctions the dying half ('a scheduler that dies is named by the supervisor and takes the process down'), and no occurrence is actually lost: `occurrence(now)` is recomputed from the wall clock, so after the supervisor's restart the same window is still due and every schedule that was skipped is placed. The remaining content — a result struct that cannot express partial failure, and a hand-rolled `for { s.Run(ctx) }` spinning on ErrConflict — is a cosmetic gap in a shape `Workers` shares deliberately (jobs/workers_run.go:172).

</details>

<details><summary><b>ScheduleCadence can express only a fixed offset from an absolute anchor: no timezone, no calendar cadence, missed windows collapsed</b></summary>

All the code claims check out (jobs/schedule.go:44-95; no `time.Location` anywhere in jobs/), but nothing here is a broken promise: the two constructors are honestly named `At` and `FixedEvery`, no doc, decision or use case offers civil-time or cron semantics, and skip-to-latest is a defensible default for a fixed-interval scheduler. This is a feature request against a deliberately minimal surface, not a defect — the reviewer's DST example is a consequence of asking a fixed-interval API for a civil-time schedule.

</details>

<details><summary><b>The automatic payload identity digests the wire encoding while calling itself semantic, and the test that pins it is vacuous</b></summary>

The vacuity claim is wrong. jobs/definition_test.go:48 defines two definitions with *different codec versions* (`String(1)` and `String(2)`) and asserts one digest; since `digestEncodedPayload` is called with `identityInfo.Version` (the constant `AutomaticPayloadIdentityVersion`, definition.go:16) rather than `codecInfo.version`, the assertion is exactly what fails if the codec revision is ever mixed into the identity — it pins a real property. The 'semantic' word appears only in an unexported domain-separator string (placement.go:319), never on the public surface, and returning `EnqueueConflict` when two payloads genuinely encode to different bytes is defensible rather than a wrong answer.

</details>

<details><summary><b>jobsredis Claim, Renew and Recover mutate one entry per MULTI/EXEC, so a mid-batch failure leaves leases issued or rotated that the caller never receives</b></summary>

The code reads as described (per-item `repo.save` inside the loop, driver.go:182/233/396), but both consequences dissolve. The Renew half is indistinguishable from what the worker already does with *any* renew error: jobs/workers_run.go:1288 marks every delivery in the batch lost on `call.err != nil` regardless of driver atomicity, which is already reported as jobs-renew-batch-loses-every-lease. The Claim half self-heals: a claim persisted but not returned leaves the invocation in state Queued with a lease, which expires at `LeaseUntil`, is picked up by the expired-lease arm of `recoveryIDs` and dispatched through the Queued branch of `prepareRecovered` — a delay bounded by the lease TTL, not a loss.

</details>

<details><summary><b>Nothing in the neutral contract says whether a backend can join the caller's transaction</b></summary>

The load-bearing claim — 'no boot assertion can be written to catch it' — is false. The repository's own D-118 proven-by test does exactly that assertion with public API only: `TestPostgresAmbientCRUDPlacement` enqueues inside `crud.InNewTx`, rolls back, and asserts `ErrInvocationNotFound`, then commits and asserts the invocation is there. UC-031 point 6's 'the author can assert which one their program built' is therefore satisfiable, and D-118 explicitly sanctions a driver built without a `Source` as 'a legitimate configuration, not a degraded one'. A `Capabilities.TransactionalPlacement` bit would be an improvement, not a defect.

</details>

---

## cache

<a id="cache"></a>

### Verdict

This module is unusually well-reasoned in the places its decision docs cover, and genuinely naive in the seams between them. The bounded-work machinery (D-085), the activation graph (D-104, D-111), the memo (D-094), the batch all-or-nothing rule (D-095) and the capability discovery (D-093) are each deliberate and each hold up under reading; several reviewer findings dissolved once I read the decision that already answered them. Where it is naive is in the reactions it chose for things that go wrong: a deliberate ValueSchema rotation is classified as corruption and then neither served, evicted nor re-loaded, so a routine deploy step hard-fails every cached key for up to seven days; a single WriteFailure knob forces the store-after-load and an explicit Put onto opposite horns, so Warm/Durable throw away a value the loader already produced while Hot's Put returns nil without storing; and the two capacity dials that decide admitted concurrency — the worst-case charge plan (with a flat 16 MiB JSON constant billed to String and Bytes codecs) and a cache-wide MaxFlights of 1 — mean the shipped profiles refuse the third concurrent reader and the second concurrent cold-start key, which I reproduced (24 of 64 Warm lookups and 17 of 30 Hot cold-start resolves returned ErrSaturated). cachememory's O(entries) purge on every write, under the one mutex every read also takes, is the same shape of assumption at the storage layer (1.04 ms/Put at 100k entries, measured). The conformance suite is the weakest artefact here: driver_cancellation delegates its whole check to the implementer, backend_limits returns silently rather than skipping when no Capacity probe is supplied, no test ever writes a CapacityOnlyExpiry, and Cache.Check is never exercised — four ways a second backend can be certified without proving what BackendDescription claimed. Two documentation sentences also overstate the code: cache.New is called equivalent to the declarative path when it reaches none of the graph-level refusals, and the Put/Forget fence is stated unconditionally when it is one process's generation counter.

### Findings

| ID | Sev | Verdict | Kind | Where | What |
|---|---|---|---|---|---|
| `G-CCH-01` | 🟠 high | CONFIRMED | naive-implementation | [lookup.go:170](cache/lookup.go#L170) | A schema/codec mismatch is treated as corruption: under RefuseCorrupt every Resolve fails forever, the loader never runs and nothing evicts the entry |
| `G-CCH-02` | 🟠 high | CONFIRMED | naive-contract | [resolve.go:646](cache/resolve.go#L646) | One WriteFailure knob governs both the store-after-load and an explicit Put, so Warm/Durable destroy a loaded value on a cache write error and Hot's Put reports success without storing |
| `G-CCH-03` | 🟠 high | CONFIRMED | naive-implementation | [transient.go:107](cache/transient.go#L107) | The transient charge plan bills worst-case MaxValueBytes plus a flat 16 MiB JSON constant per operation, so a profile-default cache admits about two concurrent reads and refuses the rest |
| `G-CCH-04` | 🟠 high | CONFIRMED | naive-implementation | [resolve.go:249](cache/resolve.go#L249) | MaxFlights caps loader groups across the whole cache, not per address, and Hot/Warm default it to 1, so a miss on an unrelated key is refused with ErrSaturated |
| `G-CCH-05` | 🟠 high | CONFIRMED | naive-implementation | [backend.go:269](cache/cachememory/backend.go#L269) | Every cachememory Put walks the entire entry list while holding the single mutex all reads need |
| `G-CCH-06` | 🟡 medium | CONFIRMED | false-promise | [cache.go:69](cache/cache.go#L69) | cache.New produces a fully usable cache that reaches none of the activation-graph checks, and both docs call it equivalent |
| `G-CCH-07` | 🟡 medium | CONFIRMED | false-promise | [resolve.go:655](cache/resolve.go#L655) | The Put/Forget fence is a process-local generation counter, while the module doc and the use case state it as an unconditional guarantee |
| `G-CCH-08` | 🟡 medium | CONFIRMED | naive-contract | [lookup.go:362](cache/lookup.go#L362) | A per-address failure in the non-BatchReader fallback throws away every address already read, converting one flaky key into a full-batch origin reload |
| `G-CCH-09` | 🟡 medium | CONFIRMED | vacuous-test | [suite.go:103](cache/cachetest/suite.go#L103) | The conformance suite never issues a CapacityOnlyExpiry, so half of Backend.Put's expiry contract is unproven on a backend the core trusts to evict |
| `G-CCH-10` | 🟡 medium | CONFIRMED | vacuous-test | [suite.go:167](cache/cachetest/suite.go#L167) | The driver_cancellation conformance case is a caller-supplied closure with no contract, so it records that the author was asked, not that the backend behaves |
| `G-CCH-11` | 🟡 medium | CONFIRMED | vacuous-test | [suite.go:199](cache/cachetest/suite.go#L199) | runBackendLimits silently returns when the harness supplies no Capacity, so a backend that declares CapacityBounded never proves it evicts — and the core trusts that flag to write entries with no expiry |
| `G-CCH-12` | 🟡 medium | CONFIRMED | missing-capability | [policy.go:290](cache/policy.go#L290) | The Option set omits freshness, retention, corruption and negative-caching-off, so a custom TTL or "never cache an absence" requires abandoning Auto/Define for New |
| `G-CCH-13` | ⚪ low | CONFIRMED | missing-capability | [control.go:201](cache/cachetest/control.go#L201) | Neither the conformance suite nor the Controller can reach HealthChecker, so a backend whose CheckBackend always returns nil is certified conformant |
| `G-CCH-14` | ⚪ low | CONFIRMED | false-promise | [capability.go:84](cache/capability.go#L84) | TagInvalidation is a built-in capability no out-of-package backend can implement: no tag reaches the write path and Namespace has no exported accessor |
| `G-CCH-15` | ⚪ low | CONFIRMED | missing-capability | [cachefx.go:54](cache/cachefx/cachefx.go#L54) | cachefx groups sets, providers and resources but takes exactly one cache.Observer, so two modules cannot both observe |

#### `G-CCH-01` — A schema/codec mismatch is treated as corruption: under RefuseCorrupt every Resolve fails forever, the loader never runs and nothing evicts the entry

**🟠 high** · **CONFIRMED** · `naive-implementation` · [cache/lookup.go:170](cache/lookup.go#L170) · `cacheCore.corruptRead / cacheCore.resolveAddress`

**Evidence**

cache/envelope.go:117 turns any envelope whose codec id or ValueSchema differs from the live declaration into ErrCorrupt: `if codecID != descriptor.id || ValueSchema(binary.BigEndian.Uint32(encoded[14:18])) != descriptor.schema`. cache/lookup.go:137-144 routes that to corruptRead, whose whole body (cache/lookup.go:170-176) observes an event and then `if this.policy.Corruption == CorruptAsMiss { return Result[V]{State: Miss}, nil }; return Result[V]{}, failure(string(operation), ErrCorrupt)` — there is no backendDelete on that path. cache/resolve.go:201-210 aborts before any loader: `cached, readErr := this.lookupAddressAdmitted(ctx, address)` ... `if readErr != nil { this.coord.mu.Unlock(); return Result[V]{}, readErr }`. RefuseCorrupt is the shipped default of Warm (cache/policy.go:204) and Durable (cache/policy.go:227). In the batch path one bad entry kills the whole call: cache/lookup.go:449. By contrast a KeyVersion bump is safe by construction because it is mixed into the address digest (cache/address.go:116-122) while ValueSchema is not.

**Why it is naive**

The envelope version fields model exactly one cause of a mismatch — a corrupt or foreign byte string — and the routine cause is the opposite: a deliberate, deployed rotation of the value schema. "Do not use this value" is implemented as "fail the caller" rather than "evict and recompute", and the rejected entry survives its own rejection for the full retention window.

**Failure scenario**

A team adds a field to ProductCard and bumps `cache.JSON[ProductCard](1)` to `(2)` on a Warm or Durable cache. Every address written by the previous build now decodes to ErrCorrupt. I ran it: two Cache instances over one backend, String(1) writes and String(2) reads, Warm policy — Resolve returned `cache: lookup: cached value is corrupt or incompatible` on all three attempts with loaderRuns=0, and the offending entry was still in the backend afterwards. The page is hard-down for every already-cached key until retention expires (45 min Warm, 7 days Durable); recovery through the public API is Forget per key or a NamespaceTemplate.Generation bump. A ResolveMany over 256 keys fails wholesale (cache/lookup.go:449) if any one of them is still on the old schema.

**Suggested shape of the fix**

Distinguish a schema/codec mismatch from structural damage in decodeEnvelopeAccounting and treat the former as a Miss unconditionally — it is by definition re-loadable. At minimum, delete the offending address before returning on the read-through path, and document that a ValueSchema bump requires a matching Generation bump.

**How it was verified**

Read all cited lines and reproduced end to end with a throwaway test in package cache (since removed): three consecutive Resolve calls returned ErrCorrupt with loaderRuns=0 and one entry still stored. Searched docs/ai/decisions for corrupt/Corrupt: only D-007, D-043 and D-094 hit, and D-094 mentions corruption only to say a memoized envelope that fails to decode is dropped before the corruption policy runs. No decision states what the corruption policy should do, and FL-025:81 says only "Corruption and backend failures follow the declared policy".

*Merged from reviewer findings: cache-core-contract-value-schema-rotation-is-corruption, cache-semantics-corrupt-entry-poisons-address-forever*

#### `G-CCH-02` — One WriteFailure knob governs both the store-after-load and an explicit Put, so Warm/Durable destroy a loaded value on a cache write error and Hot's Put reports success without storing

**🟠 high** · **CONFIRMED** · `naive-contract` · [cache/resolve.go:646](cache/resolve.go#L646) · `cacheCore.storeLoaded / cacheCore.commitMutationAs`

**Evidence**

After a successful loader run, cache/resolve.go:646-648 discards the value if the cache write fails: `if _, err := this.commitFlight(member, encoded, expiry); err != nil { return resultSnapshot{}, err }` — the snapshot carrying the payload is never built, so every waiter gets that error. commitFlight propagates unless the policy says otherwise (cache/resolve.go:694-697). The same single field decides Put's honesty (cache/mutation.go:135-137): `if this.policy.WriteFailure == Ignore { return nil }`, with nothing having deleted the old entry — beginMutation (cache/mutation.go:56-85) only bumps a process-local generation. The two shipped defaults sit on opposite horns: Hot uses Ignore (cache/policy.go:252), Warm and Durable use Propagate (cache/policy.go:202, :226). ResolveMany has the same shape via commitLoaded (cache/resolve_many.go:241-244). The exported Option set (cache/policy.go:290-414) contains nothing for WriteFailure, and `grep -rn WriteFailure docs/` outside docs/api/surface.md returns nothing.

**Why it is naive**

It treats "the cache failed to store" as one event with one correct reaction, when the two callers want opposite things. A reader wants the value it already paid the loader for even if the cache is down; a write-through writer must know the cache still holds the old value. There is no third setting and no way to return a loaded value together with a storage warning.

**Failure scenario**

I ran both horns. Durable + a backend whose Put returns an error: the loader ran and produced the value, and Resolve returned `{Value:"" State:0}` with `cache: resolve: cache backend operation failed` — so a Postgres/Redis capacity event becomes a full request outage while the origin is healthy, and `Stale: ServeOnLoaderError` does not rescue it because a commitFlight error is not a *loaderFailure. Hot + the same backend: `Put(ctx, "k", "new")` returned <nil> and the subsequent Lookup still returned the pre-update value as a Hit — the application logs a successful write-through and serves the stale row for the rest of the retention window with no error anywhere.

**Suggested shape of the fix**

Split the policy: a store-after-load failure must never fail a Resolve that has a value in hand (return Loaded and emit the failure as an event), while an explicit Put/Forget failure must always be reported. If one knob has to stay, add a `WriteFailure(FailurePolicy)` option and document the per-profile defaults in docs/modules/en/cache.md.

**How it was verified**

Read all cited lines and reproduced both horns with a throwaway test in package cache (since removed). Corrected one reviewer's framing: the Hot horn is not reachable with a Redis backend through the declarative path, because Hot's ProviderKind is MemoryProviderKind (cache/policy.go:182) and Auto selects the provider by kind — it is reachable via cache.New, ResolveMany, or a closed/rejecting process backend. The Warm/Durable horn needs no such caveat. Grepped docs/ai/decisions for WriteFailure/ReadFailure/InvalidateFailure/Propagate: no cache decision mentions them.

*Merged from reviewer findings: cache-core-contract-put-swallows-failed-write-by-default, cache-semantics-writefailure-conflates-store-after-load-with-explicit-put*

#### `G-CCH-03` — The transient charge plan bills worst-case MaxValueBytes plus a flat 16 MiB JSON constant per operation, so a profile-default cache admits about two concurrent reads and refuses the rest

**🟠 high** · **CONFIRMED** · `naive-implementation` · [cache/transient.go:107](cache/transient.go#L107) · `transientPlanFor / cacheCore.lookupStable`

**Evidence**

Every read takes a whole-operation lease before anything else (cache/lookup.go:45) and holds it across the backend round trip; resolve does the same at cache/resolve.go:190 and :303. The charge is the worst case the policy permits, not the value at hand: cache/transient.go:95-110 builds `lookup` from four copies of MaxValueBytes plus one envelope plus the flat `jsonSafeRuntimeBytes` (16 MiB, cache/codec.go:391), added unconditionally at cache/transient.go:107 even for Bytes/String codecs. Measured through the public API: Hot and Warm both give MaxTransientBytes=268435456 against lookupOperation=100675802 and resolve=201372294 — two concurrent lookups, one concurrent resolve; a 256-key batch charges 219470608, leaving less than one lookup. `Hot.With(MaxValueBytes(1<<10))` resets the budget to plan.minimum (cache/policy.go:329-333, :629) and derives 34154230 against lookupOperation=16794842 — still two, because the 16 MiB constant dominates.

**Why it is naive**

It assumes a budget that admits one worst-case operation is a budget for a cache — i.e. that concurrency is one. The charge is a static upper bound on MaxValueBytes rather than the bytes actually in hand, and the flat JSON runtime constant is billed to codecs that never touch JSON, so a cache of 5-byte strings is charged 96 MiB per lookup.

**Failure scenario**

A consumer declares `cache.Auto[K,V](cache.Warm)` over their own Postgres-backed cache.Backend, values a few hundred bytes. I ran 64 concurrent Lookup calls against a backend with 50 ms latency at Warm defaults: 40 hits and 24 `cache: lookup: cache loader capacity is occupied` (ErrSaturated) after the 1 s TransientSaturation wait. Separately, with Hot defaults, a single Lookup issued alongside an in-flight 256-key LookupMany returned ErrSaturated outright. Nothing in Describe reports the admitted concurrency, so the operator sees a 256 MiB budget and a cache that refuses the third caller.

**Suggested shape of the fix**

Charge the bytes actually in hand (encoded length, decoded charge) rather than MaxValueBytes for every operation, drop jsonSafeRuntimeBytes for codecs that are not JSON, and derive the default MaxTransientBytes from a declared concurrency target rather than from one operation's charge. Expose the admitted concurrent-operation count in PolicyDescription so the ceiling is visible at start-up.

**How it was verified**

Read D-085 in full. It sanctions a "conservative charge plan" and forbids describing transient bytes as a loader count, but its stated rationale is preventing unbounded multiplication of cache work; it nowhere sanctions a cap that refuses the second and third ordinary reader, and UC-024 §H-CACHE-01 claims "Ordinary defaults are complete". Corrected the reviewer's claim that the default budget is plan.minimum: Hot/Warm/Durable set MaxTransientBytes explicitly (256/256/512 MiB) and plan.minimum is only used when an option resets it (cache/policy.go:329-333) — the admitted-concurrency consequence is the same either way and I measured it. Numbers above are my own measurements, not the reviewer's.

*Merged from reviewer findings: cache-semantics-default-transient-budget-admits-two-concurrent-reads*

#### `G-CCH-04` — MaxFlights caps loader groups across the whole cache, not per address, and Hot/Warm default it to 1, so a miss on an unrelated key is refused with ErrSaturated

**🟠 high** · **CONFIRMED** · `naive-implementation` · [cache/resolve.go:249](cache/resolve.go#L249) · `cacheCore.resolveAddress`

**Evidence**

The counter is on the cache, not on the address: cache/cache.go:52 declares `activeFlights int` inside `coordination`, cache/resolve.go:544 increments it in registerFlightLocked, and every admission test is `this.coord.activeFlights < this.policy.MaxFlights` (cache/resolve.go:249, :339, :384, :484). hotDefaults sets `MaxFlights: 1` with `FlightSaturation: WaitBounded(250 * time.Millisecond)` (cache/policy.go:241-242) and Auto defaults to Hot (cache/declaration.go:82); Warm is also 1 (cache/policy.go:191). The stale escape hatch does not apply on those defaults either — cache/resolve.go:416 serves the stale value only when FlightSaturation.mode == ServeStaleFlight, which neither Hot nor Warm is.

**Why it is naive**

The cap is written as though a cache holds one hot key. MaxFlights is the mechanism that protects the origin from a stampede, which is a per-address concern, but it is enforced as a global concurrency limit of one loader for the entire cache, so unrelated keys contend for one slot and a cold cache turns a burst of distinct misses into a burst of errors.

**Failure scenario**

A Hot cache restarts or its store is flushed and 30 requests arrive for 30 different ids. I ran exactly that with a 20 ms loader: 20 loaded and 10 returned `cache: resolve: cache loader capacity is occupied`. I isolated the cause from the transient budget by re-running with `Hot.With(MaxTransientBytes(8<<30))` (MaxFlights still 1): 13 loaded, 17 saturated; with `MaxFlights(32)` added, all 30 loaded. The caller cannot distinguish that error from the origin being down, for a cache miss that should simply have loaded.

**Suggested shape of the fix**

Enforce one flight per address by construction (state.member already does this) and let MaxFlights bound concurrent distinct loads at a value sized for the workload, defaulting well above 1; or, at minimum, fall back to serving a usable stale value or running the load uncoalesced instead of returning ErrSaturated on flight saturation.

**How it was verified**

Read D-085: it says "MaxFlights remains a distinct logical cap on loader groups ... Profiles set both and expose both", which justifies having the cap but not its default of 1. docs/modules/en/cache.md:240-242 accurately describes the semantics ("MaxFlights is the number of loader groups"), so this is the default and the interaction with saturation, not a doc mismatch. cache/transient_test.go:3119-3162 pins the cross-key refusal under Reject(); I re-ran it under the shipped WaitBounded default myself.

*Merged from reviewer findings: cache-semantics-maxflights-is-cache-wide-and-defaults-to-one*

#### `G-CCH-05` — Every cachememory Put walks the entire entry list while holding the single mutex all reads need

**🟠 high** · **CONFIRMED** · `naive-implementation` · [cache/cachememory/backend.go:269](cache/cachememory/backend.go#L269) · `(*Backend).Put / purgeExpiredLocked`

**Evidence**

Put locks backend.mu at cachememory/backend.go:260 and does not release it until :299. Inside that window it calls purgeExpiredLocked (:269), whose body is an unconditional walk of every live entry (:518-532): `for item := backend.lru; item != nil; index++ { newer := item.newer; if expired(item, now) { ... }; item = newer }`. There is no early exit once enough room is free, and the list is LRU-ordered, not expiry-ordered. Get takes the same sync.Mutex at :183 — there is no RWMutex and no sharding. Entries written with CapacityOnlyExpiry have a zero expiresAt, so `expired` (:711-713) is never true for them and the scan removes nothing while still visiting every entry.

**Why it is naive**

It assumes the store is small enough that touching all of it per write is free, and that expiry cleanup must be exhaustive rather than sufficient. A cache's whole point is to be large; the Limits API invites a large MaxEntries, and the cost model is silently O(n) per write with n chosen by the caller.

**Failure scenario**

An application configures `cachememory.New(Limits{MaxEntries: 100_000, ...})` and serves a read-heavy endpoint whose misses refill the cache. I benchmarked Put against a store pre-filled to capacity with 1-byte values and no expired entries: 4.6 us/op at MaxEntries=1000, 42 us/op at 10000, 1.04 ms/op at 100000. Process-wide write throughput caps near 1000/s and every concurrent Lookup on any other key stalls up to a millisecond behind an unrelated write. docs/modules/en/cachememory.md uses MaxEntries: 10_000 as its worked example, so ~42 us of exclusive-lock time per write is the documented configuration.

**Suggested shape of the fix**

Stop purging exhaustively on the write path: purge only until the new entry fits (the eviction loop at :534+ already walks from the LRU end), and/or keep an expiry-ordered structure so the sweep is O(expired) rather than O(entries). If an exhaustive sweep is wanted, move it behind the cache.Maintainer capability, which is exactly the seam D-093 defines for a maintenance sweep.

**How it was verified**

Read all cited lines and ran my own benchmark in package cachememory (since removed) to get the numbers above; they match the reviewer's within noise. Searched docs/ai/decisions for purge/sweep/DeleteExpired and for files naming cachememory: only D-093, D-096 and D-114 mention the package and none discusses the sweep or the locking. TestPutPurgesExpiredBeforeEvictingLiveLRU (backend_test.go:318) pins the ordering, not the scan width.

*Merged from reviewer findings: cache-backend-and-harness-memory-put-scans-whole-store*

#### `G-CCH-06` — cache.New produces a fully usable cache that reaches none of the activation-graph checks, and both docs call it equivalent

**🟡 medium** · **CONFIRMED** · `false-promise` · [cache/cache.go:69](cache/cache.go#L69) · `New`

**Evidence**

New (cache/cache.go:69) -> newResolvedCache -> configure (cache/cache.go:134) validates only the single cache: backend description, clock skew, envelope vs MaxItemBytes, retention vs expiry support. It never sees a ResourceID; core.resourceID is assigned only in prepareActivation (cache/activation.go:308). Every graph-level refusal lives in Activate: the physical-namespace collision (cache/activation.go:112-116), the Requires check (cache/activation.go:269-273), and the eviction-domain refusal driven entirely by `cacheOwners[provider.resourceIdentity()]` populated at cache/activation.go:104-106 and consumed at :126 and :128. cache/resource.go:91-109 confirms the shape: with `Tenants: []ResourceTenant{DurableSecurityTenant}` and no cache activated on that resource, `activated` is false and `declaredCache` is false, so no problem is raised. The docs say the opposite: docs/modules/en/cache.md:354-356 — "New remains useful for libraries, tests and consumers with their own composition system. It does not weaken validation, policy or bounded-work semantics" — and docs/ai/usecases/modules/cache/Cache.md:31-33 — "The direct New constructor remains the equivalent opt-out from top-level declarations."

**Why it is naive**

The contract assumes every cache in a process is born through Activate. New is a first-class, documented public constructor producing an identical *Cache[K,V] with zero resource identity, so the safety rule D-104 exists to enforce is opt-in by which constructor a package happened to pick — and the reference documentation states there is no difference.

**Failure scenario**

A library or internal package builds its cache with `cache.New(runtime, redisBackend, ...)` over the same client the session revocation list uses. The composition root does everything D-104 asks: it declares `{Resource: "redis-main", Tenants: []ResourceTenant{DurableSecurityTenant}}` and sets RequireDeclaredResources: true. Activate inspects only its own Sets, so the New cache is in no cacheOwners entry, activation succeeds and reports no problem. Under allkeys-lru pressure the cache's writes evict revocation entries, Revoked reads absence as "not revoked", and signed-out sessions come back — in a deployment that turned the strictness on.

**Suggested shape of the fix**

At minimum, delete the "does not weaken validation" and "equivalent" claims and state exactly which checks New skips (physical-namespace collision, Requires, eviction domain, undeclared resource). Better: give New a required resource identity plus a process registry Activate consults, or refuse a SharedBackend topology from New.

**How it was verified**

Read D-104 in full. Its "Where it lives" section names only cache/resource.go, cache/activation.go and cache/provider.go, and its rationale is "Activate refuses the graph, which is the earliest moment the answer exists" — it never contemplates a cache that never reaches Activate, so its stated reasoning does not cover this. Traced evictionDomainProblems by hand to confirm a resource declared with only DurableSecurityTenant raises nothing when no declared cache resolved to it. Re-judged from the reviewer's high to medium: the provable defect is the false equivalence in two documents, and the auth consequence needs several independent consumer choices.

*Merged from reviewer findings: cache-core-contract-new-bypasses-eviction-domain*

#### `G-CCH-07` — The Put/Forget fence is a process-local generation counter, while the module doc and the use case state it as an unconditional guarantee

**🟡 medium** · **CONFIRMED** · `false-promise` · [cache/resolve.go:655](cache/resolve.go#L655) · `cacheCore.commitFlight`

**Evidence**

The whole fence is a comparison of in-memory state: cache/resolve.go:658 `allowed := state.member == member && state.generation == member.generation && !state.writeActive && !state.invalidating && !member.invalidated`, where state comes from this.coord.states (cache/cache.go:49-57, a per-cacheCore map) and Forget marks the loser through invalidateMemberLocked (cache/mutation.go:311-319). Nothing is sent to or read from the backend to establish ordering: the write is an unconditional backendPut (cache/resolve.go:685) and the delete an unconditional backendDelete (cache/mutation.go:272). Backend has no conditional write (cache/backend.go:34-38) and CompareAndSwapper is never called by the core. Meanwhile docs/modules/en/cache.md:182-184 states with no qualifier "Put and Forget are fenced against concurrent loads so an older loader cannot publish over a newer mutation", and UC-024 H-CACHE-03 says "An invalidated or overwritten generation cannot be restored by an earlier loader." The qualifier that does exist is attached to something else — docs/modules/en/cache.md:240 scopes only coalescing to one process. SharedBackend is an accepted topology today (cache/cache.go:161-164).

**Why it is naive**

The fence models a single process as the whole system. Generation counters live in one process's heap, so two replicas over one Redis have two independent, non-comparable fences, and the backend write that decides the outcome is unconditional.

**Failure scenario**

Two replicas share one Redis-backed cache.Backend. Replica A misses and starts a flight whose loader reads product:7 as v1. Before it returns, an admin write lands on replica B: B updates the row to v2 and calls Forget("product:7"), which deletes the key and returns nil. A's loader then returns, commitFlight finds A's own local generation untouched, and it writes v1 back with a fresh full retention. Both replicas serve v1 for the whole retention window (45 min Warm, 7 days Durable) with no further invalidation pending — the exact interleaving the documented fence says cannot happen.

**Suggested shape of the fix**

Qualify the guarantee in docs/modules/en/cache.md:182-184 and UC-024 H-CACHE-03 the way coalescing is already qualified, or make the flight's store conditional through the already-declared CompareAndSwapper when the backend has one, so a superseded write is refused by the backend rather than by a local counter.

**How it was verified**

Read all cited lines and D-095, which describes the same fence ("a superseded write is a no-op") without limiting it to one process and explicitly scopes only single-flight to one process. No decision states the fence is process-local. Re-judged from the reviewer's high to medium: no shared backend ships yet (docs/modules/en/cache.md "does not claim those backends yet") and UC-024's verdict line explicitly scopes to in-process caching, so a consumer must supply their own SharedBackend to reach it — which the public Backend interface fully permits.

*Merged from reviewer findings: cache-semantics-mutation-fence-is-process-local-but-documented-absolutely*

#### `G-CCH-08` — A per-address failure in the non-BatchReader fallback throws away every address already read, converting one flaky key into a full-batch origin reload

**🟡 medium** · **CONFIRMED** · `naive-contract` · [cache/lookup.go:362](cache/lookup.go#L362) · `cacheCore.batchGetBackend / batchReadFailure`

**Evidence**

The fallback reads addresses one at a time and abandons the whole round on the first failure — cache/lookup.go:362-381: `for _, address := range addresses { value, found, err := backendGet(...); if err != nil { return this.batchReadFailure(ctx, addresses, err) } ... encoded[address] = value }`. batchReadFailure (cache/lookup.go:406-426) then returns `make(map[Address][]byte)` under ReadFailure == AsMiss — discarding everything already accumulated in `encoded` — or fails the entire call under Propagate. The public shape offers nowhere to put the distinction: LookupMany returns ([]Result[V], error) and Result.State is one of Hit/Miss/Negative/Stale/Loaded (cache/result.go:9-17), so "the backend refused this one key" is indistinguishable from "this key is not cached".

**Why it is naive**

It assumes a batch read either wholly succeeds or wholly fails, but the fallback loop is literally N independent single reads whose per-item outcomes are already in hand and are then thrown away.

**Failure scenario**

A ResolveMany for 200 keys against a backend without BatchReadCapability: key 173 times out while keys 1-172 were already read as hits. Under Hot/Warm (ReadFailure: AsMiss) batchReadFailure returns an empty map, so all 200 keys are reported as misses and the batch loader recomputes all 200 from the origin — one flaky key multiplies origin load by 200x, the exact stampede the cache exists to prevent. Under Durable (Propagate) the same single key fails the whole LookupMany.

**Suggested shape of the fix**

Keep the entries the fallback loop already read and apply the failure only to the addresses that actually failed (they become misses under AsMiss). Longer term, give the batch API a way to report per-key backend failure so a caller can tell a genuine absence from a key the backend could not answer.

**How it was verified**

Read all cited lines. D-095 governs ResolveMany and demands "Do not return a partially filled result slice with an error", but that rule is about loader answers and the write phase ("nothing has been written by then"); it says nothing about discarding successful backend reads, and its own summary of the read phase is only "cache/lookup.go — the read phase it reuses unchanged". The BatchReader path's wholesale failure is inherent to the driver returning one error; only the fallback loop's discard is the defect. Note cachememory implements BatchReader, so reaching this needs a consumer-written backend that does not.

*Merged from reviewer findings: cache-semantics-batch-read-has-no-partial-failure-channel*

#### `G-CCH-09` — The conformance suite never issues a CapacityOnlyExpiry, so half of Backend.Put's expiry contract is unproven on a backend the core trusts to evict

**🟡 medium** · **CONFIRMED** · `vacuous-test` · [cache/cachetest/suite.go:103](cache/cachetest/suite.go#L103) · `cachetest.Run`

**Evidence**

cache.Expiry has two valid modes, RelativeExpiry and CapacityOnlyExpiry (cache/backend.go:22-26). Every Put the suite issues uses the first — suite.go:103, 138, 155, 177, 211, 385, 1407, 1425, 1460 all spell `cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: ...}` — and suitePolicy fixes `policy.Retention = cache.ExpireAfter(8 * time.Second)` (suite.go:1499). `grep -n CapacityOnly cache/cachetest/suite.go` returns nothing. But the core reaches the second mode from public policy: cache.CapacityBoundedRetention() (cache/policy.go:35) sets Retention.capacityOnly, physicalExpiry then returns `Expiry{Mode: CapacityOnlyExpiry}` (cache/envelope.go:212-214) and expiryForWrite passes it straight through (cache/envelope.go:219-221). cache/cache.go:173-176 permits that policy on any backend that is ProcessBackend and CapacityBounded — exactly the class this suite certifies. cachememory pins the behaviour in its own file (TestCapacityOnlyDoesNotPhysicallyExpire, backend_test.go:307), outside the shared suite and therefore inherited by nobody.

**Why it is naive**

The suite treats expiry as one thing with a TTL. The real contract has two modes, and the mode that carries no TTL is the one a backend is most likely to mishandle, because most stores need something written into the key's lifetime field.

**Failure scenario**

Someone adds a second process backend — a bounded freecache/ristretto wrapper — declaring CapacityBounded: true. cachetest.Run passes every subtest. Its Put maps Expiry by reading expiry.RetainFor, so a CapacityOnlyExpiry write (RetainFor == 0) is stored with a zero TTL, which in that store means expire immediately. A consumer who selects `policy.Retention = cache.CapacityBoundedRetention()` — legal, and validated at build time by cache/cache.go:173-176 — gets a 100% miss rate and a loader call on every request, while the conformance suite that exists to catch this reports green.

**Suggested shape of the fix**

Add a backend_capacity_expiry subtest that runs when description.CapacityBounded: Put with `Expiry{Mode: cache.CapacityOnlyExpiry}`, advance the clock past any TTL the suite otherwise uses, and assert the value is still there and is evicted only by capacity pressure. Pair it with a typed-facade case built on cache.CapacityBoundedRetention().

**How it was verified**

Confirmed the grep returns nothing and read every Put site in suite.go. Read D-104 (about resource identity, silent on expiry modes) and D-093. UC-024's edge-case list says "capacity-only retention is allowed only where the backend supports it" — an assertion about activation-time gating, which is exactly the claim the suite leaves unproven, not a sanction for omitting it.

*Merged from reviewer findings: cache-backend-and-harness-suite-skips-capacity-only-expiry*

#### `G-CCH-10` — The driver_cancellation conformance case is a caller-supplied closure with no contract, so it records that the author was asked, not that the backend behaves

**🟡 medium** · **CONFIRMED** · `vacuous-test` · [cache/cachetest/suite.go:167](cache/cachetest/suite.go#L167) · `runDriverCancellation`

**Evidence**

The entire subtest body is `func runDriverCancellation(t *testing.T, harness Harness) { harness.VerifyCancellation(t) }` (suite.go:167-169), registered as a named conformance case at suite.go:48. openHarness checks only that the field is non-nil (`harness.VerifyCancellation == nil` at suite.go:73) and never that it does anything. The suite itself owns only the pre-cancelled-context case in runBackendCancellation (suite.go:151-165), which every implementation gets right by accident because the first thing most of them do is ctx.Err(). What actually matters — cancellation landing after the call has entered the driver, and the requirement that such a call leaves no mutation behind — is entirely in the harness. cachememory's real implementation is 45 lines that reach into the backend's private mutex (cache/cachememory/conformance_test.go:41-44 wiring, :62-126 body) and assert the cancelled Put did not store and the cancelled Delete did not remove; none of that is stated as a requirement anywhere the next backend author will read.

**Why it is naive**

A conformance harness whose subtest is a caller-supplied closure with no contract is a hole shaped like a test.

**Failure scenario**

A backend author writes `VerifyCancellation: func(*testing.T) {}`, or a two-line version that only re-checks a pre-cancelled context, which is what the name suggests. `go test` prints `--- PASS: TestConformance/driver_cancellation`. Their Put ignores cancellation once inside the driver and writes anyway, so a request cancelled by a client disconnect still publishes a half-computed value under the key and the next reader gets it — with the suite that exists to prevent an inconsistent backend reporting no problem.

**Suggested shape of the fix**

Move the real check into the suite: give Harness a hook that blocks the backend mid-operation (Pause in control.go is already this primitive) and have the suite assert context.Canceled plus "no mutation landed" itself. Failing that, require the harness to report which operations it covered and fail if Get/Put/Delete are not among them.

**How it was verified**

Read suite.go:41-73 and :151-169 and cachememory/conformance_test.go. Searched docs/ai/decisions for cancellation entries touching cache: D-084 covers shared-flight contexts (a facade concern) and D-085 transient budgets; neither sanctions delegating a conformance case to the implementer. FL-025:245 lists cancellation among what "backend_test.go and its conformance test" cover, which is true only because cachememory happens to have written a real one.

*Merged from reviewer findings: cache-backend-and-harness-suite-driver-cancellation-is-delegated*

#### `G-CCH-11` — runBackendLimits silently returns when the harness supplies no Capacity, so a backend that declares CapacityBounded never proves it evicts — and the core trusts that flag to write entries with no expiry

**🟡 medium** · **CONFIRMED** · `vacuous-test` · [cache/cachetest/suite.go:199](cache/cachetest/suite.go#L199) · `runBackendLimits / openHarness`

**Evidence**

The subtest has two probes and both are conditional. The oversized-item probe runs only `if description.MaxItemBytes <= maximumItemProbeBytes` (suite.go:200-206, with maximumItemProbeBytes = 8<<20 at :37). The capacity probes (the entry loop at :212-234 and the byte-pressure pair at :240-254) run only if harness.Capacity != nil; otherwise suite.go:207-209 is a bare `return`, not a t.Skip, so nothing in the output says the check did not run. openHarness validates harness.Capacity when supplied (suite.go:80-84) but never demands one when the backend declared CapacityBounded: true. That declaration is load-bearing: cache/cache.go:173-176 refuses CapacityBoundedRetention() unless the backend says CapacityBounded, i.e. the core will write entries with no expiry at all purely because the backend claimed to evict. Compare runBackendBatch, which at least calls `t.Skip("backend has no batch capability")` (suite.go:174).

**Why it is naive**

The harness treats its two most important limits — the item ceiling and the capacity ceiling — as optional extras the implementer may decline, rather than as the consequences of two fields the implementer already declared in BackendDescription.

**Failure scenario**

A shared-Redis backend is added declaring MaxItemBytes: 512<<20 (above the 8 MiB probe threshold, so the oversized probe is skipped) and CapacityBounded: true (because the server runs allkeys-lru), and its harness leaves Capacity nil because sizing a deterministic eviction probe against a live server is awkward. TestConformance/backend_limits executes zero assertions and prints PASS. The backend in fact returns a driver error rather than cache.ErrTooLarge for an oversized value, and its eviction is the server's. A consumer then selects cache.CapacityBoundedRetention(), which cache/cache.go:173-176 gates solely on that flag, and every entry is written with no TTL on the strength of a claim nothing tested.

**Suggested shape of the fix**

Make the two description fields imply their tests: require harness.Capacity in openHarness whenever description.CapacityBounded is true, with an explicit opt-out field that names why so declining is visible, and replace the silent returns with t.Skip carrying a reason so a skipped conformance probe shows up in the output instead of reading as a pass.

**How it was verified**

Read suite.go:37, :80-84, :199-254 and cache/cache.go:173-176. Searched docs/ai/decisions for CapacityBounded: no hits; D-104 is about which resource a cache may live on, not about proving the capacity claim. UC-024 H-CACHE-06 asserts cachememory's limits and eviction are "covered by its backend conformance suite", which holds only because cachememory's own harness supplies a Capacity (conformance_test.go:37-40).

*Merged from reviewer findings: cache-backend-and-harness-suite-backend-limits-can-be-a-no-op*

#### `G-CCH-12` — The Option set omits freshness, retention, corruption and negative-caching-off, so a custom TTL or "never cache an absence" requires abandoning Auto/Define for New

**🟡 medium** · **CONFIRMED** · `missing-capability` · [cache/policy.go:290](cache/policy.go#L290) · `Option`

**Evidence**

`type Option interface{ apply(*Policy) error }` (cache/policy.go:290) has an unexported method, so a consumer cannot write one. The eight shipped options are MaxValueBytes, MaxFlights, MaxTransientBytes, MaxTransientWaiters, TransientSaturation, FlightSaturation, StaleBehavior and NegativeFor (cache/policy.go:316-414) — nothing for Freshness, Retention, Jitter, MaxKeyBytes, MaxValueDepth, ReadFailure, WriteFailure, InvalidateFailure, Corruption or LastWaiter. NegativeFor refuses a non-positive duration (cache/policy.go:407-410), so NoNegativeCaching() is unreachable from a profile. Profile has only unexported fields and no exported constructor (cache/policy.go:174-179; docs/api/surface.md lists no `func …() Profile`), and Auto accepts only a Profile (cache/declaration.go:78-97). DefinitionSpec has no Policy field (cache/declaration.go:43-51). The only escape is New, which takes a raw Policy — and skips every graph-level check (see cache-new-bypasses-the-graph-level-refusals). Warm ships `Negative: CacheAbsenceFor(2 * time.Minute)` (cache/policy.go:186) and Durable five minutes (:209).

**Why it is naive**

The profile abstraction assumes three operational shapes cover an application. Freshness is the single most application-specific parameter a cache has, and how long a newly created or newly permitted thing stays invisible is a correctness decision, not an operational tier.

**Failure scenario**

A permission-lookup cache wants Warm's retention on a shared backend but must never cache an absence, because a newly granted permission has to take effect immediately. `cache.Warm.With(...)` offers no option that reaches NoNegativeCaching(), so the absence is stored for two minutes on every miss and a freshly granted permission is denied for two minutes. Expressing it requires cache.New with a hand-built Policy, which drops the cache out of Set/Activate and therefore out of the eviction-domain refusal, the physical-namespace collision check and the Requires check. The same wall stops anything wanting a 60-second TTL.

**Suggested shape of the fix**

Add Freshness, Retention, Negative, Jitter and Corruption options (plus the three failure policies), or let Auto/Define accept a validated Policy directly, so the escape from the three profiles is not the constructor that skips activation.

**How it was verified**

Read cache/policy.go:174-179, :290-414 and cache/declaration.go:43-97 and confirmed Policy's exported fields make New fully configurable while Auto/Define are not. No decision in docs/ai/decisions governs the profile or option set; D-104 and D-111 govern activation, D-085 the transient budget. Re-scoped the reviewer's claim: the gap is real but the workaround (New with a hand-built Policy) does produce the identical policy — only the graph checks are lost, which is why this is medium rather than high.

*Merged from reviewer findings: cache-core-contract-declarative-path-cannot-shape-policy*

#### `G-CCH-13` — Neither the conformance suite nor the Controller can reach HealthChecker, so a backend whose CheckBackend always returns nil is certified conformant

**⚪ low** · **CONFIRMED** · `missing-capability` · [cache/cachetest/control.go:201](cache/cachetest/control.go#L201) · `cachetest.Controller / controlledBackend.Next`

**Evidence**

cachetest.Operation names four operations and no more (control.go:19-24) and validOperation makes FailNext/PauseNext refuse anything else. controlledBackend wraps those four (control.go:208-250) and exposes `Next() cache.Backend` returning the raw backend (control.go:201-206). cache.CapabilityOf walks that chain (capability.go:205-219), so HealthCheckerOf(controller.Backend()) resolves straight past the controller — and Cache.Check uses exactly that lookup (cache/health.go:22). Only BatchReader is preserved deliberately, by controlledBatchBackend (control.go:92-94, :241-250). On top of that, `grep -n 'Check\|Health' cache/cachetest/suite.go` returns nothing: no subtest ever calls Cache.Check, even though docs/modules/en/cachememory.md presents CheckBackend as a shipped capability so that "Cache.Check has something to consult".

**Why it is naive**

The harness models the backend as three byte-moving methods, but the real cache.Backend is those three plus six optional capabilities the core discovers by walking past decorators — so a decorator built to intercept the seam intercepts only the part of the seam it happened to name.

**Failure scenario**

A backend implements `func (b *B) CheckBackend(context.Context) error { return nil }` and never notices its own connection is gone. It passes cachetest.Run completely, because no subtest calls Check. A consumer wires that cache's Check into their readiness endpoint (UC-024 H-CACHE-09 is exactly this use), the store dies, the probe stays green and the instance stays in rotation. A consumer writing their own test with the public cachetest.MustController cannot make that probe fail either — there is no health Operation to name, and Cache.Check would reach the raw backend through Next() anyway.

**Suggested shape of the fix**

Add a backend_health subtest that runs when cache.HealthCheckerOf(harness.Backend) succeeds: assert Check is nil while open and reports a failure once the harness's Close has run. Give the Controller an operation for each built-in capability it forwards, or have it implement the capability interfaces so CapabilityOf stops at the controller.

**How it was verified**

Read control.go:19-24, :92-94, :201-250, capability.go:201-219, health.go:22, and confirmed the grep over suite.go returns nothing. D-093 sanctions the traversal ("a typed lookup that walks decorators through Next()") and its rationale is about a driver being able to have capabilities the core can find; it says nothing about a decorator built to intercept the seam, and no decision sanctions the suite never exercising a shipped built-in capability.

*Merged from reviewer findings: cache-backend-and-harness-harness-cannot-reach-health-or-the-other-capabilities*

#### `G-CCH-14` — TagInvalidation is a built-in capability no out-of-package backend can implement: no tag reaches the write path and Namespace has no exported accessor

**⚪ low** · **CONFIRMED** · `false-promise` · [cache/capability.go:84](cache/capability.go#L84) · `TagInvalidator`

**Evidence**

`type TagInvalidator interface { InvalidateTag(context.Context, Namespace, Tag) error }` (cache/capability.go:84-86), with TagInvalidationCapability (:22), a probe (:145-146) and a validated Tag value type (:64-82). The write path has no tag channel: `Backend.Put(context.Context, Address, []byte, Expiry) error` (cache/backend.go:36) and its only caller backendPut pass address, opaque envelope bytes and expiry. A repo-wide grep shows Tag appears in no cache file other than capability.go and its test. And the Namespace handed to InvalidateTag is opaque outside the package: its fields are unexported (cache/address.go:19-25) and its only exported method is `func (this Namespace) String() string { return "[cache namespace]" }` (cache/address.go:65), so a backend cannot derive the NamespaceDigest it actually stored in Address (cache/address.go:86-90, :124).

**Why it is naive**

The capability was modelled on Redis-style tag indexes without checking that the neutral contract can carry a tag from the caller to the store. Both halves are missing: nothing associates a tag with an entry at write time, and the invalidation argument is a value the callee cannot read.

**Failure scenario**

A Redis adapter written as its own module (required by D-033) implements InvalidateTag to satisfy TagInvalidationCapability. It receives (ctx, Namespace, Tag), has never been told any entry's tags — Put gave it only an Address — and cannot translate the Namespace into the NamespaceDigest values it holds. The only implementation it can write returns nil or errors. Meanwhile Supports(backend, TagInvalidationCapability) returns true purely because the method exists, and a `DefinitionSpec{Requires: []Capability{TagInvalidationCapability}}` passes activation (cache/activation.go:269-273) on a backend that cannot do the thing.

**Suggested shape of the fix**

Either remove TagInvalidator/TagInvalidationCapability/Tag from the built-in set until the write path can carry tags, or complete the contract: add a tag list to the write and give Namespace an exported Digest() [32]byte so an out-of-package backend can correlate the two.

**How it was verified**

Read capability.go, backend.go:34-38, address.go:19-90 and confirmed by grep that Tag reaches nothing else in the cache tree. D-093 is the governing decision and it deliberately includes TagInvalidator among the six ("a driver with ... a tag index had no way to say so"), so the interface existing is decided — but D-093 says nothing about whether a built-in interface is implementable at all, which is the specific gap here. Kept at low: nothing in a running deployment produces a wrong answer.

*Merged from reviewer findings: cache-core-contract-taginvalidator-cannot-be-implemented*

#### `G-CCH-15` — cachefx groups sets, providers and resources but takes exactly one cache.Observer, so two modules cannot both observe

**⚪ low** · **CONFIRMED** · `missing-capability` · [cache/cachefx/cachefx.go:54](cache/cachefx/cachefx.go#L54) · `cachefx.Contributions.Observer`

**Evidence**

Contributions carries three fx groups and one singleton: `Sets []cache.Set` (cachefx.go:48), `Providers []cache.Provider` (:50), `Resources []cache.ResourceDeclaration` (:52), then ``Observer cache.Observer `optional:"true"``` (:54). There is AsSet, AsProvider and AsResource (:22-32) but no AsObserver, and activation takes the single value verbatim: `if runtime.Observer == nil { runtime.Observer = contributed.Observer }` (:139-141). The core ships a fan-out built for exactly this — cache.Observers(children ...Observer) composing up to MaxObservers with each panic isolated (cache/observers.go) — and the repository ships a first-party observer a graph would naturally provide, `otel.Cache(t) cache.Observer` (otel/cache.go:23).

**Why it is naive**

It assumes a deployment has one interested party. Observation is the canonical cross-cutting concern — telemetry, an audit sink and an application's own metrics are three independent modules — which is why the same file uses groups for the other three contributions.

**Failure scenario**

A service wires `fx.Provide(vvotel.Cache)` for tracing and, from a different module, `fx.Provide(newCacheAuditObserver)` for its own counters. fx fails the graph at build with a duplicate cache.Observer provide. The only fix is a root-level constructor that imports both modules and calls cache.MustObservers(a, b) — reintroducing exactly the central knowledge of every contributing module that AsSet/AsProvider/AsResource exist to avoid.

**Suggested shape of the fix**

Add AsObserver(constructor) and a `vv.cache.observers` group, and have activation compose the collected children with cache.Observers(...), returning its error when more than MaxObservers are contributed, keeping the existing single binding as the zero-or-one shorthand.

**How it was verified**

Read cachefx.go in full and confirmed otel/cache.go:23 returns a cache.Observer. Read D-111, which is the decision behind the group pattern in this file and argues for contribution over central knowledge — it supports rather than refutes this. Searched docs/ai/decisions for observer: nothing constrains the fx binding to one. docs/modules/en/cache.md:116 documents the singleton shape, so this is a capability gap, not a false promise — hence low.

*Merged from reviewer findings: cache-backend-and-harness-cachefx-has-no-observer-group*

### Refuted in cache

<details><summary><b>CompareAndSwapper and Transactional are built-in capabilities no code path can reach</b></summary>

The factual claims hold — I confirmed by grep that CompareAndSwapperOf and TransactionalOf are called nowhere outside capability.go and its test, and that only BatchReaderOf (cache/lookup.go:338) and HealthCheckerOf (cache/health.go:22) are used by the core. But D-093 deliberately sanctions this: its "The decision" section states that before the change the other capabilities "were unimplementable: a driver with a compare-and-swap, a maintenance sweep or a tag index had no way to say so, and Definition.Requires could demand exactly one thing", and it then names all six interfaces including CompareAndSwapper and Transactional. The interfaces existing so a driver can advertise them and Requires can demand them is the decided design, not an oversight. The concrete production consequence the reviewer describes (last-writer-wins across replicas) is reported under cache-mutation-fence-is-process-local-but-documented-absolutely, so keeping this too would be the same defect twice.

</details>

<details><summary><b>The conformance suite never issues concurrent backend calls it asserts on</b></summary>

Partly refuted by reading the suite. It is true that the four backend-direct subtests (runBackendValues :100, runBackendExpiry :135, runBackendBatch :171, runBackendLimits :199) are single-goroutine and that the dedicated hammering test lives in cache/cachememory/race_test.go, outside the shared suite. But the claim that the suite "never issues concurrent backend calls" does not hold: runTransientAccounting (suite.go:456-530) starts one goroutine per transient holder, each paused in Controller.before (control.go:251+) — i.e. before the underlying call — and the deferred `pause.Release()` then lets them all enter the real backend concurrently, under -race. Several other subtests (mutation fences at :1295-1341, singleflight, waiter cancellation) likewise drive concurrent goroutines that reach the backend. A dedicated concurrency case with an explicit invariant assertion is genuinely absent, but that is a coverage wish rather than a demonstrated hole, and it overlaps the two conformance findings already kept.

</details>

---

## auth

<a id="auth"></a>

### Verdict

The auth core is genuinely well-built where it was thought about: the guard's marker chain, nil-like handling, option-draft copying, `LookupOrRefuse` and the cardinality refusal are all deliberate, pinned by non-vacuous tests, and backed by decisions whose rationale actually covers what the code does. Six of the seven reviewer findings survived verification, but the shape of what survived is telling — five of the six are the module's *edges* rather than its centre: the published-versus-used API split (`Authenticate` vs `AuthenticateValues`, where every shipped binding is safe and every document shows the unsafe form), the boot gate's route identity being coarser than net/http's (host stripped in `routeOf`), the JWKS flight donating one waiter's context values in flat contradiction of the sibling D-084, the ungated observer fan-out that three other packages in this repo isolate, and two consumer-facing pages that describe a gRPC stream as if it behaved like a unary call. That is the recurring pattern: the invariant is real and implemented on the path the library itself walks, and it quietly stops holding one step outside it — in a consumer's own binding, on a host-scoped pattern, in a stream, in a callback. Deliberately minimal, and soundly so, are the things reviewers were most tempted by: `Credential` as two plain strings, the absence of a `Reauthenticate` option, and `Audience` meaning all-of — each considered, each either decided (D-055, D-076) or pinned by a test that would fail if it changed. The real systemic defect here is documentation drift with security weight: `docs/modules/en/auth.md`, FL-019 and `docs/modules/en/authgrpc.md` each state a guarantee the code delivers only on a path they do not show.

### Findings

| ID | Sev | Verdict | Kind | Where | What |
|---|---|---|---|---|---|
| `G-AUT-01` | 🟠 high | CONFIRMED | false-promise | [surface.go:58](auth/http/authnet/surface.go#L58) | authnet.Surface strips the host from a ServeMux pattern, so a host-scoped route inherits another host's access declaration |
| `G-AUT-02` | 🟠 high | CONFIRMED | false-promise | [guard.go:104](auth/guard.go#L104) | Guard.Authenticate, the only documented entry point, cannot see a duplicated credential, so D-099's refusal does not hold on the documented path |
| `G-AUT-03` | 🟡 medium | CONFIRMED | false-promise | [guard.go:131](auth/guard.go#L131) | The mid-stream re-check authgrpc.md tells a long-lived stream to write is a permanent no-op through the guard it holds |
| `G-AUT-04` | 🟡 medium | CONFIRMED | naive-implementation | [jwks.go:293](auth/authjwt/jwks.go#L293) | The shared JWKS fetch and its degraded observer are seeded with the first missing request's context values |
| `G-AUT-05` | 🟡 medium | CONFIRMED | false-promise | [authgrpc.md:57](docs/modules/en/authgrpc.md#L57) | The only gRPC stream wiring authgrpc.md shows answers Unknown for an authentication refusal |
| `G-AUT-06` | 🟡 medium | CONFIRMED | naive-implementation | [refusal.go:76](auth/refusal.go#L76) | Guard.refuse runs consumer Observers inline with no recover, against the repo's own panic-isolated fan-out standard |
| `G-AUT-07` | ⚪ low | CONFIRMED | naive-contract | [parser.go:83](auth/authjwt/parser.go#L83) | authjwt.Audience means all-of and the consumer-facing page never says so |

#### `G-AUT-01` — authnet.Surface strips the host from a ServeMux pattern, so a host-scoped route inherits another host's access declaration

**🟠 high** · **CONFIRMED** · `false-promise` · [auth/http/authnet/surface.go:58](auth/http/authnet/surface.go#L58) · `routeOf`

**Evidence**

`auth/http/authnet/surface.go:52-63` is exactly as quoted: after cutting the method, `if slash := strings.Index(pattern, "/"); slash > 0 { pattern = pattern[slash:] }` discards everything before the first `/`, which for a Go 1.22 ServeMux pattern is the host. `authhttp.Route` (`auth/http/authhttp/surface.go:46-49`) carries only `Method` and `Path`; `key` (`surface.go:239-245`) builds `strings.ToUpper(method) + " " + path`; `Area.disagreements` (`surface.go:143-150`) records mounted routes into a `seen` set and only reports a route whose key is not declared, so a second route on the same key is silently absorbed. I ran the real code: `GET public.example.com/reports` + `GET admin.example.com/reports` record as `[{GET /reports} {GET /reports}]` and `Verify` with the single declaration `authhttp.Public("GET", "/reports", ...)` returned nil; `/` + `admin.example.com/` record as `[{* /} {* /}]` and one `Public("*", "/", ...)` verifies clean.

**Why it is naive**

The recorder identifies a route by method and path; a ServeMux route is identified by host, method and path, and the host is precisely the axis used to split an admin surface from a public one. The gate's identity function is coarser than the router's, so two different routes become one key.

**Failure scenario**

A consumer serves a public site and an admin console off one `Surface`: `s.HandleFunc("GET public.example.com/reports", publicQuarterly)` exists and is declared `authhttp.Public(http.MethodGet, "/reports", "the public quarterly report")`. Someone later adds `s.HandleFunc("GET admin.example.com/reports", everyRowInTheTenant)` and writes no declaration. `Surface.Verify` returns nil, the process starts, and the admin route serves having never been declared or reviewed — the outcome D-073 exists to make impossible. `admin.example.com/` is worse: the whole admin host records as `* /` and is covered by the landing page's declaration.

**Suggested shape of the fix**

Carry the host on `authhttp.Route`/`Endpoint` (empty meaning any host) and include it in `key`, or make `routeOf` refuse a pattern carrying a host the way `authnet` already refuses what it cannot see rather than absorbing it silently. Either way pin it with a test in `auth/http/authnet/surface_test.go` and record the limit in D-073, which today names only two.

**How it was verified**

Opened the file and reproduced the collapse with a scratch test against the real `Surface` and `Verify` (output: `routes=[{GET /reports} {GET /reports}]`, `VERIFY PASSED with only one declaration`; scratch file removed). Searched D-073, `docs/modules/en/authnet.md` and `auth/http/authnet/surface_test.go` for `host` — zero hits, so nothing sanctions or pins this. D-073 names exactly two accepted limits (a route registered past the Surface, Fiber's hand-written HEAD) and hosts are not among them. Note the gate is a boot-time declaration check, not a per-request enforcement point, so this is an escape from the review mechanism rather than a runtime authz bypass — which is why I rate it high and not critical.

#### `G-AUT-02` — Guard.Authenticate, the only documented entry point, cannot see a duplicated credential, so D-099's refusal does not hold on the documented path

**🟠 high** · **CONFIRMED** · `false-promise` · [auth/guard.go:104](auth/guard.go#L104) · `(*Guard).Authenticate`

**Evidence**

`auth/guard.go:104-115` collapses the source to at most one value before the guard can count it — the `get` result is wrapped as `[]string{value}` or nil. The cardinality refusals live one level down in `(*Guard).credential` (`auth/guard.go:173` `if len(raw) > 1`, `auth/guard.go:198` `if cardinality > 1`) and are unreachable from this method. All four shipped bindings call the list form (`auth/http/authnet/authnet.go:19`, `authgin/authgin.go:17`, `authfiber/authfiber.go:19`, `rpc/authgrpc/interceptor.go:35` and `:49`), but `grep -rn AuthenticateValues docs/` is empty — it appears in no document at all — while `docs/modules/en/auth.md:160` shows `ctx, err := guard.Authenticate(r.Context(), r.Header.Get)` as the guard's only worked example and `:175` repeats "`Authenticate` takes a `func(name string) string`". FL-019 step 2 (`docs/ai/flows/FL-019-a-token-becomes-a-principal.md:12-13`) names `Guard.Authenticate` and says it "is handed a `func(name string) string`", and its Failure modes section (`:198-202`) states unconditionally that one source carrying two values is "a 401 wrapping `ErrCredentialCardinality`, and the authenticator is not reached". I ran both entry points against one `http.Header`: `Authenticate` → `err=<nil> calls=1 token="first"`; `AuthenticateValues` → `ErrCredentialCardinality`. Through `authhttp.Cookie("session")` with two `Cookie` headers: `Authenticate` → `err=<nil> token="attacker"`; `AuthenticateValues` → refused.

**Why it is naive**

The seam models a request header as a single string. HTTP headers are lists, and duplicates are what proxy chains, header injection and request smuggling produce. The library knows this — it built `AuthenticateValues` and D-099 for it — but left the single-value form as the published, documented API and never wrote the safe one down.

**Failure scenario**

A consumer mounts this library's auth on a framework it ships no binding for (echo, chi, httprouter) by copying `docs/modules/en/auth.md:160`. A request arrives with two `Authorization` headers — an edge proxy that appends rather than replaces, or an attacker probing a gateway/origin inconsistency. The guard authenticates the first and never raises `ErrCredentialCardinality`. With `authhttp.Cookie` the D-099 case breaks verbatim: `Cookie: session=attacker` followed by `Cookie: session=victim` authenticates as the attacker's session, and "never from two cookies of that name" does not hold.

**Suggested shape of the fix**

Make the list-aware form the documented one: change the `auth.md` example and FL-019 step 2 to `AuthenticateValues`, add it to `docs/modules/en/auth.md`'s guard table, and either deprecate `Authenticate` or have it refuse — a `func(name) string` cannot report multiplicity, so it should be named for what it is. At minimum, D-099 and FL-019 must stop stating the refusal unconditionally.

**How it was verified**

Read the method, `credential`, all four bindings and the cardinality test, then reproduced both arms with a scratch test (removed afterwards). D-099 states the invariant with no qualification and cites `TestAListAwareGuardRefusesEveryDuplicateCredential`, which calls only `AuthenticateValues` (`auth/guard_cardinality_test.go:49,:70,:79`) — so nothing pins the documented path. D-045/D-055 justify the header-getter seam over an `http.Request`, but `AuthenticateValues` proves multiplicity is expressible without a transport type, so that rationale does not cover the loss. Reachability caveat I could not remove: no shipped binding is affected, so this bites only a consumer writing their own transport from the docs — that is why I keep it at high rather than critical. Also note `docs/api/surface.md` lists no methods at all for any type, so the reviewer's "absent from surface.md" point is not evidence.

#### `G-AUT-03` — The mid-stream re-check authgrpc.md tells a long-lived stream to write is a permanent no-op through the guard it holds

**🟡 medium** · **CONFIRMED** · `false-promise` · [auth/guard.go:131](auth/guard.go#L131) · `(*Guard).authenticate`

**Evidence**

`auth/guard.go:129-134`: `if mark := authenticationMark(ctx, this); mark != nil { if mark == latestAuthenticationMark(ctx) && mark.principal == principalStateFrom(ctx) { return ctx, nil } }`. The short-circuit keys on guard identity and principal-state identity only; it never re-reads the credential and never reaches `this.authn.Authenticate` (`auth/guard.go:156`). `auth/rpc/authgrpc/interceptor.go:49-53` authenticates once and hands the handler `&authenticated{ServerStream: serverStream, ctx: ctx}` whose `Context()` (`interceptor.go:100`) returns that marked context, so every later guard call on it hits the short-circuit. `docs/modules/en/authgrpc.md:107-109` states the limit and then names the remedy: "A credential that expires mid-stream is not noticed... A long-lived stream that must re-check does it in its own loop." I ran that loop with the guard the interceptor was built from, revoking the credential after the mark was installed: `re-check 0/1/2: err=<nil> authenticatorCalls=1` for both `AuthenticateValues` and `Authenticate`, and `PrincipalFrom` still returns `{streamer ...}`. `auth/principal.go` exposes no expiry or validity, so nothing the handler can read reports the credential died.

**Why it is naive**

The idempotence rule models a repeated `Authenticate` as accidental double-mounting of middleware. It does not model deliberate re-verification on a connection that outlives its credential — the one shape gRPC streams (and SSE/websocket bridges) make routine, and the shape the module page explicitly hands back to the consumer.

**Failure scenario**

A service exposes a bidirectional stream open for hours. Following `authgrpc.md:109` the handler re-checks every minute with the `*auth.Guard` it was constructed with: `if _, err := guard.AuthenticateValues(stream.Context(), md.Get); err != nil { return err }`. An operator revokes the API key or the JWT expires. Every re-check returns nil without calling the authenticator, the stream keeps full authority until the client closes it, nothing is logged or observed, and the consumer believes they implemented the documented remedy.

**Suggested shape of the fix**

Correct `docs/modules/en/authgrpc.md:109` (both language pages) to say the loop must call the `Authenticator` directly, or re-run the guard on a context that carries no marker — re-running it on `stream.Context()` is a no-op. D-076 forecloses the other obvious fix, so the doc is the thing that has to change.

**How it was verified**

Read the short-circuit and reproduced it with a scratch test against the real guard (removed afterwards). D-076 is the governing decision and its entire stated rationale is about composition of stacked guards — "a principal is not evidence of the check a route declared", "pointer identity does not define assurance order" — and its forbidden list says "Do not add an opt-in reauthentication option as the primary fix". That rationale does not cover re-verifying one guard on a long-lived connection, so the finding survives, but D-076 does rule out the reviewer's first suggested remedy; only the documentation fix is open. Downgraded from high to medium: the failure requires the consumer to write the loop, and the no-loop baseline is honestly documented as a limit.

#### `G-AUT-04` — The shared JWKS fetch and its degraded observer are seeded with the first missing request's context values

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [auth/authjwt/jwks.go:293](auth/authjwt/jwks.go#L293) · `(*jwks).refresh / runFetch / queueDegraded`

**Evidence**

`auth/authjwt/jwks.go:293` is `go this.runFetch(context.WithoutCancel(ctx), flight)`, where `ctx` is whichever request context reached `keyFor` → `refresh` first (`jwks.go:188` calls `this.refresh(ctx)` with the parse context). `context.WithoutCancel` strips cancellation and keeps every value. That seed is used for the outbound request — `jwks.go:310` `fctx, cancel := context.WithTimeout(seed, JWKSFetchTimeout)` → `:391` `http.NewRequestWithContext(ctx, ...)` with the consumer's `*http.Client` from `JWKSClient` — and for the degraded callback: `:342` `this.queueDegraded(seed, *degraded)`, `:350` `this.pending = &degradedNotice{ctx: ctx, ...}`, `:372` `context.WithTimeout(notice.ctx, time.Second)`, `:374` `this.observeDegraded(observerCtx, ...)`. `auth/authjwt/jwks_test.go` makes no assertion about the fetch or observer context values; its only context assertions are cancellation ones (`:519`, `:582`) and the re-entry test at `:879` only re-parses through the passed ctx.

**Why it is naive**

It models a shared cache refill as belonging to the request that discovered the miss. The work outlives that request and is consumed by every other waiter, so an arbitrary caller's tenant binding, logger, trace span and any body/transaction graph are handed to a callback and a RoundTripper that run after that request has returned.

**Failure scenario**

A multi-tenant deployment on this repo's own tenancy + otel stack. A request bound to tenant A misses the JWKS cache; the fetch is seeded from tenant A's request context and the request returns 300ms later. The provider is down, so the detached fetch runs the full `JWKSFetchTimeout`; ten seconds after that request ended, `runFetch` invokes the consumer's `JWKSDegradedObserver` with a context derived from it — an observer reading `tenancy.From(ctx)` or `port.Logger(ctx)` attributes a provider-wide outage to tenant A while tenants B and C are the ones being refused, and an `otelhttp` transport supplied through `JWKSClient` parents the shared fetch span under a span that ended 9.7s earlier. `this.pending` also holds that request's whole context chain alive until the drain loop runs.

**Suggested shape of the fix**

Seed the flight from `context.Background()` plus the framework-owned `JWKSFetchTimeout`, exactly as `cache/resolve.go` does under D-084, and give the degraded observer a value-free context too. If a deployment needs a logger or tracer on the fetch, take it as an explicit `JWKS…` option rather than donating one waiter's request scope.

**How it was verified**

Opened every cited line; the code says what the reviewer says. D-078 sanctions the detachment but its stated rationale is entirely about cancellation lifetime ("Initiators and waiters each stop waiting on their own context without cancelling the shared fetch") and says nothing about values — the exception clause applies. D-084 decides the identical question for `cache` and forbids the pattern in terms ("`context.WithoutCancel(firstCaller)` is therefore not a safe base: it strips cancellation while retaining every value"; "Do not base a shared loader on the first caller with `WithoutCancel`"), but its invariant is scoped to cache flights, so it is precedent rather than a binding rule over `authjwt`. I rated this medium, not high: no principal is installed in the context at key-fetch time (authentication is still in flight), so concrete harm needs a value-reading `JWKSClient` transport or observer — both documented seams, but not every deployment fills them.

#### `G-AUT-05` — The only gRPC stream wiring authgrpc.md shows answers Unknown for an authentication refusal

**🟡 medium** · **CONFIRMED** · `false-promise` · [docs/modules/en/authgrpc.md:57](docs/modules/en/authgrpc.md#L57) · `authgrpc.Stream wiring snippet`

**Evidence**

`docs/modules/en/authgrpc.md:56-57` is the page's only mount snippet: `grpc.ChainUnaryInterceptor(crudgrpc.Errors(), authgrpc.Unary(guard))` then `grpc.ChainStreamInterceptor(authgrpc.Stream(guard))` — the unary chain carries a renderer, the stream chain carries none. `auth/rpc/authgrpc/interceptor.go:49-51` returns the bare fault (`return err`), and `grep -rn GRPCStatus --include=*.go .` over the whole repository is empty, so `errs.Fault` implements no `GRPCStatus()` and grpc-go answers `codes.Unknown`. The renderer that fixes it exists and is named nowhere on the page: `crud/rpc/crudgrpc/interceptor.go:27` `func StreamErrors(options ...RenderOption) grpc.StreamServerInterceptor`, whose body mirrors `Errors` (`:10`). Meanwhile `authgrpc.md:80-81` promises the opposite: "`crudgrpc.Errors` renders it, and `errs.KindUnauthorized` already maps to `UNAUTHENTICATED`." The stream test cannot catch it — `auth/rpc/authgrpc/interceptor_test.go:280-291` asserts only `errors.Is(err, auth.ErrUnauthenticated)`, which holds while the wire code is Unknown. `docs/modules/ru/authgrpc.md:57` carries the same snippet.

**Why it is naive**

The page treats the stream path as a mirror of the unary path when the two need different interceptors, and the test pins the in-process classification rather than what a gRPC client can observe.

**Failure scenario**

A consumer copies the four-step snippet verbatim and mounts a streaming method behind `authgrpc.Stream(guard)`. A client opens the stream with an expired token; the client receives `rpc error: code = Unknown desc = errs: unauthorized: unauthenticated` instead of `UNAUTHENTICATED`, so its standard "refresh the token and retry on UNAUTHENTICATED" branch never fires. Every unary method on the same server answers correctly, which makes it read as a stream-specific application bug.

**Suggested shape of the fix**

Put `crudgrpc.StreamErrors()` first in `ChainStreamInterceptor` in both language pages, name it in the page's "What is different" section and in FL-019's binding table, and change the refusal arm of `TestAStreamIsAuthenticatedWhenItOpens` to assert `status.Code` through a chain that includes the renderer, so the wire answer is what is pinned.

**How it was verified**

Read the snippet, both interceptors, `crudgrpc.Errors`/`StreamErrors` and the stream test. Confirmed no `GRPCStatus` anywhere in the repo, so `status.FromError` on a bare fault fails and grpc-go falls back to Unknown. FL-019's binding table repeats the same claim with the same gap. No decision covers stream error rendering; D-008 and D-056 are about the 404/403 ordering and the refusal's shape. It is recorded as an open item in `docs/ai/usecases/Release-readiness.md` and two module sweeps — recorded, not decided, and still shipped on the consumer-facing page, which is what makes it a live false promise rather than a known-and-sanctioned gap.

#### `G-AUT-06` — Guard.refuse runs consumer Observers inline with no recover, against the repo's own panic-isolated fan-out standard

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [auth/refusal.go:76](auth/refusal.go#L76) · `(*Guard).refuse`

**Evidence**

`auth/refusal.go:72-80`: `for _, observer := range this.observers { observer.Refused(ctx, Reason{...}) }` — no `recover`, no bound, no derived context, and the loop is the fan-out for every `auth.Observe(...)` registered. Two comparisons in this repository: `auth/authjwt/jwks.go:370-375` calls the *other* auth observer as `func() { defer func() { _ = recover() }(); observerCtx, stop := context.WithTimeout(notice.ctx, time.Second); defer stop(); this.observeDegraded(observerCtx, notice.state) }()`, and D-078 records that as deliberate ("Observer calls are deadline-bound, panic-safe, reentrant-safe"). `cache/observers.go:35-44` runs each child through `observeIsolated` with `defer func() { _ = recover() }()`, and `jobs/worker_observers.go:40` does the same. `grep -rn "recover()" auth/ port/ app/` outside tests hits only `jwks.go:371`. `docs/modules/en/auth.md:261-262` documents half the hazard — "The observer runs on the request's own goroutine, so an implementation that blocks blocks the refusal" — and says nothing about a panic. `auth/observer_test.go` exercises only a well-behaved recording observer and `Sampled`.

**Why it is naive**

The seam treats a consumer-supplied callback as trusted, always-returning code on the path whose rate an unauthenticated attacker controls. The same repository already concluded an observer is none of those things — three times, in three packages — and left this one bare.

**Failure scenario**

A consumer registers `auth.Observe(auth.Sampled(100, metrics))` as `auth.md:249` shows, where `metrics.Refused` writes into a map a config reload replaced with nil, or calls a slog handler that panics on a nil `Reason.Err`. Under the credential-stuffing run `Sampled` is documented for, every hundredth 401 panics out of the middleware. On net/http, `http.Server` recovers and closes the connection with no response, killing authenticated traffic on that keep-alive connection; on the gRPC binding grpc-go does not recover handler panics at all, so the process dies. With two observers registered, the first panicking one also prevents the second from ever seeing a refusal — the exact failure D-096 names ("it lets one child's panic swallow the ones after it").

**Suggested shape of the fix**

Isolate each observer call the way `cache/observers.go:41` and `jwks.go:371` already do — `defer func() { _ = recover() }()` per child, so later observers still run and the refusal still returns — and state in `auth.md` that a panicking observer is contained, next to the existing note that a blocking one blocks.

**How it was verified**

Opened `refuse`, the JWKS drain, `cache/observers.go` and `jobs/worker_observers.go`. D-056 governs the refusal's shape, not the notification; D-062 explains why the seam exists, not its containment; FL-019 step 15 records the goroutine choice and no panic policy while recording panic-safety for the JWKS observer in the same flow. D-096's invariant ("a bounded, ordered, panic-isolated observer fan-out") is scoped to `cache` and `jobs`, so it is precedent rather than binding on `auth` — but it is the repository stating explicitly that an unisolated fan-out is wrong, and no decision sanctions the asymmetry. Kept at medium: it needs a buggy consumer observer to fire. One correction to the reviewer — on Gin and Fiber the panic reaches whatever recovery middleware is mounted; the unrecovered case that actually kills a process is gRPC, not the HTTP bindings.

#### `G-AUT-07` — authjwt.Audience means all-of and the consumer-facing page never says so

**⚪ low** · **CONFIRMED** · `naive-contract` · [auth/authjwt/parser.go:83](auth/authjwt/parser.go#L83) · `Audience / New`

**Evidence**

`auth/authjwt/parser.go:34-36` appends to `s.audience`; `:82-84` `if len(s.audience) > 0 { po = append(po, jwt.WithAllAudiences(s.audience...)) }`. `golang-jwt/jwt/v5@v5.3.1/parser_option.go:89-99` documents `WithAllAudiences` as requiring all named audiences to be present. The only other reach is `AllowAnyAudience()` (`parser.go:46-48`), which disables the check, and `New` panics without one or the other (`:68-70`). `docs/modules/en/authjwt.md:22` lists `Audience` under "what is checked" and `:39` shows a single audience; nowhere on the page does the word all-of, both-of or any-of appear. RFC 7519 §4.1.3 makes `aud` a set the recipient must find itself in, so the default reading of `Audience("a", "b")` is any-of.

**Why it is naive**

It models a service as having exactly one name. Real deployments carry two names at once during an audience rename, behind a gateway alias, or when one binary serves two logical APIs — and the contract can express "all of these" and "nothing at all", but not the thing the RFC describes.

**Failure scenario**

A team renames its audience from `articles-api` to `https://articles.example.com` and, following the module page, adds the new name: `authjwt.Audience("articles-api", "https://articles.example.com")`. Every already-minted token carries only `articles-api`, so `WithAllAudiences` refuses all of them and the service 401s until every outstanding token expires. The one-line escape the API offers is `AllowAnyAudience()`, which the page itself calls making the token "replayable against every other service that trusts the same issuer".

**Suggested shape of the fix**

Cheapest fix is one sentence on `docs/modules/en/authjwt.md` saying `Audience(a, b)` requires both and naming the two-parser `Chain` as the rename path. The capability fix is `AnyAudienceOf(...)` (and its `AnyIssuerOf` twin) satisfying `New`'s start-up check and validating after parse — which is already written down as open blocker 12.

**How it was verified**

Read the option, the parser construction, the vendored `WithAllAudiences` and the test. `auth/authjwt/parser_test.go:264-303` (`TestNamingTwoAudiencesRequiresBothOfThem`) pins the all-of semantics deliberately and is not vacuous, so the *behaviour* is intended; what survives is that the consumer-facing page never states it, and `docs/ai/usecases/modules/auth/Auth.md:740` records blocker 12 ("`Audience` means all-of") as an open sharp edge rather than a decision. I must correct the reviewer on one point that changes the severity: `authjwt.Claims` does carry `aud`. `claims.go:19-38` copies every raw claim into `Extra`, and `parser.go:134-146` decodes the full `MapClaims` into `C`, so `Extra["aud"]` is present and `Authenticator(New[Claims](k, AllowAnyAudience()), mapper)` is a two-line any-of workaround; only `Standard`'s fixed mapper lacks a hook. With a real workaround and a loud fail-closed failure, this is low, not medium. D-078 governs authjwt trust bounds and says nothing about audience cardinality, so nothing sanctions it either.

### Refuted in auth

<details><summary><b>Credential.Token is a bare exported string while the repository requires a self-redacting value type for a database password</b></summary>

No library path leaks the token, so the failure is only reachable through consumer-authored code. I read every place the token travels: `auth/credential.go:11-15` and `:33-43` never render it; `auth/apikey/apikey.go:78-92` puts only the scheme into an error (`auth.Unauthenticatedf("credential is not %s", this.scheme)`) and the token into `store.Lookup` as a lookup argument; `auth/refusal.go:72-80` forwards only `Kind`, a `Detail` written in-package and the authenticator's own error. D-081, the contrary standard cited, states its invariant as "An ordinary rendering of a database *configuration* never reveals authentication material" and its mechanism is `Config.Password`/`Config.DSN` in `utils/vvdb` — a boot-time config tree that is routinely printed whole, which a per-request `Credential` is not. The nearest auth-side hazard was considered and answered deliberately: `docs/modules/en/apikey.md:126-130` makes the scheme check the default precisely so "a store that logs misses" is not handed every non-key token. What is left is a hardening preference: the scenario needs the consumer to write `fmt.Errorf("keys.Find(%q): %w", key, err)` over a value they must handle as a plain string to query with anyway. No decision, doc or test promises the token is protected, and nothing in `auth` breaks.

</details>

---

## access (auth/access)

<a id="access"></a>

### Verdict

Where this module is genuinely naive is at its edges rather than its core: the identifier rule is applied on three of the four paths that touch the column, the permission catalogue it seeds is five-eighths decorative, and its two most security-relevant defaults (`revokeOthers` off, no attempt limiter on the change-password verification) are the unsafe ones with no deployment override. The rotation design is the sharpest case — a two-digest lineage is a reuse detector an attacker evades by rotating twice, and the branch that would have caught it turns out to be tested by nothing at all: I deleted the entire theft response and `go test ./...` stayed green, as did the cross-site handler assertion in two of the three HTTP bindings after I deleted their `Protect` call. Everywhere else the minimalism is deliberate and sound and I confirmed it as such: D-066 really does refuse identity ownership on stated grounds, D-089 really does bound sign-in cost at a seam, D-098 and D-112 really do reason their way to the shapes they chose, and the accessnet control test is exactly what a non-vacuous version of its two siblings looks like. Nothing in this batch was refuted outright, which is unusual, but three findings shrank materially under checking — the bind-budget failure needs 65k sessions rather than 999 because access is Postgres/MySQL only, the `IdleTTL` complaint survives only in its negative-clamp half, and the login-CSRF finding runs straight into a categorical prohibition in D-102 whose rationale half-covers it. The most valuable single output here is not a bug but a measurement: three decision documents (D-102, D-098, D-112) each cite tests under "Proven by" that do not prove what they claim.

### Findings

| ID | Sev | Verdict | Kind | Where | What |
|---|---|---|---|---|---|
| `G-ACC-01` | 🟠 high | CONFIRMED | false-promise | [usecase.set-password.go:34](auth/access/usecase.set-password.go#L34) | SetPassword writes the directory's raw identifier into credentials.identifier, so Normalize is applied to the lookup but not to this write |
| `G-ACC-02` | 🟠 high | CONFIRMED | naive-contract | [accessjwt.go:290](auth/access/accessjwt/accessjwt.go#L290) | Refresh-credential reuse is detectable only one rotation back; an older stolen credential is a plain 401 that closes nothing |
| `G-ACC-03` | 🟡 medium | CONFIRMED | vacuous-test | [accessfiber_test.go:192](auth/access/http/accessfiber/accessfiber_test.go#L192) | The cross-site handler assertion in accessfiber and accessgin passes with the Protect call deleted — proved by deleting it |
| `G-ACC-04` | 🟡 medium | CONFIRMED | false-promise | [grant.usecases.go:24](auth/access/grant.usecases.go#L24) | Five of the eight permissions access declares and seeds are enforced nowhere, and its own administrative use cases check none |
| `G-ACC-05` | 🟡 medium | CONFIRMED | naive-contract | [usecase.change-password.go:58](auth/access/usecase.change-password.go#L58) | A password change leaves every other session live unless the request body asks otherwise, and no configuration can change that |
| `G-ACC-06` | 🟡 medium | CONFIRMED | naive-implementation | [usecase.change-password.go:38](auth/access/usecase.change-password.go#L38) | The current-password check in change-password is unlimited, unobserved, and shares the sign-in path's hashing bulkhead |
| `G-ACC-07` | 🟡 medium | CONFIRMED | naive-implementation | [usecase.logout-all.go:91](auth/access/usecase.logout-all.go#L91) | Every session-closing path names all of a subject's live sessions in one IN list and is refused past the dialect's bind budget |
| `G-ACC-08` | 🟡 medium | CONFIRMED | vacuous-test | [accessjwt.go:277](auth/access/accessjwt/accessjwt.go#L277) | Nothing anywhere tests that a replayed credential closes the session or reaches the deny-list — proved by deleting the whole arm |
| `G-ACC-09` | 🟡 medium | CONFIRMED | false-promise | [eviction.go:73](auth/access/accessjwt/revokeredis/eviction.go#L73) | The eviction check asks one node while revocations are written across all of them, on a client type whose purpose is to be multi-node |
| `G-ACC-10` | 🟡 medium | CONFIRMED | naive-implementation | [accessfx.go:100](auth/access/accessfx/accessfx.go#L100) | The start-up grant sync is a check-then-insert, so replicas starting together fail each other's OnStart hook |
| `G-ACC-11` | 🟡 medium | CONFIRMED | leaky-abstraction | [accessnet.go:243](auth/access/http/accessnet/accessnet.go#L243) | Attempt.IP is whatever a binding puts in it, and the shipped net/http binding puts host:port there |
| `G-ACC-12` | 🟡 medium | CONFIRMED | naive-implementation | [access.grants.go:74](auth/access/access.grants.go#L74) | GrantsService.For discards every Directory.Describe failure and returns a principal with an empty profile |
| `G-ACC-13` | 🟡 medium | CONFIRMED | naive-implementation | [accessjwt.go:124](auth/access/accessjwt/accessjwt.go#L124) | A batch revocation abandons the rest of the batch on the first Redis error, while the log claims the whole group failed |
| `G-ACC-14` | ⚪ low | CONFIRMED | missing-capability | [access.secret.go:29](auth/access/access.secret.go#L29) | The Hasher seam has no way to say a stored hash needs re-deriving, and no sign-in ever upgrades one |
| `G-ACC-15` | ⚪ low | CONFIRMED | false-promise | [FL-023-a-sign-in-becomes-a-session.md:137](docs/ai/flows/FL-023-a-sign-in-becomes-a-session.md#L137) | FL-023 says a sign-up opens its session after the commit; the code opens it inside the transaction, and a test pins that |
| `G-ACC-16` | ⚪ low | PLAUSIBLE | false-promise | [access.config.go:56](auth/access/access.config.go#L56) | Config.Sessions() clamps a negative IdleTTL to the default and lets a zero disable the idle deadline, so DefaultIdleTTL is reachable only from a wrong value |
| `G-ACC-17` | ⚪ low | PLAUSIBLE | missing-capability | [crosssite.go:64](auth/access/http/accesshttp/crosssite.go#L64) | The cross-site check exits before it looks at the origin, so the one handler that installs a cookie is the one it can never reach |

#### `G-ACC-01` — SetPassword writes the directory's raw identifier into credentials.identifier, so Normalize is applied to the lookup but not to this write

**🟠 high** · **CONFIRMED** · `false-promise` · [auth/access/usecase.set-password.go:34](auth/access/usecase.set-password.go#L34) · `SetPasswordUseCase.Execute`

**Evidence**

`identifier := profile.Identifier` (usecase.set-password.go:34) is taken straight from `directory.Describe` (:30) and written verbatim on both branches — `SaveOnly(&Credential{... Identifier: identifier ...})` (:58) and `Update(..., CredentialUpdate{Identifier: &identifier, SecretHash: &hash})` (:63-66). The folding function is a method on `Subject` (`func (this Subject) Identifier(raw string) string`, access.subject.go:17-22) and its only two call sites are the read side and sign-up: `Endpoints.SignIn` → `this.subject.Identifier(body.Email)` (access.endpoints.go:67) and `SignUpUseCase.Execute` → `this.subject.Identifier(identifier)` (usecase.signup.go:54). `SetPasswordUseCase` is built from `Runtime.deps()` → `newDeps(store, grants, hasher, config, logger, revocations, protection)` (access.runtime.go:76-81) — a `*Deps` that carries no `Subject`, so it cannot reach `Normalize` even in principle. The lookup is an exact match on a byte-for-byte TEXT column: `Credential_.Identifier.Eq(identifier)` (access.repo.go:90) against `"identifier" TEXT NOT NULL` (migrations/00001_access.sql:120).

**Why it is naive**

It assumes the identifier the consumer's own account row reports and the identifier the credential column is keyed on are the same string. They are the same only if the consumer folds before storing its own row — and the module's own documented `Registrar` stores the raw form (`&User{Email: form.Email, ...}`) while returning `form.Email` for the library to fold (docs/modules/en/access.md:178-186), so divergence is the documented default rather than the exception.

**Failure scenario**

A deployment mounts `SubjectSpec{Normalize: strings.ToLower}` (the shape at docs/modules/en/access.md:45). An account registered through sign-up holds credential identifier "ann@example.com" while its profile e-mail is "Ann@Example.com". An administrator calls `runtime.SetPassword()` — the documented reset path — and the credential's identifier is rewritten to "Ann@Example.com". Every later sign-in folds to "ann@example.com", `Store.CredentialFor` finds nothing, and the account answers `bad_credentials` forever. The reset reported success and closed every session on the way out (usecase.set-password.go:72-84), so nobody is left signed in to notice.

**Suggested shape of the fix**

Give the password-setting path the subject it acts on (a `Subject`-bound `SetPassword` off `MountedSubject`, or a `Normalize` on `Deps`) and fold `profile.Identifier` before writing it. If folding inside the library is unacceptable, stop writing `Identifier` from `SetPassword` at all and correct docs/modules/en/access.md:165-166 and UC-023 must-hold 4.

**How it was verified**

Read all four files at the cited lines; the code says exactly this. Searched docs/ai/decisions/ for the subject: D-066 §'No identifier normalisation' says the column is written and read as supplied and the *consumer* applies the rule on both sides — but its stated rationale is explicitly that "the consumer already has to be trusted with both sides. It supplies the identifier to `EnrollUseCase` and to `LoginUseCase`" (D-066:58-61). `SetPasswordUseCase` gives the consumer no side: the string is read out of `Directory.Describe` inside the library. D-066 therefore does not cover this. Two documents state the opposite of the code: docs/modules/en/access.md:165-166 ("`SubjectSpec.Normalize` is applied by the library on both sides of the column") and UC-023 must-hold 4 ("it is applied to both the write and the lookup — never to one of them"). I dropped the reviewer's parallel claim about `EnrollUseCase.Execute` (usecase.enroll.go:49 writes `cmd.Identifier` unfolded): there the consumer supplies the string and D-066's rationale does reach it. Downgraded from critical to high — the failure is a silent lockout and a corrupted credential row, not an authentication bypass.

#### `G-ACC-02` — Refresh-credential reuse is detectable only one rotation back; an older stolen credential is a plain 401 that closes nothing

**🟠 high** · **CONFIRMED** · `naive-contract` · [auth/access/accessjwt/accessjwt.go:290](auth/access/accessjwt/accessjwt.go#L290) · `core.find / core.Refresh`

**Evidence**

`core.find` looks the presented digest up in exactly two columns — `crud.Where(crud.Eq("TokenHash", digest))` then `crud.Where(crud.Eq("PreviousTokenHash", digest))` — and returns `(nil, nil)` when neither matches (accessjwt.go:290-306). `Refresh` then takes `if session == nil { return access.AuthResponse{}, refused() }` (accessjwt.go:270-272) and never reaches `Classify`, so the theft arm at accessjwt.go:277-284 (`close(... access.ReasonRefreshReplayed)`, the WARN, the deny-list write) is unreachable for anything older than one generation. `core.swap` overwrites `PreviousTokenHash` with the current digest on every rotation (accessjwt.go:373-387), so generation N-2 is destroyed by rotation N. The row carries only the two digests (model.go).

**Why it is naive**

It models theft as "the attacker and the victim take turns, exactly once". An attacker holding a stolen refresh credential rotates on its own schedule; each rotation pushes the victim's copy one generation further back, and after the second one the victim's credential matches no column at all. Staying ahead of detection costs the attacker one request per `AccessTTL`.

**Failure scenario**

AccessTTL=5m, RefreshGrace=10s, RefreshTTL defaults to the session TTL of 720h. An attacker exfiltrates refresh credential T. It rotates T→T1 (row: current=D1, previous=D). Four minutes later, before the victim's client refreshes, it rotates T1→T2 (row: current=D2, previous=D1 — D is gone). The victim's client presents T: `find` misses both columns, `Refresh` answers `refused()`, nothing is closed, no `ReasonRefreshReplayed` is written, no deny-list entry is made and no WARN is logged. The victim sees an ordinary sign-out and signs in again; the attacker keeps the original lineage rotating for up to 30 days. UC-023 must-hold 15 — "a credential replayed after it was spent closes the session it belonged to" — is false for exactly this case.

**Suggested shape of the fix**

Put a monotonic rotation counter on the session row, mint the generation into the refresh credential, and classify any presented credential whose generation is below the row's current one as `Replay` rather than letting it fall off the end of a two-column lookup. This needs no second lifecycle, which is the only thing docs/modules/en/accessjwt.md:207-210 actually argues against.

**How it was verified**

Read `find`, `Refresh`, `swap` and `Classify` (rotation.go:49-67, whose `case presented.Previous == "" || presented.Digest != presented.Previous: return Unusable` says the same thing in the pure function). Searched decisions: D-098 governs the answer-then-swap order and the lost-swap re-read and says nothing about a credential more than one generation old; D-072 covers announcing closures, not detecting them. docs/modules/en/accessjwt.md:207-210 argues only against a *separate family id* that could outlive its session — that rationale rules out a second lifecycle, not a counter on the row it already has. No decision sanctions a two-digest window. Kept the reviewer's severity: a documented theft-detection guarantee is false, and the stolen lineage survives for the full RefreshTTL.

#### `G-ACC-03` — The cross-site handler assertion in accessfiber and accessgin passes with the Protect call deleted — proved by deleting it

**🟡 medium** · **CONFIRMED** · `vacuous-test` · [auth/access/http/accessfiber/accessfiber_test.go:192](auth/access/http/accessfiber/accessfiber_test.go#L192) · `TestACookieBorneWriteFromAnotherSiteIsRefusedByThisTransport`

**Evidence**

Both tests build `handler := &Handler{jar: jar}` — a zero `access.Endpoints`. accessfiber asserts only `if err := answer(crossSiteWrite(), handler.SignOut); err == nil { t.Fatal(...) }` (accessfiber_test.go:192-194); accessgin asserts only `if !refused.IsAborted() || len(refused.Errors) == 0` (accessgin_test.go:167-169). With `jar.protect` gone, control reaches `this.endpoints.SignOut(...)` → `Endpoints.principal` → `RequirePrincipal` → `auth.Unauthenticated("no principal in context")` (access.api.go:131-137), which is a non-nil error for Fiber and `c.Error(err); c.Abort()` for Gin (accessgin.go:215-218). I deleted the three-line `protect` block from `accessfiber.Handler.SignOut` and from `accessgin.Handler.SignOut` and re-ran the test in each: both printed `ok`. The same deletion in `accessnet.Handler.SignOut` fails, because accessnet asserts the status: `signing out answered 401 for a request made from another site; the handler never asks` (accessnet_test.go:158-161).

**Why it is naive**

The assertion distinguishes "an error happened" from "no error happened", but every path through the handler under test produces an error, so it cannot distinguish the cross-site refusal from the unauthenticated one. What is being pinned is a 403 with `CodeCrossSite`, and neither binding looks at the kind or the code.

**Failure scenario**

A refactor drops `jar.protect` from `accessfiber.Handler.SignOut` (or `SignOutAll`, `ChangeSecret`, `KillSession`, `Refresh`, `Register`, none of which have a handler assertion in any binding). `make unit` stays green and `make check-triplets` stays green, because it compares test *names* and the names still match. A cookie-authenticated deployment on Fiber or Gin then serves cross-site sign-outs and password changes with the only test that claimed to cover it still passing.

**Suggested shape of the fix**

Assert the fault rather than its nil-ness: `errs.AsFault(err)` with `fault.Code == accesshttp.CodeCrossSite` and `fault.Kind == errs.KindForbidden` in accessfiber, and the same over `refused.Errors[0]` in accessgin — matching what accessnet already gets from its 403 check.

**How it was verified**

Verified empirically, not by reading: three edit-run-restore cycles, fiber ok, gin ok, net FAIL. Working tree restored and `git status` is clean under auth/. This also falsifies a claim in a binding decision: D-102 §Proven by says `TestACookieBorneWriteFromAnotherSiteIsRefusedByThisTransport` is "carried file-for-file by `accessnet`, `accessgin` and `accessfiber`, each ... asserting the handler refuses rather than the policy value alone" — true only of `accessnet`. CLAUDE.md's own rule about control cases is the standard the two miss.

#### `G-ACC-04` — Five of the eight permissions access declares and seeds are enforced nowhere, and its own administrative use cases check none

**🟡 medium** · **CONFIRMED** · `false-promise` · [auth/access/grant.usecases.go:24](auth/access/grant.usecases.go#L24) · `GrantService.GrantRole / SetPasswordUseCase.Execute`

**Evidence**

`OwnGrants()` declares eight permissions and `Sync` seeds them all, including `{Code: PermGrantWrite, Name: "Grant and revoke roles and permissions"}` and `{Code: PermCredentialWrite, Name: "Set another account's password"}` (access.seed.go:129-143); `rolePlan` gives every declared permission to `admin` (access.seed.go:68-83). A repository-wide grep for `PermGrantRead`, `PermGrantWrite`, `PermSessionRead`, `PermSessionKill` and `PermCredentialWrite` outside tests returns only their declaration (access.api.go:46-51) and their seeding — no other use. Only the three role permissions are enforced, through `security.Gate(RolePolicy())` in `NewRoleService` (role.usecases.go:16-22, 61) and `PermissionPolicy()` (role.usecases.go:25-29). `NewGrantService(store)` wraps the raw store with no policy (grant.usecases.go:15-17), and `GrantRole`, `RevokeRole`, `GrantPermission`, `RevokePermission`, `AttachToRole`, `DetachFromRole` and `Describe` contain no `access.Require` and no principal read. `SetPasswordUseCase.Execute` takes an arbitrary `SubjectRef` and rewrites its password with no check (usecase.set-password.go:18-84). `accessfx` publishes both into every graph regardless (`newSetPassword`, `newGrantService`, accessfx.go:80-94).

**Why it is naive**

It models the permission catalogue as documentation rather than as a gate. A seeded row named "Set another account's password", held by `admin`, reads to an operator and to a consumer as something enforced somewhere — and for the sibling resource (`RoleService`) it is, which is exactly what makes the asymmetry invisible.

**Failure scenario**

A consumer takes `*access.GrantService` out of the fx graph and mounts `POST /admin/subjects/{type}/{id}/roles` the way they mounted `RoleService` — behind `authhttp.Authenticated()`, because `RoleService` needs no extra check (its gate is inside). Every signed-in caller of any subject type can then POST themselves `{"role":"admin"}` and hold every permission in the deployment. The same shape over `SetPasswordUseCase` lets any signed-in caller set any other account's password.

**Suggested shape of the fix**

Either call `access.Require(ctx, PermGrantWrite)` / `PermGrantRead` / `PermCredentialWrite` at the top of each `GrantService` method and of `SetPasswordUseCase.Execute`, with an explicit unguarded constructor for the seed path that legitimately runs with no principal; or stop declaring and seeding permissions nothing in the module reads, and say in docs/modules/en/access.md that these five are labels the consumer must enforce itself.

**How it was verified**

Read every cited line and ran the grep myself; `PermSessionRead` is unused too, which the reviewer missed. D-109 states the rule this breaks and names this exact operation: "An application operation — a dead-jobs listing, a password reset — enforces its permission imperatively inside its own body ... This is ground truth: it is the check that actually runs." No decision sanctions the absence; D-066 sanctions the absence of *routes*, which is a different thing. I downgraded from high to medium: the escalation needs the consumer to mount an unguarded route, and FL-023:157 does acknowledge that `SetPassword` is "what a seed or an administration screen calls", which is why a bare `Require` inside it would break the seed path. What is unambiguous is that five declared, seeded, admin-held permissions are enforced by nothing.

#### `G-ACC-05` — A password change leaves every other session live unless the request body asks otherwise, and no configuration can change that

**🟡 medium** · **CONFIRMED** · `naive-contract` · [auth/access/usecase.change-password.go:58](auth/access/usecase.change-password.go#L58) · `ChangePasswordUseCase.Execute`

**Evidence**

`if !cmd.RevokeOthers { return nil }` (usecase.change-password.go:58-60) — the revocation is skipped entirely when the flag is false. The flag comes straight off the wire: `RevokeOthers bool `json:"revokeOthers"`` (access.endpoints.go:61), passed through unmodified in `Endpoints.ChangeSecret` (access.endpoints.go:103-109), and the bindings decode that struct from the body with no default. Omitting the key in JSON yields `false`. The sibling `SetPasswordUseCase.Execute` revokes unconditionally (usecase.set-password.go:72-77) and docs/modules/en/access.md:236 promises that for it ("It closes every session the subject held"). Nothing in `access.Config` (access.config.go:5-29) can change the change-password default. A repository-wide grep for `revokeOthers`/`RevokeOthers` outside tests returns only access.command.go:58, access.endpoints.go:61 and :107, and usecase.change-password.go:58 — it appears in no document at all.

**Why it is naive**

It treats "do my other sessions die?" as a client preference on the same footing as a display option, when it is the deployment's security policy and the reason people change a password in the first place. The safe value is also the one a hand-written client is least likely to send.

**Failure scenario**

A session is stolen — shared machine, XSS-lifted bearer token, a leaked native-client credential store. The owner does the one thing every product tells them to do and changes their password through `POST /auth/password`. The front end posts `{"current":"…","new":"…"}` with no `revokeOthers` key, so `cmd.RevokeOthers` is false, nothing is revoked, no sink is announced, and the attacker's session keeps working for the rest of the 720h absolute TTL — under `accessjwt` it keeps rotating its refresh credential for that whole window. The endpoint answers 200 with `revoked: 0`, and the deployment has no setting that would have made it otherwise.

**Suggested shape of the fix**

Invert the wire default — revoke unless the caller explicitly opts out — or take the choice out of the body entirely and put it in `access.Config` so the deployment decides. Either way document it: `revokeOthers` currently appears in no document in the repository.

**How it was verified**

Read the code and confirmed both existing exercises of the path pass `RevokeOthers: true` (access.revocation_test.go, test/integration/auth_access_serialization_test.go), so the false branch is unexercised. Searched decisions and use cases: D-072 lists `ChangePasswordUseCase.Execute` as one of the five closing paths but governs only how it announces, never when it closes; UC-023 §12 covers the administrator's *reset* and §7 covers sign-out; neither reaches the self-service change. No doc sanctions the default because no doc mentions the flag. Downgraded from high to medium — the caller has a workaround (send the flag), but the deployment has none and the shipped default is the unsafe one.

#### `G-ACC-06` — The current-password check in change-password is unlimited, unobserved, and shares the sign-in path's hashing bulkhead

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [auth/access/usecase.change-password.go:38](auth/access/usecase.change-password.go#L38) · `ChangePasswordUseCase.Execute`

**Evidence**

`ok, err := this.Hasher.Verify(cmd.Current, credential.SecretHash)` (usecase.change-password.go:38) runs with no `this.admit(...)` and no `this.recordAttempt(...)`. `Deps.admit` is defined at access.protection.go:68 and its only caller in the module is usecase.login.go:32; `recordAttempt` (access.protection.go:79) is called only from usecase.login.go:36, 91, 95. The endpoint is mounted for every deployment (`{"POST", this.Path("/password"), ChangeSecret, false}`, accesshttp.go) and `Endpoints.ChangeSecret` needs only a principal (access.endpoints.go:98-111). The only other brake is `BulkheadHasher`, which is a concurrency permit rather than a rate — and it is the single shared instance `Runtime` built (access.runtime.go:47-50), so those verifications occupy the same permits sign-ins need, and `enter` answers `Overloaded()` to whoever finds the queue full (access.protection.go:281-294).

**Why it is naive**

It assumes that holding a session means you already know the password, so checking `Current` is a formality. The reason `Current` is asked for at all is the opposite assumption — that a session may be held by somebody who does not know the password — and that check is the only thing between session theft and permanent account takeover.

**Failure scenario**

An attacker who has lifted one access token replays `POST /auth/password` with `{"current": guess, "new": "…"}`. Each wrong guess costs one Argon2id verification and is recorded nowhere: no counter moves, no `AttemptObserver` is told, no lockout ever fires, and the deployment's Redis-backed `AttemptLimiter` — the thing D-089 tells an operator to install — never sees the traffic. At four permits and ~50 ms per hash the attacker gets millions of guesses a day, and the same requests starve the sign-in path of its hashing permits, so ordinary users get `overloaded` 503s while it runs.

**Suggested shape of the fix**

Route the current-password check through the same `admit`/`recordAttempt` pair as `LoginUseCase`, with an `Attempt` keyed on the subject rather than an identifier, and count a wrong `Current` as a failed attempt.

**How it was verified**

Read both use cases and `Deps.admit`/`recordAttempt`/`observe` in full; the grep result holds. D-089 is the governing decision and its scope does not reach here: its invariant is "the number of *sign-in* attempts ... bounded", its rationale is entirely about a sign-in refusal becoming an oracle, and its 'Where it lives' table names `usecase.login.go` and `usecase.enroll.go` only. UC-023 §11 states "Guessing is bounded" without qualification, which this path does not honour. Both reviewers found the same defect from different angles; merged. Downgraded to medium — it needs a prior session compromise to matter.

*Merged from reviewer findings: access-usecases-change-password-unlimited-guessing*

#### `G-ACC-07` — Every session-closing path names all of a subject's live sessions in one IN list and is refused past the dialect's bind budget

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [auth/access/usecase.logout-all.go:91](auth/access/usecase.logout-all.go#L91) · `Deps.revoke`

**Evidence**

`Deps.revoke` reads every matching row with `GetAll(ctx, ..., crud.ForUpdate(), crud.PrimaryOnly())` and no limit (usecase.logout-all.go:66-73), then writes them with `crud.Where(crud.InAny("ID", ids))` (:91). `InAny` binds one parameter per value — `for i, v := range this.values { ... w.bind(v) }` (crud/predicate.go:203-209) — and `SQL.Err` refuses the whole statement once the argument list exceeds the ceiling: "statement needs %d bound values, but dialect %q permits at most %d" (crud/render.go:113-127) against `Postgres.MaxBindValues() = 65_535` (crud/dialect.go:92) and `MySQL.MaxBindValues() = 65_535` (crud/dialect.go:129). Nothing caps live sessions per subject: `opaqueIssuer.Issue` inserts one row per sign-in with no count check (access.strategy.go:70-91), and a successful sign-in actively frees the attempt budget (`MemoryLimiter.Record` deletes the identifier counter on `AttemptSucceeded`, access.protection.go:184-190). `Store.LiveSessionsOf` (access.repo.go:205-220) and `Endpoints.ListSessions` (access.endpoints.go:121-135) are unpaginated in the same way.

**Why it is naive**

It assumes the number of live sessions a subject holds is small enough to name in one statement. The count is caller-controlled — one row per successful sign-in, kept for the 720h default TTL, never pruned — and the paths that read all of them are exactly the ones a compromise depends on: sign-out-everywhere, change-password-with-revoke, and the administrator's reset.

**Failure scenario**

An attacker holding stolen credentials, or a badly behaved automated client that signs in per run over the 30-day TTL, accumulates more than 65,535 live rows for one subject. From then on `SignOutAll`, `ChangeSecret` with `revokeOthers`, `KillSession`'s siblings and `runtime.SetPassword()` all build an `UPDATE ... WHERE id IN (...)` past the bind budget and are refused with a `*crud.SchemaError` before `Exec`. Because `revoke` runs inside the transaction, the password write is rolled back with it: the account can no longer be signed out anywhere or have its password reset, and nothing removes the rows, so it stays that way. Below the threshold the same call takes a `FOR UPDATE` lock on tens of thousands of rows in one transaction.

**Suggested shape of the fix**

Chunk the write the way `SaveAll` and `Delete` already do under D-079 — charge the fixed scopes, divide the remainder, run the chunks inside the transaction the caller already opened, accumulating the sink list — and either bound how many sessions a subject may hold or paginate `LiveSessionsOf`/`ListSessions`.

**How it was verified**

Read `revoke`, `InAny`, the `inNode` renderer, `SQL.Err` and the dialect limits myself. D-079 is the decision on point and it forbids rather than sanctions this: "An `IN` predicate cannot be repaired by rendering several `IN` clauses ... A direct Go predicate that is too large is therefore refused. A caller can reduce the set" — `access` is that caller and does not reduce it. D-072 mandates the read-then-write-by-id shape but says nothing about how many. I dropped the reviewers' SQLite framing: `auth/access/migrations/00001_access.sql` is PostgreSQL (UUID, TIMESTAMPTZ, gen_random_uuid) and the integration suite runs Postgres and MySQL, both 65,535 — so the 999 threshold is not reachable. That is why I downgraded both reviewers' 'high' to medium: it needs ~65k sign-ins for one subject.

*Merged from reviewer findings: access-usecases-revoke-in-list-unbounded*

#### `G-ACC-08` — Nothing anywhere tests that a replayed credential closes the session or reaches the deny-list — proved by deleting the whole arm

**🟡 medium** · **CONFIRMED** · `vacuous-test` · [auth/access/accessjwt/accessjwt.go:277](auth/access/accessjwt/accessjwt.go#L277) · `core.Refresh case Replay`

**Evidence**

accessjwt.go:277-284 is the entire theft response: `close(ctx, session.ID, now, access.ReasonRefreshReplayed)`, a WARN, then `refused()`; `close` writes `RevokedAt`/`RevokedReason` and then `this.spec.Revocation.Revoke(ctx, session, now.Add(AccessTTL))` (accessjwt.go:410-421). I replaced the whole `case Replay:` body with a bare `return access.AuthResponse{}, refused()` and ran `go test ./...` in `auth/access/accessjwt`: `ok`. `rotation_test.go` exercises `Classify` as a pure function and never constructs a `core`; `rotation_race_test.go` and `accessjwt_test.go` drive only `Rotate`/`RotateAgain` and the idle `Unusable`. In `test/integration`, grepping for `Refresh` and `Replay` returns only three `response.Refresh != ""` assertions in the sign-up files — `Refresh(` is never called, so `authMemoryRevocations.Revoked` is a method nothing in the suite reaches.

**Why it is naive**

The suite pins the verdict and leaves the consequence unpinned. That `Classify` returns `Replay` is proved; that `Replay` closes anything, writes `ReasonRefreshReplayed`, or produces a deny-list entry is proved nowhere — so the pure-function tests read as coverage of a guarantee they never touch.

**Failure scenario**

Someone simplifies the replay branch into an ordinary refusal, or a `Revocation` misconfiguration makes `close` a no-op. A replayed credential then becomes a plain 401 that closes no session and writes no deny-list entry, and `Revocation` becomes decorative on the replay path. `make unit` and `make integration` both stay green. UC-023 must-hold 15 and docs/modules/en/accessjwt.md:181-185 ("close the lineage", "The row records `ReasonRefreshReplayed`") have no test behind them.

**Suggested shape of the fix**

Add a `core`-level test beside `rotation_race_test.go`: script a row whose `previous_token_hash` is the presented digest with `rotated_at` past the grace, assert the UPDATE carries `ReasonRefreshReplayed`, assert a fake `RevocationList` received the session id with `until >= now + AccessTTL`, and keep the in-grace `RotateAgain` case as the control that the row shape alone is not what closed it.

**How it was verified**

Verified empirically, not by reading: edit, `go test ./...` → ok, restore, `go test ./...` → ok. Working tree clean. D-112 §Proven by, D-098 §Proven by and FL-023 §Tests that walk this flow all list `rotation_test.go` and `rotation_race_test.go` for this behaviour, and both are `Rotate`/`RotateAgain`-only — so three documents credit coverage that does not exist. D-020 (tests are the specification) is the rule this breaks, not a sanction for it.

#### `G-ACC-09` — The eviction check asks one node while revocations are written across all of them, on a client type whose purpose is to be multi-node

**🟡 medium** · **CONFIRMED** · `false-promise` · [auth/access/accessjwt/revokeredis/eviction.go:73](auth/access/accessjwt/revokeredis/eviction.go#L73) · `List.EvictionPolicy / List.VerifyEvictionPolicy`

**Evidence**

`New` takes a `redis.UniversalClient` (revokeredis.go:25) and `revokeredisfx.Dependencies.Client` pulls a `redis.UniversalClient` straight from the graph (revokeredisfx.go:17) — the type `redis.NewUniversalClient` returns a `*ClusterClient` for multiple `Addrs`. The check is one `this.client.ConfigGet(ctx, EvictionParameter)` (eviction.go:73). I read the vendored go-redis v9.7.3: `ConfigGet` builds `NewMapStringStringCmd(ctx, "config", "get", parameter)` with no `SetFirstKeyPos` (commands.go:509-513); `ClusterClient.cmdSlot` calls `cmdSlot(cmd, cmdFirstKeyPos(cmd))` (osscluster.go:1832-1838); `cmdFirstKeyPos` falls through its switch and returns 1 (command.go:78-99); `cmdSlot` then routes to `hashtag.Slot(cmd.stringArg(1))` = `hashtag.Slot("get")` — one deterministic master. `Revoked`/`Revoke` use `Exists`/`Set` on `this.key(session)` (revokeredis.go:48, 60), which route by the real key and land on every master. No code under `auth/` iterates nodes, and `verifyOnStart` runs the check exactly once (revokeredisfx.go:45-52).

**Why it is naive**

The check assumes "the client" is "a server". It models a single instance and is wired to the one client type whose whole purpose is that it may not be one. It also assumes the policy is fixed for the process lifetime: a Sentinel failover to a replica that was never given `noeviction`, or a managed provider's parameter-group change, is never re-asked.

**Failure scenario**

A deployment runs a 3-master Redis Cluster behind `redis.NewUniversalClient(&redis.UniversalOptions{Addrs: [a,b,c]})` and wires `revokeredisfx.Auto()`. Node A is `noeviction`; node C was created from a different parameter group and is `allkeys-lru`. The start hook sends `CONFIG GET maxmemory-policy` to the master owning the slot of the literal string "get", gets `noeviction`, returns `Retaining`, and the process starts clean. Revoked session ids hash across all three masters. Under memory pressure node C evicts the third of the revocation entries it holds, `List.Revoked` reads `Exists`=0 for them, and the authenticator admits signed-out sessions for the rest of their `AccessTTL` — the exact defect D-112 exists to make impossible, with the check reporting that it cannot happen.

**Suggested shape of the fix**

Type-switch and fan the check out: `ForEachMaster` on a `*redis.ClusterClient`, refusing if any node evicts and refusing outright if the node set cannot be enumerated; for a failover client, either re-run the check on reconnect or say in `EvictionPolicy` that the verdict covers one node. At minimum refuse a client this check cannot cover completely rather than answering `Retaining` for it.

**How it was verified**

I checked the go-redis routing claim in the module cache rather than taking it on trust — `cmdFirstKeyPos` really does return 1 for `config`, so the slot is `hashtag.Slot("get")`. D-112 §Invariant states the guarantee for "its own server's `maxmemory-policy`" (singular) and neither it nor docs/modules/en/revokeredis.md reasons about more than one node; D-104 constrains who else shares the resource, not how many nodes it has. Nothing sanctions a one-node sample standing in for the whole keyspace, and `eviction_test.go`/`revokeredisfx_test.go` are single-miniredis throughout. Kept at medium rather than the reviewer's high: the failure needs a heterogeneous cluster, since a uniformly evicting one is still caught by the sampled node.

#### `G-ACC-10` — The start-up grant sync is a check-then-insert, so replicas starting together fail each other's OnStart hook

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [auth/access/accessfx/accessfx.go:100](auth/access/accessfx/accessfx.go#L100) · `syncOnStart`

**Evidence**

`syncOnStart` runs `runtime.Declare(...)` then `return runtime.Sync(ctx)` inside an `fx.Hook.OnStart` (accessfx.go:96-103), so a failure aborts application start. `Sync` is read-all-then-insert-missing with no transaction and no conflict handling: `syncPermissions` reads every permission into a map and then calls `store.Permissions.Save(...)` for each code it did not see, wrapping any error as `"access: declaring permission %q: %w"` (access.seed.go:36-67); `ensureRole` does `RoleBySlug` then `Save` on not-found (access.seed.go:85-102); `attachAll` does the same shape against `role_permissions` (access.seed.go:104-127). The schema makes each one a hard conflict: `uq_permissions_code` (migrations/00001_access.sql:47), `uq_roles_slug` (:58), `uq_role_permissions` (:65). The documented contract is that every process does this: docs/modules/en/access.md:441 says "`runtime.Sync(ctx)` at start-up, before the server accepts anything" and :451 says "every start, before the first request". The only tests over `accessfx` are `fx.ValidateApp` graph-shape checks that never run a constructor.

**Why it is naive**

It assumes exactly one process reaches `Sync` at a time, while the module's own wiring runs it in every replica's start hook — so the ordinary deployment shape (N replicas rolled out together, or a scale-up) runs it concurrently against one database.

**Failure scenario**

Three replicas of a fresh deployment start within the same second. All three read the empty `permissions` table, all three try to insert `role.read`; two lose on `uq_permissions_code`, `Sync` returns a wrapped unique violation, the fx `OnStart` hook fails and those processes exit. The deployment sees crash-looping pods on every first rollout, and on any later deploy that adds a permission or a `ModuleGrants` role — self-healing only because the winner's rows make the retry a no-op, so the cause never appears in the application's own logs.

**Suggested shape of the fix**

Make `Sync` idempotent against a concurrent peer: wrap it in one transaction behind an advisory lock, or treat a unique violation on `permissions`/`roles`/`role_permissions` as "someone else declared it" and re-read rather than failing the hook.

**How it was verified**

Read `syncOnStart`, all three write paths in `access.seed.go`, and the three unique indexes in the migration. D-070 explicitly hardens the sibling path against exactly this class — "The unique index on `subject_type` is what covers the absent-row race the lock cannot" (FL-023 §Arranging what a sign-up grants) — so the `Seeder` path is race-aware and the `Sync` path is not, and no decision covers `Sync` under concurrency. D-011 confirms a model with an unset auto key is an `INSERT` and not an upsert.

#### `G-ACC-11` — Attempt.IP is whatever a binding puts in it, and the shipped net/http binding puts host:port there

**🟡 medium** · **CONFIRMED** · `leaky-abstraction` · [auth/access/http/accessnet/accessnet.go:243](auth/access/http/accessnet/accessnet.go#L243) · `agentOf`

**Evidence**

The three bindings disagree about one neutral field: `accessnet` builds `access.Agent{UserAgent: r.Header.Get("User-Agent"), IP: r.RemoteAddr}` (accessnet.go:243) — `http.Request.RemoteAddr` is `host:port`, a fresh value per TCP connection — while `accessgin` uses `c.ClientIP()` (accessgin.go:221) and `accessfiber` uses `c.IP()` (accessfiber.go:215), both of which are bare addresses. `MemoryLimiter.keys` then uses the string verbatim as a counter key: `keys["ip\x00"+attempt.IP] = this.policy.MaxPerIP` (access.protection.go:213). `Attempt.IP` (access.protection.go:19) carries no statement that it is normalised, and no test in any binding asserts anything about the address.

**Why it is naive**

The transport-neutral contract carries a transport artefact. "Counting per IP" is only true if every binding hands over a canonical address, and one of the three hands over one that is unique per connection — so the per-IP ceiling degenerates into a per-connection ceiling of `MaxPerIP`, and the counter map gains an entry per connection instead of per attacker.

**Failure scenario**

A deployment on `accessnet` installs `access.DefaultMemoryLimiter()` and believes it has the 50-failures-per-IP ceiling D-089 describes. One host guessing passwords opens a new connection per request; every attempt lands under a different key (`203.0.113.7:41522`, `:41523`, …), so the IP ceiling never fires and only the 10-per-identifier ceiling remains — credential stuffing across many identifiers from one host is not limited at all. Each attempt also allocates a counter `prune` cannot remove until its window closes, and `prune` only runs once the map holds 4096 entries (access.protection.go:222-231). The session row written on the same path stores `1.2.3.4:41522` in `sessions.ip`, which is then rendered back to the user in `SessionDto.IP`.

**Suggested shape of the fix**

State on `Agent.IP`/`Attempt.IP` that it is a bare address, split the port off in `accessnet` with `net.SplitHostPort` (falling back to the raw value), and add the assertion to the binding triplet's tests so the three cannot drift again.

**How it was verified**

Read all three `agentOf` implementations and `MemoryLimiter.keys`/`Admit`/`Record`. D-089 names `MemoryLimiter` "per identifier and per IP" and its 'Where it lives' table stops at `access.protection.go`; it never says where the address comes from, and nothing sanctions a per-connection key. This is also the repository's own triplet rule (CLAUDE.md: "A change to one HTTP binding is a change to all three") failing on a difference nobody parked in a `routing_test.go`.

#### `G-ACC-12` — GrantsService.For discards every Directory.Describe failure and returns a principal with an empty profile

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [auth/access/access.grants.go:74](auth/access/access.grants.go#L74) · `GrantsService.For`

**Evidence**

`if profile, err := directory.Describe(ctx, ref.ID); err == nil { principal.Profile = profile }` (access.grants.go:73-77) — the error is not returned, not wrapped and not logged, so a cancelled context and a dead profile store are indistinguishable from a directory that simply has nothing to say. Every other error in the same function is returned (access.grants.go:31-33, 43-45, 58-60). This runs on every authenticated request (`SessionAuthenticator.Authenticate` → `this.grants.For(ctx, ref)`, access.authenticator.go) and on every sign-in response (`opaqueIssuer.Issue` → `this.deps.Grants.For`, access.strategy.go:96). The sibling call site treats the same error as fatal: `SetPasswordUseCase.Execute` returns it (usecase.set-password.go:30-33). The test stub carries a `describeErr` field (access_stub_test.go:17) that no test in the package ever sets.

**Why it is naive**

It assumes `Describe` is a cheap local lookup that either has a profile or does not. It is a consumer-implemented method over the consumer's own account table, so it fails the way any database call fails — and `Profile.Attributes map[string]any` (access.api.go:37) is an open bag that invites exactly the kind of data a caller then makes decisions on.

**Failure scenario**

The accounts table is briefly unreachable, or a request is cancelled mid-flight, while the sessions table is fine. Every request still authenticates and every principal comes back with `Profile{}`: `GET /auth/me` answers 200 with an empty display name and identifier, the sign-in response embeds a blank principal, and consumer code reading `principal.Profile.Attributes["tenant"]` sees `nil` and reads it as "no tenant" rather than "unknown". Nothing records that it happened.

**Suggested shape of the fix**

Return the failure, or at minimum mark the profile as unresolved so a caller can tell "this subject has no profile" from "the directory could not answer"; set `describeErr` in a test to pin whichever is chosen.

**How it was verified**

Read the function and both call sites. Correcting the reviewer: `GrantsService` holds no logger at all — `NewGrants(store, directories)` (access.grants.go:19-21) takes only those two — so it could not log this even if it wanted to; the reviewer's "the module holds a `*slog.Logger` and does not use it here" is wrong about this type. Searched decisions: D-066 fixes `Directory` at three read methods and says the count is load-bearing but says nothing about how their errors travel; D-062 makes the silent discard worse rather than better. FL-023 §Verifying a request lists `GrantsService.For` in the chain without mentioning the swallow. No decision covers it. Kept at medium: the roles and permissions the module's own authorization uses do propagate their errors, so this is a silent degradation of the profile only.

#### `G-ACC-13` — A batch revocation abandons the rest of the batch on the first Redis error, while the log claims the whole group failed

**🟡 medium** · **CONFIRMED** · `naive-implementation` · [auth/access/accessjwt/accessjwt.go:124](auth/access/accessjwt/accessjwt.go#L124) · `core.SessionsRevoked`

**Evidence**

`for _, session := range sessions { if err := this.spec.Revocation.Revoke(ctx, session, until); err != nil { return err } }` (accessjwt.go:120-128) — the first failure abandons the remainder. The caller wraps it as `fmt.Errorf("%s sessions %v: %w", subject, ids, err)`, naming every id in the group (access.revocation.go:76-79), and `Deps.announce` logs it and moves on (access.revocation.go:38-45). The same file already knows the alternative — `return errors.Join(failures...)` one line below, at access.revocation.go:81, is used across subject types but not within one.

**Why it is naive**

It treats a sequence of per-key network writes as all-or-nothing when it is neither. One transient `Revoke` failure — a connection reset, a `MOVED` during a cluster reshard, a deadline hit on entry k — discards the n-k entries that would have succeeded, and the error the operator reads claims the whole group failed.

**Failure scenario**

A user with 12 live sessions calls `POST /auth/logout-all?all=true` while Redis is failing over. `Deps.revoke` commits all 12 rows. `announce` → `tell` → `SessionsRevoked` writes entry 1, hits a dropped connection on entry 2 and returns immediately; entries 3-12 are never attempted even though the connection has recovered by then. Those sessions keep authenticating for the full `AccessTTL` because the authenticator only consults the deny-list, and the logged message names all 12 ids, so an operator cannot tell which actually landed. Recovery depends on the consumer having wired `Runtime.ReannounceRevocations` into a worker — an opt-in nothing in the library runs by itself.

**Suggested shape of the fix**

Keep going after a failure and return `errors.Join` of the per-session errors, so a blip costs one entry rather than the tail of the batch and the reported error names the ids that actually failed.

**How it was verified**

Read `SessionsRevoked`, `Deps.tell` and `Deps.announce` in full; `errors.Join` really is already imported and used in the caller. D-072 mandates the announcement and the replay path but says nothing about batch semantics; FL-023 §Closing a session step 5 says only "A failure here is logged and does not fail the sign-out". No decision sanctions abandoning the remainder.

#### `G-ACC-14` — The Hasher seam has no way to say a stored hash needs re-deriving, and no sign-in ever upgrades one

**⚪ low** · **CONFIRMED** · `missing-capability` · [auth/access/access.secret.go:29](auth/access/access.secret.go#L29) · `Hasher`

**Evidence**

`type Hasher interface { Hash(password string) (string, error); Verify(password, encoded string) (bool, error) }` (access.secret.go:27-30) — no context, no identity, one `bool` for the answer. `Argon2Hasher.Verify` deliberately derives with the *stored* parameters (`argon2.IDKey(..., stored.time, stored.memory, stored.threads, uint32(len(stored.digest)))`, access.secret.go:67), and `LoginUseCase.Execute` does nothing with the result but branch (usecase.login.go:57-67). `parseStoredHash` and every bound around it are unexported (access.secret.go:89-145), so a consumer cannot ask through the public API whether a row was written at last year's cost. The only public path that rewrites a hash is `runtime.SetPassword()`, which closes every session of the subject it touches (usecase.set-password.go:72-84).

**Why it is naive**

It models a password store as a pure predicate. A real one has to migrate: parameters get raised every few years — that is why `Argon2Hasher`'s fields are exported and why the PHC string carries `m`,`t`,`p` at all — and the only moment the plaintext exists to re-derive from is a successful sign-in. The contract gives that moment no way to notice and no way to write back.

**Failure scenario**

A deployment ships with the defaults and later raises `Argon2Hasher.Time`/`Memory` after a hardware review. Every account created before the change keeps verifying at the old cost forever, and nothing in the system reports how many there are. "Upgrade everybody" means either logging everybody out through `SetPassword` or forcing a password reset for the whole user base. Because `Hasher` is exported, the signature also freezes once the module is tagged.

**Suggested shape of the fix**

Widen the seam before it is tagged: `Verify(ctx context.Context, password, encoded string) (Result, error)` where `Result` carries `Matched` and `Rehash`, or an optional `Rehasher` found through the `SourceOf`-style helper the repository already uses for optional capabilities, with `LoginUseCase` writing the new hash back inside the transaction it already holds. The context is worth having independently: a consumer-supplied HSM- or KMS-peppered `Hasher` can neither be cancelled nor reach `port.Logger(ctx)`, which D-062 makes the only permitted logging seam.

**How it was verified**

Read the interface, `Argon2Hasher.Verify` and `LoginUseCase.Execute`; `TestAHashVerifiesAgainstAHasherWithDifferentParameters` (access.secret_test.go) does pin that raising `Time`/`Memory`/`KeyLen` keeps old hashes verifying, so the cost is tunable and old rows are permanent by design. D-089 owns this signature and bounds what a *stored* hash may cost, but its 'What it forbids' list constrains parsing, not migration; no decision or use case mentions rehash-on-login. Downgraded from medium to low: it is a capability gap with a (bad) workaround rather than a wrong answer.

#### `G-ACC-15` — FL-023 says a sign-up opens its session after the commit; the code opens it inside the transaction, and a test pins that

**⚪ low** · **CONFIRMED** · `false-promise` · [docs/ai/flows/FL-023-a-sign-in-becomes-a-session.md:137](docs/ai/flows/FL-023-a-sign-in-becomes-a-session.md#L137) · `SignUpUseCase.Execute`

**Evidence**

FL-023 §Registering step 5 reads: "**the session, after the commit** — opening it inside would let a failure with nothing to do with signing in roll it back" (FL-023:137-138). The code does the opposite: `response, err = this.issuer.Issue(txCtx, subject, agent)` is the last statement inside `this.Store.OwnedTx(...)` (usecase.signup.go:60-61). The behaviour is deliberate and pinned — `TestSignUpDiscardsResponseAndRollsBackOnIssuerOrCommitFailure` asserts `issuer.inTransaction` and that an issuer failure rolls the registration back (access.signup_serialization_test.go:37-82) — and docs/modules/en/access.md:191-192 agrees with the code ("writes the account, credential, default-role grant and session before one commit"). The flow doc is the odd one out.

**Why it is naive**

Not a runtime defect — a stale map, and CLAUDE.md makes flows the file an agent reads instead of re-deriving the call chain. This step describes the transaction boundary backwards, which is the one property the rest of the sign-up section is about.

**Failure scenario**

An agent or a consumer reads FL-023 to learn where a sign-up's session is opened, concludes that a custom `SessionIssuer` runs outside the registration transaction, and writes an issuer that performs an external side effect or uses its own connection — the exact thing docs/modules/en/access.md:319-324 warns against. It compiles, the tests it writes pass, and the registration rollback it never expected leaves the side effect behind.

**Suggested shape of the fix**

Rewrite step 5 to match the code and the module doc — the issuer runs inside the sign-up transaction and a failure in it rolls the account and credential back — and name `TestSignUpDiscardsResponseAndRollsBackOnIssuerOrCommitFailure` under 'Tests that walk this flow'.

**How it was verified**

Read FL-023:120-138, `SignUpUseCase.Execute` and the test body. The ground rules make a doc-vs-code mismatch a false-promise finding in its own right. D-066 §What it forbids ("Do not make `EnrollUseCase` open its own transaction with `InNewTx`. It joins") and D-072 (which governs when a *revocation* is announced relative to a commit — a different path, stated correctly in the same flow) between them make the code's ordering the intended one, so the doc is what is wrong.

#### `G-ACC-16` — Config.Sessions() clamps a negative IdleTTL to the default and lets a zero disable the idle deadline, so DefaultIdleTTL is reachable only from a wrong value

**⚪ low** · **PLAUSIBLE** · `false-promise` · [auth/access/access.config.go:56](auth/access/access.config.go#L56) · `Config.Sessions`

**Evidence**

`Sessions()` repairs two fields from zero and the third only from below zero: `if settings.TTL <= 0 { settings.TTL = DefaultSessionTTL }` / `if settings.IdleTTL < 0 { settings.IdleTTL = DefaultIdleTTL }` / `if settings.TouchInterval <= 0 { ... }` (access.config.go:53-61). Every consumer treats zero as off: `Session.Live` guards with `idle > 0 &&` (access.model.go:100), `LiveSessionsOf` with `if idle > 0` (access.repo.go:216), and accessjwt's own start-up guard with `case window.Idle > 0 && settings.AccessTTL > window.Idle:` (accessjwt.go:174) over `lifetimes := dependencies.Config.Sessions()` (:77). `DefaultIdleTTL = 7 * 24 * time.Hour` is exported (access.config.go:33) yet reachable only from a negative duration. `accessfx.Module(configuration access.Config)` takes a Go value (accessfx.go:44-55), so an in-process wiring gets whatever the zero value means. The unit test that pins the pair covers `TTL` only (`TestAnUnsetSessionLifetimeFallsBackToTheDefault`, access.deps_test.go:64-72).

**Why it is naive**

The one field where zero means "disable a security deadline" is the one field the defaulting function does not repair, and the branch that is there converts a value somebody wrote wrongly into a default rather than reporting it — the exact clamp shape the module refuses elsewhere.

**Failure scenario**

Two paths. (a) A consumer writes `access.New(access.RuntimeSpec{Config: access.Config{Session: access.SessionConfig{TTL: 30 * 24 * time.Hour}}})`, having set the field they knew about: sessions never idle out, an abandoned token on a shared machine authenticates for the full 30 days, `GET /auth/sessions` lists month-old sessions as live, and for an `accessjwt` subject `checkLifetimes` skips its `AccessTTL > IdleTTL` arm entirely, so D-088's start-up guard silently does not run. (b) A deployment writes `IdleTTL: -1h` (a subtraction that came out backwards, a flag parsed wrong): it silently becomes 168h, the deployment reports the value it configured and runs on another, and nothing ever mentions the `-1h`.

**Suggested shape of the fix**

Refuse a negative `IdleTTL` by name and value at start-up, the way `accessjwt.refuseLifetimesBelowZero` does for `Spec`, and decide explicitly what a zero means — either fill it from `DefaultIdleTTL` like its two neighbours and give "no idle deadline" its own spelling, or document zero-means-off next to the 168h row in docs/modules/en/access.md:490.

**How it was verified**

Read `Sessions()`, `Session.Live`, `LiveSessionsOf` and `checkLifetimes`. Marked PLAUSIBLE rather than CONFIRMED for the zero half: `TestAnIdleDeadlineNobodySetLeavesEveryCredentialAlone` (rotation_test.go) and `TestSessionIsClosedByRevocationExpiryAndIdleness` pin zero-means-off at the consumption sites, so that half may be deliberate — and the documented 168h default is real on the cleanenv path, where the `env-default:"168h"` tag fills it. What I could not settle is whether the Go-literal path was meant to be repaired. The negative half is not ambiguous: D-088 §What it forbids bullet 4 says "Do not treat a negative duration as 'unset'. It is the same clamp wearing a zero test", and UC-023 must-hold 24 says a duration below zero "stops the start and names the field and the value, rather than becoming that default and being reported nowhere" — `SessionConfig` does the forbidden thing. I downgraded to low because the reviewer's headline (a zero Config is the most permissive combination) rests on the half I could not settle.

#### `G-ACC-17` — The cross-site check exits before it looks at the origin, so the one handler that installs a cookie is the one it can never reach

**⚪ low** · **PLAUSIBLE** · `missing-capability` · [auth/access/http/accesshttp/crosssite.go:64](auth/access/http/accesshttp/crosssite.go#L64) · `Credentials.Protect`

**Evidence**

`Protect` returns nil at crosssite.go:64-66 — `if !this.presented(cookie) { return nil }` — before it reads `Sec-Fetch-Site` (:68) or the deployment's `CrossSite.Origins` (:72-75). Every binding calls it at the top of `SignIn` (accessnet.go:129, accessgin.go:112, accessfiber.go:126), where it is a no-op for any caller not already signed in. The handler then resolves the delivery — with no `X-Auth-Delivery` header the default is cookies — and the response sets both HttpOnly cookies. Nothing checks the request's Content-Type: `porthttp.DecodeJSON` only reads and unmarshals (port/porthttp/body.go:14-49), so `Content-Type: text/plain` with a JSON body is a CORS simple request needing no preflight. There is no exported way to make `Protect` run on a request that carries no cookie; the short-circuit is unconditional.

**Why it is naive**

The contract models ambient authority as something only ever *spent*, never *created*. The sign-in response is the one place on this surface that installs ambient authority into a browser, and it is the one unsafe handler the check can never reach, because the precondition for the check is the very cookie the handler is about to set.

**Failure scenario**

A logged-out victim loads attacker.test, which posts a no-cors `fetch` with `Content-Type: text/plain` and the attacker's credentials to `/auth/login`. `jar.protect` finds no cookie and returns nil, `DecodeJSON` parses the body regardless of Content-Type, and the response carries `Set-Cookie: access=…; HttpOnly` and `refresh=…`. Where the browser stores them, the victim later navigates to the deployment's own front end and is silently operating inside the attacker's account. A deployment that configured `CrossSite{Origins: ["https://app.example"]}` gets no protection on this path either, because the origin list is never consulted for it.

**Suggested shape of the fix**

Treat this as a documentation decision first: D-102 §What this does not do lists XSS on the deployment's own origin and consumer routes, and login CSRF belongs there if it is being accepted. If it is to be closed, the narrow version is to run the origin half on an unsafe request whose *resolved delivery* would set a cookie — a caller asking for `X-Auth-Delivery: body` stays unchecked, which preserves the native-client property the short-circuit exists for.

**How it was verified**

Read `Protect`, `presented`, all three `SignIn` handlers and `DecodeJSON`; the code says what the reviewer says. Marked PLAUSIBLE and reduced to low for two reasons I could not settle. First, D-102 §What it forbids bullet 1 is categorical — "Do not check a request that presented no cookie of this deployment's" — and its rationale ("a check that refuses the innocent is a check somebody waives") does reach a native client signing in with no `Origin`, so the reviewer's claim that the rationale misses this case is only half right: it misses the *minting* framing but does cover the innocent-caller cost of the fix. Second, whether the browser stores a cookie set on a cross-site no-cors subresource response is browser- and third-party-cookie-policy dependent, and I could not verify it here. What is certain and worth reporting is that D-102 never considered login CSRF at all.

---

## Refuted (do not re-litigate)

Ten candidate findings were raised by a reviewer and killed by the verifier. They are recorded per module
above, under each module's **Refuted** heading, with the code or the decision that settles them. The
recurring reason is the one this repository warns about in `CLAUDE.md`: the reasoning was in
`docs/ai/decisions/` and not in the code.

| Module | Refuted candidate | Settled by |
|---|---|---|
| storage | Chain signals a failed composition by returning a nil Store instead of an error | repo-wide convention (`port.ChainService`), pinned by test, tracked as an open M0 freeze item |
| storage | Neither a write nor a presigned link can carry Content-Disposition, Content-Encoding or Cache-Control | declared non-goal in the storage roadmap; the consumer owns the `*minio.Client` |
| jobs | One schedule's enqueue error abandons every remaining schedule in the cycle, and ScheduleRunResult cannot repo | D-108 sanctions the dying half; no occurrence is lost across the restart |
| jobs | ScheduleCadence can express only a fixed offset from an absolute anchor: no timezone, no calendar cadence, mis | feature request against a deliberately minimal, honestly named surface |
| jobs | The automatic payload identity digests the wire encoding while calling itself semantic, and the test that pins | the vacuity claim is wrong — the test uses two codec versions and pins a real property |
| jobs | jobsredis Claim, Renew and Recover mutate one entry per MULTI/EXEC, so a mid-batch failure leaves leases issue | self-heals via the expired-lease arm; the Renew half duplicates `G-JOB-02` |
| jobs | Nothing in the neutral contract says whether a backend can join the caller's transaction | D-118's own proven-by test makes exactly that assertion with public API only |
| cache | CompareAndSwapper and Transactional are built-in capabilities no code path can reach | D-093 decides this deliberately; the production consequence is kept as `G-CCH-07` |
| cache | The conformance suite never issues concurrent backend calls it asserts on | false as stated — `runTransientAccounting` and others do drive concurrent backend calls |
| auth | Credential.Token is a bare exported string while the repository requires a self-redacting value type for a dat | no library path leaks it; D-081 is about a printable boot config, not a per-request credential |

---

## Method

Two phases, 21 agents, all Opus at `xhigh` reasoning effort.

**Review (15 agents).** Two to three per module, each given a disjoint file slice and a distinct lens, and
told to read the non-test files in full plus the tests beside them. The lenses were chosen to be the
questions a naive implementation fails: the trust boundary and whether the scope reaches the driver
(tenancy); the object-store contract and the real S3 driver (storage); the declaration/codec contract,
crash-and-concurrency semantics, and driver honesty across `jobspg` vs `jobsredis` (jobs); the key
derivation and contract, the read-through and invalidation path, and the backend plus the conformance
harness (cache); the identity model and the shared-secret authenticator, and JWT verification plus the
transport triplet (auth); the identity store and its defaults, the attacked flows, and token rotation,
revocation and the browser surface (access).

Every reviewer was told what "naive" means here — naive contract, naive implementation, false promise,
missing capability, leaky abstraction, vacuous test — and told explicitly that naming, comment density,
layout, missing GoDoc and "could be more idiomatic" are not findings.

**Verify (6 agents, one per module).** Reopened every cited line, hunted the refutation, searched
`docs/ai/decisions/` by subject, checked that the failure is reachable through the public API by a
consumer, re-judged severity independently, and deduped across the two or three reviewers. Several
verifiers went further than reading: they wrote probes against the public API and deleted guards to check
whether a test would notice. Those probe files were removed; `git status` is clean.

### Known limits of this audit

- The reviewers were slice-scoped, so a defect that only appears in the interaction between two modules —
  `tenancy` × `jobs` durable context being the obvious candidate — was found only because one reviewer
  followed a call out of its slice. There was no dedicated cross-module lens.
- No finding was reproduced against a live PostgreSQL, MySQL, Redis or MinIO. The `jobspg` index and
  bind-budget findings (`G-JOB-11`, `G-ACC-07`) are read off the migrations and the dialect limits, not
  measured. The cache measurements (`G-CCH-03`, `G-CCH-04`, `G-CCH-05`) were run in-process.
- `crud/`, `port/`, `errs/`, `app/`, `remote/`, `health/`, `otel/`, `runtime/` and `utils/` were out of
  scope and were read only where a call from one of the six modules led into them.
