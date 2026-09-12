# EVENTSOURCE — detection measurement — GAPS

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
| Sections named by no defect row | **1 of 52** (`RunCheckpoints`/`durability`) |
| Sections whose body can be emptied with the tree green | **1 of 52** (the same one) |
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
  - [ ] `ledgerRepeatSection`, or a new `collision` section, claims one key with two fingerprints
        that differ and refuses a ledger whose loser answers the asked fingerprint.
  - [ ] A defect row `answers the fingerprint it was handed rather than the one its table holds`
        exists in `ledgerDefects()` and `TestTheLedgerHarnessStillDetectsEveryDefectItWasBuiltToDetect`
        reports its section `failed`, with the plain row `passed` beside it.
  - [ ] The row's control proves the new section fails for the new defect and not for `echoes`.
  - [ ] `event/eventpg`'s `applicationMutations()` gains the live spelling and stays at 6 of 6.
- **Status:** open

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
  - [ ] Either a `CheckpointDefect` row names `durability` (a factory whose `Sibling` builds a value
        with none of what the first wrote, as `defects.go:112` already spells for the store suite),
        or a named control asserts the section `failed` for such a factory.
  - [ ] Neutralising `sections_checkpoints.go:517` turns `./event/eventtest/` red.
  - [ ] `undecorated` in `checkpoints_test.go` is empty, or its remaining member carries a control
        that fails when the section's body is emptied.
- **Status:** open

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
  - [ ] The `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` table drives three rows, one
        per arm: `Complete` alone, `First` alone, `Last` alone.
  - [ ] Each is refused with `ErrLedger` and the three refusals are distinct sentences (the table's
        existing `said` rule already enforces that).
  - [ ] Rewriting `||` as `&&` turns `./event/receipt/` red.
- **Status:** open

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
  - [ ] Either a test drives a staged version that moved between `stage` and `Commit` and asserts
        `errStaleClaim`, or the comment at `transaction.go:163-165` states that the check has no
        reachable input today and names what would give it one.
  - [ ] If the second: a test asserts the property the check would otherwise hold — an autocommit
        append into a claimed stream is refused — so the defence's *purpose* stays pinned.
- **Status:** open

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
  - [ ] The backlog entry states the ceiling and reports `killed / inventoried-reachable` rather
        than `killed / all assertions`.
  - [ ] The four sections above reach at least one inventory row per two assertion sites, or the
        residue is named as assertions no store can falsify (a factory contract, not a store one).
- **Status:** open

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
  - [ ] The section reads `answered` once into a variable and branches on it, so the deleted
        assertion leaves a clean failure or a clean pass rather than a second receive.
- **Status:** open

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
