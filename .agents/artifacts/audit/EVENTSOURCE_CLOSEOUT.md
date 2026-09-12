# event/ — the PostgreSQL event-sourcing roadmap — CLOSEOUT AUDIT (2026-09-12)

**Scope.** The whole roadmap `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`: its
twelve-item *Definition of done*, its *Verification matrix*, the nine research appendices
ES-01…ES-09, the documentation obligations, the backlog, and the consumer's first day.

**Tree audited.** `f161aee` plus the uncommitted phase-5 change: 41 modified tracked files and 22
new paths (`git status --short | wc -l` → 77). Nothing in this audit was fixed. Three files were
mutated and restored byte-identically; the whole of `event/` was hashed before and after (213 files)
and the manifests are identical.

**Verdict in one line.** The delivered work satisfies **8 of the 12** Definition-of-done items, with
**2 partial** (1, 6), **1 partial on a foreign red** (12) and **2 not met** (8, 10) — and the two
not met are both admitted by the roadmap's own text rather than hidden. Eight of the nine appendices
have landed, one (ES-09) is deferred with its contract in the repository, and one landing (ES-05)
carries a partial recorded below the standard the brief sets.

---

## Map

- **Kernel** — `event/` (root package, 24 non-test files): the declaration seam (`Define`/`Declare`/
  `From`/`Then`), the eight-method `Store` contract, `Repo.Load/Append/Digest/StateAt/Within/
  Authority`, the reader, `Checkpoints`/`Track`, a seven-member `Outcome` and a six-class refusal
  vocabulary. Closure is stdlib + `crud` + `errs` + `utils`; `scripts/event_test.go` holds it.
- **Stores** — `event/eventmemory` (complete, transactional, not a double) and `event/eventpg`
  (the one module under `event/`, `go.mod` requires pgx **for its fixtures only**; `check-deps`
  reports `./event/eventpg: 0 external packages` in production). `event/eventtest` is the contract
  as a suite: `Run` (20 sections) and `RunCheckpoints` (14).
- **Consumers** — `event/projection` (20 non-test files): a supervised `runtime.Runner` per
  projection, two advance modes, a typed router, partitions, a sequenced park/DLQ, generations with
  a barrier and a fenced cutover, an effect capability, and — phase 5 — `mark.go` + `wait.go`.
- **Phase 5's new package** — `event/receipt` (7 non-test files, 293-line `claim.go` its largest):
  `Key`, `Fingerprint`, `Receipt`, `Ledger` (the interface, unimplemented here), `Claim`/`Once`/
  `Held.Complete`, `Resolve`, four sentinels.
- **Where state lives.** Every durable write is the caller's: `eventpg` at `SchemaVersion = 2` with
  four tables and no fifth; the park table, the generations table and the receipts table are the
  application's own, behind interfaces. No package here opens, commits or rolls back a transaction
  (`TestNoDoorOpensCommitsOrRollsBackAnything`), and no constructor starts a goroutine
  (`startsNothing`, driven from `scripts/event_test.go:77`).
- **Models/LLMs** — none, anywhere. Not applicable to this repository.
- **Tests** — unit per package under `-race`; `event/eventpg`'s tagged suite is the live tier and
  refuses to run without `FROSTGROVE_EVENTPG_TEST_DSN`; `scripts/` holds the structural walks
  (`event_test.go`, `projection_test.go`, `docs_test.go`, `extensions_test.go`) and
  `scripts/event_kernel.sha256` is a 152-path content fence over `event/` outside `eventpg`.

---

## Gate commands, run by me

| Command | Result |
|---|---|
| `make check` | **GREEN, 11 arms** — `check-deps · check-tiers · check-utils · check-triplets · check-todo · check-replaces · check-tidy · check-otel-schema · check-otel-module · check-workspace · check-event-kernel`, each `ok`. `CHECK EXIT=0` |
| `make vet` | **GREEN**, every workspace module plus `./_examples`. `VET EXIT=0` |
| `gofmt -l .` | **silent** (0 lines) |
| `git diff --check` | **clean**, exit 0. Untracked phase-5 files scanned separately for trailing whitespace: none |
| `make examples` | **GREEN**, `EX EXIT=0` — `_examples/event-guide` builds, vets and tests |
| API surface | `generate_api /tmp/surface_check.md` then `diff -u docs/api/surface.md /tmp/surface_check.md` → **0 lines**. The committed baseline is regenerated |
| live suite ×2 | `ok github.com/frostgrove/vv/event/eventpg 123.832s` then `125.060s`, `-race -count=1 -tags=integration`, both exit 0 |
| live suite, DSN unset | **FAILS, does not skip**: *"FROSTGROVE_EVENTPG_TEST_DSN is not set, and this suite proves nothing without a database, so it fails rather than skipping"*, and it prints the command. `FAIL … 0.002s` |
| `BenchmarkStreamReplay` | **11 464 077 · 11 623 541 ns/op, 1146 · 1162 ns/event** (10x, count 2, PostgreSQL 17.9). Independently reproduces D-145's measurement |
| `make unit` | **NOT GREEN — exit 2.** See below |

### `make unit` — one named foreign red, and two more it never reaches

`make unit` aborts at the first failing module, which is the root one, so it reports **one** failure
and stops. I ran the two it never reaches by hand.

| Arm | Red | Mine? |
|---|---|---|
| `./scripts` — `TestNoI18nPackageCostsMoreThanItsErrorSeam` | *"the i18n extension reaches github.com/go-json-experiment/json/jsontext outside its error seam"* | **No** — the named foreign red. Untouched, not allowlisted |
| `./i18n` — `i18n/cmd/vv-i18n`, **29** `--- FAIL:` lines (`TestExtractUsage*`, `TestExtractCommand*`) | `Complete:false` on generic/cross-package factory extraction | **No** — owner's i18n work. Never reached by `make unit` |
| `./test` — `test/auditflow`, `TestGeneratedAuditProfilesSeparateServingRuntimeFromDeploymentAuthority` | *"generated serving health check: audit serving runtime is not ready"* | **No** — owner's audit work. Never reached by `make unit`. It matters here because `test/auditflow` is also the **only** event × audit composition fixture in the repository |

Every package under `event/` is green under `-race`: `event 7.513s`, `eventmemory`, `eventtest`,
`projection 6.118s`, `receipt 1.020s`.

---

## Definition of done, item by item

| # | Item | Verdict |
|---|---|---|
| 1 | one accepted ADR names the aggregate, the module, the PostgreSQL choice, the direct API boundary and the forbidden imports | **partial** |
| 2 | exactly one eventpg module, no combination package | **met** |
| 3 | one aggregate passes codec, append/load, conflict, rollback, replay and v1→v2 against supported PostgreSQL | **met** |
| 4 | expected-version, commit-uncertainty and no-blind-retry documented and executable | **met** |
| 5 | every accepted base adapter uses a base-owned typed chain and passes conformance | **met (vacuously)** |
| 6 | audit, tenancy, broker and telemetry proven only in application composition fixtures | **partial** |
| 7 | transaction-local projection/outbox/snapshot has its own activation gate and failure matrix | **met** |
| 8 | isolated `GOWORK=off` graphs prove root-only, event-only and composed consumers | **not met** |
| 9 | privacy scanners find no payload, identity, SQL, raw error, expected version or checkpoint in default diagnostics | **met** |
| 10 | restore/replay, mixed-release rollback, capacity and incident runbooks reviewed before beta | **not met** |
| 11 | documentation labels provisional and implemented APIs accurately; pages, index rows, flow, surface | **met** |
| 12 | `make check`, `make unit`, `make vet`, `git diff --check` pass and the live suite ran | **partial — `make unit` red on three foreign arms** |

**1 — partial.** The module (`event/eventpg`), the PostgreSQL choice, the direct-API boundary and the
transaction rule are all named in accepted ADRs: [[D-121]], [[D-126]], [[D-127]], [[D-118]]. **No ADR
names an aggregate**, and D-121 says so in its own words: E0 decision 1 *"still holds for `eventpg`
and it does not hold here"* — yet `eventpg` shipped a live schema at version 2 anyway. That is a
decision taken in the roadmap's margin rather than in a record. The **forbidden imports** are held
by direction (`TestNoBaseSubsystemDependsOnTheEventExtension`, `TestNoEventPackageCostsMoreThanThe
SeamItNames`, and `check-deps`); the roadmap's forbidden package **names** — `eventaudit`,
`eventotel`, `tenancyevent`, `eventkafka` and the rest — are enforced by no check: `rg` over
`scripts/` finds none of those strings. A package under `event/` would be caught (an uncharged
package fails `costsNoMoreThanItNames` twice); a top-level `eventotel/` would not be.

