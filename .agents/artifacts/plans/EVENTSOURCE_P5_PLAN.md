# EVENTSOURCE PHASE 5 — THE CALLER'S GUARANTEES: ES-05, ES-07, ES-08, ES-09 — IMPLEMENTATION PLAN

**Status:** plan, phase 5 of the PostgreSQL event-sourcing roadmap, and the last one.
**Written against:** [`EVENTSOURCE_P5_USECASES.md`](../usecases/EVENTSOURCE_P5_USECASES.md)
(UC-204…UC-251, INV-108…INV-126) and its
[GAPS file](../gaps/EVENTSOURCE_P5_USECASES_GAPS.md) — round 1 red, one `[critical]` and ten
`[high]`, all closed 2026-09-12; [`EVENTSOURCE_P5_STUDY.md`](../usecases/EVENTSOURCE_P5_STUDY.md);
[`EVENTSOURCE_REFERENCE.md`](../gaps/EVENTSOURCE_REFERENCE.md), whose three **Reject** entries are
binding and are not re-proposed; the four appendices at
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md:783-821`, read in the original
Russian, part 3 of each sentence by sentence; the shipped `event/`, `event/eventmemory/`,
`event/eventtest/`, `event/eventpg/`, `event/projection/`, `runtime/`, `crud/`, `jobs/`;
`scripts/checks.sh`, `scripts/event_test.go`, `scripts/projection_test.go`,
`scripts/extensions_test.go`, `scripts/docs_test.go`; PostgreSQL **17.9** at
`localhost:55432`, measured 2026-09-12.
**Format precedent:** [`EVENTSOURCE_P4_PLAN.md`](EVENTSOURCE_P4_PLAN.md).
**[SPEC]** below means [`EVENTSOURCE_P5_USECASES.md`](../usecases/EVENTSOURCE_P5_USECASES.md); a
bare `§` is a section of it. **[APX]** means the four appendices. **[GAPS]** means the round-1
audit.

**Delivery policy in force (2026-09-08).** Only `[critical]` and `[high]` block. `[medium]` and
`[low]` are appended to [`EVENTSOURCE_BACKLOG.md`](../gaps/EVENTSOURCE_BACKLOG.md) under `## P5`
and **left alone** — recording is scheduling, not forgiving, and a medium is not fixed because it
is quick. **A gate never proceeds silently red:** a section is reported closed by its checkpoint's
output, never by `go test` being green. Phase 1 reported six sections closed on a green suite with
29 of 32 review verdicts red; that is the failure this line exists to prevent.

**Exactly one repository gate is red at HEAD, and it is foreign.** Reproduced on the pristine tree
today, `go test -count=1 ./scripts/` reporting one `--- FAIL:` line and no other:

- `TestNoI18nPackageCostsMoreThanItsErrorSeam` (`./scripts`) — *"the i18n extension reaches
  github.com/go-json-experiment/json/jsontext outside its error seam"*. It arrived with the owner's
  i18n work, it is the owner's call, and it is neither fixed nor allowlisted here.

It is not touched and **it is not folded into a green claim**: every section below that runs
`go test ./scripts/` names which arm is red and that it is foreign.

**`check-tidy` is *not* a second foreign red, and an earlier draft of this plan said it was.**
`EVENTSOURCE_P4_VERIFY.md` §1 recorded it red over 29 satellites — every one missing the
`github.com/jackc/pgx/v5 v5.10.0/go.mod` hash line — and that observation was true when it was
taken. It does not reproduce. Run twice from a clean `git status` on 2026-09-12:

```
$ ./scripts/checks.sh tidy      → check-tidy: ok
$ ./scripts/checks.sh replaces  → check-replaces: ok
```

The satellite `go.sum` files were re-tidied between that observation and this HEAD. **Both arms are
therefore required green in every checkpoint that has anything new to say to them, and S6 runs both
after the examples land** — because `_examples/go.mod` requires no `event/eventpg` today, S6 adds
two example directories, and a require in a satellite without its matching `replace` is invisible
under `go.work` and visible only to these two `GOWORK=off` arms. Carrying a stale red forward would
have excused exactly the arm that catches the thing this phase is about to do.

Five obligations frame everything, and all five are executable:

- **The kernel manifest moves, deliberately, in four places and no fifth.**
  `scripts/event_kernel.sha256` holds **152 paths** today and freezes everything under `event/`
  outside `event/eventpg`. `event`, `event/projection`, `event/eventmemory` (three comments) and
  the new `event/receipt` all move it; **`event/eventtest` does not**, and that is the phase's own
  claim about itself (§ *The conformance extension*). Every section's `event-kernel-moved` allowed
  set is anchored, and a path outside it fails the section.
- **The `Store`, `Log`, `Checkpoints` and `Limits` contracts do not move, so no certified store
  goes red.** [SPEC] §5.4 and INV-120. This is proved by `make api`'s diff over the `event` section
  being exactly three added lines and by `event/eventtest`'s own inventory tests being green
  unchanged — not by an unchanged file.
- **`event/receipt` is a package of the root module and takes no third-party import**, including
  in its `_test.go` files, because `check-deps` lists the root module with
  `-deps -test -tags=integration`. Its closure is `event`, `crud`, `errs`, `utils` and the standard
  library ([[D-033]]). **Every live test of this phase lives in `event/eventpg/`.**
- **Nothing this phase adds starts anything.** `startsNothing`
  (`scripts/extensions_test.go:147`, called from `scripts/event_test.go:71`) forbids a `go`
  statement in any non-test file under `event/`. A wait runs on its caller's goroutine and costs
  nothing when nobody is waiting; `Claim`, `Once` and `Resolve` run on the caller's. [[D-092]].
- **The live gate names its own command and an unset DSN fails it.** `TestMain`
  (`event/eventpg/main_integration_test.go`) exits 1 on an unset `FROSTGROVE_EVENTPG_TEST_DSN`, so
  a live claim cannot be made by a suite that skipped. S4 and S5 are where **twenty-two** of
  [SPEC] §6's twenty-four items are proved, twice in a row; the other two are item 23, which §6's
  own sentence routes into `event`, and item 24, which is a benchmark. § *The §6 item → test map*
  is where each of the twenty-four is named, and a checkpoint's count is not evidence for an item
  that map does not carry.

---

## What this plan delivers, and what it does not

