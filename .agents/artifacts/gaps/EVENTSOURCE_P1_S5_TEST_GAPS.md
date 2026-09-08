# EVENTSOURCE_P1 — S5 `event/eventtest` — TEST GAPS

## Round 1 — econv-test-reviewer (clean context) — 2026-09-07

**Baseline.** `go build ./...`, `go vet ./event/...`, `gofmt -l event` all clean.
`go test -race -count=1 ./event/...` green: `event` 1.279 s, `event/eventmemory`
1.053 s, `event/eventtest` 1.456 s — 1.79 s wall for the three. `-race -count=3`
green; five runs of `-race -count=2 -shuffle=on` green, no flake, no order
dependence. Both S5 phase-5 checkpoint `-list` count clauses hold (1 and 4).
`-fuzz FuzzKeysReportsACollisionExactlyWhenTwoIdentitiesRenderOneKey -fuzztime 30s`:
91 006 execs, no failure, no corpus artifact left behind. Statement coverage of
`event/eventtest` by its own tests: **86.3 %**.

**Every mutation below was applied to a snapshot-verified tree and reverted; the
final `sha256sum -c` over all 87 `event/**.go` files reports zero differences and
`go test -race -count=1 ./event/...` is green.**

### Mutation log

The unit under review is a *conformance suite*, so "break the implementation"
means two different things and both were done: break the **suite** and see
whether its own tests (`suite_test.go`, `defects_test.go`, `inventory_test.go`,
`proxies_test.go`) go red; break the **kernel / the store** and see whether the
suite goes red.

