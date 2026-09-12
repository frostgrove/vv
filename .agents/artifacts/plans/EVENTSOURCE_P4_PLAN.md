# EVENTSOURCE PHASE 4 — THE PROJECTOR SUBSYSTEM: ES-01, ES-02, ES-03, ES-04, ES-06 — IMPLEMENTATION PLAN

**Status:** plan, phase 4 of the PostgreSQL event-sourcing roadmap.
**Written against:** [`EVENTSOURCE_P4_USECASES.md`](../usecases/EVENTSOURCE_P4_USECASES.md)
(UC-131…UC-192, INV-083…INV-106) and its
[GAPS file](../gaps/EVENTSOURCE_P4_USECASES_GAPS.md) — round 1 red, all four
`[critical]` and sixteen `[high]` closed 2026-09-09;
[`EVENTSOURCE_P4_STUDY.md`](../usecases/EVENTSOURCE_P4_STUDY.md);
[`EVENTSOURCE_REFERENCE.md`](../gaps/EVENTSOURCE_REFERENCE.md), whose three
**Reject** entries are binding and are not re-proposed; the nine appendices at
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md:760-862`, read in
Russian; the mechanism pages at the other end of every URL in them (Axon
streaming processors, the Axon sequenced DLQ guide, the Marten async daemon, the
Marten side-effects page, the Critter Stack versioned-projection post); the
shipped `event/`, `event/eventmemory/`, `event/eventtest/`, `event/eventpg/`,
`event/projection/`, `runtime/`, `crud/`; `scripts/checks.sh`,
`scripts/event_test.go`, `scripts/projection_test.go`, `scripts/docs_test.go`;
PostgreSQL **17.9**.
**Format precedent:** [`EVENTSOURCE_P3_PLAN.md`](EVENTSOURCE_P3_PLAN.md).
**[SPEC]** below means
[`EVENTSOURCE_P4_USECASES.md`](../usecases/EVENTSOURCE_P4_USECASES.md); a bare
`§` is a section of it. **[APX]** means the appendix block above.

**Delivery policy in force (2026-09-08).** Only `[critical]` and `[high]` block.
`[medium]` and `[low]` go to
[`EVENTSOURCE_BACKLOG.md`](../gaps/EVENTSOURCE_BACKLOG.md) under `## P4` and are
**left alone** — recording is scheduling, not forgiving, and a medium is not
fixed because it is quick. **A gate never proceeds silently red:** a section is
reported closed by its checkpoint's output, never by `go test` being green.
Phase 1 reported six sections closed on a green suite with 29 of 32 review
verdicts red; that is the failure this line exists to prevent.

Four obligations frame everything below, and all four are executable:

- **The `event` package itself does not move, and that is checked rather than
  promised.** §5.1 says `docs/api/surface.md`'s `event` section is byte-identical
  after this phase. `check-event-kernel` freezes the whole tree under `event/`
  outside `event/eventpg`, so the manifest **does** move — for
  `event/projection/`, `event/eventtest/` and (if anything at all)
  `event/eventmemory/`. Every section's `event-kernel-moved` allowed set below
  therefore excludes `^event/[a-z_]*\.go$`, and a top-level kernel file appearing
  in any moved set fails the section. That is §5.1 made into a command.
- **`eventtest.RunCheckpoints` gains two sections and three defects**, and the
  control that forces the third **does not exist and S2 writes it**. A section no
  defect breaks has no control, so every assertion in it can be deleted and every
  run stays green — but the test that holds that rule,
  `TestEverySectionIsNamedByADefectThatBreaksIt`
  (`event/eventtest/defects_test.go:71-88`), iterates `eventtest.SectionNames()`
  and `eventtest.Defects()`, which are the **store** suite's; its `guarded` helper
  (`defects_test.go:90-99`) is called from nowhere else in the package. The
  checkpoint suite's own `TestEveryCheckpointDefectIsReportedByItsOwnSection`
  (`checkpoints_test.go:248-272`) runs defect → section and never section →
  defect. So today nothing in the tree would notice a `topology` section whose
  body is `_ = this`. **S2 writes
  `TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt` over
  `CheckpointSectionNames()` and `CheckpointDefects()`, and that is what forces
  the third defect.** It carries a `checkpointGuarded` of its own rather than
  calling `guarded`, because `[]eventtest.Defect` and
  `[]eventtest.CheckpointDefect` are different types and making `guarded`
  generic would move `defects_test.go`, which no section's allowed set admits.
  Both live in `checkpoints_test.go`. The suite widening is a **store-contract**
  widening (§5.3) that the signature baseline cannot see, so it is announced in
  the `eventtest` package doc, `docs/modules/{en,ru}/eventtest.md` and the
  release note.
- **`event/projection` is a package of the root module.** It may import nothing
  third-party — including in its own `_test.go` files, because `check-deps` lists
  the root module with `-deps -test -tags=integration`. **Every live test of this
  phase lives in `event/eventpg/`.** And `startsNothing`
  (`scripts/extensions_test.go:147`, called from `scripts/event_test.go:66`)
  forbids a `go` statement in any non-test file under `event/`: the partition
  workers are N `runtime.Runner` values the host supervises, the redrive runs on
  the operator's goroutine, and the DLQ scanner is a `runtime.Runner` the host
  wraps `Redrive.Any` in. [[D-092]].
- **The live gate names its own command and an unset DSN fails it.** `TestMain`
  (`event/eventpg/main_integration_test.go:29`) exits 1 on an unset
  `FROSTGROVE_EVENTPG_TEST_DSN`, so a live claim cannot be made by a suite that
  skipped. S6 is where the nineteen items of §6 are proved, twice in a row.

---

## What this plan delivers, and what it does not

Nine new non-test files in `event/projection` and eight modified ones
(`spec.go`, `pass.go`, `page.go`, `state.go`, `projection.go`, `classify.go`,
`errors.go` — whose "four, and none of them crosses a store seam" comment stops
being true — and `doc.go`); two new `eventtest` sections with three new defects
across three files; a widened
`scripts/projection_test.go` (four new walks — the dispatch-import walk, the
sequence-hash modulus walk, the two-cursor walk that gives §UC-142 a spelling to
refuse, and the [[D-130]] transaction walk §INV-103 has been promising — each
with its fixture control, plus the three existing walks extended over the new
surface); no new module, no
new package, **no change to any file of the `event` package itself**; nineteen
live cases in `event/eventpg` behind `//go:build integration`; two `_examples`;
two decision records (D-134, D-135) and one amendment recorded beside [[D-131]];
two module pages in two languages each, one flow extended and one new; a
regenerated `docs/api/surface.md` whose `event` section is unchanged.

It does **not** deliver ES-05, ES-07, ES-08 or ES-09 — no `projection.Wait`, no
store-issued barrier token, no operation receipt, no `history.AtVersion`, no
snapshot. Nor a merge, a shared reader fanned out to N routers, a filter on
`Log.ReadAll`, a library-started thread pool, a second durable-intent table, a
time-based claim in a *store*, a `Position → Cursor` function, `Reset`/`Rewind`/
`SetCheckpoint`/`Resume`/`Retry`/`Clear`, skipping unknown event types, an
applied position, a sandbox for a handler, or any knowledge of a read model's
table names. Each refusal is [SPEC] §2's and is not re-argued here.

---

## Decisions this plan makes, that [SPEC] left open or got wrong

[SPEC] §8 leaves eight tensions to the plan. Three of them are answered below;
five are answered in place where the contract needs them. **P-1, P-7 and P-8 are
corrections to [SPEC]** — three shapes it froze that do not hold, each found by
reading the kernel or by walking a failure the spec's own prose already names.
P-1 is the most important line in this document; P-7 is the one whose absence
would have shipped two senders on the first cutover of every existing
deployment.

### P-1 — `ByStream()` may not be `envelope.Stream.String()`, because that renders the family and drops the key

[SPEC] §1.2 and §5.2 both spell the default sequencer as
`envelope.Stream.String()`. `event.Stream.String()` is
`event/identity.go:26-32`:

```go
func (this Stream) String() string {
	if checkName(this.Family) != "" {
		return fieldOpen + "stream unnameable" + fieldClose
	}
	return fieldOpen + "stream " + this.Family + fieldClose
}
```

It renders **`[stream orders]`** — the family, and nothing else. The `Key` is not
in it, and it deliberately is not: that method exists to put a family into a
refusal's text without letting a forged one close the field the renderer opened.

Implemented as written, `ByStream()` is not
`SequentialPerAggregatePolicy`; it is a per-*family* policy. Two consequences,
and both are defects:

- **ES-02 stops working.** Every `orders` event hashes to one key, so a four-way
  `Cover` puts the whole family in one partition and the other three apply
  nothing. §UC-135's positive case fails, which is the good news; a reader who
  patched the assertion instead of the key would have shipped it.
- **ES-03 over-blocks catastrophically.** One poison order parks the sequence
  `[stream orders]`, and every later event of **every** order in the system is
  parked behind it without reaching a handler. That is `OneSequence()` wearing
  the default's name, and it is exactly the "one broken order stops the
  projector" outcome §1.4 says the queue exists to prevent.

**So `ByStream()` is `event.Compose(Family, string(Key))`** — the kernel's own
frozen, injective, escaping composition (`event/identity.go:48-56`), which is
already what every stream identity in this repository is built from and whose
doc comment says two distinct part lists never render one key. Nothing else in
[SPEC] changes: the constructor's name, its `Sequencer.Name()` and its role as
the nil default are as written. §UC-135's control gains a second arm asserting
that a family-only key collapses the four partitions to one, so the correction is
measured and not merely applied.

### P-2 — the redrive's tier check is the half a `RedriveSpec` can make, and the missing half is named

[SPEC] §1.4 says *"the tier check of §1.1 applies to a redrive call exactly as it
does to a pass: tier B is refused before the handler runs"*. §1.1's comparison
needs **two** authorities: the checkpoint store's, from
`tracker.Transaction(inner)`, and the destination's, from `crud.KeyOf`. A
`RedriveSpec` (§5.2) carries no `Checkpoints` — deliberately, because §INV-094
says no redrive path issues a `Checkpoints.Save` and the fence is the loop's. So
there is no second authority to compare against and tier B is not detectable
from a redrive.

**What `NewRedrive`/`Redrive` do instead, and it is stated rather than implied:**
`Destination: Unchecked` is refused at construction (`ErrSpec`, the same reason
as the park's), and per call, inside the unit and before the handler, the
destination is resolved with `crud.ExecutorFor` and required to be a transaction
with `crud.IsTransaction` — the two halves `checkUnit` already has, minus the
comparison. The park's own alignment is **falsified by consequence** exactly as
§1.6 says for `Park` and `Generations`: §UC-182's crash arm asserts the letter is
still parked and nothing applied, which a `Redriver` over a second pool fails.

Adding `Checkpoints` to `RedriveSpec` was considered and rejected: a field that
exists only to be compared invites a caller to believe a redrive records
progress, which is the one thing §INV-094 forbids, and it would put a
`Checkpoints` on the operator's path for a call that never touches one.

### P-3 — what a `Park`, a `Generations` or an `Effects` failure costs

[SPEC] gives the failure table for `ErrParkFull` and for nothing else on these
three seams. The pass's failure table is total by construction
(`pass.go:refused`), so each arm is written rather than absent:

| Call | Failure | What the pass does |
|---|---|---|
| `Park.Park` | `errors.Is(err, ErrParkFull)` | the third verdict: unit rolls back, no advance, nothing parked, no attempt consumed, `PhaseBlocked`, unbounded retry, `Ready` fails past `Tolerate` (§UC-150) |
| `Park.Park` | anything else | **halt**, naming the park — today's `held.sink != nil` arm, unchanged in spirit: a policy with nowhere to record is a skip with extra words |
| `Park.Sequences`, `Park.Holds` | any | `postpone` — retry without consuming an attempt, like `ErrBackend`. It is a read of the application's own table, and halting a live projection because that table blinked is a projector stopped by another route |
| `Generations.Active` | any | `postpone`, same argument. A cutover row that cannot be read is not a reason to stop applying events; it is a reason not to stage |
| `Effects.Stage` | classifier says `Retryable` | `redeliver` — the ordinary backoff, an attempt consumed |
| `Effects.Stage` | classifier says `Permanent` | **halt**, naming `Effects`. It is deliberately **not** routed to the park: parking an envelope because the *staging* failed would record a read-model hole that does not exist |

### P-4 — `Observe` refuses an `of` that carries a partition

§5.2 documents `Observe`'s `of` as *"the generation — a whole identity"* and
leaves it at that, while §UC-155 makes the same mistake a refusal for a
`RedriveSpec`. GAP-10 was closed *"by typing rather than by documenting"*, and
this is the one place the typing is incomplete. `Observe` and `Reached` refuse an
identity carrying a partition with `ErrTopology`, naming the field. Backlog `## P4`
item 2 (`[medium]`) stays open and is not otherwise touched.

### P-5 — `Spec.Pace` is a read throttle and its precedence is written down

§UC-167 says `Pace` must not apply while following, where `Idle` governs. So:
`Pace` gates the **read** and only while the previous read answered `more`;
`Idle` gates the **follow**; `Backoff` gates the **retry**; all three are
measured through `spec.Ticks`, so §UC-167 drives the schedule rather than
sleeping through it. Backlog `## P4` item 7 (`[medium]`) stays open as the
documentation question it is.

### P-6 — [SPEC] §8's three answered tensions

- **§8.1, `Sequencer.Name()`** — it stays a **contract**, checked at the park and
  nowhere else. Making it visible needs a column in the kernel's frozen
  `Checkpoint` (§5.1 forbids) or in the application's table (which the framework
  does not read). The module page says so beside `Sequence`.
- **§8.2, an evicted letter's identity** — the example `Redriver` keeps evicted
  letters in an `evicted` table rather than deleting them, because `Park.Holes`
  must count "evicted without being applied" and therefore the implementation
  already keeps the row. The module page says which guarantee rests on it.
- **§8.6, `Park.Holes`** — the example `Park` derives it as **two counts**
  (queued letters plus evicted-unapplied letters) rather than keeping a column,
  so it cannot drift from the rows it summarises. The module page names it as the
  one method whose wrong answer is invisible until a cutover admits a holed
  generation.
- **§8.7, a `Cover` is checked where it is written** — both `_examples` show
  **only** the `for _, part := range cover.Partitions()` spelling, which is the
  cheapest way to make the checked path the obvious one.
- **§8.3 (the byte bound), §8.4 (`Pace` vs a governor), §8.5 (the shared reader's
  measurement) and §8.8 (`Reached` is "delivered", not "the rows agree")** are
  documentation obligations discharged in S6 and recorded in the backlog where
  they are open.

### P-7 — the effect gate is on `Generations`, not on `Generation`, because otherwise the first cutover of every live deployment has two senders

[SPEC] §1.6 gates the third suppressor on the projection's own generation:
*"`Generations` is required beside `Effects` whenever `Spec.Generation !=
Ungenerated`, and optional otherwise: a projection that has no generations has
one sender, and the fence already ensures that."* Read as the suppressor's
condition — which is how it was written into `pass.go` below — it is wrong, and
the failure is on the one path every existing consumer must take.

§1.5 says `Ungenerated` renders nothing, *"so every projection that exists today
is generation zero and its name does not change"*. `CutoverSpec.From` is a
`Generation`, so `Cutover(From: Ungenerated, To: 2)` is expressible and is the
**only** migration a running deployment has: giving the live projection
`Generation: 1` first renames `orders` to `orders@1`, which changes the
checkpoint row key and resumes from the origin against a live read model — the
silent version of the failure §UC-188's refusal exists to make loud.

So take the shipped case. `orders` runs at `Ungenerated` with `Effects` staging
order confirmations. `orders@2` is built beside it, `Reached`, and the cutover
commits. `orders@2` reads `Active("orders") == 2` and stages. `orders` — still
running, still holding `Effects` — skips a suppressor gated on a condition it can
never satisfy, and stages too. Two emails, two payment captures, per event, with
no error on any path, while D-135 claims the boundary is closed. That is exactly
what ES-06 exists to prevent.

**The suppressor is gated on the capability being supplied, not on the
projection's own number:**

```go
3. spec.Generations != nil → Generations.Active(inner, spec.Name)
                             not this generation stops here
```

At `Ungenerated` with no cutover yet the row answers `Ungenerated`, which **is**
this projection's generation, so it stages exactly as before. After
`Activate(from: Ungenerated, to: 2)` the row answers 2, and the retiring
projection stops staging inside the transaction that commits its own advance —
the same fence, the same breath, one code path for both cases.

Three consequences, each stated rather than implied:

- **`Generations.Active` answers `Ungenerated` and a nil error for a projection
  with no ownership row.** That is the pre-cutover state of every existing
  deployment, and an error there would `postpone` a live projection for ever
  under **P-3**. It goes on the interface.
- **The deployment ordering is an operator obligation, and it is the blue/green
  shape [[D-131]]'s amendment already teaches for `Ignore`.** Release *n* adds
  `Generations` to the live `Ungenerated` spec — staging is unchanged, because
  the row answers `Ungenerated`. Release *n+1* builds `orders@2`. Only then is
  the cutover safe. Skipping the first release is what leaves two senders.
- **The limit is stated, not implied.** A live projection carrying `Effects` and
  **no** `Generations` at all is outside the boundary: the framework holds no
  route to an ownership row, cannot invent one, and refusing `Effects` without
  `Generations` universally was already rejected in §1.5 for imposing an
  ownership table on every single-sender projection that has no generations. So
  its cutover has two senders until it is stopped, and that sits in D-135 and on
  the module page beside the second-`Spec.Name` edge, measured by §UC-193's
  control rather than asserted in prose.

Refusal 11 moves with it: a `Generations` supplied beside `Effects` at
`Ungenerated` is no longer "supplied where nothing uses it" — it is the
migration, and it is used.

**And the gate is a mechanism, not a guarantee this framework can make on its
own** *(round 2, GAP-1, driven against PostgreSQL 17.9 and at the Go level).*
What the suppressor gives is a read of the row through the ambient transaction.
What makes that read a **boundary** is the implementation's: `Active` must take a
row lock the cutover's `UPDATE` waits behind — `FOR SHARE` / `FOR KEY SHARE` — or
run in a `SERIALIZABLE` unit. [[D-126]] forbids this framework choosing the
level, and at `READ COMMITTED` a plain read and a concurrent `Activate` of one
row do not conflict: both commit, and the pass that read `1` commits its staged
effect under a row that already names `2`. So the obligation goes on the
`Generations` doc comment where `Park`'s ordering obligations live, [SPEC] §1.6
says which read closes the window, and §UC-202 measures the two recipes against
each other rather than leaving the difference in prose. It is also stated what
the lock does **not** buy: the retiring generation goes on advancing past the
barrier the cutover was observed at, and that overlap is §1.5's window, closed by
draining rather than by locking.

### P-8 — a claim is an ownership token, because an expired one otherwise reproduces the disorder the queue exists to prevent

[SPEC] §1.4 and §5.2 spell the claim protocol as
`Claim(ctx, of, sequence) (claimed string, found bool, err error)` with
`Release`, `Touch` and `Evict` taking `(of, sequence)`. The claim therefore
answers a **sequence name**, not a claim identity, and the same §1.4 adopts
Axon's expiry rule: *"a claim that is never released expires by the
application's clock in the application's table"*. Those two sentences cannot both
hold.

Operator A claims `A`, holding `A2 A3 A4`, and applies `A2`. Its host stalls — a
GC pause, a slow `Destination`, a frozen container — past the application's claim
duration. The `runtime.Runner` §1.4 explicitly invites a host to wrap
`Redrive.Any` in re-claims `A`, legally, reads `A3 A4`, and applies `A3`. A
resumes. `Redrive.Sequence` read its letters **once**, so nothing re-checks: A
applies `A3` a second time, or reaches `A4` before B's `A3` commits. A letter
applied twice, and `A4` before `A3` — the queue's ordering guarantee broken by
the queue's own recovery path, which is verbatim the failure §1.4 introduces
`Claim` to close. Then A calls `Release(of, "A")` on its way out and frees
**B's** claim. §UC-191's two simultaneous `Claim`s see none of it.

**So a claim is a value, and every write that ownership authorises carries it
back:**

```go
// Minted by the implementation and opaque to this package, which compares
// nothing in it and only hands it back. Of and Sequence say what was granted;
// Token is what an implementation recognises its own grant by; Until is the
// deadline it granted, published so an operator can size a unit against it and
// nothing more — the framework reads no clock. [[D-126]] rules out a store's
// clock and a fence, not this.
type Claim struct {
	Of       Identity
	Sequence string
	Token    string
	Until    time.Time
}

type Redriver interface {
	Claim(ctx context.Context, of Identity, sequence string) (Claim, bool, error)
	Sequence(ctx context.Context, claim Claim) ([]Letter, error)
	Evict(ctx context.Context, claim Claim, letter Letter) error
	Touch(ctx context.Context, claim Claim, cause error) error
	Release(ctx context.Context, claim Claim) error
}
```

A `Claim` is total: it names the identity and the sequence as well as the grant,
so the four calls that carry one take nothing else. That is why `Evict` no longer
takes an `of` and `Touch` no longer takes a `(of, sequence)` pair — the pair is
in the claim, and a call that could name one sequence while holding another's
grant is the ambiguity this correction exists to remove.

`projection.Claim` the type and `Redriver.Claim` the method coexist: a method
name lives in its type's namespace, which is what `ParkSequence` had to be
renamed around and this does not.

The contract obligation is one sentence and it is where the correctness lives:
**a call whose claim no longer owns the sequence is refused with an error
wrapping `ErrClaimLost`, never applied.** `Evict` runs inside the same unit as
the apply, so a refused `Evict` rolls the apply back with it — which is why the
token has to travel on the write and not merely on the read. On `ErrClaimLost`
the redrive stops that sequence at once, issues **no** `Touch` and **no**
`Release` (it owns neither), and answers `Retried` carrying what it applied
before the loss beside an error wrapping the sentinel — a lost claim is the
operation failing, not the letter failing, so it is not a `Retried.Cause`.

`ErrClaimLost` joins the sentinels in `errors.go` (**GAP-12**'s home). The module
page states the relationship the operator owns: **the claim duration must exceed
the longest unit a redrive may take**, because a duration shorter than one letter's
apply turns every redrive into a sequence of lost claims and drains nothing.

---

### P-9 — the partition filter lands in S1, with the vocabulary, because a published predicate nobody calls is a promise the tree does not keep

**Made after S1's review, which drove it.** S1 as planned shipped `Cover`,
`Spec.Partition` and `Partition.Matches` and scheduled the filter for S2. Driven
over `eventmemory` with the module page's own spelling — `for _, part := range
cover.Partitions()`, 8 streams × 3 events — the two runners of an admitted cover
applied **every one of the 24 events twice**, both checkpoint rows reporting
`Advance: 1, Applied: 24, Highest: 24`, both runners `PhaseFollowing`, no refusal
anywhere. `Partition.Matches` had zero non-test callers. `make api` had already
recorded all of it as shipped capability.

Two closes were available: refuse a non-`Whole` `Spec.Partition` at `New` until
S2, or land the filter. The refusal was rejected for a reason the review's own
close criteria name: it leaves `Matches` inert, so the fourth criterion — *"a test
asserts `Partition.Matches` has a caller in a non-test file"* — is unsatisfiable
while it stands, and it makes **P-10**'s refusal undrivable, because a partitioned
runner could not be constructed to drive it.

So `Projection.matching` lands in S1 and four tests move from S2 to S1 with it:
`TestFourPartitionsApplyEveryEventOnceAndKeepEachKeyInOrder`,
`TestAPageThatMatchesNothingAdvancesAndCallsNoHandler` and
`TestASequencerPanicHaltsAndAHandlerPanicDoesNot`, plus the surface walk
`TestEveryPublishedTopologyPredicateHasACaller`.
`TestUnorderedSpreadsAndOneSequenceConcentrates` stays in S2: what it measures is
a restart mid-drain landing in the same partition, which is the loop's
re-delivery rather than the filter.

Three things ride with the filter and are not separable from it:

- **The share is decided once, at the read**, and held beside the page, because a
  retry re-applies the page it holds and asking the sequencer again would be a
  second chance for an unstable key to move an envelope between attempts.
- **`Whole()` calls no sequencer at all.** It matches everything, so the fast path
  is exact rather than an optimisation — and it is what makes the check cost
  nothing for every projection that exists today.
- **A panic out of a sequencer halts** (§UC-144). It is not recovered into the
  page's failure the way a handler's is: the filter runs outside `applyPage`'s
  recovery, and an envelope that could not be assigned to a sequence belongs to no
  partition, so there is nothing for a retry, a quarantine or a park to be about.

S2 keeps `Split`, the handoff and the `eventtest` topology sections, which is the
bulk of it.

### P-10 — the resume asks one question the `Cover` cannot: is a coarser row already recording?

**ES-02 study nuance 3, which the plan had dropped.** Axon's
`JpaTokenStore.initializeTokenSegments` throws rather than adopting an existing
set of segment rows: *"only when a streaming processor starts for the first time
can it initialize the number of segments to use"*. [SPEC] cited it at line 341 for
what is `validateSegment`'s job, so the mechanism the citation pointed at was not
the one it described. Both are corrected in the same change: the citation is
split, and §1.3.1 is written.

Driven, the two-release migration: `orders` drained unpartitioned to
`Highest: 200`, then two runners at `{0,1}` and `{1,1}` admitted with no refusal,
applying 200 further envelopes over a log of 200 into a read model already holding
them — three rows, all `Highest: 200, Applied: 200`, all green. It **survives the
filter**: with each partition reading its own half, the duplicate count halves and
does not reach zero.

So `Projection.unclaimed` runs once per runner life, at the resume, and refuses
with `ErrTopology` when a checkpoint row exists for any **coarser** share of this
runner's key space. It is at the resume and not at `New` because `New` performs no
I/O by contract, because the answer is a fact about the store rather than about
the spec, and because the failure it guards is a deployment rather than a
composition.

**Every coarser ancestor and not only `Identity.Whole()`**, which is what the
review scoped it to: the same hazard one level down is `{0,3}` started beside a
live `orders#0.1`, the ancestor set is at most ten rows for a mask of 1023, and it
is **empty** for a projection that named no partition — so the cost is zero for
every projection that exists today and ten loads once per process start at the
published ceiling. The finer direction is deliberately not covered and is stated
in §1.3.1: enumerating it would need a `List` the `Checkpoints` contract does not
have, and that direction is a merge, which §1.3 refuses outright.

### P-11 — `Cover{}` and `Identity{}` are refused at every door, and the discriminator is exact rather than a marker

Both types have no exported field, so the zero composite literal is the **only**
value reachable without the constructor — and it is the one value the constructor
never answers beside a nil error (`NewCover()` refuses the empty set,
`NewIdentity("")` refuses the empty name). That makes `Count() == 0` and
`Projection() == ""` exact discriminators, so **no marker field is carried**:
a `checked bool` beside them would be redundant state whose only writer is the
same constructor.

S1 has no door that *takes* either — `Observe`, `Reached`, `Cutover`,
`Letter.Identity`, `RedriveSpec.Of` are S3 and S4 — so what S1 ships is the rule
and its enforcement rather than a refusal with nothing to refuse:
`TestEveryDoorTakingACoverOrAnIdentityRefusesItsZeroValue` walks
`event/projection` for an exported function taking a `Cover` or an `Identity`
**parameter** whose body never asks, reports nothing today, and carries a fixture
control with two doors that ask and two that do not. It goes red on the first S3
or S4 door that lands without the question, in the section that lands it.

The consequence that made this `[high]` rather than a tidiness note is §UC-143's
`min` over an empty cover: the fold's identity element is `MaxUint64`, which every
generation trivially clears, so §UC-159's `Reached` answers true and §UC-163's
cutover commits on no evidence at all. S4 writes the refusal, not the fold.

### P-12 — `NewIdentity` spells the kernel's identifier rule a second time, deliberately

An `Identity` reaches `event.Track` on the `Spec` path and **only** there. From S3
it is an input at five doors — `Letter.Identity`, `RedriveSpec.Of`, `Observe`'s
`of`, `Cutover`'s `from` and `to` — none of which crosses that door, and §5.1
freezes the `event` surface for this phase so no kernel checker can be exported to
call. Driven before the fix: `NewIdentity("a[b]c")`, `("a\x00b")`, `("a\nb")`,
`("a\xffb")` and `ParseIdentity("a[b]c@2#3.7")` all answered a nil error, while
the type's own doc comment claimed *"carrying neither bracket, so it passes the
kernel's identifier rule"*.

So the rule is duplicated in `unnameable`, phrase for phrase, and the duplication
is pinned rather than trusted: `TestNewIdentityRefusesEveryNameTheKernelRefuses`
walks `NewIdentity` and `event.Track` over one table **and over every byte a name
can carry**, and fails on a name only one of them takes.

**It is not stricter than the kernel either, and that is the part worth stating.**
A name of two spaces was among the review's driven examples; the kernel takes it,
because §the kernel text rule is explicit that there is no trimming, no case
folding and no normalisation — keys compare as bytes. `NewIdentity` takes it too.
Refusing it would have broken the very agreement the pin exists to hold, and would
have been this package inventing a naming policy the framework does not have. The
two deliberate differences are `@` and `#`, and they have their own use case
(§UC-188) and their own test.

---

## The kernel baseline move

`check-event-kernel` compares `scripts/event_kernel.sha256` against a `find` over
`event/` outside `event/eventpg`, file by file. Every section below opens by
recording its predecessor **before its first file is written**:

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

`event-kernel-moved` fails on an empty diff as loudly as on an unexpected path,
and it reads the manifest rather than git, so it says the same thing before
`git add`, after it, and after `git commit`.

**S1 through S5 close that way. S6 closes with the opposite assertion**, because
S6 moves nothing the manifest sees — its whole file list is `event/eventpg`
tests, `scripts/`, `_examples/` and `docs/`, and `event/eventpg/*` is excluded
from the manifest by construction (`scripts/checks.sh:335`). Handed an empty
moved set, `event-kernel-moved` fails on purpose, and making it pass would mean
touching a file for the fence's sake — the fence measuring nothing. So S6 runs
`diff -u .git/event_kernel_before_s6 scripts/event_kernel.sha256` and requires it
empty. Same evidence, opposite direction, stated where a later reader will
otherwise read it as the fence being relaxed.

### The complete set of paths phase 4 may add to the manifest

**New, `event/projection/`:** `identity.go`, `partition.go`, `cover.go`,
`sequence.go`, `topology.go`, `park.go`, `redrive.go`, `generation.go`,
`effect.go`, and their `_test.go` peers (`identity_test.go`,
`partition_test.go`, `cover_test.go`, `sequence_test.go`, `topology_test.go`,
`park_test.go`, `redrive_test.go`, `generation_test.go`, `effect_test.go`,
`alignment_test.go`).

**Modified, `event/projection/`:** `spec.go`, `pass.go`, `page.go`, `state.go`,
`projection.go`, `classify.go`, `errors.go`, `doc.go`, `spec_test.go`,
`retry_test.go`, `unit_test.go`, `loop_test.go`, `harness_test.go`,
`lifecycle_test.go`.

**New, `event/eventtest/`:** `sections_topology.go`, and rows in
`sections_checkpoints.go`, `defects_checkpoints.go`, `inventory.go` (nothing —
the checkpoint inventory lives in `sections_checkpoints.go`), plus
`checkpoints_test.go` (the certified counts, `checkpointDefects` 5 → 8 and the
new `TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt`) and `doc.go`.
**`defects_test.go` does not move** — `inventoried` sizes the store suite and
this phase adds no store defect — and no section's allowed set admits it, so a
count edited there is reported rather than absorbed.

**`event/eventmemory/`:** nothing is planned. `eventmemory.Checkpoints` already
refuses a save at advance 1 over a live row (`checkpoints.go:113` —
`checkpoint.Advance != held.Advance+1` → `Conflict`) and its cursor is the log's
own, not bound to a projection name, so it should pass both new sections
unchanged. **If a file there moves, S2's report says which and why** — a store
that fails a new obligation is a real finding, not a fix to absorb.

**Forbidden in every section, and checked:** `^event/[a-z_]*\.go$`. The `event`
package itself does not move.

### The justifying sentence for the re-baseline

