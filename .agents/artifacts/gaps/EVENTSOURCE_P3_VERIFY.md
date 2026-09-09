# EVENTSOURCE P3 — the verification gate

**Ran 2026-09-09, against the working tree as it stands, PostgreSQL 17.9 at
`postgres://vv:vv@localhost:55432/vv`.**

Every blocking finding in `EVENTSOURCE_P3_S{1..5}_GAPS.md` marked closed was re-checked by
**constructing the input the finding named and running it**, not by reading the closure note.
The interleavings were driven from a program outside this repository — `/tmp/p3verify`, its own
Go module with `replace github.com/frostgrove/vv => <this checkout>` — so no test helper of the
code under test is trusted. Where the finding's own claim was that a fix discriminates, the fix
was mutated back to its pre-fix form and the case re-run; every mutation was reverted and
`./scripts/checks.sh event-kernel` was green afterwards, with `diff -q` against a byte copy
taken before the mutation.

**Verdict: green.** No `[critical]` or `[high]` survives. One new `[medium]` is raised and
recorded (`EVENTSOURCE_BACKLOG.md` `## P3` §76). Nothing was fixed here.

---

## 1. Every blocking finding, re-checked by driving its input

| Finding | Claimed | Input constructed and run | Result |
|---|---|---|---|
| **S1 GAP-1** `[high]` `Tracker.Load` re-seats the fence downwards | closed | `Load → Save ×5 → Forget → Load → Save`, and a loser re-loading after a fenced save, both over `eventmemory.Checkpoints` from outside the repo | **CLOSED, verified** |
| **S2 GAP-1** `[high]` a fenced save resurrects a row a `Forget` removed | closed | the race driven in `psql` against the DDL the golden renders, both the shipped statement and the pre-fix one | **CLOSED, verified, and it discriminates** |
| **S2 GAP-2** `[high]` `RunCheckpoints` enforces an undeclared microsecond round trip | closed | a coarse-grained checkpoint store run through `eventtest.RunCheckpoints` at four grains from outside the repo | **CLOSED, verified, with a control** |
| **S2 GAP-3** `[high]` `Load` answers a half-absent checkpoint inside a unit that staged a `Forget` | closed | the staged `Forget`, then the raw `Load` and the kernel door's `Load`, inside the same transaction | **CLOSED, verified** |
| **S3 GAP-1** `[high]` the presented advance is computed twice | closed | a caller's own retry loop around the transaction, so `Spec.Unit` runs the work twice in one pass | **CLOSED, verified** |
| **S3 GAP-2** `[high]` a closed `Spec.Wake` is an unbounded read loop | closed | `Idle = 1h`, `Wake` closed after the first page, log reads counted over 200 ms, plus an open-channel control | **CLOSED, verified** |
| **S3 GAP-3** `[high]` the two-instance case is in no section's Tests list | closed | the live case run individually; the claim-order mutation re-driven | **CLOSED, verified, and it discriminates** |
| **S4 GAP-1** `[critical]` a settlement attributes another instance's row to its own save | closed | the shorter-winner-leaves interleaving rebuilt from scratch outside the repo, over a transactional read model, with the fix mutated back | **CLOSED, verified, and it discriminates** |
| **S4 GAP-2** `[high]` §UC-104's Must-not is guarded by an assertion that cannot fail | closed | §UC-104's Must-not implemented verbatim in `settle`, live suite re-run | **CLOSED, verified, and it discriminates** |
| **S5 GAP-1** `[high]` the exactly-once walk exempts any promise after a negation | closed | the finding's own driven sentence re-written into both module pages | **CLOSED, verified in both languages** |

Detail per finding follows.

### S1 GAP-1 — the fence cannot be re-seated downwards

Driven from `/tmp/p3verify`, verbatim:

```
[S1-1a] after five saves the row is advance=5 cursor="c-5"
[S1-1a] Load after Forget answered advance=0 fresh=true err=event: this store is not the one
  this value was minted over, or it did not state its own bounds: "accounts.balances" is fenced
  between advance 5 and 5 and this checkpoint store answered 0, and a row that moved under a
  live tracker is not one it can go on writing
[S1-1a] the save after that presented advance=6 and answered event: the stream is not at the
  version this append was decided at: conflict; the row is advance=0 cursor=""
[S1-1a] VERDICT: restart-at-the-origin reachable = false

[S1-1b] first=<nil> second=…: conflict
[S1-1b] the loser re-loaded advance=0 err=… "orders" is fenced between advance 0 and 0 and this
  checkpoint store answered 1 …
[S1-1b] the loser's second save answered …: conflict; the row is advance=1 cursor="a"
[S1-1b] VERDICT: take-turns through one tracker reachable = false
[S1-1b] CONTROL: eight trackers at one advance gave 1 winner(s) and 7 ErrConflict
```

Both halves of the finding are refused now, and the control shows the fence is not simply
absent. The finding's original numbers — the row coming back at advance 1 with cursor `z`, and
the loser's second save landing at advance 2 — are unreachable.

### S2 GAP-1 — the `Forget` race, driven in `psql`

Scratch schema carrying the checkpoints DDL, a row at advance 5, session A holding
`BEGIN; DELETE …; pg_sleep(3); COMMIT`, session B issuing the **shipped** save at advance 6 one
second in:

```
UPDATE 0
Time: 1999.319 ms (00:01.999)
-- the table after: (0 rows)
```

The same interleaving against the **pre-fix** statement shape
(`INSERT … WHERE $3 = 1 OR EXISTS (…) ON CONFLICT DO UPDATE WHERE advance = $3 - 1`):

```
INSERT 0 1
-- the table after:  orders.v1 | 06 | 6
```

So the fix is real and the check discriminates. **Control:** the same shipped save with no
concurrent `Forget` answers `UPDATE 1` and leaves `orders.v1 | 06 | 6`.

### S2 GAP-2 — a coarse instant is certified only when the factory declares its grain

A checkpoint store correct in every rule the contract states, differing only in the grain of its
instant column, run through `eventtest.RunCheckpoints` from outside the repo:

| Arm | Outcome |
|---|---|
| exact, nothing declared (**control**) | certified — twelve sections |
| millisecond, `Instant` declared | certified |
| second, `Instant` declared | certified |
| millisecond, `Instant` **not** declared | **not certified** — nine sections |

