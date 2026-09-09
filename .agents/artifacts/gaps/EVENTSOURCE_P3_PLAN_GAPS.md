# EVENTSOURCE — phase 3 (projections, checkpoints, catch-up) plan — GAPS

## Round 1 — plan auditor (coverage, checkpoint misuse, kernel fence, `make check` arms) — 2026-09-08

Audited: [`EVENTSOURCE_P3_PLAN.md`](../plans/EVENTSOURCE_P3_PLAN.md) against
[`EVENTSOURCE_P3_USECASES.md`](../usecases/EVENTSOURCE_P3_USECASES.md) (UC-099…UC-130,
INV-066…INV-082), [`EVENTSOURCE_P3_USECASES_GAPS.md`](EVENTSOURCE_P3_USECASES_GAPS.md),
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P2` §5/§25/§59 and `## P3` §1–§15, and the
code itself — re-read rather than taken from the plan's account of it:
`event/{store,reader,bounds,fact,errors,text,identity}.go`,
`event/eventmemory/{transaction,log,cursor,store}.go`,
`event/eventpg/{read,cursor,verify,migration,schema,executor,classify}.go`,
`event/eventtest/{suite,report,inventory,sections_resumption,probe}.go`,
`runtime/{runner,supervisor,loop,periodic}.go`, `jobs/{policy,outcome,handler_error}.go`,
`crud/executor.go`, `scripts/{checks.sh,checks_test.go,event_test.go,extensions_test.go,
extensionlisting_test.go,docs_test.go}`, `_examples/go.mod`, `.gitignore`, and PostgreSQL 17.9 live
at `postgres://vv:vv@localhost:55432/vv`.

**What I drove live before writing anything below.** The plan's "What was measured" section is not
decoration and I reproduced the half of it that decides the DDL, on the same server:
`select version()` → **PostgreSQL 17.9**; `pg_get_keywords()` catcode for `cursor` → **U**, so the
unquoted column name is right; and the fenced save statement on a temp table with the real check
constraints answered **`INSERT 0 1` / `INSERT 0 0` / `INSERT 0 1` / `INSERT 0 0`** on the plan's four
paths, exactly as tabulated. `./scripts/checks.sh event-kernel` is green in this tree, so the arm the
plan re-baselines is live and passing today. **The checkpoint shape itself — a store-minted cursor,
an advance fence, `Progress` as an observation — is correct and is not a finding anywhere below**;
neither is the snapshot deferral, which reports a method, five passes, a warm-up and a *correction*
to §5.1's own paging figure, which is what a real run looks like rather than an assertion.

What is wrong is at the edges of that shape: two ways a checkpoint store can silently answer or
persist the wrong thing, one anti-vacuity rule the plan drops, and four places where the phase's own
evidence — the manifest fence, the section checkpoints, the live census — proves less than the plan
says it does.

