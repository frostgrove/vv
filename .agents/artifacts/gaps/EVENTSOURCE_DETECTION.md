# EVENTSOURCE — detection measurement — GAPS

## Round 2 — dispositions — 2026-09-13

All six are dispositioned below, each against the reproduction that came first. GAP-1, GAP-2 and
GAP-3 are **fixed**; GAP-4 and GAP-6 were cheap once the code was open and are **fixed** too;
GAP-5's documentation half is **fixed** and its inventory half is **scheduled** in
`EVENTSOURCE_BACKLOG.md` item 1, which now names the four thin sections and the ceiling.

One close criterion was **refused with its argument**: GAP-3's suggestion that the three refusals
carry the row they saw. `event/refusalmessages_test.go` forbids a refusal from rendering a `Version`
— "a version, a position and a cursor are the three numbers an operator's log would let anybody
replay a history from" — and the first attempt at it turned that gate red. The three sentences are
distinct by naming the *field* that was not fresh instead: already complete, a first version, a last
version. GAP-3's second question — whether an empty commit's range deserves a representation of its
own — is answered by [[D-143]] and declined below.

## Round 1 — test reviewer (independent measurement) — 2026-09-13

Nothing in this round was fixed. Sixty-five implementation mutations and fourteen assertion
neutralisations were applied one at a time and reverted; five test files were added and removed.
The whole tree was hashed before and after (2366 files) and the two manifests are identical
(`sha256(manifest) = a6b50f40c2f708a97981ac3e0373582090e2c24b7c475ae6f03385d92113c2a1`),
`git status --porcelain` unchanged (65 lines, the two stages' own paths).

**Method.** Every number below is mine. I did not inherit the previous stages' figures; where I
could re-derive their "before", I did, by cloning the repository at `07b681e`, discarding the two
stages' uncommitted work (`git checkout -- . && git clean -fd`, 0 dirty lines) and running the
same mutation set against that tree.

---

### Headline numbers

| Measurement | Result |
|---|---|
| Implementation mutations, current tree | **61 killed / 65 applied = 94%** |
| The 47 of those that also exist at `07b681e`, current tree | **44 / 47 = 94%** |
| The same 47, pre-stage tree (`07b681e`, stages discarded) | **42 / 47 = 89%** |
| Assertion neutralisation in the seven `Run` section files (sample of 14) | **1 / 14 = 7%** |
| Sections named by no defect row | **1 of 52** (`RunCheckpoints`/`durability`) — **0 of 52 after round 2** |
| Sections whose body can be emptied with the tree green | **1 of 52** (the same one) — **0 after round 2** |
| Consumer harnesses that fail against a defective implementation | **3 of 3**, each naming the obligation |
| `event/` suite runtime | 14 s wall (`event` 7.5 · `eventmemory` 1.5 · `eventtest` 13.4 · `projection` 6.1 · `receipt` 1.0, parallel) |
| Live tagged suite | 134.7 s then 139.9 s, both green |

**These two numbers measure different things and must not be compared.** 94% is the rate at which
the tree's tests detect a defect in the *implementation*. 7% is the rate at which the tree's tests
detect the deletion of one *assertion inside the conformance suite*. The second has a structural
ceiling — see GAP-5 — of about 24%, because the suite is falsified by a 43-row defect inventory
against 179 assertion sites. The backlog's `27/165 = 16%` and the previous stage's `19/185 = 10.3%`
are of the second kind; my 94% is of the first. Neither number supersedes the other.

---

### GAP-1 [critical][immediate] the ledger harness certifies a ledger under which every collision reads as a repeat

- **Where:** `event/eventtest/sections_ledger.go:45-66` (`ledgerRepeatSection`),
  `event/eventtest/defects_ledger.go:33-35` (the `echoes` row), consequence at
  `event/receipt/claim.go:272-281` (`verdictOf`).
- **What:** I wrote a `receipt.Ledger` decorator whose `Claim`, when it loses, answers the row its
  table holds **except** that it copies back the fingerprint it was *asked with*. This is the
  ordinary shape of a `RETURNING` list that binds a parameter instead of a column, or of a mapper
  that fills the struct from the request and overwrites only the fields it recognises.
  `eventtest.RunLedger` certified it: all seven sections `passed`, exit 0.
- **Why this severity:** `verdictOf` decides `Repeated` on `held.Fingerprint.Equal(spec.Fingerprint)`.
  Under such a ledger that is true for **every** loser, so a genuine `Collided` — a *different*
  operation submitted under a key some earlier operation already spent — is answered `Repeated`
  with a nil error. `receipt.Once` then skips the work and returns a `Held` whose receipt names the
  first operation's range. The caller is told "this already happened" about an operation that never
  ran, and `Resolve` reports the wrong events for that key. `ErrCollision` becomes unreachable.
  This is the same class of silent wrong answer the harness was built to remove, from the other
  side: not history duplicated, history suppressed.
- **Root cause, measured:** the harness never drives a collision. `ledgerRepeatSection` claims key
  `"repeat"` twice with the *same* fingerprint (`this.receipt("repeat", 2)` at both lines 49 and 60),
  so `sameRow`'s fingerprint arm compares a value against itself. The `echoes` inventory row is
  caught only by the *range* arm (`First`/`Last`/`Complete`), which is why it still reports a defect
  and why this one slips past it. Verified: `probeReturningOnly` (a loser answered no row) **is**
  caught, by `sameRow`'s key arm — so the section is not inert, it is partitioned wrong.
- **Close criteria:**
  - [x] `ledgerRepeatSection` now drives **two** losers through one helper, `receipts.lost`: one
        claiming with the fingerprint the row holds and one with another operation's. The section is
        partitioned rather than given a seventh rule — `sameRow`'s fingerprint arm was always the
        right assertion and had nothing to compare.
  - [x] `ledgerDefects()` carries `answers the fingerprint it was handed rather than the one its
        table holds` (`echoesPrint`), inventory 10 → 11, and
        `TestTheLedgerHarnessStillDetectsEveryDefectItWasBuiltToDetect` reports `repeat: failed` for
        it with the plain ledger `passed` beside it.
  - [x] `TestTheEchoedFingerprintIsCaughtByTheFingerprintAndTheEchoedRowByItsRange` reads the
        *reason*: `echoes` is refused by the range arm and `echoesPrint` by the fingerprint arm, so
        neither answers for the other and the second loser is doing the work.
  - [x] `applicationMutations()` gains `echoes-the-fingerprint-it-was-handed` (5 → 6, the live
        `liveLedger.echoesPrint` flag), and the live run is 6 of 6.
- **Status:** **fixed 2026-09-13.** Reproduced first: a probe ledger echoing the asked fingerprint,
  through `RunLedger` over the reference — all seven sections `passed`, exit 0. After the fix the
  same probe reports `repeat: failed — a claim of that same key made under another operation's
  fingerprint answered the fingerprint sha256:0909… where the row holds sha256:0202…`, in memory and
  again against live PostgreSQL. The probe file was deleted; `echoesPrint` is its permanent
  replacement in the inventory.

---

### GAP-2 [high][immediate] the checkpoint suite's `durability` section asserts nothing that anything depends on

- **Where:** `event/eventtest/sections_checkpoints.go:501-518`, exemption at
  `event/eventtest/checkpoints_test.go:328`.
- **What:** M61 — deleting line 517, the section's **only** assertion
  (`this.unchangedRow(ctx, restarted, name, written, …)`) — leaves `go test -race ./event/...`
  green. It is also the one section of the fifty-two named by no defect row, and the exemption at
  `checkpoints_test.go:328` is what lets `checkpointGuarded` pass over it.
- **Why this severity:** `durability` is the section that says a checkpoint row survives the value
  that wrote it. Gutted, `RunCheckpoints` reports `durability: passed` for a store that persists
  nothing, and a consumer reads a certificate for the one property a restart depends on. The
  existing control, `TestAStoreThatDoesNotPersistDeclinesDurabilityAndCertifiesTheOtherThirteen`,
  asserts the **gate** (`not certified` when persistence is unclaimed) and never the body, so it
  passes over an empty body exactly as the inventory does.
- **Why this timing:** the exemption is written down as "the list can only shrink", which reads as
  a bounded concession. It is not bounded today: nothing fails if the section becomes empty.
- **Close criteria:**
  - [x] A `CheckpointDefect` row names `durability`: `kept`, a decorator that answers only for the
        rows the value that wrote them holds — a store whose map is a field, or a write-back cache
        nothing flushed. The exemption's stated ground (*"no decorator can falsify a property about
        a value built afterwards"*) was wrong, which is why it had to be load-bearing.
  - [x] Neutralising `sections_checkpoints.go:517` turns `./event/eventtest/` red:
        *"the suite no longer detects a checkpoint store that answers only for the rows the value
        that wrote them holds: its durability section was reported "passed""*.
  - [x] `undecorated` is gone, with `sortedNames` and the exemption-staleness loop that served it.
        Inventory 14 → 15; `checkpointGuarded` now requires a defect for every section without an
        escape.
- **Status:** **fixed 2026-09-13.** Reproduced first: line 517 deleted, `go test -race ./event/...`
  green on all five packages, exit 0.

---

### GAP-3 [high][immediate] `claimAnswered`'s three-arm refusal is pinned by one row shape, and the shape that escapes is one this framework produces

- **Where:** `event/receipt/claim.go:266-268`; the only fixture that drives it,
  `event/receipt/claim_test.go:715-720` (`settled`).
- **What:** M43 — widening `if held.Complete || held.First != 0 || held.Last != 0` to `&&` —
  survives on both trees. The only row driven sets all three (`First, Last, Complete = 1, 2, true`),
  so the `&&` mutant still refuses it and the `||` is never exercised as a partition.
- **Why this severity:** the escaping shape is `Complete = true, First = 0, Last = 0`, and this
  package writes exactly that row itself: `Held.Complete` at `claim.go:186-190` sets
  `filled.Complete = true` and leaves the range at zero for an **empty commit** — the documented
  way a decision that yielded no changes is recorded. So a ledger whose `won` comes from a rowcount
  variable while its `RETURNING` picked up a pre-existing row certifies clean whenever the earlier
  operation under that key was an empty commit, and `Claim` then hands a second caller `Recorded`
  over a key that is already spent. That is a duplicate append under one idempotency key — the
  exact failure the closeout names as the top risk.
- **Close criteria:**
  - [x] The table drives three rows, one per arm, built by `wonWith`: `Complete` alone, `First`
        alone, `Last` alone.
  - [x] Each is refused with `ErrLedger` and the three sentences are distinct — **by naming the
        field rather than the row**. Rendering the row itself (`%d..%d complete=%v`) was written
        first and turned `./event` red on `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor`
        (*"receipt/claim.go:268:27 in claimAnswered: a Version is rendered into a refusal"*), which
        is a binding rule and a good one. The one `if` became a three-arm `switch`: already
        complete · a range that carries a first version · one that carries a last version.
  - [x] `||` rewritten as `&&` turns `./event/receipt/` red, and so does deleting any one of the
        three arms on its own — four mutations, four distinct failures, each naming its own row.
- **The representation question, answered rather than left open.** Three zeroes do **not** mean two
  things: `Complete` is the discriminator and [[D-143]] says so in as many words — *"`0..0` is also
  the honest range of a decision that yielded no changes, which is why the discriminator is
  `Receipt.Complete` and never the range"*. A distinguishable range (a NULL pair, an `Empty` flag)
  would add a column to every consumer's table for a state the pair already spells, and
  `receipt.Receipt` is the shape a ledger author maps to columns — so changing it is a contract move
  against a binding decision, for no information gained. **Declined**, and the fix instead makes the
  refusal say which of the three it saw, which is where the ambiguity actually was: the old sentence
  said *"already carries a range"* about a row carrying none.
- **Status:** **fixed 2026-09-13.** Reproduced first: `||` → `&&` at `claim.go:266`,
  `go test -race ./event/...` green on all five packages, exit 0.

---

### GAP-4 [medium][deferred] `Tx.revalidate` reports through a body nothing can reach

- **Where:** `event/eventmemory/transaction.go:166-175`, claim machinery at
  `event/eventmemory/log.go:91-109`.
- **What:** M30 — deleting the whole staleness check — survives on both trees.
- **Why this severity:** reading the claim machinery, it appears genuinely unreachable: a claim
  names its `*Tx` weakly, so the only way an entry dies is the transaction becoming unreachable,
  and an unreachable transaction cannot reach `Commit`. Every other writer is refused at
  `append.go:46`. So this is most likely defensive code with no input, not an untested path — but
  the file does not say so, and an arm nothing can reach reads exactly like an arm nothing tested.
  A later change that makes claims sweepable rather than weak would silently remove the defence.
- **Close criteria:**
  - [x] The second option: the comment now states that nothing reaches `errStaleClaim` today, why
        (publish is the only writer of a stream's history, every other writer of a claimed stream is
        refused at `append.go:46`, a claim dies only with a transaction nothing can reach and one
        nothing can reach never reaches `Commit`), and what would give it an input — a claim that
        can be swept rather than one that dies with its holder.
  - [x] The property it would otherwise be the last line of is already pinned, and the comment names
        it: `TestAStreamIsClaimedOnlyWhileSomethingCanStillCommitIt`, whose first subtest refuses an
        autocommit append into a live transaction's claimed stream and whose second is the control
        that the same claim clears once nothing can reach the transaction.
  - The comment also separates it from its neighbour: `revalidateSaves` **does** have inputs, because
    no claim covers a checkpoint row.
- **Status:** **fixed 2026-09-13.** Not removed: an arm with no input today is a defence against the
  change that gives it one, and the change that would is named.

---

### GAP-5 [medium][immediate] the suite's detection rate is bounded by its inventory size, and the backlog reports the rate without the bound

- **Where:** `event/eventtest/defects*.go` (43 rows) against `event/eventtest/sections_*.go`
  (179 `if … { this.refuse(…) }` sites in the seven `Run` section files),
  `.agents/artifacts/gaps/EVENTSOURCE_BACKLOG.md:41-56`.
- **What:** the suite is falsified by running each inventoried defect and requiring the section that
  names it to fail. At most one assertion per defect can be *the first* to report it, so at most 43
  of 179 assertion sites can ever be killed by that method — a ceiling of ~24%. The backlog reports
  `19/185 = 10.3%` and `27/165 = 16%` against an implied ceiling of 100%, which both overstates the
  deficiency and hides the actionable half of it: **136 assertion sites are, by construction,
  outside the reach of the current falsification method.** My own sample of 14 sites killed 1
  (7%), consistent with that.
- **Why this timing:** the backlog entry stays open until the rate improves. Improving it by adding
  assertions is impossible; the only lever is inventory rows, and the per-section table makes the
  targets obvious: `binding` 2 defects against 11 assertions, `expected version` 1 against 10,
  `stream identity` 2 against 8, `payload ownership` 4 against 8. Three sections
  (`durability`, `monotone visibility`, `store failure classification`) have one defect or none
  against one assertion or none.
- **Close criteria:**
  - [x] `EVENTSOURCE_BACKLOG.md` item 1 now states the ~24% ceiling, reports the rate against what
        the method can reach, says the lever is inventory rows and never assertions, and carries the
        per-section table below. `EVENTSOURCE_CLOSEOUT.md` carries the same correction beside the
        three mutations it ran, and its "what I did not check" bullet no longer leaves the 16%
        unqualified.
  - [ ] The four thin sections — `binding` 2/11, `expected version` 1/10, `stream identity` 2/8,
        `payload ownership` 4/8 — reach one inventory row per two assertion sites, or their residue
        is named as assertions no store can falsify. **Scheduled**, in the backlog entry, which now
        names them as the work it keeps.
- **Status:** **documentation half fixed 2026-09-13; inventory half scheduled** in the backlog. The
  two are separable: the misreported metric was actively misleading and is corrected now; writing
  eight or ten more defect decorators is ordinary work with no gate behind it.

---

### GAP-6 [low][deferred] a deleted locking-read assertion fails by a ten-minute deadlock rather than a message

- **Where:** `event/eventtest/sections_generations.go:169-179`.
- **What:** M53 — deleting the `refuse` at line 175 — is caught, but by hanging: the section's own
  `<-answered` at line 181 has already been drained by the `case err := <-answered` arm, so the
  harness's self-test blocks until `go test`'s package timeout panics. Detection took 10 minutes
  against ~20 seconds for every other mutation in this campaign.
- **Why this severity:** it is caught, so nothing ships wrong. A maintainer who breaks that line
  gets a goroutine dump instead of the sentence the house rule asks for.
- **Close criteria:**
  - [x] The channel is received from in one arm or the other and never in both: the wait's tail —
        the rollback, the second receive and the cutover's answer — moved inside the `time.After`
        arm, so the arm that already received never receives again.
- **Status:** **fixed 2026-09-13.** Reproduced first, on the shape as it stood: the assertion
  deleted, `go test -race -timeout 45s -run TestTheOwnershipHarnessStillDetects…` panicked with
  *"test timed out after 45s … goroutine 1 [chan receive]"* after 46s wall. On the fixed shape the
  same deletion fails in **1.0s** with the sentence the rule asks for: *"the harness no longer
  detects an ownership row that reads the ownership row without taking a lock on it: its locking
  read section was reported "passed""*.

---

## Mutation log

`go test -race -count=1 ./event/...` after each; applied and reverted one at a time; green
confirmed between campaigns. `NOT-APPLIED` means the target text does not exist in that tree.

### Campaign 1 — 53 implementation mutations, current tree and `07b681e`

| # | File · what was broken | `07b681e` | current |
|---|---|---|---|
| M01 | `errors.go` `refusal.Is` — the `!inVocabulary` class gate dropped | killed | killed |
| M02 | `errors.go` `refusal.Is` — the context.Canceled/DeadlineExceeded early false removed | killed | killed |
| M03 | `errors.go` `door.unclassified` — the two doors' fail-safe defaults swapped | killed | killed |
| M04 | `errors.go` `failure.refuse` — the `Conflict` switch arm dropped | killed | killed |
| M05 | `errors.go` `retryable` — `==` flipped to `!=` on the fault kind | killed | killed |
| M06 | `errors.go` `walk` — the hop budget never reports exhaustion | killed | killed |
| M07 | `codec.go` `Encode` — `json.Marshal(&value)` → `json.Marshal(value)` | killed | killed |
| M08 | `codec.go` `canEncodeWith` — the codec's own refusal discarded | killed | killed |
| M09 | `encodable.go` `fields` — the empty-object refusal made unreachable | killed | killed |
| M10 | `encodable.go` `collect` — two fields rendering one JSON name admitted | killed | killed |
| M11 | `encodable.go` `readAsJSON` — the promotion arm inverted | killed | killed |
| M12 | `encodable.go` `visit` — the interface refusal retargeted | killed | killed |
| M13 | `encodable.go` `collect` — the embedded-pointer-to-unexported refusal dropped | killed | killed |
| M14 | `comparison.go` — `disturbedAtItsOwnWidth`, the second disturbance, dropped | n/a | **killed** |
| M15 | `comparison.go` `shares` — the slice/map pointer-identity arm dropped | killed | killed |
| M16 | `comparison.go` `same` — the slice length comparison dropped | killed | killed |
| M17 | `comparison.go` `sameEntries` — a map key compared with itself | killed | killed |
| M18 | `comparison.go` `same` — the invalid-value arm answers agreement | killed | killed |
| M19 | `comparison.go` `sameOpaque` — the type's own `Equal` never asked | killed | killed |
| M20 | `comparison.go` `sameOpaque` — the zero-sized shortcut dropped | killed | killed |
| M21 | `identity.go` `escapePart` — the separator and escape byte no longer escaped | killed | killed |
| M22 | `binding.go` `admitLimits` — a stated bound of zero admitted | killed | killed |
| M23 | `store.go` `Support.stated` — an unstated capability reads as stated | killed | killed |
| M24 | `backing.go` `Backing.Equal` — every backing compares equal | killed | killed |
| M25 | `repo.go` `Append` — the token's backing comparison dropped | killed | killed |
| M26 | `repo.go` `checkPage` — the density check widened from `!=` to `<` | killed | killed |
| M27 | `repo.go` `apply` — a store-supplied type name no longer held to the identifier rule | killed | killed |
| M28 | `eventmemory/transaction.go` `stage` — only the first stream of a unit claimed | **survived** | **killed** |
| M29 | `eventmemory/transaction.go` `release` — only one claimed stream released | **survived** | **killed** |
| M30 | `eventmemory/transaction.go` `revalidate` — the commit-time recheck dropped | **survived** | **survived** |
| M31 | `eventmemory/transaction.go` `WithTransaction` — the binding keyed by no log | killed | killed |
| M32 | `eventmemory/append.go` — the expected-version comparison widened `!=` → `>` | killed | killed |
| M33 | `eventmemory/append.go` — an append into a stream another unit claimed admitted | killed | killed |
| M34 | `eventmemory/read.go` `ReadStream` — the end bound widened `>=` → `>` | **survived** | **survived** (equivalent, below) |
| M35 | `eventmemory/read.go` `ReadAll` — cursor ordering widened `>` → `>=` | killed | killed |
| M36 | `eventmemory/cursor.go` — a cursor minted over another log read as this one's | killed | killed |
| M37 | `projection/wait.go` `poll` — the second ownership read on the reaching poll dropped | killed | killed |
| M38 | `projection/wait.go` `parked` — `Holds` never asked | killed | killed |
| M39 | `projection/wait.go` `poll` — the first ownership read dropped | killed | killed |
| M40 | `receipt/claim.go` `Claim` — the incomplete-row refusal inverted | killed | killed |
| M41 | `receipt/claim.go` `Held.Complete` — the once-only gate dropped | killed | killed |
| M42 | `receipt/claim.go` `sameUnit` — ledger and store transactions no longer compared | killed | killed |
| M43 | `receipt/claim.go` `claimAnswered` — the fresh-row check widened `\|\|` → `&&` | **survived** | **survived** |
| M44 | `receipt/claim.go` `verdictOf` — `Repeated` and `Collided` swapped | killed | killed |
| M45 | `receipt/key.go` — a bracket in an operation key admitted | killed | killed |
| M46 | `receipt/claim.go` `Held.Complete` — another aggregate's commit admitted | killed | killed |
| M47 | `eventtest/suite.go` `report` — the certified-nothing rule dropped | killed | killed |
| M48 | `eventtest/suite.go` `report` — a section that reported no verdict no longer named | killed | killed |
| M49 | `eventtest/ledger.go` `admitLedger` — a factory that begins no unit admitted | n/a | killed |
| M50 | `eventtest/generations.go` `admitGenerations` — an unstated `Closes` admitted | n/a | killed |
| M51 | `eventtest/sections_ledger.go` — the claim-order assertion deleted | n/a | killed |
| M52 | `eventtest/sections_park.go` — the committed-state assertion deleted | n/a | killed |
| M53 | `eventtest/sections_generations.go` — the locking-read assertion deleted | n/a | killed (by timeout — GAP-6) |

Applied at `07b681e`: 47. Killed: **42/47 = 89%**. Same 47 on the current tree: **44/47 = 94%**.
Whole current set: **50/53**.

The two that moved are M28 and M29 — the claim set covering every stream of a unit of work and
releasing every one of them. Those are precisely the two escapes backlog item 2 says were closed by
`TestAClaimCoversEveryStreamOfATransactionAndIsReleasedOnEveryOne`, and that claim is now
independently confirmed: both mutants are green at `07b681e` and red here, in the test written for
them. M14 is the third: the second, wider disturbance the stage added to `reusesItsBuffer`, which
did not exist before and is killed here by
`TestACodecThatDecodesIntoAReusedBufferIsCaught/a_codec_that_fills_its_buffer…`.

### Campaign 2 — 12 further mutations, under-sampled areas, current tree

| # | File · what was broken | verdict |
|---|---|---|
| M54 | `projection/partition.go` `Matches` — the membership comparison flipped | killed |
| M55 | `projection/partition.go` `Split` — the finer mask loses the bit it adds | killed |
| M56 | `projection/router.go` `Apply` — the ignored-type arm inverted | killed |
| M57 | `projection/router.go` `foreignTo` — `\|\|` widened to `&&` | killed |
| M58 | `projection/effect.go` — the effect gate's generation comparison flipped | killed |
| M59 | `receipt/resolve.go` — the horizon comparison flipped for an absent row | killed |
| M60 | `receipt/resolve.go` — the horizon-against-row comparison reversed | killed |
| M61 | `eventtest/sections_checkpoints.go` — the `durability` section asserts nothing | **survived** — GAP-2 |
| M62 | `eventtest/sections_transactions.go` — the section returns before asserting | killed |
| M63 | `eventtest/sections_write.go` — the expected-version section returns before asserting | killed |
| M64 | `eventtest/sections_read.go` — the global-paging section returns before asserting | killed |
| M65 | `eventtest/sections_lifecycle.go` — the cancellation section returns before asserting | killed |
| M66 | `eventmemory/checkpoints.go` — target text absent | NOT-APPLIED |

**11 / 12 applied.** Combined with campaign 1: **61 killed / 65 applied = 94%.**

### M34 is an equivalent mutant and is not counted as a gap

`ReadStream`'s `if after >= event.Version(length)` widened to `>`. For `after > length` the mutant
still returns early. For `after == length` it falls through to `first = length`,
`count = min(length-first, StreamPage) = 0`, and answers an empty page — the same answer as the
guard, differing only in `nil` versus a zero-length slice, which no caller in the tree distinguishes.
Honest reading: 61/64 viable = **95%**, or 61/65 as run.

### Campaign 3 — assertion neutralisation, the backlog's own metric, sampled

Method: the backlog neutralises one `refuse` call site at a time by deleting its own `if` block. I
did the identical thing by making the condition `false && (…)`, over a deterministic every-seventh
spread of the 100 sites in the seven `Run` section files that carry no short variable declaration
(179 sites in total; the 79 excluded carry a `:=` whose variable would then be unused).

| # | Site | Condition | Verdict |
|---|---|---|---|
| A01 | `sections_write.go:32` | `err != nil` | survived |
| A02 | `sections_write.go:142` | `room < 1` | survived |
| A03 | `sections_write.go:253` | `!envelope.RecordedAt.Equal(page[index].RecordedAt)` | **killed** |
| A04 | `sections_read.go:56` | `len(seen) < 2` | survived |
| A05 | `sections_read.go:142` | `!sameLedger(state, whole) \|\| reached.Version() != at.Version()` | survived |
| A06 | `sections_read.go:261` | `reached.Version() != event.Version(limits.MaxBatch)+1` | survived |
| A07 | `sections_lifecycle.go:109` | `beside == nil` | survived |
| A08 | `sections_lifecycle.go:325` | `!errors.Is(err, one.is)` | survived |
| A09 | `sections_ownership.go:73` | `!samePage(first, kept)` | survived |
| A10 | `sections_ownership.go:150` | `!sameLedger(before, beside)` | survived |
| A11 | `sections_resumption.go:18` | `err != nil` | survived |
| A12 | `sections_resumption.go:138` | `err != nil` | survived |
| A13 | `sections_transactions.go:79` | `!sibling.Backing().Equal(store.Backing())` | survived |
| A14 | `sections_transactions.go:133` | `!first.Authority().Same(second.Authority()) \|\| …` | survived |

**1 / 14 = 7%**, against the ~24% ceiling GAP-5 derives. The previous stage reported 10.3% by the
same method over the whole population; my sample is consistent with it and I do not claim it
refutes it — fourteen draws cannot.

### Per-section inventory pressure, computed from the source

| Section | Defect rows | `refuse` sites in its own function |
|---|---|---|
| binding | 2 | 11 |
| stream identity | 2 | 8 |
| expected version | 1 | 10 |
| dense versions | 1 | 4 |
| global order | 3 | 2 |
| conservation | 2 | 4 |
| stream paging | 4 | 2 |
| global paging | 1 | 4 |
| resumption | 3 | 6 |
| bounds | 2 | 6 |
| payload ownership | 4 | 8 |
| refusal classes | 1 | 5 |
| cancellation | 3 | 6 |
| lifecycle | 2 | 7 |
| concurrency | 2 | 4 |
| transactions | 6 | 2 |
| durability | 1 | 1 |
| shared backing | 1 | 4 |
| monotone visibility | 1 | 1 |
| store failure classification | 1 | 0 |
| **total** | **43** | **95** (plus 84 in helpers) |

---

## Sections named by no defect row

Computed from the five inventories and the five defect lists in `event/eventtest/`:

| Suite | Sections | Defect rows | Named by none |
|---|---|---|---|
| `Run` | 20 | 43 | 0 |
| `RunCheckpoints` | 14 | 14 | **1** — `durability` |
| `RunLedger` | 7 | 10 | 0 |
| `RunGenerations` | 5 | 6 | 0 |
| `RunPark` | 6 | 8 | 0 |
| **total** | **52** | **81** | **1** |

The backlog's "nine of twenty" is stale for the `Run` suite: it is now zero of twenty, and
`TestEverySectionIsNamedByADefectThatBreaksIt` computes it with its own control. The single
remainder is the checkpoint suite's `durability`, explicitly exempted at `checkpoints_test.go:328`
— and M61 shows the exemption is load-bearing, so it is a real hole, not a bookkeeping one (GAP-2).

**Round 2:** that remainder is closed. The checkpoint inventory is 15 rows over 14 sections, the
ledger's is 11 over 7, and no section of the fifty-two is named by no defect row.

---

## Consumer-harness falsification — three of three fail, naming the obligation

Three defective implementations of my own, in `event/eventtest`'s external test package, driven
through the published entry points and deleted afterwards. Each failed exactly one section.

| Implementation | Entry point | Result |
|---|---|---|
| A ledger whose `Claim` does `Find` first and reports `won` from that read | `RunLedger` | **FAIL** — `claim order`: *"a second claim of the key another unit had already taken reported once the unit that took it committed that it took the key too, so both callers append under one operation key and this ledger reads the row before it inserts it"* |
| A `Generations` written from scratch whose `Activate` is fenced and whose `Active` takes no lock | `RunGenerations` | **FAIL** — `locking read`: *"a cutover moved the ownership row … while the unit that had read it was still open, so this read takes no lock: the retiring generation commits the effect it staged under a row that no longer names it"* |
| A `Park` written from scratch whose `Holds` answers from a snapshot taken at construction when asked outside a unit | `RunPark` | **FAIL** — `committed state`: *"a sequence parked and committed is not held when the question is asked outside a unit of work, so a wait polls a queue it cannot see and answers a deadline for a change that is parked"* |

Controls, all green:

- the same queue with the stale snapshot turned off — `RunPark` passes, so the failure above is the
  defect's and not the fixture's;
- the repository's own three reference implementations — `RunLedger`, `RunGenerations`, `RunPark`
  all pass in memory;
- the live PostgreSQL implementations —
  `TestTheLiveLedgerSatisfiesTheContract`, `TestTheLiveOwnershipRowSatisfiesTheContract`,
  `TestTheLiveQueueSatisfiesTheContract` all pass, every subsection;
- the repository's own live falsification —
  `TestTheApplicationHarnessesCatchADefectiveImplementation` passes all five subprocess defects
  (`select-before-insert`, `horizon-from-the-newest-row`, `find-on-the-claiming-connection`,
  `plain-ownership-read`, `queue-on-the-pool`).

One further probe, and it is GAP-1: a ledger answering the loser **no row at all** is caught
(`repeat` and `claim order`, by `sameRow`'s key arm); a ledger answering the loser the **fingerprint
it was asked with** is certified by all seven sections.

---

## Gate runs

| Command | Result |
|---|---|
| `make check` | **GREEN, 13 arms.** `CHECK EXIT=0`; last arms `check-event-kernel: ok`, `check-event-combinations: ok, 10 names refused across 1704 files and 37 modules`, `check-event-consumer: ok` (root-only, event-only and composed graphs each linked and reported) |
| `make vet` | **GREEN**, every workspace module plus `./_examples`. `VET EXIT=0` |
| `gofmt -l .` | **silent**, 0 lines |
| `make examples` | **GREEN**, `EXAMPLES EXIT=0` |
| `go test -race -count=1 ./event/...` | **GREEN** — `event 7.5s · eventmemory 1.5s · eventtest 13.4s · projection 6.1s · receipt 1.0s`, 14 s wall |
| live tagged suite ×2 | `ok … 134.682s` then `ok … 139.869s`, `-race -count=1 -tags=integration`, both exit 0 |

### The three foreign reds — named, reproduced at `07b681e`, none mine

| Arm | Failure | Foreign? |
|---|---|---|
| `./scripts` `TestNoI18nPackageCostsMoreThanItsErrorSeam` | `i18n_test.go:31: the i18n extension reaches github.com/go-json-experiment/json/jsontext outside its error seam and declared MessageFormat/CLDR ecosystem` | **Yes** — reproduced on the pristine `07b681e` clone with both stages discarded |
| `./i18n/cmd/vv-i18n` | 36 `--- FAIL` lines (parents and subtests). `./i18n` itself is green | **Yes** — the owner's i18n work. The brief's figure of 29 counts only leaf failures |
| `./test/auditflow` `TestGeneratedAuditProfilesSeparateServingRuntimeFromDeploymentAuthority` | generated serving health check not ready | **Yes** — reproduced on the pristine clone |

`make unit` is **not green** and is not reported as such. Nothing under `event/` is red.

---

## Tree restoration

- 65 implementation mutations and 14 assertion neutralisations applied and reverted, each by
  writing the original bytes back immediately after its run.
- Five test files added under `event/eventtest/` for the harness falsification and removed.
- Whole-tree `sha256sum` manifest over 2366 files taken before the first mutation and after the
  last, and the two are identical:
  `a6b50f40c2f708a97981ac3e0373582090e2c24b7c475ae6f03385d92113c2a1`.
- `git status --porcelain` identical to the starting 65 lines.
- The pre-stage comparison ran on a clone at `/tmp/before_tree`, since deleted; the working tree was
  never checked out or stashed.

---

## Round 3 — independent verification of the round-2 fixes — 2026-09-13

Nothing here was fixed and nothing was read for credit. Every claim below is a command I ran. The
tree was hashed before the first mutation and after the last: **2367 files,
`sha256(manifest) = db9eef13255b268f88b8baab7bdd72ea5f90a1645dc107ad847c2b7fa39e73cb`, identical**,
`git status --porcelain` unchanged at 26 lines, three probe packages created and deleted.

### Verdict

| Finding | Round-2 status | My verdict |
|---|---|---|
| GAP-1 `[critical]` the ledger harness certifies an echoing ledger | fixed | **CLOSED** — proved with a `Ledger` of my own, written from nothing |
| GAP-2 `[high]` the durability body can be emptied green | fixed | **CLOSED** — body emptied, tree red; exemption gone, guard names durability |
| GAP-3 `[high]` `claimAnswered` widens on a shape this package writes | fixed | **CLOSED** — four mutations, four distinct deaths; the escaping shape driven end to end |
| GAP-4 `[medium][deferred]` `Tx.revalidate` unreachable | fixed (documented) | accepted — the comment names the input that would give it one |
| GAP-5 `[medium][immediate]` the metric's ceiling | doc half fixed | accepted — ceiling and per-section table in both documents; residue scheduled |
| GAP-6 `[low][deferred]` detection by deadlock | fixed | **CLOSED** — 1.0 s with a sentence, measured |
| — | — | **NEW GAP-7 `[medium][deferred]`** — see below |

Detection rate has not fallen: **12 of 12 viable mutations of my own killed**, two further mutations
rejected as non-viable because they do not compile.

---

### GAP-1 — closed. Proved with a ledger of my own, not the fix round's fixture.

I wrote a complete `receipt.Ledger` from nothing in an external package
(`event/eventtest/gap1probe`, since deleted): its own table, its own per-key latch standing in for
the primary-key index, its own unit of work, `Find` and `Horizon` on no unit at all — the insert
first and the read after it, in one unit, the way the contract describes. One flag, `echo`, is the
one thing written wrong: when the claim loses, `found.Fingerprint = row.Fingerprint`. It shares no
line with `echoesPrint` and is not a decorator over anything.

- `RunLedger` over it: **`repeat: failed`** — *"a claim of that same key made under another
  operation's fingerprint answered the fingerprint sha256:0909… where the row holds sha256:0202…"*.
  The other six sections pass, so the refusal is the defect's and not the fixture's.
- The **control**, the identical ledger with `echo` off: all seven sections `passed`, exit 0.
- The **reproduction**, so the fix is what catches it: with only
  `event/eventtest/sections_ledger.go` reverted to `HEAD` (the pre-fix partition, one loser) and
  everything else as it stands, the same echoing ledger is certified — `ok`, exit 0. Restored
  byte-identically (`b1462754…`, the kernel baseline).
- The repository's own reference: `TestTheReferenceLedgerIsCertified` — seven of seven `passed`.
- **Live PostgreSQL 17.9**: `TestTheLiveLedgerSatisfiesTheContract` — seven of seven `passed`;
  `TestTheApplicationHarnessesCatchADefectiveImplementation` — six of six, the new
  `echoes-the-fingerprint-it-was-handed` among them, on the `repeat` section, 2.15 s.

**The consequence, checked at `event/receipt/claim.go` rather than reasoned about.** A second probe
package (`event/receipt/gap1collision`, since deleted) wired `eventmemory` plus a ledger of my own
and drove `receipt.Once`:

- a key spent on one operation, claimed again by a different one → `errors.Is(err, ErrCollision)`
  true, the stream unmoved. **`ErrCollision` is reachable and returned.**
- controls: the identical operation retried under its own key → `Repeated`, nothing appended; a
  fresh key → appends. So the refusal is about the key and not about everything.
- the mirror, under the echoing ledger: the same collision answers **nil**, the append is
  suppressed, and `Resolve` reports the first operation's range to the second caller. The
  consequence GAP-1 named is real, and the `repeat` section is the only thing standing in front
  of it.

### GAP-2 — closed. The body was emptied and the tree went red.

`event/eventtest/sections_checkpoints.go:517` deleted (the section's only assertion):
`go test -race ./event/...` → **`--- FAIL: TestEveryCheckpointDefectIsReportedByItsOwnSection`**,
*"the suite no longer detects a checkpoint store that answers only for the rows the value that wrote
them holds: its durability section was reported "passed""*. Restored byte-identically
(`d7df40ef…`), suite green.

The exemption is gone rather than moved: `undecorated`, `sortedNames` and the staleness loop are
deleted, and `checkpointGuarded` has no escape. Removing the `kept` inventory row alone turns
`./event/eventtest/` red twice — *"the checkpoint suite carries 14 defects where its inventory is 15
rows"* and *"no defect in this suite's inventory breaks the durability section"*. And the section
still asserts through the **second** value: rewriting `unchangedRow(ctx, restarted, …)` to
`unchangedRow(ctx, held, …)` is killed. The old exemption's stated ground — that no decorator can
falsify a property about a value built afterwards — was simply wrong, and the fix says so.

### GAP-3 — closed. Four mutations, four deaths, and the escaping shape driven.

`claimAnswered`'s three arms are now a `switch`. The equivalent of M43 — collapsing it back to
`if held.Complete && held.First != 0 && held.Last != 0` — is **killed**:
*"a ledger that reports a win beside a row an empty commit already completed answered <nil>"*.
Deleting each arm on its own is killed too, each with its own sentence: `already complete`,
`a first version`, `a last version`. Four for four, restored byte-identically each time
(`4491c93d…`).

**Driven end to end** in a third probe package (`event/receipt/gap3empty`, since deleted):

- an operation that yields no changes leaves a row with `Complete = true, First = 0, Last = 0` —
  asserted on the ledger's own row, so the premise is measured and not assumed;
- a **retry** over that key against a conformant ledger → `Repeated`, nothing appended;
- the same retry against a ledger whose `won` is a rowcount while its read picked up the row already
  there → refused with `ErrLedger`, verdict **not** `Recorded`, the stream unmoved;
- the **control**: the same defect over a key whose operation wrote a range, refused by a different
  arm. Under the `&&` mutant the control still passes while the empty-commit case fails — which is
  exactly the asymmetry GAP-3 named.

Under the `&&` mutant my own probe reports *"a retry over a key an empty commit had already spent
was handed Recorded, so the work ran a second time under one operation key"*.

**One correction to my own method, recorded because it nearly produced a false green.** My first
version of that probe had the second caller submit a *different* operation. It passed under the
`&&` mutant — not because the check held, but because the **fingerprint** arm fired first. A probe
that passes for the wrong reason is the same liability as a test that does. The published shape
above claims with the fingerprint the row holds, so the completion arm is the only thing left.

The representation question is answered, not deferred, and the answer is grounded: `D-143` line 72
does say *"`0..0` is also the honest range of a decision that yielded no changes, which is why the
discriminator is `Receipt.Complete`"*. Declining a contract move against a binding decision for no
information gained is right.

### GAP-6 — closed, and I timed it.

Deleting the locking-read `refuse` fails in **1.0 s** (2.7 s wall for the whole `-run`) with
*"the harness no longer detects an ownership row that reads the ownership row without taking a lock
on it: its locking read section was reported "passed""*, against the ten-minute package timeout
round 1 measured. The `select`'s tail assertions still run on the passing arm — the section did not
become weaker by moving them.

---

### NEW — GAP-7 `[medium][deferred]` two of `sameRow`'s five arms are reached by no inventory row

- **Where:** `event/eventtest/ledger.go:204-217` (`receipts.sameRow`), arms `found.Key != want.Key`
  and `found.Complete != want.Complete`.
- **What:** neutralising either arm (`case false && (…)`) leaves `go test -race ./event/eventtest/`
  green. The fingerprint arm and the range arm are both killed, by
  `TestTheEchoedFingerprintIsCaughtByTheFingerprintAndTheEchoedRowByItsRange` — so the partition the
  fix installed is real and load-bearing; these two are its uncovered neighbours.
- **Not a regression, measured:** both survive on the pre-fix tree too (`git stash -u`, same two
  neutralisations, both green, then `stash pop`). Adding an inventory row and a second loser can
  only have increased detection.
- **Why it is worth a row anyway:** round 1 reported that a ledger answering a loser **no row at
  all** is caught by `sameRow`'s key arm. That is still true of the shipped code and is exactly the
  single-statement claim `D-142` warns about — but nothing in the inventory drives it, so the arm
  that would report it can be deleted unnoticed. `sameRow` is a helper, and the per-section table
  GAP-5 recorded counts only the seven `Run` section files: the four application harnesses'
  **helpers** are outside every measurement taken so far.
- **Close criteria:**
  - [ ] `ledgerDefects()` carries a row for a ledger that answers a loser no row at all (a
        single-statement claim, one snapshot) and one that answers a loser a row for another key,
        both on the `repeat` or `claim order` section.
  - [ ] Each of `sameRow`'s five arms dies on its own neutralisation, read by a test that names
        which arm reported — the shape `TestTheEchoedFingerprintIsCaughtBy…` already has.
  - [ ] The per-section pressure table is extended to the four application harnesses, helpers
        included, so `RunLedger`'s residue is visible the way `Run`'s now is.

---

### Mutation log — round 3

`go test -race -count=1` over `./event/ ./event/eventmemory/ ./event/eventtest/ ./event/projection/
./event/receipt/`, applied and reverted one at a time, every file restored by writing the original
bytes back and every hash checked against `scripts/event_kernel.sha256`.

**The three closures, re-driven.**

| # | What was broken | Verdict |
|---|---|---|
| R01 | `sections_ledger.go` reverted to `HEAD` (pre-fix repeat section), my echoing ledger through `RunLedger` | **certified — exit 0.** The reproduction; the fix is what catches it |
| R02 | the fixed tree, my echoing ledger through `RunLedger` | **`repeat: failed`**, naming the fingerprint it answered and the one the row holds |
| R03 | the same ledger with the echo off | all seven `passed` — the control |
| R04 | `sections_checkpoints.go:517` deleted (durability's only assertion) | **killed** — `TestEveryCheckpointDefectIsReportedByItsOwnSection` |
| R05 | the `kept` durability row removed from `checkpointDefects()` | **killed**, twice: the inventory size and the per-section guard |
| R06 | `claim.go` `claimAnswered`: the three arms collapsed to one `&&` | **killed** — the empty-commit row |
| R07 | `claim.go`: the `held.Complete` arm deleted | **killed** — *"already complete"* |
| R08 | `claim.go`: the `held.First != 0` arm deleted | **killed** — *"a first version"* |
| R09 | `claim.go`: the `held.Last != 0` arm deleted | **killed** — *"a last version"* |
| R10 | `sections_generations.go`: the locking-read `refuse` deleted | **killed in 1.0 s** with a sentence — GAP-6 |

**Twelve of my own, across `event/receipt` and `event/eventtest`.**

| # | File · what was broken | Verdict |
|---|---|---|
| N01 | `receipt/claim.go` `verdictOf` — the fingerprint comparison negated | killed — `TestACompletionThatIsNotTheClaimsIsRefusedFiveWays` |
| N02 | `receipt/claim.go` `claimAnswered` — the no-row-at-all refusal dropped | killed — `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` |
| N03 | `receipt/claim.go` `Claim` — the incomplete-row refusal never fires | killed — `TestAClaimAnAppendAndACompletionAreOneTransaction` |
| N04 | `receipt/claim.go` `Held.Complete` — the row is completed as not complete | killed — `TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt` |
| N05 | `receipt/claim.go` `sameUnit` — ledger and store transactions no longer compared | killed — `TestAClaimIsRefusedUnlessTheLedgerAndTheStoreAreOneTransaction` |
| N06 | `receipt/claim.go` `Held.Complete` — the empty-commit guard on the range dropped | killed — `TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt` |
| N07 | `eventtest/sections_ledger.go` `lost` — the `sameRow` call deleted | **NOT VIABLE** — `found` declared and not used; it does not compile |
| N07b | `eventtest/sections_ledger.go` `lost` — the row compared with itself | killed — both `TestTheLedgerHarnessStillDetects…` and `TestTheEchoedFingerprint…` |
| N08 | `eventtest/sections_ledger.go` — the second loser claims with the row's own fingerprint (the pre-fix partition) | killed — `TestTheLedgerHarnessStillDetectsEveryDefectItWasBuiltToDetect` |
| N09 | `eventtest/defects_ledger.go` `echoesPrint` — the defect no longer echoes | killed — same |
| N10 | `eventtest/sections_checkpoints.go` `durability` — read back through the value that wrote | killed — `TestEveryCheckpointDefectIsReportedByItsOwnSection` |
| N11 | `eventtest/ledger.go` `sameRow` — the fingerprint arm neutralised | killed — `TestTheEchoedFingerprint…` |
| N12 | `eventtest/defects_checkpoints.go` `kept.Load` — the row-hiding arm deleted | **NOT VIABLE** — `written` declared and not used |
| N13 | `eventtest/ledger.go` `sameRow` — the range arm neutralised | killed — `TestTheEchoedFingerprint…` |
| N14 | `eventtest/ledger.go` `sameRow` — the key arm neutralised | **SURVIVED** — GAP-7, pre-existing |
| N15 | `eventtest/ledger.go` `sameRow` — the completion arm neutralised | **SURVIVED** — GAP-7, pre-existing |

**12 viable of 14 attempted excluding the two GAP-7 arms; 12 killed = 100%.** Counting N14 and N15,
**12 / 14 = 86%** over a set deliberately weighted toward the two files the fix round touched. The
two non-viable ones are worth their own sentence: an assertion in `lost` and the hiding arm in
`kept` cannot be *deleted* at all, because Go refuses the unused variable that is left. That is a
real property of those two sites and not a measurement failure.

**Read as a diff, looking for what a fix introduces.** Nothing widened: `switch {case A; case B;
case C}` refuses exactly the set `A || B || C` did. Nothing weakened: `checkpointGuarded` lost an
escape rather than gaining one, and the staleness loop it dropped only guarded a list that no
longer exists. No control asserts less: `ledgerRepeatSection` went from one loser to two with the
same assertion on both, and `lockingReadSection` moved its tail into the arm that had not already
drained the channel, so both assertions still run on the passing path. No section passes for a new
wrong reason — N10 and N08 are the two mutations that would have shown one, and both die.

---

### Gate runs — round 3

| Command | Result |
|---|---|
| `make check` | **GREEN, 13 arms**, `CHECK EXIT=0`; `check-event-kernel: ok`, `check-event-combinations: ok, 10 names refused across 1704 files and 37 modules`, `check-event-consumer: ok` |
| `make vet` | **GREEN**, `VET EXIT=0`, every workspace module plus `./_examples` |
| `make examples` | **GREEN**, `EXAMPLES EXIT=0` |
| `gofmt -l .` | **silent** |
| `go test -race -count=1 ./event/...` | **GREEN** — `event 7.1s · eventmemory 1.5s · eventtest 13.4s · projection 6.1s · receipt 1.0s`, 14 s wall |
| live tagged suite ×2 | `ok … 143.268s` then `ok … 143.413s`, `-race -count=1 -tags=integration ./...` inside `event/eventpg`, both exit 0 |
| live tagged suite, DSN unset | **FAILS rather than skips**, and prints the command to run — `FROSTGROVE_EVENTPG_TEST_DSN is not set, and this suite proves nothing without a database` |

`go test -race ./event/...` does **not** reach `event/eventpg`: it is its own module, so the live
suite is run from inside it. Round 2's figure of 134–140 s was over the same package.

### The three foreign reds — reproduced with the whole fix round stashed

`git stash push -u` (0 dirty lines), all three re-run, `git stash pop` (26 dirty lines back), tree
manifest identical afterwards:

| Arm | With the fix round stashed | Verdict |
|---|---|---|
| `./scripts` `TestNoI18nPackageCostsMoreThanItsErrorSeam` | `FAIL` | **foreign** |
| `./i18n/cmd/vv-i18n` | `FAIL`, 36 `--- FAIL` lines (`./i18n` itself green) | **foreign** |
| `./test/auditflow` | `FAIL` | **foreign** |

No file the fix round touched is under `i18n/`, `test/` or `scripts/i18n*`. `make unit` is **not**
green and is not reported as such. Nothing under `event/` is red.

### Tree restoration — round 3

- 26 mutations applied and reverted one at a time, each by writing the original bytes back and
  confirming the file's sha256 against `scripts/event_kernel.sha256`.
- Three probe packages created under `event/eventtest/gap1probe`, `event/receipt/gap1collision` and
  `event/receipt/gap3empty`, and removed.
- Two `git stash push -u` / `stash pop` cycles, with the manifest re-verified after each.
- Whole-tree `sha256sum` manifest over **2367** files before the first mutation and after the last:
  identical, `db9eef13255b268f88b8baab7bdd72ea5f90a1645dc107ad847c2b7fa39e73cb`.
- `git status --porcelain` unchanged at 26 lines.
