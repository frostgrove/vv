# EVENTSOURCE_P1 — plan (phase 3) — GAPS

## Round 4 — econv-plan-validator (clean context, re-audit) — 2026-09-07

Re-audit of `plans/EVENTSOURCE_P1_PLAN.md` (2767 lines, read in full) after round 3's repairs,
against `usecases/EVENTSOURCE_P1_USECASES.md`, `usecases/EVENTSOURCE_P1_RECONCILE.md`, the
`## Carry into the plan` list in `gaps/EVENTSOURCE_P1_USECASES_GAPS.md:5994-6029`, `CLAUDE.md`,
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` and the econv references. Every
codebase claim below was re-derived by reading and by **running** in this worktree, not taken from
the plan. **All eleven round-3 findings are genuinely closed** — the dispositions are checked
one by one at the end. Two of this round's findings (GAP-135, GAP-137) are residuals the round-3
repairs left behind: GAP-124 fixed the `errors.As` half of the transport story and left the
`errors.Is` half, and GAP-130's zero-value second payload fixed the source of the second decode
and left the ownership of the two encodings it compares.

---

## Round 4 — findings

### GAP-135 [high][immediate] The refusal wrapper lets `errors.Is` reach the store's cause, so `port.KindOf` reads a `crud` sentinel out of it and an **uncertain commit renders 503 "retry me"** — the same defect GAP-124 closed for `errors.As` and left open for `Is`

- **Where:** PLAN.md `#### event/errors.go` — the traversal table (`errors.Is` | `sentinel`,
  `wrapped` **and `cause`**, minus the two context sentinels) and the paragraph beginning "`As`
  reaching `wrapped` and nothing else is what makes the declared wraps readable **without** letting
  a store's incidental cause set the caller's status"; the `Outcome` map row
  `Failure(Unconfirmed, c)` → "`ErrUncertain`, wrapping no class; the context half of `c` is
  unreachable"; [SPEC] §2.3 ("`ErrBackend`, `ErrConflict`, `ErrCursor`, `ErrClosed`, `ErrUncertain`
  and `ErrAmbientNotTransaction` may all carry a store's error as a cause … `errors.Is` reaches
  it"), §UC-034 **Must not happen**.
- **What:** The plan states the goal in terms — a store's incidental cause must not set the
  caller's status — and then closes only the `errors.As` door. `port.KindOf`
  (`port/kind.go:17-24`, read at this worktree) asks `errs.AsFault` **first**; when that finds
  nothing it falls through to `sentinelKind` (`port/kind.go:70-91`), which is **six `errors.Is`
  branches**: `crud.ErrNotFound`, `crud.ErrForbidden`, `crud.ErrConflict`, `crud.ErrUnavailable`,
  `crud.ErrBadRequest`, `crud.ErrMissingID`. The planned wrapper answers `Is` for the cause. So for
  the one sentinel that is defined as *wrapping no class* — `ErrUncertain` — the store's cause sets
  the status anyway, through `Is` instead of through `As`.
  It is not a hypothetical cause: `crudsql` **is** where phase 2's store gets its errors, and it
  classifies by wrapping exactly these sentinels — `crud/adapter/crudsql/conflict_test.go:220-255`
  asserts `errors.Is(got, crud.ErrConflict)` over driver errors, and `crud/errors.go:32,38` are the
  conflict and retryable sentinels the framework tells every subsystem to wrap. The kernel's own
  design assumes it: `ErrBackend → crud.ErrUnavailable` **iff the cause is retryable** can only be
  decided by inspecting the cause for that sentinel.
- **Why this severity:** `high`. Concrete: `eventpg` issues its `INSERT … RETURNING` inside the
  caller's transaction; the connection is reset in the commit window; the store classifies
  `Failure(Unconfirmed, err)` where `err` came back through `crudsql`'s classification carrying
  `crud.ErrUnavailable`. The kernel returns `ErrUncertain` wrapping no class, exactly as designed.
  The HTTP boundary calls `porthttp.Status(err)` → `port.KindOf` → `AsFault` finds nothing →
  `sentinelKind` → `errors.Is(err, crud.ErrUnavailable)` → the wrapper says **true** →
  `KindRetryable` → `http.StatusServiceUnavailable`. The client is told the operation is
  retryable, retries it, and — with no idempotency key, which is [SPEC] non-goal 35 — the same
  decision is appended twice at two versions. Duplicated facts in an append-only history is the
  worst outcome this subsystem has. The same channel renders an `ErrUncertain` whose cause carries
  `crud.ErrConflict` as **409** ("someone else wrote"), which §UC-034 names word for word: *no
  layer may convert an unknown commit into a conflict … or a plain backend failure*. `port.KindOf`
  is that layer, and this plan is the one that decided the transport story (C5). Not `critical`
  only because it takes a store cause that carries a `crud` sentinel plus a caller that honours
  503 — but that is the only store phase 2 plans to write.
- **Why this timing:** the wrapper is S1, it is frozen at S6's baseline, and C5's whole argument
  for which class a sentinel lives in is built on what a transport reads. Discovering it in phase 2
  means editing `port` (a second subsystem's surface) or moving a sentinel between classes, which
  §2.3's own growth rule calls breaking.
- **Close criteria:**
  - [ ] `event/errors.go` states what `errors.Is` answers for a target **outside** the `event`
        vocabulary, per sentinel — not only for the two context sentinels. One of: (a) `Is` stops at
        `sentinel` and `wrapped`, and a store's cause is reachable only through a named exported
        accessor; (b) the write-class and wiring-class sentinels suppress the six `crud` sentinels
        `port/kind.go:70-91` matches, the way C3 suppresses `context.Canceled`; (c) `ErrUncertain`
        carries no cause at all. Whichever is chosen, [SPEC] §2.3's "`errors.Is` reaches it" is
        edited in S6's list, because today it says the opposite.
  - [ ] The chosen answer keeps `ErrBackend → crud.ErrUnavailable` reachable (it is a **declared**
        class wrap, not an incidental cause) and keeps `ErrRefused`'s cause reachable (C2 depends
        on it), so the fix is per sentinel and not a blanket.
  - [ ] A named test in a named file in a named section's `-list` count clause, asserting both
        directions in one table: `porthttp.Status` of an `ErrUncertain` whose cause carries
        `crud.ErrUnavailable` is **500**, and of an `ErrBackend` whose cause carries the same is
        **503**. The second assertion is the control — without it the first passes against a kernel
        that lost every class wrap.
- **Status:** open

### GAP-136 [high][immediate] `Fact.RoundTrip` can never return nil for a fact whose payload type has no non-zero encoding, so every no-payload fact — `AccountClosed`, `OrderCancelled` — fails §UC-042's happy path with `ErrSample` and cannot be made to pass

- **Where:** PLAN.md `### C1 — GAP-100`, the paragraph "Where the second payload comes from, and
  what happens when there cannot be one" ("when the caller's sample **encodes to the same bytes as
  the zero value** … `RoundTrip` then returns `ErrSample` naming the revision, rather than a nil
  error") and its `*Test:*` line ("**two samples per case** … a zero-valued one, where **both**
  codecs must answer `ErrSample`"); `#### event/fact.go`'s `RoundTrip` comment; matrix rows UC-042
  and C1.
- **What:** The refusal is keyed on *the sample encodes as the zero value*, which is a property of
  the **type** whenever the type has no distinguishable non-zero value. A fact declared over
  `type Closed struct{}` — or any payload type whose fields are all zero in every legitimate
  instance — encodes to `{}` for every sample there is, so `RoundTrip` returns `ErrSample` for that
  revision on every call, forever. `eventtest.RoundTrip` reports it as a failure, and the signature
  `RoundTrip(byRevision ...any)` is positional over revisions, so the revision cannot be skipped
  without shifting every later sample onto the wrong revision. The plan's justification — "the
  refusal costs the caller nothing it had", because a zero sample proves nothing about fidelity —
  is true of a zero sample of a rich type and false of the only sample an empty type has: there
  fidelity is total by construction and a pass would be honest.
- **Why this severity:** `high`, and it is a `universality.md` finding: the contract works for the
  aggregates the document sketched (`Opened{At}`, `Credited{Amount}`) and refuses a shape every
  event-sourced domain has. A no-payload fact is the ordinary spelling of a state transition whose
  only data is *that it happened* — the envelope already carries `RecordedAt`, the stream and the
  version, so the payload has nothing left to hold. Concrete: an application declares
  `Declare(accounts, "accounts.closed", From(JSON[Closed]()), foldClosed)` with
  `type Closed struct{}`, writes the test §UC-042 tells it to write
  (`eventtest.RoundTrip(t, closed, Closed{})`), and gets a red test naming a revision it cannot fix.
  The framework's answer would have to be "declare a field you do not need", which is the
  definition of a contract fitted to the sample.
- **Why this timing:** `RoundTrip` and `ErrSample` are S2 and the sentinel set is frozen at S6.
  It is also the runnable half of **blocking carry item 1**, so a mechanism that mis-refuses is the
  item not being closed.