**2 — met.** `find event -name go.mod` → exactly one, `event/eventpg/go.mod`; `go.work:25` lists it
once. `check-deps` enumerates the six packages of the extension and no seventh.

**3 — met, with the item-1 caveat.** Live and green twice: conformance (`Run`, 20 sections, 19
*passed* and `monotone visibility` *not certified* with the store's own stated reason), concurrency
(`TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts`), batch density
(`TestABatchLandsDenseAscendingAndAtOneInstant`), rollback
(`TestARollbackRestoresNothingBecauseNothingWasTouched`), evolution
(`TestStoredRowsAreUpcastAndNeverRewritten`). The "one aggregate" is the suite's fixture, not a
consumer's, which is item 1 again.

**4 — met.** `event/outcome.go`'s seven outcomes with `Unclassified` as the zero value;
`eventpg/classify.go:outcomeOf` selects `NotWritten` on four proofs and never from a `default` arm;
`TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires` and
`uncertainty_integration_test.go` drive it live; `TestNoNotWrittenBranchIsUnguarded` is the
structural half. The no-blind-retry rule is on the eventpg page and at
`docs/usage-guides/event-sourcing.md:186`.

**5 — met vacuously.** E3 was deferred for want of a consumer; no base adapter shipped, so there is
nothing to hold to the clause. `event.Chain` is the *revision* chain, not middleware — checked, it
is `From`/`Then` over codecs (`event/chain.go:32,42`). No `event.Middleware` exists.

**6 — partial.** **Audit:** proven — `test/auditflow/subsystem_composition_test.go:83` composes
`event` + `eventmemory` + `audit` + jobs + storage in the unpublished `test` module and asserts the
event commit retains its store's transaction authority before an audit capture. **No production
cross-import exists**: `rg 'frostgrove/vv/(tenancy|audit|otel)' event/` → nothing. **Tenancy: no
fixture at all.** Nothing in `test/` or `_examples/` composes tenancy with event; `_examples/tenancy-
sharedrow` imports `crud/sqlrepo`, `tenancy`, `tenancyrow`, `tenancystorage` and no event package.
Mitigating: no event doc *claims* tenancy support — `rg -i tenan docs/modules/en/event*.md
docs/modules/en/projection.md docs/usage-guides/event-sourcing.md` finds only `Tenant` as a field
name in an example identity — so the gap is a missing proof, not a false promise. **Telemetry:** the
row reduces to "no OTel imports", which holds. **Broker:** nothing shipped ([[D-118]]).

**7 — met.** `Advance: InUnit` is an explicit activation gate that requires `Unit` **and**
`Destination` and checks the handler writes where the advance does; `AfterApply` is the named honest
alternative. The failure matrix is `docs/modules/en/projection.md`'s four-rollback section. The
outbox is refused by [[D-118]] rather than half-built. The snapshot is [[D-145]] — a contract whose
two gates are stated unmet — and its absence is enforced by
`TestNoSnapshotAuthorityIsDeclaredOrPromised`, which now walks six surfaced packages including
`event/receipt`.

**8 — not met.** There is no `scripts/event-consumer.sh`, no `check-event-consumer` target in
`Makefile`/`scripts/vv`, and no event fixture under `scripts/`. The model the matrix names
(`scripts/otel-consumer.sh`) builds an isolated module with `GOENV=off GOWORK=off` and a private
module cache, and asserts the resolved directory is **not** in the repository — nothing equivalent
exists for `event`. `_examples` *is* `GOWORK=off` (`scripts/modules.sh:54`) but it is **one** module
carrying ent, gorm, gin, fiber, grpc and the OTel SDK, so it proves nothing about isolation. The
phase-5 gate's own `/tmp/p5verify` module was a real out-of-tree consumer, and it is gone: an
ephemeral program is not the repeatable artifact this item asks for.

**9 — met.** The scanner is `event/refusalmessages_test.go:41`
`TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor`: a **type-driven** AST walk (not a string
grep) over every package under `event/` that a program links — `event`, `eventmemory`, `eventpg`,
`projection`, `receipt`; `eventtest` is excluded because it imports `testing`. It derives what a
message is (any call returning an error and taking a `string`), follows a value through local
conversions, `Sprintf` and concatenation, and forbids `Key`, `Cursor`, `Version`, `Position`,
anything reaching them, and byte slices. Its own falsification fixture asserts **13** escapes are
reported and **6** permitted renderings are not, plus floors (`total < 60`, and both wrapping and
bare messages must be present). Siblings close the rest:
`TestNoRefusalOfABoundedReadNamesAVersion`, `event/projection/wait_test.go:1038`
`TestNoRefusalOfAWaitNamesAPositionOrAKey`, `event/receipt/errors_test.go:19`
`TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage`, and `event/renderedtypes_test.go:69`,
which fails when a **fifth** identity type is declared and nothing says whether it may be rendered.
I confirmed the scanner is live by mutation (M-A below). Two honest edges: (a) "SQL" and "raw
errors" are carried by the refusal contract (`event/errors.go:205` — a store's own text does not
travel; `event/refusal_test.go:506`) rather than by a scanner that looks for statement text; (b) the
"default diagnostics" surface is empty by construction — nothing under `event/` writes a log line,
span or metric, which `scripts/event_test.go`'s charging table states as the reason `port` stays
outside `projection`'s closure.

**10 — not met.** `rg -ril runbook docs/` names `tenancy`, `vvdb` and two roadmaps — **nothing for
the event subsystem**. The mixed-release rehearsal is **half**: `audit_integration_test.go:253`
`TestStoredRowsAreUpcastAndNeverRewritten` proves a v1 build writes, a v2 build reads through the
upcaster, the stored bytes are unchanged and the v1 build still folds them — but **no arm has the v2
build write**, which is the dangerous direction (an old reader meeting revision 2). The roadmap's E2
still lists both this and the archive/restore rehearsal as open, and `docs/roadmaps/Roadmap.md`
repeats it, so this is admitted rather than concealed.

**11 — met.** See *Documentation obligations* below. The roadmap's own E0/E1 prose names APIs that
never shipped (`eventpg.AppendRequest`, `events.Within(tx)`), but every such block is labelled
*"Illustrative only"* and the baseline table carries per-row **Superseded** notes, which is the
labelling this item asks for.

**12 — partial.** Four of the five sub-commands pass (table above). `make unit` is red; all three
red arms reproduce at `HEAD` and none is phase 5's. I am not reporting `make unit` green.

---

## The nine appendices

| # | Verdict | Where the evidence is |
|---|---|---|
| ES-01 one transaction authority | **landed** (phase 4) | removed from the roadmap's open list; `event.Authority`, `Repo.Within`, `checkUnit`, and phase 5 reuses it in `receipt.sameUnit` (`claim.go:223`) with five refusals |
| ES-02 partitions as a mask, position handed over | **landed** (phase 4) | `event/projection/partition.go`, `topology.go`, [[D-140]], live `partition_integration_test.go` |
| ES-03 causal-order-preserving DLQ | **landed** (phase 4) | `park.go`, `sequence.go`, `redrive.go`, live `park_integration_test.go` |
| ES-04 generations with a barrier and a cutover | **landed** (phase 4) | `generation.go`, [[D-140]], live `generation_integration_test.go` |
| ES-05 waiting for a named change | **landed, one partial** | below |
| ES-06 an effect as a spec capability | **landed, acceptable partial** (phase 4) | `effect.go`, [[D-141]]; the remainder is in capitals on the module page (`projection.md:640`) and in `D-141:159`: ***"`Active` must be a LOCKING read"*** — the pattern the brief names |
| ES-07 the uncertain append | **landed, one unenforceable clause** | below |
| ES-08 historical state at a version | **landed** | below |
| ES-09 compatible snapshots | **deferred, argument in the repository** | below |
| ES-10 OTel for store/replay/projections | **open, and correctly so** | still listed in the roadmap; `rg 'go.opentelemetry' event/` → nothing |

### ES-05 — landed, with the one partial recorded below the accepted standard

Both doors ship and neither is a lag check: `WaitSpec.Committed(ctx, store, commit)` mints a mark
from the commit's **whole range** read back **after** the caller's transaction committed (refused if
a transaction of that store is bound to the context, `wait.go:127`), and `MarkOf(Barrier)` over
`Observe`. The park is asked **first and on every poll** (`wait.go:391`), before the census, and
`ErrParked` is terminal — which is the one failure the appendix exists to forbid. The deadline
answers `ErrNotVisible` wrapping `context.DeadlineExceeded` with a filled `Visibility`; a
cancellation answers `ctx.Err()` bare. There is no stale-read flag and no head: `Reached`'s five
"is not"s are on `projection.md:522-545`.

