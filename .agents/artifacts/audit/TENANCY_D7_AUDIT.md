# tenancy tests — Dimension 9 (test suite quality and mutation resistance) — AUDIT (2026-09-06)

Scope audited: `tenancy/**/*_test.go`, `test/integration/tenancy_test.go`,
`scripts/tenancy_test.go`, and the tenancy-relevant tests in
`crud/decorators/security/`. Source under mutation: `tenancy/**` only.
Nothing was fixed. Every mutation was applied one at a time and restored;
the restore was verified by SHA-256 after each one.

---

## Map — what the suite actually is

| Layer | Location | Contents |
|---|---|---|
| Core unit tests | `tenancy/*_test.go` (8 files, 1027 lines) | `binding_test`, `contract_test`, `grant_test`, `grant_binding_test`, `scope_test`, `seal_test`, `vocabulary_test`, `support_test`. Split between `package tenancy` (internal: `binding_test`, `seal_test`, `grant_binding_test`, `support_test`) and `package tenancy_test` (external). |
| Row seam | `tenancy/tenancyrow/*_test.go` (705 lines) | Everything runs against `crudtest.Postgres()`, a **recorder**: it captures SQL and replays canned rows. No database. |
| DB-per-tenant | `tenancy/tenancydb/*_test.go` (666 lines) | Recording `Sources` factory (`openings`), injected clock, 6 concurrency tests. No database. |
| Jobs seam | `tenancy/tenancyjobs/*_test.go` (509 lines) | `jobsmemory` backend, real `jobs` catalog/queue. No queue table. |
| Storage / cache seams | 154 + 155 lines | Internal (`package tenancystorage` / `package tenancycache`) tests that call the **unexported** `namespaceOf` / `Partition` directly. |
| Structural | `scripts/tenancy_test.go` (107 lines at audit time; rewritten to 131 lines by a concurrent agent at 00:44) | 3 tests: no base package imports the extension; no adapter costs more than its seam; importing does nothing. Build-graph checks, **cannot detect behaviour**. |
| Live database | `test/integration/tenancy_test.go` (165 lines) | **2 test functions, 5 subtests**, `//go:build integration`, Postgres only. |
| End-to-end wiring | `_examples/tenancy-sharedrow/main.go` (165 lines) | Compiled and vetted by `make examples`; **`[no test files]`** — its 5 printed lines are asserted by nobody. |

Composition root for tests: none. Every test builds its own `*tenancy.Authority`
through `tenancy.New` with `tenancy.Fixed` or a hand-written resolver
(`directory`, `knownTenants`, `switchable`, `counting`, `failingResolver`).
State under test: the `Directory` mutex-guarded map (`tenancydb`) and the
per-authority salt; everything else is stateless per call.

---

## Baseline

```
$ go test -count=1 ./tenancy/... ./scripts/...
ok  	github.com/frostgrove/vv/tenancy	0.004s
ok  	github.com/frostgrove/vv/tenancy/tenancycache	0.002s
ok  	github.com/frostgrove/vv/tenancy/tenancydb	0.144s
ok  	github.com/frostgrove/vv/tenancy/tenancyjobs	0.002s
ok  	github.com/frostgrove/vv/tenancy/tenancyrow	0.002s
ok  	github.com/frostgrove/vv/tenancy/tenancystorage	0.004s
ok  	github.com/frostgrove/vv/scripts	2.677s
```

Green at 00:16. **Red at 00:39**, in the same working tree, without any change of
mine — see GAP-1. Green again at 00:47, after a concurrent agent fixed it.

Counts, from `go test -count=1 -v ./tenancy/...`:

```
$ grep -c '^=== RUN' baseline_v.txt      -> 170
$ grep '^=== RUN' | grep -vc '/'         ->  90   (top-level Test functions)
$ grep '^=== RUN' | grep  -c '/'         ->  80   (subtests)
```

Per package: `tenancy` 38, `tenancyrow` 17, `tenancydb` 16, `tenancycache` 7,
`tenancystorage` 6, `tenancyjobs` 6 top-level functions.
Plus `scripts` 3 (+2 subtests) and `test/integration` 2 (+5 subtests).

Determinism:

```
$ go test -count=5 -race ./tenancy/...            all ok
$ go test -count=20 -race ./tenancy/tenancydb/    ok  3.895s
$ go test -count=20 -race -cpu=1 ./tenancy/tenancydb/  ok  3.899s
$ (8 spinner processes) go test -count=10 -race ./tenancy/tenancydb/  ok 2.465s
$ go vet ./tenancy/... ./scripts/...              clean
$ gofmt -l tenancy scripts test/integration crud/decorators/security   (empty)
```

Coverage:

```
$ go test -cover ./tenancy/...
tenancy 79.9%  tenancycache 73.3%  tenancydb 87.6%
tenancyjobs 80.4%  tenancyrow 79.8%  tenancystorage 71.4%
$ go test -coverpkg=./tenancy/... -coverprofile=... ./tenancy/...
total: 83.7% of statements
```

Exported symbols at **0.0%** of statements across the whole tenancy tree:
`Authority.Must`, `Authority.Admits`, `Grant.Purpose/Size/String/Format/LogValue/MarshalJSON`,
`Purpose.String/IsZero`, `Resolution.String/Format`, `Scope.String/Format/LogValue`,
`Reference.LogValue`, `tenancycache.Key.Unwrap/Scope/String`, `tenancycache.Partitioned`,
`tenancydb.SourcesFunc.Source`, `tenancydb.Directory.Close`, `tenancystorage.Store`.

---

## Scorecard

| Rubric item | Verdict | Evidence |
|---|---|---|
| Mutation resistance | **at risk** | 71 mutations, **50 caught, 21 survived** (kill rate 70.4%). Every survivor is a genuine, non-equivalent behaviour change. |
| Vacuous assertions | **at risk** | Relation narrowing asserted by `strings.Contains(clause, "tenant_id")` only: M41 and M42 replace the value with a *different tenant's* and both survive. |
| `errors.Is` vs error strings | **pass** | Zero equality/`Contains` assertions on error text. The only `.Error()` use, `scope_test.go:156`, is a *negative* leak assertion. `errors.Is` appears 45× across 11 test files. |
| Controls next to vacuously-passable tests | **pass** | 18 explicit control assertions across 9 of 11 test files ("the control refuses too, so the assertion above proves nothing", "ran no statement, so this proves nothing", "no engine was walked, so this test measured nothing"). |
| Timing-based concurrency tests | **at risk** | 4 tests turn on `time.Sleep`/`holdFor` (`tenancydb/database_test.go:407,434,459,499`). They held under `-count=20 -race -cpu=1` and under 8-way CPU contention, but their assertions become silently vacuous rather than red under enough slowdown. |
| `t.Fatalf` from non-test goroutines | **fail** | 4 goroutines call `bound(t,…)`/`boundTo(t,…)`, both of which `t.Fatalf` — `database_test.go:414, 441, 478, 504`. |
| Doubles more permissive than production | **fail** | `crudtest.Recorder` returns pushed rows regardless of the `WHERE` clause. Every unit isolation assertion is about *emitted SQL text*, never about *rows excluded*. |
| Skips / xfails | **pass** | `grep -rn 't\.Skip\|Skipf\|testing.Short' tenancy test/integration/tenancy_test.go scripts/tenancy_test.go` → no matches. |
| `t.Parallel()` in integration | **pass** | none (`CLAUDE.md` rule honoured). |
| Asserting on internal calls | **judged acceptable** | `revalidate_test.go` counts `resolver.lookups`. This is the only way to separate "refused by the control plane" from "refused by the carried scope"; it asserts an observable I/O effect, not a call sequence. Not raised as a gap. |
| Determinism (`-count=5 -race`) | **pass** | green, see above. |
| Live-database breadth | **fail** | 2 functions, 5 subtests, 7 of 18 gate verbs, 1 of 2 ownership strategies, 1 of 2 topologies, numeric owner column only, no relation/preload, no grant, no revalidation, no durable, no object, no cache. |
| Stated-invariant coverage | **fail** | INV-8 (through), INV-11, INV-20, INV-23, INV-25 and INV-9 each have a hole a mutation walks through — see Findings. |

