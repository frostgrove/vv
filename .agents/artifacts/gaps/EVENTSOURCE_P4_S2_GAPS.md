# EVENTSOURCE P4 — S2 (the partitioned loop, the handoff, and the store contract) — GAPS

## Round 1 — econv-code-reviewer — 2026-09-09

Two blocking findings, both driven rather than read. Everything the section
*claims* reproduces exactly — the pasted checkpoint output, the moved set, the
live 14-of-14 census, the mutation table (three re-run independently, three
caught). What is broken is a path S1 and S2 opened together and no case in the
tree walks: a projection whose `Identity` renders anything other than
`Spec.Name`.

---

### GAP-1 [critical][immediate] The settlement re-reads a row keyed by `Spec.Name`, so every partitioned or generational projection settles against the wrong row — it halts on a lost fence, and after a rollback it adopts the coarse row and writes there for ever

- **Where:** `event/projection/pass.go:460`

  ```go
  tracker, err := event.Track(this.spec.Checkpoints, this.spec.Name)
  ```

  Every other checkpoint call in the loop goes through `this.tracker`, built in
  `New` from `identity.String()` (`spec.go:136-142`, `projection.go:58-69`), or
  through `this.identity.coarser()` (`pass.go:84`). `settle` is the one place
  that re-derives the name, and it re-derives it wrong: for
  `Spec{Name: "orders", Partition: {0,1}}` the row key is `orders#0.1` and
  `settle` reads `orders`. Same for `Spec{Name: "orders", Generation: 2}` —
  `orders@2` vs `orders`.

- **What:** `settle` is the bounded resolution for every save whose fate its own
  answer did not carry — `ErrUncertain`, `ErrConflict`, and a caller's `Unit`
  answering after the save inside it returned nil (`pass.go:424-483`). It builds
  a *second* tracker to re-read the row. Because that tracker is keyed by
  `Spec.Name`, a partitioned or generational projection reads a row that either
  does not exist (the ordinary case) or belongs to a *different* topology. Three
  of the four arms in the table then decide on a row that is not this runner's,
  and `confirmed`, `rolledBack` and `overtaken` all assign
  `this.tracker = tracker` (`pass.go:520`, `:532`, `:554`) — after which the loop
  writes **every subsequent checkpoint under the coarse name**.

- **Why this severity:** two driven runs, both against `eventmemory` through the
  shipped harness, both with a `Checkpoints` decorator recording the *name* of
  every call.

  **(a) A lost fence halts, which is [[D-133]] inverted.** A partitioned runner
  at `orders#0.1`, a second live instance of the same partition reaching the
  fence first — the shape every rolling restart produces on purpose:

  ```
  partition matching the stream: "0.1", identity "orders#0.1"
  phase=halted err=projection: this projection stopped advancing and is not applying events:
      "orders": projection: this checkpoint store refused a save over the very row its own
      fence admits: event: the stream is not at the version this append was decided at: conflict
  loads=[orders orders#0.1 orders]
  saves=[orders#0.1]
  ```

  The three loads are `unclaimed`'s coarser probe, the resume, and the
  settlement. The settlement's is `orders` — absent, `Advance 0` — so
  `found.Advance+1 == waiting.presented` with `fenced(cause)` true, and
  `pass.go:474-475` halts on `errFenceRefused`. [[D-133]]'s stated invariant is
  *"A checkpoint save the fence refuses does not halt the projection"*, and its
  own argument for why halting was rejected is *"a framework whose singleton
  runner dies on every deploy is not one a deployment can use"*. With
  `Spec.Partition` set, every partition dies on every rolling deploy. At an
  advance above 1 it does not even reach that arm: `found.Advance == 0` against
  `presented == N+1` falls to the default and halts on `errRowRetired`.
  `docs/modules/en/projection.md:245` states the opposite in as many words —
  *"A save the fence refuses does not halt the projection"*.

  **(b) The loop permanently adopts the coarse row.** A partitioned `InUnit`
  runner at `orders#0.1` whose handler refuses one page, so the unit rolls back
  and `settle` reaches `rolledBack`:

  ```
  identity is "orders#0.1"
  loads=[orders orders#0.1 orders]
  saves=[orders#0.1 orders orders]
  row "orders"      = {Cursor:…:4 Advance:4 Progress:{Highest:4 Applied:4 …}}
  row "orders#0.1"  = {Advance:0 …}
  ```

  After one rollback the runner writes its checkpoints to `orders` and its own
  row never moves again. Three consequences, all silent at the time and all
  permanent:
  1. On the next restart `unclaimed` finds a live `orders` row and **halts every
     partition of the projection for good** (`pass.go:96`) — the migration
     refusal firing on rows the framework wrote itself.
  2. With a real `Cover` of N partitions, all N converge on the single `orders`
     row. They contend on one fence, and each loser's `overtaken` rebuilds its
     reader from *another partition's* cursor (`pass.go:550`) — so a partition
     jumps forward over positions it never delivered. That is permanent, silent
     loss of every event in the gap, which is exactly the failure
     `pass.go:496-498`'s `anothers` comment says the cursor comparison exists to
     prevent.
  3. `orders#0.1` stays at advance 0, so a restart re-walks the log from the
     origin into a live read model.

  Neither path needs an exotic store: (a) needs two replicas, (b) needs one
  handler failure under `InUnit`.

