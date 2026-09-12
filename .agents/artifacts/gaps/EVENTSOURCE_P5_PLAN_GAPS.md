# EVENTSOURCE P5 — PLAN — GAPS

**Round 1 — 2026-09-12. Verdict: red.** One `[critical]` and five `[high]`. The `[critical]` is a
statement the specification publishes in full and the plan adopts as *"the reference
implementation"*; it was executed against PostgreSQL 17.9 in this audit and does not do what both
documents say it does, at any of the three isolation levels [[D-126]] names. Eleven `[medium]` and
`[low]` findings go to [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P5` as items 12–22 and
are **left alone**.

## Round 1 — plan auditor — 2026-09-12

Audited [`EVENTSOURCE_P5_PLAN.md`](../plans/EVENTSOURCE_P5_PLAN.md) against
[`EVENTSOURCE_P5_USECASES.md`](../usecases/EVENTSOURCE_P5_USECASES.md) (**[SPEC]**),
[`EVENTSOURCE_P5_STUDY.md`](../usecases/EVENTSOURCE_P5_STUDY.md),
[`EVENTSOURCE_REFERENCE.md`](EVENTSOURCE_REFERENCE.md), the four appendices at
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md:783-822` read in the original Russian,
and the tree as it stands at `1fd671b`. Every claim below was re-derived from the source — the
shell arms were run, the counts were counted, and the one SQL claim that decides ES-07 was executed
against the live database rather than reasoned about.

### What was verified and is sound

The findings are read against a plan that is unusually well grounded. All of this was checked and
holds:

- **The coverage matrix is arithmetically complete and its own rule holds.** All 48 use cases
  (`UC-204`…`UC-251`) and all 19 invariants (`INV-108`…`INV-126`) appear exactly once; no
  identifier appears that [SPEC] does not define. The mechanical extraction the plan demands was
  re-run: every one of the 73 `Test…` names in the matrix is inside a counted `-list` pattern, the
  eight names counted but not in the matrix are the pre-existing repository arms the plan says they
  are, and every `-list` pattern is character-identical to the `-run` pattern beside it. Each
  asserted arity (10, 15, 14, 10, 15, 7, 7, 8) matches its pattern's member count.
- **The kernel-fence arithmetic is right.** `scripts/event_kernel.sha256` holds exactly 152 lines;
  `event_kernel_manifest` (`scripts/checks.sh:529-533`) is `find event -type f -not -path
  'event/eventpg/*'`, so `event/eventpg` really is excluded by construction and S4/S5/S6's inverted
  `diff` fence is the right instrument rather than a relaxed one. `event_kernel_moved` does fail on
  an empty moved set (`:629-632`) and does anchor with `[[ =~ ]]`, so the plan's anchored EREs bind.
- **The `make api` diff arm is exact.** `docs/api/surface.md` renders one line per exported var
  (`var ErrBackend error`, …) and one per method, and the `awk` range from
  `^## github.com/frostgrove/vv/event$` to the first `^## github.com/frostgrove/vv/event/` extracts
  232 lines ending at `event/eventmemory`. `Repo.Digest`, `Repo.StateAt` and `ErrVersion` are
  therefore exactly three added lines and zero removed, as claimed.
- **P-1 is correct and is the plan's best correction.** `withDefaults` (`event/projection/spec.go:349`)
  really does begin `if spec.OnPermanentFailure != ParkSequence { spec.Park = nil }`, and a `WaitOf`
  that copied `spec.Park` verbatim would hand `Wait` a queue no pass writes to. Deriving through
  `withDefaults` is the right fix.
- **P-2, P-3, P-4 and P-6 are real.** `costOverruns` (`scripts/extensions_test.go:43-47`) does fail
  twice for an uncharged package (`len(packages) != len(cost.charged)+1`, then `cost.uncharged`);
  `checkedEventPackages` (`scripts/projection_test.go:549-563`) does read its list out of
  `docs/api/surface.md` with a `listed < 5` floor, so `make api` before `./scripts/` is a real
  ordering constraint; `eventpg`'s `position` is `identity: generation "always", increment 1`
  (`event/eventpg/schema.go:253`) and `eventmemory`'s `publish` increments before it assigns
  (`event/eventmemory/log.go:113-116`), and `event/eventtest` asks for neither.
- **P-7's reading of `surveyed` is correct.** `surveyed` (`event/projection/generation.go:158-194`)
  calls `neverHandedDown` for every fresh member, which is a second `event.Track` plus a second
  `Load` against the retirement name (`:205-215`), so the two-reads-per-fresh-member cost is real
  and `1 + (1 or 2) × |cover|` is the honest number. The `20 × (1 + 8) = 180` arithmetic checks out.
- **P-12 is checked rather than asserted.** `Append`'s six steps
  (`event/repo.go:44-68`, the comment; `:68-104`, the body) reach the store first at step 5
  (`this.store.Backing()`), so `Digest` running steps 1–4 issues no store call. `records`
  (`:175-192`) and `apply` (`:260-273`) really are where the encoded batch and the envelope→fold map
  live, and neither is reachable from outside `event`.
- **S6's flow-index loop is an executable obligation and its stated HEAD state is exact.** Run
  verbatim over the tree, it reports exactly one failure — `FL-036 does not name
  event/eventtest/sections_topology.go` — which is what the plan says it inherits, no more and no
  less.
- **The numbering is free and the greps bind.** `FL-039`–`FL-042` are taken and `FL-043` is free;
  `D-141` is the highest decision and `D-142`–`D-145` are free; `UC-035` is the highest repo use
  case and `UC-036`–`UC-038` are free; `docs/modules/{en,ru}/Index.md` rows really are
  `| [event](event.md) | …`, so `grep -c '^| \[receipt\](receipt.md)'` binds; the roadmap really
  carries three `### Приложение ES-0(5|7|8) ` headings and one `ES-09` one today, so both count arms
  are meaningful.
- **The eight existing projection sentinels and their `"Eight, and none of them crosses a store
  seam"` opening are where the plan says** (`event/projection/errors.go:5,38-45`), and
  `checkUnit`'s `authority.Same(mine)` comparison is at `event/projection/pass.go:932-957`.
- **The `[[D-132]]` handling is honest.** The gate is refused on `D-132:49`'s *"a measured **need**,
  not a measured **cost**. There is no consumer"* — a policy, correctly quoted — rather than on the
  benchmark, and `TestNoSnapshotAuthorityIsDeclaredOrPromised` is kept green and un-narrowed. Route
  (c) is what [APX] part 4 asks for.
- **The backlog is accurately described.** `## P5` holds exactly items 1–11, nine `[medium]` and two
  `[low]`, matching the plan's Debt table row for row.

---

### GAP-1 [critical][immediate] The published one-statement claim cannot answer `Repeated` at READ COMMITTED and aborts the loser at the other two levels — executed, not reasoned

- **Where:** [SPEC] §1.2 (`EVENTSOURCE_P5_USECASES.md:934-968`, the SQL block), [SPEC] §3 §UC-217
  (`:1982-1994`), [SPEC] §5.3's `Ledger` doc comment (`:3058-3066`), [SPEC] §6 item 12
  (`:3321`); PLAN § *The conformance extension* (the `two-statement claim` defect row), § *Sections*
  S5 `TestTwoCallersRaceOneKey`, **P-10** (*"the reference implementation"*), and D-142's planned
  clause *"the primary-key index as the serialisation point and therefore no isolation level chosen
  ([[D-126]])"*.
- **What:** [SPEC] publishes the claim statement in full and the plan adopts it as the reference
  implementation and the byte-for-byte baseline for `TestTheExampleLedgerIsTheOneTheLiveSuiteProved`:

  ```sql
  WITH claimed AS (
    INSERT INTO receipts (key, fingerprint, family, stream_key, recorded_at)
    VALUES ($1, $2, $3, $4, statement_timestamp())
    ON CONFLICT (key) DO NOTHING
    RETURNING …
  )
  SELECT true AS won, * FROM claimed
  UNION ALL
  SELECT false AS won, r.* FROM receipts r
   WHERE r.key = $1 AND NOT EXISTS (SELECT 1 FROM claimed)
  ```

  Both documents assert of it: *"the loser blocks until the winner commits, then sees zero rows and
  is a repeat"*, and that the primary-key index serialises *"which holds at **every** isolation
  level — so [[D-126]] is respected and no level is chosen."*

  **Run against PostgreSQL 17.9 at `localhost:55432` on 2026-09-12, two `psql` sessions, the winner
  holding its transaction open for 3 s so the overlap is certain:**

  | Spelling | READ COMMITTED, the loser | REPEATABLE READ / SERIALIZABLE, the loser |
  |---|---|---|
  | **[SPEC] §1.2's CTE, verbatim** | **`(0 rows)`** — no `won`, no receipt | `ERROR: could not serialize access due to concurrent update` · `ROLLBACK` |
  | `ON CONFLICT (key) DO UPDATE SET key = receipts.key RETURNING …, (xmax = 0) AS won` | the **held row**, `won = f`, the winner's fingerprint | the same 40001 abort |
  | `INSERT … ON CONFLICT DO NOTHING;` **then** a separate `SELECT` — the plan's *"two-statement claim"* **defect** | `INSERT 0 0`, then the **held row** | the same 40001 abort |

  Three consequences, and each one breaks something the plan commits to:

  1. **At READ COMMITTED the reference statement returns nothing at all.** The whole CTE is one
     statement; its snapshot is taken before the winner commits. The speculative-insertion wait
     happens inside that statement and does not refresh the snapshot, so the `UNION ALL` branch's
     `SELECT … FROM receipts WHERE r.key = $1` sees no row either. The ledger therefore answers
     `won == false` with **no receipt** — which is exactly the shape the plan's own
     `TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused` refuses with `ErrLedger`
     (*"`won == false` beside no receipt"*). The reference implementation is refused by the
     framework's own door, and `Repeated` is unreachable on the losing path.
  2. **At REPEATABLE READ and SERIALIZABLE the loser's transaction aborts.** It does not block, see
     zero rows and answer anything; it gets SQLSTATE 40001 and is rolled back, taking the caller's
     whole unit — claim, append, complete — with it. `TestTwoCallersRaceOneKey`'s planned assertion
     (*"the loser blocks …, sees zero rows and answers `Repeated`* … *bound through
     `crudsql.DB.WithTxOptions`"*) cannot pass at two of the three levels it is written for, and
     [SPEC]'s *"holds at **every** isolation level"* is false. [[D-126]] is still respected — the
     ledger chooses no level — but the sentence that justifies it is not the true one.
  3. **The defect row is inverted.** The plan's ledger-defect #1 is *"`SELECT … ; INSERT … ON
     CONFLICT DO NOTHING` instead of the single CTE"*, said to make *"the loser stop blocking and
     both append"*. Measured: the `INSERT`-first two-statement spelling is **correct** at READ
     COMMITTED — the loser still blocks on the index, then its own second statement takes a fresh
     snapshot and reads the winner's row. Only the `SELECT`-first ordering is defective, and it is
     defective because of the ordering, not because of the statement count. So the *"Claim is ONE
     statement"* mandate, the `[[D-118]]`-shaped argument for it, and the defect that is supposed to
     falsify it all rest on a premise the database contradicts.
- **Why it matters:** this is ES-07's load-bearing statement. If S3 is written against it, `Claim`'s
  `Repeated` path is dead code at every isolation level, `Once` never short-circuits, and the retry
  after a lost connection — UC-215, UC-227, the whole «зачем» of the appendix — either refuses with
  `ErrLedger` or appends a second copy of the events. If S5 is written against it,
  `TestTwoCallersRaceOneKey` goes red and the likely repair under time pressure is to drop the two
  stricter isolation levels, which silently withdraws the only evidence D-142's [[D-126]] clause
  has. And S6 would publish the statement on `docs/modules/{en,ru}/receipt.md` as *the reference
  implementation* and pin it byte-for-byte with
  `TestTheExampleLedgerIsTheOneTheLiveSuiteProved` — freezing the wrong SQL into a contract a third
  party is told to copy.
- **Close criterion:** the claim statement is replaced by one measured to return the held row to the
  loser, and the isolation-level clause is restated to what PostgreSQL does. Concretely, all four:
  1. [SPEC] §1.2's SQL block, the module pages, `_examples/event-receipts` and S5's fixture carry a
     spelling whose loser receives the held row at READ COMMITTED — the `DO UPDATE … RETURNING …,
     (xmax = 0) AS won` form above, or `INSERT … DO NOTHING` followed by a `SELECT` in the same
     transaction, with whichever is chosen argued against the other two rows of the table above.
  2. `TestTwoCallersRaceOneKey` asserts, per level: READ COMMITTED → the loser answers `Repeated`
     with the winner's range; REPEATABLE READ and SERIALIZABLE → the loser's transaction fails with
     SQLSTATE `40001`, nothing is appended, and **the retry answers `Repeated`**. Row counts read in
     `psql` on every arm. A level that is dropped rather than asserted fails this criterion.
  3. D-142 states the serialisation point as *the index plus the level's conflict behaviour*, names
     40001 and whose job the retry is, and no longer claims the mechanism is level-independent.
  4. The ledger-defect table's row 1 names the `SELECT`-**first** ordering as the defect, or is
     replaced by a defect that is one; the *"Claim is ONE statement"* obligation is either dropped or
     re-argued on a property that was measured.

---

### GAP-2 [high][immediate] `check-tidy` is green at HEAD, and the plan pre-authorises reporting it red as foreign

- **Where:** PLAN §0, *"Two repository gates are red at HEAD and both are foreign. **Reproduced on
  the pristine tree today**: … `make check`'s `check-tidy`, across the satellite modules, traced by
  `EVENTSOURCE_P4_VERIFY.md` §1 to `56349ba`"*; S3's *"`check-tidy` and `check-replaces` are **not**
  in this checkpoint … both are red at HEAD for reasons this phase did not cause"*; the deliverable
  checklist's *"red on `check-tidy` with the identical set red on the pristine tree"*.
- **What:** run twice on the pristine tree today, from a clean `git status`:

  ```
  $ ./scripts/checks.sh tidy      → check-tidy: ok
  $ ./scripts/checks.sh replaces  → check-replaces: ok
  ```

  Both are **green**. The only red arm of `make unit`/`make check` at HEAD is
  `TestNoI18nPackageCostsMoreThanItsErrorSeam`, which reproduces exactly as described. So the plan
  declares a second foreign red that does not exist, excuses two `make check` arms from every one of
  its six checkpoints on that basis, and pre-writes the sentence a section would use to report them
  red — *"the identical set red on the pristine tree"*.
- **Why it matters:** S6 adds `_examples/event-wait` and `_examples/event-receipts` to the
  `_examples` module, and `_examples/go.mod` today requires no `event/eventpg` (the two shipped
  event examples reach only `event`, `event/eventmemory` and `event/projection`). A receipts example
  that reaches a PostgreSQL store adds a require, and a require in a satellite needs a matching
  `replace` — the trap `CLAUDE.md` names by hand, invisible under `go.work` and visible only to
  `check-tidy`/`check-replaces`, which run `GOWORK=off`. With those two arms excluded on a false
  premise, the phase can end with an untidy `_examples/go.mod` and a close-out report that says the
  red was inherited. That is the "gate proceeds silently red" failure the plan's own §0 exists to
  prevent, with the excuse already written.
- **Close criterion:** §0 names one foreign red, not two, and cites the reproduction; S3's exclusion
  paragraph and the deliverable checklist are corrected; and **S6's checkpoint runs
  `./scripts/checks.sh tidy && ./scripts/checks.sh replaces`** after the examples land, both
  required green. If `EVENTSOURCE_P4_VERIFY.md` §1 really did observe a red `check-tidy`, the
  plan says what closed it between then and now rather than carrying the observation forward.

---

### GAP-3 [high][immediate] [SPEC] §6 item 14 has no tagged test, and the plan asserts it delivers items 11–24

- **Where:** PLAN § *What this plan delivers* (*"[SPEC] §6's twenty-four live items carried by
  **twenty-five** tagged tests in `event/eventpg`"*), §0's fifth obligation (*"S4 and S5 are where
  [SPEC] §6's twenty-four items are proved, twice in a row"*), S5's header (*"[SPEC] §6 items
  11–24"*); [SPEC] §6 item 14 (`EVENTSOURCE_P5_USECASES.md:3326-3328`).
- **What:** [SPEC] §6 item 14 is *"**The collision table** (§UC-216) — a byte, a stream, an order —
  **beside the version variant that must NOT collide**, which is the arm that proves the digest's
  contents rather than assuming them"*, filed under *"ES-07, in `event/eventpg`"*. S5's fifteen
  tagged tests cover items 11, 12, 13, 15, 16, 17, 18, 19, 20, 21 and 22 and nothing else; the
  collision table is delivered by `TestADigestCollidesOnAByteAStreamAndAnOrder` and
  `TestTwoAttemptsAtDifferentVersionsDigestEqual`, both **untagged, in `event`**, and the matrix
  routes UC-216 to *"S1 + S3"* with checkpoint S3. The twenty-five/twenty-four arithmetic is
  satisfied by four S5 tests that are not §6 items at all
  (`TestThePublishedClaimBindsNoInstantAsAParameter`,
  `TestFourLedgerDefectsEachBreakTheCaseThatNamesThem`,
  `TestACodecThatDoesNotEncodeTheSameBytesTwice`, and the second half of item 19), so the count
  hides the omission rather than exposing it.
- **Why it matters:** the plan's delivery rule is that a section is reported closed by its
  checkpoint's output, and S5's checkpoint counts fifteen names and will print fifteen passes. The
  close-out will then read *"[SPEC] §6 items 11–24, green twice in a row"* over an item nothing
  tagged ran. Leaving it untagged may well be right — `Digest` issues no store call, so a live arm
  adds little — but that is an argument the plan does not make, and § *What this plan delivers /
  does not deliver* does not record the refusal. An unrecorded omission inside a completeness claim
  is how a gate ends up green over a hole.
- **Close criterion:** either S5 gains a tagged arm for item 14 and the count moves to twenty-six,
  or the "Does not deliver" section records *"§6 item 14 ships untagged, because `Repo.Digest`
  reaches no store and a live arm would re-measure the same pure function"*, the S5 header reads
  *"items 11–13 and 15–24"*, and the twenty-four/twenty-five sentence is restated so the arithmetic
  names what it counts. Verified by re-running the matrix extraction plus a second extraction that
  maps each §6 item number to the test that carries it, with no item unmapped.

---

### GAP-4 [high][immediate] A `Committed`-minted `Mark` records nothing about the spec that minted it, so two projections over one log produce a false `Reached` for a parked event

- **Where:** [SPEC] §5.2's `Mark` doc comment (*"MarkOf **also** carries the projection the barrier
  was observed from, which Wait compares against Of"*); PLAN § *`event/projection/mark.go`* (*"`MarkOf`
  attaches none, because a barrier is folded from checkpoint rows … — and it carries the projection
  it was observed from, which `Wait` compares against `Of`"*); the matrix's UC-204 row, whose S4
  control is *"a **second** projection of the same log is asserted to be unaddressed by the same
  wait"*.
- **What:** the guard is asymmetric by construction. `MarkOf` records the projection and `Wait`
  refuses a mark from another one; `WaitSpec.Committed` records the sequence keys **its own**
  `spec.Sequence` answered and records no identity at all. `Wait` reads `spec.Park`, `spec.Of` and
  `spec.Over` but never `spec.Sequence` — the keys are already on the mark — so there is nothing in
  the mark or in `Wait` that can notice the mark came from a different `WaitSpec`.
- **Why it matters:** the plan's own UC-204 control establishes the deployment that breaks it. A
  host runs two projections over one log — say `orders` keyed `ByStream()` and `invoices` keyed on a
  customer field — and builds two `WaitSpec`s at the composition root, exactly as the plan tells it
  to. A request path writes:

  ```go
  mark, err := orders.Committed(ctx, store, commit)   // keys: the stream key
  invoices.Until = mark
  vis, err := projection.Wait(ctx, invoices)          // asks invoices' park under orders' keys
  ```

  Every value is of the right type, `WaitOf` derived both specs correctly, and nothing refuses. The
  position on the mark is global, so the census comparison is right and the wait reaches. The park
  question is asked under a key no letter of `invoices` was ever parked under, `Holds` answers
  false, and the caller is told **`Reached: true` for an event `invoices` parked** — which is
  verbatim the failure «scan checkpoint после parking не доказывает применение события» exists to
  forbid, and the failure UC-206's `Park`-nil control is written to make impossible for the *other*
  route into it.
- **Close criterion:** `Committed` records `this.Of` on the `Mark` and `Wait` compares it against
  `spec.Of` for **both** doors, refusing with `ErrGeneration` (or `ErrSpec`) when they differ —
  giving `Mark` one rule rather than two. Pinned by an arm of
  `TestAMarkIsMintedOnlyFromANumberAStoreProduced` that mints from one `WaitSpec` and waits on a
  second whose `Of` differs, asserting the refusal, **with the control that the same mark on its own
  spec reaches**; and by a live arm beside `TestAConfirmedCommandIsVisibleWithoutASleep`'s
  second-projection control, since that test already stands the two projections up. If the guard is
  refused instead, [SPEC] §5.2 and both module pages carry the obligation in the same breath as
  `Sequence`'s, and INV-111's falsifier gains the cross-spec arm. This must be settled before S2
  writes `mark.go`: after S2 the `event/projection` section of `docs/api/surface.md` and the S2
  fence are both already recorded.

---

### GAP-5 [high][immediate] `jobs` already ships this mechanism, and D-142 is planned to name only its enum

- **Where:** PLAN S6, D-142: *"why a job cannot express a receipt (never delivered, no worker, no
  lease, no dead-letter, read by a query and not by a drain) … and why the three-way verdict is
  borrowed from `jobs.PlacementOutcome` (`jobs/queue.go:229-241`) rather than invented"*; PLAN
  § *The conformance extension*'s defect table.
- **What:** the plan names the enum and stops. What the tree actually holds is a complete second
  implementation of the same semantics, by symbol: `jobs.Unique(raw string) EnqueueOption`
  (`jobs/queue.go:383-390`), `jobs.PlacementOnce` / `jobs.PlacementUnique`
  (`jobs/placement.go:21`), the verdicts `PlacementCreated` / `PlacementExistingSamePayload` /
  `PlacementConflict` (`jobs/queue.go:229-241`) — *the same key → same content is a repeat, different
  content is a conflict* triple — and a live PostgreSQL driver for it:
  `jobspg.Driver.placeExisting` (`jobs/jobspg/driver.go:108-148`), reached through
  `findIntent` (`jobs/jobspg/repo_ops.go:58`) followed by `insertIntent`
  (`:232-250`, `INSERT … ON CONFLICT … DO NOTHING`, `rows != 1 → errIntentConflict`).

  That is `SELECT`-then-`INSERT … ON CONFLICT DO NOTHING` — the shape the plan's defect table calls
  defect #1 — and under a real race its loser receives `errIntentConflict`, an **error**, not
  `PlacementExistingSamePayload`. So the repository already contains the competing mechanism, it
  already has the losing-path behaviour the receipt contract forbids, and GAP-1's measurements show
  the plan's judgement of that shape is upside-down.
- **Why it matters:** D-142 as planned would record a false comparison in the binding layer —
  "borrowed rather than invented" over a mechanism it never adjudicated — and `docs/modules/en/receipt.md`
  would tell a consumer to write a ledger by hand beside a shipped `jobs.Unique` the page does not
  mention. The repository would then hold two answers to "the same key was presented twice" with no
  document saying which to reach for, which is the third-mechanism outcome `EVENTSOURCE_REFERENCE.md`
  exists to prevent. Worse, defect #1 is currently written so that it "proves" a property the
  shipped `jobspg` code violates, and nobody would notice.
- **Close criterion:** D-142 adjudicates `jobs.Unique`, `jobs.PlacementOnce`,
  `jobs.PlacementExistingSamePayload`/`PlacementConflict` and `jobspg.Driver.placeExisting` **by
  symbol**, answering three questions: why a receipt is not a `PlacementOnce` with a null payload
  (the durable-row-without-a-worker argument, made against the real API rather than the enum); what
  `jobspg`'s losing path does today and whether that is a defect of `jobs` worth a backlog row; and
  which of the two a consumer reaches for, in one sentence on `receipt.md` and one on `jobs`'s page.
  Verified by `grep -c 'PlacementOnce\|jobspg' docs/ai/decisions/D-142-*.md` being non-zero and by
  the defect table's row 1 surviving GAP-1's correction.

---

### GAP-6 [high][immediate] S6 adds four documentation files and a guide, and runs eight of the doc checks that police them

- **Where:** PLAN S6's checkpoint (the counted `-list`/`-run` pattern of eight names); the
  deliverable checklist's *"`make unit` is green **except** `scripts/TestNoI18nPackageCostsMoreThanItsErrorSeam`"*.
- **What:** S6 is the last section of the last phase. It adds `docs/modules/{en,ru}/receipt.md`,
  edits four more module pages in two languages, adds `docs/usage-guides/event-sourcing.md`, a flow,
  three use cases, four decisions and a release-note paragraph. Its checkpoint runs exactly eight
  `scripts/` tests. `scripts/docs_test.go` holds fourteen, and among the ones **not** run are:
  - `TestNoDocPromisesExactlyOnceDelivery` (`scripts/docs_test.go:981`), which walks the whole of
    `../docs` and fails any page promising exactly-once delivery. The page most likely in this
    repository's history to contain the words *"exactly once"* is a new page about **operation
    idempotency**, and the plan itself puts this test's English-only blind spot on the backlog while
    not running it;
  - `TestEveryNameTheProjectionPackageRenamedIsOnBothGuidesAsARename` (`:764`), which walks both
    `projection.md` guides — the two pages S6 edits;
  - `TestTheStatusADocPromisesIsTheOneTheFunctionItNamesReturns` (`:253`).

  No checkpoint in the plan runs `go test ./scripts/` whole, and no checkpoint runs `make unit`.
  The checklist asserts a `make unit` result that no command in the plan produces.
- **Why it matters:** the plan's stated rule is *"a section is reported closed by its checkpoint's
  output, never by `go test` being green"*. Here the inverse bites: the checkpoint's output is green
  over eight names while the repository is red on a ninth, and S6 is the last section, so nothing
  downstream catches it. That is phase 1's failure mode with the counting arm pointed at the wrong
  subset.
- **Close criterion:** S6's checkpoint runs the whole package —
  `go test -race -count=1 ./scripts/` — with the single foreign red named in the report by
  `TestNoI18nPackageCostsMoreThanItsErrorSeam` and every other failure treated as this section's;
  the counted `-run` arm stays as the arm that proves the new tests exist and ran. The deliverable
  checklist's `make unit` line becomes a command S6 executes rather than a claim it makes. Verified
  by the S6 transcript showing one `FAIL:` line and it being the i18n one.

---

### Recorded to the backlog, not fixed here

Appended to [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P5` as items 12–22 under the
2026-09-08 policy, and **left alone**:

| # | Finding | |
|---|---|---|
| 12 | `TestNoModulusIsAppliedToASequenceHash` carries a **third** `< 18` file floor P-4 does not name | `[medium]` |
| 13 | The conformance table states `checkpointDefects` at 8; it is **14** | `[medium]` |
| 14 | `WaitSpec.Committed` takes any `event.Store` with no backing guard, and the guard needs an `event` accessor only S1 may add | `[medium]` |
| 15 | P-7's rebuild-wait cost narrative omits `surveyed`'s `ErrTopology` window | `[medium]` |
| 16 | S1's and S2's `event-kernel-moved` allowed sets exclude the shared test fixtures their tests need | `[medium]` |
| 17 | `_examples/event-receipts` has no named store, and `_examples/go.mod` requires no `event/eventpg` | `[medium]` |
| 18 | `event/projection/errors.go`'s *"none of them crosses a store seam"* is not addressed while `ErrUncommitted` is added | `[medium]` |
| 19 | P-3 names five walks riding on `checkedEventPackages`; there are seven | `[low]` |
| 20 | `ByStream()` is `withDefaults`'s **ninth** clause, not its tenth | `[low]` |
| 21 | § *The snapshot gate* merges D-132's no-op and `event.JSON` bands into one range | `[low]` |
| 22 | `replay`'s `upTo == 0` is an in-band "no bound" sentinel while `StateAt` refuses version zero | `[low]` |

---

### What the next round must re-check

1. GAP-1's replacement statement, **executed** against the live database at all three isolation
   levels, with the transcript in the section report. Not reasoned about.
2. The §6 item → test map, as a second mechanical extraction beside the matrix one.
3. That `check-tidy` and `check-replaces` are green after S6, from a clean tree, with `GOWORK=off`.
4. That `Mark`'s two doors carry the same rule, or that the asymmetry is written down where a
   consumer reads it.