---

## Metrics

| Metric | Threshold | Actual | Worst offenders |
|---|---|---|---|
| Mutations survived | 0 | **21 / 71** | M06, M17, M41, M42, M44–M48, M50, M52, M53, M56–M60, M62, M64, M65, M68 |
| Live-DB test functions for the priority topology | — | **2** | `test/integration/tenancy_test.go:50, 143` |
| Gate verbs with a live tenancy test | 18 | **7** | missing: `Get`, `First`, `Aggregate`, `ExistsUnscoped`, `InsertBatch`, `SaveAll`, `SaveOnly`, `UpdateAll`, `DeleteAll`, `Restore`, `Tx` |
| Gate verbs with **no** tenancy test at any level | 0 | **2** | `Restore`, `ExistsUnscoped` |
| Ownership strategies ever executed by a database | 2 | **1** | `tenancyrow.Through` appears in no file outside `tenancy/tenancyrow/row_test.go` |
| Exported tenancy symbols at 0% statement coverage | 0 | **21** | `tenancystorage.Store`, `tenancydb.Directory.Close`, `tenancycache.Partitioned`, `Scope.LogValue`, `Reference.LogValue` |
| `t.Fatalf` reached from a non-test goroutine | 0 | **4** | `tenancydb/database_test.go:414, 441, 478, 504` |
| Timing-dependent tests | 0 | **4** | `database_test.go:407, 434, 459, 499` |
| Duplicated test-support helpers | — | **9 helpers across 6 packages** | `reference`/`activeAuthority`/`authorityFor` ×3, `fixedAuthority` ×3, `directory`/`manyTenantAuthority`/`knownTenants`/`bound` ×2 |
| Doc-cited test names that do not exist | 0 | **2** at 00:39, **0** at 00:47 | `D-117:148, D-117:151` — closed mid-audit by a concurrent agent |
| Composition-root (example) tests | — | **0** | `_examples/tenancy-sharedrow` → `[no test files]` |

---

## Mutation table

Method: one edit at a time to `tenancy/**`, `go test -count=1 -v`, restore, verify
SHA-256. M01–M40 ran against `./tenancy/... ./scripts/...`; M41–M71 against
`./tenancy/...` only, after the `scripts` package proved to be a source of
false CAUGHT verdicts under concurrent-agent interference (it shells out to
`go list`; see Methodology). Every CAUGHT verdict below names a behavioural
tenancy test, never a `scripts` structural test.

