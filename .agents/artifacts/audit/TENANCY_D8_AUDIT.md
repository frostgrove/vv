# tenancy documentation — correctness & completeness — AUDIT (2026-09-06)

Repository: `/home/user/ws/gd/lease/frostgrove/vv/framework`, module `github.com/frostgrove/vv`.
Rubric: repo `CLAUDE.md` ("Keeping the docs consistent"), which treats a stale doc as a failing
test. Scope: every document that describes tenancy. **No file was modified by this audit.**

> **The tree moved under this audit.** Another process edited `tenancy/*.go` and, at 00:35–00:36,
> rewrote `D-117`, `FL-033` and the multitenancy roadmap while the audit was running. Every
> finding below was **re-measured after 00:36** and reflects the tree as of **00:38**. Where an
> earlier defect was fixed by that in-flight edit it is recorded as fixed, with the evidence
> that it was real, because the same edit introduced worse ones. Details in
> *What I did not check*.

## Map

Tenancy documentation is nine artefacts in five directories, plus three passing mentions:

- **Consumer reference** — `docs/modules/en/tenancy.md` (359) / `docs/modules/ru/tenancy.md`
  (364). One row each in `docs/modules/{en,ru}/Index.md` (en:69, ru:80). **No
  `docs/usage-guides/` page.**
- **Binding decisions** — `D-116` (one extension, root module, core costs no seam), `D-117`
  (a scope is minted, never manufactured), `D-115` (unscoped existence is an exact outer
  effect); all `Status: accepted`, all in `docs/ai/decisions/Index.md:170-172`. `D-008`,
  `D-007`, `D-030` are older decisions the reference cites.
- **The map** — `FL-033` (197). Listed in `docs/ai/flows/Index.md:106`; its 14 tenancy source
  files are all in the reverse index (237-250).
- **The guarantees** — `UC-028` under `docs/ai/usecases/modules/tenancy/` (122 lines; the audit
  map said this directory did not exist — it does) and `UC-004` under `.../security/`.
- **What is open** — `docs/roadmaps/2026-09-01-multitenancy-roadmap.md` (523) with a published
  profile and a 12-row definition-of-done; `docs/roadmaps/Roadmap.md` §13 (286-307).
- **Baseline** — `docs/api/surface.md:1757-1841`, six tenancy packages.
- **Passing mentions** — `docs/usage-guides/{ent,gorm}.md` §13 (a parallel note in both, as
  `CLAUDE.md` requires), `_examples/README.md:53`, `README.md` (none — GAP-9).

Code: `tenancy/` (9 files; direct imports are stdlib + `crud` only, verified) plus five seam
adapters. **106 exported identifiers, 58 of them methods.**

The repository has **automated doc checks** — `scripts/docs_test.go`. They are the reason this
documentation is as accurate as it is, and **they are RED right now** (GAP-1).

## Scorecard

| Question | Verdict | Evidence |
|---|---|---|
| (1) Is anything FALSE? | **fail** | 3 behavioural claims disproved by executable tests with controls; 2 cited tests that do not exist; 1 count off by one; 1 nonexistent symbol; 1 roadmap line contradicting its own governing decision; 1 example output count off by one with a self-contradiction two lines later |
| — do the code samples compile? | **pass** | All 13 EN snippets + the 1 RU-only snippet transcribed verbatim into a module against the real library → `go build ./...` clean. Harness proved to fail on an injected wrong option name |
| — `surface.md` vs its own generator | **pass** | Tenancy section regenerated through the exact `go doc -short` pipeline in `scripts/modules.sh:api()` → byte-identical |
| — `surface.md` vs `go doc -all` | **fail** | 58 exported methods in `go doc -all`, 0 in `surface.md` (GAP-11) |
| — cited **file paths** exist | **pass (as of 00:36)** | 0 stale `*.go` paths across all 9 scoped docs. 7 were stale at 00:15 and were fixed by the in-flight edit |
| — cited **test names** exist | **fail** | 2 of 35 do not: `D-117:148`, `D-117:151` (GAP-1) |
| (2) Is anything MISSING? | **fail** | 1 of 11 adoption questions answered, 4 partially, 6 unanswered. 55/106 exported identifiers never named in the reference |
| (3) Is RU faithful? | **pass, 2 drifts** | 15/15 sections 1:1; inline-symbol multiset identical; `[[…]]` multiset identical; code blocks identical modulo translated comments (GAP-12) |
| Index row for every doc | **fail** | `docs/ai/usecases/modules/Index.md` has no `tenancy` row (GAP-6) |
| Flows carry the file table + reverse index | **pass** | FL-033's 16 paths all exist; all 14 tenancy files in the reverse index |
| Superseded decisions say so | **pass** | D-116:10-13 and roadmap:14-24 both record the supersession, from both sides. No tenancy decision is silently superseded |
| Roadmap lists nothing already done | **pass** | All 5 items in `Roadmap.md:297-307` verified still open (`rls.go` absent; `test/integration/tenancy_test.go` holds 2 functions, neither a two-database test; no fake-extension fixture) |

## Metrics

| Metric | Threshold | Actual | Worst offenders |
|---|---|---|---|
| Doc claims disproved by an executable test | 0 | **3** | `D-117:148/150-151`, `en:123`/`ru:124`, `en:291-308`/`ru:297-314` |
| `go test ./scripts/...` (the doc checks) | green | **RED** | `TestEveryTestNameTheDocsCiteExists` |
| Cited test names that do not exist | 0 | **2 / 35** | `D-117:148`, `D-117:151` |
| Cited `*.go` paths that do not exist | 0 | **0** (was 7 at 00:15) | fixed in-flight |
| Cited symbols that do not exist | 0 | **1** | `tenancycache.CacheScope`, roadmap:415 |
| Doc code samples that fail to compile | 0 | **0** | — |
| Exported identifiers never named in the reference | — | **55 / 106 (52%)** | `ParseReference`, `NewEpoch`, `ParsePurpose`, `New`, `Verify`, `With`, `Classify`, `Sealer`/`Seal`/`Unseal`, `Digest`, `Directory`, `Lease`, `Close`, `Evict`, `Cached`, `Namespace`, `Partition`, `SourcesFunc`, `Member`, all 12 `Outcome*` |
| Exported methods in `docs/api/surface.md` | 58 | **0** | `Authority.Bind`, `Authority.Each`, `Sealer.Seal`, `Directory.Borrow`, `Lease.Source` |
| Broken relative links in scoped docs | 0 | **9** | `docs/Index.md:9,11`; `docs/modules/{en,ru}/Index.md:4,5`; roadmap:9; `roadmaps/Index.md:32` |
| Mislabelled links | 0 | **2** | en:358 / ru:363 label FL-033 as "jobs" |
| Broken UC↔FL reverse links | 0 | **4** | FL-033 omits UC-028; `flows/Index.md:106` omits UC-028; `usecases/Index.md:138` omits FL-005/006; `UC-004`'s table omits FL-020 |
| Missing `Index.md` rows | 0 | **1** | `docs/ai/usecases/modules/Index.md` — no `tenancy` row |
| Adoption questions unanswered | 0 | **6 / 11** | see (2) |
| RU sections missing vs EN | 0 | **0** | — |

