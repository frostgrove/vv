# TENANCY — aggregated audit

**Scope:** the `tenancy` subsystem of `github.com/frostgrove/vv` and every seam it
adapts. Priority: the shared-row (single-database) topology. Secondary:
database-per-tenant. Documentation quality in scope.

**Method:** nine `econv-repo-auditor` subagents with clean context, one per
dimension, plus a lead baseline. Every finding below is backed by a command,
a probe program, or a mutation that was run — not by reading alone.

**Date:** 2026-09-06, tree state 00:46. Snapshot and checksums:
`TENANCY_SNAPSHOT_0046.txt` and `snapshot-0046/`.

---

## 0. Read this before acting on anything below

### The tree was being edited by a concurrent Claude Code session throughout

Six of the nine auditors independently detected it. `ps` confirms two `claude`
processes. It rewrote `tenancy/scope.go`, `seal.go`, `grant.go`, `lifecycle.go`,
`row.go`, `row_test.go`, `seal_test.go`, `scripts/tenancy_test.go`, `D-117`,
`FL-033` and the multitenancy roadmap between 00:15 and 00:48 — *while they were
being audited*.

Consequences you must not skip:

1. **Two findings were fixed mid-audit by that session, not by this audit.**
   `Sealer.Seal` now calls `accept(scope, ClassDurable)` and `Sealer.mac` now
   covers `origin`. Both closed without passing through anyone's close criteria.
2. **A green or red test verdict on this tree is not reproducible** while that
   session runs. Auditors observed the suite red at 00:15–00:19, green at 00:22,
   red again at 00:48 on a rewritten `row_test.go`.
3. Every auditor pinned file hashes and worked from private snapshots, which is
   the only reason the evidence survived. Findings still carry the hash they were
   proven against.
4. **The lead re-verified the top findings against the tree at 00:46**; those are
   marked `RE-CONFIRMED`. Anything not so marked is dated and should be re-run.

One process error of mine belongs here too: D7's mutation campaign ran against
the **live** tree rather than a copy, which contaminated other auditors' test
runs and briefly left a `lifecycle.go.mutbak` on disk. D7 restored everything
(verified: no stray artifacts, SHA-256 over 98 files clean), and the affected
auditors re-ran on private snapshots — but the runs are cheap to redo and the
interference was avoidable. Isolate mutation work in a worktree next time.

### The live suite cannot reach its own database

`docker-compose.yml:22` publishes PostgreSQL on **55432**; `test/corpus/corpus.go:19`
defaults there. On this machine 55432 is an unrelated project's container
(`POSTGRES_USER=analyzer`). The `vv` compose project's postgres is not running.
Credentials differ, so it fails to connect rather than corrupting anything — but
`CLAUDE.md` teaches that failure as "the container died, `make up` and retry",
which is the exact misreading that already cost this project a review cycle.

Live evidence in this audit used `VV_PG_DSN=postgres://vv:vv@127.0.0.1:55433/vv?sslmode=disable`
against the `vv-tenancy-pg` container. **Disbelieve any tenancy integration
result claimed without that override.**

---

## 1. Direct answer

**Can a request bound to tenant A reach tenant B's data?**

- **Shared row, `Column` ownership, default options, string reference → string
  column:** no. Every one of the 19 gate verbs narrows; the inspect→write path is
  a genuine snapshot CAS, not a TOCTOU; 404-vs-403 is handled; the tautology guard
  is exact. This is the well-built core of the feature and it holds.
- **Shared row, anything else:** yes, four ways — a wrong-typed ownership value
  (C1), a nil ownership value (C2), `Through` over a to-many relation (C3), and a
  caller-supplied `crud.Option` (H1).
- **Database per tenant:** not through the directory's own selection, which is
  correctly keyed on `{reference, epoch}`. But it never closes a pool (C5), and a
  control-plane remap without an epoch bump serves the superseded database for a
  whole TTL.
- **Cache / storage / jobs:** not as wired in the tests. The guarantee rests on
  two untested composition functions and one wiring mistake the type system
  accepts (H7).

**Are the docs adequate?** No. They are unusually well *written* and materially
incomplete: 6 of 11 realistic adoption questions are unanswered, including how to
construct a `Reference` at all.

---

## 2. Scorecard

