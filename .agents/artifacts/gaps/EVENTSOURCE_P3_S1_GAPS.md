# EVENTSOURCE_P3 — S1 (the kernel additions, the door, and the baseline mechanism) — GAPS

## Round 1 — econv code reviewer — 2026-09-08

Reviewed against [`EVENTSOURCE_P3_PLAN.md`](../plans/EVENTSOURCE_P3_PLAN.md) §S1 and
§"Contracts before code", [`EVENTSOURCE_P3_USECASES.md`](../usecases/EVENTSOURCE_P3_USECASES.md)
§1, §3.1, UC-099/101/102/103/111/129 and INV-066/068/069/076/078/082, and the code as it
stands. Everything below was executed, not read.

**One `[high][immediate]` was open. It is closed in round 2 below, and the
section is green.**

---

### GAP-1 [high][immediate] `Tracker.Load` re-seats the fence downwards, so `Forget` + `Load` restarts a projection at the origin with no error on any path

- **Where:** `event/checkpoint.go:113-123` (`Tracker.Load`), `event/checkpoint.go:143-163`
  (`Tracker.admit`), `event/checkpoint.go:188-194` (`Tracker.Forget` and the comment above it).

- **What:** `Load` ends with `this.advance, this.loaded = held.Advance, true`, unconditionally,
  and `admit` never compares the store's answer with the advance this tracker already holds.
  So the fence is whatever the last `Load` answered, in either direction. Two consequences,
  both driven and both reproduced:

  1. **A `Forget` followed by a `Load` on the same live tracker resets the fence to zero, and
     the next `Save` lands at advance 1** — a fresh row, and a walk that resumes from the
     origin. Run against a `Checkpoints` implementing exactly the fence §3.1 prescribes
     (`/tmp/reviewfence`, a map store with the `Advance-1`/`Advance==1` admission rule):

     ```
     row after five saves: {Projection:accounts.balances Cursor:5 Advance:5 Progress:{Highest:500 ...}}
     Load after Forget answered {Projection: Cursor: Advance:0 ...} err=<nil> Fresh=true
     the save after that answered <nil>; the row is now {... Cursor:1 Advance:1 ...}
     ```

     `Forget`'s own comment two lines above says the opposite in as many words: *"It does not
     reset the fence … resetting the fence would turn it into a silent restart at the origin."*
     The method does not reset it; `Load` does, and `Load` is on the same value.

  2. **The loser of a fenced save re-loads and its next save lands**, so two processes take
     turns over one name:

     ```
     first=<nil>  second=…: conflict  row={Cursor:a Advance:1}
     the loser reloaded {Cursor:a Advance:1} err=<nil>
     the loser's second save answered <nil>; the row is {Cursor:c Advance:2}
     ```

     §INV-069's own words: *"A refused save … is never retried at a re-read advance, which
     would make two processes take turns."* The door permits precisely the re-read.

  Control run beside both, so the fence is not simply absent: eight trackers loaded at one
  advance and saving concurrently under `-race` give **1 winner and 7 `ErrConflict`**.

- **Why this severity:** §UC-102's Must-not is *"The projection must not silently create a new
  row at advance 1 and continue, which would leave the read model holding events no checkpoint
  accounts for"*, and §UC-101's is *"it must not restart from the origin — that re-applies the
  whole log against a live read model."* Both are reachable through the public door, with no
  error, no refusal and no observer transition, on a call order the shipped loop is specified
  to use: D10b makes a second `Tracker.Load` the **designed** resolution of an unconfirmed
  save. Concretely — an `AfterApply` projection at advance 5 takes an `ErrUncertain` from its
  save, performs D10b's resolution `Load`, and finds the row gone because an operator retired
  the name in the same minute; the tracker silently re-seats to 0 and the next pass writes
  advance 1 over a live read model. The whole reason `Track` and `*Tracker` are exported
  rather than kept for `event/projection` is §INV-082 — *"a second consumer inherits the
  re-checks rather than re-deriving them"* — and what a second consumer inherits today is a
  fence it can reset by accident.