**The partial:** ES-05's part 3 says *«Deadline возвращает timeout/degraded»*. Only *timeout*
shipped. There is no degraded verdict and — this is what matters — **no paragraph in `docs/` saying
why one is unavailable**. `PhaseDegraded` exists (`projection.md:865`) but is the projector's own
readiness, not a waiter's answer. The reason (a phase is in-process and a waiter usually is not that
process) is written only in `.agents/artifacts/gaps/EVENTSOURCE_BACKLOG.md` `## P5` item 1, as
`[medium]`. Measured against the ES-06 pattern the brief sets — *"the remainder written in capitals
on the module page and in D-141"* — this partial is recorded one layer lower than the standard.

### ES-07 — landed, and its central mechanism is unenforceable by design

`event/receipt` is the appendix, clause by clause: identity + fingerprint + range written beside the
append in the caller's transaction (`sameUnit` requires the store to state transactions, one of its
own bound to the context, and the ledger to resolve **the same** `Authority` — five refusals);
`Repeated` / `ErrCollision` for a repeat and for a differing content under one key; an absent row is
`Unresolved` and never a rollback ([[D-143]]; `Resolve`'s doc carries *"AN ABSENT ROW IS NOT A
ROLLBACK"*); retention bounds the window through `Ledger.Horizon` on the ledger's own database
clock; the framework re-decides nothing and `Store.Append` gains no deduplication. The fingerprint
deliberately excludes the version (`digestOf`, `repo.go:203`), which is what makes `Repeated`
reachable for the retry-in-a-new-process the mechanism exists for.

**The risk to state plainly:** the contract's serialisation point is *the caller's SQL* — `INSERT …
ON CONFLICT (key) DO NOTHING` **then** a separate `SELECT`, in that order. The framework cannot
check it. The repository's own test proves the failure:
`event/eventpg/receipt_integration_test.go:1808`, *"a claim whose select runs before its insert
breaks TestTwoCallersRaceOneKey"* — **both** callers answer `Recorded`, **both** append, and the
stream holds two copies. Three places document the ordering (D-142, `receipt.go`'s `Ledger` doc, the
usage guide) and **no place a consumer can run** checks it: `eventtest` publishes `Run` and
`RunCheckpoints` only, and the four-defect decorator test lives inside `event/eventpg`'s tagged
suite. `docs/modules/en/receipt.md:213` admits it: *"There is no published conformance harness for
a…"*. Backlog `[medium]` item 54.

### ES-08 — landed

`Repo.StateAt(ctx, id, version) (S, error)` — **two** returns; `docs/api/surface.md` confirms no
append token. The prefix is complete and dense: it pages through the same `ReadStream` a load does,
checks each page **whole** before truncating it, and stops the fold at the bound (`repo.go:315-357`).
Version zero, a version past the end and an empty stream are one `ErrVersion` naming no number
(`TestNoRefusalOfABoundedReadNamesAVersion`). No timestamp parameter, overload or option exists
([[D-144]]'s invariant; confirmed in the surface). An unreadable revision in the prefix is
`ErrRevision` and the **zero** state, never a partial fold. `UC-032` was not widened: the diff is 20
added lines, all under a new `## See also`, and no clause of `What must hold` or `Out of scope`
moved — I read the diff rather than trusting the claim.

### ES-09 — deferred, with the argument in the repository and not in a note

[[D-145]] is an accepted ADR with **no code**, indexed at `docs/ai/decisions/Index.md:202`, which
**amends** [[D-132]] without superseding it. It carries all five of part 3's sentences: five
bindings compared **before deserialisation**, a state-computation version independent of payload
revision, an observable fallback, an unreadable **event** that the fallback never hides, history
never deleted, and both gates with the statement that neither is met. It also records the sentence
nothing can check — that nothing detects a forgotten computation-version bump.

**The gate was asked before the file was written, and I re-asked it.** My own
`BenchmarkStreamReplay` run: **11 464 077 and 11 623 541 ns/op at 1146 and 1162 ns/event**. Against
the ~50 ms trigger that puts the crossing at ~43 000 events, reproducing D-145's ~45 000 inside
noise and inside D-132's recorded 30 000–50 000 band. Gate 1 asks for a *named deployment's* p99 and
none exists; Gate 2 (snapshot+tail ≡ full replay) is unmet and, as D-145 notes, absent from both
source implementations. This is the right shape for a deferral and is the pattern the other
appendices should be measured against.

---

## Verification matrix, row by row

| Row | Required proof | Verdict / where |
|---|---|---|
| One extension | exactly one eventpg module | **met** — `find event -name go.mod` → 1; `go.work:25` |
| Root optionality | no root/non-event consumer has a PostgreSQL event graph; `check-deps` green | **met** — `check-deps: ok`, `./event/eventpg: 0 external packages` (pgx is fixtures only, the `jobspg` shape) |
| Import direction | a graph test rejects an optional-extension import either way | **met** — `TestNoBaseSubsystemDependsOnTheEventExtension` + `costsNoMoreThanItNames`'s six charged rows, falsified by `scripts/extensionwalk_test.go:94` |
| No combinations | no event × tenancy/audit/OTel/storage/broker package | **met in fact, unchecked as names** — none exists; no check holds the forbidden **names** (item 1) |
| Direct seam | no generic root event contract without [[D-048]] evidence | **met** — [[D-121]]; `event` is explicitly not in the contract manifest |
| Base composition | each accepted adapter is ordinary base middleware | **n-a** — none accepted (E3 deferred) |
| Capabilities | exact effects never tunnel; capabilities honest through opaque neighbours | **met** — `Capabilities`/`Support` with `Unstated` as the zero value (`store.go:14`), `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries`, [[D-141]]'s effect gate |
| Append | expected version, immutable rows, stream order — **live concurrent PostgreSQL** | **met** — `append_integration_test.go:133` `TestEightWritersAtOneVersionLeaveOneWinnerAndSevenConflicts`; `:192` density; `schema_integration_test.go:495` the database itself refuses UPDATE/DELETE/TRUNCATE |
| Atomicity | one proven transaction authority, or the configuration refuses the claim | **met** — `Repo.Within`, `event.Authority.Same`, `receipt.sameUnit`'s five refusals, `transaction_integration_test.go:472` |
| Audit | application mapping only, no mutual import | **met** — `test/auditflow/subsystem_composition_test.go`; no production cross-import. Note: that package is red at HEAD for a foreign reason |
| Tenancy | verified scope/routing precedes event SQL; wrong or out-of-generation scope fails with zero leakage | **NO PROOF** — no tenancy × event fixture anywhere. No event doc claims tenancy, so nothing is over-promised |
| Broker | at-least-once crash windows | **n-a** — nothing shipped; [[D-118]] settles it |
| Telemetry | no OTel imports; no-op/exporter failure changes no result | **met on the checkable half** — `rg 'go.opentelemetry' event/` → nothing; the rest is ES-10 |
| Evolution | one reader path per retained revision; deterministic upcasters; **mixed-release rehearsal passes** | **partial** — reader-side proven live (`TestStoredRowsAreUpcastAndNeverRewritten`); the writer side of a mixed release is not rehearsed |
| Lifecycle | constructors start nothing; every continuous activity is a supervised runner | **met** — `startsNothing` forbids a `go` statement in any non-test file under `event/`; `projection` is a `runtime.Runner`; a wait runs on the caller's goroutine and costs nothing when nobody waits |
| Schema | verify by default, migrate on a profile, refuse an unknown schema before an append | **met** — `Prepare`/`Check`/`VerifySchema`, `ErrNotReady` before any door (`executor.go:42`), `schema_integration_test.go:157,190`, [[D-127]] |
| Health | a readiness answer with no importance of its own | **met structurally** — `Store.Check(ctx)` *is* a `health.Probe` shape with **no import of `health`**, documented at `docs/modules/en/eventpg.md:39` under [[D-091]] |
| Modules | `GOWORK=off` fixtures for root-only, eventpg-only and multi-extension, on the `scripts/otel-consumer.sh` model | **NOT MET** — no such script, target or fixture (DoD 8) |
| Live evidence | the command is recorded and its absence fails | **met** — verified by running with the variable unset: it fails and prints the command |
| Documentation | pages both languages, index rows, a flow, a use case, regenerated surface | **met** — below |

---

## Documentation obligations

| Obligation | Verdict |
|---|---|
| Module page per event package, **both** languages | **met** — `event`, `eventmemory`, `eventpg`, `eventtest`, `projection`, `receipt` exist in `docs/modules/en/` **and** `docs/modules/ru/` |
| Index rows, both languages | **met** — `docs/modules/en/Index.md:117-122`, `docs/modules/ru/Index.md:128-133`; the `receipt` row is present in both |
| A flow for **every** source file under `event/` | **met** — 97 non-test `.go` files; 94 have a row in `docs/ai/flows/Index.md`. The 3 without are `event/testdata/crossings/{change,control,identity}/*.go`, which are compile-fixtures, not source. **Backlog `[high]` item 8 — "thirteen of fourteen have no flow row" — is stale and was never flipped** |
| Its second half — the doc walk is English-only | **also closed and not flipped**: `deliveryWordings` in `scripts/docs_test.go` carries a Russian wording and fails if either language matches nothing |
| Use cases | **met** — UC-036, UC-037, UC-038 shipped and indexed (`Index.md:111-113` and the flow-mapping table at `:152-154`); UC-032 gained a `See also` and nothing else |
| Decisions | **met** — D-142…D-145 written and indexed (`Index.md:199-202`) with a phase narrative at `:447-460` |
| Flows | **met** — FL-043 new and indexed; FL-036 and FL-038 extended; the reverse index carries the 9 new files |
| Usage guide | **met and unusually good** — `docs/usage-guides/event-sourcing.md`, 463 lines, and **every Go fence in it is compiled**: `_examples/event-guide` is the page's code and `TestEveryGoFenceInTheEventSourcingGuideIsCompiled` (`scripts/docs_test.go:1843`) compares them |
| Runnable examples | **met** — 6 event examples; I ran two of them against the live database and both printed the behaviour they claim (see *Consumer* below) |
| `docs/api/surface.md` regenerated | **met** — 0-line diff against a fresh generation |
| `docs/roadmaps/Roadmap.md` no longer lists what is closed | **met in the body, stale in the summary table** — §15 is rewritten around what is left (retention/archival, `eventpgfx`), but the table row at `Roadmap.md:27` still gives the blocker as *"a named aggregate; the vocabulary, the in-memory store and the conformance suite are delivered"*, which is the phase-1 sentence and does not mention the store, projections, the wait, the receipt or the bounded read |
| Release notes | **met** — `docs/release-notes/v0.1.0.md:143` carries the `Park.Holds` contract widening in capitals as a change for implementers |

---

## Test-suite detection power — three mutations I ran

Mutations applied to the shipped code, each run against its own package, each restored from a
`/tmp` copy; the whole `event/` tree was hashed before and after (213 files) and the two manifests
are byte-identical, `git status --short` unchanged at 77 entries.

| # | Mutation | Caught? |
|---|---|---|
| M-A | `event/repo.go` — `if reached < version` → `if reached < 0`, so `StateAt` answers a partial state for a stream shorter than the bound | **YES**, two tests: `TestAVersionPastTheEndAndAnEmptyStreamAreOneRefusal` (*"a bound past the end of a five-fact stream answered &lt;nil&gt;"*) and `TestNoRefusalOfABoundedReadNamesAVersion` |
| M-B | `event/projection/wait.go` — `held.lowest < Until.At()` → `held.lowest+1 < Until.At()`, so a wait reaches one position early | **YES**, two tests: `TestTheParkIsAskedBeforeTheCensusOnEveryPoll`'s **control** subtest (*"answered {Reached:true At:5 …} where one member is behind the mark"*) and `TestTheDerivedSpecAnswersWhatThreeHandWrittenOnesGetWrong` |
| M-C | `event/receipt/claim.go` — `verdictOf`'s `Repeated` arm inverted, so a *differing* fingerprint reads as `Repeated` | **YES**, five tests, including `TestACollisionTravelsAsAnErrorAndNotOnlyAsAVerdict` and `TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage` |

Three for three, and M-B is the one that matters: it was caught by a *control* subtest rather than a
positive one, which is the shape this repository's own test doctrine asks for.

**Corrected 2026-09-13, after an independent measurement (`.agents/artifacts/gaps/EVENTSOURCE_DETECTION.md`).**
Two numbers are quoted around this subsystem and they measure different things. The **16%** /
**10.3%** figures the backlog carries are *assertion neutralisation inside the conformance suite*,
and that method has a structural ceiling of about **24%** — the suite is falsified by running each
inventoried defect and requiring its section to fail, so at most one assertion per inventory row can
ever be the first to report, which is 43 rows against 179 assertion sites. Quoted against an implied
ceiling of 100% they overstate the deficiency and hide the actionable half of it: 136 sites are out
of that method's reach by construction, and the only lever is inventory rows. The number that
measures what a reader expects is *implementation* mutation: **61 of 65 killed = 94%**, against 89%
re-derived on a clean pre-stage clone. The per-section inventory pressure is tabulated in the
backlog's item 1.

---

## Findings

### GAP-1 [high][immediate] Nine `[critical]`/`[high]` findings sit open in the backlog, and at least four of them are stale

- **Where:** `.agents/artifacts/gaps/EVENTSOURCE_BACKLOG.md:23,37,44,50,58,60,67,71` (P1) and `:385` (P2)
- **Scale:** systemic (9 open items of blocking severity out of 302)
- **Confidence:** CONFIRMED — a per-section count over `^### \d+\.` headers:
  `P1-deferred total=9 crit=3 high=5 low=1` · `P2 total=65 high=2 (1 marked CLOSED)` ·
  `From-the-reference total=14 high=2 (both CLOSED)` · `P3 74` · `P4 76` · `P5 64`, and
  `TOTAL {critical: 3, high: 9, medium: 152, low: 134, untagged: 4, total: 302}`.
  Open after removing the four `CLOSED`/`REFUSED` markers: **3 critical + 6 high**.
- **What:** the brief expects zero, because the 2026-09-08 policy defers only medium and low. The
  policy has in fact held **perfectly since it was set**: P3 (74), P4 (76) and P5 (64) contain zero
  critical and zero high. All nine are **pre-policy** P1/P2 entries. But at least four have had
  their own stated closure conditions met and were never flipped:
  - item 1 `[critical]` says *"stays open until that check exists"* — the check exists:
    `event/eventpg/mutation_integration_test.go:66`
    `TestTheConformanceSuiteCatchesADefectiveStore`;
  - item 4 `[high]` says *"`Conflict Outcome = iota` survives"* — the code now reads
    `Unclassified Outcome = iota // the zero value: the door's fail-safe default`
    (`event/outcome.go:6`) and `Unstated Support = iota` (`event/store.go:14`), with
    `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries` closing its "six rewrites survive"
    clause;
  - item 6 `[high]` names three unfalsified `scripts/` bodies — `scripts/extensionwalk_test.go`
    falsifies all three (`:47`, `:68`, `:94`, `:158`);
  - item 8 `[high]` names thirteen files with no flow row and an English-only doc walk — both are
    closed (94/94 real source files carry a flow row; `deliveryWordings` carries Russian).
  - item 2 `[critical]`'s "no second store value over one log" is also now exercised
    (`event/eventtest/sections_write.go:320`, `sections_resumption.go:36`). Items 3, 5 and 7 I did
    not verify.

    **Corrected 2026-09-12, by measurement:** that last bullet was wrong, and wrong in the way a
    conformance suite is always wrong — the two sections it names use a second store value for
    *reads*, and item 2 is about a **unit of work** carried through one. The mutation it names
    (the store finding its transaction by the value that began it) left `./event/eventmemory/`
    green as written. It is closed now, in `eventmemory`'s own suite and in the conformance suite
    both; see the backlog entry. Item 3 measured the same way: two of its three arms were already
    closed and the third — an opaque value over a buffer filled to the payload's width — really did
    pass `RoundTrip` with nil, and is refused now.
- **Why this severity:** the backlog is the scheduling instrument the whole delivery policy rests
  on. A reader asking *"is anything blocking still open?"* gets nine yeses, of which several are
  false, and cannot tell which. That is worse than nine true yeses, because it teaches the next
  reader to discount the file.
- **Why immediate:** it is the artifact the next phase plans from, and `CLAUDE.md`'s own rule —
  *"Resolved something marked `Status: open`? Flip the status, say what settled it"* — makes this a
  documentation defect rather than tidiness.
- **Close criteria:**
  - [ ] each of items 1–8 and 25 is re-checked against the tree and either flipped with the test
        that settled it, or restated in today's terms;
  - [ ] the survivors carry an owner, as item 25 already does.

### GAP-2 [high][immediate] Nothing a consumer can run checks the three interfaces a consumer must implement

- **Where:** `event/projection/park.go:81` (`Park`), `event/projection/generation.go:60`
  (`Generations`), `event/receipt/receipt.go` (`Ledger`); the defect that proves it is
  `event/eventpg/receipt_integration_test.go:1808`
- **Scale:** systemic (3 interfaces; `eventtest` publishes 2 harnesses and neither is for these)
- **Confidence:** CONFIRMED — `rg 'func Run|func Conform' event/eventtest/*.go` → `Run` (store) and
  `RunCheckpoints` (checkpoints) only. And the defect is measured in the repository:
  `TestFourLedgerDefectsEachBreakTheCaseThatNamesThem`'s first arm asserts that with the `SELECT`
  before the `INSERT`, *"the loser answered Recorded (appended: true)"* and the stream holds **2**
  copies of one operation's events.
- **What / Why this severity:** the phase-4 and phase-5 guarantees are only as strong as code the
  framework does not own. A `Ledger` whose claim reads before it inserts silently double-appends
  under an idempotency key — the exact failure ES-07 exists to prevent — and answers `Recorded` to
  both callers with no refusal from any framework door. `Generations.Active` carries a second such
  obligation, in capitals on `projection.md:640` and `D-141:159`: it **must** be a locking read, or
  at `READ COMMITTED` a retired generation stages an effect anyway. `Park.Holds` carries a third
  (`projection.md:803`): it is now asked **outside** a unit of work as well. None of the three is
  checkable at run time and none has a runnable harness.
- **Why this timing:** it is a contract-shaped gap. Adding `receipttest`/`parktest` later is
  additive, but every consumer who adopts before it exists ships an unchecked implementation, and
  the class of bug is silent duplication of history.
- **Mitigation already in the tree, and it is real:** the reference `Ledger` is
  `_examples/event-receipts/main.go`, `TestTheExampleLedgerIsTheOneTheLiveSuiteProved` compares its
  statements with the live suite's byte for byte, `docs/modules/en/receipt.md:213` admits the
  missing harness, and backlog `[medium]` items 54 and 70 schedule it.
- **Close criteria:**
  - [x] `receipt`'s and `projection`'s obligations get an exported harness on the `eventtest.Run`
        model, or the module pages say in capitals that a consumer must copy the reference and
        cannot verify a variant.
        **Done, 2026-09-12:** `eventtest.RunLedger` (7 sections, 10 defects), `RunGenerations`
        (5 sections, 6 defects) and `RunPark` (6 sections, 8 defects), each falsified against its
        own inventory in `event/eventtest` and five of them again through live PostgreSQL in
        `event/eventpg/application_integration_test.go`. `Effects` remains uncertified: it is a
        one-method sink whose obligation is that what it does rolls back with the unit, and a
        harness holding the interface cannot see what an implementation wrote.

### GAP-3 [medium][deferred] The wait's stated cost is understated on both module pages and in the guide

- **Where:** `docs/modules/en/projection.md:587`, `docs/modules/ru/projection.md:609`,
  `docs/usage-guides/event-sourcing.md:324-327`
- **Scale:** local (one number, in three places, EN and RU agreeing with each other)
- **Confidence:** CONFIRMED by reading the two functions. Per poll: `parked()`
  (`wait.go:446`) issues **one** `Park.Sequences` plus one `Holds` per mark sequence key until the
  first true; `surveyed()` (`generation.go:158`) issues **one** `Load` per cover member **plus** a
  `neverHandedDown` read for each member whose row is fresh. So a healthy four-member cover with a
  one-key mark is `1 + 1 + 4 = 6` reads a poll, and at the 50 ms default that is **120 a second**;
  the rebuild case is `1 + 1 + 8 = 10`, i.e. **200 a second**. The pages say **80**, and *"fifty
  concurrent waiters … about a thousand a second"* where 50 × 80 is 4 000.
- **What:** the plan's own correction P-7 required the page to carry `1 + (1 or 2) × |cover|`
  *"worked at the worse end — a four-partition rebuild wait is 180 reads a second, not 100"*. The
  page carries a single number lower than even the optimistic reading, and the fifty-waiter
  sentence is off by 4–5×. P-7's own justification is the reason this is worth writing down: *"A
  number a page states and a deployment then measures differently is how a page stops being read at
  all."*
- **Why this timing:** documentation-only, no contract moves.
- **Close criteria:**
  - [ ] the three places carry the derivation and the rebuild number, EN and RU together.

### GAP-4 [medium][deferred] `Roadmap.md`'s summary table row for item 15 is the phase-1 sentence

- **Where:** `docs/roadmaps/Roadmap.md:27`
- **Scale:** local
- **Confidence:** CONFIRMED — the row reads *"a named aggregate; the vocabulary, the in-memory store
  and the conformance suite are delivered"* while §15 at `:319-470` describes the PostgreSQL store,
  projections, partitions, generations, the wait, the receipt, the bounded read and D-145.
- **What:** the table is the part of that file anybody reads first, and it is four phases behind its
  own section. `CLAUDE.md` treats doc drift as a defect, not untidiness.
- **Close criteria:**
  - [ ] the row names what §15 now says is left: retention/archival and `eventpgfx`.

### GAP-5 [medium][deferred] ES-05's one missing clause is argued only in an agent artifact

- **Where:** absence in `docs/`; the argument is in
  `.agents/artifacts/gaps/EVENTSOURCE_BACKLOG.md` `## P5` item 1
- **Scale:** local
- **Confidence:** CONFIRMED — `rg -i degraded` over `docs/modules/{en,ru}/projection.md`,
  `UC-036` and `D-14*.md` returns only `PhaseDegraded`, which is the projector's readiness and not a
  wait's verdict.
- **What:** the appendix asks for *timeout **or** degraded*; one shipped and the other's refusal is
  unargued in the doc layer. The brief's own standard for an acceptable partial — ES-06's, in
  capitals on the module page and in an ADR — is one layer above where this sits.
- **Close criteria:**
  - [ ] one paragraph on `projection.md` (both languages) saying why a wait cannot report a phase,
        or a second verdict that does.

---

## Remediation order

1. **GAP-1, the backlog triage** — S, blast radius: the next phase's plan. Everything else is read
   through this file; do it first so the rest is scheduled against a true list.
2. **GAP-2, a harness for `Ledger` / `Park` / `Generations`** — L, blast radius: every consumer's
   own code. Contract-shaped, so it is cheaper before the first tag than after; the reference
   implementations that a harness would be written against already exist and are already pinned.
3. **DoD 8, the `GOWORK=off` event consumer** — M, blast radius: release confidence. Depends on the
   first tag for the `otel-consumer.sh` form, so the repeatable half is a local-replace fixture now
   and the tag-resolving half at release.
4. **DoD 10's mixed-release writer arm** — M, blast radius: an upgrade rehearsal. One more arm on
   `TestStoredRowsAreUpcastAndNeverRewritten` where the v2 build **writes** and the v1 build reads;
   the refusal path already exists, only the rehearsal does not.
5. **The tenancy × event composition fixture** — M, blast radius: a matrix row with no proof. Not
   urgent while no doc claims tenancy support; urgent the day one does.
6. **GAP-3, GAP-4, GAP-5** — S each, documentation only, no dependency on the above.

---

## What a consumer adopting this today hits first

I read the guide as a newcomer and ran the two new examples against the live database, unmodified:

```
$ cd _examples && GOWORK=off go run ./event-wait
reached after 2 poll(s): at=9 behind=0 moved=true parked=false
a mark minted inside the writing transaction is refused, before the commit it would have described
parked after 2 poll(s): quarantined=2, 3 letter(s) held, sequences [waitorder/parked-…]

$ cd _examples && GOWORK=off go run ./event-receipts
Once: verdict=[verdict recorded] range=3..3 complete=true
repeat: verdict=[verdict repeated] range=3..3, and no second append
collision: the same key with other content is refused, and nothing was appended
in flight: [standing unresolved], horizon … — report it as in flight, do NOT re-issue the command
```

Both work first time. The daily API is good: `WaitOf(spec, cover)` derives the five facts a wait
needs from the projector's own `Spec` rather than asking a request handler to restate them;
`receipt.Once` owns the claim/decide/append/complete order so it cannot be written in the wrong one;
`StateAt` returns no token so a historical read cannot become the first half of a write. The guide's
Go fences are compiled by a test. This is not a clumsy surface.

**Top risks, in the order a consumer meets them:**

1. **Three interfaces to implement, with obligations nothing checks.** To get `Reached` to mean
   *applied* rather than *delivered* you need a `Park`; to rebuild you need `Generations`; to answer
   "did my command happen?" you need a `Ledger`. All three are yours, all three carry rules stated
   in capitals, and none has a harness (GAP-2). The `Ledger`'s statement ordering is the sharpest:
   get it backwards and two callers both append under one idempotency key, with no refusal.
2. **The `Generations.Active` locking-read rule.** `projection.md:640` / `D-141:159`: a plain read
   at `READ COMMITTED` lets a generation that no longer serves reads stage an effect anyway. It is
   documented in capitals and is invisible to every test a consumer can run.
3. **A codec that is not byte-stable makes every receipt retry a refusal.** The digest is over the
   encoded bytes; a clock, a fresh id or a map in iteration order in a payload turns every retry
   into `ErrCollision`. The guide says so (`:399`) and calls it the consumer's obligation — loud and
   never wrong, but it will be met in production by somebody who did not read Part VI.
4. **The wait's cost is understated by 1.5–2.5× on the page you would size from** (GAP-3), and
   `Every` is the only lever.
5. **`idle_in_transaction_session_timeout` is load-bearing.** One leaked connection stalls every
   projection over the schema *and* keeps every `Resolve` `Unresolved` for as long. It is the first
   bullet of Part VIII and deserves to be higher.
6. **No isolated-consumer proof.** Nothing in the repository demonstrates that a program importing
   only `event` + `event/eventpg` resolves a clean graph (DoD 8). `_examples` shares one module with
   ent, gorm, gin, fiber and the OTel SDK, so a consumer's first `go mod tidy` is the first time
   anyone will find out.
7. **No runbook.** Restore/replay, capacity and incident procedure are undocumented for this
   subsystem (DoD 10), and the retention/archival interlock against every projection's stored cursor
   is named as future work in two places rather than written.

---

## What I did not check

- **`event/eventtest`'s internal detection power.** Backlog `[critical]` items 2 and 3 and `[high]`
  items 5 and 7 name specific surviving mutations inside `eventtest`, `comparison.go`, the reader
  and the analysers. I verified only that items 1, 4, 6 and 8 have had their stated conditions met;
  I did not re-drive the 165-assertion neutralisation measurement that produced the 16 % number.
  **Driven on 2026-09-13 by an independent measurement**, which also showed that number has a ~24 %
  ceiling and must not be read as a detection rate — see the correction above.
- **The phase-4 appendices at their source.** ES-01…ES-04 and ES-06 I checked by artifact presence
  (files, ADRs, flows, live tests) and by the phase-4 gate's record, not sentence by sentence
  against the Russian originals — the roadmap has removed them, so the originals are no longer in
  the file.
- **The RU module pages beyond spot checks.** I confirmed the `receipt` row, the wait ordering
  section and the cost paragraph; I did not read all six RU pages against their EN counterparts.
- **`make tidy`, `make vuln`, `make generate`, `make api`'s write path.** `check-tidy` and
  `check-replaces` ran green inside `make check`; I generated the surface to `/tmp` rather than
  letting `make api` write, so the write path itself is untested by me.
- **The three foreign red arms' causes.** I reproduced all three and confirmed none is under
  `event/`; I did not diagnose them.
- **Concurrency beyond what the live suite drives.** I ran the tagged suite twice under `-race` and
  read `wait.go`, `claim.go` and `repo.go`; I wrote no additional concurrent driver of my own.
- **`_examples/event-partitions`, `event-generations`, `event-checkpoints-elsewhere`.** Built and
  vetted by `make examples`; I ran only `event-wait` and `event-receipts`.

---

# Round 2 — final closing audit (2026-09-12, clean context)

**Scope.** The four Definition-of-done items the closing round answered (8, 10, 1's second half,
6's tenancy half), plus all twelve walked again — because a fix can break a neighbour — plus the
gates. Nothing was fixed. Nine files were mutated and restored; the **whole tree** (2 348 files,
`.git` excluded) was hashed before and after and the two manifests are **identical**, with
`git status --porcelain | wc -l` at 21 both times.

**Verdict in one line.** The two not-met items are **closed and I broke both of their checks
myself to prove they can fail**; the two partials are now **10 met / 2 partial / 0 not met** — but
the roadmap is **not closed**, because item 1's ADR clause was deliberately declined, item 12's
`make unit` is red on three foreign arms, and **3 `[critical]` + 7 `[high]` backlog entries remain
open**, which the delivery policy says block.

## Gate commands, run by me

| Command | Result |
|---|---|
| `make check` | **GREEN, 13 arms** — the eleven of round 1 plus `check-event-combinations` (*"ok, 10 names refused across 1686 files and 37 modules"*) and `check-event-consumer`. `CHECK EXIT=0`, 22.7 s |
| `make vet` | **GREEN**, every workspace module plus `./_examples`. `VET EXIT=0` |
| `make examples` | **GREEN**, `EXIT=0` |
| `gofmt -l .` | **silent** (0 lines) |
| `git diff --check` | clean, exit 0. The five untracked paths scanned separately: 0 trailing-whitespace lines, 0 tabs in the two Markdown pages |
| `docs/api/surface.md` | `generate_api /tmp/surface_fresh.md` → `diff -u` **0 lines**. The closing round added no exported API |
| `go test -race ./event/...` | **GREEN** — `event 6.749s`, `eventmemory`, `eventtest`, `projection 6.103s`, `receipt 1.020s` |
| live suite ×2 | `ok …/event/eventpg 124.374s` then `127.542s`, `-race -count=1 -tags=integration`, both exit 0 |
| live suite, DSN unset | **FAILS, does not skip**, and prints the command. `FAIL … 0.002s` |
| `test/eventflow` ×2 untagged, ×2 tagged | **GREEN** all four (`1.006s`–`1.008s`) |
| `make unit` | **NOT GREEN — exit 2**, on three foreign arms (below) |

### The three foreign red arms, reproduced in a clean worktree

`git worktree add /tmp/vv-head HEAD` — HEAD, **without** the closing round's 21 uncommitted paths:

| Arm | At HEAD | Mine? |
|---|---|---|
| `./scripts` `TestNoI18nPackageCostsMoreThanItsErrorSeam` | fails identically: *"the i18n extension reaches github.com/go-json-experiment/json/jsontext outside its error seam"* | **No** |
| `./i18n` `cmd/vv-i18n` | **29** `--- FAIL:` lines, the same count as in the working tree | **No** |
| `./test/auditflow` `TestGeneratedAuditProfilesSeparateServingRuntimeFromDeploymentAuthority` | fails identically: *"generated serving health check: audit serving runtime is not ready"* | **No** — and its file (`generated_profile_integration_test.go`) imports `app/module`, `audit`, `health` and the two local fixture packages, and contains the string `event` **zero** times |

`make unit` still aborts at the root module, so it reports one and never reaches the other two; I ran
those two by hand. The two audit×event composition proofs in that red package
(`TestApplicationTranslatesExactEventCommitAndJobAttemptAtOneBoundary`,
`TestApplicationCapturesLogicalStorageAndActivatedProfileAsIndependentEvidence`) **pass individually
under `-race`** — the red is beside them, not under them.

## Definition of done, walked again

| # | Round 1 | Now | What settled it |
|---|---|---|---|
| 1 | partial | **partial** | names met and falsified four ways; **no ADR names an aggregate** and the roadmap now records the refusal with a reason |
| 2 | met | **met** | `find event -name go.mod` → 1; and the names are now checked, not just the direction |
| 3 | met | **met** | live ×2 |
| 4 | met | **met** | unchanged, live |
| 5 | met (vacuously) | **met (vacuously)** | no base adapter shipped |
| 6 | partial | **met** | audit fixture passes individually; tenancy fixture exists, and I broke it |
| 7 | met | **met** | unchanged |
| 8 | **not met** | **met** | `scripts/event-consumer.sh` + `check-event-consumer`; three graphs, six breakages each loud |
| 9 | met | **met** | unchanged, green |
| 10 | **not met** | **met** | runbook EN+RU; the v2-writes/v1-reads rehearsal is live and I proved it bites |
| 11 | met | **met** | surface 0-diff; the runbook is inside the doc walk (proved by mutation) |
| 12 | partial | **partial** | four of five sub-commands green; `make unit` red on three foreign arms |

**10 met, 2 partial, 0 not met.**

## Every check I broke, and what it said

Each mutation was applied, run, and reverted from a `/tmp` copy; every restore was verified by
`sha256sum`, and `check-event-kernel` was re-run after the one inside `event/`.

| # | What I broke | Arm | What it answered |
|---|---|---|---|
| 1 | `mkdir eventotel && echo 'package eventotel' > eventotel/doc.go` | `check-event-combinations` | named the directory **and** the package clause; `make check` **exit 2** |
| 2 | `tmpbridge/doc.go` declaring `package eventaudit` | same | *"these files declare a combination package this repository refuses to have: ./tmpbridge/doc.go"* |
| 3 | `tmpmod/go.mod` = `module github.com/frostgrove/vv/eventkafka` | same | *"./tmpmod/go.mod: github.com/frostgrove/vv/eventkafka"* |
| 4 | ran it in a tree with a `go.mod`, a README and no Go file | same | *"found 0 Go files and 1 modules, so the names it refuses were compared against nothing"*, exit 1 — **the vacuity guard is real** |
| 5 | neutered the directory arm in `checks.sh` (`-name "zzz-$name"`) | `go test ./scripts` | `TestACombinationPackageNameIsRefusedWhereverItIsWritten/a_directory_named_for_a_combination` **FAILED**: *"a directory named eventotel was admitted with 0"* |
| 6 | root-only fixture imports `event/eventpg` | `check-event-consumer` | *"root-only could not resolve its module graph with go mod tidy, so nothing about this graph was measured"* + go's own text |
| 7 | root-only fixture reduced to `func main() {}` | same | *"root-only resolved **no** github.com/frostgrove/vv module at all, so its graph was never loaded and nothing below it was measured"* |
| 8 | root-only fixture importing only `event` | same | *"links 4 packages … and this graph is written to link at least 8"* — the floor bites |
| 9 | event-only fixture blank-imports `pgx/v5/stdlib` | same | listed **29** third-party packages it must not link, pgx and x/text included |
| 10 | expected module set for event-only given a fourth module | same | a unified diff of expected vs resolved |
| 11 | `GOMODCACHE=$(mktemp -d)` — a cold cache | same | *"could not resolve its module graph … module lookup disabled by GOPROXY=off"*, exit 1 — **a graph that cannot load is loud, never quiet** |
| 12 | `test/eventflow`: `repo()` reads the tenant from the context instead of `authority.Scope` | `go test ./test/eventflow` | **4 subtests FAILED** — unbound, foreign-authority, out-of-generation and suspended each answered `<nil>` where they must refuse |
| 13 | `event/fact.go`: `revision > len(chain.links)` replaced by a clamp to the last link | live suite | `TestARolledBackBuildMeetingRevisionTwoRefusesRatherThanFolding` **FAILED** on two subtests: *"a build that retains one revision read a stream holding revision 2 and answered &lt;nil&gt;"* |
| 14 | appended a citation of a test that does not exist to the runbook | `go test ./scripts` | `TestEveryTestNameTheDocsCiteExists` **FAILED**, naming `docs/usage-guides/event-operations.md:402` — **the new page is inside the doc walk** |
| 15 | deleted `check_event_combinations` **and** `check_event_consumer` from the `all)` case | `make check` | **exit 0**, and the whole `./scripts` suite reported only the pre-existing foreign failure — see GAP-6 |

## The three isolated graphs, resolved by hand

Not the script's word: I built two of them myself in `/tmp`, outside the repository,
`GOWORK=off GOPROXY=off GOTOOLCHAIN=local`.

| Graph | `go list -m all` | linked, non-stdlib | third party linked |
|---|---|---|---|
| root-only | `example.com/…` + `github.com/frostgrove/vv` only. **No `go.sum` is written at all** | 8: `utils`, `crud`, `errs`, `event`, `eventmemory`, `runtime`, `projection`, `receipt` | **none** |
| event-only | 8 modules: the two vv ones plus pgx, pgpassfile, pgservicefile, puddle, x/sync, x/text. `go.sum` = 12 lines | 14 | **none** |
| composed | 24 modules | 29 | 15, and one of them is **`github.com/cespare/xxhash/v2`**, not OTel (GAP-9) |

The event-only row is the interesting one and the script's own comment says it: `eventpg` requires
pgx **for its live fixtures** and imports none of it, so a consumer's `go mod tidy` fetches pgx,
records its sums and links nothing — `go: … eventpg tested by eventpg.test imports pgx/v5/pgconn`.
A consumer behind a proxy allowlist needs pgx allowed for a binary that contains no pgx.

## Capacity — consistent, and the number round 1 reported was the wrong one

I counted the statements in the code rather than trusting either page.

- `waiter.parked` (`event/projection/wait.go:443-465`): **one** `Park.Sequences`, then
  `if count == 0 { return false, nil }` — **`Holds` is never called while the queue is empty**.
- `surveyed` (`event/projection/generation.go:158-191`): **one** `Load` per cover member, plus one
  more per member whose row is `Fresh()`, through `neverHandedDown` → `retired`
  (`event/projection/topology.go:205-218`, exactly one `Load`).
- `waiter.resolves`: `Generations.Active` **twice over the whole wait** — poll 1 and the reaching
  poll — never per poll.

So a four-member cover at `Every`=50 ms: **5 reads a poll = 100/s** healthy, 6 = 120/s with a
non-empty queue, 9 = 180/s rebuilding, 10 = 200/s both. Fifty waiters = 5 000/s. The same four
numbers appear in `docs/modules/en/projection.md:587-602`, `docs/modules/ru/projection.md:609-624`,
`docs/usage-guides/event-sourcing.md:324-332`, `docs/usage-guides/event-operations.md:281-302` and
`docs/usage-guides/ru/event-operations.md:296-315` — **five places, identical**, EN and RU. No `80`
and no *"about a thousand a second"* survives anywhere under `docs/`.

They are also what the tests measure: `TestAWaitStartsNothingAndSavesNothing` asserts **4** loads for
one poll over a recorded four-member cover and **8** for a fresh one, and
`TestAHealthyWaitAsksSequencesAndNeverHoldsOrHoles` asserts **3** `Sequences` and **0** `Holds` over
three polls.

**Round 1's GAP-3 arithmetic (120/200) was itself wrong** — it charged a `Holds` per poll that the
code gates behind a non-zero count. The delivered numbers are right and the correction that produced
them is right; the closing round improved on its own brief here rather than following it.

## Findings

### GAP-6 [medium][deferred] Both new arms are in `make check` and nothing holds them there

- **Where:** `scripts/checks.sh:742-743` (the `all)` case); the absent test in `scripts/`
- **Scale:** local (2 arms, 1 script with no test of any kind)
- **Confidence:** CONFIRMED — I deleted both lines from the `all)` case: `make check` exited **0**,
  and `go test -count=1 ./scripts/` reported exactly one failure, the pre-existing foreign one. The
  precedent is in the same directory: `scripts/otel_release_test.go:28` asserts
  `"$SCRIPT_DIR/otel-consumer.sh"` appears in `release.sh` and `:62` asserts that script's source
  still carries `GOWORK=off`, `GOENV=off`, `GOMODCACHE=`, `GOCACHE=`, `GOPATH=`, `GOFLAGS=-mod=mod`
  and its fixture path; `scripts/i18n_release_test.go:15,28` do the same for `i18n-consumer.sh`.
  `event-consumer.sh` is named by no test at all.
- **What / Why this severity:** Definition-of-done item 8 is met *because that arm runs*. Remove the
  line — in a merge, in a rebase, in a tidy-up of the `all)` case — and item 8 silently un-meets
  with a green `make check`. `check-event-combinations` is half-protected: `TestThisRepositoryCarries
  NoExtensionCombinationPackage` still invokes it directly, so the property survives its removal
  from `make check` (in a `./scripts` arm that is red today for a foreign reason). The consumer arm
  has no such second holder.
- **Why this timing:** it does not change a contract and nothing is wrong today. It is one test on a
  template that already exists twice in the same package.
- **Close criteria:**
  - [ ] a test asserts `check_event_consumer` and `check_event_combinations` are in `checks.sh`'s
        `all)` case, on the `otel_release_test.go:28` model;
  - [ ] a test asserts `event-consumer.sh` still carries `GOWORK=off`, `GOPROXY=off` and its three
        fixture paths.

### GAP-7 [medium][deferred] `check-event-consumer` needs a warm module cache, and `make check` is offline

- **Where:** `scripts/event-consumer.sh:27` (`consumer_environment=(GOWORK=off GOPROXY=off …)`),
  which does **not** set `GOMODCACHE` — unlike `scripts/otel-consumer.sh:17-21`, which gives the
  consumer a private one
- **Scale:** local (2 of the 3 graphs — event-only and composed)
- **Confidence:** CONFIRMED — `GOMODCACHE=$(mktemp -d) ./scripts/event-consumer.sh` → *"event-only
  could not resolve its module graph with go mod tidy … module lookup disabled by GOPROXY=off"*,
  exit 1, after root-only passed.
- **What / Why this severity:** a fresh clone whose module cache has never seen pgx or the OTel SDK
  gets a **red `make check` on a green tree**. The failure is loud and names the cause, which is
  exactly the property the brief demanded, so this is a usability cost and not a correctness one —
  but `make check` is the command this repository tells a contributor to run before reporting a task
  done, and its first run on a new machine will fail here.
- **Why this timing:** it is a trade the script's own header argues for (offline `make check` before
  a tag exists) and changing it means choosing between offline and cold-start.
- **Close criteria:**
  - [ ] either one sentence in the script's header and in `docs/` saying the arm needs a warmed
        cache and what warms it, or a refusal that distinguishes *"this graph is wrong"* from
        *"this machine has never downloaded pgx"*.

### GAP-8 [medium][deferred] The first incident in the runbook branches on facts it never says how to read

- **Where:** `docs/usage-guides/event-operations.md:316-333` and its Russian counterpart at
  `:331-352`
- **Scale:** local (1 of the 4 incidents; the other three are actionable)
- **Confidence:** CONFIRMED — `rg -n 'checkpoints|Observer|State\.' docs/usage-guides/event-operations.md`
  returns one line, `State.Parked` in a table of meanings. The page gives SQL for the `xmin` stall
  (`:144-149`) and for the park queue (`:185-191`) — the second over a table that is the
  **consumer's own** — and none for the `checkpoints` table, which is **`eventpg`'s** and whose
  columns are fixed and knowable: `event/eventpg/schema.go:311-330` declares `projection`, `cursor`,
  `advance`, `highest`, `applied`, `quarantined`.
- **What / Why this severity:** step 1 is *"Is `Highest` moving at all, for **every** projection over
  the schema?"* — the single most valuable question on the page, because a yes sends the operator
  straight to Part 2 — and an operator holding psql at 03:00 is not told the one-line `SELECT` that
  answers it. Steps 2–4 (`PhaseBlocked`, halted, two writers of one name) branch on in-process state
  with no stated observation path; `Observer`/`State` is documented on `projection.md` and the
  runbook never sends the reader there for this.
- **Why this timing:** documentation only, no contract moves.
- **Close criteria:**
  - [ ] a `SELECT` over the `checkpoints` table beside the two queries already on the page, in both
        languages;
  - [ ] one sentence saying where a phase is read from and that a deployment must expose it.

### GAP-9 [low][deferred] `FL-037` says the composed graph's only third party is OpenTelemetry

- **Where:** `docs/ai/flows/FL-037-a-recorded-fact-becomes-a-postgresql-row.md`, the composed row of
  the three-graph table
- **Confidence:** CONFIRMED — resolved by hand: 15 third-party packages, of which
  `github.com/cespare/xxhash/v2` is not under `go.opentelemetry.io`. The script is right and says so
  (`links_nothing_but "$label" "$prepared" go.opentelemetry.io github.com/cespare/xxhash/v2`); only
  the flow's prose rounds it away.
- **Close criteria:** - [ ] the row names xxhash, or says "the OTel API and what it pulls".

### GAP-10 [low][deferred] A timestamp is compared with a position

- **Where:** `docs/usage-guides/event-operations.md:332` — *"Then `At` moves and `Highest` does not
  lag it by much"*
- **Confidence:** CONFIRMED — `Progress.At` is a `time.Time` (`event/checkpoint.go:40`) and
  `Progress.Highest` is a `Position` (`:25`). Part 0's *"alert on the pair"* uses them correctly; the
  incident step reads as if one lags the other.
- **Close criteria:** - [ ] say what `Highest` should not lag by much — the log's head.

### GAP-11 [low][deferred] The rebuild recipe has a runnable program and does not name it

- **Where:** `docs/usage-guides/event-operations.md:42-64` (the recipe) and `:391-400` (where to go
  next)
- **Confidence:** CONFIRMED — I ran `GOWORK=off go run ./event-generations` unmodified and it printed
  the recipe's five steps and the rollback in order: *"generation 2 rebuilt beside it … reached=true
  behind=0 holes=0, and it staged nothing"*, *"the read target moved … in one fenced write"*, *"the
  rollback is the same call with From and To exchanged"*. `_examples/event-partitions` and
  `event-checkpoints-elsewhere` also ran first time. Separately, the runbook's one Go fence is
  outside `TestEveryGoFenceInTheEventSourcingGuideIsCompiled`, which names only
  `../docs/usage-guides/event-sourcing.md` (`scripts/docs_test.go:1829`) — the two calls it shows are
  correct today (`Redrive.Sequence`/`Any` at `event/projection/redrive.go:194,202`, returning
  `Retried` at `:64`), but nothing keeps them so.
- **Close criteria:** - [ ] the recipe names `_examples/event-generations`; - [ ] the fence is
  compiled or the page says it is illustrative.

### Foreign, and not this round's: two dead links in a file this round edited

`docs/Index.md:9` and `:11` point at `decisions/Index.md` and `flows/Index.md`; the directories are
`docs/ai/decisions/` and `docs/ai/flows/`. `git diff docs/Index.md` shows those lines untouched — the
round added two usage-guide bullets and nothing else — so this is pre-existing and outside the
dimension. It is recorded because the round had the file open. Every link in **both** runbooks and
every link the round added resolves.

## Is the roadmap closed?

**No — and nothing the closing round delivered is why.** Three things are between this and closed:

1. **Item 1's ADR clause.** *"one accepted ADR names the aggregate"* is unmet and was **declined on
   product grounds**, recorded in the roadmap's own table: *"inventing one to satisfy the clause
   would put a fictional domain in an accepted ADR"*. That is the right call and it is written down.
   The item is still not met by its own text.
2. **Item 12.** `make unit` is red on three arms, all three foreign, all three reproduced at HEAD in
   a clean worktree.
3. **The backlog, which the delivery policy makes the deciding instrument.** `.agents/artifacts/gaps/
   EVENTSOURCE_BACKLOG.md` now holds **308** items: `{critical: 3, high: 10, medium: 158, low: 134,
   untagged: 3}`. Removing the four entries already marked `CLOSED`/`REFUSED` leaves **3 critical and
   7 high open**, and the policy defers only medium and low. All of them are pre-policy P1/P2 debt
   plus the round's own item 69 — the round recorded its own blockers rather than hiding them, which
   is the artifact working — but a policy that says high blocks and ten highs that are open cannot
   both be true at once.

Round 1's GAP-1 (four of nine blocking entries have had their stated closure conditions met and were
never flipped) and GAP-2 (nothing a consumer can run checks `Ledger`, `Park` or `Generations`) are
**untouched and still open**, by design: neither was in this round's brief, and the round says so at
`## Closeout` item 69.

## Remediation order

1. **GAP-1 of round 1 — triage the nine pre-policy blocking entries.** S. Everything is scheduled
   through that file and several of its yeses are false.
2. **GAP-2 of round 1 — a runnable harness for `Ledger` / `Park` / `Generations`.** L. The single
   largest thing between this library and a consumer who cannot silently double-append.
3. **GAP-6 — hold the two new arms with a test.** S, blast radius: whether item 8 stays met.
4. **Backlog 67 — an event arm in `release.sh`** on the `i18n-consumer.sh` `git archive` + `file://`
   proxy model, which needs no published tag and proves the half `check-event-consumer` cannot:
   resolution with **no replace**, from a proxy, outside this tree. M.
5. **GAP-8 — the checkpoint query in the runbook.** S.
6. **GAP-7, GAP-9, GAP-10, GAP-11.** S each, no dependencies.

## What I did not check, this round

- **`make integration` as a whole**, and `make tidy`, `make vuln`, `make generate`, `make api`'s
  write path. `check-tidy` and `check-replaces` ran green inside `make check`; I generated the
  surface to `/tmp` rather than letting `make api` write. `make integration` would be red on the
  foreign `test/auditflow` arm regardless; I ran `test/eventflow` under the integration tag twice
  instead.
- **The composed consumer graph by hand for its vv module set** — the script asserts it and I read
  its output; I resolved root-only and event-only myself and only listed composed's third party.
- **The Russian runbook line by line.** I verified its structure (21 headings against 21), its five
  capacity numbers, its links and that it is inside the doc walk; I did not read it against the
  English sentence by sentence.
- **`event/eventtest`'s internal detection power**, the phase-4 appendices at their Russian source,
  and the three foreign reds' causes — all as round 1, unchanged.
- **Concurrency beyond the live suite.** Two `-race` runs of the tagged suite and four of
  `test/eventflow`; I wrote no driver of my own.