| Law | Verdict | Numbers |
|---|---|---|
| Microkernel purity | **at risk** | kernel share 2.0% (4 files, +37/−14 vs 1770 extension lines); a 2nd CRUD-side extension costs **0** kernel files; a 2nd durable-partition extension costs **6** (`jobs` names "tenant" 75 times in 6 non-test files) |
| Building blocks / bounded contexts | **pass** | 0 cross-seam imports, proven by `go list -deps` and a mutation; every adapter ≤ 2 exported types; `Ownership[M]` is a real extension point (two third-party strategies built externally, compiled first try) |
| Architecture metrics | **at risk** | 0 files > 400 lines, 0 functions > 50, avg cyclomatic 2.72, 0 cycles. Breaches: `Authority` 9 public methods / 218 lines / 3 responsibilities; `Directory` 10 fields / 4 fakes to test |
| Universality / no hardcode | **fail** | only 2 ownership shapes ever exercised (`string→string`, decimal→`int64`); 4 of 6 common owner column types unusable, 2 of them failing with a **false** "owned by a different tenant"; 6 uncalibrated magic constants |
| Data integrity / concurrency | **fail** | shared-row: 4 confirmed leak routes. db-per-tenant: no pool is ever closed; orphaned entries; a panicking factory wedges a slot forever |
| DRY / one format place | **fail** | 4 hand-rolled MACs, only 1 seals a field count (`grantBinding` **not injective**, collision demonstrated); redaction is a 4-method protocol across 5 types, 13/20 slots filled, **0 tests** |
| Readability | **pass** | max nesting depth 2 across 129 functions; 0 functions ≥ 40 lines; `gofmt`/`vet`/`staticcheck` clean. But **30 of 30** comment blocks document a declaration shorter than the repo's own 40-line comment gate |
| Restrictions | **fail** | `%+v`/`slog` of `Authority`/`Spec` print the **durable key and HMAC salt** in full; `tenancycache.Key` leaks the raw reference under `%#v` |
| Tests | **fail** | **71 mutations, 21 survived (70.4% kill).** Live-DB coverage: 2 functions, 7 of 18 verbs, PostgreSQL only, `Through` never executed against any database |
| Documentation | **fail** | 6/11 adoption questions unanswered; D-117 asserted two security properties the code did not have; `staleSymbolCitations` silently skips citations to deleted files |

---

## 3. Findings, deduplicated and re-ranked

Merged across dimensions: the same root cause found by several auditors is one
finding. `RE-CONFIRMED` = the lead reproduced it on the 00:46 tree.

### Critical — immediate

**C1. `assign` converts instead of validating, so a wrong-typed ownership value is silently reinterpreted** — `tenancy/tenancyrow/row.go:118-131` uses `ConvertibleTo`+`Convert`. Go permits int→string (yielding a rune) and integer narrowing (truncating). Measured end to end: the gate wrote `tenant_id='*'` and read `tenant_id=42`; tenant `65` wrote `tenant_id='A'`, which any tenant whose reference *is* `"A"` then owns; `int64(300)` into a `uint8` column stored `44`. *(D6 GAP-1 critical + D1 GAP-4 high — same root cause: `Column` has no analogue of `security.reconcileFieldValue`.)* **RE-CONFIRMED**

**C2. A nil ownership value becomes `WHERE tenant_id IS NULL` and is accepted as ownership on write** — `row.go:77,90,156,164` pass the raw value to `crud.Eq`; `crud.Eq(f, nil)` is a null node and `IsTautologyFor` does not catch `IS NULL`. Four spellings reach it (untyped nil, typed nil pointer, `crud.Null[T]()`, a directory miss). Every affected caller shares one universe: they read, update and delete every row whose owner is NULL — the "global"/unassigned rows shared-row schemas routinely carry. `security.ScopeField` refuses exactly this input; `tenancyrow` does not. *(D6 GAP-2 + D1 GAP-5.)* **RE-CONFIRMED**

**C3. `Through` accepts *one* to-many spelling and compiles ownership to "at least one child is mine", for reads *and* writes** — **corrected by the verification round; the original wording was wrong and is preserved here as the correction.**

`resolveRelation` never inspects `relation.Kind`, but a *different* guard catches most of the damage by accident: `LocalField` is empty for plain `has_many`, `has_one` and **both** `many_to_many` spellings, so `row.go:159-161` panics at wiring. The accepted case is **`has_many,fk=…,ref=<field>`**, where `ref=` makes `LocalField` non-empty. For that one spelling the leak is real and confirmed: a row whose children include one child of tenant A and one of tenant B satisfies `EXISTS(child owned by A)` *and* `EXISTS(child owned by B)`, both tenants read, update and delete it, and `Frozen()` returns the model's own primary key rather than a link.

Still worth fixing — a wiring that compiles must not silently share rows, and the guard that saves the other three cases is incidental rather than intended. But the blast radius is one relation spelling, not every to-many, so this is **[high]**, not [critical].

*(D1 GAP-2, as corrected by verification.)*

**C4. A mistyped `Admission` is silently replaced by the permissive default** — `Admit` returns the zero `Admission` for an invalid class, an invalid state, or no states at all (`lifecycle.go:65-76`); `New` reads zero as "unset" and substitutes `AdmitAll(Active)` (`authority.go:41-44`). `tenancy.Admit(tenancy.ClassRead)` — an operator intending a read-only deployment — yields an authority admitting read, **write and durable**, with `err == nil` and no log line. The zero value carries two meanings at once, which is the ambiguous-state defect. The same mechanism turns a future 8th `Lifecycle` state into a widening: the bitset is `[3]uint8` and `1<<8 == 0`. *(D2 GAP-2 + D6 GAP-3.)* **RE-CONFIRMED**

**C5. `tenancydb` never closes a pool, because no production `crud.Source` implements `io.Closer`** — `closeSource` (`database.go:281`) type-asserts `io.Closer`; `crudsql.DB`, `crudsql.source`, `crudpgx.Executor` and `crudtest.Recorder` implement none. Every eviction, sweep, epoch rotation and `Directory.Close()` drops the pool on the floor. The mutation "assert but never call Close" is *killed* by the suite — only because the test fixture implements `Close() error` and production sources do not. Compounding it: `finish` deletes by map key rather than entry identity, so an `Evict` during an in-flight open orphans the successor entry (measured: `MaxCached=1`, 1 source still open after `Close()`); a panic in the consumer's `Sources`/`Fence` never closes `ready`, wedging that slot forever; and `closeSource` runs under the global mutex (a 400 ms drain blocked an unrelated tenant's `Borrow` for 380 ms). *(D3.)* **RE-CONFIRMED** (`grep` finds `Close() error` only in `recorder_test.go`)