| # | Mutation | file:line | Caught? | By which test | UC / INV left unproven |
|---|---|---|---|---|---|
| M01 | `Scope.boundTo` always returns true | `tenancy/scope.go:61` | yes | `TestOnlyTheAuthorityThatMintedAScopeAcceptsIt`, `TestTheOriginFencesOneDeploymentFromAnother`, +3 | — |
| M02 | `bind()` ignores `Origin` | `tenancy/scope.go:66` | yes | `TestTheOriginIsPartOfTheBindingAndNotJustTheSalt` | — |
| M03 | `bind()` ignores `Epoch` | `tenancy/scope.go:71` | yes | `TestEveryFieldOfAResolutionIsPartOfItsBinding/another_generation` | — |
| M04 | `bind()` ignores `Lifecycle` | `tenancy/scope.go:70` | yes | `TestEveryFieldOfAResolutionIsPartOfItsBinding/another_lifecycle` | — |
| M05 | `Admission.Admits` always true | `tenancy/lifecycle.go:95` | yes | 13 tests incl. `TestOnlyAnAdmittedLifecycleReachesWork` | — |
| M06 | `inconsistent()` always `LifecycleUnknown` | `tenancy/lifecycle.go:110` | **NO** | — | UC-9 / INV-15 — the `New()` guard that refuses a write-without-read admission policy |
| M07 | `With()` drops the pinning check | `tenancy/context.go:28` | yes | `TestBindingASecondTenantInsideBoundWorkRefuses`, `TestTheUnitOfWorkPinCoversTheGenerationAsWellAsTheTenant` | — |
| M08 | `Scope()` skips `permittedByGrant` | `tenancy/context.go:51` | yes | `TestAReadGrantCannotWriteAtEitherGuard`, `TestAContextCarriedOutOfAnExpiredGrantStopsWorking` | — |
| M09 | `Scope()` never revalidates | `tenancy/context.go:54` | yes | `TestRevalidationStopsTheNextVerbAfterTheControlPlaneMoves` | — |
| M10 | `column.Narrow` names ANOTHER tenant's value | `tenancy/tenancyrow/row.go:77` | yes | `TestEveryVerbNarrowsToTheScopesOwnTenantAndNoOther` (all 9 subtests) | — |
| M11 | `column.Narrow` names a different column | `tenancy/tenancyrow/row.go:77` | yes | `TestATenantSeesEveryRowItOwns` + 3 | — |
| M12 | `column.Narrow` returns a tautology | `tenancy/tenancyrow/row.go:77` | yes | 10 tests incl. `TestATenantSeesEveryRowItOwns` | — |
| M13 | `column.Apply` returns nil always | `tenancy/tenancyrow/row.go:99` | yes | `TestACreateForAnotherTenantIsRefusedWhicheverModeIsDeclared` (both modes) | — |
| M14 | `column.Frozen` returns nil | `tenancy/tenancyrow/row.go:97` | yes | `TestAMutationCannotMoveARowBetweenTenants` | — |
| M15 | `relationScopes` always returns nil | `tenancy/tenancyrow/row.go:248` | yes | `TestAPreloadOfADeclaredRelationCarriesTheTenant` | — |
| M16 | `through.Apply` admits a create | `tenancy/tenancyrow/row.go:185` | yes | `TestARowOwnedThroughARelationIsNarrowedByThatRelation/and_a_create_is_refused…` | — |
| M17 | `through.Frozen` returns nil | `tenancy/tenancyrow/row.go:171` | **NO** | — | **UC-28 / INV-8** — the local FK that points at the owner is no longer immutable |
| M18 | `Unseal` skips `hmac.Equal` | `tenancy/seal.go:76` | yes | `TestAForgedDurableRecordEntersNoHandler` +3 | — |
| M19 | `Seal` drops the binding fields | `tenancy/seal.go:52` | yes | `TestBindingFieldsCannotBeSlidPastOneAnother` +6 | — |
| M20 | `grantBinding` drops the cohort | `tenancy/grant.go:198` | yes | `TestEveryPartOfAGrantIsInsideItsBinding` (3 subtests) | — |
| M21 | `Each` skips the per-member deadline re-check | `tenancy/grant.go:136` | yes | `TestAGrantThatExpiresMidRunStopsAndReportsWhatFinished` | — |
| M22 | `reserve` drops the `len >= max` check | `tenancy/tenancydb/database.go:171` | yes | `TestTheCacheIsBoundedRatherThanGrowingWithTenants` +2 | — |
| M23 | `release` never closes an evicted source | `tenancy/tenancydb/database.go:231` | yes | `TestEvictionWaitsForTheLastBorrower` | — |
| M24 | `finish` never evicts a failed open | `tenancy/tenancydb/database.go:207` | yes | `TestADatabaseTheFenceDoesNotRecogniseIsRefused` | — |
| M25 | `Classify` returns the resolver's own error | `tenancy/errors.go:51` | yes | `TestAResolverFailureNeverTravelsBackAsText` | — |
| M26 | `Scope.Digest` drops the generation | `tenancy/scope.go:88` | yes | `TestTheDigestSeparatesTenantsAndGenerations` +2 | — |
| M27 | `Partition` returns a constant | `tenancy/tenancycache/cache.go:51` | yes | `TestTwoTenantsWithEqualCacheKeysStayApart` | — |
| M28 | `holds()` skips `hmac.Equal` | `tenancy/grant.go:167` | yes | `TestAGrantFromAnotherAuthorityIsNotHeldHere` | — |
| M29 | `mint` skips the admission check | `tenancy/authority.go:111` | yes | `TestOnlyAnAdmittedLifecycleReachesWork` +5 | — |
| M30 | restorer skips the generation comparison | `tenancy/tenancyjobs/jobs.go:92` | yes | `TestWorkEnqueuedBeforeASuspensionDoesNotRunAfterIt/restored_to_a_new_generation` | — |
| M31 | `column.Apply` overwrites a foreign owner on create | `tenancy/tenancyrow/row.go:112` | yes | `TestACreateForAnotherTenantIsRefusedWhicheverModeIsDeclared/derive` | — |
| M32 | `Reference.valid` accepts control/space | `tenancy/reference.go:48` | yes | `TestAReferenceIsRefusedWhenItCouldNotSurviveAKeyOrANamespace` | — |
| M33 | `reserve` never sweeps expired entries | `tenancy/tenancydb/database.go:165` | yes | `TestABindingIsGivenBackAfterItsBorrowerLifetime` | — |
| M34 | `Lease.Release` loses its idempotence guard | `tenancy/tenancydb/database.go:77` | yes | `TestOneLeaseReleasedFromManyGoroutinesCountsOnce` | — |
| M35 | `Sealer()` accepts a missing/short durable key | `tenancy/seal.go:28` | yes | `TestADeploymentWithNoDurableKeySealsNothing` | — |
| M36 | `Directory.scope` always asks for `ClassRead` | `tenancy/tenancydb/database.go:218` | yes | `TestADatabaseIsNotHandedToAScopeTheClassNoLongerAdmits` | — |
| M37 | the cache key drops the generation | `tenancy/tenancydb/database.go:124` | yes | `TestARotatedGenerationDoesNotReachTheBindingItReplaced` | — |
| M38 | the durable record drops the job definition | `tenancy/tenancyjobs/jobs.go:106` | yes | `TestADurableRecordCannotBeReplayedIntoAnotherJobOrQueue/into_another_job` | — |
| M39 | the durable record drops the queue namespace | `tenancy/tenancyjobs/jobs.go:105` | yes | `TestADurableRecordCannotBeReplayedIntoAnotherJobOrQueue/into_another_queue` | — |
| M40 | `namespaceOf` ignores the scope entirely | `tenancy/tenancystorage/storage.go:49` | yes | `TestAnObjectNamespaceIsInjectiveAndNoTenantsIsAPrefixOfAnothers` | — |
| M41 | `column.Relations` narrows the preload to ANOTHER tenant | `tenancy/tenancyrow/row.go:90` | **NO** | — | **UC-24, UC-30 / INV-11** — the preload's second statement is asserted to *mention* `tenant_id`, never to name *this* tenant |
| M42 | `through.Narrow` names ANOTHER tenant's value | `tenancy/tenancyrow/row.go:156` | **NO** | — | **UC-19..UC-23 / INV-11** for the relation-owned resource |
| M43 | `through.Relations` returns nothing | `tenancy/tenancyrow/row.go:159` | yes | `TestARowOwnedThroughARelationIsNarrowedByThatRelation` | — |
| M44 | `Purpose.valid` accepts control/space | `tenancy/grant.go:40` | **NO** | — | INV-26 — a purpose is a label that reaches a signal vocabulary |
| M45 | `Accept` drops the `MaxCohortSize` bound | `tenancy/grant.go:86` | **NO** | — | UC-81 / INV-20 — "a grant names 1..10000 tenants" |
| M46 | `Accept` stops de-duplicating the cohort | `tenancy/grant.go:98` | **NO** | — | UC-82 / INV-21 — a duplicated member is entered twice, so "exactly once" fails |
| M47 | `Each` ignores context cancellation mid-run | `tenancy/grant.go:140` | **NO** | — | UC-80 / INV-21 — a cancelled cohort run keeps entering tenants |
| M48 | `open` accepts a nil source from the factory | `tenancy/tenancydb/database.go:190` | **NO** | — | UC-42 / INV-6 — `(nil, nil)` yields a `Lease` with a nil `Source` instead of `ErrUnmapped` |
| M49 | `open` shares the caller's cancellation | `tenancy/tenancydb/database.go:184` | yes | `TestABorrowerThatGivesUpDoesNotFailTheOnesBesideIt` | — |
| M50 | `Directory.Close()` does nothing | `tenancy/tenancydb/database.go:264` | **NO** | — | INV-23 — shutdown leaks every pool and a closed directory keeps serving |
| M51 | `Scope.MarshalJSON` serialises the tenant | `tenancy/scope.go:46` | yes | `TestNothingCarryingATenantRendersIt/the_scope` | — |
| M52 | `Scope.LogValue` logs the tenant reference | `tenancy/scope.go:44` | **NO** | — | **UC-86 / INV-25** — "no log field contains a tenant reference" |
| M53 | `Reference.LogValue` logs the tenant reference | `tenancy/reference.go:31` | **NO** | — | **UC-86 / INV-25** |
| M54 | `classOf` always answers `ClassRead` | `tenancy/tenancyrow/row.go:260` | yes | `TestTheRepositorySeamAsksForTheClassOfTheVerb` | — |
| M55 | `Namespace` ignores the class it was asked for | `tenancy/tenancystorage/storage.go:21` | yes | `TestANamespaceIsRefusedWhenTheClassIsNot` | — |
| M56 | the namespace digest is narrowed to 4 bytes | `tenancy/tenancystorage/storage.go:14` | **NO** | — | INV-18 — injectivity is sampled at 500 tenants, so any digest width ≥ 4 bytes passes |
| M57 | `Lookup` accepts a resolution naming a different tenant | `tenancy/authority.go:98` | **NO** | — | **UC-9 / INV-2** — a control plane answering about tenant B mints a verified scope for B under A's reference |
| M58 | `mint` drops the class validity check | `tenancy/authority.go:105` | **NO** | — | UC-17 / INV-6 — `Class(9)` is never presented |
| M59 | revalidation drops the reference/validity check | `tenancy/context.go:65` | **NO** | — | **UC-18 / INV-2, INV-16** — the same hole as M57, on the revalidation path |
| M60 | `From()` drops the zero-scope guard | `tenancy/context.go:78` | **NO** | — | INV-1 — defence in depth against a zero `Scope` reaching the context |
| M61 | `reserve` never shares an open | `tenancy/tenancydb/database.go:168` | yes | `TestOneTenantSelectsOneDatabaseAndKeepsIt` | — |
| M62 | `Evict` evicts every tenant, not the one named | `tenancy/tenancydb/database.go:241` | **NO** | — | UC-48 / INV-23 — an operator evicting one tenant tears down every pool |
| M63 | `unlink` forgets to mark the entry evicted | `tenancy/tenancydb/database.go:257` | yes | `TestEvictionWaitsForTheLastBorrower` | — |
| M64 | `Admit` drops the class validity guard | `tenancy/lifecycle.go:67` | **NO** | — | INV-15 — `Admit(Class(9), …)` panics with index-out-of-range |
| M65 | `sweep` reclaims a binding a borrower still holds | `tenancy/tenancydb/database.go:250` | **NO** | — | UC-48 / INV-23 — an in-use binding leaves the cache, so the next borrower opens a second pool and the bound drifts |
| M66 | producer captures with the read class | `tenancy/tenancyjobs/jobs.go:36` | yes | `TestAProducerRefusesToCaptureAScopeTheDurableClassDoesNotAdmit` | — |
| M67 | row policy narrows for the write class | `tenancy/tenancyrow/row.go:222` | yes | `TestTheRepositorySeamAsksForTheClassOfTheVerb` | — |
| M68 | `permits` accepts an invalid class | `tenancy/grant.go:177` | **NO** | — | INV-20 — `Accept(…, Class(9))` yields a grant that permits nothing instead of `ErrMalformed` |
| M69 | restored partition disagrees with the sealed reference | `tenancy/tenancyjobs/jobs.go:99` | yes | `TestWorkEnqueuedByATenantIsExecutedAsThatTenant` +2 | — |
| M70 | enqueued partition names a tenant the token does not | `tenancy/tenancyjobs/jobs.go:40` | yes | `TestWorkEnqueuedByATenantIsExecutedAsThatTenant` +2 | — |
| M71 | `column.Frozen` names a different column | `tenancy/tenancyrow/row.go:97` | yes | `TestAMutationCannotMoveARowBetweenTenants` +4 | — |

