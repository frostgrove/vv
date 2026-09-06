# tenancyjobs / tenancystorage / tenancycache — building blocks + data integrity — AUDIT (2026-09-06)

> **Snapshot and provenance.** Every measurement below was produced against a copy of the
> repository at `/tmp/claude-1000/-home-user-ws-gd-lease/82a92a15-ac6c-4104-9609-680bbbcbfeb1/scratchpad/fw`,
> verified byte-identical (`diff -r`) to `tenancy/`, `cache/`, `storage/` and `jobs/` in the working
> tree at **00:40**. The working tree was being edited concurrently throughout the audit by a
> second, independent Claude Code session; at ~00:20 my first rsync captured `tenancy/lifecycle.go`
> and `tenancy/seal.go` mid-mutation (`inconsistent()` body removed; `hmac.Equal` replaced by a
> `string` comparison) with a `lifecycle.go.mutbak` alongside — another auditor running mutations
> against the live tree. I re-synced those two files and re-ran **every** experiment and **all 22
> mutations** against the verified-identical snapshot. The audited repository was not modified:
> `git status --porcelain | wc -l` = 54 before and after, and no `zz_*` / `*.mutbak` / `*.stash`
> exists under `tenancy/`, `cache/`, `storage/` or `jobs/`.
>
> **Known staleness at the time of writing (00:47).** `tenancy/seal.go` (mtime 00:43:18) and
> `tenancy/seal_test.go` (mtime 00:42:39) changed *after* the 00:40 snapshot. See GAP-16 and the
> re-check note on GAP-10.

## Direct answer

**No.** With the adapters wired as documented, tenant A cannot read, overwrite, invalidate or
execute tenant B's cached values, stored objects or background work. I proved this end-to-end
against real backends (`cachememory`, `storagefs`, `jobsmemory`), not by reading. **But** the
guarantee rests on four things that nothing in the repository holds: two untested composition
functions that can be silently neutered (`tenancycache.Partitioned`, `tenancystorage.Store`), one
wiring mistake the type system accepts (`cache.Global` over a tenant key), one handle-lifetime hole
(a `Store` obtained before deletion keeps writing after it), and one ambient-context inheritance
path for system-scoped jobs.

## Map

- **Entry points.** All three adapters are package-level functions; there is no container.
  `tenancycache.Keyed/Partition/Partitioned`, `tenancystorage.Namespace/Store`,
  `tenancyjobs.ContextProvider/IdentityRestorer`. Total exported surface: 2 + 3 + 2 symbols.
- **Composition root.** The consumer's. `_examples/tenancy-sharedrow/main.go:96` wires
  `tenancystorage.Namespace` only; **nothing in the repository wires `tenancycache` or
  `tenancyjobs` outside unit tests.**
- **Kernel/extension.** `tenancy` (core) ← each adapter → exactly one seam. Verified:
  `go list -deps` for each adapter yields `{seam} utils crud tenancy {itself}` and nothing else.
- **State.** None in the adapters. Storage: a namespace string derived per call. Cache: `Key[K]`
  carries a `tenancy.Scope` by value; the partition is recomputed per address. Jobs: state lives in
  the queue row (`jobs.DurableContextRecord`: scope, tenant digest, actor digest, token, provenance,
  epoch, unkeyed binding digest).
- **The two MACs.** `tenancy.Scope.binding` is HMAC-SHA256 under a per-process random salt
  (`authority.go:131`) — process-local unforgeability. `tenancy.Sealer` is HMAC-SHA256 under a
  configured `DurableKey` ≥ 32 bytes (`seal.go:132` at snapshot time) — cross-process.
  `Scope.Digest()` (`scope.go:84`) is a plain SHA-256 of `len||reference || epoch`, deliberately
  non-authorising; it is the sole input to both the object namespace and the cache partition.
- **Where the seam's guarantee actually lands.** Storage: `storage.Namespace` → `Backend` methods →
  `objects/<ns>/<key>` (fs, `storagefs.go:669`) or `prefix/<ns>/<key>` in one bucket (minio,
  `backend.go:752`). Cache: `cache.Address{NamespaceDigest, PartitionDigest, KeyDigest}`
  (`address.go:86`) — three independent 32-byte fields. Jobs: `jobs.PartitionKey` + sealed
  `ProtectedIdentityToken`.
- **Tests that run.** `go test ./tenancy/...` green (6 packages). `go test ./scripts/...` was RED
  during the audit and is green again — see GAP-16.

## Scorecard

| Law | Verdict | Evidence |
|---|---|---|
| **BB — building blocks recognisable** | pass | `Key[K]` is a proper VO (immutable, redacted `String`, `MarshalJSON` refuses, equality by value incl. scope); the adapters are thin factories over provider-owned interfaces. Longest function 28 lines. |
| **BB — provider owns the interface** | pass | Every adapter implements interfaces declared by its seam (`jobs.TrustedContextProvider`, `cache.Partitioner`, `storage.Backend` consumers). Zero consumer-declared ports. |
| **BB — no cross-context implementation imports** | pass | `go list -deps` proves one seam per adapter; `scripts/tenancy_test.go:53-84` covers all five adapters and **really fails** (mutation M23). |
| **BB — VO does no external validation** | pass (borderline) | `Key[K]` holds no authority; the control-plane check is in the `Keyed` factory. But the *result* of that check has unbounded lifetime → GAP-3. |
| **BB — error contract** | **fail** | `tenancystorage.Namespace` returns two unrelated taxonomies depending on which prefix rule fails → GAP-9. `RestoreTrustedIdentity` destroys all four of the adapter's typed refusals → GAP-7. |
| **DI — cross-tenant read/write/invalidate** | pass | Proven end-to-end for cache (Lookup/Put/Forget/LookupMany/Resolve) and storage (Put/Open/Head/Delete/Stage/Promote/Abort/CleanupExpired). |
| **DI — prefix / concatenation ambiguity** | pass | Namespaces are `prefix + "-" + hex(16)`, fixed-width suffix ⇒ decomposition unique; 168 adversarial (prefix, reference, epoch) triples produced 168 namespaces, none a prefix of another. Cache never concatenates: `Address` is three digests. |
| **DI — in-flight dedup / eviction domain** | mixed | Single-flight keys on the full `Address` (`cache.go:51`) — proven not to join tenants. But the partition is **not** an eviction domain → GAP-12. |
| **DI — bulk invalidation** | **fail** | `cache.TagInvalidator` has no partition → GAP-5. |
| **DI — generation fencing** | mixed | Storage + cache: epoch inside the digest, proven. Jobs: partition and intent key **exclude** the epoch → GAP-6. |
| **DI — job identity, forgery, replay** | pass | Seal binds (namespace-digest, definition, reference, epoch); replay into another queue/job refused; a lying restorer refused by `RestoredIdentity.validFor` (`durable_context.go:497`). |
| **DI — fail-closed for unscoped work** | mixed | A `PartitionGlobal` job gives the handler no scope and every seam answers `ErrNoScope` — closed. But an ambient scope on the worker context is inherited → GAP-4. |
| **DI — retry / idempotency (UC-57)** | **fail** | Authority re-derived per attempt (proven, 3 lookups for 3 deliveries). Nothing else: no attempt fence, no dedup key, no compensation → GAP-13. |
| **Tests — do they hold the guarantee** | **fail** | 22 mutations, **12 killed, 10 survived**, including the two that collapse tenancy entirely. |

## Metrics