---

# (1) What is FALSE

### GAP-1 [critical][immediate] D-117 now asserts two security properties the code does not have, and cites two tests that do not exist
- **Where:** `docs/ai/decisions/D-117-a-verified-scope-is-minted-never-manufactured.md:139-141, 148, 150-151`
  (introduced by the 00:35 rewrite)
- **Scale:** local (one decision doc, 2 false capability claims + 2 dead citations)
- **Confidence:** CONFIRMED — two executable tests against the real module, plus the repo's own
  checker:
  ```
  === RUN   TestDocClaim_OriginIsInsideTheSeal
      DOC IS WRONG: production unsealed staging's record as tenant="acme" generation=7
      — Origin is NOT inside the seal; only DurableKey is
  --- FAIL
  === RUN   TestDocClaim_SealReAsksTheDurableClass
      DOC IS WRONG: Seal sealed a scope whose lifecycle is not admitted for ClassDurable
      — Seal calls Authority.minted, which checks the mint only, never Admission
  --- FAIL

  $ go test ./scripts/...
  --- FAIL: TestEveryTestNameTheDocsCiteExists (0.09s)
      TestARecordSealedInAnotherDeploymentIsNotRead is cited by
        ../docs/ai/decisions/D-117-…md:151 and no _test.go declares it
      TestOnlyAScopeTheDurableClassAdmitsIsSealed is cited by
        ../docs/ai/decisions/D-117-…md:148 and no _test.go declares it
  FAIL	github.com/frostgrove/vv/scripts	2.646s
  ```
- **What:**
  1. **`:150-151`** — "`TestARecordSealedInAnotherDeploymentIsNotRead` … a copied `DurableKey`
     is not enough, because **`Origin` is inside the seal too**." It is not.
     `Sealer.mac` (`tenancy/seal.go:83-98`) MACs `count ‖ binding fields ‖ generation ‖
     reference` under `durableKey` and nothing else; `grep -c 'rigin' tenancy/seal.go` → **0**.
     Two deployments sharing a `DurableKey` and differing only in `Spec.Origin` produce
     byte-identical tokens and each unseals the other's records — proved above.
     The real test is `TestARecordAnotherDeploymentSealedIsNotRead` (`tenancy/seal_test.go:142`),
     a **different name**, and what it exercises is a different key, not a different origin.
  2. **`:139-141` + `:148`** — "`tenancy/seal.go` … where the durable class is **re-asked**
     before it does", proven by `TestOnlyAScopeTheDurableClassAdmitsIsSealed`, "the core
     primitive refuses a lifecycle the durable class does not admit, **rather than relying on
     each adapter to ask first**." `Seal` (`seal.go:41-55`) calls `Authority.minted`
     (`authority.go:122-130`), which checks `scope.boundTo(salt, origin)` and returns — it never
     touches `admission`. `grep -c 'ClassDurable\|admission\|accept(' tenancy/seal.go` → **0**.
     The durable-class check lives exactly where the doc says it does not: in the adapter,
     `tenancy/tenancyjobs/jobs.go:36` (`authority.Scope(ctx, tenancy.ClassDurable)`).
  3. **`:53`** — "The **ten** sentinels in `tenancy/errors.go`". There are **eleven**
     (`ErrMalformed` at `errors.go:11` plus the 10 in the `refusals` table at `errors.go:34-37`).
     This one survived the rewrite.
- **Why this severity:** `CLAUDE.md`: "**A decision doc is binding.** If `docs/ai/decisions/`
  says something must not happen, it must not happen." D-117 now tells the next engineer that
  the seal is fenced by deployment origin. An operator clones a staging deployment from a
  production configuration — the case `tenancy/binding_test.go:8-13` describes verbatim as the
  reason `Origin` exists ("the salt is regenerated but the wiring is copied verbatim") — keeps
  the `DurableKey`, and staging's workers execute production's queued tenant work. D-117 is the
  document that would have stopped them. It now says the opposite. Second: a deployment that
  admits `Active` for reads and writes but not for durable work believes the core refuses to
  seal; it does not, and the only thing standing between a non-durable scope and a durable
  record is the adapter the doc says is not relied on.
- **Why this timing:** The doc checks are red **now**; `CLAUDE.md` requires `make check` before
  reporting a task done. And the two claims describe a design that does not exist — leaving them
  means either the code is changed to match (a security change to the seal MAC, which is a
  wire-format change to every queued record) or the doc is corrected. That decision cannot wait
  behind anything else.
- **Close criteria:**
  - [ ] `go test ./scripts/...` is green
  - [ ] either `Sealer.mac` includes `Origin` **and** `Seal` re-asks `ClassDurable`, with the two
        cited tests written; or `:139-141`, `:148` and `:150-151` are corrected to describe
        `minted()` and to cite `TestARecordAnotherDeploymentSealedIsNotRead` /
        `TestOnlyAScopeThisAuthorityMintedIsSealed`
  - [ ] `:53` says eleven
  - [ ] whichever way it goes is recorded, per `CLAUDE.md`'s "do not edit history into agreement"

  **History (evidence that the class of defect is recurrent, not a one-off).** At 00:15 this
  same section cited five files that did not exist and one symbol that did not:
  ```
  MISSING: tenancy/jobs.go        (cited :121 with `JobContext`, `JobIdentity`, `tokenMAC`
                                   — grep across all *.go: no output for any of the three)
  MISSING: tenancy/seams_test.go  (:146)
  MISSING: tenancy/row_test.go    (:143 → tenancy/tenancyrow/row_test.go)
  MISSING: tenancy/durable_test.go(:153 → tenancy/tenancyjobs/durable_test.go)
  MISSING: tenancy/database_test.go(:156 → tenancy/tenancydb/database_test.go)
  `classify`                      (:120 → the function is exported `Classify`)
  ```
  All six were fixed by the 00:35 rewrite. The rewrite replaced dead pointers with false claims.