**50 caught / 21 survived / 71 applied.** No survivor is an equivalent mutant:
each changes an externally observable refusal, predicate, log field or resource
outcome.

---

## Findings

### GAP-1 [critical][immediate] D-117 asserts two seal guarantees the code does not have, and cites two tests that do not exist
- **STATUS AT HANDOFF: CLOSED BY SOMEBODY ELSE, MID-AUDIT.** Measured red at
  00:39-00:41. At 00:43:18 a concurrent agent rewrote `tenancy/seal.go`
  (`mac` now writes `this.authority.origin`; `Seal` now calls
  `accept(scope, ClassDurable)` instead of `minted(scope)`) and
  `tenancy/seal_test.go`. Re-verified at 00:47 with the same probe:
  ```
  PROBE 1  staging sealed -> production unsealed:
           err=tenancy: scope was not produced by this authority: forbidden
           RESULT: Origin fences the seal
  PROBE 2  Seal(scope minted for ClassRead) -> 0 bytes,
           err=tenancy: tenant lifecycle does not admit this work: forbidden
           RESULT: Seal refuses the class
  $ go test -count=1 ./tenancy/... ./scripts/...   all ok
  ```
  The finding is kept in full because it was real, because the evidence below is
  what a re-audit must reproduce as *fixed*, and because the fix arrived without
  passing through this audit — nobody has yet checked it against the close
  criteria at the end of this entry.
- **Where (as measured):** `docs/ai/decisions/D-117-a-verified-scope-is-minted-never-manufactured.md:148-153`; the code it describes is `tenancy/seal.go:41-55` (`Seal`) and `tenancy/seal.go:83-98` (`mac`).
- **Scale:** local (2 claims, 2 missing tests) — but the claims are security guarantees.
- **Confidence:** CONFIRMED.

  The repository's own check is red:
  ```
  $ go test -count=1 ./scripts/...
  --- FAIL: TestEveryTestNameTheDocsCiteExists (0.09s)
      docs_test.go:59: TestARecordSealedInAnotherDeploymentIsNotRead is cited by
        ../docs/ai/decisions/D-117-…md:151 and no _test.go declares it
      docs_test.go:59: TestOnlyAScopeTheDurableClassAdmitsIsSealed is cited by
        ../docs/ai/decisions/D-117-…md:148 and no _test.go declares it
  ```
  Both guarantees were then reproduced against the built package from a probe
  module outside the repository:
  ```
  PROBE 1 — does Origin fence a durable seal, as D-117:152-153 claims?
    staging sealed -> production unsealed: ref="acme" gen=1 err=<nil>
    RESULT: Origin is NOT inside the seal — a record crosses deployments that share a DurableKey

  PROBE 2 — does Seal refuse a lifecycle the durable class does not admit, as D-117:148-150 claims?
    authority admits ClassDurable/Active? false
    Seal(scope minted for ClassRead) -> 44 bytes, err=<nil>
    RESULT: Seal does NOT check the durable class; it checks only that this authority minted the scope
  ```
- **What / why this severity:** `Sealer.mac` (`seal.go:84`) keys on `durableKey`
  and MACs `{count, binding fields, epoch, reference}` — `origin` is absent.
  `Seal` calls `this.authority.minted(scope)` (`seal.go:45`), which is
  `boundTo` only; it never calls `accept`, so no admission check happens.
  Two deployments that share a `DurableKey` — the documented rotation and
  staging-clone shapes — read each other's queue rows. And the primitive relies
  on `tenancyjobs.Capture` asking for `ClassDurable` first, which is exactly
  what D-117 says it does *not* do. A decision document in this repository is
  binding law (`CLAUDE.md`); law that names two non-existent tests as its
  evidence is worse than no law.
- **Caveat, stated because it matters:** `D-117…md` has mtime `2026-09-06 00:35:35`,
  i.e. it was edited by a concurrent agent *during* this audit; the same command
  was green at 00:16. Whoever owns that edit owns this finding. The two code
  facts (no `origin` in the seal MAC, no class check in `Seal`) are independent
  of the edit and are true of the working tree either way.
- **Close criteria:**
  - [ ] `go test ./scripts/...` green: every test name D-117 cites is declared.
  - [ ] Either `Sealer.mac` includes `origin` and a test proves a same-key,
        different-`Origin` deployment cannot unseal, or D-117 stops claiming it.
  - [ ] Either `Seal` calls `accept(scope, ClassDurable)` and a test proves a
        read-only-admission deployment cannot seal, or D-117 stops claiming it.

### GAP-2 [high][immediate] Nothing proves the relation narrowing names *this* tenant
- **Where:** `tenancy/tenancyrow/row_test.go:343` (`TestAPreloadOfADeclaredRelationCarriesTheTenant`) and `row_test.go:286` (`TestARowOwnedThroughARelationIsNarrowedByThatRelation`); source at `tenancy/tenancyrow/row.go:90` and `row.go:156`.
- **Scale:** systemic — every relation-narrowing assertion in the repository (2 tests, 3 assertions) is a substring check.
- **Confidence:** CONFIRMED — M41 and M42 both survive, twice each.
  ```
  M41 column.Relations narrows the preload to ANOTHER tenant  -> SURVIVED
  M42 through.Narrow names ANOTHER tenant's value             -> SURVIVED
  ```
- **What / why this severity:** the assertions are
  `strings.Contains(clauseOf(statements[1]), "tenant_id")` and
  `strings.Contains(statement, "tenant_id") || …"invoices"`. Replacing the bound
  value with `"globex-1a2b"` — another tenant — leaves both strings present and
  the suite green. Concretely: a `Value` function that resolved the wrong tenant
  (a mapping bug, an off-by-one in a lookup table, a stale cache) would preload
  *another tenant's* line items into every invoice, and this suite would stay
  green. The root-level equivalent of this defect (M10) *is* caught, because
  `TestEveryVerbNarrowsToTheScopesOwnTenantAndNoOther:107` asserts
  `slices.Contains(statement.Args, any(secretReference))`. The relation path
  simply never got the same assertion. This is the same class of hole the plan's
  Round 3 records as the worst finding of the previous review — closed for the
  root, still open one level down.
- **Why this timing:** INV-11 is the invariant the framework's own history is
  written around ("a scope that stopped at a preload"). Any further work on
  relation ownership will be built on an assertion that cannot fail.
- **Close criteria:**
  - [ ] Both tests assert the bound argument equals the scope's reference, as `row_test.go:107` does.
  - [ ] A control subtest binds a second tenant and shows the argument changes with it.
  - [ ] M41 and M42 go red.

### GAP-3 [high][immediate] The `Through` strategy has never been executed by a database, and its frozen link is untested
- **Where:** `tenancy/tenancyrow/row.go:151-189`; the only tests are `row_test.go:274-300`.
- **Scale:** local but total — one strategy, zero live coverage, one unproven invariant.
- **Confidence:** CONFIRMED.
  ```
  $ grep -rn 'tenancyrow.Through' test/ _examples/ --include='*.go'
  (no matches)
  M17 through.Frozen returns nil -> SURVIVED   (confirmed twice)
  ```