**Delivers.** Two methods and one sentinel in `event` (`Repo.Digest`, `Repo.StateAt`,
`ErrVersion`) and three reworded comments; two new files and three modified ones in
`event/projection` (the wait: `Mark`, `MarkOf`, `WaitSpec`, `WaitOf`, `WaitSpec.Committed`,
`Visibility`, `Wait`, four sentinels, and one widened sentence of `Park.Holds`'s contract); a new
package `event/receipt` (ten exported types, six functions, seven constants and four sentinels —
`make api` measured, and the two functions **P-21** adds are in that count); four widened
`scripts/` walks and one new dependency-budget row; **twenty-six** tagged tests in `event/eventpg`
— S4's ten and S5's sixteen — of which **twenty-three carry twenty-two of [SPEC] §6's twenty-four
items** and three carry obligations of this plan that §6 does not list
(`TestThePublishedClaimBindsNoInstantAsAParameter`, `TestACodecThatDoesNotEncodeTheSameBytesTwice`,
`TestFourLedgerDefectsEachBreakTheCaseThatNamesThem`); §6 item 23 ships untagged in `event` and
item 24 is `BenchmarkStateAt`, both named in § *The §6 item → test map*; two `_examples`; four
decision records
(D-142, D-143, D-144, **D-145 — a contract with no code**); three new repo-level use cases
(UC-036, UC-037, UC-038) and one `See also` on [[UC-032]]; a new module page in two languages and
four updated ones; two flows extended and one new (**FL-043**); a new consumer usage guide; the
roadmap close-out; a regenerated `docs/api/surface.md`.

**Does not deliver.** No snapshot, memo or cache of a folded state — [[D-132]]'s invariant is
unchanged and its enforcement test is neither loosened nor narrowed (§ *The snapshot gate*). No
head, lag metric or "caught up". No timestamp boundary on a historical read. No version ceiling or
filter on `Store.ReadStream`, and no other change to the store contract. No fourth `Resolve`
standing that proves a rollback. No retention sweep. No new `event.Outcome` member. No OTel span,
metric or log line — ES-10 owns that and is not phase 5. No wait and no receipt in `eventpg`:
neither is a store capability, `eventpg` gains nothing and `SchemaVersion` stays at **2**. Each
refusal is [SPEC] §2's and is not re-argued here.

---

## Decisions this plan makes, that [SPEC] left open or got wrong

[SPEC] §8 leaves nine tensions to the plan. All nine are answered — P-8 through P-16 below. **P-1
through P-7 are corrections**: five shapes [SPEC] froze without noticing what the tree does to
them, and two costs it stated wrongly. P-1 and P-7 are the two that would have shipped a defect.

**P-17 and P-18 are two more corrections, and they are the round-1 plan audit's**
([`EVENTSOURCE_P5_PLAN_GAPS.md`](../gaps/EVENTSOURCE_P5_PLAN_GAPS.md) GAP-1 and GAP-4). They are
numbered after the tensions rather than folded into P-1…P-7 so that every reference already written
against a P-number still resolves. Both would have shipped a defect: P-17's was a SQL statement
both documents published and neither had run, and P-18's was a `Reached: true` for a parked event.

### P-1 — `WaitOf` must derive through `withDefaults(spec)`, not through `ByStream()` alone

[SPEC] §5.2 says `WaitOf` *"applies `ByStream()` where `Spec.Sequence` is nil, which is the default
`New` applies"*. `New` applies that default in **one** place — `withDefaults`
(`event/projection/spec.go:349-383`) — and `ByStream()` is its **tenth** clause. The first clause
is this:

```go
if spec.OnPermanentFailure != ParkSequence {
    spec.Park = nil
}
```

A host that fills in `Spec.Park` beside a policy that is not `ParkSequence` has a projection that
**never writes a letter** — `New` drops the queue. A `WaitOf` that copied `spec.Park` verbatim
would hand `Wait` a `Park` no pass ever writes to, and the failure is not "one wasted count": a
`Park` implementation is contractually allowed to resolve its executor from the ambient unit for
every method except `Sequences`, and [SPEC]'s own widening only moves `Holds`. A `Sequences` that
refuses outside a unit fails the **first** poll, which [SPEC] §1.1 makes **terminal**. So a
perfectly healthy deployment gets its very first wait refused with a store error, for a queue it
does not have.

So: `WaitOf` calls `withDefaults(spec)` and reads the result. One function is the source of both
defaults, a third spelling of either is unwritable, and the drift CLAUDE.md treats as a defect
cannot start. What `WaitOf` does **not** take from it is `Ticks` and `Every`: a projection's
interval is its idle policy and a wait's is a request path's latency budget — two different numbers
that happen to share a type — and `Spec.Idle`'s default (`defaultIdle`) is nothing a request
handler should inherit by accident. Stated on the module page, pinned by
`TestWaitOfDerivesTheSameDefaultsNewApplies`.

### P-2 — `event/receipt` needs a row in `scripts/event_test.go` or the phase's own gate says so

`TestNoEventPackageCostsMoreThanTheSeamItNames` (`scripts/event_test.go:44`) drives
`costOverruns` (`scripts/extensions_test.go:43`), which enumerates **every** package under
`event/` and fails twice for one that is not in the `charged` map:

```go
if len(packages) != len(cost.charged)+1 { … }   // 5 today, 6 after this phase
…
allowance, charged := cost.charged[found.path]
if !charged { complaints = append(complaints, cost.uncharged(found.path)) }
```

[SPEC] never names this file. The row is `eventExtension + "/receipt": ""` — the vocabulary and
nothing else — and the paragraph above the map gains one sentence: *a receipt is a durable record
beside an append, so it costs `event` for the `Commit`, the `Authority` and the `Stream` it
records, and `crud`/`errs` through the vocabulary's own contracts; it reaches no store, no
subsystem and no driver, which is why it is charged at zero and why the `Ledger` is an interface
rather than an implementation.* If that row is absent the section is red rather than quietly
skipped, which is the half a written table can do and a `grep` cannot.

### P-3 — the walks that would police `event/receipt` cannot see it until `make api` has run

`checkedEventPackages` (`scripts/projection_test.go:549`) reads its package list out of
`docs/api/surface.md`, *"so a package that never reached `make api` is not one this file quietly
stops asking about."* Five walks ride on it: `TestNoSnapshotAuthorityIsDeclaredOrPromised`,
`TestCursorIsNeverCompared`, `TestNoExportedFunctionTakesAPositionAndAnswersACursor`,
`TestNoExportedFunctionOrdersOrTakesTwoCursors` and
`TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity`.

So **S3 runs `make api` before it runs `./scripts/`**, in the same section, or five walks report
green having walked five packages instead of six. And the floor moves with it: `listed < 5` becomes
`listed < 6` and the `slices.Contains` assertion gains `eventExtension+"/receipt"`, so a section
dropped from the baseline is reported rather than absorbed. That floor is the only thing in the
repository that can see this class of miss.

### P-4 — the two file-count guards in `scripts/projection_test.go` move with the package

`TestNoCommentInTheProjectionPackagePromisesExactlyOnce` asserts `files < 18` and
`TestNothingInTheProjectionPackageOpensATransaction` asserts `walked < 18`. The package holds
eighteen non-test files today and **twenty** after S2 (`mark.go`, `wait.go`). Both move to 20 in
S2, in the same change as the files. A guard left at 18 passes over a walk that found nine tenths
of the package, which is exactly the failure the guard exists for — P4 hit this and recorded it.
`event/projection/lifecycle_test.go:235`'s stale `walked < 9` is P4 backlog and is **not** touched.

### P-5 — `TestEveryFieldOfAPublishedSpecIsRead` must gain the three new specs and must not gain `Receipt`

The walk (`scripts/projection_test.go:918`) asks two forms, `Spec` and `RedriveSpec`, and reports
any exported field no line of the package reads. This phase publishes three more forms with
fields: `projection.WaitSpec` (nine), `receipt.ClaimSpec` (five), `receipt.ResolveSpec` (four). All
three join it — `WaitSpec` in S2, the receipt pair in S3 with a second arm over the new package,
because `checkedProjection` is hardwired to one path.

**`receipt.Receipt` is deliberately not added, and the reason is on the type.** It is a record the
*ledger* fills and the framework reads back; `RecordedAt` is written by
`statement_timestamp()` and read by the application and by an operator, not by this package. A
walk that demanded a read would force a use that does not exist, which is a test shaping code
rather than measuring it. The exclusion is one line in the walk's own comment so that the next
reader does not add it back.

### P-6 — the zero-`Mark` discriminator rests on a property no conformance section certifies

[SPEC] §1.1: *"positions are drawn from an identity sequence starting at 1, so `at == 0` is never a
real position and is exactly 'not minted'."* Both shipped stores have it — `eventpg`'s `position`
is `GENERATED ALWAYS AS IDENTITY … increment 1` (`event/eventpg/schema.go:253`) and
`eventmemory`'s `publish` increments **before** it assigns (`event/eventmemory/log.go:113-116`) —
and **nothing in `eventtest` asks for it.** A third store whose first position were 0 is
conformant today.

Adding a section for it would move the store contract, which INV-120 forbids and which is the
whole reason phase 5 has no conformance extension. So it is closed at the mint instead, where it
already is: `WaitSpec.Committed`'s fourth refusal answers `ErrUncommitted` for a zero position. A
store that numbered from zero would have exactly one event in its life that could not be waited on,
and it would be **refused** rather than forged into a mark that clears every wait. That is the safe
direction, it costs that store nothing else, and the residue goes to the backlog as `[low]` rather
than into a section.

### P-7 — [SPEC]'s polling-cost arithmetic is wrong for the case a rebuild wait is in

[SPEC] §1.1 states the cost as *"one `Sequences` count plus one `Load` per cover member per poll"*.
`Wait` polls `surveyed` (`event/projection/generation.go:159-190`), and `surveyed` calls
`neverHandedDown` for **every member whose row is fresh** — which is a second `event.Track` plus a
second `Load` against the retirement name (`topology.go:198-…`). So a generation that has not saved
yet — a rebuild just declared, which is the wait a deployment tool makes — costs **two** reads per
member per poll, not one, and the cost falls to one per member as each member records its first
checkpoint.

Nothing is optimised. A "this member was fresh last poll" memo is state a wait may not hold
(INV-112), and the two refusals `surveyed` buys are worth more than the read. What moves is the
**stated** number: the module page carries `1 + (1 or 2) × |cover|` reads per poll with the reason,
and the `Every: 50 ms` arithmetic is worked at the worse end — a four-partition rebuild wait is
`20 × (1 + 8) = 180` reads a second, not 100. A number a page states and a deployment then measures
differently is how a page stops being read at all.

It also answers backlog `## P5` item 10 in passing, which asked for the third thing `surveyed`
does: all members fresh is the **origin** and a nil error, so a wait against a generation that has
never saved polls against position zero and waits, which is right and is derivable only by reading
`Observe`'s contract. It is now on the page.

### P-8 — `Every` stays at 50 ms and the arithmetic goes on the page  *(tension 1)*

A shared poller with fan-out is a `runtime.Runner`, which the host would have to supervise and
which would cost the "nothing when nobody is waiting" property — Reject 1's shape, and it is
refused for Reject 1's reason. Raising the default instead buys a deployment nothing it cannot buy
itself: `Every` is a field, and a request path that wants 200 ms writes 200 ms. What a default
cannot be is *invisible*, so the module page carries P-7's arithmetic, names raising `Every` as the
lever, and says that fifty concurrent waiters on a four-partition cover is a thousand small
indexed `SELECT`s a second — cheap until it is not.

### P-9 — `Moved` is exactly what it says and is evidence of nothing at `Polls == 1`  *(tension 2)*

`Visibility.Moved` stays *"whether `At` changed across this wait's polls"* and gains one sentence
that closes the gap [SPEC] §8.2 names: **with `Polls < 2` it is always false and means nothing**,
because one observation cannot show a change. Requiring "two observations at least `Idle` apart"
is refused: `Idle` is the projection's own field, a request path's deadline is routinely shorter
than it, and a `Moved` that were sometimes withheld would be a second kind of false. The field
stays exactly true and the page says what it is not — *a projector between two slow passes has not
moved either*. Pinned by `TestADeadlineSaysWhichKindOfNotYetItWas`, whose stopped arm asserts
`Moved: false` **and** `Polls > 1`.

### P-10 — the example `Ledger` is the reference implementation, and it says so  *(tension 3)*

`_examples/event-receipts` carries the claim in full — `INSERT … ON CONFLICT (key) DO NOTHING`
**then** the `SELECT`, in that order, in one transaction (**P-17**) — the completing `UPDATE`, the
SQL-side `Horizon` and the sweep. It is named on the module page as **the reference
implementation** rather than an illustration: its statements are the ones the contract describes,
and a ledger that differs from them is the one that owes the argument. What it is not is a
*certified* implementation — see § *The conformance extension* for what stands in for that and what
goes to the backlog. It is a reference only because S5 ran it and S6 pins it byte for byte; the
label is downstream of the measurement, never the other way round, and **P-17** is the phase that
learned why that ordering matters.

### P-11 — retention gets a worked number with its derivation  *(tension 4)*

The module page gives one: **retention must exceed the longest window in which a client may present
the same key again, which for an HTTP idempotency key is conventionally 24 hours**, and a
deployment whose retries are a workflow's rather than a browser's sets it to that workflow's
timeout instead. Refusing to guess would leave every consumer to derive the same sentence; giving
a number without the derivation would have them copy it into a system where it is wrong. The
framework still prunes nothing.

### P-12 — `Repo.Digest` bypasses no policing decorator  *(tension 5)*

Checked rather than assumed. A policing decorator in a consumer's path is a `Store` decorator —
that is the only seam `event.Open` takes — and `Digest` **reaches no store at all**: it runs the
first four of `Append`'s six steps, all of which read values the caller handed in plus
`this.limits`, which was retained at `Bind` from `Store.Limits()` and is already exported on the
`Store` interface. So there is nothing a decorator would have refused and nothing a caller learns
that it could not already read. `TestADigestIsTheBytesThisAppendWouldWrite`'s recording store
asserts zero calls of any kind, which is the half a walk can hold.

### P-13 — the base-state parameter's home is named, and it is the loop's seed  *(tension 6)*

ES-08 and ES-09 are one design and phase 5 implements one half of one clause. The plan records
where the other half goes so the later phase extends rather than writes a second loop:
**`replay`'s lower bound already exists and is its seed.** The loop begins
`this.store.ReadStream(ctx, stream, at)` with `at = 0`, and `ReadStream`'s contract is
*first version `after+1`* — which is the `version > :from` half of the reference's SQL, exactly.
So a snapshot-resumed load is the same function seeded with the snapshot's version and the
snapshot's state, and the `version <= :to` half is what phase 5 adds. Two warnings the later phase
must read, both recorded in D-144 and D-145: the accumulated version (`at += Version(len(page))`)
must be seeded with the base version or the ceiling is computed against a stream that starts at
one; and `checkPage`'s `after` argument is the same seed, so a resumed read checks density from the
snapshot forward and not from version 1 — which is right, and is the sentence that stops somebody
"fixing" it.

### P-14 — `BenchmarkStateAt`'s numbers go to the backlog, not to a decision  *(tension 7)*

[[D-132]] is explicit that a measured **cost** is not a reason. So S5 records the bounded read's
cost at 1 %, 50 % and 100 % of a 100 000-event stream in `EVENTSOURCE_BACKLOG.md` `## P5`, where
ES-09's eventual Gate 1 can find it, and **not** in D-144 or D-145, where it would slowly become
an argument it was refused.

### P-15 — the ownership row is read twice and the third read is refused  *(tension 8)*

[SPEC] §1.1's "first poll and the poll that would reach" stands. A per-poll read buys a faster
refusal for a case the caller cannot act on any sooner, at one more `SELECT` per interval per
waiter on a row every read path of the deployment already contends for. A wait long enough to span
several cutovers is a deployment tool's, and a deployment tool waiting on an arriving generation on
purpose passes `Generations: nil`, which is what that field's nil case is for. What may not move is
the **last** read: that one turns a false `Reached` into a refusal, and
`TestACutoverUnderAWaitIsRefusedRatherThanAnswered` asserts `Active` was read exactly twice so that
dropping either is red.

**Twice over the wait, not twice on two polls — corrected in S4's remediation ([S4 GAPS] GAP-1).**
The shipped `poll` guarded the last read with `if !first`, so a wait that reached on its **first**
poll read the ownership row once, before the park and the census, and never again. That is the path
a caught-up deployment takes every time, and the window the second read closes is open on it
identically: driven live, a cutover committing inside poll 1's census answered
`{Reached:true At:1 Behind:0 Polls:1}` with a nil error while `Active` already resolved to
generation 3. The guard is gone. The last read is unconditional, both reads land on the one poll of
a wait that reaches at once, and the cost is one `SELECT` for that wait. The read count stays
**two**, so this paragraph's own arithmetic and the test that pins it are unchanged.

### P-16 — every refusal `Claim` has is reachable through `Once`, and that is a test  *(tension 9)*

`Once` calls `Claim`, so the refusals are reachable by construction — but "by construction" is what
stops being true on the first refactor. `TestEveryRefusalClaimHasIsReachableThroughOnce` drives
each of `Claim`'s refusals through both doors and asserts the same sentinel comes back, and the
module page and both examples reach for `Once` first. The open-coded form stays for the caller that
must answer a repeat differently from a first write, and it is documented as that rather than as
the shape a reader copies.

### P-17 — the claim is `INSERT` then `SELECT`, and the one-statement mandate was wrong  *(correction, [GAPS] GAP-1)*

[SPEC] published a claim statement in full, this plan adopted it as *the reference
implementation*, and **it does not do what either document said.** It was executed rather than
reasoned about. PostgreSQL **17.9** at `localhost:55432`, two `psql` sessions, the winner holding
its transaction open 3 s so the overlap is certain, on 2026-09-12:

| The loser's side | READ COMMITTED | REPEATABLE READ / SERIALIZABLE | a third caller of the same key, while a repeat holds its unit open 4 s |
|---|---|---|---|
| [SPEC]'s CTE — `DO NOTHING` feeding a `UNION ALL` over the same table | **`(0 rows)`** | `40001`, `ROLLBACK` | — |
| `DO UPDATE SET key = receipts.key RETURNING …, (xmax = 0) AS won` | the held row, `won = f` | `40001`, `ROLLBACK` | **blocked 3035 ms** |
| **`DO NOTHING;` then `SELECT`** — what this plan now adopts | `INSERT 0 0`, then the held row | `40001`, `ROLLBACK` | **34 ms** |

The loser blocked 2037–2040 ms on every arm; one row survived for the key on every arm and it was
always the winner's; the retry after a `40001` answered the held row at both stricter levels. Four
things follow, and each one moved something.

1. **The CTE cannot answer a repeat.** The whole statement shares one snapshot, taken before the
   winner committed; the speculative-insertion wait happens inside it and does not refresh it, so
   the `UNION ALL` branch's `SELECT … FROM receipts` sees nothing either. The ledger answers
   `won == false` beside **no receipt** — which is precisely what
   `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` refuses with `ErrLedger`. The
   published reference implementation is refused by this framework's own door, and `Repeated` is
   unreachable on the losing path. **The read has to be a statement of its own**, because at READ
   COMMITTED a second statement is a second snapshot.
2. **"Holds at every isolation level" is false, and the true sentence is better.** At the two
   stricter levels the loser does not block-and-repeat; it raises SQLSTATE `40001` and its whole
   unit — claim, append, completion — rolls back. Nothing is half-written, nothing is appended
   twice, and the **retry** repeats. [[D-126]] is untouched and is now justified by something true:
   *this framework sets no isolation level anywhere*, and what the level decides is **how a loser
   fails**, not whether two can append. `40001` is already `errs.Retryable()` here
   (`event/eventpg/classify.go:69-79`), so the retry has a home and D-142 names whose job it is.
3. **The defect table's row 1 was inverted.** The `INSERT`-first two-statement spelling measured
   **correct** at READ COMMITTED. Only the `SELECT`-**first** ordering is defective, and it is
   defective for the ordering: its `SELECT` runs before the block, sees nothing, and the caller
   decides it is first and appends — the `INSERT`'s `0` arrives after the decision. The defect row
   is rewritten to name the ordering, which is a defect that was measured.
4. **The one-statement form that *does* work is refused anyway, for a reason that was also
   measured.** `DO UPDATE … (xmax = 0) AS won` answers the loser correctly at READ COMMITTED — and
   takes a row lock on the winner's receipt, which the repeat then holds for the whole of its own
   unit of work, because a claim runs inside the caller's transaction. A third presentation of the
   same key waits **3035 ms** on a caller that is doing nothing with the row; under the two-statement
   form it waits **34 ms**. A retry storm is exactly when a key is presented repeatedly, so that is
   the load-shape the mechanism exists for and the one the lock convoys. Two smaller reasons:
   `DO UPDATE` writes a dead tuple and fires row triggers on every repeat, and its discriminator is
   a system column while `DO NOTHING`'s is the affected-row count every driver already has.

**What the two-statement form gives up, stated rather than discovered later:** a window between
the two statements in which a retention sweep could delete the row the `SELECT` is about to read,
which surfaces as `won == false` beside no receipt and is refused with `ErrLedger`. A horizon
shorter than the age of a row written milliseconds ago is not a horizon (**P-11**), so the window is
closed by retention rather than by the statement — and that is a sentence on the module page, not a
silence.

[SPEC] §1.2, §1.2.1, §UC-217, §5.3's `Ledger` doc comment and §6 items 12 and 14 carry all of
this; `_examples/event-receipts`, S5's fixture and both module pages carry the same SQL; and D-142
states the serialisation point as *the index plus the level's conflict behaviour*, names `40001`
and whose job the retry is, and claims no level-independence.

### P-18 — a `Committed` mark records the spec that minted it, and `Wait` compares it at both doors  *(correction, [GAPS] GAP-4)*

[SPEC] gave `Mark` two rules: `MarkOf` recorded the projection and `Wait` compared it; `Committed`
recorded none, on the argument that *a position is a fact about the log and every projection over
that log reads the same one*. The argument is true about the **position** and says nothing about
the rest of the mark. A `Committed` mark also carries the **sequence keys `spec.Sequence`
answered**, and those are one projection's alone.

The deployment that breaks it is the one this plan's own UC-204 control stands up — two projections
over one log, two `WaitSpec`s built at the composition root, exactly as the module page says to:

```go
mark, err := orders.Committed(ctx, store, commit)   // keys: orders' sequencer's
invoices.Until = mark
vis, err := projection.Wait(ctx, invoices)          // asks invoices' park under orders' keys
```

Every value is of the right type, `WaitOf` derived both specs correctly, and **nothing refuses**.
The position is global so the census comparison is right and the wait reaches; the park question is
asked under a key no letter of `invoices` was ever parked under, `Holds` answers false, and the
caller is told `Reached: true` for an event `invoices` parked. That is verbatim
*«scan checkpoint после parking не доказывает применение события»* — the failure ES-05 exists to
forbid — reached by the one route UC-206's `Park`-nil control does not cover.

So: **`Committed` records `this.Of.Projection()` on the `Mark`, `MarkOf` records
`Barrier.Projection`, and `Wait` refuses at its door, before any store call, when the mark's
projection is not `spec.Of`'s.** One rule, both doors. The sentinel is **`ErrSpec`**, not
`ErrGeneration` and not `ErrTopology`: `event/projection/errors.go:22-23` already rules that
*"a name a spec chose is still ErrSpec, because a name is something a spec names"*, and what
differs here is a name. The comparison is on the **projection name only** and never the generation,
because a barrier of another generation of the same projection is the cutover case UC-211 admits.

A caller that genuinely wants to wait on a second projection derives that projection's own
`WaitSpec` and calls **its** `Committed` — one more `ReadStream`, and the right park asked the
right question. That is on the module page beside the refusal.

**This lands in S2, before `mark.go` is written**, because after S2 the `event/projection` section
of `docs/api/surface.md` and S2's kernel fence are both already recorded. It is pinned by an arm of
`TestAMarkIsMintedOnlyFromANumberAStoreProduced` that mints from one `WaitSpec` and waits on a
second whose `Of` differs, **with the control that the same mark on its own spec reaches**, and
live by an arm of `TestAConfirmedCommandIsVisibleWithoutASleep`, which already stands two
projections up.

### P-19 — the mint reproduces all three of `Repo.checkPage`'s arms, not its first  *(correction, [S2 GAPS] GAP-2)*

[SPEC] §5.2 froze the weaker sentence — *"each page's first envelope's `Stream` and `Version` must
be the ones asked for"* — and S2 implemented it faithfully. It is the **third** route to
*«scan checkpoint после parking не доказывает применение события»*, after UC-206's nil `Park` and
P-18's cross-projection mark, and neither of those covers it.

Driven against the shipped S2 code: a store answering `[a-17 v1 (A), b-42 v2 (ZZZ)]` for the commit
`a-17 v1..v2` made `spec.Committed` answer `mark.At() = 4`, `err = nil` — **another stream's
position** — while the kernel's `Repo.Load` over the **identical page** refused it with
`event: the store failed: [stream waits.order] was read and [stream waits.order] answered`. The
mark lost `B`, the key its own commit produced, and picked up `ZZZ`. With `B` parked, the wait then
answered `Visibility{Reached: true, At: 104}` and a **nil error**: a silent success, with nothing
for a caller to branch on, for a change sitting in the queue.

The premise is not exotic and is not assumed away eleven lines earlier in this repository:
`Repo.checkPage`'s own comment names *"a shared database, a restored dump or another service's
writer"*. *"Ours is simpler"* is not available when the reference is in the tree and its stronger
check is four lines. So `resolved` delegates to an unexported `honest` carrying all three arms —
no page longer than the store's published `StreamPage`, every envelope the stream asked for, every
envelope one version above the one before it — and the doc that called these checks *"the kernel's
own"* now says which method it reproduces and that it reproduces all of it. §5.2 and INV-109 move
to the per-envelope wording in the same change.

### P-20 — the caller's context outranks the poll number, and a poll that answered still answers  *(correction, [S2 GAPS] GAP-1)*

[SPEC] §5.2's five-exit table and its poll-number rule were written as if the only thing that could
end a poll were a store. They collide: a per-request budget shorter than a round trip expires
**during the first** round trip, and S2's `waiting` answered that with the **Unreadable** exit — the
class §5.2 reserves for *"the caller asking wrong"*.

Driven against the shipped S2 code: a 30 ms deadline elapsing inside poll 1 answered
`projection: this topology change is not one this projection can make: the member "orders@2" could
not be read: context deadline exceeded` with `errors.Is(err, ErrNotVisible) == false`; a
cancellation inside poll 1 answered the same class with `Visibility{Polls: 1}` rather than
`ctx.Err()` bare and the zero `Visibility`; **the identical deadline landing on poll 4 answered
`ErrNotVisible`**. The same physical event answered two classes depending on where the clock landed.
So §UC-213's handler — `if errors.Is(err, ErrNotVisible) { serveStale(vis.Behind) }` — did not serve
stale at the moment a loaded deployment most wants it, and its operator was pointed at a cover
misconfiguration that does not exist. The kernel is not the culprit: `Tracker.Load` answers
`context.Canceled` with `errors.Is(err, event.ErrBackend) == false`, so UC-032 §12 holds one layer
down and it was the wait bending it.

So **the context rule wins over the poll number**: a poll whose refusal arrives with
`ctx.Err() != nil` takes the deadline exit or the cancellation exit, on poll 1 exactly as on poll 4.
Two things it does **not** do, and both are controls rather than assertions: a first poll failing
while the budget is **live** is still `ErrTopology` with `Visibility{Polls: 1}`, and a poll that
**answered** still answers — `ErrParked` and `ErrGeneration` are conclusions drawn from rows that
were read and are checked before the context, because a caller can act on a redrive and cannot act
on a timeout. `Wait`'s published doc states the tie-break in one sentence, which neither §5.2 nor
this plan previously did.

**P-20's other half, missed here and corrected in S4's remediation ([S4 GAPS] GAP-2).** Taking the
deadline exit is not the whole of the rule: the poll's own refusal was still *wrapped into* it, and
a store reached with a done context answers its own classification — `eventpg`'s `Checkpoints.Load`
returns the bare `ctx.Err()` when `opened` sees it and `event.Failure(Unclassified, …)` when the
clock lands inside the statement, so the census hands back `ErrTopology` over `event.ErrBackend`
for a cover that is right and a database that is fine. Measured live over a healthy store at a real
interval: **3 of 8** short budgets came back carrying `ErrTopology`, and the locked-table arm
carries `event.ErrBackend` beside it. So a refusal is carried into the deadline only when it is
**not** the caller's own context coming back — `errors.Is(refusal, ctx.Err())` **or**
`errors.Is(event.CauseOf(refusal), ctx.Err())`, because a refusal answers false for
`context.DeadlineExceeded` by design and the cause is the only door onto it. A refusal that is not
the budget is evidence and is still wrapped, which is §UC-244's rule and is now pinned by a control
on both tiers.

### P-21 — a `Ledger` could not bind either of the two values it stores  *(correction, made while implementing S3)*

[SPEC] §5.3 publishes `Key` and `Fingerprint` as structs with unexported fields, whose only
renderings are `"[operation key]"` and `"sha256:"+hex`, and then requires a `Ledger` — written by
the application, in another package — to `INSERT INTO receipts (key, fingerprint, …) VALUES
($1, $2, …)` and to scan both back into a `Receipt`. **Neither direction is expressible on that
surface.** `Key.String` answers the redaction rather than the value, and `NewFingerprint` takes a
`[32]byte` that a `text` column does not hold. The whole point of the interface is that a third
party implements it, and S5's own live fixture and S6's `_examples/event-receipts` are two such
parties.

So the surface gains exactly two names, one per direction, and no more:

- **`func (this Key) Value() string`** — `jobs.LegacyIntent.Value` one subsystem over, and for the
  same reason: a redacted identity needs one named door for the driver, and naming it is what keeps
  every other reader on `String`. Its doc comment says it is the ledger's statement and nobody
  else's.
- **`func ParseFingerprint(raw string) (Fingerprint, error)`** — the other half of `String`, so the
  rendering stays frozen in the one package that owns it rather than being re-derived by every
  implementation. Three refusals, all `ErrSpec`: no `sha256:` prefix, text that is not hexadecimal,
  and a digest that is not thirty-two bytes.

No behaviour moves. [SPEC] §5.3 gains the two lines in the same change, and the fake `Ledger` of
S3's own tests stores the two text forms and reads them back through `NewKey` and
`ParseFingerprint`, so the round trip an implementation needs is exercised by the shape of the
fixture rather than by a test written for it.

---

## The snapshot gate — measured first, and the measurement does not open it

[SPEC] §1.4 takes route (c): ES-09 ships **D-145, an accepted contract with no code**. That is only
honest if the gate was actually asked, so it was asked before this section was planned.

**Measured 2026-09-12**, PostgreSQL 17.9 at `localhost:55432`, Intel i9-10900K, with
`BenchmarkStreamReplay` (`event/eventpg/replay_integration_test.go:85`) — the instrument
[[D-132]] names — over a 120-byte payload, a no-op codec and a no-op fold, three runs each:

| Stream | `-benchtime` | ns/op, run by run | Full replay | ns/event |
|---|---|---|---|---|
| **10 000 events** | `20x`, `-count 3` | 14 881 024 · 13 050 254 · 12 914 297 | **12.91 – 14.88 ms** | 1 291 – 1 488 |
| **100 000 events** | `10x`, `-count 3` | 110 687 007 · 111 301 799 · 108 429 160 | **108.4 – 111.3 ms** | 1 084 – 1 113 |

Three readings:

1. **The instrument is stable, and where it is not the difference is stated rather than rounded
   away.** [[D-132]]:18-21 recorded **10.19–10.93 ms** at 10 000 with a no-op codec and
   **16.25–17.72 ms** with `event.JSON`, and **104.4–170.6 ms** at 100 000. Today's 100 000-event
   figure sits inside that band. Today's 10 000-event figure — 12.91–14.88 ms, on the same no-op
   codec this benchmark uses — is **20–45 % above** D-132's no-op number and below its `event.JSON`
   one, which is a differently loaded machine on a different day and not a regression: the
   per-event cost at 10 000 (1.29–1.49 µs) against 100 000 (1.08–1.11 µs) is the per-call fixed
   cost amortising, and the two agree within 30 % across a factor of ten in stream length. That
   agreement is D-132's own control that the benchmark measures a replay rather than a constant,
   and it holds.
2. **The threshold lands where [[D-132]] said it does.** Against the re-entry trigger of a p99
   `Repo.Load` above **~50 ms**, a 10 000-event aggregate is **3.4–3.9× under** it and a
   100 000-event one is **2.2× over**. At the measured 1.08–1.11 µs/event the crossing is
   **~45 000 events**, inside D-132's recorded "somewhere above 30 000 – 50 000 events". **The
   trigger does not move**, and the slightly slower 10 000 figure does not move it either: the
   crossing is set by the per-event cost at length, which is the 100 000-event number.
3. **And the gate does not open.** `D-132:49`: *"The gate is a measured **need**, not a measured
   cost. **There is no consumer.**"* Everything above is this repository's benchmark over a
   loopback socket; Gate 1 asks for **a named deployment's own p99, in its own environment, with
   its own payloads**. No such deployment exists. ES-09's own «зачем» — *«ускорить длинные
   streams»* — is the cost argument D-132 refuses by name, so route (b) has no argument to stand
   on either.

**So the deferral is planned, and it is a section that writes a decision.** S6 writes D-145 with
the full contract [SPEC] §1.4 specifies — the five bindings compared before deserialisation, the
two components of the state-computation version, the sentence saying nothing detects a forgotten
bump, the observable fallback, the unreadable-event rule, the five constraints inherited from the
reference and the port, and both gates with the statement that neither is met. [[D-132]] is
**amended, not superseded**: its invariant stands, `TestNoSnapshotAuthorityIsDeclaredOrPromised`
stays green and un-narrowed, and D-132 gains a `See also` line pointing at the contract its trigger
opens into. The numbers above go into D-145 as the measurement that was taken and the reason it is
not the one Gate 1 wants — so the phase that eventually opens the gate starts from a number instead
of a re-derivation.

**The cost of deferring is bounded and is stated.** An aggregate must exceed roughly 45 000 events
before a snapshot pays anything at all, `StateAt` gives a caller the bounded prefix that removes
the most common reason to want one, and `BenchmarkStateAt` (S5) records what the bounded read costs
so Gate 1 has both halves.

---

## The kernel baseline move

`check-event-kernel` (`scripts/checks.sh:538`) compares `scripts/event_kernel.sha256` — **152
paths** at HEAD — against a `find` over `event/` outside `event/eventpg`, file by file, and refuses
rather than reporting ok when it cannot ask. Every section records its predecessor **before its
first file is written**:

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s<N>
```

and closes with the manifest fence:

```sh
./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s<N> \
     '<the section'"'"'s allowed set, anchored>' <the section's required paths>
```

`event-kernel-moved` fails on an empty diff as loudly as on an unexpected path, and it reads the
manifest rather than git, so it says the same thing before `git add` and after `git commit`.

**S1, S2 and S3 close that way. S4, S5 and S6 close with the opposite assertion**, because their
whole file list is `event/eventpg` tests, `scripts/`, `_examples/` and `docs/`, and
`event/eventpg/*` is excluded from the manifest by construction (`scripts/checks.sh:530`). Handed
an empty moved set, `event-kernel-moved` fails on purpose, and making it pass would mean touching a
file for the fence's sake. So those three run
`diff -u .git/event_kernel_before_s<N> scripts/event_kernel.sha256` and require it **empty**. Same
evidence, opposite direction, stated here because a later reader will otherwise read it as the
fence being relaxed.

### The complete set of paths phase 5 may add to the manifest

**Modified, `event/`:** `repo.go` (`Digest`, `StateAt`, the bounded loop), `errors.go`
(`ErrVersion`), `token.go` (one comment), `repo_test.go`, `replay_test.go` (extended).
**New, `event/`:** `stateat_test.go`, `digest_test.go`.

**Modified, `event/eventmemory/`:** `doc.go`, `transaction.go` — **comments only**, three of them,
reworded off the word "receipt" so it has one meaning once `event/receipt` exists. No behaviour, no
signature, no test.

**New, `event/projection/`:** `mark.go`, `wait.go`, `mark_test.go`, `wait_test.go`.
**Modified, `event/projection/`:** `errors.go` (four sentinels, "Eight" → "Twelve"), `park.go`
(one widened sentence on `Holds`, and no second one), `doc.go` (the wait's paragraph),
`harness_test.go` (extended).

**New, `event/receipt/`:** `doc.go`, `key.go`, `fingerprint.go`, `receipt.go`, `claim.go`,
`resolve.go`, `errors.go`, and their `_test.go` peers.

**`event/eventtest/`: nothing.** No store obligation moves, so a store certified at the end of
phase 4 is certified at the end of phase 5 unchanged. If a file there moves, the section's report
says which and why — a suite that grew during a phase that claims it did not is a real finding, not
a diff to absorb.

**Forbidden in every section, and checked:** any path under `event/eventtest/`, and in S2 and S3
any path matching `^event/[a-z_]*\.go$` — the `event` package itself moves in S1 and nowhere else.

### The justifying sentence for the re-baseline

> Phase 5 moves the manifest for three reasons and no fourth. **`event` itself moves** because the
> two things ES-07 and ES-08 need are unreachable from outside it: the encoded records a batch
> becomes are built in `Repo.records` behind unexported fields, and the envelope→fold map is
> `Repo.apply` over the unexported `aggregate.facts`, so a caller can neither fingerprint what it
> is about to write nor fold a prefix without re-writing its own type switch and duplicating every
> fold. Two methods and one sentinel is the smallest surface that closes both, and no interface,
> field or signature of the store contract moves with them — which is why `event/eventtest` does
> not move and a third-party store's certified suite does not go red. **`event/projection` moves**
> because a wait is the consumer's question over rows the consumer already owns: it reads
> `Progress.Highest`, which [[D-128]] makes a completeness watermark by kernel law, and it adds
> nothing to the substrate. **`event/receipt` is a new package under `event/`** because a receipt
> must be atomic with an append in the event store's own database — [[D-118]]'s rule rather than an
> exception to it, since *"'Atomic' across two handles is a sentence with no meaning"* — while
> being the application's own table behind an interface this framework declares and does not
> implement, which is the shape `Park` and `Generations` already have and the shape [APX]'s framing
> demands. **`event/eventmemory` moves for three comments and nothing else**, so that "receipt"
> means one thing in this tree.

---

## Coverage matrix

Every UC and INV of [SPEC]. **Section** is where it is delivered; **Checkpoint** is the section
whose command proves it; **Proved by** names the test. **Every name in the Proved-by column is in
the Tests list of the section its Checkpoint column names, and inside that checkpoint's own counted
`-list` pattern.** A name here that no arm counts is a use case that can be reported closed on a
green `go test`, which is phase 1's failure mode exactly — re-run the extraction after any edit to
this matrix.

**The rule is one-directional, and deliberately.** Every name in the Proved-by column must be
inside a counted pattern; a counted pattern may hold names this matrix does not — the repository's
own pre-existing arms that a section re-runs (`TestCursorIsNeverCompared`,
`TestEveryFieldOfAPublishedSpecIsRead`, `TestNoCommentInTheProjectionPackagePromisesExactlyOnce`
and the rest), and the doc checks S6 names in its own prose. **Checked mechanically, not by
reading:** extract every `Test…` from this matrix and from every `-list` pattern below and compare
the two sets. Run it again after any edit to either — it is thirty seconds, and it is the only
thing that holds this rule. It found one breach on its first run — §UC-221's
`TestAResolveReadsNoEventAndOffersNothingToAppendWith`, named here and counted nowhere — which is
closed above.

### Use cases — Group BA, waiting for a change to be visible (ES-05)

| UC | Section | Checkpoint | Proved by |
|---|---|---|---|
| UC-204 a confirmed command is read back and the read does not show the old state | S2 + S4 | **S4** | `TestAConfirmedCommandIsVisibleWithoutASleep` (S4, with the not-running control, the second-projection arm, and **P-18**'s cross-spec mark refused on that second projection) |
| UC-205 a wait reaches on its first poll and never sleeps | S2 | S2 | `TestAWaitReachesOnItsFirstPollAndNeverSleeps` with the one-event-short control |
| UC-206 a parked sequence is named rather than waited out | S2 + S4 | S2 + **S4** | `TestTheParkIsAskedBeforeTheCensusOnEveryPoll` (S2); `TestTheParkedPairIsTheWholeOfTheAppendix` (S4) — the `Park`-nil control is what makes either mean anything |
| UC-207 a sequence parked in one partition while another lags | S2 + S4 | S4 | `TestParkedInOnePartitionWhileAnotherLags` (S4, live four-partition cover) with the not-parked control |
| UC-208 a healthy projection pays one count per poll, and a wait never asks `Holes` | S2 + S4 | S2 + S4 | `TestAHealthyWaitAsksSequencesAndNeverHoldsOrHoles` (S2, recording `Park`, placement asserted, `Quarantined > 0` control); `TestTheCallCountBudgetAndItsPlacement` (S4) |
| UC-209 a mark minted inside the transaction that wrote it | S2 + S4 | **S4** | `TestCommittedRefusesTheSixItCannotMint` (S2); `TestAMarkMintedInsideTheWritingTransactionIsRefused` (S4 — against `eventpg`, where the naive version *works*) |
| UC-210 a commit that is not readable back | S2 + S4 | S4 | `TestCommittedRefusesTheSixItCannotMint` (S2); `TestAMarkMintedInsideTheWritingTransactionIsRefused` (S4, the open-then-rolled-back arms) |
| UC-211 an empty commit, a zero mark, and a barrier of the generation being waited on | S2 | S2 | `TestABarrierMintedMarkIsRefusedBesideAPark`, four arms with the `Park`-nil control |
| UC-212 a deadline elapses, and the answer says which kind of not-yet it was | S2 + S4 | **S4** | `TestADeadlineSaysWhichKindOfNotYetItWas` (S2, fake clock); `TestSlowVersusStopped` (S4) with the cancellation control |
| UC-213 a caller serves stale data, and it is code somebody wrote | S2 + S6 | S2 | `TestThereIsNoFieldThatTurnsARefusalIntoASuccess` — a surface walk over `WaitSpec` plus the module-page arm in S6 |
| UC-242 the three fields a hand-written `WaitSpec` gets wrong | S2 + S4 | **S4** | `TestTheDerivedSpecAnswersWhatThreeHandWrittenOnesGetWrong` (S2); `TestTheDerivedSpecAgainstThreeHandWrittenOnesLive` (S4) |
| UC-243 a commit that spans two sequences | S2 | S2 | `TestCommittedReadsTheCommitsOwnRangeAndNothingElse` with the recording `Park` asserting two `Holds` per poll |
| UC-244 a poll that cannot be made | S2 + S4 | **S4** | `TestAPollThatCannotBeMadeIsTerminalFirstAndPolledThroughAfter` (S2); `TestAPollThatFailsFirstAndAPollThatFailsFourth` (S4, live mid-split cover) |
| UC-245 a cutover completes while a wait is running | S2 + S4 | **S4** | `TestACutoverUnderAWaitIsRefusedRatherThanAnswered` (S2); `TestACutoverThatCommitsWhileAWaitIsRunning` (S4) with the `Generations`-nil control |

### Use cases — Group BB, the operation receipt (ES-07)

| UC | Section | Checkpoint | Proved by |
|---|---|---|---|
| UC-214 a command is claimed, appended and completed in one transaction | S3 + S5 | **S5** | `TestAClaimAnAppendAndACompletionAreOneTransaction` (S3, over `eventmemory`); `TestClaimAppendCompleteAndTheRollbackControl` (S5, rows read in `psql`) |
| UC-215 the same key is presented again with the same content | S3 + S5 | **S5** | `TestARepeatIsAnsweredOnlyFromACompleteRow` (S3); `TestTheLostConnectionRetryEndToEnd` (S5, new process, moved version, fingerprints asserted equal) |
| UC-216 the same key is presented with different content | S1 + S3 + S5 | S3 + **S5** | `TestADigestCollidesOnAByteAStreamAndAnOrder` (S1, the digest half **with the version arm that must not collide**); `TestACollisionTravelsAsAnErrorAndNotOnlyAsAVerdict` (S3); `TestTheCollisionTableLiveBesideTheVersionVariant` (S5 — §6 item 14, the table through a real ledger row, `psql` copies counted) |
| UC-217 two callers race one key | S5 | **S5** | `TestTwoCallersRaceOneKey` (S5) — three isolation levels bound through `crudsql.DB.WithTxOptions`, asserting `Repeated` at READ COMMITTED and a `40001` rollback whose **retry** repeats at the other two (**P-17**), with the winner-rolls-back control |
| UC-218 a ledger on a second pool | S3 + S5 | S3 + S5 | `TestAClaimIsRefusedUnlessTheLedgerAndTheStoreAreOneTransaction` (S3); `TestTheTwoPoolRefusalBesideTheOnePoolAcceptance` (S5) |
| UC-219 a claim outside any transaction, and against a store with none | S3 | S3 | `TestAClaimIsRefusedUnlessTheLedgerAndTheStoreAreOneTransaction`, arms 3 and 4, with the admitted control |
| UC-220 an absent receipt while the writing transaction is still open | S5 | **S5** | `TestUnresolvedWhileOpenAndAfterARollbackAreIndistinguishable` (S5, a second session held idle-in-transaction) |
| UC-221 a receipt that is present and complete | S3 + S5 | S3 | `TestAResolveReadsNoEventAndOffersNothingToAppendWith` with the recording store asserting one call — the transaction question — and no other |
| UC-222 a claim that reached its commit with nobody resolving it | S3 + S5 | S3 + S5 | `TestARepeatIsAnsweredOnlyFromACompleteRow` (S3, the `ErrIncomplete` arm); `TestAnUnresolvedClaimRefusesItsRetry` (S5) with UC-246's completed-empty control |
| UC-223 a receipt older than retention, and the clock the horizon is on | S3 + S5 | **S5** | `TestAnAbsentRowIsUnresolvedAndAHorizonDecidesExpired` (S3, four standings); `TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms` (S5, both horizon spellings) |
| UC-224 a fingerprint is the bytes that would be written, not the command | S1 | S1 | `TestADigestIsTheBytesThisAppendWouldWrite` — two pairs, a recording store asserting zero calls, and the malformed-batch refusals **including the carried one**, opened by the two-unencodable-decisions pair that names the collision |
| UC-225 a key that renders | S3 | S3 | `TestAKeyRendersNothingOfItsValue` with the refusal-message arm |
| UC-226 a ledger that answers something no ledger answers | S3 | S3 | `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` with the conformant control |
| UC-227 a retry after a lost connection, end to end | S5 | **S5** | `TestTheLostConnectionRetryEndToEnd` with **the naive control that writes the events twice** |
| UC-246 a decision that yields no changes | S3 + S5 | S3 | `TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt` (the empty-commit arm) with the no-`Complete` control |
| UC-247 a codec that does not encode the same bytes twice | S3 + S5 | **S5** | `TestACodecThatDoesNotEncodeTheSameBytesTwice` (S5) with the instant-from-the-command control |
| UC-248 a completion that is not the claim's | S3 + S5 | S3 + S5 | `TestACompletionThatIsNotTheClaimsIsRefusedFiveWays` (S3); the same five with `psql` row reads (S5) |
| UC-249 a resolve issued inside the writing transaction | S3 + S5 | S3 | `TestAResolveInsideTheWritingTransactionIsRefused` with the after-the-commit control |
| UC-250 one key, two streams | S5 | **S5** | `TestOneKeyTwoStreamsAndTheKeyPerAppendControl` — the inverted assertion with its two controls |
| UC-251 a caller that ignores a verdict, and one that claims too late | S3 + S5 | **S5** | `TestTwoMisorderedCallersAndTheirOnceControl` (S5, `psql` copies counted on every arm) |

### Use cases — Group BC, historical state at a version (ES-08)

| UC | Section | Checkpoint | Proved by |
|---|---|---|---|
| UC-228 the state before a disputed change | S1 + S5 | S1 | `TestAPrefixFoldsToTheStateItsVersionHolds` with the version-6-against-version-7 control; `TestThePrefixAtEveryBoundaryOfARealStream` (S5, live) |
| UC-229 the bound is respected across a page boundary | S1 + S5 | S1 | `TestABoundedReadStopsAtThePageItNeeds` — recorded page counts at 1, 4, 5, 8, 11 over a `StreamPage` of 4, with `Load`'s three-page control; `TestThePrefixAtEveryBoundaryOfARealStream` (S5) |
| UC-230 a version past the end of the stream | S1 + S5 | S1 | `TestAVersionPastTheEndAndAnEmptyStreamAreOneRefusal` with the at-head control; `TestPastTheEndAnEmptyStreamAndVersionZeroLive` (S5) |
| UC-231 a stream with no events, and version zero | S1 + S5 | S1 | the same test's second and third arms, asserting the two refusals are **the same** refusal; `TestPastTheEndAnEmptyStreamAndVersionZeroLive` (S5) |
| UC-232 an unreadable event inside the prefix | S1 + S5 | S1 | `TestAnUnreadableEventInThePrefixReturnsTheZeroState` — four variants, each against `Load`'s identical refusal; `TestTheUnreadableEventTableLive` (S5) |
| UC-233 a store that answers a page out of order | S1 | S1 | `TestAPageOutOfOrderIsRefusedBeforeItIsTruncated` with the in-order control |
| UC-234 the result cannot be appended with | S1 + S6 | S1 | `TestABoundedReadYieldsNothingThatCanAppend` with `Load`'s signature asserted unchanged; `make api`'s three-line diff is the other half |
| UC-235 there is no timestamp boundary | S1 + S6 | **S6** | `TestNoEventGuideOffersATimestampBoundary` (`scripts/docs_test.go`, with a fixture control) plus the surface walk in S1 |
| UC-236 the store contract did not move | S1 | S1 | the `event`-section diff of `make api` being exactly three added lines, plus `go test ./event/eventtest/` green with its inventory counts unchanged |

### Use cases — Group BD, the snapshot contract (ES-09)

These four are **obligations on the phase that eventually writes the code**, not tests phase 5
runs. They are carried by D-145 and their falsifier is the decision's completeness.

| UC | Section | Checkpoint | Proved by |
|---|---|---|---|
| UC-237 a snapshot is refused before its payload is read | S6 | S6 | D-145 §"what a snapshot is bound to" states all five and the before-deserialisation ordering with Axon's `readSnapshot` quoted as the source |
| UC-238 a fold changes and nobody bumps the version | S6 | S6 | D-145 states *nothing detects it*, in those words, and specifies the inverted control the implementing phase owes |
| UC-239 an unreadable event in the tail is not hidden by the fallback | S6 | S6 | D-145 §"the fallback" states the discriminator is **which call failed** and names the debugging failure if it is got wrong |
| UC-240 snapshot plus tail equals full replay | S6 | S6 | D-145 §"the two gates" states Gate 2's corpus and its two perturbations |
| UC-241 the gate is a document, and phase 5 has not met it | S6 | S6 | `TestNoSnapshotAuthorityIsDeclaredOrPromised` green and un-narrowed with its fixture control still reporting; `docs/api/surface.md` publishing no snapshot name; D-145 existing and recording that neither gate is met |

### Invariants

| INV | Section | Checkpoint | Proved by |
|---|---|---|---|
| INV-108 a wait's target is a minted position, and `Highest` is not restated | S2 + S6 | S2 + S6 | `TestAMarkIsMintedOnlyFromANumberAStoreProduced` (no exported field, two doors, `%v` renders `[mark]`, zero refused, **and P-18's cross-spec mark refused with the same-spec control**); UC-211; UC-243; `TestNoProjectionGuideRestatesHighestAsThePagesLastPosition` (S6, fixture control) |
| INV-109 a mark is resolved after the commit, never inside the transaction that wrote it | S2 + S4 | **S4** | UC-209's pair and UC-210's pair; `TestCommittedReadsTheCommitsOwnRangeAndNothingElse`'s recording store asserting the read count against the commit's size |
| INV-110 the park is asked before the census, every poll, under the mark's keys, and nothing else of the park | S2 + S4 | S2 + S4 | UC-206's pair, UC-207, UC-211's fourth arm, UC-243; UC-208's recording park in **both** directions and the bound-transaction assertion |
| INV-111 the five declared facts are derived, and a hand-written spec carries the obligation | S2 + S6 | S2 | UC-242's four arms; `TestWaitOfDerivesTheSameDefaultsNewApplies` (**P-1**); the module-page arm in S6 |
| INV-112 a wait is read-only, starts nothing, reuses no tracker across a save | S2 + S4 | S2 + S4 | `TestAWaitStartsNothingAndSavesNothing` (recording `Checkpoints`: no `Save`, no `Forget`, `Load` per member per poll); `startsNothing` over the new files; `TestAWaitBesideTheProjectionsOwnLoop` under `-race` (S4) |
| INV-113 what a reached wait promises, what it does not, and what every other exit means | S2 + S4 + S6 | **S4** | UC-204's second-projection arm; UC-206's `Park`-nil arm; UC-245's three arms with the nil control; UC-244's two failure shapes; the five negative sentences on the module page (S6) |
| INV-114 a claim is one transaction with its append at all three doors, or it is refused | S3 + S5 | **S5** | UC-218's pair, UC-219's two, UC-248's five with the admitted control, UC-249's mirror with its control, UC-214's rollback control, UC-217's race — **read in `psql`, not from Go**; `TestEveryRefusalClaimHasIsReachableThroughOnce` (S3, **P-16**) and `TestFourLedgerDefectsEachBreakTheCaseThatNamesThem` (S5) |
| INV-115 an absent receipt is never a rollback, and the horizon is the ledger's own clock | S3 + S5 | **S5** | UC-220's three arms asserting the first and third are indistinguishable; UC-223's four standings and two skew arms; `TestThePublishedClaimBindsNoInstantAsAParameter` (S5, reading the example's SQL) |
| INV-116 a fingerprint covers one append's whole range and nothing a store or a load assigns | S1 + S3 | S1 | UC-216's collision table beside its **non**-colliding version variant; `TestTwoAttemptsAtDifferentVersionsDigestEqual`; UC-224's two pairs; the recording store asserting `Digest` made no call; and `TestTheDigestPreimageIsFrozen`, the six golden vectors that make the preimage a value rather than a description — the rest of the file is relative and a whole change of layout preserves injectivity |
| INV-117 a repeat answers from a complete receipt, and an unresolved one answers nothing | S3 + S5 | S3 + S5 | UC-215, UC-222's pair, UC-246's completed empty range, UC-227's end-to-end retry with its naive control; `TestNoReceiptTypeExposesAnAppendToken` (surface walk, S3) |
| INV-118 a bounded read refuses rather than returning a partial state | S1 | S1 | UC-230, UC-231, UC-232 (asserting `Load` gives the identical refusal), UC-233's out-of-order page |
| INV-119 a historical read yields nothing that can append, and the boundary is half-open at neither end | S1 | S1 | UC-234's signature check beside `Load`'s unchanged one; UC-228's 6-against-7; UC-229's recorded page counts |
| INV-120 the store contract and the conformance inventory do not move | S1 + S2 + S3 | S1 | UC-236; `make api`'s `event`-section diff of exactly three added lines; `event/eventtest`'s own inventory tests green with unchanged counts; and every section's `event-kernel-moved` allowed set excluding `^event/eventtest/` |
| INV-121 what a snapshot is bound to, and when the comparison is made *(contract)* | S6 | S6 | D-145 existing and stating all five bindings, the ordering, and the undetectable bump — falsified for phase 5 by D-145 omitting any of them |
| INV-122 the fallback is the snapshot's alone, and it is observable *(contract)* | S6 | S6 | D-145 stating both clauses — falsified for phase 5 by either being absent |
| INV-123 nothing this phase adds starts anything, opens a transaction, or writes a line | S2 + S3 | S3 | `startsNothing` over `event/projection` and `event/receipt`; `TestNothingInTheProjectionPackageOpensATransaction` widened to `event/receipt` with its fixture control; `TestNoEventPackageCostsMoreThanTheSeamItNames` showing `event/receipt` reaching `crud`, `errs`, `utils` and nothing else (**P-2**); a recording source asserting no `Begin`, `Commit` or `Rollback` on any path |
| INV-124 every refusal names its field, wraps a published sentinel, and names no data | S1 + S2 + S3 | S3 | `TestNoRefusalOfABoundedReadNamesAVersion` (S1), `TestNoRefusalOfAWaitNamesAPositionOrAKey` (S2), `TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage` (S3) — each in the shape `event/refusalmessages_test.go` already has, asserting the input never appears in the output |
| INV-125 a decision must encode to the same bytes under one key, and nothing here can check it | S3 + S5 + S6 | **S5** | UC-247's pair; the obligation on `Repo.Digest`'s doc comment and on both module pages beside `Sequencer`'s three, held by a doc check (S6) |
| INV-126 one operation key covers one append, to one stream | S3 + S5 | **S5** | UC-250's inverted assertion with its two controls; UC-248's cross-stream refusal; the key-per-append recipe on the module page, held by a doc check (S6) |

### The appendices, and where each is delivered

| Appendix | Delivered by | The clause of its *адаптация* that decides the shape |
|---|---|---|
| **ES-05** waiting for a specific change in a read model | **S2** (the value, the doors, the poll loop) + **S4** (live) + **S6** (the page, D-144's sibling prose) | *«ждать подтверждённую stream/version либо store-issued barrier конкретного projection generation, а не "пока lag станет нулём". Scan checkpoint после parking не доказывает применение события»* — which is why the park is asked **first**, on **every** poll, under the keys the mark carries |
| **ES-07** finding out what happened to an uncertain append | **S1** (`Repo.Digest`) + **S3** (`event/receipt`) + **S5** (live) + **S6** (D-142, D-143, the page, the example ledger) | *«Отсутствующая квитанция не доказывает rollback, пока исходная транзакция не разрешилась»* — the third answer, `Unresolved`, and the horizon that keeps a swept row from reading as one |
| **ES-08** historical state at a version | **S1** (`Repo.StateAt`, `ErrVersion`) + **S5** (live, `BenchmarkStateAt`) + **S6** (D-144, the page) | *«Неподдерживаемая revision или отсутствующий префикс возвращают отказ, не частичное состояние»* — four refusals and no second return value |
| **ES-09** snapshots that are compatible, with a safe fallback | **S6 only — D-145, a contract with no code** | *«Оптимизированный load требует отдельного принятого контракта, а не незаметного изменения гарантий [[UC-032]]»* — and § *The snapshot gate* is the measurement that says the trigger is not met |

### The §6 item → test map

The coverage matrix maps use cases; **this maps [SPEC] §6's twenty-four live items**, and it is the
second mechanical extraction, run beside the matrix one and re-run after any edit to either. It
exists because the round-1 audit found item 14 unmapped inside a sentence asserting *"items 11–24"*
— the arithmetic came out right because four S5 tests are obligations of this plan rather than §6
items, so a count could balance over a hole. **No item may be unmapped.** A count of tests is not
evidence for an item; this table is.

| §6 | What it proves | Test | Section | Tagged |
|---|---|---|---|---|
| 1 | a confirmed command visible without a sleep | `TestAConfirmedCommandIsVisibleWithoutASleep` | S4 | yes |
| 2 | the parked pair | `TestTheParkedPairIsTheWholeOfTheAppendix` | S4 | yes |
| 3 | parked in one partition while another lags | `TestParkedInOnePartitionWhileAnotherLags` | S4 | yes |
| 4 | a mark minted inside the writing transaction | `TestAMarkMintedInsideTheWritingTransactionIsRefused` | S4 | yes |
| 5 | slow versus stopped | `TestSlowVersusStopped` | S4 | yes |
| 6 | the call-count budget and its placement | `TestTheCallCountBudgetAndItsPlacement` | S4 | yes |
| 7 | a wait beside the projection's own loop under `-race` | `TestAWaitBesideTheProjectionsOwnLoop` | S4 | yes |
| 8 | the derived spec against three hand-written wrong ones | `TestTheDerivedSpecAgainstThreeHandWrittenOnesLive` | S4 | yes |
| 9 | a poll that fails first and a poll that fails fourth | `TestAPollThatFailsFirstAndAPollThatFailsFourth` | S4 | yes |
| 10 | a cutover that commits while a wait is running | `TestACutoverThatCommitsWhileAWaitIsRunning` | S4 | yes |
| 11 | claim, append, complete in one transaction | `TestClaimAppendCompleteAndTheRollbackControl` | S5 | yes |
| 12 | two callers race one key, three levels | `TestTwoCallersRaceOneKey` | S5 | yes |
| 13 | the two-pool refusal and the five completion refusals | `TestTheTwoPoolRefusalBesideTheOnePoolAcceptance` | S5 | yes |
| **14** | **the collision table beside the version variant** | **`TestTheCollisionTableLiveBesideTheVersionVariant`** | **S5** | **yes** |
| 15 | `Unresolved` while open, and after a rollback | `TestUnresolvedWhileOpenAndAfterARollbackAreIndistinguishable` | S5 | yes |
| 16 | `Expired`, `Unresolved`, zero `Issued`, two skew arms | `TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms` | S5 | yes |
| 17 | the lost-connection retry with its naive control | `TestTheLostConnectionRetryEndToEnd` | S5 | yes |
| 18 | an unresolved claim refusing its retry | `TestAnUnresolvedClaimRefusesItsRetry` | S5 | yes |
| 19a | one key, two streams, and the key-per-append control | `TestOneKeyTwoStreamsAndTheKeyPerAppendControl` | S5 | yes |
| 19b | the two misordered callers and their `Once` control | `TestTwoMisorderedCallersAndTheirOnceControl` | S5 | yes |
| 20 | the prefix at every boundary of a real stream | `TestThePrefixAtEveryBoundaryOfARealStream` | S5 | yes |
| 21 | past the end, empty stream, version zero | `TestPastTheEndAnEmptyStreamAndVersionZeroLive` | S5 | yes |
| 22 | the unreadable-event table | `TestTheUnreadableEventTableLive` | S5 | yes |
| **23** | the out-of-order page | `TestAPageOutOfOrderIsRefusedBeforeItIsTruncated` | **S1** | **no** |
| **24** | the bounded read's cost at 1 %, 50 %, 100 % | `BenchmarkStateAt` | S5 | yes, **benchmark** |

**Three rows do not behave like the rest, and each is stated rather than absorbed.**

- **Item 14 gains a tagged arm rather than a recorded refusal**, and the count moves 15 → 16 in S5
  and 25 → 26 overall. The easy argument for leaving it untagged is that `Repo.Digest` reaches no
  store (**P-12**), so a live digest re-measures a pure function. That argument is right about the
  digest and wrong about the item: §6 item 14's live half is the table travelling **through a real
  `Ledger` row into a verdict** — the three that differ answering `Collided` with `ErrCollision`,
  the version variant answering `Repeated` with the first attempt's range, and `psql` showing one
  copy of the events on every arm. That is `Claim`'s fingerprint comparison against a committed
  PostgreSQL row, which no untagged test touches, and it is the arm that exercises the corrected
  claim statement (**P-17**) on its repeat path. The pure half stays in S1 and the two together are
  what make "the content decided it" measured at both levels of the design.
- **Item 23 is untagged in `event`, and §6's own sentence is why**: *"which needs a deliberately
  broken store and therefore runs in `event` against `eventmemory`'s defect harness"*. A store that
  answers a page out of order is not a state PostgreSQL can be put into; decorating `eventpg` to
  fake one would prove the decorator. The routing is §6's, not this plan's, and it is recorded here
  so no close-out reads "items 11–24, tagged" over it.
- **Item 24 is a `Benchmark` and the `-list '^Test…'` arms cannot see it.** S5's checkpoint runs it
  through its own `-bench '^BenchmarkStateAt$'` arm. A counting arm that silently covered 23 of 24
  is the shape this map exists to prevent.

**The arithmetic, named rather than asserted:** twenty-six tagged tests in `event/eventpg` = S4's
ten + S5's sixteen. Twenty-three of them carry twenty-two §6 items (item 19 takes two tests). The
other three are obligations of this plan that §6 does not list —
`TestThePublishedClaimBindsNoInstantAsAParameter` (INV-115),
`TestACodecThatDoesNotEncodeTheSameBytesTwice` (UC-247, INV-125) and
`TestFourLedgerDefectsEachBreakTheCaseThatNamesThem` (§ *The conformance extension*). Items 23 and
24 are the two above. 22 + 2 = 24.

**Checked mechanically, like the matrix, and it is the same thirty seconds.** Extract every
`^N. **` item number from [SPEC] §6, extract every row of the table above, and assert the two sets
are equal; then assert every test the table names is inside a counted `-list` pattern of the
section its row names, with `BenchmarkStateAt` satisfied by the `-bench` arm instead. Run against
this plan on 2026-09-12 it reports **24 items, 24 mapped, none unmapped, every test counted**. Run
it again after any edit to either table — the first run of it is what found item 14.

---

## Contracts before code

Every signature below is written before the code, by file and by package. Receiver name is `this`
throughout. Comments are exceptional — the ones quoted are the ones a genuinely complex function or
an invariant the code cannot make visible earns. Every doc comment is [SPEC] §5's; what this
section adds is the **file**, the **export rationale** and the **manifest consequence**.

### `event/repo.go` — two methods  *(modified · moves the manifest)*

```go
func (this *Repo[S, ID]) Digest(at At[S], changes ...Change[S]) ([32]byte, error)

func (this *Repo[S, ID]) StateAt(ctx context.Context, id ID, version Version) (S, error)
```

**Why exported, `Digest`.** The bytes an append would write are not reachable from outside this
package: `Change[S]` carries its encoded payload in unexported fields and `records`
(`event/repo.go:175-192`) is where a batch becomes `[]Record`. A caller that fingerprinted anything
else — the domain command, the `Change` values' Go representation — would fingerprint a thing the
store never sees. It runs the first four of `Append`'s six steps, **issues no store call and writes
nothing** (P-12), and answers the same refusals `Append` would, so a caller that digests first
learns a malformed append before it claims anything.

**Why exported, `StateAt`.** Same argument one level over: `Aggregate.Fold` folds decided `Change`
values, not `Envelope`s, and the envelope→fold map is `apply` (`:260-272`) over the unexported
`aggregate.facts`. An application cannot build an at-version replay out of the public surface
without duplicating every fold. **There is no second return value**, and that is the design: a
value that looked like a load token would invite a `Load → Decide → Append` whose decision was made
against the history the call left out.

**The loop.** `replay` gains one parameter and stays one function — INV-118's *"the failure path is
`replay`'s own and is not a second implementation"*:

```go
// upTo is the version the fold stops at, and zero means the end of the stream,
// which is what a Load asks for. Two orderings inside are load-bearing: the page
// is checked whole before anything is discarded, because a page returned as
// [v3, v1, v2] truncated first passes a check that only sees what it was given
// and folds to a state that is wrong at the right version; and the loop stops
// reading the moment the accumulated version reaches the bound, folding nothing
// above it.
//
// The over-read is at most one page whatever the stream's length: a bound at
// version 7 of a 100 000-event stream reads one page and discards the rest of
// it. A ceiling on the store contract would buy one partial page of I/O and cost
// every store author a signature, a conformance section and a re-certification.
func (this *Repo[S, ID]) replay(ctx context.Context, stream Stream, upTo Version) (S, Version, error)
```

`Load` calls `replay(ctx, stream, 0)`. The lower bound is already the loop's `at` seed — see
**P-13**, which is where ES-09's `version > :from` half lands.

### `event/errors.go` — one sentinel  *(modified · moves the manifest)*

```go
// request class, beside ErrKey
ErrVersion = fmt.Errorf("event: the version this read was bounded at is not one this stream holds: %w", crud.ErrBadRequest)
```

**Why the request class.** A version a caller asked for and the stream does not hold is data only
the caller can correct, and an undeclared wrap falls through to `errs.KindInternal` — a 500 for a
client error. `vocabulary()` gains the name in the same change, or `refusal.Is`'s cross-class
guard stops covering it.

### `event/token.go`, `event/eventmemory/doc.go`, `event/eventmemory/transaction.go` — three comments

`token.go:23` (*"answers on an empty receipt"*), `eventmemory/doc.go:24` (*"The commit receipt does
not"*) and `eventmemory/transaction.go:35-36` (*"every commit receipt … comparing receipts later"*)
become "commit" or "commit token". `event/eventtest`'s local variables named `receipt` are **left
alone**: they are locals in a package that never imports the new one, and renaming eleven
identifiers would put a large no-behaviour diff into the baseline re-record beside the changes a
reviewer needs to read.

### `event/projection/mark.go` — the value a wait waits for  *(new · moves the manifest)*

```go
type Mark struct { ... }            // unexported fields only

func (this Mark) At() event.Position
func (this Mark) Zero() bool
func (this Mark) String() string    // "[mark]"

func MarkOf(barrier Barrier) Mark
```

**Why exported, and why opaque.** A caller that could write one would be waiting for evidence it
invented — the door `Cutover` already closes by having no barrier field
(`generation.go:299-302`). Both minting doors take a number a store produced. What a mark carries
differs by door and decides what a wait may promise: `WaitSpec.Committed` attaches the distinct
sequence keys the spec's own sequencer answered for **every** envelope of the commit, in
first-appearance order, so the park can be asked about this caller's own change; `MarkOf` attaches
none, because a barrier is folded from checkpoint rows and there is no envelope to ask. It holds
**no `Envelope` and no payload**: the keys are computed once, at the mint, rather than by running
the application's sequencer on every poll, and `String()` answers `"[mark]"` for the reason
`Backing` and `Authority` do. The zero value is refused at `Wait`'s door (**P-6**).

**Both doors record the projection they minted from, and that is one rule rather than two**
(**P-18**). `Committed` stores `this.Of.Projection()`; `MarkOf` stores `Barrier.Projection`; `Wait`
refuses with `ErrSpec`, at its door and before any store call, a mark whose projection is not
`spec.Of`'s — comparing the **name only**, never the generation, because a barrier of another
generation of the same projection is the cutover case UC-211 admits. The field is a `string` and
not an `Identity`: a `Barrier` carries a name and a generation and no partition, so an `Identity`
would have to be invented at the `MarkOf` door to hold a value only half of it can fill.

### `event/projection/wait.go` — the spec, the poll and the verdict  *(new · moves the manifest)*

```go
func WaitOf(spec Spec, over Cover) (WaitSpec, error)

func (this WaitSpec) Committed(ctx context.Context, store event.Store, commit event.Commit) (Mark, error)

type WaitSpec struct {
	Checkpoints event.Checkpoints
	Park        Park
	Sequence    Sequencer
	Generations Generations
	Of          Identity
	Over        Cover
	Until       Mark
	Every       time.Duration
	Ticks       runtime.Ticks
}

type Visibility struct {
	Reached     bool
	At          event.Position
	Behind      event.Position
	Moved       bool
	Quarantined uint64
	Parked      bool
	Polls       int
}

func Wait(ctx context.Context, spec WaitSpec) (Visibility, error)
```

**Why `WaitOf` is exported and is the spelling the page gives.** Three of the five facts a wait
needs are silently wrong-able by a request handler that does not read the projector's wiring — a
`Sequence` that is not the projection's reports reached for a parked event, a nil `Park` reports
delivered where the caller asked applied, and an `Over` that is not the cover the rows are recorded
at folds a minimum over the wrong set. `WaitOf` derives all five from the `Spec` the runner was
built from, **through `withDefaults`** (**P-1**), performs no I/O, starts nothing, and refuses the
zero `Cover` and a spec that names no identity. The struct stays assemblable by hand for the reason
`Cover` gives for its own, and that is where the three obligations live.

**Why `Committed` is a method and not a free function.** The sequence keys must come from the
sequencer the projection runs, and the spec is where that is; a free function would have taken one
as an argument, which is one more value a request handler can get wrong. **It is also what makes
P-18's guard possible**: a method has a `this.Of` to record, and a free function would have had
nothing to stamp the mark with. It reads the commit's **whole range** — one `ReadStream` for a
commit that fits a page, one more per page beyond — and carries six refusals: an empty commit, a
transaction of this store's bound to `ctx`, a store that does not show `commit.Last()`
(`ErrUncommitted`), a zero position (`ErrUncommitted`), a page the store's own answer is not honest
about — longer than its published `StreamPage`, or carrying **any** envelope that is another
stream's or is not one version above the one before it (`event.ErrBackend`, **P-19**) — and a nil
`Sequence` beside a non-nil `Park` (`ErrSpec`).

**`Wait`'s three door refusals, before any store call**, all `ErrSpec`: the zero `Mark`; a mark
whose projection is not `spec.Of`'s (**P-18**); and a barrier-minted mark beside a non-nil `Park`,
which has no sequence key to ask the park about.

**Why `Visibility` has no stale-read field.** A wait that did not reach returns a filled-in
`Visibility` beside a refusal, so serving stale data is `if errors.Is(err, ErrNotVisible) {
serveStale() }` — a branch a reviewer can see — rather than a flag every caller ends up passing.

### `event/projection/errors.go` — four sentinels  *(modified · moves the manifest)*

```go
ErrNotVisible  = errors.New("projection: this change was not visible in this generation's read model within the deadline this wait was given")
ErrParked      = errors.New("projection: the sequence this change belongs to is parked, so the scan passed it and the read model never received it")
ErrUncommitted = errors.New("projection: this store shows no event at the version this commit reports, so the transaction that wrote it has not committed or it rolled back")
ErrGeneration  = errors.New("projection: this wait names a generation that is not the one reads of this projection resolve to")
```

The file's opening comment moves from *"Eight, and none of them crosses a store seam"* to
**"Twelve"**, and the four new ones are described where the eight are.

### `event/projection/park.go` — one sentence, named  *(modified · moves the manifest)*

`Holds`'s contract gains that **a wait also asks it outside a unit of work, and an implementation
must answer the committed state there**. Nothing else about `Park` changes: `Sequences`'s
outside-a-unit rule is unchanged, `Park` itself is a pass's write, and **`Holes` is not asked by a
wait at all**. The `Sequences`-first gate means a projection that has parked nothing never reaches
the widened clause.

**It is announced in four places because no automated gate can see it**: here, on
`docs/modules/{en,ru}/projection.md`, in `docs/release-notes/v0.1.0.md`, and in D-144's sibling
paragraph. `check-event-kernel` watches file digests and `make api` watches signatures; a sentence
of contract is invisible to both. **One sentence, and it is `Holds`'s.**

### `event/receipt` — the new package  *(new · moves the manifest)*

Package `receipt`. First-party closure: `event`, `crud`, `errs`, `utils`. No third-party import,
so the root module is unchanged ([[D-033]]), and the dependency row is **P-2**.

| File | Holds |
|---|---|
| `doc.go` | the package's one opening paragraph, including the sentence that gives the word its meaning here beside `event.Commit`'s |
| `key.go` | `Key`, `NewKey`, `Key.Zero`, `Key.String` — renders `"[operation key]"` and never its value — and `Key.Value`, the ledger's own door (**P-21**) |
| `fingerprint.go` | `Fingerprint`, `NewFingerprint`, `ParseFingerprint` (**P-21**), `Equal`, `Zero`, `String` (`"sha256:"`+hex) |
| `receipt.go` | `Receipt` (the durable row) and `Ledger` (the application's table, five methods) |
| `claim.go` | `Verdict` and its three members, `ClaimSpec`, `Held`, `Claim`, `Once`, `Held.Verdict`, `Held.Receipt`, `Held.Complete` |
| `resolve.go` | `Standing` and its four members, `ResolveSpec`, `Resolution`, `Resolve` |
| `errors.go` | `ErrSpec`, `ErrCollision`, `ErrIncomplete`, `ErrLedger` |

```go
type Ledger interface {
	Transaction(ctx context.Context) (event.Authority, error)
	Claim(ctx context.Context, receipt Receipt) (held Receipt, won bool, err error)
	Complete(ctx context.Context, receipt Receipt) error
	Find(ctx context.Context, key Key) (Receipt, bool, error)
	Horizon(ctx context.Context) (time.Time, error)
}

func Claim(ctx context.Context, spec ClaimSpec) (Held, error)
func Once(ctx context.Context, spec ClaimSpec, work func(context.Context) (event.Commit, error)) (Held, error)
func (this Held) Complete(ctx context.Context, commit event.Commit) error
func Resolve(ctx context.Context, spec ResolveSpec) (Resolution, error)

// P-21 — the two the Ledger cannot be written without.
func (this Key) Value() string
func ParseFingerprint(raw string) (Fingerprint, error)
```

**Why a package and not a file in `event`.** A receipt is not a store capability and must not become
one: `eventpg` stays at `SchemaVersion = 2`, no fifth table, no migration, no profile decision
([[D-101]]/[[D-127]]), and a deployment that does not want receipts deploys nothing. It is also
what makes the atomicity a **checked** property — `Claim` compares `Ledger.Transaction(ctx)` with
`Store.Transaction(ctx)` through `Authority.Same`, which is phase 4's `checkUnit`
(`event/projection/pass.go:932-957`) reused for a second purpose.

**Why `Held` holds an unexported pointer.** So a second `Complete` on one claim is refusable at
all, and so a copy of a `Held` is the same claim rather than a second one.

**Why `Claim` returns an error for `Collided` and for an unresolved prior claim.** A verdict is a
value it is legal to discard and a refusal is not: the worst code that compiles —
`held, err := Claim(...)`, `if err != nil`, then `Append` — must refuse rather than spend a
stranger's key. `Repeated` stays ignorable and is caught one call later, because `Complete` refuses
a verdict that is not `Recorded` and the caller's `crud.InNewTx` rolls the second copy of the events
back. **The transaction is the enforcement**, which is the only enforcement a package that opens no
transaction can honestly have.

**Why `Standing` is a second enum and not four more `Verdict` members.** A claim holds the
primary-key index and never sees `Unresolved`; a resolve writes nothing and never sees `Recorded`.
One enum with seven members would let each door return the other's.

### What is deliberately absent from the surface

`AllowStale`, `Stale`, `OnTimeout` or any field that turns a wait's refusal into a success. A
`Prefix` or `Provenance` second return on `StateAt`. A timestamp parameter, overload or option
anywhere. A version ceiling on `Store.ReadStream`. A fourth `Resolve` standing that proves a
rollback — the `pg_current_xact_id`/`pg_snapshot_xmin` proof was designed and is refused with its
cost in D-143, so it is not re-proposed. A new `event.Outcome` member. Any identifier matching
`(?i)snapshot`, anywhere under `event/` or in `docs/api/surface.md`.

---

## The conformance extension

**The store contract does not move, and phase 5's conformance work is proving that rather than
extending it.** `Wait` uses `Checkpoints.Load` through `event.Track` and `Store.Transaction`;
`Committed` uses `ReadStream` and `Transaction`; `StateAt` uses `ReadStream`; `receipt` uses
`Store.Transaction` and a `Ledger` that is nobody's store. Every one of those is inside the contract
as it stands, so `eventtest.inventory()` keeps its **twenty** sections with the same `needs` gates,
`RunCheckpoints` keeps its **fourteen**, and `event/eventpg/census_integration_test.go`'s
`checkpointCensus()` does not move. That is the opposite of phase 4 and is stated rather than left
to be inferred from an unchanged file.

**Three arms hold it, and two of them already exist:**

| Claim | Held by | Where |
|---|---|---|
| the exported store surface did not move | `make api`'s `event`-section diff being exactly three added lines, against a predecessor captured before S1's first file | S1 checkpoint |
| the suite's inventory did not move | `event/eventtest`'s own counts (`inventoried` at 29, `checkpointDefects` at 8, the certified-section assertions) green **unchanged** | S1, S2, S3 checkpoints |
| the suite's **files** did not move | every section's `event-kernel-moved` allowed set excludes `^event/eventtest/`, so a file edited there fails the section | S1, S2, S3 |

### The `Ledger` is a third-party-implementable interface, and it gets defects rather than a suite

`receipt.Ledger` is the one genuinely new contract a third party will implement, and its
obligations are exactly the kind `eventtest` exists for: the claim is **an `INSERT … ON CONFLICT
(key) DO NOTHING` followed by a `SELECT` of the same key, in that order, in one transaction**
(**P-17**), the horizon is monotone and comes from the database's clock, `Find` sees committed rows
only, and `Claim` and `Complete` run inside the caller's transaction while `Find` and `Horizon` run
outside one.

A published `receipttest` harness is what would certify them, and **it is not in this phase** — the
same call, with the same reasoning, that left the park's byte bound uncertified in phase 4
(backlog `## P4` item 70). Publishing a conformance module for `Ledger` alone while `Park`,
`Generations` and `Effects` — three shipped application interfaces of the same shape — have none
would be a suite chosen by recency rather than by risk, and doing all four is a phase of its own.

**What phase 5 ships instead is the falsifying half without the package.** S5 runs a ledger whose
statements are the reference implementation's (**P-10**) against **four decorators**, each a shape
a real implementation reaches by writing one thing wrong, and each asserted to break the case
that names it.

**The ledger S5 runs is a fixture in `event/eventpg`, not an import of `_examples`**, and it has to
be: `_examples` is an unpublished module the toolchain ignores at the root, so a tagged test under
`event/eventpg` cannot reach it. What keeps the two from drifting is S6's
`TestTheExampleLedgerIsTheOneTheLiveSuiteProved`, which compares **both statements of the claim,
in order**, against S5's fixture **byte for byte** — so "the reference implementation" is a fact the
live suite proved rather than a label the page applies. Comparing only the `INSERT` would be the
one thing this test must not do: **P-17** is a finding about the order, so an example whose `SELECT`
moved would pass a test that only looked at the first statement.

| Ledger defect | The one thing written wrong | The case it must break |
|---|---|---|
| `select-first claim` | the `SELECT` issued **before** the `INSERT … ON CONFLICT DO NOTHING` rather than after it — same two statements, same transaction, wrong order (**P-17**) | `TestTwoCallersRaceOneKey` — the loser's `SELECT` runs before the block, sees nothing, answers `Recorded`, and **both append**; `psql` shows two copies |
| `process clock` | `recorded_at` bound as a parameter from `time.Now()` instead of `statement_timestamp()` | `TestThePublishedClaimBindsNoInstantAsAParameter`, and the skew arms of `TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms` |
| `optimistic horizon` | `Horizon` answering `MAX(recorded_at)` instead of `MIN` | `TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms` — a swept row reads as one that never existed |
| `dirty find` | `Find` on the claiming connection instead of a fresh one | `TestUnresolvedWhileOpenAndAfterARollbackAreIndistinguishable` — `Found` for an operation that can still roll back |

**The first row is the one the round-1 audit inverted, and the correction is the point.** The plan
used to call *two statements* the defect and *one statement* the contract; the database says the
statement count is not the variable and the **order** is. A fifth decorator — the CTE [SPEC] used
to publish — is deliberately **not** added: it does not break a case, it breaks the door, answering
`won == false` beside no receipt and being refused by
`TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` before any case runs. That it is caught
at all is worth one sentence in D-142 and not a decorator.

Each decorator is asserted to make its named case **fail**, in the shape
`test/integration/gate_relscope_test.go` uses: without that assertion the four positive cases pass
whether or not the ledger's statements are the ones the contract describes. The published harness
goes to the backlog as `[medium]` with this paragraph as its argument.

---

## Sections

Statuses: `[ ]` not started · `[~]` partial (must carry `MISSING:`) · `[x]` done, checkpoint
executed · `[!]` blocked (must carry `BLOCKED BY:`).

**Ordering rule: no section leaves the tree red.** `go build ./...` and `go vet ./...` pass after
every one, and `make unit` stays as green as HEAD's one foreign red allows, because every live
test is behind `//go:build integration`.

**Every checkpoint names its tests and counts them before running them.** Each `-run` clause is
preceded by `test "$(go test -list '<the same anchored pattern>' … | grep -c '^Test')" = <N>`,
patterns written `'^(A|B)$'` because `-run` and `-list` match unanchored. Every counting arm over a
tagged package is preceded by an arm proving the binary lists at all, because `TestMain` runs before
`m.Run()` handles the flag.

**Two shell hazards, both of which cost time to find.** Some agent harnesses replace `grep` with a
wrapper around `ugrep`, whose `-qv` exit status is inverted relative to GNU grep's — so no
checkpoint below turns on `grep -qv`. And `scripts/checks.sh` is `set -euo pipefail`, so an arm
added there fails the whole run on a `grep` that matches nothing.

**`./scripts/` is red at HEAD on `TestNoI18nPackageCostsMoreThanItsErrorSeam`.** Every checkpoint
that runs `./scripts/` runs the counted `-run` pattern for **this phase's** arms, which is green,
and the whole-package run is reported with the foreign red named. No section reports `make unit`
green.

---

### S1 — the bounded read and the digest  `[x]`   *(no database · moves the manifest)*

**Delivers ES-08 whole and ES-07's kernel half.** It is the only change to the `event` package
itself, it is self-contained, and it is first because `receipt` cannot be written without `Digest`.

**Appendices** ES-08, ES-07 (the digest).

**Files** `event/repo.go`, `event/errors.go`, `event/token.go` (one comment),
`event/eventmemory/doc.go`, `event/eventmemory/transaction.go` (two comments) — modified;
`event/stateat_test.go`, `event/digest_test.go` — new; `event/repo_test.go`,
`event/replay_test.go`, `event/refusalmessages_test.go`, `event/refusal_test.go` — extended;
`docs/api/surface.md` regenerated.

> **Correction, made while implementing S1:** `event/refusal_test.go` was missing from this list
> and from the checkpoint's allowed set. `declaredVocabulary()` lives there and
> `TestTheRefusalVocabularyIsAPartition` pairs it against every `Err*` declared in a non-test file
> and against `vocabulary()`'s own body, so `ErrVersion` cannot be added to `event/errors.go`
> without its row — the section cannot be written at all under the old set. One row added, nothing
> else in the file touched.

**Realises** `Repo.StateAt`, `Repo.Digest`, `ErrVersion`, `replay`'s `upTo` parameter and its place
in `vocabulary()`.

**Covers** UC-224, UC-228…UC-234, UC-236, and UC-216's digest half; INV-116, INV-118, INV-119,
INV-120, INV-124 (this section's refusals).

**Tests** — all untagged, in `event`:

- `TestAPrefixFoldsToTheStateItsVersionHolds` — §UC-228. Twelve events, `StateAt(…, 7)` folds the
  first seven by the folds `Load` uses; version 12 equals `Load`. **Control:** version 6 differs
  from version 7 in exactly the way the seventh fact's fold says, so the bound is inclusive and
  measured rather than assumed.
- `TestABoundedReadStopsAtThePageItNeeds` — §UC-229, §INV-119. A recording store with
  `StreamPage` 4 over eleven events, read at 1, 4, 5, 8, 11; page counts asserted per bound — two
  at version 5 and **not three**, one at version 4. **Control:** the same reads through `Load` cost
  three pages each, so the bound does work.
- `TestAVersionPastTheEndAndAnEmptyStreamAreOneRefusal` — §UC-230, §UC-231. Three arms — past the
  end, a stream never appended to, version zero — each `ErrVersion` with the zero state, and the
  first two asserted to be **the same** refusal. **Control:** `StateAt(…, 5)` on the five-event
  stream succeeds, and the refusal costs no second confirming read.
- `TestAnUnreadableEventInThePrefixReturnsTheZeroState` — §UC-232, §INV-118. Four variants — an
  undeclared type, an undeclared revision, a refusing upcaster, a recorded payload over
  `MaxPayload` — each its own sentinel and the **zero** state, never the accumulator.
  **Control:** the identical stream read through `Load` gives the identical refusal, which is what
  proves the behaviour was inherited rather than re-implemented.
- `TestAPageOutOfOrderIsRefusedBeforeItIsTruncated` — §UC-233. A defect store answering
  `[v3, v1, v2]`; `StateAt(…, 2)` answers `ErrBackend` from `checkPage`. **Control:** the same
  store in order is accepted at the same bound. This is the arm that would pass on a
  truncate-then-check implementation.
- `TestABoundedReadYieldsNothingThatCanAppend` — §UC-234, §INV-119. The signature asserted through
  reflection to return exactly two values, neither an `At[S]`. **Control:** `Load`'s signature is
  asserted unchanged in the same test.
- `TestADigestIsTheBytesThisAppendWouldWrite` — §UC-224, §INV-116, **P-12**. Two pairs: domain
  inputs that differ with byte-identical records digest **once**; identical domain inputs whose
  codec records a timestamp digest **twice** — labelled in the test's own failure message as the
  mechanism being disabled rather than as correct behaviour. **Control:** a recording store asserts
  `Digest` issued **no call of any kind**, and a malformed batch — a change decided for another
  stream, **a decision its declared codec could not encode**, a payload over `MaxPayload`, a forged
  token — is refused with the same sentinel `Append` would give it. The malformed-batch subtest
  opens with the pair that names the consequence: two different decisions of one fact that
  `encoding/json` refuses (`+Inf` and `NaN`), asserted refused **before** the table runs, because a
  change carrying its own refusal carries no payload and every such decision otherwise digests to
  the record's shape alone.
- `TestTheDigestPreimageIsFrozen` — §INV-116, the durable half. Six golden 32-byte vectors in the
  shape of `TestComposeRendersTheFrozenKey`: the empty batch, one record, two records, an empty
  payload at revision 0, a key whose own rendering is escaped again, and a revision over one byte.
  They are the only absolute assertions about the digest — everything else in the file is `A != B`
  within one process — and they exist because S3 makes this value durable under P-11's 24-hour
  retention, so build *N* writing a fingerprint and build *N+1* reading it is a routine deployment.
- `TestTwoAttemptsAtDifferentVersionsDigestEqual` — §UC-215's digest half, §INV-116. The load-bearing
  arm of the whole ES-07 design: two `At[S]` values at different versions over one stream, the same
  records, one fingerprint.
- `TestADigestCollidesOnAByteAStreamAndAnOrder` — §UC-216's table. Three arms that must differ — one
  byte of one payload, a different stream, the same records reordered — **beside the arm that must
  NOT differ**, which is the expected version. That fourth arm is what makes the other three mean
  "the content decided it".
- `TestNoRefusalOfABoundedReadNamesAVersion` — §INV-124. Over every new error path, in
  `refusalmessages_test.go`'s shape: the input never appears in the output, and no refusal names 5,
  9, a key or a stream's key.

**Checkpoint** (no database):

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s1
awk '/^## github.com\/frostgrove\/vv\/event$/,/^## github.com\/frostgrove\/vv\/event\//' \
    docs/api/surface.md > .git/event_surface_before_p5 && test -s .git/event_surface_before_p5
# … write the section …
gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 \
&& go build ./... && go vet ./event/... \
&& test "$(go test -list '^(TestAPrefixFoldsToTheStateItsVersionHolds|TestABoundedReadStopsAtThePageItNeeds|TestAVersionPastTheEndAndAnEmptyStreamAreOneRefusal|TestAnUnreadableEventInThePrefixReturnsTheZeroState|TestAPageOutOfOrderIsRefusedBeforeItIsTruncated|TestABoundedReadYieldsNothingThatCanAppend|TestADigestIsTheBytesThisAppendWouldWrite|TestTwoAttemptsAtDifferentVersionsDigestEqual|TestADigestCollidesOnAByteAStreamAndAnOrder|TestTheDigestPreimageIsFrozen|TestNoRefusalOfABoundedReadNamesAVersion)$' ./event/ | grep -c '^Test')" = 11 \
&& go test -race -count=1 -run '^(TestAPrefixFoldsToTheStateItsVersionHolds|TestABoundedReadStopsAtThePageItNeeds|TestAVersionPastTheEndAndAnEmptyStreamAreOneRefusal|TestAnUnreadableEventInThePrefixReturnsTheZeroState|TestAPageOutOfOrderIsRefusedBeforeItIsTruncated|TestABoundedReadYieldsNothingThatCanAppend|TestADigestIsTheBytesThisAppendWouldWrite|TestTwoAttemptsAtDifferentVersionsDigestEqual|TestADigestCollidesOnAByteAStreamAndAnOrder|TestTheDigestPreimageIsFrozen|TestNoRefusalOfABoundedReadNamesAVersion)$' ./event/ \
&& go test -race -count=1 ./event/... \
&& make api \
&& awk '/^## github.com\/frostgrove\/vv\/event$/,/^## github.com\/frostgrove\/vv\/event\//' \
       docs/api/surface.md > .git/event_surface_after_s1 \
&& test "$(diff .git/event_surface_before_p5 .git/event_surface_after_s1 | grep -c '^> ')" = 3 \
&& test "$(diff .git/event_surface_before_p5 .git/event_surface_after_s1 | grep -c '^< ')" = 0 \
&& ./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s1 \
     '^event/(repo|errors|token|repo_test|replay_test|refusal_test|refusalmessages_test|stateat_test|digest_test)\.go$|^event/eventmemory/(doc|transaction)\.go$' \
     event/repo.go event/errors.go event/stateat_test.go event/digest_test.go
```

The three-added-zero-removed arm is **UC-236 and INV-120 made into a command**: `Digest`, `StateAt`
and `ErrVersion`, and nothing removed or changed. Report the `make api` diff in the section's
transcript — it is a question for a person, as it always is.

#### S1 — executed 2026-09-12, checkpoint green

Run from a clean tree with `.git/event_kernel_before_s1` recorded from the 152-path HEAD manifest
and `.git/event_surface_before_p5` cut from the pre-phase `docs/api/surface.md`. `EXIT=0` for the
whole `&&` chain; `gofmt -l .` printed nothing and the `= 11` counting arm passed before the run.

**Re-run after round 1's two `[high]` findings were closed** — GAP-1's carried-refusal row and
GAP-2's golden vectors. The pattern counts eleven names, not ten: `TestTheDigestPreimageIsFrozen`
is the arm GAP-2 asked for.

```
ok  	github.com/frostgrove/vv/event	1.014s        # the eleven named tests, -race
ok  	github.com/frostgrove/vv/event	6.809s        # ./event/... -race
ok  	github.com/frostgrove/vv/event/eventmemory	1.506s
ok  	github.com/frostgrove/vv/event/eventtest	4.330s
ok  	github.com/frostgrove/vv/event/projection	1.927s
api: docs/api/surface.md regenerated — read the diff
event-kernel-baseline: 154 files recorded in scripts/event_kernel.sha256
check-event-kernel: ok
the files under event/ this section moved:
  event/digest_test.go
  event/errors.go
  event/eventmemory/doc.go
  event/eventmemory/transaction.go
  event/refusal_test.go
  event/refusalmessages_test.go
  event/repo.go
  event/repo_test.go
  event/stateat_test.go
  event/token.go
event-kernel-moved: ok
EXIT=0
```

**The `make api` diff, whole file — three added, none removed, none changed.** It is a question
for a person, and the question is whether `[32]byte` is the right shape for a value `receipt`
will wrap in S3; the plan's answer is yes, because a `Fingerprint` that took a named type from
`event` would put half of `receipt`'s vocabulary in the kernel.

```
@@ -5836,6 +5836,7 @@ var ErrTransactionMismatch error
 var ErrUncertain error
 var ErrUnknownType error
 var ErrUpcast error
+var ErrVersion error
 var ErrWrongStore error
 var ErrWrongStream error
@@ -5903,7 +5904,9 @@ type Repo[S any, ID any] struct {
 func (*Repo[S, ID]) Append(context.Context, At[S], ...Change[S]) (At[S], Commit, error)
 func (*Repo[S, ID]) Authority(context.Context) (Authority, error)
+func (*Repo[S, ID]) Digest(At[S], ...Change[S]) ([32]byte, error)
 func (*Repo[S, ID]) Load(context.Context, ID) (S, At[S], error)
+func (*Repo[S, ID]) StateAt(context.Context, ID, Version) (S, error)
 func (*Repo[S, ID]) Within(context.Context) (context.Context, error)
```

**Repository gates after the section.** `make check` all green — `check-deps`, `check-tiers`,
`check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`,
`check-otel-schema`, `check-otel-module`, `check-workspace`, `check-event-kernel`. `make vet`,
`make tidy` and `gofmt -l .` clean. `go test -count=1 ./scripts/` reports **exactly one**
`--- FAIL:` line, `TestNoI18nPackageCostsMoreThanItsErrorSeam`, which is the foreign red named at
the head of this plan and is not touched; the three `event` arms
(`TestNoEventPackageCostsMoreThanTheSeamItNames`,
`TestMerelyImportingTheEventExtensionStartsNothing`,
`TestNoBaseSubsystemDependsOnTheEventExtension`) are green on their own counted run. `make unit`
is green apart from that one arm. The live `event/eventpg` suite was run although S1 needs no
database — `ok github.com/frostgrove/vv/event/eventpg 111.462s` — because `replay` gained a
parameter and `eventtest`'s conformance runs through it.

**On the round-2 re-run the live suite was red once in three runs, and it is not this section's.**
`TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot` failed with *"the walk beyond the gap
answered []"*; alone it passes in 1.219s and the two full runs after it were green (`ok 118.035s`,
`ok 114.169s`). It is the cluster-wide `pg_snapshot_xmin` floor the backlog already carries: any
other session that has written and not ended holds settlement down, and in a full suite run the
other tests are exactly that. Round 2 changed one comment and one `_test.go` of package `event`,
neither of which is compiled into `eventpg`'s binary. Backlog item **27** under `## P5`.

**Mutation evidence — eleven breaks, each restored.** Every one was applied to `event/repo.go`,
run, and reverted; the test that caught it is named beside it.

| What was broken | What caught it |
|---|---|
| `checkPage` moved *after* the truncation | `TestAPageOutOfOrderIsRefusedBeforeItIsTruncated` — and **only** its second arm, `[v1, v3, v2]` at a bound of 1. See the correction below |
| the bound made exclusive (`page[:upTo-at-1]`) | `TestAPrefixFoldsToTheStateItsVersionHolds`, all four arms |
| the `reached < version` refusal disabled | `TestAVersionPastTheEndAndAnEmptyStreamAreOneRefusal` |
| the `version == 0` refusal removed | `TestAVersionPastTheEndAndAnEmptyStreamAreOneRefusal` |
| the loop's `at >= upTo` early return removed | `TestABoundedReadStopsAtThePageItNeeds` — version 4 cost 2 pages against 1 |
| the fold accumulator returned instead of the zero state (**both** `replay` and `StateAt` changed — changing either alone is invisible, which is INV-118's "the failure path is `replay`'s own" working) | `TestAnUnreadableEventInThePrefixReturnsTheZeroState` — reported `{Balance:6}`, versions 1–3 |
| `StateAt` wrapping the loop's refusal in a sentence of its own | `TestAnUnreadableEventInThePrefixReturnsTheZeroState`'s through-a-`Load` control |
| the length prefix dropped from the digest preimage | `TestADigestCollidesOnAByteAStreamAndAnOrder`'s split arm — and nothing else, which is why that arm calls `digestOf` directly |
| the composed stream dropped from the preimage | `TestADigestCollidesOnAByteAStreamAndAnOrder`'s different-stream row |
| the revision dropped from the preimage | `TestADigestCollidesOnAByteAStreamAndAnOrder`'s revision row |
| the expected version **added** to the preimage | `TestTwoAttemptsAtDifferentVersionsDigestEqual` and the fourth, must-not-differ row |
| `Digest` given one `Store.Backing()` call | `TestADigestIsTheBytesThisAppendWouldWrite`'s zero-call control |
| the short-prefix refusal made to name `reached` and `version` | `TestNoRefusalOfABoundedReadNamesAVersion` **and** the static `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor`, which reported `repo.go:84: a Version is rendered into a refusal` |
| the version-zero refusal made to name the stream key | the same pair |
| the transaction question removed from `StateAt` | `TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames` — `Backing 0 times where the contract names 1` |

**Round 1's two survivors, and the five mutations that now catch them.** Both were re-driven from
the pristine file before anything was written, then re-applied after — the reproduction is in the
round-1 record and the closure is here.

| What was broken | What caught it, after |
|---|---|
| `Digest`'s third step — the `change.err` loop — deleted (GAP-1's survivor: `./event/...` was wholly green under it, and two different unencodable decisions of one fact both answered `12f8779f…` with no error) | `TestADigestIsTheBytesThisAppendWouldWrite`'s malformed-batch subtest, reporting **the collision first**: *"two different decisions of one fact … are one fingerprint 551f9e1a…"*. The table's own `ErrEncode` row is the second half and pins the sentinel, the zero `[32]byte` and the sentence `Append` gives the same batch |
| the preimage's record fields reordered **and** `eightBytes` flipped to little-endian (GAP-2's survivor: injectivity is preserved, so every relative arm passed and `./event/...` was green) | `TestTheDigestPreimageIsFrozen`, on the first vector — `66d33f5b…` against the frozen `d332cf61…` |
| the reorder **alone**, big-endian kept | the same test, on the one-record vector — `1a2dff3d…` against `204bf6a4…`. The empty-batch vector cannot see this one, which is why there are six |
| the endianness **alone**, field order kept | the same test, on the empty-batch vector — the length prefix of the composed stream is enough |
| `Compose(stream.Family, …)` replaced by `stream.Family + "/" + string(stream.Key)` — the third tidy the frozen sentence names | the same test, on the empty-batch vector. The escaped-key vector is what makes this one a layout question rather than a coincidence: a key that already carries a `/` renders `acme%2Fevil%252FA-17` under `Compose` and `acme/evil%2FA-17` under a bare join |

The six vectors were computed twice: once by `digestOf` and once by an independent implementation
of the documented format, and the two agree byte for byte. A golden vector taken only from the
code under test freezes whatever that code does, including a defect.

`digestOf` also gained the sentence `Compose` carries — that the encoding is frozen because a
fingerprint outlives the build that computed it, and that inside a retention window a deployment
is routine. It names the four tidies that look harmless (a reorder, a narrowed length, a flipped
byte order, a separation tag) and says the bytes are held as vectors rather than described. No
file left the section's allowed set: both changes are in `event/repo.go` and
`event/digest_test.go`, and the surface diff is still three added lines and nothing else.

**Correction to this section's UC-233 arm.** The plan called for one out-of-order page,
`[v3, v1, v2]` read at version 2, and said it is "the arm that would pass on a truncate-then-check
implementation". Executed, it is not: truncating `[v3, v1, v2]` to two envelopes leaves `[v3, v1]`,
whose first envelope is already at the wrong version, so `checkPage` refuses it **either way** and
the arm proves nothing about the ordering. The discriminating shape is a page whose disorder falls
entirely in the part the bound discards — `[v1, v3, v2]` read at version 1, which truncate-first
accepts and folds to a state that is wrong at the right version with no refusal anywhere. Both arms
ship: UC-233's own, and the one that carries the claim.

**Two departures from this section's file list, both recorded rather than absorbed.**
`event/refusal_test.go` was added to the allowed set for the reason written above the checkpoint.
`event/replay_test.go` was **not** extended and did not move: `replay`'s new parameter is `0` on
every path a `Load` takes, so the file's existing arms already exercise the unbounded loop
unchanged, and INV-118's "not a second implementation" is falsified by
`TestAnUnreadableEventInThePrefixReturnsTheZeroState`'s through-a-`Load` control rather than by a
new arm there. `event/repo_test.go` was extended, with the two arms the store-call table was the
right home for: a bounded read costs `Backing` 1, `Transaction` 1 and **one** page, and a bounded
read on a context marked for one transaction and now carrying another is `ErrTransactionMismatch`
before any statement.

---

### S2 — the wait  `[x]`   *(no database · moves the manifest)*

**Delivers ES-05's value, its doors and its poll loop**, and the one widened sentence of `Park`'s
contract. Nothing here touches the `event` package: that is in the section's allowed set as an
exclusion.

**Appendices** ES-05.

**Files** `event/projection/mark.go`, `wait.go`, `mark_test.go`, `wait_test.go` — new;
`event/projection/errors.go`, `park.go`, `doc.go`, `harness_test.go` — modified;
`scripts/projection_test.go` — modified (**P-4**, **P-5**); `docs/api/surface.md` regenerated.
**One more, added while implementing:** `docs/ai/flows/Index.md` gains the reverse-index row for
each new file, because `TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex` goes red the
moment they land — correction 4 below.

**Realises** `Mark`, `Mark.At`, `Mark.Zero`, `Mark.String`, `MarkOf`, `WaitSpec`, `WaitOf`,
`WaitSpec.Committed`, `Visibility`, `Wait`, `ErrNotVisible`, `ErrParked`, `ErrUncommitted`,
`ErrGeneration`.

**Covers** UC-205, UC-206 (the pair), UC-208, UC-209/UC-210 (the refusal half), UC-211, UC-212 (the
fake-clock half), UC-213, UC-242, UC-243, UC-244 (the harness half), UC-245 (the harness half);
INV-108, INV-110, INV-111, INV-112, INV-113 (the value half), INV-120, INV-123, INV-124.

**Tests** — all untagged, in `event/projection`:

- `TestAMarkIsMintedOnlyFromANumberAStoreProduced` — §INV-108, **P-18**. A compile-level check that
  `Mark` has no exported field and no third minting door; `%v` of one renders `[mark]` and no
  position; the zero `Mark` is refused by `Wait` with `ErrSpec`. **And the cross-spec arm:** two
  `WaitSpec`s over one log with different `Of`s, a mark minted from the first and handed to the
  second, refused with `ErrSpec` **before any store call** — asserted by a recording `Checkpoints`
  that sees none — and the same arm for a `MarkOf` mark, so both doors are shown to carry one rule.
  **Controls:** a `MarkOf` of a non-zero barrier is admitted at every door the zero one is refused
  at; and **the same mark on its own spec reaches**, which is what stops the cross-spec arm passing
  because everything is refused.
- `TestCommittedReadsTheCommitsOwnRangeAndNothingElse` — §UC-243, §INV-109. A sequencer keyed on a
  payload field; a commit of three facts keyed `A`, `B`, `A`; the mark carries `A` and `B` in
  first-appearance order and **not** the envelopes. A recording store asserts the read count against
  the commit's size. **Control:** a recording `Park` asserts `Holds` is called twice per poll — once
  per distinct key and not once per envelope.
- `TestCommittedRefusesTheSixItCannotMint` — §UC-209, §UC-210, §INV-109, **P-19**. Empty commit →
  `ErrSpec`; a bound transaction → `ErrSpec` before any read; a short page → `ErrUncommitted` naming
  the two indistinguishable causes in one sentence; a zero position → `ErrUncommitted`; nil
  `Sequence` beside non-nil `Park` → `ErrSpec`; and **all three of `Repo.checkPage`'s arms** →
  `event.ErrBackend`: a page whose **first** envelope is another stream's, one whose **second** is,
  one whose second repeats the version of the first, and one longer than the `StreamPage` the store
  publishes while being otherwise dense and this stream's. The second-envelope arm carries the
  consequence driven rather than assumed: the mark this commit does mint carries the key that page
  would have dropped, and a queue holding it answers `ErrParked` where a mark minted off the foreign
  page answers `Reached: true` on a nil error. **Control:** the same call on a fresh context after
  the commit mints the mark.
- `TestTheParkIsAskedBeforeTheCensusOnEveryPoll` — §UC-206, §UC-207, §INV-110. A parked sequence
  with `Highest` already past the mark answers `ErrParked` on poll 1 with `Visibility{Parked:true,
  Polls:1}` and reads no census; a four-member cover where the caller's partition parked and another
  lags answers `ErrParked` on poll 1 and not at the deadline. **Control:** the identical wiring with
  `Park` **nil** answers `Reached: true` for the same parked event — the failure this case exists
  to pin, and the whole of *«scan checkpoint после parking не доказывает применение события»*.
- `TestAHealthyWaitAsksSequencesAndNeverHoldsOrHoles` — §UC-208, §INV-110. A recording `Park`
  counting four methods **and recording whether a transaction of the destination's was bound**:
  three polls → `Sequences` 3, `Holds` 0, `Holes` **0**, `Park` 0, and no bound transaction on any
  call. **Control:** the same wait against a generation with `Quarantined > 0` makes the **same**
  three calls, so the absence of `Holes` is unconditional.
- `TestAWaitReachesOnItsFirstPollAndNeverSleeps` — §UC-205. `Every: time.Hour` with a recording
  `Ticks`; `Polls == 1`, `Reached` true, and the ticker either never asked for or never read from.
  **Control:** one event short takes at least two polls.
- `TestABarrierMintedMarkIsRefusedBesideAPark` — §UC-211. Four arms: an empty commit, the zero
  `Mark`, a barrier of the generation being waited on **admitted** with `Park` nil, and the same
  barrier mark beside a non-nil `Park` **refused**. **Control:** the fourth arm with `Park` nil
  reaches, so the refusal is about the missing sequence key and not about the barrier.
- `TestADeadlineSaysWhichKindOfNotYetItWas` — §UC-212, **P-9**, **P-20**. Two waits on a fake clock:
  advancing but slow → `Moved: true`, a shrinking `Behind`, `Polls > 1`; stopped → `Moved: false`, a
  constant `Behind`, `Polls > 1`. Both errors satisfy `ErrNotVisible` **and**
  `context.DeadlineExceeded`. A third wait whose budget runs out **inside its first poll** — a
  checkpoint store whose round trip outlasts it — satisfies the same two with
  `Visibility{Polls: 1}` and carries **neither `ErrTopology` nor `event.ErrBackend`**: the refusal
  it collected is that budget coming back through the store, not a poll that could not be made
  (remediation, [S4 GAPS] GAP-2). **Controls:** a **cancelled** context returns `ctx.Err()` bare
  with the zero `Visibility`, and so does one cancelled **inside** a poll; the same first poll
  failing while the budget is live still answers `ErrTopology` and not `ErrNotVisible`; a deadline
  reached over a poll that **really** failed does carry `ErrTopology` and `event.ErrBackend`, which
  is what the third wait is told apart from; and a **parked** answer arriving after the budget
  elapsed is still `ErrParked` and is not rendered as a deadline.
- `TestAPollThatCannotBeMadeIsTerminalFirstAndPolledThroughAfter` — §UC-244, §INV-113. A
  `Checkpoints` failable on demand: a first-poll failure returns at once with `Visibility{Polls:1}`
  and that store's refusal unwrapped; a fourth-poll failure is polled through, and the deadline's
  error satisfies `ErrNotVisible`, `context.DeadlineExceeded` **and** the last poll's own refusal.
  **Control:** the healthy wiring reaches, and the second arm's `Polls` is asserted greater than the
  number of failures.
- `TestACutoverUnderAWaitIsRefusedRatherThanAnswered` — §UC-245, **P-15**. Four arms over a
  movable `Generations`: the retiring generation refused on the poll that would have reached; the
  same generation refused on the poll that **reaches at once**, with the cutover landing between
  that poll's ownership read and its census read (remediation, [S4 GAPS] GAP-1); the arriving one
  refused on poll **1**; the active one reaches. **Control:** the same arms with `Generations` nil
  answer `Reached: true`, `ErrNotVisible`, `Reached: true`; and a recording `Generations` asserts
  `Active` was read **exactly twice** over the whole wait — over two polls in the fourth arm and
  over the one poll of a wait that reaches at once.
- `TestTheDerivedSpecAnswersWhatThreeHandWrittenOnesGetWrong` — §UC-242, §INV-111. `WaitOf` against
  three hand-written specs differing in one field each — a foreign `Sequence`, a nil `Park`, a
  two-member `Over` of a four-member cover — each asserted to answer the **wrong** answer, so the
  day one becomes checkable the arm fails and says so. **Control:** the derived spec answers
  `ErrParked`, and a fourth arm asserts `WaitOf` refuses the zero `Cover` and a `Spec` naming no
  identity.
- `TestWaitOfDerivesTheSameDefaultsNewApplies` — **P-1**, §INV-111. A `Spec` with `Park` set beside
  `OnPermanentFailure != ParkSequence` yields a `WaitSpec` whose `Park` is **nil**; a `Spec` with a
  nil `Sequence` yields `ByStream()`. **Control:** the same `Spec` with `ParkSequence` keeps its
  queue, and a `Park` that refuses outside a unit is shown to fail the first poll under the naive
  derivation and not under this one.
- `TestAWaitStartsNothingAndSavesNothing` — §INV-112. A recording `Checkpoints`: no `Save`, no
  `Forget`, and **one `Load` per cover member per poll** for a recorded generation — with **P-7**'s
  second arm asserting **two** per fresh member, which is what the module page states.
- `TestThereIsNoFieldThatTurnsARefusalIntoASuccess` — §UC-213. A reflective walk over `WaitSpec`
  and `Visibility` finding no `AllowStale`, `Stale`, `OnTimeout` or equivalent. **Control:** a
  fixture struct carrying one is reported.
- `TestNoRefusalOfAWaitNamesAPositionOrAKey` — §INV-124. Every new error path, asserting no
  position, key, cursor or identity value reaches a message.

**In `scripts/`** — extended here, in the same change as the files:

- `TestNoCommentInTheProjectionPackagePromisesExactlyOnce` and
  `TestNothingInTheProjectionPackageOpensATransaction`: both `< 18` guards → **`< 20`** (**P-4**).
- `TestEveryFieldOfAPublishedSpecIsRead`: the walked list gains `"WaitSpec"` (**P-5**).

**Checkpoint** (no database):

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s2
# … write the section …
gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 \
&& go build ./... && go vet ./event/... \
&& test "$(go test -list '^(TestAMarkIsMintedOnlyFromANumberAStoreProduced|TestCommittedReadsTheCommitsOwnRangeAndNothingElse|TestCommittedRefusesTheSixItCannotMint|TestTheParkIsAskedBeforeTheCensusOnEveryPoll|TestAHealthyWaitAsksSequencesAndNeverHoldsOrHoles|TestAWaitReachesOnItsFirstPollAndNeverSleeps|TestABarrierMintedMarkIsRefusedBesideAPark|TestADeadlineSaysWhichKindOfNotYetItWas|TestAPollThatCannotBeMadeIsTerminalFirstAndPolledThroughAfter|TestACutoverUnderAWaitIsRefusedRatherThanAnswered|TestTheDerivedSpecAnswersWhatThreeHandWrittenOnesGetWrong|TestWaitOfDerivesTheSameDefaultsNewApplies|TestAWaitStartsNothingAndSavesNothing|TestThereIsNoFieldThatTurnsARefusalIntoASuccess|TestNoRefusalOfAWaitNamesAPositionOrAKey)$' ./event/projection/ | grep -c '^Test')" = 15 \
&& go test -race -count=1 -run '^(TestAMarkIsMintedOnlyFromANumberAStoreProduced|TestCommittedReadsTheCommitsOwnRangeAndNothingElse|TestCommittedRefusesTheSixItCannotMint|TestTheParkIsAskedBeforeTheCensusOnEveryPoll|TestAHealthyWaitAsksSequencesAndNeverHoldsOrHoles|TestAWaitReachesOnItsFirstPollAndNeverSleeps|TestABarrierMintedMarkIsRefusedBesideAPark|TestADeadlineSaysWhichKindOfNotYetItWas|TestAPollThatCannotBeMadeIsTerminalFirstAndPolledThroughAfter|TestACutoverUnderAWaitIsRefusedRatherThanAnswered|TestTheDerivedSpecAnswersWhatThreeHandWrittenOnesGetWrong|TestWaitOfDerivesTheSameDefaultsNewApplies|TestAWaitStartsNothingAndSavesNothing|TestThereIsNoFieldThatTurnsARefusalIntoASuccess|TestNoRefusalOfAWaitNamesAPositionOrAKey)$' ./event/projection/ \
&& go test -race -count=1 ./event/... \
&& make api \
&& test "$(go test -list '^(TestNoCommentInTheProjectionPackagePromisesExactlyOnce|TestNothingInTheProjectionPackageOpensATransaction|TestEveryFieldOfAPublishedSpecIsRead|TestMerelyImportingTheEventExtensionStartsNothing|TestCursorIsNeverCompared|TestNoExportedFunctionOrdersOrTakesTwoCursors|TestNoSnapshotAuthorityIsDeclaredOrPromised)$' ./scripts/ | grep -c '^Test')" = 7 \
&& go test -race -count=1 -run '^(TestNoCommentInTheProjectionPackagePromisesExactlyOnce|TestNothingInTheProjectionPackageOpensATransaction|TestEveryFieldOfAPublishedSpecIsRead|TestMerelyImportingTheEventExtensionStartsNothing|TestCursorIsNeverCompared|TestNoExportedFunctionOrdersOrTakesTwoCursors|TestNoSnapshotAuthorityIsDeclaredOrPromised)$' ./scripts/ \
&& ./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s2 \
     '^event/projection/(mark|wait|errors|park|doc|mark_test|wait_test|harness_test)\.go$' \
     event/projection/mark.go event/projection/wait.go event/projection/errors.go event/projection/park.go
```

The allowed set names no `^event/[a-z_]*\.go$` and no `^event/eventtest/`, so the `event` package
and the conformance suite are held still by the fence rather than by intention. Report the
`docs/api/surface.md` diff for the `event/projection` section — it should hold the §5.2 block and
nothing else.

#### S2 — executed 2026-09-12, checkpoint green

Run from the S1 tree with `.git/event_kernel_before_s2` recorded from the 154-path manifest S1
left. `EXIT=0` for the whole `&&` chain; `gofmt -l .` printed nothing and both counting arms passed
before their runs. **The output below is the re-run after the two blocking review findings were
closed** (**P-19**, **P-20**, recorded below); the chain, the fifteen names, the seven names and the
158-path manifest are the section's own and did not move.

```
LISTED=15
ok  	github.com/frostgrove/vv/event/projection	4.686s   # the fifteen named tests, -race
ok  	github.com/frostgrove/vv/event	7.742s
ok  	github.com/frostgrove/vv/event/eventmemory	1.511s
ok  	github.com/frostgrove/vv/event/eventtest	4.466s
ok  	github.com/frostgrove/vv/event/projection	5.605s
api: docs/api/surface.md regenerated — read the diff
SCRIPTS-LISTED=7
ok  	github.com/frostgrove/vv/scripts	7.233s
event-kernel-baseline: 158 files recorded in scripts/event_kernel.sha256
check-event-kernel: ok
the files under event/ this section moved:
  event/projection/doc.go
  event/projection/errors.go
  event/projection/harness_test.go
  event/projection/mark.go
  event/projection/mark_test.go
  event/projection/park.go
  event/projection/wait.go
  event/projection/wait_test.go
event-kernel-moved: ok
EXIT=0
```

The diff over `docs/api/surface.md` is **37 added lines, 0 removed, 0 changed** on the re-run as on
the first run: **P-19** and **P-20** added no exported surface — `honest` is unexported and the
context tie-break is a branch in `waiting`.

**The `make api` diff over the `event/projection` section — the §5.2 block and nothing else.** It is
a question for a person, and the question is whether `Visibility` should carry the queue's current
depth beside `Quarantined`; the plan's answer is no, because that number is a `Park.Holes` call from
a caller's goroutine with no unit and would move a second sentence of a contract third parties
implement on a path neither `make api` nor `check-event-kernel` can see.

```
 var ErrClaimLost error
+var ErrGeneration error
 var ErrHalted error
+var ErrNotVisible error
 var ErrOvertaken error
 var ErrParkFull error
+var ErrParked error
 var ErrRetired error
 var ErrSpec error
 var ErrTopology error
+var ErrUncommitted error
 var ErrUnrouted error
@@
+type Mark struct {
+	<unexported fields>
+}
+func (Mark) At() github.com/frostgrove/vv/event.Position
+func (Mark) String() string
+func (Mark) Zero() bool
+func MarkOf(Barrier) Mark
@@
+type Visibility struct {
+	Reached bool
+	At github.com/frostgrove/vv/event.Position
+	Behind github.com/frostgrove/vv/event.Position
+	Moved bool
+	Quarantined uint64
+	Parked bool
+	Polls int
+}
+func Wait(context.Context, WaitSpec) (Visibility, error)
+func WaitOf(Spec, Cover) (WaitSpec, error)
+type WaitSpec struct {
+	Checkpoints github.com/frostgrove/vv/event.Checkpoints
+	Park Park
+	Sequence Sequencer
+	Generations Generations
+	Of Identity
+	Over Cover
+	Until Mark
+	Every time.Duration
+	Ticks github.com/frostgrove/vv/runtime.Ticks
+}
+func (WaitSpec) Committed(context.Context, github.com/frostgrove/vv/event.Store, github.com/frostgrove/vv/event.Commit) (Mark, error)
```

Nothing under `event/` outside `event/projection` moved, and no identifier matching `(?i)snapshot`
reached either the package or the baseline — `TestNoSnapshotAuthorityIsDeclaredOrPromised` is in the
counted seven and is green.

**Repository gates after the section.** `make check` all green — `check-deps`, `check-tiers`,
`check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`,
`check-otel-module`, `check-workspace`, `check-event-kernel`. `make vet`, `make tidy` and
`gofmt -l .` clean. `go test -count=1 ./scripts/` reports **exactly one** `--- FAIL:` line,
`TestNoI18nPackageCostsMoreThanItsErrorSeam`, which is the foreign red named at the head of this
plan and is not touched; `make unit` is green apart from that one arm. The live `event/eventpg`
suite was run although S2 needs no database — `ok github.com/frostgrove/vv/event/eventpg 126.563s`
— because six of its tagged files import `event/projection`, so the new package code is compiled
into that binary.

#### Five corrections made while implementing S2

1. **`Committed` carries a seventh refusal: a store this call names none of.** The six §5.2
   publishes are all reachable, and none of them covers `store == nil`, which would have
   dereferenced a nil interface inside a library. `cutting` refuses every absent interface field of
   a `CutoverSpec` through `absent()` and this door does the same, with `ErrSpec`. The test's name
   is unchanged — it is about the six the mint cannot make — and the nil store is its seventh
   subtest, named as this correction's.

2. **`Wait`'s door restates `Observe`'s three as well as the plan's three.** [SPEC] §5.2 names the
   zero `Mark`, the cross-projection mark and the barrier-minted mark beside a `Park`. It does not
   name the zero `Identity`, an `Identity` carrying a partition, or the zero `Cover` — and `Wait`
   polls `surveyed` directly rather than `Observe`, which is where those three live
   (`generation.go:115-124`). Without them a zero `Cover` folds a census over no members, answers
   the origin and burns the caller's whole deadline in silence. Both `cover.go:22-24` and
   `identity.go:46-47` already say in the shipped tree that **every door in this phase that takes
   one refuses its zero value**, so this is the existing contract applied rather than a refusal
   invented. `generation.go` is outside this section's allowed set, so the three are restated in
   `wait.go` with a comment saying whose they are rather than factored into a shared helper.

3. **`TestTheDerivedSpecAnswersWhatThreeHandWrittenOnesGetWrong` runs over two setups, not
   §UC-242's one, because the single setup is not constructible.** Two reasons, both executed.
   *First*, §UC-242's wrong `Over` is *"an `Over` that names two of the four members as a two-member
   cover"* — and `NewCover` refuses exactly that: two members at mask 3 sum to half the key space
   and its second refusal is a set that does not cover it. *Second*, in a setup that has a parked
   sequence the park is asked **before** the census on every poll, so a spec that differs from the
   derived one only in `Over` answers `ErrParked` and never reaches the census at all. So the
   sequence and park arms run over a parked setup, and the cover arm runs over one with nothing
   parked and one member of four behind the mark — where the derived spec answers `ErrNotVisible`
   and the hand-written coarse cover answers `Reached: true` off the row the generation recorded
   before it was partitioned. The wrong answers are still asserted, which is the point of the case.

4. **`docs/ai/flows/Index.md` gained two rows, and it is not in this section's file list.**
   `TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex` (`scripts/docs_test.go:1389`) goes red
   the moment a non-test file lands under `event/projection`, so `mark.go` and `wait.go` had to be
   named or the section would have closed on a red gate. Both point at **FL-038**, which is the flow
   the plan's own S6 table already assigns the wait to. **S6 still owes FL-038's body and its file
   table** — until then the two rows point at a flow whose body does not mention them, which is
   exactly backlog `## P4` item 63's shape and is inside S6's obligation rather than a new one.

5. **"unwrapped and unreclassified" is asserted as a string equality, not as `errors.Is` against a
   planted sentinel.** A `Checkpoints` made to fail on demand hands its error to `Tracker.Load`,
   which runs it through `refuseRead` and answers the kernel's own `event.ErrBackend` — the planted
   sentinel is gone before `surveyed` ever sees it, which is the kernel's behaviour and not this
   section's. So `TestAPollThatCannotBeMadeIsTerminalFirstAndPolledThroughAfter` asserts
   `ErrTopology` and `event.ErrBackend`, and then asserts that the wait's refusal is **character for
   character** the one `Observe` answers over the same wiring. That is a stronger statement than a
   sentinel match: it says the wait added no sentence of its own.

#### Mutation evidence — twenty breaks, each restored

Every one was applied to `event/projection/wait.go` or `mark.go`, run against the fifteen named
tests, and reverted. The file was compared with its saved original after the campaign and is
byte-identical.

| What was broken | What caught it |
|---|---|
| the park asked only on the poll that would reach | `TestTheParkIsAskedBeforeTheCensusOnEveryPoll`, and five more — the appendix's own sentence has five independent witnesses |
| the sequence keys not de-duplicated at the mint | `TestCommittedReadsTheCommitsOwnRangeAndNothingElse` — three `Holds` calls over a two-key commit |
| the mint reading only the commit's last event (`at := commit.Last() - 1`) | `TestCommittedReadsTheCommitsOwnRangeAndNothingElse` |
| the cross-projection refusal removed (**P-18**) | `TestAMarkIsMintedOnlyFromANumberAStoreProduced` |
| `Mark.Zero` always false | `TestAMarkIsMintedOnlyFromANumberAStoreProduced` |
| `WaitOf` copying `spec.Park` verbatim beside a `ByStream()` default of its own (**P-1**, the naive derivation) | `TestWaitOfDerivesTheSameDefaultsNewApplies` |
| the second `Generations.Active` read dropped (**P-15**) | `TestACutoverUnderAWaitIsRefusedRatherThanAnswered` |
| a poll that cannot be made never terminal | `TestAPollThatCannotBeMadeIsTerminalFirstAndPolledThroughAfter` — **and it was a survivor on the first pass**, see below |
| a poll that cannot be made always terminal | `TestAPollThatCannotBeMadeIsTerminalFirstAndPolledThroughAfter` |
| `Park.Holes` asked on every poll | `TestAHealthyWaitAsksSequencesAndNeverHoldsOrHoles` |
| the ticker created before the first poll | `TestAWaitReachesOnItsFirstPollAndNeverSleeps` |
| the barrier-mark-beside-a-`Park` refusal removed | `TestABarrierMintedMarkIsRefusedBesideAPark` and `TestNoRefusalOfAWaitNamesAPositionOrAKey` |
| `Moved` set without the one-observation guard (**P-9**) | `TestADeadlineSaysWhichKindOfNotYetItWas` and `TestAWaitReachesOnItsFirstPollAndNeverSleeps` |
| the zero-position refusal at the mint removed | `TestCommittedRefusesTheSixItCannotMint` |
| `Reached: true` reported beside the generation refusal | `TestACutoverUnderAWaitIsRefusedRatherThanAnswered` |
| the page's first-envelope stream-and-version check removed | `TestCommittedRefusesTheSixItCannotMint` and `TestNoRefusalOfAWaitNamesAPositionOrAKey` |
| the zero-`Cover` refusal at the door removed (correction 2) | `TestNoRefusalOfAWaitNamesAPositionOrAKey` — **a survivor on the first pass**, see below |
| the `ErrUncommitted` short-stream refusal removed | `TestCommittedRefusesTheSixItCannotMint` |
| a third exported door answering a `Mark` added | `TestAMarkIsMintedOnlyFromANumberAStoreProduced` — the source walk, which is the only thing that can see a door reflection cannot enumerate |
| `Mark.String` rendering the position it holds | `TestAMarkIsMintedOnlyFromANumberAStoreProduced` |

**Two survived the first pass, and both survived for one reason: the tests hung instead of
failing.** A wait whose refusal stopped being terminal, and a wait whose zero-`Cover` door stopped
refusing, both fall through to `select { ctx.Done(); ticker }` — and both arms were written with
`context.Background()` and a ticker nothing fires, so the process blocked until `go test`'s own
ten-minute timeout instead of reporting anything. A test that can only fail by hanging is a test
whose failure a suite reports as an infrastructure problem. Both arms now run under a bounded
context they must never reach, with the reason on the line, and both mutations are caught as
`--- FAIL:` lines naming the test. That is the same class of finding as a vacuous pass and is
recorded here rather than quietly fixed.

#### The two blocking findings the review raised, and how each was closed

`.agents/artifacts/gaps/EVENTSOURCE_P5_S2_GAPS.md` round 1 returned two `[high][immediate]`
findings, both driven rather than read. Both are now closed; **P-19** and **P-20** carry the
argument and the contract move, and this is the evidence.

**GAP-2 → P-19, the mint's page check.** Reproduced first, against the shipped code, with a store
whose page's second envelope was another stream's at the version asked for:

```
commit a-17 v1..v2, tags A then B; the second envelope replaced by b-42's, tagged ZZZ

before   spec.Committed              -> mark.At()=3   err=<nil>     (the commit's own last is 2)
         Repo.Load over the same page -> event: the store failed: [stream waits.order] was read
                                         and [stream waits.order] answered
         with "B" parked, Wait        -> {Reached:true At:13 …} err=<nil>  ErrParked=false

after    spec.Committed              -> event: the store failed: this commit's own range was
                                         asked for and an envelope of the page this store
                                         answered is another stream's or another version's …
         with "B" parked, the mark this commit does mint -> ErrParked
```

**GAP-1 → P-20, the context inside a poll.** Reproduced with a `Checkpoints` whose `Load` blocks
until the caller's own context is done and then answers `ctx.Err()`:

```
before   deadline (30ms) in poll 1 -> ErrTopology, ErrNotVisible=false, Visibility{Polls:1}
         cancel        in poll 1   -> ErrTopology wrap, Visibility{Polls:1}, not ctx.Err() bare
         deadline      in poll 4   -> ErrNotVisible=true          (the same event, another class)

after    deadline      in poll 1   -> ErrNotVisible=true, DeadlineExceeded=true, ErrTopology=true,
                                       Visibility{Polls:1}
         cancel        in poll 1   -> "context canceled", bare, Visibility{}
         deadline      in poll 4   -> unchanged
```

Six more mutations were applied over the two fixes, each run against the fifteen and reverted, and
the file `diff`ed byte-for-byte against its saved original afterwards. **None survived, and none
failed by hanging** — every one reported a `--- FAIL:` line naming its test in under two seconds.

| What was broken | What caught it |
|---|---|
| the `ctx.Err()` branch removed from `waiting` (the finding, re-applied) | `TestADeadlineSaysWhichKindOfNotYetItWas` — three arms: the first-poll deadline, the cancellation control, and its in-poll arm |
| the `ctx.Err()` branch moved **before** the terminal exit | `TestADeadlineSaysWhichKindOfNotYetItWas` — the parked-answer control, which is the only thing that says a conclusion outranks the budget it was drawn under |
| `honest` reduced to `read[:1]` and its page cap dropped (the finding, re-applied) | `TestCommittedRefusesTheSixItCannotMint` — all three new arms |
| the page cap dropped alone | `TestCommittedRefusesTheSixItCannotMint` — the over-long-page arm only, so the arm is sensitive to the cap and not to the walk |
| the per-envelope walk reduced to `read[:1]`, cap kept | `TestCommittedRefusesTheSixItCannotMint` — the second-envelope and repeated-version arms only |

The first assertion written for the parked-answer control was **too weak and was caught by its own
mutation**: `ErrParked` travels `%w`-wrapped **into** the deadline's error, so `errors.Is(err,
ErrParked)` passes on both sides of the ordering. The discriminating half — `errors.Is(err,
ErrNotVisible)` must be **false** — is on the line with the reason beside it.

**Two `[medium]` items from that round were fixed in passing and are named rather than absorbed.**
Neither was fixed because it was quick: `EVENTSOURCE_BACKLOG.md` `## P5` items 29–32 stand as
written, and GAP-3, GAP-4 and GAP-6 are untouched. What moved is that **P-19 added a nineteenth
refusal path**, and `TestNoRefusalOfAWaitNamesAPositionOrAKey` gained its row in the same change, so
the fix did not widen GAP-5's hole; and `TestADeadlineSaysWhichKindOfNotYetItWas` now runs two
controls that did not exist, because the new branch has to be shown to discriminate.

---

### S3 — the operation receipt  `[x]`   *(no database · moves the manifest)*

**Delivers ES-07's package**, its doors, its two obligations and its refusals. It depends on S1's
`Repo.Digest` and on nothing in S2.

**Appendices** ES-07.

**Files** `event/receipt/{doc,key,fingerprint,receipt,claim,resolve,errors}.go` and their
`_test.go` peers — new; `scripts/event_test.go` (**P-2**), `scripts/projection_test.go` (**P-3**,
**P-5**) — modified; `docs/api/surface.md` regenerated **before** `./scripts/` runs.

**Realises** the whole of [SPEC] §5.3.

**Covers** UC-214 (the unit half), UC-215, UC-216 (the verdict half), UC-218, UC-219, UC-221,
UC-222, UC-223 (the four standings), UC-224, UC-225, UC-226, UC-246, UC-248, UC-249, UC-251 (the
first arm); INV-114 (the door half), INV-117, INV-123, INV-124, INV-126.

**Tests** — all untagged, in `event/receipt`, over `eventmemory` and a fake `Ledger` that shares its
authority:

- `TestAClaimAnAppendAndACompletionAreOneTransaction` — §UC-214. `Recorded`, the append admitted,
  `Complete` writing the range onto the claimed row, one row after the commit. **Control:** rolling
  the unit back leaves **no** row at all, so the row's existence is evidence about the transaction
  and not about the claim.
- `TestAClaimIsRefusedUnlessTheLedgerAndTheStoreAreOneTransaction` — §UC-218, §UC-219, §INV-114.
  Four arms: two handles for one database; no transaction of the store's bound; a store whose
  `Capabilities().Transactions != Supported`; and a ledger whose `Transaction` answers invalid.
  **Control:** one handle for both is accepted.
- `TestACompletionThatIsNotTheClaimsIsRefusedFiveWays` — §UC-248, §INV-114. No transaction; a second
  different transaction of the same store; a commit of another stream; a second `Complete`; a
  verdict that is not `Recorded`. All `ErrSpec`, **before the ledger is written**. **Control:** the
  claim's own transaction, its own commit, once, on `Recorded`, is admitted.
- `TestAResolveInsideTheWritingTransactionIsRefused` — §UC-249, §INV-114. Three arms: the claiming
  transaction's context; a context carrying the ledger's transaction but not the store's; a fresh
  context. `ErrSpec`, `ErrSpec`, `Unresolved`. **Control:** the same three after the commit are
  `ErrSpec`, `ErrSpec`, `Found` — the refusal is about the placement and not about the row.
- `TestARepeatIsAnsweredOnlyFromACompleteRow` — §UC-215, §UC-222, §INV-117. A complete row with an
  equal fingerprint → `Repeated` and the first attempt's range; a row that exists and is **not**
  complete → **`ErrIncomplete` and no verdict at all**. **Control:** the same replay with the row
  deleted answers `Recorded` and appends, so `Repeated` is the row's answer and not a guess.
- `TestACollisionTravelsAsAnErrorAndNotOnlyAsAVerdict` — §UC-216, **[GAPS] GAP-3**. `Collided`
  arrives as `ErrCollision` from `Claim` itself; the worst code that compiles —
  `held, err := Claim(...)`, `if err != nil`, `Append` — is written out in the test and asserted to
  refuse. **Control:** the same shape on `Recorded` appends.
- `TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt` — §UC-246, §UC-251's control. `Once` runs the
  work once on `Recorded`, never on `Repeated`, and completes an **empty** `Commit` with a zero
  range so a no-op decision is a finished operation. **Control:** the same caller without the
  `Complete` leaves an incomplete row whose retry answers `ErrIncomplete`.
- `TestEveryRefusalClaimHasIsReachableThroughOnce` — **P-16**. Each of `Claim`'s refusals driven
  through both doors, same sentinel. **Control:** a refusal reachable through only one door fails
  the table.
- `TestAKeyRendersNothingOfItsValue` — §UC-225. `String()` and `%v` answer `[operation key]`;
  `NewKey` refuses empty, over-bound, invalid UTF-8, a control character and a bracket.
  **Control:** the refusal messages are asserted to contain none of the rejected inputs.
- `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` — §UC-226. `won == true` beside another
  key's receipt; `won == false` beside no receipt; a `Horizon` in the future. Each `ErrLedger` at
  the door. **Control:** a conformant ledger passes every check.
- `TestAnAbsentRowIsUnresolvedAndAHorizonDecidesExpired` — §UC-223, §INV-115. Four standings over a
  fake ledger: `Found`, `Incomplete`, `Unresolved`, `Expired`; a **zero `Issued` answers
  `Unresolved`** and carries the horizon, and is never `ErrSpec`. **Control:** an `Issued` before
  the horizon answers `Expired` and one after it answers `Unresolved`.
- `TestAResolveReadsNoEventAndOffersNothingToAppendWith` — §UC-221. `Found` with the key, the
  fingerprint, the stream and `First`/`Last`, which is enough to answer the original request
  without re-reading the aggregate. **Control:** a recording store asserts `Resolve` issued
  **exactly one** call — the transaction question — and no other.
- `TestNoReceiptTypeExposesAnAppendToken` — §INV-117. A reflective walk over every exported type of
  the package finding no `event.At[…]` in any field or return.
- `TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage` — §INV-124.

**In `scripts/`** — extended here, in the same change:

- `TestNoEventPackageCostsMoreThanTheSeamItNames`: `charged` gains
  `eventExtension + "/receipt": ""`, and the doc paragraph gains the sentence (**P-2**).
- `checkedEventPackages`: the floor `listed < 5` → `< 6`, and the `slices.Contains` assertion gains
  `eventExtension+"/receipt"` (**P-3**).
- `TestNothingInTheProjectionPackageOpensATransaction`: widened to walk `../event/receipt` too,
  keeping its fixture control (§INV-123).
- `TestEveryFieldOfAPublishedSpecIsRead`: a second arm over `event/receipt` for `ClaimSpec` and
  `ResolveSpec`, with the comment saying why `Receipt` is not asked (**P-5**).

**Checkpoint** (no database):

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s3
# … write the section …
gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 \
&& go build ./... && go vet ./event/... \
&& test "$(go test -list '^(TestAClaimAnAppendAndACompletionAreOneTransaction|TestAClaimIsRefusedUnlessTheLedgerAndTheStoreAreOneTransaction|TestACompletionThatIsNotTheClaimsIsRefusedFiveWays|TestAResolveInsideTheWritingTransactionIsRefused|TestARepeatIsAnsweredOnlyFromACompleteRow|TestACollisionTravelsAsAnErrorAndNotOnlyAsAVerdict|TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt|TestEveryRefusalClaimHasIsReachableThroughOnce|TestAKeyRendersNothingOfItsValue|TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused|TestAnAbsentRowIsUnresolvedAndAHorizonDecidesExpired|TestAResolveReadsNoEventAndOffersNothingToAppendWith|TestNoReceiptTypeExposesAnAppendToken|TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage)$' ./event/receipt/ | grep -c '^Test')" = 14 \
&& go test -race -count=1 -run '^(TestAClaimAnAppendAndACompletionAreOneTransaction|TestAClaimIsRefusedUnlessTheLedgerAndTheStoreAreOneTransaction|TestACompletionThatIsNotTheClaimsIsRefusedFiveWays|TestAResolveInsideTheWritingTransactionIsRefused|TestARepeatIsAnsweredOnlyFromACompleteRow|TestACollisionTravelsAsAnErrorAndNotOnlyAsAVerdict|TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt|TestEveryRefusalClaimHasIsReachableThroughOnce|TestAKeyRendersNothingOfItsValue|TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused|TestAnAbsentRowIsUnresolvedAndAHorizonDecidesExpired|TestAResolveReadsNoEventAndOffersNothingToAppendWith|TestNoReceiptTypeExposesAnAppendToken|TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage)$' ./event/receipt/ \
&& go test -race -count=1 ./event/... \
&& make api \
&& grep -q '^## github.com/frostgrove/vv/event/receipt$' docs/api/surface.md \
&& test "$(go test -list '^(TestNoEventPackageCostsMoreThanTheSeamItNames|TestNoBaseSubsystemDependsOnTheEventExtension|TestMerelyImportingTheEventExtensionStartsNothing|TestNothingInTheProjectionPackageOpensATransaction|TestEveryFieldOfAPublishedSpecIsRead|TestNoSnapshotAuthorityIsDeclaredOrPromised|TestCursorIsNeverCompared)$' ./scripts/ | grep -c '^Test')" = 7 \
&& go test -race -count=1 -run '^(TestNoEventPackageCostsMoreThanTheSeamItNames|TestNoBaseSubsystemDependsOnTheEventExtension|TestMerelyImportingTheEventExtensionStartsNothing|TestNothingInTheProjectionPackageOpensATransaction|TestEveryFieldOfAPublishedSpecIsRead|TestNoSnapshotAuthorityIsDeclaredOrPromised|TestCursorIsNeverCompared)$' ./scripts/ \
&& ./scripts/checks.sh deps && ./scripts/checks.sh tiers && ./scripts/checks.sh utils \
&& ./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s3 \
     '^event/receipt/[a-z_]*\.go$' \
     event/receipt/doc.go event/receipt/key.go event/receipt/fingerprint.go event/receipt/receipt.go event/receipt/claim.go event/receipt/resolve.go event/receipt/errors.go
```

**`make api` runs before `./scripts/` and that ordering is P-3**, not tidiness: five walks read
their package list out of the baseline, and running them first reports green over five packages
while the sixth is unexamined. The `grep -q` on the new section header is the cheap arm that says
the baseline actually grew.

`check-tidy` and `check-replaces` are **not** in this checkpoint, and the reason is that they have
nothing new to say here and **not** that they are red: `event/receipt` is a package of the root
module with no `go.mod`, so no require and no replace moves. Both are **green at HEAD** (§0) and
both are required green in S6, which is the section that adds to `_examples`. If either goes red in
this section it is this section's, because nothing in it should be able to move them.

#### Checkpoint output, run 2026-09-12

```
RECEIPT-LISTED=14
ok  	github.com/frostgrove/vv/event/receipt	1.019s
ok  	github.com/frostgrove/vv/event	7.146s
ok  	github.com/frostgrove/vv/event/eventmemory	1.507s
ok  	github.com/frostgrove/vv/event/eventtest	4.381s
ok  	github.com/frostgrove/vv/event/projection	5.620s
ok  	github.com/frostgrove/vv/event/receipt	1.021s
api: docs/api/surface.md regenerated — read the diff
SURFACE-SECTION-PRESENT
SCRIPTS-LISTED=7
ok  	github.com/frostgrove/vv/scripts	8.530s
check-deps: ok
check-tiers: ok
check-utils: ok
event-kernel-baseline: 170 files recorded in scripts/event_kernel.sha256
check-event-kernel: ok
the files under event/ this section moved:
  event/receipt/claim.go
  event/receipt/claim_test.go
  event/receipt/doc.go
  event/receipt/errors.go
  event/receipt/errors_test.go
  event/receipt/fingerprint.go
  event/receipt/key.go
  event/receipt/key_test.go
  event/receipt/receipt.go
  event/receipt/receipt_test.go
  event/receipt/resolve.go
  event/receipt/resolve_test.go
event-kernel-moved: ok
EXIT=0
```

`gofmt -l .` printed nothing, which is the `grep -qx 0` arm above it. The manifest moved from
**158** paths to **170**: the twelve files of the new package and nothing else — the diff of the two
manifests is twelve additions and no change to any line already in it, so `event`,
`event/projection`, `event/eventmemory` and `event/eventtest` are byte-identical to what S2 left.

The `make api` diff is **78 added lines, 0 removed, 0 changed**, all of them one new
`## github.com/frostgrove/vv/event/receipt` section: the store contract did not move, which is
INV-120's first arm, and `event/eventtest`'s own inventory counts are green unchanged, which is its
second.

**The question `make api`'s diff leaves for a person** is `Key.Value` (**P-21**): a redacted
identity now has a door its value comes back out of, and nothing but a doc comment says that door is
the ledger's. The alternatives were costed — a `LegacyKey` second type in `jobs`'s shape, or a
`Ledger` that takes the text forms instead of the values — and both put more surface in the way of
the one call that needs it. The answer stands as `Value`, and if it is ever wrong the thing to add
is the second type rather than to remove the method.

**Repository gates after the section.** `make check` all green — `check-deps`, `check-tiers`,
`check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`,
`check-otel-module`, `check-workspace`, `check-event-kernel`. `go test -count=1 ./scripts/` reports
**exactly one** `--- FAIL:` line, `TestNoI18nPackageCostsMoreThanItsErrorSeam`, which is the foreign
red named at the head of this plan and is not touched. `make unit` is green apart from that one arm;
`go build ./...`, `go vet ./event/...` and `gofmt -l .` are clean. No live suite was run: nothing
under `event/eventpg` imports `event/receipt` until S5 writes it, so there was nothing there to
compile that this section moved.

#### Two corrections made while implementing S3

1. **`Key.Value` and `ParseFingerprint` — [SPEC] §5.3 published an interface no third party could
   implement.** The finding, the argument and the two names are **P-21**, written above with the
   tensions rather than here so that the next reader of the contract meets it where the contract is.
   [SPEC] §5.3 carries both in the same change.

2. **`Claim` refuses an incomplete row BEFORE it compares fingerprints, and the order is the
   contract's own sentence.** [SPEC]'s verdict table states `Collided` without the completeness
   qualifier, so a row that is both incomplete and a stranger's could answer either. §"the three
   verdicts" decides it: *"`Claim` cannot conclude anything from it … rather than reading the
   completed-repeat branch"* — and a fingerprint comparison **is** a conclusion drawn from the row.
   So the `ErrIncomplete` return sits above `verdictOf` and `TestEveryRefusalClaimHasIsReachableThroughOnce`
   drives exactly that pair: its incomplete row is claimed under a fingerprint that also differs, and
   the answer required is `ErrIncomplete`.

#### Mutation evidence — thirty breaks, each restored

Every one was applied to a non-test file of `event/receipt`, run against the fourteen named tests
under `-race`, and reverted; the seven files were `diff`ed against their saved originals after the
campaign and are byte-identical. **None survived the second pass, and none failed by building
rather than by testing** — the two that originally reported a compile error were rewritten to
compile (`_ = authority`, `_ = held`) so that what caught them is a `--- FAIL:` line naming a test.

| What was broken | What caught it |
|---|---|
| the claim's whole transaction question removed | twelve of the fourteen — the atomicity is the one thing every case rests on |
| `sameUnit` no longer compares the ledger's transaction with the store's | `TestAClaimIsRefusedUnlessTheLedgerAndTheStoreAreOneTransaction` (the two-pool arm) |
| `sameUnit` no longer asks whether the store states transactions | `TestAClaimIsRefusedUnlessTheLedgerAndTheStoreAreOneTransaction`, and two more |
| the completion no longer checks it is the claim's own transaction | `TestACompletionThatIsNotTheClaimsIsRefusedFiveWays` |
| the completion no longer checks the commit's stream | `TestACompletionThatIsNotTheClaimsIsRefusedFiveWays` |
| the completion may be issued twice | `TestACompletionThatIsNotTheClaimsIsRefusedFiveWays` |
| a repeat may be completed | `TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage` |
| the completion no longer compares the commit's own authority | `TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage` |
| an empty commit records `Commit.Last` as its range | `TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt` — the no-op arm, which is the whole of UC-246 |
| an empty commit's authority is compared too | `TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt` |
| an unresolved row is read as a repeat | `TestARepeatIsAnsweredOnlyFromACompleteRow`, and three more |
| a fingerprint that differs is read as a repeat | `TestACollisionTravelsAsAnErrorAndNotOnlyAsAVerdict`, and two more |
| a collision travels as a verdict alone | `TestACollisionTravelsAsAnErrorAndNotOnlyAsAVerdict`, and two more |
| `Once` runs the work on a repeat | `TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt` |
| `Once` no longer completes what it claimed | `TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt` |
| a ledger answering no row at all is trusted | `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` — **a survivor on the first pass**, see below |
| a ledger answering another key's row is trusted | `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` |
| a ledger reporting a win beside a row it was not handed is trusted | `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` |
| a ledger reporting a win beside a row that already carries a range is trusted | `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` |
| the resolve's placement question removed | `TestAResolveInsideTheWritingTransactionIsRefused`, and two more |
| the resolve no longer asks the ledger where it is | `TestAResolveInsideTheWritingTransactionIsRefused` |
| a caller that cannot date its key is answered `Expired` | `TestAnAbsentRowIsUnresolvedAndAHorizonDecidesExpired` |
| the horizon boundary is past it rather than at it | `TestAnAbsentRowIsUnresolvedAndAHorizonDecidesExpired` |
| a horizon later than a row the ledger answers for is trusted | `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` |
| the resolve reads the store's `Limits` beside the placement question | `TestAResolveReadsNoEventAndOffersNothingToAppendWith` — the zero-other-calls control |
| a key renders its own value | `TestAKeyRendersNothingOfItsValue` |
| the identifier rule no longer refuses a bracket | `TestAKeyRendersNothingOfItsValue` |
| a refused key is rendered into its own refusal | `TestAKeyRendersNothingOfItsValue` and the refusal table |
| a receipt door answers an append token | `TestNoReceiptTypeExposesAnAppendToken` |
| `ParseFingerprint` accepts a digest of any length | `TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage` |

**Two of the thirty are there because the campaign found two arms nothing drove.** `claimAnswered`'s
win path checks that the row read back is the row the claim just inserted — same fingerprint, same
stream, no range, not complete — and the first draft of
`TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` ran only the two arms [SPEC] §UC-226
names. Code with no test is the same liability from the other side, so the case gained a `misread`
and a `settled` ledger and both mutations are caught. The alternative — deleting the two arms — was
refused: a winner that reads back somebody else's row is exactly the lookup a ledger gets wrong by
keying its `SELECT` on something other than the key it inserted.

**One survived the first pass, and it was a test weakness rather than a code one.** Deleting
`claimAnswered`'s zero-key arm changed nothing observable, because the arm below it —
*the row is not the key that was claimed* — subsumes it: `claimable` has already refused a zero
`spec.Key`, so a zero `held.Key` can never equal it. The two arms are kept, because *no row at all
beside a repeat* is the single-statement CTE **P-17** measured and a ledger author is owed that
sentence rather than the generic one; what changed is that
`TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` now asserts the two refusals are
**different sentences** and that each names its own defect. Re-run, the mutation is caught.

---

### S4 — the wait, live  `[x]`   ***(LIVE DATABASE · moves nothing)***

**Delivers ES-05's proof.** [SPEC] §6 items 1–10, against PostgreSQL 17.9, green **twice in a
row**, with rows read through `docker compose exec -T postgres psql -U vv -d vv` rather than
trusted from Go.

**Appendices** ES-05.

**Files** `event/eventpg/wait_integration_test.go` (new) and
`event/eventpg/main_integration_test.go` (untouched — named because it is what fails an unset DSN).
Nothing under `event/` outside `event/eventpg` moves, which the checkpoint asserts.
**Two more, added while implementing:** `event/eventpg/park_integration_test.go` — `livePark.Holds`
answers on the pool where no transaction is bound, which is S2's widened sentence of `Park`'s
contract as an implementation carries it (correction 1 below); and
`docs/ai/flows/FL-038-a-settled-cursor-becomes-a-durable-checkpoint.md`, whose *Tests that walk this
flow* list names every tagged file of `event/eventpg` and now names this one.

**Covers** UC-204, UC-206, UC-207, UC-208, UC-209, UC-210, UC-212, UC-242, UC-244, UC-245;
INV-109, INV-110, INV-112, INV-113.

**Tests** — all `//go:build integration`, in `event/eventpg`:

- `TestAConfirmedCommandIsVisibleWithoutASleep` — §UC-204. The whole appendix in one test: append,
  commit, `spec.Committed` (one `ReadStream`), `Wait`, read the read model, see the change. **No
  `sleep`, no polling of a business table, no assertion about wall-clock duration.**
  **Controls:** the same wait against a projection that is not running returns `ErrNotVisible` with
  `Reached: false, Moved: false`; and a **second** projection of the same log is asserted to be
  unaddressed by the same wait (§INV-113 item 1).
- `TestTheParkedPairIsTheWholeOfTheAppendix` — §UC-206. The one test a reasonable implementer would
  have written wrong: `Park` supplied → `ErrParked`; `Park` **nil** → `Reached: true` for the same
  parked event.
- `TestParkedInOnePartitionWhileAnotherLags` — §UC-207. A live four-partition cover; the caller's
  stream hashes into a parked partition while another is a hundred positions behind; `ErrParked` on
  poll 1. **Control:** with that partition not parked the same wiring waits for the laggard and
  reaches.
- `TestAMarkMintedInsideTheWritingTransactionIsRefused` — §UC-209, §UC-210, §INV-109. Against
  `eventpg` **specifically**, because that is the store where the naive version *works* and
  produces a mark nothing will ever deliver. Three arms: bound transaction → `ErrSpec`; a fresh
  connection while the writer is open → `ErrUncommitted`; after the writer **rolled back** →
  `ErrUncommitted` again, asserted **indistinguishable** from the previous arm. **Control:** after
  the commit, the same call mints the mark.
- `TestSlowVersusStopped` — §UC-212. Two live waits with a 200 ms deadline; `Moved` tells them
  apart; both errors carry `ErrNotVisible` and `context.DeadlineExceeded` and **neither carries
  `ErrTopology` or `event.ErrBackend`**. A third arm lands the budget **inside** a census read by
  holding the checkpoint table in `ACCESS EXCLUSIVE` from a second connection, which is the shape a
  loaded PostgreSQL answers a short per-request deadline (remediation, [S4 GAPS] GAP-2).
  **Controls:** a cancelled context returns `ctx.Err()` bare and the zero `Visibility`; and a
  deadline reached over a poll that really failed **does** carry both sentinels and the blinked
  row's cause, so the two deadlines are told apart rather than merged.
- `TestTheCallCountBudgetAndItsPlacement` — §UC-208. A recording `Park` and a recording
  `Checkpoints` over the live store: `Sequences` once per poll, `Holds` never while the queue is
  empty, **`Holes` never at all**, `Load` per member per poll, and **no bound transaction on any
  `Park` call**. **Control:** the arm over a generation with `Quarantined > 0` makes the same calls.
- `TestAWaitBesideTheProjectionsOwnLoop` — §INV-112. A wait running concurrently with the
  projection's loop under `-race`: neither fence moves and the checkpoint row's advance is the
  projection's alone.
- `TestTheDerivedSpecAgainstThreeHandWrittenOnesLive` — §UC-242. The live half: a parking
  projection and a live four-member cover; the three wrong arms assert the wrong answers.
- `TestAPollThatFailsFirstAndAPollThatFailsFourth` — §UC-244. Including **a cover taken mid-split
  against the live checkpoint table**, so the terminal/polled-through split is proven on the two
  states a deployment actually reaches. **Control:** the healthy cover reaches.
- `TestACutoverThatCommitsWhileAWaitIsRunning` — §UC-245. Both directions, live, plus a third arm
  where the cutover commits **inside the first poll's census** — the poll a caught-up deployment
  reaches on — held there by a `countedCheckpoints` that blocks load 1 until the test releases it
  (remediation, [S4 GAPS] GAP-1). **Control:** the `Generations`-nil arm answers `Reached: true` for
  the read model the caller is no longer reading.

**Checkpoint** ***(live database)***:

```sh
cp scripts/event_kernel.sha256 .git/event_kernel_before_s4
export FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable'
# … write the section …
gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 \
&& go vet -tags=integration ./event/eventpg/ \
&& test "$(go test -tags=integration -list '^(TestAConfirmedCommandIsVisibleWithoutASleep|TestTheParkedPairIsTheWholeOfTheAppendix|TestParkedInOnePartitionWhileAnotherLags|TestAMarkMintedInsideTheWritingTransactionIsRefused|TestSlowVersusStopped|TestTheCallCountBudgetAndItsPlacement|TestAWaitBesideTheProjectionsOwnLoop|TestTheDerivedSpecAgainstThreeHandWrittenOnesLive|TestAPollThatFailsFirstAndAPollThatFailsFourth|TestACutoverThatCommitsWhileAWaitIsRunning)$' ./event/eventpg/ | grep -c '^Test')" = 10 \
&& go test -race -count=1 -tags=integration -run '^(TestAConfirmedCommandIsVisibleWithoutASleep|TestTheParkedPairIsTheWholeOfTheAppendix|TestParkedInOnePartitionWhileAnotherLags|TestAMarkMintedInsideTheWritingTransactionIsRefused|TestSlowVersusStopped|TestTheCallCountBudgetAndItsPlacement|TestAWaitBesideTheProjectionsOwnLoop|TestTheDerivedSpecAgainstThreeHandWrittenOnesLive|TestAPollThatFailsFirstAndAPollThatFailsFourth|TestACutoverThatCommitsWhileAWaitIsRunning)$' ./event/eventpg/ \
&& go test -race -count=1 -tags=integration ./event/eventpg/ \
&& go test -race -count=1 -tags=integration ./event/eventpg/ \
&& ./scripts/checks.sh event-kernel \
&& diff -u .git/event_kernel_before_s4 scripts/event_kernel.sha256
```

**Corrected while implementing S4:** the counted `-list` arm read `= 11` against a pattern of ten
names and this section's own arithmetic (§ *The §6 item → test map*: "S4's ten"); it is `= 10`.

The two whole-suite runs are the "green twice in a row" rule, and the `diff` is the **opposite**
fence: this section may move nothing the manifest sees, so an empty diff is the assertion rather
than a non-empty allowed set. **The remediation below breaks that fence on purpose and says so**:
closing a `[high]` in `event/projection/wait.go` is a kernel edit, and the two digests it moves are
listed and justified under § *S4 — remediation* rather than absorbed. The `-list` arm runs before the `-run` arm because `TestMain` exits 1
on an unset DSN and a suite that skipped must not be able to report ok.

#### S4 — executed 2026-09-12, checkpoint green

Against PostgreSQL 17.9 at `postgres://vv:vv@localhost:55432/vv`, with
`FROSTGROVE_EVENTPG_TEST_PSQL='docker exec -i vv-postgres-1 psql -U vv -d vv'` set so every
`destination.rows`, `livePark.letters` and `liveGenerations.recorded` assertion is cross-checked
against what psql prints rather than against this process's own account of a query it also issued.
`EXIT=0` for the whole `&&` chain; `gofmt -l .` printed nothing and the counting arm passed before
its run.

```
LISTED=10
ok  	github.com/frostgrove/vv/event/eventpg	8.574s     # the ten named tests, -race
ok  	github.com/frostgrove/vv/event/eventpg	128.108s   # the whole tagged package, -race
ok  	github.com/frostgrove/vv/event/eventpg	139.634s   # and again
check-event-kernel: ok
EXIT=0
```

Beside it, and green on the same tree: `gofmt -l .` silent, `go build ./...`, `go vet ./event/...`,
`go test -race -count=1 ./event/...` (five packages) and `make check` (fourteen checks, including
`check-event-kernel`). `make unit` has **one** red arm and it is foreign —
`TestNoI18nPackageCostsMoreThanItsErrorSeam` in `./scripts`, the i18n CLI's
`github.com/go-json-experiment/json/jsontext` charge, which arrived with the owner's i18n work and
is untouched here.

The `diff -u .git/event_kernel_before_s4 scripts/event_kernel.sha256` printed **nothing**, which is
this section's own fence: the two files it moved are both under `event/eventpg`, which
`event_kernel_manifest` excludes by construction.

**Mutation evidence — seven breaks of `event/projection/wait.go`, each restored:**

| What was broken | What caught it |
|---|---|
| the park asked **after** the census instead of before it | `TestTheParkedPairIsTheWholeOfTheAppendix` (the answer carried a census it must not have read), `TestParkedInOnePartitionWhileAnotherLags` (`ErrNotVisible` at the deadline where `ErrParked` on poll 1 is the answer) and `TestTheCallCountBudgetAndItsPlacement` (`Sequences` once instead of once per poll) |
| `Committed`'s `authority.Valid()` refusal removed | `TestAMarkMintedInsideTheWritingTransactionIsRefused` — the mint answered `<nil>` **and minted a real mark**, which is the whole reason this case is against `eventpg` |
| the first-poll-is-terminal arm of `waiting` removed | `TestAPollThatFailsFirstAndAPollThatFailsFourth`, both the injected-failure arm and the already-split arm: each burned its deadline |
| the second `resolves` read on the reaching poll removed | `TestACutoverThatCommitsWhileAWaitIsRunning` — `<nil>` for a read model the caller is no longer reading |
| `Visibility.Moved` never set | `TestSlowVersusStopped` — `Moved:false` over ten polls at `At:122` of a mark at 301 |
| a `Holes` call added to `parked` | `TestTheCallCountBudgetAndItsPlacement`, both standings |
| the cross-spec `Mark` guard removed (**P-18**) | `TestAConfirmedCommandIsVisibleWithoutASleep`'s second-projection arm — `<nil>` for a mark minted from another projection's spec |

**Correction 1 — `livePark.Holds` answers outside a unit, and that is S2's widened sentence being
implementable.** The live queue refused every call with no transaction of its source bound, which is
right for the pass and wrong for a wait: `Park`'s contract now says *"HOLDS IS ALSO ASKED OUTSIDE A
UNIT, BY A WAIT, AND AN IMPLEMENTATION MUST ANSWER THE COMMITTED STATE THERE"*. Unfixed, every arm
whose queue holds the caller's sequence answers that refusal on its first poll — terminal — instead
of `ErrParked`, so `TestTheParkedPairIsTheWholeOfTheAppendix`, `TestParkedInOnePartitionWhileAnotherLags`
and `TestTheDerivedSpecAgainstThreeHandWrittenOnesLive` would all have failed for the fixture rather
than for the code. `Park` and `Evict` still refuse outside a unit, and
`TestTheCallCountBudgetAndItsPlacement` records, per call, whether a transaction was bound — so what
the pass buys is still measured rather than dropped.

**Correction 2 — three arms drive their polls through a `runtime.Ticks` of the test's rather than
through the clock.** `TestTheCallCountBudgetAndItsPlacement`, `TestAPollThatFailsFirstAndAPollThatFailsFourth`
and `TestACutoverThatCommitsWhileAWaitIsRunning` assert an exact poll count or need an event to land
*between* two polls; a wall-clock interval makes both a race. What is driven is the interval, never
the database: every row those arms read is one PostgreSQL holds.

**Correction 3 — releasing a poll is not the same as a poll being over, and the two arms that move
the row mid-wait wait on the census-read count.** `paced.fire` returns when the wait is blocked on
its ticker, which says the *previous* poll finished and the next one is now running — so a case that
fired and then advanced the row was racing the poll it had just released. Both
`TestTheCallCountBudgetAndItsPlacement` and `TestACutoverThatCommitsWhileAWaitIsRunning` hold their
own `countedCheckpoints` and wait for `loads` to reach the poll number before the brake comes off: a
poll that has read a row below the mark cannot then reach on what happens next. Each ran green three
times in a row after the change.

**Two live shapes the plan named and the database would not produce, recorded rather than forced:**

- **UC-242's wrong `Over` cannot be reached by running anything.** A partitioned runner beside a
  coarser row halts at its claim (`event/projection/pass.go`'s `unclaimed`), and a `Split` retires
  the parent rather than leaving its row — so the leftover coarse row a wrong cover folds over is a
  state an *operator* leaves and a runner cannot. The arm writes it through the store's own
  `event.Track`/`Save` door, with a cursor this store minted for a member that is recording, and
  says so in as many words: what that arm is about is the set a census folds, not the set a runner
  may start against.
- **UC-244's mid-split cover is a real `projection.Split`, in both directions.** Two members at mask
  1 with rows, and the split run either before the wait (terminal on poll 1) or between two driven
  polls (polled through, and the deadline decides with `ErrTopology` wrapped in). The control beside
  it is the cover the children record at, asserted **not** to answer `ErrTopology` — so the refusal
  is about the cover naming a retired member and not about the table.

#### S4 — remediation, executed 2026-09-12: [S4 GAPS] GAP-1 and GAP-2 closed

Both were `[high][immediate]` and both are now **reproduced, fixed, and pinned by a test on each
tier that fails when the fix is reverted**. Two paragraphs of contract moved with them — **P-15**
and **P-20** above, and §UC-212, §UC-244, §UC-245 and §1.1 of [SPEC] — because in both cases the
document and the code disagreed and the document was the half that was right about GAP-1 and wrong
about GAP-2.

**GAP-1 — the ownership read on the reaching poll is unconditional.** `poll`'s `if !first` guard is
deleted; `resolves` runs after the census on every poll that would answer `Reached`, including the
first. Driven live against PostgreSQL 17.9, with a `countedCheckpoints` holding census load 1 open
while the cutover commits:

```
before  PROBE A: vis={Reached:true At:2 Behind:0 Moved:false Quarantined:0 Parked:false Polls:1}
                 err=<nil>   active=3   read model: [probeone-warm|probeone@2 probeone-1|probeone@2]
after   PROBE A: vis={Reached:false At:2 ... Polls:1}
                 err=projection: this wait names a generation that is not the one reads of this
                      projection resolve to …                                          active=3
```

The read model the wait was answering `true` about is generation 2's, and `Active` already resolved
to 3 — the stale read §UC-245's *"must not"* names, on the path a caught-up deployment takes every
time. Pinned by `TestACutoverThatCommitsWhileAWaitIsRunning`'s third arm (live) and by
`TestACutoverUnderAWaitIsRefusedRatherThanAnswered`'s *"cut over inside the census of the first
poll"* arm with its no-cutover control asserting the row is read **twice on that one poll** (unit).
Reverted, they fail with `{Reached:true … Polls:1}` and `<nil>` — the exact transcript above.

**GAP-2 — a deadline that lands inside a poll is the budget, not the store.** `waiting` carries a
poll's refusal into `stopped` only when `expired` says it is not the caller's own context coming
back: `errors.Is(refusal, ctx.Err())` or `errors.Is(event.CauseOf(refusal), ctx.Err())`, the second
because a refusal answers false for `context.DeadlineExceeded` by design and the cause is the only
door onto it. Driven live over a **healthy** store, a healthy schema and a recorded row:

```
before  8 rounds at Every: 1ms, budgets 40–89 ms:  3 of 8 answered ErrTopology
        locked-table arm: topology=true backend=true
            cause=&pgconn.errTimeout{err:context.deadlineExceededError{}}
after   8 rounds, same wiring:                     0 of 8
        locked-table arm: {Reached:false At:0 Polls:1}
            projection: this change was not visible … and Visibility carries what the last poll
            that could be read saw: context deadline exceeded
```

`eventpg` answers **two** shapes and the fix covers both: the bare `ctx.Err()` when
`Checkpoints.opened` sees a done context, and `event.Failure(Unclassified, …)` — which becomes
`event.ErrBackend` at the read door — when the clock lands inside the statement. The second is
reachable deterministically by holding the checkpoint table in `ACCESS EXCLUSIVE` from a second
connection, which is what the live arm does. Pinned by `TestSlowVersusStopped`'s third arm and the
two negative assertions on its existing arms (live), and by
`TestADeadlineSaysWhichKindOfNotYetItWas`'s first-poll arm (unit). Each has a **control** asserting
that a poll which really could not be made **does** carry `ErrTopology`, `event.ErrBackend` and the
blinked row's cause — without it the positive arm would pass for a wait that had stopped wrapping
refusals at all, which is §UC-244's rule going the other way.

**The manifest moved, and this is what moved.** Two digests, both paths already in § *The complete
set of paths phase 5 may add to the manifest*: `event/projection/wait.go` (the two-line guard, the
`expired` helper and three doc paragraphs) and `event/projection/wait_test.go` (one new arm, one new
control, one assertion inverted). No path appeared, none disappeared, nothing under
`event/eventtest/` moved, and no exported name changed — `make api` regenerates `docs/api/surface.md`
byte-identical. `diff -u .git/event_kernel_before_s4 scripts/event_kernel.sha256` is therefore
**not** empty any more, and its whole content is those two lines.

**Checkpoint, re-run at the remediated tree** (same DSN, `FROSTGROVE_EVENTPG_TEST_PSQL` set, so the
cross-checking fixtures compared against psql rather than skipping):

```
gofmt -l . | wc -l                                                   0
go vet -tags=integration ./event/eventpg/                            ok (exit 0)
go test -tags=integration -list '^(the ten)$' … | grep -c '^Test'    LISTED=10
go test -race -count=1 -tags=integration -run '^(the ten)$' …        ok 10.867s
go test -race -count=1 -tags=integration ./event/eventpg/            ok 125.425s
go test -race -count=1 -tags=integration ./event/eventpg/  (again)   ok 126.035s
./scripts/checks.sh event-kernel-baseline                            170 files recorded
./scripts/checks.sh event-kernel                                     check-event-kernel: ok
diff -u .git/event_kernel_before_s4 scripts/event_kernel.sha256      2 lines, both wait*.go
go build ./...                                                       ok
go vet ./event/...                                                   ok
go test -race -count=1 ./event/...                                   ok (5 packages: 7.1s 1.5s
                                                                     4.4s 6.1s 1.0s)
make check                                                           11 arms, all ok
make api                                                             docs/api/surface.md unchanged
make unit                                                            exactly one --- FAIL:
                                                                     TestNoI18nPackageCostsMoreThanItsErrorSeam (foreign)
```

`make unit` is **not** green and this record does not claim it is: the one red arm is
`TestNoI18nPackageCostsMoreThanItsErrorSeam` in `./scripts`, the i18n CLI's
`github.com/go-json-experiment/json/jsontext` charge, which arrived with the owner's i18n work, is
untouched here and is the owner's to settle. `make check` reports **eleven** `ok` arms, which is
what this tree has — the "fourteen" in the S4 record above is [S4 GAPS] GAP-6, a `[low]` left in the
backlog rather than fixed, so the number is corrected here and not there.

**GAP-3, GAP-4, GAP-5 and GAP-6 were not touched**, per the 2026-09-08 delivery policy: they are
`[medium]`/`[low]` and stand in `EVENTSOURCE_BACKLOG.md` `## P5` as items 44–47.

---

### S5 — the receipt and the bounded read, live  `[x]`   ***(LIVE DATABASE · moves nothing)***

**Delivers ES-07's and ES-08's proof.** [SPEC] §6 items **11–22 and 24** — item 23 is §6's own
routing into `event` and lands in S1 (§ *The §6 item → test map*) — plus the four ledger defects
(§ *The conformance extension*). Sixteen tagged tests and one benchmark.

**Appendices** ES-07, ES-08.

**Files** `event/eventpg/receipt_integration_test.go`, `event/eventpg/stateat_integration_test.go`
(new). The reference `Ledger` it runs is `_examples/event-receipts`' — which S6 publishes — so S5
carries its own copy of the statements in a test fixture and S6 asserts the two are the same SQL,
byte for byte and in order, with `TestTheExampleLedgerIsTheOneTheLiveSuiteProved`.

**Covers** UC-214, UC-215, UC-216 (the live half, §6 item 14), UC-217, UC-218, UC-220, UC-222,
UC-223, UC-227, UC-228/UC-229 (live), UC-232 (live), UC-246, UC-247, UC-248, UC-250, UC-251;
INV-114, INV-115, INV-116 (the live half), INV-117, INV-125, INV-126.

**Tests** — all `//go:build integration`, in `event/eventpg`:

- `TestClaimAppendCompleteAndTheRollbackControl` — §UC-214, rows in `psql`.
- `TestTwoCallersRaceOneKey` — §UC-217, §INV-114, **P-17**. Run under **READ COMMITTED,
  REPEATABLE READ and SERIALIZABLE**, bound by the caller through
  `crudsql.DB.WithTxOptions(&sql.TxOptions{Isolation: …})`, and asserting a **different** thing per
  level because that is what the database does:
  - *READ COMMITTED* — the loser **blocks** on the primary-key index, its insert reports zero rows,
    its following `SELECT` reads the winner's row, and it answers `Repeated` **with the winner's
    range**.
  - *REPEATABLE READ and SERIALIZABLE* — the loser blocks and then fails with SQLSTATE **`40001`**,
    reached through `errors.Is(err, …)` on the classified cause and never by string; **nothing is
    appended**; and **the retry answers `Repeated`** with the winner's range.

  `psql` row counts on every arm: one receipt for the key, one copy of the events. **A level that
  is dropped rather than asserted fails this section** — the two stricter arms are the only
  evidence that a 40001 loser is a rollback and not a second append, which is the whole of what
  D-142's [[D-126]] clause rests on. **Control:** the winner **rolling back** leaves the loser free
  to insert and append, so the block resolved on the writer's outcome and not on a timeout.
- `TestTheTwoPoolRefusalBesideTheOnePoolAcceptance` — §UC-218, and the five completion refusals of
  §UC-248 each followed by a `psql` read asserting the row still carries the first attempt's range
  or none at all.
- `TestUnresolvedWhileOpenAndAfterARollbackAreIndistinguishable` — §UC-220, §INV-115. A second
  session held **idle-in-transaction**; `Resolve` does not block and carries the horizon; the
  committed arm answers `Found`; the rolled-back arm answers `Unresolved` **again**, and the case
  asserts the two absences are indistinguishable. Beside it, §UC-249's resolve issued **inside** the
  writing transaction and refused — the failure this arm would otherwise mask.
- `TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms` — §UC-223, §INV-115. Four standings across a
  configured horizon, with the horizon taken from `MIN(recorded_at)` in one arm and from
  `now() - retention` computed in SQL in the other; two skew arms with the caller's instants
  deliberately offset in each direction, each asserted to produce the standing the arithmetic says
  and **neither producing a false `Found` nor a false "it did not happen"**.
- `TestThePublishedClaimBindsNoInstantAsAParameter` — §INV-115. The claim statement is read and
  asserted to carry `statement_timestamp()` and no bound instant.
- `TestTheLostConnectionRetryEndToEnd` — §UC-227, §UC-215, §INV-125. A killed connection between
  `COMMIT` and the response; a **second process** holding only the key; a re-load at the moved
  version, a fresh decision, a digest asserted **equal** across the process boundary; `Repeated`
  with the original range; one copy of the events in `psql`. **Control:** the identical retry
  **without** a key, taking the naive path, is run and asserted to write the events **twice** — so
  the mechanism's value is measured rather than asserted.
- `TestAnUnresolvedClaimRefusesItsRetry` — §UC-222 beside §UC-246's completed empty range answering
  `Repeated`, which is the pair that keeps `Incomplete` an honest defect report.
- `TestACodecThatDoesNotEncodeTheSameBytesTwice` — §UC-247, §INV-125. A fact recording
  `time.Now()`; the retry is `Collided` and nothing is appended twice and nothing is reported done
  that was not. **Control:** the same aggregate taking its instant from the command retries to
  `Repeated`.
- `TestOneKeyTwoStreamsAndTheKeyPerAppendControl` — §UC-250, §INV-126. The inverted assertion: one
  key over two streams answers `Repeated` for an operation that differs at the second, and the case
  asserts that **wrong** answer. **Controls:** the key-per-append spelling answers `Repeated` for A
  and `Collided` for B; and completing A's claim with B's commit is `ErrSpec`.
- `TestTwoMisorderedCallersAndTheirOnceControl` — §UC-251. A caller that appends after `Repeated`
  has its `Complete` refused and `psql` shows **one** copy; a caller that claims after appending is
  refused by nothing and `psql` shows **two**, which the case asserts. **Controls:** the same two
  through `Once` leave one copy each; and a first-arm variant that never calls `Complete` leaves two.
- `TestFourLedgerDefectsEachBreakTheCaseThatNamesThem` — § *The conformance extension*. The four
  decorators, each asserted to make its named case fail.
- `TestThePrefixAtEveryBoundaryOfARealStream` — §UC-228, §UC-229 live, across `StreamPage`
  boundaries with recorded page counts.
- `TestPastTheEndAnEmptyStreamAndVersionZeroLive` — §UC-230, §UC-231.
- `TestTheUnreadableEventTableLive` — §UC-232, with the assertion that `Load` on the same stream
  gives the identical refusal.
- `TestTheCollisionTableLiveBesideTheVersionVariant` — §UC-216, §INV-116, **[SPEC] §6 item 14**.
  The collision table through a real `Ledger` row rather than through `Digest` alone: one key
  claimed and completed, then re-presented three ways that must differ — one byte of one payload, a
  different stream, the same records reordered — each answering `Collided` and arriving as
  `ErrCollision` from `Claim` itself. **Beside the arm that must NOT differ:** the identical records
  re-presented after the stream moved on, at a different expected version, answering `Repeated` with
  the first attempt's range. `psql` counts one copy of the events on every arm and one row for the
  key. The version arm is what makes the other three mean "the content decided it"; without it the
  test would pass on a digest that fingerprinted everything.
- `BenchmarkStateAt` — beside `BenchmarkStreamReplay`, reporting the bounded read at **1 %, 50 %
  and 100 %** of a 100 000-event stream. It is **not a gate**; the numbers go to
  `EVENTSOURCE_BACKLOG.md` `## P5` (**P-14**), where ES-09's Gate 1 will find them.

**Checkpoint** ***(live database)***:

```sh
cp scripts/event_kernel.sha256 .git/event_kernel_before_s5
export FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable'
# … write the section …
gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 \
&& go vet -tags=integration ./event/eventpg/ \
&& test "$(go test -tags=integration -list '^(TestClaimAppendCompleteAndTheRollbackControl|TestTwoCallersRaceOneKey|TestTheTwoPoolRefusalBesideTheOnePoolAcceptance|TestUnresolvedWhileOpenAndAfterARollbackAreIndistinguishable|TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms|TestThePublishedClaimBindsNoInstantAsAParameter|TestTheLostConnectionRetryEndToEnd|TestAnUnresolvedClaimRefusesItsRetry|TestACodecThatDoesNotEncodeTheSameBytesTwice|TestOneKeyTwoStreamsAndTheKeyPerAppendControl|TestTwoMisorderedCallersAndTheirOnceControl|TestFourLedgerDefectsEachBreakTheCaseThatNamesThem|TestThePrefixAtEveryBoundaryOfARealStream|TestPastTheEndAnEmptyStreamAndVersionZeroLive|TestTheUnreadableEventTableLive|TestTheCollisionTableLiveBesideTheVersionVariant)$' ./event/eventpg/ | grep -c '^Test')" = 16 \
&& go test -race -count=1 -tags=integration -run '^(TestClaimAppendCompleteAndTheRollbackControl|TestTwoCallersRaceOneKey|TestTheTwoPoolRefusalBesideTheOnePoolAcceptance|TestUnresolvedWhileOpenAndAfterARollbackAreIndistinguishable|TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms|TestThePublishedClaimBindsNoInstantAsAParameter|TestTheLostConnectionRetryEndToEnd|TestAnUnresolvedClaimRefusesItsRetry|TestACodecThatDoesNotEncodeTheSameBytesTwice|TestOneKeyTwoStreamsAndTheKeyPerAppendControl|TestTwoMisorderedCallersAndTheirOnceControl|TestFourLedgerDefectsEachBreakTheCaseThatNamesThem|TestThePrefixAtEveryBoundaryOfARealStream|TestPastTheEndAnEmptyStreamAndVersionZeroLive|TestTheUnreadableEventTableLive|TestTheCollisionTableLiveBesideTheVersionVariant)$' ./event/eventpg/ \
&& go test -race -count=1 -tags=integration ./event/eventpg/ \
&& go test -race -count=1 -tags=integration ./event/eventpg/ \
&& go test -tags=integration -run '^$' -bench '^BenchmarkStateAt$' -benchtime 20x -count 3 ./event/eventpg/ \
&& ./scripts/checks.sh event-kernel \
&& diff -u .git/event_kernel_before_s5 scripts/event_kernel.sha256
```

Every `psql` assertion in this section is a **row count read out of the database**, not a Go value:
`docker compose exec -T postgres psql -U vv -d vv -tAc 'select count(*) from …'`. The whole point of
UC-227, UC-250 and UC-251 is how many copies of the events exist, and Go is the party under test.

#### S5 — executed 2026-09-12, checkpoint green

Against PostgreSQL 17.9 at `postgres://vv:vv@localhost:55432/vv`, with
`FROSTGROVE_EVENTPG_TEST_PSQL='docker exec -i vv-postgres-1 psql -U vv -d vv'` set, so every
`receiptStand.copies`, `receiptStand.row` and `receiptStand.rowCount` assertion is cross-checked
against what psql prints rather than against this process's own account of a query it also issued.
`EXIT=0` for the whole `&&` chain.

```
LISTED=16
ok  	github.com/frostgrove/vv/event/eventpg	3.866s     # the sixteen named tests, -race
ok  	github.com/frostgrove/vv/event/eventpg	127.352s   # the whole tagged package, -race
ok  	github.com/frostgrove/vv/event/eventpg	128.706s   # and again
BenchmarkStateAt/1_per_cent-20     20	   1235713 ns/op	      1236 ns/event
BenchmarkStateAt/1_per_cent-20     20	   1117943 ns/op	      1118 ns/event
BenchmarkStateAt/1_per_cent-20     20	   1070646 ns/op	      1071 ns/event
BenchmarkStateAt/50_per_cent-20    20	  65425826 ns/op	      1309 ns/event
BenchmarkStateAt/50_per_cent-20    20	  68269076 ns/op	      1365 ns/event
BenchmarkStateAt/50_per_cent-20    20	  71209222 ns/op	      1424 ns/event
BenchmarkStateAt/100_per_cent-20   20	 137064539 ns/op	      1371 ns/event
BenchmarkStateAt/100_per_cent-20   20	 125590370 ns/op	      1256 ns/event
BenchmarkStateAt/100_per_cent-20   20	 120504206 ns/op	      1205 ns/event
check-event-kernel: ok
EXIT=0
```

`diff -u .git/event_kernel_before_s5 scripts/event_kernel.sha256` printed **nothing**, which is this
section's own fence: both files it adds are under `event/eventpg`, which `event_kernel_manifest`
excludes by construction. The benchmark's numbers are in `EVENTSOURCE_BACKLOG.md` `## P5` item 48
with their derivation and the sentence that they are **not** Gate 1 (**P-14**); an earlier run of
the same arm, an hour apart, is recorded beside them.

Beside it, and green on the same tree: `gofmt -l .` silent, `go build ./...`, `go vet ./event/...`,
`go test -race -count=1 ./event/...` (five packages: 7.5s 1.5s 4.4s 6.1s 1.0s), `make check`
(**eleven** `ok` arms, `check-event-kernel` among them) and `make api`, whose diff over
`docs/api/surface.md` is **S1–S3's 117 lines unchanged** — S5 publishes no exported name, and the
diff was read line by line to confirm it carries nothing of this section's.

`make unit` is **not** green and this record does not claim it is: exactly one arm is red and it is
foreign — `TestNoI18nPackageCostsMoreThanItsErrorSeam` in `./scripts`, the i18n CLI's
`github.com/go-json-experiment/json/jsontext` charge, which arrived with the owner's i18n work and
is untouched here.

**Mutation evidence — eighteen breaks, each restored.** Fourteen of `event/receipt`, two of
`event/repo.go`, one of `event/repo.go`'s fold loop and one of the fixture's own frozen SQL. Every
one of the sixteen tests is named by at least one row, so no test in this section is passing
vacuously. `event/receipt` is untracked at HEAD, so the harness restored each file from an in-memory
copy rather than through `git checkout`, and `check-event-kernel` was re-run afterwards.

| What was broken | What caught it |
|---|---|
| `Claim`'s `ErrIncomplete` arm removed | `TestAnUnresolvedClaimRefusesItsRetry` |
| `verdictOf` never answers `Collided` | `TestTheCollisionTableLiveBesideTheVersionVariant` (all three arms) and `TestACodecThatDoesNotEncodeTheSameBytesTwice` |
| `Complete`'s `authority.Same` comparison removed | `TestTheTwoPoolRefusalBesideTheOnePoolAcceptance` — the *admitted* control failed, because the second-transaction arm had already written the range |
| `Complete` admits a verdict that is not `Recorded` | `TestTwoMisorderedCallersAndTheirOnceControl` |
| `sameUnit` stops comparing the two transactions | `TestTheTwoPoolRefusalBesideTheOnePoolAcceptance` |
| a resolve inside the writing transaction is admitted (the **store** arm only) | `TestUnresolvedWhileOpenAndAfterARollbackAreIndistinguishable`, **after** the third arm was added — see correction 2 |
| a zero `Issued` is compared rather than refused a conclusion | `TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms`, both horizon spellings |
| `findAnswered` stops checking the published horizon | `TestFourLedgerDefectsEachBreakTheCaseThatNamesThem` (the optimistic-horizon arm) |
| `Once` runs its work on every verdict | `TestTwoMisorderedCallersAndTheirOnceControl`'s `Once` control |
| `Complete` stops comparing the commit's stream | `TestOneKeyTwoStreamsAndTheKeyPerAppendControl`'s crossed-commit control |
| a second `Complete` on one claim is admitted | `TestTheTwoPoolRefusalBesideTheOnePoolAcceptance` |
| the digest covers the version the token was loaded at | `TestTheCollisionTableLiveBesideTheVersionVariant`'s version arm **and** `TestTheLostConnectionRetryEndToEnd` — the two halves of the same decision, one in a process that died and one in the process that retried |
| `StateAt` stops refusing a stream shorter than its bound | `TestPastTheEndAnEmptyStreamAndVersionZeroLive` |
| the bound is exclusive of the version asked for | `TestThePrefixAtEveryBoundaryOfARealStream` |
| `Complete` writes no range onto the row it claimed | `TestClaimAppendCompleteAndTheRollbackControl` |
| a claim answers `Recorded` whatever the insert reported | `TestTwoCallersRaceOneKey`, all three levels |
| an unreadable envelope is skipped rather than refused | `TestTheUnreadableEventTableLive`, three of four arms (the fourth is the store's own refusal and is not reached by a kernel mutation) |
| the published claim binds `$5` instead of `statement_timestamp()` | `TestThePublishedClaimBindsNoInstantAsAParameter` |

**Correction 1 — a racer claims first and loads afterwards, and the case says why.** §UC-217's
*Given* is *"both then appending to the same stream at the same expected version"*, and written that
way the case cannot see what it is about: a loser wrongly told `Recorded` appends at the version it
decided at, `eventpg`'s own single-statement CAS refuses it with `ErrConflict`, and **no second copy
lands whatever the ledger did**. The store's version check is a second line of defence and it masks
the first. So both racers claim and then load inside their own unit — which is the order §1.2 gives
and the one `Once` makes unwritable the other way round — and the loser that is wrongly admitted
loads the version the winner left and appends beside it. With the reference ledger the work never
runs; with the `select-first` decorator `psql` shows **two** copies. The layering this exposes is
recorded as backlog `## P5` item 50 `[low]`, because the module page states the receipt's value over
the version check and nowhere states that a test at one version proves nothing about the other.

**Correction 2 — §UC-249 needs a third arm, and a mutation is what found it.** The case shipped with
two of the three resolves [SPEC] names — the claiming transaction's own context, and a context
carrying a transaction of the ledger's and not the store's — and both were caught by
`outsideEveryTransaction`'s **ledger** check, because on the claiming context the ledger resolves the
same transaction. Deleting the **store** check left the whole suite green. The third arm is the
mirror: a transaction of the store's bound, with a ledger on a second handle that has none, where
only the store check can refuse. With it, the mutation fails as it must. This is [SPEC] §UC-249's own
three-call *Given* read literally rather than folded into two.

**Correction 3 — the unreadable-event table needs a page bound, and the reason is a real difference
between the two refusals.** Three of the four variants are the kernel's (`ErrUnknownType`,
`ErrRevision`, `ErrUpcast`) and refuse the **envelope**; the fourth is the store's own
(`errRowOutsideSchema` → `event.ErrBackend`) and refuses the **page it scanned**. At the default page
size the control *"a bound below the broken version reads normally"* therefore failed on the fourth
variant alone, for the store's reason and not for the bound's. The case runs at `StreamPage: 3`, which
puts the broken version 4 in the second page, and the control then holds for all four — and the
difference between a row-level and a page-level refusal is now visible in the case rather than
discovered by whoever next changes a page size.

**Correction 4 — the dirty-find decorator is caught by a second door when the ledger is empty, and
the arm was made to break the case it names.** `Find` on the claiming connection first answered
`ErrLedger`, not `Found`: the uncommitted row is dated `statement_timestamp()` inside the open
transaction while `Horizon` reads `MIN(recorded_at)` over **committed** rows — none — and publishes
`now()`, which is after the row it just answered for. `findAnswered`'s horizon rule caught it. That
is the framework working, and it is not the case §UC-220 is about, so the arm now commits one
ordinary operation first (which is what gives a ledger a horizon at all) and then holds its
transaction open: the defect answers `Found` for an operation that can still roll back, which is the
sentence the decorator exists to falsify. The empty-table horizon itself is backlog `## P5` item 49
`[medium]`.

**One live shape recorded rather than forced: the blocked loser is read out of `pg_stat_activity`.**
`receiptStand.waitsOnTheIndex` polls for a backend with `wait_event_type = 'Lock'` inside a statement
naming this ledger's table, and the winner does not commit until it sees one. A fixed sleep would
make *"the loser blocks on the primary-key index"* an assumption; this makes it a row PostgreSQL
printed, and it is also what keeps the six races (three levels × the rollback control) at a fifth of
a second each instead of a hard-coded hold.

---

### S6 — the decisions, the docs, the examples and the roadmap close-out  `[x]`   *(no database · moves nothing under `event/`)*

**Delivers ES-09 in full — D-145, a contract with no code — and closes the roadmap.** This is the
last section of the last phase, so it carries every documentation obligation the roadmap has
accumulated, not only this phase's.

**Appendices** ES-09 (whole), and the documentation half of ES-05, ES-07, ES-08.

#### The four decisions

- **D-142 — an operation receipt is the application's table beside the append.** The [[D-118]]
  decision that file demands before a durable table is written. It carries the claim-before-append
  order with the two-retries-at-different-versions failure it exists for, and `Once` as the call
  that owns it; **the fingerprint's contents and the deliberate absence of the expected version**,
  with the worked retry that its presence made unanswerable and the statement that the anchor is
  **recorded** as `First - 1` rather than compared; the byte-reproducibility obligation and its
  fail-closed failure mode; **the preimage's layout as a frozen format** — the field order, the
  eight big-endian bytes of each length and of each revision, the composed stream that opens it,
  and the fact that a fingerprint is read back by a later build than the one that wrote it, so a
  tidy inside the retention window turns every in-flight key into a false `Collided`; the
  one-key-one-append rule with the key-per-append recipe; and `Store.Append` staying
  non-deduplicating. Links [[D-118]] [[D-122]] [[D-125]] [[D-126]] [[UC-032]]. Two of its sections
  are rewritten by this round's audit and are specified here because both were about to record
  something false.

  **§ The serialisation point** (**P-17**, [GAPS] GAP-1). The primary-key index is the
  serialisation point, and the decision states it as *the index plus the level's conflict
  behaviour* rather than as a level-independent property, because the level-independent claim was
  executed and is false. It names: the two statements and that their **order** is the mechanism;
  that a single statement cannot do it at READ COMMITTED, with the one-snapshot reason; SQLSTATE
  **`40001`** at REPEATABLE READ and SERIALIZABLE, the fact that it rolls the caller's whole unit
  back rather than splitting it, and that the **retry** answers `Repeated`; that `40001` is already
  `errs.Retryable()` in this tree (`event/eventpg/classify.go:69-79`) so the retry has a home and
  the retry is the **caller's**, through `crud.InNewTx`; and that this framework still sets no
  isolation level anywhere, which is the whole of what [[D-126]] asks. The measured table from
  **P-17** goes in, including the `DO UPDATE` row and the 3035 ms / 34 ms third-caller number that
  refuses it, so the one-statement form is not re-proposed by the next reader who notices it works.

  **§ What `jobs` already does, and why a receipt is not one** (**[GAPS] GAP-5**). The repository
  already ships a key → three-way-verdict mechanism with a live PostgreSQL driver, and D-142 must
  adjudicate it **by symbol** rather than name its enum in passing. The symbols, read at HEAD:

  - `jobs.EnqueueOnce` / `jobs.EnqueueOnceIn` (`jobs/queue.go:461`, `:509`), driven by a
    `ProducerIntent`, producing `PlacementOnce` (`:635`) and answering
    **`EnqueueOnceOutcome`** — `EnqueueCreated` / `EnqueueExistingSamePayload` / `EnqueueConflict`
    (`:260-266`), a rename of `PlacementOutcome` (`:229-241`). *That* is the triple.
  - `jobspg.Driver.placeExisting` (`jobs/jobspg/driver.go:109-151`), case `jobs.PlacementOnce`
    (`:119-127`): `samePayloadDigest` decides `PlacementExistingSamePayload` against
    `PlacementConflict` — the same key, same content is a repeat, different content is a conflict.
  - reached through `findIntent` (`jobs/jobspg/repo_ops.go:58`, a `SELECT … FOR UPDATE`) then
    `insertIntent` (`:232-250`, `INSERT … ON CONFLICT … DO NOTHING`, `rows != 1 → errIntentConflict`).

  **Two corrections to the finding as raised, because a decision that records a false comparison is
  worse than one that records none.** *First*, `jobs.Unique` (`:383`) is **not** this mechanism:
  it sets `PlacementUnique`, whose existing-key answer is `PlacementExisting`
  (`jobspg/driver.go:146`) with **no payload comparison at all** — "one live job per key", not the
  triple. D-142 names `EnqueueOnce`, not `Unique`. *Second*, the losing path does **not** hand a
  caller `errIntentConflict`: both `Driver.Place` (`driver.go:45-54`) and `TxStager.Stage`
  (`stager.go:112-121`) retry it up to three times, and the retry's `findIntent` sees the winner's
  committed row and answers `EnqueueExistingSamePayload`. Only three genuine conflicts in a row
  become `jobs.RejectPlacement(jobs.ErrConflict)`.

  **And the argument that a job cannot express a receipt is therefore not the atomicity one.**
  `jobspg.Driver.Place` (`driver.go:22-42`) **does** enlist in the caller's ambient transaction when
  one is bound — `crud.ExecutorFor` → `crudsql.Transaction` → `TxStager.Stage` → `placeInTx` — so a
  placement *can* be one commit with an append. The four reasons that hold are these, and each is
  against the real API:
  1. **A job is a row that will run.** `EnqueueOnce` creates a delivery a worker leases, retries and
     dead-letters; a receipt is a row nobody executes. Expressing one as the other needs a no-op
     handler and makes the invocation's lease/attempt/DLQ lifecycle the receipt's.
  2. **A receipt must be readable later, by key, from another process — and `jobs` cannot be
     asked.** The intent key is `scope/revision/purpose/digest` (`repo_ops.go:38-56`), a **hash**;
     the key's preimage is never stored, and `jobs` publishes no "what happened to intent K" read.
     `Resolve`'s `Unresolved` and `Expired` are unanswerable, which is the whole of ES-07's «зачем».
  3. **A receipt records what the append produced.** `Held.Complete(ctx, commit)` writes `First` and
     `Last` onto the claimed row *after* the append, in the same transaction; a placement's
     `PayloadDigest` is fixed at enqueue and there is no second write to the placed row.
  4. **There is no horizon.** A swept `jobs` invocation is indistinguishable from one that never
     existed — exactly what `Ledger.Horizon` exists to prevent — and `jobs` publishes nothing to
     compare an `Issued` against.

  Plus the boundary reason: `event/receipt`'s closure is `event`, `crud`, `errs`, `utils` and the
  standard library (**P-2**), and importing `jobs` would be a new subsystem edge from the event
  extension into another subsystem.

  **The three-way verdict is convergent, not borrowed**, and D-142 says so in those words: both
  arrive at created / existing-same-content / conflict because that is what a content-addressed
  idempotency key can answer. The decision names the one place they differ and why —
  `receipt.Collided` travels as an **error** (`ErrCollision`) while `EnqueueConflict` is a value,
  because `EnqueueOnce` *performs* the placement and leaves nothing to ignore, whereas a claim's
  placement is the caller's next statement.

  **Which a consumer reaches for**, one sentence on `docs/modules/{en,ru}/receipt.md` and one on
  `jobs`'s page **— corrected in S6: there is no `jobs` module page, so the reciprocal direction
  points at [[FL-035]] and the missing page is backlog `## P5` item 57** —: reach for `receipt` when
  the question is *what happened to this operation* and the
  answer must be readable later, by key, from another process; reach for `jobs.EnqueueOnce` when the
  question is *has this work already been scheduled* and the answer is only needed at the moment of
  scheduling. Without those two sentences the repository holds two answers to "the same key was
  presented twice" and no document saying which — the third-mechanism outcome
  `EVENTSOURCE_REFERENCE.md` exists to prevent.

  **Is `jobspg`'s losing path a defect of `jobs` worth a backlog row?** Yes, and not the one the
  finding named. The retry makes the READ COMMITTED case correct; above it, `TxStager.Stage`
  retries on a transaction it does not own and that the first attempt's `40001` has already
  aborted, so all three attempts fail and the caller gets `jobs.ErrConflict` for a plain repeat.
  That is **backlog `## P5` item 23**, `[medium]`, measured, and it is a change to `jobs` with
  tests in three drivers — it is not repaired here, and D-142 links it so the comparison it draws
  is against `jobs` as it actually is.

  **Held by** `grep -c 'EnqueueOnce\|PlacementOnce\|jobspg' docs/ai/decisions/D-142-*.md` being
  non-zero, and by the ledger-defect table's row 1 as **P-17** rewrote it.
- **D-143 — an absent receipt is not a rollback, and the third answer is the point.** Why `Resolve`
  has four standings and a claim has three; what `Unresolved` means in PostgreSQL terms and why no
  lock closes it; what a caller does with it; why a claim answers `ErrIncomplete` rather than a
  fourth verdict, and why `Resolve` refuses inside the writing transaction; how retention bounds the
  window through a horizon rather than letting a swept row read as a rollback; **which clock each
  instant comes from** — `statement_timestamp()` for the row, SQL for the horizon, the caller's own
  for the optional `Issued` — with the skew's error term and the argument that it decides only
  between two non-conclusions; and the **fourth standing that was designed and refused**, the
  `pg_current_xact_id`/`pg_snapshot_xmin` proof `eventpg`'s walk already makes, with its cost
  stated so it is not re-proposed. Names `idle_in_transaction_session_timeout` as the **inherited**
  lever (`EVENTSOURCE_REFERENCE.md` §Documentation obligations item 1) rather than inventing a
  second one. Links [[D-126]] [[D-128]].
- **D-144 — a bounded read is a read and never a load.** Why `StateAt` returns no token and why the
  absence of a second return **is** the mechanism; why version zero, a version past the end and an
  empty stream are one refusal; why it is not a widening of UC-032 §6, clause by clause (not a
  cache, not a snapshot, not a partial rehydration); why there is no timestamp boundary and what one
  would have to be; and why `Store.ReadStream` does not grow a ceiling — with the one-page
  arithmetic and Reject 3 as the precedent. Carries **P-13**'s two warnings for the phase that adds
  the base state. Links [[D-128]] [[D-132]] [[UC-032]].
- **D-145 — a snapshot's contract, accepted without code.** The whole of [SPEC] §1.4, plus § *The
  snapshot gate*'s measured numbers and the sentence that says why they are not Gate 1's. It
  **amends** [[D-132]] rather than superseding it: D-132's invariant and its enforcement test stand,
  and D-145 is the contract its re-entry trigger opens into. A `See also` line is added to D-132
  pointing at it, and **D-132 is not marked superseded.**

All four get rows in `docs/ai/decisions/Index.md` in the same change.

#### The roadmap close-out — what the last phase owes, in full

| Obligation | What lands | Held by |
|---|---|---|
| **Module pages, `en`** | new `docs/modules/en/receipt.md`; the wait section on `projection.md`; `Digest`/`StateAt` on `event.md`; one cross-reference on `eventpg.md` from the global-`xmin` stall section, because that is the condition under which a wait freezes and a receipt stays unresolved | `TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs`, `TestEveryTestNameTheDocsCiteExists` |
| **Module pages, `ru`** | the localised twin of every one of the four | the same walks; `TestNoDocPromisesExactlyOnceDelivery` is English-only and that limitation is P1 backlog item 8, restated here rather than silently inherited |
| **Module index rows** | `docs/modules/{en,ru}/Index.md` gain a `receipt` row beside `event`, `eventmemory`, `eventpg`, `projection`, `eventtest` — **six event packages, six rows, both languages** | a row count asserted by the checkpoint |
| **The flows** | [[FL-036]] gains `StateAt`, `Digest` and `ErrVersion` in its body **and** its file table; [[FL-038]] gains the wait, because it is the checkpoint flow and a wait is a read of those rows; **FL-043** is new — *an uncertain append becomes a resolvable operation* — because neither receipt door is on the decision-to-fact path. **FL-039 is already taken** by the i18n merge (backlog `## P4` item 63), so the receipt flow is 043 and the number is chosen here rather than discovered at write time | `TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex`, widened to every non-test file under `event/` |
| **The reverse index** | `docs/ai/flows/Index.md` gains every file this phase touches, **including the three whose only change is a comment**; and `effect.go`/`generation.go`'s rows are pointed at flows whose bodies name them — backlog `## P4` item 63, which is inside this obligation rather than a medium being fixed because it is quick: a reverse-index row pointing at a flow that does not mention the file **is** "the flows mapping every source file" not being met | the widened walk above, plus a new arm asserting each mapped flow's own file table names the file |
| **Use cases** | three new repo-level use cases — **UC-036** the wait, **UC-037** the receipt, **UC-038** the historical read — in `docs/ai/usecases/modules/event/`, each with rows in both tables of `docs/ai/usecases/Index.md`; and **one `See also` line on [[UC-032]]**. **No clause of UC-032's `What must hold` changes and its `Out of scope` gains nothing**, because nothing that was out of scope came in | a doc check asserting UC-032's two sections are byte-identical to their predecessor |
| **The usage guide** | new `docs/usage-guides/event-sourcing.md` — the guide a consumer follows to stand the thing up: declare an aggregate, bind a store, run a projection, wait for a read, claim an operation. There is no such guide today and the roadmap's last phase is where it is owed. **Its Go is compiled**: `_examples/event-guide` is the page's code and nothing else, added while closing [GAPS] GAP-1 — the two name-matching checks below read the page and are structurally unable to see a call, an argument order or a struct literal, which is how four API errors got onto it with both green | `TestEveryGoFenceInTheEventSourcingGuideIsCompiled` (three controls), `TestNoDocCallsASupervisorMethodTheTypeDoesNotHave`, `TestEveryTestNameTheDocsCiteExists`, `TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs` |
| **Examples** | `_examples/event-wait` (append → `WaitOf` → `spec.Committed` → `Wait` → read, with the parked branch written out) and `_examples/event-receipts` (the reference `Ledger`: the `INSERT`-then-`SELECT` claim with `statement_timestamp()` and the sentence saying why the read is a statement of its own, the completing `UPDATE`, a SQL-side `Horizon` from a configured retention, a periodic that sweeps — **corrected in S6 from a `jobs` periodic to `runtime.Every`, because a `jobs` schedule needs a second module, a schema version and a worker fleet to issue one `DELETE`; backlog `## P5` item 58**, `Once` as the shape the command uses, the open-coded form beside it, `ErrCollision` and `ErrIncomplete` both branched on, and a `Resolve` path that reports in-flight from a key with **no** `Issued`); two rows in `_examples/README.md`; and **`_examples/event-guide`**, which is not a program — it is the usage guide's Go, compiled, added while closing [GAPS] GAP-1 | `make examples`, `TestTheExampleLedgerIsTheOneTheLiveSuiteProved`, and `TestEveryGoFenceInTheEventSourcingGuideIsCompiled` |
| **`make api`** | regenerated once more, with the `event/receipt` section and the `event/projection` block in place; the diff read by a person | the checkpoint |
| **The roadmap** | the `### Приложение ES-05`, `ES-07` and `ES-08` sections are **deleted** from `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` and the opening paragraph is rewritten to name them as done and say where the result lives — **exactly phase 4's precedent**, which deleted five sections and left *«выполнены и удалены из этого списка … здесь их больше нет, потому что это список открытого»* with links to [[D-140]], [[D-141]], [[FL-038]], [[FL-042]]. Ours links D-142, D-143, D-144, [[FL-036]], [[FL-038]], **FL-043** and the module pages. **`### Приложение ES-09` stays** and its `Статус` line becomes *"контракт — D-145; оба gate не выполнены"*, so the one open item carries its own definition of done. `docs/roadmaps/Roadmap.md` §15 names what now ships and drops what this roadmap closed | `scripts/roadmap_test.go`, plus the three heading greps in the checkpoint |
| **The release note** | `docs/release-notes/v0.1.0.md` records the one widened sentence of `Park.Holds`, written **as a widening**, and the sentence that a wait never asks `Holes` | a doc check for both sentences |
| **The backlog** | `EVENTSOURCE_BACKLOG.md` `## P5` gains `BenchmarkStateAt`'s numbers (**P-14**), the `receipttest` deferral with its argument, P-6's uncertified position-origin, and every `[medium]`/`[low]` this phase's own section reviews raised | the Debt table below |

#### Doc checks written or widened here

- `TestNoProjectionGuideRestatesHighestAsThePagesLastPosition` — §INV-108. Both `projection.md`
  pages walked for the forbidden restatement, with a **fixture paragraph that must be reported**.
- `TestNoEventGuideOffersATimestampBoundary` — §UC-235. Both `event.md` pages assert that a
  timestamp is neither business time nor commit order, cite `recorded_at` being
  `statement_timestamp()`, and say that one could only ever be a lookup resolving to a version; no
  example sorts by it. **Control:** a fixture page that omits the sentence is reported.
- `TestTheThreeObligationsAWaitCannotCheckAreStatedTogether` — §INV-111, §INV-125, §INV-126. The
  three `WaitSpec` obligations on the projection pages beside `Sequencer`'s three; the
  byte-reproducibility obligation and the key-per-append recipe on `receipt.md`. **Control:** a
  fixture page stating two of three is reported.
- `TestEveryGoFenceInTheEventSourcingGuideIsCompiled` — [GAPS] GAP-1. Every fenced Go block of
  `docs/usage-guides/event-sourcing.md` compared with `_examples/event-guide`, line for line, in
  order, with whitespace collapsed so gofmt owns the shape and the comparison is about code.
  **Three controls**, one per error the section shipped: `event.Bind`'s arguments reversed, an
  `eventpg.Spec` literal without `DB`, and a `Partition` where `WaitOf` takes a `Cover` — and each
  control fails loudly if the line it mutates has left the page, so it cannot go vacuous.
- `TestNoDocCallsASupervisorMethodTheTypeDoesNotHave` — [GAPS] GAP-1's fourth error, which is
  inherited and appears in three places. Every `supervisor.X(` in `docs/` checked against the
  exported methods `runtime.Supervisor` actually declares, read out of the source rather than
  listed. **Control:** a fixture calling `Add` and `Start` reports exactly `Add`.
- `TestTheExampleLedgerIsTheOneTheLiveSuiteProved` — the example's **two** claim statements and
  S5's fixture compared byte for byte **and in order** (**P-17**), so **P-10**'s "reference
  implementation" is a fact rather than a label. **Control:** a fixture whose two statements are
  swapped is reported, which is what keeps the ordering half of the comparison from being vacuous.

**Checkpoint** (no database):

```sh
cp scripts/event_kernel.sha256 .git/event_kernel_before_s6
# … write the section …
gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 \
&& make api \
&& test "$(grep -c '^| \[receipt\](receipt.md)' docs/modules/en/Index.md)" = 1 \
&& test "$(grep -c '^| \[receipt\](receipt.md)' docs/modules/ru/Index.md)" = 1 \
&& test "$(ls docs/modules/en/{event,eventmemory,eventpg,eventtest,projection,receipt}.md | wc -l)" = 6 \
&& test "$(ls docs/modules/ru/{event,eventmemory,eventpg,eventtest,projection,receipt}.md | wc -l)" = 6 \
&& test -f docs/ai/flows/FL-043-an-uncertain-append-becomes-a-resolvable-operation.md \
&& test -f docs/usage-guides/event-sourcing.md \
&& for f in $(find event -name '*.go' -not -name '*_test.go' -not -path 'event/testdata/*'); do \
     row=$(grep -m1 "| \`$f\` |" docs/ai/flows/Index.md) || { echo "no flow row: $f"; exit 1; }; \
     for fl in $(printf '%s' "$row" | grep -oh 'FL-[0-9]*'); do \
       grep -q "$f" docs/ai/flows/$fl-*.md || { echo "$fl does not name $f"; exit 1; }; \
     done; done \
&& test "$(go test -list '^(TestEveryTestNameTheDocsCiteExists|TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs|TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex|TestNoProjectionGuideRestatesHighestAsThePagesLastPosition|TestNoEventGuideOffersATimestampBoundary|TestTheThreeObligationsAWaitCannotCheckAreStatedTogether|TestTheExampleLedgerIsTheOneTheLiveSuiteProved|TestNoSnapshotAuthorityIsDeclaredOrPromised|TestEveryGoFenceInTheEventSourcingGuideIsCompiled|TestNoDocCallsASupervisorMethodTheTypeDoesNotHave)$' ./scripts/ | grep -c '^Test')" = 10 \
&& go test -race -count=1 -run '^(TestEveryTestNameTheDocsCiteExists|TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs|TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex|TestNoProjectionGuideRestatesHighestAsThePagesLastPosition|TestNoEventGuideOffersATimestampBoundary|TestTheThreeObligationsAWaitCannotCheckAreStatedTogether|TestTheExampleLedgerIsTheOneTheLiveSuiteProved|TestNoSnapshotAuthorityIsDeclaredOrPromised|TestEveryGoFenceInTheEventSourcingGuideIsCompiled|TestNoDocCallsASupervisorMethodTheTypeDoesNotHave)$' ./scripts/ \
&& go test -race -count=1 ./scripts/ 2>&1 | tee .git/s6_scripts.log; \
test "$(grep -c '^--- FAIL: ' .git/s6_scripts.log)" = 1 \
&& grep -q '^--- FAIL: TestNoI18nPackageCostsMoreThanItsErrorSeam' .git/s6_scripts.log \
&& grep -q 'D-145' docs/ai/decisions/Index.md \
&& grep -q 'D-145' docs/ai/decisions/D-132-*.md \
&& test "$(grep -c 'superseded' docs/ai/decisions/D-132-*.md)" = 0 \
&& R=docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md \
&& test "$(grep -cE '^### Приложение ES-0(5|7|8) ' $R)" = 0 \
&& test "$(grep -cE '^### Приложение ES-09 ' $R)" = 1 \
&& grep -q 'D-145' $R \
&& make examples \
&& ./scripts/checks.sh tidy \
&& ./scripts/checks.sh replaces \
&& ./scripts/checks.sh event-kernel \
&& diff -u .git/event_kernel_before_s6 scripts/event_kernel.sha256
```

**The whole-package `./scripts/` run is the arm that closes [GAPS] GAP-6, and the counted `-run`
arm above it stays.** They answer different questions: the counted arm proves this phase's ten
new and widened doc checks exist and ran; the whole-package arm proves the **other six** in
`scripts/docs_test.go` did not go red under four new documentation files and a new usage guide.
`TestNoDocPromisesExactlyOnceDelivery` (`:981`) walks all of `../docs` and the page most likely in
this repository's history to contain the words *"exactly once"* is a new page about **operation
idempotency**; `TestEveryNameTheProjectionPackageRenamedIsOnBothGuidesAsARename` (`:764`) walks the
two `projection.md` pages S6 edits; `TestTheStatusADocPromisesIsTheOneTheFunctionItNamesReturns`
(`:253`) reads every status a doc promises. S6 is the last section of the last phase, so nothing
downstream would have caught them. **Exactly one `--- FAIL:` line is permitted and it must be the
i18n one** — that is what the two `test`/`grep` arms assert, and it is why the run is `; test …`
rather than `&& …`: `go test` exits non-zero on the foreign red and the pipeline must survive it to
read the log. Every other failure is this section's.

**`check-tidy` and `check-replaces` run after `make examples`** and both must be green. S6 adds
`_examples/event-wait` and `_examples/event-receipts`; `_examples/go.mod` requires no
`event/eventpg` today and names no `replace` for it, so a receipts example that reaches a
PostgreSQL store adds a require that needs a matching `replace` — invisible under `go.work` and
visible only to these two `GOWORK=off` arms. Both were measured green on the pristine tree (§0), so
a red here is this section's and is repaired here.

The `for` loop is the roadmap's flow obligation made into a command, and it has **two** arms
because one of them is vacuous alone: every non-test source file under `event/` has a reverse-index
row, **and every flow that row points at names the file in its own body**. `event/testdata/**` is
excluded because a fixture is not source.

#### S6 — executed, 2026-09-12

Every arm of the checkpoint above, run in order. Real output.

```
$ gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 && make api && …
api: docs/api/surface.md regenerated — read the diff
ARMS 1-8 OK                       # both receipt index rows, six module pages per language,
                                  # FL-043, the usage guide, and the two-armed flow loop

$ test "$(go test -list '^(…ten names…)$' ./scripts/ | grep -c '^Test')" = 10
10

$ go test -race -count=1 -run '^(…the same ten…)$' ./scripts/
ok  	github.com/frostgrove/vv/scripts	17.126s

$ go test -race -count=1 ./scripts/ 2>&1 | tee .git/s6_scripts.log | tail
    i18n_test.go:31: the i18n extension reaches github.com/go-json-experiment/json/jsontext outside its error seam and declared MessageFormat/CLDR ecosystem
FAIL
FAIL	github.com/frostgrove/vv/scripts	136.653s
FAIL
$ test "$(grep -c '^--- FAIL: ' .git/s6_scripts.log)" = 1 && grep -q '^--- FAIL: TestNoI18nPackageCostsMoreThanItsErrorSeam' …
EXACTLY ONE FOREIGN FAIL

$ grep -q 'D-145' docs/ai/decisions/Index.md && grep -q 'D-145' docs/ai/decisions/D-132-*.md \
  && test "$(grep -c 'superseded' docs/ai/decisions/D-132-*.md)" = 0 && … the three heading greps …
D-145 / D-132 / ROADMAP ARMS OK   # ES-05/07/08 headings: 0 · ES-09 headings: 1 · D-145 named

$ make examples
ok  	github.com/frostgrove/vv/_examples/event-guide	0.007s
?   	github.com/frostgrove/vv/_examples/event-receipts	[no test files]
?   	github.com/frostgrove/vv/_examples/event-wait	[no test files]
EXAMPLES_EXIT=0

$ ./scripts/checks.sh tidy       → check-tidy: ok
$ ./scripts/checks.sh replaces   → check-replaces: ok
$ ./scripts/checks.sh event-kernel → check-event-kernel: ok
$ diff -u .git/event_kernel_before_s6 scripts/event_kernel.sha256
KERNEL DIFF EMPTY
```

**The counted arm reads ten and not eight**, and both new names arrived with [GAPS] GAP-1: the
checkpoint below the table was widened in the same pass that closed it, so that the arm counting
this section's doc checks counts the ones that hold its usage guide. The numbers above are the
re-run at the closing tree, not the first pass's.

Beyond the checkpoint, the three whole-tree commands, run rather than asserted:

```
$ make check
check-deps: ok · check-tiers: ok · check-utils: ok · check-triplets: ok · check-todo: ok
check-replaces: ok · check-tidy: ok · check-otel-schema: ok · check-otel-module: ok
check-workspace: ok · check-event-kernel: ok            # every arm, exit 0

$ make unit
--- FAIL: TestNoI18nPackageCostsMoreThanItsErrorSeam (0.08s)
FAIL	github.com/frostgrove/vv/scripts	137.286s
                                        # ONE --- FAIL: line across 62 ok packages, and it is the
                                        # foreign i18n one. make unit is NOT reported green.
                                        # That red stops the workspace arm before ./_examples, so
                                        # `make examples` is what runs the examples' own tests and
                                        # it is green — including event-guide's.

$ go build ./...                        # exit 0
$ go vet ./event/...                    # clean
$ go test -race -count=1 ./event/...
ok  event  7.054s · eventmemory  1.504s · eventtest  4.369s · projection  6.104s · receipt  1.020s

$ FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1 -tags=integration ./event/eventpg/
ok  	github.com/frostgrove/vv/event/eventpg	127.439s
```

The live suite is re-run here although S6 changed no Go file under `event/`: a section that edits
`scripts/` and `_examples/` has no business claiming the tagged suite is unaffected without asking
it. `gofmt -l .` is silent and `go vet ./event/...` is clean.

**The `make api` diff is empty**, and that is the arm rather than a disappointment: S6 changed no
non-test Go file outside `scripts/`, so the surface it regenerates is the one S5 left. The same
sentence is why `diff -u` against the S6 predecessor is empty and why `check-event-kernel` is green
without a re-baseline.

**Beyond the checkpoint, driven rather than described.** Both new examples were **run against the
live database** (PostgreSQL 17.9 at `localhost:55432`), because a reference implementation nobody
executed is a label:

```
$ cd _examples && GOWORK=off go run ./event-wait
reached after 2 poll(s): at=1 behind=0 moved=true parked=false
a mark minted inside the writing transaction is refused, before the commit it would have described
parked after 2 poll(s): quarantined=0, 1 letter(s) held, sequences [waitorder/parked-…]

$ cd _examples && GOWORK=off go run ./event-receipts
Once: verdict=[verdict recorded] range=1..1 complete=true
repeat: verdict=[verdict repeated] range=1..1, and no second append
open-coded: 201, and this attempt wrote it
collision: the same key with other content is refused, and nothing was appended
incomplete: the retry is refused, and Resolve reports [standing incomplete] — a defect report, not a state to retry through
in flight: [standing unresolved], horizon 2026-09-11T17:39:07+05:00 — report it as in flight, do NOT re-issue the command
in flight: after the commit it resolves [standing found] at 1..1
sweep: "receipt-sweep" would run hourly; one pass removed 0 row(s) older than 24h0m0s
```

**Five mutations, each restored, and which test caught which.**

| What was broken | Reported by |
|---|---|
| the example ledger's two claim statements exchanged in source order | `TestTheExampleLedgerIsTheOneTheLiveSuiteProved` — *"statement 1 is `claimInsertStatement` in the live suite and `claimSelectStatement` in the example … which is the whole of the finding this comparison exists for"*, **and** its own swapped-fixture control, which then reported that the comparison was satisfied |
| «не порядок фиксации» removed from `docs/modules/ru/event.md` | `TestNoEventGuideOffersATimestampBoundary` — the Russian page named, the clause named |
| the key-per-append recipe reworded on `docs/modules/en/receipt.md` | `TestTheThreeObligationsAWaitCannotCheckAreStatedTogether` |
| the pre-S6 restatement of `Progress.Highest` put back on `docs/modules/ru/projection.md` | `TestNoProjectionGuideRestatesHighestAsThePagesLastPosition`, quoting the sentence |
| a reverse-index row deleted; then, separately, `FL-038`'s body stopped naming `event/projection/wait.go` | `TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex`, **one arm each** — which is what shows the second arm is not the first one restated |

**Three deviations from this section's own text, each recorded rather than absorbed.**

1. **There is no `jobs` module page**, so the reciprocal *"which a consumer reaches for"* sentence
   points at [[FL-035]] instead. Backlog `## P5` item **57**, `[low]`.
2. **The receipts example sweeps with `runtime.Every`** rather than a `jobs` periodic: a `jobs`
   schedule needs a second module, a schema version and a worker fleet to issue one `DELETE`.
   Backlog `## P5` item **58**, `[low]`.
3. **Both new examples need a database.** `ParkSequence` refuses a `Destination` of
   `projection.Unchecked`, so the parked branch cannot be driven over a map behind a mutex; and a
   receipt is only a receipt if it commits with the events. `_examples/go.mod` gains
   `event/eventpg` with its matching `replace`, which is exactly what the two `GOWORK=off` arms
   above exist to catch — and both are green.

#### S6 — [GAPS] GAP-1 closed, 2026-09-12

`docs/usage-guides/event-sourcing.md` shipped with four API errors. **Each was reproduced by the
compiler before it was touched**, in a throwaway package under `_examples/` with the page's own
lines in it, one fixed at a time so the next error could surface:

```
vet: probe.go:27:30: in call to event.Bind, type *eventpg.Store of store does not match
                     *event.Aggregate[S, ID] (cannot infer S and ID)                        # :144
vet: probe.go:33:36: cannot use projection.Whole() (value of struct type projection.Partition)
                     as projection.Cover value in argument to projection.WaitOf             # :237
vet: probe.go:43:13: supervisor.Add undefined (type *runtime.Supervisor has no field
                     or method Add)                                                    # :39, :197
$ go run ./s6probe
partIII: eventpg: this store cannot be assembled from this spec: Spec.DB is nil, and a store
over no database is a store that refuses everything at its first call                       # :140
```

The fourth is the one that compiles, which is why it is the worst of them: a reader concludes the
`Source` is the handle and meets the refusal at run time with a `Spec` literal that looked
complete. The page now opens the pool and names both halves — `sql.Open` then `crudsql.Postgres`
over the **same** `*sql.DB` — because `New`'s third refusal is a `Source` and a `DB` that are two
different databases, and a snippet that hides the handle cannot show that.

Two elisions went with them. Part III's and Part VI's transaction blocks carried `...` and
unchecked `err`, so neither would have compiled had anybody tried; both are written out. **No fence
on the page now elides anything**, and the comparison below deliberately supports no skip marker —
a fence that needs one is a fence to split.

**What holds it is a command, and the fourth close criterion is where the value is.** The gates the
section named for this file, `TestEveryTestNameTheDocsCiteExists` and
`TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs`, match backticked test names and
`file.go:Symbol` citations; neither can see a call. So the page's Go is now compiled:
`_examples/event-guide` is that Go and nothing else, `make examples` builds and vets it, and
`TestEveryGoFenceInTheEventSourcingGuideIsCompiled` compares the two line for line and in order.
Both directions are held — reverting the page reports drift, reverting the package fails to build.

**Four mutations, each reverting one shipped fix, each caught, each restored.**

| What was put back | Reported by |
|---|---|
| `event.Bind(Orders, store)` | `TestEveryGoFenceInTheEventSourcingGuideIsCompiled` — *"whose first 9 of 10 lines are compiled and which then diverges at …"* |
| `eventpg.Spec{Source: source, …}` without `DB` | the same, diverging at line 5 of 10 — **the error that compiles, caught by a text comparison rather than by a compiler** |
| `projection.WaitOf(spec, projection.Whole())` | the same, 4 of 5 |
| `supervisor.Add(following)` | the same (*"no line of ../\_examples/event-guide reads that"*) **and** `TestNoDocCallsASupervisorMethodTheTypeDoesNotHave`, naming the four methods the type has |

Each of the three controls also refused to pass on the mutated page — *"no fence carries …, so the
control mutates nothing and proves nothing"* — which is what keeps a control from surviving a
rewrite of the line it exists to mutate.

`supervisor.Add` was the inherited one and it is gone from all three places: both `projection.md`
pages now show `runtime.Auto(following)` beside the sentence that a supervisor takes its runners at
construction. Nothing else on those pages moved.

**One pre-existing page defect was repaired because this section's own new check reported it.**
Both `projection.md` pages restated `Progress.Heighest`'s promise as *"the read page's last
position"*, which [[D-128]]'s `What it forbids` names by name — *"it is the same number and a
different promise"*. The sentence's intent (a partition that matches nothing still advances) is
kept and the restatement is gone. Backlog item 43's *"both module pages still say `Holds` runs
inside your unit of work, full stop"* is closed by the same edit pass, as the planned four-place
announcement of the widened sentence rather than as a medium fixed because it was quick.

---

**The second arm is red at HEAD on one file and S6 inherits it**: `FL-036` does not name
`event/eventtest/sections_topology.go`, which the index maps to it. That is backlog `## P4` item
63's shape — an index an agent trusts and then finds empty — and it is inside this obligation
rather than a medium being fixed because it is quick. Run the loop before writing anything, so the
section knows which failures are its own. (`effect.go` and `generation.go`, which item 63 also
named, now map to `FL-042`, whose body does name them; only the `eventtest` row is still open.) The `D-132` arms are UC-241 made into a command: the amendment is present and the word
**superseded** is not.

---

## The deliverable checklist

- [x] `event/eventtest` is byte-identical, proved by every section's `event-kernel-moved` allowed
      set excluding it and by the three no-database sections' fences.
- [x] `docs/api/surface.md`'s `event` section grew by **exactly three** lines — `Repo.Digest`,
      `Repo.StateAt`, `ErrVersion` — with nothing removed or changed (S1's diff arm).
- [x] `Store`, `Log`, `Checkpoints`, `Limits`, `Capabilities`, `Envelope`, `Record`,
      `AppendRequest` and `Outcome` are unchanged in every field and method;
      `eventtest.inventory()` holds the same **twenty** sections, `RunCheckpoints` the same
      **fourteen**, and `event/eventpg`'s `checkpointCensus()` is untouched.
- [x] **One** sentence of `Park`'s contract moved, it is `Holds`'s, and it is announced in four
      places — `park.go`, both `projection.md` pages, and the release note. `Holes` is asked by no
      wait, on any generation, at any `Quarantined` count (UC-208's recording park).
- [x] `event/receipt` is charged at zero in `scripts/event_test.go` and reaches `crud`, `errs`,
      `utils` and nothing else (**P-2**); the package count arm moved 5 → 6.
- [x] `checkedEventPackages` lists **six** packages and its floor says so (**P-3**).
- [x] **`go test -race -count=1 ./scripts/` was run whole by S6** and reported exactly one
      `--- FAIL:` line, `TestNoI18nPackageCostsMoreThanItsErrorSeam` — the one foreign red, which is
      the owner's and is neither repaired nor allowlisted here. `make unit` was then run as a
      command rather than asserted as a result, and reports the same single arm.
- [x] `make check` is green on **all eight** arms — `check-deps`, `check-tiers`, `check-utils`,
      `check-triplets`, `check-todo`, `check-replaces`, `check-tidy` and `check-event-kernel`.
      `check-tidy` and `check-replaces` were measured green on the pristine tree (§0), S6 runs them
      after `make examples`, and a red in either is **this phase's** and is repaired here.
- [x] `make vet` clean; `gofmt -l` silent; `make api` regenerated and its diff read.
- [x] The live suite ran **twice in a row** with the DSN set in both S4 and S5, and did not skip.
      S5: `127.352s` then `128.706s`, under `-race`, whole package.
- [x] Every one of [SPEC] §6's **twenty-four** items is named in § *The §6 item → test map* against
      a test that ran — twenty-two by the twenty-six tagged tests, item 23 untagged in `event` by
      §6's own routing, item 24 by `BenchmarkStateAt`. The map was re-extracted after the last edit
      to it and no item is unmapped.
- [x] `TestTwoCallersRaceOneKey` asserts **all three** isolation levels, each with the answer that
      level actually gives (**P-17**); no level was dropped. The `40001` arms assert nothing was
      appended and that the **retry** repeats, with `psql` row counts on every arm. The two stricter
      levels reach their refusal through `errs.AsFault` — `CodeSerializationFailure`,
      `KindRetryable` — and never by string.
- [x] A `Mark` minted from one `WaitSpec` and waited on another is refused with `ErrSpec` at both
      doors (**P-18**), pinned untagged in S2 with its same-spec control and live in S4 beside
      UC-204's second projection.
- [x] `make examples` builds both new examples and both run, and it builds and tests
      `_examples/event-guide` — the usage guide's Go ([GAPS] GAP-1), which is not a program.
- [x] D-142, D-143, D-144 and D-145 are written and indexed; [[D-132]] carries a `See also` and is
      **not** marked superseded; `TestNoSnapshotAuthorityIsDeclaredOrPromised` is green with its
      fixture control still reporting.
- [x] UC-036, UC-037 and UC-038 exist with rows in both of `docs/ai/usecases/Index.md`'s tables;
      [[UC-032]] gained a `See also` and **no clause**.
- [x] Six event packages have a module page in **both** languages and a row in both indexes.
- [x] Every non-test source file under `event/` has a reverse-index row **and** the flow it points
      at names it in its own file table.
- [x] `docs/usage-guides/event-sourcing.md` exists **and its Go compiles**: every fenced Go
      block on it is `_examples/event-guide`, line for line, held by
      `TestEveryGoFenceInTheEventSourcingGuideIsCompiled` with three controls ([GAPS] GAP-1).
- [x] ES-05, ES-07 and ES-08 are **removed** from the roadmap's open list; ES-09's row stays open
      with D-145 as its definition of done; `Roadmap.md` §15 names what ships.
- [x] Every `[critical]`/`[high]` raised by a section's review is closed and re-audited **before**
      the next section starts; every `[medium]`/`[low]` is appended to `EVENTSOURCE_BACKLOG.md`
      under `## P5` and left alone.

---

## Debt

Recorded, scheduled, and **not** fixed in this phase.

**Already in `EVENTSOURCE_BACKLOG.md` `## P5`** — eleven findings from the round-1 spec audit, nine
`[medium]` and two `[low]`. The ones this plan touches the edge of, and does not close:

| Backlog `## P5` | What stays open | Why this phase does not close it |
|---|---|---|
| 1. the appendix's «degraded» deadline answer has no verdict `[medium]` | one deadline answer ships, plus `Moved` | **P-9** sharpens what `Moved` is and is not; a second verdict is surface [SPEC] froze |
| 2. `Digest`'s result is never tied to what `Append` writes `[medium]` | nothing checks the `changes` handed to both are the same | structurally uncheckable; INV-125 states the obligation and UC-247 pins the fail-closed mode |
| 3. `Mark`'s contents and rendering `[medium]` | comparability and process lifetime unstated | `String()` and the two doors are specified; the rest is surface |
| 4. `ResolveSpec.Store` has no stated purpose `[medium]` | — | **closed by [GAPS] GAP-4**; the row stays for the record |
| 5. INV-110/INV-112 state call orders rather than properties `[medium]` | the invariants stay procedural | the falsifiers are behavioural, so nothing is unproved; restating them is a spec edit |
| 6. ES-09's `SnapshotFilter` composition rule `[medium]` | D-145 specifies five column comparisons and no composition | one sentence in D-145 would close it and the delivery policy says a medium is not fixed because it is quick — **it is recorded in D-145's own "open questions" list instead**, which is the honest half |
| 7. ES-09's «оператор может отбросить snapshot» has no invariant `[medium]` | the property is not stated in a form a test could take | D-145 is a contract without code; the invariant belongs to the phase that writes the code |
| 8. "a snapshot decode that panics falls back" assumes a `recover` `[medium]` | the clause stands unargued | same — D-145 records it as an open question rather than asserting it settled |
| 9. `Visibility.Behind` is a `Position` used as a distance `[medium]` | a second field of the shape `## P4` item 1 already carries | [SPEC] §5.2 froze it; the phase that fixes one fixes both |
| 10. a wait against a generation with no checkpoint rows `[low]` | — | **answered in passing by P-7**: the module page now states `surveyed`'s third answer |
| 11. §0 counts three kernel names and lists four `[low]` | the spec's paragraph | a spec edit, not a code one |

**New to the backlog in this phase**, appended by S6 under `## P5`:

- **`BenchmarkStateAt`'s numbers**, as numbers, so ES-09's Gate 1 has both halves of the arithmetic
  and the deferral does not acquire the argument [[D-132]] refused it (**P-14**). Alongside them,
  § *The snapshot gate*'s measured replay figures: **12.91–14.88 ms at 10 000 events, 108.4–111.3 ms
  at 100 000**, PostgreSQL 17.9, 2026-09-12.
- **A `receipttest` conformance harness** for a `Ledger`. The four obligations are proved for the
  reference implementation by four injected defects (§ *The conformance extension*) and a third
  implementation is now *provable* and none exists — the same call, with the same reasoning, that
  left the park's byte bound uncertified in phase 4. `[medium]`.
- **A published origin for a store's first position** (**P-6**). The zero-`Mark` discriminator
  rests on positions starting at 1, both shipped stores have it, and no conformance section asks
  for it; the mint refuses a zero position, so the failure is a refusal and not a forged mark.
  `[low]`.
- **`TestNoDocPromisesExactlyOnceDelivery` is English-only**, so every `docs/modules/ru/` page this
  phase adds is unreadable to it — P1 backlog item 8, restated because this phase adds two more
  Russian pages to the blind spot rather than because it is new. `[medium]`. **S6 now runs the test
  itself** as part of the whole-package `./scripts/` arm, so the English half is no longer unrun;
  what stays open is the Russian half.
- **`jobspg`'s ambient placement cannot retry a lost race above READ COMMITTED** — already appended
  as `## P5` item **23**, `[medium]`, while closing [GAPS] GAP-5. A finding about `jobs` rather than
  about `event`, measured on PostgreSQL 17.9, and named by D-142 so the comparison that decision
  draws is against `jobs` as it is.

**Not debt, and named so it is not read as debt:** ES-09 ships as **D-145, a contract with no
code**, because [[D-132]]'s gate is a measured *need* and § *The snapshot gate* took the
measurement and found a *cost*. That is route (c), it is what [APX] part 4 literally asks for, and
the phase that eventually writes the code implements a decision rather than re-deriving one.