| Metric | Threshold | Actual | Worst offenders |
|---|---|---|---|
| files > 400 lines (adapters) | 0 | 0 | `tenancyjobs/jobs.go` 107, `tenancycache/cache.go` 58, `tenancystorage/storage.go` 51 |
| functions > 50 lines | 0 | 0 | longest: `RestoreIdentity` 28, `Capture` 25 |
| internal imports per adapter file | ≤ 5 | 2, 2, 2 | — |
| adapters importing a second seam | 0 | **0** | verified by `go list -deps` and mutation M23 |
| exported symbols per adapter | ≤ 7 | 2 / 3 / 2 | — |
| `go vet ./tenancy/...` | clean | clean | — |
| `gofmt -l tenancy/` | empty | empty | — |
| effective tenant discriminator, storage | ≥ 32 B available | **16 B** (128 bit), 14 B of budget unused at a 1-char prefix | `tenancystorage/storage.go:14` |
| effective tenant discriminator, cache | — | **16..32 B**, varies with key length | `tenancycache/cache.go:52`, `cache/address.go:108` |
| mutants killed / applied | 22/22 | **12/22** | see table below |
| public composition functions with a test | 5/5 | **3/5** | `tenancycache.Partitioned`, `tenancystorage.Store` |
| UC-51..65 claimed covered by plan S3 | 15 | ~8 have a test | UC-57, 58, 60(accept.), 61(partial), 62, 63, 64 uncovered |
| `go test ./scripts/...` | green | RED at 00:42, green at 00:47 | `TestEveryTestNameTheDocsCiteExists` — GAP-16 |

### Mutation table (all applied to the verified-identical copy, project tests only)

| # | Mutation | Result |
|---|---|---|
| M1 | `tenancycache/cache.go:45` drop zero-scope guard | KILLED `TestAPartitionWithNoScopeIsRefused` |
| M2 | `cache.go:48` partition floor 16 → 1 | KILLED `TestAPartitionTooNarrowToIdentifyATenantIsRefused` |
| M3 | `cache.go:51` partition on reference only (drop generation) | KILLED `TestARestoredGenerationReadsNoneOfThePreviousOnesValues` |
| **M17** | **`cache.go:57` `Partitioned` returns `cache.Global`** | **SURVIVED** |
| M18 | `cache.go:26` `Keyed` ignores its class argument | KILLED `TestACacheKeyIsRefusedWhenTheClassIsNot` |
| **M4** | **`tenancystorage/storage.go:14` digest 16 → 4 bytes** | **SURVIVED** |
| **M16** | **digest 16 → 8 bytes** | **SURVIVED** |
| M5 | digest 16 → 2 bytes | KILLED `TestAnObjectNamespaceIsInjectiveAnd...` |
| **M14** | **digest 16 → 24 bytes (overflows the 63-char namespace at `MaxPrefixBytes`)** | **SURVIVED** |
| **M15** | **`storage.go:13` `MaxPrefixBytes` 30 → 60** | **SURVIVED** |
| **M6** | **`storage.go:50` drop the `"-"` separator** | **SURVIVED** (property still holds — hex is fixed-width; framing is unpinned, not broken) |
| M7 | `storage.go:43` drop zero-scope guard | KILLED `TestANamespaceWithNoScopeIsRefused` |
| M8 | `storage.go:49` namespace = reference instead of digest | KILLED (2 tests) |
| **M20** | **`storage.go:33` `Store` puts every tenant in namespace `"shared"`** | **SURVIVED** |
| M19 | `storage.go:21` `Namespace` ignores its class argument | KILLED `TestANamespaceIsRefusedWhenTheClassIsNot` |
| M9 | `tenancyjobs/jobs.go:104` drop the queue+definition binding | KILLED `TestADurableRecordCannotBeReplayed...` (3 subtests) |
| **M13** | **`jobs.go:106` concatenate the two binding fields into one** | **SURVIVED** (property still holds — first field is a fixed 32 bytes) |
| M10 | `jobs.go:92` drop the generation check | KILLED `.../restored_to_a_new_generation` |
| **M11** | **`jobs.go:88` re-derive under `ClassRead` instead of `ClassDurable`** | **SURVIVED** |
| M12 | `jobs.go:35` never seal at capture | KILLED (3 tests) |
| **M22** | **`jobs.go:77` never take the `ContextSystem` branch** | **SURVIVED** |
| M21 | `jobs.go:99` hand the handler the unbound context | KILLED `TestWorkEnqueuedByATenantIsExecutedAsThatTenant` |
| M23 | add `storage` import to `tenancycache` | KILLED `scripts/tenancy_test.go:73` |

## What I probed and cleared (silence is not evidence)

**`tenancystorage.namespaceOf` — separator injection, prefix containment, `ParseNamespace` rules.**
Clear.

```
prefix="a-b"                     -> ns="a-b-72504fae4da7e7a26508f7fa75e0ed7f"          err=<nil>
prefix="trail-"                  -> ns="trail--72504fae4da7e7a26508f7fa75e0ed7f"       err=<nil>
prefix="objects-0011223344556677"-> ns="objects-0011223344556677-72504fae...ed7f"      err=<nil>
prefix="-lead" / "Objects" / "obj/ects" / "obj ects" / "объекты" -> storage parse namespace: invalid
prefix=30×"p" -> ok (63 chars, exactly the storage limit); 31×"p" -> tenancy.ErrMalformed
produced 168 namespaces over 7 prefixes × 8 references × 3 epochs, all distinct, none a prefix of another
```

A prefix containing `-` is accepted but harmless: the hex tail is a fixed 32 characters drawn from
an alphabet that excludes `-`, so `prefix + "-" + hex` decomposes uniquely, and
`|n1| ≥ 34 > MaxPrefixBytes = 30` makes containment arithmetically impossible. `storage.ParseNamespace`
(`types.go:29` → `validate.go:11`) allows only `[a-z0-9]` plus interior `-`, 1..63 bytes — it cannot
admit a namespace that is a prefix of another *of this shape*.

**LIST / iterate / delete-by-prefix.** There is none.

```
storage.Store verbs: [Abort Capabilities CleanupExpired Delete Head Open Promote Put Stage TemporaryURL]
```

`CleanupExpired` is the only bulk-delete verb and it is namespace-scoped in both backends:
`storagefs.go:369` iterates `stageDirectory(namespace)`; `storageminio/backend.go:397` lists
`stagePrefix(namespace)`, which ends in `/` (`backend.go:760`), so `stage/o-…dee/` cannot match
`stage/o-…def/`. Object names are `prefix/namespace/key` (`checkedJoin`, `backend.go:780`) — the
namespace is a whole path segment.

**End-to-end on `storagefs`, two tenants, one logical key:**

```
acme reads "acme-secret" back from the key both tenants wrote
acme's delete leaves globex's object in place
acme promoting globex's stage id: storage promote: not_found
acme aborting globex's stage id returns <nil> — an unknown id is not an existence oracle
after acme's Abort and CleanupExpired (removed 0), globex still owns its stage: promote err=<nil> size=13
physical namespaces: objects/objects-1fe851ae…, objects-72504fae…, objects-836c1754…  (acme-x, acme, globex)
```

**`tenancycache.Partition` — is `partition||key` concatenated? No.** `cache/address.go:112-126`
hashes the partition on its own (`key.go:122` `sha256.Sum256(raw)`) and stores it as a separate
`Address` field. The classic ambiguity pair proves it:

```
(ab,c)  ns=dade4f06 partition=fb8e20fc2e4c3f24 key=8b953848dbdef3d5
(a,bc)  ns=dade4f06 partition=ca978112ca1bbdca key=2976048eb19ff480
two tenants with one logical key share KeyDigest and differ only in PartitionDigest
an empty partition is refused: cache: partition key: … partition has 0 encoded bytes
```

The partition floor fails closed, and the refusal keeps the tenancy sentinel (`opaqueError.Is` at
`cache/errors.go` chases the cause):

```
budget=  8 -> 0 bytes  err=tenancy: tenant binding is not compatible with this capability: forbidden
budget= 15 -> 0 bytes  err=…ErrIncompatible
budget= 16 -> 16 bytes; budget=32 -> 32; budget=64 -> 32 (capped)
logical key of 16368 bytes -> Put: <nil>
logical key of 16369 bytes -> Put: cache: partition key: … (errors.Is(err, tenancy.ErrIncompatible)=true)
```

**Invalidation, batch, single-flight — all honour the partition.** `Forget` → `transientAddress` →
`addressOf` (`mutation.go:212`); `LookupMany` → `batchAddresses` → `addressOf` (`lookup.go:263`);
the flight/coordination map is `map[Address]*addressState` (`cache.go:51`), so the dedup key
includes `PartitionDigest`. UC-63, under `-race`, four concurrent goroutines on one logical key:

```
acme reads "acme-secret" (Hit), globex reads "globex-secret" (Hit)
Forget by acme leaves globex's entry intact
acme's batch lookup of the key globex populated: state=Miss
loader ran 1 time(s) for acme and 1 time(s) for globex
values = ["acme-value" "acme-value" "globex-value" "globex-value"]
generation 2 reading generation 1's key: state=Miss
```

**`Key[K]` hashing/comparison/serialisation.** Scope is included in `==` and in Go map keys
(2 distinct entries for one logical key). `String()`/`fmt.Sprint` redact to `"[tenant cache key]"`.
The reflective codec **refuses** the type outright, so no consumer can accidentally build a
scope-blind codec that way:

```
cache.StructKey[tenancycache.Key[string]] = cache: build struct key: invalid…: field scope needs a stable cachekey tag
```

**`record(namespace, definition)` vs `Sealer.mac`.** No two pairs can produce the same MAC input.
`jobs.Namespace.Digest()` is a fixed 32 bytes (`scope.go:151`); `mac` seals the field count and
length-prefixes each field (`seal.go:187-198` at snapshot time). M13 (concatenating the two fields)
survives *because the property does not depend on it* — I verified that, rather than assuming it.
Replay across queue and definition is refused with `tenancy.ErrUntrusted` (existing test, killed by
M9).

**Epoch check ordering in `RestoreIdentity`.** No window. `Authority.Lookup` (`authority.go:173`) →
`mint` (`authority.go:187`) is pure: it allocates a `Scope` and has no side effect. The generation
comparison (`jobs.go:92`) happens before `With` and before the value escapes. The residual window is
*after* the check, and it is a design choice, not a defect — see GAP-13.

**A lying restorer cannot substitute a tenant.** `RestoredIdentity.validFor`
(`durable_context.go:497-505`) recomputes `partitionKey(namespace, tenant)` and compares it with the
record's. Confirmed: `substitution refused: jobs: driver operation failed`.

**A record flipped to `ContextSystem` cannot run a tenant handler.** `checkDeliveryCompatibility`
(`delivery_record.go:325-327`) refuses when `wantsTenant == record.Genesis.Partition.Global()`. This
matters because `digestDurableContext` (`durable_context.go:822`) is an **unkeyed** SHA-256 — a
queue-table writer can recompute it — so the definition/partition cross-check is the thing standing
between a DB writer and an unbound execution. It holds.

**UC-58 both ends.** `jobs.NewQueue` refuses a catalog that requires a tenant partition with no
context provider (`queue.go:89`); `resolveWorkersConfig` requires an identity restorer
**unconditionally** (`workers_config.go:203`).

**Cross-seam imports.** Zero, for all three, verified by `go list -deps`; `scripts/tenancy_test.go`
covers all five adapters and I proved it fails when violated (M23).

## Findings

### GAP-1 [high][immediate] The two functions a composition root actually calls are untested, and either one can be neutered into total cross-tenant collapse with the suite green

- **Where:** `tenancy/tenancycache/cache.go:56-58` (`Partitioned`), `tenancy/tenancystorage/storage.go:28-34` (`Store`)
- **Scale:** local (2 functions), but each is the single point every consumer wires
- **Confidence:** CONFIRMED —
  `M17 cache/cache.go:57 Partitioned returns a Global scope` → `SURVIVED`
  `M20 storage/storage.go:33 Store puts every tenant in one namespace` → `SURVIVED`
  (`go test ./tenancy/tenancycache/ ./tenancy/tenancystorage/` green under both)
- **What / Why this severity:** `tenancycache/cache_test.go` calls `Partition[string]()` directly and
  never `Partitioned`; `tenancystorage/storage_test.go` calls `namespaceOf` and `Namespace` and never
  `Store`. So the two functions that convert a correct primitive into a correct *composition* are
  unpinned. A refactor that drops the partitioner or hardcodes the namespace ships green. The
  observable failure: acme's `Put(key,"acme-secret")` followed by globex's `Lookup(key)` returns
  `"acme-secret"` — I ran exactly that against `cache.Global` (GAP-2) and it hit.
- **Why this timing:** structural. Any future change to either function has no safety net today, and
  every fix listed below touches one of them.
- **Close criteria:**
  - [ ] a test that builds a `cache.Cache` through `tenancycache.Partitioned` with a real backend, writes for A, reads for B and asserts a miss — and that fails under M17
  - [ ] a test that builds two `storage.Store`s through `tenancystorage.Store` on one backend and round-trips the same key for two tenants — and that fails under M20
  - [ ] both listed under the plan's mutation-evidence table
- **Status:** open

### GAP-2 [high][immediate] `cache.Global` over a tenant-owned key compiles, runs and serves one tenant's value to another

- **Where:** `cache/key.go:89` (`Global`), `tenancy/tenancycache/cache.go:17-20` (`Key[K]`), `cache/declaration.go:25` (`GlobalPlan`)
- **Scale:** local (one wiring shape, two reachable APIs: `cache.New` and `cache.Define`)
- **Confidence:** CONFIRMED —
  ```
  globex reading with a Global scope: state=Hit value="acme-secret"
  ```
- **What / Why this severity:** `Key[K]` is a plain type parameter to `cache.Global[K]`; nothing in
  the cache or the adapter objects. A composition root that writes
  `cache.Global[tenancycache.Key[Invoice]](ns)` instead of `tenancycache.Partitioned[Invoice](ns)` —
  one word — type-checks, activates, and every tenant shares one address space for every key. This is
  the exact inverse of `Partition`'s zero-scope guard, which fails loudly. The guard exists at the
  wrong layer.
- **Why this timing:** it is a public-contract shape. Closing it later means changing how a
  `Scope[K]` is constructed, which is a breaking change for every consumer.
- **Close criteria:**
  - [ ] `cache.Global` refuses (compile-time via a marker interface, or at `New`/`Define`) a key type that carries a tenancy scope, **or** `tenancycache` documents the hazard in `docs/modules/{en,ru}/tenancy.md` and a decision doc records why it cannot be prevented
  - [ ] a control test asserting the leak exists without the partitioner, next to the positive test (the `gate_relscope_test.go` pattern this project already mandates)
- **Status:** open

### GAP-3 [high][immediate] The admission check has the lifetime of the returned handle, and nothing bounds that lifetime

