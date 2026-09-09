# EVENTSOURCE_P3 — S4 (the live proof) — GAPS

## Round 1 — remediation — 2026-09-09

**Both blocking findings are closed.** GAP-1 was reproduced first by a live case of its own — two
real `Projection` values of one name, and the read model ended holding `[s-1 s-4]` over a log of
four while the checkpoint stood at advance 2, highest 4, quarantined 0 — then fixed by making the
**cursor** the settlement's attribution test rather than the advance alone, and the contracts that
stated the wrong rule ([[D-133]], §UC-104, §3.4's failure table, the plan's D10b) were rewritten in
the same change. GAP-2's inert guard is unconditional now, and the mutation that implements
§UC-104's Must-not verbatim was re-driven and **fails** the case at both levels. The section's
checkpoint was re-run whole: `13` and `1` counted, both live passes green — `106.148s` and
`105.851s` — the benchmark executed, both unset-DSN arms refusing, and `event-kernel-moved` naming
exactly the two files the fix moved. Per-finding detail is under each heading below.

The four `[medium]`/`[low]` items stay in the backlog, untouched, per the delivery policy — except
§54, which is the same site as GAP-1 seen from another angle and is marked resolved there with what
its analysis got wrong.

## Round 1 — implementation reviewer — 2026-09-09

**Verdict: RED.** One `[critical][immediate]` and one `[high][immediate]`. The section's own
twelve cases are green, twice in a row, on PostgreSQL 17.9, and the two mutations the plan
records as its strongest were re-driven and both were caught. The gate is red anyway, because the
interleaving S4 exists to drive — two live instances of one name where one of them goes away —
**loses events permanently and silently**, and it was driven here with two real
`projection.Projection` values and no hand-written rows.

### What was run, and the numbers

| Check | Result |
|---|---|
| `go test -race -count=1 -tags=integration ./event/eventpg/...`, pass 1 | `ok … 105.706s` |
| the same, pass 2 (with `FROSTGROVE_EVENTPG_TEST_PSQL` set) | `ok … 105.369s` |
| `-list` count of the twelve named tests | **12** |
| `-list` count of `^BenchmarkStreamReplay$` | **1** |
| `BenchmarkStreamReplay -benchtime 1x` | `1 10638390 ns/op 1064 ns/event` (plan pasted 1168) |
| `TestTheReplayBenchmarkMeasuresTwoOrdersApart` own log | `1000 events in 1.014786ms (1015 ns/event), 100000 in 109.823032ms (1098 ns/event)` (plan pasted 1021 / 1028) |
| unset DSN, `go test` | fails, does not skip |
| unset DSN, benchmark | fails, does not skip |
| `FROSTGROVE_EVENTPG_TEST_PSQL` pointed at a database that does not exist | `TestAQuarantineIsEnvelopeGranular` **fails**, naming the query — the cross-check can fail |
| `./scripts/checks.sh event-kernel` | `ok` — nothing under `event/` outside `event/eventpg` moved |
| `make check` | ok on all ten arms |
| `gofmt -l event scripts` | silent |
| `go vet -tags=integration ./event/eventpg/...` | clean |
| `go test -race -count=1 ./event/...` | green over all four packages |
| S4 files | `projection_integration_test.go` 1109 / `projectioncase_integration_test.go` 425 / `router_integration_test.go` 191 / `rebuild_integration_test.go` 183 / `replay_integration_test.go` 145 lines; 12 `func Test`, 18 `t.Run`; 0 `t.Skip`, 0 `nolint`, 0 `TODO`; one `time.Sleep`, and it is a `pg_stat_activity` poll |
| goroutines in `event/projection` non-test files | `grep -n '^\s*go ' event/projection/*.go` → none; no `init()` |
| exactly-once creep | the four hits (`state.go:52`, `doc.go:24`, `spec.go:60`, `executor.go:75`) are about a halt being published once, a unit running the work once and a statement being issued once — none is a delivery promise |

### The mutations that were re-driven

