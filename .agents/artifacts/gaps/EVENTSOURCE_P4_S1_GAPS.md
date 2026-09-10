# EVENTSOURCE_P4 — S1 (the name, the set, the sequence, and the one authority) — GAPS

## Round 1 — econv code reviewer — 2026-09-09

Reviewed against [`EVENTSOURCE_P4_PLAN.md`](../plans/EVENTSOURCE_P4_PLAN.md) §S1 and §P-1…P-8,
[`EVENTSOURCE_P4_USECASES.md`](../usecases/EVENTSOURCE_P4_USECASES.md) §1.1–§1.2, §5.2,
UC-131/132/133/135/137/141/142/145/186/187/188 and INV-083/085/086/097/104/105,
[`EVENTSOURCE_P4_STUDY.md`](../usecases/EVENTSOURCE_P4_STUDY.md) §0, §ES-01, §ES-02,
[`EVENTSOURCE_REFERENCE.md`](EVENTSOURCE_REFERENCE.md), the binding decisions D-092, D-118,
D-126, D-128, D-129, D-130, D-133, and the code as it stands. Everything below was executed
or driven, not read.

**Round 2 — closure, 2026-09-09.** All five are closed; each was reproduced first
and each carries a test that fails when the fix is reverted, with the failure
output recorded in the plan's mutation table. The section's plan moved with the
code: **P-9** (the filter lands in S1), **P-10** (the resume-time topology
refusal), **P-11** (the zero values) and **P-12** (the duplicated identifier rule)
are new decisions in `EVENTSOURCE_P4_PLAN.md`, §UC-194, §UC-195, §UC-196 and
§INV-107 are new in `EVENTSOURCE_P4_USECASES.md`, and the coverage matrix,
S1's and S2's test lists and S1's checkpoint moved with them. Per-gap resolutions
are at the end of each entry below.

**Verdict: red.** One `[critical][immediate]` and four `[high][immediate]`. Nothing in the
section is unsound arithmetic — the mask, the cover arithmetic, the injectivity and the
alignment comparison are all correct and all load-bearing under a break test. What is wrong is
that the section published a topology vocabulary whose central promise the tree does not yet
keep, left the one Axon mechanism that guards a topology *handover* unimplemented and
unrecorded, and shipped code that contradicts three documents that were not touched.

**The checkpoint pasted into the plan is real.** Re-run verbatim, in the corrected order, from
`.git/event_kernel_before_s1`: `EXIT=0`, the `-list` arms counted 11 and 1, and the seventeen
moved paths came back in the same order with the same names. `make check` is green on all ten
arms, `make unit` green across every module, `make examples` green, `gofmt -l .` silent,
`go vet ./event/...` silent, and the `event` section of `docs/api/surface.md` diffs clean
against `.git/event_surface_before_p4`.

---

### GAP-1 [critical][immediate] `Cover`, `Spec.Partition` and `Partition.Matches` are published and inert: a set `NewCover` admitted, used exactly as the plan's §8.7 spelling prescribes, applies every event once per member

- **Where:** `event/projection/cover.go:8-19` (the `Cover` doc comment and type),
  `event/projection/cover.go:24-46` (`NewCover`), `event/projection/spec.go:74-81` (the three
  new fields and their comment), `event/projection/partition.go:89-91` (`Matches`, which
  nothing calls), `event/projection/pass.go:227-232` (`wholePage`, which hands the handler the
  page the log answered with no filter of any kind), `docs/api/surface.md:1100-1131`
  (regenerated, so all of it is now published surface).

- **What:** `Spec.Partition` is accepted, rendered into `Identity`, rendered into the checkpoint
  row key and into `Projection.Name()` — and it filters nothing. `Partition.Matches` has zero
  callers in the tree (`grep -rn 'Matches(' event/ --include='*.go' | grep -v _test.go` →
  nothing). `Cover.Partitions()` hands a host a checked set, and the runners built from that
  set each apply the whole log.

  Driven, over `eventmemory`, with the plan's own §8.7 spelling
  (`for _, part := range cover.Partitions()`), 8 streams × 3 events:

  ```
  the cover of 2 members was admitted
  runner "vv.event.projection.orders#0.1" was admitted for partition "0.1"
  runner "vv.event.projection.orders#1.1" was admitted for partition "1.1"

  0 events applied exactly once, 24 applied more than once, 24 distinct events
    a/1 applied 2 times
    a/2 applied 2 times
    a/3 applied 2 times
    b/1 applied 2 times

  row "orders"     advance=0 applied=0  highest=0  fresh=true
  row "orders#0.1" advance=1 applied=24 highest=24 fresh=false
  row "orders#1.1" advance=1 applied=24 highest=24 fresh=false
  ```

  Both rows report `Applied: 24` over a log of 24. Both runners report `PhaseFollowing`. No
  refusal, no observer transition, no error on any path.

  `Spec.Partition`'s own comment (`spec.go:74`) reads *"The three that name this projection's
  own share of the log"*, and [SPEC] §5.2's text for the same field is *"The fraction of the
  key space this runner owns. … Take it from a `Cover`"*. Neither is true of the delivered
  field: it names a share of the checkpoint row key and nothing else. `Cover`'s comment
  (`cover.go:16-18`) then tells a reader *"Build your runners from `Partitions()`: a set that
  was checked is the one thing that makes those two unconstructible"* — where "those two" are
  a gap and an overlap, whose consequence the two lines above spell as *"every key matching
  both applied twice and out of order"*. Following that instruction today produces exactly that
  consequence, from a set that was checked.