- **What / why this severity:** two separate holes.
  (a) `through.Frozen()` returns the relation's local key so that repointing it
  is refused — the comment at `row.go:169-171` says exactly why: "repointing it
  is how a row leaves one tenant for another without any column of its own ever
  changing". Returning `nil` instead leaves the suite green. So the concrete
  attack — `lines.Update(ctx, id, LineUpdate{InvoiceID: &someoneElsesInvoiceID})` —
  has no test. (There is no `InvoiceID` field on `LineUpdate` in the fixture, which
  is *why* it has no test: the fixture cannot express the attack.)
  (b) `through.Narrow` compiles to `crud.Eq("Owner.TenantID", v)`, a correlated
  subquery. No database has ever executed it. Nobody has established that it is
  valid SQL on Postgres, let alone that it excludes rows.
- **Why this timing:** `Through` is one of the two published ownership strategies.
  Shipping a narrowing whose SQL has never run is a correctness claim with no evidence.
- **Close criteria:**
  - [ ] `LineUpdate` (or an equivalent fixture) exposes the local key, and a test asserts an update that repoints it is refused with `crud.ErrForbidden`, with a control showing the update succeeds for a non-ownership field.
  - [ ] A live-database test seeds two tenants' parents and children and proves a `Through`-owned read/update/delete under A returns and touches none of B's.
  - [ ] M17 and M42 go red.

### GAP-4 [high][immediate] The log channel is an untested leak path — INV-25 has no scan test
- **Where:** `tenancy/scope.go:44` (`Scope.LogValue`), `tenancy/reference.go:31` (`Reference.LogValue`), `tenancy/grant.go:63` (`Grant.LogValue`). Nearest test: `tenancy/scope_test.go:373` (`TestNothingCarryingATenantRendersIt`).
- **Scale:** systemic — 3 `LogValue` implementations, 0 tests, 0.0% statement coverage on all three.
- **Confidence:** CONFIRMED.
  ```
  M52 Scope.LogValue logs the tenant reference     -> SURVIVED (confirmed twice)
  M53 Reference.LogValue logs the tenant reference -> SURVIVED (confirmed twice)
  $ go tool cover -func=cpall.out | awk '$NF=="0.0%"'
  tenancy/scope.go:44:      LogValue   0.0%
  tenancy/reference.go:31:  LogValue   0.0%
  tenancy/grant.go:63:      LogValue   0.0%
  ```
- **What / why this severity:** `TestNothingCarryingATenantRendersIt` covers
  `fmt.Sprint`, `%v`, `%s`, `%+v` and `json.Marshal`. It does not touch `slog`.
  `LogValue` is the method `log/slog` calls — it is *the* production log path for
  these types, and it is the one nobody tests. INV-25's own "How to check" asks
  for "unguessable sentinels … the captured generic stream contains zero sentinel
  occurrences. The scan runs as a test." No such test exists. Changing
  `slog.StringValue(this.String())` to `slog.StringValue(this.reference.Value())`
  — a one-token edit a reviewer would wave through — puts every tenant reference
  into the log pipeline, and the suite stays green.
- **Close criteria:**
  - [ ] A test logs a `Scope`, a `Reference`, a `Grant`, a `Resolution` and every sentinel error through a `slog.Handler` capturing to a buffer, and asserts the buffer contains no tenant sentinel.
  - [ ] The same test has a control showing a deliberately-leaking value *is* detected.
  - [ ] M52 and M53 go red.

### GAP-5 [high][immediate] A control plane that answers about the wrong tenant is not refused by any test
- **Where:** `tenancy/authority.go:98-100` (`Lookup`) and `tenancy/context.go:65-67` (`current`).
- **Scale:** systemic — the same guard on both minting paths, neither covered.
- **Confidence:** CONFIRMED — M57 and M59 both survive (M57 confirmed twice).
- **What / why this severity:** both guards exist precisely because a resolver is
  application code. Every test resolver in the suite (`Fixed`, `directory`,
  `knownTenants`, `switchable`, `counting`) answers `ErrUnmapped` on a mismatch,
  so the mismatch branch is never entered. Delete the guard and nothing goes red.
  The failure it protects against: a control plane with a caching bug returns
  tenant B's `Resolution` for a `Lookup(A)`; the authority mints a *verified*
  scope for B, and every downstream narrowing, namespace, partition and durable
  seal is B's. This is INV-2 ("a value that the data plane accepts as a verified
  scope exists only if the injected trust authority produced it") failing not by
  forgery but by the authority producing the wrong one. It is also the one place
  a durable worker cannot recover from, because `RestoreIdentity` re-Looks-up by
  the reference from the record.
- **Close criteria:**
  - [ ] A resolver double that returns a `Resolution` naming a different reference; `Lookup` answers `ErrUntrusted` and no scope is minted.
  - [ ] The same for the revalidation path, with `Spec.Revalidate: true`.
  - [ ] A resolver double that returns an invalid `Resolution` (zero epoch, unknown lifecycle) — `!resolution.valid()` — on both paths.
  - [ ] M57 and M59 go red.

### GAP-6 [high][immediate] `New()`'s admission-consistency guard is unproven
- **Where:** `tenancy/lifecycle.go:109-116` (`inconsistent`), used at `tenancy/authority.go:45-47`.
- **Scale:** local.
- **Confidence:** CONFIRMED — M06 survived on three separate runs.
- **What / why this severity:** `inconsistent()` is the only thing stopping a
  deployment from declaring "writes admitted for `Provisioning`, reads not". The
  code comment (`lifecycle.go:100-104`) states the consequence: every write
  resolves a read scope first, so such a policy silently refuses every
  provisioning write. `New` returns a named error for it. No test constructs that
  spec. Making `inconsistent()` return `LifecycleUnknown` unconditionally leaves
  170 tests green.
- **Close criteria:**
  - [ ] `tenancy.New` with `Admit(ClassWrite, Provisioning)` and no matching read admission returns an error naming the state.
  - [ ] A control: the same spec plus `Admit(ClassRead, Provisioning)` constructs.
  - [ ] `ClassDurable` is asserted *not* to be held to the read floor (the documented exception at `lifecycle.go:106-108`).
  - [ ] M06 goes red.

### GAP-7 [high][immediate] Live-database coverage is 2 functions over 7 of 18 verbs, one strategy and one column type
- **Where:** `test/integration/tenancy_test.go` — the whole file.
- **Scale:** systemic.
- **Confidence:** CONFIRMED — verb matrix computed by grepping call sites:

  | verb | unit calls | live calls | | verb | unit | live |
  |---|---|---|---|---|---|---|
  | `GetByID` | 3 | 2 | | `SaveAll` | 1 | **0** |
  | `Get` | 2 | **0** | | `Save` | 7 | 3 |
  | `GetAll` | 13 | 2 | | `SaveOnly` | 1 | **0** |
  | `First` | 2 | **0** | | `Update` | 3 | 1 |
  | `Aggregate` | 2 | **0** | | `UpdateAll` | 4 | **0** |
  | `Count` | 3 | 1 | | `Delete` | 2 | 1 |
  | `Exists` | 2 | 1 | | `Restore` | **0** | **0** |
  | `ExistsUnscoped` | **0** | **0** | | `DeleteAll` | 4 | **0** |
  | `InsertBatch` | 1 | **0** | | `Tx` | 1 | **0** |

  `go vet -tags=integration ./integration/...` is clean, so the file builds.
- **What is missing live, by scenario:** bulk mutation (`UpdateAll`, `DeleteAll`) —
  the highest-blast-radius operation and the one UC-29 exists for; relation and
  preload narrowing (UC-24, UC-30 — no live fixture at any depth, let alone the
  depth ≥ 2 the acceptance criterion names); the `Through` strategy; a **string**
  ownership column (the live model's `TenantID` is `int64`, so the default
  `Value` path — `reference.Value()` — is never executed by a database, only the
  `numericTenant` converter); revalidation (`Spec.Revalidate`); epoch rotation;
  grants and `Each`; the durable seam against `jobspg`; the object seam against a
  real backend; the cache seam against a real cache; the whole `tenancydb`
  topology; two databases holding equal ids; `Restore` on a tombstoned
  tenant-owned model; frozen-column enforcement through `UpdateAll`;
  `crud.ErrForbidden` vs `crud.ErrNotFound` distinguishability under a real
  unique constraint.