### GAP-2 [high][immediate] The admission-consistency rule is documented wider than the code enforces
- **Where:** `docs/modules/en/tenancy.md:123-126`, `docs/modules/ru/tenancy.md:123-126`
- **Scale:** systemic (2 occurrences — EN and RU carry the identical wrong claim)
- **Confidence:** CONFIRMED — executable test **with a control**:
  ```
  === RUN   TestDocClaim_DurableWithoutReadIsRefusedAtConstruction
      DOC IS WRONG: tenancy.New accepted an admission that admits Migrating for
      durable work but not for reading
  --- FAIL
  === RUN   TestControl_WriteWithoutReadIsRefusedAtConstruction
      CONTROL OK: tenancy: migrating admits writes but not reads, and every write
      resolves a read scope first: admit it for reads too, or for neither
  --- PASS
  ```
- **What:** EN:123 — "a state admitted for writing **or for durable work** but not for reading
  is refused, naming the state." RU:124 — "допущенное к записи **или к долговременной работе**".
  `tenancy/lifecycle.go:109-116` `inconsistent()` tests **only** `ClassWrite`; the error text
  (`authority.go:46`) says "admits writes but not reads". The code's own comment
  (`lifecycle.go:106-108`) states the doc's opposite: *"Durable work is deliberately not held to
  that floor."* `D-117:37-43` gets it right — the module reference does not.
- **Why this severity:** A deployment writes
  `Admit(ClassRead, Active).Merge(Admit(ClassDurable, Active, Migrating))` intending "the queue
  drains during a migration", is told by the reference that this is refused at construction with
  the state named, and therefore writes no test for it. It is accepted. Symmetrically, a team
  that *wants* durable-during-migration reads the doc, believes the library forbids it, and
  builds a second mechanism outside the library to get it.
- **Why this timing:** It is a claim about a start-up failure contract in the two documents
  `CLAUDE.md`'s lookup table sends a consumer to, and the reference and the binding decision
  currently contradict each other on it.
- **Close criteria:**
  - [ ] EN:123-126 / RU:123-126 say "admitted for **writing** but not for reading" and state the
        durable exemption, matching `D-117:37-43` and `lifecycle.go:106-108`
  - [ ] a test pins the exemption, with the write case as its control

### GAP-3 [high][immediate] `Authority.Each` reports success when every member failed, and no doc says so
- **Where:** `docs/modules/en/tenancy.md:291-308`, `docs/modules/ru/tenancy.md:297-314`,
  `docs/ai/usecases/modules/tenancy/UC-028…md:62-66`
- **Scale:** systemic (2 module docs + 1 use case)
- **Confidence:** CONFIRMED — executable test:
  ```
  === RUN   TestEachReturnsNilErrorWhenEveryMemberFails
      Each returned err=<nil>, outcomes=[{[tenant reference] error billing exploded}
                                         {[tenant reference] error billing exploded}]
  --- PASS
  ```
  Source: `tenancy/grant.go:157-159` turns the closure's error into a `Member{Outcome, Err}`;
  `:143` appends it; `:145` returns `outcomes, nil`.
- **What:** The docs say a member whose **lifecycle** moved "is recorded in `outcomes` and
  skipped rather than failing the run". They never say an error from **the caller's own closure**
  is treated identically. The sample is
  `outcomes, err := authority.Each(ctx, grant, ClassWrite, func(ctx) error { return billing.Roll(ctx) })`
  and the prose that follows discusses only lifecycle.
- **Why this severity:** The idiomatic reading of that sample is `if err != nil { … }`. A monthly
  billing run in which every tenant's `billing.Roll` fails returns `err == nil` and is logged as
  a success. `D-117:101-103` names this exact failure as the reason a bound context is refused a
  grant — *"a billing run that bills nothing and says it worked"* — and the module reference
  leaves the other door to it open and unlabelled.
- **Why this timing:** Silent-wrong-result on the primary cross-tenant API, in the page a
  consumer copies from.
- **Close criteria:**
  - [ ] EN/RU state that `Each`'s `error` covers only whole-run refusals (`ErrGrantRequired`,
        `ErrUntrusted`, `ErrStale`, `ErrPinned`, `ctx.Err()`) and that every per-member failure —
        lifecycle **and** the closure's own error — is in `outcomes`
  - [ ] the sample inspects `outcomes`, or one sentence says it must
  - [ ] `UC-028:62-66` says the same

### GAP-4 [high][immediate] The reference presents database-per-tenant as supported; every other document fences it
- **Where:** `docs/modules/en/tenancy.md:254-288`, `docs/modules/ru/tenancy.md:260-294`
- **Scale:** systemic (2 occurrences)
- **Confidence:** CONFIRMED — 35 lines of unqualified how-to with a full `NewDirectory` sample
  and no caveat: `grep -c 'not advertised' docs/modules/{en,ru}/tenancy.md` → **0, 0**. Against it:
  - `roadmap:485` (Published profile, Topology): "Shared row only. The database-per-tenant
    strategy is implemented and unit-proved but **not advertised**"
  - `UC-028:107`: "**covered for shared row; the database-per-tenant profile is not advertised.**"
    and `:114-117`: "two real databases, outage behaviour and … the suspend/migration/restore
    rehearsal have not been run"
  - `Roadmap.md:297-300`: the same, under "What remains open"
  - `test/integration/tenancy_test.go` holds two functions, neither a two-database test
- **What:** The one page a consumer is sent to for "what can this package do" advertises a
  topology every other page fences. The reference's own "What it does not cover" (EN:338-352)
  has four items and this is not among them.
- **Why this severity:** A team picks database-per-tenant off the reference and builds a
  `Sources` factory and secret-store integration around `tenancydb.Directory`, then discovers at
  release review that the profile does not cover it. The roadmap's definition-of-done row 12
  (`roadmap:517`) claims "**met** — documentation … lists every unsupported integration as
  fenced or deferred". It does not; that row is false.
- **Why this timing:** It decides what a consumer commits an architecture to.
- **Close criteria:**
  - [ ] EN/RU "One database per tenant" opens with the profile status in `UC-028:107`'s words
  - [ ] "What it does not cover" gains PostgreSQL RLS, operator procedures and pre-tenancy row
        backfill — the three "Known exclusions" (`roadmap:499`) the reference omits
  - [ ] roadmap row 12 is re-evaluated rather than left `met`