> Phase 4 moves the manifest for `event/projection/` and `event/eventtest/` and
> for nothing else. `event/projection` is the consumer, and everything this phase
> adds — a partition, a cover, an identity, a sequencer, a park, a redrive, a
> generation and an effect — belongs to the consumer and to the chosen store,
> which is [APX]'s own framing (*«Владельцы — опциональная event-подсистема и
> выбранный store, не framework kernel»*). `event/eventtest` moves because the
> suite is the kernel's evidence half and a split has no spelling outside the
> `Checkpoints` surface it already publishes; that widening is a **store
> obligation** and is announced where a store author reads. No file of the
> `event` package moves, because nothing this phase needs is missing from the
> vocabulary: `Backing`, `NewAuthority`, `Authority.Same`, `Compose`, `Track`,
> `Checkpoints`, `Position` and `Progress` are all already there, and using three
> of them for a purpose they were not written for is a comment in `pass.go`
> rather than a contract change.

---

## Coverage matrix

Every UC and INV of [SPEC]. **Section** is where it is delivered; **Checkpoint**
is the section whose command proves it; **Proved by** names the test or the
`eventtest` subtest. A lower-case name — `topology`, `topology handoff` — is a
`t.Run` subtest of `eventtest.RunCheckpoints`, meaningful only because S2's
defect harness shows the suite catches a broken store while exercising the real
one.

Every UC and INV of [SPEC], plus **UC-193**, which **P-7** made necessary, and
**UC-194**, **UC-195**, **UC-196** and **INV-107**, which S1's review made
necessary, and **UC-197**, **UC-198** and **UC-199**, which S3's review made
necessary — each written into [SPEC] in the same change as the code that closes
it — 94 identifiers, `UC-131…UC-199` and `INV-083…INV-107`.

**Every name below is in the Tests list of the section its Checkpoint column
names, and inside that checkpoint's own counted `-list` pattern.** A name here
that no arm counts is a use case that can be reported closed on a green
`go test`, which is phase 1's failure mode exactly.

**That rule was checked mechanically rather than by reading**, extracting every
`Test…` from this matrix and matching it against the counted pattern of the
section its Checkpoint column names. It found five breaches, and all five are
closed above: `TestNoExportedFunctionOrdersOrTakesTwoCursors` and
`TestAGenerationDrainsBesideTheLiveOneAndTheRowsAgree` named tests that existed
nowhere in the plan or the tree; `TestCursorIsNeverCompared` and
`TestNewRefusesEverySpecItCannotAssemble` ran in arms that counted nothing; and
`TestEverySequencerIsTotalPureAndStable` was counted by S1 while the matrix
named S2. Re-run the extraction after any matrix edit — it is thirty seconds and
it is the only thing that holds this rule.

### Use cases — Group AA, one transaction authority (ES-01)

| UC | Section | Checkpoint | Proved by |
|---|---|---|---|
| UC-131 checkpoint store and destination are one transaction | S1 | S1 + S6 | `TestTheAlignmentIsComparedInsideTheUnitBeforeTheHandler` (S1, bound fake); `TestTierAIsProvedAndTierBIsRefusedLive` (S6) |
| UC-132 a unit opens two transactions, one per resource | S1 | S1 + S6 | `TestTwoTransactionsUnderOneUnitAreRefusedAndOneIsAccepted` (S1); `TestTierAIsProvedAndTierBIsRefusedLive` (S6, two pools) |
| UC-133 the destination cannot be resolved through `crud` | S1 | S1 | `TestUncheckedMakesNoComparisonAtAll` (S1) — the existing `unit_test.go` cases re-run unchanged are the regression half |
| UC-134 a foreign resource and the framework's promise | S6 | S6 | `TestAForeignDestinationGetsFourPromisesAndNotTheFifth` (S6, N kill points) |

### Use cases — Group AB, sequences and partitions (ES-02)