- **Why this timing:** `*Tracker` is the public contract S2's `RunCheckpoints` and S3's
  `pass.go` are about to be written against, and D10b's resolution table is already a
  re-implementation in the consumer of a rule the door could hold once. If the door does not
  hold it, every consumer of `Checkpoints` must re-derive it, and the third-party checkpoint
  store §UC-123 invites will be certified by a suite that drives a door which does not enforce
  the property its own comment advertises. Changing `Tracker` after S2 and S3 are written
  against it is a kernel-signature change under a re-baselined manifest.

- **Close criteria:**
  - [x] `Tracker.Load` refuses, as `ErrWrongStore`, an answer whose `Advance` is below one this
        tracker has already loaded or saved — or the code states in one place why a regression
        is admissible, and `Forget`'s comment stops asserting a guarantee the type does not hold.
  - [x] A case in `event/tracker_test.go` drives `Load → Save ×N → Forget → Load → Save` and
        asserts the last save does not land at advance 1 and the row is not created.
  - [x] A case drives two trackers over one store, lets one lose the fence, re-loads it, and
        asserts the loser's second save is refused rather than landing.
  - [x] A control keeps the legitimate re-load: a first `Load` on a fresh tracker over a store
        holding no row still answers `Fresh()` and still permits advance 1.
  - [x] `go test -race -count=1 ./event/...` green, and
        `./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s1 '<S1 allowed set>' …`
        still reports only S1's allowed paths.
  - **Status:** closed — round 2, 2026-09-08

---

## What was counted, run and broken — the numbers behind the verdict

**Contract conformance, both directions.** `go doc -all ./event` lists exactly the eleven
symbols S1's plan declared and no twelfth: `Progress` (4 fields), `Checkpoint` (4 fields),
`Checkpoint.Fresh`, `CheckpointCapabilities` (2 fields), `Checkpoints` (7 methods), `Track`,
`Tracker` (7 methods: `Projection`, `Capabilities`, `Backing`, `Transaction`, `Load`, `Save`,
`Forget`), `MaxCursorBytes`, `Fact.Family`, `Fact.Read`. No silent extra public surface; no
declared symbol missing. `checkPage`'s signature change is unexported, as the plan said.

**Metrics, counted.** `event/checkpoint.go` 194 lines; longest function `admit` 21 lines; max
nesting depth 2; every refusal a guard clause with an early return; zero flag parameters.
`event/fact.go` 257 lines (+36); `event/reader.go` 100 (+22); `event/bounds.go` 36 (+7).
`go list -deps ./event` reaches exactly `crud`, `errs`, `utils` and the standard library —
unchanged from HEAD, so the vocabulary's cost did not move.

**Microkernel purity.** `grep -rn 'eventpg|eventmemory|eventtest|projection' event/checkpoint.go
event/reader.go event/fact.go event/bounds.go` → **0** matches on a package path; the only hits
are the field/parameter name `projection`. `Checkpoints` is a new extension point with a
declared contract (the 10-line interface comment), a registration mechanism (`Track`), a
failure policy (`refuse(err, door)` at the read/append doors, no new sentinel) and a
compatibility note (`Capabilities` carries `Transactions` and `Persistence`). The kernel works
with zero implementations registered — S1 ships none, and `make unit` is green.

**Lifecycle.** `grep -n 'go func|^func init|^var |log\.|fmt\.Print|os\.Getenv|time\.Now|rand\.'`
over the four changed kernel files → one match, `this.log.ReadAll` (a false positive on `log.`).
No goroutine, no `init`, no package-level mutable state, no clock, no env read, no logger.
`TestMerelyImportingTheEventExtensionStartsNothing` passes in `make unit`.

**Non-monotone visibility.** Nothing in S1 assumes a gap-free ordered global stream.
`checkPage`'s ascending rule is per page and gaps are explicitly normal; `Progress.Highest` is
never read by the door; there is no `Position → Cursor` anywhere; `Cursor` is compared only
against `""`, never ordered and never arithmetic. The two new `checkPage` arms cannot refuse a
legitimate `eventpg` answer — `mintCursor` is fixed-width 58 bytes and never empty
(`event/eventpg/cursor.go:39-46`) — which the live run confirms rather than assumes.

