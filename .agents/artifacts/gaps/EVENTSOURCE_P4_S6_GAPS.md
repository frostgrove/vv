# EVENTSOURCE P4 — S6 (the live proof, the examples, the decisions and the gate) — GAPS

## Round 1 — econv-code-reviewer — 2026-09-12

**Both blocking findings are closed — see the two `Closed, 2026-09-12` blocks
below and the re-run gate at the end of this file.**

Two blocking findings. One is a **survivor**: the whole live suite stays green
with ES-04's central safety gate hardwired open, so §6.9's named arm proves the
half it waits for and not the half §UC-159 is about (GAP-1, driven — the full
tagged suite ran `ok` under the mutation). One is a **piece marked done that is
not in the tree**: [SPEC] §7.3's three renames, which the plan's S6 file list
repeats verbatim, appear on neither `projection.md` (GAP-2).

**Everything else this section claims about itself reproduces, and most of it
reproduces exactly.** The transcript is real, the manifest and surface arms are
honest and I settled both a second way, the two red arms are pre-existing
baseline that I confirmed on a pristine worktree rather than on the section's
word, and the five interleavings this section makes possible are handled — three
of them I drove myself rather than reading the tests that drive them.

---

## The pasted checkpoint output is real. Every line re-run at HEAD

```
gofmt -l . | wc -l                                                       0
go build ./...                                                           silent
go vet ./...                                                             silent
git diff --check                                                         empty

go test -tags=integration -list '^(the 26)$' ./event/eventpg/ | grep -c '^Test'        26
go test -race -count=1 -tags=integration ./event/eventpg/...   RUN 1     ok  117.171s
go test -race -count=1 -tags=integration ./event/eventpg/...   RUN 2     ok  115.087s
go test -race -count=1 -tags=integration ./event/eventpg/...   RUN 3     ok  119.168s
                                                    (RUN 3 with FROSTGROVE_EVENTPG_TEST_PSQL set)

go test -list '^(the five walks)$' ./scripts/ | grep -c '^Test'                        5
go test -race -count=1 -run '^(the five walks)$' ./scripts/                            ok  12.565s

go test -race -count=1 ./...            green everywhere but scripts/TestNoI18nPackageCostsMoreThanItsErrorSeam
make examples                           EXIT 0, both new examples build
make vet                                every module, silent
./scripts/checks.sh event-kernel        check-event-kernel: ok
diff -u .git/event_kernel_before_s6 scripts/event_kernel.sha256          empty
check-deps / tiers / utils / triplets / todo / replaces                  all ok
./scripts/checks.sh tidy                RED, 29 modules
```

The plan claims 115.951 s and 116.064 s; I got 117.171 s, 115.087 s and
119.168 s. Twice in a row, three times in fact, and it did not skip.

**The two reds are the two the section named, and I proved the baseline rather
than believing it.** `git worktree add --detach /tmp/vv-pristine HEAD` and
`./scripts/checks.sh tidy` there prints the **identical 29-module list** —
`diff -u` of the two lists is empty — and
`go test -run '^TestNoI18nPackageCostsMoreThanItsErrorSeam$' ./scripts/` is red
there too, on the same `github.com/go-json-experiment/json/jsontext` subject. No
stash was taken; nothing in the working tree moved to establish this.

**The manifest arm holds, and so does the stronger claim behind it.**
`event-kernel-baseline` records 152 files and diffs empty against
`.git/event_kernel_before_s6`; and the renderer-independent settlement —
`grep -v 'event/projection/\|event/eventtest/'` over
`.git/event_kernel_before_s1` against `scripts/event_kernel.sha256` — is **82
files each, byte-identical**. The kernel did not move across five sections.

**The `event` surface arm holds, settled the way the plan says to settle it and
independently of the plan's own commands.** `make api` in the pristine worktree
and the `awk` range over the result gives **232 lines**; the same range over the
delivered `docs/api/surface.md` gives 232, and `diff -u` is empty. `make api` in
the working tree is byte-idempotent, so the committed file is current. The plan's
explanation of the 232-vs-59 diff is confirmed at the source:
`git log -1 --format=%ci -- scripts/api-surface` is **2026-09-10**, after the
predecessor was recorded on 2026-09-09.

