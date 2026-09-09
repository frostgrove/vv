# EVENTSOURCE P3 — S3 (`event/projection`: the loop, the router, the lifecycle) — GAPS

## Round 1 — implementation reviewer — 2026-09-09

**What was verified, with the commands, before any finding below was written.**

- The section's pasted checkpoint is real: `go build ./...`, `go vet ./event/... ./scripts/...`,
  `gofmt -l event scripts` silent, the counted `-list` arm = **21**, `go test -race -count=1
  ./event/...` green (`event 6.339s`, `eventmemory 1.508s`, `eventtest 4.083s`, `projection
  1.282s`), the three `./scripts/` arms green, `check-deps: ok`, `event-kernel-baseline` →
  131 files, `check-event-kernel: ok`, and `event-kernel-moved .git/event_kernel_before_s3` →
  the same 21-path moved set the plan pastes, `event-kernel-moved: ok`, `EXIT=0`.
- Beside it: `make check` green on all ten arms, `make unit` green, and the live
  `event/eventpg` suite re-run green in **96.760 s** against
  `postgres://vv:vv@localhost:55432/vv` (the section claims 96.9 s — reproduced).
- Kernel boundary: `git status --porcelain event/` shows exactly the plan's allowed set;
  the four files S3 moved outside `event/projection/` (`event/checkpoint.go`,
  `event/reader.go`, `event/store.go`, `event/eventtest/sections_read.go`) each carry
  [[D-128]] contract text and no behaviour change (`git diff` read line by line).
- Counted metrics: 9 non-test files, 1 913 non-test lines, longest file `pass.go` 555;
  longest real function body **32 lines** (`Run`), then `insideAUnit` 28 and `settle` 26 —
  no function over 40, no nesting past depth 3, no parameter list over 4. Exported surface
  30 symbols, matching [SPEC] §9.2 plus the two S3 changes the plan records (`ErrOvertaken`,
  the `Quarantined` value) and the typed `Unchecked` (D-133/plan). No package-level mutable
  state; `grep` for `go ` statements, `func init`, `log.`, `fmt.Print*`, `os.Getenv` in
  non-test files → **zero**. Import closure of `./event/projection` is `crud`, `errs`,
  `event`, `runtime`, `utils` and the stdlib; `check-deps` reports 0 external for the root
  module.
- Exactly-once creep: `grep -rin "exactly.once"` over the new package and the two new
  decisions finds three hits, none of them a delivery promise, and
  `TestNoDocPromisesExactlyOnceDelivery` runs green over `docs/`.
- **Mutation testing — the two most important tests were broken and both failed.** (a) The
  claim-first order in `claimed` (`pass.go:192-212`) reversed to apply-then-save →
  `TestTheAdvanceIsClaimedBeforeTheHandlerInsideAUnitAndAfterItOutside` fails, and
  `TestTwoLiveInstancesUnderInUnitApplyEachEventOnce` fails under `-race` on every one of
  10 runs (`"one" was applied 2 times`). (b) The [[D-133]] arm reverted so a fenced conflict
  halts → both two-instance tests fail and `TestAForgottenCheckpointRefusesTheNextSaveAndHalts`
  still passes, so the two are discriminated. (c) `copyOf` returning the page as-is →
  `TestARetryReAppliesTheLogsOwnPageAfterABackoff`'s ownership control fails. All three
  mutations were reverted and `check-event-kernel: ok` proves the tree is byte-identical.