**Live evidence.**
`FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' go test -race
-count=1 -tags=integration ./event/eventpg/...` → `ok … 79.289s`. The plan's pasted
`78.728s` reproduces.

**The pasted S1 checkpoint output is real.** Re-ran the last five arms verbatim:
`event-kernel-baseline: 108 files recorded`, `check-event-kernel: ok`, the moved set is the
same eight paths in the same order, `event-kernel-moved: ok`, exit 0. Both count arms
(`= 12` over `./event/`, `= 3` over `./scripts/`) pass. `go build ./...`, `go vet
./event/... ./scripts/...`, `gofmt -l event scripts`, `make unit` (no `FAIL`), `make check`
(`check-event-kernel: ok`) all green.

**Kernel boundary.** `git status --porcelain event/` lists eight paths and every one is on the
plan's allowed list; the four required paths (`checkpoint.go`, `bounds.go`, `reader.go`,
`fact.go`) all moved. The predecessor in `.git/event_kernel_before_s1` was independently
verified rather than trusted: all **104** recorded lines match `git show HEAD:<path> |
sha256sum` with **0** mismatches, and `git ls-tree -r HEAD event | grep -v eventpg` is also
104 files. The baseline move is recorded in three places — the plan's own path list,
`scripts/checks.sh`'s `event_kernel_baseline` comment, and the FL-037 / Roadmap edits.

**The two most important tests, broken deliberately.** Every mutation was caught; each was
restored and the tree re-verified byte-identical by sha256 afterwards.

| Mutation | Result |
|---|---|
| `admit`'s name check removed (`held.Projection != this.projection`) | **red** — `TestTheDoorRefusesAnAnswerAboutAnotherName`: "answered `<nil>`, and a consumer that took that row would skip every event between the two positions" |
| `checkPage`'s empty-cursor-beside-a-page arm removed | **red** — `TestAReaderRefusesAnEmptyCursorBesideANonEmptyPage` |
| `admit`'s presence-total arm removed | **red** — `TestPresenceIsTotalAndAnEmptyCursorIsNeverAResumePoint/a_stored_row_at_a_non-zero_advance_with_no_cursor` |
| `admit`'s `MaxCursorBytes` arm removed | **red** — but see backlog `## P3` §39: it goes red through the fixture log's own `ErrCursor`, not through the door |
| `Tracker.Save`'s save-before-a-load refusal removed | **red** — `TestASaveBeforeALoadIsRefused` |
| `Fact.Read`'s family check removed | **red** — `TestFactReadDecodesThroughItsOwnChainAndRefusesTheFourHistoryClasses` |
| `Fact.Read` copies the payload (`bytes.Clone`) | **red** — `TestTheValueFactReadAnswersMayAliasThePayload` |
| `event_kernel_moved`'s empty-moved-set refusal removed | **red** — the vacuity case |
| the moved set computed in one direction (`comm -13`) | **red** — the removal half of the fifth case |
| `check_event_kernel` gutted to `echo ok` | **red** — three of five cases |

**Interleavings.** S1 ships no `Checkpoints` implementation and no projector, so three of the
four asked-for interleavings (a writer committing behind a passed position, a crash between a
projection write and its checkpoint, a restart mid-batch) are not constructible until S2/S3 and
are the S2/S4 checkpoints' to prove. The one that is constructible now — **two projectors
sharing one checkpoint** — was built and run under `-race`: eight trackers at one advance give
one winner and seven `ErrConflict`; the loser's *recovery* is GAP-1.

**Exactly-once creep.** `grep -rni 'exactly.once' event/ scripts/` over the new code → zero
claims. The only hits under `docs/` are the existing refusals in `D-118`, `FL-035` and `FL-036`.

