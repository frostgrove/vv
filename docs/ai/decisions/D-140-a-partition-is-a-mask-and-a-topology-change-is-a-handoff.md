# D-140 — A partition is a mask, and a topology change is a handoff

**Status:** accepted
**Invariant:** A partition is `(id, mask)` with `mask = 2^k − 1`, and a key
belongs to the partition whose id equals the low `k` bits of FNV-1a/32 over the
UTF-8 bytes of its sequence key. No partition count is stored anywhere and
nothing computes a partition from a modulus. The only topology change is a
`Split`: one partition becomes two at mask `2m+1`, both starting at the parent's
exact cursor, the parent's retirement is recorded and its row removed — in one
transaction of the caller's, refused outright without a parent row, and holding
no state between runs. There is no merge, and no spelling for one.

## The decision

### The mask, and why `hash % N` is corrupting rather than inconvenient

The obvious partitioner is `hash(key) % N`. It is correct while `N` is fixed and
it is unrecoverable the moment `N` changes, which is the only reason anyone ever
partitions: the load grew.

Under `hash % N → hash % (N+1)` roughly `N/(N+1)` of all keys change partition,
and each lands in one whose checkpoint is at an **unrelated** position. Two
things follow at once, in opposite directions. A key that moved to a partition
standing *ahead* of where its old one stood has the events between the two
positions **skipped** — nothing will ever deliver them, and every checkpoint row
reports a healthy watermark while it happens. A key that moved to a partition
standing *behind* has its history **re-delivered out of order**: its `paid`
lands, then its `placed` lands on top of it. Neither is signalled anywhere. The
read model is simply wrong, in both directions at once, from the moment the
release rolls out.

A mask is the arithmetic that has no such moment. `Partition.Split` is Axon's
`Segment.split`: the lower child keeps the parent's id under the finer mask and
the higher one takes the bit the finer mask added. Every key the parent matched
is matched by exactly one of them, and **no key of any other partition moves at
all**. That is the whole difference, and it is measured rather than asserted:
`TestFourPartitionsOverOneLogAndTheModulusControl` runs four real partitions over
one log and then runs the modulus re-partitioning as a fixture beside them,
asserting that the reordering and the skipping both exist.

`Partition.Count()` exists for a reader and stores nothing. It is the one place
the forbidden arithmetic is even spellable, and what stops it being used is a
source walk: `TestNoModulusIsAppliedToASequenceHash` reports any `%` applied to
the result of the package's own hash, with a fixture control that proves the walk
finds one when there is one.

### The handoff, in five steps, and why steps two and four are not redundant

`Split` reads the parent's row, reads the record of the parent's own retirement,
reads **both children's rows**, writes both children at advance 1 carrying the
parent's cursor byte for byte, records the retirement, and removes the parent's
row. It is one transaction of the caller's — `crud.InNewTx(ctx, source, work)` —
because the framework opens none ([[D-130]]).

Two of those steps look redundant and are the point.

**Both children are read BEFORE either is written.** A child row standing beside
a live parent is a finer topology that is already recording. Writing over it
hands one share of the log to two writers, each with its own fence and its own
watermark, and neither of them can tell. The read is what turns that into
`ErrTopology` naming the child and the advance it stands at.

**The retirement is recorded as a row of its own, before the parent's row is
removed.** `Forget` deletes the only evidence that the retired share was ever
recorded. Without the record, redeploying the release that ran *before* the split
is admitted at its next deploy, resumes from the origin, and walks the whole log
again into the read model the children are filling — with every row reporting a
healthy watermark while it happens. With it, that runner halts at its first
resume. This is vv's replacement for Axon's `validateSegment`: Axon asks the
token store whether the segment it holds is still part of the current set, and vv
asks the rows, because the rows are the only place a topology is written down.

Every decision is derived from rows read inside the run and nothing is carried
between runs, so a `Unit` that runs its body twice — which [[D-130]] says a
caller's may — performs one split or answers a refusal, and never two children at
the origin.

### An absent parent is refused, whatever the reason for the absence

A partition that never ran and a partition whose row was forgotten, dropped or
restored in part read **alike** from the rows. The framework cannot tell them
apart and does not guess: both are `ErrTopology`, and the message names both
readings with both remedies — a partition that never ran needs no split (declare
the `Cover` and start its members, and each begins where it is declared), and a
partition whose row was lost is a row to restore.