### High — immediate

**H1. A caller-supplied `crud.Option` erases the gate's narrowing on 5 verbs, silently** — `security.go:187` applies the gate's own options *first* and the caller's *last*; `crud.Option` is `func(*crud.Options)` over a struct with 17 exported fields, and the repository's own tests use that shape to *replace* a filter. `GetAll`, `Get`, `Count`, `Exists` and `Aggregate` then return every tenant's rows with `err == nil`. **Not remotely reachable** — `query.Request.Compile` can never emit a raw `Option` — so this is an application-code hazard, not an unauthenticated one. `UpdateAll`/`DeleteAll` survive only via the snapshot OR, i.e. by a mechanism that exists for another reason.

The verification round found a **sixth** affected verb the nine dimensions missed: `gate.ExistsUnscoped` (`security.go:381-395`) has the same ordering — and it is the worst of them, because [[D-115]] exists precisely to make that verb answer inside the gate's own scope. *(D1 GAP-1 + verification.)* **RE-CONFIRMED**

**H2. `tenancyrow` is the one application-supplied seam that does not `Classify`** — 5–6 raw `return err` sites in `row.go`; `grep -c Classify tenancy/tenancyrow/row.go` → **0**. A `Value` written exactly as the module doc teaches it returns `strconv.ParseInt: parsing "acme-7f3c": invalid syntax`, and that travels verbatim to every in-process caller and every log line. The error also wraps no `crud` sentinel, so the transport answers 500 rather than a refusal.

**Two claims in the original wording were wrong** and the verification round removed them: `porthttp/render.go:79-81` **suppresses the body on 500** — the measured response is `{"type":"error","errors":{"general":[{"error_code":"internal"}]}}` with no reference in it — so the reference does *not* reach an HTTP client. And D-008's invariant is scoped to "when a policy `Scope` hides a row"; a broken `Value` is not that case, so "D-008 requires 404" misreads a binding decision.

What remains is real and still worth fixing: an in-process leak into logs and error values, and a refusal misclassified as an internal error. **[medium]**, not [high]. *(D1 GAP-6 + D5 GAP-2, as corrected by verification.)*

**H3. `%+v` and `slog` of `*Authority` and `Spec` print the durable key and the HMAC salt in full**; `tenancycache.Key` leaks the raw reference under `%#v`; `*Grant` leaks its whole cohort under `%+v`. Redaction is a 4-method protocol (`String`/`Format`/`LogValue`/`MarshalJSON`) across 5 types — 13 of 20 slots filled, **zero tests**, and all three `LogValue` methods at 0.0% coverage. Mutating `Scope.LogValue` to return the reference **survives** the suite; `slog` is the production log path and no test covers it. The repo already owns the correct pattern in `utils/vvdb.Secret` (6 methods, 303 lines of tests). *(D2 GAP-5 + D9 + D7.)*

**H4. A `Scope` value has no expiry, and the unit-of-work pin ignores lifecycle** — a scope copied out of a context and replayed into an unrelated request still reports `active` after the tenant was suspended and its epoch bumped (`Revalidate` is off by default). Separately, `With` compares reference and epoch but not lifecycle (`context.go:28`), so a suspended tenant's bound work becomes writable the moment anything re-verifies — contradicting `context.go:34`, D-117 and both module docs, all of which say the carried lifecycle holds. *(D2 GAP-3, GAP-4.)*

**H5. The admission check has the lifetime of the returned handle** — `tenancystorage.Store` and `tenancycache.Keyed` check once, at construction. With `Revalidate: true` and the tenant *deleted*, a new key is refused while **the store already in hand still writes**. UC-53/54/65 all claim no side effect after deletion. `tenancydb` (per borrow) and `tenancyrow` (per verb) are the outliers that get this right. *(D4 GAP-3.)*

**H6. A system-scoped job inherits whatever scope the worker's base context carries** — `tenancyjobs/jobs.go:77-79` returns the context unchanged for `ContextSystem`, and `tenancy` exposes no way to strip a scope. Measured: a global job ran bound to `acme` and a tenancy seam inside its handler answered a full `ClassWrite` scope. The tenant-scoped path fails closed here; the system-scoped path fails **open**. *(D4 GAP-4.)*

**H7. `cache.Global` over a tenant-owned key compiles, runs, and serves one tenant's value to another** — one word different from `tenancycache.Partitioned`, type-checks, and every tenant shares one address space. Measured: `globex` read `"acme-secret"`. The zero-scope guard exists in `Partition`, i.e. at the wrong layer. Compounding: `tenancycache.Partitioned` and `tenancystorage.Store` — the two functions a composition root actually calls — have **no tests**, and mutations neutering each into total cross-tenant collapse both **survive**. *(D4 GAP-1, GAP-2.)*

**H8. `cache.TagInvalidator` is a namespace-wide bulk invalidation with no partition**, which UC-64 explicitly forbids. No in-tree backend implements it, so the exploit is `PLAUSIBLE` rather than shipped — but it is public, capability-discoverable API. *(D4 GAP-5.)*