| # | What was broken | Where | Suite result |
|---|---|---|---|
| M1 | Deleted the whole uncertainty-outranks-cancellation clause (C3 / §INV-029) | `sections_lifecycle.go:44-51` | **NOT CAUGHT** — all green |
| M2 | `winners != 1` → `winners < 0` and `len(written) != 1` → `< 0`: the concurrency section admits any number of winners | `sections_write.go:302-307` | **NOT CAUGHT** — all green |
| M3 | Deleted the set-equality tail of `conservation` (§INV-034's only proof) | `sections_read.go:90-102` | **NOT CAUGHT** — all green |
| M4 | Replaced the **entire body** of all nine sections that no defect row names with `ctx := this.context(); _, repo, _ := this.open(); this.load(ctx, repo, this.account("a"))` | `sections_write.go`, `sections_read.go`, `sections_lifecycle.go` | **NOT CAUGHT** — all green, including `TestEverySectionFailsAgainstAStoreThatRefusesEverything` |
| M5 | Deleted the kernel's page-length check | `event/repo.go:235-238` | **CAUGHT** — `TestAnOverLongPageIsRefused`; `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect`: *"the suite no longer detects a store that publishes a stream page shorter than the page it returns: its bounds section was reported passed"* |
| M6 | Dropped `bytes.Clone` from `eventmemory`'s hand-out | `event/eventmemory/read.go:80-83` | **CAUGHT** — `TestTheMemoryStoreSatisfiesTheContract` and `…AtNarrowerLimits`, `payload ownership: failed` |
| M7 | Shrank the defect inventory from 18 rows to 1 | `defects.go:36-73` | **NOT CAUGHT** — all green |
| M8 | Gutted `Run` to `sweep(...) { t.Log(line) }` — no `t.Error` on `failed`, none on `unreported`, none on `certified()==0` | `suite.go:91-106` | **NOT CAUGHT** — all green |
| M9 | M8 **plus** M6 together: the gutted `Run` over a store that hands out its own mutable memory | — | **NOT CAUGHT** — `go test -run TestTheMemoryStoreSatisfiesTheContract ./event/eventmemory/` → `ok`. A demonstrably broken store is certified. |
| M10 | `probe.unable` made a no-op, so an unaskable clause reports `passed` | `probe.go:67-69` | **CAUGHT** — `TestACursorAStoreCannotParseIsRefusedRatherThanReadFromTheBeginning:270` |
| M11 | Deleted the `missing()` gate from `admit` (anti-vacuity rules 1 and 3 at the door) | `suite.go:165-167` | **NOT CAUGHT** — all green |
| M12 | Deleted `contended`'s `ErrConflict` assertion + its balance check, and `aftermath`'s `ErrConflict` assertion | `sections_transactions.go:105-112, 162-164` | **NOT CAUGHT** — all green |
| M13 | Reduced `refusal classes`' five-row policy table to "issue the append, ignore the answer" | `sections_lifecycle.go:164-194` | **NOT CAUGHT** — all green |

**11 of 13 mutations survived.** The two that were caught are both mutations of
code *outside* S5 (the kernel and the memory store). **Every single mutation of
S5's own deliverable went undetected.**

---

### GAP-1 [critical][immediate] `Run` — the one exported function the whole section exists to ship — has no test, and a gutted `Run` certifies a demonstrably broken store

- **Where:** `event/eventtest/suite.go:91-106`; the tests that should cover it,
  `event/eventtest/suite_test.go:22-40` (`TestARunThatCertifiedNothingFails`),
  `:48-64` (`TestASectionThatNeverReturnedIsNotReportedPassed`), `:14-16`
  (`TestATrivialStoreNeedsNoInternalAccess`), `:18-20`
  (`TestATransactionCapableStoreSatisfiesTheContract`);
  `event/eventmemory/conformance_test.go:14-24`.
- **What:** `Run` has exactly three behaviours beyond dispatching sections —
  `t.Error(given.reason)` when a section is `failed`, `t.Errorf(...)` when a
  section is `unreported`, and `t.Error(...)` when `certified(verdicts) == 0`.
  **All three can be deleted and the whole tree stays green** (M8). Every test
  in the suite's own package that observes verdicts goes through
  `eventtest.Certify` (`export_test.go:38-45`), whose reporter is
  `func(t *testing.T, given verdict) { t.Log(given.line()) }` — by construction
  it *cannot* fail a test. The only four call sites of `Run` are
  `TestATrivialStoreNeedsNoInternalAccess`,
  `TestATransactionCapableStoreSatisfiesTheContract` and the two
  `event/eventmemory` conformance tests, and all four are run against stores that
  pass, so `Run`'s failure path is never taken by anything. `go tool cover`
  confirms: `suite.go:91 Run 66.7 %`.
- **Why this severity:** M9 is the proof and it is not hypothetical. With `Run`
  gutted — a mutation nothing in the tree detects — I re-applied M6 (the memory
  store returns its own retained payload memory instead of a clone, a real
  §INV-021 violation that `eventmemory`'s own unit tests and the `payload
  ownership` section both detect) and ran
  `go test -count=1 -run 'TestTheMemoryStoreSatisfiesTheContract' ./event/eventmemory/`:
  **`ok`**. A store that corrupts a caller's page on the next read is certified
  by the conformance suite. This is the exact failure mode `defects_test.go:9-13`
  names in its own comment — "one that tests nothing passes everything, and
  nobody notices until a store is wrong in production" — and the suite has that
  hole in its front door.
- **Why this timing:** `Run` is the artefact phase 2's `eventpg` runs *verbatim*
  and the *only* thing `eventpg` can call — `Certify`, `Defects` and
  `SectionNames` all live in `export_test.go` and are not on the package's
  surface. Phase 2's entire conformance evidence for a PostgreSQL store is one
  call to a function that no test has ever proved reports a failure. UC-043
  ("a store implementer runs the suite") and the two anti-vacuity rules the plan
  assigns to `Run` (§S5 "a run in which every claimed section was skipped
  **fails**") are unproved.
- **Close criteria:**
  - [ ] A test drives `Run` (not `Certify`) against a store that fails a section
        and asserts that the `*testing.T` it was handed was marked failed —
        e.g. through a `testing.T` obtained from an inner `t.Run` whose result is
        read, or by extracting `Run`'s decision into a pure function over
        `[]verdict` that both `Run` and the test call.
  - [ ] The same for the `unreported` branch and for the `certified() == 0`
        branch, each with a control that a correct store is *not* reported.
  - [ ] Mutation re-run: deleting each of the three `t.Error*` calls in `Run`
        independently turns exactly one test red; the message is pasted.
  - [ ] `go tool cover -func` shows `Run` at 100 %.
- **Status:** **closed** — round 1 of the disposition, 2026-09-07.
  `Run`'s three reports and `admit`'s four are named values (`suite.go:91-118`,
  `certifiedNothing`, `noVerdictFrom`, `buildsNoStore`, `answersNoStore`,
  `narrowKey`), and `event/eventtest/run_test.go` drives nine store shapes
  through **`Run`** — not `Certify` — in a child of this same test binary, then
  reads the child's exit status, what it said, and whether any section was
  reported. The subprocess is the only way to watch a `t.Error` without taking
  it: `Fail` propagates from a subtest to its parent, so no in-process test can
  observe a failed `T` and stay green. The pattern is the repository's own
  (`event/crossings_test.go` shells out to `go build`).
  **Mutation, each applied and reverted:**
  - per-section `t.Error(given.reason)` deleted → *"a run over a store that fails
    one section left the test it was handed green, where a store like this must
    leave it failed"*
  - the `unreported` loop deleted → the same message for *a section whose factory
    hook leaves the run*
  - the `certified(verdicts) == 0` branch deleted → *"a run over a store that
    refuses every operation never reported \"eventtest: this run certified
    nothing — …\", so nothing in this suite would notice if it stopped reporting
    it at all"*
  The control is the first case: a store that satisfies the contract leaves the
  child green and never says the certified-nothing sentence.

### GAP-2 [critical][immediate] Nine of the twenty sections have no control that can fail: their entire bodies reduce to one `Load` and every test stays green

- **Where:** the nine sections `stream identity`
  (`sections_write.go:81-138`), `concurrency` (`:259-313`), `shared backing`
  (`:315-344`), `conservation` (`sections_read.go:69-103`), `global paging`
  (`:154-179`), `monotone visibility` (`:245-260`), `cancellation`
  (`sections_lifecycle.go:13-57`), `lifecycle` (`:59-102`), `store failure
  classification` (`:283-308`). The defect inventory (`defects.go:36-73`) names
  eleven sections and none of these nine.
- **What:** M4. I replaced all nine bodies with
  `ctx := this.context(); _, repo, _ := this.open(); this.load(ctx, repo, this.account("a"))`
  — one store call each, no assertion at all — and
  `go test -count=1 ./event/...` was **green**, including
  `TestEverySectionFailsAgainstAStoreThatRefusesEverything`,
  `TestEverySectionInTheInventoryWasReported`,
  `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect`,
  `TestTheMemoryStoreSatisfiesTheContract`, and the prefilled / narrow / busy /
  shifting runs. M1, M2, M3, M12 and M13 are the same finding at finer grain,
  each targeting one clause rather than a whole section.
- **Why this severity:** these nine sections are the *only* S5 evidence the
  coverage matrix cites for the following, and for each of them the proof can be
  deleted with no test going red:

  | Item | Matrix's S5 proof | Guarded? |
  |---|---|---|
  | UC-018 a load under a cancelled context | `cancellation` | no |
  | UC-036 a bounded read | `global paging` | no |
  | UC-039 repeated reads tile a quiescent log | `global paging` | no |
  | UC-047 close, and everything after | `lifecycle` | no (S3's `TestCloseIsIdempotentAndDecidesNothing` covers `eventmemory` only) |
  | UC-051 the mapper produces an illegal key | `stream identity` | no |
  | UC-057 cancellation during an append, two windows | `cancellation` | no |
  | UC-059 two aggregates, one family | `binding`'s `ErrFamily` arm | no (`binding`'s two defects are `illegalProduct` and `drifting`) |
  | UC-060 the backend fails an append | `store failure classification`, `refusal classes` | no / no (M13) |
  | UC-066 a transaction open and nothing bound | `transactions`' `unbound` case | no |
  | INV-029 cancellation identity, uncertainty outranks it | `cancellation` | no (M1) |
  | INV-030 every question answered by the exact outer value | `refusal classes`, `store failure classification` | no (M13) |
  | INV-032 close is idempotent and decides nothing | `lifecycle` | no |
  | **INV-034 the two reads describe one set** | `conservation` — **its only proof anywhere** | no (M3) |
  | INV-036 a walk tiles | `global paging` | no |
  | INV-038 what is safe to copy and to share | `concurrency` | no (M2) |
  | INV-039 one family names one aggregate | `binding`'s `ErrFamily` arm | no |
  | INV-043 an unstated capability refused at both doors | `binding`'s dishonest-store arm | no |
  | INV-045 a store classifies; the kernel maps that | `store failure classification` | no |
  | UC-037 no monotone-visibility promise | `monotone visibility` | no |
  | UC-038 per-stream order is a subsequence | `global order`'s `subsequence` helper | no — `reusedPositions` sets every `Position` to 1, so `ascending` refuses first and `subsequence` is never reached on the defect path |
  | C2 a decorator's own refusal | `refusal classes` | no (M13) |
  | C3 `errors.Is(err, DeadlineExceeded)` false on `ErrUncertain` | `cancellation` | no (M1) |
  | C9 codec/mapper/fold/clock concurrency | `concurrency` | no (M2) |

  §INV-034 is the sharpest case: `conservation` is its only stated proof in the
  whole plan, and the section's entire assertion body can be deleted silently.
  That is `restrictions.md` §5's "missing test for a stated invariant" —
  the invariant has a *section*, but no test that fails when the section stops
  asserting it, which is the same thing.
- **Why this timing:** phase 2 writes `eventpg` against exactly these twenty
  sections. A section that asserts nothing certifies a PostgreSQL store on a
  clause it never checked, and the error is unrecoverable from the `eventpg`
  side because `eventpg` cannot see the defect inventory at all. Fixing it after
  `eventpg` ships means re-certifying a store that was already declared correct.
- **Close criteria:**
  - [ ] Every one of the twenty sections is named by at least one row in
        `defects.go:defects()`, and the row's store breaks a clause the section
        asserts rather than a call the section makes.
  - [ ] A test computes that coverage rather than asserting a remembered list:
        `for every section name, some defect names it` — with the shortened-
        inventory style control (`inventory_test.go:25-28`) proving the
        assertion can fail.
  - [ ] Re-run M4: gutting any one of the twenty sections turns
        `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` red, and the
        message names the section.
  - [ ] The finer-grained mutations M1, M2, M3, M12 and M13 each turn a test
        red; each message is pasted.
- **Status:** **closed** — round 1 of the disposition, 2026-09-07.
  Nine defects added, one for each section no row named — `truncatedKeys`
  (`stream identity`), `lastWriteWins` (`concurrency`), `cachedStreams`
  (`shared backing`), `partialLog` (`conservation`), `ownPlace`
  (`global paging`), `withheldTails` (`monotone visibility`),
  `uncertainWhenCancelled` (`cancellation`), `closesOnce` (`lifecycle`),
  `unclassifying` (`store failure classification`) — plus two for clauses the
  section's own row reached before: `renamedInTheLog`, whose log answers the run's
  own count under other stream keys (§INV-034's set equality, which `partialLog`
  never reaches because the count check fires first), and a second `transactions`
  row for `contended`'s conflict. **Twenty-nine rows, and every one of the twenty
  sections is named.** `TestEverySectionIsNamedByADefectThatBreaksIt` computes it
  with one control per section.
  **Mutation:** **all twenty** sections gutted in turn — the whole body replaced
  with `ctx := this.context(); _, repo, _ := this.open(); this.load(ctx, repo,
  this.account("a"))` — and every one turns
  `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` red naming the defect
  and the section, e.g. *"the suite no longer detects a store that truncates a
  stream key to the width of the column it keeps keys in: its stream identity
  section was reported \"passed\""*. The finer mutations:
  - **M2** (`winners != 1` and `len(written) != 1` both → `< 0`) → *"…reads the
    stream itself to decide the version it admits an append at: its concurrency
    section was reported \"passed\""*
  - **M3** (`conservation`'s two set-equality loops deleted) → *"…answers the log
    with a stream key other than the one the event was written to: its
    conservation section was reported \"passed\""*
  - **M12** (`contended`'s `ErrConflict` assertion and balance check, and
    `aftermath`'s `ErrConflict` assertion, all deleted) → *"…admits an append
    inside a transaction at a version another has already committed over: its
    transactions section was reported \"passed\""*
  - **M1 and M13 are argued rather than closed, below.**

### GAP-3 [critical][immediate] The anti-vacuity gate at the door can be deleted undetected, because its only test calls the pure helper instead of running a factory

- **Where:** `event/eventtest/suite.go:156-170` (`admit`), the gate at
  `:165-167`; `event/eventtest/suite_test.go:66-88`
  (`TestAClaimedCapabilityWithAMissingHookIsRefused`), which calls
  `eventtest.Missing(...)` (`export_test.go:85-87`) directly.
- **What:** M11. I deleted
  `if broken := missing(capabilities, factory); broken != "" { t.Fatal(...) }`
  from `admit` and the tree stayed green. The test asserts that the *pure
  function* `missing` returns a non-empty string for three inputs it constructs
  itself; it never runs a factory through `Run` or `Certify`, so it never
  observes that the door refuses anything. It is exactly `restrictions.md` §5's
  "tests assert on contracts and observable outcomes, not on internals" and the
  rubric's "asserting on a value the test itself computed": the four-row table is
  a test of a helper's return value, not of the admission rule.
- **Why this severity:** anti-vacuity rules 1 and 3 are two of the five the plan
  calls "the section's point". Rule 1 — a claimed capability with no hook fails
  the run — is what stops a store from claiming `Transactions: Supported` and
  then supplying no `Begin` so the `transactions` section is skipped. Rule 3 —
  an `Unstated` capability is refused — is §INV-043's own clause. With the gate
  gone, a store that claims transactions with no `Begin` hook reaches
  `transactionsSection`, which calls `this.factory.Begin(...)` at
  `sections_transactions.go:230` on a nil func and **panics**, taking the whole
  test binary down with a nil-pointer dereference rather than the stated fatal
  message. Nothing in the tree distinguishes those two outcomes.
- **Why this timing:** `Factory` is the public extension point phase 2
  implements against and this is its declared admission contract. A store author
  who supplies an incomplete `Factory` must get the sentence in
  `suite.go:186-189`, not a stack trace; nothing proves they do.
- **Close criteria:**
  - [ ] A test runs a real `Factory` — a store claiming `Transactions:
        Supported` with `Begin == nil`, and one with `Transactions: Unstated` —
        through the admission path and observes the refusal, rather than calling
        `Missing`.
  - [ ] Its control: the same factory with the hook supplied is admitted and
        reaches a verdict.
  - [ ] Deleting the `missing` call from `admit` turns that test red; the
        message is pasted.
  - [ ] Same treatment for `admit`'s other two fatals (`factory.New == nil`,
        `factory.New` answering `nil`) and for `runIdentity`'s
        `MaxKey`-too-small fatal at `suite.go:224-227` — all four are declared
        contract failures of the exported `Factory` and none has a test.
- **Status:** **closed** — round 1 of the disposition, 2026-09-07.
  Four of `run_test.go`'s cases drive a real `Factory` through `Run` and observe
  the door: a store claiming `Transactions` with `Begin == nil`, a store whose
  `Transactions` is `Unstated`, a factory with `New == nil`, and one whose `New`
  answers nil. Each asserts the child failed, that the message the door produced
  is in the output — computed by asking `eventtest.Missing` for it, or from the
  named constant the door reports — and that **no section was reported at all**,
  which is the difference between the stated fatal and the nil-`Begin` panic this
  finding describes. `runIdentity`'s `MaxKey` fatal is the fifth, driven with a
  store publishing `MaxKey` 20 against a requirement of 21. The control is the
  first case: the same fixture, complete, is admitted and reaches twenty
  verdicts.
  **Mutation:** each of the four door refusals deleted in turn turns exactly one
  case red. The `missing` gate (M11) reports both *"a run over a store that
  claims transactions and a factory that begins none never reported \"eventtest:
  this store claims Transactions and this factory supplies no Factory.Begin, so
  the transactions section cannot run — a claimed capability is never
  skipped\""* and *"…reported a verdict for a section, where it must be refused
  at the door with no section reported"*.

### GAP-4 [high][immediate] `TestEverySectionFailsAgainstAStoreThatRefusesEverything` is the claimed universal control and it discriminates only "the section makes at least one store call"

- **Where:** `event/eventtest/suite_test.go:274-287`;
  `event/eventtest/fixtures_test.go:575-624` (`refusingStore`).
- **What:** the plan calls this "anti-vacuity rule 4, **computed rather than
  remembered**: … a section that passes there is a section carrying no control,
  and the run names it". Measured, its discriminating power is one store call:
  every section begins with `this.open()` → `this.store()` → `this.load(...)`,
  and `refusingStore.ReadStream` answers `event.Failure(event.NotWritten, …)`,
  so `probe.load` refuses and the section is `failed` **before any assertion the
  section owns is reached**. M4 is the proof: nine sections with every assertion
  removed still fail against it. The test measures liveness, not control.
- **Why this severity:** this is the rule the plan leans on for the eleven
  sections the defect inventory does not name, and it is the reason GAP-2 was
  not caught by three rounds of implementation review. A control whose
  discriminating power is a constant is worse than no control, because it is
  reported as one.
- **Why this timing:** it is cited as evidence in `## Sections` S5 and in the
  checkpoint narrative. Leaving it as-is means the next reviewer reads the same
  false assurance.
- **Close criteria:**
  - [ ] The test's comment and the plan's `Anti-vacuity` paragraph state what it
        actually proves (every section reaches the store) rather than what it
        does not (every section carries a control).
  - [ ] A second, real control exists: a store that answers every call
        *successfully but wrongly* per section (GAP-2's per-section defect
        rows), so that "passes against a broken store" is what fails the run.
- **Status:** **closed** — round 1 of the disposition, 2026-09-07.
  Both halves. The test's own comment, the plan's § Anti-vacuity paragraph, the
  plan's test-table row and the failure message now say what it proves — every
  section reaches the store, which is liveness — and name the defect inventory as
  what proves control. The second, real control is GAP-2's twenty-section
  inventory: a store that answers every call successfully and wrongly, per
  section. No mutation of its own: M4 is the demonstration, and it is now red for
  all twenty.

### GAP-5 [high][immediate] The defect inventory's size is unpinned: seventeen of its eighteen rows can be deleted with no test going red

- **Where:** `event/eventtest/defects.go:36-73`;
  `event/eventtest/defects_test.go:14-40`, the only floor being
  `if len(named) == 0` at `:37`.
- **What:** M7. I replaced `defects()` with a one-row slice and
  `go test -count=1 ./event/...` was green.
  `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` iterates whatever the
  slice holds; it asserts each row's section fails, that no two rows share a
  name, and that every named section exists — none of which constrains the
  slice's contents. C8 is stated as "one defect count, one inventory, **computed
  by a test**", and what the test computes is a property of the rows that happen
  to be there.
- **Why this severity:** the inventory is the suite's only real mutation harness
  (GAP-2). A row silently dropped during a refactor takes a section's only
  guard with it and reports nothing. This is the same class of failure
  `inventory_test.go` was written to close for *sections* — and the inventory of
  defects got no equivalent.
- **Why this timing:** phase 2 will add `eventpg`-specific defects, and the
  first edit to `defects()` is when a row can go missing.
- **Close criteria:**
  - [ ] A test asserts the inventory's size, with a shortened-inventory control
        that must fail the same assertion (the `inventory_test.go:25-28`
        pattern).
  - [ ] A test asserts that every section name in `inventory()` appears in at
        least one defect row (this also closes GAP-2's measurement half).
  - [ ] Deleting one arbitrary defect row turns exactly one test red; the
        message names the row.
- **Status:** **closed** — round 1 of the disposition, 2026-09-07.
  `TestTheDefectInventoryIsTheSizeItSaysItIs` pins the count at 29 with the
  shortened-inventory control (`sized(defects[:len-1])` must fail the same
  assertion), and `TestEverySectionIsNamedByADefectThatBreaksIt` asserts every
  section name is in at least one row, with **twenty** controls — one per
  section, over an inventory with that section's rows removed.
  **Mutation:** dropping any row turns the size test red (*"the suite carries 28
  defects where its inventory is 29 rows"*); removing a section's rows turns the
  coverage test red naming it (*"no defect in this suite's inventory breaks the
  lifecycle section, so nothing here can tell whether that section still asserts
  anything"*). GAP-10's `switch` is closed in the same edit: `factoriesFor`
  reads a map from defect name to factory pair, so a thirtieth store-shaped row
  is a map entry and not a branch, and the `t.Fatalf` for a row with no builder
  is driven for every row on every run.

### GAP-6 [high][immediate] Two tests are named for rules they never observe, because they assert through `Certify`, which cannot fail

- **Where:** `event/eventtest/suite_test.go:22-40`
  (`TestARunThatCertifiedNothingFails`) and `:48-64`
  (`TestASectionThatNeverReturnedIsNotReportedPassed`).
- **What:** `TestARunThatCertifiedNothingFails` asserts
  `eventtest.Certified(gated) == 0`, that three verdicts came back, and that
  each is `"not certified"`. It never calls `Run` and never observes that a run
  certifying nothing *fails*. `TestASectionThatNeverReturnedIsNotReportedPassed`
  asserts `verdicts[0].Word != "passed"` and `Certified(verdicts) == 0` — again
  never observing that `Run` reports the row. Both names assert `Run`'s
  behaviour; both bodies assert the precondition of that behaviour. The plan's
  test table repeats the claim: `TestARunThatCertifiedNothingFails` "pins
  anti-vacuity rule 2 … and that is what `Run` fails on" — the "and that is what
  `Run` fails on" half is asserted by nothing (proved by M8).
- **Why this severity:** the section's own honesty rule. A reader — and three
  rounds of implementation review — takes the name as coverage. The names are
  the reason GAP-1 survived to phase 5.
- **Why this timing:** a test name is a contract with the next reader; renaming
  or re-pointing it is cheap now and is the difference between GAP-1 being
  found and being believed closed.
- **Close criteria:**
  - [ ] Each test either observes `Run`'s decision (see GAP-1) or is renamed to
        what it asserts (`TestAGatedSectionIsReportedNotCertified`,
        `TestTheVerdictOfASectionThatNeverReturnedIsNotPassed`).
  - [ ] The plan's S5 test table is corrected in the same edit.
- **Status:** **closed** — round 1 of the disposition, 2026-09-07.
  Renamed to what they assert:
  `TestARunThatCertifiedNothingFails` → **`TestAGatedSectionIsReportedNotCertified`**,
  `TestASectionThatNeverReturnedIsNotReportedPassed` → **`TestTheVerdictOfASectionThatNeverReturnedIsNotPassed`**,
  and — the same defect one row further, found while closing GAP-3 —
  `TestAClaimedCapabilityWithAMissingHookIsRefused` → **`TestAClaimWithNoHookAndACapabilityNobodyStatedAreBothNamed`**,
  because its body asks the rule and not the door. Each carries a comment naming
  `TestTheRunReportsWhatItFound` as where the rule itself is watched. The plan's
  S5 test table, its § Anti-vacuity paragraph, its § Contracts paragraph and the
  phase-5 checkpoint command are corrected in the same edit — the checkpoint's
  four-name `-list` clause now names `TestTheRunReportsWhatItFound` and still
  counts 4.

### GAP-7 [medium][immediate] The verdict table the plan publishes for `eventmemory` is asserted by nothing, and it silently changed under mutation

- **Where:** `event/eventmemory/conformance_test.go:14-24`; the claim in
  `## Sections` S5: "`eventmemory` reports eighteen *passed* and the two *not
  certified* rows §UC-044 predicts of it by name — `durability` … and `store
  failure classification`".
- **What:** both conformance tests are a bare `eventtest.Run(t, …)`. The plan's
  own checkpoint says "The `-v` output of the first `-run` is the section list,
  and it is the evidence: every section named, each *passed* or *not certified*,
  none skipped silently" — that evidence is a human reading `-v` output, not an
  assertion. Under M4 the run reported `store failure classification: passed`
  instead of `not certified — [outcome unconfirmed] cannot be produced by this
  store`, i.e. the published table changed, and nothing was red.
- **Why this severity:** UC-044 ("a store cannot exercise a capability") is
  matrix-proved by "`TestEverySectionInTheInventoryWasReported`, `store failure
  classification`" — the *third word* for `eventmemory` is the whole point of
  the use case and it is not asserted for the store the use case is about.
- **Why this timing:** `eventpg` will publish its own verdict table and will
  copy this shape.
- **Close criteria:**
  - [ ] `event/eventmemory` asserts its expected verdict per section by name —
        eighteen `passed`, `durability` and `store failure classification`
        `not certified` — with the reason string matched by sentinel or by the
        capability that produced it, not by substring.
  - [ ] Flipping one expected word turns the test red.
- **Status:** open — **timing argued down to `[deferred]`**, round 1 of the
  disposition, 2026-09-07, and carried in the plan's `## Debt`. The finding is
  right; both ways of closing it today are worse than the hole. Asserting the
  verdict per section from `eventmemory` needs `Run` to **answer** its verdicts,
  which is a public contract change in the one phase whose contract is otherwise
  frozen, and the place to decide that is where phase 2 first runs the suite and
  publishes a table of its own. The alternative — parsing the `-v` output of a
  child run, as `TestTheRunReportsWhatItFound` now does for the exit status —
  makes a second package depend on `verdict.line()`'s rendering, matched by
  substring, which this finding's own close criteria refuse. What is uncovered
  meanwhile is bounded: the twenty verdicts *are* asserted for the staging
  fixture by `TestEverySectionInTheInventoryWasReported`, and the two words
  `eventmemory` answers come from `needsPersistence` and from its `Fail` hook,
  both of which are driven elsewhere.

### GAP-8 [medium][deferred] The recorded-instant defect is never exercised at the global read door: `unrecorded.ReadAll` is at 0.0 % coverage

- **Where:** `event/eventtest/defects.go:295-298` (`unrecorded.ReadAll`, the only
  0.0 % function in the package); the clause at
  `event/eventtest/sections_write.go:238-257` (`probe.instants`), which reads
  only through `this.drain(...)` → `ReadStream`.
- **What:** GAP-24 of the implementation review added the recorded-instant
  clause and a decorator that blanks the field at both read doors. The section
  reads only the stream door, so the `ReadAll` half of the decorator is dead
  code and the clause is unproved for the global read. A store whose
  `ReadStream` select list keeps `recorded_at` and whose `ReadAll` select list
  drops it — two different queries in any real store, and exactly the migration
  shape GAP-24 describes — is certified.
- **Why this severity:** it is one arm of §INV-009's third clause, with no
  contract impact on the kernel; the section that owns it exists and asserts the
  other arm.
- **Why this timing:** deferred — closing it is one assertion inside an existing
  section and it changes no contract. It must be closed before `eventpg`, whose
  two read paths are genuinely two SQL statements.
- **Close criteria:**
  - [ ] `probe.instants` (or `global order` / `conservation`) asserts the same
        two properties over a `ReadAll` page of the run's own events.
  - [ ] `go tool cover -func` shows `unrecorded.ReadAll` above 0 %.
  - [ ] Removing that assertion reports the sixteenth defect *passed*.
- **Status:** open — `[deferred]`, carried to the plan's `## Debt`.

### GAP-9 [medium][deferred] Five declared failure paths of the exported `Factory` contract have no test

- **Where:** `event/eventtest/suite.go:157-162` (`factory.New == nil`, `New`
  answering `nil`), `:224-227` (`MaxKey` with no room — the fatal that names the
  suite's own requirement), `probe.go:73-75` (`this.store()` answering nil),
  `export_test.go:60-62` (`chosen`'s "names no section" fatal); `Factory.Window`
  (`suite.go:88`) is never exercised at a non-default value.
- **What:** each is a declared, message-bearing failure of the public extension
  point and none is driven. `Factory.Window` in particular exists so that a
  store whose operations are a network away is not reported failed for a wait —
  the store phase 2 writes is exactly that store, and no test proves the field
  is read (`probe.context()` at `probe.go:50-58` is only ever entered with
  `Window == 0`).
- **Why this severity:** edge coverage of a public contract with no wrong-answer
  risk to the kernel; the messages are the DX a store author meets first.
- **Why this timing:** deferred — additive tests, no contract change. `Window`
  should be closed before `eventpg` sets one.
- **Close criteria:**
  - [ ] A table test drives each of the five and asserts the message identifies
        the cause (matched by the requirement, not by substring of prose).
  - [ ] A factory with `Window: 50*time.Millisecond` is observed to set that
        deadline on the store's calls (the `deadlines` decorator at
        `fixtures_test.go:529-573` already measures the deadline; assert its
        value).
- **Status:** partly closed, remainder `[deferred]` and carried to the plan's
  `## Debt`. Three of the five are now driven by `TestTheRunReportsWhatItFound`:
  `factory.New == nil`, `New` answering nil, and `runIdentity`'s `MaxKey` fatal,
  each asserted by the message the door reports and by the absence of any section
  verdict. Left: `probe.store()` answering nil mid-section, `chosen`'s "names no
  section" fatal, and `Factory.Window` at a non-default value — the last being
  the one phase 2 needs.

### GAP-10 [medium][deferred] `factoriesFor` dispatches on the defect's name string, so a second factory-shaped defect needs a new `case`

- **Where:** `event/eventtest/defects_test.go:54-71`;
  `event/eventtest/export_test.go:16-20` (`Defect`, whose only variation point
  is `Over func(event.Store) event.Store`).
- **What:** four of eighteen defects are stores rather than decorators, and the
  test maps them to fixtures by matching the defect's human-readable **name**
  against four string literals duplicated from `defects.go`. `universality.md`'s
  second-instance test: a fifth store-shaped defect works only after a new
  `case` is written, and a renamed row falls through to `t.Fatalf` — loud, but
  the mechanism is a per-instance branch rather than data. The `defect` struct
  could carry `factories func(*testing.T) (plain, broken Factory)` and the
  switch would disappear.
- **Why this severity:** it fails loudly rather than silently, and the
  enumeration is internal to a test file that no other package can reach
  (`Defects()` lives in `export_test.go`), so no external contract bends around
  it. That is what keeps it below `high`.
- **Why this timing:** deferred — internal to the suite's own tests, no contract
  impact. It should be closed in the same edit as GAP-5, which touches the same
  inventory.
- **Close criteria:**
  - [ ] `defect` expresses a factory-shaped row directly; `factoriesFor`'s
        `switch` is gone.
  - [ ] Adding a nineteenth defect of either shape requires editing only
        `defects()` and the fixture it names.
- **Status:** **closed** — round 1 of the disposition, 2026-09-07, in the same
  edit as GAP-5. `factoriesFor` reads `storeShaped()`, a map from the defect's
  name to the pair of factories it is built from, so the four store-shaped rows
  are data and a fifth needs a map entry rather than a `case`. The `t.Fatalf` for
  a row with no builder stays and is exercised for every row of the inventory on
  every run, so a renamed row still fails loudly. The `defect` struct is
  unchanged: the factories live in the third package where the fixtures are, and
  `defects.go` ships in the suite's own package, so a row cannot carry them.

### GAP-11 [medium][deferred] `fixtures_test.go` is 865 lines, more than twice `architecture.md`'s threshold, and is not in the plan's metrics table

- **Where:** `event/eventtest/fixtures_test.go` (865 lines); the plan's
  `## Architecture metrics` table, which counts every non-test file in
  `event/eventtest` and no test file.
- **What:** `architecture.md` sets ≤ 400 lines per file and requires a breach to
  be justified in writing in the plan. The file holds seven distinct
  responsibilities — the `held` log, the `sliceStore`, the `stagingStore` and its
  transaction, the `deadlines` watcher, the `refusingStore`, the `fixtures`
  wrap/unwrap registry, and eight factory builders — which is the "one file
  because the original was long" shape the same document bans. `wrappers_test.go`
  (113) already carved out one slice of it in round 3; the rest did not follow.
  Related and in the same file: `sliceFactory(admitsAnything bool, …)`,
  `stagingFactory(leaks bool, …)`, `lenientFactory(lenient bool)` and
  `persistentFactory(forgets bool)` are flag parameters that change what the
  function means — `architecture.md`'s hard-ban list.
- **Why this severity:** a measurable threshold breached without written
  justification is `high` by `architecture.md`'s own rule; graded `medium` here
  because it is test-only and no shipped contract turns on it. Note it as an
  unjustified breach either way.
- **Why this timing:** deferred — a split changes no behaviour, and it will
  churn again when GAP-2's per-section defect fixtures land, so it should follow
  them.
- **Close criteria:**
  - [ ] Either the file is split along the responsibilities above, or the plan's
        metrics table carries the breach with a written justification, as it
        does for `probe`'s lines-per-class.
  - [ ] The four boolean flag parameters become named constructors or a small
        options value.
- **Status:** open — `[deferred]`, carried to the plan's `## Debt`. Unchanged at
  865 lines: this round added its stores to `defects_*.go` and `wrappers_test.go`
  rather than to it.

### GAP-12 [low][deferred] The fixture's cursor minter is a wall clock, so two stores built in the same nanosecond make a "foreign" cursor native

- **Where:** `event/eventtest/fixtures_test.go:75-77` (`newHeld`, minter =
  `strconv.FormatInt(time.Now().UnixNano(), 36)`), read at `:131-156`
  (`held.read` compares `minter != this.minter` to answer `errCursorForeign`)
  and consumed by `probe.foreignCursor` (`sections_resumption.go:65-78`), which
  requires `ErrCursor`.
- **What:** the fixture's notion of "another backing" is a timestamp. Two
  `newHeld()` calls landing on one nanosecond produce two stores whose cursors
  parse as each other's, `foreignCursor` gets a page instead of `ErrCursor`, and
  `resumption` reports the store failed for the fixture's clock.
  `restrictions.md` §3: clocks are injected, never called ad hoc.
- **Why this severity:** measured on this host at **0 collisions in 2 000 000
  consecutive `newHeld()` pairs**, and no flake in five `-race -count=2
  -shuffle=on` runs, so it is latent rather than live. A coarser clock (a
  container with a low-resolution timer, a different GOOS) makes it live, and
  the failure it produces accuses the store.
- **Why this timing:** deferred — fixture-internal, no contract impact.
- **Close criteria:**
  - [ ] The minter is a process counter (`atomic.Uint64`) or `crypto/rand`, not
        a clock.
  - [ ] `held.end()`, `cursor()` and `read()` still round-trip.
- **Status:** open — `[deferred]`, carried to the plan's `## Debt`.

### GAP-13 [low][deferred] §INV-019's negative half is a manual act, where the repository already has an automated pattern for it

- **Where:** `event/eventtest/suite_test.go:11-16`
  (`TestATrivialStoreNeedsNoInternalAccess`); the plan's checkpoint text: "must
  state that the unexport-one-field demonstration was performed and reversed; a
  compile-only test that was never made to fail proves that a package compiles";
  the existing pattern at `event/testdata/crossings` +
  `event/crossings_test.go` (`TestTheCrossingsThatMustNotCompile`).
- **What:** the compile-time proof is real — `fixtures_test.go` builds a
  complete store in a third package from the exported vocabulary alone — but the
  *demonstration that it can fail* (unexport `Envelope.RecordedAt`, watch
  `eventtest_test` stop compiling) is a one-off human act recorded in prose. The
  repository already runs negative-compile assertions as ordinary tests.
- **Why this severity:** the positive half genuinely holds and is checked on
  every `go test`; only the falsification is manual.
- **Why this timing:** deferred — additive, no contract change.
- **Close criteria:**
  - [ ] A `testdata`-style negative-compile case asserts that a store needing
        `event`-internal access does not build, run as an ordinary test.
- **Status:** open — `[deferred]`, carried to the plan's `## Debt`.

### GAP-14 [low][deferred] Plan text drift inside the S5 section: fifteen defects vs eighteen, six test files vs eight

- **Where:** `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md`, S5: "all fifteen
  defects are detected" and "the defect inventory **is** a fifteen-mutation
  suite that runs on every `make unit`" against `Carried gaps`' "eighteen
  defects after round 3" and `defects.go`'s eighteen rows; "Fourteen files of
  suite and six of test" against fourteen suite files and **eight** test files
  (`defects_test`, `export_test`, `fixtures_test`, `fuzz_test`, `inventory_test`,
  `proxies_test`, `suite_test`, `wrappers_test`).
- **What:** the round-1 narrative was not updated when rounds 2 and 3 added
  rows and files. The counts are the section's own evidence that the harness is
  as large as claimed.
- **Why this severity:** prose only; the code and the tests agree with each
  other.
- **Why this timing:** deferred — a doc edit, no behaviour.
- **Close criteria:**
  - [ ] The two counts in the S5 narrative match `defects()` and the file list.
- **Status:** **closed** — round 1 of the disposition, 2026-09-07. The S5
  narrative is recounted rather than annotated: twenty-nine defects, nineteen
  files of suite and nine of test, and the two mutation-suite sentences say
  twenty-nine. The counts were taken with `wc -l` and `ls` at the time of
  writing, not remembered.

---

### What the suite gets right, so a later round does not re-litigate it

- The kernel and store halves of the mutation matrix are genuinely covered:
  M5 (kernel page-length check) and M6 (`eventmemory` payload hand-off) both
  turned tests red with messages that name the defect and the section, and M5's
  message walked the whole chain from a kernel branch to the defect inventory.
- `TestEachProxyReportsTheThingItExistsToFind` (`proxies_test.go:83-134`) is the
  model the rest of the section should follow: nine reporting branches driven,
  three silent controls beside them, `roundTrips` / `keysRender` /
  `familiesDiffer` at 100 %.
- `FuzzKeysReportsACollisionExactlyWhenTwoIdentitiesRenderOneKey` is a true
  equivalence rather than an implication, seeded with the separator, the empty
  part, a NUL, a backslash, a 4096-byte part and NFC/NFD `café`. It is the one
  place in S5 where the property is quantified over caller data.
- `TestEverySectionInTheInventoryWasReported` has a real, working control (a run
  over an inventory one section shorter must fail the same assertion), and
  `TestACursorAStoreCannotParseIsRefusedRatherThanReadFromTheBeginning` was the
  only test to catch a `probe`-level mutation (M10).
- Universality of the aggregate: the suite declares its own `ledger` /
  `accountID` and the proxy tests use a structurally different `inventory` /
  `warehouseID` — the second-instance check passes. Every count a section writes
  is derived from `Limits()`, and `TestEverySectionIsCertifiedAtTheNarrowestLimitsAStoreMayPublish`
  plus `TestTheMemoryStoreSatisfiesTheContractAtNarrowerLimits` are real
  measurements against a second set of legal numbers.
- Determinism and isolation: no `t.Parallel`, no sleeps, no network, no real
  clock in an assertion, the one `t.SkipNow` is a documented simulation of
  `runtime.Goexit`, no leftover fixtures, no fuzz corpus written. Fast:
  1.79 s wall for the three packages under `-race`.

---

## Round 1 of the disposition — 2026-09-07

Six findings closed (GAP-1, GAP-2, GAP-3 `[critical]`; GAP-4, GAP-5, GAP-6
`[high]`), two more closed on the way (GAP-10, GAP-14), one partly (GAP-9), one
argued down in timing (GAP-7), and five carried to the plan's `## Debt` (GAP-7,
GAP-8, GAP-9's remainder, GAP-11, GAP-12, GAP-13). Every mutation below was
applied to the tree, run, read and reverted; the tree afterwards is
`go test -race -count=2 ./event/...` green, `make unit` green, `gofmt -l .`
silent.

### The two clauses no defect can guard, argued rather than closed

**M1 and M13 are the kernel's obligations, not a store's.** `cancellation`'s
second window binds the suite's own `unconfirming` decorator over the store under
test and asserts that what comes back is `ErrUncertain` and does not match a
cancellation; `refusal classes`' five-row policy table binds the suite's own
`policing` decorator and asserts that a decorator's refusal reaches the caller as
its own sentinel with its own text. **In both, the value that produces the
failure is the suite's**, so no store under test can break either clause and no
row of the defect inventory can be written for one. A section-level defect
guarding them is not merely missing — it is not expressible.

Both clauses are live rather than dead, and both restate a kernel obligation the
kernel's own suite already pins. Demonstrated:

| Kernel mutation | `event/eventtest`'s clause | `event`'s own suite |
|---|---|---|
| `case Unconfirmed: return newRefusal(ErrUncertain, nil, this.cause)` → `return this.cause` | `cancellation` **failed** — *"an append cancelled after it was issued answered context canceled, and uncertainty outranks a cancellation because reporting an uncertain commit as a cancellation cannot be recovered"* | `TestAnOutcomeOutsideTheVocabularyNormalises` red, two subtests |
| `case Unconfirmed:` → `newRefusal(ErrUncertain, this.cause, this.cause)` | not caught — the leak is through `errors.As` and the suite asks `errors.Is` | `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` red |

`refusal_test.go`'s `TestAContextCauseNeverTravelsThroughARefusal` and
`TestTheRefusalVocabularyIsAPartition` pin the `Refused` mapping and the
cause-carrying rules the policy table restates, the same way. The clauses stay
in the suite because they are the sentences a **store author** reads about what
the kernel will do with what they classify; what they are not is a control over
the store, and this file is where that is written down.

### The residue, named rather than claimed

- The nine sections GAP-2 named are guarded by one defect each. A section may
  still have a *clause* the section's own row reaches after — the row fails the
  section, so the section keeps its control, but that particular clause could be
  deleted silently. Two were found and closed with a second row
  (`conservation`'s set equality, `transactions`' `contended`); the systematic
  version of this question is per-clause mutation testing, which is not what a
  defect inventory is.
- `event/eventtest` runs in **2.5 s** under `-race`, up from 1.45 s: the nine
  child processes `TestTheRunReportsWhatItFound` spawns cost 1.07 s of it. No
  test in the package sleeps, and the children are `os.Args[0]` — no toolchain,
  no network, no temporary files.

---

## Round 2 — econv-test-reviewer (clean context) — 2026-09-07

**Baseline.** `go build ./...`, `go vet ./event/...`, `gofmt -l event` all clean.
`go test -race -count=1 ./event/...` green: `event` 1.265 s, `event/eventmemory`
1.053 s, `event/eventtest` 2.516 s — 2.86 s wall for the three; without `-race`
the same three are 0.13 s, so the section's unit suite is fast and touches no
database, no network and no toolchain. `-race -count=3` green (5.85 s). Three
`-count=1 -shuffle=on` runs green — no order dependence. Both phase-5 checkpoint
`-list` count clauses hold (1 and 4). Statement coverage of `event/eventtest` by
its own tests: **87.3 %** (was 86.3 %); `event/eventmemory` 98.2 %. Plan counts
re-checked and correct: 19 suite files, 9 test files, 29 defect rows, 23 top-level
tests in `eventtest` + 2 in `eventmemory` + 1 fuzz target.

**Every mutation below was applied to a snapshot-verified tree and reverted.** A
`sha256sum -c` over all 95 `event/**.go` files reports zero differences, `git
status --porcelain event/` is byte-identical to the state at the start of this
round, and `go build ./... && go vet ./event/... && gofmt -l event && go test
-race -count=1 ./event/...` is green afterwards.

### Mutation log — part 1: re-verifying round 1's six closures

| # | What was broken | Where | Result |
|---|---|---|---|
| R1 | Deleted `Run`'s per-section `t.Error(given.reason)` | `suite.go:109-111` | **CAUGHT** — `run_test.go:41` *"a run over a store that fails one section left the test it was handed green, where a store like this must leave it failed"* |
| R2 | Deleted `Run`'s `unreported` loop | `suite.go:113-117` | **CAUGHT** — *"a run over a section whose factory hook leaves the run … never reported \"…the transactions section reported no verdict at all…\""* |
| R3 | Deleted `Run`'s `certified(verdicts) == 0` branch | `suite.go:118-120` | **CAUGHT** — *"a run over a store that refuses every operation never reported \"eventtest: this run certified nothing — …\""* |
| R4 | Deleted the `missing()` gate from `admit` | `suite.go:180-182` | **CAUGHT** — two cases red, naming both the claimed-hook and the `Unstated` refusals |
| R5 | Gutted **each of the twenty sections in turn** — whole body replaced with `ctx := this.context(); _, repo, _ := this.open(); this.load(ctx, repo, this.account("a"))` (four needed one unused import blanked) | all six `sections_*.go` | **CAUGHT, 20/20** — `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` red every time, naming the defect and the section |
| R6 | Kernel: `case Refused: newRefusal(ErrRefused, …)` → `newRefusal(ErrUncertain, nil, this.cause)` | `event/errors.go:237-238` | **CAUGHT** — 8 tests in `event`'s own suite red, plus both `eventmemory` conformance runs |

**GAP-1, GAP-2, GAP-3, GAP-4, GAP-5, GAP-6 and GAP-10 of round 1 are confirmed
closed by mutation, not by claim.** R6 also confirms the round-1 disposition's
argument for M1 and M13: the kernel obligations that `cancellation`'s second
window and `refusal classes`' policy table restate *are* pinned by `event`'s own
S1 suite, so those two clauses are redundant rather than the sole proof.

### Mutation log — part 2: the clause-level campaign (new)

Round 1's disposition named the residue it left — *"a section may still have a
clause the section's own row reaches after … the systematic version of this
question is per-clause mutation testing"* — and this round ran it. Every one of
the **165** `this.refuse(…)` calls in `sections_*.go` was neutralised **one at a
time** into a silent abort (`panic(abort{})` with no `broke` recorded, so the
verdict machinery is untouched and the section reports `passed` when that clause
would have fired), and the whole `event/eventtest` suite was run against each.

The mutation is **conservative**: a silent abort stops the section at that point,
so a defect the section would have caught *later* is also hidden — which makes a
mutation *more* likely to be reported caught than a plain deletion of the
assertion would. The survivor set below is therefore a lower bound on the real
one.

| Section (clauses in its own body and its own helpers) | Load-bearing / total |
|---|---|
| `binding` | **0 / 11** |
| `bounds` | **0 / 6** |
| `global order` (excluding the shared `ascending`) | **0 / 4** |
| `transactions` | 2 / 30 |
| `expected version` | 1 / 10 |
| `lifecycle` (incl. `beside`, `closedWithin`) | 1 / 9 |
| `refusal classes` (incl. `wroteWhatItSaid`, `partitioned`) | 1 / 9 |
| `stream identity` | 1 / 8 |
| `cancellation` | 1 / 6 |
| `payload ownership` (incl. `spilled`, `inbound`, `aliased`) | 4 / 22 |
| `resumption` (incl. `foreignCursor`, `unparsableCursor`) | 2 / 11 |
| `dense versions` (incl. `instants`) | 2 / 8 |
| `store failure classification` (incl. `classified`) | 1 / 5 |
| `shared backing` | 2 / 5 |
| `global paging` | 1 / 4 |
| `conservation` | 2 / 4 |
| `concurrency` | 2 / 4 |
| `stream paging` | 1 / 2 |
| `durability`, `monotone visibility`, shared `ascending` | 1 / 1 each |
| **Total** | **27 / 165 — 138 survived** |

Eight assertion clauses in `probe.go` (the ones that assert rather than forward a
store's own error) were driven the same way:

| # | Clause | Result |
|---|---|---|
| P1 | `probe.store` — a later store publishing other `Limits` | `probe.go:79` **CAUGHT** |
| P2 | `probe.store` — a later store claiming other `Capabilities` | `probe.go:82` **CAUGHT** |
| P3–P5 | `probe.unchanged` — capabilities / limits / backing constancy | `probe.go:106,109,112` **CAUGHT** |
| P6 | `probe.store` — the factory answered no store | `probe.go:76` **SURVIVED** |
| P7 | `probe.drain` — a page longer than the one this store publishes | `probe.go:201` **SURVIVED** |
| P8 | `probe.advanced` — a page beside the cursor it was read from | `probe.go:275` **SURVIVED** |

**141 of the 173 assertions the shipped conformance suite makes can be silently
disabled with the whole tree green.**

### Mutation log — part 3: §INV-019's negative half

`event/store.go:101` `RecordedAt` → `recordedAt`: `event/eventtest` (the shipped
package) fails first at `sections_write.go:244` and `defects_write.go:76`,
`event/eventtest_test` fails at `fixtures_test.go:127/321/755`, and
`event/eventmemory` fails at `append.go:55`. Restored; build green. The claim
holds — with the wrinkle recorded as GAP-19 below.

---

### GAP-15 [critical][immediate] 138 of the suite's 165 assertions can be deleted with the whole tree green — the defect inventory guards one clause per section, not the section

- **Where:** `event/eventtest/defects.go:34-95` (the inventory);
  `event/eventtest/defects_test.go:15-41`
  (`TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect`), `:71-88`
  (`TestEverySectionIsNamedByADefectThatBreaksIt`); the 138 surviving clauses are
  spread across all six `sections_*.go` and `probe.go:76,201,275`.
- **What:** the campaign above. `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect`
  asks one question per defect row — *does the section named for this row report
  `failed`* — so a row certifies exactly the **one** clause that happens to fire
  first for that row's store. Every other clause in the same section is
  unobserved. Measured detection rate: **27 / 165 = 16 %**. The nine sections
  round 1's GAP-2 rescued are rescued at that granularity and no finer:
  `lifecycle` is 1/9, `cancellation` 1/6, `concurrency` 2/4, `conservation` 2/4,
  `stream identity` 1/8.
- **Why this severity:** this is the rubric's own `[critical]` trigger — a suite
  that stays green over a gutted implementation — and it is the *same* failure
  round 1 graded `[critical]` at section granularity, one level down. The bug
  that ships undetected is not hypothetical: the artefact is what phase 2's
  `eventpg` runs **verbatim**, and phase 2 is the first change that will edit
  these sections (a store a network away, `Factory.Window`, `Factory.Tail`, GAP-8's
  `ReadAll` instant). Any weakening made there — an `errors.Is` narrowed, a
  balance check widened, a loop bound relaxed — turns exactly nothing red, and the
  first evidence is a PostgreSQL store certified on a property nobody checked.
  The suite's own `defects_test.go:10-14` states the failure mode it exists to
  prevent — *"one that tests nothing passes everything, and nobody notices until
  a store is wrong in production"* — and 84 % of it is in that state.
- **Why this timing:** it blocks a correct S6 and a correct phase 2. S6 writes
  the ADRs and the docs that publish this suite as the store contract's
  enforcement; publishing it with a measured 16 % assertion coverage means the
  next store author reads a guarantee that is not there. Closing it after
  `eventpg` ships means re-certifying a store that was already declared correct.
- **Close criteria:**
  - [ ] A clause-level mutation harness exists and is **run and recorded**, not
        remembered: the survivor list is in the plan with a count, the way the
        defect inventory's size is.
  - [ ] For every row of `## Coverage matrix` whose **Proved by** column names an
        S5 conformance section and names no other test, at least one clause
        asserting that row is demonstrated load-bearing — the clause is disabled,
        a named test turns red, the message is pasted. GAP-17 lists the rows this
        is currently false for.
  - [ ] The three zero-clause sections of GAP-16 reach a non-zero count.
  - [ ] The remaining survivors that are **not** covered by the two rules above
        are enumerated in `## Debt` with the argument for each — as round 1 did
        for M1 and M13, whose kernel backstop R6 re-confirms — rather than left
        uncounted.
- **Status:** open

### GAP-16 [high][immediate] `bounds`, `binding` and `global order` satisfy anti-vacuity rule 5 with a defect that breaks a *call* the section makes, not a *clause* it asserts — which is what GAP-2's own close criterion forbade

- **Where:** `event/eventtest/sections_read.go:204-243` (`boundsSection`, clauses
  at `:213`, `:221`, `:225`, `:230`, `:238`, `:241`);
  `event/eventtest/sections_write.go:12-61` (`bindingSection`, clauses at `:19`,
  `:26`, `:29`, `:33`, `:36`, `:41`, `:44`, `:47`, `:52`, `:55`, `:59`);
  `event/eventtest/sections_read.go:13-59` (`globalOrderSection` `:22`, `:37` and
  `subsequence` `:51`, `:57`); the rows that name them,
  `event/eventtest/defects.go:36-39` (`illegalProduct`, `drifting`), `:45-46`
  (`reusedPositions`), `:62-63` (`publishesAShorterPage`).
- **What:** round 1's GAP-2 close criterion 1 reads *"…is named by at least one
  row in `defects.go:defects()`, **and the row's store breaks a clause the section
  asserts rather than a call the section makes**"*. For these three it does not.
  `publishesAShorterPage` relimits `StreamPage` to 1, and the kernel's own page
  check inside `this.load` refuses — every clause `boundsSection` writes survives
  neutralisation. `illegalProduct` is refused inside `probe.bind`; `drifting` is
  caught by `probe.unchanged` (`probe.go:106-112`), a probe helper, so all eleven
  clauses in `bindingSection`'s own text survive. `reusedPositions` is caught by
  the shared `ascending` helper, so the position-stability loop (`:27-40`) and
  `subsequence` (`:47-59`) both survive.
- **Why this severity:** `bounds` is the S5 proof the matrix cites for **UC-025**
  and **C4/GAP-103**; `binding` for **UC-006**, **UC-059**, **INV-039** and
  **INV-043**; `global order` for **UC-038** and half of **INV-009**. Concretely:
  delete `boundsSection:221` — the assertion that a batch of `MaxBatch + 1`
  answers `ErrTooLarge` — and nothing in the tree notices; a store whose batch
  bound is never enforced is then certified for UC-025 by a section whose only
  live check is that a `Load` succeeds. Same for `bindingSection:44`, the
  `ErrFamily` arm, which is UC-059's and INV-039's only S5 clause.
- **Why this timing:** the criterion was written into round 1's close list and is
  cited in the plan (`§ Anti-vacuity`, rule 5) and in the coverage matrix preamble
  as what *"makes citing a section as proof mean something"*. It is a public claim
  about the artefact phase 2 depends on, and it is false for three of twenty.
- **Close criteria:**
  - [ ] Each of the three gains a defect row whose store answers every call
        successfully and **wrongly** in a way one of that section's own clauses —
        not `probe.load`, `probe.bind` or `probe.store` — is what catches: e.g. a
        store that admits a batch over its published `MaxBatch`; a binding that
        admits a second aggregate over one family; a store whose per-stream order
        in the log is not a subsequence of that stream's own read.
  - [ ] Each is demonstrated: the clause is neutralised, the new row's test turns
        red, the message is pasted; and the row is asserted `passed` on the store
        without the defect.
  - [ ] The three counts in GAP-15's table are re-measured and are non-zero.
- **Status:** open

### GAP-17 [high][immediate] Three matrix rows have no load-bearing proof anywhere in the tree: UC-066, UC-061 and UC-038's subsequence clause

- **Where:**
  - UC-066 (*a transaction open and nothing bound*) —
    `event/eventtest/sections_transactions.go:182-192`, the single clause at
    `:190`. Matrix **Proved by:** `transactions` and nothing else;
    `event/eventmemory/transaction_test.go` has no test of an append issued while
    an unbound transaction is open.
  - UC-061 (*the caller mutates a handed-out state*) —
    `event/eventtest/sections_ownership.go:177-193` (`aliased`), clauses at `:184`
    and `:191`. Matrix **Proved by:** `payload ownership`.
    `event/fold_test.go:284:TestAReferenceKindStateFoldsWithoutAliasing` is S2's
    and folds with no store, so it cannot see a state field that points into a
    store's page.
  - UC-038 (*per-stream order is a subsequence of global order*) —
    `event/eventtest/sections_read.go:47-59` (`subsequence`), clauses at `:51`
    and `:57`. Matrix **Proved by:** `global order`, `conservation`;
    `conservation` proves set equality, not order. `grep -rn subsequence` over
    `event/` outside `eventtest` returns nothing.
- **What:** all five clauses survive neutralisation. Deleting them leaves the
  tree green, so `restrictions.md` §5's *missing test for a stated invariant*
  applies: the item has a **section**, but nothing fails when the section stops
  asserting it — which round 1 already established is the same thing.
- **Why this severity:** each is the whole of one matrix row. The bug that ships:
  a store that folds a rolled-back unbound transaction's autocommitted writes
  away (UC-066); a store whose codec aliases and whose `Load` hands the caller a
  `[][]byte` pointing into the page it still serves, so one request's mutation
  changes the next request's state (UC-061 — the exact §INV-021 shape the section
  exists for); a store whose log interleaves one stream against its own read, so a
  projector folding from the log reaches a different state than `Load` does
  (UC-038). None is detectable by anything else in the tree.
- **Why this timing:** these are the rows the matrix's third column was added to
  make enforceable, and S6 copies that column into `docs/ai/flows/FL-036` where
  `scripts/docs_test.go` checks the cited names exist — a citation that exists and
  proves nothing survives that check. Fix before the citation is published.
- **Close criteria:**
  - [ ] One defect row per item, whose store breaks that clause and passes every
        other clause of the same section, so the row's failure is that clause's.
  - [ ] Each demonstrated by neutralising the clause and reading the message.
  - [ ] `TestTheDefectInventoryIsTheSizeItSaysItIs`' count updated in the same
        edit, and `TestEverySectionIsNamedByADefectThatBreaksIt` still green.
- **Status:** open

### GAP-18 [medium][immediate] Two of the suite's own cross-section assertions — the over-long page and the resume-in-place cursor — are guarded by nothing

- **Where:** `event/eventtest/probe.go:192-207` (`drain`), the clause at `:201`;
  `event/eventtest/probe.go:271-275` (`advanced`), the clause at `:275`.
- **What:** P7 and P8 of the campaign. `drain:201` is the suite's own copy of the
  page-length obligation — the kernel's copy in `Repo.checkPage` is guarded
  (round 1's M5, and the `bounds` row still names it), but the suite's own is
  reached by every section that drains a stream and can be removed silently.
  `advanced:275` is the only thing between a store that answers a page beside the
  cursor it was read from and a consumer that resumes at the page it has already
  read, forever; the `ownPlace` row for `global paging` does not reach it.
- **Why this severity:** both are shared helpers, so one deletion weakens twelve
  sections at once rather than one, and neither has a contract impact on the
  kernel — the kernel keeps its own page check. `advanced` is the sharper of the
  two: an infinite resume loop is a production outage a green conformance run
  would have promised against.
- **Why this timing:** immediate rather than deferred only because it is one
  defect row each and it is the same edit as GAP-16 and GAP-17. If those slip,
  this one may travel with them.
- **Close criteria:**
  - [ ] A defect row whose store answers a page longer than the `StreamPage` it
        publishes **without** the kernel refusing first (i.e. at the `drain` door
        of a section that reads raw), and one whose `ReadAll` answers a non-empty
        page with the cursor it was given.
  - [ ] Each demonstrated by neutralising the clause and reading the message.
- **Status:** open

### GAP-19 [low][deferred] The recorded §INV-019 demonstration no longer isolates the third-package claim, and its citation has moved

- **Where:** the plan's S5 `§ Verified by mutation` paragraph — *"`Envelope.RecordedAt`
  unexported: `event/eventtest_test` fails to compile at `fixtures_test.go:268`"*;
  the actual references are now `event/eventtest/fixtures_test.go:127`, `:321`,
  `:755` in the third package, **and** `event/eventtest/sections_write.go:244,253`
  and `defects_write.go:76` in the shipped `eventtest` package itself.
- **What:** re-run this round (part 3). The compile does break, so the claim is
  true, but the first errors are now in a **first-party** package, not in the
  third one, so the demonstration as written no longer shows what §INV-019 says —
  that a store built by a stranger out of the exported vocabulary alone needs no
  `event`-internal access. GAP-24's `probe.instants` clause added the first-party
  references after the demonstration was recorded.
- **Why this severity:** prose accuracy about a demonstration that still holds;
  the positive half runs on every `go test`.
- **Why this timing:** deferred, and it should be closed together with round 1's
  GAP-13, which asks for the negative half to become an ordinary
  `event/testdata/crossings`-style test rather than a recorded human act — at
  which point the citation stops being prose.
- **Close criteria:**
  - [ ] The plan's sentence names a current line in `fixtures_test.go` and says
        that `eventtest`'s own files reference the field too, or the demonstration
        is replaced by GAP-13's automated one.
- **Status:** open

---

### Carried from round 1, re-verified still open this round

- **GAP-7** `[medium][deferred]` — both `event/eventmemory` conformance tests are
  still a bare `eventtest.Run` (`conformance_test.go:14-24`); the eighteen
  *passed* / two *not certified* table is asserted by nothing for that store. The
  round-1 argument for deferring stands, and its cost is smaller than it reads:
  for eighteen of the twenty sections the *staging* fixture's `passed` **is**
  asserted, one section at a time, by
  `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect`'s control arm
  (`defects_test.go:34-36`).
- **GAP-8** `[medium][deferred]` — `probe.instants` still reads only through
  `drain`; `unrecorded.ReadAll` still 0 %.
- **GAP-9's residue** `[medium][deferred]` — `Factory.Window` is still never
  driven at a non-default value; `probe.context()` (`probe.go:50-58`) is only ever
  entered with `Window == 0`.
- **GAP-11** `[medium][deferred]` — `fixtures_test.go` is still 865 lines, and the
  four boolean flag parameters are unchanged. Every other file in the package is
  under `architecture.md`'s 400.
- **GAP-12** `[low][deferred]` — `newHeld` still mints with
  `strconv.FormatInt(time.Now().UnixNano(), 36)` (`fixtures_test.go:75-77`).
- **GAP-13** `[low][deferred]` — §INV-019's negative half is still a manual act;
  see GAP-19.

### What this round confirms is right, so a later round does not re-litigate it

- All six round-1 findings are closed by mutation, not by claim (R1–R5), and the
  round-1 argument for M1 and M13 is confirmed by R6: those two clauses restate a
  kernel obligation `event`'s own suite pins with eight red tests.
- `run_test.go`'s subprocess pattern is the right answer to "nothing in-process
  can watch a `t.Error` without taking it", and its nine store shapes are each
  distinguishable: four separate deletions in `Run` and `admit` each turned
  exactly one case red with a message naming it.
- Determinism and isolation are clean: `-race -count=3` green, three
  `-shuffle=on` runs green, no `t.Parallel`, no sleeps, no network, no toolchain,
  no leftover files, no fuzz corpus written. The two `t.SkipNow()` calls are a
  documented simulation of `runtime.Goexit` and are named as such at
  `suite_test.go:47-54`.
- Universality holds: the suite declares its own `ledger`/`accountID`
  (`declaration.go`) and the proxy tests use a structurally different
  `inventory`/`warehouseID`; every count a section writes is derived from
  `Limits()`, and `TestEverySectionIsCertifiedAtTheNarrowestLimitsAStoreMayPublish`
  plus `TestTheMemoryStoreSatisfiesTheContractAtNarrowerLimits` are real second
  measurements. No golden value fits one sample.
- `TestEachProxyReportsTheThingItExistsToFind` and
  `FuzzKeysReportsACollisionExactlyWhenTwoIdentitiesRenderOneKey` remain the two
  places in S5 where the property is driven through every branch and quantified
  over caller data; the fuzz target's expectation is computed from
  `Aggregate.Key` rather than from the proxy it tests, so it is an independent
  equivalence and not a tautology.
- Doubles discipline is sound: the fixtures are second *implementations* of the
  published `event.Store` contract, built in a third package from the exported
  vocabulary alone, which is what §INV-019 requires; nothing mocks a project-owned
  type and nothing patches a private method. `event.Store` carries no optional
  interface, so [[D-061]]'s `Next()` obligation does not apply to `over` or to the
  decorators in `wrappers_test.go`.