- **Why this severity:** §UC-135's *Must not* is *"No envelope may be applied by two
  partitions, and no partition may apply an envelope whose key does not match it"*, and
  §INV-105 rests the whole ES-02 guarantee on the shape *"`Observe`, `Reached` and `Cutover`
  take a `Cover`"*. What the tree publishes is an API whose documented use violates the first
  of those on the first event, silently, with both checkpoint rows healthy. A handler that is
  an idempotent upsert on `(Stream, Version)` survives it; a handler that increments a counter,
  inserts a row, or writes an append-only ledger — all of which `InUnit` exists to serve —
  corrupts its read model N-fold and nothing in the framework says a word. `make api` has
  already recorded the surface, so a consumer reading `docs/api/surface.md` sees
  `Cover`/`NewCover`/`Whole`/`NewPartition`/`Spec.Partition` as shipped capability.

- **Why this timing:** it cannot be deferred past this section's `[x]` because the surface is
  already regenerated and the doc comments already make the promise. The plan schedules the
  filter for S2 (`matchedPage`), and that is a legitimate close — but only with a control that
  is red against today's tree, because the plan's S2 test list
  (`TestFourPartitionsApplyEveryEventOnceAndKeepEachKeyInOrder`) is written as a positive case
  plus two sequencer controls and would pass just as well if `Matches` were still uncalled and
  the four partitions each read a disjoint quarter for some other reason. Closing it in S1 is
  one refusal and one test; closing it in S2 is the filter plus the control. Leaving S1 `[x]`
  with neither is what makes the section report a delivery it did not make.

- **Close criteria:**
  - [ ] Either (a) `New` refuses a `Spec.Partition` that is not `Whole()` with `ErrTopology`,
        naming the field and saying the filter has not landed, and a test pins the refusal —
        or (b) S2 lands the `matchedPage` filter and the section's own checkpoint runs a test
        that is **demonstrated red** against `git stash`-ed S2 (record the failure output in
        the plan, not just the pass).
  - [ ] `Spec.Partition`'s doc comment says what the field does in the tree as it stands, or
        the field is not exported until it does it.
  - [ ] `Cover`'s doc comment does not instruct a host to build runners from `Partitions()`
        until building runners from `Partitions()` delivers each key to exactly one of them.
  - [ ] A test asserts `Partition.Matches` has a caller in a non-test file of
        `event/projection` — the same shape as the existing surface walks — so an inert
        predicate cannot ship again.

- **Status: CLOSED — the filter landed in S1 rather than being deferred to S2.**

  **Reproduced first.** The review's numbers came back exactly: 8 streams × 3
  events, a cover of `{0,1}` and `{1,1}`, runners built with
  `for _, part := range cover.Partitions()` —
  *"0 events applied exactly once, 24 applied more than once, 24 distinct
  events"*, `row "orders#0.1" advance=1 applied=24 highest=24` and the same for
  `orders#1.1`.

  **Which close was taken, and why not the other one.** The refusal at `New` was
  rejected: it leaves `Matches` inert, so this entry's own fourth criterion is
  unsatisfiable while it stands, and it makes GAP-2's refusal undrivable because
  no partitioned runner could be constructed to drive it. So `Projection.matching`
  landed in S1 (**P-9**), with `matchedPage`, the isolation pass iterating the
  matched page, and the sequencer-panic halt that is not separable from calling a
  sequencer out of the loop. Four tests moved from S2 to S1 with it. After the
  fix, the same driver: *"24 events applied exactly once, 0 applied more than
  once"*, `orders#0.1 applied=12 highest=24`, `orders#1.1 applied=12 highest=24`.

  - [x] The filter landed, and the test is **demonstrated red** against the tree
        as it was reviewed — `matching` mutated to answer the whole page, three
        tests red, output in the plan's mutation table.
  - [x] `Spec.Partition`'s doc comment says what the field does, and [SPEC] §5.2's
        text is corrected to match.
  - [x] `Cover`'s instruction to build runners from `Partitions()` is now true.
  - [x] `TestEveryPublishedTopologyPredicateHasACaller` (`scripts/`) asserts
        `Partition.Matches` and `Sequencer.SequenceOf` each have a call in a
        non-test file, with a fixture control that declares both and calls
        neither. Deleting the `Matches` call turns it red.

---

### GAP-2 [high][immediate] ES-02 nuance 3 is dropped: nothing refuses a partitioned set started beside a live whole-space checkpoint row, so the obvious migration replays the whole log against a live read model