| UC | Section | Checkpoint | Proved by |
|---|---|---|---|
| UC-135 four partitions drain one log, each key in order | **S1** | S1 + S6 | `TestFourPartitionsApplyEveryEventOnceAndKeepEachKeyInOrder` + the fresh-key control + **P-1's family-only-key control** (S1 — moved from S2 with the filter, see **P-9**); `TestFourPartitionsOverOneLogAndTheModulusControl` (S6) |
| UC-136 a page none of whose envelopes match | **S1** | S1 | `TestAPageThatMatchesNothingAdvancesAndCallsNoHandler` (S1 — **P-9**) |
| UC-137 `Unordered` and `OneSequence` told apart | S1 + S2 | S1 + S2 | `TestUnorderedSpreadsAndOneSequenceConcentrates` (S2), `TestEverySequencerIsTotalPureAndStable` (S1) |
| UC-138 a split, and every key lands in exactly one child | S2 | S2 + S6 | `TestASplitWritesTwoChildrenAtTheParentsCursorInOneTransaction` (S2); `TestASplitsThreeStatementsAreOneTransaction`, `TestFourPartitionsOverOneLogAndTheModulusControl` (S6) |
| UC-139 a split under a running parent | S2 | S6 | `TestARunningParentHaltsWhenItsRowIsSplitAway` (S6) with the drained-parent control |
| UC-140 a split of a partition with no checkpoint row | S2 | S2 + S6 | `TestASplitOfAParentWithNoRowIsRefusedForBothAbsences` (S2); `TestASplitWithNoParentRowWritesNothing` (S6, rows read in `psql`) |
| UC-141 a split at the published ceiling | S1 | S1 | `TestASplitAtTheCeilingIsRefusedAndOneBelowItSucceeds` (S1 — `Partition.Split`, arithmetic only) |
| UC-185 a split under a `Unit` that runs its body twice | S2 | S2 | `TestASplitUnderATwiceRunUnitIsOneSplitOrARefusal` (S2) |
| UC-186 a declared partition set leaves a gap | S1 | S1 | `TestACoverWithAGapIsRefusedAndACompleteOneIsNot` (S1) |
| UC-187 a declared partition set overlaps | S1 | S1 | `TestACoverWithAnOverlapIsRefusedByMaskArithmeticAndNotByName` (S1) |
| UC-142 a merge is asked for | S1 + S6 | S6 | `TestNoExportedFunctionOrdersOrTakesTwoCursors` (S6, **new** in `scripts/projection_test.go`, in S6's counted five-name scripts pattern) with the `Merge` fixture control |
| UC-143 a partitioned projection's aggregate progress | S4 | S4 | `TestObserveAnswersTheMinimumAcrossTheCover` with the single-member control (S4) |
| UC-144 a sequencer panics | **S1** | S1 | `TestASequencerPanicHaltsAndAHandlerPanicDoesNot` (S1 — the filter is what calls a sequencer from the loop, so the halt lands with it, **P-9**) |
| UC-145 a partition name is built and parsed | S1 | S1 | `TestAnIdentityRendersAndRoundTrips` (S1) |
| UC-188 a projection is legally named `orders@2` today | S1 | S1 | `TestADelimiterInAProjectionNameIsRefusedAtConstruction` with the `orders.v2` control (S1) |
| UC-194 a partitioned runner beside a live coarser checkpoint row | **S1** | S1 | `TestAPartitionedRunnerBesideALiveCoarserRowIsRefused` with the after-the-handoff control and the never-ran control (S1 — **P-10**) |
| UC-195 a `Cover` or an `Identity` nobody built reaches a door | **S1**…S5 | S1 | `TestTheZeroCoverAndTheZeroIdentityAreTellableFromEveryCheckedOne` (S1) and `TestEveryDoorTakingACoverOrAnIdentityRefusesItsZeroValue` (S1, `scripts/`, vacuous today with a fixture control) — **P-11** |
| UC-196 a name the kernel's identifier rule refuses | **S1** | S1 | `TestNewIdentityRefusesEveryNameTheKernelRefuses`, which walks `NewIdentity` and `event.Track` over one table and over every byte (S1 — **P-12**) |

### Use cases — Group AC, the park (ES-03)

| UC | Section | Checkpoint | Proved by |
|---|---|---|---|
| UC-146 a poison event parks its sequence and the following events | S3 | S3 + S6 | `TestAPermanentFailureParksItsSequenceAndTheEventsBehindIt` with the succeeding control (S3); `TestTheBlockingTestAndTheAdvanceAreOneCommit` (S6) |
| UC-147 a later page meets a parked sequence | S3 | S3 + S6 | `TestALaterPageParksWhatTheQueueAlreadyHolds` (S3); same live case (S6) |
| UC-148 a healthy projection pays nothing | S3 | S3 + S6 | `TestTheFastPathCallsHoldsNeverAndSequencesOncePerResume` with the after-a-park and after-a-drain arms (S3); `TestTheFastPathCostsNothingLive` (S6) |
| UC-149 a second instance parks while this one holds a zero count | S3 | S3 | `TestAParkWriteRollsBackWithItsUnitAndAnOvertakenRereadsTheCount` (S3) |
| UC-150 the park is full | S3 | S3 + S6 | `TestAFullParkBlocksTheAdvanceAndSkipsNothing` (three bounds, S3); `TestAFullParkBlocksAndOneDeleteClearsIt` (S6) |
| UC-151 1023 sequences accept a 1024th letter | S3 | S3 | `TestTheParksBoundIsPerSequenceAndNotPerQueue` (S3) — **an obligation on the implementation, not a property of the framework**: there is no `isFull` on `Park`, so the two-dimension arms are asserted of the reference queue and the framework-visible half is the fourth arm, a running projection over a one-dimensional queue sitting in `PhaseBlocked` where the two-dimensional one keeps advancing |
| UC-152 a parked sequence is redriven and drains | S3 | S3 | `TestARedriveDrainsASequenceInInsertOrderAndTouchesNoCheckpoint` (S3) |
| UC-153 a redrive stops at the first repeat failure | S3 | S3 + S6 | `TestARedriveStopsAtTheFirstLetterThatFailsAgain` (S3); `TestARedriveStopsAtTheRepeatFailureAndSavesNothing` (S6) |
| UC-154 a redrive rotates rather than starving | S3 | S3 | `TestARedriveRotatesByLeastRecentlyTried` with the one-sequence control (S3) |
| UC-191 two operators redrive at once | S3 | S3 + S6 | `TestTwoGatedRedrivesNeverProcessOneSequence` (S3); `TestTwoOperatorsRedrivingAtOnce` (S6) |
| UC-155 a redrive across a sequencer change | S3 | S3 | `TestARedriveNamingAnotherSequencerOrAPartitionIsRefused` (S3) |
| UC-156 a parked projection does not report "caught up" | S3 | S3 | `TestAParkedProjectionIsDegradedAndStillReady` with the after-redrive control (S3) |
| UC-157 an operator skips a letter | S3 | S3 | `TestAnEvictionLeavesAHoleAndDecrementsNothing` with the redriven-to-completion control (S3) |
| UC-179 a partition holding a parked sequence is split | S3 | S6 | `TestASplitLeavesTheParkedLettersReachable` with the empty-park control (S6) |
| UC-189 two generations share one `Park` | S3 | S3 | `TestTwoGenerationsShareAParkAndSeeNoneOfEachOthersLetters` with the two-partitions control (S3) |
| UC-190 `ParkSequence` beside a wiring that cannot order it | S3 | S3 | `TestParkSequenceIsRefusedOutsideTierA` with the `Halt` control, which now carries the `Park` beside it (S3) |
| UC-197 the attempt budget is spent on a retryable failure | S3 | S3 | `TestASpentAttemptBudgetParksATransientFailureAndARedriveClearsIt` with the permanent-failure control (S3) |
| UC-198 a `Park` beside a policy that does not name it | S3 | S3 | `TestAParkIsInertBesideAPolicyThatDoesNotNameIt` with the `ParkSequence` control (S3) |
| UC-199 which unit each `Park` method is called in | S3 | S3 | `TestEachParkMethodIsCalledWhereItsContractSaysItIs` with the demanding-`Sequences` control (S3) |

### Use cases — Group AD, generations and cutover (ES-04)

| UC | Section | Checkpoint | Proved by |
|---|---|---|---|
| UC-158 a generation is built beside the running one | S4 | S6 | `TestAGenerationDrainsBesideTheLiveOneAndTheRowsAgree` (S6 live item 9, UC-120's assertion re-run under the naming) with the stopped-mid-drain control |
| UC-159 a barrier is observed and reached | S4 | S4 + S6 | `TestABarrierIsObservedAndReached` with the all-fresh control (S4); `TestTheBarrierTheCutoverAndTheRollback` (S6) |
| UC-180 a barrier from a set with an unreported member | S4 | S4 | `TestObserveRefusesACoverWhoseMemberHasNoRow` with the three-arm control (S4) |
| UC-200 a retiring cover no member of which holds a row | S4 | S4 | `TestACutoverRefusesARetiringCoverNoMemberOfWhichHoldsARow`, both arms with their three controls (S4) |
| UC-201 the retiring generation advances between the barrier and the switch | S4 | S4 | `TestTheCutoverWindowIsWhatTheRetiringGenerationAdvancedUnderIt` with the at-rest control (S4) |
| UC-160 the retiring generation cannot write into the arriving one | S4 | S6 | `TestARetiringGenerationCannotBeToldToWriteIntoTheArrivingOne` with the ignores-its-batch control (S6) |
| UC-161 the cutover switches the read target atomically | S4 | S6 | `TestTheCutoverSwitchesEveryTableAtOnceForAReaderInOneSnapshot` with the stale-reader negative arm (S6) |
| UC-162 two operators cut over at once | S4 | S4 + S6 | `TestTwoCutoversLeaveOneWinnerAndOneConflict` (S4); `TestTwoOperatorsCuttingOverAtOnce` (S6, gated) |
| UC-163 a cutover refused because the arriving generation has holes | S4 | S4 + S6 | `TestACutoverRefusesOnHolesAndNotOnQuarantined` (three arms, S4); `TestACutoverCannotBeHandedABarrier` (S6) |
| UC-164 a rollback | S4 | S4 + S6 | `TestARollbackIsTheSameCallExchangedAndErrRetiredWhenTheRowsAreGone` (S4); `TestTheBarrierTheCutoverAndTheRollback` (S6) |
| UC-165 a rebuild is cancelled | S4 | S6 | `TestACancelledRebuildResumesFromItsRowAndNeverFromTheOrigin` (S6) |
| UC-166 the old generation meets the new one's event type | S4 | S4 | `TestAnIgnoredTypeLetsTheOldGenerationKeepServing` with the uncovered-family control (S4) |
| UC-167 a rebuild is paced | S4 | S4 | `TestPaceThrottlesTheReadWhileDrainingAndNotWhileFollowing` with the `Pace: 0` control (S4) |
| UC-168 a generation's cost is read off the database | S6 | S6 | `TestEightWalksCostEightTimesOneProjectionsReads` (S6, a recorded number) |

### Use cases — Group AE, effects (ES-06)

| UC | Section | Checkpoint | Proved by |
|---|---|---|---|
| UC-169 a live projection stages and a rebuild does not | S5 | S5 | `TestTheLiveGenerationStagesAndTheRebuildDoesNot` with the flipped-row control (S5) |
| UC-170 `Effects` beside `AfterApply` | S5 | S5 | `TestEffectsAreRefusedBesideAfterApply` (S5) |
| UC-171 a warm-up is interrupted and resumes suppressed | S5 | S6 | `TestAnInterruptedWarmUpResumesSuppressed` with the fresh-start control (S6) |
| UC-172 a page straddles the barrier | S5 | S5 + S6 | `TestAStraddlingPageStagesExactlyTheEnvelopesPastTheBarrier` (S5); the live arm (S6) |
| UC-181 a page in which one envelope is parked and one is applied | S5 | S5 | `TestAParkedEnvelopeIsNotStagedAndIsNotLost` with the nothing-parked control (S5) |
| UC-182 a redrive stages the effect of the letter it applies | S5 | S5 + S6 | `TestARedriveStagesWhatItAppliesAndAnEvictionStagesNothing` (S5); the crash arm (S6) |
| UC-173 a retired generation stops staging at the cutover | S5 | S6 | `TestARetiredGenerationStopsStagingAtTheCutover` with the no-row fixture control (S6), driving the interleaving rather than taking the cutover between passes |
| UC-192 the ownership row is reached over a second pool | S5 | S6 | `TestAnOwnershipRowOverASecondPoolLeavesTwoSenders` (S6, one measured boundary of three) |
| UC-202 the cutover commits between two passes' ownership reads | S5 | S5 + S6 | `TestTheOwnershipReadIsABoundaryOnlyWhenACutoverWaitsForIt`, two recipes each the other's control (S5); the live pair (S6) — round 2, GAP-1 |
| UC-203 the same generation restarted with `EffectsAfter` dropped | S5 | S5 + S6 | `TestABarrierDroppedFromTheSpecRestagesTheWarmUpBelowIt` with the field-still-set control (S5); beside §6.12's interrupted warm-up (S6) — round 2, GAP-4 |
| UC-193 a generation-zero projection is retired by the first cutover | S5 | S5 | `TestAnUngeneratedProjectionWithGenerationsStopsStagingAtTheCutover` with the no-`Generations` control (S5) — **P-7** |
| UC-174 the ownership read costs nothing when nothing is staged | S5 | S5 | `TestTheOwnershipRowIsNotReadWhenThereIsNothingToStage` with the one-envelope control (S5) |
| UC-175 `EffectsAfter` beside `Park` | S5 | S5 | `TestABarrierBesideAParkIsRefused` with two controls (S5) |
| UC-176 `EffectsAfter` with no `Effects` | S5 | S5 | `TestABarrierWithNothingToGateIsRefused` (S5) |
| UC-177 the projection path reaches nothing that can dispatch | S6 | S6 | `TestNoPackageOnTheProjectionPathCanDispatch` with the `net/http` fixture control (S6) |
| UC-178 an effect sink is a staged job | S5 | S6 | `TestAStagedJobTheReadModelAndTheAdvanceCommitTogether` (S6) |
| UC-183 a stage is followed by a lost fence | S5 | S6 | `TestAStageRolledBackByALostFence` with the committing control (S6) |
| UC-184 a rebuild spelled as a second `Spec.Name` carries `Effects` | S5 | S5 | `TestASecondProjectionNameWithEffectsStagesEveryHistoricalEvent` with the generation-spelled control (S5) |

### Invariants

| INV | Section | Checkpoint | Proved by |
|---|---|---|---|
| INV-083 `InUnit` means one authority, and two are refused | S1 | S1 + S6 | UC-131/UC-132's pair, plus a recording checkpoint store asserting the comparison happens inside the unit, before the handler, **per pass and not once** |
| INV-084 what a foreign destination is promised, and what it is not | S6 | S6 | UC-134's kill campaign; UC-133's control; and a doc check that no page asserts the fifth |
| INV-085 a sequence key is total, pure and stable | **S1** | S1 | UC-135's control (an unstable sequencer leaves events applied twice or not at all, asserted); UC-144's halt against the handler-panic control; a doc check that no page claims the key is validated |
| INV-086 a partition is a mask, no key moves except by a split, no count is stored | S1 | S1 + S6 | UC-138 against its `hash % N` control (S6); UC-141's ceiling; and an AST check that no `%` is applied to a hash of a sequence key anywhere under `event/` |
| INV-087 a topology change is one transaction, refused without a parent row, holds no state | S2 | S2 + S6 | UC-138 counting the statements; UC-139's halt; UC-140's two absences with the has-run control; UC-185's twice-run unit; and an injected failure between the two child writes asserting neither child exists (S6) |
| INV-105 a declared partition set covers the key space exactly once | S1 | S1 | UC-186's gap and UC-187's overlap against the admitted-set control; UC-180's unreported member; UC-195's zero value |
| INV-107 only the first start of a projection chooses its topology | **S1** | S1 | UC-194's two-release sequence against both of its controls |
| INV-088 a cursor is never ordered, and a merge has no spelling | S6 | S6 | UC-142's `TestNoExportedFunctionOrdersOrTakesTwoCursors` with its `Merge` fixture control; `TestCursorIsNeverCompared` extended over the new files, in the same counted pattern |
| INV-089 a partitioned projection's progress is a `min` | S4 | S4 + S6 | UC-143 with unequal partitions and a single-member control; a doc check that no page calls a per-partition `Highest` the projection's progress |
| INV-090 a park blocks the sequence, in this page, in later pages, and across a split | S3 | S3 + S6 | UC-146, UC-147, UC-179 (S6, with the empty-park control), UC-190's refusals, and UC-198 for the "every configuration that constructs" clause — a `Park` beside another policy is inert rather than merely unused |
| INV-091 the park, the read model and the scan checkpoint commit together | S3 | **S6** | `TestTheBlockingTestAndTheAdvanceAreOneCommit` (S6) is the **only** proof of the commit-together half: over `eventmemory` the alignment is fabricated and the read model is a Go map, so S3's arms — an injected rollback after the park leaving the queue empty and the advance unmoved, the same injection inside a redrive, UC-190's refusal — falsify the *ordering and the rollback*, which is what a memory stand can falsify, and nothing about one commit |
| INV-092 a full park stops the partition and skips nothing | S3 | S3 + S6 | UC-150's three bounds; UC-151's two dimensions; the pass-after-eviction arm (S6) |
| INV-093 the park's bound is two-dimensional and per sequence | S3 | S3 | UC-151, and the honest half of that row: the two dimensions are the implementation's obligation and the framework's own arm is the cost of missing them |
| INV-094 a redrive is ordered, rotating, exclusive, one at a time, touches no checkpoint | S3 | S3 + S6 | UC-153's untouched `A4`; UC-154's rotation with its control; UC-191's two gated callers; a recording `Checkpoints` asserting zero saves across a redrive campaign (S6) |
| INV-095 "caught up" is never published while a sequence is parked, and the count is live | S3 | S3 | UC-156 with the clears-after-redrive control; UC-152's next-pass drop; UC-148's counts at both ends |
| INV-096 no counter is decremented, no applied position is published | S3 + S4 | S4 + S6 | UC-157; UC-138's split arithmetic asserting the sum across the set is unchanged; a surface walk asserting no exported `Position` is added beside `Progress.Highest` |
| INV-097 a generation is a name, its rows are its own, two identities never render one name | S1 + S4 | S1 + S6 | UC-145's round trip; UC-188's refusal with its control; UC-189's shared park; UC-158 and UC-160's boundary control (S6); plus an injectivity property test over generated identities |
| INV-098 a cutover derives its own evidence, is one fenced write, refused without it | S4 | S4 + S6 | UC-161 counting one write and measuring atomicity from a reader; UC-162's race; UC-163's three arms; UC-164's `ErrRetired`; UC-180's "no field to hand a barrier through" |
| INV-099 a barrier is a `Position`, never a resume point | S4 | S4 + S6 | UC-159 with its all-fresh control; the position→cursor surface walk extended over the new packages |
| INV-100 an effect capability is a value, a rebuild has none, `Stage` is a write | S5 | S5 + S6 | UC-169 with its ownership control; UC-170's refusal; UC-183's lost fence (S6); UC-184's measured edge; and a surface check that `Batch` carries no `Effects`, no dispatcher and no context key |
| INV-101 the effect gate rests on three things, and what each rests on is stated | S5 | S5 + S6 | UC-171 and UC-203's dropped constant, UC-172, UC-181 on both appliers (UC-147), UC-182 with its refused barrier, UC-173 with its no-row fixture, UC-202's two recipes, UC-192's second-pool boundary, UC-174's call count |
| INV-106 an effect belongs to an applied envelope, not to a page | S5 | S5 | UC-181 with its nothing-parked control **on both appliers** — the isolation pass and the blocking one (UC-147), which hold the owed envelopes in two variables; UC-182's eviction arm and its refused barrier; UC-172's straddling page |
| INV-102 the framework contracts against dispatch and does not sandbox it | S6 | S6 | UC-177's walk with its fixture control, and a doc check that the honest sentence is present |
| INV-103 nothing starts, opens a transaction, or writes a line | S1…S5 | S1 + S6 | `startsNothing` extended over every new file; `TestNothingInTheProjectionPackageOpensATransaction` — **written in S1, because no such check exists today** — with its reported fixture control; the dependency budget row (`./runtime`) unchanged; and a construction test asserting `Split`, `NewRedrive`, `Observe` and `Cutover` issue nothing until called with a context |
| INV-104 every refusal names its field and wraps a published sentinel | S1…S5 | S5 | `TestNewRefusesEverySpecItCannotAssemble` extended with this phase's combinations, asserting a spec wrong in three places reports three problems — inside S5's counted `EFF` pattern, not in an arm of its own |

### The appendices, and where each is delivered

| Appendix | Delivered by | The clause of its *адаптация* that decides the shape |
|---|---|---|
| **ES-01** durable subscriptions, atomic SQL projection | **S1** (the alignment comparison, the tier table, the foreign contract) + **S6** (live) | *«выбранный SQL-профиль фиксирует effect + checkpoint одной transaction authority»* — one, and phase 3's `checkUnit` proved two exist and never that they were the same one |
| **ES-02** parallel subscribers, order within a sequence | **S1** (`Sequencer`, `Partition`, `Cover`, `Identity`, the page filter, the resume-time topology refusal) + **S2** (`Split`, the `eventtest` topology sections) + **S6** (live) | *«Изменение числа partitions требует согласованной передачи позиции, не замены `hash % N` на ходу»* — the count is never changed; a partition is split, and the handoff is one transaction |
| **ES-03** a DLQ that preserves causal order | **S3** + **S6** (live) | *«parking и scan checkpoint атомарны … overflow останавливает затронутую partition, не пропускает событие … skip — явная операторская операция с отметкой неполноты»* |
| **ES-04** rebuilding beside the running one | **S4** + **S6** (live) | *«generation имеет отдельные данные, checkpoints и claims … Переключение read target атомарно … Rollback допустим, пока …»* |
| **ES-06** a replay that does not resend emails | **S5** + **S6** (live) | *«отдельные projection/effect handlers; rebuild не получает effect-dispatch capability … durable граница владения live effects не допускает двух отправителей … Это не sandbox»* |
| ES-05, ES-07, ES-08, ES-09 | **not this phase** | §2's non-goals; §1.5 shows ES-04's readiness does not need ES-05, which was the study's third open question |

---

## Contracts before code

Every signature below is written before the code, by file and by package.
Receiver name is `this` throughout. Comments are exceptional; the ones quoted are
the ones a genuinely complex function or a non-obvious invariant earns. **Nothing
lands in `event/`** — the vocabulary is unchanged, and the three symbols
`event/projection` starts using for a new purpose (`Backing`, `NewAuthority`,
`Authority.Same`) are already exported.

### `event/projection/errors.go` — every sentinel this package publishes  *(modified)*

**All four new sentinels are declared here and nowhere else**, in the one `var`
block the package already has. That is not tidiness: a sentinel declared in the
file that first wraps it puts `ErrTopology` in an S2 file while S1's
`NewPartition`, `Partition.Split` and `NewCover` all need it to build, and S1's
manifest fence forbids the S2 file — so S1 could neither compile nor pass. One
home, named once, in every section's allowed set that needs it.

```go
var (
	ErrSpec      = errors.New("projection: this projection cannot be assembled from this spec")
	ErrHalted    = errors.New("projection: this projection stopped advancing and is not applying events")
	ErrOvertaken = errors.New("projection: a second live writer at this projection name holds the checkpoint")
	ErrUnrouted  = errors.New("projection: this envelope's type is of a family this router routes and no route claims it")

	ErrTopology  = errors.New("projection: this topology change is not one this projection can make")
	ErrParkFull  = errors.New("projection: this park has no room for another letter, and the partition stops rather than skipping one")
	ErrClaimLost = errors.New("projection: this sequence's claim is no longer this caller's, so nothing it was about to write may land")
	ErrRetired   = errors.New("projection: this generation's checkpoints are gone, so nothing may be pointed back at it")
)
```

Which section declares which, and the file is in that section's `Files` list and
its `event-kernel-moved` allowed set:

| Sentinel | Declared in | Wrapped by |
|---|---|---|
| `ErrTopology` | **S1** | `NewPartition`, `Partition.Split`, `NewCover` (S1); `Split` (S2); `Redrive`'s sequencer and partition refusals (S3); `Observe`, `Reached` (S4) |
| `ErrParkFull` | **S3** | a `Park.Park` the queue has no room for |
| `ErrClaimLost` | **S3** | a `Redriver` write whose claim no longer owns the sequence (**P-8**) |
| `ErrRetired` | **S4** | a `Cutover` whose target's checkpoint rows are gone |

The header comment — *"Four, and none of them crosses a store seam:
construction, lifecycle, contention and routing"* — stops being true in S1 and is
rewritten there, in the same change, to the five kinds it now names:
construction, lifecycle, contention, routing and **topology**. The store-seam
clause is unchanged and is the reason `ErrClaimLost` reads the way it does: what
a `Redriver` refused is the application's own table refusing, and it travels as
this package's sentinel rather than as a second vocabulary.

**Correction, made in S1:** the count in that comment moves *with the sentinels*,
not to eight at once. S1 wrote **five**, because a header claiming eight over a
`var` block holding five is exactly the drift this repository treats as a defect;
S3 moves it to seven and S4 to eight, each in the section that adds the sentinel.

### `event/projection/identity.go` — the one place a recorded name is built  *(new)*

Exported because it is the primary key of a checkpoint row, a park and a runner
name at once, and because §UC-145 says a caller must not assemble it by hand.
Opaque because `Identity{Projection: "orders@2"}` would render a string that
parses back as another identity — [[D-125]]: a composed key is a wire format.

```go
// Rendered into every name a projection records through — the checkpoint row key
// and the runner name — and parsed back out of one. "orders", "orders@2",
// "orders#3.7", "orders@2#3.7". Within event.MaxNameBytes and carrying neither
// bracket, so it passes the kernel's identifier rule.
type Identity struct {
	projection string
	generation Generation
	partition  Partition
}

type Generation uint32

const Ungenerated Generation = 0

func NewIdentity(projection string, generation Generation, partition Partition) (Identity, error)
func ParseIdentity(text string) (Identity, error)

func (this Identity) Projection() string
func (this Identity) Generation() Generation
func (this Identity) Partition() Partition
func (this Identity) Whole() Identity
func (this Identity) String() string
```

- `NewIdentity` refuses, each wrapping `ErrSpec` and naming the field: an empty
  projection; a projection containing `@` or `#`; a rendering longer than
  `event.MaxNameBytes`. **It does not re-implement `checkName`** — that is
  unexported kernel and §5.1 keeps it there. `New` passes `identity.String()` to
  `event.Track`, which is the definitive door, and its refusal is collected the
  same way.
- **Escaping was rejected** and the reason goes on the type: `orders@2` would
  render `orders%402`, its checkpoint row key would change under a live
  projection, and it would resume from the origin against a live read model — the
  silent version of the failure the refusal makes loud at boot (§UC-188).
- `ParseIdentity` refuses every string `String()` never produces — `orders@0`,
  `orders#0.0`, a non-numeric generation, a malformed partition — because
  injectivity is what makes a name safe as a primary key (§INV-097).
- `Whole()` drops the partition and keeps the generation. It is what a `Park` and
  a `Redriver` are keyed by, and §1.4 is why: a partition-keyed park orphans its
  letters the moment the partition holding them is split.

### `event/projection/partition.go` — a mask, not a modulus  *(new)*

```go
const MaxPartitions = 1024

type Partition struct{ id, mask uint32 }

func Whole() Partition
func NewPartition(id, mask uint32) (Partition, error)
func ParsePartition(text string) (Partition, error)

func (this Partition) Matches(sequence string) bool
func (this Partition) Split() (Partition, Partition, error)
func (this Partition) Mask() uint32
func (this Partition) ID() uint32
func (this Partition) Count() int
func (this Partition) Whole() bool
func (this Partition) String() string   // "3.7", and "" for Whole

// FNV-1a/32 over the UTF-8 bytes of the sequence key. Published, and never
// changed: changing it moves every key, which is the hash % N failure by another
// door. [[D-125]] — a composed key is a wire format, and so is a hash.
func hash(sequence string) uint32
```

`Matches` is `this.mask == 0 || this.mask&hash(sequence) == this.id`.
`Split` answers `{id, 2m+1}` and `{id + (2m+1 ^ m), 2m+1}`, refusing at the
ceiling with `ErrTopology` naming it. `NewPartition` refuses a mask that is not
`2^k − 1`, a mask at or above `MaxPartitions`, and an id above the mask.
`Count()` is `mask+1` for a reader and is stored nowhere — backlog `## P4` item 9
(`[medium]`) records the objection and stays open.

### `event/projection/cover.go` — the set is the thing that has to be right  *(new)*

Exported because a gap is silent permanent data loss and a lone runner cannot
see the set (§1.3). `Observe`, `Reached` and `Cutover` take a `Cover` and never a
`[]Partition`, so no `min` in this phase is a `min` over a hole.

```go
type Cover struct{ partitions []Partition }

func NewCover(partitions ...Partition) (Cover, error)

func (this Cover) Partitions() []Partition   // a copy, so a caller cannot re-open the set
func (this Cover) Count() int
```

`NewCover` refuses on two exact arithmetic facts and nothing heuristic, each
wrapping `ErrTopology`: two members overlap when
`(idA ^ idB) & min(maskA, maskB) == 0`, and a set with no overlap covers the
space when `Σ MaxPartitions/(mask+1) == MaxPartitions`. An empty set is refused.
`Whole()` alone is a legal cover of one.

### `event/projection/sequence.go` — who names a sequence  *(new)*

```go
// A sequence is the set of events that must be applied in the order the log
// holds them: two envelopes with one key are ordered with respect to each other,
// and two with different keys are not.
//
// SequenceOf is total, pure and stable for the life of the log. There is no
// error: an envelope that could not be assigned to a sequence has no sequence to
// be parked in, so a panic out of one halts the projection rather than failing
// its page.
type Sequencer interface {
	Name() string
	SequenceOf(envelope event.Envelope) string
}

func ByStream() Sequencer                                        // the nil default
func Unordered() Sequencer                                       // stable, not random
func OneSequence() Sequencer
func SequenceBy(name string, of func(event.Envelope) string) Sequencer
```

- `ByStream()` — **`string(event.Compose(envelope.Stream.Family, string(envelope.Stream.Key)))`**,
  not `Stream.String()`. See **P-1**. `Name()` is `"by-stream"`.
- `Unordered()` — the envelope's position rendered as decimal text. Stable, so a
  re-delivery after a restart lands in the same partition; Axon can hand a
  null-sequence event to any segment because one coordinator owns them all, and
  vv's partitions are separate processes.
- `OneSequence()` — one constant key.
- `SequenceBy` refuses a nil function and an empty name at construction (it
  answers a `Sequencer` whose use `New` refuses, collected as `ErrSpec`).

### `event/projection/topology.go` — the handoff  *(new)*

`ErrTopology` is declared in `errors.go` and by S1, not here — see that block.

```go
type SplitSpec struct {
	Checkpoints event.Checkpoints
	Identity    Identity
	Unit        func(ctx context.Context, work func(context.Context) error) error
}

func Split(ctx context.Context, spec SplitSpec) (Identity, Identity, error)
```

Five steps, and the two that look redundant are the point (§1.3). In **one**
transaction of the checkpoint store's backing — the caller's `Unit`,
`crud.InNewTx` being the one-line spelling — and **every call through
`event.Track`, one tracker per identity, never against the raw store** ([[D-129]]:
the tracker door is the only caller of a `Checkpoints`, and this phase does not
make itself the exception):

1. `Load` the parent and **both** children.
2. Refuse an absent parent with `ErrTopology`, naming both readings and both
   remedies — a partition that never ran needs no split, and one whose row was
   lost is [[D-133]]'s restore case. The message names any child row it found,
   which is the "the first attempt committed after all" reading.
3. Refuse a child row beside a live parent.
4. `Save` both children at advance 1 carrying the parent's cursor **byte for
   byte** **and the parent's `Progress.Highest`**; the lower-numbered child takes
   the parent's `Applied` and `Quarantined`, the higher starts both at zero, so
   the sum across the set is unchanged. `At` is the split's own instant.
5. `Forget` the parent.

**`Highest` is enumerated because the tracker door will not enumerate it for
you.** `admit` deliberately does not make `Progress` total
(`event/checkpoint.go:149-191`) — *"Progress is an observation nobody resumes
from"* — so a child written at `Highest: 0` is a legal row that nothing refuses.
`Observe` answers the **lowest** `Highest` across the cover and `Cutover` derives
its barrier from `Observe`. Split a live `orders` at position 900_000 with
`Highest` dropped and the next `Observe` answers `0`: `Reached` is trivially true
for any arriving generation, `Cutover` refuses nothing, and the read target
switches to a generation that has applied nothing. [SPEC] §1.3 says *"both
children take the parent's `Cursor` **and its `Highest`**"*, and this is the one
write that has to honour it.

Every decision comes from rows read inside the run and nothing is carried between
runs, so a `Unit` that runs its body twice ([[D-130]]) produces one split or a
refusal and never two children at the origin (§UC-185). No `Merge` is exported;
`ErrTopology` names the refusal if one is attempted through `Split`'s inverse.

**Two additions made in S2, both from [[D-130]]'s "the caller owns `Unit` and may
return anything".** Step 0 is `inACallersTransaction`: inside the unit and before
anything is read, `Tracker.Transaction(ctx)` must answer a valid authority, or
the call is `ErrSpec` naming `Unit` — a unit that opened nothing leaves three
writes the store commits one at a time, which is the half-made handoff this whole
shape exists to refuse, and the pass already asks exactly this question in
`checkUnit`. And what `Split` answers is read against **what the body reached**,
never against what the unit returned: a body that refused travels as that refusal
even when the unit answers `nil`, and a unit that never ran the work at all is
`ErrTopology` wrapping the shipped `errUnitRanNothing`. Both are driven by
subtests of `TestASplitUnderATwiceRunUnitIsOneSplitOrARefusal`.

### `event/projection/park.go` — the queue  *(new)*

`ErrParkFull` is declared in `errors.go`, by this section — see that block.

```go
// A parked envelope. Cause is the failure for the letter that could not be
// applied and nil for every letter parked behind it: those never reached a
// handler, which is the whole of what the queue is for.
type Letter struct {
	Identity  Identity
	Sequencer string
	Sequence  string
	Envelope  event.Envelope
	Cause     error
	Attempt   int
}

type Park interface {
	Sequences(ctx context.Context, of Identity) (uint64, error)
	Holds(ctx context.Context, of Identity, sequence string) (bool, error)
	Park(ctx context.Context, letter Letter) error
	Holes(ctx context.Context, of Identity) (uint64, error)
}
```

The `Identity` is **always** `Identity.Whole()`, which the framework is the only
producer of, so an implementation keying its table on the value it is handed is
correct by construction (GAP-10's closure). The letter carries no effect field:
it carries its **envelope**, and the redrive re-derives the effect from it.

**Where each method runs is part of the contract and the four differ**, so the
interface's own comment states it per method rather than collectively. `Holds`
and `Park` run **inside** the caller's unit — that is what orders a redrive's
eviction against the loop's blocking test. `Sequences` runs **outside** it:
`counted` is called from `once`, before `deliver` opens a unit, because a healthy
projection would otherwise open a transaction per pass to be told the queue is
still empty. `Holes` is a cutover's question and no pass asks it. An
implementation that requires the ambient transaction in `Sequences` fails every
pass, and `counted` reads that as a **postpone** — so the projection retries for
ever without advancing and without halting, which is why the sentence is the
contract's and not a note.

**The bound is the implementation's and this package can neither call it nor
certify it.** There is no `isFull` on the interface; what is declared is
`ErrParkFull` and what a pass does about it. §UC-151 is therefore an obligation
on the implementation rather than a property of the framework, and what a test
here can hold is what getting it wrong **costs**: a one-dimensional `isFull()`
refuses the events behind a blocker in a sequence the queue already holds, so the
partition sits in `PhaseBlocked` with the blocked sequence's own room unused.
That control is what §UC-151's arm asserts, beside the reference queue's own two
dimensions.

### `event/projection/redrive.go` — the operator's half  *(new)*

`ErrClaimLost` is declared in `errors.go`, by this section. The claim protocol is
**P-8**'s and not [SPEC] §5.2's.

```go
// Minted by the implementation, opaque here: this package compares nothing in a
// Claim and only hands it back. It is total — the identity and the sequence are
// in it, so a call cannot name one sequence while holding another's grant.
// Until is the deadline the implementation granted, published so an operator can
// size a unit against it; the framework reads no clock and enforces no expiry.
type Claim struct {
	Of       Identity
	Sequence string
	Token    string
	Until    time.Time
}

type Redriver interface {
	Claim(ctx context.Context, of Identity, sequence string) (Claim, bool, error)
	Sequence(ctx context.Context, claim Claim) ([]Letter, error)
	Evict(ctx context.Context, claim Claim, letter Letter) error
	Touch(ctx context.Context, claim Claim, cause error) error
	Release(ctx context.Context, claim Claim) error
}

type RedriveSpec struct {
	Identity     Identity
	Handler      Handler
	Sequencer    Sequencer
	Park         Redriver
	Unit         func(ctx context.Context, work func(context.Context) error) error
	Destination  any
	Effects      Effects
	EffectsAfter event.Position
	Generations  Generations
}

type Redrive struct{ spec RedriveSpec }

func NewRedrive(spec RedriveSpec) (*Redrive, error)

func (this *Redrive) Sequence(ctx context.Context, sequence string) (Retried, error)
func (this *Redrive) Any(ctx context.Context) (Retried, error)

type Retried struct {
	Sequence string
	Applied  int
	Left     int
	Cause    error
}
```

`Claim` is the rotation **and** the exclusion, and it is the mechanism the study
recovered (`claimDeadLetter` / `processingStarted`) and an earlier draft of
[SPEC] dropped. `Release` is called on every exit path, including a panic. The
clock that expires an abandoned claim is the application's, in the application's
table — [[D-126]] rules out a store's clock and a fence, not this — and **because
it expires, the grant travels on every write it authorises** (**P-8**). An
implementation refuses a `Sequence`, `Evict`, `Touch` or `Release` whose claim it
no longer holds, with an error wrapping `ErrClaimLost`.

**One unit per letter**, so a crash between the apply and the `Evict` leaves the
letter parked and nothing applied, and the next redrive starts that sequence
again from its first letter. Inside one unit: the tier check (**P-2**), the
handler over a `Batch` of one, the effect stage, the `Evict`. On a handler
failure the unit rolls back and `Touch` runs in a unit of its own, then the call
answers `Retried{..., Cause}` with a **nil** error — the redrive did not fail;
the letter did.

**A lost claim is not a letter failing.** `Evict` runs inside the letter's unit,
so a refusal there rolls the apply back with it — which is the whole reason the
grant has to travel on the write and not only on the read. On `ErrClaimLost` the
redrive stops the sequence at once, issues no `Touch` and no `Release` (it owns
neither), and answers `Retried` carrying what it applied *before* the loss beside
an error wrapping the sentinel. A host that wrapped `Redrive.Any` in a
`runtime.Runner` reads that sentinel as "somebody else has it", not as a
permanent failure.

`NewRedrive` refuses: a nil `Handler`, `Park` or `Unit`; a `Destination` of
`Unchecked` or nil; an `Identity` carrying a partition (`ErrTopology`); `Effects`
at a generation with no `Generations`, exactly as `New` does; **and a non-zero
`EffectsAfter`, which `New` accepts.** That asymmetry is the one place the two
doors part and it is the queue that parts them: a letter is in the park because a
loop parked it, a loop parks only under `ParkSequence`, and `New` refuses that
beside a barrier — so every letter a redrive can drain was parked by a generation
that had none and is owed its effect, and a barrier here can only suppress it for
ever. The field is present **to be refused** rather than absent, because the spec
builder that serves a loop and a redrive is exactly the shape that copies it
across, and a refusal at that call site names the rule where a missing field
would name nothing. [SPEC] §1.6's "the same three" is corrected to say so
(round 2, GAP-3).

**There is no `Classifier` on a `RedriveSpec`, and the omission is the answer to
the mechanism's `EnqueuePolicy`** rather than an oversight — an earlier draft of
this block carried one, it was published, defaulted to `Classify` and read by
nothing, and a caller who filled it in got the same forever-requeue as one who
did not. Both classes end at the same place here: the letter is requeued with its
new cause, because the alternative is to remove a letter without applying it and
this framework removes nothing. `scripts/projection_test.go`'s
`TestEveryFieldOfAPublishedSpecIsRead` is what holds that, over the field object
rather than over the name.

**The framework suppresses the `Release` on a lost claim** and does not rely on
the implementation refusing it: nothing in the `Redriver` contract makes
`Release` compare the token, so an idempotent `DELETE … WHERE sequence = $1` is a
fair spelling of it, against which `claimed`'s guard is the only thing between a
loser and freeing the winner's sequence mid-drain. The `*park` fake counts the
call, so the arm asserts zero rather than asserting an outcome the fake's own
`owns()` would produce either way.

### `event/projection/generation.go` — the barrier, the readiness and the one write  *(new)*

`ErrRetired` is declared in `errors.go`, by this section — see that block.

```go
// The row your read path resolves its tables through, and the one write a
// cutover is. ONE ROW PER PROJECTION across all its generations — it names the
// winner, so a row per generation could not — which is why this is keyed by a
// projection name and a Park is keyed by an Identity.
//
// THE PRECONDITION TRAVELS WITH THE PROMISE: the switch is atomic for every
// table at once FOR A READER THAT RESOLVES THIS ROW IN THE SAME SNAPSHOT AS THE
// TABLES IT THEN READS. A read path that caches the answer per process, per
// request or per connection is a legitimate choice with a window the length of
// its cache, and what it may not be called is atomic.
//
// A projection with no row answers Ungenerated and a nil error. That is the
// state of every deployment that has never cut over, and it is what lets a live
// Ungenerated projection carry a Generations before there is anything to own:
// it stages, because the row names its own generation. An error there would
// postpone a live projection for ever (P-3, P-7).
type Generations interface {
	Active(ctx context.Context, projection string) (Generation, error)
	Activate(ctx context.Context, projection string, from, to Generation) error
}

type Barrier struct {
	Projection string
	Generation Generation
	At         event.Position
}

func Observe(ctx context.Context, checkpoints event.Checkpoints, of Identity, over Cover) (Barrier, error)

type Readiness struct {
	Reached     bool
	Behind      event.Position
	Quarantined uint64
	Holes       uint64
}

func Reached(ctx context.Context, checkpoints event.Checkpoints, park Park, barrier Barrier, arriving Identity, over Cover) (Readiness, error)

type CutoverSpec struct {
	Checkpoints event.Checkpoints
	Generations Generations
	Park        Park
	Projection  string
	From, To    Generation
	Retiring    Cover
	Arriving    Cover
	Unit        func(ctx context.Context, work func(context.Context) error) error

	AcceptQuarantined bool
}

func Cutover(ctx context.Context, spec CutoverSpec) error
```

- `Observe` answers the **lowest** `Highest` across the cover, composing one row
  key per member with `of` and reaching each through `event.Track`. All members
  fresh → the origin, and that is the true answer. Some fresh and some not →
  `ErrTopology` naming the member with no row (§UC-180). `of` carrying a
  partition → `ErrTopology` (**P-4**).
- **Amended by the S4 review (GAP-1):** an absent member is asked one further
  question — `retired(...)`, the row `Split` writes — and a member with no row of
  its own **beside a retirement row** is `ErrTopology` naming both. It is §UC-180
  one level down: the rows tell a share that never ran apart from one that ran and
  handed its cursor to two children, so folding the second in as position zero is
  the same lie with a second spelling. The origin answer survives untouched for a
  cover with no rows *and* no retirements (§UC-159's control), which is the
  discriminating pair the two controls assert.
- `Reached` asks `Holes` **once** of the arriving generation's park, because a
  park is keyed by a whole identity. A nil park answers zero by construction.
- **Added in S4:** `Reached` refuses a `Barrier` whose `Projection` is not the
  arriving generation's — which is how the zero `Barrier` is refused, since it
  names none — and one whose `Generation` **is** the arriving generation's, which
  would be a set measured against its own rows. **P-11**'s door rule, at the one
  value it could not reach.
- **Added in S4:** `Cutover` asks `Split`'s question of the caller's unit —
  `Tracker.Transaction(ctx)` must answer a valid authority — inside the unit and
  before anything is read, `ErrSpec` naming `Unit`. What that buys is the
  *arriving* generation's rows and the ownership row moving together; what it does
  not buy is a retiring generation that stands still, and the two are now stated
  apart (see the overlap window below).
- **`Cutover` takes no barrier.** Inside the caller's unit it `Observe`s one from
  the retiring generation's own rows, `Reached`es the arriving one against it,
  refuses, and then issues exactly one fenced `Activate`. There is no field
  through which a zero can be handed in (GAP-2's closure).
- **Amended by the S4 review (GAP-1):** and no field through which a zero can be
  *derived* either. A retiring cover no member of which holds a row is
  `ErrRetired`, with the same force as the arriving arm and for a different
  reason: there the generation has nothing behind it, here it has nothing to say,
  and the barrier folded from that silence is the origin, which every arriving
  generation clears by `x >= 0`. The refusal names both readings the rows cannot
  tell apart — a cover that is not the one this generation records at (a live
  four-partition generation declared as `Whole()`), and a generation nothing ever
  recorded for. Standing a read target up where nothing preceded it is a row the
  application's own `Generations` writes; it is not a switch this call derives.
- **Amended by the S4 review (GAP-2) — the overlap window is named and not
  closed.** The barrier is the retiring generation's watermark *as this call read
  it*. That generation is a separate runner committing in its own transaction;
  nothing here claims, locks or fences its rows, and [[D-126]] forbids reaching
  for an isolation level (which would not help — `REPEATABLE READ` makes the
  barrier the snapshot value, also stale-low). So if it is still advancing, reads
  move **backwards** at the switch by its advance over the life of the caller's
  transaction, and stay there until the arriving generation catches up. Written
  down where an operator reads it, in Marten's own shape: drain or stop the
  retiring generation before, or as, the switch commits; `Observe` it twice and
  see whether the barrier moved; `Spec.Pace` on the arriving generation lengthens
  the recovery, so drop it first — and `Spec.Pace`'s own field comment says so.
  **The alternative is adjudicated rather than left unmentioned:** Axon's
  `resetTokens` requires the processor shut down and then claims every token in
  one transaction. Neither half is taken. This call cannot stop a runner in
  another process, and claiming a live generation's checkpoint rows means writing
  them — which takes that runner's fence away, to buy a window an operator closes
  by draining. Driven: the mutation that adds that claim answers *"a checkpoint row
  moved between the save this transaction staged and its commit"*, which is the
  fence fight the refusal is about.
- It refuses on `Readiness.Holes` and **never** on `Progress.Quarantined`, which
  is cumulative and would refuse a fully recovered generation for ever
  (GAP-18's closure). `AcceptQuarantined` therefore means what its name says.
- A rollback is this call with `From` and `To` exchanged; a target whose rows are
  gone is `ErrRetired`. Symmetry is not elegance — it is what makes the rollback
  path exercised by the same tests as the forward one.

### `event/projection/effect.go` — the capability that is a value  *(new)*

```go
// What an effect handler is handed, and the value that IS the capability: a
// projection Handler has no route to one. The envelopes are the ones this page
// APPLIED, past the barrier, and no others — a parked envelope is not among them
// and is not lost either: its letter carries its effect, and the redrive that
// applies it is what stages it. An evicted letter's effect is never staged.
type Effect struct {
	Identity  Identity
	Envelopes []event.Envelope
	Attempt   int
}

// Stage, not Send. It is called INSIDE the transaction that commits the advance,
// so what it does must roll back with it: a staged job ([[D-118]]), a row in your
// own tables. An externally visible irreversible action here — an HTTP call, a
// payment, a mail, a non-transactional publish — is sent again on every rollback
// the pass can take, and a lost fence ([[D-133]]) makes that the expected outcome
// of a rolling deploy. The dial-out belongs to whatever drains the stage.
type Effects interface {
	Stage(ctx context.Context, effect Effect) error
}

type EffectsFunc func(ctx context.Context, effect Effect) error

func (this EffectsFunc) Stage(ctx context.Context, effect Effect) error
```

`Stage` and not `Dispatch`, because `Dispatch` is a lie about where it runs
(GAP-4's closure). `Effect.Envelopes` is never empty: a page with nothing to
stage does not call `Stage` at all.

### `event/projection/classify.go` — three renames  *(modified)*

| Was | Is | Why |
|---|---|---|
| `Quarantines` (interface) | `Park`, and it moves to `park.go` | it blocks the sequence now; the old name described a sink that did not |
| `Quarantined` (struct) | `Letter`, and it moves to `park.go` | it is a queue entry with an order, not a record of a skip |
| `Failure`'s `Quarantine` (const) | `ParkSequence` | a package-level `const Park` and a package-level `type Park` cannot coexist in one Go package, and the new name says the thing that is new: the **sequence** is what is parked |

`Progress.Quarantined` — the kernel field — keeps its name and its meaning.
`Spec.Quarantine` becomes `Spec.Park`, a field, which collides with nothing. A
consumer meets each as a compile error; §7.3 and the module pages say so.

### `event/projection/spec.go` — eight fields and eleven refusals  *(modified)*

```go
type Spec struct {
	// … every existing field, unchanged, except Quarantine Quarantines -> Park Park

	Sequence     Sequencer       // nil is ByStream()
	Partition    Partition       // the zero value is Whole()
	Generation   Generation      // Ungenerated renders nothing
	Park         Park
	Generations  Generations
	Effects      Effects
	EffectsAfter event.Position
	Pace         time.Duration
}
```

New refusals, all wrapping `ErrSpec`, all naming their field, all **collected**
(§INV-104):

1. `NewIdentity` refused the name — the delimiter or the length (§UC-188).
2. `OnPermanentFailure: ParkSequence` with a nil `Park`.
3. `OnPermanentFailure: ParkSequence` outside `Advance: InUnit` (§UC-190).
4. `OnPermanentFailure: ParkSequence` with `Destination: Unchecked` (§UC-190).
5. `Effects` beside `Advance: AfterApply` (§UC-170).
6. `EffectsAfter` set with a nil `Effects` (§UC-176).
7. `EffectsAfter > 0` beside `OnPermanentFailure: ParkSequence` (§UC-175).
8. `Effects` set with `Generation != Ungenerated` and a nil `Generations`.
9. `Pace < 0`.
10. A `Sequence` whose `Name()` is empty, or which is a `SequenceBy` over a nil
    function.
11. `Generations` or `Park` supplied where nothing uses them — **not** refused,
    because refusing them would break a composition root that wires one spec
    builder for a live generation and a rebuild. **They are inert rather than
    merely unused, and that is a line of code and not a hope**: `withDefaults`
    drops `spec.Park` unless `OnPermanentFailure` is `ParkSequence`. Gated on the
    field alone — which is what `delivering`, `counted` and `matching` each
    tested for — a `Park` beside `Halt` at `AfterApply` runs the whole blocking
    and parking path from `outsideAUnit`: `Holds` per envelope and a letter
    written with no transaction to roll it back, so a postponed pass parks the
    same envelope twice, `Park.Holes` is inflated for ever while
    `Progress.Quarantined` reads one, and a later `Redrive` applies the duplicate
    twice. It is dropped in **one** place rather than tested at each of the three
    that reach for it. And a `Generations` beside `Effects` at `Ungenerated` is
    not one of those cases: after **P-7** it is used, and it is the migration
    path — the release that adds it is what makes the next release's cutover
    safe.

`withDefaults` gains `Sequence = ByStream()` when nil. Nothing else defaults:
`Partition`'s zero value already **is** `Whole()`, and `Generation`'s zero value
already **is** `Ungenerated`, which is the property that keeps every projection
that exists today at its own name and its own row.

### `event/projection/pass.go` — the alignment, the filter, the park and the stage  *(modified)*

**`checkUnit` gains the comparison**, and it is three shipped calls with no new
surface (§1.1):

```go
authority, err := this.tracker.Transaction(inner)                 // already there
executor, bound := crud.ExecutorFor(inner, this.spec.Destination) // already there
mine, err := event.NewAuthority(this.tracker.Backing(), crud.KeyOf(executor))
aligned := err == nil && authority.Same(mine)
```

Tier A proceeds; tier B is `ErrSpec`, a halt, per pass, inside the unit, before
the handler, and it never reaches the classifier ([[D-130]] forbids the
downgrade by name); `Unchecked` makes no comparison at all. `event/projection`
still may not import `crud/adapter/crudsql` — `crud.KeyOf` is in `crud`, which is
already in the package's dependency budget (`scripts/event_test.go:45-49`), so
**the budget row does not move**.

**The dispatch**, replacing `wholePage`/`oneAtATime`:

- `matchedPage` — the page filtered by `this.spec.Partition.Matches(sequence)`.
  A page that matches nothing advances once with `Applied` risen by zero and
  **does not call the handler** (§UC-136). `presentSave` keeps using the *read*
  page's last position, not the matched subset's, which is what makes that true.
- While the loop believes the parked count is non-zero, each matched envelope is
  asked `Park.Holds` and a held one is parked without reaching the handler
  (§UC-147). While the count is zero, `Holds` is never called (§UC-148).
- On a permanent page failure under `ParkSequence`, the isolation pass
  `sequenceBySequence` re-delivers one envelope at a time in position order, and
  on a permanent failure parks that envelope **and every later envelope of the
  same sequence in this page** without calling the handler for them (§UC-146).
  That is the whole difference between a dead-letter queue and a skip list, and
  it is the largest gap in the shipped tree: `oneAtATime` continues to the next
  envelope of the same stream today.
- A panic out of a **sequencer** halts and is not recovered into a page failure:
  `applyPage`'s recovery does not extend there, because an envelope that could
  not be assigned to a partition has no sequence to be parked in (§UC-144).

**The count's one clearing rule**, stated once because three of them is how an
invariant becomes false without anybody editing it: `Park.Sequences` is read
**once per resume** — a process start, or an `overtaken` that adopted another
instance's row — **and again at the start of every pass while the count this loop
believes is non-zero.** A healthy projection pays one call per resume and nothing
per pass; a degraded one pays one call per page and clears at the next pass after
a drain (§UC-148, §UC-152, §UC-156, §INV-095).

**The stage**, last inside the unit and after the handler, cheapest suppressor
first (§1.6):

1. `spec.Effects == nil` → nothing is called and no round trip is made.
2. the applied envelopes past `spec.EffectsAfter` → empty stops here, still with
   no round trip.
3. `spec.Generations != nil` → `Generations.Active(inner, spec.Name)` through the
   checkpoint store's ambient transaction; an answer other than `spec.Generation`
   stops here.
4. `Effects.Stage(inner, Effect{...})`.

**Suppressor 3 is gated on the capability, not on `spec.Generation`** — that is
**P-7**, and it is the difference between a boundary that closes and one that
does not. Gated on `spec.Generation != Ungenerated` it never runs for a
projection that exists today, so the first `Cutover(From: Ungenerated, To: 2)`
of any live deployment leaves the retiring `orders` staging beside `orders@2`
for ever. Gated on the capability, the same code path serves both: before a
cutover the row answers `Ungenerated`, which is `spec.Generation`, and it stages;
after one it answers 2 and it does not.

A suppression is never an error and never a phase: it is the normal state of
every rebuild and of every retired generation.

### `event/projection/page.go`, `state.go`, `projection.go`  *(modified)*

```go
type Batch struct {
	Projection string
	Identity   Identity   // new
	Envelopes  []event.Envelope
	Attempt    int
}

type State struct {
	Projection string
	Identity   Identity   // new
	Phase      Phase
	Progress   event.Progress
	Parked     uint64     // new: live parked sequences, zero when nothing is blocked
	Attempt    int
	Err        error
	At         time.Time
}

const (
	PhaseDegraded Phase = "degraded"
	PhaseBlocked  Phase = "blocked"
)
```

`Projection.Name()` becomes `"vv.event.projection." + this.identity.String()`, so
`runtime.Supervisor`'s duplicate refusal (`runtime/supervisor.go:64-68`) covers
two runners of one partition in one process. `PhaseFollowing` is published only
when the whole log is read **and** the live parked count is zero; `PhaseDegraded`
deliberately does **not** fail `Ready`, because a replica reporting unhealthy for
one poison order is a projector that stopped by another route. `PhaseBlocked`
fails `Ready` once the accumulated backoff outlasts `Tolerate`. `Batch` and
`State` keep `Projection string` beside `Identity`: backlog `## P4` item 3
(`[low]`) records the redundancy and stays open.

### What is deliberately absent from the surface

`Merge`, `Reset`, `Rewind`, `SetCheckpoint`, `Resume`, `Retry`, `Clear`, a
`Position → Cursor` function, an applied position, a `Wait`, a barrier token, a
snapshot, a `List` on `Checkpoints`, a filter on `Log.ReadAll`, a
`CheckpointCapabilities` flag for the topology obligations, an
`AmIInYourTransaction()` on `Park` or `Generations`, and any field or method that
would let a handler reach an effect. Each is [SPEC] §2's or §5.3's and each has
its reason there.

**The mechanism's `EnqueuePolicy`/`EnqueueDecision` is absent, and this is where
that is recorded rather than left unadjudicated.** Its four answers map onto what
this framework already has, and only one of them has no counterpart on purpose:
`Decisions.enqueue(cause)` is `OnPermanentFailure: ParkSequence`;
`Decisions.requeue(cause, modifier)` is what a repeat failure in a redrive
already does through `Touch`; `Decisions.evict()` is an operator's act, because
the framework writes to the queue and removes nothing (`park.go`); and
`Decisions.doNotEnqueue()` would advance the scan over an event no handler
applied — a skip — which §INV-092 refuses outright. The guide's worked
retry-counter policy is `Letter.Attempt` read against the application's own
table, which is where the queue lives, and `Spec.Attempts` + `Spec.Classifier`
are the same budget one level up. What that costs is stated where a consumer
reads it (`docs/modules/{en,ru}/projection.md`) and asserted by §UC-197: an
outage lasting `Attempts` passes parks every sequence of the page the projection
was on, each letter carrying the transient cause, and the recovery is a redrive —
which under `Halt` would instead be a halted projection.

---

## The conformance extension

`eventtest.RunCheckpoints` gains **two** sections, and this is a plan decision
that makes §5.3's one section implementable without weakening it.

§5.3 lists three store obligations and argues that the third — a cursor written
under one identity reads back unchanged under another — must be **mandatory
rather than capability-gated**, because a split has no other spelling and a
`CheckpointCapabilities` flag would move a frozen kernel type and turn a
conformance failure into a run-time discovery. That argument is about obligation
3 and it is taken whole. Obligation 1, though, is *"three statements of one split
commit atomically **in a transaction the caller opened**"*, and the suite's
`Begin` hook is only required of a store that claims `Transactions`
(`checkpoints.go:33-36`). A mandatory section asking a store to commit three
statements atomically would fail it for a capability it correctly does not claim,
which is the vacuity rule the suite already holds itself to. So:

| Section | Gate | Obligations |
|---|---|---|
| `topology` | **none — mandatory** | 2. a save at advance 1 over a live row is refused by the store's own fence, not only by the tracker's. 3. a cursor written under one identity is read back unchanged under another |
| `topology handoff` | `needsCheckpointTransactions` | 1. a `Load`, two `Save`s at advance 1 and a `Forget` in one caller-opened transaction are all or nothing |

### The three defects that falsify them

`TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt` — **S2 writes it**, and
until it exists nothing in the tree fails on a *checkpoint* section no defect
names. It is a `checkpointGuarded` in `checkpoints_test.go`, shaped exactly like
`guarded` (`defects_test.go:90-99`) but over `eventtest.CheckpointSectionNames()`
and `eventtest.CheckpointDefects()`, with the same control: for each section, the
same assertion over an inventory with that section's rows removed must fail.
Deleting either new section's defect row turns it red. With it, these three
defects are not optional. Each is a
decorator over an otherwise-correct checkpoint store, and each is a shape a real
implementation reaches by writing **one statement** wrong.

| Defect | Section it must break | The one statement |
|---|---|---|
| `binds the cursor to the name that saved it` | `topology` | a cursor written under `orders#1.1` and read under `orders#1.3` comes back changed, while a round trip under one name is exact. Nothing in phases 1–3 forbids it, which is why the obligation is genuinely new. **Correction, made in S2:** the digest spelling this row first carried — `Save` tags with the saving name, `Load` untags with the requested one — falsifies nothing, because the caller always hands `Save` the *untagged* cursor it just read, so the tag round-trips per name and the cross-name read is exact too. The shipped decorator (`namespaced`) keeps the name each cursor was **first stored under** and recomposes the answer when another name asks for it, which is the store that composes the projection into the cursor it returns |
| `creates a row at advance 1 over a live one` | `topology` | `INSERT … ON CONFLICT DO UPDATE` where the shipped store writes `DO NOTHING` (`event/eventpg/checkpoints.go:353-358`) — a split's child write landing over a live row is a partition silently adopted |
| `forgets outside the caller's transaction` | `topology handoff` | a `Forget` issued on a pool while the caller holds a transaction — the parent stays retired when the unit rolls back, and the handoff is half-committed with no error on any path. The existing `detaching` defect detaches `Save` only, and is assigned to `transactions` |

### The counts that must move with them

| Assertion | Where | From | To |
|---|---|---|---|
| certified checkpoint sections, the fixture | `checkpoints_test.go:313, 331, 340, 403` | 12 | **14** |
| certified without persistence, the fixture | `checkpoints_test.go:310` | 11 | **13** |
| `checkpointDefects` | `checkpoints_test.go:275` | 5 | **14** |
| `files < 9` in `TestNoCommentInTheProjectionPackagePromisesExactlyOnce` | `scripts/projection_test.go:139` | 9 | **the count the package holds at the end of that section** |

**Correction, found in S1:** that last row cannot be **18** in S2. It is a lower
bound on a walk of `event/projection`, and the package holds nine files today,
thirteen after S1, fourteen after S2 and eighteen only after S5 — so a guard set
to 18 in S2 fails the moment it lands. The guard moves with the tree, section by
section: S1 raised its own new walk's guard to 13 and left this one at 9, S2
raises both to 14, and 18 is S5's.

**Correction, made in S2: `checkpointDefects` goes to 14 rather than to 8, and
`unfenced` changes section.** The new
`TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt` — the test this plan
mandates, written over `CheckpointSectionNames()` and `CheckpointDefects()` —
went red on its first run naming **seven** sections no defect breaks:
`binding`, `forget`, `bounds`, `refusal classes`, `lifecycle`, `concurrency` and
`durability`. The plan had assumed the checkpoint suite was already covered the
way the store suite is; it was not, and shipping the test with seven exemptions
would have delivered its letter and none of its purpose. Six are closed in S2
with one surgical decorator each (`ahead`, `ambient`, `pedantic`, `narrow`,
`verbose`, `closing`), and `unfenced` moves from `fence` to `concurrency` —
which is the section its own doc comment describes — with `ahead` taking `fence`.
`durability` is exempted and **named** in `undecorated`
(`event/eventtest/checkpoints_test.go`), with the per-section control asserting
that a defect naming an exempted section fails the test, so the list can only
shrink; the reason is structural and is backlog `## P4` item 35: a checkpoint
defect is a decorator over one value, and `durability` is about a value the
factory builds afterwards, which the store suite spells as a *factory*.

**Also corrected in S2:**
`TestAStoreThatDoesNotPersistDeclinesDurabilityAndCertifiesTheOtherEleven` is
renamed `…TheOtherThirteen` — a name that says eleven over an assertion of 13 is
the drift this repository treats as a defect — and the two flow docs that cite it
(`FL-036`, `FL-038`) are updated in the same change.

**`inventoried` (`defects_test.go:47`) stays at 29 and is not in this table.** It
sizes `eventtest.Defects()` — the **store** conformance suite's inventory
(`export_test.go:22`) — and this phase adds no store defect. The checkpoint
suite has its own count, `checkpointDefects` at `checkpoints_test.go:275`,
asserted by `TestEveryCheckpointDefectIsReportedByItsOwnSection`
(`checkpoints_test.go:248-272`) against `eventtest.CheckpointDefects()`
(`export_test.go:106`), and that is the one three new checkpoint defects move.
Moving `inventoried` instead fails `TestTheDefectInventoryIsTheSizeItSaysItIs`
and leaves the checkpoint assertion failing at 5 ≠ 8 — two red tests for a
mis-read line number, on an arm S3, S4, S5 and S6 all re-run.

The last row is not cosmetic: the arm exists to prove it read the right tree, and
a guard left at 9 while the package holds eighteen files would pass over a walk
that found half of them. (`event/projection/lifecycle_test.go:235` carries a
second stale `walked < 9` guard; it is backlog `## P4` and is **not** touched
here.)

### What the two stores must do

Neither `eventmemory.Checkpoints` nor `eventpg.Checkpoints` is expected to
change. `eventmemory` refuses a save whose advance is not one above what the row
holds (`checkpoints.go:113`), so obligation 2 holds; its cursor is the log's own
and carries no projection name, so obligation 3 holds. `eventpg` writes
`INSERT … ON CONFLICT DO NOTHING` at advance 1 and `UPDATE … WHERE advance = $3-1`
above it (`checkpoints.go:353-364`) — its doc comment already argues exactly this
case — and its cursor is `vve1` plus the log identity plus `(from, bound, reach)`
(`cursor.go:39-45`), with no name in it. **If either goes red, that is a real
finding and the section's report says which obligation and why**; it is not a
result to absorb into the suite.

**How many each certifies, and they are not the same number.**
`eventmemory.Checkpoints.Capabilities()` answers
`{Transactions: Supported, Persistence: Unsupported}`
(`event/eventmemory/checkpoints.go:40-42`), so it declines `durability` today and
will go on declining it: of fourteen sections it certifies **13**. `eventpg`
claims both and certifies **14**. The two new sections are one mandatory and one
gated on `needsCheckpointTransactions`, which `eventmemory` claims, so both are
asked of it. **`eventmemory.Checkpoints.Capabilities()` is not touched** — a
store whose rows live on a `*Log` in memory claiming persistence would be a lie
that also moves the kernel manifest and switches the `durability` section on
against a `Sibling` sharing the same log. The manifest fence is what proves it
was not touched: no `event/eventmemory/*` path may appear in S2's moved set.

**And the satellite's census of the section list is not the store class.**
`event/eventpg/census_integration_test.go` enumerates the twelve checkpoint
sections in `checkpointCensus()` (`:71-87`), and `certifies` fails on
`len(reported) != len(want)` (`:152-155`). A run reporting fourteen against a
census of twelve fails
`TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines` **before
any of §6's nineteen items is read**. So the census moves in the same section
that adds the sections — S2 — with `{"topology", certified}` and
`{"topology handoff", certified}` appended; **S2 cannot run it** (it is
`//go:build integration`), so S2 type-checks it with `go vet -tags=integration
./event/eventpg/` and S6 is where it runs. The `downgrades()` inventory beside it
(`:205` `withdrawals = 5`) is considered and left at five with the reason
recorded: neither new section is gated on a hook a factory can withdraw —
`topology` is mandatory and `topology handoff` rides
`needsCheckpointTransactions`, whose withdrawal row (`no-transactions-claim`)
already exists and now covers both it and `transactions`.

---

## Sections

Statuses: `[ ]` not started · `[~]` partial (must carry `MISSING:`) · `[x]` done,
checkpoint executed · `[!]` blocked (must carry `BLOCKED BY:`).

**Ordering rule: no section leaves the tree red.** `go build ./...` and
`go vet ./...` pass after every one, and `make unit` stays green throughout
because every live test is behind `//go:build integration`.

**Every checkpoint names its tests and counts them before running them.** Each
`-run` clause is preceded by
`test "$(go test -list '<the same anchored pattern>' … | grep -c '^Test')" = <N>`,
patterns written `'^(A|B)$'` because `-run` and `-list` match unanchored. Every
counting arm over a tagged package is preceded by an arm proving the binary lists
at all, because `TestMain` runs before `m.Run()` handles the flag.

**Two shell hazards, both of which cost time to find.** Some agent harnesses
replace `grep` with a wrapper around `ugrep`, whose `-qv` exit status is inverted
relative to GNU grep's — so no checkpoint below turns on `grep -qv`, and the ones
that use `grep` use `grep -c` inside `$( )` and `grep -q` without `-v`. And
`scripts/checks.sh` is `set -euo pipefail`, so an arm added there fails the whole
run on a `grep` that matches nothing; the existing arms end `|| true` where that
is intended.

---

### S1 — the name, the set, the sequence, and the one authority  `[x]`   *(no database · moves the manifest)*

**Delivers ES-01's residual work and ES-02's value half, plus the filter that
makes the value half mean something.** `Identity`, `Generation`, `Partition`,
`Cover`, `Sequencer` and the four constructors; the `checkUnit` alignment; and —
added after this section's own review, see **P-9** and **P-10** — the per-read
partition filter, the sequencer-panic halt, and the resume-time refusal of a
topology started over a live coarser one. Nothing is parked and nothing is split.

**Appendices** ES-01 (residual), ES-02 (the vocabulary).

**Files** `event/projection/identity.go`, `partition.go`, `cover.go`,
`sequence.go` (new); `errors.go`, `spec.go`, `pass.go`, `page.go`, `state.go`,
`projection.go` (modified); `identity_test.go`, `partition_test.go`,
`cover_test.go`, `sequence_test.go`, `alignment_test.go` (new);
`spec_test.go`, `harness_test.go` (extended); `scripts/projection_test.go` and
`scripts/docs_test.go` (extended); `docs/ai/flows/FL-038-*.md`,
`docs/ai/flows/Index.md`, `docs/modules/{en,ru}/projection.md` and
`docs/release-notes/v0.1.0.md` (updated in the same change as the code, which is
what `CLAUDE.md` requires and what this section's review found missing).

`errors.go` is here because `ErrTopology` is: `NewPartition`, `Partition.Split`
and `NewCover` all wrap it and S1 does not build without it. `harness_test.go` is
here because this section builds the aligned stand every park and effect test of
S3 and S5 is written on. `scripts/projection_test.go` is here because the
[[D-130]] source check is cheapest to write **before** the three functions that
would break it exist.

**Realises** `Generation`, `Ungenerated`, `Identity` and its six methods,
`NewIdentity`, `ParseIdentity`, `MaxPartitions`, `Partition` and its eight
methods, `Whole`, `NewPartition`, `ParsePartition`, `Cover`, `NewCover`,
`Cover.Partitions`, `Cover.Count`, `Sequencer`, `ByStream`, `Unordered`,
`OneSequence`, `SequenceBy`, `ErrTopology`, `Spec.Sequence`, `Spec.Partition`,
`Spec.Generation`, `Batch.Identity`, `State.Identity`, the tier comparison in
`checkUnit`, and — **P-9**, **P-10** — `Projection.matching` with the
`matchedPage` applier, `Identity.coarser` and `Projection.unclaimed`.

**Covers** UC-131, UC-132, UC-133, UC-135, UC-136, UC-137 (the sequencer half),
UC-141, UC-142 (the surface half), UC-144, UC-145, UC-186, UC-187, UC-188,
UC-194, UC-195, UC-196; INV-083, INV-085, INV-086, INV-105, INV-107, INV-097 (the
rendering half), INV-104 (this section's refusals).

**Tests** — all untagged, in `event/projection`:

- `TestAnIdentityRendersAndRoundTrips` — §UC-145. `NewIdentity("orders", 2, {3,7})`
  renders `orders@2#3.7`; `ParseIdentity` round-trips it; `Whole()` renders
  `orders@2`; the rendering carries no `[` or `]`. **Control:**
  `NewIdentity("orders", Ungenerated, Whole())` renders `orders` — every
  projection that exists today keeps its name and its row.
- `TestADelimiterInAProjectionNameIsRefusedAtConstruction` — §UC-188. `orders@2`
  is refused, naming the delimiter and the field, and `New` collects it as
  `ErrSpec` at boot before a page is read. **Control:** `orders.v2` is accepted,
  because only `@` and `#` separate the parts.
- `TestTwoDistinctIdentitiesNeverRenderOneName` — §INV-097's injectivity, as a
  property over a generated cross product of projections, generations and
  partitions, plus the assertion that `ParseIdentity` refuses every string
  `String()` never produces (`orders@0`, `orders#0.0`, `orders@x`).
- `TestASplitAtTheCeilingIsRefusedAndOneBelowItSucceeds` — §UC-141. `ErrTopology`
  naming `MaxPartitions`; the mask does not wrap; `NewPartition` admits nothing
  beyond it.
- `TestAMaskMovesNoKeyOutOfTheParentsHalfOfTheSpace` — §INV-086, as a property
  over many keys: for every key matching `{1,1}`, exactly one of `{1,3}` and
  `{3,3}` matches it after the split, and no key matching `{0,1}` moves.
- `TestACoverWithAGapIsRefusedAndACompleteOneIsNot` — §UC-186. `{0,3},{1,3},{3,3}`
  is refused naming the uncovered quarter. **Control:** the complete four are
  admitted, and `Whole()` alone is a cover of one.
- `TestACoverWithAnOverlapIsRefusedByMaskArithmeticAndNotByName` — §UC-187.
  `{1,3}` and `{1,7}` are refused by `(idA ^ idB) & min(maskA, maskB) == 0`.
  **Control:** `{0,3},{1,3},{2,3},{3,7},{7,7}` — the same space with one member
  split — is admitted, so the check discriminates rather than refusing every set
  whose masks differ.
- `TestEverySequencerIsTotalPureAndStable` — §UC-137's half, §INV-085. Each of
  the four answers a key for every envelope of a fixture log, twice, identically;
  `Unordered` answers a *stable* key, asserted by two calls over one envelope;
  and **P-1's arm:** `ByStream()` answers two different keys for two streams of
  one family, with the control that `Stream.String()` answers one — so the
  correction is measured. `Name()` is asserted for all four.
- `TestTheAlignmentIsComparedInsideTheUnitBeforeTheHandler` — §UC-131, §INV-083.
  A recording checkpoint store whose `Transaction` answers an authority over a
  chosen identity, and a `crud.BindExecutor` binding a fake transaction executor
  whose `DataSource()` is that same identity. The comparison happens inside the
  unit, before the handler, **once per pass and not once per value**.
- `TestNothingInTheProjectionPackageOpensATransaction` — §INV-103's *"opens a
  transaction"* clause, in `scripts/projection_test.go`. An AST walk over
  `event/projection`'s non-test files reporting any call whose selector is
  `Begin`, `Commit` or `Rollback` on any receiver, and `crud.InNewTx`,
  `crud.InTx` and `crud.InAtomic` by package and name. **Control:** a fixture
  source carrying one of each is reported by the same walk, each shape named, in
  the shape `TestTheProjectionStartsNothingAndReadsNoEnvironment`'s
  `forbiddenShapes` control already uses. The tree is clean today — the only
  match anywhere under `event/projection` is the word `crud.InNewTx` inside a
  comment (`spec.go:52`), which an AST walk does not see.
- `TestTwoTransactionsUnderOneUnitAreRefusedAndOneIsAccepted` — §UC-132. Two
  different identities → `ErrSpec` naming both resources, a halt, no handler
  call, no advance. **Control:** the identical composition with one identity for
  both drains the log. This pair is the whole of ES-01's residual work.
- `TestUncheckedMakesNoComparisonAtAll` — §UC-133. `Unchecked` skips the
  comparison; `Unchecked` is not reachable by leaving `Destination` zero.
- `TestNewRefusesEverySpecItCannotAssemble` — extended in `spec_test.go` with
  this section's refusals, asserting a spec wrong in three places reports three
  problems.
- `TestFourPartitionsApplyEveryEventOnceAndKeepEachKeyInOrder` — §UC-135,
  **moved here from S2 by P-9**, over `eventmemory` with four `Projection` values
  at `{0..3, 3}`. Every event applied exactly once across the four; each stream's
  events in that stream's order; four independent checkpoints whose `Applied` sums
  to the log and whose `Highest` is each the log's last position. **Control A:**
  the same four with a sequencer minting a fresh key per call, run one at a time
  so the count is deterministic, leaves events applied twice or not at all —
  asserted. **Control B (P-1):** a family-only key puts every event in one
  partition, asserted, so `ByStream`'s composition is measured.
- `TestAPageThatMatchesNothingAdvancesAndCallsNoHandler` — §UC-136, **moved here
  by P-9**. Under `OneSequence()` the one key reaches exactly one member of a
  four-way cover, so the test computes which and drives the other: the checkpoint
  advances once, `Highest` is the read page's last position, `Applied` is zero,
  the handler is never called. **Control:** the member the key does reach advances
  by the **same cursor**, compared byte for byte, with `Applied` risen by the
  page's length.
- `TestASequencerPanicHaltsAndAHandlerPanicDoesNot` — §UC-144, **moved here by
  P-9**, because the filter is what calls a sequencer from the loop. The halt
  names the sequencer, no handler runs, no advance. **Control:** a handler panic
  on the same envelope is recovered into a permanent page failure, reaches the
  configured policy, and the handler was called.
- `TestAPartitionedRunnerBesideALiveCoarserRowIsRefused` — §UC-194, §INV-107,
  **P-10**. The two-release migration driven: `orders` drained unpartitioned, then
  `{0,1}` and `{1,1}` started, each halting with `ErrTopology` naming the row it
  found and naming `Split`, writing no row of its own and reaching no handler.
  **Control A:** the same release after the handoff spelled by hand — both
  children written at the parent's cursor through `event.Track`, the parent
  retired — is admitted and applies nothing twice. **Control B:** a partition set
  on a projection that never ran is admitted and drains. Plus the ancestor arm: a
  live `orders#0.1` refuses a runner at `orders#0.3`, naming the half.
- `TestNewIdentityRefusesEveryNameTheKernelRefuses` — §UC-196, **P-12**. The two
  spellings of one rule walked against each other over a table, over every byte a
  name can carry, and over the bound the generation and partition push it past.
  **Control:** `orders.v2`, `orders/paid`, a non-ASCII name and a name of two
  spaces are admitted by both, so the duplication is not a stricter rule wearing
  the kernel's name.
- `TestTheZeroCoverAndTheZeroIdentityAreTellableFromEveryCheckedOne` — §UC-195,
  **P-11**. `projection.Cover{}` and `projection.Identity{}` constructed from
  `package projection_test`; `Count() == 0` and `Projection() == ""` shown to be
  exact against every value the two constructors answer.
- `TestEveryPublishedTopologyPredicateHasACaller` — **P-9**, in
  `scripts/projection_test.go`. `Partition.Matches` and `Sequencer.SequenceOf`
  each have a call in a non-test file of `event/projection`. **Control:** a
  fixture declaring both and calling neither is counted at zero, so the arms are
  not satisfied by the declaration.
- `TestEveryDoorTakingACoverOrAnIdentityRefusesItsZeroValue` — **P-11**, in
  `scripts/projection_test.go`. Vacuous today. **Control:** a fixture with two
  doors that ask and two that do not, asserting exactly the two that do not are
  reported.
- `TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex` — in
  `scripts/docs_test.go`. Every non-test file of `event/projection` has a row in
  `docs/ai/flows/Index.md`. It is what caught `identity.go`, `partition.go`,
  `cover.go` and `sequence.go` being absent from it, and nothing else in this
  repository can see a file that was added and not listed.

**How a tier-A projection is built in an untagged test, and what that is
evidence for.** S3's sixteen park tests and S5's ten effect tests all need a spec
`ParkSequence` accepts, which means `Advance: InUnit` **and** a `Destination`
whose `crud.KeyOf` matches the checkpoint store's authority. Over `eventmemory`
there is no such value: its checkpoint authority is minted over `txIdentity`
(`event/eventmemory/transaction.go:41-44`), an unexported struct with no exported
accessor on `*Tx` or on `event.Authority`, and `crud.SameDataSource` refuses two
values of different types outright (`crud/executor.go:562-571`). Every shipped
`InUnit` test in this package therefore uses `Destination = projection.Unchecked`
(`unit_test.go:69,151,326,398,572`, `loop_test.go:406`) — which is exactly what
`ParkSequence` refuses.

So S1 adds **one** seam to `harness_test.go` and everything after it uses that
one: `aligned(t)`, a `watchedCheckpoints`-shaped decorator whose `Transaction`
answers `event.NewAuthority(backing, chosen)` over a value the test also gives
the destination through `crud.BindExecutor`, so `crud.KeyOf(executor) == chosen`.
`TestTheAlignmentIsComparedInsideTheUnitBeforeTheHandler` and
`TestTwoTransactionsUnderOneUnitAreRefusedAndOneIsAccepted` are what prove the
seam discriminates — one identity for both is admitted, two are refused.

**And it is said once, plainly, because it decides what twenty tests are evidence
for: the alignment in an untagged test is fabricated.** The comparison passes
because the test chose both sides of it, and the "read model" underneath is a Go
map with no transaction at all. What those tests can falsify is the *ordering* —
that the comparison happens inside the unit and before the handler, that a park
write rolls back with its unit, that the queue is empty and the advance unmoved.
What they cannot falsify is that a park row, a read-model row and a checkpoint
row land in one commit, because there is only one thing there that commits. That
half is S6's `TestTheBlockingTestAndTheAdvanceAreOneCommit`, over two real
tables, and §INV-091's matrix row names S6 alone for it.

**Checkpoint** (no database):

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s1
awk '/^## github.com\/frostgrove\/vv\/event$/,/^## github.com\/frostgrove\/vv\/event\//' \
    docs/api/surface.md > .git/event_surface_before_p4 && test -s .git/event_surface_before_p4
# … write the section …
gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 \
&& go build ./... && go vet ./event/... \
&& test "$(go test -list '^(TestAnIdentityRendersAndRoundTrips|TestADelimiterInAProjectionNameIsRefusedAtConstruction|TestTwoDistinctIdentitiesNeverRenderOneName|TestNewIdentityRefusesEveryNameTheKernelRefuses|TestASplitAtTheCeilingIsRefusedAndOneBelowItSucceeds|TestAMaskMovesNoKeyOutOfTheParentsHalfOfTheSpace|TestACoverWithAGapIsRefusedAndACompleteOneIsNot|TestACoverWithAnOverlapIsRefusedByMaskArithmeticAndNotByName|TestTheZeroCoverAndTheZeroIdentityAreTellableFromEveryCheckedOne|TestEverySequencerIsTotalPureAndStable|TestASequencerPanicHaltsAndAHandlerPanicDoesNot|TestFourPartitionsApplyEveryEventOnceAndKeepEachKeyInOrder|TestAPageThatMatchesNothingAdvancesAndCallsNoHandler|TestAPartitionedRunnerBesideALiveCoarserRowIsRefused|TestTheAlignmentIsComparedInsideTheUnitBeforeTheHandler|TestTwoTransactionsUnderOneUnitAreRefusedAndOneIsAccepted|TestUncheckedMakesNoComparisonAtAll)$' ./event/projection/ | grep -c '^Test')" = 17 \
&& go test -race -count=1 -run '^(TestAnIdentityRendersAndRoundTrips|TestADelimiterInAProjectionNameIsRefusedAtConstruction|TestTwoDistinctIdentitiesNeverRenderOneName|TestNewIdentityRefusesEveryNameTheKernelRefuses|TestASplitAtTheCeilingIsRefusedAndOneBelowItSucceeds|TestAMaskMovesNoKeyOutOfTheParentsHalfOfTheSpace|TestACoverWithAGapIsRefusedAndACompleteOneIsNot|TestACoverWithAnOverlapIsRefusedByMaskArithmeticAndNotByName|TestTheZeroCoverAndTheZeroIdentityAreTellableFromEveryCheckedOne|TestEverySequencerIsTotalPureAndStable|TestASequencerPanicHaltsAndAHandlerPanicDoesNot|TestFourPartitionsApplyEveryEventOnceAndKeepEachKeyInOrder|TestAPageThatMatchesNothingAdvancesAndCallsNoHandler|TestAPartitionedRunnerBesideALiveCoarserRowIsRefused|TestTheAlignmentIsComparedInsideTheUnitBeforeTheHandler|TestTwoTransactionsUnderOneUnitAreRefusedAndOneIsAccepted|TestUncheckedMakesNoComparisonAtAll|TestNewRefusesEverySpecItCannotAssemble)$' ./event/projection/ \
&& test "$(go test -list '^(TestNothingInTheProjectionPackageOpensATransaction|TestEveryPublishedTopologyPredicateHasACaller|TestEveryDoorTakingACoverOrAnIdentityRefusesItsZeroValue|TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex)$' ./scripts/ | grep -c '^Test')" = 4 \
&& go test -race -count=1 -run '^(TestNothingInTheProjectionPackageOpensATransaction|TestEveryPublishedTopologyPredicateHasACaller|TestEveryDoorTakingACoverOrAnIdentityRefusesItsZeroValue|TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex)$' ./scripts/ \
&& ./scripts/checks.sh event-kernel-baseline \
&& test "$(go test -list '^TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity$' ./scripts/ | grep -c '^Test')" = 1 \
&& go test -race -count=1 ./event/... ./scripts/ \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s1 \
     '^event/projection/(identity|partition|cover|sequence|errors|spec|pass|page|state|projection|identity_test|partition_test|cover_test|sequence_test|alignment_test|spec_test|unit_test|loop_test|harness_test|retry_test|lifecycle_test)\.go$' \
     event/projection/identity.go event/projection/partition.go event/projection/cover.go event/projection/sequence.go event/projection/errors.go
```

**Correction, made in S1:** `event-kernel-baseline` runs **before** the full
`go test ./event/... ./scripts/` arm and not after it. `./scripts/` carries
`TestTheEventKernelOfThisRepositoryIsWhereThisPhaseLeftIt`
(`scripts/checks_test.go:545`), which shells out to `checks.sh event-kernel`, so
a section that added a file and had not yet re-baselined fails the suite arm
before it ever reaches the fence. The evidence is unchanged — the fence still
compares the new manifest against the predecessor recorded before the first file
was written — and every section below has the same ordering defect.

The last arm is what proves the `event` package did not move: no
`^event/[a-z_]*\.go$` path can match that ERE. `errors.go` is in the allowed set
**and** required by path, because `ErrTopology` is what S1's three refusals wrap
and a section that did not declare it did not deliver.

**Checkpoint run, 2026-09-09, re-run after the review's five findings were
closed** — every arm, in the corrected order, exit 0. `gofmt -l .` printed nothing
and `go build ./...` and `go vet ./event/...` were silent; the `-list` arms
counted **17 and 4**.

```
ok  	github.com/frostgrove/vv/event/projection	1.022s
ok  	github.com/frostgrove/vv/scripts	1.066s
event-kernel-baseline: 140 files recorded in scripts/event_kernel.sha256
ok  	github.com/frostgrove/vv/event	6.703s
ok  	github.com/frostgrove/vv/event/eventmemory	1.509s
ok  	github.com/frostgrove/vv/event/eventtest	4.103s
ok  	github.com/frostgrove/vv/event/projection	1.512s
ok  	github.com/frostgrove/vv/scripts	26.389s
check-event-kernel: ok
the files under event/ this section moved:
  event/projection/alignment_test.go
  event/projection/cover.go
  event/projection/cover_test.go
  event/projection/errors.go
  event/projection/harness_test.go
  event/projection/identity.go
  event/projection/identity_test.go
  event/projection/page.go
  event/projection/partition.go
  event/projection/partition_test.go
  event/projection/pass.go
  event/projection/projection.go
  event/projection/sequence.go
  event/projection/sequence_test.go
  event/projection/spec.go
  event/projection/spec_test.go
  event/projection/state.go
event-kernel-moved: ok
EXIT=0
```

The moved set is unchanged by the five closures: every file the closures touched
was already in it, and the three new tests outside `event/` are in `scripts/`,
which the manifest does not cover.

`make check` is green (`check-deps`, `check-tiers`, `check-utils`,
`check-triplets`, `check-todo`, `check-replaces`, `check-tidy`,
`check-otel-schema`, `check-workspace`, `check-event-kernel` all `ok`), and the
dependency budget row did not move: `./event/projection` still costs `./runtime`
on top of `./crud` and `./errs`, which is what `check-deps` re-derives.

**§5.1's obligation, measured rather than promised.** `make api` regenerated
`docs/api/surface.md` and the `event` section is byte-identical to
`.git/event_surface_before_p4`, recorded before the first file of this phase was
written:

```
$ diff -u .git/event_surface_before_p4 /tmp/event_surface_after.txt && echo "EVENT SECTION BYTE-IDENTICAL"
EVENT SECTION BYTE-IDENTICAL
```

The only section that moved is `event/projection`, which gained exactly
`MaxPartitions`, `Cover`/`NewCover`, `Generation`/`Ungenerated`,
`Identity`/`NewIdentity`/`ParseIdentity`, `Partition`/`NewPartition`/
`ParsePartition`/`Whole` and `Sequencer`/`ByStream`/`OneSequence`/`SequenceBy`/
`Unordered`.

**The live suite was run even though this section is not a live one**, because
`checkUnit`'s new comparison is on the path every shipped `InUnit` live case
takes and §1.1's claim that they are all tier A is a prediction rather than a
fact until it is run. It is green twice in a row:

```
$ FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1 -tags=integration ./event/eventpg/...
ok  	github.com/frostgrove/vv/event/eventpg	104.796s
ok  	github.com/frostgrove/vv/event/eventpg	104.471s
```

Every live case runs at `Whole()`, so `Projection.unclaimed` asks nothing and the
filter calls no sequencer: the two additions are measured to cost the
unpartitioned path nothing, live, rather than argued to.

**Mutation evidence.** Eleven mutations, each applied to a non-test file, run,
and reverted:

| Mutation | Caught by |
|---|---|
| `ByStream` answers `envelope.Stream.String()` — [SPEC] §1.2 as written, **P-1** | `TestEverySequencerIsTotalPureAndStable` — 3 distinct keys over 6 envelopes where 5 were expected, and the P-1 arm reporting the key `[stream orders]` |
| `Partition.Split` drops the ceiling refusal | `TestASplitAtTheCeilingIsRefusedAndOneBelowItSucceeds` — split into `0.2047` and `1024.2047` |
| `Partition.Split` answers `{id+1, 2m+1}` for the higher child | `TestAMaskMovesNoKeyOutOfTheParentsHalfOfTheSpace` — a key of `1.1` in neither child |
| `hash` is FNV-1 rather than FNV-1a | the same test's hash arm, against `hash/fnv` |
| `NewCover` drops the sum check | `TestACoverWithAGapIsRefusedAndACompleteOneIsNot` |
| `NewCover` compares members by name (`id == id && mask == mask`) | `TestACoverWithAnOverlapIsRefusedByMaskArithmeticAndNotByName` |
| `checkUnit` ignores the alignment's answer | `TestTwoTransactionsUnderOneUnitAreRefusedAndOneIsAccepted` — the tier B wiring drains instead of halting |
| `checkUnit` compares once and caches | `TestTheAlignmentIsComparedInsideTheUnitBeforeTheHandler` — the order came back `[unit opens applied unit closes …]` |
| `NewIdentity` admits a delimiter | `TestADelimiterInAProjectionNameIsRefusedAtConstruction` |
| `Identity.String` drops the generation | `TestAnIdentityRendersAndRoundTrips` and `TestTwoDistinctIdentitiesNeverRenderOneName` |
| `Projection.Name` and `event.Track` take `Spec.Name` rather than the identity | `TestAnIdentityRendersAndRoundTrips`'s row-and-runner arm |

**Four more mutations, after the review's closures, each applied to a non-test
file, run, and reverted:**

| Mutation | Caught by |
|---|---|
| `matching` answers the whole page whatever the partition (S1 as it was reviewed) | `TestFourPartitionsApplyEveryEventOnceAndKeepEachKeyInOrder` — *"0 events were applied once, 36 more than once and 0 never, over a log of 36"*; `TestAPageThatMatchesNothingAdvancesAndCallsNoHandler` — *"the handler was called 1 times for a page none of whose envelopes this partition matches"*; `TestASequencerPanicHaltsAndAHandlerPanicDoesNot` — *"it published [draining draining following]"* |
| the `Matches` call is deleted from `matching`, leaving the predicate published and inert | `TestEveryPublishedTopologyPredicateHasACaller` — *"no non-test file of event/projection calls Matches"*, and the same for `SequenceOf` |
| `unclaimed` iterates no coarser identity | `TestAPartitionedRunnerBesideALiveCoarserRowIsRefused` — the two runners drained instead of halting: *"it published [draining draining following draining draining following]"* |
| `NewIdentity` drops `unnameable` | `TestNewIdentityRefusesEveryNameTheKernelRefuses` — twelve subtests red, each naming the kernel's own phrase for the rule the two spellings now disagree about |

**One mutation that was NOT caught, and it is a finding rather than a gap.**
Rewriting `Matches` as `hash(sequence)%(this.mask+1) == this.id` leaves every test
green — because for `mask = 2^k − 1` the modulus and the mask are the same
arithmetic. The modulus is corrupting only when the *count changes*, which is
§UC-138's `hash % N` control and lives in **S2 and S6**. S1 measures the property
a fixed mask has; nothing in S1 can tell the two spellings apart, and a test
claiming to would be measuring nothing. It stays true after **P-9**: the filter
calls `Matches` and does not change the count.

---

### S2 — the partitioned loop, the handoff, and the store contract  `[x]`   *(no database · moves the manifest)*

**Delivers ES-02's mechanism.** The loop filters by partition; `Split` performs
the six-step handoff through the `event.Track` door; `eventtest` gains the two
sections and three defects that make a third store's split provable.

**Appendices** ES-02.

**Files** `event/projection/topology.go` (new), `topology_test.go` (new),
`settlement_test.go` (new — the review's, see *Closed in review* below);
`identity.go` and `pass.go` (modified — the review's),
`partition_test.go` (modified, for §UC-194's control through `Split`),
`harness_test.go` (modified: the one-run `stand.unit` every split test is
performed inside); `event/eventtest/sections_topology.go`
(new), `sections_checkpoints.go`, `defects_checkpoints.go`, `checkpoints_test.go`,
`doc.go` (modified); `event/eventpg/census_integration_test.go` (modified),
`event/eventpg/projection_integration_test.go` (modified — the review's).

**Correction, made in S2:** `scripts/projection_test.go` is in this list too. The
counts table above requires both of its `event/projection` file-count guards to
move to 14 in this section and the Files list omitted it; neither guard *fails*
at 14 (both are lower bounds), which is exactly why a section that did not raise
them would have walked a fourteen-file package behind a nine-file guard. And the
docs `CLAUDE.md` requires in the same change: `docs/ai/flows/FL-038-*.md` (a new
"The handoff" section, the `topology.go` and `sections_topology.go` rows, and the
Proved-by rows), `docs/ai/flows/Index.md`, `docs/ai/flows/FL-036-*.md` (the
renamed test it cites), `docs/modules/{en,ru}/projection.md`,
`docs/modules/{en,ru}/eventtest.md` and `docs/release-notes/v0.1.0.md`.

`defects_test.go` is **not** in this list: `inventoried` is the store suite's and
does not move. `census_integration_test.go` is, and it is the one file this
section changes that this section cannot run — it is `//go:build integration`,
so S2 type-checks it with `go vet -tags=integration ./event/eventpg/` and S6 runs
it. Left out, `TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines`
is red from the moment the sections land until somebody finds it in S6.

**Realises** `SplitSpec` and `Split`, and the two conformance sections with their
defects. The partition filter and the sequencer-panic halt **landed in S1** with
the vocabulary they belong to — **P-9**.

**Covers** UC-137 (the loop half), UC-138, UC-139 (the construction half),
UC-140, UC-185, UC-194 (the through-`Split` control); INV-087, INV-105 (the
`Observe` half is S4's).

**Tests** — untagged, in `event/projection` and `event/eventtest`:

- `TestUnorderedSpreadsAndOneSequenceConcentrates` — §UC-137. **Control:** a
  restart mid-drain under `Unordered` re-delivers the surviving page to the
  **same** partition, asserted by the destination rows rather than by the key.

  `TestFourPartitionsApplyEveryEventOnceAndKeepEachKeyInOrder`,
  `TestAPageThatMatchesNothingAdvancesAndCallsNoHandler` and
  `TestASequencerPanicHaltsAndAHandlerPanicDoesNot` **were in this list and are
  now in S1's**, with the filter they measure — **P-9**.
- `TestASplitWritesTwoChildrenAtTheParentsCursorInOneTransaction` — §UC-138. A
  recording `Checkpoints` counts four statements in one transaction; both
  children carry the parent's cursor byte for byte **and the parent's
  `Progress.Highest`, asserted on each of them**; the lower child inherits
  `Applied` and `Quarantined` and the higher starts at zero; nothing reaches the
  store other than through the tracker door. **Control:** the same child rows
  written with `Highest: 0` are admitted by `event.Track`'s door without a murmur
  — asserted, because `admit` deliberately does not make `Progress` total
  (`event/checkpoint.go:149-191`) — so the `Highest` arm above is this test's
  assertion and not the kernel's. What that legal-but-wrong row then does to a
  barrier is S4's `TestACutoverTakesNoBarrierAndDerivesItsOwn`.
- `TestASplitOfAParentWithNoRowIsRefusedForBothAbsences` — §UC-140. Both
  absences refused with `ErrTopology`; the message names the two readings and
  their two remedies, and names any child row it found. **Control:** a split of a
  partition that *has* run writes exactly four statements, counted, and answers
  its children.
- `TestAPartitionedRunnerBesideALiveCoarserRowIsRefused` — **re-run in S2 with
  its control spelled through the real `Split`** rather than by hand. S1 writes
  the two child rows at the parent's cursor and retires the parent through
  `event.Track` directly, because `Split` does not exist there; S2 replaces that
  arm with `Split` and asserts the same admission, which is what makes the
  handoff and the refusal one story rather than two (§UC-194, **P-10**).
- `TestASplitOverAnExistingChildRowIsRefused` — the ambiguous-commit arm of
  §INV-087.
- `TestASplitUnderATwiceRunUnitIsOneSplitOrARefusal` — §UC-185. A `Unit` that
  runs its body twice, once rolled back and once committed, produces one split;
  a run following a **committed** one finds the parent absent and is refused,
  naming the children — through the retirement record, which is the one absence
  the rows tell apart. **Control:** one run alone writes exactly four statements,
  so the arity behaviour is attributable to the second run.
- `TestTheCheckpointStoreSatisfiesTheContract` — unchanged name, now running
  fourteen sections and reporting **13** certified for `eventmemory`: it declines
  `durability`, because its rows live on a `*Log` in memory and its
  `Capabilities()` says `Persistence: Unsupported`. `eventpg` certifies 14 —
  measured in S2 rather than left to S6, because the census edit is a claim only
  a database can answer.
  `eventmemory.Checkpoints.Capabilities()` is not touched, and the manifest fence
  is what says so.
- `TestEveryCheckpointDefectIsReportedByItsOwnSection` — extended with **nine**
  new defects, each breaking the section it names through the existing
  `oneCheckpointVerdict` pair (the store with the defect reported `failed`, the
  same store without it reported `passed`); `checkpointDefects` 5 → 14. Three are
  the topology ones this section is about and six close the sections the new
  test found unguarded — see the correction under *The counts that must move*.
  Two of the new rows break more sections than the one they name, which the pair
  above does not measure; that is backlog `## P4` item 36.
- `TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt` — **new**, in
  `checkpoints_test.go`, and it is what forces the third defect to exist. A
  `checkpointGuarded` over `CheckpointSectionNames()` and `CheckpointDefects()`,
  plus the per-section control: for each section, the same assertion over an
  inventory with that section's rows removed must fail. Deleting either new
  section's defect row turns it red; before this test, a `topology` section whose
  body is `_ = this` certified everywhere and nothing noticed. **It went red on
  its first run for seven sections and closed six of them** — the correction
  under *The counts that must move* — and the seventh, `durability`, is named in
  an `undecorated` list the test itself keeps honest: a defect that names an
  exempted section fails it, so the list only ever shrinks.
- `TestTheDefectInventoryIsTheSizeItSaysItIs` and
  `TestEverySectionIsNamedByADefectThatBreaksIt` — **unchanged**, `inventoried`
  still 29. They are the store suite's, and no store defect is added.

**Closed in review** (`EVENTSOURCE_P4_S2_GAPS.md`, round 1, both blocking, both
driven before they were fixed). Two contract changes, and the plan is amended
here rather than annotated:

- **GAP-1 `[critical]` — the settlement is keyed by the identity, not by
  `Spec.Name`.** `pass.go`'s `settle` re-derived the row key from `Spec.Name`,
  which renders the identity only at `Ungenerated` over the whole key space. Every
  other checkpoint call in the loop already went through `this.tracker` or through
  `Identity.coarser()`. Driven twice against `eventmemory`: a partitioned runner
  that lost the fence **halted** on `errFenceRefused` — [[D-133]] inverted, and the
  shape every rolling restart produces on purpose — and a partitioned `InUnit`
  runner whose unit rolled back adopted the coarse row and wrote every later
  checkpoint there, after which a restart halts every partition via `unclaimed`.
  One line, plus `settlement_test.go` (both settlements, over a partition, a
  generation and the render-alike control) and
  `TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity` in
  `scripts/projection_test.go`, which reports any `event.Track` in the package
  whose name argument mentions no `Identity`.
- **GAP-2 `[high]` — a split is one way, and the record is what makes it one.**
  `Split`'s fifth step `Forget`s the parent, and `Identity.coarser()` is empty for
  `Whole()`, so redeploying the release that ran one release earlier — the
  ordinary rollback of a bad deploy — was admitted and replayed the whole log into
  the live children's read model (driven: `phase=following err=<nil>`, 36 of 36
  re-applied, three rows all reporting a healthy watermark). **The handoff is six
  steps, not five:** the retirement is recorded durably before the row is removed,
  as an ordinary checkpoint row at `<identity>#split` carrying the parent's own
  cursor, and every runner asks about its own at the resume (`Projection.unretired`
  — one `Load` at start-up, for the unpartitioned too). `handOver` reads that
  record for the parent and both children, so a second split of a retired parent
  and a split over a child that was itself split are both refused. A name within
  `len("#split")` of `event.MaxNameBytes` has no room for the mark and is refused
  outright, which is what lets an absent record be read as "no split happened".
  Backlog `## P4` item 32 is amended: what stays open is a **hand-declared** finer
  row, and its old justification ("a documented-against action", "§1.3 already
  refuses a merge outright") is corrected in it and in §1.3.1.
- **What the extra resume `Load` cost, measured rather than assumed.** Three live
  `eventpg` cases counted the projection's `Load`s as a total and blocked on the
  second — `TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving` (both arms
  and its `NotWritten` control) and
  `TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt` — so the retirement
  probe made each of them stall on the resume instead of on the settlement. The
  counter in `asked` is now keyed by the row the case is about
  (`event/eventpg/projection_integration_test.go`), which is what those cases
  always meant: the resume's read and the settlement's, of the row itself. The
  probe is one `Load` of a different row, at start-up, once.

**Checkpoint** (no database):

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s2
# … write the section …
gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 \
&& go build ./... && go vet ./event/... \
&& test "$(go test -list '^(TestUnorderedSpreadsAndOneSequenceConcentrates|TestASplitWritesTwoChildrenAtTheParentsCursorInOneTransaction|TestASplitOfAParentWithNoRowIsRefusedForBothAbsences|TestASplitOverAnExistingChildRowIsRefused|TestASplitUnderATwiceRunUnitIsOneSplitOrARefusal|TestAPartitionedRunnerBesideALiveCoarserRowIsRefused|TestTheReleaseThatRanBeforeASplitIsRefusedWhenItIsRedeployed|TestASplitOfAnAlreadyRetiredParentIsRefused|TestASplitOfANameWithNoRoomForItsRetirementIsRefused|TestALostFenceIsSettledOnTheRowOfThisRunnersOwnIdentity|TestAUnitThatRollsBackLeavesTheAdvanceOnThisRunnersOwnRow)$' ./event/projection/ | grep -c '^Test')" = 11 \
&& go test -race -count=1 -run '^(TestUnorderedSpreadsAndOneSequenceConcentrates|TestASplitWritesTwoChildrenAtTheParentsCursorInOneTransaction|TestASplitOfAParentWithNoRowIsRefusedForBothAbsences|TestASplitOverAnExistingChildRowIsRefused|TestASplitUnderATwiceRunUnitIsOneSplitOrARefusal|TestAPartitionedRunnerBesideALiveCoarserRowIsRefused|TestTheReleaseThatRanBeforeASplitIsRefusedWhenItIsRedeployed|TestASplitOfAnAlreadyRetiredParentIsRefused|TestASplitOfANameWithNoRoomForItsRetirementIsRefused|TestALostFenceIsSettledOnTheRowOfThisRunnersOwnIdentity|TestAUnitThatRollsBackLeavesTheAdvanceOnThisRunnersOwnRow)$' ./event/projection/ \
&& test "$(go test -list '^(TestTheCheckpointStoreSatisfiesTheContract|TestEveryCheckpointDefectIsReportedByItsOwnSection|TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt|TestTheDefectInventoryIsTheSizeItSaysItIs|TestEverySectionIsNamedByADefectThatBreaksIt)$' ./event/eventtest/ ./event/eventmemory/ | grep -c '^Test')" = 5 \
&& go test -race -count=1 -run '^(TestTheCheckpointStoreSatisfiesTheContract|TestEveryCheckpointDefectIsReportedByItsOwnSection|TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt|TestTheDefectInventoryIsTheSizeItSaysItIs|TestEverySectionIsNamedByADefectThatBreaksIt)$' ./event/eventtest/ ./event/eventmemory/ \
&& test "$(go test -list '^TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity$' ./scripts/ | grep -c '^Test')" = 1 \
&& ./scripts/checks.sh event-kernel-baseline \
&& go test -race -count=1 ./event/... ./scripts/ \
&& go vet -tags=integration ./event/eventpg/ \
&& test "$(grep -c '"topology", certified\|"topology handoff", certified' event/eventpg/census_integration_test.go)" = 2 \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s2 \
     '^event/(projection/(topology|topology_test|settlement_test|identity|pass|projection|partition_test|harness_test|loop_test)\.go|eventtest/(sections_topology|sections_checkpoints|defects_checkpoints|checkpoints_test|doc)\.go|eventmemory/[a-z_]*\.go)$' \
     event/projection/topology.go event/eventtest/sections_topology.go
```

The two `eventpg` arms are what make the census edit a delivery rather than a
promise, and neither pretends to be the live run. `go vet -tags=integration`
type-checks the tagged satellite without a database, so the file still compiles;
the `grep -c` arm counts the two census rows, so a `checkpointCensus()` left at
twelve is caught in this section rather than four sections later, when it would
fail `TestBothConformanceRunsCertifyEverySectionButTheOneThisStoreDeclines`
before any of §6's nineteen items were read. **Whether those two sections in fact
certify for `eventpg` is S6's to prove**, and if either does not, that is a real
finding about the store and not a census to edit.

`defects_test.go` has left the allowed set — this section
does not touch it, and a moved `inventoried` is the mistake the fence should
report. `event/eventpg/*` is outside the manifest by construction
(`scripts/checks.sh:335`), so the census edit does not appear in the moved set at
all.

**If `event/eventmemory/` appears in the moved set, the report says which file
and which obligation forced it.** The plan expects none.

**Checkpoint run, 2026-09-09, re-run in full after the review's two fixes** —
every arm, in the corrected order (the re-baseline before the full suite arm, as
S1 corrected, and the block above now spells that order), exit 0. `gofmt -l .`
printed nothing and `go build ./...` and `go vet ./event/...` were silent; the
three `-list` arms counted **11, 5 and 1**; the census `grep -c` arm counted
**2**; and `go vet -tags=integration ./event/eventpg/` was silent.

```
ok  	github.com/frostgrove/vv/event/projection	1.039s
ok  	github.com/frostgrove/vv/event/eventtest	1.260s
ok  	github.com/frostgrove/vv/event/eventmemory	1.258s
event-kernel-baseline: 144 files recorded in scripts/event_kernel.sha256
ok  	github.com/frostgrove/vv/event	6.862s
ok  	github.com/frostgrove/vv/event/eventmemory	1.516s
ok  	github.com/frostgrove/vv/event/eventtest	4.366s
ok  	github.com/frostgrove/vv/event/projection	1.550s
ok  	github.com/frostgrove/vv/scripts	26.684s
check-event-kernel: ok
the files under event/ this section moved:
  event/eventtest/checkpoints_test.go
  event/eventtest/defects_checkpoints.go
  event/eventtest/doc.go
  event/eventtest/sections_checkpoints.go
  event/eventtest/sections_topology.go
  event/projection/harness_test.go
  event/projection/identity.go
  event/projection/partition_test.go
  event/projection/pass.go
  event/projection/settlement_test.go
  event/projection/topology.go
  event/projection/topology_test.go
event-kernel-moved: ok
EXIT=0
```

The manifest is **144** files rather than 143, and the moved set is twelve rather
than nine: `identity.go`, `pass.go` and the new `settlement_test.go` are the
review's, and the allowed ERE names all three.

**No `event/eventmemory/` file is in the moved set and no top-level `event/`
file is either**, which is §5.1's obligation and the plan's own expectation.
`eventmemory.Checkpoints` certifies **13** of the fourteen sections and goes on
declining `durability`, with `Capabilities()` untouched.

`make check` is green (`check-deps`, `check-tiers`, `check-utils`,
`check-triplets`, `check-todo`, `check-replaces`, `check-tidy`,
`check-otel-schema`, `check-workspace`, `check-event-kernel` all `ok`), and the
dependency budget row did not move: `./event/projection` still costs `./runtime`
on top of `./crud` and `./errs`.

**§5.1's obligation, measured again.** `make api` regenerated
`docs/api/surface.md` and the `event` section is byte-identical to
`.git/event_surface_before_p4`:

```
$ diff -u .git/event_surface_before_p4 /tmp/event_surface_after.txt && echo "EVENT SECTION BYTE-IDENTICAL"
EVENT SECTION BYTE-IDENTICAL
```

The only section that moved is `event/projection`, which gained exactly `Split`
and `SplitSpec` on top of S1's vocabulary.

**The live suite was run even though this section is not a live one**, because
the census edit is a claim about `eventpg` that only a database can answer, and
because the two new conformance sections are asked of every store. Green twice in
a row, and the live checkpoint run certifies **all fourteen** sections including
`topology` and `topology handoff` — so the two census rows are a delivery rather
than a promise, and S6 inherits no open question about them:

```
$ FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1 -tags=integration ./event/eventpg/...
ok  	github.com/frostgrove/vv/event/eventpg	104.990s
ok  	github.com/frostgrove/vv/event/eventpg	104.095s

$ … -run '^TestTheCheckpointStoreSatisfiesTheContract$' -v
eventtest: topology: passed
eventtest: topology handoff: passed
```

**Re-run after the review's fixes, and it went red first**, which is the whole
value of having run it: the retirement probe is one extra `Load` at the resume,
and three live cases counted the projection's `Load`s as a total and blocked on
the second — so each stalled on the resume rather than on the settlement
(`the backend this case interrupts was not terminated`, `the new replica
delivered 0 pages`). `asked`'s counter is now keyed by the row the case is about,
which is what those cases always meant. Green twice in a row afterwards, and all
fourteen sections still certify:

```
$ FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1 -tags=integration ./event/eventpg/...
ok  	github.com/frostgrove/vv/event/eventpg	105.122s
ok  	github.com/frostgrove/vv/event/eventpg	106.946s

$ … -run '^TestTheCheckpointStoreSatisfiesTheContract$' -v
eventtest: topology: passed
eventtest: topology handoff: passed
```

**Mutation evidence.** Sixteen mutations, each applied to a non-test file (or to
the defect inventory, for two of them), run, and reverted. The last three are the
review's, and each of the two blocking findings was reproduced by driving it
before it was fixed:

| Mutation | Caught by |
|---|---|
| the higher child is written with `Progress{At}` alone, dropping the parent's `Highest` | `TestASplitWritesTwoChildrenAtTheParentsCursorInOneTransaction` — *"the child \"orders#3.3\" reports the watermark 0 where the parent reported 36"* |
| the higher child inherits the parent's `Applied` too | the same test — *"reports 12 applied and 0 quarantined where it takes 0 and 0, so the sum across the set is not the parent's"* |
| `handOver` does not retire the parent | the same test — *"the split issued 2 writes where a handoff is two child rows and one retirement: [load … save orders#1.3@1 save orders#3.3@1]"* — and §UC-140's four-statement control |
| an absent parent answers the two children instead of refusing | `TestASplitOfAParentWithNoRowIsRefusedForBothAbsences`, all three arms — *"splitting \"orders#1.1\" with no row answered \"orders#1.3\" and \"orders#3.3\", and neither has a cursor to start from"* |
| the child-beside-a-live-parent refusal is deleted | `TestASplitOverAnExistingChildRowIsRefused`, both arms |
| `inACallersTransaction` is deleted | `TestASplitUnderATwiceRunUnitIsOneSplitOrARefusal` — *"a split under a unit that opened nothing was answered <nil>"* |
| the unit's answer overrides the body's (`refused` dropped from the switch) | the same test — *"a unit that swallowed the refusal answered \"orders#1.3\" and \"orders#3.3\""* |
| `Unordered()` mints a fresh key on every call | `TestUnorderedSpreadsAndOneSequenceConcentrates` — *"Unordered applied 22 events once, 7 more than once and 7 never"* |
| `Unordered()` keys on the position **and the delivery round** — stable within one drain, different across a restart | the same test's restart control — *"\"k9/2\" was applied by \"0.3\" before the restart and by \"1.3\" after it"* — which is the one arm the strongest form above never reaches |
| `topologySection`'s body returns at once | `TestEveryCheckpointDefectIsReportedByItsOwnSection` — both topology defects *"reported \"passed\""* |
| `topologyHandoffSection`'s body returns at once | the same test — *"the suite no longer detects a checkpoint store that forgets outside the caller's transaction"* |
| the two `topology` defect rows are deleted from the inventory | `TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt` — *"no defect in this suite's inventory breaks the topology section"* |
| `topology` is added to the `undecorated` exemption list while its defects stand | the same test — *"the exemption is stale and the list only ever shrinks"* |
| `settle` re-keyed to `this.spec.Name` (GAP-1's defect, restored) | `TestALostFenceIsSettledOnTheRowOfThisRunnersOwnIdentity` — *"\"orders#0.1\" lost the fence and halted"*, and the same for `orders@2`; `TestAUnitThatRollsBackLeavesTheAdvanceOnThisRunnersOwnRow` — *"the row \"orders#0.1\" stands at advance 0 where this runner finished 5 pages"*; `TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity` reported `pass.go:494` by line. The control arm at `Ungenerated` over the whole key space stayed green, which is what says the other two measure the identity |
| the resume's retirement probe removed (GAP-2's defect, restored) | `TestTheReleaseThatRanBeforeASplitIsRefusedWhenItIsRedeployed`, both levels — *"redeploying \"orders\" was admitted beside its live children and reached following: 0 envelopes are held once and 36 more than once"*, and 18 more than once for `orders#1.1` |
| the retirement record's write removed from `handOver` | the same test, both levels — and `TestASplitOfAnAlreadyRetiredParentIsRefused` fell back to `noParent`: *"the refusal reads … and does not name \"already retired\""* |

---

### S3 — the park and the redrive  `[x]`   *(no database · moves the manifest)*

**Delivers ES-03.** The three renames, the blocking dispatch, the zero-cost fast
path, the two-dimensional bound, the third verdict, the claimed rotating redrive,
and the two new phases.

**Appendices** ES-03.

**Files** `event/projection/park.go`, `redrive.go` (new), `park_test.go`,
`redrive_test.go` (new); `errors.go` (modified, for `ErrParkFull` and
`ErrClaimLost`), `classify.go`, `spec.go`, `pass.go`, `state.go`,
`projection.go`, `doc.go` (modified); `retry_test.go`, `spec_test.go`,
`harness_test.go`, `topology_test.go` (modified); `event/eventpg/projection_integration_test.go`
and `projectioncase_integration_test.go`
(the rename, and the `Park` a `Quarantines` sink becomes, so the satellite still
compiles); `docs/modules/{en,ru}/projection.md`,
`docs/ai/flows/Index.md` and
`docs/ai/flows/FL-038-a-settled-cursor-becomes-a-durable-checkpoint.md` (the
rename rows, because `scripts/docs_test.go` fails on a symbol a doc names and
the tree no longer declares); and `scripts/projection_test.go` (modified, for
`TestEveryFieldOfAPublishedSpecIsRead` — outside the kernel manifest, which
covers `event/` alone).

**Amended during S3** (three lines, each a fact the section met rather than a
scope change):

1. `topology_test.go` joins the allowed set and the Files list. Its `drained`
   helper builds a projection with `OnPermanentFailure: Quarantine`, so the
   rename is a compile error there; and because `ParkSequence` blocks the
   sequence, that helper's parent now reaches `PhaseDegraded` rather than
   `PhaseFollowing`. Both are the rename landing, not a second change.
2. `event/eventpg/projectioncase_integration_test.go` joins the Files list for
   the same reason: `running.degraded` is what a live park's drained state is
   awaited by, and `following` cannot be reused for it.
3. **§UC-146's "without ever reaching the handler" is exact for a sequence the
   queue already holds and is stated one clause longer for the page that
   discovers the failure.** The whole-page attempt that discovers a permanent
   failure carried every envelope of that page in one batch and rolled it back as
   one — under tier A, which is the only tier `ParkSequence` constructs at, the
   handler's writes for it went back with the advance. From the moment the
   failure is known, an envelope behind a parked one is never delivered again.
   `TestAPermanentFailureParksItsSequenceAndTheEventsBehindIt` asserts the
   re-delivery half and `TestALaterPageParksWhatTheQueueAlreadyHolds` asserts the
   absolute half; the module page states both. Making the absolute half hold for
   the discovering page too would mean delivering one envelope at a time to every
   projection carrying a `Park`, which is the page batching gone.

**Realises** `Park`, `Letter`, `ParkSequence`, `ErrParkFull`, `Park.Holes`,
`Claim`, `ErrClaimLost`, `Redriver`, `RedriveSpec`, `Redrive`, `NewRedrive`,
`Redrive.Sequence`, `Redrive.Any`, `Retried`, `PhaseDegraded`, `PhaseBlocked`,
`State.Parked`, `Spec.Park`, and the failure arms of **P-3**. `ErrParkFull` and
`ErrClaimLost` are declared in `errors.go`, which S1 already opened.

**Covers** UC-146…UC-157, UC-189, UC-190, UC-191, UC-197, UC-198, UC-199;
INV-090, INV-091, INV-092, INV-093, INV-094, INV-095, INV-096 (the eviction
half).

**Tests** — untagged, in `event/projection`:

- `TestAPermanentFailureParksItsSequenceAndTheEventsBehindIt` — §UC-146. The page
  `A1 A2 B1 A3 B2`, `A2` permanently failing: `A1` applied, `A2` parked with its
  cause, **`A3` parked without ever reaching the handler** with a nil cause, `B1`
  and `B2` applied, one advance, `Quarantined` +2, `State.Parked` 1. **Control:**
  the same page with `A2` succeeding applies all five and parks nothing.
- `TestALaterPageParksWhatTheQueueAlreadyHolds` — §UC-147, with one `Holds` per
  envelope made inside the unit. **Control:** with the park empty, `Holds` is not
  called at all for that page.
- `TestTheFastPathCallsHoldsNeverAndSequencesOncePerResume` — §UC-148, §INV-095,
  and the one clearing rule measured **at both ends**: zero `Holds` and one
  `Sequences` over a full drain with an empty park; after one park, one
  `Sequences` per **pass** and one `Holds` per matching envelope; after a redrive
  empties the queue, the next pass reads zero and both counts return to the
  healthy ones.
- `TestAParkWriteRollsBackWithItsUnitAndAnOvertakenRereadsTheCount` — §UC-149,
  §INV-091. An injected rollback after the park leaves the queue empty and the
  advance unmoved. **Control:** one instance alone never re-reads the count
  within a run while it is zero.
- `TestAFullParkBlocksTheAdvanceAndSkipsNothing` — §UC-150, §INV-092. Three
  bounds, all three ending the pass identically: the unit rolls back — there is
  always a unit — the checkpoint does not advance, **nothing is parked including
  the letters earlier envelopes of the same page parked successfully**, no
  envelope is skipped, `PhaseBlocked`, no attempt consumed, `Ready` fails past
  `Tolerate`. **Control:** one eviction lets the very next pass advance with no
  restart, which is what distinguishes a block from a halt.
- `TestTheParksBoundIsPerSequenceAndNotPerQueue` — §UC-151, §INV-093. 1023
  single-letter sequences accept a letter for an existing sequence and a 1024th
  new one, and refuse a second new one. **Control:** the same queue at the letter
  bound refuses a letter for the full sequence while accepting one for another.
- `TestARedriveDrainsASequenceInInsertOrderAndTouchesNoCheckpoint` — §UC-152,
  §INV-094, with a recording `Checkpoints` asserting zero saves. `State.Parked`
  drops to zero at the loop's next **pass**. **Control:** a redrive of a sequence
  that is not parked answers `Retried{Applied: 0}` and no error.
- `TestARedriveStopsAtTheFirstLetterThatFailsAgain` — §UC-153. `A2` applied and
  evicted, `A3` requeued with its new cause and its attempt raised, **`A4`
  untouched**. **Control:** the same sequence with `A3` fixed drains fully.
- `TestARedriveRotatesByLeastRecentlyTried` — §UC-154. **Control:** `Any` on a
  park holding one failing sequence returns the same sequence every time, so the
  rotation is visible only when there is something to rotate.
- `TestTwoGatedRedrivesNeverProcessOneSequence` — §UC-191, §INV-094. Two
  concurrent `Any` calls driven through a gate so the contention is **caused**:
  exactly one processes `A`, the other takes another or answers `found = false`;
  no letter applied twice; `Release` called on every exit path including a panic,
  **carrying the `Claim` it was granted**, so a caller cannot release a grant it
  does not hold. **Control:** one caller alone behaves exactly as §UC-154 states.
- `TestAnExpiredClaimAppliesEvictsAndReleasesNothing` — **P-8**, §UC-191's other
  half and the one two simultaneous `Claim`s cannot see. Sequence `A` holds
  `A2 A3 A4`. Caller A claims it, applies and evicts `A2`, and its claim is then
  expired by the fixture between letters and re-granted to B. A's `Evict` of `A3`
  is refused with `ErrClaimLost`, so **its whole unit rolls back**: `A3` is not
  applied, its effect is not staged, nothing is evicted. A issues no `Touch` and
  no `Release`, so B's grant survives, and A answers `Retried{Applied: 1}` beside
  an error wrapping `ErrClaimLost`. **Control:** the identical run with no expiry
  drains `A2 A3 A4` in order, evicts three letters and releases once — so the
  refusal above is the expiry's and not the fixture's.
- `TestARedriveNamingAnotherSequencerOrAPartitionIsRefused` — §UC-155.
  `ErrTopology` naming both, before any handler is called; a `RedriveSpec` whose
  `Identity` carries a partition is refused the same way. **Control:** the
  matching name proceeds.
- `TestAParkedProjectionIsDegradedAndStillReady` — §UC-156, §INV-095.
  `PhaseDegraded`, `Parked` 2, `Quarantined` non-zero, `Ready` **passes**.
  **Control:** after the redrive, `PhaseFollowing` at the next pass with
  `Quarantined` still non-zero — the durable mark does not clear and the phase
  does.
- `TestAnEvictionLeavesAHoleAndDecrementsNothing` — §UC-157, §INV-096.
  **Control:** a sequence parked and then redriven to completion leaves
  `Quarantined` non-zero and `Holes` zero.
- `TestTwoGenerationsShareAParkAndSeeNoneOfEachOthersLetters` — §UC-189.
  Generation 2's `Sequences` answers zero because its key is `orders@2`.
  **Control:** two **partitions** of one generation do share a park,
  deliberately, which is what keeps a split from orphaning letters.
- `TestParkSequenceIsRefusedOutsideTierA` — §UC-190, §INV-090. Three specs:
  `AfterApply`, `InUnit` + `Unchecked`, `InUnit` + resolvable. The first two are
  `ErrSpec` naming the field and the reason; the third is accepted. **Control:**
  the same two specs with `Halt` are accepted, so the refusal is the park's and
  not the mode's.
- `TestAParkFailureThatIsNotFullHaltsAndAReadFailurePostpones` — **P-3**'s table,
  each arm asserted.
- `TestAParkIsInertBesideAPolicyThatDoesNotNameIt` — §UC-198, §INV-090's "every
  configuration that constructs" clause. A queue already holding the incoming
  page's sequence, beside `Halt`, at `AfterApply` and at `InUnit`: zero
  `Sequences`, zero `Holds`, zero letters, and the page applied or halted exactly
  as the named policy says. **Control:** the same queue and the same page under
  `ParkSequence` asks `Holds` three times and parks two.
- `TestEachParkMethodIsCalledWhereItsContractSaysItIs` — §UC-199. Counted per
  method by the unit bound on the context each was handed: `Holds` and `Park`
  inside, `Sequences` outside, `Holes` never. **Control:** a `Sequences` that
  refuses when no unit is bound leaves the projection at `PhaseRetrying` with the
  row at advance zero and the handler never called.
- `TestASpentAttemptBudgetParksATransientFailureAndARedriveClearsIt` — §UC-197.
  `Attempts: 2` over a page of four orders and a retryable failure: six handler
  calls, four sequences parked at attempt 3 each carrying the transient cause,
  one advance, `PhaseDegraded`; then a redrive drains all four and the next pass
  reports `PhaseFollowing` with `Quarantined` still 4. **Control:** the same page
  under a permanent failure parks at attempt 2 after five handler calls and
  publishes no `PhaseRetrying`.

**In `scripts/`, in the same counted pattern as this section's other structural
arm:**

- `TestEveryFieldOfAPublishedSpecIsRead` — every exported field of `Spec` and
  `RedriveSpec` is read by some line of `event/projection`, asked of the field
  **object** rather than of the name so a field called `Handler` is not counted
  read because another type's is. **Control:** a fixture spec with a `Classifier`
  nothing reads is reported and its read field is not.

**Checkpoint** (no database). `PARK` is the twenty names above; the
`./event/... ./scripts/` run is what catches the doc-symbol drift the renames
cause (`scripts/docs_test.go` fails on a symbol a doc names and the tree no
longer declares), and the two counted `scripts/` names are this section's
structural arms:

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s3
# … write the section …
PARK='^(TestAPermanentFailureParksItsSequenceAndTheEventsBehindIt|TestALaterPageParksWhatTheQueueAlreadyHolds|TestTheFastPathCallsHoldsNeverAndSequencesOncePerResume|TestAParkWriteRollsBackWithItsUnitAndAnOvertakenRereadsTheCount|TestAFullParkBlocksTheAdvanceAndSkipsNothing|TestTheParksBoundIsPerSequenceAndNotPerQueue|TestARedriveDrainsASequenceInInsertOrderAndTouchesNoCheckpoint|TestARedriveStopsAtTheFirstLetterThatFailsAgain|TestARedriveRotatesByLeastRecentlyTried|TestTwoGatedRedrivesNeverProcessOneSequence|TestAnExpiredClaimAppliesEvictsAndReleasesNothing|TestARedriveNamingAnotherSequencerOrAPartitionIsRefused|TestAParkedProjectionIsDegradedAndStillReady|TestAnEvictionLeavesAHoleAndDecrementsNothing|TestTwoGenerationsShareAParkAndSeeNoneOfEachOthersLetters|TestParkSequenceIsRefusedOutsideTierA|TestAParkFailureThatIsNotFullHaltsAndAReadFailurePostpones|TestAParkIsInertBesideAPolicyThatDoesNotNameIt|TestEachParkMethodIsCalledWhereItsContractSaysItIs|TestASpentAttemptBudgetParksATransientFailureAndARedriveClearsIt)$'
gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 \
&& go build ./... && go vet ./event/... \
&& test "$(go test -list "$PARK" ./event/projection/ | grep -c '^Test')" = 20 \
&& go test -race -count=1 -run "$PARK" ./event/projection/ \
&& test "$(go test -list '^(TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity|TestEveryFieldOfAPublishedSpecIsRead)$' ./scripts/ | grep -c '^Test')" = 2 \
&& go test -race -count=1 ./event/... ./scripts/ \
&& ./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s3 \
     '^event/projection/(park|redrive|park_test|redrive_test|classify|spec|pass|state|projection|errors|doc|retry_test|spec_test|harness_test|topology_test|unit_test|loop_test)\.go$' \
     event/projection/park.go event/projection/redrive.go
```

**Ran green, 2026-09-09, and re-run after the review's six blocking findings were
closed.** `gofmt` silent, `go build ./...` and `go vet ./event/...` clean, the
twenty names counted and run under `-race`, the two `scripts/` names counted:

```
ok  	github.com/frostgrove/vv/event/projection	1.031s
ok  	github.com/frostgrove/vv/event	6.795s
ok  	github.com/frostgrove/vv/event/eventmemory	1.514s
ok  	github.com/frostgrove/vv/event/eventtest	4.378s
ok  	github.com/frostgrove/vv/event/projection	1.572s
ok  	github.com/frostgrove/vv/scripts	26.709s
event-kernel-baseline: 148 files recorded in scripts/event_kernel.sha256
check-event-kernel: ok
the files under event/ this section moved:
  event/projection/classify.go
  event/projection/doc.go
  event/projection/errors.go
  event/projection/harness_test.go
  event/projection/park.go
  event/projection/park_test.go
  event/projection/pass.go
  event/projection/projection.go
  event/projection/redrive.go
  event/projection/redrive_test.go
  event/projection/retry_test.go
  event/projection/spec.go
  event/projection/spec_test.go
  event/projection/state.go
  event/projection/topology_test.go
event-kernel-moved: ok
```

Beside it: `make unit` green over every module, `make vet` clean, `make check`
green (`check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`,
`check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace`,
`check-event-kernel`), `make examples` green, `make api` regenerated with the
`event` section unchanged by the review's edits — a removed struct field is not a
line of the baseline, which is the reason `TestEveryFieldOfAPublishedSpecIsRead`
is a `scripts/` arm and not a `make api` diff — and only `event/projection`
moved. The satellite was not merely compiled: the whole tagged suite ran live
against PostgreSQL 17.9 — `ok github.com/frostgrove/vv/event/eventpg 104.855s`
after the remediation, `105.409s` before it — with `TestAParkIsSequenceGranular`
(the renamed `TestAQuarantineIsEnvelopeGranular`) green against a real `Park`
table, and the live `queue.Sequences` reading through `into.on(ctx)`, which falls
back to the pool when nothing is bound and therefore conforms to the corrected
`Park` contract.

**Re-run 2026-09-12, after the audit/otel/i18n line was merged in, and the two
reds it brought are named rather than absorbed.** The section's own gates are
unchanged:

```
gofmt -l .                                                         0
go build ./... ; go vet ./event/...                                clean
go test -list "$PARK" ./event/projection/ | grep -c '^Test'        20
ok  	github.com/frostgrove/vv/event/projection	1.028s   (the twenty, -race)
ok  	github.com/frostgrove/vv/event	6.868s
ok  	github.com/frostgrove/vv/event/eventmemory	1.508s
ok  	github.com/frostgrove/vv/event/eventtest	4.346s
ok  	github.com/frostgrove/vv/event/projection	1.561s
ok  	github.com/frostgrove/vv/scripts	6.255s   (the two counted structural names)
event-kernel-baseline: 148 files recorded in scripts/event_kernel.sha256
check-event-kernel: ok
event-kernel-moved: ok            (the same fifteen files, all event/projection/)
ok  	github.com/frostgrove/vv/event/eventpg	111.037s   (-tags=integration, PostgreSQL 17.9)
```

The whole `./scripts/` package and `make check` are **red at HEAD, and neither
red is this section's**. `go test ./scripts/` fails on exactly one arm,
`TestNoI18nPackageCostsMoreThanItsErrorSeam` (*"the i18n extension reaches
github.com/go-json-experiment/json/jsontext outside its error seam"*), whose
subject is `i18n/cmd/vv-i18n` importing `jsonv2` directly; `make check` fails at
`check-tidy` over **29 satellites** — `app/appfx`, `crud/http/crudgin`,
`auth/rpc/authgrpc`, `audit/auditpg` (which carries no `go.sum` at all), `test`,
and among them `event/eventpg`, whose own `go.mod` this phase never touched. Both
were driven to their origin rather than assumed: at `ce36e2d`, the tree this
section shipped on, `make check-tidy` answers `check-tidy: ok` and the i18n arm
does not yet exist; both appear at `fefa3e9`/`939bcd9`, the merge that brought the
audit, otel and i18n line in. `make unit` fails on that one i18n arm and on
nothing else; `make vet` and `make examples` are green. `make api` is likewise
not idempotent at HEAD for a reason outside this phase — `scripts/api-surface`
was rewritten in `c5e7b62` and the baseline in `docs/api/surface.md` was last
written in the old format, so a regeneration reformats all 13 860 lines of every
package's surface; the file was left as committed, because the repository's own
rule is that a diff there is a question for a person. The regeneration was read
before being discarded, which is where §GAP-3's close criterion is met:
`RedriveSpec` renders with six fields — `Identity`, `Handler`, `Sequencer`,
`Park`, `Unit`, `Destination` — and no `Classifier`.

**Falsified rather than asserted.** Fourteen mutations were driven, each restored,
and each named the test that caught it: the isolation pass continuing to the next
envelope of a parked sequence (§UC-146, the count arm); `delivering()` never
choosing the blocking page (§UC-147); the count read every pass whatever it says
and the loop's own park not making it believe (§UC-148, both ends); `ErrParkFull`
halting instead of blocking, and a blocked pass consuming an attempt (§UC-150);
the per-sequence letter bound dropped (§UC-151 **and** §UC-150's first bound); the
redrive continuing past a letter that failed again (§UC-153); the letters of a
sequence answered out of insert order (§UC-152); the grant never compared, so a
claim is a read (§P-8); the release not deferred, so a panic keeps it (§UC-191);
`followed()` never answering `PhaseDegraded` (§UC-156); the tier refusal dropped
(§UC-190, and `TestNewRefusesEverySpecItCannotAssemble` with it); the letter keyed
by the runner's full name instead of `Identity.Whole()` (§UC-189's control, which
was **strengthened to a live partitioned runner because the first version of it
survived this mutation**); the sequencer never compared (§UC-155); `Holes`
counting only what is queued (§UC-157); and the blocking test made outside the
unit (§UC-147's `outside` count).

**Six more were driven after S3's review**, one per blocking finding, so that no
finding is recorded closed on a green suite alone. Each was applied to the
shipped tree, run, and restored:

| Mutation | Test | Result |
|---|---|---|
| `withDefaults` no longer drops `spec.Park` beside a policy that does not name it | `TestAParkIsInertBesideAPolicyThatDoesNotNameIt` | **FAIL** — *"the projection never published a state where the whole log was read: it published [draining draining degraded]"*, both arms |
| `unblockedPage` asks `Holds` on a context of its own rather than the unit's | `TestEachParkMethodIsCalledWhereItsContractSaysItIs` | **FAIL** — *"Holds ran inside the caller's unit 0 times where the contract says 1"* |
| `Redrive.claimed` releases on a lost claim (`_ = lost` for `if lost { return }`) — **the arm the old test survived** | `TestAnExpiredClaimAppliesEvictsAndReleasesNothing` | **FAIL** — *"Release was called 1 times over a grant this caller had already lost"* |
| `RedriveSpec` grows back an inert `Classifier` | `TestEveryFieldOfAPublishedSpecIsRead` (`scripts/`) | **FAIL** — *"RedriveSpec.Classifier is published and no line of the package reads it"* |
| `permanent()` no longer promotes a retryable failure once the budget is spent (`return false` for `this.attempt >= this.spec.Attempts`) | `TestASpentAttemptBudgetParksATransientFailureAndARedriveClearsIt` | **FAIL** — *"the projection never published a state where the budget was spent and the page was parked: it published [draining retrying retrying]"* |
| `parking()` counts an `ErrParkFull` letter as written instead of ending the pass | `TestTheParksBoundIsPerSequenceAndNotPerQueue` | **FAIL** — *"the projection never published a state where the queue refused the letter and the partition stopped: it published [draining draining degraded]"* |

The last row is what answers the vacuity finding rather than the prose around it:
the two arms that assert the reference queue's own `room` still cannot fail on
framework code, but the third and fourth arms now do — a mutation in `pass.go`
reddens them, so §UC-151's row counts an arm with a framework subject beside the
two with a fixture one.

The old fourteenth arm — *"the release not deferred, so a panic keeps it"* — is a
different mutation and still reddens `TestTwoGatedRedrivesNeverProcessOneSequence`;
what it never touched is the suppression, because the `*park` fake's own `owns()`
produced the outcome either way. The fake now counts the call.

---

### S4 — generations, the barrier and the cutover  `[x]`   *(no database · moves the manifest)*

**Delivers ES-04.** A generation is a number in the recorded name; the barrier is
a `Position` and that is legal; `Cutover` derives its own evidence and issues one
fenced write; a rollback is the same call exchanged.

**Appendices** ES-04.

**Files** `event/projection/generation.go` (new), `generation_test.go` (new);
`errors.go` (modified, for `ErrRetired`), `spec.go`, `projection.go`, `pass.go`
(modified, for `Pace`); `spec_test.go` (modified, for the `Pace` refusal arm);
`generation_test.go` carries the `Ignore` case. And **`docs/ai/flows/Index.md`**,
which is outside the kernel manifest and is not optional:
`scripts/docs_test.go`'s `TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex`
goes red the moment a new file of `event/projection` is not named by the reverse
index, so `generation.go` joins FL-038's row list in this section rather than in
S6.

**Amended during S4** (four lines, each a fact the section met rather than a
scope change):

1. **`Cutover` asks `Split`'s question about the caller's unit**, inside it and
   before anything is read: `Tracker.Transaction(ctx)` must answer a valid
   authority, or the call is `ErrSpec` naming `Unit`. The contract block says the
   evidence and the switch are one snapshot; a unit that opened nothing makes
   them two, and a generation that was at the barrier when it was measured is not
   one when the row moves. It is a second spelling of `inACallersTransaction`
   rather than a call to it, because that helper lives in `topology.go`, whose
   message is about a handoff and which this section's manifest fence does not
   admit.
2. **`Reached` refuses a barrier of another projection and one observed from the
   generation it is being asked about.** The first is also how the zero `Barrier`
   is refused — it names no projection — which closes the same door **P-11**
   closes for `Cover{}` and `Identity{}` at a value the plan's own contract block
   left open. The second is the `min` over its own rows answering true by
   arithmetic.
3. **No refusal of this section renders a `Position`**, and that is the kernel's
   rule rather than a preference: `event/refusalmessages_test.go` reports a
   message built over one, so `behindTheBarrier` names the rule and the two
   generations, and the numbers are `Readiness.Behind` and `Barrier.At`, which
   `Reached` answers as values. The first draft named them in the text and the
   whole `./event/...` run went red on it.
4. **`Cutover`'s spec door has a test of its own** —
   `TestACutoverTakesNoBarrierAndDerivesItsOwn`'s first subtest, eight refusals
   with a legal control — because `cutting` and the unit check were otherwise
   code no test could falsify, which is the shape this phase's mutation pass
   exists to find.
5. **"A compile-time assertion over the struct's fields" is spelled
   `reflect.TypeFor[projection.CutoverSpec]()`**, walked for a field named
   `Barrier` and for a field of type `Barrier`, with an arm that fails if the
   struct has no fields at all. Go cannot assert the *absence* of a field at
   compile time — the nearest is an unkeyed composite literal, which breaks on
   every field added for any reason and would therefore be a test about field
   order. The walk fails on the one change it is about.

**Amended again by the S4 implementation review, 2026-09-12** — two blocking
findings closed, both in the same seam and both reproduced before they were
fixed:

6. **GAP-1 [critical] — silence on the retiring side is refused, and a retired
   member is told apart from one that never ran.** `Observe`'s all-members-fresh
   arm answered the origin and `Cutover` guarded `held.recorded == 0` on the
   *arriving* census only, so a wrong `Retiring` cover yielded `Barrier.At = 0`
   and every arriving generation cleared it by `x >= 0`. Two fixes, at two
   levels. `surveyed` now asks `retired(...)` of each absent member, so a cover
   whose members a `Split` retired is `ErrTopology` naming the retirement row —
   §UC-180 one level down, and `Observe` answers it too. `switching` now folds the
   retiring census out of one walk (`observed`, of which `Observe` is the public
   half) and refuses `recorded == 0` with `ErrRetired`. The contract block above
   carries both. `TestACutoverTakesNoBarrierAndDerivesItsOwn`'s `legal` fixture
   gained the retiring row it never had: a legal cutover has a retiring generation
   that ran, and the spec-door subtest was asserting eight refusals over a spec
   that was itself unobservable.
7. **GAP-2 [high] — the read-target overlap window is named, bounded and
   adjudicated, and the comments no longer over-promise.** `inTheCallersUnit`,
   `Cutover` and `Generations` claimed the caller's unit made the evidence and the
   switch one snapshot; against a *running* retiring generation it cannot, and no
   isolation level closes it ([[D-126]] forbids choosing one). The three comments
   now state what the unit buys — the arriving generation's rows and the ownership
   row move together — beside what it does not, `Cutover` carries the window in
   Marten's own shape with the way to avoid it, `Spec.Pace` says to drop it before
   cutting over, and the Axon `resetTokens` alternative is refused in writing with
   its reason. It is the one finding whose answer is a contract statement rather
   than a refusal, so it is pinned by a measurement instead: see the two tests
   below.

**Realises** `Generations`, `Barrier`, `Observe`, `Readiness`, `Reached`,
`CutoverSpec`, `Cutover`, `ErrRetired`, `Spec.Pace`, and **P-4**, **P-5**.

**Covers** UC-143, UC-159, UC-180, UC-162, UC-163, UC-164, UC-166, UC-167;
INV-089, INV-096 (the split-arithmetic half), INV-098, INV-099.

**Tests** — untagged, in `event/projection`:

- `TestObserveAnswersTheMinimumAcrossTheCover` — §UC-143, §INV-089. Four
  partitions at 900/1200/1150/1300 answer 900. Nothing answers a `max`, a sum or
  an average. **Control:** `Whole()`, a cover of one, answers its own number, so
  the `min` is not a constant.
- `TestObserveRefusesACoverWhoseMemberHasNoRow` — §UC-180. `ErrTopology` naming
  the member; an absent row is **not** read as position zero. **Control:** the
  same cover with all four rows answers 900, and a cover all of whose rows are
  absent answers the origin — the three arms told apart by the rows rather than
  by the caller. Plus **P-4**: an `of` carrying a partition is refused.
- `TestABarrierIsObservedAndReached` — §UC-159, §INV-099. `Reached: false,
  Behind: n` below the barrier and `true` at or above it, with `Quarantined`
  summed and `Holes` asked once. Nothing turns the barrier into a cursor, resumes
  from it, or stores it as a checkpoint.
- `TestACutoverRefusesOnHolesAndNotOnQuarantined` — §UC-163, §INV-098. Three
  arms: two letters still parked (refused), one letter evicted unapplied
  (refused), everything parked **redriven to completion** with `Quarantined` 2 and
  `Holes` 0 (**proceeds without the override**). **Control:** a cutover with
  nothing ever parked proceeds and both numbers are zero.
- `TestACutoverTakesNoBarrierAndDerivesItsOwn` — §INV-098, and **GAP-10's arm**.
  `CutoverSpec` has no `Barrier` field, asserted by a compile-time assertion over
  the struct's fields; `Cutover` calls `Observe` on the retiring cover inside the
  same unit, and a fixture retiring generation with live rows cannot yield a
  barrier of zero. **The fixture is a `Split` and not a hand-written pair of
  rows:** a live `orders` at position 900_000 is split through `Split`, and
  `Observe` over the resulting cover answers **900_000**, the parent's number.
  **Control:** the same two children written by hand at `Highest: 0` — legal rows
  the tracker door admits — make `Observe` answer the origin, `Reached` trivially
  true for a generation that has applied nothing, and the cutover proceed. That
  control is what makes the positive arm evidence: without a split in the fixture
  the test passes whatever `Split` writes.
- `TestTwoCutoversLeaveOneWinnerAndOneConflict` — §UC-162. One commits, the
  other's fenced `Activate` matches nothing and answers `ErrConflict`; the row
  holds `to` exactly once; neither reads the row and then writes it in two
  statements.
- `TestARollbackIsTheSameCallExchangedAndErrRetiredWhenTheRowsAreGone` — §UC-164.
  **Control:** a rollback to a generation whose checkpoint rows were dropped
  answers `ErrRetired` and writes nothing.
- `TestAnIgnoredTypeLetsTheOldGenerationKeepServing` — §UC-166 and the [[D-131]]
  amendment. Without preparation the old generation halts with `ErrUnrouted`;
  with `Ignore(router, "orders", "orders.RefundIssued")` in its **own** build it
  skips them and keeps serving. **Control:** a type of an *uncovered* family is
  skipped and counted by `Router.Skipped()` with no declaration at all, so the
  `Ignore` requirement is scoped to covered families rather than universal.
- `TestPaceThrottlesTheReadWhileDrainingAndNotWhileFollowing` — §UC-167, **P-5**,
  driven through the injected `Ticks`. **Control:** `Pace: 0` reads as fast as the
  store answers.
- `TestSplitPreservesTheSumAcrossThePartitionSet` — §INV-096's split arithmetic,
  asserted as a sum over the cover before and after.

**Added by the S4 implementation review** — two names, §UC-200 and §UC-201:

- `TestACutoverRefusesARetiringCoverNoMemberOfWhichHoldsARow` — §UC-200, GAP-1.
  Two arms. **(a)** a drained `orders` is `Split`, and a cutover declaring the
  pre-split cover is `ErrTopology` naming `orders#split`, with the ownership row
  unmoved; `Observe` over that cover is refused too. **Control:** the same cutover
  over the children's cover proceeds, so the refusal is the retirement and not the
  cover's size. **(b)** a four-partition live generation declared as `Whole()` is
  `ErrRetired` naming the identity, with nothing written. **Two controls:** a
  generation that genuinely never ran — no rows and no retirement — still answers
  the origin from `Observe` with a nil error, which is §UC-159's documented answer
  unmoved; and the same cutover over the cover that generation *does* record at
  proceeds.
- `TestTheCutoverWindowIsWhatTheRetiringGenerationAdvancedUnderIt` — §UC-201,
  GAP-2. The retiring generation commits an advance from 1000 to 1050 inside the
  `Activate` and outside the cutover's unit. The cutover is admitted — that is the
  contract — and what is asserted is the **bound**: the read target lands exactly
  50 positions behind, the retiring row stands at advance 2 (its own runner's two
  writes and no third), and the cutover issued **zero** checkpoint saves, which is
  the Axon-claim refusal made falsifiable. **Control:** the same cutover with the
  retiring generation at rest leaves the two generations at the same watermark,
  so draining first is what closes the window and the measurement is of the
  advance rather than of the switch.

**Checkpoint** (no database):

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s4
# … write the section …
GEN='^(TestObserveAnswersTheMinimumAcrossTheCover|TestObserveRefusesACoverWhoseMemberHasNoRow|TestABarrierIsObservedAndReached|TestACutoverRefusesOnHolesAndNotOnQuarantined|TestACutoverTakesNoBarrierAndDerivesItsOwn|TestACutoverRefusesARetiringCoverNoMemberOfWhichHoldsARow|TestTheCutoverWindowIsWhatTheRetiringGenerationAdvancedUnderIt|TestTwoCutoversLeaveOneWinnerAndOneConflict|TestARollbackIsTheSameCallExchangedAndErrRetiredWhenTheRowsAreGone|TestAnIgnoredTypeLetsTheOldGenerationKeepServing|TestPaceThrottlesTheReadWhileDrainingAndNotWhileFollowing|TestSplitPreservesTheSumAcrossThePartitionSet)$'
gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 \
&& go build ./... && go vet ./event/... \
&& test "$(go test -list "$GEN" ./event/projection/ | grep -c '^Test')" = 12 \
&& go test -race -count=1 -run "$GEN" ./event/projection/ \
&& test "$(go test -list '^TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity$' ./scripts/ | grep -c '^Test')" = 1 \
&& go test -race -count=1 ./event/... ./scripts/ \
&& ./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s4 \
     '^event/projection/(generation|generation_test|errors|spec|pass|projection|spec_test|harness_test)\.go$' \
     event/projection/generation.go event/projection/errors.go
```

`errors.go` is in the allowed set and required by path for the same reason it was
in S1's: `ErrRetired` is what `Cutover`'s rollback arm wraps, it has one home, and
a section that did not declare it did not deliver.

**One line of the chain is run out of order and the reason is mechanical.**
`go test ./scripts/` carries `TestTheEventKernelOfThisRepositoryIsWhereThisPhaseLeftIt`,
which runs `check-event-kernel` over the *current* manifest — so the suite is red
until the baseline is re-recorded, and the chain above re-records it one line
later. The baseline was therefore taken first and the suite run after it. Nothing
is weakened by that: `event-kernel-moved` compares the **new** manifest against
`.git/event_kernel_before_s4`, which was recorded before the first file of this
section was written, so the fence still reads the whole section's diff.

**Ran green, 2026-09-12 — re-run line for line after the review's two blocking
findings were closed.** `gofmt` silent, `go build ./...` and `go vet ./event/...`
clean, the **twelve** names counted and run under `-race`, the `scripts/` arm
counted:

```
gofmt -l .                                                          0
go build ./... ; go vet ./event/...                                clean
go test -list "$GEN" ./event/projection/ | grep -c '^Test'         12
ok  	github.com/frostgrove/vv/event/projection	1.321s   (the twelve, -race)
go test -list '^TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity$' ./scripts/ | grep -c '^Test'   1
event-kernel-baseline: 150 files recorded in scripts/event_kernel.sha256
ok  	github.com/frostgrove/vv/event	7.017s
ok  	github.com/frostgrove/vv/event/eventmemory	1.506s
ok  	github.com/frostgrove/vv/event/eventtest	4.344s
ok  	github.com/frostgrove/vv/event/projection	1.887s
check-event-kernel: ok
the files under event/ this section moved:
  event/projection/errors.go
  event/projection/generation.go
  event/projection/generation_test.go
  event/projection/pass.go
  event/projection/projection.go
  event/projection/spec.go
  event/projection/spec_test.go
event-kernel-moved: ok
```

Beside it: `make vet` clean, `make examples` green (exit 0), the structural checks
run individually — `check-deps`, `check-tiers`, `check-utils`, `check-triplets`,
`check-todo`, `check-replaces`, `check-otel-schema`, `check-workspace`,
`check-event-kernel` — all `ok`. The satellite was not merely compiled: the whole
tagged suite ran live against PostgreSQL 17.9 —
`ok github.com/frostgrove/vv/event/eventpg 107.661s`
(`-race -count=1 -tags=integration`), the first run's 104.893s within run noise.
The exported surface is byte-identical to the one the review walked: `observed`,
`neverHandedDown` and `unobservable` are unexported, so `go doc -all
./event/projection`'s `func`/`type` lines diff empty against HEAD.

**One red the fix caused, and the gate caught it rather than a reviewer.**
Folding `Observe`'s body into a shared `observed` moved its three door checks out
of the exported function, and `scripts/projection_test.go`'s
`TestEveryDoorTakingACoverOrAnIdentityRefusesItsZeroValue` — **P-11**'s rule,
held structurally — reported *"Observe takes the Identity of and never asks
whether it was built"* and the same for its `Cover`. The doors were put back in
`Observe`'s own body and `observed` left as the walk, with a comment saying where
a cutover's doors are instead: `cutting`'s, asked of the spec before the unit
opens. That is the better placement anyway — the refusal reaches the caller
without a transaction being opened for it.

**The two reds at HEAD are the two S3 named, and neither is this section's.**
`go test ./scripts/` and `make unit` fail on exactly one arm,
`TestNoI18nPackageCostsMoreThanItsErrorSeam` (*"the i18n extension reaches
github.com/go-json-experiment/json/jsontext outside its error seam"*), whose
subject is `i18n/cmd/vv-i18n`; `make check` stops at `check-tidy` over the same
29 satellites, `event/eventpg` among them, whose `go.mod` this phase has never
touched. Both arrived with the audit/otel/i18n merge (`fefa3e9`/`939bcd9`) and
this section touches no file of either subject — `git status` lists
`event/projection/{errors,pass,projection,spec,spec_test}.go`,
`event/projection/generation{,_test}.go`, `docs/ai/flows/Index.md` and
`scripts/event_kernel.sha256` and nothing else. Unchanged after the review's
closure: no `go.mod` or `go.sum` was touched and no file of `i18n/` was, so the
`check-tidy` stop is still `./vvdb/dbpgx`'s `go.sum` and the one `./scripts/` red
is still the i18n arm.

**Falsified rather than asserted.** Eleven mutations were driven, each applied to
the shipped tree, run, and restored; every one of them was caught, and the first
message each test printed is quoted:

| Mutation | Test | Result |
|---|---|---|
| `surveyed` no longer refuses a cover whose member holds no row | `TestObserveRefusesACoverWhoseMemberHasNoRow` | **FAIL** — *"a cover with a member that has not reported answered `<nil>`"* |
| `Observe` folds the maximum instead of the minimum | `TestObserveAnswersTheMinimumAcrossTheCover` | **FAIL** — *"answered a barrier of 1300, where the max is 1300 … and the one position the projection as a whole delivered past is 900"* |
| `Cutover` refuses on the cumulative `Quarantined` instead of on `Holes` | `TestACutoverRefusesOnHolesAndNotOnQuarantined` | **FAIL** — *"a generation that recovered completely was refused the cutover"*, the arm GAP-18 is about |
| `Cutover` reads the ownership row and then writes it | `TestTwoCutoversLeaveOneWinnerAndOneConflict` | **FAIL** — *"the ownership row was read 2 times by a cutover, and a read followed by a write is what admits two winners"* |
| the `held.recorded == 0` arm dropped, so a generation with no rows is merely behind | `TestARollbackIsTheSameCallExchangedAndErrRetiredWhenTheRowsAreGone` | **FAIL** — the control reads the refusal as `ErrTopology` where a dropped generation is `ErrRetired` |
| `Split` hands its children the parent's cursor **without** its watermark | `TestACutoverTakesNoBarrierAndDerivesItsOwn` | **FAIL** — *"the split children answer a barrier of 0 where their parent stood at 36, and a barrier at the origin is reached by anything"* |
| `throttled` never answers `waitPace` | `TestPaceThrottlesTheReadWhileDrainingAndNotWhileFollowing` | **FAIL** — *"the projection never asked for an interval to wait"* |
| `Pace` throttles every read, including the first and the one after the poll | `TestPaceThrottlesTheReadWhileDrainingAndNotWhileFollowing` | **FAIL** — the drain never finishes inside the three intervals the case grants it |
| `Reached` admits a barrier of another projection, and the zero `Barrier` with it | `TestABarrierIsObservedAndReached` | **FAIL** — *"the zero Barrier was admitted as evidence and answered `<nil>`"* |
| a router never claims a type its own build ignores | `TestAnIgnoredTypeLetsTheOldGenerationKeepServing` | **FAIL** — *"it published [draining halted]"*, which is [[D-131]] without the escape |
| `Split` gives **both** children the parent's counts | `TestSplitPreservesTheSumAcrossThePartitionSet` | **FAIL** — *"the two children account for 24 applied and 48 quarantined where their parent accounted for 12 and 24"* |

The sixth and the eleventh are mutations of `topology.go`, which this section does
not own and does not change: they are what makes the split a *fixture* rather than
decoration, which is the sentence the plan's own test list argues for.

**Five more, driven when the review's findings were closed**, each applied to the
fixed tree, run, and restored:

| Mutation | Test | Result |
|---|---|---|
| the retiring `stood.recorded == 0` refusal dropped | `TestACutoverRefusesARetiringCoverNoMemberOfWhichHoldsARow` | **FAIL** — *"a four-partition generation declared as Whole() answered `<nil>`, and a read target moved on the origin lands on a generation that applied three events"* |
| the `neverHandedDown` call dropped from `surveyed` | the same test, split arm | **FAIL** — *"observing the pre-split cover of a generation standing at 36 answered `<nil>`"* |
| `neverHandedDown` refuses **every** absent row, not a retired one | `TestObserveRefusesACoverWhoseMemberHasNoRow` | **FAIL** at §UC-159's control, *"a cover all of whose rows are absent"* — which is what keeps `Observe`'s documented answer where it was |
| the retiring refusal made unconditional | the new test's **two** controls | **FAIL** — *"the cover the retiring generation actually records at was refused too, so the arm above is about the cover's size and not about the retirement"* |
| `switching` claims the retiring rows the way Axon's `resetTokens` does | `TestTheCutoverWindowIsWhatTheRetiringGenerationAdvancedUnderIt` | **FAIL** — *"a checkpoint row moved between the save this transaction staged and its commit"*, which is the fence fight the refusal in `Cutover`'s contract is about |

The third and fourth are the ones that matter: they are mutations that make the
new refusals *wider*, and both controls catch them. A refusal nothing can
over-fire is one nobody has measured.

---

### S5 — the effect capability and its gate  `[x]`   *(no database · moves the manifest)*

**Delivers ES-06.** The capability is a value, not a mode; three suppressors,
cheapest first; the barrier is per envelope, one side of its comparison is a
checkpoint column and the other is a deployment-held constant that says so; an
effect belongs to an applied envelope and follows it into the park.

**Appendices** ES-06.

**Files** `event/projection/effect.go` (new), `effect_test.go` (new);
`spec.go`, `pass.go`, `redrive.go`, `doc.go` (modified); `generation.go`
(modified in round 2, for the obligation `Active` carries — GAP-1).

**Realises** `Effect`, `Effects`, `EffectsFunc`, `Spec.Effects`,
`Spec.EffectsAfter`, `Spec.Generations`, `RedriveSpec.Effects` and
`RedriveSpec.Generations` with `RedriveSpec.EffectsAfter` **refused**, the
suppressor order, the `Generations.Active` locking obligation, and **P-3**'s
`Stage` arms.

**Covers** UC-169, UC-170, UC-172, UC-174, UC-175, UC-176, UC-181, UC-182,
UC-184, UC-193, UC-202, UC-203, and UC-147's effect clause; INV-100, INV-101
(the untagged half), INV-104, INV-106.

**Tests** — untagged, in `event/projection`:

- `TestTheLiveGenerationStagesAndTheRebuildDoesNot` — §UC-169. Generation 1 with
  `Effects` and the row holding 1 stages once per applied envelope past its
  barrier, inside the unit; generation 2 with `Effects: nil` stages nothing.
  **Control:** the row is flipped to 2 and generation 2 is restarted **with
  `Effects` set** — it stages, and generation 1, still holding `Effects`, stages
  nothing. That isolates the two suppressors from each other, which is what
  §UC-169's earlier control could not do.
- `TestEffectsAreRefusedBesideAfterApply` — §UC-170. `ErrSpec` naming the mode and
  saying why. **Control:** the same spec at `InUnit` is accepted.
- `TestAStraddlingPageStagesExactlyTheEnvelopesPastTheBarrier` — §UC-172,
  §INV-106. A page `N-2 N-1 N N+1 N+2` calls `Stage` once with exactly `N+1` and
  `N+2`; the handler is called with all five. **Control:** a page entirely at or
  below `N` does not call `Stage` at all, **not even with an empty slice**, and a
  page entirely above it stages all of it.
- `TestAParkedEnvelopeIsNotStagedAndIsNotLost` — §UC-181, §UC-147, §INV-106. A
  page `A2 B1` with `A` failing permanently under `ParkSequence`: `Stage` is
  called once with exactly `B1`; `A2` is parked with its effect. **A second arm
  for the other applier** (round 2, GAP-2): a later page `A3 C1` delivered while
  the queue already holds `a` runs `unblockedPage`, which parks `A3` *without
  calling the handler*; `Stage` takes `C1` alone and the letter carries `A3`'s
  effect. The two appliers hold the owed envelopes in two different variables
  three lines apart, so one arm falsifies one of them and the mutation
  `held.owed = this.matched` survived the whole package until this arm existed.
  **Control:** the same two pages with nothing parked stage both, and both times,
  so each exclusion is attributable to the queue.
- `TestARedriveStagesWhatItAppliesAndAnEvictionStagesNothing` — §UC-182. Applying
  `A2` stages `A2`'s effect in the same transaction as the apply and the `Evict`.
  **Control:** the same redrive with `Effects` nil applies both letters and stages
  nothing, and a redrive whose identity's generation does not own the row stages
  nothing either. **A refusal arm** (round 2, GAP-3): a `RedriveSpec` carrying a
  non-zero `EffectsAfter` is refused with `ErrSpec` naming the field, because the
  only thing a barrier can do on this side is suppress an effect that is owed and
  nothing will offer it again. **Two controls:** the identical spec at zero
  constructs, and `New` accepts the very barrier this door refuses — so the
  refusal is the queue's and not the field's.
- `TestTheOwnershipRowIsNotReadWhenThereIsNothingToStage` — §UC-174.
  `Generations.Active` is not called at all, counted on a recording
  implementation. **Control:** a page with one applied envelope past the barrier
  reads it exactly once.
- `TestABarrierBesideAParkIsRefused` — §UC-175. **Controls:** `EffectsAfter > 0`
  with `Halt` is accepted, and `ParkSequence` with no barrier is accepted, so the
  refusal is the combination's.
- `TestABarrierWithNothingToGateIsRefused` — §UC-176. **Control:** `Effects` with
  no `EffectsAfter` is accepted and stages from the first envelope.
- `TestASecondProjectionNameWithEffectsStagesEveryHistoricalEvent` — §UC-184, the
  **measured edge** of ES-06's guarantee. `Spec.Name: "orders-rebuild"` at
  `Ungenerated` with `Effects` set stages an effect for every historical event,
  asserted. **Control:** the same rebuild spelled `Generation: 2` with the row
  holding 1 stages nothing.
- `TestAnUngeneratedProjectionWithGenerationsStopsStagingAtTheCutover` — §UC-193,
  **P-7**, and it is the test that fails against the suppressor list [SPEC] §1.6
  implies. A spec at `Generation: Ungenerated` with `Effects` **and**
  `Generations`, over a row that answers `Ungenerated`: it stages, because the
  row names its own generation. `Activate("orders", Ungenerated, 2)` commits, and
  its very next pass stages nothing while its handler keeps applying and its
  checkpoint keeps advancing — the shape §UC-173 uses, one generation lower.
  **Control:** the identical spec with `Generations: nil` keeps staging after the
  same cutover, asserted, which is the stated limit of the boundary measured
  rather than written down — and, gated on `spec.Generation != Ungenerated`, the
  first arm behaves like the control and the test is red.
- `TestTheOwnershipReadIsABoundaryOnlyWhenACutoverWaitsForIt` — §UC-202,
  **added in round 2 (GAP-1)**. The one interleaving that decides the boundary,
  driven rather than reasoned about: two generations draining one log, the
  retiring pass held between its ownership read and its commit, the arriving pass
  reading the same row, and the cutover's write issued across both. Under the
  documented **locking** read the `UPDATE` waits behind every open unit — both
  passes read 1, the envelope is staged once, and the cutover returns only after
  the unit that staged it committed. Under a **plain** read the cutover commits
  between the two reads and the same envelope is staged by both. **Each arm is
  the other's control:** the same interleaving, the same specs, one clause of SQL
  apart. Driven in `psql` on PostgreSQL 17.9 first, both ways, and the fixture is
  the two recipes rather than a sleep.
- `TestABarrierDroppedFromTheSpecRestagesTheWarmUpBelowIt` — §UC-203,
  **added in round 2 (GAP-4)**. A generation warmed up under `EffectsAfter: 4`,
  interrupted at position 2, restarted from its own rows by a release that no
  longer names the barrier: it stages `A3 A4 A5`, the first two of which are
  below the barrier it was warmed up under. **Control:** the same restart with
  the field still set stages `A5` alone (§UC-171). That pair is what makes "one
  side of the comparison is a checkpoint column and the other is a constant the
  deployment holds" a measured sentence rather than a caveat.
- `TestABatchCarriesNoRouteToAnEffect` — §INV-100. A compile-time assertion that
  `Batch` has no `Effects` field, no dispatcher and no context key, plus a
  reflection walk over `Batch`'s fields.
- `TestNewRefusesEverySpecItCannotAssemble` — §INV-104, extended a third time
  with this section's combinations, asserting a spec wrong in three places
  reports three problems. It is **inside `EFF`** rather than in an arm of its
  own, because the matrix names S5 as its checkpoint and a name no arm counts is
  a name nothing pins.
- `TestAStageThatFailsIsRedeliveredOrHaltsAndIsNeverParked` — **added while
  writing S5**, because **P-3**'s three `Stage`/`Generations.Active` arms are in
  this section's *Realises* and none of the twelve above reaches them, so the
  whole failure table would have shipped with no falsifier. Three arms, told
  apart by `State.Attempt`: a transient refusal redelivers and consumes one; a
  permanent one **halts, parks nothing, and does not re-enter the isolation
  pass** — asserted as one handler delivery, which is what a missing arm costs;
  an ownership row that cannot be read postpones and consumes none. `EFF` gains
  the name and its count moves from twelve to **thirteen**.

**Checkpoint** (no database):

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s5
# … write the section …
EFF='^(TestTheLiveGenerationStagesAndTheRebuildDoesNot|TestEffectsAreRefusedBesideAfterApply|TestAStraddlingPageStagesExactlyTheEnvelopesPastTheBarrier|TestAParkedEnvelopeIsNotStagedAndIsNotLost|TestARedriveStagesWhatItAppliesAndAnEvictionStagesNothing|TestTheOwnershipRowIsNotReadWhenThereIsNothingToStage|TestABarrierBesideAParkIsRefused|TestABarrierWithNothingToGateIsRefused|TestABarrierDroppedFromTheSpecRestagesTheWarmUpBelowIt|TestASecondProjectionNameWithEffectsStagesEveryHistoricalEvent|TestAnUngeneratedProjectionWithGenerationsStopsStagingAtTheCutover|TestTheOwnershipReadIsABoundaryOnlyWhenACutoverWaitsForIt|TestABatchCarriesNoRouteToAnEffect|TestAStageThatFailsIsRedeliveredOrHaltsAndIsNeverParked|TestNewRefusesEverySpecItCannotAssemble)$'
gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 \
&& go build ./... && go vet ./event/... \
&& test "$(go test -list "$EFF" ./event/projection/ | grep -c '^Test')" = 15 \
&& go test -race -count=1 -run "$EFF" ./event/projection/ \
&& test "$(go test -list '^TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity$' ./scripts/ | grep -c '^Test')" = 1 \
&& go test -race -count=1 ./event/... ./scripts/ \
&& ./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& ./scripts/checks.sh event-kernel-moved .git/event_kernel_before_s5 \
     '^event/projection/(effect|effect_test|spec|pass|redrive|doc|spec_test|harness_test|generation)\.go$' \
     event/projection/effect.go
```

`redrive_test.go` is deliberately **not** in the allowed set, so this section's
redrive arms live in `effect_test.go` beside the loop's — which is the right home
anyway: what they pin is the gate, and the gate is one value both callers reach
for.

**`generation.go` joined the allowed set in round 2, and nothing else did.**
GAP-1's close criterion is a sentence on the `Generations` contract, and that
contract is S4's file: the obligation `Active` carries belongs on the interface
that declares it, beside `Park`'s ordering obligations, and not restated in a
file the implementor never opens. `generation_test.go` stayed out — the two
recipes are fixtures of this section's subject, so `ownedRow` lives in
`effect_test.go` and S4's `ownership` fake is untouched.

**One line of the chain is run out of order, for S4's mechanical reason.**
`go test ./scripts/` carries
`TestTheEventKernelOfThisRepositoryIsWhereThisPhaseLeftIt`, which runs
`check-event-kernel` over the *current* manifest, so the suite is red until the
baseline is re-recorded and the chain re-records it one line later. The baseline
was taken first and the suite run after it; `event-kernel-moved` compares the
**new** manifest against `.git/event_kernel_before_s5`, recorded before the first
file of this section was written, so the fence still reads the whole diff.

**Ran green, 2026-09-12 — re-run line for line after round 2's four closures.**
`gofmt` silent, `go build ./...` and `go vet ./event/...` clean, the **fifteen**
names counted and run under `-race`, the `scripts/` arm counted. The `scripts`
failure is printed rather than filtered: it is the baseline red this section
names below, it belongs to the very command this block quotes, and the `&&` chain
as written stops there — so the two `event-kernel` lines after it were run
separately and are shown as such:

```
gofmt -l .                                                          0
go build ./... ; go vet ./event/...                                clean
go test -list "$EFF" ./event/projection/ | grep -c '^Test'         15
ok  	github.com/frostgrove/vv/event/projection	1.049s   (the fifteen, -race)
go test -list '^TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity$' ./scripts/ | grep -c '^Test'   1
ok  	github.com/frostgrove/vv/event	7.096s
ok  	github.com/frostgrove/vv/event/eventmemory	1.511s
ok  	github.com/frostgrove/vv/event/eventtest	4.360s
ok  	github.com/frostgrove/vv/event/projection	1.920s
--- FAIL: TestNoI18nPackageCostsMoreThanItsErrorSeam (0.08s)
    i18n_test.go:31: the i18n extension reaches github.com/go-json-experiment/json/jsontext outside its error seam and declared MessageFormat/CLDR ecosystem
FAIL	github.com/frostgrove/vv/scripts	125.488s

# run separately, because the chain above stops at the red it discloses
event-kernel-baseline: 152 files recorded in scripts/event_kernel.sha256
check-event-kernel: ok
the files under event/ this section moved:
  event/projection/doc.go
  event/projection/effect.go
  event/projection/effect_test.go
  event/projection/generation.go
  event/projection/pass.go
  event/projection/redrive.go
  event/projection/spec.go
  event/projection/spec_test.go
event-kernel-moved: ok
```

Beside it: `make vet` clean, `make examples` green (exit 0), the structural checks
run individually — `check-deps`, `check-tiers`, `check-utils`, `check-triplets`,
`check-todo`, `check-replaces`, `check-otel-schema`, `check-workspace`,
`check-event-kernel` — all `ok`. The satellite was not merely compiled: the whole
tagged suite ran live against PostgreSQL 17.9 —
`ok github.com/frostgrove/vv/event/eventpg 106.280s`
(`-race -count=1 -tags=integration`), within run noise of S4's 107.661s. This
section is a no-database one and that run is a regression check rather than its
evidence; ES-06's live arms are S6's §6.11–§6.13.

**Re-run after round 2**, because `event/eventpg`'s tagged tests import
`event/projection` and a refusal added at a door is exactly the kind of change
that only a live wiring meets: `make vet` clean, `make examples` green (exit 0),
`check-deps`/`check-tiers`/`check-utils`/`check-triplets`/`check-todo`/`check-replaces`
all `ok`, and
`ok github.com/frostgrove/vv/event/eventpg 106.716s`
(`-race -count=1 -tags=integration`), within run noise of the 106.280s above.
GAP-1's interleaving arm was additionally run `-count=25` under `-race`, green,
because a case that passes once and fails on rerun is a real defect and this one
drives contention.

**The two reds at HEAD are the two S3 and S4 named, and neither is this
section's.** `go test ./scripts/` fails on exactly one arm,
`TestNoI18nPackageCostsMoreThanItsErrorSeam`, whose subject is `i18n/cmd/vv-i18n`;
`make check` stops at `check-tidy` over `./vvdb/dbpgx`'s `go.sum`. Both arrived
with the audit/otel/i18n merge (`fefa3e9`/`939bcd9`) — `git log -1 --
vvdb/dbpgx/go.sum` answers `87ae803` — and this section touches no file of either
subject: `git diff --stat -- '*/go.mod' '*/go.sum'` is empty and no file of
`i18n/` is in the diff.

**One red this section caused, and a shipped structural rule caught it rather
than a reviewer.** `event/refusalmessages_test.go`'s
`TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor` reported *"a Position is
rendered into a refusal"* against both new `EffectsAfter` messages, which printed
the barrier's value. A refusal names the rule that was broken and never the data
that broke it, so both now say "EffectsAfter names a barrier". The rule is
phase 1's and it reached across a file phase 1 never saw.

**One addition to this section's test list, made while writing it.**
`TestAStageThatFailsIsRedeliveredOrHaltsAndIsNeverParked` — **P-3**'s three
`Stage`/`Generations.Active` arms are in this section's *Realises* and none of the
twelve planned names reaches them, so the whole failure table would have shipped
with no falsifier. Four mutations of those arms (15–18 below) are caught by it and
by nothing else. `EFF` gained the name and its count moved to thirteen; the plan
was changed before the code.

**Falsified rather than asserted.** Seventeen mutations were driven, each applied
to the shipped tree, run, and restored; every one was caught, and the first
message each test printed is quoted:

| Mutation | Test | Result |
|---|---|---|
| suppressor 3 gated on `spec.Generation != Ungenerated`, which is what [SPEC] §1.6 implied | `TestAnUngeneratedProjectionWithGenerationsStopsStagingAtTheCutover` | **FAIL** — *"the retiring projection staged [A1 A2 A3] after the row moved"* — **P-7**, measured |
| the barrier applied per page rather than per envelope | `TestAStraddlingPageStagesExactlyTheEnvelopesPastTheBarrier` | **FAIL** — *"the sink took [[P1 P2 P3 P4 P5]] … owes an effect for exactly the envelopes past it"* |
| `Stage` called with an empty slice rather than not at all | the straddling page's below-the-barrier control | **FAIL** — *"the sink was called 1 times for a page with nothing past the barrier … not even with an empty slice"* |
| a parked envelope's effect is staged with the applied ones, **on the isolation pass** | `TestAParkedEnvelopeIsNotStagedAndIsNotLost` | **FAIL** — *"the sink took [[A1] [A2 B1]] … telling the world about it is the inversion the capability exists to prevent"* |
| the redrive applies a letter and stages nothing | `TestARedriveStagesWhatItAppliesAndAnEvictionStagesNothing` | **FAIL** — *"the sink took [], where one unit per letter is one effect per letter"* |
| the ownership row is never consulted | `TestTheLiveGenerationStagesAndTheRebuildDoesNot` | **FAIL** — *"the retiring generation staged [A1 A2 B1 A4], and it holds a sink"* |
| `Effects` beside `AfterApply` accepted | `TestEffectsAreRefusedBesideAfterApply` | **FAIL** — *"a spec staging effects outside a unit of work was accepted"* |
| a barrier with nothing to gate accepted | `TestABarrierWithNothingToGateIsRefused` | **FAIL** — *"a caller who set it got a suppression of nothing"* |
| a barrier beside a queue accepted | `TestABarrierBesideAParkIsRefused` | **FAIL** — *"what it leaves is a generation whose rows a cutover cannot compare"* |
| `Effects` at a generation with no ownership row accepted | `TestNewRefusesEverySpecItCannotAssemble` | **FAIL** — *"a spec carrying Effects at a generation with no ownership row was accepted"* |
| the ownership row read **before** the barrier is applied | `TestTheOwnershipRowIsNotReadWhenThereIsNothingToStage`, both the no-sink arm and the nothing-past-the-barrier arm | **FAIL** — *"the ownership row was read 1 times for a page with nothing past the barrier"* |
| the stage runs **before** the handler, over the matched page | `TestAParkedEnvelopeIsNotStagedAndIsNotLost` | **FAIL** — the parked envelope is staged |
| the redrive stages **outside** the letter's unit | `TestARedriveStagesWhatItAppliesAndAnEvictionStagesNothing` | **FAIL** — *"2 stages were made outside the letter's unit"* |
| **P-3**'s three arms dropped from the failure table | `TestAStageThatFailsIsRedeliveredOrHaltsAndIsNeverParked` | **FAIL** — *"the projection retried at attempt 2, where a read of the application's own table costs no attempt"* |
| a permanent stage failure falls through to the isolation pass | the same | **FAIL** — *"the page was delivered 2 times … rather than sent back through the isolation pass one envelope at a time"* |
| an unreadable ownership row halts instead of postponing | the same | **FAIL** — *"it published [draining halted]"* |
| a transient stage failure halts instead of being redelivered | the same | **FAIL** — *"it published [draining halted]"* |

The eleventh is the one worth naming: it is the *order* of the suppressors rather
than any one of them, and it is caught twice — by a projection with no sink and by
a page with nothing past the barrier — which is what makes "cheapest first" a
measured property rather than a comment. The fifteenth is caught only by the test
this section added, which is the argument for adding it.

**Four more, driven in round 2**, each applied to the shipped tree, run, restored,
and the tree verified unchanged afterwards. The first of them is the finding: it
**survived** the whole package before the arm below existed, which is what
"falsified rather than asserted" costs when only one of two appliers has an arm.

| Mutation | Test | Result |
|---|---|---|
| `unblockedPage` sets `held.owed = this.matched`, staging an effect for every envelope it **parked without calling the handler** | `TestAParkedEnvelopeIsNotStagedAndIsNotLost`, the §UC-147 arm | **SURVIVED** before round 2 (GAP-2) · **FAIL** now — *"the sink took [[A1] [B1] [A3 C1]], where the page A3 C1 owes an effect for C1 alone — A3 never reached the handler at all"* |
| the third suppressor dropped, so the ownership row is read and ignored | `TestTheOwnershipReadIsABoundaryOnlyWhenACutoverWaitsForIt`, both arms | **FAIL** — *"the arriving generation staged [A1 A2 A3] beside the retiring one, and no envelope is staged by both"* |
| `refusedBarrier` dropped from `NewRedrive` | `TestARedriveStagesWhatItAppliesAndAnEvictionStagesNothing`, the refusal arm | **FAIL** — *"a redrive carrying a barrier was accepted, and what it then does is apply a letter and stage nothing — the one outcome [SPEC] §1.6 calls the effect lost for ever"* |
| the barrier compared against the origin rather than against `EffectsAfter` | `TestABarrierDroppedFromTheSpecRestagesTheWarmUpBelowIt`, both arms | **FAIL** — *"the warm-up staged 1 times below its own barrier, so this arm starts from a generation that was never warming up"* |

**Three `[low]` findings recorded and left alone**, under `## P4` items 60, 61 and
62: the second payload clone a sink costs per page, `Effect.Attempt` being the
delivery's attempt rather than the envelope's, and `Spec.Effects` being inert
beside `AfterApply` by refusal where `Spec.Park` is inert beside `Halt` by a line
of code.

**Round 2 — four `[high]` findings, all four closed, 2026-09-12.** Two of them
were this section's own headline claims measured and found not to hold, and both
are corrections to [SPEC] rather than to the code:

- **GAP-1 — the two-sender boundary is the application's to close, and the code
  said it was closed.** Driven in `psql` on PostgreSQL 17.9 first: `T1 BEGIN;
  select active → 1` · `T2 update active=2` (committed, no wait) · `T1 insert
  staged_effect; COMMIT` → both commit, one staged effect, the row at 2, nothing
  rolled back. Then the same read `for share`: the cutover was issued at
  `:23.810` and returned at `:25.815`, **after** the staging unit committed at
  `:25.814`. So the mechanism is right and the boundary needs a sentence the
  contract did not have: the obligation is on `Generations.Active` and it is
  now stated there, `effect.go`'s *"the loser's whole unit rolls back"* is gone,
  [SPEC] §1.6 says which read closes the window and what it does **not** close
  (§1.5's overlap, which draining closes), §UC-173 and §UC-192 are restated, and
  §UC-202 measures the two recipes against each other under `-race`, twenty-five
  runs.
- **GAP-2 — the blocking applier's staging arm had no falsifier**, and it is the
  arm a degraded projection runs on every page. The mutation is in the table
  above; the arm is in the test list.
- **GAP-3 — `RedriveSpec.EffectsAfter` accepted a wiring whose only outcome is
  the permanent loss of an owed effect.** Reproduced first: a redrive carrying a
  barrier answered `{Applied: 2, Left: 0}` with `rows=[A2 A3] staged=[] calls=0`
  and a nil error on every path. `NewRedrive` now refuses it, `unstageable` keeps
  the one refusal both doors share, and the barrier-with-nothing-to-gate refusal
  moves to `refusedEffects` where it is the loop's alone.
- **GAP-4 — the barrier is a deployment-held constant and [SPEC] called both
  sides checkpoint columns.** Reproduced: a generation warmed up under
  `EffectsAfter: 4` staged nothing over positions 1–2; the same generation
  restarted with the field dropped staged `[A3 A4 A5]`, two of which are below the
  barrier it was warmed up under. Recording the barrier is refused here and the
  refusal is on the record: it is a checkpoint-row question, and §5.1 freezes the
  `event` surface for this whole phase. So the constant is **named** as one — in
  `Spec.EffectsAfter`, in `gate.past`, in [SPEC] §1.6 — `Barrier.At`'s "and with
  nothing else" is reconciled with the one operator use that is the only shipped
  source of `N`, and §UC-203 measures the failure with the field-still-set
  control beside it.

Nothing was added to the backlog by round 2: the four were all `[high]`, and the
six `[medium]`/`[low]` items it recorded (63–68) are the reviewer's, left alone.

---

### S6 — the live proof, the examples, the decisions and the gate  `[x]`   ***(LIVE DATABASE · moves the manifest)***

**Amended during S6 (one line, a fact the section met rather than a scope
change): this plan's `D-134`, `D-135` and `FL-039` were free when it was written
and are not now** — `D-134` is the OpenTelemetry module, `D-135` the i18n module
and `FL-039` the message-rendering flow, all landed since. The two decisions are
therefore written as **D-140** and **D-141** and the new flow as **FL-042**;
every "D-134"/"D-135"/"FL-039" below means those. The [[D-131]] amendment and
[[FL-038]] are unmoved.

**Delivers §6's nineteen items, twice in a row**, plus the two examples, the two
decisions, the pages, the flows, the source walks and the regenerated surface.
Green unit tests are not evidence for any of §6; rows are checked in the database
with `psql` rather than in Go.

**Appendices** all five, at their live end.

**Files** `event/eventpg/partition_integration_test.go`,
`topology_integration_test.go`, `park_integration_test.go`,
`generation_integration_test.go`, `effect_integration_test.go`,
`cost_integration_test.go` (new, all `//go:build integration`);
`event/eventpg/projection_integration_test.go`, `rebuild_integration_test.go`
(extended); `scripts/projection_test.go` (the dispatch walk and the guard
counts); `scripts/docs_test.go` and `docs/ai/decisions/D-020-*.md` (the rename
walk, added when round 1 was closed); `_examples/event-partitions/main.go`,
`_examples/event-generations/main.go`, `_examples/README.md` (new/extended);
`docs/ai/decisions/D-134-*.md`, `D-135-*.md`, `Index.md`;
`docs/ai/decisions/D-131-*.md` (the blue/green amendment recorded **beside** it);
`docs/modules/{en,ru}/projection.md`, `eventtest.md`, `eventpg.md`;
`docs/ai/flows/FL-038-*.md`, `docs/ai/flows/FL-039-an-applied-envelope-becomes-a-staged-effect.md`
(new), `docs/ai/flows/Index.md`; `docs/roadmaps/Roadmap.md`;
`docs/api/surface.md`.

**Live tests** — every one behind `//go:build integration`, in `event/eventpg`:

1. `TestTierAIsProvedAndTierBIsRefusedLive` — §6.1, §UC-131, §UC-132. Two pools,
   two transactions, one spec: the halt happens **before** the handler runs,
   asserted by an empty destination table and an unmoved checkpoint row read with
   `psql`. **Control:** one transaction for both drains.
2. `TestFourPartitionsOverOneLogAndTheModulusControl` — §6.2, §UC-135, §UC-138,
   §INV-086. The positive case asserts each key's events landed in one partition
   in order; the **control** runs a `hash % N → hash % (N+1)` re-partitioning as
   a fixture and asserts `OrderPaid` lands before `OrderCreated`, so a green
   positive case is known to be proving something.
3. `TestASplitsThreeStatementsAreOneTransaction` — §6.3, §INV-087, with a failure
   injected between the two child writes asserting **neither** child row exists.
4. `TestARunningParentHaltsWhenItsRowIsSplitAway` — §6.4, §UC-139. **Control:** a
   split of a drained parent halts nothing.
5. `TestTheBlockingTestAndTheAdvanceAreOneCommit` — §6.5, §UC-146, §UC-147,
   §INV-091. `A3` never reaches the handler, `B` keeps flowing, the park row and
   the checkpoint row moved in one commit — verified by a rollback that leaves
   neither.
6. `TestTheFastPathCostsNothingLive` — §6.6, §UC-148. `Holds` count zero over a
   full drain with an empty park, and rising after the first park.
7. `TestAFullParkBlocksAndOneDeleteClearsIt` — §6.7, §UC-150. `PhaseBlocked`, no
   advance, no skip; then one `DELETE` and the very next pass advances, with no
   restart.
8. `TestARedriveStopsAtTheRepeatFailureAndSavesNothing` — §6.8, §UC-153,
   §INV-094, with a recording `Checkpoints` asserting **zero** saves.
9. `TestTheBarrierTheCutoverAndTheRollback` — §6.9, §UC-159, §UC-161, §UC-164.
   **The arm in the middle is §UC-159's `Then` and was added when S6's round-1
   review was closed** (GAP-1): the arriving generation is held inside its second
   page — the page size is eight over a log of twenty-four so that there is a
   committed row under the barrier to hold it at — and while it stands there
   `Reached` answers `Reached: false` with `Behind` equal to the distance the rows
   record, and a `Cutover` onto it answers `ErrTopology` and leaves the ownership
   row unmoved. Without it the whole tagged suite is green with
   `Readiness{Reached: true, …}` hardwired in `event/projection/generation.go`,
   because every other live arm *waits* for `Reached` and a constant true only
   shortens the wait. Beside it,
   `TestTheCutoverSwitchesEveryTableAtOnceForAReaderInOneSnapshot`
   with a reader held open across the commit, plus **the negative arm**: a reader
   that resolved the row *before* the commit keeps reading generation 1, and that
   is **correct** until it is retired. Beside them,
   `TestAGenerationDrainsBesideTheLiveOneAndTheRowsAgree` — §UC-158, ES-04's
   headline outcome and the one nothing else in this plan runs: UC-120's shipped
   assertion (`event/eventpg/rebuild_integration_test.go`) re-run under the new
   naming, `orders` and `orders@2` over one log into two destinations, both
   draining, the rows compared at the end and their checkpoint rows read
   separately in `psql`. **Control:** the arriving generation stopped mid-drain
   leaves the rows disagreeing, so the agreement is the drain's.
10. `TestTwoOperatorsCuttingOverAtOnce` — §6.10, §UC-162, driven with a gate on
    the shape `TestTwoLiveInstancesOfOneNameOverOneSchema` already uses.
11. `TestARetiredGenerationStopsStagingAtTheCutover` — §6.11, §UC-173, with the
    **no-ownership-row fixture** asserting both *would* stage. This is the window
    Marten calls "a separate concern". **The cutover is committed while a
    retiring pass is open between its ownership read and its commit, and not
    between two passes** (round 2, GAP-1): the between-passes version passes by
    scheduling luck and would be read afterwards as evidence the window is
    closed. So the live case runs **both recipes** — §UC-202 — a `Generations`
    whose `Active` is `SELECT active … FOR SHARE`, asserting one staged row per
    envelope across both generations and that the cutover's `UPDATE` waited; and
    the same fixture without the clause, asserting two. Rows counted in `psql`.
    Beside it, `TestAnOwnershipRowOverASecondPoolLeavesTwoSenders` — §UC-192, a
    third way to lose the same boundary, asserted rather than claimed.
12. `TestAnInterruptedWarmUpResumesSuppressed` — §6.12, §UC-171. Killed at `K`
    between `M` and `N`, restarted, and the effect sink holds nothing from
    `(K, N]`. **Control:** the same generation started fresh agrees on the
    boundary. **And the arm that says what this proves and what it does not**
    (round 2, GAP-4, §UC-203): the same restart with `EffectsAfter` dropped
    stages all of `(K, N]`, because the barrier's side of the comparison is a
    constant the deployment holds. Without it §6.12 is read as proof of a
    durability the barrier does not have.
13. `TestAStraddlingPageStagesExactlyThePastBarrierEnvelopesLive` — §6.13.
14. `TestEightWalksCostEightTimesOneProjectionsReads` — §6.14, §UC-168. Eight
    walks measured against a one-projection baseline, **recorded as a number**
    and written to the backlog under `## P4` so Reject 3's eventual decision has
    a measurement rather than an intuition ([SPEC] §8.5).
15. `TestASplitLeavesTheParkedLettersReachable` — §6.15, §UC-179, §INV-090. The
    letters stay under `orders@2`, the matching child blocks the sequence's later
    events, and `A2` is not applied by a redrive **after** `A4..A6`. **Control:**
    the same split with an empty park leaves both children paying nothing.
16. `TestASplitWithNoParentRowWritesNothing` — §6.16, §UC-140, with the rows read
    in `psql` to prove nothing was written, against the has-run control.
17. `TestTwoOperatorsRedrivingAtOnce` — §6.17, §UC-191, gated so the contention is
    caused: one claim wins, no letter is applied twice, and the sequence's order
    holds — read off the **destination rows** rather than off the Go values.
18. `TestAStageRolledBackByALostFence` — §6.18, §UC-183. Two instances of one
    identity; the loser's staged job row absent and its advance unmoved after
    `ErrOvertaken`; the winner's present exactly once. **Control:** the same stage
    on a committing pass leaves both. Beside it,
    `TestAStagedJobTheReadModelAndTheAdvanceCommitTogether` — §UC-178.
19. `TestACutoverCannotBeHandedABarrier` — §6.19, §UC-180, §UC-163. A cover with
    a member that has no row is refused; a generation that parked and redrove
    completely cuts over with no override; one holding a live letter does not.

Plus `TestAForeignDestinationGetsFourPromisesAndNotTheFifth` (§UC-134),
`TestARetiringGenerationCannotBeToldToWriteIntoTheArrivingOne` (§UC-160, with the
ignores-its-batch control), `TestACancelledRebuildResumesFromItsRowAndNeverFromTheOrigin`
(§UC-165), and the `eventtest` `topology` / `topology handoff` sections certified
for `eventpg` through the existing `TestTheCheckpointStoreSatisfiesTheContract`.

**Source walks** — untagged, in `scripts/`:

- `TestNoPackageOnTheProjectionPathCanDispatch` — §UC-177, §INV-102. A
  `go/types` import walk over `event/projection` and everything it reaches,
  asserting no package imports `net`, `net/http`, `net/smtp` or `os/exec`
  transitively. **Control:** a fixture package importing `net/http` **is**
  reported by the same walk, so a walk that resolved nothing is not read as a
  clean tree — the shape `checkedFixture` already uses.
- `TestNoModulusIsAppliedToASequenceHash` — §INV-086. An AST walk for `%` applied
  to the result of the package's `hash`, with a fixture control.
- `TestNoExportedFunctionOrdersOrTakesTwoCursors` — §UC-142, §INV-088, and it is
  **new**: it did not exist when this plan's matrix first named it. A merge is
  the one topology change [[D-128]] and [[D-129]] jointly forbid, and its
  signature is what it cannot hide — two partitions become one, so the function
  that performs it takes both children's cursors and has to decide which the
  survivor resumes from. There is no such decision. So: over the same
  `checkedEventPackages` list, no exported signature's parameter tuple mentions
  `event.Cursor` twice. The existing `TestNoExportedFunctionTakesAPositionAndAnswersACursor`
  asks a different question and would not see one. **Control:** a
  `checkedFixture` package declaring
  `func Merge(held, taken event.Cursor) event.Cursor` **is** reported by the same
  walk — the shape `TestCursorIsNeverCompared` already uses — so a walk that
  resolved nothing is not read as a clean surface. The ordering half of §INV-088
  stays `TestCursorIsNeverCompared`'s, which is why that name is in this
  checkpoint's counted pattern too.
- `TestNothingInTheProjectionPackageOpensATransaction` — §INV-103's transaction
  clause. Written in **S1**, before `Split`, `Cutover` and `Redrive` exist;
  re-counted here because S6 is the phase's gate and by then all three do.
- The three existing walks extended over the new surface, with their guard counts
  raised: `TestNoExportedFunctionTakesAPositionAndAnswersACursor`,
  `TestNoConstructorTakesAProgressAndAnswersACursor`, `TestCursorIsNeverCompared`
  (whose fixture control stays), `TestNoSnapshotAuthorityIsDeclaredOrPromised`,
  and `TestNoCommentInTheProjectionPackagePromisesExactlyOnce` with `files < 18`.

**Documentation, in this same change:**

- **D-134 — a partition is a mask, and a topology change is a handoff.** The mask
  arithmetic; why `hash % N` is corrupting rather than inconvenient; the
  five-step handoff and why steps 2 and 4 are not redundant; the absent-row halt
  as vv's replacement for `validateSegment`; the absent-parent **refusal** and
  why guessing "fresh" is [[D-133]]'s halt case; the refusal of `Merge` with
  **both** reasons ([[D-129]] and the `eventpg` cursor's read-time `bound`, which
  makes two walks to one position mint different bytes); `Cover` and what it does
  **not** cover; and the N× read cost with Reject 3 as its ceiling. Links
  [[D-092]] [[D-125]] [[D-128]] [[D-129]] [[D-133]].
- **D-135 — an effect is a separate capability, gated on a durable ownership
  row.** Why the capability is a value rather than a mode; why Marten's default
  is the safe one and Axon's is not; the `AfterApply` refusal with Reject 2's
  window as its reason; `Stage` and not `Dispatch`, with the four rollback paths;
  the per-envelope barrier and why per-page is wrong; an effect following its
  envelope into the park; the ownership read inside the committing transaction as
  the two-sender boundary **neither source has**, carrying its precondition in
  the same breath — **and that precondition is two sentences, not one**: the row
  is read through the ambient transaction, *and* the implementation's `Active`
  takes a lock the cutover waits on, because [[D-126]] leaves the isolation level
  to the caller and a plain read at `READ COMMITTED` closes nothing (§UC-202).
  The barrier's paragraph says in the same breath that one side of its comparison
  is a checkpoint column and the other a deployment-held constant, and where an
  operator gets the number (§UC-203). And — as one section, not a footnote —
  **what the boundary does not cover**, which is four things and not one: a
  rebuild spelled as a second `Spec.Name` (§UC-184), a `Generations` reached over
  a second pool (§UC-192), a `Generations` whose `Active` does not lock
  (§UC-202), and **a live projection carrying `Effects` with no `Generations` at
  all**. Plus the overlap the ownership row never addressed: a retiring
  generation still advancing past the observed barrier stages envelopes the
  arriving one has not reached, which is §1.5's window and is closed by draining
  (§UC-201). The third is **P-7**'s: the suppressor is gated on the capability being
  supplied, so a projection that never supplied one has nothing to gate, and its
  first `Cutover(From: Ungenerated, …)` runs two senders until the retiring
  projection is stopped. The remedy is a deployment ordering — add `Generations`
  to the live `Ungenerated` spec one release *before* the cutover, which stages
  exactly as before because the row answers `Ungenerated` — and it is the same
  blue/green shape [[D-131]]'s amendment records for `Ignore`. §UC-193 is the
  measurement: the arm asserts the retirement, the control asserts the limit.
  Then the contract-not-sandbox sentence with the test that backs it. Links
  [[D-092]] [[D-118]] [[D-126]] [[D-130]] [[D-131]] [[D-133]].
- **Beside [[D-131]], not inside it:** the blue/green deployment ordering —
  `Ignore` in the *previous* release — which is a use of the rule and not a
  change to it.
- `docs/modules/{en,ru}/projection.md` — six contract rows become nine; the three
  renames written as renames with their reasons — a `Was | Is | Why` table on
  both guides, four rows because `Spec.Quarantine → Spec.Park` is one of them,
  each carrying §5.2's reason, and the sentence that `Progress.Quarantined` keeps
  its name and its meaning (delivered when round 1's GAP-2 was closed; until then
  the pages carried only the new names); the rebuild recipe changed to
  `Spec.Generation` with what the second-`Spec.Name` spelling does **not** get;
  the `Cover` sentence beside `Split`; the `Unchecked` section gaining "a foreign
  destination gets `Halt`"; the `AfterApply` outbox asymmetry Reject 2 owes; "an
  HTTP call inside the unit is not protected by the fence"; "a skipped event's
  effect is never sent"; `Reached` is "delivered", not "the rows agree" (§8.8);
  and §8.1's, §8.2's and §8.6's sentences. Plus this phase's two obligations that
  are an operator's rather than the framework's, each with what goes wrong when
  it is not met: **the release ordering for the effect gate** — add `Generations`
  to a live `Ungenerated` spec one release *before* the first cutover, or that
  cutover runs two senders until the retiring projection is stopped (**P-7**,
  measured by §UC-193's control) — and **the claim duration**, which must exceed
  the longest unit a redrive may take, because a shorter one turns every redrive
  into a run of lost claims that drains nothing, and `ErrClaimLost` is what an
  operator sees when it is (**P-8**). **Added by the S4 implementation review —
  two more, both operators' and both already in `Cutover`'s own contract:** (1)
  **the covers a cutover declares must be the ones those generations record at**,
  because a cover no member of which holds a row is `ErrRetired` and one whose
  members a `Split` retired is `ErrTopology` — the four-partition generation
  declared as `Whole()` is the case to print (§UC-200); and (2) **the read-target
  overlap window**, in Marten's own shape — drain or stop the retiring generation
  before, or as, the switch commits, `Observe` it twice to see whether the barrier
  moved, drop `Spec.Pace` on the arriving generation first, and what it costs when
  it is not met is reads moving backwards by the retiring generation's advance
  over the life of the cutover's transaction (§UC-201). The blue/green section is
  where the second belongs, beside the effect half it already carries.
  **Added by the S5 implementation review — a third, and it is the one the
  two-sender paragraph cannot be written without** (round 2, GAP-1): the
  application's `Generations.Active` must be a **locking** read — `SELECT active
  … FOR SHARE` — or run in a `SERIALIZABLE` unit. What goes wrong when it is not
  met is a retiring pass committing a staged effect under a row that already
  names the arriving generation, at every isolation level this repository names,
  with both units committing and neither rolling back (§UC-202). The page states
  it in the same breath as the boundary, or it states a guarantee the recipe
  beside it does not deliver. **And a fourth, about the barrier** (round 2,
  GAP-4): `Spec.EffectsAfter` is a deployment-held constant, the operator gets
  the number from `Observe`'s `Barrier.At`, and a release that lowers or drops it
  re-stages the warm-up below it with nothing refusing it (§UC-203).
- `docs/modules/{en,ru}/eventtest.md` — the two topology sections' three store
  obligations, and that a phase-3-certified store may go red on the third.
- `docs/modules/{en,ru}/eventpg.md` — the N× walk cost and the multiplied
  `xmin`-stall exposure, beside the stall section phase 3 wrote.
- `docs/ai/flows/FL-038` gains the split, the park and the cutover;
  **FL-039** is new for the effect gate; the reverse index in
  `docs/ai/flows/Index.md` gains every file this phase touches.
- `docs/ai/usecases/` — [[UC-032]] is **not** widened. [APX] says so explicitly.
- `docs/roadmaps/Roadmap.md` — ES-01, ES-02, ES-03, ES-04 and ES-06 **removed**
  from the open list rather than annotated done; ES-05, ES-07, ES-08, ES-09 stay.
  **Amended in S6 (one line, a fact the section met):** the open list those five
  are *on* is not `Roadmap.md` — it is the "Дополнительные приложения — 2026-09-08"
  section of `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md`, whose
  `**Статус: не выполнено.**` markers are what makes them open. The five
  appendices are removed there and `Roadmap.md`'s projector paragraph, which
  describes what the projector delivers, is updated to name what now ships.
- `_examples/README.md` — two rows; both examples use the
  `for _, part := range cover.Partitions()` spelling and nothing else (§8.7).

**Checkpoint** (**live database**, run twice in a row):

```sh
./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s6
# … write the section …
export FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable'
gofmt -l . | tee /dev/stderr | wc -l | grep -qx 0 \
&& go build ./... && go vet ./... \
&& go test -tags=integration -list '.' ./event/eventpg/ | grep -q '^Test' \
&& test "$(go test -tags=integration -list '^(TestTierAIsProvedAndTierBIsRefusedLive|TestFourPartitionsOverOneLogAndTheModulusControl|TestASplitsThreeStatementsAreOneTransaction|TestARunningParentHaltsWhenItsRowIsSplitAway|TestTheBlockingTestAndTheAdvanceAreOneCommit|TestTheFastPathCostsNothingLive|TestAFullParkBlocksAndOneDeleteClearsIt|TestARedriveStopsAtTheRepeatFailureAndSavesNothing|TestTheBarrierTheCutoverAndTheRollback|TestAGenerationDrainsBesideTheLiveOneAndTheRowsAgree|TestTheCutoverSwitchesEveryTableAtOnceForAReaderInOneSnapshot|TestTwoOperatorsCuttingOverAtOnce|TestARetiredGenerationStopsStagingAtTheCutover|TestAnOwnershipRowOverASecondPoolLeavesTwoSenders|TestAnInterruptedWarmUpResumesSuppressed|TestAStraddlingPageStagesExactlyThePastBarrierEnvelopesLive|TestEightWalksCostEightTimesOneProjectionsReads|TestASplitLeavesTheParkedLettersReachable|TestASplitWithNoParentRowWritesNothing|TestTwoOperatorsRedrivingAtOnce|TestAStageRolledBackByALostFence|TestAStagedJobTheReadModelAndTheAdvanceCommitTogether|TestACutoverCannotBeHandedABarrier|TestAForeignDestinationGetsFourPromisesAndNotTheFifth|TestARetiringGenerationCannotBeToldToWriteIntoTheArrivingOne|TestACancelledRebuildResumesFromItsRowAndNeverFromTheOrigin)$' ./event/eventpg/ | grep -c '^Test')" = 26 \
&& go test -race -count=1 -tags=integration ./event/eventpg/... \
&& go test -race -count=1 -tags=integration ./event/eventpg/... \
&& test "$(go test -list '^(TestNoPackageOnTheProjectionPathCanDispatch|TestNoModulusIsAppliedToASequenceHash|TestNoExportedFunctionOrdersOrTakesTwoCursors|TestCursorIsNeverCompared|TestNothingInTheProjectionPackageOpensATransaction)$' ./scripts/ | grep -c '^Test')" = 5 \
&& go test -race -count=1 -run '^(TestNoPackageOnTheProjectionPathCanDispatch|TestNoModulusIsAppliedToASequenceHash|TestNoExportedFunctionOrdersOrTakesTwoCursors|TestCursorIsNeverCompared|TestNothingInTheProjectionPackageOpensATransaction)$' ./scripts/ \
&& go test -race -count=1 ./... \
&& make examples \
&& make api && git diff --stat -- docs/api/surface.md \
&& ./scripts/checks.sh event-kernel-baseline \
&& ./scripts/checks.sh event-kernel \
&& diff -u .git/event_kernel_before_s6 scripts/event_kernel.sha256 \
&& make check && make vet && make tidy && git diff --check
```

**The manifest arm is a `diff` and not `event-kernel-moved`, and that is the
honest statement rather than the fence relaxed.** `event_kernel_moved` fails on
an empty moved set by design (`scripts/checks.sh:434-437`: *"a section that moved
no file under event/ did not deliver"*), and the manifest excludes
`event/eventpg/*` (`checks.sh:335`). S6's whole file list is `event/eventpg`
tests, `scripts/`, `_examples/` and `docs/` — **it moves nothing under `event/`
that the manifest sees**, and the predecessor is recorded at the start of S6, so
S1's `errors.go`, S3's and S5's `doc.go` do not count. Given an allowed ERE and
no required paths, the chain is `&&`-joined and the whole live section reports
failure for a bookkeeping reason; the repair an implementer reaches for —
touching `event/projection/doc.go` so the arm has something to see — makes the
fence measure nothing. So S6 asserts the opposite of what the other five assert:
**the manifest is byte-identical to its predecessor.** `event-kernel-moved`
cannot make that statement, and a `diff` can.

**And one arm that is the whole of §5.1.** The `event` section of the surface is
recorded **once, before S1 writes its first file** — beside that section's
manifest predecessor, and by the same argument: a predecessor that survives a
commit says the same thing before `git add` and after it.

Before S1:

```sh
awk '/^## github.com\/frostgrove\/vv\/event$/,/^## github.com\/frostgrove\/vv\/event\//' \
    docs/api/surface.md > .git/event_surface_before_p4
test -s .git/event_surface_before_p4
```

In S6, after `make api`:

```sh
awk '/^## github.com\/frostgrove\/vv\/event$/,/^## github.com\/frostgrove\/vv\/event\//' \
    docs/api/surface.md > /tmp/event_surface_after_p4
diff -u .git/event_surface_before_p4 /tmp/event_surface_after_p4   # must be empty
```

The range runs from `## …/vv/event` to `## …/vv/event/eventmemory`, which is the
next section in the generated file. **A diff there is not a question for a
person; it is a section that failed**, because §5.1's whole claim is that the
kernel's exported surface does not move. The rest of `docs/api/surface.md` moves
by everything in §5.2, and *that* diff is the question for a person it always
is.

**Run, 2026-09-12, and re-run in full after round 1's two `[high]`s were closed.**
Everything below is what the commands printed on the re-run, reds included. The
two reds are the two the first run had, and both are the pristine tree's.

```
$ ./scripts/checks.sh event-kernel-baseline && cp scripts/event_kernel.sha256 .git/event_kernel_before_s6
event-kernel-baseline: 152 files recorded in scripts/event_kernel.sha256

$ export FROSTGROVE_EVENTPG_TEST_DSN='postgres://vv:vv@localhost:55432/vv?sslmode=disable'
$ export FROSTGROVE_EVENTPG_TEST_PSQL='docker compose exec -T postgres psql -U vv -d vv'
$ gofmt -l . | wc -l
0
$ go build ./... && go vet ./...          # both silent

$ go test -tags=integration -list '.' ./event/eventpg/ | grep -c '^Test'
(lists; not empty)
$ go test -tags=integration -list '^(TestTierAIsProvedAndTierBIsRefusedLive|…|TestACancelledRebuildResumesFromItsRowAndNeverFromTheOrigin)$' ./event/eventpg/ | grep -c '^Test'
26

$ go test -race -count=1 -tags=integration ./event/eventpg/...   # RUN 1
ok  	github.com/frostgrove/vv/event/eventpg	115.700s
$ go test -race -count=1 -tags=integration ./event/eventpg/...   # RUN 2
ok  	github.com/frostgrove/vv/event/eventpg	118.140s

$ go test -list '^(TestNoPackageOnTheProjectionPathCanDispatch|TestNoModulusIsAppliedToASequenceHash|TestNoExportedFunctionOrdersOrTakesTwoCursors|TestCursorIsNeverCompared|TestNothingInTheProjectionPackageOpensATransaction)$' ./scripts/ | grep -c '^Test'
5
$ go test -race -count=1 -run '^(…the same five…)$' ./scripts/
ok  	github.com/frostgrove/vv/scripts	10.153s

$ go test -race -count=1 -run '^(TestEveryNameTheProjectionPackageRenamedIsOnBothGuidesAsARename|TestAPageThatDocumentsOnlyTheNewNamesIsReported)$' ./scripts/
ok  	github.com/frostgrove/vv/scripts	1.057s

$ go vet ./event/...                      # silent
$ go test -race -count=1 ./event/...
ok  	github.com/frostgrove/vv/event	6.684s
ok  	github.com/frostgrove/vv/event/eventmemory	1.501s
ok  	github.com/frostgrove/vv/event/eventtest	4.328s
ok  	github.com/frostgrove/vv/event/projection	1.920s

$ go test -race -count=1 ./...
--- FAIL: TestNoI18nPackageCostsMoreThanItsErrorSeam (0.09s)
    i18n_test.go:31: the i18n extension reaches github.com/go-json-experiment/json/jsontext outside its error seam and declared MessageFormat/CLDR ecosystem
FAIL
FAIL	github.com/frostgrove/vv/scripts	147.046s
(every other package ok)

$ make examples
… ?   	github.com/frostgrove/vv/_examples/event-generations	[no test files]
… ?   	github.com/frostgrove/vv/_examples/event-partitions	[no test files]
(and every other example builds)

$ make api && git diff --stat -- docs/api/surface.md
api: docs/api/surface.md regenerated — read the diff
 docs/api/surface.md | 17146 ++++++++++++++++++++++++++++++++++++++++----------
 1 file changed, 13908 insertions(+), 3238 deletions(-)

$ ./scripts/checks.sh event-kernel-baseline
event-kernel-baseline: 152 files recorded in scripts/event_kernel.sha256
$ ./scripts/checks.sh event-kernel
check-event-kernel: ok
$ diff -u .git/event_kernel_before_s6 scripts/event_kernel.sha256
(empty)

$ make vet
(every module, silent)
$ git diff --check
(empty)

$ make check
check-deps: ok
check-tiers: ok
check-utils: ok
check-triplets: ok
check-todo: ok
check-replaces: ok
./app/appfx is not tidy — run make tidy
… 29 modules, ending ./vvdb/dbpgx …
make: *** [Makefile:18: check] Error 1
```

**`make check` is red on `check-tidy`, for 29 modules, and it is not this
section's.** `git stash push --include-untracked && ./scripts/vv check-tidy`
prints the **identical 29-line set** on the pristine tree, and
`diff -u` of the two lists is empty. Every other arm of `make check` is green,
including `check-event-kernel`. The condition is a `go.sum` shape — the missing
`/go.mod` hash lines — across every satellite module, and the repair is a
repo-wide `make tidy` that would put 29 unrelated `go.sum` files into this
section's diff. It is reported rather than swept in, and rather than reported as
green.

**The `event` surface arm needs its own paragraph, and it is the honest statement
rather than the fence relaxed.** The literal command

```sh
diff -u .git/event_surface_before_p4 /tmp/event_surface_after_p4
```

is **not** empty — it is 232 lines against 59. The reason is not the surface: it
is `scripts/api-surface`, which was rewritten on **2026-09-10**, after the
predecessor was recorded on 2026-09-09 and before this section ran. The old
generator rendered `type Aggregate[S any, ID any] struct{ ... }` and the new one
expands every field and method, so the predecessor and the current file are two
renderings of one surface and the diff measures the renderer.

So the claim is settled two ways that the renderer cannot reach, and **both are
empty**:

```sh
# 1. the same section, regenerated from the pristine tree by the SAME generator
$ git stash push --include-untracked && make api && awk '…' docs/api/surface.md > /tmp/event_surface_pristine
$ git stash pop && diff -u /tmp/event_surface_pristine /tmp/event_surface_after_p4
(empty — 232 lines against 232)

# 2. the manifest, which is renderer-independent: every event/ file outside
#    event/projection and event/eventtest, by sha256, before S1 and now
$ grep -v "event/projection/\|event/eventtest/" .git/event_kernel_before_s1 | sort > /tmp/a
$ grep -v "event/projection/\|event/eventtest/" scripts/event_kernel.sha256 | sort > /tmp/b
$ diff -u /tmp/a /tmp/b
(empty — 82 files each)
```

A surface cannot move while every file that declares it is byte-identical, so
§5.1 holds. **The predecessor file is left as it was recorded rather than
re-minted under the new generator**, because re-recording it would destroy the
one thing it is for.

**Mutation evidence.** Seven, each restored, each caught by a named arm. The
seventh is the review's own survivor, and it is in this table because it is now
caught rather than because it always was:

| Broken | Caught by |
|---|---|
| `Partition.Matches` made to match everything | `TestFourPartitionsOverOneLogAndTheModulusControl` — *"the four partitions between them applied the whole log did not happen"* |
| the ownership suppressor removed from `gate.stage` | `TestARetiredGenerationStopsStagingAtTheCutover/the locking read` — *"`locking-live` was staged 2 times where the locking read leaves it staged exactly once"* |
| `gate.past` made per page rather than per envelope | `TestAStraddlingPageStagesExactlyThePastBarrierEnvelopesLive` (*"staged [straddle-0 … straddle-4] where exactly the envelopes past the barrier are [straddle-3 straddle-4]"*) **and** `TestAnInterruptedWarmUpResumesSuppressed` |
| the blocking `Park.Holds` call replaced by `false` | `TestTheBlockingTestAndTheAdvanceAreOneCommit` **and** `TestASplitLeavesTheParkedLettersReachable` — *"no child asked the queue whether a sequence was parked"* |
| `import "net/http"` planted in `event/projection/doc.go` | `TestNoPackageOnTheProjectionPathCanDispatch` — both `net/http` and the `net` it reaches, with the path it was reached through |
| `Matches` rewritten as `hash(sequence)%(this.mask+1)` | `TestNoModulusIsAppliedToASequenceHash` — *"partition.go:90:27 takes a modulus of hash"* |
| `generation.go:273` hardwired to `Readiness{Reached: true, Quarantined: held.quarantined}` | `TestTheBarrierTheCutoverAndTheRollback` — *"generation 2 stands at 8 under the barrier at 24 and its readiness answers {Reached:true Behind:0 Quarantined:0 Holes:0}"*, and the cutover arm alone with the readiness one disarmed — *"the cutover from generation 1 to generation 2, which stands at 8 under the barrier at 24, answered &lt;nil&gt;"*. The whole tagged suite is **red** under it (`FAIL … 114.098s`); before the arm was written it was `ok … 56.008s` |

After the last restore, `./scripts/checks.sh event-kernel-baseline` still diffs
empty against `.git/event_kernel_before_s6`, which is what says the tree came
back.

---

## The deliverable checklist

- [x] `event/` outside `event/projection` and `event/eventtest` is byte-identical,
      proved by S1–S5's `event-kernel-moved` allowed sets and by S6's `diff`
      against its own predecessor, which is the same claim for a section that
      moves nothing under `event/`. **And by the 82-file sha256 comparison
      against `.git/event_kernel_before_s1`, which is empty.**
- [x] `docs/api/surface.md`'s `event` section is byte-identical — proved by
      regenerating it from the pristine tree with the SAME generator, because
      `scripts/api-surface` was rewritten on 2026-09-10 and the recorded
      predecessor is in the old rendering. Both settlements are in the transcript
      above and both are empty.
- [x] The checkpoint suite runs **fourteen** sections. `eventpg.Checkpoints`
      certifies fourteen; `eventmemory.Checkpoints` certifies **thirteen** and
      declines `durability`, because it does not claim persistence and this phase
      does not make it claim any. `checkpointDefects` is 8 and every checkpoint
      section is named by one, held by
      `TestEveryCheckpointSectionIsNamedByADefectThatBreaksIt`; `inventoried` is
      still 29.
- [x] `event/eventpg/census_integration_test.go`'s `checkpointCensus()` names all
      fourteen sections, and the live run certifies against it.
- [~] `make unit` green throughout; `make vet` clean; `gofmt -l` silent;
      `make check` green on `check-deps`, `check-tiers`, `check-utils`,
      `check-triplets`, `check-todo`, `check-replaces` and
      `check-event-kernel` — and **red on `check-tidy`, for 29 modules, with the
      identical set red on the pristine tree**. `go test -race ./...` is green
      everywhere but `scripts/TestNoI18nPackageCostsMoreThanItsErrorSeam`, which
      is also red on the pristine tree. Both are reported rather than repaired
      here: one is a repo-wide `go.sum` shape and the other is the i18n
      extension's dependency seam, and neither is `event/`'s.
- [x] The live suite ran twice in a row with the DSN set, and did not skip — 115.951s and 116.064s, both `ok`.
- [x] `make examples` builds both new examples, and both run: `event-partitions` prints the four rows, the split and the refusal of a second one; `event-generations` prints the barrier, the switch, the refusal on the fence, the retired generation staging nothing and the rollback.
- [x] Every test name cited in `docs/` exists (`scripts/docs_test.go`), and every
      symbol a doc names is declared where the doc says it is — the renames are
      what makes this a real risk.
- [x] The four names this phase removed are on **both** projection guides as
      renames, beside the one that did not change, and a walk holds it:
      `TestEveryNameTheProjectionPackageRenamedIsOnBothGuidesAsARename` with
      `TestAPageThatDocumentsOnlyTheNewNamesIsReported` as its control. That
      check reads the old names out of the package to confirm they are gone, so
      it cannot pass on a table documenting a rename nobody made.
- [x] D-140, D-141 (the numbers D-134/D-135 became — see the S6 amendment) and the [[D-131]] amendment are written, indexed in
      `docs/ai/decisions/Index.md`, and linked from the pages that cite them.
- [x] The open list no longer carries ES-01…ES-04 and ES-06: the five appendices are removed from `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` and `Roadmap.md` names what now ships. ES-05, ES-07, ES-08 and ES-09 stay.
- [x] Every `[critical]`/`[high]` raised by a section's review is closed and
      re-audited **before** the next section starts; every `[medium]`/`[low]` is
      appended to `EVENTSOURCE_BACKLOG.md` under `## P4` and left alone.

**Round 1 of S6's review raised two `[high]`s and both are closed.**

- **GAP-1 — the whole tagged suite was green with ES-04's barrier gate hardwired
  open.** Reproduced first: with `Readiness{Reached: true, …}` in
  `event/projection/generation.go:273`,
  `go test -race -count=1 -tags=integration -run '^TestTheBarrierTheCutoverAndTheRollback$'`
  printed `ok … 1.138s`. The shape was the cause — every live arm *waits* for
  `Reached`, so a constant true only shortens the wait, and nothing anywhere
  asserted `Reached == false`, `Behind`, or a cutover onto a behind-but-recorded
  generation. Closed by holding the arriving generation inside its second page
  (`heldPage`, and a page size of eight over a log of twenty-four so that a
  committed row stands under the barrier) and asserting both halves of §UC-159's
  `Then` off the rows: `Reached: false` with `Behind` equal to
  `barrier.At - row.highest`, and `ErrTopology` from the `Cutover`, with the
  ownership row unmoved. Both halves catch the mutation independently — the
  second was checked with the first disarmed — and the whole tagged suite is now
  red under it.
- **GAP-2 — [SPEC] §7.3's renames were on neither `projection.md`.** Reproduced:
  with the new section stripped, `grep -c` for the old vocabulary over both pages
  answers `0` and `0`, while `Progress.Quarantined` — the one name that did not
  change — appeared four times. Closed with a `Was | Is | Why` table on both
  guides covering `Quarantines → Park`, `Quarantined → Letter`, `Failure`'s
  `Quarantine → ParkSequence` and `Spec.Quarantine → Spec.Park`, each carrying
  §5.2's reason, plus the sentence that `Progress.Quarantined` keeps its name and
  its meaning and that finding it there is not evidence the other four are still
  present. Held by `TestEveryNameTheProjectionPackageRenamedIsOnBothGuidesAsARename`
  in `scripts/docs_test.go`, which reads the four old names out of
  `event/projection` to confirm they are gone and the four new ones to confirm it
  walked the right package — with `TestAPageThatDocumentsOnlyTheNewNamesIsReported`
  as its control — and is red on all eight rows against the pre-fix pages.

---

## Debt

Recorded, scheduled, and **not** fixed in this phase. Sixteen `[medium]`/`[low]`
findings from the round-1 spec audit already sit in
[`EVENTSOURCE_BACKLOG.md`](../gaps/EVENTSOURCE_BACKLOG.md) `## P4`, plus two
`[medium]` entries carried from `## From the reference`. The ones this plan
touches the edge of, and does not close:

| Backlog `## P4` | What stays open | Why this phase does not close it |
|---|---|---|
| 1. `Readiness.Behind` is a `Position` used as a distance `[medium]` | the type invites `barrier − highest` to be read as a count of undelivered events, which a sequence that burns values does not give | the field is [SPEC] §5.2's, and changing it is surface |
| 2. `Observe` takes an `Identity` that can carry a `Partition` `[medium]` | the redundancy stays | **P-4** refuses the wrong value at run time; removing the possibility is a signature change |
| 3. `Batch`/`State` carry both `Projection` and `Identity` `[low]` | the redundancy stays | [SPEC] §5.2 froze both, and dropping `Projection` is a breaking change beyond this phase's three renames |
| 4. the applied-position refusal rests on one clause that is wrong `[medium]` | the argument in §1.4 | the *refusal* is right; the argument for it is what is weak |
| 5. `RedriveSpec.Park` is a `Redriver` while `Spec.Park` is a `Park` `[low]` | the asymmetric field name | [SPEC] §5.2 |
| 6. a split under a running parent copies a stale cursor `[medium]` | the re-delivery window is unstated | §UC-139 asserts the halt, which is the safe half; the window's size is a documentation question |
| 7. `Spec.Pace` precedence `[medium]` | the module page's wording | **P-5** fixes the code; the page's phrasing is the open half |
| 8. §UC-168 is a measurement, not a falsifiable use case `[low]` | it stays a measurement | it is deliberately a recorded number ([SPEC] §8.5) |
| 9. `Partition.Count()` publishes the forbidden arithmetic `[medium]` | the method stays | [SPEC] §5.2, and `TestNoModulusIsAppliedToASequenceHash` is what stops it being used |
| 10. `Whole()` the constructor beside `(Partition).Whole()` `[low]` | both names stay | [SPEC] §5.2 |
| 11. `Progress.Quarantined` now counts envelopes never offered `[medium]` | the counter's meaning widens | it is the kernel field and §5.1 keeps it frozen |
| 12. `Split` the function beside `Partition.Split` the method `[low]` | both stay | [SPEC] §5.2 |
| 13. §INV-084's "no test anywhere asserts the fifth" `[low]` | an unfalsifiable clause | a doc check covers the reachable half |
| 14. `Effect.Attempt` has no stated lifecycle `[medium]` | it mirrors `Batch.Attempt` | stated on the type, not otherwise resolved |
| 15. `Redriver.Sequence` returns an unbounded `[]Letter` `[medium]` | the bound is the `Park` implementer's | `MaxSequenceLetters` bounds it in practice and nothing in the contract says so |
| 16. `Sequencer.Name()` has no validity or uniqueness rule `[low]` | it stays free text | §8.1's decision: it is a contract, checked at the park |

**New to the backlog in this phase**, appended by S6 as entries 69, 70 and 71:

- **The N×M walk measurement** from §UC-168, as a number, under `## P4`, so
  Reject 3's shared-reader decision has evidence rather than an intuition
  ([SPEC] §8.5). Written: **8.0x**, eight runners reading 320 envelopes in 65
  walks against a one-projection baseline of 40 in 12.
- **A `parktest`-style conformance harness** for a `Park`/`Redriver`. The two
  count bounds are exercised by §UC-151 and the byte bound only by the example
  ([SPEC] §8.3); the shape that would close it is a published suite, and it is
  not in this phase. `[medium]`.
- **A second store's topology certification.** The two new `eventtest` sections
  are proved against `eventmemory` and `eventpg`; a third implementation is now
  *provable* and none exists. `[low]`.

**Not debt, and named so it is not read as debt:** ES-05, ES-07, ES-08 and ES-09
are phase 5 by [APX] and [SPEC] §2, and §1.5 shows ES-04's readiness does not
need ES-05 — which was the study's third open question and is now answered.