- **Why this timing:** the unit suite runs against `crudtest.Postgres()`, which
  returns the rows the test pushed **regardless of the `WHERE` clause**. Every
  unit isolation assertion is therefore about emitted SQL text. Only a database
  can prove exclusion. Two functions is the whole evidence base for the
  advertised topology.
- **Close criteria:**
  - [ ] A live test per bulk verb, seeding two tenants and asserting the other tenant's rows are unchanged.
  - [ ] A live preload/relation test at depth ≥ 2 with the control UC-24 asks for.
  - [ ] A live `Through` test.
  - [ ] A live test with a string ownership column.
  - [ ] The published profile in `docs/modules/en/tenancy.md` names the verbs that have live evidence and the ones that do not.

### GAP-8 [medium][immediate] `Restore` and `ExistsUnscoped` have no tenancy test at any level — INV-9 is not met
- **Where:** absent from `tenancy/tenancyrow/row_test.go` and `test/integration/tenancy_test.go`.
- **Scale:** 2 of 18 gate verbs.
- **Confidence:** CONFIRMED — `grep -ro 'invoices\.Restore(\|lines\.Restore(' tenancy/tenancyrow` → 0; same for `ExistsUnscoped`.
- **What / why this severity:** the fixtures make these verbs untestable *by
  construction*: neither `Invoice` (`row_test.go:17`) nor `User`
  (`test/integration/model.go:13`) carries a tombstone column, so `Restore` cannot
  be reached. INV-9 requires "every verb the constrained seam exposes has an
  explicit forward/narrow/refuse decision". `Restore` resurrects a tombstoned row;
  if the tenancy policy did not reach it, tenant A could un-delete tenant B's row
  by guessing an id — the same shape as `Update`/`Delete`, which *are* tested.
  The gate is believed to cover it; nothing in the tenancy suite says so.
  `ExistsUnscoped` is the verb [[D-115]] was written for and the plan's own
  mutation table cites it — but that test lives in `crud/`, not here.
- **Close criteria:**
  - [ ] A tenant-owned fixture model with a tombstone column.
  - [ ] `Restore` appears in `TestEveryVerbNarrowsToTheScopesOwnTenantAndNoOther` and `TestNoVerbRunsWithoutAVerifiedScope`.
  - [ ] `ExistsUnscoped` under a tenancy policy has a test in this package, or the profile records that it is covered only in `crud/`.

### GAP-9 [medium][immediate] The grant's own bounds are unproven
- **Where:** `tenancy/grant.go:86` (cohort bound), `:98` (de-duplication), `:40` (purpose charset), `:140` (cancellation), `:177` (class validity). Tests: `grant_test.go:87-100` only checks empty cohort, empty purpose and an already-expired deadline.
- **Scale:** systemic — 5 guards, 5 survivors (M44, M45, M46, M47, M68).
- **Confidence:** CONFIRMED.
- **What / why this severity:** the grant is the *only* sanctioned way to cross
  tenants (INV-20), so its bounds are the blast radius. Concretely: a cohort of
  50 000 references is accepted (`MaxCohortSize` is unproven); a cohort naming
  the same tenant three times enters it three times, so INV-21's "a retry applies
  each tenant exactly once" fails on the first run, not on a retry; a cancelled
  context does not stop a cohort run; `Accept(purpose, cohort, until, Class(9))`
  returns a grant that permits nothing rather than `ErrMalformed`, moving the
  failure from construction to a confusing `ErrGrantRequired` at use.
- **Close criteria:**
  - [ ] `Accept` with `MaxCohortSize+1` references answers `ErrMalformed`; with `MaxCohortSize` it succeeds (control).
  - [ ] `Accept` with a duplicated reference produces `Size() == 1` and `Each` enters it once.
  - [ ] `Each` with a context cancelled after member 1 returns 1 outcome and `context.Canceled`.
  - [ ] `ParsePurpose` rejects control characters and over-length values.
  - [ ] `Accept(…, Class(9))` answers `ErrMalformed`.
  - [ ] M44–M47 and M68 go red.

### GAP-10 [medium][deferred] The directory's lifecycle and eviction selectivity are unproven
- **Where:** `tenancy/tenancydb/database.go:264` (`Close`), `:241` (`Evict`), `:250` (`sweep`), `:190` (nil source).
- **Scale:** 4 survivors (M50, M62, M65, M48); `Directory.Close` is at 0.0% coverage.
- **Confidence:** CONFIRMED.
- **What / why this severity:** `Close()` can be replaced by `return nil` and the
  suite stays green — so nothing proves shutdown releases pools, nor that a closed
  directory refuses (`reserve` checks `this.closed`, which only `Close` sets).
  `Evict(reference)` can be made to evict *every* tenant and nothing notices,
  because `TestEvictionWaitsForTheLastBorrower` only ever has one tenant cached:
  an operator rotating one tenant's credential would drop every pool in the
  process. `sweep` can be made to reclaim a binding a borrower still holds, which
  silently doubles the pool count for that tenant and breaks the `MaxCached`
  accounting INV-23 rests on. A factory returning `(nil, nil)` yields a `Lease`
  whose `Source()` is nil — a panic at the first statement instead of `ErrUnmapped`.
- **Why deferred:** `tenancydb` is the topology the profile explicitly does **not**
  advertise. These do not block the shared-row release.
- **Close criteria:**
  - [ ] A test caching two tenants, evicting one, asserting the other's binding survives.
  - [ ] A test that `Close()` closes every cached source and that a subsequent `Borrow` answers `ErrUnavailable`.
  - [ ] A test that an expired-but-borrowed entry is not swept.
  - [ ] A `Sources` double returning `(nil, nil)` answers `ErrUnmapped`.

### GAP-11 [medium][deferred] `t.Fatalf` is reached from four non-test goroutines
- **Where:** `tenancy/tenancydb/database_test.go:414, 441, 478, 504` — each calls `bound(t, …)` (`support_test.go:56`) or `boundTo(t, …)` (`database_test.go:402`) inside `go func()`, and both helpers call `t.Fatalf`.
- **Scale:** 4 sites.
- **Confidence:** CONFIRMED by reading; `go vet` does not flag it and the calls do not fire today because the authorities are healthy.
- **What / why this severity:** `t.Fatalf` from a non-test goroutine calls
  `runtime.Goexit` on that goroutine. The `sync.WaitGroup` those goroutines
  `defer wg.Done()` on still completes, so the test does *not* stop — it reports
  a failure whose stack points at the wrong place, and in
  `TestABorrowerThatGivesUpDoesNotFailTheOnesBesideIt` the remaining assertions
  read a `failures` slice that was never written. This is the documented
  `testing` misuse; when it fires it turns a real defect into a confusing report.
- **Close criteria:**
  - [ ] The four goroutines take a pre-built context, or the helpers return `(context.Context, error)` and the goroutines use `t.Errorf` + `return`.