| Mutation | Caught by |
|---|---|
| `eventpg/read.go` `deliverable` returns the whole page | `TestAProjectionDoesNotPassAPositionAWriterCouldStillCommit` — *"applied 1 rows while position 1 was still uncommitted"* — and the reviewer's own probe: *"the checkpoint reached highest 5 while position 4 was still uncommitted"* |
| `projection/pass.go` `claimed` applies before it claims | `TestTwoLiveInstancesOfOneNameOverOneSchema` — *"the handlers were called for **26** envelopes over a log of 24"*, reproducing [[D-133]] §3's recorded number exactly; and two `event/projection` unit cases |
| every page applied twice (`outsideAUnit`) | 5 of the 12 (`TestTwoLiveInstances…`, `TestTheTwoModes…`, `TestAProjectionResumes…`, `TestAProjectionDoesNotPass…`, `TestARouterRoutes…`) — **but not** `TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving`, see GAP-2 |
| the settled page re-applied on the settlement path (§UC-104's Must-not, verbatim) | **nothing.** Green across `go test ./event/...` and the whole live suite |

### The interleavings that were driven, and what each does

| Interleaving | Driven by | Outcome |
|---|---|---|
| two instances of one name over one checkpoint, both modes, gate-forced contention | `TestTwoLiveInstancesOfOneNameOverOneSchema`, run 3× under `-race` | handled: `InUnit` leaves the contended page in 1 row and calls handlers 24 times over a log of 24; `AfterApply` leaves it in 2. No loss |
| a crash between the handler's write and the advance | `TestTheTwoModesLeaveDifferentStateAtOneKillPoint` | handled: `AfterApply` re-delivers the page at `Attempt == 1`; `InUnit` takes the rows back |
| a restart **mid-batch** (`MaxRead 2`, log of 6, killed in the second page) | reviewer probe | handled: `AfterApply` tallies `m-3:2 m-4:2`, everything else 1; `InUnit` tallies everything 1. Nothing dropped; the window is exactly one page |
| a writer that draws a position and commits after a projection has already checkpointed above the events before it | reviewer probe (the projection **FOLLOWING** when the gap opens) | handled: for 2 s the checkpoint never reaches the held position and the read model does not grow; after the commit both arrive in position order |
| a rolled-back append leaving a burnt position, `AfterApply` | reviewer probe | handled: settled and passed, `highest` reaches the last committed position |
| two live instances where the winner's page is **shorter** and the winner then exits | **reviewer probe — see GAP-1** | **two events dropped, permanently and silently** |

---

### GAP-1 [critical][immediate] A settlement attributes another instance's row to its own save and resumes past events nobody applied

- **Where:** `event/projection/pass.go:400-401` (`settle`'s `case found.Advance == waiting.presented:`)
  and `pass.go:429-436` (`confirmed`, which sets `this.tracker` and calls `landed` but leaves
  `this.reader` at **this pass's own in-memory cursor**, unlike `overtaken` at `pass.go:463`
  which rebuilds from `found.Cursor`).
  Enshrined by `docs/ai/decisions/D-133-…:73` (*"at the advance this pass presented, and the save
  was merely unconfirmed | this pass's own save landed | continue from the in-memory cursor"*) and
  by `event/eventpg/projection_integration_test.go:838-907`, whose first arm **simulates** the
  landed save by having a second session (`blockingCheckpoint`, `:773-787`) commit a row at that
  advance carrying the cursor `held-by-another-session`, and calls the projection carrying on
  from its own cursor the correct outcome.
- **What:** when a pass's save leaves its outcome unread (`ErrUncertain`, or the caller's `Unit`
  answering an error after the inner `Save` returned nil), the bounded resolution reads the row
  and treats *"the row is at the advance I presented"* as proof that **my** save is what put it
  there. It is not proof. A second live instance of the same name — which [[D-133]] says every
  rolling deploy runs on purpose — reaches that same advance the moment this pass's unit rolls
  back and releases the row (or, at advance 1, its speculative tuple). Its cursor can be
  **behind** this pass's, because it read earlier or has a different `MaxRead`. `confirmed` then
  resumes from this pass's cursor, and every event between the two cursors is applied by nobody
  and is behind the checkpoint for good.
- **Why this severity:** driven live, twice, on PostgreSQL 17.9. With two real
  `projection.Projection` values of one name over one schema, both `InUnit`, a log of
  `g-1 g-2 g-3`, the old replica at `MaxRead 1` and the new one taking the whole page, the new
  replica's `Unit` answering an error once after the save inside it returned nil, and the old
  replica exiting after its save — which is exactly what a rolling deploy does:

  ```
  the old replica left {advance:1 highest:1 applied:1 quarantined:0} and the read model holds [g-1]
  after the settlement the checkpoint row is {advance:2 highest:4 applied:2 quarantined:0}
    and the read model holds [g-1 g-4]
  "g-2" reached no row of the read model while the checkpoint stands at highest 4:
    it is behind the cursor and is never delivered again
  ```

  Two committed events are lost. There is no error on any path, `Quarantined` is 0 — so
  `event/checkpoint.go:29-32`'s *"non-zero means that destination has holes"* is false — and
  `Progress.Highest = 4`, which [[D-128]] publishes as a completeness watermark and S5 is about to
  freeze as the contract a third-party `Checkpoints` implementer satisfies. It breaks §INV-073
  (*a projection never skips an event it could not apply*) and §UC-105's *"a rollback leaves
  neither"* — the rollback left neither, and the checkpoint then advanced past the page anyway.
  The same thing was reproduced independently with the row written by hand rather than by a second
  `Projection`, so the mechanism is the store's own fence and not a peculiarity of the harness.
- **Why this timing:** S5 freezes `event/checkpoint.go`'s `Progress.Highest` wording as a
  published contract and re-baselines `check-event-kernel` over `event/projection/pass.go`. After
  that, changing where a settlement resumes from means reopening a frozen kernel manifest and a
  published checkpoint contract. It is also the one claim S4 exists to make: the section reports
  the interleavings as proved.
- **Close criteria:**
  - [x] The settlement resumes from `found.Cursor` on the `found.Advance == waiting.presented`
        path as well — the row's cursor is the resume authority ([[D-133]] already says so for the
        contended branch) — **or** the row carries a writer identity that makes the attribution
        provable rather than assumed. Whichever is chosen is written into [[D-133]]'s table, which
        currently states the unprovable rule.
  - [x] A live case in `event/eventpg` drives two real `Projection` values of one name where the
        winner's page is shorter and the winner then stops, and asserts out of the database that
        every event of the log reached the read model. It fails on today's code and passes after
        the change.
  - [x] `TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving`'s first arm no longer presents
        a cursor no log minted (`held-by-another-session`) as the shape of "my save landed", or
        says in its own words that the framework cannot tell the two apart and what it does about
        it.
  - [x] The whole live suite green twice in a row after the change.
- **Status:** closed, 2026-09-09

**How it was closed.** The cursor decides, not a writer identity — the first of the two options,
and the cheaper one: the fence admits **one** writer at each advance and this pass presented its
own cursor beside it, so a row at that advance carrying any other cursor is provably somebody
else's. `event/projection/pass.go` grew `anothers(found, cause)`, which is `fenced(cause) ||
found.Cursor != this.cursor`, and `settle`'s first arm reads it — so that row is `overtaken` like
any other lost fence: the row is adopted, the reader rebuilt from **its** cursor, the page dropped,
the loop backs off. The other direction stays unprovable and does not need proving: a second
instance that read the same page presents the same cursor, and both resume at the same point.

**Reproduced before it was fixed, at both levels.**

- `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt` (`event/eventpg`, live, new) — two
  real `Projection` values of one name over one schema, the old replica at `MaxRead 1` and the new
  one taking the whole log, driven in the one order that produces the defect: the new replica
  claims advance 1 and its `Unit` takes the claim back, the old replica takes that advance with its
  own one-event cursor and stops, and the new replica's settling `Load` is held at a gate until
  that row is committed. Against the unfixed code, verbatim:

  ```
  the read model holds map[s-1:1 s-4:1] while the checkpoint stands at advance 2, highest 4
    and quarantined 0: "s-2" is behind the cursor and is never delivered again
  ```

  which is the review's own numbers reproduced by the case rather than by a probe. Green after the
  change. **Controls:** the two cursors must differ, the new replica's first page must have been
  the whole log, and exactly one save must have been issued before the settlement — so a run in
  which the two instances read the same page fails rather than passes; and one instance alone over
  the same log applies every event once.
- `TestASettlementResumesFromTheRowsCursorAndNeverFromItsOwn` (`event/projection`, no database,
  new) — the same rule in `make unit`, two arms told apart by the cursor alone: *"the settled
  projection applied [four] where the row it settled against carries the cursor after [one]"*
  against the unfixed code, and the second arm — the row carrying **this pass's own** cursor — is
  the control that fails if every settlement is read as contention.

**The contracts that stated the wrong rule, changed with the code**: [[D-133]]'s table (rows three
and four, which were one row), plus a new entry under *What it forbids*; §UC-104's **Then** and
**Must not** with a `Changed` line; §3.4's failure table, both the `ErrConflict` and the
`ErrUncertain` rows — the first of which still said *halt* after S3 had already changed it; and
the plan's D10b table and S4 section. Backlog `## P3` §54 raised this site at `[medium]` and
concluded *"nothing is lost"*; it is marked resolved there with what it got wrong.

**The fence moved and was re-recorded**: the fix reached `event/projection/pass.go` and
`event/projection/unit_test.go`, both inside `check-event-kernel`'s manifest.
`.git/event_kernel_before_s4` was recorded first, the baseline regenerated, and
`event-kernel-moved` names exactly those two paths.

---

### GAP-2 [high][immediate] §UC-104's "must not re-apply the page" is guarded by an assertion that cannot fail, and the mutation implementing that Must-not survives every test in the repository

- **Where:** `event/eventpg/projection_integration_test.go:899-905`

  ```go
  if got := rows.tally(t); len(got) != len(payloads) {
      for _, payload := range payloads {
          if got[payload] != 1 { t.Fatalf("… the page was applied once and never re-applied …") }
      }
  }
  ```

- **What:** the body only runs when the tally holds a **different number of distinct payloads**
  than the log. A page applied twice gives `{u-1:2, u-2:2}` — two distinct payloads over a log of
  two — so the guard never fires and the assertion the case advertises is never made. The guard
  catches a *missing* payload and is inert for the *duplicated* one, which is the only shape
  §UC-104's Must-not is about.
- **Why this severity:** §UC-104 says in as many words *"must not re-apply the page on the
  assumption that the save failed"*, and the coverage matrix names this test as what proves it.
  Mutating `settle` to do exactly that —

  ```go
  case found.Advance == waiting.presented:
      _ = this.applyPage(ctx, this.page, this.attempt)   // re-apply on the assumption the save failed
      return this.confirmed(waiting, found, tracker)
  ```

  — leaves `go test ./event/...` green **and** the whole live `./event/eventpg` suite green
  (`ok … 54.873s`), all three arms of this very case included. A stated Must-not with no test
  that can fail is phase 1's failure mode, and this is the case the plan's own checkpoint counts
  for UC-104. (The same mutation applied indiscriminately to every page *is* caught, by five other
  cases — so the hole is specific to the settlement path, which is the path UC-104 names.)
- **Why this timing:** it is an assertion in a file this section owns, it is the only guard for
  the Must-not, and S5 closes the phase. Leaving it means UC-104 ships reported-proved and
  unproved, and GAP-1's fix would land with no case able to tell whether the settlement
  re-delivers.
- **Close criteria:**
  - [x] The tally assertion is unconditional: every payload is in exactly one row, in both arms.
  - [x] The mutation above is re-driven and the case **fails** with it, and the section's report
        says so.
- **Status:** closed, 2026-09-09

**How it was closed.** The guard is gone; the tally runs unconditionally in both arms. Two more
things changed with it, because the assertion was only half the hole:

- the arm that stands for the landed save now commits a row carrying **the cursor that save
  presented**, read off the recording checkpoint store (`asked.cursor`) and written into the
  blocking session's own row before it commits (`landedSave`). `held-by-another-session` was a
  cursor no log ever minted, and under GAP-1's fix it is not the shape of "my save landed" at all
  — it is another instance's row;
- the case says in its own words what the framework can and cannot prove: a matching advance
  beside the presented cursor is this pass's save *or* a second instance's indistinguishable one,
  and both resume at the same point, so nothing is decided by telling them apart.

**Re-driven.** With §UC-104's Must-not implemented verbatim —
`case found.Advance == waiting.presented: _ = this.applyPage(ctx, this.page, this.attempt)` — the
case now fails, live:

```
--- FAIL: TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving/a_row_at_the_advance_this_pass
          _presented,_carrying_its_cursor,_is_the_save_having_landed_and_the_loop_carries_on
    the read model holds map[u-1:2 u-2:2] where the page was applied once and never re-applied
      on the assumption that the save failed
```

`{u-1:2, u-2:2}` is exactly the tally the old guard let through. The same mutation is also caught
without a database, by `TestASettlementResumesFromTheRowsCursorAndNeverFromItsOwn`'s control arm
(*"applied [one two three four]"*), so the Must-not is pinned in `make unit` as well as live.

---

### Deferred to the backlog (recorded, not fixed)

Four `[medium]`/`[low]` items were raised and appended to
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P3` as §61–§64, per the 2026-09-08 policy:

- §61 `[medium]` — `stopping()` reads a handler's own `context.DeadlineExceeded` as a shutdown and
  returns it from `Run`.
- §62 `[low]` — the plan's S4 report says the row counts are read "over a pool that is neither the
  projection's nor the handler's"; `destination.read` uses `projectionCase.pool`, which is both.
- §63 `[low]` — a lost fence publishes `ErrOvertaken` to the `Observer` only on the first turn.
- §64 `[low]` — [[D-128]] cites `event/projection/pass.go:220` for `Progress.Highest`; the line is
  `presentSave` at `pass.go:295-305`.