The guess that looks safe is not. Answering the two children and writing nothing
is the arm that makes the dangerous reading indistinguishable from the safe one;
writing them at the origin walks the whole log into a live read model. This is
[[D-133]]'s halt case by another door: an absent row where one is owed is
terminal, because the alternative is a silent replay.

A parent that was **not drained** meets the same rule from the other side. Its
next save finds no row at its advance, is refused, and it halts — it does not
create a fresh row at advance 1 beside its children, and it does not report
contention (`ErrOvertaken`, which does not halt). `TestARunningParentHaltsWhenItsRowIsSplitAway`
drives that live, against a drained-parent control that halts nothing.

### A merge is refused, and here are both reasons

Two partitions becoming one is not the inverse of a split; it is a different
operation that cannot be performed at all, and there are **two** independent
reasons.

The first is [[D-129]]. The survivor has to resume from somewhere, and the only
candidates are the two children's cursors. Choosing between them is ordering two
cursors, and a cursor is opaque bytes a store minted rather than a point on a
line. Taking the "lower" one re-delivers everything between them to the child
that was ahead; taking the "higher" one skips everything between them for the
child that was behind. There is no third answer.

The second is narrower and it is why the refusal cannot be relaxed for a
particular store. `eventpg`'s cursor carries a read-time `bound` — the watermark
below which the store has settled — so **two walks that reached one position mint
different bytes**. Even a store whose cursor embeds its position could not be
merged by arithmetic on the number, because the rest of the cursor is not a
function of the number.

So there is no `Merge`, and the surface is what says so:
`TestNoExportedFunctionOrdersOrTakesTwoCursors` asserts that no exported
signature of the extension mentions `event.Cursor` twice in its parameters, with
a fixture control declaring exactly the function that would. The ordering half is
`TestCursorIsNeverCompared`'s. The route to a coarser topology is a new
generation ([[D-141]]), which replays into its own rows and needs no such
decision.

### `Cover`, and what it does not cover

A mask makes each partition correct **on its own** and says nothing about the
set, which is where the two silent failures live: a forgotten member, whose share
of the log is never delivered to anything while every runner reports healthy; and
two members that overlap, which are two names, two checkpoint rows, no fence
between them, and every key matching both applied twice and out of order the
moment their cursors diverge.

`NewCover` is the one place a set is checked, on two exact arithmetic facts:
two members overlap when `(idA ^ idB) & min(maskA, maskB)` is zero, and a set
with no overlap covers the space when the members' shares sum to
`MaxPartitions`. `Observe`, `Reached` and `Cutover` take a `Cover` and never a
`[]Partition`, so no aggregate answer in this phase is computed over a set with a
gap.

What it does not cover is stated rather than implied: **a single runner cannot
see the set.** A host that assembles runners by hand keeps the freedom it always
had, and the `_examples` show only the `for _, part := range cover.Partitions()`
spelling because that is the cheapest way to make the checked path the obvious
one.

Because the guarantee is carried by the type, the zero value has to be refused
where it is taken. `Cover{}` is a legal composite literal in any package,
`Count() == 0` tells it from every checked set exactly, and every door asks —
which is why no marker field is carried. The same holds for `Identity{}`, whose
`Projection() == ""` is the same exact discriminator, and `NewIdentity` spells
the kernel's identifier rule a second time rather than delegating to it, because
an `Identity` is an input at five doors that never reach `event.Track` and §5.1
freezes the `event` surface. The two are pinned to one another by
`TestNewIdentityRefusesEveryNameTheKernelRefuses`, and the deliberate difference
is exactly two characters — `@` and `#` — and no more.

### Only the first start chooses a topology

A runner whose partition is not `Whole()` refuses to resume while a checkpoint
row exists for any **coarser** share of its own key space. It is a resume-time
question rather than a construction check because it is about rows, it is asked
once per runner life, it costs one `Load` per ancestor, and it costs a projection
that named no partition nothing at all.

The finer direction is outside it, and stated as such: enumerating the finer set
would need a `List` the `Checkpoints` contract does not have, and that direction
is a merge anyway.

### The cost, stated because it is the opposite of Axon's