### GAP-12 [medium][deferred] Four concurrency tests are timing-based and become vacuous rather than red under slowdown
- **Where:** `tenancy/tenancydb/database_test.go:407` (5 ms hold), `:434` (5 ms), `:459` (60 ms hold, two 10 ms sleeps), `:499` (2 s hold, 20 ms sleep, 50 ms deadline, 1 s wall-clock bound).
- **Scale:** 4 tests.
- **Confidence:** CONFIRMED that they are timing-based; CONFIRMED that they are
  *currently* effective — M49 (`open` shares the caller's cancellation) is caught
  by `:459`, and M61 by `TestOneTenantSelectsOneDatabaseAndKeepsIt`. PLAUSIBLE
  that they degrade: 40 repetitions under `-race`, `-cpu=1` and 8-way CPU
  contention did not flake.
  ```
  $ go test -count=20 -race -cpu=1 ./tenancy/tenancydb/   ok  3.899s
  ```
- **What / why this severity:** in `:459` the abandoning borrower's 60 ms open must
  still be in flight when `giveUp()` fires 20 ms later. On a machine 4× slower the
  ordering holds; on one where the 60 ms `time.After` fires early relative to the
  two 10 ms sleeps, the three "beside it" borrowers never wait on the abandoned
  open at all and the assertion passes without testing anything. The test cannot
  distinguish "the guarantee holds" from "the scenario did not occur".
- **Close criteria:**
  - [ ] The `openings` double signals readiness on a channel instead of holding for a duration, so the interleaving is forced rather than raced.
  - [ ] Each test asserts the scenario actually occurred (e.g. the number of waiters that blocked), so it cannot pass without exercising it.

### GAP-13 [medium][deferred] The recorder is more permissive than any database, and the suite knows it only in one place
- **Where:** every `tenancy/tenancyrow` test; `crudtest.Postgres()`.
- **Scale:** systemic — 17 test functions in `tenancyrow`.
- **Confidence:** CONFIRMED by construction: `crudtest.Recorder` replays pushed
  rows; no predicate is evaluated. `TestAPreloadOfADeclaredRelationCarriesTheTenant:334`
  pushes a child row owned by `"globex-1a2b"` and the test passes *because* the
  recorder hands it back — the assertion is about the SQL, not the row.
- **What / why this severity:** this is the honest limit of the layer and the
  tests are written accordingly (`TestATenantSeesEveryRowItOwns:319` asserts the
  exact clause `"tenant_id" = $1`, which is the strongest assertion in the file
  and the one that kills M12). The gap is that the layer's limit is not written
  down anywhere a reader will see it, so a future reader will over-trust
  `tenancyrow`'s 17 green tests. Combined with GAP-7 (two live functions), the
  net evidence for "another tenant's rows are excluded" is much thinner than the
  test count suggests.
- **Close criteria:**
  - [ ] A one-paragraph note in `tenancy/tenancyrow/support_test.go` (or the module doc) stating that these tests assert emitted SQL, not exclusion, and naming the live tests that do.

### GAP-14 [medium][immediate] UC-028 is unreachable from the module index and its Status overstates coverage
- **Where:** `docs/ai/usecases/modules/Index.md` (no `tenancy` row at all); `docs/ai/usecases/Index.md:138` (UC-028 appears **only** in the "Coverage map", not in the main use-case table that ends at line 106); `docs/ai/usecases/modules/tenancy/UC-028-…md:109`.
- **Scale:** local, 2 index omissions + 1 overstatement.
- **Confidence:** CONFIRMED.
  ```
  $ grep -n 'tenancy' docs/ai/usecases/modules/Index.md      -> no matches (rc=1)
  $ grep -n 'UC-028' docs/ai/usecases/Index.md               -> 138: (coverage map only)
  ```
  For comparison, the equally new UC-031 appears at both line 106 (main table)
  and line 140 (coverage map), and `jobs` has a row in `modules/Index.md`.
- **What / why this severity:** `modules/Index.md` is the newcomer's entry point
  ("The order is for newcomers"); a module whose UC is not listed there is a
  module whose contract nobody finds. And UC-028's Status says "Guarantees 1–8
  and 11–16 have tests" — guarantee 4 explicitly includes "for one owned across a
  declared relation — where what is frozen is the key that points at the owner",
  which GAP-3 (M17) shows has no test. Guarantee 13's log half is GAP-4.
- **Close criteria:**
  - [ ] A `tenancy` row in `docs/ai/usecases/modules/Index.md` with its import paths, sweep and verdict.
  - [ ] A UC-028 row in the main table of `docs/ai/usecases/Index.md`.
  - [ ] UC-028's Status names guarantee 4's relation half and guarantee 13's log half as untested, or those tests are added.

### GAP-15 [low][deferred] The only end-to-end composition root has no test
- **Where:** `_examples/tenancy-sharedrow/main.go`; `scripts/modules.sh:54`.
- **Scale:** local.
- **Confidence:** CONFIRMED.
  ```
  $ cd _examples && GOWORK=off go test ./tenancy-sharedrow/...
  ?   github.com/frostgrove/vv/_examples/tenancy-sharedrow	[no test files]
  $ GOWORK=off go run ./tenancy-sharedrow
  invoices are narrowed by crud.Middleware[main.Invoice,int64]
  a request with no verified tenant: refused before any statement, and the message names no tenant
  a request for an active tenant: bound to active tenant 42
  its object namespace is a fixed-width digest: invoices-81b1212c808dc16396b0f84877d1d369
  a cohort grant covers 2 tenants, for reads only
  ```
- **What / why this severity:** `_examples/README.md:53` says "Run it and read the
  four lines it prints" (it prints five). It is the only place the extension is
  wired from outside the module, and the only artefact that would catch a
  composition-root regression — but `make examples` only builds and vets it. A
  wiring change that silently stopped narrowing would still compile.
- **Close criteria:**
  - [ ] An `example_test.go` with `Output:` comments, so `go test` asserts the printed lines.

### GAP-16 [low][deferred] Nine test-support helpers are duplicated across six packages
- **Where:** `reference`, `activeAuthority`, `authorityFor` (3 copies: `tenancy/scope_test.go`, `tenancyrow/support_test.go`, `tenancydb/support_test.go`); `fixedAuthority` (3: `tenancystorage`, `tenancyjobs`, `tenancycache`); `directory`, `manyTenantAuthority`, `knownTenants`, `bound` (2 each).
- **Scale:** systemic, 6 packages.
- **Confidence:** CONFIRMED by grep.
- **What / why this severity:** partly forced by Go — `_test.go` helpers do not
  cross package boundaries. But the repository already owns `crudtest` as the
  answer to exactly this. The copies have already drifted: `tenancydb/support_test.go:43`
  has a `switchable` resolver, `tenancy/scope_test.go:400` has a different one,
  and `tenancyjobs`'s `knownTenants` differs from `tenancy`'s only in package
  qualification. A fix to one will not reach the others.
- **Close criteria:**
  - [ ] A `tenancy/tenancytest` package exporting the resolver doubles and authority builders; the six `support_test.go` files import it.

---

## Remediation order

1. **GAP-1** (S, blast radius: one decision doc + two lines of `seal.go`, or two
   doc paragraphs). It is the only red check in the tree and it is a security
   claim. Everything else can wait behind it because it decides what `Seal`
   *should* do, and GAP-5's fixture work overlaps.
2. **GAP-2** (S, `tenancyrow` tests only). Two `slices.Contains` assertions. No
   production change. Do it before anything else touches relations.
3. **GAP-5** (S, one resolver double reused in two tests). Pure test addition;
   unblocks nothing but closes the largest unproven guard.
4. **GAP-6** (XS, one construction test).
5. **GAP-3** (M, needs a fixture change — `LineUpdate` must expose the local key —
   and a live-database test). Depends on GAP-2 landing first so the relation
   assertions are trustworthy, and on the GAP-7 fixture work below.
6. **GAP-4** (S, one `slog` capture test + control). Independent.
7. **GAP-7** (L, live fixtures for bulk verbs, relations at depth ≥ 2, `Through`,
   a string owner column). This is the largest item and everything about relation
   and `Through` evidence funnels through it. Blast radius: `test/integration/`
   schema + the published profile.
8. **GAP-8** (M, needs a tombstoned tenant-owned fixture; shares the GAP-7 schema work).
9. **GAP-9** (S, five focused grant tests). Independent of everything above.
10. **GAP-14** (XS, two index rows + one Status paragraph). Do it with whichever
    of GAP-3/GAP-4 lands, since it must restate what is proven.
11. **GAP-11, GAP-12** (M together, `tenancydb` test rewrite: replace holds with
    channel handshakes and move `t.Fatalf` out of goroutines). One change, both gaps.
12. **GAP-10** (M, `tenancydb` lifecycle tests). Deferred with the topology.
13. **GAP-13, GAP-15, GAP-16** (S each, no dependencies).

---

## Methodology note — concurrent agents interfered with this audit

Three separate times, a sibling agent working in the same tree invalidated a run:

- The first harness crashed because its backup file vanished mid-run
  (`FileNotFoundError: .../tenancy/context.go.mutbak`), leaving M01's mutation
  live in `tenancy/scope.go`. Detected by checksum, restored from the backup,
  and the whole wave re-run from scratch.
- `go test` reported `[setup failed]` with
  `open .../tenancy/tenancydb/database.go: no such file or directory` for five
  packages at once — a sibling had the tree moved aside. That run (M24) was
  discarded and re-run.
- Wave 2 initially showed six CAUGHT verdicts whose only failing tests were
  `scripts` structural tests (`TestNoTenancyPackageCostsMoreThanTheSeamItNames`,
  `TestTheExtensionDoesNothingWhenItIsMerelyImported`) — which shell out to
  `go list` and cannot detect behaviour. Re-running wave 2 against
  `./tenancy/...` only turned all six into SURVIVED. **Every verdict in the table
  above is from a run whose catching tests are behavioural tenancy tests.**

Every survivor was re-run at least twice. The harness verifies the file's bytes
before mutating and after restoring, and aborts if either check fails.

---

## Tree verification

`git status` cannot verify `tenancy/` — the whole tree is **untracked**, as are
`scripts/tenancy_test.go` and `test/integration/tenancy_test.go`. Restoration was
therefore verified by SHA-256 over all 98 `.go` files in scope
(`tenancy`, `scripts`, `test/integration`, `crud/decorators/security`), snapshot
taken at 00:20 before the first mutation:

```
$ find tenancy scripts test/integration crud/decorators/security -name '*.go' \
    | sort | xargs sha256sum | diff pristine.txt -
(no output)
REPO PRISTINE
$ find . -name '*.mutbak' -o -name '*.orig'
(no output)
$ go vet ./tenancy/... ./scripts/...        clean
$ gofmt -l tenancy scripts test/integration crud/decorators/security   (empty)
```

That check passed after every mutation and again at 00:41.

**After 00:41 the tree moved under concurrent agents.** Nine files now differ from
the 00:20 snapshot: `tenancy/scope.go`, `tenancy/seal.go`, `tenancy/seal_test.go`
(00:43), `tenancy/grant.go`, `tenancy/grant_binding_test.go` (00:43),
`scripts/tenancy_test.go` (00:44), `tenancy/tenancystorage/storage.go` and
`.../storage_test.go` (00:47), `tenancy/tenancyrow/row_test.go` (00:48).
**None of the difference is mine.** Every one
of the 71 mutation sites was re-read in the current tree and every one is in its
original form — `boundTo` still ends `return hmac.Equal(...)`, `bind` still writes
`origin`, `lifecycle` and `epoch`, `Each` still re-checks the deadline and
`ctx.Err()`, `holds` still compares the MAC, `Unseal` still calls `hmac.Equal`,
`Sealer()` still guards `MinDurableKeyBytes`. The differences are the sibling's
*additions* (`writeField(mac, this.authority.origin)` and `writeCount` in
`seal.mac`, `accept(scope, ClassDurable)` in `Seal`, a second cohort loop in
`grant.go`, a rewritten `scripts/tenancy_test.go`) — text this audit never wrote.

The six survivors that live in those files were re-run against the **post-fix**
tree and all six still survive:
```
M44 Purpose.valid accepts control/space characters  -> SURVIVED
M45 Accept drops the MaxCohortSize bound            -> SURVIVED
M46 Accept stops de-duplicating the cohort          -> SURVIVED
M47 Each ignores context cancellation mid-run       -> SURVIVED
M52 Scope.LogValue logs the tenant reference        -> SURVIVED
M68 permits accepts an invalid class                -> SURVIVED
FINAL TREE CLEAN
```

For the tracked part:

```
$ git diff --name-only | grep '\.go$'
crud/decorators/faults/faults.go
crud/decorators/security/security.go
crud/errors.go
crud/executor.go
```
— all four were already modified in the baseline snapshot taken before this
audit began, and `crud/decorators/security/security.go` is inside the checksum
set above and is byte-identical to that baseline.

`git status --porcelain` differs from the pre-audit baseline only by
concurrent-agent edits to tracked documentation
(`M docs/ai/usecases/modules/Index.md`, `M jobs/README.md`, and content changes
inside `docs/ai/flows/Index.md` and `docs/ai/decisions/D-117-…md`) — see the
methodology note. **No file was written by this audit except this document.**

Caveat the reader must carry: every number in this report describes the tree as
of the 00:20 snapshot. The mutation verdicts for `tenancy/seal.go` (M18, M19,
M35) and `tenancy/scope.go` (M01-M04, M26, M51) were measured against the
pre-fix code and have not been re-measured against the sibling's rewrite. A
re-audit of the seal should re-run them.

---

## What I did not check

- **The integration suite was not executed.** `docker-compose.yml:23` binds
  `55432:5432`, and `ss -ltn` shows `127.0.0.1:55432` held by an unrelated
  `pgvector/pgvector:pg18` container (`analyzer4-postgres-1`, up 25 hours). The
  suite's default DSN is `postgres://vv:vv@127.0.0.1:55432/vv`
  (`test/corpus/corpus.go:19`) and `Target.reset(t)` drops and recreates the
  schema of whatever it reaches. Running it would have pointed a destructive
  suite at somebody else's database. This is the same port collision recorded as
  the cause of a previous false-green run. A `vv-tenancy-pg` container is up on
  `127.0.0.1:55433`, so the suite is runnable with `VV_PG_DSN` overridden — I did
  not do it because it is destructive and was not asked for. What I did verify:
  `go vet -tags=integration ./integration/...` is clean, so the file builds.
- **`crud/decorators/security/` was read, not mutated.** Its 15 test files
  (5 357 lines) are the gate's own suite; mutating `security.go` would grade the
  gate, not the tenancy tests. Its tenancy-relevant tests
  (`unscoped_test.go`, `relscope_test.go`, `updateall_test.go`, `edge_test.go`)
  were read for overlap only. A `security`-dimension audit is still owed.
- **`jobs`, `storage`, `cache`, `crud` internals** were not mutated; only the
  tenancy adapters that call them.
- **Mutation of test files** (to prove a test is vacuous by weakening its
  fixture) was not attempted beyond the source mutations above.
- **Property-based testing** of `bind`/`grantBinding`/`Digest` round-trips was
  not attempted; the existing tests are example-based.
- **`docs/modules/ru/tenancy.md`** was not compared against the English version.
- **FL-033 / FL-035 flow documents** were not audited against the code paths they
  name; only D-117's test citations were, because a `scripts` check made them
  falsifiable.
- **Whether the 21 survivors were also survivors of the prior review's 58
  mutations** — the plan records counts, not the mutation list, so the two sets
  cannot be intersected.


---

## State at handoff (00:49)

The tree is **not** the tree this audit measured, and it is **not** green.

```
$ go test -count=1 ./tenancy/... ./scripts/...
--- FAIL: TestAnUnscopedBulkMutationIsRefusedRatherThanWidened (0.00s)
    row_test.go:292: the narrowing is not the leading conjunct, so the statement
      is not scoped by it: UPDATE "invoices" SET "number" = $1
      WHERE ("tenant_id" = $2 AND ("id" = $3 AND "tenant_id" = $4 AND …))
FAIL	github.com/frostgrove/vv/tenancy/tenancyrow	0.003s
```

`tenancy/tenancyrow/row_test.go` grew from 13 KB to 18 KB at 00:48:16 and now
carries a "leading conjunct" assertion that its own subject does not satisfy.
`tenancy/tenancyrow/row.go` is byte-identical to the 00:20 snapshot, so this is a
test-side change in progress by another agent, not a regression in the strategy.
It is reported, not touched. Whoever owns that edit owns the red.

Two of this audit's findings were closed by other agents while it was being
written — GAP-1 (`seal.go`, 00:43) and, partially, the `scripts/tenancy_test.go`
rewrite (00:44) which strengthens the structural checks the audit describes.
Neither fix has been checked against the close criteria stated here.