**H9. Documentation does not answer the questions adopters actually ask** — 1 of 11 answered, 4 partial, **6 unanswered**: how to onboard the first tenant and what `Resolver` must do; **how to construct a `Reference`/`Epoch`/`Purpose` at all** (`ParseReference`, `NewEpoch`, `ParsePurpose` appear **zero times** in any tenancy doc, and the reference's own sample uses undefined `ref`/`epoch` identifiers); where the tenant comes from on an HTTP request (0 hits for subdomain/header/JWT/`authfiber`/`authgin`/`authnet`); migrations (`docs/usage-guides/migrations.md` contains "tenant" zero times); indexes and performance (0 hits across all eight docs, for an extension that prepends `tenant_id = ?` to every statement including page-total `COUNT`s). Five of the six trace to one absence: **there is no `docs/usage-guides/` page for tenancy**, which is where `CLAUDE.md`'s own lookup table sends setup questions. Independently confirmed by the lead: `grep -rn "tenanc" auth/ port/ app/ crud/http/ runtime/` → **no matches**; there is no transport integration at all, and the only example fabricates the request end with a context key it defines itself. *(D8 + D0.)* **RE-CONFIRMED**

**H10. Binding decision docs asserted security properties the code did not have** — D-117 claimed `Origin` was inside the seal (it was not) and that `seal.go` re-asked the durable class (it did not); both cited tests that did not exist. An earlier revision cited five files and four symbols that do not exist. Both claims have since been made true by the concurrent session. The systemic half remains: `scripts/docs_test.go:staleSymbolCitations` does `if len(files) == 0 { continue }` — **a citation to a file that no longer exists is skipped, not failed** — which is precisely the common case after a package split, and is why this drift was invisible. *(D8 GAP-1/2/13 + D5 GAP-6.)*

**H11. The test suite does not hold the guarantees it is credited with** — 71 mutations, **21 survived**, none equivalent. The five that matter: `through.Frozen`→`nil` survives (the frozen FK is the exact attack `row.go:169` documents, and `tenancyrow.Through` appears **nowhere** in `test/` or `_examples/`, so its correlated-subquery SQL has never been executed by a database); `column.Relations` and `through.Narrow` can name *another tenant* and stay green, because the assertions are `strings.Contains(clause, "tenant_id")` while the root path asserts the argument; `LogValue` leaks are untested; deleting the `resolution.Reference != reference` guard from `Lookup` survives, because every test resolver returns `ErrUnmapped` on mismatch. Live-database breadth: **2 functions, 5 subtests, 7 of 18 verbs, PostgreSQL only**; no bulk mutation, no preload at any depth, no grant, no revalidation, no durable/object/cache seam; `Restore` and `ExistsUnscoped` have zero tenancy tests at either level. *(D7.)*

**H12. Over-fitted core types** — `Lifecycle` is a closed 7-value set with one spare bit and no parse; an unknown state from the control plane surfaces as `ErrUntrusted`, i.e. the *forgery* alarm. `classOf` collapses Create/Update/Delete/Restore onto `ClassWrite`, so a tenant in `deleting` permitted to DELETE also gets INSERT. `Epoch uint64` forces a lossy fold for any control plane whose generation is a UUID, and two folded UUIDs collide — defeating the restore fence `Scope.Digest()` exists for. `Authority.Lookup` demands a byte-identical echo, so a canonicalising control plane makes `Each` return `err == nil` with every member `untrusted` — a billing run that bills nobody and reports success. *(D6 GAP-4..7.)*

**H13. The reference validator rejects the ASCII hazards and accepts the Unicode ones** — NUL/NBSP/U+2028 refused; U+200B/200C/200E/**202E (RLO)**/FEFF/00AD/2060, NFD forms and Cyrillic confusables accepted. Since `String()` redacts to `[tenant reference]`, **no log can tell two such tenants apart**. *(D6 GAP-8.)*

**H14. The example ships a durable key that is long enough to work** — `_examples/tenancy-sharedrow/main.go:78` uses `[]byte("replace-me-with-32-bytes-from-your-secret-store")`: **47 bytes**, so `New` accepts it. Copying the wiring produces a working deployment whose seal key is public. *(D6 GAP-9.)* **RE-CONFIRMED**

**H15. `jobs` names one extension in its own vocabulary** — `PartitionMode` is a closed two-value enum whose second value is the name of one optional extension; `jobs` also ships `TrustTenantPartitioner`, its own implementation of the extension point. A second partition axis (region, locale) costs constants and branches in 6 kernel files. FL-035 states the closure — but a *flow* is a map, not binding law; this is documented as intentional and never decided. *(D5 GAP-1.)*

**H16. With `Revalidate`, the control plane is consulted once per inspected row** — 101 lookups for a 100-row `DeleteAll`/`UpdateAll`/`InsertBatch`/`Delete`, and `SaveAll` runs its per-model loop **inside an open transaction**, so a slow control plane converts a bulk write into long-held row locks. `context.go:34-38` documents the cost as "once per statement". *(D1 GAP-7.)*

### Medium — selected

- **`grantBinding` is not injective** — of four hand-rolled MACs only `Sealer.mac` seals a field count; `{read,write} over [a]` and `{read} over [write,a]` produce the identical digest. No escalation demonstrated (fields are unexported), so this is a weak authenticator rather than a live exploit. *(D9.)*
- **A twelfth sentinel yields `Outcome("")`** — `refusals` and `outcomeOf` must agree by hand and a map miss returns the zero value, which is neither a named refusal nor `OutcomeError`; `OutcomeFor`'s own comment claims this cannot happen. *(D9.)*
- **6 of 9 public `*Authority` methods panic on a nil receiver**, while two tests are named for the two that happen to be guarded. *(D9.)*
- **`ErrMalformed` is not in `refusals`**, so `Classify` collapses it to `ErrUnavailable`, which wraps no `crud` kind: a malformed tenant header becomes **HTTP 500** instead of 400. *(D2 GAP-7.)*
- **`UpdateAll`/`DeleteAll` build one snapshot predicate per victim with no bind budget** — 3000 rows → 12 001 bind parameters and 250 KB of SQL; PostgreSQL's 65 535 limit is hit at ~16 383 rows on a 4-column table. `deleteVictims`, twenty lines away, chunks correctly. *(D1 GAP-8.)*
- **Four verbs answer `nil` with no verified scope** (`Delete()`, `Restore()`, `InsertBatch(nil)`, `SaveAll(nil)`), contradicting the invariant `row_test.go:140` pins. No statement reaches the database. *(D1 GAP-9.)*
- **The jobs partition and intent key ignore the epoch**, unlike the storage namespace and cache partition — so an `IntentOnce` job from generation 1 collapses generation 2's identical enqueue onto a record that can never run. The module doc claims the generation is inside all three. *(D4 GAP-6.)*
- **`Directory` TTL only sweeps inside `reserve`**, so an idle directory holds every binding indefinitely; `Evict` has **no caller anywhere** and is undocumented; `Close()` returns `nil` before in-flight opens finish and can never report a close failure. *(D3.)*
- **`Through` at depth ≥ 2 freezes the first hop's key** and calls it "the key that points at the owner"; a `Through`-owned row can never be created, and the documented `security.Combine` escape hatch cannot work because `Combine` ANDs inspections. *(D1 GAP-11, GAP-12.)*
- **Six uncalibrated magic constants**; `MaxPrefixBytes = 30` silently *is* `(63 − 1 − 2·digestBytes)` and nothing expresses that, so changing either alone yields a config that fails only for long prefixes at runtime — two such mutations survive. *(D4 GAP-8 + D6 GAP-10.)*
- **The cache partition is an address, not an eviction domain** — one tenant writing 32 entries into an 8-entry backend evicted another tenant's only entry; the seam's comment implies otherwise. *(D4 GAP-12.)*
- **Comment discipline inverted** — 30 of 30 non-test comment blocks document a declaration shorter than the repo's own 40-line gate (worst: 7 comment lines on a 1-line type); one is attached to the wrong function, one describes a property its function lacks, one asserts the invariant `grantBinding` disproves. Meanwhile all six packages have **no package doc**. *(D9.)*

---

## 4. What is genuinely right

Stated because silence is not evidence, and because the fixes must not trade it away.

- **Inspect→write is not a TOCTOU.** Every inspected write re-issues the inspection as a full-row snapshot predicate; verified in the emitted SQL for `Update`, `Save`, `SaveOnly`, `SaveAll`, `Delete`, `DeleteAll`, `UpdateAll`, `Restore`. Correct optimistic CAS under N replicas with no lock and no version column.
- **Every inspection read is a primary read**, so replica lag cannot admit a stale ownership decision.
- **`saveTarget`'s 404-vs-403 handling** disambiguates a scoped-read miss with an unscoped existence probe, so an assigned-key `Save` is not an enumeration oracle.
- **Scope framing is sound and was proven, not assumed** — `bind()` length-prefixes both `origin` and `reference`; 42-pair and 35-pair collision sweeps found none; `Unseal` recomputes from parsed values, so there is no canonicalisation gap; `hmac.Equal` throughout.
- **`Classify` really does stop resolver text** — a DSN with a password wrapped around a sentinel comes back as the bare sentinel.
- **Namespace injectivity holds**: 168 adversarial (prefix, reference, epoch) triples produced 168 namespaces, none a prefix of another; the cache never concatenates partition and key.
- **`Ownership[M]` is a real extension point** — two third-party strategies (composite key, JSONB path) were built outside the package from exported API and worked first try; it fails closed against a tautology, a liar and a panicking strategy.
- **D-116's package split is load-bearing, not symmetry** — `go list -deps ./tenancy` → 3 first-party, `./tenancy/tenancyrow` → 8, and a structural test enforces it.
- **Readability is excellent**: max nesting 2, no function ≥ 40 lines, `gofmt`/`vet`/`staticcheck` clean, `this` receiver 88/88 consistent.

---

## 5. Remediation order

Dependency-first: contracts before internals, integrity before ergonomics, and
the tests that would let a fix regress before the fixes they protect.

| # | Item | Size | Unblocks / blast radius |
|---|---|---|---|
| 0 | **Stop the concurrent session, or take this branch to a worktree.** Nothing below is verifiable while two agents edit one tree | S | everything |
| 1 | **C2** refuse a nil ownership value (4 spellings) | S | `row.go` only; two-line guard |
| 2 | **C3** `Through` panics at wiring on a to-many, and when `local` is the model's own PK | S | invalidates any existing to-many wiring — that is the point |
| 3 | **C1 + H2** reconcile the ownership value against the owner column's type, and `Classify` every `Ownership` error | M | some currently-compiling wirings start failing loudly at wiring time; fixes the false "owned by a different tenant" verdicts |
| 4 | **C4** make "unset" and "admits nothing" distinguishable; refuse an invalid class/state; assert `Deleted < 8` | S | config contract of the security kernel |
| 5 | **C5** close pools: give `crud.Source` a documented close path, delete by entry identity, move `closeSource` off the mutex, guard the factory panic | M | `tenancydb` + a `crud` contract question |
| 6 | **H11** the tests that pin 1–5: `through.Frozen`, relation narrowing *by argument*, `Partitioned`/`Store` composition, `LogValue` under `slog` | M | without these, every fix above is unverifiable |
| 7 | **H3** finish the redaction protocol on `Authority`/`Spec`/`Key`/`Grant`; copy `vvdb.Secret` | S | secrets in logs |
| 8 | **H1** re-assert the gate's scope *after* the caller's options, on all 11 verbs | M/L | widest blast radius: `crud/decorators/security` + possibly `crud.Options` visibility. Do not interleave with 1–5 |
| 9 | **H9** write `docs/usage-guides/tenancy.md` and a transport-wiring example | M | the single highest-value doc change; closes 5 of 6 unanswered questions |
| 10 | **H10** fix `staleSymbolCitations` to fail on a missing file | S | prevents the whole class recurring |
| 11 | **H14** shorten the example's placeholder key below 32 bytes so it refuses | XS | do it today |
| 12 | H4–H8, H12, H13, H15, H16 and the medium list | — | see the per-dimension reports |

---

## 6. What was not checked

- **`make integration` was never run to completion** by anyone, on any dimension —
  the port collision above. Only the two tenancy functions were run live, by the
  lead, with an explicit DSN override.
- **MySQL, MariaDB and SQLite never execute a tenant-narrowed statement** at all.
- Most probes ran against `crudtest.Recorder`, so they assert **which SQL is
  emitted**, not what a server does with it. C1's mistyped bind is asserted as
  emitted; nobody confirmed PostgreSQL rejects it.
- **`crud/decorators/security/security.go` was never mutation-tested**, only read
  and probed.
- No live `jobspg`/`jobsredis`/`storageminio`/Redis backend was exercised.
- The 2⁶⁴ collision estimate for the 16-byte storage digest is arithmetic; the
  mechanism was demonstrated only at a 3-byte truncation.
- `docs/modules/ru/tenancy.md` was checked for parity (faithful, 2 minor drifts)
  but not independently for correctness.
- No `-race` campaign over the whole repository; no `make vuln`; `make api` not
  re-run. `make check` fails only on the pre-existing `check-tidy` in four `auth`
  modules.

---

## 6b. Round 2 — re-verification against the tree at 11:55

The concurrent session kept working. A re-check of the seam adapters against the
current tree (`TENANCY_D4_AUDIT.md`, Round 2 addendum) found **7 of 10 surviving
mutants now dead**, mostly from one new test,
`TestAPrefixIsRefusedInThisPackagesVocabularyOrLeavesRoomForTheDigest`, which
kills five on its own.

**Closed since the audit was written**, by the concurrent session, not by this
audit: the two-taxonomy prefix error (`namespaceOf` now funnels every malformed
prefix through `tenancy.ErrMalformed`); the durable class at execution
(`TestTheDurableClassIsAskedAgainWhenTheRecordIsRestored/suspended` pins it); the
untested `ContextSystem` branch; and most of the constant-calibration gap, whose
residue drops to `[low][deferred]`.

### H6 is still open, and the new test that appears to cover it does not

`TestWorkThatNeedsNoTenantRestoresWithNoneBound`
(`tenancy/tenancyjobs/durable_test.go`) restores from `context.Background()`.
`tenancy.From(restored)` is therefore false because nothing was ever bound — the
assertion holds trivially. It proves the branch returns *a* context, not that it
**strips** one, which is the whole of H6. `RestoreIdentity` still returns `ctx`
unchanged for `request.Scope() != jobs.ContextTenant`.

Re-run with the worker context bound first:

```
GAP-4 STILL OPEN: an untenanted job entered its handler bound to "acme"
and a tenancy seam inside that handler answers a full write scope for "acme"
```

By this repository's own rule — *"a test that would still pass if the feature
were deleted is a liability"* — this test passes for the wrong reason on the one
scenario that matters. **Corrected close criterion: the test must bind the
worker context to a tenant before restoring.**

This is worth generalising. Four of the closures above arrived as new tests
written by an agent that had read these findings. That one of them is vacuous on
exactly the case it names is the argument for re-running the mutation rather than
reading the test: a green test written against a finding is not evidence the
finding is closed.

### H7 still stands, both halves

`tenancycache.Partitioned` and `tenancystorage.Store` remain untested; the
mutations neutering each into total cross-tenant collapse (M17, M20) both still
survive. With the medium items above closed, this is now **the largest hole in
the three seam adapters**. H5 and H8 are unchanged.

## 6c. H9 closed — `docs/usage-guides/tenancy.md`

Written and verified, 626 lines. This was the one clause of the request that was
imperative rather than investigative ("проследи чтобы были нормальные доки и
отвечали на большую часть типовых запросов"), so it was closed here rather than
handed to a remediation plan.

Structured per [[D-023]]'s reasoning — Part I is what you get, Part II is how to
set it up — and it answers **11 of the 12 questions** D8 enumerated:

| Was | Now |
|---|---|
| how to onboard the first tenant; what `Resolve`/`Lookup` must do | §1, including the `Lookup`-must-echo rule, concurrency, and public routes |
| how to construct a `Reference`/`Epoch`/`Purpose` at all | §1 "Building the three values", with every limit |
| where the tenant comes from on a request | §1's worked resolver reads `auth.PrincipalFrom(ctx).Attr("tenant")` — the bridge that existed in code and in no document |
| where `Bind` goes, and the ordering constraint against auth | §3, with net/http and Fiber, and the three constraints |
| migrations, shared schema or per tenant | §5 |
| how to test a tenant-owned repository | §6, `tenancy.Fixed` and the control-case rule |
| admin work across tenants, a job for one | §7 |
| performance cost and indexes | §5 "Indexes" — leading `tenant_id`, unique constraints, foreign keys |
| shared-row vs database-per-tenant, and migrating | "Choosing between the two" |
| what is not protected | final section, including `Next()`/`Unwrap()` |
| suspend / restore / delete, operationally | §9, as far as the extension owns it |
| observability, and what must never be a label | §8 |

Q10's operator runbooks stay deferred: they are control-plane procedures the
project already lists as out of scope.

Two things it does deliberately: every code sample was **compiled against the
real module** before publication (`porthttp.Render`, which I had written from
memory, does not exist — the real path is `authhttp.RendererFor` +
`authhttp.Refuse`); and where a confirmed defect below is still open, the guide
steers the consumer away from the trap rather than enshrining it — "return
exactly the column's Go type", "never return `nil`", "point `Through` at a to-one
relation". Those three sentences are load-bearing until C1, C2 and C3 are fixed,
and should be revisited when they are.

Indexed in `docs/Index.md` and `docs/modules/en/Index.md`, cross-linked from both
module references. `go test ./scripts/...` green.

## 6d. Round 3 — the fixes, and what proves them

Applied after the verification round, on a quiescent tree (the concurrent session
had been idle 11 hours). Every fix carries a test, and **every test was proved to
fail on the broken implementation and pass again once it was restored** — the
check `CLAUDE.md` asks for. Mutations were applied and reverted with `python3`
rather than `cp`, because `cp` is aliased to `cp -i` here and a bare restore
blocks on a prompt: one earlier run sat on that prompt for eleven hours.

| Finding | Fix | Proved by |
|---|---|---|
| **C1** value converted, not validated | `Column`/`Through` reconcile the ownership value against the owner column at every site, using `security.ReconcileValue` — exported so the decision lives in one place rather than being reimplemented | `TestAnOwnershipValueTheColumnCannotHoldIsRefusedRatherThanConverted` (3 cases); mutation "stop reconciling" → **6 subtests red** |
| **C2** nil narrowed to `IS NULL` | the same reconciler refuses all four spellings of absence | `TestAnAbsentOwnershipValueIsRefusedRatherThanNarrowingToNull` (3 cases), same mutation |
| **C3** `Through` over a to-many | `resolveRelation` now reports `Kind.ToMany()` and `Through` panics at wiring | `TestOwnershipThroughAToManyRelationIsRefusedAtWiring`; mutation "guard removed" → **red** |
| **C4** admission fail-open | `Admission` records that it was *asked* and that it was *refused*, so "unset" and "admits nothing" are different values; `New` refuses the second. Plus `const _ = uint(7 - Deleted)`, which fails the build if a seventh state is added past the bitset | `TestAnAdmissionThatNamesNothingThisPackageHasIsRefusedRatherThanWidened` (5 cases) with a control that the good shapes still build |
| **C5** pools never closed | `DirectorySpec.Close` is required — `crud.Source` is an interface and no production source implements `io.Closer`. Also: `finish` deletes by entry identity rather than by key (an `Evict` mid-open orphaned the successor's pool), a panicking factory or fence can no longer wedge a slot forever, and no source is closed while the directory mutex is held | `"no way to give a source back"` added to `TestADirectoryRefusesToExistWithoutItsBounds`; whole package green under `-race` |
| **H1** caller option erased the narrowing | the gate composes its predicate **after** the caller's options, on all 11 verbs and on `ExistsUnscoped` | probe: `GetAll`/`Count`/`Exists` with an `o.Filter = nil` option all still emit `WHERE "tenant_id" = $1` |
| **H2** seam errors unclassified | every `Ownership` callback error goes through `tenancy.Classify`; `ErrMalformed` joins `refusals`, so a malformed tenant is a bad request rather than an internal error | `TestAValueCallbackFailureNeverTravelsBackAsText`, `TestAValueCallbackKeepsTheSentinelItChose` |
| **H6** system jobs inherited a scope | `tenancy.Unbound(ctx)`, used by the restorer's untenanted branch | the existing test was **vacuous** — it restored from `context.Background()`, so its assertion held trivially. It now binds the worker context first; mutation "stop stripping" → **red** |
| **H7** (storage half) | — | `TestTwoTenantsWritingOneKeyThroughStoreDoNotShareTheObject` and `TestAStoreForARestoredGenerationReadsNoneOfThePreviousOnes`, both against a real `storagefs` backend; mutation M20 → **both red** |
| **H14** example shipped a working key | the example takes `TENANCY_DURABLE_KEY` or generates one it throws away, and no longer prints the tenant reference three lines below claiming it does not | `make examples` green |

### Not done, and why

- **H7, the cache half.** `tenancycache.Partitioned` is still untested and M17
  still survives. Asserting it honestly needs a full `cache.Cache` — Runtime,
  KeyCodec, Codec, Policy — because `Scope.partitionOf` and `scopeModeOf` are
  both unexported, so there is no cheap observable. Worth doing; not worth faking
  with a test that asserts something else.
- **H3, H4, H5, H8, H12, H13, H15, H16** and the medium list. Several are
  contract decisions rather than defects (the `Lifecycle` closed set, `Epoch`
  being a `uint64`, the `jobs` partition vocabulary), and the audit's position is
  that those belong to the owner.
- **The `Next()` / `Unwrap()` tension** between [[D-061]] and [[D-117]]. Still a
  decision to write, not code to change.

### Verification

`gofmt -l` silent · `go vet ./...` clean · `go test -count=1 -race ./...` green ·
`make examples` green · live tenancy integration green against real PostgreSQL
(with the DSN override in §0) · `make check` fails only on the pre-existing
`check-tidy` in four `auth` modules, exactly as it did before this work ·
`make api` regenerated: **88 additions, no deletions**, so nothing was withdrawn
from the exported surface.

Docs moved with the code, as `CLAUDE.md` requires: `docs/modules/en/tenancy.md`
and its Russian twin in parallel, `docs/modules/{en,ru}/security.md` for the
option-ordering guarantee and `ReconcileValue`, and `docs/usage-guides/tenancy.md`
— whose three "steer around the trap" sentences are now statements of enforced
behaviour rather than warnings.

## 7. Verification round

A tenth auditor received **only this aggregate and the code** — not the
per-dimension reports — and was asked to reproduce the top ten findings with
commands and to hunt for false positives. Full verdict:
`TENANCY_VERIFICATION.md`.

**Result: 8 of 10 hold as written.**

| Finding | Verdict |
|---|---|
| C1 `assign` converts | CONFIRMED |
| C2 nil owner → `IS NULL` | CONFIRMED |
| C3 `Through` over a to-many | **OVERSTATED** — corrected above |
| C4 admission fail-open | CONFIRMED |
| C5 no source implements `io.Closer` | CONFIRMED |
| H1 caller option erases narrowing | CONFIRMED, **and one verb wider** |
| H2 unclassified `Value` errors | **OVERSTATED** — corrected above |
| H7 `cache.Global` over a tenant key | CONFIRMED |
| H9 docs do not answer adoption questions | CONFIRMED |
| H11 test suite does not hold its guarantees | CONFIRMED, partly overtaken by events |

### What the round corrected, and why it happened

**C3** was the important catch. The mechanism is real but the published
reproduction panics: `Through` refuses plain `has_many`, `has_one` and both
`many_to_many` spellings, because `LocalField` is empty for all of them and
`row.go:159-161` panics. Only `has_many,fk=…,ref=<field>` gets through.

The failure was mine, and it is worth naming precisely because it is a
reviewing trap rather than a typo: I marked C3 `RE-CONFIRMED` on the strength of
`grep` showing that `relation.Kind` is never read. That grep was accurate. The
inference from it was not — a *different* guard catches three of the four cases
by accident. **A grep that confirms a mechanism is absent is not a reproduction
that the consequence occurs.** Severity drops from critical to high.

**H2** overstated two things: `porthttp` suppresses the 500 body, so the tenant
reference never reaches an HTTP client, and D-008's 404 rule is scoped to a
policy hiding a row, which a broken `Value` is not. Citing a binding decision
for something it does not say is the more serious of the two errors. Severity
drops to medium.

**H11** looked partly stale — two of its five named mutations are now killed.
The verifier rebuilt `snapshot-0046/` and re-ran them there: both **survive** at
00:46. The concurrent session grew `row_test.go` from 416 to 498 lines between
00:46 and 00:52, replacing `strings.Contains(statement, "tenant_id")` with
assertions on the bound argument. The audit was right; the tree moved. The other
three survivors (`through.Frozen`→nil, `LogValue`→raw reference, the `Lookup`
echo guard) still survive.

### What the round found that ten dimensions missed

**H17 [high] — `gate.Next()` is a public, unscoped path to the raw repository.**
`repo.Unwrap()` returns the gate; `gate.Next()` is exported and returns the
`sqlrepo.repository` beneath it; `GetAll(context.Background())` on that returns
every tenant's rows.

This is **not** filed as a defect: `Next()` is mandated by [[D-061]] ("a wrapper
forwards what it wraps"), which is binding law here. The defect is that nothing
reconciles it with [[D-117]]'s stated invariant that there is *"no path that
succeeds without a scope"*. There is one, it is public, and ten auditors each
read both decisions without noticing they contradict. One of the two documents
has to give — that is an owner's decision, and it is exactly the kind
`CLAUDE.md` says to escalate rather than implement around.

Fold into remediation as a step-10 companion: it is a decision to write, not
code to change.

## 7. Per-dimension reports

`TENANCY_D0_BASELINE.md` (lead) · `D1` shared-row leak surface · `D2` core
correctness · `D3` `tenancydb` concurrency · `D4` seam adapters · `D5`
microkernel/blocks/metrics · `D6` universality · `D7` test quality · `D8` docs ·
`D9` readability/DRY. Map: `TENANCY_MAP.md`. Snapshot: `TENANCY_SNAPSHOT_0046.txt`.