- **Where:** `event/projection/spec.go:126-165` (`New`, which builds the identity and never
  looks at any other row), `event/projection/pass.go:55-68` (`resume`, which loads this
  identity's row and no other), `event/projection/identity.go:105-108`
  (`Identity.Whole()`, the value that would name the row to look at, present and unused by any
  check). Nothing in `event/projection` reads a second checkpoint name.

- **What:** [`EVENTSOURCE_P4_STUDY.md`](../usecases/EVENTSOURCE_P4_STUDY.md) §ES-02 nuance 3 —
  *"Only the **first** start may choose the count. … `JpaTokenStore.initializeTokenSegments`
  throws `UnableToClaimTokenException("Could not initialize segments. Some segments were
  already present.")` rather than adopting whatever is there."* — is in neither the code nor
  the spec nor the plan. [SPEC] §"The set is the thing that has to be right" cites
  `initializeTokenSegments` by name at line 341 and maps it to the **gap and overlap** check,
  which is `validateSegment`'s job, not `initializeTokenSegments`'s. The mechanism the citation
  points at is the one that refuses a topology being *adopted over an existing one*, and vv has
  no analogue.

  Driven, the migration an operator will actually perform — release *n* runs `orders`
  unpartitioned to completion, release *n+1* adds `Partition:` to the spec and deploys two
  replicas:

  ```
  release n: "orders" drained; applied=1000 row{advance=4 highest=1000}
  release n+1: two partitioned runners were admitted with no refusal;
               they applied 2000 MORE envelopes over a log of 1000
    row "orders"     advance=4 highest=1000 applied=1000 fresh=false
    row "orders#0.1" advance=4 highest=1000 applied=1000 fresh=false
    row "orders#1.1" advance=4 highest=1000 applied=1000 fresh=false
  ```

  Three rows, all reporting `Highest: 1000, Applied: 1000`, all green. The read model took
  every event three times.

  **This is independent of GAP-1 and survives S2.** Once `matchedPage` filters, the two
  partitioned runners apply their own halves — from the origin, over a read model `orders`
  already populated to position 1000. The duplicate count falls from 2000 to 1000; it does not
  fall to zero. The only safe route from unpartitioned to partitioned is `Partition.Split`
  through `SplitSpec` (S2), which hands the parent's cursor down; nothing steers an operator to
  it and nothing refuses the other route. §UC-140's remedy sentence — *"a partition that never
  ran needs no split — declare the `Cover` and start its members"* — is the dangerous
  instruction read out of context, because the framework cannot tell "never ran" from "the
  whole-space row is right there".

- **Why this severity:** the parent's rule is explicit — a dropped study nuance is `[high]`
  unless the code documents why it does not apply, and nothing documents it. Beyond the rule:
  the failure is the exact shape §1.2 and §INV-086 exist to refuse (*"events of one key are
  skipped in one direction and re-delivered out of order in the other"* is `hash % N`'s
  version; this is the same outcome reached by a different door), it happens on the single most
  likely operator action, it produces no error, and every dashboard reads healthy. `Progress` is
  a completeness watermark per row, so three rows at 1000 is exactly what a correct
  four-partition set looks like from the outside.

- **Why this timing:** the check lives at resume, which is S2's `pass.go`, and S2's test list
  does not contain it — so if it is not recorded now with a use case attached it does not get
  written at all. It also needs a decision, not just code: the refusal costs one extra
  `Checkpoints.Load` of `Identity.Whole()`'s name at resume (through a second `event.Track`,
  which is the legal door under [[D-129]]), and whether that cost is paid once per start or not
  at all is the owner's call, not an implementer's.

- **Close criteria:**
  - [ ] A use case and an invariant state what happens when a partitioned runner starts and the
        whole-space row for its projection exists — refused, or admitted with the consequence
        written down — and the plan assigns it to a section.
  - [ ] If refused: `resume` reads `Identity.Whole()`'s row once, refuses with `ErrTopology`
        naming both rows and naming `Split` as the migration, and a test drives the two-release
        sequence above and asserts the second release never reaches a handler. **Control:** the
        same second release after a `Split` of the parent is admitted and applies nothing twice.
  - [ ] If admitted: `docs/modules/{en,ru}/projection.md` carry the two-release sequence with
        its measured outcome, and [SPEC] §5.2's `Spec.Partition` comment names `Split` as the
        only route from an existing projection.
  - [ ] [SPEC] line 341's citation of `initializeTokenSegments` is corrected to
        `validateSegment`, or the sentence is split so each mechanism names the failure it
        actually guards.