- **Why this timing:** it is a wrong public contract on the value S3, S4 and S5
  all build on. S3's park and redrive are keyed by `Identity.Whole()`, S4's
  generations are `Identity` with a non-zero `Generation`, and every one of them
  will inherit a settlement that reads the wrong row. S4 in particular makes
  `Generation` the *primary* migration mechanism, so this stops being a
  partitioned-only defect the moment S4 lands. It also invalidates the S2
  checkpoint's own green: `go test ./event/...` passes because no case in the
  tree drives a conflict, an uncertain save or a unit rollback through a
  projection whose identity is not its name.

- **Close criteria:**
  - [x] `settle` builds its tracker from `this.identity.String()`
        (`event/projection/pass.go:494`); no `event.Track` in the package is keyed
        by `Spec.Name`.
  - [x] `TestALostFenceIsSettledOnTheRowOfThisRunnersOwnIdentity` — a partitioned
        runner loses the fence on every page and reaches `PhaseRetrying` with
        `ErrOvertaken`, applies the whole log through the rows the winner left
        (which is the reader rebuilt from the winner's cursor, read off the
        destination rather than off a field), and never halts.
  - [x] `TestAUnitThatRollsBackLeavesTheAdvanceOnThisRunnersOwnRow` — a
        partitioned `InUnit` runner through a unit rollback: every `Save` the
        store saw carries the partition's own row key, and `orders` holds no row.
        Its control is the retrying state carrying the handler's own failure, so
        the `rolledBack` arm was reached rather than skipped.
  - [x] Both are table-driven over a partition, `Generation: 2` with
        `Partition: Whole()`, and the render-alike control at `Ungenerated` over
        the whole key space — which passes whichever row is read and is what says
        the other two arms measure the identity.
  - [x] `TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity`
        (`scripts/projection_test.go`) reports any `event.Track` in
        `event/projection` whose second argument mentions no value of type
        `Identity`, with a two-call fixture as its control.
- **Mutation:** `settle` reverted to `this.spec.Name` → the two loop tests fail on
  the partition and generation arms and pass on the control arm, and the
  structural guard reports `pass.go:494` by line.
- **Status:** closed 2026-09-09

---

### GAP-2 [high][immediate] `Split` retires the parent row, and the previous release redeployed beside the children replays the whole log with no refusal on any path

- **Where:** `event/projection/topology.go:140-142` (`parent.Forget`);
  `event/projection/identity.go:156-166` (`coarser()` is empty for `Whole()`);
  `event/projection/pass.go:83-100` (`unclaimed` therefore asks nothing);
  `docs/modules/en/projection.md:196-235` and
  `docs/release-notes/v0.1.0.md` (neither warns)

- **What:** `Split`'s fifth step removes the parent's checkpoint row. That row was
  the only evidence that the projection had ever run at the coarser share, and
  `unclaimed` — the one guard for "only the first start chooses the topology" —
  reads coarser ancestors only, of which a `Whole()` runner has none. So the
  ordinary rollback of a bad deploy (re-deploy release *n*, the spec with no
  `Partition:`) starts a whole-space runner that finds no row, resumes from the
  origin, and re-applies the entire log into the read model the two live children
  are still writing.

