# EVENTSOURCE P4 — PLAN — GAPS

**Round 1 closed 2026-09-09.** All twelve blocking findings — one `[critical]`,
eleven `[high]` — are closed in
[`EVENTSOURCE_P4_PLAN.md`](../plans/EVENTSOURCE_P4_PLAN.md), and two of them
changed a contract [SPEC] had frozen: the effect gate is now on `Generations`
rather than on `Generation` (**P-7**, with §UC-193 written into
[`EVENTSOURCE_P4_USECASES.md`](../usecases/EVENTSOURCE_P4_USECASES.md)), and a
claim is an ownership token rather than a sequence name (**P-8**). The matrix
rule was re-checked mechanically over all 87 rows and reports zero breaches; the
twelve `[medium]`/`[low]` findings stay in
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P4`, untouched.

## Round 1 — plan auditor — 2026-09-09

Audited [`EVENTSOURCE_P4_PLAN.md`](../plans/EVENTSOURCE_P4_PLAN.md) against
[`EVENTSOURCE_P4_USECASES.md`](../usecases/EVENTSOURCE_P4_USECASES.md),
[`EVENTSOURCE_REFERENCE.md`](EVENTSOURCE_REFERENCE.md), the appendices at
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md:760+`, and the tree as it stands.
Every claim below was re-derived from the source rather than read off the plan.

**What was verified and is sound**, so the findings are read against a plan that is mostly right:

- **Coverage is arithmetically complete.** All 86 identifiers the spec defines (`UC-131…UC-192`,
  `INV-083…INV-106`) appear in the matrix; no identifier appears there that the spec does not
  define. Every counted `-list` pattern's arity matches the integer asserted beside it (11, 8, 16,
  10, 10, 25, 2).
- **P-1 is correct and is the plan's best line.** `event.Stream.String()` really is
  `event/identity.go:26-32` and renders `[stream orders]`; `event.Compose` (`identity.go:48-56`) is
  the right replacement.
- **The tier comparison compiles and is sound over the shipped adapters.**
  `event.Tracker.Backing()` (`event/checkpoint.go:123`), `event.NewAuthority`
  (`event/authority.go:23`), `Authority.Same` (`:36`) and `crud.KeyOf` (`crud/executor.go:524`)
  all exist. `crudsql.Tx{Executor: Executor{q: tx}}` (`crud/adapter/crudsql/crudsql.go:181`) makes
  `crud.KeyOf(executor)` the `*sql.Tx`, which is exactly what `eventpg.Checkpoints.Transaction`
  mints its authority over (`event/eventpg/checkpoints.go:138-143`); a savepoint answers the parent
  `*sql.Tx` through both routes. `event/projection` already imports `crud`, so the budget row in
  `scripts/event_test.go:45-49` does not move, as claimed.
- **`checkName` really admits `@`, `#` and `.`** (`event/text.go:63-70` refuses only brackets,
  control runes, non-UTF-8, empty and over-cap), so `orders@2#3.7` passes `event.Track`.
- **`event/projection` reaches none of `net`, `net/http`, `net/smtp`, `os/exec`** today
  (`go list -deps ./event/projection`, 127 packages, zero matches), so
  `TestNoPackageOnTheProjectionPathCanDispatch` can be green and can fail.
- **`startsNothing` does forbid a `go` statement in every non-test file under `event/`**:
  `packagesUnder` lists `.GoFiles` only and `underTest` is decided by a non-test `testing` import
  (`scripts/extensionlisting_test.go:112-136`, `scripts/extensions_test.go:147-179`).
- **`files < 9` → 18 is right**: `event/projection` holds exactly nine non-test files today and the
  plan adds nine.
- **The two new store obligations are real and falsifiable in principle.** `eventpg` answers
  `Conflict` for a save at advance 1 over a live row (`ON CONFLICT DO NOTHING` → 0 rows →
  `event/eventpg/checkpoints.go:273-278`), and its cursor carries no projection name
  (`cursor.go:40-47`).
- **The awk range over `docs/api/surface.md` is exact**: `## …/vv/event` is line 1015 and the next
  `## …/vv/event/` header is `event/eventmemory` at 1073.

---

### GAP-1 [critical][immediate] The effect gate cannot suppress generation zero, so the first cutover of any live deployment has two senders