- **Close criteria:**
  - [ ] `RoundTrip` tells apart *this revision's reader type has no non-zero value* from *the
        caller passed a zero sample of a type that has one*. The first is certified
        **fidelity-only** and returns nil; the second is `ErrSample`. Whatever distinguishes them
        must be stated as a mechanism (the zero value's encoding compared against a second,
        kernel-derived probe; the codec's own answer; a declared marker) and must not need
        `reflect` over the application's type graph, which §UC-052 deleted.
  - [ ] `eventtest.RoundTrip`'s report says which of the two properties it proved for each
        revision, so a green run over a no-payload fact does not read as an aliasing proof — the
        plan already promises that column and it now has a third value to carry.
  - [ ] `TestACodecThatDecodesIntoAReusedBufferIsCaught` gains a case whose payload type is
        `struct{}`: `event.JSON` must **pass** it and the scratch-reusing codec must be reported as
        not-certified rather than as a pass, with the populated case beside it as the control.
- **Status:** open

### GAP-137 [medium][immediate] The aliasing proxy compares two encodings it does not own, so a codec that reuses its **encode** buffer — the same codec family the proxy exists to catch — makes the comparison compare a buffer with itself and always pass

- **Where:** PLAN.md `### C1 — GAP-100`, *Runnable proxy* ("decodes the caller's payload for a
  revision, re-encodes the result and **keeps the bytes**, decodes a second, different payload of
  the same revision, re-encodes the first result again, and refuses if the two encodings differ");
  `#### event/codec.go`'s `Codec[V]` comment, which states an obligation for `Decode` and **none**
  for `Encode`.
- **What:** The proxy holds `bytes1 := Encode(v1)` across two further calls on the same codec and
  then compares it with `bytes2 := Encode(v1)`. Nothing in the plan says `bytes1` is cloned, and
  nothing in the `Codec` contract forbids `Encode` from returning a slice into a buffer it reuses —
  the ordinary shape `buf = buf[:0]; …; return buf`. For a codec that reuses **both** buffers,
  which is exactly the hand-rolled binary reader `Codec[V]` exists to admit, `bytes1` and `bytes2`
  are the same array: the second `Encode` overwrites the first, the comparison is
  `x == x`, and the scratch-reusing decoder passes. The one runnable proof of blocking carry item 1
  then certifies the defect it was built to detect.
- **Why this severity:** `medium` — it does not break a caller's program, it breaks the check.
  Concrete: a store author ships a msgpack codec that reuses one encode buffer and one decode
  scratch; `eventtest.RoundTrip` is green; the first `Load` of a two-event stream folds the second
  event's payload twice, because the value handed to the first fold aliased the scratch the second
  decode wrote. That is GAP-100's original failure arriving through a green test.
- **Why this timing:** it is the mechanism of a `[high]` carried gap, in S2, and the fix is one
  clause plus one fixture.
- **Close criteria:**
  - [ ] `event/fact.go` states that `RoundTrip` takes ownership of each encoding — a clone, or a
        stated `Encode`-side obligation that a returned slice is the caller's — before it makes the
        next call on that codec, and the `Codec[V]` comment carries whichever half is the codec's.
  - [ ] `TestACodecThatDecodesIntoAReusedBufferIsCaught` gains a third fixture codec that reuses
        **both** its encode and its decode buffer and must **fail**; with the decode-only codec
        beside it, so a fix that clones the wrong one is caught.
- **Status:** open

### GAP-138 [medium][immediate] `Bind` writes the family set with "no lock" while §INV-038 advertises `*Binding` as safe for concurrent use — a lazily-binding application gets `fatal error: concurrent map writes`, and no planned test can see it

- **Where:** PLAN.md `#### event/binding.go` ("`Bind`'s five constant-time checks … 2. the family
  is not already bound through this `Binding` to a *different* declaration → `ErrFamily`" and "No
  codec walk, no reflection, **no lock** — which is what makes a store per request over a borrowed
  tenant lease ordinary (§UC-055)"); [SPEC] §INV-038's row `*Binding` | *it is a handle* | **safe;
  its family set is written only by `Bind`*; §UC-055; §INV-039 ("The set of bound families lives on
  that `Binding` value").
- **What:** `Bind` mutates per-`Binding` state and the plan says it takes no lock, while the
  invariant table a consumer reads says a `*Binding` is safe for concurrent use. The two are only
  jointly true if `Bind` is single-goroutine, and nothing anywhere says so — §UC-055 pushes in the
  opposite direction by making a per-request store an ordinary shape, which is one short step from
  a per-request or lazily-populated repository cache over a shared `*Binding`.
- **Why this severity:** `medium`, and it is a Go runtime **fatal**, not a recoverable error:
  concurrent map write kills the process and no `recover` sees it. Concrete: an application binds
  lazily — two HTTP handlers, one for `accounts` and one for `orders`, each calling
  `event.Bind(binding, agg)` on first use behind a `sync.Once` of its own — and under any real
  concurrency the second write to the family map lands during the first. It reproduces in
  production and not in the suite: the `concurrency` conformance section drives **one repository**,
  and every other planned test binds sequentially.
- **Why this timing:** it is a public-contract question about a value the composition root holds,
  in S4, and the answer changes either `event/binding.go` or a [SPEC] invariant row that S6 must
  then edit.
- **Close criteria:**
  - [ ] `event/binding.go` states whether `Bind` may run concurrently with itself. If **no**: the
        module page and §INV-038's `*Binding` row say *composition root only, one goroutine*, and
        §INV-038 joins S6's [SPEC] edit list — it currently reads as an unqualified *safe*.
  - [ ] If **yes**: the family set is guarded, the cost is added to `Bind`'s budget line (which
        today says "one allocation" and "no lock"), and a `-race` test binds two aggregates from
        two goroutines through one `*Binding` and asserts one `ErrFamily` and one success for the
        collision case.
- **Status:** open

### GAP-139 [medium][immediate] `self-falsification` is removed from every store's run, which is what §UC-045 **Must not happen** forbids in terms, and the plan records the change in neither `## Disagreements` nor S6's [SPEC] edit list

- **Where:** PLAN.md `#### The section inventory, in code, and the test that it was honoured`
  ("`self-falsification` is **not** in the inventory and that is deliberate"); [SPEC] §D.9's
  section table, row `self-falsification` | **always**; §UC-045 **Trigger** ("Any conformance run,
  including `eventpg`'s in phase 2") and **Must not happen** ("The self-check must not be an
  opt-in, **a separate target** or a test somebody remembers to write once"); matrix row UC-045.
- **What:** C8 (GAP-107) asked for one defect inventory in code and a test that computes coverage.
  The plan delivers that **and** additionally deletes the section from `Run`, so the artefact a
  store author executes no longer falsifies itself. The plan's argument for the deletion is sound
  on its own terms — running another store's defect fixtures inside a store author's run reports
  someone else's failures against theirs — but it is a change to a [SPEC] `[happy]` use case's
  Observed, and the plan's own rule at the top says such a change is stated "in the row that
  changes it and again in `## Disagreements`". It appears in neither, and S6's [SPEC] edit list
  (which names §UC-004, §UC-017, §UC-022, §UC-030, §UC-048, §UC-053, §UC-060, §D.1, §D.2, §D.9,
  §D.12, §D.13, §D.14, §INV-018, §INV-021, §INV-024, §2.3) does not name §UC-045.
- **Why this severity:** `medium`. Concrete: phase 2's `eventpg` lives in its own module and runs
  `eventtest.Run` from its own package test. Under this plan that run executes twenty sections and
  **no** self-check, so the property §UC-045 was written for — "making the sanity check a property
  of every run catches the suite's own rot with the same command that runs it" — is not what
  `eventpg`'s green means. The residual risk is small (the root module's own `make unit` runs
  `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect`), which is why this is not `high`; the
  defect is that a reader of a green `eventpg` run will believe the stronger claim the use case
  makes, and nothing tells them otherwise.
- **Why this timing:** it is the shape of S5's exported API and of §UC-045's Observed; deciding it
  after `eventtest` ships means changing what a published suite does.
- **Close criteria:**
  - [ ] Either `Run` executes the self-check (with the plan's own objection answered — e.g. the
        defect fixtures reported under a name that cannot be mistaken for the store under test),
        or §UC-045's **Trigger**, **Must not happen** and §D.9's `always` row are named in S6's
        [SPEC] edit list and the deletion is argued in `## Disagreements` beside D1–D3.
  - [ ] `eventtest`'s module page states, for a store author reading a green run, that the
        self-check is the root module's own test and not part of their run — or that it is part of
        it, whichever is chosen.
- **Status:** open

### GAP-140 [medium][immediate] Three different defect counts live in the plan and in [SPEC] at once — six, eight and eleven — which is precisely the drift carry item 8 exists to prevent, and S6 edits none of the five [SPEC] sites that say six

- **Where:** PLAN.md `### C8 — GAP-107` ("The count is **eleven**"); S5 **Delivers** ("its eleven
  self-falsification defects"); `## Risks` **R4** ("**Eight defects**, six forwarding decorators and
  two purpose-built stores"); [SPEC] §UC-037 (`:1852`), §UC-048 (`:2007`, `:2024`), §UC-045
  (`:2644`), §INV-035 (`:3474`), §D.9 (`:4588`) — all reading **six**, all unnamed in S6's edit
  list; [GAPS] `## Carry into the plan` item 8 ("the count is equal in §UC-045, §UC-048 (twice),
  §INV-035, §UC-037 and §D.9").
- **What:** C8 answers the count question by moving the inventory into `defects.go` so a test
  computes it, which is right. It then also asserts a number in prose (eleven), the section that
  builds it repeats that number, the risk table states a third one (eight, with a breakdown — "six
  forwarding decorators and two purpose-built stores" — that matches neither six nor eleven), and
  the five [SPEC] sites still say six. Carry item 8's close criterion is the counts being **equal**;
  they are not, and the one place a test would catch a drift covers only `defects.go` against the
  suite's own sections, not the prose.
- **Why this severity:** `medium`. Concrete: an implementer works from S5's file list and R4's
  risk row, builds eight fixtures, and the inventory test passes — because it checks that every
  defect in `defects.go` fails the section named for it, not that eleven exist. The four defects
  C4 and `event/bounds.go` added (the over-long page, the mis-ordered page, the foreign-stream page,
  the illegal read product) are then silently absent, and the kernel-side verification they were
  built to falsify ships unfalsified.
- **Why this timing:** the numbers are in the section an implementer executes, and the [SPEC] edits
  are S6's, which is the last section.
- **Close criteria:**
  - [ ] R4's "eight" is corrected, or removed in favour of a pointer to C8 — one count in the plan.
  - [ ] S6's [SPEC] edit list names §UC-037, §UC-045, §UC-048 (twice), §INV-035 and §D.9 as the
        sites whose defect count changes, since `scripts/docs_test.go` does not read
        `.agents/artifacts/` and nothing else will catch them.
  - [ ] `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` asserts the inventory covers the
        four C4/bounds defects **by name**, so an implementer who builds only [SPEC]'s six fails
        rather than passes.
- **Status:** open

### GAP-141 [low][immediate] The store-honesty checks go from three to four without a row saying so, and §INV-022's own "three is the count everywhere it is stated" now disagrees with the plan at five [SPEC] sites

- **Where:** PLAN.md `#### event/binding.go` ("Checks 1, 3, 4 and 5 are **store-honesty checks and
  they run at both doors**") and `#### event/reader.go` ("`Read` refuses a nil `Log` … and then
  applies the same **four** store-honesty checks `Bind` does"); [SPEC] §INV-022 ("**both** perform
  the same **three** store-honesty checks … Three is the count everywhere it is stated (§UC-031,
  §UC-036, §UC-055, §D.4, §D.7)").
- **What:** The plan adds a nil-store check as `Bind`'s first check — a good addition, argued from
  a composition root that ignored a constructor error — and folds it into the store-honesty set,
  making it four at both doors. §INV-022 states three and names five other sites that repeat it.
  None of the six is in S6's [SPEC] edit list. `Read`'s sentence is additionally self-overlapping:
  it refuses a nil `Log` and then applies "the same four", the first of which is the nil check.
- **Why this severity:** `low` — the behaviour is right and strictly safer; only the count and the
  documents drift. It is listed because it is the exact failure mode C6 (GAP-105) was raised for,
  one level up, and because the plan's own opening rule says a change to [SPEC] is stated in the
  row that changes it.
- **Why this timing:** the doc edits are S6's and the wording is S1/S4's contract text.
- **Close criteria:**
  - [ ] The plan says in the `Bind` row that the count becomes four and why, and S6's [SPEC] edit
        list names §INV-022, §UC-031, §UC-036, §UC-055, §D.4 and §D.7.
  - [ ] `Read`'s sentence is disentangled: either the nil check is one of the four, or it is named
        separately and the four become the remaining three.
- **Status:** open

### GAP-142 [low][immediate] S6's checkpoint chains `make check` with `&&` while the plan records a pre-existing `check-tidy` failure — and `make check` is **green** on this tree, so the debt row is false and pre-authorises waving away a red the change itself caused

- **Where:** PLAN.md `## Risks` R10 and `## Debt` ("a pre-existing **`make check-tidy`** failure in
  `app/http/appfiber`, `auth/access/accessjwt`, `.../revokeredis` and `.../revokeredisfx`. Both
  present on the clean tree before any change here"); S6's checkpoint (`… && make check && make
  unit && make vet && …`).
- **What:** Measured in this worktree just now: `bash scripts/checks.sh tidy` → `check-tidy: ok`,
  and `make check` → `check-deps: ok`, `check-tiers: ok`, `check-utils: ok`, `check-triplets: ok`,
  `check-todo: ok`, `check-replaces: ok`, `check-tidy: ok`, `check-otel-schema: ok`,
  `check-workspace: ok`, exit 0. The four modules the row names have modified `go.mod`/`go.sum` in
  the working tree, so whatever was true at `72e7d22` is not true of the tree the plan will be
  implemented against.
- **Why this severity:** `low` in itself, but the direction of the error is the dangerous one: a
  recorded, named, "verified" pre-existing failure is a licence to read S6's `make check` red as
  somebody else's. If the row stays, an implementer whose `event` change breaks `check-tidy` (a new
  module is the classic way, and phase 2 adds one) has a plan-sanctioned reason not to look.
- **Why this timing:** S6's checkpoint is the gate for the whole phase, and a chain that cannot go
  green is a checkpoint nobody can honestly mark `[x]`.
- **Close criteria:**
  - [ ] The `check-tidy` half of R10 and of `## Debt` is re-measured on the tree the work starts
        from and deleted if it is green, exactly as this round measured it.
  - [ ] If any arm of `make check` is genuinely red before the change, S6's checkpoint says so the
        way the `make integration` clause already does ("Expected: green except …"), rather than
        chaining it with `&&`.
- **Status:** open

### GAP-143 [low][immediate] Two matrix rows still name a section whose `Covers` omits them, and the plan's "zero disagreements … computed rather than eyeballed" is false as written

- **Where:** PLAN.md `## Coverage matrix` rows `INV-017 … | S2 + S4 | S4 |` and
  `INV-028 … | S1 + S4 | S4 |` against S4's `Covers` list, which names neither; the paragraph
  "Zero disagreements remain in either direction, computed rather than eyeballed: 113 rows … each
  section list equal to the set of sections whose `Covers` names it".
- **What:** Computed here over the whole matrix: 113 rows, 68 UC and 45 INV, none missing, none
  duplicated, and **five** row/`Covers` disagreements — three of which are the two withdrawn items
  the plan declares absent by design (UC-023 → S4, S5; INV-037 → S4), and two of which are real:
  INV-017 and INV-028 point at S4 and S4's `Covers` does not carry them. Both were added to the
  matrix in round 3 (INV-028 gained `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse`, INV-017
  gained `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen`) and the `Covers` lists were
  not updated with them — GAP-116's pattern, one round later.
- **Why this severity:** `low`: both named tests **are** in S4's `-list` count clause, so the work
  will be done. What is wrong is the claim of a computed agreement, which is the thing that makes
  the matrix trustworthy at all.
- **Why this timing:** an implementer works from the section's `Covers`, and S4 is where both are
  delivered.
- **Close criteria:**
  - [ ] S4's `Covers` gains INV-017 (the panicking-codec surfacing) and INV-028 (the authority
        comparison), or the two matrix rows drop S4.
  - [ ] The "zero disagreements" paragraph states the two withdrawn rows as the declared exception,
        so the next recount reproduces the same number.
- **Status:** open

### GAP-144 [low][immediate] S1's phase-4 "real evidence" prints an import graph and asserts nothing, so the block exits 0 whatever it prints — GAP-114's vacuity arriving through the other door

- **Where:** PLAN.md S1 checkpoint phase 4 (`… && go list -deps -f '…' ./event/`) and the paragraph
  below it ("The last command is this block's real evidence … **It must print exactly four lines**").
- **What:** Every other checkpoint in the plan was rewritten in round 2 to assert a count before
  running anything, because a command that cannot fail is not a checkpoint. This one prints a list
  for a human to compare against a sentence. `go list -deps` exits 0 for any import graph that
  builds, so an `event` that imported `port`, `jobs` or `tenancy` in S1 passes S1 and is caught
  only in S6.
- **Why this severity:** `low` — S6's `TestNoEventPackageCostsMoreThanTheSeamItNames` closes it
  five sections later, and the graph is unlikely to be wrong at S1.
- **Why this timing:** it is one line in a block that is about to be executed.
- **Close criteria:**
  - [ ] The clause compares: `test "$(go list -deps -f '…' ./event/ | wc -l)" = 4` together with a
        membership check, or an equivalent that is red when a fifth first-party package appears.
- **Status:** open

### GAP-145 [low][immediate] C2's rationale promises a policy refusal can render **429**, and this framework has no 429: `errs.Kind` has no such member and `porthttp.StatusFor` has no such branch

- **Where:** PLAN.md `### C2 — GAP-101`, *Why the kernel does not derive a transport class from the
  cause* ("a quota wrapper that wants a 403 wraps `crud.ErrForbidden`, one that wants a **429**
  builds an `*errs.Fault`"), repeated in `#### event/errors.go`'s `wrapped` table row for
  `ErrRefused` ("A decorator that wants a 429 builds an `*errs.Fault` and it arrives").
- **What:** Read at this worktree: `errs/code.go:45-60` declares `KindInternal`, `KindNotFound`,
  `KindUnauthorized`, `KindForbidden`, `KindRetryable`, `KindConflict`, `KindValidation`,
  `KindBadRequest`, `KindTooLarge`, `KindMethodNotAllowed` — and no rate-limit kind;
  `port/porthttp/errors.go:27-49` maps those ten and defaults to 500, with no
  `http.StatusTooManyRequests` anywhere under `port/` or `errs/`. A decorator that builds an
  `*errs.Fault` can therefore obtain any of ten statuses, none of them 429.
- **Why this severity:** `low` — the mechanism (the cause is the wrap, and it reaches `errors.As`)
  is right and unaffected; only the example is unbuildable. It is listed because the example is
  load-bearing in the argument that the kernel need not derive a class, and D-122 is planned to
  record that argument for a phase-2 reader who will try it.
- **Why this timing:** it is text in S1's contract and in an ADR S6 writes.
- **Close criteria:**
  - [ ] The 429 example is replaced with one this tree can produce (403 through
        `crud.ErrForbidden`, 503 through `crud.ErrUnavailable`, 409 through `crud.ErrConflict`), or
        adding a rate-limit `errs.Kind` is named as a `## Debt` row against `errs`/`port` with the
        note that it is a second subsystem's surface and out of this phase's scope.
- **Status:** open

### GAP-146 [low][immediate] `eventmemory`'s `Close` leaves a live transaction's per-stream claims held forever, so those streams become permanently unwritable through the shared `*Log`, and the `lifecycle` section asserts only invisibility

- **Where:** PLAN.md `#### The transaction and position design, stated` (admission step 3: "another
  **live** transaction holds a claim on this stream → `Failure(Conflict, …)` **at once**"), the
  degradation paragraph ("`Close` is idempotent, returns nil every time, refuses nothing, and
  neither commits nor rolls back staged work (§INV-032)"); [SPEC] §INV-032's falsification, which
  asks for invisibility and its control and nothing about writability.
- **What:** A claim is released by `Commit` or by `Rollback`. `Close` may do neither, so a store
  closed with an unresolved transaction leaves its claims in the `*Log` — and the `*Log` is shared
  by every sibling `Store` value (§UC-054). Every later append to those streams, through any
  sibling, is `ErrConflict` forever. Nothing in the plan says this, and no planned case looks: the
  `lifecycle` section closes and then asserts invisibility and `ErrClosed`, both of which hold.
- **Why this severity:** `low`. `eventmemory` is a test store and a process usually ends; the
  visible shape is a test suite in which one test closes a store mid-transaction and a later test
  sharing the `*Log` gets an inexplicable `ErrConflict` on a fresh stream. It is worth one sentence
  because the alternative reading — that `Close` should release the claims — would let another
  writer take a version the staged records also claim, which §INV-032 forbids `Close` from deciding.
- **Why this timing:** it is a design consequence in S3's own text, and stating it costs one line.
- **Close criteria:**
  - [ ] `### event/eventmemory/`'s degradation paragraph states what a `Close` over an unresolved
        transaction leaves behind, in the same terms the plan already uses for a transaction held
        across a network call.
  - [ ] Either the `lifecycle` case or `TestCloseIsIdempotentAndDecidesNothing` asserts the stated
        answer — whichever it is — so the behaviour is pinned rather than incidental.
- **Status:** open

---

## Verified and found sound — round 4

Stated so the absence of a finding is deliberate.

**Round 3's eleven dispositions are genuine, not reworded.** GAP-124: the wrapper now holds
`sentinel`/`wrapped`/`cause` with a traversal table, `wrapped` is tabulated per sentinel,
`ErrRefused`'s cause **is** its wrap, and
`TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` is in `event/status_test.go`
in **S4** and in S4's `-list` clause, asserting 413 and 500 in one test with the vacuity argument
written down; the `port/porthttp` test-import claim is right (`go list -deps` without `-test`
cannot see it, `check_deps` lists with `-test` at `scripts/checks.sh:32` and porthttp is
first-party). What it left behind is GAP-135, the other traversal. GAP-125: the twenty section
names are an inventory built by an unexported function, `TestEverySectionInTheInventoryWasReported`
has a shortened-inventory control, and the third matrix column exists — I computed it: all 113 rows
carry a `Proved by`, 54 distinct test names, and **every one of them appears in some section's
`-list` count clause** (checked mechanically), while the section names cited are all in the twenty.
GAP-126: *assume unusable*, with `Closed` and `Refused` named as the exceptions, the retry unit
moved to the transaction, the aftermath case with its control, R15 and the `eventpg` debt row.
GAP-127: the seam declares no `Begin`/`Commit`/`Rollback`, the recording store is S4's, and S6's
`go/ast` walk has no allow-list and a stated anti-vacuity demonstration; both `Covers` lists carry
INV-007 and INV-027. GAP-128: `Capabilities()` and `Limits()` retained at the door, `Backing()` per
operation with the reason, and the zero-calls claim is now literal. GAP-129: `check_surface` is
gone, `CLAUDE.md`'s sentence is quoted and obeyed, and R7 records what proves §INV-019 instead.
GAP-130: the second payload is the revision's zero-value encoding and `ErrSample` is the 24th
sentinel — closed as described, and GAP-136 and GAP-137 are about what that mechanism does at the
edges, not a reopening. GAP-131: the append path measures `sum(len(Record.Payload))`, the `MaxBatch`
product is gone, and the budget paragraph no longer calls the two rules one. GAP-132: eight
extension points, one rule, one table. GAP-133: the loop stops on a short page with the cost priced
and the truncating-store defect named; the per-page rule is `after + 1`; `Load` returns the zero
state and the zero token on any error. GAP-134: `Backing.valid` is unexported with its reader
named, `TestClose` is renamed, and the bare-cancellation obligation is on the contract.

**Codebase claims re-derived, by reading and by running.** `cache/errors.go:26-37` declares
`Error`, `Is` and an unexported accessor and **no `Unwrap`, no `As`** — the shape the plan names.
`port/kind.go:17-24` asks `errs.AsFault` first (`errs/fault.go:106-112`, an `errors.As`), and
`sentinelKind` at `:70-91` is six `errors.Is` branches plus three `errors.As` types, with no
`KindTooLarge` row; `port/porthttp/errors.go:27-49` maps `KindTooLarge` → 413 and defaults to 500.
`jobs/bounds.go` really carries `MaxPayloadBytes = 1 << 20` and `MaxNameBytes = 128` (the plan cites
`:6` for a constant at `:7` and `:22-23` for `MaxDecodedBytes`/`MaxPayloadDepth` at `:21-22` —
off-by-one in a citation that no check reads, not worth a finding). `jobs/json.go` is 1407 lines and
`cache/codec.go` 1564; `jobs/json.go:35:jsonCodecFor` and `:1062:discover` exist as cited.
`cache/cachetest/suite.go` imports `testing` in a non-test file and has exactly **seven** `t.Skip`
sites at the seven lines quoted. `crud/sqlrepo/blueprint.go:74:Define` / `:82:TryDefine`,
`jobs/queue.go:808:normalizeSenderError` with its `err.(rejectedPlacement)`,
`jobs/upcast.go:71:upcastOwned`, `jobs/identity.go:273:validRegistryName`, `jobs/catalog.go:25:NewCatalog`,
`jobs/durability.go:223` and `storage/types.go:211` as `bool` capability structs,
`crud/executor.go:507:isNilValue` (unexported) and `:562:SameDataSource`, `crud/executor.go:441:bindingFor`,
`crudsql.Transaction`'s `executor.(interface{ Tx() *sql.Tx })`, `cache/address.go:32-53:NamespaceOf`
hashing its length prefix rather than rendering it, and `health.Probe`'s `Check(ctx) error` — all
present as described. `D-120` is the highest decision, `FL-035` the highest flow and `UC-031` the
highest repo use case, so D-121…D-125, FL-036 and UC-032 are free. `go list -deps` measured:
`./crud` → `utils, crud`; `./errs` → `errs` — so S1's four-line expectation is right. Measured in
this tree: `go test -list '^(TestNope)$' ./errs/` prints only its `ok` line and exits 0, and
`go test -list` prints one line per matching test, so the `grep -c '^Test'` count clauses work as
the plan says.

**Structural checks against the planned file list.** `make check` runs green here today (all nine
arms), which is what GAP-142 is about. `check_deps` lists the root with `-test` and filters
`{{if not .Standard}}`, so `eventtest`'s `testing` import and `status_test.go`'s `port/porthttp`
import are both invisible to it; `check_tiers` reads TIER0 and TIER0_STDLIB **without** `-test` and
TIER0_SEALED (`errs`) with it, so nothing `event` does reaches any of the three arms; `check_utils`
gains one safe `SUBSYSTEMS` row; `check_triplets` is untouched and the refusal to add an `event` row
is argued correctly; `check_replaces`, `check_workspace` and `check_tidy` are untouched because no
module is added. `scripts/tenancy_test.go` really declares `extension` (`:9`), `listed` (`:11`),
`firstPartyDependencies` (`:28`) and `under` (`:37`) at package level, so
`scripts/extensions_test.go` is the right repair and the newcomer's three names do not collide.

**Microkernel.** I walked the seam again for out-of-package constructibility and could not find the
`event/` file `eventpg` would have to diff: every seam value is a plain struct with exported fields
(`Stream`, `Record`, `AppendRequest`, `Envelope`, `Capabilities`, `Limits`), a defined type over
`string`/`uint64` (`Key`, `Version`, `Position`, `Cursor`), an exported constant (`Support`,
`Outcome`), or an opaque value with an exported minting constructor (`Backing`, `Authority`,
`Failure`). The kernel imports no store, there is no registry, no type switch and no `if name ==`.
`event/bounds.go` is a ceiling a store's own numbers live under rather than a list a store joins,
and `eventtest`'s `Factory` needs nothing `eventpg` cannot supply (`Tx` is `crud.Tx`'s shape). The
one thing phase 2 adds outside its own module is `replace` lines in `test/` and `_examples/`, which
is `CLAUDE.md`'s rule for any module and not an `event/` diff.

**Contracts read as a consumer.** The generics compile as written: `Define[Account](name, funcLit)`
infers `ID` from the literal, `Then(prev, JSON[B](), up)` unifies `B` from two independent sources
so a mismatched upcaster is a compile error, `Declare` takes `S`/`ID` from the aggregate and `E`
from the chain. I looked for the silent misuse the shared `Change[S]`/`At[S]` type parameters
invite — a token or a change from aggregate A used with repo B over one state type — and every
route is closed by a check the plan already states: the token's stream is compared with each
change's (`ErrWrongStream`), the backing is compared per non-empty append (`ErrWrongStore`), one
family names one aggregate through a `Binding` (`ErrFamily`), and an append writes to the token's
stream rather than to the repo's, so a cross-repo append of a matching stream lands where it would
have landed anyway. No signature lets a caller name an expected version, and `Load` returns the zero
state and the zero token with any error, so a partially rehydrated aggregate is unreachable.

**Concurrency and integrity of `eventmemory`.** I looked for the data race the design permits and
found none in the store itself: everything mutable is on the `*Log`, admission and commit are both
under the store-wide mutex, an autocommit append takes and releases its claim inside one call,
positions are assigned at commit in one critical section so commit order is position order, and
`Rollback` advancing the counter produces §UC-029's burn without ever leaving an unassigned position
below a committed one. Immediate refusal on a claimed stream removes the two-lock deadlock entirely
(R12 records the cost). The two integrity notes I did find are one level out and one level in:
GAP-138 (`Bind`, in the kernel) and GAP-146 (`Close` over a live transaction).

**Carry items.** All ten `## Carry into the plan` items have a matrix row, a section and a named
artefact, and the seven that live in conformance sections now have the existence proof round 3
demanded. Of the three that block: item 2 (GAP-101) is closed cleanly — `Refused`, `ErrRefused`,
the map row, the `refusal classes` case with its zero-events assertion and the
unclassified-decorator-error pair; item 3 (GAP-102) is closed cleanly — `false`, one mechanism,
a table test and both halves in both windows; item 1 (GAP-100) is closed in the contract clause and
**not** in its runnable proxy, which is GAP-136 and GAP-137.

**Not findings, checked and dismissed.** The twenty-name inventory matches §D.9's table minus
`self-falsification` and the fifteen/five gating split is arithmetically right. `durability` and
`shared backing` prove no matrix row — `durability` never runs in phase 1 (`Persistence:
Unsupported`) and `shared backing` is §UC-054's store-side half, already proved by
`TestTwoStoreValuesOverOneLog`; neither is scope creep. `Read` requiring a non-zero `MaxBatch` and
`MaxKey` from a `Log` that will never use them is [SPEC] §INV-022's own decision, stated per bound,
not the plan's. `RoundTrip`'s comparison of encodings assumes a deterministic codec, which
§INV-023 already makes the codec's promise. `event.JSON`'s `CanEncode` walking `reflect.Type` at
declaration is `jobs/json.go:1062:discover`'s model and not §UC-052's deleted per-value copier.
`ReadOnly` without a `Next()` remains right and D-121 is the right place for it. The ~81-symbol
breach is counted and argued. The plan's refusal to fix `jobs/queue.go:808` and
`crudsql.Transaction` here is correct and both are in `## Debt`.

---

## Round 3 — econv-plan-validator (clean context, re-audit) — 2026-09-07

Re-audit of `plans/EVENTSOURCE_P1_PLAN.md` after round 2's repairs, against
`usecases/EVENTSOURCE_P1_USECASES.md`, `usecases/EVENTSOURCE_P1_RECONCILE.md`, the
`## Carry into the plan` list in `gaps/EVENTSOURCE_P1_USECASES_GAPS.md:5994-6029`, `CLAUDE.md`,
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` and the econv references. Every
codebase claim below was re-derived from the tree at `72e7d22` by reading and by running, not
taken from the plan. The disposition of all fourteen round-1 findings is checked at the end;
**all fourteen are genuinely closed**, and four of this round's findings are residuals the
round-2 repairs left behind rather than reopenings.

---

## Round 3 — dispositions — 2026-09-07

All eleven round-3 findings are **closed** in `plans/EVENTSOURCE_P1_PLAN.md`. One close
criterion is **rejected on the record** (GAP-130's "the scratch-reusing codec must fail in
both") and its finding closed a different way. No round-3 finding carried a `[deferred]`
timing, so nothing moved into `## Debt` on that account; the three new debt rows — a store
whose limits change after the door, what `eventpg` must document about a poisoned
transaction, and the surface baseline as a report rather than a gate — come from GAP-128,
GAP-126 and GAP-129.

| Finding | Disposition |
|---|---|
| GAP-124 `[high]` | closed — the opaque wrapper answers `errors.As` for the refusal's **declared wrap** and nothing else, `ErrRefused`'s cause **is** its wrap, and `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` lands in **S4** asserting 413 and 500 in one test |
| GAP-125 `[high]` | closed — twenty section names in an `eventtest` inventory, `TestEverySectionInTheInventoryWasReported` with a shortened-inventory control, and a third matrix column, `Proved by`, on all 123 rows, copied into FL-036 where `scripts/docs_test.go:49` enforces it |
| GAP-126 `[high]` | closed — **assume unusable**, with `Closed` and `Refused` as the two named exceptions; §UC-022's northstar becomes *retry the transaction, not the append*; a `transactions` aftermath case with its control; R15 and a `## Debt` row for `eventpg` |
| GAP-127 `[high]` | closed — the seam declares no transaction control (S1, `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries`), a recording store (S4), and a `go/ast` walk with no allow-list (S6); both matrix rows re-pointed and both `Covers` lists edited |
| GAP-128 `[medium]` | closed — `Capabilities()` and `Limits()` retained at the door, `Backing()` read per operation with the reason, zero-calls made literal, and §UC-031 named as the independent reason the short circuit does not move |
| GAP-129 `[medium]` | closed — `check_surface` **dropped**; `CLAUDE.md`'s sentence is quoted and obeyed, D2 records the withdrawal, R7 and D-121 restate what proves §INV-019 |
| GAP-130 `[medium]` | closed — the second payload is the revision's zero value; a sample that encodes as the zero value is `ErrSample`, a 24th sentinel in the request class. One criterion clause rejected, argued in the row |
| GAP-131 `[medium]` | closed — the append path measures `sum(len(Record.Payload))`; the `MaxBatch` product check is deleted; the two read products stay and the budget paragraph stops calling them one rule |
| GAP-132 `[medium]` | closed — a policy table for all eight extension points, and the rule that decides it: recover where a refusal channel exists for the same failure, never otherwise |
| GAP-133 `[medium]` | closed — the loop stops on a short page (priced, with the defect that catches a truncating store), the per-page rule is `after + 1` and step one, and `Load` returns the zero state and zero token on any error |
| GAP-134 `[low]` | closed — `Backing.valid` unexported and `Equal`'s reader named, `TestClose` renamed, the bare-cancellation clause on the contract |

---

### GAP-124 [high][immediate] `ErrTooLarge`'s declared class wrap is unreachable through the planned opaque wrapper, so an oversized payload renders **500** and not 413 — which is the exact premise C5 used to keep it on the request class, and the one test that would have caught it is named in no section, no file and no checkpoint

- **Where:** PLAN.md `event/errors.go` — the class table row `request | ErrKey, ErrEncode,
  ErrTooLarge → *errs.Fault{errs.KindTooLarge}` and, three paragraphs below, "The unexported cause
  wrapper is `cache/errors.go:26:opaqueError`'s shape — `Error()` returns the category's text, `Is`
  still reaches the cause"; PLAN.md `### C5 — GAP-104` item 1 ("the request class would answer a
  client `413`") and its `*Test:*` line ("a `porthttp` assertion that a load of an oversized stored
  payload does not render as a client error"); matrix row `C5 … | S1 | S1`; [SPEC] §UC-024 and
  §UC-025 Observed; [GAPS] `## Carry into the plan` item 5.
- **What:** Two defects, and the second is why the first is still standing.
  1. **The wrap cannot be read.** `port.KindOf` (`port/kind.go:17-24`) asks `errs.AsFault(err)`
     first, which is `errors.As(err, &*errs.Fault)` (`errs/fault.go:106-112`). `errors.As` walks
     `Unwrap() error` / `Unwrap() []error` and an `As(any) bool` method; it does **not** consult
     `Is`. The shape the plan names — `cache/errors.go:26-37` — declares `Error`, `Is` and the
     unexported `opaqueErrors` and **no `Unwrap` and no `As`** (read at `72e7d22`). So the Fault is
     invisible to `errors.As` from outside the wrapper, `KindOf` falls through to
     `sentinelKind` (`port/kind.go:70-91`), whose branch list is `crud.ErrNotFound`,
     `ErrForbidden`, `ErrConflict`, `ErrUnavailable`, `ErrBadRequest`, `ErrMissingID` and
     `errors.As` over three `crud`/`query` types — **there is no `KindTooLarge` branch** — so the
     answer is `errs.KindInternal` and `porthttp.StatusFor` (`port/porthttp/errors.go:27-49`)
     answers `http.StatusInternalServerError`. Measured, not reasoned: an `Is`-only wrapper over
     `fmt.Errorf("%w", fault)` gives `errors.Is → true`, `errors.As → false`.
     `ErrConflict → crud.ErrConflict` and `ErrBackend → crud.ErrUnavailable` are unaffected,
     because those two ride `errors.Is` and `sentinelKind` has their branches. **`ErrTooLarge` is
     the one declared class wrap in the set that needs `errors.As`, and it is the one that breaks.**
  2. **The assertion that would have caught it is unplanned.** C5 names a `porthttp` assertion as
     half of its test. S1's phase-5 checkpoint names five tests and none is it; S1's file list
     names no test file for it; the matrix's C5 row points at S1 for both section and checkpoint.
     This is GAP-116's pattern in a new row: the plan promises a check and no section plans it.
     The placement is also wrong on the merits — C5's wording ("a **load** of an oversized stored
     payload") needs `Repo.Load` (S4) and a store (S3), so it cannot execute in S1 at all.
  3. **The assertion as worded is vacuous even once written.** `StatusFor`'s `default` is 500, so
     "does not render as a client error" is true for *every* sentinel the table does not name,
     including the broken `ErrTooLarge`. Its control has to be the positive direction — an
     oversized **caller** payload (§UC-024) rendering 413 — which is precisely the assertion that
     fails today.
- **Why this severity:** `high`. §UC-024's and §UC-025's Observed is that a transport answers the
  status the class already maps to. Concrete: an HTTP handler calls a usecase that calls
  `repo.Append` with a 2 MiB change; the store's `MaxPayload` refuses it; `porthttp.Status(err)`
  answers 500; the client is told the server broke and retries; the operator gets a 5xx alert for a
  client sending too much data, and the request-class/history-class split C5 spent a whole section
  arguing buys nothing because neither side reaches a client status. It is not `critical` only
  because nothing is written and no data is wrong.
- **Why this timing:** `event/errors.go` is S1, the sentinel set and the wrapper shape are frozen
  at S6's `make api` baseline, and C5's argument for which class `ErrTooLarge` lives in is built on
  the mapping. Discovering it in phase 2 means either changing a sentinel's class (breaking) or
  editing `port/kind.go` (a second subsystem's surface, out of this phase's scope).
- **Close criteria:**
  - [ ] The plan states how an `*errs.Fault` reaches `errors.As` from a refusal `event` returns:
        the wrapper implements `As` for `*errs.Fault` and nothing else, or the request-class
        refusals are returned without the opaque wrapper (they carry no store cause to hide), or
        `port` gains a `KindTooLarge` sentinel branch — one of the three, named, with the effect on
        §INV-025 and on C3's context suppression stated for whichever is chosen.
  - [ ] A named test lives in a named file in a named section and appears in that section's
        `-list` count clause. It asserts **both** directions: an oversized caller payload
        (§UC-024) renders `http.StatusRequestEntityTooLarge`, and a load of an oversized **stored**
        payload (§UC-017, `ErrPayload`) does not. Without the first half the second passes
        vacuously against `StatusFor`'s 500 default.
  - [ ] If the test lives under `event/`, the plan states that `event`'s *test* binary importing
        `port/porthttp` is intended, and confirms `scripts/event_test.go`'s cost arm does not see
        it (`{{range .GoFiles}}` and `go list -deps` without `-test` do not — verified against
        `scripts/tenancy_test.go:37,120`), so the check is not loosened on its first run.
- **Status:** **closed** — round 3, in `PLAN.md`. The finding's mechanism is
  confirmed against the tree: `cache/errors.go:26-37` declares `Error`, `Is` and
  an unexported accessor and **no `Unwrap`, no `As`**, and `port.KindOf`
  (`port/kind.go:17-24`) asks `errs.AsFault` first, which is `errors.As`
  (`errs/fault.go:106-112`). The repair is the **first** of the three options,
  taken in `event` alone so no second subsystem's surface moves. `event/errors.go`
  now states the wrapper's three fields (`sentinel`, `wrapped`, `cause`) and a
  traversal table: `errors.Is` reaches all three minus the two context sentinels,
  `errors.As` reaches **`wrapped` only**, `Error()` reaches neither cause nor
  wrap. `wrapped` is tabulated per sentinel — `ErrTooLarge` →
  `*errs.Fault{errs.KindTooLarge}`, `ErrConflict` → `crud.ErrConflict`,
  `ErrBackend` → `crud.ErrUnavailable`, `ErrUpcast` → the application's error, and
  the one exception the finding did not have to raise: **`ErrRefused`'s cause is
  its wrap**, which is what makes C2's promised 429 arrive at all. `As` is
  deliberately *not* let through to an ordinary cause, with the reason stated: an
  `*errs.Fault{KindNotFound}` attached inside a driver stack must not turn an
  uncertain commit into a 404 (§UC-057). Effect on §INV-025: the fault the kernel
  builds is `errs.TooLarge()` naming the bound and the byte count, never the
  payload; for `ErrRefused` the value is one the decorator deliberately built and
  owns. Effect on C3: none, and it is argued — a context sentinel is an
  `errors.errorString` and the suppression lives in `Is`.
  The test is named, filed and counted:
  `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` in
  `event/status_test.go`, **S4** (it needs `Repo.Load` and a store, so S1 was the
  wrong section on the merits too), in S4's `-list` clause, asserting **both**
  directions — 413 for §UC-024's oversized caller payload, 500 for §UC-017's
  oversized stored payload — with the finding's own vacuity argument written into
  the plan. S1 additionally counts
  `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot`. The `port/porthttp`
  import is stated as a **test** import, invisible to `go list -deps` without
  `-test` and therefore to `scripts/event_test.go`'s cost arm and to S1's
  four-line assertion, and green under `check_deps`, which does list with `-test`
  (`scripts/checks.sh:32`).

---

### GAP-125 [high][immediate] The vacuity defence stops at the package boundary: a conformance **section** has no existence proof, and seven of the ten carried gaps land in sections rather than in top-level tests

- **Where:** PLAN.md `## Sections` preamble ("Every checkpoint is split, and every named test is
  counted before it is run … `test "$(go test -list …| grep -c '^Test')" = N`"); S5's phase-5
  checkpoint, which counts exactly four top-level names
  (`TestTheMemoryStoreSatisfiesTheContract`, `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect`,
  `TestATrivialStoreNeedsNoInternalAccess`, `TestARunThatCertifiedNothingFails`); PLAN.md
  `### event/eventtest/` ("Nineteen sections, twenty with C4's addition"); the carried-gap rows
  whose `*Test:*` is a section — C2 (`refusal classes`), C3 (`cancellation`), C4 (`bounds`,
  `stream paging`), C7 (`payload ownership`), C9 (`concurrency`), C10.2 and C10.4 (`lifecycle`).
- **What:** `go test -list` prints top-level `Test…` functions only; a `t.Run` subtest is invisible
  to it. Every conformance section is a subtest of `Run(t, factory)`. So:
  - A suite shipped with twelve of the twenty sections written passes `-list` (four names, all
    present), passes `go test -race ./event/...`, prints twelve *passed* lines, and S5 is marked
    `[x]`. Nothing in the plan counts sections.
  - The four anti-vacuity rules S5 states do not close it: "a claimed capability whose hook is
    missing fails" catches a missing *hook*, "a run in which every claimed section was skipped
    fails" catches only the all-skipped case, and `defects.go`'s inventory test proves that
    *the sections a defect names* exist — which is a subset (`concurrency`, `refusal classes` and
    `lifecycle` have no defect fixture named for them anywhere in the plan).
  - The consequence is precise and it is where it hurts most: **the runnable artefact of C2, C3,
    C4, C7, C9, C10.2 and C10.4 — seven of the ten items round 10 deliberately carried forward,
    including two of the three blocking `[high]`s — has no executable existence proof.** C1's
    proxy is a top-level test and is counted; C2's and C3's are not.
  - Second half, same mechanism at a coarser grain: the matrix has 113 rows and the six checkpoints
    name ~40 tests between them. The other ~70 rows are proved by the trailing blanket
    `go test -race -count=1 ./event/...`, which passes with zero tests written for any of them.
    UC-010, UC-011, UC-018, UC-019, UC-020, UC-025, UC-027, UC-030, UC-031, UC-032, UC-033, UC-036,
    UC-046, UC-049, UC-059, UC-061, UC-062, UC-064 and INV-006, INV-008, INV-020, INV-026, INV-028,
    INV-039, INV-042, INV-043, INV-044 all name a checkpoint whose named-test list contains nothing
    that could prove them.
- **Why this severity:** `high` — `gaps.md`'s "missing test for a stated invariant", applied to the
  mechanism by which the carried gaps are supposed to be proved. Concrete failure: phase 5 writes
  the suite, runs out of budget at `transactions`, and ships without `refusal classes` and
  `cancellation`. Both checkpoints are green, S5 is `[x]`, and the two blocking `[high]` carry
  items are closed in the plan and absent from the tree — which is exactly the outcome GAP-114
  existed to prevent, arriving one level down.
- **Why this timing:** It is the plan's proof mechanism for S5, and S5 is the section the whole
  phase's evidence rests on. A checkpoint corrected after S5 is signed off does not un-sign it.
- **Close criteria:**
  - [ ] `eventtest` carries a section inventory in code on `defects.go`'s model — one slice of
        section names, iterated by `Run` — and the plan lists the twenty names verbatim so the
        inventory can be compared against the document.
  - [ ] A named top-level test asserts the run reported **every** section in that inventory, each
        exactly once, with one of the three words; it is added to S5's `-list` count clause and the
        count raised. Its anti-vacuity control is a run with one section removed, which must fail.
  - [ ] Every matrix row whose checkpoint column names a section either names a test in that
        section's checkpoint clause, or names the conformance **section** that proves it plus the
        inventory test above. A row proved only by the blanket `go test ./event/...` is not a
        proved row.
- **Status:** **closed** — round 3, in `PLAN.md`. Both halves.
  **(1) The sections are an inventory in code.** `event/eventtest/inventory.go`
  holds the twenty section names, built by an unexported function so nothing is
  package-level and mutable (§INV-013), and `Run` iterates it. The plan lists all
  twenty verbatim so the code and the document can be diffed, says which five are
  capability- or hook-gated, and states that a gated section that does not run is
  reported *not certified* — one of the three words — so all twenty are reported
  on every run. It also settles the count the plan had left ambiguous:
  `self-falsification` is **not** in the inventory, because [SPEC] §D.9 makes it
  the one run whose `Factory` the suite writes itself and C8 already moved it into
  `defects.go`.
  **(2) `TestEverySectionInTheInventoryWasReported`** (S5, `package eventtest`,
  so it reaches the unexported runner; the fixture stores stay in
  `package eventtest_test` and both compile into one binary) asserts every
  inventory name was reported exactly once with one of the three words, with a
  one-section-shorter run as its control. It is in S5's `-list` clause and the
  count is raised 3 → 4. The suite's anti-vacuity rules go from four to five.
  **(3) The matrix gained a third column, `Proved by`**, and every one of the 113
  rows plus the ten carried-gap rows now names a counted checkpoint test or an
  inventoried conformance section — machine-checked while editing: no name in the
  column is absent from a `-list` pattern, no section name is absent from the
  inventory. Where the only honest proof was a conformance section the checkpoint
  moved to S5 and S5's `Covers` gained the row; where it was an AST walk it moved
  to S6. Nine tests were added to S4, one each to S1, S3, S5 and S6, two to S2,
  and one to `./scripts/`. The column is copied into `docs/ai/flows/FL-036` in
  S6, where `scripts/docs_test.go:49` (`TestEveryTestNameTheDocsCiteExists`,
  verified present) fails on a cited test name that does not exist — so the names
  are enforced by `make unit` rather than by this document. The `-list` rule in
  the `## Sections` preamble now states its own limit: it stops at the top level
  and cannot see a subtest.

---

### GAP-126 [high][immediate] The contract does not say what a refusal does to the caller's ambient transaction, so §UC-022's prescribed recovery works on `eventmemory` and is impossible on `eventpg` — and `eventtest` certifies both

- **Where:** PLAN.md `event/store.go` → "**A conflict is reported from `Append`, never from the
  caller's own commit** … a caller that writes `if errors.Is(err, event.ErrConflict) { reload and
  decide again }` around `Append` must take that branch on every store. `eventpg` gets it from the
  unique index on `(family, key, version)`, whose losing `INSERT` waits and then raises";
  PLAN.md `### event/eventmemory/` → "another **live** transaction holds a claim on this stream →
  `Failure(Conflict, …)` **at once, without waiting**"; PLAN.md S5's three `transactions` cases;
  [SPEC] §UC-022, §D.5's two-decision shape, §D.6's northstar; [[D-118]] (join the ambient
  transaction, never autocommit).
- **What:** Under [[D-118]] the store writes **inside the caller's transaction**. On PostgreSQL any
  error raised by a statement inside a transaction block aborts that block: every subsequent
  statement answers `25P02 current transaction is aborted, commands ignored until end of
  transaction block` until `ROLLBACK` (or `ROLLBACK TO SAVEPOINT`). So for the program §D.6 and
  §UC-022 tell a caller to write:
  ```
  txCtx, _ := repo.Within(bind(ctx, tx))
  state, at, _ := repo.Load(txCtx, id)
  ...
  if _, _, err := repo.Append(txCtx, at, changes...); errors.Is(err, event.ErrConflict) {
      state, at, err = repo.Load(txCtx, id)      // reload and decide again
  }
  ```
  - on `eventmemory` the losing `Append` staged nothing and released nothing, the `*Tx` is intact,
    the reload succeeds, and the program is correct;
  - on `eventpg` the losing `INSERT` raised `23505`, the caller's `*sql.Tx` is poisoned, the reload
    is `25P02` — which the store classifies as `NotWritten` or leaves unclassified, so the caller
    gets `ErrBackend` (and `crud.ErrUnavailable`, i.e. a 503 and a retry, if the cause is read as
    retryable) for what is an ordinary optimistic-concurrency conflict.
  The three `transactions` conformance cases added in round 2 do not see this: they assert the
  door, the class and that exactly one set of events survives, and then finish the transaction.
  Nothing asserts the transaction is still usable, so both stores pass. This is GAP-112's finding —
  "the two stores the phase ships and plans differ on the one observable §UC-022 exists to fix, and
  `eventtest` is supposed to be what makes them one contract" — surviving its own repair: round 2
  moved the *door* and the *class* onto the contract and left the *aftermath* unstated.
- **Why this severity:** `high`. A stated `[edge]` use case whose caller-visible recovery is
  correct on one conformant store and impossible on another, with the conformance suite certifying
  both, and the wrong answer is a 5xx-and-retry for a conflict. It is not `critical` because no
  data is written or corrupted — the loser's events are correctly absent.
- **Why this timing:** It is a clause on the frozen `Store` contract and an assertion in a
  conformance section being specified now. Adding either after `eventpg` ships is adding a
  requirement `eventpg` has already been certified without.
- **Close criteria:**
  - [ ] `event/store.go`'s contract states, per refusal class, whether the caller's ambient
        transaction remains usable after `Append` or `ReadStream` returns it — one of: it always
        remains usable (which `eventpg` cannot honour without an internal savepoint), it must be
        assumed unusable and the caller's obligation is to abandon or roll back to its own
        savepoint, or the answer is per-store and a **capability** carries it beside
        `Transactions`.
  - [ ] Whichever is chosen, §UC-022's and §D.6's northstar spelling is corrected to a program that
        is correct on both stores, and the correction is listed in S6's [SPEC] edit list.
  - [ ] The `transactions` section gains a case: a conflict raised inside a bound transaction,
        followed by the operation the contract says a caller may perform, asserted to give the
        contract's stated answer; with the same operation after a **successful** append as its
        control, so a store that refuses everything after any error fails the control.
  - [ ] If the answer is "assume unusable", `## Debt` or `## Risks` names it as what `eventpg`
        must document, and `eventmemory` is not permitted to be more forgiving than the contract in
        a way the suite cannot see.
- **Status:** **closed** — round 3, in `PLAN.md`. The contract now answers the
  aftermath, and the answer is **"assume unusable", with two named exceptions**,
  stated on `event/store.go`: a refusal the kernel raises before the store is
  reached (declaration, wiring, request and history classes, and the empty-append
  short circuit) leaves the transaction untouched because nothing was issued;
  `Failure(Closed, …)` and `Failure(Refused, …)` leave it usable too, because
  *refused rather than tried* is exactly what those two outcomes mean — which is
  also what stops §UC-048's quota wrapper from forcing a caller to abandon an
  unrelated transaction; **everything else must be assumed to have made the
  caller's transaction unusable**, and the caller's obligation is to roll back to
  its own savepoint or to abandon the transaction, issuing nothing else on it
  first. The two alternatives are refused on the record: a store-issued savepoint
  is transaction control on a transaction the framework does not own (§INV-027),
  and a per-store capability would make every caller write two programs, which is
  what `eventtest` exists to prevent.
  §UC-022's and §D.6's northstar are corrected in S6's [SPEC] edit list, in terms:
  **the retry unit is the transaction, not the append** — roll back, begin again,
  reload, decide again — which is correct on both stores and is the ordinary
  optimistic-concurrency shape on SQL. The `transactions` section gains the
  aftermath case with the required control (a reload inside the same transaction
  after a *successful* append, so a store that refuses everything after anything
  fails the control), and S5 gains a standing rule: **no case may exercise a
  freedom the contract only permits**, so nothing asserts what `eventmemory`
  happens to survive. `eventmemory`'s own section says it is more forgiving and
  that this is not a promise; `## Risks` R15 and a `## Debt` row name what
  `eventpg` must document, including the internal-savepoint variant and its
  cost.

---

### GAP-127 [high][immediate] INV-007's and INV-027's proof is a "source check" that no section's file list plans — the defect round 2 closed for INV-042 and INV-011, still open in two rows

- **Where:** PLAN.md `## Coverage matrix` → `| INV-007 a conflict is never retried by the framework
  | S4 (source check) | S4 |` and `| INV-027 the framework never opens, commits or rolls back a
  transaction | S4 (source check + a recording store) | S4 |`; S4's `Files` list
  (`event/binding.go`, `repo.go`, `marker.go`, `token.go`, `reader.go` — no test file, no AST
  walk); S4's phase-5 checkpoint (twelve names, none of them either); S6's `Files` list, whose two
  AST checks are `event/mutablestate_test.go` (§INV-013) and `event/refusalmessages_test.go`
  (§INV-011) and nothing else; GAP-116's round-2 disposition, which closed the identical shape for
  INV-042 by dropping S6 from the row and naming S2's and S4's tests instead.
- **What:** Both rows promise a mechanism — a check over the source that no retry loop and no
  `Begin`/`Commit`/`Rollback` call exists under `event/` — and no section delivers one. INV-027's
  second half ("a recording store") is also unnamed: no test in any checkpoint drives a recording
  store and asserts the framework issued no transaction control. These are not bookkeeping rows:
  §INV-027 is [[D-118]]'s enforcement and §INV-007 is [[D-040]]'s, and the failure they guard
  against — a helpful retry added to `Append` in a later phase, or a `tx.Commit()` slipped into
  `Within` — is exactly the kind a reviewer misses and a structural check does not.
- **Why this severity:** `high` — `gaps.md`'s "missing test for a stated invariant", twice, on the
  two invariants that hold the transaction-ownership boundary. Concrete: phase 2's `eventpg`
  author, wanting `ErrConflict` to be recoverable (GAP-126), adds a bounded retry inside
  `Repo.Append`; nothing in `make check` or `make unit` says a word.
- **Why this timing:** The matrix and the section lists are what phases 4 and 5 execute against,
  and S4 is two sections away.
- **Close criteria:**
  - [ ] Either a named file and a named test appear in S4's (or S6's) `Files` list and in that
        section's `-list` count clause — the `go/ast` plumbing S6 already reuses for
        `event/mutablestate_test.go` will carry both: no `for`/`goto` retry around a `store.Append`
        call, and no selector named `Begin`, `Commit` or `Rollback` on a `Store` under `event/` —
        or the two matrix rows drop "source check" and name the runtime tests that prove them.
  - [ ] INV-027's "recording store" half is a named test that counts the store calls one
        `Load`+`Append` makes and asserts the multiset, with the transactional path as its control.
  - [ ] The matrix is re-diffed against the section `Covers` lists in both directions after the
        change, as round 2 did, and the disposition table under the matrix is updated.
- **Status:** **closed** — round 3, in `PLAN.md`. Both rows now name mechanisms
  that a section plans, and the promise is stronger than the one they made.
  **§INV-027 gains a first, free enforcement:** the `Store` seam declares no
  method that opens, commits or rolls back anything, so "the framework never
  opens, commits or rolls back a transaction" is true by the shape of the
  interface before a test runs. That is stated on `event/store.go` and pinned by
  `TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries`, added to S1's `-list`
  clause (5 → 7 names with C5's), where it also proves §INV-012 and §INV-001's
  method-inventory half.
  **The source check is planned.** `event/transactioncontrol_test.go`,
  `TestTheKernelNeverIssuesTransactionControlAndNeverRetries`, in S6's `Files`
  list and in S6's `./event/` `-list` clause (2 → 3): a `go/ast` walk over the
  non-test files directly under `event/` failing on any call whose selector is
  `Begin`, `Commit` or `Rollback`, on any call to the store's `Append` lexically
  inside a `for` or `range`, and on any `goto`. It needs **no** allow-list —
  `Load`'s paging loop contains `ReadStream`, not `Append` — which is what keeps
  it from being loosened on its first run (R6). Its anti-vacuity demonstration is
  named: put a `for` around `Append`, watch it go red, restore, and say so in the
  report.
  **The recording store is a named test.**
  `TestOneLoadAndOneAppendMakeExactlyTheStoreCallsTheContractNames` in S4's
  `-list` clause asserts the multiset of store calls one `Load`+`Append` makes,
  with the transactional path as its control.
  The matrix rows are re-pointed — §INV-007 to S4 + S6, §INV-027 to S1 + S4 + S6
  — the `Covers` lists of S1, S4 and S6 are edited to match, the diff was
  recomputed in both directions, and the disposition table under the matrix
  carries the change.

---

### GAP-128 [medium][immediate] `Append` step 1 reads `Limits()` before the empty-append short circuit, which is a call on the store — so §INV-031's "a recording store asserting zero calls" cannot pass, and the plan's own argument for rejecting GAP-121 refutes it

- **Where:** PLAN.md `event/repo.go` → "1. the token's key — the kernel's text rules **and**
  `Limits().MaxKey`, both of which issue nothing → `ErrKey`. Before the empty-append short circuit
  … — **then the short circuit**"; twenty lines later, "§INV-031's … falsification is *'a recording
  store asserting zero calls'*, which **forbids step 5 (`store.Backing()`) and step 6
  (`store.Transaction(ctx)`) as much as it forbids a statement**"; PLAN.md `### C10` row 3;
  [SPEC] §INV-031 ("**Falsified by** A recording store asserting zero calls") and §D.5's `Load`
  order ("the store's `MaxKey` — reading `Limits()` issues nothing, so this is still *before the
  store is reached*"); S4's `TestAnEmptyAppendChecksTheKeyAndNothingElse`.
- **What:** `Limits()` is a method on `Store`. A recording store counts it. The plan uses "it is a
  call on the store" to forbid `Backing()` and `Transaction(ctx)` before the short circuit and, in
  the same contract, places `Limits()` there. The two spec sentences it inherits disagree with each
  other in the same way (§D.5 treats `Limits()` as not reaching the store; §INV-031 counts calls),
  and the plan resolves neither. An implementer meets this at S4 and has three exits, all bad:
  weaken the recording-store assertion to "no `Append`/`ReadStream`/`Transaction` calls" (the named
  test survives and stops proving §INV-031 as written), move `MaxKey` out of step 1 (which
  contradicts C10.3 and re-opens §UC-051's ordering), or cache `Limits()` on the `Repo` at `Bind`
  — which is probably the right answer and has consequences the plan states nowhere: a store whose
  `Limits()` varies per call is then bound by its first answer, which needs saying beside §INV-022's
  "every bound … unescapable", and the same caching argument would have permitted `Backing()`
  before the short circuit, weakening the rejection of GAP-121.
- **Why this severity:** `medium` — no data is wrong; what breaks is a named test's ability to prove
  what the plan says it proves, and one line of the `Store` contract's stability guarantee.
- **Why this timing:** It is an ordinal in a fixed order landing in S4, and it decides whether a
  named checkpoint test can be written as specified.
- **Close criteria:**
  - [ ] The plan states whether `Capabilities()`, `Limits()` and `Backing()` are read once at
        `Bind`/`Read` and retained, or called per operation — and [SPEC]'s "stable for the store's
        life" clause is cited as what makes the chosen answer safe.
  - [ ] §INV-031's zero-calls falsification is restated in the terms the answer makes true (zero
        calls, or zero calls other than the retained ones read at `Bind`), and
        `TestAnEmptyAppendChecksTheKeyAndNothingElse` is described against that statement.
  - [ ] If `Limits()` is retained, the plan says what a store whose limits change after `Bind` gets,
        and the `bounds` conformance section says whether it asserts anything about it.
- **Status:** **closed** — round 3, in `PLAN.md`, and the resolution makes the
  rejection of GAP-121 stronger rather than weaker.
  **`Capabilities()` and `Limits()` are read once per door — at `Bind` and at
  `Read` — and retained**, which [SPEC]'s "stable for the store's life" clause is
  what makes safe, and which is now also cited as what makes §D.4's constant-time
  claim true by construction. **`Backing()` is read per operation**, alone among
  the three, and the reason is stated: it is the one whose staleness lets a write
  land in the wrong database, because comparing a token against a *remembered*
  backing cannot see a store that re-pointed. So the caching argument does not
  extend to it.
  **§INV-031's falsification is restated as literally zero**: step 1 reads the
  retained `MaxKey`, not `store.Limits()`, so a recording store sees no call of
  any of the eight methods and
  `TestAnEmptyAppendChecksTheKeyAndNothingElse` needs no exemption list.
  **And the short circuit does not move**, for a reason independent of cost:
  §UC-031 states the observable ("a token over another backing *succeeds* with an
  empty change list"), so even a free `Backing()` would not license step 5 before
  it. Both reasons are written down beside each other.
  A store whose limits or capabilities vary after the door is bound by its first
  answer; that is stated on `event/store.go` as a store obligation and recorded in
  `## Debt` as unpoliced, with the reason a conformance case cannot see it — the
  `bounds` section asserts nothing about it.

---

### GAP-129 [medium][immediate] S6 adds `check_surface` to `make check`, which `CLAUDE.md` forbids in terms, and the plan does not record the contradiction anywhere

- **Where:** PLAN.md S6 `Files` → "`scripts/checks.sh` — `event` added to `SUBSYSTEMS`; new
  `check_surface` arm; `scripts/vv` gains `check-surface`; `Makefile` `COMMANDS` gains it; **`make
  check` runs it**"; PLAN.md `### The defining test` item 2, which concedes the check is unsound
  for §INV-019 and that "an unrelated additive export fails it"; `CLAUDE.md` "Verifying work":
  "**`make api` regenerates `docs/api/surface.md`. Nothing checks it and nothing should: a diff
  there is a question for a person.**"; `scripts/modules.sh:134-157:api`, which regenerates the
  whole file for every package of every module.
- **What:** `CLAUDE.md` is binding for this repository and states the negative rule directly. The
  plan quotes the *same* file's header sentence ("a diff there is a question for a person") as
  support for turning that diff into a build failure, which inverts it. The cost is real and the
  plan already names it: every additive export anywhere under `event/` — a `String()` method, a
  sentinel, a constant a later section adds — turns `make check` red until somebody regenerates the
  baseline, which is R6's own "a structural check loosened under pressure on its first run is a
  decorative one" happening by design. It is also the arm the matrix once pointed at for §INV-019
  and no longer does, so its remaining value is a change-detector the plan itself calls neither
  necessary nor sufficient.
- **Why this severity:** `medium` — nothing is unsafe; a binding house rule is broken, a repo-wide
  gate gains a noisy arm, and the plan's rule for exactly this case ("If you believe a decision is
  wrong, do not quietly implement around it. Say so, name the decision, and let the owner decide")
  is not followed.
- **Why this timing:** It is a change to `make check` in S6 that every later change in the
  repository has to live with, and it is one row in `## Disagreements` to fix.
- **Close criteria:**
  - [ ] Either `check_surface` is dropped from `make check` and the phase-1 baseline stays what
        `CLAUDE.md` says it is — a file a person reads the diff of — or `## Disagreements` gains a
        fourth entry naming `CLAUDE.md`'s sentence, stating the argument for overriding it, and
        saying what the owner is being asked to decide.
  - [ ] If it stays, the plan states the arm's scope precisely (the `## github.com/frostgrove/vv/event…`
        sections of `docs/api/surface.md` and nothing else, since `api()` regenerates every
        package) and what a contributor does when it goes red for an unrelated additive export.
- **Status:** **closed** — round 3, in `PLAN.md`, by the finding's **first**
  option: `check_surface` is **dropped**. `CLAUDE.md` is binding and says of
  `docs/api/surface.md` that "nothing checks it and nothing should"; the plan does
  not implement around a binding rule, and quoting the second half of that
  sentence as support for gating on the diff inverted it. What changed:
  `### The defining test` item 2 is rewritten to say the baseline is the
  regenerated `event/…` sections read by a person, with the withdrawal and its
  argument in place, and item 3 makes the git arm a report rather than a gate;
  S6's `Files` list loses the `check_surface` arm, the `scripts/vv` command, the
  `Makefile` `COMMANDS` row and the `scripts/checks_test.go` fixture, keeping only
  `event` in `SUBSYSTEMS`; S6's checkpoint keeps `make api` and
  `git --no-pager diff --stat docs/api/surface.md` and states that nothing gates
  on them; R7 is rewritten to say what is lost (nothing that was the proof) and
  what remains (`TestATrivialStoreNeedsNoInternalAccess`); D-121's paragraph and
  §INV-019's matrix row and `Covers` entries follow; and `## Disagreements` D2 now
  records the proposal as withdrawn and names the freeze-at-first-tag question as
  the one actually put to the owner.

---

### GAP-130 [medium][immediate] `Fact.RoundTrip`'s second decode — the runnable half of blocking carry item 1 — has no stated source for the "second, different payload", and the only source the signature allows is vacuous for a zero-valued sample

- **Where:** PLAN.md `### C1 — GAP-100` ("*Runnable proxy* … decodes the caller's payload for a
  revision, re-encodes the result and keeps the bytes, **decodes a second, different payload of the
  same revision**, re-encodes the first result again, and refuses if the two encodings differ");
  PLAN.md `event/fact.go` → `func (this *Fact[S, ID, E]) RoundTrip(byRevision ...any) ([]E, error)`
  — **one** value per revision; [GAPS] `## Carry into the plan` item 1 (`high`, blocking).
- **What:** The proxy needs two distinct payloads decoded by **one revision's own codec**, because
  that is the codec whose scratch buffer is under test. The signature supplies one value per
  revision, so the second payload has to be manufactured by the kernel, and the plan does not say
  from what. The candidates and what each costs:
  - re-encoding the same sample — identical bytes, so a scratch-reusing codec produces identical
    content on both decodes and the comparison passes. Vacuous, silently;
  - the zero value of that revision's reader type, which the erased `selfEncode` the chain already
    holds can produce — sound **unless the caller's sample is itself the zero value**, in which case
    it collapses to the previous case with no signal;
  - the next revision's sample, which is a different type and a different codec — it does not
    exercise the codec under test at all.
  The plan's named test `TestACodecThatDecodesIntoAReusedBufferIsCaught` requires the scratch-reusing
  codec to fail, so an implementer who picks the first candidate will notice at S2 — but the fix is
  then invented under pressure, in the section that freezes `RoundTrip`'s contract, and the
  zero-sample hole survives because no fixture exercises it.
- **Why this severity:** `medium` — the failure is a silently weakened proxy for an obligation the
  document already concedes it cannot enforce, not a wrong behaviour. It is reported because it is
  the executable half of one of the three findings round 10 left open on purpose.
- **Why this timing:** `RoundTrip` is an exported method landing in S2 and part of the frozen
  surface; its algorithm is the kernel's, as the plan says, so it cannot be adjusted from a test
  later.
- **Close criteria:**
  - [ ] The plan states where `RoundTrip`'s second payload comes from, and what happens when it is
        byte-identical to the first — refuse the round trip with a named refusal, or state in terms
        that the aliasing half of the check is not exercised for that input.
  - [ ] `TestACodecThatDecodesIntoAReusedBufferIsCaught` carries the zero-valued sample as a case,
        not only a populated one, and the scratch-reusing codec must fail in both.
  - [ ] `eventtest.RoundTrip`'s doc row on the module page says which of the two properties it
        proves for a given sample, so a store author reading a green run knows what it did not test.
- **Status:** **closed** — round 3, in `PLAN.md`, with **one clause of close
  criterion 2 rejected on the record** (below).
  **The second payload is the revision's zero value**, produced by the erased
  `selfEncode` the chain already holds — no `reflect`, no second sample, and a
  legal payload of the right type so the codec runs its whole path. The plan
  states why the two rejected candidates are rejected: re-encoding the caller's
  own sample gives identical bytes and passes vacuously; the next revision's
  sample is a different type and a different codec.
  **The hole is closed rather than tolerated.** When the sample encodes to the
  same bytes as the zero value — which is the only case where the two decodes are
  the same decode — `RoundTrip` returns **`ErrSample`**, naming the revision.
  Detection is by comparing *encodings*, so it needs no comparability and also
  catches a sample that is not literally zero and encodes as if it were.
  `ErrSample` is a 24th sentinel, **request class** beside `ErrEncode`, additive
  by §2.3's own growth rule; the count, `event/errors.go`'s table, the metrics
  count (net zero, against `Backing.Valid` unexported) and §2.3's S6 edit all
  follow.
  `eventtest.RoundTrip`'s module-page row states which of the two properties it
  proved for a given sample — fidelity always, non-aliasing only where the sample
  carried data.
  **Rejected:** "the scratch-reusing codec must fail in both". Under this
  resolution the zero-sample case is refused for **every** codec, well-behaved or
  not, because the check cannot be performed — and a zero sample proves nothing
  about fidelity either, so nothing is lost. Asserting that a *scratch-reusing*
  codec fails there would require the kernel to perform a check it has just
  established it cannot perform. The test carries the case, and asserts
  `ErrSample` for both codecs; that is the anti-vacuity the criterion was
  reaching for.

---

### GAP-131 [medium][immediate] The resident-bytes product is enforced against the **worst-case** payload at `Bind` for `MaxBatch`, where the kernel is holding the exact encoded bytes at `Append` — so a correct deployment is refused and the remedy is to make the payload cap wrong

- **Where:** PLAN.md `event/bounds.go` → "`MaxBatch <= MaxResidentBytes / MaxPayload`", "All three
  answer `ErrWrongStore`"; `## Architecture metrics` → Budgets closing paragraph; R13; PLAN.md
  `event/repo.go` step 4 ("the store's `MaxPayload` and `MaxBatch` → `ErrTooLarge`"); [SPEC]
  §UC-011's Observed, §UC-021, §D.13.
- **What:** The three product checks are not the same problem. For `StreamPage` and `MaxRead` the
  store allocates before the kernel sees anything, so a worst-case bound at `Bind` is the only
  bound available and R13's argument is right. For `MaxBatch` it is not: at step 4 the kernel
  already holds every `Record.Payload` and can add up `len()`. Enforcing
  `MaxBatch ≤ MaxResidentBytes / MaxPayload` instead refuses programs whose actual resident bytes
  are three orders of magnitude below the ceiling. Concretely: a deployment declares one document
  fact whose payload is up to 1 MiB, so its store publishes `MaxPayload: 1<<20`; the kernel then
  forces `MaxBatch ≤ 64`; an import operation that appends 400 events of 200 B each — 80 KB, 0.1 %
  of the ceiling — is `ErrTooLarge`, and §INV-003 says an append is one atomic unit that cannot be
  split. The remedies the plan offers are "lower the page" (does not apply to a batch) and "it is a
  modelling question", and the one that actually works is to lower `MaxPayload` below what the
  application needs.
- **Why this severity:** `medium` — a legal program is refused rather than a wrong answer produced,
  and the store's own `MaxBatch` is still the caller-facing bound. It is reported because
  `universality.md`'s test is whether a contract behaves correctly for an aggregate nobody has
  shown it, and "one big fact and many small ones in one store" is the ordinary shape of every
  domain with a document and an audit trail.
- **Why this timing:** `event/bounds.go` and `Append`'s step 4 are S1 and S4, and the ceilings are
  frozen at S6's baseline. Relaxing a refusal later is additive; the deployments squeezed in
  between are not recoverable.
- **Close criteria:**
  - [ ] The plan decides, on the record, whether the resident bound on an append is the **actual**
        sum of `len(Record.Payload)` at step 4 (checked against `MaxResidentBytes`, with
        `MaxBatch` left as the store's own count bound) or the `Bind`-time product — and if the
        product, what a deployment that legitimately needs both a large `MaxPayload` and a large
        batch does, in a sentence that is not "lower `MaxPayload`".
  - [ ] If the actual sum is taken: the sentinel for exceeding it is named (`ErrTooLarge`, request
        class, beside the batch count), the `Bind`-time `MaxBatch` product check is deleted or
        justified as a separate store-honesty check, and the `bounds` conformance fixture's
        at-the-product control is restated against whichever bound survives.
  - [ ] §D.13's and `## Architecture metrics`' budget paragraph is corrected so the three products
        are no longer described as one rule when two of them are enforced against a worst case and
        one against a measurement.
- **Status:** **closed** — round 3, in `PLAN.md`, by the finding's **first**
  option: on the append path the bound is the **actual sum** of
  `len(Record.Payload)`, checked at step 4 against `MaxResidentBytes` and refused
  with **`ErrTooLarge`**, request class, beside the per-record cap and the batch
  count. The `Bind`-time `MaxBatch` product check is **deleted**, and the deletion
  is argued rather than noted: the product form refuses an 80 KB import because
  some other fact in the same store may be 1 MiB, §INV-003 forbids splitting the
  append, and the only remedy left would be lowering a payload cap the application
  needs — one big fact beside many small ones is the ordinary shape of every
  domain with a document and an audit trail. `MaxBatch` keeps `MaxBatchCount` as
  a **count** ceiling, with its "what it protects" column rewritten to the
  per-envelope fixed cost it actually bounds. The two read products stay, with
  R13's argument, because a store fills a page before the kernel sees a byte of
  it. `event/bounds.go`, `Bind`'s check 4, `Append`'s step 4, `MaxResidentBytes`'s
  and `MaxBatchCount`'s table rows, the `## Architecture metrics` budget
  paragraph, the `Bind` and `Append` budget rows, R13 and the `bounds` conformance
  fixture are all restated so the two enforcements are never again described as
  one rule. `TestAnAppendWhoseActualBytesExceedTheResidentCeilingIsRefused` is
  added to S4's `-list` clause, and §D.13's S6 edit names the split.

---

### GAP-132 [medium][immediate] The extension points declare no failure policy for a panic out of a codec, a mapper or a store, while an upcaster's panic is recovered — so hostile stored bytes reach an application decoder with no stated containment

- **Where:** PLAN.md `event/chain.go` ("An upcaster's **panic is recovered** into `ErrUpcast` …
  A **fold's panic is not recovered** (§UC-067) — the asymmetry is §INV-017's whole content");
  PLAN.md `event/codec.go` (the `Codec[V]` contract carries C1's aliasing clause and C9's
  concurrency clause and no panic clause); PLAN.md `event/store.go` (no panic clause); PLAN.md
  `event/aggregate.go` (`Define`'s mapper: injectivity and concurrency, no panic clause);
  `microkernel.md` "Extension points" item 3 ("A failure policy — what happens when an extension
  raises, times out, returns garbage … the policy is written down, not implicit");
  PLAN.md `event/store.go`'s trust boundary paragraph ("A store is any `event.Store` — a shared
  database another service also writes, a restored dump …").
- **What:** `event` names three extension points — `Store`, `Codec[V]` and the declaration
  callbacks (mapper, fold, upcaster) — and states a panic policy for exactly two of the callbacks.
  The gaps that matter:
  - **`Codec.Decode` on the load path.** The plan's own round-2 repair established that a store's
    bytes are untrusted. A non-`encoding/json` codec — protobuf, msgpack, gob, a hand-rolled
    binary reader, all of which `Codec[V]` exists to admit — panics on a truncated or hostile
    buffer. There is no clause saying whether `Load` recovers it. As written it unwinds out of
    `Load` mid-page, past the kernel's page release, into the caller's request goroutine, with the
    caller's transaction open. The asymmetry with `ErrUpcast` is arbitrary: an upcaster's panic on
    the same bytes in the same call is recovered.
  - **`Codec.Encode` at `Fact.New`.** `encoding/json` re-panics a user `MarshalJSON` panic. The
    plan says `New` carries "a marshaller that errors" on the `Change`; a marshaller that panics is
    a different path and is not stated.
  - **A store's panic** in any of the eight methods, and **the injected clock's** panic in
    `eventmemory`.
- **Why this severity:** `medium` — `encoding/json` does not panic on malformed input, so the
  shipped codec is safe and the failure needs a second codec or a hostile log. It is reported
  because the phase freezes the extension point, `microkernel.md` makes the policy mandatory in
  writing, and the plan already writes the policy for the two neighbouring callbacks.
- **Why this timing:** It is a clause on `Codec[V]`'s and `Store`'s contracts in S1 and S2, and the
  recovery decision changes `Load`'s and `New`'s code, not a comment.
- **Close criteria:**
  - [ ] The plan states, for each of `Codec.Encode`, `Codec.Decode`, the identity mapper, a store
        method and `eventmemory`'s clock, whether a panic is recovered and into which sentinel, or
        that it is deliberately not — with the reason, in the form §INV-017's fold/upcaster
        asymmetry already takes.
  - [ ] Whatever is recovered is exercised: a named test drives a panicking `Decode` through `Load`
        and a panicking `Encode` through `Fact.New`, with the non-panicking codec as the control.
  - [ ] If a store's panic is deliberately not recovered, `## Risks` says what a caller's open
        transaction is left in, since the caller owns it.
- **Status:** **closed** — round 3, in `PLAN.md`. `event/codec.go` now carries
  the policy for **every** extension point, as a table, and — the part that makes
  it more than a list — **the rule that decides it**: *the kernel recovers a panic
  from an extension that has a stated refusal channel for the same failure, and
  maps it to the sentinel that channel already produces; it recovers nothing
  else.* So `Codec.Encode` → `ErrEncode`, `Codec.Decode` → `ErrPayload`,
  `Codec.CanEncode` → `ErrCodecType` → `ErrDeclaration`, an upcaster → `ErrUpcast`
  (unchanged); a fold, the identity mapper, a store method and `eventmemory`'s
  clock are **not** recovered, because the first three have no error channel a
  panic could be a second spelling of (§INV-017 says a fold cannot fail) and a
  store has both a channel and an `Outcome` vocabulary, so recovering it would be
  the kernel classifying what the store did not (§INV-045). §INV-017's asymmetry
  therefore stops being arbitrary. The clauses are written where an implementer
  reads them: on `Codec[V]`, on `Define`'s mapper parameter, on
  `eventmemory.Spec.Clock`, and on `event/store.go`.
  `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen` (S4, counted) drives
  a panicking `Decode` through `Load`, a panicking `Encode` through `Fact.New` and
  a panicking `CanEncode` through `Declare`, with the same codec returning errors
  as its control. `## Risks` R14 states what a panicking store leaves the caller's
  open transaction in and why closing it is already the caller's job under
  [[D-118]].

---

### GAP-133 [medium][immediate] `Load`'s paging contract is missing its termination rule and its failure return, and the per-page density rule as worded refuses every page after the first

- **Where:** PLAN.md `event/repo.go` → "**`Load`'s order:** … (3) the paged `ReadStream`, each page
  verified (ascending, **dense from 1**, this stream, no longer than `Limits().StreamPage`), folded
  and released"; PLAN.md `event/store.go` → "`ReadStream` returns envelopes of the stream that was
  asked for, at ascending versions strictly above `after`, and … **dense from 1**"; PLAN.md
  `event/aggregate.go` → `Fold`'s cause 4 "returns the state as of the last change applied";
  [SPEC] §5's `ReadStream` bullet ("at most `Limits().StreamPage` of them, and **a short page means
  'no more at this bound' only when it is genuinely the end**"); §INV-006, §UC-011.
- **What:** Three clauses an implementer needs and does not have.
  1. **The stop condition is unstated.** [SPEC] decides it — a short page ends the load — and the
     plan never repeats it, so the kernel's loop is undefined between "stop on a short page" and
     "read until an empty page". The two differ for a store that returns a short non-final page:
     the first silently truncates a stream and returns a state at a lower version with no refusal,
     which is §INV-006's failure; the second costs one extra read per load and cannot. Since the
     plan spends a section (C4/GAP-119) buying exactly this class of protection for order, the
     omission of the cheaper half of it reads as an oversight rather than a decision.
  2. **"Dense from 1" is a property of the concatenation, not of a page.** Read literally as a
     per-page check — which is how both occurrences are worded, and the second is inside the
     verification list — page 2 of any stream longer than `StreamPage` fails, since its first
     version is `after+1`. The verification is `page[0].Version == after+1` and `+1` thereafter.
     §UC-011 is the only test that would catch it, and the plan's stated control ("a correctly
     ordered page at exactly `StreamPage`") is page one, so it would not.
  3. **What `Load` returns on failure is unstated**, in a document that states the opposite answer
     for the neighbouring call: `Fold` deliberately returns the partially folded state with its
     error. §INV-006 forbids `Load` from doing the same, and nothing in the contract says so.
- **Why this severity:** `medium` — every defect here needs either a defective store (1) or is
  caught by the first long-stream test (2). Reported because 1 is a silent wrong state, which is
  this subsystem's worst observable, and 3 is one clause against a stated invariant.
- **Why this timing:** All three are clauses on `Load`, landing in S4, and 1 decides an assertion in
  a conformance section being specified now.
- **Close criteria:**
  - [ ] `Load`'s order states the stop condition, and says whether a short page is trusted as the
        end or confirmed by one more read; if trusted, the plan names the conformance defect that
        catches a truncating store and which section it fails.
  - [ ] The per-page verification is written as "the first version is `after + 1` and each
        subsequent is one higher", not "dense from 1", in both places.
  - [ ] `Load`'s contract states what it returns with a non-nil error (the zero state and the zero
        token), and a named test asserts it for a mid-load page failure — with `Fold`'s opposite
        answer cited so the asymmetry is deliberate.
- **Status:** **closed** — round 3, in `PLAN.md`, all three.
  **(1) The stop condition is stated:** the loop stops on a page shorter than the
  retained `StreamPage` and issues no confirming read, and the cost of the
  alternative is priced — one extra round trip on *every* load, which is 0.25 % on
  a 391-page load and a doubling on the common three-event stream. The store that
  the rule leaves room for is named: a store returning a short non-final page is
  caught by the **short-page defect**, which fails `stream paging` and, per
  GAP-25, `dense versions` and `conservation`.
  **(2) The per-page rule is rewritten in both places** — `event/store.go`'s
  contract and `Load`'s order — as *"the first version is `after + 1` and each
  subsequent one exactly one higher"*, with an explicit sentence that *dense from
  1* is a property of the concatenation and never of a page. C4's paragraph and
  the `Outcome`-map row for a mis-shaped page are corrected to match.
  **(3) `Load` returns the zero state and the zero token with any non-nil error**,
  stated on the contract with `Fold`'s opposite answer cited and the asymmetry
  argued: `Fold` consumed the caller's own value and handing it back is the only
  honest answer (§UC-041), while `Load` builds a value the caller has never seen.
  `TestALoadThatFailsMidStreamReturnsNothing` is added to S4's `-list` clause,
  with a successful load of the same stream as its control so a `Load` that always
  returned the zero state fails.

---

### GAP-134 [low][immediate] Three residuals: two exported `Backing` methods with no named reader, a test name the plan's own anchoring argument warns about, and a store obligation stated only in C1/C3 prose

- **Where and what:**
  1. **`Backing.Equal` and `Backing.Valid`.** PLAN.md `event/backing.go`'s "why exported" table
     answers "The kernel's comparison at `Bind`, `Read` and every `Append`; **a store calls
     neither**". A method whose only caller is the package that declares it does not need to be
     exported, and `## Architecture metrics` defends the ~81-symbol breach with "none a 'for later'
     API". `Authority.Same` and `Authority.Valid` are different — §UC-030's caller compares them.
  2. **`TestClose`.** S3's phase-5 checkpoint names it inside an anchored pattern, in a plan whose
     own paragraph uses `TestClose` as the example of a name that matches too much. Every other
     name in the plan is a sentence; this one says nothing about what breaks when it fails, which
     is `CLAUDE.md`'s stated rule for failure messages applied to names.
  3. **"A bare cancellation, never a `Failure`" is a store obligation stated in C3's argument
     only.** C3 makes the opaque wrapper answer `false` for both context sentinels unconditionally,
     which means a store that classifies a cancelled-before-issue append as
     `Failure(NotWritten, ctx.Err())` — a natural choice — loses cancellation identity entirely and
     the caller sees `ErrBackend`, retries, and alerts on a client disconnect. [SPEC] §5 does state
     it ("Refuses only for cancellation (**bare**)"); PLAN.md's `event/store.go` contract section,
     which is what an implementer of `eventpg` reads, does not.
- **Why this severity:** `low` — none changes a behaviour the phase can observe; 3 is caught by the
  `cancellation` conformance section for any store that runs the suite.
- **Why this timing:** 1 is an export in the S6 baseline, 2 is a name in a checkpoint, 3 is one
  clause beside the ownership clauses already on `event/store.go`.
- **Close criteria:**
  - [ ] `Backing.Equal` and `Backing.Valid` are unexported, or the table names the reader outside
        `event` that justifies them.
  - [ ] `TestClose` is renamed to a sentence naming what it pins, and the checkpoint pattern
        updated.
  - [ ] `event/store.go`'s contract carries the bare-cancellation clause beside the ownership
        clauses, with C3's consequence stated: a classified cancellation is not recoverable by
        `errors.Is`.
- **Status:** **closed** — round 3, in `PLAN.md`, all three.
  **(1)** `Backing.Valid` is **unexported** (`valid`), with the reason in the
  table: `NewBacking` already told a store whether its identity was accepted, by
  returning an error. `Backing.Equal` **stays exported** and the table now names
  the reader the criterion asked for — `eventtest`'s `shared backing` section,
  which asserts that a sibling reports the *same* backing directly rather than
  inferring it from a round trip. `Authority.Valid` is distinguished and stays.
  Net effect on the ≈ 81-symbol count is zero, against `ErrSample`'s addition, and
  the metrics table and its breach row say so.
  **(2)** `TestClose` → `TestCloseIsIdempotentAndDecidesNothing` in S3's pattern.
  The plan's anchoring paragraph keeps `TestClose` as its *example* of a name that
  matches too much, which is the one place the string still belongs.
  **(3)** The bare-cancellation clause is on `event/store.go`'s contract, beside
  the ownership clauses, with C3's consequence spelled out: a classified
  cancellation is not recoverable by `errors.Is`, so a store that returns
  `Failure(NotWritten, ctx.Err())` turns a client disconnect into a retried,
  alerted backend failure.

---

## Verified and found sound — round 3

Stated so the absence of a finding is deliberate.

**Round 2's fourteen dispositions are genuine, not reworded.** GAP-110: `Compose` is escape-and-join,
and I checked the property that matters rather than the prose — the escape set is the complement of
the key rule, so every output is a legal key; `/` and `%` are both escaped, so splitting on `/` and
unescaping is a total inverse and the rendering is injective by construction, not merely
collision-resistant; the only colliding pair (`Compose()` and `Compose("")`) is refused as empty at
every door; the overhead is priced and D-125 freezes it. GAP-111: six ceilings with derivations, and
the product enforced as three divisions with the zero-field check first, which does not overflow on
a 32-bit build (the residual is GAP-131, about *which* product, not about whether). GAP-112 and
GAP-113: the transaction and position design is now written, and it is internally consistent —
position assignment at commit under one mutex makes commit order position order, so the newest
committed position really is a safe watermark, and `Rollback` advancing the counter produces
§UC-029's burn without ever leaving an unassigned position below a committed one. I looked for the
race the design permits and did not find one: everything mutable is on the `*Log`, admission and
commit are both under the store-wide mutex, an autocommit append takes and releases its claim inside
one call, and immediate refusal on a claimed stream removes the two-lock ordering deadlock entirely
(the cost is R12, which is recorded). GAP-114: the `-list` count clauses are real and the patterns
are anchored for the right reason (both the pass and the fail reproduce in this tree); the residual
is GAP-125, one level down. GAP-115: `scripts/tenancy_test.go` at `72e7d22` does declare `extension`
(`:9`), `listed` (`:11`), `firstPartyDependencies` (`:28`), `under` (`:37`) and both incumbent test
names, and its grep really is `go [a-zA-Z]` unanchored (`:120-134`) — the `go/ast` replacement and
the shared-helper file are the right repairs. GAP-117, GAP-118, GAP-119, GAP-120, GAP-121, GAP-122
and GAP-123 all land as described; I re-counted `cache/cachetest/suite.go`'s `t.Skip` and it is
seven at the seven lines quoted.

**Codebase claims re-derived at `72e7d22`.** `go list -deps` measured: `./crud` → `utils, crud`;
`./errs` → `errs`; `./jobs` → five outside itself; `./tenancy` → two — so S1's "exactly four lines"
expectation for `./event` is right and the comparison is right. `crud/executor.go:507:isNilValue`
is unexported and `:562:SameDataSource` answers `true` for two typed nils of one type and `false`
for a nil interface — so `Backing.Valid()` as defined does its job and the restatement is justified.
`crud.ErrConflict` (`crud/errors.go:32`) and `crud.ErrUnavailable` (`:38`) exist and
`port/kind.go:70-91` has branches for both, so those two class wraps reach a transport (unlike
GAP-124's). `errs.TooLarge`/`KindTooLarge` exist. `cache/cachetest/suite.go` imports `testing` in a
non-test file, so Q9 is right and `check_deps` (`scripts/checks.sh:38`, filtering
`{{if not .Standard}}`) will not see it. `D-120` is the highest decision and `UC-031` the highest
repo use case, so D-121…D-125 and UC-032 are the free numbers.

**Structural checks against the planned file list.** `check_deps`: no third-party import, and the
root listing includes `-test`, which the plan's fixtures satisfy. `check_tiers`: `event` joins
neither `TIER0` nor `TIER0_STDLIB` and importing `crud` (which *is* `TIER0_STDLIB`) constrains
nothing, so it is untouched. `check_utils`: `SUBSYSTEMS` gains one safe row. `check_triplets`:
untouched, and the refusal to add an `event` row is argued correctly — two implementations of one
contract are held equal by `eventtest.Run`, which is stronger than matching names.
`check_replaces`/`check_workspace`/`check_tidy`: untouched, since no module is added. The only new
arm is `check_surface`, which is GAP-129.

**Microkernel.** I walked the store seam again for out-of-package constructibility and agree with
the plan: `Stream`, `Record`, `AppendRequest`, `Envelope`, `Capabilities` and `Limits` are plain
structs with exported fields; `Key`, `Version`, `Position` and `Cursor` are defined types over
`string`/`uint64`; `Backing`, `Authority` and `Failure` have exported minting constructors. No
kernel file imports a store, there is no registry, no type switch and no `if name ==`. After
GAP-111's repair I could not find a kernel file `eventpg` would have to diff — `event/bounds.go` is
now a ceiling a store's own numbers live under, and a deployment that outgrows `MaxKeyBytes` digests
in its own mapper. GAP-131 is about which bound is measured, not about a kernel edit.

**Contract reading.** The generic signatures compile as written: `Define[Account](name, funcLit)`
infers `ID` from the literal's parameter type; `Then(prev, JSON[B](), up)` unifies `B` from two
independent sources, so a mismatched upcaster is a compile error; `Declare` takes `S`, `ID` from the
aggregate and `E` from the chain. I checked the aliasing hazard I expected in `Chain` — two facts
branched from one shared prefix chain sharing a reader slice — and it cannot occur: every hop
re-types every retained reader to the new `V`, so `Then` must allocate a fresh slice, and there is
no `append` to share. `repoB.Append(atFromA, changesFromA…)` over one state type is deliberate and
is argued in [SPEC] §INV-020, not an oversight. §INV-044's identity-carrying `Fold` closes the
crossing the type system cannot.

**Not findings, checked and dismissed.** The empty-append short circuit's stated position and its
two consequences are argued from §INV-031's own text and I agree the round-2 rejection of GAP-121's
repair was correct (GAP-128 is about a third store call the same argument forbids, not about
reopening it). `ReadOnly` without a `Next()` is right and recording it in D-121 is the right way to
stop a [[D-061]] review reopening it. `Define`/`TryDefine` is cited correctly against
`crud/sqlrepo/blueprint.go:74`/`:82`. The `Support` tri-state is genuinely the first in this tree.
The ~81-symbol breach is counted and argued per `architecture.md`'s escape clause and the argument
holds: §INV-019 forces every seam type to be exported, so a smaller count is a smaller extension
point. All ten `## Carry into the plan` items have a matrix row, a section and a named artefact;
what they lack is an executable existence proof for the seven that live in conformance sections,
which is GAP-125.

---
## Round 2 — dispositions — 2026-09-07

All fourteen round-1 findings are **closed** in `plans/EVENTSOURCE_P1_PLAN.md`.
One repair is **rejected on the record** and its finding closed a different way:

| Finding | Disposition |
|---|---|
| GAP-110 `[critical]` | closed — `Compose` renders escape-and-join; the length prefix is withdrawn; frozen in **D-125** |
| GAP-111 `[high]` | closed — six derived ceilings, `MaxKeyBytes` 4096 → 2048, and **`MaxResidentBytes`** enforcing the product at both doors as a division |
| GAP-112 `[high]` | closed — admission at `Append` against committed + staged, read-your-own-writes, and `Tx.Commit` cannot report a conflict; all three stated on the **contract** |
| GAP-113 `[high]` | closed — positions assigned at **commit**, rollback advances the counter by what it staged, `MonotoneVisibility: Supported` re-derived, cursor encoding stated |
| GAP-114 `[high]` | closed — every named-test clause preceded by a `-list` count assertion; every checkpoint split into a phase-4 and a phase-5 block; S1 given five named tests |
| GAP-115 `[medium]` | closed — `scripts/extensions_test.go` holds the shared helpers, the newcomer's three test names are distinct, and the `go ` grep becomes a `go/ast` walk |
| GAP-116 `[medium]` | closed — thirteen rows repaired both ways with the disposition table pasted under the matrix; UC-058 moved to S4; INV-042's row drops S6; INV-011's S6 check is now planned |
| GAP-117 `[medium]` | closed — the fixture test is named as the proof, `check-surface` demoted to a change-detector, anti-vacuity demonstrated once |
| GAP-118 `[medium]` | closed — `Failure` normalises an out-of-range `Outcome`; `String()` renders no number; the map keeps an explicit `default:` |
| GAP-119 `[medium]` | closed — the kernel verifies ascending, dense, this-stream pages and ascending `ReadAll` positions; two defects added |
| GAP-120 `[medium]` | closed — the trust boundary derived, `Envelope.Type` and `Revision` bounded, a stored name's rendering rule stated, the decode path's gaps named in D-124 and `## Debt` |
| GAP-121 `[medium]` | closed — **the proposed repair is rejected** (§INV-031's own falsification forbids any store call before the short circuit); the ordinal and both consequences are stated, and §UC-030 uses `Repo.Authority(ctx)` |
| GAP-122 `[medium]` | closed — `helpers.go` → `proxies.go`, with the naming rule and its measurement in `## Architecture metrics` |
| GAP-123 `[low]` | closed — all four clauses |

No round-1 finding carried a `[deferred]` timing, so nothing moved into `## Debt`
on that account; the two new debt rows (`crud`'s unexported nil predicate, the
codec's unbounded decode path) come from GAP-123.3 and GAP-120.

---

## Round 1 — econv-plan-validator (clean context) — 2026-09-07

Sources read in full: `plans/EVENTSOURCE_P1_PLAN.md`, `usecases/EVENTSOURCE_P1_USECASES.md`
(§1, §2.3, §5 INV-018/021/022/023/024, §D.1, §D.6–§D.9, §D.13–§D.15, Group E),
`usecases/EVENTSOURCE_P1_RECONCILE.md` §3.1–§3.8, `gaps/EVENTSOURCE_P1_USECASES_GAPS.md`
rounds 9–10 and its `## Carry into the plan`, `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`,
`CLAUDE.md`, and the econv references. Every codebase claim below was re-derived from the tree at
`72e7d22`, not taken from the plan.

---

### GAP-110 [critical][immediate] `Compose` as specified returns bytes that the kernel's own key rule refuses, so §UC-050's happy path cannot execute — and the rendering it decides is frozen forever by §INV-005

- **Where:** PLAN.md `## Contracts` → `event/identity.go`, lines 359 and 362-365 ("`Compose`
  writes a 4-byte big-endian length before each part, which is
  `cache/address.go:38-43:NamespaceOf`'s mechanism"); PLAN.md `event/text.go` ("non-empty, valid
  UTF-8, no NUL, no control character … Applied to a key at four doors"); matrix rows UC-050 → S2
  and INV-033 → S2/S5.
- **What:** A 4-byte big-endian length prefix emits bytes in `0x00`–`0x1F` for every part shorter
  than 2^24 bytes — which is every part. `Compose("acme", "A-17")` produces
  `"\x00\x00\x00\x04acme\x00\x00\x00\x04A-17"`. That value is a `Key`. The kernel then applies its
  own text rule to that key at four doors: it contains NUL, it contains control characters, and it
  is not valid UTF-8 as a whole. **Every composed key is refused with `ErrKey` at `Fact.New`,
  `Fold`, `Load` and `Append`.** The `stream identity` conformance section (§D.9) independently
  asserts "a NUL is refused", so the suite would fail the framework's own recommended mapper.
  The misreading is traceable: [REC] §3.6 says NamespaceOf "writes a 4-byte big-endian length
  before each part **into the hash**" — the prefixed bytes are hashed and never become a string.
  I verified `cache/address.go:32-53`: the prefixed bytes go to `sha256.New()`, and the `Namespace`
  keeps a `[32]byte` digest plus the cloned parts. The plan dropped "into the hash" and made the
  intermediate byte stream the key.
- **Why this severity:** `critical`. §UC-050 is a `[happy]` use case, §D.1 tells every application
  to write `event.Compose(...)` "on day one even for a single-part identity", and the plan's own
  justification for exporting `Compose` is "making the correct rendering the shortest one to write".
  The shortest one to write does not work. Worse, the *choice of fix* is a wire format that §INV-005
  freezes for the life of every stream ever written: a length-prefix in decimal, a separator with
  escaping, a hex or base64 framing and a hash are four different renderings, and changing the
  answer after the first append orphans every stream (§D.1's own "the failure is quiet: every
  aggregate reads as version 0"). That is `gaps.md`'s "architectural decision guaranteed to force a
  rewrite".
- **Why this timing:** It is the contract of an exported function landing in S2, cited by the matrix
  under UC-050 and INV-033, and it is a frozen wire format. An implementer who discovers it at the
  first test will pick a rendering under pressure, in S2, with no ADR.
- **Close criteria:**
  - [ ] The plan states the exact byte-level rendering `Compose` produces, and that rendering
        satisfies `event/text.go`'s rule for every part list of legal parts.
  - [ ] The plan states, with the argument, whether the rendering is injective by construction
        (framing) or collision-resistant (hash), and what a store's operator sees in the key column.
  - [ ] `Compose`'s output at the `MaxKey` bound is priced: the framing overhead per part is stated
        so a deployment can size `MaxKey`.
  - [ ] A test in `event/` composes a part list containing an empty part, a part with the separator
        character (whatever it becomes), a part with a NUL, and a multi-byte UTF-8 part, and asserts
        (a) the result passes the kernel key rule, (b) two distinct part lists never render equal.
        The last assertion must fail if the framing is removed.
  - [ ] D-121 (or a fifth ADR) records the rendering as frozen and names what reopening it costs.
- **Status:** **closed** — round 2, in `PLAN.md`.
  The length-prefix rendering is **withdrawn**; the finding's reading of
  `cache/address.go:32-53` is correct and is now recorded in the plan so the
  misreading is not repeated. `Compose` renders **escape-and-join**: `%` → `%25`,
  `/` → `%2F`, every byte that is not part of the UTF-8 encoding of a non-control
  rune → `%XX`, parts joined with `/`. Injective **by construction** and
  reversible rather than merely collision-resistant, so a store's key column
  still shows the identity that produced it. Total over arbitrary `string` parts,
  because §D.1's mapper is infallible and `Compose` therefore may not return an
  error; the escape set is defined as the exact complement of `event/text.go`'s
  rule and reads the same predicate, so the two cannot drift. Overhead priced:
  `B + P - 1` bytes with nothing to escape, never more than `3B + P - 1`.
  `TestComposeRendersALegalKeyAndNeverCollides` (S2 checkpoint) drives the empty,
  separator, `%`, NUL, invalid-UTF-8 and multi-byte cases and asserts pairwise
  non-collision, with removing the `/` escape as its named falsifier. Frozen in
  **D-125**, a fifth ADR, which names what reopening it costs; Q2's numbering
  becomes D-121…D-125 and S6 grows the row.

---

### GAP-111 [high][immediate] Three of the five kernel ceilings have no derivation, and all five bound only the factors of a product the plan itself says is the bound — a conformant store may hand back a 4 GiB page

- **Where:** PLAN.md `## Contracts` → `event/bounds.go` (`MaxKeyBytes = 4 << 10`,
  `MaxBatchCount = 1024`, `MaxPageCount = 4096`); PLAN.md `## Architecture metrics` → Budgets,
  closing sentence ("**raising `MaxPayload` without lowering `StreamPage` and `MaxBatch` multiplies
  the memory one load or append can hold. The bound is the product.**"); PLAN.md `event/binding.go`
  ("its `Limits()` has no zero field and none above the kernel ceiling"); matrix INV-022 → S4,
  UC-011 → S4.
- **What:** Two separate defects in one file.
  1. **No derivation.** [SPEC] §D.13 derives every number it states: `MaxPayload` 64 KiB from
     `jobs.DefaultPayloadBytes`, the payload ceiling 1 MiB from `jobs.MaxPayloadBytes` (verified,
     `jobs/bounds.go:19`), the identifier cap 128 B from `jobs.MaxNameBytes` (verified, `:6`),
     `MaxBatch` 64 and `StreamPage` 256 from the product arithmetic, `MaxKey` 512 B from "keys are
     indexed". The plan introduces three numbers [SPEC] never states — `MaxKeyBytes = 4096`,
     `MaxBatchCount = 1024`, `MaxPageCount = 4096` — and derives none of them. `universality.md`
     names exactly this: "magic thresholds with no derivation, no config entry and no calibration
     note". They are not configurable by anything: a deployment whose aggregate identity is a 5 KB
     composite (a URL, a multi-part tenant/region/entity key through `Compose`'s framing) can never
     be served by any store, and the only remedy is to edit `event/bounds.go` — a **kernel file
     edited for an extension's deployment need**, which is the defining microkernel test failing.
  2. **The ceilings bound the wrong thing.** `Bind` checks each `Limits()` field against its own
     ceiling independently. A store publishing `MaxPayload: 1<<20, StreamPage: 4096, MaxBatch: 1024`
     passes every check the plan describes. One `ReadStream` page is then up to **4 GiB** and one
     `Append` up to **1 GiB** — against §UC-011's stated Observed ("memory in use is bounded by one
     page and by the state itself") and §D.13's 16 MiB. The plan states the product rule as advice
     to a deployment and then declines to enforce it, in the same document that adds a kernel check
     (C4) for a page longer than the store's own `StreamPage`. The cheap check (`len(page)`) is
     planned; the consequential one (`MaxPayload × StreamPage`) is not.
- **Why this severity:** `high` under `universality.md` (undeirved thresholds are `[high]` minimum
  and never deferred) and under `architecture.md` (a threshold breach with no written justification).
  Concrete failure: a phase-2 `eventpg` deployment sizes `StreamPage: 2048` for throughput on
  small events; a later application declares a 512 KB document fact under the same store; a single
  `Load` of a busy stream now allocates ~1 GiB per page per concurrent request and the process is
  OOM-killed, with `Bind` having certified the store as honest.
- **Why this timing:** `event/bounds.go` is S1 and every number in it becomes part of the frozen
  surface at S6's `make api` baseline. Raising a ceiling later is additive; lowering one is
  breaking for every store already published against it.
- **Close criteria:**
  - [ ] Each of the five ceilings carries a one-line derivation in the plan, in §D.13's form, naming
        what it is protecting and what a deployment does when it is wrong.
  - [ ] The plan decides whether the kernel enforces the **product** at `Bind`/`Read`
        (`MaxPayload × StreamPage`, `MaxPayload × MaxBatch`, `MaxPayload × MaxRead` against a stated
        memory ceiling) or whether §UC-011's Observed is narrowed in writing to "bounded by
        `MaxPayload × StreamPage`, which a deployment chooses". One of the two, on the record.
  - [ ] If enforced: the `bounds` conformance section gains a store whose factors are each legal and
        whose product is not, and it must be refused at **both** doors; the at-the-product control
        must be accepted.
  - [ ] The plan states what a deployment does when its identity legitimately exceeds `MaxKeyBytes`,
        and that answer is not "edit `event/bounds.go`".
- **Status:** **closed** — round 2, in `PLAN.md`.
  Both halves. (1) `event/bounds.go` carries a six-row table giving every ceiling
  its derivation, what it protects, and what a deployment does when it is the
  wrong number. `MaxKeyBytes` drops 4096 → **2048**, derived from the narrowest
  index tuple phase 2 targets less the family and the version; a deployment whose
  identity exceeds it digests it **in its own mapper** — `Compose(tenant,
  hex(sha256(url)))` — and never by editing a kernel file. `MaxBatchCount` = 1024
  is `MaxResidentBytes / jobs.DefaultPayloadBytes`; `MaxPageCount` = 4096 caps a
  page's ~120 B envelope headers at ~480 KiB. (2) A sixth constant,
  **`MaxResidentBytes = 64 << 20`**, is the ceiling on the **product**, and `Bind`
  and `Read` enforce all three products — `StreamPage`, `MaxBatch` and `MaxRead`
  each against `MaxResidentBytes / MaxPayload`, **written as divisions** so the
  check cannot overflow an `int` on a 32-bit build. `ErrWrongStore` at both doors.
  The `bounds` conformance section gains the each-factor-legal / product-illegal
  store and its at-the-product control, and `defects.go` carries it.

---

### GAP-112 [high][immediate] `eventmemory`'s transaction design is one sentence, and as written it makes §UC-022's `ErrConflict` unreachable on the transactional path and leaves two appends in one transaction undefined — neither is covered by any planned conformance case

- **Where:** PLAN.md `### event/eventmemory/`, line 909-912 ("Transactions stage per-`*Tx` and
  **re-validate the expected version at commit**; a rollback discards the staged work and **burns**
  the positions it would have taken"); PLAN.md S3 `Covers` (UC-022 admission, UC-029 rollback);
  [SPEC] §UC-022 Observed ("One caller gets a receipt; the other a **typed conflict** carrying the
  framework's existing conflict class"); [SPEC] §UC-028 Observed ("**two appends in one transaction**
  carry authorities that compare `Same`"); [SPEC] §D.9's `transactions` row, which lists join,
  refuse-non-transaction, mismatch, rollback-discards, two-live-transactions, two-`Within`-chained
  and the never-bound control — and **not** a second append, a re-load, or two concurrent
  transactions.
- **What:** Three semantics are decided by "stage and re-validate at commit" and none is written
  down.
  1. **Where a conflict is reported.** Two callers both load a stream at v3 inside their own
     transactions; both stage an append at `Expected: 3`; both `Append` calls return **nil**; the
     first `tx.Commit` succeeds and the second fails. The loser's error arrives from
     `eventmemory.Tx.Commit(ctx) error` — a store-package error, not `event.ErrConflict`, not
     produced by the kernel and not passed through the `Outcome` map at all, because the kernel is
     not in the commit path (§INV-027). A caller written to §UC-022 (`if errors.Is(err,
     event.ErrConflict) { reload and decide again }`) never takes that branch. A real SQL store
     reports the same conflict from `Append` (a unique violation on `(family, key, version)`,
     [SPEC] §D.14), so the two stores the phase ships and plans differ on the one observable
     §UC-022 exists to fix — and `eventtest` is supposed to be what makes them one contract.
  2. **A second append in one transaction.** §UC-028's Observed requires it. If stage-time
     admission compares `Expected` against the *committed* version only, the second append at
     `Expected: 4` is a spurious `Conflict`; if it compares against committed+staged, it works.
     The plan says which nowhere.
  3. **Read-your-own-writes.** A `Load` after an `Append` inside the same transaction either sees
     the staged events (SQL behaviour) or does not (a naive staging buffer). §D.5's two-decision
     shape and §UC-021 make the re-load an ordinary program. Nothing states the answer, and no
     conformance section drives it, so `eventpg` and `eventmemory` are free to differ and the
     suite will certify both.
- **Why this severity:** `high`. Item 1 is a stated `[edge]` use case producing a caller-visible
  refusal of the wrong class through a path with no test; items 2 and 3 are behaviour undefined for
  ordinary neighbouring input (`universality.md`'s last bullet). It is not `critical` only because
  no data is corrupted — the loser's events are correctly absent.
- **Why this timing:** S3 writes the design and S5 freezes the conformance vocabulary. A section
  added after `eventpg` ships is a section `eventpg` has already passed without.
- **Close criteria:**
  - [ ] The plan states, in S3, whether `eventmemory` admits at stage time against committed+staged
        (and therefore reports `Failure(Conflict, …)` from `Append`, matching a SQL store) or only at
        commit — and if the latter, what §UC-022's "typed conflict" means for a transactional caller.
  - [ ] The plan states whether a read inside a transaction sees that transaction's staged appends,
        as a property of the **contract** (§D.14's `ReadStream` bullet), not of one store.
  - [ ] The `transactions` conformance section gains three cases, each with its control:
        two appends in one transaction (asserting the second is admitted and the two authorities are
        `Same`); a `Load` after an `Append` in one transaction (asserting whichever answer the
        contract took); two concurrent transactions appending to one stream (asserting exactly one
        set of events survives and that the loser's refusal carries the sentinel the contract names).
  - [ ] `eventtest.Tx`'s `Commit` error contract is stated: what class of error a commit-time
        conflict is, and whether the suite may assert an `event` sentinel on it.
- **Status:** **closed** — round 2, in `PLAN.md`.
  `### event/eventmemory/` now carries a written design instead of a sentence, and
  the three semantics are decided at the **contract** level on `event/store.go`
  so `eventtest` certifies one contract rather than two behaviours. Admission is
  at `Append` against **committed + staged**, so a second append in one
  transaction is admitted at the version the first produced and §UC-022's
  conflict is `event.ErrConflict` **from `Append`** on every store — `eventpg`
  from the unique index on `(family, key, version)`, `eventmemory` from a
  per-stream claim. A read issued inside a transaction sees that transaction's
  staged appends (read-your-own-writes, the SQL behaviour, now contractual).
  `Tx.Commit` therefore **cannot report a conflict**; its only error is a use
  error of the store's own class, and the suite never asserts an `event` sentinel
  on one. `eventmemory` refuses a claimed stream **at once rather than waiting**,
  which is deadlock-free by construction; the contract promises the door and the
  class and explicitly not the timing (R12). Three `transactions` cases with
  controls are added to S5 and to S3's checkpoint.

---

### GAP-113 [high][immediate] `eventmemory` claims `MonotoneVisibility: Supported` while also burning stage-time positions; the two are jointly satisfiable only under a design the plan does not state, and getting it wrong silently skips events for a projector

- **Where:** PLAN.md `### event/eventmemory/`, lines 904-912 ("Capabilities: … `MonotoneVisibility:
  Supported` …", "a rollback discards the staged work and **burns** the positions it would have
  taken (§UC-029, §INV-009)"); matrix UC-037 → "S3 (**watermark cursor**) + S5 (`resumption`)" —
  the only place the word "watermark" appears in the plan, in a table cell, with nothing in S3's
  body; [SPEC] §D.9's `monotone visibility` row ("a committed event is immediately at or below the
  returned cursor") and its `resumption` row ("including with a writer committing out of position
  order"); §INV-035, §INV-036, §INV-034.
- **What:** "Burning the positions a rollback would have taken" requires positions to be **allocated
  when the append is staged**, before the transaction's outcome is known — otherwise there is
  nothing to burn. Stage-time allocation makes commit order differ from position order: transaction
  A stages positions 5-6, B stages 7, B commits, A commits. Between the two commits a `ReadAll`
  observes 7 and not 5-6.
  - If the cursor is a plain position watermark, a consumer that persists it after seeing 7 **never
    sees 5 and 6**. §INV-034 ("the two reads describe one set") and §INV-036 ("a walk tiles") are
    both false, and the failure is silent: a projection is permanently short two events.
  - If the cursor is a *safe* watermark (below the lowest in-flight position), then immediately
    after B commits, the committed event at position 7 is **above** the returned cursor — which is
    exactly what §D.9's `monotone visibility` section asserts must not happen. So
    `MonotoneVisibility: Supported` is a false capability claim, and the section that exists to
    catch it runs only because the store claimed it.
  The plan asserts four properties — burned positions, `MonotoneVisibility: Supported`, safe
  resumption, and a watermark cursor — and states no design under which all four hold. S3's body
  describes none of the locking, the position counter's ownership, or the cursor's encoding.
- **Why this severity:** `high`. The failure mode of the first branch is lost events in a consumer,
  which is `gaps.md`'s "wrong behaviour / data loss", and it is invisible: every read succeeds, no
  refusal is produced, and the two-transaction interleaving that produces it is the ordinary shape
  of §UC-028 under concurrency. It is the store the whole root module's tests run against.
- **Why this timing:** It is S3's core integrity design, it decides `eventmemory`'s capability
  report (which decides which conformance sections run at all), and the cursor encoding is a
  persisted value §INV-035 makes safe to store across restarts.
- **Close criteria:**
  - [ ] S3 states when a position is assigned (stage or commit), who owns the counter, and what
        "burned" means in terms of that answer.
  - [ ] S3 states the cursor's encoding and the exact rule that makes it a safe resume point in the
        presence of an open transaction that staged a lower position.
  - [ ] The plan re-derives `MonotoneVisibility` for `eventmemory` from that design, and either
        keeps `Supported` with the argument or drops it to `Unsupported` — in which case §D.9's
        `monotone visibility` section is reported *not certified* for the root module's only store,
        and that is stated in `## Debt` as a named uncovered section.
  - [ ] The `resumption` section's "writer committing out of position order" case is driven with
        two **transactions** (not two autocommit appends), and its control is the same walk with no
        interleaving. It must fail against a store that returns a plain high-water position.
- **Status:** **closed** — round 2, in `PLAN.md`.
  The four properties are made jointly true by moving position assignment to
  **commit**: under one store-wide mutex `Commit` assigns positions in staging
  order and publishes in the same critical section, so commit order **is**
  position order, nothing can commit below the newest position, and the newest
  position is the watermark §UC-037 describes — `MonotoneVisibility: Supported`
  is kept, re-derived rather than asserted. §UC-029's burn is preserved without
  stage-time allocation: `Rollback` **advances the counter by the number of
  records it staged**, so the gap stays, the second append's position is higher
  and nothing is ever reissued. The cursor's encoding is stated —
  `"<log fingerprint>:<newest position, decimal>"`, the fingerprint minted once in
  `NewLog` and shared by every `Store` over that `*Log`, which is what makes
  §UC-053's foreign cursor and §INV-035's across-store-values resumption both
  work. The `resumption` section's out-of-position-order writer is driven by the
  **stage-time-allocating defect store**, because `eventmemory` by construction
  cannot commit out of order — stated in the matrix cell for UC-037 so the case
  is not read as vacuous.

---

### GAP-114 [high][immediate] Every checkpoint's "real evidence" is a `go test -run <names>` that exits 0 when none of the named tests exists, so a section can be marked `[x]` on a vacuous green

- **Where:** PLAN.md checkpoints S2, S3, S4 and S5, each of the form
  `go test -race -count=1 -v -run 'TestA|TestB|…' ./event/…`; S1's checkpoint
  (`go test -race -count=1 ./event/`, no named test at all, while S1's `Covers` claims INV-012,
  INV-016, INV-024, INV-025, INV-026, INV-028, INV-030, INV-045); ECONV SKILL.md ("`[x]` without an
  executed checkpoint is a lie written into an artifact"); `restrictions.md` §10.
- **What:** Verified in this tree:
  ```
  $ go test -count=1 -run 'TestThisNameDoesNotExistAnywhere' ./errs/
  ok  github.com/frostgrove/vv/errs 0.001s [no tests to run]
  $ echo $?
  0
  ```
  A `-run` pattern that matches nothing is a **pass**. Every named-test checkpoint in this plan is
  therefore green before a single one of those tests is written, and green forever if a name is
  misspelled or a test is later renamed (nothing in this repository greps `.agents/artifacts/` —
  `scripts/docs_test.go:49:TestEveryTestNameTheDocsCiteExists` reads `docs/` only, which I verified).
  The ECONV chain makes this concrete rather than theoretical: phase 4 implements section N and
  phase 5 writes its tests, so at the moment the section's checkpoint is executed the named tests do
  not exist. S1 is worse: it names no test and its "real evidence" is a `go list -deps` line, which
  proves the import graph and nothing about the eight invariants S1 claims to cover.
- **Why this severity:** `high` — `gaps.md`'s "missing test for a stated invariant", applied to
  the mechanism by which every invariant in this plan is supposed to be proved. The concrete failure
  is a phase-4 report stating S2 green with `TestACodecThatDecodesIntoAReusedBufferIsCaught`
  (carry item 1, a blocking `[high]`) never having been written.
- **Why this timing:** It is the plan's proof mechanism; every section depends on it, and a
  checkpoint corrected after S2 has been signed off does not un-sign S2.
- **Close criteria:**
  - [ ] Every checkpoint that names tests asserts they **ran**: either `go test -run … -v` piped
        through a count assertion, or a preceding `go test -list '<same pattern>' ./pkg` whose output
        line count is compared against the number of names. A missing name must make the command
        non-zero.
  - [ ] The plan states, per section, which checkpoint clauses are executable at the end of phase 4
        (implementation) and which only after phase 5 (tests), so `[x]` at phase 4 cannot claim a
        test-backed invariant.
  - [ ] S1's checkpoint names the tests that prove the invariants S1's `Covers` claims (at minimum
        INV-024's pairwise ground-truth table and INV-016's `Equal`-never-`==` case), or S1's
        `Covers` list is reduced to what its checkpoint actually shows.
- **Status:** **closed** — round 2, in `PLAN.md`.
  The finding's measurement is reproduced in the plan, and both close criteria are
  implemented. Every clause that names tests is now preceded by
  `test "$(go test -list '^(<same pattern>)$' ./pkg | grep -c '^Test')" = N`, which
  is non-zero when a name is missing or misspelt (`go test -list` prints one line
  per matching test; both the pass and the fail measured in this tree). The
  patterns are **anchored**, because `-run`/`-list` match unanchored and a bare
  `TestClose` would also match `TestCloseIsIdempotent` — the same vacuity through
  the other door. Every checkpoint is split into a
  **phase 4 (implementation)** block and a **phase 5 (tests)** block, with the
  status rule stated once under `## Sections`: `[~]` with `MISSING: <section>
  tests` after phase 4, `[x]` only after phase 5. S1 gains five named tests for
  the invariants it can prove without a store (§INV-024's pairwise table,
  §INV-016's `Equal`-never-`==`, C3's context table, the out-of-range `Outcome`
  normalisation, §INV-025 over every `String()`), and its `Covers` says in words
  that §INV-018, §INV-021, §INV-030, §INV-045 and §INV-011 are contract clauses
  here and proved in S4 and S5.

---

### GAP-115 [medium][immediate] `scripts/event_test.go` as planned redeclares three symbols and two test names that already exist in `scripts/tenancy_test.go` — S6 does not compile, and the natural repair is either a scope violation or a silent rename of the plan's named evidence

- **Where:** PLAN.md S6 `Files` → "`scripts/event_test.go` — on `scripts/tenancy_test.go`'s model,
  three tests: `TestNoBaseSubsystemDependsOnTheOptionalExtension` …
  `TestNoEventPackageCostsMoreThanTheSeamItNames` … `TestTheExtensionDoesNothingWhenItIsMerelyImported`
  (the `^func init(`, `go func(`, `go <ident>` grep, **copyable verbatim**)"; the real file
  `scripts/tenancy_test.go:1` (`package scripts`), `:9` (`const extension = ".../tenancy"`),
  `:11` (`func listed`), `:28` (`func firstPartyDependencies`), `:37` (`func under`),
  `:49` (`func TestNoBaseSubsystemDependsOnTheOptionalExtension`),
  `:120` (`func TestTheExtensionDoesNothingWhenItIsMerelyImported`).
- **What:** `scripts/` is one Go package. Two of the three test names the plan gives are already
  declared in it, and the four helpers the "copyable verbatim" model rests on (`extension`,
  `listed`, `firstPartyDependencies`, `under`) are package-level and bound to the tenancy prefix.
  `go test ./scripts/...` — the first clause of S6's own checkpoint — fails with
  `TestNoBaseSubsystemDependsOnTheOptionalExtension redeclared in this block`. The three repairs an
  implementer reaches for are all bad: rename the new tests (the plan's named evidence for INV-011,
  INV-013 and INV-042 then does not match the plan), generalise `tenancy_test.go` (a file outside
  S6's declared list, and its own comment argues against a table over extensions — which the plan's
  `## Risks and refusals` correctly refuses), or parameterise the helpers (a real change to a
  passing check, unplanned).
  Secondary: the grep pattern is `go [a-zA-Z]` **unanchored**, so it matches the literal text "go "
  followed by a letter anywhere in any line of any `.go` file, comments included. `event/store.go`
  and `event/codec.go` are planned to carry the ownership and concurrency prose (C1, C7, C9); a
  sentence containing "go through", "go to" or "go with" turns INV-013's structural check red for a
  comment. That is how a structural check gets loosened on its first run, which the plan's own R6
  names as the failure to avoid.
- **Why this severity:** `medium` — nothing is unsafe and the compile error is immediate, but it
  lands on the section that carries INV-011, INV-013, INV-019 and INV-036 and it invites either a
  scope violation or a quiet divergence between the plan's named tests and the tree's.
- **Why this timing:** It is S6's file list and its checkpoint's first command.
- **Close criteria:**
  - [ ] S6 names test identifiers that do not already exist in `package scripts`, and names the
        helper symbols the new file declares (or states that the shared helpers are lifted into a
        third file, with that file in S6's list).
  - [ ] The grep pattern for the event arm is stated in a form that cannot match prose — anchored at
        the start of a statement, or applied to `go/ast` rather than to text, which
        `scripts/docs_test.go` already has the plumbing for and which S6 already plans to reuse for
        the mutable-state check.
  - [ ] `go test -count=1 ./scripts/...` is green with both files present.
- **Status:** **closed** — round 2, in `PLAN.md`.
  Both halves. A new `scripts/extensions_test.go` takes `listed`,
  `firstPartyDependencies` and `under` (which gains a `prefix` parameter), and
  `scripts/tenancy_test.go` — now in S6's file list — loses those three
  declarations and **keeps both test names**, because five documents cite them and
  `scripts/docs_test.go` fails on a cited name that does not exist. The newcomer
  takes distinct names: `TestNoBaseSubsystemDependsOnTheEventExtension`,
  `TestNoEventPackageCostsMoreThanTheSeamItNames`,
  `TestMerelyImportingTheEventExtensionStartsNothing`. The third is a **`go/ast`
  walk** — `*ast.GoStmt` and any `*ast.FuncDecl` named `init` — not the unanchored
  grep, which would have gone red on the words "go through" in the prose
  `event/store.go` and `event/codec.go` are planned to carry. `## Risks and
  refusals` records that this is deduplication and not the table over extensions
  it refuses.

---

### GAP-116 [medium][immediate] Fourteen rows of the coverage matrix name a section whose own `Covers` list omits them, and one of those — INV-042 in S6 — points at a source check S6 does not plan; UC-058 has no named test anywhere

- **Where:** PLAN.md `## Coverage matrix` against each section's `**Covers**` paragraph. Computed
  pairwise; the disagreements are:

  | Item | Matrix says | That section's `Covers` |
  |---|---|---|
  | UC-021 | S2 (`Fold`) + S4 | S2 omits it |
  | UC-028 | S3 (`Tx`) + S4 | S3 omits it |
  | UC-034 | S1 + S4 + S5 | S5 omits it |
  | UC-040 | S3 + S5 | S5 omits it |
  | UC-042 | S2 + S5 | S2 omits it |
  | UC-055 | S3 + S4 | S4 omits it |
  | UC-058 | S4 | S4 omits it — **and S3's `Covers` claims it instead** |
  | UC-067 | S2 + S4 | S4 omits it |
  | INV-001 | S1 + S5 | S5 omits it |
  | INV-011 | S1 + S6 | S1 omits it |
  | INV-018 | S1 + S4 | S4 omits it (this is C4's landing) |
  | INV-021 | S1 + S2 + S3 + S5 | S1 omits it |
  | INV-042 | S2 + S4 + **S6 (source check)** | S4 and S6 both omit it |

  (The matrix itself is complete: all 68 UC and all 45 INV appear exactly once, and the only items
  absent from every `Covers` list are the two withdrawn ones, UC-023 and INV-037. Verified
  programmatically against the spec's headings.)
- **What:** An implementer works from the section, not from the matrix. Two of these are more than
  bookkeeping:
  - **INV-042** ("the kernel retains no application value") is the load-bearing invariant of the
    redesign that replaced `View`. The matrix promises a **source check in S6**. S6's file list
    contains a mutable-state AST check for §INV-013 and nothing for INV-042, and S6's `Covers` is
    "INV-011, INV-013, INV-019, INV-036". So the invariant's third leg is promised and unplanned.
  - **UC-058** ("one append, two identical changes") is claimed by S3's `Covers` and by the matrix's
    S4 column, and no checkpoint anywhere names a test for it. It is a kernel behaviour (two
    identical `Change` values in one `Append`), so S3's claim is also wrong on the merits.
- **Why this severity:** `medium` — nothing is architecturally wrong, but this is precisely how a
  use case is quietly dropped between a matrix and a section, and the matrix is the artefact the
  ECONV done-checklist reads.
- **Why this timing:** The section lists are what phases 4 and 5 execute against, starting with S1.
- **Close criteria:**
  - [ ] Every matrix row and every section `Covers` list agree, in both directions; a script or a
        manual diff pasted into the plan showing zero disagreements.
  - [ ] INV-042's S6 source check is either a named file and a named test in S6's list, or the
        matrix row drops S6 and INV-042 is proved by S2+S4 alone with the tests named.
  - [ ] UC-058's section is decided (S3 or S4) and a named test appears in that section's checkpoint.
- **Status:** **closed** — round 2, in `PLAN.md`.
  All thirteen rows repaired and the disposition table pasted under the matrix, in
  both directions, with zero remaining. UC-021 and UC-042 into S2; UC-028 into S3;
  UC-034 and UC-040 into S5; UC-055 and UC-067 into S4; INV-001, INV-011, INV-018,
  INV-021 and INV-042 into the sections that omitted them. **UC-058 moved** out of
  S3's `Covers` into S4's, with `TestOneAppendCarriesTwoIdenticalChanges` named in
  S4's phase-5 checkpoint — the finding is right that S3's claim was also wrong on
  the merits. **INV-042's matrix row drops S6**: the source check was promised and
  unplanned, so it is proved by S2 and S4 with named tests, which is the second
  branch the close criteria allow. Separately, INV-011's S6 source check — also
  promised — is now a named file and test (`event/refusalmessages_test.go`,
  `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor`) with `Stream.String()` as
  its control.

---

### GAP-117 [medium][immediate] `check_surface` is presented as the executable half of §INV-019's zero-diff obligation and is neither sound nor complete for it

- **Where:** PLAN.md `### The defining test — can a second store be added with **zero** diffs to
  `event/`?` item 2 ("**`check-surface`, an arm of `make check` (S6).** … failing on exactly the
  change §INV-019 forbids"); matrix INV-019 → "S5 (compile-time) + S6 (`check-surface`) | S6";
  `scripts/modules.sh:134:api` (verified: `go doc -short` per package into `docs/api/surface.md`).
- **What:** §INV-019 forbids **a diff to any file under `event/`** being required to add a second
  store. A surface diff is a different predicate in both directions:
  - **Unsound.** A store can be admitted by changing `event`'s *unexported* internals — a widened
    `Outcome` switch, a relaxed key rule, a new field on an unexported struct, an added
    `errors.As` branch — with `go doc -short` output byte-identical. `check_surface` passes and
    §INV-019 was violated.
  - **Incomplete/noisy.** Any additive export unrelated to any store (a `String()` method, a new
    sentinel) fails `make check` and must be waved through by regenerating the baseline, which is
    exactly the "loosened under pressure" failure mode the plan's own R6 names.
  The plan half-acknowledges this by calling the git arm "what replaces (2)'s weaker half after the
  first tag" — but `git diff --stat <tag> -- event/` has the same two problems, since it also
  forbids every unrelated change to `event/`. The genuinely sound half is item 1 (the third-package
  fixture stores in `eventtest`'s own test package), and it is stated last and weighted least.
- **Why this severity:** `medium` — the obligation is real and the compile-time half does most of
  the work; what is wrong is that the plan names the wrong artefact as the proof and the matrix's
  checkpoint column points at it.
- **Why this timing:** It is the checkpoint the matrix names for the phase's defining architectural
  invariant, and it lands in S6 as an arm of `make check` that every later change must live with.
- **Close criteria:**
  - [ ] The plan states what `check_surface` actually proves ("the exported surface of `event/…` has
        not changed since the committed baseline; read the diff") and stops calling it a test of
        §INV-019.
  - [ ] §INV-019's checkpoint column names the compile-time fixture test
        (`TestATrivialStoreNeedsNoInternalAccess` and siblings) as the proof, with `check_surface` as
        a change-detector beside it.
  - [ ] The fixture test's anti-vacuity is stated: it must fail if any value on the store seam is
        made unconstructible from outside `event` — demonstrated once by unexporting one field and
        watching the third package fail to compile.
- **Status:** **closed** — round 2, in `PLAN.md`.
  The defining-test list is reordered and relabelled: (1) the third-package fixture
  is **the proof**, (2) `check-surface` and (3) the git arm are **change-detectors
  beside it**, and the plan now states what `check-surface` actually proves ("the
  exported surface of `event/…` has not changed since the committed baseline; read
  the diff") together with both of the finding's directions — unsound for an
  unexported widening, noisy for an unrelated export. §INV-019's checkpoint column
  moves from S6 to **S5** and names `TestATrivialStoreNeedsNoInternalAccess`. The
  anti-vacuity is stated and required in the report: unexport one field of
  `Envelope`, watch the third package fail to compile, restore, say so — a
  compile-only test never made to fail proves only that a package compiles.

---

### GAP-118 [medium][immediate] The `Outcome` → refusal map is called total and fixed, and is undefined for an `Outcome` value outside the seven named constants

- **Where:** PLAN.md `event/outcome.go` (`type Outcome uint8`, seven constants, `func Failure(outcome
  Outcome, cause error) error`); PLAN.md `event/errors.go` ("The `Outcome` → refusal map, total and
  fixed"); PLAN.md `## Microkernel classification` per-capability table ("`Outcome` → sentinel map |
  **kernel mechanism** | Total, fixed, and the only thing the kernel reads").
- **What:** `Outcome` is a defined `uint8`, so `event.Outcome(200)` is legal Go and
  `event.Failure(event.Outcome(200), err)` is constructible by any store or decorator — including
  by arithmetic on a value read from configuration, and including by a store built against a later
  `event` and vendored into an older tree. The map's rows cover the seven named values plus "any
  error that is not a `Failure`". An out-of-range `Failure` **is** a `Failure`, so it falls through
  every row. The kernel's behaviour is then whatever the switch's missing default happens to do —
  in Go, fall through to a zero value or a nil error, which on the append door is the difference
  between `ErrUncertain` and **returning nil for a write that never happened**.
- **Why this severity:** `medium`. It takes a store doing something unusual to reach, but the
  consequence at the append door is the worst one in the vocabulary (a silent success for an
  unwritten append) and the claim of totality is stated three times in the plan.
- **Why this timing:** It is one clause on `Failure`'s contract and one `default:` arm, in S1, on
  the frozen extension point.
- **Close criteria:**
  - [ ] `Failure`'s contract states what an out-of-range `Outcome` means. The safe answer is that
        `Failure` normalises it to `Unclassified` at construction, or that the map's default arm is
        the door's fail-safe default; either is fine, stated is not optional.
  - [ ] `Outcome.String()` is stated for an out-of-range value (§INV-025 — it must name the value's
        class, not carry data).
  - [ ] A test in `event/` drives `Failure(Outcome(200), errSentinel)` through `Append` and
        `ReadAll` and asserts the door's fail-safe default, with the seven in-range values as its
        control table.
- **Status:** **closed** — round 2, in `PLAN.md`.
  `Failure` **normalises** an outcome outside the seven to `Unclassified` at
  construction, so totality is a property of the constructor rather than a hope
  about callers; the map's switch still carries an explicit `default:` equal to
  `Unclassified`'s row as the second line of defence. `Outcome.String()` answers
  `"[outcome unclassified]"` for an out-of-range value and never renders the
  number, because a store's number is data (§INV-025). The map table gains the
  row. `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault` drives
  `Failure(Outcome(200), …)` through `Append` and `ReadAll` with the seven
  in-range values as its control table; S1 carries the normalisation half
  (`TestAnOutcomeOutsideTheVocabularyNormalises`) and S4 the two doors, both in
  their checkpoints.

---

### GAP-119 [medium][immediate] The kernel is planned to verify a page's **length** (C4) and not its **order or density**, so a store that returns events out of version order produces a silently wrong aggregate state

- **Where:** PLAN.md `### C4 — GAP-103` ("a page longer than the bound the store itself published is
  `ErrBackend` … Cost: one `len()` per page"); PLAN.md `event/repo.go` `Load`'s order ("(3) the paged
  `ReadStream`, each page folded and released"); [SPEC] §D.14's `ReadStream` bullet ("returns
  envelopes at **ascending versions** strictly above `after`"); §INV-009, §INV-008, §INV-006.
- **What:** The fold is order-dependent by construction — that is what an event-sourced state *is*.
  The kernel folds whatever sequence the store returned. A store (or a decorator, or a SQL statement
  that lost its `ORDER BY` when someone added an index hint) that returns
  `[v3, v1, v2]` produces a state that is wrong in a way no refusal reports and no later read
  detects: the version is right, the event count is right, the state is silently different. The
  planned kernel-side page verification checks the cheap property (`len(page) <= StreamPage`) and
  not the one that changes the answer. The check is two comparisons per envelope — strictly
  ascending, strictly above `after`, and (for a load from 0) dense from 1 — against the one `len()`
  the plan already accepted the cost of. `dense versions` and `conservation` are conformance
  sections a store runs *against itself* in its own test suite; they are not a runtime guarantee for
  a store a deployment did not write, which is precisely the store §INV-018's import-inversion
  argument is about.
- **Why this severity:** `medium` — it requires a defective store, and both stores this phase and
  phase 2 ship will be correct. It is reported because a wrong aggregate state with no error is the
  worst observable this subsystem has, because the plan already accepted the per-page verification
  cost for a weaker property, and because §INV-006's "no partially rehydrated state" is enforced for
  every other cause and not for this one.
- **Why this timing:** It is one clause in the same place C4's clause goes, in S4, and it changes an
  assertion in a conformance section being specified now.
- **Close criteria:**
  - [ ] The plan states whether the kernel verifies ascending, strictly-above-`after` versions per
        page — and if it does not, §INV-008/§INV-006 record it as an unpoliced store obligation in
        the same words §INV-033's injectivity is recorded.
  - [ ] If verified: the sentinel is named (`ErrBackend`, store class, beside C4's over-long page)
        and the `bounds` or `stream paging` section gains an out-of-order-page decorator to
        `defects.go`, with the count in `defects.go` recomputed (C8 makes the count a computed
        value, so this costs one row).
  - [ ] The at-the-boundary control exists: a correctly ordered page at exactly `StreamPage` is
        accepted.
- **Status:** **closed** — round 2, in `PLAN.md`.
  The kernel verifies order as well as length, and the clause is on
  `event/store.go` beside C4's: `ReadStream` pages must be **ascending, dense from
  1, and of the requested stream**; `ReadAll` pages must have **ascending
  positions**. Three comparisons per envelope, against one decode and one fold —
  and the plan says why the cheap check alone was the wrong trade. The sentinel is
  `ErrBackend`, store class, and it is added to the `Outcome`-map table as the one
  row the kernel raises rather than maps. `defects.go` gains the mis-ordered-page
  and foreign-stream-page decorators (inventory now **eleven**, enumerated in C8),
  and `stream paging`'s control is a correctly ordered page at exactly
  `StreamPage`. `Load`'s stated order names the verification as part of step 3.

---

### GAP-120 [medium][immediate] Bytes from the store are the one input treated as trusted: `Envelope.Type` and `Envelope.Revision` are unbounded and unvalidated, and D-124's threat-model argument for the smaller codec rests on that assumption without deriving it

- **Where:** PLAN.md `event/codec.go` ("the threat model is different and it is the argument: a job
  payload and a cache value are adversarial input, **an event payload arrives from this system's own
  append-only log** behind a byte cap the kernel applies"); PLAN.md `event/store.go` (`Envelope.Type
  string`, `Revision int`, no bound); PLAN.md `event/text.go` ("Applied to a key at four doors and to
  a **declared** identifier at declaration") — declared, i.e. never to a stored one; PLAN.md
  `event/errors.go` history class (`ErrUnknownType`, `ErrRevision`); §INV-025.
- **What:** Three assumptions are made and none is derived.
  1. **The log is not this system's own in general.** A store is any `event.Store` — a shared
     database another service also writes, a restored dump, a store behind a decorator, or in phase
     2 a PostgreSQL table with its own grants. §INV-018's whole argument is that the kernel does not
     trust a store's numbers; the codec argument trusts a store's bytes.
  2. **`Envelope.Type` has no cap.** `MaxNameBytes` is applied at declaration only, so a store may
     return a 1 MiB type string. It is then used for a map lookup and — per §INV-025's own
     allowance that a refusal *names identifiers* — is a candidate for the `ErrUnknownType` message.
     A stored type containing newlines and control characters rendered into an error that reaches a
     log is log injection; a 1 MiB one is a log line nobody wanted.
  3. **The decode side has no bound but `MaxPayload`.** The plan states `CanEncode`'s three limits
     (encode side, at declaration) and states that `event.JSON` has "**nothing else**". `jobs` caps
     decoded bytes (`MaxDecodedBytes`) and depth (`MaxPayloadDepth`); `event` caps neither, so the
     stated budget "the bound is the product" is true of the page and not of what the page decodes
     into.
- **Why this severity:** `medium` — `encoding/json`'s own `maxNestingDepth` bounds the worst case
  and the expansion factor from a capped payload is small, so this is not a remote crash. It is
  reported because D-124 is an ADR whose central paragraph is this threat model, and an ADR that
  asserts a trust boundary it does not derive is the artefact phase 2 will cite when it decides not
  to harden `eventpg`'s read path either.
- **Why this timing:** D-124's paragraph and the `Envelope`/`Type` contract clauses are S1 and S6,
  and the frozen struct is where a cap would live.
- **Close criteria:**
  - [ ] The plan states the trust boundary explicitly: what the kernel assumes about bytes and
        strings coming *out* of a store, and what it validates. "Our own log" is derived from
        something or replaced.
  - [ ] `Envelope.Type` gets a stated bound (the kernel's `MaxNameBytes` is the obvious one, since
        it is the cap a declared identifier passes) and a stated refusal when it is exceeded.
  - [ ] §INV-025's rendering rule is stated for a **stored** type name: it is data from a store, not
        a declared identifier, so either it does not travel in a message or it travels bounded and
        escaped. A test asserts a stored type containing a newline and 64 KB of text does not reach
        a rendered message unbounded.
  - [ ] D-124 states what the smaller codec does **not** bound on the decode path, so phase 2 knows
        what it is inheriting.
- **Status:** **closed** — round 2, in `PLAN.md`.
  The trust boundary is stated and derived rather than assumed: a store is any
  `event.Store`, so the kernel trusts its bytes and strings exactly as far as its
  numbers, and "our own log" is **withdrawn** as an argument. `Envelope.Type` is
  checked against the kernel identifier rule (`MaxNameBytes`, valid UTF-8, no NUL,
  no control character) before use and is `ErrUnknownType`, history class, when it
  fails — a name that cannot be an identifier can name no declared fact.
  §INV-025's rendering rule for a **stored** name is stated: it travels in a
  refusal only when it passed that rule, otherwise the refusal names the stream
  and the byte count. `Envelope.Revision` outside 1..`Revisions()` is `ErrRevision`
  as a bound rather than an assumption. `event/codec.go` and D-124 now say what
  the decode path does **not** bound — no `MaxDecodedBytes`, no `MaxPayloadDepth`
  — and `## Debt` carries hardening it as the change that retires the gap.

---

### GAP-121 [medium][immediate] The empty-append short circuit's position in `Append`'s six-step order is unstated, which decides both whether `ErrWrongStore` is reachable and whether a no-op decision inside a transaction can be compared with another subsystem's authority

- **Where:** PLAN.md `event/repo.go` ("**`Append`'s order, fixed, six steps**" … step 1 "Before the
  empty-append short circuit, so a forged `At[S]{}` is refused even when nothing would be written");
  PLAN.md `## Questions [SPEC] deferred` → GAP-53 ("An empty `Commit.Stream()` answers the token's
  stream … **`Authority()` is the invalid one**"); [SPEC] §UC-030 ("Two subsystems write in one
  transaction, and the claim is **checkable**"), §UC-020 ("a decision that changed nothing"),
  §UC-031, §INV-031.
- **What:** Step 1 is stated to run before the short circuit. Steps 2-6 are not placed relative to
  it at all, and two of them matter.
  - If the short circuit sits after step 1, then §UC-031's case (a token minted over backing A,
    presented to a repo over backing B) **succeeds** for an empty change list, returning a receipt,
    because step 5 never runs. A caller that composes "decide, then append whatever came back" gets
    a success from a wiring mistake whenever the decision was empty — and the same mistake with one
    change is `ErrWrongStore`. Behaviour that flips on the length of a list is the shape §UC-020
    exists to make ordinary.
  - `Commit.Authority()` on an empty append is decided as **the invalid authority**. §UC-030's whole
    point is that an application compares `event`'s authority with an auditor's to check that both
    writes are in one transaction. An operation that decides nothing (§UC-020's happy path) and then
    performs that comparison gets an invalid authority and a false negative — the framework reports
    "these are not the same transaction" for a correct program, which is the exact failure mode
    §D.6's deleted `BoundTo`/`WroteWithin` branch was rejected for ("failed on a correct no-op
    program").
- **Why this severity:** `medium` — no data is wrong; a correct program is refused or a wiring bug
  is admitted, both in the empty case only.
- **Why this timing:** It is an ordinal in a fixed order landing in S4, and `Commit`'s accessors are
  frozen with the contract.
- **Close criteria:**
  - [ ] `Append`'s order states where the empty-append short circuit sits relative to all six steps,
        with the argument for the cheap steps that still run (the backing comparison is one struct
        comparison and §INV-031's "touches no store" survives it).
  - [ ] The plan states what `Commit.Authority()` answers for an empty append **while a transaction
        is bound**, and §UC-030's comparison is stated to work for a no-op decision — or §UC-030
        records that an empty append cannot participate in the comparison, and says what a caller
        does instead.
  - [ ] A test asserts an empty append with a foreign-backing token, and an empty append inside a
        bound transaction whose `Commit.Authority()` is compared with a second append's, both with
        the one-change case as the control.
- **Status:** **closed** — round 2, in `PLAN.md`. **The proposed repair is
  rejected; the underlying holes are closed a different way, and the argument is
  in the plan** under `event/repo.go` → "Where the empty-append short circuit
  sits".
  *Rejected:* moving the backing comparison (step 5) or the transaction question
  (step 6) before the short circuit. §INV-031 states in terms that *"The one check
  it still performs is the token's own key"*, and its falsification is *"a
  recording store asserting zero calls"* — `store.Backing()` and
  `store.Transaction(ctx)` are calls on the store, so running either costs
  §INV-031 the falsification that makes "an empty append is free" a fact a caller
  may rely on. [SPEC] is the authority on shape and the plan may not narrow it.
  *Closed:* the ordinal is now stated — the short circuit sits between steps 1 and
  2 — **and both consequences are stated rather than discovered**: `ErrWrongStore`
  is unreachable for an empty append by design (the wiring mistake surfaces at the
  first append that could have done damage), and `Commit.Authority()` is the
  invalid one per §INV-031 and §UC-020. The finding's real defect — §UC-030's
  false negative on a no-op decision — is closed by the second branch its own
  close criteria allow: **§UC-030's comparison uses `Repo.Authority(ctx)`**
  (C10.5), which answers the store's question for the context and is valid
  whenever a transaction of this store's backing is bound, including for an
  operation that decided nothing. Tests: an empty append with a foreign-backing
  token asserting **success**, and an empty append in a bound transaction whose
  `Commit.Authority()` is invalid while `Repo.Authority(ctx)` compares `Same` —
  each with the one-change control.

---

### GAP-122 [medium][immediate] `event/eventtest/helpers.go` is a file name `architecture.md` hard-bans and that has no precedent anywhere in this repository

- **Where:** PLAN.md S5 `Files` (`event/eventtest/… helpers.go …`) and `## Architecture metrics`
  (`event/eventtest/helpers.go | 3 | 1 | 0 | 150`, the three being `RoundTrip`, `Keys` and
  `Families`); `architecture.md` "Explicit ban: the god object spread across files" ("Parts have no
  domain names: `*_helpers`, `*_utils`, `*_common` …") and its hard bans list.
- **What:** `find . -name 'helpers*.go' -o -name 'utils*.go' -o -name 'common*.go'` outside
  `test/` and `_examples/` returns **nothing** in this tree — every file in `crud/`, `jobs/`,
  `cache/`, `auth/`, `storage/` and `tenancy/` is named for what it owns. The three symbols in the
  planned file are not helpers: they are the suite's three **runnable proxies** for obligations the
  framework cannot check (round-trip fidelity, mapper injectivity, family distinctness), which is a
  responsibility with a name. Naming the file `helpers.go` on day one is how the file becomes the
  place the next three unrelated things go.
- **Why this severity:** `medium` — `gaps.md`'s "weak naming in a public API", cheap now and
  irreversible-by-inertia later. It is also the one place the plan's file list reads as "split
  because the package is big" rather than "split because these own something".
- **Why this timing:** It is a file name in S5's declared list and a row in the metrics table.
- **Close criteria:**
  - [ ] The file is named for what it owns (e.g. `proxies.go`) or its three symbols move into the
        section files that use them, and the metrics table is updated.
  - [ ] No file in `event/`, `event/eventmemory/` or `event/eventtest/` is named `helpers`,
        `utils`, `common`, `misc` or `base`.
- **Status:** **closed** — round 2, in `PLAN.md`.
  `event/eventtest/helpers.go` becomes **`proxies.go`** in S5's file list and in
  the metrics table, and `## Architecture metrics` carries the rule and the
  measurement: no file under `event/` is named `helpers`, `utils`, `common`,
  `misc` or `base`, and the `find` that returns nothing in this tree is quoted.
  The three symbols are named for what they are — the suite's runnable proxies for
  round-trip fidelity, mapper injectivity and family distinctness.

---

### GAP-123 [low][immediate] Four one-clause contract holes, each in text an implementer reads as the contract

- **Where and what:**
  1. **`Open(nil)` panics at `Bind`.** PLAN.md `event/binding.go`: "`func Open(store Store) *Binding`
     // starts nothing, **cannot fail**", and `Bind`'s four checks begin with `store.Backing()`. A
     nil `Store` — the ordinary shape when a composition root's constructor returned an error the
     caller ignored — reaches `Bind` and panics on a nil interface instead of returning
     `ErrWrongStore`, which is the sentinel §INV-022 assigns to every other dishonest store. One
     clause: `Bind` refuses a nil store with `ErrWrongStore`.
  2. **`ReadOnly` is a decorator with no `Next()`.** PLAN.md `event/reader.go`. `CLAUDE.md` states
     the rule without exception ("give any decorator you add a `Next()`", [[D-061]]), and [SPEC]
     §D.14's compatibility paragraph repeats it. `ReadOnly`'s whole purpose is that a consumer
     **cannot** assert its way back to the append surface, so it must be the one wrapper without
     one — and the plan does not say so. A reviewer applying [[D-061]] will read it as a violation,
     and an implementer under review pressure will add the `Next()` that defeats it.
  3. **`crud`'s nil predicate is restated in `event` and the duplication is not in `## Debt`.**
     PLAN.md `event/backing.go` correctly derives why (`crud/executor.go:507:isNilValue` is
     unexported — verified; `crud.SameDataSource` at `:562` answers `true` for two typed nils of one
     type — verified by reading it). The restatement is a second implementation of one predicate, and
     `## Debt` lists neither it nor the finding it implies against `crud` (export the predicate, or
     add `crud.ValidDataSource`), while it does list two other `crud`-side findings.
  4. **A count that is already wrong.** PLAN.md `### event/eventtest/`: "`cachetest`'s `t.Skip` at
     **five** sites is the shape §UC-044 refuses". `grep -c 't.Skip' cache/cachetest/suite.go`
     is **7** (lines 214, 250, 1508, 1674, 1697, 1715, 1721). C8's own finding is that a count in
     prose drifts; this one arrived drifted.
- **Why this severity:** `low` individually — none changes a behaviour, and 1 and 4 are visible the
  moment the corresponding line is written.
- **Why this timing:** 1 and 2 are clauses on contracts landing in S4 and S6's module page; 3 is a
  `## Debt` row; 4 is a number in a document the ADRs will cite.
- **Close criteria:**
  - [ ] `Bind`'s check list includes a nil store, with the sentinel named.
  - [ ] The plan states that `ReadOnly` is deliberately the one wrapper with no `Next()`, with the
        argument, and D-121 or the module page records it so a [[D-061]] review does not reopen it.
  - [ ] `## Debt` carries the restated nil predicate and the `crud`-side change that would retire it.
  - [ ] The `cachetest` skip count is corrected or the sentence stops carrying a number.
- **Status:** **closed** — round 2, in `PLAN.md`. All four.
  (1) `Bind`'s check list becomes **five** and begins with the nil store — a nil
  interface or a nil pointer inside one, `NewBacking`'s own predicate applied one
  level out — refused with `ErrWrongStore`; `Read` refuses a nil `Log` the same
  way. `Open` still cannot fail. (2) The plan states that `ReadOnly` is
  deliberately the one wrapper in this repository with no `Next()`, with the
  argument ([[D-061]] exists so a capability is not lost by accident; removing one
  on purpose is this wrapper's job), and **D-121** records it so a [[D-061]] review
  closes rather than reopens it. (3) `## Debt` carries the restated nil predicate
  and the `crud`-side change that retires it — export `crud.ValidDataSource` over
  the existing unexported `isNilValue`, after which `event/backing.go` deletes its
  copy. (4) The `cachetest` skip count is corrected to **seven**, with the seven
  line numbers quoted so the next reader can recount.

---

## Verified and found sound

Stated so the absence of a finding is deliberate rather than unexamined.

**The ten `## Carry into the plan` items all land, and the three blocking ones land well.**
C1 (GAP-100) puts the obligation on `Codec[V]` where an implementer reads it, refuses the kernel
copy for the right reason, and — the part that makes it not a sentence — `Fact.RoundTrip`'s
interleaved second decode is a genuine runnable falsifier with `event.JSON` as its control, named in
S2's checkpoint. C2 (GAP-101) decides option (b) on the record, argues the store class from §2.3's
own row rather than adding a seventh class, records the three rejections, and — the half [GAPS]
asked for and I checked separately — states that an *unclassified* error from a decorator still
becomes `ErrUncertain`, with the paired conformance case that makes the obligation visible. C3
(GAP-102) derives `false` from §UC-057's asymmetry rather than asserting it, places the mechanism in
one place (the opaque cause wrapper, `cache/errors.go:26:opaqueError`'s shape — verified, and its
`Is` does reach the cause, which is why the override is needed), and drives both halves in both
windows. C4–C10 each carry a section and a named artefact; C8's move of the defect inventory from
five prose sites into `defects.go` with a computed count is the right shape and closes GAP-107
properly.

**Coverage arithmetic.** All 68 UC and all 45 INV appear exactly once in the matrix, matching the
spec's headings exactly (computed, not eyeballed); both withdrawals are present and forward.
Every live UC and INV appears in at least one section's `Covers`.

**Codebase claims.** Every source citation I checked resolves at `72e7d22`:
`jobs/queue.go:808:normalizeSenderError` and its `err.(rejectedPlacement)`; `jobs/json.go:35:jsonCodecFor`
and `:1062:discover`; `jobs/upcast.go:71:upcastOwned` and its recover; `jobs/identity.go:validRegistryName`;
`jobs/definition.go:341:normalizeUpcasters`; `jobs/catalog.go:25:NewCatalog`; `jobs/durability.go:81:Failure`
and `:223`'s all-`bool` `Capabilities`; `storage/types.go:211`'s all-`bool` `Capabilities`;
`crud/sqlrepo/blueprint.go:74:Define`/`:82:TryDefine`; `crud/executor.go:441:bindingFor`, `:507:isNilValue`
(unexported), `:562:SameDataSource` (returns `true` for two typed nils, `false` for a nil interface or a
non-comparable value — so `Valid()` as defined does the job); `crudsql.Transaction`'s two bare type
assertions; `cache/errors.go:26:opaqueError`; `cache/cachetest/suite.go`'s `*testing.T` in a non-test
file; `errs.TooLarge`/`errs.KindTooLarge`; `crud.ErrConflict`/`ErrUnavailable`. `jobs.MaxPayloadBytes`
is `1 << 20` and `jobs.MaxNameBytes` is `128`, as the plan says.

**Structural checks.** I ran the plan's file list against `scripts/checks.sh` mentally and where
possible for real. `check_deps` filters on `{{if not .Standard}}`, so `testing` in
`event/eventtest/*.go` costs nothing (Q9 is right). `check_tiers` does not touch `event` — it is not
in `TIER0`, and `crud`'s `TIER0_STDLIB` membership constrains `crud`, not its importers. `check_utils`
gains one safe row. `check_triplets` is untouched and the plan's refusal to add an `event` row is
argued correctly. `check_replaces`/`check_workspace` are untouched because no module is added.
S1's `go list -deps` expectation is right: I measured `./crud` → `utils, crud` and `./errs` → `errs`,
so `./event` must print exactly those four lines, against `./jobs`'s five and `./tenancy`'s three.

**Microkernel.** I walked every value on the store seam for out-of-package constructibility and
found none that requires `event`-internal access: `Stream`/`Record`/`AppendRequest`/`Envelope`/
`Capabilities`/`Limits` are plain structs with exported fields; `Key`/`Version`/`Position`/`Cursor`
are defined types over `string`/`uint64` and therefore convertible; `Backing`, `Authority` and the
error channel have exported minting constructors. The kernel imports no store package, has no
registry, no type switch and no `if name ==`. The only kernel file I found that a second store's
*deployment* could force a diff to is `event/bounds.go` — reported as GAP-111.

**Sections.** Each section builds on the previous one's types only, and `go build ./...` does pass
after each as ordered: S1's interfaces need no implementation, S2's declaration seam needs no store,
S3's `eventmemory` needs only S1, S4's caller seam needs S1–S2, S5 needs S4. The ordering argument
(`eventmemory` before the caller seam so S4 is exercised against a real store) is sound, though it
sits oddly beside the metrics table's "`Repo` needs one fake `Store`".

**Not findings, checked and dismissed.** The `Define`/`TryDefine` answer to Q21 is right and cited
correctly. The `Support` tri-state is genuinely the first in this tree and the argument for it is
sound. `errors.As` rather than a type assertion is the right rule and `jobs` is the right cautionary
tale. The exported-symbol breach (~80 against a threshold of 7) is counted and argued in writing per
`architecture.md`'s own escape clause, and I agree with the argument: §INV-019 forces every
store-seam type to be exported, so a smaller count would be a smaller extension point. `Store`'s
eight methods are justified by the `Log`/`Store` split. The three `## Disagreements` are recorded
honestly and the plan implements the spec's shape in each, which is correct behaviour for a plan.
`## Debt` accounts for every deferred `[GAPS]` item: GAP-25 stays open, GAP-26/66/77/78/91/98/99 are
correctly described as closed rather than carried, and GAP-41 and GAP-53 are decided here and leave
the list. No item was dropped.