- **Interleavings driven, not read** (a scratch module outside the repo, `-race`, over
  `eventmemory` + `eventmemory.Checkpoints`): a crash between the handler's write and the
  advance → the page is redelivered, `three`/`four` applied twice, **nothing dropped**; a
  restart mid-batch → resumes at the last saved cursor; a rolled-back append burning a
  position → both committed events delivered, `Highest` 3, **no stall**; three live
  instances of one name in both modes to a 24-event log → 0 dropped, 1–4 duplicated under
  `AfterApply`, 0 under `InUnit`, **none halted**, over 6 runs; `Progress.Highest` never
  observed going backwards under two-instance contention and never observed passing a
  position no handler saw; a backend that fails the resolving `Load` three times → retried
  **as the resolution** and recovered without a second save. A writer committing a position
  a projector has passed is not constructible over `eventmemory` (`MonotoneVisibility:
  Supported`, positions assigned inside the publishing section) and is correctly S4's live
  case (UC-113).
- Nothing in `event/projection` reads a position other than `pass.go:281`
  (`Progress.Highest` = the page's last position, sound under [[D-128]]), computes a head,
  compares cursors or does position arithmetic, so nothing is correct only on `eventmemory`.

Three findings block. Three `[medium]` and three `[low]` are appended to
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P3` §54–§59 and left alone.

---

## Round 2 — the three blocking findings closed — 2026-09-09

All three are `closed` below, each reproduced before it was fixed and each left with a
test that fails if the fix is reverted.

- **GAP-1** reproduced: a `Unit` running its body twice halted the projection permanently with
  `"this checkpoint store refused a save over the very row its own fence admits: … conflict"`,
  read model empty, row at advance 0. Fixed by making `Tracker.Save` answer the advance it
  presented — kernel, `(uint64, error)` — deleting `Projection.advance`, and refusing a second
  run of the work inside one pass by name without a halt.
- **GAP-2** reproduced: 112 082 log reads in 200 ms with a closed `Wake` and no tick fired.
  Fixed by the two-value receive and a nil-ed loop-local channel.
- **GAP-3** closed by writing the live case rather than only scheduling it, and it **corrected**
  D-133 §3: at advance 1 the loser is held by speculative insertion, not by a row lock.

**Mutations run, each reverted afterwards, `check-event-kernel: ok` proving the tree is
byte-identical:** (a) the `entered` guard removed → GAP-1's case fails with the original halt;
(b) `Tracker.Save` answering `this.advance` → the kernel's new subtest fails on the refused arm;
(c) the `errFenceRefused` arm of `settle` deleted → GAP-1's fence case fails on the message; (d)
`claimed` reordered to apply before it saves → the live two-instance case fails, 26 handler calls
over a log of 24. GAP-2's fix was mutation-tested by the reproduction itself.

---

### GAP-1 [high][immediate] The advance a pass presents is computed twice, and the settlement table decides on the copy that can be wrong

- **Where:** `event/projection/pass.go:278-287` (`presentSave`, line 279:
  `held.presented = this.advance + 1`) against `event/checkpoint.go:237-262`
  (`Tracker.Save`, line 250: `presented := Checkpoint{… Advance: this.advance + 1 …}`);
  consumed by `event/projection/pass.go:368-393` (`settle`) and
  `pass.go:411-430` (`confirmed`, `rolledBack`).
- **What:** [SPEC] §3.1 fixes that **the fence is the door's** — *"the tracker holds the
  advance it loaded, presents one above it, and refuses a save before a load — so no caller
  can present a wrong advance, and `Checkpoint.Advance` is a field the store reads rather
  than one a caller computes"*. The projection nevertheless keeps its own copy
  (`Projection.advance`, `projection.go:30`) and re-derives the presented advance from it,
  because `*event.Tracker` exposes no accessor for the number it just presented. The two
  copies agree only while a pass issues exactly one save. They part the moment a caller's
  `Unit` runs the body more than once — and then the whole [[D-133]]/D10b settlement table,
  which is `found.Advance` compared against `waiting.presented`, is decided on the wrong
  number.
- **Why this severity:** driven, not argued. A `Unit` that retries its body — the ordinary
  40001 retry loop, and the shape [[D-126]] and [[D-040]] tell callers to own, since the
  store may not — makes the second `presentSave` record `presented = 1` while the tracker
  presents 2 over a row at 0. The store refuses; `settle` reads `found.Advance = 0`,
  matches `found.Advance+1 == waiting.presented && fenced`, and halts with
  `errFenceRefused`:

  ```
  a Unit that retried its body halted the projection: projection: this projection stopped
  advancing and is not applying events: "orders": projection: this checkpoint store refused
  a save over the very row its own fence admits: event: the stream is not at the version
  this append was decided at: conflict
  the read model holds [one two] and the row is {Advance:0}
  ```

  So: the projection is **permanently dead** on the first serialisation failure of a
  deployment that took [[D-126]]'s advice, the halt is terminal for that value's life, a new
  value in a new process halts again on the next 40001, and the refusal **names the
  checkpoint store as the party at fault** when the framework presented an advance no fence
  admits. That is the rolling-deploy failure [[D-133]] was written to remove, reached by a
  different door. A second shape is silent rather than loud: a `Unit` that runs the body
  twice on one transaction leaves `advance 4` for three pages, the model `[one one two
  three]`, and `Projection.advance` one behind the tracker's for the rest of the process —
  after which an `ErrUncertain` whose save landed is read as `found.Advance > presented` and
  published as `ErrOvertaken` with no second writer anywhere.
- **Why this timing:** it is a kernel-contract violation in the phase that publishes that
  contract. S4 drives `TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving` and S5
  freezes `Tracker` on the exported surface and writes the module page that tells a third
  party the fence is the door's. Repairing it afterwards means either a new exported
  `Tracker` method or unwinding the settlement table's inputs — both after the surface
  baseline is published.
- **Close criteria:**
  - [x] The advance a pass presents is read from the party that presented it rather than
        re-derived — **`Tracker.Save` answers it**: its signature is now
        `(ctx, cursor, progress) (uint64, error)` and it answers the advance it built into
        the `Checkpoint`, on a refusal as well as on a save that landed, because a refused
        save is exactly when the number is needed. `Projection.advance` is **deleted** — it
        was written in four places and read in one — so `grep -rn "advance" event/projection/`
        finds no copy of the fence at all, and `landed` lost its first parameter.
  - [x] `Spec.Unit`'s arity is stated on the field itself (*"It runs the work exactly
        once"*), and a `Unit` that runs the work a second time inside one pass is **refused
        by name** before it writes anything — `errUnitRanTwice`, beside `errUnitRanNothing`
        which refuses zero. The refusal is **not a halt**: the first run's tally is kept
        deliberately, so the settlement reads the row against the advance that run presented
        and finds either it (the unit committed anyway — the page lands, applied once) or the
        one below it (the unit went back — the page is re-delivered).
  - [x] `TestAUnitThatRunsTheWorkTwiceIsRefusedAndThePageIsRedelivered` drives a caller's own
        retry loop around the transaction: the page is applied **once**, the row is at advance
        1, nothing halts, the unit was asked to run the work three times over two passes, and
        the published `State.Err` names the field — `rolledBack` now carries the unit's answer
        into the redelivery, because a projection backing off for ever over a unit assembled
        wrong is one nobody can diagnose from *"it did not commit"*.
  - [x] `TestACheckpointStoreThatRefusesASaveItsFenceAdmitsHalts` pins `errFenceRefused` to
        the one door it is behind — a store answering `Failure(Conflict, …)` over a row it did
        not move — with the **control** that the same refusal over a row that **did** move is
        contention, publishes `ErrOvertaken` and does not halt.
- **Status:** closed — 2026-09-09

---

### GAP-2 [high][immediate] A closed `Spec.Wake` channel turns following into an unbounded read loop

- **Where:** `event/projection/projection.go:185-194` (`follow`, line 190:
  `case <-this.spec.Wake:`); the field at `event/projection/spec.go:70`.
- **What:** `Wake` is received from and never checked for closure. A closed channel is
  permanently ready, so `follow` returns immediately for ever and the loop reads the log as
  fast as the store can answer. Nothing refuses it, nothing detects it, and no comment says
  the channel must never be closed.
- **Why this severity:** driven. `Spec.Idle = 1s`, `Wake` closed after the first page:
  **109 682 reads of the log in 200 ms** — roughly 550 000 `ReadAll` per second, which
  against `eventpg` is 550 000 statements per second on the log table, per projection, until
  the process is killed. Closing a channel is *the* Go idiom for broadcasting to an unknown
  number of waiters, and a composition root that closes `Wake` at shutdown — the most
  natural place to have one — turns its own shutdown into a database saturation event. This
  is the same busy loop [SPEC] §3.2(6) refuses twice by name (the halted projection that
  keeps polling, the classifier that spins on a permanent failure) and the property
  §Adopt/R13 of the reference adjudication says must be kept deliberately: *"a wake signal
  is a hint layered on top of a poll that always runs … `Spec.Wake` must never become a
  replacement for `Spec.Idle`."* A closed channel makes it exactly that replacement.
- **Why this timing:** `Wake` is public surface. S5 publishes it in the module pages and in
  `docs/api/surface.md`; after that, a guard that changes what a closed channel means is a
  behaviour change to a published seam rather than a line in the loop.
- **Close criteria:**
  - [x] `follow` reads the channel with the two-value form and, on a closed one, sets the
        loop's own copy to nil — a nil channel is never ready in a `select`, so the loop stops
        waiting on it for its life and follows on `Idle` alone. It re-selects rather than
        returning, so a closed `Wake` produces no wake at all rather than one spurious pass.
        The channel is now a field of `Projection` because it is loop state, and `Run`'s
        goroutine is the only writer.
  - [x] `TestAClosedWakeStopsWakingAndAnOpenOneStillDoes`: with `Wake` closed and no tick
        fired, at most two reads over 200 ms — **112 082 before the fix**. **Control:** an open
        channel still releases the poll, so a loop that ignored `Wake` entirely fails.
  - [x] Written down where a caller reads it — the `Spec.Wake` field's own comment — and
        scheduled for the module page: the plan's S5 now names it as one of three sentences
        that section owes, beside `Idle`, in the words *"a hint layered on a poll that always
        runs and never the delivery mechanism"*. There is no `projection.md` yet; S5 writes it.
- **Status:** closed — 2026-09-09

---

### GAP-3 [high][immediate] [[D-133]]'s claim-before-the-handler is proved only against a mutex the test supplies, and the live two-instance case is in no section's Tests list

- **Where:** `docs/ai/decisions/D-133-a-projection-that-loses-the-fence-takes-turns.md:93-104`
  and `:117-121`; the proof it names at `event/projection/unit_test.go:305-321`; the plan's
  S4 Tests list, `EVENTSOURCE_P3_PLAN.md:2463-2529`;
  `EVENTSOURCE_BACKLOG.md:1991-2032` (`## From the reference` §2, still `[high]`, still open).
- **What:** [[D-133]] states as fact that *"a fenced `UPDATE … WHERE advance = $3 - 1` takes
  the checkpoint row's lock. Presenting the advance first therefore blocks the second
  instance on that row before its handler runs"*, and its own §3 admits the property belongs
  to the **store**. The only test of it,
  `TestTwoLiveInstancesUnderInUnitApplyEachEventOnce`, obtains that mutual exclusion from a
  `sync.Mutex` the test itself wraps the unit in (`unit_test.go:308-321`) — over
  `eventmemory`, which D-133 says behaves the *other* way. So the invariant is stated about
  PostgreSQL and pinned about a mutex. Meanwhile the reference adjudication's §Adopt A2 and
  backlog §2 (`[high]`, **P3-NOW**) both owe a live `--scale 2` case in
  `event/eventpg/projection_integration_test.go`, and the plan's S4 Tests list — the list S4
  will actually execute — still contains no concurrent two-instance case;
  `TestAProjectionResumesThroughASecondValueOverOneBacking` is sequential replacement. The
  same backlog entry's owed module-page row still reads *"the loser halts and does not
  resume"*, which [[D-133]] has reversed, so the scheduling artifact now contradicts the
  binding decision.
- **Why this severity:** "missing test for a stated invariant", and the invariant is the
  justification for reordering the two writes inside the unit. If PostgreSQL's fenced
  `UPDATE`/`INSERT … ON CONFLICT DO NOTHING` does not block the loser where D-133 says it
  does — a question about lock acquisition on a row that does not yet exist at advance 1, and
  about which of the two statements the loser runs — then under `InUnit` both instances
  apply the overlapping page exactly as under `AfterApply`, the module page S5 writes will
  say otherwise, and the reference's own reason for testing at N=2 applies verbatim: single
  instance testing never executes the contention branch, so an implementation that is wrong
  at N=2 passes.
- **Why this timing:** S4 executes the list it is given and S5 writes the module page from
  the decision. Once both are done, the claim is published twice and the case that would
  have falsified it is nobody's.
- **Close criteria:**
  - [x] The case is **written and green**, not only listed:
        `TestTwoLiveInstancesOfOneNameOverOneSchema` in
        `event/eventpg/projection_integration_test.go`, two `Projection` values of one name
        over one schema in both modes, asserted against the read model's rows in the database.
        The plan's S4 Tests list and its checkpoint carry it (eleven tests became twelve), and
        S4 says why it arrived a section early. **The interleaving is driven**: a gate holds
        each instance's first save until both have issued one, so both contend for advance 1
        over the page they both read. **Controls:** the gate must have opened and
        `ErrOvertaken` must have been published — a run in which one instance drained the log
        before the other woke **fails** — and one instance alone applies every event once,
        contends with nothing and reaches `PhaseFollowing`.
  - [x] It asserts exactly that, and D-133 §3 is **confirmed and its mechanism corrected**.
        Under `InUnit` the handlers of the two instances were called for **24 envelopes over a
        log of 24** and the contended page is in **one** row; under `AfterApply` it is in
        **two**. The correction: at advance 1 there is **no row to lock**, so what refuses the
        loser is PostgreSQL's speculative insertion on the store's
        `INSERT … ON CONFLICT DO NOTHING` (`checkpoints.go:353-364`), not the fenced `UPDATE`'s
        row lock — which is exactly the question the adjudication raised and could not answer
        without a database. D-133 now says so, with the measurement.
        **Mutation:** reordering `claimed` to apply before it saves takes the `InUnit` handler
        count to 26 and the case fails, so it discriminates.
  - [x] `EVENTSOURCE_BACKLOG.md` `## From the reference` §2 is **closed** and its owed doc row
        rewritten to [[D-133]]'s answer — the loser takes its turn, `ErrOvertaken` is how a
        deployment is told, and only an absent row or one behind the fence halts. §Adopt A2 of
        `EVENTSOURCE_REFERENCE.md` carries the same closure box.
  - [x] D-133's **Proven by** names the live case, and its "Where it lives" names the file.
- **Status:** closed — 2026-09-09

---

**Deferred and recorded, not fixed** — `EVENTSOURCE_BACKLOG.md` `## P3` §54–§59:
§54 `Progress.Highest` and the stored cursor can move backwards when an unconfirmed save
resolves against another writer's row `[medium]`; §55 D5's *the quarantine sink is called
inside the unit* is pinned by no test `[medium]`; §56 three arms of `settle`'s table are
unreachable from any case `[medium]`; §57 the isolation pass is told apart by
`apply.foreseen == nil` `[low]`; §58 the idle ticker is created once for the loop's life, so
following is fixed-rate-with-drop rather than the fixed delay the adjudication records
`[low]`; §59 [[D-128]]'s **Proven by** cites the wrong line range `[low]`.