- **Why this severity:** driven end to end — 12 streams, 36 envelopes, drained
  whole, split, both children started and following, then the un-partitioned spec
  redeployed:

  ```
  the rolled-back unpartitioned release: phase=following err=<nil>
  it re-applied 36 of the 36 envelopes the split's children already hold
  ```

  `PhaseFollowing`, `Err` nil, `Ready` green, three rows all reporting a healthy
  watermark. This is byte for byte the failure §1.3.1 of
  `EVENTSOURCE_P4_USECASES.md:546-576` describes and `unclaimed` was written to
  refuse — *"Three rows, all reporting the same watermark, no error on any path,
  every dashboard green"* — reached through the door `Split` itself opens.

  §1.3.1 does state the finer direction is uncovered, and backlog `## P4` item 32
  records it at `[medium]`. Both are scoped to *"an operator hand-declaring a
  coarser cover instead of rebuilding, which is a documented-against action"* and
  to the example `orders#0.1` beside a live `orders#0.3`. Neither covers this
  case and neither justification survives it:
  - it is not a hand-declared cover, it is **the spec that was already in
    production one release ago**, redeployed unchanged;
  - it is not documented against anywhere. `grep -n "finer\|rollback"` over
    `docs/modules/en/projection.md`, `docs/ai/flows/FL-038-*.md` and
    `docs/release-notes/v0.1.0.md` returns nothing about it. The module page's
    only statement about going back is *"There is no merge"*, which reads as "the
    framework does not offer one", not as "attempting it by redeploy silently
    doubles your read model";
  - §1.3.1's own sentence — *"§1.3 already refuses a merge outright"* — is drift.
    Nothing refuses it. What is absent is a `Merge` **function**; the *state* an
    operator reaches without one is admitted in silence.

  Before S2 this state was unreachable: the `orders` row existed and any
  partitioned runner was refused by `unclaimed`. `Split`'s `Forget` is what makes
  it reachable, so it is this section's.

- **Why this timing:** S4 makes cutover and rollback between generations a
  first-class operation, and a rollback that lands on a retired row is the same
  hole one level up. Whatever mechanism closes this — a durable retirement marker
  written where `Forget` is issued, a `Checkpoints.Names(prefix)` the S4 `Observe`
  wants anyway, or an explicit refusal at the `Whole()` resume — is a store or a
  topology contract decision, and deciding it after S4 has built its own rollback
  on top means rewriting both.

- **Close criteria:**
  - [x] `TestTheReleaseThatRanBeforeASplitIsRefusedWhenItIsRedeployed` — drain →
        `Split` → both children following → the same spec deployed again, at two
        levels: the unpartitioned release after `orders` was split, and
        `orders#1.1` after it was split in turn. Both halt with `ErrHalted` +
        `ErrTopology`, the refusal names the record and the two ways out, and the
        application counts are unchanged from before the redeploy. Its control is
        the same release on a projection nothing ever split.
  - [x] The mechanism is a durable retirement record, written at the site that
        retires the parent (`topology.go:handOver`, the save immediately before
        `parent.Forget`) and read at `pass.go:unretired`. It is an ordinary
        checkpoint row at `<identity>#split` carrying the parent's own cursor, so
        no store contract widened and `Checkpoints` still has seven methods.
        `TestASplitOfAnAlreadyRetiredParentIsRefused` and
        `TestASplitOfANameWithNoRoomForItsRetirementIsRefused` pin the other two
        doors it opens.
  - [x] `docs/modules/en/projection.md` and `docs/modules/ru/projection.md` both
        state, in the split section, what redeploying the previous release does,
        what the record is called and the two ways out; `docs/release-notes/v0.1.0.md`
        says it in the `Split` note, in the paragraph a deploying team reads.
  - [x] Backlog `## P4` item 32 is amended: what is closed is every coarser share
        `Split` itself retired; what stays `[medium]` is a **hand-declared** finer
        row, and both halves of the old justification are corrected in place.
  - [x] `EVENTSOURCE_P4_USECASES.md` §1.3.1 now says a `Merge` **function** is what
        is refused and the state is not, and carries the retirement record as its
        own paragraph rather than leaving the redeploy uncovered.
