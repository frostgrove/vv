# EVENTSOURCE P4 — the gate — VERIFY

**2026-09-12.** Every section ran one review and one fix round and nobody had
verified the fixes. This is that verification. Nothing in the repository was
edited to make anything pass: three mutations were applied, run, and restored,
and `git status --porcelain` is byte-identical to where it started.

---

## Verdict

**The phase's own work is GREEN. Two repository gates are RED at HEAD and both
are foreign to this phase's diff — reported, not fixed, and not softened.**

| | |
|---|---|
| Blocking findings re-checked by constructing the input they named | **40** — PLAN 12, USECASES 20, S1 5, S2 2, S3 6 (via Round 3's mutations, re-run), S4 2, S5 4, S6 2. Overlaps counted once per document. |
| Closed by a note and not by the code | **none** |
| Closures whose *named artefact* does not exist | **2**, both in `EVENTSOURCE_P4_PLAN.md`, both documentation drift — recorded below and appended to the backlog |
| Interleavings driven from outside the repository | **8 required + 11 more = 19**, all handled |
| Appendix nuances landed | ES-01 4/4, ES-02 4/4, ES-03 3/3, ES-04 5/5, ES-06 3 landed + 1 partial-by-record |
| `exactly-once` claimed anywhere | **no** — and a `scripts/` arm with a fixture control forbids it |
| `make check` | **RED** — `check-tidy`, 29 satellites, foreign |
| `make unit` | **RED** — one arm, `TestNoI18nPackageCostsMoreThanItsErrorSeam`, foreign |
| `make vet` · `make examples` · `gofmt -l .` · `make api` | green · green · silent · idempotent |
| Tagged live suite, twice | `ok … 115.249s` and `ok … 117.279s` |
| Unset `FROSTGROVE_EVENTPG_TEST_DSN` | fails, does not skip |

---

## 1. The two red gates, and where they come from

### `make check` — RED, exit 2

```
check-deps: ok
check-tiers: ok
check-utils: ok
check-triplets: ok
check-todo: ok
check-replaces: ok
./app/appfx is not tidy — run make tidy
…
./vvdb/dbpgx is not tidy — run make tidy
  diff current/go.sum tidy/go.sum
  @@ -6,6 +6,7 @@
  +github.com/jackc/pgx/v5 v5.10.0/go.mod h1:mal1tBGAFfLHvZzaYh77YS/eC6IX9OWbRV1QIIM0Jn4=
  …
make: *** [Makefile:18: check] Error 1
EXIT=2
```

Twenty-nine satellites, every one of them missing `/go.mod` hash lines. Traced
rather than attributed:

- `GOWORK=off go mod tidy -diff` in the **root** module exits 0 — the root is tidy.
- The untidy set includes `audit/auditpg`, `auth/rpc/authgrpc`, `crud/http/crudgin`,
  `test`, `utils/vvgoose` — modules this phase has no relationship with.
- `git diff --stat ce36e2d HEAD -- event/eventpg/go.{mod,sum}` shows the file was
  last touched by `56349ba` (*"feat: otel & i18n & audit log added"*); the same
  diff over `app/appfx` and `vvdb/dbpgx` shows the same line.
- The phase-4 working set contains **no** `go.mod` or `go.sum`:
  `git status --porcelain | grep -E 'go\.(mod|sum)'` is empty.
- The local toolchain is `go1.27.0-X:nodwarf5` against a workspace declaring
  `go 1.26.6`, which is the shape of the missing hash lines.

So: **the gate is red and stays red**, and the cause is the merged
audit/otel/i18n line plus a toolchain mismatch, not this phase. S3's Round 3
recorded the same reading against `ce36e2d`; this run confirms it independently
from the file history.

### `make unit` — RED, exit 2

```
--- FAIL: TestNoI18nPackageCostsMoreThanItsErrorSeam (0.09s)
    i18n_test.go:31: the i18n extension reaches github.com/go-json-experiment/json/jsontext
        outside its error seam and declared MessageFormat/CLDR ecosystem
FAIL	github.com/frostgrove/vv/scripts	136.276s
EXIT=2
```

One arm and no other; 61 packages `ok`. The subject is `i18n/cmd/vv-i18n`, four
files importing `jsonv2`/`jsontext`, arriving in `c5e7b62`/`939bcd9`.
`scripts/i18n_test.go` itself arrives in `56349ba`. Foreign to `event/`.

**Every phase-4 arm inside `./scripts/` passes when run by name:**

```
--- PASS: TestNoCommentInTheProjectionPackagePromisesExactlyOnce (0.01s)
--- PASS: TestNothingInTheProjectionPackageOpensATransaction (0.00s)
--- PASS: TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity (1.37s)
--- PASS: TestEveryFieldOfAPublishedSpecIsRead (0.00s)
--- PASS: TestNoExportedFunctionOrdersOrTakesTwoCursors (0.02s)
--- PASS: TestCursorIsNeverCompared (1.41s)
--- PASS: TestNoSnapshotAuthorityIsDeclaredOrPromised (0.00s)
--- PASS: TestEveryPublishedTopologyPredicateHasACaller (0.01s)
--- PASS: TestEveryDoorTakingACoverOrAnIdentityRefusesItsZeroValue (0.00s)
--- PASS: TestNoPackageOnTheProjectionPathCanDispatch (0.59s)
--- PASS: TestNoModulusIsAppliedToASequenceHash (0.00s)
```

---

## 2. Blocking findings: re-checked by constructing the input, not by reading the note

### 2.1 Driven by mutation — the fix reverted, the test run, the fix restored

Baseline recorded before anything moved:
`sha256sum event/projection/*.go event/eventpg/*.go | sha256sum` =
`0c40ab0e502239467bf20c7a81f7c63ab6629ba124e36c8bbb152524c0061fd6`.

| Finding | The fix, reverted | What was run | Result |
|---|---|---|---|
| **S6 GAP-1** [high] — the barrier gate hardwired open | `generation.go:273` → `Readiness{Reached: true, …}` | `-tags=integration -run '^TestTheBarrierTheCutoverAndTheRollback$' ./event/eventpg/` | **FAIL** — *"generation 2 stands at 8 under the barrier at 24 and its readiness answers {Reached:true Behind:0 Quarantined:0 Holes:0}, where a generation short of the barrier answers Reached: false and how far it still is"* |
| same, unit half | same | `./event/projection/` by name | **FAIL** ×3 — `generation_test.go:258` *"a generation whose lowest watermark is 800 reached a barrier at 900"*; `:611` *"a cutover to a generation one position short of the barrier answered <nil>"*; `:771` *"a rollback to a generation 400 positions behind answered <nil>"* |
| **S2 GAP-1** [critical] — the settlement keyed by `Spec.Name` | `pass.go:734` → `event.Track(this.spec.Checkpoints, this.spec.Name)` | `go test -count=1 ./event/projection/` | **FAIL** — `settlement_test.go:224` *"the row \"orders@2\" stands at advance 0 where this runner finished 5 pages"*, plus eight topology arms |
| **PLAN GAP-1** [critical] — the effect gate cannot suppress generation zero | `effect.go:141` → the `active != this.of.Generation()` return removed | `-run 'Effect\|Stage\|Suppress\|Generation\|Cutover\|Ownership' ./event/projection/` | **FAIL** ×5, including the closure's own test — `effect_test.go:887` *"the retiring projection staged [A1 A2 A3] after the row moved, and the read happens inside the transaction that commits its own advance"* |

After each restore, `git status --porcelain` diffed empty against the baseline
and the hash above came back identical. It is identical now.

### 2.2 Driven from an external module — the input each closure named

`/tmp/p4drive`, `module p4drive`, `replace github.com/frostgrove/vv => <this
checkout>`, exported surface only, `-race`, `GOWORK=off`. Nineteen scenarios,
all passing their own assertions (`exit 0`, no `!!` lines).

| Closure | Input constructed | Result |
|---|---|---|
| **PLAN GAP-1** [critical] | Ungenerated projection + `Effects` + `Generations` over an empty row; then cut over Ungenerated → 1; then read two more events | staged `[orders:a@1 orders:b@2]` before, **0 new rows** after. **CLOSED** |
| **USECASES GAP-1** [critical] — a split orphans the park | park `k0`, `Split` the whole space, run both children over later events of `k0` | `applied=[k1@2 k2@3 k3@4 k2@7 k1@6 k3@8]`, `queue=map[orders/poison-key:2]`, four `Holds` calls **inside the children's units**, `k0` never reached a handler. **CLOSED** |
| **USECASES GAP-2** [critical] + **S4 GAP-1** [critical] — a caller-supplied barrier / silence as a barrier | `CutoverSpec` has no `Barrier` field; cutover whose retiring cover holds no row; `Observe` over 3-of-4 members; `Observe` over an all-fresh cover; cutover on that origin | `ErrRetired: true` and the row unmoved; `ErrTopology: true` naming `orders@1#3.3`; all-fresh answers `At:0` with a nil error; the origin is refused. **CLOSED** |
| **USECASES GAP-3** [critical] — the park × effect interaction | park a sequence under a spec carrying `Effects`; redrive it with `Effects`; then an operator eviction | after the park: `[orders:good@2]` only; after the redrive: `[orders:good@2 orders:poison@1]`; the eviction staged **0**. **CLOSED** |
| **USECASES GAP-4** [critical] + **S5 GAP-1** — the lost fence | two live instances of one name, `Effects` on both, transactional read model and transactional outbox | `staged` = six rows, `duplicate effects: []`, `duplicates in the read model: []`. **CLOSED** |
| **USECASES GAP-5** — the §UC-169 control | row names 1 → both run; flip the row to 2 → both run again | *"row flipped to 2: generation 1 staged 0 new ; generation 2 staged 2 new -> [orders@2:a@3 orders@2:b@4]"*. **CLOSED** |
| **USECASES GAP-6** — rebuild by name reaches the capability ungated | a second `Spec.Name` with `Effects` + `Generations` | *"a rebuild spelled as a SECOND PROJECTION NAME staged: 2 new rows -> [orders-v2:a@1 orders-v2:b@2]"* — **the edge is real and open, exactly as the closure recorded it in writing.** Not a regression; see §5 |
| **USECASES GAP-7** — an absent parent read as `Fresh()` | `Split` over a projection with no row | `ErrTopology: true`, both readings named. **CLOSED** |
| **USECASES GAP-8** — the cover's arithmetic | `NewCover` with a gap; with an overlap | *"2.3 matches no member of this set"*; *"0.3 and 0.1 both match every key one of them matches"*. **CLOSED** |
| **USECASES GAP-9** — the opaque identity | `@`, `#`, empty, bracket, `\x00`, invalid UTF-8; four round trips; four strings `String` never produces | every one refused with `ErrSpec`; `orders`/`orders@2`/`orders#3.7`/`orders@2#3.7` round trip; `orders@0`, `orders#0.0`, `orders#`, `orders@02` refused. **CLOSED**. `NewIdentity("  ")` is admitted — the argued rejection in S1 GAP-3's closure, and the kernel takes it too |
| **USECASES GAP-10** — `Park`/`Redriver` take an `Identity` | `docs/api/surface.md` | `Park`/`Redriver`/`Letter`/`Claim` all carry `Identity`; `Generations` keeps a bare name, as stated. **CLOSED** |
| **USECASES GAP-11** — the rename collision | the package compiles; the surface renders `Park` (interface), `Letter` (struct), `ParkSequence` (const), `Spec.Park` (field) | **CLOSED** |
| **USECASES GAP-12 / GAP-13** — the park's tier | `ParkSequence` beside `AfterApply`; beside `Unchecked`; with no `Park` | three refusals at `New`, before any I/O. **CLOSED** — §INV-090 holds in every configuration that constructs |
| **USECASES GAP-14** — two redrives, one sequence | two `Redrive.Any` goroutines, handler gated on a channel so both are in flight | `[{Applied:0} {Applied:3}]`, `duplicates: []`, `out of order: []`. **CLOSED** |
| **USECASES GAP-15** — `State.Parked` cannot clear | park three, redrive completely, run a pass | `phase=following parked=0`. **CLOSED** |
| **USECASES GAP-16** — the second-pool boundary asserted, not measured | redrive whose unit binds nothing for `Destination`; then a non-transaction; then a loop under two transactions in one unit | three refusals, all before the handler; the letter stays at 3, nothing applied, nothing evicted; the loop halts on *"two commits"*. **CLOSED — falsified by consequence, as the closure said** |
| **USECASES GAP-17** — cutover atomicity without the reader's precondition | a reader resolves the row, a cutover commits, the reader resolves again | `1` then `2`; the first reader goes on reading the retiring generation. **CLOSED as a named window** |
| **USECASES GAP-18** — `Quarantined` never decrements | park, redrive completely, then cut over with **no** override | `Quarantined=3`, `Holes=0`, cutover `<nil>`. **CLOSED** |
| **USECASES GAP-19** — the widened `Checkpoints` conformance | `checkpointDefects` and the two counts tests | `TestTheDefectInventoryIsTheSizeItSaysItIs` and `TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt` both PASS; `event/eventpg/census_integration_test.go:86-87` carries `{"topology", certified}` and `{"topology handoff", certified}`. **CLOSED** (but see §4, D1) |
| **USECASES GAP-20** — `Split` under a twice-running unit | `Split` under a unit whose body runs twice | one refusal (*"was already retired by a split… children already hold rows"*) and **both child rows fresh at advance 0 after the rollback** — one split or a refusal, never two children at the origin. **CLOSED** |
| **S1 GAP-1** [critical] — `Cover`/`Partition` published and inert | four runners built from `cover.Partitions()` over one log | every key matched exactly one member; `applied` = 2×`keys`, `outOfOrder: []`, `duplicates: []`. **CLOSED** |
| **S1 GAP-2** — a partitioned set beside a live whole-space row | start `orders#0.1` beside a live `orders` | `phase=halted`, `ErrTopology`, *"records through a checkpoint row standing at advance 1 for a coarser share"*. **CLOSED** |
| **S1 GAP-4** — the zero `Cover` and the zero `Identity` | `Observe`, `Reached`, `Split`, `NewRedrive`, `Cutover` each handed one | six refusals, each naming the value. **CLOSED** |
| **S2 GAP-2** — the pre-split release redeployed | run the whole-space spec again after a `Split` | `phase=halted`, *"was retired by a split… The row \"orders#split\" records the retirement"*. **CLOSED** |
| **S3 GAP-1…GAP-6** | Round 3 already reverted each fix and watched the pinning test fail. Re-run here at HEAD unmutated | `./event/projection/` green; `RedriveSpec` renders with no `Classifier` in `docs/api/surface.md`; the call trace shows `Sequences [outside]`, `Park [in unit]`, `Holds [in unit]`, `Holes` only from `Reached`. **CLOSED** |
| **S4 GAP-2** — the read-target overlap window | driven above | named on `Generations`, on `Cutover` and on both module pages; not closed, and not claimed closed. **CLOSED as a named window** |
| **S5 GAP-2** — the blocking applier's staging arm | `c-parked-effect` exercises exactly that arm: a degraded projection's `unblockedPage` staging `good@2` while `poison@1` parks | **CLOSED** |
| **S5 GAP-3** — a redrive accepts a barrier | `NewRedrive` with `EffectsAfter: 3` | refused: *"a redrive drains letters a generation with no barrier parked — every one of them is owed its effect"*. **CLOSED by refusing the field** |
| **S5 GAP-4** — the barrier is a constant, not a recorded position | warm up generation 2 under a barrier of 4 (stages nothing), then restart it with the barrier dropped to 0 | *"the same generation restarted with the barrier DROPPED to 0 and staged: [orders@2:a@5]"* — **the documented consequence reproduces exactly**, and the doc block states it in capitals. **CLOSED by naming the constant** |
| **S6 GAP-2** — the three renames on neither module page | `docs/modules/{en,ru}/projection.md` | both carry the four-row table `Quarantines→Park`, `Quarantined→Letter`, `Failure.Quarantine→ParkSequence`, `Spec.Quarantine→Spec.Park`, plus the *"`Progress.Quarantined` keeps its name"* paragraph. **CLOSED** |
| **PLAN GAP-2…GAP-12** | every named test located and run | all present and passing; the one exception is §4 D2 |

Every test named in a PLAN closure exists where the closure put it:
`TestNoExportedFunctionOrdersOrTakesTwoCursors`,
`TestNothingInTheProjectionPackageOpensATransaction`,
`TestEveryFieldOfAPublishedSpecIsRead`,
`TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity` (all
`scripts/projection_test.go`);
`TestAnExpiredClaimAppliesEvictsAndReleasesNothing` (`redrive_test.go`),
`TestASplitWritesTwoChildrenAtTheParentsCursorInOneTransaction`
(`topology_test.go`), `TestACutoverTakesNoBarrierAndDerivesItsOwn`
(`generation_test.go`), `TestNewIdentityRefusesEveryNameTheKernelRefuses`
(`identity_test.go`),
`TestAnUngeneratedProjectionWithGenerationsStopsStagingAtTheCutover` and
`TestASecondProjectionNameWithEffectsStagesEveryHistoricalEvent`
(`effect_test.go`), `TestAGenerationDrainsBesideTheLiveOneAndTheRowsAgree`
(`event/eventpg/rebuild_integration_test.go`),
`TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt`
(`event/eventtest/checkpoints_test.go`),
`TestTheBlockingTestAndTheAdvanceAreOneCommit`
(`event/eventpg/park_integration_test.go`).

**No finding was closed by a note and not by the code.**

---

## 3. The interleavings, driven from outside this repository

`/tmp/p4drive` — its own module, a `replace` onto this checkout, exported surface
only. Nothing in `event/` is reached except through published names. The read
model, the park, the outbox and the ownership row are all **transactional in the
driver**, so a rollback is measured rather than assumed; the first run of the
claim-expiry case was rejected precisely because the driver's read model was not,
and the assertion would have been the driver's rather than the framework's.

### A claim expiring mid-batch — **handled**

```
three letters parked: map[orders/poison:3]
  >> the grant over "orders/poison" expired between letter 1 and letter 2
Retried={Sequence:orders/poison Applied:1 Left:2 Cause:<nil>}
  err = the letter "orders/poison" of "orders" was applied and could not be evicted,
        so its unit rolled back and nothing was applied:
        projection: this claim no longer owns the sequence it was granted: "orders/poison"
  ErrClaimLost: true
  applied: [poison@1]
  queue:   map[orders/poison:2]
  trace:   … Claim: [outside] | Sequence:… [outside] | Evict:… [in unit] | Evict:… [in unit]
```

Letter 1 committed. Letter 2's apply **and** eviction rolled back together. **No
`Release` in the trace** — the loser does not free the winner's sequence, which
is the whole of the framework-side suppression, and the driver's `Release` is
deliberately the idempotent spelling that would not have caught it. Nothing
dropped, nothing reordered, nothing double-applied.

### A partition count change with two events of one sequence in flight — **handled**

```
the whole-space runner applied [k0@1 … k5@6] (phase=following)
in flight: 6 events appended above the parent's cursor (advance=1 highest=6)
split: orders -> orders#0.1 and orders#1.1
  orders#0.1   advance=1 highest=6 applied=6 cursor==parent's: true
  orders#1.1   advance=1 highest=6 applied=0 cursor==parent's: true
  the parent's own row is true (fresh means retired)
after the children drained: applied=[k0@1 … k5@6 k0@7 k2@9 k4@11 k1@8 k3@10 k5@12]
  out of order: []      duplicates:   []
  every key matches exactly one member of the cover
```

`Progress.Highest` travels to **both** children (PLAN GAP-10's subject), the
lower takes the counts, the higher starts at zero. The pre-split release
redeployed halts with `ErrTopology`; a finer runner beside a live coarser row
halts too.

### A worker killed holding a claim — **handled, with the documented cost visible**

A panic out of the handler mid-drain: the panic reaches the caller, `Release`
runs on the way out (`Release:… [outside]` in the trace), letter 1 is applied and
evicted, letters 2 and 3 stay parked in order, nothing double-applied. A
cancelled context mid-drain: `"the unit carrying the letter … did not commit, so
nothing was applied and nothing was evicted: context canceled"`, the letter stays
parked. **Note for a real store:** the driver's `Release` ignores its context, so
it succeeded under cancellation; against a database it would fail and the grant
would stand until the application's clock expires it — which is exactly why
`Claim.Until` is published and why the `Redriver` contract states the duration
obligation.

### A parked sequence receiving later events while another continues — **handled**

```
first drain:  phase=degraded parked=1 applied=[good@2] queue=map[orders/poison:1]
after later events: phase=degraded parked=1
  applied: [good@2 good@4]
  queue:   map[orders/poison:3]
  out of order: [] ; duplicates: []
  checkpoint: advance=2 highest=5 applied=2 quarantined=3
```

The blocker **and both following events of that sequence** are parked without
reaching the handler; the other sequence keeps being applied; the scan checkpoint
advances past all of them and `Applied` (2) and `Quarantined` (3) are counted
separately, which is ES-03 nuance 1.

### A DLQ overflow — **handled**

```
phase=blocked parked=1 err=the park of "orders" has no room for the sequence "orders/second":
  projection: this park has no room for the letter this pass has to write: the queue holds 1 sequences and its bound is 1
  queue:   map[]
  applied: []
  checkpoint: advance=0 highest=0 applied=0 quarantined=0
after room was made: phase=degraded parked=2 applied=[third@3] queue=map[orders/first:1 orders/second:1]
```

`PhaseBlocked`, not halted and not skipped. The **whole unit rolled back —
including the letter that had already been written before the full one** — the
checkpoint stood still, and the same page was delivered again once there was
room. That is USECASES GAP-13's dissolution, measured.

### A cutover with a reader mid-query — **handled as documented, window and all**

```
a reader resolved the ownership row and got generation 1; it has not read its tables yet
the cutover committed while that reader was mid-query: the row now names generation 2
  a reader that resolves NOW gets generation 2
two operators cutting over at once: 1 winner, 1 refusal; the row names generation 2
  the loser was refused: … the row of "orders" holds 2 and not 1: conflict
```

One fenced write; one winner, one refusal. The reader that resolved first goes on
reading the retiring generation, which is the cached-resolution window the
`Generations` doc names and refuses to call atomic.

### A rebuild beside the live generation, then rolled back — **handled**

```
generation 1 is live and applied [a@1 b@2 c@3]
barrier observed from generation 1: {Projection:orders Generation:1 At:3}
cutover to a generation with no rows: … ErrRetired: true
generation 2 drained beside generation 1: readiness={Reached:true Behind:0 Quarantined:0 Holes:0}
  orders@1   advance=1 highest=3 applied=3
  orders@2   advance=1 highest=3 applied=3
cut over: the ownership row now names generation 2
rolled back: the ownership row names generation 1 again
generation 2 moved on; its barrier is now 6 … generation 1 highest=3
rollback to a generation that fell behind: … ErrTopology: true ; the row still names 2
  live read model rows:     [a@1 b@2 c@3]
  arriving read model rows: [a@1 b@2 c@3 a@4 b@5 c@6]
```

Two separate read models, two separate checkpoint rows, the barrier derived from
the retiring generation's own rows. The rollback is admitted while the old
generation is still current and refused **by the same arithmetic** once it has
fallen behind — which is ES-04 nuance 4 exactly.

### A rebuild attempting an effect — **handled in every shipped shape; one open edge**

```
generation 1, which the ownership row names, staged: [orders@1:a@1 orders@1:b@2]
a rebuild with Effects nil staged: 0 new rows
a rebuild at generation 3 HANDED Effects while the row names 1 staged: 0 new rows
Effects at generation 4 with no Generations: … a generation is exactly what admits two senders
the barrier observed from generation 1 is 2
generation 5, which the row NOW names, warmed up under a barrier of 2 and staged: 0 new rows
a rebuild spelled as a SECOND PROJECTION NAME staged: 2 new rows -> [orders-v2:a@1 orders-v2:b@2]
a redrive carrying EffectsAfter: … a redrive drains letters a generation with no barrier parked
```

Five shapes suppress. The sixth — a rebuild spelled as a second `Spec.Name` —
**stages every historical event**, which is USECASES GAP-6's admitted edge,
argued down in writing rather than checked, pinned by
`TestASecondProjectionNameWithEffectsStagesEveryHistoricalEvent`, and taught
against on the module page. Confirmed open, confirmed as recorded, not a
regression.

---

## 4. Appendix conformance — part 3, sentence by sentence

### ES-01 — durable subscriptions and an atomic SQL projection — **4/4 landed**

| Sentence | Verdict |
|---|---|
| *«выбранный SQL-профиль фиксирует effect + checkpoint одной transaction authority»* | **landed.** `Advance: InUnit` + `Unit` + `Destination`; `pass.go:932-957` resolves the destination inside the unit, requires `crud.IsTransaction`, mints `event.NewAuthority(tracker.Backing(), crud.KeyOf(executor))` and refuses unless `authority.Same(mine)`. Three tiers named: aligned, divergent, unresolvable. Driven: the divergent tier halts on *"two commits"*. |
| *«У текущего eventpg log читается вне write transaction… обработчик пишет effect/checkpoint в предоставленной SQL-транзакции»* | **landed.** `event/eventpg/read.go:110-130` step 7: *"a bound is minted … never while a transaction of this backing is bound … A walk inside a transaction therefore stops at the first gap it meets and stays there until the same cursor is walked outside one, which is where draining the log belongs."* |
| *«Курсор не проходит незавершённый append, `MAX(sequence)` не доказательство видимости»* | **landed.** The settled-watermark walk, `deliverable()` at `read.go:214` and `settledAt()` at `:232`, over `pg_snapshot_xmin(pg_current_snapshot())`. No `MAX()` anywhere in the cursor path. |
| *«Чужая БД/HTTP требуют своей идемпотентности»* | **landed.** `projection.Unchecked` is the written admission; the `Effects` contract puts the dial-out *"with its own retry and its own idempotency"* on whatever drains the stage. |

### ES-02 — parallel subscribers with order inside a sequence — **4/4 landed**

| Sentence | Verdict |
|---|---|
| *«application задаёт ключ последовательности по конфликтующей read model; aggregate ID подходит не для любой multi-stream проекции»* | **landed.** `SequenceBy(name, of)` is the application's; `ByStream()`, `Unordered()`, `OneSequence()` are the three named alternatives, and `ByStream` composes family+key rather than rendering the stream, with the reason (*"a four-way cover would put the whole family in one partition"*). |
| *«SQL commit проверяет актуальное поколение claim вместе с effect/checkpoint»* | **landed**, adapted deliberately. For the loop the claim is the checkpoint fence, presented **before** the handler under `InUnit` so a second instance blocks on the tuple and never applies the page (`claimed()`); for the redrive it is a first-class `Claim` token that travels on `Evict`, `Touch` and `Release`. [[D-133]] records that the loser takes turns rather than halting. |
| *«Изменение числа partitions требует согласованной передачи позиции, не замены `hash % N` на ходу»* | **landed.** `Partition` is `(id, mask)` with `mask = 2^k-1`; `Split` is Axon's `Segment.split`; no count is stored; `TestNoModulusIsAppliedToASequenceHash` holds it; the handoff is one transaction with the retirement recorded before the row is removed. |
| *«Внешний HTTP такой fence не защищает»* | **landed.** Answered in writing on `Effects.Stage` — the four rollback paths are enumerated and a lost fence is named as *"the expected outcome of a rolling deploy rather than an exotic one"*. |

### ES-03 — a DLQ that preserves causal order — **3/3 landed**

| Sentence | Verdict |
|---|---|
| *«parking и scan checkpoint атомарны; успешная applied-позиция считается отдельно»* | **landed.** `Park` runs in the unit, `presentSave` in the same one; `Progress.Applied` and `Progress.Quarantined` are two columns. Driven: `advance=2 highest=5 applied=2 quarantined=3`. |
| *«Очередь ограничена числом sequences/bytes; overflow останавливает затронутую partition, не пропускает событие»* | **landed.** Two bounds, per-sequence and per-queue, with the numbers left to the implementation (*"Axon's are 1024 and 1024"*) and one sentinel, `ErrParkFull`, for whatever bound it enforces — a byte bound is expressible through the same door. The framework's half is `PhaseBlocked`, no attempt consumed, the unit rolled back, nothing skipped. Driven. |
| *«Retry обрабатывает sequence по порядку; skip — явная операторская операция с отметкой неполноты проекции»* | **landed.** `Redrive.drain` walks the letters in insert order and stops at the first that fails again. The skip is the operator's own row removal; the mark is `Park.Holes` (*"queued now plus evicted-unapplied"*), which `Cutover` refuses on, with `AcceptQuarantined` as the deliberate override. Module page, en:356-359 and en:325-332. |

### ES-04 — rebuilding beside the running version — **5/5 landed**

| Sentence | Verdict |
|---|---|
| *«generation имеет отдельные данные, checkpoints и claims; catch-up продолжается до согласованного барьера»* | **landed.** The generation is in the rendered `Identity`, so the checkpoint rows, the park key and the runner name all carry it; the data is the application's own tables; `Observe`/`Reached`/`Barrier` are the barrier. Driven. |
| *«Переключение read target атомарно для заявленного набора таблиц»* | **landed with its precondition travelling with it.** One fenced `Activate`; atomic *"for a reader that resolves this row in the same snapshot as the tables it then reads"*, with the cached-resolution window named as legitimate-but-not-atomic in three places. |
| *«Старый worker не пишет в новое поколение»* | **landed structurally.** An old worker renders its own identity, so its row and its tables are its own; there is no path by which it addresses another generation's. |
| *«Rollback допустим, пока старое поколение поддерживается актуальным либо снова догнало историю»* | **landed.** Driven both ways: admitted while current, `ErrTopology` once behind. |
| *«Rebuild получает отдельный ресурсный бюджет»* | **landed.** `Spec.Pace`, contracted as a read throttle and **explicitly not** a concurrency cap, with the instruction to drop it before cutting over because it lengthens the overlap window. |

### ES-06 — a replay that does not resend — **3 landed, 1 partial by record**

| Sentence | Verdict |
|---|---|
| *«отдельные projection/effect handlers; rebuild не получает effect-dispatch capability»* | **landed.** `Handler` receives a `Batch` and holds no route to `Effects`; a rebuild is a spec with `Effects` nil. Driven: 0 rows. |
| *«Начальный backfill тоже имеет явную effect policy»* | **landed.** `Effects` nil is the default and the safe one; `EffectsAfter` is the explicit warm-up barrier, refused with no sink and refused beside `ParkSequence`. |
| *«При переключении поколения durable граница владения live effects не допускает двух отправителей»* | **PARTIAL, and the gap is named rather than hidden.** The boundary is durable and is one row read through the ambient transaction. What is **not** closed, and is written in capitals on `Generations` and in [[D-141]]: (a) it needs a `FOR SHARE` / `SERIALIZABLE` read the framework cannot enforce — at READ COMMITTED and REPEATABLE READ a concurrent `Activate` conflicts with nothing; (b) the retiring generation advancing **past the barrier** before the switch is Marten's accepted overlap and an operator closes it by draining; (c) the rebuild-by-name spelling is outside the gate entirely. `TestARetiredGenerationStopsStagingAtTheCutover` passes with **both** the positive arm and the two-senders control against live PostgreSQL. |
| *«Это не sandbox: произвольный HTTP внутри пользовательского projection callback запрещается его контрактом и проверяется тестом, не блокируется магией»* | **landed.** `TestNoPackageOnTheProjectionPathCanDispatch` walks `event/projection` and everything it reaches transitively for `net`, `net/http`, `os/exec`, with a fixture control that imports `net/http` and must be reported. PASS. The module page says in the same paragraph that a handler may still dial out. |

---

## 5. No exactly-once claim

`grep -rniE 'exactly.?once'` over `event/`, both `projection.md` pages, both
`eventpg.md` pages, D-140/D-141, FL-038/FL-042 and the two new examples returns
**no delivery claim**. Every hit is one of:

- a different subject — *"a statement reaches a driver exactly once"*, *"every
  section is reported exactly once"*, *"`Spec.Unit` runs the work exactly once"*;
- a bounded interleaving under a stated precondition —
  `effect_integration_test.go:228` (*"Under the documented locking read … staged
  EXACTLY ONCE. Under the plain read the same interleaving stages it twice"*),
  `partition_test.go:221` (four partitions **route** every event to one member);
- an explicit refusal of the reading — `FL-035:113,130`.

`at-least-once` is stated where a consumer reads it: `docs/modules/en/projection.md:101`
*"### 2. Delivery is at least once, in both modes"*, again at `:133`, `:191` and
`:464` (*"That is not a defect; it is what at least once means here"*), and in
the `ru` twin. It is **enforced**:
`TestNoCommentInTheProjectionPackagePromisesExactlyOnce` scans every comment in
the package and carries a fixture whose package comment promises it — PASS.

---

## 6. The gate, run in full

| Check | Result |
|---|---|
| `make check` | **RED, exit 2** — `check-deps`/`check-tiers`/`check-utils`/`check-triplets`/`check-todo`/`check-replaces` `ok`; `check-tidy` red over 29 satellites, foreign (§1) |
| `make unit` | **RED, exit 2** — one arm, `TestNoI18nPackageCostsMoreThanItsErrorSeam`, foreign (§1); 61 packages `ok` |
| `make vet` | `EXIT=0` |
| `make examples` | `EXIT=0` |
| `gofmt -l .` | silent |
| `make api` | `EXIT=0`, *"docs/api/surface.md regenerated"*, and **idempotent**: `diff` against the pre-run copy is 0 lines. The 13 908-line diff against `HEAD` is `c5e7b62`'s renderer rewrite, already absorbed by this phase's working tree |
| tagged suite, run 1 | `ok github.com/frostgrove/vv/event/eventpg 115.249s` |
| tagged suite, run 2 | `ok github.com/frostgrove/vv/event/eventpg 117.279s` |
| unset `FROSTGROVE_EVENTPG_TEST_DSN` | `FROSTGROVE_EVENTPG_TEST_DSN is not set, and this suite proves nothing without a database, so it fails rather than skipping.` `FAIL … 0.002s`, `EXIT=1` |
| `_examples/event-partitions` | runs: *"every key's events were applied in the log's order, and no key reached two partitions"*, *"the 5-partition cover drained the rest of the log and every key is still in order"* |
| `_examples/event-generations` | runs: *"generation 2 rebuilt beside it: 16 rows, reached=true behind=0 holes=0, and it staged nothing"*, *"a second cutover from 1 is refused on the fence"*, *"the retired generation applied 8 more rows and staged 0 more effects"* |
| external driver, 19 scenarios | `exit 0`, no failed assertion |
| tree after three mutations | `git status --porcelain` diffs empty against the baseline; `sha256sum event/projection/*.go event/eventpg/*.go \| sha256sum` = `0c40ab0e…061fd6`, the value recorded before the first mutation |

---

## 7. What this run found that the sections did not

Two documentation drifts. Both are `[medium]` under the delivery policy, both are
appended to `EVENTSOURCE_BACKLOG.md` under `## P4`, and neither is fixed here.

- **D1 — `EVENTSOURCE_P4_PLAN.md` contradicts itself on `checkpointDefects`, and
  one of the wrong statements is inside a ticked deliverable.** Line 1601 and the
  correction at 1611 say **14**, which is what
  `event/eventtest/checkpoints_test.go:277` ships. Line 573 still says *"5 → 8"*
  and line 3851 — a `- [x]` item — still says *"`checkpointDefects` is 8"*.
  PLAN GAP-3's closure note also says *"5 → 8"*. The code is right and the counts
  tests pass; the plan is what is wrong, in the one place a reader checks whether
  a deliverable was met.
- **D2 — the alignment seam PLAN GAP-11's closure names does not exist.** The
  plan at line 1893 and the closure both say *"S1 adds one seam to
  `harness_test.go` … `aligned(t)`"*. There is no `func aligned` anywhere.
  What shipped is the type `alignedCheckpoints` (`harness_test.go:211-222`) plus
  `(*stand).inUnit` (`:233`), which does the job the closure describes — the
  substance landed and the name in the record did not. The rest of that closure
  holds: the plan does say *"the alignment in an untagged test is fabricated"* in
  full at 1901-1909, §INV-091's matrix row does name **S6** alone for the
  commit-together half, and `TestTheBlockingTestAndTheAdvanceAreOneCommit` exists
  in `event/eventpg/park_integration_test.go`.

And one thing worth restating rather than filing, because it is open **by
decision** and a future reader will meet it: a rebuild spelled as a second
`Spec.Name` reaches the effect capability with no gate and stages every
historical event. It is driven above, it is pinned by a test, it is argued on the
record in USECASES GAP-6's closure, and the module page teaches
`Spec.Generation` instead. It is not a regression and it is not a survivor — it
is a stated limit of the guarantee.