- **Where:** `tenancy/tenancystorage/storage.go:28-34` (`Store`), `tenancy/tenancycache/cache.go:25-31` (`Keyed`) / `:43-54` (`Partition` checks only `IsZero`)
- **Scale:** systemic across two of the three adapters (`tenancydb.Borrow` re-asks per borrow; `tenancyrow` re-asks per verb — these two are the outliers)
- **Confidence:** CONFIRMED, with `Spec.Revalidate: true` —
  ```
  a NEW cache key for the deleted tenant is refused: tenancy: tenant lifecycle does not admit this work: forbidden
  the key that was already in hand still partitions: 32 bytes, err=<nil>
  the store that was already in hand still writes: err=<nil>
  ```
- **What / Why this severity:** the seam comment at `storage.go:17-19` argues the check belongs at
  the point of use rather than at the bind — but "the point of use" turns out to be *handle
  construction*, once. A request handler that obtains a `storage.Store` at the top and writes at the
  bottom will write for a tenant deleted in between. UC-65 states "suspension makes the data
  unreachable through the seam" and UC-53/54 state "no tenant side effect occurs" after
  suspension/deletion; neither holds for a handle already in hand, and `Revalidate` does not help
  because it is consulted inside `Authority.Scope`, which the returned `storage.Store` never calls
  again.
- **Why this timing:** it is a contract property of a value the seam hands out. Fixing it later
  changes the returned type (a leased store, or a store that re-checks per verb), which is a breaking
  change.
- **Close criteria:**
  - [ ] the validity window of a `storage.Store` and a `tenancycache.Key[K]` is stated in `docs/modules/{en,ru}/tenancy.md` in one sentence ("valid for the unit of work that built it")
  - [ ] or `Store` returns a store that re-resolves the class per verb (a `storage.Middleware`, which `storage.Chain` already supports at `store.go:27`)
  - [ ] a test: build the handle, delete the tenant, assert the next write refuses (currently it succeeds)
- **Status:** open

### GAP-4 [high][immediate] A system-scoped job inherits whatever tenancy scope the worker's base context carries

- **Where:** `tenancy/tenancyjobs/jobs.go:77-79`; `jobs/worker_delivery.go:110` passes the worker's runtime `ctx`; `tenancy` has no API to strip a scope (only `With`, `context.go:23`)
- **Scale:** local (one branch), systemic in blast radius (every `PartitionGlobal` definition in the deployment)
- **Confidence:** CONFIRMED —
  ```
  a system-scoped job runs bound to "acme" because the worker context was bound
  and a tenancy seam called from the handler answers a full write scope for that tenant
  ```
- **What / Why this severity:** `RestoreIdentity` returns `jobs.NewRestoredIdentity(ctx, …)` unchanged
  for `ContextSystem`. If the worker's base context ever carries a scope — an in-process worker
  started under a bound request, an integration harness, a `runtime` graph whose root context was
  bound — every global job runs as that tenant, and a tenancy seam inside its handler answers a full
  `ClassWrite` scope. The tenant-scoped path fails closed here (`With` returns `ErrPinned` for a
  different tenant); the system-scoped path fails **open**. Note the asymmetry is invisible: the
  record is correct, the restorer is correct, the leak is in what was already in the context.
- **Why this timing:** it needs an API in the core (`tenancy.Without(ctx)` or an unbound-context
  constructor); adding that later means changing the adapter's contract with `jobs`.
- **Close criteria:**
  - [ ] `RestoreIdentity` returns a context provably free of any tenancy scope for `request.Scope() != jobs.ContextTenant`
  - [ ] a test that binds the worker context to A, restores a global record, and asserts `tenancy.From(restored)` is `false` (currently `true`)
  - [ ] M22 (`never take the unbound branch`) is killed by that test
- **Status:** open

### GAP-5 [high][immediate] `cache.TagInvalidator` is a namespace-wide bulk invalidation with no partition, which UC-64 explicitly forbids

- **Where:** `cache/capability.go:84-86`, `cache/capability.go:112` (`TagInvalidatorOf`), `cache/capability.go:22` (advertised as `tag_invalidation`)
- **Scale:** local (one interface), but it is public API and capability-discoverable
- **Confidence:** CONFIRMED for the contract; PLAUSIBLE for a shipped exploit (no in-tree backend implements it) —
  ```
  InvalidateTag(ctx, Namespace, Tag) accepted and recorded [tags/customer:42]
  and the backend advertises tag_invalidation, so a consumer will find and use it
  rg -n 'InvalidateTag|TagInvalidatorOf' --type go  ->  only cache/capability.go and cache/capability_test.go
  ```
- **What / Why this severity:** UC-64 requires "a bulk or pattern invalidation with no partition is
  refused or narrowed, never executed as written". `InvalidateTag` takes a `Namespace` and a `Tag`
  and no partition; nothing in `cache/` narrows it and no tenancy adapter wraps it. A deployment on a
  Redis-shaped backend that implements the capability and calls
  `cache.TagInvalidatorOf(backend).InvalidateTag(ctx, ns, tag)` wipes every tenant's tagged entries in
  that namespace. UC-64's own reasoning applies: "its blast radius is the same shape as the read
  leak". The same holds, more weakly, for `cache.Transactional.InTransaction` (`capability.go:88`),
  which hands the consumer a raw `Backend` and `Address` has exported fields.
- **Why this timing:** it is a published contract in the seam the adapter claims to partition. Adding
  a partition parameter later is breaking.
- **Close criteria:**
  - [ ] `InvalidateTag` carries a partition (or a documented "global by construction, never for tenant-partitioned caches" note in `docs/modules/{en,ru}/cache.md` and the tenancy module doc)
  - [ ] `tenancycache` either provides a partition-narrowed invalidator or the tenancy doc names tag invalidation as out of the seam's guarantee
  - [ ] a test asserting an unpartitioned pattern invalidation is refused, per UC-64's acceptance
- **Status:** open

### GAP-6 [medium][immediate] The jobs partition and intent key ignore the generation, unlike the storage namespace and the cache partition

- **Where:** `jobs/scope.go:286-300` (`partitionKey` hashes namespace + `partition.value` only), `tenancy/tenancyjobs/jobs.go:40` passes `scope.Reference().Value()` with no epoch
- **Scale:** local (one function), affects every restored tenant
- **Confidence:** CONFIRMED —
  ```
  generation 1 and generation 2 share the job partition db9c36a4ddccbe45
  and the same payload in both generations produces the same intent digest,
  so a once/collapse key crosses the restore
  ```
- **What / Why this severity:** `docs/modules/en/tenancy.md:246` says "The generation is inside the
  digest, so a restored tenant reads none of the previous generation's objects or cached values" —
  and that is true for storage and cache, proven. For jobs it is not: the queue partition and the
  `IntentKey` scope binding (`jobs/scope.go` `intentScopeBinding`) are keyed on the reference alone.
  Concrete consequence: tenant A is restored to generation 2; an `IntentOnce` job enqueued by
  generation 1 is still in the intent table, so generation 2's identical enqueue collapses onto a
  record that will never run (it fails the epoch check at restore). The tenant's post-restore work
  silently does not happen. The *execution* fence holds (this is not a leak); the *placement* fence
  does not.
- **Why this timing:** the partition string is what the durable record commits to. Changing it later
  is a queue migration.