- **Mutation:** the resume probe removed → both arms re-applied the whole log
  (`following`, 36 of 36 and 18 of 18). The record's write removed → the same, and
  the already-retired refusal falls back to `noParent`.
- **What the probe cost, found live rather than reasoned about.** The full tagged
  `eventpg` suite went red on three cases that counted the projection's `Load`s as
  a total and blocked on the second — `TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving`
  (both arms and its `NotWritten` control) and
  `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt` — because the
  retirement probe made each of them stall on the resume rather than on the
  settlement. `asked`'s counter is now keyed by the row the case is about, which
  is what those cases always meant, and all four arms are green against
  PostgreSQL 17.9.
- **Status:** closed 2026-09-09

---

## What was measured, and what held

Every number below was produced by running the command, not by reading the plan.

**The section's own checkpoint, re-run in full, exit 0 on every arm.**
`gofmt -l .` → 0. `go build ./...` and `go vet ./event/...` silent. The two
`-list` arms counted **6** and **5**. `go test -race -count=1 ./event/... ./scripts/`
green (`event 6.4s`, `eventmemory 1.5s`, `eventtest 4.3s`, `projection 1.5s`,
`scripts 26.3s`). `go vet -tags=integration ./event/eventpg/` silent. The census
`grep -c` arm counted **2**. `event-kernel-baseline` recorded **143** files;
`check-event-kernel: ok`; `event-kernel-moved` against
`.git/event_kernel_before_s2` reported exactly the nine files the plan lists, and
`ok`. `make check` green on all ten checks.

**Kernel boundary.** `git status --porcelain event/` → 12 modified + 11 untracked,
and `grep -E '^event/[a-z_]+\.go$|^event/eventmemory/'` over the changed set
returns **0**. No top-level `event/` file and no `eventmemory/` file moved, which
is §5.1's obligation and the plan's expectation.

**Surface.** `make api` regenerated `docs/api/surface.md`; the
`github.com/frostgrove/vv/event` section is byte-identical to
`.git/event_surface_before_p4`. `event/projection` gained exactly `Split` and
`SplitSpec` over S1's vocabulary — 2 new exported symbols, both in the contract.

**Live evidence, re-run.** `FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1
-tags=integration -run '^TestTheCheckpointStoreSatisfiesTheContract$' ./event/eventpg/`
→ **14 of 14 sections `passed`**, `topology` and `topology handoff` included,
`ok … 1.474s`. The census edit is a delivery, not a promise.

**Mutation, three re-run independently, three caught.**

| Mutation | Result |
|---|---|
| the `!above.Fresh()` refusal deleted (`topology.go`) | `TestASplitOverAnExistingChildRowIsRefused/a_row_at_orders#3.3` — *"splitting \"orders#1.1\" over a live \"orders#3.3\" answered \"orders#1.3\" and \"orders#3.3\""* |
| the higher child written with `Progress{At}` alone | `TestASplitWritesTwoChildrenAtTheParentsCursorInOneTransaction` — *"the child \"orders#3.3\" reports the watermark 0 where the parent reported 36"* |
| `topologySection`'s body returns at once (`sections_topology.go`) | `TestEveryCheckpointDefectIsReportedByItsOwnSection` — both topology defects *"reported \"passed\""* |

**Interleavings driven, not read.**