- **Status: CLOSED — refused at the resume, over every coarser ancestor.**

  **Reproduced first**, at the review's shape and a smaller size: `orders` drained
  unpartitioned to `advance=1 highest=200 applied=200`, then `{0,1}` and `{1,1}`
  admitted with no refusal — three rows all at `highest=200 applied=200`, and
  *"0 applied once, 200 applied more than once"*: the read model took the whole
  log a second and a third time.

  **What was built (P-10).** `Projection.unclaimed` runs once per runner life, at
  the resume, and refuses with `ErrTopology` when a checkpoint row is live for any
  **coarser** share of this runner's key space. It is every ancestor rather than
  `Identity.Whole()` alone, because the same hazard one level down is `{0,3}`
  started beside a live `orders#0.1`; the ancestor set is at most ten rows and is
  **empty** for a projection that named no partition, so the unpartitioned path
  pays nothing — measured live, where every `eventpg` case runs at `Whole()`.

  - [x] §UC-194 and §INV-107 state the outcome; the plan assigns it to S1 and
        re-runs its control through the real `Split` in S2.
  - [x] `resume` reads the coarser rows and refuses with `ErrTopology` naming both
        the row it found and `Split`.
        `TestAPartitionedRunnerBesideALiveCoarserRowIsRefused` drives the
        two-release sequence and asserts no handler is reached and no row is
        written. **Control A:** the handoff spelled by hand through `event.Track`
        — both children at the parent's cursor, the parent retired — is admitted
        and applies nothing twice. **Control B:** a partition set on a projection
        that never ran drains. Plus the ancestor arm.
  - [x] The module pages carry the migration and its refusal in both languages,
        and [SPEC] §5.2's `Spec.Partition` comment names `Split` as the only route
        from an existing projection.
  - [x] [SPEC] line 341's citation is split: `validateSegment` keeps the gap and
        overlap, `initializeTokenSegments` is moved to the new §1.3.1, which is
        the failure it actually guards.

  **What is not closed and is recorded rather than forgiven:** the mirror
  direction — a live *finer* row beside a coarser runner — is backlog item 32
  `[medium]`. It needs a `List` the `Checkpoints` contract does not have, and the
  direction it guards is a merge, which §1.3 refuses outright. §1.3.1 states the
  limit out loud.

---

### GAP-3 [high][immediate] `NewIdentity` admits brackets, control characters, invalid UTF-8 and whitespace-only names, and its own doc comment says it does not

- **Where:** `event/projection/identity.go:22-33` (the doc comment: *"Within
  `event.MaxNameBytes` and carrying neither bracket, so it passes the kernel's identifier
  rule"*), `event/projection/identity.go:40-56` (`NewIdentity`, which checks empty, the two
  delimiters and the byte bound, and nothing else),
  `event/projection/identity.go:61-82` (`ParseIdentity`, which ends in `NewIdentity` and
  inherits the hole), `event/text.go:62-70` (`checkName`, the rule the comment claims).

- **What:** the comment above `NewIdentity` says *"It does not re-implement the kernel's
  identifier rule. `New` hands `String()` to `event.Track`, which is the door that owns it"*.
  That delegation is real for the `Spec` path and only for it. Driven:

  ```
  NewIdentity("a[b]c")   -> "a[b]c"   err=<nil>
  NewIdentity("a\x00b")  -> "a\x00b"  err=<nil>
  NewIdentity("a\nb")    -> "a\nb"    err=<nil>
  NewIdentity("  ")      -> "  "      err=<nil>
  ParseIdentity("a[b]c@2#3.7") -> "a[b]c@2#3.7" err=<nil>
  ```

  The `[` is the character `event/text.go:41-47` names as *"the two characters a rendered field
  is framed with … a frame the guard does not read is a guard that protects nothing"*, and
  `checkName` refuses it precisely so *"an identifier a program declared always names itself in
  a log line"*.

- **Why this severity:** `Identity` is the phase's primary key, and from S3 it is an **input**
  rather than an output — `Letter.Identity`, `RedriveSpec.Of`, `Observe(ctx, of)`,
  `Cutover`'s `from`/`to` all take an `Identity` a caller built with `NewIdentity` and none of
  them passes through `event.Track`. §UC-145's *Must not* is *"the rendering must not use `[`
  or `]`"*, stated about the value and not about the `Spec` path. Concretely: an application
  calls `projection.NewIdentity("orders]", Ungenerated, Whole())` for its redrive tooling; the
  value is accepted; it keys a park table row that no projection can ever be constructed for,
  so the queue is orphaned and `Holes` counts letters nothing will ever drain; and the same
  string reaches an operator's log through the refusal texts that interpolate it, closing a
  field the renderer opened. A type whose doc comment asserts an invariant it does not hold is
  worse than one that asserts nothing, because the next four sections are written trusting it.

- **Why this timing:** S3, S4 and S5 all take an `Identity` at a public door. If the invariant
  is not on the constructor, each of those doors has to re-derive it, and the first one that
  forgets is the one that ships. It is also the last moment the choice is free: §5.1 freezes
  the `event` surface for this phase, so exporting a kernel name-checker is not available and
  the answer has to be either a deliberate duplication in `NewIdentity` or a per-door check —
  and that is a decision, recorded, not an implementer's shrug.