- **Close criteria:**
  - [ ] a decision doc records why the jobs partition is generation-agnostic while the other two are not, **or** the partition carries the epoch
  - [ ] `docs/modules/{en,ru}/tenancy.md` states the difference in the sentence that currently claims all three
  - [ ] a test asserting the intended behaviour for a once-job across a restore
- **Status:** open

### GAP-7 [medium][immediate] The adapter's four typed refusals are flattened to one untyped `ErrDriver`, so an operator cannot tell "tenant deleted" from "control plane down"

- **Where:** `jobs/durable_context.go:540,544` (`return nil, ErrDriver` discards `restoreErr`); `jobs/worker_delivery.go:115` (`DeferDeliveryCommand(…, ReasonDependency, …)`)
- **Scale:** local (one collapse point), affects all four refusal classes
- **Confidence:** CONFIRMED by code and by output —
  ```
  a suspended tenant whose deployment still admits READS: restore = jobs: driver operation failed
  substitution refused: jobs: driver operation failed
  ```
- **What / Why this severity:** `tenancyjobs.RestoreIdentity` returns `ErrInactive` (suspended),
  `ErrUnmapped` (deleted), `ErrStale` (generation moved) and `ErrUntrusted` (forged) — four different
  operational meanings with four different correct responses. `RestoreTrustedIdentity` throws all of
  them away. The worker then treats every one as a transient dependency failure and defers with
  backoff until `MaxDeliveryDeferrals` or `MaxElapsed` (`jobs/invocation.go:554`, `jobs/profile.go:17`)
  marks the invocation `InvocationDead` with `ReasonDeferralsExhausted`. So UC-53/54's "reaches a
  terminal state, does not retry forever" holds, but the terminal reason names the wrong cause, and a
  genuinely forged record is indistinguishable from a control-plane outage in the operator's view.
  Note the existing test at `durable_test.go:198-201` already documents this collapse for the producer
  side as deliberate; the consequence on the worker side is not documented anywhere.
- **Why this timing:** it is the observability contract other sections will build dashboards and
  runbooks on (UC-66..77 are the deferred operator procedures).
- **Close criteria:**
  - [ ] a mechanism for a restorer to signal "refuse permanently" vs "retry" (a sentinel `jobs` recognises), or a written decision that it may not
  - [ ] `docs/modules/en/tenancy.md` states what an operator sees when a deleted tenant's backlog drains
  - [ ] a test pinning the terminal reason for each of the four refusals
- **Status:** open

### GAP-8 [medium][immediate] `digestBytes = 16`, `MinPartitionBytes = 16` and `MaxPrefixBytes = 30` are uncalibrated magic constants with no static guard tying them to the namespace budget

- **Where:** `tenancy/tenancystorage/storage.go:13-15`, `tenancy/tenancycache/cache.go:11-12`
- **Scale:** local (5 constants across two files)
- **Confidence:** CONFIRMED —
  ```
  scope digest is 32 bytes, namespace carries 16 bytes (32 hex chars)
  namespace "o-72504fae4da7e7a26508f7fa75e0ed7f" has 34 chars; storage allows 63;
    unused budget with a 1-char prefix = 29 chars = 14 more digest bytes
  longest namespace: prefix 30 + separator 1 + hex 32 = 63
  M4 digest 16 -> 4 bytes   SURVIVED
  M16 digest 16 -> 8 bytes  SURVIVED
  M14 digest 16 -> 24 bytes SURVIVED   (30 + 1 + 48 = 79 > 63: a 30-char prefix now fails at runtime)
  M15 MaxPrefixBytes 30 -> 60 SURVIVED (a 40-char prefix now fails at runtime)
  ```
  and the mechanism, demonstrated at a deliberately narrowed width:
  ```
  with a 3-byte truncation, "t879" and "t3594" share the physical namespace "objects-116402" after 3594 references
  and storage.ParseNamespace accepts it
  ```
- **What / Why this severity:** the comment at `storage.go:36-41` justifies *using a digest* but says
  nothing about *how wide*. At 16 bytes a deliberate collision costs ~2^64 offline work on
  attacker-chosen references (a signup slug), after which two tenants share one physical bucket
  namespace — full read and overwrite. The seam throws away 14 bytes of available budget to buy
  nothing. Separately, `MaxPrefixBytes = 30` silently *is* `(63 − 1 − 2·digestBytes)`; nothing
  expresses that, so either constant can be changed independently into a configuration that only
  fails for long prefixes at runtime, and the test suite will not notice (M14, M15). The cache
  constant has the better comment (`cache.go:37-42`) but the same absence of a derivation.
- **Why this timing:** `universality.md` — an uncalibrated magic threshold is never deferred, and this
  one sets the security margin of the whole object seam.
- **Close criteria:**
  - [ ] a calibration note next to `digestBytes` and `MinPartitionBytes`: the collision model, the assumed number of tenants, whether references are attacker-chosen, and how to widen
  - [ ] `MaxPrefixBytes` expressed as an expression over `digestBytes` and the storage namespace limit, with a compile-time or `TestMain` assertion
  - [ ] a test that exercises `namespaceOf(strings.Repeat("p", MaxPrefixBytes), scope)` **successfully** (today only the +1 failure case is tested) — this alone kills M14 and M15
- **Status:** open

### GAP-9 [medium][immediate] `tenancystorage.Namespace` answers in two unrelated error taxonomies depending on which prefix rule fails

- **Where:** `tenancy/tenancystorage/storage.go:46-48` (returns `tenancy.ErrMalformed`) vs `:50` (returns whatever `storage.ParseNamespace` returns)
- **Scale:** local
- **Confidence:** CONFIRMED —
  ```
  prefix=31×"p"     -> tenancy: an object namespace prefix is 1..30 bytes: … crud: bad request
  prefix="Objects"  -> storage parse namespace: invalid
  prefix="obj/ects" -> storage parse namespace: invalid
  ```
- **What / Why this severity:** a caller writing `errors.Is(err, tenancy.ErrMalformed)` catches a
  too-long prefix and misses an upper-case one. Every other refusal from this seam is a `tenancy`
  sentinel wrapping a `crud` error kind (`tenancy/errors.go:10-31`), and `tenancy.Classify` exists
  precisely so a seam presents one channel. The cache seam gets this right (`cache/errors.go`
  `opaqueError.Is` chases the cause, so `errors.Is(err, tenancy.ErrIncompatible)` holds — I verified).
  Building blocks: the provider owns its error contract; here two providers own half each.
- **Why this timing:** it is the seam's public error contract.
- **Close criteria:**
  - [ ] `namespaceOf` validates the prefix itself and returns `tenancy.ErrMalformed` for every malformed prefix, or wraps the storage error in it
  - [ ] a test asserting `errors.Is(err, tenancy.ErrMalformed)` for each rejected prefix class (case, separator, whitespace, non-ASCII, length)
- **Status:** open

### GAP-10 [medium][immediate] Nothing pins the class the worker re-derives under; switching it lets a suspended tenant's queued work run

- **Where:** `tenancy/tenancyjobs/jobs.go:88`
- **Scale:** local
- **Confidence:** CONFIRMED at the 00:40 snapshot —
  `M11 re-derive under ClassRead instead of ClassDurable` → `SURVIVED`, and the exploit is real on the
  unmutated code:
  ```
  a suspended tenant whose deployment still admits READS: restore = jobs: driver operation failed
  the same tenant under ClassRead mints fine: err=<nil> zero=false
  ```