### GAP-5 [medium][immediate] The multitenancy roadmap names a symbol that does not exist and contradicts its own governing decision
- **Where:** `docs/roadmaps/2026-09-01-multitenancy-roadmap.md:415, 419`
- **Scale:** local (2 lines)
- **Confidence:** CONFIRMED — `grep -rn 'CacheScope' --include='*.go' .` → **no output**.
- **What:**
  - `:415` cites `` tenancycache.`Partition`/`Keyed`/`CacheScope` ``. **`CacheScope` does not
    exist**; the symbol is `Partitioned` (`tenancy/tenancycache/cache.go:56`).
  - `:419` — M3 item 3: "**Done** — `jobs.go`, `storage.go`, `cache.go`; **no package per seam**."
    The delivered layout **is** one package per seam — D-116's whole invariant — and this same
    document states it at `:63` and `:87-99`. The line is a survivor of the pre-D-116 shape and
    now contradicts the binding decision two hundred lines above it.
  (Two stale paths at `:395` and `:509` were fixed by the 00:36 edit; the `CacheScope` and
  "no package per seam" lines in the same section were not.)
- **Why this severity:** `:419` is a "Done" claim contradicting the decision that governs the
  layout; a reader cannot tell which is current without reading the tree. `:415` sends a
  consumer looking for an exported name that never existed.
- **Why this timing:** It is the document `Roadmap.md:294` forwards to for the published profile
  and per-milestone state — what a release review reads.
- **Close criteria:**
  - [ ] `:415` says `Partitioned`; `:419` says "one package per seam, per [[D-116]]"

### GAP-6 [medium][immediate] Four broken reverse links between UC-028, FL-033 and the indexes, and no `tenancy` row in the module usecase index
- **Where:** `docs/ai/usecases/modules/Index.md` (no row); `docs/ai/flows/FL-033…md:4`;
  `docs/ai/flows/Index.md:106`; `docs/ai/usecases/Index.md:114, 138`;
  `docs/ai/usecases/modules/security/UC-004…md:4, 99-106`
- **Scale:** systemic (4 broken links, 5 files)
- **Confidence:** CONFIRMED —
  ```
  $ grep -c -i 'tenanc' docs/ai/usecases/modules/Index.md      → 0
  $ grep -n 'UC-028' docs/ai/flows/*.md                        → still not mentioned
  $ grep -n 'UC-028' docs/ai/usecases/Index.md
  138: … | [[FL-033]] [[FL-007]] [[FL-008]] |      # UC-028:4 declares five flows
  ```
- **What:**
  1. `docs/ai/usecases/modules/Index.md` lists 24 module rows and **no `tenancy` row**, although
     `modules/tenancy/UC-028-…md` exists. `CLAUDE.md`: *"An index that does not list a file is
     worse than a missing file — an agent trusts the index and stops looking."* `tenancy/` is
     also the only module usecase directory with no overview file (20 of 25 have one).
  2. `FL-033:4` declares `**Implements:** [[UC-004]] [[UC-012]]` and `flows/Index.md:106` repeats
     it; `UC-028:100` says FL-033 covers it. **The link is one-way** — and FL-033 was rewritten
     at 00:35 without adding it.
  3. `usecases/Index.md:138` gives UC-028 three flows; `UC-028:4` declares five (drops FL-005,
     FL-006).
  4. `UC-004:4` declares six flows; its own "Covered by" table (`:100-106`) lists five (drops
     FL-020), as does `usecases/Index.md:114`. UC-004 does not list `[[FL-033]]` although FL-033
     claims to implement it.
- **Why this severity:** `CLAUDE.md`'s lookup order is index-first by design. An agent asked
  "which guarantees does this tenancy change touch?" opens `usecases/modules/Index.md`, finds no
  tenancy row, and concludes UC-028 does not exist — the exact failure the rule was written
  against.
- **Why this timing:** All five files are already modified in the working tree; the tenancy row
  is the one that was not added.
- **Close criteria:**
  - [ ] `usecases/modules/Index.md` has a `tenancy` row pointing at UC-028
  - [ ] `FL-033:4` and `flows/Index.md:106` name `[[UC-028]]`
  - [ ] `usecases/Index.md:138` and `:114` match the use cases' own headers
  - [ ] `UC-004`'s header and table agree, and both name `[[FL-033]]`

### GAP-7 [medium][immediate] Six navigation links to `decisions/` and `flows/` omit the `ai/` segment
- **Where:** `docs/Index.md:9, 11`; `docs/modules/en/Index.md:4, 5`; `docs/modules/ru/Index.md:4, 5`
- **Scale:** systemic (6 links, 3 files)
- **Confidence:** CONFIRMED — every relative link in `docs/**/*.md` and both READMEs resolved
  against the filesystem:
  ```
  docs/Index.md            -> decisions/Index.md        (resolves to docs/decisions/Index.md)
  docs/Index.md            -> flows/Index.md            (resolves to docs/flows/Index.md)
  docs/modules/en/Index.md -> ../../decisions/Index.md  (resolves to docs/decisions/Index.md)
  docs/modules/en/Index.md -> ../../flows/Index.md      (resolves to docs/flows/Index.md)
  docs/modules/ru/Index.md -> ../../decisions/Index.md
  docs/modules/ru/Index.md -> ../../flows/Index.md
  ```
  Real paths: `docs/ai/decisions/Index.md`, `docs/ai/flows/Index.md`.
- **What:** `docs/Index.md` is the documentation front door; two of its six rows 404. The module
  index — the page `README.md:1844` sends a consumer to — 404s on both cross-references in its
  opening sentence.
- **Why this severity:** `CLAUDE.md`'s lookup order is *flows index → decisions index → usecases
  index*. Two thirds of that route is unreachable by link from either entry point. For tenancy
  it is the only navigational path to D-116, D-117 and FL-033, none of which the reference links
  directly (it uses bare `[[…]]` wiki syntax).
- **Why this timing:** Both module `Index.md` files are already modified in the tree.
- **Close criteria:**
  - [ ] all six links carry the `ai/` segment
  - [ ] a link check runs over `docs/**/*.md` — there is none today (GAP-10)

### GAP-8 [medium][immediate] `_examples/README.md` states the wrong output count and contradicts itself two lines later
- **Where:** `_examples/README.md:53, 55-57`
- **Scale:** local (2 statements)
- **Confidence:** CONFIRMED — run:
  ```
  $ cd _examples && GOWORK=off go run ./tenancy-sharedrow
  invoices are narrowed by crud.Middleware[main.Invoice,int64]
  a request with no verified tenant: refused before any statement, and the message names no tenant
  a request for an active tenant: bound to active tenant 42
  its object namespace is a fixed-width digest: invoices-81b1212c808dc16396b0f84877d1d369
  a cohort grant covers 2 tenants, for reads only
  → 5 lines
  ```
- **What:** `:53` says "Run it and read the **four** lines it prints." It prints five. The same
  line calls it "**the only** example with no database"; `:55` calls it "**the second** example
  that needs no database" (`example/`, `:59-61`, is the first).
- **Why this severity:** A reader who counts four and stops skips the cohort-grant line — the
  only demonstration of the cross-tenant API in the repository. The only/second contradiction
  makes the sentence unusable either way.
