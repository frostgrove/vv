# EVENTSOURCE_P4 — the projector subsystem (ES-01, ES-02, ES-03, ES-04, ES-06) — GAPS

## Round 1 — spec audit (coverage / invariants / DX) — 2026-09-09

Audited: [`EVENTSOURCE_P4_USECASES.md`](../usecases/EVENTSOURCE_P4_USECASES.md) against
[`EVENTSOURCE_P4_STUDY.md`](../usecases/EVENTSOURCE_P4_STUDY.md), the ES-01/02/03/04/06 appendices
(`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md:774-832`, read in Russian),
[`EVENTSOURCE_REFERENCE.md`](EVENTSOURCE_REFERENCE.md), and the binding decisions
[[D-092]] [[D-118]] [[D-125]] [[D-126]] [[D-128]] [[D-129]] [[D-130]] [[D-131]] [[D-133]].

**Verdict: red at round 1; all 20 blocking findings closed on 2026-09-09** — see
each finding's `Status`. Four `[critical]` and sixteen `[high]`, of which twelve
were closed by a change of shape (a `Cover` type, an opaque `Identity`, a park
keyed by `Identity.Whole()`, a barrier `Cutover` derives rather than accepts, a
`Claim` instead of a read, `Stage` instead of `Dispatch`) and three by a refusal
that removes the ambiguous arm entirely (`Split` on an absent parent,
`ParkSequence` outside tier A, `@`/`#` in a projection name). None was closed by
silence, and GAP-6's "require `Generations` beside every `Effects`" and GAP-9's
"escape the delimiter" are recorded as rejected with their reasons in the spec.

**Original verdict, retained.** 20 blocking findings. Four are `[critical]`: three of them are cross-mechanism
holes at the intersections this phase's five items create (park × split, park × effects, cutover ×
barrier provenance), and one is a contract that actively directs a consumer's payment call into a
transaction that rolls back. The document is strong on each mechanism read in isolation and weak at
every point where two of them meet — which is exactly where the appendices' part 3 lives.

**Universality:** no spec-level hardcode found. `orders@2#3.7`, `A1 A2 B1 A3 B2`,
`OrderCreated`/`OrderPaid`, `Spec.Pace: 200ms` and the 1024/1024 park bounds are all fixture
parameters of rules stated generally, and §1.4 explicitly assigns the numbers to the implementation
rather than to the contract. FNV-1a/32 and `MaxPartitions = 1024` are published wire constants with
a stated reason ([[D-125]]), not fits to one example.

---

### GAP-1 [critical][immediate] A split orphans the parent's parked letters, so a supported topology change silently ends the causal-order guarantee

- **Where:** §1.3 ("The handoff, in five steps"; "What `Split` does with `Progress`"), §1.4
  ("**A sequence, not an event**"; "The existence check"), §5.2 `SplitSpec`/`Split`, §UC-138,
  §INV-090.