**Both examples run and print what the checklist says they print.**

```
$ cd _examples && GOWORK=off go run ./event-partitions
declared a cover of 4 partitions: [0.3 1.3 2.3 3.3]
orders#0.3   applied=6   highest=24  keys=3          (x4, one per partition)
every key's events were applied in the log's order, and no key reached two partitions
orders#3.3 became orders#3.7 and orders#7.7, both at the parent's exact cursor
a second split of the retired parent is refused: there is nothing left to hand down
the 5-partition cover drained the rest of the log and every key is still in order

$ GOWORK=off go run ./event-generations
generation 1 is serving: 16 rows, 16 effects staged, barrier at 16
generation 2 rebuilt beside it: 16 rows, reached=true behind=0 holes=0, and it staged nothing
the read target moved: Generations.Active answers 2, in one fenced write
a second cutover from 1 is refused on the fence: the row holds 2 exactly once
the retired generation applied 8 more rows and staged 0 more effects
the rollback is the same call with From and To exchanged: the row holds 1 again
```

**Kernel boundary is exact.** `git status --porcelain event/` lists nine modified
and ten new, all under `event/projection` and `event/eventpg`. Nothing outside
those two moved — `check-event-kernel: ok`, and the 82-file comparison above is
the same statement without a renderer in it. S6's own two files under `event/`
are `event/eventpg/projection_integration_test.go` and `rebuild_integration_test.go`,
both tests, both in the plan's file list. The `event/projection/*.go` files whose
mtimes are later than `.git/event_kernel_before_s6` (04:06) hash **identically**
to what that predecessor records, which is the mutation campaign restoring the
tree, exactly as the transcript says.

**Lifecycle is clean.** `TestMerelyImportingTheEventExtensionStartsNothing` PASS;
`grep -rn '\bgo func' event/projection/*.go` outside tests → the only hits are
three prose comments. No goroutine, none in a constructor, and the examples are
the host that starts them.

**The psql cross-check is live and not vacuous**, which is the one thing that
could have made "rows read in psql" a sentence rather than a measurement. With
`FROSTGROVE_EVENTPG_TEST_PSQL='docker compose exec -T postgres psql -U vv -d vv'`
the whole suite is green (119.168 s); with the command deliberately broken
(`-U nobody -d nowhere`) `TestASplitWithNoParentRowWritesNothing` **fails** on
three subtests naming the query it could not ask. The database is the right one —
`select current_database(), version()` → `vv|PostgreSQL 17.9` — and the suite
leaves no scratch schema behind (`eventpg_shared` only).

---

## The interleavings, driven rather than read

Three of the five I built and ran myself, in a throwaway
`event/eventpg/zzreview_integration_test.go` (deleted afterwards; the tree is
byte-identical to how I found it and `check-event-kernel` says so).

**1. A claim expiring mid-batch — handled, in both of its two moments.**

*Revoked inside the first letter's own unit* (`DELETE FROM …_claims` on a second
pool connection while the handler for `A2` is running):

```
retried={Applied:0 Left:3} err=… was applied and could not be evicted, so its unit
  rolled back and nothing was applied: projection: this claim no longer owns the sequence
read model = [A1]            queue = [A:2:<cause> A:3:- A:4:-]
second drain by the next claim = [A1 A2 A3 A4], Applied:3 Left:0
```

*Revoked between two letters* (the revocation fires while `B3` is applying, after
`B2` committed):

```
retried={Applied:1 Left:2} err=… ErrClaimLost
read model = [B1 B2]         psql prints [B1 B2]      queue = [B:3:- B:4:-]
second drain = [B1 B2 B3 B4], Applied:2 Left:0
```

Nothing dropped, nothing reordered, nothing double-applied, and the token really
does travel on the write rather than only on the read — an eviction refused after
a successful apply rolls the apply back with it. The `lost` suppression in
`Redrive.claimed` does what its comment says: the loser does not release the
winner's sequence.