- **What / Why this severity:** `TestAProducerRefusesToCaptureAScopeTheDurableClassDoesNotAdmit` pins
  the class on the **producer** side only. On a deployment with `Admit(ClassRead, Active, Suspended)`
  and `Admit(ClassDurable, Active)` — a shape the core explicitly supports and `lifecycle.go:105-115`
  deliberately permits for durable work — flipping this one argument runs a suspended tenant's backlog
  and the suite stays green. That is the "we kept billing them after they asked us to stop" failure
  UC-53 names.
- **Why this timing:** it is the guard that makes UC-53/54 true at execution, and nothing held it at
  the snapshot.
- **⚠ RE-CHECK BEFORE ACTING.** After my snapshot, the concurrent session changed `tenancy/seal.go`
  (mtime **00:43:18**): `Sealer.Seal` now calls `authority.accept(scope, ClassDurable)` (`seal.go:50`)
  and `Sealer.mac` now covers `origin` (`seal.go:94`), and `tenancy/seal_test.go` (mtime **00:42:39**)
  now declares `TestOnlyAScopeTheDurableClassAdmitsIsSealed` (`seal_test.go:103`). That new check sits
  on the **producing/sealing** path; `Unseal` returns a reference rather than a scope, so on the face
  of it the **worker** path (`tenancyjobs/jobs.go:88`) is still the only place that chooses
  `ClassDurable` at execution. **I did not re-run M11 against the post-00:43 tree.** Re-run it before
  anyone opens or closes this gap.
- **Close criteria:**
  - [ ] M11 re-run against the current tree, result recorded
  - [ ] a test with an admission that separates `ClassRead` from `ClassDurable`, asserting the *restore* refuses — and that fails under M11
- **Status:** open (evidence dated 00:40; re-check required)

### GAP-11 [medium][deferred] The `ContextSystem` branch of `RestoreIdentity` has no test at all

- **Where:** `tenancy/tenancyjobs/jobs.go:77-79`
- **Scale:** local
- **Confidence:** CONFIRMED — `M22 never take the unbound branch` → `SURVIVED`. Under M22 every
  `PartitionGlobal` job is refused with `ErrUntrusted` and no test notices.
- **What / Why this severity:** a deployment that mixes tenant-scoped and global definitions on one
  worker (the normal case) would lose all global work on this regression. The correct behaviour is
  proven by my probe, not by the suite:
  ```
  record scope = system, partition global = true
  record carries NO token: nothing ties this row to a tenant
  handler context carries a scope: false
  a tenancy seam called from this handler answers: tenancy: no verified tenant scope: forbidden
  ```
- **Why this timing:** module-internal coverage; no external contract changes.
- **Close criteria:**
  - [ ] a test enqueueing a `PartitionGlobal` definition from a bound context, restoring it, and asserting both that the handler runs and that `authority.Scope` refuses inside it
- **Status:** open

### GAP-12 [medium][deferred] The cache partition addresses entries but is not an eviction domain; one tenant evicts every other tenant's entries

- **Where:** `tenancy/tenancycache/cache.go:37-42` (the comment implies otherwise), `cache/cachememory/backend.go:68` (one `map[cache.Address]*entry` under one global `MaxEntries`/`MaxBytes`)
- **Scale:** local (one comment), systemic in effect (every process-local cache)
- **Confidence:** CONFIRMED —
  ```
  after acme wrote 32 entries into an 8-entry backend, globex's only entry is: Miss
  ```
- **What / Why this severity:** the seam's comment says a too-narrow partition means "a tenant sharing
  an eviction domain with whoever else lands on its prefix, which is worse than refusing the
  operation" — the reader concludes a wide-enough partition buys an eviction domain. It does not:
  `PartitionDigest` is an address component, and capacity is global. A noisy tenant cold-starts every
  other tenant. Not a leak; a cross-tenant availability coupling that the documentation implies does
  not exist.
- **Why this timing:** comment/doc accuracy; no contract change.
- **Close criteria:**
  - [ ] the comment is corrected, or `docs/modules/{en,ru}/tenancy.md` states that a partition is an address, not a budget
- **Status:** open

### GAP-13 [medium][deferred] UC-57: the adapter re-derives authority per attempt and guarantees nothing else, and neither half of that is written down

- **Where:** `tenancy/tenancyjobs/jobs.go:76-100`
- **Scale:** local
- **Confidence:** CONFIRMED —
  ```
  attempt 0 restores "acme" after 1 control-plane calls
  attempt 1 restores "acme" after 2 control-plane calls
  attempt 2 restores "acme" after 3 control-plane calls
  3 deliveries of one record cost 3 control-plane lookups
  ```
  and, for a handler that spans a restore:
  ```
  Revalidate=false: the handler keeps writing to generation 1 after the restore to generation 2
  Revalidate=true:  the second half of the handler is refused mid-flight: tenancy: ErrStale (the first half is already applied)
  ```