Seven blocking findings follow. `[medium]` and `[low]` are appended to
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P3` §18–§34 under the 2026-09-08 policy and
are listed there, not here. (§16 and §17 are the plan's own reservations and I did not take them.)

---

### GAP-1 [critical][immediate] The door checks that absence is total and never that presence is — an empty cursor at `Advance > 0` silently restarts at the origin

- **Where:** the plan's `event/checkpoint.go` contract, "What `Tracker.Load` re-checks, in this
  order" (four items), "What `Tracker.Save` does"; the `eventpg` DDL row
  `cursor text NOT NULL CHECK (octet_length(cursor) <= 4096)`; §INV-068, §INV-082, §UC-101.
- **What:** `Tracker.Load`'s four re-checks are: the store's error at the read door; **absence is
  total** (`Advance == 0` ⇒ empty cursor, empty name, zero progress); the name matches when
  `Advance > 0`; and `len(Cursor) <= MaxCursorBytes`. Nothing checks the converse. A checkpoint with
  `Advance == 7` and `Cursor == ""` passes all four, and `event.Read(log, "")` **is the origin** —
  verified in the code, not inferred: `eventpg/cursor.go:readCursor` returns `walk{}` for the empty
  cursor and `eventmemory/cursor.go:readCursor` returns position 0, both with a comment saying the
  empty cursor is the origin and never a refusal. `Tracker.Save` has the same hole from the other
  side: it refuses a cursor *over* `MaxCursorBytes` and accepts one of length zero, and I confirmed
  live that the planned DDL accepts it — `insert into cp values ('empty','',1,0,0,0,now())` →
  `INSERT 0 1`, `octet_length` 0.
- **The failure, concretely.** Two routes reach it and neither produces an error on any path:
  1. A `Log` whose `ReadAll` answers a non-empty page with an empty cursor. `Reader.checkPage`
     bounds the page length and the position ordering and says nothing about the cursor
     (`event/reader.go:74`), and the plan's new arm only bounds it *above*. The reader's cursor
     becomes `""`, the page applies, `Tracker.Save` persists `""` at advance *n*, the next
     `Reader.Next` reads from the origin and returns the same page — **the projection re-applies
     the head of the log forever while its checkpoint advances once per pass**, reports
     `PhaseDraining`, and never halts. `Progress.Highest` keeps re-reporting the same positions and
     `Applied` climbs.
  2. Any row that reaches `''` by another hand — a NULL-to-empty coercion in a third-party
     `Checkpoints`, a hand-written repair, a restore. `Tracker.Load` accepts it, the projection
     resumes **at the origin** and re-applies the entire log against a live read model.
  Route 2 is precisely §UC-101's Must-not ("it must not restart from the origin — that re-applies
  the whole log against a live read model") reached by a checkpoint that is *readable* rather than
  unreadable, so §INV-068's own falsification never fires.
- **Why this severity:** it is the exact shape the phase brief forbids — "a shape that lets a caller
  silently do the wrong thing is wrong even when it is simpler". There is no error, no halt, no
  observer transition and no readiness signal; the only symptom is a read model being written twice
  or a projection that never leaves the first page. The door exists for one reason (§INV-082 — "a
  store is trusted exactly as far as its numbers are, which is not at all") and it half-applies that
  rule to its own central field.
- **Why this timing:** it is one predicate in the door and one `CHECK` in the DDL, both of which
  land in S1/S2. After S2 the DDL change is a schema version 3.
- **Close criteria:**
  - [ ] `Tracker.Load` refuses `Advance > 0` beside an empty `Cursor` with `ErrWrongStore`, in the
        same list and with the same "presence is total" wording as the absence check.
  - [ ] `Tracker.Save` refuses an empty cursor before issuing, with the same class it uses for an
        over-ceiling one, so the projection halts rather than persisting a resume point that is the
        origin.
  - [ ] `Reader.checkPage`'s new cursor arm bounds the cursor **below as well as above**: a
        non-empty page answered with an empty cursor is `ErrBackend`, named beside the over-length
        case.
  - [ ] `event/eventpg`'s `cursor` column carries `octet_length(cursor) >= 1` (the `bytesBetween`
        shape the other text columns already use), and the goldens and fingerprint move with it.
  - [ ] Three cases: a `Log` decorator minting `""` beside a non-empty page refused at
        `Reader.Next`; a `Checkpoints` answering `{Advance: 7, Cursor: ""}` refused at
        `Tracker.Load` with **no read issued from it**; and a live `Save("")` refused before the
        statement. The `eventtest` `bounds` section gains the empty-cursor row beside the
        `MaxCursorBytes` one.

---

### GAP-2 [high][immediate] `eventpg` persists an opaque cursor in a `text` column, so a cursor that is not valid UTF-8 cannot be checkpointed at all

- **Where:** the plan's `eventpg` table (`cursor text NOT NULL CHECK (octet_length(cursor) <= 4096)`),
  §3.1 ("The two may be one resource … or two"), D8, `event/identity.go:14`, `event/text.go`.
- **What:** `event.Cursor` is `type Cursor string` and the kernel deliberately constrains **nothing**
  about its bytes: `checkText`/`checkName` (the "valid UTF-8, no NUL, no control character" rule) are
  applied to keys, families and wire type names and to no cursor anywhere — I grepped every use of
  `Cursor` in `event/*.go` to be sure. A cursor is opaque bytes by design. The plan then persists it
  in a PostgreSQL `text` column, which cannot hold a NUL byte or an invalid UTF-8 sequence. Measured
  on 17.9 just now: `insert into t values (chr(0))` → `ERROR: null character not permitted`;
  `insert into t2 values (convert_from('\xc3'::bytea,'UTF8'))` → `ERROR: invalid byte sequence for
  encoding "UTF8": 0xc3`. The same schema already knows this and answers it correctly one table over:
  `events.payload` is **`bytea`** (`event/eventpg/schema.go:240`) precisely because it holds bytes the
  kernel does not constrain, while `family`, `key` and `type` are `text` because the kernel *does*
  constrain them.
- **The failure, concretely.** `eventpg.Checkpoints` takes a `DB` and a `Source` and never sees a
  log, so a checkpoint table in a PostgreSQL database beside a log that is **not** `eventpg` is a
  wiring the type system invites and §3.1 blesses ("the two may be one resource or two"). Give it a
  third-party `Log` whose cursor is a packed binary struct — legal, unconstrained, and the obvious
  encoding for a store that does not want base64's 33 % — and the first `Save` fails with a driver
  error the store cannot classify. `outcomeOf` answers `Unclassified`, the append door maps that to
  **`ErrUncertain`** (`event/errors.go:244`), D10b's bounded resolution loads, finds the advance one
  below, and halts. So the operator is told "a save was issued and its outcome was never confirmed"
  about a save that is *structurally impossible* and will never succeed, on every restart, forever.
- **Why this severity:** it silently narrows the checkpoint contract from "persists the store's
  opaque cursor as bytes and reads nothing in it" (§1.2, the plan's own words) to "persists cursors
  that happen to be legal UTF-8", and it reports the narrowing as an uncertain write. The two shipped
  logs happen to mint ASCII (`eventpg` a four-character tag plus base64url, 58 bytes; `eventmemory`
  a fingerprint, a colon and decimal digits), which is exactly why no test in the plan would ever
  find it.
- **Why this timing:** it is the column type, and after S2 changing it is a schema version 3.
- **Close criteria:** one of the two, decided and written down, not both:
  - [ ] the `cursor` column becomes `bytea` with the same `octet_length` bounds, the store binds
        `[]byte(cursor)` and reads it back byte-for-byte — or the store base64-encodes on the way in
        and decodes on the way out, and the column comment says the encoding is the store's and not
        the cursor's; **or**
  - [ ] the kernel declares that a `Cursor` is legal text within `MaxCursorBytes` — the `checkText`
        rule, in `event/bounds.go`'s own list — and enforces it where store honesty is already
        checked (`Reader.checkPage` for a log, `Tracker.Load` for a checkpoint store), so a store
        minting binary is refused at the door instead of at a column.
  - [ ] Either way: a case that saves and loads back a cursor containing a NUL byte and one
        containing `0xff`, asserting the specified outcome (round-trip, or a refusal naming the rule)
        rather than an `ErrUncertain` about a save that cannot land.

---

### GAP-3 [high][immediate] Making `Sibling` "optional and gating nothing" drops the first anti-vacuity rule for `Persistence`, which §UC-123 forbids in as many words

- **Where:** the plan's "`Sibling` is required by `Persistence`, and the plan records that this is
  the wrong capability and does it anyway"; [SPEC] §9.4; §UC-123's Must-not; and the mechanism this
  is modelled on, `event/eventtest/suite.go:187 missing()`.
- **What:** §UC-123's Must-not is "**No capability may be claimed without its hook**, and a run in
  which every section was not certified must fail rather than print `ok`", and §9.4 restates it as
  the first of three anti-vacuity rules. The existing store suite implements it exactly:
  `missing()` walks four claims and returns a fatal message when `stated == Supported && hook == nil`
  — and note that it already solves the plan's stated dilemma, because there the hook that means
  "two values over one backing" is gated on **`SharedBacking`** and not on `Persistence`
  (`{"SharedBacking", …, "Factory.Sibling", factory.Sibling == nil, "shared backing"}`), while
  `Persistence` has no hook at all. The plan's answer to `CheckpointCapabilities` having no
  `SharedBacking` field is to make `Sibling` gate nothing: supplied → `durability` runs and reports
  `passed`, absent → it reports `not certified`. That is the rule deleted for one capability.
- **The failure, concretely.** Two of them:
  1. A third-party `Checkpoints` declares `Persistence: Supported` (it is a database table; of
     course it does) and its factory omits `Sibling`. The run prints `durability: not certified` in
     a list of eleven `passed` lines and exits 0. Under the store suite the same mistake is
     `t.Fatal` before a section runs. The implementer ships a store whose only durability claim was
     never asked for — "a store that only the door catches is a store whose implementer never
     learns", which is the plan's own argument for running the suite raw.
  2. `eventmemory.Checkpoints` declares `Persistence: Unsupported` **and supplies `Sibling`**, so the
     section named `durability` reports **`passed`** for a store that says nothing of it survives the
     process. The word `passed` beside the word `durability` for an in-memory map is the opposite of
     the report.go contract ("Three words, never two. A section a store did not claim and a section a
     store could not demonstrate are reported alike, and neither of them is a pass").
- **Why this severity:** the whole of §UC-123, and the plan's own claim that "a third implementation
  is provable", rests on the three anti-vacuity rules. Dropping one for the capability that decides
  whether a checkpoint survives a restart — which is what a checkpoint is *for* — is the suite
  certifying the thing it cannot certify.
- **Why this timing:** it is the shape of `RunCheckpoints`'s admission, written in S2 and asserted
  against by S2's mutation harness.
- **Close criteria:**
  - [ ] `Sibling` is **required when `Persistence == Supported`** and its absence is fatal before any
        section runs, in the same words `missing()` uses; the two-field `CheckpointCapabilities`
        stays as [SPEC] §9.4 fixes it.
  - [ ] `durability` is gated on `Persistence == Supported`, so `eventmemory.Checkpoints` reports
        `not certified` with its own reason and `eventpg.Checkpoints` reports `passed`. The
        cross-value behaviour of the memory store is exercised where the plan already says it is —
        by every section that takes a second value — and that is stated in the section's reason
        rather than in a verdict it did not earn.
  - [ ] A self-test in `event/eventtest`: a `CheckpointFactory` claiming `Persistence` with no
        `Sibling` **fails the run before a section starts**, and one declaring `Persistence:
        Unsupported` reports `durability` as `not certified` and still certifies the other eleven.
  - [ ] `## P3` §9 stays open and its text records that the gate was moved rather than removed.

---

### GAP-4 [high][immediate] The memory `InUnit` proof requires editing `event/eventmemory/transaction.go`, which S2's own fence and the plan's "complete set of paths" both forbid

- **Where:** the plan's `eventmemory.Checkpoints` contract ("It joins the ambient `*eventmemory.Tx`
  for its log: a `Save` inside a transaction is staged and published at `Commit`, discarded at
  `Rollback`"); "The complete set of paths phase 3 may add to the manifest"; S2's **Files** list;
  S2's manifest fence regex; S3's "the `InUnit` path, which the memory checkpoint store's ambient
  transaction makes reachable with no database at all".
- **What:** I read `event/eventmemory/transaction.go`. `Tx` holds exactly `{log, identity, finished,
  staged []event.Envelope, counts map[event.Stream]int}`; `Commit` publishes `this.staged` to the log
  and nothing else; `Rollback` does `this.log.position += event.Position(len(this.staged))` — so the
  staged slice is load-bearing for burnt positions and a checkpoint row cannot be smuggled into it.
  There is no hook, no callback list and no second staging area. `Begin` and `ambient` are methods on
  `*Store`, not on `*Log`, and the context key is `transactionKey{log *Log}`. Therefore a
  `Checkpoints` built over a `*Log` that stages a save and publishes it at `Commit` **requires**
  changes to `event/eventmemory/transaction.go` (a second staged field, honoured by `Commit` and
  discarded by `Rollback`) and almost certainly to `log.go`/`store.go` (an `ambient` lookup a
  non-`Store` value can make).
  S2's **Files** list names only `event/eventmemory/checkpoints.go`. The plan's declared "complete set
  of paths phase 3 may add to the manifest" — which the plan calls "the plan's own fence" — names
  only `event/eventmemory/checkpoints.go` and `checkpoints_test.go`. And S2's manifest fence,
  `grep -qvE '^event/(eventmemory/checkpoints(_test)?\.go|eventtest/…)$'`, **fails** the moment
  `event/eventmemory/transaction.go` appears in the manifest diff.
- **The failure, concretely.** The implementer reaches S2, discovers `Tx` cannot stage a checkpoint,
  edits `transaction.go`, and the section's own last arm goes red. The plan's rule for that is
  "reported out loud rather than absorbed" — but the thing being reported is the plan's list being
  wrong, and the cheap resolution under time pressure is to add the path to the regex, which is the
  "the check got in the way" outcome the phase brief names. Worse, this is the file that decides
  whether an in-memory `Rollback` burns positions correctly, so it is not a file that should be
  edited without the section saying what changed in it.
- **Why this severity:** S3's entire `InUnit` proof — §UC-105's memory half, §UC-107(d), the
  isolation pass under a unit, D5's "the sink is called inside the unit" — is built on this store,
  and the plan's fence forbids the change that makes it exist. Either the evidence or the fence is
  wrong, and both are load-bearing.
- **Why this timing:** the path list is what every section's last arm is written against; it must be
  right before S1 records the first manifest.
- **Close criteria:**
  - [ ] The plan's path list and S2's fence regex name every `event/eventmemory` file the ambient
        join touches (`transaction.go`, and `log.go`/`store.go` if `ambient` is lifted), with one
        sentence each saying what moves and why — the same standard the plan holds `suite.go`,
        `probe.go` and `report.go` to.
  - [ ] The `Tx` change is named as a shape change: a second staged area, published by `Commit` and
        discarded by `Rollback`, that `Rollback`'s position arithmetic does not read.
  - [ ] A case pins the interaction the change creates: a rolled-back unit that staged **both** an
        append and a checkpoint save leaves the log's position burnt by the append count only, and
        the checkpoint row where it was.

---

### GAP-5 [high][immediate] The manifest fence passes vacuously the moment the section's work is committed, and it is the only control on a check that now re-baselines itself

- **Where:** the plan's "Sections" preamble (the fence, written once and referenced by S1, S2 and
  S3), and each section's last arm.
- **What:** the arm is
  ```sh
  ! git diff HEAD -- scripts/event_kernel.sha256 \
    | sed -n 's/^[+-][0-9a-f]\{64\}  //p' | LC_ALL=C sort -u | grep -qvE '<allowed>'
  ```
  With an **empty** input, `grep -qv` matches nothing, exits 1, and the leading `!` turns that into
  success. The plan justifies `git diff HEAD` on the grounds that it "covers staged and unstaged
  alike, so the arm says the same thing before and after `git add`" — which is true, and stops being
  true after `git commit`. The plan also says, one section earlier, that the re-baseline lands "in
  the same commit as the code" and that this is the whole advantage of spelling (b). So the intended
  workflow and the arm contradict each other: the moment the section is committed, `git diff HEAD`
  for that file is empty and **the fence proves nothing at all**.
- **The failure, concretely.** An agent finishes S2, commits code plus the regenerated manifest
  (which is what the plan asks for), then runs the checkpoint to report the section. Every arm passes,
  including the fence — while an unplanned edit to, say, `event/eventtest/sections_write.go` sits
  inside the recorded manifest, absorbed. This matters more than it would have under the old arm,
  because the git-baseline version could not be silenced by regenerating anything: it compared
  against a commit constant. The manifest version makes `./scripts/checks.sh event-kernel` green by
  construction after `event-kernel-baseline` runs, so the **only** thing standing between phase 3 and
  an unrecorded kernel edit is this fence.
- **Why this severity:** the phase brief asks whether the baseline move is justified or is the check
  being worked around. The move is justified and the manifest is the better spelling — but the
  replacement control is inert in the exact situation it was written for, and no arm asserts the
  diff is non-empty or that the expected paths are *present* rather than merely not-unexpected.
- **Why this timing:** the fence is written once in S1 and referenced three times; fixing it after
  S3 means three sections were reported on an arm that did not run.
- **Close criteria:**
  - [ ] The fence reads the manifest against a recorded predecessor that survives a commit — a copy
        taken before `event-kernel-baseline` runs (`cp scripts/event_kernel.sha256 $TMP/before` at
        the top of the checkpoint and `diff` against it), or `git diff <the phase's base commit>` —
        so it says the same thing before *and after* `git commit`.
  - [ ] The arm asserts the diff is **non-empty** and that every path on the section's own allowed
        list is present in it, not only that no path outside it is: a section that adds
        `event/checkpoint.go` and does not is a section that did not deliver.
  - [ ] `scripts/checks_test.go` gains the case: the fence run over an unchanged manifest must
        **fail**, so the arm cannot pass by having nothing to compare.

---

### GAP-6 [high][immediate] Eleven use cases' named proofs are counted by no checkpoint, and S4 — the live-evidence section — has no checkpoint at all

- **Where:** the coverage matrix; S1's, S2's and S3's `go test -list … | grep -c '^Test'` arms;
  S4's "**Checkpoint (live)** — the same shape as S2's, with S4's own list and count".
- **What:** the plan's own standard is "**Every checkpoint names its tests and counts them before
  running them**". Walking the matrix against the three written count arms (S1: 9 names; S3: 12
  names; S2 live: 9 names; S2 untagged: **no count arm at all**):

  | UC / INV | The matrix's named proof | Counted by |
  |---|---|---|
  | UC-099 | `TestAFreshTrackerAnswersTheZeroCheckpointAndSavesAtAdvanceOne`, `TestAFirstRunStartsAtTheOriginAndASecondResumes` | nothing |
  | UC-101 | `TestAnUnreadableCursorHaltsAndNeverRestartsAtTheOrigin` | nothing |
  | UC-102 | `TestAForgottenCheckpointRefusesTheNextSaveAndHalts` | nothing |
  | UC-105 | `TestTheAdvanceAndTheHandlersRowsCommitTogether` | nothing — and S4's own **Tests** list does not contain this name either |
  | UC-108 | `TestARedeliveryCarriesTheSameIdentitiesInMemoryOfItsOwn` | nothing |
  | UC-111 | `TestAHistoryClassFailureHaltsAndNamesNoData` | nothing |
  | UC-112 | `TestADrainingProjectionReachesFollowingAndStaysThere` | nothing |
  | UC-116 | `TestAClosedStoreHaltsAndATransientBackendRecovers` | nothing |
  | UC-117 | `TestTheSupervisorHoldsAProjectionAndNewStartsNothing` | nothing |
  | UC-122 | `TestForgetTouchesOneNameAndAnAbsentOneIsNotARefusal` | nothing |
  | INV-066 | `TestNoExportedFunctionTakesAPositionAndAnswersACursor`, `TestCursorIsNeverCompared` | nothing — and neither appears in any section's **Tests** list |
  | INV-072 | "a grep of `event/projection`'s own comments" | nothing — `TestNoDocPromisesExactlyOnceDelivery` reads `docs/` only (`scripts/docs_test.go:802`), never Go source |
  | INV-077, INV-079 | a surface check over `docs/api/surface.md`; the §INV-008 grep extended | nothing — S5's checkpoint runs neither |

  And S4, which the coverage matrix names as the **Checkpoint** column for UC-100, UC-104, UC-105,
  UC-106, UC-107(e), UC-110, UC-113, UC-120, UC-121, UC-125, UC-128, UC-130, INV-066, INV-070,
  INV-071, INV-073 and INV-081 — seventeen rows, the phase's whole live case — carries no executable
  checkpoint. "The same shape as S2's, with S4's own list and count" is a placeholder: there is no
  list, no count, no DSN command and no twice-in-a-row loop written out.
- **The failure, concretely.** This is phase 1's failure mode restated: "29 of 32 review verdicts
  were red while the suite passed throughout". `go test ./event/...` runs whatever exists, so a test
  that was never written is a test that never fails; the count arms are the only mechanism that turns
  a missing proof into a red line, and for eleven use cases plus four invariants there is none. A
  reviewer handed S4 cannot execute it.
- **Why this severity:** the plan is the contract for what gets built. A UC whose proof no arm counts
  is a UC that can be reported closed on a green `go test`, which the delivery policy forbids in as
  many words.
- **Why this timing:** the count arms are what each section is reported against; they cannot be added
  retroactively to a section already reported.
- **Close criteria:**
  - [ ] S4 carries a written checkpoint on S2's shape: the `-list` count arm with S4's own anchored
        pattern and its integer, the "the binary lists at all" arm, the DSN'd `-race -count=1
        -tags=integration` run **twice in a row**, and the unset-DSN arm.
  - [ ] Every test name in the coverage matrix appears in the **Tests** list of the section that
        claims it *and* in that section's counted pattern, or the matrix cites the test that is
        actually counted. The two lists are reconciled in both directions.
  - [ ] S2 gains an untagged count arm for what it delivers untagged
        (`eventmemory/checkpoints_test.go`, `eventtest/defects_test.go`, `eventtest/run_test.go`).
  - [ ] INV-072's source-comment grep, INV-066's two surface/AST walks and INV-077/INV-079's surface
        checks are assigned to a file in a section's **Files** list and counted in that section's
        checkpoint, or the matrix stops naming them.

---

### GAP-7 [high][immediate] S2's live checkpoint has no census arm, so a `RunCheckpoints` run in which `transactions` or `durability` was never certified passes the section

- **Where:** S2's "**Checkpoint (live)**" block and the sentence under it — "**the reviewer reads the
  section verdicts, not the `ok` line:** … the census arm asserts each section reported `passed`".
- **What:** there is no census arm. The block runs `go vet`, the lists-at-all arm, a `-list` count of
  nine, two tagged passes, and the unset-DSN arm. Nothing reads a verdict. The claim in the prose is
  the one thing the block does not do — and the repository already has the mechanism it is claiming:
  `event/eventpg/census_integration_test.go` carries
  `TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines` and
  `TestADowngradedSectionIsCaughtByTheCensusAndByNothingElse`, whose own comment says a downgrade
  "from passed to not certified" is caught "by the census and by nothing else". Neither is named in
  S2's count list, neither is extended to `RunCheckpoints`, and `event/eventpg/census_integration_test.go`
  is not in S2's **Files** list.
- **The failure, concretely.** `RunCheckpoints` against live `eventpg` runs twelve sections. Suppose
  the `CheckpointFactory` for the live store is built without `Begin` (or with a `Begin` that returns
  a context the store does not recognise). `transactions` reports `not certified`, `t.Log`s a line
  nobody reads, and the package exits `ok`. S2 is reported closed with the section that certifies the
  **`InUnit` half of the entire phase** never having run — and `RunCheckpoints`'s own third
  anti-vacuity rule does not fire, because eleven other sections certified. Under the plan's
  `Sibling` decision (GAP-3) the same is true of `durability`.
- **Why this severity:** it is the difference between the live gate proving the checkpoint store and
  the live gate printing `ok`. The plan asserts the guard exists; a section reported on that sentence
  is reported on evidence that was never collected.
- **Why this timing:** S2 is where both stores' conformance is claimed; every later section cites it.
- **Close criteria:**
  - [ ] `census_integration_test.go` is in S2's **Files** list and gains the `RunCheckpoints` half:
        every one of the twelve checkpoint sections reported `passed` for live `eventpg`, and exactly
        the expected set (`durability` for `eventmemory`) reported `not certified`, with the reason
        text pinned.
  - [ ] The census's self-falsification is extended the way the existing one is: a deliberately
        downgraded section must be caught **by the census and by nothing else** — i.e. the run
        without it exits `ok`.
  - [ ] S2's live checkpoint names and counts both census tests in its `-list` arm.

---

**Checked and found sound, recorded so a later round does not re-derive them:**

- **The measurements.** PostgreSQL 17.9 confirmed; `cursor` is an unreserved keyword (catcode `U`);
  the fenced save's four paths reproduce exactly (`INSERT 0 1`, `0 0`, `0 1`, `0 0`) against the real
  check constraints. The plan corrects [SPEC] §5.1's paging figure downward (83 %, not 92–94 %)
  rather than repeating it, which is what an independent run looks like. **The snapshot decision
  rests on a measured number**, states its method, and ships the instrument (`BenchmarkStreamReplay`)
  so it is reversible from evidence.
- **`check-deps` / `check-tiers` / `check-utils` / `check-triplets` / `check-replaces` /
  `check-tidy` / `check-workspace`.** Walked file by file against `scripts/checks.sh`. `check-deps`
  is the only one at risk and the plan's reading of it is right — `go list -deps -test
  -tags=integration ./...` means a third-party import in an `event/projection` **test** would fail
  it, which is why every live projection test belongs in `event/eventpg`. No new module, so
  `check-replaces` and `check-workspace` see nothing; `event/projection` adds no `require`, so
  `check-tidy` sees nothing; `TIER0`/`TIER0_STDLIB`/`SHARED` do not contain `event`, so `check-tiers`
  is untouched; `utils/` is not reached; there is no fourth HTTP binding, so `check-triplets` is
  untouched. `_examples` already carries pgx and a `replace` for the library, so D8 really does need
  no new `replace` line.
- **`scripts/event_test.go` and `scripts/extensions_test.go`.** The `charged` row is right: I ran
  `go list -deps ./event ./runtime` and the first-party closure is exactly `utils`, `crud`, `errs`,
  `event`, `runtime` — five paths, as D9 claims. `costOverruns`'s `len(packages) != len(charged)+1`
  really does fail the moment the package exists, so deliverable 6 is self-enforcing.
  `startsNothing`'s goroutine arm really does exempt only packages importing `testing` (derived from
  `.Imports`, which excludes test imports), so the "no `go` statement in a non-test file of
  `event/projection`" constraint is real — and satisfiable, since `Run` needs one goroutine (its
  own), `Drain` is a channel select on the caller's, and the observer publish is synchronous.
  `startsBeforeMain` runs over every package including `eventtest`; `var Unchecked any = unchecked{}`
  is a composite literal and `var _ runtime.Runner = (*Projection)(nil)` is a conversion, so neither
  trips `initialiserCalls`.
- **The kernel baseline move is justified**, and the manifest is the better of [SPEC] §11.1's two
  spellings: it names files in three directions, needs no git, and closes `## P2` §5. The argument
  that a regenerated manifest is reviewable where a moved sha is not is correct. What is wrong is the
  fence around it (GAP-5), not the decision.
- **No exactly-once claim anywhere**, in the plan or in the contracts it fixes; `ErrHalted` is
  correctly never what `Run` returns, which matches `Supervisor.supervise`'s
  `expected := stopping && (err == nil || errors.Is(err, context.Canceled))`.
- **The `runtime` seams are used rather than re-implemented**: `Runner`, `Drainer`, `Readier`,
  `Declaration{Singleton, Durable}`, `Ticks`/`SystemTicks`. `NewPeriodic` and `NewLoop` are correctly
  *not* used — `periodic` runs a fixed-interval pass with a timeout and a `*slog.Logger`, and a
  projection's wait is a three-way select over ticker, wake channel and drain. The three `jobs`
  mechanisms the plan re-spells (`BackoffPolicy`, `RetryLimit`, the permanent/retryable verdict) are
  named as re-spellings with the import-closure reason, in D-129, rather than reinvented silently.
- **INV-071's argument matches the code**: `event/eventpg/read.go` step 7 — "a bound is minted only
  when there is no useful outstanding one, and never while a transaction of this backing is bound" —
  is exactly why the read must be outside the unit, and the burnt-gap live case is the right
  falsification.
- **D7's sealing router, D6's aliasing rule and the copy-per-attempt** are consistent with
  `event/store.go`'s `Envelope.Payload` hand-off rule and with §INV-021; the ownership control in
  `TestARetryReAppliesTheLogsOwnPageAfterABackoff` (a handler that truncates, reorders and overwrites
  the payload bytes) is the one that would otherwise pass vacuously, and it is written.
- **The twenty-four sentinels really are twenty-four** (`event/errors.go:63 vocabulary()`), and
  nothing in the plan adds to them; `refuse(err, door)` exists with exactly the read/append
  asymmetry D10a relies on (`event/errors.go:244`).
- **`crud.ExecutorFor(ctx context.Context, source any) (Executor, bool)`** and
  **`crud.IsTransaction(e Executor) bool`** exist with the signatures §3.2(3)'s fourth check needs,
  so `Spec.Destination any` resolves without a type switch.