- **Close criteria:**
  - [ ] `NewIdentity` refuses a projection name the kernel's identifier rule refuses —
        brackets, control characters, invalid UTF-8, empty — wrapping `ErrSpec` and naming the
        field, **or** the comment at `identity.go:22-33` stops claiming the property and every
        door in S3–S5 that takes an `Identity` checks it, with a test per door.
  - [ ] If the rule is duplicated, a decision record says why (§5.1's freeze) and a source walk
        or a test pins the two spellings against drift, in the shape `event/text.go:62-70`'s
        comment already asks for.
  - [ ] `TestAnIdentityRendersAndRoundTrips`'s bracket assertion moves from one hand-picked
        value to the constructor: `NewIdentity` over a table of names the kernel refuses answers
        an error for every one. **Control:** `orders.v2` and `orders/paid` are still admitted.

- **Status: CLOSED — the rule is on the constructor, and the duplication is
  pinned.**

  **Reproduced first:** `NewIdentity("a[b]c")`, `("a\x00b")`, `("a\nb")`,
  `("a\xffb")` and `ParseIdentity("a[b]c@2#3.7")` all answered `err=<nil>`,
  against the kernel refusing `a[b]c` as *"a projection name contains a bracket"*.

  - [x] `NewIdentity` refuses what the kernel's identifier rule refuses —
        `unnameable`, phrase for phrase: empty, over `MaxNameBytes`, invalid
        UTF-8, a control character, a bracket — wrapping `ErrSpec` and naming
        `Name`.
  - [x] **P-12** records why the rule is duplicated (§5.1's freeze, and an
        `Identity` being an input at five doors that never reach `event.Track`),
        and `TestNewIdentityRefusesEveryNameTheKernelRefuses` is the drift pin: it
        walks `NewIdentity` and `event.Track` over one table **and over every byte
        a name can carry**, failing on a name only one of them takes.
  - [x] The bracket assertion is no longer one hand-picked value: twelve refused
        names, plus the byte walk, plus the bound arm. **Control:** `orders.v2`,
        `orders/paid`, a non-ASCII name and a name of two spaces are admitted by
        both.

  **One item of this entry is rejected, with the reason.** The review lists
  `NewIdentity("  ")` among the defects. It is not one, and refusing it would
  break the property the rest of the entry asks for. `event/text.go`'s own rule is
  explicit — *"No case folding, no trimming, no Unicode normalisation — keys
  compare as bytes"* — so the kernel takes `"  "` as a projection name, as a
  stream family and as a wire type name. `NewIdentity` taking it too is the two
  spellings **agreeing**; refusing it would be this package inventing a naming
  policy the framework does not have, and the drift pin would then have to carry a
  hand-written exception list, which is exactly the thing that rots. The two
  deliberate differences from the kernel stay at two, `@` and `#`, and each has a
  use case and a test. Recorded here rather than silently skipped.

---

### GAP-4 [high][immediate] `Cover{}` and `Identity{}` are constructible from outside the package, so the two values whose whole job is to carry a proof can be produced without one

- **Where:** `event/projection/cover.go:19` (`type Cover struct{ partitions []Partition }` —
  unexported field, but `projection.Cover{}` is a legal composite literal in any package),
  `event/projection/identity.go:34-38` (same), `event/projection/cover.go:51`
  (`Count()`, the only thing that can tell a checked set from a zero one, and nothing consumes
  it), and the S4 plan block at `EVENTSOURCE_P4_PLAN.md:1949-1958`, whose `Observe` tests cover
  an absent row and do not cover an empty cover.

- **What:** driven:

  ```
  Cover{} literal: count=0 partitions=[]
  Identity{} renders "", projection="" gen=0 partitionWhole=true
  ```

  `NewCover()` with no arguments is refused — *"a cover of no partitions matches no key"* — and
  `projection.Cover{}` produces that same value with no refusal. `NewIdentity("")` is refused
  and `projection.Identity{}` produces an identity that renders the empty string.

- **Why this severity:** §INV-105's statement of the mechanism is *"`Observe`, `Reached` and
  `Cutover` take a `Cover` and never a `[]Partition`, so no aggregate answer in this phase is
  computed over a set with a gap"* — the guarantee is carried by the **type**, and the type does
  not carry it. §UC-143 says `Observe` answers a `min` over the cover; over zero members a `min`
  is either the origin (harmless) or the identity element of the fold, `MaxUint64` (a barrier
  every generation trivially clears, so §UC-159's `Reached` answers true and §UC-163's cutover
  proceeds on no evidence at all). Which of those two S4 writes is not decided anywhere, so
  today the difference between "a cutover refused for want of evidence" and "a cutover that
  committed on none" is a line S4 has not written yet. This spec already applies the opposite
  standard to the identical hazard one page earlier: §UC-133's *Must not* is *"`Unchecked` must
  not be reachable by leaving `Destination` zero"*, and `TestUncheckedMakesNoComparisonAtAll`
  pins it. A `Cover` reachable by leaving it zero is the same defect with a different name.

- **Why this timing:** the cheap fix belongs on the type, in the section that owns the type. Once
  S4 is written against `Cover`, the guard has to be repeated at three doors instead of stated
  once, and `Identity{}` will by then be an input at five. It is also the difference between a
  compile-time property and a run-time one: an unexported non-zero marker makes
  `projection.Cover{}` fail at every consumer for free.

- **Close criteria:**
  - [ ] `Cover` carries something a zero value cannot fake — an unexported `checked bool`, or a
        `Valid()`/`Count() > 0` precondition — and every door in this phase that takes a `Cover`
        refuses the zero value with `ErrTopology` naming the field.
  - [ ] The same decision is made and recorded for `Identity{}`: either a marker, or every door
        that takes an `Identity` refuses one whose `Projection()` is empty.
  - [ ] A test constructs `projection.Cover{}` and `projection.Identity{}` from
        `package projection_test` and asserts the refusal at each door. **Control:** the same
        doors admit the values `NewCover`/`NewIdentity` answered.

- **Status: CLOSED — the discriminator is exact, and the enforcement lands before
  the doors do.**

  **Reproduced first:** `Cover{} literal: count=0 partitions=[]` and
  `Identity{} renders "", projection=""`, against `NewCover()` and
  `NewIdentity("")` both refused.

  **What was decided (P-11), and it is not a marker.** Neither type has an
  exported field, so the zero composite literal is the **only** value reachable
  without the constructor — and it is the one value the constructor never answers
  beside a nil error. That makes `Count() == 0` and `Projection() == ""` exact
  rather than heuristic, and a `checked bool` beside them would be redundant state
  whose only writer is the same constructor. Both doc comments now say so.

  - [x] The rule — every door refuses the zero value, `Cover` with `ErrTopology`
        and `Identity` with `ErrSpec` — is in §UC-195 and in §INV-105's statement,
        and the plan carries it into S3, S4 and S5.
  - [x] `TestEveryDoorTakingACoverOrAnIdentityRefusesItsZeroValue`
        (`scripts/projection_test.go`) walks `event/projection` for an exported
        function taking either as a **parameter** whose body never asks. It reports
        nothing today, because no such door exists yet, and its fixture control has
        two doors that ask and two that do not and asserts exactly the two that do
        not are reported. It goes red in the section that lands the first door
        without the question.
  - [x] `TestTheZeroCoverAndTheZeroIdentityAreTellableFromEveryCheckedOne`
        constructs both zero values from `package projection_test` and shows the
        discriminator exact against every value the two constructors answer.

  **Why a refusal at a door was not written in S1:** there is no door. `Observe`,
  `Reached`, `Cutover`, `Letter.Identity` and `RedriveSpec.Of` are S3 and S4. A
  guard with no caller would be dead code; the walk is the mechanism that makes it
  impossible to add the caller without the guard. §UC-143's `min` over an empty
  cover — the origin or `MaxUint64`, and the second is a cutover on no evidence —
  is named in §UC-195's *Must not*, so S4 writes the refusal rather than the fold.

---

### GAP-5 [high][immediate] Three documents contradict the code this section shipped, and the flow that maps `pass.go` states a count that is now wrong

- **Where:** `docs/ai/flows/FL-038-a-settled-cursor-becomes-a-durable-checkpoint.md:100-104`;
  `docs/modules/en/projection.md:87-93`; `docs/modules/ru/projection.md:90-98`;
  `docs/ai/flows/Index.md:527-535`; `event/projection/pass.go:581-587` (the third check);
  `docs/release-notes/` (one file, `v0.1.0.md`, untouched).

- **What:** four separate drifts, all created by this section and none closed by it.

  1. **FL-038 step 4 says *"`InUnit`'s **two** per-pass checks"*** and enumerates them:
     `Tracker.Transaction` must answer a valid authority, and `crud.ExecutorFor` must find an
     executor `crud.IsTransaction` accepts. There are now **three**, and the third — the
     `authority.Same(mine)` comparison at `pass.go:585` — is the whole of ES-01's residual work
     and the one clause of ES-01 the study called *"asserted rather than held"*. The flow is
     the map an agent reads before editing `pass.go`; it now maps a two-step check onto a
     three-step function.

  2. **Both module pages say the framework cannot see the thing it now refuses.**
     `en:89-91`: *"A handler that writes anywhere else — a second database, a document store, a
     search index — is outside that transaction, **no check in this framework can see it**"*.
     `ru:93-95` says the same. That sentence is now half true: a `Destination` that resolves to
     a *different* transaction is seen, refused with `ErrSpec` and halted, per pass; only a
     handler writing past an aligned `Destination` is unseen. A consumer reading the page cannot
     tell which half they are in, and the page is the only place they would look.

  3. **A behaviour change with no release note.** A shipped `Advance: InUnit` spec whose `Unit`
     binds one transaction for the checkpoint store and another for `Destination` was accepted
     before this section and halts after it. That is the intended outcome (§INV-083) and it is
     *correct*; it is also a wiring some deployment is running today, and there is no note, no
     doc line and no migration sentence anywhere in the tree. `docs/release-notes/` holds one
     file and `git status --porcelain docs/release-notes/` is empty.

  4. **The reverse index does not list the four new files.** `docs/ai/flows/Index.md:527-535`
     maps nine `event/projection/` files to FL-038 and does not carry `identity.go`,
     `partition.go`, `cover.go` or `sequence.go`. `CLAUDE.md` on this file: *"An index that does
     not list a file is worse than a missing file — an agent trusts the index and stops
     looking."*

- **Why this severity:** `CLAUDE.md` is binding and states the standard itself: *"Documentation
  drift is a defect here, not untidiness … Treat a stale doc exactly like a failing test"* and
  *"Update in the same change as the code. Never 'later'."* Item 2 is the one that costs a
  consumer money: they read that no check can see a divergent destination, deploy the two-pool
  wiring the page implicitly blesses, and their projection halts in production with a message
  about two commits they were told the framework could not detect. Item 1 is the one that costs
  the next agent a session: `FL-038` is exactly what `CLAUDE.md`'s lookup order sends them to
  before touching `pass.go`, and it will tell them there are two checks.

- **Why this timing:** the plan schedules docs for S6 (`EVENTSOURCE_P4_PLAN.md:2122-2135`),
  which is five sections and an unknown number of days away, and `CLAUDE.md` outranks the plan.
  The four edits are minutes of work; the risk they carry is a consumer on a wiring that
  changed under them with nothing to read. Items 1 and 2 in particular are not "documentation
  the phase will produce" — they are existing sentences that this section made false.

- **Close criteria:**
  - [ ] `FL-038` step 4 says three checks and names the third, and the flow's file table gains
        the four new files.
  - [ ] `docs/modules/en/projection.md` and `docs/modules/ru/projection.md` state, in the same
        paragraph, which divergence the framework now refuses and which it still cannot see, and
        name the halt a two-transaction unit takes.
  - [ ] `docs/ai/flows/Index.md`'s reverse index carries `event/projection/identity.go`,
        `partition.go`, `cover.go` and `sequence.go`.
  - [ ] A release note records the `InUnit` behaviour change with the wiring that used to be
        accepted and the two remedies (align the unit, or say `Unchecked`).
  - [ ] `scripts/docs_test.go` gains an arm that fails when a non-test file under
        `event/projection` is absent from the flow reverse index, so the fourth item cannot
        recur silently.

- **Status: CLOSED — all four drifts, in the same change as the code.**

  1. [x] `FL-038` step 4 says **three** checks and names the third
        (`event.NewAuthority(Tracker.Backing(), crud.KeyOf(executor))` compared
        `Same`), with the sentence about what it tells apart. Step 1 gained
        `Projection.unclaimed` and step 2 gained `Projection.matching`; step 5's
        `Applied` clause now says *the envelopes this partition matched* and says
        why `Highest` is the read page's.
  2. [x] Both module pages replace *"no check in this framework can see it"* with
        the two divergences named apart — a `Destination` resolving to a different
        transaction is **seen and refused**, a handler writing past an aligned one
        is not — and both carry the halt a two-transaction unit now takes, as a
        block quote with its two remedies. Both also gained a seventh contract row
        for partitions, sequences and the topology refusal, because this section
        changed what the package can do and `CLAUDE.md` requires that page to move
        with it.
  3. [x] `docs/release-notes/v0.1.0.md` records the `InUnit` behaviour change with
        the wiring that used to be accepted and the two remedies, and a second
        note for the partition filter and the resume-time refusal.
  4. [x] `docs/ai/flows/Index.md`'s reverse index carries `identity.go`,
        `partition.go`, `cover.go` and `sequence.go`, and `FL-038`'s own file
        table gained a row for each with its symbols.
  - [x] `scripts/docs_test.go` gained
        `TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex`, which was
        **red before the fix** — it named all four missing files — and which is
        what makes item 4 unable to recur silently.

---

## What was verified and found sound

Counted and driven, not eyeballed.

**The section's own tests are load-bearing.** Three breakages, each restored and each confirmed
restored by `./scripts/checks.sh event-kernel` (`ok`):

| Break | Test | Result |
|---|---|---|
| `if false && !authority.Same(mine)` in `checkUnit` | `TestTwoTransactionsUnderOneUnitAreRefusedAndOneIsAccepted` | **FAIL** — *"the projection never published a state where the projection halted: it published [draining draining following]"* |
| `NewCover`'s overlap test → `left.String() == right.String()` | `TestACoverWithAnOverlapIsRefusedByMaskArithmeticAndNotByName` | **FAIL** — the refusal came back as the *gap* message, so the assertion on the two member names is what discriminates |
| `ByStream()` → `envelope.Stream.String()` (P-1's wrongness, restored) | `TestEverySequencerIsTotalPureAndStable` | **FAIL** twice — *"3 distinct keys over 6 envelopes where 5 were expected"* and *"two streams of one family answered the key `[stream orders]`"* |

**The alignment comparison is proved live, not only in the fabricated stand.** Inverting
`!authority.Same(mine)` to `authority.Same(mine)` and running the tagged suite against
PostgreSQL 17.9 turned `TestAProjectionInAUnitPassesABurntGap` red
(`event/eventpg/projection_integration_test.go:609`). That test's wiring is
`crud.InNewTx(ctx, this.source, work)` with `spec.Destination = this.source`
(`projection_integration_test.go:257-260`), so `crud.KeyOf` on a real `crudsql` executor and
`eventpg.Checkpoints.Transaction`'s `event.NewAuthority(this.backing, tx)` do resolve to one
`*sql.Tx` and do compare `Same` against live PostgreSQL. The full tagged suite is green
**twice in a row**: `ok github.com/frostgrove/vv/event/eventpg 104.556s` and `104.885s` under
`-race -count=1`.

**Arithmetic.** `Partition.Split` is Axon's `Segment.split` with the `mask == 0` special case
correctly elided (`0 + (1^0) == 1`); the ceiling refuses at `mask+1 > 1024` without wrapping and
`NewPartition` refuses `mask & (mask+1) != 0`, `mask+1 > MaxPartitions` and `id > mask`, so
`Partition{id:5, mask:0}` and `orders#3.2047` are both unconstructible by every exported route.
`NewCover`'s overlap predicate `(idA^idB) & min(maskA,maskB) == 0` was checked by hand against
`{0,1}`⊃`{2,3}` (overlap, correct) and `{1,1}`/`{2,3}` (disjoint, correct); with no overlap the
shares are disjoint so `share != MaxPartitions` implies a gap, and `gapIn` names it at the
set's own finest mask (`{2,3}` for the §UC-186 set — asserted by the test).

**Injectivity** holds by construction — the projection is free of both delimiters, the
generation renders iff non-zero and without leading zeros, the partition renders iff `mask != 0`
in canonical `id.mask` — and the test walks 4×5×9 = 180 identities plus 17 strings `String`
never produces, all refused.

**Hash distribution**, measured over `event.Compose("orders", key)` for three realistic key
shapes at masks 1 and 3, is even; FNV-1a's weak low bit does not bite here:

```
orders/<n>                mask=1 -> map[0:2000 1:2000]     mask=3 -> map[0:1001 1:999 2:999 3:1001]
ORD-<even, zero padded>   mask=1 -> map[0:2000 1:2000]     mask=3 -> map[0:1000 1:1000 2:1000 3:1000]
uuid-ish                  mask=3 -> map[0:1005 1:999 2:995 3:1001]
```

**Ordering of the alignment check.** `checkUnit` is called from `insideAUnit`
(`pass.go:158-166`) inside `spec.Unit`'s body, once, before `claimed`, which is before both
`applyPage` and `presentSave` on either applier. `outsideAUnit` (`pass.go:124-137`) does not
call it, so `AfterApply` makes no comparison — asserted by the control. §UC-131's *"must not be
made once and cached across passes"* is pinned by the two-pass step trace
`[unit opens, compared, applied, unit closes] ×2`.

**Refusal collection.** `New`'s `ErrDeclaration` suppression is sound in both directions:
`event.Track` checks `nilByAnyRoute(checkpoints)` **before** `checkName`
(`event/checkpoint.go:90-97`) and answers `ErrWrongStore` for it, so a nil store is never
suppressed by a bad name; and `checkText("")` answers *"is empty"*, so the suppressed error is
always the duplicate the identity already reported. §INV-104's "three problems" case is exercised
and answers exactly 3.

**Binding decisions.** No contradiction found. [[D-092]]: `grep` for a `go` statement in every
non-test file under `event/projection` returns nothing, and `scripts/` is green including
`TestTheProjectionStartsNothingAndReadsNoEnvironment`. [[D-129]]: no `Merge`, no
`Position → Cursor`, no cursor comparison; `docs/api/surface.md` gained nothing taking two
cursors. [[D-126]]: nothing added reads a clock — `hash`, `Matches`, `Split`, `NewCover` and
`SequenceOf` are all pure. [[D-128]], [[D-130]], [[D-133]], [[D-118]]: untouched by this
section. `MaxPartitions = 1024` is [SPEC] §5.2's number and refuses loudly rather than answering
wrongly.

**The kernel boundary held.** `git status --porcelain event/` lists nine `event/projection`
files and nothing else; no `^event/[a-z_]*\.go$` path appears in the moved set; the seventeen
moved paths are all inside the section's declared allowed set; `docs/api/surface.md`'s `event`
section is byte-identical to `.git/event_surface_before_p4` and the only section that moved is
`event/projection`, which gained exactly `MaxPartitions`, `ErrTopology`, `Cover`/`NewCover`,
`Generation`/`Ungenerated`, `Identity`/`NewIdentity`/`ParseIdentity`,
`Partition`/`NewPartition`/`ParsePartition`/`Whole`, and
`Sequencer`/`ByStream`/`OneSequence`/`SequenceBy`/`Unordered` — nothing else, no silent extra
surface.

**Interleavings this section makes possible, driven.** A claim expiring mid-batch, a partition
count change with events in flight, a parked sequence receiving a later event, a cutover with a
reader mid-query and a rebuild beside a live generation all require `Redriver`, `Split`, `Park`
and `Cutover`, none of which exists yet — they belong to S2, S3 and S4 and are not reviewable
here. The two this section *does* make possible were driven and are GAP-1 and GAP-2 above: two
partitioned runners over one log (every event applied twice, both rows healthy) and a
partitioned set started beside a live whole-space row (the whole log replayed, three rows
healthy).

**Medium and low findings** are recorded in
[`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P4` items 29–31 and are left alone.
Backlog items 9, 10, 12, 16 and 24 already cover the `Partition.Count` modulus invitation, the
two `Whole`s, the two `Split`s, `Sequencer.Name()`'s validity rule and the alignment refusal's
three-outcomes-one-message problem; none of them is re-raised here.