| Interleaving | Outcome |
|---|---|
| a partition-count change with events of one sequence in flight — `Split` committed from inside the parent's own `Save` hook, between apply and save | **handled.** Parent applied k0–k2 and halted loudly on `errRowRetired`; children resumed at the parent's committed cursor and delivered k2–k7. One page re-delivered, nothing dropped, nothing reordered. Matches the module page's *"a running parent whose row is split away finds it absent at its next save and halts"*. |
| a partitioned runner losing the fence to a second instance | **GAP-1.** Halts instead of taking turns. **Closed:** takes turns, over a partition and over a generation. |
| a partitioned `InUnit` runner whose unit rolls back | **GAP-1.** Adopts the coarse row; every later checkpoint lands there. **Closed:** every save carries its own row key. |
| the previous (un-partitioned) release redeployed after a `Split` | **GAP-2.** 36 of 36 re-applied, `PhaseFollowing`, no error. **Closed:** halts with `ErrTopology` naming the retirement record, at both levels. |
| two concurrent `Split`s of one parent | **handled by the store's fence**, and the mechanism is the one the new conformance section certifies: the loser's child `INSERT … ON CONFLICT (projection) DO NOTHING` at advance 1 (`eventpg/checkpoints.go:355-360`, PK on `projection`) writes zero rows and the whole unit rolls back with `ErrTopology`. |
| a claim expiring mid-batch, a parked sequence taking a later event, a cutover with a reader mid-query, a rebuild beside a live generation | **not reachable in S2** — `ErrClaimLost` and the park are S3, `Observe`/`Cutover` are S4. |

**Binding decisions.** [[D-128]] untouched (no reader change, no `(writer_xid,
position)` tuple). [[D-129]] held: `Split` copies `held.Cursor` byte for byte,
orders nothing, and `docs/api/surface.md` shows no `Position → Cursor` anywhere.
[[D-130]] held: `Split` opens no transaction, takes the caller's `Unit`, carries
no decision between two runs of the body, and reads its answer off what the body
reached rather than off what the unit returned (`topology.go:43-57`).
[[D-118]] held: no second durable-intent table — the retirement record closing
GAP-2 is a checkpoint row of the store the projection already records through,
written inside the caller's own transaction, and not an intent table. [[D-092]] held:
`grep 'go func\|^\s*go '` over the non-test files of `event/projection` returns
**0**, and `Split` runs on the caller's goroutine. [[D-126]] held: no isolation
level is named. **[[D-133]] was violated by GAP-1 and is now held** — see its Proven-by rows.

**Architecture, counted.** `topology.go` 207 lines, 9 functions, 2 exported
symbols, longest function `handOver` at 46 lines, maximum nesting 3 tabs.
`sections_topology.go` 119 lines, 4 functions. Package dependency budget
unchanged: `go list -deps ./event/projection` → `crud`, `errs`, `event`,
`runtime`, `utils` — the same five S1 had. No global mutable state
(`grep -nE '^var [a-z]' event/projection/*.go` excluding tests → none). No
`log.`, no `fmt.Print`, no `os.Getenv`, no `time.Sleep`, no `//nolint` in either
new file. `event/projection` holds exactly **14** non-test files, and both
`scripts/projection_test.go` guards (lines 141 and 178) were raised to 14 in
this section, as the plan's own correction required.

**Universality.** Every literal in the two new files traced to a source:
`MaxPartitions = 1024` is a published ceiling, named in three refusals;
`2166136261`/`16777619` are FNV-1a/32's standard constants, pinned against
`hash/fnv` by `partition_test.go:116`; `partitionSeparator = "."`,
`generationMark = "@"`, `partitionMark = "#"` are the wire format, each with a
refusal that keeps the rendering injective; `narrowestColumn = 255` in
`defects_checkpoints.go` is a deliberately-chosen defect width and is documented
as one. No fixture path, no layout-tuned regex, no `if id == "…"`, no shape
assumption, no threshold without a stated origin.

**Contract conformance.** `SplitSpec` and `Split` match the plan's declared
signatures exactly; the two additions the plan records as made in S2
(`inACallersTransaction` as step 0, and reading the answer off the body rather
than the unit) are both present and both driven. `Batch.Identity` and
`State.Identity` match the plan block. The four new eventtest defects the plan
names, plus the six that close previously-unguarded sections, are present;
`checkpointDefects` is 14, `checkpointInventory` is 14, `inventoried` is
untouched at 29, and the `undecorated` exemption holds `durability` alone with
its reason on the variable. No drift in either direction found.

**Testability.** Both new files are testable with the fakes already in the tree —
`Split` needs one recording `Checkpoints` and one `Unit`; `topologySection` needs
the suite's own factory. No fixture path is referenced from a non-test file.