- **What:** The park is keyed by the recorded name — §1.5: *"its **park** is its own, because the
  park is keyed by the same recorded name"* — and a split retires the parent's name and creates two
  new ones. The five-step handoff moves the **cursor** and the **counters** and says nothing about
  the letters. The spec resolves the park × generation interaction explicitly ("The park and the
  rebuild, resolved by construction") and never asks the same question of park × split.
  After a split, every letter parked under `orders#1.1` is unreachable from `orders#1.3` and
  `orders#3.3`: their `Park.Sequences` answers **0**, so by §1.4's own fast path `Holds` is *never
  called*, and the first later event of a parked sequence is handed straight to the handler.
- **Why this severity:** Concrete interleaving. Sequence `A` fails permanently at `A2`; `A2 A3` are
  parked under `orders#1.1`; `Quarantined` is 2. An operator splits the partition (a supported,
  documented operation, and §1.3 makes it the *only* topology change there is). Child `orders#1.3`
  inherits `A`'s key space, holds zero parked sequences, and applies `A4 A5 A6 …` over a read model
  that never received `A2` — `OrderPaid` over an order whose `OrderCreated` was parked, which
  §1.4 names as *"the whole reason ES-03 exists"*. Then a redrive of the orphaned sequence applies
  `A2` **after** `A4..A6` are already applied, which corrupts the read model in the other direction.
  No error on any path; the only trace is a `Quarantined` counter the lower child inherited.
  §INV-090's statement — *"every later envelope of the same sequence … in this page and in every
  later page, until the sequence drains — are parked without being applied"* — is falsified by a
  first-class operation of the same phase.
- **Why this timing:** It is a cross-mechanism hole between two in-scope items whose answer changes
  the `Park` contract (letters must be re-keyable, or `Split` must refuse a parent holding letters,
  or the park must be keyed by something a split does not move). All three are contract changes, not
  implementation details, and §5.2's `Park`/`SplitSpec` freeze the shapes.
- **Close criteria:**
  - [ ] The spec states what a split does with the parent's parked letters — refuse the split while
        the park is non-empty, re-key the letters inside the same transaction, or key the park by
        something the split does not change — and gives the reason the other two were rejected.
  - [ ] §INV-090 gains "across a split" to its scope, or a stated exclusion with its consequence.
  - [ ] A use case: a partition holding a parked sequence is split; the following events of that
        sequence must still not reach a handler. **Control:** the same split with an empty park
        changes nothing.
  - [ ] The redrive-after-split case is named: which identity a `RedriveSpec` must carry for an
        orphaned letter, and what refuses the wrong one.
- **Status:** closed — resolved by a change of shape. §1.4 gains "The park is keyed by the projection and the generation, and never by the partition": a `Park`/`Redriver` is handed `Identity.Whole()` and nothing else, so a split moves no park key, needs no re-keying transaction and is not refused. A redrive is generation-wide; a `RedriveSpec` carrying a partitioned identity is `ErrTopology`. The cost — a partition pays `Holds` while any partition of its generation holds a letter — is stated with the argument that it never costs correctness (a sequence belongs to one live partition, and only a split moves it, whose children are new runners and therefore resume). §INV-090 gains "across a split", §INV-097 states the key, §UC-179 is the case with the empty-park control, and §UC-189 is the two-generations-one-park pair.

---

### GAP-2 [critical][immediate] `Cutover` takes the barrier as a caller-supplied value it cannot verify, so `Barrier{At: 0}` points every read at an empty table

- **Where:** §1.5 ("Readiness: a barrier is a `Position`"), §5.2 ES-04 (`Observe`, `Reached`,
  `CutoverSpec.Barrier`, `Cutover`), §UC-159, §UC-161, §INV-098.
- **What:** `CutoverSpec` carries `Barrier Barrier` and `Over []Partition` (the arriving
  generation's). The retiring generation's partition set is **not** a parameter, so `Cutover`
  cannot re-derive the barrier — it can only re-run `Reached` against whatever `Barrier` value it
  was handed. `Barrier.At` is an ordinary `event.Position` field on an exported struct.
- **Why this severity:** `Cutover(ctx, CutoverSpec{From: 1, To: 2, Barrier: Barrier{}, …})`
  compiles, runs, returns nil and switches every read to generation 2 — whose tables may be empty,
  because `Reached` against a zero barrier is trivially true and `Quarantined` on a generation that
  applied nothing is zero. §UC-159's own control blesses the zero barrier (*"A barrier observed from
  a **fresh** generation is position zero and `Reached` is immediately true"*), so the one case that
  would have caught it asserts the opposite. The same failure arrives by a second door: `Observe`
  takes `over []Partition` from the caller, and a caller who passes a partition that has no row
  gets `Fresh()` → `Highest 0` → **barrier 0** from a fully-live generation. This is the naive
  contract for the whole of ES-04 — no error, no warning, and the symptom is a site serving an empty
  read model.
- **Why this timing:** It is the shape of the public API. Fixing it means `Cutover` observing the
  barrier itself from the retiring generation (which needs its partition set in the spec) or
  `Barrier` becoming unforgeable; both change the exported surface §5.2 freezes and §7.2's D-134/135
  will record.
- **Close criteria:**
  - [ ] `Cutover` observes the barrier from the retiring generation inside the same unit, or
        `Barrier` carries provenance `Cutover` verifies (`Projection`, `From`, and a value no caller
        can synthesise), and the spec says which.
  - [ ] A use case: a cutover attempted with a barrier the caller invented is refused, naming the
        field. **Control:** the barrier `Observe` answers proceeds.
  - [ ] `Observe` refuses (or `Readiness` reports) a partition set in which any member has no
        checkpoint row, rather than reading the absent row as position zero.
  - [ ] §UC-159's fresh-generation control is rewritten so that "reached at once" is not the
        certified behaviour of a barrier of zero against a live generation.
- **Status:** closed — `Barrier` is removed from `CutoverSpec` entirely. `Cutover` observes the barrier itself from the retiring generation's rows inside the same unit (§1.5 "A barrier a caller can invent is not evidence"), so `CutoverSpec` carries `Retiring Cover` and `Arriving Cover` and there is no field through which a zero can be handed in. `Observe` no longer reads an absent row as position zero: all-fresh answers the origin (true — nothing was delivered), mixed is `ErrTopology` naming the member. §UC-159's control is rewritten to the all-fresh case and §UC-180 is the discriminating pair. §INV-098 restated.

---

### GAP-3 [critical][immediate] The park × effect interaction is unspecified, and both readings are wrong

- **Where:** §1.4 (the dispatch loop), §1.6 (`Effect.Envelopes` — *"the page's, past the barrier and
  no others"*), §5.2 ES-03/ES-06, §UC-146, §UC-172, §INV-100, §INV-101.
- **What:** `Effect.Envelopes` is defined as the page's envelopes past `EffectsAfter` and by
  nothing else. A page in which `A2` failed permanently and `A3` was parked without reaching the
  handler still contains `A2` and `A3`. The spec never says whether they are dispatched. Nor does
  the redrive path carry an `Effects`: `RedriveSpec` has `Handler`, `Sequencer`, `Park Redriver`,
  `Unit`, `Destination`, `Classifier` — and no effect sink at all.
- **Why this severity:** Both branches are production defects and the spec picks neither.
  *(a)* If parked envelopes are dispatched, the framework emails a customer about an order state its
  own read model refused to apply — ES-06's stated purpose (*«replay не повторяет письма и
  платежи»*) inverted, and the effect fires for an event that will later be redriven and dispatched
  again by nothing.
  *(b)* If they are not dispatched, the effect for every parked event is **lost for ever**: the loop
  will never see those envelopes again (the checkpoint advanced over them), and the redrive has no
  capability to dispatch. A permanently-failing `Park` policy therefore silently drops an unbounded
  set of live effects, with `Quarantined` — a *read-model* completeness counter — as the only trace.
  §INV-101 claims the effect gate is "durable at both ends" while one of its two ends is undefined.
- **Why this timing:** It fixes either `Effect.Envelopes`' definition or `RedriveSpec`'s shape —
  both exported in §5.2 — and it belongs in D-135 (§7.2), which is written this phase.
- **Close criteria:**
  - [ ] The spec states whether a parked envelope is dispatched, with the reason the other answer
        was rejected.
  - [ ] If it is not, the spec states where a parked event's effect goes on a successful redrive, or
        records the permanent loss out loud in the `Park` contract and on the module page.
  - [ ] A use case with a page in which one envelope is parked and one is applied, asserting exactly
        which reach `Dispatch`. **Control:** the same page with nothing parked dispatches both.
  - [ ] §INV-101's statement names the park as one of the conditions the gate is decided under.
- **Status:** closed — decided, with the rejected reading argued. §1.6 "An effect follows its envelope, including into the park": `Effect.Envelopes` is the envelopes this page **applied** past the barrier, so a parked one is not staged; the letter carries its effect, and `RedriveSpec` gains `Effects`, `EffectsAfter` and `Generations` so the redrive that applies a letter stages it in the same transaction. An eviction stages nothing, ever, and that is on the module page. §UC-181, §UC-182, §INV-101 (three durable conditions, park named) and the new §INV-106.

---

### GAP-4 [critical][immediate] The spec routes a dial-out handler into `Spec.Effects`, which is dispatched inside a unit that rolls back — and ES-02's «Внешний HTTP такой fence не защищает» is answered nowhere

- **Where:** §1.1 (*"a handler that dials out … is an *effect*, it belongs in `Spec.Effects`, and
  §1.6 is where that is enforced"*), §1.6 (`Effects` is `InUnit`-only; the ownership row is read
  inside the committing transaction), §5.2 `Effects`, §UC-178, §INV-100.
- **What:** `Effects.Dispatch` is called **inside** the caller's unit of work. Every rollback path
  the spec itself enumerates — a lost fence ([[D-133]] `ErrOvertaken`, which the spec expects on
  every rolling deploy), `ErrParkFull` (§1.4: *"the unit rolls back"*), a later envelope's failure,
  a serialisation failure the caller's `Unit` retries — leaves a *sent* HTTP request and an
  **unadvanced** checkpoint, so the same page is re-dispatched on the next pass. The only shape
  that is safe there is a durable stage inside the transaction ([[D-118]]), which §UC-178 shows and
  §1.6's prose permits but never *requires*. Meanwhile ES-02 part 3's closing sentence —
  *«Внешний HTTP такой fence не защищает»* — appears in no section of the spec, in no use case and
  in no invariant. The study did not carry it forward either; it is a dropped appendix sentence.
- **Why this severity:** The document's own reading path takes a consumer with an HTTP call from
  §1.1 to `Spec.Effects` and then hands them an interface whose every call site is inside a
  transaction that can roll back. The result is double-charged payments on a rolling deploy — with
  every check in the tree green, because the framework did exactly what it promised.
- **Why this timing:** It is the contract on the `Effects` type, which §7.2's D-135 records this
  phase, and the correction changes the interface's stated obligation (and probably its name).
- **Close criteria:**
  - [ ] The `Effects` contract states that `Dispatch` must make a durable write inside the unit and
        must not perform an externally-visible irreversible action, with the rollback paths named.
  - [ ] §1.1's sentence stops routing a dial-out handler into `Spec.Effects` and routes it to the
        drain of the stage instead.
  - [ ] ES-02's *«Внешний HTTP такой fence не защищает»* is answered in writing, in the section that
        owns the fence, and the module page carries it beside the `AfterApply` outbox asymmetry the
        reference's Reject 2 already owes.
  - [ ] A use case: a dispatch followed by a lost fence leaves the stage rolled back with the
        advance. **Control:** the same dispatch on a committing pass leaves both.
- **Status:** closed — the contract and the method name both change. `Effects.Dispatch` becomes `Effects.Stage`, contracted to make a durable write inside the unit and to perform no externally visible irreversible action, with the four rollback paths named on the type. §1.1 stops routing a dial-out into `Spec.Effects` and routes it to the drain of the stage, and answers ES-02's «Внешний HTTP такой fence не защищает» in writing — the fence protects a write that rolls back with it, an HTTP call is not one, and a lost fence is the expected outcome of a rolling deploy. §UC-183 is the lost-fence case with its committing control; §UC-178 rewritten; §INV-100 carries the staging clause.

---

### GAP-5 [high][immediate] §UC-169's control is unsatisfiable — it contradicts §UC-173 and §1.6

- **Where:** §UC-169 **Control**, against §1.6 item 2 and §UC-173.
- **What:** §UC-169's control is *"Generation 2 restarted with `Effects` set **does** dispatch, so
  the suppression is attributable to the field and not to the generation number."* §1.6 requires
  `Generations` beside `Effects` whenever `Spec.Generation != Ungenerated`, and §UC-173 makes the
  ownership row the thing that decides. Generation 2 is not the active generation in §UC-169's
  fixture (generation 1 is live and dispatching), so with `Generations` wired the control dispatches
  **nothing**, and without it `New` refuses the spec. The control cannot pass as written.
- **Why this severity:** It is the falsifier §INV-100 names, and it is the one case that would tell
  "suppressed because `Effects` is nil" apart from "suppressed because the ownership row says
  someone else". If it is implemented as written it will be made to pass by weakening the ownership
  gate — which is the strongest thing this phase claims to contribute.
- **Why this timing:** Two use cases in one document disagreeing about what the primary ES-06
  mechanism does; the plan will implement one of them.
- **Close criteria:**
  - [ ] §UC-169's control is rewritten so it is satisfiable — e.g. `Ungenerated` on both sides, or
        the ownership row flipped first — and states which of the two suppressors it isolates.
  - [ ] The spec states the *precedence* of the two suppressors (`Effects: nil`, the ownership row,
        `EffectsAfter`) and which one a `State` or an error names when a dispatch does not happen.
- **Status:** closed — §UC-169's control is now "flip the ownership row to 2, restart generation 2 with `Effects` set: it stages, and generation 1, still holding `Effects`, stages nothing", which isolates the two suppressors instead of contradicting §UC-173. §1.6 gains "Which suppressor suppressed, and in what order": `Effects: nil`, then `EffectsAfter` per envelope, then the ownership row per pass — cheapest first — and states that a suppression is never an error or a phase, because it is the normal state of every rebuild, with the ownership row itself as the operator's evidence.

---

### GAP-6 [high][immediate] A rebuild by name — the shape this phase keeps and cites — reaches the effect capability with no gate at all

- **Where:** §1.5 (*"This is already proven: `TestARebuiltProjectionDrainsBesideTheLiveOneAndTheRowsAgree`
  … Phase 4 does not rebuild that"*), §1.6 (*"A rebuild is a spec with `Effects` nil"*; `Generations`
  required only when `Generation != Ungenerated`), §UC-158, §INV-100.
- **What:** The spec keeps UC-120's shipped rebuild — a second `Spec.Name` over one log — as the
  proven base and layers generations on top of it without deprecating it. A rebuild spelled that
  way has `Generation == Ungenerated`, so `Generations` is not required, the ownership row is never
  read, and `Effects` set on it dispatches **every historical event**. §1.6's "a rebuild is a spec
  with `Effects` nil" is a definition, not a refusal; nothing in §5.2 or §4 makes it checkable.
- **Why this severity:** The appendix clause is *«rebuild не получает effect-dispatch capability»*,
  and the phase's answer holds only for rebuilds spelled the new way. The old spelling is the one on
  the current module page and in the shipped live test, so it is the one a consumer copies. The
  failure is the one ES-06 exists to prevent, at full historical volume.
- **Why this timing:** It is a hole in the phase's headline guarantee, and closing it is either a
  refusal at `New` or a documented deprecation — both contract text.
- **Close criteria:**
  - [ ] The spec says what a rebuild-by-name with `Effects` set does, and either refuses it, or
        requires `Generations` whenever `Effects` is set (not only when `Generation` is non-zero),
        or records in writing that the gate does not cover that spelling.
  - [ ] §INV-100's statement is falsifiable for both spellings, or names the one it covers.
  - [ ] The module page's rebuild recipe is updated in the same change, since it currently teaches
        the ungated shape.
- **Status:** closed by a written refusal plus a measured edge, and the alternative is argued down rather than ignored. §1.5 "The rebuild-by-name spelling, and the exact edge of the guarantee": the framework cannot distinguish a second `Spec.Name` from any other projection, and requiring `Generations` beside every `Effects` was rejected because it taxes every single-sender projection and would still pass this case (the second name's own row answers `Ungenerated`). So the recipe changes to `Spec.Generation` (§7.3, module page), the edge is stated, and §UC-184 asserts what the old spelling actually does, in §UC-160's boundary-measuring shape. §INV-100 names the spelling it covers.

---

### GAP-7 [high][immediate] `Split` reads an absent parent row as `Fresh()`, which is precisely [[D-133]]'s halt case

- **Where:** §1.3 ("Splitting a **fresh** parent (no row, `Checkpoint.Fresh()`) writes nothing at
  all: both children start at the origin"), §UC-140, §INV-087.
- **What:** `Split` has one arm for "the parent has no checkpoint row" and treats it as the origin.
  A row can be absent for two reasons and the spec conflates them: the partition never ran, **or**
  the name was forgotten, restored, retired by a previous split, or dropped by an operator.
  [[D-133]]'s table gives that exact input one answer — *"no row at all, or one **behind** the
  fence → the name was forgotten, reset or restored under a running projection → **halt**"* — and
  its stated reason applies here word for word: *"creating a fresh row at advance 1 would leave the
  read model holding events no checkpoint accounts for."*
- **Why this severity:** A partition at position 5 000 000 whose row is lost (an operator `Forget`,
  a partial restore, a second `Split` after an ambiguous commit) is split; `Split` answers success;
  the host starts two children; both walk the **whole log from the origin** into a live read model.
  The loop halts loudly on the same input; `Split` restarts silently. §UC-140's **Must not** worries
  only about writing an empty cursor at advance 1 and misses the case entirely.
- **Why this timing:** It is `Split`'s exported contract and the one operation that creates
  checkpoint rows outside the loop.
- **Close criteria:**
  - [ ] `Split` distinguishes "no parent row and no child rows" from "no parent row", by loading the
        two child identities before it decides, and refuses the second with `ErrTopology`.
  - [ ] The spec states `Split`'s behaviour when it is called twice — the ambiguous-commit case —
        and whether the second call is a refusal or a no-op.
  - [ ] §UC-140 gains the second arm and a control: a split of a *forgotten* parent writes nothing
        and refuses; a split of a genuinely fresh one answers the children.
- **Status:** closed — the fresh arm is removed. §1.3 "A parent with no row is refused, because the framework cannot tell which absence it is" quotes [[D-133]] back and refuses with `ErrTopology`, naming both readings and both remedies; the same load also refuses a child row beside a live parent, which is the ambiguous-commit case. §UC-140 rewritten to the two absences with the has-run control; §INV-087 restated.

---

### GAP-8 [high][immediate] Nothing states or checks that the live partition set covers the key space exactly once, and the spec's `validateSegment` replacement covers only half the case

- **Where:** §1.3 ("There is no stored N"; *"**This is vv's replacement for Axon's
  `validateSegment`**"*), §5.2 `Partition`, §INV-086, group AB (no use case).
- **What:** Axon's `validateSegment` detects **both** a concurrent split (the sibling row appeared)
  and a concurrent merge (the sibling row vanished), and `initializeTokenSegments` refuses to adopt
  a topology it did not create. The spec's replacement is the parent's absent-row halt, which
  detects only the case where *this* runner's own row was retired. Nothing detects the two
  topologies a host can declare wrongly:
  *(a)* **A gap** — the host starts `{0,3}`, `{1,3}` and `{3,3}` and forgets `{2,3}`. Every key in
  the missing quarter is never delivered, for ever. `Observe` answers a healthy `min` because it is
  computed over the same wrong `[]Partition` the caller passed. `Ready` is green on every runner.
  *(b)* **An overlap** — `{1,3}` and `{1,7}` are different names, so `runtime.Supervisor` admits
  both, their checkpoint rows never conflict, and every key matching both is applied twice, out of
  order with respect to itself once their cursors diverge.
- **Why this severity:** The whole point of ES-02's part 3 is that a partition topology cannot be
  changed by hand safely; the spec proves the *split* is safe and then leaves the *set* unverified,
  which is the study's own warning (*"vv is currently safe by absence, which is a different thing
  from being safe by construction"*). Case (a) is silent permanent data loss in the read model with
  no error on any path — the exact failure §1.3 says the mask exists to prevent, arriving through
  the one door the mask does not guard.
- **Why this timing:** The remedy is either an exported check (a `Cover([]Partition) error` the host
  or `Observe` runs), or a stated, documented limitation with the operator procedure that replaces
  it. Both are surface decisions.
- **Close criteria:**
  - [ ] An invariant: the live partition set is a *partition* of the key space — every key matches
        exactly one member — with how it is falsified.
  - [ ] A use case for a gap and a use case for an overlap, each asserting what the framework does.
        **Control:** a complete set applies every event exactly once.
  - [ ] The spec states whether the framework checks the declared set (and where — `Observe`,
        `Cutover`, the runner's construction) or explicitly refuses to, and if it refuses, the
        sentence appears on the module page beside `Split`.
- **Status:** closed by a type. §1.3 "The set is the thing that has to be right, so the set is a type" introduces `Cover`/`NewCover`, refusing a gap and an overlap on exact mask arithmetic, and `Observe`, `Reached` and `Cutover` take a `Cover` and never a `[]Partition`. The host builds its runners from `Cover.Partitions()`, so the checked path is the obvious one. What stays open is stated: a lone runner cannot see the set, and that sentence goes on the module page beside `Split`. §UC-186, §UC-187, §INV-105, §8 tension 7.

---

### GAP-9 [high][immediate] `Identity` is a composed key with no escaping, no delimiter refusal, and no round trip for names that are legal today

- **Where:** §1.2/§1.5 (the recorded name), §5.2 `Identity`, `ParseIdentity`, §UC-145, §INV-097.
- **What:** `Identity.String()` composes `projection @ generation # id . mask` and `ParseIdentity`
  splits it back. `Spec.Name` today is any name passing `event.Track`'s `checkName` — within
  `MaxNameBytes`, valid UTF-8, no control character, no bracket. `@`, `#` and `.` are all legal in a
  name today. So `Identity{Projection: "orders@2"}` renders `orders@2` and parses back as
  `{Projection: "orders", Generation: 2}` — not a round trip — and a consumer whose live projection
  is called `orders#3.7` or `billing@eu` silently acquires a generation or a partition, with its
  checkpoint row, its park key and its runner name all now meaning something else.
- **Why this severity:** [[D-125]] is cited by this very document for the hash and not for the name:
  *a composed key is a wire format*. An unescaped composition over an alphabet that admits its own
  delimiters is the failure that decision exists for, and here it lands on the primary key of the
  checkpoint table, the park and the generations row at once. §UC-145 asserts the round trip only
  for names it constructed.
- **Why this timing:** It is the one place a name is built, it is on the wire, and every table in
  the phase is keyed by it.
- **Close criteria:**
  - [ ] `Identity` refuses `@`, `#` (and any other delimiter it uses) in `Projection`, with a named
        error, or the spec states the escaping rule.
  - [ ] §UC-145 gains the adversarial case: a projection legally named `orders@2` today, asserting
        the refusal and naming the migration for a deployment that already has one.
  - [ ] §INV-097 states the injectivity property — two distinct `Identity` values never render the
        same string — and how it is falsified.
- **Status:** closed — `Identity` becomes opaque (`NewIdentity`, `ParseIdentity`, accessors), and `NewIdentity` refuses `@` and `#` in a projection name with a named error collected at `New` as `ErrSpec`. Escaping was considered and rejected in writing: it would change the rendered name of a projection that is legal today, moving its checkpoint row and restarting it at the origin — the silent version of the failure the refusal makes loud at boot. §UC-188 is the adversarial case with the `orders.v2` control and the migration; §INV-097 states injectivity.

---

### GAP-10 [high][immediate] `Park`, `Redriver` and `Generations` take a bare `projection string` and the spec never says which string it is

- **Where:** §5.2 ES-03 (`Park.Sequences(ctx, projection string)`, `Park.Holds(ctx, projection,
  sequence string)`), `Redriver.Oldest/Sequence(ctx, projection …)`, ES-04
  (`Generations.Active/Activate(ctx, projection string, …)`), against §1.5 (*"its park is its own,
  because the park is keyed by the same recorded name"*) and `Letter.Identity Identity`.
- **What:** Three application-implemented interfaces are keyed by a `string` named `projection`,
  which in the shipped `Quarantines` means `Spec.Name`. §1.5 says the park is keyed by the
  **recorded name**, i.e. `Identity.String()`. `Letter` then carries a whole `Identity`, so an
  implementer has two plausible keys in front of them and the document never picks one. For
  `Generations` the answer must be the *bare* projection name (one ownership row per projection, not
  per generation) — the opposite choice from `Park` — and that is also never stated.
- **Why this severity:** An implementer who keys the park table on `Letter.Identity.Projection` (a
  field with that name, on the value they are handed) writes code that compiles, passes every
  single-generation test, and then shares one park across generations: the live generation's parked
  sequences block the **rebuild**, which parks events it never failed on, ends with `Quarantined`
  non-zero, and can never be cut over — while §INV-097 asserts that a generation's park is its own.
  The reciprocal error on `Generations` (keying by the rendered identity) makes `Active` answer per
  partition, so a cutover moves one partition's effects and not the others'.
- **Why this timing:** Three public interfaces; the ambiguity is designed into their signatures.
- **Close criteria:**
  - [ ] Each of the three parameters is typed or documented so only one key is possible — an
        `Identity` where the recorded name is meant, a `string` where the bare projection is meant —
        and the asymmetry between `Park` and `Generations` is stated with its reason.
  - [ ] A use case: two generations of one projection over one `Park` implementation; the older
        generation's letters do not block the newer one. **Control:** two partitions of one
        generation share nothing either.
- **Status:** closed by typing rather than by documenting. `Park` and `Redriver` take an `Identity` — always `Identity.Whole()`, which the framework is the only producer of — so an implementation keying on the value it is handed is correct by construction and there is no second candidate. `Generations` keeps a bare projection name with the reason stated on the type: one row per projection across all generations, because the row names the winner and a row per generation could not. §UC-189 is the two-generations case; its control states the deliberate opposite for two partitions.

---

### GAP-11 [high][immediate] The `Quarantine → Park` rename collides with the `Park` interface at package scope: the surface in §5.2 does not compile

- **Where:** §5.2 "Renames" (`Quarantines → Park`; `Failure`'s `Quarantine → Park`), §5.2 ES-03
  (`type Park interface`), §UC-146/§UC-175 (`OnPermanentFailure: Park`), §7.3.
- **What:** Phase 3 ships `type Failure uint8` with package-level constants `Halt` and `Quarantine`,
  and `type Quarantines interface`. The rename table turns **both** into `Park`: a package-level
  constant `Park Failure` and a package-level `type Park interface` cannot coexist in one Go
  package. Every use case in group AC is written against the colliding spelling
  (`OnPermanentFailure: Park` beside `Spec.Park Park`).
- **Why this severity:** The exported surface §5.2 declares as "what `make api` must show" cannot be
  built. It is cheap now and expensive after the plan writes it: the third name will be chosen under
  time pressure and will end up in the module page, the surface baseline and every use case above.
- **Why this timing:** §5.2 is the contract the next section implements, and the rename is a
  breaking change a consumer meets as a compile error (§7.3 says so).
- **Close criteria:**
  - [ ] The three renamed names are spelled so they coexist (e.g. `Park` the interface,
        `ParkFailure`/`OnFailurePark` the verdict) and every use case, invariant and §5.2 block uses
        the chosen spelling consistently.
  - [ ] §7.3's rename note lists all three old→new pairs with the final spellings.
- **Status:** closed — the three renames are respelled so they coexist: `Quarantines` → `Park` (interface), `Quarantined` → `Letter` (struct), `Failure`'s `Quarantine` → **`ParkSequence`** (constant). `Spec.Quarantine` becomes the field `Spec.Park`, which collides with nothing. Every use case, invariant and §5.2 block uses the chosen spelling, and §7.3 lists all three pairs as compile errors a consumer meets.

---

### GAP-12 [high][immediate] §INV-090 states the causal-order guarantee unconditionally while §1.4 gives it away in one sentence under `AfterApply` and `Unchecked`

- **Where:** §INV-090 (statement), against §1.4 ("The redrive and the loop cannot both apply one
  letter … Under `AfterApply`, or with `Unchecked`, they do not, and the contract says so rather
  than pretending"), §INV-091, §UC-152, §UC-153.
- **What:** §INV-090 is written as a property of the mechanism with no mode named. §1.4 states, in
  passing, that the serialisation the property rests on exists only under `InUnit` with an aligned
  destination. The interleaving is concrete: the redrive evicts `A`'s last letter and commits; the
  loop, mid-page under `AfterApply`, has already read `Holds(A) = false` and applies `A9`; the
  redrive is still applying `A7`. `A9` lands before `A7`. That is the exact defect the queue exists
  to prevent, produced by two supported operations with no error on any path.
- **Why this severity:** An invariant that is false in a configuration the spec ships and does not
  refuse is worse than no invariant: the falsifier list (§UC-146, §UC-147) is all `InUnit`, so the
  suite will be green while the property does not hold for half the matrix. §1.4's one sentence is
  not a scope statement — it is buried in a paragraph about a different question.
- **Why this timing:** Either `OnPermanentFailure: Park` is refused outside `InUnit` with an aligned
  destination (a construction refusal, i.e. contract), or the invariant is scoped and the module
  page carries the exclusion. Both change §4 and §5.2.
- **Close criteria:**
  - [ ] §INV-090 names the modes it holds under, and the modes it does not are listed with the
        interleaving that breaks them.
  - [ ] The spec decides whether `Park` beside `AfterApply`/`Unchecked` is refused at construction
        or admitted with a stated weakening.
  - [ ] A use case for a concurrent redrive and loop under the weaker wiring, asserting what the
        framework does. **Control:** the same pair under tier A serialises.
- **Status:** closed by a construction refusal rather than by scoping the invariant. §1.4 "The park is tier A only": `OnPermanentFailure: ParkSequence` requires `Advance: InUnit` and a `Destination` that is not `Unchecked`, `New` refuses the rest, and tier B is already refused per pass — so a park that constructs is a park at tier A and §INV-090 holds in every shipped configuration. The rejected alternative (admit and scope) is argued down: its falsifier list would be tier A while half the matrix silently failed. A foreign destination gets `Halt`, stated as honest rather than as a limitation. §UC-190, §INV-090, §INV-091.

---

### GAP-13 [high][immediate] `ErrParkFull` under `AfterApply`: "nothing is parked" is false, and the retry re-parks the prefix on every attempt

- **Where:** §1.4 ("What overflow does"), §UC-150, §INV-092.
- **What:** The `ErrParkFull` arm is written for one mode: *"The unit rolls back, the checkpoint
  does not advance, **no envelope is skipped**, nothing is parked."* Under `AfterApply` there is no
  unit. A page in which `A2` parked successfully and `C5` hit the queue's bound leaves `A2`'s letter
  **durably in the queue**; the checkpoint does not advance; the page is re-delivered; `A2` is
  parked again (or duplicated, depending on the application's `Park`), and every retry adds letters
  to the queue whose fullness is the thing blocking the projection.
- **Why this severity:** The stated remedy — *"an operator draining the queue is the fix and the
  projection must resume by itself when they do"* — is defeated: the retry loop refills the queue
  while the operator drains it, and the retry has **no attempt budget** by design, so it never
  stops. §INV-092's "without parking" is falsified by the ordinary interleaving.
- **Why this timing:** It is the failure-table arm the plan will implement verbatim, and the fix is
  either a refusal (`Park` requires `InUnit`, see GAP-12) or a stated, different `AfterApply`
  behaviour.
- **Close criteria:**
  - [ ] The `ErrParkFull` arm states the `AfterApply` case explicitly, or `Park` is refused there.
  - [ ] §UC-150 runs its three bounds under both modes, or names the mode it is scoped to.
  - [ ] §INV-092's "nothing is parked" is qualified, and the duplicate-letter growth is either
        impossible by construction or stated as the `Park` implementer's obligation.
- **Status:** closed — dissolved by GAP-12's refusal, and said out loud rather than left implied. §1.4's overflow arm now states that "nothing is parked" is a statement about a unit and that there is always a unit, so a page whose earlier envelopes parked rolls those letters back with everything else and the budget-free retry cannot refill the queue that is blocking it. §UC-150 and §INV-092 carry the same sentence.

---

### GAP-14 [high][immediate] Two concurrent redrives process one sequence twice, out of order — the letter claim was mined and then dropped

- **Where:** §1.4 ("The redrive"), §5.2 `Redriver`, §UC-152, §UC-153, §UC-154, §INV-094; against
  study §ES-03 nuance 8 (*"The letter claim is a second time-based claim with its own duration …
  Two operators redriving at once do not process one sequence twice"*) and the source's
  `claimDeadLetter` / `processingStarted` predicate quoted at study lines 546-562.
- **What:** `Redriver` is `Oldest`, `Sequence`, `Evict`, `Touch`. `Oldest` is a **read**, not a
  claim. Two operators (or one operator and a `runtime.Runner` the host wrapped `Any` in, which
  §1.4 explicitly invites) both get the same least-recently-tried sequence, both load the same
  `[]Letter`, and both apply from the first. `Evict` is not stated to be fenced, so both succeed;
  the two units interleave and `A3` can be applied before `A2` finishes. §1.4 answers only the
  loop × redrive pair and never the redrive × redrive pair.
- **Why this severity:** The redrive is the only path that applies a letter, and the ordering it
  must preserve is the one the whole queue exists for. The source solves it with an explicit claim,
  the study recorded the mechanism in full, and the spec dropped it — which is precisely the failure
  mode the study stage exists to prevent. [[D-126]] rules out a *store's* clock, not a claim in the
  application's own table, and §1.4 already puts the rotation clock there.
- **Why this timing:** It is a method on an exported interface the application implements; adding it
  later is a breaking change to every `Redriver`.
- **Close criteria:**
  - [ ] `Redriver` gains a claim (or `Sequence` is contracted to claim, with the release path
        stated), or the spec refuses concurrency and says how it is enforced.
  - [ ] A use case: two concurrent `Redrive.Any` calls over one park; exactly one processes a
        sequence and the other takes another or answers empty. **Control:** one caller alone behaves
        as §UC-154 states.
  - [ ] §INV-094 states the mutual-exclusion property and how it is falsified.
- **Status:** closed — the mined claim is restored. `Redriver.Oldest` becomes `Claim(ctx, of, sequence)`, one method for both entry points, taking the least recently tried **unclaimed** sequence or a named one, with `Release` on every exit path and an expiry clock in the application's table ([[D-126]] rules out a store's clock, not this). §1.4 records the source's `claimDeadLetter`/`processingStarted` predicate as the reason. §UC-191 is the two-gated-callers case with the single-caller control; §INV-094 states the exclusion.

---

### GAP-15 [high][immediate] `State.Parked` and `PhaseDegraded` cannot clear — §UC-152 and §UC-156 say "next pass", §UC-148 and §1.4 say "once per resume"

- **Where:** §1.4 ("The existence check"; "The state"), §UC-148, §UC-152, §UC-156, §INV-095.
- **What:** §1.4 defines the parked count as read *"once per resume … a process start, or an
  `overtaken` that adopted another instance's row"*, raised by this loop's own parks and by nothing
  else. §UC-148 certifies exactly that (`Sequences` called exactly once per resume). But §UC-152
  says *"`State.Parked` drops to zero at the loop's next resume and the phase returns to
  `following`"* and §UC-156's control says *"reports `PhaseFollowing` at its **next pass**"* — three
  different clearing rules in one document, and the two most-cited ones are incompatible with
  §UC-148's call count.
- **Why this severity:** Under §1.4's own rule the count is monotone within a run, so a projection
  that parked one sequence and had it redriven stays `PhaseDegraded` and pays a `Holds` per envelope
  **for the life of the process**. `PhaseDegraded` then means "has parked something since this
  process started", not "has holes now" — and §INV-095's statement (*"`PhaseFollowing` is published
  only when … the live parked count is zero"*) calls it live when it is not. An operator watching
  the phase to know when a drain is complete never sees it clear.
- **Why this timing:** It decides whether `Park.Sequences` is a per-resume or per-pass call, which
  is the whole of §1.4's "a healthy projection pays nothing" argument and a hot-path property.
- **Close criteria:**
  - [ ] One clearing rule, stated once, with its cost; §UC-148, §UC-152, §UC-156 and §INV-095 all
        agree with it.
  - [ ] If the count stays per-resume, `State.Parked` is renamed or documented as "parked since this
        instance resumed", and the module page says how an operator learns the queue is empty.
- **Status:** closed — one rule, stated once, and the two rejected ones named. §1.4: `Park.Sequences` is read once per resume, and again at the start of every pass **while the count is non-zero**. A healthy projection still pays nothing per pass; a degraded one pays one call per page and clears at the next pass after a drain. §UC-148 (both ends of the count), §UC-152 ("next pass"), §UC-156's control and §INV-095 all agree, and §INV-095 now says why the count is live.

---

### GAP-16 [high][immediate] §1.1's own argument — *measured, not asserted* — is applied to `Destination` and to nothing else

- **Where:** §1.1 (tiers A/B/C, *"it is now measured rather than asserted"*), against §1.6 item 2
  (`Generations.Active` *"through the checkpoint store's ambient transaction"*), §1.4 (the park
  write in the transaction), §5.2 `RedriveSpec` (`Unit`, `Destination`, no stated check),
  §INV-083, §INV-101.
- **What:** The residual ES-01 work is the discovery that `checkUnit` proved two transactions exist
  and never that they were the same one, and the fix is a per-pass `Authority.Same` comparison.
  Three more properties of exactly that class are then introduced and left asserted:
  *(a)* `Generations.Active` must run in the committing transaction — the durable two-sender
  boundary §1.5 calls *"the strongest single thing phase 4 contributes over both sources"* — and
  nothing measures it. A `Generations` implemented over a second pool compiles and reopens the
  window silently.
  *(b)* `Park.Park`/`Holds` must ride the same transaction for §INV-091 to hold; unmeasured.
  *(c)* `RedriveSpec` carries a `Unit` and a `Destination` and the spec never says whether the tier
  check applies to a redrive at all, nor whether the handler's write and the `Evict` commit
  together. If they do not, a redrive loses or double-applies a letter at every crash.
- **Why this severity:** §1.1 argues at length that an alignment which cannot be proven is not one
  this framework asserts. Two paragraphs later the phase asserts three of them, and the one carrying
  ES-06's headline guarantee is the least checkable of the three.
- **Why this timing:** The check is the same three shipped calls; deciding it later means either a
  second `Unchecked`-shaped value appearing after the surface is frozen, or the guarantee shipping
  unmeasured.
- **Close criteria:**
  - [ ] The spec states, for `Generations` and `Park`, whether the alignment is measured, and if not,
        why the argument that made it mandatory for `Destination` does not apply.
  - [ ] §UC-173 gains a case in which `Generations` resolves to a second transaction, asserting the
        refusal or the stated weakening.
  - [ ] The redrive's atomicity is stated: which writes commit together, what the tier check does
        there, and what a crash between the apply and the evict leaves.
- **Status:** closed — argued where it is asserted, and measured where measurement exists. §1.6 states that `Destination` is measured because the framework resolves the value and holds an identity, while `Generations` and `Park` are interfaces over which any self-report would be written by the same code that is wrong; so the alignment is asserted and falsified **by consequence** — a rollback — rather than by comparison. §UC-192 asserts the second-pool boundary against §UC-173's control, §UC-149/§INV-091 do the same for the park, and the redrive's atomicity is now stated in full: the apply, the `Evict`, the `Touch` and the staged effect are one transaction, the tier check applies, `Unchecked` is refused, and a crash leaves the letter parked.

---

### GAP-17 [high][immediate] Cutover atomicity is stated without the reader's precondition, and nothing orders retirement against readers

- **Where:** §1.5 ("What the cutover switches, atomically"), §UC-161, §INV-098; against [[D-130]]'s
  *"Do not state `InUnit`'s atomicity without its precondition in the same breath."*
- **What:** *"The answer is that the read target is a row, and a row switches atomically for every
  table at once"* is true only if the reader resolves the row **in the same snapshot** as the data
  it then reads. A read path that caches `Active` per process, per request or per connection — the
  obvious implementation, since it is one row read on every query — sees a stale generation. The
  spec never states the obligation. It also never orders the *retirement*: §1.5 says *"When the old
  generation is dropped, its letters are dropped with it, by the same operator operation"* and
  nothing says a reader may still be holding generation 1 when its tables go away.
- **Why this severity:** The failure is a live site querying dropped tables, i.e. a hard outage, and
  it arrives from following the spec's own recipe. §UC-161's control (a reader held open across the
  commit) measures the property in the one configuration where it holds and certifies the general
  claim from it.
- **Why this timing:** [[D-130]] makes "state the precondition beside the promise" a house rule for
  exactly this shape, and D-134/D-135 (§7.2) are written this phase.
- **Close criteria:**
  - [ ] The atomicity sentence carries its precondition wherever it appears (spec, module page,
        decision): the row is read in the same snapshot as the tables it resolves.
  - [ ] The retirement ordering is stated — a retired generation's tables are dropped only after
        every reader has re-resolved, and how a deployment knows that.
  - [ ] §UC-161 gains the negative arm: a reader that resolved the row earlier reads the retiring
        generation, and that is correct until it is dropped.
- **Status:** closed — the precondition travels with the promise everywhere it appears (§1.5, the `Generations` type, and D-134/D-135 in §7.2): atomic **for a reader that resolves the row in the same snapshot as the tables it then reads**, with the cached-resolution window named as a legitimate choice that may not be called atomic. Retirement is ordered against readers as a second, later, deliberate step, and §UC-161 gains the negative arm — a reader that resolved earlier keeps reading the retiring generation, correctly, until it is dropped.

---

### GAP-18 [high][immediate] `Quarantined` never decrements and `Cutover` refuses a non-zero `Quarantined`, so a fully-recovered generation can only cut over through the override

- **Where:** §1.4 ("What a skip marks" — *"never decremented by anything — not by a successful
  redrive, not by an eviction"*), §1.5 (*"`Cutover` refuses a generation whose `Quarantined` is
  non-zero"*), §UC-156 control, §UC-163, §INV-096, §INV-098.
- **What:** The two rules compose into a contradiction. A rebuild that parked one sequence and then
  redrove it **completely** has a read model with no holes and a `Quarantined` of 2 for ever.
  `Cutover` reads that counter as *"its rows have holes and cannot be compared to the live ones"* —
  false — and refuses. The documented escape is `AcceptQuarantined`, described as taking an
  incomparable generation *"anyway, out loud"*.
- **Why this severity:** The ordinary recovery path (park, fix, redrive, cut over) is reachable only
  through the flag whose whole purpose is to admit a *known-broken* generation. Operators will set
  it by default, and the one signal that would have stopped a genuinely holed cutover stops meaning
  anything. This is the check §1.5 calls *"a cutover's only evidence"*.
- **Why this timing:** It decides whether the phase needs a second, decrementable durable number (a
  live "unrecovered" count) or a different gate — a field on the checkpoint or a question asked of
  the `Park`. Either is contract surface.
- **Close criteria:**
  - [ ] `Cutover`'s evidence distinguishes "has holes now" from "had a failure and recovered", with
        the mechanism named.
  - [ ] §UC-163 gains the recovered case: a generation that parked and fully redrove is admitted
        without the override, or the refusal is justified against this exact scenario.
  - [ ] §INV-096 and §INV-098 are restated so they do not disagree about what `Quarantined` proves.
- **Status:** closed — the evidence changes from "had a failure" to "has a hole now". §1.4 adds `Park.Holes` (queued now plus evicted-unapplied), `Readiness` carries it, and `Cutover` refuses on it rather than on `Progress.Quarantined`, which never falls. A generation that parked and redrove completely now cuts over **without** `AcceptQuarantined`, so the override goes back to meaning what its name says. §UC-163 has three arms including the recovered one; §INV-096 states why `Holes` is a question and not a decremented counter; §INV-098 restated.

---

### GAP-19 [high][immediate] §5.3 widens the `Checkpoints` conformance contract while §5.1 says the kernel does not move

- **Where:** §5.1 (*"`docs/api/surface.md`'s `event` section is byte-identical after this phase"*),
  §5.3 (`RunCheckpoints` gains a `topology` section), §7.1.
- **What:** The new section requires every checkpoint store to admit the split's three statements in
  one transaction, to refuse a save at advance 1 over a live row, and to **round-trip a cursor
  written by one identity and read by another**. The third is a new obligation: nothing in phases
  1–3 forbids a store from binding a cursor to the projection name that saved it, and [[D-129]]'s
  round-trip requirement is stated through *a second value*, not a second *identity*. A third-party
  store certified under phase 3 can go red on phase 4's suite with no signature having changed —
  a breaking contract change presented as "nothing changes", because the baseline that guards the
  kernel only watches exported signatures.
- **Why this severity:** `eventtest` is the portability contract; a widening that is invisible to
  `check-event-kernel` is precisely the class of change the freeze exists to catch. It also lands on
  the argument §1.3 uses to avoid a `List` method, so it is load-bearing rather than incidental.
- **Why this timing:** It changes what a store must do, and stores are written against the published
  suite.
- **Close criteria:**
  - [ ] The spec states the widening explicitly, with the three new obligations listed as store
        obligations rather than as a suite section.
  - [ ] It says whether the new section is gated on a capability or mandatory, and what a store that
        cannot round-trip a cursor across identities is told (and whether `Split` is then refused
        against it).
  - [ ] §5.1's "nothing changes" is qualified so a reader is not told the store contract is frozen.
- **Status:** closed — the widening is stated as a widening. §5.3 lists the three obligations as **store** obligations, says the cursor-across-identities round trip is genuinely new against phases 1–3 and that a phase-3-certified store can go red, and makes it mandatory rather than capability-gated with the reason (a split has no other spelling; a `CheckpointCapabilities` flag would move a frozen kernel type and turn a conformance failure into a run-time discovery). §5.1's "nothing changes" is qualified, §7.1 says the baseline cannot see it, and §7.3 adds the `eventtest` module page row.

---

### GAP-20 [high][immediate] `Split`, `Observe`, `Reached` and `Cutover` call `event.Checkpoints` directly, and `Split`'s behaviour under a `Unit` that runs the work twice is unstated

- **Where:** §5.2 `SplitSpec.Checkpoints event.Checkpoints`, `Observe(…, checkpoints
  event.Checkpoints, …)`, `Reached(…)`, `CutoverSpec.Checkpoints`; against [[D-129]] "Where it
  lives" (*"the `Tracker` door, **which is the only caller of a `Checkpoints`**"*) and [[D-130]]
  (*"A caller's `Unit` may run the work more than once … the second run is refused before it writes
  anything"*).
- **What:** Four new entry points take the raw store. None of the tracker door's admission checks —
  the half-absent row, the empty cursor at a live advance, another name's row, a cursor over the
  ceiling — is stated to apply to the rows `Split` writes or to the rows `Observe`/`Reached` read as
  a barrier. And `Split` performs three ordered writes inside a `Unit` the caller owns, which
  [[D-130]] says may run its body more than once; the spec never says what the second run does
  (its first-run `Forget` has already removed the parent it re-reads, so the second run takes
  §1.3's *fresh* arm and writes nothing — see GAP-7).
- **Why this severity:** A barrier read off a half-absent row is a number `Cutover` acts on; a child
  row written past the door is a checkpoint no `Tracker` ever admitted. The `Unit` arity obligation
  is one phase 3 hit and solved explicitly for the pass, and the spec re-introduces the shape
  without inheriting the answer.
- **Why this timing:** It is the exported shape of four functions and the relationship between
  `event/projection` and a frozen kernel door.
- **Close criteria:**
  - [ ] The spec states whether these four go through `event.Track` or the raw store, and which of
        the door's checks apply; if the door is bypassed, [[D-129]]'s sentence is amended in the same
        change rather than contradicted.
  - [ ] `Split`'s behaviour under a `Unit` that runs its body twice is stated, with the refusal or
        the idempotence rule, and a use case for it (§UC-138's control counts statements and cannot
        see this).
  - [ ] `Observe`/`Reached` state what they do with a partition whose row is absent, malformed, or
        carries another name — none of which currently produces anything but a position.
- **Status:** closed — all four go through the door. §1.3 step 2 and §5.2 state that `Split`, `Observe`, `Reached` and `Cutover` reach the store only through `event.Track`, one tracker per identity, so every admission check applies to the rows a split writes and the rows a barrier is read from; [[D-129]]'s sentence stands unamended. `Split`'s arity is answered by holding no state between runs — a re-run after a rollback repeats the same work, a re-run after a commit is refused by the absent-parent rule — with §UC-185 as the case, and `Observe`'s absent/foreign/malformed row behaviour is stated in §UC-180 and §INV-087.

---

## Deferred — recorded in [`EVENTSOURCE_BACKLOG.md`](EVENTSOURCE_BACKLOG.md) `## P4`

Sixteen `[medium]`/`[low]` findings are appended there under the 2026-09-08 delivery policy and are
not repeated here. They do not block this gate.