**2. A partition count change with events of one sequence in flight — handled,
and I put a number on the window the backlog calls unstated.** The split lands
while the parent is mid-walk; the parent halts at its next save with
`ErrOvertaken`-free terminal wording, its row is gone, and the two children drain
from the cursor the split copied:

```
the parent halted after applying 60 of 240 rows
240 distinct payloads over a log of 240; 8 were applied more than once
REVIEW: split-under-a-running-parent re-delivery window = 8 of 240 envelopes applied twice
```

**Nothing was dropped.** That is at-least-once inside its own contract, and it is
backlog `## P4` item 6's "the window's size is a documentation question" answered
with a measurement rather than left open.

**3. A parked sequence receiving a later event — handled.** With `C2` parked and
`C3` arriving afterwards:

```
the handler was offered: [C1 C2 C1 C2 D1 D2]        (C3 never reached it)
read model = [C1 D1 D2]                              queue = [C:2:<cause> C:4:-]
after the redrive: [C1 D1 D2 C2 C3]                  (C2 before C3)
```

`D` kept flowing throughout. This is ES-03 nuance 2 — the blocking test is on the
sequence and the following events never reach a handler — held live.

**4. A cutover with a reader mid-query — handled, and the section drives it
better than I would have.** `TestTheCutoverSwitchesEveryTableAtOnceForAReaderInOneSnapshot`
runs a reader loop that resolves the ownership row and both of that generation's
tables in one `REPEATABLE READ` transaction, asserts the row counts `{3,5}` vs
`{7,11}` never mix, requires readers on **both** sides of the commit before it
will conclude, and keeps a third reader open across the switch to assert the
negative arm (it re-reads `active = 1` and that is correct until retirement).

**5. A rebuild while the live generation advances — handled.**
`TestARetiredGenerationStopsStagingAtTheCutover` holds a retiring pass open
between its ownership read and its commit, starts the arriving generation against
the same row, and issues the cutover across both — then asserts the `UPDATE`
**waited** and the envelope is staged exactly once, against a plain-read control
that leaves it staged twice and a no-ownership-row fixture in which both stage.
That is the round-2 recipe, and it is genuinely gated rather than scheduled.

---

## Mutations I ran

Four, each restored, tree verified byte-identical afterwards.

| Broken | Caught by | Verdict |
|---|---|---|
| `func Merge(held, taken event.Cursor) event.Cursor` planted in `event/projection` | `TestNoExportedFunctionOrdersOrTakesTwoCursors` — *"…Merge takes 2 cursors…"* | caught |
| `surveyed`'s partial-cover refusal (`held.recorded > 0 && held.recorded < over.Count()`) disarmed | `TestACutoverCannotBeHandedABarrier` — *"a barrier over a cover one member of which holds no row answered <nil>"* | caught |
| `switching`'s `ready.Holes > 0 && !spec.AcceptQuarantined` disarmed | `TestACutoverCannotBeHandedABarrier` — *"a cutover onto a generation holding a live letter was admitted"* | caught |
| `Readiness{Reached: held.lowest >= barrier.At}` → `Reached: true` | **nothing live.** `ok github.com/frostgrove/vv/event/eventpg 56.008s` | **SURVIVOR → GAP-1** — now caught by `TestTheBarrierTheCutoverAndTheRollback`, `FAIL … 114.098s` |

I also reproduced one of the plan's own six, to check the table is not a story:
`Partition.Matches` → `return true` fails
`TestFourPartitionsOverOneLogAndTheModulusControl` with the exact message the
plan quotes — *"the four partitions between them applied the whole log did not
happen"*. The table is real.

---

## Nuance conformance — ES-01, ES-02, ES-03, ES-04, ES-06

Read against `EVENTSOURCE_P4_STUDY.md` §§ES-01..ES-06 part 2 and part 4. I found
no dropped nuance that is not documented as not applying. The ones worth naming
because they are the ones that get dropped:

| Study nuance | Where it lives in the tree |
|---|---|
| ES-02/2 `hash % N` is corrupting, not inconvenient | `partition.go:16-27`, D-140 §1, `TestNoModulusIsAppliedToASequenceHash`, the live control |
| ES-02/3 only the first start chooses the count | `identity.go:162` `coarser()` + the resume-time ancestor walk; D-140 §"Only the first start" |
| ES-02/5 a merge of two halves is not `min(a,b)` | refused, **with both reasons** (D-140 §"A merge is refused"), and the surface walk that holds it |
| ES-03/1 the checkpoint advances over parked events | `Progress.Quarantined` + `Park.Holes`, module page §"Following is a statement about the last read" |
| ES-03/4 the redrive rotates by least-recently-tried and is not automatic | `Redrive.Any` doc + `candidates()` `ORDER BY min(tried_at)`; module page §"The redrive" |
| ES-03/5 the bound is two-dimensional and asked **per sequence** | module page §"Bound it in two dimensions, and ask per sequence", with Axon's 1024/1024 named |
| ES-03/7 + ES-06/7 a reset clears the DLQ | **needs no analogue**: the park is keyed by `Identity.Whole()` = `orders@2`, so a generation gets its own queue and the retiring one's letters stay under its own key. Documented at `projection.md:547-554`. |
| ES-04/4 a rebuild must be stricter than continuous execution | stricter by absence — `OnPermanentFailure` is `Halt` or `ParkSequence` and there is **no skip policy**, so Marten's `SkipApplyErrors = true` has no vv spelling to be asymmetric about; `Cutover` refuses on `Holes` |
| ES-04/5 D-131 vs blue/green | decided, not discovered: the [[D-131]] amendment, recorded **beside** the rule with the `Ignore`-one-release-earlier ordering, and the same shape extended to `Generations` |
| ES-06/1-2 replay-ness must be durable and end at a **recorded** position | **half-taken, and the half that is not is stated in the same breath** — `Spec.EffectsAfter` is a deployment constant, D-141 says so, obligation 3 on the module page says so, and `TestABarrierDroppedFromTheSpecRestagesTheWarmUpBelowIt` measures the cost |
| ES-06/3 suppression is the default, Marten's default is the safe one | D-141 §"Why the capability is a value rather than a mode" |
| ES-06/5 neither reference closes the two-sender window | closed, with the four cases it does **not** cover written as a section rather than a footnote |
| ES-06/6 the no-HTTP rule is a contract and not a sandbox | `TestNoPackageOnTheProjectionPathCanDispatch` with its `net/http` fixture control, said as "contract and not sandbox" in three places |

Binding decisions: I found nothing contradicting **D-128** (no `(writer_xid, position)`
tuple, no merge, `Highest` unredefined), **D-129** (no position→cursor anywhere;
the two surface walks hold both halves), **D-130** (no transaction opened in
`event/projection`; `TestNothingInTheProjectionPackageOpensATransaction` PASS),
**D-133** (the split's absent-parent refusal is argued *as* D-133's halt case),
**D-118** (`Stage` is a write in the caller's bound transaction, never a second
intent table), **D-092** (no goroutine under `event/`), **D-126** (the store
chooses no isolation level, and the locking-read obligation is pushed onto the
caller *with the failure spelled out* rather than assumed).

---

### GAP-1 [high][immediate] The whole live suite is green with ES-04's barrier gate hardwired open, and §6.9 claims §UC-159

- **Where:** `event/projection/generation.go:273`
  (`answered := Readiness{Reached: held.lowest >= barrier.At, …}`) and its one
  consumer `generation.go:447` (`case !ready.Reached: return behindTheBarrier(…)`);
  the arm that claims it is
  `event/eventpg/generation_integration_test.go:222-235` inside
  `TestTheBarrierTheCutoverAndTheRollback`; the plan's §6 item 9
  (`EVENTSOURCE_P4_PLAN.md:3409-3411`, *"§6.9, §UC-159, §UC-161, §UC-164"*);
  [SPEC] §UC-159's **Then** (*"`Reached` answers `Reached: false, Behind: n`
  while generation 2's minimum is below 900"*).