Axon's segments divide one tracking processor's work: N segments over one event
stream, coordinated by one processor, reading it once. vv's partitions are N
separate runners with N checkpoint rows, and each one **walks the whole log**.
N partitions cost N× the read traffic, and N partitions × M generations cost
N×M.

That is measured and recorded rather than stated:
`TestEightWalksCostEightTimesOneProjectionsReads` runs two generations at four
partitions each over one log and reports the ratio — 8.0× at the time of
writing — beside a one-projection baseline, and the number is in
`EVENTSOURCE_BACKLOG.md` under `## P4`.

The lever that would change it is a shared reader, and it is deliberately not in
this phase (Reject 3): adding a filter to `Log.ReadAll` would make the log's
contract about a consumer's partitioning, and a shared reader is a second piece
of coordination between runners that are separate processes on purpose. The
measurement above is what that decision will eventually be made on.

## What this forecloses

- **No `Merge`, and no exported signature taking two cursors.** Both reasons
  above stand independently, so relaxing either does not open the door.
- **No partition count stored anywhere**, and no arithmetic that derives a
  partition from one. `Partition.Count()` is for a reader.
- **No filter on `Log.ReadAll`.** The N× cost is paid and recorded.
- **No guess at the meaning of an absent parent row.** Both readings are refused
  and both remedies are named.
- **No aggregate over an unchecked partition set.** Every door takes a `Cover`,
  and every door refuses `Cover{}`.

## Where it lives

- `event/projection/partition.go` — `Partition`, `NewPartition`, `Whole`,
  `ParsePartition`, `Matches`, `Split`, `Count`, and `MaxPartitions`.
- `event/projection/cover.go` — `Cover`, `NewCover`, `Partitions`, `Count`.
- `event/projection/identity.go` — `Identity`, `NewIdentity`, `ParseIdentity`,
  `Whole`, and `retiredMark`, the name a split's retirement row is keyed by.
- `event/projection/sequence.go` — `Sequencer`, `ByStream`, `OneSequence`,
  `Unordered`, `SequenceBy`.
- `event/projection/topology.go` — `Split`, `SplitSpec`, `handOver`,
  `recordingFiner`, `retired`, `noParent`, `alreadySplit`, `alreadyFiner`.
- `event/projection/pass.go` — the partition filter, applied to every page before
  a handler sees one.

## Proven by

- `TestFourPartitionsOverOneLogAndTheModulusControl` (`event/eventpg`, live) —
  four runners at mask 3 over one log, every key in order and in one partition,
  four rows advancing independently. **Control:** the `hash % 3 → hash % 4`
  re-partitioning as a fixture, asserting that the reordering and the skipping
  are both there.
- `TestASplitsThreeStatementsAreOneTransaction` (`event/eventpg`, live) — a
  failure injected between the two child writes leaves **neither** child row and
  the parent exactly where it was. **Control:** the same split uninjected makes
  the handoff.
- `TestARunningParentHaltsWhenItsRowIsSplitAway` (`event/eventpg`, live), with
  the drained-parent control.
- `TestASplitWithNoParentRowWritesNothing` (`event/eventpg`, live) — both
  absences refused, rows read in `psql`, against the has-run control.
- `TestASplitLeavesTheParkedLettersReachable` (`event/eventpg`, live) — a park is
  keyed by `Identity.Whole()`, so a split does not orphan its letters; with
  `TestASplitOverAnEmptyParkLeavesBothChildrenPayingNothing` as the control.
- `TestNoModulusIsAppliedToASequenceHash` (`scripts`) — the AST walk, with its
  fixture control.
- `TestNoExportedFunctionOrdersOrTakesTwoCursors` (`scripts`) — the merge's
  signature, with the `func Merge(held, taken event.Cursor) event.Cursor`
  fixture control. `TestCursorIsNeverCompared` holds the ordering half.
- `TestEightWalksCostEightTimesOneProjectionsReads` (`event/eventpg`, live) — the
  N×M measurement, recorded as a number.
- `TestSplitPreservesTheSumAcrossThePartitionSet`,
  `TestACutoverRefusesARetiringCoverNoMemberOfWhichHoldsARow` and the
  `event/projection` cover, identity and topology suites.

## See also

[[D-092]] [[D-125]] [[D-128]] [[D-129]] [[D-133]] [[D-141]] [[FL-038]]