**Already-recorded backlog entries this section closes or confirms.** `## P3` §31 (`*Tracker`
exported with no stated concurrency rule) is **closed** — `event/checkpoint.go:86-87` states
it. §33 (`bounds.go`'s header count) is **closed** — the header now says five factors and names
`MaxCursorBytes` as the seventh constant. §22 (`find event -type f` picks up gitignored
artefacts) **reproduces**: `touch event/coverage.out` makes `./scripts/checks.sh event-kernel`
exit 1 and print "a second store is written with zero diffs to the vocabulary", which is not
what happened. It stays `[medium]` and stays open.

**Deferred this round** — written to
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P3` §36–§44 and left alone.

---

## Round 2 — GAP-1 closed — 2026-09-08

### The reproduction, before the fix

`TestALoadNeverReSeatsAFenceThisTrackerEstablished` was written first and run against the
unchanged door. Four of its six subtests were red, and the two that were not are the controls
that must stay green:

```
--- FAIL: TestALoadNeverReSeatsAFenceThisTrackerEstablished
    --- FAIL: .../a_forgotten_row_is_not_a_fresh_start_for_the_tracker_that_forgot_it
        a load over the row this tracker forgot answered <nil>, and a tracker that took the
        absence would write a fresh row at advance 1 and resume the walk from the origin
    --- FAIL: .../the_loser_of_a_fenced_save_does_not_re-read_the_winner's_advance
        the loser re-read the row and was answered <nil>, and a save at the advance it read is
        what makes two processes take turns over one name
    --- FAIL: .../a_save_over_one_the_store_did_not_confirm_never_reaches_the_store
        a second save over an unresolved one answered <nil>, and either advance it presents is
        a guess about a row nobody read
    --- FAIL: .../a_save_this_tracker_did_not_commit_may_be_rolled_back_under_it
        a save the store committed itself: a row that went back under a tracker whose save the
        store committed answered <nil>
```

The first two are the finding's own two reproductions, driven through the public door against
`recordingCheckpoints`, which implements exactly the §3.1 fence. The third and fourth are the
two the finding's fix implies and did not name.

### What changed, and why the contract moved rather than only the code

**`Tracker` holds a window, not the number its last `Load` happened to answer.** `floor` is the
lowest advance the row can still be at; `advance` is the fence a save presents one above. A
`Load` is admitted only when `floor <= Advance <= advance` (`advance + 1` while a save is
unresolved), and a tracker that has never loaded has established nothing and takes whatever the
store holds — a process that restarts is how a projection ordinarily begins. That is
`event/checkpoint.go`'s `Tracker.established`, the fifth re-check in `admit`, `ErrWrongStore`,
running after the other four because they name a defect an operator reads off the row and this
one names a row that moved while a tracker was live.

**The two ends part for a reason the door can state.** A save the tracker watched a store commit
for itself cannot go back, so the floor rises with it. A save the tracker did **not** commit can
— an unresolved one may not have landed, and one staged in the caller's transaction may still
roll back — and both are exactly D10b's reload. The tracker tells them apart by asking the store
the one question `Transaction` exists to answer, after a save that returned nil: a valid
authority means the row is staged in a transaction this tracker does not commit, so the floor
stays. It issues no statement (§3.1), and erring in the cautious direction only widens the
window. Without it, the InUnit rollback arm of D10b is unreachable — mutation 5 below is that
measurement.

**`Tracker.Save` refuses a save over one the store never confirmed.** Either advance such a save
presents is a guess about a row nobody read; one `Load` settles it. This is §UC-104's "must not
re-save blindly" held at the door instead of re-derived by every consumer.

**What D10b keeps.** Its "anything else → halt" row is now the door's, so a consumer that does
not implement it cannot silently restart at the origin or take turns with a second writer
(§INV-082). Its first two rows stay the consumer's, because "did the handler's writes land" is
not a question about the row and the door cannot answer it: the door admits **both** advances an
unconfirmed save could have left and the table decides continue / re-apply / halt per mode.
`EVENTSOURCE_P3_PLAN.md` §D10b, §"What `Tracker.Load` re-checks" (item 6 and the paragraph under
it), §"What `Tracker.Save` does" (the guard and the four-row table of what an answer moves) and
§"What `Tracker.Forget` does not do" carry all of it.

**`Forget`'s comment now describes the type.** It said the fence is not reset because that would
be a silent restart at the origin; the load after it did the resetting. Both halves are refusals
now, and the comment names both.

### The fix broken deliberately, one arm at a time

Each mutation was applied to `event/checkpoint.go` alone, run, and reverted; the file was
confirmed byte-identical afterwards with `cmp`.

| Mutation | Result |
|---|---|
| the absence arm of `admit` no longer compares against the window | **red** — `a forgotten row is not a fresh start for the tracker that forgot it` |
| the present arm of `admit` no longer compares against the window | **red** — `the loser of a fenced save…` and `a save this tracker did not commit…` |
| the `unresolved` guard in `Save` disabled | **red** — `a save over one the store did not confirm never reaches the store` |
| `autocommits` hard-wired false, so no save raises the floor | **red** — `a forgotten row…` and `a save this tracker did not commit…` |
| `autocommits` hard-wired true, so a staged save raises it too | **red** — `a save this tracker did not commit may be rolled back under it` |
| an unconfirmed save no longer opens the window | **red** — `a save the store did not confirm is settled by a load, either way` and the blind-re-save case |

The last two are the pair that keeps the window from being always shut or always open: with
`autocommits` constant in either direction one of the two legitimate re-loads dies.

### The section's checkpoint, re-run whole

`TestALoadNeverReSeatsAFenceThisTrackerEstablished` joins the counted set, `12 → 13`.
Exit 0, output pasted into the plan:

```
ok  	github.com/frostgrove/vv/event	5.906s
ok  	github.com/frostgrove/vv/event/eventmemory	1.249s
ok  	github.com/frostgrove/vv/event/eventtest	2.527s
ok  	github.com/frostgrove/vv/scripts	1.171s
event-kernel-baseline: 108 files recorded in scripts/event_kernel.sha256
check-event-kernel: ok
the files under event/ this section moved:
  event/bounds.go
  event/checkpoint.go
  event/checkpoint_test.go
  event/fact.go
  event/fact_test.go
  event/reader.go
  event/reader_test.go
  event/tracker_test.go
event-kernel-moved: ok
PLAN S1 CHECKPOINT EXIT=0
```

Beside it: `go build ./...`, `go vet ./event/... ./scripts/...`, `gofmt -l .` silent over the
whole tree, `make unit` and `make vet` with no `FAIL`, and `make check` with `check-event-kernel:
ok` at the re-recorded manifest. The kernel moved only S1's eight allowed paths, so the addition
is inside the baseline S1 already declared and moves it no further.

**Live, twice in a row, because the door is new and the store under it is not:**

```
FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1 -tags=integration ./event/eventpg/...
ok  	github.com/frostgrove/vv/event/eventpg	79.142s
ok  	github.com/frostgrove/vv/event/eventpg	78.692s
```

`make api` regenerated `docs/api/surface.md`, which had been stale since S1: the diff is exactly
S1's five declared additions — `Checkpoint`, `CheckpointCapabilities`, `Checkpoints`, `Progress`,
`Tracker`/`Track` — and **nothing from this fix**, because `floor`, `unresolved`, `established`
and `autocommits` are all unexported. The public surface is where the plan said it would be.

**One environment note, because it cost time and the plan predicted it.** This shell's `grep` is
the `ugrep` wrapper whose `-qv` exit status is inverted (`printf 'a\nb\n' | grep -qv '^a$'` → 1;
`/usr/bin/grep` → 0), so the trailing `! … | grep -qvE …` arm of the re-verification command
reports the opposite of what it means here. Run with `/usr/bin/grep` it is exit 0. That arm is
also vacuous as written against `git diff HEAD`, because `scripts/event_kernel.sha256` is still
**untracked** — the diff is empty. The check that actually reads the moved set is
`./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s1 …`, run above, and the same
question asked directly of the recorded predecessor lists the eight allowed paths and nothing
else.