- **What:** with

  ```go
  answered := Readiness{Reached: true, Quarantined: held.quarantined}
  ```

  the entire tagged suite passes:

  ```
  $ go test -race -count=1 -tags=integration ./event/eventpg/...
  ok  	github.com/frostgrove/vv/event/eventpg	56.008s
  ```

  Not one of the twenty-six named live tests notices. The reason is the shape of
  the arm: it **waits** for `Reached` to become true and then cuts over, so a
  constant `true` makes the wait return on its first poll (that is also why the
  run drops from 116 s to 56 s) and every later assertion still holds. There is
  no live call anywhere that asserts `Reached == false`, no live assertion of
  `Behind`, and no live `Cutover` onto a generation that holds rows but stands
  below the barrier — `grep -rn 'Reached: false\|Behind' event/eventpg/*_integration_test.go`
  returns the two `waitFor` predicates and nothing else.
- **Why this severity:** ES-04's entire product claim is *"переключение read
  target … пока новое поколение не догнало историю"* — the read target is not
  pointed at a generation that has delivered less than the one it replaces. That
  is one comparison, in one expression, and S6 is the section whose whole mandate
  is to prove such things against a live PostgreSQL rather than against a fake.
  The concrete failure the live set would not see: an operator cuts over to a
  generation 400 positions behind, `Cutover` commits the fenced write, every read
  in production moves backwards by 400 positions of history, and the suite that
  is supposed to be the gate for exactly this reports `ok`. It is not a defect in
  shipped behaviour — the `event/projection` unit suite **does** catch it, on
  three named tests (`TestABarrierIsObservedAndReached`,
  `TestACutoverTakesNoBarrierAndDerivesItsOwn/the barrier is the split parent's
  own watermark`, `TestARollbackIsTheSameCallExchangedAndErrRetiredWhenTheRowsAreGone/a
  rollback to a generation that fell behind is refused by the same arithmetic`) —
  so it is a survivor in the section's declared coverage rather than a wrong
  system. That is what keeps it at `[high]` and off `[critical]`.
- **Why this timing:** S6 is the phase gate. A coverage claim that is wrong in
  the gate is the one place a wrong coverage claim cannot be deferred: the next
  thing that happens is the phase closing on the strength of it, and the line
  *"§6.9, §UC-159"* is what a future reader will trust instead of re-deriving.
  It is also three lines of test to close.
- **Close criteria:**
  - [x] Before the `waitFor(readiness.Reached)` in
        `TestTheBarrierTheCutoverAndTheRollback`, with generation 2 running and
        holding a row but standing below the barrier, a `Cutover` call asserts
        `errors.Is(err, projection.ErrTopology)` and the ownership row is
        unmoved — the same shape the `ErrRetired` arm above it already uses.
  - [x] The same test asserts one `Reached` answer with `Reached == false` and
        `Behind > 0` off the live rows, which is §UC-159's **Then** verbatim.
  - [x] Re-running the whole tagged suite with
        `Readiness{Reached: true, …}` in `event/projection/generation.go` is
        **red**, and the message names the generation and the barrier. Recorded
        in the plan's mutation-evidence table as a seventh row.
  - [x] `./scripts/checks.sh event-kernel` diffs empty against
        `.git/event_kernel_before_s6` after the restore.
