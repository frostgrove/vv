# EVENTSOURCE_P1 - phase 1 usecases/invariants/DX - GAPS

## Round 1 resolution - 2026-09-06

All twenty-four `[immediate]` findings are closed in
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` itself; each `Status`
line below names what closed it and where. The two `[deferred]` findings stay
`open` here, as `gaps.md` requires, and are carried into the spec's new
`## 8. Deferred` so the phase boundary cannot drop them.

One disagreement is on the record rather than settled by silence: **GAP-4's
premise is rejected** while its defect is fixed. The finding argues that
INV-028's unforgeability and UC-030's cross-subsystem comparison can only both
hold if both subsystems derive a token from one shared identity vocabulary. That
is true of value *equality* and false of the question being asked: each
subsystem can instead answer a predicate about the transaction the application
already holds, which needs no shared type, adds no import in either direction,
and cannot be forged because asking requires holding the transaction. The
argument is written into §UC-030 and §INV-028; the residual — whether a shared
minted identity is ever wanted — is on the agenda as Q17.

Two findings were closed more strongly than their close criteria asked, and the
plan should know it. **GAP-3**: rather than scoping the tiling claim to stores
declaring `MonotoneVisibility`, the read now returns a store-minted opaque
cursor and safe resumption is **mandatory for every store** (§INV-035), with
`MonotoneVisibility` demoted to a freshness claim. **GAP-2**: rather than
documenting an immutability obligation, a state type holding a reference kind
must declare a copier or be refused at declaration (§UC-052) — option (a), (b)
and (c) combined into the one that neither trusts a paragraph nor outlaws real
aggregates.

## Round 1 - econv-usecase-validator (coverage + invariant + DX roles) - 2026-09-06

Sources read: `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (whole file),
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` (whole file), `CLAUDE.md`,
`docs/ai/decisions/Index.md`, `~/.claude/skills/econv/SKILL.md`,
`references/gaps.md`, `references/universality.md`, `references/microkernel.md`.
No `.go` file was opened.

Checks run: every actor and entry mode against a happy path; every ES non-negotiable invariant
against a restated INV; the edge matrix (empty / malformed / oversized / ambiguous / duplicated /
multilingual / partially failing dependency / timeout / zero results / everything-matches /
conflicting signals); the three universality checks (unseen input, delete-the-example,
second-instance) against the sketch's `Account` aggregate; the microkernel extension-point
four-part test; Go-level feasibility of every inference claim in D.11; and a UC-vs-UC
contradiction sweep.

### GAP-1 [critical][immediate] The stream key has no injectivity, validity or byte-identity contract - two aggregates can silently share one history

- **Where:** §2.1 "stream key" row (line 112); D.1 "Why the identity mapper is declared here"
  (lines 1443-1450); UC-013 precondition (line 421); INV-022 (lines 1247-1256).
- **What:** The identity mapper `func(id AccountID) event.Key` is declared as "the single place
  where an aggregate identity becomes a key", and the only failure the spec worries about is *one
  ID rendered two ways* ("a stream that splits in half"). The opposite direction - *two IDs
  rendered as one key* - is never forbidden, never detected and never mentioned. Nothing in the
  spec requires the mapper to be injective, nothing bounds the key's length, nothing forbids an
  empty key, and nothing states whether keys compare as bytes or after any normalisation. INV-022
  bounds "every declared identifier"; a key is produced at runtime by application code and is
  therefore not a declared identifier, so it falls outside every bound in the document. UC-013
  even names "a stream key collision across families" as a *cause* of an unknown-type refusal,
  which concedes collisions happen while leaving the same-family collision - the one that merges
  two aggregates of the *same* type into one stream - completely silent.
- **Why this severity:** Consider any aggregate whose identity is composite, which is the ordinary
  case outside the sketch: `func(id OrderID) event.Key { return event.Key(id.Tenant + id.Number) }`.
  Tenant `ab` / order `c1` and tenant `a` / order `bc1` are one stream. Both aggregates load each
  other's facts, both fold them (the event types are declared, so no refusal fires), both append at
  a version derived from the other's history. There is no error at any point and no invariant this
  violates as written. Same failure with a case-folding key, a `fmt.Sprintf("%v")` key over a
  struct, a key built from user text where NFC and NFD forms both occur, or a zero-valued ID
  mapping to the empty key that every zero-valued ID shares. This is silent, permanent corruption
  of the one thing the phase exists to make trustworthy.
- **Why this timing:** It is a public contract on the declaration seam (`Define`'s second
  argument) and on the store contract's key type. Adding an injectivity obligation, a key
  validation point and a bound afterwards changes `Define`, `Key`, the refusal vocabulary and
  every conformance section that writes a key. It is also a fit-to-the-example finding
  (`event.Key(id)` is a correct mapper only because `AccountID` is a string), which
  `universality.md` forbids deferring.
- **Close criteria:**
  - [ ] The spec states the mapper's obligation as a property the application owes (injective over
        the aggregate's identity domain) and says what the framework does when it cannot check it.
  - [ ] A UC covers a non-string, composite identity and shows the mapper shape that keeps
        injectivity (a length-prefixed or otherwise unambiguous encoding), so the mechanism is not
        `event.Key(id)`-shaped.
  - [ ] Key validity is defined at the seam: empty key refused, length bounded (added to INV-022's
        enumeration), permitted byte domain stated, and comparison declared byte-exact with
        normalisation named as the caller's job.
  - [ ] A UC covers "the key the mapper produced is illegal for this store's key space" with a
        typed refusal before any statement.
  - [ ] An INV states that a stream is identified by (family, key) compared as bytes, and names
        how a store proves it (a conformance section that writes two keys differing only by a
        normalisation form or by a separator ambiguity and asserts two streams).
- **Status:** fixed — §2.1 gives the key an injectivity, validity and byte-comparison contract; §UC-050 covers a composite identity and the length-prefixed composing helper; §UC-051 refuses an empty or over-long key before any statement (`ErrKey`); §INV-033 states (family, key) byte-exact identity, names the application obligation and the framework's three answers to it, and its falsification is a conformance section writing separator-ambiguous and NFC/NFD keys; the key cap joins §INV-022's table; §D.1 shows the wrong and the right mapper side by side.

### GAP-2 [critical][immediate] `view.State()` returns a shallow copy that aliases the view's own accumulator - correct only for a state of scalars

- **Where:** D.5 line 1646 ("`view.State()` returns a copy of the aggregate"); D.2 lines
  1510-1517 (the residual-hazard argument); UC-021 (lines 547-555); INV-021 (lines 1237-1245);
  D.0 line 1380 (`type Account struct{ Open bool; Currency string; Balance int64 }`).
- **What:** D.2 admits the hazard - "a state containing a slice or map whose backing array a fold
  mutates in place" - and closes it *only from the load side*: the accumulator is unreachable
  "until the view is returned". After the view is returned, `State()` hands the caller a copy that
  shares every backing array and map header with the live accumulator the view keeps for its next
  local fold (UC-021 keeps appending against the same view). The spec never says the copy is
  shallow, never requires the state type to be deep-copyable or free of reference kinds, and never
  states an obligation on the decision function not to mutate what it was given.
- **Why this severity:** The sketch's decision function `func (a Account) Credit(...)` is safe only
  because `Account` holds three scalars. Give the aggregate the shape almost every real one has -
  `type Order struct{ Lines []Line; Applied map[string]bool }` - and a decision function that
  writes `a.Applied[cmd.ID] = true` or `append`s in place into spare capacity mutates the view's
  state without appending a fact. The next `view.Append` folds onto a state that no event
  sequence produces, and the receipt reports a version the state does not correspond to. A reload
  gives a different state than the live view. This is the "unambiguous state" rule in
  `data-integrity.md` broken silently, and it is precisely the second-instance test in
  `universality.md`: the design works on the shown aggregate and needs a new rule for the next one.
- **Why this timing:** It decides the signature of `State()` (value vs a caller-supplied clone
  seam vs a documented immutability obligation on the state type), and INV-021's "the accumulator
  can alias something the caller still holds" clause is currently claimed as closed when it is not.
  Every later section - the decision-function convention, the conformance suite's fold sections,
  the memory store's copy semantics - is written against whichever answer this takes.
- **Close criteria:**
  - [ ] The spec states plainly that the copy is shallow, and either (a) declares an application
        obligation that the state type is treated as immutable by folds and decisions, with the
        falsification named, or (b) adds a declared cloning seam, or (c) requires the state type to
        contain no reference kinds and refuses at declaration.
  - [ ] A UC covers an aggregate whose state holds a slice and a map, exercising fold, `State()`,
        decide and a second append against the same view.
  - [ ] INV-021's aliasing clause is restated so it is true of the whole view lifetime, not only
        of the load, and carries a falsification that mutates the returned state and asserts the
        view and a reload still agree.
- **Status:** fixed — the copy is stated shallow, and the strict option (c) is taken in a form that does not outlaw real aggregates: §UC-052 makes `event.Copy` **required** at declaration for any state type holding a reference kind, refusing and naming the field otherwise, and `State()` applies it; §INV-021's aliasing clause is restated over the whole view lifetime with the mutate-and-reload falsification and the no-copier-refused control; §D.1 and §D.2's residual-hazard paragraph are rewritten.

### GAP-3 [critical][immediate] The global-read cursor pattern in the DX sketch silently skips events, and the conformance suite makes the unsound guarantee mandatory

- **Where:** D.7 lines 1754-1762 (`cursor = e.Position`); UC-036 (lines 802-818); UC-037 (lines
  819-836); UC-039 (lines 848-858); D.9 conformance table row "global paging | always | Any bound
  tiles the log exactly once" (line 1878); INV-009 (lines 1110-1125).
- **What:** The document is internally at war with itself on the one property UC-037 correctly
  identifies as "the single most commonly missed correctness property in event stores".
  UC-037/INV-009 declare monotone visibility to be a *per-store capability that may be false*, and
  explain the exact failure: "position 7 can become visible before position 5, so a checkpointing
  consumer silently skips 5 forever". Then D.7 - the normative DX sketch for actor A4 - writes
  that exact consumer (`cursor = e.Position`, resume `After: cursor`) with no guard, no capability
  check and no comment. And UC-039 plus the conformance table promise, for *every* store
  unconditionally ("always"), that repeated bounded reads reconstruct the log "exactly once" with
  "no event skipped at a page boundary". Those two claims cannot both hold on a store that
  declares `MonotoneVisibility: false`, which is the store the spec itself says a plain SQL
  sequence produces (UC-037, Q8).
- **Why this severity:** The only consumer-facing read shape in phase 1 is the one that loses
  data, and the loss is invisible: the projector, outbox relay and export tool of later phases are
  all specified to be written against this shape (D.7 "A future projector, outbox relay or export
  tool is written against this shape"). A mandatory conformance section that asserts a property a
  legitimate store cannot keep either forces phase 2's store to declare a guarantee it does not
  have, or fails a correct store - both outcomes are worse than having no section.
- **Why this timing:** The safe resume point is a *contract shape*, not an implementation detail:
  a store that cannot promise monotone visibility must expose a watermark (a position below which
  no writer can still commit) or the read must return a resume token rather than a raw position.
  That decision changes `AllQuery`, the envelope's role in resumption and the capability report -
  all public, all depended on by phase 2.
- **Close criteria:**
  - [ ] UC-039 and the "global paging" conformance row are scoped: exact tiling is asserted for a
        quiescent store, or for a store declaring `MonotoneVisibility`, and the section states
        which.
  - [ ] A UC covers "a bounded read is resumed on a store that does not guarantee monotone
        visibility" and states what the contract offers instead of a raw position (a safe
        watermark, an opaque resume token, or an explicit statement that resumption is unsafe and
        the read is single-pass only).
  - [ ] D.7's sketch either performs the capability check inline or is rewritten to use the safe
        resume shape, so no reader can copy the losing pattern out of the northstar.
  - [ ] An INV states that a resumption point returned by the read is safe to persist for every
        store the contract admits, and names its falsification (a store that commits out of
        position order must fail a section, not the consumer).
- **Status:** fixed, and more strongly than the close criteria asked — the read now returns a store-minted opaque `Cursor` (§UC-036, §UC-053, §D.7), §INV-035 makes safe resumption **mandatory for every store** rather than a capability, and `MonotoneVisibility` is demoted to a freshness claim (§UC-037, §INV-009). §UC-039 is scoped to a quiescent store and the D.9 row with it; a new mandatory `resumption` section covers the non-monotone case; the D.7 sketch uses `page.Resume` and `AllQuery` has nowhere to put a position, so the losing pattern is unspellable.

### GAP-4 [critical][immediate] The cross-subsystem authority comparison in D.6 is unimplementable under INV-028 as written

- **Where:** D.6 line 1705 (`if !commit.Authority().Same(app.auditor.AuthorityFor(txCtx))`);
  UC-030 (lines 686-705); INV-028 (lines 1317-1327); [ES] lines 390-395 and 417-424.
- **What:** INV-028 requires the authority value to be unconstructible, unparseable,
  unmarshallable and unreconstructible "by a caller or a decorator", and UC-030 requires two
  *different subsystems* (event and audit/jobs) to produce values that compare equal when they
  share one transaction. Those two requirements can only be met simultaneously if both subsystems
  derive their token from a single identity vocabulary neither of them owns. The spec never names
  that vocabulary, never says what type crosses `Same`'s parameter, and Q4 (which is the only
  place the transaction seam is questioned) asks only whether the root `event` package may name
  `crud.Source` - it never asks how a *foreign* subsystem's authority becomes comparable. [ES] is
  no help and is in fact the source of the tension: it requires "the same binding, not the
  same-looking handle" while also forbidding `eventpg` from importing `jobs` to borrow one.
- **Why this severity:** UC-030 is one of the phase's headline guarantees and the whole reason
  the receipt carries an authority at all ("the framework's whole contribution is making the claim
  checkable", D.6 line 1749). As written, `Same` must take `any` or a locally-declared interface,
  and the only implementable answers are (a) both sides expose something a decorator could
  reconstruct - which INV-028 forbids - or (b) both sides compare an identity minted by a shared
  root package - which is a public dependency decision this spec does not take. Discovering this
  during implementation forces a rewrite of the receipt type, the authority type and the audit
  composition story.
- **Why this timing:** It is a public contract that phase 2 and any audit work depend on, and it
  changes the root `event` package's import graph (Q11's subsystem/graph checks read that graph).
  It cannot be an implementation detail: the comparison is inter-package by construction.
- **Close criteria:**
  - [ ] The spec states what type `Same` accepts and where the comparable identity is minted, or
        records the question as a first-class open question on the reconciliation agenda with the
        two candidate answers and their costs.
  - [ ] INV-028's falsification is extended to the cross-subsystem case, or narrowed explicitly to
        within-subsystem equality with UC-030 rewritten to match what is actually provable.
  - [ ] A UC or a stated non-goal decides whether phase 1 ships a cross-subsystem comparison at
        all, given no second subsystem in the tree exposes one yet.
- **Status:** fixed; one premise rejected on the record. The finding is right that the receipt could not do what UC-030 claimed, and that is closed: §UC-030 now ships within-subsystem equality (`Same`) plus a cross-subsystem **predicate** (`Authority.BoundTo(ctx)`), §INV-028 is restated in those two scopes, §D.6 rewrites the sketch, and Q17 records the shared-minting alternative with its cost. **Rejected:** the finding's premise that the two requirements can only be met by a shared identity vocabulary — that is true of value *equality* and false of the question actually being asked. Nobody must compare two tokens if each token can be asked about the one thing both subsystems already hold: the caller's transaction. Nothing crosses the package boundary, nothing becomes forgeable (asking requires holding the transaction), and no import is added in either direction. The argument is written into §UC-030 under "Why the comparison is a predicate".

### GAP-5 [critical][immediate] Store identity is defined per store *instance*, which is correct only for the in-memory store

- **Where:** UC-007 lines 326-333 ("Streams in one are invisible in the other"); UC-031 (lines
  707-725); INV-016 (lines 1189-1197); [ES] lines 466-476 (per-request `eventpg.New` over a
  borrowed tenant lease) and lines 380-386 (`crud.SameDataSource` / `crud.KeyOf` as how a mismatch
  is detected).
- **What:** UC-007 asserts as an *observed* property of "two stores in one process" that each
  store's streams are invisible to the other, and UC-031/INV-016 build the append-time safety
  check on that model: a view carries "the identity of the store it came from" and an append
  through a different store is refused. Both statements are true of two `eventmemory` values and
  false of two stores over one database. The spec explicitly anticipates the mixed case ("in phase
  2 a memory store beside a real one") but never the same-backing case, and it never says what
  store identity *is* - instance, backing authority, or datasource key. [ES] shows the shape that
  makes this bite: a tenancy deployment constructs a store per request over a borrowed lease, so
  two store values over the same database in the same process is the ordinary case, not an exotic
  one. There is also no use case anywhere for a store whose lifetime is shorter than the process,
  even though UC-006/UC-046 assign construction, ownership and close to the composition root.
- **Why this severity:** With instance identity, an application that constructs a store per
  request gets a refusal (UC-031's typed wiring refusal) on a perfectly legal program - a view
  loaded in one helper and appended in another that re-borrowed the lease - and the refusal names
  "a scope kind and nothing about either store", so it is undiagnosable. With backing identity,
  UC-007's stated observation is wrong and its test would be written against the wrong property.
  Either way one of the two use cases is a lie, and INV-016 - the invariant whose stated failure
  mode is "a write lands in the wrong database" - rests on an undefined term.
- **Why this timing:** Store identity is in the view's data shape, in the store contract, and in
  the conformance suite's transaction section. It is exactly the kind of "correct on the sample
  implementation, wrong on the next one" finding `universality.md` forbids deferring, and phase
  2's store is the second instance that breaks it.
- **Close criteria:**
  - [ ] The spec defines store identity as a named property (backing authority, not instance) and
        says how two store values prove they share one, including the memory store's answer.
  - [ ] UC-007's "Observed" is corrected to a property that holds for both implementations, or
        split into two use cases (two independent stores / two stores over one backing).
  - [ ] A UC covers a store whose lifetime is one request (the tenancy lease shape [ES] shows),
        including construction cost, `Bind` cost, close, and whether a view may cross the boundary.
  - [ ] INV-016's falsification adds the same-backing control: two stores over one backing must
        *accept* the append, so an instance-pointer implementation fails the control.
- **Status:** fixed — identity is the **backing**, defined in §2.1 and §INV-016 as what a store writes to rather than the store value. §UC-007 is corrected to two backings and §UC-054 added for two store values over one backing (with the two-backing case as its control); §UC-055 covers the per-request store over a borrowed tenant lease, including construction cost, close ownership and whether a view may cross; §INV-016 gains the same-backing control that an instance-pointer implementation fails; §D.4 adds the `Log` spec field so the memory store can express a shared backing at all; D.9 gains a `Sibling` factory hook and a `shared backing` section.

### GAP-6 [high][immediate] The view's state after any failed append other than a conflict is unspecified

- **Where:** UC-023 (lines 576-592, the only defined post-failure view state); UC-024 (lines
  594-607); UC-025 (lines 609-621); UC-034 (lines 758-782); UC-047 (lines 966-972); D.5 lines
  1649-1651 ("Why the view advances on success").
- **What:** The spec defines exactly two view transitions: advance on success, poison on conflict.
  Five other append failures exist in the document - unencodable payload, oversized payload, batch
  bound exceeded, commit uncertainty, closed store - and for none of them does the spec say whether
  the view advanced, stayed put, or is poisoned. Commit uncertainty is the dangerous one: by
  definition the store does not know whether the version advanced, so "the view's version" has no
  correct value, and UC-034's recovery instruction ("load again and decide again") is advice the
  shape does not enforce - unlike the conflict case, where UC-023 makes the rule structural.
- **Why this severity:** The whole safety claim of D.5 is that the version is not a parameter, so
  it cannot be wrong. That claim only holds if every path that could desynchronise the view from
  the store either advances it correctly or refuses. An implementation that advances the view on
  an uncertain commit produces a view that will conflict forever; one that does not advance it
  after a partially-applied append produces a silent double write on the next append if the first
  had in fact landed. The spec gives an implementer no way to choose correctly, and the
  conformance suite has no section that would catch a wrong choice.
- **Why this timing:** It is the state machine of the phase's central value type. `data-integrity.md`
  requires explicit state machines; this one has two of seven transitions written down, and every
  store and every caller depends on the rest.
- **Close criteria:**
  - [ ] The view's states and transitions are enumerated (fresh / advanced / stale / uncertain /
        unusable) with the trigger for each, as one table.
  - [ ] UC-034 states the view's state after an uncertain commit and whether a further append
        through it is expressible.
  - [ ] Each remaining append failure (UC-024, UC-025, UC-047, cancellation) states its transition.
  - [ ] A conformance section pins the transitions, with the control that the success transition
        still works.
- **Status:** fixed — §INV-037 and the table in §D.5 enumerate five states and six transitions. Every append failure now names its transition: UC-024, UC-025, UC-031, UC-047 and UC-057's before-write window are `unchanged`; UC-022 is `stale`; UC-034 and UC-057's after-write window are `uncertain`, refusing with `ErrStaleView` wrapping `ErrUncertain`. A mandatory `view transitions` conformance section pins them with the success transition as its control.

### GAP-7 [high][immediate] Codec-dependent validation is placed at declaration time, but the codec is chosen per store at composition

- **Where:** UC-004 line 273 ("a payload type containing a field kind the codec cannot encode
  deterministically") and lines 277-279 (panic before `main`); D.4 lines 1596-1604 (the codec is a
  property of the store, chosen in the store spec); UC-007 (one declaration bound to two stores);
  INV-023 line 1266 ("a payload type containing a map, which must either encode deterministically
  or be refused at declaration").
- **What:** Declarations are package-level values built before any store exists (E1, "before
  `main` runs"). The codec is a store spec field and a deployment choice, and the same declaration
  is bound to two stores in UC-007. So "the codec" is not a thing the declaration can consult, and
  a declaration-time encodability check must either hardcode one codec's rules into the kernel or
  check nothing. INV-023 makes the same mistake in the other direction: whether a map encodes
  deterministically is a codec property (stdlib JSON sorts map keys; another codec need not), yet
  the refusal is placed at declaration.
- **Why this severity:** As written this is policy in the kernel - `microkernel.md`'s
  `[high][immediate]` case - and it makes the codec seam decorative: the only codec whose rules the
  declaration can enforce is the built-in one, so a second codec either cannot be used or is
  validated against the wrong rules. It also breaks the [[D-021]] promise the spec leans on, since
  the check that actually matters (this payload type against *this deployment's* codec) is the one
  that would not run.
- **Why this timing:** It decides where the check lives, which decides whether `Bind`/store
  construction can fail and with what, and whether `Codec` carries a "can you encode this type"
  question at all. Both the declaration API and the codec contract change with the answer.
- **Close criteria:**
  - [ ] The spec separates codec-independent declaration checks (non-struct payload, nil fold, nil
        upcaster, duplicate name, identifier bounds) from codec-dependent ones.
  - [ ] Codec-dependent validation is moved to a point where the codec is known - store
        construction or `Bind` - and a UC covers it, with the refusal shape stated and [[D-021]]
        satisfied (start-up, not request time).
  - [ ] INV-023's map clause is restated as a codec obligation with the codec contract's own
        determinism requirement, not as a declaration-time refusal.
  - [ ] The `Codec` contract states whether it can be asked about a type at all, since UC-004's
        check needs that seam if it stays.
- **Status:** fixed — declaration checks are split from codec-dependent ones. §UC-004 loses the codec trigger and says why; §UC-056 moves the check to `Bind`, where the codec is known and it is still start-up ([[D-021]]); `Bind` returns an error in §D.0, §D.4 and the §D.11 inference table; the `Codec` contract gains the one question "can you encode this type deterministically"; §INV-023's map clause is restated as a codec obligation checked at bind, with a two-codec falsification.

### GAP-8 [high][immediate] The transaction binding is store-specific, which breaks the memory store's stated reason to exist

- **Where:** UC-028 lines 652-654 ("bound it in the context in the way the concrete store
  documents it"); D.6 line 1684 (`txCtx := app.bindTransaction(ctx, tx) // however the concrete
  store documents it`); UC-040 and D.8 lines 1828-1831 ("A test that passes here and fails on a
  database should be a bug in the database store, not a difference in the seam"); Q12 lines
  2086-2092; Q4 lines 2034-2041.
- **What:** The binding vocabulary is delegated to each store implementation. That means the
  *application code under test* - the line that puts the transaction in the context, and the
  transaction type itself - differs between the memory store and the SQL store. An app-usecase
  that uses `Within` therefore cannot be exercised against the memory store without editing it,
  which is precisely the outcome Q12 says the memory store's transactions exist to prevent and
  which D.8 promises does not happen. Q4 records the shape question ("whether `eventmemory` and
  `eventpg` share a binding vocabulary or only a shape") but does not record that the "only a
  shape" answer contradicts UC-040 and Q12.
- **Why this severity:** It nullifies a headline use case (A6's "unit-test a transaction-bound
  app-usecase with no database") while the document still claims it. Every conformance
  "transactions" section is also implicated: the suite must construct a transaction for an
  arbitrary store, and with a store-defined vocabulary the suite needs a per-store hook that is
  not in the factory signature shown in D.9.
- **Why this timing:** It changes the store contract (does the contract own a `Beginner`-shaped
  seam or not?), the conformance factory signature, and the memory store's scope - which Q12 says
  "changes the size of phase 1 materially".
- **Close criteria:**
  - [ ] The spec decides whether the transaction binding is a contract-level vocabulary or
        store-private, and states the consequence for UC-040 either way.
  - [ ] If store-private, D.8 and Q12's claim is corrected, and a UC covers what an app-usecase
        author actually does to test the transaction path.
  - [ ] The conformance suite's factory signature in D.9 is extended to whatever the "transactions"
        section needs to begin a transaction on an arbitrary store.
- **Status:** fixed — the binding vocabulary is **store-private** and the consequence is stated rather than denied: §UC-040 and §D.8 now say precisely what is shared (the app-usecase, byte-identical) and what is not (the two lines that open and bind a transaction, which belong to the composition root), and record that the earlier no-line-differs claim was false. Q12 and Q4 are corrected to match. The D.9 factory grows a `Begin` function, and a store declaring `TransactionBinding` without one **fails** rather than skipping (§UC-043).

### GAP-9 [high][immediate] Sealing on first *bind* is too late - the spec's own helpers read a declaration without binding it

- **Where:** UC-005 (lines 288-302, "sealed on first bind"); INV-013 lines 1159-1160 ("mutated
  only before it is bound, and is read-only for its whole observable life"); D.8 line 1838
  (`event.Replay(accounts, ...)` - no store, no bind); D.8 line 1845 (`eventtest.RoundTrip(t,
  credited, ...)`); D.11 line 1930 (`event.Replay(agg, changes...)`).
- **What:** Two of the three testing entry points read the declaration's tables without ever
  binding it to a store. `Replay` consults folds; `RoundTrip` consults the reader chain and the
  codec. If sealing is triggered by `Bind`, both observe an unsealed, still-mutable table, and
  INV-013's claim - "read-only for its whole observable life" - is false by the spec's own DX
  sketch. The repository's recorded failure that INV-013 cites (`Relation.resolveDefaults` written
  outside its `Once`, found by reading) is exactly this shape.
- **Why this severity:** It is a stated invariant that the document's own examples violate, and
  the failure it guards is a data race between a late declaration and a concurrent first reader -
  a defect class this repository has already been bitten by and which `-race` will not reproduce
  reliably.
- **Why this timing:** Sealing on first *read* rather than first bind changes where the seal lives
  (on the declaration, guarded, consulted by every reader) and therefore the shape of `Type`,
  `Fact`, `Replay` and the test helpers.
- **Close criteria:**
  - [ ] UC-005 and INV-013 are restated so the seal is triggered by the first *observation* of the
        table, whatever performs it, and every reader listed.
  - [ ] The DX sketch's store-free helpers are named as sealing readers.
  - [ ] INV-013's falsification adds a case that declares late after a `Replay`-only use.
- **Status:** fixed — sealing is triggered by the **first observation**, not by `Bind`. §UC-005 is retitled and lists every reader that seals (bind, store-free replay, round-trip, any read of a fact's chain, the conformance suite); §INV-013 is restated and its falsification adds the declare-after-`Replay` case that a bind-triggered seal passes; §D.8 names both store-free helpers as sealing readers.

### GAP-10 [high][immediate] The store contract - the phase's one extension point - has no shape anywhere in the document

- **Where:** §1 line 42 (`event/eventtest` "an exported conformance suite that any store runs");
  actor A5 (line 191); UC-043 (lines 900-916); INV-018/INV-019 (lines 1209-1227); §6 (the DX
  section, which sketches A1, A2, A3, A4 and A6 and never A5); D.13 lines 1974-1981 (the four
  required parts of the extension point).
- **What:** A5 (store implementer) is a declared first-class actor whose daily work is
  *implementing* the contract, and the document never shows that contract's method set, its
  parameter and result shapes, or its obligations. `store.ReadAll`, `store.Capabilities`,
  `store.Close` appear in sketches; append, stream read, paging and transaction binding do not.
  `microkernel.md` requires an extension point to declare "a contract - a protocol/interface with a
  stable, typed data shape in both directions"; D.13 asserts all four parts are satisfied while
  the first one is never written down.
- **Why this severity:** INV-019 ("a second store costs zero kernel diffs") is the phase's
  self-declared defining architectural test and it cannot be evaluated against an unspecified
  interface. The conformance suite - the phase's third deliverable - is a test of a contract that
  does not exist in the spec, so its sections in D.9 are testing behaviour whose call shape the
  plan will have to invent. Several open questions in this file (paging shape Q7, cancellation
  semantics, uncertainty reporting) are consequences of that absence.
- **Why this timing:** Phase 2 is defined as "runs the identical suite against a different
  factory". If the contract's shape is settled during implementation rather than specified, the
  zero-diff claim is decided by accident.
- **Close criteria:**
  - [ ] The document shows the store contract's shape from the implementer's side, at the same
        level of fidelity as the consumer sketch: every method, what it receives, what it returns,
        and which are optional capabilities.
  - [ ] For each method, the spec states what a store may refuse, what it must not convert, and
        what it may leave to the kernel (encoding, bounds, sealing, fold).
  - [ ] The compatibility note in D.13 is made concrete: what an additive method looks like and how
        an existing store keeps compiling.
  - [ ] A5 gets a DX subsection alongside A1-A6, or the spec states why the implementer's shape is
        out of scope for phase 1 and where it is decided.
- **Status:** fixed — §D.14 writes the contract out from A5's side: `Log`, `Store`, the optional `Transactional`, and every request/page struct; a kernel-versus-store division-of-labour table; per-method statements of what may be refused, what must never be converted and what is left to the kernel; and the note that the suite is the specification. §D.13's first required part now points at it and its compatibility note is made concrete (additive by new optional interface, never by a method on an existing one; decorators declare `Next()`; structs grow by field).

### GAP-11 [high][immediate] No invariant requires the per-stream read and the global read to describe the same set of committed events

- **Where:** UC-036/UC-038/UC-039 (the global read, lines 802-858); UC-010/UC-011 (the stream
  read); INV-009 (lines 1110-1125); INV-001..INV-032 (no conservation property anywhere).
- **What:** The document specifies two independent read paths and orders each of them, but never
  states that they are two projections of one set. Nothing forbids a store from making an event
  visible to a stream load and not to the global read (or the reverse), permanently or
  transiently. UC-038 states only that per-stream order is a *subsequence* of global order - a
  statement about order, not about membership.
- **Why this severity:** This is the conservation invariant the whole later pipeline rests on. An
  event that folds into an aggregate's state but never appears in the global read is a decision
  that no projector, outbox or export ever sees - silent, permanent divergence between the write
  model and everything downstream, with no test in the suite that could notice. Conversely a store
  that exposes an event globally that the stream read does not return breaks replay
  reproducibility. `data-integrity.md`'s "unambiguous state" and the coverage role's conservation
  check both land here.
- **Why this timing:** It is a store-contract obligation and a mandatory conformance section; both
  are being frozen now.
- **Close criteria:**
  - [ ] An INV states that for every committed append, the events it wrote are returned by both the
        stream read for that stream and the global read, and by no other stream's read.
  - [ ] Its falsification names a conformance section that appends across several streams and
        compares the union of stream reads with the global read for set equality.
  - [ ] The interaction with permitted position gaps and with a store that does not guarantee
        monotone visibility is stated (eventual set equality, and what "eventual" is bounded by).
- **Status:** fixed — §INV-034 states that every committed append's events are returned by that stream's read, by the global read and by no other stream's read, with the quiescent/cursor-bounded qualification for concurrent writers and gaps; a mandatory `conservation` conformance section asserts set equality of the union of stream reads against the global read; §UC-038 gains a note that its subsequence claim is about order and not membership.

### GAP-12 [high][immediate] Cancellation inside the commit window has no defined class, while INV-029 demands both classes survive

- **Where:** UC-018 (lines 492-503, cancellation on *load* only); UC-034 (lines 758-782);
  INV-029 (lines 1329-1336); D.9 conformance row "cancellation | always | A cancelled context
  refuses before any write and preserves the cancellation's identity" (line 1880).
- **What:** There is no use case for cancellation during an append, and the conformance row
  covers only the "before any write" case. The interesting case is the one every real store has:
  the context's deadline expires after the write has been issued and before the commit is
  acknowledged. INV-029 requires cancellation to reach the caller as cancellation and unknown
  commit to reach it as unknown commit, and the spec never says which of the two this is.
  UC-034's contract ("the append may or may not have happened") and a cancellation are mutually
  exclusive answers to the same event.
- **Why this severity:** The two classes have opposite caller obligations - a cancellation during
  a drain is ignorable, an uncertain commit means the caller must re-load and re-decide before
  doing anything else. Two store implementations will answer differently, the conformance suite as
  specified will pass both, and INV-029's falsification ("driving each of the three through two
  opaque decorators") never constructs the ambiguous case.
- **Why this timing:** It is part of the refusal vocabulary and of the store contract's
  obligations, both frozen in this phase.
- **Close criteria:**
  - [ ] A UC covers cancellation during an append, distinguishing "cancelled before any write" from
        "cancelled after the write was issued".
  - [ ] The spec states the precedence rule (an uncertain outcome outranks a cancellation, or the
        reverse) and why.
  - [ ] The conformance "cancellation" section gains the in-flight case, and INV-029's
        falsification is extended to it.
- **Status:** fixed — §UC-057 covers cancellation during an append and separates the before-write and after-write windows; the precedence rule (an uncertain outcome outranks a cancellation) is stated once with its asymmetry argument, in §UC-057, §INV-029 and §INV-037; the `cancellation` conformance row gains the in-flight case and §INV-029's falsification constructs the ambiguous case with the before-write control.

### GAP-13 [high][immediate] [ES] non-negotiable "delivery is at least once, consumers are idempotent, no exactly-once claim" is not restated - and UC-039 asserts the opposite word

- **Where:** [ES] lines 235-237 (the ninth non-negotiable invariant); §5 "From [ES]'s
  non-negotiable list" (INV-001..INV-014); UC-039 title and line 853 ("The concatenation is every
  committed event, in order, once").
- **What:** The spec claims to restate [ES]'s non-negotiable list "precisely enough to test", and
  twelve of the thirteen bullets have an INV. The at-least-once/consumer-idempotence bullet has
  none. It is not out of scope by virtue of "nothing runs": phase 1 ships the bounded read that
  every future consumer resumes from, and that read is exactly where the at-least-once property is
  either established or lost. The only text touching it - UC-039 - promises "exactly once", the one
  phrase [ES] forbids being claimed.
- **Why this severity:** The coverage role's mandate is that every non-negotiable appears testably
  restated; one is missing and the neighbouring use case contradicts it. A reader of this document
  will take "reads tile the log exactly once" as a delivery guarantee, which is how an
  exactly-once claim enters a system.
- **Why this timing:** It is a contract statement about the read that phase 2's projector inherits,
  and it is one word in a mandatory conformance section.
- **Close criteria:**
  - [ ] An INV restates the bullet for what phase 1 actually ships: what the bounded read
        guarantees about duplicates on resumption, and that a consumer must be idempotent.
  - [ ] UC-039's wording is changed so "exactly once" describes a single uninterrupted pass over a
        quiescent store and never a delivery guarantee.
  - [ ] The relationship to GAP-3's resume-point contract is stated in both places.
- **Status:** fixed — §INV-036 restates the bullet for what phase 1 ships: a walk tiles, delivery is at least once, consumers are idempotent, and no use of "exactly once" may denote delivery. §UC-039 is retitled and rescoped to a quiescent store with the delivery reading explicitly forbidden; §D.7 carries the same paragraph; the relationship to the cursor contract (§INV-035) is stated in both places.

### GAP-14 [high][immediate] The refusal vocabulary is never enumerated, so INV-024 and its mandatory conformance section cannot be tested

- **Where:** INV-024 (lines 1268-1282); INV-025 (lines 1284-1294); D.9 conformance row "refusal
  vocabulary | always | Every sentinel is reachable by `errors.Is`" (line 1879); UC-013, UC-014,
  UC-015, UC-016, UC-017, UC-024, UC-025, UC-031, UC-032, UC-033, UC-034, UC-047 (each describes
  "a typed refusal" without naming it or saying whether it is distinct from its neighbours).
- **What:** Twelve use cases produce a refusal; the spec says three things about the set (conflict
  wraps the existing conflict class, stale-view wraps conflict, oversized wraps the too-large
  class) and never lists it. Several use cases say a refusal must be "distinct from" another
  (UC-014 vs UC-013, UC-032 vs UC-033, UC-034 vs conflict) - which is a statement about a set
  nobody has written down. INV-024 declares that "every branchable failure is reachable with
  `errors.Is` against an exported sentinel" and its falsification is "a table test asserting each
  refusal's relationships in both directions"; the table is the missing artifact.
- **Why this severity:** A stated invariant with no enumerable subject, plus a mandatory
  conformance section that cannot be written. It also risks two use cases collapsing onto one
  sentinel during implementation (UC-013 unknown type and UC-014 unreadable revision are the
  obvious pair) which would silently delete a distinction the spec calls critical for rolling
  deploys.
- **Why this timing:** Sentinels are exported API. Adding or splitting one later is a public
  contract change, and Q1 already flags that the classes they wrap are a reconciliation question -
  which is answerable only against a list.
- **Close criteria:**
  - [ ] The document carries one table of this subsystem's refusals: name, the use case that
        raises it, what it wraps, and which other refusal it must be distinguishable from.
  - [ ] Each of the twelve refusal-producing use cases points at a row.
  - [ ] INV-024's falsification names the table, including the negative directions it must assert.
- **Status:** fixed — §2.3 enumerates the vocabulary: nineteen sentinels with the use case that raises each, what it wraps and what it must be distinguishable from. Every refusing use case gained a **Refusal** line pointing at its row; §INV-024's subject is now that table and its falsification walks every row in both directions; the D.9 `refusal vocabulary` row is rewritten against it.

### GAP-15 [medium][immediate] The actor-to-happy-path map points at the wrong use cases for five of seven actors

- **Where:** lines 196-197.
- **What:** "A3 -> UC-016, UC-024; A4 -> UC-032; A5 -> UC-038; A6 -> UC-036; A7 -> UC-043" is
  stale by an inconsistent offset. UC-016 and UC-024 are edge cases (a refusing upcaster, an
  unencodable payload), UC-032 is an A3 transaction edge, UC-038 is an A4 read, UC-036 is an A4
  read, UC-043 is A5's. The correct happy paths are A3 -> UC-019/UC-028, A4 -> UC-036, A5 ->
  UC-043, A6 -> UC-040, A7 -> UC-048.
- **Why this severity:** Local inaccuracy, but it is the document's only evidence for its own
  claim that "every actor has at least one happy path before any failure path", and the phase-3
  plan's coverage matrix is built from exactly this mapping.
- **Why this timing:** Cheap now; it silently seeds a wrong coverage matrix into the plan.
- **Close criteria:**
  - [ ] Every row of the map resolves to a use case whose actor matches and whose tag is `[happy]`.
- **Status:** fixed — the map now reads A1 → UC-001, UC-002, UC-003; A2 → UC-006, UC-007; A3 → UC-019, UC-028; A4 → UC-036; A5 → UC-043; A6 → UC-040; A7 → UC-048, and every row resolves to a `[happy]` use case whose actor matches.

### GAP-16 [medium][immediate] Three broken cross-references, one of which loses an open question entirely

- **Where:** §1 non-goal 11 line 83 ("§UC-024, §INV-3" - UC-024 is the unencodable-payload edge;
  the transaction use case is UC-028, and the invariant is INV-003); §1 non-goal 14 line 92 ("An
  append idempotency key. Argued and rejected in §UC-029" - the argument is in UC-035; UC-029 is
  the transaction rollback); UC-035 line 797 ("Recorded as an open question (Q10)" - Q10 is
  "Numbers that need a real consumer"; no question in Q1-Q15 is about an idempotency key).
- **What:** As written, a decision the document explicitly says it is *not* closing is recorded
  nowhere on the reconciliation agenda, and two non-goals cite arguments that are not where they
  say they are.
- **Why this severity:** The open-questions section is defined as "the reconciliation agenda"; an
  item that claims to be on it and is not will be dropped at the phase boundary, which is exactly
  the loss `gaps.md` forbids for deferred items.
- **Why this timing:** The agenda is consumed by the next phase.
- **Close criteria:**
  - [ ] Non-goal 11 and non-goal 14 cite the sections that carry their arguments.
  - [ ] The idempotency-key question is added to §7 with its own number, or UC-035 is rewritten to
        say the decision is closed.
- **Status:** fixed — non-goal 11 cites §UC-028 and §INV-003; non-goal 14 no longer claims the argument is closed and points at Q16; UC-035's citation is corrected from Q10 to Q16; **Q16** is added to §7 so the idempotency-key decision is on the reconciliation agenda in its own right.

### GAP-17 [medium][immediate] INV-002's scope contradicts the empty append

- **Where:** INV-002 (lines 1031-1041); UC-020 lines 538-540 ("must not be reported as evidence
  that the caller's view was current - because it performed no version check"); INV-031 line 1354.
- **What:** INV-002 says, without qualification, that an append is admitted only if the committed
  version equals the view's version and that there is "no parameter, overload, option or flag that
  skips the check". An append of zero changes skips the check by design. Read literally, the
  phase's own chosen behaviour falsifies its second invariant.
- **Why this severity:** An invariant whose scope is wrong is untestable as stated and invites an
  implementation that "fixes" it by making the empty append hit the store, which UC-020 and INV-031
  forbid.
- **Why this timing:** Invariant text is what the conformance sections are named after.
- **Close criteria:**
  - [ ] INV-002's statement is scoped to an append of at least one change, with the empty case
        pointed at INV-031.
  - [ ] Q5 records that the scope carve-out is the cost of the no-op choice.
- **Status:** fixed — §INV-002 is retitled and scoped to an append of at least one change, with the carve-out stated explicitly and pointed at §INV-031, and with the warning against "fixing" it by sending an empty append to the store. Q5 records that the carve-out is the cost of the no-op choice and what happens to INV-002 if the owner picks the refusal.

### GAP-18 [medium][immediate] `Durability` names two different things with two different value sets

- **Where:** D.6 lines 1740-1744 ("durability is a value on the receipt... committed now; pending
  the caller's transaction; volatile"); D.7 lines 1780-1787 (`Durability // Volatile | Durable` as
  a field of `Capabilities`); UC-019 (a receipt names "its durability class"); Q8 (asks whether
  `Durability` needs a fourth value, without saying which of the two it means).
- **What:** One name is used for a per-commit classification with three values and for a
  per-store capability with two. Q8 conflates them.
- **Why this severity:** Weak naming in a public API, and the ambiguity has already propagated
  into an open question that cannot be answered as asked.
- **Why this timing:** Both names are exported and both are read by conformance sections.
- **Close criteria:**
  - [ ] The two concepts carry two names, with their value sets stated once each.
  - [ ] Q8 is restated against whichever one it is about.
- **Status:** fixed — the two concepts carry two names with their value sets stated once each: `Commit.Durability` (Committed | PendingCaller | Volatile) per commit, `Capabilities.Persistence` (Volatile | Durable) per store, in §D.6, §D.7 and the D.9 durability row. Q8 is restated as a question about the receipt only, with this document's answer and why.

### GAP-19 [medium][immediate] The only store-spec surface shown declares one of the four bounds INV-022 requires

- **Where:** D.4 lines 1578-1582 (`Codec`, `MaxPayload`, `Clock`); INV-022 (lines 1247-1256:
  payload, append batch, stream read, global read, declared identifier length); UC-011 (page size
  is "a declared store setting"); UC-025 (a "declared batch bound"); Q7 and Q10.
- **What:** INV-022 requires five bounds, all declared and none escapable. The spec's only
  declaration surface carries one. The batch bound, the read page size and the identifier length
  caps have no home, and identifier caps arguably belong to the declaration rather than the store.
- **Why this severity:** An invariant with a stated falsification ("a test per bound, each with an
  at-the-bound control") whose subjects are not all declarable anywhere.
- **Why this timing:** The store spec is a public struct and the bounds section of the conformance
  suite is mandatory.
- **Close criteria:**
  - [ ] Every bound INV-022 names has a declaration site, with its default and its zero-value
        meaning stated ("zero means the declared default", never unbounded).
  - [ ] The identifier-length caps are placed on whichever side owns them, and UC-004's
        over-long-identifier refusal points at that place.
- **Status:** fixed — §INV-022 carries a table of six bounds, each with its declaration site, its default and its zero-value meaning; the store spec in §D.4 shows `MaxPayload`, `MaxBatch`, `MaxKey`, `StreamPage` and `MaxRead`; the declared-identifier cap is placed on the kernel as a constant with the argument for why it is not a deployment setting, and UC-004's over-long-identifier refusal points there. Q10 is widened to the five numbers that need a derivation.

### GAP-20 [medium][immediate] Close semantics are left to each store, so a caller cannot write one correct shutdown

- **Where:** UC-046 line 958 ("close is idempotent or refuses a second call, and says which");
  INV-032 (lines 1361-1365); D.9 conformance row "lifecycle" (line 1882).
- **What:** The contract offers two behaviours and delegates the choice to the implementation.
  Every caller who writes `defer store.Close()` and also closes explicitly on a shutdown path -
  the ordinary shape - gets different results per store, and the conformance suite cannot assert
  either.
- **Why this severity:** A caller-visible behavioural difference between two implementations of one
  contract is exactly what the conformance suite exists to eliminate; leaving it open makes the
  suite's lifecycle section unwritable in that respect.
- **Why this timing:** It is a one-line contract choice that both stores and the suite are built on.
- **Close criteria:**
  - [ ] The contract picks one (idempotent close is the conventional answer) and INV-032 states it.
  - [ ] The lifecycle section asserts it for every store.
- **Status:** fixed — the contract picks idempotent close, with the argument, in §UC-046; §INV-032 is retitled and states it; the D.9 lifecycle row asserts it for every store. The two-behaviour version is added to §D.12's rejected list with the reason (`defer Close()` plus an explicit shutdown close is correct against one store and broken against the other).

### GAP-21 [medium][immediate] The bounded consumer is handed the whole store, including its append surface

- **Where:** D.7 line 1754 (`store.ReadAll(...)` called on the store value); actor A4 line 190
  ("What it may hold: a position it obtained from a previous read"); UC-036.
- **What:** A4's stated holdings are a position, yet the only way to perform the read is to hold
  the `Store` itself, which also appends, binds transactions and closes. There is no read-only
  projection of the store in the document.
- **Why this severity:** Least privilege on the seam that phase 2's projector and outbox relay
  will be built against; a projector that can append is how a replay writes.
- **Why this timing:** It is a contract shape (a `ReadSource`-style narrowing) that later phases
  inherit; retrofitting it changes what the composition root hands out.
- **Close criteria:**
  - [ ] The spec states what a bounded consumer holds, and either introduces a read-only view of
        the store or records why the full store is acceptable in phase 1.
  - [ ] A4's row in the actor table matches whatever the answer is.
- **Status:** fixed — `Log` (Capabilities + ReadAll) is the read half of the contract in §D.14, `Store` embeds it, a consumer's constructor names `event.Log`, and `event.ReadOnly(store)` exists for the case where the consumer must not be able to assert its way back. A4's actor row now says it holds a cursor and a read-only `Log`; §UC-036 and §D.7 are written against it.

### GAP-22 [medium][immediate] Fold and upcaster purity is asserted as an invariant Go cannot enforce, and its falsification only detects nondeterminism

- **Where:** INV-004 (lines 1055-1067); UC-010 line 379; UC-012 line 412.
- **What:** "Folds perform no I/O, read no clock, draw no randomness, emit no telemetry" is a
  property of application-supplied functions. The framework cannot prevent any of it, and the
  stated falsifications (fold twice and compare; fold under an expired deadline) detect only the
  nondeterministic subset - a fold that writes to a file or increments a counter passes all three.
  The spec does not say this.
- **Why this severity:** An invariant the system cannot hold is a claim, not a property. The plan
  will either try to enforce it (and cannot) or quietly drop it.
- **Why this timing:** It changes how INV-004 is written and which conformance section, if any,
  owns it.
- **Close criteria:**
  - [ ] INV-004 states which half is enforced by the framework (the fold's signature, its inputs,
        the absence of a context or store in its parameters) and which half is an application
        obligation.
  - [ ] The obligation half names the checkable proxy (determinism across two replays) and admits
        what it cannot see.
- **Status:** fixed — §INV-004 is split into what the signature holds (no context, no store, no clock, no logger, no error channel), what is an application obligation (a fold that writes a file compiles and runs, and the framework cannot see it) and the checkable proxy (determinism across two replays, which catches only the nondeterministic subset). A source check on the signatures joins the falsifications.

### GAP-23 [medium][immediate] Re-appending the same change slice after a success silently duplicates facts, and D.10 claims it cannot happen

- **Where:** D.10 line 1907 ("A retry loop. The view refuses after a conflict, so the loop cannot
  be written by accident"); UC-021 (lines 547-555); D.3 (`Change` is a value the caller holds).
- **What:** The view is poisoned only by a conflict. After a *successful* append the view stays
  usable and the caller still holds the `[]event.Change` it just wrote; appending it again succeeds
  at the advanced version and records the same facts twice. Nothing says whether a change or a
  change slice is single-use, and no use case covers a duplicate change within one batch either
  (which is legitimate - two identical credits - and therefore must be distinguished from the
  accidental case in writing).
- **Why this severity:** D.10 is the document's summary of what the caller "never can" do, and this
  is one of the shapes an outer retry (`for attempts { ...; if err != nil { continue } }` around a
  block that decides once and appends inside the loop) produces.
- **Why this timing:** Whether a `Change` is consumed by a successful append is a contract property
  of the central value type.
- **Close criteria:**
  - [ ] The spec states whether a `Change` value may be appended more than once and what happens.
  - [ ] A UC covers a batch containing two equal changes and states that it is legal.
  - [ ] D.10's retry-loop claim is narrowed to what the shape actually prevents.
- **Status:** fixed — §D.3 states that a `Change` is an ordinary value, a successful append does not consume it, and re-appending the same slice records the facts again; §UC-058 covers a batch of two equal changes as legal; §D.10's retry-loop claim is narrowed to the loop after a conflict or an uncertainty, with the outer re-append shape spelled out and the reason the framework cannot detect it. Marking a change consumed is added to §D.12's rejected list.

### GAP-24 [medium][immediate] The non-panicking declaration sibling has no shape and no use case

- **Where:** UC-004 lines 278-279 ("A non-panicking sibling exists for callers that want to build
  declarations dynamically and handle the error"); E1 (line 203, declaration is package-level);
  INV-013 (the type table's mutability window).
- **What:** One sentence introduces a second, error-returning declaration path and nothing else in
  the document mentions it: no shape (an error-returning `Declare` cannot initialise a package-level
  `var` without an `init`), no use case, no actor, and no statement of how dynamically built
  declarations interact with sealing (GAP-9) or with the compile-time aggregate binding the whole
  DX rests on.
- **Why this severity:** A public API surface introduced in a subordinate clause. It is also the
  one path by which a declaration could be built after other goroutines exist.
- **Why this timing:** It is exported API and it touches the sealing rule.
- **Close criteria:**
  - [ ] Either a use case and a shape for the dynamic path, with its sealing and concurrency story,
        or its removal from UC-004.
- **Status:** fixed by removal — §UC-004 states there is no error-returning sibling, with the argument (a declaration binds Go types at compile time, so there is nothing dynamic to build; the return would exist only to be discarded by an `init` and would give a second, unsealed way to construct a table) and with what replaces it: the panic's value is an error carrying `ErrDeclaration`, so a recovering test matches with `errors.Is`. The shape is also in §D.12's rejected list.

### GAP-25 [medium][deferred] UC-045's "each defect fails exactly one named section" is false for its own first example

- **Where:** UC-045 (lines 936-948), especially "Each defect fails exactly one named section"; D.9
  conformance sections "expected version" and "concurrency" (lines 1872-1873).
- **What:** A store that ignores the expected version fails the "expected version" section *and*
  the "concurrency" section, which asserts one winner among N writers at one version. The
  requirement as written cannot be satisfied by the suite as designed.
- **Why this severity:** A falsifiable claim in a use case that is false; it will make the
  conformance suite's sanity check unwritable as specified.
- **Why this timing:** It is a property of the suite's section design, fixable when the suite is
  written; nothing else depends on the exact wording.
- **Close criteria:**
  - [ ] UC-045 requires each defect to fail at least one *named* section and no unrelated section,
        or the sections are partitioned so the claim holds.
- **Status:** open (deferred) — carried into the spec's new §8 Deferred. UC-045's wording is relaxed now to "at least one named section and no unrelated section", which is true and testable; the stronger option — partitioning the sections so each defect maps to exactly one — is left to when the suite is written, since nothing outside it depends on the answer. **Amended in the round-10 resolution:** GAP-95 forced the six defects to be checked one by one, and *no unrelated section* is false for three of them, not one. §UC-045 now ships the claim the suite can hold — *each defect fails the section named for it* — with the three exceptions named there; §8's entry says so; and what remains deferred is unchanged, the section partitioning that would let a stronger claim be made.

### GAP-26 [low][deferred] `event.Replay` returns an error whose origin the document never names

- **Where:** D.8 line 1838 (`state, err := event.Replay(accounts, opened.New(...), ...)`); D.11
  line 1930; INV-017 (a fold cannot fail); UC-041.
- **What:** Replay applies folds to changes that are already typed and already bound to the
  aggregate. INV-017 forbids folds failing and the payloads are not encoded on this path (UC-041),
  so no failure mode in the document produces this error. If it exists for sealing or for a nil
  declaration, the spec should say so; if it does not, the signature should not carry it.
- **Why this severity:** Cosmetic in effect, but an error return with no reachable cause teaches
  every caller to write a branch that cannot be taken.
- **Why this timing:** Signature detail with no dependents.
- **Close criteria:**
  - [ ] The spec names Replay's failure mode or drops the error from the sketch.
- **Status:** closed — see `## Resolution — seam redesign`. Round 4 deletes `event.Replay` and gives the one surviving fold named causes, so the error is no longer a branch that cannot be taken. The spec's §8 records that GAP-26 leaves the deferred list; this line is corrected to match it.

---

**Checked and found sound** (so the absence of a finding is deliberate, not an omission): the
actor list itself (A1-A7 cover every party phase 1 has) and entry modes E1-E6; twelve of [ES]'s
thirteen non-negotiables have a testable restatement (the thirteenth is GAP-13); the
never-written-stream-versus-version-0 collapse (UC-009) is correct and well argued; the empty
append (UC-020/INV-031) is decided with its cost stated and recorded as Q5; the partial-rehydration
family (UC-013..UC-017, INV-006) is complete across unknown type, unreadable revision, malformed
bytes, refusing upcaster and oversized payload, each with the control that the clean stream loads;
the paging equivalence control at page size 1 (UC-011) is the right falsification; the
compile-time cross-aggregate refusal (UC-002/UC-026/INV-020) is a genuine build-failure fixture and
not a runtime check; the "no manufacturable view" claim (UC-027/INV-015) is the strongest part of
the document and holds; the conflict-carries-no-version rule (INV-026) is correct and its reason is
the right one; the declaration-time refusals (UC-004) are correctly placed under [[D-021]] except
for the codec-dependent subset (GAP-7); the D.11 inference table is honest and each of its claims
is achievable in Go as written (partial type-argument lists with the explicit parameters first,
inference of the chain's element type through `Then`, method-level inference on a fully
instantiated `*Fact`), and the one place inference stops - no method-level type parameters, hence
`Then(From[V1](), up)` rather than a fluent chain - is admitted rather than hidden; the rejected
alternatives in D.12 each carry a real argument; the 64 KiB payload default is a literal but is
correctly flagged as needing a derivation in Q10, which is what `universality.md` asks for when the
general answer needs a consumer.

---

## Round 2 resolution - 2026-09-06

All fourteen `[immediate]` findings of round 2 are closed in
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` itself; each `Status`
line below names what closed it and where. GAP-41 stays `open (deferred)` here,
as `gaps.md` requires, and is carried into the spec's `## 8. Deferred` beside
GAP-25 and GAP-26 so the phase boundary cannot drop it.

**One premise is rejected on the record: GAP-31's.** The finding is right that
two aggregates may declare one family, that nothing in the document forbade it,
and that the failure is byte for byte INV-033's corruption reached by a route
INV-033's scope excludes — all of that is fixed. What it gets wrong is the claim
that nothing *can* detect it, "because INV-013 forbids the package-level registry
that would be the only place two independent declarations meet". A registry is not
the only such place. The composition root binds every aggregate it uses to a
store, so `Bind` is a second meeting point, and a set of bound families held on a
**store value** is per-value state with the caller's own lifetime — not the
package-level mutable state INV-013 forbids, and not import-order dependent,
global, or untestable in isolation. So the ordinary route — a copied declaration
whose family string was not changed, both bound at the composition root — is
caught by a mechanism rather than left to an obligation. The residual the finding
is right about (two processes, two deployments) keeps the obligation and gets a
runnable proxy. The argument is written into §UC-059 and §INV-039, and the
residual is on the agenda as Q19. The same reading of INV-013 also resolves
GAP-35's "obvious escape ... closed by INV-013": a memo on a store value is not a
global, which the finding's own third close criterion already allowed.

Two findings were closed more strongly than their close criteria asked, and the
plan should know it. **GAP-30**: rather than giving `event.Since` a use case, a
refusal and a row, the spelling is **removed from phase 1** — it is the only
hand-typed number in the design, its failure mode is a successful misdecode, and
retention is additive, so shipping it untested buys nothing (`restrictions.md`
§1). **GAP-28**: the walk is not merely documented as transitive, it is argued to
terminate without a visited set or a depth bound, because every cycle in a Go type
graph passes through a reference kind and each of those ends the walk with "copier
required".

Two public contracts moved, and phase 2 inherits both. `Store` gained `Codec()`
and `Limits()` (GAP-27), without which `Bind`'s refusal and the mandatory `bounds`
section were unwritable; and `Load` returns a `*View` rather than a value
(GAP-32), without which the view's state machine was escapable by assignment.

## Round 2 - econv-usecase-validator (coverage + invariant + DX roles, re-audit) - 2026-09-06

Sources read: `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (whole file, 3287 lines),
`.agents/artifacts/gaps/EVENTSOURCE_P1_USECASES_GAPS.md` (round 1, whole file),
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` (whole file), `CLAUDE.md`,
`docs/ai/decisions/Index.md`, `~/.claude/skills/econv/SKILL.md`, `references/gaps.md`,
`references/universality.md`, `references/restrictions.md`. No `.go` file was opened.

**Round 1's twenty-four `[immediate]` findings were re-checked one by one against the round-2 text,
not against the resolution notes.** All twenty-four are genuinely closed, not reworded: GAP-1 by
§2.1 + UC-050 + UC-051 + INV-033 + the INV-022 key row; GAP-2 by UC-052 + INV-021's lifetime clause
(but see GAP-28 below, which is the same failure one level of nesting down); GAP-3 by the minted
`Cursor` + INV-035 made mandatory + `AllQuery` having nowhere to put a position; GAP-4 by the
`BoundTo` predicate + Q17 — the rejection of its premise is argued and I agree with the rejection;
GAP-5 by `Backing` + UC-054 + UC-055; GAP-6 by INV-037's table (but it is not total — GAP-29);
GAP-7 by UC-056 moving the check to `Bind` (which is now unimplementable through the contract —
GAP-27); GAP-8 by making the binding store-private and saying what that costs; GAP-9 by sealing on
first observation with the reader list; GAP-10 by §D.14 (incomplete — GAP-27); GAP-11 by INV-034;
GAP-12 by UC-057's two windows and the precedence rule; GAP-13 by INV-036; GAP-14 by §2.3's
nineteen rows (one class is missing — GAP-29); GAP-15 by the corrected actor map; GAP-16 by the
corrected citations and Q16; GAP-17 by INV-002's carve-out; GAP-18 by `Commit.Durability` versus
`Capabilities.Persistence`; GAP-19 by INV-022's six-row table; GAP-20 by choosing idempotent close
(which now contradicts itself — GAP-33); GAP-21 by `Log` + `event.ReadOnly`; GAP-22 by INV-004's
three-way split; GAP-23 by UC-058 + the narrowed D.10 claim; GAP-24 by removal.

Checks run this round: every actor and entry mode against a `[happy]` use case; all thirteen [ES]
non-negotiables against a testable INV (all thirteen are present now); the edge matrix (empty /
malformed / oversized / ambiguous / duplicated / multilingual / partially failing dependency /
timeout / zero results / everything-matches / conflicting signals / crash / cancellation /
zero-length batch / duplicate-in-batch / absent-stream-versus-version-0); the three
`universality.md` checks against every mechanism the sketch shows, with the second-instance test run
on a nested state type, a state type that *is* a reference kind, a composite non-string identity, an
event type with five retained revisions and a truncated retention; a UC-versus-UC and UC-versus-INV
contradiction sweep; Go-level feasibility of every row of D.11; and the microkernel four-part
extension-point test against §D.14 rather than against §D.13's claim about it.

Eight of the fifteen findings below were **introduced by round 2's own fixes** (GAP-27, GAP-28,
GAP-29, GAP-33, GAP-34, GAP-35, GAP-37, GAP-40), which is the case `gaps.md` predicts when it
requires a fresh audit after every fix round.

### GAP-27 [high][immediate] The store contract in §D.14 cannot carry the codec or the bounds the kernel is required to enforce, and `Codec` is never written out at all

- **Where:** §D.14 lines 2984-3037 (`Log`, `Store`, `Transactional`, and the request/page structs);
  §D.14's division-of-labour table lines 3041-3049; §D.13 line 2940 ("a contract (`Store`, `Log`,
  `Codec`, typed both ways — **written out in D.14**)"); §D.4 lines 2364-2373 (`Codec`,
  `MaxPayload`, `MaxBatch`, `MaxKey`, `StreamPage`, `MaxRead` live on `eventmemory.Spec`);
  §INV-022's table lines 1776-1783 ("Declared where: the store spec"); UC-056; UC-051; UC-017;
  UC-024; UC-025; UC-011.
- **What:** The kernel's stated obligations are, verbatim from D.14: "Encoding and decoding payloads
  through the codec", "Every bound: payload, batch, key, page, identifier", "Validating the key the
  mapper produced". `AppendRequest.Records` is "already encoded, already within every bound" and
  `AppendRequest.Stream` is "already validated". So the kernel — `event.Bind` and the `Repository`
  it returns — must obtain, from a value of type `event.Store`, this store's codec and this store's
  five numbers. The `Store` interface exposes `Capabilities`, `ReadAll`, `Backing`, `Append`,
  `ReadStream`, `Close`, and `Capabilities` holds five booleans and an enum. There is no accessor
  for either. The bounds and the codec live on `eventmemory.Spec` — a struct of the *store's own
  package*, which the kernel cannot name without importing every store implementation, which is the
  inversion INV-018/INV-019 exist to forbid. Separately, D.13 asserts the extension point's first
  required part is satisfied because `Codec` is "written out in D.14"; D.14 does not contain the
  word `Codec` in any declaration, so the type carrying the load-bearing question ("can you encode
  this type deterministically", D.4 line 2393) has no shape anywhere in the document.
- **Why this severity:** UC-056 is a whole use case — a start-up refusal at `Bind`, the round-1 fix
  for GAP-7 — and it is not expressible against this contract: `Bind(store, agg)` has no way to
  reach the codec it must interrogate. The mandatory `bounds` conformance section ("Payload cap,
  batch cap, key cap, read caps... each with an at-the-bound control") is likewise unwritable,
  because the suite receives an `event.Store` from the factory and cannot ask it what its caps are.
  An implementer resolving this the cheap way moves encoding and bound-checking into each store,
  which contradicts D.14's own table, moves `ErrTooLarge`/`ErrBatch`/`ErrEncode`/`ErrKey` from the
  kernel into N stores, and makes "the store never sees a Go type" (INV-018) the only thing left of
  the division of labour. A second consequence is already visible in the text: UC-054 says two store
  values over one backing make streams "visible through the other", which is false when the two
  values were constructed with different `MaxKey` or `MaxPayload` — nothing in the document forbids
  that, and nothing detects it.
- **Why this timing:** `Store`, `Log` and `Capabilities` are declared frozen at the end of phase 1
  (D.13's compatibility note), and phase 2 is defined as "runs the identical suite against a
  different factory". A missing accessor discovered during implementation is a change to a frozen
  public interface that every store and the suite depend on.
- **Close criteria:**
  - [ ] The contract states how the kernel obtains this store's codec, as a method on `Store` (or on
        an optional interface), with the answer stable for the store's life.
  - [ ] `Codec` is written out at D.14's fidelity: name, encode, decode, and the "can you encode this
        type deterministically" question, with what it may refuse.
  - [ ] The five bounds INV-022 places on "the store spec" are reachable from an `event.Store` value,
        or INV-022's table is corrected to say where they actually live and D.14's division-of-labour
        row for bounds moves with them.
  - [ ] The suite's `bounds` section is shown to be writable against whatever the answer is: it must
        be able to ask an arbitrary store for its caps to construct the at-the-bound control.
  - [ ] UC-054's "visible through the other" states what it requires of the two store values'
        specs, or the contract makes bounds a property of the backing rather than of the value.
- **Status:** fixed — the contract carries both values. §D.14 adds `Codec()` and `Limits()` (with `Limits` and `Codec` written out at the same fidelity as the rest, `CanEncode` taking a zero value so `reflect` stays out of the exported contract), and moves `Limits()` and `Backing()` onto `Log` so the read half and the suite can reach them. §INV-018 is amended to say what the store *carries and never applies*, with the argument that the alternative — the kernel naming `eventmemory.Spec` — is the import inversion it exists to forbid. §INV-022's table gains a "read from" column, `Limits()` for every store-owned bound. §D.4 says why the store hands them back, §D.9's `bounds` row builds each case from `store.Limits()` rather than a literal, and §UC-056 says where `Bind` gets the codec. §UC-054 states what two values over one backing must agree on (codec, `MaxPayload`, `MaxKey`, `KeyDomain` — refused at construction otherwise) and what they need not (the operational numbers). Both alternatives are in §D.12's rejected list.

### GAP-28 [critical][immediate] The copier requirement is stated over the state type's *fields*, so a nested reference kind — or a state type that is itself one — escapes it silently

- **Where:** UC-052 lines 391-419 ("At declaration the framework walks the state type's fields. A
  reference kind — slice, map, pointer, channel, function, interface — makes a copier **required**
  ... refused, naming the field"); §D.1 lines 2214-2218 ("The declaration walks the state type's
  fields. A slice, map, pointer, channel, function or interface field makes `event.Copy`
  **required**"); INV-021 lines 1745-1769; §D.0 line 2081 and §D.1 line 2203 (both example states
  are flat).
- **What:** The rule is written twice and both times over *fields of the state type*, with the
  refusal "naming the field". A `struct` field is not one of the six listed kinds, and neither is an
  array. So the state type almost every real aggregate has one level down —
  `type Cart struct{ Header CartHeader; Totals Totals }` with `CartHeader{ Tags []string }`, or
  `type Order struct{ Items [8]Item }` with `Item{ Meta map[string]string }` — requires no copier,
  is accepted at declaration, and `State()` hands out a value sharing the nested slice header and
  map header with the view's live accumulator. A state type that is *itself* a reference kind
  (`type Ledger map[string]int64`, `type Log []Entry`) has no fields at all, so the walk finds
  nothing, requires nothing, and every `State()` returns the same map header.
- **Why this severity:** This is exactly GAP-2's failure, unchanged: a decision function writes into
  a nested map, the view's state advances without a fact being appended, the next fold runs on a
  state no event sequence produces, the receipt reports a version the state does not correspond to,
  and a reload disagrees with the live view — with no error anywhere and no invariant violated as
  written. It is also the `universality.md` second-instance test failing on the first structurally
  different instance: the mechanism is fitted to the two flat states the document shows
  (`Account{bool,string,int64}` and `Order{[]Line, map[string]bool}`) and needs a new rule for the
  next one. Round 1 rated this failure `[critical]`; the mechanism now exists but its stated domain
  does not cover the ordinary case.
- **Why this timing:** It is the declaration seam's refusal rule and the semantics of `State()` —
  public contract, and the thing every aggregate author meets on day one. It is a
  `universality.md` finding, which that law forbids deferring.
- **Close criteria:**
  - [ ] The rule states whether the walk is transitive over field types, and what it does with a
        nested struct, an array of structs, an embedded field, an unexported field of a reference
        kind, and a recursive type (a bound or a cycle rule, so the walk terminates).
  - [ ] The rule states what happens when the state type is itself a slice, map or pointer, so the
        no-fields case is not silently exempt.
  - [ ] UC-052 carries an example that is nested rather than flat, and INV-021's falsification uses
        a nested state so the test cannot pass on a shallow walk.
  - [ ] The refusal text's "naming the field" is restated as naming the *path* to the offending kind,
        since with a transitive walk the field is not necessarily top-level.
- **Status:** fixed, and the termination argument is stronger than the criteria asked. §UC-052 is retitled ("at any depth") and rewritten: the walk is over the state type's **type graph** — struct fields, embedded fields, unexported fields, array element types — and starts at the state type itself, so `type Ledger map[string]int64` is covered by the no-fields case. The refusal names the **path** (`Order.Header.Tags`). Termination needs no visited set and no depth bound, because every cycle in a Go type graph passes through a pointer, slice, map or interface and each of those ends the walk with "copier required" — that argument is in §UC-052 and §D.1. §UC-052's example is nested and §INV-021's falsification requires a nested state, so a shallow walk fails it; §INV-021's statement says "at any depth" and enumerates the four places. The field-level rule is in §D.12's rejected list with what it exempts.

### GAP-29 [high][immediate] A backend failure is admitted by the store contract, has no refusal row, no view transition, and no rule about whether it may have landed

- **Where:** §D.14 line 3053-3057 (`Append` "may refuse ... with a transaction refusal, with closed,
  or with a genuine backend failure"), lines 3061 and 3064 (`ReadStream`/`ReadAll` "refuses only for
  cancellation, closed or a backend failure"); §2.3 lines 202-223 (nineteen rows, none of them a
  backend failure); INV-024 lines 1815-1837 ("Every branchable failure is reachable with `errors.Is`
  against an exported sentinel of this subsystem", subject = "§2.3's table **and nowhere else**");
  INV-037 line 2052 (the `unchanged` row's enumeration) and line 2045 ("every outcome of an append
  moves it deterministically").
- **What:** The store contract names a failure class the refusal vocabulary does not contain and the
  view's state machine does not place. INV-037 claims totality — "A view is in exactly one of five
  states, and every outcome of an append moves it deterministically" — and its `unchanged` row is an
  explicit list of nine triggers that does not include a backend failure. So for the one append
  outcome that a real store produces most often after a conflict, the document says neither which
  sentinel the caller matches nor whether the view may be used again.
- **Why this severity:** The two things a caller must distinguish are "the write certainly did not
  land" (retry or re-decide freely, view `unchanged`) and "the write may have landed" (`uncertain`,
  reload and re-decide). A driver error is sometimes the first and sometimes the second, and only
  the store knows which — which is precisely the argument UC-057 makes for cancellation, and it is
  not made here. An implementer who leaves the view usable after a backend error that had in fact
  issued the write produces the silent double write the whole GAP-6 fix exists to prevent. There is
  a second, concrete instance in this repository's own taxonomy: [[D-040]] says a lock timeout,
  deadlock or serialisation failure is a retryable class and never a client error. Phase 2's
  PostgreSQL append will produce serialisation failures routinely, and phase 1 gives them no
  sentinel, no wrapping rule and no view state — so a caller cannot tell a retryable backend failure
  from a conflict from an uncertainty, which are three different obligations.
- **Why this timing:** Sentinels are exported API and INV-024's subject is declared closed. The view
  state machine and the conformance `view transitions` and `refusal vocabulary` sections are being
  frozen now, and phase 2 inherits both.
- **Close criteria:**
  - [ ] §2.3 gains a row (or an explicitly stated pass-through rule) for a store's own failure,
        saying what it wraps — including whether it may carry the framework's existing retryable
        class — and what it must be distinguishable from.
  - [ ] The store contract states that a store must classify every failure it returns as
        certainly-not-written or unknown, and that "unknown" is `ErrUncertain`, so no third answer
        exists.
  - [ ] INV-037's `unchanged` row either includes a certainly-not-written backend failure or a sixth
        state is added; the conformance `view transitions` section drives it.
  - [ ] §2.3 states how the vocabulary grows, since D.13's compatibility note covers interfaces and
        structs but not the sentinel set.
- **Status:** fixed — §UC-060 covers a store's own failure and makes the classification part of the contract: **certainly-not-written or unknown, with no third answer**, because only the store knows which window it was in. §2.3 gains the `ErrBackend` row, wrapping the store's own error and carrying the framework's retryable class when the store classified it retryable ([[D-040]]'s taxonomy, named); §INV-037's `unchanged` row is restated as a rule ("the write never reached the backing") with the certainly-not-written failure enumerated in it, and the unclassifiable case joins `uncertain`; §D.5's table gains the row and §D.14's `Append` bullet states the obligation. §2.3 now says how the set grows (adding a row is additive and safe; splitting or merging is breaking), and D.13's compatibility note carries it too. The conformance suite gains an optional `Fail` hook and a `store failure classification` section, and says plainly that without the hook it reports the section as not certified rather than passing it.

### GAP-30 [high][immediate] `event.Since[V2](2)` re-introduces a hand-typed revision number in a subordinate clause, with no use case, no refusal and a silent misdecode

- **Where:** §D.2 line 2259-2261 ("A history whose earliest revision is no longer retained writes
  `event.Since[V2](2)` — phase 1 provides the spelling so that phase-2 retention is not precluded,
  and tests nothing about archival"); §1 non-goal 13 lines 106-108; §D.2 lines 2247-2250 ("The
  current revision is the chain's length — derived, never stated, therefore never wrong" and "A
  revision number cannot be typo'd, because there is no revision number to type"); INV-010; §D.11
  (the inference table does not list `Since`); §2.3 (no refusal for it).
- **What:** The chain's whole safety argument is that the revision number is derived from the chain's
  position and therefore cannot be wrong. `Since[V2](2)` is the one constructor that takes the number
  as an argument, and it silently shifts the meaning of every position in the chain. It appears once,
  in a clause, with no use case, no actor, no declaration-time validation, no entry in D.11, no row
  in §2.3, and the document says it "tests nothing".
- **Why this severity:** Write `Since[V2](3)` where the earliest retained revision is really 2 and
  the chain is off by one for its whole life. An envelope at revision 3 is decoded with revision 2's
  reader — and because revision N+1's payload is usually revision N's plus a field, a JSON decode of
  the newer bytes into the older struct *succeeds*, drops the new field, and folds a wrong state.
  There is no refusal at any point. That is silent history corruption produced by a one-character
  typo in the only place the design admits a number, in a document whose stated property is that no
  such number exists. It is also `restrictions.md` §1's forbidden shape: exported public API added
  "for later", untested by the phase that ships it.
- **Why this timing:** It is exported API on the declaration seam, and the alternative (drop the
  spelling and let phase 2's retention work introduce it with its own tests and refusal) costs
  nothing now and cannot be taken later without a breaking change.
- **Close criteria:**
  - [ ] Either `Since` is removed from phase 1 and non-goal 13 says retention will introduce its own
        spelling, or it gets a use case, a declaration-time refusal (what makes a `Since` argument
        illegal, and what it is checked against), a row in D.11 and a row in §2.3.
  - [ ] If it stays, D.2's "there is no revision number to type" claim is corrected, and the document
        says what protects an application from an off-by-one — including whether a store can even
        detect one (an envelope below the declared floor is `ErrRevision`, one above is a misdecode).
  - [ ] INV-010's "declaring a revision twice, or leaving a gap, does not compile" is restated for a
        chain that starts above 1, where a gap is now expressible as a number.
- **Status:** fixed by **removal**, which is the first branch of the criterion and the stronger one. §D.2 states that a chain always starts at revision 1, that the spelling is removed, and why: it is the one constructor taking the number as an argument, and an off-by-one decodes newer bytes into the older struct *successfully* — because revision N+1's payload is usually N's plus a field — dropping the field and folding a wrong state, with no refusal anywhere. Non-goal 13 is rewritten: retention is additive, so nothing is precluded, and it will bring its own constructor, its own refusal for an envelope below the floor, and its own tests, written by the phase that can exercise them. §INV-010 gains the clause that "does not compile" is available *because* a chain starts at 1, and that a later retention spelling introduces the first such number and owes this invariant a check. The shape is in §D.12's rejected list.

### GAP-31 [high][immediate] Two aggregates may declare the same stream family, and the document neither forbids it nor states it as an obligation

- **Where:** UC-002 lines 307-320, especially "a wire name reused across two aggregates must not
  collide, because the tables are per-aggregate"; §2.1 line 130 ("**stream family** ... Declared
  once"); INV-033 lines 1951-1981 (injectivity is scoped to "the aggregate's identity domain");
  INV-013 (no package-level mutable state, so no registry can see two declarations); UC-013's Note
  lines 654-658.
- **What:** UC-002 explicitly reasons about what two independent aggregate declarations may share,
  and concludes that a reused *wire type name* is harmless because the tables are per-aggregate. It
  never asks the dangerous half of the same question: two `Define` calls with the same *family*
  string. INV-033 makes injectivity an obligation over one aggregate's identity domain and says
  nothing across aggregates. Nothing in the document forbids two families being equal, nothing
  detects it — and nothing *can*, because INV-013 forbids the package-level registry that would be
  the only place two independent declarations meet.
- **Why this severity:** The ordinary way to reach it is copying an aggregate declaration to start a
  second one and changing the state type but not the family string. Because the type names were
  copied too, every wire name in the merged stream is declared on both sides, so UC-013's
  `ErrUnknownType` — the refusal that catches the cross-family case and is cited as catching it —
  never fires. Both aggregates load each other's facts, both fold them, both append at versions
  derived from the other's history. This is byte for byte the failure INV-033 is written for, reached
  by the one route INV-033's scope excludes, and it is silent and permanent. The document treats the
  same-family key collision as its most serious hazard and gives it an invariant, a helper, a
  validation point and a conformance section; the family collision gets nothing.
- **Why this timing:** It is an application obligation that belongs beside INV-033, and if the answer
  is a runnable proxy it is part of `eventtest`'s exported surface, which is being frozen. It also
  decides whether UC-002's stated reasoning is complete or misleading.
- **Close criteria:**
  - [ ] The document states family uniqueness as an obligation with the same seriousness as
        injectivity: one family names one aggregate, forever, across processes and deployments.
  - [ ] It says why the framework cannot check it (the no-registry rule) and what it offers instead —
        a proxy in `eventtest`, a documented naming rule, or an explicit refusal to help.
  - [ ] UC-002's "Must not happen" is extended so the two-aggregate collision analysis covers the
        family, not only the wire type name.
  - [ ] INV-033's consequence paragraph names this second route to the same corruption, so the two
        obligations are read together.
- **Status:** fixed; **one premise rejected on the record**. The defect is real and is closed: §INV-039 states family uniqueness with the same seriousness as injectivity — one family names one aggregate, forever, across processes and deployments — and names this as the second route to §INV-033's corruption, with the two invariants told to be read together. §UC-002's "Must not happen" now covers the family and says why the reused-wire-name analysis was only half the question, and §UC-013's note lists both silent collisions. **Rejected:** the claim that nothing *can* detect it because INV-013 forbids the only place two declarations meet. The composition root is a second meeting place: `Bind` refuses a second, different declaration with an equal family (§UC-059, `ErrFamily`), holding the set of bound families on the **store value**, which is per-value state with the caller's lifetime and not the package-level mutable state INV-013 forbids — §INV-013 now says so explicitly, because reading it more broadly costs the framework the two checks it can actually perform. That catches the copied-declaration route this is reached by. The residual the finding is right about — two processes, two deployments, or two stores in one process — keeps the obligation, gets the `eventtest.Families` proxy (§D.1, §D.8), and is on the agenda as Q19.

### GAP-32 [high][immediate] The mutability and concurrency contract of `View` and `Repository` is unstated, and INV-037's state machine is meaningless without it

- **Where:** §D.5 lines 2439-2455 and the state table lines 2477-2484; INV-037 lines 2044-2066;
  UC-021 lines 801-811; §D.6 line 2578-2581 ("The unbound repository is shared by every request; a
  bound one is per transaction"); INV-013's falsification line 1661 ("a `-race` test with many
  goroutines loading and appending across several aggregates at once"); §1 line 57 (`eventmemory` is
  "concurrency-safe").
- **What:** A `View` is mutable by design: it advances on success, folds its own changes, and
  transitions to `stale` or `uncertain`. The document never says whether `Load` returns a pointer or
  a value, what copying a view means, or whether a view may be used from two goroutines. If a view is
  a value type, a copy taken before an append carries the pre-append version and the `fresh` state,
  so INV-037's five states are per copy and the poisoning of UC-023/UC-034 is escapable by assigning
  the view to a second variable — which is one line and looks harmless. If it is a pointer, that is
  a contract that has to be stated ([[D-065]] is this repository's own rule for exactly this
  question). The same silence covers `Repository`: D.6 asserts in an aside that the unbound one is
  "shared by every request", which is a thread-safety promise that no invariant states and no
  conformance section tests, while `Within(ctx)` derives a per-transaction one.
- **Why this severity:** The phase's central safety claim is "the version is not a parameter, so it
  cannot be wrong", and round 1 established (GAP-6) that the claim holds only if every path either
  advances the view or refuses. A copied view is such a path and it is not in the table. An
  implementer who makes `View` a struct of value fields produces a type where
  `stale := view; stale.Append(...)` retries a conflicted decision — the shape [ES] forbids the
  framework from performing and this design promises the caller cannot perform either. A shared
  `Repository` that an implementer gives per-request mutable state (a cached transaction binding, a
  memoised codec check) races every concurrent request, which is the failure [HOUSE] records as
  found by reading rather than by running.
- **Why this timing:** It is the data shape of the two caller-facing types, frozen with them, and
  `data-integrity.md`'s one-line test ("is it still correct with 10 instances running") lands on it
  directly.
- **Close criteria:**
  - [ ] The document states, for `View`: pointer or value, what copying one means, and whether it is
        safe for concurrent use — and if copying is expressible, how INV-037's states survive it.
  - [ ] The document states, for `Repository` (bound and unbound), that it is safe for concurrent use
        by many goroutines, and names the conformance or `-race` case that proves it.
  - [ ] INV-037 gains the copy transition or states that a view cannot be copied, with the mechanism.
  - [ ] `Change`, `Commit` and `Authority` get one sentence each saying they are values safe to copy,
        since UC-030 already depends on copying a receipt changing no answer.
- **Status:** fixed — §INV-038 answers per type: a `View` is a **handle** (`Load` returns a pointer, copying it names one view, one goroutine owns it), a `Repository` bound or unbound **is** safe for concurrent use by many, and `Change`, `Commit`, `Authority`, `Cursor` and `Backing` are values safe to copy whose answers copying does not change. The copy transition is answered by making it inexpressible rather than by adding a row: §INV-037's five states are states of *the* view because there is only one. §D.5 states the pointer return as a contract with the one-line escape it prevents (`stale := view` after a conflict), §2.1's `view` row says it, §D.10 gains the bullet, and §D.12 rejects the value-type shape. The falsifications are a `-race` test across one repository, a conflicted view refusing through a second name, and a check that the load result is a pointer type since the argument rests on it.

### GAP-33 [medium][immediate] Close with an open transaction: three statements in the document, two incompatible behaviours

- **Where:** UC-046 line 1412 ("an open transaction at close is a refusal, not a quiet commit");
  INV-032 lines 1941-1949 ("Close is **idempotent**: a second call returns nil and does nothing. No
  staged transactional work is committed by the close itself" and the falsification "closing a store
  with an open transaction and asserting the transaction's work is absent"); §D.14 line 3070
  ("`Close` is idempotent, **refuses nothing**, and commits no staged work"); §D.9's `lifecycle` row
  line 2821.
- **What:** UC-046 says close *refuses* when a transaction is open. D.14 says close refuses nothing.
  INV-032 says close discards the staged work and says nothing about the return value. And if the
  first close does refuse, "the second and every later call returns nil" makes the two calls of the
  ordinary `defer store.Close()` plus explicit-shutdown-close pattern answer differently.
- **Why this severity:** This is round 1's GAP-20 reappearing inside the fix: one contract, two
  behaviours, and the `lifecycle` conformance section cannot assert either. The caller-visible
  difference is small but it is precisely the class the suite exists to eliminate.
- **Why this timing:** One line of the frozen contract, read by a mandatory conformance section.
- **Close criteria:**
  - [ ] One behaviour is chosen for "close while a transaction is open" and the same words appear in
        UC-046, INV-032 and D.14.
  - [ ] If close can refuse at all, the idempotence rule states what the second call returns after a
        first call that refused.
  - [ ] The `lifecycle` conformance row asserts the chosen behaviour.
- **Status:** fixed — one behaviour, one wording, three places. Close is idempotent and **refuses nothing**, including when a transaction is open: that transaction's staged work is discarded, never committed, and the assertable rule is that the work is absent. The argument is in §UC-046 — the transaction is the caller's, its own commit or rollback is the authority, and a refusal would be reported through the return value of `defer store.Close()` that nobody reads — and the same words are in §INV-032 and §D.14. §D.9's `lifecycle` row asserts nil **and** the absent work, since the second alone passes against an implementation that refuses. §UC-046 also records that an earlier draft said the opposite of §D.14, which is the defect rather than a wording slip, and §D.12 rejects the refusing variant.

### GAP-34 [medium][immediate] "A store must not accept a cursor it did not mint" forbids the cross-process resumption UC-053 exists for

- **Where:** UC-053 lines 1259-1272, especially the Trigger ("Reading again from the persisted
  cursor, **in a new process**") against the Must-not-happen ("A store must not accept a cursor it
  did not mint"); §D.14 line 3063 ("refuses a cursor it did not mint"); §2.3's `ErrCursor` row;
  §D.9's `resumption` row line 2815; INV-016 (identity is the backing, never the store value).
- **What:** In a new process the store *value* that minted the cursor no longer exists, and under
  UC-054 a second store value over one backing is "one store for every purpose in this document".
  So "minted" cannot mean the store value — but the contract says "the store", in a document that has
  spent a whole finding establishing that "the store" is ambiguous between value and backing.
- **Why this severity:** Read literally, the obligation forbids the only scenario the cursor exists
  for. An implementer who takes it literally puts a per-instance nonce in the cursor and breaks
  restart resumption; the mandatory `resumption` section would catch it ("persisted and reused in a
  fresh reader"), so the failure is loud rather than silent — which is why this is medium and not
  high.
- **Why this timing:** It is a store-contract obligation and a mandatory conformance clause, both
  frozen now, and the fix is to say backing rather than store.
- **Close criteria:**
  - [ ] The obligation is restated in terms of what actually mints: a cursor is valid for any store
        over the backing that minted it, and `ErrCursor` covers a cursor from another backing, an
        unparseable one, and one from an incompatible cursor format.
  - [ ] UC-053's Must-not-happen and D.14's `ReadAll` bullet use the same wording.
  - [ ] The `resumption` conformance row states that the fresh reader may be a different store value
        over the same backing, which is what a restart is.
- **Status:** fixed — "minted" means the **backing**, in the same words everywhere. §UC-053 gains a paragraph saying why the store value cannot be what mints (in a new process it does not exist, and two values over one backing are one store), states that a per-instance nonce breaks the only scenario the cursor exists for, and widens `ErrCursor` to a cursor minted over another backing, an unparseable one, and one in a format the store no longer accepts. §D.14's `ReadAll` bullet uses the same wording, §D.9's `resumption` row says the fresh reader may be a different store value over the same backing, and Q18's closing clause is corrected.

### GAP-35 [medium][immediate] UC-055 forbids on every `Bind` exactly the work UC-056 requires on every `Bind`

- **Where:** UC-055 lines 526-529 ("Binding reads the sealed declaration and allocates the
  repository; it **re-reads no declaration state**") and lines 533-535 ("A per-request store must not
  re-seal, **re-validate** or re-register a declaration; that would put a lock on the request path");
  UC-056 lines 548-553 ("`Bind` asks the codec about **every** reader type in the declaration — every
  retained revision, not only the current one"); INV-013 (no package-level mutable state, so the
  answer cannot be memoised globally).
- **What:** UC-055 is the per-request store over a borrowed tenant lease, where `Bind` runs on the
  request path. UC-056 makes `Bind` walk every reader type of every retained revision and interrogate
  the codec about each. Those two use cases, both added in round 2, describe the same call doing and
  not doing the same work. The obvious escape — memoise the (codec, declaration) answer — is closed
  by INV-013.
- **Why this severity:** An implementer must choose, and one of the two resolutions is dangerous: if
  `Bind` skips the codec walk when it has "already been validated", then in exactly the deployment
  [ES] shows (a store per request) UC-056's start-up refusal never happens and an unencodable reader
  type reaches the first append instead. The other resolution costs a reflection walk per request,
  which UC-055 says it must not.
- **Why this timing:** It decides `Bind`'s cost and failure model, both public, and the plan cannot
  write UC-055 and UC-056 as written.
- **Close criteria:**
  - [ ] UC-055's "re-validate" is narrowed to what it actually forbids (re-sealing, mutating the
        declaration, taking a lock on shared state) so it does not contradict UC-056.
  - [ ] The document states what `Bind` costs, in terms an implementer can hold to: O(retained
        revisions) codec questions per bind, with no shared state and no lock.
  - [ ] If memoisation is wanted, it is placed on the store value (which is per request anyway) and
        INV-013's no-global rule is shown to survive it.
- **Status:** fixed — §UC-055's prohibition is narrowed to what it actually forbids (re-sealing, mutating the declaration, taking a lock on state shared between requests) and its Flow now says the bind asks the codec, so the two use cases describe the same call doing the same work. It states what a bind costs in terms an implementer can be held to: O(retained readers) codec questions over an already-sealed immutable table, plus one allocation, no lock and no shared state. It also forbids the dangerous resolution by name — skipping the walk because "some earlier bind did it" deletes UC-056's refusal in exactly this deployment — and places the memo, if ever wanted, on the store value, which is per request here anyway. §INV-013 gains the "what this does not forbid" clause that makes that legal.

### GAP-36 [medium][immediate] The byte domain a key must live in has a stated rule and no declaration site; the family and the type name have neither

- **Where:** §2.1 line 132 ("inside the byte domain that store declares"); UC-051 lines 663-664
  ("outside the byte domain that store declared"); INV-033 lines 1956-1959 ("a store whose key space
  is narrower — one that stores keys in a text column, for instance — **declares** the domain it
  accepts"); §D.4's store spec lines 2364-2373 (`MaxKey` only); §D.7's `Capabilities` lines
  2677-2683 (no domain field); §INV-022's table (a key-byte cap, no domain); UC-004 line 336 (an
  identifier is checked for emptiness and length only).
- **What:** Three places require a store to declare the byte domain its key space accepts, and the
  kernel is the one that validates the key "before the store is reached" (UC-051, D.14). No struct in
  the document has a field for it. The same silence covers the declared identifiers: a family and a
  wire type name are validated for emptiness and length only, and they are stored in the same columns
  a key is, so a store with a narrow domain has the same problem with them and no rule at all.
- **Why this severity:** Phase 2's PostgreSQL store is the immediate second instance: a `text` column
  cannot hold a NUL byte or invalid UTF-8, while §2.1 says the default key domain is arbitrary bytes.
  With no declaration site the kernel cannot refuse, so the key reaches the driver and comes back as
  a raw backend error — which INV-025 forbids from travelling and §2.3 has no row for (see GAP-29).
  The stated alternative, silent mangling, is the INV-033 corruption. `Capabilities` may grow by
  field per D.13, so the fix is additive, which is why this is medium.
- **Why this timing:** It is a field on a struct declared frozen, and the `stream identity` and
  `bounds` conformance sections are supposed to test what it declares.
- **Close criteria:**
  - [ ] The key domain has a declaration site reachable from an `event.Store`, with a default
        (arbitrary bytes) and a stated meaning for the zero value.
  - [ ] UC-051's refusal names the domain rule and the conformance `stream identity` section drives
        a key outside a narrowed domain against a store that declares one.
  - [ ] The same question is answered for the family and the wire type name, or the document states
        why an identifier the author types needs no domain rule.
- **Status:** fixed — the key domain is `KeyDomain` on the store spec, reachable from any store value through `Limits()` (§D.4, §D.14), with `KeyBytes` as the default and the zero value meaning the default; §INV-022's table carries it as its own row. §UC-051 names the domain rule, says why it must be refused at the seam (otherwise the key reaches the driver and comes back as a raw message §INV-025 forbids from travelling), and gains a control that drives a key legal under the default domain and illegal under a narrowed one. §D.9's `bounds` section covers it. The identifier question is answered rather than deferred: a family and a wire type name are validated against a **kernel-wide** domain — valid UTF-8, no control character, no NUL — because they are the same in every deployment and must be legal in *every* store's identifier space, so the kernel takes the narrow intersection and a store may not narrow it further (§UC-004, §INV-022, with the reasoning beside the cap it already owns).

### GAP-37 [medium][immediate] `Backing`'s comparison semantics are unstated, and INV-015's argument rests on them

- **Where:** §D.14 line 3068 ("`Backing` is pure and stable, and equal exactly when two stores write
  to the same place"); INV-016 lines 1685-1705; INV-015 lines 1681-1683 ("a zero view is not usable —
  its backing is empty, and an empty backing equals nothing, so the append refuses"); UC-048 line
  1448 ("a wrapper must forward it rather than answer one of its own").
- **What:** The document requires four things of `Backing` without saying what it is: equality that
  is not pointer equality of a value a decorator may have replaced (INV-016), forwardability through
  a wrapper (UC-048), a zero value that "equals nothing" including another zero value (INV-015), and
  purity and stability (D.14). A plain comparable struct compared with `==` satisfies the first three
  only by accident and fails the fourth requirement outright, since two zero values compare equal.
- **Why this severity:** INV-015 — the phase's second-strongest safety claim, that a view exists only
  where the framework produced it — is argued from "an empty backing equals nothing". If `Backing` is
  a comparable value type and some store returns its zero value (an easy mistake, and nothing refuses
  it), a manufactured zero view compares equal to that store and the append proceeds. The actual
  defence is elsewhere and stronger — a zero view's stream key is empty, which UC-051 refuses — so
  the consequence is a wrong argument rather than a hole, which is why this is medium rather than
  high. But the argument is what the plan will implement.
- **Why this timing:** It is a type in the frozen contract and the comparison is performed by the
  kernel on every append.
- **Close criteria:**
  - [ ] The document states how two `Backing` values are compared (`==` on a comparable type, an
        `Equal` method, or an opaque handle) and what a store returning a zero `Backing` means —
        refused at `Bind`, or an identity that matches nothing.
  - [ ] INV-015's argument is restated over the defence that actually holds (the empty-key refusal of
        UC-051), with the backing comparison as the second line rather than the first.
  - [ ] UC-048's forwarding obligation is expressed in terms of the chosen shape.
- **Status:** fixed — `Backing` is an opaque value compared with **`Equal`, never with `==`**, whose zero value is invalid and matches nothing including another invalid one; §D.14 writes it out with `Equal` and `Valid`, §INV-016 gains the "how two backings are compared" paragraph with the reason `==` cannot express the last part, and a store that answers the invalid backing is **refused at `Bind`** (`ErrWrongStore`, and the §2.3 row says so). §INV-015's argument is restated over the three defences in the order they actually hold — the empty-key refusal first, since a composite literal of an all-unexported-field struct does compile; the backing comparison second; the build-failure fixture for the forms that do not compile — and it records that the earlier draft rested on the weakest of the three. §INV-016's falsification gains the third control (two invalid backings must compare unequal, which an `==` implementation fails), and §UC-048 expresses forwarding as returning the wrapped store's own backing value, with the same obligation for its limits and codec.

### GAP-38 [medium][immediate] The empty-append receipt makes the D.6 northstar sketch fail on a correct no-op program

- **Where:** §D.6 line 2556 (`if !commit.Authority().BoundTo(txCtx) || !app.auditor.WroteWithin(txCtx)
  { return errNotOneTransaction }`); UC-020 lines 790-792 ("a receipt that says plainly it is empty:
  zero events, no position range, **no authority**"); INV-031; UC-030.
- **What:** UC-020 makes an empty append an ordinary success — an idempotent re-application, a cancel
  of something already cancelled — and gives it a receipt with no authority. D.6's sketch, which is
  the document's normative shape for the transaction path, calls `Authority().BoundTo(txCtx)` on that
  receipt unconditionally and treats a `false` as a misconfiguration.
- **Why this severity:** A caller who copies the sketch gets `errNotOneTransaction` and a rolled-back
  transaction every time the domain legitimately decides nothing changed — on a correct program,
  from the document's own recommended shape. It also leaves unstated whether calling `Authority()`
  on an empty commit is even legal or panics.
- **Why this timing:** It is the northstar sketch for mode E4 and the shape every consumer copies,
  and it is one branch.
- **Close criteria:**
  - [ ] The sketch handles the empty commit, or `Commit` exposes the "is this empty" fact the sketch
        branches on first.
  - [ ] The document states what `Authority()` returns for an empty commit and what `BoundTo` answers
        for that value.
  - [ ] UC-020's Observed says what an empty receipt answers to the atomicity predicate, since UC-030
        is the only reader of it.
- **Status:** fixed — `Commit.Empty()` is the fact the sketch branches on first, and §D.6 now guards the whole atomicity claim with it plus a comment saying why (an append that wrote nothing is atomic with nothing), followed by the paragraph "Why the sketch asks `Empty()` first". §UC-020's Observed states that an empty receipt carries the **invalid** authority, that asking for it is legal and never panics, that it answers **no** to the predicate and compares equal to nothing — and adds the rule that a caller must not have to know a receipt is empty before it may ask it a question, or the northstar sketch fails on a correct program. §D.14 writes `Commit` out with `Empty`, `Authority` and `Durability`.

### GAP-39 [medium][immediate] The mapper's output is a permanent wire format and no invariant says so

- **Where:** §D.1 lines 2160-2190 (the mapper, `event.Compose`, and the wrong/right pair); INV-005
  lines 1556-1566 (wire identity is declared: family, type name, revision — the key is not among
  them); INV-033 (byte-exact comparison, injectivity); UC-050's Control line 388 (the plain
  conversion and the composing helper are both legal mappers for the same shape of identity).
- **What:** INV-005 exists because a Go rename must not orphan history, and it covers the family, the
  type name and the revision. The stream key is the fourth thing written into every row and it is
  produced by application code that an ordinary refactor changes: adding a field to an ID struct,
  switching `event.Key(id)` to `event.Compose(id)` when a second field arrives, changing a
  `strconv` format. Every one of those renders the same identity to a different key. Nothing in the
  document says the mapper's output is frozen from the first append onward.
- **Why this severity:** The failure is the split the document calls the cheap loud one — but it is
  only loud if somebody looks: every aggregate reads as version 0 with a zero state, which is
  indistinguishable from a fresh install (UC-009 deliberately removes the not-found refusal), and
  then gets written to, producing two divergent histories for one identity. It is the one place an
  ordinary refactor rewrites the past, which is exactly what INV-005 exists to prevent everywhere
  else.
- **Why this timing:** It is one clause in an existing invariant and a line in the mapper's
  documentation, and it changes what UC-050's control asserts (two legal spellings for one identity
  shape produce two different keys, which is the trap).
- **Close criteria:**
  - [ ] An invariant (or an extension of INV-005) states that the mapper's output is a wire format:
        once a stream has been written, the rendering of that identity may never change.
  - [ ] §D.1 says it beside the mapper, where the author is choosing the rendering, and notes that
        `event.Key(id)` and `event.Compose(id)` are not interchangeable after the first append.
  - [ ] UC-050's control states that the two spellings produce different keys, so nobody reads it as
        "either is fine".
- **Status:** fixed — §INV-005 is retitled and extended: the key is the fourth wire value, and because it is *computed* rather than typed, the rule for it is a freeze — once a stream has been written, that identity's rendering may never change. It names the ordinary refactors that break it (a field added to an ID struct, `Key` swapped for `Compose`, a `strconv` format changed), states that the failure is quiet because every aggregate then reads as version 0 with a zero state and UC-009 deliberately removes the not-found refusal, and adds a fixture that writes with one spelling and reloads with the other. §D.1 carries the same paragraph beside the mapper, where the author chooses the rendering, with the advice to take the composing helper on day one. §UC-050's control now asserts that the two spellings of one identity produce two different keys.

### GAP-40 [medium][immediate] The copier obligation gets no runnable proxy while the identical injectivity obligation gets one

- **Where:** §D.1 line 2184 (`eventtest.Keys(t, accounts, id1, id2, id3, ...)`); INV-033 lines
  1963-1969 ("the framework owes three things instead ... a test helper that takes a sample of
  identities and asserts distinct keys, so the obligation has a proxy an application can run");
  UC-052 and INV-021 lines 1764-1769 (the falsification is a per-aggregate test the application must
  write by hand, with no helper); §D.8 (the `eventtest` surface: `RoundTrip`, and `Run`).
- **What:** The document identifies two application obligations the framework cannot verify — an
  injective mapper and a correct copier — and reasons at length that an unverifiable obligation needs
  a runnable proxy. It then ships a proxy for one of them. A declared copier that returns its
  argument (`func(o Order) Order { return o }`) is accepted, is exactly what a hurried author writes,
  and reinstates GAP-2's aliasing with no error anywhere.
- **Why this severity:** `restrictions.md` §5 requires every stated invariant to have a test;
  INV-021's application-side half has none that an application can run, and the conformance suite
  cannot supply it because the suite tests stores, not aggregates. The asymmetry also reads as an
  argument that one obligation matters and the other does not.
- **Why this timing:** `eventtest`'s exported surface is one of the phase's three deliverables and is
  being frozen; adding a helper later is additive but the plan will not write a test nobody asked
  for.
- **Close criteria:**
  - [ ] `eventtest` gains a copier proxy — given a state value with populated reference fields, it
        mutates what the copier returned and asserts the original is unchanged — or the document
        states why the copier needs no proxy while the mapper does.
  - [ ] INV-021's falsification names that helper rather than describing a hand-written test.
  - [ ] UC-052's Control includes a wrong copier (identity) failing the proxy, so the helper is not
        vacuous.
- **Status:** fixed — `eventtest.Copies(t, agg, sample)` takes a state value with populated reference fields, mutates what the copier returned and asserts the original is unchanged. §INV-021 gains the paragraph that says why an unverifiable obligation needs a proxy and that shipping one for the mapper and not the copier reads as an argument that one matters; its falsification names the helper. §UC-052's control includes the identity copier failing it. §D.1 shows the call beside `event.Copy`, and §D.8 groups all three proxies — `Keys`, `Copies`, `Families` — with the rule that an obligation with no runnable proxy is one nobody discovers breaking.

### GAP-41 [low][deferred] "A payload type that is not a struct" is a kernel policy about a codec's domain, with no argument

- **Where:** UC-004 line 337 ("a payload type that is not a struct"); §D.4 lines 2390-2399 and UC-056
  lines 556-562 (the argument that the kernel must not hold a codec's encodability rules).
- **What:** The document argues carefully that encodability is the codec's question, answered at
  `Bind` because a hardcoded rule in the kernel would be one codec's policy and would make the codec
  seam decorative. It then keeps one such rule at declaration — the payload must be a struct — and
  never argues it. A `type Amount int64` payload is encodable by every codec the document imagines.
- **Why this severity:** It is a constraint on every application's payload types with no stated
  reason, and it is inconsistent with the reasoning two sections later. The likely real reason —
  a non-struct payload cannot gain a field, so it has no evolution path — is a good argument and
  belongs in the text.
- **Why this timing:** It is a declaration-time refusal that costs nothing to leave as it is until
  the declaration checks are written; nothing else depends on it.
- **Close criteria:**
  - [ ] UC-004 states why a payload must be a struct, or drops the rule and lets the codec answer.
- **Status:** open (deferred) — carried into the spec's §8 Deferred beside GAP-25 and GAP-26. The finding's own guess at the real reason is right and is now written down in §UC-004: a non-struct payload cannot gain a field, so it has no evolution path and the reader chain buys it nothing. What stays deferred is whether that argument is reason enough to refuse rather than to let the codec answer as everything else codec-shaped does. Nothing depends on it and both answers are one line, so it is decided when the declaration checks are written.

---

**Checked this round and found sound** (so the absence of a finding is deliberate): all thirteen
[ES] non-negotiables now have a testable restatement; the refusal table's nineteen rows are
internally consistent and every refusing use case points at one; the cursor design closes GAP-3
completely and the losing pattern (`cursor = e.Position`) is genuinely unspellable because `AllQuery`
has nowhere to put a position; the `BoundTo` predicate is a correct third answer to GAP-4 and the
rejection of that finding's premise is right; `Backing` as identity is the right resolution of GAP-5
and UC-054/UC-055 are the controls that prove it; UC-057's precedence rule (uncertainty outranks
cancellation) is argued from the asymmetry of caller obligations, which is the correct argument;
INV-004's three-way split between what the signature holds, what the application owes and what
determinism can catch is honest; every row of D.11 is achievable in Go as written, including
`event.Copy` inferring `Option[S]` against the explicitly-supplied `S`, and the one place inference
stops is admitted; the five defaults in D.4 are flagged as needing a derivation (Q10) rather than
presented as derived, which is what `universality.md` asks; `event.Compose`'s length-prefixing is a
real mechanism and not a keyword list; no use case, invariant or DX line hardcodes the `Account`
example — the two universality residuals found are shape assumptions (GAP-28's flat state,
GAP-30's typed revision number), not domain literals; the empty-append carve-out in INV-002 is now
scoped rather than contradicted; UC-009's collapse of never-existed and version-0 is correct; and
the conformance suite's vacuity rules (every optional section skipped fails; every section carries a
control) are the right application of [[D-020]].

---

## Round 3 - econv-usecase-validator (coverage + invariant + DX roles, re-audit) - 2026-09-06

Sources read: `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (whole file, 3949 lines),
`.agents/artifacts/gaps/EVENTSOURCE_P1_USECASES_GAPS.md` (rounds 1 and 2, whole file),
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` (whole file), `CLAUDE.md`,
`docs/ai/decisions/Index.md`, `~/.claude/skills/econv/SKILL.md` and `references/gaps.md`,
`universality.md`, `microkernel.md`, `architecture.md`, `data-integrity.md`, `restrictions.md`.
No `.go` file was opened.

**Round 2's fourteen `[immediate]` findings were re-checked one by one against the round-3 text,
not against the resolution notes. All fourteen are genuinely closed, not reworded.** GAP-27 by
`Codec()` + `Limits()` on the contract, `Limits`/`Codec` written out in §D.14, INV-018's amendment
and INV-022's "read from" column — and UC-054 now states what two values over one backing must
agree on. GAP-28 by UC-052's type-graph walk (transitive, starting at the state type itself, path
named in the refusal) with a termination argument that is correct for Go — every cycle passes
through a reference kind and each ends the walk. GAP-29 by UC-060, the `ErrBackend` row, INV-037's
`unchanged` rule and the `store failure classification` section. GAP-30 by removing `event.Since`
outright, which is the stronger branch. GAP-31 by UC-059 + INV-039 + `ErrFamily` + Q19; the
rejection of its "nothing can detect it" premise is right — `Bind` is a second meeting place and a
set on a store value is not package-level state. GAP-32 by INV-038's per-type answer and `*View`.
GAP-33 by one wording in three places. GAP-34 by "minted over the backing". GAP-35 by narrowing
UC-055 and stating the bind cost. GAP-36 by `KeyDomain` on `Limits` plus the kernel identifier
domain. GAP-37 by `Backing.Equal`/`Valid`, the invalid zero value and INV-015's three ordered
defences. GAP-38 by `Commit.Empty()` and the guarded §D.6 sketch. GAP-39 by INV-005's key freeze.
GAP-40 by `eventtest.Copies`. GAP-41 stays `open (deferred)` in §8 and its argument is now written.

Checks run this round: every actor and entry mode against a `[happy]` use case; all thirteen [ES]
non-negotiables against a testable INV; [ES]'s ten capability/wrapper obligations against a use
case or invariant; the edge matrix (empty / malformed / oversized / ambiguous / duplicated /
multilingual / partially failing dependency / timeout / zero results / everything-matches /
conflicting signals / crash / cancellation / zero-length batch / duplicate-in-batch /
absent-stream-versus-version-0); the three `universality.md` checks against every mechanism, with
the second-instance test run on a state type holding `time.Time`, a payload type holding a slice, a
composite non-string identity and a state type that is itself a map; a naive-contract sweep asking
for each documented shape whether a caller can do the wrong thing silently; Go-level feasibility of
every type in §D.14 as written, not only of §D.11's inference table; a UC-versus-UC,
UC-versus-INV and INV-versus-§D.14 contradiction sweep; and the microkernel four-part test.

**Five of the eleven findings below were introduced by round 2's and round 3's own fixes**
(GAP-42 by the transitive walk, GAP-44 and GAP-49 by §D.14 and the factory, GAP-48 by the refusal
table's new rows, GAP-51 by `Limits`). Four are pre-existing holes that earlier rounds did not
reach because they are about the *append's receiver* rather than about its arguments.

### GAP-42 [high][immediate] The transitive copier walk refuses ordinary state types the application does not own, and the escape hatch permanently disarms the check

- **Where:** §UC-052 lines 449-498, especially "the framework walks the state type's **type
  graph** ... through struct fields, embedded fields, unexported fields and array element types";
  §D.1 lines 2650-2660; §INV-021 lines 2036-2058; §D.0 line 2534.
- **What:** The walk has no stopping rule for a reference kind reached inside a type the
  application did not write. `time.Time` holds `loc *time.Location`; `big.Int` holds a slice;
  `netip.Addr` holds a pointer; `decimal.Decimal` holds a `*big.Int`. Every one of them is reached
  through the unexported fields the walk explicitly follows. So
  `type Order struct{ PlacedAt time.Time; Total int64 }` — the most ordinary aggregate state there
  is — is **refused at declaration** unless the author declares a copier, and the only copier that
  can be written for it is `func(o Order) Order { return o }`, which is the exact value UC-052's
  own control calls the wrong copier. `eventtest.Copies` cannot falsify it either: the helper
  "mutates what the copier returned", and nothing outside `time` can mutate a `time.Time`, so the
  proxy passes vacuously. The second half is worse: the requirement is satisfied **once per
  aggregate**, not once per reference kind. A copier declared to get past a `time.Time` is still
  present the day the author adds `Lines []Line`, so the declaration is accepted, the copier does
  not clone the new slice, and no refusal fires anywhere.
- **Why this severity:** `universality.md`'s second-instance test fails on the first state type
  that carries a timestamp, which is not an exotic instance. And the failure the mechanism exists
  to prevent returns by the front door: an aggregate that legitimately declared an identity copier
  for a foreign value type, then grew a slice field, aliases the view's accumulator exactly as
  GAP-2 and GAP-28 described — the decision function writes into the slice, the view's next fold
  runs on a state no event sequence produces, and a reload disagrees with the live view, silently.
  The mechanism is therefore correct only for aggregates whose state is built from scalars, slices
  and maps the author owns, which is the sample this document keeps showing.
- **Why this timing:** It is the declaration seam's refusal rule and the semantics of `State()` —
  the first thing every aggregate author meets, and a `universality.md` finding, which that law
  forbids deferring.
- **Close criteria:**
  - [ ] The spec states what the walk does with a reference kind reached inside a type the
        application does not own, with a mechanism rather than "declare an identity copier" — an
        opt-in "this type is a value" declaration, a walk that stops at a type with no exported
        mutable path, or an explicit statement that such states are refused and why that is
        acceptable.
  - [ ] The spec states whether one declared copier satisfies a type graph that later grows a new
        reference kind, and if it does, what stops the aliasing GAP-28 closed from returning.
  - [ ] UC-052 carries an instance whose reference kind is unreachable from `eventtest.Copies`, and
        says what the proxy proves there — since a proxy that cannot fail is the liability
        [[D-020]] names.
  - [ ] §D.1's example set includes a state with a `time.Time`, so the shortest correct declaration
        for it is visible rather than inferred.
- **Status:** **dissolved** by round 4 — the copier walk, `event.Copy` and `eventtest.Copies` are deleted, because the object they protected (a live accumulator the kernel keeps and hands out repeatedly) no longer exists. See `## Resolution — seam redesign`.

### GAP-43 [high][immediate] The aliasing rule covers only what the view hands out; the payload the caller still holds is folded into the accumulator by reference

- **Where:** §INV-021's statement lines 2036-2045 ("nothing the view hands out shares mutable
  memory with the accumulator"); §UC-019 lines 922-925 ("The view must not be advanced by
  re-reading the store or by decoding its own written bytes — it folds the changes it already
  holds"); §D.3 lines 2802-2809 and 2818-2822 (`New` "stores the typed payload and a closure that
  will encode it"); §UC-052; §UC-004 (the declaration's checks name no payload copier).
- **What:** The document spends UC-052, INV-021, §D.1 and four rejected alternatives closing the
  outbound direction — the accumulator must not be reachable through `State()`. The inbound
  direction is never mentioned. A `Change` holds the payload **value the caller constructed**, and
  UC-019 requires the local advance to fold that value rather than decode the bytes it wrote. So
  for any payload type with a reference kind — `type LinesAdded struct{ Lines []Line }`,
  `type Tagged struct{ Tags []string }`, a `map[string]string` of attributes, all ordinary — a fold
  written as `s.Lines = e.Lines` leaves the view's accumulator aliasing a slice the caller still
  holds and can mutate. The declared state copier does not help: it clones on the way *out*, and
  the corruption arrives on the way *in*.
- **Why this severity:** It is GAP-2's failure with the arrow reversed, and it is asymmetric
  between the two paths the phase promises are identical: a freshly loaded view folds values the
  codec just produced and cannot alias anything, while a locally advanced view folds caller memory.
  So a live view and a reload of the same stream disagree — the exact divergence §INV-021 exists to
  forbid — and the store's own history is fine, which is what makes it invisible. A second,
  smaller consequence sits beside it: encoding happens at append, so mutating the payload between
  `New` and `Append` changes the fact that is recorded, and no line says whether that is legal.
- **Why this timing:** It decides whether a payload type with a reference kind needs the same
  declaration-time treatment the state type got, which is the declaration seam's public refusal
  rule, and whether `New` copies. Both are frozen with the declaration API.
- **Close criteria:**
  - [ ] The spec states whether the payload a `Change` holds may be mutated after `New` and what
        the framework guarantees if it is.
  - [ ] §INV-021's statement covers the inbound direction: what the accumulator may share with a
        value the caller still holds, and what closes it — a payload copier requirement symmetric
        with UC-052, a copy at `New`, or a stated application obligation with a runnable proxy.
  - [ ] A use case exercises an aggregate whose payload carries a slice: decide, append, mutate the
        payload the caller still holds, and assert the view's next fold and a reload still agree.
  - [ ] §UC-019's "it folds the changes it already holds" says what value is folded, since that
        sentence is what forbids the round-trip that would otherwise break the alias.
- **Status:** **dissolved** by round 4 — `Fact.New` encodes at the moment of decision and the value a `Change` folds is the one decoded back from those bytes, so a payload the caller still holds is unreachable from anything the framework has. See `## Resolution — seam redesign`.

### GAP-44 [high][immediate] Four types the store contract requires a store to return are declared unconstructible outside `event`, so no second store is writable as specified

- **Where:** §D.14 lines 3542-3555 (`Log.Backing() Backing`, `Log.ReadAll(...) (AllPage, error)`,
  `Store.Append(...) (Commit, error)`), lines 3612-3616 (`AllPage.Resume Cursor`), lines 3618-3642
  (`Backing struct{ /* opaque */ }`, `Commit struct{ /* opaque */ }`, `Authority struct{ /* opaque
  */ }` with "No exported field, no constructor, no marshaller"); §2.1's `cursor` row line 158
  ("store-minted, opaque ... no caller derives one"); §INV-028 lines 2217-2218; §INV-015 lines
  1938-1947; §UC-053 lines 1471-1478.
- **What:** `eventmemory` and `eventpg` are packages other than `event`. In Go, a struct whose
  fields are unexported cannot be constructed outside its own package except through an exported
  constructor — and an exported constructor is equally available to a caller. The contract requires
  a store to produce four such values on every operation: a `Backing` from what it writes to, a
  `Cursor` in every `AllPage`, a `Commit` from every `Append`, and the `Authority` that `Commit`
  carries. The document states for three of them that a caller must not be able to construct one
  (INV-015 leans on the invalid backing; INV-028 forbids constructing an authority; UC-053 forbids
  a caller deriving a cursor), and never says how a store therefore mints one.
- **Why this severity:** INV-019 — "adding a store implementation requires new files under its own
  package and **no change** to `event/`" — is the phase's self-declared defining architectural
  test, and it cannot hold: the first store outside `event` needs `event` to grow minting API it
  does not have. Both resolutions available at implementation time are wrong in a way the document
  would not catch. Exporting `event.NewCursor` / `event.NewBacking` / a `Commit` builder makes
  every "no caller can construct one" argument false in one line — a caller can mint a cursor for a
  position it chose, which is the losing pattern §D.7 says is *unspellable*. Moving the mint into
  the kernel means `Append` and `ReadAll` must return store-shaped data the kernel wraps, which is
  a change to the two central method signatures of a contract §D.13 declares frozen at the end of
  phase 1.
- **Why this timing:** It is the shape of the one extension point, phase 2 is defined as "the
  identical suite against a different factory", and the alternative resolutions differ in the
  return types of `Append` and `ReadAll`. Discovering it while writing `eventmemory` means
  rewriting the contract that `eventtest` and `eventpg` are both specified against.
- **Close criteria:**
  - [ ] For each of `Backing`, `Cursor`, `Commit` and `Authority`, the document says who mints it
        and how a package other than `event` does so.
  - [ ] If a minting call is exported, the invariant that leaned on unconstructibility (INV-015 for
        `Backing`, INV-028 for `Authority`, UC-053 for `Cursor`) is restated over what actually
        holds, as INV-015's three-defence paragraph already does for the view.
  - [ ] If the kernel mints them, `Append` and `ReadAll` return the store-shaped values the kernel
        wraps, and §D.14's division-of-labour table gains the row.
  - [ ] INV-019's falsification — "a second trivial store inside the conformance suite's own test
        package" — is shown to be writable against whatever the answer is, since that fixture is in
        a third package again.
- **Status:** **closed** — two of the four values never cross the store boundary at all (`Commit` is kernel-assembled because `Store.Append` returns only an error; `At[S]` is unpacked into `AppendRequest` and never reaches a store), `Cursor` is a defined string type a store mints by conversion, and `Backing`/`Authority` get exported minting constructors whose forgery buys nothing, with §INV-015 and §INV-028 restated over what actually holds. See `## Resolution — seam redesign`.

### GAP-45 [high][immediate] The append's receiver is a `View`, so the backing comparison INV-016 describes has no second party and no reachable trigger

- **Where:** §D.5 line 2943 and §D.6 line 3052 and §D.8 lines 3235/3243 and §D.11 line 3429 (every
  append in the document is `view.Append(ctx, changes...)`); §INV-016 lines 1955-1958 ("An append
  compares the view's **backing** with the appending repository's"); §D.14's kernel column line
  3654 ("Comparing the view's backing with the repository's"); §UC-031 lines 1250-1275
  ("Appending a view obtained from store A through a repository over store B"); §UC-054 lines
  577-583 ("A view loaded through the first store is appended through the second"); §UC-047 lines
  1658-1660 ("a view held across a close ... becomes usable again against another store over the
  same backing"); §INV-015 lines 1938-1950.
- **What:** `view.Append` has exactly one party. A view must already hold whatever it appends
  through, so the repository an append uses is by construction the one that loaded the view, and
  the two backings compared are the same value. There is no spelling anywhere in the document for
  appending a view through a *different* repository — §D.12 rejects `repo.Append(ctx, id,
  expectedVersion, changes...)` and nothing replaces it. Consequently UC-031's scenario, UC-054's
  Trigger, UC-047's "usable again against another store", the `ErrWrongStore` row, the `Sibling`
  factory hook and the `shared backing` conformance section describe behaviour no program can
  reach. INV-015's second defence has the same problem: a manufactured `&event.View[…]{}` has no
  repository at all, so `view.Append` on it dereferences nothing rather than comparing an invalid
  backing against a real one, and UC-047's "No panic" is what would actually break.
- **Why this severity:** A whole use case, an invariant, a sentinel, a factory hook and a
  conformance section rest on a comparison the API shape makes vacuous, and one of the phase's two
  strongest safety claims (INV-015) is argued from it. Either the DX sketch is wrong and appends
  are repository-mediated — in which case §D.5, §D.6, §D.8 and §D.11 all show the wrong call and
  §D.12 rejects the right one — or the sketch is right and five sections are protecting nothing. An
  implementer will pick one silently, and the conformance suite as written cannot tell.
- **Why this timing:** It is the signature of the single call this phase is designed around, and
  the answer decides what a `View` holds, whether `ErrWrongStore` exists, and whether the `Sibling`
  hook is needed at all.
- **Close criteria:**
  - [ ] The document states what a `View` holds and how `view.Append` reaches a store.
  - [ ] Under that answer, UC-031's scenario is either shown to be expressible — with the call
        written out — or the use case, the sentinel, the conformance section and INV-016's
        comparison are restated over what can actually happen.
  - [ ] INV-015's second defence is restated so it does not depend on a comparison a manufactured
        view never reaches, and the manufactured-view case says what happens instead of a panic
        (UC-047).
  - [ ] UC-054 and UC-047 name the call by which a view meets a second store value over one
        backing, since both use cases are stated in terms of one.
- **Status:** **dissolved** by round 4 — the append is a **repository** call taking a token, `repo.Append(ctx, at, changes...)`, so the token and the repository are two independently obtained values and the backing comparison has two parties by construction. See `## Resolution — seam redesign`.

### GAP-46 [high][immediate] Nothing refuses a load or append on an unbound repository under a context that carries a transaction, so the write silently escapes it

- **Where:** §D.6 lines 3039-3052 (`bound, err := accountRepo.Within(txCtx)` then
  `bound.Load(txCtx, ...)` then `view.Append(txCtx, changes...)` — the context is passed to every
  call whether or not it was bound); §UC-028 lines 1170-1189; §UC-032 lines 1277-1291 (the refusal
  is only for `Within` *asked for* with no transaction present); §D.14's `Within` bullet line 3698;
  §INV-003.
- **What:** The three use cases in group E all start from the caller having called `Within`. The
  case nobody covers is the caller who opens a transaction, puts it in the context, and then uses
  the **unbound** repository — which is one forgotten line, and which §D.6's own sketch makes look
  harmless by threading `txCtx` through `Load` and `Append` as if the context were what carries the
  binding. Under the document's rules the unbound store must ignore an ambient transaction (pure
  ambience is rejected by UC-032 and §D.12), so the append runs on autocommit. The caller's
  transaction then rolls back and the events stay.
- **Why this severity:** It is the failure [[D-118]] is quoted for on line 1288 of this same
  document — "a placement that silently escaped the caller's transaction is the failure this whole
  path exists to prevent, and it would be invisible" — reached by the one route the document does
  not check. Every guarantee in group E holds, and the caller's atomicity claim is false: `Empty()`
  is false, `BoundTo(txCtx)` answers **no**, and §D.6's sketch would catch it only because the
  sketch calls `Within`. UC-029's rollback guarantee is the property a reader believes is in force,
  and it is not.
- **Why this timing:** It is a store-contract obligation (may a store look at a context it was not
  bound through, and must it refuse?) and it changes what the `transactions` conformance section
  asserts. Both are frozen in this phase, and phase 2's SQL store is where the consequence is real.
- **Close criteria:**
  - [ ] A use case covers "the context carries a transaction this store recognises and the caller
        never called `Within`", and states the answer: refuse, or write on autocommit and say why
        that is safe.
  - [ ] If the answer is refuse, it gets a row in §2.3 and a `transactions` conformance clause; if
        it is autocommit, §D.6's sketch stops threading `txCtx` through calls where it has no
        effect, since that is what teaches the mistake.
  - [ ] §UC-028's "Must not happen" covers the unbound repository as well as the bound one.
- **Status:** **closed**, and the answer is the opposite of the one the finding assumed. [[D-118]] already decided it for the sibling subsystem: an ambient transaction on this store's own backing is **joined**, and only an ambient executor that is *not* a transaction is refused — never autocommit. §UC-046 and §INV-041 follow it, and the round-3 `ErrUnbound` that refused it is deleted. See `## Resolution — seam redesign`.

### GAP-47 [high][immediate] `Within` is specified as both a declared-chain walk and an exact-outer effect, and the tested half bypasses a policy decorator

- **Where:** §UC-033 lines 1297-1306 — Flow: "discovered through the declared unwrapping helper";
  Must-not-happen: "a decorator that does not forward it must not cause a store that *does* have it
  to appear not to" immediately followed by "an unknown wrapper must not be walked through"; Why:
  "navigation may walk a declared chain; executable effects are exact-outer or fail closed.
  Binding a transaction is an executable effect"; §INV-030 lines 2253-2265, whose falsification
  tests "a wrapper that declares what it wraps keeping discovery alive"; §UC-048 lines 1681-1683.
- **What:** The two rules are incompatible and both are written. Under "walk the declared chain",
  `TransactionalOf` reaches an inner store's `Transactional` through any decorator that supplies
  `Next()`, and `Within` then returns **the inner store**: every load and every append inside that
  transaction runs below the decorator, so a wrapper whose declared policy is to refuse or police
  appends is silently removed for the whole transactional path. Under "exact-outer", a decorator
  that does not itself implement `Transactional` makes the capability absent — which is precisely
  what UC-033's own Must-not-happen forbids in the sentence before.
- **Why this severity:** [[D-115]], which this use case cites as its authority, decided the
  opposite of what the use case specifies: `ExistsUnscopedOf` answers from the exact outer value
  and never walks, and it is recorded as "the last executable effect that still tunnelled". A
  contract that cites the decision and then specifies the tunnel reintroduces the defect the
  decision closed, in the one path where the caller has already been told atomicity is guaranteed.
  INV-030's falsification tests the wrong half, so nothing would catch it.
- **Why this timing:** It is the semantics of an exported discovery helper and of `Repository.Within`,
  and it is a mandatory conformance case (`capability honesty` and `transactions`). It also decides
  whether §D.13's compatibility promise — "a capability added later is reachable through decorators
  written earlier" — is true or is exactly the tunnel.
- **Close criteria:**
  - [ ] One rule is chosen for `Transactional`, stated once, and UC-033's Flow, its
        Must-not-happen, INV-030's statement and INV-030's falsification all say it.
  - [ ] Whichever is chosen, the document states what happens to a declared decorator's policy for
        operations performed through the store `Within` returned.
  - [ ] The citation of [[D-061]]/[[D-115]] matches the rule chosen, or the document records that
        it departs from them and why.
- **Status:** **dissolved** by round 4 — there is no optional interface and no discovery: `Transaction(ctx)` is a **required** `Store` method answered by the exact outer value, and `Within` returns a **context** rather than an inner store, so the declared-chain-versus-exact-outer question has no subject and no decorator is ever bypassed. See `## Resolution — seam redesign`.

### GAP-48 [medium][immediate] The refusal table contradicts itself in two rows, so the mandatory `refusal vocabulary` section fails on its own subject

- **Where:** §2.3 lines 226-227 — `ErrDeclaration` | "Must be distinguishable from: every other
  row" against `ErrSealed` | "Wraps: `ErrDeclaration`"; §INV-024's statement line 2147 ("the
  stale-view refusal wraps the conflict") against §2.3's `ErrStaleView` row line 239 ("Wraps:
  whatever poisoned the view: the conflict class after UC-022, `ErrUncertain` after UC-034");
  §INV-024's falsification lines 2160-2165; §D.9's `refusal vocabulary` row line 3360.
- **What:** INV-024's falsification is a table test asserting, for every row, that it matches what
  its "wraps" column names and does **not** match what its "must be distinguishable from" column
  names. Row 1 fails that test against row 2 by construction: `ErrSealed` wraps `ErrDeclaration`,
  so `errors.Is` reaches `ErrDeclaration` from a sealing refusal, so `ErrDeclaration` is not
  distinguishable from every other row. Separately, INV-024's own one-line summary of the wrapping
  rules says the stale-view refusal wraps the conflict, full stop, which is false after UC-034 and
  is the half a test author would copy.
- **Why this severity:** This is round 1's GAP-14 reappearing inside the fix: a mandatory
  conformance section whose subject is an enumeration that cannot be satisfied as enumerated. The
  likely implementation outcome is that the section is written to skip row 1, which quietly removes
  the assertion that a declaration refusal is not reachable at request time.
- **Why this timing:** Sentinels are exported API and the section is mandatory; the fix is one
  cell and one clause, and it is cheaper before a test is written against the wrong reading.
- **Close criteria:**
  - [ ] `ErrDeclaration`'s "must be distinguishable from" cell excludes the rows that wrap it, or
        `ErrSealed` stops wrapping it and the reason is stated.
  - [ ] INV-024's statement names both classes `ErrStaleView` can wrap, matching §2.3 and UC-023.
  - [ ] The negative half of the falsification says how it treats a row that another row wraps.
- **Status:** **dissolved** by round 4 — the pairwise "must be distinguishable from" column is deleted and the vocabulary is a **class partition** with one rule (`errors.Is` never crosses a class) plus a closed list of four declared wraps. The sentence that contradicted itself is no longer expressible, and `ErrStaleView` — the second half of the finding — no longer exists. See `## Resolution — seam redesign`.

### GAP-49 [medium][immediate] The `Sibling` hook's requiredness is derivable from nothing, while the mandatory `resumption` section is specified in terms of it

- **Where:** §D.9 lines 3325-3326 ("Required when the store's backing can be shared"); §D.9's
  `resumption` row line 3356 ("A cursor from a previous read, persisted and reused in **a different
  store value over the same backing** — which is what a restart is ... always"); §D.9's `shared
  backing` row line 3365 ("`Sibling` supplied"); §UC-043 lines 1546-1553 and 1564-1569 (a store
  that declares `TransactionBinding` and supplies no `Begin` **fails**; only the failure-injection
  hook may be missing and reported as not certified); §D.7's `Capabilities` lines 3194-3200.
- **What:** `Begin` is required exactly when `Capabilities.TransactionBinding` is true, so the
  suite can tell a missing hook from an unclaimed capability. `Sibling` has no such field: nothing
  in the capability report says whether a store's backing can be shared. So a store that omits
  `Sibling` is indistinguishable from one that cannot share, and the suite cannot apply its own
  rule. Worse, `resumption` is an `always` section and its stated method needs a second store value
  over one backing, so for a store with no `Sibling` the mandatory section cannot be run as
  written.
- **Why this severity:** The suite's two anti-vacuity rules — a claimed capability is never skipped
  for want of a hook, and a run in which every optional section was skipped fails — are what stop a
  green run from being evidence-free, and one hook sits outside both. The `resumption` section is
  the one that protects INV-035, the phase's answer to GAP-3.
- **Why this timing:** `Capabilities` and the factory are both declared frozen at the end of phase
  1, and `Capabilities` growing a field is additive only before it is frozen.
- **Close criteria:**
  - [ ] Whether a store's backing can be shared is declared (a capability field or an equivalent),
        so a missing `Sibling` is either a failure or an honest skip, in the same words UC-043 uses
        for `Begin`.
  - [ ] The `resumption` row states what it does when no second value is obtainable — a weaker
        same-value walk that is still mandatory, or a reported not-certified — rather than
        describing a method it cannot always use.
- **Status:** **closed** — `Capabilities.SharedBacking` makes the hook's requiredness derivable in the same words `Transactions` makes `Begin`'s, and §D.9's `resumption` row states what it does without a sibling: the same walk in a fresh reader over one store value, still mandatory, with only the *restart* half needing a second value. See `## Resolution — seam redesign`.

### GAP-50 [medium][immediate] `New` is infallible in all four sketches while the document requires it to refuse a disagreeing shared backing

- **Where:** §D.0 line 2515, §D.4 line 2830, §D.8 line 3227 and §D.9 line 3316 (all
  `store := eventmemory.New(...)`, one return value); §UC-054 lines 587-591 ("A store constructed
  over a non-empty backing that disagrees on any of them is refused at construction"); §D.4 lines
  2915-2924 ("`New` over a non-empty one that disagrees refuses at construction"); mode E2 line 298
  ("At construction, as a returned error or a panic on a programmer error").
- **What:** A constructor that can refuse cannot have the signature every sketch shows. The
  document also does not choose between E2's two permitted shapes for this refusal: a codec or
  cap disagreement over a shared backing is a composition mistake, which could be either a returned
  error or a panic, and the two produce different composition roots.
- **Why this severity:** §D.0 and §D.4 are the shapes a consumer copies, and the store constructor
  is the composition root's first line. It also propagates: `eventtest.Factory.New` is
  `func(t *testing.T) event.Store` and `Sibling` is `func(t, s) event.Store`, neither of which can
  carry a construction refusal.
- **Why this timing:** It is a public signature in the northstar and in the conformance factory,
  and every later sketch is written against it.
- **Close criteria:**
  - [ ] `New`'s shape matches its stated failure mode in every sketch, including §D.9's factory
        fields.
  - [ ] The document says which of E2's two shapes a shared-backing disagreement takes, and why.
- **Status:** **closed** — every sketch now writes `eventmemory.NewLog` and `eventmemory.New` as error-returning, §D.4 states the rule once (a store's spec carries deployment values, so E2's shape for it is a **returned error, never a panic**), and the conformance factory's `New` takes a `*testing.T` because only the factory knows how to report a construction failure. The disagreement itself is now structural rather than checked: the two data-bearing numbers live on the shared `Log`, so two store values over one log cannot disagree about them. See `## Resolution — seam redesign`.

### GAP-51 [medium][immediate] `AllQuery.Limit`'s zero value has two contradictory meanings, on the one path with no kernel in it

- **Where:** §D.14 line 3609 (`Limit int // always > 0`) and line 3599 (`StreamQuery.Limit int //
  always > 0; the kernel never asks for an unbounded page`); §INV-022's table line 2094 ("global
  read bound | the caller's query, capped by the store spec | `Limits()` | the store's default")
  and its statement line 2085 ("A zero value always means 'the declared default', never 'no
  limit'"); §D.14's `ReadAll` bullet line 3679 ("returns no more than the smaller of `Limit` and
  `Limits().MaxRead`").
- **What:** `AllQuery` is filled by a **consumer**, not by the kernel — §D.14 says `ReadAll` is
  "the one path with no kernel between the caller and the store". So its `Limit` is the one bound a
  caller can leave at zero, and the document says both that it is "always > 0" and that a zero
  means the store's default. `min(0, MaxRead)` is zero, so a store implementing the comment
  literally returns an empty page forever and a consumer's drain loop never terminates.
- **Why this severity:** Two stores will answer differently and the `global paging` section would
  pass both, because neither the section nor any control exercises a zero limit. It is small, but
  it is a zero-value meaning on a frozen struct, which is the class INV-022 exists to fix
  everywhere else.
- **Why this timing:** `AllQuery` is public and frozen, and INV-022's per-bound test is specified
  to build its cases from `Limits()`.
- **Close criteria:**
  - [ ] One meaning for `AllQuery.Limit == 0`, stated in §D.14 and §INV-022 in the same words.
  - [ ] The `bounds` or `global paging` section has a case for it, with the at-the-bound control
        INV-022 requires for every other bound.
- **Status:** **dissolved** by round 4 — the caller-supplied read limit is deleted along with `AllQuery`, `StreamQuery`, `AllPage` and `StreamPage`. The kernel is the only party that ever fills a read, `ReadAll(ctx, after Cursor)` has nowhere to put a limit, and `Limits().MaxRead` therefore has exactly one meaning. See `## Resolution — seam redesign`.

### GAP-52 [medium][immediate] `ErrKey` wraps the wiring class, but its ordinary cause is request data

- **Where:** §2.3 line 230 (`ErrKey` | UC-051 | "the framework's wiring/scope class"); §UC-051
  lines 804-806 ("An identity whose mapper output is empty — **the zero value of an identity type
  is the ordinary way to reach this** — or longer than the store's declared key cap"); §2.3 line
  242 (`ErrWrongStore` wraps the same class); Q1 lines 3721-3729, which asks about the conflict and
  too-large classes and about UC-031's wiring class, but not about this row.
- **What:** The two rows that wrap the wiring/scope class are a composition mistake (a view from
  the wrong backing) and a value that arrived with a request (a blank or over-long identity). They
  are the same class for a transport, so the two cannot be answered differently: the commonest
  cause of `ErrKey` — a request that omitted an id, or one whose tenant string exceeds this store's
  key cap — is reported as an operator wiring failure.
- **Why this severity:** [[D-040]]'s line is that a class decides a status and a retry posture. A
  client error rendered as a wiring failure is the wrong status, and it is also the shape that
  turns ordinary bad input into an operator page. INV-025 already forbids the key from travelling,
  so the caller has nothing else to branch on.
- **Why this timing:** The wrapped class is exported behaviour and Q1's reconciliation is
  specified to settle the class list against the tree; a row missing from that question is a row
  nobody settles.
- **Close criteria:**
  - [ ] §2.3 says which class `ErrKey` wraps for a caller-supplied identity and which for a
        mapper defect, or states why one class is right for both.
  - [ ] Q1 names `ErrKey` among the classes reconciliation must settle.
- **Status:** **closed** — `ErrKey` is in the **request** class (§2.3, §UC-051), because a blank or over-long identity arrives with a request and [[D-040]]'s line is that the class decides the status and the retry posture. Q1 is restated to settle the classes **per class** rather than per row, which is what the partition makes possible. See `## Resolution — seam redesign`.

### GAP-53 [low][deferred] Two accessors of an empty receipt are unspecified though every accessor must answer

- **Where:** §UC-020 lines 940-951 (an empty receipt "carries zero events, no position range, the
  version the view was already at — unbumped — and the **invalid** authority ... every accessor
  must answer rather than panic"); §D.14 lines 3631-3636 (`Empty`, `Stream`, `Version`,
  `Positions`, `Durability`, `Authority`).
- **What:** `Durability()` on an empty commit has no honest value — nothing was written, so it is
  neither `Committed` nor `PendingCaller` nor `Volatile` — and the document does not choose one.
  `Version()`'s contract also drifts: §D.14 says "the load version when empty" while UC-020 says
  the version the view was already at, which after a prior successful append is not the load
  version.
- **Why this severity:** Cosmetic today: no reader in phase 1 branches on an empty commit's
  durability, and both readings of `Version()` agree on the first append. It becomes real when a
  consumer decides post-commit work from `Durability()`.
- **Why this timing:** Accessor semantics with no dependents in this phase; decided when the
  receipt is written.
- **Close criteria:**
  - [ ] `Durability()` and `Stream()` on an empty commit have stated answers.
  - [ ] §D.14's `Version()` comment and §UC-020 use the same words.
- **Status:** **open (deferred)**, and much smaller than it was. Round 4 deletes the two accessors that had no honest answer — `Durability()` (the per-commit class is gone outright) and `Positions()` (a "for later" API with no phase-1 reader) — and replaces the one whose contract drifted, `Version()`, with `First()`, `Last()` and `Count()`, each with one meaning. What remains deferred is only the wording of `Stream()` on an empty receipt, which nobody in phase 1 reads. Carried into the spec's §8 Deferred. See `## Resolution — seam redesign`.

---

**Checked this round and found sound** (so the absence of a finding is deliberate): all thirteen
[ES] non-negotiables have a testable restatement and all ten of its capability/wrapper obligations
land somewhere, except obligation 3, which is GAP-47; every actor and entry mode resolves to a
`[happy]` use case and the map is correct; §2.3's twenty-one rows match the "twenty-one refusals
wide" claim and the twenty-two refusing use cases each point at a row; the UC-052 termination
argument is correct for Go — a struct cannot contain itself by value, so every cycle passes through
a kind that ends the walk; the removal of `event.Since` is the right branch and INV-010's
"does not compile" clause now says what it depends on; UC-060's two-answer classification is the
right shape and is the only place a store is allowed to know something the kernel cannot; the
`unchanged` row being a rule rather than a list is what makes INV-037 total; UC-059's control pair
(two families succeed, the same declaration bound twice succeeds) is the distinction the check
actually has to make; INV-005's key freeze and UC-050's control are the correct answer to a
computed wire value; `Backing.Equal` with an invalid zero that matches nothing including another
invalid one is the right shape and INV-015's three ordered defences are honest about which is
weakest; INV-038's per-type answer is correct and `stale := view` is genuinely inexpressible once
`Load` returns a pointer; the multi-page stream load cannot tear, because a stream is append-only
and its versions are dense, so any prefix a load sees is a consistent state; `event.JSON()` living
in `event` is a default implementation behind the `Codec` contract rather than policy in the
kernel, since nothing in the kernel branches on it; the five numeric defaults in §D.4 are declared
config with a named open question (Q10) demanding a calibration note, which is what
`universality.md` asks of a threshold; `event.Compose`'s length-prefixing is a mechanism, and the
`strconv` hazard for a non-string identity part is already covered by INV-005's freeze; and no use
case, invariant or DX line encodes the `Account` example — the two universality residuals this
round found (GAP-42's foreign value type, GAP-43's payload with a slice) are shape assumptions,
not domain literals.

---

## Resolution — seam redesign - 2026-09-06

Round 3 left twelve findings open, six of them `[high][immediate]`, and its own
audit recorded that **five of its eleven new findings had been introduced by
rounds 2 and 3's fixes**. That ratio is the signature of patching a shape rather
than changing it, and it is why round 4 changed the shape instead of writing a
thirteenth fix.

**The one root cause, and it is one object.** The spec had grown a live, mutable
`View` holding five jobs at once: a fold accumulator, the value handed to the
application, a self-advancing copy of the store's version, the transaction
binding, and one party to a storage-backing comparison. `architecture.md`'s
relocation test fails on it by inspection — it cannot be moved to its own package
without the store binding and the sealed declaration — and every open finding
below is a consequence of one object holding all five.

**What replaced it.** *The kernel never retains a Go value the application owns.*
`Load` hands the state over outright and forgets it; `Append` is a **repository**
call taking an unforgeable `At[S]` token carrying only (stream, version,
backing); a second decision in one operation advances the caller's own state
through one exported pure fold; nothing sealed crosses the store boundary in the
store's direction; and the codec moves from the store to the declaration, which
is the shape the tree already has (`jobs.Codec[P]`, `jobs.Upcast(from, to, fn)`).

**The count. Six dissolved, five closed, one deferred.** *Dissolved* means the
shape change makes the question unaskable, not that it was answered — a
distinction this file has been strict about since round 1, and one round 3's own
audit found earlier rounds over-claiming.

| # | Severity | Disposition | What did it |
|---|---|---|---|
| GAP-42 | `[high]` | **dissolved** | The transitive copier walk, `event.Copy` and `eventtest.Copies` are deleted. They existed to protect a live accumulator the kernel kept and handed out repeatedly; there is no such object, so there is nothing to copy. §UC-052 records why the mechanism was deleted rather than fixed: it refused `struct{ PlacedAt time.Time }`, its only escape (`func(o Order) Order { return o }`) was the exact value its own control called wrong, its proxy could not falsify it because nothing outside `time` can mutate a `time.Time`, and it was satisfied once per aggregate rather than once per kind. §INV-042 is the property that replaces it and §UC-061 is its falsification |
| GAP-43 | `[high]` | **dissolved** | `Fact.New` encodes **at the moment of decision** and the `Change` carries the value decoded back from those bytes, never the caller's. Mutating a payload between `New` and `Append` cannot change the recorded fact, and a fold written `s.Lines = e.Lines` aliases nothing the caller holds. The question "may the payload be mutated after `New`" has no subject. §UC-063 exercises it; §D.13 prices it at one encode plus one decode per change, paid even for changes a decision discards |
| GAP-44 | `[high]` | **closed** | Value by value (§D.15): `Commit` is kernel-assembled because `Store.Append` returns **only an error**, and `At[S]` is unpacked into `AppendRequest` and never reaches a store — those are the two whose unconstructibility carries weight and neither crosses the boundary. `Cursor` is a **defined string type** a store mints by conversion. `Backing` and `Authority` get exported minting constructors, and §INV-015 and §INV-028 are restated over what actually holds: a forged authority is never accepted as an argument by anything (the kernel obtains one from `store.Transaction(ctx)`), and a forged backing has nowhere to go because the only comparison is against the store's own `Backing()` on both sides. §INV-019 becomes an **executable** check in `make check`, plus the compile-time fixture — a trivial third store in `eventtest`'s own test package — that proves the seam today rather than in phase 2 |
| GAP-45 | `[high]` | **dissolved** | The append is `repo.Append(ctx, at, changes...)`. The token and the repository are two independently obtained values, so the backing comparison §INV-016 describes has two parties by construction and §UC-031's scenario is one ordinary line away. `view.Append`, which had one party and made the whole check vacuous, does not exist |
| GAP-46 | `[high]` | **closed**, with the finding's assumed answer rejected | The finding is right that nothing covered the case and right that it is the failure [[D-118]] is quoted for. It is wrong about the direction, and so was rounds 1–3's spec: **[[D-118]] already decided that a durable write made while the caller's transaction is bound to the store's own source is written *inside* it**, and that only an ambient executor which is *not* a transaction is refused — never autocommit. So the escape the finding feared is autocommit-while-a-transaction-is-open, and **joining is what prevents it**. §UC-046 and §INV-041 follow [[D-118]]; round 3's `ErrUnbound` is deleted; `ErrAmbientNotTransaction` is the refusal for the half [[D-118]] does refuse. Two subsystems answering one question two ways was itself the defect, and if a future owner wants the refusal back it needs a decision doc superseding [[D-118]] for **both**, not a paragraph in a spec |
| GAP-47 | `[high]` | **dissolved** | `Transaction(ctx)` is a **required** `Store` method, so the exact outer value always answers, nothing is discovered and nothing can be missing; and `Within` returns a **context** carrying a kernel-minted marker chained on any previous one, so no decorator is removed from the path for the duration of a transaction. The declared-chain-versus-exact-outer question has no subject left to rule on, [ES]'s capability obligation 3 is satisfied by construction, and [[D-115]]'s tunnel cannot reopen here. The bound-repository shape the finding's neighbours worried about is gone too: there is no bound value to call with a foreign context, and §UC-049 refuses a marked context whose store now answers a different transaction |
| GAP-48 | `[medium]` | **dissolved** | The pairwise "must be distinguishable from" column is deleted. §2.3 is a **partition**: six classes, one rule (`errors.Is` never crosses a class), no row-to-row claim, and a closed list of four declared wraps. `ErrSealed` wrapping `ErrDeclaration` is now legal and unremarkable — they are one class — so the contradicting sentence is not expressible rather than corrected. The second half of the finding disappears with `ErrStaleView`, which round 4 deletes outright |
| GAP-49 | `[medium]` | **closed** | `Capabilities.SharedBacking` makes `Sibling`'s requiredness derivable in exactly the words `Transactions` makes `Begin`'s, and §D.9's `resumption` row now states what it does when no second value is obtainable: the same walk in a fresh reader over one store value, still mandatory, with only the *restart* half needing a sibling. Generalised beyond this one hook by §INV-043: every capability is a **tri-state** whose `Unstated` zero `Bind` refuses, so no store can skip a section by forgetting a field |
| GAP-50 | `[medium]` | **closed** | `eventmemory.NewLog` and `eventmemory.New` return errors in every sketch, and §D.4 states E2's shape once: a store's spec carries deployment values, so its constructor returns an **error, never a panic**. The conformance factory's `New` takes a `*testing.T` because only the factory knows how to report a construction failure. The refusal that forced the question is also now largely structural: the two data-bearing numbers live on the shared `Log`, so two store values over one log cannot disagree about them at all |
| GAP-51 | `[medium]` | **dissolved** | The caller-supplied read limit is deleted, together with `AllQuery`, `StreamQuery`, `AllPage` and `StreamPage`. `ReadAll(ctx, after Cursor)` has nowhere to put a limit, the kernel is the only party that fills a read, and `Limits().MaxRead` therefore has exactly one meaning. `min(0, MaxRead)` is not a program anybody can write |
| GAP-52 | `[medium]` | **closed** | `ErrKey` is in the **request** class (§2.3, §UC-051), with the argument written where the refusal is raised: a blank or over-long identity arrives with a request, and [[D-040]]'s line is that the class decides the status and the retry posture. Q1 is restated to settle the classes **per class** rather than per row, which is what the partition makes possible |
| GAP-53 | `[low]` | **deferred**, and reduced | The two accessors with no honest answer are deleted — `Durability()` (the per-commit class is gone; `Capabilities.Persistence` is the one the suite reads) and `Positions()` (a "for later" API with no phase-1 reader). The one whose contract drifted, `Version()`, becomes `First()`, `Last()` and `Count()`, each with one meaning, so the drift is not expressible. What is carried into the spec's §8 is only the wording of `Stream()` on a receipt for an append that never reached a store, which nobody in phase 1 reads |

**One earlier deferral is closed rather than carried. GAP-26** asked what failure
`Replay`'s error could have, given that a fold cannot fail. Round 4 deletes
`Replay` — one store-free fold, one name, `Aggregate.Fold` — and gives the
survivor a named cause: a `Change` carrying a deferred encoding failure from
`Fact.New` must **surface** inside `Fold` rather than fold as a no-op (§UC-024,
§UC-041). The error is no longer a branch that cannot be taken. §8 now carries
GAP-25, GAP-41 and GAP-53.

**What else round 4 changed that no finding asked for**, listed so a later reader
does not read it as drift:

- The **codec moved from the store to the declaration** (`From`/`Then` each carry
  a `Codec[V]`). This deletes `Store.Codec()`, restores the encodability check to
  before `main` under [[D-021]], makes `Bind` O(1) so GAP-35's contradiction
  cannot recur, gives every revision its own codec, and makes "decode revision
  1's bytes with revision 2's codec" a defect that can at least be *stated*. Its
  cost is written down rather than hidden (§D.2, §UC-056): a deployment can no
  longer choose JSON in development and a binary format in production without a
  data migration.
- **`event.Since`, `KeyDomain`, `ErrBatch`, `ErrStaleView`, `Authority.BoundTo`,
  `Commit.Durability`, `Commit.Positions` and `event.Replay` are all deleted**,
  each with its reason in the spec's round-4 table.
- The **conformance suite falsifies itself on every run** against five
  deliberately defective decorators (§UC-045), and **honest non-certification**
  becomes a first-class outcome reported in the same words as an unclaimed
  capability (§UC-044).
- **One kernel-wide text rule** replaces the per-store key domain, and the
  per-store dial and its INV row go with it (§INV-033).
- **`crudsql.WithTransaction(ctx, tx)` was written in every round-3 sketch and
  does not exist** — it is an `Option`. The composition-root line is
  `crud.WithExecutorFor(ctx, source, tx)`, recovered by
  `crudsql.TransactionFor`. Fixed everywhere before it became a compile error a
  consumer hits on day one.
- The **savepoint case is decided explicitly** (§UC-049): a savepoint and its
  parent are one authority, because a savepoint has no commit of its own, and a
  store that minted a fresh authority per savepoint would falsify §UC-030's
  equality on a correct program.
- The **fail-safe classification is scoped** (§UC-060): "not certainly-not-written
  is uncertain" applies to a failure from the **append attempt itself** and must
  not swallow a closed store, a cancellation before any statement, the kernel's
  own pre-store refusals, or a failure from a method that issues no statement.
- **`Append` returns three values and there is one way to call it** (§UC-019,
  §D.5): a two-value convenience overload would be two ways to do one thing, and
  the shorter one would be the one everybody used — which is how the receipt, and
  with it the atomicity claim, would quietly leave the shape consumers copy.
- **Conflict and uncertainty recovery is a bounded procedure** (§UC-034), because
  `for { repo.Append(ctx, at, changes...) }` otherwise compiles into a livelock
  that can never write.
- **Costs are quantified rather than asserted** (§D.13): what a 10 000- and a
  100 000-event aggregate cost in memory and work, what the deleted `View` cost
  by comparison (~20 MB of retained payload bytes per in-flight view at 100 000
  events, and an O(n·k) re-fold), what `Fact.New` costs, and the product
  arithmetic behind every default.
- **Receivers are spelled `this`** throughout, matching the tree's dominant
  convention.

**What round 4 could not do, and it is one thing.** The refusal vocabulary is
still policed by a **table** — twenty-one sentinels in six classes is a rule a
test walks, not a mechanism that makes a wrong classification unspellable. That
is the largest remaining piece of the kernel that exists to enforce consistency
rather than to provide a capability, and it is where the next reduction should
look, probably by making the **class**, not the sentinel, the thing a caller
matches. It is recorded here rather than in §8 because it is a direction, not a
decision waiting on an owner.

**One departure from a live tree convention is on the record and unresolved.**
§UC-004 ships a panic-only declaration, and the tree does `Define`/`MustDefine`
in eight places. The tree's reason is one the blind spec did not have — the
negative tests are written against the returned error, and phase 1's declaration
checks are its largest cluster of negative cases — and a `MustDefine` that is
three lines over `Define` and returns the same value preserves §INV-013. Recorded
as **Q21** so the owner decides rather than an implementer.

---

## Round 5 - econv-usecase-validator (coverage + invariant + DX roles, post-rewrite re-audit) - 2026-09-06

Sources read: `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (whole file, 4127 lines),
`.agents/artifacts/gaps/EVENTSOURCE_P1_USECASES_GAPS.md` (rounds 1-3 and the round-4 resolution,
whole file), `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` (the non-negotiable
list and the capability/wrapper obligations), `CLAUDE.md`, `~/.claude/skills/econv/SKILL.md` and
`references/gaps.md`, `universality.md`, `microkernel.md`, `architecture.md`.

**Framework symbols the spec names were checked against the tree, and that check is kept separate
from the spec's own claims.** Every one exists and has the shape the spec attributes to it:
`crud.WithExecutorFor`/`crud.ExecutorFor`/`crud.KeyOf`/`crud.SameDataSource` (`crud/executor.go`),
`crudsql.Transaction`/`crudsql.TransactionFor`, and — the round-4 correction is right —
`crudsql.WithTransaction` really is an `Option` (`crud/adapter/crudsql/crudsql.go:35`) and not a
context binder. `crud.ErrConflict` and `crud.ErrUnavailable` exist (`crud/errors.go:32,38`);
`errs.KindTooLarge` and `errs.KindConflict` exist. `jobs.TransactionBinding` is
`struct{ value [32]byte }` with a refusing `MarshalJSON` and a `FromBytes` constructor
(`jobs/durability.go:305-323`), which is exactly the shape §D.14's `NewAuthority(over, [32]byte)`
copies; and `jobspg`'s stager does mint fresh entropy per call (`jobs/jobspg/stager.go:78`), so
§UC-030's and Q17's measured claim about non-comparability is accurate. `jobs.Codec[P]`,
`jobs.EncodedPayload`, `jobs.MaxPayloadBytes`, `jobs.DefaultPayloadBytes`, `jobs.MaxNameBytes` and
`jobs.NewCatalog` all exist. **No symbol the spec names is missing from the tree.**

**Enumeration.** UC-001…UC-063 each appear exactly once (UC-023 `WITHDRAWN` in place);
INV-001…INV-043 each appear exactly once (INV-037 `WITHDRAWN` in place). No number is cited
without being written and none is written twice.

**Regression sweep over the rewrite.** Every mention of a deleted construct (`View`,
`ErrStaleView`, `event.Replay`, `event.Copy`, `eventtest.Copies`, `AllQuery`/`StreamQuery`/
`AllPage`, `Authority.BoundTo`, `Commit.Durability`/`Positions`, `KeyDomain`, `ErrBatch`,
`ErrUnbound`, `event.Since`, `Store.Codec()`, the optional `Transactional`) is in a
"what was deleted and why" context; **no section still describes a removed shape as live**, and
UC-045's five defect sections all resolve to rows in §D.9's section table. §INV-036's own
falsification ("there are five occurrences of 'exactly once'") is literally true and all five are
prohibitions. The seam between the two sittings did not leave a contradiction of that kind.

**Round 3's twelve findings were re-checked against the new text rather than the resolution
note.** GAP-44 is closed only *partly* — see GAP-57 below, which is its unenumerated remainder.
GAP-42, GAP-43, GAP-45, GAP-47, GAP-48 and GAP-51 are genuinely **dissolved**: the objects they
quantified over (the copier walk, the deferred encode closure, `view.Append`'s single party, the
optional `Transactional`, the caller-supplied limit) are absent from the text, not merely
deprecated. GAP-46, GAP-49, GAP-50 and GAP-52 are closed as described. GAP-53 is correctly
reduced. **But GAP-43's dissolution moved the aliasing question one hop rather than deleting it**
(GAP-55), and GAP-46's answer opened a second escape route the finding did not consider
(GAP-56).

Checks run: every actor and entry mode against a `[happy]` use case; all thirteen [ES]
non-negotiables against a testable INV (all thirteen present); [ES]'s ten capability/wrapper
obligations against a UC or INV (all ten land; obligation 2 and 3 are satisfied by construction
now that nothing is discovered); the edge matrix; the three `universality.md` checks with the
second-instance test run on `struct{ PlacedAt time.Time; Total int64 }`, a state that is itself a
map, a payload carrying a slice, a composite identity, zero events, 100 000 events and a
three-revision chain; a silent-misuse sweep asking for each shape what a caller can do that
compiles, runs, returns no error and is wrong; Go-level compilability of every §D sketch against
the new signatures; the microkernel four-part test with `eventpg`'s signature constructed by hand,
value by value; and a UC-vs-UC, UC-vs-INV and INV-vs-§D contradiction sweep.

**Three of the twelve findings below were introduced by the round-4 rewrite** (GAP-54 by moving
the append onto the repository, GAP-55 by moving the encode into `Fact.New`, GAP-56 by adopting
[[D-118]]'s join without covering the two routes where nothing is bound). The rest are
pre-existing holes earlier rounds did not reach, plus one — GAP-57 — that is GAP-44's own
remainder.

### GAP-54 [critical][immediate] `Append` accepts a token and a change list that came from two different instances of one aggregate, and writes the facts into the wrong stream in silence

- **Where:** §D.5 line 3233 (`Append(ctx context.Context, at At[S], changes ...Change[S]) (At[S], Commit, error)`);
  §D.5 line 3235 (`At[S].Stream()` — "diagnostics; the input to nothing"); §D.3 lines 3133-3134
  (`Fact.New(payload E) Change[S]` — a change carries no stream); §D.10 lines 3609-3638 (the
  inventory of what cannot be written, and the **two** things a caller can still get wrong);
  §UC-019; §INV-015; §INV-020.
- **What:** `At[S]` and `Change[S]` are both parameterised by the **state type**, never by the
  aggregate *instance*. A token loaded for account A and a change list decided from account B's
  state are both `At[Account]` and `[]Change[Account]`, so
  `repo.Append(ctx, atFrom, creditChanges...)` compiles, is admitted at `atFrom`'s version, and
  records B's facts in A's stream. Every check the design has passes: the backing matches (one
  store), the version matches (the token is current), the wire types are declared on this
  aggregate (it is the same aggregate), and the fold accepts them (they are the right type). The
  document never mentions this pairing anywhere, and §D.10 — which introduces itself as "not a
  summary — an inventory, because a design's safety is what it makes **inexpressible**, and an
  honest one names what it does not" — lists exactly two residual mistakes and this is not one of
  them.
- **Why this severity:** The canonical two-instance operation is a transfer:
  `fromState, fromAt, _ := repo.Load(ctx, from)`, `toState, toAt, _ := repo.Load(ctx, to)`, two
  decisions, two appends. Swapping `fromAt` and `toAt` — two adjacent variables of the same type in
  the same block — puts the debit in the payee's stream and the credit in the payer's. Both
  appends return no error, both receipts are honest, both streams have dense versions, and a
  reload of either account folds facts it never decided. That is silent permanent corruption of the
  one thing the phase exists to make trustworthy, on the second-instance input of
  `universality.md`'s test: the design is correct for an operation that touches one instance of an
  aggregate — the only shape §D.0, §D.5, §D.6 and §D.8 ever show — and wrong for one that touches
  two. It also falsifies the phase's headline claim as a reader will take it: "the version is not a
  parameter, so it cannot be wrong" is true, and it hides that the **stream** is now a parameter,
  carried positionally in a bare value, and can be.
- **Why this timing:** It is the signature of the single call this phase is designed around. Every
  candidate answer changes a public contract — a change that carries its stream (`Fact.New(at,
  payload)` so `Append` can refuse a change from another stream), an `Append` that re-derives the
  key from the identity it is also given, or a named residual plus a runnable proxy. Round 3's
  `view.Append(ctx, changes...)` could not express the token half of this mistake at all, so the
  rewrite widened the surface and the document did not notice.
- **Close criteria:**
  - [ ] §D.10's inventory names this as a third thing a caller can still get wrong, in the same
        detail as the `Fold` line and the re-appended slice, with the failure spelled out.
  - [ ] The document states whether a `Change` can be bound to the stream it was decided for, and
        if not, why the framework cannot refuse a cross-instance append that it *can* refuse for a
        cross-aggregate one (§INV-020) and a cross-backing one (§UC-031).
  - [ ] A use case covers one operation over two instances of one aggregate — the transfer shape —
        with the correct pairing as its happy path and the swapped pairing as the case whose
        outcome the document must state.
  - [ ] If the answer is that the framework cannot see it, `eventtest` gets a proxy on the model of
        `Keys`, `Families` and the deleted `Copies`, because §D.1 and §INV-039 both argue that an
        obligation with no runnable proxy is one nobody discovers breaking.
- **Status:** **closed by a shape change** — a `Change` now names the stream it was
  decided for. `Fact.New(id, payload)` takes the aggregate's **own identity type**
  and renders the key through the declared mapper (`*Fact[S, ID, E]`), and `Append`
  admits a change list only when every change's stream equals the token's, refusing
  `ErrWrongStream` before any statement; `Fold` refuses a list naming two streams.
  §UC-064 is the use case, §INV-044 the invariant, and §D.10's inventory carries
  both the new prevented row and the residual it does not cover. Go cannot type a
  value by aggregate *instance*, so ladder level 1 is unavailable and level 3 is
  taken instead. The DX gains no framework type: the decision takes the identity it
  already holds. See `## Resolution — the pairing seam`.

### GAP-55 [high][immediate] A `Change` retains a value of the application's payload type, which falsifies §INV-042's statement and its check, and re-opens the inbound aliasing direction one hop further in

- **Where:** §2.1's `change` row line 234 ("the payload **already encoded** … plus the value
  decoded back from those bytes, which is what a local fold applies. A `Change` retains no value
  the caller can still reach"); §D.3 lines 3137-3148 (`New` "keeps two things: the frozen bytes,
  and the value decoded back **from those bytes**" … "A fold written `s.Lines = e.Lines` aliases
  nothing the caller holds"); §INV-042 lines 2845-2856 and its falsification lines 2863-2866;
  §INV-021's outbound clause lines 2461-2463; §INV-038's `Change[S]` row line 2750; §UC-063;
  §UC-052 lines 556-560.
- **What:** Two defects, one cause. (1) §INV-042's statement is "No type in `event`,
  `eventmemory` or `eventtest` keeps a value of an application's state type, **or a value of an
  application's payload type**, after the call that produced it returns", and its checkable
  property is "**no type in `event` retains an `S` or an `E` across a call boundary**". A `Change`
  is a type in `event` that retains an `E` across the boundary of `New` — the document says so
  three sentences later, as a design requirement. The stated falsification, "a source check over
  every struct in `event` asserting no field is assigned from a parameter of type `S` or `E` and
  outlives the call", therefore **must fail on a correct implementation**: the round-4
  load-bearing invariant has a check the design cannot pass. (2) The retained `E` is handed to the
  application's fold, and §D.3 explicitly blesses a fold that aliases it (`s.Lines = e.Lines`). So
  the caller's state ends up sharing a slice header with a value the framework holds inside a
  `Change`. The caller then mutates its own state — which §UC-052 says is entirely its business —
  and the `Change`'s decoded value changes with it. Folding that same `Change` again (§UC-021's
  advance, `Fold` called twice in a test, §D.10's legal re-append of a slice the caller still
  holds) now produces a value that **differs from the frozen bytes that were recorded**.
- **Why this severity:** The consequence is exactly the divergence §INV-021 exists to forbid,
  reached by the one path round 4 declared unaskable. A live locally-advanced state and a reload
  disagree, the store's history is perfect, and nothing reports anything — which is what makes it
  invisible. `struct{ Lines []Line }` and `struct{ Tags []string }` are the payload shapes §UC-063
  itself calls "All ordinary", so this is `universality.md`'s second-instance test failing on the
  instance the document chose to demonstrate the property with. Neither control catches it:
  §UC-061 mutates a state whose values came from *envelopes* (which the kernel does not retain) and
  §UC-063 mutates the caller's *pre-encode* payload (which the `Change` genuinely does not hold).
  The one sequence that fails — fold a change into state, mutate the state, fold the same change
  again — is written nowhere. §INV-038's "`Change[S]` … safe" for concurrent use is unsound for the
  same reason.
- **Why this timing:** It decides what `Change` holds and what `Fold` hands a fold — the two
  central data shapes of the phase — and it decides whether §INV-042's check is writable at all.
  The cheap fixes are contract-level: decode per fold rather than once at `New` (paying a decode
  per fold instead of one at `New`), state that the decoded value is the framework's and a fold
  must not retain it, or hand the fold a value the framework does not keep. All three change §D.3.
- **Close criteria:**
  - [ ] §INV-042's statement and its checkable property are restated so they are true of the
        design: whatever `Change` is allowed to retain is named as the one exception, with the
        reason, and the source check is rewritten so a correct implementation passes it.
  - [ ] The document states what the fold's `E` parameter may alias and what the application may do
        with it — specifically whether a fold may retain a reference kind reached through `e`, and
        what the framework guarantees if it does.
  - [ ] A use case or a control exercises the failing sequence: fold a change, mutate a reference
        kind the fold aliased out of it, fold the same change again, and assert the result still
        equals a reload.
  - [ ] §INV-038's `Change[S]` row states what "safe for concurrent use" means given that a fold
        can publish a path into its retained value.
- **Status:** **dissolved** — a `Change` retains no decoded value at all. `New`
  encodes and keeps the frozen bytes, the stream and an adapter that closes over
  the declaration; the value a fold receives is decoded **at the moment of the
  fold**. §INV-042's statement and its source check are therefore true of the
  design rather than falsified by it, "what may a fold retain out of a `Change`"
  has no subject, and the ordinary path pays one encode and no decode. §UC-065 is
  the falsifying sequence — fold, mutate, fold again, compare with a reload — that
  neither §UC-061 nor §UC-063 could reach. See `## Resolution — the pairing seam`.

### GAP-56 [high][immediate] An append escapes an open caller transaction onto autocommit by two ordinary routes, and §D.10 lists that escape as inexpressible

- **Where:** §D.10 line 3618 ("An operation that escapes the caller's transaction onto autocommit
  | The store answers what is bound and the kernel refuses or joins (§INV-041)"); §INV-041's first
  row line 2823 ("nothing | the operation runs on the store's own autocommit"); §UC-046 lines
  1428-1437 ("Why round 3's answer is deleted rather than narrowed") and its Precondition line
  1409-1412; §UC-030's Flow lines 1519-1523 ("Each **refuses to run outside the transaction it was
  given** … Both writes therefore ran in the one transaction the context carried, or one of them
  refused before writing anything"); §UC-028's "What `Within` is for" lines 1374-1386;
  §UC-040's Control line 1940 (a store declaring `Transactions: Unsupported`); §UC-033.
- **What:** §INV-041's three answers are keyed on **what the store finds in the context**, so they
  are silent about the two cases where the store finds nothing while a transaction is open. Route
  (i): the caller opens a transaction and never binds it into the context — one forgotten
  `crud.WithExecutorFor` line, and the mirror image of the omission §UC-046 exists for. Route
  (ii): the store's `Capabilities().Transactions` is `Unsupported`, which §UC-040's own control and
  §UC-033's precondition make a live shape, so `Transaction(ctx)` answers the invalid authority for
  every context and the append runs on autocommit while the caller's transaction is open. In both,
  `Load` and `Append` succeed, `Empty()` is false, the receipt carries the invalid authority, and
  nothing refuses. There is no use case for either. `Within` catches both (`ErrNoTransaction`,
  `ErrNoTransactionBinding`) — and §UC-046 is the section that tells a reader `Within` is not what
  admits the write and that an ambient transaction is joined without it.
- **Why this severity:** It is the failure [[D-118]] is quoted for on line 2834 of this document,
  "a placement that silently escaped the caller's transaction is the failure this whole path exists
  to prevent, and it would be invisible", reached by two routes the document does not check and one
  of which it makes look harmless. §UC-030's atomicity argument — the reason round 4 could delete
  the `BoundTo` branch from §D.6 — is *false* under route (i): with nothing bound, neither
  subsystem refuses, both write on autocommit, the caller commits or rolls back a transaction that
  contains neither write, and the guarantee "either both writes are in it or one of them refused"
  does not hold. Round 3's deleted branch would have caught exactly this, because a receipt from an
  autocommit append carries the invalid authority. §D.10's table then states the opposite of what
  §INV-041 permits, which is a direct contradiction between the two sections a reader consults for
  this question.
- **Why this timing:** It is the transaction seam's contract and the `transactions` conformance
  section, both frozen in this phase, and phase 2's SQL store is where the consequence is real. It
  also decides whether §UC-030's guarantee is stated as "by construction" or as "provided the
  caller called `Within` or the store can see transactions", which is what the sketch every
  consumer copies must say.
- **Close criteria:**
  - [ ] A use case covers "a transaction is open and the store finds nothing bound for its
        backing", stating the answer and why it is safe, or naming the refusal.
  - [ ] A use case or a clause covers "a transaction is bound and the store's
        `Capabilities().Transactions` is `Unsupported`", stating whether an append is admitted on
        autocommit and what tells the caller.
  - [ ] §D.10's escape row is narrowed to what §INV-041 actually guarantees, or §INV-041 gains the
        answer that makes the row true.
  - [ ] §UC-030's Flow states the precondition its "by construction" argument needs, and §D.6 says
        whether `Within` is required for the atomicity claim it makes — since §UC-046 currently
        reads as making it optional.
- **Status:** **closed**, and the honest answer is level four with a control.
  §UC-066 covers both routes and states the outcome: the store finds nothing of its
  own, §INV-041's first row applies, and the operation runs on autocommit. No
  mechanism can see it — the only observable is what is bound for this store's
  backing, and a transaction nobody bound is indistinguishable from no transaction.
  What is offered is the mechanism the caller opts into, `Within`, which refuses
  before any statement; the obligation is **stated as an application obligation**
  rather than implied; §D.10's escape row is narrowed to "an executor the store can
  see" and gains the residual as its third entry; §UC-030's Flow names the
  precondition its by-construction argument needs and §D.6 says the `Within` line
  is what enforces it; and the `transactions` conformance section **drives route 1
  and asserts the events survive a rollback**, which is the control that proves the
  obligation is real. See `## Resolution — the pairing seam`.

### GAP-57 [high][immediate] `Stream`, `Version`, `Position` and `Key` appear in the store contract's signatures and in values a store must fill, and none of them is written out anywhere

- **Where:** §D.14 lines 3766-3853 ("This is A5's whole surface. **If it is not here, a store
  neither implements it nor may rely on it**"), specifically `ReadStream(ctx, s Stream, after
  Version)`, `AppendRequest{Stream Stream; Expected Version}`, `Envelope{Stream Stream; Version
  Version; Position Position}`; §D.15 lines 3968-3977 (the value-by-value walk, whose first row is
  "`Envelope`, `Limits`, `Capabilities`, `Record`, `AppendRequest` | plain structs with exported
  fields, filled directly, no constructor"); §2.1's `stream` row line 225 ("The pair (family, key),
  compared byte-exact. Comparable"); §INV-019.
- **What:** A grep of the whole document for a `type` declaration of `Stream`, `Version`,
  `Position` or `Key` returns nothing. `Cursor` is rescued in prose ("a defined string type",
  §UC-053) precisely because GAP-44 forced the question for it; the four types beside it were never
  enumerated. A store must **decompose** a `Stream` into a family column and a key column on every
  append and read, must **construct** one for every `Envelope` it returns, must **mint** a
  `Position` for every event it writes, and must **compare** `req.Expected` against a committed
  `Version` and assign dense ones. If `Stream` is a struct with unexported fields — which is the
  shape every other identity-bearing value in this document takes, and which "Comparable" does not
  contradict — then §D.15's first row is false: `eventpg` cannot fill an `Envelope`, and `event`
  must grow accessors or a constructor it does not have.
- **Why this severity:** This is GAP-44's defect, unclosed for the values GAP-44's audit did not
  list. §INV-019 — "no value a store must return that it cannot produce from its own package" — is
  the phase's self-declared defining architectural test, and it cannot be evaluated for four of the
  types on the seam. Both resolutions available at implementation time have costs the document
  would not catch: exported fields on `Stream` mean a caller can build a `Stream` for any family
  including one it did not declare, which is a second way to reach the merged history §INV-039 and
  §INV-033 are written about; unexported fields plus minting constructors mean `event` grows API in
  phase 2, which is the zero-diff violation. §UC-043's compile-time fixture ("a deliberately
  trivial store in `eventtest`'s own test package … if any type the contract requires a store to
  return needed `event`-internal access, it would not compile") is the right mechanism and it is
  the reason this is `[high]` and not `[critical]` — the seam is proven or broken on day one rather
  than in phase 2 — but the document must say which answer that fixture is expected to compile
  against.
- **Why this timing:** §D.14 is declared frozen at the end of phase 1 and phase 2 is defined as
  "the identical suite against a different factory". A decision taken while writing `eventmemory`
  is a decision taken by accident, which is the exact wording §INV-019 uses about itself.
- **Close criteria:**
  - [ ] `Stream`, `Version`, `Position` and `Key` are written out in §D.14 at the fidelity of
        `Limits`, `Backing` and `Authority`: their declarations, and for each, how a package other
        than `event` constructs one and reads one.
  - [ ] If any of them is constructible by a caller, the invariant that assumed otherwise is
        restated over what actually holds — as §INV-015 and §INV-028 already do for the at-token
        and the authority — with particular attention to whether a caller-built `Stream` can name
        an undeclared family.
  - [ ] §D.15's per-value table gains a row for each, and §D.14's opening claim ("if it is not
        here…") is true after the addition.
- **Status:** **closed** — `Key`, `Version`, `Position` and `Stream` (plus
  `Cursor`, previously only in prose) are written out in §D.14 as a defined
  `string`, two defined `uint64`s, a two-exported-field comparable struct and a
  defined `string`. §D.14 gains a per-value table saying how a package other than
  `event` decomposes, constructs, mints and compares each, §D.15's walk shows
  `eventpg` doing it in `ReadStream` and `Append` bodies, and §INV-015 answers the
  hazard the finding named: a caller-built `Stream` has **nowhere to go** through
  the kernel, because `Repo` takes an identity and a token and `Log` takes a
  cursor, so the only surface accepting one is `Store`, which is the database
  handle. See `## Resolution — the pairing seam`.

### GAP-58 [high][immediate] `Backing.Equal` is required to be `crud.SameDataSource`'s answer and `event` is required not to import it for that

- **Where:** §2.1's `backing` row line 229 ("Backings compare with `Equal`, never with `==` … `Equal`
  is `crud.SameDataSource`'s question and **must not be a second answer to it**"); §INV-016 lines
  2383-2389 (the same sentence, plus the algorithm: "a nil or non-comparable identity matches
  nothing, dynamic types must match, and only then are the identities compared"); §D.14 line 3843
  (`func NewBacking(identity any) Backing`); §D.15 line 3986 ("**`event` imports** the standard
  library and `crud` — the latter **only for the error taxonomy** that decides what a refusal
  renders as"); Q4 line 4051 ("What remains open is whether the root `event` package should name
  `crud`'s executor vocabulary at all, or stay with the error taxonomy alone as it does today").
- **What:** `NewBacking` takes an `any` identity, so `Backing.Equal` must implement the
  nil-and-dynamic-type-aware comparison of two `any` values that §INV-016 spells out — which is
  `crud.SameDataSource`'s body (`crud/executor.go:562`, verified). The document requires
  simultaneously that this is not a second answer to that function and that `event` imports `crud`
  only for the error taxonomy. Both cannot hold: either `event` calls `crud.SameDataSource` (and
  §D.15's import statement and Q4's "as it does today" are wrong) or it reimplements the rule (and
  §2.1's and §INV-016's "must not be a second answer to it" is broken, along with
  `architecture.md`'s one-concept-one-implementation rule for a comparison performed on every
  append).
- **Why this severity:** It is a leaky abstraction on the kernel's import graph, which Q11's
  structural checks read, and it is a DRY violation of the kind `architecture.md` rates
  `[high]` — two implementations of one decision that will diverge, where the decision is "are
  these two stores writing to the same place" and the failure of divergence is
  §UC-031/§UC-054/§INV-016 answering differently from the rest of the framework. §INV-016's three
  controls (two backings unequal, two values over one backing equal, two invalid backings unequal)
  pass under either resolution, so nothing in the document distinguishes them.
- **Why this timing:** It changes the root package's import list, which §D.15 states as a
  contract and which Q11 and `make check-deps` measure; and it changes whether `Backing` is a
  wrapper over `crud`'s answer or a type with its own comparison semantics — a public shape phase 2
  builds against.
- **Close criteria:**
  - [ ] One answer is chosen and the same words appear in §2.1, §INV-016, §D.15's import paragraph
        and Q4.
  - [ ] If `event` does not import `crud.SameDataSource`, the document says what stops the two
        answers from diverging, and §INV-016's falsification gains the case that would catch a
        divergence (a `crud.Source` pair the two answers disagree about).
  - [ ] Q4's "as it does today" is corrected, since it is the only statement of the current import
        list and it is inside the question that is supposed to settle it.
- **Status:** **closed by deciding it** — `event` imports `crud`, and
  `Backing.Equal` **is** `crud.SameDataSource`; `Backing.Valid` is
  `crud.SameDataSource(id, id)`, which is `crud/catalog/set.go:54`'s own idiom.
  §2.1, §INV-016, §D.15's import paragraph and Q4 all say it in the same words, and
  §INV-016's falsification gains the divergence control the finding asked for: a
  `crud.Source` pair driven through **both** functions with the answers asserted
  identical. The reimplementation branch was rejected because it is
  `architecture.md`'s two-implementations-of-one-decision on a comparison performed
  on every append, and because `crud` is first-party and costs no `require` line —
  which is the question the finding correctly said was architectural rather than
  about dependencies. See `## Resolution — the pairing seam`.

### GAP-59 [high][immediate] The family-collision check is scoped to one `Binding` value, so a second `Open` — or a second store over one backing — defeats it silently

- **Where:** §UC-059 lines 781-784 ("The composition root binds both aggregates **through one
  `Binding`** … A `Binding` value remembers which families have been bound **through it**");
  §INV-039 lines 2776-2780 ("**What the framework can check** The reachable route — a declaration
  copied to start a second one with the family string unchanged, both bound at the composition root
  — is refused by `Bind`"); §D.4 line 3173 (`func Open(store Store) *Binding`, which nothing
  restricts to one call); §UC-007 line 682 ("through two `Binding` values"); §UC-054's rule line
  229/706-717 ("Two store values over one backing are **one store for every purpose in this
  document**"); §UC-055 (a store, and therefore a `Binding`, per request).
- **What:** The set of bound families lives on a `*Binding`, and a `*Binding` is produced by
  `Open(store)`, which any composition root may call any number of times and which §UC-007 already
  shows called twice. So the check catches the copied-declaration collision only when the root
  happens to funnel every `Bind` through one `Binding` value — a discipline the document never
  states and nothing enforces. Two `Binding`s over **one** store defeat it; two store values over
  **one backing** defeat it while §UC-054 simultaneously declares those two values "one store for
  every purpose in this document", which makes the two sections contradict each other on exactly
  the question the check exists to answer.
- **Why this severity:** What escapes is not a diagnostic but §INV-033's corruption reached by
  §INV-039's route: two aggregates over one history, each folding the other's facts, appending at
  versions derived from the other's, with no error at any point and every wire type declared on
  both sides so §UC-013's refusal never fires. §INV-039's "What the framework can check" paragraph
  is a claim about a mechanism, and the mechanism is escapable by one extra line that reads as
  ordinary modular composition. Round 2 rejected GAP-31's premise on the argument that `Bind` is a
  second meeting place; that argument is right and the scope chosen for the meeting place is one
  level too narrow.
- **Why this timing:** Where the set lives is a public shape (`Open`'s return, and whether `Bind`
  can be called from two roots), and moving it — to the store value, to the backing, or to an
  explicit "one `Binding` per process" rule with a refusal on a second `Open` — changes §D.4,
  §UC-007, §UC-054 and the `binding` conformance section together.
- **Close criteria:**
  - [ ] The document states the scope of the family set explicitly — per `Binding`, per store
        value, or per backing — and says what a second `Open` over one store does.
  - [ ] Whichever scope is chosen is reconciled with §UC-054's "one store for every purpose",
        either by widening the check to the backing or by naming this as an admitted exception with
        the reason.
  - [ ] §INV-039's "what the framework can check" states the escape it does not cover, in the same
        plain terms it uses for the cross-process residual.
  - [ ] §D.9's `binding` section drives the escape: two different declarations with one family,
        bound through two `Binding` values over one backing, with the stated outcome asserted.
- **Status:** **closed by stating the scope, and the wider mechanism is rejected
  on the record.** All four close criteria are met: §UC-006 states the
  one-`Binding`-per-store obligation and what a second `Open` does; §UC-054 names
  the bound-family set as the **one stated exception** to "one store for every
  purpose", so the two sections stop contradicting each other; §INV-039's "what it
  cannot" carries both halves — the cross-process case and the second `Binding` —
  in the same plain terms; and §D.9's `binding` section **drives the escape and
  asserts it is admitted**, with the one-`Binding` collision as its control, so
  silence cannot be read as a guarantee. **Rejected:** widening the check to the
  backing. There is no per-backing scope the kernel can hold — a `Store` is a
  foreign interface the kernel cannot attach state to, and the only wider scope is
  the package-level registry §INV-013 and §UC-001 both forbid, which would make
  declaration order matter, make two tests interfere and make an import side effect
  load-bearing. Q19 is widened to carry both residuals and names the additive
  answer. See `## Resolution — the pairing seam`.

### GAP-60 [medium][immediate] Every kernel-side validation of a store's honesty happens at `Bind`, and a consumer-only deployment never binds anything

- **Where:** §D.7 lines 3364-3376 (`Log`, `ReadOnly(store Store) Log`, `Read(log Log, after
  Cursor) *Reader`); §A4's row line 359 ("a `*Reader` over a read-only `Log`"); §INV-022's table
  lines 2491-2498 (every store-owned bound: "Zero means | **refused at `Bind`**"); §INV-043 lines
  2869-2872 ("`Bind` refuses a store that answers `Unstated`"); §D.4's four `Bind` checks lines
  3188-3193; §UC-036's Control line 1738.
- **What:** All four of the checks that make a store's self-description trustworthy — valid
  backing, no zero limit, no over-ceiling limit, no unstated capability — are performed by `Bind`,
  and nothing on the read path calls `Bind`. A projector or export process holds a `Log` (that is
  exactly what §A4 says it holds) and calls `Read`. A store answering `MaxRead: 0` therefore
  reaches a consumer unvalidated; §D.14 requires `ReadAll` to return "at most `Limits().MaxRead`"
  envelopes, so the honest implementation of a zero returns an empty page, `Next` answers false,
  and the drain loop terminates immediately having read nothing. §UC-036's Control ("a store with
  fewer events than one page returns them all in one page and then an empty one, so a reader that
  always asks again terminates") passes against exactly that store.
- **Why this severity:** It is GAP-51's failure — a limit whose zero means "read nothing forever"
  on the one path with no kernel between the caller and the store — re-entering from the store's
  side after round 4 closed it from the caller's. It is `[medium]` rather than `[high]` because the
  zero comes from a store rather than from a caller and any store that is also written through is
  validated at `Bind`; a read-only deployment over a mis-specified store is the reachable case.
- **Why this timing:** It decides whether `Read`/`ReadOnly` validate, which is a public entry
  point being frozen, and whether §INV-022's and §INV-043's "refused at `Bind`" wording is complete.
- **Close criteria:**
  - [ ] The document states where a `Log` obtained without `Bind` is validated, or states that it
        is not and why that is acceptable.
  - [ ] §INV-022's "Zero means" column and §INV-043's statement name every entry point that
        enforces them, not only `Bind`.
  - [ ] §UC-036 gains a control that a store answering a zero read bound is refused or reported,
        since the present control passes against it.
- **Status:** **closed** — the rule is stated over the **doors** rather than over
  one of them: a store enters the kernel by exactly two calls, `Bind` and `Read`,
  and both perform the three store-honesty checks (valid backing, no zero or
  over-ceiling limit, no unstated capability). `Read` therefore returns
  `(*Reader, error)` (§D.7, and the projector sketch checks it); §INV-022's "Zero
  means" column and §INV-043's statement name both; §UC-036 gains the control the
  finding asked for — a store answering `MaxRead: 0` is refused — beside the
  existing one, which passed against it. The family check stays `Bind`'s alone,
  because a read binds no declaration. See `## Resolution — the pairing seam`.

### GAP-61 [medium][immediate] The declaration class promises "never reachable at request time" and §UC-005 admits the route that reaches it

- **Where:** §2.3 line 311 (declaration class: "Raised as a **panic**, before `main`, never
  returned, **never reachable at request time**"); §UC-005 line 626 ("**Actor** A1 (by mistake,
  e.g. from an `init` in another package **or at runtime**)") and its Observed line 631-632; §UC-041
  line 1957 ("a test that folds and then declares another fact must be refused"); §INV-024.
- **What:** `ErrSealed` is in the declaration class, and §UC-005's own actor line says the late
  declaration can arrive at runtime. A late declaration on a request goroutine therefore raises a
  declaration-class panic at request time, which the class table says cannot happen. The class
  table is the frozen public statement a transport and an operator read; "never reachable at
  request time" is precisely the property a transport would rely on to decide it need not recover
  this class.
- **Why this severity:** A false property in the one table that partitions the refusal vocabulary,
  in the round that replaced a self-contradicting matrix with a partition specifically so a claim
  like this could not be wrong. The consequence is bounded — a panic that takes a process down for
  a programmer error is defensible — but the document must say that is what happens rather than
  say it cannot.
- **Why this timing:** One clause in a frozen table, read by the mandatory `refusal classes`
  section, and cheaper before a transport is written against the wrong reading.
- **Close criteria:**
  - [ ] The declaration class's description is true of `ErrSealed`'s runtime route, or §UC-005
        states that a runtime late declaration is out of the class and says which class it is in.
  - [ ] The document says what a process does with a declaration-class panic raised on a request
        goroutine — recovered by the transport, or fatal by design.
- **Status:** **closed by saying what actually happens.** §2.3's declaration class
  now reads "raised as a **panic**, never returned as an error; normally before
  `main`; the one route that reaches a request goroutine is a late declaration, and
  the framework deliberately does not soften it", and §UC-005 gains a paragraph
  saying the panic is not recovered, why taking the process down for a programmer
  error is the deliberate choice, and what a reader must not conclude. The property
  a transport can rely on is the true and narrower one: a declaration refusal is
  never *returned*, so nobody branches on it. See `## Resolution — the pairing
  seam`.

### GAP-62 [medium][immediate] §INV-024's falsification needs the intra-class wrap graph, and the document declares exactly one edge of it

- **Where:** §INV-024's falsification lines 2546-2549 ("A table test that walks every sentinel and
  asserts, for each of the other twenty, that `errors.Is` answers **true** only when they are in
  one class **and one wraps the other**, and **false** otherwise"); §2.3 lines 302-307 ("Within a
  class, one sentinel may wrap another … Outside the vocabulary there is a **closed list of four
  declared wraps**"); §UC-005's Refusal line 649 (`ErrSealed` wraps `ErrDeclaration` — the only
  intra-class wrap the document states); §UC-056's Refusal line 621 (`ErrCodecType`, whose relation
  to `ErrDeclaration` is unstated while §UC-004 lists its trigger under `ErrDeclaration`);
  §D.9's `refusal classes` row line 3582.
- **What:** The partition deleted the pairwise "must be distinguishable from" column, which is
  right, but the falsification still requires a **pairwise ground truth**: for every one of the
  21×20 ordered pairs it must know whether `errors.Is` should answer true. It knows the answer for
  cross-class pairs (always false) and for the four external wraps. For intra-class pairs it needs
  the wrap graph, and the document declares one edge of it (`ErrSealed` → `ErrDeclaration`) and
  leaves the rest open — including `ErrCodecType`, which §UC-004 and §UC-056 describe as one
  trigger with two sentinels, and the request class's three sentinels, of which §UC-024 says only
  that two of them "are distinct".
- **Why this severity:** A mandatory conformance section whose subject is not fully enumerated,
  which is round 1's GAP-14 and round 3's GAP-48 recurring one level down. The likely
  implementation outcome is a test that asserts only the cross-class half, which quietly deletes
  the intra-class assertions and lets a specific refusal stop being reachable from its general one.
- **Why this timing:** Sentinels and their wrapping are exported behaviour, the section is
  mandatory, and the fix is a short list rather than a matrix — which is the whole benefit the
  partition was adopted for.
- **Close criteria:**
  - [ ] §2.3 carries the closed list of **intra-class** wraps beside the closed list of four
        external ones, or states that there are none besides `ErrSealed` → `ErrDeclaration`.
  - [ ] `ErrCodecType`'s relation to `ErrDeclaration` is stated, since §UC-004 lists its trigger.
  - [ ] §INV-024's falsification names the list it walks, so the negative half is computable.
- **Status:** **closed** — §2.3 carries the **intra-class wrap list** beside the
  four external ones, and it has exactly two entries: `ErrSealed` → `ErrDeclaration`
  and `ErrCodecType` → `ErrDeclaration`, with "no other sentinel wraps another"
  stated for the remaining five classes. §UC-056's Refusal line names its wrap,
  §INV-024's falsification now reads that list — "true exactly when the pair appears
  in §2.3's intra-class list, false otherwise" — so the negative half is computable
  rather than guessed, and the count is corrected to twenty-one other sentinels.
  See `## Resolution — the pairing seam`.

### GAP-63 [medium][immediate] §INV-013's own falsification fails on the document's required API

- **Where:** §INV-013's falsification lines 2334-2337 ("An AST check over `event/...` for a
  package-level `var` of a mutable kind (map, slice, **pointer**, channel, func, **interface**, or a
  struct holding a `sync` type)"); §2.3's twenty-one sentinels lines 309-320; §D.0 line 2916
  (`var accounts = event.Define[Account](…)` — the declaration idiom, a package-level pointer);
  Q13 line 4060 ("The mutable-state half does not exist anywhere and must be written as an AST
  check").
- **What:** The check as specified flags every one of the subsystem's own exported sentinels: a
  `var ErrConflict = errors.New(…)` is a package-level `var` of **interface** kind, which is the
  shape the tree uses everywhere (`crud/errors.go:32` is the model the spec points at). It also
  flags the package-level `*Aggregate` and `*Fact` values that any fixture inside
  `event/eventtest` must hold, which is the idiom §D.0 teaches. So the only check the document
  offers for one of [ES]'s thirteen non-negotiables cannot pass against a correct implementation,
  and Q13 records it as the one that "must be written" — against a predicate that is wrong.
- **Why this severity:** A stated invariant whose only falsification is unsatisfiable is a claim,
  which is the rule §5's own preamble sets ("An invariant with no falsification is a claim"). The
  invariant itself is sound and testable; what is wrong is the predicate, and the fix is one
  clause — which is why this is `[medium]` and not `[high]`.
- **Why this timing:** Q13 hands this check to the plan as work, and a plan that implements the
  predicate as written produces a check that must be suppressed on first run, which is how a
  structural check becomes decorative.
- **Close criteria:**
  - [ ] The predicate is restated so it targets **mutation** rather than kind: immutable
        package-level values (error sentinels, declarations that are sealed on first observation)
        are named as permitted, with the reason each is safe.
  - [ ] Q13 carries the corrected predicate, since it is the entry that hands the work forward.
- **Status:** **closed** — §INV-013 carries the predicate as a table over
  **mutation** rather than kind: an error sentinel initialised by `errors.New` and
  never reassigned is permitted; an `*Aggregate`/`*Fact` declaration value is
  permitted *because* the seal makes its table read-only for its whole observable
  life; a `var` that is the target of an assignment, an index assignment, a
  `delete` or an `append` outside its initialiser is a violation, as is any map or
  slice `var` and anything holding a `sync` type. Q13 carries the corrected
  predicate, since it is the entry that hands the work to the plan. See
  `## Resolution — the pairing seam`.

### GAP-64 [medium][immediate] "Two calls inside one live transaction must answer `Same`" has no stated lifecycle, and the one sketch that implements it leaks an entry per transaction forever

- **Where:** §D.14 lines 3788-3790 ("Called on every operation. **Two calls inside one live
  transaction must answer `Same`**, and two live transactions must not"); §D.15 lines 3929-3930
  (`live sync.Map // *sql.Tx -> [32]byte, entered on first sight, **dropped at end**`) and lines
  3954-3965; §INV-028; §UC-049's savepoint paragraph lines 1467-1481.
- **What:** The contract's `Same`-across-calls requirement forces a store to memoise a token per
  live transaction, because minting fresh entropy per call — which is what the tree's only
  precedent does (`jobspg`'s stager, verified: `d.token()` per call) — fails it. §D.15's sketch
  memoises in a `sync.Map` keyed by `*sql.Tx` and comments "dropped at end", and a `*sql.Tx` offers
  no completion callback: nothing tells the store the transaction ended. As written, the normative
  phase-2 shape retains one 32-byte entry plus a map slot for **every transaction the process ever
  ran**. The obvious alternative — deriving the token from the `*sql.Tx` pointer value — is worse
  and unstated: Go may reuse a freed address, so two different transactions could compare `Same`,
  which is exactly the failure §UC-030's control is written to catch.
- **Why this severity:** An unbounded map on the main write path of every transactional
  application, in the sketch the document offers as its proof that the seam works. It is
  `[medium]` because it is phase-2 code rather than a phase-1 contract violation — but the
  *contract clause* that forces it is phase 1's, and it is stated with no implementability note.
- **Why this timing:** The clause is in the frozen contract and the `transactions` conformance
  section drives it (`begin, begin again, append in each, assert the two authorities are Same`),
  so a store must satisfy it to pass. The document should say how, or weaken the clause to what a
  store can keep without a lifecycle hook.
- **Close criteria:**
  - [ ] §D.14 states how a store is expected to know a transaction ended, or scopes the `Same`
        requirement to what is implementable without knowing (for example, to two calls with the
        same executor value observable in the context).
  - [ ] §D.15's `live` map either shows the eviction point or is replaced by a derivation that
        needs none, and the pointer-reuse hazard is named so nobody reaches for the cheap answer.
  - [ ] §D.13's cost table, which prices `store.Transaction(ctx)` at "a context lookup and a
        comparison", says what the memoisation costs in memory per live transaction.
- **Status:** **dissolved** — there is no memo, so there is no eviction point to
  specify. An `Authority` **is** its transaction's identity held by reference —
  `NewAuthority(over Backing, transaction any)`, the `*sql.Tx` itself — and `Same`
  is `Backing.Equal` then `crud.SameDataSource`. Nothing is minted per call and
  nothing is remembered, so §D.15's `secret` and `live sync.Map` are deleted; the
  savepoint rule becomes structural, because `crudsql`'s savepoint returns the
  parent `*sql.Tx`; and the pointer-reuse hazard the finding named as "worse and
  unstated" is **prevented by the retention itself** — a live authority keeps its
  transaction reachable, so two live authorities either name one transaction or
  hold two distinct live pointers. §D.14's clause is scoped to live transactions,
  §INV-028 carries the whole argument, and §D.13 prices `Transaction(ctx)` at a
  lookup, an assertion and a struct fill. See `## Resolution — the pairing seam`.

### GAP-65 [low][immediate] One dangling cross-reference and one code fragment that does not compile

- **Where:** Entry mode E2's row line 374 ("never as a panic, because a store's spec carries
  deployment values (**§UC-050n**)") against §UC-006 line 670 ("**§UC-050n is not a thing**; the
  rule is stated here once"); §UC-021's snippet line 1094
  (`at, _, err := repo.Append(ctx, at, first...)`) against §D.5's identical shape line 3266
  (`at, _, err = repo.Append(ctx, at, first...)`).
- **What:** E2, which is a normative row of the entry-mode table, cites an anchor that does not
  exist and that the document elsewhere says does not exist. Separately, §UC-021 writes the
  two-decision advance with `:=` where `at` and `err` both already exist (the snippet uses `at` on
  the right-hand side, so it must pre-exist), which does not compile; §D.5 writes the same three
  lines correctly with `=`. Two spellings of one canonical shape, one of them wrong.
- **Why this severity:** Cosmetic in effect. It is raised because round 1's GAP-16 established
  that a dangling reference in this document is how an item leaves the agenda, and because §UC-021
  is the shape §D.10 and §UC-062 both point a reader at.
- **Why this timing:** Both fixes are one character each and cost nothing now; the fragment is what
  a consumer copies.
- **Close criteria:**
  - [ ] E2's row cites §UC-006, which is where the rule is actually stated.
  - [ ] §UC-021's snippet uses the same assignment forms as §D.5's.
- **Status:** **closed** — E2's row cites §UC-006, which is where the returned-error
  rule is actually stated, and §UC-006's parenthetical no longer refers to the
  non-existent anchor. §UC-021's snippet uses `=` on the two lines whose variables
  all pre-exist and `:=` only on the line introducing `second`, matching §D.5, with
  a sentence saying why. See `## Resolution — the pairing seam`.

### GAP-66 [medium][deferred] Two of the suite's five self-falsification defects cannot be expressed by a forwarding decorator without doing what §UC-048 forbids one to do

- **Where:** §UC-045's table lines 2135-2141 (the five defects, expressed as decorators: "The suite
  wraps the factory's own store in **five deliberately defective decorators**"); §UC-048's
  Must-not-happen lines 1883-1891 ("It must not **replay, buffer or re-inspect** an
  `AppendRequest`'s records"); §D.9's `self-falsification` row line 3586 (`always`).
- **What:** "Ignores `AppendRequest.Expected` and admits every append" cannot be done by
  forwarding: the wrapped store enforces the expected version, so the decorator must first read the
  stream and rewrite `Expected` to the store's actual version — a read-modify the contract does not
  contemplate. "Leaves a rolled-back transaction's events readable" cannot be done by forwarding
  either: the wrapped store discards the staged work, so the decorator must buffer the
  `AppendRequest`'s records and serve them from its own memory afterwards, which is the one thing
  §UC-048 names as forbidden. The other three (short page, ahead-of-watermark cursor, reused
  position) are ordinary output rewrites and are fine.
- **Why this severity:** A mandatory `always` section specified in terms of a mechanism that is not
  available for two of its five cases. The likely outcome is that those two are written as
  something else (a hand-built defective store rather than a decorator) or quietly dropped, and the
  section's value is that all five are checked on every run.
- **Why this timing:** Deferred: nothing outside the suite depends on how the defects are
  expressed, and the section can be written correctly once the suite exists. Recorded so it is not
  discovered as a surprise.
- **Close criteria:**
  - [ ] §UC-045 says how each defect is constructed where a forwarding decorator cannot express it
        — a purpose-built defective store, or a stated exemption from §UC-048's obligations for the
        suite's own fixtures.
  - [ ] §UC-048's "must not buffer an `AppendRequest`'s records" states that it binds production
        decorators and not the suite's deliberate defects, or the defect is re-expressed.
- **Status:** **closed rather than carried**, because it cost two sentences.
  §UC-045's table gains a "how it is built" column: the three output rewrites stay
  forwarding decorators, and the two that cannot be — ignoring `AppendRequest.
  Expected`, and serving a rolled-back transaction's events — are **purpose-built
  defective stores** inside `eventtest`'s own test package, which is the fixture
  §UC-043's control and §INV-019's compile-time proof already require to exist.
  §UC-048 gains a "who these obligations bind" clause: a value claiming to forward,
  not a fixture claiming to be broken. §D.9's `self-falsification` row and §UC-048's
  control say three decorators rather than five. See `## Resolution — the pairing
  seam`.

---

**Checked this round and found sound** (so the absence of a finding is deliberate): the UC and INV
numbering is complete and unduplicated across both sittings, and the withdrawal-in-place of UC-023
and INV-037 is done correctly with forwarding notes; no section, sketch, refusal row, invariant or
flow still names a removed mechanism as live, and every `§D` sketch except §UC-021's fragment
compiles against the new signatures; every actor and entry mode resolves to a `[happy]` use case
and the map is correct; all thirteen [ES] non-negotiables have a testable restatement and all ten
capability/wrapper obligations land, with obligations 2, 3 and 5 now satisfied **by construction**
rather than by rule, which is the strongest form; §INV-036's self-referential grep claim is
literally true (five occurrences, all prohibitions); the class partition genuinely makes GAP-48's
contradiction unspellable, and its three-sentence growth rule is the right shape; deleting the
caller-supplied read limit genuinely makes `min(0, MaxRead)` unwritable **from the caller's side**
(GAP-60 is the store's side and a different question); the `Cursor`-as-defined-string decision with
its cost stated is the honest resolution of GAP-44 for the cursor, and `Commit` being
kernel-assembled because `Store.Append` returns only an error is a real structural answer rather
than a promise; §INV-019's executable `git diff` arm plus the third-package compile fixture is the
right pair of mechanisms and is a genuine improvement over a promise; §UC-034's bounded recovery
procedure correctly names the livelock and stops at one retry with the right argument; §UC-060's
fail-safe scoping list is exhaustive over the paths that exist; §UC-049's savepoint decision is
correct and its reasoning (an authority claims which durable commit carries the write) is the right
one; §UC-052's deletion of the copier walk is genuinely a dissolution — the walk, `event.Copy` and
`eventtest.Copies` are absent from the text and §UC-061 is a real falsification of the property
that replaced it; the `time.Time`-holding state in §D.0 is the second-instance test passing where
round 3 failed it; `Support` as a tri-state with an `Unstated` zero refused at `Bind` is the right
generalisation of GAP-49; §D.13 prices the design rather than asserting it, including what the
deleted shape cost; and every framework symbol the document names exists in the tree with the shape
attributed to it, including the `crudsql.WithTransaction`-is-an-`Option` correction, which was a
real defect in three earlier rounds.

---

## Resolution — the pairing seam - 2026-09-06

Round 5 left thirteen findings open: two `[critical]`/`[high]` clusters about
what a value *pairs with*, one about types the contract used and never declared,
and eight smaller ones. Round 4's own audit recorded that **three of round 5's
findings were introduced by round 4's rewrite** — the ratio this file has been
tracking since round 1 — so the first question asked of each was whether a change
of shape makes it unaskable, and only then whether a rule closes it.

**The count. Two dissolved, eleven closed, none deferred, one premise rejected on
the record.** *Dissolved* keeps the meaning it has had since round 1: the object
the finding quantified over is absent from the text, not deprecated.

| # | Severity | Disposition | What did it |
|---|---|---|---|
| GAP-54 | `[critical]` | **closed by a shape change** | A `Change` names the stream it was decided for. `Fact.New(id, payload)` takes the aggregate's **own identity type** — not a framework type — and renders the key through the declared mapper, so `Append` refuses a change list decided for another instance with `ErrWrongStream`, before any statement. §UC-064, §INV-044, and the new prevented row plus the named residual in §D.10 |
| GAP-55 | `[high]` | **dissolved** | A `Change` retains **no** decoded value. `New` keeps frozen bytes, a stream and an adapter that closes over the declaration; a fold decodes at the moment of the fold. §INV-042's check becomes satisfiable, the aliasing question loses its subject, and the common path gets cheaper. §UC-065 |
| GAP-56 | `[high]` | **closed**, at ladder level four, with a control | Nothing can see a transaction that was never bound. §UC-066 states that plainly, names `Within` as the mechanism the caller opts into, narrows §D.10's escape row, gives §UC-030's argument its precondition, and has the `transactions` section **assert the unsafe outcome on purpose** so it cannot become a belief |
| GAP-57 | `[high]` | **closed** | `Key`, `Version`, `Position`, `Stream` and `Cursor` are declared in §D.14, with a per-value table of how a package other than `event` obtains each, and §D.15 shows `eventpg` doing it. §INV-015 answers the caller-built-`Stream` hazard: it has nowhere to go through the kernel |
| GAP-58 | `[high]` | **closed by deciding it** | `event` imports `crud`; `Backing.Equal` **is** `crud.SameDataSource` and `Valid` is `SameDataSource(id, id)`, which is `crud/catalog/set.go`'s own idiom. §2.1, §INV-016, §D.15 and Q4 all say it once, and the falsification gains the divergence control |
| GAP-59 | `[high]` | **closed by stating the scope**, wider mechanism rejected | All four close criteria met: the scope stated (§UC-006), the exception named (§UC-054), the escape stated in §INV-039, and the `binding` section **driving it and asserting it is admitted**. A per-backing check has no home the kernel can hold; Q19 carries both residuals |
| GAP-60 | `[medium]` | **closed** | The three store-honesty checks run at **both** doors, `Bind` and `Read`. `Read` returns `(*Reader, error)`, §INV-022 and §INV-043 name both, and §UC-036 gains the control that the old one passed against |
| GAP-61 | `[medium]` | **closed** | §2.3's declaration class says what actually happens to a late declaration on a request goroutine, and §UC-005 says the panic is deliberate and unrecovered. The property a transport can rely on is the true, narrower one: never *returned* |
| GAP-62 | `[medium]` | **closed** | §2.3 carries the closed **intra-class** wrap list — two edges, both into `ErrDeclaration` — and §INV-024's falsification reads it, so the negative half is computable |
| GAP-63 | `[medium]` | **closed** | §INV-013's predicate is a table over **mutation** rather than kind, permitting error sentinels and sealed declaration values by name. Q13 carries it forward |
| GAP-64 | `[medium]` | **dissolved** | An `Authority` **is** its transaction's identity held by reference. Nothing is minted, nothing memoised, nothing to evict; the savepoint rule becomes structural, and the pointer-reuse hazard is prevented by the retention itself |
| GAP-65 | `[low]` | **closed** | E2 cites §UC-006; §UC-021's snippet uses the assignment forms that compile, matching §D.5 |
| GAP-66 | `[medium][deferred]` | **closed rather than carried** | Two of the five self-falsification defects become purpose-built defective **stores**; §UC-048's obligations are scoped to a value claiming to forward |

**The premise rejected, and it is GAP-59's implied fix.** The finding offers
"widen the check to the backing" as one of two acceptable reconciliations. There
is no per-backing scope available: a `Store` is a foreign interface the kernel
cannot attach state to, and any process-wide map keyed by backing is the
package-level mutable registry §INV-013 forbids, §UC-001 forbids by name, and
round 2 already argued costs more than it buys — it makes declaration order
matter, makes two tests interfere and makes an import side effect load-bearing.
The other reconciliation the finding offers, "naming this as an admitted
exception with the reason", is the one taken, and it is taken in four places
rather than one so that no section can be read alone and mislead.

**Why GAP-54's answer is `New(id, payload)` and not `New(at, payload)`.** Both
give a `Change` its stream. The token form threads a framework type through every
decision function in every application, and it leaves §UC-041's store-free fold —
the one whose whole point is that no store exists — needing a token only a store
can mint. The identity form uses the application's own type, a value the caller
already holds from the command it is serving, and the framework cross-checks it
against the token the load minted. That is one value threaded and one comparison,
not a second thing to remember. The alternative is in §D.12's rejected list with
this argument.

**What round 5 changed that no finding asked for, listed so a later reader does
not read it as drift:**

- `Declare` returns `*Fact[S, ID, E]` rather than `*Fact[S, E]`, because minting
  a change needs the mapper. All three parameters are still inferred at the call
  site; `Change[S]` is unchanged at one parameter, so folds and appends see one
  type. §D.11's inference table carries both rows.
- `Fact.New` is a third door at which a key is produced, so the kernel's text rule
  and non-emptiness run there too, and an illegal key rides the `Change`'s
  existing deferred-failure channel (§UC-051, §D.3). The store's `MaxKey` stays at
  `Load`/`Append`, which is the payload ceiling's split exactly.
- `Aggregate.Fold` refuses a change list naming two streams, which is free once a
  change has a stream and is always a mistake, and it is now the third named cause
  of `Fold`'s error return (§UC-041, §D.8).
- `§INV-042` is widened from `S` and `E` to `S`, `E` and **`ID`**: a `Change` and
  an `At[S]` hold the mapper's *rendering*, which is a string the framework made,
  and never the identity value.
- §D.13 is repriced: `Fact.New` is one encode, a fold is one decode per change,
  and `store.Transaction(ctx)` is a lookup, an assertion and a struct fill with no
  allocation and no map.
- §5.3's heading becomes "Rounds 4 and 5's invariants" and §D.10's becomes "the
  three things it still can", because both counts moved.
- The round-4 deletion table's `StreamPage` row is disambiguated: the deleted
  thing was a page struct, and the surviving `StreamPage` is a field of `Limits`.

**What is still owed, and it is unchanged.** §8 carries GAP-25, GAP-41 and
GAP-53, none of which round 5 touched. §9 carries the seven things a later phase
does. The one direction round 4 recorded as its own largest remaining reduction —
that the refusal vocabulary is policed by a table rather than by a mechanism —
is smaller after round 5 (the intra-class wrap list makes the table's ground truth
computable rather than inferred) but it is not gone, and it is still where the
next reduction should look.

---

## Round 6 - econv-usecase-validator (coverage + invariant + DX roles, post-pairing-seam re-audit) - 2026-09-06

Sources read: `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (whole file, 4914 lines),
`.agents/artifacts/gaps/EVENTSOURCE_P1_USECASES_GAPS.md` (every round and both resolutions),
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` (the non-negotiable list, the ten
capability/wrapper obligations, the base-seam table), `CLAUDE.md`, `~/.claude/skills/econv/SKILL.md`
and `references/{gaps,universality,microkernel,architecture,data-integrity,restrictions,
building-blocks}.md`.

**Framework symbols the spec names, checked against the tree, and kept separate from the spec's
own quality.** `crud.SameDataSource` is `crud/executor.go:562` and does exactly what §INV-016
attributes to it for an untyped nil, a type mismatch and a non-comparable value (one attribution
inaccuracy for a *typed* nil, GAP-78). `crud.KeyOf` (`executor.go:524`), `crud.ExecutorFor`
(`:370`), `crud.WithExecutorFor` (`:341`) and `crud.IsTransaction` (`:38`) exist with the shapes
the document uses. `crudsql.Transaction(executor crud.Executor) (*sql.Tx, bool)`
(`crudsql.go:195`) and `crudsql.TransactionFor` (`:210`) exist, and **§UC-049's savepoint premise
is correct**: `(*savepoint).Tx()` returns `this.parent.tx` (`:232`), so a savepoint and its parent
resolve to one `*sql.Tx`; the round-4 correction that `crudsql.WithTransaction` is an `Option`
(`:35`) also still holds. `crud.ErrConflict` and `crud.ErrUnavailable` are `crud/errors.go:32,38`;
`errs.KindTooLarge` is `errs/code.go:58` and `errs.TooLarge()` is `errs/build.go:25`.
`jobs.Upcast[A,B](from Codec[A], to Codec[B], fn)` is `jobs/upcast.go:25` and `invokeUpcaster`
recovers a panic into a sentinel (`:138-144`), which is the shape §INV-017 cites.
`tenancy/errors.go` does import `crud`, so §INV-016's precedent claim is real. **No symbol the
document names is missing or misattributed**, with the one exception noted in GAP-78.

**Enumeration.** UC-001…UC-066 each appear exactly once (UC-023 `WITHDRAWN` in place);
INV-001…INV-044 each appear exactly once (INV-037 `WITHDRAWN` in place). No number is cited
without being written, none is written twice, and §2.3's "twenty-two sentinels" is arithmetically
correct (3+7+3+4+2+3), as are §INV-024's "twenty-one others" and "22 × 21 ordered pairs".

**Regression sweep over the round-5 fixes.** Every occurrence of `View`, `ErrStaleView`,
`event.Replay`, `event.Copy`, `eventtest.Copies`, `AllQuery`/`StreamQuery`/`AllPage`,
`Authority.BoundTo`, `Commit.Durability`/`Positions`, `KeyDomain`, `ErrBatch`, `ErrUnbound`,
`event.Since`, `Store.Codec()`, the optional `Transactional` and `event.NewCursor` sits in a
"deleted" or "rejected" context; **no section describes a removed shape as live**. Every §D sketch
compiles against the current signatures, including §UC-021's (GAP-65 genuinely fixed) and §D.15's
`return event.NewAuthority(this.backing, tx)` against `(Authority, error)`. Five smaller misses the
round-5 fixes did not reach are in GAP-76, and one is a real regression introduced by GAP-58's fix
(GAP-75).

**Round 5's thirteen findings re-checked against the new text, not the disposition note.**
GAP-55 and GAP-64 are genuinely **dissolved by a change of shape**: the object each quantified over
(the `Change`'s decoded value; the minted-and-memoised authority token) is absent from the text, and
§INV-042's source check and §D.13's pricing are now consistent with the design. GAP-54 is **closed
by a change of shape** and is the strongest fix in the round — but it closed the pairing error at
one of its two call sites, and GAP-70 below is the other. GAP-57, GAP-58, GAP-60, GAP-61, GAP-62,
GAP-63, GAP-65 and GAP-66 are **closed by added rules or added text**, which is the weaker form, and
three of them left the fix incomplete in a neighbouring section (GAP-74, GAP-75, GAP-76). GAP-56 and
GAP-59 are **closed by stating an obligation** — the weakest form available and, in both cases,
correctly argued as the only one available; both now carry a conformance section that drives the
escape and asserts it is *not* prevented, which is the right way to hold a stated obligation.

Checks run: every actor and entry mode against a `[happy]` use case (all seven actors, all six
modes, map correct); all thirteen [ES] non-negotiables against a testable INV (all present, though
item 9 lands in §5.2's INV-036 rather than in §5.1, which the §5 preamble's "INV-001…INV-014
restate [ES]'s list" glosses over); [ES]'s ten capability/wrapper obligations (obligations 1-7 land
squarely, 8 and 9 are conditioned on wrappers phase 1 does not build, 10 is the weakest — the
document has method inventories but no baseline that *fails when the seam grows*, which is what
`make api` exists for and which §9 should name); the edge matrix; the three `universality.md`
checks with the second-instance test run on `struct{ PlacedAt time.Time; Total int64 }`, a state
that is itself a map, a payload carrying a slice, a composite non-comparable identity, zero events,
100 000 events, a three-revision chain and a generated declaration; the aliasing sweep in all three
directions; a silent-misuse sweep; the microkernel four-part test with `eventpg`'s eight methods
constructed by hand, value by value, naming the first type it cannot produce; `architecture.md`'s
metric table counted rather than eyeballed; and a UC-vs-UC, UC-vs-INV and INV-vs-§D contradiction
sweep.

**Two of the twelve findings below were introduced by the round-5 fix round** (GAP-75 by GAP-58's
import decision, and one clause of GAP-76 by GAP-55's fourth aliasing clause). GAP-70 is GAP-54's
own remainder. The rest are pre-existing holes no round has reached, and GAP-67 is the largest of
them: it is the half of the extension contract that runs from the store back to the kernel.

### GAP-67 [critical][immediate] The store→kernel failure-classification channel is not declared anywhere, so `eventpg` cannot express a conflict, a certainly-not-written failure, an unknown commit or a bad cursor without `event` growing API

- **Where:** §D.14 lines 4473-4477 ("This is A5's whole surface. **If it is not here, a store
  neither implements it nor may rely on it**"), line 4519 (`Append(ctx, req AppendRequest) error`),
  lines 4644-4657 (the `ReadAll` and `Append` per-method bullets); §2.3 lines 435-442 ("A store
  never adds a sentinel; it returns its own error and **the kernel maps it into exactly one row**");
  §UC-060 lines 1469-1494; §UC-034 lines 1841-1842; §D.15 line 4756 (the value-by-value table's last
  row, "`error` | its own, classified through `errs/sqlerr`, mapped by the kernel"); §D.9 line 4258
  (the mandatory-when-supplied `store failure classification` section); §D.16 line 4809 (the claim
  that the contract has "a stable typed data shape in **both** directions" and that "round 5
  finished it"); §INV-019.
- **What:** The contract requires a store to make **four** classifications the kernel cannot make
  for itself, and declares a vocabulary for none of them. (1) *Conflict* — §D.14 says "a unique
  violation on (family, key, version) is the conflict", but `Append` returns a bare `error` and the
  kernel is forbidden to re-read the stream to check (§UC-019: "The framework must not re-read the
  store"). (2) *Certainly-not-written vs unknown* — §UC-060 makes this "part of the contract"
  and §D.14 repeats it, and there is no type, sentinel, wrapper, interface or field through which a
  store says which one it means. (3) *Retryable* — `ErrBackend` carries the framework's retryable
  class "when and only when the store classified the failure retryable", by an unstated means.
  (4) *A cursor this store did not mint* — `ReadAll` refuses "with an error the kernel maps to
  `ErrCursor`", again by an unstated means. §2.3's sentence is the one that makes the hole
  visible: a kernel that has been handed an opaque driver error has no information with which to
  "map it into exactly one row", and §2.3 simultaneously forbids the store the only obvious
  alternative ("a store never adds a sentinel"). §D.15's "classified through `errs/sqlerr`" does not
  close it either: `errs` has codes for unique violation and serialisation failure, and **no code
  that means "the commit outcome is unknown"** — the one classification that is genuinely
  event-specific and genuinely only the store can make.
- **Why this severity:** Every resolution grows `event`'s exported surface — an
  `event.NotWritten(err)/event.Uncertain(err)` wrapper pair, an `Outcome` returned beside the error,
  an interface a store's error implements, or a declaration that a store may return the kernel's own
  sentinels (which contradicts §2.3). Deciding that in phase 2 is a diff to `event/`, which is
  exactly what §INV-019 — the phase's self-declared defining architectural test — forbids, and
  §INV-019 says of itself that a decision taken while writing the first store "is a decision taken
  by accident". Deciding it wrongly is worse than late: the kernel's fail-safe default is "anything
  that does not say *certainly not written* is `ErrUncertain`", so an implementer with no channel
  either reports **every** ordinary backend failure as uncertainty — which §UC-060 itself calls the
  failure mode that makes the signal worthless ("a signal that fires on everything is read as
  firing on nothing") — or invents a channel that `eventmemory` and `eventpg` spell differently, in
  which case the `store failure classification` section passes for one and fails the other for
  reasons the contract cannot diagnose. And the third possibility is the dangerous one: an
  unconfirmed write reported as a plain backend failure is §UC-060's own "silent double write".
  This is GAP-44 and GAP-57's defect for the one value on the seam that neither audit enumerated,
  and it falsifies §D.14's opening sentence and §D.16's first row as written.
- **Why this timing:** §D.14 is declared frozen at the end of phase 1, §1 states what `eventpg`
  must write with zero `event/` diffs, and the conformance suite's `Fail` hook already asserts
  `FailNotWritten → ErrBackend` and `FailUnconfirmed → ErrUncertain` — a test of a channel that
  does not exist. It is a public contract three sections and one whole conformance section depend
  on.
- **Close criteria:**
  - [ ] §D.14 declares the vocabulary a store uses to classify an `Append` failure — the exported
        type, wrapper or sentinel set — at the fidelity `Backing` and `Authority` now have, and
        states which classifications are the store's and which the kernel derives.
  - [ ] The same for the cursor refusal on `ReadAll` and for the "not a transaction" answer from
        `Transaction`, so every store→kernel error path has one named spelling.
  - [ ] §D.15's per-value table gains a row for the classified error showing how `eventpg` produces
        one from its own package, and §2.3's "a store never adds a sentinel; the kernel maps it"
        sentence is made true of that mechanism or replaced.
  - [ ] §INV-019's falsification covers it: the third-package trivial store in `eventtest` must be
        able to return a classified failure of each kind without `event`-internal access.
- **Status:** **closed** — the channel is declared, and it is the tree's own answer rather than a
  second one. §D.14 gains `Outcome` (a closed enum of six: `Unclassified`, `Conflict`, `NotWritten`,
  `Unconfirmed`, `Closed`, `BadCursor`) and `Failure(outcome, cause) error`, at the fidelity
  `Backing` and `Authority` have, plus the **total map** from outcome to a row of §2.3 and the
  door-dependent fail-safe default for an unclassified error (`ErrUncertain` from `Append`,
  `ErrBackend` from a read). That is `jobs.RejectPlacement` + `normalizeSenderError`'s shape: an
  extension names a classification the kernel owns and everything else normalises to the safe answer.
  §2.3's "a store never adds a sentinel; the kernel maps it" is now a mechanism, §UC-060 and §UC-057
  say which value a store returns in which window, §UC-047 gives `Closed` its spelling, §D.14's
  `ReadAll` bullet gives `BadCursor` its own, and `Transaction`'s error stays **bare** because it has
  exactly one meaning. §D.15 shows `eventpg.classify` doing all of it from its own package and gains
  two table rows, including one for the unknown commit — the classification `errs` has no code for,
  which is why the channel had to exist. §INV-045 is the new invariant; §INV-019's compile-time proof
  requires the third-package fixture to return each outcome. `eventtest.FailKind` is **deleted** and
  the hook takes an `event.Outcome`, so the classification is spelled once in the subsystem. See
  `## Resolution — the store's own voice`.

### GAP-68 [high][immediate] The outbound aliasing direction is closed for a state and for a fold's `E` and left open for `Envelope.Payload`: a store need only *freeze* the bytes it returns, and the kernel does not clone

- **Where:** §INV-021 lines 2906-2917, clause (3) ("A store **freezes** each `Envelope.Payload` it
  returns and must not reuse the buffer across pages; the kernel does **not** clone on decode")
  read against clause (1)'s definition of the two words ("clones the codec's output **and** freezes
  it with a full-slice expression"); §D.7 lines 4016-4018 (`Reader.Events() []Envelope`); §D.14
  lines 4639-4649 (the `ReadStream` and `ReadAll` bullets, which say nothing about payload
  ownership); §UC-052 lines 663-670 and §UC-061, which bless the caller mutating anything it was
  handed; §D.9 line 4248 (the `payload ownership` section, which asserts a **stronger** property
  than §INV-021 states).
- **What:** "Freeze" and "clone" are two different acts and §INV-021 uses them deliberately: a
  full-slice expression stops an `append` from reaching the store's array, and stops nothing else.
  A store that returns `payload[:n:n]` over its own stored array satisfies clause (3) literally.
  Two callers then reach that array. (a) A4 holds `Reader.Events()` and every `Envelope.Payload` in
  it; `e.Payload[0] = 0` writes into the memory store's history, and no rule anywhere forbids a
  consumer from touching bytes it was handed. (b) A3 never sees the envelope, but the kernel hands
  those exact bytes to the revision's codec without cloning — clause (3) says so as a deliberate
  cost decision — so any codec that decodes a `[]byte` or string field by aliasing its input (the
  ordinary shape for a compact binary codec; `event.JSON` happens to be safe) publishes the store's
  own array into the application's state, which §UC-052 then invites the caller to mutate in place.
  The result is that §UC-061's assertion — "the reload returns the fold of the stream, which
  disagrees with the mutated value" — becomes false, because the reload folds the mutated bytes.
- **Why this severity:** It is silent, permanent corruption of the store's own history reached by
  the one path this document argues is safe, and it is `universality.md`'s second-instance test
  failing: the clause is correct for a store whose payloads came out of a driver row scan and wrong
  for the store the phase ships. §D.9's `payload ownership` row already asserts the right property
  ("mutating a returned payload changes no later read"), so the *test* and the *invariant* disagree,
  and the invariant is the normative half an implementer of `ReadStream`/`ReadAll` reads — §D.14's
  per-method bullets do not mention payload ownership at all.
- **Why this timing:** It is one clause of the frozen store contract and it decides a per-event
  cost on the read path, which §D.13 prices. Deciding it after `eventmemory` is written means
  deciding it by measuring rather than by contract, and the phase-2 store inherits whichever answer
  was accidental.
- **Close criteria:**
  - [ ] §INV-021 clause (3) says which of *clone* or *freeze* a store owes for a payload it returns,
        with the memory store named as the case that decides it, and §D.14's `ReadStream`/`ReadAll`
        bullets carry the same obligation where an implementer will read it.
  - [ ] The document states what a caller may do with an `Envelope.Payload` it was handed and with a
        reference kind a codec may have aliased out of one, in the same words §UC-065 uses for a
        fold's `E`.
  - [ ] §UC-061's control is extended to the case that would falsify it: a state whose value aliases
        a decoded payload, mutated, then reloaded.
  - [ ] §D.9's `payload ownership` section and §INV-021 assert the same property in the same words.
- **Status:** **closed by moving the obligation to the party that knows.** §INV-021's clause (3) no
  longer says *freeze*; it says **a store hands out, in every `Envelope.Payload`, bytes it will never
  read again** — so it clones when the bytes it would otherwise pass are bytes it retains, and does
  not when they are not. That closes both paths the finding names with one rule: A4's
  `Reader.Events()` and the aliasing codec that publishes the store's array into application state.
  It is free for a SQL store (a row scan is already its own allocation) and costs the in-memory store
  one copy per envelope, which §D.13 prices — the right way round, since the cost falls on the store
  used in tests. It is also the boundary `jobs.invokeUpcaster` already treats this way
  (`bytes.Clone` in, `bytes.Clone` out). §INV-021 gains the "what a caller may do with what it is
  handed" sentence in §UC-065's words, §D.14's `ReadStream` and `ReadAll` bullets carry the
  obligation where an implementer reads it, §UC-061 gains the aliasing-codec case and a second
  control, and §D.9's `payload ownership` row asserts the same property in the same words with the
  overwrite-and-re-read case that tells the two readings apart. The rejected alternative — a
  kernel-side clone on decode — is in §D.12 with its argument: it pays on every store and still
  leaves `Reader.Events()` open. The "three clauses" count becomes four (GAP-76). See
  `## Resolution — the store's own voice`.

### GAP-69 [high][immediate] "Close discards the staged transactional work" is true of the memory store and false of every store that stages inside the caller's transaction, and a mandatory conformance section asserts it

- **Where:** §UC-047 lines 2061-2089 ("Staged transactional work is **discarded**, never committed
  by the close itself — the transaction is the caller's, and its own commit or rollback is the
  authority") and its Control lines 2086-2089; §INV-032 lines 3151-3157 ("That work is discarded,
  and its **absence** is the assertable half"); §D.9 line 4251 (the `lifecycle` section, `always`:
  "staged work is **absent**"); §D.14 line 4658 (`Close` "closes nothing it did not open");
  §UC-055 line 2075; §INV-041 line 3329 (the join row, which is what puts the work in the caller's
  transaction in the first place).
- **What:** §UC-047's sentence contradicts itself in its own two halves. Under §INV-041 an append
  that finds a bound transaction is written **inside it**, so for any SQL store the staged rows live
  in the caller's `*sql.Tx` and not in the store. `Close` then closes nothing it did not open, which
  §UC-055 and §D.14 both require, so it cannot discard anything: the caller's own `Commit` makes the
  rows permanent, exactly as the second half of the sentence says. "That work is discarded" is
  therefore false for `eventpg`, and the `lifecycle` section — which runs `always`, so `eventpg`
  must pass it — asserts a property a correct `eventpg` does not have. Depending on how the suite
  spells "absent" it either fails a correct store (commit after close, and the events are there) or
  passes vacuously (roll back after close, and they were never going to be there).
- **Why this severity:** `universality.md`'s second-instance test, on a mandatory section: the close
  semantics are written from the shape of the one store that owns its own staging, and the neighbour
  input is the store the whole contract exists for. It is round 1's GAP-3 shape — a mandatory
  conformance section asserting something a legitimate store cannot keep — and round 1 rated that
  class critical; it is `high` here only because the consequence is a mis-specified test rather than
  lost data.
- **Why this timing:** `Close`'s contract is frozen in this phase and the `lifecycle` section is
  written against it. A suite written to the current wording is a suite that either has to be
  loosened for phase 2 (which `restrictions.md` §5 forbids) or was vacuous from the start.
- **Close criteria:**
  - [ ] §UC-047 and §INV-032 separate the two cases: a store that owns its staging discards it, and
        a store that stages in the caller's transaction affects nothing, whose commit or rollback is
        the sole authority.
  - [ ] §D.9's `lifecycle` row states what "staged work is absent" means for each of the two, so the
        assertion is not vacuous for either.
  - [ ] §D.14's `Close` bullet carries the same split, since it is the sentence an implementer reads.
  - [ ] The `transactions` section's rollback case and the `lifecycle` section's close case are
        distinguishable, so neither passes on the other's behaviour.
- **Status:** **closed by separating the two store shapes and asserting only what is true of both.**
  §UC-047 now says `Close` **neither commits nor rolls back**, then names the two shapes: a store
  that owns its staging discards it, a store that stages inside the caller's transaction holds none
  of it and touches nothing — which is what §UC-055 and §D.14 already required and what round 5's
  "discarded" contradicted. The assertable property common to both is **invisibility**: immediately
  after a close with an unresolved transaction, no other reader sees that work, because under one
  shape it is gone and under the other it is uncommitted. §D.9's `lifecycle` row asserts exactly
  that, with the commit-before-close control that stops "invisible" passing against a store that
  hides everything, and explicitly asserts **nothing** about a commit issued after a close — which
  §UC-047 names as store-dependent and hands to the caller as an ordering obligation Go's own
  `defer` order already satisfies. §INV-032's title and statement change with it, and §D.14's
  `Close` bullet carries the split. The `transactions` rollback case stays distinguishable: it
  resolves the transaction with the store still open. See `## Resolution — the store's own voice`.

### GAP-70 [high][immediate] §INV-044 closes the two-instance pairing error at `Append` and leaves the identical error open, and unnamed, at `Aggregate.Fold`

- **Where:** §INV-044 lines 3423-3449 ("A `Fold` refuses a change list naming two streams, because a
  state is one instance's" — the whole of what `Fold` checks); §UC-041 lines 2188-2198; §UC-064's
  Flow line 2303 ("`Fold` performs the half it can") and its residual lines 2331-2335, which names
  only the domain-vocabulary mistake; §D.8 lines 4099-4106; §D.5 lines 3894-3902 (the two-decision
  shape, `state, err = accounts.Fold(state, first...)`); §D.10 lines 4295-4335, which introduces
  itself as an inventory of what a caller can still get wrong and lists three things.
- **What:** Round 5 closed `repo.Append(ctx, toAt, debitChanges...)` because a token and a change
  list are two independently obtained values of one Go type. `accounts.Fold(fromState, toChanges...)`
  is the same mistake between the same two kinds of value on the other call the design added, and it
  is not closed and not named: a state carries no stream, so `Fold` cannot compare, and the check it
  does perform (one stream per list) passes on a list that is entirely the *other* instance's. The
  consequence is not local. In §UC-021's two-decision shape the caller folds the wrong instance's
  changes onto this instance's state, decides again on that state naming **this** instance's
  identity, and appends through **this** instance's token — so every §INV-044 comparison passes, the
  version matches, the backing matches, and this account's history gains a fact decided from another
  account's state, with no error anywhere. §D.10's "One thing that is not on either list" covers
  `fromState.Debit(to, amount)` — a mistake written in the domain's own vocabulary — and this is not
  that: it is two adjacent variables of one type swapped at a framework call, which is the exact
  class §UC-064 exists to refuse.
- **Why this severity:** Same failure and same reachability class as GAP-54, one call site over. The
  document's strongest DX claim after round 5 is that the mechanical pairing error is refused
  deterministically; leaving one of the two calls unguarded *and* unlisted makes §D.10's stated
  contract with the reader — "a design's safety is what it makes inexpressible, and an honest one
  names what it does not" — untrue.
- **Why this timing:** `Aggregate.Fold`'s signature is public, it is the one function §UC-041,
  §UC-021, §UC-062 and §D.8 all point at, and the candidate closures change it (a `Fold` that takes
  the at-token the caller already holds; a `Fold` on the repository beside the store-free one; or an
  explicit residual with a runnable proxy on `eventtest.Keys`'s model). Choosing after the fold is
  written is choosing by accident.
- **Close criteria:**
  - [ ] The document states whether `Fold` can compare its change list against the state's stream,
        and if not, why the framework can refuse this pairing at `Append` and not here.
  - [ ] §D.10's inventory names it as a fourth thing a caller can still get wrong, in the same
        detail as the forgotten `Fold` line, with the failure spelled out.
  - [ ] §UC-064's residual distinguishes the mechanical crossing at `Fold` from the domain-vocabulary
        mistake it currently names, since only one of the two is a framework-shaped error.
  - [ ] If the answer is that the framework cannot see it, `eventtest` gets a proxy, on the argument
        §D.1 and §INV-039 both make: an obligation with no runnable proxy is one nobody discovers
        breaking.
- **Status:** **closed by a shape change, the same one round 5 made one call earlier.**
  `Aggregate.Fold` takes the aggregate's own identity — `accounts.Fold(id, state, changes...)` — and
  refuses any change whose stream is not that identity's, which is the identical comparison `Append`
  makes and subsumes round 5's weaker "one stream per list" check (a two-stream list has at least one
  change that is not `id`'s). The framework therefore **can** compare, so the finding's fourth
  criterion (a proxy for what it cannot see) does not apply to this crossing. The DX gains no
  framework type and nothing to remember: the identity is the value the caller loaded with, and it
  now appears at every call that is about one instance — `Load(ctx, id)`, `New(id, payload)`,
  `Fold(id, state, …)` — while `Append` needs none because both its values already carry their
  stream. The token form was rejected for the reason §UC-064 already rejected `New(at, payload)`:
  it leaves §UC-041's store-free fold needing a token only a store can mint (§D.12). §UC-041,
  §UC-064's Flow, §INV-044, §D.1, §D.5, §D.8, §D.11 and §UC-021's and §D.5's snippets all move
  together. **What remains is named rather than hidden**: a *state* carries no stream, so
  `accounts.Fold(id, otherState, changes...)` still passes — that is §D.10's new fourth residual,
  §UC-064's residual now separates it from the domain-vocabulary mistake it used to be lumped with,
  and no proxy can close it either, because a proxy would need to know which `Load` produced which
  state, which is the retention §INV-042 forbids. See `## Resolution — the store's own voice`.

### GAP-71 [medium][immediate] The savepoint assertion §UC-049 hands the conformance suite contradicts §INV-028, and the `Factory` has no hook that can produce a savepoint

- **Where:** §UC-049 lines 1664-1667 ("The conformance suite drives it: begin, begin again, append
  in each, assert the two authorities are `Same`"); §INV-028 lines 3056-3060 ("two **live**
  transactions on one backing answer authorities that do not [compare `Same`]") and its
  falsification lines 3102-3108; §D.9 line 4254 (the `transactions` row: "savepoint and parent are
  one authority, **two live transactions are two**"); §D.9 lines 4200-4207 (`Factory`) and lines
  4218-4223 (the hook-requiredness table); §D.15 lines 4781-4787 (`eventpg`'s `Begin`, which calls
  `Beginner().Begin(ctx)` on the store's own source).
- **What:** The factory's only transaction hook is `Begin(t, ctx, s event.Store) (context.Context,
  Tx)`, which begins **from the store** — §D.15's own implementation opens a top-level transaction
  on the datasource. "Begin, begin again" through that hook therefore produces two independent live
  transactions, which §INV-028 and §D.9 both require to be **not** `Same`; §UC-049 tells the suite to
  assert they *are*. And there is no hook, and no method on `eventtest.Tx` (which has only `Commit`
  and `Rollback`), through which the suite can obtain a savepoint of an existing transaction — so
  the `transactions` section's savepoint assertion, which is the whole reason round 5 made the rule
  structural, cannot be written against the declared factory.
- **Why this severity:** A mandatory section carries an assertion that states the opposite of the
  invariant it is testing, and the affordance it needs is missing from a factory the document
  freezes. The likely outcome is the one GAP-49 already produced once: the case is written as
  something else or quietly dropped, and the property round 5 spent a finding on stops being
  checked.
- **Why this timing:** `Factory` is the exported surface every store implementer writes against, and
  a hook added later is a breaking change to every store's test file.
- **Close criteria:**
  - [ ] §UC-049's sentence says which two calls it means, and it agrees with §INV-028 and §D.9 on
        what "begin, begin again" must answer.
  - [ ] The `Factory` (or `eventtest.Tx`) gains whatever is needed to open a savepoint of a live
        transaction, with its requiredness derivable from the capability report the way `Begin`'s and
        `Sibling`'s are, or the savepoint case is stated as a store-side obligation with no
        conformance assertion and §UC-049 says so.
  - [ ] §D.9's `transactions` row lists both the not-`Same` case and the savepoint case with the hook
        each needs.
- **Status:** **closed, and the factory grows no hook** — which is the second reconciliation the
  finding offers. §UC-049's sentence was simply wrong and is replaced: "begin, begin again" through
  the only hook that exists produces two independent live transactions, so the `transactions` section
  asserts they are **not** `Same`, in agreement with §INV-028 and §D.9. The savepoint case is not a
  claim about a store at all — it is a claim about the authority's derivation — so it is asserted
  where it is one, in two places that exist: `NewAuthority(b, tx)` twice over one identity answering
  `Same`, in `event`'s own tests, which is the whole content of the structural rule and which a store
  minting fresh entropy per call fails; and `crudsql`'s `(*savepoint).Tx()` returning the parent's
  `*sql.Tx`, in `eventpg`'s own package test in phase 2, now §9 item 8. Adding a `Savepoint` hook was
  rejected on §D.9's own rule — its requiredness would be derivable from no capability, which is
  exactly the defect GAP-49 raised — and a savepoint is `crudsql` vocabulary the memory store has
  none of. §D.9 also gains a paragraph naming the three properties that are deliberately *not*
  sections, because they do not vary with the store. See `## Resolution — the store's own voice`.

### GAP-72 [medium][immediate] A panicking fold is disposed of in half a sentence, in a class whose meaning and whose declared wrap do not fit it, and no use case covers it

- **Where:** §INV-017 lines 2837-2846 ("A panic from **either** is recovered and reported as that
  same refusal"); §UC-016 lines 1135-1152 (the only use case, whose subject is an upcaster); §2.3
  line 391 (the history class: "*The stored history cannot be read by this declaration*") and line
  430 (the declared wrap: "`ErrUpcast` → **the application's own error**"); §INV-004; §D.16 line
  4811 ("the panic-recovery discipline around every application callback (§INV-017)").
- **What:** A fold is `func(S, E) S` with no error channel, and the framework calls it once per
  event on every load. The most ordinary Go fold defect — writing into a nil map, which §D.0's own
  fold guards against by hand, and which is guaranteed on any aggregate whose state type *is* a map
  (§UC-052 blesses `type Ledger map[string]int64`) — panics. §INV-017 disposes of that in one word
  ("either"), assigns it to `ErrUpcast`, and no use case, control or conformance row covers it.
  Three things then do not fit: `ErrUpcast` is in the **history** class, whose stated meaning is
  that the *stored history* cannot be read, while a panicking fold is a programmer error and the
  history is fine; `ErrUpcast`'s declared wrap is "the application's own error", and a recovered
  panic value is not one; and a load of a single-revision fact — a stream with no upcaster anywhere
  in it — can now return `ErrUpcast`, which the caller reads as "an upcaster refused".
  §UC-005 shows the document is willing to take the process down for a programmer error and argues
  why; this clause silently takes the opposite line for the other application callback on the read
  path, without saying it is doing so.
- **Why this severity:** The refusal vocabulary is a frozen partition, and this is a sentinel doing
  a job outside its class's stated meaning with no case that exercises it — the shape GAP-61 was
  raised for, one class over. It is `medium` because the consequence is a misleading refusal rather
  than data loss.
- **Why this timing:** Class membership and the declared wrap list are what a transport and
  `INV-024`'s mandatory table test are written against, and both are frozen with the contract.
- **Close criteria:**
  - [ ] The document says what happens when a fold panics — recovered into a named refusal, or not
        recovered — and argues it beside §UC-005's argument for the other programmer-error panic.
  - [ ] If it is recovered, it names a sentinel whose class meaning fits, states what that sentinel
        wraps (a recovered panic is not an application error), and §2.3's wrap lists are updated.
  - [ ] A use case covers it with a control, and the `refusal classes` or a fold-facing section
        asserts it.
- **Status:** **closed by deleting the mechanism rather than finding it a better class.** A fold's
  panic is **not recovered** (§UC-067, §INV-017). The argument is §UC-005's, made explicitly beside
  it: a fold has no error channel because it cannot fail on *data*, so a fold that panics is a
  programmer error, it is deterministic — the same stream panics on every load in every process — and
  recovering it converts that into a per-request refusal a retry loop will hammer. An upcaster is
  different and §UC-016 now says why in one bullet: it is the one callback whose subject is data, so
  its refusal is a value, and `jobs.invokeUpcaster` is the precedent for recovering exactly that one.
  All three misfits the finding names go with the mechanism: no history-class sentinel for a
  healthy history, no declared wrap over a value that is not an error, no `ErrUpcast` from a load
  with no upcaster in it. §INV-017's "either" is corrected and its falsification asserts the panic
  reaches the caller as a panic; §UC-067 carries the case with the non-panicking fold and the
  panicking upcaster as its two controls; §UC-016's Refusal line says a recovered upcaster panic
  wraps **nothing** and does not carry the panic value, on §INV-025's grounds and
  `jobs.HandlerFailure`'s line. The assertion is in `event`'s own tests, not a conformance section,
  because it does not vary with the store (§D.9). See `## Resolution — the store's own voice`.

### GAP-73 [medium][immediate] `Within`'s marker is compared as "the innermost marker", which refuses a correct program the moment two stores' `Within` contexts are chained

- **Where:** §UC-028 lines 1540-1546 ("a kernel-minted **marker** for that authority, **chained on
  any marker already there**. Every later `Load` and `Append` on that context asks the same question
  and compares the answer with **the marker**"); §UC-049 lines 1639-1643 ("The context's **innermost**
  marker names T1. They are not `Same`"); §D.6 lines 3979-3981; §UC-007 (two stores in one process);
  §INV-025 (the refusal names nothing about either transaction).
- **What:** The marker chain has no stated key. §UC-049 resolves the comparison against "the
  innermost marker", and that is the only lookup rule the document gives. An operation that calls
  `Within` on a repository over store A and then on a repository over store B — two backings, two
  transactions, one context, which §UC-007 and §UC-055 both make ordinary — leaves B's marker
  innermost. A's next `Load` or `Append` then compares A's own authority against B's marker;
  `Authority.Same` begins with `Backing.Equal`, the backings differ, so the answer is false and the
  operation is refused with `ErrTransactionMismatch` — on a correct program, with a message that by
  §INV-025 names neither transaction and nothing about either, which is undiagnosable. The obvious
  correct rule is "the innermost marker **whose backing is this store's**", and the document never
  states it.
- **Why this severity:** A wiring refusal fired on a correct composition, in the one mechanism a
  caller opts into specifically to be told early that its atomicity claim is sound. It is `medium`
  because the correct rule is one clause and nothing else changes.
- **Why this timing:** The marker is the only thing §UC-049 exists for, the mismatch refusal is in
  the frozen wiring class, and the `transactions` section drives it.
- **Close criteria:**
  - [ ] §UC-028 and §UC-049 state the lookup rule: which marker in the chain a given store's
        operation compares against, and by what key.
  - [ ] A case covers two `Within` contexts chained for two different backings and asserts both
        stores' operations proceed.
  - [ ] The `transactions` section's mismatch case is written so that it cannot pass against an
        implementation that always compares the innermost marker.
- **Status:** **closed by stating the lookup rule, and it is one clause.** §UC-028 gains it: a marker
  carries an authority, an authority carries its backing, and an operation compares against **the
  innermost marker whose backing is its own store's**. A context with no marker for this store's
  backing is unmarked *for this store*, and §INV-041's three rows apply to it unchanged — so the rule
  needs no new state and no new refusal, only a key on a chain that already exists. §UC-049's Flow
  is reworded to "the innermost marker **for this store's backing**". The case the finding names is
  now a **control** on §UC-028 and a row of §D.9's `transactions` section: two `Within` contexts
  chained for two backings, both stores' operations asserted to proceed — which an
  always-innermost implementation fails, so the mismatch case cannot pass against it. See
  `## Resolution — the store's own voice`.

### GAP-74 [medium][immediate] §INV-038's copy-and-share table omits the one mutable handle a consumer holds, the two values every request goroutine reads, and the envelope

- **Where:** §INV-038 lines 3237-3261 (the per-type table, whose stated reason for existing is that
  "one answer for all of them would be wrong for some"); §D.7 lines 4016-4018 (`*Reader`, with
  `Next`, `Events` and `Cursor`); §D.1 lines 3586-3599 and §D.2 lines 3655-3657 (`*Aggregate` and
  `*Fact`, which §D.0's idiom makes package-level values); §INV-021's outbound clause line 2900
  ("reachable from nothing the framework retains, **because the framework retains nothing**").
- **What:** The table answers `At`, `Change`, `Commit`, `Authority`, `Backing`, `Cursor`, `S`,
  `*Repo`, `*Binding` and `Store`, and does not answer `*Reader`, `*Aggregate`, `*Fact` or
  `Envelope`. `*Reader` is the only stateful handle in the caller-facing surface — `Next` advances a
  cursor and `Events` serves the page it fetched — and it is what actor A4 holds for its whole
  working life; a projector that fans a page out to workers, or two goroutines draining one reader,
  has no answer. `*Aggregate` and `*Fact` are read concurrently by every request goroutine in the
  process, which is safe *because* of the seal (§UC-005) — a fact worth one row, since the whole
  table exists to say which values that is true of. And `*Reader` also falsifies §INV-021's stated
  reason: it **does** retain a page of envelopes across the `Next` call boundary, so "the framework
  retains nothing" is not the argument that makes the reader's payloads safe (which is GAP-68's
  subject).
- **Why this severity:** A concurrency contract with holes on the two values most likely to be
  shared, in a document that otherwise closes concurrency questions carefully. `medium` because the
  answers are almost certainly "single goroutine" and "safe, because sealed", and writing them down
  is the whole fix.
- **Why this timing:** It is a frozen public contract, and a `-race`-clean test suite proves nothing
  about a rule nobody wrote.
- **Close criteria:**
  - [ ] §INV-038's table gains rows for `*Reader`, `*Aggregate`, `*Fact` and `Envelope`, each with
        its copying and its concurrent-use answer.
  - [ ] §INV-021's "because the framework retains nothing" is narrowed to the values it is true of,
        with the reader's page named as what it retains and for how long.
  - [ ] The `concurrency` conformance section covers whichever of the new rows is a store's or a
        kernel's obligation rather than a caller's.
- **Status:** **closed** — §INV-038's table gains four rows. `*Aggregate` and `*Fact`: handles, safe
  for many goroutines, **and this is why the seal exists** — the tables are read-only from first
  observation, which is what makes every request goroutine reading them safe (§UC-005). `*Reader`:
  the one stateful value in the caller-facing surface, one goroutine at a time, with the page it
  returned fannable to workers while `Next` is not called. `Envelope`: free to copy, safe to read,
  and its payload is the caller's to mutate because of GAP-68's clause (3), so two goroutines sharing
  one and one of them writing is the caller's own race like any slice. §INV-021's outbound clause is
  narrowed as the finding asks: "because the framework retains nothing" is true of everything the
  *kernel* hands out, and the reader's page — retained from one `Next` to the next — is safe for
  clause (3)'s reason instead. §D.9's `concurrency` row now has the goroutines read one `*Aggregate`
  and its `*Fact`s throughout, which is the only one of the new rows that is not a caller's own
  affair. See `## Resolution — the store's own voice`.

### GAP-75 [medium][immediate] §D.15's import list, rewritten by GAP-58's fix, omits the package two of §2.3's four declared wraps require

- **Where:** §D.15 lines 4764-4773 ("**`event` imports** the standard library and `crud`, and `crud`
  for exactly two things"); §2.3 line 427 (`ErrTooLarge` → "the framework's too-large class
  (`errs.KindTooLarge`)"); Q1 line 4833 ("an `*errs.Fault` with `errs.KindTooLarge`, because the
  tree has no too-large sentinel"); §UC-017's Refusal line 1167.
- **What:** GAP-58's fix restated the root package's import list as "the standard library and
  `crud`" and enumerated the two `crud` symbols. But `ErrTooLarge` is required to carry
  `errs.KindTooLarge`, which is `errs/code.go:58` and reachable only by importing `errs` — Q1 says
  so in those words, and `crud.ErrConflict` and `crud.ErrUnavailable` cover only the other two
  wraps. So the one paragraph that states the kernel's import graph as a contract is wrong, in the
  round whose finding was that the import graph was stated in two ways that could not both hold.
  §INV-025's opaque-rendering mechanism cites `cache/errors.go` as a shape rather than an import, so
  that one is fine.
- **Why this severity:** It is the same defect GAP-58 raised, reintroduced at half scale by GAP-58's
  own fix, in the paragraph Q11's structural checks and `make check-deps` read. `medium` because
  `errs` is first-party and costs no `require` line, so the fix is one clause and no argument.
- **Why this timing:** The import list is stated as a contract and Q4 and Q11 both settle against
  it; a reconciliation phase reading it will measure the wrong graph.
- **Close criteria:**
  - [ ] §D.15's import paragraph names every package `event` imports and what each is for, including
        `errs`.
  - [ ] Q4's answer, which enumerates what `event` names, is consistent with it.
- **Status:** **closed, and replaced by a table so it cannot drift again.** §D.15's import paragraph
  becomes a three-row enumeration: the standard library (named package by package, including
  `reflect` for the nil-identity predicate and `testing` in `eventtest` only); `crud` for exactly
  three symbols — `SameDataSource`, `ErrConflict`, `ErrUnavailable` — and explicitly not the executor
  vocabulary; and **`errs`**, for `errs.KindTooLarge`, which is the third of §2.3's four class wraps
  and lives nowhere else. The fourth class wrap needs no import, because an application's error
  arrives as a value, and §INV-025's `cache/errors.go` citation is a shape and not an import, both of
  which are now said. Q1 and Q4 are rewritten to match. The finding's own observation — that this is
  GAP-58's defect reintroduced at half scale by GAP-58's fix — is recorded in the table. See
  `## Resolution — the store's own voice`.

### GAP-76 [low][immediate] Five statements the round-5 fixes did not reach, one of them introduced by a round-5 fix

- **Where:** §INV-021 line 2906 ("**The three clauses** that make the byte half true") followed by
  four numbered clauses, the fourth added by GAP-55's fix; §D.14 line 4555
  (`Unstated Support = iota   // the zero value; Bind refuses it`, when §INV-043 and §D.9 both say
  `Bind` **and** `Read`); §UC-036 line 1919 ("the same **four** checks `Bind` does") against line 145,
  line 1951 and §D.4 line 3802, which all say **three** store-honesty checks; §2.1 line 298 ("the
  mapper … runs at three moments: a load, an **append**, and `Fact.New`") against §D.5 line 3856,
  where `Append(ctx, at, changes...)` has no identity parameter and therefore cannot run the mapper;
  §7 line 4831 (the table header "Status after round 4", when six of its rows were rewritten by
  round 5).
- **What:** Five small inconsistencies, each in a place a later reader will treat as normative: a
  count that no longer matches its own list, the frozen store contract's own comment naming one of
  the two doors, a four-versus-three disagreement about the same check list, a claim that the mapper
  runs at a call that has nothing to run it on, and a stale column header over a table round 5
  edited.
- **Why this severity:** Cosmetic in effect, individually. Raised because round 1's GAP-16 and round
  5's GAP-65 both established that this document's small drifts are how a fix silently fails to
  land, and because two of the five (§D.14's comment, §2.1's three moments) are in the two sections
  an implementer reads as contract.
- **Why this timing:** Each is one clause, and the §D.14 comment is what a store implementer will
  obey.
- **Close criteria:**
  - [ ] §INV-021 says "four clauses", or the fourth is folded into another.
  - [ ] §D.14's `Unstated` comment names `Bind` and `Read`.
  - [ ] §UC-036, §UC-031, §D.4 and §D.7 use one count and one enumeration for the store-honesty
        checks.
  - [ ] §2.1's mapper row names the moments the mapper actually runs, and says separately where the
        store's `MaxKey` is applied.
  - [ ] §7's header names the round its statuses are current as of.
- **Status:** **closed, all five.** (1) §INV-021 says "The **four** clauses". (2) §D.14's `Unstated`
  comment reads "Bind AND Read refuse it (INV-043)". (3) The store-honesty checks are **three**
  everywhere — §UC-036, §UC-031, §UC-055, §INV-022, §D.4 and §D.7 — with the enumeration written the
  same way in each and `Bind`'s family collision named as the fourth check that is not one of them.
  (4) §2.1's mapper row names the moments the mapper actually runs — `Repo.Load`, `Fact.New` and
  `Aggregate.Fold` — says `Repo.Append` names no identity and therefore renders nothing, and states
  separately that the store's `MaxKey` is applied at `Load` and `Append` against the key the token or
  the change already holds. (5) §7's header reads "Status after round 6". See
  `## Resolution — the store's own voice`.

### GAP-77 [medium][deferred] `architecture.md`'s exported-symbol threshold is breached by an order of magnitude and §D.16's metric table omits the row

- **Where:** §D.16 lines 4814-4821 (the `architecture.md` metric table, which answers kernel
  imports, kernel diffs, type switches, public methods per type and the fake count); `event`'s
  exported surface as the document defines it — `Define`, `Compose`, `Declare`, `From`, `Then`,
  `JSON`, `Open`, `Bind`, `ReadOnly`, `Read`, `NewBacking`, `NewAuthority`, twenty-two sentinels,
  and roughly fifteen exported types.
- **What:** `architecture.md` sets "public symbols exported by a module ≤ 7" and states that a
  breach "must be justified in writing", and §D.16's table — the document's own answer to that
  file — does not carry the row. The breach is large and is probably correct (a vocabulary package
  for a subsystem is not a class), but nobody has written the justification, and §D.16 answering
  five of the six metrics reads as if all six were checked.
- **Why this severity:** A measurable threshold breached without the written justification the law
  requires. `medium` because the design is very unlikely to change as a result.
- **Why this timing:** Deferred: the justification belongs in the plan, which is where
  `architecture.md` says a breach is justified, and no contract depends on the answer.
- **Close criteria:**
  - [ ] §D.16 (or the plan) carries the exported-symbol count and the argument for it, distinguishing
        the vocabulary a subsystem must export from the surface a class exposes.
  - [ ] The same row is stated for `eventmemory` and `eventtest`.
- **Status:** **closed rather than carried**, because it costs a paragraph and no design. §D.16's
  metric table gains the row — `event` ≈ 71 (13 functions, 27 types, 9 constants, 22 sentinels),
  `eventmemory` 8, `eventtest` 6 — followed by the written justification `architecture.md` requires:
  the ≤ 7 threshold is a metric over a **class**, where each extra method is another way for one
  object to be inconsistent, and `event` is the vocabulary of a subsystem. Each of the four groups is
  argued separately: the 22 sentinels are a partition a caller matches on and collapsing them puts a
  string where `errors.Is` is; the 27 types are the two seams, and §INV-019 *requires* every
  store-seam type to be constructible from another package, so a smaller count here is a smaller
  extension point; the 13 functions each have a named reader and none is a "for later" API. The
  measurement that is not breached — no exported type over 8 methods, and the one at 8 is a port with
  two consumable halves — is the row above it. See `## Resolution — the store's own voice`.

### GAP-78 [low][deferred] "A nil identity matches nothing" is true of `crud.SameDataSource` for an untyped nil and false for a typed one, and two invalid backings can therefore compare equal

- **Where:** §INV-016 lines 2803-2813 ("That function already does every part of what this invariant
  needs: **a nil identity matches nothing** … Two **invalid** backings therefore compare
  **unequal**"); §D.14 lines 4568-4580 (`NewBacking(identity any)`, `Backing.Valid`, and
  `NewAuthority`'s "a nil or non-comparable one is refused, which is `crud.SameDataSource(t, t)`").
- **What:** `crud.SameDataSource` (`crud/executor.go:562-571`) returns false when either argument is
  a nil **interface** and otherwise compares dynamic type then value. A typed nil pointer — the
  shape a store reaches by handing `NewBacking` or `NewAuthority` a `*sql.DB` or `*sql.Tx` it failed
  to set — is not a nil interface: `SameDataSource(t, t)` answers **true**, so such a backing is
  `Valid`, such an authority is accepted, and two stores that both forgot compare `Equal` and
  `Same`. The document's zero `event.Backing{}` is genuinely invalid, so the common case is right;
  what is inaccurate is the attributed rule, and §INV-016's third control ("two invalid backings must
  be unequal") passes without exercising it.
- **Why this severity:** Reaching it needs a store bug, and the conformance suite's own controls
  would not distinguish it. Recorded because the document states the rule as a property of a
  framework function rather than as its own, and the property is narrower than stated.
- **Why this timing:** Deferred: nothing in phase 1 depends on the difference, and the fix is a
  clause in `NewBacking`/`NewAuthority`'s stated refusal.
- **Close criteria:**
  - [ ] §INV-016 states the rule as `crud.SameDataSource` actually holds it, distinguishing a nil
        interface from a typed nil.
  - [ ] `NewBacking` and `NewAuthority` say whether they refuse a typed nil, and §INV-028's
        falsification covers it.
- **Status:** **closed rather than carried**, because the fix is a constructor check and a corrected
  attribution. §INV-016 now states what `crud.SameDataSource` actually holds — a **nil interface**
  matches nothing — and states the rest as *this package's own*: a typed nil is not a nil interface,
  so `SameDataSource(t, t)` answers true for one, and **`NewBacking` and `NewAuthority` refuse an
  identity that is nil by any route** — a nil interface, or a nil pointer, map, slice, channel or
  func inside one — which is `crud`'s own `isNilValue` predicate applied at a constructor that runs
  once per store. That is the second rung of the ladder (refused at construction, before any work)
  rather than a rule, and it costs nothing on any request path. `NewBacking` therefore returns
  `(Backing, error)`; §D.14's declaration and comments, §D.15's `eventpg.New` and its per-value table
  row all move with it, and §INV-016 gains a fifth control and §INV-028 a typed-nil case, each with
  a non-nil identity of the same type accepted in the same test. See
  `## Resolution — the store's own voice`.

---

**Checked this round and found sound** (so the absence of a finding is deliberate): the UC and INV
enumeration is complete and unduplicated across three sittings, with both withdrawals in place and
forwarding correctly; no section, sketch, refusal row or invariant names a removed mechanism as
live; every §D sketch compiles against the current signatures, including the two-decision fragment
GAP-65 fixed and §D.15's `NewAuthority` return; §UC-064 and §INV-044 are a genuine shape change and
the `New(id, payload)`-over-`New(at, payload)` argument is correct on both grounds it gives;
§UC-065's fold-mutate-fold-again sequence really is the third aliasing direction and really was the
open one; §INV-028's "the authority *is* the transaction, held by reference" is sound, including the
pointer-reuse argument (a live authority keeps its `*sql.Tx` reachable) and the retention cost
(`database/sql` nils out `dc` and `txi` on close, so a retained committed `*sql.Tx` holds no
connection), and `crudsql`'s savepoint genuinely returns the parent's `*sql.Tx`, so the savepoint
rule is structural as claimed; the microkernel walk over `eventpg`'s eight methods finds every
**data** type constructible from another package — `Stream`, `Key`, `Version`, `Position`, `Cursor`,
`Envelope`, `Record`, `AppendRequest`, `Limits`, `Capabilities`, `Support`, `Backing`, `Authority`,
and the invalid `event.Authority{}` composite literal — which closes GAP-57 properly, the error
being the one remaining value (GAP-67); §UC-066 and §INV-041 are the honest answer to a case no
mechanism can see, and having the `transactions` section assert the unsafe outcome on purpose is the
right way to stop silence becoming a belief; §UC-059's rejection of a package-level registry is
correct and the four-place statement of the scope is the right compensation; §INV-013's
mutation-not-kind predicate is now satisfiable by the document's own required API; §2.3's two-edge
intra-class wrap list makes §INV-024's negative half computable; the store-honesty checks at both
doors close GAP-60 including the control that the old one passed against; every framework symbol
named exists with the shape attributed to it (one narrow exception, GAP-78); the second-instance
tests all pass — `struct{ PlacedAt time.Time; Total int64 }`, a map-typed state, a payload with a
slice, a composite non-comparable identity, zero events, 100 000 events, a three-revision chain and
a generated declaration each cost the same declaration and no extra argument; no §D line, invariant
or use case encodes one example aggregate as a mechanism (`Applied map[string]bool` is shown as the
application's own answer and costs no framework feature); and §D.13 prices the design from stated
arithmetic rather than asserting it.

---

## Resolution — the store's own voice - 2026-09-06

Round 6 left twelve findings open: one `[critical]`, three `[high]`, five
`[medium][immediate]`, one `[low][immediate]` and two deferred. Round 6's own
audit recorded that **two of them were introduced by round 5's fixes** — the ratio
this file has tracked since round 1 — so the first question asked of each was
whether a change of shape makes it unaskable, and only then whether a rule closes
it.

**The count. Twelve closed, none deferred, none dissolved, one premise rejected on
the record.** Nothing new is carried into `## Debt`, and the two `[deferred]`
findings are closed here rather than carried, because both cost a paragraph and no
design.

| # | Severity | Disposition | What did it |
|---|---|---|---|
| GAP-67 | `[critical]` | **closed** | `Outcome` and `Failure(outcome, cause)` in §D.14, with a total map to §2.3 and a door-dependent fail-safe default. It is `jobs.RejectPlacement` + `normalizeSenderError`'s shape rather than a second answer: an extension names a classification the kernel owns, everything else normalises to the safe one. `eventtest.FailKind` is deleted; §INV-045 is the new invariant |
| GAP-68 | `[high]` | **closed by moving the obligation** | §INV-021 clause (3) becomes *a store hands out bytes it will never read again* — clone what you retain, not merely freeze. One rule on the party that knows closes both the `Reader.Events()` path and the aliasing-codec path, and it is free for the store that would have paid |
| GAP-69 | `[high]` | **closed by separating two shapes** | `Close` neither commits nor rolls back; a store that owns its staging discards it, one that stages in the caller's transaction holds none. The `lifecycle` section asserts the half true of both — invisibility — with the commit-before-close control |
| GAP-70 | `[high]` | **closed by a shape change** | `Aggregate.Fold(id, state, changes...)`: the identity at the fold, exactly as round 5 put it at `Fact.New`, refusing any change decided for another instance. The residual that remains — a *state* carries no stream — is §D.10's new fourth item |
| GAP-71 | `[medium]` | **closed, no hook added** | §UC-049's assertion is corrected to §INV-028's (two live transactions are **not** `Same`), and the savepoint case moves to where it is a claim: `NewAuthority` twice over one identity, in `event`'s tests, plus `eventpg`'s own test in phase 2 (§9 item 8) |
| GAP-72 | `[medium]` | **closed by deleting the mechanism** | A fold's panic is not recovered (§UC-067). All three misfits go with it; the upcaster stays recovered and §UC-016 says why in one bullet |
| GAP-73 | `[medium]` | **closed by one clause** | A marker is selected by **backing**: the innermost one whose backing is this store's. Two `Within` contexts for two backings now compose, and that case is a control and a `transactions` row |
| GAP-74 | `[medium]` | **closed** | §INV-038 gains `*Reader`, `*Aggregate`, `*Fact` and `Envelope`; §INV-021's "retains nothing" is narrowed to the kernel and the reader's page is named |
| GAP-75 | `[medium]` | **closed, and replaced by a table** | §D.15's import paragraph becomes a three-row enumeration naming `errs`, and Q1 and Q4 are rewritten to match it |
| GAP-76 | `[low]` | **closed, all five** | Four clauses, `Bind` **and** `Read` in the `Unstated` comment, three store-honesty checks everywhere, the mapper's real three moments, and §7's header |
| GAP-77 | `[medium][deferred]` | **closed rather than carried** | §D.16 carries the exported-symbol count for all three packages and the written justification `architecture.md` requires, argued per group |
| GAP-78 | `[low][deferred]` | **closed rather than carried** | §INV-016 states the rule as `crud.SameDataSource` actually holds it; `NewBacking` (now error-returning) and `NewAuthority` refuse an identity that is nil by any route |

**The premise rejected, and it is GAP-71's first candidate fix.** The finding
offers, as one of two reconciliations, that the `Factory` "gains whatever is needed
to open a savepoint of a live transaction, with its requiredness derivable from the
capability report the way `Begin`'s and `Sibling`'s are". There is no capability it
could be derived from, and inventing one — `Savepoints Support` — would be a
capability that exists to make a test hook required, which is the tail wagging the
contract; a savepoint is `crudsql` vocabulary, the memory store has none, and
`eventmemory` would answer `Unsupported` forever. GAP-49 raised the *symmetric*
defect (a hook whose requiredness was derivable from nothing) and the answer there
was a capability the data genuinely has. Here the honest answer is the finding's
own second option: the property is about the authority's derivation rather than
about a store, so it is asserted where it is a claim.

**Two things round 6 changed that no finding asked for, listed so a later reader
does not read them as drift:**

- **§2.3 separates a *class wrap* from a *cause*.** The four-wrap list was closed
  at four while `ErrBackend`'s row held "the store's own error" inside it and
  §UC-046's `ErrAmbientNotTransaction` carried one with no row at all — two things
  called one word, which GAP-67's channel would have multiplied by five. A class
  wrap reaches a class a transport already maps and stays closed at four; a cause
  is the error a refusal was produced from, reachable with `errors.Is` and rendered
  opaquely. One obligation comes with it and is checkable: a store's own error must
  not itself be a sentinel of this vocabulary, or the partition would be false
  through a back door. §INV-024's statement and falsification and §D.9's
  `refusal classes` row carry it.
- **`NewBacking` returns `(Backing, error)`.** GAP-78's fix needs a refusal
  somewhere, and the constructor is the only place that runs once per store rather
  than once per operation. It brings §D.15's `eventpg.New` into line with the
  error-returning-constructor rule §D.4 already states.

**What is still owed, and it is unchanged.** §8 carries GAP-25, GAP-41 and
GAP-53 — all from rounds 1–3, none touched by round 6, none load-bearing for
anything else. §9 now carries nine things a later phase does, two of them added
here: the savepoint half of §INV-028 in `eventpg`'s package test, and an
exported-surface baseline for the three packages, which is [ES]'s tenth capability
obligation and the one round 6's audit called the weakest — the document has method
inventories and no baseline that fails when the seam grows, and `make api` is what
that is for elsewhere in the tree.

**The direction the next reduction should look**, unchanged from round 5 and
narrower now: the refusal vocabulary is still policed by a table rather than by a
mechanism. Round 5 made its ground truth computable (the intra-class wrap list);
round 6 made the store's half of it a **mechanism** — a store can no longer spell a
classification wrong, only lie about which one is true. What is left is the
kernel's own side: twenty-two sentinels whose class membership is asserted by a
table test rather than carried by a type.

---

## Round 7 - econv-usecase-validator (coverage + invariant + DX roles, post-store's-own-voice re-audit) - 2026-09-06

Sources read: `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (whole file, 5690 lines),
`.agents/artifacts/gaps/EVENTSOURCE_P1_USECASES_GAPS.md` (every round and all three resolutions),
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` (the non-negotiable list, the ten
capability/wrapper obligations, the transaction and atomicity rules), `CLAUDE.md`,
`~/.claude/skills/econv/SKILL.md` and `references/{gaps,universality,microkernel,architecture,
data-integrity}.md`.

**Framework symbols the spec names, checked against the tree and kept separate from the spec's own
quality.** `crud.SameDataSource` is `crud/executor.go:562-571` and behaves exactly as §INV-016 now
states it — a nil *interface* matches nothing, dynamic types must match, non-comparable matches
nothing — so round 6's typed-nil correction is accurate rather than merely plausible.
`crud.isNilValue` is `crud/executor.go:507-518` and covers chan/func/interface/map/pointer/slice,
which is the predicate §INV-016 describes — but it is **unexported** (GAP-85). `crud.KeyOf`
(`:524-529`) returns the identity or the value itself, which is what §D.15's
`NewBacking(crud.KeyOf(spec.Source))` needs. `jobs.RejectPlacement` (`jobs/queue.go:799-806`) does
have the shape §D.14 cites for `Failure`: an extension names a member of a closed exported set and
anything else normalises to one safe answer, which is the fail-safe default's precedent.
`normalizeSenderError` (`:808`) exists. **No symbol the document names this round is missing or
misattributed**, with the one scope inaccuracy in GAP-85.

**Enumeration.** UC-001…UC-067 each appear exactly once (UC-023 `WITHDRAWN` in place, forwarding to
§UC-022 + §UC-034); INV-001…INV-045 each appear exactly once (INV-037 `WITHDRAWN` in place,
forwarding to §INV-040). No number is cited without being written and none is written twice. §2.3's
"twenty-two sentinels" is still arithmetically correct (3+7+3+4+2+3) after `ErrWrongStream`, and no
sentinel was added this round. `Outcome` is six values and §D.14's map has a row for each plus the
two door rows.

**Regression sweep over the round-6 fixes.** Every occurrence of `eventtest.FailKind`,
`FailNotWritten`, `FailUnconfirmed`, the store-side *freeze* obligation, `Close` "discards", the
two-argument `Fold(state, changes...)`, "the innermost marker" without a key, and the recovered
panicking fold sits in a *deleted* or *rejected* context; **no section describes a superseded shape
as live**. All eleven live `Fold(` call sites carry the identity. `Bind` **and** `Read` appear
together in §D.14's `Unstated` comment, the store-honesty checks are **three** in all six places
that state them, §INV-021 says "four clauses", §7's header reads round 6, and §UC-021's snippet
still compiles. Five smaller things the round-6 fixes did not reach or introduced are GAP-81,
GAP-82, GAP-85 and GAP-86; one is round 6's own fix reaching half its scope (GAP-79).

**Round 6's twelve findings re-checked against the new text, not the disposition note.** GAP-67 is
**closed by a change of shape** and is the round's strongest fix: `Outcome`, `Failure`, a total map,
a door-dependent default, `FailKind` deleted, §D.15's `classify` written out — the object the
finding said did not exist is now declared at `Backing`'s fidelity, and §INV-019's compile-time
proof requires the third-package fixture to return each outcome. GAP-70 and GAP-72 are also
**closed by a change of shape** (the identity at `Fold`; the recovery mechanism deleted outright).
GAP-69 is **closed by a shape distinction** — two store shapes named, and only the property true of
both asserted. GAP-68 is **closed by moving an obligation to the party that knows**, which is the
strongest form a rule can take — but it is the *store's* retained bytes it moves, and the kernel's
identically retained bytes (a `Change`'s frozen payload) are left with no owner, which is GAP-79.
GAP-71, GAP-73, GAP-74, GAP-75, GAP-77 and GAP-78 are **closed by added rules or added text**, the
weaker form: each is correct and each carries a control, but each is a sentence an implementer must
obey rather than a shape that makes the error unaskable, and two of them left a residual in a
neighbouring section (GAP-85 from GAP-78's fix, GAP-86 from GAP-77's). GAP-76 is **closed, all five**
— and its fourth item is what produced GAP-82.

Checks run: every actor (A1–A7) and entry mode (E1–E6) against a `[happy]` use case, map re-verified
row by row; all thirteen [ES] non-negotiables against a testable INV (all present; item 9 still
lands in §5.2's INV-036 while §5.1's preamble says INV-001…INV-014 restate the list, which is the
same gloss round 6 noted and is not raised again); [ES]'s ten capability/wrapper obligations
(1–7 land squarely, 8 and 9 are conditioned on wrappers phase 1 does not build, 10 is now §9 item 9
as debt); the edge matrix (empty, malformed, oversized, ambiguous, duplicated, multilingual,
partially-failing dependency, timeout, zero results, conflicting signals — all present); the three
`universality.md` checks with the second-instance test run on `struct{ PlacedAt time.Time; Total
int64 }`, a state that is itself a map, a payload carrying a slice, a composite non-comparable
identity, zero events, 100 000 events, a three-revision chain and a generated declaration — the last
two of which produced GAP-80; the aliasing sweep in all three directions, which produced GAP-79; a
silent-misuse sweep including every call that pairs two instance-scoped values; the microkernel
walk with `eventpg`'s eight methods constructed by hand, value by value; `architecture.md`'s metric
table; and a UC-vs-UC, UC-vs-INV and INV-vs-§D contradiction sweep.

**The microkernel walk passes.** Hand-constructing `eventpg`'s eight methods from §D.14 as now
written: `Capabilities()` needs `Capabilities` + `Support` (plain struct, exported constants);
`Limits()` needs `Limits` (plain struct); `Backing()` needs `Backing` (`NewBacking`, exported,
now `(Backing, error)`); `ReadAll` needs `[]Envelope` + `Cursor` (plain struct; defined string,
minted by conversion); `ReadStream` needs `[]Envelope` and consumes `Stream`/`Version` (plain
struct of two exported fields; defined `uint64`); `Transaction` needs `Authority` (`NewAuthority`,
exported) **and the invalid one**, which is `event.Authority{}` — a composite literal of an
all-unexported-field struct, legal Go from any package; `Append` consumes `AppendRequest`/`Record`
and returns `error`, which is `event.Failure(event.NotWritten, cause)` over an exported closed enum;
`Close` returns a plain error. **There is no type it cannot construct.** GAP-67's closure is what
made that true — the error was the last value on the seam without a spelling — and §INV-019's
compile-time half (a third-package fixture returning a classified failure of every outcome) is the
right way to hold it. The zero-diff claim is sound as written.

**Two instances of one aggregate can no longer be crossed at any call the framework mints both sides
of.** `Append` compares change-stream against token-stream; `Fold` compares change-stream against
the mapper's rendering of the identity it was given. The one surviving crossing — a *state* handed
to another instance's fold or decision — is named in §D.10 item 4 and §UC-064's residual 2, with the
correct argument for why no proxy closes it. That part of the design is finished.

**The worst thing a caller can still write that compiles, runs, returns no error and is wrong** is
no longer a pairing error: it is a fold that publishes a reference kind a codec aliased out of a
`Change`'s frozen bytes, followed by an ordinary in-place mutation of the caller's own state
(§UC-052 blesses it). That is GAP-79, and the document asserts the opposite in four places.

### GAP-79 [high][immediate] Clause (3)'s rule was given to the store and not to the kernel: a `Change`'s frozen bytes are handed to a codec that may alias them, so a blessed fold plus a blessed mutation rewrites a fact that was already decided

- **Where:** §INV-021 clause (4) lines 3293-3295 ("A `Change`'s frozen bytes are read by every fold
  and **written by nothing**"); §INV-021's "Why clause (3) says *clone what you retain*" lines
  3296-3308, which is where the document establishes that "a codec that decodes a `[]byte` or string
  field by aliasing — the ordinary shape for a compact binary format, which `event.JSON` merely
  happens not to be" is a legitimate codec the design must tolerate; §D.3 lines 4260-4265 ("a fold
  written `s.Lines = e.Lines` aliases a value the framework **drops the instant it returns**, and
  folding one change twice gives two independent values"); §UC-065 lines 2647-2654 ("Two folds of one
  change must not be able to disagree" and "What the fold's `E` may alias … **Nothing the framework
  holds, at any depth**"); §UC-063 line 2542 ("Mutating a payload between `New` and `Append`
  **cannot** change what is recorded"); §INV-038's `Change[S]` row line 3689 ("two goroutines folding
  one change decode two independent values"); §D.10's rows lines 4858-4859; §D.9's `payload
  ownership` row line 4800, whose §UC-065 clause is driven with no aliasing codec while its §UC-061
  clause is; §UC-052 lines 787-791, which invites the caller to write into `s.Header.Tags` and
  `s.Items[0].Meta` in place.
- **What:** Round 6 correctly identified that the party which **retains** bytes owes "hand out
  memory you will never read again", and gave that obligation to the store for `Envelope.Payload`.
  The kernel retains bytes too, and nobody owes anything for them. Clause (1) says `Fact.New`
  **clones** the codec's output and freezes it, so a `Change` holds a framework-owned array for its
  whole life. §D.3 says every fold decodes those frozen bytes afresh. With an aliasing codec — the
  shape §INV-021 itself calls ordinary — the decoded `E`'s `[]byte` or string fields **point into
  that array**, and the document then blesses `s.Lines = e.Lines` (§D.3, §UC-065) and blesses the
  caller mutating `s.Lines[0]` in place (§UC-052, §UC-061). Three stated properties become false on
  the same sequence: (a) §UC-065's own Trigger — fold, mutate, fold the same change again — returns
  a value that no longer equals the reload, because the second decode reads the mutated array;
  (b) §INV-038's `Change[S]` row is wrong twice over, since the two folds are not independent and two
  goroutines folding one change while either publishes an aliased field is a data race under `-race`;
  (c) if the change list is appended after the fold — which §D.10 item 2 states is expressible, and
  which any caller that folds to inspect a projected state before committing writes naturally — the
  bytes that reach `Record.Payload` are the mutated ones, so **the fact recorded is not the fact
  decided**, which is the single claim §UC-063 and §D.3 call "unrepresentable rather than forbidden".
- **Why this severity:** It is silent, permanent corruption of an already-decided fact, reached
  entirely through steps the document blesses one by one, and it falsifies an invariant clause, a
  use case's Trigger, a concurrency row and two rows of the "cannot be written" inventory. It is
  `universality.md`'s second-instance test on the codec seam: correct for `event.JSON`, wrong for the
  compact binary codec the document names in the very paragraph that fixes the store's half. The
  conformance suite will not catch it — §D.9's `payload ownership` row drives the aliasing codec only
  through the *envelope* path (§UC-061) and drives §UC-065's fold-mutate-fold with an ordinary
  payload — so a green run is evidence of nothing here. It is `high` rather than `critical` because
  reaching it needs a non-JSON codec, which phase 1 ships none of; but the phase ships the `Codec`
  interface and invites applications to implement it.
- **Why this timing:** It is a clause of the frozen contract and it decides who pays a per-fold
  allocation, which §D.13 prices. The three candidate owners are not interchangeable — the kernel
  cloning the frozen bytes before each decode (symmetric with clause (3): the party that retains
  hands out memory it will never read again, and the cost falls only where a fold actually runs,
  which the ordinary load-decide-append path never does); a stated `Codec.Decode` obligation not to
  alias its input (which contradicts §INV-021's own recognition that aliasing decoders are ordinary
  and would make `Envelope.Payload`'s clause (3) unnecessary); or declaring a fold's `E` borrowed
  (which §UC-065 explicitly forbids). Deciding after `event` is written is deciding by accident, and
  the wrong choice is discovered by a consumer's binary codec in production.
- **Close criteria:**
  - [ ] §INV-021 states who owns the `Change`'s frozen bytes at the decode boundary, in the same
        words clause (3) uses for the store, and clause (4)'s "written by nothing" is either made
        true or replaced.
  - [ ] §D.3's "aliases a value the framework drops the instant it returns" is made true of an
        aliasing codec or scoped to a non-aliasing one.
  - [ ] §UC-065's control drives a codec that decodes by aliasing its input, exactly as §UC-061's
        now does, so the property is exercised rather than inherited from `event.JSON`.
  - [ ] §UC-063's "cannot change what is recorded" and §D.10's two rows say whether the
        fold-then-mutate-then-append sequence is covered, and by what.
  - [ ] §INV-038's `Change[S]` concurrent-use row is re-derived from whichever answer is taken, and
        §D.13 prices it.
- **Status:** closed — see `## Resolution — one rule, and one scope`

### GAP-80 [high][immediate] "Aggregate B's value in aggregate A's call is a compile error" holds only when two aggregates have two different Go state types, which the document's own examples make an ordinary neighbour input

- **Where:** §UC-002's Observed lines 665-667 ("it is a **compile error**, not a runtime check") and
  its Control lines 677-678 ("a test that constructs both and asserts the compile error via a
  build-failure fixture"); §INV-020 lines 3263-3271 ("A change, an at-token, a fact handle and a
  repository are all parameterised by the state type, so passing aggregate B's value to aggregate
  A's call is a **compile error**. **There is no runtime check for it, because there is no runtime it
  can reach**") and its falsification ("a build-failure fixture per crossing"); §D.10's row line 4847
  ("Aggregate B's change in aggregate A's append | A compile error (§INV-020)"); §UC-026 lines
  1527-1540; §D.2 lines 4189-4190 ("`Change[S]` stays parameterised by the state type alone");
  §UC-052 line 774 (`type Ledger map[string]int64` as a blessed aggregate state); non-goal 9 and
  §D.16's "generated rather than hand-written" case.
- **What:** Every caller-seam type is parameterised by `S` (and `ID`), never by the aggregate. Two
  aggregates whose state type coincides are therefore **the same Go types throughout**:
  `*Aggregate[Ledger, LedgerID]`, `Change[Ledger]`, `At[Ledger]`, `*Repo[Ledger, LedgerID]` and even
  `*Fact[Ledger, LedgerID, Credited]`. `repoA.Append(ctx, atFromB, changesFromB...)` then compiles,
  and so does `aggA.Fold(idOfA, stateOfB, changesOfB...)`. That is not an exotic input: §UC-052
  blesses a state type that is itself a map, a framework has no way to stop two bounded contexts
  reusing one value type, and a generated declaration set (§D.16 names generation as a case the
  design must survive) emits the same `Aggregate[Doc, DocID]` for several document kinds. §UC-002's
  precondition quietly assumes "a second declaration with **its own types**"; §INV-020 and §D.10
  state the claim with no scope at all.
- **Why this severity:** `universality.md`: an invariant written from the sample's shape (Account vs
  Order) and false on a structurally different instance of the same domain object, reported
  `[high][immediate]` at minimum, never deferred. The *behaviour* is safe — a `Change` carries
  (family, key) and §INV-044's comparison refuses the crossing with `ErrWrongStream`, which is the
  right class — so this is a false claim rather than a hole in the design; but it is false in the one
  invariant the document uses to argue that the whole class of cross-aggregate error is unreachable,
  and §INV-020's falsification ("a build-failure fixture per crossing") is a test that cannot be
  written for this case, so a suite proving §INV-020 proves it only for the shape it was written
  from. It also matters to an implementer: "there is no runtime check for it, because there is no
  runtime it can reach" tells whoever writes `Append` that the family half of the stream comparison
  is redundant for cross-aggregate values, when it is the only thing that catches them.
- **Why this timing:** §INV-020 and §UC-002 are what a reader believes about aggregate isolation, and
  §D.10's inventory is explicitly a contract with the reader that it "names what it does not"
  prevent. Scoping the claim costs two clauses now; discovering it when the build-failure fixture
  cannot be written costs an argument about whether the design changed.
- **Close criteria:**
  - [ ] §INV-020 states the scope of the compile-time half — distinct state types — and names what
        refuses the crossing when two aggregates share one state type, with the sentinel.
  - [ ] §UC-002's Observed and Control distinguish the two cases, and the second case gets a runtime
        assertion the way §UC-064's does.
  - [ ] §D.10's row is split the way its "Instance B's change" rows already are, so the inventory
        stays honest.
  - [ ] §UC-064's "Why this is a call-time refusal and not a compile error" — which currently rests
        on "two *aggregates* are two Go types" — states that this is true of the aggregates' *state
        types* rather than of aggregates.
- **Status:** closed — see `## Resolution — one rule, and one scope`

### GAP-81 [medium][immediate] `Fold`'s error contract is stated twice as "three causes and no fourth" while §UC-051 makes `Fold` a fourth: the identity it was given can render an illegal key

- **Where:** §UC-041's "Why it returns an error" lines 2449-2453 ("Three causes and no fourth: a
  `Change` carrying a deferred encoding or key failure from `Fact.New`, a decode that fails at fold
  time, and a change decided for a stream that is not this identity's") and its Refusal line
  2454-2455 (which names only `ErrWrongStream` and "the change's own carried refusal"); §D.8 lines
  4643-4652 ("three causes and no fourth"); §UC-051's Trigger line 1201 ("Any call that names that
  identity — `Repo.Load`, `Fact.New` or `Aggregate.Fold`") and its Flow lines 1205-1210 ("the
  kernel's rules — non-empty and the text rule — are checked wherever a key is produced, which is
  `Load`, `New` and `Fold`"); §2.1's mapper row line 391.
- **What:** Round 6 gave `Fold` the identity (GAP-70's fix) and updated §UC-051 and §2.1 to say the
  mapper now runs there, so `accounts.Fold(zeroID, state, changes...)` renders an empty key and must
  refuse with `ErrKey` — a **request**-class sentinel from a store-free call. §UC-041 and §D.8 were
  not updated: both still close the list at three, and neither list contains it. The three named
  causes do not cover it, because cause 1 is explicitly "a `Change` carrying a deferred … failure
  **from `Fact.New`**" and this failure is produced by `Fold`'s own argument. An implementer now has
  two answers for `accounts.Fold(zeroID, …)`: `ErrKey` (§UC-051) or `ErrWrongStream` (§UC-041's
  closed list, since an empty rendering matches no change's stream), and the two are in two different
  classes with two different transport answers.
- **Why this severity:** A closed-list claim ("no fourth") that is false, in the section an
  implementer reads for the fold's error contract, producing a class-crossing ambiguity in the
  frozen refusal vocabulary. `medium` because both candidate answers refuse and neither writes
  anything.
- **Why this timing:** `Fold`'s signature and its refusal set are public contract, §D.9 states that
  the `Fold` crossing is asserted in `event`'s own tests rather than in the suite, and that test has
  to assert one sentinel.
- **Close criteria:**
  - [ ] §UC-041 and §D.8 enumerate the same causes, and the count matches the list.
  - [ ] §UC-041's Refusal line names every sentinel `Fold` can produce, with its class.
  - [ ] §UC-051's Control or §UC-041's covers a `Fold` whose own identity renders an illegal key, and
        asserts which sentinel.
- **Status:** closed — see `## Resolution — one rule, and one scope`

### GAP-82 [medium][immediate] §INV-015's first and load-bearing defence requires `Append` to refuse an empty token key with `ErrKey`, and §UC-051's round-6 rewrite removes `Append` from the doors that apply the kernel's key rules

- **Where:** §INV-015's "three defences, in the order they actually hold" lines 3113-3121 ("(1) Its
  stream key is empty, which §UC-051 refuses with `ErrKey` before any statement … **The first is the
  one that fires.**") and its falsification lines 3137-3141 ("a test that appends a zero token and
  asserts `ErrKey`"); §UC-051's Flow lines 1205-1213, as rewritten by GAP-76's fix ("the **kernel's**
  rules — non-empty and the text rule — are checked wherever a key is produced, which is `Load`,
  `New` and `Fold` … the **store's** `Limits().MaxKey` is checked at `Load` and at `Append`"); §2.1's
  mapper row line 391 ("`Repo.Append` names no identity … so nothing renders there; the store's
  `MaxKey` is applied at `Load` and at `Append`").
- **What:** GAP-76's fourth item correctly removed the claim that the mapper *runs* at `Append`. In
  doing so it assigned `Append` exactly one key check — the store's `MaxKey` — and assigned the
  kernel's non-empty and text rules to the three calls that *produce* a key. A zero `At[S]{}` carries
  an empty key which nothing produced, so under §UC-051 as now written `Append` does not check it.
  §INV-015 — the invariant the document calls "the strongest claim in the phase" — depends on exactly
  that check being present, says it is the defence that actually fires, and hands the test suite
  "append a zero token and assert `ErrKey`", which now cannot pass. The claim survives on defence
  (2), the invalid backing, so a forged token is still refused; but with a different sentinel in a
  different class than the invariant states, and one of §INV-015's own falsifications is
  unsatisfiable.
- **Why this severity:** A stated falsification of the phase's strongest safety claim that a correct
  implementation fails, plus two sections disagreeing about which sentinel a forged token produces.
  `medium` because the token is refused either way and nothing is written.
- **Why this timing:** Both sentinels are in the frozen vocabulary and in different classes
  (`ErrKey` is request, `ErrWrongStore` is wiring), so a transport answers them differently; and the
  test is written against §INV-015.
- **Close criteria:**
  - [ ] §UC-051's Flow says whether `Append` applies the kernel's key rules to the key the token
        already carries, or only the store's `MaxKey`.
  - [ ] §INV-015's defence (1), its "the first is the one that fires" ordering and its falsification
        agree with whichever answer §UC-051 gives.
  - [ ] §2.1's mapper row carries the same split.
- **Status:** closed — see `## Resolution — one rule, and one scope`

### GAP-83 [medium][immediate] Two of the suite's five self-falsification defects are specified as the append-only-slice fixture "with one rule broken", and one of them must be transaction-capable — which that fixture is defined in two places as not being

- **Where:** §UC-045's defect table lines 2857-2858 (the two rows built as "**the suite's own
  defective store**, not a decorator", the second of which is "Leaves a rolled-back transaction's
  events readable", aimed at the `transactions` section) and its "Why two of the five are stores"
  lines 2860-2871 ("built instead as purpose-made stores inside `eventtest`'s own test package —
  **the same append-only-slice fixture** §UC-043's control and §INV-019's compile-time proof already
  require to exist, with one rule broken on purpose"); §UC-043's Control lines 2799-2804 ("a
  deliberately trivial store — an append-only slice **with no transactions**"); §INV-019's
  compile-time half lines 3248-3256 (the same words); §D.9's `transactions` row line 4806 (runs only
  when `Transactions` is claimed) and the `Factory` lines 4740-4745, whose `Begin` hook is supplied
  by the store under test's implementer.
- **What:** A store with no transactions cannot leave a rolled-back transaction's events readable,
  and the `transactions` section does not run against a store that does not claim `Transactions`. So
  the defect either is not the append-only-slice fixture (contradicting §UC-045's "the same
  fixture"), or the fixture has transactions (contradicting §UC-043 and §INV-019, which both name it
  as having none). The second half is unstated too: a defective store the *suite* owns needs a
  `Begin` the suite owns, and `Factory.Begin` is the store implementer's hook for the store under
  test. §UC-045's "A defect aimed at a capability the store does not claim (the transaction one) is
  reported **not certified**" is about the store under test and does not resolve either half.
- **Why this severity:** This is the mechanism whose entire purpose is that a green run is evidence,
  and one of its five defects is unbuildable as specified. GAP-49's shape exactly — a mandatory
  assertion expressed in terms of an affordance that does not exist — and the likely outcome is the
  same: the case is written as something else or quietly dropped, and the suite's self-check silently
  loses a fifth of its coverage.
- **Why this timing:** `Factory` is exported surface the document freezes, and §INV-019's
  compile-time proof is stated over the same fixture; whether that fixture is transaction-capable
  changes what §UC-043's control asserts and what §INV-019 proves.
- **Close criteria:**
  - [ ] §UC-045 says what each of the two defective stores is built from, and if either needs
        transactions, §UC-043's and §INV-019's description of the fixture is reconciled with it.
  - [ ] The document says where a transaction the *suite* owns comes from for a store the suite
        built, since `Factory.Begin` belongs to the store under test.
  - [ ] §UC-045's "at least one named section and no unrelated section" claim is stated for a defect
        whose section runs conditionally, so a skipped section is not a pass for the defect either.
- **Status:** closed — see `## Resolution — one rule, and one scope`

### GAP-84 [medium][immediate] The outcome map is door-scoped only for `Unclassified`: `Transaction` is a fourth door with an unconditional answer, a classified outcome from the wrong door is mapped anyway, and nothing says the kernel finds an `Outcome` through a decorator's wrapping

- **Where:** §D.14's map lines 5221-5231, specifically the `Transaction` row ("a non-nil error from
  `Transaction` → `ErrAmbientNotTransaction`, wiring class, wrapping it. That method has exactly one
  failure … so its error needs no classification and takes none") and the five classified rows, none
  of which is scoped to a door; §INV-045 lines 3948-3956 ("it is different per door", which then
  names `Append`, `ReadStream` and `ReadAll` and not `Transaction`) and its falsification lines
  3963-3977, which drives the three doors and not the fourth; §UC-047's Refusal lines 2319-2321 and
  §D.9's `lifecycle` row line 4803 ("an operation after close is `ErrClosed`", `always`); §UC-060's
  scoping list line 1655 ("a failure from `Transaction(ctx)`, `Limits()`, `Capabilities()` or
  `Backing()`, none of which issues a statement"); §UC-048's forwarding obligations line 2347 ("must
  not convert a cancellation, a conflict or an unknown commit into another class").
- **What:** Three related holes in an otherwise complete channel. (1) **`Transaction` is a fourth
  door and its error is mapped unconditionally.** The natural defensive implementation — every method
  guarded by `if this.closed() { return …, event.Failure(event.Closed, …) }`, which is exactly what
  §D.15's own `classify` puts first — makes a closed store answer `ErrAmbientNotTransaction`
  (wiring class) on the `Load` that follows a `Close`, while the `lifecycle` section, which runs
  `always`, asserts `ErrClosed` (store class). The document never says "`Transaction` must not fail
  because the store is closed"; it says only that its error "has exactly one meaning", which is a
  statement about intent rather than an obligation an implementer will read as one. (2) **A
  classified outcome is mapped regardless of which door produced it.** A store that returns
  `Failure(Conflict, …)` from `ReadStream` hands a caller a write-class `ErrConflict` from a *load*;
  §D.15's comment says the conflict arm "cannot fire on a read", which is a property of that store
  rather than of the contract, and §INV-045 says the map "is total, fixed and the only thing it
  reads". (3) **Nothing says how the kernel finds the `Outcome`.** A decorator that forwards and
  wraps — the ordinary observing decorator §UC-048 blesses — buries the `Failure` one `%w` deep. If
  the kernel type-asserts rather than `errors.As`, a wrapped `Failure(BadCursor, …)` takes the read
  door's fail-safe default and becomes `ErrBackend`: a store-class conversion of one class into
  another performed by the *kernel*, which is precisely what [ES] obligation 7 and §UC-048 forbid a
  wrapper to do, and it would make §INV-030's "the exact outer value always answers" true of the
  method and false of the error.
- **Why this severity:** Each is a concrete misclassification a correct store or a correct decorator
  reaches, in a channel that is one round old and whose whole point is that two stores cannot spell
  one classification two ways. `medium` because each is one clause and none loses data — (1) and (3)
  produce a wrong class, (2) needs a store bug.
- **Why this timing:** The map is frozen with the contract, `lifecycle` and `store failure
  classification` are both written against it, and the decorator question decides whether the kernel
  unwraps — which is a kernel implementation rule, not a store one.
- **Close criteria:**
  - [ ] §D.14's map and §INV-045 agree on how many doors there are and what each does with an
        unclassified and a classified error, `Transaction` included.
  - [ ] The document says whether a store may report closure from `Transaction`, and if not,
        §UC-047's "every later operation refuses with `ErrClosed`" says which call performs it.
  - [ ] §D.14 says whether the map is door-scoped for classified outcomes, or states that a store
        returning an impossible outcome for a door is a lie the suite cannot see, in §INV-045's own
        "what it does not claim" words.
  - [ ] §INV-045 states that the kernel finds the outcome through wrapping, and §UC-048's decorator
        obligations say a forwarding wrapper may wrap a store's error without losing its
        classification — with a `refusal classes` case that drives a wrapped `Failure`.
- **Status:** closed — see `## Resolution — one rule, and one scope`

### GAP-85 [low][immediate] The nil-by-any-route predicate is attributed to `crud`'s `isNilValue`, which is unexported, while §D.15's import table permits `event` exactly three `crud` symbols — so `event` must restate it, which is the duplication §INV-016's own argument rejects

- **Where:** §INV-016 lines 3161-3170 ("**`NewBacking` and `NewAuthority` refuse an identity that is
  nil by any route** … which is exactly `crud`'s own `isNilValue` predicate applied at a constructor")
  and lines 3171-3181 ("So `event` imports `crud` for **two** things and no others"); §D.14 lines
  5175-5188 (the same rule in both constructors' comments); §D.15's import table lines 5481-5485
  (`crud` for "**three** symbols and no more … `SameDataSource` … `crud.ErrConflict` and
  `crud.ErrUnavailable`", and the standard library for "`reflect` (the nil-identity predicate of
  `NewBacking`/`NewAuthority`, and nothing else)"). In the tree, `isNilValue` is
  `crud/executor.go:507-518` and is **not exported**; `comparableIdentity` (`:520`) is not either.
- **What:** GAP-78's fix closes a real hole and closes it in the right place, but its two statements
  do not agree. §INV-016 reads as reuse of a framework predicate; §D.15's import table makes reuse
  impossible and names `reflect` as the means, i.e. `event` writes its own copy of an eleven-line
  `crud` predicate. That is a second answer to "is this identity usable", on the constructor half of
  the same question §INV-016 refused to answer twice for `SameDataSource` — GAP-58's argument, one
  function over. The two honest resolutions are opposite in kind: state that `event` restates the
  predicate and why the duplication is acceptable here (it is small, it is total, and it has a
  five-case control), or record that `crud` should export it and that the export is a fourth symbol
  on the import list.
- **Why this severity:** `low` — the predicate is eleven lines, the behaviour either way is the same,
  and §INV-016 already ships the controls. Raised because the document states its import graph as a
  contract that Q4 and Q11 settle against, and because "applied at a constructor" reads as reuse of
  something no other package can reach.
- **Why this timing:** Immediate because it is one clause in the paragraph a reconciliation phase
  measures, and because the alternative resolution changes `crud`'s exported surface, which is a
  decision and not an implementation detail.
- **Close criteria:**
  - [ ] §INV-016 says whether `event` restates the predicate or calls one, and §D.15's import table
        agrees with it.
  - [ ] If it restates it, the duplication is named against §INV-016's own DRY argument for
        `SameDataSource`, and its control asserts the two answers agree for a nil interface, a typed
        nil pointer, a nil map, a nil slice and a non-nil identity of each.
- **Status:** closed — see `## Resolution — one rule, and one scope`

### GAP-86 [low][immediate] Four residual drifts from round 6's own edits

- **Where:** §5.3's header line 3759 ("Rounds 4 and 5's invariants"), which now carries INV-045,
  written this round and labelled round 6's by §5's preamble at line 2910; §D.16 line 5543
  ("`eventmemory` is **7**", followed by a list of seven names and then "plus its `Tx` type — 8 with
  it, and a `Tx` a caller must name is not optional" — so the count is 8, and the round-6 resolution
  table says 8); §D.9's `payload ownership` row line 4800, whose third clause is §UC-065's
  fold-mutate-fold — a property of a `Change` and of the kernel's decode, which §D.9's own new
  paragraph at lines 4818-4830 says does not belong in a per-store suite because "a property that
  does not vary with the store does not belong in it"; §D.2's `Codec` interface line 4171
  (`Name() string`), which no use case, invariant or §D section reads.
- **What:** Four small things a later reader treats as normative: a section header that names the
  wrong rounds, a count that contradicts its own parenthetical and the resolution note, a section
  assignment that contradicts the rule stated eighteen lines below it, and a method on the one
  interface every application with a custom codec must implement, with no stated reader — which
  §D.16's own "none is a 'for later' API" defence covers for functions and not for interface methods.
- **Why this severity:** `low` individually. Raised because rounds 1, 5 and 6 each established that
  this document's small drifts are how a fix silently fails to land, and because two of the four
  (§D.9's assignment, `Codec.Name`) are in what an implementer reads as contract.
- **Why this timing:** Each is one clause, and `Codec.Name()`'s answer changes an exported interface
  every consumer implements.
- **Close criteria:**
  - [ ] §5.3's header names the rounds it now carries.
  - [ ] §D.16 states one number for `eventmemory`, and it matches its own list.
  - [ ] §UC-065's assertion sits where §D.9's rule puts it, or §D.9 says why the `Change` half is a
        store's business after all.
  - [ ] `Codec.Name()` has a named reader, or it leaves the interface.
- **Status:** closed — see `## Resolution — one rule, and one scope`

---

**Checked this round and found sound** (so the absence of a finding is deliberate): the UC and INV
enumeration is complete and unduplicated, both withdrawals forward correctly, and no cited number is
unwritten; the microkernel walk over `eventpg`'s eight methods now finds **no** type it cannot
construct, which GAP-67's closure is what made true, and `event.Authority{}` as the invalid answer is
legal Go from any package; §INV-019's compile-time half (a third-package fixture returning a
classified failure of every outcome) is the right way to hold the zero-diff claim and is stronger
than the `git diff` arm alone; the `Outcome`/`Failure` channel is the tree's own shape
(`jobs.RejectPlacement` verified at `jobs/queue.go:799`) rather than a second one, and the
door-dependent fail-safe default is argued from the asymmetry of the two wrong guesses rather than
asserted; `Aggregate.Fold(id, …)` reached all eleven live call sites and every rejected two-argument
form sits in a rejected context; §UC-067's refusal to recover a fold's panic is argued beside
§UC-005's and the three misfits GAP-72 named are genuinely gone; §UC-047/§INV-032's two store shapes
and the invisibility property common to both are correct, and the commit-before-close control is what
stops it passing vacuously; §UC-028's marker-by-backing rule is the minimal correct fix and its
control is the case an always-innermost implementation fails; §INV-038's four new rows are right,
including `*Reader` as the one stateful caller-facing handle and the narrowing of §INV-021's
"retains nothing" to the kernel; §INV-016's typed-nil correction is accurate against
`crud/executor.go:562-571` and `NewBacking` returning an error is the right rung; the two-instance
pairing error is closed at both calls the framework mints both sides of, and the one residual (a
state carries no stream) is named in two places with the correct argument for why no proxy closes it;
§D.10's inventory is honest about all four residuals; every actor and entry mode has a `[happy]` use
case and the map is correct; all thirteen [ES] non-negotiables and obligations 1–7 map to a testable
INV; the edge matrix is complete (zero events, 100 000 events, empty, malformed, oversized,
duplicated, multilingual, ambiguous, timeout, conflicting signals); the second-instance tests on
`struct{ PlacedAt time.Time; Total int64 }`, a map-typed state, a payload with a slice, a composite
non-comparable identity, a three-revision chain and a generated declaration each cost the same
declaration and no extra argument, and the two that did not pass are GAP-79 and GAP-80; no §D line,
invariant or use case encodes one example aggregate as a mechanism (`Applied map[string]bool`,
`accounts.account` and the transfer are all shown as the application's own answers and cost no
framework feature); and §D.13 prices the design from stated arithmetic, including the new per-store
clone, rather than asserting it.

---

## Resolution — one rule, and one scope - 2026-09-06

Round 7 left eight findings open: two `[high][immediate]` and six between
`[medium]` and `[low]`, all immediate, none deferred. Round 7's own audit recorded
that **six of the eight are the round-6 fixes' residue** — the ratio this file has
tracked since round 1 — so the first question asked of each was again whether a
change of shape makes it unaskable, and only then whether a rule closes it.

**The count. Eight closed, none deferred, none carried to `## Debt`, one
alternative rejected on the record.** `## Deferred` still holds GAP-25, GAP-41 and
GAP-53 and gains nothing; `## Debt` still holds nine items and gains nothing.

| # | Severity | Disposition | What did it |
|---|---|---|---|
| GAP-79 | `[high]` | **dissolved by making one rule uniform** | §INV-021 stops being four clauses and becomes *one rule with three landings*: **a party that retains a byte array hands a decoder, or a caller, memory it will never read again.** The codec's landing is `Fact.New`'s clone, the store's is `Envelope.Payload`, and the kernel's — the one round 6 left out — is `Fold` cloning a `Change`'s frozen bytes before each decode. The finding's "who owns these bytes" is no longer askable, because ownership is not per-party any more |
| GAP-80 | `[high]` | **closed by correcting the overclaim (option a)** | §INV-020 becomes a two-row invariant: the compile error holds for two aggregates over **two state types**, and `ErrWrongStream` — the family half of the same comparison that catches two instances — covers two aggregates over **one**. §UC-002's trigger takes the second declaration both ways and its control is two controls; §UC-026, §D.2, §D.10, §D.11 and §INV-044 carry the same scope. Option (b) was considered and is rejected on the record in §D.12 |
| GAP-81 | `[medium]` | **closed by enumerating what was already true** | `Fold` has **four** causes, in `Append`'s own order: `ErrKey` for an identity that renders illegally, `ErrWrongStream`, a carried refusal, a decode failure. §UC-041 and §D.8 now list the same four and §UC-041's Refusal names both sentinels with their classes; the third control asserts `Fold(zeroID, …)` is `ErrKey` and not `ErrWrongStream`, which is what pins the order |
| GAP-82 | `[medium]` | **closed by a rule stated where it is a mechanism** | The split becomes **"a key is validated at every door it crosses, produced or presented"**: the kernel's text rules at `Load`, `New`, `Fold` and `Append`; the store's `MaxKey` at the two calls that know a store. §D.5 writes `Append`'s six-step order out, §INV-015's defence (1) is performed again and its falsification is writable, §INV-031 says the key check survives the empty-append short circuit, §2.1 carries the split, §D.13 prices the scan |
| GAP-83 | `[medium]` | **closed by separating three fixtures** | `eventtest`'s test package holds the trivial store (§UC-043's control and §INV-019's proof, unchanged), an admit-everything store, and a **separate transaction-capable** leaky-rollback store. The suite writes the whole `Factory` for a self-falsification run, so `Begin` is the suite's and the section a defect targets always runs for that defect — which is what the "at least one named section" claim needed |
| GAP-84 | `[medium]` | **closed in three clauses, each a mechanism** | §D.14 and §INV-045 now agree on **four doors** (`Transaction`, `ReadStream`, `ReadAll`, `Append`), disambiguated from §INV-022's two; a store **must not report closure from `Transaction`** and answers the invalid authority with a nil error, so `lifecycle`'s `always` assertion holds; a classified outcome is mapped **at any door**, and a store that classifies impossibly is a liar in §INV-045's existing "what it does not claim" sense; and the kernel finds the outcome with **`errors.As`**, so a wrapping decorator keeps it — with a `refusal classes` case and a `store failure classification` case that drive a wrapped `Failure` |
| GAP-85 | `[low]` | **closed by naming the duplication and pricing it** | §INV-016 says `event` **restates** the nil predicate over `reflect` because `crud`'s is unexported, and argues why that is not GAP-58's defect: `SameDataSource` is a comparison on every append whose divergence changes which program is admitted, this is a constructor-time total function whose two answers both refuse. Its control asserts all five nil routes case by case. §D.14's comment and §D.15's import table say the same thing |
| GAP-86 | `[low]` | **closed, all four** | §5.3's header names rounds 4, 5 and 6; `eventmemory` is **8** and its list has eight names; §UC-065's fold-mutate-fold moves out of `payload ownership` to `event`'s own tests, where §D.9's own rule puts it — and is driven there **with an aliasing codec**, which is GAP-79's fifth close criterion; `Codec.Name()` is **deleted** from the interface, having no reader anywhere |

**GAP-79, in full, because it decided a cost.** The three candidate owners were
not interchangeable and the finding said so. *Oblige `Codec.Decode` not to alias
its input* is rejected: §INV-021 had already established that aliasing decoders
are ordinary, and a codec that could be trusted this way would make the store's
landing unnecessary too — it is a promise no third-party codec makes and no proxy
can falsify. *Declare a fold's `E` borrowed* is rejected: §UC-065 forbids it in
terms, and it is level four of the safety ladder where level three is available.
Both rejections are rows in §D.12, together with a third — *a hand-out rule that
binds the store and not the kernel* — so the shape round 6 shipped is on the
rejected list rather than merely superseded.

**The cost, stated in §D.13 rather than called acceptable.** On a replay through
`Load` the kernel's clone costs **nothing**: the load path decodes an
`Envelope.Payload`, which is the store's hand-out and is already memory nobody
will read again, so a 10 000- and a 100 000-event replay each pay zero kernel
clones. That is the argument for uniformity rather than against it — a kernel that
cloned defensively everywhere would have paid on every event of every read for
every store. The clone falls only on `Aggregate.Fold` over `Change` values: one
allocation and one 200 B copy per change, none retained past the decode, so
~2 MB total for a 10 000-change fold and ~20 MB for a 100 000-change fold, one
payload live at a time. The ordinary operation folds at most `MaxBatch` = 64
changes, which is ~13 KB.

**The alternative rejected on the record for GAP-80**, so nobody re-proposes it in
phase 2: a **per-aggregate phantom type parameter** — `Aggregate[S, ID, A]`,
`Change[S, A]`, `At[S, A]`, `Repo[S, ID, A]`, `Fact[S, ID, E, A]`. It does close
the second row at the type level. It costs every consumer a marker type whose only
job is to be distinct, adds a parameter to four of the five caller-seam types, and
cannot be inferred from anything at the declaration site, so `Define` spells all
three by hand — which is §D.11's whole subject going the wrong way. And it buys
nothing for the larger risk, two *instances* of one aggregate, which no type
parameter reaches and which the same comparison already refuses. §D.12 carries it.

**Two things round 7 changed that no finding asked for, listed so a later reader
does not read them as drift:**

- **The three round-narrative sections are deleted and replaced by one.**
  "Round 4 — the seam redesign", "Round 5 — the pairing seam" and "Round 6 — the
  store's own voice" were 229 lines that restated §D.12's rejected-alternatives
  table, §2.3, §INV-021 and §INV-040 in prose, one section per round. They are
  replaced by a 45-line `## How this document got its shape` carrying the root
  cause, the thesis, and an eight-row table of *the shape change → what it makes
  unaskable*, pointing at §D.12 for the arguments and at this file for the
  dispositions. A specification no implementer will finish reading has failed at
  its one job, and a per-round section is the growth pattern that gets it there.
- **Eleven duplicated arguments are compressed to their citation.** Each was
  argued in full in two or three places: the `Transactional` interface (§UC-033 +
  §D.12 + §INV-030), the per-commit durability class (§UC-020 + §D.5 + §D.12 + Q8),
  the caller-supplied read limit (§UC-036 + §D.7 + §D.12 + non-goal 19), `ErrBatch`
  (§UC-025 + §D.12), the partition-not-a-matrix argument (§2.3 + §INV-024), the
  store-owned codec (§D.2 + §INV-023), the cursor's type (§UC-053 + §D.12 + Q18),
  the family collisions `Bind` cannot see (§UC-059 + §INV-039 + Q19), the memory
  store's transactions (§UC-040 + Q12), the idempotency key (§UC-035 + Q16 +
  non-goal 14), the two-values-one-backing agreement list (§UC-054 + §D.4), the
  savepoint assertion's home (§UC-049 + §INV-028 + §D.12 + §9), the minted-token
  rejection (§INV-028 + §D.12), and the two `Close` staging shapes (§UC-047 +
  §INV-032 + §D.14 + §D.12). In each the argument now lives once and the other
  sites cite it.

**Line count: 5690 before, 5669 after.** Eight findings closed, one deleted
mechanism (`Codec.Name`), one deleted section, fourteen compressions.

**The zero-diff check, re-run by hand after the edits.** `eventpg`'s eight
methods, constructed value by value from §D.14 as round 7 leaves it:
`Capabilities`/`Support` are a plain struct and exported constants;
`Limits` is a plain struct; `Backing` is `event.NewBacking(crud.KeyOf(source))`,
exported and error-returning; `Envelope`, `Record`, `AppendRequest` are plain
structs with exported fields; `Stream` is two exported fields and `Key`,
`Version`, `Position`, `Cursor` are defined types a store converts into;
`Transaction` needs `event.NewAuthority` and the invalid `event.Authority{}`,
which is a composite literal of an all-unexported-field struct and legal Go from
any package; every error is `event.Failure(outcome, cause)` over an exported
closed enum. **There is no value it cannot construct.** None of round 7's eight
fixes adds a store obligation that needs new API: the kernel's clone, `Append`'s
key check, `Fold`'s fourth cause, `errors.As` and the non-door-scoped map are all
kernel-side; "a store must not report closure from `Transaction`" *removes* a
thing a store may do; §INV-020's scope is caller-seam only; and the third
`eventtest` fixture uses `NewAuthority` and `Authority{}`, both already exported.
The one new `event` import is `bytes`, which is standard library and invisible to
`check-deps`. **Phase 2 is still writable with zero diffs under `event/`.**

**The direction the next reduction should look**, unchanged from rounds 5 and 6:
the refusal vocabulary's kernel half is still policed by a table test rather than
carried by a type. Twenty-two sentinels whose class membership is asserted rather
than structural is the last place in this design where a rule stands in for a
mechanism.

---

## Round 8 - econv-usecase-validator (coverage + invariant + DX roles, post-one-rule-one-scope re-audit) - 2026-09-06

Sources read: `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (whole file, **5669 lines**,
read in nine passes with no gap), `.agents/artifacts/gaps/EVENTSOURCE_P1_USECASES_GAPS.md` (all
seven rounds and all four resolutions, including this fix round's dispositions),
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` (the thirteen non-negotiables at
lines 217-243, the ten capability/wrapper obligations at lines 331-356, the transaction and
atomicity rules), `CLAUDE.md`, `~/.claude/skills/econv/SKILL.md` and
`references/{gaps,universality,microkernel}.md`.

**Framework symbols the spec names, checked against the tree and kept separate from the spec's own
quality.** Every symbol this round's edits added or leaned on is real and is attributed the shape it
actually has. `jobs.EncodedPayload`'s constructor (`jobs/codec.go:50-62`) does
`bytes.Clone(data)` **and then** `data[:len(data):len(data)]`, which is exactly the "clones
`Encode`'s output and freezes it with a full-slice expression" §INV-021's codec landing attributes
to it, and `EncodedPayload.Bytes()` (`:66`) clones on the way out. `jobs.invokeUpcaster`
(`jobs/upcast.go:138-153`) calls the application's upcaster with `bytes.Clone(encoded)` and recovers
a panic into `ErrInvalid` — so §INV-021's "`bytes.Clone` in … at a decode boundary is also what
`jobs.invokeUpcaster` already does" and §UC-016's recovered-upcaster precedent are both accurate;
the "`bytes.Clone` out" half of that sentence lives in `EncodedPayload.Bytes()` rather than in
`invokeUpcaster`, which is a citation looseness and not a wrong shape. `crud.SameDataSource`
(`crud/executor.go:562`) opens with `if a == nil || b == nil { return false }` over the *interface*,
which is precisely the typed-nil narrowness §INV-016 now states as its own. `crud.KeyOf` (`:524`),
`crud.ExecutorFor` (`:370`, returning `(Executor, bool)`), `crud.WithExecutorFor` (`:341`),
`crudsql.Transaction` (`crud/adapter/crudsql/crudsql.go:195`, returning `(*sql.Tx, bool)`) and
`crudsql.TransactionFor` (`:210`, returning `(*sql.Tx, bool)`) all exist as named — and §D.6's
argument that only the **two-step** form distinguishes *nothing is bound* from *bound but not a
transaction* is true of those two signatures as written. `crud.ErrUnavailable` (`crud/errors.go:38`)
and `errs.KindTooLarge` (`errs/code.go:58`) are exported, so §D.15's three-symbol `crud` list plus
`errs` is buildable. `crud.IsTransaction` (`crud/executor.go:38`) is exported and the spec correctly
does **not** use it, because the authority needs the `*sql.Tx` itself. **No symbol the document names
is missing, unexported where it is used as exported, or misattributed.**

**Enumeration.** UC-001…UC-067 and INV-001…INV-045 each appear **exactly once**; no number in either
range is missing, none is written twice, and no number outside either range is cited. UC-023 and
INV-037 are `WITHDRAWN` in place and every citation of them is the withdrawal notice itself
(lines 434, 1286, 1293, 2751, 3551, 3556, 3557) — neither is cited as live. §2.3's "twenty-two
sentinels" is arithmetically correct (3+7+3+4+2+3). §D.16's "≈ 71" resolves to 13+27+9+22 and each of
the four lists has the stated length; `eventmemory` is 8 with eight names; `eventtest` is 6 with six
names. §INV-036's falsification counts five occurrences of "exactly once" and there are exactly five
(2028, 3539, 3542, 3545, 4520), every one a prohibition.

**Regression sweep over this fix round's own edits.** Every mention of `ErrStaleView`, `event.Replay`,
`Store.Codec()`, `event.Copy`/`eventtest.Copies`, `eventtest.FailKind`/`FailNotWritten`/
`FailUnconfirmed`, `event.Since`, `Commit.Durability`/`Positions`, the store-side *freeze* wording
and the two-argument `Fold(state, changes...)` sits in a **deleted or rejected** context; `Codec.Name`
is gone from §D.2's interface and from everywhere else. §INV-021's clause numbering is gone with no
orphan citation of "clause (3)" or "clause (4)". §5.3's header names rounds 4, 5 and 6 and matches
§5's preamble. "Three doors" and the unkeyed "innermost marker" survive only in their own rejected
sentences. All eleven live `Fold(` call sites carry the identity. §UC-021's and §D.5's snippets still
compile. **Four things the fix did not reach are GAP-88, GAP-89, GAP-90 and GAP-92**; one is a hole
the uniform rule left at a fourth hand-off (GAP-87); one is the compression pass stopping short
(GAP-91).

**GAP-79 re-checked against the new text, byte by byte.** The path is now closed in all three
directions the finding named. Caller payload → `Fact.New` → `Codec.Encode` → **clone + full-slice
freeze** (§INV-021 codec landing, §D.3), so a codec that reuses an internal buffer cannot reach the
`Change`. `Change` → fold → **clone before the decode** (§INV-021 kernel landing, §D.3 line 4156,
§UC-065 line 4459-4463, §INV-038's `Change[S]` row, §D.14's division-of-labour row 5257, §D.10 row
4766), so an aliasing codec's `E` points into a per-fold copy and `s.Lines = e.Lines` publishes
memory the framework drops. Store → caller → the store's hand-out landing, unchanged, with the load
path deliberately not cloning because the envelope payload is already the store's hand-out (priced at
zero in §D.13). §UC-063's "the other order" — fold, mutate the published state, then append the same
list — is now true, and §UC-065's control **drives an aliasing codec** rather than inheriting
`event.JSON`'s good behaviour. The conformance suite deliberately does **not** drive the kernel path:
§D.9's `payload ownership` row carries the store's landing only, and §UC-065's fold-mutate-fold is
moved to `event`'s own tests under §D.9's stated rule that a property which does not vary with the
store does not belong in a per-store suite. That is consistent, and §INV-021's falsification list and
§INV-038's `-race` row both name the aliasing codec, so the kernel landing is falsifiable where it
was placed. **One hand-off of the same bytes is still unowned — the kernel handing its retained
array to the store as `Record.Payload` — and that is GAP-87.**

**GAP-80 re-checked against the new text.** A pair of aggregates over one state type
(`type Ledger map[string]int64`, or a generated set emitting one `Aggregate[Doc, DocID]`) was walked
through every claim: §INV-020's two rows, §UC-002's two triggers and two controls, §UC-026's
boundary paragraph, §UC-064's "why this is a call-time refusal", §D.2 line 4084, §D.5 line 4313,
§D.10's split rows 4762-4763, §D.11's two "when its state type differs" rows, §INV-044's "and two
aggregates over one state type", §D.12's phantom-parameter rejection, and §D.9's `expected version`
row. Every one is true as written **except the falsification's third crossing**: over one state
type the *repository* crossing has no runtime refusal at all, which is GAP-90.

**The microkernel walk passes, and nothing this round added moved it.** Hand-constructing `eventpg`'s
eight methods from §D.14 as it now stands, value by value: `Capabilities()` needs `Capabilities`
(plain struct of four `Support` fields) and the exported constants; `Limits()` needs `Limits` (plain
struct of five ints); `Backing()` needs `event.NewBacking(crud.KeyOf(spec.Source))`, exported and
error-returning; `ReadAll` needs `[]Envelope` (plain struct, exported fields) and `Cursor`
(defined string, minted by conversion) and may fail with `event.Failure(event.BadCursor, err)`;
`ReadStream` consumes `Stream` (two exported fields) and `Version` (defined `uint64`) and returns the
same envelopes; `Transaction` needs `event.NewAuthority` **and the invalid answer**, which is
`event.Authority{}` — a composite literal of an all-unexported-field struct, legal Go from any
package, and the value §D.15's own sketch writes twice; `Append` consumes `AppendRequest`/`Record`
and returns `event.Failure(outcome, cause)` over an exported closed enum, or a bare `ctx.Err()`;
`Close` returns a plain error. **There is no value it cannot construct.** All eight of this round's
fixes are kernel-side, caller-seam-side, or *removals* of a store affordance ("a store must not report
closure from `Transaction`" takes a behaviour away and needs `event.Authority{}`, already exported);
the third `eventtest` fixture needs `NewAuthority`, `Authority{}` and its own context key, all
available from a third package. The one new `event` import is `bytes`. **`microkernelPasses: true`.**

**Coverage, checked and sound.** Every actor A1–A7 resolves to a `[happy]` use case with a matching
actor line (A1→UC-001/002/003, A2→UC-006/007, A3→UC-019/028, A4→UC-036, A5→UC-043, A6→UC-040,
A7→UC-048) and every entry mode E1–E6 has a group. All **thirteen** [ES] non-negotiables map to a
testable invariant (append-only→INV-001, exact version→INV-002, one transaction→INV-003, pure
rehydration→INV-004, declared wire identity→INV-005, no partial rehydration→INV-006, no framework
retry→INV-007, replay is authority→INV-008, at-least-once→INV-036 with the placement noted in §5.1's
preamble, no default telemetry fields→INV-011, not a CRUD collection→INV-012, no constructor starts
anything→INV-013, no start-up migration→INV-014). Obligations 1–7 land squarely (§UC-048, §INV-030,
§INV-043, §INV-029); 8 and 9 are conditioned on wrappers phase 1 does not build; 10 is §9 item 9 plus
§D.16's inventories. The edge matrix is complete: empty (UC-009, UC-020), malformed (UC-004, UC-015),
oversized (UC-017, UC-024, UC-025), ambiguous (UC-034), duplicated (UC-035, UC-058), multilingual
(UC-051's NFC/NFD and NUL, INV-033), partially failing dependency (UC-060), timeout (UC-018,
UC-057), zero results (UC-009, UC-036's control), everything-matches (UC-039), conflicting signals
(UC-049, UC-057). No UC contradicts another that I could construct.

**Universality, three checks run.** `struct{ PlacedAt time.Time; Total int64 }`, a state that **is**
a map, a payload carrying a slice, a composite non-comparable identity, zero events, 100 000 events,
a three-revision chain and a generated declaration set each cost the same declaration and no extra
argument; the two aggregates over one state type now cost one runtime refusal that is named. No use
case, invariant or §D line encodes one example aggregate as a mechanism — `Applied map[string]bool`,
`accounts.account` and the transfer are all shown as the application's own answers and buy no
framework feature. The one place a rule is written from the two stores that exist and is silent about
a third is GAP-87.

**Silent misuse, asked and answered.** The worst thing a caller can write that compiles, runs,
returns no error and is wrong remains §D.10 item 1 (forgetting the `Fold` line between two appends)
and item 4 (handing one instance's state to another instance's fold). Both are named, argued, and
item 1 has a control (§UC-062) whose whole content is that deleting the line must fail the test. The
document does not pretend either is closed. The worst thing a **store implementer** can write that
compiles, runs, returns no error and is wrong is now GAP-87, and the document names nothing about it.

**Length.** 5669 lines, against 5690 before this fix round: it shrank by 21 while closing eight
findings and deleting one mechanism, so the growth pattern is not the finding. The remaining
redundancy is GAP-91.

### GAP-87 [high][immediate] The kernel hands the store its own retained array as `Record.Payload`, and nothing in the contract forbids the store — or a policing decorator — to write into it, so §INV-021's now-uniform rule has a fourth hand-off it does not describe

- **Where:** §INV-021's rule and its three-landing table, lines 3162-3171, specifically the store row
  ("It clones a `Record.Payload` it keeps for the mirror reason: the kernel's `Change` outlives the
  append") — an obligation about **retention** and not about **mutation**; §D.14's `Append` bullet
  lines 5295-5305, which is A5's whole surface and lists exactly four prohibitions (no retry, no
  chunk, no deduplicate, no class conversion) and nothing about the buffer; §D.14's `AppendRequest` /
  `Record` declarations lines 5070-5080 (`Records []Record // already encoded, already inside every
  bound`); §UC-063 line 2358 ("What reaches `Record.Payload` is still the frozen array"); §D.3 lines
  4159-4161 ("The frozen array itself is written by nothing and read only by the cloner"); §UC-048
  line 2140, which forbids a **wrapper** to "replay, buffer or re-inspect an `AppendRequest`'s
  records" and does not forbid it to modify them, and §UC-045 lines 2695-2696, which scopes even that
  to "a decorator that claims to forward"; §D.5's two-decision sketch lines 4328-4336, which folds the
  same change list **after** it has been appended.
- **What:** §INV-021 is now stated as **one rule** — *a party that retains a byte array hands a
  decoder, or a caller, memory it will never read again* — with three landings, and the document's
  own argument for uniformity is that "a rule that binds one holder and not another is two rules with
  one name, and whichever holder is left out is where the corruption lands". There is a **fourth
  hand-off of the same array and it is not in the table**: the kernel retains the `Change`'s frozen
  bytes for that change's whole life and hands that exact array to the store as `Record.Payload` — a
  recipient that is neither a decoder nor a caller, and one the kernel demonstrably **will** read
  again (every later `Fold`, and every later `Append` of the same slice, which §D.3 line 4197 says is
  legal). Read literally, the uniform rule forbids that hand-off; read charitably, it is an exception
  the document does not name. Either way the obligation that makes it safe — *a store must not write
  into the payload it was handed* — appears nowhere, and §D.14 is by its own opening sentence the
  whole of what a store "may rely on". A store that transforms a payload in place (compression,
  encryption, redaction — the ordinary reason to touch a buffer you were given) silently rewrites the
  caller's already-decided `Change`; a decorator that redacts in place does the same and violates no
  sentence of §UC-048. The consequences are the three §UC-063/§UC-065 close as unrepresentable: the
  fold that §D.5's own northstar runs **after** the append yields a state that disagrees with the
  reload with no error anywhere; re-appending the slice records the mutation; and §D.3's "written by
  nothing" becomes false.
- **Why this severity:** `universality.md`'s second-instance test on the phase's one extension point:
  the rule is correct for the two stores that exist — `eventmemory` clones what it retains, a SQL
  store hands the bytes to a driver — and undefined for a structurally different third, which is the
  "behaviour undefined for a neighbouring input: the code neither handles it nor fails loudly" row,
  reported `[high][immediate]` at minimum and never deferred. It is also GAP-79 exactly one hop over:
  the same array, the same silent corruption of an already-decided fact, the same "the fact recorded
  is not the fact decided" claim falsified, differing only in that GAP-79 was reachable through steps
  the document blessed and this one is reachable through a step the document does not mention. The
  stdlib states this obligation where it has the same shape (`io.Writer`: "Implementations must not
  retain p" **and** "must not modify the slice data, even temporarily"), which is the evidence that a
  contract of this kind is expected to carry it rather than assume it.
- **Why this timing:** §D.14 is frozen at the end of phase 1 and this is a clause of it; it also
  decides whether the kernel must clone into `Record.Payload` (paying `MaxBatch` copies per append,
  which §D.13 would have to price) or the store must promise not to write (free, and the same rung
  §INV-021 already uses for the store's outbound half). Deciding after `event` is written is deciding
  by accident, and the wrong choice is discovered by a consumer's store in production — which is
  GAP-79's own timing argument, unchanged.
- **Close criteria:**
  - [ ] §INV-021's table names the kernel→store hand-off of `Record.Payload`, or the rule is restated
        so that it is total over the design's own byte flows rather than over three parties.
  - [ ] §D.14's `Append` bullet states what a store may do with `Records[i].Payload` — read it, and
        clone it if it retains it — and states that it must not write into it, in the same place the
        other four `Append` prohibitions live.
  - [ ] §UC-048's decorator obligations say the same for an `AppendRequest` it forwards, since
        "replay, buffer or re-inspect" does not cover modification.
  - [ ] §D.3's "written by nothing and read only by the cloner" is made true, or scoped to say who
        else reads it and under what promise.
  - [ ] The `payload ownership` conformance section gains the inbound half — append a change, have
        the fixture write into every byte of every `Record.Payload` it was handed, then fold the same
        change list and re-load, both unchanged — so the obligation is exercised rather than written
        down, with the uninjected append as its control.
- **Status:** closed by making §INV-021's rule total over **five hand-offs** rather than three holders, and by choosing the store's **promise not to write** over a kernel-side clone — priced in §D.13 at 10 000 clones / ~2 MB for a 10 000-event write and 100 000 / ~20 MB for 100 000, on every store, against zero for the promise. §D.14's `Append` bullet and `Record.Payload`'s comment carry the obligation, §UC-048 extends it to a forwarding decorator, §D.3's sentence is made true, §D.9's `payload ownership` gains an inbound case with a control, and §UC-045 gains a **sixth** self-falsification defect aimed at it. The rejected clone is a §D.12 row.

### GAP-88 [medium][immediate] GAP-81's fix renumbered `Fold`'s causes and left two references pointing at the wrong ones, and the fourth cause — a decode failure at fold time — still has no sentinel anywhere

- **Where:** §UC-041's "Why it returns an error, and the causes are four" lines 2243-2261, which lists
  (1) `ErrKey`, (2) `ErrWrongStream`, (3) the change's carried refusal, (4) a decode that fails when
  the fold runs; its **Refusal** line 2262-2264 ("`ErrKey` (request class) for cause 1,
  `ErrWrongStream` (wiring class) for **cause 4**, and the change's own carried refusal for **causes 2
  and 3**"); its **Control** line 2270 ("what pins the order of **causes 1 and 4**"); §D.8's parallel
  enumeration lines 4550-4560, which is correct; §2.3's class table lines 301-302.
- **What:** The list and §D.8 agree; the two lines directly under the list do not. `ErrWrongStream` is
  cause **2**, not 4; the carried refusal is cause **3** alone, not 2 and 3; and the Control's
  "order of causes 1 and 4" is the order of causes 1 and **2**, which is the only ordering the
  zero-identity case can pin. Worse, the mis-numbering hides that **cause 4 has no sentinel at all**:
  a decode that fails when a fold runs is described ("a broken codec, whose proxy is
  `eventtest.RoundTrip`") and never classified, here or in §D.8 or in §2.3. The obvious candidate,
  `ErrPayload`, is in the **history** class, whose stated meaning is *the stored history cannot be
  read by this declaration* — and a `Change` is not stored history, it is a fact decided seconds ago,
  so borrowing it repeats precisely the class misuse GAP-72 found for a panicking fold. GAP-81's
  second close criterion was "§UC-041's Refusal line names every sentinel `Fold` can produce, with its
  class", and it is not met.
- **Why this severity:** A test author writing the `Fold` cases reads the Refusal line, not the list,
  and asserts `ErrWrongStream` for a decode failure and a carried refusal for a stream crossing —
  both wrong, in two different classes with two different transport answers. `medium` because every
  candidate answer refuses and nothing is written.
- **Why this timing:** `Fold`'s refusal set is public contract in a frozen vocabulary; §D.9 states
  that the `Fold` crossing is asserted in `event`'s own tests rather than in the suite, and that test
  has to name one sentinel per cause.
- **Close criteria:**
  - [ ] §UC-041's Refusal line maps each of the four causes to its own sentinel, with the numbers
        matching the list above it.
  - [ ] §UC-041's Control says "causes 1 and 2".
  - [ ] Cause 4 has a named sentinel and a class, and if it is `ErrPayload` the history class's
        stated meaning is reconciled with a decode of a `Change`'s own bytes; §D.8 carries the same
        answer.
- **Status:** closed by renumbering §UC-041's Refusal and Control lines to the list above them and giving cause 4 the sentinel it lacked: **`ErrPayload`, history class**, with the class's meaning restated over *a fact's recorded bytes* rather than over the store — a `Change`'s frozen array and the envelope it becomes are the same bytes (§UC-063), so one decode failure needs one sentinel. §D.8 and §UC-015 carry the same answer, and the resolution argues why this is not GAP-72's class misuse repeated.

### GAP-89 [medium][immediate] §UC-045's "Must not happen" still carries the superseded skip clause, which contradicts the three-fixture fix's central guarantee that the section a defect targets always runs

- **Where:** §UC-045's "Must not happen" lines 2721-2724 ("A defect aimed at a capability the store
  does not claim (the transaction one) is reported **not certified** in §UC-044's words, never as a
  pass"); against the same use case's "What the two defective stores are built from" lines 2697-2708
  (three fixtures, the third claiming `Transactions: Supported`) and "Where its transaction comes
  from" lines 2709-2717 ("The self-falsification run is a run whose whole `Factory` `eventtest`
  writes … The suite therefore **also fixes the defect's capability report, so the section a defect is
  aimed at always runs for that defect**: a skipped section is never a pass for a defect"); §D.9's
  `self-falsification` row line 4715, which runs `always`.
- **What:** GAP-83's fix moved the self-falsification run off the store under test entirely: the suite
  builds the defective store, writes the whole `Factory`, and fixes the capability report. Under that
  shape there is no store in the run that "does not claim" transactions — the leaky-rollback fixture
  claims `Supported` by construction — so the Must-not-happen clause describes a situation the new
  mechanism makes unreachable, and it describes it as the *correct* outcome. The two sentences are
  eight lines apart and say opposite things about the same defect. An implementer who reads the
  Must-not-happen line as normative builds exactly the skip path GAP-83 closed, and the suite's
  self-check silently loses a fifth of its coverage against a store that declares
  `Transactions: Unsupported` — which is `eventmemory` under Q12's cheaper fallback and every
  read-through store §UC-033's precondition contemplates.
- **Why this severity:** It is a live contradiction inside the one use case whose entire purpose is
  that a green run is evidence, and the losing reading is the one that produces a vacuous pass.
  `medium` because the fix is one clause and the correct behaviour is stated eight lines above it.
- **Why this timing:** The suite's dispatch rule for the self-falsification run is being specified
  now; a plan section written against the wrong sentence produces a skip path that then has to be
  removed together with its test.
- **Close criteria:**
  - [ ] §UC-045's "Must not happen" either deletes the "not certified" clause or scopes it to
        something the three-fixture shape can still reach, and does not contradict "the section a
        defect is aimed at always runs for that defect".
  - [ ] §UC-044's three-word reporting rule is stated to be about the **store under test**, so that
        "not certified" and the self-falsification run cannot be read as the same mechanism.
- **Status:** closed by deleting the superseded skip clause from §UC-045's *Must not happen* — it now says no defect's section may be skipped for any reason — and by scoping §UC-044's three words to the **store under test** in a new bullet, so *not certified* and the self-falsification run can no longer be read as one mechanism.

### GAP-90 [medium][immediate] §INV-020's second row is falsified over "the same three crossings", but the repository crossing has no runtime refusal over one state type, so a third of the stated falsification cannot be written and the row overclaims

- **Where:** §INV-020's two-row table lines 3126-3128 (row 2: "share **one Go state type** | a
  **call-time refusal**, `ErrWrongStream`") and its **Falsified by** lines 3147-3153 ("A build-failure
  fixture per crossing (change, token, repository) over **two different state types** … and, over
  **one** state type, a runtime assertion that **the same three crossings** return `ErrWrongStream`
  and write nothing"); §D.5's six-step `Append` order lines 4304-4311, which contains no comparison
  of the token's family against the repository's own aggregate; §INV-044's statement lines 3763-3769,
  which defines the comparison as change-stream against token-stream and nothing else; §UC-002's
  Control lines 499-504.
- **What:** Over two state types the three crossings are three distinct compile errors, because
  `Change[S]`, `At[S]` and `*Repo[S, ID]` all differ. Over one state type they collapse to two
  behaviours, not three. The **change** crossing and the **token** crossing both reduce to "some
  change's stream ≠ the token's stream" and are refused with `ErrWrongStream` as stated. The
  **repository** crossing does not: `*Repo[Ledger, LedgerID]` bound for aggregate A and for aggregate
  B is one Go type, and nothing compares a token's family or a change's family against the
  repository's own. `repoB.Append(ctx, atFromA, changesFromA...)` therefore **succeeds** — correctly,
  since the token and the changes agree and the stream comes from the token — and `repoB.Load(ctx,
  id)` returns aggregate B's stream folded by B's folds with **no error**, which is a silent wrong
  answer that only the eventual append catches, and only when the caller mints through A's fact
  handles. So the falsification's second half is unwritable for one of the three crossings it names,
  and the row's unqualified "a call-time refusal, `ErrWrongStream`" is true of two crossings out of
  three.
- **Why this severity:** GAP-80's whole content was that an invariant stated at the wrong scope proves
  itself only for the shape it was written from, and its fix restated the scope while leaving the
  falsification quantified over a crossing the new scope does not cover. A suite written against it
  either drops the third case silently or asserts a refusal that a correct implementation does not
  produce. `medium` rather than `high` because the behaviour is safe — the dangerous combination is
  refused at the append, and the harmless one lands in the right stream.
- **Why this timing:** §INV-020 was rewritten this round precisely so a later reader could trust its
  scope; the residual is one clause now and an argument about whether the design changed once the
  fixture cannot be written.
- **Close criteria:**
  - [ ] §INV-020's falsification names, for the one-state-type half, only the crossings that have a
        runtime refusal, and says what the repository crossing does instead.
  - [ ] §INV-020's row 2 or a neighbouring clause states that a repository is a handle and that the
        stream comes from the token, so "the wrong repository" is refused only through the values it
        is called with.
  - [ ] If a `Repo`-carries-its-family check is wanted instead, §D.5's six-step order gains it and
        §INV-044 says so; otherwise §D.10 gains the row naming `repoB.Load` as a residual, beside its
        four existing ones.
- **Status:** closed by scoping the falsification (option a) and naming the residual. §INV-020's row 2 covers the two crossings that pair two values; a new clause says `Append` never reads the repository's declaration, so the append crossing is the same call over one store and `ErrWrongStore` over two, and `repoB.Load` is correct behaviour for `repoB` with nothing to compare against. §D.10 gains a **fifth** residual row for `repoB.Load`, and the `Repo`-carries-its-family alternative is rejected on the record in §D.12.

### GAP-91 [medium][deferred] Two arguments the compression pass did not reach are each argued in full in four and five places, which is the growth pattern the round's own note says it was reversing

- **Where:** The **two-doors store-honesty** argument (GAP-60's), argued in full — with the projector,
  the `MaxRead: 0` store and the drain that reads nothing — at §UC-036 lines 1937-1946, §INV-022 lines
  3248-3260, §D.4 lines 4230-4236 and §D.7 lines 4494-4503, with a fifth statement of the same
  rejection in §D.12 line 4899 and the three-check enumeration restated again at §UC-031 lines
  1782-1788 and §UC-055 lines 841-847 — seven sites, four of them carrying the whole argument. The
  **`Fold` takes the identity** argument (GAP-70's), argued in full at §UC-041 lines 2231-2242,
  §INV-044 lines 3780-3789, §D.8 lines 4542-4548 and §D.5 lines 4338-4343, with two more rejections in
  §D.12 lines 4900-4901 and a further statement in §UC-064 lines 2403-2411.
- **What:** This fix round's note lists fourteen arguments compressed to a citation and states the
  principle — "the argument now lives once and the other sites cite it". These two are the largest
  remaining violations of it and are not on the list. Each restatement is a place a later fix must
  reach: GAP-76, GAP-82 and GAP-86 were all one edit landing in some of the places a rule was stated
  and not others, and the two arguments above are stated in more places than any of those were.
- **Why this severity:** `medium` under the reviewer's standing rule that a rule stated in three places
  or a rejected alternative argued twice is a defect and not a style preference. No contradiction
  exists between the seven and four sites **today** — I checked them clause by clause and they agree
  on the count of three, on which door is which, and on what the identity subsumes — so nothing is
  wrong; what is wrong is the surface area of the next edit.
- **Why this timing:** `deferred`. It blocks no implementation, changes no contract, and costs nothing
  to leave; it is carried here so it reaches `## Debt` rather than being lost, and so that whoever
  edits either rule next knows there are seven sites and four sites rather than one.
- **Close criteria:**
  - [ ] The two-doors argument lives once (§INV-022 is its natural home) and §UC-036, §D.4 and §D.7
        cite it rather than restating the projector and the `MaxRead: 0` store.
  - [ ] The `Fold`-identity argument lives once (§INV-044) and §UC-041, §D.5 and §D.8 cite it.
  - [ ] The line count after the compression is recorded, as this round's note recorded 5690 → 5669.
- **Status:** closed rather than deferred. Each argument now has one normative home — the two-doors argument in **§INV-022**, the `Fold`-identity argument in **§INV-044** — and every other site is one line plus a pointer. A third claim found in the sweep, the `Binding` escape and its scope, is homed in **§INV-039**, and eleven further single-paragraph duplications were compressed to their citation. **Line count 5669 → 5569**, recorded as the third close criterion asked.

### GAP-92 [low][immediate] Five residual inaccuracies left by this round's edits, each one clause

- **Where and what:**
  1. **§D.3 line 4159** — "The frozen array itself is written by nothing and **read only by the
     cloner**". It is also read by `Append`, which passes it to the store as `Record.Payload` (§UC-063
     line 2358, §INV-021's store row). The concurrency conclusion drawn from it still holds, because
     every reader is a reader; the sentence does not. It is the same clause GAP-87 asks to be made
     true, and is listed here so it is not fixed only there.
  2. **§INV-016 lines 3011-3013 vs its control lines 3043-3047** — the rule names **six** nil routes
     (a nil interface, or a nil pointer, map, slice, **channel** or func inside one) and §D.14's two
     constructor comments (lines 5115-5119, 5127-5129) name the same six; the control asserts
     **five**, omitting the channel. The restated predicate is pinned case by case except for one case.
  3. **§UC-060's scoping list line 1477** — "a failure from `Transaction(ctx)`, `Limits()`,
     `Capabilities()` or `Backing()`, none of which issues a statement". Since GAP-84's fix, §D.14
     declares those last three "refuse nothing and issue nothing" and gives them no error return at
     all, and §D.14's door count (line 5175-5177) is four with those three excluded. Three quarters of
     that list is dead text in the paragraph an implementer reads for the fail-safe default's scope.
  4. **§D.14's map row line 5173 vs its door paragraph lines 5175-5177** — the map has a row for "a
     non-nil error from `Close`" while the paragraph immediately below defines a door as "a method
     that can hand the kernel an error" and says there are four, excluding `Close`. Both are
     defensible in isolation (`Close` returns nil by contract) and together they read as a
     miscount.
  5. **§2.1's aggregate row line 203 vs §UC-052 line 601 and §UC-067 lines 2542-2545** — "Its Go state
     type has a **usable zero value** and that zero value is the only origin of state" is stated
     unconditionally, while §UC-052 blesses `type Ledger map[string]int64`, whose zero value is a nil
     map that an unguarded fold panics on for the first event of **every** fresh stream, and §UC-067
     confirms it and refuses to recover it. The two are reconcilable — a nil map is readable, and the
     fold owns the guard — but §2.1 is where a reader learns what a state type must be, and it does
     not send them anywhere.
- **Why this severity:** `low` individually; none changes a behaviour and none is a contradiction a
  test would catch. Raised because rounds 1, 5, 6 and 7 each recorded that this document's small
  drifts are how a fix silently fails to land, and because items 1, 3 and 4 sit in what an implementer
  reads as contract.
- **Why this timing:** Each is one clause, item 1 is load-bearing for GAP-87's wording, and item 2
  changes a stated control's case list.
- **Close criteria:**
  - [ ] §D.3's sentence names every reader of the frozen array, or says only that nothing writes it.
  - [ ] §INV-016's control asserts six nil routes, or the rule and §D.14's comments name five.
  - [ ] §UC-060's scoping list names only methods that can return an error.
  - [ ] §D.14's `Close` row and its door definition agree.
  - [ ] §2.1's "usable zero value" cites §UC-052 and §UC-067, or is narrowed to what it actually
        requires.
- **Status:** closed, all five. §D.3 names both readers of the frozen array; §INV-016's control asserts six nil routes and its prose says "a six-case control"; §UC-060's scoping list names only `Transaction`; §D.14's door paragraph states that `Close` is not a door and why its map row exists; §2.1's *usable zero value* is narrowed to *readable* and cites §UC-052 and §UC-067.

---

**Checked this round and found sound** (so the absence of a finding is deliberate): GAP-79 is closed
in all three aliasing directions and the byte path from `Fact.New` to the caller's state has no
remaining hop where a codec can reach framework memory — the one unowned hop left is in the *other*
direction and is GAP-87; §UC-065's control and §INV-021's and §INV-038's falsifications all drive the
aliasing codec, and §D.9's decision to assert the kernel landing in `event`'s own tests rather than in
a per-store suite follows its own stated rule and is right; §D.13 prices the kernel clone from
arithmetic (zero on the load path, one 200 B copy per change on `Fold`, ~13 KB for an ordinary
`MaxBatch` fold) rather than calling it acceptable. GAP-80 is closed everywhere except the one
falsification clause of GAP-90, and the phantom-parameter alternative is rejected on the record with
the cost stated. GAP-82's "a key is validated at every door it crosses, produced or presented" is a
mechanism rather than a rule, §D.5's six-step order performs it, §INV-031 says it survives the
empty-append short circuit, and §INV-015's falsification is now writable. GAP-83's three fixtures are
enumerated and the suite-owned `Factory` answers the question `Factory.Begin` could not; only the
stale skip clause survives (GAP-89). GAP-84's four doors, the `errors.As` rule and the "a closed store
must not report closure from `Transaction`" clause are consistent across §D.14, §INV-045, §D.9's
`lifecycle` row, §UC-047 and §D.15's `classify` sketch, whose comment explicitly declines the
`closed()` guard in `Transaction`. GAP-85's duplication is named, priced and given a case-by-case
control. GAP-86's four items all landed. The enumeration is complete and unduplicated, both
withdrawals forward correctly, and no removed mechanism is described as live anywhere. The microkernel
walk finds no value `eventpg` cannot construct. Every symbol the document names in the tree exists
with the shape it is given, including the two the argument leans on hardest
(`jobs.EncodedPayload`'s clone-plus-full-slice and `jobs.invokeUpcaster`'s clone-before-the-callback).
And the document shrank while closing eight findings, which is the first round in which that is true.

---

## Resolution — one rule over five hand-offs, and one home per argument - 2026-09-07

Round 8 left six findings open: one `[high][immediate]`, four between `[medium]`
and `[low]` and all immediate, and one `[medium][deferred]`. **All six are
closed, none is carried to `## Debt`, and the deferred one was taken rather than
deferred** — because it is the mechanism that produced GAP-76, GAP-82, GAP-86 and
half of round 8, and a duplication that has already caused four findings is not a
style preference.

**The count. Six closed, none deferred, two alternatives rejected on the record,
one conformance defect and one residual added.** `## Deferred` still holds GAP-25,
GAP-41 and GAP-53 and gains nothing; `## Debt` still holds nine items and gains
nothing.

| # | Severity | Disposition | What did it |
|---|---|---|---|
| GAP-87 | `[high]` | **closed by making the rule total over hand-offs rather than over holders** | §INV-021 stops being *one rule with three landings* and becomes *one rule stated over five hand-offs*: **whoever hands payload bytes to another party says whether it is still a reader of that array.** The table is now total — codec→`New`, kernel→codec at a fold, **kernel→store as `Record.Payload`**, store→kernel-or-caller, kernel→codec on the load path — and each row names the recipient's freedom or the sender's copy. The obligation is the **store's promise not to write**, argued and priced below. §D.14's `Append` bullet carries it beside the other four prohibitions, `Record.Payload`'s declaration carries it as a comment, §UC-048 extends it to a forwarding decorator by name, §D.3's "read only by the cloner" is made true, and §D.9's `payload ownership` gains an **inbound** half with a control |
| GAP-88 | `[medium]` | **closed by fixing the numbering and giving cause 4 a sentinel** | §UC-041's Refusal line now maps one sentinel per cause in the list's own numbering — `ErrKey` (1), `ErrWrongStream` (2), the change's carried refusal (3), **`ErrPayload`** (4) — and its Control says *causes 1 and 2*. Cause 4 is `ErrPayload`, history class, and the class's stated meaning is restated over **a fact's recorded bytes** rather than over the store, which is what makes one decode failure need one sentinel. §D.8 and §UC-015 carry the same answer |
| GAP-89 | `[medium]` | **closed by deleting the superseded clause and scoping the word** | §UC-045's "Must not happen" now says **no defect's section may be skipped, for any reason**, and states that *not certified* is §UC-044's word about the **store under test** and is never an outcome of the self-check. §UC-044 gains a "Whose report this is" bullet saying the same from the other side, so the two mechanisms cannot be read as one |
| GAP-90 | `[medium]` | **closed by scoping the falsification and naming the residual (option a)** | §INV-020's row 2 now says the call-time refusal covers **the two crossings that pair two values**, and a new clause says what the repository crossing does instead: `Append` never reads the repository's declaration, so over one store `repoB.Append(ctx, atFromA, changesFromA...)` is *the same call* `repoA` would have made; and `repoB.Load` is correct behaviour for `repoB`, with no value in the call carrying A's family. The falsification names only the crossings that have a runtime answer and asserts the repository crossing to be what it is. §D.10 gains a **fifth** residual row. The `Repo`-carries-its-family alternative is rejected on the record in §D.12 |
| GAP-91 | `[medium]` `deferred` | **closed rather than deferred** | Each of the two arguments now has exactly one normative home, and a third claim found in the sweep has one too. Enumerated below |
| GAP-92 | `[low]` | **closed, all five** | §D.3's sentence names both readers of the frozen array; §INV-016's control asserts **six** nil routes and its prose says "a six-case control"; §UC-060's scoping list names only `Transaction`, the one other method that can return an error; §D.14's door paragraph says **`Close` is not a door** and why its row exists; §2.1's *usable zero value* is narrowed to *readable*, cites `type Ledger map[string]int64` and points at §UC-052 and §UC-067 |

### GAP-87, in full, because it decided a cost

Two shapes were available and they are not interchangeable.

**The kernel hands the store a copy.** One allocation and one copy per record, on
**every** append, on **every** store. At §D.13's worked 200 B payload and
`MaxBatch` = 64: writing 10 000 events is at least 157 appends and costs
**10 000 clones and ~2 MB copied**; 100 000 events is at least 1 563 appends and
costs **100 000 clones and ~20 MB**. It falls on the **write** path, which the
kernel's fold-side clone does not, and most of it is paid by the SQL store, which
hands the bytes straight to a driver and retains none of them — so the cost lands
exactly where the risk is not.

**The store promises not to write.** **Zero bytes, zero allocations.** The burden
lands on the party that has the contract: §D.14 is by its own opening sentence the
whole of what a store may rely on, and a promise of exactly this shape is what the
standard library states where it has the same problem (`io.Writer`: an
implementation "must not retain p" **and** "must not modify the slice data, even
temporarily"). A promise is level four of this document's own safety ladder, so it
does not travel alone: the `payload ownership` section gains an **inbound** case —
mint one fact into two identical change lists, append one, then require that
folding either gives one state and that a reload equals it — and §UC-045 gains a
**sixth** self-falsification defect, a forwarding decorator that writes into
`Record.Payload`, which must fail that section. A store that writes into the array
is therefore caught by a test rather than reproached by a sentence.

The promise is taken. Both numbers are in §D.13 and the rejected clone is a row in
§D.12.

**Is the table total?** Checked, and there is no sixth hand-off. Payload bytes
cross a party boundary at exactly five points: `Encode`'s output to `Fact.New`; a
`Change`'s frozen array to a codec at every fold; that same array to a store as
`Record.Payload`; a store's `Envelope.Payload` to the kernel or to a caller; and
the kernel forwarding that envelope payload to a codec on the load path — the
fifth is named precisely so the table is total, and its obligation is *nothing*,
which is why a replay pays zero kernel clones. The caller's own payload value is
not a sixth: it is consumed at `Fact.New` by being encoded, which is the inbound
half of the invariant's statement and not a hand-off of an array.

### GAP-88's sentinel, argued rather than picked

Cause 4 is a decode that fails when a fold runs — a codec that can encode a value
and cannot decode its own output. It is **`ErrPayload`, history class**, and the
history class's description moves from *the stored history cannot be read by this
declaration* to *a fact's recorded bytes cannot be read by this declaration*.

That is not GAP-72's class misuse repeated. GAP-72 had three specific mismatches
and none of them applies: a panicking fold means the code is wrong and the bytes
are fine, while here the bytes genuinely cannot be read by this declaration;
`ErrUpcast`'s declared wrap is the application's own error and a recovered panic
is not one, while `ErrPayload` wraps nothing; and a load with no upcaster in it
would have reported "an upcaster refused", while a decode failure reports a decode
failure. The bytes are also *the same bytes*: `Fact.New` records the fact at the
moment of decision (§UC-063), so a `Change`'s frozen array and the envelope it
later becomes are byte-identical, and all that differs between §UC-015's decode
and §UC-041's is how long ago. A new sentinel for that would be a second name for
one question, which is what §2.3's own growth rule and `architecture.md` both
refuse.

### GAP-90's choice, argued rather than assumed

The finding offered a `Repo`-side family comparison or an honest scoping. The
comparison is rejected, and the reason is that it refuses the wrong half:

- **At `Append` it would refuse a program whose data outcome is correct.** `Append`
  reads the token, the changes and the store — never the repository's declaration
  — so over one store `repoB.Append(ctx, atFromA, changesFromA...)` performs the
  identical work `repoA.Append` would have performed and lands each fact in the
  stream it was decided for. Over two backings it is already `ErrWrongStore`.
- **At `Load` it would still have nothing to compare.** The repository is the only
  party in that call that names a family; an identity carries none. So the
  crossing that produces a wrong *answer* is untouched by the check that would
  refuse the harmless one.

So §INV-020's falsification is scoped to what is writable, the repository crossing
is asserted to be what it is, and `repoB.Load` joins §D.10's inventory as a fifth
residual — the treatment this document gives a residual it cannot close.

### The DRY pass, and where each argument now lives

Three claims were argued in more than two places. Each now has exactly one
normative home; every other site is one line plus a pointer.

| The argument | Its single home | The sites reduced to a citation |
|---|---|---|
| **Two doors run the store-honesty checks** (GAP-60's) | **§INV-022**, "Where 'refused' happens" | §UC-036's Flow and its "Why `Read` validates", §UC-031's "The same refusal at `Bind` and at `Read`", §UC-055's bind cost, §D.4's "The last three of those", §D.7's "`Read` returns an error because it is a door". §D.12's rejected-alternative row stays, because that table is by design the one home for a rejection |
| **`Fold` takes the identity** (GAP-70's) | **§INV-044**, "Why the `Fold` half needs the identity" | §UC-041's "Why the fold takes an identity", §D.8's "Why the one fold takes an identity", §D.5's paragraph under the two-decision sketch. §D.12's two rows stay for the same reason as above |
| **The `Binding` escape and its scope** — found in the sweep, argued in six places | **§INV-039**, "What it cannot, both halves" and "What is offered instead" | §UC-006's one-`Binding` bullet, §UC-059's "What the framework cannot catch", §UC-054's exception, §D.4's "One `Binding` per store value", Q19 |

Eleven further single-paragraph compressions were taken where an argument's second
statement added no clause its home did not have: §D.0's identity and three-value
paragraphs (to §UC-064 and §UC-019), §UC-030's two `Authority.BoundTo` paragraphs
(to §D.12 and Q17), §UC-046's round-3 rejection (to §INV-041), §UC-016's
recovered-callback asymmetry and §INV-017's correction (to §UC-067), §UC-049's
savepoint pair (to §INV-028), §UC-056's codec cost (to §D.2), §INV-045's
door paragraphs (to §D.14), §INV-016's import argument (to §D.15), §INV-032's and
§D.14's `Close` staging shapes (to §UC-047), §INV-024's intra-class list (to
§2.3), §D.10's item 1 and its closing note (to §UC-021 and §UC-064), and the
`Q1`/`Q4`/`Q12`/`Q13`/`Q18` rows (to §D.15, §UC-040, §INV-013 and §UC-053).

**Line count: 5669 before, 5569 after — 100 lines, against round 7's 21.** That is
with GAP-87's total table, its cost paragraph, a sixth conformance defect, a fifth
§D.10 residual, a new sentinel assignment and two new §D.12 rows all added in the
same round.

### The consistency sweep

Re-read end to end. `UC-001…UC-067` and `INV-001…INV-045` each appear **exactly
once**, no number is missing, none is written twice, and no number outside either
range is cited; UC-023 and INV-037 are `WITHDRAWN` in place and never cited as
live. Every `§D.n` and every `Qn` cited resolves to a section that exists.
"exactly once" occurs **five** times and every one is a prohibition, which is what
§INV-036's own falsification asserts. Every mention of a removed mechanism —
`ErrStaleView`, `event.Replay`, `Store.Codec()`, `event.Copy`/`eventtest.Copies`,
`FailKind`, `event.Since`, `Commit.Durability`/`Positions`, `Codec.Name`, the
store-side *freeze* wording, the two-argument `Fold` — sits in a deleted or
rejected context. The word *landing* is gone from the document, replaced
throughout by *hand-off*, so no citation of §INV-021 names a structure that no
longer exists. §D.5's and §UC-021's Go snippets still compile as written, and
§D.8's unit test still declares only new variables with `:=`. The six-defect count
is consistent across §UC-037, §UC-045, §UC-048, §INV-035 and §D.9; the
three-fixture count is unchanged, because the sixth defect is a decorator.

### The zero-diff check, re-run by hand after the edits

`eventpg`'s eight methods, constructed value by value from §D.14 as round 8 leaves
it. `Capabilities` is a plain struct of four `Support` fields over exported
constants; `Limits` is a plain struct of five ints; `Backing` is
`event.NewBacking(crud.KeyOf(spec.Source))`, exported and error-returning;
`Transaction` needs `event.NewAuthority` and the invalid `event.Authority{}`, a
composite literal of an all-unexported-field struct and legal Go from any package;
`ReadStream` and `ReadAll` consume `Stream` (two exported fields), `Version` and
`Cursor` (defined types a store converts into) and return `[]Envelope` (plain
struct, exported fields); `Append` consumes `AppendRequest`/`Record` (exported
fields) and returns `event.Failure(outcome, cause)` over an exported closed enum,
or a bare `ctx.Err()`; `Close` returns a plain error. **There is no value it
cannot construct.**

Every one of round 8's six fixes is kernel-side, caller-seam-side, suite-side, or
a *removal* of a store affordance. `ErrPayload` for `Fold`'s fourth cause is a
kernel sentinel that already exists. §INV-020's scoping is caller-seam only.
§UC-044's and §UC-045's clauses are suite-side. §D.3, §UC-060 and §2.1 are prose.
And GAP-87's obligation **takes a behaviour away** from a store — it may no longer
write into a buffer it was handed — which needs no API at all; the store that
wants transformed bytes uses `bytes.Clone`, which is standard library. The sixth
self-falsification defect and the `payload ownership` inbound case are both
written in `eventtest` from exported API (`event.Store`, `event.AppendRequest`,
the declaration idiom, `Repo.Load`/`Append`, `Aggregate.Fold`). **`event` gains no
import and no symbol, and phase 2 is still writable with zero diffs under
`event/`.**

### The direction the next reduction should look

Unchanged from rounds 5, 6 and 7: the refusal vocabulary's kernel half is still
policed by a table test rather than carried by a type. Twenty-two sentinels whose
class membership is asserted rather than structural is the last place in this
design where a rule stands in for a mechanism. Everything else that was stated in
more than two places now has one home and a pointer.

---

## Round 9 - econv-usecase-validator (coverage + invariant + DX roles, post-one-rule-five-hand-offs re-audit) - 2026-09-07

Sources read: `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (whole file, **5569 lines**,
read in nine contiguous passes with no gap), `.agents/artifacts/gaps/EVENTSOURCE_P1_USECASES_GAPS.md`
(all eight rounds and all five resolutions, including this fix round's dispositions),
`docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` (the thirteen non-negotiables at lines
219-243 and the ten capability/wrapper obligations at lines 336-356, read verbatim),
`~/.claude/skills/econv/SKILL.md` and `references/{gaps,microkernel,universality}.md`, and `CLAUDE.md`.

**Framework symbols, checked against the tree and kept separate from the spec's own quality.** Every
symbol this round's edits lean on is real and correctly attributed. `jobs.DefaultPayloadBytes`
(`jobs/bounds.go:18`) is `64 << 10`, `jobs.MaxPayloadBytes` (`:19`) is `1 << 20` and
`jobs.MaxNameBytes` (`:7`) is `128` — §D.13's three derivations are exact, not approximate.
`crud.UnsafeBulkInsertFor` (`crud/executor.go:385`) and `jobs.Stager` (`jobs/queue.go:36`) exist and
carry the shapes Q5 and §UC-040 attribute to them. The one new non-tree citation this round added is
the `io.Writer` quotation in §INV-021 and §D.12; both halves of it — "must not modify the slice data,
even temporarily" and "Implementations must not retain p" — are the standard library's own words for
`io.Writer`, so the analogy is quoted accurately rather than paraphrased into something stronger.
**No symbol the document names is missing, unexported where it is used as exported, or
misattributed.** Everything below is about the specification, not about its references.

**Enumeration, counted rather than inherited.** `UC-001…UC-067` is 67 `####` headers and
`INV-001…INV-045` is 45; every number in both ranges appears **exactly once**, none is missing, none
is written twice, and no number outside either range is cited anywhere (grepped for `UC-06[89]`,
`UC-07x`, `INV-04[6-9]`, `INV-05x` — zero matches). UC-023 and INV-037 are `WITHDRAWN` in place and
every citation of either is its own withdrawal notice or the numbering paragraph; neither is cited as
live. "exactly once" occurs five times (1949, 3482, 3485, 3488, 4407) and every one is a prohibition,
which is what §INV-036's falsification asserts. §2.3's "twenty-two sentinels" is 3+7+3+4+2+3 = 22.
§D.16's "≈ 71" resolves to 13 functions + 27 types + 9 constants + 22 sentinels, and each of the four
lists has exactly the stated length (the 27 is 11 caller-seam + 16 store-seam; the 9 is `Support`'s 3
plus `Outcome`'s 6). `eventmemory` is 8 with eight names and `eventtest` is 6 with six.
§UC-045's six-defect count is four decorators plus two purpose-built stores and is consistent across
§UC-037, §UC-045, §UC-048, §INV-035 and §D.9's `self-falsification` row.

**Regression sweep over the large structural edit.** Every mention of `ErrStaleView`, `event.Replay`,
`Store.Codec()`, `event.Copy`/`eventtest.Copies`, `eventtest.FailKind`, `event.Since`,
`Commit.Durability`/`Positions`, `Codec.Name` and the store-side *freeze* wording sits in a deleted or
rejected context. The word *landing* is gone and every §INV-021 citation names a *hand-off* — with one
wrong ordinal, which is GAP-97 item 1. Every §D.n and every Qn cited resolves to a section that
exists. All fourteen compressions the fix round listed were followed to their stated home and each
home carries the argument: §UC-064 for the identity, §UC-019 for the three values, §D.12 and Q17 for
`Authority.BoundTo`, §INV-041 for the round-3 rejection, §UC-067 for the recovered-callback asymmetry,
§INV-028 for the savepoint, §D.2 for the codec cost, §D.14 for the doors, §D.15 for the imports,
§UC-047 for the two `Close` staging shapes, §2.3 for the intra-class list, §UC-021 and §UC-064 for
§D.10's item 1. Nothing was compressed into a section that does not carry it.

**GAP-87 re-checked by enumerating every hand-off myself before reading the table.** The six
crossings a payload array makes are: (1) the caller's payload value into `Fact.New`; (2) `Encode`'s
output into `Fact.New`; (3) a `Change`'s frozen array into a codec at every fold; (4) that same array
into a store as `Record.Payload`; (5) a store's `Envelope.Payload` out to the kernel or a caller;
(6) that envelope payload into a codec on the load path. §INV-021's table is (2)…(6) with (1)
explicitly disposed of as "consumed by being encoded", and for **payload arrays** that is total — I
tried and failed to construct a seventh. Each row names an owner, a reader and a writer, and the
`Append`-side obligation reaches the forwarding decorator by name (§UC-048) as well as the store
(§D.14). The suite has **both halves**: §D.9's `payload ownership` gains an inbound case whose
mechanism actually falsifies — mint one fact into two lists, append one, fold either, compare, and
compare both to a reload — with a genuine control (the two lists fold identically before the append),
and §UC-045's sixth defect is a decorator aimed at it. A store that writes into the buffer fails.
**Where the totality claim breaks is one level out from the array**, at the `[]Envelope` slice and at
the array's *lifetime* rather than the store's future reads, and that is GAP-94.

**GAP-88, GAP-89, GAP-90 and GAP-92 verified against the new text, not the disposition note.**
GAP-88: §UC-041's Refusal now maps one sentinel per cause in the list's own numbering, its Control
says "causes 1 and 2", §D.8's parallel enumeration agrees, and §UC-015 and §2.3's history row carry
the same `ErrPayload`-over-a-fact's-recorded-bytes answer. Closed. GAP-89: §UC-045's *Must not happen*
now says no defect's section may be skipped for any reason and scopes *not certified* to §UC-044's
store under test; §UC-044 gains the mirror bullet. Closed. GAP-90: I walked two aggregates over
`type Ledger map[string]int64`, families `a.ledger` and `b.ledger`, both bound through one `Binding`
to one store, and wrote §INV-020's falsification out in full. Both halves are writable exactly as
stated: over two state types the three build-failure fixtures are three distinct compile errors
(`Change[S]`, `At[S]` and the `*Repo[S, ID]` receiver each differ); over one state type the change
crossing and the token crossing both reduce to "some change's stream ≠ the token's" and are
`ErrWrongStream` with zero events, and the repository crossing is asserted to be what it is —
`repoB.Append(ctx, atFromA, changesFromA...)` is admitted and lands in A's stream, because I walked
§D.5's six steps and confirmed **none of them reads the repository's declaration**, and
`repoB.Load(ctx, anAIdentity)` is B's correct answer. §D.10's fifth residual and §D.12's rejection row
both landed. Closed. GAP-92: items 2, 3, 4 and 5 are closed as described (§INV-016's control asserts
six nil routes and its prose says "a six-case control"; §UC-060's list names only `Transaction`;
§D.14 says `Close` is not a door and why its row exists; §2.1 narrows *usable* to *readable* and
points at §UC-052 and §UC-067). **Item 1 moved the error rather than removing it** and is GAP-97
item 2.

**GAP-91 and the DRY pass — the boolean, derived by walking every site.** The **two-doors** argument
has exactly one normative home, §INV-022's "Where 'refused' happens", and its six other sites are
each one line plus a pointer (§UC-031's "The same refusal at `Bind` and at `Read`", §UC-036's Flow and
its "Why `Read` validates", §UC-055's bind cost, §D.4's "The last three of those", §D.7's "`Read`
returns an error because it is a door", §INV-043); §D.12's rejection row stays by design. The
**`Fold`-takes-the-identity** argument has exactly one home, §INV-044's "Why the `Fold` half needs the
identity", and §UC-041, §D.8, §D.5, §UC-021 and §UC-064 are one line plus a pointer each. The third
claim the sweep found, the `Binding` escape, is homed in §INV-039 with §UC-006, §UC-059, §UC-054, §D.4
and Q19 pointing at it. **`duplicationClosed: true`.** The sweep for other multiply-argued claims
found one residual — the `errors.As`-not-a-type-assertion rule, argued *in full* in two places with
the same example — which is GAP-98, and four near-misses I checked and cleared: `Within`-returns-a-
context (one four-part enumeration in §D.6, one clause elsewhere), the two-identical-changes inference
(four sites, each a single line with a pointer), the savepoint derivation (one full home in §UC-049,
clauses elsewhere) and "the reload is the authority" (four one-line statements). None of those is an
argument restated.

**Microkernel — hand-constructed from §D.14 as it now stands, not inherited.** `Capabilities()`:
`event.Capabilities{…}` over the exported `Support` constants. `Limits()`: a plain struct of five
ints. `Backing()`: `event.NewBacking(crud.KeyOf(spec.Source))`, exported and error-returning.
`Transaction()`: `event.Authority{}` — a composite literal of an all-unexported-field struct, legal Go
from any package — plus `event.NewAuthority(backing, tx)` and the store's own bare sentinel.
`ReadStream()`: consumes `Stream` (two exported fields) and `Version` (a defined `uint64`), returns
`[]event.Envelope` built by composite literal with `event.Key(k)`, `event.Version(n)`,
`event.Position(n)`; fails with `event.Failure(outcome, cause)`. `ReadAll()`: adds `event.Cursor(s)`
by conversion and `event.Failure(event.BadCursor, err)`. `Append()`: consumes `AppendRequest`/`Record`
by exported field and returns `event.Failure(…)` or a bare `ctx.Err()`; **the new obligation takes an
affordance away** — a store may no longer write into `Records[i].Payload` — and needs no API at all,
because a store that wants transformed bytes uses `bytes.Clone`. `Close()`: a plain error. **There is
no value `eventpg` must build that it cannot build from outside package `event`.** All six of this
round's dispositions are kernel-side, caller-seam-side, suite-side or a removal of a store affordance;
`event` gains no symbol and no import. Every fix I propose below is prose, a suite case, or a further
*removal* of a store affordance, so it stays true. **`microkernelPasses: true`.**

**Coverage.** All seven actors resolve to a `[happy]` use case with a matching actor line and all six
entry modes have a group. All **thirteen** roadmap non-negotiables map to a testable invariant
(append-only→INV-001, exact version→INV-002, one transaction→INV-003, pure rehydration→INV-004,
declared wire identity→INV-005, no partial rehydration→INV-006, no framework retry→INV-007, replay is
authority→INV-008, at-least-once→INV-036, no default telemetry fields→INV-011, not a CRUD
collection→INV-012, no constructor starts anything→INV-013, no start-up migration→INV-014). Of the ten
capability/wrapper obligations, 1→§UC-048's forward-exactly-once, 2 and 3→§INV-030, 4→§UC-048's
before-the-forwarded-call clause, 5→§UC-048 plus §INV-043, 6→§UC-048's replay/buffer/re-inspect
clause — now extended to in-place writes — 7→§INV-029 plus §UC-018/§UC-057, 10→§9 item 9 plus §D.16's
inventories. **8 and 9 are the only two with no stated mapping**, which is GAP-99. The edge matrix is
complete: empty (UC-009, UC-020), malformed (UC-004, UC-015), oversized (UC-017, UC-024, UC-025),
ambiguous (UC-034), duplicated (UC-035, UC-058), multilingual (UC-051's NFC/NFD and NUL), partially
failing dependency (UC-060), timeout (UC-018, UC-057), zero results (UC-009, UC-036's control),
everything-matches (UC-039), conflicting signals (UC-049, UC-057). I could construct no pair of use
cases that contradict.

**Universality, all eleven probes run.** `struct{ PlacedAt time.Time; Total int64 }`, a state that
**is** a map, two aggregates over one state type, a payload carrying a slice, a composite
non-comparable identity, zero events, 100 000 events, three retained revisions and a generated
declaration set each cost the same declaration and no extra argument, and no use case, invariant or
§D line encodes one example aggregate as a mechanism — `Applied map[string]bool`, `accounts.account`
and the transfer are all shown as the application's own answers and buy no framework feature. **Two of
the eleven fail.** *A state that is a map* is blessed by §UC-052 and §UC-067 and then handed to
`Aggregate.Fold`, where the framework's own fold loop writes through it, which is GAP-93. *A store
that is neither `eventmemory` nor a row-scan SQL store* is admitted by the payload-ownership rule and
silently corrupts a consumer, which is GAP-94 — the same second-instance test that produced GAP-87,
applied to the value beside the one GAP-87 fixed.

**Silent misuse, asked and answered.** The worst thing a **caller** can write that compiles, runs,
returns no error and is wrong is no longer §D.10 item 1: it is
`advanced, err := accounts.Fold(id, state, changes...)`, which the document's own §D.0 fold makes
mutate `state` in place, and which the document names nowhere (GAP-93). Item 1 and item 4 remain named
and controlled. The worst thing a **store implementer** can write is now named and exercised
(GAP-87's close), except for the two shapes GAP-94 covers. The worst thing a **decorator** can write
is named by §UC-048, with the wrong hand-off ordinal (GAP-97 item 1).

**Length.** **5569 lines**, measured, against 5669 before this round: −100 while closing six findings
and adding a total hand-off table, a cost paragraph, a sixth conformance defect, a fifth residual and
two rejection rows. The compression is real and is not the finding.

### GAP-93 [high][immediate] `Aggregate.Fold` writes through the caller's own state, so folding one (state, changes) pair twice gives two different answers and `advanced := Fold(id, state, …)` leaves `state` a value no event sequence produces — and the document names this mechanism everywhere except the one place the framework performs it

- **Where:** §D.1 line 3924 and §D.8 line 4413, the signature
  `func (this *Aggregate[S, ID]) Fold(id ID, state S, changes ...Change[S]) (S, error)`; §D.0's own
  fold lines 3839-3849, whose body is `this.Applied[e.Command] = true`; §UC-052 lines 610-614 ("A
  decision function that writes into `s.Applied` … mutates **the caller's own value**, which nothing
  in the framework can see and **nothing in the framework is folding**") and its residual lines
  632-636; §INV-042 line 3667 ("a decision that mutates its own state in place mutates only the
  caller's value"); §INV-021's *Inbound* clause lines 3100-3103, which enumerates the inbound values
  as "a payload passed to `Fact.New`, a byte slice" and stops; §INV-004 lines 2724-2737, whose
  checkable proxy is "determinism across two replays of one stream"; §UC-021's snippet line 1195 and
  §D.5's line 4235, both of which write `state, err = accounts.Fold(id, state, …)`; §D.10's five
  residuals lines 4660-4703.
- **What:** `Fold` takes `state S` by value, and Go copies the struct header. For every field of a
  reference kind the copy the fold receives **shares** the caller's memory, so the application's own
  fold — called by the framework, in a loop the framework owns — writes into the value the caller
  still holds. Three consequences, none of which the document states anywhere:
  1. **The input is silently consumed.** After `advanced, err := accounts.Fold(id, state, changes...)`
     the caller's `state` is a hybrid: `state.Applied` carries every change's effect and
     `state.Balance` carries none. That is precisely the "state no event sequence produces" §UC-052
     names as the residual of a caller mutating a handed-out state — reached here without the caller
     mutating anything. A caller that keeps the pre-fold value for an audit line, a comparison, a
     retry or a second branch has a value that never existed.
  2. **`Fold` is not idempotent and is not a pure function of its arguments.** `x := Fold(id, s, c)`
     followed by `y := Fold(id, s, c)` answers `x.Balance != y.Balance` for §D.0's own aggregate,
     because the duplicate-guard map was set by the first call and the second call's fold returns
     early. §INV-004 asserts folds are pure and its only proxy is two replays *through `Load`*, which
     starts from a zero value the kernel owns and therefore cannot see this at all.
  3. **For a state type that *is* a reference kind the input and the output are one value.**
     §UC-052 blesses `type Ledger map[string]int64` and §UC-067 confirms it; for that type `Fold`
     returns the map it was given, `state, err = accounts.Fold(...)` is a no-op assignment, and
     folding the same list twice double-applies every event with no error anywhere.
- **Why this severity:** `high` under `universality.md`'s second-instance test and under the user's
  standing rule that a shape which lets a caller silently do the wrong thing is wrong even when
  simpler. The mechanism is not exotic: it is §D.0's own fold, on §D.0's own state type, called
  through the document's own two-decision shape. The document has **already reasoned about this exact
  mechanism** and drawn the boundary in the one place where its reason does not hold — §UC-052's
  "nothing in the framework is folding" is true of a decision function and false of
  `Aggregate.Fold`, which is the framework folding the caller's value. Not `critical`, because the
  blessed spelling (`state, err = Fold(id, state, …)`, which both snippets use) yields the correct
  result and no history is corrupted; what is wrong is every neighbouring spelling and the purity
  claim.
- **Why this timing:** `Fold`'s parameter semantics are public contract frozen at the end of phase 1,
  and §D.10's residual inventory and §INV-021's hand-off table — the two artefacts a later reader
  trusts for "what can I still get wrong" and "who owns what memory" — are being written now. It also
  decides a test: §UC-065's falsification deliberately folds the second time onto a **fresh zero
  state**, which is exactly the shape that cannot see this, so the suite as specified would go green
  against it.
- **Close criteria:**
  - [ ] §UC-041 (or §D.8) states what `Fold` does to the state it is given: either *the input state
        is consumed and must not be used again*, or *the input is unaffected*, with the second
        answer costing a copy the document must then price and reconcile with §UC-052's deletion of
        the copier walk.
  - [ ] §INV-021's *Inbound* half names the state handed to `Fold` beside the payload handed to
        `Fact.New`, or says in terms that the invariant is about payload bytes only and points at
        wherever the state answer now lives.
  - [ ] §INV-004's purity statement or its proxy is scoped so that "two replays of one stream" is not
        read as covering `Aggregate.Fold` over a caller-supplied state, or gains the second proxy that
        does: fold one (state, changes) pair twice from two independent copies and assert the two
        results are equal.
  - [ ] §D.10 gains the residual, or the document says why it is not one.
  - [ ] A control in `event`'s own tests: fold a change list into a state holding a map and a slice,
        and assert whichever answer §UC-041 took — with the scalar-only state in the same test, so the
        case cannot pass by being true of every state type.
- **Status:** closed by naming `Fold`'s contract and enumerating the inbound direction. `Fold` applies the **caller's own folds to the caller's own value**, so it writes through every reference kind that value reaches and **the input is consumed** (§UC-041); §INV-021's inbound half becomes a **seven-row enumeration of every value a caller hands the framework**, with the state at `Fold` the one row that is not *read only*; §UC-052's "nothing in the framework is folding" is corrected; §INV-004 separates what *pure* claims from what it does not and gains a **second proxy** (fold one pair twice from two independently constructed states) that can see at `Fold` what two replays through `Load` structurally cannot; **§UC-068** is the new use case, whose control asserts the argument **is** advanced and therefore **fails if a later `Fold` clones**, with a scalar-only state beside it; §D.10 gains a sixth residual, §D.1 and §D.8 carry it on the signature, §D.9 routes the test. The copier walk was **not** resurrected — the rejection stands and is restated in §D.12 beside a new row rejecting a `Fold` that clones.

### GAP-94 [high][immediate] The payload-ownership rule is stated over the byte array and over "the store will never read it again", so a store that pools the `[]Envelope` it returns, or one whose payload bytes belong to a driver that will overwrite them, is admitted by the contract and silently rewrites a consumer's page

- **Where:** §INV-021's fourth hand-off, line 3115 ("a **store** → the kernel or a caller,
  `Envelope.Payload` | no — it clones what it retains, does not clone what it does not, and never
  reuses one buffer across two pages"); §D.14's `ReadStream` bullet lines 5180-5183 ("**Every
  `Payload` it returns is memory it will never read again**") and its `ReadAll` bullet line 5189
  ("under the same payload-ownership rule as `ReadStream`"); §D.7's `Log` interface line 4344,
  `ReadAll(ctx, after Cursor) ([]Envelope, Cursor, error)`, and `ReadStream`'s `([]Envelope, error)`
  at §D.14 line 4964; §INV-038's `Envelope` row line 3512 and its `*Reader` row line 3517 ("`Next`
  advances it and `Events` serves what the last `Next` fetched"); §D.15's `eventpg` sketch comment
  lines 5302-5304 ("payload came out of a row scan this store does not keep, so it is already memory
  nobody will read again: no clone"); §D.9's `payload ownership` row line 4592, whose outbound half
  asserts "a page's payload bytes are not reused across pages".
- **What:** The rule quantifies over the wrong thing, twice.
  1. **It covers the payload array and not the `[]Envelope` slice.** Nothing in the contract forbids
     a store from returning a slice whose backing array it reuses on the next `ReadAll`/`ReadStream`.
     A `*Reader` hands that slice to the consumer through `Events()` and §INV-038 blesses fanning it
     out to workers; the next `Next` then overwrites the envelopes the consumer is still holding —
     stream, version, position and payload header all — with no error and no way for the consumer to
     notice. `eventmemory` and a `database/sql` store both allocate a fresh slice per call, which is
     exactly why the rule reads as complete: it is written from the two stores that exist.
  2. **It is stated as a property of the store's future reads rather than of the memory's
     lifetime.** "Memory it will never read again" is satisfied by an array a *third party* will
     overwrite. That is the ordinary high-performance driver shape — a `RawValues`-style accessor
     whose byte slices are documented valid only until the next fetch, and equally an `mmap`ed page
     or a decompression scratch a codec library owns. A store author reading §D.14's "it clones what
     it retains, does not clone what it does not" concludes correctly that it retains nothing and
     therefore clones nothing, and §D.15's own sketch comment reasons in exactly those words. The
     consumer's page then changes under it between two `Next` calls. This repository already ships
     `crud/adapter/crudpgx`, so the driver whose accessor has this shape is the second one a store
     here would plausibly be built on.
- **Why this severity:** `high`, and it is GAP-87's own argument applied one value out. GAP-87 was
  `[high][immediate]` because a rule correct for the two stores that exist and undefined for a
  structurally different third is `universality.md`'s "behaviour undefined for a neighbouring input:
  the code neither handles it nor fails loudly", never deferred. Both shapes above are neighbouring
  inputs of the phase's one extension point; both produce silent wrong data in a consumer rather than
  an error; and the second of them is reached by reasoning the document's own `eventpg` sketch
  performs. It also falsifies a claim the round just made: §INV-021 says its table is total over the
  design's byte flows, and the `[]Envelope` slice is a byte flow the table does not mention while
  §INV-038 separately records that the kernel retains it across a call.
- **Why this timing:** §D.14 is frozen at the end of phase 1 and this is a clause of it. It also
  decides whether the kernel must copy a page out of the store's slice (paying `MaxRead` envelope
  copies per read, which §D.13 would have to price) or the store must promise a lifetime — the same
  choice GAP-87 made, and the same reason it had to be made before `event` was written rather than
  after a consumer's store found it in production.
- **Close criteria:**
  - [ ] §INV-021's fourth hand-off and §D.14's `ReadStream`/`ReadAll` bullets state the obligation as
        a **lifetime** — the arrays a read returns are the caller's until it drops them, and no party
        may write them — rather than as a claim about the store's own future reads, so that a store
        over a driver whose buffers are valid until the next fetch must clone.
  - [ ] The same two places state whether the returned `[]Envelope` may be reused across calls, and
        if not, say it in the same sentence as the payload rule.
  - [ ] §D.15's `eventpg` sketch comment says *why* no clone is needed there — the scan allocated —
        rather than "the store does not keep it", so the sketch does not teach the wrong predicate.
  - [ ] §D.9's `payload ownership` outbound half gains the case that catches it: hold page one, fetch
        page two, and assert page one is unchanged in both its payload bytes **and** its envelopes,
        with the single-page store as its control.
  - [ ] §UC-045 gains a defect aimed at it, or the existing "returns one reused buffer for every page"
        decorator (§INV-021 line 3168) is restated to reuse the envelope slice as well.
- **Status:** closed by re-quantifying the rule over the **recipient** and extending §INV-021's table to **seven** hand-offs, with the domain enumerated before the table: seven crossings of mutable memory, of which the store boundary contributes exactly four (`AppendRequest.Records`, `Record.Payload`, `[]Envelope`, `Envelope.Payload`) and everything else that crosses carries no writable memory. Rows 6 and 7 are **appended**, so every existing ordinal citation stays correct. The obligation is now *what a read returns belongs to whoever receives it, for as long as it keeps it and from whatever goroutine it reads it on*, and every party in the chain owes it — a store that **borrows** its bytes copies. §D.14's three struct declarations carry it as comments and its per-method bullets restate it; §D.15's `eventpg` comment now says *the scan allocated* rather than *the store does not keep it*; §D.9's `payload ownership` gains the hold-page-one case; §INV-038 gains a `[]Envelope` row; §D.7 tells A4 the page is its own; §D.13 prices rows 6 and 7 at zero for every store that allocates per call; §D.12 gains the rejection row.

### GAP-95 [medium][immediate] The sixth self-falsification defect writes into `Record.Payload` **before forwarding**, so it corrupts the store's own history and fails every section that decodes — which falsifies §UC-045's own "at least one named section and no unrelated section"

- **Where:** §UC-045's defect table line 2607 ("**Writes into an `AppendRequest`'s `Record.Payload`
  **before forwarding it** — the redacting or compressing wrapper §INV-021's third hand-off forbids |
  a forwarding decorator, rewriting the **input** | `payload ownership`"); §INV-021's falsification
  lines 3174-3178, whose fixture "writes into **every byte** of every `Record.Payload` it is handed";
  §UC-045's Flow line 2600 ("The suite runs each section against six deliberately defective stores,
  one at a time, and asserts each fails the section named for it") and its claim lines 2656-2659
  ("Each defect fails **at least one named section and no unrelated section**"); §D.9's `stream
  paging` row line 4588 ("one stream folded at `StreamPage` and at 1 gives identical state and
  version") and `expected version` row line 4584, both of which decode.
- **What:** A decorator that overwrites the array **before** forwarding hands the store the mutated
  bytes, so the store persists them. Every later section that decodes the history then fails: `stream
  paging` folds the stream twice, `expected version`'s control loads the two-instance transfer's
  streams, and any section whose control reloads a state gets a decode failure or a wrong value. That
  is a defect failing a large set of unrelated sections, which is exactly what §UC-045's relaxed
  claim says no defect does — and GAP-25, the deferred item the relaxation exists for, is scoped to
  one defect failing one *adjacent* section, not to one defect breaking every decoding section in the
  suite. The fixture that isolates the property is the mirror one: write into the forwarded array
  **after** the store has read it, so the store's history is correct and only the caller's `Change`
  is corrupted — at which point §D.9's inbound case (fold the appended list, fold the untouched list,
  compare both to a reload) is the only thing that fails, which is what the defect is for.
  "Not even temporarily" makes write-then-restore forbidden too, so the fixture cannot restore.
- **Why this severity:** `medium`. Nothing is unsafe and the obligation is correct; what is wrong is
  the fixture, and an implementer who builds it as written gets a self-falsification run that is red
  in five places and cannot tell which failure is the evidence. It also quietly weakens the suite's
  one anti-rot mechanism, because the natural fix an implementer reaches for is to stop running the
  other sections against that defect — which is the skip path GAP-83 and GAP-89 both closed.
- **Why this timing:** The self-falsification run's dispatch is being specified now, and this is the
  one defect of the six whose fixture semantics decide whether the "no unrelated section" half of the
  claim is testable at all.
- **Close criteria:**
  - [ ] §UC-045's fourth defect row says the write happens **after** the forwarded call returns (or
        otherwise specifies a mutation that leaves the store's persisted bytes correct), and §INV-021's
        falsification fixture matches it.
  - [ ] §UC-045's "at least one named section and no unrelated section" is checked against all six
        defects as now specified, and any defect for which it is false joins GAP-25 in §8 by name
        rather than being covered by silence.
- **Status:** closed. §UC-045's fourth defect writes into `Record.Payload` **after the forwarded call returns**, so the store's history stays correct and only the caller's `Change` is corrupted — which is exactly what §D.9's inbound case measures. §INV-021's falsification fixture matches, and a new bullet says why, naming the red-in-five-places run the other order produces. The second criterion was checked against all six defects and **the claim was false**: §UC-045 now ships *each defect fails the section named for it*, with the three defects that also fail other sections listed by name, and §8's GAP-25 entry records that round 9's finding proved the first relaxation still too strong.

### GAP-96 [medium][immediate] `Fold`'s fourth cause fires mid-list, and neither what `Fold` returns on an error nor what state the caller is left holding is stated anywhere — while `Load`'s equivalent is pinned by its own invariant

- **Where:** §UC-041's four causes lines 2161-2188 and its Refusal line 2189-2192; §D.8's parallel
  enumeration lines 4435-4446; §D.5's `Append` order lines 4209-4216, which §D.8 says `Fold` follows;
  §INV-006 lines 2758-2765, which pins the same question for `Load` ("the caller receives the **zero**
  state and a zero at-token, never the accumulator"); §UC-010's "the **zero** state is returned with
  the error, never the accumulator" line 904.
- **What:** Causes 1, 2 and 3 are pre-checks over the whole list — the key, then every change's
  stream, then every change's carried refusal — but **cause 4, a decode that fails when the fold
  runs, can only fire on change *k* of *n***, after *k−1* folds have already run. The document says
  nothing about what `Fold` returns then. `Load`'s answer (the zero value) is wrong here because the
  caller supplied the state; returning the input is not available if any earlier fold wrote through a
  reference-kind field (GAP-93); and returning the partial accumulator is what §INV-006 forbids for
  the other fold. All three are defensible and none is written, so two implementations will differ
  and the conformance suite has nothing to assert — §D.9 places the `Fold` crossing in `event`'s own
  tests, and that test has to name a return value.
- **Why this severity:** `medium` rather than `high`: cause 4 is a broken codec, `eventtest.RoundTrip`
  is the proxy that catches it before it reaches a fold, and every candidate answer refuses rather
  than corrupting. What makes it a defect is that `Fold` is the one public function with a documented
  error contract and no documented value contract, on a document whose §INV-040 exists precisely so
  that "every outcome puts the caller in exactly one row".
- **Why this timing:** It is one clause and it is decided together with GAP-93's answer — if `Fold`
  is stated to consume its input, the error path's answer follows from it; if it is stated to leave
  the input alone, the error path can honestly return the input.
- **Close criteria:**
  - [ ] §UC-041 states what `Fold` returns for each of its four causes, and in particular what it
        returns when cause 4 fires after *k* successful folds.
  - [ ] The answer is consistent with §INV-006's rule for `Load` or says why the two differ.
  - [ ] §D.9's "asserted in `event`'s own tests" list names the return-value assertion beside the
        sentinel assertion.
- **Status:** closed. §UC-041 states what `Fold` returns for each cause — causes 1–3 are pre-checks and return **the input untouched**, cause 4 fires on change *k* and returns **the state as of the last change applied**, and on any error the state is not usable — with a bullet on why that is not §INV-006's answer. §INV-006's title and a new scope clause say it is stated over `Load` and only over `Load`. §UC-041's Control gains a cause-4-at-change-2-of-3 case asserting both the sentinel and the returned state, and §D.9's "asserted in `event`'s own tests" list names it as its own property, renumbered to seven.

### GAP-97 [low][immediate] Six one-clause residuals from this round's edits, three of them in what an implementer reads as contract

- **Where and what:**
  1. **§UC-048 line 2064** — the decorator's *Must not happen* forbids writing into an
     `AppendRequest`'s `Payload` and cites "§INV-021's **fourth** hand-off". That obligation is
     §INV-021's **third** row (kernel → store); the fourth is store → kernel-or-caller. Every other
     citation of it is correct — §UC-045 line 2607, §UC-063 line 2289, §INV-021's own falsification
     line 3174, §D.13 line 4861, §D.14 line 5211 all say *third* — so §UC-048 is the single wrong
     pointer, and it sits in the one paragraph a decorator author reads.
  2. **§D.3 lines 4072-4074** — "The frozen array itself is written by nobody and **read by exactly
     two parties**: the cloner in `Fold`, and `Append`, which hands it to the store". The store, and
     any decorator forwarding the request, is a third reader — that is the entire content of the third
     hand-off. GAP-92 item 1 moved this sentence from one party to two and left the same class of
     error; the concurrency conclusion drawn from it still holds, because every party is a reader,
     but the count does not.
  3. **§D.10 line 4646** — "**The one hand-off whose sender is still a reader**". Three of the five
     have a sender that is still a reader (rows 1, 2 and 3); the property that distinguishes row 3 is
     the one §INV-021 line 3114 states — it is the one *answered by a promise instead of a copy*.
  4. **§D.13 line 4870 and §INV-021 line 3125** — "The promise costs **zero bytes and zero
     allocations**" / "The promise costs nothing". It costs zero for a store that retains nothing,
     and for a store that retains it costs exactly the clone the kernel-side alternative would have
     paid — which §D.14's own `Append` bullet requires ("if it keeps it beyond the call it
     **clones**"). That store is `eventmemory`, the one every unit test in the tree will use. The
     comparison still wins, because under the alternative the SQL store pays too; the number is
     wrong, in the section that exists because `restrictions.md` asks a cost to be derived rather
     than asserted.
  5. **§UC-006 line 736** — "**The store constructor returns an error** and never panics: it carries
     deployment values a deployment can get wrong, and E2's failure shape for those is a returned
     error. **The rule is stated here once**" — and §D.4 line 4126 states the identical
     justification in the same words ("a store's spec carries deployment values a deployment can get
     wrong, and E2's failure shape for those is a returned error, never a panic"). One of the two is
     the home; the sentence claiming to be it is not obviously the one.
  6. **§D.9's `lifecycle` row line 4595** — "an operation after close is `ErrClosed` — including a
     `Load`, **whose first act is `Transaction(ctx)`**", against §UC-051 line 986, which requires the
     key to be validated "before any statement and **before the store is reached**" at `Load`.
     `Transaction(ctx)` is a method on the store. The two orderings disagree about which sentinel a
     `Load` with a zero identity on a closed store produces; neither test as specified constructs
     that input, so nothing catches it.
- **Why this severity:** `low` individually — none changes a behaviour, and items 2, 3 and 4 draw
  conclusions that survive their own inaccuracy. Raised because item 1 is a pointer into a table an
  implementer follows, items 3 and 4 sit in the two sections that carry this round's central
  decision, and rounds 1, 5, 6, 7 and 8 each recorded that this document's small drifts are how a fix
  silently fails to land.
- **Why this timing:** Each is one clause, item 1 is a wrong cross-reference in a normative
  prohibition, and item 4 is a number in the table that justifies choosing the promise over the
  clone.
- **Close criteria:**
  - [ ] §UC-048 says *third* hand-off.
  - [ ] §D.3 names every reader of the frozen array, or drops the count and says only that nothing
        writes it.
  - [ ] §D.10's row names the property that actually distinguishes row 3.
  - [ ] §D.13 and §INV-021 price the promise for a retaining store as well as for a non-retaining
        one, or scope the "zero" to the store that does not retain.
  - [ ] One of §UC-006 and §D.4 carries the argument and the other cites it; the one that claims
        "stated here once" is the one that carries it.
  - [ ] §D.9's `lifecycle` row and §UC-051 agree about what `Load` does first.
- **Status:** closed, all six. (1) §UC-048 says *third* hand-off, and gains the `Records` slice beside it. (2) §D.3 drops the count and names every reader of the frozen array. (3) §D.10's row names the property that distinguishes row 3 — *answered by a promise instead of a copy*. (4) §D.13 and §INV-021 price the promise per store: zero for one that does not retain, exactly the kernel-side clone for one that does, which is `eventmemory`. (5) §UC-006 carries the constructor argument and §D.4 cites it. (6) §D.5 gains **`Load`'s order** beside `Append`'s — key first, then `Transaction(ctx)`, then the paged read — so a `Load` with a zero identity on a closed store is `ErrKey`; §D.9's `lifecycle` row and §UC-051 now agree and both cite it.

### GAP-98 [medium][deferred] The `errors.As`-not-a-type-assertion rule is argued in full in two places with the same example, which the DRY pass's own standard makes a defect

- **Where:** §INV-045 lines 3763-3769 ("The kernel finds it with **`errors.As`**, so a decorator that
  forwards and wraps keeps the classification; a type assertion would let a wrapped
  `Failure(BadCursor, …)` take a door's default and become `ErrBackend` — one class converted into
  another **by the kernel**") and §D.14 lines 5102-5109 ("**The kernel finds the outcome through
  wrapping, not by a type assertion.** … A type assertion would make a wrapped `Failure(BadCursor, …)`
  take a door's fail-safe default and become `ErrBackend`: one class converted into another **by the
  kernel**"). §UC-048 lines 2069-2074 is the third site and is correctly one line plus a pointer.
- **What:** Two full statements of one argument, with the same worked example, the same conclusion and
  the same cross-references, eight hundred lines apart. GAP-91's fix established the standard — one
  normative home, every other site one line plus a pointer — and closed the two largest violations of
  it; this is the largest remaining one and was inside the sweep's stated scope ("a third claim found
  in the sweep"). It is smaller than either of GAP-91's (two sites rather than seven and four), and
  the two agree clause by clause today, so nothing is wrong; what is wrong is that the next edit to
  the rule has two places to land and the round's own history says that is how GAP-76, GAP-82, GAP-86
  and half of round 8 happened.
- **Why this severity:** `medium` under the standing rule that a rule argued in full twice is a defect
  and not a style preference. No contradiction exists between the two today — I checked them clause by
  clause and they agree on the mechanism, the example and the conclusion.
- **Why this timing:** `deferred`. It blocks no implementation, changes no contract and costs nothing
  to leave. Carried so it reaches `## Debt` rather than being lost, and so that whoever edits the rule
  next knows there are two homes.
- **Close criteria:**
  - [ ] The argument lives once — §D.14 is its natural home, beside the map it is about — and §INV-045
        states the rule in one clause and cites it.
  - [ ] The line count after the compression is recorded, as rounds 8 and 9 recorded 5690 → 5669 →
        5569.
- **Status:** closed rather than deferred. The argument lives once, in **§D.14** beside the map it is about; §INV-045 states the rule in one clause and cites it. Line count recorded: **5569 → 5567**.

### GAP-99 [low][deferred] Two of [ES]'s ten capability/wrapper obligations have no stated mapping, and the reason they are vacuous lives only in a previous round's audit note

- **Where:** [ES] lines 351-354, obligations 8 (`port.Service` restore, preserved as
  `port.RestorableOf` discovers it) and 9 (`storage.Store` capabilities forwarded exactly, `Open`
  stream ownership preserved); the document cites obligations 3, 4, 7 and 10 by number (lines 1117,
  2060, 2068, 2086, 3387, 5105, 5564) and covers 1, 2, 5 and 6 by content, and names 8 and 9 nowhere;
  §1's non-goal list lines 132-192, which does not name a `port.Service` or `storage.Store` adapter
  among its twenty refusals.
- **What:** Both obligations are conditioned on wrappers phase 1 does not build, so both are vacuous
  — but that judgement is recorded in round 8's audit note and not in the document, and [ES]'s own
  wording is "Any event adapter or future event-specific chain must satisfy **all of these** before
  release". A later reader checking the phase against [ES] finds two obligations with no home and no
  statement that they were considered, which is the situation §1's non-goal list exists to prevent
  ("Each is named so that a later reader can see it was decided rather than forgotten").
- **Why this severity:** `low`. Nothing is unsafe and the coverage is genuinely complete; what is
  missing is one sentence saying so.
- **Why this timing:** `deferred`. It affects no contract and no plan section; it is a completeness
  note for whoever writes the decision doc Q15 owes.
- **Close criteria:**
  - [ ] §1's non-goals or §D.16's obligation walk names [ES] obligations 8 and 9 and says they are
        vacuous for phase 1 because no `port.Service` and no `storage.Store` adapter is built, and
        what would reopen them.
- **Status:** closed rather than deferred. **Non-goal 21** names the two wrappers phase 1 does not build — a `port.Service` adapter and a `storage.Store` adapter — says [ES]'s obligations 8 and 9 are therefore **vacuous rather than unmet**, and says what would reopen them. §D.16 gains a paragraph mapping **all ten** obligations to their homes, so no later reader finds two with no statement that they were considered.

---

**Checked this round and found sound** (so the absence of a finding is deliberate). GAP-87's close is
correct in its own terms: I enumerated the six payload crossings before reading the table, and for
byte arrays §INV-021's five rows plus the disposed-of caller payload are total — I could not construct
a seventh. The choice of the store's promise over a kernel clone is argued from arithmetic rather than
asserted, the rejected clone is a §D.12 row, the obligation reaches the forwarding decorator by name,
and the conformance case that exercises it has a mechanism that genuinely falsifies plus a
non-vacuous control. GAP-88's `ErrPayload` assignment is argued rather than picked and the history
class's restatement over *a fact's recorded bytes* is consistent in §2.3, §UC-015, §UC-041 and §D.8.
GAP-89's two clauses are consistent from both sides. GAP-90's falsification is writable in full, and I
verified by walking §D.5's six steps that `Append` genuinely never reads the repository's declaration,
which is what makes the scoping honest rather than convenient. GAP-91's two arguments each have one
home and six and five pointers, and the `Binding` escape has one home and five. The enumeration is
complete and unduplicated, both withdrawals forward correctly, no removed mechanism is described as
live, every §D.n and Qn resolves, §D.5's and §UC-021's Go snippets still compile as written (`at` and
`err` on the right-hand side of the first line, so `=` on the first and last and `:=` only where
`second` is introduced), and §D.8's unit test declares only new variables with `:=`. The microkernel
walk finds no value `eventpg` cannot construct, and every fix this round proposes leaves that true.
The three numbers §D.13 derives from `jobs` are exact against `jobs/bounds.go`. The document shrank by
100 lines while closing six findings.

### The direction the next reduction should look

Two of this round's four substantive findings — GAP-93 and GAP-94 — are the same shape and it is
worth naming: **the document's ownership rules are stated over the values the two existing
implementations happen to pass, and both holes are one value out from a value the rule does name.**
§INV-021 names the payload array and not the `[]Envelope` that carries it; the inbound half names the
payload handed to `Fact.New` and not the state handed to `Fold`. GAP-79, GAP-87 and now these two are
four instances of one pattern in four consecutive rounds, and each was found by asking *who else
touches this memory* rather than by reading the rule. The rule's next revision should be quantified
over **every value that crosses a party boundary**, not over the ones the design's two stores and one
codec happen to move, and its falsification should be a table with one row per crossing rather than a
list of cases.

---

## Resolution — two enumerations, and every rule made total over one - 2026-09-07

Round 9 left seven findings open: two `[high][immediate]`, three between
`[medium]` and `[low]` and immediate, and two `[deferred]`. **All seven are
closed, none is carried to `## Debt`, and both deferred ones were taken rather
than deferred.**

Round 9's own closing note named the pattern and prescribed the method, and this
round applied it literally: **write the complete domain first, make the rule
total over it, and leave the enumeration in the invariant's own section so every
later rule can quantify over it by name.** Both `[high]`s were the same shape —
an ownership rule stated over the values the two stores and one codec that exist
happen to move — so both are closed by an enumeration rather than by a clause.

**The count. Seven closed, none deferred, three alternatives rejected on the
record, one use case, two enumerations, one conformance case and one residual
added.** `## Deferred` still holds GAP-25, GAP-41 and GAP-53; GAP-25's own
statement is corrected because round 9's finding proved the relaxation it shipped
was still too strong. `## Debt` still holds nine items and gains nothing.

| # | Severity | Disposition | What did it |
|---|---|---|---|
| GAP-93 | `[high]` | **closed by an enumeration plus a stated contract, with a control that fails if a later `Fold` clones** | The answer taken is the one the finding predicted: `Fold` applies the **caller's own folds to the caller's own value**, in the loop the caller would otherwise have written, so it writes through every reference kind that value reaches. **The input is consumed.** §UC-041 carries the contract and why it is not a copy; §INV-021's inbound half becomes a **seven-row enumeration of every value a caller hands the framework**, of which the state at `Fold` is the one row whose answer is not *read only*; §UC-052's false blessing (*nothing in the framework is folding*) is corrected in place; §INV-004 is rewritten to say what *pure* claims and what it does not, and gains a **second proxy**; §UC-068 is the new use case with the `Ledger` no-op-assignment case and a control that **fails if `Fold` ever clones**; §D.10 gains a sixth residual; §D.1 and §D.8 carry it on the signature; §D.5, §UC-021 and §D.9 point at it |
| GAP-94 | `[high]` | **closed by re-quantifying the rule over the recipient and extending the table to seven hand-offs** | §INV-021's outbound table gains **row 6** (`AppendRequest.Records`, the slice) and **row 7** (`[]Envelope`, the page), and its rule becomes *whoever hands mutable memory over says whether **anybody** will write it again*, quantified over **what the recipient may do**: keep it indefinitely, read it from any goroutine, write into it. The domain is enumerated before the table — seven crossings, of which **the store boundary contributes exactly four** — and everything else that crosses is named as carrying no writable memory. §D.14's `Envelope`, `Record` and `AppendRequest` declarations carry it as comments, its `ReadStream`/`ReadAll`/`Append` bullets restate it per method, §D.15's `eventpg` comment is corrected to say *the scan allocated* rather than *the store does not keep it*, §D.9's `payload ownership` gains the hold-page-one case, §INV-038 gains a `[]Envelope` row, §D.7 tells A4 the page is its own, §D.13 prices rows 6 and 7, and §D.12 gains the rejection row |
| GAP-95 | `[medium]` | **closed by moving the write to after the forwarded call** | §UC-045's fourth defect now writes into `Record.Payload` **after the forwarded call returns**, with a bullet saying why: before it, the store persists the mutated bytes and every decoding section goes red, which is a run that cannot say which failure is the evidence. §INV-021's falsification fixture matches. "Not even temporarily" still forbids write-then-restore, so the fixture does not restore |
| GAP-96 | `[medium]` | **closed by stating the value contract and scoping §INV-006** | §UC-041 now says what `Fold` returns for each cause: causes 1–3 are pre-checks and return **the input untouched**; cause 4 fires on change *k* and returns **the state as of the last change applied**; on any error the state is not usable and a reload is the authority. §INV-006's title and a new scope clause say it is stated over `Load` and only over `Load`, and why the two differ. §UC-041's Control gains the mid-list case, and §D.9's "asserted in `event`'s own tests" list names the return-value assertion |
| GAP-97 | `[low]` | **closed, all six** | (1) §UC-048 says *third* hand-off. (2) §D.3 drops the count and names every reader of the frozen array, the store and a forwarding decorator included. (3) §D.10's row names the property that actually distinguishes row 3 — *answered by a promise instead of a copy*. (4) §D.13 and §INV-021 price the promise **per store**: zero for one that does not retain, exactly the kernel-side clone for one that does, which is `eventmemory`. (5) §UC-006 keeps the constructor argument and §D.4 cites it. (6) §D.5 gains **`Load`'s order** beside `Append`'s, so a `Load` with a zero identity on a closed store is `ErrKey`; §D.9's `lifecycle` row and §UC-051 now agree and both point at it |
| GAP-98 | `[medium]` `deferred` | **closed rather than deferred** | §D.14 keeps the `errors.As`-not-a-type-assertion argument, beside the map it is about; §INV-045 states the rule in one clause and cites it. Line count recorded below |
| GAP-99 | `[low]` `deferred` | **closed rather than deferred** | [ES]'s obligations **8** and **9** are recorded **in the document**: non-goal 21 names the two wrappers phase 1 does not build, says both obligations are therefore *vacuous rather than unmet*, and says what would reopen them; §D.16 gains a one-paragraph walk mapping **all ten** obligations to their homes |

### GAP-93's answer, argued rather than assumed

Three shapes were available.

**A deep copy of `S`.** That is the transitive copier walk round 4 deleted, whose
rejection stands and was not resurrected: it refused `struct{ PlacedAt time.Time }`,
its escape hatch permanently disarmed it, and its proxy could not falsify it.

**A shallow copy.** Go already performs one at the call. It is not a fix — it is
precisely the mechanism that produces the hybrid the finding describes, a map
advanced and the scalars not.

**A stated contract with a runnable control.** Taken. `Fold` is the loop the
caller would have written, and a `Fold` that behaved differently from that loop
is the one thing a pure seam may not do; a fold that mutates a large state in
place is legitimate, and `type Ledger map[string]int64` — which §UC-052 blesses —
has no other kind of fold. So the input is **consumed**, both blessed spellings
already assign back, and §UC-068's control asserts the argument **is** advanced,
so a later `Fold` that started cloning **fails a test** rather than quietly
changing a frozen contract. The scalar-only state in the same test is what stops
the case passing by being true of every state type.

**§INV-004's proxy is now able to see a violation — and is honest about which
one.** Proxy (1), two replays through `Load`, pins the fold at the kernel-owned
zero value and structurally cannot reach `Aggregate.Fold`. Proxy (2) — one
(state, changes) pair folded twice **from two independently constructed states** —
pins the same determinism where the origin is the caller's. The invariant now says
in terms that *pure* claims the **result** is a function of the two arguments and
does **not** claim the `S` argument is left alone, and warns that proxy (2) must
never be written by folding one value twice, because that asks §UC-068's question
and would report the contract as a violation.

### GAP-94's choice, argued rather than assumed

The finding offered a kernel-side copy of the page or a store-side lifetime
promise, and named the same trade GAP-87 made.

**The kernel copies the page out.** `MaxRead` envelope headers plus one slice per
read, on every read, on every store — paid by the ordinary store that already
allocates a fresh slice out of a row scan, which is every store this document
contemplates. It also cannot help the payload arrays without copying those too,
at which point a replay stops paying zero kernel clones.

**The store promises a lifetime.** Taken, and quantified over the **recipient**
rather than the sender, which is the whole content of the fix: *memory the store
will never read again* is a claim about the sender's future and is satisfied by an
array a third party overwrites. So the obligation is **what a read returns belongs
to whoever receives it, for as long as it keeps it and from whatever goroutine it
reads it on**, and every party in the chain owes it — a store that borrows its
bytes from a driver accessor, an `mmap`ed page or a library scratch **copies
before returning them**. It costs zero for every store that allocates per call and
falls only on the store that chose to pool or to borrow. `crud/adapter/crudpgx` is
already in this repository, so §D.15's sketch comment is corrected to teach the
right predicate rather than the wrong one.

### GAP-95's second criterion, checked and answered honestly

§UC-045's claim was checked against all six defects as now specified, and it does
not hold. **Neither *exactly one section* nor *at least one and no unrelated
section* is true.** The admit-everything store also fails `concurrency`; the
short-page decorator also fails `dense versions` and `conservation`, because a
truncated stream read is a smaller set than the global read and a load through it
returns a truncated state; the position-reusing decorator fails whatever else
compares positions. So §UC-045 now ships the claim the suite **can** hold — *each
defect fails the section named for it* — with the three exceptions listed there by
name, and §8's GAP-25 entry is rewritten to record that round 9's finding proved
the first relaxation still too strong. §INV-034's conservation set is pinned to
`(Stream, Version)` in the same pass, so that section has a definition rather than
an implication.

### The consistency sweep

Re-read end to end. `UC-001…UC-068` is 68 `####` headers and `INV-001…INV-045` is
45; every number in both ranges appears **exactly once**, none is missing, none is
written twice, and no number outside either range is cited (grepped). UC-023 and
INV-037 are `WITHDRAWN` in place and never cited as live. Every `§UC-nnn` and
`§INV-nnn` citation in the document was resolved mechanically against the header
list — **zero dangling**. Every `§D.n` and every `Qn` cited resolves to a section
that exists (17 and 21 respectively, both complete). "exactly once" occurs
**five** times and every one is a prohibition, which is what §INV-036's own
falsification asserts. Every mention of a removed mechanism — `ErrStaleView`,
`event.Replay`, `Store.Codec()`, `event.Copy`/`eventtest.Copies`, `FailKind`,
`event.Since`, `Commit.Durability`/`Positions`, `Authority.BoundTo`, `ErrBatch`,
the store-side *freeze* wording — sits in a deleted or rejected context.

**Every index was re-checked after the renumbering, because that is the class the
round warned about.** §INV-021's outbound table gained two rows and they were
**appended, not inserted**, so ordinals 1–5 keep their meaning and all seventeen
citations of *third*/*fourth*/*second*/`INV-021.n` elsewhere stayed correct; the
table now says in terms that its numbering is stable and cited by ordinal. Three
index errors were found and fixed by this sweep: §INV-038's `*Reader` row pointed
at "the row above" for the page, which after the new `[]Envelope` row is no longer
the row above (now named); §INV-021's inbound summary said "six of the seven rows
are read-only" against a table with four read-only rows, two retentions and one
written-through (now four/two/one); and §INV-038's "why every row says free" was
false of four rows that say "copy the pointer" (reworded). §D.10's residual list
is 1–6 and both citations of it (§D.12's *fifth*, three sites' *sixth*) point at
the right item. The six-defect count is consistent across §UC-037, §UC-045,
§UC-048, §INV-035 and §D.9; the three-fixture count is unchanged, because the
sixth defect is still a decorator. §D.9's not-a-section list was recounted and is
now **seven**, numbered, because the `Fold` return value is a seventh property and
was being carried as a clause of the third.

### The zero-diff check, re-run by hand after the edits

`eventpg`'s eight methods, constructed value by value from §D.14 as this round
leaves it. `Capabilities()`: a plain struct of four `Support` fields over exported
constants. `Limits()`: a plain struct of five ints. `Backing()`:
`event.NewBacking(crud.KeyOf(spec.Source))`, exported and error-returning.
`Transaction()`: `event.Authority{}` — a composite literal of an
all-unexported-field struct, legal Go from any package — plus
`event.NewAuthority(backing, tx)` and the store's own bare sentinel.
`ReadStream()`: consumes `Stream` (two exported fields) and `Version` (a defined
`uint64`), returns `[]event.Envelope` built by composite literal with
`event.Key(k)`, `event.Version(n)`, `event.Position(n)`; fails with
`event.Failure(outcome, cause)`. `ReadAll()`: adds `event.Cursor(s)` by conversion
and `event.Failure(event.BadCursor, err)`. `Append()`: consumes
`AppendRequest`/`Record` by exported field and returns `event.Failure(…)` or a bare
`ctx.Err()`. `Close()`: a plain error. **There is no value `eventpg` must build
that it cannot build from outside package `event`.**

**And this round's two new obligations take affordances away rather than adding
API.** Hand-offs 6 and 7 forbid a store to pool a `[]Envelope`, to retain a
`Records` slice, or to hand out memory it does not own — satisfied by an ordinary
per-call `make`/`append` and by `bytes.Clone`, both standard library, and already
what `eventmemory` and any `database/sql` store do. `Fold`'s consumption contract,
§INV-006's scoping, §INV-004's second proxy, §UC-068 and the corrected
self-falsification fixture are kernel-side, caller-seam-side or suite-side, all
written from exported API. **`event` gains no symbol and no import, and phase 2 is
still writable with zero diffs under `event/`.**

### Line count

**5569 before, 5567 after.** Two enumerations, a use case, a conformance case, a
residual, three rejection rows and a §D.16 obligation walk were added and paid for
by compression, without deleting an argument: every DRY reduction moved a claim to
a cited home rather than dropping it, and round-by-round archaeology that the GAPS
file already records was removed in preference to anything normative.

### The direction the next reduction should look

Unchanged from rounds 5 to 9 in its subject and now the only one left: the refusal
vocabulary's kernel half is still policed by a table test rather than carried by a
type. Twenty-two sentinels whose class membership is asserted rather than
structural is the last place in this design where a rule stands in for a
mechanism. The ownership rules are no longer in that category — both directions
are now enumerations a reader can check for totality, which is what rounds 5 to 9
each tried to reach one value at a time.

---

## Round 10 - econv-usecase-validator (coverage + invariant + DX roles, post-two-enumerations re-audit) - 2026-09-07

Sources read: `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` (whole file, **5567 lines**,
read in eight contiguous passes with no gap), `.agents/artifacts/gaps/EVENTSOURCE_P1_USECASES_GAPS.md`
(round 9 in full plus this fix round's dispositions, and every earlier round's finding titles and the
five resolutions), `docs/roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md` (the thirteen
non-negotiables at 219-243 and the ten obligations at 336-356, read verbatim),
`~/.claude/skills/econv/SKILL.md` and `references/{gaps,microkernel,universality}.md`, `CLAUDE.md`.
No `.go` file was read to judge a design; the tree was read only to check the spec's citations.

**Framework symbols, checked against the tree and kept separate from the spec's own quality.**
`crud.SameDataSource` (`crud/executor.go:562`) has exactly the four-step shape §INV-016 attributes to
it — nil→false, type mismatch→false, non-comparable→false, then `a == b` — and answers **true** for a
pair of typed nils, which is precisely what §INV-016's GAP-78 clause says and why `NewBacking`
restates the predicate. `crud.KeyOf` (`:524`), `crud.ExecutorFor` (`:370`), `crud.WithExecutorFor`
(`:341`), `crud.IsTransaction` (`:38`), `crud.UnsafeBulkInsertFor` (`:385`), `crudsql.Transaction`
(`crud/adapter/crudsql/crudsql.go:195`) and `crudsql.TransactionFor` (`:210`) all exist, are exported
and carry the shapes the document gives them — including §D.6's claim that only the two-step form
tells *nothing bound* from *bound but not a transaction*, which `crudsql.Transaction(executor)
(*sql.Tx, bool)` and `crud.ExecutorFor(ctx, source) (Executor, bool)` make exactly true.
`crud.ErrConflict` and `crud.ErrUnavailable` are exported sentinels (`crud/errors.go:32,38`) and
`errs.KindTooLarge` plus `errs.Fault` exist (`errs/code.go:58`), so §D.15's import table is real.
`crud`'s `isNilValue` is genuinely unexported (`crud/executor.go:39,211,245`), so §INV-016's
duplication argument is honest. `sql.RawBytes` and a `RawValues`-shaped pgx accessor are correctly
characterised in §D.15's corrected comment. **No symbol the document names is missing, unexported
where it is used as exported, or misattributed.** Everything below is about the specification.

**Enumeration 1 — the outbound hand-offs. NOT total. One member constructed, and it is the one
whose absence corrupts.** I enumerated the crossings myself before reading the table, by asking
*which parties exist* (caller, kernel, codec, store, decorator, suite) and *what mutable memory
passes between each ordered pair*. The table's seven are right and each is answered. The crossing it
does not contain is **codec → kernel → the application's fold: the value `Decode` returns**. It is
the exact mirror of row 1, which exists *because* "a codec may reuse an internal buffer and cannot be
asked to promise otherwise" — a premise the table applies to `Encode` and drops for `Decode`. A codec
that decodes into a scratch it reuses on the next call is admitted by every clause in the document
and silently corrupts a folded state (GAP-100). I also probed for a ninth and failed: the state `S`
`Load` returns, the `E` a fold publishes, `At`/`Commit`/`Authority`/`Backing`/`Cursor`, the decorator
forwarding a copy, and the transaction identity an `Authority` holds are each either derivable from
an existing row or carry no writable memory. **One member, not two.**

**Enumeration 2 — the inbound rows. NOT total. Five members constructed.** The table's stated scope
is "a caller hands the framework a value at the calls below, and **this table is all of them**", and
it omits `event.Compose(parts ...string)`, `eventtest.Keys(t, a, ids ...ID)`,
`eventtest.RoundTrip(t, fact, byRevision ...any)`, `eventtest.Families(t, declarations ...)` and
`eventmemory.New(Spec{Clock: …})` — the last of which retains an application callback the store then
calls on every append, which is row 7's shape at a call row 7 does not name (GAP-105). Every missing
member's answer is the benign one, which is why this is `medium` and not `high`; what is wrong is
the totality claim and the "four of the seven / two / one" count stated over it.

**`enumerationsTotal: false`**, on the strength of GAP-100's member for the outbound table and
GAP-105's five for the inbound one.

**GAP-93, walked for three state types before reading the answer.** (a) `struct{ Balance int64 }`:
Go copies, the argument is untouched, the result carries the folds — and §UC-068's control asserts
exactly that ("with a scalar-only state in the same test asserted **unaffected**"). (b)
`struct{ Applied map[string]bool; Balance int64 }`: the map advances through the caller's value and
the scalar does not, so the argument is a hybrid — §UC-068's *Observed* states it in those words and
§D.8's unit test asserts all three halves (`advanced.Balance != 350 || !reloaded.Applied["cmd-2"] ||
reloaded.Balance != 250`), which I traced against §D.0's own fold and found correct. (c)
`type Ledger map[string]int64`: `advanced` and `state` are one value, `state, err = Fold(…)` is a
no-op assignment and a second fold double-applies — §UC-068 says all of it. **The document says so
for all three.** §INV-004 now separates what *pure* claims from what it does not and gains proxy (2)
with the warning that it must never be written by folding one value twice; the honest scope ("what
neither proxy sees") is stated. The control that fails if a later `Fold` clones exists **twice** —
§UC-068's Control and §D.8's unit test, whose failure message is *"Fold did not advance the argument
in place, so its contract has changed"*. GAP-93 is closed. One residual: §UC-068 calls
`this[e.Account] += e.Minor; return this` "the only fold such a type can have", which is the
unguarded fold §2.1 and §UC-067 require to be guarded and which panics on the zero `Ledger` — GAP-109
item 2.

**GAP-94, walked with the two stores the finding named.** A store over a driver accessor whose
buffers are valid until the next fetch: the rule is now quantified over the recipient — "what a read
returns belongs to whoever receives it, for as long as it keeps it and from whatever goroutine it
reads it on" — and §D.14's `ReadStream` bullet names that accessor, an `mmap`ed page and a library
scratch by name, so the store is **obliged to clone**, not merely undefined. A store that pools its
`[]Envelope`: row 7 requires "a fresh slice per call, whose backing array no later `ReadStream` or
`ReadAll` reuses", so it is **refused**. §D.15's sketch comment now says *the scan allocated* and
spells out the other answer, so copying it is safe and it teaches the right predicate. Both closed.
What survives one level further in is the slice's **capacity**: rows 4 and 7 grant the recipient the
right to write into what it is handed and never require the hand-out's capacity to be its own, while
§INV-021 row 1 requires a full-slice expression for the identical hazard inside the kernel
(GAP-106).

**GAP-95…GAP-99 verified against the new text, not the notes.** GAP-95: §UC-045's fourth defect
writes **after the forwarded call returns**, §INV-021's falsification fixture matches word for word,
the "why" bullet names the red-in-five-places run, and the claim shipped is the one the suite can
hold with its three exceptions named — I checked each of the three and agree, and I re-derived that
the fourth defect fails only `payload ownership`. Closed. GAP-96: §UC-041 answers all four causes,
§INV-006 is scoped to `Load` with the reason, the Control drives cause 4 at change 2 of 3 and §D.9's
not-a-section list is renumbered to seven and names it. Closed. GAP-97: all six landed — §UC-048 says
*third* and gains `Records`, §D.3 drops the count and names the store and a forwarding decorator as
readers, §D.10's row says *answered by a promise instead of a copy*, §D.13 and §INV-021 price the
promise per store, §UC-006 carries the constructor argument and §D.4 cites it, and §D.5 gains
`Load`'s order so §D.9's `lifecycle` row and §UC-051 agree. Closed. GAP-98: the argument lives once,
in §D.14, and §INV-045 states it in one clause and cites it. Closed. GAP-99: non-goal 21 and §D.16's
ten-obligation walk both exist. Closed.

**Regression sweep, mechanical.** `UC-001…UC-068` is 68 `####` headers and `INV-001…INV-045` is 45;
every number in both ranges appears **exactly once**, none is missing or doubled (grepped and
counted). UC-023 and INV-037 are `WITHDRAWN` in place and cited only as withdrawals. **All seventeen
ordinal citations of §INV-021's table resolve to the row they name** — §UC-036 (4, 7), §UC-048 (3, 6),
§UC-061 (4), §UC-063 (3), §UC-065 (2), §D.7 (4, 7), §D.9 (3, 4, 6, 7 store / 1, 2, 5 kernel), §D.10
(3 / 4, 7), §D.12 (4, 7), §D.14's three struct comments (`INV-021.6`, `.3`, `.4`/`.7`) and §D.15's
sketch (`.4`, `.7`) — so appending rows 6 and 7 rather than inserting them held. §D.10's residual list
is 1–6 and both citations (§D.12's *fifth*, three sites' *sixth*) point at the right item. Counts:
22 sentinels = 3+7+3+4+2+3; §D.16's ≈ 71 = 13+27+9+22 and each of the four lists has exactly that
length; 6 bounds with 5 in `Limits`; 8 store methods split 4/4; 4 doors; 3 store-honesty checks at 2
doors; 6 defects from 3 fixtures; 7 not-a-section properties; 7 inbound rows summing 4+2+1. "exactly
once" occurs **five** times and every one is a prohibition. Every §D.n (17) and Qn (21) resolves.
**Two counts do not hold**: §INV-021's falsification names a *seventh* conformance decorator against
§UC-045's six (GAP-107), and the inbound table's totality claim (GAP-105). Everything else this round
edited landed where it said it did.

**Microkernel — derived by hand from §D.14 as it now stands.** I constructed each of `eventpg`'s
eight methods and named every value it must build. `Capabilities()`: a composite literal of four
exported `Support` fields over exported constants. `Limits()`: five exported ints. `Backing()`:
`event.NewBacking(crud.KeyOf(spec.Source))`, exported, error-returning. `Transaction()`:
`event.Authority{}` — a composite literal of an all-unexported-field struct, which is legal Go from
any package because no field is named — plus `event.NewAuthority(backing, tx)` and the store's own
bare sentinel. `ReadStream()`: consumes `Stream` (two exported fields) and `Version` (a defined
`uint64`); returns `[]event.Envelope` built field by field with `event.Key(k)`, `event.Version(n)`,
`event.Position(n)`; fails with `event.Failure(outcome, cause)`. `ReadAll()`: adds `event.Cursor(s)`
by conversion and `event.Failure(event.BadCursor, err)`. `Append()`: consumes `AppendRequest` and
`Record` by exported field, returns `event.Failure(…)` or a bare `ctx.Err()`. `Close()`: a plain
error. Nothing sealed travels in the store's direction, so no minting call is owed for `At[S]` or
`Commit`. **There is no value `eventpg` must build that it cannot build from outside package
`event`.** This round's two new obligations take affordances away (do not pool, do not retain, clone
what you borrow) and are satisfied by `make`/`append`/`bytes.Clone`. I also checked that **every fix
I propose below leaves that true**: GAP-100 and GAP-108 are prose obligations on an
application-supplied value plus a `RoundTrip` assertion; GAP-101's worst case adds one `Outcome`
constant, which §D.14's own compatibility paragraph makes an additive kernel change and not a
per-store one; GAP-102, GAP-103, GAP-104, GAP-106 and GAP-107 are clauses, a class move, a
conformance case and a fixture. **`microkernelPasses: true`.**

**Coverage.** All seven actors resolve to a `[happy]` use case whose actor line matches (A1→UC-001/2/3,
A2→UC-006/7, A3→UC-019/28, A4→UC-036, A5→UC-043, A6→UC-040, A7→UC-048) and all six entry modes have a
group; group G carries no mode label, which is the only hole and is cosmetic. All **thirteen** roadmap
non-negotiables map to a testable invariant, and I checked each against the roadmap's own wording
rather than against the document's claim: append-only→INV-001, exact version→INV-002, one
transaction→INV-003, pure rehydration→INV-004, declared wire identity→INV-005, no partial
rehydration→INV-006, no framework retry→INV-007, replay is authority→INV-008, at-least-once→INV-036,
no default telemetry fields→INV-011, not a CRUD collection→INV-012, no constructor starts
anything→INV-013, no start-up migration→INV-014. Of the ten obligations, 1–7 and 10 have homes and 8
and 9 are recorded as vacuous in non-goal 21 and §D.16 — the coverage is complete and now stated in
the document. The edge matrix is complete: empty (UC-009, UC-020), malformed (UC-004, UC-015),
oversized (UC-017, UC-024, UC-025), ambiguous (UC-034), duplicated (UC-035, UC-058), multilingual
(UC-051, INV-033), partially failing dependency (UC-060), timeout (UC-018, UC-057), zero results
(UC-009, UC-036's control), everything-matches (UC-039), conflicting signals (UC-049, UC-057). **Two
pairs contradict**: §INV-018 against §D.14's division of labour (GAP-103) and §INV-021's falsification
against §UC-045's defect count (GAP-107).

**Universality, all eleven probes plus two of my own.** `struct{ PlacedAt time.Time; Total int64 }`,
a state that **is** a map, two aggregates over one state type, a payload carrying a slice, a composite
non-comparable identity, zero events, 100 000 events, three retained revisions and a generated
declaration set each cost the same declaration and buy no framework feature; `Applied
map[string]bool`, `accounts.account` and the transfer are shown as the application's own answers
throughout, and I found **no use case, invariant or §D line that encodes one example aggregate as a
mechanism**. A store that pools, and a store whose driver returns fetch-scoped buffers, are both now
obliged to clone (GAP-94 closed). **Two probes of my own fail**: a store that sub-slices one page
buffer into N payloads is admitted and hands a consumer slices whose capacity runs into the next
event's bytes (GAP-106), and a codec that is not `event.JSON` — one that decodes into a scratch it
reuses — is admitted and silently corrupts every folded state (GAP-100). Both are the same
second-instance test that produced GAP-68, GAP-79, GAP-87 and GAP-94, applied to the two parties the
enumerations treat as sources of bytes but never as holders of state.

**Silent misuse, asked and answered.** The worst thing a **caller** can write is
`if errors.Is(err, context.DeadlineExceeded) { return timeout }` in front of its `ErrUncertain`
branch: the store spells the commit window `Failure(Unconfirmed, ctx.Err())`, `Failure` wraps its
cause so `errors.Is` reaches it, and the first branch therefore swallows exactly the "it may have
landed" fact §UC-057 calls unrecoverable (GAP-102). The worst thing a **decorator** can write is
`if overQuota { return errQuota }` before forwarding — the shape §UC-048's own precondition invites —
which the append door's fail-safe default turns into `ErrUncertain` for a write that was never issued
(GAP-101). The worst thing a **codec author** can write is a `Decode` that returns a value pointing
into a buffer it reuses (GAP-100). None of the three returns an error, none fails a stated test, and
each is the ordinary shape of its party.

**Length. 5567 lines, measured** (`wc`-equivalent from the final read: last numbered line 5567), which
matches the fix round's own recorded count exactly.

### GAP-100 [high][immediate] The outbound enumeration is stated over the bytes a codec *receives* and the bytes it *encodes*, and never over the value it *decodes* — so a codec that decodes into a buffer it reuses is admitted by every clause and silently corrupts every folded state, on the one path the kernel pays zero clones for

- **Where:** §INV-021's outbound domain paragraph lines 3089-3099 ("Mutable memory crosses a party
  boundary at exactly **seven** points … Everything else crossing any boundary is a string, a scalar,
  a func value, an opaque handle or an error"); row 1 line 3103, whose whole justification is that a
  codec "**may reuse an internal buffer and cannot be asked to promise otherwise**"; rows 2 and 5
  lines 3104 and 3107, which describe the kernel handing a codec *input* and stop there; §D.3 lines
  4058-4062 ("The value a fold receives is produced by the fold, decoded when the fold runs from a
  **clone** of the frozen bytes, so a fold written `s.Lines = e.Lines` aliases a value **the framework
  drops the instant it returns**"); §UC-065's "What the fold's `E` may alias" lines 2344-2346
  ("**Nothing the framework holds**, at any depth: it is decoded for this call, handed over and
  forgotten, so an application that keeps a slice reached through `e` keeps memory nothing will look
  at again"); §INV-021's "What a caller may do with what it is handed" lines 3170-3175; §D.2's
  `Codec[V]` interface lines 3973-3977; §D.12's rejection row line 4798 (*an obligation on
  `Codec.Decode` not to alias its input*).
- **What:** Every statement above is about memory **the framework** holds. None of them is about
  memory **the codec** holds. `Decode([]byte) (V, error)` may legally return a `V` whose `[]byte` or
  string fields point into a scratch buffer the codec owns and overwrites on its next call — a pooled
  arena, a decompression scratch, a code-generated decoder that unmarshals into a reused message.
  That is not the rejected shape: §D.12 rejected *"do not alias your input"*, and the kernel's clone
  (row 2) and the store's hand-out (row 4) both answer the input direction. Nothing answers the
  **output** direction, and the consequences are exactly the ones this rule exists to prevent.
  1. **A replay produces a wrong state, deterministically.** `Load` decodes envelope 1 → `e1.Blob`
     points at the scratch; the blessed fold `s.Blob = e.Blob` publishes it; decoding envelope 2
     overwrites the scratch; the state the caller receives holds event 2's bytes under event 1's
     field. No error anywhere.
  2. **Every existing control is blind to it by construction.** §INV-004's proxy (1) compares two
     replays, which agree — they are wrong identically. §UC-061 asserts the pre-mutation state equals
     the reload, which it does. §UC-065 folds the same change twice and compares to a reload; with a
     scratch codec the second decode *heals* the first value, so the case passes. §UC-068 asserts the
     argument advanced. The one codec phase 1 ships, `event.JSON`, allocates on decode, so a green
     suite is evidence of nothing here — which is the sentence §UC-065's own third control already
     uses about the other direction.
  3. **It reaches the history.** §D.10 item 2 states that re-appending a change list the caller still
     holds is expressible and legal. Between the fold and that append the scratch has moved on, and
     what the fold published into the state — which a decision then reads — is another event's
     bytes; the fact decided from it is recorded. "The fact recorded is not the fact decided" returns
     by the one party the table never contracts.
- **Why this severity:** `high`, matching this file's own calibration for GAP-79, which was
  `high` rather than `critical` "because reaching it needs a non-JSON codec, which phase 1 ships none
  of; but the phase ships the `Codec` interface and invites applications to implement it". Everything
  else about the two findings is identical: silent, deterministic, invisible to `-race`, invisible to
  every stated proxy, and reached through steps the document blesses one at a time. It is also a
  direct falsification of this round's central claim — the outbound table says "exactly seven" and
  says a codec cannot be asked to promise anything about its buffers, and then omits the one hand-off
  where that premise bites.
- **Why this timing:** It is a clause of the `Codec` contract, which is frozen at the end of phase 1
  and is the second extension point the phase ships. It also decides a test (`eventtest.RoundTrip` is
  the only helper positioned to falsify it) and it decides nothing about cost, because the answer is
  an obligation plus an assertion rather than a copy — but the *wrong* answer is a kernel-side deep
  copy of `E`, which is §UC-052's deleted copier walk arriving through a back door if the question is
  reopened after `event` is written.
- **Close criteria:**
  - [ ] §INV-021's outbound enumeration names the codec's `Decode` output as a hand-off, with its
        sender, its recipient and what makes it safe — or states in terms that the table is over byte
        arrays the *framework or a store* owns and points at wherever the codec's own obligation now
        lives.
  - [ ] The `Codec` contract states the obligation in one clause: **a decoded value may alias its
        input and may hold freshly allocated memory, and must not alias memory the codec will write
        or reuse** — which is compatible with §D.12's rejection of "do not alias your input" and with
        row 1's premise about `Encode`, and which is the only one of the three candidate owners
        (codec, kernel deep copy, borrowed `E`) that is not already rejected on the record.
  - [ ] §UC-065's and §D.3's "the framework drops it the instant it returns" and "nothing the
        framework holds, at any depth" are scoped to what they are true of, so a reader cannot take
        them for a promise about the value's lifetime.
  - [ ] `eventtest.RoundTrip` gains the proxy: decode two different payloads of one revision and
        assert the first decoded value is unchanged after the second decode, with the shipped JSON
        codec as the passing control and a deliberately scratch-reusing codec asserted to fail —
        so the obligation is runnable rather than written down, which is the standard §INV-033 sets
        for the injectivity obligation it also cannot check in general.
  - [ ] §D.13 records the cost of the answer taken (zero, if it is an obligation).
- **Status:** open

### GAP-101 [high][immediate] A policing decorator — the actor whose happy path §UC-048 is, with "refuse writes for a tenant that is over quota" in its own precondition — has no way to spell its refusal, and the append door's fail-safe default turns it into `ErrUncertain` for a write that was never issued

- **Where:** §UC-048's Precondition lines 1975-1977 ("a wrapper that wants to count appends, **refuse
  writes for a tenant that is over quota**, or record what was read") and its *Must not happen* line
  1998 ("A policy refusal must arrive **before** the forwarded call, never after a partial effect
  ([ES] obligation 4)"); §UC-060's fail-safe default lines 1390-1408, whose scoping list names only
  "the kernel's own pre-store refusals … which never reached the store"; §D.14's map row line 5085
  ("`Failure(Unclassified, cause)`, or **any error that is not a `Failure`** | the door's fail-safe
  default: `ErrUncertain` from `Append`"); §INV-045's statement lines 3742-3748; §2.3's store class
  row line 307 and §UC-034's bounded procedure lines 1727-1743.
- **What:** A decorator **is** a store to the kernel (§INV-030), so an error it returns from `Append`
  crosses the store boundary and is read by the same map. The ordinary policing wrapper —
  `if overQuota { return errQuota }; return this.next.Append(ctx, req)` — compiles, forwards nothing,
  writes nothing, and reaches the caller as **`ErrUncertain`**: *the write may have landed, do
  §UC-034's single retry and then reload*. For a refusal that never reached a backend. That is the
  precise inversion of the fail-safe default's own justification ("guessing the safe way costs one
  reload and guessing the other way is a silent double write"), and §UC-060's scoping list cannot
  save it, because a decorator's refusal is inside the append attempt from the kernel's side.
  Two things are missing and they are different:
  1. **Nothing tells A7 to classify.** §UC-048 forbids a wrapper to convert a cancellation, a
     conflict or an unknown commit into another class, and says a policy refusal must come before the
     forwarded call — and never says that the wrapper's own refusal is a failure it must name an
     `Outcome` for. The one bullet about errors (lines 1999-2004) is about wrapping the **wrapped
     store's** error, not about producing one.
  2. **Even classified, there is no honest row.** `Failure(NotWritten, err)` is the only safe
     selection, and it maps to `ErrBackend`, store class — *the store itself refused or failed* —
     class-wrapping the framework's retryable class when the cause carries it. A tenant over quota is
     neither a backend failure nor retryable, and a transport that maps the store class answers a
     5xx for what is a policy decision about a client.
- **Why this severity:** `high` under `universality.md`'s "behaviour undefined for a neighbouring
  input: the code neither handles it nor fails loudly" and under `gaps.md`'s *leaky abstraction*. The
  neighbouring input is not exotic: it is the second of the three wrappers §UC-048's own precondition
  names, on the [happy] use case of one of the seven actors. It is GAP-67's finding — *a store is
  required to make a classification the contract gives it no way to spell* — applied to the party
  §UC-048 exists for, and the default the gap falls through to is the most alarming value in the
  vocabulary.
- **Why this timing:** It is a clause of §UC-048 plus, if the second half is taken, a value in a
  closed enum that §D.14's compatibility paragraph freezes with the contract. `Outcome` grows
  additively, but only before a store has shipped that maps around its absence.
- **Close criteria:**
  - [ ] §UC-048 states what a wrapper's **own** refusal must look like, in one clause, and says what
        an unclassified one becomes — so an implementer cannot reach the `ErrUncertain` default by
        omission.
  - [ ] The document decides, on the record, between: (a) a policy refusal is spelled
        `Failure(NotWritten, err)` and renders as `ErrBackend`, with the DX cost stated; (b) a
        seventh `Outcome` and the row of §2.3 it maps to; (c) policy does not belong on the store
        seam at all, in which case §UC-048's precondition drops the quota example and says where a
        quota refusal does belong. Whichever is taken, the rejected ones become §D.12 rows.
  - [ ] §D.9's `self-falsification` or `refusal classes` section gains the case: a decorator that
        refuses before forwarding, asserting the sentinel the answer above chose and **zero events**
        — which no current section drives, because every defect in §UC-045 forwards or lies rather
        than refusing.
  - [ ] §UC-060's scoping list says whether a decorator's own refusal is inside or outside "the
        append attempt itself".
- **Status:** open

### GAP-102 [high][immediate] `ErrUncertain` carries `ctx.Err()` as its cause, so `errors.Is(err, context.DeadlineExceeded)` answers **true** in the commit window — the caller's first branch swallows the one fact §UC-057 calls unrecoverable, and §2.3 says in terms that this cannot happen

- **Where:** §2.3 lines 362-363 ("`context.Canceled` and `context.DeadlineExceeded` are **not**
  sentinels of this subsystem and **are never wrapped by one**: they travel as themselves
  (§INV-029)") against §2.3's cause list lines 349-353 ("`ErrBackend`, `ErrConflict`, `ErrCursor`,
  `ErrClosed`, **`ErrUncertain`** and `ErrAmbientNotTransaction` may all carry a store's error as a
  cause, because all six are produced from one") and the cause definition lines 346-348 ("reachable
  with `errors.Is`, rendered opaquely so its text does not travel"); §UC-057's "How a store says
  which window it was in" lines 1339-1345 ("In the second it returns
  `event.Failure(event.Unconfirmed, ctx.Err())`") and its Observed line 1324 ("the cancellation's
  identity is *not* what the caller matches"); §D.14's `Failure` comment line 5065 ("Wraps cause so
  errors.Is reaches it") and the map's `Unconfirmed` row line 5082; §INV-029's falsification lines
  3375-3378.
- **What:** The specified mechanism makes the forbidden thing true. The store hands the kernel
  `Failure(Unconfirmed, ctx.Err())`; the kernel maps it to `ErrUncertain` **carrying the cause**; a
  caller's `errors.Is(err, context.DeadlineExceeded)` therefore answers `true` on the very error
  whose whole purpose is to say *this may have landed*. The ordinary Go shape —
  `if errors.Is(err, context.DeadlineExceeded) { … }` before the subsystem's own branches, which is
  how every timeout-aware handler in this tree is written — takes the cancellation branch, and the
  caller reports a timeout for a write that may be durable. §UC-057's precedence rule
  ("reporting an uncertain commit as a cancellation loses the 'it may have landed' fact and **cannot
  be recovered**") is established at the store→kernel boundary and lost at the kernel→caller
  boundary, which is the boundary the caller actually reads. Nothing catches it: §INV-029's
  falsification asserts `ErrUncertain` matches and never asserts that the cancellation does not.
  The document has the machinery for exactly this and applies it one case short — §2.3's *one
  obligation a cause carries* forbids a store's error from being a sentinel of this vocabulary
  "because `errors.Is` from a refusal of one class could reach a sentinel of another through the
  cause", which is the identical back door, argued and closed for one pair of values and left open
  for the two the document separately declares must travel as themselves.
- **Why this severity:** `high`. The failure is silent, the input is the ordinary one (a deadline
  expiring mid-commit is the *reason* `ErrUncertain` exists), the consequence is the one the document
  names as unrecoverable, and the caller's mistake is the shape it was told to write everywhere else
  in this framework. Not `critical` only because the caller that branches on `ErrUncertain` **first**
  is correct and the design's own §D.6 sketch does not branch on cancellation at all.
- **Why this timing:** It is the semantics of a frozen sentinel, it decides one line of the kernel's
  mapping code, and it decides an assertion in a conformance section (`cancellation`) that is being
  specified now. A caller-visible `errors.Is` answer cannot be changed after consumers exist.
- **Close criteria:**
  - [ ] The document states what `errors.Is(err, context.Canceled)` and
        `errors.Is(err, context.DeadlineExceeded)` answer for an `ErrUncertain` produced in the commit
        window, and §2.3's "never wrapped by one" is either made true — the kernel drops a context
        cause when it maps `Unconfirmed`, keeping the store's non-context cause if there is one — or
        scoped to §UC-057's first window with the second window's answer written beside it.
  - [ ] Whichever answer is taken is derived rather than asserted, against §UC-057's stated asymmetry
        and against §2.3's own cause obligation, whose argument is the same one.
  - [ ] §D.9's `cancellation` section asserts both halves: in the first window the cancellation
        sentinel matches **and** `ErrUncertain` does not; in the second window `ErrUncertain` matches
        **and** the cancellation sentinel answers whatever the clause above decided.
  - [ ] §INV-029's falsification names the negative assertion, not only the positive one.
- **Status:** open

### GAP-103 [medium][immediate] §INV-018 says a store "never applies" its limits and the kernel enforces every bound on its behalf; §D.14 makes the store apply two of them, and no party checks a page longer than the bound — so §UC-011's memory guarantee is a claim with no falsification

- **Where:** §INV-018's statement lines 2976-2978 ("It **carries** its limits and capabilities and
  **never applies them**: the kernel enforces **every** bound on the store's behalf, so a store cannot
  enforce one differently") and its falsification line 2982 ("A signature inventory of `Store` and
  `Log`"); §D.14's division-of-labour table line 5154, store column ("**capping `ReadAll` at
  `MaxRead` and `ReadStream` at `StreamPage`**"); §D.14's `ReadStream` bullet line 5176 ("at most
  `Limits().StreamPage` of them") and `ReadAll` line 5190 ("at most `Limits().MaxRead`"); §UC-036's
  Flow line 1795; §UC-011's Observed lines 902-903 ("Memory in use is bounded by one page and by the
  state itself, **not by stream length**"); §D.9's `bounds` row line 4582.
- **What:** Two of the six bounds are the store's to apply and four are the kernel's, and the
  invariant states the opposite without a carve-out. It is not only wording: it decides whether
  anybody checks the store's half. Today nobody does. A store that ignores `StreamPage` and returns a
  100 000-event stream in one page satisfies every conformance section — `stream paging` compares a
  fold at `StreamPage` with a fold at 1 and both are correct, `bounds` builds its cases from
  `store.Limits()` and asserts the *caller-side* bounds — while §UC-011's stated Observed, the one
  memory guarantee the phase makes for a long stream, is false. The mirror defect *is* policed:
  §UC-045's short-page decorator fails `stream paging`. The over-long page has no defect, no section
  and no sentinel, and §INV-018's falsification (a signature inventory) is structurally unable to see
  either.
- **Why this severity:** `medium`. Nothing is corrupted and no caller is misled about data; what
  fails is a resource bound under an input — a very long stream — that §UC-011 exists for, and an
  invariant that reads as enforced is an obligation nobody performs. It is `restrictions.md`'s
  "performance within stated requirements" only until the store is somebody else's.
- **Why this timing:** §INV-018 and §D.14's division of labour are the two places an implementer
  reads to decide who writes the `LIMIT`, and they disagree today. The conformance section is being
  specified now and a section added later is a section `eventpg` has already passed without.
- **Close criteria:**
  - [ ] §INV-018 states the division as it actually is — the store applies the two page bounds
        because only it issues the read, the kernel applies the other four — and says why that is not
        the import inversion the invariant exists to forbid.
  - [ ] The document says what the kernel does with a page longer than the bound: verify and refuse
        (naming the sentinel), or accept and record it as an unpoliced store obligation with
        §UC-011's Observed narrowed to what a conformant store gives.
  - [ ] If it is verified, §D.9's `bounds` section gains the case, with the at-the-bound page as its
        control; if it is not, §UC-045's defect table gains the over-long-page store or §D.10 records
        it beside the other things a store can lie about.
- **Status:** open

### GAP-104 [medium][immediate] Two sentinels sit in classes their own use cases contradict, and the class is the thing a transport maps

- **Where:** §UC-017's Refusal lines 1081-1082 (`ErrTooLarge`, **request** class, for a **stored**
  payload larger than the cap, on a `Load`) against §2.3's request row line 304 ("*The data this
  operation was given cannot be used.* The ordinary cause is a request; a transport answers a client
  error") and its history row line 305 ("*A fact's recorded bytes cannot be read by this
  declaration.* … a deploy, a retention or a broken-codec question"); §UC-053's "When it is refused"
  lines 1905-1908 ("Three causes, one sentinel … Each is a wiring or data mistake the consumer can
  act on and **none is a store failure**") against its Refusal line 1915 (`ErrCursor`, **store**
  class) and §2.3's store row line 307 ("*The store itself refused or failed.*"); §2.3's own boundary
  paragraph lines 313-319, which fixes exactly this question for `ErrKey` and `ErrWrongStore`;
  §INV-024's "every sentinel belongs to exactly one class".
- **What:** The partition assigns a class per **sentinel**, and two sentinels carry causes from two
  classes.
  1. **`ErrTooLarge` on the load path.** §UC-024 and §UC-025 use it for a caller's oversized payload
     and batch, where the request class and the too-large class wrap are exactly right. §UC-017 uses
     the same sentinel for bytes **already in the history** — written before the cap was lowered, or
     by another writer — which is §2.3's history class by its own definition. A transport that maps
     the class answers a client `413` for a `GET` that carried no entity, and the caller's stated
     obligation for the request class ("send different data") is unavailable: there is no data to
     change. The document already made this exact argument once, for `ErrKey` (GAP-52), and once for
     `ErrBatch` — "one caller obligation and one transport class" — and §UC-017 has neither.
  2. **`ErrCursor`.** Its use case says in terms that none of its three causes is a store failure,
     and its class says the store failed. A cursor minted over backing A and presented to backing B
     is, word for word, §2.3's own definition of the wiring class: "two values the framework minted
     apart, put together by a hand", neither of which "arrived with a request".
- **Why this severity:** `medium`. No data is corrupted and both refusals are refusals; what is wrong
  is the classification a transport branches on and the retry posture a caller derives from it —
  which is the entire reason §2.3 exists as a partition rather than a list.
- **Why this timing:** Moving a sentinel between classes is **breaking** by §2.3's own growth rule,
  so it is free now and permanent later. It also changes a row of §INV-024's ground-truth table,
  which is being written now.
- **Close criteria:**
  - [ ] §UC-017's stored-oversize refusal is decided: either it takes the history class's sentinel
        (`ErrPayload` is already stated over "a fact's recorded bytes", which an oversized stored
        payload is) or the document argues why a stored-bytes failure is a request-class fault, in
        the same terms §2.3's `ErrKey`/`ErrWrongStore` paragraph uses.
  - [ ] `ErrCursor`'s class and §UC-053's "none is a store failure" are made to agree, in whichever
        direction, with the argument stated where the class is assigned.
  - [ ] §2.3's boundary paragraph is extended to cover whichever of the two moves, so the rule that
        decides class membership is stated once and applied to every sentinel rather than to the two
        that were argued.
- **Status:** open

### GAP-105 [medium][immediate] The inbound enumeration claims to be every call at which a caller hands the framework a value, and five such calls are missing — including the one that hands a store a callback it invokes on every append

- **Where:** §INV-021's inbound paragraph lines 3136-3137 ("A caller hands the framework a value at
  the calls below, and **this table is all of them**"), its seven rows 3139-3147 and the count stated
  over them lines 3149-3152 ("**Four** of the seven rows are *read and not retained*; **two** are
  retentions … and the **one** row where the framework's own loop writes"); the calls it omits —
  `event.Compose(parts ...string)` (§D.1 line 3915), `eventtest.RoundTrip(t, fact, byRevision ...any)`
  (§D.8 line 4411), `eventtest.Keys(t, a, ids ...ID)` (line 4412),
  `eventtest.Families(t, declarations ...Declaration)` (line 4413) and `eventmemory.New(Spec{Clock:
  func() time.Time, …})` (§D.4 lines 4146-4155); §INV-042's scope, which is stated over "`event`,
  `eventmemory` **or** `eventtest`", so all five are inside the boundary the invariant draws.
- **What:** Four of the five hand over a variadic slice — mutable memory by any definition, since the
  caller can write `parts[0]` or `ids[1]` after the call — and one hands over an application callback
  the store **retains and calls on every append**, which is row 7's shape at a call row 7 does not
  name. `RoundTrip`'s `byRevision ...any` is the sharpest: it hands the framework application values
  of every retained reader type, which the helper then encodes and decodes, and it is the one inbound
  hand-off of a *payload-shaped* value outside `Fact.New`. Every missing member's answer is the
  benign one — read, not retained, except the clock, which is retained by design — so nothing is
  unsafe. What is wrong is that the enumeration is the artefact this round added *because* a rule
  stated over the values that happen to exist is how GAP-68, GAP-79, GAP-87 and GAP-94 each happened,
  and it repeats the pattern one call-site out. The stated four/two/one count is over a table that is
  not all of them.
- **Why this severity:** `medium`, not `high`: I constructed five members and every one of their
  answers is trivial, so no behaviour is undefined and no store, codec or caller is misled. It is
  reported because a totality claim that is false is worse than no claim — a later reader checking a
  new call against "this table is all of them" concludes the question was answered.
- **Why this timing:** Rows added later shift the stated count and the "one row whose answer is not
  read only" phrasing that §UC-041, §UC-052 and §D.10 item 6 all cite; doing it now costs three rows
  and one recount, and §INV-021's own note that its numbering is stable and cited by ordinal shows
  the cost of doing it after.
- **Close criteria:**
  - [ ] The inbound table gains the missing calls, or its scope sentence is narrowed to what it
        covers ("every call on the caller seam that hands over a value the framework may retain or
        write") with the helpers and `Compose` named as the remainder and answered in one line.
  - [ ] The four/two/one count is recomputed against whatever the table becomes, and the sites that
        cite "the one row whose answer is not *read only*" are checked against it.
  - [ ] A store's own spec callbacks — `eventmemory.Spec.Clock` and anything an `eventpg.Spec`
        carries — are answered somewhere: retained by design, called on every append, and by
        implication safe for concurrent use (see GAP-108).
- **Status:** open

### GAP-106 [medium][immediate] Rows 4 and 7 give the recipient the right to write into what a read returns and never require the hand-out's **capacity** to be its own, so a store that sub-slices one page buffer hands a consumer slices whose `append` overwrites the next event's payload

- **Where:** §INV-021 row 1 line 3103, which requires `Fact.New` to freeze "**with a full-slice
  expression**, which is `jobs.EncodedPayload`'s exact shape" — the document's own recognition of the
  hazard, applied once, inside the kernel; rows 4 and 7 lines 3106 and 3109 ("clones what it retains
  **and what it does not own**, and never reuses a buffer across two calls"; "a fresh slice per call,
  whose backing array no later `ReadStream` or `ReadAll` reuses"); the recipient clause lines
  3122-3124 ("what a read returns belongs to whoever receives it, for as long as it keeps it and from
  whatever goroutine it reads it on") and "What a caller may do with what it is handed: **Anything**"
  lines 3170-3175; §INV-038's `Envelope` row line 3510 ("the payload is the caller's to mutate");
  §D.14's `Envelope` comment lines 4997-5002; §D.9's `payload ownership` row line 4583.
- **What:** Construct the third store shape, after the pooling one and the borrowing one: a store
  that reads one page's rows into **one** buffer and sub-slices a payload per row — the ordinary
  shape of an efficient decoder, and the one a wire-protocol read produces naturally. It retains
  nothing, owns what it hands out, allocates a fresh buffer and a fresh slice per call, and therefore
  satisfies rows 4 and 7 clause by clause. Its payload slices are disjoint in *range* and share a
  backing array in *capacity*: `e.Payload` for envelope *i* has `cap` running to the end of the page.
  A consumer told it may do "anything" with what it was handed writes
  `e.Payload = append(e.Payload, b…)` — or an aliasing codec's decoded field does — and silently
  overwrites envelope *i+1*'s bytes in the page it is still holding. The conformance case cannot see
  it: `payload ownership` overwrites **every byte of every** payload, which is exactly the write that
  cannot distinguish a shared backing array from disjoint ones.
- **Why this severity:** `medium` rather than `high`: it takes an `append` (or a codec that appends)
  rather than an ordinary write, and neither shipped store has the shape. It is reported at all
  because it is the same second-instance test that produced GAP-94 one level further in, because the
  document already applies the exact remedy in row 1 for the identical hazard, and because a rule
  that grants the recipient "anything" while the sender may hand over a slice whose spare capacity is
  another event's data is a naive contract by the standing rule.
- **Why this timing:** It is one clause in the two places rows 4 and 7 are stated for an implementer
  (§INV-021 and §D.14's `Envelope` comment), and it is a case in a section being specified now. After
  `eventpg` ships, "may I `append` to a payload I was handed" is a question with two answers in the
  field.
- **Close criteria:**
  - [ ] Rows 4 and 7 and §D.14's `Envelope`/`ReadStream`/`ReadAll` text say that what a read returns
        is the recipient's **including its capacity** — a full-slice expression, as row 1 already
        requires of the kernel — or state that the recipient may write within the length and not
        beyond it, in which case "anything" and §INV-038's "the payload is the caller's to mutate"
        are narrowed to match.
  - [ ] §D.9's `payload ownership` gains the case that separates the two: `append` one byte to page
        one's **first** envelope's payload and assert the **second** envelope's payload is unchanged,
        with a store that clones per envelope as its control.
- **Status:** open

### GAP-107 [medium][immediate] §INV-021's falsification names a seventh conformance decorator, against a defect count of six asserted in five places — so an implementer either breaks the count or drops the case that catches a pooling store in the self-check

- **Where:** §INV-021's falsification lines 3197-3199 ("And **a conformance decorator that returns one
  reused payload buffer and one reused envelope slice for every page**, which the `payload ownership`
  section must fail"); §UC-045's Flow line 2567 ("The suite runs each section against **six**
  deliberately defective stores") and its six-row table lines 2569-2576; §UC-045's "Why two of the six
  are stores" line 2578; §UC-048's lines 2007 and 2024 ("two of the suite's six … Four of the suite's
  six"); §INV-035 line 3473 and §UC-037 line 1852 ("one of the six defects the suite injects into
  itself"); §D.9's `self-falsification` row line 4588 ("the six defects — four forwarding decorators
  and two purpose-built defective stores").
- **What:** GAP-94's close criteria offered two ways to give hand-off 7 a self-falsification arm —
  add a defect to §UC-045, or restate the existing reused-buffer decorator — and the second was
  taken, which leaves a decorator that is aimed at a named conformance section, is built exactly like
  the six, and is not one of them. The count is asserted in five places and the fixture inventory in
  §UC-045 ("three fixtures, not one with two variants") is derived from it. An implementer resolves
  this by adding a seventh (and every count is wrong) or by dropping the invariant's clause (and the
  self-check loses the only arm that proves the suite still detects a store that pools its page —
  which is the whole of GAP-94's outbound half).
- **Why this severity:** `medium`. Nothing is unsafe and both resolutions are cheap; what is wrong is
  that the document does not choose, in the one artefact whose failure mode is silent by construction
  and whose count it polices in five places.
- **Why this timing:** The suite's dispatch and its self-check inventory are specified now, and this
  is the second round in which a count and a fixture list had to be reconciled after the fact.
- **Close criteria:**
  - [ ] The reused-payload-buffer-and-envelope-slice decorator is either §UC-045's **seventh** defect,
        with the count corrected in §UC-045, §UC-048 (twice), §INV-035, §UC-037 and §D.9 — or it is
        stated as a fixture of `event`'s own tests and §INV-021's falsification says which, in which
        case §D.9's "what is deliberately not a section" list gains it as an eighth property.
  - [ ] Whichever it becomes, the section it must fail is named and the store-under-test half
        (hold page one, fetch page two) is stated to be a different assertion from the self-check
        half, so the two are not read as one case.
- **Status:** open

### GAP-108 [medium][immediate] Nothing states that a `Codec`, an identity mapper or a store's injected clock is safe for concurrent use, while §INV-038 promises that the values that hold them are — so the seal's "nothing writes" is true of the table and false of what the table returns

- **Where:** §INV-038's `*Aggregate[S, ID]`, `*Fact[S, ID, E]` row line 3515 ("**safe for many
  goroutines, and this is why the seal exists**: the tables are read-only from the first observation
  onward (§UC-005), so **every request goroutine in the process reads them concurrently and nothing
  writes**") and its `*Repo[S, ID]` row line 3513 ("**safe for many goroutines**"); §INV-021's inbound
  row 7 lines 3147 ("a codec, a fold, an upcaster, a mapper … **retained, by design**, on the sealed
  declaration, and **called by the framework**"); §D.2's `Codec[V]` interface lines 3973-3977;
  §INV-004, which states the purity obligation for **folds and upcasters** and says nothing about
  codecs or mappers; §INV-023, which asks a codec for *deterministic encodability* and not for
  purity; §D.4's `Spec{Clock: func() time.Time}` line 4149; §D.9's `concurrency` row line 4587.
- **What:** The seal makes the *table* immutable. What a read of the table yields is an
  application-supplied value the framework then **invokes** — `codec.Encode`, `codec.Decode`,
  `fold`, `upcast`, `key(id)`, `clock()` — from every request goroutine, concurrently, and no
  invariant requires any of them to be safe for that. The gap is not uniform: a fold and an upcaster
  inherit safety from §INV-004's and §2.1's purity obligations, and a codec and a mapper inherit
  nothing, because "deterministic" is asked of one encode and injectivity of the mapper's *values*.
  A `Codec` with any internal state — a reusable buffer, a memo, a pooled decoder — is a data race
  the framework's own call performs, reported under `-race` inside `event`, and §D.9's `concurrency`
  section will pass against it because the only codec in the suite is `event.JSON`. §INV-038's own
  stated reason for existing is that "one answer for all of them would be wrong for some", and the
  table has no row for the four values every operation calls.
- **Why this severity:** `medium`. Most codecs are stateless and the answer is almost certainly
  "must be safe for concurrent use, and the framework calls it from every goroutine"; writing it down
  is the whole fix. It is `medium` and not `low` because the document promises the containing values
  are goroutine-safe, which a reader will take as covering what they contain, and because it is the
  same party GAP-100 is about.
- **Why this timing:** It is a frozen obligation on the second extension point, and a `-race`-clean
  suite proves nothing about a rule nobody wrote — which is GAP-74's own sentence, applied to the
  value GAP-74's fix stopped one level short of.
- **Close criteria:**
  - [ ] §INV-038's table gains a row for the retained callbacks (codec, fold, upcaster, mapper, and a
        store spec's clock), stating that the framework calls them from many goroutines concurrently
        and that each must therefore be safe for concurrent use.
  - [ ] The `Codec` contract and the mapper's contract say it where an implementer reads them (§D.2,
        §D.1), so it is not only in the invariant.
  - [ ] §D.9's `concurrency` section drives at least one section through a codec that would fail if
        the obligation were not met — or the document states that the obligation is unpoliced and
        why, as it does for injectivity.
- **Status:** open

### GAP-109 [low][immediate] Six one-clause residuals, four of them in text an implementer reads as contract

- **Where and what:**
  1. **§UC-005 lines 685-689** — "The readers that seal, **exhaustively** for phase 1" lists binding,
     `Aggregate.Fold`, the round-trip helper, any read of a fact's revision or chain (including
     `Fact.New`), and the conformance suite. §D.8 line 4435 says "**All three helpers above** are
     sealing readers", the three being `RoundTrip`, `Keys` and `Families` — so two readers are
     declared sealing outside a list that calls itself exhaustive and that says "adding a reader
     without adding it here is the defect".
  2. **§UC-068 lines 2447-2448** — "the only fold such a type can have: `this[e.Account] += e.Minor;
     return this`". That fold panics on the zero `Ledger`, which §2.1 line 212 and §UC-067 lines
     2403-2406 both say is where a nil-map state starts and which §2.1 says "the fold owns the guard"
     for. A control written from the fold as printed panics instead of asserting; a guarded one
     allocates on the first event, so `advanced` and `state` are **not** one value on a fresh stream
     and "folding the same list twice applies every event twice" needs the non-nil precondition
     stated.
  3. **§UC-051 line 973** — "§D.5 fixes where the check sits in each call's order — **first in
     both**". §D.5's `Append` order puts the kernel's text rules at step (1) and "the store's bounds"
     at step (4), so the store's `MaxKey` half of the same check is fourth at `Append` and first at
     `Load`. Which sentinel wins for a token whose key is over `MaxKey` **and** whose stream differs
     from a change's is decided by that ordinal, and no control constructs it.
  4. **§UC-047 line 1931 and §D.14 lines 4955-4965** — "Every later operation refuses" against a
     closed store that must answer `Transaction(ctx)` with the invalid authority and **no error**.
     `Within` asks only that question, so on a closed store it answers `ErrNoTransaction` — *you
     forgot to open a transaction* — when a transaction **is** bound and the store is shut; and if
     one is bound, `Within` **succeeds** on a closed store. Neither is wrong, and neither is stated;
     §D.9's `lifecycle` drives `Load` and `Append` only.
  5. **§D.6 line 4298 and §UC-030 line 1628** — `Repo.Authority(ctx)` is in the signature block and
     is read by §UC-030's assertion, and no use case states what it returns on an unmarked context,
     on a context carrying a non-transaction executor, or on a closed store. It is the one exported
     method on the caller seam with no Refusal line anywhere.
  6. **§INV-036's falsification lines 3486-3490** — the check is "a grep of the finished document for
     'exactly once'", and §D.16 line 5422 writes **"forward-exactly-once"**, which that grep does not
     match. The claim ("there are five, and they are …") is correct for the unhyphenated form only,
     so the mechanism as specified is narrower than the rule it polices.
- **Why this severity:** `low` individually — none changes a behaviour, and items 1, 2 and 6 are
  visible the moment the corresponding test is written. Raised because items 1, 3 and 4 sit in text
  an implementer treats as contract, item 2 is the fold a control is written from, and rounds 1, 5,
  6, 7, 8 and 9 each recorded that this document's small drifts are how a fix silently fails to land.
- **Why this timing:** Each is one clause, and item 3 is an ordinal that decides which sentinel a
  caller sees.
- **Close criteria:**
  - [ ] §UC-005's exhaustive list names every sealing reader §D.8 declares, or §D.8 stops declaring
        `Keys` and `Families` sealing and says what they read instead.
  - [ ] §UC-068's `Ledger` fold is the guarded one, or the case states the non-nil precondition its
        claim needs.
  - [ ] §UC-051 and §D.5 agree about where `MaxKey` sits in `Append`'s order.
  - [ ] §UC-047 or §D.9's `lifecycle` says what `Within` and `Repo.Authority` do on a closed store.
  - [ ] `Repo.Authority(ctx)` has a stated return and refusal contract in a use case.
  - [ ] §INV-036's grep covers the hyphenated form, or §D.16 stops using it.
- **Status:** open

---

**Checked this round and found sound** (so the absence of a finding is deliberate). GAP-93's close is
correct in its own terms and I walked all three state types before reading its answer; the answer is
argued from the three available shapes, the two rejected ones are §D.12 rows, the control exists
twice and would fail if `Fold` ever cloned, and §INV-004 is now honest about what its two proxies
see and what neither does. GAP-94's close is the right trade and is derived rather than asserted; the
`eventpg` comment now teaches the predicate instead of the conclusion. GAP-95's fixture order is
correct and I re-derived that the fourth defect fails only its own section; the three defects that
fail extra sections are named and each one's extra failures follow from what a store lying about that
property does to the data. GAP-96's four return values are consistent with §INV-006's newly stated
scope. All six of GAP-97's items landed. GAP-98's argument has one home. GAP-99's obligations 8 and 9
are recorded in the document. The seventeen ordinal citations of §INV-021's table all resolve, which
was the class four of round 9's findings belonged to. Every UC and INV number is present exactly once,
both withdrawals forward correctly, every §D.n and Qn resolves, and no removed mechanism (`ErrStaleView`,
`event.Replay`, `Store.Codec()`, `event.Copy`, `FailKind`, `event.Since`, `Commit.Durability`,
`Authority.BoundTo`, `ErrBatch`, the store-side *freeze* wording) is described as live. §D.5's,
§UC-021's and §D.8's Go fragments still compile as written, and §D.8's new §UC-068 assertion is
correct against §D.0's own fold. The microkernel walk finds no value `eventpg` cannot construct, and
every fix proposed above leaves that true. The thirteen non-negotiables and the ten obligations all
have homes. No use case encodes one example aggregate as a mechanism.

### The direction the next reduction should look

Rounds 5 to 10 have now produced six findings of one shape, and the shape has narrowed: **the design
contracts the values that cross a boundary and does not contract the parties that hold state across
calls.** The store was made a party with obligations (GAP-68, GAP-87, GAP-94); the kernel was made
one (GAP-79); the caller was made one (GAP-93). The **codec** is still treated as a pure function in
every rule and as a stateful object in exactly one clause — row 1's "it may reuse an internal buffer
and cannot be asked to promise otherwise" — and both of this round's `high` findings about it
(GAP-100, GAP-108) follow from that asymmetry. The next revision should give the codec the same
treatment the store got: a named party, with what it may retain, what it may alias, and what may call
it concurrently, stated once and falsified by `eventtest.RoundTrip`.

## Carry into the plan

Each line is a constraint an implementer must satisfy and the test that catches its violation. The
first three block the gate.

1. **A decoded value must not alias memory its codec will reuse or write.** (GAP-100, `high`) The
   `Codec` contract states it; §INV-021's outbound enumeration either names the crossing or scopes
   itself away from it. *Test:* `eventtest.RoundTrip` decodes two payloads of one revision and
   asserts the first value is unchanged after the second decode — passing against `event.JSON`,
   failing against a deliberately scratch-reusing codec in the same test.
2. **A decorator's own refusal must be classified, and the document must decide what class a policy
   refusal is.** (GAP-101, `high`) An unclassified error from a wrapper's `Append` must not silently
   become `ErrUncertain`. *Test:* a conformance case drives a decorator that refuses before
   forwarding and asserts the chosen sentinel **and zero events**.
3. **`errors.Is(err, context.DeadlineExceeded)` must have one stated answer on an `ErrUncertain` from
   the commit window.** (GAP-102, `high`) *Test:* the `cancellation` section asserts both halves in
   both windows — the positive sentinel and the negative one.
4. **The store applies the two page bounds; somebody verifies them.** (GAP-103) *Test:* a `bounds`
   case, or an over-long-page defect in the self-check, or §UC-011's Observed narrowed in writing.
5. **`ErrTooLarge` on the load path and `ErrCursor` are classified by the rule §2.3 states, not by
   the site that raises them.** (GAP-104) *Test:* §INV-024's ground-truth table plus a transport-level
   assertion that a load of an oversized stored payload does not render as a client error.
6. **The inbound enumeration says what it covers and covers it.** (GAP-105) *Test:* the recomputed
   four/two/one count, and every citation of "the one row whose answer is not *read only*" checked
   against it.
7. **What a read returns is the recipient's including its capacity.** (GAP-106) *Test:* `append` one
   byte to page one's first payload and assert the second envelope's payload is unchanged.
8. **The suite has one defect count and one fixture inventory.** (GAP-107) *Test:* the count is equal
   in §UC-045, §UC-048 (twice), §INV-035, §UC-037 and §D.9, and every defect named there has a
   section it must fail.
9. **Every value the framework retains and calls is safe for concurrent use, and says so.**
   (GAP-108) *Test:* the `concurrency` section drives one section through a codec that would fail the
   obligation, or the document records the obligation as unpoliced with the reason.
10. **The six residuals of GAP-109**, each one clause, each with the check named in its close
    criteria — in particular §UC-005's exhaustive reader list, §UC-068's `Ledger` fold, and where
    `MaxKey` sits in `Append`'s order.