The obligation now sits on `event.Progress.At` ("a store keeps it at whatever grain its own
backing holds one at — no grain is named here"), and `CheckpointFactory.Instant` carries
`Cursor`'s anti-vacuity rules. Closed.

*Observed while driving it, already recorded:* two of the nine failures name the wrong field —
`bounds` reports *"a cursor of exactly the 4096 bytes … byte 4096 first differing"* and
`concurrency` reports *"one saver alone was refused after the contended round"* when what
differs is the instant. That is backlog §46 `[medium]`, raised in S2 and still open. It is
recorded, not forgiven.

### S2 GAP-3 — a staged `Forget` answers total absence, and the door admits it

```
[S2-3] the store answered advance=0 cursor="" name="" progress-zero=true err=<nil>
[S2-3] the kernel door answered advance=0 fresh=true err=<nil>
[S2-3] after the rollback the row is advance=1 cursor="a"
```

The half-absent answer the door refused is gone; absence is total, `Fresh()` says so, and the
rollback puts the row back. Closed.

### S3 GAP-1 — a `Unit` that runs the work twice

A caller's own retry loop around the transaction — the shape [[D-126]] and [[D-040]] tell callers
to own — driven against a transactional read model:

```
[S3-1] the unit was asked to run the work 3 times; the handler was called for map[one:2 two:2];
       the COMMITTED read model holds map[one:1 two:1]
[S3-1] row advance=1 highest=2 applied=2; halted=false
[S3-1] a state named the field: projection: the unit of work carrying this page did not commit:
       projection: this unit of work ran the work it was given more than once, and one pass
       presents one advance: a unit that retries its transaction answers the failure instead,
       and the page is re-delivered
```

Nothing halts, the field is named, the page is re-delivered and the committed read model holds
each payload once. The finding's original outcome — a permanent halt naming *the checkpoint
store* as at fault, an empty read model and a row at advance 0 — is unreachable. The two handler
calls are the redelivery, and the first went back with its unit; that is at-least-once, which is
what the module page says.

### S3 GAP-2 — a closed `Wake`

```
[S3-2] Wake closed, Idle 1h, no tick: 0 reads of the log in the 200 ms after
[S3-2] CONTROL: an open Wake released the poll, 1 read(s) followed
```

The finding measured 112 082 reads in the same window. Zero now, and the control proves the
channel is still a wake rather than ignored.

### S3 GAP-3 and S4 GAP-1 — two live instances, and the settlement's attribution

The live case and the whole shipped set were run individually, each with its own control, all
green:

```
--- PASS: TestTwoLiveInstancesOfOneNameOverOneSchema (0.30s)
    --- PASS: /under_InUnit_the_loser_is_refused_before_its_handler_runs
    --- PASS: /under_AfterApply_the_loser_has_already_applied
    --- PASS: /the_control:_one_instance_alone_drains_the_same_log_and_never_halts
--- PASS: TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt (0.17s)
    --- PASS: /the_control:_one_instance_alone_over_the_same_log_applies_every_event_once
--- PASS: TestTheTwoModesLeaveDifferentStateAtOneKillPoint (0.26s)
--- PASS: TestAProjectionDoesNotPassAPositionAWriterCouldStillCommit (0.14s)
--- PASS: TestAProjectionInAUnitPassesABurntGap (0.06s)
--- PASS: TestAProjectionResumesThroughASecondValueOverOneBacking (0.10s)
```

**The claim-order mutation was re-driven** (`claimed` applies before it claims):

```
--- FAIL: TestTwoLiveInstancesOfOneNameOverOneSchema/under_InUnit_the_loser_is_refused…
    the handlers were called for 27 envelopes over a log of 24, where a loser refused before
    its handler runs calls none
--- FAIL: TestTheAdvanceIsClaimedBeforeTheHandlerInsideAUnitAndAfterItOutside/inside_a_unit…
    the pass wrote in the order [apply save] where inside a unit the claim goes first
--- FAIL: TestTwoLiveInstancesUnderInUnitApplyEachEventOnce/under_InUnit_the_loser_never_applies
    "two" was applied 2 times, where a claim taken before the handler is one the losing
    instance never gets past
```

**S4 GAP-1's own shape was rebuilt from scratch outside the repo** — two `Projection` values of
one name, both `InUnit` over transactional read models, the new replica taking the whole log and
its unit rolling back, the old replica at `MaxRead 1` taking advance 1 with its own one-event
cursor and then dying, and the new replica's settling `Load` held at a gate until that row is
committed. On the shipped code:

```
[I6] the old replica issued 2 save(s) and left advance=1 highest=1 applied=1;
     committed so far map[s-1:1]
[I6] the new replica's Load #2 answered advance=1 cursor="…:1"
[I6] the new replica's handler took [s-2 s-3 s-4]
[I6] the COMMITTED read model holds [s-1:1 s-2:1 s-3:1 s-4:1] while the row is advance=2
     highest=4 quarantined=0
[I6] behind the cursor and never delivered again: []
```

With `anothers` mutated back to `return fenced(cause)` — the pre-fix rule — the same driver
produces the defect the review reported:

```
[I6] the COMMITTED read model holds [s-1:1 s-2:0 s-3:0 s-4:0] while the row is advance=1
     highest=1 quarantined=0
[I6] behind the cursor and never delivered again: [s-2 s-3 s-4]
[I6] the new replica halted: false
```

Three committed events applied by nobody, no error on any path, `Quarantined` 0. The fix is what
closes it.

### S4 GAP-2 — §UC-104's Must-not

The assertion is unconditional now (`projection_integration_test.go:942-947`: every payload in
exactly one row, in both arms), and the landed-save arm commits the cursor the save actually
presented rather than `held-by-another-session`. The Must-not implemented verbatim —

```go
case found.Advance == waiting.presented:
    _ = this.applyPage(ctx, this.page, this.attempt)
    return this.confirmed(waiting, found, tracker)
```

— now fails the case, live:

```
--- FAIL: TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving/a_row_at_the_advance_this_
          pass_presented,_carrying_its_cursor,_is_the_save_having_landed_and_the_loop_carries_on
    the read model holds map[u-1:2 u-2:2] where the page was applied once and never re-applied
      on the assumption that the save failed
```

`{u-1:2, u-2:2}` is exactly the tally the old inert guard let through.

### S5 GAP-1 — the exactly-once walk

The finding's own driven sentence, re-written into the shipped page:

```
docs/modules/en/projection.md:77
  The framework deduplicates nothing. Every event reaches the handler exactly once, so a read
  model needs no idempotency of its own.
→ FAIL: ../docs/modules/en/projection.md:77 promises exactly-once delivery, and delivery in
  this repository is at least once
```

and its Russian equivalent:

```
docs/modules/ru/projection.md:79
→ FAIL: ../docs/modules/ru/projection.md:79 promises exactly-once delivery, and delivery in
  this repository is at least once
```

Both were **green** before the narrowing. Both pages were restored and the three walks are green
(`TestNoDocPromisesExactlyOnceDelivery`, `TestNoCommentInTheProjectionPackagePromisesExactlyOnce`,
`TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot`). The fixture carries the
driven sentence verbatim at `window.md:13` asserted **reported**, beside `window.md:5` asserted
not reported, and the Russian pair at `обход.md:9` / `:11`.

---

## 2. The interleavings, driven from outside the repository

`/tmp/p3verify` — its own module, `-race`, built against this checkout through a `replace`. It
uses only the exported surface of `event`, `event/eventmemory` and `event/projection`; its
tally, its gates, its staging read model and its observer are its own.

| # | Interleaving | What actually happened |
|---|---|---|
| I1a | two live instances of one name, **`AfterApply`**, both gated so neither's first save lands until both have issued one, over a log of 24 | **Neither halts.** `ErrOvertaken` published. Handler applied **40** envelopes over a log of 24 — **16 events applied twice**. Nothing missing. Row `advance=3 highest=24 applied=24 quarantined=0`. This is the documented behaviour: *"under `AfterApply` … both instances apply the overlapping page"* |
| I1b | the same, **`InUnit`**, over `eventmemory` | **Neither halts.** `ErrOvertaken` published. Handler applied **32** envelopes — **8 applied twice**, the contended page. Nothing missing. This too is documented: *"a store that stages its save optimistically and revalidates at commit — the in-memory one — lets both apply under `InUnit` too and rolls the loser's half back"*. Against `eventpg`, where the fenced save holds a tuple, the shipped live case measures the loser applying **nothing** |
| I2 | a crash between the handler's write and its checkpoint advance | Handled. Before the crash: rows written, row at advance 0. After a fresh value resumed: `map[c-1:2 c-2:2 c-3:2]` — the page re-delivered, **nothing dropped**, row `advance=1 highest=3 applied=3` |
| I3 | a restart **mid-batch** (`MaxRead 2`, log of 6, killed inside the second page) | Handled. `m-3` and `m-4` applied twice, everything else once, **nothing dropped**; the window is exactly one page. Row `advance=3 highest=6 applied=6` |
| I4 | a rolled-back append leaving a **burnt position** | Handled. Delivered `[b-1 b-2]`, row `advance=1 highest=3 applied=2`, phase `following` — **no stall** over the burnt position |
| I5 | a writer that draws a position and commits after a projector read past it | Over `eventmemory` this shape is **not constructible** — positions are drawn at commit, so the late append got the highest position and delivery stayed in position order (`[w-1 w-2 w-late]`, nothing missing). The real case is live and shipped: `TestAProjectionDoesNotPassAPositionAWriterCouldStillCommit`, re-run green here, and its discriminating mutation (`deliverable` returns the whole page) re-driven: *"the projection applied 1 rows while position 1 was still uncommitted, and a consumer that checkpointed past it never sees it"* |
| I6 | two live instances where the **winner's page is shorter** and the winner then exits | Handled — see S4 GAP-1 above. Every event committed exactly once; the pre-fix rule loses three |

**Does either instance halt?** No — in six runs across both modes, neither instance ever
reached `PhaseHalted`; the loser published `ErrOvertaken`, took the row's cursor and backed off.
**Does `Handler.Apply` run twice for one event?** Yes, under contention, in both modes over
`eventmemory` and under `AfterApply` over PostgreSQL — and the module page says so in both
languages before a reader can be surprised by it.

---

## 3. Every P3-now entry of `EVENTSOURCE_REFERENCE.md`, and where it landed

| Entry | Phase | Landing |
|---|---|---|
| §Blocking / A1's **contract** decision — is position order a law or a capability | P3-now | [[D-128]], `docs/ai/decisions/D-128-the-log-delivers-in-position-order.md`, accepted, in `decisions/Index.md:183` |
| §Adopt **A2** — a live test at N=2 | P3-now (S4) | `TestTwoLiveInstancesOfOneNameOverOneSchema`, three arms including the one-instance control, green |
| §Adopt **A2** — a row in both module pages | P3-now (S5) | `docs/modules/en/projection.md:138-181` and `ru:…`, §"One name is one writer" |
| §Adopt **A2** — decide and record the `Conflict` arm | P3-now | [[D-133]] — reload-and-retry, not halting; the arm is `saveFailed` → `awaiting` → `settle` |
| §Documentation **1** — the global `xmin` stall + mitigations | P3-now (S5) | `docs/modules/en/eventpg.md:235-265` and `ru:242-272`: the cluster-wide floor named, the freeze named, `idle_in_transaction_session_timeout`, `statement_timeout`, a monitored lag metric on the `Progress.At`/`Highest` pair, and the `LOCK … IN SHARE ROW EXCLUSIVE MODE` alternative recorded rather than implemented |
| §Documentation **2** — a new projection reads the whole log | P3-now (S5) | `docs/modules/en/eventpg.md:269-273` and the projection pages |
| §Documentation **3** — the `AfterApply` two-instance double-apply | P3-now (S5) | `docs/modules/{en,ru}/projection.md`, the second bullet of §"What the two modes cost" |
| §Documentation **4** — what it costs in transaction ids | P3-now (S5) | `docs/modules/en/eventpg.md:297-302` |
| §Documentation **5** — `Wake` is a hint, never a delivery mechanism | P3-now (S5) | `docs/modules/en/projection.md:219`, `ru:233`, and the `Spec.Wake` field comment |
| §Reject **1** — *what vv must prove instead*: one `Runner` per name | P3-now | [[D-130]]:22 — *"one slow handler delays only its own projection"*, with `TestTheSupervisorHoldsAProjectionAndNewStartsNothing` named |
| §Reject **2** — the `jobs.Stager` outbox asymmetry belongs on the module page | S5-adjacent | **not on either page**; recorded as backlog §71 `[medium]`, raised by the S5 review. Scheduled, not discharged |
| §Reject **3** — *what vv must accept*: every projection reads every event | S5-adjacent | **no landing anywhere, and no backlog entry either.** Raised here — see §6 |
| §Adopt **A1**'s implementation, **A3**, W7–W11, R14–R32 | P4 / backlog | `EVENTSOURCE_BACKLOG.md` `## From the reference` §1–§14, each with the reference site and the mechanism in full |

`## From the reference` §1 (the xid8 scoping) is **CLOSED by [[D-128]]**, §2 (two live instances)
**CLOSED by [[D-133]] and S3's review**, §3 (the `xmin` stall) **CLOSED by S5**. The `## P2 [high]`
entry the adjudication said would become unexecutable is marked *REFUSED by [[D-128]], not
implementable as written*, which is what the adjudication asked for out loud.

---

## 4. The delivery-order contract is frozen as the ADR decided

[[D-128]] decides: position-ascending delivery **and** one stream's order being a subsequence of
the log's are both **kernel laws**, with no capability to deny either; the reference's
`(TRANSACTION_ID, ID)` tuple read is **refused**, because it presumes the writing transaction
takes its id at the append and [[D-118]]/[[D-126]] forbid vv to satisfy that — measured live on
17.9, it reorders one stream against itself; and `Progress.Highest` keeps its completeness
meaning. All four sites agree with that, and with each other:

| Site | What it now says | In the frozen manifest |
|---|---|---|
| `event/reader.go:100-106` | the ascending arm still refuses with `ErrBackend`; the comment names it as **one half of a law and not a store's option**, says the other half is certified live by the resumption section, and cites [[D-128]] | yes |
| `event/store.go:126-135` | *"Two orderings, and both are laws rather than options … `Capabilities` carries no member for either and never will"* | yes |
| `event/eventtest/sections_read.go:24-25, 47-58, 61-70, 174` | `probe.ascending` and `probe.subsequence`; `globalOrderSection` and `globalPagingSection` take **no `needs` function** (`inventory.go:30, 33`), and the comment says why | yes |
| `event/checkpoint.go:14-25` | `Progress.Highest` is *"a completeness watermark rather than a number off the last envelope … it means this much only because the log delivers in position order for every store ([[D-128]])"* | yes |

`scripts/event_kernel.sha256` holds **131** files and lists all four, plus
`event/projection/pass.go`. `./scripts/checks.sh event-kernel` is green, and regenerating the
baseline produces a byte-identical manifest. `make api` regenerates `docs/api/surface.md`
byte-identically (`sha256 c70c79c6…`); the diff against `HEAD` is **50 additions, 0 removals**,
so nothing left the published surface.

The published `Progress.Highest` sentence is the same one in three places —
`event/checkpoint.go`, `docs/modules/en/event.md:389-400` and
`docs/modules/en/projection.md:128-136` — and [[D-128]]'s own §"What `Progress.Highest` means"
carries it verbatim.

**One caveat, already recorded and not blocking:** backlog §72 `[medium]` (S5 GAP-5) says the
watermark rests on a second premise the `Log.ReadAll` contract does not state — *a cursor never
advances past a position a writer could still commit*. Position ordering is necessary and not
sufficient; the obligation **is** certified (`sections_resumption.go`'s `acrossTheFlight`,
§INV-080) but it is not written where a store author reads the contract. That is the most
consequential of the deferred items and it is scheduled.

---

## 5. The global `xmin` stall is documented, with its mitigations

`docs/modules/en/eventpg.md:235-265`, and `ru:242-272` carries the same section. It names the
mechanism (`pg_snapshot_xmin(pg_current_snapshot())` is the **cluster's** oldest running
transaction id, not this schema's), the trigger (any session anywhere that wrote and then went
idle in transaction — *"an unrelated slow report, a leaked connection, a paused debugger"*), the
consequence stated as the reference states it (*"Nothing is lost … **Delivery freezes, and every
projection over this schema freezes with it**"*), and three mitigations *"none of which is this
library's to apply"*:

- `idle_in_transaction_session_timeout` on the application role — *"the one that matters; without
  it a single leaked connection is an unbounded stall"*;
- `statement_timeout` on the same role, for the not-idle-but-very-long shape;
- a monitored lag metric, on the **pair** `Progress.At` and `Progress.Highest`, *"because a
  `Highest` that stops moving while the log grows … is indistinguishable from a halted projection
  at a glance"*.

It also records the reference's own unimplemented alternative (`pg_sequence_last_value` behind
`LOCK … IN SHARE ROW EXCLUSIVE MODE` in its own transaction), with its cost — blocking every
write to `events` once per poll — and says it is recorded rather than implemented.

---

## 6. What this gate raises

**One `[medium]`, appended to `EVENTSOURCE_BACKLOG.md` `## P3` as §76 and left alone.**

> **§76 — the reference's Reject 3 acceptance is on no page and in no entry `[medium]`.**
> `EVENTSOURCE_REFERENCE.md:464-472` refuses a filter parameter on `Log.ReadAll` and states what
> vv accepts instead: *"every projection reads every event, so N projections cost N × the read
> traffic. If that ever becomes the binding cost, the answer is a shared reader fanned out to N
> routers — not a filter on the store contract."* Every other obligation the adjudication leaves
> behind is discharged or scheduled; this one is in no module page, no decision, no flow and no
> backlog entry. `grep -rn "reads every event\|N × the read\|shared reader" docs/` returns
> nothing. Operational rather than behavioural, hence `[medium]`.

**Nothing else. No `[critical]` and no `[high]`.** Seven `[medium]`/`[low]` items from the S5
review remain open by policy and are all scheduled — backlog §69 (deliverable 12's absent
enforcement), §70 (the `AfterApply`/one-below/unconfirmed halt in neither table), §71 (the
`jobs.Stager` asymmetry), §72 (the `Log` contract's unstated second premise), §73 (the
`ErrConflict` row), §74 (`event/store.go`'s path list and two missing FL-038 rows), §75
(§INV-021's stale count) — plus §46 and §54–§68 from earlier sections. Recording is scheduling.

---

## 7. No exactly-once claim exists in the new code, tests or docs

`grep -rni "exactly.once|ровно один раз|ровно однажды"` over `event/`, `docs/` and `scripts/`
returns nothing that is a delivery promise:

- `event/eventpg/executor.go:75` — one **statement** reaching a driver once;
- `event/projection/doc.go:24` and `spec.go:60` — `Spec.Unit` runs **the work** once, which is
  the arity of a caller's unit and is the sentence the narrower comment walk exists for;
- `event/projection/retry_test.go:294`, `unit_test.go:378-385`,
  `event/eventtest/inventory_test.go:27`, `event/eventpg/cursor_integration_test.go:40`,
  `sources_test.go:524` — test prose about one measured case, one section report, one statement;
- the `docs/` hits outside `event` are `crud`, `jobs`, `cache` and `vvcfg` prose predating this
  phase; `jobs`' own [[D-118]]:107 forbids the claim by name and `UC-031`/`UC-032` list it under
  what is **not** promised.

Both machine walks are green over the whole tree: `TestNoDocPromisesExactlyOnceDelivery` (with
the narrowed window verified above by driving a promise past it and watching it fail) and
`TestNoCommentInTheProjectionPackagePromisesExactlyOnce`.

---

## 8. The checks

| Arm | Result |
|---|---|
| `make check` | **ok on all ten arms** — `check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace`, `check-event-kernel`. Exit 0, run twice (before and after every mutation) |
| `make unit` | **green**, 36 module runs, no `FAIL` line. Exit 0, run twice |
| `make vet` | **green**. Exit 0, run twice |
| `gofmt -l .` | **silent** |
| `make examples` | **green**, exit 0 |
| `make api` | regenerates `docs/api/surface.md` byte-identically; diff vs `HEAD` = 50 additions, **0 removals** |
| `./scripts/checks.sh event-kernel` | ok; manifest 131 files, byte-identical on regeneration |
| eventpg tagged suite, pass 1 | `ok github.com/frostgrove/vv/event/eventpg 104.809s` — exit 0 |
| eventpg tagged suite, pass 2 | `ok github.com/frostgrove/vv/event/eventpg 105.133s` — exit 0 |
| eventpg tagged suite, re-run after every mutation was reverted | `ok … 104.198s` and `ok … 104.999s` — exit 0 both |
| unset `FROSTGROVE_EVENTPG_TEST_DSN` | **fails, does not skip**: *"this suite proves nothing without a database, so it fails rather than skipping"* |
| the worked example, live | five stdout lines, then `example_checkpoints` `cursor_bytes=28 advance=1 highest=6 applied=6 quarantined=0` and `example_balances` `acme/1 100 3` / `acme/2 100 3`, read back with `docker compose exec -T postgres psql` |

The tree is byte-identical to the one those numbers were taken on: every mutation was reverted
with `cp` from a copy taken before it, `diff -q` confirmed each restoration, and
`check-event-kernel` was green after each. The only files this gate changed are this document
and the one backlog entry it appended.

---

## Live output, verbatim

```
$ cd _examples && GOWORK=off go run ./event-checkpoints-elsewhere
balances   draining   applied=0 quarantined=0
balances   draining   applied=6 quarantined=0
balances   following  applied=6 quarantined=0
acme/1               100
acme/2               100

$ docker compose exec -T postgres psql -U vv -d vv \
    -c "SELECT projection, octet_length(cursor) AS cursor_bytes, advance, highest, applied,
        quarantined FROM example_checkpoints;"
 projection | cursor_bytes | advance | highest | applied | quarantined
------------+--------------+---------+---------+---------+-------------
 balances   |           28 |       1 |       6 |       6 |           0

$ docker compose exec -T postgres psql -U vv -d vv -c "SELECT * FROM example_balances ORDER BY 1;"
 account | balance | version
---------+---------+---------
 acme/1  |     100 |       3
 acme/2  |     100 |       3

$ FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable' \
    go test -race -count=1 -tags=integration ./event/eventpg/...
ok  	github.com/frostgrove/vv/event/eventpg	104.809s
ok  	github.com/frostgrove/vv/event/eventpg	105.133s
```