- **Why this timing:** `_examples/README.md` is uncommitted (` M` in `git status`), so the wrong
  count has not shipped and is cheap to fix now.
- **Close criteria:**
  - [ ] `:53` says five or drops the count; `:53` and `:55` agree

### GAP-9 [high][immediate] `README.md` still says "Multi-tenancy is one line" and never mentions the extension
- **Where:** `README.md:738-782`; whole file 1849 lines
- **Scale:** local (one section — but it is the repository's front page)
- **Confidence:** CONFIRMED —
  ```
  $ grep -c 'vv/tenancy\|tenancyrow\|tenancydb\|D-116\|D-117\|FL-033\|modules/en/tenancy.md' README.md
  0
  $ grep -n '^#\{1,3\} ' README.md | grep -i tenan      → no output
  ```
  All 22 "tenan" hits are `TenantID` columns or `security.ScopeField` samples.
- **What:** `README.md:738` — "Multi-tenancy is one line:" followed by
  `security.ScopeField[Doc, int64]("TenantID", func(ctx) …ctx.Value(tenantKey{})…)`. That is the
  hand-wired form: an unverified value the application put in the context itself, no lifecycle,
  no generation, no durable identity, no object or cache reach. `README.md:1840-1849` ("Where to
  read next") has no row for it either. Both usage guides **do** carry the parallel note
  (`ent.md:1126-1132`, `gorm.md:1109`), so the README is the only surface still presenting the
  one-liner as the whole answer.
- **Why this severity:** For most readers the README is the only document. A team that ships
  `ScopeField` over a context key and later needs suspension, generations and tenant-scoped jobs
  has built the thing `UC-028:9-14` exists to replace.
- **Why this timing:** `CLAUDE.md`: "Added a public API → the flow, the use case,
  `docs/modules/<package>.md`". The module doc was written; the front page was not.
- **Close criteria:**
  - [ ] `README.md:738` distinguishes the hand-wired scope from the verified one, in the words
        the two usage guides already use, and links `docs/modules/en/tenancy.md`
  - [ ] `README.md:1840-1849` gains a row, or the security section carries the pointer

### GAP-10 [medium][immediate] The doc checker skips any citation to a file that does not exist
- **Where:** `scripts/docs_test.go` — `staleSymbolCitations`, `filesNamed`, `citedSymbols`,
  `TestEveryTestNameTheDocsCiteExists`
- **Scale:** systemic (one hole; it hid 6 stale citations in D-117 and 2 in the roadmap for as
  long as they stood)
- **Confidence:** CONFIRMED — read from the source:
  ```go
  for _, citation := range cited {
      files := filesNamed(citation.path, declared)
      if len(files) == 0 {
          continue            // ← a citation to a file that does not exist is SKIPPED, not failed
      }
      checked++
      …
  }
  ```
  `filesNamed` matches only against files that were walked, so a nonexistent path yields an empty
  slice. Second, `citedSymbols` records a citation only when the span matches `file.go:Symbol`;
  D-117 wrote `` `tenancy/jobs.go` — `JobContext`, … ``, which is never parsed as a citation.
  Third, `TestEveryTestNameTheDocsCiteExists` is satisfied by a name existing **anywhere** in the
  tree, which is why D-117's four wrong test-file paths passed while their names resolved.
  The name check does work when a name exists nowhere — it is what caught GAP-1.
- **What:** Two holes of the exact class a package split produces.
- **Why this severity:** `CLAUDE.md` treats a stale doc as a failing test. The test built to
  catch it was green on all six of D-117's dead pointers.
- **Why this timing:** Fixing the checker first makes GAP-1 and GAP-5 self-detecting and stops
  the next package move from re-creating them.
- **Close criteria:**
  - [ ] a citation naming a `*.go` path the tree does not hold **fails** rather than being
        skipped, with a control fixture proving the failure
  - [ ] a test-name citation that names a file is checked against **that file**
  - [ ] a bare `` `path/file.go` `` span with symbols in adjacent prose is either parsed or
        declared out of scope in a comment
  - [ ] a relative-link check exists (GAP-7 found 9 broken links in scoped docs and 63 elsewhere)

### GAP-11 [medium][deferred] `docs/api/surface.md` contains none of the tenancy packages' 58 exported methods
- **Where:** `docs/api/surface.md:1757-1841` (and by construction the whole file)
- **Scale:** systemic (58 methods absent from the tenancy section alone)
- **Confidence:** CONFIRMED — the committed section is byte-identical to a regeneration through
  the exact `go doc -short` pipeline in `scripts/modules.sh:api()`, so the file is *correct
  against its generator*. But:
  ```
  exported methods in `go doc -all` over ./tenancy/... : 58
  methods in surface.md's tenancy section              :  0
  ```
  Absent: `Authority.Bind`, `Authority.Each`, `Authority.Verify`, `Authority.Lookup`,
  `Authority.Scope`, `Authority.With`, `Authority.Accept`, `Authority.Sealer`, `Sealer.Seal`,
  `Sealer.Unseal`, `Scope.Digest`, `Directory.Borrow`, `Directory.Evict`, `Directory.Close`,
  `Directory.Cached`, `Lease.Source`, `Lease.Release`, `Key.Unwrap`, `Admission.Merge`, +39 more.
- **What:** `surface.md:3-5` states its own contract: *"after v0.1.0 a line that disappears from
  this file is a breaking change, and a line that changes shape is one too."* Removing
  `Authority.Each` or changing `Authority.Bind`'s signature changes no line in this file.
- **Why this severity:** The baseline covers constructors and types but not the operational API —
  for tenancy, the entire request path. `CLAUDE.md` says "a diff there is a question for a
  person"; there will be no diff to ask about.
- **Why this timing:** Deferred — a property of `make api` repository-wide, not a tenancy defect,
  and changing the generator is a decision. Reported once with a count rather than 58 times.
- **Close criteria:**
  - [ ] either `api()` uses a method-bearing form, or `surface.md`'s header states that methods
        are out of the baseline and names what else guards them

### GAP-12 [medium][deferred] The error section leaves `ErrMalformed` unexplained and overstates the resolver collapse; two RU translation drifts
- **Where:** `docs/modules/en/tenancy.md:311-334`, `docs/modules/ru/tenancy.md:317-340`;
  `docs/modules/ru/tenancy.md:5-6, 130-131`; `docs/modules/{en,ru}/tenancy.md:358/363`
- **Scale:** systemic (2 module docs)
- **Confidence:** CONFIRMED — `tenancy/errors.go:11`:
  `ErrMalformed = fmt.Errorf("…: %w", crud.ErrBadRequest)`.
- **What:**
  1. The doc lists all eleven sentinels (EN:317-321) and accounts for exactly ten: "The first
     seven wrap `crud.ErrForbidden`" (true), "`ErrPinned` wraps `crud.ErrConflict`" (true),
     "`ErrCapacity` and `ErrUnavailable` deliberately wrap neither" (true). `ErrMalformed` —
     the one that renders **400**, not 403 — is never mentioned again.
  2. "A resolver that fails for its own reasons produces `ErrUnavailable` and nothing else."
     `Classify` (`errors.go:47-57`) **preserves** any of the ten `refusals` a resolver
     deliberately returned — the behaviour `Fixed.Lookup` (`authority.go:149`) depends on, and
     which `D-117:53-57` and the code comment at `errors.go:39-46` both state. `Classify` is
     exported precisely for consumer seams (`D-116:65-66`) and is named **zero times** in either
     module doc.
  3. EN:358 / RU:363 label a link to FL-033 as **"jobs"**. There is no `docs/modules/*/jobs.md`
     at all (`ls docs/modules/en | grep -i job` → no output), so a consumer hunting the jobs
     reference — which `tenancyjobs` requires (`jobs.IdentityProvenance`, `jobs.IdentityEpoch`,
     `jobs.PartitionTenantRequired`) — is sent to a flow.
  4. RU:5-6 leaves the import block's comments in English while every other comment in the RU
     document is translated. RU:131 renders EN:130 "a scope … **does not verify** here" as
     "здесь **не проверяется**", which reads as *"is not checked"* — a skipped check rather than
     a refusal. The intended sense is "не пройдёт проверку".
- **Why this severity:** A transport that maps "all tenancy sentinels except
  Capacity/Unavailable" to 403 renders a malformed tenant reference as 403 instead of 400. A
  consumer writing a `Resolver` for an unknown tenant never learns that returning
  `tenancy.ErrUnmapped` is supported and preserved. And a RU reader can come away believing a
  foreign scope bypasses validation.
- **Why this timing:** Deferred — the correct accounts exist in `D-117:53-57` and `FL-033:142`,
  so each is recoverable; all four are cheap and ride along with GAP-2's pass over the same files.
- **Close criteria:**
  - [ ] the error section states `ErrMalformed` → `crud.ErrBadRequest` → 400
  - [ ] it names `Classify` and states that a resolver may deliberately return any of the ten
        refusal sentinels
  - [ ] EN:358 / RU:363 read `[FL-033]` or "the durable half of the flow", not "jobs"
  - [ ] RU:5-6 comments translated; RU:131 reads "здесь проверку не пройдёт"

### GAP-13 [low][deferred] FL-033's failure table attributes `ErrIncompatible` to a check the directory does not perform
- **Where:** `docs/ai/flows/FL-033…md:148`
- **Scale:** local (one table row)
- **Confidence:** CONFIRMED — the row reads "an unmapped, over-budget or **schema-incompatible**
  database | `Directory.Borrow` | `ErrUnmapped` / `ErrCapacity` / `ErrIncompatible`". In
  `tenancy/tenancydb/database.go`, `ErrUnmapped` comes from `open()` when the factory returns a
  nil source (`:190`), `ErrCapacity` from `reserve()` (`:170`), and `ErrIncompatible` **only**
  from `scope()` for an invalid `Class` value (`:212-214`). There is no schema check in `Borrow`;
  FL-033's own `:124-127` says correctly that the schema-vs-tenant question is the consumer's
  `Fence` — which is optional (`DirectorySpec.Fence`, `:31`; `open()` returns the source
  unfenced when it is nil, `:192-193`).
  (The row above it cited `jobIdentity.RestoreIdentity` at 00:15; the 00:35 edit corrected it to
  `identityRestorer.RestoreIdentity`.)
- **Why this severity:** A consumer reading the table concludes the directory validates schema
  compatibility, supplies no `Fence`, and gets a directory that hands back whatever the factory
  returned.
- **Why this timing:** Deferred — the correct account is in the same document at `:124-127`.
- **Close criteria:**
  - [ ] the row is split: `ErrUnmapped`/`ErrCapacity` from `Borrow`, `ErrIncompatible` from an
        invalid class, and the schema question attributed to the caller's `Fence`

---

# (2) What is MISSING

`CLAUDE.md`'s lookup table sends "How does a consumer set this up?" to `docs/usage-guides/`.
**There is no tenancy page there** — `ls docs/usage-guides/` → `ent.md gorm.md migrations.md
model-generation.md repository.md`. The two ORM guides carry a five-line parallel note
(`ent.md:1126-1132`, `gorm.md:1109`) that says the extension exists and links the module doc;
nothing else. That single absence causes most of the table: the module reference is a
*reference*, and every question that is a *setup* question has no home.

| # | Question a team adopting this will ask | Answered where | Verdict |
|---|---|---|---|
| 1 | How do I onboard the very first tenant, and what must my `Resolver` do? | `en/tenancy.md:76-105` gives the interface and `tenancy.Fixed`. `UC-028:82-83` puts onboarding **out of scope**. Nothing says what `Resolve` should return on a public/unauthenticated route (`public route\|unauthenticated route` across all 7 tenancy docs → **0**), nor whether `Resolve`/`Lookup` are called concurrently (`concurren\|goroutine\|thread.?safe` → **1 hit, roadmap only**) | **unanswered** |
| 1b | …how do I construct a `Reference`, an `Epoch`, a `Purpose` at all? | **Nowhere.** `ParseReference`, `NewEpoch`, `ParsePurpose` are named **zero times** across `en/tenancy.md`, `ru/tenancy.md`, `FL-033`, `UC-028`, `D-116`, `D-117` and the roadmap. The reference's own sample (`en:99-101`) uses bare `ref` and `epoch` identifiers it never defines. `MaxReferenceBytes`(128), `MaxPurposeBytes`(64), `MaxCohortSize`(10000), `MaxPrefixBytes`(30), `MinPartitionBytes`(16) are likewise never mentioned — the first line a consumer writes is undocumented, and so is every limit that will refuse it | **unanswered** |
| 2 | Where does the tenant come from on a request (subdomain / header / JWT claim), and how do I wire it with `authfiber`/`authgin`/`authnet`? | `FL-033:12-15` and `UC-028:80-81` say only that the host authenticates first and that tenancy "never sees a token, a header or a router". `subdomain` across all tenancy docs → **0**. `authfiber\|authgin\|authnet` → **0**. `_examples/auth-jwt-gin` shows a tenant claim into `security.ScopeField`, not into `tenancy`; `_examples/tenancy-sharedrow` has no HTTP at all | **unanswered** |
| 3 | Where does `Bind` go in a middleware chain, and what is the ordering constraint against auth? | Partially: `en:61-65` "once the host has authenticated"; `FL-033:12-15` the same. Neither shows a chain, names a binding, or says what happens if `Bind` runs before auth or inside the handler | **partially** |
| 4 | How do I run migrations — one shared schema or one per tenant? | **Nowhere.** `docs/usage-guides/migrations.md` (56 lines): **0** occurrences of "tenant". `docs/modules/{en,ru}/vvgoose.md`: **0**. `en/tenancy.md`: **0** occurrences of "migration". `vvgoose.Execute(&cfg.DB)` takes one DB config; a `tenancydb` deployment needs N and no document says how. `UC-028:86-88` puts "migrate" among out-of-scope operator procedures | **unanswered** |
| 5 | How do I test my own tenant-owned repository? What replaces a scope constructor? | In principle: `en:95-105` — "A test builds one the way production does", with the `tenancy.Fixed` sample; `D-117:69-74` explains why there is no test constructor. In practice: `crudtest` is named **0** times in every tenancy doc, and nothing shows a `crudtest.Postgres()` recorder wired to a `tenancyrow.Repository` — which is exactly what `tenancy/tenancyrow/row_test.go` does | **partially** |
| 6 | How do I run an admin/ops query across all tenants, and a background job for one? | Best-covered. Cross-tenant: `en:291-308`, `D-117:50-52`, `UC-028:62-66`. Per-tenant durable: `en:211-250`, `FL-033:67-94`, `D-117:44-49`. Two caveats: the headline sample is a grant accepted for `ClassRead` then run with `ClassWrite`, i.e. a refusal presented as usage (`en:294-296`); and GAP-3 | **answered, with GAP-3** |
| 7 | What is the performance cost, and what indexes do I need on the tenant column? | **Nowhere.** `\bindex\b\|performance\|latency\|overhead\|benchmark` across `en/tenancy.md`, `ru/tenancy.md`, `FL-033`, `UC-028`, `UC-004`, `D-116`, `D-117` and the roadmap → **0 hits in all eight**. The extension prepends `tenant_id = ?` to every statement including page-total `COUNT`s and preload second statements, and no document mentions an index once | **unanswered** |
| 8 | How do I choose between shared-row and database-per-tenant, and can I migrate between them? | Choice: partially — `en:36-43` (the cost table), `en:254-288`, `roadmap:481-500` (shared-row only) — but the reference does not say so (GAP-4). Migration between topologies: **nothing, in any document** | **partially** |
| 9 | What is NOT protected? | The reference's "What it does not cover" (`en:338-352`) has 4 items; `roadmap:499` "Known exclusions" has 8. In both: `Tx`, assigned-key `Save`, the pagination-cursor oracle, gate-over-gate scoped writes (the last at `en:203-206` rather than in the list). **Absent from the reference:** PostgreSQL RLS; operator procedures (suspend, migrate, restore, delete, legal hold); pre-tenancy rows ("Refused at runtime. Backfilling a null or default owner is an operator procedure", `roadmap:495`); the unique-constraint create oracle as an item in its own right; and the database-per-tenant profile itself (GAP-4). The reference adds one the profile does not: raw statements / `crud.UnsafeExecFor` | **partially — 5 of 9 missing** |
| 10 | What happens on a tenant suspend / restore / delete, operationally? | Mechanism: answered — `en:132-148` (`Revalidate`), `en:244-250` (generation in the digest), `FL-033:83-94`, `D-117:76-80`. Procedure: **explicitly out of scope** (`UC-028:86-88`) and open at M5 (`Roadmap.md:302-304`). Missing is the reference saying so: a consumer reading `en/tenancy.md` alone learns the fence and never learns the rehearsals were not run | **partially** |
| 11 | How do I observe it — metrics, `OutcomeFor`, and what must never become a label? | Best in the set. `en:333-334`/`ru:338-339` name `OutcomeFor` and "one of twelve closed constants, which is what a dashboard or a metric label may carry" (verified: `tenancy/outcome.go:7-20` declares exactly 12). `en:313-314` and `:328-331` state what a refusal never carries; `roadmap:306-317` has the full negative list. Gap: `Outcomes()` — the enumerator a metric registry needs — is never named in either module doc, nor is `OutcomeFor(nil) == OutcomeOk` | **answered** |

**Score: 1 answered, 4 partially, 6 unanswered.**
The highest-value missing artefact is `docs/usage-guides/tenancy.md`: it would carry questions
1, 1b, 2, 3, 4 and 5 — five of the six unanswered rows.

---

# (3) Is the RU translation faithful?

**Yes, structurally and semantically, with the two drifts folded into GAP-12.** Measured, not
eyeballed:

| Check | Result |
|---|---|
| Sections | **15 EN / 15 RU, 1:1 in order.** No section exists in one language only |
| Per-section non-blank line delta | max **+2** (`Ownership…`, `The other seams`) — Russian wrapping variance; nothing added or dropped |
| Code fences | 28 / 28 |
| Fenced content | **identical** except translated comments and the Cyrillic `Fence:` identifier (diffed by extracting all fenced content from both) |
| Inline code spans outside fences | **identical multiset**: `set(EN)-set(RU)` = ∅, `set(RU)-set(EN)` = ∅, and **no span's occurrence count differs** |
| `[[…]]` cross-references | **identical multiset**: `D-116`×3, `D-117`×2, `D-008`, `D-007`, `D-030`, `FL-005`, `UC-012` |
| Markdown links | the same 3 in both |
| Does either language document behaviour the other does not? | **No.** Every factual assertion checked appears in both — the tables, the `Origin` note, the `Revalidate` trade, the `Combine` recommendation, the `MaxCached`/`TTL`/`Authority` rationale, all four "does not cover" items |

Both RU-only snippets compile: the Cyrillic `Fence: этоДействительноЕгоБаза` identifier and the
whole RU `DirectorySpec` literal, transcribed verbatim, build against the real module.

The RU document is faithful **to a source that contains errors**: it reproduces GAP-2
(`ru:123-126`), GAP-3 (`ru:297-314`), GAP-4 (`ru:260-294`) and GAP-12 (`ru:317-340`, `ru:363`)
exactly. Fixing EN without RU would break a parity that currently holds.

---

## Remediation order

Dependencies first: the red check before anything, the checker before the citations it should
have caught, the fence before the pages built on it.

| # | Work | Size | Blast radius | Why here |
|---|---|---|---|---|
| 1 | **GAP-1** — decide whether `Seal` gains `Origin` + durable-class admission, or D-117:139-151 is corrected; fix `:53` | **M** (S if doc-only; **L** if the code changes — the seal MAC is a wire format every queued record already carries) | `docs/ai/decisions/D-117` and possibly `tenancy/seal.go` + every durable record in flight | The doc checks are RED, and a binding decision currently promises a security property that does not exist. Nothing else should land first |
| 2 | **GAP-10** — close the nonexistent-file hole in `scripts/docs_test.go` | S | `scripts/` only | Makes #1, #5 and any future package move self-detecting rather than found by hand |
| 3 | **GAP-2** — correct the admission-consistency claim in EN + RU | S | 2 paragraphs | Behavioural claim about start-up in the consumer contract; independent |
| 4 | **GAP-3** — document `Each`'s error contract in EN, RU, UC-028 | S | 3 docs | Silent-wrong-result on the cross-tenant API; same EN/RU pass as #3 |
| 5 | **GAP-4** — fence db-per-tenant in EN + RU; complete "What it does not cover" against `roadmap:499` | M | 2 docs; forces re-evaluating roadmap row 12 | Decides what the reference *claims*; #7 and #9 both depend on the answer |
| 6 | **GAP-5 + GAP-6 + GAP-7** — `CacheScope`/"no package per seam"; the missing `tenancy` index row and the UC-028↔FL-033 reverse links; the six `ai/` links | S | 8 docs, all already modified in the tree | Link and index hygiene; what makes #5 and #9 reachable by navigation at all |
| 7 | **GAP-9** — README's "Multi-tenancy is one line" | S | one README section | Depends on #5: the README must not point at a topology the reference fences |
| 8 | **GAP-8** — `_examples/README.md` five lines, only/second | S | 1 doc, uncommitted | Independent; fold into any pass |
| 9 | **`docs/usage-guides/tenancy.md`** — the missing setup page (questions 1, 1b, 2, 3, 4, 5) | **L** | new doc + rows in `usage-guides/`, `docs/Index.md`, both module indexes; must follow the parallel style `CLAUDE.md` requires of that directory | The largest single gap. Depends on #5 (must not document an unadvertised topology as setup) and on #3/#4 (it will restate those contracts) |
| 10 | **GAP-12 + GAP-13** — `ErrMalformed`/`Classify`, the "jobs" label, the two RU drifts, FL-033's `ErrIncompatible` row | S | 3 docs | Deferred cleanup; rides along with whichever pass touches those files |
| 11 | **GAP-11** — the `make api` method gap | M | `scripts/modules.sh` + all 2717 lines of `surface.md`; a repository-wide decision | Last: it is a question for a person and it regenerates the whole baseline |

---

## What I did not check

**The test suite's state, and why I report no test failure as a finding.** A concurrent process
was mutating the tree throughout. Proof:

```
$ h1=$(find . -name '*.go' … | md5sum); go test -count=1 ./tenancy/...; h2=$(…)
before=cd05874951b0b89a4eb3e1d3cdfead0d  after=f241b7bac3a54d02d6ca7682cfca8e0c
!!! WHOLE TREE MUTATED DURING RUN
$ find . -name '*.go' -mmin -15
./tenancy/scope.go ./tenancy/grant.go ./tenancy/seal.go
./tenancy/tenancyrow/row.go ./tenancy/tenancydb/database.go
```

At one point `tenancy/tenancyrow/row.go:77` read `crud.Eq("Total", value)` in place of
`crud.Eq(this.owner.Name, value)` — an injected mutation that would narrow every tenant query on
the wrong column; it was back to the correct form minutes later. Successive `go test
./tenancy/...` runs therefore reported different failures each time, and one test passed in
isolation while failing in the package run. **I therefore assert no result about
`go test ./tenancy/...`, and I can neither confirm nor refute the roadmap's definition-of-done
row 10 ("the `tenancy` and `crud` unit suites … are green").** `go test ./scripts/...` was
stable and reproducible across four runs and is the only suite result I rely on (GAP-1).

Every code fact asserted above was re-verified after 00:36 against the live tree or a snapshot
(`scratchpad/tenancy_snap/`, `scratchpad/seal_at_audit.go`, md5s recorded); the four behavioural
claims (GAP-1 ×2, GAP-2, GAP-3) were proved by executable tests, two of them with controls.

**Also not checked:**

- `test/integration/tenancy_test.go` was not run — it needs Docker (`make up`), and the audit map
  records two pre-existing `vvdb` failures unrelated to tenancy. I verified only that the file
  exists and holds the two functions FL-033:197 and the roadmap cite.
- `make check`, `make api`, `make examples`, `make vet` were not run as such; I reproduced the
  `api` generator's tenancy output directly and ran `./tenancy-sharedrow` and `./scripts/...`.
- **Prose accuracy of `FL-007` and `FL-008`.** Both are in scope by name; I confirmed they exist,
  are linked from UC-004/UC-028 and are in the flows index, but I did not verify their step
  lists against `crud/decorators/security/security.go` (1102 lines). `FL-008` is modified in the
  working tree.
- `D-115`'s argument beyond its Proven-by (all 4 test names and all 5 file paths verified). Its
  reasoning about `ExistsUnscopedOf` was not re-derived from `crud/executor.go`.
- `docs/usage-guides/{ent,gorm}.md` beyond the §13 tenancy paragraph and its link target.
- **Whether D-116 and FL-033 acquired new defects in their 00:35–00:36 rewrites.** I re-ran the
  path sweep (0 stale) and the test-name sweep (D-116: 2/2 OK) after them, and re-read FL-033's
  failure table, but I did not re-read either document end to end afterwards. D-117 acquired two
  false claims in that window; the same may be true of the other two.
- **RU translations of the non-module docs.** `D-115/116/117`, `FL-033`, `UC-004`, `UC-028`, the
  roadmaps and all usage guides are EN-only. `docs/Index.md:8` offers en/ru only for `modules/`,
  which suggests that is intended rather than a gap — but it is not written down anywhere.
- **`_examples/otel-sdk-bootstrap/`** exists on disk and appears in no row of
  `_examples/README.md`'s matrix and nowhere in its prose. Out of dimension; noted because it is
  the same defect class as GAP-6 and surfaced from the same sweep.
- Whether the **63 non-tenancy broken relative links** found by the sweep are known: 7 roadmap
  "historical snapshot" links, 8 in `roadmaps/Index.md`, `modules/*/access.md` → three
  nonexistent binding pages, `modules/*/storage.md` → a missing roadmap, `modules/*/vvdb.md` →
  `../../flows/FL-021…`. Only the tenancy-reachable ones are in the findings.
- I did not verify that all **106 exported identifiers** behave as some doc describes; I verified
  signatures, struct fields, constant values, and the four behavioural claims the docs make
  load-bearing.