- **Closed, 2026-09-12.** Reproduced before it was fixed: with
  `Readiness{Reached: true, Quarantined: held.quarantined}` at
  `generation.go:273`,
  `go test -race -count=1 -tags=integration -run '^TestTheBarrierTheCutoverAndTheRollback$' ./event/eventpg/`
  printed `ok … 1.138s`.

  The arm is written where the close criteria put it, and the state it needs is
  **driven**: the arriving generation's handler holds its second page open
  (`heldPage` in `event/eventpg/generation_integration_test.go`), and the case's
  page size is eight over a log of twenty-four so that page one has committed a
  checkpoint row under the barrier before the hold takes effect. While it stands
  there the case reads the row on a connection no projection is using, asserts
  `Reached == false` with `Behind == barrier.At - row.highest`, issues the
  `Cutover` and asserts `ErrTopology` with the ownership row unmoved; then it
  releases the hold and the original arms run unchanged.

  Falsified twice, each half alone:

  ```
  # the mutation, both halves live
  generation_integration_test.go:291: generation 2 stands at 8 under the barrier at 24
      and its readiness answers {Reached:true Behind:0 Quarantined:0 Holes:0} …
  # the mutation with the readiness assertion disarmed — the cutover alone catches it
  generation_integration_test.go:297: the cutover from generation 1 to generation 2,
      which stands at 8 under the barrier at 24, answered <nil> …
  ```

  The whole tagged suite under the mutation is now
  `FAIL github.com/frostgrove/vv/event/eventpg 114.098s`, where it was
  `ok … 56.008s`. After the restore `check-event-kernel: ok` and
  `diff -u .git/event_kernel_before_s6 scripts/event_kernel.sha256` is empty,
  which is what says `generation.go` came back byte-identical.

---

### GAP-2 [high][immediate] [SPEC] §7.3's three renames are on neither `projection.md`, in a section that is marked `[x]`