- **Where:** PLAN.md "the stage" suppressor list (§`pass.go`, step 3: *"`spec.Generation !=
  Ungenerated` → `Generations.Active(inner, spec.Name)`"*); `Spec` refusal 8; the D-135 bullet
  *"the ownership read inside the committing transaction as the two-sender boundary **neither
  source has**"*. Inherited from [SPEC] §1.6 (*"a projection that has no generations has one
  sender, and the fence already ensures that"*).
- **What:** Suppressor 3 is skipped entirely when `Spec.Generation == Ungenerated`. Every
  projection that exists today is `Ungenerated` ([SPEC] §1.5: *"`Ungenerated` (zero) renders
  nothing, so every projection that exists today is generation zero and its name does not
  change"*). `CutoverSpec.From` is a `Generation`, so `From: Ungenerated, To: 2` is expressible and
  is the only migration path an existing deployment has. After that cutover the retiring
  projection is still running, still holds `Effects`, and **cannot read the ownership row** — the
  suppressor that would stop it is gated on the one condition it can never satisfy.
- **Why this severity:** Deployment runs `orders` (`Ungenerated`) with `Effects` staging order
  confirmations. `orders@2` is built beside it, `Reached`, and `Cutover(From: 0, To: 2)` commits.
  `orders@2` reads `Active("orders") == 2` and stages. `orders` skips step 3 and stages. Every
  event from the cutover onward produces **two** staged effects — two emails, two payment
  captures — with no error on any path, until an operator notices and stops `orders`. This is the
  exact failure ES-06 exists to prevent, on the one path every existing consumer must take, while
  the plan's own D-135 claims the boundary is closed. The alternative — giving the live projection
  `Generation: 1` up front — is unavailable to an already-running one: `orders` → `orders@1`
  changes the checkpoint row key and resumes from the origin against a live read model, which is
  precisely the silent failure §UC-188's refusal exists to make loud.
- **Why this timing:** It is the shape of `Spec`, of the suppressor order and of `CutoverSpec`, and
  D-135 is written in S6 asserting the opposite. Every one of S5's ten tests is written against the
  suppressor list.
- **Close criteria:**
  - [x] Either `Cutover` refuses `From == Ungenerated` (naming the migration and the remedy), or
        suppressor 3 runs whenever a `Generations` is supplied — including at `Ungenerated`, where
        `Active` answering anything other than `Ungenerated` suppresses — or `New` refuses `Effects`
        beside `Generations` at `Ungenerated`. Whichever is chosen is stated with its consequence
        for the shipped `Ungenerated` projection.
  - [x] A use case and a test cover the generation-zero retirement, in the shape §UC-173 uses, and
        the test fails against today's suppressor list.
  - [x] D-135 and `docs/modules/{en,ru}/projection.md` state what the boundary does **not** cover,
        beside the second-`Spec.Name` edge that is already written down, or state that it now does.
- **Status:** closed 2026-09-09 — **P-7** gates suppressor 3 on `spec.Generations != nil` rather than on `spec.Generation`; `Generations.Active` answers `Ungenerated` with a nil error for a projection with no row; refusal 11 amended; §UC-193 written into [SPEC] beside §UC-192 and `TestAnUngeneratedProjectionWithGenerationsStopsStagingAtTheCutover` added to S5's Tests and its counted `EFF` pattern (10 → 12), with the no-`Generations` control that measures the limit; the release ordering and the uncovered case stated in D-135 and on `docs/modules/{en,ru}/projection.md`.

### GAP-2 [high][immediate] Two use cases are proved by tests that exist nowhere, in direct violation of the plan's own matrix rule

- **Where:** PLAN.md coverage matrix rows UC-142 (line 332) and UC-158 (line 363).
- **What:** The plan states, in bold: *"Every name below is in the Tests list of the section its
  Checkpoint column names, and inside that checkpoint's own counted `-list` pattern. A name here
  that no arm counts is a use case that can be reported closed on a green `go test`, which is
  phase 1's failure mode exactly."* Two names break it:
  `TestNoExportedFunctionOrdersOrTakesTwoCursors` (UC-142, "a merge is asked for", also carrying
  INV-088) and `TestAGenerationDrainsBesideTheLiveOneAndTheRowsAgree` (UC-158, "a generation is
  built beside the running one" — the headline ES-04 outcome). Both appear **only** in the matrix:
  neither is in any section's Tests list, neither is in any counted `-list`/`-run` pattern, and
  `grep -rn` over the whole tree finds neither. S6's numbered live list has no item for UC-158
  either, and S4's `Covers` line omits UC-158.
- **Why this severity:** UC-158 is what ES-04 is for. As written, S6 can report ES-04 closed with
  every arm green while nothing anywhere runs a rebuild beside a live generation under the new
  naming. UC-142's refusal of a merge — the one thing [[D-128]] and [[D-129]] jointly forbid — is
  proved by a walk that does not exist; the walk that does exist
  (`TestNoExportedFunctionTakesAPositionAndAnswersACursor`, `scripts/projection_test.go:42`) asks a
  different question and would not see a two-cursor function.
- **Why this timing:** A missing test for a stated invariant, and the checkpoint that would have
  caught it is the one being written.
- **Close criteria:**
  - [x] UC-158 names a test that is in S6's numbered live list and inside S6's counted 25-name
        pattern (which becomes 26), or S4's, and the name exists after the section runs.
  - [x] UC-142 names either a real new walk (in `scripts/projection_test.go`, in S6's counted
        two-name pattern, with a fixture control) or the existing walk it actually means, and the
        `event.Read` control it cites is the one that walk already carries.
  - [x] A mechanical re-check: every `Test…` name in the matrix appears in the counted pattern of
        the section its Checkpoint column names.
- **Status:** closed 2026-09-09 — `TestNoExportedFunctionOrdersOrTakesTwoCursors` is now a real new walk in S6's Source walks with a `Merge` fixture control, and `TestAGenerationDrainsBesideTheLiveOneAndTheRowsAgree` is S6 live item 9's third test; S6's live pattern 25 → 26 and its scripts pattern 2 → 5. The mechanical re-check was run over all 87 matrix rows: it found five breaches, not two — `TestCursorIsNeverCompared` (INV-088) and `TestNewRefusesEverySpecItCannotAssemble` (INV-104) ran in arms that counted nothing, and `TestEverySequencerIsTotalPureAndStable` was counted by S1 while UC-137 named S2. All five closed; the re-check now reports zero.

### GAP-3 [high][immediate] The conformance-extension counts name the store defect inventory, not the checkpoint one, so S2 goes red on its own numbers

- **Where:** PLAN.md "The counts that must move with them", row *"`inventoried` |
  `defects_test.go:47` | 29 | **32**"*; and the framing obligation *"`TestEverySectionIsNamedByADefectThatBreaksIt`
  (`event/eventtest/defects_test.go:71`) is what forces the third"*.
- **What:** `inventoried = 29` (`event/eventtest/defects_test.go:47`) sizes `eventtest.Defects()` —
  the **store** conformance suite's inventory (`export_test.go:22`). The checkpoint suite has its
  own: `checkpointDefects = 5` at `event/eventtest/checkpoints_test.go:275`, asserted by
  `TestEveryCheckpointDefectIsReportedByItsOwnSection` (`checkpoints_test.go:248-272`) against
  `eventtest.CheckpointDefects()` (`export_test.go:106`). Three new **checkpoint** defects move 5
  to 8 and leave 29 alone.
- **Why this severity:** Following the plan literally sets `inventoried = 32` while
  `eventtest.Defects()` still returns 29 → `TestTheDefectInventoryIsTheSizeItSaysItIs` fails; and
  `checkpointDefects` stays 5 while `CheckpointDefects()` returns 8 →
  `TestEveryCheckpointDefectIsReportedByItsOwnSection` fails at its first `t.Fatalf`. S2's
  `go test -race -count=1 ./event/... ./scripts/` arm goes red for a reason the plan says is the
  fix, and the tempting repair — reverting one of the counts — silently drops a defect.
- **Why this timing:** It is S2's checkpoint, and S3–S6 all re-run `./event/...`.
- **Close criteria:**
  - [x] The counts table names `checkpointDefects` at `event/eventtest/checkpoints_test.go:275`,
        5 → 8, and leaves `inventoried` at 29.
  - [x] `event/eventtest/checkpoints_test.go` stays inside S2's `event-kernel-moved` allowed set
        (it already is).
- **Status:** closed 2026-09-09 — the counts table names `checkpointDefects` at `checkpoints_test.go:275`, 5 → 8, and states in full why `inventoried` stays at 29; S2's Tests bullet for `TestTheDefectInventoryIsTheSizeItSaysItIs` now says *unchanged*, and `defects_test.go` has left both S2's file list and its allowed set so a count edited there is reported.

### GAP-4 [high][immediate] Nothing in the tree forces a defect for a *checkpoint* section, so the two new `eventtest` sections can be written to assert nothing and stay green

- **Where:** PLAN.md framing obligation 2 and "The three defects that falsify them".
- **What:** `TestEverySectionIsNamedByADefectThatBreaksIt` (`defects_test.go:71-88`) iterates
  `eventtest.SectionNames()` and `eventtest.Defects()` — the **store** suite — and calls `guarded`
  (`defects_test.go:90-99`), which is called from nowhere else in the package
  (`grep -rn 'guarded(' event/eventtest/`). `TestEveryCheckpointDefectIsReportedByItsOwnSection`
  runs defect → section and never section → defect. So the anti-vacuity rule the plan leans on to
  *force* the third defect does not see `topology` or `topology handoff` at all.
- **Why this severity:** This is the audit question the plan raises about itself and answers wrong.
  A `topology` section whose body is `_ = this` certifies for every store and every gate in this
  phase stays green: `Certified` counts it, the census counts it, and no test asks whether it
  asserts anything. That is exactly the failure mode a conformance suite cannot see about itself,
  in the one place this phase widens the store contract.
- **Why this timing:** The sections and their defects are written together in S2; adding the guard
  afterwards means rewriting both.
- **Close criteria:**
  - [x] A `TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt` exists, built on the same
        `guarded`-style control (an inventory with that section's rows removed must fail), and it
        fails when either new section's defect row is deleted.
  - [x] Each of the three new defects is shown to break its own section and to leave the plain
        store passing, through the existing `oneCheckpointVerdict` pair.
  - [x] Its addition is in S2's file list and inside S2's `event-kernel-moved` allowed set.
- **Status:** closed 2026-09-09 — S2 writes `TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt` in `checkpoints_test.go`, carrying its own `checkpointGuarded` (the types differ and making `guarded` generic would move `defects_test.go`), with the per-section removal control; it is in S2's Tests list and in S2's counted eventtest pattern, which goes 4 → 5. Framing obligation 2 rewritten to say the control does not exist and S2 writes it.

### GAP-5 [high][immediate] S6's `event-kernel-moved` arm cannot pass: S6 moves no file under `event/` outside `event/eventpg`

- **Where:** PLAN.md S6 **Files** list and S6 checkpoint, arm
  `./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s6 '^event/(projection/doc\.go|eventtest/doc\.go)$'`.
- **What:** `event_kernel_moved` computes the moved set from the two manifests and **fails on an
  empty one** (`scripts/checks.sh:434-437`: *"a section that moved no file under event/ did not
  deliver"*). The manifest excludes `event/eventpg/*` (`checks.sh:335`). S6's entire file list is
  `event/eventpg/*` test files, `scripts/projection_test.go`, `_examples/`, `docs/` — nothing under
  `event/` that the manifest sees. The predecessor is recorded at the start of S6, so S3's and S5's
  `doc.go` edits do not count. The allowed ERE names two `doc.go` files that appear in no S6 file
  list and are required by nothing (`"${@:3}"` is empty).
- **Why this severity:** S6's checkpoint chain is `&&`-joined, so the run stops there and the whole
  live section reports failure for a bookkeeping reason. The obvious repair — touching
  `event/projection/doc.go` so the arm has something to see — is a workaround that makes the fence
  measure nothing.
- **Why this timing:** It is the gate that closes the phase.
- **Close criteria:**
  - [x] Either S6 lists the `event/`-side files it really moves (and requires them by path), or the
        `event-kernel-moved` arm is replaced for S6 by an arm asserting the manifest is **identical**
        to its predecessor — which is the honest statement for a section that changes nothing under
        `event/`, and which `event-kernel-moved` cannot make.
  - [x] Whichever is chosen is stated with the reason, so a later reader does not read the change
        as the fence being relaxed.
- **Status:** closed 2026-09-09 — S6's arm is `diff -u .git/event_kernel_before_s6 scripts/event_kernel.sha256`, the assertion that the manifest is identical to its predecessor, with the reason written both at the arm and in *The kernel baseline move*: S6 moves nothing the manifest sees, `event_kernel_moved` fails on an empty set by design, and touching a file to feed it would make the fence measure nothing.

### GAP-6 [high][immediate] `event/eventpg/census_integration_test.go` pins the checkpoint section list and is in no file list, so the live suite goes red

- **Where:** PLAN.md S6 **Files**; PLAN.md *"Neither `eventmemory.Checkpoints` nor
  `eventpg.Checkpoints` is expected to change."*
- **What:** `checkpointCensus()` (`event/eventpg/census_integration_test.go:71-87`) enumerates the
  twelve checkpoint sections and their verdicts, and `certifies` fails on
  `len(reported) != len(want)` (`census_integration_test.go:152-155`). Adding two sections makes a
  conformance run report fourteen against a census of twelve, and
  `TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines` fails. The file is named in
  no section. The store class does not change — the *satellite's census of it* must.
- **Why this severity:** S6's `go test -race -count=1 -tags=integration ./event/eventpg/...` — run
  twice — is where §6's nineteen items are proved, and it fails before any of them are read. The
  census exists precisely so a downgraded section cannot print `ok`; leaving it at twelve while the
  suite runs fourteen also leaves the two new sections outside the one harness that would notice
  them being downgraded to "not certified".
- **Why this timing:** S2 writes the sections; the satellite that certifies them is red from that
  moment until S6.
- **Close criteria:**
  - [x] `event/eventpg/census_integration_test.go` is in the file list of the section that adds the
        sections (S2, or S6 with S2 recorded as leaving the satellite red and why).
  - [x] `checkpointCensus()` gains the two rows with their verdicts, and the `downgrades()`
        inventory beside it is considered — a new section with no withdrawal row is a section the
        census cannot catch a downgrade of.
- **Status:** closed 2026-09-09 — `event/eventpg/census_integration_test.go` is in **S2**'s file list, `checkpointCensus()` gains `{"topology", certified}` and `{"topology handoff", certified}`, and S2 gains two arms it can run without a database: `go vet -tags=integration ./event/eventpg/` and a `grep -c` of the two rows. `downgrades()` is considered and stays at five, with the reason recorded: neither new section is gated on a withdrawable hook, and `no-transactions-claim` already covers `topology handoff`.

### GAP-7 [high][immediate] `eventmemory` cannot certify fourteen sections, and the deliverable checklist makes that a box to tick

- **Where:** PLAN.md S2 Tests (*"now reporting fourteen certified sections for `eventmemory`"*) and
  the deliverable checklist (*"`eventmemory.Checkpoints` and `eventpg.Checkpoints` both certify
  fourteen checkpoint sections"*).
- **What:** `eventmemory.Checkpoints.Capabilities()` returns `Persistence: Unsupported`
  (`event/eventmemory/checkpoints.go:40-42`), so the `durability` section is declined for it — the
  shipped assertion is `certified != 11` of twelve
  (`event/eventtest/checkpoints_test.go:291-315`). After two mandatory-or-transaction-gated
  sections, `eventmemory` certifies **13**, not 14. `eventpg` certifies 14.
- **Why this severity:** A gate item that is false as written is a gate that proceeds red or is made
  green by a lie. The cheapest way to make the box true is to have `eventmemory` claim persistence —
  which is untrue of a store whose rows live on a `*Log` in memory, moves the kernel manifest, and
  would silently switch on the `durability` section against a `Sibling` that shares the same log.
- **Why this timing:** It is the section's reported outcome and the phase's checklist.
- **Close criteria:**
  - [x] S2 and the checklist say 13 for `eventmemory` and 14 for `eventpg`, with the declined
        section named.
  - [x] `eventmemory.Checkpoints.Capabilities()` is unchanged, proved by the manifest fence.
- **Status:** closed 2026-09-09 — S2's Tests bullet and the deliverable checklist say **13** for `eventmemory` (declining `durability`, named) and **14** for `eventpg`; `eventmemory.Checkpoints.Capabilities()` is explicitly not touched and the manifest fence is named as what proves it.

### GAP-8 [high][immediate] INV-103's "opens a transaction" clause is proved by nothing, and the plan says the check already runs

- **Where:** PLAN.md invariant matrix, INV-103: *"`startsNothing` extended over every new file; the
  `Begin`/`Commit`/`Rollback` source check [[D-130]] already runs; …"*
- **What:** No such check exists. `startsOrReadsSomething`
  (`event/projection/lifecycle_test.go:275-327`) reports goroutines, `init`, package-level writes
  and the calls in `forbiddenCalls` (`lifecycle_test.go:266-270`) — `log.*`, `fmt.Print*`,
  `os.Getenv`/`LookupEnv`/`Environ` — and nothing about transactions. `scripts/projection_test.go`
  carries four walks, none of them this one. `grep -rn 'opensATransaction'` finds it only in
  `event/eventpg/sources_test.go`, which is a store-side check about `*sql.DB` versus `*sql.Tx`.
- **Why this severity:** [[D-130]]'s core refusal — the framework opens no transaction — is
  unfalsifiable for `event/projection`, and phase 4 is the first to add functions that plausibly
  would: `Split`, `Cutover` and `Redrive` each take a caller `Unit`, and the one-line implementation
  an implementer reaches for is `crud.InNewTx` inside the function rather than in the caller's spec.
  That would compile, pass every arm of every checkpoint in this plan, and quietly move the
  transaction boundary out of the caller's hands — the property ES-01 is entirely about.
- **Why this timing:** It is the control for three of this phase's four new entry points, and it is
  cheapest to write before they are.
- **Close criteria:**
  - [x] A source check over `event/projection`'s non-test files reports `Begin`, `Commit`,
        `Rollback`, `InNewTx`, `InTx` and `InAtomic`, with a fixture control that is reported.
  - [x] It is named in a section's Tests list and inside that section's counted `-list` pattern.
  - [x] INV-103's matrix row names the check that exists rather than one that does not.
- **Status:** closed 2026-09-09 — `TestNothingInTheProjectionPackageOpensATransaction` is written in **S1**, in `scripts/projection_test.go`: an AST walk over `event/projection`'s non-test files reporting `Begin`/`Commit`/`Rollback` on any receiver and `crud.InNewTx`/`InTx`/`InAtomic` by package and name, with a reported fixture control. It is counted in S1 (its own one-name arm) and re-counted in S6's five-name scripts pattern; INV-103's row names it and says it is new.

### GAP-9 [high][immediate] The claim protocol carries no ownership token, so an expired claim reproduces the disorder the queue exists to prevent

- **Where:** PLAN.md `event/projection/redrive.go`: `Claim(ctx, of Identity, sequence string)
  (claimed string, found bool, err error)`, `Release(ctx, of, sequence)`, and *"`Release` is called
  on every exit path, including a panic. The clock that expires an abandoned claim is the
  application's, in the application's table."*
- **What:** `Claim` answers a sequence name, not a claim identity, and `Release`/`Touch`/`Evict`
  take only `(of, sequence)`. Nothing lets an implementation refuse a write from a caller whose
  claim has expired and been taken by somebody else. `Redrive.Sequence` reads the letters **once**
  (`Park.Sequence`) and then runs one unit per letter, so a claim lost mid-iteration is never
  re-checked.
- **Why this severity:** Operator A runs `Redrive.Any`; it claims sequence `A` holding letters
  `A2 A3 A4` and applies `A2`. Its host stalls (GC pause, a slow `Destination`, a container
  freeze) past the application's claim duration. The `runtime.Runner` the plan explicitly invites a
  host to wrap `Redrive.Any` in re-claims `A` — legally, by the expiry rule the plan adopts from
  Axon — reads `A3 A4`, and applies `A3`. A resumes and applies its own next letter `A3` again, or
  reaches `A4` before B's `A3` commits. Two applies of one letter, and `A4` before `A3` — the
  queue's own ordering guarantee broken by the queue's own recovery path, which is verbatim the
  failure [SPEC] §1.4 introduces `Claim` to close. A then calls `Release(A)` on its way out and
  frees **B's** claim, so a third caller enters while B is still applying. §UC-191's planned test
  (`TestTwoGatedRedrivesNeverProcessOneSequence`) drives two *simultaneous* `Claim`s and cannot see
  any of this.
- **Why this timing:** It is the signature of two interface methods that the example `Redriver`,
  both `_examples` and every S3/S6 redrive test are written against.
- **Close criteria:**
  - [x] `Claim` answers an opaque claim value (or the deadline it was granted) that `Release`,
        `Touch` and `Evict` carry, and the `Redriver` contract states that a call whose claim no
        longer owns the sequence must be refused rather than applied.
  - [x] A test expires a claim between two letters of one sequence and asserts the loser applies
        nothing, evicts nothing and releases nothing — with the control that the same run without
        the expiry drains normally.
  - [x] The module page states the relationship between the application's claim duration and the
        longest unit a redrive may take.
- **Status:** closed 2026-09-09 — **P-8**: `Claim` answers a total, opaque `Claim` value (`Of`, `Sequence`, `Token`, `Until`) that `Sequence`, `Evict`, `Touch` and `Release` all carry; the contract states a write whose claim no longer owns the sequence is refused with `ErrClaimLost`, and that a lost claim stops the redrive with no `Touch` and no `Release`. `TestAnExpiredClaimAppliesEvictsAndReleasesNothing` expires a claim between two letters and asserts the loser's unit rolls back, with the no-expiry control; PARK 16 → 17. The claim-duration obligation is on the module page.

### GAP-10 [high][immediate] `Split` drops `Progress.Highest` from the children, and a child at zero makes `Observe` answer the origin and `Cutover` derive a barrier of zero

- **Where:** PLAN.md `event/projection/topology.go`, step 4: *"`Save` both children at advance 1
  carrying the parent's cursor **byte for byte**; the lower-numbered child takes the parent's
  `Applied` and `Quarantined`, the higher starts both at zero … `At` is the split's own instant."*
  [SPEC] §1.3 *"What `Split` does with `Progress`"* says *"both children take the parent's `Cursor`
  **and its `Highest`**"*.
- **What:** The plan enumerates `Cursor`, `Applied`, `Quarantined` and `At` and omits `Highest`.
  The tracker door does not refuse it — `admit` deliberately does not make `Progress` total
  (`event/checkpoint.go:149-191`) — so a child written with `Highest: 0` is a legal row.
- **Why this severity:** `Observe` answers *"the lowest `Highest` across the cover"* and `Cutover`
  derives its barrier from `Observe` over the retiring cover. Split partition `{0,1}` of a live
  `orders` that has reached position 900_000; both children go in at `Highest: 0`; the next
  `Observe` answers `0`; `Reached` is trivially true for any arriving generation, `Cutover` refuses
  nothing, and the read target switches to a generation that has applied nothing. S4's
  `TestACutoverTakesNoBarrierAndDerivesItsOwn` asserts *"a fixture retiring generation with live
  rows cannot yield a barrier of zero"* — its fixture has no split, so it passes while the real
  path is broken. `TestSplitPreservesTheSumAcrossThePartitionSet` sums `Applied`/`Quarantined` and
  does not look at `Highest`.
- **Why this timing:** It is the contract of the one write `Split` makes, and every generation and
  cutover test in S4 and S6 reads what it wrote.
- **Close criteria:**
  - [x] Step 4 states `Highest` explicitly, both children taking the parent's, matching [SPEC] §1.3.
  - [x] `TestASplitWritesTwoChildrenAtTheParentsCursorInOneTransaction` asserts `Highest` on both
        children, and a `Observe`-after-a-split arm asserts the barrier is the parent's number and
        not the origin.
- **Status:** closed 2026-09-09 — step 4 states `Highest` explicitly with a paragraph on why the tracker door will not enumerate it; `TestASplitWritesTwoChildrenAtTheParentsCursorInOneTransaction` asserts it on both children with the admitted-at-zero control; and S4's `TestACutoverTakesNoBarrierAndDerivesItsOwn` now builds its fixture with a real `Split` and asserts `Observe` answers the parent's 900_000, against the hand-written-at-zero control.

### GAP-11 [high][immediate] No tier-A wiring exists for an untagged `event/projection` test over `eventmemory`, and S3 and S5 need one for about twenty tests

- **Where:** PLAN.md S3 (*"Tests — untagged, in `event/projection`"*, sixteen names) and S5 (ten
  names), against `Spec` refusals 3 and 4 (`ParkSequence` requires `InUnit` and refuses
  `Unchecked`) and the new `checkUnit` comparison.
- **What:** After S1, tier A requires `authority.Same(event.NewAuthority(tracker.Backing(),
  crud.KeyOf(executor)))`, and `crud.SameDataSource` refuses two values of different types
  (`crud/executor.go:562-571`). `eventmemory`'s checkpoint authority is minted over `txIdentity`
  (`event/eventmemory/transaction.go:41-44`) — an **unexported** struct with no exported accessor
  anywhere on `*Tx` or on `event.Authority`. Every shipped `InUnit` test in this package uses
  `Destination = projection.Unchecked` (`unit_test.go:69,151,326,398,572`, `loop_test.go:406`),
  which is exactly what `ParkSequence` refuses. The plan never says how a park test constructs a
  destination that compares `Same`.
- **Why this severity:** Every one of S3's park tests and S5's park-and-effect tests either cannot
  be constructed or must fabricate the authority through a `watchedCheckpoints`-style decorator
  (`event/projection/harness_test.go:119`) overriding `Transaction`. The second is workable and is
  almost certainly what will be written — but then INV-091 (*"the park, the read model and the scan
  checkpoint commit together"*), whose S3 arm is *"an injected rollback after the park asserting the
  queue is empty and the advance unmoved"*, is asserted over a comparison the test itself made true
  and a "read model" that is a Go map with no transaction at all. The invariant's real proof is S6's
  `TestTheBlockingTestAndTheAdvanceAreOneCommit` alone, and the plan lists S3 as a checkpoint for it.
- **Why this timing:** It is the harness twenty tests are written on, and it decides what those
  tests are evidence for.
- **Close criteria:**
  - [x] The plan states how a tier-A projection is constructed over `eventmemory` in an untagged
        test, names the seam, and says in one sentence that the alignment there is fabricated.
  - [x] INV-091's matrix row names S6 as the only checkpoint that proves the commit-together half,
        and S3's row is narrowed to what a memory stand can actually falsify.
  - [x] `harness_test.go` is in the file list of the section that adds the wiring.
- **Status:** closed 2026-09-09 — S1 adds one named seam, `aligned(t)` in `harness_test.go`, and the plan says in three sentences that the alignment there is fabricated and what those tests can and cannot falsify; INV-091's Checkpoint column is now **S6** alone for the commit-together half, with S3's arms narrowed in the row itself; `harness_test.go` is in S1's file list.

### GAP-12 [high][immediate] S1 realises `ErrTopology` but the plan declares it in an S2 file that S1's own manifest fence forbids

- **Where:** PLAN.md S1 **Realises** (*"… `ErrTopology`, `Spec.Sequence` …"*), the contract block
  `event/projection/topology.go` (*"`var ErrTopology = errors.New(...)`"*, an S2 file), and S1's
  `event-kernel-moved` allowed ERE
  `^event/projection/(identity|partition|cover|sequence|spec|pass|page|state|projection|…_test)\.go$`.
- **What:** S1's own contracts need the sentinel: `NewPartition` refuses *"a mask that is not
  `2^k − 1` … with `ErrTopology` naming it"*, `Partition.Split` refuses at the ceiling with it, and
  `NewCover` refuses *"each wrapping `ErrTopology`"*. S1's allowed set contains neither
  `topology.go` nor `errors.go` nor `classify.go` nor `doc.go`, so the declaration has nowhere legal
  to go, and `go build ./...` in S1's checkpoint fails without it.
- **Why this severity:** S1 cannot be closed as written. The repair an implementer reaches for —
  declaring it in `partition.go` — contradicts the contract block and S2's `Realises` list, and
  nothing afterwards notices the drift; the other repair, widening S1's allowed set, is the fence
  being relaxed to fit rather than the plan being corrected.
- **Why this timing:** It is the first section's build.
- **Close criteria:**
  - [x] `ErrTopology` has one stated home; if it is `errors.go`, that file is in S1's file list and
        in S1's allowed ERE, and the "four, and none of them crosses a store seam" comment moves in
        the same section.
  - [x] `ErrParkFull` (S3) and `ErrRetired` (S4) are checked the same way against their sections'
        allowed sets.
- **Status:** closed 2026-09-09 — all four new sentinels have one home, `event/projection/errors.go`, with a contract block of its own naming which section declares which; the *four, and none of them crosses a store seam* comment is rewritten in S1 in the same change. `errors.go` is in S1's and S4's file lists, allowed EREs and required paths (S3's ERE already carried it, and its file list now names it).

---

## Deferred — recorded in [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P4`

Twelve `[medium]`/`[low]` findings (items 17–28) are appended there under the 2026-09-08 delivery
policy and are not repeated here. They do not block this gate. Chief among them, so they are not
lost: the
`Quarantine` → `ParkSequence` change is a behaviour change rather than the rename the plan calls it
(`event/projection/retry_test.go:262-341` runs three subtests at `AfterApply`, which refusal 3 now
forbids, over one stream, whose later envelopes `ParkSequence` now blocks); `event/projection/
lifecycle_test.go:235` carries a second stale `walked < 9` guard the plan does not raise; and
`TestNoModulusIsAppliedToASequenceHash` walks a tree in which the modulus it forbids cannot appear.