- **What / Why this severity:** UC-57 requires "authority is re-derived from scratch for the retry" —
  satisfied, and I measured it, but there is no test asserting the lookup count (UC-52's acceptance
  asks for exactly that counter). UC-57 also requires "the job's effects are idempotent or explicitly
  transactional per attempt, so a retry never doubles a side effect nor lands a second half against a
  different generation than the first half". The adapter provides no attempt fence, no idempotency key
  and no compensation, and hands the handler nothing that would let it build one. The two `Revalidate`
  settings give two different, both-undocumented answers: pinned (all halves land in the *previous*
  generation's namespace — i.e. into a bucket namespace the restored tenant will never read) or
  refused mid-flight (partial application with no compensation).
- **Why this timing:** documentation and coverage; no contract change required to close.
- **Close criteria:**
  - [ ] `docs/modules/{en,ru}/tenancy.md` states, for both `Revalidate` settings, what a handler that spans a restore does
  - [ ] a test with an instrumented resolver asserting one lookup per delivery (UC-52's acceptance)
  - [ ] UC-57 is either covered by a test or moved to the plan's `## Debt` with a reason
- **Status:** open

### GAP-14 [medium][deferred] The plan claims S3 covers UC-51..65; seven of those fifteen have no test in these three packages

- **Where:** `.agents/artifacts/plans/TENANCY_PLAN.md:326` ("**Covers** UC-51..65; INV-14, 18, 19"), `:653` (coverage matrix, `S3 | 51–65`)
- **Scale:** systemic (7 use cases)
- **Confidence:** CONFIRMED by reading every test in the three packages
- **What / Why this severity:** uncovered: **UC-57** (retry after partial application — GAP-13);
  **UC-58** worker-side (`workers_config.go:203` enforces it, but no tenancy test); **UC-60's
  acceptance** ("a context that contains a tenant-shaped value the partition mapper was not given" —
  structurally impossible since `Partitioner` takes no `ctx`, which is a *better* answer than a test,
  but it is unstated); **UC-61's adversarial classes** (the existing test uses one prefix `"objects"`
  and references `"tenant-"+"x"*k+i` — no unicode-normalisation variants, no case variants, no
  separator inside a logical name, and no round-trip through a store, all of which the acceptance
  names); **UC-62** (no `List` exists on `storage.Store` — vacuously true, but nothing asserts the
  absence, so adding a `List` later silently breaks it); **UC-63** (holds — I proved it under `-race`
  — but the property is untested); **UC-64** (violated — GAP-5). The claim is a green marker over
  seven open items.
- **Why this timing:** coverage honesty; no code contract changes.
- **Close criteria:**
  - [ ] each of the seven either gets a test or moves to `## Debt` with a reason
  - [ ] the coverage matrix stops asserting a range and names the covered use cases
- **Status:** open

### GAP-15 [low][deferred] `tenancycache` and `tenancyjobs` have no end-to-end wiring anywhere, and `tenancycache` covers only one of the cache's two construction paths

- **Where:** `_examples/tenancy-sharedrow/main.go:96` (wires `tenancystorage.Namespace` only); `tenancy/tenancycache/cache.go:56` returns a `cache.Scope[K]` for `cache.New` (`cache/cache.go:69`) but there is no `ScopePlan` helper for the declarative `cache.Define` path (`cache/declaration.go:43`)
- **Scale:** local
- **Confidence:** CONFIRMED — `rg -n 'tenancycache|tenancyjobs' _examples/` returns nothing
- **What:** a consumer on the declarative path must write
  `cache.PartitionedPlan(tenancycache.Partition[K]())` themselves; that is exactly the moment GAP-2's
  `GlobalPlan` mistake becomes available. The absence of an example is also why GAP-1 went unnoticed.
- **Why this timing:** DX and coverage; no contract change.
- **Close criteria:**
  - [ ] the example (or a usage guide) wires all three seams, including `tenancycache` through both cache construction paths
- **Status:** open

### GAP-16 [medium][immediate] `go test ./scripts/...` was red on doc drift in D-117 — observed 00:42, closed 00:47 by the concurrent session

- **Where:** `docs/ai/decisions/D-117-a-verified-scope-is-minted-never-manufactured.md:148,151`
- **Scale:** local (2 citations)
- **Confidence at 00:42:** CONFIRMED — I ran it:
  ```
  --- FAIL: TestEveryTestNameTheDocsCiteExists
      TestARecordSealedInAnotherDeploymentIsNotRead is cited by …D-117…:151 and no _test.go declares it
      TestOnlyAScopeTheDurableClassAdmitsIsSealed  is cited by …D-117…:148 and no _test.go declares it
  ```
  At that moment `tenancy/seal_test.go` declared `TestARecordAnotherDeploymentSealedIsNotRead` (:142)
  and `TestOnlyAScopeThisAuthorityMintedIsSealed` (:87), and no test asserted "only a scope the durable
  class admits is sealed". `D-117` had mtime `2026-09-06 00:35:35` — it was edited *during* the audit.
- **Resolution at 00:47:** **closed by the concurrent session, not by me.** The coordinator reports
  `go test ./scripts/...` green as of 00:47, and I verified read-only that
  `tenancy/seal_test.go` now declares `TestOnlyAScopeTheDurableClassAdmitsIsSealed` (**:103**) and
  `TestARecordSealedInAnotherDeploymentIsNotRead` (**:175**); mtimes `seal_test.go` 00:42:39,
  `seal.go` 00:43:18. Alongside them, `Sealer.Seal` now calls `authority.accept(scope, ClassDurable)`
  (`seal.go:50`) and `Sealer.mac` now covers `origin` (`seal.go:94`).
- **Why this is recorded rather than deleted:** it is the concrete proof that this audit ran against a
  moving tree, and it dates the boundary. Every mutation result and every experiment in this document
  predates `seal.go`'s 00:43:18 change. The `origin` addition to `mac` strengthens the framing
  conclusion in "What I probed and cleared" (more sealed input, same length-prefixing); the
  `accept(scope, ClassDurable)` addition is on the producing path and is the reason GAP-10 carries a
  re-check flag.
- **Close criteria:**
  - [x] the two citations name existing tests (`seal_test.go:103`, `:175`)
  - [x] `go test ./scripts/...` green (reported by the coordinator at 00:47; not re-run by me)
- **Status:** fixed by the concurrent session at ~00:43–00:47

## Remediation order

Boundaries and contracts before internals; the two test gaps that would let any of the fixes regress
come first.

1. **GAP-1** — tests for `Partitioned` and `Store`. **S**, blast radius nil. Everything below changes
   one of those two functions; without these tests each fix is unverifiable. Do this first even though
   it is the smallest item.
2. **GAP-10** (after its re-check) + **GAP-11** — tests for the class argument and the `ContextSystem`
   branch. **S**, blast radius nil. Same reason, jobs side.
3. **GAP-4** — an unbound-context path for system-scoped jobs. **M**, blast radius: `tenancy` core
   gains one function; `tenancyjobs` changes one branch. Must precede any further jobs work because it
   changes what the adapter promises `jobs`.
4. **GAP-3** — decide and state the handle validity window; if the answer is "re-check per verb", it
   changes the return type of `tenancystorage.Store`. **M–L**, blast radius: every consumer of `Store`.
   Do it before consumers exist (today there are none — GAP-15 — which is the cheapest this will ever
   be).
5. **GAP-2** — refuse or document `cache.Global` over a tenant key. **M**, blast radius:
   `cache/key.go` public constructors. Do it with GAP-4/GAP-3 while the seam has no external consumers.
6. **GAP-5** — partition or explicitly exclude `InvalidateTag`. **M**, blast radius: `cache` public
   capability interface. Breaking after the first tag-invalidating backend ships.
7. **GAP-8** — calibration notes and the `MaxPrefixBytes`/`digestBytes` guard, plus the boundary test.
   **S**, blast radius nil if the width stays at 16; **L** and a data migration if it changes, which is
   the argument for deciding now.
8. **GAP-6** — decide whether the jobs partition carries the epoch. **L** if yes (queue migration),
   **S** if the answer is "no, and here is why". Decide before a deployment has a queue.
9. **GAP-9** — one error taxonomy out of `tenancystorage`. **S**, blast radius: callers matching on
   the error.
10. **GAP-7** — a permanent-refusal channel from restorer to worker. **M**, blast radius: `jobs`
    public contract. Depends on nothing above; can run in parallel.
11. **GAP-13**, **GAP-14** — documentation and coverage honesty. **S** each.
12. **GAP-12**, **GAP-15** — comment correction and example wiring. **S** each.

## What I did not check

- **`tenancyrow` and `tenancydb`.** Out of scope by instruction. GAP-3's handle-lifetime finding is
  stated as an *outlier* relative to them based on reading `docs/modules/en/tenancy.md:262-268` and
  `context.go`, not on testing them.
- **Anything in `tenancy/seal.go` or `tenancy/seal_test.go` after 00:40.** My snapshot predates the
  `accept(scope, ClassDurable)` and `mac`-covers-`origin` changes. M9, M10, M12 and the
  `record`/`mac` framing analysis were run against the pre-00:43 `seal.go`. See GAP-10 and GAP-16.
- **`jobspg` / `jobsredis` / `storageminio` / any Redis cache backend against a live server.** No
  Docker was used; `make integration` was not run. The minio finding is from
  `objectName`/`stagePrefix`/`checkedJoin` executed directly, not against a bucket.
  `test/integration/tenancy_test.go` was not executed.
- **Concurrency at scale.** UC-63's acceptance asks for "hundreds of goroutines across many tenants";
  I ran four goroutines over two tenants under the loader-blocking pattern. I did not run `-race`
  across the whole suite.
- **The 2^64 collision claim in GAP-8 is arithmetic, not a demonstration.** I demonstrated the
  mechanism at a 3-byte truncation (3594 references) and reasoned to 16 bytes; I did not attempt a
  2^64 search.
- **Whether `digestDurableContext`'s unkeyed digest is exploitable end to end** through a live
  `jobspg` queue. I established that `checkDeliveryCompatibility` (`delivery_record.go:325`) refuses
  the scope-flip by reading the code and the descriptor path; I did not drive a forged row through a
  real worker.
- **`cache.Transactional.InTransaction`** as a cross-partition write vector: noted from the signature,
  not probed.
- **The `_examples` and `test` modules**, `docs/ai/flows/FL-033`, and the ru/en doc parity for the
  seam paragraphs.
- **Whether the tree changed again after 00:47.** A second, independent Claude Code session was
  editing it throughout; every result above was reproduced against a snapshot verified byte-identical
  at 00:40, and the two post-snapshot citations in GAP-16 were confirmed read-only at 00:47.

---

## Addendum — Round 2 re-check against the moving tree (2026-09-06 11:56)

**Why this exists.** The findings above are dated to a snapshot verified byte-identical at **00:40**.
A background-task notification pulled me back to this artifact, and a check showed the working tree
had moved again: the concurrent Claude Code session edited `tenancy/tenancystorage/storage.go`
(mtime **00:47:16**), `tenancy/tenancystorage/storage_test.go` (**00:51:08**),
`tenancy/tenancyjobs/durable_test.go`, `tenancy/tenancyjobs/support_test.go`, `tenancy/grant.go`,
`tenancy/scope.go`, `tenancy/seal.go`, `tenancy/tenancyrow/row.go` and several test files. Leaving
this document asserting `Status: open` on gaps that are demonstrably closed would make it the kind of
stale artifact `CLAUDE.md` treats as a failing test.

**Nothing above was edited.** Findings accumulate per `gaps.md`. This addendum only records which of
them survive on the current tree, with fresh evidence.

**Method.** A second snapshot at `…/scratchpad/fw2`, verified byte-identical to the working tree by
`diff -r` at **11:55:56** (`fw2 SNAPSHOT VERIFIED IDENTICAL`), baseline `go test ./tenancy/...` green
in all six packages. Mutations re-applied to that snapshot with the same harness. The audited
repository was not modified: `git status --porcelain | grep -c '^ M'` = 38, and zero `zz_*` /
`*.mutbak` / `*.stash` files exist under `tenancy/`, `cache/`, `storage/` or `jobs/`.

### Mutation verdicts, then and now

| # | Mutation | 00:40 | 11:56 | Killed by |
|---|---|---|---|---|
| M4 | `storage.go` digest 16 → 4 bytes | SURVIVED | **KILLED** | `TestAPrefixIsRefusedInThisPackagesVocabularyOrLeavesRoomForTheDigest` |
| M16 | digest 16 → 8 bytes | SURVIVED | **KILLED** | same |
| M14 | digest 16 → 24 bytes | SURVIVED | **KILLED** | same |
| M15 | `MaxPrefixBytes` 30 → 60 | SURVIVED | **KILLED** | same |
| M6 | drop the `"-"` separator | SURVIVED | **KILLED** | same (+ `/over_the_budget`, `/empty`) |
| M11 | re-derive under `ClassRead` | SURVIVED | **KILLED** | `TestTheDurableClassIsAskedAgainWhenTheRecordIsRestored/suspended` |
| M22 | never take the `ContextSystem` branch | SURVIVED | **KILLED** | `TestWorkThatNeedsNoTenantRestoresWithNoneBound` |
| **M17** | **`Partitioned` returns `cache.Global`** | SURVIVED | **SURVIVED** | — |
| **M20** | **`Store` puts every tenant in one namespace** | SURVIVED | **SURVIVED** | — |
| M13 | concatenate the two binding fields | SURVIVED | SURVIVED | benign, as documented above |

Seven of ten survivors are now dead. Two of the three that remain are GAP-1.

### Status changes

- **GAP-9 — CLOSED.** `namespaceOf` (`tenancy/tenancystorage/storage.go:56-67`) now funnels every
  malformed-prefix rejection through one `tenancy.ErrMalformed`, and the new comment states the
  reason in the same terms this finding used: *"The refusal is answered in this package's vocabulary,
  because a caller that has to branch on two error taxonomies branches on neither."* Note a design
  consequence worth carrying forward: the explicit `len(prefix) > MaxPrefixBytes` check is gone, so
  `MaxPrefixBytes` is now documentation plus a test rather than an enforced constant — the new test
  is the only thing holding it, which is precisely why M14/M15 now die.
- **GAP-8 — MOSTLY CLOSED, residue reclassified `[low][deferred]`.** The "14 unused digest bytes"
  observation is answered: the budget is deliberately divided, and
  `TestAPrefixIsRefusedInThisPackagesVocabularyOrLeavesRoomForTheDigest` (`storage_test.go:90`) pins
  the division from both sides. What remains is only the calibration note for *why 128 bits is the
  right side of that trade* — sharpened, not weakened, by the new comment's own admission that the
  digest "is derived from a reference an attacker can guess".
- **GAP-10 — CLOSED.** This is the re-check the coordinator flagged.
  `TestTheDurableClassIsAskedAgainWhenTheRecordIsRestored` (`durable_test.go:288`) pins the class at
  **execution**, with a `/suspended` subtest, and kills M11.
- **GAP-11 — CLOSED.** `TestWorkThatNeedsNoTenantRestoresWithNoneBound` (`durable_test.go:165`)
  exercises the `ContextSystem` branch and kills M22.
- **GAP-16 — remains closed** (recorded above).

### Still open, re-verified on the current tree

- **GAP-1** — both halves. M17 and M20 still survive: `tenancycache.Partitioned` and
  `tenancystorage.Store` remain untested, and each still collapses tenancy silently. This is now the
  single largest hole in the three seams.
- **GAP-2** — unchanged. `tenancy/tenancycache/cache.go` surface is byte-identical; `cache.Global`
  over a tenant key is still accepted.
- **GAP-3** — unchanged. `Store` still returns an unbounded handle (`storage.go:31-37`).
- **GAP-4** — unchanged, and **the new test does not cover it**.
  `TestWorkThatNeedsNoTenantRestoresWithNoneBound` restores from `context.Background()`, so its
  assertion that `tenancy.From(restored)` is false holds trivially: nothing was ever bound. It proves
  the branch returns *a* context, not that the branch *strips* a scope. Re-run against the current
  tree with the worker context bound first:
  ```
  GAP-4 STILL OPEN: an untenanted job entered its handler bound to "acme"
  and a tenancy seam inside that handler answers a full write scope for "acme"
  ```
  `RestoreIdentity` (`tenancy/tenancyjobs/jobs.go:77-79`) still returns `ctx` unchanged. The close
  criterion stands, and should now read: the test must **bind the worker context to a tenant first**,
  or it passes for the wrong reason.
- **GAP-5** — unchanged. `InvalidateTag(context.Context, Namespace, Tag)` (`cache/capability.go:85`)
  still carries no partition.
- **GAP-6, GAP-7, GAP-12, GAP-13, GAP-14, GAP-15** — not re-checked in this round; nothing observed
  suggests they moved.

### Revised remediation order

1. **GAP-1** (M17 + M20) — unchanged as the first item, and now the only high-severity *test* gap left.
2. **GAP-4** — with the corrected close criterion above; the existing test must not be mistaken for coverage.
3. **GAP-3**, **GAP-2**, **GAP-5** — as before.
4. **GAP-6**, **GAP-7** — as before.
5. **GAP-8 residue** (calibration note only), **GAP-12..GAP-15** — as before.
6. ~~GAP-9~~, ~~GAP-10~~, ~~GAP-11~~, ~~GAP-16~~ — closed.

### Caveat, unchanged

The tree is still being edited by an independent session. Everything in this addendum is dated
**11:56** against a snapshot verified identical at **11:55:56**. Re-verify before acting.