- **Where:** `docs/modules/en/projection.md` and `docs/modules/ru/projection.md`
  (both, entire file); the deliverable at `EVENTSOURCE_P4_PLAN.md:3551-3553`
  (*"six contract rows become nine; **the three renames written as renames with
  their reasons**; …"*); [SPEC] §7.3 at
  `EVENTSOURCE_P4_USECASES.md:3835-3840`, which states the reason in the same
  sentence — *"because a consumer meets each as a compile error"*; the rename
  table itself at `EVENTSOURCE_P4_USECASES.md:3172-3176` and
  `EVENTSOURCE_P4_PLAN.md:1353-1359`.
- **What:** the three renames are `Quarantines` (interface) → `Park`,
  `Quarantined` (struct) → `Letter`, and `Failure`'s `Quarantine` (const) →
  `ParkSequence`, plus the field `Spec.Quarantine` → `Spec.Park`. The pages
  document the *new* names correctly and completely — nine contract rows, the
  park section, the redrive section, six operator obligations — and mention the
  old ones **nowhere**:

  ```
  $ grep -rn 'Quarantines\b\|Spec\.Quarantine\b\|Failure\.Quarantine\|projection\.Quarantine\b' \
        docs/modules/en/projection.md docs/modules/ru/projection.md
  (no output)
  $ grep -n -i 'was renamed\|used to be\|previously called\|переимен' docs/modules/{en,ru}/projection.md
  docs/modules/ru/projection.md:276:  Миграция для такого длинного имени — переименование, то есть перестроение.
  ```

  and that one hit is about renaming a *projection name*, which is a different
  subject. `Progress.Quarantined` — the kernel field, which deliberately keeps
  its name and meaning — appears four times and is the only thing a reader
  grepping for the old vocabulary will find, which is the most confusing possible
  outcome: the one name that did **not** change is the only one on the page.
- **Why this severity:** this is a piece named in the section's own file list,
  inside a section marked `[x]`, and it is the deliverable whose rationale the
  spec spells out rather than assumes. The concrete failure: a consumer upgrades,
  meets `undefined: projection.Quarantines`, opens
  `docs/modules/en/projection.md` — the one page the repository's own contract
  says is the consumer's reference for exactly this
  (`CLAUDE.md`: *"Changed what a package can do, or an option's name or
  default → `docs/modules/<package>.md` — it is a consumer's reference and a
  wrong option name there is a compile error they hit and you did not"*) — and
  finds no trace of the symbol they are holding. The new name is discoverable
  only by reading 792 lines of prose, or by diffing `docs/api/surface.md`, which
  the repository says is *"a question for a person"* and not a migration guide.
  Four symbols changed and the migration note for them exists in three artifacts
  (`[SPEC] §7.3`, the plan §S3, the plan §S6) and in none of the two places it
  was required to land.
- **Why this timing:** it is the section that is the documentation gate, the
  renames are this phase's only breaking surface change, and the S6 checklist
  already leans on them (*"every symbol a doc names is declared where the doc
  says it is — **the renames are what makes this a real risk**"*). That check
  passes precisely because the pages carry only new names. Deferring it means
  the phase closes with the breaking-change note in planning artifacts that are
  not shipped documentation.
- **Close criteria:**
  - [x] `docs/modules/en/projection.md` carries a `Was | Is | Why` table with the
        three renames and `Spec.Quarantine` → `Spec.Park`, each row carrying the
        reason [SPEC] §7.3 gives, and the sentence that `Progress.Quarantined`
        keeps its name and its meaning.
  - [x] `docs/modules/ru/projection.md` carries the parallel table — the two
        guides are parallel by design.
  - [x] `grep -c 'Quarantines' docs/modules/en/projection.md` is non-zero and
        every occurrence is in the migration table.
  - [x] `go test -run '^(TestEveryTestNameTheDocsCiteExists|TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs)$' ./scripts/`
        stays green with the old names present — i.e. the table names them as
        removed rather than as declared somewhere.
- **Closed, 2026-09-12.** Reproduced before it was fixed: with the new section
  stripped back out, `grep -c 'Quarantines\|Spec\.Quarantine\|Failure.*Quarantine'`
  over both pages answers `0` and `0`.

  Both guides now carry **Four names changed with the park, and you meet each as
  a compile error** / **Вместе с парковкой изменились четыре имени…** — a
  `Was | Is | Why` table with `Quarantines → Park`, `Quarantined → Letter`,
  `Failure`'s `Quarantine → ParkSequence` and `Spec.Quarantine → Spec.Park`, each
  row carrying §5.2's reason, above the sentence that `Progress.Quarantined`
  keeps its name and its meaning **and that finding it there is not evidence the
  other four are still present** — which was the specific confusion this finding
  named. `grep -c 'Quarantines'` is 2 on each page and both occurrences are in
  that section.

  A walk holds it rather than a convention:
  `TestEveryNameTheProjectionPackageRenamedIsOnBothGuidesAsARename` in
  `scripts/docs_test.go` reads the four old names out of `event/projection` to
  confirm they are gone — by kind, because `Readiness.Quarantined` is a field
  that legitimately still exists — reads the four new ones to confirm it walked
  the right package, requires each pair on one line of **code spans** on both
  guides (`Quarantine` is a substring of the other two, so a text search answers
  yes for the wrong row), and requires `Progress.Quarantined` to be declared in
  `event` and named on both pages.
  `TestAPageThatDocumentsOnlyTheNewNamesIsReported` is the control. Run against
  the pre-fix pages it is red on all eight rows; the three existing doc walks
  stay green with the old names present.

---

## What I checked and found clean, with the numbers

- **The 26 named live tests exist**, by the plan's own ERE: `grep -c '^Test'` → **26**.
- **The five source walks exist and run**: `-list` → **5**, `-race` run `ok 12.565s`.
  `TestNoExportedFunctionOrdersOrTakesTwoCursors` reads **≥200** exported
  signatures and its fixture control fires;
  `TestNoModulusIsAppliedToASequenceHash` reads **18** non-test files of
  `event/projection` (`ls -1 event/projection/*.go | grep -v _test | wc -l` → 18,
  so the guard is exact) and its fixture control fires;
  `TestNoPackageOnTheProjectionPathCanDispatch` reaches **≥10** packages and its
  `net/http` fixture is reported.
- **Every test name D-140 and D-141 cite exists.** 16 of 16 for D-141, 12 of 12
  for D-140, resolved by `grep -rl 'func <name>('` across the tree; and the three
  doc walks (`TestEveryTestNameTheDocsCiteExists`,
  `TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs`,
  `TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex`) are green.
- **The decisions match the code, clause by clause on the ones I checked.**
  D-141's three-suppressors-cheapest-first is `effect.go:131-149` in that order;
  its "`Stage` is called with a non-empty slice or not at all" is the
  `len(owed) == 0 → return nil` guard; its four rollback paths are all reachable;
  its "gated on the capability being supplied and never on this projection's own
  generation" is `!absent(this.generations)` and not a generation comparison.
  D-140's five-step handoff, the both-children-read-first step, the recorded
  retirement, and `Split`'s mask arithmetic (`mask<<1+1`, `id + (mask^this.mask)`)
  all check out, and the arithmetic is Axon's `Segment.split` exactly.
- **`[[UC-032]] is not widened`** — `git status --porcelain docs/ai/usecases/` is
  empty. `FL-042` is registered against it in the flow index, which is registry
  bookkeeping and not a widening of its text.
- **The roadmap's open list really shrank.** All five appendix bodies are
  **removed** (`git diff | grep '^-### '` shows ES-01, ES-02, ES-03, ES-04,
  ES-06), `Статус: не выполнено` survives only on ES-05, ES-07, ES-08, ES-09, and
  `Roadmap.md` gained a paragraph naming what now ships including the 8.0× cost.
- **The conformance census is fourteen**, and `checkpointCensus()` names all
  fourteen with `eventpg` certifying every one — proved by the live run of
  `TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines`.
- **Backlog 69/70/71 are appended and honest**, including the measurement as an
  actual transcript line rather than a claim.

---

## Recorded to the backlog under `## P4`, not fixed

Entries 72, 73, 74 and 75. The first is the one worth reading: **the S6 gate
command as written does not set `FROSTGROVE_EVENTPG_TEST_PSQL`**, so every psql
cross-check the section's evidence rests on returns `asked=false` and is silently
not taken. The transcript ran with it set, and I re-ran with it set and green —
so the evidence exists; the artifact that a future run will follow does not ask
for it.

---

**Status: closed, 2026-09-12** — GAP-1 and GAP-2 both fixed, each reproduced
first and each left with a falsifier. Four `[medium]`/`[low]`/measurement items
stay in `EVENTSOURCE_BACKLOG.md` `## P4` as 72–75, untouched.

## Round 1 closure — the gate, re-run in full

```
gofmt -l . | wc -l                                                       0
go build ./...                                                           silent
go vet ./...                                                             silent
go vet ./event/...                                                       silent
git diff --check                                                         empty

go test -tags=integration -list '^(the 26)$' ./event/eventpg/ | grep -c '^Test'        26
go test -race -count=1 -tags=integration ./event/eventpg/...   RUN 1     ok  115.700s
go test -race -count=1 -tags=integration ./event/eventpg/...   RUN 2     ok  118.140s
                                        (both with FROSTGROVE_EVENTPG_TEST_PSQL set)

go test -race -count=1 ./event/...                                       all ok
go test -race -count=1 -run '^(the five walks)$' ./scripts/              ok  10.153s
go test -race -count=1 -run '^(the two rename walks)$' ./scripts/        ok   1.057s
go test -race -count=1 ./...            green everywhere but scripts/TestNoI18nPackageCostsMoreThanItsErrorSeam
make examples                           EXIT 0
make api                                byte-idempotent; the event section is 232 lines, unmoved
make vet                                every module, silent
./scripts/checks.sh event-kernel        check-event-kernel: ok
diff -u .git/event_kernel_before_s6 scripts/event_kernel.sha256          empty
check-deps / tiers / utils / triplets / todo / replaces                  all ok
make check                              RED on check-tidy, the same 29 modules

the mutation, re-applied over the closed section:
go test -race -count=1 -tags=integration ./event/eventpg/...   FAIL  114.098s
```

The two reds are the two this review already proved on a pristine worktree, and
neither moved. `make tidy` is **not** run: it would put 29 unrelated `go.sum`
files into this section's diff, which is the reason the first transcript reports
`check-tidy` red rather than repairing it.
